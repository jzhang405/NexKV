// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSerializeDeserializeLeafNode(t *testing.T) {
	// Create a leaf node
	node := NewNode(true)
	err := node.Insert([]byte("key1"), []byte("value1"))
	require.NoError(t, err)
	err = node.Insert([]byte("key2"), []byte("value2"))
	require.NoError(t, err)

	// Create page and serialize
	page := NewPage(1, model.LeafPage)
	err = SerializeNode(node, page)
	require.NoError(t, err)

	// Deserialize
	restored, err := DeserializeNode(page)
	require.NoError(t, err)

	// Verify
	require.True(t, restored.IsLeaf)
	assert.Equal(t, 2, len(restored.Keys))
	assert.Equal(t, 2, len(restored.Values))
	assert.True(t, bytes.Equal(restored.Keys[0], []byte("key1")))
	assert.True(t, bytes.Equal(restored.Values[0], []byte("value1")))
	assert.True(t, bytes.Equal(restored.Keys[1], []byte("key2")))
	assert.True(t, bytes.Equal(restored.Values[1], []byte("value2")))
}

func TestSerializeDeserializeInternalNode(t *testing.T) {
	// Create internal node with children
	node := NewNode(false)
	child1 := NewNode(true)
	_ = child1.Insert([]byte("a"), []byte("a_value"))
	child2 := NewNode(true)
	_ = child2.Insert([]byte("z"), []byte("z_value"))

	node.Children = []*Node{child1, child2}
	node.Keys = [][]byte{[]byte("m")} // Separator key

	// Create page and serialize
	page := NewPage(1, model.InternalPage)
	err := SerializeNode(node, page)
	require.NoError(t, err)

	// Deserialize
	restored, err := DeserializeNode(page)
	require.NoError(t, err)

	// Verify
	require.False(t, restored.IsLeaf)
	assert.Equal(t, 1, len(restored.Keys))
	assert.True(t, bytes.Equal(restored.Keys[0], []byte("m")))
	// Note: children are not fully restored in pure memory mode
}

func TestSerializeEmptyNode(t *testing.T) {
	// Empty leaf node
	node := NewNode(true)

	page := NewPage(1, model.LeafPage)
	err := SerializeNode(node, page)
	require.NoError(t, err)

	restored, err := DeserializeNode(page)
	require.NoError(t, err)
	assert.True(t, restored.IsLeaf)
	assert.Equal(t, 0, len(restored.Keys))
	assert.Equal(t, 0, len(restored.Values))
}

func TestSerializeLargeNode(t *testing.T) {
	// Create node with many keys
	node := NewNode(true)
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := node.Insert(key, value)
		require.NoError(t, err)
	}

	// Check size
	size, err := GetSerializedSize(node)
	require.NoError(t, err)
	assert.Less(t, size, PageDataSize)

	// Serialize and deserialize
	page := NewPage(1, model.LeafPage)
	err = SerializeNode(node, page)
	require.NoError(t, err)

	restored, err := DeserializeNode(page)
	require.NoError(t, err)

	// Verify all keys
	assert.Equal(t, 100, len(restored.Keys))
	for i := 0; i < 100; i++ {
		expectedKey := []byte{byte(i)}
		expectedValue := []byte{byte(i + 100)}
		assert.True(t, bytes.Equal(restored.Keys[i], expectedKey))
		assert.True(t, bytes.Equal(restored.Values[i], expectedValue))
	}
}

func TestGetSerializedSize(t *testing.T) {
	tests := []struct {
		name     string
		node     *Node
		expected int
	}{
		{
			name:     "empty node",
			node:     NewNode(true),
			expected: 8, // Header only
		},
		{
			name: "single key-value",
			node: func() *Node {
				n := NewNode(true)
				_ = n.Insert([]byte("k"), []byte("v"))
				return n
			}(),
			expected: 8 + 2 + 1 + 2 + 1, // Header + KeyLen+Key + ValueLen+Value
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size, err := GetSerializedSize(tt.node)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, size)
		})
	}
}

func TestValidateSerializedData(t *testing.T) {
	t.Run("valid leaf node data", func(t *testing.T) {
		node := NewNode(true)
		_ = node.Insert([]byte("key1"), []byte("val1"))

		page := NewPage(1, model.LeafPage)
		err := SerializeNode(node, page)
		require.NoError(t, err)

		err = ValidateSerializedData(page.Data[:])
		assert.NoError(t, err)
	})

	t.Run("invalid - too short", func(t *testing.T) {
		data := []byte{1, 0, 0} // Too short
		err := ValidateSerializedData(data)
		assert.Error(t, err)
	})

	t.Run("invalid - truncated key", func(t *testing.T) {
		data := []byte{
			1,       // IsLeaf
			0, 0, 0, // Padding
			1, 0, 0, 0, // NumKeys = 1
			5, 0, // KeyLen = 5
			'k', 'e', // Only 2 bytes of key data
		}
		err := ValidateSerializedData(data)
		assert.Error(t, err)
	})
}

func TestPageFromNode(t *testing.T) {
	node := NewNode(true)
	_ = node.Insert([]byte("test"), []byte("data"))

	page, err := PageFromNode(42, node)
	require.NoError(t, err)

	assert.Equal(t, model.PageID(42), page.ID)
	assert.Equal(t, model.LeafPage, page.Type)
	assert.True(t, page.IsDirty())
}

func TestNodeFromPage(t *testing.T) {
	original := NewNode(true)
	_ = original.Insert([]byte("key"), []byte("value"))

	page, err := PageFromNode(1, original)
	require.NoError(t, err)

	restored, err := NodeFromPage(page)
	require.NoError(t, err)

	assert.True(t, restored.IsLeaf)
	assert.Equal(t, 1, len(restored.Keys))
	assert.True(t, bytes.Equal(restored.Keys[0], []byte("key")))
}

func TestSerializeNilNode(t *testing.T) {
	page := NewPage(1, model.LeafPage)
	err := SerializeNode(nil, page)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node is nil")
}

func TestDeserializeNilPage(t *testing.T) {
	_, err := DeserializeNode(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "page is nil")
}

// BenchmarkSerializeNode benchmarks the serialization performance.
func BenchmarkSerializeNode(b *testing.B) {
	node := NewNode(true)
	for i := 0; i < 50; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		_ = node.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page := NewPage(1, model.LeafPage)
		_ = SerializeNode(node, page)
	}
}

// BenchmarkDeserializeNode benchmarks the deserialization performance.
func BenchmarkDeserializeNode(b *testing.B) {
	node := NewNode(true)
	for i := 0; i < 50; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		_ = node.Insert(key, value)
	}

	page := NewPage(1, model.LeafPage)
	_ = SerializeNode(node, page)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DeserializeNode(page)
	}
}
