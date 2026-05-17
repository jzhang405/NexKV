// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestChunkManager(t *testing.T) *DiskChunkManager {
	t.Helper()
	dir := t.TempDir()
	cm, err := NewDiskChunkManager(dir, 1*1024*1024) // 1MB chunks for testing
	require.NoError(t, err)
	require.NotNil(t, cm)
	return cm
}

func TestDiskChunkManager_Allocate_NewChunk(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, err := cm.Allocate(4096, 0)
	require.NoError(t, err)
	require.NotZero(t, pos)
	assert.False(t, pos.IsZero())
	assert.Equal(t, uint32(0), pos.ChunkID()) // first chunk is id 0
}

func TestDiskChunkManager_WritePage_ReadPage(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, err := cm.Allocate(100, 1)
	require.NoError(t, err)

	testData := []byte("hello chunk persistence test data")
	err = cm.WritePage(pos, testData)
	require.NoError(t, err)

	readData, err := cm.ReadPage(pos)
	require.NoError(t, err)
	assert.Equal(t, testData, readData)
}

func TestDiskChunkManager_WritePage_ReadPage_MultiplePages(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	data1 := []byte("page one data")
	data2 := []byte("page two data here")
	data3 := []byte("page three")

	pos1, err := cm.Allocate(len(data1), 1)
	require.NoError(t, err)
	pos2, err := cm.Allocate(len(data2), 1)
	require.NoError(t, err)
	pos3, err := cm.Allocate(len(data3), 0)
	require.NoError(t, err)

	require.NoError(t, cm.WritePage(pos1, data1))
	require.NoError(t, cm.WritePage(pos2, data2))
	require.NoError(t, cm.WritePage(pos3, data3))

	// Read back
	r1, _ := cm.ReadPage(pos1)
	r2, _ := cm.ReadPage(pos2)
	r3, _ := cm.ReadPage(pos3)

	assert.Equal(t, data1, r1)
	assert.Equal(t, data2, r2)
	assert.Equal(t, data3, r3)
}

func TestDiskChunkManager_FreePage(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, err := cm.Allocate(100, 1)
	require.NoError(t, err)

	require.NoError(t, cm.WritePage(pos, []byte("test")))
	require.NoError(t, cm.FreePage(pos))

	// After FreePage, the position is in removedPages
	stats := cm.Stats()
	assert.Equal(t, int64(1), stats.FreePages)
}

func TestDiskChunkManager_Allocate_MultipleChunks(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	// Allocate many pages to trigger chunk creation
	pageCount := 100
	for i := 0; i < pageCount; i++ {
		pos, err := cm.Allocate(4096, 1)
		require.NoError(t, err)
		require.NoError(t, cm.WritePage(pos, []byte("data")))
	}

	stats := cm.Stats()
	assert.True(t, stats.ActiveChunks >= 1)
	assert.Equal(t, int64(pageCount), stats.TotalPages)
}

func TestDiskChunkManager_Stats(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos1, _ := cm.Allocate(100, 1)
	pos2, _ := cm.Allocate(200, 0)
	cm.WritePage(pos1, []byte("a"))
	cm.WritePage(pos2, []byte("b"))
	cm.FreePage(pos1)

	stats := cm.Stats()
	assert.Equal(t, int64(2), stats.TotalPages)
	assert.Equal(t, int64(1), stats.FreePages)
	assert.Equal(t, int64(2), stats.WriteOps)
}

func TestDiskChunkManager_Closed(t *testing.T) {
	cm := setupTestChunkManager(t)
	require.NoError(t, cm.Close())

	_, err := cm.Allocate(100, 1)
	assert.ErrorIs(t, err, ErrChunkClosed)

	_, err = cm.ReadPage(0)
	assert.ErrorIs(t, err, ErrChunkClosed)

	err = cm.WritePage(0, []byte("x"))
	assert.ErrorIs(t, err, ErrChunkClosed)
}

func TestDiskChunkManager_CloseIdempotent(t *testing.T) {
	cm := setupTestChunkManager(t)
	require.NoError(t, cm.Close())
	require.NoError(t, cm.Close()) // second close should be safe
}

func TestDiskChunkManager_Sync(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, _ := cm.Allocate(100, 1)
	cm.WritePage(pos, []byte("sync test"))
	require.NoError(t, cm.Sync())
}
