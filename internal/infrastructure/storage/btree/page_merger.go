// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

// PageMerger defines the Merge/Borrow operations for B+Tree structural reorganization.
// Extracted from BTreeStorage per ISP — OffheapBTreeStorage implements it via composition.
// handleLeafMerge / handleInternalMerge receive PageMerger, not the full BTreeStorage.
type PageMerger interface {
	MergeLeaves(left, right LeafPage) (LeafPage, error)
	BorrowFromLeftLeaf(self, sibling LeafPage) (newSelf, newSib LeafPage, err error)
	BorrowFromRightLeaf(self, sibling LeafPage) (newSelf, newSib LeafPage, err error)

	MergeNodes(left, right NodePage, separator []byte) (NodePage, error)
	BorrowFromLeftNode(self, sibling NodePage, separator []byte) (newSelf, newSib NodePage, newSep []byte, err error)
	BorrowFromRightNode(self, sibling NodePage, separator []byte) (newSelf, newSib NodePage, newSep []byte, err error)
}
