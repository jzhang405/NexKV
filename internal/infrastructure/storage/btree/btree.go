// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license
// that can be found in the LICENSE file.

package btree

import (
	"context"
	"sync/atomic"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
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
	metrics *BTreeMetrics        // performance counters (optional)
	tracer  Tracer               // operation tracer for debugging (optional)
	tsGen   mvcc.TSGenerator     // MVCC timestamp generator (Phase 2a)
}

// Verify BTree implements service.KVStore at compile time.
var _ service.KVStore = (*BTree)(nil)

// NewBTree creates a new BTree backed by the given storage.
// Initializes with a single empty leaf page as root.
func NewBTree(storage *OffheapBTreeStorage) (*BTree, error) {
	return NewBTreeWithMetricsAndTracer(storage, nil, nil)
}

// NewBTreeWithMetrics creates a new BTree with optional metrics collection.
// If metrics is nil, no metrics are collected.
func NewBTreeWithMetrics(storage *OffheapBTreeStorage, metrics *BTreeMetrics) (*BTree, error) {
	return NewBTreeWithMetricsAndTracer(storage, metrics, nil)
}

// NewBTreeWithMetricsAndTracer creates a new BTree with optional metrics and tracer.
// If metrics is nil, no metrics are collected.
// If tracer is nil, DefaultTracer is used.
func NewBTreeWithMetricsAndTracer(storage *OffheapBTreeStorage, metrics *BTreeMetrics, tracer Tracer) (*BTree, error) {
	pageID, err := storage.AllocLeafPage()
	if err != nil {
		return nil, errpkg.BTreeInitRootLeaf(err)
	}

	if tracer == nil {
		tracer = DefaultTracer
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
		metrics: metrics,
		tracer:  tracer,
		tsGen:   mvcc.NewLocalTS(),
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
// Returns ErrKeyNotFound if the key does not exist or has been tombstoned.
// Read path is lock-free: searchPath → read leaf → parse flag → return value.
func (b *BTree) Get(_ context.Context, key []byte) ([]byte, error) {
	if err := b.checkOpen(); err != nil {
		return nil, err
	}

	// Search path to leaf (retains all PageRefs)
	path, err := searchPath(b.rootRef, key)
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

	// Update metrics
	if b.metrics != nil {
		b.metrics.ReadCount.Add(1)
	}

	// 解析 MVCC Value（Phase 2a）：Flag + beginTS + realVal
	raw := leaf.GetValue(idx)
	flag, _, realVal := offheap.ParseValueWithMVCC(raw)
	if flag == offheap.FlagTombstone {
		return nil, ErrKeyNotFound
	}
	return realVal, nil
}

// GetRaw returns the complete MVCC-encoded value for the given key.
// Unlike Get, it does not filter Tombstone entries — callers see Flag + beginTS + realVal.
// Returns ErrKeyNotFound only if the key does not physically exist in the tree.
// Used by MVCC readers that need to inspect beginTS or Tombstone status.
func (b *BTree) GetRaw(_ context.Context, key []byte) ([]byte, error) {
	if err := b.checkOpen(); err != nil {
		return nil, err
	}

	path, err := searchPath(b.rootRef, key)
	if err != nil {
		return nil, err
	}
	defer path.ReleaseAll()

	leafEntry := path.Leaf()
	pInfo := leafEntry.Ref.GetPageInfo()
	if pInfo == nil {
		return nil, ErrPageFreed
	}

	leaf, err := b.storage.GetLeafPage(pInfo.PageID)
	if err != nil {
		return nil, err
	}

	idx, found := leaf.Search(key)
	if !found {
		return nil, ErrKeyNotFound
	}

	if b.metrics != nil {
		b.metrics.ReadCount.Add(1)
	}

	// leaf.GetValue(idx) returns a deepCopy (Go heap), safe to return directly
	return leaf.GetValue(idx), nil
}

// Set inserts or updates a key-value pair.
// Uses CAS retry loop for concurrent safety.
// If the key already exists, updates the value (upsert semantics).
// If the key was previously tombstoned, restores it with delta=+1.
func (b *BTree) Set(_ context.Context, key, value []byte) error {
	if err := b.checkOpen(); err != nil {
		return err
	}

	err := writeOperation(b, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if found {
			// Update existing key — 检查 Tombstone 恢复
			raw := leaf.GetValue(idx)
			flag, _, _ := offheap.ParseValueWithMVCC(raw)
			newLeaf, err := leaf.Update(idx, offheap.BuildMVCCValue(offheap.FlagNormal, b.tsGen.NextTS(), value))
			if err != nil {
				return nil, err
			}
			delta := int64(0)
			if flag == offheap.FlagTombstone {
				delta = +1 // Tombstone 恢复：Key 重新可见
			}
			return &leafMutation{
				newPageID: newLeaf.PageID(),
				delta:     delta,
			}, nil
		}

		// Insert new key — Value 带 MVCC Header（Phase 2a）
		newLeaf, err := leaf.Insert(key, offheap.BuildMVCCValue(offheap.FlagNormal, b.tsGen.NextTS(), value))
		if err != nil {
			return nil, err
		}
		return &leafMutation{
			newPageID: newLeaf.PageID(),
			delta:     1,
		}, nil
	})

	if err == nil && b.metrics != nil {
		b.metrics.WriteCount.Add(1)
	}

	return err
}

// Delete logically removes the given key from the B+Tree via Tombstone.
// Returns ErrKeyNotFound if the key does not exist or is already tombstoned.
// Physical deletion is deferred to Phase 3 Compaction.
func (b *BTree) Delete(_ context.Context, key []byte) error {
	if err := b.checkOpen(); err != nil {
		return err
	}

	err := writeOperation(b, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if !found {
			return nil, ErrKeyNotFound
		}

		// 防重复删除：检查现有 Flag
		raw := leaf.GetValue(idx)
		flag, _, _ := offheap.ParseValueWithMVCC(raw)
		if flag == offheap.FlagTombstone {
			return nil, ErrKeyNotFound // 已删除，no-op
		}

		// Tombstone：Phase 2a 使用 9-byte MVCC header，若原 Value >= 9B 走快路径，否则 Delete+Insert
		tombstoneVal := offheap.BuildMVCCValue(offheap.FlagTombstone, b.tsGen.NextTS(), nil)
		newLeaf, err := leaf.Update(idx, tombstoneVal)
		if err != nil {
			return nil, err
		}
		return &leafMutation{
			newPageID: newLeaf.PageID(),
			delta:     -1,
		}, nil
	})

	if err == nil && b.metrics != nil {
		b.metrics.DeleteCount.Add(1)
	}

	return err
}

// Size returns the number of key-value pairs in the tree.
// After Tombstone: Size = logical visible key count (excludes tombstoned keys).
func (b *BTree) Size() int64 {
	return b.size.Load()
}

// GetMetrics returns a snapshot of current performance metrics.
// Returns nil if metrics collection is not enabled.
func (b *BTree) GetMetrics() *MetricsSnapshot {
	if b.metrics == nil {
		return nil
	}
	snapshot := b.metrics.Snapshot()
	return &snapshot
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
