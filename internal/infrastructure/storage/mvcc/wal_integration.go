// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"encoding/binary"
	"fmt"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"sort"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// WriteBufferSnapshot is an immutable snapshot of the write buffer for async apply.
type WriteBufferSnapshot struct {
	Entries map[string]WriteEntry
	Ordered []string
}

// Keys returns all active keys in the write buffer.
func (wb *WriteBuffer) Keys() []string {
	keys := make([]string, 0, len(wb.entries))
	for k := range wb.entries {
		keys = append(keys, k)
	}
	return keys
}

// Snapshot creates an independent copy of the write buffer for async apply.
func (wb *WriteBuffer) Snapshot() *WriteBufferSnapshot {
	entries := make(map[string]WriteEntry, len(wb.entries))
	for k, v := range wb.entries {
		e := v
		if e.Value != nil {
			e.Value = deepCopy(e.Value)
		}
		if e.OldValue != nil {
			e.OldValue = deepCopy(e.OldValue)
		}
		entries[k] = e
	}
	ordered := make([]string, len(wb.ordered))
	copy(ordered, wb.ordered)
	return &WriteBufferSnapshot{Entries: entries, Ordered: ordered}
}

// ToWALEntries converts the write buffer to WAL entries with the given commitTS.
// The last entry is a Commit marker with commitTS encoded in the Key field.
func (wb *WriteBuffer) ToWALEntries(commitTS uint64) []*service.WALEntry {
	entries := wb.WriteEntries()
	entries = append(entries, CommitEntry(commitTS))
	return entries
}

// WriteEntries returns the WAL entries for all keys in the write buffer,
// WITHOUT a trailing Commit marker. Caller must append CommitEntry separately
// after commitTS is allocated (Phase 3.2: commitTS after WAL sync).
func (wb *WriteBuffer) WriteEntries() []*service.WALEntry {
	keys := wb.OrderedKeys()
	entries := make([]*service.WALEntry, 0, len(keys))

	for _, key := range keys {
		e, ok := wb.entries[key]
		if !ok {
			continue
		}
		var walType service.WALType
		switch e.Op {
		case OpInsert:
			walType = service.WALTypeInsert
		case OpUpdate:
			walType = service.WALTypeUpdate
		case OpDelete:
			walType = service.WALTypeDelete
		}
		entries = append(entries, &service.WALEntry{
			Type:  walType,
			Key:   []byte(key),
			Value: e.Value,
		})
	}

	return entries
}

// CommitEntry creates a WAL Commit marker entry with commitTS in the Key field.
func CommitEntry(commitTS uint64) *service.WALEntry {
	commitKey := make([]byte, 8)
	binary.BigEndian.PutUint64(commitKey, commitTS)
	return &service.WALEntry{
		Type: service.WALTypeCommit,
		Key:  commitKey,
	}
}

// TxPrepareEntry creates a Phase 3.3 TxPrepare WAL entry containing oldValue snapshots
// for all keys in the write buffer. This entry is written BEFORE commitTS allocation and
// BTree Apply. On recovery, if TxPrepare exists without a matching TxCommit, the oldValues
// are used to rollback any partially-applied keys.
//
// WAL entry format (Phase 3.3):
//
//	Key:   [txID:8 BE]
//	Value: [keyCount:4 BE]
//	       [(keyLen:4 BE)(key:keyLen)(op:1)(oldFlag:1)(oldBeginTS:8)(oldValLen:4 BE)(oldVal:oldValLen)(newFlag:1)(newValLen:4 BE)(newVal:newValLen)] × N
func TxPrepareEntry(txID uint64, wb *WriteBuffer) *service.WALEntry {
	keys := wb.OrderedKeys()
	sort.Strings(keys) // deterministic order

	// Calculate total value size
	totalSize := 4 // keyCount
	for _, key := range keys {
		e, ok := wb.entries[key]
		if !ok {
			continue
		}
		// keyLen(4) + key + op(1) + oldFlag(1) + oldBeginTS(8) + oldValLen(4) + oldVal + newFlag(1) + newValLen(4) + newVal
		totalSize += 4 + len(key) + 1 + 1 + 8 + 4 + len(e.OldValue) + 1 + 4 + len(e.Value)
	}

	buf := make([]byte, totalSize)
	offset := 0

	// keyCount
	binary.BigEndian.PutUint32(buf[offset:], uint32(len(keys)))
	offset += 4

	for _, key := range keys {
		e, ok := wb.entries[key]
		if !ok {
			continue
		}

		// keyLen + key
		binary.BigEndian.PutUint32(buf[offset:], uint32(len(key)))
		offset += 4
		copy(buf[offset:], key)
		offset += len(key)

		// op
		buf[offset] = byte(e.Op)
		offset++

		// oldFlag
		buf[offset] = e.OldFlag
		offset++

		// oldBeginTS
		binary.BigEndian.PutUint64(buf[offset:], e.OldBeginTS)
		offset += 8

		// oldValLen + oldVal
		binary.BigEndian.PutUint32(buf[offset:], uint32(len(e.OldValue)))
		offset += 4
		copy(buf[offset:], e.OldValue)
		offset += len(e.OldValue)

		// newFlag (determined by Op)
		newFlag := byte(FlagNormal)
		if e.Op == OpDelete {
			newFlag = FlagTombstone
		}
		buf[offset] = newFlag
		offset++

		// newValLen + newVal
		binary.BigEndian.PutUint32(buf[offset:], uint32(len(e.Value)))
		offset += 4
		copy(buf[offset:], e.Value)
		offset += len(e.Value)
	}

	txIDKey := make([]byte, 8)
	binary.BigEndian.PutUint64(txIDKey, txID)

	return &service.WALEntry{
		Type:  service.WALTypeTxPrepare,
		Key:   txIDKey,
		Value: buf,
	}
}

// ParseTxPrepareEntry parses a TxPrepare WAL entry back into a list of key/value pairs.
// Returns the txID, and a slice of parsed entries for recovery rollback/replay.
type TxPrepareParsedEntry struct {
	Key        string
	Op         WriteOp
	OldFlag    byte
	OldBeginTS uint64
	OldValue   []byte
	NewFlag    byte
	NewValue   []byte
}

func ParseTxPrepareEntry(e *service.WALEntry) (txID uint64, entries []TxPrepareParsedEntry, err error) {
	if e.Type != service.WALTypeTxPrepare {
		return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, "not a TxPrepare entry")
	}
	if len(e.Key) < 8 {
		return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, "TxPrepare: key too short")
	}
	txID = binary.BigEndian.Uint64(e.Key[0:8])

	buf := e.Value
	if len(buf) < 4 {
		return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, "TxPrepare: value too short")
	}
	keyCount := binary.BigEndian.Uint32(buf[0:4])
	offset := 4

	for i := uint32(0); i < keyCount; i++ {
		if offset+4 > len(buf) {
			return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, fmt.Sprintf("TxPrepare: truncated at key %d", i))
		}
		keyLen := binary.BigEndian.Uint32(buf[offset:])
		offset += 4
		if offset+int(keyLen) > len(buf) {
			return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, "TxPrepare: truncated key data")
		}
		key := string(buf[offset : offset+int(keyLen)])
		offset += int(keyLen)

		if offset+12 > len(buf) {
			return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, "TxPrepare: truncated at entry header")
		}
		op := WriteOp(buf[offset])
		offset++
		oldFlag := buf[offset]
		offset++
		oldBeginTS := binary.BigEndian.Uint64(buf[offset:])
		offset += 8
		oldValLen := binary.BigEndian.Uint32(buf[offset:])
		offset += 4
		if offset+int(oldValLen) > len(buf) {
			return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, "TxPrepare: truncated oldVal")
		}
		oldVal := make([]byte, oldValLen)
		copy(oldVal, buf[offset:offset+int(oldValLen)])
		offset += int(oldValLen)

		if offset+5 > len(buf) {
			return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, "TxPrepare: truncated at newVal header")
		}
		newFlag := buf[offset]
		offset++
		newValLen := binary.BigEndian.Uint32(buf[offset:])
		offset += 4
		if offset+int(newValLen) > len(buf) {
			return 0, nil, errpkg.Wrap(errpkg.ErrMVCCTxPrepareCorrupted, "TxPrepare: truncated newVal")
		}
		newVal := make([]byte, newValLen)
		copy(newVal, buf[offset:offset+int(newValLen)])
		offset += int(newValLen)

		entries = append(entries, TxPrepareParsedEntry{
			Key: key, Op: op,
			OldFlag: oldFlag, OldBeginTS: oldBeginTS, OldValue: oldVal,
			NewFlag: newFlag, NewValue: newVal,
		})
	}

	return txID, entries, nil
}

// OrderedKeys returns keys from the snapshot in insertion order.
func (s *WriteBufferSnapshot) OrderedKeys() []string {
	return s.Ordered
}
