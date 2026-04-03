// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"sync/atomic"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// BTree is a concurrent B+Tree with COW semantics.
// Implements service.KVStore for basic Get/Set/Delete operations.
//
// Phase 5 scope: single-leaf operations without split propagation.
// Multi-level trees and split handling are added in Phase 6.
type BTree struct {
	rootRef *RootPageRef         // CAS-replaceable root reference
	storage *OffheapBTreeStorage // page storage backend
	size    atomic.Int64         // KV pair count
	closed  atomic.Bool          // closed flag
}

// Verify BTree implements service.KVStore at compile time.
var _ service.KVStore = (*BTree)(nil)

// NewBTree creates a new BTree backed by the given storage.
// Initializes with a single empty leaf page as root.
func NewBTree(storage *OffheapBTreeStorage) (*BTree, error) {
	pageID, err := storage.AllocLeafPage()
	if err != nil {
		return nil, errpkg.BTreeInitRootLeaf(err)
	}

	// Phase 5: COW场景下页面由writeOperation显式管理
	// PageRef.freeFunc仅在PageRef销毁时调用（如树关闭时）
	// 包装storage.FreePage以匹配func(model.PageID)签名
	rootRef := NewRootPageRef(pageID, 1, func(id model.PageID) {
		_ = storage.FreePage(id)
	})
	return &BTree{
		rootRef: rootRef,
		storage: storage,
	}, nil
}

// checkOpen returns ErrTreeClosed if the tree is closed.
func (b *BTree) checkOpen() error {
	if b.closed.Load() {
		return ErrTreeClosed
	}
	return nil
}

// Get retrieves the value for the given key.
// Returns ErrKeyNotFound if the key does not exist.
// Read path is lock-free: searchPath → read leaf → return value.
func (b *BTree) Get(_ context.Context, key []byte) ([]byte, error) {
	if err := b.checkOpen(); err != nil {
		return nil, err
	}

	// Search path to leaf (retains all PageRefs)
	path, err := searchPath(b.storage, b.rootRef, key)
	if err != nil {
		return nil, err
	}
	defer path.ReleaseAll()

	// Get leaf page
	leafEntry := path.Leaf()
	pInfo := leafEntry.Ref.GetPageInfo()
	if pInfo == nil {
		return nil, ErrPageFreed
	}

	leaf, err := b.storage.GetLeafPage(pInfo.PageID)
	if err != nil {
		return nil, err
	}

	// Search for key
	idx, found := leaf.Search(key)
	if !found {
		return nil, ErrKeyNotFound
	}

	return leaf.GetValue(idx), nil
}

// Set inserts or updates a key-value pair.
// Uses CAS retry loop for concurrent safety.
// If the key already exists, updates the value (upsert semantics).
func (b *BTree) Set(_ context.Context, key, value []byte) error {
	if err := b.checkOpen(); err != nil {
		return err
	}

	return writeOperation(b, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if found {
			// Update existing key
			newLeaf, err := leaf.Update(idx, value)
			if err != nil {
				return nil, err
			}
			return &leafMutation{
				newPageID: newLeaf.PageID(),
				delta:     0, // update doesn't change count
			}, nil
		}

		// Insert new key
		newLeaf, err := leaf.Insert(key, value)
		if err != nil {
			return nil, err
		}
		return &leafMutation{
			newPageID: newLeaf.PageID(),
			delta:     1,
		}, nil
	})
}

// Delete removes the given key from the B+Tree.
// Returns ErrKeyNotFound if the key does not exist.
func (b *BTree) Delete(_ context.Context, key []byte) error {
	if err := b.checkOpen(); err != nil {
		return err
	}

	return writeOperation(b, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if !found {
			return nil, ErrKeyNotFound
		}

		newLeaf, err := leaf.Delete(idx)
		if err != nil {
			return nil, err
		}
		return &leafMutation{
			newPageID: newLeaf.PageID(),
			delta:     -1,
		}, nil
	})
}

// Size returns the number of key-value pairs in the tree.
func (b *BTree) Size() int64 {
	return b.size.Load()
}

// Close closes the BTree and releases all resources.
// Idempotent: subsequent Close calls are no-op.
func (b *BTree) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}
	return b.storage.Close()
}

// --- service.KVStore stubs (not in Phase 5 scope) ---

func (b *BTree) GetBatch(_ context.Context, _ [][]byte) ([][]byte, error) {
	return nil, ErrNotImplemented
}

func (b *BTree) SetBatch(_ context.Context, _ []service.KVPair) error {
	return ErrNotImplemented
}

func (b *BTree) DeleteBatch(_ context.Context, _ [][]byte) error {
	return ErrNotImplemented
}

func (b *BTree) RangeScan(_ context.Context, _, _ []byte) (service.Iterator, error) {
	return nil, ErrNotImplemented
}

func (b *BTree) BeginTx(_ context.Context, _ ...service.TxOption) (service.Transaction, error) {
	return nil, ErrNotImplemented
}

func (b *BTree) CreateSnapshot(_ context.Context) (service.SnapshotID, error) {
	return 0, ErrNotImplemented
}

func (b *BTree) ReleaseSnapshot(_ context.Context, _ service.SnapshotID) error {
	return ErrNotImplemented
}

func (b *BTree) Stats(_ context.Context) (*service.StoreStats, error) {
	return nil, ErrNotImplemented
}
