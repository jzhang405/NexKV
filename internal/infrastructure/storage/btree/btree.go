// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license
// that can be found in the LICENSE file.

package btree

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/checkpoint"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/chunk"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/lob"
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
	compactWp      WatermarkProvider        // Phase 6.5: GC-safe watermark for compaction
	compactMu      sync.Mutex
	epochMgr       *EpochManager // COW old page reclamation (nil if disabled)
	epochCancel    context.CancelFunc
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
	lobMgr := lob.NewDefaultLOBManager(storage.GetPageManager())

	// Phase 6 Tier 2: LOB file storage (nil if no lobDir configured)
	var lobFileMgr mvcc.LOBFileManager
	if cfg.lobDir != "" {
		var err error
		lobFileMgr, err = lob.NewDefaultLOBFileManager(cfg.lobDir)
		if err != nil {
			storage.Close()
			return nil, errpkg.BTreeCreatePageManager(err)
		}
	}

	bt.txMgr = cfg.buildTxManager(newStorageAdapter(bt), lobMgr, lobFileMgr)

	// EpochManager: optional COW old-page reclamation
	if cfg.enableEpoch {
		em := NewEpochManager(func(id model.PageID) { _ = storage.FreePage(id) })
		ctx, cancel := context.WithCancel(context.Background())
		em.StartBackgroundReclaim(ctx)
		bt.epochMgr = em
		bt.epochCancel = cancel
	}

	return bt, nil
}

// SetChunkManager injects the ChunkManager and PageSerializer for AO persistence (Phase 4.3).
func (b *BTree) SetChunkManager(cm service.ChunkManager, serializer *chunk.PageSerializer) {
	b.storage.SetChunkManager(cm, serializer)
}

// DirtyBytes returns the number of COW-allocated bytes since the last ResetDirtyBytes.
// O(1) atomic read — Lealone collectDirtyMemory() equivalent.
func (b *BTree) DirtyBytes() uint64 { return b.storage.dirtyBytes.Load() }

// ResetDirtyBytes resets the dirty byte counter after a successful checkpoint save.
func (b *BTree) ResetDirtyBytes() { b.storage.dirtyBytes.Store(0) }

// EnumeratePages performs post-order DFS from the BTree root, returning pre-serialized page data.
// Children appear before parents (Children-First semantics) so parent writes are safe.
// Each page is Retained during traversal and Released after children are processed,
// preventing concurrent Free+Alloc TOCTOU.
//
// Implements checkpoint.BTreeScanner.EnumeratePages.
func (b *BTree) EnumeratePages(_ checkpoint.PageRef) ([]checkpoint.PageFlushItem, error) {
	if b.storage.serializer == nil {
		return nil, errpkg.Wrap(errpkg.ErrBTreeNotImplemented, "EnumeratePages requires ChunkManager (call SetChunkManager first)")
	}
	return b.enumeratePagesInternal(b.rootRef)
}

func (b *BTree) enumeratePagesInternal(root *RootPageRef) ([]checkpoint.PageFlushItem, error) {
	// visited guards against infinite recursion from structural bugs (安全网)
	visited := make(map[model.PageID]bool)
	var items []checkpoint.PageFlushItem
	var dfs func(ref *PageRef) error

	dfs = func(ref *PageRef) error {
		pi := ref.GetPageInfo()
		if visited[pi.PageID] {
			return nil
		}
		visited[pi.PageID] = true

		// Retain to prevent concurrent Free during DFS
		ref.Retain()
		defer ref.Release()

		// Post-order: process children first
		if !pi.IsLeaf {
			cc := ref.GetChildren()
			if cc != nil {
				for _, childRef := range cc.Children {
					if childRef != nil {
						if err := dfs(childRef); err != nil {
							return err
						}
					}
				}
			}
		}

		// Serialize current page
		pageType := uint8(0) // index
		if pi.IsLeaf {
			pageType = 1 // leaf
		}

		item := checkpoint.PageFlushItem{
			PageID:   pi.PageID,
			PageType: pageType,
			ChunkPos: pi.ChunkPos,
		}

		// If page is not yet persisted, serialize it
		if pi.ChunkPos == 0 {
			ptr := b.storage.pm.PageIDToPtr(uint32(pi.PageID))
			data, serErr := b.storage.serializer.Serialize(ptr, chunk.MaxPagePayload)
			if serErr != nil {
				return errpkg.Wrap(serErr, "checkpoint: serialize page")
			}
			item.PageData = data
		}

		items = append(items, item)
		return nil
	}

	if err := dfs(&root.PageRef); err != nil {
		return nil, err
	}
	return items, nil
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

	// Epoch: protect the full read (searchPath + page data access)
	var epochSlot int
	if b.epochMgr != nil {
		epochSlot = b.epochMgr.AllocSlot()
		b.epochMgr.EnterRead(epochSlot)
		defer b.epochMgr.ExitRead(epochSlot)
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
	defer leaf.Release()

	// Search for key
	idx, found := leaf.Search(key)
	if !found {
		return nil, ErrKeyNotFound
	}

	// Update metrics
	if b.metrics != nil {
		b.metrics.ReadCount.Add(1)
	}

	// GetValue returns mmap sub-slice (zero-copy). Copy before returning:
	// epoch ExitRead runs via defer at end of Get, invalidating mmap slice.
	raw := leaf.GetValue(idx)
	mvccVal, err := mvcc.ParseMVCC(raw)
	if err != nil {
		return nil, err
	}
	if mvccVal.IsTombstone() {
		return nil, ErrKeyNotFound
	}
	val := make([]byte, len(mvccVal.RealVal))
	copy(val, mvccVal.RealVal)
	return val, nil
}

// getRawBytes returns the raw MVCC-encoded bytes for key directly from the leaf page.
// The returned slice is a copy and safe to retain. Used by GetRaw and GetWithMeta.
func (b *BTree) getRawBytes(key []byte) ([]byte, error) {
	if err := b.checkOpen(); err != nil {
		return nil, err
	}

	var epochSlot int
	if b.epochMgr != nil {
		epochSlot = b.epochMgr.AllocSlot()
		b.epochMgr.EnterRead(epochSlot)
		defer b.epochMgr.ExitRead(epochSlot)
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

	// Copy mmap sub-slice before epoch ExitRead (defer above)
	raw := leaf.GetValue(idx)
	cp := make([]byte, len(raw))
	copy(cp, raw)
	return cp, nil
}

// GetRaw returns the complete MVCC-encoded value for the given key.
// Unlike Get, it does not filter Tombstone entries -- callers see Flag + beginTS + realVal.
// Returns ErrKeyNotFound only if the key does not physically exist in the tree.
// Used by MVCC readers that need to inspect beginTS or Tombstone status.
func (b *BTree) GetRaw(_ context.Context, key []byte) (mvcc.MVCCValue, error) {
	raw, err := b.getRawBytes(key)
	if err != nil {
		return mvcc.MVCCValue{}, err
	}
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
			encoded, buildErr := mvcc.BuildMVCC(mvcc.FlagNormal, b.tsGen.NextTS(), value, 0, 0, nil)
			if buildErr != nil {
				return nil, buildErr
			}
			// CAS-first in-place: check if new value fits old slot
			if lh, ok := leaf.(*leafPageHandle); ok && lh.TryInPlace(idx, encoded) {
				delta := int64(0)
				tombstoneDelta := int16(0)
				if mvccVal.IsTombstone() {
					delta = +1
					tombstoneDelta = -1
				}
				return &leafMutation{
					newPageID:      lh.PageID(),
					delta:          delta,
					tombstoneDelta: tombstoneDelta,
					inPlace:        true,
					inPlaceIdx:     idx,
					inPlaceValue:   encoded,
				}, nil
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
		encoded, buildErr := mvcc.BuildMVCC(mvcc.FlagNormal, b.tsGen.NextTS(), value, 0, 0, nil)
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
	}, MaxCASRetries)

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
		tombstoneVal, buildErr := mvcc.BuildMVCC(mvcc.FlagTombstone, b.tsGen.NextTS(), nil, 0, 0, nil)
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
	}, MaxCASRetries)

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
	// Shutdown epoch manager
	if b.epochCancel != nil {
		b.epochCancel()
		b.epochMgr.Shutdown()
	}
	// Shutdown BatchWriter (if initialized): close channels, wait for workers to drain
	return b.storage.Close()
}

// AfterCheckpoint triggers an explicit reclamation pass after a checkpoint.
// Best-effort: no-op if EpochManager is not enabled.
func (b *BTree) AfterCheckpoint() {
	if b.epochMgr != nil {
		b.epochMgr.tryReclaim()
	}
}

// --- service.KVStore batch operations ---

// GetBatch reads multiple keys in a single epoch+searchPath window.
// All keys are processed on the same leaf page (single-layer Phase 5 BTree),
// sharing one epoch Acquire→ExitRead for all values.
//
// Missing/tombstone keys return nil in results. Callers MUST check results[i]==nil.
func (b *BTree) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
	if err := b.checkOpen(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}

	n := len(keys)

	// If root is internal (multi-layer), fall back to individual Gets.
	// Single-page optimization only works when all keys are on the root leaf.
	if !b.rootRef.GetPageInfo().IsLeaf {
		return b.getBatchMultiPage(ctx, keys)
	}

	// --- Single-page optimized path (root is leaf) ---

	// Track original positions: sort by key for sequential leaf access,
	// then map back after retrieval.
	type indexedKey struct {
		idx int
		key []byte
	}
	indexed := make([]indexedKey, n)
	for i, k := range keys {
		indexed[i] = indexedKey{idx: i, key: k}
	}
	sort.Slice(indexed, func(i, j int) bool {
		return string(indexed[i].key) < string(indexed[j].key)
	})

	// Single epoch window for all keys
	var epochSlot int
	if b.epochMgr != nil {
		epochSlot = b.epochMgr.AllocSlot()
		b.epochMgr.EnterRead(epochSlot)
		defer b.epochMgr.ExitRead(epochSlot)
	}

	// Single searchPath — all keys on same page (root is leaf)
	path, err := searchPath(b.rootRef, indexed[0].key)
	if err != nil {
		return nil, err
	}
	defer path.ReleaseAll()

	pInfo := path.Leaf().Ref.GetPageInfo()
	if pInfo == nil {
		return nil, ErrPageFreed
	}

	leaf, err := b.storage.GetLeafPage(pInfo.PageID)
	if err != nil {
		return nil, err
	}
	defer leaf.Release()

	// Batch Search + GetValue + ParseMVCC + tombstone check
	rawResults := make([][]byte, n)
	for i, ik := range indexed {
		idx, found := leaf.Search(ik.key)
		if !found {
			continue // nil = missing
		}
		raw := leaf.GetValue(idx) // mmap sub-slice

		mv, parseErr := mvcc.ParseMVCC(raw)
		if parseErr != nil || mv.IsTombstone() {
			continue // nil = tombstone / parse error
		}

		// Copy before epoch ExitRead (defer above)
		val := make([]byte, len(mv.RealVal))
		copy(val, mv.RealVal)
		rawResults[i] = val
	}

	// Map back to original key order
	results := make([][]byte, n)
	for i, ik := range indexed {
		results[ik.idx] = rawResults[i]
	}

	if b.metrics != nil {
		b.metrics.ReadCount.Add(int64(n))
	}

	return results, nil
}

// getBatchMultiPage reads keys individually when root is internal (multi-layer BTree).
func (b *BTree) getBatchMultiPage(ctx context.Context, keys [][]byte) ([][]byte, error) {
	results := make([][]byte, len(keys))
	for i, key := range keys {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		val, err := b.Get(ctx, key)
		if errors.Is(err, ErrKeyNotFound) {
			continue // nil = missing/tombstone
		}
		if err != nil {
			return nil, err
		}
		results[i] = val
	}
	return results, nil
}

// getBatchRawBytes reads raw MVCC-encoded bytes for multiple keys in one
// searchPath+epoch window. Used by SnapshotTx.GetBatch via btreeStorageAdapter.
func (b *BTree) getBatchRawBytes(ctx context.Context, keys [][]byte) ([][]byte, error) {
	if err := b.checkOpen(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}

	n := len(keys)

	// If root is internal, fall back to individual gets
	if !b.rootRef.GetPageInfo().IsLeaf {
		results := make([][]byte, n)
		for i, key := range keys {
			val, err := b.getRawBytes(key)
			if errors.Is(err, ErrKeyNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			results[i] = val
		}
		return results, nil
	}

	// --- Single-page batch path ---
	type indexedKey struct {
		idx int
		key []byte
	}
	indexed := make([]indexedKey, n)
	for i, k := range keys {
		indexed[i] = indexedKey{idx: i, key: k}
	}
	sort.Slice(indexed, func(i, j int) bool {
		return string(indexed[i].key) < string(indexed[j].key)
	})

	// Single epoch window
	var epochSlot int
	if b.epochMgr != nil {
		epochSlot = b.epochMgr.AllocSlot()
		b.epochMgr.EnterRead(epochSlot)
		defer b.epochMgr.ExitRead(epochSlot)
	}

	path, err := searchPath(b.rootRef, indexed[0].key)
	if err != nil {
		return nil, err
	}
	defer path.ReleaseAll()

	pInfo := path.Leaf().Ref.GetPageInfo()
	if pInfo == nil {
		return nil, ErrPageFreed
	}

	leaf, err := b.storage.GetLeafPage(pInfo.PageID)
	if err != nil {
		return nil, err
	}
	defer leaf.Release()

	rawResults := make([][]byte, n)
	for i, ik := range indexed {
		idx, found := leaf.Search(ik.key)
		if !found {
			continue // nil = missing
		}
		raw := leaf.GetValue(idx) // mmap sub-slice
		// Copy before epoch ExitRead
		cp := make([]byte, len(raw))
		copy(cp, raw)
		rawResults[i] = cp
	}

	// Map back to original order
	results := make([][]byte, n)
	for i, ik := range indexed {
		results[ik.idx] = rawResults[i]
	}

	return results, nil
}

// SetBatch applies multiple KV pairs in segmented COW batches.
// Keys are sorted internally. Each segment fills one page (~90 entries) and
// is published via a single CAS. Total: O(N/pageSize) COWs instead of O(N).
//
// Uses the same writeOperation path as BTree.Set — not PageDispatcher.
func (b *BTree) SetBatch(ctx context.Context, pairs []service.KVPair) error {
	if err := b.checkOpen(); err != nil {
		return err
	}
	if len(pairs) == 0 {
		return nil
	}

	for _, p := range pairs {
		if err := b.SetWithRetry(ctx, p.Key, p.Value, maxCASFastAttempts); err != nil {
			return err
		}
	}

	return nil
}

// DeleteBatch 批量删除。
// V1 简单串行：逐 key 调用 Delete。
func (b *BTree) DeleteBatch(ctx context.Context, keys [][]byte) error {
	if err := b.checkOpen(); err != nil {
		return err
	}
	for _, key := range keys {
		if err := b.Delete(ctx, key); err != nil {
			if errors.Is(err, ErrKeyNotFound) {
				continue
			}
			return err
		}
	}
	return nil
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

// BeginPessimisticTx creates a transaction with pessimistic locking (Phase 1: Lealone RowLock equiv).
// Put/Delete acquire per-key KeyLock eagerly; Commit skips PreCheck.
func (b *BTree) BeginPessimisticTx(ctx context.Context) (service.Transaction, error) {
	if err := b.checkOpen(); err != nil {
		return nil, err
	}
	tx, err := b.txMgr.BeginPessimisticTx(ctx, mvcc.SnapshotIsolation)
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

func (a *txAdapter) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
	return a.tx.GetBatch(ctx, keys)
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

// SetCompactionWatermark sets the watermark provider for tombstone compaction.
// After set, TryCompact() can be called to reclaim space from pages with
// high tombstone ratios. Callers should wire this to ActiveTxRegistry.Watermark()
// at server startup, and trigger TryCompact() after each Checkpoint.
func (b *BTree) SetCompactionWatermark(wp WatermarkProvider) {
	b.compactMu.Lock()
	b.compactWp = wp
	b.compactMu.Unlock()
}

// TryCompact performs a single tombstone compaction cycle if a watermark
// provider has been configured. Best-effort: returns nil even if compaction
// is skipped (no provider, tree closed) or partially fails.
func (b *BTree) TryCompact() error {
	b.compactMu.Lock()
	wp := b.compactWp
	b.compactMu.Unlock()
	if wp == nil {
		return nil
	}
	if err := b.checkOpen(); err != nil {
		return nil
	}
	return b.Compact(wp)
}
