// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"path/filepath"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPageManager_New tests creating a new page manager.
func TestPageManager_New(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	pm, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm.Close()

	assert.NotNil(t, pm)
	assert.Equal(t, path, pm.path)
	assert.False(t, pm.closed.Load())
}

// TestPageManager_Allocate tests page allocation.
func TestPageManager_Allocate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	pm, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm.Close()

	// Allocate first page
	pageID1 := pm.AllocatePage()
	assert.Equal(t, model.PageID(0), pageID1)

	// Allocate second page
	pageID2 := pm.AllocatePage()
	assert.Equal(t, model.PageID(1), pageID2)

	// Allocate third page
	pageID3 := pm.AllocatePage()
	assert.Equal(t, model.PageID(2), pageID3)
}

// TestPageManager_WriteAndRead tests writing and reading pages.
func TestPageManager_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	pm, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm.Close()

	// Create and write a page
	pageID := pm.AllocatePage()
	page := NewPage(pageID, model.LeafPage)

	// Write some data
	testData := []byte("Hello, World!")
	copy(page.Data[:], testData)

	// Mark as dirty and write
	page.MarkDirty()
	err = pm.WritePage(page)
	require.NoError(t, err)

	// Read back
	readPage, err := pm.ReadPage(pageID)
	require.NoError(t, err)

	assert.Equal(t, page.ID, readPage.ID)
	assert.Equal(t, page.Type, readPage.Type)
	assert.Equal(t, testData, readPage.Data[:len(testData)])
	assert.False(t, readPage.IsDirty())
}

// TestPageManager_ReadNonExistent tests reading a non-existent page.
func TestPageManager_ReadNonExistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	pm, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm.Close()

	// Try to read a page that doesn't exist
	_, err = pm.ReadPage(999)
	assert.Error(t, err)
}

// TestPageManager_Reopen tests reopening an existing database.
func TestPageManager_Reopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// Create and write data
	pm1, err := NewPageManager(path)
	require.NoError(t, err)

	pageID := pm1.AllocatePage()
	page := NewPage(pageID, model.LeafPage)
	testData := []byte("Persistent Data")
	copy(page.Data[:], testData)
	page.MarkDirty()

	err = pm1.WritePage(page)
	require.NoError(t, err)
	err = pm1.Close()
	require.NoError(t, err)

	// Reopen and verify
	pm2, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm2.Close()

	// Should skip page 0 (already allocated)
	newPageID := pm2.AllocatePage()
	assert.Equal(t, model.PageID(1), newPageID)

	// Read back the original page
	readPage, err := pm2.ReadPage(pageID)
	require.NoError(t, err)
	assert.Equal(t, testData, readPage.Data[:len(testData)])
}

// TestPageManager_MultiplePages tests writing and reading multiple pages.
func TestPageManager_MultiplePages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	pm, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm.Close()

	// Write multiple pages
	numPages := 10
	pages := make([]model.PageID, numPages)

	for i := 0; i < numPages; i++ {
		pageID := pm.AllocatePage()
		pages[i] = pageID

		page := NewPage(pageID, model.LeafPage)
		testData := []byte{byte(i)}
		copy(page.Data[:], testData)
		page.MarkDirty()

		err = pm.WritePage(page)
		require.NoError(t, err)
	}

	// Read back and verify
	for i, pageID := range pages {
		readPage, err := pm.ReadPage(pageID)
		require.NoError(t, err)

		expectedData := []byte{byte(i)}
		assert.Equal(t, expectedData, readPage.Data[:1])
	}
}

// TestPageManager_Flush tests flushing dirty pages.
func TestPageManager_Flush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	pm, err := NewPageManager(path)
	require.NoError(t, err)

	// Write a page
	pageID := pm.AllocatePage()
	page := NewPage(pageID, model.LeafPage)
	testData := []byte("Flush Test")
	copy(page.Data[:], testData)
	page.MarkDirty()

	err = pm.WritePage(page)
	require.NoError(t, err)

	// Flush
	err = pm.Flush()
	require.NoError(t, err)

	// Close and reopen to verify persistence
	err = pm.Close()
	require.NoError(t, err)

	pm2, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm2.Close()

	readPage, err := pm2.ReadPage(pageID)
	require.NoError(t, err)
	assert.Equal(t, testData, readPage.Data[:len(testData)])
}

// TestPageManager_Stats tests statistics reporting.
func TestPageManager_Stats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	pm, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm.Close()

	// Allocate some pages
	_ = pm.AllocatePage()
	_ = pm.AllocatePage()
	_ = pm.AllocatePage()

	stats := pm.Stats()
	assert.Equal(t, uint64(3), stats.TotalPages)
	assert.Equal(t, int64(3*PageSize), stats.DatabaseSize)
}

// TestPageManager_ConcurrentAccess tests concurrent page access.
func TestPageManager_ConcurrentAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent test in short mode")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	pm, err := NewPageManager(path)
	require.NoError(t, err)
	defer pm.Close()

	const numGoroutines = 10
	const pagesPerGoroutine = 100

	done := make(chan bool, numGoroutines)

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			for j := 0; j < pagesPerGoroutine; j++ {
				pageID := pm.AllocatePage()
				page := NewPage(pageID, model.LeafPage)

				// Write unique data
				data := []byte{byte(id), byte(j)}
				copy(page.Data[:2], data)
				page.MarkDirty()

				err := pm.WritePage(page)
				if err != nil {
					t.Errorf("Write failed: %v", err)
					return
				}
			}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify all pages were allocated
	stats := pm.Stats()
	expectedPages := uint64(numGoroutines * pagesPerGoroutine)
	assert.Equal(t, expectedPages, stats.TotalPages)
}

// BenchmarkPageManager_Write benchmarks page write performance.
func BenchmarkPageManager_Write(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.db")

	pm, err := NewPageManager(path)
	require.NoError(b, err)
	defer pm.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pageID := pm.AllocatePage()
		page := NewPage(pageID, model.LeafPage)
		page.MarkDirty()
		_ = pm.WritePage(page)
	}
}

// BenchmarkPageManager_Read benchmarks page read performance.
func BenchmarkPageManager_Read(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.db")

	pm, err := NewPageManager(path)
	require.NoError(b, err)
	defer pm.Close()

	// Pre-allocate pages
	numPages := 1000
	for i := 0; i < numPages; i++ {
		pageID := pm.AllocatePage()
		page := NewPage(pageID, model.LeafPage)
		page.MarkDirty()
		_ = pm.WritePage(page)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pageID := model.PageID(i % numPages)
		_, _ = pm.ReadPage(pageID)
	}
}
