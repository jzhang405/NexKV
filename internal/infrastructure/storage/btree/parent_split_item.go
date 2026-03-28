// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// ParentSplitItem 实现异步父节点分裂任务
// 基于 Lealone 的 asyncSplitPage() 设计，使用 TaskScheduler 在后台处理父节点分裂
type ParentSplitItem struct {
	*model.BaseTask[struct{}]

	btree        *BTree
	parentPageID model.PageID // 需要分裂的父节点
	leftChildID  uint32       // 分裂后的左子节点
	rightChildID uint32       // 分裂后的右子节点
	splitKey     []byte       // 分裂键
	path         []*PageInfo  // 从 Root 到父节点的路径（PageInfo 切片）
	shardID      int
	taskOrder    int
}

// NewParentSplitItem 创建父节点分裂任务
//
// 参数:
//   - bt: BTree 实例
//   - parentPageID: 需要分裂的父节点 ID
//   - leftChildID, rightChildID: 刚分裂出的左右子节点
//   - splitKey: 分裂键（要插入到祖父节点的键）
//   - path: 从 Root 到父节点的完整路径（PageInfo 切片，用于递归分裂）
//   - shardID: 分片 ID（用于 TaskScheduler 路由）
//   - taskOrder: 任务执行顺序（用于 TaskScheduler 索引）
func NewParentSplitItem(
	bt *BTree,
	parentPageID model.PageID,
	leftChildID, rightChildID uint32,
	splitKey []byte,
	path []*PageInfo,
	shardID, taskOrder int,
) *ParentSplitItem {
	return &ParentSplitItem{
		btree:        bt,
		parentPageID: parentPageID,
		leftChildID:  leftChildID,
		rightChildID: rightChildID,
		splitKey:     splitKey,
		path:         path,
		shardID:      shardID,
		taskOrder:    taskOrder,
		BaseTask: model.NewBaseTask(
			model.TaskPriorityHigh, // 父节点分裂优先级高
			3,                      // 最大重试 3 次
			func(ctx context.Context, trCtx model.TaskRunnerContext) (struct{}, error) {
				// Execute 逻辑：在这里实现父节点分裂

				// 执行父节点分裂（完整流程）
				// 注意：这是在独立的 goroutine 中执行
				err := bt.splitInternalOffHeapSyncRecursive(
					parentPageID,
					leftChildID,
					rightChildID,
					splitKey,
					path,
				)

				if err != nil {
					return struct{}{}, errpkg.BTreeAsyncParentSplitFailed(int(parentPageID), err)
				}

				return struct{}{}, nil
			},
		),
	}
}

// ShardID 返回分片 ID，用于 TaskScheduler 路由
// 基于 parentPageID + 1，确保同一页面的分裂任务路由到同一个 Worker
func (item *ParentSplitItem) ShardID() int {
	return item.shardID
}

// TaskOrder 返回任务执行顺序，用于 TaskScheduler 索引
// 固定为 1（btree-split 的数组索引）
func (item *ParentSplitItem) TaskOrder() int {
	return item.taskOrder
}
