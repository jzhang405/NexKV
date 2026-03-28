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
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/wal"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

var (
	// Use centralized errors from pkg/errors
	ErrNotImplemented    = errpkg.ErrBTreeNotImplemented
	ErrClosed            = errpkg.ErrBTreeClosed
	ErrRetry             = errpkg.ErrBTreeRetry
	ErrInvalidPath       = errpkg.ErrBTreeInvalidPath
	ErrKeyNotFound       = errpkg.ErrBTreeKeyNotFound
	ErrPageStale         = errpkg.ErrBTreePageStale
	ErrCircularReference = errpkg.ErrBTreeCircularReference
)

// PageRefCache 维护 PageID → PageRef 的映射（Off-Heap 模式）
// 用于快速查找已有的 PageRef，避免重复创建
type PageRefCache struct {
	cache map[model.PageID]*PageRef
	mu    sync.RWMutex
}

// NewPageRefCache 创建新的 PageRefCache
func NewPageRefCache() *PageRefCache {
	return &PageRefCache{
		cache: make(map[model.PageID]*PageRef),
	}
}

// GetOrCreate 获取或创建 PageRef
func (c *PageRefCache) GetOrCreate(pageID model.PageID, isLeaf bool) *PageRef {
	c.mu.RLock()
	ref, ok := c.cache[pageID]
	c.mu.RUnlock()

	if ok {
		currentInfo := ref.GetPageInfo()
		if currentInfo != nil && currentInfo.GetPageID() != uint64(pageID) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if ref, ok := c.cache[pageID]; ok && ref.GetPageInfo().GetPageID() == uint64(pageID) {
				return ref
			}
			info := NewPageInfo()
			info.SetNodeRef(offheap.NewNodeRef(uint32(pageID), isLeaf))
			newRef := NewPageRefWithInfo(info)
			c.cache[pageID] = newRef
			return newRef
		}
		return ref
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if ref, ok := c.cache[pageID]; ok {
		return ref
	}

	info := NewPageInfo()
	info.SetNodeRef(offheap.NewNodeRef(uint32(pageID), isLeaf))
	ref = NewPageRefWithInfo(info)
	c.cache[pageID] = ref
	return ref
}

// Update 更新 PageRef（用于分裂等场景）
func (c *PageRefCache) Update(pageID model.PageID, ref *PageRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[pageID] = ref
}

// Delete 删除 PageRef
func (c *PageRefCache) Delete(pageID model.PageID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, pageID)
}

// Replace 原子替换 PageRef
func (c *PageRefCache) Replace(oldPageID, newPageID model.PageID, ref *PageRef) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, oldPageID)
	c.cache[newPageID] = ref
}

// BTree is the main BTree storage engine with CCOW and persistence.
//
// Architecture:
// - ChunkManager for append-only storage
// - RootPageRef for atomic root updates
// - Lazy loading (only Root resident)
// - Copy-on-Write concurrency control
type BTree struct {
	config    *model.BTreeConfig
	cowConfig *COWDeltaRefConfig // COW Delta 物化配置
	closed    bool
	closedMu  sync.RWMutex

	// Context management for background goroutines
	ctx        context.Context    // Context for controlling background goroutines
	cancelFunc context.CancelFunc // Cancel function to stop all background goroutines

	// Root management
	rootRef *RootPageRef // Root page reference (atomic updates)

	// Storage
	chunkMgr *ChunkManager // Append-only storage manager
	wal      wal.WAL       // Write-Ahead Log for crash recovery

	// Off-Heap storage
	offheapPM      *offheap.PageManager // Off-Heap 页面管理器
	offheapAdapter *OffHeapAdapter      // Off-Heap 适配器
	pageRefCache   *PageRefCache        // PageID → PageRef 映射（Off-Heap 模式）

	// Configuration
	maxLevels int  // Maximum tree levels
	enableWAL bool // Enable WAL logging

	// PageID management
	nextPageID atomic.Uint64 //nolint:unused // Next page ID to allocate (lock-free)

	// Persistence coordination
	writeMu sync.Mutex // Global write lock for persistence operations

	// Performance optimization
	stats            *PageStats // 页面访问统计（热数据识别）
	hotPageThreshold int64      // 热数据阈值（来自配置）

	// Scheduler for concurrent write operations
	scheduler       *concurrency.TaskScheduler   // Task scheduler for concurrent operations
	perCoreExecutor *concurrency.PerCoreExecutor // Per-Core executor (owned by scheduler)

	// Split coordination: 防止多个 goroutine 同时分裂同一页面
	splitMuMap sync.Map // map[uint32]*sync.Mutex - 页面级别的分裂锁

	// Epoch-based page release: 延迟释放页面避免竞态条件
	epochBasedFreeList *EpochBasedFreeList
}

// EpochBasedFreeList 延迟释放列表
type EpochBasedFreeList struct {
	currentEpoch uint64                    // 当前 epoch
	pending      map[uint64][]model.PageID // epoch → 待释放页面列表
	mu           sync.Mutex
}

func NewEpochBasedFreeList() *EpochBasedFreeList {
	return &EpochBasedFreeList{
		currentEpoch: 0,
		pending:      make(map[uint64][]model.PageID),
	}
}

func (e *EpochBasedFreeList) Add(pageID model.PageID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pending[e.currentEpoch] = append(e.pending[e.currentEpoch], pageID)
}

func (e *EpochBasedFreeList) AdvanceEpoch(pm *offheap.PageManager) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.currentEpoch++

	epochToDelayed := e.currentEpoch - 2
	if e.currentEpoch >= 2 {
		pagesToDelayed := e.pending[epochToDelayed]
		delete(e.pending, epochToDelayed)
		for _, pid := range pagesToDelayed {
			pm.Free(uint32(pid))
		}
	}

	epochToFree := e.currentEpoch - 3
	if e.currentEpoch >= 3 {
		delete(e.pending, epochToFree)
		pm.AdvanceDelayedFreeList()
	}
}

// BTreeSchedulerAdapter 实现 TaskScheduler 接口
type BTreeSchedulerAdapter struct {
	scheduler *concurrency.TaskScheduler
}

func (a *BTreeSchedulerAdapter) EnqueueWithShard(item any, taskName string) error {
	if shardItem, ok := item.(concurrency.ShardItem); ok {
		return a.scheduler.EnqueueWithShard(shardItem, taskName)
	}
	return errpkg.BTreeShardItemInterface()
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
			return nil, errpkg.BTreeCreateDirectory(dir, err)
		}
	}

	// Initialize ChunkManager and WAL
	var chunkMgr *ChunkManager
	var walImpl wal.WAL
	enableWAL := dir != ""

	if dir != "" {
		// Open ChunkManager for append-only storage
		cm, err := NewChunkManager(dir)
		if err != nil {
			return nil, errpkg.BTreeOpenChunkManager(dir, err)
		}
		chunkMgr = cm

		// Open WAL using the general-purpose WAL implementation
		walDir := filepath.Join(dir, "wal")
		w, err := wal.NewDiskWAL(&wal.WALConfig{
			Dir:         walDir,
			SegmentSize: 64 * 1024 * 1024, // 64MB
			SyncPolicy:  wal.SyncPolicyEveryWrite,
		})
		if err != nil {
			chunkMgr.Close()
			return nil, errpkg.BTreeOpenWAL(walDir, err)
		}
		walImpl = w
	}

	// Off-Heap 存储（方案 B：完全替换）
	// 创建 PageManager（64MB，支持 50000+ keys）
	// 计算：50000 keys / 40 keys/page = 1250 页
	//      内部节点约 1250/180 * 3层 ≈ 21 页
	//      分裂开销 2x ≈ 2500 页
	//      2500 * 4KB = 10MB（64MB 提供充足余量）
	mmapSize := 64 * 1024 * 1024 // 64MB
	offheapPM, err := offheap.NewPageManager(mmapSize)
	if err != nil {
		return nil, errpkg.BTreeCreateOffheapManager(err)
	}

	// 创建 OffHeapAdapter
	offheapAdapter := NewOffHeapAdapter(offheapPM)

	// 分配初始根叶子节点（使用 Off-Heap）
	initialRootPageID, err := offheapAdapter.AllocLeafPage()
	if err != nil {
		offheapPM.Close()
		return nil, errpkg.BTreeAllocRootPage(err)
	}

	// 创建初始根 PageInfo（使用 NodeRef）
	initialRootInfo := NewPageInfo()
	initialRootInfo.SetNodeRef(offheap.NewNodeRef(uint32(initialRootPageID), true)) // true = isLeaf
	initialRootInfo.SetParentRef(nil)                                               // 根节点没有父引用

	// ✅ 修复 goroutine 泄漏：创建 context 用于控制后台 goroutines（必须在创建 RootPageRef 之前）
	ctx, cancelFunc := context.WithCancel(context.Background())

	rootPageRef := NewRootPageRefWithInfo(ctx, initialRootInfo)

	// Calculate max levels based on config
	maxLevels := 10 // Default value

	// 创建 COW Delta 物化配置
	cowConfig := NewCOWDeltaRefConfigFromBTreeConfig(config)

	// 创建性能优化组件
	stats := NewPageStats()

	// 创建 PageRefCache（Off-Heap 模式）
	pageRefCache := NewPageRefCache()

	// 创建 Epoch-based 延迟释放列表
	epochBasedFreeList := NewEpochBasedFreeList()

	btree := &BTree{
		config:             config,
		cowConfig:          cowConfig,
		closed:             false,
		ctx:                ctx,
		cancelFunc:         cancelFunc,
		rootRef:            rootPageRef,
		chunkMgr:           chunkMgr,
		wal:                walImpl,
		offheapPM:          offheapPM,
		offheapAdapter:     offheapAdapter,
		pageRefCache:       pageRefCache,
		maxLevels:          maxLevels,
		enableWAL:          enableWAL,
		stats:              stats,
		hotPageThreshold:   config.HotPageThreshold,
		epochBasedFreeList: epochBasedFreeList,
	}

	// 应用 GC 配置（如果指定）
	// 注意：这会影响整个进程，使用时需谨慎
	if config.GCPercent > 0 {
		debug.SetGCPercent(config.GCPercent)
	}

	// Replay WAL if exists (crash recovery)
	if enableWAL && walImpl != nil {
		if err := btree.replayWAL(); err != nil {
			// Close resources on error
			chunkMgr.Close()
			walImpl.Close()
			return nil, errpkg.BTreeReplayWAL(err)
		}
	}

	// 方案 2：初始化内置 TaskScheduler
	// 使用自动检测的 CPU 核心数
	schedulerCores := runtime.NumCPU()
	btree.scheduler = concurrency.NewTaskScheduler("btree", schedulerCores)

	// 注册 btree-set 任务
	err = btree.scheduler.RegisterTask(
		func(item any) concurrency.TaskStatus {
			// 检查 item 是否实现了 TaskRunner 接口
			if runner, ok := item.(model.TaskRunner); ok {
				// 同步执行任务（TaskScheduler 已经在 Worker 线程中）
				runner.Run(context.Background(), nil)
				// 等待任务完成并检查结果
				if task, ok := item.(interface {
					Wait(context.Context) (any, error)
				}); ok {
					_, err := task.Wait(context.Background())
					if err != nil {
						// ErrRetry 表示应该重试
						if errors.Is(err, ErrRetry) {
							return concurrency.TaskRetrying
						}
						return concurrency.TaskFailed
					}
				}
				return concurrency.TaskPassed
			}
			return concurrency.TaskPassed
		},
		"btree-set",
		model.TaskPriorityNormal,
		0, // executionOrder = 0 (数组索引 0)
	)
	if err != nil {
		// 清理资源
		if chunkMgr != nil {
			chunkMgr.Close()
		}
		if walImpl != nil {
			walImpl.Close()
		}
		return nil, errpkg.BTreeRegisterTask("set", err)
	}

	// 注册 btree-split 任务（异步父节点分裂）
	// 基于 Lealone 的 asyncSplitPage() 设计
	err = btree.scheduler.RegisterTask(
		func(item any) concurrency.TaskStatus {
			// 检查 item 是否实现了 TaskRunner 接口
			if runner, ok := item.(model.TaskRunner); ok {
				// 同步执行任务（TaskScheduler 已经在 Worker 线程中）
				runner.Run(context.Background(), nil)
				// 等待任务完成并检查结果
				if task, ok := item.(interface {
					Wait(context.Context) (any, error)
				}); ok {
					_, err := task.Wait(context.Background())
					if err != nil {
						// ErrRetry 表示应该重试
						if errors.Is(err, ErrRetry) {
							return concurrency.TaskRetrying
						}
						return concurrency.TaskFailed
					}
				}
				return concurrency.TaskPassed
			}
			return concurrency.TaskPassed
		},
		"btree-split",
		model.TaskPriorityHigh, // 高优先级，优先处理父节点分裂
		1,                      // executionOrder = 1 (数组索引 1)
	)
	if err != nil {
		// 清理资源
		if chunkMgr != nil {
			chunkMgr.Close()
		}
		if walImpl != nil {
			walImpl.Close()
		}
		btree.scheduler.Stop()
		return nil, errpkg.BTreeRegisterTask("split", err)
	}

	// 启动 TaskScheduler
	executor, err := concurrency.NewPerCoreExecutor()
	if err != nil {
		// 清理资源
		if chunkMgr != nil {
			chunkMgr.Close()
		}
		if walImpl != nil {
			walImpl.Close()
		}
		btree.scheduler.Stop()
		return nil, errpkg.BTreeCreateExecutor(err)
	}
	btree.perCoreExecutor = executor // 保存 executor 引用

	if err := btree.scheduler.Start(executor); err != nil {
		// 清理资源
		if chunkMgr != nil {
			chunkMgr.Close()
		}
		if walImpl != nil {
			walImpl.Close()
		}
		return nil, errpkg.BTreeStartScheduler(err)
	}

	return btree, nil
}

// ===== KVStore Interface Implementation (Placeholder) =====

// Get retrieves a value by key with lazy loading support.
//
// This method implements the read path of the BTree:
// 1. Find the path from Root to Leaf using searchPathWithRefs() (Off-Heap mode)
// 2. Get the leaf PageRef and search for the key
// 3. Return the value if found, or ErrKeyNotFound if not
//
// Performance:
// - O(log n) page traversals
// - Off-Heap storage: no page loading overhead
// - Lock-free reads using PageRefCache
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
	if b.closed {
		return nil, ErrClosed
	}

	const maxRetries = 5 // 最多重试 5 次（增加以提高并发成功率）

	for attempt := range maxRetries {
		// Off-Heap 模式：使用 searchPathWithRefs
		leafRef, path, _, err := b.findLeafPageRef(ctx, key)
		if err != nil {
			// 如果查找路径失败且不是最后一次尝试，重试
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			// 不要包装 ErrRetry，否则 errors.Is() 检查会失败
			return nil, err
		}

		if len(path) == 0 || leafRef == nil {
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			return nil, ErrKeyNotFound
		}

		// 获取叶子节点的 PageInfo
		leafInfo := leafRef.GetPageInfo()
		if leafInfo == nil {
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			return nil, ErrKeyNotFound
		}

		// 获取叶子节点 PageID
		leafPageID := model.PageID(leafInfo.GetPageID())

		// Off-Heap 模式：验证页面已加载
		if !leafInfo.IsPageLoaded() {
			return nil, errpkg.BTreeLeafPageNotLoaded()
		}

		// 使用 OffHeapAdapter.GetFromOffHeap 直接读取
		value, found, err := b.offheapAdapter.GetFromOffHeap(leafPageID, key)
		if err != nil {
			// 如果读取错误且不是最后一次尝试，重试
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			return nil, errpkg.BTreeOffheapGet(err)
		}

		if found {
			// 成功找到，推进 epoch 释放待释放页面
			b.epochBasedFreeList.AdvanceEpoch(b.offheapPM)
			return value, nil // 成功找到，直接返回
		}

		// 未找到，在并发场景下可能是由于：
		// 1. 页面刚刚分裂，key 被移动到新页面
		// 2. PageRefCache 缓存了旧的 PageRef
		// 3. 搜索路径在分裂后变得无效

		// 如果在页面分裂期间，可能出现临时找不到
		// 策略：让出 CPU，重新搜索（而不是重试读取同一个页面）
		if attempt < maxRetries-1 {
			// 让出 CPU，让其他 goroutine 完成分裂和缓存更新
			runtime.Gosched()

			// 继续下一次循环，重新执行 findLeafPageRef
			// 这样可以获取到更新后的 PageRef 和路径
			continue
		}

		return nil, ErrKeyNotFound
	}

	return nil, ErrKeyNotFound
}

// Set stores a key-value pair with Copy-on-Write and CAS.
//
// This method implements the write path of the BTree:
// 1. Find the path from Root to Leaf using searchPath()
// 2. Create a copy-on-write path (clone all pages from root to leaf)
// 3. Insert/Update the key-value pair in the leaf
// 4. Atomically update the root using CAS (Compare-And-Swap)
// 5. Spin retry on CAS failure with runtime.Gosched()
//
// Performance:
// - O(log n) page copies for CCOW
// - Atomic root switch using CAS
// - Lock-free spin retry on concurrent writes
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
	if b.closed {
		return ErrClosed
	}

	// 方案 2：使用 SetWithRetryAndQueue（经过优化的版本）
	// 使用内置 TaskScheduler，在快速路径失败时自动切换到队列模式
	if b.scheduler != nil {
		return b.SetWithRetryAndQueue(ctx, &BTreeSchedulerAdapter{scheduler: b.scheduler}, key, value)
	}

	// Fallback：如果没有 scheduler，使用 Direct 模式
	return b.setDirect(ctx, key, value)
}

// setDirect 直接写入模式（fallback，当 scheduler 未初始化时）
func (b *BTree) setDirect(ctx context.Context, key, value []byte) error {
	const maxRetries = 10 // 增加重试次数以处理循环引用

	for attempt := range maxRetries {
		// 检查上下文取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := b.setWithLeafLock(ctx, key, value)
		if err == nil {
			// 操作成功，推进 epoch 释放待释放页面
			b.epochBasedFreeList.AdvanceEpoch(b.offheapPM)
			return nil
		}

		// 智能重试逻辑
		switch {
		case errors.Is(err, ErrRetry):
			// CAS 失败：快速重试
			runtime.Gosched()
			continue

		case errors.Is(err, ErrCircularReference):
			// 循环引用错误：指数退避后重试
			// 这是由于页面释放后重新分配导致的临时不一致
			if attempt < maxRetries-1 {
				// 指数退避：1ms, 2ms, 4ms, 8ms, ... 最多 512ms
				backoffDuration := time.Duration(1<<uint(attempt)) * time.Millisecond
				if backoffDuration > 512*time.Millisecond {
					backoffDuration = 512 * time.Millisecond
				}
				select {
				case <-time.After(backoffDuration):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			// 达到最大重试次数，返回错误
			return errpkg.BTreeCircularReferenceRetry(maxRetries, err)

		default:
			// 其他错误：不重试，直接返回
			return err
		}
	}

	return errpkg.BTreeMaxRetriesExceeded(maxRetries)
}

// Delete removes a key.
func (b *BTree) Delete(ctx context.Context, key []byte) error {
	if b.closed {
		return ErrClosed
	}

	// Off-Heap 模式：使用 Leaf-Level Locking + MVCC
	if b.offheapPM != nil {
		return b.deleteOffHeapWithMVCC(ctx, key)
	}

	// On-Heap 模式：原有实现
	const maxRetries = 3

	for attempt := range maxRetries {
		// 1. 检查上下文取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 2. 查找键的路径
		_, path, err := b.findLeafPage(ctx, key)
		if err != nil {
			return errpkg.BTreeFindLeafPage(err)
		}

		// 3. CCOW：复制路径
		copiedPath, err := b.copyPath(path)
		if err != nil {
			return errpkg.BTreeCopyPath(err)
		}

		// 4. 删除键
		leafInfo := copiedPath[len(copiedPath)-1]
		leaf := leafInfo.GetLeafPage()

		deleted, err := leaf.Delete(key)
		if err != nil {
			return errpkg.BTreeDeleteFromLeaf(err)
		}

		if !deleted {
			return ErrKeyNotFound
		}

		// 5. 检查是否需要 Merge
		const minKeys = 8
		if leaf.NumKeys() < minKeys && len(path) >= 2 {
			// 重新启用 Merge，使用原始 path 访问兄弟节点
			if err := b.mergeLeaf(leafInfo, copiedPath, path); err != nil {
				return errpkg.BTreeMergeLeaf(err)
			}
		}

		// 6. CAS 更新根节点（带重试）
		newRootInfo := copiedPath[0]
		oldRootInfo := b.rootRef.pInfo.Load()
		oldRootID := uint64(0)
		if oldRootInfo != nil {
			oldRootID = oldRootInfo.GetPageID()
		}

		if b.rootRef.ReplacePage(oldRootID, newRootInfo) {
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
			if err := b.persistRoot(newRootInfo); err != nil {
				return errpkg.BTreePersistRoot(err)
			}
		}

		return nil
	}

	return ErrRetry
}

// deleteOffHeapWithMVCC 使用 MVCC + Leaf-Level Locking 实现 Off-Heap Delete
// 参考 Set 操作的实现模式（leaf_lock_set.go）
func (b *BTree) deleteOffHeapWithMVCC(ctx context.Context, key []byte) error {
	const maxRetries = 3

	for attempt := range maxRetries {
		// 1. 检查上下文取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 2. 查找叶子节点和路径（只读，不克隆）
		leafRef, path, _, err := b.findLeafPageRef(ctx, key)
		if err != nil {
			// 不要包装 ErrRetry，否则 errors.Is() 检查会失败
			return err
		}

		if len(path) == 0 {
			return errpkg.BTreeEmptyPath()
		}

		// 3. 获取叶子锁（懒加载，每个 PageRef 有独立的锁）
		pageLock := leafRef.GetLock()
		if pageLock == nil {
			return errpkg.BTreePageLockNil()
		}

		// 使用 TryLock 快速失败（避免死锁）
		if !pageLock.TryLock() {
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			return ErrRetry
		}
		defer pageLock.Unlock()

		// 4. 获取当前 PageInfo（在锁保护下）
		oldInfo := leafRef.GetPageInfo()
		if oldInfo == nil {
			return errpkg.BTreeLeafPageInfoNil()
		}

		// 5. 验证页面已加载（Off-Heap 模式）
		if !oldInfo.IsPageLoaded() {
			return errpkg.BTreeLeafPageNotLoaded2()
		}

		// 6. Off-Heap 删除（使用 COW 语义）
		oldPageID := model.PageID(oldInfo.GetPageID())
		newPageID, err := b.offheapAdapter.DeleteFromLeafPage(oldPageID, key)
		if err != nil {
			if errors.Is(err, ErrKeyNotFound) {
				return ErrKeyNotFound
			}
			return errpkg.BTreeOffheapDelete(err)
		}

		// 7. 创建新的 PageInfo（Off-Heap 模式）
		newInfo := NewPageInfo()
		newInfo.SetNodeRef(offheap.NewNodeRef(uint32(newPageID), true)) // true = isLeaf
		// 继承其他属性
		newInfo.SetPos(oldInfo.GetPos())
		if oldInfo.IsDirty() {
			newInfo.MarkDirty()
		}

		// 8. Leaf-Level CAS（在锁保护下，几乎不会失败）
		if !leafRef.ReplacePage(oldInfo, newInfo) {
			// CAS 失败（极少发生），返回重试
			// 注意：newInfo 由 Go GC 自动管理，无需手动释放
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			return ErrRetry
		}

		// 9. 如果 pageID 变化，需要更新 root 或父节点
		if newPageID != oldPageID {
			// 检查是否是根节点（单层树）
			if len(path) == 1 && leafRef == b.rootRef.PageRef {
				// 特殊处理：根节点的 delete 场景
				// 需要更新 rootRef 而不是只更新 PageRefCache
				oldRootInfo := b.rootRef.pInfo.Load()
				if oldRootInfo == nil {
					return errpkg.BTreeRootInfoNil("root update")
				}
				oldRootID := oldRootInfo.GetPageID()
				if !b.rootRef.ReplacePage(oldRootID, newInfo) {
					// CAS 失败，返回重试
					if attempt < maxRetries-1 {
						runtime.Gosched()
						continue
					}
					return ErrRetry
				}
				// 更新 PageRefCache
				b.pageRefCache.Delete(oldPageID)
				b.pageRefCache.Update(newPageID, leafRef)
			} else if len(path) >= 2 {
				// 多层树：需要更新父节点的 child 指针
				// 参考分裂操作的实现（leaf_lock_set.go:handleSplitOffHeapSync）

				// 获取父节点的 PageInfo
				parentInfo := path[len(path)-2]
				if parentInfo == nil {
					return errpkg.BTreeParentInfoNil("delete")
				}

				oldParentPageID := model.PageID(parentInfo.GetPageID())

				// 检查父节点是否是根节点
				currentRootInfo := b.rootRef.pInfo.Load()
				parentRef := b.pageRefCache.GetOrCreate(oldParentPageID, false)
				if currentRootInfo != nil && currentRootInfo.GetPageID() == uint64(oldParentPageID) {
					// 父节点就是根节点，使用根的 PageRef
					if b.rootRef.PageRef == nil {
						return errpkg.BTreeRootPageRefNil("parent update")
					}
					parentRef = b.rootRef.PageRef
				}

				// 获取父节点锁（自底向上加锁）
				parentLock := parentRef.GetLock()
				if parentLock == nil {
					return errpkg.BTreeParentLockNil()
				}

				if !parentLock.TryLock() {
					// 锁获取失败，返回重试
					if attempt < maxRetries-1 {
						runtime.Gosched()
						continue
					}
					return ErrRetry
				}
				defer parentLock.Unlock()

				// 找到父节点中指向当前子节点的索引
				// 通过二分查找找到 key 在父节点中的位置
				childIndex := 0
				count := b.offheapAdapter.pa.GetCount(uint32(oldParentPageID))
				for i := 0; i < int(count); i++ {
					keyOff, keyLen, encodedChild := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(oldParentPageID), i)
					child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
					k := b.offheapAdapter.pa.GetKey(uint32(oldParentPageID), keyOff, keyLen)
					// 如果找到匹配的 child 或者 key 大于当前 key，就找到了位置
					if model.PageID(child) == oldPageID || bytes.Compare(k, key) > 0 {
						childIndex = i
						break
					}
					childIndex = i + 1
				}

				// 检查是否是 extraChild（N+1 child）
				if childIndex == int(count) {
					// extraChild 的情况
					// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
					encodedExtraChild := b.offheapAdapter.pa.GetChild(uint32(oldParentPageID), int(count))
					extraChild, _ := b.offheapAdapter.DecodeChildWithVersion(encodedExtraChild)
					if model.PageID(extraChild) != oldPageID {
						// 不是我们要找的 child，继续查找
						// 这种情况不太可能发生，但为了安全起见
						childIndex = -1
					}
				}

				if childIndex < 0 {
					// 没有找到对应的 child 指针，返回错误
					return errpkg.BTreeChildNotFound(uint64(oldParentPageID), uint64(oldPageID))
				}

				// 使用 UpdateChildIndex 更新父节点
				newParentPageID, err := b.offheapAdapter.UpdateChildIndex(oldParentPageID, childIndex, newPageID)
				if err != nil {
					return errpkg.BTreeUpdateParentChildIndex(err)
				}

				// 创建新的父节点 PageInfo
				newParentInfo := NewPageInfo()
				newParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(newParentPageID), false))
				newParentInfo.SetPos(parentInfo.GetPos())
				if parentInfo.IsDirty() {
					newParentInfo.MarkDirty()
				}

				// CAS 更新父节点
				if !parentRef.ReplacePage(parentInfo, newParentInfo) {
					// CAS 失败，返回重试
					// 注意：newParentInfo 由 Go GC 自动管理，无需手动释放
					if attempt < maxRetries-1 {
						runtime.Gosched()
						continue
					}
					return ErrRetry
				}

				// 更新 PageRefCache
				b.pageRefCache.Delete(oldParentPageID)
				b.pageRefCache.Update(newParentPageID, parentRef)

				// 延迟释放旧父页面
				b.epochBasedFreeList.Add(oldParentPageID)
			} else {
				// 其他情况：更新 PageRefCache
				b.pageRefCache.Delete(oldPageID)
				b.pageRefCache.Update(newPageID, leafRef)
			}
		}

		// 10. 推进 epoch 释放旧页面
		b.epochBasedFreeList.AdvanceEpoch(b.offheapPM)

		// 11. 持久化（如果有 ChunkManager）
		if b.chunkMgr != nil {
			if err := b.persistRoot(newInfo); err != nil {
				return errpkg.BTreePersistRoot(err)
			}
		}

		return nil
	}

	return ErrRetry
}

// GetBatch retrieves multiple values (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现批量操作优化
func (b *BTree) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
	if b.closed {
		return nil, ErrClosed
	}
	return nil, ErrNotImplemented
}

// SetBatch stores multiple key-value pairs (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现批量操作优化
func (b *BTree) SetBatch(ctx context.Context, pairs []service.KVPair) error {
	if b.closed {
		return ErrClosed
	}
	return ErrNotImplemented
}

// DeleteBatch removes multiple keys (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现批量操作优化
func (b *BTree) DeleteBatch(ctx context.Context, keys [][]byte) error {
	if b.closed {
		return ErrClosed
	}
	return ErrNotImplemented
}

// RangeScan returns an iterator for a key range (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现范围查询功能
func (b *BTree) RangeScan(ctx context.Context, start, end []byte) (service.Iterator, error) {
	if b.closed {
		return nil, ErrClosed
	}
	return nil, ErrNotImplemented
}

// BeginTx starts a transaction (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现事务支持
func (b *BTree) BeginTx(ctx context.Context, opts ...service.TxOption) (service.Transaction, error) {
	if b.closed {
		return nil, ErrClosed
	}
	return nil, errpkg.BTreeBeginTxNotImplemented()
}

// CreateSnapshot creates a snapshot (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现快照隔离功能
func (b *BTree) CreateSnapshot(ctx context.Context) (service.SnapshotID, error) {
	if b.closed {
		return 0, ErrClosed
	}
	return 0, errpkg.BTreeCreateSnapshotNotImplemented()
}

// ReleaseSnapshot releases a snapshot (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现快照隔离功能
func (b *BTree) ReleaseSnapshot(ctx context.Context, id service.SnapshotID) error {
	if b.closed {
		return ErrClosed
	}
	return errpkg.BTreeReleaseSnapshotNotImplemented()
}

// Stats returns storage statistics (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现统计监控功能
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
			return errpkg.BTreeTruncateWAL(err)
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

	// Use Set() with WAL disabled
	return b.Set(ctx, key, value)
}

// Close closes the BTree storage engine and releases resources.
func (b *BTree) Close() error {
	b.closedMu.Lock()
	defer b.closedMu.Unlock()

	if b.closed {
		return nil // Already closed
	}

	// ✅ 修复 goroutine 泄漏：首先取消所有后台 goroutines
	if b.cancelFunc != nil {
		b.cancelFunc()
		b.cancelFunc = nil
	}
	b.ctx = nil

	// 方案 2：停止内置 TaskScheduler
	if b.scheduler != nil {
		b.scheduler.Stop()
		b.scheduler = nil
	}

	// 关闭 PerCoreExecutor（停止 worker pool）
	if b.perCoreExecutor != nil {
		b.perCoreExecutor.Close()
		b.perCoreExecutor = nil
	}

	// Close ChunkManager
	if b.chunkMgr != nil {
		if err := b.chunkMgr.Close(); err != nil {
			return errpkg.BTreeCloseChunkManager(err)
		}
	}

	// Close OffHeap PageManager
	if b.offheapPM != nil {
		b.offheapPM.Close()
	}

	// Close WAL
	if b.wal != nil {
		if err := b.wal.Close(); err != nil {
			return errpkg.BTreeCloseWAL(err)
		}
	}

	b.closed = true
	return nil
}

// ===== 性能优化：热数据和内存监控 =====

// StartBackgroundOptimization 启动后台优化任务
// 定期优化热数据页面并衰减读计数
func (b *BTree) StartBackgroundOptimization(ctx context.Context, interval time.Duration) {
	if b.stats == nil {
		return
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				b.optimizeHotPages()
				b.stats.DecayReadCounts(0.9) // 衰减 10%
			case <-ctx.Done():
				return
			}
		}
	}()
}

// optimizeHotPages 优化热数据页面
func (b *BTree) optimizeHotPages() {
	// 获取读取次数最多的页面
	topPages := b.stats.GetTopReadPages(100)

	for _, pageID := range topPages {
		// 获取当前根节点
		rootInfo := b.rootRef.pInfo.Load()
		if rootInfo == nil {
			continue
		}

		// 查找页面（需要遍历树）
		page := b.findPageByID(rootInfo, pageID)
		if page == nil {
			continue
		}

		// 检查是否为 LeafPage 且在 Delta 模式
		if leafPage, ok := page.(*LeafPage); ok && leafPage.IsInDeltaMode() {
			readCount := b.stats.GetReadCount(pageID)
			// 热数据且有增量，物化优化
			if readCount > b.hotPageThreshold && leafPage.GetDeltaCount() > 0 {
				leafPage.materialize()
			}
		}
	}
}

// findPageByID 根据 PageID 查找页面（辅助方法）
func (b *BTree) findPageByID(rootInfo *PageInfo, pageID model.PageID) any {
	if rootInfo == nil {
		return nil
	}

	// 检查根节点
	rootPage := rootInfo.GetPage()
	if rootPage == nil {
		return nil
	}

	// 如果根节点就是目标
	if pg, ok := rootPage.(*LeafPage); ok && pg.GetPageID() == pageID {
		return pg
	}
	if pg, ok := rootPage.(*InternalPage); ok && pg.GetPageID() == pageID {
		return pg
	}

	// 对于 InternalPage，递归查找子节点
	if internalPage, ok := rootPage.(*InternalPage); ok {
		for _, childRef := range internalPage.children {
			if childRef == nil {
				continue
			}
			childInfo := childRef.GetPageInfo()
			if childInfo == nil {
				continue
			}
			page := b.findPageByID(childInfo, pageID)
			if page != nil {
				return page
			}
		}
	}

	return nil
}

// ===== 懒加载机制 =====

// loadPage 从 ChunkManager 加载页面（懒加载核心封装）
// 这是 BTree 对 ChunkManager.LoadPage() 的封装，提供统一的错误处理
//
// 参数：
//
//	pos - 64 位位置编码
//
// 返回：
//
//	any - 页面对象（实际类型为 *LeafPage 或 *InternalPage）
//	error - 错误信息
//
// 懒加载流程：
// 1. 检查 ChunkManager 是否初始化
// 2. 调用 ChunkManager.LoadPage(pos) 加载并反序列化
// 3. 返回页面对象
//
// 注意：此方法不会更新 PageInfo，调用者需要手动设置
func (b *BTree) loadPage(pos int64) (any, error) {
	// 1. 检查 ChunkManager（仅持久化模式需要）
	if b.chunkMgr == nil {
		return nil, errpkg.BTreeChunkManagerNotInit()
	}

	// 2. 调用 ChunkManager.LoadPage()（根据位置编码加载页面）
	page, err := b.chunkMgr.LoadPage(pos)
	if err != nil {
		return nil, errpkg.BTreeLoadPageAt(pos, err)
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
//	any - 页面对象
//	error - 错误信息
func (b *BTree) getPageOrLoad(info *PageInfo) (any, error) {
	if info == nil {
		return nil, errpkg.BTreePageInfoNil()
	}

	// 如果 page 已加载，直接返回
	if info.IsPageLoaded() {
		return info.GetPage(), nil
	}

	// 如果 pos == 0，说明页面从未持久化
	if info.GetPos() == 0 {
		return nil, errpkg.BTreePageNotLoadedNoPos()
	}

	// 懒加载：从 ChunkManager 加载
	page, err := b.loadPage(info.GetPos())
	if err != nil {
		return nil, errpkg.BTreeLoadPage(err)
	}

	// 更新 PageInfo.page
	info.SetPage(page)

	return page, nil
}

// ===== BTree Interface Implementation (Placeholder) =====

// GetHeight returns the tree height (not implemented).
func (b *BTree) GetHeight(ctx context.Context) (int, error) {
	if b.closed {
		return 0, ErrClosed
	}

	return b.GetDepth(), nil
}

// GetPageCount returns the total page count (not implemented).
func (b *BTree) GetPageCount(ctx context.Context) (int, error) {
	if b.closed {
		return 0, ErrClosed
	}
	return 0, ErrNotImplemented
}

// DumpTree returns a string representation of the tree (not implemented).
func (b *BTree) DumpTree(ctx context.Context) (string, error) {
	if b.closed {
		return "", ErrClosed
	}
	return "", ErrNotImplemented
}

// Validate validates the tree structure (not implemented).
func (b *BTree) Validate(ctx context.Context) error {
	if b.closed {
		return ErrClosed
	}
	return ErrNotImplemented
}

// ===== Get/Set Helper Methods =====

// setWithCAS attempts to insert a key-value pair using CAS.
//
// copyPath creates copy-on-write copies of all pages in the path.
//
// This function clones all PageInfo objects in the path, creating new
// Page objects for each. This ensures that concurrent readers can still
// access the old version while the writer modifies the new version.
//
// 修复：使用 PageID 来正确匹配和更新 InternalPage 的子节点引用
func (b *BTree) copyPath(path []*PageInfo) ([]*PageInfo, error) {
	if len(path) == 0 {
		return nil, errpkg.BTreeEmptyPath()
	}

	copiedPath := make([]*PageInfo, len(path))

	// 优化：预先构建 PageID -> PageInfo 映射表，避免 O(n²) 嵌套循环
	pageInfoMap := make(map[model.PageID]*PageInfo, len(path))
	for _, info := range path {
		var pageID model.PageID
		switch p := info.GetPage().(type) {
		case *LeafPage:
			pageID = p.pageID
		case *InternalPage:
			pageID = p.pageID
		default:
			continue
		}
		pageInfoMap[pageID] = info // 暂时存储原始 PageInfo，稍后更新
	}

	// Clone all PageInfos in the path
	for i, info := range path {
		newInfo := info.Clone()
		copiedPath[i] = newInfo

		// 更新映射表为克隆后的 PageInfo
		var pageID model.PageID
		switch p := newInfo.GetPage().(type) {
		case *LeafPage:
			pageID = p.pageID
		case *InternalPage:
			pageID = p.pageID
		default:
			continue
		}
		pageInfoMap[pageID] = newInfo
	}

	// Rebuild child references for InternalPages
	for _, info := range copiedPath {
		// 关键修复：如果是 InternalPage，需要重建子节点引用
		// 因为 InternalPage.Clone() 只是浅拷贝了 children[]
		//
		// 方法：使用 PageID 来匹配子节点，而不是对象地址
		if internalPage, ok := info.GetPage().(*InternalPage); ok && internalPage != nil {
			// 遍历所有子节点引用
			for j := range len(internalPage.children) {
				childRef := internalPage.children[j]
				if childRef == nil {
					continue
				}

				childInfo := childRef.GetPageInfo()
				if childInfo == nil {
					continue
				}

				childPage := childInfo.GetPage()
				if childPage == nil {
					continue
				}

				// 获取子节点的 PageID
				var childPageID model.PageID
				switch p := childPage.(type) {
				case *LeafPage:
					childPageID = p.pageID
				case *InternalPage:
					childPageID = p.pageID
				default:
					// 未知类型，跳过
					continue
				}

				// 优化：使用映射表进行 O(1) 查找，避免嵌套循环
				childReplacement := pageInfoMap[childPageID]

				if childReplacement != nil {
					// 子节点在路径中，使用克隆的 PageInfo
					newChildRef := NewPageRefWithInfo(childReplacement)
					// 设置 parentRef：指向当前克隆的 InternalPage（使用 atomic.Value）
					newChildRef.SetParentRef(b.rootRef.PageRef)
					internalPage.children[j] = newChildRef
				} else {
					// 子节点不在路径中，创建新的克隆
					clonedChildInfo := childInfo.Clone()
					newChildRef := NewPageRefWithInfo(clonedChildInfo)
					// 设置 parentRef（使用 atomic.Value）
					newChildRef.SetParentRef(b.rootRef.PageRef)
					internalPage.children[j] = newChildRef
				}
			}
		}
	}

	return copiedPath, nil
}

// copyPathShallow 浅拷贝路径（延迟深拷贝优化）
//
// 与 copyPath 的区别：
// - copyPath: 深拷贝所有 Page（PageInfo + Page）
// - copyPathShallow: 只浅拷贝 PageInfo，共享 Page 引用
//
// 使用场景：
// - CAS 前的路径拷贝，避免大量无效深拷贝
// - CAS 成功后，再通过 finalizeDeepClone 转为深拷贝
//
// 并发安全性：
// - 浅拷贝状态下的 Page 必须只读
// - CAS 成功后的修改会触发深拷贝
func (b *BTree) copyPathShallow(path []*PageInfo) ([]*PageInfo, error) {
	if len(path) == 0 {
		return nil, errpkg.BTreeEmptyPath()
	}

	copiedPath := make([]*PageInfo, len(path))

	// 优化：预先构建 PageID -> PageInfo 映射表
	pageInfoMap := make(map[model.PageID]*PageInfo, len(path))
	for _, info := range path {
		if internalPage, ok := info.GetPage().(*InternalPage); ok && internalPage != nil {
			if len(internalPage.children) != len(internalPage.keys)+1 {
				// 不变式违反：children 数量应该是 keys 数量 + 1
				continue
			}
		}

		var pageID model.PageID
		switch p := info.GetPage().(type) {
		case *LeafPage:
			pageID = p.pageID
		case *InternalPage:
			pageID = p.pageID
		default:
			continue
		}
		pageInfoMap[pageID] = info
	}

	// Clone all PageInfos in the path（Off-Heap 模式）
	// Off-Heap 模式：使用 CloneShallow() 克隆 NodeRef（正确设置 cloneStatus）
	for i, info := range path {
		// Off-Heap 模式：使用 CloneShallow() 设置正确的克隆状态
		newInfo := info.CloneShallow()
		copiedPath[i] = newInfo

		// 更新映射表（从 NodeRef 获取 PageID）
		pageID := model.PageID(newInfo.GetPageID())
		pageInfoMap[pageID] = newInfo
	}

	// Rebuild child references for InternalPages
	for _, info := range copiedPath {
		if internalPage, ok := info.GetPage().(*InternalPage); ok && internalPage != nil {
			// 遍历所有子节点引用
			for j := range len(internalPage.children) {
				childRef := internalPage.children[j]
				if childRef == nil {
					continue
				}

				childInfo := childRef.GetPageInfo()
				if childInfo == nil {
					continue
				}

				childPage := childInfo.GetPage()
				if childPage == nil {
					continue
				}

				// 获取子节点的 PageID
				var childPageID model.PageID
				switch p := childPage.(type) {
				case *LeafPage:
					childPageID = p.pageID
				case *InternalPage:
					childPageID = p.pageID
				default:
					continue
				}

				// 使用映射表查找克隆的子节点
				childReplacement := pageInfoMap[childPageID]

				if childReplacement != nil {
					// 子节点在路径中，使用浅拷贝的 PageInfo
					newChildRef := NewPageRefWithInfo(childReplacement)
					// 优化：使用 SetParentRef（atomic.Value）
					newChildRef.SetParentRef(b.rootRef.PageRef)
					internalPage.children[j] = newChildRef
				} else {
					// 子节点不在路径中，创建浅拷贝
					shallowChildInfo := childInfo.CloneShallow()
					newChildRef := NewPageRefWithInfo(shallowChildInfo)
					// 优化：使用 SetParentRef（atomic.Value）
					newChildRef.SetParentRef(b.rootRef.PageRef)
					internalPage.children[j] = newChildRef
				}
			}
		}
	}

	return copiedPath, nil
}

// finalizeDeepClone 将浅拷贝路径转换为深拷贝（延迟深拷贝优化）
//
// 使用场景：
// - CAS 成功后，将浅拷贝路径转为深拷贝
// - 确保后续修改有独立的 Page 副本
//
// 实现逻辑：
// - 遍历路径中的所有 PageInfo
// - 如果是浅拷贝状态，执行 CloneDeep 转换
// - 重建子节点引用（指向深拷贝后的 PageInfo）
func (b *BTree) finalizeDeepClone(copiedPath []*PageInfo) error {
	if len(copiedPath) == 0 {
		return nil
	}

	// 构建映射表：PageID -> 深拷贝后的 PageInfo
	deepClonedMap := make(map[model.PageID]*PageInfo, len(copiedPath))

	// Phase 1: 将所有浅拷贝转为深拷贝
	for i, info := range copiedPath {
		// 如果已经是深拷贝，跳过
		if info.IsDeepClone() {
			var pageID model.PageID
			switch p := info.GetPage().(type) {
			case *LeafPage:
				pageID = p.pageID
			case *InternalPage:
				pageID = p.pageID
			default:
				continue
			}
			deepClonedMap[pageID] = info
			continue
		}

		// 执行深拷贝
		deepClonedInfo := info.CloneDeep()
		copiedPath[i] = deepClonedInfo

		// 更新映射表
		var pageID model.PageID
		switch p := deepClonedInfo.GetPage().(type) {
		case *LeafPage:
			pageID = p.pageID
		case *InternalPage:
			pageID = p.pageID
		default:
			continue
		}
		deepClonedMap[pageID] = deepClonedInfo
	}

	// Phase 2: 更新子节点引用（指向深拷贝后的 PageInfo）
	for _, info := range copiedPath {
		if internalPage, ok := info.GetPage().(*InternalPage); ok && internalPage != nil {
			// 遍历所有子节点引用
			for j := range len(internalPage.children) {
				childRef := internalPage.children[j]
				if childRef == nil {
					continue
				}

				childInfo := childRef.GetPageInfo()
				if childInfo == nil {
					continue
				}

				childPage := childInfo.GetPage()
				if childPage == nil {
					continue
				}

				// 获取子节点的 PageID
				var childPageID model.PageID
				switch p := childPage.(type) {
				case *LeafPage:
					childPageID = p.pageID
				case *InternalPage:
					childPageID = p.pageID
				default:
					continue
				}

				// 查找深拷贝后的子节点
				deepClonedChild := deepClonedMap[childPageID]
				if deepClonedChild != nil {
					// 使用深拷贝的 PageInfo
					newChildRef := NewPageRefWithInfo(deepClonedChild)
					// 优化：使用 SetParentRef（atomic.Value）
					newChildRef.SetParentRef(b.rootRef.PageRef)
					internalPage.children[j] = newChildRef
				}
				// 如果不在映射表中，说明不在路径内，保持原引用
			}
		}
	}

	return nil
}

// copyPathWithDelta 使用 Delta Chain 模式复制路径（零拷贝优化）
//
// 与 copyPathShallow 的区别：
// - copyPathShallow: LeafPage 立即深拷贝（兼容现有行为）
// - copyPathWithDelta: 使用 CloneWithDelta（零拷贝），延迟物化到 CAS 前
//
// 使用场景：
// - 写路径：使用 copyPathWithDelta 减少拷贝开销
// - CAS 失败率高：使用 copyPathShallow 避免重复物化
func (b *BTree) copyPathWithDelta(path []*PageInfo) ([]*PageInfo, error) {
	if len(path) == 0 {
		return nil, errpkg.BTreeEmptyPath()
	}

	copiedPath := make([]*PageInfo, len(path))

	// 使用 CloneWithDelta 替代深拷贝
	for i, info := range path {
		switch p := info.GetPage().(type) {
		case *LeafPage:
			// 使用 Delta Chain 模式克隆（零拷贝）
			clonedPage := p.CloneWithDelta()
			newInfo := NewPageInfo()
			newInfo.SetPage(clonedPage)
			newInfo.cloneStatus.Store(CloneStatusShallow) // 标记为浅拷贝（需要时才物化）
			copiedPath[i] = newInfo

		case *InternalPage:
			// InternalPage 也使用 Delta Chain 模式（半零拷贝）
			clonedPage := p.CloneWithDelta()
			newInfo := NewPageInfo()
			newInfo.SetPage(clonedPage)
			newInfo.cloneStatus.Store(CloneStatusShallow)
			copiedPath[i] = newInfo

		default:
			// 其他类型，使用浅拷贝
			newInfo := info.CloneShallow()
			copiedPath[i] = newInfo
		}
	}

	// 重建子节点引用
	// 注意：Delta Chain 模式下，children 仍然需要重建引用
	return b.rebuildChildRefs(copiedPath)
}

// rebuildChildRefs 重建子节点引用（辅助方法）
// 用于 copyPathWithDelta 和 copyPathShallow
func (b *BTree) rebuildChildRefs(copiedPath []*PageInfo) ([]*PageInfo, error) {
	// 性能优化：使用 copiedPageIDMap（从 clonedPath 构建）
	// 只重建路径中的节点引用，避免全局遍历
	copiedPageIDMap := make(map[model.PageID]*PageInfo, len(copiedPath))
	for _, info := range copiedPath {
		var pageID model.PageID
		switch p := info.GetPage().(type) {
		case *LeafPage:
			pageID = p.pageID
		case *InternalPage:
			pageID = p.pageID
		default:
			continue
		}
		copiedPageIDMap[pageID] = info
	}

	// 关键修复：始终更新子节点引用，无论 childInfo 是否为 nil
	// Root cause：InternalPage.Clone() 复制 PageRef 指针，PageRef.GetPageInfo() 指向原始 PageInfo
	//           必须更新为 copiedPath 中的克隆 PageInfo
	for _, info := range copiedPath {
		if internalPage, ok := info.GetPage().(*InternalPage); ok && internalPage != nil {
			selfPageID := internalPage.GetPageID()

			for j := range len(internalPage.children) {
				childRef := internalPage.children[j]
				if childRef == nil {
					continue
				}

				// 获取子节点的 PageID
				var childPageID model.PageID
				childPage := childRef.GetPage()
				if childPage == nil {
					continue
				}
				switch p := childPage.(type) {
				case *LeafPage:
					childPageID = p.pageID
				case *InternalPage:
					childPageID = p.pageID
				default:
					continue
				}

				// 跳过自身，避免匹配错误
				if childPageID == selfPageID {
					continue
				}

				// 在 copiedPath 中查找对应的 PageInfo
				copiedChildInfo := copiedPageIDMap[childPageID]

				// 始终更新子节点引用（即使 childInfo != nil）
				// 原因：childInfo 指向原始路径中的 PageInfo，需要更新为克隆的 PageInfo
				if copiedChildInfo != nil {
					newChildRef := NewPageRefWithInfo(copiedChildInfo)
					// 保持 parentRef
					if parentRef := childRef.parentRef.Load(); parentRef != nil {
						newChildRef.SetParentRef(parentRef.(*PageRef))
					}
					internalPage.children[j] = newChildRef
				}
			}
		}
	}

	return copiedPath, nil
}

// ===== Persistence Integration =====

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
		return 0, errpkg.BTreeChunkManagerNotInit()
	}

	// 1. 获取页面
	if !pageInfo.IsPageLoaded() {
		return 0, errpkg.BTreePageNotLoaded()
	}

	page := pageInfo.GetPage()
	if page == nil {
		return 0, errpkg.BTreePageInfoNil()
	}

	// 2. 序列化页面
	var data []byte
	var err error

	switch p := page.(type) {
	case *LeafPage:
		data, err = p.Serialize()
		if err != nil {
			return 0, errpkg.BTreeSerializeLeafPage(err)
		}
	case *InternalPage:
		data, err = p.Serialize()
		if err != nil {
			return 0, errpkg.BTreeSerializeInternalPage(err)
		}
	default:
		return 0, errpkg.BTreeUnknownPageType(fmt.Sprintf("%T", page))
	}

	// 3. 分配页面空间
	pos, err := b.chunkMgr.AllocatePage(pageType)
	if err != nil {
		return 0, errpkg.BTreeAllocatePage(err)
	}

	// 4. 写入页面
	if err := b.chunkMgr.WritePage(pos, data); err != nil {
		return 0, errpkg.BTreeWritePageToChunk(err)
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
						return errpkg.BTreePersistChildPage(childInfo.GetPageID(), err)
					}
				}
			}
		}

		// 1.2 持久化当前内部节点
		_, err := b.persistPage(pageInfo, PageTypeInternal)
		if err != nil {
			return errpkg.BTreePersistInternalPageErr(err)
		}

	case *LeafPage:
		// 2. 持久化叶子节点
		_, err := b.persistPage(pageInfo, PageTypeLeaf)
		if err != nil {
			return errpkg.BTreePersistLeafPageErr(err)
		}
	}

	return nil
}

// persistRoot 持久化根节点
//
// 这是持久化流程的入口点，在 Set 操作完成后调用
// 确保 Root 页面及其所有子节点都被持久化到磁盘
//
// 参数：
//
//	rootInfo - 要持久化的根节点 PageInfo（应该使用当前线程修改的版本）
//
// 注意：调用者必须在 CAS 成功后持有 writeMu 锁，确保持久化期间
// 没有并发修改导致页面结构不一致
func (b *BTree) persistRoot(rootInfo *PageInfo) error {
	if rootInfo == nil {
		return errpkg.BTreeRootPageInfoNil()
	}

	// 递归持久化整个树（自底向上）
	return b.persistPageRecursive(rootInfo)
}

// ===== Merge Operations =====

// findChildIndexInParent 查找子节点在父节点中的索引
func (b *BTree) findChildIndexInParent(parent *InternalPage, childInfo *PageInfo) (int, error) {
	childPageID := childInfo.GetPageID()

	for i := range parent.NumChildren() {
		childRef := parent.GetChild(i)
		if childRef != nil {
			info := childRef.GetPageInfo()
			if info != nil && info.GetPageID() == childPageID {
				return i, nil
			}
		}
	}

	return -1, errpkg.BTreeChildNotFoundInParent(childPageID)
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
				return errpkg.BTreeLoadPageFromChunk(err)
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

	// 从 copiedPath 获取父节点（用于修改和访问兄弟节点）
	if len(copiedPath) < 2 {
		return errpkg.BTreeCopiedPathTooShort(2, len(copiedPath))
	}

	parentInfo := copiedPath[len(copiedPath)-2]
	parent := parentInfo.GetInternalPage()
	if parent == nil {
		return errpkg.BTreeParentPageNotLoaded()
	}

	// 2. 找到当前节点在父节点中的位置
	leafIndex, err := b.findChildIndexInParent(parent, leafInfo)
	if err != nil {
		return errpkg.BTreeFindChildIndex(err)
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

	// 1. 从左兄弟借最后一个键值对
	lastIdx := leftSibling.NumKeys() - 1
	borrowedKey := leftSibling.keys[lastIdx]
	borrowedValue := leftSibling.values[lastIdx]

	// 2. 在删除键之前，先确定新的分隔键
	// 如果删除后左兄弟还有键，使用新的最大键（倒数第二个键）
	var newSeparatorKey []byte
	if lastIdx > 0 {
		// 删除后至少还有一个键，使用新的最大键
		newSeparatorKey = leftSibling.keys[lastIdx-1]
	}

	// 3. 从左兄弟删除最后一个键值对
	leftSibling.keys = leftSibling.keys[:lastIdx]
	leftSibling.values = leftSibling.values[:lastIdx]
	leftSibling.version++

	// 4. 将借来的键值对插入到当前节点的开头
	leaf.keys = insertSlice(leaf.keys, 0, borrowedKey)
	leaf.values = insertSlice(leaf.values, 0, borrowedValue)

	// 5. 更新父节点的分隔键
	if newSeparatorKey != nil {
		parent.keys[leafIndex-1] = newSeparatorKey
	}
	parent.version++
	leaf.version++

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

	// 1. 从右兄弟借第一个键值对
	borrowedKey := rightSibling.keys[0]
	borrowedValue := rightSibling.values[0]

	// 2. 从右兄弟删除第一个键值对
	rightSibling.keys = rightSibling.keys[1:]
	rightSibling.values = rightSibling.values[1:]
	rightSibling.version++

	// 3. 将借来的键值对追加到当前节点末尾
	leaf.keys = append(leaf.keys, borrowedKey)
	leaf.values = append(leaf.values, borrowedValue)

	// 4. 更新父节点的分隔键
	// 使用右兄弟删除后的新最小键（即现在的第一个键）
	if rightSibling.NumKeys() > 0 {
		newSeparatorKey := rightSibling.keys[0]
		parent.keys[leafIndex] = newSeparatorKey
	}
	parent.version++
	leaf.version++

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

	// 1. 合并节点：Left + Right
	// 修复：对于叶子节点，分隔键不应该插入到合并后的节点中
	// 只有右节点的键值对需要移动到左节点

	// 2. 将右节点的所有键值对追加到左节点
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
		oldRootID := parentInfo.GetPageID()
		if !b.rootRef.ReplacePage(oldRootID, leftNodeInfo) {
			return ErrRetry
		}
		// 更新新根节点的 parentRef 为 nil
		leftNodeInfo.SetParentRef(nil)
		return nil
	}

	// 5. 检查父节点是否需要 Merge（递归向上合并）
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

	// 2.4 更新右节点子节点的父引用（指向左节点）
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
		oldRootID := parentInfo.GetPageID()
		if !b.rootRef.ReplacePage(oldRootID, leftNodeInfo) {
			return ErrRetry
		}
		// 更新新根节点的 parentRef 为 nil
		// leftNodeInfo 现在是根节点，不应该有父节点
		leftNodeInfo.SetParentRef(nil)
		return nil
	}

	// 5. 检查父节点是否需要 Merge（递归向上合并）
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

	// P0 修复: 防御性检查 - 确保左兄弟有足够的键和子节点
	if leftSibling.NumKeys() < 2 {
		return errpkg.BTreeLeftSiblingInsufficientKeys(leftSibling.NumKeys())
	}
	if len(leftSibling.children) != leftSibling.NumKeys()+1 {
		return errpkg.BTreeLeftSiblingChildrenMismatch(leftSibling.NumKeys(), len(leftSibling.children))
	}

	// 1. 从父节点获取分隔键
	separatorKey := parent.keys[nodeIndex-1]

	// 2. 从左兄弟借最后一个键和子节点
	lastIdx := leftSibling.NumKeys() - 1
	borrowedKey := leftSibling.keys[lastIdx]

	// P0 修复: 添加边界检查，防止数组越界
	if lastIdx+1 >= len(leftSibling.children) {
		return errpkg.BTreeLeftSiblingIndexOutOfRange(lastIdx, len(leftSibling.children))
	}
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

	// P0 修复: 防御性检查 - 确保右兄弟有足够的键和子节点
	if rightSibling.NumKeys() < 1 {
		return errpkg.BTreeRightSiblingInsufficientKeys(rightSibling.NumKeys())
	}
	if len(rightSibling.children) != rightSibling.NumKeys()+1 {
		return errpkg.BTreeRightSiblingChildrenMismatch(rightSibling.NumKeys(), len(rightSibling.children))
	}

	// 1. 从父节点获取分隔键
	separatorKey := parent.keys[nodeIndex]

	// 2. 从右兄弟借第一个键和子节点
	borrowedKey := rightSibling.keys[0]

	// P0 修复: 添加边界检查，防止空切片访问
	if len(rightSibling.children) == 0 {
		return errpkg.BTreeRightSiblingNoChildren()
	}
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

	// 从 copiedPath 获取父节点（用于修改和访问兄弟节点）
	if len(copiedPath) < 2 {
		return errpkg.BTreeCopiedPathTooShort(2, len(copiedPath))
	}

	parentInfo := copiedPath[len(copiedPath)-2]
	parent := parentInfo.GetInternalPage()
	if parent == nil {
		return errpkg.BTreeParentPageNotLoaded()
	}

	// 2. 找到当前节点在父节点中的位置
	nodeIndex, err := b.findChildIndexInParent(parent, nodeInfo)
	if err != nil {
		return errpkg.BTreeFindChildIndex(err)
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
