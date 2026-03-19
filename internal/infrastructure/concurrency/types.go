// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import "github.com/jzhang405/NexKV/internal/domain/model"

// ==========================================
// TaskStatus 调度队列视角的任务状态
// ==========================================

// TaskStatus 调度队列视角的任务状态
// 区别于 OperationStatus（BaseTask 的异步执行状态）
// TaskStatus 控制队列中元素的 Dequeue 时机
type TaskStatus int

const (
	// TaskQueued 任务已入队，等待执行
	TaskQueued TaskStatus = iota
	// TaskExecuting 正在执行（Peek 成功后）
	TaskExecuting
	// TaskPassed 执行成功，需要 Dequeue
	TaskPassed
	// TaskFailed 执行失败，需要 Dequeue
	TaskFailed
	// TaskRetrying 需要重试，保留在队列
	TaskRetrying
	// TaskTimeout 超时，需要 Dequeue
	TaskTimeout
)

// String 返回状态字符串
func (s TaskStatus) String() string {
	switch s {
	case TaskQueued:
		return "queued"
	case TaskExecuting:
		return "executing"
	case TaskPassed:
		return "passed"
	case TaskFailed:
		return "failed"
	case TaskRetrying:
		return "retrying"
	case TaskTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

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
