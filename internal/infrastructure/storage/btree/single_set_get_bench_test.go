// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// init 设置 GC 配置为性能优化模式
// 注意：这会影响整个进程的 GC 行为
func init() {
	// 设置 GOGC=400 优化 GC 触发频率，提升性能
	// 在生产环境中应通过 BTreeConfig.GCPercent 配置
	debug.SetGCPercent(400)
}

// ============================================================================
// 预分配 key/value 池 - 避免 fmt.Sprintf 开销
// 目的：与 Lealone 进行公平的性能比较
// ============================================================================

// 预分配的 key/value 池（避免测试中的格式化开销）
var (
	preallocatedKeys   [][]byte
	preallocatedValues [][]byte
	preallocMu         sync.Mutex
	preallocInitCount  int // 当前已初始化的数量
)

// initPreallocated 初始化预分配的 key/value 池（支持动态扩展）
func initPreallocated(count int) {
	preallocMu.Lock()
	defer preallocMu.Unlock()

	// 如果已初始化的数量足够，直接返回
	if preallocInitCount >= count {
		return
	}

	// 扩展切片
	preallocatedKeys = make([][]byte, count)
	preallocatedValues = make([][]byte, count)

	// 预生成所有 key 和 value
	for i := 0; i < count; i++ {
		// key: k-{i} 格式（使用固定宽度确保字典序正确）
		// ✅ 修复：使用 %07d 格式，确保 k-0000001 < k-0000002 < ... < k-0009999 < k-0010000
		preallocatedKeys[i] = []byte(fmt.Sprintf("k-%07d", i))

		// value: v-{i} 格式
		preallocatedValues[i] = []byte(fmt.Sprintf("v-%07d", i))
	}

	preallocInitCount = count
}

const maxPreallocCount = 1000000 // 预分配 100 万个 key/value

// ============================================================================
// Single Set 基准测试
// ============================================================================

// BenchmarkSingleSet_Serial 测试单线程 Set 性能（无并发冲突）
func BenchmarkSingleSet_Serial(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % maxPreallocCount
		if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// BenchmarkSingleSet_Update 测试单线程反复更新同一个 key（无冲突）
func BenchmarkSingleSet_Update(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	key := []byte("same-key")
	value := preallocatedValues[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// BenchmarkSingleSet_Concurrent_1Writer 测试单个 goroutine 写入（作为 baseline）
func BenchmarkSingleSet_Concurrent_1Writer(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
			idx := i % maxPreallocCount
			if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleSet_Concurrent_2Writers 测试 2 个 goroutine 并发写入（会有 CAS 冲突）
func BenchmarkSingleSet_Concurrent_2Writers(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
			idx := i % maxPreallocCount
			if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleSet_Concurrent_4Writers 测试 4 个 goroutine 并发写入
func BenchmarkSingleSet_Concurrent_4Writers(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
			idx := i % maxPreallocCount
			if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleSet_Concurrent_8Writers 测试 8 个 goroutine 并发写入
func BenchmarkSingleSet_Concurrent_8Writers(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
			idx := i % maxPreallocCount
			if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleSet_HotKey 测试并发写入同一个 key（极端冲突场景）
func BenchmarkSingleSet_HotKey(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
			idx := i % maxPreallocCount
			if err := tree.Set(ctx, hotKey, preallocatedValues[idx]); err != nil {
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
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % maxPreallocCount
		if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// BenchmarkSingleSet_GoroutinePool_2Writers 使用真实 goroutine 池（2个worker）
func BenchmarkSingleSet_GoroutinePool_2Writers(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
				idx := (workerID*100000 + i) % maxPreallocCount
				if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[i%maxPreallocCount]); err != nil {
					b.Errorf("Set failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkSingleSet_GoroutinePool_4Writers 使用真实 goroutine 池（4个worker）
func BenchmarkSingleSet_GoroutinePool_4Writers(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
				idx := (workerID*100000 + i) % maxPreallocCount
				if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[i%maxPreallocCount]); err != nil {
					b.Errorf("Set failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkSingleSet_GoroutinePool_8Writers 使用真实 goroutine 池（8个worker）
func BenchmarkSingleSet_GoroutinePool_8Writers(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
				idx := (workerID*100000 + i) % maxPreallocCount
				if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[i%maxPreallocCount]); err != nil {
					b.Errorf("Set failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkSingleSet_GoroutinePool_HotKey 使用真实 goroutine 池（热点key场景）
func BenchmarkSingleSet_GoroutinePool_HotKey(b *testing.B) {
	initPreallocated(maxPreallocCount)

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
				idx := (workerID*100000 + i) % maxPreallocCount
				if err := tree.Set(ctx, hotKey, preallocatedValues[idx]); err != nil {
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
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充数据 - 使用较小的数量避免 split 问题
	const preloadCount = 100
	for i := 0; i < preloadCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed at i=%d: %v", i, err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % preloadCount
		_, err := tree.Get(ctx, preallocatedKeys[idx])
		if err != nil {
			b.Fatalf("Get failed for idx=%d: %v", idx, err)
		}
	}
}

// BenchmarkSingleGet_Repeat 测试反复读取同一个 key（缓存友好）
func BenchmarkSingleGet_Repeat(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充单个 key
	key := preallocatedKeys[0]
	value := preallocatedValues[0]
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
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充数据
	const preloadCount = 100
	for i := 0; i < preloadCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed: %v", err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % preloadCount
			if _, err := tree.Get(ctx, preallocatedKeys[idx]); err != nil {
				b.Fatalf("Get failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkSingleGet_HotKey 测试并发读取同一个热点 key
func BenchmarkSingleGet_HotKey(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充热点 key
	hotKey := preallocatedKeys[0]
	hotValue := preallocatedValues[0]
	if err := tree.Set(ctx, hotKey, hotValue); err != nil {
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
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充数据
	const preloadCount = 100
	for i := 0; i < preloadCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
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
				idx := (i + workerID*1000) % preloadCount
				if _, err := tree.Get(ctx, preallocatedKeys[idx]); err != nil {
					b.Errorf("Get failed: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkSingleGet_GoroutinePool_HotKey 使用真实 goroutine 池（热点key场景）
func BenchmarkSingleGet_GoroutinePool_HotKey(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充热点 key
	hotKey := preallocatedKeys[0]
	hotValue := preallocatedValues[0]
	if err := tree.Set(ctx, hotKey, hotValue); err != nil {
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

// ============================================================================
// 1M Keys 完整读写基准测试
// 目的：测试真实场景下的大规模读写性能（内存模式）
// ============================================================================

// Benchmark1MKeys_Set 测试基于 1M 已有键的 Set 更新性能
func Benchmark1MKeys_Set(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充 1M 键（模拟生产环境数据规模）
	b.StopTimer()
	for i := 0; i < maxPreallocCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed at i=%d: %v", i, err)
		}
	}
	b.StartTimer()

	// 更新已有键（触发 Copy-on-Write）
	for i := 0; i < b.N; i++ {
		idx := i % maxPreallocCount
		if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}

// Benchmark1MKeys_Get_Serial 测试单线程从 1M 键中读取
func Benchmark1MKeys_Get_Serial(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充 1M 键
	b.StopTimer()
	for i := 0; i < maxPreallocCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed at i=%d: %v", i, err)
		}
	}
	b.StartTimer()

	// 从 1M 键中随机读取
	for i := 0; i < b.N; i++ {
		idx := i % maxPreallocCount
		if _, err := tree.Get(ctx, preallocatedKeys[idx]); err != nil {
			b.Fatalf("Get failed: %v", err)
		}
	}
}

// Benchmark1MKeys_Get_Concurrent_4Readers 测试并发从 1M 键中读取（4个reader）
func Benchmark1MKeys_Get_Concurrent_4Readers(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充 1M 键
	b.StopTimer()
	for i := 0; i < maxPreallocCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed at i=%d: %v", i, err)
		}
	}
	b.StartTimer()

	// 并发读取
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % maxPreallocCount
			if _, err := tree.Get(ctx, preallocatedKeys[idx]); err != nil {
				b.Fatalf("Get failed: %v", err)
			}
			i++
		}
	})
}

// Benchmark1MKeys_Get_Concurrent_8Readers 测试并发从 1M 键中读取（8个reader）
func Benchmark1MKeys_Get_Concurrent_8Readers(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充 1M 键
	b.StopTimer()
	for i := 0; i < maxPreallocCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed at i=%d: %v", i, err)
		}
	}
	b.StartTimer()

	// 并发读取
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			idx := i % maxPreallocCount
			if _, err := tree.Get(ctx, preallocatedKeys[idx]); err != nil {
				b.Fatalf("Get failed: %v", err)
			}
			i++
		}
	})
}

// Benchmark1MKeys_Mixed_50Read50Write 测试混合读写（50% Get + 50% Set）
func Benchmark1MKeys_Mixed_50Read50Write(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充 1M 键
	b.StopTimer()
	for i := 0; i < maxPreallocCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed at i=%d: %v", i, err)
		}
	}
	b.StartTimer()

	// 混合读写：偶数索引读，奇数索引写
	for i := 0; i < b.N; i++ {
		idx := i % maxPreallocCount
		if i%2 == 0 {
			// 读操作
			if _, err := tree.Get(ctx, preallocatedKeys[idx]); err != nil {
				b.Fatalf("Get failed: %v", err)
			}
		} else {
			// 写操作（更新已有键）
			if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
		}
	}
}

// Benchmark1MKeys_Mixed_80Read20Write 测试读多写少场景（80% Get + 20% Set）
func Benchmark1MKeys_Mixed_80Read20Write(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充 1M 键
	b.StopTimer()
	for i := 0; i < maxPreallocCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed at i=%d: %v", i, err)
		}
	}
	b.StartTimer()

	// 混合读写：80% 读，20% 写
	for i := 0; i < b.N; i++ {
		idx := i % maxPreallocCount
		if i%5 < 4 {
			// 读操作（80%）
			if _, err := tree.Get(ctx, preallocatedKeys[idx]); err != nil {
				b.Fatalf("Get failed: %v", err)
			}
		} else {
			// 写操作（20%）
			if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
				b.Fatalf("Set failed: %v", err)
			}
		}
	}
}

// Benchmark1MKeys_Mixed_Concurrent 测试并发混合读写（4 readers + 4 writers）
func Benchmark1MKeys_Mixed_Concurrent(b *testing.B) {
	initPreallocated(maxPreallocCount)

	ctx := context.Background()
	tmpDir := ""

	tree, err := OpenBTree(tmpDir, &model.BTreeConfig{})
	if err != nil {
		b.Fatalf("Failed to open BTree: %v", err)
	}
	defer tree.Close()

	// 预填充 1M 键
	b.StopTimer()
	for i := 0; i < maxPreallocCount; i++ {
		if err := tree.Set(ctx, preallocatedKeys[i], preallocatedValues[i]); err != nil {
			b.Fatalf("Setup Set failed at i=%d: %v", i, err)
		}
	}
	b.StartTimer()

	// 4 个 reader + 4 个 writer
	var wg sync.WaitGroup
	workers := 8

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			isReader := (workerID % 2) == 0
			for i := 0; i < b.N/workers; i++ {
				idx := (workerID*100000 + i) % maxPreallocCount
				if isReader {
					// 读操作
					if _, err := tree.Get(ctx, preallocatedKeys[idx]); err != nil {
						b.Errorf("Get failed: %v", err)
					}
				} else {
					// 写操作
					if err := tree.Set(ctx, preallocatedKeys[idx], preallocatedValues[idx]); err != nil {
						b.Errorf("Set failed: %v", err)
					}
				}
			}
		}(w)
	}
	wg.Wait()
}
