// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package service

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// TaskRunnerContext 任务执行上下文
// ==========================================

// TaskRunnerContext 任务执行上下文接口
//
// 提供 TaskRunner 的执行环境和任务提交入口。
// 用于将任务提交到执行器，并管理执行上下文的生命周期。
type TaskRunnerContext interface {
	// Submit 提交任务到执行器
	Submit(task model.TaskRunner) error

	// Executor 获取执行器引用
	Executor() model.TaskExecutorRef

	// Context 获取执行上下文
	Context() context.Context

	// Cancel 取消执行上下文
	// 立即取消，不等待正在执行的任务
	Cancel()

	// GracefulShutdown 优雅关闭执行上下文
	// 1. 停止接收新任务
	// 2. 等待正在执行的任务完成
	// 3. 超时后强制取消
	GracefulShutdown(timeout time.Duration) error
}
