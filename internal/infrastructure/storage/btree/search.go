// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"sync"
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

var searchPathPool = sync.Pool{
	New: func() any {
		s := make(SearchPath, 0, 8)
		return &s
	},
}

// ReleaseAll decrements reference count for every PageRef on the path.
// Must be called when the search path is no longer needed.
func (p SearchPath) ReleaseAll() {
	for _, entry := range p {
		entry.Ref.Release()
	}
	// Return backing array to pool if eligible
	if cap(p) >= 8 && cap(p) <= 32 {
		cp := p[:0]
		searchPathPool.Put(&cp)
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
// ★ Uses GetChildren() — never creates new PageRef objects.
// All children caches are pre-populated by split/ReplaceRoot operations.
// If cache is nil for a non-leaf node, returns ErrRetry (cache not yet ready).
func searchPath(rootRef *RootPageRef, key []byte) (SearchPath, error) {
	pp := searchPathPool.Get().(*SearchPath)
	path := (*pp)[:0]

	currentRef := &rootRef.PageRef
	currentRef.Retain()

	for {
		pInfo := currentRef.GetPageInfo()
		if pInfo == nil {
			path.ReleaseAll()
			return nil, errpkg.Wrapf(ErrBTreeSearchError, "btree: searchPath: nil PageInfo on page %d", currentRef.pageID)
		}

		// Check if leaf — stop descending
		// Uses PageInfo.IsLeaf (set at PageRef creation/CAS) to avoid TOCTOU race
		// with page allocator reuse.
		if pInfo.IsLeaf {
			// ★ FIX: Leaf with Redirect means this leaf was split but searchPath
			// didn't catch it at the parent level (e.g., root leaf split).
			// Return ErrRetry to force re-traversal from root.
			if pInfo.Redirect {
				path.ReleaseAll()
				return nil, ErrRetry
			}
			path = append(path, PathEntry{Ref: currentRef, Index: -1})
			return path, nil
		}

		// Internal node: navigate using children cache.
		// ★ GetChildren() only reads existing cache — never creates new PageRef objects.
		// Cache is always pre-populated by split/ReplaceRoot operations.
		cache := currentRef.GetChildren()
		if cache == nil {
			// Cache not yet ready — another goroutine is mid-split.
			// Retry from root to get a consistent view.
			path.ReleaseAll()
			return nil, ErrRetry
		}

		idx := cache.Search(key)

		// ★ P1-1 fix: bounds check — idx could be out of range during concurrent split
		if idx >= len(cache.Children) || cache.Children[idx] == nil {
			path.ReleaseAll()
			return nil, ErrRetry // children list invalidated, retry from root
		}

		childRef := cache.Children[idx]
		childRef.Retain() // ★ Retain immediately — prevents concurrent freeFunc recycling

		// Redirect following: if child has Redirect in PageInfo,
		// re-navigate via the parent's updated children cache.
		// Redirect is set atomically via CAS on PageInfo — no window gap.
		childInfo := childRef.GetPageInfo()
		actualIdx := idx
		if childInfo.Redirect {
			// Page was split — release stale childRef and re-navigate.
			childRef.Release()
			updatedCache := currentRef.GetChildren()
			if updatedCache != nil {
				reIdx := updatedCache.Search(key)
				if reIdx < len(updatedCache.Children) && updatedCache.Children[reIdx] != nil {
					childRef = updatedCache.Children[reIdx]
					childRef.Retain()
					actualIdx = reIdx
				} else {
					path.ReleaseAll()
					return nil, ErrRetry
				}
			} else {
				path.ReleaseAll()
				return nil, ErrRetry
			}
		}

		path = append(path, PathEntry{Ref: currentRef, Index: actualIdx})
		currentRef = childRef
	}
}
