// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import "github.com/jzhang405/NexKV/internal/domain/model"

// ==========================================
// ExecutionOrder 执行顺序常量
// ==========================================

// ExecutionOrder 任务执行顺序
// TaskScheduler 按此顺序从小到大处理任务
// 原则：依赖关系决定执行顺序（例如：WAL → BTree → Compaction）
type ExecutionOrder int

const (
	// ExecutionOrderWALAppend WAL 追加（最高优先级，数据持久化）
	ExecutionOrderWALAppend ExecutionOrder = 1

	// ExecutionOrderBTreeSet BTree 更新（基于持久化数据更新内存索引）
	ExecutionOrderBTreeSet ExecutionOrder = 2

	// ExecutionOrderCompaction 压缩整理（后台任务，最低优先级）
	ExecutionOrderCompaction ExecutionOrder = 3

	// ExecutionOrderCustom 自定义任务起始值
	// 用户任务应使用 >= 100 的值，避免与预定义任务冲突
	ExecutionOrderCustom ExecutionOrder = 100
)

// Int 返回整数值（用于与 int 类型兼容）
func (o ExecutionOrder) Int() int {
	return int(o)
}

// ==========================================
// TaskStatus 别名（使用 model.TaskStatus）
// ==========================================

// TaskStatus 调度队列视角的任务状态
// 别名指向 model.TaskStatus，保持统一
type TaskStatus = model.TaskStatus

// 重新导出 TaskStatus 常量
const (
	TaskQueued    = model.TaskQueued
	TaskExecuting = model.TaskExecuting
	TaskPassed    = model.TaskPassed
	TaskFailed    = model.TaskFailed
	TaskTimeout   = model.TaskTimeout
	TaskBusy      = model.TaskBusy
	TaskRetrying  = model.TaskRetrying
	TaskCompleted = model.TaskCompleted
)

// ==========================================
// TaskQueueHandler 队列处理器接口
// ==========================================

// TaskQueueHandler 队列处理器接口（支持 Peek + Execute 两阶段执行）
type TaskQueueHandler interface {
	// 队列管理
	QueueLen() int          // 获取队列长度
	Enqueue(item any) error // 客户端入队任务
	Peek(item *any) bool    // 查看队首元素（不出队）
	Dequeue(item *any) bool // 移除队首元素（出队）

	// 任务执行
	Execute(item any) TaskStatus // 执行任务处理逻辑，返回执行状态

	// 元数据
	Name() string                  // 任务名称
	Priority() model.TaskPriority  // 优先级（发给 Executor 的参数）
	ExecutionOrder() int           // 执行顺序（TaskScheduler 内部排序，从小到大）
	GetTask() *model.BaseTask[any] // 获取异步结果任务（复用现有）
}
