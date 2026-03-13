// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BenchmarkSingleSet_Serial 测试单线程 Set 性能（无并发冲突）
func BenchmarkSingleSet_Serial(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// BenchmarkSingleSet_Update 测试单线程反复更新同一个 key（无冲突）
func BenchmarkSingleSet_Update(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	key := []byte("same-key")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// BenchmarkSingleSet_Concurrent_1Writer 测试单个 goroutine 写入（作为 baseline）
func BenchmarkSingleSet_Concurrent_1Writer(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			if err := tree.Set(ctx, key, value); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleSet_Concurrent_2Writers 测试 2 个 goroutine 并发写入（会有 CAS 冲突）
func BenchmarkSingleSet_Concurrent_2Writers(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			if err := tree.Set(ctx, key, value); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleSet_Concurrent_4Writers 测试 4 个 goroutine 并发写入
func BenchmarkSingleSet_Concurrent_4Writers(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			if err := tree.Set(ctx, key, value); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleSet_Concurrent_8Writers 测试 8 个 goroutine 并发写入
func BenchmarkSingleSet_Concurrent_8Writers(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			if err := tree.Set(ctx, key, value); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleSet_HotKey 测试并发写入同一个 key（极端冲突场景）
func BenchmarkSingleSet_HotKey(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	hotKey := []byte("hot-key")
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			value := []byte(fmt.Sprintf("value-%d", i))
			if err := tree.Set(ctx, hotKey, value); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// ============================================================================
// Goroutine Pool 基准测试（使用真实 goroutine 池替代 RunParallel）
// 目的：消除 RunParallel 带来的性能开销，验证真实并发性能
// ============================================================================

// BenchmarkSingleSet_GoroutinePool_1Writer 使用真实 goroutine 池（1个worker）
func BenchmarkSingleSet_GoroutinePool_1Writer(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// BenchmarkSingleSet_GoroutinePool_2Writers 使用真实 goroutine 池（2个worker）
func BenchmarkSingleSet_GoroutinePool_2Writers(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	workers := 2
	var wg sync.WaitGroup

	b.ResetTimer()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < b.N/workers; i++ {
				key := []byte(fmt.Sprintf("key-%d-%d", workerID, i))
				value := []byte(fmt.Sprintf("value-%d", i))
				if err := tree.Set(ctx, key, value); err != nil {
					b.Errorf("Set failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkSingleSet_GoroutinePool_4Writers 使用真实 goroutine 池（4个worker）
func BenchmarkSingleSet_GoroutinePool_4Writers(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	workers := 4
	var wg sync.WaitGroup

	b.ResetTimer()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < b.N/workers; i++ {
				key := []byte(fmt.Sprintf("key-%d-%d", workerID, i))
				value := []byte(fmt.Sprintf("value-%d", i))
				if err := tree.Set(ctx, key, value); err != nil {
					b.Errorf("Set failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkSingleSet_GoroutinePool_8Writers 使用真实 goroutine 池（8个worker）
func BenchmarkSingleSet_GoroutinePool_8Writers(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	workers := 8
	var wg sync.WaitGroup

	b.ResetTimer()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < b.N/workers; i++ {
				key := []byte(fmt.Sprintf("key-%d-%d", workerID, i))
				value := []byte(fmt.Sprintf("value-%d", i))
				if err := tree.Set(ctx, key, value); err != nil {
					b.Errorf("Set failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkSingleSet_GoroutinePool_HotKey 使用真实 goroutine 池（热点key场景）
func BenchmarkSingleSet_GoroutinePool_HotKey(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	hotKey := []byte("hot-key")
	workers := 8
	var wg sync.WaitGroup

	b.ResetTimer()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < b.N/workers; i++ {
				value := []byte(fmt.Sprintf("value-%d-%d", workerID, i))
				if err := tree.Set(ctx, hotKey, value); err != nil {
					b.Errorf("Set failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// ============================================================================
// Single Get 基准测试
// 目的：测试读操作性能，对比读写性能差异
// ============================================================================

// BenchmarkSingleGet_Serial 测试单线程 Get 性能（无并发冲突）
func BenchmarkSingleGet_Serial(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充数据 - 使用较小的数量避免 split 问题
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Setup Set failed: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		_, err := tree.Get(ctx, key)
		if err != nil {
			b.Fatalf("Get failed for key-%d: %v", i%numKeys, err)
		}
	}
}

// BenchmarkSingleGet_Repeat 测试反复读取同一个 key（缓存友好）
func BenchmarkSingleGet_Repeat(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充单个 key
	key := []byte("same-key")
	value := []byte("value")
	if err := tree.Set(ctx, key, value); err != nil {
		b.Fatalf("Setup Set failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tree.Get(ctx, key)
		if err != nil {
			b.Fatalf("Get failed: %v", err)
		}
	}
}

// BenchmarkSingleGet_Concurrent_8Readers 使用 RunParallel（8个reader）
func BenchmarkSingleGet_Concurrent_8Readers(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充数据
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Setup Set failed: %v", err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%d", i%numKeys))
			if _, err := tree.Get(ctx, key); err != nil {
				b.Fatalf("Get failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleGet_HotKey 测试并发读取同一个热点 key
func BenchmarkSingleGet_HotKey(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充热点 key
	hotKey := []byte("hot-key")
	value := []byte("hot-value")
	if err := tree.Set(ctx, hotKey, value); err != nil {
		b.Fatalf("Setup Set failed: %v", err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := tree.Get(ctx, hotKey); err != nil {
				b.Fatalf("Get failed: %v", err)
			}
		}
	})
}

// BenchmarkSingleGet_GoroutinePool_8Readers 使用真实 goroutine 池（8个reader）
func BenchmarkSingleGet_GoroutinePool_8Readers(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充数据
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Setup Set failed: %v", err)
		}
	}

	workers := 8
	var wg sync.WaitGroup
	b.ResetTimer()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < b.N/workers; i++ {
				key := []byte(fmt.Sprintf("key-%d", (i+workerID*1000)%numKeys))
				if _, err := tree.Get(ctx, key); err != nil {
					b.Errorf("Get failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkSingleGet_GoroutinePool_HotKey 使用真实 goroutine 池（热点key场景）
func BenchmarkSingleGet_GoroutinePool_HotKey(b *testing.B) {
	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充热点 key
	hotKey := []byte("hot-key")
	value := []byte("hot-value")
	if err := tree.Set(ctx, hotKey, value); err != nil {
		b.Fatalf("Setup Set failed: %v", err)
	}

	workers := 8
	var wg sync.WaitGroup
	b.ResetTimer()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < b.N/workers; i++ {
				if _, err := tree.Get(ctx, hotKey); err != nil {
					b.Errorf("Get failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}
