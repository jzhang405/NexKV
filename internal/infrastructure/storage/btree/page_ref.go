// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"strings"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PageRef manages concurrent access to a single page in the B+Tree.
// Each PageRef corresponds to one page in the tree.
// Parent-child relationships are resolved via SearchPath (path array index)
// rather than stored parentRef pointers — eliminates stale pointer risk
// during concurrent splits and reduces per-PageRef memory overhead.
//
// Lifecycle: exists as long as the page is part of the tree.
// Created during split/merge propagation or tree initialization.

// Retain is a no-op (page lifetime managed by EpochManager RCU-style).
func (r *PageRef) Retain() {}

// Release is a no-op (page lifetime managed by EpochManager RCU-style).
func (r *PageRef) Release() {}

// RefCount returns 0 (refCount removed, epoch-based reclamation).
func (r *PageRef) RefCount() int32 { return 0 }

type PageRef struct {
	pageID   model.PageID                  // bound at creation, immutable
	pInfo    atomic.Pointer[PageInfo]      // atomically replaced page info
	children atomic.Pointer[ChildrenCache] // child refs with embedded separator keys; updated via CAS
	// refCount removed: page lifetime managed by EpochManager RCU-style.
	// Pages are freed via epoch-gated deferred reclamation, not reference counting.
}

// NewPageRef creates a new PageRef with the given page identity.
// freeFunc is called when the refCount reaches zero (typically storage.FreePage).
// pageID is bound at creation and never changes — Release uses it directly
// to avoid TOCTOU race with concurrent CAS replacing pInfo (C1 fix).
func NewPageRef(pageID model.PageID, version uint64, _ ...any) *PageRef {
	r := &PageRef{pageID: pageID}
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

// IsLeaf returns whether this page is a leaf node.
func (r *PageRef) IsLeaf() bool {
	return r.pInfo.Load().IsLeaf
}

// PageID returns the page ID of this reference.
func (r *PageRef) PageID() model.PageID { return r.pageID }

// ChildIDs returns the PageIDs of all children (alias for ChildPageIDs).
// Exists to satisfy checkpoint.PageRef interface. Returns nil for leaf pages.
func (r *PageRef) ChildIDs() []model.PageID { return r.ChildPageIDs() }

// ChildPageIDs returns the PageIDs of all children.
// Used by checkpoint DFS traversal. Returns nil for leaf pages.
func (r *PageRef) ChildPageIDs() []model.PageID {
	cc := r.children.Load()
	if cc == nil || len(cc.Children) == 0 {
		return nil
	}
	ids := make([]model.PageID, len(cc.Children))
	for i, child := range cc.Children {
		ids[i] = child.pageID
	}
	return ids
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
// Release removed: page lifetime managed by EpochManager RCU-style.



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

// isLeafPageError checks if the error indicates the page is a leaf (expected condition).
func isLeafPageError(err error) bool {
	// GetNodePage returns "btree: page X is not a node page" when the page type is LeafPage
	return strings.Contains(err.Error(), "is not a node page")
}
