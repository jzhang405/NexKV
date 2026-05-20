// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
)

// --- Phase 6.5: MergeLeaves ---

func (s *OffheapBTreeStorage) MergeLeaves(left, right LeafPage) (LeafPage, error) {
	leftCount := left.Count()
	rightCount := right.Count()

	newRawID, err := s.pm.Alloc()
	if err != nil {
		return nil, fmt.Errorf("btree: merge leaves alloc: %w", err)
	}

	srcVersion := s.pa.GetVersion(uint32(left.PageID()))
	s.pa.InitLeafPage(newRawID, srcVersion+1)

	leftKeys, leftVals := s.collectKVRange(uint32(left.PageID()), 0, leftCount)
	rightKeys, rightVals := s.collectKVRange(uint32(right.PageID()), 0, rightCount)

	dataEnd := uint16(0)
	for i := range leftKeys {
		if err := s.pa.InsertLeafEntry(newRawID, i, leftKeys[i], leftVals[i], &dataEnd); err != nil {
			s.pm.Free(newRawID)
			return nil, fmt.Errorf("btree: merge leaves insert left: %w", err)
		}
	}
	for i := range rightKeys {
		idx := len(leftKeys) + i
		if err := s.pa.InsertLeafEntry(newRawID, idx, rightKeys[i], rightVals[i], &dataEnd); err != nil {
			s.pm.Free(newRawID)
			return nil, fmt.Errorf("btree: merge leaves insert right: %w", err)
		}
	}

	// Phase 6.5 (G7): sum tombstone counts from both source pages
	leftTC := s.pa.GetTombstoneCount(uint32(left.PageID()))
	rightTC := s.pa.GetTombstoneCount(uint32(right.PageID()))
	s.pa.SetTombstoneCount(newRawID, leftTC+rightTC)

	newID := model.PageID(newRawID)
	return &leafPageHandle{id: newID, pa: s.pa, storage: s}, nil
}

func (s *OffheapBTreeStorage) collectKVRange(rawID uint32, start, end int) ([][]byte, [][]byte) {
	count := int(s.pa.GetCount(rawID))
	if end > count {
		end = count
	}
	var keys, vals [][]byte
	for i := start; i < end; i++ {
		keyOff, keyLen, valOff, valLen := s.pa.GetLeafEntryOffset(rawID, i)
		keys = append(keys, s.pa.GetKey(rawID, keyOff, keyLen))
		vals = append(vals, s.pa.GetValue(rawID, valOff, valLen))
	}
	return keys, vals
}

// --- Phase 6.5: BorrowFromLeftLeaf ---

func (s *OffheapBTreeStorage) BorrowFromLeftLeaf(self, sibling LeafPage) (LeafPage, LeafPage, error) {
	sibCount := sibling.Count()
	if sibCount == 0 {
		return nil, nil, ErrBorrowSourceEmpty
	}

	lastIdx := sibCount - 1
	borrowedKey := sibling.GetKey(lastIdx)
	borrowedVal := sibling.GetValue(lastIdx)

	selfRawID := uint32(self.PageID())

	// COW: copy entire page, then insert borrowed key at position 0
	// InsertLeafEntry shifts existing entries right automatically
	newSelfRawID, err := s.pm.Alloc()
	if err != nil {
		return nil, nil, fmt.Errorf("btree: borrow left leaf self alloc: %w", err)
	}
	srcSlice := unsafe.Slice((*byte)(s.pm.PageIDToPtr(selfRawID)), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(s.pm.PageIDToPtr(newSelfRawID)), offheap.PageSize)
	copy(dstSlice, srcSlice)

	selfVersion := s.pa.GetVersion(selfRawID)
	s.pa.SetVersion(newSelfRawID, selfVersion+1)

	// Insert the borrowed key at position 0
	dataEnd := s.pa.GetDataEnd(newSelfRawID)
	if err := s.pa.InsertLeafEntry(newSelfRawID, 0, borrowedKey, borrowedVal, &dataEnd); err != nil {
		s.pm.Free(newSelfRawID)
		return nil, nil, fmt.Errorf("btree: borrow left leaf self insert: %w", err)
	}

	sibRawID := uint32(sibling.PageID())
	sibKeys, sibVals := s.pa.CollectKVExcept(sibRawID, lastIdx)

	newSibRawID, err := s.pm.Alloc()
	if err != nil {
		s.pm.Free(newSelfRawID)
		return nil, nil, fmt.Errorf("btree: borrow left leaf sib alloc: %w", err)
	}
	sibVersion := s.pa.GetVersion(sibRawID)
	s.pa.InitLeafPage(newSibRawID, sibVersion+1)

	sibDataEnd := uint16(0)
	for i := range sibKeys {
		if err := s.pa.InsertLeafEntry(newSibRawID, i, sibKeys[i], sibVals[i], &sibDataEnd); err != nil {
			s.pm.Free(newSelfRawID)
			s.pm.Free(newSibRawID)
			return nil, nil, fmt.Errorf("btree: borrow left leaf sib rebuild: %w", err)
		}
	}

	newSelf := &leafPageHandle{id: model.PageID(newSelfRawID), pa: s.pa, storage: s}
	newSib := &leafPageHandle{id: model.PageID(newSibRawID), pa: s.pa, storage: s}
	return newSelf, newSib, nil
}

// --- Phase 6.5: BorrowFromRightLeaf ---

func (s *OffheapBTreeStorage) BorrowFromRightLeaf(self, sibling LeafPage) (LeafPage, LeafPage, error) {
	sibCount := sibling.Count()
	if sibCount == 0 {
		return nil, nil, ErrBorrowSourceEmpty
	}

	borrowedKey := sibling.GetKey(0)
	borrowedVal := sibling.GetValue(0)

	selfRawID := uint32(self.PageID())
	newSelfRawID, err := s.pm.Alloc()
	if err != nil {
		return nil, nil, fmt.Errorf("btree: borrow right leaf self alloc: %w", err)
	}
	srcSlice := unsafe.Slice((*byte)(s.pm.PageIDToPtr(selfRawID)), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(s.pm.PageIDToPtr(newSelfRawID)), offheap.PageSize)
	copy(dstSlice, srcSlice)

	selfVersion := s.pa.GetVersion(selfRawID)
	s.pa.SetVersion(newSelfRawID, selfVersion+1)

	dataEnd := s.pa.GetDataEnd(newSelfRawID)
	if err := s.pa.InsertLeafEntry(newSelfRawID, self.Count(), borrowedKey, borrowedVal, &dataEnd); err != nil {
		s.pm.Free(newSelfRawID)
		return nil, nil, fmt.Errorf("btree: borrow right leaf self insert: %w", err)
	}

	sibRawID := uint32(sibling.PageID())
	sibKeys, sibVals := s.pa.CollectKVExcept(sibRawID, 0)

	newSibRawID, err := s.pm.Alloc()
	if err != nil {
		s.pm.Free(newSelfRawID)
		return nil, nil, fmt.Errorf("btree: borrow right leaf sib alloc: %w", err)
	}
	sibVersion := s.pa.GetVersion(sibRawID)
	s.pa.InitLeafPage(newSibRawID, sibVersion+1)

	sibDataEnd := uint16(0)
	for i := range sibKeys {
		if err := s.pa.InsertLeafEntry(newSibRawID, i, sibKeys[i], sibVals[i], &sibDataEnd); err != nil {
			s.pm.Free(newSelfRawID)
			s.pm.Free(newSibRawID)
			return nil, nil, fmt.Errorf("btree: borrow right leaf sib rebuild: %w", err)
		}
	}

	newSelf := &leafPageHandle{id: model.PageID(newSelfRawID), pa: s.pa, storage: s}
	newSib := &leafPageHandle{id: model.PageID(newSibRawID), pa: s.pa, storage: s}
	return newSelf, newSib, nil
}

// --- Phase 6.5: MergeNodes ---

func (s *OffheapBTreeStorage) MergeNodes(left, right NodePage, separator []byte) (NodePage, error) {
	leftCount := left.Count()
	rightCount := right.Count()

	newRawID, err := s.pm.Alloc()
	if err != nil {
		return nil, fmt.Errorf("btree: merge nodes alloc: %w", err)
	}

	srcVersion := s.pa.GetVersion(uint32(left.PageID()))
	s.pa.InitIndexPage(newRawID, srcVersion+1)

	dataEnd := uint16(0)
	leftRawID := uint32(left.PageID())
	rightRawID := uint32(right.PageID())

	for i := 0; i < leftCount; i++ {
		keyOff, keyLen, childU64 := s.pa.GetIndexEntryOffset(leftRawID, i)
		key := s.pa.GetKey(leftRawID, keyOff, keyLen)
		if err := s.pa.InsertIndexEntry(newRawID, i, key, uint32(childU64), &dataEnd); err != nil {
			s.pm.Free(newRawID)
			return nil, fmt.Errorf("btree: merge nodes insert left: %w", err)
		}
	}

	leftExtraChild := uint32(s.pa.GetChild(leftRawID, leftCount))
	if err := s.pa.InsertIndexEntry(newRawID, leftCount, separator, leftExtraChild, &dataEnd); err != nil {
		s.pm.Free(newRawID)
		return nil, fmt.Errorf("btree: merge nodes insert separator: %w", err)
	}

	for i := 0; i < rightCount; i++ {
		keyOff, keyLen, childU64 := s.pa.GetIndexEntryOffset(rightRawID, i)
		key := s.pa.GetKey(rightRawID, keyOff, keyLen)
		if err := s.pa.InsertIndexEntry(newRawID, leftCount+1+i, key, uint32(childU64), &dataEnd); err != nil {
			s.pm.Free(newRawID)
			return nil, fmt.Errorf("btree: merge nodes insert right: %w", err)
		}
	}

	rightExtraChild := uint32(s.pa.GetChild(rightRawID, rightCount))
	s.pa.SetChild(newRawID, leftCount+1+rightCount, rightExtraChild)

	newID := model.PageID(newRawID)
	return &nodePageHandle{id: newID, pa: s.pa, storage: s}, nil
}

// --- Phase 6.5: BorrowFromLeftNode ---

func (s *OffheapBTreeStorage) BorrowFromLeftNode(self, sibling NodePage, _ []byte) (NodePage, NodePage, []byte, error) {
	sibCount := sibling.Count()
	if sibCount == 0 {
		return nil, nil, nil, ErrBorrowSourceEmpty
	}

	sibRawID := uint32(sibling.PageID())
	lastIdx := sibCount - 1

	sibLastKeyOff, sibLastKeyLen, _ := s.pa.GetIndexEntryOffset(sibRawID, lastIdx)
	sibLastKey := s.pa.GetKey(sibRawID, sibLastKeyOff, sibLastKeyLen)
	sibExtraChild := uint32(s.pa.GetChild(sibRawID, sibCount))

	newSep := make([]byte, len(sibLastKey))
	copy(newSep, sibLastKey)

	selfRawID := uint32(self.PageID())
	selfCount := self.Count()

	selfKeys := make([][]byte, selfCount)
	selfChildren := make([]uint32, selfCount)
	for i := 0; i < selfCount; i++ {
		ko, kl, chU64 := s.pa.GetIndexEntryOffset(selfRawID, i)
		selfKeys[i] = s.pa.GetKey(selfRawID, ko, kl)
		selfChildren[i] = uint32(chU64)
	}
	selfExtraChild := uint32(s.pa.GetChild(selfRawID, selfCount))

	newSelfRawID, err := s.pm.Alloc()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("btree: borrow left node self alloc: %w", err)
	}
	selfVersion := s.pa.GetVersion(selfRawID)
	s.pa.InitIndexPage(newSelfRawID, selfVersion+1)

	dataEnd := uint16(0)
	if err := s.pa.InsertIndexEntry(newSelfRawID, 0, sibLastKey, sibExtraChild, &dataEnd); err != nil {
		s.pm.Free(newSelfRawID)
		return nil, nil, nil, fmt.Errorf("btree: borrow left node self insert: %w", err)
	}
	for i := 0; i < selfCount; i++ {
		if err := s.pa.InsertIndexEntry(newSelfRawID, i+1, selfKeys[i], selfChildren[i], &dataEnd); err != nil {
			s.pm.Free(newSelfRawID)
			return nil, nil, nil, fmt.Errorf("btree: borrow left node self rebuild: %w", err)
		}
	}
	s.pa.SetChild(newSelfRawID, selfCount+1, selfExtraChild)

	newSibRawID, err := s.pm.Alloc()
	if err != nil {
		s.pm.Free(newSelfRawID)
		return nil, nil, nil, fmt.Errorf("btree: borrow left node sib alloc: %w", err)
	}
	sibVersion := s.pa.GetVersion(sibRawID)
	s.pa.InitIndexPage(newSibRawID, sibVersion+1)

	sibDataEnd := uint16(0)
	for i := 0; i < lastIdx; i++ {
		ko, kl, chU64 := s.pa.GetIndexEntryOffset(sibRawID, i)
		key := s.pa.GetKey(sibRawID, ko, kl)
		if err := s.pa.InsertIndexEntry(newSibRawID, i, key, uint32(chU64), &sibDataEnd); err != nil {
			s.pm.Free(newSelfRawID)
			s.pm.Free(newSibRawID)
			return nil, nil, nil, fmt.Errorf("btree: borrow left node sib rebuild: %w", err)
		}
	}
	newSibExtraChild := uint32(s.pa.GetChild(sibRawID, lastIdx))
	s.pa.SetChild(newSibRawID, lastIdx, newSibExtraChild)

	newSelf := &nodePageHandle{id: model.PageID(newSelfRawID), pa: s.pa, storage: s}
	newSib := &nodePageHandle{id: model.PageID(newSibRawID), pa: s.pa, storage: s}
	return newSelf, newSib, newSep, nil
}

// --- Phase 6.5: BorrowFromRightNode ---

func (s *OffheapBTreeStorage) BorrowFromRightNode(self, sibling NodePage, _ []byte) (NodePage, NodePage, []byte, error) {
	sibCount := sibling.Count()
	if sibCount == 0 {
		return nil, nil, nil, ErrBorrowSourceEmpty
	}

	sibRawID := uint32(sibling.PageID())

	sibFirstKeyOff, sibFirstKeyLen, sibFirstChildU64 := s.pa.GetIndexEntryOffset(sibRawID, 0)
	sibFirstKey := s.pa.GetKey(sibRawID, sibFirstKeyOff, sibFirstKeyLen)
	sibFirstChild := uint32(sibFirstChildU64)

	newSep := make([]byte, len(sibFirstKey))
	copy(newSep, sibFirstKey)

	selfRawID := uint32(self.PageID())
	selfCount := self.Count()
	selfExtraChild := uint32(s.pa.GetChild(selfRawID, selfCount))

	newSelfRawID, err := s.pm.Alloc()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("btree: borrow right node self alloc: %w", err)
	}
	srcSlice := unsafe.Slice((*byte)(s.pm.PageIDToPtr(selfRawID)), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(s.pm.PageIDToPtr(newSelfRawID)), offheap.PageSize)
	copy(dstSlice, srcSlice)

	selfVersion := s.pa.GetVersion(selfRawID)
	s.pa.SetVersion(newSelfRawID, selfVersion+1)

	dataEnd := s.pa.GetDataEnd(newSelfRawID)
	if err := s.pa.InsertIndexEntry(newSelfRawID, selfCount, sibFirstKey, sibFirstChild, &dataEnd); err != nil {
		s.pm.Free(newSelfRawID)
		return nil, nil, nil, fmt.Errorf("btree: borrow right node self insert: %w", err)
	}
	s.pa.SetChild(newSelfRawID, selfCount+1, selfExtraChild)

	newSibRawID, err := s.pm.Alloc()
	if err != nil {
		s.pm.Free(newSelfRawID)
		return nil, nil, nil, fmt.Errorf("btree: borrow right node sib alloc: %w", err)
	}
	sibVersion := s.pa.GetVersion(sibRawID)
	s.pa.InitIndexPage(newSibRawID, sibVersion+1)

	sibDataEnd := uint16(0)
	for i := 1; i < sibCount; i++ {
		ko, kl, chU64 := s.pa.GetIndexEntryOffset(sibRawID, i)
		key := s.pa.GetKey(sibRawID, ko, kl)
		if err := s.pa.InsertIndexEntry(newSibRawID, i-1, key, uint32(chU64), &sibDataEnd); err != nil {
			s.pm.Free(newSelfRawID)
			s.pm.Free(newSibRawID)
			return nil, nil, nil, fmt.Errorf("btree: borrow right node sib rebuild: %w", err)
		}
	}
	sibOrigExtraChild := uint32(s.pa.GetChild(sibRawID, sibCount))
	s.pa.SetChild(newSibRawID, sibCount-1, sibOrigExtraChild)

	newSelf := &nodePageHandle{id: model.PageID(newSelfRawID), pa: s.pa, storage: s}
	newSib := &nodePageHandle{id: model.PageID(newSibRawID), pa: s.pa, storage: s}
	return newSelf, newSib, newSep, nil
}
