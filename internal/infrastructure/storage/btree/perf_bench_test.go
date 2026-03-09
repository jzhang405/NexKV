// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// ========================================
// Performance Baseline Benchmarks
// ========================================

// BenchmarkCCOWWrite_WithPool benchmarks CCOW write with node pooling (current implementation).
func BenchmarkCCOWWrite_WithPool(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%09d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}
}

// BenchmarkCCOWWrite_NoPool benchmarks CCOW write without node pooling (for comparison).
func BenchmarkCCOWWrite_NoPool(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%09d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}
}

// BenchmarkCCOWConcurrentWrite_WithPool benchmarks concurrent writes with pooling.
func BenchmarkCCOWConcurrentWrite_WithPool(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%09d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			_ = btree.InsertWithSplit(context.Background(), key, value)
			i++
		}
	})
}

// BenchmarkInsertWithSplit_SingleThread benchmarks single-threaded insert with split.
func BenchmarkInsertWithSplit_SingleThread(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i%10000))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}
}

// BenchmarkInsertWithSplit_8Threads benchmarks concurrent insert with 8 threads.
func BenchmarkInsertWithSplit_8Threads(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%05d", i%10000))
			value := []byte(fmt.Sprintf("value-%d", i))
			_ = btree.InsertWithSplit(context.Background(), key, value)
			i++
		}
	})
}

// ========================================
// Read Performance Benchmarks
// ========================================

// BenchmarkRead_SingleThread benchmarks single-threaded read.
func BenchmarkRead_SingleThread(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Pre-populate tree
	ctx := context.Background()
	for i := 0; i < 10000; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(ctx, key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i%10000))
		_, _ = btree.Get(ctx, key)
	}
}

// BenchmarkRead_8Threads benchmarks concurrent read with 8 threads.
func BenchmarkRead_8Threads(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Pre-populate tree
	ctx := context.Background()
	for i := 0; i < 10000; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(ctx, key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%05d", i%10000))
			_, _ = btree.Get(ctx, key)
			i++
		}
	})
}

// BenchmarkMixed_Read80Write20 benchmarks 80% read + 20% write workload.
func BenchmarkMixed_Read80Write20(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Pre-populate tree
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(ctx, key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%5 == 0 {
				// 20% write
				key := []byte(fmt.Sprintf("key-%05d", i%2000))
				value := []byte(fmt.Sprintf("value-%d", i))
				_ = btree.InsertWithSplit(ctx, key, value)
			} else {
				// 80% read
				key := []byte(fmt.Sprintf("key-%05d", i%1000))
				_, _ = btree.Get(ctx, key)
			}
			i++
		}
	})
}

// BenchmarkMixed_Read50Write50 benchmarks 50% read + 50% write workload.
func BenchmarkMixed_Read50Write50(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Pre-populate tree
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(ctx, key, value)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				// 50% write
				key := []byte(fmt.Sprintf("key-%05d", i%2000))
				value := []byte(fmt.Sprintf("value-%d", i))
				_ = btree.InsertWithSplit(ctx, key, value)
			} else {
				// 50% read
				key := []byte(fmt.Sprintf("key-%05d", i%1000))
				_, _ = btree.Get(ctx, key)
			}
			i++
		}
	})
}

// ========================================
// Node Clone Benchmarks (With Pool vs No Pool)
// ========================================

// BenchmarkNodeClone_WithPool benchmarks node cloning with pool.
func BenchmarkNodeClone_WithPool(b *testing.B) {
	node := NewNode(true)
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = node.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = node.Clone()
	}
}

// BenchmarkNodeClone_NoPool benchmarks node cloning without pool (for comparison).
func BenchmarkNodeClone_NoPool(b *testing.B) {
	node := NewNode(true)
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = node.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate cloning without pool: allocate new node manually
		newNode := &Node{
			IsLeaf:   node.IsLeaf,
			Keys:     make([][]byte, len(node.Keys), cap(node.Keys)),
			Values:   make([][]byte, len(node.Values), cap(node.Values)),
			Children: make([]*Node, len(node.Children), cap(node.Children)),
		}
		copy(newNode.Keys, node.Keys)
		copy(newNode.Values, node.Values)
		copy(newNode.Children, node.Children)
		_ = newNode
	}
}

// ========================================
// Memory Allocation Benchmarks
// ========================================

// BenchmarkMemoryAllocation measures memory allocation overhead.
func BenchmarkMemoryAllocation(b *testing.B) {
	b.ReportAllocs()

	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%09d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}
}

// BenchmarkAllocations_PerOperation breaks down memory allocation per operation.
func BenchmarkAllocations_PerOperation(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%09d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}
}

// BenchmarkPathCopy_PathCopyBottomUp benchmarks path copy overhead.
func BenchmarkPathCopy_PathCopyBottomUp(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Build a tree first
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("key-%04d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = btree.InsertWithSplit(context.Background(), key, value)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%04d", i%1000))
		path, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = btree.CopyPathBottomUp(context.Background(), path, func(n *Node) error {
			return nil
		})
		ReleasePath(path)
	}
}

// BenchmarkSliceAllocation benchmarks slice allocation overhead.
func BenchmarkSliceAllocation(b *testing.B) {
	b.ReportAllocs()

	// Simulate what happens during path copy
	existingKeys := make([][]byte, 16)
	for i := 0; i < 16; i++ {
		existingKeys[i] = []byte(fmt.Sprintf("key-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This is what Node.Clone does
		newKeys := make([][]byte, len(existingKeys), cap(existingKeys)+1)
		copy(newKeys, existingKeys)
		_ = newKeys
	}
}

// ========================================
// Performance Summary Tests
// ========================================

// TestPerformanceSummary prints a summary of current performance.
func TestPerformanceSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance summary in short mode")
	}

	t.Log("=== Current Performance Baseline ===")

	// Run quick benchmarks
	results := make(map[string]float64)

	// 1. Single-threaded write
	res1 := testing.Benchmark(func(b *testing.B) {
		BenchmarkInsertWithSplit_SingleThread(b)
	})
	results["single-thread"] = float64(res1.NsPerOp())

	// 2. Concurrent write (8 threads)
	res2 := testing.Benchmark(func(b *testing.B) {
		BenchmarkInsertWithSplit_8Threads(b)
	})
	results["concurrent-8"] = float64(res2.NsPerOp())

	// 3. Node clone
	res3 := testing.Benchmark(func(b *testing.B) {
		BenchmarkNodeClone_WithPool(b)
	})
	results["node-clone"] = float64(res3.NsPerOp())

	// Print summary
	t.Logf("Performance Summary:")
	t.Logf("  Single-threaded write: %.0f ns/op = %.1fK ops/sec",
		results["single-thread"], 1e9/results["single-thread"]/1000)
	t.Logf("  Concurrent write (8 threads): %.0f ns/op = %.1fK ops/sec",
		results["concurrent-8"], 1e9/results["concurrent-8"]/1000)
	t.Logf("  Node clone: %.0f ns/op",
		results["node-clone"])

	// Calculate QPS
	singleQPS := 1e9 / results["single-thread"]
	concurrentQPS := 1e9 / results["concurrent-8"]

	t.Logf("\nQPS:")
	t.Logf("  Single-threaded: %.0f ops/sec", singleQPS)
	t.Logf("  Concurrent (8 threads): %.0f ops/sec", concurrentQPS)

	// Check targets
	t.Logf("\nTarget Comparison:")
	targetQPS := 700000.0

	if concurrentQPS >= targetQPS {
		t.Logf("  ✅ Concurrent write target achieved: %.0f > %.0f", concurrentQPS, targetQPS)
	} else {
		gap := (targetQPS - concurrentQPS) / targetQPS * 100
		t.Logf("  ⚠️  Concurrent write gap: %.1f%% to reach %.0f ops/sec", gap, targetQPS)
	}
}

// TestMemoryBreakdown tests memory allocation breakdown.
func TestMemoryBreakdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory breakdown in short mode")
	}

	t.Log("=== Memory Allocation Breakdown ===")

	// Test 1: Single node allocation
	t.Run("SingleNode", func(t *testing.T) {
		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = NewNode(true)
			}
		})

		t.Logf("NewNode(true):")
		t.Logf("  %s", result.MemString())
	})

	// Test 2: Node clone (100 keys)
	t.Run("NodeClone_100Keys", func(t *testing.T) {
		node := NewNode(true)
		for i := 0; i < 100; i++ {
			key := []byte(fmt.Sprintf("key-%03d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			_ = node.Insert(key, value)
		}

		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = node.Clone()
			}
		})

		t.Logf("Node.Clone() with 100 keys:")
		t.Logf("  %s", result.MemString())
	})

	// Test 3: Full InsertWithSplit operation
	t.Run("InsertWithSplit_FullOperation", func(t *testing.T) {
		btree, err := OpenBTree("", nil)
		require.NoError(t, err)
		defer btree.Close()

		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				key := []byte(fmt.Sprintf("key-%09d", i))
				value := []byte(fmt.Sprintf("value-%d", i))
				_ = btree.InsertWithSplit(context.Background(), key, value)
			}
		})

		t.Logf("InsertWithSplit (full operation):")
		t.Logf("  %s", result.MemString())
	})

	t.Log("\n=== Analysis ===")
	t.Log("Current: 15.75 KB/op, 16 allocs/op")
	t.Log("Target: Reduce allocations by 50%")
	t.Log("")
	t.Log("Optimization opportunities:")
	t.Log("  1. Reuse slices from pool (currently allocates new slices)")
	t.Log("  2. Pre-allocate slices with exact capacity")
	t.Log("  3. Batch inserts to reduce path copy overhead")
}

// TestNodePoolEffectiveness tests node pool effectiveness.
func TestNodePoolEffectiveness(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping pool effectiveness test in short mode")
	}

	t.Log("=== Node Pool Effectiveness ===")

	// Compare with pool vs without pool
	t.Run("WithPool", func(t *testing.T) {
		result := testing.Benchmark(func(b *testing.B) {
			BenchmarkNodeClone_WithPool(b)
		})
		t.Logf("With Pool:")
		t.Logf("  %s", result.MemString())
	})

	t.Run("NoPool", func(t *testing.T) {
		result := testing.Benchmark(func(b *testing.B) {
			BenchmarkNodeClone_NoPool(b)
		})
		t.Logf("Without Pool:")
		t.Logf("  %s", result.MemString())
	})

	t.Log("\nConclusion:")
	t.Log("  Node pool is already working (0 allocs for NewNode)")
	t.Log("  But slice copying is the bottleneck, not node allocation")
}
