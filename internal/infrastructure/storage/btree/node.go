// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"errors"
	"sort"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

var (
	// ErrNodeFull is returned when trying to insert into a full node.
	ErrNodeFull = errors.New("node is full")

	// ErrNodeNotFull is returned when trying to split a non-full node.
	ErrNodeNotFull = errors.New("node is not full")

	// ErrKeyNotFound is returned when a key is not found in the node.
	ErrKeyNotFound = errors.New("key not found")

	// ErrInvalidKey is returned for invalid key operations.
	ErrInvalidKey = errors.New("invalid key")
)

// Node represents a BTree node (either leaf or internal).
//
// Phase 2.5 - Hybrid Architecture:
//   - Primary: Pure in-memory with direct pointers for performance
//   - Secondary: PageID field for persistence support
//   - Transition: Gradual migration to PageID-based indirection
//
// Phase 2 - Lazy Loading Support:
//   - Children []*Node: In-memory cache for fast access
//   - ChildIDs []PageID: Persistent references for serialization
//   - Both fields are kept in sync for hybrid access
//
// Node Layout:
//   - Leaf node:   [Keys[i], Values[i]] pairs, sorted by Keys
//   - Internal node: Keys[i] are separators, Children[i] and Children[i+1]
//     are subtrees with keys < Keys[i] and >= Keys[i] respectively
//
// Invariants:
//   - len(Keys) <= DefaultMaxKeys
//   - For internal nodes: len(Children) == len(ChildIDs) == len(Keys) + 1
//   - Keys are always sorted in ascending order
//   - All keys in Children[i] < Keys[i] <= keys in Children[i+1]
type Node struct {
	// PageID stores the persistent page identifier (0 for in-memory nodes).
	// Used for serialization and crash recovery.
	// When PageID == 0, the node exists only in memory.
	PageID model.PageID

	// Keys stores the sorted keys in ascending order.
	// For leaf nodes: actual data keys
	// For internal nodes: separator keys between children
	Keys [][]byte

	// Values stores the values (only for leaf nodes).
	// Values[i] corresponds to Keys[i].
	// Not used for internal nodes (will be empty).
	Values [][]byte

	// Children stores child node pointers (only for internal nodes).
	// Used as in-memory cache for fast access.
	// Children[i] contains keys < Keys[i]
	// Children[i+1] contains keys >= Keys[i]
	// Using direct Node pointers eliminates PageID indirection.
	Children []*Node

	// ChildIDs stores persistent PageID references (only for internal nodes).
	// Used for serialization and lazy loading.
	// ChildIDs[i] corresponds to Children[i].
	// When a child is loaded from disk, ChildIDs[i] is used to fetch the node.
	// For in-memory nodes (PageID == 0), ChildIDs[i] will be 0.
	ChildIDs []model.PageID

	// IsLeaf indicates whether this is a leaf node.
	// Leaf nodes have Values, internal nodes have Children.
	IsLeaf bool

	// pinCount tracks the number of active references to this node.
	// Used for cache management and eviction.
	// When pinCount > 0, the node cannot be evicted from cache.
	pinCount int32
}

// NewNode creates a new node with the given type.
// PageID is initialized to 0 (in-memory only).
// ChildIDs is initialized to empty slice for internal nodes.
// Call allocateNodePageID() to assign a persistent page ID.
func NewNode(isLeaf bool) *Node {
	return &Node{
		PageID:   0, // In-memory node, no persistent page yet
		IsLeaf:   isLeaf,
		Keys:     make([][]byte, 0, model.DefaultMaxKeys),
		Values:   make([][]byte, 0, model.DefaultMaxKeys),
		Children: make([]*Node, 0, model.DefaultMaxKeys+1),
		ChildIDs: make([]model.PageID, 0, model.DefaultMaxKeys+1), // Initialize ChildIDs
	}
}

// Search searches for a key in the node using binary search.
// Returns the index where the key is found or should be inserted.
func (n *Node) Search(key []byte) int {
	return sort.Search(len(n.Keys), func(i int) bool {
		return bytes.Compare(n.Keys[i], key) >= 0
	})
}

// BinarySearch is an alias for Search for clarity.
func (n *Node) BinarySearch(key []byte) int {
	return n.Search(key)
}

// Get retrieves the value for a key (leaf nodes only).
func (n *Node) Get(key []byte) ([]byte, error) {
	if !n.IsLeaf {
		return nil, errors.New("Get: only leaf nodes have values")
	}

	idx := n.Search(key)
	if idx < len(n.Keys) && bytes.Equal(n.Keys[idx], key) {
		return n.Values[idx], nil
	}

	return nil, ErrKeyNotFound
}

// Insert inserts a key-value pair into a leaf node.
func (n *Node) Insert(key, value []byte) error {
	if len(key) == 0 {
		return ErrInvalidKey
	}

	if !n.IsLeaf {
		return errors.New("Insert: use InsertChild for internal nodes")
	}

	if len(n.Keys) >= model.DefaultMaxKeys {
		return ErrNodeFull
	}

	// Check if key already exists
	idx := n.Search(key)
	if idx < len(n.Keys) && bytes.Equal(n.Keys[idx], key) {
		// Update existing key
		n.Values[idx] = value
		return nil
	}

	// Insert new key-value pair
	n.Keys = append(n.Keys, nil)
	n.Values = append(n.Values, nil)

	// Shift elements to make room
	copy(n.Keys[idx+1:], n.Keys[idx:])
	copy(n.Values[idx+1:], n.Values[idx:])

	// Insert new elements
	n.Keys[idx] = key
	n.Values[idx] = value

	return nil
}

// BatchInsert inserts multiple key-value pairs in one operation.
// This is more efficient than multiple Insert calls as it reduces shifts.
func (n *Node) BatchInsert(keys, values [][]byte) error {
	if len(keys) != len(values) {
		return errors.New("BatchInsert: keys and values length mismatch")
	}

	if !n.IsLeaf {
		return errors.New("BatchInsert: use InsertChild for internal nodes")
	}

	// Check if we have enough capacity
	if len(n.Keys)+len(keys) > model.DefaultMaxKeys {
		return ErrNodeFull
	}

	// Sort keys if not already sorted (simple check)
	// For now, assume keys are sorted or we'll handle unsorted case

	// Find insertion positions and merge
	newKeys := make([][]byte, 0, len(n.Keys)+len(keys))
	newValues := make([][]byte, 0, len(n.Values)+len(values))

	i, j := 0, 0
	for i < len(n.Keys) && j < len(keys) {
		cmp := bytes.Compare(n.Keys[i], keys[j])
		if cmp < 0 {
			// Old key comes first
			newKeys = append(newKeys, n.Keys[i])
			newValues = append(newValues, n.Values[i])
			i++
		} else if cmp > 0 {
			// New key comes first
			newKeys = append(newKeys, keys[j])
			newValues = append(newValues, values[j])
			j++
		} else {
			// Duplicate key, update with new value
			newKeys = append(newKeys, n.Keys[i])
			newValues = append(newValues, values[j])
			i++
			j++
		}
	}

	// Append remaining
	for i < len(n.Keys) {
		newKeys = append(newKeys, n.Keys[i])
		newValues = append(newValues, n.Values[i])
		i++
	}
	for j < len(keys) {
		newKeys = append(newKeys, keys[j])
		newValues = append(newValues, values[j])
		j++
	}

	// Replace with new slices
	n.Keys = newKeys
	n.Values = newValues

	return nil
}

// InsertChild inserts a key and child node pointer into an internal node.
func (n *Node) InsertChild(key []byte, child *Node) error {
	if len(key) == 0 {
		return ErrInvalidKey
	}

	if n.IsLeaf {
		return errors.New("InsertChild: use Insert for leaf nodes")
	}

	if len(n.Keys) >= model.DefaultMaxKeys {
		return ErrNodeFull
	}

	// Find insertion position
	idx := n.Search(key)

	// Insert key
	n.Keys = append(n.Keys, nil)
	copy(n.Keys[idx+1:], n.Keys[idx:])
	n.Keys[idx] = key

	// Insert child (right side of the key)
	n.Children = append(n.Children, nil)
	copy(n.Children[idx+2:], n.Children[idx+1:])
	n.Children[idx+1] = child

	return nil
}

// Split splits a full node into two nodes.
// Returns the new node and the median key to promote to parent.
func (n *Node) Split() (*Node, []byte, error) {
	if len(n.Keys) < model.DefaultMaxKeys {
		return nil, nil, ErrNodeNotFull
	}

	// Find median index
	mid := (model.DefaultMaxKeys - 1) / 2
	medianKey := n.Keys[mid]

	// Create new node for right half
	rightNode := NewNode(n.IsLeaf)

	if n.IsLeaf {
		// Leaf node: median key is COPIED to parent, stays in left node
		// Left: keys[0..mid], Right: keys[mid+1..end]
		rightNode.Keys = append(rightNode.Keys, n.Keys[mid+1:]...)
		rightNode.Values = append(rightNode.Values, n.Values[mid+1:]...)

		// Left node keeps keys[0..mid] (including median, which is copied)
		n.Keys = n.Keys[:mid+1]
		n.Values = n.Values[:mid+1]
	} else {
		// Internal node: median key is MOVED to parent, removed from left node
		// Left: keys[0..mid-1], Right: keys[mid+1..end]
		rightNode.Keys = append(rightNode.Keys, n.Keys[mid+1:]...)
		rightNode.Children = append(rightNode.Children, n.Children[mid+1:]...)

		// Left node keeps keys[0..mid-1] and children[0..mid]
		n.Keys = n.Keys[:mid]
		n.Children = n.Children[:mid+1]
	}

	return rightNode, medianKey, nil
}

// Merge merges another node into this node.
// Only valid when both nodes are below minimum capacity.
func (n *Node) Merge(other *Node) error {
	if n.IsLeaf != other.IsLeaf {
		return errors.New("Merge: cannot merge different node types")
	}

	totalKeys := len(n.Keys) + len(other.Keys)
	if totalKeys > model.DefaultMaxKeys {
		return errors.New("Merge: merged node would exceed capacity")
	}

	// Append keys and values/children from other node
	n.Keys = append(n.Keys, other.Keys...)

	if n.IsLeaf {
		n.Values = append(n.Values, other.Values...)
	} else {
		n.Children = append(n.Children, other.Children...)
	}

	return nil
}

// Delete removes a key from the node.
// For leaf nodes, removes the key-value pair.
// For internal nodes, removes the key and adjusts children.
func (n *Node) Delete(key []byte) error {
	idx := n.Search(key)
	if idx >= len(n.Keys) || !bytes.Equal(n.Keys[idx], key) {
		return ErrKeyNotFound
	}

	if n.IsLeaf {
		// Remove key-value pair
		n.Keys = append(n.Keys[:idx], n.Keys[idx+1:]...)
		n.Values = append(n.Values[:idx], n.Values[idx+1:]...)
	} else {
		// Remove key and child
		n.Keys = append(n.Keys[:idx], n.Keys[idx+1:]...)
		// Note: child removal requires more complex logic in BTree
	}

	return nil
}

// Size returns the number of keys in the node.
func (n *Node) Size() int {
	return len(n.Keys)
}

// IsEmpty returns true if the node has no keys.
func (n *Node) IsEmpty() bool {
	return len(n.Keys) == 0
}

// IsFull returns true if the node has reached maximum capacity.
func (n *Node) IsFull() bool {
	return len(n.Keys) >= model.DefaultMaxKeys
}

// IsUnderflow returns true if the node has fallen below minimum capacity.
func (n *Node) IsUnderflow() bool {
	return len(n.Keys) < model.DefaultMinKeys
}

// Clear removes all keys, values, and children from the node.
func (n *Node) Clear() {
	n.Keys = n.Keys[:0]
	n.Values = n.Values[:0]
	n.Children = n.Children[:0]
}

// Clone creates a shallow copy of the node.
// Simple and efficient implementation using make() + copy().
// Both PageID and ChildIDs are copied for persistence support.
func (n *Node) Clone() *Node {
	clone := &Node{
		PageID:   n.PageID,
		IsLeaf:   n.IsLeaf,
		Keys:     make([][]byte, len(n.Keys), cap(n.Keys)),
		Values:   make([][]byte, len(n.Values), cap(n.Values)),
		Children: make([]*Node, len(n.Children), cap(n.Children)),
		ChildIDs: make([]model.PageID, len(n.ChildIDs), cap(n.ChildIDs)),
	}

	copy(clone.Keys, n.Keys)
	copy(clone.Values, n.Values)
	copy(clone.Children, n.Children)
	copy(clone.ChildIDs, n.ChildIDs)

	return clone
}

// Acquire increments the reference count for this node.
// Used for cache management to prevent premature eviction.
func (n *Node) Acquire() {
	atomic.AddInt32(&n.pinCount, 1)
}

// Release decrements the reference count for this node.
// When the reference count reaches zero, the node can be evicted from cache.
func (n *Node) Release() {
	atomic.AddInt32(&n.pinCount, -1)
}

// PinCount returns the current reference count for this node.
func (n *Node) PinCount() int32 {
	return atomic.LoadInt32(&n.pinCount)
}
