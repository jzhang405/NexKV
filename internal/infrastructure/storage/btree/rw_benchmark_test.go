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

// BenchmarkRead_Throughput benchmarks read throughput (QPS).
func BenchmarkRead_Throughput(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Setup: Insert 10000 keys
	rootInfo := btree.root.Get()
	for i := 0; i < 10000; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	b.ReportAllocs()

	ops := int64(0)
	for i := 0; i < b.N; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		path, _ := btree.FindPath(key)
		if len(path) > 0 {
			ops++
			ReleasePath(path)
		}
	}

	// Report QPS
	b.ReportMetric(float64(ops)/float64(b.N), "ops/op")
}

// BenchmarkRead_Latency benchmarks read latency (nanoseconds per operation).
func BenchmarkRead_Latency(b *testing.B) {
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Setup: Insert 10000 keys
	rootInfo := btree.root.Get()
	for i := 0; i < 10000; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	// Pick a middle key
	testKey := []byte{127, 39}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, _ := btree.FindPath(testKey)
		if len(path) > 0 {
			ReleasePath(path)
		}
	}
}

// BenchmarkWrite_Single benchmarks single key write (CCOW).
func BenchmarkWrite_Single(b *testing.B) {
	ctx := context.Background()
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte(fmt.Sprintf("value-%d", i))

		path, _ := btree.FindPath(key)
		if len(path) > 0 {
			newRoot, _ := btree.CopyPathBottomUp(ctx, path, func(node *Node) error {
				return node.Insert(key, value)
			})
			if newRoot != nil {
				rootInfo := btree.root.Get()
				rootInfo.Root = newRoot
				rootInfo.Release()
			}
			ReleasePath(path)
		}
	}
}

// BenchmarkWrite_Update benchmarks update existing keys.
func BenchmarkWrite_Update(b *testing.B) {
	ctx := context.Background()
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Setup: Insert 1000 keys
	rootInfo := btree.root.Get()
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i)}
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte{byte(i % 1000)}
		value := []byte(fmt.Sprintf("updated-%d", i))

		path, _ := btree.FindPath(key)
		if len(path) > 0 {
			newRoot, _ := btree.CopyPathBottomUp(ctx, path, func(node *Node) error {
				return node.Insert(key, value)
			})
			if newRoot != nil {
				rootInfo := btree.root.Get()
				rootInfo.Root = newRoot
				rootInfo.Release()
			}
			ReleasePath(path)
		}
	}
}

// BenchmarkMixed_ReadWrite_90_10 benchmarks 90% read, 10% write workload.
func BenchmarkMixed_ReadWrite_90_10(b *testing.B) {
	ctx := context.Background()
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Setup
	rootInfo := btree.root.Get()
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i)}
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	b.ReportAllocs()

	readOps := int64(0)
	writeOps := int64(0)

	for i := 0; i < b.N; i++ {
		if i%10 < 9 {
			// Read (90%)
			key := []byte{byte(i % 1000)}
			path, _ := btree.FindPath(key)
			if len(path) > 0 {
				readOps++
				ReleasePath(path)
			}
		} else {
			// Write (10%)
			key := []byte{byte(i % 1000)}
			value := []byte(fmt.Sprintf("value-%d", i))

			path, _ := btree.FindPath(key)
			if len(path) > 0 {
				newRoot, _ := btree.CopyPathBottomUp(ctx, path, func(node *Node) error {
					return node.Insert(key, value)
				})
				if newRoot != nil {
					rootInfo := btree.root.Get()
					rootInfo.Root = newRoot
					rootInfo.Release()
					writeOps++
				}
				ReleasePath(path)
			}
		}
	}

	b.ReportMetric(float64(readOps)/float64(b.N), "read/op")
	b.ReportMetric(float64(writeOps)/float64(b.N), "write/op")
}

// BenchmarkMixed_ReadWrite_50_50 benchmarks 50% read, 50% write workload.
func BenchmarkMixed_ReadWrite_50_50(b *testing.B) {
	ctx := context.Background()
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Setup
	rootInfo := btree.root.Get()
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i)}
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	b.ReportAllocs()

	readOps := int64(0)
	writeOps := int64(0)

	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			// Read (50%)
			key := []byte{byte(i % 1000)}
			path, _ := btree.FindPath(key)
			if len(path) > 0 {
				readOps++
				ReleasePath(path)
			}
		} else {
			// Write (50%)
			key := []byte{byte(i % 1000)}
			value := []byte(fmt.Sprintf("value-%d", i))

			path, _ := btree.FindPath(key)
			if len(path) > 0 {
				newRoot, _ := btree.CopyPathBottomUp(ctx, path, func(node *Node) error {
					return node.Insert(key, value)
				})
				if newRoot != nil {
					rootInfo := btree.root.Get()
					rootInfo.Root = newRoot
					rootInfo.Release()
					writeOps++
				}
				ReleasePath(path)
			}
		}
	}

	b.ReportMetric(float64(readOps)/float64(b.N), "read/op")
	b.ReportMetric(float64(writeOps)/float64(b.N), "write/op")
}

// BenchmarkRealWorld_Workload benchmarks realistic read/write workload.
func BenchmarkRealWorld_Workload(b *testing.B) {
	ctx := context.Background()
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	// Setup: Insert initial data (hot set)
	rootInfo := btree.root.Get()
	for i := 0; i < 10000; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	b.ReportAllocs()

	hits := int64(0)
	misses := int64(0)

	for i := 0; i < b.N; i++ {
		// Simulate real workload: 95% reads, 5% writes
		// 80% of reads hit hot set (first 1000 keys)
		if i%100 < 95 {
			// Read
			var key []byte
			if i%10 < 8 {
				// Hot set (80%)
				key = []byte{byte(i % 256), byte((i / 256) % 256)}
			} else {
				// Cold set (20%)
				key = []byte{byte((i + 5000) & 0xff), byte(((i + 5000) >> 8) & 0xff)}
			}

			path, _ := btree.FindPath(key)
			if len(path) > 0 {
				hits++
				ReleasePath(path)
			} else {
				misses++
			}
		} else {
			// Write (mostly to hot set)
			key := []byte{byte(i % 256), byte((i / 256) % 256)}
			value := []byte(fmt.Sprintf("value-%d", i))

			path, _ := btree.FindPath(key)
			if len(path) > 0 {
				newRoot, _ := btree.CopyPathBottomUp(ctx, path, func(node *Node) error {
					return node.Insert(key, value)
				})
				if newRoot != nil {
					rootInfo := btree.root.Get()
					rootInfo.Root = newRoot
					rootInfo.Release()
				}
				ReleasePath(path)
			}
		}
	}

	b.ReportMetric(float64(hits)/float64(b.N), "hits/op")
	b.ReportMetric(float64(misses)/float64(b.N), "misses/op")
	hitRate := float64(hits) / float64(hits+misses) * 100
	b.ReportMetric(hitRate, "hit_rate%")
}
