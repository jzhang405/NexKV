// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"os"
	"path/filepath"
	"testing"

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
