// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// 错误导出（便捷访问）
// ==========================================

var (
	// ErrPoolClosed 任务池已关闭
	ErrPoolClosed = errors.ErrPoolClosed
	// ErrPoolFull 任务池已满
	ErrPoolFull = errors.ErrPoolFull
	// ErrTaskArgLengthMismatch 任务和参数长度不匹配
	ErrTaskArgLengthMismatch = errors.ErrTaskArgLengthMismatch
	// ErrTaskCanceled 任务已取消
	ErrTaskCanceled = errors.ErrTaskCanceled
	// ErrTaskTimeout 任务超时
	ErrTaskTimeout = errors.ErrTaskTimeout
	// ErrTooManyDelayedTasks 延迟任务数已达上限
	ErrTooManyDelayedTasks = errors.ErrTooManyDelayedTasks
)
