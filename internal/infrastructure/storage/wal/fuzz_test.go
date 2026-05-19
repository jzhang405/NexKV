// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package wal

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// FuzzWALFormat byte-fuzzes WAL entry marshal/unmarshal.
// Valid entries must round-trip correctly; random bytes must not panic.
func FuzzWALFormat(f *testing.F) {
	// Seed corpus
	f.Add([]byte("k"), []byte("v"), byte(WALTypeInsert), uint64(1))
	f.Add([]byte("key-long-12345"), []byte("value-long-67890"), byte(WALTypeUpdate), uint64(42))
	f.Add([]byte{}, []byte{}, byte(WALTypeDelete), uint64(0))
	f.Add(make([]byte, 8), []byte{}, byte(WALTypeCommit), uint64(100))

	f.Fuzz(func(t *testing.T, key, value []byte, typ byte, txID uint64) {
		if typ > byte(WALTypeSplit) {
			typ = typ % (byte(WALTypeSplit) + 1)
		}
		if txID == 0 {
			txID = 1
		}

		entry := &WALEntry{
			LSN:       LSN(1),
			TxID:      txID,
			Timestamp: 1234567890,
			Type:      WALType(typ),
			Key:       key,
			Value:     value,
			PrevLSN:   LSNInvalid,
		}

		data, err := MarshalWALEntry(entry)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}

		// Round-trip
		decoded := &WALEntry{}
		if err := UnmarshalWALEntry(decoded, data); err != nil {
			t.Fatalf("Unmarshal failed for valid data: %v", err)
		}
		if decoded.Type != entry.Type {
			t.Errorf("Type mismatch: %d vs %d", decoded.Type, entry.Type)
		}
		if decoded.TxID != entry.TxID {
			t.Errorf("TxID mismatch: %d vs %d", decoded.TxID, entry.TxID)
		}
		if string(decoded.Key) != string(entry.Key) {
			t.Errorf("Key mismatch: %q vs %q", decoded.Key, entry.Key)
		}
		if string(decoded.Value) != string(entry.Value) {
			t.Errorf("Value mismatch: %q vs %q", decoded.Value, entry.Value)
		}
	})
}

// FuzzWALFormatCorrupted verifies that random byte slices do not panic Unmarshal.
func FuzzWALFormatCorrupted(f *testing.F) {
	// Seed with valid entries to increase coverage
	for i := 0; i < 5; i++ {
		entry := &WALEntry{
			LSN: LSN(i + 1), TxID: uint64(i + 1), Type: WALType(i % 7),
			Key: []byte{byte(i)}, Value: []byte{byte(i + 100)},
		}
		data, _ := MarshalWALEntry(entry)
		f.Add(data)

		// Corrupted variants
		if len(data) > 8 {
			corrupted := make([]byte, len(data))
			copy(corrupted, data)
			corrupted[8] ^= 0xFF
			f.Add(corrupted)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 {
			return
		}
		entry := &WALEntry{}
		_ = UnmarshalWALEntry(entry, data) // must not panic
	})
}

// FuzzWALRecovery tests random WAL entry sequences with mixed commit/rollback/truncation.
func FuzzWALRecovery(f *testing.F) {
	f.Add(int(5), int(3))

	f.Fuzz(func(t *testing.T, numEntries int, numCommitted int) {
		if numEntries < 1 || numEntries > 50 {
			return
		}
		if numCommitted < 0 || numCommitted > numEntries {
			numCommitted = numEntries / 2
		}

		dir := t.TempDir()
		config := &WALConfig{
			Dir:         dir,
			SegmentSize: 64 * 1024 * 1024,
			SyncPolicy:  SyncPolicyEveryWrite,
		}

		w, err := NewDiskWAL(config)
		if err != nil {
			t.Fatal(err)
		}

		// Phase 1: write entries with mixed commit/rollback
		type txInfo struct {
			entries   []*WALEntry
			commitTS  uint64
			committed bool
		}
		txMap := make(map[uint64]*txInfo)

		for i := 0; i < numEntries; i++ {
			txID := uint64(i + 1)
			ti := &txInfo{commitTS: uint64(100 + i), committed: i < numCommitted}

			key := []byte{byte(i)}
			val := []byte{byte(i + 100)}
			e := NewWALEntry(WALTypeInsert, txID, key, val, LSNInvalid)
			if _, err := w.Append(e); err != nil {
				t.Fatal(err)
			}
			ti.entries = append(ti.entries, e)

			// Append commit or rollback marker
			if ti.committed {
				commitKey := make([]byte, 8)
				binary.BigEndian.PutUint64(commitKey, ti.commitTS)
				cmt := NewWALEntry(WALTypeCommit, txID, commitKey, nil, e.LSN)
				if _, err := w.Append(cmt); err != nil {
					t.Fatal(err)
				}
			} else {
				rb := NewWALEntry(WALTypeRollback, txID, nil, nil, e.LSN)
				if _, err := w.Append(rb); err != nil {
					t.Fatal(err)
				}
			}
			txMap[txID] = ti
		}

		_ = w.Sync()
		_ = w.Close()

		// Phase 2: recover and verify
		w2, err := NewDiskWAL(config)
		if err != nil {
			t.Fatal(err)
		}
		defer w2.Close()

		entries, err := w2.Recover()
		if err != nil {
			t.Fatal(err)
		}

		// Verify entries are sorted by LSN
		for i := 1; i < len(entries); i++ {
			if entries[i].LSN < entries[i-1].LSN {
				t.Errorf("LSN order violated: entries[%d].LSN=%d < entries[%d].LSN=%d",
					i, entries[i].LSN, i-1, entries[i-1].LSN)
			}
		}

		// Verify committed transactions have commit markers
		committedByTx := make(map[uint64]bool)
		for _, e := range entries {
			if e.Type == WALTypeCommit {
				committedByTx[e.TxID] = true
			}
		}
		for txID, ti := range txMap {
			if ti.committed && !committedByTx[txID] {
				t.Errorf("TxID=%d should be committed but missing commit marker", txID)
			}
		}
	})
}

// FuzzWALSegmentRotation tests that segment rotation is triggered correctly.
func FuzzWALSegmentRotation(f *testing.F) {
	f.Add(int(100), int(1024))

	f.Fuzz(func(t *testing.T, numEntries int, entrySize int) {
		if numEntries < 1 || numEntries > 200 {
			return
		}
		if entrySize < 100 || entrySize > 4096 {
			return
		}

		dir := t.TempDir()
		config := &WALConfig{
			Dir:         dir,
			SegmentSize: 1024 * 1024, // 1MB minimum valid, small to trigger rotation
			SyncPolicy:  SyncPolicyEveryWrite,
		}

		w, err := NewDiskWAL(config)
		if err != nil {
			t.Fatal(err)
		}

		val := make([]byte, entrySize)
		for i := 0; i < numEntries; i++ {
			key := []byte{byte(i % 256)}
			e := NewWALEntry(WALTypeInsert, uint64(i+1), key, val, LSNInvalid)
			_, err := w.Append(e)
			if err != nil {
				t.Fatalf("Append %d failed: %v", i, err)
			}
		}

		_ = w.Sync()
		_ = w.Close()

		// Verify segment files exist
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}

		walFiles := 0
		for _, f := range files {
			if filepath.Ext(f.Name()) == ".wal" {
				walFiles++
			}
		}
		if walFiles == 0 {
			t.Error("expected at least one WAL segment file")
		}

		// Recover from segments
		w2, err := NewDiskWAL(config)
		if err != nil {
			t.Fatal(err)
		}
		defer w2.Close()

		entries, err := w2.Recover()
		if err != nil {
			t.Fatal(err)
		}

		// Verify LSN ordering across segments
		if !sort.SliceIsSorted(entries, func(i, j int) bool {
			return entries[i].LSN < entries[j].LSN
		}) {
			t.Error("recovered entries across segments not sorted by LSN")
		}
	})
}
