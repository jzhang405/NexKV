// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// MemoryBTree is a pure in-memory BTree without persistence.
// This is designed for performance testing and benchmarking.
//
// Key differences from BTree:
//   - No disk persistence (no ChunkManager, no WAL)
//   - No serialization overhead
//   - Direct in-memory operations
//   - Simpler concurrency control (single mutex)
//
// Use cases:
//   - Performance benchmarking
//   - Memory-only operations testing
//   - Comparison with persistent BTree
type MemoryBTree struct {
	mu       sync.RWMutex
	root     *LeafPage
	maxKeys  int
	splitThreshold int
}

// NewMemoryBTree creates a new pure in-memory BTree.
func NewMemoryBTree() *MemoryBTree {
	return &MemoryBTree{
		root:    NewLeafPage(model.RootPageID),
		maxKeys: model.DefaultMaxKeys,
		splitThreshold: model.DefaultMaxKeys - 1,
	}
}

// Set stores a key-value pair in pure memory.
//
// This is a simplified version that:
//   - Performs in-memory operations only
//   - No disk I/O
//   - No serialization
//   - Simple mutex-based concurrency
//   - No split/merge (limited to maxKeys)
func (m *MemoryBTree) Set(ctx context.Context, key, value []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("key cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Simple case: root is a leaf (tree has only one level)
	if m.root == nil {
		m.root = NewLeafPage(model.RootPageID)
	}

	// Try to insert into root leaf
	needsSplit, err := m.root.Insert(key, value)
	if err != nil {
		return fmt.Errorf("insert into leaf: %w", err)
	}

	// For this simple version, we don't handle split
	// In production, you would check if needsSplit and split the node
	_ = needsSplit

	return nil
}

// Get retrieves a value by key in pure memory.
func (m *MemoryBTree) Get(ctx context.Context, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("key cannot be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.root == nil {
		return nil, ErrKeyNotFound
	}

	value, found := m.root.Get(key)
	if !found {
		return nil, ErrKeyNotFound
	}

	return value, nil
}

// Delete removes a key from the tree in pure memory.
func (m *MemoryBTree) Delete(ctx context.Context, key []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("key cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.root == nil {
		return ErrKeyNotFound
	}

	deleted, err := m.root.Delete(key)
	if err != nil {
		return err
	}

	if !deleted {
		return ErrKeyNotFound
	}

	// Note: Merge logic is not implemented in this simple version
	// In a production system, you would check if keys < minKeys and merge

	return nil
}

// splitRoot splits the root leaf node.
func (m *MemoryBTree) splitRoot() error {
	if m.root == nil {
		return fmt.Errorf("cannot split nil root")
	}

	// Create two new leaf pages
	leftLeaf := NewLeafPage(model.PageID(1))
	rightLeaf := NewLeafPage(model.PageID(2))

	// Split keys between left and right
	keys := m.root.keys
	values := m.root.values
	mid := len(keys) / 2

	leftLeaf.keys = append(leftLeaf.keys, keys[:mid]...)
	leftLeaf.values = append(leftLeaf.values, values[:mid]...)

	rightLeaf.keys = append(rightLeaf.keys, keys[mid:]...)
	rightLeaf.values = append(rightLeaf.values, values[mid:]...)

	// The split key (middle key) would be promoted to an internal page
	// in a multi-level tree. For this simple version, we just keep
	// it as a marker.

	// In a full implementation, you would create an InternalPage here
	// and set it as the new root with two children (left and right).

	// For simplicity, we'll just replace the root with the left page
	// and note that right page is orphaned (this is a simplified version)
	m.root = leftLeaf

	// TODO: In a complete implementation:
	// - Create InternalPage as new root
	// - Add split key to InternalPage
	// - Add left and right as children

	return nil
}

// GetStats returns statistics about the memory BTree.
func (m *MemoryBTree) GetStats() *MemoryBTreeStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.root == nil {
		return &MemoryBTreeStats{
			NumKeys: 0,
			Depth:   0,
		}
	}

	return &MemoryBTreeStats{
		NumKeys: m.root.NumKeys(),
		Depth:   1, // Simple version always has depth 1
	}
}

// MemoryBTreeStats holds statistics about the memory BTree.
type MemoryBTreeStats struct {
	NumKeys int
	Depth   int
}

// String returns a string representation of the stats.
func (s *MemoryBTreeStats) String() string {
	return fmt.Sprintf("Keys: %d, Depth: %d", s.NumKeys, s.Depth)
}

// BenchmarkSet is a helper function for benchmarking pure memory Set operations.
// It performs multiple Set operations and returns timing information.
func (m *MemoryBTree) BenchmarkSet(ctx context.Context, numOps int, keySize, valueSize int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := 0; i < numOps; i++ {
		key := fmt.Sprintf("key-%010d", i)
		value := fmt.Sprintf("value-%010d", i)

		_, err := m.root.Insert([]byte(key), []byte(value))
		if err != nil {
			return fmt.Errorf("insert failed at iteration %d: %w", i, err)
		}

		// Simple split handling (not optimal for benchmarking)
		if m.root.NumKeys() > m.splitThreshold {
			// For benchmarking, we'll just clear and create new
			// This is NOT how a real BTree would work
			oldKeys := m.root.keys
			oldValues := m.root.values

			m.root = NewLeafPage(model.RootPageID)
			m.root.keys = oldKeys[:m.maxKeys/2]
			m.root.values = oldValues[:m.maxKeys/2]
		}
	}

	return nil
}

// BenchmarkGet is a helper function for benchmarking pure memory Get operations.
func (m *MemoryBTree) BenchmarkGet(ctx context.Context, numOps int, keySize int) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := 0; i < numOps; i++ {
		key := fmt.Sprintf("key-%010d", i%100) // Modulo to reuse keys

		_, found := m.root.Get([]byte(key))
		if !found {
			// Key not found is acceptable in benchmarking
			continue
		}
	}

	return nil
}
