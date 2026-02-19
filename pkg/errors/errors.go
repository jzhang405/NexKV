// Package errors 提供统一的错误定义
// 对齐 spike v18.0 sentinel error 模式
package errors

import (
	stderrors "errors"
	"fmt"
)

// ===========================
// 标准 Sentinel Errors（对齐 spike v18.0）
// ===========================

var (
	// 通用错误（对齐 spike v18.0）
	ErrCanceled        = stderrors.New("operation canceled")
	ErrTimeout         = stderrors.New("operation timeout")
	ErrCompleted       = stderrors.New("operation already completed")
	ErrAlreadyCanceled = stderrors.New("operation already canceled")
	ErrInvalidParam    = stderrors.New("invalid parameter")

	// Transport 层错误（新增）
	ErrTransportClosed  = stderrors.New("transport is closed")
	ErrAlreadyConnected = stderrors.New("already connected")
	ErrConnectionFailed = stderrors.New("connection failed")
	ErrNotConnected     = stderrors.New("not connected")
	ErrChannelClosed    = stderrors.New("channel is closed")
	ErrMessageTooLarge  = stderrors.New("message size exceeds limit")
	ErrInvalidMessage   = stderrors.New("invalid message format")
	ErrNodeNotFound     = stderrors.New("node not found")
	ErrPeerIDInvalid    = stderrors.New("invalid peer ID format")
	ErrAddrInvalid      = stderrors.New("invalid address format")
	ErrAddrTooLong      = stderrors.New("address too long")

	// 异步模块错误
	ErrAsyncExecFailed = stderrors.New("async operation failed")
	ErrCallbackPanic   = stderrors.New("callback panic recovered")
)

// ===========================
// 增强错误结构（携带上下文信息）
// ===========================

// NexError 增强错误（携带上下文信息）
type NexError struct {
	Err     error  // 原始 sentinel error（必须）
	Details string // 错误详情
}

// Error 实现 error 接口
func (e *NexError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s", e.Err.Error(), e.Details)
	}
	return e.Err.Error()
}

// Unwrap 支持错误链
func (e *NexError) Unwrap() error {
	return e.Err
}

// Is 支持 errors.Is() 比较（基于 sentinel error）
func (e *NexError) Is(target error) bool {
	return stderrors.Is(e.Err, target)
}

// ===========================
// 便捷包装函数（返回增强错误）
// ===========================

// Wrap 包装标准错误，携带详情
func Wrap(err error, details string) *NexError {
	return &NexError{
		Err:     err,
		Details: details,
	}
}

// Wrapf 包装标准错误，格式化详情
func Wrapf(err error, format string, args ...any) *NexError {
	return &NexError{
		Err:     err,
		Details: fmt.Sprintf(format, args...),
	}
}
