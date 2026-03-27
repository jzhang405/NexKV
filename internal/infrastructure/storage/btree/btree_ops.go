// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ===== 已实现的辅助函数 =====

// GetDepth 返回当前树的深度
// 深度定义为从根节点到叶子节点的最长路径长度
func (b *BTree) GetDepth() int {
	rootInfo := b.rootRef.GetRootPageInfo()
	if rootInfo == nil {
		return 0
	}

	// 从根节点开始，向下遍历到叶子节点
	depth := 0
	currentPage := rootInfo.GetPage()

	for currentPage != nil {
		if leaf, ok := currentPage.(*LeafPage); ok && leaf != nil {
			// 到达叶子节点
			break
		}

		if internal, ok := currentPage.(*InternalPage); ok && internal != nil {
			// 内部节点，继续向下
			if len(internal.children) > 0 {
				// 获取第一个子节点
				childRef := internal.children[0]
				if childRef != nil {
					childInfo := childRef.pInfo.Load()
					if childInfo != nil {
						currentPage = childInfo.GetPage()
						depth++
						continue
					}
				}
			}
		}

		break
	}

	return depth + 1 // +1 因为 depth 从 0 开始计数
}

// GetStats 返回 BTree 的统计信息
func (b *BTree) GetStats() *BTreeStats {
	rootInfo := b.rootRef.GetRootPageInfo()
	if rootInfo == nil {
		return &BTreeStats{
			Depth:     0,
			MaxLevels: b.maxLevels,
			RootSize:  0,
			MaxKeys:   model.DefaultMaxKeys,
			MinKeys:   model.DefaultMinKeys,
		}
	}

	rootPage := rootInfo.GetPage()
	rootSize := 0
	if rootPage != nil {
		if leaf, ok := rootPage.(*LeafPage); ok {
			rootSize = leaf.NumKeys()
		} else if internal, ok := rootPage.(*InternalPage); ok {
			rootSize = internal.NumKeys()
		}
	}

	return &BTreeStats{
		Depth:     b.GetDepth(),
		MaxLevels: b.maxLevels,
		RootSize:  rootSize,
		MaxKeys:   model.DefaultMaxKeys,
		MinKeys:   model.DefaultMinKeys,
		// 统计功能
		TotalNodes: 0,
		TotalKeys:  0,
	}
}

// GetMaxLevels returns the maximum number of levels in the tree.
func (b *BTree) GetMaxLevels() int {
	return b.maxLevels
}

// SetMaxLevels sets the maximum number of levels in the tree.
func (b *BTree) SetMaxLevels(levels int) {
	b.maxLevels = levels
}

// BTreeStats holds BTree statistics.
type BTreeStats struct {
	Depth      int
	MaxLevels  int
	RootSize   int
	MaxKeys    int
	MinKeys    int
	TotalNodes int
	TotalKeys  int
}

// String returns a string representation of the stats.
func (s *BTreeStats) String() string {
	return fmt.Sprintf("Depth: %d/%d, RootSize: %d/%d",
		s.Depth, s.MaxLevels, s.RootSize, s.MaxKeys)
}

// ===== 已删除的废弃函数 =====
//
// 以下函数已被新 API 替代，已删除：
// - InsertWithSplit() → 使用 b.Set() 替代
// - DeleteWithMerge() → 使用 b.Delete() 替代
//
// 这些函数在 Week 14 重构时被移除，以简化代码库。

// ===== TaskScheduler 集成 =====

// TaskScheduler 定义 TaskScheduler 接口，避免循环依赖
// TaskScheduler 提供按 ShardID 路由任务的能力
type TaskScheduler interface {
	// EnqueueWithShard 根据 ShardID 分发任务到对应核心
	EnqueueWithShard(item any, taskName string) error
}

// SetWithTask 提交 Set 操作到 TaskScheduler 队列
//
// 此方法用于高并发场景下，当快速路径（3 次同步重试）失败后，
// 将操作提交到 TaskScheduler 队列，利用 CPU 核心亲和性减少跨核心锁竞争
//
// 参数：
//   - ctx: 上下文
//   - scheduler: TaskScheduler 实例
//   - key: 要设置的键
//   - value: 要设置的值
//
// 返回：
//   - error: 操作失败时返回错误
func (b *BTree) SetWithTask(
	ctx context.Context,
	scheduler TaskScheduler,
	key, value []byte,
) error {
	if b.closed {
		return ErrClosed
	}

	// P0 + P1 优化：只在慢速路径时查找 leafRef（快速路径成功时不会执行到这里）
	// 计算 ShardID：基于 key 所在的 PageID
	shardID := 0 // 默认值
	var leafRef *PageRef

	leafRef, _, err := b.findLeafPageRef(ctx, key)
	if err == nil && leafRef != nil {
		pageInfo := leafRef.GetPageInfo()
		if pageInfo != nil {
			// ShardID = PageID + 1（避免 0 值，0 保留给动态负载均衡）
			shardID = int(pageInfo.GetPageID()) + 1
		}
	}

	// 创建任务项（传入提前计算的 shardID 和缓存的 leafRef）
	// Execute 时使用 setWithLeafLockAndRef 避免双重路径查找
	// P2 优化：btree-set 的 taskOrder 固定为 0（假设是第一个注册的 task）
	const btreeSetTaskOrder = 0
	item := NewBTreeSetItem(b, key, value, 3, shardID, leafRef, btreeSetTaskOrder)

	// 提交到调度器
	if err := scheduler.EnqueueWithShard(item, "btree-set"); err != nil {
		return err
	}

	// ✅ Phase 1 修复：同步等待任务完成，避免假成功
	// EnqueueWithShard 只是异步入队，后台任务可能失败
	// 必须等待任务执行完成并检查结果
	result, err := item.Wait(ctx)
	if err != nil {
		return fmt.Errorf("task execution failed: %w", err)
	}

	_ = result // result 是 struct{}，无需使用
	return nil
}

// SetWithRetryAndQueue 实现 Set 操作的完整重试策略
//
// 快速路径：3 次同步重试（路径搜索 + TryLock）
// - 大多数情况下 1-2 次重试即可成功
// - 每次重试需要重新搜索路径（~500 ns）
//
// 慢速路径：3 次失败后提交到 TaskScheduler 队列
// - 利用 CPU 核心亲和性减少跨核心锁竞争
// - 适合高并发热点 key 场景
//
// 参数：
//   - ctx: 上下文
//   - scheduler: TaskScheduler 实例（如果为 nil，3 次失败后返回 ErrRetry）
//   - key: 要设置的键
//   - value: 要设置的值
//
// 返回：
//   - error: 操作成功返回 nil，失败返回错误
func (b *BTree) SetWithRetryAndQueue(
	ctx context.Context,
	scheduler TaskScheduler,
	key, value []byte,
) error {
	if b.closed {
		return ErrClosed
	}

	// 快速路径：3 次重试
	// 性能测试对比（8 线程，3轮平均）：
	//   - 1 次重试：~1.638M ops/sec（基准）
	//   - 2 次重试：~1.627M ops/sec（-0.7%）
	//   - 3 次重试：~1.664M ops/sec（+1.6%）✓ 略优
	//   - 5 次重试：~1.617M ops/sec（-1.3%）
	// 结论：差异很小（<3%），使用 3 次作为平衡点
	const maxFastRetries = 3

	for attempt := range maxFastRetries {
		// 检查上下文取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := b.setWithLeafLock(ctx, key, value)
		switch err {
		case nil:
			// ✅ 只有真正成功（不重试）才推进 epoch
			// ErrRetry 意味着操作未完成（例如 pageID 变化需要重试）
			// 过早推进 epoch 会导致页面被释放，其他 goroutine 可能访问已释放的页面
			b.epochBasedFreeList.AdvanceEpoch(b.offheapPM)
			return nil // 成功
		case ErrRetry:
			// ✅ ErrRetry：数据可能未真正写入，不要推进 epoch
			// 例如：UpdateLeafEntry 成功但 pageID 变化，需要重试
			// 如果此时推进 epoch，可能导致旧页面被释放，其他 goroutine 访问失败
			if attempt < maxFastRetries-1 {
				runtime.Gosched()
			}
		default:
			// 检查是否为可重试错误（如循环引用）
			if errors.Is(err, ErrCircularReference) {
				if attempt < maxFastRetries-1 {
					runtime.Gosched()
				}
				break // 继续重试
			}
			return err // 其他错误直接返回
		}
	}

	// 慢速路径：3 次重试均失败，提交到 TaskScheduler 队列
	if scheduler != nil {
		return b.SetWithTask(ctx, scheduler, key, value)
	}

	// 没有 TaskScheduler，返回 ErrRetry
	return ErrRetry
}

// ===== 批量处理支持 =====

// setWithLeafLockAndRef 使用已知的 leafRef 执行 Set 操作
//
// 此方法用于批量处理场景，使用 BTreeSetItem 创建时缓存的 leafRef，
// 避免重复查找路径，实现零查找开销。
//
// **重要**：此方法使用与 setWithLeafLock 相同的 Leaf-Level Locking 模式：
// - Copy-on-Write（使用 Delta Chain）
// - Leaf-Level CAS（原子替换叶子节点）
// - 正确处理分裂
//
// 并发安全保证：
// 1. 使用缓存的 leafRef 前验证 PageInfo 未变更
// 2. PageInfo 变更时自动回退到完整查找路径
// 3. TryLock 失败时返回 ErrRetry
//
// 参数：
//   - ctx: 上下文
//   - leafRef: 缓存的叶子节点引用
//   - key: 要设置的键
//   - value: 要设置的值
//
// 返回：
//   - error: 操作成功返回 nil，失败返回错误
func (b *BTree) setWithLeafLockAndRef(
	ctx context.Context,
	leafRef *PageRef,
	key, value []byte,
) error {
	if leafRef == nil {
		// 缓存无效，回退到完整查找
		return b.setWithLeafLock(ctx, key, value)
	}

	// 获取页面锁
	pageLock := leafRef.GetLock()
	if pageLock == nil {
		return ErrRetry
	}

	// 使用 TryLock 快速失败（避免死锁）
	if !pageLock.TryLock() {
		return ErrRetry
	}
	defer pageLock.Unlock()

	// 获取当前 PageInfo（在锁保护下）
	oldInfo := leafRef.pInfo.Load()
	if oldInfo == nil {
		return ErrRetry
	}

	// 验证并获取叶子节点
	oldPage := oldInfo.GetPage()
	if oldPage == nil {
		return ErrRetry
	}

	leafPage, ok := oldPage.(*LeafPage)
	if !ok {
		// 页面类型错误，回退到完整查找
		return b.setWithLeafLock(ctx, key, value)
	}

	// 再次验证 PageInfo 未变更（在获取锁期间）
	currentInfo := leafRef.pInfo.Load()
	if currentInfo != oldInfo {
		// PageInfo 已变更，路径可能失效，回退到完整查找
		return b.setWithLeafLock(ctx, key, value)
	}

	// 使用 Delta Chain 克隆（写时复制优化）
	newLeafPage := leafPage.CloneWithDelta()
	if newLeafPage == nil {
		return fmt.Errorf("clone leaf page failed")
	}

	// 在克隆的叶节点上插入键值对
	_, err := newLeafPage.Insert(key, value)
	if err != nil {
		return fmt.Errorf("insert into leaf: %w", err)
	}

	// 创建新的 PageInfo
	newInfo := NewPageInfo()
	newInfo.SetPage(newLeafPage)
	// 继承其他属性
	newInfo.SetPos(oldInfo.GetPos())
	if oldInfo.IsDirty() {
		newInfo.MarkDirty()
	}

	// Leaf-Level CAS（在锁保护下，几乎不会失败）
	if !leafRef.ReplacePage(oldInfo, newInfo) {
		// CAS 失败（极少发生），返回重试
		return ErrRetry
	}

	// 检查是否需要分裂（同步，在锁保护下）
	if newLeafPage.NumKeys() > splitThreshold {
		// 需要分裂，由于没有完整路径信息，回退到完整查找
		// 注意：这种情况极少发生（<1%），对性能影响有限
		return b.setWithLeafLock(ctx, key, value)
	}

	// 持久化集成（仅持久化模式）
	if b.chunkMgr != nil {
		// 获取全局写锁，防止并发修改干扰持久化
		b.writeMu.Lock()
		defer b.writeMu.Unlock()

		// 获取当前 Root（CAS 后可能已改变）
		currentRoot := b.rootRef.pInfo.Load()
		if currentRoot == nil {
			return fmt.Errorf("root page info is nil after persist")
		}

		// 构建持久化路径：从 Root 到 Leaf 的完整路径
		persistPath := b.buildPersistPath(currentRoot, newInfo)

		// 深拷贝路径（确保数据独立）
		if err := b.finalizeDeepClone(persistPath); err != nil {
			return fmt.Errorf("finalize deep clone: %w", err)
		}

		// 持久化根节点（会递归持久化整个树）
		if err := b.persistRoot(currentRoot); err != nil {
			return fmt.Errorf("persist root: %w", err)
		}
	}

	return nil
}

// processBatch 批量处理多个 BTreeSetItem
//
// 优化策略：
// 1. 使用缓存的 leafRef，零查找开销
// 2. 按 PageID 分组处理，提高局部性
// 3. PageInfo 变更时自动回退到完整查找
//
// 参数：
//   - ctx: 上下文
//   - items: 要批量处理的任务项
//
// 返回：
//   - []error: 每个任务项的执行结果
func (b *BTree) processBatch(ctx context.Context, items []*BTreeSetItem) []error {
	results := make([]error, len(items))

	// 按 PageID 分组（使用缓存的 ShardID 反推）
	type groupedItems struct {
		pageID  int
		indices []int // 记录原始索引
	}
	groups := make(map[int]*groupedItems)

	// 第一阶段：分组（记录原始索引）
	for idx, item := range items {
		pageID := item.shardID - 1 // ShardID = PageID + 1
		if groups[pageID] == nil {
			groups[pageID] = &groupedItems{pageID: pageID}
		}
		groups[pageID].indices = append(groups[pageID].indices, idx)
	}

	// 第二阶段：按 PageID 分组批量处理
	for _, group := range groups {
		for _, idx := range group.indices {
			item := items[idx]
			// 使用缓存的 leafRef，完全不需要查找！
			err := b.setWithLeafLockAndRef(ctx, item.leafRef, item.key, item.value)
			results[idx] = err
		}
	}

	return results
}
