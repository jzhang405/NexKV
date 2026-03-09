// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

var (
	// ErrInvalidDataFormat is returned when data format is invalid.
	ErrInvalidDataFormat = errors.New("invalid data format")
)

// Serializer handles binary serialization of pages and nodes.
type Serializer struct {
	compression model.CompressionType
}

// NewSerializer creates a new serializer with the given compression type.
func NewSerializer(compression model.CompressionType) *Serializer {
	return &Serializer{
		compression: compression,
	}
}

// MarshalPage serializes a page to binary format.
//
// Binary Layout (Uncompressed):
//   [1]   PageType (uint8)
//   [8]   Version (uint64, big-endian)
//   [8]   PageID (uint64, big-endian)
//   [4]   RefCount (uint32, big-endian)
//   [N]   Data (4075 bytes for 4KB page)
//
// Binary Layout (Compressed):
//   [1]   PageType (uint8)
//   [8]   Version (uint64, big-endian)
//   [8]   PageID (uint64, big-endian)
//   [4]   CompressedFlag (uint32, 1 if compressed)
//   [4]   DataLen (uint32, big-endian, compressed data length)
//   [N]   CompressedData (variable length)
func (s *Serializer) MarshalPage(page *Page) ([]byte, error) {
	if page == nil {
		return nil, errors.New("MarshalPage: nil page")
	}

	buf := make([]byte, PageSize)

	// Write header
	buf[0] = byte(page.Type)
	binary.BigEndian.PutUint64(buf[1:9], page.Version)
	binary.BigEndian.PutUint64(buf[9:17], uint64(page.ID))
	binary.BigEndian.PutUint32(buf[17:21], uint32(page.GetRefCount()))

	// Copy data
	copy(buf[PageHeaderSize:], page.Data[:])

	// Apply compression if enabled
	if s.compression != model.CompressionNone {
		compressed, err := s.compress(buf[PageHeaderSize:])
		if err != nil {
			return nil, fmt.Errorf("MarshalPage: compression failed: %w", err)
		}

		// Build compressed format
		result := make([]byte, PageHeaderSize+4+4+len(compressed))
		copy(result, buf[:PageHeaderSize])

		// Set compressed flag
		binary.BigEndian.PutUint32(result[17:21], 1)

		// Write compressed data length
		binary.BigEndian.PutUint32(result[21:25], uint32(len(compressed)))

		// Write compressed data
		copy(result[25:], compressed)

		return result, nil
	}

	return buf, nil
}

// UnmarshalPage deserializes a page from binary format.
func (s *Serializer) UnmarshalPage(data []byte) (*Page, error) {
	if len(data) < PageHeaderSize {
		return nil, ErrInvalidDataFormat
	}

	// Read header
	pageType := model.PageType(data[0])
	version := binary.BigEndian.Uint64(data[1:9])
	pageID := model.PageID(binary.BigEndian.Uint64(data[9:17]))
	refCountOrFlag := binary.BigEndian.Uint32(data[17:21])

	// Check if data is compressed (by checking data length)
	// Compressed format: [21]header [4]CompressedFlag [4]DataLen [N]Data
	// Uncompressed format: [21]header [4075]Data (total 4096)
	if len(data) != PageSize {
		// Assume compressed format
		if len(data) < PageHeaderSize+4 {
			return nil, ErrInvalidDataFormat
		}

		// Verify compressed flag
		if refCountOrFlag != 1 {
			return nil, fmt.Errorf("UnmarshalPage: invalid compressed format, expected flag=1, got %d", refCountOrFlag)
		}

		// Read compressed data length
		dataLen := binary.BigEndian.Uint32(data[21:25])
		if len(data) < PageHeaderSize+4+int(dataLen) {
			return nil, ErrInvalidDataFormat
		}
		compressedData := data[25 : 25+dataLen]

		// Decompress
		decompressed, err := s.decompress(compressedData)
		if err != nil {
			return nil, fmt.Errorf("UnmarshalPage: decompression failed: %w", err)
		}

		if len(decompressed) > PageDataSize {
			return nil, fmt.Errorf("UnmarshalPage: decompressed data too large: %d > %d", len(decompressed), PageDataSize)
		}

		// Build page
		page := &Page{
			ID:      pageID,
			Type:    pageType,
			Version: version,
		}
		copy(page.Data[:], decompressed)
		page.RefCount.Store(int32(1)) // Default refcount for loaded pages

		return page, nil
	}

	// Uncompressed format (4096 bytes total)
	page := &Page{
		ID:      pageID,
		Type:    pageType,
		Version: version,
	}
	copy(page.Data[:], data[PageHeaderSize:])
	page.RefCount.Store(int32(refCountOrFlag))

	return page, nil
}

// compress compresses data using the configured compression algorithm.
// TODO(Phase 5): Integrate actual compression library (Snappy, LZ4, or ZSTD).
func (s *Serializer) compress(data []byte) ([]byte, error) {
	// Phase 1: No compression implemented yet
	return data, nil
}

// decompress decompresses data using the configured compression algorithm.
// TODO(Phase 5): Integrate actual compression library (Snappy, LZ4, or ZSTD).
func (s *Serializer) decompress(data []byte) ([]byte, error) {
	// Phase 1: No compression implemented yet
	return data, nil
}

// MarshalNode serializes a node to binary format.
// Optimized to reduce allocations and append operations.
func (s *Serializer) MarshalNode(node *Node) ([]byte, error) {
	if node == nil {
		return nil, errors.New("MarshalNode: nil node")
	}

	keyCount := len(node.Keys)

	// Calculate total size
	size := 4 // Key count (uint32)
	for _, key := range node.Keys {
		size += 4 + len(key) // Key length + key data
	}

	if node.IsLeaf {
		for _, value := range node.Values {
			size += 4 + len(value) // Value length + value data
		}
	} else {
		size += len(node.Children) * 8 // PageID (uint64)
	}

	// Pre-allocate buffer with exact size
	buf := make([]byte, size)

	// Write key count at position 0
	binary.BigEndian.PutUint32(buf[0:4], uint32(keyCount))

	offset := 4

	// Write keys
	for _, key := range node.Keys {
		// Key length
		binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(key)))
		offset += 4
		// Key data
		copy(buf[offset:offset+len(key)], key)
		offset += len(key)
	}

	// Write values or children
	if node.IsLeaf {
		for _, value := range node.Values {
			// Value length
			binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(len(value)))
			offset += 4
			// Value data
			copy(buf[offset:offset+len(value)], value)
			offset += len(value)
		}
	} else {
		for _, childID := range node.Children {
			// Child ID (8 bytes)
			binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(childID))
			offset += 8
		}
	}

	return buf, nil
}

// UnmarshalNode deserializes a node from binary format.
// Optimized to reuse pre-allocated slices and reduce allocations.
func (s *Serializer) UnmarshalNode(data []byte, isLeaf bool) (*Node, error) {
	if len(data) < 4 {
		return nil, ErrInvalidDataFormat
	}

	// Read key count first to know exact capacity needed
	keyCount := binary.BigEndian.Uint32(data[0:4])

	// Create node with pre-allocated capacity
	node := &Node{
		IsLeaf:   isLeaf,
		Keys:     make([][]byte, keyCount, keyCount),     // Exact capacity
		Children: make([]model.PageID, 0, keyCount+1),     // Pre-allocate
	}

	offset := 4

	// Read keys
	for i := uint32(0); i < keyCount; i++ {
		if offset+4 > len(data) {
			return nil, ErrInvalidDataFormat
		}
		keyLen := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4

		if offset+int(keyLen) > len(data) {
			return nil, ErrInvalidDataFormat
		}
		// Use slice reference instead of copying
		node.Keys[i] = data[offset : offset+int(keyLen)]
		offset += int(keyLen)
	}

	// Read values or children
	if isLeaf {
		node.Values = make([][]byte, keyCount, keyCount) // Exact capacity
		for i := uint32(0); i < keyCount; i++ {
			if offset+4 > len(data) {
				return nil, ErrInvalidDataFormat
			}
			valueLen := binary.BigEndian.Uint32(data[offset : offset+4])
			offset += 4

			if offset+int(valueLen) > len(data) {
				return nil, ErrInvalidDataFormat
			}
			// Use slice reference instead of copying
			node.Values[i] = data[offset : offset+int(valueLen)]
			offset += int(valueLen)
		}
	} else {
		childCount := keyCount + 1
		node.Children = node.Children[:childCount] // Use pre-allocated slice
		for i := uint32(0); i < childCount; i++ {
			if offset+8 > len(data) {
				return nil, ErrInvalidDataFormat
			}
			node.Children[i] = model.PageID(binary.BigEndian.Uint64(data[offset : offset+8]))
			offset += 8
		}
	}

	return node, nil
}
