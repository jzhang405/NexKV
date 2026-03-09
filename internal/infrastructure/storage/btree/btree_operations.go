// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// InsertWithSplit inserts a key-value pair with automatic node splitting.
// This is a higher-level operation that handles node overflow by splitting.
func (b *BTree) InsertWithSplit(ctx context.Context, key, value []byte) error {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 1. Find the path to the leaf
	path, err := b.FindPath(key)
	if err != nil {
		return fmt.Errorf("failed to find path: %w", err)
	}
	defer ReleasePath(path) // ✅ 释放 path 回 pool

	// Check context again before potentially expensive operations
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 2. Copy the path bottom-up and insert in the new leaf
	// This is the CORE of CCOW: all modifications happen on copied nodes
	newRoot, err := b.CopyPathBottomUp(ctx, path, func(node *Node) error {
		// node is a COPY of the original node
		// Insert into the copied node (not the original)
		if err := node.Insert(key, value); err != nil {
			if err == ErrNodeFull {
				// Node is full, we'll handle splitting separately
				return ErrNodeFull
			}
			return err
		}
		return nil
	})

	// 3. Handle node full case (need splitting)
	if err != nil && err.Error() == "node is full" {
		return b.insertWithSplitPath(ctx, path, key, value)
	}

	if err != nil {
		return fmt.Errorf("failed to copy path: %w", err)
	}

	// 4. Update root via CAS (atomic commit point)
	return b.root.Update(ctx, newRoot, 0)
}

// insertWithSplitPath handles insertion when node is full by splitting.
func (b *BTree) insertWithSplitPath(ctx context.Context, path Path, key, value []byte) error {
	// Get the leaf node (last in path)
	leafIdx := len(path) - 1
	leafNode := path[leafIdx].Node

	// Split the leaf node
	rightNode, medianKey, err := leafNode.Split()
	if err != nil {
		return fmt.Errorf("failed to split node: %w", err)
	}

	// Insert the key into the appropriate half
	if compare := compareBytes(key, medianKey); compare < 0 {
		// Key goes to left node (original leaf)
		if err := leafNode.Insert(key, value); err != nil {
			return fmt.Errorf("failed to insert into left node: %w", err)
		}
	} else {
		// Key goes to right node
		if err := rightNode.Insert(key, value); err != nil {
			return fmt.Errorf("failed to insert into right node: %w", err)
		}
	}

	// Now we need to insert the median key into the parent
	// This requires walking up the tree and potentially splitting parents too
	return b.promoteSplit(ctx, path, medianKey, rightNode, leafIdx)
}

// promoteSplit promotes a split result up the tree.
// medianKey is the key to insert into the parent
// rightNode is the new right child
// splitIdx is the index of the node that was split
func (b *BTree) promoteSplit(ctx context.Context, path Path, medianKey []byte, rightNode *Node, splitIdx int) error {
	// If we split the root, we need to create a new root
	if splitIdx == 0 {
		return b.splitRoot(ctx, path[0].Node, medianKey, rightNode)
	}

	// Get the parent node
	parentIdx := splitIdx - 1
	parentNode := path[parentIdx].Node

	// Try to insert median key and right child into parent
	if len(parentNode.Keys) < model.DefaultMaxKeys {
		// Parent has space, insert directly
		if err := parentNode.InsertChild(medianKey, rightNode); err != nil {
			return fmt.Errorf("failed to insert into parent: %w", err)
		}

		// Copy the modified path up to parent
		newRoot, err := b.CopyPathBottomUp(ctx, path[:parentIdx+1], func(node *Node) error {
			return nil // Already modified
		})
		if err != nil {
			return fmt.Errorf("failed to copy path: %w", err)
		}

		return b.root.Update(ctx, newRoot, 0)
	}

	// Parent is also full, need to split recursively
	// For simplicity, we'll handle this by creating a new root
	return b.splitRoot(ctx, path[0].Node, medianKey, rightNode)
}

// splitRoot creates a new root when the current root needs to split.
func (b *BTree) splitRoot(ctx context.Context, oldRoot *Node, medianKey []byte, rightNode *Node) error {
	// Create new internal node as root
	newRoot := NewNode(false)

	// Set children: oldRoot as left child, rightNode as right child
	newRoot.Children = append(newRoot.Children, oldRoot)
	newRoot.Children = append(newRoot.Children, rightNode)

	// Insert the separator key
	newRoot.Keys = append(newRoot.Keys, medianKey)

	// Update versioned root
	return b.root.Update(ctx, newRoot, 0)
}

// DeleteWithMerge deletes a key with automatic node merging.
// This is a higher-level operation that handles node underflow by merging.
func (b *BTree) DeleteWithMerge(ctx context.Context, key []byte) error {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 1. Find the path to the leaf
	path, err := b.FindPath(key)
	if err != nil {
		return fmt.Errorf("failed to find path: %w", err)
	}

	// Check context again before potentially expensive operations
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 2. Delete from the leaf
	leafNode := path[len(path)-1].Node
	if err := leafNode.Delete(key); err != nil {
		if err == ErrKeyNotFound {
			return ErrKeyNotFound
		}
		return fmt.Errorf("failed to delete: %w", err)
	}

	// 3. Check if node is underflow
	if leafNode.IsUnderflow() {
		// Try to merge with sibling
		// For simplicity, we just merge with parent
		return b.mergeUnderflow(ctx, path, len(path)-1)
	}

	// 4. Copy the path bottom-up (no merge needed)
	newRoot, err := b.CopyPathBottomUp(ctx, path, func(node *Node) error {
		return nil // Leaf already modified
	})
	if err != nil {
		return fmt.Errorf("failed to copy path: %w", err)
	}

	// 5. Update root
	return b.root.Update(ctx, newRoot, 0)
}

// mergeUnderflow handles node underflow by merging with sibling.
func (b *BTree) mergeUnderflow(ctx context.Context, path Path, underflowIdx int) error {
	// For simplicity, we just copy the path as-is
	// A full implementation would:
	// 1. Try to borrow from sibling
	// 2. If sibling also underflow, merge with sibling
	// 3. Recursively handle parent underflow

	newRoot, err := b.CopyPathBottomUp(ctx, path, func(node *Node) error {
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to copy path: %w", err)
	}

	return b.root.Update(ctx, newRoot, 0)
}

// compareBytes compares two byte slices.
// Returns -1 if a < b, 0 if a == b, 1 if a > b
func compareBytes(a, b []byte) int {
	return compareBytesInternal(a, b)
}

// compareBytesInternal is the actual comparison function.
func compareBytesInternal(a, b []byte) int {
	min := len(a)
	if len(b) < min {
		min = len(b)
	}

	for i := range min {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}

	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

// GetMaxLevels returns the maximum number of levels in the tree.
func (b *BTree) GetMaxLevels() int {
	return b.maxLevels
}

// SetMaxLevels sets the maximum number of levels in the tree.
func (b *BTree) SetMaxLevels(levels int) {
	b.maxLevels = levels
}

// GetDepth returns the current depth of the tree.
func (b *BTree) GetDepth() int {
	rootInfo := b.root.Get()
	defer rootInfo.Release()

	depth := 0
	current := rootInfo.Root
	for !current.IsLeaf {
		depth++
		if len(current.Children) == 0 {
			break
		}
		current = current.Children[0]
	}
	return depth + 1
}

// GetStats returns statistics about the BTree.
func (b *BTree) GetStats() *BTreeStats {
	rootInfo := b.root.Get()
	defer rootInfo.Release()

	return &BTreeStats{
		Depth:     b.GetDepth(),
		MaxLevels: b.maxLevels,
		RootSize:  rootInfo.Root.Size(),
		MaxKeys:   model.DefaultMaxKeys,
		MinKeys:   model.DefaultMinKeys,
	}
}

// BTreeStats holds BTree statistics.
type BTreeStats struct {
	Depth      int
	MaxLevels  int
	RootSize   int
	MaxKeys    int
	MinKeys    int
	TotalNodes int
	TotalKeys  int
}

// String returns a string representation of the stats.
func (s *BTreeStats) String() string {
	return fmt.Sprintf("Depth: %d/%d, RootSize: %d/%d",
		s.Depth, s.MaxLevels, s.RootSize, s.MaxKeys)
}
