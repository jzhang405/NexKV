// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"fmt"
	"os"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

const (
	// ChunkBlockSize is the physical block size for alignment.
	ChunkBlockSize = 4096

	// ChunkHeaderBlocks is the number of duplicate header blocks for crash safety.
	ChunkHeaderBlocks = 2

	// ChunkHeaderSize is the total header size (2 × 4096 bytes).
	ChunkHeaderSize = ChunkBlockSize * ChunkHeaderBlocks
)

// ChunkFile represents a single .ao chunk file.
// Aligns with Lealone Chunk.java.
type ChunkFile struct {
	id       uint32          // chunk identifier
	seq      uint64          // global monotonic sequence number
	file     *os.File        // underlying file handle
	path     string          // btree_[id]_[seq].ao
	capacity int64           // max capacity (256MB default)
	nextOffset int64         // next append position

	// page metadata (Lealone pagePositionToLengthMap)
	pagePosToLen map[model.ChunkPosition]int32 // pos → pageLength

	// removed pages set (Lealone removedPages — ConcurrentSkipListSet<Long> equivalent)
	removedPages map[model.ChunkPosition]struct{}
}

// openChunkFile creates or opens a chunk file at the given path.
// Uses O_EXCL for creation to prevent symlink attacks.
func openChunkFile(path string, capacity int64) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			// Open existing chunk file
			f, err = os.OpenFile(path, os.O_RDWR, 0600)
			if err != nil {
				return nil, fmt.Errorf("chunk: open existing file %s: %w", path, err)
			}
			return f, nil
		}
		return nil, fmt.Errorf("chunk: create file %s: %w", path, err)
	}
	// Pre-allocate space for crash consistency
	if err := preallocate(f, capacity); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("chunk: preallocate: %w", err)
	}
	return f, nil
}

// writeHeader writes the dual-block text header (2 × 4096 bytes).
// Called at chunk finalization, not at creation (Lealone semantics).
func (c *ChunkFile) writeHeader(h *ChunkHeader) error {
	text := h.encode()
	if len(text) > ChunkBlockSize {
		return fmt.Errorf("chunk: header text too large: %d > %d", len(text), ChunkBlockSize)
	}
	// Pad to block size
	buf := make([]byte, ChunkBlockSize)
	copy(buf, text)

	// Write block 1 at offset 0
	if _, err := c.file.WriteAt(buf, 0); err != nil {
		return fmt.Errorf("chunk: write header block 1: %w", err)
	}
	// Write block 2 at offset 4096 (crash-safe duplicate)
	if _, err := c.file.WriteAt(buf, ChunkBlockSize); err != nil {
		return fmt.Errorf("chunk: write header block 2: %w", err)
	}
	return nil
}

// readHeader reads and validates the dual-block header.
// Tries block 0 first; falls back to block 1 if block 0 is corrupted.
func (c *ChunkFile) readHeader() (*ChunkHeader, error) {
	buf := make([]byte, ChunkHeaderSize)
	if _, err := c.file.ReadAt(buf, 0); err != nil {
		return nil, fmt.Errorf("chunk: read header: %w", err)
	}

	// Try block 0 first
	h, err := decodeHeader(buf[:ChunkBlockSize])
	if err == nil {
		return h, nil
	}

	// Fall back to block 1 (crash-safe duplicate)
	h, err = decodeHeader(buf[ChunkBlockSize:])
	if err != nil {
		return nil, fmt.Errorf("chunk: both header blocks corrupted: %w", ErrInvalidChunkHeader)
	}
	return h, nil
}
