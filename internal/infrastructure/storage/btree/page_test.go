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

// TestNewPage verifies page creation and initialization.
func TestNewPage(t *testing.T) {
	tests := []struct {
		name     string
		id       model.PageID
		pageType model.PageType
	}{
		{
			name:     "leaf page",
			id:       1,
			pageType: model.LeafPage,
		},
		{
			name:     "internal page",
			id:       2,
			pageType: model.InternalPage,
		},
		{
			name:     "meta page",
			id:       0,
			pageType: model.MetaPage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := NewPage(tt.id, tt.pageType)

			assert.Equal(t, tt.id, page.ID)
			assert.Equal(t, tt.pageType, page.Type)
			assert.Equal(t, uint64(0), page.Version)
			assert.Equal(t, int32(1), page.GetRefCount())
			assert.False(t, page.IsDirty())
		})
	}
}

// TestPageRefCount verifies reference counting mechanics.
func TestPageRefCount(t *testing.T) {
	page := NewPage(1, model.LeafPage)

	// Initial reference count should be 1
	assert.Equal(t, int32(1), page.GetRefCount())

	// Acquire should increment
	page.Acquire()
	assert.Equal(t, int32(2), page.GetRefCount())

	page.Acquire()
	assert.Equal(t, int32(3), page.GetRefCount())

	// Release should decrement
	count := page.Release()
	assert.Equal(t, int32(2), count)
	assert.Equal(t, int32(2), page.GetRefCount())

	count = page.Release()
	assert.Equal(t, int32(1), count)
	assert.Equal(t, int32(1), page.GetRefCount())
}

// TestPageRefCountConcurrent verifies thread-safe reference counting.
func TestPageRefCountConcurrent(t *testing.T) {
	page := NewPage(1, model.LeafPage)
	const goroutines = 100
	const acquiresPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Concurrent acquires
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < acquiresPerGoroutine; j++ {
				page.Acquire()
			}
		}()
	}

	wg.Wait()

	// Initial (1) + goroutines * acquires
	expectedCount := int32(1 + goroutines*acquiresPerGoroutine)
	assert.Equal(t, expectedCount, page.GetRefCount())

	// Concurrent releases
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < acquiresPerGoroutine; j++ {
				page.Release()
			}
		}()
	}

	wg.Wait()

	// Should return to initial count
	assert.Equal(t, int32(1), page.GetRefCount())
}

// TestPageIsLeaf verifies leaf page detection.
func TestPageIsLeaf(t *testing.T) {
	leafPage := NewPage(1, model.LeafPage)
	assert.True(t, leafPage.IsLeaf())
	assert.False(t, leafPage.IsInternal())
	assert.False(t, leafPage.IsMeta())
}

// TestPageIsInternal verifies internal page detection.
func TestPageIsInternal(t *testing.T) {
	internalPage := NewPage(2, model.InternalPage)
	assert.True(t, internalPage.IsInternal())
	assert.False(t, internalPage.IsLeaf())
	assert.False(t, internalPage.IsMeta())
}

// TestPageIsMeta verifies meta page detection.
func TestPageIsMeta(t *testing.T) {
	metaPage := NewPage(0, model.MetaPage)
	assert.True(t, metaPage.IsMeta())
	assert.False(t, metaPage.IsLeaf())
	assert.False(t, metaPage.IsInternal())
}

// TestPageDirtyFlag verifies dirty flag operations.
func TestPageDirtyFlag(t *testing.T) {
	page := NewPage(1, model.LeafPage)

	// Initially not dirty
	assert.False(t, page.IsDirty())

	// Mark as dirty
	page.MarkDirty()
	assert.True(t, page.IsDirty())

	// Clear dirty flag
	page.ClearDirty()
	assert.False(t, page.IsDirty())

	// Mark dirty again
	page.MarkDirty()
	assert.True(t, page.IsDirty())
}

// TestPageVersion verifies version management.
func TestPageVersion(t *testing.T) {
	page := NewPage(1, model.LeafPage)

	// Initial version
	assert.Equal(t, uint64(0), page.GetVersion())

	// Set version
	page.SetVersion(1)
	assert.Equal(t, uint64(1), page.GetVersion())

	page.SetVersion(100)
	assert.Equal(t, uint64(100), page.GetVersion())

	page.SetVersion(0)
	assert.Equal(t, uint64(0), page.GetVersion())
}

// TestPageDataSize verifies page data size constants.
func TestPageDataSize(t *testing.T) {
	// Verify page size is 4KB
	assert.Equal(t, 4096, PageSize)

	// Verify header size calculation
	assert.Equal(t, 21, PageHeaderSize)

	// Verify data size calculation
	assert.Equal(t, 4096-21, PageDataSize)

	// Verify data size matches expected
	assert.Equal(t, 4075, PageDataSize)
}

// TestPageTypeString verifies PageType string representation.
func TestPageTypeString(t *testing.T) {
	tests := []struct {
		pageType model.PageType
		expected string
	}{
		{model.LeafPage, "Leaf"},
		{model.InternalPage, "Internal"},
		{model.MetaPage, "Meta"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.pageType.String())
		})
	}
}

// BenchmarkPageAllocation benchmarks page allocation performance.
func BenchmarkPageAllocation(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			page := NewPage(1, model.LeafPage)
			_ = page
		}
	})
}

// BenchmarkPageAcquireRelease benchmarks acquire/release performance.
func BenchmarkPageAcquireRelease(b *testing.B) {
	page := NewPage(1, model.LeafPage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page.Acquire()
		page.Release()
	}
}

// BenchmarkPageConcurrentAccess benchmarks concurrent page access.
func BenchmarkPageConcurrentAccess(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			page := NewPage(1, model.LeafPage)
			page.Acquire()
			page.MarkDirty()
			page.Release()
		}
	})
}
