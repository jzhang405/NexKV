// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BenchmarkCopyPathBottomUp benchmarks the CCOW path copying operation.
func BenchmarkCopyPathBottomUp(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a test path with proper parent-child relationships
		path := make(Path, 3)

		// Create leaf node (level 0)
		leafPage, _ := btree.pageManager.Allocate()
		leafNode := NewNode(true)
		path[2] = &PathNode{
			Node:   leafNode,
			PageID: leafPage.ID,
			Level:  0,
		}

		// Create internal node (level 1) that points to leaf
		internalPage, _ := btree.pageManager.Allocate()
		internalNode := NewNode(false)
		internalNode.Children = []model.PageID{leafPage.ID} // Parent points to child
		path[1] = &PathNode{
			Node:   internalNode,
			PageID: internalPage.ID,
			Level:  1,
		}

		// Create root node (level 2) that points to internal
		rootPage, _ := btree.pageManager.Allocate()
		rootNode := NewNode(false)
		rootNode.Children = []model.PageID{internalPage.ID} // Parent points to child
		path[0] = &PathNode{
			Node:   rootNode,
			PageID: rootPage.ID,
			Level:  2,
		}

		btree.pageManager.Release(leafPage)
		btree.pageManager.Release(internalPage)
		btree.pageManager.Release(rootPage)
		b.StartTimer()

		modifyFunc := func(node *Node) error {
			return node.Insert([]byte("test-key"), []byte("test-value"))
		}
		_, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCopyPathBottomUp_SingleLevel benchmarks single-level path copying.
func BenchmarkCopyPathBottomUp_SingleLevel(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create single-level path (just leaf)
		path := make(Path, 1)
		page, _ := btree.pageManager.Allocate()
		node := NewNode(true)
		path[0] = &PathNode{
			Node:   node,
			PageID: page.ID,
			Level:  0,
		}
		btree.pageManager.Release(page)

		modifyFunc := func(node *Node) error {
			return node.Insert([]byte("test-key"), []byte("test-value"))
		}
		_, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCopyPathBottomUp_ThreeLevels benchmarks three-level path copying (realistic BTree depth).
func BenchmarkCopyPathBottomUp_ThreeLevels(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create three-level path (root -> internal -> leaf) with proper relationships
		path := make(Path, 3)

		// Leaf node
		leafPage, _ := btree.pageManager.Allocate()
		leafNode := NewNode(true)
		path[2] = &PathNode{
			Node:   leafNode,
			PageID: leafPage.ID,
			Level:  0,
		}

		// Internal node pointing to leaf
		internalPage, _ := btree.pageManager.Allocate()
		internalNode := NewNode(false)
		internalNode.Children = []model.PageID{leafPage.ID}
		path[1] = &PathNode{
			Node:   internalNode,
			PageID: internalPage.ID,
			Level:  1,
		}

		// Root node pointing to internal
		rootPage, _ := btree.pageManager.Allocate()
		rootNode := NewNode(false)
		rootNode.Children = []model.PageID{internalPage.ID}
		path[0] = &PathNode{
			Node:   rootNode,
			PageID: rootPage.ID,
			Level:  2,
		}

		btree.pageManager.Release(leafPage)
		btree.pageManager.Release(internalPage)
		btree.pageManager.Release(rootPage)
		b.StartTimer()

		modifyFunc := func(node *Node) error {
			return node.Insert([]byte("test-key"), []byte("test-value"))
		}
		_, err := btree.CopyPathBottomUp(ctx, path, modifyFunc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkModifyPage benchmarks the ModifyPage operation.
func BenchmarkModifyPage(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := btree.pageManager.Allocate()
		if err != nil {
			b.Fatal(err)
		}

		key := []byte("test-key")
		value := []byte("test-value")

		err = btree.ModifyPage(page, key, value, ModifyInsert)
		if err != nil {
			btree.pageManager.Release(page)
			b.Fatal(err)
		}

		btree.pageManager.Release(page)
	}
}

// BenchmarkModifyPage_Insert benchmarks ModifyPage with insert operation.
func BenchmarkModifyPage_Insert(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := btree.pageManager.Allocate()
		if err != nil {
			b.Fatal(err)
		}

		key := []byte("test-key")
		value := []byte("test-value")

		err = btree.ModifyPage(page, key, value, ModifyInsert)
		if err != nil {
			btree.pageManager.Release(page)
			b.Fatal(err)
		}

		btree.pageManager.Release(page)
	}
}

// BenchmarkModifyPage_Update benchmarks ModifyPage with update operation.
func BenchmarkModifyPage_Update(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := btree.pageManager.Allocate()
		if err != nil {
			b.Fatal(err)
		}

		// First insert
		key := []byte("test-key")
		value := []byte("test-value")
		_ = btree.ModifyPage(page, key, value, ModifyInsert)

		// Then update
		newValue := []byte("new-value")
		err = btree.ModifyPage(page, key, newValue, ModifyUpdate)
		if err != nil {
			btree.pageManager.Release(page)
			b.Fatal(err)
		}

		btree.pageManager.Release(page)
	}
}

// BenchmarkModifyPage_Delete benchmarks ModifyPage with delete operation.
func BenchmarkModifyPage_Delete(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := btree.pageManager.Allocate()
		if err != nil {
			b.Fatal(err)
		}

		// First insert
		key := []byte("test-key")
		value := []byte("test-value")
		_ = btree.ModifyPage(page, key, value, ModifyInsert)

		// Then delete
		err = btree.ModifyPage(page, key, nil, ModifyDelete)
		if err != nil {
			btree.pageManager.Release(page)
			b.Fatal(err)
		}

		btree.pageManager.Release(page)
	}
}

// BenchmarkUpdateChildReference benchmarks the child reference update operation.
func BenchmarkUpdateChildReference(b *testing.B) {
	config := model.NewDefaultBTreeConfig()
	btree, err := OpenBTree("/tmp/bench", config)
	if err != nil {
		b.Fatal(err)
	}
	defer btree.Close()

	// Create test nodes
	parentNode := NewNode(false)
	parentNode.Children = []model.PageID{1, 2, 3, 4, 5}

	oldPageID := model.PageID(3)
	newPageID := model.PageID(30)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := btree.updateChildReference(parentNode, oldPageID, newPageID)
		if err != nil {
			b.Fatal(err)
		}
		// Reset for next iteration
		parentNode.Children[2] = oldPageID
	}
}
