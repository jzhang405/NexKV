// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree2

import "github.com/jzhang405/NexKV/internal/domain/model"

// ResolvedPath records the navigation path from a root page to a leaf page.
// This is a simplified version without PageRef tracking (Phase 4 will add the full version).
type ResolvedPath struct {
	PageIDs []model.PageID // page IDs traversed from root to leaf
	Indices []int          // child index chosen at each level (-1 for leaf)
	LeafID  model.PageID   // the final leaf page ID
}

// resolvePath navigates from rootID down to the leaf page that would contain key.
// Uses direct pageID traversal without reference counting.
func resolvePath(storage *OffheapBTreeStorage, rootID model.PageID, key []byte) (*ResolvedPath, error) {
	path := &ResolvedPath{}
	currentPageID := rootID

	for {
		rawID := uint32(currentPageID)
		if storage.pa.IsLeaf(rawID) {
			path.LeafID = currentPageID
			path.PageIDs = append(path.PageIDs, currentPageID)
			path.Indices = append(path.Indices, -1)
			return path, nil
		}

		node := &nodePageHandle{id: currentPageID, pa: storage.pa, storage: storage}
		idx, _ := node.Search(key)
		path.PageIDs = append(path.PageIDs, currentPageID)
		path.Indices = append(path.Indices, idx)

		childID := node.GetChild(idx)
		currentPageID = childID
	}
}
