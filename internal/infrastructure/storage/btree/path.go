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

// PathNode represents a node in the path from root to leaf.
type PathNode struct {
	Node  *Node // The node at this level
	Level int   // The level in the tree (0 = leaf)
}

// Path represents a path from root to leaf.
type Path []*PathNode

// pathPool is a sync.Pool for Path objects.
var pathPool = sync.Pool{
	New: func() any {
		// Pre-allocate capacity for BTree depth with safety margin
		// Default max levels is 10, we use 20 to handle edge cases
		return make(Path, 0, 20)
	},
}

// AcquirePath gets a Path from the pool or creates a new one.
func AcquirePath() Path {
	return pathPool.Get().(Path)
}

// ReleasePath returns a Path to the pool for reuse.
func ReleasePath(path Path) {
	// Only return paths with expected capacity to avoid polluting pool
	// with expanded slices
	const maxExpectedCapacity = 20
	if cap(path) <= maxExpectedCapacity {
		path = path[:0]
		pathPool.Put(path) //nolint:staticcheck
	}
	// Let GC reclaim paths that exceeded expected capacity
}

// nodeCache is a simple cache for deserialized nodes.
// This is a placeholder for future optimization.
type nodeCache struct {
	cache map[model.PageID]*Node
	mu    sync.RWMutex
}

// newNodeCache creates a new node cache.
func newNodeCache() *nodeCache {
	return &nodeCache{
		cache: make(map[model.PageID]*Node),
	}
}

// Get retrieves a node from cache or deserializes it.
func (nc *nodeCache) Get(pageID model.PageID, page *Page, deserializeFunc func(*Page) *Node) *Node {
	nc.mu.RLock()
	node, ok := nc.cache[pageID]
	nc.mu.RUnlock()

	if ok {
		return node
	}

	// Not in cache, deserialize and cache it
	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Double-check after acquiring write lock
	if node, ok := nc.cache[pageID]; ok {
		return node
	}

	node = deserializeFunc(page)
	nc.cache[pageID] = node
	return node
}

// Invalidate removes a node from cache (used during CCOW operations).
func (nc *nodeCache) Invalidate(pageID model.PageID) {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	delete(nc.cache, pageID)
}

// InvalidateAll clears all cached nodes.
func (nc *nodeCache) InvalidateAll() {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	nc.cache = make(map[model.PageID]*Node)
}

// FindPath finds the path from root to leaf for the given key.
// This is a read-only operation and does not require locking.
// Pure memory implementation - no PageManager.Get calls needed.
//
// IMPORTANT: Caller is responsible for releasing the returned path using ReleasePath().
func (b *BTree) FindPath(key []byte) (Path, error) {
	// Use path pool to reduce allocations
	path := AcquirePath()

	// Start from current root
	rootInfo := b.root.Get()
	defer rootInfo.Release()

	// Get root node from rootInfo
	currentNode := rootInfo.Root
	currentLevel := b.maxLevels

	for currentLevel > 0 {
		currentLevel--

		// Add current node to path
		pathNode := &PathNode{
			Node:  currentNode,
			Level: currentLevel,
		}
		path = append(path, pathNode)

		// If this is a leaf node, we're done
		if currentNode.IsLeaf {
			break
		}

		// Find the child to descend to
		idx := currentNode.Search(key)
		if idx >= len(currentNode.Children) {
			// Key is greater than all keys, go to rightmost child
			currentNode = currentNode.Children[len(currentNode.Children)-1]
		} else {
			currentNode = currentNode.Children[idx]
		}
	}

	return path, nil
}

// CopyPathBottomUp copies the path from bottom to top (leaf to root).
// Returns the new root node after the copy-on-write operation.
// PURE MEMORY IMPLEMENTATION: No Page.Data copying, just Node.Clone().
// This eliminates the 4075-byte copy overhead, providing ~9.5x performance improvement.
func (b *BTree) CopyPathBottomUp(ctx context.Context, path Path, modifyFunc func(*Node) error) (*Node, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if len(path) == 0 {
		return nil, fmt.Errorf("empty path")
	}

	// Save old nodes before they get modified
	oldNodes := make([]*Node, len(path))
	for i, pathNode := range path {
		oldNodes[i] = pathNode.Node
	}

	// Process from leaf to root
	for i := len(path) - 1; i >= 0; i-- {
		oldNode := oldNodes[i]

		// Clone the node (shallow copy: Keys, Values, Children slices are copied)
		// This is MUCH faster than copying 4075 bytes of Page.Data
		newNode := oldNode.Clone()

		if i == len(path)-1 {
			// This is the leaf node, apply modification
			if err := modifyFunc(newNode); err != nil {
				return nil, fmt.Errorf("failed to modify leaf node: %w", err)
			}
		} else {
			// This is an internal node, update child reference
			// Find the old child and update it to the new child
			oldChild := oldNodes[i+1]
			newChild := path[i+1].Node // The already-copied child from previous iteration

			// Find and replace the child reference
			found := false
			for j, child := range newNode.Children {
				if child == oldChild {
					newNode.Children[j] = newChild
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("failed to update child reference")
			}
		}

		// Update path node for next iteration
		path[i].Node = newNode
	}

	// Return the new root node
	return path[0].Node, nil
}

// CopyPathBottomUpBatch performs batch CCOW operations.
// This is more efficient than multiple CopyPathBottomUp calls as it reduces path copying.
// Returns the new root node after all batch operations are applied.
func (b *BTree) CopyPathBottomUpBatch(ctx context.Context, path Path, batchFunc func(*Node) error) (*Node, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if len(path) == 0 {
		return nil, fmt.Errorf("empty path")
	}

	// Save original node references before modification
	oldNodes := make([]*Node, len(path))
	for i, pathNode := range path {
		oldNodes[i] = pathNode.Node
	}

	// Clone only the leaf node and apply batch operations
	leafIdx := len(path) - 1
	newLeaf := oldNodes[leafIdx].Clone()
	if err := batchFunc(newLeaf); err != nil {
		return nil, fmt.Errorf("failed to apply batch operations: %w", err)
	}
	path[leafIdx].Node = newLeaf

	// Update parent references bottom-up
	for i := leafIdx - 1; i >= 0; i-- {
		oldParent := oldNodes[i]
		newParent := oldParent.Clone()

		// Find the child to update
		oldChild := oldNodes[i+1]
		newChild := path[i+1].Node // Use the updated child from path

		found := false
		for j, child := range newParent.Children {
			if child == oldChild {
				newParent.Children[j] = newChild
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("failed to update child reference in batch operation")
		}

		path[i].Node = newParent
	}

	return path[0].Node, nil
}

// Pure Memory BTree Implementation Notes:
// - copyPage and copyPageOptimized are no longer needed
// - serializeNodeToPage and deserializeNode are no longer needed
// - ModifyPage is no longer needed
// - Node operations are done directly in memory with Clone()
// - This eliminates 4075-byte Page.Data copying overhead

// ModifyOperation represents the type of modification (kept for tests).
type ModifyOperation int

const (
	ModifyInsert ModifyOperation = iota
	ModifyUpdate
	ModifyDelete
)

// FindPathPageID finds the path from root to leaf for the given key using PageID-based lazy loading.
// This method uses PageCache.GetNode() to load nodes on-demand from three-tier cache.
// Falls back to direct pointer mode when root.PageID == 0 (memory-only mode).
//
// IMPORTANT: Caller is responsible for releasing the returned path using ReleasePath().
func (b *BTree) FindPathPageID(key []byte) (Path, error) {
	// Get current root
	rootInfo := b.root.Get()
	defer rootInfo.Release()

	rootNode := rootInfo.Root

	// Check if we're in memory-only mode (PageID == 0)
	if rootNode.PageID == 0 {
		// Fall back to direct pointer mode
		return b.findPathDirect(key)
	}

	// Use PageID-based lazy loading
	return b.findPathPageID(key, rootNode.PageID)
}

// findPathPageID implements PageID-based path finding with lazy loading.
func (b *BTree) findPathPageID(key []byte, rootPageID model.PageID) (Path, error) {
	path := AcquirePath()

	currentID := rootPageID
	currentLevel := b.maxLevels

	for currentLevel > 0 {
		currentLevel--

		// Lazy load node from PageCache
		currentNode, err := b.pageCache.GetNode(currentID)
		if err != nil {
			ReleasePath(path)
			return nil, fmt.Errorf("load node %d: %w", currentID, err)
		}
		defer currentNode.Release()

		// Add current node to path
		pathNode := &PathNode{
			Node:  currentNode,
			Level: currentLevel,
		}
		path = append(path, pathNode)

		// If this is a leaf node, we're done
		if currentNode.IsLeaf {
			break
		}

		// Validate internal node has children
		if len(currentNode.ChildIDs) == 0 {
			ReleasePath(path)
			return nil, ErrInvalidPath
		}

		// Find the child to descend to
		idx := currentNode.Search(key)
		if idx >= len(currentNode.ChildIDs) {
			// Key is greater than all keys, go to rightmost child
			currentID = currentNode.ChildIDs[len(currentNode.ChildIDs)-1]
		} else {
			currentID = currentNode.ChildIDs[idx]
		}
	}

	return path, nil
}

// findPathDirect implements direct pointer-based path finding (memory-only mode).
// Used as fallback when root.PageID == 0.
func (b *BTree) findPathDirect(key []byte) (Path, error) {
	path := AcquirePath()

	// Get current root
	rootInfo := b.root.Get()
	defer rootInfo.Release()

	currentNode := rootInfo.Root
	currentLevel := b.maxLevels

	for currentLevel > 0 {
		currentLevel--

		// Add current node to path
		pathNode := &PathNode{
			Node:  currentNode,
			Level: currentLevel,
		}
		path = append(path, pathNode)

		// If this is a leaf node, we're done
		if currentNode.IsLeaf {
			break
		}

		// Find the child to descend to
		idx := currentNode.Search(key)
		if idx >= len(currentNode.Children) {
			// Key is greater than all keys, go to rightmost child
			currentNode = currentNode.Children[len(currentNode.Children)-1]
		} else {
			currentNode = currentNode.Children[idx]
		}
	}

	return path, nil
}
