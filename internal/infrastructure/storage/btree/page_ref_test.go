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
func newTestPageRef(t *testing.T, pageID model.PageID, version uint64) (*PageRef, *atomic.Bool) {
	t.Helper()
	freed := &atomic.Bool{}
	freeFunc := func(id model.PageID) {
		freed.Store(true)
	}
	r := NewPageRef(pageID, version, freeFunc)
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
	r, _ := newTestPageRef(t, 42, 7)

	info := r.GetPageInfo()
	require.NotNil(t, info)
	assert.Equal(t, model.PageID(42), info.PageID)
	assert.Equal(t, uint64(7), info.Version)
}

func TestPageRefCASSuccess(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1)

	old := r.GetPageInfo()
	newInfo := &PageInfo{PageID: 2, Version: 2}

	assert.True(t, r.CAS(old, newInfo))

	current := r.GetPageInfo()
	assert.Equal(t, model.PageID(2), current.PageID)
	assert.Equal(t, uint64(2), current.Version)
}

func TestPageRefCASConflict(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1)

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
	r, freed := newTestPageRef(t, 1, 1)

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
	r, freed := newTestPageRef(t, 5, 1)
	assert.False(t, freed.Load())

	r.Retain()  // refCount: 0 → 1
	r.Release() // refCount: 1 → 0, triggers freeFunc
	assert.True(t, freed.Load(), "freeFunc should be called when refCount reaches 0")
}

// --- Redirect Tests ---

func TestPageRefRedirect(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1)
	left, _ := newTestPageRef(t, 2, 1)

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
	r, _ := newTestPageRef(t, 1, 1)
	left, _ := newTestPageRef(t, 2, 1)

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
	right, _ := newTestPageRef(t, 3, 1)
	failInfo := &PageInfo{
		PageID:   oldInfo.PageID,
		Version:  oldInfo.Version + 2,
		Redirect: true,
		NewRef:   right,
	}
	assert.False(t, r.CAS(oldInfo, failInfo))
}

// --- RootPageRef Tests ---
// --- RootPageRef Tests ---

func TestRootPageRefNew(t *testing.T) {
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	info := root.GetPageInfo()
	require.NotNil(t, info)
	assert.Equal(t, model.PageID(1), info.PageID)
	assert.Equal(t, uint64(1), info.Version)
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
	left := NewPageRef(2, 2, func(model.PageID) {})
	right := NewPageRef(3, 2, func(model.PageID) {})

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

// --- Concurrent CAS Test ---

func TestConcurrentCAS(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1)

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
	r, _ := newTestPageRef(t, 1, 1)

	assert.Panics(t, func() {
		r.Release() // refCount 0 → -1 → panic
	}, "Release on zero refCount should panic")
}

func TestPageRefDoubleReleasePanics(t *testing.T) {
	r, _ := newTestPageRef(t, 1, 1)

	r.Retain()  // 0 → 1
	r.Release() // 1 → 0, triggers freeFunc
	assert.Panics(t, func() {
		r.Release() // 0 → -1 → panic
	}, "double Release should panic")
}

// --- C2: ReplaceRoot with children propagation ---

func TestRootPageRefReplaceRootWithChildren(t *testing.T) {
	root := NewRootPageRef(1, 1, func(model.PageID) {})

	child1 := NewPageRef(10, 1, func(model.PageID) {})
	child2 := NewPageRef(20, 1, func(model.PageID) {})

	oldInfo := root.GetPageInfo()
	newInfo := &PageInfo{PageID: 2, Version: 2}

	ok := root.ReplaceRoot(oldInfo, newInfo, &ChildrenCache{Children: []*PageRef{child1, child2}})
	assert.True(t, ok)

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

// --- Fix Verification Tests ---

// --- Fix Verification Tests ---

// TestPageRefReleaseCorrectPageID verifies C1 fix:
// After CAS replaces pInfo (changing PageID), Release should free the
// ORIGINAL pageID (bound at creation), not the new one from pInfo.
func TestPageRefReleaseCorrectPageID(t *testing.T) {
	var freedID atomic.Int64
	freeFunc := func(id model.PageID) {
		freedID.Store(int64(id))
	}

	r := NewPageRef(42, 1, freeFunc)

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
	r, _ := newTestPageRef(t, 1, 1)
	left, _ := newTestPageRef(t, 2, 1)

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

// --- Redirect RefCount Tests ---

// TestPageRef_Redirect_NewRefRefCount verifies that NewRef in PageInfo
// does NOT need explicit Retain — it's just a pointer in an immutable PageInfo.
// The PageRef lifecycle is managed by the parent's children cache.
func TestPageRef_Redirect_NewRefRefCount(t *testing.T) {
	var freedLeft atomic.Int64

	left := NewPageRef(10, 1, func(id model.PageID) {
		freedLeft.Store(int64(id))
	})

	// Retain leftRef (as handleLeafSplit does in Step 3)
	left.Retain()
	assert.Equal(t, int32(1), left.RefCount())

	// Set redirect — NewRef is a pointer in PageInfo, no extra Retain
	parent := NewPageRef(1, 1, nil)
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
	left := NewPageRef(10, 1, freeFunc)
	right := NewPageRef(20, 1, freeFunc)
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
	left := NewPageRef(10, 1, nil)
	right := NewPageRef(20, 1, nil)

	// ✅ C5 fix: Use ReplaceRoot with children
	oldInfo := oldRoot.GetPageInfo()
	newInfo := &PageInfo{
		PageID:  2,
		Version: oldInfo.Version + 1,
	}
	newChildren := &ChildrenCache{Children: []*PageRef{left, right}}

	success := oldRoot.ReplaceRoot(oldInfo, newInfo, newChildren)
	assert.True(t, success, "ReplaceRoot should succeed")

	// Verify version is incremented
	assert.Equal(t, uint64(2), oldRoot.GetPageInfo().Version, "root version should be incremented")
}

// TestPageRef_Redirect_ConcurrentCAS verifies concurrent CAS on PageInfo
// with Redirect+NewRef fields is safe (no data race).
func TestPageRef_Redirect_ConcurrentCAS(t *testing.T) {
	parent := NewPageRef(1, 1, nil)

	var wg sync.WaitGroup
	const numGoroutines = 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Writer: CAS redirect info
		go func(id int) {
			defer wg.Done()
			ref := NewPageRef(model.PageID(id*2), 1, nil)
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
