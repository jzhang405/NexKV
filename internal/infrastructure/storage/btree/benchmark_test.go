// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this license is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// BenchmarkBTree_Get_Single benchmarks single-threaded Get operations
func BenchmarkBTree_Get_Single(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)
	key := []byte("key-500")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tree.Get(context.Background(), key)
	}
}

// BenchmarkBTree_Get_Concurrent benchmarks concurrent Get operations
func BenchmarkBTree_Get_Concurrent(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)
	key := []byte("key-500")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = tree.Get(context.Background(), key)
		}
	})
}

// BenchmarkBTree_Set_Single benchmarks single-threaded Set operations
func BenchmarkBTree_Set_Single(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%1000))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = tree.Set(context.Background(), key, value)
	}
}

// BenchmarkBTree_Set_Concurrent benchmarks concurrent Set operations
func BenchmarkBTree_Set_Concurrent(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%d", counter%1000))
			value := []byte(fmt.Sprintf("value-%d", counter))
			_ = tree.Set(context.Background(), key, value)
			counter++
		}
	})
}

// BenchmarkBTree_Delete_Single benchmarks single-threaded Delete operations
func BenchmarkBTree_Delete_Single(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%1000))
		_ = tree.Delete(context.Background(), key)
		// Re-insert to keep tree size
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = tree.Set(context.Background(), key, value)
	}
}

// BenchmarkBTree_SearchPath benchmarks search path operations
func BenchmarkBTree_SearchPath(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)
	ctx := context.Background()
	key := []byte("key-500")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tree.searchPath(ctx, key)
	}
}

// BenchmarkBTree_PageRef_GetPage benchmarks PageRef.GetPage operation
func BenchmarkBTree_PageRef_GetPage(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	// Get root page info
	rootInfo := tree.rootRef.GetPageInfo()
	if rootInfo == nil {
		b.Fatal("root is nil")
	}

	// Create a PageRef for testing
	ref := NewPageRefWithInfo(rootInfo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ref.GetPage()
	}
}

// BenchmarkBTree_PageInfo_Touch benchmarks PageInfo.Touch operation
func BenchmarkBTree_PageInfo_Touch(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	// Get root page info
	rootInfo := tree.rootRef.GetPageInfo()
	if rootInfo == nil {
		b.Fatal("root is nil")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rootInfo.Touch()
	}
}

// setupBenchmarkTree creates a benchmark tree with the specified number of keys
func setupBenchmarkTree(b testing.TB, size int) *BTree {
	b.Helper()

	tempDir := b.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(b, err)

	// Insert keys
	ctx := context.Background()
	for i := 0; i < size; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		if err != nil {
			b.Fatalf("failed to insert key-%d: %v", i, err)
		}
	}

	return tree
}

// BenchmarkBTree_MixedWorkload benchmarks mixed read/write workload
func BenchmarkBTree_MixedWorkload(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		for pb.Next() {
			if counter%10 == 0 {
				// 10% writes
				key := []byte(fmt.Sprintf("key-%d", counter%1000))
				value := []byte(fmt.Sprintf("value-%d", counter))
				_ = tree.Set(ctx, key, value)
			} else {
				// 90% reads
				key := []byte(fmt.Sprintf("key-%d", counter%1000))
				_, _ = tree.Get(ctx, key)
			}
			counter++
		}
	})
}

// BenchmarkBTree_RandomAccess benchmarks random key access
func BenchmarkBTree_RandomAccess(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	ctx := context.Background()
	keys := make([][]byte, 1000)
	for i := 0; i < 1000; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := keys[i%1000]
		_, _ = tree.Get(ctx, key)
	}
}

// BenchmarkBTree_SequentialScan benchmarks sequential key scan
func BenchmarkBTree_SequentialScan(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%1000))
		_, _ = tree.Get(ctx, key)
	}
}

// BenchmarkBTree_ConcurrentReaders benchmarks multiple concurrent readers
func BenchmarkBTree_ConcurrentReaders(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)
	key := []byte("key-500")

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for g := 0; g < 100; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					_, _ = tree.Get(ctx, key)
				}
			}()
		}
		wg.Wait()
	}
}

// BenchmarkBTree_ConcurrentWriters benchmarks multiple concurrent writers
func BenchmarkBTree_ConcurrentWriters(b *testing.B) {
	tree := setupBenchmarkTree(b, 1000)

	ctx := context.Background()
	counter := 0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for g := 0; g < 10; g++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					key := []byte(fmt.Sprintf("key-%d", (counter*10+id)%1000))
					value := []byte(fmt.Sprintf("value-%d", counter*10+id+j))
					_ = tree.Set(ctx, key, value)
				}
			}(g)
		}
		wg.Wait()
		counter++
	}
}
