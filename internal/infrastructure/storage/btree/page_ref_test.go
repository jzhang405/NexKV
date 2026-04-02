// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// --- Helpers ---

// newTestPageRef creates a PageRef with a dummy freeFunc that records calls.
func newTestPageRef(t *testing.T, pageID model.PageID, version uint64, parent *PageRef) (*PageRef, *atomic.Bool) {
	t.Helper()
	freed := &atomic.Bool{}
	freeFunc := func(id model.PageID) {
		freed.Store(true)
	}
	r := NewPageRef(pageID, version, parent, freeFunc)
	return r, freed
}

// --- PageInfo Tests ---

func TestPageInfoImmutable(t *testing.T) {
	info := &PageInfo{PageID: 42, Version: 7}
	assert.Equal(t, model.PageID(42), info.PageID)
	assert.Equal(t, uint64(7), info.Version)
}

// --- PageRef Core Tests ---

func TestPageRefNewGetPageInfo(t *testing.T) {
	r, _ := newTestPageRef(t, 42, 7, nil)

	info := r.GetPageInfo()
	require.NotNil(t, info)
	assert.Equal(t, model.PageID(42), info.PageID)
	assert.Equal(t, uint64(7), info.Version)
}

func TestPageRefCASSuccess(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)

	old := r.GetPageInfo()
	newInfo := &PageInfo{PageID: 2, Version: 2}

	assert.True(t, r.CAS(old, newInfo))

	current := r.GetPageInfo()
	assert.Equal(t, model.PageID(2), current.PageID)
	assert.Equal(t, uint64(2), current.Version)
}

func TestPageRefCASConflict(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)

	old := r.GetPageInfo()
	// Simulate concurrent modification
	r.pInfo.Store(&PageInfo{PageID: 99, Version: 99})

	newInfo := &PageInfo{PageID: 2, Version: 2}
	assert.False(t, r.CAS(old, newInfo))

	// Current should be the concurrently written value
	current := r.GetPageInfo()
	assert.Equal(t, model.PageID(99), current.PageID)
}

func TestPageRefRetainRelease(t *testing.T) {
	r, freed := newTestPageRef(t, 1, 1, nil)

	r.Retain()
	r.Retain()
	assert.Equal(t, int32(2), r.RefCount())

	r.Release()
	assert.False(t, freed.Load(), "page should not be freed yet")
	assert.Equal(t, int32(1), r.RefCount())

	r.Release()
	assert.True(t, freed.Load(), "page should be freed when refCount hits 0")
}

func TestPageRefReleaseFree(t *testing.T) {
	r, freed := newTestPageRef(t, 5, 1, nil)
	assert.False(t, freed.Load())

	r.Retain()  // refCount: 0 → 1
	r.Release() // refCount: 1 → 0, triggers freeFunc
	assert.True(t, freed.Load(), "freeFunc should be called when refCount reaches 0")
}

func TestPageRefParentRef(t *testing.T) {
	parent, _ := newTestPageRef(t, 1, 1, nil)
	child, _ := newTestPageRef(t, 2, 1, parent)

	assert.Equal(t, parent, child.GetParentRef())
	assert.Nil(t, parent.GetParentRef())
}

func TestPageRefGetPathToRoot(t *testing.T) {
	root, _ := newTestPageRef(t, 1, 1, nil)
	mid, _ := newTestPageRef(t, 2, 1, root)
	leaf, _ := newTestPageRef(t, 3, 1, mid)

	path := leaf.GetPathToRoot()
	require.Len(t, path, 3)
	assert.Equal(t, model.PageID(3), path[0].GetPageInfo().PageID)
	assert.Equal(t, model.PageID(2), path[1].GetPageInfo().PageID)
	assert.Equal(t, model.PageID(1), path[2].GetPageInfo().PageID)
}

// --- SplitMarker Tests ---

func TestPageRefSplitMarker(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)
	left, _ := newTestPageRef(t, 2, 1, nil)
	right, _ := newTestPageRef(t, 3, 1, nil)

	// Initially no marker
	assert.Nil(t, r.GetSplitMarker())

	// Set marker
	r.SetSplitMarker(left, right, []byte("e"))

	marker := r.GetSplitMarker()
	require.NotNil(t, marker)
	assert.Equal(t, left, marker.Left)
	assert.Equal(t, right, marker.Right)
	assert.Equal(t, []byte("e"), marker.SplitKey)

	// FollowSplit: key < splitKey → left
	target, ok := r.FollowSplit([]byte("a"))
	assert.True(t, ok)
	assert.Equal(t, left, target)

	// FollowSplit: key >= splitKey → right
	target, ok = r.FollowSplit([]byte("e"))
	assert.True(t, ok)
	assert.Equal(t, right, target)

	target, ok = r.FollowSplit([]byte("z"))
	assert.True(t, ok)
	assert.Equal(t, right, target)
}

func TestPageRefFollowSplitNoMarker(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)

	target, ok := r.FollowSplit([]byte("a"))
	assert.False(t, ok)
	assert.Nil(t, target)
}

// --- GetOrCreateChildren Tests ---

func TestPageRefGetOrCreateChildrenLeaf(t *testing.T) {
	s := newTestStorage(t)
	id, err := s.AllocLeafPage()
	require.NoError(t, err)

	r, _ := newTestPageRef(t, id, 1, nil)

	children := r.GetOrCreateChildren(s)
	assert.Nil(t, children, "leaf page should have no children")
}

func TestPageRefGetOrCreateChildrenNode(t *testing.T) {
	s := newTestStorage(t)

	// Consume pageID 0 (sentinel for InsertIndexEntry child!=0 constraint)
	sentinel, _ := s.AllocNodePage()
	_ = sentinel

	// Allocate children
	c1, _ := s.AllocNodePage()
	c2, _ := s.AllocNodePage()

	// Create root node with key "e" and children c1, c2
	rootID, _ := s.AllocNodePage()
	dataEnd := s.pa.GetDataEnd(uint32(rootID))
	require.NoError(t, s.pa.InsertIndexEntry(uint32(rootID), 0, []byte("e"), uint32(c1), &dataEnd))
	s.pa.SetChild(uint32(rootID), 1, uint32(c2))

	r, _ := newTestPageRef(t, rootID, 1, nil)
	children := r.GetOrCreateChildren(s)

	require.Len(t, children, 2)
	assert.Equal(t, c1, children[0].GetPageInfo().PageID)
	assert.Equal(t, c2, children[1].GetPageInfo().PageID)
	assert.Equal(t, r, children[0].GetParentRef())
	assert.Equal(t, r, children[1].GetParentRef())
}

func TestPageRefGetOrCreateChildrenConcurrent(t *testing.T) {
	s := newTestStorage(t)

	sentinel, _ := s.AllocNodePage()
	_ = sentinel

	c1, _ := s.AllocNodePage()
	c2, _ := s.AllocNodePage()

	rootID, _ := s.AllocNodePage()
	dataEnd := s.pa.GetDataEnd(uint32(rootID))
	require.NoError(t, s.pa.InsertIndexEntry(uint32(rootID), 0, []byte("e"), uint32(c1), &dataEnd))
	s.pa.SetChild(uint32(rootID), 1, uint32(c2))

	r, _ := newTestPageRef(t, rootID, 1, nil)

	const goroutines = 10
	var wg sync.WaitGroup
	results := make([][]*PageRef, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = r.GetOrCreateChildren(s)
		}(i)
	}
	wg.Wait()

	// All goroutines should get the same children slice
	for i := 1; i < goroutines; i++ {
		assert.Equal(t, results[0], results[i], "all goroutines should see same children")
	}
}

// --- RootPageRef Tests ---

func TestRootPageRefNew(t *testing.T) {
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	info := root.GetPageInfo()
	require.NotNil(t, info)
	assert.Equal(t, model.PageID(1), info.PageID)
	assert.Equal(t, uint64(1), info.Version)
	assert.Nil(t, root.GetParentRef(), "root should have no parent")
}

func TestRootPageRefReplaceRoot(t *testing.T) {
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	oldInfo := root.GetPageInfo()
	newInfo := &PageInfo{PageID: 2, Version: 2}

	ok := root.ReplaceRoot(oldInfo, newInfo, nil)
	assert.True(t, ok)

	current := root.GetPageInfo()
	assert.Equal(t, model.PageID(2), current.PageID)
}

func TestRootPageRefTryFollowSplit(t *testing.T) {
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	// No split initially
	_, ok := root.TryFollowSplit()
	assert.False(t, ok)

	// Set split marker
	left := NewPageRef(2, 2, nil, func(model.PageID) {})
	right := NewPageRef(3, 2, nil, func(model.PageID) {})
	root.SetSplitMarker(left, right, []byte("m"))

	marker, ok := root.TryFollowSplit()
	assert.True(t, ok)
	require.NotNil(t, marker)
	assert.Equal(t, left, marker.Left)
	assert.Equal(t, right, marker.Right)
}

// --- SchedulerLock Tests ---

func TestSchedulerLockBasic(t *testing.T) {
	var lock SchedulerLock

	func() {
		lock.Lock()
		defer lock.Unlock()
		_ = true // non-empty critical section
	}()

	// Verify unlocked state allows re-acquisition
	func() {
		lock.Lock()
		defer lock.Unlock()
		_ = true
	}()
}

func TestSchedulerLockConcurrent(t *testing.T) {
	var lock SchedulerLock
	var counter atomic.Int32

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			lock.Lock()
			defer lock.Unlock()
			counter.Add(1)
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(goroutines), counter.Load())
}

// --- Concurrent CAS Test ---

func TestConcurrentCAS(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)

	// Capture old BEFORE spawning goroutines — all compete with same old
	old := r.GetPageInfo()

	const goroutines = 10
	var successCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(version uint64) {
			defer wg.Done()
			newInfo := &PageInfo{PageID: model.PageID(version + 100), Version: version}
			if r.CAS(old, newInfo) {
				successCount.Add(1)
			}
		}(uint64(i))
	}
	wg.Wait()

	assert.Equal(t, int32(1), successCount.Load(), "exactly one CAS should succeed")
}

// --- C1: Release underflow protection ---

func TestPageRefReleaseUnderflowPanics(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)

	assert.Panics(t, func() {
		r.Release() // refCount 0 → -1 → panic
	}, "Release on zero refCount should panic")
}

func TestPageRefDoubleReleasePanics(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)

	r.Retain()  // 0 → 1
	r.Release() // 1 → 0, triggers freeFunc
	assert.Panics(t, func() {
		r.Release() // 0 → -1 → panic
	}, "double Release should panic")
}

// --- C2: ReplaceRoot with children propagation ---

func TestRootPageRefReplaceRootWithChildren(t *testing.T) {
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	child1 := NewPageRef(10, 1, nil, func(model.PageID) {})
	child2 := NewPageRef(20, 1, nil, func(model.PageID) {})

	oldInfo := root.GetPageInfo()
	newInfo := &PageInfo{PageID: 2, Version: 2}

	ok := root.ReplaceRoot(oldInfo, newInfo, []*PageRef{child1, child2})
	assert.True(t, ok)

	// Children should have parentRef pointing to root's embedded PageRef
	assert.Equal(t, &root.PageRef, child1.GetParentRef())
	assert.Equal(t, &root.PageRef, child2.GetParentRef())
}

func TestRootPageRefReplaceRootConflict(t *testing.T) {
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	// Get old info BEFORE concurrent modification
	oldInfo := root.GetPageInfo()

	// Simulate concurrent modification AFTER we captured oldInfo
	root.pInfo.Store(&PageInfo{PageID: 99, Version: 99})

	// Now ReplaceRoot should fail because current != oldInfo
	newInfo := &PageInfo{PageID: 2, Version: 2}
	ok := root.ReplaceRoot(oldInfo, newInfo, nil)
	assert.False(t, ok)

	// Current should remain the concurrently written value
	current := root.GetPageInfo()
	assert.Equal(t, model.PageID(99), current.PageID)
}

// --- C4: SetParentRef / GetParentRef atomic ---

func TestPageRefSetParentRef(t *testing.T) {
	parent, _ := newTestPageRef(t, 1, 1, nil)
	child, _ := newTestPageRef(t, 2, 1, nil)

	assert.Nil(t, child.GetParentRef())

	child.SetParentRef(parent)
	assert.Equal(t, parent, child.GetParentRef())

	child.SetParentRef(nil)
	assert.Nil(t, child.GetParentRef())
}

func TestPageRefSetParentRefConcurrent(t *testing.T) {
	child, _ := newTestPageRef(t, 1, 1, nil)

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	parents := make([]*PageRef, goroutines)
	for i := range goroutines {
		parents[i], _ = newTestPageRef(t, model.PageID(i+100), 1, nil)
	}

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			child.SetParentRef(parents[idx])
		}(i)
	}
	wg.Wait()

	// Should have some valid parent (no race detector failure)
	final := child.GetParentRef()
	assert.NotNil(t, final)
}

// --- SplitMarker + RootPageRef interaction ---

func TestRootPageRefSplitMarkerInteraction(t *testing.T) {
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	// Initially no split
	marker, ok := root.TryFollowSplit()
	assert.False(t, ok)
	assert.Nil(t, marker)

	// Set split marker on root
	left := NewPageRef(2, 2, nil, func(model.PageID) {})
	right := NewPageRef(3, 2, nil, func(model.PageID) {})
	root.SetSplitMarker(left, right, []byte("m"))

	// TryFollowSplit should now return the marker
	marker, ok = root.TryFollowSplit()
	assert.True(t, ok)
	require.NotNil(t, marker)
	assert.Equal(t, left, marker.Left)
	assert.Equal(t, right, marker.Right)
	assert.Equal(t, []byte("m"), marker.SplitKey)

	// ReplaceRoot should not clear the split marker
	oldInfo := root.GetPageInfo()
	newInfo := &PageInfo{PageID: 4, Version: 3}
	ok = root.ReplaceRoot(oldInfo, newInfo, nil)
	assert.True(t, ok)

	// Split marker should still be accessible
	marker2, ok := root.TryFollowSplit()
	assert.True(t, ok)
	assert.Equal(t, marker, marker2)
}

// --- Fix Verification Tests ---

// TestPageRefReleaseCorrectPageID verifies C1 fix:
// After CAS replaces pInfo (changing PageID), Release should free the
// ORIGINAL pageID (bound at creation), not the new one from pInfo.
func TestPageRefReleaseCorrectPageID(t *testing.T) {
	var freedID atomic.Int64
	freeFunc := func(id model.PageID) {
		freedID.Store(int64(id))
	}

	r := NewPageRef(42, 1, nil, freeFunc)

	// Simulate COW: CAS replaces pInfo with a new PageID
	oldInfo := r.GetPageInfo()
	newInfo := &PageInfo{PageID: 99, Version: 2}
	require.True(t, r.CAS(oldInfo, newInfo))

	// Retain + Release to trigger freeFunc
	r.Retain()  // 0 → 1
	r.Release() // 1 → 0, should free pageID=42 (original), NOT 99

	assert.Equal(t, int64(42), freedID.Load(),
		"Release should free the original pageID bound at creation, not the CAS'd one")
}

// TestSplitMarkerKeyCopy verifies I1 fix:
// Modifying the original splitKey after SetSplitMarker should not
// affect the marker's internal copy.
func TestSplitMarkerKeyCopy(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)
	left, _ := newTestPageRef(t, 2, 1, nil)
	right, _ := newTestPageRef(t, 3, 1, nil)

	// Use a mutable slice as splitKey
	key := []byte("hello")
	r.SetSplitMarker(left, right, key)

	// Mutate the original slice
	key[0] = 'z'

	marker := r.GetSplitMarker()
	require.NotNil(t, marker)
	assert.Equal(t, []byte("hello"), marker.SplitKey,
		"marker should have its own copy, immune to caller mutation")
}

// TestSchedulerLockDoubleUnlockPanics verifies I2 fix:
// Double-unlock should panic to catch programming errors early.
func TestSchedulerLockDoubleUnlockPanics(t *testing.T) {
	var lock SchedulerLock

	// First lock/unlock cycle — put lock into unlocked state
	func() {
		lock.Lock()
		defer lock.Unlock()
		_ = true
	}()

	assert.Panics(t, func() {
		lock.Unlock() // double-unlock should panic
	}, "double Unlock should panic")
}

// --- PageRef Lock/Unlock Tests ---

func TestPageRef_LockUnlock(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)

	// Lock/Unlock should protect a read-modify-write without panic
	r.Lock()
	info := r.GetPageInfo()
	info.Version++ //nolint:staticcheck // SA4001: intentional in-place modification for test
	r.Unlock()
	_ = info
}

func TestPageRef_LockConcurrency(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)

	var wg sync.WaitGroup
	const n = 100
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			r.Lock()
			_ = r.GetPageInfo() // non-empty critical section
			r.Unlock()
		}()
	}
	wg.Wait()
}
