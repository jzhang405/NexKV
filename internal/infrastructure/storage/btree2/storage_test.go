// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree2

import (
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
)

const testMmapSize = 256 * offheap.PageSize // 256 pages for testing

func newTestStorage(t *testing.T) *OffheapBTreeStorage {
	t.Helper()
	s, err := NewOffheapBTreeStorage(testMmapSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// --- Allocation Tests ---

func TestAllocLeafPage(t *testing.T) {
	s := newTestStorage(t)

	id, err := s.AllocLeafPage()
	require.NoError(t, err)

	// Verify page is a leaf
	rawID := uint32(id)
	assert.True(t, s.pa.IsLeaf(rawID), "allocated page must be leaf type")
}

func TestAllocNodePage(t *testing.T) {
	s := newTestStorage(t)

	id, err := s.AllocNodePage()
	require.NoError(t, err)

	rawID := uint32(id)
	assert.False(t, s.pa.IsLeaf(rawID), "allocated page must be index type")
}

func TestAllocFreeBasic(t *testing.T) {
	s := newTestStorage(t)

	id, err := s.AllocLeafPage()
	require.NoError(t, err)

	// Free the page
	err = s.FreePage(id)
	require.NoError(t, err)

	// Re-allocate should succeed (page recycled from free list)
	id2, err := s.AllocLeafPage()
	require.NoError(t, err)
	assert.NotNil(t, id2) // just verify it doesn't error
}

// --- COW Tests ---

func TestCopyLeafPage(t *testing.T) {
	s := newTestStorage(t)

	srcID, err := s.AllocLeafPage()
	require.NoError(t, err)

	// Write some data to src
	rawSrcID := uint32(srcID)
	dataEnd := s.pa.GetDataEnd(rawSrcID)
	err = s.pa.InsertLeafEntry(rawSrcID, 0, []byte("key1"), []byte("val1"), &dataEnd)
	require.NoError(t, err)

	// COW copy
	newID, newLeaf, err := s.CopyLeafPage(srcID)
	require.NoError(t, err)
	assert.NotEqual(t, srcID, newID, "COW must return different pageID")

	// Verify data copied
	idx, found := newLeaf.Search([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, 0, idx)
	assert.Equal(t, []byte("val1"), newLeaf.GetValue(idx))
}

func TestCopyNodePage(t *testing.T) {
	s := newTestStorage(t)

	srcID, err := s.AllocNodePage()
	require.NoError(t, err)

	newID, newNode, err := s.CopyNodePage(srcID)
	require.NoError(t, err)
	assert.NotEqual(t, srcID, newID)
	assert.False(t, newNode.IsLeaf())
}

func TestCopyPageVersionIncrement(t *testing.T) {
	s := newTestStorage(t)

	srcID, err := s.AllocLeafPage()
	require.NoError(t, err)

	srcVersion := s.pa.GetVersion(uint32(srcID))
	assert.Equal(t, uint64(1), srcVersion, "new leaf page version should be 1")

	newID, _, err := s.CopyLeafPage(srcID)
	require.NoError(t, err)

	dstVersion := s.pa.GetVersion(uint32(newID))
	assert.Equal(t, srcVersion+1, dstVersion, "COW copy must increment version")
}

func TestCopyLeafPageOriginalImmutable(t *testing.T) {
	s := newTestStorage(t)

	srcID, err := s.AllocLeafPage()
	require.NoError(t, err)

	rawSrcID := uint32(srcID)
	dataEnd := s.pa.GetDataEnd(rawSrcID)
	err = s.pa.InsertLeafEntry(rawSrcID, 0, []byte("key1"), []byte("val1"), &dataEnd)
	require.NoError(t, err)

	srcCount := int(s.pa.GetCount(rawSrcID))

	// COW copy
	_, newLeaf, err := s.CopyLeafPage(srcID)
	require.NoError(t, err)

	// Modify the copy
	dataEnd2 := s.pa.GetDataEnd(uint32(newLeaf.PageID()))
	err = s.pa.InsertLeafEntry(uint32(newLeaf.PageID()), 1, []byte("key2"), []byte("val2"), &dataEnd2)
	require.NoError(t, err)

	// Verify original unchanged
	assert.Equal(t, srcCount, int(s.pa.GetCount(rawSrcID)), "original page count must not change")
}

// --- Page Access Tests ---

func TestGetLeafPage(t *testing.T) {
	s := newTestStorage(t)

	id, err := s.AllocLeafPage()
	require.NoError(t, err)

	leaf, err := s.GetLeafPage(id)
	require.NoError(t, err)
	assert.Equal(t, id, leaf.PageID())
	assert.True(t, leaf.IsLeaf())
}

func TestGetNodePage(t *testing.T) {
	s := newTestStorage(t)

	id, err := s.AllocNodePage()
	require.NoError(t, err)

	node, err := s.GetNodePage(id)
	require.NoError(t, err)
	assert.Equal(t, id, node.PageID())
	assert.False(t, node.IsLeaf())
}

func TestGetLeafPage_WrongType(t *testing.T) {
	s := newTestStorage(t)

	id, err := s.AllocNodePage()
	require.NoError(t, err)

	_, err = s.GetLeafPage(id)
	assert.Error(t, err, "getting leaf handle for node page should fail")
}

func TestGetNodePage_WrongType(t *testing.T) {
	s := newTestStorage(t)

	id, err := s.AllocLeafPage()
	require.NoError(t, err)

	_, err = s.GetNodePage(id)
	assert.Error(t, err, "getting node handle for leaf page should fail")
}

// --- Close Tests ---

func TestClose(t *testing.T) {
	s, err := NewOffheapBTreeStorage(testMmapSize)
	require.NoError(t, err)

	require.NoError(t, s.Close())

	// After close, operations should fail (pm is closed)
	_, err = s.AllocLeafPage()
	assert.Error(t, err)
}

func TestDoubleClose(t *testing.T) {
	s, err := NewOffheapBTreeStorage(testMmapSize)
	require.NoError(t, err)

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "double close should not panic")
}

// --- Validation Tests ---

func TestPageIDValidation(t *testing.T) {
	s := newTestStorage(t)

	// uint32 max is valid
	rawID, err := s.validatePageID(model.PageID(math.MaxUint32))
	assert.NoError(t, err)
	assert.Equal(t, uint32(math.MaxUint32), rawID)

	// uint32 max + 1 is invalid
	_, err = s.validatePageID(model.PageID(math.MaxUint32) + 1)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidPage))
}

// --- Concurrent Tests ---

func TestConcurrentAllocFree(t *testing.T) {
	s := newTestStorage(t)

	const goroutines = 10
	const opsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range opsPerGoroutine {
				id, err := s.AllocLeafPage()
				if err != nil {
					t.Logf("alloc failed: %v", err)
					continue
				}
				if err := s.FreePage(id); err != nil {
					t.Logf("free failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()
}

// --- Phase 6.5 Stub Tests ---

func TestMergeLeavesStub(t *testing.T) {
	s := newTestStorage(t)
	_, err := s.MergeLeaves(nil, nil)
	assert.True(t, errors.Is(err, ErrNotImplemented))
}

func TestBorrowStubs(t *testing.T) {
	s := newTestStorage(t)

	_, _, err := s.BorrowFromLeftLeaf(nil, nil)
	assert.True(t, errors.Is(err, ErrNotImplemented))

	_, _, err = s.BorrowFromRightLeaf(nil, nil)
	assert.True(t, errors.Is(err, ErrNotImplemented))

	_, err = s.MergeNodes(nil, nil, nil)
	assert.True(t, errors.Is(err, ErrNotImplemented))

	_, _, _, err = s.BorrowFromLeftNode(nil, nil, nil)
	assert.True(t, errors.Is(err, ErrNotImplemented))

	_, _, _, err = s.BorrowFromRightNode(nil, nil, nil)
	assert.True(t, errors.Is(err, ErrNotImplemented))
}
