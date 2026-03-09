// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BenchmarkPageAllocation_PageManagerOnly benchmarks just the page allocation.
func BenchmarkPageAllocation_PageManagerOnly(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := btree.pageManager.Allocate()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCopyPageOnly benchmarks just the copyPage operation.
func BenchmarkCopyPageOnly(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	// Allocate a test page
	testPage, _ := btree.pageManager.Allocate()
	testPageID := testPage.ID
	btree.pageManager.Release(testPage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := btree.copyPage(testPageID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDeserializeNode benchmarks node deserialization.
func BenchmarkDeserializeNode(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	page, _ := btree.pageManager.Allocate()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = btree.deserializeNode(page)
	}

	btree.pageManager.Release(page)
}

// BenchmarkSerializeNodeToPage benchmarks node serialization.
func BenchmarkSerializeNodeToPage(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	page, _ := btree.pageManager.Allocate()
	node := NewNode(true)
	node.Insert([]byte("test-key"), []byte("test-value"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = btree.serializeNodeToPage(node, page)
	}

	btree.pageManager.Release(page)
}

// BenchmarkGetAndReleasePage benchmarks page Get and Release operations.
func BenchmarkGetAndReleasePage(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	testPageID := model.PageID(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := btree.pageManager.Get(testPageID)
		if err != nil {
			b.Fatal(err)
		}
		btree.pageManager.Release(page)
	}
}
