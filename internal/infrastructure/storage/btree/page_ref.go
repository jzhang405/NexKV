// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
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
	pageID     model.PageID               // bound at creation, immutable — used by Release
	pInfo      atomic.Pointer[PageInfo]   // atomically replaced page info
	parentRef  atomic.Pointer[PageRef]    // parent reference; nil for root (managed by RootPageRef)
	children   atomic.Pointer[[]*PageRef] // lazy-loaded child refs; nil = leaf or not populated
	refCount   atomic.Int32               // reference count; zero triggers freeFunc
	freeFunc   func(model.PageID)         // bound at creation; called when refCount reaches 0
	lock       SchedulerLock              // leaf-level spin lock
	splitLatch atomic.Int32               // split mutex; 0 = free, 1 = held
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
		IsLeaf:    true,     // default: leaf; internal split handlers override
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

// GetOrCreateChildren returns the child PageRef slice for this node.
// For leaf pages, returns (nil, nil).
// For internal nodes, lazily constructs children from page data on first access.
// Thread-safe via CAS.
//
// Error handling:
// - Returns (nil, nil) for leaf pages (expected condition)
// - Returns (nil, error) for real errors (tree closed, invalid page)
func (r *PageRef) GetOrCreateChildren(storage BTreeStorage) ([]*PageRef, error) {
	if c := r.children.Load(); c != nil {
		return *c, nil
	}

	info := r.GetPageInfo()
	if info == nil {
		return nil, nil
	}

	// Check if this is a leaf — leaves have no children
	if storage == nil {
		return nil, nil
	}
	page, err := storage.GetNodePage(info.PageID)
	if err != nil {
		// Check if this is the expected "not a node page" error (leaf page)
		// This is the normal case when traversing to a leaf
		if isLeafPageError(err) {
			return nil, nil
		}
		// Real error: tree closed, invalid page, etc.
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
		// 查询物理层确定 IsLeaf（页面在线，父节点是 internal node）
		isLeaf := true // 默认 leaf
		childNode, err := storage.GetNodePage(childID)
		if err == nil {
			isLeaf = childNode.IsLeaf()
		}
		// isLeafPageError → 保持默认 isLeaf=true（正常 leaf 路径）
		childRef.pInfo.Store(&PageInfo{
			PageID:    childID,
			Version:   1,
			IsLeaf:    isLeaf,
			NodeState: NodeNormal,
		})
		newChildren[i] = childRef
	}

	if r.children.CompareAndSwap(nil, &newChildren) {
		return newChildren, nil
	}
	// Another goroutine won the CAS race
	return *r.children.Load(), nil
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
