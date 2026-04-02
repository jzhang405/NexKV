// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PageHandle is the common read-only interface for all page types.
// Short-lived: created per operation, discarded after use.
type PageHandle interface {
	// Identity
	PageID() model.PageID
	Count() int
	IsFull() bool
	IsLeaf() bool
	Capacity() float64

	// Read
	Search(key []byte) (index int, found bool)
	GetKey(idx int) []byte // returns a copy
}

// LeafPage extends PageHandle with leaf KV operations.
// All mutation methods follow COW semantics: return new instance, original unchanged.
type LeafPage interface {
	PageHandle

	// Leaf read
	GetValue(idx int) []byte // returns a copy

	// COW mutations — allocate new page, copy+modify, return new LeafPage
	Insert(key, value []byte) (LeafPage, error)
	Update(idx int, value []byte) (LeafPage, error)
	Delete(idx int) (LeafPage, error)
	Split() (left, right LeafPage, splitKey []byte, err error)

	// Validation
	Validate() error
}

// NodePage extends PageHandle with index page child management.
// All mutation methods follow COW semantics.
type NodePage interface {
	PageHandle

	// Node read
	GetChild(idx int) model.PageID
	ChildCount() int // = key count + 1

	// COW mutations
	ReplaceChild(idx int, newChildID model.PageID) (NodePage, error)
	InsertChild(idx int, splitKey []byte, left, right model.PageID) (NodePage, error)
	RemoveChild(idx int) (NodePage, error)
	Split() (left, right NodePage, splitKey []byte, err error)

	// Validation
	Validate() error
}
