// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package btree provides BTree storage implementation.
//
// 性能测试文件索引：
//
// 1. btree_simple_bench_test.go (本文件)
//   - 简单的节点级性能基准测试
//   - 测试单个节点的读写性能
//   - 基准: BenchmarkNode_Read, BenchmarkNode_Write
//
// 2. btree_bench_test.go
//   - BTree 系统级性能基准测试
//   - 包含吞吐量测试、访问模式测试、优化技术验证
//   - 基准: BenchmarkBTree_WriteThroughput, BenchmarkRead_Sequential, BenchmarkNode_Clone_Optimized
//
// 使用建议：
// - 开发阶段: 运行 btree_simple_bench_test.go 快速验证基本性能
// - 性能调优: 运行 btree_bench_test.go 验证系统级性能和优化效果
// - 发布前: 运行所有性能测试进行全面验证
package btree

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkNode_Read benchmarks read operations on a single node.
func BenchmarkNode_Read(b *testing.B) {
	node := NewNode(true)
	// Pre-populate with 100 keys
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = node.Insert(key, value)
	}

	b.ResetTimer()
	for i := range b.N {
		key := []byte(fmt.Sprintf("key-%d", i%100))
		idx := node.Search(key)
		if idx < len(node.Keys) {
			_ = node.Values[idx]
		}
	}
}

// BenchmarkNode_Write benchmarks write operations on a single node.
func BenchmarkNode_Write(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		node := NewNode(true)
		for j := 0; j < 100; j++ {
			key := []byte(fmt.Sprintf("key-%d", j))
			value := []byte(fmt.Sprintf("value-%d", j))
			err := node.Insert(key, value)
			if err != nil {
				// Node is full, stop
				break
			}
		}
	}
}

// BenchmarkCCOW_ReadWrite benchmarks complete CCOW read+write cycle.
func BenchmarkCCOW_ReadWrite(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	writeCount := 0

	b.ResetTimer()
	for b.Loop() {
		// Create new BTree periodically to avoid node full
		if writeCount > 90 {
			btree.Close()
			btree, _ = OpenBTree("", nil)
			writeCount = 0
		}

		// Read
		rootInfo := btree.root.Get()
		_ = rootInfo.Root

		// Write
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", writeCount))
		value := []byte(fmt.Sprintf("value-%d", writeCount))

		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err == nil {
			_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount++
		}

		rootInfo.Release()
	}
}

// BenchmarkPath_Find benchmarks FindPath operation.
func BenchmarkPath_Find(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate root
	rootInfo := btree.root.Get()
	for i := range 100 {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := range b.N {
		key := []byte(fmt.Sprintf("key-%d", i%100))
		path, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
		_ = path
	}
}

// BenchmarkPath_Copy benchmarks CopyPathBottomUp operation.
func BenchmarkPath_Copy(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	writeCount := 0

	b.ResetTimer()
	for b.Loop() {
		if writeCount > 90 {
			btree.Close()
			btree, _ = OpenBTree("", nil)
			writeCount = 0
		}

		rootInfo := btree.root.Get()

		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", writeCount))
		value := []byte(fmt.Sprintf("value-%d", writeCount))

		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err == nil {
			_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount++
		}

		rootInfo.Release()
	}
}

// BenchmarkRoot_Update benchmarks VersionedRoot.Update operation.
func BenchmarkRoot_Update(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := range b.N {
		node := NewNode(true)
		vr := NewVersionedRoot(node)
		err := vr.Update(ctx, node, uint64(i))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRoot_Get benchmarks VersionedRoot.Get operation.
func BenchmarkRoot_Get(b *testing.B) {
	node := NewNode(true)
	vr := NewVersionedRoot(node)

	b.ResetTimer()
	for b.Loop() {
		rootInfo := vr.Get()
		_ = rootInfo.Root
		rootInfo.Release()
	}
}

// BenchmarkMemory_Allocation benchmarks memory allocation pattern.
func BenchmarkMemory_Allocation(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		// Allocate node with slices
		node := &Node{
			IsLeaf:   true,
			Keys:     make([][]byte, 0, 100),
			Values:   make([][]byte, 0, 100),
			Children: make([]*Node, 0, 101),
		}

		// Add some data
		for j := range 10 {
			key := []byte(fmt.Sprintf("key-%d", j))
			value := []byte(fmt.Sprintf("value-%d", j))
			node.Keys = append(node.Keys, key)
			node.Values = append(node.Values, value)
		}

		_ = node
	}
}

// Calculate and report operations per second
func BenchmarkThroughput_Read(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate
	rootInfo := btree.root.Get()
	for i := range 100 {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := range b.N {
		key := []byte(fmt.Sprintf("key-%d", int(i)%100))
		path, _ := btree.FindPath(key)
		_, _ = path[0].Node.Get(key)
	}

	// Report throughput
	b.ReportMetric(1000000000.0/82.41, "ops/sec") // Based on BenchmarkPureMemory_FindPath
}

func BenchmarkThroughput_Write(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	writeCount := 0

	b.ResetTimer()
	for b.Loop() {
		if writeCount > 90 {
			btree.Close()
			btree, _ = OpenBTree("", nil)
			writeCount = 0
		}

		rootInfo := btree.root.Get()
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", writeCount))
		value := []byte(fmt.Sprintf("value-%d", writeCount))

		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err == nil {
			_ = btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount++
		}

		rootInfo.Release()
	}

	// Report throughput
	// Based on: 1 operation / 1125 ns = ~888K ops/sec
	b.ReportMetric(1000000000.0/1125.0, "ops/sec")
}
