// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import "bytes"

// ChildrenCache holds the cached children and separator keys for an internal node.
// Replaces the old []*PageRef children slice, embedding separator keys so that
// searchPath can navigate without reading the physical NodePage.
//
// Invariant: len(Separators) == len(Children) - 1
//
// Layout (same as B+Tree internal node):
//
//	Separators:  [k1]    [k2]    [k3]
//	Children:  [C0]    [C1]    [C2]    [C3]
//
// Search semantics (matches nodePageHandle.Search):
//
//	key < k1       → 0  (C0)
//	k1 <= key < k2 → 1  (C1)
//	key == k2      → 2  (C2, right subtree for exact match)
//	key >= k3      → 3  (C3)
type ChildrenCache struct {
	Children   []*PageRef // child PageRefs, ordered
	Separators [][]byte   // Separators[i] separates Children[i] and Children[i+1]
}

// Search returns the child index for the given key.
// Semantics identical to nodePageHandle.Search:
//   - key == Separators[i] → returns i+1 (right subtree, B+Tree convention)
//   - key < Separators[0]  → returns 0
//   - key >= Separators[last] → returns len(Children)-1
//
// For an empty cache (no children), returns 0.
// For a single child (no separators), returns 0.
func (c *ChildrenCache) Search(key []byte) int {
	if c == nil || len(c.Children) == 0 {
		return 0
	}
	if len(c.Separators) == 0 {
		return 0
	}

	// Binary search over separators.
	// Find the smallest i such that key < Separators[i].
	lo, hi := 0, len(c.Separators)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if bytes.Compare(key, c.Separators[mid]) < 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	// lo is now the number of separators that are <= key,
	// which equals the child index (since Children[i] holds keys in [Separators[i-1], Separators[i]))
	return lo
}

// copyKey creates an independent copy of a key obtained from mmap.
// GetKey() returns a slice backed by mmap memory that may change or be unmapped;
// all persistent references must use copied keys.
func copyKey(k []byte) []byte {
	cp := make([]byte, len(k))
	copy(cp, k)
	return cp
}
