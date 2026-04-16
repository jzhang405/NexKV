// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package mvcc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// mockStorage implements StorageBackend for testing.
// Thread-safe via sync.Mutex.
type mockStorage struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMockStorage() *mockStorage {
	return &mockStorage{data: make(map[string][]byte)}
}

func (m *mockStorage) GetRaw(_ context.Context, key []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	val, ok := m.data[string(key)]
	if !ok {
		return nil, ErrKeyNotFound
	}
	result := make([]byte, len(val))
	copy(result, val)
	return result, nil
}

func (m *mockStorage) Set(_ context.Context, key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[string(key)] = cp
	return nil
}

func (m *mockStorage) Delete(_ context.Context, key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, string(key))
	return nil
}

func (m *mockStorage) rawSet(key string, flag byte, ts uint64, val []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	encoded, _ := BuildMVCC(flag, ts, val)
	m.data[key] = encoded
}

func newTestTxManager(storage *mockStorage) TxManager {
	return NewTxManager(storage, NewLocalTS())
}

// ---------------------------------------------------------------------------
// Basic Put + Commit + Get
// ---------------------------------------------------------------------------

func TestSnapshotTx_Basic_PutGet(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	if err := tx.Put([]byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// New transaction should see the committed value
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	val, err := tx2.Get(context.Background(), []byte("k1"))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "v1" {
		t.Fatalf("expected v1, got %s", val)
	}
	tx2.Rollback()
}

// ---------------------------------------------------------------------------
// Snapshot read isolation
// ---------------------------------------------------------------------------

func TestSnapshotTx_SnapshotRead(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// TX1 starts and reads k1
	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)

	// TX2 commits a new value for k1
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2.Put([]byte("k1"), []byte("v2"))
	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("TX2 Commit failed: %v", err)
	}

	// TX1 should see k1 as not existing (snapshot before TX2)
	_, err := tx1.Get(context.Background(), []byte("k1"))
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound for snapshot read, got val=%v err=%v", err, err)
	}
	tx1.Rollback()
}

// ---------------------------------------------------------------------------
// Read-your-own-writes
// ---------------------------------------------------------------------------

func TestSnapshotTx_ReadYourOwnWrites(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx.Put([]byte("k1"), []byte("v1"))

	val, err := tx.Get(context.Background(), []byte("k1"))
	if err != nil {
		t.Fatalf("Get own write failed: %v", err)
	}
	if string(val) != "v1" {
		t.Fatalf("expected v1, got %s", val)
	}
	tx.Rollback()
}

// ---------------------------------------------------------------------------
// Conflict detection
// ---------------------------------------------------------------------------

func TestSnapshotTx_ConflictDetection(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// Pre-populate k1
	storage.rawSet("k1", FlagNormal, 1, []byte("original"))

	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)

	// TX1 reads and writes k1
	tx1.Put([]byte("k1"), []byte("from_tx1"))

	// TX2 also writes k1 (conflicting write)
	tx2.Put([]byte("k1"), []byte("from_tx2"))

	// TX1 commits first
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatalf("TX1 Commit failed: %v", err)
	}

	// TX2 commits → should conflict (TX1 changed beginTS)
	err := tx2.Commit(context.Background())
	if err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tombstone in snapshot
// ---------------------------------------------------------------------------

func TestSnapshotTx_TombstoneInSnapshot(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// Pre-populate k1
	storage.rawSet("k1", FlagNormal, 1, []byte("v1"))

	tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx.Delete([]byte("k1"))
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit delete failed: %v", err)
	}

	// New transaction should not see k1
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	_, err := tx2.Get(context.Background(), []byte("k1"))
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound after delete, got %v", err)
	}
	tx2.Rollback()
}

// ---------------------------------------------------------------------------
// Tombstone recovery
// ---------------------------------------------------------------------------

func TestSnapshotTx_TombstoneRecovery(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// Pre-populate then delete
	storage.rawSet("k1", FlagNormal, 1, []byte("v1"))

	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx1.Delete([]byte("k1"))
	tx1.Commit(context.Background())

	// Re-insert
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2.Put([]byte("k1"), []byte("v2"))
	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("Commit re-insert failed: %v", err)
	}

	// Verify
	tx3, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	val, err := tx3.Get(context.Background(), []byte("k1"))
	if err != nil {
		t.Fatalf("Get after recovery failed: %v", err)
	}
	if string(val) != "v2" {
		t.Fatalf("expected v2, got %s", val)
	}
	tx3.Rollback()
}

// ---------------------------------------------------------------------------
// Rollback
// ---------------------------------------------------------------------------

func TestSnapshotTx_Rollback(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx.Put([]byte("k1"), []byte("v1"))
	tx.Rollback()

	// k1 should not exist
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	_, err := tx2.Get(context.Background(), []byte("k1"))
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound after rollback, got %v", err)
	}
	tx2.Rollback()
}

// ---------------------------------------------------------------------------
// Partial rollback (multi-key commit failure)
// ---------------------------------------------------------------------------

func TestSnapshotTx_PartialRollback(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// Pre-populate k1 and k2
	storage.rawSet("k1", FlagNormal, 1, []byte("v1"))
	storage.rawSet("k2", FlagNormal, 1, []byte("v2"))

	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx1.Put([]byte("k1"), []byte("new1"))
	tx1.Put([]byte("k2"), []byte("new2"))

	// Concurrently modify k1 to cause TX1 conflict on second key
	// (after TX1 commits first key, conflict on second key triggers rollback)
	// Actually, let's use a simpler approach: have TX2 modify k1 before TX1 commits

	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2.Put([]byte("k1"), []byte("concurrent"))
	_ = tx2.Commit(context.Background())

	err := tx1.Commit(context.Background())
	if err == nil {
		t.Fatal("expected conflict error")
	}

	// k1 should be TX2's value, k2 should be unchanged
	tx3, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	val1, _ := tx3.Get(context.Background(), []byte("k1"))
	if string(val1) != "concurrent" {
		t.Fatalf("expected k1=concurrent after rollback, got %s", val1)
	}
	val2, _ := tx3.Get(context.Background(), []byte("k2"))
	if string(val2) != "v2" {
		t.Fatalf("expected k2=v2 (unchanged), got %s", val2)
	}
	tx3.Rollback()
}

// ---------------------------------------------------------------------------
// Version chain traversal
// ---------------------------------------------------------------------------

func TestSnapshotTx_VersionChainTraversal(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// Multiple updates to k1
	for i := 0; i < 5; i++ {
		tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
		tx.Put([]byte("k1"), []byte(fmt.Sprintf("v%d", i)))
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("Commit %d failed: %v", i, err)
		}
	}

	// Latest should be v4
	tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	val, err := tx.Get(context.Background(), []byte("k1"))
	if err != nil {
		t.Fatalf("Get latest failed: %v", err)
	}
	if string(val) != "v4" {
		t.Fatalf("expected v4, got %s", val)
	}
	tx.Rollback()
}

// ---------------------------------------------------------------------------
// Insert version chain (non-nil value)
// ---------------------------------------------------------------------------

func TestSnapshotTx_InsertVersionChain(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// TX1 inserts k1
	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx1.Put([]byte("k1"), []byte("v1"))
	_ = tx1.Commit(context.Background())

	// After Insert only, version chain exists but may be empty
	// (Insert does NOT create a version chain node)

	// TX2 updates k1 — this creates a version chain node storing v1 as old value
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2.Put([]byte("k1"), []byte("v2"))
	_ = tx2.Commit(context.Background())

	// Verify version chain was built by Update
	chain := tm.(*txManager).versionStore.Load("k1")
	if chain == nil {
		t.Fatal("expected version chain for k1")
	}
	node := chain.Load()
	foundNormalNode := false
	for node != nil {
		if node.flag == FlagNormal {
			foundNormalNode = true
			break
		}
		node = node.next
	}
	if !foundNormalNode {
		t.Fatal("expected at least one Normal node in version chain (from Update)")
	}
}

// ---------------------------------------------------------------------------
// Concurrent writes to different keys should not conflict
// ---------------------------------------------------------------------------

func TestSnapshotTx_ConcurrentWriteDifferentKeys(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	const numTx = 10
	var wg sync.WaitGroup
	var errors atomic.Int64

	for i := 0; i < numTx; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
			key := []byte(fmt.Sprintf("key%d", idx))
			tx.Put(key, []byte(fmt.Sprintf("val%d", idx)))
			if err := tx.Commit(context.Background()); err != nil {
				errors.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if errors.Load() > 0 {
		t.Fatalf("expected no conflicts for different keys, got %d errors", errors.Load())
	}
}
