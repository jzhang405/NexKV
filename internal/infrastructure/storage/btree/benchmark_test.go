// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"strconv"
	"testing"
)

// newBenchmarkBTree creates a BTree for benchmarking with larger storage.
func newBenchmarkBTree(b *testing.B) (*BTree, *OffheapBTreeStorage) {
	b.Helper()
	storage, err := NewOffheapBTreeStorage(128 * 1024 * 1024) // 128MB for benchmarks
	if err != nil {
		b.Fatalf("failed to create storage: %v", err)
	}
	tree, err := NewBTree(storage)
	if err != nil {
		b.Fatalf("failed to create btree: %v", err)
	}
	b.Cleanup(func() { tree.Close() })
	return tree, storage
}

// BenchmarkBTreeSequentialSet measures sequential write performance.
// Note: Limited to 100 unique keys to avoid triggering split (Phase 5 doesn't support split yet).
func BenchmarkBTreeSequentialSet(b *testing.B) {
	tree, _ := newBenchmarkBTree(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte("key-" + strconv.Itoa(i%100))
		value := []byte("value-" + strconv.Itoa(i%100))
		if err := tree.Set(ctx, key, value); err != nil {
			b.Fatalf("Set failed: %v", err)
		}
	}
}
