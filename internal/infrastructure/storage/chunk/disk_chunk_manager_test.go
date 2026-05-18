// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

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

func TestDiskChunkManager_Allocate_InvalidPageType(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	_, err := cm.Allocate(100, 99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid pageType")
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

	data1 := []byte("page one data here with enough padding to exceed MinPagePayload 56 bytes total")
	data2 := []byte("page two data here with enough padding to exceed MinPagePayload 56 bytes total")
	data3 := []byte("page three here with enough padding to exceed MinPagePayload 56 bytes total")

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

func TestDiskChunkManager_ReadPage_NotFound(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, err := cm.Allocate(100, 1)
	require.NoError(t, err)
	// Do NOT write — just try to read
	_, err = cm.ReadPage(pos)
	assert.ErrorIs(t, err, ErrPageNotFound)
}

func TestDiskChunkManager_ReadPage_ChunkNotFound(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, err := model.EncodeChunkPosition(9999, 8192, 1)
	require.NoError(t, err)
	_, err = cm.ReadPage(pos)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk 9999 not found")
}

func TestDiskChunkManager_WritePage_ChunkNotFound(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, err := model.EncodeChunkPosition(9999, 8192, 1)
	require.NoError(t, err)
	err = cm.WritePage(pos, []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk 9999 not found")
}

func TestDiskChunkManager_FreePage_ChunkNotFound(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, err := model.EncodeChunkPosition(9999, 8192, 1)
	require.NoError(t, err)
	err = cm.FreePage(pos)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk 9999 not found")
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

func TestDiskChunkManager_NewChunkOnFull(t *testing.T) {
	dir := t.TempDir()
	cm, err := NewDiskChunkManager(dir, 10000) // 10KB chunk
	require.NoError(t, err)
	defer cm.Close()

	pos1, err := cm.Allocate(100, 1)
	require.NoError(t, err)
	require.NoError(t, cm.WritePage(pos1, []byte("page1data")))

	pos2, err := cm.Allocate(4000, 0)
	require.NoError(t, err)
	require.NoError(t, cm.WritePage(pos2, []byte("page2data")))

	stats := cm.Stats()
	assert.True(t, stats.ActiveChunks >= 1)
	assert.Equal(t, int64(2), stats.TotalPages)
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

func TestDiskChunkManager_Stats_ReadOps(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, _ := cm.Allocate(100, 1)
	cm.WritePage(pos, []byte("read ops test"))

	cm.ReadPage(pos)
	stats := cm.Stats()
	assert.Equal(t, int64(1), stats.ReadOps)
}

func TestDiskChunkManager_WritePage_WriteOpsCounted(t *testing.T) {
	cm := setupTestChunkManager(t)
	defer cm.Close()

	pos, _ := cm.Allocate(100, 1)
	stats := cm.Stats()
	assert.Equal(t, int64(0), stats.WriteOps) // Allocate does not count

	cm.WritePage(pos, []byte("data"))
	stats = cm.Stats()
	assert.Equal(t, int64(1), stats.WriteOps) // WritePage counts
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

func TestDiskChunkManager_Close_ClosedOperations(t *testing.T) {
	cm := setupTestChunkManager(t)
	pos, _ := cm.Allocate(100, 1)
	cm.WritePage(pos, []byte("test"))

	require.NoError(t, cm.Close())

	_, err := cm.Allocate(100, 1)
	assert.ErrorIs(t, err, ErrChunkClosed)
	_, err = cm.ReadPage(pos)
	assert.ErrorIs(t, err, ErrChunkClosed)
	err = cm.WritePage(pos, []byte("x"))
	assert.ErrorIs(t, err, ErrChunkClosed)
	err = cm.FreePage(pos)
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

func TestChunkIDBitSet_Clear(t *testing.T) {
	b := newChunkIDBitSet()
	b.set(5)
	b.set(10)

	b.clear(5)
	b.clear(10)

	id, err := b.nextClearBit(0)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), id)

	b.set(5)
	id, err = b.nextClearBit(0)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), id)
}

func TestChunkIDBitSet_ClearOutOfRange(t *testing.T) {
	b := newChunkIDBitSet()
	// Clearing an out-of-range bit should not panic
	b.clear(99999999)
}

func TestChunkIDBitSet_SetExtendWords(t *testing.T) {
	b := newChunkIDBitSet()
	b.set(128) // word 2 — triggers extension
	id, err := b.nextClearBit(0)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), id)
}

func TestChunkIDBitSet_NextClearBitExtend(t *testing.T) {
	b := newChunkIDBitSet()
	for i := uint32(0); i < 128; i++ {
		b.set(i)
	}
	id, err := b.nextClearBit(0)
	require.NoError(t, err)
	assert.Equal(t, uint32(128), id)
}

func TestChunkIDBitSet_NextClearBit_StartOffset(t *testing.T) {
	b := newChunkIDBitSet()
	b.set(0)
	b.set(1)
	b.set(2)
	id, err := b.nextClearBit(1)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), id)
}
