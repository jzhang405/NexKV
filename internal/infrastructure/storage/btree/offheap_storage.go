// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"math"
	"sync/atomic"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
)

// OffheapBTreeStorage implements BTreeStorage using mmap-backed offheap pages.
// It is the sole bridge between btree and the offheap layer.
type OffheapBTreeStorage struct {
	pm     *offheap.PageManager
	pa     *offheap.PageAccessor
	closed atomic.Bool
}

// NewOffheapBTreeStorage creates a new storage backed by an mmap region of mmapSize bytes.
func NewOffheapBTreeStorage(mmapSize int) (*OffheapBTreeStorage, error) {
	pm, err := offheap.NewPageManager(mmapSize)
	if err != nil {
		return nil, errpkg.BTreeCreatePageManager(err)
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
		return 0, errpkg.BTreeAllocLeafPage(err)
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
		return 0, errpkg.BTreeAllocNodePage(err)
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
		return 0, errpkg.BTreeAllocForCOW(err)
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
		return nil, errpkg.BTreePageNotLeafPage(uint64(pageID))
	}
	return &leafPageHandle{id: pageID, pa: s.pa, storage: s}, nil
}

func (s *OffheapBTreeStorage) GetNodePage(pageID model.PageID) (NodePage, error) {
	rawID, err := s.validatePageID(pageID)
	if err != nil {
		return nil, err
	}
	if s.pa.IsLeaf(rawID) {
		return nil, errpkg.BTreePageNotNodePage(uint64(pageID))
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

// IsLeafPage reads the physical page header to determine if pageID is a leaf.
// GetPageAccessor 返回底层的 PageAccessor，用于 Inspector 遍历物理页面。
func (s *OffheapBTreeStorage) GetPageAccessor() *offheap.PageAccessor {
	return s.pa
}

// AllocatedPageCount 返回已分配过的页面数量上限（pageID ∈ [1, AllocatedPageCount())）。
func (s *OffheapBTreeStorage) AllocatedPageCount() uint32 {
	return s.pm.NextPageID()
}

func (s *OffheapBTreeStorage) IsLeafPage(pageID model.PageID) bool {
	rawID, err := s.validatePageID(pageID)
	if err != nil {
		return false
	}
	return s.pa.IsLeaf(rawID)
}

// --- Close ---

func (s *OffheapBTreeStorage) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil // already closed
	}
	return s.pm.Close()
}

// Compile-time interface satisfaction
var _ PageMerger = (*OffheapBTreeStorage)(nil)

// --- Internal Helpers ---

func (s *OffheapBTreeStorage) validatePageID(id model.PageID) (uint32, error) {
	if id > math.MaxUint32 {
		return 0, errpkg.BTreePageIDExceedsMax(uint64(id))
	}
	return uint32(id), nil
}
