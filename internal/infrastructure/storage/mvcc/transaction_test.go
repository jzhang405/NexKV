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

func (m *mockStorage) SetBatch(_ context.Context, pairs []KVPair) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range pairs {
		cp := make([]byte, len(p.Value))
		copy(cp, p.Value)
		m.data[string(p.Key)] = cp
	}
	return len(pairs), nil
}

func (m *mockStorage) rawSet(key string, flag byte, ts uint64, val []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	encoded, _ := BuildMVCC(flag, ts, val, 0, 0, nil)
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

	// TX1 acquires lock on k1 at Put, then commits
	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx1.Put([]byte("k1"), []byte("from_tx1"))
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatalf("TX1 Commit failed: %v", err)
	}

	// TX2 acquires lock on k1 (released after TX1 commit), sees TX1's new value
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2.Put([]byte("k1"), []byte("from_tx2"))
	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("TX2 Commit failed: %v", err)
	}

	// Verify: last write wins (pessimistic locking gives serial order)
	v, _ := storage.GetRaw(context.Background(), []byte("k1"))
	mv, _ := ParseMVCC(v)
	if string(mv.RealVal) != "from_tx2" {
		t.Fatalf("expected from_tx2 (last write wins), got %s", mv.RealVal)
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

	// TX1: Put + Commit first (releases locks)
	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx1.Put([]byte("k1"), []byte("new1"))
	tx1.Put([]byte("k2"), []byte("new2"))
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatalf("TX1 Commit failed: %v", err)
	}

	// TX2: overwrites k1 after TX1 committed
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2.Put([]byte("k1"), []byte("concurrent"))
	_ = tx2.Commit(context.Background())

	// k1 = TX2's value (last write), k2 = TX1's value (unchanged by TX2)
	tx3, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	val1, _ := tx3.Get(context.Background(), []byte("k1"))
	if string(val1) != "concurrent" {
		t.Fatalf("expected k1=concurrent (TX2 last write), got %s", val1)
	}
	val2, _ := tx3.Get(context.Background(), []byte("k2"))
	if string(val2) != "new2" {
		t.Fatalf("expected k2=new2 (TX1 write), got %s", val2)
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

	// Verify previous version is embedded in BTree value (v1 stored as prev after v2 update)
	raw, err := storage.GetRaw(context.Background(), []byte("k1"))
	if err != nil {
		t.Fatal("expected k1 to exist")
	}
	mv, _ := ParseMVCC(raw)
	if mv.PrevBeginTS == 0 {
		t.Fatal("expected prev version in BTree value after Update")
	}
	if string(mv.RealVal) != "v2" {
		t.Fatalf("current = v2 expected, got %s", mv.RealVal)
	}
	if string(mv.PrevVal) != "v1" {
		t.Fatalf("prev = v1 expected, got %s", mv.PrevVal)
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

func TestSnapshotTx_SnapshotTS(t *testing.T) {
	store := newMockStorage()
	tm := NewTxManager(store, NewLocalTS())
	tx, err := tm.BeginTx(context.Background(), SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	ts := tx.SnapshotTS()
	if ts == 0 {
		t.Error("SnapshotTS should be non-zero")
	}
}

func TestTxManager_SetWAL(t *testing.T) {
	store := newMockStorage()
	tm := NewTxManager(store, NewLocalTS())
	manager := tm.(*txManager)
	if manager.wal != nil {
		t.Error("wal should be nil initially")
	}
	// SetWAL with nil (disable WAL) is valid
	manager.SetWAL(nil)
}

func TestSnapshotTx_CommitAndWait(t *testing.T) {
	store := newMockStorage()
	tm := NewTxManager(store, NewLocalTS())
	tx, err := tm.BeginTx(context.Background(), SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	// CommitAndWait delegates to Commit in sync mode
	stx := tx.(*SnapshotTx)
	if err := stx.CommitAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Key should be visible
	val, err := store.GetRaw(context.Background(), []byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if val == nil {
		t.Error("CommitAndWait should persist data")
	}
}

func TestSnapshotTx_CheckActive_AfterCommit(t *testing.T) {
	store := newMockStorage()
	tm := NewTxManager(store, NewLocalTS())
	tx, err := tm.BeginTx(context.Background(), SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	// checkActive after commit should return error
	stx := tx.(*SnapshotTx)
	if err := stx.checkActive(); err == nil {
		t.Error("checkActive should error after commit")
	}
}

func TestSnapshotTx_CheckActive_AfterRollback(t *testing.T) {
	store := newMockStorage()
	tm := NewTxManager(store, NewLocalTS())
	tx, err := tm.BeginTx(context.Background(), SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	stx := tx.(*SnapshotTx)
	if err := stx.checkActive(); err == nil {
		t.Error("checkActive should error after rollback")
	}
}

func TestKeyLock_LockTimeout(t *testing.T) {

	kl := &KeyLock{}

	if err := kl.Lock(); err != nil {
		t.Fatal(err)
	}
	kl.Unlock()

	// Second lock should also succeed
	if err := kl.Lock(); err != nil {
		t.Fatal(err)
	}
	kl.Unlock()
}

// ============================================================================
// Phase 1: Pessimistic Locking Tests (Lealone RowLock equiv)
// ============================================================================

// newPessimisticTx creates a pessimistic transaction for testing.
func newPessimisticTx(storage *mockStorage, level IsolationLevel) *SnapshotTx {
	tsGen := NewLocalTS()
	tm, _ := NewTxManagerWithGC(storage, tsGen, nil).(*txManager)
	tx, _ := tm.BeginPessimisticTx(context.Background(), level)
	return tx.(*SnapshotTx)
}

func TestPessimisticTx_Basic_PutGet(t *testing.T) {
	storage := newMockStorage()
	storage.rawSet("k1", FlagNormal, 1, []byte("oldVal"))
	tx := newPessimisticTx(storage, SnapshotIsolation)
	_ = tx.Put([]byte("k1"), []byte("v1"))
	_ = tx.Commit(context.Background())

	v, _ := tx.engine.storage.GetRaw(context.Background(), []byte("k1"))
	mv, _ := ParseMVCC(v)
	if string(mv.RealVal) != "v1" {
		t.Fatalf("expected v1, got %s", mv.RealVal)
	}
	if tx.heldLocks != nil {
		t.Fatal("heldLocks should be nil after Commit")
	}
}

func TestPessimisticTx_RollbackReleasesLocks(t *testing.T) {
	storage := newMockStorage()
	storage.rawSet("k1", FlagNormal, 1, []byte("oldVal"))
	tx := newPessimisticTx(storage, SnapshotIsolation)
	_ = tx.Put([]byte("k1"), []byte("v1"))
	_ = tx.Rollback()

	if tx.heldLocks != nil {
		t.Fatal("heldLocks should be nil after Rollback")
	}
	v, _ := storage.GetRaw(context.Background(), []byte("k1"))
	mv, _ := ParseMVCC(v)
	if string(mv.RealVal) != "oldVal" {
		t.Fatalf("rollback should preserve old value, got %s", mv.RealVal)
	}
}

func TestPessimisticTx_DuplicatePutSameKey(t *testing.T) {
	storage := newMockStorage()
	storage.rawSet("k1", FlagNormal, 1, []byte("oldVal"))
	tx := newPessimisticTx(storage, SnapshotIsolation)
	_ = tx.Put([]byte("k1"), []byte("v1"))
	_ = tx.Put([]byte("k1"), []byte("v2")) // same key — no deadlock
	_ = tx.Commit(context.Background())

	v, _ := storage.GetRaw(context.Background(), []byte("k1"))
	mv, _ := ParseMVCC(v)
	if string(mv.RealVal) != "v2" {
		t.Fatalf("second Put should win, got %s", mv.RealVal)
	}
	if tx.heldLocks != nil {
		t.Fatal("heldLocks should be nil after Commit")
	}
}

func TestPessimisticTx_VsOptimistic(t *testing.T) {
	storage := newMockStorage()
	storage.rawSet("k1", FlagNormal, 1, []byte("oldVal"))

	// Both BeginTx and BeginPessimisticTx now create pessimistic transactions.
	// Verify they behave identically.
	ptx := newPessimisticTx(storage, SnapshotIsolation)
	_ = ptx.Put([]byte("k1"), []byte("v1"))
	_ = ptx.Commit(context.Background())

	// BeginTx also creates pessimistic now (Phase 1 unified)
	om := newTestTxManager(storage)
	otx, _ := om.BeginTx(context.Background(), SnapshotIsolation)
	_ = otx.Put([]byte("k1"), []byte("v2"))
	// Verify pessimistic BEFORE Commit (releaseHeldLocks sets nil after)
	if otx.(*SnapshotTx).heldLocks == nil {
		t.Fatal("BeginTx should now create pessimistic tx (non-nil heldLocks)")
	}
	_ = otx.Commit(context.Background())

	v, _ := storage.GetRaw(context.Background(), []byte("k1"))
	mv, _ := ParseMVCC(v)
	if string(mv.RealVal) != "v2" {
		t.Fatalf("last write should win, got %s", mv.RealVal)
	}
}

func TestPessimisticTx_MixedReadWrite(t *testing.T) {
	ctx := context.Background()
	storage := newMockStorage()
	tsGen := NewLocalTS()
	preloadTS := tsGen.NextTS()
	storage.rawSet("k1", FlagNormal, preloadTS, []byte("oldVal"))
	storage.rawSet("k2", FlagNormal, preloadTS+1, []byte("oldVal2"))
	_ = tsGen.NextTS() // advance past preload

	tm, _ := NewTxManagerWithGC(storage, tsGen, nil).(*txManager)
	tx, _ := tm.BeginPessimisticTx(ctx, SnapshotIsolation)

	v, _ := tx.Get(ctx, []byte("k1"))
	if string(v) != "oldVal" {
		t.Fatalf("Get k1: expected oldVal, got %s", v)
	}
	_ = tx.Put([]byte("k2"), []byte("v2"))
	_ = tx.Put([]byte("k3"), []byte("v3"))
	_ = tx.Commit(ctx)
	if tx.(*SnapshotTx).heldLocks != nil {
		t.Fatal("heldLocks should be nil after Commit")
	}
}

func TestPessimisticTx_DefaultPessimistic(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)
	tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	// Since Phase 1 unified: BeginTx always creates a pessimistic transaction
	if tx.(*SnapshotTx).heldLocks == nil {
		t.Fatal("BeginTx should always create pessimistic tx (non-nil heldLocks)")
	}
	_ = tx.Put([]byte("k1"), []byte("v1"))
	_ = tx.Commit(context.Background())
}

func TestPessimisticTx_Delete(t *testing.T) {
	storage := newMockStorage()
	storage.rawSet("k1", FlagNormal, 1, []byte("oldVal"))
	tx := newPessimisticTx(storage, SnapshotIsolation)
	_ = tx.Delete([]byte("k1"))
	_ = tx.Commit(context.Background())

	v, _ := storage.GetRaw(context.Background(), []byte("k1"))
	mv, _ := ParseMVCC(v)
	if !mv.IsTombstone() {
		t.Fatal("expected tombstone after delete")
	}
	if tx.heldLocks != nil {
		t.Fatal("heldLocks should be nil after Commit")
	}
}
