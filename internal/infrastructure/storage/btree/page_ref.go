// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PageRef manages concurrent access to a single page in the B+Tree.
// Each PageRef corresponds to one page in the tree, linked via parentRef
// to form a path from leaf to root.
//
// Lifecycle: exists as long as the page is part of the tree.
// Created during split/merge propagation or tree initialization.
type PageRef struct {
	pageID     model.PageID                  // bound at creation, immutable — used by Release
	pInfo      atomic.Pointer[PageInfo]      // atomically replaced page info
	parentRef  atomic.Pointer[PageRef]       // parent reference; nil for root (managed by RootPageRef)
	children   atomic.Pointer[ChildrenCache] // lazy-loaded child refs with embedded separator keys; updated via CAS
	refCount   atomic.Int32                  // reference count; zero triggers freeFunc
	freeFunc   func(model.PageID)            // bound at creation; called when refCount reaches 0
	lock       SchedulerLock                 // leaf-level spin lock
	splitLatch atomic.Int32                  // split mutex; 0 = free, 1 = held
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
		PageID:    pageID,
		Version:   version,
		IsLeaf:    true, // default: leaf; internal split handlers override
		NodeState: NodeNormal,
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
	success := r.pInfo.CompareAndSwap(old, newInfo)
	if !success {
		GlobalTracer.LogPageRefOp(r, "CAS.fail", map[string]interface{}{
			"oldPageID":  old.PageID,
			"newPageID":  newInfo.PageID,
			"oldVersion": old.Version,
			"newVersion": newInfo.Version,
		})
	}
	return success
}

// Retain increments the reference count.
// Called during searchPath for each PageRef on the traversal path.
func (r *PageRef) Retain() {
	old := r.refCount.Add(1)
	if old < 0 {
		panic(fmt.Sprintf("Retain on pageRef with negative refCount: %d, pageID=%d", old, r.pageID))
	}
}

// Release decrements the reference count.
// When refCount reaches zero, calls the bound freeFunc with the immutable pageID
// to reclaim the page. Uses r.pageID (bound at creation) instead of reading
// from pInfo to avoid TOCTOU race with concurrent CAS (C1 fix).
// Panics if called when refCount is already zero (use-after-free bug).
func (r *PageRef) Release() {
	defer func() {
		if p := recover(); p != nil {
			// 记录 debug 信息
			debug.PrintStack()
			log.Printf("PANIC in Release: pageID=%d, refCount=%d, pInfo=%v",
				r.pageID, r.refCount.Load(), r.pInfo.Load())
			panic(p) // 重新抛出
		}
	}()

	old := r.refCount.Load()
	if old <= 0 {
		debug.PrintStack()
		panic(fmt.Sprintf("Release() called on pageRef with refCount=%d, pageID=%d, pInfo=%v",
			old, r.pageID, r.pInfo.Load()))
	}

	v := r.refCount.Add(-1)

	if v < 0 {
		panic("btree: Release() called on pageRef with zero refCount")
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

// GetChildren returns the existing ChildrenCache for this PageRef.
// Returns nil if no cache has been set (leaf pages, or internal node
// whose cache hasn't been populated by split/ReplaceRoot yet).
//
// ★ DOES NOT create new PageRef objects. The only valid sources of
// children cache are: ReplaceRoot, updateChildrenCache, and
// distributeChildrenAfterSplit. These operations reuse existing
// PageRef objects, preserving redirect state and version info.
//
// Previously, GetOrCreateChildren would create brand-new PageRef objects
// (version=0, no redirect info) when the cache was nil. This caused
// searchPath to navigate via stale PageRefs → data loss.
func (r *PageRef) GetChildren() *ChildrenCache {
	return r.children.Load()
}

// GetOrCreateChildren returns the ChildrenCache for this node.
// For leaf pages, returns (nil, nil).
// For internal nodes, lazily constructs ChildrenCache from page data on first access,
// including separator keys copied from the physical page.
// Thread-safe via CAS.
//
// Error handling:
// - Returns (nil, nil) for leaf pages (expected condition)
// - Returns (nil, error) for real errors (tree closed, invalid page)
func (r *PageRef) GetOrCreateChildren(storage BTreeStorage) (*ChildrenCache, error) {
	if c := r.children.Load(); c != nil {
		return c, nil
	}

	info := r.GetPageInfo()
	if info == nil {
		return nil, nil
	}

	// Fast path: check IsLeaf from PageInfo before attempting GetNodePage
	if info.IsLeaf {
		return nil, nil
	}

	if storage == nil {
		return nil, nil
	}
	page, err := storage.GetNodePage(info.PageID)
	if err != nil {
		if isLeafPageError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetOrCreateChildren: %w", err)
	}
	if page.IsLeaf() {
		return nil, nil
	}

	childCount := page.ChildCount()
	newChildren := make([]*PageRef, childCount)
	for i := range childCount {
		childID := page.GetChild(i)
		childRef := NewPageRef(childID, 0, r, r.freeFunc)
		// Query physical layer to determine IsLeaf
		isLeaf := true // default: leaf
		childNode, err := storage.GetNodePage(childID)
		if err == nil {
			isLeaf = childNode.IsLeaf()
		}
		childRef.pInfo.Store(&PageInfo{
			PageID:    childID,
			Version:   1,
			IsLeaf:    isLeaf,
			NodeState: NodeNormal,
		})
		newChildren[i] = childRef
	}

	// Extract separator keys (one per key entry in the node page)
	// page.Count() returns the number of keys; ChildCount() = Count() + 1
	keyCount := page.Count()
	separators := make([][]byte, keyCount)
	for i := range keyCount {
		separators[i] = copyKey(page.GetKey(i))
	}

	newCache := &ChildrenCache{
		Children:   newChildren,
		Separators: separators,
	}

	// ★ Fix: Never overwrite an existing cache. ReplaceRoot/handleLeafSplit
	// may have stored a newer cache (with correct children + separators)
	// after we read nil. CAS(nil, newCache) would overwrite that newer data,
	// causing searchPath to navigate via stale children → data loss.
	if !r.children.CompareAndSwap(nil, newCache) {
		// Someone else stored a cache — return theirs, discard ours
		existing := r.children.Load()
		if existing != nil {
			return existing, nil
		}
		// Extremely rare: CAS failed AND Load returned nil (concurrent store+load race).
		// Return our cache anyway — better than nil.
	}
	return newCache, nil
}

// isLeafPageError checks if the error indicates the page is a leaf (expected condition).
func isLeafPageError(err error) bool {
	// GetNodePage returns "btree: page X is not a node page" when the page type is LeafPage
	return strings.Contains(err.Error(), "is not a node page")
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

// TryAcquireSplitLatch attempts to acquire the split latch.
// Returns true if the latch was acquired, false if another goroutine holds it.
func (r *PageRef) TryAcquireSplitLatch() bool {
	return r.splitLatch.CompareAndSwap(0, 1)
}

// ReleaseSplitLatch releases the split latch.
func (r *PageRef) ReleaseSplitLatch() {
	r.splitLatch.Store(0)
}

// IsSplitting returns true if the split latch is currently held.
func (r *PageRef) IsSplitting() bool {
	return r.splitLatch.Load() == 1
}
