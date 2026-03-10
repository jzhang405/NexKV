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
	// ErrBufferTooSmall is returned when page data is too small.
	ErrBufferTooSmall = errors.New("buffer too small")

	// ErrInvalidFormat is returned when data format is invalid.
	ErrInvalidFormat = errors.New("invalid data format")
)

// SerializeNode serializes a Node into Page.Data.
//
// Layout (4075 bytes total):
//
//	Offset 0:     IsLeaf (1 byte)
//	Offset 1-3:   Padding (3 bytes)
//	Offset 4-7:   NumKeys (4 bytes, little-endian)
//	Offset 8+:    Keys section: [KeyLen(2) + KeyData] × NumKeys
//	              Then Values section: [ValueLen(2) + ValueData] × NumKeys (leaf)
//	              Or Children section: [PageID(8)] × (NumKeys+1) (internal)
func SerializeNode(node *Node, page *Page) error {
	if node == nil {
		return errors.New("SerializeNode: node is nil")
	}
	if page == nil {
		return errors.New("SerializeNode: page is nil")
	}

	// Get buffer
	buf := page.Data[:]
	if len(buf) < 8 {
		return ErrBufferTooSmall
	}

	// Offset 0: IsLeaf (1 byte)
	if node.IsLeaf {
		buf[0] = 1
	} else {
		buf[0] = 0
	}

	// Offset 1-3: Padding (3 bytes, reserved for future use)
	buf[1] = 0
	buf[2] = 0
	buf[3] = 0

	// Offset 4-7: NumKeys (4 bytes, little-endian)
	numKeys := uint32(len(node.Keys))
	binary.LittleEndian.PutUint32(buf[4:8], numKeys)

	// Offset 8+: Write keys and values/children
	offset := 8

	// Write keys: [KeyLen(2) + KeyData] × NumKeys
	for _, key := range node.Keys {
		keyLen := uint16(len(key))
		if offset+2 > len(buf) {
			return ErrBufferTooSmall
		}
		binary.LittleEndian.PutUint16(buf[offset:offset+2], keyLen)
		offset += 2

		if offset+int(keyLen) > len(buf) {
			return ErrBufferTooSmall
		}
		copy(buf[offset:offset+int(keyLen)], key)
		offset += int(keyLen)
	}

	// Write values (leaf nodes) or children (internal nodes)
	if node.IsLeaf {
		// Write values: [ValueLen(2) + ValueData] × NumKeys
		for _, value := range node.Values {
			valueLen := uint16(len(value))
			if offset+2 > len(buf) {
				return ErrBufferTooSmall
			}
			binary.LittleEndian.PutUint16(buf[offset:offset+2], valueLen)
			offset += 2

			if offset+int(valueLen) > len(buf) {
				return ErrBufferTooSmall
			}
			copy(buf[offset:offset+int(valueLen)], value)
			offset += int(valueLen)
		}
	} else {
		// Write children: [PageID(8)] × (NumKeys + 1)
		for _, child := range node.Children {
			if child == nil {
				// Use 0 as null PageID
				if offset+8 > len(buf) {
					return ErrBufferTooSmall
				}
				binary.LittleEndian.PutUint64(buf[offset:offset+8], 0)
			} else {
				// Use the child's PageID for serialization
				// If PageID is 0 (in-memory only), serialize as 0
				if offset+8 > len(buf) {
					return ErrBufferTooSmall
				}
				binary.LittleEndian.PutUint64(buf[offset:offset+8], uint64(child.PageID))
			}
			offset += 8
		}
	}

	// Mark page as dirty
	page.MarkDirty()

	return nil
}

// DeserializeNode deserializes Page.Data into a Node.
//
// See SerializeNode for the layout specification.
func DeserializeNode(page *Page) (*Node, error) {
	if page == nil {
		return nil, errors.New("DeserializeNode: page is nil")
	}

	// Get buffer
	buf := page.Data[:]
	if len(buf) < 8 {
		return nil, ErrBufferTooSmall
	}

	// Offset 0: IsLeaf (1 byte)
	isLeaf := buf[0] == 1

	// Offset 4-7: NumKeys (4 bytes, little-endian)
	numKeys := int(binary.LittleEndian.Uint32(buf[4:8]))

	// Create node
	node := NewNode(isLeaf)
	node.Keys = make([][]byte, 0, numKeys)

	// Offset 8+: Read keys
	offset := 8
	for i := 0; i < numKeys; i++ {
		if offset+2 > len(buf) {
			return nil, ErrBufferTooSmall
		}
		keyLen := int(binary.LittleEndian.Uint16(buf[offset : offset+2]))
		offset += 2

		if offset+keyLen > len(buf) {
			return nil, ErrBufferTooSmall
		}
		key := make([]byte, keyLen)
		copy(key, buf[offset:offset+keyLen])
		node.Keys = append(node.Keys, key)
		offset += keyLen
	}

	// Read values (leaf) or children (internal)
	if isLeaf {
		node.Values = make([][]byte, 0, numKeys)
		for i := 0; i < numKeys; i++ {
			if offset+2 > len(buf) {
				return nil, ErrBufferTooSmall
			}
			valueLen := int(binary.LittleEndian.Uint16(buf[offset : offset+2]))
			offset += 2

			if offset+valueLen > len(buf) {
				return nil, ErrBufferTooSmall
			}
			value := make([]byte, valueLen)
			copy(value, buf[offset:offset+valueLen])
			node.Values = append(node.Values, value)
			offset += valueLen
		}
	} else {
		// Read children: [PageID(8)] × (NumKeys + 1)
		// Note: For pure memory BTree, we don't restore children from PageID yet
		// This will be handled when Page-based architecture is fully implemented
		node.Children = make([]*Node, 0, numKeys+1)
		for i := 0; i < numKeys+1; i++ {
			if offset+8 > len(buf) {
				return nil, ErrBufferTooSmall
			}
			pageID := binary.LittleEndian.Uint64(buf[offset : offset+8])
			offset += 8

			if pageID == 0 {
				// Null child
				node.Children = append(node.Children, nil)
			} else {
				// Placeholder: will be resolved by PageCache
				// For now, create empty node as placeholder
				node.Children = append(node.Children, nil)
			}
		}
	}

	return node, nil
}

// GetSerializedSize returns the estimated size needed to serialize a node.
func GetSerializedSize(node *Node) (int, error) {
	if node == nil {
		return 0, errors.New("GetSerializedSize: node is nil")
	}

	size := 8 // Header: IsLeaf(1) + Padding(3) + NumKeys(4)

	// Keys: [KeyLen(2) + KeyData] × NumKeys
	for _, key := range node.Keys {
		size += 2 + len(key)
	}

	// Values or Children
	if node.IsLeaf {
		// Values: [ValueLen(2) + ValueData] × NumKeys
		for _, value := range node.Values {
			size += 2 + len(value)
		}
	} else {
		// Children: PageID(8) × (NumKeys + 1)
		size += 8 * (len(node.Keys) + 1)
	}

	return size, nil
}

// ValidateSerializedData validates that serialized data is well-formed.
func ValidateSerializedData(data []byte) error {
	if len(data) < 8 {
		return fmt.Errorf("data too short: %d < 8", len(data))
	}

	// Read numKeys
	numKeys := int(binary.LittleEndian.Uint32(data[4:8]))

	offset := 8

	// Validate keys
	for i := 0; i < numKeys; i++ {
		if offset+2 > len(data) {
			return fmt.Errorf("key %d: incomplete length at offset %d", i, offset)
		}
		keyLen := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
		offset += 2

		if offset+keyLen > len(data) {
			return fmt.Errorf("key %d: incomplete data at offset %d, need %d, have %d",
				i, offset, keyLen, len(data)-offset)
		}
		offset += keyLen
	}

	// Validate values or children based on IsLeaf flag
	isLeaf := data[0] == 1
	if isLeaf {
		for i := 0; i < numKeys; i++ {
			if offset+2 > len(data) {
				return fmt.Errorf("value %d: incomplete length at offset %d", i, offset)
			}
			valueLen := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
			offset += 2

			if offset+valueLen > len(data) {
				return fmt.Errorf("value %d: incomplete data at offset %d, need %d, have %d",
					i, offset, valueLen, len(data)-offset)
			}
			offset += valueLen
		}
	} else {
		expectedChildren := numKeys + 1
		for i := 0; i < expectedChildren; i++ {
			if offset+8 > len(data) {
				return fmt.Errorf("child %d: incomplete PageID at offset %d", i, offset)
			}
			offset += 8
		}
	}

	return nil
}

// PageFromNode creates a new Page and serializes the Node into it.
// This is a convenience function that combines NewPage and SerializeNode.
func PageFromNode(id model.PageID, node *Node) (*Page, error) {
	pageType := model.LeafPage
	if !node.IsLeaf {
		pageType = model.InternalPage
	}

	page := NewPage(id, pageType)
	if err := SerializeNode(node, page); err != nil {
		return nil, fmt.Errorf("PageFromNode: %w", err)
	}

	return page, nil
}

// NodeFromPage deserializes a Node from a Page.
// This is a convenience function that wraps DeserializeNode.
func NodeFromPage(page *Page) (*Node, error) {
	return DeserializeNode(page)
}
