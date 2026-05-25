// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"fmt"
	"encoding/binary"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
	"os"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/wal"
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
	id         uint32   // chunk identifier
	seq        uint64   // global monotonic sequence number (recovery sorting)
	file       *os.File // underlying file handle
	capacity   int64    // max capacity (256MB default)
	nextOffset int64    // next append position

	mu sync.Mutex // protects pagePosToLen, removedPages, and concurrent I/O

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
				return nil, errpkg.Wrap(err, fmt.Sprintf("chunk: open existing file %s", path))
			}
			return f, nil
		}
		return nil, errpkg.Wrap(err, fmt.Sprintf("chunk: create file %s", path))
	}
	// Pre-allocate space for crash consistency
	if err := preallocate(f, capacity); err != nil {
		f.Close()
		os.Remove(path)
		return nil, errpkg.Wrap(err, "chunk: preallocate")
	}
	return f, nil
}

// writeHeader writes the dual-block text header (2 × 4096 bytes).
// Called at chunk finalization, not at creation (Lealone semantics).
func (c *ChunkFile) writeHeader(h *ChunkHeader) error {
	text := h.encode()
	if len(text) > ChunkBlockSize {
		return errpkg.Wrap(ErrChunkHeaderError, fmt.Sprintf("chunk: header text too large: %d > %d", len(text), ChunkBlockSize))
	}
	// Pad to block size
	buf := make([]byte, ChunkBlockSize)
	copy(buf, text)

	// Write block 1 at offset 0
	if _, err := c.file.WriteAt(buf, 0); err != nil {
		return errpkg.Wrap(err, "chunk: write header block 1")
	}
	// Write block 2 at offset 4096 (crash-safe duplicate)
	if _, err := c.file.WriteAt(buf, ChunkBlockSize); err != nil {
		return errpkg.Wrap(err, "chunk: write header block 2")
	}
	return nil
}

// readHeader reads and validates the dual-block header.
// Tries block 0 first; falls back to block 1 if block 0 is corrupted.
func (c *ChunkFile) readHeader() (*ChunkHeader, error) {
	buf := make([]byte, ChunkHeaderSize)
	if _, err := c.file.ReadAt(buf, 0); err != nil {
		return nil, errpkg.Wrap(err, "chunk: read header")
	}

	// Try block 0 first
	h, err := decodeHeader(buf[:ChunkBlockSize])
	if err == nil {
		return h, nil
	}

	// Fall back to block 1 (crash-safe duplicate)
	h, err = decodeHeader(buf[ChunkBlockSize:])
	if err != nil {
		return nil, errpkg.Wrap(ErrInvalidChunkHeader, "chunk: both header blocks corrupted")
	}
	return h, nil
}

// scanPageFrames walks the chunk body at fixed stride (MaxDiskPageSize=4100)
// and rebuilds pagePosToLen by CRC32C + pageType verification.
// Assumes pages are serialized with MaxPagePayload (current Checkpoint behavior).
// Frame format: [CRC32C:4][pageData:4096].
func (c *ChunkFile) scanPageFrames() map[model.ChunkPosition]int32 {
	result := make(map[model.ChunkPosition]int32)
	buf := make([]byte, MaxDiskPageSize)

	const consecutiveMissLimit = 16
	consecutiveMiss := 0

	for offset := int64(ChunkHeaderSize); offset+MaxDiskPageSize <= c.capacity; offset += MaxDiskPageSize {
		_, err := c.file.ReadAt(buf, offset)
		if err != nil {
			break
		}

		expected := binary.LittleEndian.Uint32(buf[0:CRCSize])
		actual := wal.CRC32C(buf[CRCSize:])
		pageType := buf[CRCSize+offheap.PageTypeFieldOffset]

		if expected != actual || (pageType != offheap.PageTypeIndex && pageType != offheap.PageTypeLeaf) {
			consecutiveMiss++
			if consecutiveMiss > consecutiveMissLimit {
				break
			}
			continue
		}

		consecutiveMiss = 0
		pos, err := model.EncodeChunkPosition(c.id, uint32(offset), pageType)
		if err != nil {
			continue
		}
		result[pos] = MaxDiskPageSize
	}
	return result
}
