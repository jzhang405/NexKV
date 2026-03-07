// Package bftree 提供 Bf-Tree 存储引擎实现
//
// Bf-Tree 是 B+ 树的优化变体，使用 Mini-Page 机制和 Delta Chain 优化，
// 减少写入放大，提升并发性能。
package bftree

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/wal"
)

// BfTree Bf-Tree 存储引擎
//
// 核心设计：
// - Mini-Page 机制：3-level 分层存储（L1-L6/Full）
// - Delta Chain 优化：写入先记录到 Delta Chain，定期合并
// - PageTable 管理：页面生命周期管理（引用计数、固定机制）
// - WAL 集成：预写日志保证持久性
// - 并发控制：RWMutex（MVP），未来可升级到 BitmapLock
type BfTree struct {
	// 核心组件
	rootPageID uint64     // 根页面 ID
	pageTable  *PageTable // 页面表管理器
	pageStore  *pageStore // 页面存储（MVP：内存存储）
	config     *Config    // 配置

	// WAL 集成
	wal        wal.WAL // 预写日志
	walEnabled bool    // WAL 开关

	// 并发控制
	rwLock sync.RWMutex // 读写锁（MVP）
	bitmapLock    *BitmapLock   // BitmapLock（P1 优化）
	useBitmapLock bool          // 是否使用 BitmapLock

	// 状态管理
	closed atomic.Bool // 关闭标志
	stats  BfTreeStats // 统计信息
}

// BfTreeStats Bf-Tree 统计信息
type BfTreeStats struct {
	// 页面统计
	TotalPages     int64 // 总页面数
	LeafPages      int64 // 叶子页面数
	InnerPages     int64 // 内部页面数
	TotalPageBytes int64 // 页面总字节数

	// 操作统计
	ReadCount   int64 // 读操作次数
	WriteCount  int64 // 写入操作次数
	DeleteCount int64 // 删除操作次数

	// Delta Chain 统计
	TotalDeltas    int64 // Delta Chain 总条目数
	CompactCount   int64 // 合并操作次数
	TotalDeltaSize int64 // Delta Chain 总大小（字节）

	// WAL 统计
	WALAppends    int64 // WAL 追加次数
	WALSyncCount  int64 // WAL 同步次数
	WALTotalBytes int64 // WAL 总字节数
}

// NewBfTree 创建新的 Bf-Tree
func NewBfTree(config *Config) (*BfTree, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// 创建 PageTable
	pageTable := NewPageTable()

	// 创建 pageStore
	pageStore := newPageStore()

	// 创建 WAL（如果启用）
	var w wal.WAL
	var walErr error
	if config.EnableWAL {
		walConfig := &wal.WALConfig{
			Dir:         config.WALDir,
			SegmentSize: config.SegmentSize,
			SyncPolicy:  wal.SyncPolicyEveryWrite,
		}
		w, walErr = wal.NewDiskWAL(walConfig)
		if walErr != nil {
			return nil, fmt.Errorf("failed to create WAL: %w", walErr)
		}
	}

	tree := &BfTree{
		rootPageID: 0, // 初始为空树
		pageTable:  pageTable,
		pageStore:  pageStore,
		config:     config,
		wal:        w,
		walEnabled:    config.EnableWAL,
		useBitmapLock: config.UseBitmapLock,
	}

	// 恢复 WAL（如果启用）
	if config.EnableWAL {
		if err := tree.recover(); err != nil {
			// 关闭 WAL
			_ = w.Close()
			return nil, fmt.Errorf("failed to recover from WAL: %w", err)
		}
	}


	// 初始化 BitmapLock（如果启用）
	if config.UseBitmapLock {
		tree.bitmapLock = NewBitmapLock(config.BitmapLockShards)
	}

	return tree, nil
}

// recover 从 WAL 恢复数据
func (t *BfTree) recover() error {
	if !t.walEnabled {
		return nil
	}

	// 读取 WAL 日志
	entries, err := t.wal.Recover()
	if err != nil {
		return fmt.Errorf("failed to recover WAL: %w", err)
	}

	// 重放日志
	for _, entry := range entries {
		if err := t.applyWALEntry(entry); err != nil {
			return fmt.Errorf("failed to apply WAL entry LSN=%d: %w", entry.LSN, err)
		}
	}

	return nil
}

// applyWALEntry 应用 WAL 日志条目
func (t *BfTree) applyWALEntry(entry *wal.WALEntry) error {
	t.rwLock.Lock()
	defer t.rwLock.Unlock()

	switch entry.Type {
	case wal.WALTypeInsert:
		return t.insertLocked(entry.Key, entry.Value, false) // false = 不写 WAL
	case wal.WALTypeUpdate:
		return t.updateLocked(entry.Key, entry.Value, false)
	case wal.WALTypeDelete:
		return t.deleteLocked(entry.Key, false)
	default:
		return nil // 忽略其他类型
	}
}

// Get 获取键值（同步）
func (t *BfTree) Get(ctx context.Context, key []byte) ([]byte, error) {
	if t.closed.Load() {
		return nil, ErrTreeClosed
	}

	t.rwLock.RLock()
	defer t.rwLock.RUnlock()

	atomic.AddInt64(&t.stats.ReadCount, 1)

	return t.lookup(key)
}

// lookup 查找键（内部，已持有锁）
func (t *BfTree) lookup(key []byte) ([]byte, error) {
	// 空树
	if t.rootPageID == 0 {
		return nil, ErrKeyNotFound
	}

	// 从根节点开始查找
	currentPageID := t.rootPageID

	for {
		entry, found := t.pageTable.Get(currentPageID)
		if !found {
			return nil, ErrPageNotFound
		}

		// 根据页面类型处理
		switch entry.pageType {
		case PageTypeLeaf:
			// 叶子节点：直接查找键
			leafNode, err := t.pageStore.getLeaf(currentPageID)
			if err != nil {
				return nil, err
			}
			value, found := leafNode.Get(key)
			if !found {
				return nil, ErrKeyNotFound
			}
			return value, nil

		case PageTypeInner:
			// 内部节点：继续向下查找
			innerNode, err := t.pageStore.getInner(currentPageID)
			if err != nil {
				return nil, err
			}
			childID, found := innerNode.FindChild(key)
			if !found {
				return nil, ErrKeyNotFound
			}
			currentPageID = childID

		default:
			return nil, fmt.Errorf("unknown page type: %d", entry.pageType)
		}
	}
}

// Set 设置键值（同步）
func (t *BfTree) Set(ctx context.Context, key, value []byte) error {
	if t.closed.Load() {
		return ErrTreeClosed
	}

	t.rwLock.Lock()
	defer t.rwLock.Unlock()

	atomic.AddInt64(&t.stats.WriteCount, 1)

	// 写 WAL
	if t.walEnabled {
		entry := wal.NewWALEntry(wal.WALTypeInsert, 0, key, value, wal.LSNInvalid)
		if _, err := t.wal.Append(entry); err != nil {
			return fmt.Errorf("failed to append WAL: %w", err)
		}
		atomic.AddInt64(&t.stats.WALAppends, 1)
		atomic.AddInt64(&t.stats.WALTotalBytes, int64(len(key)+len(value)))
	}

	return t.insertLocked(key, value, true)
}

// insertLocked 插入键值（内部，已持有锁）
func (t *BfTree) insertLocked(key, value []byte, writeWAL bool) error {
	// 空树：创建根节点
	if t.rootPageID == 0 {
		pageID, err := t.pageTable.Alloc(PageTypeLeaf, L1)
		if err != nil {
			return err
		}
		t.rootPageID = pageID

		leafNode := NewLeafNode(pageID, L1)
		if err := leafNode.Set(key, value); err != nil {
			return err
		}
		t.pageStore.putLeaf(pageID, leafNode)
		atomic.AddInt64(&t.stats.LeafPages, 1)
		return nil
	}

	// 非空树：查找插入位置的叶子节点
	leafPageID, err := t.findLeafPage(t.rootPageID, key)
	if err != nil {
		return err
	}

	// 获取叶子节点
	leafNode, err := t.pageStore.getLeaf(leafPageID)
	if err != nil {
		return err
	}

	// 插入到叶子节点
	if err := leafNode.Set(key, value); err != nil {
		// 检查是否需要分裂（Delta Chain 满）
		if err == ErrDeltaFull {
			// 分裂叶子节点
			leftPageID, rightPageID, splitKey, oldPageID, splitErr := t.splitLeafNode(leafPageID)
			if splitErr != nil {
				return fmt.Errorf("failed to split leaf node: %w", splitErr)
			}

			// Phase 2.3: 使用 insertSplitIntoParent 支持多级分裂
			// 找到父节点
			parentPageID, err := t.findParent(leafPageID)
			if err != nil {
				return fmt.Errorf("failed to find parent: %w", err)
			}

			// 将分裂结果插入父节点（支持多级分裂）
			if insertErr := t.insertSplitIntoParent(parentPageID, leftPageID, rightPageID, splitKey); insertErr != nil {
				return fmt.Errorf("failed to insert split to parent: %w", insertErr)
			}

			// 成功后释放旧节点
			_ = t.pageTable.Free(oldPageID)

			// 重试插入：根据键值决定插入左或右节点
			targetPageID := leftPageID
			if compareKeys(key, splitKey) >= 0 {
				targetPageID = rightPageID
			}

			// 重新获取目标节点并插入
			targetNode, getNodeErr := t.pageStore.getLeaf(targetPageID)
			if getNodeErr != nil {
				return fmt.Errorf("failed to get target node after split: %w", getNodeErr)
			}

			if setErr := targetNode.Set(key, value); setErr != nil {
				return fmt.Errorf("failed to insert after split: %w", setErr)
			}

			return nil
		}
		return err
	}

	return nil
}

// findLeafPage 查找键应该所在的叶子页面
func (t *BfTree) findLeafPage(rootPageID uint64, key []byte) (uint64, error) {
	currentPageID := rootPageID

	for {
		entry, found := t.pageTable.Get(currentPageID)
		if !found {
			return 0, ErrPageNotFound
		}

		if entry.pageType == PageTypeLeaf {
			return currentPageID, nil
		}

		// 内部节点：继续向下
		innerNode, err := t.pageStore.getInner(currentPageID)
		if err != nil {
			return 0, err
		}

		childID, found := innerNode.FindChild(key)
		if !found {
			// 返回最左边的子节点
			if len(innerNode.children) == 0 {
				return 0, ErrPageNotFound
			}
			childID = innerNode.children[0]
		}
		currentPageID = childID
	}
}

// Update 更新键值（同步）
func (t *BfTree) Update(ctx context.Context, key, value []byte) error {
	if t.closed.Load() {
		return ErrTreeClosed
	}

	t.rwLock.Lock()
	defer t.rwLock.Unlock()

	atomic.AddInt64(&t.stats.WriteCount, 1)

	// 写 WAL
	if t.walEnabled {
		entry := wal.NewWALEntry(wal.WALTypeUpdate, 0, key, value, wal.LSNInvalid)
		if _, err := t.wal.Append(entry); err != nil {
			return fmt.Errorf("failed to append WAL: %w", err)
		}
		atomic.AddInt64(&t.stats.WALAppends, 1)
	}

	return t.updateLocked(key, value, true)
}

// updateLocked 更新键值（内部，已持有锁）
func (t *BfTree) updateLocked(key, value []byte, writeWAL bool) error {
	// 空树：键不存在
	if t.rootPageID == 0 {
		return ErrKeyNotFound
	}

	// 查找叶子页面
	leafPageID, err := t.findLeafPage(t.rootPageID, key)
	if err != nil {
		return err
	}

	// 获取叶子节点
	leafNode, err := t.pageStore.getLeaf(leafPageID)
	if err != nil {
		return err
	}

	// 检查键是否存在
	_, found := leafNode.Get(key)
	if !found {
		return ErrKeyNotFound
	}

	// 更新键值（使用 Set，LeafNode 内部会处理为 Update）
	return leafNode.Set(key, value)
}

// Delete 删除键值（同步）
func (t *BfTree) Delete(ctx context.Context, key []byte) error {
	if t.closed.Load() {
		return ErrTreeClosed
	}

	t.rwLock.Lock()
	defer t.rwLock.Unlock()

	atomic.AddInt64(&t.stats.DeleteCount, 1)

	// 写 WAL
	if t.walEnabled {
		entry := wal.NewWALEntry(wal.WALTypeDelete, 0, key, nil, wal.LSNInvalid)
		if _, err := t.wal.Append(entry); err != nil {
			return fmt.Errorf("failed to append WAL: %w", err)
		}
		atomic.AddInt64(&t.stats.WALAppends, 1)
	}

	return t.deleteLocked(key, true)
}

// deleteLocked 删除键值（内部，已持有锁）
func (t *BfTree) deleteLocked(key []byte, writeWAL bool) error {
	// 空树：键不存在
	if t.rootPageID == 0 {
		return ErrKeyNotFound
	}

	// 查找叶子页面
	leafPageID, err := t.findLeafPage(t.rootPageID, key)
	if err != nil {
		return err
	}

	// 获取叶子节点
	leafNode, err := t.pageStore.getLeaf(leafPageID)
	if err != nil {
		return err
	}

	// 删除键值
	if err := leafNode.Delete(key); err != nil {
		return err
	}

	// Phase 2.3: 删除后立即检查是否需要合并兄弟节点
	// 设计决策 1：Delete 后立即检查
	if err := t.tryMergeAfterDelete(leafPageID); err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}

	return nil
}

// GetStats 获取统计信息
func (t *BfTree) GetStats() BfTreeStats {
	t.rwLock.RLock()
	defer t.rwLock.RUnlock()

	stats := t.stats

	// 更新页面统计
	pageStats := t.pageTable.GetStats()
	stats.TotalPages = pageStats.CurrentCount

	return stats
}

// Sync 刷盘，确保持久化
//
// 如果启用了 WAL，将 WAL 数据刷到磁盘
// 如果未启用 WAL，直接返回成功（MVP：数据仅在内存中）
func (t *BfTree) Sync() error {
	if t.closed.Load() {
		return ErrTreeClosed
	}

	// 如果启用了 WAL，同步 WAL
	if t.wal != nil && t.walEnabled {
		t.rwLock.RLock()
		defer t.rwLock.RUnlock()

		if err := t.wal.Sync(); err != nil {
			return fmt.Errorf("failed to sync wal: %w", err)
		}

		// 更新统计
		atomic.AddInt64(&t.stats.WALSyncCount, 1)
		return nil
	}

	// MVP: 未启用 WAL，直接返回成功
	return nil
}

// Close 关闭 Bf-Tree
func (t *BfTree) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return ErrTreeClosed
	}

	t.rwLock.Lock()
	defer t.rwLock.Unlock()

	// 关闭 WAL
	if t.wal != nil {
		if err := t.wal.Close(); err != nil {
			return fmt.Errorf("failed to close WAL: %w", err)
		}
	}

	return nil
}

// Lock helper methods for BitmapLock integration
// 这些方法根据配置自动选择使用 RWMutex 或 BitmapLock

// lockPage 锁定指定页面（写锁）
// 如果启用 BitmapLock，使用细粒度锁；否则使用全局 RWMutex
func (t *BfTree) lockPage(pageID uint64) {
	if t.useBitmapLock && t.bitmapLock != nil {
		t.bitmapLock.Lock(pageID)
	} else {
		t.rwLock.Lock()
	}
}

// unlockPage 解锁指定页面（写锁）
func (t *BfTree) unlockPage(pageID uint64) {
	if t.useBitmapLock && t.bitmapLock != nil {
		t.bitmapLock.Unlock(pageID)
	} else {
		t.rwLock.Unlock()
	}
}

// rlockPage 锁定指定页面（读锁）
func (t *BfTree) rlockPage(pageID uint64) {
	if t.useBitmapLock && t.bitmapLock != nil {
		t.bitmapLock.RLock(pageID)
	} else {
		t.rwLock.RLock()
	}
}

// runlockPage 解锁指定页面（读锁）
func (t *BfTree) runlockPage(pageID uint64) {
	if t.useBitmapLock && t.bitmapLock != nil {
		t.bitmapLock.RUnlock(pageID)
	} else {
		t.rwLock.RUnlock()
	}
}
