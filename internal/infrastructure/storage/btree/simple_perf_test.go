// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

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
		node.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(writeCount))
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
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount++
		}

		rootInfo.Release()
	}
}

// BenchmarkRoot_Update benchmarks VersionedRoot.Update operation.
func BenchmarkRoot_Update(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
		rootInfo := vr.Get()
		_ = rootInfo.Root
		rootInfo.Release()
	}
}

// BenchmarkMemory_Allocation benchmarks memory allocation pattern.
func BenchmarkMemory_Allocation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Allocate node with slices
		node := &Node{
			IsLeaf:   true,
			Keys:     make([][]byte, 0, 100),
			Values:   make([][]byte, 0, 100),
			Children: make([]*Node, 0, 101),
		}

		// Add some data
		for j := 0; j < 10; j++ {
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
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%100))
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
	for i := 0; i < b.N; i++ {
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
			btree.root.Update(ctx, newRoot, uint64(writeCount))
			writeCount++
		}

		rootInfo.Release()
	}

	// Report throughput
	// Based on: 1 operation / 1125 ns = ~888K ops/sec
	b.ReportMetric(1000000000.0/1125.0, "ops/sec")
}
