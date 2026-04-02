// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree2

import (
	"bytes"
	"fmt"
	"unsafe"

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
	id      model.PageID
	pa      *offheap.PageAccessor
	storage *OffheapBTreeStorage
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
	raw := h.pa.GetKey(rawID, keyOff, keyLen)
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp
}

func (h *leafPageHandle) GetValue(idx int) []byte {
	rawID := uint32(h.id)
	_, _, valOff, valLen := h.pa.GetLeafEntryOffset(rawID, idx)
	raw := h.pa.GetValue(rawID, valOff, valLen)
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp
}

func (h *leafPageHandle) Insert(key, value []byte) (LeafPage, error) {
	// COW: allocate new page, copy 4096 bytes, modify the copy
	newRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, fmt.Errorf("btree2: leaf insert alloc: %w", err)
	}

	srcPtr := h.storage.pm.PageIDToPtr(uint32(h.id))
	dstPtr := h.storage.pm.PageIDToPtr(newRawID)
	srcSlice := unsafe.Slice((*byte)(srcPtr), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(dstPtr), offheap.PageSize)
	copy(dstSlice, srcSlice)

	srcVersion := h.pa.GetVersion(uint32(h.id))
	h.pa.SetVersion(newRawID, srcVersion+1)

	// Search in the new (copied) page
	rawNewID := newRawID
	idx, found, _ := h.pa.SearchKey(rawNewID, key, true)
	if found {
		h.storage.pm.Free(newRawID)
		return nil, ErrDuplicateKey
	}

	dataEnd := h.pa.GetDataEnd(rawNewID)
	if err := h.pa.InsertLeafEntry(rawNewID, int(idx), key, value, &dataEnd); err != nil {
		h.storage.pm.Free(newRawID)
		return nil, fmt.Errorf("btree2: leaf insert entry: %w", err)
	}

	newID := model.PageID(newRawID)
	return &leafPageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
}
func (h *leafPageHandle) Update(idx int, value []byte) (LeafPage, error) {
	if idx < 0 || idx >= h.Count() {
		return nil, fmt.Errorf("btree2: leaf update: index %d out of range [0, %d): %w", idx, h.Count(), ErrKeyNotFound)
	}

	// COW copy
	newRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, fmt.Errorf("btree2: leaf update alloc: %w", err)
	}
	srcPtr := h.storage.pm.PageIDToPtr(uint32(h.id))
	dstPtr := h.storage.pm.PageIDToPtr(newRawID)
	srcSlice := unsafe.Slice((*byte)(srcPtr), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(dstPtr), offheap.PageSize)
	copy(dstSlice, srcSlice)

	srcVersion := h.pa.GetVersion(uint32(h.id))
	h.pa.SetVersion(newRawID, srcVersion+1)

	// Try in-place overwrite on the new page
	if h.pa.OverwriteLeafValue(newRawID, idx, value) {
		newID := model.PageID(newRawID)
		return &leafPageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
	}

	// Value is larger than original — fall back to delete + insert
	key := h.GetKey(idx) // returns a copy from the original page
	keys, vals := h.pa.CollectKVExcept(newRawID, idx)
	h.storage.pm.Free(newRawID)

	// Rebuild page without the old entry, then insert new KV
	rebuildRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, fmt.Errorf("btree2: leaf update rebuild alloc: %w", err)
	}
	h.pa.InitLeafPage(rebuildRawID, srcVersion+1)
	dataEnd := uint16(0)
	for i := range keys {
		if err := h.pa.InsertLeafEntry(rebuildRawID, i, keys[i], vals[i], &dataEnd); err != nil {
			h.storage.pm.Free(rebuildRawID)
			return nil, fmt.Errorf("btree2: leaf update rebuild: %w", err)
		}
	}
	// Insert the new KV pair
	insertIdx, _, _ := h.pa.SearchKey(rebuildRawID, key, true)
	if err := h.pa.InsertLeafEntry(rebuildRawID, insertIdx, key, value, &dataEnd); err != nil {
		h.storage.pm.Free(rebuildRawID)
		return nil, fmt.Errorf("btree2: leaf update reinsert: %w", err)
	}

	newID := model.PageID(rebuildRawID)
	return &leafPageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
}
func (h *leafPageHandle) Delete(idx int) (LeafPage, error) {
	count := h.Count()
	if idx < 0 || idx >= count {
		return nil, fmt.Errorf("btree2: leaf delete: index %d out of range [0, %d): %w", idx, count, ErrKeyNotFound)
	}

	keys, vals := h.pa.CollectKVExcept(uint32(h.id), idx)

	newRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, fmt.Errorf("btree2: leaf delete alloc: %w", err)
	}
	srcVersion := h.pa.GetVersion(uint32(h.id))
	h.pa.InitLeafPage(newRawID, srcVersion+1)

	dataEnd := uint16(0)
	for i := range keys {
		if err := h.pa.InsertLeafEntry(newRawID, i, keys[i], vals[i], &dataEnd); err != nil {
			h.storage.pm.Free(newRawID)
			return nil, fmt.Errorf("btree2: leaf delete rebuild: %w", err)
		}
	}

	newID := model.PageID(newRawID)
	return &leafPageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
}
func (h *leafPageHandle) Split() (LeafPage, LeafPage, []byte, error) {
	count := h.Count()
	if count < 2 {
		return nil, nil, nil, fmt.Errorf("btree2: leaf split: page has %d entries, need at least 2", count)
	}

	mid := count / 2

	// splitKey = right page's first key (copy-up: retained in right, also copied up to parent)
	keyOff, keyLen, _, _ := h.pa.GetLeafEntryOffset(uint32(h.id), mid)
	splitKey := h.pa.GetKey(uint32(h.id), keyOff, keyLen)
	splitKeyCopy := make([]byte, len(splitKey))
	copy(splitKeyCopy, splitKey)

	leftRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("btree2: leaf split alloc left: %w", err)
	}
	rightRawID, err := h.storage.pm.Alloc()
	if err != nil {
		h.storage.pm.Free(leftRawID)
		return nil, nil, nil, fmt.Errorf("btree2: leaf split alloc right: %w", err)
	}

	srcVersion := h.pa.GetVersion(uint32(h.id))

	if _, err := h.pa.BulkInitLeafFromSource(uint32(h.id), leftRawID, 0, mid); err != nil {
		h.storage.pm.Free(leftRawID)
		h.storage.pm.Free(rightRawID)
		return nil, nil, nil, fmt.Errorf("btree2: leaf split left bulk init: %w", err)
	}
	h.pa.SetVersion(leftRawID, srcVersion+1)

	if _, err := h.pa.BulkInitLeafFromSource(uint32(h.id), rightRawID, mid, count); err != nil {
		h.storage.pm.Free(leftRawID)
		h.storage.pm.Free(rightRawID)
		return nil, nil, nil, fmt.Errorf("btree2: leaf split right bulk init: %w", err)
	}
	h.pa.SetVersion(rightRawID, srcVersion+1)

	left := &leafPageHandle{id: model.PageID(leftRawID), pa: h.pa, storage: h.storage}
	right := &leafPageHandle{id: model.PageID(rightRawID), pa: h.pa, storage: h.storage}
	return left, right, splitKeyCopy, nil
}
func (h *leafPageHandle) Validate() error {
	count := h.Count()
	if count < 0 {
		return fmt.Errorf("btree2: leaf validate: negative count %d", count)
	}
	for i := 1; i < count; i++ {
		prev := h.GetKey(i - 1)
		curr := h.GetKey(i)
		if bytes.Compare(prev, curr) >= 0 {
			return fmt.Errorf("btree2: leaf validate: key ordering violation at idx %d: %q >= %q", i, prev, curr)
		}
	}
	return nil
}

// nodePageHandle is a read-only handle to an index page in offheap memory.
type nodePageHandle struct {
	id      model.PageID
	pa      *offheap.PageAccessor
	storage *OffheapBTreeStorage
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
