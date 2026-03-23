// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// BTreeSetItem 实现 ShardItem 接口
// 用于将 BTree Set 操作提交到 TaskScheduler 队列
// 通过嵌入 BaseTask[struct{}] 实现接口组合（TaskRunner + TaskResult）
//
// 设计说明：
// - 存储提交时查找的 leafRef，供 processBatch 复用（零查找开销）
// - ShardID 在构造时计算（基于提交时的 PageID + 1）
// - TaskOrder 在构造时传入（用于直接数组索引，替代 map 查找）
// - 执行时验证 PageInfo 未变更，否则回退到完整查找
type BTreeSetItem struct {
	*model.BaseTask[struct{}]

	btree      *BTree
	key        []byte
	value      []byte
	maxRetries int
	attempts   int64
	shardID    int      // 基于 PageID + 1
	leafRef    *PageRef // 缓存提交时查找的 leaf（用于批量处理）
	taskOrder  int      // executionOrder，用于直接数组索引
}

// NewBTreeSetItem 创建新的 BTree Set 任务项
//
// 参数：
//   - bt: BTree 实例
//   - key: 要设置的键
//   - value: 要设置的值
//   - maxRetries: 最大重试次数（0 表示不重试）
//   - shardID: 分片 ID（由调用方提前计算）
//   - leafRef: 缓存的叶子节点引用（由调用方提供）
//   - taskOrder: 任务执行顺序（由调用方提供，用于直接数组索引）
//
// 返回：
//   - *BTreeSetItem: 创建的任务项
//
// P0 + P1 + P2 优化：
// - P0: ShardID 计算移到调用方（SetWithTask）
// - P1: leafRef 由调用方提供，Execute 时使用 setWithLeafLockAndRef 避免双重查找
// - P2: taskOrder 由调用方提供，EnqueueWithShard 使用数组索引替代 map 查找
func NewBTreeSetItem(
	bt *BTree,
	key, value []byte,
	maxRetries int,
	shardID int, // 由调用方提前计算
	leafRef *PageRef, // 由调用方提供缓存
	taskOrder int, // 由调用方提供，用于直接数组索引
) *BTreeSetItem {
	return &BTreeSetItem{
		btree:      bt,
		key:        key,
		value:      value,
		maxRetries: maxRetries,
		attempts:   0,
		shardID:    shardID,
		leafRef:    leafRef,   // 使用调用方提供的缓存
		taskOrder:  taskOrder, // 使用调用方提供的 executionOrder
		BaseTask: model.NewBaseTask(
			model.TaskPriorityNormal,
			maxRetries,
			func(ctx context.Context, trCtx model.TaskRunnerContext) (struct{}, error) {
				// P1 优化：使用缓存的 leafRef 避免双重路径查找
				// setWithLeafLockAndRef 会验证 PageInfo 未变更，失效时自动回退
				err := bt.setWithLeafLockAndRef(ctx, leafRef, key, value)
				if err != nil {
					return struct{}{}, fmt.Errorf("btree set with leaf ref failed: %w", err)
				}
				return struct{}{}, nil
			},
		),
	}
}

// ShardID 返回分片 ID
// 基于 PageID + 1，TaskScheduler 使用此值进行取模路由
func (item *BTreeSetItem) ShardID() int {
	return item.shardID
}

// MaxRetries 返回最大重试次数
func (item *BTreeSetItem) MaxRetries() int {
	if item.maxRetries > 0 {
		return item.maxRetries
	}
	return 3 // 默认重试 3 次
}

// IncAttempts 增加尝试次数并返回当前次数
func (item *BTreeSetItem) IncAttempts() int {
	return int(atomic.AddInt64(&item.attempts, 1))
}

// GetAttempts 返回当前尝试次数（不增加计数）
func (item *BTreeSetItem) GetAttempts() int {
	return int(atomic.LoadInt64(&item.attempts))
}

// GetKey 返回任务关联的键（用于调试和日志）
func (item *BTreeSetItem) GetKey() []byte {
	return item.key
}

// GetValue 返回任务关联的值（用于调试和日志）
func (item *BTreeSetItem) GetValue() []byte {
	return item.value
}

// ===== BatchShardItem 接口实现 =====

// BatchType 返回批量类型标识
func (item *BTreeSetItem) BatchType() string {
	return "btree-set"
}

// PreferredBatchSize 返回建议的批量大小
// 随机前缀 key 场景下最优值：8
// 均匀 key 场景下最优值：32
// 当前配置：8（针对分散 key 优化）
func (item *BTreeSetItem) PreferredBatchSize() int {
	const btreeSetBatchSize = 8
	return btreeSetBatchSize
}

// TaskOrder 返回任务执行顺序（executionOrder）
// 用于 EnqueueWithShard 直接数组索引访问，替代 map 查找
func (item *BTreeSetItem) TaskOrder() int {
	return item.taskOrder
}
