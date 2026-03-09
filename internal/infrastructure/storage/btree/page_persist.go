// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

var (
	// ErrPageNotFound is returned when a page cannot be found.
	ErrPageNotFound = errors.New("page not found")

	// ErrPageCorrupted is returned when page data is corrupted.
	ErrPageCorrupted = errors.New("page corrupted")

	// ErrStoreClosed is returned when operating on a closed store.
	ErrStoreClosed = errors.New("store closed")
)

// PageManager manages page persistence to disk.
//
// Design principles:
// - Simple file-based storage (.db file)
// - Fixed-size pages (4KB) for easy addressing
// - PageID = file offset / PageSize
// - No complex free page management (simplified for PoC)
type PageManager struct {
	// file is the underlying storage file.
	file *os.File

	// path is the file path.
	path string

	// nextPageID is the next page ID to allocate.
	nextPageID atomic.Uint64

	// closed indicates whether the manager is closed.
	closed atomic.Bool

	// mu protects file operations.
	mu sync.Mutex
}

// NewPageManager creates or opens a page store.
func NewPageManager(path string) (*PageManager, error) {
	// Open file (create if not exists)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	// Get file size to determine next page ID
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat file: %w", err)
	}

	fileSize := info.Size()
	nextPageID := uint64(fileSize / PageSize)

	pm := &PageManager{
		file: file,
		path: path,
	}
	pm.nextPageID.Store(nextPageID)

	return pm, nil
}

// WritePage writes a page to disk at the specified offset.
// The page must be marked dirty before calling this method.
func (pm *PageManager) WritePage(page *Page) error {
	if pm.closed.Load() {
		return ErrStoreClosed
	}

	if page == nil {
		return errors.New("WritePage: page is nil")
	}

	if !page.IsDirty() {
		// Page not modified, no need to write
		return nil
	}

	// Synchronous write (simplified for PoC)
	// TODO: Add async writeback for better performance
	return pm.syncWritePage(page)
}

// syncWritePage synchronously writes a page to disk.
func (pm *PageManager) syncWritePage(page *Page) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Calculate offset
	offset := int64(page.ID) * PageSize

	// Seek to offset
	_, err := pm.file.Seek(offset, io.SeekStart)
	if err != nil {
		return fmt.Errorf("seek to offset %d: %w", offset, err)
	}

	// Serialize page to buffer
	buf := make([]byte, PageSize)
	buf[0] = byte(page.Type)              // Type (1 byte)
	binary.LittleEndian.PutUint64(buf[1:9], page.Version) // Version (8 bytes)
	binary.LittleEndian.PutUint64(buf[9:17], uint64(page.ID)) // ID (8 bytes)
	copy(buf[17:], page.Data[:])          // Data (4075 bytes)

	// Write to file
	_, err = pm.file.Write(buf)
	if err != nil {
		return fmt.Errorf("write page %d: %w", page.ID, err)
	}

	// Sync to disk
	if err := pm.file.Sync(); err != nil {
		return fmt.Errorf("sync page %d: %w", page.ID, err)
	}

	// Clear dirty flag
	page.ClearDirty()

	return nil
}

// ReadPage reads a page from disk.
func (pm *PageManager) ReadPage(pageID model.PageID) (*Page, error) {
	if pm.closed.Load() {
		return nil, ErrStoreClosed
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Calculate offset
	offset := int64(pageID) * PageSize

	// Seek to offset
	_, err := pm.file.Seek(offset, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("seek to offset %d: %w", offset, err)
	}

	// Read page data
	buf := make([]byte, PageSize)
	n, err := pm.file.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read page %d: %w", pageID, err)
	}
	if n != PageSize {
		return nil, fmt.Errorf("incomplete read: %d < %d", n, PageSize)
	}

	// Deserialize page
	pageType := model.PageType(buf[0])
	version := binary.LittleEndian.Uint64(buf[1:9])
	id := model.PageID(binary.LittleEndian.Uint64(buf[9:17]))

	page := NewPage(id, pageType)
	page.SetVersion(version)
	copy(page.Data[:], buf[17:])

	return page, nil
}

// AllocatePage allocates a new page ID.
func (pm *PageManager) AllocatePage() model.PageID {
	pageID := model.PageID(pm.nextPageID.Load())
	pm.nextPageID.Add(1)
	return pageID
}

// Flush flushes all dirty pages to disk.
func (pm *PageManager) Flush() error {
	if pm.closed.Load() {
		return ErrStoreClosed
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Sync file
	if err := pm.file.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}

	return nil
}

// Close closes the page manager.
func (pm *PageManager) Close() error {
	if pm.closed.Load() {
		return nil // Already closed
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Sync file
	if err := pm.file.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}

	// Close file
	if err := pm.file.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	pm.closed.Store(true)

	return nil
}

// Stats returns statistics about the page manager.
func (pm *PageManager) Stats() PageManagerStats {
	return PageManagerStats{
		TotalPages:   pm.nextPageID.Load(),
		DatabaseSize: int64(pm.nextPageID.Load()) * PageSize,
	}
}

// PageManagerStats holds statistics about the page manager.
type PageManagerStats struct {
	TotalPages   uint64
	DatabaseSize int64
}
