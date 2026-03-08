// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestMarshalPage verifies page marshaling.
func TestMarshalPage(t *testing.T) {
	serializer := NewSerializer(model.CompressionNone)

	t.Run("marshal leaf page", func(t *testing.T) {
		page := NewPage(1, model.LeafPage)
		page.Data[0] = 0x01
		page.Data[100] = 0x64

		data, err := serializer.MarshalPage(page)
		require.NoError(t, err)
		require.Len(t, data, PageSize)

		// Verify header
		assert.Equal(t, byte(model.LeafPage), data[0])
		assert.Equal(t, byte(0x01), data[PageHeaderSize]) // First byte of data
		assert.Equal(t, byte(0x64), data[100+PageHeaderSize])
	})

	t.Run("marshal internal page", func(t *testing.T) {
		page := NewPage(2, model.InternalPage)
		page.Version = 100

		data, err := serializer.MarshalPage(page)
		require.NoError(t, err)
		require.Len(t, data, PageSize)

		// Verify type
		assert.Equal(t, byte(model.InternalPage), data[0])
	})

	t.Run("marshal nil page", func(t *testing.T) {
		_, err := serializer.MarshalPage(nil)
		assert.Error(t, err)
	})
}

// TestUnmarshalPage verifies page unmarshaling.
func TestUnmarshalPage(t *testing.T) {
	serializer := NewSerializer(model.CompressionNone)

	t.Run("unmarshal leaf page", func(t *testing.T) {
		page := NewPage(1, model.LeafPage)
		page.Data[0] = 0x42
		page.Data[PageDataSize-1] = 0xFF

		data, err := serializer.MarshalPage(page)
		require.NoError(t, err)

		unmarshaled, err := serializer.UnmarshalPage(data)
		require.NoError(t, err)

		assert.Equal(t, page.ID, unmarshaled.ID)
		assert.Equal(t, page.Type, unmarshaled.Type)
		assert.Equal(t, page.Version, unmarshaled.Version)
		assert.Equal(t, byte(0x42), unmarshaled.Data[0])
		assert.Equal(t, byte(0xFF), unmarshaled.Data[PageDataSize-1])
	})

	t.Run("unmarshal internal page", func(t *testing.T) {
		page := NewPage(5, model.InternalPage)
		page.Version = 42

		data, err := serializer.MarshalPage(page)
		require.NoError(t, err)

		unmarshaled, err := serializer.UnmarshalPage(data)
		require.NoError(t, err)

		assert.Equal(t, page.ID, unmarshaled.ID)
		assert.Equal(t, page.Type, unmarshaled.Type)
		assert.Equal(t, page.Version, unmarshaled.Version)
	})

	t.Run("unmarshal invalid data", func(t *testing.T) {
		_, err := serializer.UnmarshalPage([]byte{1, 2, 3})
		assert.Error(t, err)
	})

	t.Run("roundtrip with refcount", func(t *testing.T) {
		page := NewPage(10, model.LeafPage)
		page.Acquire()
		page.Acquire()
		page.Release() // Now refcount = 2

		data, err := serializer.MarshalPage(page)
		require.NoError(t, err)

		unmarshaled, err := serializer.UnmarshalPage(data)
		require.NoError(t, err)

		assert.Equal(t, page.ID, unmarshaled.ID)
		assert.Equal(t, int32(2), unmarshaled.GetRefCount())
	})
}

// TestMarshalNode verifies node marshaling.
func TestMarshalNode(t *testing.T) {
	serializer := NewSerializer(model.CompressionNone)

	t.Run("marshal leaf node", func(t *testing.T) {
		node := NewNode(true)
		err := node.Insert([]byte("key1"), []byte("value1"))
		require.NoError(t, err)
		err = node.Insert([]byte("key2"), []byte("value2"))
		require.NoError(t, err)

		data, err := serializer.MarshalNode(node)
		require.NoError(t, err)
		assert.NotEmpty(t, data)
	})

	t.Run("marshal internal node", func(t *testing.T) {
		node := NewNode(false)
		node.Keys = append(node.Keys, []byte("key1"))
		node.Children = append(node.Children, 1, 2)

		data, err := serializer.MarshalNode(node)
		require.NoError(t, err)
		assert.NotEmpty(t, data)
	})

	t.Run("marshal nil node", func(t *testing.T) {
		_, err := serializer.MarshalNode(nil)
		assert.Error(t, err)
	})

	t.Run("marshal empty node", func(t *testing.T) {
		node := NewNode(true)
		data, err := serializer.MarshalNode(node)
		require.NoError(t, err)

		// Should only have key count (4 bytes)
		assert.Len(t, data, 4)
	})
}

// TestUnmarshalNode verifies node unmarshaling.
func TestUnmarshalNode(t *testing.T) {
	serializer := NewSerializer(model.CompressionNone)

	t.Run("unmarshal leaf node", func(t *testing.T) {
		node := NewNode(true)
		err := node.Insert([]byte("apple"), []byte("red"))
		require.NoError(t, err)
		err = node.Insert([]byte("banana"), []byte("yellow"))
		require.NoError(t, err)

		data, err := serializer.MarshalNode(node)
		require.NoError(t, err)

		unmarshaled, err := serializer.UnmarshalNode(data, true)
		require.NoError(t, err)

		assert.Equal(t, node.Size(), unmarshaled.Size())
		for i := range node.Keys {
			assert.True(t, bytes.Equal(node.Keys[i], unmarshaled.Keys[i]))
			assert.True(t, bytes.Equal(node.Values[i], unmarshaled.Values[i]))
		}
	})

	t.Run("unmarshal internal node", func(t *testing.T) {
		node := NewNode(false)
		node.Keys = append(node.Keys, []byte("key1"), []byte("key2"))
		node.Children = append(node.Children, 1, 2, 3)

		data, err := serializer.MarshalNode(node)
		require.NoError(t, err)

		unmarshaled, err := serializer.UnmarshalNode(data, false)
		require.NoError(t, err)

		assert.Equal(t, node.Size(), unmarshaled.Size())
		assert.Equal(t, len(node.Children), len(unmarshaled.Children))
	})

	t.Run("unmarshal invalid data", func(t *testing.T) {
		_, err := serializer.UnmarshalNode([]byte{1, 2, 3}, true)
		assert.Error(t, err)
	})

	t.Run("unmarshal empty node", func(t *testing.T) {
		data := []byte{0, 0, 0, 0} // Key count = 0
		node, err := serializer.UnmarshalNode(data, true)
		require.NoError(t, err)
		assert.True(t, node.IsEmpty())
	})
}

// TestSerializerRoundtrip verifies roundtrip serialization.
func TestSerializerRoundtrip(t *testing.T) {
	serializer := NewSerializer(model.CompressionNone)

	t.Run("page roundtrip", func(t *testing.T) {
		original := NewPage(42, model.LeafPage)
		original.Version = 100
		original.Data[0] = 0xAB
		original.Data[100] = 0xCD

		data, err := serializer.MarshalPage(original)
		require.NoError(t, err)

		recovered, err := serializer.UnmarshalPage(data)
		require.NoError(t, err)

		assert.Equal(t, original.ID, recovered.ID)
		assert.Equal(t, original.Type, recovered.Type)
		assert.Equal(t, original.Version, recovered.Version)
		assert.Equal(t, original.Data[0], recovered.Data[0])
		assert.Equal(t, original.Data[100], recovered.Data[100])
	})

	t.Run("node roundtrip", func(t *testing.T) {
		original := NewNode(true)
		pairs := [][]byte{
			[]byte("key1"), []byte("value1"),
			[]byte("key2"), []byte("value2"),
			[]byte("key3"), []byte("value3"),
		}

		for i := 0; i < len(pairs); i += 2 {
			err := original.Insert(pairs[i], pairs[i+1])
			require.NoError(t, err)
		}

		data, err := serializer.MarshalNode(original)
		require.NoError(t, err)

		recovered, err := serializer.UnmarshalNode(data, true)
		require.NoError(t, err)

		assert.Equal(t, original.Size(), recovered.Size())
		for i := range original.Keys {
			assert.True(t, bytes.Equal(original.Keys[i], recovered.Keys[i]))
			assert.True(t, bytes.Equal(original.Values[i], recovered.Values[i]))
		}
	})
}

// BenchmarkMarshalPage benchmarks page marshaling.
func BenchmarkMarshalPage(b *testing.B) {
	serializer := NewSerializer(model.CompressionNone)
	page := NewPage(1, model.LeafPage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := serializer.MarshalPage(page)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUnmarshalPage benchmarks page unmarshaling.
func BenchmarkUnmarshalPage(b *testing.B) {
	serializer := NewSerializer(model.CompressionNone)
	page := NewPage(1, model.LeafPage)
	data, err := serializer.MarshalPage(page)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := serializer.UnmarshalPage(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMarshalNode benchmarks node marshaling.
func BenchmarkMarshalNode(b *testing.B) {
	serializer := NewSerializer(model.CompressionNone)
	node := NewNode(true)

	for i := 0; i < 10; i++ {
		node.Insert([]byte{byte(i)}, []byte("value"))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := serializer.MarshalNode(node)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUnmarshalNode benchmarks node unmarshaling.
func BenchmarkUnmarshalNode(b *testing.B) {
	serializer := NewSerializer(model.CompressionNone)
	node := NewNode(true)

	for i := 0; i < 10; i++ {
		node.Insert([]byte{byte(i)}, []byte("value"))
	}

	data, err := serializer.MarshalNode(node)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := serializer.UnmarshalNode(data, true)
		if err != nil {
			b.Fatal(err)
		}
	}
}
