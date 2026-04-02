// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree2

import (
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
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

// --- Phase 1 stub implementations ---

// leafPageHandle is a read-only handle to a leaf page in offheap memory.
// Phase 2 will replace the panic stubs with full COW implementations.
type leafPageHandle struct {
	id model.PageID
	pa *offheap.PageAccessor
}

func (h *leafPageHandle) PageID() model.PageID { return h.id }
func (h *leafPageHandle) IsLeaf() bool          { return true }

func (h *leafPageHandle) Count() int {
	rawID := uint32(h.id)
	return int(h.pa.GetCount(rawID))
}

func (h *leafPageHandle) Capacity() float64 {
	rawID := uint32(h.id)
	return h.pa.GetSpaceUsage(rawID)
}

func (h *leafPageHandle) IsFull() bool {
	return h.Capacity() > 0.95
}

func (h *leafPageHandle) Search(key []byte) (int, bool) {
	rawID := uint32(h.id)
	idx, found, _ := h.pa.SearchKey(rawID, key, true)
	return int(idx), found
}

func (h *leafPageHandle) GetKey(idx int) []byte {
	rawID := uint32(h.id)
	keyOff, keyLen, _, _ := h.pa.GetLeafEntryOffset(rawID, idx)
	return h.pa.GetKey(rawID, keyOff, keyLen)
}

func (h *leafPageHandle) GetValue(idx int) []byte {
	rawID := uint32(h.id)
	_, _, valOff, valLen := h.pa.GetLeafEntryOffset(rawID, idx)
	return h.pa.GetValue(rawID, valOff, valLen)
}

func (h *leafPageHandle) Insert(_, _ []byte) (LeafPage, error) {
	panic("btree2: LeafPage.Insert not implemented until Phase 2")
}
func (h *leafPageHandle) Update(_ int, _ []byte) (LeafPage, error) {
	panic("btree2: LeafPage.Update not implemented until Phase 2")
}
func (h *leafPageHandle) Delete(_ int) (LeafPage, error) {
	panic("btree2: LeafPage.Delete not implemented until Phase 2")
}
func (h *leafPageHandle) Split() (LeafPage, LeafPage, []byte, error) {
	panic("btree2: LeafPage.Split not implemented until Phase 2")
}
func (h *leafPageHandle) Validate() error {
	panic("btree2: LeafPage.Validate not implemented until Phase 2")
}

// nodePageHandle is a read-only handle to an index page in offheap memory.
type nodePageHandle struct {
	id model.PageID
	pa *offheap.PageAccessor
}

func (h *nodePageHandle) PageID() model.PageID { return h.id }
func (h *nodePageHandle) IsLeaf() bool          { return false }

func (h *nodePageHandle) Count() int {
	rawID := uint32(h.id)
	return int(h.pa.GetCount(rawID))
}

func (h *nodePageHandle) Capacity() float64 {
	rawID := uint32(h.id)
	return h.pa.GetSpaceUsage(rawID)
}

func (h *nodePageHandle) IsFull() bool {
	return h.Count() >= MaxInternalKeys
}

func (h *nodePageHandle) Search(key []byte) (int, bool) {
	rawID := uint32(h.id)
	idx, found := h.pa.SearchChildIndex(rawID, key)
	return idx, found
}

func (h *nodePageHandle) GetKey(idx int) []byte {
	rawID := uint32(h.id)
	keyOff, keyLen, _ := h.pa.GetIndexEntryOffset(rawID, idx)
	return h.pa.GetKey(rawID, keyOff, keyLen)
}

func (h *nodePageHandle) GetChild(idx int) model.PageID {
	rawID := uint32(h.id)
	encoded := h.pa.GetChild(rawID, idx)
	childID, _ := offheap.DecodeChildWithVersion(encoded)
	return model.PageID(childID)
}

func (h *nodePageHandle) ChildCount() int {
	return h.Count() + 1 // B+Tree: N keys → N+1 children
}

func (h *nodePageHandle) ReplaceChild(_ int, _ model.PageID) (NodePage, error) {
	panic("btree2: NodePage.ReplaceChild not implemented until Phase 3")
}
func (h *nodePageHandle) InsertChild(_ int, _ []byte, _, _ model.PageID) (NodePage, error) {
	panic("btree2: NodePage.InsertChild not implemented until Phase 3")
}
func (h *nodePageHandle) RemoveChild(_ int) (NodePage, error) {
	panic("btree2: NodePage.RemoveChild not implemented until Phase 6.5")
}
func (h *nodePageHandle) Split() (NodePage, NodePage, []byte, error) {
	panic("btree2: NodePage.Split not implemented until Phase 3")
}
func (h *nodePageHandle) Validate() error {
	return fmt.Errorf("btree2: NodePage.Validate not implemented until Phase 3")
}
