// Package service 定义领域服务接口
package service

import (
	"context"
	"time"
)

// ==========================================
// OperationStatus 操作状态定义
// ==========================================

// OperationStatus 操作状态
type OperationStatus int

const (
	// StatusPending 操作待执行
	StatusPending OperationStatus = iota
	// StatusRunning 操作正在执行
	StatusRunning
	// StatusCompleted 操作成功完成
	StatusCompleted
	// StatusFailed 操作失败
	StatusFailed
	// StatusCanceled 操作被取消
	StatusCanceled
	// StatusDiscarded 操作结果被丢弃
	StatusDiscarded
	// StatusTimeout 操作超时
	StatusTimeout
)

// IsTerminal 检查是否为终态
func (s OperationStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCanceled, StatusDiscarded, StatusTimeout:
		return true
	default:
		return false
	}
}

// String 返回状态字符串表示
func (s OperationStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCanceled:
		return "canceled"
	case StatusDiscarded:
		return "discarded"
	case StatusTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// ==========================================
// 异步操作接口
// ==========================================

// AsyncOp[T] 异步操作接口
// 封装异步计算的结果和状态
type AsyncOp[T any] interface {
	// Await 阻塞等待结果
	Await(ctx context.Context) (T, error)

	// OnComplete 注册完成回调，返回回调 ID 用于注销
	OnComplete(callback func(T, error)) string

	// OnError 注册错误回调，返回回调 ID 用于注销
	OnError(callback func(error)) string

	// OnSuccess 注册成功回调，返回回调 ID 用于注销
	OnSuccess(callback func(T)) string

	// OffComplete 注销完成回调
	OffComplete(cbID string) error

	// WithTimeout 设置超时（P2-2: 链式超时设置）
	WithTimeout(timeout time.Duration) AsyncOp[T]

	// IsDone 检查是否完成
	IsDone() bool

	// IsSuccess 检查是否成功
	IsSuccess() bool

	// IsFailed 检查是否失败
	IsFailed() bool

	// IsCanceled 检查是否取消
	IsCanceled() bool
}
