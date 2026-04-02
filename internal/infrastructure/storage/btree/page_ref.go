// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// SplitMarker records a page split for concurrent reader visibility.
// Set by propagateSplit after parent CAS succeeds.
// Readers check this marker to follow splits without waiting for parent update.
type SplitMarker struct {
	Left     *PageRef
	Right    *PageRef
	SplitKey []byte
}

// PageRef manages concurrent access to a single page in the B+Tree.
// Each PageRef corresponds to one page in the tree, linked via parentRef
// to form a path from leaf to root.
//
// Lifecycle: exists as long as the page is part of the tree.
// Created during split/merge propagation or tree initialization.
type PageRef struct {
	pageID      model.PageID                // bound at creation, immutable — used by Release
	pInfo       atomic.Pointer[PageInfo]    // atomically replaced page info
	parentRef   atomic.Pointer[PageRef]     // parent reference; nil for root (managed by RootPageRef)
	children    atomic.Pointer[[]*PageRef]  // lazy-loaded child refs; nil = leaf or not populated
	refCount    atomic.Int32                // reference count; zero triggers freeFunc
	splitMarker atomic.Pointer[SplitMarker] // split visibility marker
	freeFunc    func(model.PageID)          // bound at creation; called when refCount reaches 0
	lock        SchedulerLock               // leaf-level spin lock
}

// NewPageRef creates a new PageRef with the given page identity and parent.
// freeFunc is called when the refCount reaches zero (typically storage.FreePage).
// pageID is bound at creation and never changes — Release uses it directly
// to avoid TOCTOU race with concurrent CAS replacing pInfo (C1 fix).
func NewPageRef(pageID model.PageID, version uint64, parentRef *PageRef, freeFunc func(model.PageID)) *PageRef {
	r := &PageRef{
		pageID:   pageID,
		freeFunc: freeFunc,
	}
	r.parentRef.Store(parentRef)
	r.pInfo.Store(&PageInfo{
		PageID:  pageID,
		Version: version,
	})
	return r
}

// GetPageInfo atomically reads the current PageInfo.
// Caller must Retain the PageRef before using the returned PageInfo
// to ensure the page is not freed during use.
func (r *PageRef) GetPageInfo() *PageInfo {
	return r.pInfo.Load()
}

// CAS atomically replaces PageInfo if current equals old.
// Returns true if the swap succeeded.
func (r *PageRef) CAS(old, newInfo *PageInfo) bool {
	return r.pInfo.CompareAndSwap(old, newInfo)
}

// Retain increments the reference count.
// Called during searchPath for each PageRef on the traversal path.
func (r *PageRef) Retain() {
	r.refCount.Add(1)
}

// Release decrements the reference count.
// When refCount reaches zero, calls the bound freeFunc with the immutable pageID
// to reclaim the page. Uses r.pageID (bound at creation) instead of reading
// from pInfo to avoid TOCTOU race with concurrent CAS (C1 fix).
// Panics if called when refCount is already zero (use-after-free bug).
func (r *PageRef) Release() {
	if v := r.refCount.Add(-1); v < 0 {
		panic("btree2: Release() called on PageRef with zero refCount")
	} else if v == 0 {
		if r.freeFunc != nil && r.pageID != model.InvalidPageID {
			r.freeFunc(r.pageID)
		}
	}
}

// RefCount returns the current reference count (for testing/debugging).
func (r *PageRef) RefCount() int32 {
	return r.refCount.Load()
}

// Lock acquires the leaf-level spin lock.
func (r *PageRef) Lock() {
	r.lock.Lock()
}

// Unlock releases the leaf-level spin lock.
func (r *PageRef) Unlock() {
	r.lock.Unlock()
}

// GetParentRef returns the parent PageRef.
// Returns nil for root nodes (managed by RootPageRef).
func (r *PageRef) GetParentRef() *PageRef {
	return r.parentRef.Load()
}

// SetParentRef updates the parent reference.
// Used by RootPageRef.ReplaceRoot to propagate parent pointers.
func (r *PageRef) SetParentRef(parent *PageRef) {
	r.parentRef.Store(parent)
}

// GetOrCreateChildren returns the child PageRef slice for this node.
// For leaf pages, returns nil.
// For internal nodes, lazily constructs children from page data on first access.
// Thread-safe via CAS.
func (r *PageRef) GetOrCreateChildren(storage BTreeStorage) []*PageRef {
	if c := r.children.Load(); c != nil {
		return *c
	}

	info := r.GetPageInfo()
	if info == nil {
		return nil
	}

	// Check if this is a leaf — leaves have no children
	if storage == nil {
		return nil
	}
	page, err := storage.GetNodePage(info.PageID)
	if err != nil {
		// Error from storage: could be a leaf page (expected), ErrTreeClosed,
		// or ErrInvalidPage (programming error). For leaf pages, GetNodePage
		// returns an error because the page type is not InternalPage.
		// Returning nil causes searchPath to treat this as a leaf and stop
		// descending. Safe for normal operation (C4 design decision).
		return nil
	}
	if page.IsLeaf() {
		return nil
	}

	childCount := page.ChildCount()
	newChildren := make([]*PageRef, childCount)
	for i := range childCount {
		childID := page.GetChild(i)
		newChildren[i] = NewPageRef(childID, 0, r, r.freeFunc)
	}

	if r.children.CompareAndSwap(nil, &newChildren) {
		return newChildren
	}
	// Another goroutine won the CAS race
	return *r.children.Load()
}

// GetPathToRoot traverses parentRef chain from this node up to the root.
// Returns slice where index 0 = this node, last = root.
func (r *PageRef) GetPathToRoot() []*PageRef {
	var path []*PageRef
	current := r
	for current != nil {
		path = append(path, current)
		current = current.GetParentRef()
	}
	return path
}

// SetSplitMarker sets the split marker for this page.
// Makes a defensive copy of splitKey to prevent caller from mutating the
// marker through a shared buffer (I1 fix).
func (r *PageRef) SetSplitMarker(left, right *PageRef, splitKey []byte) {
	keyCopy := make([]byte, len(splitKey))
	copy(keyCopy, splitKey)
	marker := &SplitMarker{
		Left:     left,
		Right:    right,
		SplitKey: keyCopy,
	}
	r.splitMarker.Store(marker)
}

// GetSplitMarker reads the current split marker.
// Returns nil if no split has occurred.
func (r *PageRef) GetSplitMarker() *SplitMarker {
	return r.splitMarker.Load()
}

// FollowSplit checks for a split marker and returns the correct
// child PageRef based on the given key.
// Returns (targetRef, true) if a split was followed,
// (nil, false) if no split marker exists.
func (r *PageRef) FollowSplit(key []byte) (*PageRef, bool) {
	marker := r.splitMarker.Load()
	if marker == nil {
		return nil, false
	}
	if bytes.Compare(key, marker.SplitKey) < 0 {
		return marker.Left, true
	}
	return marker.Right, true
}
