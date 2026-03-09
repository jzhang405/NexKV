// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWAL_New tests creating a new WAL.
func TestWAL_New(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)
	defer wal.Close()

	assert.NotNil(t, wal)
	assert.Equal(t, path, wal.path)
	assert.False(t, wal.closed.Load())
}

// TestWAL_WriteAndReplay tests writing and replaying WAL entries.
func TestWAL_WriteAndReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)
	defer wal.Close()

	// Write some entries
	entries := []*WALEntry{
		NewInsertEntry([]byte("key1"), []byte("value1")),
		NewInsertEntry([]byte("key2"), []byte("value2")),
		NewInsertEntry([]byte("key3"), []byte("value3")),
	}

	for _, entry := range entries {
		err = wal.Write(entry)
		require.NoError(t, err)
	}

	// Close and reopen
	err = wal.Close()
	require.NoError(t, err)

	wal2, err := NewWAL(path)
	require.NoError(t, err)
	defer wal2.Close()

	// Replay entries
	replayed := make([]*WALEntry, 0)
	count, err := wal2.Replay(func(entry *WALEntry) error {
		replayed = append(replayed, entry)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, count)
	assert.Equal(t, 3, len(replayed))

	// Verify entries
	for i, entry := range replayed {
		assert.Equal(t, entries[i].Type, entry.Type)
		assert.Equal(t, entries[i].Key, entry.Key)
		assert.Equal(t, entries[i].Value, entry.Value)
	}
}

// TestWAL_ReplayApply tests replaying entries with application logic.
func TestWAL_ReplayApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)

	// Write entries
	err = wal.Write(NewInsertEntry([]byte("key1"), []byte("value1")))
	require.NoError(t, err)
	err = wal.Write(NewInsertEntry([]byte("key2"), []byte("value2")))
	require.NoError(t, err)

	// Replay and apply to a map
	data := make(map[string][]byte)
	count, err := wal.Replay(func(entry *WALEntry) error {
		if entry.Type == WALEntryTypeInsert {
			data[string(entry.Key)] = entry.Value
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Equal(t, []byte("value1"), data["key1"])
	assert.Equal(t, []byte("value2"), data["key2"])

	wal.Close()
}

// TestWAL_Truncate tests truncating the WAL.
func TestWAL_Truncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)

	// Write some entries
	err = wal.Write(NewInsertEntry([]byte("key1"), []byte("value1")))
	require.NoError(t, err)
	err = wal.Write(NewInsertEntry([]byte("key2"), []byte("value2")))
	require.NoError(t, err)

	// Truncate
	err = wal.Truncate()
	require.NoError(t, err)

	// Replay should return 0 entries
	count, err := wal.Replay(func(entry *WALEntry) error {
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 0, count)

	wal.Close()
}

// TestWAL_Checksum tests checksum verification.
func TestWAL_Checksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)
	defer wal.Close()

	// Write an entry
	entry := NewInsertEntry([]byte("key1"), []byte("value1"))
	err = wal.Write(entry)
	require.NoError(t, err)

	// Close and corrupt the file
	err = wal.Close()
	require.NoError(t, err)

	// Read file and corrupt checksum
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// Modify last 4 bytes (checksum)
	if len(data) >= 4 {
		data[len(data)-4] = 0xFF
		data[len(data)-3] = 0xFF
		data[len(data)-2] = 0xFF
		data[len(data)-1] = 0xFF
	}

	err = os.WriteFile(path, data, 0644)
	require.NoError(t, err)

	// Reopen and replay - should detect checksum mismatch
	wal2, err := NewWAL(path)
	require.NoError(t, err)
	defer wal2.Close()

	_, err = wal2.Replay(func(entry *WALEntry) error {
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

// TestWAL_LargeEntry tests writing large entries.
func TestWAL_LargeEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)
	defer wal.Close()

	// Create large key and value (1KB each)
	largeKey := make([]byte, 1024)
	largeValue := make([]byte, 1024)
	for i := range largeKey {
		largeKey[i] = byte(i % 256)
		largeValue[i] = byte((i + 128) % 256)
	}

	entry := NewInsertEntry(largeKey, largeValue)
	err = wal.Write(entry)
	require.NoError(t, err)

	// Replay and verify
	replayedCount := 0
	_, err = wal.Replay(func(replayedEntry *WALEntry) error {
		replayedCount++
		assert.Equal(t, largeKey, replayedEntry.Key)
		assert.Equal(t, largeValue, replayedEntry.Value)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, replayedCount)
}

// TestWAL_DeleteEntry tests delete entries.
func TestWAL_DeleteEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)
	defer wal.Close()

	// Write insert and delete entries
	err = wal.Write(NewInsertEntry([]byte("key1"), []byte("value1")))
	require.NoError(t, err)
	err = wal.Write(NewDeleteEntry([]byte("key1")))
	require.NoError(t, err)

	// Replay and verify
	var insertCount, deleteCount int
	_, err = wal.Replay(func(entry *WALEntry) error {
		if entry.Type == WALEntryTypeInsert {
			insertCount++
		} else if entry.Type == WALEntryTypeDelete {
			deleteCount++
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, insertCount)
	assert.Equal(t, 1, deleteCount)
}

// TestWAL_MultipleReplay tests multiple replays.
func TestWAL_MultipleReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)

	// Write entries
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 10)}
		err = wal.Write(NewInsertEntry(key, value))
		require.NoError(t, err)
	}

	// Replay multiple times
	for round := 0; round < 3; round++ {
		count := 0
		_, err := wal.Replay(func(entry *WALEntry) error {
			count++
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 10, count, "Round %d", round)
	}

	wal.Close()
}

// TestWAL_ConcurrentWrite tests concurrent writes.
func TestWAL_ConcurrentWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent test in short mode")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(path)
	require.NoError(t, err)
	defer wal.Close()

	const numGoroutines = 10
	const entriesPerGoroutine = 100

	done := make(chan bool, numGoroutines)

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			for j := 0; j < entriesPerGoroutine; j++ {
				key := []byte{byte(id), byte(j)}
				value := []byte{byte(j)}
				entry := NewInsertEntry(key, value)

				err := wal.Write(entry)
				if err != nil {
					t.Errorf("Write failed: %v", err)
					return
				}
			}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify all entries were written
	count, err := wal.Replay(func(entry *WALEntry) error {
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, numGoroutines*entriesPerGoroutine, count)
}

// BenchmarkWAL_Write benchmarks WAL write performance.
func BenchmarkWAL_Write(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")

	wal, err := NewWAL(path)
	require.NoError(b, err)
	defer wal.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i % 256)}
		entry := NewInsertEntry(key, value)
		_ = wal.Write(entry)
	}
}

// BenchmarkWAL_Replay benchmarks WAL replay performance.
func BenchmarkWAL_Replay(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")

	// Pre-write entries
	wal, err := NewWAL(path)
	require.NoError(b, err)

	numEntries := 10000
	for i := 0; i < numEntries; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i % 256)}
		entry := NewInsertEntry(key, value)
		_ = wal.Write(entry)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := wal.Replay(func(entry *WALEntry) error {
			return nil
		})
		if err != nil {
			b.Fatalf("Replay failed: %v", err)
		}
		if count != numEntries {
			b.Fatalf("Expected %d entries, got %d", numEntries, count)
		}
	}
}
