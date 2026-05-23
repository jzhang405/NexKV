// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"encoding/binary"

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

// OrderedKeys returns keys from the snapshot in insertion order.
func (s *WriteBufferSnapshot) OrderedKeys() []string {
	return s.Ordered
}
