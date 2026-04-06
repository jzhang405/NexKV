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

// --- Redirect Tests ---

func TestPageRefRedirect(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)
	left, _ := newTestPageRef(t, 2, 1, nil)

	// Initially no redirect
	info := r.GetPageInfo()
	assert.False(t, info.Redirect)
	assert.Nil(t, info.NewRef)

	// Set redirect via CAS
	newInfo := &PageInfo{
		PageID:   info.PageID,
		Version:  info.Version + 1,
		Redirect: true,
		NewRef:   left,
	}
	assert.True(t, r.CAS(info, newInfo))

	// Verify redirect fields
	updated := r.GetPageInfo()
	assert.True(t, updated.Redirect)
	assert.Equal(t, left, updated.NewRef)
}

func TestPageRefRedirectCASAtomic(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)
	left, _ := newTestPageRef(t, 2, 1, nil)

	// Tombstone + Redirect + NewRef set in single CAS — no window gap
	oldInfo := r.GetPageInfo()
	redirectInfo := &PageInfo{
		PageID:   oldInfo.PageID,
		Version:  oldInfo.Version + 1,
		Redirect: true,
		NewRef:   left,
	}
	assert.True(t, r.CAS(oldInfo, redirectInfo))

	// Second CAS with same oldInfo should fail
	right, _ := newTestPageRef(t, 3, 1, nil)
	failInfo := &PageInfo{
		PageID:   oldInfo.PageID,
		Version:  oldInfo.Version + 2,
		Redirect: true,
		NewRef:   right,
	}
	assert.False(t, r.CAS(oldInfo, failInfo))
}

// --- GetOrCreateChildren Tests ---

func TestPageRefGetOrCreateChildrenLeaf(t *testing.T) {
	s := newTestStorage(t)
	id, err := s.AllocLeafPage()
	require.NoError(t, err)

	r, _ := newTestPageRef(t, id, 1, nil)

	children, err := r.GetOrCreateChildren(s)
	assert.NoError(t, err)
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
	// Mark as internal node so GetOrCreateChildren doesn't fast-return on IsLeaf check
	r.pInfo.Store(&PageInfo{PageID: rootID, Version: 1, IsLeaf: false, NodeState: NodeNormal})
	children, err := r.GetOrCreateChildren(s)
	require.NoError(t, err)
	require.NotNil(t, children)
	require.Len(t, children.Children, 2)
	assert.Equal(t, c1, children.Children[0].GetPageInfo().PageID)
	assert.Equal(t, c2, children.Children[1].GetPageInfo().PageID)
	assert.Equal(t, r, children.Children[0].GetParentRef())
	assert.Equal(t, r, children.Children[1].GetParentRef())
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
	// Mark as internal node
	r.pInfo.Store(&PageInfo{PageID: rootID, Version: 1, IsLeaf: false, NodeState: NodeNormal})

	const goroutines = 10
	var wg sync.WaitGroup
	results := make([]*ChildrenCache, goroutines)
	errResults := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			children, err := r.GetOrCreateChildren(s)
			results[idx] = children
			errResults[idx] = err
		}(i)
	}
	wg.Wait()

	// All goroutines should get the same children slice
	for i := 1; i < goroutines; i++ {
		assert.Equal(t, results[0], results[i], "all goroutines should see same children")
		assert.NoError(t, errResults[i], "GetOrCreateChildren should not return error")
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

func TestRootPageRefNoRedirectNeeded(t *testing.T) {
	// Root Split uses ReplaceRoot which atomically replaces pInfo + sets children.
	// No Redirect needed on root — concurrent readers either see old leaf (correct)
	// or new internal root (correct, with children already set via B20 fix).
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	// Verify root has no Redirect
	info := root.GetPageInfo()
	assert.False(t, info.Redirect)
	assert.Nil(t, info.NewRef)

	// Simulate root split: ReplaceRoot atomically switches to new internal root
	left := NewPageRef(2, 2, nil, func(model.PageID) {})
	right := NewPageRef(3, 2, nil, func(model.PageID) {})

	newRootInfo := &PageInfo{
		PageID:  10, // new internal root page
		Version: info.Version + 1,
	}
	newChildren := &ChildrenCache{Children: []*PageRef{left, right}}
	assert.True(t, root.ReplaceRoot(info, newRootInfo, newChildren))

	// After ReplaceRoot: root's pInfo is the new internal root (no Redirect)
	updated := root.GetPageInfo()
	assert.Equal(t, model.PageID(10), updated.PageID)
	assert.False(t, updated.Redirect)
	assert.Nil(t, updated.NewRef)
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

	ok := root.ReplaceRoot(oldInfo, newInfo, &ChildrenCache{Children: []*PageRef{child1, child2}})
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

// TestPageInfoRedirectImmutable verifies redirect info is read-only via GetPageInfo.
// PageInfo is immutable after CAS — readers see consistent snapshots.
func TestPageInfoRedirectImmutable(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1, nil)
	left, _ := newTestPageRef(t, 2, 1, nil)

	oldInfo := r.GetPageInfo()
	redirectInfo := &PageInfo{
		PageID:   oldInfo.PageID,
		Version:  oldInfo.Version + 1,
		Redirect: true,
		NewRef:   left,
	}
	require.True(t, r.CAS(oldInfo, redirectInfo))

	// Read back — should match exactly
	got := r.GetPageInfo()
	assert.True(t, got.Redirect)
	assert.Equal(t, left, got.NewRef)
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

	for range n {
		go func() {
			defer wg.Done()
			r.Lock()
			_ = r.GetPageInfo() // non-empty critical section
			r.Unlock()
		}()
	}
	wg.Wait()
}

// --- Redirect RefCount Tests ---

// TestPageRef_Redirect_NewRefRefCount verifies that NewRef in PageInfo
// does NOT need explicit Retain — it's just a pointer in an immutable PageInfo.
// The PageRef lifecycle is managed by the parent's children cache.
func TestPageRef_Redirect_NewRefRefCount(t *testing.T) {
	var freedLeft atomic.Int64

	left := NewPageRef(10, 1, nil, func(id model.PageID) {
		freedLeft.Store(int64(id))
	})

	// Retain leftRef (as handleLeafSplit does in Step 3)
	left.Retain()
	assert.Equal(t, int32(1), left.RefCount())

	// Set redirect — NewRef is a pointer in PageInfo, no extra Retain
	parent := NewPageRef(1, 1, nil, nil)
	oldInfo := parent.GetPageInfo()
	redirectInfo := &PageInfo{
		PageID:   oldInfo.PageID,
		Version:  oldInfo.Version + 1,
		Redirect: true,
		NewRef:   left,
	}
	require.True(t, parent.CAS(oldInfo, redirectInfo))

	// leftRef still refCount=1 (only our Retain, no extra from redirect)
	assert.Equal(t, int32(1), left.RefCount())
	assert.Equal(t, int64(0), freedLeft.Load(), "left not freed while Retained")

	// Release our reference → refCount=0 → freed
	left.Release()
	assert.Equal(t, int32(0), left.RefCount())
	assert.Equal(t, int64(10), freedLeft.Load(), "left freed after Release")
}

// TestHandleLeafSplit_CASFailure_Cleanup verifies C1/C2 fix:
// When handleLeafSplit's parent CAS fails, all allocated resources should be cleaned up.
func TestHandleLeafSplit_CASFailure_Cleanup(t *testing.T) {
	// This test will be implemented when handleLeafSplit is added in Phase 6.0.1
	// For now, we test the PageRef cleanup pattern
	var freedPages []model.PageID
	var freedMu sync.Mutex

	freeFunc := func(id model.PageID) {
		freedMu.Lock()
		freedPages = append(freedPages, id)
		freedMu.Unlock()
	}

	// ✅ C1 fix: Immediately Retain after creation
	left := NewPageRef(10, 1, nil, freeFunc)
	right := NewPageRef(20, 1, nil, freeFunc)
	left.Retain()  // ✅ Prevent premature release
	right.Retain() // ✅ Prevent premature release

	assert.Equal(t, int32(1), left.RefCount())
	assert.Equal(t, int32(1), right.RefCount())

	// ✅ C2 fix: Complete cleanup on failure
	left.Release()
	right.Release()

	// Verify pages are freed
	freedMu.Lock()
	freedIDs := make([]model.PageID, len(freedPages))
	copy(freedIDs, freedPages)
	freedMu.Unlock()

	assert.Contains(t, freedIDs, model.PageID(10), "left should be freed after Release")
	assert.Contains(t, freedIDs, model.PageID(20), "right should be freed after Release")
}

// TestHandleRootSplit_ReplaceRoot verifies C5 fix:
// handleRootSplit should use ReplaceRoot instead of CompareAndSwap.
func TestHandleRootSplit_ReplaceRoot(t *testing.T) {
	// This test will be implemented when handleRootSplit is added in Phase 6.0.4
	// For now, we test the ReplaceRoot API
	var freedOldRoot atomic.Int64

	oldRoot := NewRootPageRef(1, 1, func(id model.PageID) {
		freedOldRoot.Store(int64(id))
	})

	// Create new children
	left := NewPageRef(10, 1, nil, nil)
	right := NewPageRef(20, 1, nil, nil)

	// ✅ C5 fix: Use ReplaceRoot with children
	oldInfo := oldRoot.GetPageInfo()
	newInfo := &PageInfo{
		PageID:  2,
		Version: oldInfo.Version + 1,
	}
	newChildren := &ChildrenCache{Children: []*PageRef{left, right}}

	success := oldRoot.ReplaceRoot(oldInfo, newInfo, newChildren)
	assert.True(t, success, "ReplaceRoot should succeed")

	// Verify parent ref is set for children
	assert.Equal(t, &oldRoot.PageRef, left.GetParentRef(), "left's parent should be root")
	assert.Equal(t, &oldRoot.PageRef, right.GetParentRef(), "right's parent should be root")

	// Verify version is incremented
	assert.Equal(t, uint64(2), oldRoot.GetPageInfo().Version, "root version should be incremented")
}

// TestPageRef_Redirect_ConcurrentCAS verifies concurrent CAS on PageInfo
// with Redirect+NewRef fields is safe (no data race).
func TestPageRef_Redirect_ConcurrentCAS(t *testing.T) {
	parent := NewPageRef(1, 1, nil, nil)

	var wg sync.WaitGroup
	const numGoroutines = 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Writer: CAS redirect info
		go func(id int) {
			defer wg.Done()
			ref := NewPageRef(model.PageID(id*2), 1, nil, nil)
			for {
				old := parent.GetPageInfo()
				newInfo := &PageInfo{
					PageID:   old.PageID,
					Version:  old.Version + 1,
					Redirect: true,
					NewRef:   ref,
				}
				if parent.CAS(old, newInfo) {
					break
				}
			}
		}(i)

		// Reader: GetPageInfo (atomic read)
		go func() {
			defer wg.Done()
			info := parent.GetPageInfo()
			if info.Redirect && info.NewRef != nil {
				_ = info.NewRef.GetPageInfo()
			}
		}()
	}

	wg.Wait()
	// ✅ No race condition, no panic
}
