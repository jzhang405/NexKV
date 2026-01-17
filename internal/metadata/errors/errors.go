// Package errors 提供统一的错误定义
package errors

import "fmt"

// ========================================
// 存储层错误
// ========================================

// ErrorCode 存储错误码
type ErrorCode int

const (
	// ErrCodeNotFound 键不存在
	ErrCodeNotFound ErrorCode = iota

	// ErrCodeAlreadyExists 键已存在
	ErrCodeAlreadyExists

	// ErrCodeVersionNotFound 版本不存在
	ErrCodeVersionNotFound

	// ErrCodeChecksum 校验和错误
	ErrCodeChecksum

	// ErrCodeClosed 存储已关闭
	ErrCodeClosed

	// ErrCodeInternal 内部错误
	ErrCodeInternal
)

// StoreError 存储错误
type StoreError struct {
	Code    ErrorCode
	Message string
	Err     error
}

// Error 实现 error 接口
func (e *StoreError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap 返回底层错误
func (e *StoreError) Unwrap() error {
	return e.Err
}

// ========================================
// 传输层错误
// ========================================

// TransportError 传输层错误
type TransportError struct {
	Op      string // 操作类型 (Send、Receive、Accept 等)
	Addr    string // 目标地址
	Err     error  // 底层错误
	tempErr bool   // 是否为临时错误（可重试）
}

// Error 实现 error 接口
func (e *TransportError) Error() string {
	if e.Addr != "" {
		return e.Op + " " + e.Addr + ": " + e.Err.Error()
	}
	return e.Op + ": " + e.Err.Error()
}

// Unwrap 返回底层错误
func (e *TransportError) Unwrap() error {
	return e.Err
}

// Temporary 返回是否为临时错误
func (e *TransportError) Temporary() bool {
	return e.tempErr
}

// NewTransportError 创建传输层错误
func NewTransportError(op, addr string, err error, tempErr bool) *TransportError {
	return &TransportError{
		Op:      op,
		Addr:    addr,
		Err:     err,
		tempErr: tempErr,
	}
}

// ========================================
// 通用错误构造函数
// ========================================

// NewStoreError 创建存储错误
func NewStoreError(code ErrorCode, message string, err error) *StoreError {
	return &StoreError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// NewNotFoundError 创建"键不存在"错误
func NewNotFoundError(key string) *StoreError {
	return &StoreError{
		Code:    ErrCodeNotFound,
		Message: fmt.Sprintf("键不存在: %s", key),
	}
}

// NewAlreadyExistsError 创建"键已存在"错误
func NewAlreadyExistsError(key string) *StoreError {
	return &StoreError{
		Code:    ErrCodeAlreadyExists,
		Message: fmt.Sprintf("键已存在: %s", key),
	}
}

// NewVersionNotFoundError 创建"版本不存在"错误
func NewVersionNotFoundError(key string, version uint64) *StoreError {
	return &StoreError{
		Code:    ErrCodeVersionNotFound,
		Message: fmt.Sprintf("版本不存在: %s@%d", key, version),
	}
}

// NewChecksumError 创建校验和错误
func NewChecksumError(expected, actual uint32) *StoreError {
	return &StoreError{
		Code:    ErrCodeChecksum,
		Message: fmt.Sprintf("校验和不匹配: 期望 %d, 实际 %d", expected, actual),
	}
}

// NewClosedError 创建"已关闭"错误
func NewClosedError(resource string) *StoreError {
	return &StoreError{
		Code:    ErrCodeClosed,
		Message: fmt.Sprintf("%s 已关闭", resource),
	}
}

// NewInternalError 创建内部错误
func NewInternalError(message string, err error) *StoreError {
	return &StoreError{
		Code:    ErrCodeInternal,
		Message: message,
		Err:     err,
	}
}
