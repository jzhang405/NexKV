// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license
// that can be found in the LICENSE file.

package btree

import (
	"context"
	"sync/atomic"
	"time"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/mvcc"
)

// BTree is a concurrent B+Tree with COW semantics.
// Implements service.KVStore for basic Get/Set/Delete operations.
//
// Phase 5 scope: single-leaf operations without split propagation.
// Multi-level trees and split handling are added in Phase 6.
type BTree struct {
	rootRef        *RootPageRef             // CAS-replaceable root reference
	storage        *OffheapBTreeStorage     // page storage backend
	size           atomic.Int64             // KV pair count
	closed         atomic.Bool              // closed flag
	metrics        *BTreeMetrics            // performance counters (optional)
	latencyMetrics *BTreeMetricsWithLatency // latency histograms (optional)
	tracer         Tracer                   // operation tracer for debugging (optional)
	tsGen          mvcc.TSGenerator         // MVCC timestamp generator (Phase 2a)
	txMgr          mvcc.TxManager           // MVCC transaction manager (Phase 2b)
}

// Verify BTree implements service.KVStore at compile time.
var _ service.KVStore = (*BTree)(nil)

// NewBTree creates a new BTree backed by the given storage.
// Initializes with a single empty leaf page as root.
func NewBTree(storage *OffheapBTreeStorage, opts ...BTreeOption) (*BTree, error) {
	cfg := newBTreeConfig(opts...)
	return newBTreeWithConfig(storage, cfg)
}

// NewBTreeWithMetrics creates a new BTree with optional metrics collection.
// If metrics is nil, no metrics are collected.
//
// Deprecated: Use NewBTree(storage, WithMetrics(metrics)) instead.
func NewBTreeWithMetrics(storage *OffheapBTreeStorage, metrics *BTreeMetrics) (*BTree, error) {
	return NewBTree(storage, WithMetrics(metrics))
}

// NewBTreeWithMetricsAndTracer creates a new BTree with optional metrics and tracer.
// If metrics is nil, no metrics are collected.
// If tracer is nil, DefaultTracer is used.
//
// Deprecated: Use NewBTree(storage, WithMetrics(metrics), WithTracer(tracer)) instead.
func NewBTreeWithMetricsAndTracer(storage *OffheapBTreeStorage, metrics *BTreeMetrics, tracer Tracer) (*BTree, error) {
	return NewBTree(storage, WithMetrics(metrics), WithTracer(tracer))
}

// newBTreeWithConfig creates a BTree from a resolved config.
func newBTreeWithConfig(storage *OffheapBTreeStorage, cfg *btreeConfig) (*BTree, error) {
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

	bt := &BTree{
		rootRef:        rootRef,
		storage:        storage,
		metrics:        cfg.metrics,
		latencyMetrics: cfg.latencyMetrics,
		tracer:         cfg.tracer,
		tsGen:          cfg.tsGen,
	}
	// TxManager uses btreeStorageAdapter (bypasses BTree's MVCC encoding)
	// — transaction layer handles BuildMVCC/ParseMVCC internally.
	bt.txMgr = cfg.buildTxManager(newStorageAdapter(bt))

	return bt, nil
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
// Read path is lock-free: searchPath -> read leaf -> parse MVCC -> return value.
func (b *BTree) Get(_ context.Context, key []byte) ([]byte, error) {
	if b.latencyMetrics != nil {
		start := time.Now()
		defer func() { b.latencyMetrics.ReadLat.Record(time.Since(start)) }()
	}

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

	// Decode MVCC value (Phase 2a): Flag + beginTS + realVal
	raw := leaf.GetValue(idx)
	mvccVal, err := mvcc.ParseMVCC(raw)
	if err != nil {
		return nil, err
	}
	if mvccVal.IsTombstone() {
		return nil, ErrKeyNotFound
	}
	return mvccVal.RealVal, nil
}

// GetRaw returns the complete MVCC-encoded value for the given key.
// Unlike Get, it does not filter Tombstone entries -- callers see Flag + beginTS + realVal.
// Returns ErrKeyNotFound only if the key does not physically exist in the tree.
// Used by MVCC readers that need to inspect beginTS or Tombstone status.
func (b *BTree) GetRaw(_ context.Context, key []byte) (*mvcc.MVCCValue, error) {
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

	// Decode raw MVCC value (deepCopy from leaf.GetValue)
	raw := leaf.GetValue(idx)
	return mvcc.ParseMVCC(raw)
}

// Set inserts or updates a key-value pair.
// Uses CAS retry loop for concurrent safety.
// If the key already exists, updates the value (upsert semantics).
// If the key was previously tombstoned, restores it with delta=+1.
func (b *BTree) Set(_ context.Context, key, value []byte) error {
	if b.latencyMetrics != nil {
		start := time.Now()
		defer func() { b.latencyMetrics.WriteLat.Record(time.Since(start)) }()
	}

	if err := b.checkOpen(); err != nil {
		return err
	}

	err := writeOperation(b, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if found {
			// Update existing key -- check Tombstone recovery
			raw := leaf.GetValue(idx)
			mvccVal, parseErr := mvcc.ParseMVCC(raw)
			if parseErr != nil {
				return nil, parseErr
			}
			encoded, buildErr := mvcc.BuildMVCC(mvcc.FlagNormal, b.tsGen.NextTS(), value)
			if buildErr != nil {
				return nil, buildErr
			}
			newLeaf, updateErr := leaf.Update(idx, encoded)
			if updateErr != nil {
				return nil, updateErr
			}
			delta := int64(0)
			tombstoneDelta := int16(0)
			if mvccVal.IsTombstone() {
				delta = +1          // Tombstone recovery: key becomes visible again
				tombstoneDelta = -1 // tombstone removed from count
			}
			return &leafMutation{
				newPageID:      newLeaf.PageID(),
				delta:          delta,
				tombstoneDelta: tombstoneDelta,
			}, nil
		}

		// Insert new key -- value with MVCC header (Phase 2a)
		encoded, buildErr := mvcc.BuildMVCC(mvcc.FlagNormal, b.tsGen.NextTS(), value)
		if buildErr != nil {
			return nil, buildErr
		}
		newLeaf, insertErr := leaf.Insert(key, encoded)
		if insertErr != nil {
			return nil, insertErr
		}
		return &leafMutation{
			newPageID:      newLeaf.PageID(),
			delta:          1,
			tombstoneDelta: 0,
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
	if b.latencyMetrics != nil {
		start := time.Now()
		defer func() { b.latencyMetrics.WriteLat.Record(time.Since(start)) }()
	}

	if err := b.checkOpen(); err != nil {
		return err
	}

	err := writeOperation(b, key, func(leaf LeafPage) (*leafMutation, error) {
		idx, found := leaf.Search(key)
		if !found {
			return nil, ErrKeyNotFound
		}

		// Prevent double delete: check existing flag
		raw := leaf.GetValue(idx)
		mvccVal, parseErr := mvcc.ParseMVCC(raw)
		if parseErr != nil {
			return nil, parseErr
		}
		if mvccVal.IsTombstone() {
			return nil, ErrKeyNotFound // already deleted, no-op
		}

		// Tombstone: Phase 2a uses 9-byte MVCC header
		tombstoneVal, buildErr := mvcc.BuildMVCC(mvcc.FlagTombstone, b.tsGen.NextTS(), nil)
		if buildErr != nil {
			return nil, buildErr
		}
		newLeaf, updateErr := leaf.Update(idx, tombstoneVal)
		if updateErr != nil {
			return nil, updateErr
		}
		return &leafMutation{
			newPageID:      newLeaf.PageID(),
			delta:          -1,
			tombstoneDelta: 1,
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

// metricsWithLatency returns the latency metrics instance. Internal API for testing.
func (b *BTree) metricsWithLatency() *BTreeMetricsWithLatency {
	return b.latencyMetrics
}

// RootPage returns the current root PageRef for checkpoint DFS traversal.
// The returned PageRef is a snapshot-safe reference — COW guarantees that
// concurrent writes create new pages without modifying the old root subtree.
func (b *BTree) RootPage() *PageRef {
	return &b.rootRef.PageRef
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

func (b *BTree) BeginTx(ctx context.Context, _ ...service.TxOption) (service.Transaction, error) {
	if err := b.checkOpen(); err != nil {
		return nil, err
	}
	tx, err := b.txMgr.BeginTx(ctx, mvcc.SnapshotIsolation)
	if err != nil {
		return nil, err
	}
	return &txAdapter{tx: tx}, nil
}

// txAdapter adapts mvcc.Tx to service.Transaction.
// mvcc.Tx has context-free Put/Delete/Rollback; service.Transaction passes ctx.
type txAdapter struct {
	tx mvcc.Tx
}

func (a *txAdapter) Get(ctx context.Context, key []byte) ([]byte, error) {
	return a.tx.Get(ctx, key)
}

func (a *txAdapter) Set(_ context.Context, key, value []byte) error {
	return a.tx.Put(key, value)
}

func (a *txAdapter) Delete(_ context.Context, key []byte) error {
	return a.tx.Delete(key)
}

func (a *txAdapter) Commit(ctx context.Context) error {
	return a.tx.Commit(ctx)
}

func (a *txAdapter) Rollback(_ context.Context) error {
	return a.tx.Rollback()
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
