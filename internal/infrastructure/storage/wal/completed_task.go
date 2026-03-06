// Package wal 提供立即完成的任务实现（MVP 简化）
package wal

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ============================================
// 立即完成的任务实现（MVP 简化）
// ============================================

// completedWALTask 已完成的 WAL 任务
type completedWALTask struct {
	result LSN
	err    error
	done   chan struct{}
}

// NewCompletedWALTask 创建已完成的 WAL 任务
func NewCompletedWALTask(fn func() (LSN, error)) model.Task[LSN] {
	// 立即执行函数获取结果
	result, err := fn()

	// 创建已完成的任务
	task := &completedWALTask{
		result: result,
		err:    err,
		done:   make(chan struct{}),
	}
	close(task.done) // 立即关闭 done channel
	return task
}

// Run 实现 TaskRunner 接口（已完成任务无需运行）
func (t *completedWALTask) Run(ctx context.Context, pipeline model.PipelineContext) {
	// 已完成的任务，无需运行
}

// Execute 实现 Task 接口（直接返回结果）
func (t *completedWALTask) Execute(ctx context.Context, pipeline model.PipelineContext) (LSN, error) {
	return t.result, t.err
}

// Wait 实现 Task 接口（立即返回）
func (t *completedWALTask) Wait(ctx context.Context) (LSN, error) {
	return t.result, t.err
}

// IsDone 实现 Task 接口（始终返回 true）
func (t *completedWALTask) IsDone() bool {
	return true
}

// Done 实现 Task 接口（返回已关闭的 channel）
func (t *completedWALTask) Done() <-chan struct{} {
	return t.done
}

// Priority 实现 TaskRunner 接口
func (t *completedWALTask) Priority() model.TaskPriority {
	return model.TaskPriorityNormal
}

// SourceID 实现 TaskRunner 接口
func (t *completedWALTask) SourceID() model.SourceID {
	return model.MustParseSourceID("wal:disk:append")
}

// completedTruncateTask 已完成的 Truncate 任务
type completedTruncateTask struct {
	result struct{}
	err    error
	done   chan struct{}
}

// NewCompletedTruncateTask 创建已完成的 Truncate 任务
func NewCompletedTruncateTask(fn func() (struct{}, error)) model.Task[struct{}] {
	// 立即执行函数获取结果
	result, err := fn()

	// 创建已完成的任务
	task := &completedTruncateTask{
		result: result,
		err:    err,
		done:   make(chan struct{}),
	}
	close(task.done) // 立即关闭 done channel
	return task
}

// Run 实现 TaskRunner 接口（已完成任务无需运行）
func (t *completedTruncateTask) Run(ctx context.Context, pipeline model.PipelineContext) {
	// 已完成的任务，无需运行
}

// Execute 实现 Task 接口（直接返回结果）
func (t *completedTruncateTask) Execute(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
	return t.result, t.err
}

// Wait 实现 Task 接口（立即返回）
func (t *completedTruncateTask) Wait(ctx context.Context) (struct{}, error) {
	return t.result, t.err
}

// IsDone 实现 Task 接口（始终返回 true）
func (t *completedTruncateTask) IsDone() bool {
	return true
}

// Done 实现 Task 接口（返回已关闭的 channel）
func (t *completedTruncateTask) Done() <-chan struct{} {
	return t.done
}

// Priority 实现 TaskRunner 接口
func (t *completedTruncateTask) Priority() model.TaskPriority {
	return model.TaskPriorityNormal
}

// SourceID 实现 TaskRunner 接口
func (t *completedTruncateTask) SourceID() model.SourceID {
	return model.MustParseSourceID("wal:disk:truncate")
}
