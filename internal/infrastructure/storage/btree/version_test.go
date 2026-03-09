// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewVersionedRoot verifies VersionedRoot creation.
func TestNewVersionedRoot(t *testing.T) {
	t.Run("create with initial root", func(t *testing.T) {
		initialNode := NewNode(true)
		vr := NewVersionedRoot(initialNode)

		root := vr.Get()
		defer root.Release()

		assert.NotNil(t, root.Root)
		assert.Equal(t, model.PageID(1), root.RootID)
		assert.Equal(t, uint64(0), root.Version)
		assert.False(t, root.Created.IsZero())
		assert.Equal(t, int32(2), root.GetRefCount()) // Initial(1) + Get() Acquire(1) = 2
		assert.Equal(t, uint64(0), root.WALSeqNum)
	})

	t.Run("verify default settings", func(t *testing.T) {
		initialNode := NewNode(true)
		vr := NewVersionedRoot(initialNode)

		assert.Equal(t, uint64(0), vr.GetCurrentVersion())
		assert.Equal(t, 1, vr.GetVersionCount())
		assert.Equal(t, 10, vr.GetMaxVersions())
	})
}

// TestVersionedRoot_Update verifies root update functionality.
func TestVersionedRoot_Update(t *testing.T) {
	ctx := context.Background()
	initialNode := NewNode(true)
	vr := NewVersionedRoot(initialNode)

	t.Run("update root", func(t *testing.T) {
		newNode := NewNode(true)
		_ = newNode.Insert([]byte("key"), []byte("value"))
		err := vr.Update(ctx, newNode, 100)
		require.NoError(t, err)

		root := vr.Get()
		defer root.Release()

		assert.NotNil(t, root.Root)
		assert.Equal(t, 1, root.Root.Size())
		assert.Equal(t, uint64(1), root.Version)
		assert.Equal(t, uint64(100), root.WALSeqNum)
	})

	t.Run("multiple updates increment version", func(t *testing.T) {
		// Create fresh instance for this test
		vr2 := NewVersionedRoot(NewNode(true))

		// Initial version is 0, after 5 updates should be version 5
		for i := range 5 {
			newNode := NewNode(true)
			_ = newNode.Insert([]byte{byte(i)}, []byte("value"))
			err := vr2.Update(ctx, newNode, uint64(i*1000))
			require.NoError(t, err)
		}

		root := vr2.Get()
		defer root.Release()

		assert.NotNil(t, root.Root)
		assert.Equal(t, uint64(5), root.Version) // 5 updates: versions 1,2,3,4,5
	})

	t.Run("update after close", func(t *testing.T) {
		vr2 := NewVersionedRoot(NewNode(true))
		err := vr2.Close()
		require.NoError(t, err)

		newNode := NewNode(true)
		err = vr2.Update(ctx, newNode, 100)
		assert.ErrorIs(t, err, ErrClosed)
	})
}

// TestVersionedRoot_ConcurrentGet verifies lock-free concurrent reads.
func TestVersionedRoot_ConcurrentGet(t *testing.T) {
	initialNode := NewNode(true)
	vr := NewVersionedRoot(initialNode)

	const goroutines = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Concurrent reads (all should be lock-free)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range 100 {
				root := vr.Get()
				assert.NotNil(t, root)
				assert.NotNil(t, root.Root)
				root.Release()
			}
		}()
	}

	wg.Wait()

	// Verify no corruption
	root := vr.Get()
	defer root.Release()
	assert.NotNil(t, root.Root)
}

// TestVersionedRoot_ConcurrentUpdate verifies concurrent root updates.
func TestVersionedRoot_ConcurrentUpdate(t *testing.T) {
	ctx := context.Background()
	initialNode := NewNode(true)
	vr := NewVersionedRoot(initialNode)

	const goroutines = 100
	const updatesPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			seqNum := id * updatesPerGoroutine
			for range updatesPerGoroutine {
				newNode := NewNode(true)
				_ = newNode.Insert([]byte("key"), []byte("value"))
				err := vr.Update(ctx, newNode, uint64(seqNum))
				assert.NoError(t, err)
				seqNum++
			}
		}(i)
	}

	wg.Wait()

	// Verify final state
	root := vr.Get()
	defer root.Release()

	// Should have the latest version
	// 100 goroutines × 100 updates = 10000 updates
	// Initial version = 0, after 10000 updates = version 10000
	assert.Equal(t, uint64(goroutines*updatesPerGoroutine), root.Version)
	assert.Equal(t, int32(2), root.GetRefCount()) // Initial(1) + Get() Acquire(1) = 2
}

// TestVersionedRoot_Snapshot verifies snapshot functionality.
func TestVersionedRoot_Snapshot(t *testing.T) {
	ctx := context.Background()
	initialNode := NewNode(true)
	vr := NewVersionedRoot(initialNode)

	t.Run("create snapshot", func(t *testing.T) {
		vr2 := NewVersionedRoot(NewNode(true))

		snapshotID, err := vr2.CreateSnapshot(ctx)
		require.NoError(t, err)
		assert.NotEqual(t, SnapshotID(0), snapshotID)

		// Verify snapshot points to version 0
		root := vr2.GetVersion(snapshotID)
		require.NotNil(t, root)
		defer root.Release()

		assert.NotNil(t, root.Root)
		assert.Equal(t, uint64(0), root.Version)
		assert.Greater(t, root.GetRefCount(), int32(1)) // At least snapshot + initial
	})

	t.Run("multiple snapshots", func(t *testing.T) {
		vr2 := NewVersionedRoot(NewNode(true))

		// Create snapshot for version 0
		snapshotID1, err := vr2.CreateSnapshot(ctx)
		require.NoError(t, err)

		// Update root
		newNode := NewNode(true)
		_ = newNode.Insert([]byte("key"), []byte("value"))
		err = vr2.Update(ctx, newNode, 100)
		require.NoError(t, err)

		// Create another snapshot
		snapshotID2, err := vr2.CreateSnapshot(ctx)
		require.NoError(t, err)

		// Verify both snapshots point to different versions
		root1 := vr2.GetVersion(snapshotID1)
		require.NotNil(t, root1)
		defer root1.Release()
		assert.NotNil(t, root1.Root)
		assert.Equal(t, uint64(0), root1.Version)

		root2 := vr2.GetVersion(snapshotID2)
		require.NotNil(t, root2)
		defer root2.Release()
		assert.NotNil(t, root2.Root)
		assert.Equal(t, uint64(1), root2.Version)
	})

	t.Run("release snapshot", func(t *testing.T) {
		vr2 := NewVersionedRoot(NewNode(true))

		snapshotID, err := vr2.CreateSnapshot(ctx)
		require.NoError(t, err)

		// Snapshot should hold a reference
		root := vr2.Get()
		initialRefCount := root.GetRefCount()
		root.Release()

		// Release snapshot
		err = vr2.ReleaseSnapshot(ctx, snapshotID)
		require.NoError(t, err)

		// Version should still exist because initial root has a reference
		// But the snapshot reference should be released
		root = vr2.GetVersion(snapshotID)
		assert.NotNil(t, root)                                 // Still exists because initial root ref > 0
		assert.Equal(t, initialRefCount-1, root.GetRefCount()) // Decreased by 1
		root.Release()
	})

	t.Run("release non-existent snapshot", func(t *testing.T) {
		err := vr.ReleaseSnapshot(ctx, 999)
		assert.NoError(t, err) // Should not error, just no-op
	})

	t.Run("create snapshot after close", func(t *testing.T) {
		vr2 := NewVersionedRoot(NewNode(true))
		err := vr2.Close()
		require.NoError(t, err)

		_, err = vr2.CreateSnapshot(ctx)
		assert.ErrorIs(t, err, ErrClosed)
	})
}

// TestVersionedRoot_VersionManagement verifies version lifecycle.
func TestVersionedRoot_VersionManagement(t *testing.T) {
	ctx := context.Background()
	initialNode := NewNode(true)
	vr := NewVersionedRoot(initialNode)

	t.Run("version count increases with updates", func(t *testing.T) {
		initialCount := vr.GetVersionCount()

		// Update 5 times
		for i := range 5 {
			newNode := NewNode(true)
			_ = newNode.Insert([]byte{byte(i)}, []byte("value"))
			err := vr.Update(ctx, newNode, uint64(i))
			require.NoError(t, err)
		}

		// Should have initial + 5 versions (initial root is version 0)
		assert.Equal(t, initialCount+5, vr.GetVersionCount())
	})

	t.Run("versions are garbage collected after release", func(t *testing.T) {
		// Use fresh instance to avoid state pollution from previous test
		vr2 := NewVersionedRoot(NewNode(true))

		// Create multiple snapshots
		var snapshotIDs []SnapshotID
		for i := range 3 {
			snapshotID, err := vr2.CreateSnapshot(ctx)
			require.NoError(t, err)
			snapshotIDs = append(snapshotIDs, snapshotID)

			if i < 2 {
				newNode := NewNode(true)
				_ = newNode.Insert([]byte{byte(i)}, []byte("value"))
				err = vr2.Update(ctx, newNode, uint64(i))
				require.NoError(t, err)
			}
		}

		// Should have 3 versions (0, 1, 2)
		assert.Equal(t, 3, vr2.GetVersionCount())

		// Release all snapshots
		for _, id := range snapshotIDs {
			err := vr2.ReleaseSnapshot(ctx, id)
			require.NoError(t, err)
		}

		// Versions with refCount 0 should be garbage collected
		// Only current version should remain
		time.Sleep(100 * time.Millisecond) // Give GC time to run
		assert.LessOrEqual(t, vr2.GetVersionCount(), 1)
	})
}

// TestRootInfo_RefCount verifies reference counting.
func TestRootInfo_RefCount(t *testing.T) {
	root := &RootInfo{
		Root:    NewNode(true),
		RootID:  1,
		Version: 0,
	}
	root.RefCount.Store(1)

	t.Run("acquire and release", func(t *testing.T) {
		assert.Equal(t, int32(1), root.GetRefCount())

		root.Acquire()
		assert.Equal(t, int32(2), root.GetRefCount())

		root.Acquire()
		assert.Equal(t, int32(3), root.GetRefCount())

		root.Release()
		assert.Equal(t, int32(2), root.GetRefCount())

		root.Release()
		assert.Equal(t, int32(1), root.GetRefCount())
	})

	t.Run("concurrent ref count changes", func(t *testing.T) {
		const goroutines = 100
		const opsPerGoroutine = 100

		var wg sync.WaitGroup
		wg.Add(goroutines * 2)

		// Acquire goroutines
		for range goroutines {
			go func() {
				defer wg.Done()
				for j := 0; j < opsPerGoroutine; j++ {
					root.Acquire()
				}
			}()
		}

		// Release goroutines
		for range goroutines {
			go func() {
				defer wg.Done()
				for j := 0; j < opsPerGoroutine; j++ {
					root.Release()
				}
			}()
		}

		wg.Wait()

		// Final refCount should be 1 (initial) + goroutines*ops - goroutines*ops = 1
		assert.Equal(t, int32(1), root.GetRefCount())
	})
}

// BenchmarkVersionedRoot_Get benchmarks root info retrieval.
func BenchmarkVersionedRoot_Get(b *testing.B) {
	vr := NewVersionedRoot(NewNode(true))

	b.ResetTimer()
	for range b.N {
		root := vr.Get()
		root.Release()
	}
}

// BenchmarkVersionedRoot_Update benchmarks root updates.
func BenchmarkVersionedRoot_Update(b *testing.B) {
	ctx := context.Background()
	vr := NewVersionedRoot(NewNode(true))

	b.ResetTimer()
	for i := range b.N {
		newNode := NewNode(true)
		_ = newNode.Insert([]byte("key"), []byte("value"))
		_ = vr.Update(ctx, newNode, uint64(i))
	}
}

// BenchmarkVersionedRoot_ConcurrentGet benchmarks concurrent reads.
func BenchmarkVersionedRoot_ConcurrentGet(b *testing.B) {
	vr := NewVersionedRoot(NewNode(true))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			root := vr.Get()
			root.Release()
		}
	})
}

// BenchmarkVersionedRoot_CreateSnapshot benchmarks snapshot creation.
func BenchmarkVersionedRoot_CreateSnapshot(b *testing.B) {
	ctx := context.Background()
	vr := NewVersionedRoot(NewNode(true))

	b.ResetTimer()
	for range b.N {
		_, _ = vr.CreateSnapshot(ctx)
	}
}
