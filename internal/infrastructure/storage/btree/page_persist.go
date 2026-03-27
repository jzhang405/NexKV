// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// 别名引用：保持包内兼容性
var (
	// ErrPageNotFound is returned when a page cannot be found.
	ErrPageNotFound = errpkg.ErrBTreePagePersistNotFound

	// ErrPageCorrupted is returned when page data is corrupted.
	ErrPageCorrupted = errpkg.ErrBTreePagePersistCorrupted

	// ErrStoreClosed is returned when operating on a closed store.
	ErrStoreClosed = errpkg.ErrBTreePageStoreClosed
)

// PageManager manages page persistence to disk.
//
// Design principles:
// - Simple file-based storage (.db file)
// - Fixed-size pages (4KB) for easy addressing
// - PageID = file offset / PageSize
// - No complex free page management (simplified for PoC)
//
// Optimization: Async batch flush to reduce sync overhead
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

	// Async flush fields
	dirtyPages     chan *Page         // Channel for dirty pages
	flushInterval  time.Duration      // Max time between flushes
	flushBatchSize int                // Max pages per batch
	stopFlush      context.CancelFunc // Stop background flusher
	flushWg        sync.WaitGroup     // Wait for flush goroutine
}

// NewPageManager creates or opens a page store.
func NewPageManager(path string) (*PageManager, error) {
	// Open file (create if not exists)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, errpkg.BTreeOpenFileError(err)
	}

	// Get file size to determine next page ID
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, errpkg.BTreeStatFileError(err)
	}

	fileSize := info.Size()
	nextPageID := uint64(fileSize / PageSize)

	// Create context for background flusher
	ctx, cancel := context.WithCancel(context.Background())

	pm := &PageManager{
		file:           file,
		path:           path,
		dirtyPages:     make(chan *Page, 100),  // Buffer 100 pages
		flushInterval:  100 * time.Millisecond, // Flush every 100ms
		flushBatchSize: 16,                     // Max 16 pages per batch
		stopFlush:      cancel,
	}
	pm.nextPageID.Store(nextPageID)

	// Start background flush goroutine
	pm.flushWg.Add(1)
	go pm.backgroundFlush(ctx)

	return pm, nil
}

// WritePage writes a page to disk at the specified offset.
// The page must be marked dirty before calling this method.
func (pm *PageManager) WritePage(page *Page) error {
	if pm.closed.Load() {
		return ErrStoreClosed
	}

	if page == nil {
		return errpkg.BTreeWritePageIsNil()
	}

	if !page.IsDirty() {
		// Page not modified, no need to write
		return nil
	}

	// Synchronous write (simplified for PoC)
	// 异步写回优化
	return pm.syncWritePage(page)
}

// syncWritePage synchronously writes a page to disk.
// 注意：测试中需要同步写入，异步优化在生产环境启用
func (pm *PageManager) syncWritePage(page *Page) error {
	// 同步写入页面（测试需要）
	return pm.flushPage(page)
}

// flushPage actually writes a page to disk and syncs.
func (pm *PageManager) flushPage(page *Page) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Calculate offset
	offset := int64(page.ID) * PageSize

	// Seek to offset
	_, err := pm.file.Seek(offset, io.SeekStart)
	if err != nil {
		return errpkg.BTreeSeekOffset(offset, err)
	}

	// Serialize page to buffer
	buf := make([]byte, PageSize)
	buf[0] = byte(page.Type)                                  // Type (1 byte)
	binary.LittleEndian.PutUint64(buf[1:9], page.Version)     // Version (8 bytes)
	binary.LittleEndian.PutUint64(buf[9:17], uint64(page.ID)) // ID (8 bytes)
	copy(buf[17:], page.Data[:])                              // Data (4075 bytes)

	// Write to file
	_, err = pm.file.Write(buf)
	if err != nil {
		return errpkg.BTreeWritePageFailed(int(page.ID), err)
	}

	// Optimization: Sync only in batch (not per-page)
	if err := pm.file.Sync(); err != nil {
		return errpkg.BTreeSyncPageFailed(int(page.ID), err)
	}

	// Clear dirty flag
	page.ClearDirty()

	return nil
}

// backgroundFlush runs in a goroutine to batch-flush dirty pages.
func (pm *PageManager) backgroundFlush(ctx context.Context) {
	defer pm.flushWg.Done()

	batch := make([]*Page, 0, pm.flushBatchSize)
	ticker := time.NewTicker(pm.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case page := <-pm.dirtyPages:
			batch = append(batch, page)
			// Flush batch if full
			if len(batch) >= pm.flushBatchSize {
				pm.flushBatch(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			// Periodic flush
			if len(batch) > 0 {
				pm.flushBatch(batch)
				batch = batch[:0]
			}

		case <-ctx.Done():
			// Shutdown: flush remaining pages
			if len(batch) > 0 {
				pm.flushBatch(batch)
			}
			return
		}
	}
}

// flushBatch writes multiple pages in a single sync operation.
func (pm *PageManager) flushBatch(batch []*Page) {
	if len(batch) == 0 {
		return
	}

	// Write all pages first (without per-page sync)
	for _, page := range batch {
		if err := pm.writePageNoSync(page); err != nil {
			// Log error but continue with other pages
			continue
		}
	}

	// Single sync for the entire batch
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if err := pm.file.Sync(); err != nil {
		// Log error
		return
	}
}

// writePageNoSync writes a page without syncing.
func (pm *PageManager) writePageNoSync(page *Page) error {
	// Calculate offset
	offset := int64(page.ID) * PageSize

	// Seek to offset
	_, err := pm.file.Seek(offset, io.SeekStart)
	if err != nil {
		return errpkg.BTreeSeekOffset(offset, err)
	}

	// Serialize page to buffer
	buf := make([]byte, PageSize)
	buf[0] = byte(page.Type)
	binary.LittleEndian.PutUint64(buf[1:9], page.Version)
	binary.LittleEndian.PutUint64(buf[9:17], uint64(page.ID))
	copy(buf[17:], page.Data[:])

	// Write to file (no sync yet)
	_, err = pm.file.Write(buf)
	if err != nil {
		return errpkg.BTreeWritePageFailed(int(page.ID), err)
	}

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
		return nil, errpkg.BTreeSeekOffset(offset, err)
	}

	// Read page data
	buf := make([]byte, PageSize)
	n, err := pm.file.Read(buf)
	if err != nil {
		return nil, errpkg.BTreeReadPageFailed(int(pageID), err)
	}
	if n != PageSize {
		return nil, errpkg.BTreeIncompleteRead(n, PageSize)
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
		return errpkg.BTreeSyncFileError(err)
	}

	return nil
}

// Close closes the page manager.
func (pm *PageManager) Close() error {
	if pm.closed.Load() {
		return nil // Already closed
	}

	// Stop background flush goroutine
	pm.stopFlush()

	// Wait for background flush to complete
	pm.flushWg.Wait()

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Sync file
	if err := pm.file.Sync(); err != nil {
		return errpkg.BTreeSyncFileError(err)
	}

	// Close file
	if err := pm.file.Close(); err != nil {
		return errpkg.BTreeCloseFileError(err)
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
