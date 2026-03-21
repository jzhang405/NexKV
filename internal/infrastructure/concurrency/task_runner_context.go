// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package concurrency

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// taskRunnerContext TaskRunnerContext 的具体实现
// ==========================================

// taskRunnerContext 任务执行上下文
// 实现 model.TaskRunnerContext 和 service.TaskRunnerContext 接口
type taskRunnerContext struct {
	ctx      context.Context
	cancel   context.CancelFunc
	executor service.TaskExecutor

	// 优雅关闭支持
	wg      sync.WaitGroup
	closed  bool
	closeMu sync.RWMutex
}

// NewTaskRunnerContext 创建新的任务执行上下文
func NewTaskRunnerContext(ctx context.Context, executor service.TaskExecutor) *taskRunnerContext {
	ctx, cancel := context.WithCancel(ctx)
	return &taskRunnerContext{
		ctx:      ctx,
		cancel:   cancel,
		executor: executor,
	}
}

// Submit 提交任务到执行器
// 实现 model.TaskRunnerContext 和 service.TaskRunnerContext 接口
func (t *taskRunnerContext) Submit(task model.TaskRunner) error {
	t.closeMu.RLock()
	defer t.closeMu.RUnlock()

	if t.closed {
		return service.ErrTaskRunnerClosed
	}

	t.wg.Add(1)
	return t.executor.Submit(t.ctx, task.SourceID(), task.Priority(), func(ctx context.Context) {
		defer t.wg.Done()
		task.Run(ctx, t)
	})
}

// Executor 获取执行器引用
func (t *taskRunnerContext) Executor() model.TaskExecutorRef {
	return t.executor
}

// Context 获取执行上下文
func (t *taskRunnerContext) Context() context.Context {
	return t.ctx
}

// Cancel 取消执行上下文
// 立即取消，不等待正在执行的任务
func (t *taskRunnerContext) Cancel() {
	if t.cancel != nil {
		t.cancel()
	}
}

// GracefulShutdown 优雅关闭执行上下文
// 1. 停止接收新任务
// 2. 等待正在执行的任务完成
// 3. 超时后强制取消
func (t *taskRunnerContext) GracefulShutdown(timeout time.Duration) error {
	// 1. 标记为已关闭，不再接收新任务
	t.closeMu.Lock()
	t.closed = true
	t.closeMu.Unlock()

	// 2. 等待正在执行的任务完成或超时
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 所有任务已完成
		return nil
	case <-time.After(timeout):
		// 超时，强制取消
		t.Cancel()
		return service.ErrTaskRunnerShutdownTimeout
	}
}
