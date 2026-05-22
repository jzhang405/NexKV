// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteHeader_NormalHeaderFits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ao")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	c := &ChunkFile{file: f}
	h := &ChunkHeader{ID: 1}
	require.NoError(t, c.writeHeader(h))
}

func TestReadHeader_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btree_0_1.ao")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	c := &ChunkFile{file: f, id: 0}
	h := &ChunkHeader{
		ID:            0,
		PageCount:     10,
		BlockSize:     ChunkBlockSize,
		FormatVersion: 1,
	}
	require.NoError(t, c.writeHeader(h))

	decoded, err := c.readHeader()
	require.NoError(t, err)
	assert.Equal(t, h.ID, decoded.ID)
	assert.Equal(t, h.PageCount, decoded.PageCount)
}

func TestReadHeader_FallbackToBlock1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btree_1_2.ao")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	c := &ChunkFile{file: f, id: 1}
	h := &ChunkHeader{
		ID:            1,
		PageCount:     5,
		BlockSize:     ChunkBlockSize,
		FormatVersion: 1,
	}
	// Write header (writes both blocks)
	require.NoError(t, c.writeHeader(h))

	// Corrupt block 0 (offset 0)
	buf := make([]byte, ChunkBlockSize)
	for i := range buf {
		buf[i] = 0xFF
	}
	_, err = c.file.WriteAt(buf, 0)
	require.NoError(t, err)

	// Should fall back to block 1 (offset 4096) and succeed
	decoded, err := c.readHeader()
	require.NoError(t, err)
	assert.Equal(t, h.ID, decoded.ID)
}

func TestOpenChunkFile_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btree_5_1.ao")

	// Pre-create the file
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	f2, err := openChunkFile(path, 1024*1024)
	require.NoError(t, err)
	require.NotNil(t, f2)

	stat, err := f2.Stat()
	require.NoError(t, err)
	assert.Equal(t, int64(0), stat.Size()) // existing file not preallocated
	f2.Close()
}

func TestWriteHeader_ErrorOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btree_readonly.ao")
	f, err := os.Create(path)
	require.NoError(t, err)
	// Close the file so writes fail
	require.NoError(t, f.Close())

	c := &ChunkFile{file: f}
	h := &ChunkHeader{ID: 1, BlockSize: ChunkBlockSize, FormatVersion: 1}
	err = c.writeHeader(h)
	require.Error(t, err)
}

func TestScanPageFrames_AllValidFrames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btree_0_1.ao")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	defer f.Close()

	// Preallocate to simulate real chunk
	require.NoError(t, f.Truncate(ChunkHeaderSize+3*MaxDiskPageSize))

	c := &ChunkFile{file: f, id: 0, capacity: ChunkHeaderSize + 3*MaxDiskPageSize}

	// Write header
	h := &ChunkHeader{ID: 0, PageCount: 3, BlockSize: ChunkBlockSize, FormatVersion: 1}
	require.NoError(t, c.writeHeader(h))

	// Create 3 valid frames via PageSerializer
	serializer := &PageSerializer{}
	pageBuf := make([]byte, MaxPagePayload)
	// Set pageType = PageTypeLeaf (offset 26 in PageHeader)
	pageBuf[offheap.PageTypeFieldOffset] = offheap.PageTypeLeaf

	for i := 0; i < 3; i++ {
		data, serErr := serializer.Serialize(unsafe.Pointer(&pageBuf[0]), MaxPagePayload)
		require.NoError(t, serErr)
		assert.Equal(t, MaxDiskPageSize, len(data))
		_, err = f.WriteAt(data, ChunkHeaderSize+int64(i)*MaxDiskPageSize)
		require.NoError(t, err)
	}

	// Scan
	result := c.scanPageFrames()
	assert.Equal(t, 3, len(result))
}

func TestScanPageFrames_CorruptedFrameSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btree_0_1.ao")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	defer f.Close()

	require.NoError(t, f.Truncate(ChunkHeaderSize+5*MaxDiskPageSize))
	c := &ChunkFile{file: f, id: 0, capacity: ChunkHeaderSize + 5*MaxDiskPageSize}

	h := &ChunkHeader{ID: 0, PageCount: 5, BlockSize: ChunkBlockSize, FormatVersion: 1}
	require.NoError(t, c.writeHeader(h))

	serializer := &PageSerializer{}
	pageBuf := make([]byte, MaxPagePayload)
	pageBuf[offheap.PageTypeFieldOffset] = offheap.PageTypeLeaf

	// Write valid frames at positions 0, 2, 4 (skip 1 and 3 are corrupted)
	for _, i := range []int{0, 2, 4} {
		data, _ := serializer.Serialize(unsafe.Pointer(&pageBuf[0]), MaxPagePayload)
		f.WriteAt(data, ChunkHeaderSize+int64(i)*MaxDiskPageSize)
	}

	// Write garbage at positions 1 and 3
	garbage := make([]byte, MaxDiskPageSize)
	for i := range garbage {
		garbage[i] = 0xFF
	}
	f.WriteAt(garbage, ChunkHeaderSize+1*MaxDiskPageSize)
	f.WriteAt(garbage, ChunkHeaderSize+3*MaxDiskPageSize)

	// Scan should find 3 valid frames and skip the corrupted ones
	result := c.scanPageFrames()
	assert.Equal(t, 3, len(result))
}

func TestScanPageFrames_EarlyExitOnZeroFill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btree_0_1.ao")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	defer f.Close()

	// Large capacity but only 2 valid frames
	require.NoError(t, f.Truncate(ChunkHeaderSize+100*MaxDiskPageSize))
	c := &ChunkFile{file: f, id: 0, capacity: ChunkHeaderSize + 100*MaxDiskPageSize}

	h := &ChunkHeader{ID: 0, BlockSize: ChunkBlockSize, FormatVersion: 1}
	require.NoError(t, c.writeHeader(h))

	serializer := &PageSerializer{}
	pageBuf := make([]byte, MaxPagePayload)
	pageBuf[offheap.PageTypeFieldOffset] = offheap.PageTypeLeaf

	// Write only 2 frames at the start
	for i := 0; i < 2; i++ {
		data, _ := serializer.Serialize(unsafe.Pointer(&pageBuf[0]), MaxPagePayload)
		f.WriteAt(data, ChunkHeaderSize+int64(i)*MaxDiskPageSize)
	}

	// Scan should exit early after 16 consecutive misses in zero-fill
	result := c.scanPageFrames()
	assert.Equal(t, 2, len(result))
}

func TestScanPageFrames_EmptyChunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btree_0_1.ao")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	defer f.Close()

	require.NoError(t, f.Truncate(ChunkHeaderSize+10*MaxDiskPageSize))
	c := &ChunkFile{file: f, id: 0, capacity: ChunkHeaderSize + 10*MaxDiskPageSize}

	h := &ChunkHeader{ID: 0, BlockSize: ChunkBlockSize, FormatVersion: 1}
	require.NoError(t, c.writeHeader(h))

	// No frames written — only zero-fill
	result := c.scanPageFrames()
	assert.Equal(t, 0, len(result))
}
