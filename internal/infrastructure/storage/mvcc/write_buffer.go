// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import "sync"

// WriteOp represents the type of write operation in a WriteBuffer entry.
type WriteOp uint8

const (
	OpInsert WriteOp = iota // key does not exist in B+Tree (or Tombstone)
	OpUpdate                // key exists in B+Tree, updating value
	OpDelete                // marking key for deletion (Tombstone)
)

// WriteEntry represents a single write operation in the WriteBuffer.
type WriteEntry struct {
	Op         WriteOp
	Value      []byte // new value (nil for Delete)
	OldValue   []byte // B+Tree old value at first Put time (deepCopy'd), nil for OpInsert
	OldFlag    byte   // B+Tree old flag at first Put time (0 for OpInsert)
	OldBeginTS uint64 // B+Tree old beginTS at first Put time (0 for OpInsert)

	// OldPrev* capture the version that will be DROPPED when this write commits.
	// When the new value is encoded, the old current version becomes prev,
	// and the old prev version is dropped. If it was a LOB, its resources
	// must be retired via epoch GC.
	OldPrevFlag byte   // B+Tree old prev flag at first Put time (0 for first version)
	OldPrevVal  []byte // B+Tree old prev value at first Put time (deepCopy'd), nil if no prev
}

// WriteBuffer is a per-transaction write buffer. NOT thread-safe.
// All operations must be called from the transaction's owning goroutine.
type WriteBuffer struct {
	entries map[string]WriteEntry // key → entry
	ordered []string              // insertion order
}

// NewWriteBuffer creates an empty WriteBuffer.
func NewWriteBuffer() *WriteBuffer {
	return &WriteBuffer{
		entries: make(map[string]WriteEntry),
	}
}

// wbPool pools WriteBuffer objects to reduce GC pressure from per-transaction map allocations.
// After Commit/Rollback, the buffer is Reset and returned to the pool for reuse.
var wbPool = sync.Pool{
	New: func() any {
		return &WriteBuffer{
			entries: make(map[string]WriteEntry, 64),
			ordered: make([]string, 0, 64),
		}
	},
}

// getWriteBuffer returns a pooled WriteBuffer.
func getWriteBuffer() *WriteBuffer {
	return wbPool.Get().(*WriteBuffer)
}

// putWriteBuffer resets wb and returns it to the pool.
func putWriteBuffer(wb *WriteBuffer) {
	if wb == nil {
		return
	}
	wb.Reset()
	wbPool.Put(wb)
}

// Reset clears the WriteBuffer for reuse while preserving underlying map/slice capacity.
func (wb *WriteBuffer) Reset() {
	for k := range wb.entries {
		delete(wb.entries, k)
	}
	wb.ordered = wb.ordered[:0]
}

// Put records a write operation for the given key.
// btreeOldValue/btreeOldFlag/btreeOldBeginTS are the B+Tree state at the time of first Put.
// For subsequent Puts on the same key, only Value is updated; OldValue/OldFlag/OldBeginTS are preserved.
func (wb *WriteBuffer) Put(key string, value []byte, btreeOldValue []byte, btreeOldFlag byte, btreeOldBeginTS uint64, oldPrevFlag byte, oldPrevVal []byte) {
	existing, has := wb.entries[key]
	if !has {
		// First write to this key: determine Op based on B+Tree state
		if btreeOldValue == nil {
			wb.entries[key] = WriteEntry{Op: OpInsert, Value: value}
		} else {
			wb.entries[key] = WriteEntry{
				Op:          OpUpdate,
				Value:       value,
				OldValue:    deepCopy(btreeOldValue),
				OldFlag:     btreeOldFlag,
				OldBeginTS:  btreeOldBeginTS,
				OldPrevFlag: oldPrevFlag,
				OldPrevVal:  deepCopy(oldPrevVal),
			}
		}
		wb.ordered = append(wb.ordered, key)
	} else {
		// Subsequent write: only update Value, preserve original OldValue/OldFlag/OldBeginTS
		existing.Value = value
		// If previously deleted, restore to Insert/Update
		if existing.Op == OpDelete {
			if existing.OldValue == nil {
				existing.Op = OpInsert
			} else {
				existing.Op = OpUpdate
			}
		}
		wb.entries[key] = existing
	}
}

// Delete records a delete operation for the given key.
// Returns ErrKeyNotFound if the key does not exist in either WriteBuffer or B+Tree.
func (wb *WriteBuffer) Delete(key string, btreeOldValue []byte, btreeOldFlag byte, btreeOldBeginTS uint64, oldPrevFlag byte, oldPrevVal []byte) error {
	existing, has := wb.entries[key]
	if !has {
		// Key not in WriteBuffer: check B+Tree state
		if btreeOldValue == nil {
			return ErrKeyNotFound
		}
		wb.entries[key] = WriteEntry{
			Op:          OpDelete,
			OldValue:    deepCopy(btreeOldValue),
			OldFlag:     btreeOldFlag,
			OldBeginTS:  btreeOldBeginTS,
			OldPrevFlag: oldPrevFlag,
			OldPrevVal:  deepCopy(oldPrevVal),
		}
		wb.ordered = append(wb.ordered, key)
	} else {
		// Key already in WriteBuffer
		if existing.Op == OpInsert {
			// Insert→Delete: cancel the insert (remove from buffer)
			delete(wb.entries, key)
			return nil
		}
		// Update→Delete or Delete→Delete: mark as OpDelete
		existing.Op = OpDelete
		existing.Value = nil
		wb.entries[key] = existing
	}
	return nil
}

// Get returns the WriteEntry for the given key, or reports whether it exists.
func (wb *WriteBuffer) Get(key string) (WriteEntry, bool) {
	entry, ok := wb.entries[key]
	return entry, ok
}

// OrderedKeys returns all keys in insertion order.
// Deleted entries (Insert→Delete removed from entries) are still present in ordered
// but will not appear in entries; callers must check existence.
func (wb *WriteBuffer) OrderedKeys() []string {
	return wb.ordered
}

// Len returns the number of active entries in the buffer.
func (wb *WriteBuffer) Len() int {
	return len(wb.entries)
}
