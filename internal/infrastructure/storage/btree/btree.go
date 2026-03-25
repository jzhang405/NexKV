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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/wal"
)

var (
	// ErrNotImplemented is returned when a method is not yet implemented.
	ErrNotImplemented = errors.New("not implemented")

	// ErrClosed is returned when operations are performed on a closed BTree.
	ErrClosed = errors.New("btree is closed")

	// ErrRetry is returned when a CAS operation fails and the caller should retry.
	ErrRetry = errors.New("cas failed, retry operation")

	// ErrInvalidPath is returned when path finding fails due to invalid node structure.
	ErrInvalidPath = errors.New("invalid path: node structure inconsistent")

	// ErrKeyNotFound is returned when a key is not found in the tree.
	ErrKeyNotFound = errors.New("key not found")

	// ErrPageStale is returned when a page state is stale during concurrent operations.
	// This can happen when a Get operation reads a page that is being split or freed.
	// The caller should retry the operation.
	ErrPageStale = errors.New("page state stale, retry")
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
		// 验证 PageRef 是否仍然有效
		// 如果缓存的 PageRef 的 pageID 与请求的 pageID 不匹配，说明缓存不一致
		currentInfo := ref.GetPageInfo()
		if currentInfo != nil && currentInfo.GetPageID() != uint64(pageID) {
			// 缓存不一致：pageID 被重用，但缓存仍指向旧的 PageInfo
			// 重新创建 PageRef
			c.mu.Lock()
			defer c.mu.Unlock()

			// Double-check after acquiring write lock
			if ref, ok := c.cache[pageID]; ok && ref.GetPageInfo().GetPageID() == uint64(pageID) {
				return ref
			}

			// 创建新的 PageRef
			info := NewPageInfo()
			info.SetNodeRef(offheap.NewNodeRef(uint32(pageID), isLeaf))
			newRef := NewPageRefWithInfo(info)
			c.cache[pageID] = newRef
			return newRef
		}
		return ref
	}

	// 需要创建新的 PageRef
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check
	if ref, ok := c.cache[pageID]; ok {
		return ref
	}

	// 创建新的 PageInfo 和 PageRef
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

	// Root management
	rootRef *RootPageRef // Root page reference (atomic updates)

	// Storage
	chunkMgr *ChunkManager // Append-only storage manager
	wal      wal.WAL       // Write-Ahead Log for crash recovery

	// Off-Heap storage (方案 B：完全替换)
	offheapPM       *offheap.PageManager       // Off-Heap 页面管理器
	offheapAdapter  *OffHeapAdapter            // Off-Heap 适配器
	pageRefCache    *PageRefCache              // PageID → PageRef 映射（Off-Heap 模式）

	// Configuration
	maxLevels int  // Maximum tree levels
	enableWAL bool // Enable WAL logging

	// PageID management
	nextPageID atomic.Uint64 // Next page ID to allocate (lock-free)

	// Persistence coordination
	writeMu sync.Mutex // Global write lock for persistence operations

	// Performance optimization
	stats            *PageStats // 页面访问统计（热数据识别）
	hotPageThreshold int64      // 热数据阈值（来自配置）

	// Scheduler for concurrent write operations (方案 2：移除 Direct 模式)
	scheduler *concurrency.TaskScheduler // Task scheduler for concurrent operations

	// Split coordination: 防止多个 goroutine 同时分裂同一页面
	splitMuMap sync.Map // map[uint32]*sync.Mutex - 页面级别的分裂锁

	// Epoch-based page release: 延迟释放页面避免竞态条件
	epochBasedFreeList *EpochBasedFreeList
}

// EpochBasedFreeList 延迟释放列表
// 页面在 CAS 成功后不立即释放，而是加入当前 epoch 的待释放列表
// 在下一个 epoch 开始时才真正释放页面
type EpochBasedFreeList struct {
	currentEpoch uint64                 // 当前 epoch
	pending      map[uint64][]model.PageID // epoch → 待释放页面列表
	mu           sync.Mutex
}

// NewEpochBasedFreeList 创建延迟释放列表
func NewEpochBasedFreeList() *EpochBasedFreeList {
	return &EpochBasedFreeList{
		currentEpoch: 0,
		pending:      make(map[uint64][]model.PageID),
	}
}

// Add 添加页面到当前 epoch 的待释放列表
func (e *EpochBasedFreeList) Add(pageID model.PageID) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 调试日志：记录页面释放请求
	stack := debug.Stack()
	// 提取关键调用栈信息
	caller := "<unknown>"
	stackStr := string(stack)
	for _, line := range strings.Split(stackStr, "\n") {
		if strings.Contains(line, "handleSplitOffHeapSync") || strings.Contains(line, "splitInternal") {
			caller = "split"
			break
		}
	}

	fmt.Printf("[EPOCH_ADD] epoch=%d pageID=%d caller=%s pending_count=%d\n",
		e.currentEpoch, pageID, caller, len(e.pending[e.currentEpoch]))

	e.pending[e.currentEpoch] = append(e.pending[e.currentEpoch], pageID)
}

// AdvanceEpoch 推进 epoch，释放 2 个 epoch 之前的页面
// 延迟 2 个 epoch 策略：
// - 当前 epoch: N
// - 正在使用的 epoch: N-1（可能还有 goroutine 在访问）
// - 可以安全释放的 epoch: N-2（所有 goroutine 应该已经完成）
func (e *EpochBasedFreeList) AdvanceEpoch(pm *offheap.PageManager) {
	e.mu.Lock()
	defer e.mu.Unlock()

	oldEpoch := e.currentEpoch
	e.currentEpoch++

	// 释放 3 个 epoch 之前的页面（currentEpoch - 3）- 增加延迟到 3 个 epoch
	// 第一步：将 N-2 的页面加入延迟释放列表
	epochToDelayed := e.currentEpoch - 2
	if epochToDelayed >= 0 {
		pagesToDelayed := e.pending[epochToDelayed]
		delete(e.pending, epochToDelayed)

		for _, pid := range pagesToDelayed {
			fmt.Printf("[EPOCH_DELAYED] epoch=%d pageID=%d\n", epochToDelayed, pid)
			pm.Free(uint32(pid))
		}
	}

	// 第二步：将 N-3 的页面从延迟释放列表移到可用列表
	epochToFree := e.currentEpoch - 3
	if epochToFree >= 0 {
		pagesToFree := e.pending[epochToFree]
		delete(e.pending, epochToFree)

		// 调试日志：记录 epoch 推进
		fmt.Printf("[EPOCH_ADVANCE] old=%d new=%d freeing_epoch=%d pages_to_free=%d\n",
			oldEpoch, e.currentEpoch, epochToFree, len(pagesToFree))

		// 将延迟释放列表中的页面移到可用列表
		moved := pm.AdvanceDelayedFreeList()
		if moved > 0 {
			fmt.Printf("[EPOCH_DELAYED_ADVANCE] moved=%d pages from delayed to available\n", moved)
		}
	} else {
		// 还没有到达可以释放的 epoch
		fmt.Printf("[EPOCH_ADVANCE] old=%d new=%d pages_to_free=0 (waiting for epoch 3)\n",
			oldEpoch, e.currentEpoch)
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
	return fmt.Errorf("item does not implement ShardItem interface")
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
	var walImpl wal.WAL
	enableWAL := dir != ""

	if dir != "" {
		// Open ChunkManager for append-only storage
		cm, err := NewChunkManager(dir)
		if err != nil {
			return nil, fmt.Errorf("open chunk manager: %w", err)
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
			return nil, fmt.Errorf("open WAL: %w", err)
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
		return nil, fmt.Errorf("create offheap page manager: %w", err)
	}

	// 创建 OffHeapAdapter
	offheapAdapter := NewOffHeapAdapter(offheapPM)

	// 分配初始根叶子节点（使用 Off-Heap）
	initialRootPageID, err := offheapAdapter.AllocLeafPage()
	if err != nil {
		offheapPM.Close()
		return nil, fmt.Errorf("alloc initial root leaf page: %w", err)
	}

	// 创建初始根 PageInfo（使用 NodeRef）
	initialRootInfo := NewPageInfo()
	initialRootInfo.SetNodeRef(offheap.NewNodeRef(uint32(initialRootPageID), true)) // true = isLeaf
	initialRootInfo.SetParentRef(nil) // 根节点没有父引用

	rootPageRef := NewRootPageRefWithInfo(initialRootInfo)

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
		config:              config,
		cowConfig:           cowConfig,
		closed:              false,
		rootRef:             rootPageRef,
		chunkMgr:            chunkMgr,
		wal:                 walImpl,
		offheapPM:           offheapPM,
		offheapAdapter:      offheapAdapter,
		pageRefCache:        pageRefCache,
		maxLevels:           maxLevels,
		enableWAL:           enableWAL,
		stats:               stats,
		hotPageThreshold:    config.HotPageThreshold,
		epochBasedFreeList:  epochBasedFreeList,
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
			return nil, fmt.Errorf("replay WAL: %w", err)
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
				// 等待任务完成
				if task, ok := item.(interface{ Wait(context.Context) (interface{}, error) }); ok {
					_, _ = task.Wait(context.Background())
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
		return nil, fmt.Errorf("register btree-set task: %w", err)
	}

	// 注册 btree-split 任务（异步父节点分裂）
	// 基于 Lealone 的 asyncSplitPage() 设计
	err = btree.scheduler.RegisterTask(
		func(item any) concurrency.TaskStatus {
			// 检查 item 是否实现了 TaskRunner 接口
			if runner, ok := item.(model.TaskRunner); ok {
				// 同步执行任务（TaskScheduler 已经在 Worker 线程中）
				runner.Run(context.Background(), nil)
				// 等待任务完成
				if task, ok := item.(interface{ Wait(context.Context) (interface{}, error) }); ok {
					_, _ = task.Wait(context.Background())
				}
				return concurrency.TaskPassed
			}
			return concurrency.TaskPassed
		},
		"btree-split",
		model.TaskPriorityHigh, // 高优先级，优先处理父节点分裂
		1,                       // executionOrder = 1 (数组索引 1)
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
		return nil, fmt.Errorf("register btree-split task: %w", err)
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
		return nil, fmt.Errorf("create per-core executor: %w", err)
	}

	if err := btree.scheduler.Start(executor); err != nil {
		// 清理资源
		if chunkMgr != nil {
			chunkMgr.Close()
		}
		if walImpl != nil {
			walImpl.Close()
		}
		return nil, fmt.Errorf("start scheduler: %w", err)
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

	const maxRetries = 5  // 最多重试 5 次（增加以提高并发成功率）

	// 调试：追踪 key-06151、key-06267、key-06709 和 key-09803 的查找过程
	debugThisKey := string(key) == "key-06151" || string(key) == "key-06150" || string(key) == "key-06152" ||
		string(key) == "key-06267" || string(key) == "key-06709" || string(key) == "key-09803"

	for attempt := 0; attempt < maxRetries; attempt++ {
		if debugThisKey {
			fmt.Printf("[GET_DEBUG] key=%s attempt=%d\n", string(key), attempt)
		}

		// Off-Heap 模式：使用 searchPathWithRefs
		leafRef, path, err := b.findLeafPageRef(ctx, key)
		if err != nil {
			// 如果查找路径失败且不是最后一次尝试，重试
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			return nil, fmt.Errorf("find leaf ref: %w", err)
		}

		if len(path) == 0 || leafRef == nil {
			if debugThisKey {
				fmt.Printf("[GET_DEBUG] key=%s path=%d leafRef=%v\n", string(key), len(path), leafRef != nil)
			}
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			return nil, ErrKeyNotFound
		}

		// 获取叶子节点的 PageInfo
		leafInfo := leafRef.GetPageInfo()
		if leafInfo == nil {
			if debugThisKey {
				fmt.Printf("[GET_DEBUG] key=%s leafInfo=nil\n", string(key))
			}
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
			return nil, fmt.Errorf("leaf page not loaded")
		}

		// 使用 OffHeapAdapter.GetFromOffHeap 直接读取
		value, found, err := b.offheapAdapter.GetFromOffHeap(leafPageID, key)
		if debugThisKey {
			fmt.Printf("[GET_DEBUG] key=%s leafPageID=%d found=%v err=%v\n", string(key), leafPageID, found, err)
			// 打印页面的所有 keys
			count := b.offheapAdapter.pa.GetCount(uint32(leafPageID))
			fmt.Printf("[GET_DEBUG] key=%s leafPageID=%d count=%d\n", string(key), leafPageID, count)
			if count > 0 {
				// 打印前5个和后5个 keys
				maxPrint := 5
				if int(count) <= maxPrint*2 {
					maxPrint = int(count)
				}
				for i := 0; i < maxPrint; i++ {
					keyOff, keyLen, _, _ := b.offheapAdapter.pa.GetLeafEntryOffset(uint32(leafPageID), i)
					pageKey := b.offheapAdapter.pa.GetKey(uint32(leafPageID), keyOff, keyLen)
					fmt.Printf("[GET_DEBUG]   key[%d]=%s\n", i, string(pageKey))
				}
				if int(count) > maxPrint*2 {
					fmt.Printf("[GET_DEBUG]   ... (%d more keys)\n", int(count)-maxPrint*2)
					for i := int(count) - maxPrint; i < int(count); i++ {
						keyOff, keyLen, _, _ := b.offheapAdapter.pa.GetLeafEntryOffset(uint32(leafPageID), i)
						pageKey := b.offheapAdapter.pa.GetKey(uint32(leafPageID), keyOff, keyLen)
						fmt.Printf("[GET_DEBUG]   key[%d]=%s\n", i, string(pageKey))
					}
				}
			}
		}
		if err != nil {
			// 如果读取错误且不是最后一次尝试，重试
			if attempt < maxRetries-1 {
				runtime.Gosched()
				continue
			}
			return nil, fmt.Errorf("offheap get: %w", err)
		}

		if found {
			// 成功找到，推进 epoch 释放待释放页面
			b.epochBasedFreeList.AdvanceEpoch(b.offheapPM)
			if debugThisKey {
				fmt.Printf("[GET_DEBUG] key=%s FOUND\n", string(key))
			}
			return value, nil  // 成功找到，直接返回
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := b.setWithLeafLock(ctx, key, value)
		switch err {
		case nil:
			// 操作成功，推进 epoch 释放待释放页面
			b.epochBasedFreeList.AdvanceEpoch(b.offheapPM)
			return nil
		case ErrRetry:
			runtime.Gosched()
			continue
		default:
			return err
		}
	}
}

// Delete removes a key (not implemented).
func (b *BTree) Delete(ctx context.Context, key []byte) error {
	if b.closed {
		return ErrClosed
	}

	// 实现 Delete 操作，集成 mergeLeaf
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
			// 重新启用 Merge，使用原始 path 访问兄弟节点
			if err := b.mergeLeaf(leafInfo, copiedPath, path); err != nil {
				return fmt.Errorf("merge leaf: %w", err)
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
				return fmt.Errorf("persist root: %w", err)
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
	return nil, errors.New("BeginTx: not implemented")
}

// CreateSnapshot creates a snapshot (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现快照隔离功能
func (b *BTree) CreateSnapshot(ctx context.Context) (service.SnapshotID, error) {
	if b.closed {
		return 0, ErrClosed
	}
	return 0, errors.New("CreateSnapshot: not implemented")
}

// ReleaseSnapshot releases a snapshot (not implemented).
//
//nolint:unused // 未实现的 API 接口，将来实现快照隔离功能
func (b *BTree) ReleaseSnapshot(ctx context.Context, id service.SnapshotID) error {
	if b.closed {
		return ErrClosed
	}
	return errors.New("ReleaseSnapshot: not implemented")
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

	// Use Set() with WAL disabled
	return b.Set(ctx, key, value)
}

// allocatePageID allocates a new unique page ID.
// This ensures that each newly created page has a unique identifier.
// 无锁实现：使用 atomic.Uint64
func (b *BTree) allocatePageID() model.PageID {
	// 使用 atomic.Add(1) 原子操作：读取旧值、加1、返回新值
	// 完全无锁，多个 goroutine 可以并发调用
	return model.PageID(b.nextPageID.Add(1))
}

// Close closes the BTree storage engine and releases resources.
func (b *BTree) Close() error {
	b.closedMu.Lock()
	defer b.closedMu.Unlock()

	if b.closed {
		return nil // Already closed
	}

	// 方案 2：停止内置 TaskScheduler
	if b.scheduler != nil {
		b.scheduler.Stop()
		b.scheduler = nil
	}

	// Close ChunkManager
	if b.chunkMgr != nil {
		if err := b.chunkMgr.Close(); err != nil {
			return fmt.Errorf("close chunk manager: %w", err)
		}
	}

	// Close OffHeap PageManager
	if b.offheapPM != nil {
		b.offheapPM.Close()
	}

	// Close WAL
	if b.wal != nil {
		if err := b.wal.Close(); err != nil {
			return fmt.Errorf("close WAL: %w", err)
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
//	any - 页面对象
//	error - 错误信息
func (b *BTree) getPageOrLoad(info *PageInfo) (any, error) {
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
		return nil, fmt.Errorf("empty path")
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
			for j := 0; j < len(internalPage.children); j++ {
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
		return nil, fmt.Errorf("empty path")
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
	// Off-Heap 模式：使用 Clone() 克隆 NodeRef
	for i, info := range path {
		// Off-Heap 模式：统一使用 Clone()
		newInfo := info.Clone()
		copiedPath[i] = newInfo

		// 更新映射表（从 NodeRef 获取 PageID）
		pageID := model.PageID(newInfo.GetPageID())
		pageInfoMap[pageID] = newInfo
	}

	// Rebuild child references for InternalPages
	for _, info := range copiedPath {
		if internalPage, ok := info.GetPage().(*InternalPage); ok && internalPage != nil {
			// 遍历所有子节点引用
			for j := 0; j < len(internalPage.children); j++ {
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
			for j := 0; j < len(internalPage.children); j++ {
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
		return nil, fmt.Errorf("empty path")
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
	return b.rebuildChildRefs(path, copiedPath)
}

// rebuildChildRefs 重建子节点引用（辅助方法）
// 用于 copyPathWithDelta 和 copyPathShallow
func (b *BTree) rebuildChildRefs(originalPath, copiedPath []*PageInfo) ([]*PageInfo, error) {
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

			for j := 0; j < len(internalPage.children); j++ {
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
func (b *BTree) splitLeaf(leafInfo *PageInfo, key []byte, copiedPath []*PageInfo) error {
	const maxKeys = 200 // LeafPage 最大键数量（优化性能）

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

	// 为新页面分配唯一的 pageID
	newPage.pageID = b.allocatePageID()

	// 4. 创建新页面的 PageInfo（直接使用，不创建 PageRef）
	newPageInfo := NewPageInfo()
	newPageInfo.SetPage(newPage)
	// parentRef 稍后设置

	// 5. 检查是否有父节点
	if len(copiedPath) < 2 {
		// 没有父节点，说明当前只有根叶子节点
		// 需要创建新的内部节点作为根
		casSuccess, err := b.splitRootFromLeaf(leafInfo, newPageInfo, key, splitKey, copiedPath)
		if err != nil {
			return err
		}
		if !casSuccess {
			return ErrRetry
		}
		return nil
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
// 返回值：
//   - bool: CAS 是否成功
//   - error: 错误信息
func (b *BTree) splitRootFromLeaf(leftInfo, rightInfo *PageInfo, key []byte, splitKey []byte, copiedPath []*PageInfo) (bool, error) {
	// 1. 创建新的内部节点作为根
	newRootPage := NewInternalPage(b.allocatePageID()) // 分配唯一的 pageID
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
	oldRootID := uint64(0)
	if oldRootInfo != nil {
		oldRootID = oldRootInfo.GetPageID()
	}
	if !b.rootRef.ReplacePage(oldRootID, newRootInfo) {
		// CAS 失败，返回 false 让调用者重试
		return false, nil
	}

	// CAS 成功，更新 copiedPath[0] 以保持一致性
	copiedPath[0] = newRootInfo

	// 修复：确定新键应该去哪个子节点，更新 copiedPath[len(copiedPath)-1]
	// LeafPage.Split() 后，左页面包含键 [0, mid)，右页面包含键 [mid, end)
	// 分裂键 splitKey = keys[mid] 在右页面中
	// 所以：key < splitKey 去 leftInfo，key >= splitKey 去 rightInfo
	var targetLeafInfo *PageInfo
	if bytes.Compare(key, splitKey) < 0 {
		targetLeafInfo = leftInfo
	} else {
		targetLeafInfo = rightInfo
	}
	copiedPath[len(copiedPath)-1] = targetLeafInfo

	// 5. 引用更新机制
	// 更新子节点的 parentRef
	leftInfo.SetParentRef(b.rootRef.PageRef)
	rightInfo.SetParentRef(b.rootRef.PageRef)

	// 6. 持久化集成
	// 分裂完成后，持久化整个树
	if b.chunkMgr != nil {
		if err := b.persistRoot(newRootInfo); err != nil {
			return false, fmt.Errorf("persist root after split: %w", err)
		}
	}

	// CAS 成功，返回 true
	return true, nil
}

// splitInternal 分裂内部节点（CCOW 版本）
// 当内部节点满时（len(keys) > 15），进行分裂操作
//
// 完整实现，支持 CCOW 和引用更新
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
	const maxKeys = 199 // InternalPage 最大键数量（优化性能，通常比 LeafPage 少 1）

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

	// 为新页面分配唯一的 pageID
	newPage.pageID = b.allocatePageID()

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
func (b *BTree) splitRootFromInternal(leftInfo, rightInfo *PageInfo, splitKey []byte, copiedPath []*PageInfo) error { //nolint:unused // copiedPath 预留用于未来优化
	// 1. 创建新的内部节点作为根
	newRootPage := NewInternalPage(b.allocatePageID()) // 分配唯一的 pageID
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
	oldRootID := uint64(0)
	if oldRootInfo != nil {
		oldRootID = oldRootInfo.GetPageID()
	}
	if !b.rootRef.ReplacePage(oldRootID, newRootInfo) {
		return ErrRetry
	}

	// 5. 引用更新机制
	// 更新子节点的 parentRef
	leftInfo.SetParentRef(b.rootRef.PageRef)
	rightInfo.SetParentRef(b.rootRef.PageRef)

	// 6. 递归更新子节点树
	// 更新左子树的所有子孙节点的 parentRef
	b.updateChildrenParentRefs(leftInfo, b.rootRef.PageRef)
	// 更新右子树的所有子孙节点的 parentRef
	b.updateChildrenParentRefs(rightInfo, b.rootRef.PageRef)

	// 7. 持久化集成
	// 分裂完成后，持久化整个树
	if b.chunkMgr != nil {
		if err := b.persistRoot(newRootInfo); err != nil {
			return fmt.Errorf("persist root after split: %w", err)
		}
	}

	return nil
}

// updateChildrenParentRefs 递归更新子节点树的 parentRef
// 引用更新机制的核心方法
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
//
// 参数：
//
//	rootInfo - 要持久化的根节点 PageInfo（应该使用当前线程修改的版本）
//
// 注意：调用者必须在 CAS 成功后持有 writeMu 锁，确保持久化期间
// 没有并发修改导致页面结构不一致
func (b *BTree) persistRoot(rootInfo *PageInfo) error {
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
		return fmt.Errorf("left sibling has insufficient keys to borrow: %d", leftSibling.NumKeys())
	}
	if len(leftSibling.children) != leftSibling.NumKeys()+1 {
		return fmt.Errorf("left sibling children count mismatch: keys=%d, children=%d",
			leftSibling.NumKeys(), len(leftSibling.children))
	}

	// 1. 从父节点获取分隔键
	separatorKey := parent.keys[nodeIndex-1]

	// 2. 从左兄弟借最后一个键和子节点
	lastIdx := leftSibling.NumKeys() - 1
	borrowedKey := leftSibling.keys[lastIdx]

	// P0 修复: 添加边界检查，防止数组越界
	if lastIdx+1 >= len(leftSibling.children) {
		return fmt.Errorf("left sibling children index out of range: lastIdx=%d, children_len=%d",
			lastIdx, len(leftSibling.children))
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
		return fmt.Errorf("right sibling has insufficient keys to borrow: %d", rightSibling.NumKeys())
	}
	if len(rightSibling.children) != rightSibling.NumKeys()+1 {
		return fmt.Errorf("right sibling children count mismatch: keys=%d, children=%d",
			rightSibling.NumKeys(), len(rightSibling.children))
	}

	// 1. 从父节点获取分隔键
	separatorKey := parent.keys[nodeIndex]

	// 2. 从右兄弟借第一个键和子节点
	borrowedKey := rightSibling.keys[0]

	// P0 修复: 添加边界检查，防止空切片访问
	if len(rightSibling.children) == 0 {
		return fmt.Errorf("right sibling has no children")
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
