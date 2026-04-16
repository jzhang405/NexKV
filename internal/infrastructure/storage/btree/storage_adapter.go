// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a AGPL-3.0-style
// license that can be found in the LICENSE file.

package btree

import (
	"context"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// btreeStorageAdapter adapts a BTree to implement mvcc.StorageBackend.
// It bypasses the BTree's built-in MVCC encoding (BuildMVCC/ParseMVCC)
// and directly writes/reads raw encoded bytes from leaf pages.
// The transaction layer handles MVCC encoding/decoding.
type btreeStorageAdapter struct {
	tree *BTree
}

// newStorageAdapter creates a StorageBackend adapter for the given BTree.
func newStorageAdapter(tree *BTree) mvcc.StorageBackend {
	return &btreeStorageAdapter{tree: tree}
}

// GetRaw returns the full MVCC-encoded value as a Go-heap copy.
// Bypasses tombstone filtering — returns raw bytes from the leaf page.
func (a *btreeStorageAdapter) GetRaw(_ context.Context, key []byte) ([]byte, error) {
	if err := a.tree.checkOpen(); err != nil {
		return nil, err
	}

	path, err := searchPath(a.tree.rootRef, key)
	if err != nil {
		return nil, err
	}
	defer path.ReleaseAll()

	leafEntry := path.Leaf()
	pInfo := leafEntry.Ref.GetPageInfo()
	if pInfo == nil {
		return nil, ErrPageFreed
	}

	leaf, err := a.tree.storage.GetLeafPage(pInfo.PageID)
	if err != nil {
		return nil, err
	}

	idx, found := leaf.Search(key)
	if !found {
		return nil, mvcc.ErrKeyNotFound
	}

	// leaf.GetValue already returns a heap copy (mmap-safe)
	return leaf.GetValue(idx), nil
}

// Set writes a pre-encoded MVCC value to the B+Tree.
// Unlike BTree.Set, this does NOT call BuildMVCC — the caller provides
// the fully encoded value (flag + beginTS + realVal).
func (a *btreeStorageAdapter) Set(_ context.Context, key, value []byte) error {
	if err := a.tree.checkOpen(); err != nil {
		return err
	}

	err := writeOperation(a.tree, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if found {
			// Detect Tombstone→Normal transition for size accounting
			var delta int64
			oldVal := leaf.GetValue(idx)
			oldMVCC, parseErr := mvcc.ParseMVCC(oldVal)
			if parseErr == nil && oldMVCC.IsTombstone() {
				newMVCC, newParseErr := mvcc.ParseMVCC(value)
				if newParseErr == nil && !newMVCC.IsTombstone() {
					delta = 1 // Tombstone→Normal: key becomes visible
				}
			}

			newLeaf, updateErr := leaf.Update(idx, value)
			if updateErr != nil {
				return nil, updateErr
			}
			return &leafMutation{
				newPageID: newLeaf.PageID(),
				delta:     delta,
			}, nil
		}

		// Insert new key with raw encoded value
		newLeaf, insertErr := leaf.Insert(key, value)
		if insertErr != nil {
			return nil, insertErr
		}
		return &leafMutation{
			newPageID: newLeaf.PageID(),
			delta:     1,
		}, nil
	})

	if err == nil && a.tree.metrics != nil {
		a.tree.metrics.WriteCount.Add(1)
	}

	return err
}

// Delete physically removes the key from the B+Tree.
// For MVCC transactions, tombstone encoding is handled by the transaction layer;
// this adapter only handles the case where the transaction needs to physically
// restore a previous value (rollback) or write a tombstone.
// Note: StorageBackend.Delete is for physical removal; MVCC uses Set with tombstone flag.
func (a *btreeStorageAdapter) Delete(_ context.Context, key []byte) error {
	if err := a.tree.checkOpen(); err != nil {
		return err
	}

	err := writeOperation(a.tree, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if !found {
			return nil, mvcc.ErrKeyNotFound
		}

		newLeaf, deleteErr := leaf.Delete(idx)
		if deleteErr != nil {
			return nil, deleteErr
		}
		return &leafMutation{
			newPageID: newLeaf.PageID(),
			delta:     -1,
		}, nil
	})

	if err == nil && a.tree.metrics != nil {
		a.tree.metrics.DeleteCount.Add(1)
	}

	return err
}
