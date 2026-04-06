// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import "github.com/jzhang405/NexKV/internal/domain/model"

// RootPageRef is the root page's PageRef.
// It specializes PageRef with atomic root switching and parentRef propagation.
// parentRef is always nil.
type RootPageRef struct {
	PageRef
}

// NewRootPageRef creates a root PageRef.
// parentRef is always nil for root nodes.
// Constructs in-place to avoid copying PageRef (which contains SchedulerLock).
func NewRootPageRef(pageID model.PageID, version uint64, freeFunc func(model.PageID)) *RootPageRef {
	r := &RootPageRef{}
	r.pageID = pageID
	r.freeFunc = freeFunc
	// parentRef stays nil (zero value) — root has no parent
	r.pInfo.Store(&PageInfo{
		PageID:    pageID,
		Version:   version,
		IsLeaf:    true,
		NodeState: NodeRoot,
	})
	return r
}

// ReplaceRoot atomically replaces the root page.
// oldInfo is the caller's expected current PageInfo (captured before mutation).
// Sets parentRef for all newChildren BEFORE the CAS publish to eliminate
// the window where a concurrent reader could see the child but find parentRef==nil.
// Returns true if the CAS succeeded.
func (r *RootPageRef) ReplaceRoot(oldInfo, newInfo *PageInfo, newChildren *ChildrenCache) bool {
	// Set parentRef BEFORE CAS — children are not yet visible to readers,
	// so setting parentRef here is safe. CAS failure is harmless: the caller
	// will create fresh PageRef objects on retry (D14 decision).
	if newChildren != nil {
		for _, child := range newChildren.Children {
			if child != nil {
				child.SetParentRef(&r.PageRef)
			}
		}
	}

	// CAS publish — atomically makes new root info visible to all readers.
	if !r.CAS(oldInfo, newInfo) {
		return false
	}

	// ★ Trace root replacement (AFTER CAS success)
	GlobalTracer.LogOp("ReplaceRoot.success", "oldPageID", oldInfo.PageID, "newPageID", newInfo.PageID, "oldVersion", oldInfo.Version, "newIsLeaf", newInfo.IsLeaf)

	// ★ CRITICAL FIX: Store children IMMEDIATELY after CAS succeeds.
	// This eliminates the window where readers see IsLeaf=false but children=nil.
	// Before this fix, children.Store was done separately by the caller,
	// creating a race condition that caused 710K+ redirect loops.
	r.children.Store(newChildren)

	return true
}
