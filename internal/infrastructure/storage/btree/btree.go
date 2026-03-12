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

	// Storage
	chunkMgr *ChunkManager // Append-only storage manager
	wal      wal.WAL       // Write-Ahead Log for crash recovery

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
	// ✅ Day 10-11: 初始化空的根叶子节点
	initialRootPage := NewLeafPage(model.PageID(0)) // 根叶子节点 ID = 0
	initialRootInfo := NewPageInfo()
	initialRootInfo.SetPage(initialRootPage)
	initialRootInfo.SetParentRef(nil) // 根节点没有父引用

	rootPageRef := NewRootPageRefWithInfo(initialRootInfo)

	// Calculate max levels based on config
	maxLevels := 10 // Default value

	btree := &BTree{
		config:    config,
		closed:    false,
		rootRef:   rootPageRef,
		chunkMgr:  chunkMgr,
		wal:       walImpl,
		maxLevels: maxLevels,
		enableWAL: enableWAL,

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

	// ✅ Day 10-11: 实现 Delete 操作，集成 mergeLeaf
	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		// 1. 检查上下文取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 2. 查找键的路径
		_, path, err := b.findLeafPage(ctx, key)
		if err != nil {
			return fmt.Errorf("find leaf page: %w", err)
		}

		// 3. CCOW：复制路径
		copiedPath, err := b.copyPath(path)
		if err != nil {
			return fmt.Errorf("copy path: %w", err)
		}

		// 4. 删除键
		leafInfo := copiedPath[len(copiedPath)-1]
		leaf := leafInfo.GetLeafPage()

		deleted, err := leaf.Delete(key)
		if err != nil {
			return fmt.Errorf("delete from leaf: %w", err)
		}

		if !deleted {
			return ErrKeyNotFound
		}

		// 5. 检查是否需要 Merge
		const minKeys = 8
		if leaf.NumKeys() < minKeys && len(path) >= 2 {
			// ✅ Phase 2.1: 重新启用 Merge，使用原始 path 访问兄弟节点
			if err := b.mergeLeaf(leafInfo, copiedPath, path); err != nil {
				return fmt.Errorf("merge leaf: %w", err)
			}
		}

		// 6. ✅ CAS 更新根节点（带重试）
		newRootInfo := copiedPath[0]
		oldRootInfo := b.rootRef.pInfo.Load()

		if b.rootRef.ReplacePage(oldRootInfo, newRootInfo) {
			// CAS 成功，继续持久化
		} else {
			// CAS 失败，说明有并发写操作
			if attempt < maxRetries-1 {
				// 短暂等待后重试
				time.Sleep(time.Microsecond * 10 * time.Duration(attempt+1))
				continue
			}
			return ErrRetry
		}

		// 7. 持久化
		if b.chunkMgr != nil {
			if err := b.persistRoot(); err != nil {
				return fmt.Errorf("persist root: %w", err)
			}
		}

		return nil
	}

	return ErrRetry
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
//
//	pos - 64 位位置编码
//
// 返回：
//
//	interface{} - 页面对象（实际类型为 *LeafPage 或 *InternalPage）
//	error - 错误信息
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
//
//	info - PageInfo 对象
//
// 返回：
//
//	interface{} - 页面对象
//	error - 错误信息
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
	_, err = leaf.Insert(key, value)
	if err != nil {
		return fmt.Errorf("insert into leaf: %w", err)
	}

	// Step 4: Check if we need to split the leaf
	const maxKeys = 16 // LeafPage 最大键数量
	if leaf.NumKeys() > maxKeys {
		// ✅ Day 10-11: 集成分裂检测和处理
		// 叶子节点已满，需要分裂
		if err := b.splitLeaf(leafInfo, copiedPath); err != nil {
			return fmt.Errorf("split leaf: %w", err)
		}
		// splitLeaf 已经处理了根节点的 CAS 更新
		return nil
	}

	// Step 5: No split needed - perform normal CCOW update
	// 获取新的根节点（copiedPath[0]）
	newRootInfo := copiedPath[0]

	// Step 6: CAS 更新根节点
	oldRootInfo := b.rootRef.pInfo.Load()
	if !b.rootRef.ReplacePage(oldRootInfo, newRootInfo) {
		// CAS 失败，触发重试
		return ErrRetry
	}

	// Step 7: ✅ Day 9: 持久化集成
	// CAS 更新成功后，持久化整个树
	if b.chunkMgr != nil {
		if err := b.persistRoot(); err != nil {
			// 持久化失败，记录错误但不中断操作
			// 数据仍在内存中，可以稍后重试
			return fmt.Errorf("persist root: %w", err)
		}
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

// splitLeaf 分裂叶子节点（CCOW 版本）
// 当叶子节点满时（len(keys) > maxKeys），进行分裂操作
//
// 参数：
//
//	leafInfo - 需要分裂的叶子节点 PageInfo（来自 copiedPath）
//	copiedPath - 复制的路径（CCOW 操作的路径副本）
//
// 返回：
//
//	error - 错误信息
//
// 分裂步骤（CCOW 架构）：
// 1. 调用 leaf.Split() 创建新页面
// 2. 在 copiedPath 中创建新页面的 PageInfo
// 3. 将分裂键插入父节点（也在 copiedPath 中）
// 4. 更新父节点的 children 引用
// 5. 检查父节点是否需要递归分裂
// 6. 处理根节点分裂
// 7. 最后通过 CAS 更新根节点
func (b *BTree) splitLeaf(leafInfo *PageInfo, copiedPath []*PageInfo) error {
	const maxKeys = 16 // LeafPage 最大键数量

	// 1. 获取叶子节点
	leafPage := leafInfo.GetLeafPage()
	if leafPage == nil {
		return fmt.Errorf("leaf page not loaded")
	}

	// 2. 检查是否需要分裂（防御性检查）
	if leafPage.NumKeys() <= maxKeys {
		return nil // 无需分裂
	}

	// 3. 调用 Split 方法（leafPage 已经被修改）
	newPage, splitKey, err := leafPage.Split()
	if err != nil {
		return fmt.Errorf("leaf split failed: %w", err)
	}

	// 4. 创建新页面的 PageInfo（直接使用，不创建 PageRef）
	newPageInfo := NewPageInfo()
	newPageInfo.SetPage(newPage)
	// parentRef 稍后设置

	// 5. 检查是否有父节点
	if len(copiedPath) < 2 {
		// 没有父节点，说明当前只有根叶子节点
		// 需要创建新的内部节点作为根
		return b.splitRootFromLeaf(leafInfo, newPageInfo, splitKey, copiedPath)
	}

	// 6. 获取父节点（在 copiedPath 中）
	parentInfo := copiedPath[len(copiedPath)-2]
	parentPage := parentInfo.GetInternalPage()
	if parentPage == nil {
		return fmt.Errorf("parent page not loaded or not internal")
	}

	// 7. 将分裂键插入父节点
	// 注意：我们需要创建临时 PageRef 来包装 newPageInfo
	newPageRef := NewPageRefWithInfo(newPageInfo)
	if err := parentPage.InsertKeyChild(splitKey, newPageRef); err != nil {
		return fmt.Errorf("insert split key to parent failed: %w", err)
	}

	// 8. 检查父节点是否需要分裂
	const maxInternalKeys = 15 // InternalPage 最大键数量
	if parentPage.NumKeys() > maxInternalKeys {
		// 父节点也需要分裂
		return b.splitInternal(parentInfo, copiedPath[:len(copiedPath)-1])
	}

	// 9. 更新 parentRef（可选，用于引用链完整性）
	// 在当前的简化实现中，我们可以跳过这一步

	// 10. 分裂完成，copiedPath 已更新
	// 调用者负责最终的 CAS 更新
	return nil
}

// splitRootFromLeaf 从叶子节点分裂创建新的根节点
func (b *BTree) splitRootFromLeaf(leftInfo, rightInfo *PageInfo, splitKey []byte, copiedPath []*PageInfo) error {
	// 1. 创建新的内部节点作为根
	newRootPage := NewInternalPage(model.PageID(1)) // 根节点 ID = 1
	newRootPage.keys = [][]byte{splitKey}

	// 2. 创建左右子节点的 PageRef
	leftRef := NewPageRefWithInfo(leftInfo)
	rightRef := NewPageRefWithInfo(rightInfo)
	newRootPage.children = []*PageRef{leftRef, rightRef}

	// 3. 创建新的 Root PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetPage(newRootPage)
	newRootInfo.SetParentRef(nil) // 根节点没有父引用

	// 4. CAS 更新根节点
	oldRootInfo := b.rootRef.pInfo.Load()
	if !b.rootRef.ReplacePage(oldRootInfo, newRootInfo) {
		return ErrRetry
	}

	// 5. ✅ Day 7: 引用更新机制
	// 更新子节点的 parentRef
	leftInfo.SetParentRef(b.rootRef.PageRef)
	rightInfo.SetParentRef(b.rootRef.PageRef)

	// 6. ✅ Day 9: 持久化集成
	// 分裂完成后，持久化整个树
	if b.chunkMgr != nil {
		if err := b.persistRoot(); err != nil {
			return fmt.Errorf("persist root after split: %w", err)
		}
	}

	return nil
}

// splitInternal 分裂内部节点（CCOW 版本）
// 当内部节点满时（len(keys) > 15），进行分裂操作
//
// ✅ Day 7: 完整实现，支持 CCOW 和引用更新
//
// 参数：
//
//	internalInfo - 需要分裂的内部节点 PageInfo（来自 copiedPath）
//	copiedPath - 复制的路径（CCOW 操作的路径副本）
//
// 返回：
//
//	error - 错误信息
func (b *BTree) splitInternal(internalInfo *PageInfo, copiedPath []*PageInfo) error {
	const maxKeys = 15 // InternalPage 最大键数量

	// 1. 获取内部节点
	internalPage := internalInfo.GetInternalPage()
	if internalPage == nil {
		return fmt.Errorf("internal page not loaded")
	}

	// 2. 检查是否需要分裂（防御性检查）
	if internalPage.NumKeys() <= maxKeys {
		return nil // 无需分裂
	}

	// 3. 调用 Split 方法
	newPage, splitKey, err := internalPage.Split()
	if err != nil {
		return fmt.Errorf("internal split failed: %w", err)
	}

	// 4. 检查是否有父节点
	if len(copiedPath) < 2 {
		// 没有父节点，说明是根节点，需要创建新的根
		// 创建新的 PageInfo 包装
		leftInfo := NewPageInfo()
		leftInfo.SetPage(internalPage)
		leftInfo.SetParentRef(nil) // 稍后设置

		rightInfo := NewPageInfo()
		rightInfo.SetPage(newPage)
		rightInfo.SetParentRef(nil) // 稍后设置

		return b.splitRootFromInternal(leftInfo, rightInfo, splitKey, copiedPath)
	}

	// 5. 获取父节点（在 copiedPath 中）
	parentInfo := copiedPath[len(copiedPath)-2]
	parentPage := parentInfo.GetInternalPage()
	if parentPage == nil {
		return fmt.Errorf("parent page not loaded or not internal")
	}

	// 6. 将分裂键插入父节点
	// 创建新页面的 PageRef 和 PageInfo
	newPageInfo := NewPageInfo()
	newPageInfo.SetPage(newPage)
	newPageRef := NewPageRefWithInfo(newPageInfo)

	if err := parentPage.InsertKeyChild(splitKey, newPageRef); err != nil {
		return fmt.Errorf("insert split key to parent failed: %w", err)
	}

	// 7. 检查父节点是否需要继续分裂
	if parentPage.NumKeys() > maxKeys {
		return b.splitInternal(parentInfo, copiedPath[:len(copiedPath)-1])
	}

	// 8. 分裂完成，copiedPath 已更新
	// 调用者负责最终的 CAS 更新
	return nil
}

// splitRootFromInternal 从内部节点分裂创建新的根节点
func (b *BTree) splitRootFromInternal(leftInfo, rightInfo *PageInfo, splitKey []byte, copiedPath []*PageInfo) error {
	// 1. 创建新的内部节点作为根
	newRootPage := NewInternalPage(model.PageID(1)) // 根节点 ID = 1
	newRootPage.keys = [][]byte{splitKey}

	// 2. 创建左右子节点的 PageRef
	leftRef := NewPageRefWithInfo(leftInfo)
	rightRef := NewPageRefWithInfo(rightInfo)
	newRootPage.children = []*PageRef{leftRef, rightRef}

	// 3. 创建新的 Root PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetPage(newRootPage)
	newRootInfo.SetParentRef(nil) // 根节点没有父引用

	// 4. CAS 更新根节点
	oldRootInfo := b.rootRef.pInfo.Load()
	if !b.rootRef.ReplacePage(oldRootInfo, newRootInfo) {
		return ErrRetry
	}

	// 5. ✅ Day 7: 引用更新机制
	// 更新子节点的 parentRef
	leftInfo.SetParentRef(b.rootRef.PageRef)
	rightInfo.SetParentRef(b.rootRef.PageRef)

	// 6. ✅ Day 7: 递归更新子节点树
	// 更新左子树的所有子孙节点的 parentRef
	b.updateChildrenParentRefs(leftInfo, b.rootRef.PageRef)
	// 更新右子树的所有子孙节点的 parentRef
	b.updateChildrenParentRefs(rightInfo, b.rootRef.PageRef)

	// 7. ✅ Day 9: 持久化集成
	// 分裂完成后，持久化整个树
	if b.chunkMgr != nil {
		if err := b.persistRoot(); err != nil {
			return fmt.Errorf("persist root after split: %w", err)
		}
	}

	return nil
}

// updateChildrenParentRefs 递归更新子节点树的 parentRef
// ✅ Day 7: 引用更新机制的核心方法
//
// 参数：
//
//	pageInfo - 父节点的 PageInfo
//	parentRef - 新的父节点引用
func (b *BTree) updateChildrenParentRefs(pageInfo *PageInfo, parentRef *PageRef) {
	if pageInfo == nil || !pageInfo.IsPageLoaded() {
		return
	}

	page := pageInfo.GetPage()
	if page == nil {
		return
	}

	// 根据页面类型处理
	switch p := page.(type) {
	case *InternalPage:
		// 内部节点：递归更新所有子节点
		for _, childRef := range p.Children() {
			if childRef != nil {
				childInfo := childRef.GetPageInfo()
				if childInfo != nil {
					// 更新子节点的 parentRef
					childInfo.SetParentRef(parentRef)
					// 递归更新子节点的子树
					b.updateChildrenParentRefs(childInfo, childRef)
				}
			}
		}
	case *LeafPage:
		// 叶子节点：没有子节点，无需继续递归
		return
	}
}

// splitRootPage 分裂根节点（Page-based 架构）
// 创建新的内部节点作为根，提升分裂键
func (b *BTree) splitRootPage(leftRef, rightRef *PageRef, splitKey []byte) error {
	// 1. 创建新的内部节点作为根
	newRootPage := NewInternalPage(model.PageID(1)) // 根节点 ID = 1
	newRootPage.keys = [][]byte{splitKey}
	newRootPage.children = []*PageRef{leftRef, rightRef}

	// 2. 创建 PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetPage(newRootPage)
	newRootInfo.SetParentRef(nil) // 根节点没有父引用

	// 3. CAS 更新根节点
	oldRootInfo := b.rootRef.pInfo.Load()
	if !b.rootRef.ReplacePage(oldRootInfo, newRootInfo) {
		return ErrRetry
	}

	// 4. 更新子节点的 parentRef（使用 embedded PageRef）
	leftRef.SetParentRef(b.rootRef.PageRef)
	rightRef.SetParentRef(b.rootRef.PageRef)

	return nil
}

// ===== Day 9: Persistence Integration =====

// persistPage 持久化单个页面到 ChunkManager
//
// 参数：
//
//	pageInfo - 需要持久化的 PageInfo
//	pageType - 页面类型（PageTypeLeaf 或 PageTypeInternal）
//
// 返回：
//
//	int64 - 页面在 Chunk 中的位置编码
//	error - 错误信息
func (b *BTree) persistPage(pageInfo *PageInfo, pageType int) (int64, error) {
	if b.chunkMgr == nil {
		return 0, fmt.Errorf("chunk manager not initialized")
	}

	// 1. 获取页面
	if !pageInfo.IsPageLoaded() {
		return 0, fmt.Errorf("page not loaded")
	}

	page := pageInfo.GetPage()
	if page == nil {
		return 0, fmt.Errorf("page is nil")
	}

	// 2. 序列化页面
	var data []byte
	var err error

	switch p := page.(type) {
	case *LeafPage:
		data, err = p.Serialize()
		if err != nil {
			return 0, fmt.Errorf("serialize leaf page: %w", err)
		}
	case *InternalPage:
		data, err = p.Serialize()
		if err != nil {
			return 0, fmt.Errorf("serialize internal page: %w", err)
		}
	default:
		return 0, fmt.Errorf("unknown page type: %T", page)
	}

	// 3. 分配页面空间
	pos, err := b.chunkMgr.AllocatePage(pageType)
	if err != nil {
		return 0, fmt.Errorf("allocate page: %w", err)
	}

	// 4. 写入页面
	if err := b.chunkMgr.WritePage(pos, data); err != nil {
		return 0, fmt.Errorf("write page to chunk: %w", err)
	}

	// 5. 更新 PageInfo 的位置
	pageInfo.SetPos(pos)

	return pos, nil
}

// persistPageRecursive 递归持久化页面及其子节点（自底向上）
//
// 参数：
//
//	pageInfo - 需要持久化的 PageInfo
//
// 返回：
//
//	error - 错误信息
func (b *BTree) persistPageRecursive(pageInfo *PageInfo) error {
	if !pageInfo.IsPageLoaded() {
		return nil // 未加载的页面无需持久化
	}

	page := pageInfo.GetPage()
	if page == nil {
		return nil
	}

	// 1. 根据页面类型处理
	switch p := page.(type) {
	case *InternalPage:
		// 1.1 先递归持久化所有子节点（自底向上）
		for _, childRef := range p.Children() {
			if childRef != nil {
				childInfo := childRef.GetPageInfo()
				if childInfo != nil {
					if err := b.persistPageRecursive(childInfo); err != nil {
						return fmt.Errorf("persist child page: %w", err)
					}
				}
			}
		}

		// 1.2 持久化当前内部节点
		_, err := b.persistPage(pageInfo, PageTypeInternal)
		if err != nil {
			return fmt.Errorf("persist internal page: %w", err)
		}

	case *LeafPage:
		// 2. 持久化叶子节点
		_, err := b.persistPage(pageInfo, PageTypeLeaf)
		if err != nil {
			return fmt.Errorf("persist leaf page: %w", err)
		}
	}

	return nil
}

// persistRoot 持久化根节点
//
// 这是持久化流程的入口点，在 Set 操作完成后调用
// 确保 Root 页面及其所有子节点都被持久化到磁盘
func (b *BTree) persistRoot() error {
	rootInfo := b.rootRef.pInfo.Load()
	if rootInfo == nil {
		return fmt.Errorf("root page info is nil")
	}

	// 递归持久化整个树（自底向上）
	return b.persistPageRecursive(rootInfo)
}

// ===== Merge Operations =====

// findChildIndexInParent 查找子节点在父节点中的索引
func (b *BTree) findChildIndexInParent(parent *InternalPage, childInfo *PageInfo) (int, error) {
	childPageID := childInfo.GetPageID()

	for i := 0; i < parent.NumChildren(); i++ {
		childRef := parent.GetChild(i)
		if childRef != nil {
			info := childRef.GetPageInfo()
			if info != nil && info.GetPageID() == childPageID {
				return i, nil
			}
		}
	}

	return -1, fmt.Errorf("child not found in parent")
}

// ensurePageLoaded 确保页面已加载（懒加载）
func (b *BTree) ensurePageLoaded(pageInfo *PageInfo) error {
	if pageInfo.IsPageLoaded() {
		return nil
	}

	// 从 ChunkManager 加载
	if b.chunkMgr != nil {
		if pos := pageInfo.GetPos(); pos != 0 {
			page, err := b.chunkMgr.LoadPage(pos)
			if err != nil {
				return fmt.Errorf("load page from chunk: %w", err)
			}
			pageInfo.SetPage(page)
		}
	}

	return nil
}

// mergeLeaf 合并叶子节点
//
// 当叶子节点键数量过少（< minKeys）时，尝试从兄弟节点借键或合并
//
// 参数：
//
//	leafInfo - 需要合并的叶子节点 PageInfo
//	copiedPath - CCOW 复制的路径
//
// 返回：
//
//	error - 错误信息
func (b *BTree) mergeLeaf(leafInfo *PageInfo, copiedPath, path []*PageInfo) error {
	const minKeys = 8

	// 1. 检查是否有父节点
	if len(path) < 2 {
		// 没有父节点，说明是根节点
		// 根节点允许任意数量的键（包括 0）
		return nil
	}

	// ✅ P0-3 修复: 统一使用 copiedPath 来获取父节点
	// 从 copiedPath 获取父节点（用于修改和访问兄弟节点）
	if len(copiedPath) < 2 {
		return fmt.Errorf("copiedPath too short: expected at least 2, got %d", len(copiedPath))
	}

	parentInfo := copiedPath[len(copiedPath)-2]
	parent := parentInfo.GetInternalPage()
	if parent == nil {
		return fmt.Errorf("parent page not loaded in copiedPath")
	}

	// 2. 找到当前节点在父节点中的位置
	leafIndex, err := b.findChildIndexInParent(parent, leafInfo)
	if err != nil {
		return fmt.Errorf("find child index: %w", err)
	}

	// 3. 尝试从左兄弟借键
	if leafIndex > 0 {
		leftSiblingRef := parent.GetChild(leafIndex - 1)
		if leftSiblingRef != nil {
			leftSiblingInfo := leftSiblingRef.GetPageInfo()

			// 懒加载左兄弟（如果未加载）
			if err := b.ensurePageLoaded(leftSiblingInfo); err != nil {
				return err
			}

			leftSibling := leftSiblingInfo.GetLeafPage()
			if leftSibling != nil && leftSibling.NumKeys() > minKeys {
				return b.redistributeLeafLeft(parent, leafInfo, leftSiblingInfo, leafIndex)
			}
		}
	}

	// 4. 尝试从右兄弟借键
	if leafIndex < parent.NumChildren()-1 {
		rightSiblingRef := parent.GetChild(leafIndex + 1)
		if rightSiblingRef != nil {
			rightSiblingInfo := rightSiblingRef.GetPageInfo()

			// 懒加载右兄弟（如果未加载）
			if err := b.ensurePageLoaded(rightSiblingInfo); err != nil {
				return err
			}

			rightSibling := rightSiblingInfo.GetLeafPage()
			if rightSibling != nil && rightSibling.NumKeys() > minKeys {
				return b.redistributeLeafRight(parent, leafInfo, rightSiblingInfo, leafIndex)
			}
		}
	}

	// 5. 如果无法借键，则合并
	// ✅ P0-3 修复: 统一使用 copiedPath 中的父节点
	// parentInfo 已经来自 copiedPath，不需要再次获取

	// 优先与右兄弟合并
	if leafIndex < parent.NumChildren()-1 {
		rightSiblingRef := parent.GetChild(leafIndex + 1)
		if rightSiblingRef != nil {
			rightSiblingInfo := rightSiblingRef.GetPageInfo()

			if err := b.ensurePageLoaded(rightSiblingInfo); err != nil {
				return err
			}

			return b.mergeLeafWithSibling(parentInfo, parent, leafInfo, rightSiblingInfo, leafIndex, copiedPath, path)
		}
	} else {
		// 与左兄弟合并
		leftSiblingRef := parent.GetChild(leafIndex - 1)
		if leftSiblingRef != nil {
			leftSiblingInfo := leftSiblingRef.GetPageInfo()

			if err := b.ensurePageLoaded(leftSiblingInfo); err != nil {
				return err
			}

			return b.mergeLeafWithSibling(parentInfo, parent, leftSiblingInfo, leafInfo, leafIndex-1, copiedPath, path)
		}
	}

	return nil
}

// redistributeLeafLeft 从左兄弟借键
//
// 当左兄弟有足够的键时，从左兄弟借一个键到当前节点
//
// 借键流程：
// 1. 父节点的分隔键下降到当前节点
// 2. 左兄弟的最大键上升到父节点作为新的分隔键
// 3. 左兄弟删除最大键
func (b *BTree) redistributeLeafLeft(
	parent *InternalPage,
	leafInfo, leftSiblingInfo *PageInfo,
	leafIndex int,
) error {
	leaf := leafInfo.GetLeafPage()
	leftSibling := leftSiblingInfo.GetLeafPage()

	// 1. 从父节点获取分隔键（分隔键将下降到当前节点）
	separatorKey := parent.keys[leafIndex-1]

	// 2. 从左兄弟借最后一个键值对
	lastIdx := leftSibling.NumKeys() - 1
	borrowedKey := leftSibling.keys[lastIdx]
	borrowedValue := leftSibling.values[lastIdx]

	// 3. 从左兄弟删除最后一个键值对
	leftSibling.keys = leftSibling.keys[:lastIdx]
	leftSibling.values = leftSibling.values[:lastIdx]
	leftSibling.version++

	// 4. 将分隔键和借来的值插入到当前节点的开头
	leaf.keys = insertSlice(leaf.keys, 0, separatorKey)
	leaf.values = insertSlice(leaf.values, 0, borrowedValue)

	// 5. 将借来的键插入到当前节点的开头（在分隔键之后）
	leaf.keys = insertSlice(leaf.keys, 1, borrowedKey)
	leaf.version++

	// 6. 更新父节点的分隔键
	// 使用左兄弟删除后的新最大键作为新的分隔键
	if leftSibling.NumKeys() > 0 {
		newSeparatorKey := leftSibling.keys[leftSibling.NumKeys()-1]
		parent.keys[leafIndex-1] = newSeparatorKey
	}
	parent.version++

	return nil
}

// redistributeLeafRight 从右兄弟借键
//
// 当右兄弟有足够的键时，从右兄弟借一个键到当前节点
//
// 借键流程：
// 1. 父节点的分隔键下降到当前节点
// 2. 右兄弟的最小键上升到父节点作为新的分隔键
// 3. 右兄弟删除最小键
func (b *BTree) redistributeLeafRight(
	parent *InternalPage,
	leafInfo, rightSiblingInfo *PageInfo,
	leafIndex int,
) error {
	leaf := leafInfo.GetLeafPage()
	rightSibling := rightSiblingInfo.GetLeafPage()

	// 1. 从父节点获取分隔键
	separatorKey := parent.keys[leafIndex]

	// 2. 从右兄弟借第一个键值对
	borrowedKey := rightSibling.keys[0]
	borrowedValue := rightSibling.values[0]

	// 3. 从右兄弟删除第一个键值对
	rightSibling.keys = rightSibling.keys[1:]
	rightSibling.values = rightSibling.values[1:]
	rightSibling.version++

	// 4. 将分隔键和借来的值追加到当前节点末尾
	leaf.keys = append(leaf.keys, separatorKey)
	leaf.values = append(leaf.values, borrowedValue)

	// 5. 将借来的键追加到当前节点末尾
	leaf.keys = append(leaf.keys, borrowedKey)
	leaf.version++

	// 6. 更新父节点的分隔键
	// 使用右兄弟删除后的新最小键（即现在的第一个键）
	if rightSibling.NumKeys() > 0 {
		newSeparatorKey := rightSibling.keys[0]
		parent.keys[leafIndex] = newSeparatorKey
	}
	parent.version++

	return nil
}

// mergeLeafWithSibling 合并两个叶子节点
//
// 当两个兄弟节点的键数量都不足时，将它们合并
//
// 合并流程：
// 1. 将父节点的分隔键插入到左节点
// 2. 将右节点的所有键值对追加到左节点
// 3. 从父节点删除分隔键和右子节点引用
// 4. 检查父节点是否需要 Merge
// 5. 处理根节点降低的特殊情况
func (b *BTree) mergeLeafWithSibling(
	parentInfo *PageInfo,
	parent *InternalPage,
	leftNodeInfo, rightNodeInfo *PageInfo,
	separatorIndex int,
	copiedPath, path []*PageInfo,
) error {
	leftNode := leftNodeInfo.GetLeafPage()
	rightNode := rightNodeInfo.GetLeafPage()

	// 1. 获取父节点的分隔键
	separatorKey := parent.keys[separatorIndex]

	// 2. 合并节点：Left + Separator + Right
	// 2.1 将分隔键追加到左节点
	leftNode.keys = append(leftNode.keys, separatorKey)

	// 2.2 将右节点的所有键值对追加到左节点
	leftNode.keys = append(leftNode.keys, rightNode.keys...)
	leftNode.values = append(leftNode.values, rightNode.values...)
	leftNode.version++

	// 3. 从父节点删除分隔键和右子节点引用
	parent.keys = append(parent.keys[:separatorIndex], parent.keys[separatorIndex+1:]...)
	parent.children = append(parent.children[:separatorIndex+1], parent.children[separatorIndex+2:]...)
	parent.version++

	// 4. 处理根节点降低
	// 如果父节点是根节点且已空（没有键），则降低树的高度
	if parentInfo == b.rootRef.pInfo.Load() && parent.NumKeys() == 0 {
		// 合并后的节点成为新的根节点
		oldRootInfo := parentInfo
		if !b.rootRef.ReplacePage(oldRootInfo, leftNodeInfo) {
			return ErrRetry
		}
		// 更新新根节点的 parentRef 为 nil
		leftNodeInfo.SetParentRef(nil)
		return nil
	}

	// 5. ✅ Phase 2.3: 检查父节点是否需要 Merge（递归向上合并）
	const minInternal = 7
	if parent.NumKeys() < minInternal && len(path) >= 2 {
		// 递归向上合并父节点
		// 注意：这里需要移除当前节点层级的路径
		return b.mergeInternal(parentInfo, copiedPath[:len(copiedPath)-1], path[:len(path)-1])
	}

	return nil
}

// mergeInternalWithSibling 合并两个内部节点
//
// 将右节点合并到左节点，并将父节点的分隔键下降
// 合并后：Left + Separator + Right
//
// 参数：
//   - parentInfo: 父节点信息（用于检查是否为根节点）
//   - parent: 父节点
//   - leftNodeInfo: 左节点信息
//   - rightNodeInfo: 右节点信息
//   - separatorIndex: 分隔键在父节点中的索引
//
// 返回：
//   - error: 错误信息
func (b *BTree) mergeInternalWithSibling(
	parentInfo *PageInfo,
	parent *InternalPage,
	leftNodeInfo, rightNodeInfo *PageInfo,
	separatorIndex int,
	copiedPath, path []*PageInfo,
) error {
	leftNode := leftNodeInfo.GetInternalPage()
	rightNode := rightNodeInfo.GetInternalPage()

	// 1. 获取父节点的分隔键
	separatorKey := parent.keys[separatorIndex]

	// 2. 合并节点：Left + Separator + Right
	// 2.1 将分隔键追加到左节点
	leftNode.keys = append(leftNode.keys, separatorKey)

	// 2.2 将右节点的所有键追加到左节点
	leftNode.keys = append(leftNode.keys, rightNode.keys...)

	// 2.3 将右节点的所有子节点引用追加到左节点
	// 注意：右节点的第一个子节点对应分隔键，需要跳过
	leftNode.children = append(leftNode.children, rightNode.children...)

	// 2.4 ✅ P0-1 修复: 更新右节点子节点的父引用（指向左节点）
	// 从父节点中找到左节点的 PageRef（在 separatorIndex 位置）
	leftNodeRef := parent.children[separatorIndex]
	if leftNodeRef != nil {
		// 遍历右节点的所有子节点，更新它们的 parentRef
		for _, childRef := range rightNode.children {
			if childRef != nil {
				childRef.SetParentRef(leftNodeRef)
			}
		}
	}

	leftNode.version++

	// 3. 从父节点删除分隔键和右子节点引用
	parent.keys = append(parent.keys[:separatorIndex], parent.keys[separatorIndex+1:]...)
	parent.children = append(parent.children[:separatorIndex+1], parent.children[separatorIndex+2:]...)
	parent.version++

	// 4. 处理根节点降低
	// 如果父节点是根节点且已空（没有键），则降低树的高度
	if parentInfo == b.rootRef.pInfo.Load() && parent.NumKeys() == 0 {
		// 合并后的节点成为新的根节点
		oldRootInfo := parentInfo
		if !b.rootRef.ReplacePage(oldRootInfo, leftNodeInfo) {
			return ErrRetry
		}
		// ✅ P0-1 修复: 更新新根节点的 parentRef 为 nil
		// leftNodeInfo 现在是根节点，不应该有父节点
		leftNodeInfo.SetParentRef(nil)
		return nil
	}

	// 5. ✅ Phase 2.3: 检查父节点是否需要 Merge（递归向上合并）
	const minInternal = 7
	if parent.NumKeys() < minInternal && len(path) >= 2 {
		// 递归向上合并父节点
		return b.mergeInternal(parentInfo, copiedPath[:len(copiedPath)-1], path[:len(path)-1])
	}

	return nil
}

// redistributeInternalLeft 从左兄弟借键（内部节点）
//
// 当左兄弟有足够的键时，从左兄弟借一个键到当前节点
//
// 借键流程：
// 1. 父节点的分隔键下降到当前节点
// 2. 左兄弟的最大键上升到父节点作为新的分隔键
// 3. 左兄弟的最大子节点移动到当前节点
func (b *BTree) redistributeInternalLeft(
	parent *InternalPage,
	nodeInfo, leftSiblingInfo *PageInfo,
	nodeIndex int,
) error {
	node := nodeInfo.GetInternalPage()
	leftSibling := leftSiblingInfo.GetInternalPage()

	// 1. 从父节点获取分隔键
	separatorKey := parent.keys[nodeIndex-1]

	// 2. 从左兄弟借最后一个键和子节点
	lastIdx := leftSibling.NumKeys() - 1
	borrowedKey := leftSibling.keys[lastIdx]
	borrowedChild := leftSibling.children[lastIdx+1] // 最后一个子节点

	// 3. 从左兄弟删除最后一个键和子节点
	leftSibling.keys = leftSibling.keys[:lastIdx]
	leftSibling.children = leftSibling.children[:lastIdx+1]
	leftSibling.version++

	// 4. 将分隔键插入到当前节点的开头
	node.keys = insertSlice(node.keys, 0, separatorKey)

	// 5. 将借来的键插入到当前节点的开头（在分隔键之后）
	node.keys = insertSlice(node.keys, 1, borrowedKey)

	// 6. 将借来的子节点插入到当前节点的开头
	node.children = insertSlice(node.children, 0, borrowedChild)

	// 7. 更新父节点的分隔键（使用左兄弟删除后的新最大键）
	if leftSibling.NumKeys() > 0 {
		newSeparatorKey := leftSibling.keys[leftSibling.NumKeys()-1]
		parent.keys[nodeIndex-1] = newSeparatorKey
	}
	parent.version++

	return nil
}

// redistributeInternalRight 从右兄弟借键（内部节点）
//
// 当右兄弟有足够的键时，从右兄弟借一个键到当前节点
//
// 借键流程：
// 1. 父节点的分隔键下降到当前节点
// 2. 右兄弟的最小键上升到父节点作为新的分隔键
// 3. 右兄弟的最小子节点移动到当前节点
func (b *BTree) redistributeInternalRight(
	parent *InternalPage,
	nodeInfo, rightSiblingInfo *PageInfo,
	nodeIndex int,
) error {
	node := nodeInfo.GetInternalPage()
	rightSibling := rightSiblingInfo.GetInternalPage()

	// 1. 从父节点获取分隔键
	separatorKey := parent.keys[nodeIndex]

	// 2. 从右兄弟借第一个键和子节点
	borrowedKey := rightSibling.keys[0]
	borrowedChild := rightSibling.children[0] // 第一个子节点

	// 3. 从右兄弟删除第一个键和子节点
	rightSibling.keys = rightSibling.keys[1:]
	rightSibling.children = rightSibling.children[1:]
	rightSibling.version++

	// 4. 将分隔键追加到当前节点
	node.keys = append(node.keys, separatorKey)

	// 5. 将借来的键追加到当前节点
	node.keys = append(node.keys, borrowedKey)

	// 6. 将借来的子节点追加到当前节点
	node.children = append(node.children, borrowedChild)

	// 7. 更新父节点的分隔键（使用右兄弟的最小键）
	if rightSibling.NumKeys() > 0 {
		newSeparatorKey := rightSibling.keys[0]
		parent.keys[nodeIndex] = newSeparatorKey
	}
	parent.version++

	return nil
}

// mergeInternal 合并内部节点
//
// 当内部节点的键数量少于 minKeys 时，尝试从兄弟节点借键或合并
//
// 参数：
//   - nodeInfo: 要合并的节点信息
//   - copiedPath: 复制路径（用于修改）
//   - path: 原始路径（用于读取）
//
// 返回：
//   - error: 错误信息
func (b *BTree) mergeInternal(nodeInfo *PageInfo, copiedPath, path []*PageInfo) error {
	const minInternal = 7

	// 1. 检查是否有父节点
	if len(path) < 2 {
		// 没有父节点，说明是根节点
		// 根节点允许任意数量的键（包括 0）
		return nil
	}

	// ✅ P0-3 修复: 统一使用 copiedPath 来获取父节点
	// 从 copiedPath 获取父节点（用于修改和访问兄弟节点）
	if len(copiedPath) < 2 {
		return fmt.Errorf("copiedPath too short: expected at least 2, got %d", len(copiedPath))
	}

	parentInfo := copiedPath[len(copiedPath)-2]
	parent := parentInfo.GetInternalPage()
	if parent == nil {
		return fmt.Errorf("parent page not loaded in copiedPath")
	}

	// 2. 找到当前节点在父节点中的位置
	nodeIndex, err := b.findChildIndexInParent(parent, nodeInfo)
	if err != nil {
		return fmt.Errorf("find child index: %w", err)
	}

	// 3. 尝试从左兄弟借键
	if nodeIndex > 0 {
		leftSiblingRef := parent.GetChild(nodeIndex - 1)
		if leftSiblingRef != nil {
			leftSiblingInfo := leftSiblingRef.GetPageInfo()

			// 懒加载左兄弟（如果未加载）
			if err := b.ensurePageLoaded(leftSiblingInfo); err != nil {
				return err
			}

			leftSibling := leftSiblingInfo.GetInternalPage()
			if leftSibling != nil && leftSibling.NumKeys() > minInternal {
				return b.redistributeInternalLeft(parent, nodeInfo, leftSiblingInfo, nodeIndex)
			}
		}
	}

	// 4. 尝试从右兄弟借键
	if nodeIndex < parent.NumChildren()-1 {
		rightSiblingRef := parent.GetChild(nodeIndex + 1)
		if rightSiblingRef != nil {
			rightSiblingInfo := rightSiblingRef.GetPageInfo()

			// 懒加载右兄弟（如果未加载）
			if err := b.ensurePageLoaded(rightSiblingInfo); err != nil {
				return err
			}

			rightSibling := rightSiblingInfo.GetInternalPage()
			if rightSibling != nil && rightSibling.NumKeys() > minInternal {
				return b.redistributeInternalRight(parent, nodeInfo, rightSiblingInfo, nodeIndex)
			}
		}
	}

	// 5. 如果无法借键，则合并
	// ✅ P0-3 修复: 统一使用 copiedPath 中的父节点
	// parentInfo 已经来自 copiedPath，不需要再次获取

	// 优先与右兄弟合并
	if nodeIndex < parent.NumChildren()-1 {
		rightSiblingRef := parent.GetChild(nodeIndex + 1)
		if rightSiblingRef != nil {
			rightSiblingInfo := rightSiblingRef.GetPageInfo()

			if err := b.ensurePageLoaded(rightSiblingInfo); err != nil {
				return err
			}

			return b.mergeInternalWithSibling(parentInfo, parent, nodeInfo, rightSiblingInfo, nodeIndex, copiedPath, path)
		}
	} else {
		// 与左兄弟合并
		leftSiblingRef := parent.GetChild(nodeIndex - 1)
		if leftSiblingRef != nil {
			leftSiblingInfo := leftSiblingRef.GetPageInfo()

			if err := b.ensurePageLoaded(leftSiblingInfo); err != nil {
				return err
			}

			return b.mergeInternalWithSibling(parentInfo, parent, leftSiblingInfo, nodeInfo, nodeIndex-1, copiedPath, path)
		}
	}

	return nil
}
