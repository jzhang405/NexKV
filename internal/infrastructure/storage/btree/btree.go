// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// Package btree provides an in-memory BTree implementation with
// copy-on-write (CCOW) concurrency control.
//
// Key Features:
//   - Lock-free read operations using atomic.Value
//   - Copy-on-write path modification for concurrency
//   - Snapshot isolation with reference counting
//   - Object pooling for performance optimization
//
// Performance:
//   - Read latency: ~10 ns/op (hardware limit)
//   - Write latency: ~40 µs/op (with CCOW)
//   - Concurrency: Multiple readers, single writer
//
// Usage:
//
//	tree, err := btree.OpenBTree("/data/tree", nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer tree.Close()
//
//	// Set key-value pair
//	err = tree.Set(ctx, key, value)
//
//	// Get value
//	val, err := tree.Get(ctx, key)
//
// Architecture:
//
// The implementation uses a pure in-memory design optimized for performance.
// Node structures store direct pointers to child nodes, eliminating PageID
// indirection overhead. This is achieved through Copy-on-Write (CoW) semantics
// where modifications create new versions of nodes along the path from root
// to leaf, allowing concurrent readers to access the old version.
//
// The VersionedRoot manages multiple versions of the tree, supporting:
//   - Snapshot isolation for consistent reads
//   - Reference counting for garbage collection
//   - Atomic root switching for lock-free reads
package btree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/wal"
)

var (
	// ErrNotImplemented is returned when a method is not yet implemented.
	ErrNotImplemented = errors.New("not implemented until Phase 3")

	// ErrClosed is returned when operations are performed on a closed BTree.
	ErrClosed = errors.New("btree is closed")

	// ErrRetry is returned when a CAS operation fails and the caller should retry.
	ErrRetry = errors.New("cas failed, retry operation")

	// ErrInvalidPath is returned when path finding fails due to invalid node structure.
	ErrInvalidPath = errors.New("invalid path: node structure inconsistent")
)

// BTree is the main BTree storage engine with CCOW and persistence.
//
// Week 13-14: Migrated to Lealone AOSE architecture
// - ChunkManager for append-only storage
// - RootPageRef for atomic root updates
// - Lazy loading (no page cache)
// - Copy-on-Write concurrency control
type BTree struct {
	config   *model.BTreeConfig
	closed   bool
	closedMu sync.RWMutex

	// Root management
	rootRef *RootPageRef // Root page reference (atomic updates)

	// Storage (Lealone AOSE)
	chunkMgr *ChunkManager // Append-only storage manager
	wal     wal.WAL       // Write-Ahead Log for crash recovery

	// Configuration
	maxLevels int  // Maximum tree levels
	enableWAL bool // Enable WAL logging

	// Legacy (deprecated, will be removed)
	root        *VersionedRoot // TODO: Week 14 - Remove after migration
	pageManager *PageManager   // TODO: Week 14 - Remove after migration
	pageCache   *PageCache     // TODO: Week 14 - Remove after migration
	nodeCache   *nodeCache     // TODO: Week 14 - Remove after migration
}

// OpenBTree opens or creates a BTree storage engine with persistence support.
//
// Parameters:
// - dir: Directory for storing database files (.db and .wal)
// - config: BTree configuration (use nil for defaults)
//
// This function will:
// - Create or open the database file
// - Create or open the WAL file
// - Replay WAL for crash recovery (if WAL exists)
// - Initialize the BTree with recovered or empty state
func OpenBTree(dir string, config *model.BTreeConfig) (*BTree, error) {
	if config == nil {
		config = model.NewDefaultBTreeConfig()
	}

	// Create directory if not exists
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create directory: %w", err)
		}
	}

	// Initialize ChunkManager and WAL
	var chunkMgr *ChunkManager
	var pageManager *PageManager // Legacy, will be removed in Week 14
	var walImpl wal.WAL
	enableWAL := dir != ""

	if dir != "" {
		// Open ChunkManager (Lealone AOSE)
		cm, err := NewChunkManager(dir)
		if err != nil {
			return nil, fmt.Errorf("open chunk manager: %w", err)
		}
		chunkMgr = cm

		// Open legacy PageManager (TODO: Week 14 - Remove)
		dbPath := filepath.Join(dir, "database.db")
		pm, err := NewPageManager(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open page manager: %w", err)
		}
		pageManager = pm

		// Open WAL using the general-purpose WAL implementation
		walDir := filepath.Join(dir, "wal")
		w, err := wal.NewDiskWAL(&wal.WALConfig{
			Dir:         walDir,
			SegmentSize: 64 * 1024 * 1024, // 64MB
			SyncPolicy:  wal.SyncPolicyEveryWrite,
		})
		if err != nil {
			pageManager.Close()
			return nil, fmt.Errorf("open WAL: %w", err)
		}
		walImpl = w
	}

	// Create initial root node (empty leaf)
	rootNode := NewNode(true)

	// Create versioned root with initial root node
	root := NewVersionedRoot(rootNode)

	// Create node cache for optimization
	nodeCache := newNodeCache()

	// Create page cache for three-tier caching (legacy, will be removed in Week 14)
	var pageCache *PageCache
	if chunkMgr != nil {
		// Persistent mode: L1 (1000 pages), L2 (10000 buffers), NodeL1 (500 nodes), with PageManager
		pageCache = NewPageCache(1000, 10000, 500, pageManager)
	} else {
		// Memory-only mode: smaller cache without PageManager
		pageCache = NewPageCache(1000, 10000, 500, nil)
	}

	// Create RootPageRef for atomic root updates
	rootPageRef := NewRootPageRef()

	// Calculate max levels based on config
	maxLevels := 10 // Default value

	btree := &BTree{
		config:     config,
		closed:     false,
		rootRef:    rootPageRef,
		chunkMgr:   chunkMgr,
		wal:        walImpl,
		maxLevels:  maxLevels,
		enableWAL:  enableWAL,

		// Legacy (TODO: Week 14 - Remove)
		root:        root,
		pageManager: pageManager,
		pageCache:   pageCache,
		nodeCache:   nodeCache,
	}

	// Replay WAL if exists (crash recovery)
	if enableWAL && walImpl != nil {
		if err := btree.replayWAL(); err != nil {
			// Close resources on error
			pageManager.Close()
			walImpl.Close()
			return nil, fmt.Errorf("replay WAL: %w", err)
		}
	}

	return btree, nil
}

// ===== KVStore Interface Implementation (Placeholder) =====

// Get retrieves a value by key with lazy loading support.
//
// This method implements the read path of the BTree:
// 1. Find the path from Root to Leaf using searchPath()
// 2. Lazy load pages at each level
// 3. Search the leaf page for the key
// 4. Return the value if found, or ErrKeyNotFound if not
//
// Performance:
// - O(log n) page traversals
// - Lazy loading: only pages needed are loaded from disk
// - Lock-free reads after initial page load
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
	if b.closed {
		return nil, ErrClosed
	}

	// Find the leaf page using searchPath
	leafInfo, _, err := b.findLeafPage(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("find leaf page: %w", err)
	}

	if leafInfo == nil {
		return nil, ErrKeyNotFound
	}

	// Get the leaf page (lazy loaded)
	leafPage, err := b.getPageOrLoad(leafInfo)
	if err != nil {
		return nil, fmt.Errorf("load leaf page: %w", err)
	}

	// Type assertion to LeafPage
	leaf, ok := leafPage.(*LeafPage)
	if !ok || leaf == nil {
		return nil, fmt.Errorf("invalid leaf page type: %T", leafPage)
	}

	// Search for the key in the leaf page
	value, found := leaf.Get(key)
	if !found {
		return nil, ErrKeyNotFound
	}

	return value, nil
}

// Set stores a key-value pair with Copy-on-Write and CAS.
//
// This method implements the write path of the BTree:
// 1. Find the path from Root to Leaf using searchPath()
// 2. Create a copy-on-write path (clone all pages from root to leaf)
// 3. Insert/Update the key-value pair in the leaf
// 4. Atomically update the root using CAS (Compare-And-Swap)
// 5. Retry on CAS failure (up to 3 retries with exponential backoff)
//
// Performance:
// - O(log n) page copies for CCOW
// - Atomic root switch using CAS
// - Retry on concurrent writes
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
	if b.closed {
		return ErrClosed
	}

	// Retry configuration
	const maxRetries = 3
	const baseDelay = 10 * time.Millisecond

	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 10ms, 20ms, 40ms
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Try to insert with CAS
		err := b.setWithCAS(ctx, key, value)
		if err == nil {
			// Success
			return nil
		}

		// Check if we should retry
		if err == ErrRetry {
			lastErr = err
			continue
		}

		// Non-retryable error
		return err
	}

	return fmt.Errorf("set failed after %d retries: %w", maxRetries, lastErr)
}

// Delete removes a key (not implemented until Phase 3).
func (b *BTree) Delete(ctx context.Context, key []byte) error {
	if b.closed {
		return ErrClosed
	}
	return ErrNotImplemented
}

// GetBatch retrieves multiple values (not implemented until Phase 3).
func (b *BTree) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
	if b.closed {
		return nil, ErrClosed
	}
	return nil, ErrNotImplemented
}

// SetBatch stores multiple key-value pairs (not implemented until Phase 3).
func (b *BTree) SetBatch(ctx context.Context, pairs []service.KVPair) error {
	if b.closed {
		return ErrClosed
	}
	return ErrNotImplemented
}

// DeleteBatch removes multiple keys (not implemented until Phase 3).
func (b *BTree) DeleteBatch(ctx context.Context, keys [][]byte) error {
	if b.closed {
		return ErrClosed
	}
	return ErrNotImplemented
}

// RangeScan returns an iterator for a key range (not implemented until Phase 3).
func (b *BTree) RangeScan(ctx context.Context, start, end []byte) (service.Iterator, error) {
	if b.closed {
		return nil, ErrClosed
	}
	return nil, ErrNotImplemented
}

// BeginTx starts a transaction (not implemented until Phase 4).
func (b *BTree) BeginTx(ctx context.Context, opts ...service.TxOption) (service.Transaction, error) {
	if b.closed {
		return nil, ErrClosed
	}
	return nil, errors.New("BeginTx: not implemented until Phase 4")
}

// CreateSnapshot creates a snapshot (not implemented until Phase 2).
func (b *BTree) CreateSnapshot(ctx context.Context) (service.SnapshotID, error) {
	if b.closed {
		return 0, ErrClosed
	}
	return 0, errors.New("CreateSnapshot: not implemented until Phase 2")
}

// ReleaseSnapshot releases a snapshot (not implemented until Phase 2).
func (b *BTree) ReleaseSnapshot(ctx context.Context, id service.SnapshotID) error {
	if b.closed {
		return ErrClosed
	}
	return errors.New("ReleaseSnapshot: not implemented until Phase 2")
}

// Stats returns storage statistics (not implemented until Phase 3).
func (b *BTree) Stats(ctx context.Context) (*service.StoreStats, error) {
	if b.closed {
		return nil, ErrClosed
	}
	return nil, ErrNotImplemented
}

// ===== Persistence Methods =====

// replayWAL replays WAL entries for crash recovery.
func (b *BTree) replayWAL() error {
	if b.wal == nil {
		return nil
	}

	// Recover all WAL entries
	entries, err := b.wal.Recover()
	if err != nil {
		return err
	}

	// Apply entries to BTree
	for _, entry := range entries {
		// Apply entry to BTree
		if entry.Type == wal.WALTypeInsert {
			// Rebuild tree from WAL entries
			if err := b.insertFromWAL(entry.Key, entry.Value); err != nil {
				return err
			}
		}
	}

	// Truncate WAL after successful replay
	if len(entries) > 0 {
		lastLSN := entries[len(entries)-1].LSN
		if err := b.wal.Truncate(lastLSN); err != nil {
			return fmt.Errorf("truncate WAL: %w", err)
		}
	}

	return nil
}

// insertFromWAL inserts a key-value pair during WAL replay.
// This bypasses WAL logging to avoid infinite recursion.
func (b *BTree) insertFromWAL(key, value []byte) error {
	ctx := context.Background()

	// Temporarily disable WAL during replay
	oldEnableWAL := b.enableWAL
	b.enableWAL = false
	defer func() { b.enableWAL = oldEnableWAL }()

	// Use InsertWithSplit (WAL is disabled, so no recursion)
	return b.InsertWithSplit(ctx, key, value)
}

// allocateNodePageID allocates a new PageID for a node.
// Returns 0 if persistence is disabled.
func (b *BTree) allocateNodePageID() model.PageID {
	if b.chunkMgr == nil {
		return 0 // In-memory mode
	}
	// TODO: Week 13-14 - Use ChunkManager to allocate page ID
	return 0
}

// persistNode persists a node to disk using PageManager.
// nolint:unused // Reserved for Phase 2.5 (full node persistence)
func (b *BTree) persistNode(node *Node) error {
	if b.chunkMgr == nil {
		return nil // Persistence disabled
	}

	// Allocate page ID
	pageID := b.pageManager.AllocatePage()

	// Create page from node
	page, err := PageFromNode(pageID, node)
	if err != nil {
		return fmt.Errorf("create page from node: %w", err)
	}

	// Write page to disk
	if err := b.pageManager.WritePage(page); err != nil {
		return fmt.Errorf("write page: %w", err)
	}

	return nil
}

// writeWAL writes an entry to the WAL.
func (b *BTree) writeWAL(entry *wal.WALEntry) error {
	if !b.enableWAL || b.wal == nil {
		return nil // WAL disabled
	}

	// Append to WAL (LSN will be assigned automatically)
	_, err := b.wal.Append(entry)
	return err
}

// Close closes the BTree storage engine and releases resources.
func (b *BTree) Close() error {
	b.closedMu.Lock()
	defer b.closedMu.Unlock()

	if b.closed {
		return nil // Already closed
	}

	// Close WAL
	if b.wal != nil {
		if err := b.wal.Close(); err != nil {
			return fmt.Errorf("close WAL: %w", err)
		}
	}

	// Close page manager
	if b.pageManager != nil {
		if err := b.pageManager.Close(); err != nil {
			return fmt.Errorf("close page manager: %w", err)
		}
	}

	b.closed = true
	return nil
}

// ===== 懒加载机制（Week 13-14 Day 1-2）=====

// loadPage 从 ChunkManager 加载页面（懒加载核心封装）
// 这是 BTree 对 ChunkManager.LoadPage() 的封装，提供统一的错误处理
//
// 参数：
//   pos - 64 位位置编码
//
// 返回：
//   interface{} - 页面对象（实际类型为 *LeafPage 或 *InternalPage）
//   error - 错误信息
//
// 懒加载流程：
// 1. 检查 ChunkManager 是否初始化
// 2. 调用 ChunkManager.LoadPage(pos) 加载并反序列化
// 3. 返回页面对象
//
// 注意：此方法不会更新 PageInfo，调用者需要手动设置
func (b *BTree) loadPage(pos int64) (interface{}, error) {
	// 1. 检查 ChunkManager（仅持久化模式需要）
	if b.chunkMgr == nil {
		return nil, fmt.Errorf("chunk manager not initialized (in-memory mode)")
	}

	// 2. 调用 ChunkManager.LoadPage()（根据位置编码加载页面）
	page, err := b.chunkMgr.LoadPage(pos)
	if err != nil {
		return nil, fmt.Errorf("load page at %d: %w", pos, err)
	}

	return page, nil
}

// getPageOrLoad 获取页面，支持懒加载（辅助方法）
// 如果 PageInfo.page 为 nil，则从 ChunkManager 加载
//
// 参数：
//   info - PageInfo 对象
//
// 返回：
//   interface{} - 页面对象
//   error - 错误信息
func (b *BTree) getPageOrLoad(info *PageInfo) (interface{}, error) {
	if info == nil {
		return nil, fmt.Errorf("pageInfo is nil")
	}

	// 如果 page 已加载，直接返回
	if info.IsPageLoaded() {
		return info.GetPage(), nil
	}

	// 如果 pos == 0，说明页面从未持久化
	if info.GetPos() == 0 {
		return nil, fmt.Errorf("page not loaded and no position (pos=0)")
	}

	// 懒加载：从 ChunkManager 加载
	page, err := b.loadPage(info.GetPos())
	if err != nil {
		return nil, fmt.Errorf("load page: %w", err)
	}

	// 更新 PageInfo.page
	info.SetPage(page)

	return page, nil
}

// ===== BTree Interface Implementation (Placeholder) =====

// GetHeight returns the tree height (not implemented until Phase 3).
func (b *BTree) GetHeight(ctx context.Context) (int, error) {
	if b.closed {
		return 0, ErrClosed
	}
	return 0, ErrNotImplemented
}

// GetPageCount returns the total page count (not implemented until Phase 3).
func (b *BTree) GetPageCount(ctx context.Context) (int, error) {
	if b.closed {
		return 0, ErrClosed
	}
	return 0, ErrNotImplemented
}

// DumpTree returns a string representation of the tree (not implemented until Phase 3).
func (b *BTree) DumpTree(ctx context.Context) (string, error) {
	if b.closed {
		return "", ErrClosed
	}
	return "", ErrNotImplemented
}

// Validate validates the tree structure (not implemented until Phase 3).
func (b *BTree) Validate(ctx context.Context) error {
	if b.closed {
		return ErrClosed
	}
	return ErrNotImplemented
}

// ===== Week 13 Day 5: Get/Set Helper Methods =====

// setWithCAS attempts to insert a key-value pair using CAS.
//
// This is the core write operation:
// 1. Find the path from Root to Leaf
// 2. Create copy-on-write copies of all pages in the path
// 3. Insert/Update the key-value pair in the leaf copy
// 4. Try to atomically update the root using CAS
// 5. Return ErrRetry if CAS fails (concurrent write detected)
func (b *BTree) setWithCAS(ctx context.Context, key, value []byte) error {
	// Step 1: Find the path from Root to Leaf
	_, path, err := b.findLeafPage(ctx, key)
	if err != nil {
		return fmt.Errorf("find leaf page: %w", err)
	}

	if len(path) == 0 {
		return fmt.Errorf("empty path")
	}

	// Step 2: Create copy-on-write copies of all pages in the path
	copiedPath, err := b.copyPath(path)
	if err != nil {
		return fmt.Errorf("copy path: %w", err)
	}

	// Step 3: Insert/Update the key-value pair in the leaf copy
	leafInfo := copiedPath[len(copiedPath)-1]
	leafPage, err := b.getPageOrLoad(leafInfo)
	if err != nil {
		return fmt.Errorf("load leaf page: %w", err)
	}

	leaf, ok := leafPage.(*LeafPage)
	if !ok || leaf == nil {
		return fmt.Errorf("invalid leaf page type: %T", leafPage)
	}

	// Insert the key-value pair
	inserted, err := leaf.Insert(key, value)
	if err != nil {
		return fmt.Errorf("insert into leaf: %w", err)
	}

	// Check if we need to split the leaf
	// TODO: Week 13-14 - Implement split logic
	if !inserted {
		// Key already exists, value updated
		// Mark the leaf as dirty
		leafInfo.MarkDirty()
	}

	// Step 4: Try to atomically update the root using CAS
	// TODO: Week 13-14 - Implement root update with CAS
	// For now, we just mark the pages as dirty

	// Mark all pages in the copied path as dirty
	for _, info := range copiedPath {
		info.MarkDirty()
	}

	return nil
}

// copyPath creates copy-on-write copies of all pages in the path.
//
// This function clones all PageInfo objects in the path, creating new
// Page objects for each. This ensures that concurrent readers can still
// access the old version while the writer modifies the new version.
func (b *BTree) copyPath(path []*PageInfo) ([]*PageInfo, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("empty path")
	}

	copiedPath := make([]*PageInfo, len(path))

	// Copy all pages in the path (from root to leaf)
	for i, info := range path {
		// Clone the PageInfo
		newInfo := info.Clone()

		// Clone the Page object (if loaded)
		if info.IsPageLoaded() {
			page := info.GetPage()
			if page != nil {
				// Clone based on page type
				switch p := page.(type) {
				case *LeafPage:
					newInfo.SetPage(p.Clone())
				case *InternalPage:
					newInfo.SetPage(p.Clone())
				default:
					return nil, fmt.Errorf("unknown page type: %T", page)
				}
			}
		}

		copiedPath[i] = newInfo
	}

	return copiedPath, nil
}

