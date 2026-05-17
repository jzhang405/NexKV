// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package model

import "fmt"

// ChunkPosition is a 64-bit encoded page location in an .ao file.
//
// Bit layout (reference Lealone PageUtils.getPagePos, adjusted):
//
//	[63:38] ChunkID     (26 bits, max 67M chunks)
//	[37:6]  FileOffset  (32 bits, max 4GB per chunk)
//	[5:1]   PageType    (5 bits, 32 page types)
//	[0]     Reserved    (1 bit)

// Zero value (ChunkPosition(0)) means the page has NOT been persisted to disk
// (dirty page), aligning with Lealone's pos==0 dirty-page semantic.
type ChunkPosition uint64

const (
	// MaxChunkID is the maximum chunk identifier (2^26 - 1 = 67,108,863).
	MaxChunkID = (1 << 26) - 1

	// chunkIDShift is the bit position of the ChunkID field.
	chunkIDShift = 38
	// fileOffsetShift is the bit position of the FileOffset field.
	fileOffsetShift = 6
	// pageTypeShift is the bit position of the PageType field.
	pageTypeShift = 1

	chunkIDMask    = (1 << 26) - 1
	fileOffsetMask = (1 << 32) - 1
	pageTypeMask   = (1 << 5) - 1
)

// EncodeChunkPosition packs chunkID, offset, and pageType into a ChunkPosition.
// Returns an error if chunkID exceeds MaxChunkID.
func EncodeChunkPosition(chunkID uint32, offset uint32, pageType uint8) (ChunkPosition, error) {
	if chunkID > MaxChunkID {
		return 0, fmt.Errorf("chunkID %d exceeds MaxChunkID %d", chunkID, MaxChunkID)
	}
	pos := (uint64(chunkID) << chunkIDShift) |
		(uint64(offset) << fileOffsetShift) |
		(uint64(pageType&pageTypeMask) << pageTypeShift)
	return ChunkPosition(pos), nil
}

// ChunkID extracts the chunk identifier.
func (p ChunkPosition) ChunkID() uint32 {
	return uint32((uint64(p) >> chunkIDShift) & chunkIDMask)
}

// FileOffset extracts the byte offset within the chunk file (relative to ChunkHeaderSize).
func (p ChunkPosition) FileOffset() uint32 {
	return uint32((uint64(p) >> fileOffsetShift) & fileOffsetMask)
}

// PageType extracts the page type indicator.
func (p ChunkPosition) PageType() uint8 {
	return uint8((uint64(p) >> pageTypeShift) & pageTypeMask)
}

// IsZero reports whether p is the zero value (page not persisted).
func (p ChunkPosition) IsZero() bool {
	return p == 0
}

// String returns a hex representation of the ChunkPosition.
func (p ChunkPosition) String() string {
	return fmt.Sprintf("ChunkPosition{chunk=%d, offset=%d, type=%d}",
		p.ChunkID(), p.FileOffset(), p.PageType())
}
