// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"math"
	"sync"
	"sync/atomic"
	"unsafe"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/chunk"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
)

// OffheapBTreeStorage implements BTreeStorage using mmap-backed offheap pages.
// It is the sole bridge between btree and the offheap layer.
type OffheapBTreeStorage struct {
	pm         *offheap.PageManager
	pa         *offheap.PageAccessor
	cm         service.ChunkManager  // Phase 4.3: .ao chunk persistence
	serializer *chunk.PageSerializer // Phase 4.3: page serialization
	pageLocs   sync.Map              // Phase 4.3: map[model.PageID]model.ChunkPosition
	closed     atomic.Bool
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
	// Phase 4.3: Delete stale pageLocs entry before freeing pageID
	s.pageLocs.Delete(pageID)
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

// --- Phase 4.3: ChunkManager Integration ---

// SetChunkManager injects the ChunkManager and PageSerializer for AO persistence.
func (s *OffheapBTreeStorage) SetChunkManager(cm service.ChunkManager, serializer *chunk.PageSerializer) {
	s.cm = cm
	s.serializer = serializer
}

// UpdatePageLocs atomically updates the pageID→ChunkPosition mapping after checkpoint.
func (s *OffheapBTreeStorage) UpdatePageLocs(mapping map[model.PageID]model.ChunkPosition) {
	for pageID, pos := range mapping {
		s.pageLocs.Store(pageID, pos)
	}
}

// LoadPage lazily loads a page from AO storage into mmap.
func (s *OffheapBTreeStorage) LoadPage(pageID model.PageID) (unsafe.Pointer, error) {
	if s.cm == nil || s.serializer == nil {
		return nil, errpkg.BTreePageNotFound(uint64(pageID))
	}
	v, ok := s.pageLocs.Load(pageID)
	if !ok {
		return nil, errpkg.BTreePageNotFound(uint64(pageID))
	}
	pos := v.(model.ChunkPosition)
	if pos.IsZero() {
		return nil, errpkg.BTreePageNotFound(uint64(pageID))
	}
	rawID, err := s.pm.Alloc()
	if err != nil {
		return nil, errpkg.BTreeAllocLeafPage(err)
	}
	data, err := s.cm.ReadPage(pos)
	if err != nil {
		s.pm.Free(rawID)
		return nil, err
	}
	dst := s.pm.PageIDToPtr(rawID)
	if _, err := s.serializer.Deserialize(data, dst); err != nil {
		s.pm.Free(rawID)
		return nil, err
	}
	return dst, nil
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

func init() {
	if offheap.SizeofPageHeader != HeaderSize {
		panic("offheap.SizeofPageHeader != btree.HeaderSize: page layout mismatch")
	}
}
