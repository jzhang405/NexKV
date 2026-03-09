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

// TestNewNode verifies node creation.
func TestNewNode(t *testing.T) {
	t.Run("leaf node", func(t *testing.T) {
		node := NewNode(true)
		assert.True(t, node.IsLeaf)
		assert.NotNil(t, node.Keys)
		assert.NotNil(t, node.Values)
		assert.NotNil(t, node.Children)
		assert.True(t, node.IsEmpty())
		assert.False(t, node.IsFull())
	})

	t.Run("internal node", func(t *testing.T) {
		node := NewNode(false)
		assert.False(t, node.IsLeaf)
		assert.NotNil(t, node.Keys)
		assert.NotNil(t, node.Values)
		assert.NotNil(t, node.Children)
	})
}

// TestNodeSearch verifies binary search functionality.
func TestNodeSearch(t *testing.T) {
	node := NewNode(true)

	// Insert some keys
	keys := [][]byte{
		[]byte("apple"),
		[]byte("banana"),
		[]byte("cherry"),
		[]byte("date"),
	}

	for _, key := range keys {
		err := node.Insert(key, []byte("value"))
		require.NoError(t, err)
	}

	tests := []struct {
		name     string
		key      []byte
		expected int
	}{
		{"existing key", []byte("banana"), 1},
		{"key before all", []byte("aardvark"), 0},
		{"key after all", []byte("zebra"), 4},
		{"key in middle", []byte("blueberry"), 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := node.Search(tt.key)
			assert.Equal(t, tt.expected, idx)
		})
	}
}

// TestNodeInsert verifies key-value insertion.
func TestNodeInsert(t *testing.T) {
	t.Run("insert into empty node", func(t *testing.T) {
		node := NewNode(true)
		err := node.Insert([]byte("key"), []byte("value"))
		require.NoError(t, err)

		assert.Equal(t, 1, node.Size())
		assert.True(t, bytes.Equal(node.Keys[0], []byte("key")))
		assert.True(t, bytes.Equal(node.Values[0], []byte("value")))
	})

	t.Run("insert in sorted order", func(t *testing.T) {
		node := NewNode(true)
		keys := []string{"apple", "banana", "cherry", "date"}

		for _, key := range keys {
			err := node.Insert([]byte(key), []byte("value"))
			require.NoError(t, err)
		}

		assert.Equal(t, 4, node.Size())

		// Verify sorted order
		for i := range keys {
			assert.True(t, bytes.Equal(node.Keys[i], []byte(keys[i])))
		}
	})

	t.Run("insert in reverse order", func(t *testing.T) {
		node := NewNode(true)
		keys := []string{"date", "cherry", "banana", "apple"}

		for _, key := range keys {
			err := node.Insert([]byte(key), []byte("value"))
			require.NoError(t, err)
		}

		assert.Equal(t, 4, node.Size())

		// Verify sorted order
		expected := []string{"apple", "banana", "cherry", "date"}
		for i, exp := range expected {
			assert.True(t, bytes.Equal(node.Keys[i], []byte(exp)))
		}
	})

	t.Run("update existing key", func(t *testing.T) {
		node := NewNode(true)
		err := node.Insert([]byte("key"), []byte("value1"))
		require.NoError(t, err)

		err = node.Insert([]byte("key"), []byte("value2"))
		require.NoError(t, err)

		assert.Equal(t, 1, node.Size())
		assert.True(t, bytes.Equal(node.Values[0], []byte("value2")))
	})

	t.Run("insert into full node", func(t *testing.T) {
		node := NewNode(true)

		// Fill node to capacity
		for i := range model.DefaultMaxKeys {
			key := []byte{byte(i)}
			err := node.Insert(key, []byte("value"))
			require.NoError(t, err)
		}

		// Try to insert one more
		err := node.Insert([]byte("extra"), []byte("value"))
		assert.ErrorIs(t, err, ErrNodeFull)
	})

	t.Run("insert empty key", func(t *testing.T) {
		node := NewNode(true)
		err := node.Insert([]byte(""), []byte("value"))
		assert.ErrorIs(t, err, ErrInvalidKey)
	})
}

// TestNodeGet verifies key retrieval.
func TestNodeGet(t *testing.T) {
	node := NewNode(true)

	// Insert some data
	pairs := map[string]string{
		"apple":  "red",
		"banana": "yellow",
		"cherry": "purple",
	}

	for key, value := range pairs {
		err := node.Insert([]byte(key), []byte(value))
		require.NoError(t, err)
	}

	t.Run("get existing key", func(t *testing.T) {
		value, err := node.Get([]byte("banana"))
		require.NoError(t, err)
		assert.True(t, bytes.Equal(value, []byte("yellow")))
	})

	t.Run("get non-existing key", func(t *testing.T) {
		_, err := node.Get([]byte("zebra"))
		assert.ErrorIs(t, err, ErrKeyNotFound)
	})

	t.Run("get from internal node", func(t *testing.T) {
		internalNode := NewNode(false)
		_, err := internalNode.Get([]byte("key"))
		assert.Error(t, err)
	})
}

// TestNodeSplit verifies node splitting.
func TestNodeSplit(t *testing.T) {
	t.Run("split full leaf node", func(t *testing.T) {
		node := NewNode(true)

		// Fill node to capacity
		for i := range model.DefaultMaxKeys {
			key := []byte{byte(i)}
			err := node.Insert(key, []byte("value"))
			require.NoError(t, err)
		}

		// Split
		rightNode, medianKey, err := node.Split()
		require.NoError(t, err)

		// Verify split: (DefaultMaxKeys - 1) / 2 = 127
		// Left: keys[0..127] (128 keys including median copy)
		// Right: keys[128..255] (128 keys)
		assert.Equal(t, 128, node.Size(), "left node should have 128 keys")
		assert.Equal(t, 128, rightNode.Size(), "right node should have 128 keys")
		assert.NotNil(t, medianKey)

		// Verify total keys preserved (256 original + 1 copy in parent = 257)
		totalKeys := node.Size() + rightNode.Size() + 1 // +1 for median copy in parent
		assert.Equal(t, model.DefaultMaxKeys+1, totalKeys)
	})

	t.Run("split non-full node", func(t *testing.T) {
		node := NewNode(true)
		_ = node.Insert([]byte("key"), []byte("value"))

		_, _, err := node.Split()
		assert.ErrorIs(t, err, ErrNodeNotFull)
	})
}

// TestNodeMerge verifies node merging.
func TestNodeMerge(t *testing.T) {
	t.Run("merge leaf nodes", func(t *testing.T) {
		node1 := NewNode(true)
		node2 := NewNode(true)

		// Add keys to both nodes (below capacity)
		for i := range 30 {
			key1 := []byte{byte(i)}
			key2 := []byte{byte(i + 30)}
			_ = node1.Insert(key1, []byte("value"))
			_ = node2.Insert(key2, []byte("value"))
		}

		err := node1.Merge(node2)
		require.NoError(t, err)

		assert.Equal(t, 60, node1.Size())
		assert.Equal(t, 30, node2.Size()) // node2 unchanged
	})

	t.Run("merge different node types", func(t *testing.T) {
		leafNode := NewNode(true)
		internalNode := NewNode(false)

		err := leafNode.Merge(internalNode)
		assert.Error(t, err)
	})

	t.Run("merge overflow", func(t *testing.T) {
		node1 := NewNode(true)
		node2 := NewNode(true)

		// Fill nodes near capacity (130+130=260 > 256)
		for i := range 130 {
			_ = node1.Insert([]byte{byte(i % 256)}, []byte("value"))
		}
		for i := range 130 {
			_ = node2.Insert([]byte{byte((i + 130) % 256)}, []byte("value"))
		}

		err := node1.Merge(node2)
		assert.Error(t, err)
	})
}

// TestNodeDelete verifies key deletion.
func TestNodeDelete(t *testing.T) {
	t.Run("delete existing key from leaf", func(t *testing.T) {
		node := NewNode(true)
		_ = node.Insert([]byte("apple"), []byte("red"))
		_ = node.Insert([]byte("banana"), []byte("yellow"))
		_ = node.Insert([]byte("cherry"), []byte("purple"))

		err := node.Delete([]byte("banana"))
		require.NoError(t, err)

		assert.Equal(t, 2, node.Size())
		assert.False(t, node.hasKey([]byte("banana")))
	})

	t.Run("delete non-existing key", func(t *testing.T) {
		node := NewNode(true)
		_ = node.Insert([]byte("key"), []byte("value"))

		err := node.Delete([]byte("nonexistent"))
		assert.ErrorIs(t, err, ErrKeyNotFound)
	})
}

// TestNodeStateChecks verifies state check methods.
func TestNodeStateChecks(t *testing.T) {
	node := NewNode(true)

	assert.True(t, node.IsEmpty())
	assert.False(t, node.IsFull())
	assert.True(t, node.IsUnderflow()) // Empty node is underflow

	// Add some keys (still below minimum)
	for i := range 10 {
		_ = node.Insert([]byte{byte(i)}, []byte("value"))
	}

	assert.False(t, node.IsEmpty())
	assert.False(t, node.IsFull())
	assert.True(t, node.IsUnderflow()) // 10 < DefaultMinKeys (64)
}

// TestNodeClear verifies node clearing.
func TestNodeClear(t *testing.T) {
	node := NewNode(true)

	// Add some keys
	for i := range 10 {
		_ = node.Insert([]byte{byte(i)}, []byte("value"))
	}

	assert.Equal(t, 10, node.Size())

	node.Clear()
	assert.True(t, node.IsEmpty())
	assert.Equal(t, 0, len(node.Values))
	assert.Equal(t, 0, len(node.Children))
}

// TestNodeClone verifies node cloning.
func TestNodeClone(t *testing.T) {
	node := NewNode(true)
	_ = node.Insert([]byte("key1"), []byte("value1"))
	_ = node.Insert([]byte("key2"), []byte("value2"))

	clone := node.Clone()

	// Verify clone has same data
	assert.Equal(t, node.Size(), clone.Size())
	assert.True(t, bytes.Equal(node.Keys[0], clone.Keys[0]))
	assert.True(t, bytes.Equal(node.Keys[1], clone.Keys[1]))

	// Verify clone is independent
	_ = clone.Insert([]byte("key3"), []byte("value3"))
	assert.Equal(t, 2, node.Size())
	assert.Equal(t, 3, clone.Size())
}

// Helper method to check if key exists
func (n *Node) hasKey(key []byte) bool {
	idx := n.Search(key)
	return idx < len(n.Keys) && bytes.Equal(n.Keys[idx], key)
}

// BenchmarkNodeInsert benchmarks node insertion.
func BenchmarkNodeInsert(b *testing.B) {
	node := NewNode(true)
	key := []byte("test-key")
	value := []byte("test-value")

	b.ResetTimer()
	for range b.N {
		_ = node.Insert(key, value)
		if node.IsFull() {
			node.Clear()
		}
	}
}

// BenchmarkNodeSearch benchmarks node search.
func BenchmarkNodeSearch(b *testing.B) {
	node := NewNode(true)

	// Fill node
	for i := range model.DefaultMaxKeys {
		_ = node.Insert([]byte{byte(i)}, []byte("value"))
	}

	key := []byte{64} // Middle key

	b.ResetTimer()
	for range b.N {
		node.Search(key)
	}
}

// BenchmarkNodeGet benchmarks node get operation.
func BenchmarkNodeGet(b *testing.B) {
	node := NewNode(true)
	key := []byte("test-key")
	value := []byte("test-value")
	_ = node.Insert(key, value)

	b.ResetTimer()
	for range b.N {
		_, _ = node.Get(key)
	}
}

// BenchmarkNodeSplit benchmarks node splitting.
func BenchmarkNodeSplit(b *testing.B) {
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		node := NewNode(true)
		for j := range model.DefaultMaxKeys {
			_ = node.Insert([]byte{byte(j)}, []byte("value"))
		}
		b.StartTimer()

		_, _, _ = node.Split()
	}
}
