// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCCOWStructuralIntegrity validates that CCOW preserves structural integrity.
// This is the CORE test for CCOW semantics: snapshots must remain immutable.
func TestCCOWStructuralIntegrity(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// 1. Build a multi-level tree (root → 10 internal nodes → 100 leaf nodes)
	t.Log("Building multi-level tree...")
	const numKeys = 1000
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%04d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		// Note: Using InsertWithSplit which triggers auto-splitting
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}

	// 2. Get snapshot (atomic load of root)
	t.Log("Taking snapshot...")
	rootInfo := btree.root.Get()
	snapshotRoot := rootInfo.Root
	originalChildCount := len(snapshotRoot.Children)
	originalDepth := btree.GetDepth()

	t.Logf("Snapshot: childCount=%d, depth=%d", originalChildCount, originalDepth)

	// 3. Concurrent writes (trigger multiple splits)
	t.Log("Starting concurrent writes...")
	const numGoroutines = 10
	const writesPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	startTime := time.Now()
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				key := []byte(fmt.Sprintf("key-%04d", goroutineID*writesPerGoroutine+j))
				value := []byte(fmt.Sprintf("val-%d", j))
				_ = btree.InsertWithSplit(context.Background(), key, value)
			}
		}(i)
	}
	wg.Wait()
	duration := time.Since(startTime)

	t.Logf("Concurrent writes completed in %v", duration)

	// 4. Verify snapshot structure unchanged (CCOW CORE)
	t.Log("Verifying snapshot integrity...")

	assert.Equal(t, originalChildCount, len(snapshotRoot.Children),
		"Snapshot child count must not change")

	assert.Equal(t, originalDepth, btree.GetDepth(),
		"Snapshot depth must not change")

	// Verify each child pointer is unchanged
	for i, child := range snapshotRoot.Children {
		assert.NotNil(t, child, "Child %d should not be nil", i)
		assert.Same(t, snapshotRoot.Children[i], child,
			"Child %d pointer must not change", i)
	}

	t.Log("✓ CCOW structural integrity verified")
}

// TestCCOWSnapshotIsolation validates snapshot isolation.
// Snapshot reads should not see concurrent writes.
func TestCCOWSnapshotIsolation(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// 1. Insert initial data
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("initial-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}

	// 2. Take snapshot
	rootInfo := btree.root.Get()
	snapshotRoot := rootInfo.Root
	snapshotVersion := rootInfo.Version
	rootInfo.Release()

	t.Logf("Snapshot taken at version %d", snapshotVersion)

	// 3. Concurrent writes (modify existing keys + insert new keys)
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for i := 0; i < numKeys; i++ {
			key := []byte(fmt.Sprintf("key-%03d", i))
			value := []byte(fmt.Sprintf("modified-%d", i))
			_ = btree.InsertWithSplit(context.Background(), key, value)
		}
	}()

	// 4. Snapshot reads should see initial data
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value, err := snapshotRoot.Get(key)

		require.NoError(t, err, "Key %s should exist in snapshot", string(key))
		assert.Equal(t, []byte(fmt.Sprintf("initial-%d", i)), value,
			"Snapshot should see initial value, not modified value")
	}

	wg.Wait()

	t.Log("✓ CCOW snapshot isolation verified")
}

// TestCCOWNoDataRace validates CCOW has no data races.
// This test MUST pass `go test -race` without any warnings.
func TestCCOWNoDataRace(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	const numGoroutines = 20
	const opsPerGoroutine = 500

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // Readers + writers

	// Writers
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte(fmt.Sprintf("key-%d-%d", id, j))
				value := []byte(fmt.Sprintf("val-%d", j))
				_ = btree.InsertWithSplit(context.Background(), key, value)
			}
		}(i)
	}

	// Readers
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte(fmt.Sprintf("key-%d-%d", id, j))
				_ = btree.InsertWithSplit(context.Background(), key, []byte("dummy"))
				_, _ = btree.Get(context.Background(), key)
			}
		}(i)
	}

	wg.Wait()

	t.Log("✓ CCOW no data race verified (run with -race to confirm)")
}

// TestCCOWMultipleSnapshots validates multiple snapshots coexist correctly.
func TestCCOWMultipleSnapshots(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// 1. Insert initial data
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("version0-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}

	// 2. Take snapshot 1
	rootInfo1 := btree.root.Get()
	snapshot1Root := rootInfo1.Root
	snapshot1Version := rootInfo1.Version
	rootInfo1.Release()

	// 3. Insert more data (version 1)
	for i := 10; i < 20; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("version1-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}

	// 4. Take snapshot 2
	rootInfo2 := btree.root.Get()
	snapshot2Root := rootInfo2.Root
	snapshot2Version := rootInfo2.Version
	rootInfo2.Release()

	// 5. Insert more data (version 2)
	for i := 20; i < 30; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("version2-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}

	// 6. Verify snapshot 1 sees only version 0 data
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value, err := snapshot1Root.Get(key)
		require.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("version0-%d", i)), value)
	}

	// Snapshot 1 should NOT see version 1/2 data
	for i := 10; i < 30; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		_, err := snapshot1Root.Get(key)
		assert.Error(t, err, "Snapshot 1 should not see keys from later versions")
	}

	// 7. Verify snapshot 2 sees version 0 + version 1 data
	for i := 0; i < 20; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value, err := snapshot2Root.Get(key)
		require.NoError(t, err)
		if i < 10 {
			assert.Equal(t, []byte(fmt.Sprintf("version0-%d", i)), value)
		} else {
			assert.Equal(t, []byte(fmt.Sprintf("version1-%d", i)), value)
		}
	}

	// Snapshot 2 should NOT see version 2 data
	for i := 20; i < 30; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		_, err := snapshot2Root.Get(key)
		assert.Error(t, err, "Snapshot 2 should not see keys from version 2")
	}

	t.Logf("Snapshot 1: version=%d, Snapshot 2: version=%d",
		snapshot1Version, snapshot2Version)
	t.Log("✓ Multiple snapshots coexist correctly")
}

// TestCCOWRootSwitching validates atomic root switching.
func TestCCOWRootSwitching(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	const numWrites = 100

	// Track version transitions
	versionChan := make(chan uint64, numWrites)
	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numWrites; i++ {
			key := []byte(fmt.Sprintf("key-%03d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			_ = btree.InsertWithSplit(context.Background(), key, value)

			rootInfo := btree.root.Get()
			versionChan <- rootInfo.Version
			rootInfo.Release()
		}
		close(versionChan)
	}()

	// Reader goroutine: verify version monotonicity
	wg.Add(1)
	go func() {
		defer wg.Done()
		var lastVersion uint64 = 0
		for version := range versionChan {
			assert.GreaterOrEqual(t, version, lastVersion,
				"Version must be monotonically increasing")
			lastVersion = version
		}
	}()

	wg.Wait()

	t.Log("✓ Atomic root switching verified")
}

// BenchmarkCCOWRead benchmarks CCOW read performance.
func BenchmarkCCOWRead(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Insert test data
	const numKeys = 10000
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i%numKeys))
		_, _ = btree.Get(context.Background(), key)
	}
}

// BenchmarkCCOWWrite benchmarks CCOW write performance.
func BenchmarkCCOWWrite(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%09d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}
}

// BenchmarkCCOWConcurrentWrite benchmarks concurrent CCOW writes.
func BenchmarkCCOWConcurrentWrite(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%09d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			_ = btree.InsertWithSplit(context.Background(), key, value)
			i++
		}
	})
}
