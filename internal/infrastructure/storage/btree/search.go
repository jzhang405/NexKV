// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PathEntry records one node on the search path from root to leaf.
type PathEntry struct {
	Ref   *PageRef // page reference (Retained during searchPath)
	Index int      // child index chosen at this level (-1 for leaf)
}

// SearchPath is the traversal path from root to leaf.
// path[0] = root, path[len-1] = leaf.
// Every PageRef on the path has been Retained; caller must call ReleaseAll
// when done (CAS failure retry, or after operation completes).
type SearchPath []PathEntry

// ReleaseAll decrements reference count for every PageRef on the path.
// Must be called when the search path is no longer needed:
//   - CAS failure before retry
//   - Read operation after value is copied
//   - Write operation after mutation is fully propagated
func (p SearchPath) ReleaseAll() {
	for _, entry := range p {
		entry.Ref.Release()
	}
}

// Leaf returns the leaf-level PathEntry (last element).
func (p SearchPath) Leaf() PathEntry {
	if len(p) == 0 {
		panic("btree: SearchPath.Leaf() called on empty path")
	}
	return p[len(p)-1]
}

// ParentPath returns the path excluding the leaf (root to leaf's parent).
// Useful for split/merge propagation which walks upward from leaf's parent.
func (p SearchPath) ParentPath() []PathEntry {
	if len(p) <= 1 {
		return nil
	}
	return p[:len(p)-1]
}

// searchPath traverses from rootRef down to the leaf page that would contain key.
// Every PageRef on the returned path has been Retained; caller must ReleaseAll.
// Handles concurrent splits via SplitMarker following (D5 decision).
//
//nolint:unused // Phase 5 BTree core will call this
func searchPath(storage *OffheapBTreeStorage, rootRef *RootPageRef, key []byte) (SearchPath, error) {
	var path SearchPath

	currentRef := &rootRef.PageRef
	currentRef.Retain()

	for {
		pInfo := currentRef.GetPageInfo()
		if pInfo == nil {
			path.ReleaseAll()
			return nil, fmt.Errorf("btree: searchPath: nil PageInfo on page %d", currentRef.pageID)
		}

		// Check if leaf — stop descending
		// Uses PageInfo.IsLeaf (set at PageRef creation/CAS) to avoid TOCTOU race
		// with page allocator reuse.
		if pInfo.IsLeaf {
			path = append(path, PathEntry{Ref: currentRef, Index: -1})
			return path, nil
		}

		// Internal node: search for child index
		node := &nodePageHandle{id: pInfo.PageID, pa: storage.pa, storage: storage}
		idx, _ := node.Search(key)
		// Note: idx may be corrected below if FollowSplit redirects us to right sibling

		// Get or lazily create child refs
		children, err := currentRef.GetOrCreateChildren(storage)
		if err != nil {
			path.ReleaseAll()
			return nil, fmt.Errorf("btree: searchPath: %w", err)
		}

		// ★ P1-1 fix: bounds check — idx could be out of range during concurrent split
		if idx >= len(children) || children[idx] == nil {
			path.ReleaseAll()
			return nil, ErrRetry // children list invalidated, retry from root
		}

		childRef := children[idx]

		// Redirect following: if child has Redirect in PageInfo,
		// re-navigate via the parent's updated children cache.
		// Redirect is set atomically via CAS on PageInfo — no window gap.
		childInfo := childRef.GetPageInfo()
		actualIdx := idx
		if childInfo.Redirect {
			// Page was split — re-navigate from parent's updated children
			updatedChildren, _ := currentRef.GetOrCreateChildren(storage)
			if updatedChildren != nil {
				reIdx, _ := node.Search(key)
				if reIdx < len(updatedChildren) && updatedChildren[reIdx] != nil {
					childRef = updatedChildren[reIdx]
					actualIdx = reIdx
				} else {
					path.ReleaseAll()
					return nil, ErrRetry
				}
			} else {
				path.ReleaseAll()
				return nil, ErrRetry
			}
		} else {
			childRef.Retain()
		}

		path = append(path, PathEntry{Ref: currentRef, Index: actualIdx})
		currentRef = childRef
	}
}

// --- Legacy path resolution (pre-Phase 5, used by tests) ---

// ResolvedPath records the navigation path from a root page to a leaf page.
// Uses raw PageID traversal without reference counting.
// Prefer SearchPath for production use.
type ResolvedPath struct {
	PageIDs []model.PageID // page IDs traversed from root to leaf
	Indices []int          // child index chosen at each level (-1 for leaf)
	LeafID  model.PageID   // the final leaf page ID
}

// resolvePath navigates from rootID down to the leaf page that would contain key.
// Uses direct pageID traversal without reference counting.
// Retained for backward compatibility with existing tests.
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
