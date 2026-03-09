// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BenchmarkPathFinding_WithPool benchmarks path finding with object pool optimization.
func BenchmarkPathFinding_WithPool(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	key := []byte("test-key")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPathFinding_MemoryAllocation benchmarks memory allocation for path finding.
func BenchmarkPathFinding_MemoryAllocation(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	b.ReportAllocs()

	key := []byte("test-key")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := btree.FindPath(key)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAcquireReleasePath benchmarks path pool acquire/release overhead.
func BenchmarkAcquireReleasePath(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := AcquirePath()
		ReleasePath(path)
	}
}

// BenchmarkPathCreation_WithoutPool benchmarks path creation without pool.
func BenchmarkPathCreation_WithoutPool(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := make(Path, 0, 10)
		_ = path
	}
}

// BenchmarkPathCreation_WithPool benchmarks path creation with pool.
func BenchmarkPathCreation_WithPool(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := AcquirePath()
		ReleasePath(path)
	}
}

// BenchmarkConcurrentPathFinding benchmarks concurrent path finding with pool.
func BenchmarkConcurrentPathFinding(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	key := []byte("test-key")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := btree.FindPath(key)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
