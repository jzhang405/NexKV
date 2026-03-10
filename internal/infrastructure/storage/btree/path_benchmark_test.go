// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"
)

// BenchmarkFindPath_Direct benchmarks path finding with direct pointer mode.
func BenchmarkFindPath_Direct(b *testing.B) {
	btree, err := OpenBTree("", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	// Setup: Insert 1000 keys
	rootInfo := btree.root.Get()
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	// Test key in the middle
	key := []byte{127, 3}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, _ := btree.FindPath(key)
		if len(path) > 0 {
			ReleasePath(path)
		}
	}
}

// BenchmarkFindPathPageID_MemoryMode benchmarks PageID mode with memory fallback.
func BenchmarkFindPathPageID_MemoryMode(b *testing.B) {
	btree, err := OpenBTree("", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	// Setup: Insert 1000 keys
	rootInfo := btree.root.Get()
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	// Test key in the middle
	key := []byte{127, 3}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, _ := btree.FindPathPageID(key)
		if len(path) > 0 {
			ReleasePath(path)
		}
	}
}

// BenchmarkFindPath_Comparison compares both methods side-by-side.
func BenchmarkFindPath_Comparison(b *testing.B) {
	btree, err := OpenBTree("", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	// Setup: Insert 1000 keys
	rootInfo := btree.root.Get()
	for i := 0; i < 1000; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	// Test key in the middle
	key := []byte{127, 3}

	b.Run("Direct", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			path, _ := btree.FindPath(key)
			if len(path) > 0 {
				ReleasePath(path)
			}
		}
	})

	b.Run("PageID", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			path, _ := btree.FindPathPageID(key)
			if len(path) > 0 {
				ReleasePath(path)
			}
		}
	})
}

// BenchmarkFindPath_SmallTree benchmarks with small tree (10 keys).
func BenchmarkFindPath_SmallTree(b *testing.B) {
	btree, err := OpenBTree("", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	rootInfo := btree.root.Get()
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	key := []byte{5}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, _ := btree.FindPath(key)
		if len(path) > 0 {
			ReleasePath(path)
		}
	}
}

// BenchmarkFindPathPageID_SmallTree benchmarks PageID mode with small tree.
func BenchmarkFindPathPageID_SmallTree(b *testing.B) {
	btree, err := OpenBTree("", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	rootInfo := btree.root.Get()
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	key := []byte{5}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, _ := btree.FindPathPageID(key)
		if len(path) > 0 {
			ReleasePath(path)
		}
	}
}

// BenchmarkFindPath_LargeTree benchmarks with larger tree (10000 keys).
func BenchmarkFindPath_LargeTree(b *testing.B) {
	btree, err := OpenBTree("", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	rootInfo := btree.root.Get()
	for i := 0; i < 10000; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	key := []byte{127, 39}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, _ := btree.FindPath(key)
		if len(path) > 0 {
			ReleasePath(path)
		}
	}
}

// BenchmarkFindPathPageID_LargeTree benchmarks PageID mode with large tree.
func BenchmarkFindPathPageID_LargeTree(b *testing.B) {
	btree, err := OpenBTree("", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	rootInfo := btree.root.Get()
	for i := 0; i < 10000; i++ {
		key := []byte{byte(i & 0xff), byte((i >> 8) & 0xff)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	key := []byte{127, 39}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, _ := btree.FindPathPageID(key)
		if len(path) > 0 {
			ReleasePath(path)
		}
	}
}

// BenchmarkPath_CopyPathBottomUp_CCOW benchmarks CCOW path copying.
func BenchmarkPath_CopyPathBottomUp_CCOW(b *testing.B) {
	btree, err := OpenBTree("", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	ctx := context.Background()

	// Setup: Insert 100 keys
	rootInfo := btree.root.Get()
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = rootInfo.Root.Insert(key, value)
	}
	rootInfo.Release()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, _ := btree.FindPath([]byte{50})
		if len(path) > 0 {
			_, _ = btree.CopyPathBottomUp(ctx, path, func(node *Node) error {
				return node.Insert([]byte{byte(i % 256)}, []byte("value"))
			})
			ReleasePath(path)
		}
	}
}
