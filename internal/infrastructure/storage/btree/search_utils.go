// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
)

// BinarySearchResult holds the result of a binary search operation
type BinarySearchResult struct {
	InsertPos int  // Position where the key should be inserted
	FoundIdx  int  // Index of the key if found, -1 otherwise
	Found     bool // True if the key was found
}

// BinarySearch performs binary search on a sorted byte slice
// Returns the insert position, found index, and whether the key was found
func BinarySearch(keys [][]byte, key []byte) BinarySearchResult {
	left, right := 0, len(keys)-1

	for left <= right {
		mid := left + (right-left)/2
		cmp := bytes.Compare(keys[mid], key)

		if cmp == 0 {
			return BinarySearchResult{
				InsertPos: mid,
				FoundIdx:  mid,
				Found:     true,
			}
		} else if cmp < 0 {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}

	return BinarySearchResult{
		InsertPos: left,
		FoundIdx:  -1,
		Found:     false,
	}
}
