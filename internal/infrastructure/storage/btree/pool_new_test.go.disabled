// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// TestAcquireReleasePage verifies page pool acquisition and release.
func TestAcquireReleasePage(t *testing.T) {
	t.Run("acquire and release page", func(t *testing.T) {
		page := AcquirePage()
		assert.NotNil(t, page)

		// Modify page
		page.ID = 42
		page.Type = model.LeafPage
		page.MarkDirty()

		// Release and acquire again
		ReleasePage(page)

		newPage := AcquirePage()
		assert.NotNil(t, newPage)

		// Page should be reset
		assert.Equal(t, model.PageID(0), newPage.ID)
		assert.Equal(t, model.PageType(0), newPage.Type)
		assert.False(t, newPage.IsDirty())
	})

	t.Run("release nil page", func(t *testing.T) {
		// Should not panic
		ReleasePage(nil)
		assert.True(t, true)
	})
}

// TestAcquireReleaseNode verifies node pool acquisition and release.
func TestAcquireReleaseNode(t *testing.T) {
	t.Run("acquire and release leaf node", func(t *testing.T) {
		node := AcquireNode()
		assert.NotNil(t, node)
		assert.True(t, node.IsLeaf)

		// Modify node
		err := node.Insert([]byte("key"), []byte("value"))
		assert.NoError(t, err)

		// Release and acquire again
		ReleaseNode(node)

		newNode := AcquireNode()
		assert.NotNil(t, newNode)
		assert.True(t, newNode.IsLeaf)
		assert.True(t, newNode.IsEmpty())
	})

	t.Run("acquire internal node", func(t *testing.T) {
		node := AcquireInternalNode()
		assert.NotNil(t, node)
		assert.False(t, node.IsLeaf)
	})

	t.Run("release nil node", func(t *testing.T) {
		// Should not panic
		ReleaseNode(nil)
		assert.True(t, true)
	})
}

// TestConcurrentPoolAccess verifies thread-safe pool access.
func TestConcurrentPoolAccess(t *testing.T) {
	t.Run("concurrent page pool access", func(t *testing.T) {
		const goroutines = 100
		const opsPerGoroutine = 1000

		var wg sync.WaitGroup
		wg.Add(goroutines)

		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < opsPerGoroutine; j++ {
					page := AcquirePage()
					assert.NotNil(t, page)
					ReleasePage(page)
				}
			}()
		}

		wg.Wait()
	})

	t.Run("concurrent node pool access", func(t *testing.T) {
		const goroutines = 100
		const opsPerGoroutine = 1000

		var wg sync.WaitGroup
		wg.Add(goroutines)

		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < opsPerGoroutine; j++ {
					node := AcquireNode()
					assert.NotNil(t, node)
					ReleaseNode(node)
				}
			}()
		}

		wg.Wait()
	})
}

// TestPoolReset verifies that objects are properly reset.
func TestPoolReset(t *testing.T) {
	t.Run("page reset", func(t *testing.T) {
		page := AcquirePage()
		page.ID = 123
		page.Type = model.InternalPage
		page.Version = 999
		page.RefCount.Store(42)
		page.MarkDirty()

		ReleasePage(page)

		resetPage := AcquirePage()
		assert.Equal(t, model.PageID(0), resetPage.ID)
		assert.Equal(t, model.PageType(0), resetPage.Type)
		assert.Equal(t, uint64(0), resetPage.Version)
		assert.Equal(t, int32(0), resetPage.GetRefCount())
		assert.False(t, resetPage.IsDirty())
	})

	t.Run("node reset", func(t *testing.T) {
		node := AcquireNode()
		// node is leaf by default

		// Add some keys
		for i := 0; i < 10; i++ {
			err := node.Insert([]byte{byte(i)}, []byte("value"))
			assert.NoError(t, err)
		}

		assert.Equal(t, 10, node.Size())
		assert.True(t, node.IsLeaf)

		ReleaseNode(node)

		resetNode := AcquireNode()
		// After release, node should be reset to default state (leaf node, empty)
		assert.True(t, resetNode.IsEmpty(), "node should be empty after reset")
		assert.True(t, resetNode.IsLeaf, "node should be reset to leaf")
		assert.Equal(t, 0, len(resetNode.Children))
	})
}

// TestPoolPreservesCapacity verifies that pool preserves slice capacity.
func TestPoolPreservesCapacity(t *testing.T) {
	t.Run("node preserves capacity", func(t *testing.T) {
		node := AcquireNode()

		// Fill node to grow slices
		for i := 0; i < 50; i++ {
			node.Insert([]byte{byte(i)}, []byte("value"))
		}

		capKeys := cap(node.Keys)
		capValues := cap(node.Values)
		assert.Greater(t, capKeys, 50)
		assert.Greater(t, capValues, 50)

		ReleaseNode(node)

		// Acquire again - should preserve capacity
		newNode := AcquireNode()
		assert.Equal(t, capKeys, cap(newNode.Keys))
		assert.Equal(t, capValues, cap(newNode.Values))
		assert.Equal(t, 0, len(newNode.Keys))
	})
}

// BenchmarkPoolPageAllocation benchmarks pooled vs new page allocation.
func BenchmarkPoolPageAllocation(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			page := AcquirePage()
			ReleasePage(page)
		}
	})
}

// BenchmarkNewPageAllocation benchmarks new page allocation.
func BenchmarkNewPageAllocation(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			page := NewPage(1, model.LeafPage)
			_ = page
		}
	})
}

// BenchmarkPoolNodeAllocation benchmarks pooled vs new node allocation.
func BenchmarkPoolNodeAllocation(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			node := AcquireNode()
			ReleaseNode(node)
		}
	})
}

// BenchmarkNewNodeAllocation benchmarks new node allocation.
func BenchmarkNewNodeAllocation(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			node := NewNode(true)
			_ = node
		}
	})
}

// BenchmarkPoolNodeWithInsert benchmarks node pool with insert operations.
func BenchmarkPoolNodeWithInsert(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		node := AcquireNode()
		b.StartTimer()

		for j := 0; j < 10; j++ {
			node.Insert([]byte{byte(j)}, []byte("value"))
		}

		b.StopTimer()
		ReleaseNode(node)
		b.StartTimer()
	}
}

// BenchmarkNewNodeWithInsert benchmarks new node with insert operations.
func BenchmarkNewNodeWithInsert(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := NewNode(true)

		for j := 0; j < 10; j++ {
			node.Insert([]byte{byte(j)}, []byte("value"))
		}

		_ = node
	}
}
