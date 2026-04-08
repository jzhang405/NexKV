// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import "github.com/jzhang405/NexKV/internal/domain/model"

// BTreeStorage is the bridge between btree and the offheap page layer.
// All page allocation, COW copy, and deallocation go through this interface.
type BTreeStorage interface {
	// Alloc allocates a new leaf or index page and returns its PageID.
	AllocLeafPage() (model.PageID, error)
	AllocNodePage() (model.PageID, error)

	// COW copy: allocate new page, memcpy 4096 bytes, increment version.
	// Returns the new page ID and a read-only handle to the copy.
	CopyLeafPage(srcID model.PageID) (model.PageID, LeafPage, error)
	CopyNodePage(srcID model.PageID) (model.PageID, NodePage, error)

	// Get returns a read-only handle for an existing page.
	GetLeafPage(pageID model.PageID) (LeafPage, error)
	GetNodePage(pageID model.PageID) (NodePage, error)

	// Free returns a page to the free list.
	FreePage(pageID model.PageID) error

	// IsLeafPage reads the physical page type to determine if pageID is a leaf.
	// Safe for live pages referenced by the tree.
	IsLeafPage(pageID model.PageID) bool

	// Close releases all resources held by the storage.
	Close() error

	// --- Phase 6.5: Merge/Borrow (stubs until Phase 6.5) ---

	MergeLeaves(left, right LeafPage) (LeafPage, error)
	BorrowFromLeftLeaf(self, sibling LeafPage) (newSelf, newSib LeafPage, err error)
	BorrowFromRightLeaf(self, sibling LeafPage) (newSelf, newSib LeafPage, err error)

	MergeNodes(left, right NodePage, separator []byte) (NodePage, error)
	BorrowFromLeftNode(self, sibling NodePage, separator []byte) (newSelf, newSib NodePage, newSep []byte, err error)
	BorrowFromRightNode(self, sibling NodePage, separator []byte) (newSelf, newSib NodePage, newSep []byte, err error)
}
