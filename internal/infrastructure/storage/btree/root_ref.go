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
func NewRootPageRef(pageID model.PageID, version uint64, freeFunc func(model.PageID)) *RootPageRef {
	return &RootPageRef{
		PageRef: *NewPageRef(pageID, version, nil, freeFunc),
	}
}

// ReplaceRoot atomically replaces the root page.
// oldInfo is the caller's expected current PageInfo (captured before mutation).
// Sets parentRef for all newChildren BEFORE the CAS publish to eliminate
// the window where a concurrent reader could see the child but find parentRef==nil.
// Returns true if the CAS succeeded.
func (r *RootPageRef) ReplaceRoot(oldInfo, newInfo *PageInfo, newChildren []*PageRef) bool {
	// Set parentRef BEFORE CAS — children are not yet visible to readers,
	// so setting parentRef here is safe. CAS failure is harmless: the caller
	// will create fresh PageRef objects on retry (D14 decision).
	for _, child := range newChildren {
		if child != nil {
			child.SetParentRef(&r.PageRef)
		}
	}

	// CAS publish — atomically makes new root info visible to all readers.
	if !r.CAS(oldInfo, newInfo) {
		return false
	}

	return true
}
