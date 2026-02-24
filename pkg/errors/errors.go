// Package errors 提供统一的错误定义
// 采用 sentinel error + NexError 包装模式
package errors

import (
	stderrors "errors"
	"fmt"
)

// ===========================
// 标准 Sentinel Errors
// ===========================

var (
	// 通用错误
	ErrCanceled        = stderrors.New("operation canceled")
	ErrTimeout         = stderrors.New("operation timeout")
	ErrCompleted       = stderrors.New("operation already completed")
	ErrAlreadyCanceled = stderrors.New("operation already canceled")
	ErrInvalidParam    = stderrors.New("invalid parameter")

	// Transport 层错误
	ErrTransportClosed  = stderrors.New("transport: is closed")
	ErrAlreadyConnected = stderrors.New("transport: already connected")
	ErrConnectionFailed = stderrors.New("transport: connection failed")
	ErrNotConnected     = stderrors.New("transport: not connected")
	ErrChannelClosed    = stderrors.New("transport: channel is closed")
	ErrMessageTooLarge  = stderrors.New("transport: message size exceeds limit")
	ErrInvalidMessage   = stderrors.New("transport: invalid message format")
	ErrNodeNotFound     = stderrors.New("transport: node not found")
	ErrPeerIDInvalid    = stderrors.New("transport: invalid peer ID format")
	ErrAddrInvalid      = stderrors.New("transport: invalid address format")
	ErrAddrTooLong      = stderrors.New("transport: address too long")

	// Async Operation 错误
	ErrAsyncExecFailed           = stderrors.New("async: operation failed")
	ErrCallbackPanic             = stderrors.New("async: callback panic recovered")
	ErrOperationAlreadyCompleted = stderrors.New("async: operation already completed")
	ErrOperationCanceled         = stderrors.New("async: operation canceled")
	ErrCallbackIDEmpty           = stderrors.New("async: callback ID cannot be empty")
	ErrCallbackNotFound          = stderrors.New("async: callback not found")
	ErrCancelNotSupported        = stderrors.New("async: cancel not supported")
	ErrDiscardNotSupported       = stderrors.New("async: discard not supported")

	// GoroutinePool 层错误
	ErrPoolClosed            = stderrors.New("concurrency: goroutine pool is closed")
	ErrPoolFull              = stderrors.New("concurrency: goroutine pool is full")
	ErrTaskArgLengthMismatch = stderrors.New("concurrency: task and argument length mismatch")
	ErrTaskCanceled          = stderrors.New("concurrency: task was canceled")
	ErrTaskTimeout           = stderrors.New("concurrency: task timeout")
	ErrTooManyDelayedTasks   = stderrors.New("concurrency: too many delayed tasks") // P1-01

	// GoroutinePool 层错误
	ErrPoolClosed            = stderrors.New("concurrency: goroutine pool is closed")
	ErrPoolFull              = stderrors.New("concurrency: goroutine pool is full")
	ErrTaskArgLengthMismatch = stderrors.New("concurrency: task and argument length mismatch")
	ErrTaskCanceled          = stderrors.New("concurrency: task was canceled")
	ErrTaskTimeout           = stderrors.New("concurrency: task timeout")
	ErrTooManyDelayedTasks   = stderrors.New("concurrency: too many delayed tasks") // P1-01

	// RPC 层错误
	ErrMajorityFailed      = stderrors.New("rpc: majority quorum not reached")
	ErrAllFailed           = stderrors.New("rpc: all nodes failed")
	ErrPeerUnreachable     = stderrors.New("rpc: peer unreachable")
	ErrNoHandler           = stderrors.New("rpc: no handler registered")
	ErrCodecFailure        = stderrors.New("rpc: codec failure")
	ErrStrategyNotMajority = stderrors.New("rpc: strategy satisfied but not majority")
	ErrInvalidStrategy     = stderrors.New("rpc: invalid response strategy")
	ErrTargetsMsgsMismatch = stderrors.New("rpc: targets and msgs length mismatch")
	ErrInvalidQuorum       = stderrors.New("rpc: invalid quorum value")
	ErrInvalidTimeout      = stderrors.New("rpc: invalid timeout value")
	ErrEmptyPeers          = stderrors.New("rpc: peers slice is empty")
	ErrNilConfig           = stderrors.New("rpc: config is nil")
	ErrNilRPC              = stderrors.New("rpc: rpc is nil")

	// Middleware 层错误
	ErrChainFrozen        = stderrors.New("middleware: chain is frozen")
	ErrRateLimitExceeded  = stderrors.New("middleware: rate limit exceeded")
	ErrCircuitBreakerOpen = stderrors.New("middleware: circuit breaker is open")
	ErrInvalidCompression = stderrors.New("middleware: invalid or unsupported compression")

	// Compressor 层错误
	ErrCompressionFailed   = stderrors.New("compressor: compression failed")
	ErrDecompressionFailed = stderrors.New("compressor: decompression failed")
	ErrUnsupportedType     = stderrors.New("compressor: unsupported compression type")
	ErrDecompressionTooBig = stderrors.New("compressor: decompressed data exceeds size limit")

	// Test Framework 层错误
	ErrTestSetupFailed    = stderrors.New("test: setup failed")
	ErrTestExecuteFailed  = stderrors.New("test: execute failed")
	ErrTestVerifyFailed   = stderrors.New("test: verify failed")
	ErrTestTeardownFailed = stderrors.New("test: teardown failed")
	ErrComponentNotFound  = stderrors.New("test: component not found")
	ErrComponentExists    = stderrors.New("test: component already exists")
	ErrDependencyNotMet   = stderrors.New("test: dependency not met")
	ErrCircularDependency = stderrors.New("test: circular dependency detected")
	ErrClusterNotRunning  = stderrors.New("test: cluster not running")
	ErrTestNodeNotFound   = stderrors.New("test: node not found")
	ErrInvalidState       = stderrors.New("test: invalid cluster state")
	ErrNotImplemented     = stderrors.New("test: not implemented")
	ErrNotInitialized     = stderrors.New("test: not initialized")

	// Clock 层错误
	ErrClockInvalidData = stderrors.New("clock: invalid data")
	ErrClockNilHLC      = stderrors.New("clock: nil HLC")
	ErrClockMarshalNil  = stderrors.New("clock: cannot marshal nil HLC")
	ErrClockInvalidSize = stderrors.New("clock: invalid HLC data size")
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
	// Guard against nil receiver
	if e == nil {
		return "error: nil"
	}
	// Guard against nil Err
	if e.Err == nil {
		if e.Details != "" {
			return e.Details
		}
		return "error: nil"
	}
	if e.Details != "" {
		return fmt.Sprintf("%s: %s", e.Err.Error(), e.Details)
	}
	return e.Err.Error()
}

// Unwrap 支持错误链
func (e *NexError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is 支持 errors.Is() 比较（基于 sentinel error）
// 注意：NexError.Err 必须是 sentinel error，不能是另一个 *NexError
func (e *NexError) Is(target error) bool {
	if e == nil {
		return false
	}
	return stderrors.Is(e.Err, target)
}

// ===========================
// 便捷包装函数（返回增强错误）
// ===========================

// Wrap 包装标准错误，携带详情
// 如果 err 本身是 *NexError，会自动解包并合并详情，防止嵌套
func Wrap(err error, details string) *NexError {
	// P0-2 修复：nil 错误返回 nil，避免 panic
	if err == nil {
		return nil
	}
	return mergeNexError(err, details)
}

// Wrapf 包装标准错误，格式化详情
// 如果 err 本身是 *NexError，会自动解包并合并详情，防止嵌套
func Wrapf(err error, format string, args ...any) *NexError {
	// P0-2 修复：nil 错误返回 nil，避免 panic
	if err == nil {
		return nil
	}
	return mergeNexError(err, fmt.Sprintf(format, args...))
}

// mergeNexError 合并 NexError 详情（内部方法）
func mergeNexError(err error, details string) *NexError {
	// P1-3 修复：防止嵌套 NexError
	if nexErr, ok := err.(*NexError); ok {
		mergedDetails := nexErr.Details
		if mergedDetails != "" && details != "" {
			mergedDetails += "; " + details
		} else if details != "" {
			mergedDetails = details
		}
		return &NexError{
			Err:     nexErr.Err,
			Details: mergedDetails,
		}
	}
	return &NexError{
		Err:     err,
		Details: details,
	}
}
