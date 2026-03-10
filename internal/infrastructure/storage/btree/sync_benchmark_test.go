// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BenchmarkInsertChild_Single benchmarks single InsertChild operation.
func BenchmarkInsertChild_Single(b *testing.B) {
	parent := NewNode(false)
	parent.PageID = 1

	child := NewNode(true)
	child.PageID = 100
	_ = child.Insert([]byte("key"), []byte("value"))

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create fresh parent for each iteration
		p := NewNode(false)
		p.PageID = 1

		key := []byte{byte(i % 256)}
		_ = p.InsertChild(key, child)
	}
}

// BenchmarkInsertChild_Sequential benchmarks sequential InsertChild operations.
func BenchmarkInsertChild_Sequential(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		parent := NewNode(false)
		parent.PageID = 1
		b.StartTimer()

		// Insert 10 children sequentially
		for j := 0; j < 10; j++ {
			child := NewNode(true)
			child.PageID = model.PageID(100 + j)
			_ = child.Insert([]byte{byte(j)}, []byte("value"))

			key := []byte{byte(j * 2)}
			_ = parent.InsertChild(key, child)
		}
	}
}

// BenchmarkInsertChild_Full benchmarks filling a node with children.
func BenchmarkInsertChild_Full(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		parent := NewNode(false)
		parent.PageID = 1
		b.StartTimer()

		// Fill node to capacity
		for j := 0; j < model.DefaultMaxKeys; j++ {
			child := NewNode(true)
			child.PageID = model.PageID(100 + j)
			_ = child.Insert([]byte{byte(j)}, []byte("value"))

			key := []byte{byte(j * 2)}
			_ = parent.InsertChild(key, child)
		}
	}
}

// BenchmarkSplit_InternalNode benchmarks splitting a full internal node.
func BenchmarkSplit_InternalNode(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a full internal node
		parent := NewNode(false)
		parent.PageID = 1

		for j := 0; j < model.DefaultMaxKeys+1; j++ {
			child := NewNode(true)
			child.PageID = model.PageID(100 + j)
			_ = child.Insert([]byte{byte(j)}, []byte("value"))

			parent.Children = append(parent.Children, child)
			parent.ChildIDs = append(parent.ChildIDs, child.PageID)
		}

		for j := 0; j < model.DefaultMaxKeys; j++ {
			key := []byte{byte(j)}
			parent.Keys = append(parent.Keys, key)
		}
		b.StartTimer()

		_, _, _ = parent.Split()
	}
}

// BenchmarkSplit_LeafNode benchmarks splitting a full leaf node.
func BenchmarkSplit_LeafNode(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a full leaf node
		leaf := NewNode(true)
		leaf.PageID = 1

		for j := 0; j < model.DefaultMaxKeys; j++ {
			key := []byte{byte(j)}
			value := []byte(fmt.Sprintf("value-%d", j))
			_ = leaf.Insert(key, value)
		}
		b.StartTimer()

		_, _, _ = leaf.Split()
	}
}

// BenchmarkMerge_InternalNodes benchmarks merging two internal nodes.
func BenchmarkMerge_InternalNodes(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create two half-full internal nodes
		left := NewNode(false)
		left.PageID = 1
		right := NewNode(false)
		right.PageID = 2

		for j := 0; j < model.DefaultMaxKeys/2; j++ {
			// Left node
			child1 := NewNode(true)
			child1.PageID = model.PageID(100 + j)
			left.Children = append(left.Children, child1)
			left.ChildIDs = append(left.ChildIDs, child1.PageID)

			if j < model.DefaultMaxKeys/2-1 {
				key := []byte{byte(j * 2)}
				left.Keys = append(left.Keys, key)
			}

			// Right node
			child2 := NewNode(true)
			child2.PageID = model.PageID(200 + j)
			right.Children = append(right.Children, child2)
			right.ChildIDs = append(right.ChildIDs, child2.PageID)

			if j < model.DefaultMaxKeys/2-1 {
				key := []byte{byte((j + model.DefaultMaxKeys/2) * 2)}
				right.Keys = append(right.Keys, key)
			}
		}
		b.StartTimer()

		_ = left.Merge(right)
	}
}

// BenchmarkMerge_LeafNodes benchmarks merging two leaf nodes.
func BenchmarkMerge_LeafNodes(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create two half-full leaf nodes
		left := NewNode(true)
		left.PageID = 1
		right := NewNode(true)
		right.PageID = 2

		for j := 0; j < model.DefaultMaxKeys/2; j++ {
			key := []byte{byte(j)}
			value := []byte(fmt.Sprintf("value-%d", j))
			_ = left.Insert(key, value)
		}

		for j := 0; j < model.DefaultMaxKeys/2; j++ {
			key := []byte{byte(j + model.DefaultMaxKeys/2)}
			value := []byte(fmt.Sprintf("value-%d", j))
			_ = right.Insert(key, value)
		}
		b.StartTimer()

		_ = left.Merge(right)
	}
}

// BenchmarkValidateChildConsistency benchmarks consistency validation.
func BenchmarkValidateChildConsistency(b *testing.B) {
	b.StopTimer()
	// Create a full internal node
	parent := NewNode(false)
	parent.PageID = 1

	for j := 0; j < model.DefaultMaxKeys+1; j++ {
		child := NewNode(true)
		child.PageID = model.PageID(100 + j)
		_ = child.Insert([]byte{byte(j)}, []byte("value"))

		parent.Children = append(parent.Children, child)
		parent.ChildIDs = append(parent.ChildIDs, child.PageID)
	}

	for j := 0; j < model.DefaultMaxKeys; j++ {
		key := []byte{byte(j)}
		parent.Keys = append(parent.Keys, key)
	}
	b.StartTimer()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = parent.ValidateChildConsistency()
	}
}

// BenchmarkEnsureChildIDs benchmarks EnsureChildIDs helper.
func BenchmarkEnsureChildIDs(b *testing.B) {
	b.StopTimer()
	// Create an internal node without ChildIDs
	parent := NewNode(false)
	parent.PageID = 1

	for j := 0; j < model.DefaultMaxKeys+1; j++ {
		child := NewNode(true)
		child.PageID = model.PageID(100 + j)
		_ = child.Insert([]byte{byte(j)}, []byte("value"))

		parent.Children = append(parent.Children, child)
		// Don't set ChildIDs
	}

	for j := 0; j < model.DefaultMaxKeys; j++ {
		key := []byte{byte(j)}
		parent.Keys = append(parent.Keys, key)
	}
	b.StartTimer()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		parent.EnsureChildIDs()
	}
}

// BenchmarkClone_WithChildIDs benchmarks cloning a node with ChildIDs.
func BenchmarkClone_WithChildIDs(b *testing.B) {
	b.StopTimer()
	// Create a full internal node
	parent := NewNode(false)
	parent.PageID = 1

	for j := 0; j < model.DefaultMaxKeys+1; j++ {
		child := NewNode(true)
		child.PageID = model.PageID(100 + j)
		_ = child.Insert([]byte{byte(j)}, []byte("value"))

		parent.Children = append(parent.Children, child)
		parent.ChildIDs = append(parent.ChildIDs, child.PageID)
	}

	for j := 0; j < model.DefaultMaxKeys; j++ {
		key := []byte{byte(j)}
		parent.Keys = append(parent.Keys, key)
	}
	b.StartTimer()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = parent.Clone()
	}
}

// BenchmarkClear_WithChildIDs benchmarks clearing a node with ChildIDs.
func BenchmarkClear_WithChildIDs(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a full internal node
		parent := NewNode(false)
		parent.PageID = 1

		for j := 0; j < model.DefaultMaxKeys+1; j++ {
			child := NewNode(true)
			child.PageID = model.PageID(100 + j)
			_ = child.Insert([]byte{byte(j)}, []byte("value"))

			parent.Children = append(parent.Children, child)
			parent.ChildIDs = append(parent.ChildIDs, child.PageID)
		}

		for j := 0; j < model.DefaultMaxKeys; j++ {
			key := []byte{byte(j)}
			parent.Keys = append(parent.Keys, key)
		}
		b.StartTimer()

		parent.Clear()
	}
}

// BenchmarkMixed_Operations benchmarks a realistic mix of operations.
func BenchmarkMixed_Operations(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		parent := NewNode(false)
		parent.PageID = 1
		b.StartTimer()

		// 50% InsertChild
		for j := 0; j < 10; j++ {
			child := NewNode(true)
			child.PageID = model.PageID(100 + j)
			_ = child.Insert([]byte{byte(j)}, []byte("value"))
			_ = parent.InsertChild([]byte{byte(j * 2)}, child)
		}

		// 30% Validate
		_ = parent.ValidateChildConsistency()

		// 10% Clone
		cloned := parent.Clone()
		_ = cloned.ValidateChildConsistency()

		// 10% Clear
		parent.Clear()
	}
}
