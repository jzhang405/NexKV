// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/wal"
)

const (
	// CRCSize is the size of the CRC32C prefix in the disk page format.
	CRCSize = 4

	// MinPagePayload is the minimum valid page payload size (empty page = header only).
	MinPagePayload = offheap.SizeofPageHeader // 56 bytes

	// MaxPagePayload is the maximum valid page payload size (full page).
	MaxPagePayload = offheap.PageSize // 4096 bytes

	// MinDiskPageSize is the minimum valid on-disk page size.
	MinDiskPageSize = CRCSize + MinPagePayload // 60 bytes

	// MaxDiskPageSize is the maximum valid on-disk page size.
	MaxDiskPageSize = CRCSize + MaxPagePayload // 4100 bytes
)

// PageSerializer handles serialization/deserialization of BTree pages
// between mmap memory and .ao disk files.
//
// Disk format (variable length): [CRC32C:4][PageHeader+Data:pageLength]
// CRC32C (Castagnoli) covers PageHeader+Data (everything after the CRC prefix).
type PageSerializer struct{}

// Serialize encodes an mmap page into the variable-length disk format.
// pageLength must be in [MinPagePayload, MaxPagePayload].
// Returns [CRC32C:4][pageData:pageLength].
func (s *PageSerializer) Serialize(ptr unsafe.Pointer, pageLength int) ([]byte, error) {
	if ptr == nil {
		return nil, ErrNilDestination
	}
	if pageLength < MinPagePayload || pageLength > MaxPagePayload {
		return nil, fmt.Errorf("page_serializer: invalid pageLength %d (range [%d,%d]): %w",
			pageLength, MinPagePayload, MaxPagePayload, ErrInvalidPageLength)
	}

	diskLen := CRCSize + pageLength
	buf := make([]byte, diskLen)

	// CRC32C placeholder (offset 0-3)
	binary.LittleEndian.PutUint32(buf[0:CRCSize], 0)

	// Copy PageHeader + Data (offset 4 onwards)
	src := unsafe.Slice((*byte)(ptr), pageLength)
	copy(buf[CRCSize:], src)

	// CRC32C (Castagnoli) covers buf[CRCSize:] — same polynomial as WAL
	crc := wal.CRC32C(buf[CRCSize:])
	binary.LittleEndian.PutUint32(buf[0:CRCSize], crc)

	return buf, nil
}

// Deserialize decodes the variable-length disk format and writes it to the mmap destination.
// dst must be a valid MaxPagePayload-byte mmap page pointer.
// Returns the actual pageLength decoded.
func (s *PageSerializer) Deserialize(data []byte, dst unsafe.Pointer) (int, error) {
	// Bounds check: lower (minimum valid page) + upper (prevent abnormally large data)
	if len(data) < MinDiskPageSize || len(data) > MaxDiskPageSize {
		return 0, fmt.Errorf("page_serializer: invalid data len %d (range [%d,%d])",
			len(data), MinDiskPageSize, MaxDiskPageSize)
	}
	if dst == nil {
		return 0, ErrNilDestination
	}

	pageLength := len(data) - CRCSize

	// Verify CRC32C (Castagnoli, covers data[CRCSize:])
	expectedCRC := binary.LittleEndian.Uint32(data[0:CRCSize])
	actualCRC := wal.CRC32C(data[CRCSize:])
	if expectedCRC != actualCRC {
		return 0, ErrCRCMismatch
	}

	// Copy PageHeader + Data to mmap
	dstSlice := unsafe.Slice((*byte)(dst), MaxPagePayload)
	copy(dstSlice, data[CRCSize:])

	// Sanity check: pageType must be 0 or 1
	pageType := *(*uint8)(unsafe.Add(dst, offheap.PageTypeFieldOffset))
	if pageType != offheap.PageTypeIndex && pageType != offheap.PageTypeLeaf {
		return 0, fmt.Errorf("page_serializer: invalid pageType %d", pageType)
	}

	return pageLength, nil
}
