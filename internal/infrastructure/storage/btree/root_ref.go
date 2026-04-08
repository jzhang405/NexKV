// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import "github.com/jzhang405/NexKV/internal/domain/model"

// RootPageRef is the root page's PageRef.
// It specializes PageRef with atomic root switching.
type RootPageRef struct {
	PageRef
}

// NewRootPageRef creates a root PageRef.
// Constructs in-place to avoid copying PageRef (which contains atomic fields).
func NewRootPageRef(pageID model.PageID, version uint64, freeFunc func(model.PageID)) *RootPageRef {
	r := &RootPageRef{}
	r.pageID = pageID
	r.freeFunc = freeFunc
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
// Returns true if the CAS succeeded.
func (r *RootPageRef) ReplaceRoot(oldInfo, newInfo *PageInfo, newChildren *ChildrenCache) bool {
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
