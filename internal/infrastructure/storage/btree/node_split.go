// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"github.com/jzhang405/NexKV/internal/domain/model"
)

// SplitCopy performs a copy-on-write split without modifying the original node.
// This is a lock-free operation that allows concurrent reads on the original node.
// Returns (newLeft, newRight, medianKey), where both new nodes are independent copies.
//
// Benefits over regular Split:
// - No lock required: original node can still be read
// - Better concurrency: multiple goroutines can read original node during split
// - Copy-on-write semantics: matches CCOW design pattern
//
// Performance: Slightly slower due to copying both nodes, but enables concurrency.
func (n *Node) SplitCopy() (*Node, *Node, []byte, error) {
	if len(n.Keys) < model.DefaultMaxKeys {
		return nil, nil, nil, ErrNodeNotFull
	}

	// Find median index
	mid := (model.DefaultMaxKeys - 1) / 2
	medianKey := n.Keys[mid]

	// Create new left node (copy of left half)
	newLeft := &Node{
		IsLeaf:   n.IsLeaf,
		Keys:     make([][]byte, mid+1),
		Values:   make([][]byte, mid+1),
		Children: make([]*Node, 0, mid+2),
	}
	copy(newLeft.Keys, n.Keys[:mid+1])
	copy(newLeft.Values, n.Values[:mid+1])
	if !n.IsLeaf {
		newLeft.Children = append(newLeft.Children, n.Children[:mid+1]...)
	}

	// Create new right node (copy of right half)
	newRight := &Node{
		IsLeaf:   n.IsLeaf,
		Keys:     make([][]byte, model.DefaultMaxKeys-mid-1),
		Values:   make([][]byte, model.DefaultMaxKeys-mid-1),
		Children: make([]*Node, 0, model.DefaultMaxKeys-mid),
	}
	copy(newRight.Keys, n.Keys[mid+1:])
	copy(newRight.Values, n.Values[mid+1:])
	if !n.IsLeaf {
		newRight.Children = append(newRight.Children, n.Children[mid+1:]...)
	}

	return newLeft, newRight, medianKey, nil
}

// SplitCopyOptimized is an optimized version of SplitCopy with pre-allocation.
// This version pre-allocates exact capacities to avoid slice growth.
func (n *Node) SplitCopyOptimized() (*Node, *Node, []byte, error) {
	if len(n.Keys) < model.DefaultMaxKeys {
		return nil, nil, nil, ErrNodeNotFull
	}

	mid := (model.DefaultMaxKeys - 1) / 2
	medianKey := n.Keys[mid]

	// Pre-allocate with exact capacities
	leftKeys := make([][]byte, mid+1)
	leftValues := make([][]byte, mid+1)
	leftChildren := make([]*Node, 0, mid+2)

	rightKeys := make([][]byte, model.DefaultMaxKeys-mid-1)
	rightValues := make([][]byte, model.DefaultMaxKeys-mid-1)
	rightChildren := make([]*Node, 0, model.DefaultMaxKeys-mid)

	// Copy left half
	copy(leftKeys, n.Keys[:mid+1])
	copy(leftValues, n.Values[:mid+1])
	if !n.IsLeaf {
		leftChildren = append(leftChildren, n.Children[:mid+1]...)
	}

	// Copy right half
	copy(rightKeys, n.Keys[mid+1:])
	copy(rightValues, n.Values[mid+1:])
	if !n.IsLeaf {
		rightChildren = append(rightChildren, n.Children[mid+1:]...)
	}

	newLeft := &Node{
		IsLeaf:   n.IsLeaf,
		Keys:     leftKeys,
		Values:   leftValues,
		Children: leftChildren,
	}

	newRight := &Node{
		IsLeaf:   n.IsLeaf,
		Keys:     rightKeys,
		Values:   rightValues,
		Children: rightChildren,
	}

	return newLeft, newRight, medianKey, nil
}
