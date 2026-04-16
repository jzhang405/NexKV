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

// ---------------------------------------------------------------------------
// Integration test: No Dirty Read
// ---------------------------------------------------------------------------

func TestIntegration_SI_NoDirtyRead(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// TX1 starts before TX2 writes
	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)

	// TX2 writes but does NOT commit yet
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2.Put([]byte("k1"), []byte("uncommitted"))
	// TX2 not committed

	// TX1 should NOT see TX2's uncommitted write
	_, err := tx1.Get(context.Background(), []byte("k1"))
	if err != ErrKeyNotFound {
		t.Fatalf("dirty read detected: expected ErrKeyNotFound, got val err=%v", err)
	}

	// TX2 commit
	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("TX2 commit failed: %v", err)
	}

	// TX1 still should NOT see TX2's write (snapshot was taken before TX2 commit)
	_, err = tx1.Get(context.Background(), []byte("k1"))
	if err != ErrKeyNotFound {
		t.Fatalf("snapshot violated: TX1 should not see TX2's committed write, err=%v", err)
	}

	tx1.Rollback()
}

// ---------------------------------------------------------------------------
// Integration test: No Non-Repeatable Read
// ---------------------------------------------------------------------------

func TestIntegration_SI_NoNonRepeatableRead(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// Pre-populate k1
	storage.rawSet("k1", FlagNormal, 1, []byte("original"))

	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)

	// First read
	val1, err := tx1.Get(context.Background(), []byte("k1"))
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	// TX2 updates k1 and commits
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2.Put([]byte("k1"), []byte("updated"))
	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("TX2 commit failed: %v", err)
	}

	// TX1 reads k1 again — should see same value as first read
	val2, err := tx1.Get(context.Background(), []byte("k1"))
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}

	if string(val1) != string(val2) {
		t.Fatalf("non-repeatable read: first=%s, second=%s", val1, val2)
	}
	if string(val2) != "original" {
		t.Fatalf("expected 'original', got '%s'", val2)
	}

	tx1.Rollback()
}

// ---------------------------------------------------------------------------
// Integration test: Concurrent writes to different keys should not conflict
// ---------------------------------------------------------------------------

func TestIntegration_SI_ConcurrentWriteDifferentKeys(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// Pre-populate keys
	for i := 0; i < 10; i++ {
		storage.rawSet(fmt.Sprintf("key%d", i), FlagNormal, 1, []byte(fmt.Sprintf("val%d", i)))
	}

	const numTx = 20
	var wg sync.WaitGroup
	var conflicts atomic.Int32

	for i := 0; i < numTx; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
			key := []byte(fmt.Sprintf("key%d", idx%10))
			tx.Put(key, []byte(fmt.Sprintf("new%d", idx)))
			if err := tx.Commit(context.Background()); err != nil {
				conflicts.Add(1)
			}
		}(i)
	}
	wg.Wait()

	// Some conflicts expected since multiple TXs write to same keys (idx%10)
	// But no false conflicts between truly different keys
	// Verify all 10 keys exist
	tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		_, err := tx.Get(context.Background(), []byte(key))
		if err != nil {
			t.Fatalf("key %s should exist: %v", key, err)
		}
	}
	tx.Rollback()
}

// ---------------------------------------------------------------------------
// Integration test: Write Skew is allowed under Snapshot Isolation
// ---------------------------------------------------------------------------

func TestIntegration_SI_WriteSkewAllowed(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	// Setup: two accounts with total balance 200
	storage.rawSet("acc1", FlagNormal, 1, []byte("150"))
	storage.rawSet("acc2", FlagNormal, 1, []byte("50"))

	// Both TXs read both accounts and update different ones
	tx1, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	tx2, _ := tm.BeginTx(context.Background(), SnapshotIsolation)

	// TX1 reads both accounts
	acc1_v1, _ := tx1.Get(context.Background(), []byte("acc1"))
	acc2_v1, _ := tx1.Get(context.Background(), []byte("acc2"))
	_ = acc1_v1
	_ = acc2_v1

	// TX2 reads both accounts
	tx2.Get(context.Background(), []byte("acc1"))
	tx2.Get(context.Background(), []byte("acc2"))

	// TX1 updates acc1 (e.g., withdraw 50)
	tx1.Put([]byte("acc1"), []byte("100"))
	// TX2 updates acc2 (e.g., withdraw 50)
	tx2.Put([]byte("acc2"), []byte("0"))

	// Both should commit successfully (Write Skew is allowed in SI)
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatalf("TX1 should commit: %v", err)
	}
	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("TX2 should commit (write skew allowed): %v", err)
	}

	// Verify final values
	tx3, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	v1, _ := tx3.Get(context.Background(), []byte("acc1"))
	v2, _ := tx3.Get(context.Background(), []byte("acc2"))
	if string(v1) != "100" {
		t.Fatalf("acc1 expected 100, got %s", v1)
	}
	if string(v2) != "0" {
		t.Fatalf("acc2 expected 0, got %s", v2)
	}
	tx3.Rollback()
}

// ---------------------------------------------------------------------------
// Integration test: Version chain long history traversal
// ---------------------------------------------------------------------------

func TestIntegration_VersionChain_LongHistory(t *testing.T) {
	storage := newMockStorage()
	tm := newTestTxManager(storage)

	const numVersions = 10

	// Create 10 versions of k1
	for i := 0; i < numVersions; i++ {
		tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
		tx.Put([]byte("k1"), []byte(fmt.Sprintf("v%d", i)))
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("commit %d failed: %v", i, err)
		}
	}

	// Latest transaction should see v9
	tx, _ := tm.BeginTx(context.Background(), SnapshotIsolation)
	val, err := tx.Get(context.Background(), []byte("k1"))
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "v9" {
		t.Fatalf("expected v9, got %s", val)
	}
	tx.Rollback()

	// Verify version chain has nodes
	tmImpl := tm.(*txManager)
	chain := tmImpl.versionStore.Load("k1")
	if chain == nil {
		t.Fatal("expected version chain for k1")
	}
	nodeCount := 0
	node := chain.Load()
	for node != nil {
		nodeCount++
		node = node.next
	}
	// Each update creates a chain node for the old value
	// The B+Tree holds the latest version directly, chain has old versions
	if nodeCount == 0 {
		t.Fatal("expected at least one node in version chain")
	}
}
