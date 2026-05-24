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
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
)

// leafPageHandle is a read-only handle to a leaf page in offheap memory.
// Short-lived: created per operation, discarded after use.
// All mutation methods follow COW semantics.
type leafPageHandle struct {
	id      model.PageID
	pa      *offheap.PageAccessor
	storage *OffheapBTreeStorage
}

func (h *leafPageHandle) PageID() model.PageID { return h.id }
func (h *leafPageHandle) IsLeaf() bool         { return true }

func (h *leafPageHandle) Count() int {
	rawID := uint32(h.id)
	return int(h.pa.GetCount(rawID))
}

func (h *leafPageHandle) Capacity() float64 {
	rawID := uint32(h.id)
	return h.pa.GetSpaceUsage(rawID)
}

func (h *leafPageHandle) IsFull(keyLen, valueLen int) bool {
	rawID := uint32(h.id)

	// 使用实际 key/value 长度精确计算空间需求
	requiredSpace := uint32(offheap.SizeofLeafEntry) + uint32(keyLen) + uint32(valueLen)

	count := h.pa.GetCount(rawID)
	dataEnd := h.pa.GetDataEnd(rawID)
	usedSpace := uint32(offheap.SizeofPageHeader) + uint32(count)*uint32(offheap.SizeofLeafEntry) + uint32(dataEnd)

	totalUsedAfterInsert := usedSpace + requiredSpace
	return float64(totalUsedAfterInsert)/float64(offheap.PageSize) > 0.95
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
		return nil, errpkg.BTreeLeafInsertAlloc(err)
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
		return nil, errpkg.BTreeLeafInsertEntry(err)
	}

	newID := model.PageID(newRawID)
	return &leafPageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
}

func (h *leafPageHandle) Update(idx int, value []byte) (LeafPage, error) {
	if idx < 0 || idx >= h.Count() {
		return nil, fmt.Errorf("btree: leaf update: index %d out of range [0, %d): %w", idx, h.Count(), ErrKeyNotFound)
	}

	rawID := uint32(h.id)
	srcVersion := h.pa.GetVersion(rawID)

	// Check old value slot size: if new value fits, use fast COW+overwrite path.
	_, _, _, oldValLen := h.pa.GetLeafEntryOffset(rawID, idx)
	if len(value) <= int(oldValLen) {
		newRawID, err := h.storage.pm.Alloc()
		if err != nil {
			return nil, errpkg.BTreeLeafUpdateAlloc(err)
		}
		srcPtr := h.storage.pm.PageIDToPtr(rawID)
		dstPtr := h.storage.pm.PageIDToPtr(newRawID)
		copy(unsafe.Slice((*byte)(dstPtr), offheap.PageSize), unsafe.Slice((*byte)(srcPtr), offheap.PageSize))
		h.pa.SetVersion(newRawID, srcVersion+1)

		if h.pa.OverwriteLeafValue(newRawID, idx, value) {
			return &leafPageHandle{id: model.PageID(newRawID), pa: h.pa, storage: h.storage}, nil
		}
		h.storage.pm.Free(newRawID)
	}

	// Slow path: new value is larger than old slot. Skip wasted COW copy,
	// directly rebuild page with new value at the correct position.
	key := h.GetKey(idx)
	keys, vals := h.pa.CollectKVExcept(rawID, idx)

	rebuildRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, errpkg.BTreeLeafUpdateRebuildAlloc(err)
	}
	h.pa.InitLeafPage(rebuildRawID, srcVersion+1)

	// Find insert position for the new key within the collected keys (which are sorted).
	insertPos := 0
	for insertPos < len(keys) && bytes.Compare(keys[insertPos], key) < 0 {
		insertPos++
	}

	// Build page: insert old entries before insertPos, then new entry, then rest.
	dataEnd := uint16(0)
	for i := 0; i < insertPos; i++ {
		if err := h.pa.InsertLeafEntry(rebuildRawID, i, keys[i], vals[i], &dataEnd); err != nil {
			h.storage.pm.Free(rebuildRawID)
			return nil, errpkg.BTreeLeafUpdateRebuild(err)
		}
	}
	if err := h.pa.InsertLeafEntry(rebuildRawID, insertPos, key, value, &dataEnd); err != nil {
		h.storage.pm.Free(rebuildRawID)
		return nil, errpkg.BTreeLeafUpdateReinsert(err)
	}
	for i := insertPos; i < len(keys); i++ {
		if err := h.pa.InsertLeafEntry(rebuildRawID, i+1, keys[i], vals[i], &dataEnd); err != nil {
			h.storage.pm.Free(rebuildRawID)
			return nil, errpkg.BTreeLeafUpdateRebuild(err)
		}
	}

	// Phase 6.5: propagate tombstoneCount through rebuild path.
	// Delta-based approach: start from old count, adjust for replaced entry.
	tc := h.pa.GetTombstoneCount(rawID)
	oldVal := h.GetValue(idx)
	if mv, err := mvcc.ParseMVCC(oldVal); err == nil && mv.IsTombstone() {
		tc-- // old tombstone entry removed
	}
	if mv, err := mvcc.ParseMVCC(value); err == nil && mv.IsTombstone() {
		tc++ // new tombstone entry inserted
	}
	h.pa.SetTombstoneCount(rebuildRawID, tc)

	return &leafPageHandle{id: model.PageID(rebuildRawID), pa: h.pa, storage: h.storage}, nil
}

func (h *leafPageHandle) Delete(idx int) (LeafPage, error) {
	count := h.Count()
	if idx < 0 || idx >= count {
		return nil, fmt.Errorf("btree: leaf delete: index %d out of range [0, %d): %w", idx, count, ErrKeyNotFound)
	}

	keys, vals := h.pa.CollectKVExcept(uint32(h.id), idx)

	newRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, errpkg.BTreeLeafDeleteAlloc(err)
	}
	srcVersion := h.pa.GetVersion(uint32(h.id))
	h.pa.InitLeafPage(newRawID, srcVersion+1)

	dataEnd := uint16(0)
	for i := range keys {
		if err := h.pa.InsertLeafEntry(newRawID, i, keys[i], vals[i], &dataEnd); err != nil {
			h.storage.pm.Free(newRawID)
			return nil, errpkg.BTreeLeafDeleteRebuild(err)
		}
	}

	newID := model.PageID(newRawID)
	return &leafPageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
}

func (h *leafPageHandle) Split() (LeafPage, LeafPage, []byte, error) {
	count := h.Count()
	if count < 2 {
		return nil, nil, nil, errpkg.BTreeLeafSplitMinKeys(count)
	}

	mid := count / 2

	// splitKey = right page's first key (copy-up: retained in right, also copied up to parent)
	keyOff, keyLen, _, _ := h.pa.GetLeafEntryOffset(uint32(h.id), mid)
	splitKey := h.pa.GetKey(uint32(h.id), keyOff, keyLen)
	splitKeyCopy := make([]byte, len(splitKey))
	copy(splitKeyCopy, splitKey)

	leftRawID, err := h.storage.pm.Alloc()
	if err != nil {
		return nil, nil, nil, errpkg.BTreeLeafSplitAllocLeft(err)
	}
	rightRawID, err := h.storage.pm.Alloc()
	if err != nil {
		h.storage.pm.Free(leftRawID)
		return nil, nil, nil, errpkg.BTreeLeafSplitAllocRight(err)
	}

	srcVersion := h.pa.GetVersion(uint32(h.id))

	if _, err := h.pa.BulkInitLeafFromSource(uint32(h.id), leftRawID, 0, mid); err != nil {
		h.storage.pm.Free(leftRawID)
		h.storage.pm.Free(rightRawID)
		return nil, nil, nil, errpkg.BTreeLeafSplitLeftBulkInit(err)
	}
	h.pa.SetVersion(leftRawID, srcVersion+1)

	if _, err := h.pa.BulkInitLeafFromSource(uint32(h.id), rightRawID, mid, count); err != nil {
		h.storage.pm.Free(leftRawID)
		h.storage.pm.Free(rightRawID)
		return nil, nil, nil, errpkg.BTreeLeafSplitRightBulkInit(err)
	}
	h.pa.SetVersion(rightRawID, srcVersion+1)

	// Phase 6.5 (G6): propagate tombstone counts to split halves
	leftTC := countTombstonesInRange(h, 0, mid)
	rightTC := countTombstonesInRange(h, mid, count)
	h.pa.SetTombstoneCount(leftRawID, leftTC)
	h.pa.SetTombstoneCount(rightRawID, rightTC)

	left := &leafPageHandle{id: model.PageID(leftRawID), pa: h.pa, storage: h.storage}
	right := &leafPageHandle{id: model.PageID(rightRawID), pa: h.pa, storage: h.storage}
	return left, right, splitKeyCopy, nil
}

// countTombstonesInRange counts MVCC tombstone entries in the given range.
// Uses direct mmap read of the flag byte (pa.GetValue with len=1) to avoid
// per-value heap allocation in the Split hot path.
func countTombstonesInRange(h *leafPageHandle, start, end int) uint16 {
	var tc uint16
	rawID := uint32(h.id)
	for i := start; i < end; i++ {
		_, _, valOff, valLen := h.pa.GetLeafEntryOffset(rawID, i)
		if valLen < 1 {
			continue
		}
		raw := h.pa.GetValue(rawID, valOff, 1)
		if raw[0] == mvcc.FlagTombstone {
			tc++
		}
	}
	return tc
}

// IncrementTombstone increments the tombstone count on the COW page.
func (h *leafPageHandle) IncrementTombstone() {
	h.pa.IncrementTombstone(uint32(h.id))
}

// DecrementTombstone decrements the tombstone count on the COW page.
func (h *leafPageHandle) DecrementTombstone() {
	h.pa.DecrementTombstone(uint32(h.id))
}

func (h *leafPageHandle) Validate() error {
	count := h.Count()
	if count < 0 {
		return errpkg.BTreeLeafValidateNegativeCount(count)
	}
	for i := 1; i < count; i++ {
		prev := h.GetKey(i - 1)
		curr := h.GetKey(i)
		if bytes.Compare(prev, curr) >= 0 {
			return errpkg.BTreeLeafValidateKeyOrderingViolation(i, prev, curr)
		}
	}
	return nil
}

// Release returns this handle to the pool for reuse.
// TryInPlace checks if value fits in the old slot for CAS-first in-place update.
// Returns true without modifying the page — the actual overwrite happens after CAS claim.
func (h *leafPageHandle) TryInPlace(idx int, value []byte) bool {
	rawID := uint32(h.id)
	_, _, _, oldValLen := h.pa.GetLeafEntryOffset(rawID, idx)
	return len(value) <= int(oldValLen)
}

func (h *leafPageHandle) Release() {
	h.storage.leafHandlePool.Put(h)
}
