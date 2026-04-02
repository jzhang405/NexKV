// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree2

import (
	"fmt"
	"math"
	"sync/atomic"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
)

// OffheapBTreeStorage implements BTreeStorage using mmap-backed offheap pages.
// It is the sole bridge between btree2 and the offheap layer.
type OffheapBTreeStorage struct {
	pm     *offheap.PageManager
	pa     *offheap.PageAccessor
	closed atomic.Bool
}

// NewOffheapBTreeStorage creates a new storage backed by an mmap region of mmapSize bytes.
func NewOffheapBTreeStorage(mmapSize int) (*OffheapBTreeStorage, error) {
	pm, err := offheap.NewPageManager(mmapSize)
	if err != nil {
		return nil, fmt.Errorf("btree2: create page manager: %w", err)
	}
	return &OffheapBTreeStorage{
		pm: pm,
		pa: offheap.NewPageAccessor(pm),
	}, nil
}

// --- Page Allocation ---

func (s *OffheapBTreeStorage) checkOpen() error {
	if s.closed.Load() {
		return ErrTreeClosed
	}
	return nil
}

func (s *OffheapBTreeStorage) AllocLeafPage() (model.PageID, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	rawID, err := s.pm.Alloc()
	if err != nil {
		return 0, fmt.Errorf("btree2: alloc leaf page: %w", err)
	}
	s.pa.InitLeafPage(rawID, 1)
	return model.PageID(rawID), nil
}

func (s *OffheapBTreeStorage) AllocNodePage() (model.PageID, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	rawID, err := s.pm.Alloc()
	if err != nil {
		return 0, fmt.Errorf("btree2: alloc node page: %w", err)
	}
	s.pa.InitIndexPage(rawID, 1)
	return model.PageID(rawID), nil
}

// --- COW Core ---

// copyPage allocates a new physical page, copies all 4096 bytes from src,
// and increments the version. This is the COW primitive.
func (s *OffheapBTreeStorage) copyPage(rawSrcID uint32) (uint32, error) {
	srcVersion := s.pa.GetVersion(rawSrcID)

	newRawID, err := s.pm.Alloc()
	if err != nil {
		return 0, fmt.Errorf("btree2: alloc for cow: %w", err)
	}

	srcPtr := s.pm.PageIDToPtr(rawSrcID)
	dstPtr := s.pm.PageIDToPtr(newRawID)

	srcSlice := unsafe.Slice((*byte)(srcPtr), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(dstPtr), offheap.PageSize)

	copy(dstSlice, srcSlice)
	s.pa.SetVersion(newRawID, srcVersion+1)

	return newRawID, nil
}

func (s *OffheapBTreeStorage) CopyLeafPage(srcID model.PageID) (model.PageID, LeafPage, error) {
	rawID, err := s.validatePageID(srcID)
	if err != nil {
		return 0, nil, err
	}
	newRawID, err := s.copyPage(rawID)
	if err != nil {
		return 0, nil, err
	}
	newID := model.PageID(newRawID)
	return newID, &leafPageHandle{id: newID, pa: s.pa, storage: s}, nil
}

func (s *OffheapBTreeStorage) CopyNodePage(srcID model.PageID) (model.PageID, NodePage, error) {
	rawID, err := s.validatePageID(srcID)
	if err != nil {
		return 0, nil, err
	}
	newRawID, err := s.copyPage(rawID)
	if err != nil {
		return 0, nil, err
	}
	newID := model.PageID(newRawID)
	return newID, &nodePageHandle{id: newID, pa: s.pa, storage: s}, nil
}

// --- Page Access ---

func (s *OffheapBTreeStorage) GetLeafPage(pageID model.PageID) (LeafPage, error) {
	rawID, err := s.validatePageID(pageID)
	if err != nil {
		return nil, err
	}
	if !s.pa.IsLeaf(rawID) {
		return nil, fmt.Errorf("btree2: page %d is not a leaf page", pageID)
	}
	return &leafPageHandle{id: pageID, pa: s.pa, storage: s}, nil
}

func (s *OffheapBTreeStorage) GetNodePage(pageID model.PageID) (NodePage, error) {
	rawID, err := s.validatePageID(pageID)
	if err != nil {
		return nil, err
	}
	if s.pa.IsLeaf(rawID) {
		return nil, fmt.Errorf("btree2: page %d is not a node page", pageID)
	}
	return &nodePageHandle{id: pageID, pa: s.pa, storage: s}, nil
}

// --- Free ---

func (s *OffheapBTreeStorage) FreePage(pageID model.PageID) error {
	rawID, err := s.validatePageID(pageID)
	if err != nil {
		return err
	}
	return s.pm.Free(rawID)
}

// --- Close ---

func (s *OffheapBTreeStorage) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil // already closed
	}
	return s.pm.Close()
}

// --- Phase 6.5 Stubs ---

func (s *OffheapBTreeStorage) MergeLeaves(_, _ LeafPage) (LeafPage, error) {
	return nil, ErrNotImplemented
}

func (s *OffheapBTreeStorage) BorrowFromLeftLeaf(_, _ LeafPage) (LeafPage, LeafPage, error) {
	return nil, nil, ErrNotImplemented
}

func (s *OffheapBTreeStorage) BorrowFromRightLeaf(_, _ LeafPage) (LeafPage, LeafPage, error) {
	return nil, nil, ErrNotImplemented
}

func (s *OffheapBTreeStorage) MergeNodes(_, _ NodePage, _ []byte) (NodePage, error) {
	return nil, ErrNotImplemented
}

func (s *OffheapBTreeStorage) BorrowFromLeftNode(_, _ NodePage, _ []byte) (NodePage, NodePage, []byte, error) {
	return nil, nil, nil, ErrNotImplemented
}

func (s *OffheapBTreeStorage) BorrowFromRightNode(_, _ NodePage, _ []byte) (NodePage, NodePage, []byte, error) {
	return nil, nil, nil, ErrNotImplemented
}

// --- Internal Helpers ---

func (s *OffheapBTreeStorage) validatePageID(id model.PageID) (uint32, error) {
	if id > math.MaxUint32 {
		return 0, fmt.Errorf("btree2: pageID %d exceeds uint32 max: %w", id, ErrInvalidPage)
	}
	return uint32(id), nil
}
