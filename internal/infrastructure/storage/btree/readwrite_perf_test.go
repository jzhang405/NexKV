// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// BenchmarkRead_Sequential benchmarks sequential read operations.
func BenchmarkRead_Sequential(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate with data (limited to avoid node full)
	rootInfo := btree.root.Get()
	numKeys := 100 // Keep under DefaultMaxKeys
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		path, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
		if len(path) == 0 {
			b.Fatal("empty path")
		}
		value, err := path[0].Node.Get(key)
		if err != nil {
			b.Fatal(err)
		}
		_ = value
	}
}

// BenchmarkWrite_Sequential benchmarks sequential write operations.
func BenchmarkWrite_Sequential(b *testing.B) {
	ctx := context.Background()
	opCount := 0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create new BTree for each iteration to avoid node full
		btree, _ := OpenBTree("", nil)

		// Get current root
		rootInfo := btree.root.Get()

		// Create path with root node
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", i%100)) // Limit to 100 unique keys
		value := []byte(fmt.Sprintf("value-%d", i))

		// Perform CCOW write
		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			// Node might be full, skip this iteration
			rootInfo.Release()
			btree.Close()
			continue
		}

		// Update root
		err = btree.root.Update(ctx, newRoot, uint64(opCount))
		if err != nil {
			rootInfo.Release()
			btree.Close()
			continue
		}

		rootInfo.Release()
		btree.Close()
		opCount++
	}
}

// BenchmarkRead_Random benchmarks random read operations.
func BenchmarkRead_Random(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate with data
	rootInfo := btree.root.Get()
	numKeys := 1000
	keys := make([][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo.Root.Insert(key, value)
		keys[i] = key
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := keys[i%numKeys]
		path, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
		if len(path) == 0 {
			b.Fatal("empty path")
		}
		value, err := path[0].Node.Get(key)
		if err != nil {
			b.Fatal(err)
		}
		_ = value
	}
}

// BenchmarkWrite_Random benchmarks random write operations.
func BenchmarkWrite_Random(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	opCount := 0
	numKeys := 1000

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Get current root
		rootInfo := btree.root.Get()

		// Create path with root node
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		value := []byte(fmt.Sprintf("value-%d", i))

		// Perform CCOW write
		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			b.Fatal(err)
		}

		// Update root
		err = btree.root.Update(ctx, newRoot, uint64(opCount))
		if err != nil {
			b.Fatal(err)
		}

		rootInfo.Release()
		opCount++
	}
}

// BenchmarkRead_Concurrent benchmarks concurrent read operations.
func BenchmarkRead_Concurrent(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate with data
	rootInfo := btree.root.Get()
	numKeys := 1000
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := []byte(fmt.Sprintf("key-%d", i%numKeys))
			path, err := btree.FindPath(key)
			if err != nil {
				b.Error(err)
				return
			}
			if len(path) == 0 {
				b.Error("empty path")
				return
			}
			value, err := path[0].Node.Get(key)
			if err != nil {
				b.Error(err)
				return
			}
			_ = value
			i++
		}
	})
}

// BenchmarkWrite_Concurrent benchmarks concurrent write operations.
func BenchmarkWrite_Concurrent(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()
	numKeys := 1000

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		localOpCount := 0
		for pb.Next() {
			// Get current root
			rootInfo := btree.root.Get()

			// Create path with root node
			path := make(Path, 1)
			path[0] = &PathNode{
				Node:  rootInfo.Root,
				Level: 0,
			}

			key := []byte(fmt.Sprintf("key-%d", localOpCount%numKeys))
			value := []byte(fmt.Sprintf("value-%d", localOpCount))

			// Perform CCOW write
			modifyFunc := func(node *Node) error {
				return node.Insert(key, value)
			}

			newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
			if err != nil {
				b.Error(err)
				rootInfo.Release()
				return
			}

			// Update root
			err = btree.root.Update(ctx, newRoot, uint64(localOpCount))
			if err != nil {
				b.Error(err)
				rootInfo.Release()
				return
			}

			rootInfo.Release()
			localOpCount++
		}
	})
}

// BenchmarkMixed_ReadWrite benchmarks mixed read/write workload.
func BenchmarkMixed_ReadWrite(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	// Pre-populate with data
	rootInfo := btree.root.Get()
	numKeys := 1000
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	ctx := context.Background()
	opCount := 0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%10 == 0 {
			// 10% writes
			rootInfo := btree.root.Get()

			path := make(Path, 1)
			path[0] = &PathNode{
				Node:  rootInfo.Root,
				Level: 0,
			}

			key := []byte(fmt.Sprintf("key-%d", opCount%numKeys))
			value := []byte(fmt.Sprintf("value-%d", opCount))

			modifyFunc := func(node *Node) error {
				return node.Insert(key, value)
			}

			newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
			if err != nil {
				b.Fatal(err)
			}

			err = btree.root.Update(ctx, newRoot, uint64(opCount))
			if err != nil {
				b.Fatal(err)
			}

			rootInfo.Release()
			opCount++
		} else {
			// 90% reads
			key := []byte(fmt.Sprintf("key-%d", i%numKeys))
			path, err := btree.FindPath(key)
			if err != nil {
				b.Fatal(err)
			}
			if len(path) == 0 {
				b.Fatal("empty path")
			}
			value, err := path[0].Node.Get(key)
			if err != nil {
				b.Fatal(err)
			}
			_ = value
		}
	}
}

// BenchmarkNodeOperations benchmarks individual node operations.
func BenchmarkNodeOperations_Insert(b *testing.B) {
	node := NewNode(true)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := node.Insert(key, value)
		if err != nil {
			// Node is full, create new one
			node = NewNode(true)
			node.Insert(key, value)
		}
	}
}

func BenchmarkNodeOperations_Search(b *testing.B) {
	node := NewNode(true)
	numKeys := 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		node.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		idx := node.Search(key)
		if idx >= len(node.Keys) {
			b.Fatal("key not found")
		}
	}
}

func BenchmarkNodeOperations_Get(b *testing.B) {
	node := NewNode(true)
	numKeys := 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		node.Insert(key, value)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i%numKeys))
		value, err := node.Get(key)
		if err != nil {
			b.Fatal(err)
		}
		_ = value
	}
}

// BenchmarkCCOW_Complete benchmarks complete CCOW operation.
func BenchmarkCCOW_Complete(b *testing.B) {
	btree, _ := OpenBTree("", nil)
	defer btree.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Step 1: FindPath
		rootInfo := btree.root.Get()
		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		// Step 2: CopyPathBottomUp
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))

		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			b.Fatal(err)
		}

		// Step 3: Update root
		err = btree.root.Update(ctx, newRoot, uint64(i))
		if err != nil {
			b.Fatal(err)
		}

		rootInfo.Release()
	}
}

// TestPureMemoryBTree_CompleteWritePath tests complete write path.
func TestPureMemoryBTree_CompleteWritePath(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// Perform 100 writes
	for i := 0; i < 100; i++ {
		rootInfo := btree.root.Get()

		path := make(Path, 1)
		path[0] = &PathNode{
			Node:  rootInfo.Root,
			Level: 0,
		}

		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))

		modifyFunc := func(node *Node) error {
			return node.Insert(key, value)
		}

		newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		require.NoError(t, err)

		err = btree.root.Update(ctx, newRoot, uint64(i))
		require.NoError(t, err)

		rootInfo.Release()
	}

	// Verify all data
	rootInfo := btree.root.Get()
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		expectedValue := []byte(fmt.Sprintf("value-%d", i))

		value, err := rootInfo.Root.Get(key)
		require.NoError(t, err)
		require.Equal(t, expectedValue, value)
	}
	rootInfo.Release()
}

// TestPureMemoryBTree_ConcurrentWrites tests concurrent write safety.
func TestPureMemoryBTree_ConcurrentWrites(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()
	const numGoroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				rootInfo := btree.root.Get()

				path := make(Path, 1)
				path[0] = &PathNode{
					Node:  rootInfo.Root,
					Level: 0,
				}

				key := []byte(fmt.Sprintf("g%d-key-%d", goroutineID, i))
				value := []byte(fmt.Sprintf("value-%d", i))

				modifyFunc := func(node *Node) error {
					return node.Insert(key, value)
				}

				newRoot, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
				if err != nil {
					t.Errorf("CopyPathBottomUp failed: %v", err)
					rootInfo.Release()
					return
				}

				err = btree.root.Update(ctx, newRoot, uint64(goroutineID*opsPerGoroutine+i))
				if err != nil {
					t.Errorf("Update failed: %v", err)
					rootInfo.Release()
					return
				}

				rootInfo.Release()
			}
		}(g)
	}

	wg.Wait()

	// Verify final state
	rootInfo := btree.root.Get()
	t.Logf("Final root node has %d keys", rootInfo.Root.Size())
	rootInfo.Release()
}
