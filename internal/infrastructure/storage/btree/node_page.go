// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"fmt"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
)

// nodePageHandle is a read-only handle to an index (internal) page in offheap memory.
// Short-lived: created per operation, discarded after use.
// All mutation methods follow COW semantics.
type nodePageHandle struct {
	id      model.PageID
	pa      *offheap.PageAccessor
	storage *OffheapBTreeStorage
}

func (h *nodePageHandle) PageID() model.PageID { return h.id }
func (h *nodePageHandle) IsLeaf() bool         { return false }

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
	if found {
		// B+Tree: exact match on key[i] → go to right subtree (child[i+1])
		return idx + 1, true
	}
	return idx, false
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

func (h *nodePageHandle) ReplaceChild(idx int, newChildID model.PageID) (NodePage, error) {
	count := h.Count()
	if idx < 0 || idx > count {
		return nil, fmt.Errorf("btree: node replace child: index %d out of range [0, %d]", idx, count)
	}

	newRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, errpkg.BTreeNodeReplaceChildAlloc(err)
	}
	srcPtr := h.storage.pm.PageIDToPtr(uint32(h.id))
	dstPtr := h.storage.pm.PageIDToPtr(newRawID)
	srcSlice := unsafe.Slice((*byte)(srcPtr), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(dstPtr), offheap.PageSize)
	copy(dstSlice, srcSlice)

	srcVersion := h.pa.GetVersion(uint32(h.id))
	h.pa.SetVersion(newRawID, srcVersion+1)

	h.pa.SetChild(newRawID, idx, uint32(newChildID))

	newID := model.PageID(newRawID)
	return &nodePageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
}

func (h *nodePageHandle) InsertChild(idx int, splitKey []byte, left, right model.PageID) (NodePage, error) {
	count := h.Count()
	if idx < 0 || idx > count {
		return nil, fmt.Errorf("btree: node insert child: index %d out of range [0, %d]", idx, count)
	}

	newRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, errpkg.BTreeNodeInsertChildAlloc(err)
	}
	srcPtr := h.storage.pm.PageIDToPtr(uint32(h.id))
	dstPtr := h.storage.pm.PageIDToPtr(newRawID)
	srcSlice := unsafe.Slice((*byte)(srcPtr), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(dstPtr), offheap.PageSize)
	copy(dstSlice, srcSlice)

	srcVersion := h.pa.GetVersion(uint32(h.id))
	h.pa.SetVersion(newRawID, srcVersion+1)

	dataEnd := h.pa.GetDataEnd(newRawID)

	if idx < count {
		// Middle insert: SetChild(idx, right) first, then InsertIndexEntry(idx, splitKey, left)
		// After: entries[idx]=(splitKey, left), entries[idx+1]=(old_key, right)
		h.pa.SetChild(newRawID, idx, uint32(right))
		if err := h.pa.InsertIndexEntry(newRawID, idx, splitKey, uint32(left), &dataEnd); err != nil {
			h.storage.pm.Free(newRawID)
			return nil, errpkg.BTreeNodeInsertChildEntry(err)
		}
	} else {
		// End insert: extraChild splits into left and right
		if err := h.pa.InsertIndexEntry(newRawID, count, splitKey, uint32(left), &dataEnd); err != nil {
			h.storage.pm.Free(newRawID)
			return nil, errpkg.BTreeNodeInsertChildAtEnd(err)
		}
		// After insert, count = old_count+1. SetChild(new_count, right) → sets extraChild
		h.pa.SetChild(newRawID, count+1, uint32(right))
	}

	newID := model.PageID(newRawID)
	return &nodePageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
}

func (h *nodePageHandle) RemoveChild(_ int) (NodePage, error) {
	return nil, fmt.Errorf("btree: NodePage.RemoveChild not implemented until Phase 6.5")
}

func (h *nodePageHandle) Split() (NodePage, NodePage, []byte, error) {
	count := h.Count()
	if count < 2 {
		return nil, nil, nil, errpkg.BTreeNodeSplitMinKeys(count)
	}

	mid := count / 2

	// move-up: splitKey is removed from both left and right, promoted to parent
	splitKey := h.GetKey(mid)
	splitKeyCopy := make([]byte, len(splitKey))
	copy(splitKeyCopy, splitKey)

	leftRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, nil, nil, errpkg.BTreeNodeSplitAllocLeft(err)
	}
	rightRawID, err := h.storage.pm.Alloc()
	if err != nil {
		h.storage.pm.Free(leftRawID)
		return nil, nil, nil, errpkg.BTreeNodeSplitAllocRight(err)
	}

	srcRawID := uint32(h.id)
	srcVersion := h.pa.GetVersion(srcRawID)

	// Left: entries[0..mid), extraChild = child[mid] (child before splitKey)
	leftExtraChild := h.pa.GetChild(srcRawID, mid)
	if _, err := h.pa.BulkInitIndexFromSource(srcRawID, leftRawID, 0, mid, leftExtraChild); err != nil {
		h.storage.pm.Free(leftRawID)
		h.storage.pm.Free(rightRawID)
		return nil, nil, nil, errpkg.BTreeNodeSplitLeftBulkInit(err)
	}
	h.pa.SetVersion(leftRawID, srcVersion+1)

	// Right: entries[mid+1..count), extraChild = original extraChild (child[count])
	rightExtraChild := h.pa.GetChild(srcRawID, count)
	if _, err := h.pa.BulkInitIndexFromSource(srcRawID, rightRawID, mid+1, count, rightExtraChild); err != nil {
		h.storage.pm.Free(leftRawID)
		h.storage.pm.Free(rightRawID)
		return nil, nil, nil, errpkg.BTreeNodeSplitRightBulkInit(err)
	}
	h.pa.SetVersion(rightRawID, srcVersion+1)

	left := &nodePageHandle{id: model.PageID(leftRawID), pa: h.pa, storage: h.storage}
	right := &nodePageHandle{id: model.PageID(rightRawID), pa: h.pa, storage: h.storage}
	return left, right, splitKeyCopy, nil
}

func (h *nodePageHandle) Validate() error {
	count := h.Count()
	if count < 0 {
		return errpkg.BTreeNodeValidateNegativeCount(count)
	}
	for i := 1; i < count; i++ {
		prev := h.GetKey(i - 1)
		curr := h.GetKey(i)
		if bytes.Compare(prev, curr) >= 0 {
			return errpkg.BTreeNodeValidateKeyOrderingViolation(i, prev, curr)
		}
	}
	if h.ChildCount() != count+1 {
		return fmt.Errorf("btree: node validate: child count %d != key count %d + 1", h.ChildCount(), count)
	}
	return nil
}
