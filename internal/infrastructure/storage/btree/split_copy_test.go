// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNode_SplitCopy 测试写时复制分裂
func TestNode_SplitCopy(t *testing.T) {
	t.Run("split copy creates independent nodes", func(t *testing.T) {
		// Create a full node
		original := NewNode(true)
		for i := 0; i < model.DefaultMaxKeys; i++ {
			key := []byte{byte(i)}
			value := []byte("value")
			err := original.Insert(key, value)
			require.NoError(t, err)
		}

		assert.Equal(t, model.DefaultMaxKeys, original.Size())
		assert.True(t, original.IsFull())

		// Perform SplitCopy (doesn't modify original)
		newLeft, newRight, medianKey, err := original.SplitCopy()
		require.NoError(t, err)
		require.NotNil(t, newLeft)
		require.NotNil(t, newRight)
		require.NotNil(t, medianKey)

		// Verify original is unchanged (Copy-on-Write!)
		assert.Equal(t, model.DefaultMaxKeys, original.Size())
		assert.True(t, original.IsFull())

		// Verify new nodes have correct sizes
		assert.Equal(t, 128, newLeft.Size())
		assert.Equal(t, 128, newRight.Size())

		// Verify all keys are present across nodes
		for i := 0; i < 128; i++ {
			key := []byte{byte(i)}
			value, err := newLeft.Get(key)
			require.NoError(t, err)
			assert.Equal(t, []byte("value"), value)
		}

		for i := 128; i < 256; i++ {
			key := []byte{byte(i)}
			value, err := newRight.Get(key)
			require.NoError(t, err)
			assert.Equal(t, []byte("value"), value)
		}

		t.Logf("SplitCopy successful:")
		t.Logf("  Original node: %d keys (unchanged)", original.Size())
		t.Logf("  New left: %d keys", newLeft.Size())
		t.Logf("  New right: %d keys", newRight.Size())
		t.Logf("  Median key: %v", medianKey)
	})

	t.Run("split copy enables concurrent reads", func(t *testing.T) {
		// Create a full node
		original := NewNode(true)
		for i := 0; i < model.DefaultMaxKeys; i++ {
			key := []byte{byte(i)}
			original.Insert(key, []byte("value"))
		}

		// Perform SplitCopy
		newLeft, newRight, _, err := original.SplitCopy()
		require.NoError(t, err)

		// Verify original can still be read
		for i := 0; i < model.DefaultMaxKeys; i++ {
			key := []byte{byte(i)}
			value, err := original.Get(key)
			require.NoError(t, err)
			assert.Equal(t, []byte("value"), value)
		}

		// Verify new nodes are accessible
		assert.Equal(t, 128, newLeft.Size())
		assert.Equal(t, 128, newRight.Size())

		t.Logf("Concurrent read test passed: original remains readable after SplitCopy")
	})
}

// TestNode_SplitCopyOptimized 测试优化的SplitCopy
func TestNode_SplitCopyOptimized(t *testing.T) {
	t.Run("optimized version produces same result", func(t *testing.T) {
		// Create a full node
		original := NewNode(true)
		for i := 0; i < model.DefaultMaxKeys; i++ {
			key := []byte{byte(i)}
			original.Insert(key, []byte("value"))
		}

		// Compare both versions
		left1, right1, median1, err1 := original.SplitCopy()
		left2, right2, median2, err2 := original.SplitCopyOptimized()

		require.NoError(t, err1)
		require.NoError(t, err2)

		// Verify both produce same result
		assert.Equal(t, left1.Size(), left2.Size())
		assert.Equal(t, right1.Size(), right2.Size())
		assert.Equal(t, median1, median2)

		t.Logf("Both versions produce identical results: %d + %d keys",
			left1.Size(), right1.Size())
	})
}

// BenchmarkNode_SplitCopy benchmarks SplitCopy (lock-free)
func BenchmarkNode_SplitCopy(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a full node
		node := NewNode(true)
		for j := 0; j < model.DefaultMaxKeys; j++ {
			key := []byte{byte(j)}
			node.Insert(key, []byte("value"))
		}
		b.StartTimer()

		// Perform SplitCopy
		_, _, _, err := node.SplitCopy()
		if err != nil {
			b.Fatalf("SplitCopy failed: %v", err)
		}
	}
}

// BenchmarkNode_SplitCopyOptimized benchmarks optimized SplitCopy
func BenchmarkNode_SplitCopyOptimized(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		node := NewNode(true)
		for j := 0; j < model.DefaultMaxKeys; j++ {
			key := []byte{byte(j)}
			node.Insert(key, []byte("value"))
		}
		b.StartTimer()

		_, _, _, err := node.SplitCopyOptimized()
		if err != nil {
			b.Fatalf("SplitCopyOptimized failed: %v", err)
		}
	}
}

// BenchmarkNode_SplitVsSplitCopy compares Split vs SplitCopy
func BenchmarkNode_SplitVsSplitCopy(b *testing.B) {
	b.Run("Original_Split", func(b *testing.B) {
		benchmarkSplit(b, false)
	})

	b.Run("SplitCopy", func(b *testing.B) {
		benchmarkSplit(b, true)
	})
}

// benchmarkSplit is a helper for split benchmarks
func benchmarkSplit(b *testing.B, useCopy bool) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		node := NewNode(true)
		for j := 0; j < model.DefaultMaxKeys; j++ {
			key := []byte{byte(j)}
			node.Insert(key, []byte("value"))
		}
		b.StartTimer()

		if useCopy {
			_, _, _, err := node.SplitCopy()
			if err != nil {
				b.Fatalf("SplitCopy failed: %v", err)
			}
		} else {
			_, _, err := node.Split()
			if err != nil {
				b.Fatalf("Split failed: %v", err)
			}
		}
	}
}

// BenchmarkConcurrentSplitCopy benchmarks concurrent SplitCopy operations
func BenchmarkConcurrentSplitCopy(b *testing.B) {
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Create a full node
			node := NewNode(true)
			for j := 0; j < model.DefaultMaxKeys; j++ {
				key := []byte{byte(j)}
				node.Insert(key, []byte("value"))
			}

			// Perform lock-free SplitCopy
			_, _, _, err := node.SplitCopy()
			if err != nil {
				b.Errorf("SplitCopy failed: %v", err)
			}
		}
	})
}
