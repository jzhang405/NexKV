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
	ErrAllWorkersBusy        = stderrors.New("concurrency: all workers are bound to different source IDs")
	ErrTooManyDelayedTasks   = stderrors.New("concurrency: too many delayed tasks") // P1-01

	// TaskExecutor 层错误
	ErrExecutorClosed    = stderrors.New("concurrency: executor is closed")
	ErrExecutorNotFound  = stderrors.New("concurrency: executor not found for requested mode")
	ErrSelectorClosed    = stderrors.New("concurrency: selector is closed")
	ErrDuplicateExecutor = stderrors.New("concurrency: executor already registered for this mode")
	ErrInvalidConfig     = stderrors.New("concurrency: invalid configuration")
	ErrQueueFull         = stderrors.New("concurrency: queue full")

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

	// Per-Core 执行器错误
	ErrPerCoreExecutorClosed  = stderrors.New("percore: executor is closed")
	ErrPerCoreQueueFull       = stderrors.New("percore: task queue is full")
	ErrPerCoreInvalidCore     = stderrors.New("percore: invalid core ID")
	ErrPerCoreShutdownTimeout = stderrors.New("percore: shutdown timeout")
	ErrPerCoreNotSupported    = stderrors.New("percore: operation not supported")

	// 可暂停调度器错误
	ErrStepNotFound         = stderrors.New("step: operation not found")
	ErrStepNotPausable      = stderrors.New("step: step is not pausable")
	ErrStepMaxPausedReached = stderrors.New("step: max paused operations limit reached")
	ErrCheckpointNotFound   = stderrors.New("step: checkpoint not found")
	ErrMigrationNotFound    = stderrors.New("step: migration not found")

	// Request ID 错误
	ErrRequestIDInvalidFormat = stderrors.New("request: invalid request id format")
	ErrRequestIDEmpty         = stderrors.New("request: request id cannot be empty")

	// CPU 亲和性错误
	ErrCPUInvalidCoreID     = stderrors.New("affinity: invalid core ID")
	ErrCPUSetAffinityFailed = stderrors.New("affinity: sched_setaffinity failed")
	ErrCPUGetCurrentThread  = stderrors.New("affinity: GetCurrentThread failed")
	ErrCPUSetAffinityMask   = stderrors.New("affinity: SetThreadAffinityMask failed")

	// SourceID 错误
	ErrSourceIDEmpty          = stderrors.New("source_id: cannot be empty")
	ErrSourceIDInvalidFormat  = stderrors.New("source_id: must be in format {module}:{sub-module}:{action}")
	ErrSourceIDModuleEmpty    = stderrors.New("source_id: module cannot be empty")
	ErrSourceIDSubModuleEmpty = stderrors.New("source_id: sub-module cannot be empty")
	ErrSourceIDActionEmpty    = stderrors.New("source_id: action cannot be empty")

	// TaskMode 错误
	ErrTaskModeUnknown = stderrors.New("task_mode: unknown task mode")
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

// ===========================
// 便捷格式化函数（返回错误）
// ===========================

// InvalidParamf 创建参数无效错误
func InvalidParamf(format string, args ...any) error {
	return stderrors.New("invalid parameter: " + fmt.Sprintf(format, args...))
}

// InvalidCoreID 创建无效核心ID错误
func InvalidCoreID(coreID, maxCoreID int) error {
	return Wrapf(ErrCPUInvalidCoreID, "core ID %d out of range [0, %d]", coreID, maxCoreID)
}

// SourceIDEmpty 创建 SourceID 为空错误
func SourceIDEmpty() error {
	return ErrSourceIDEmpty
}

// SourceIDInvalidFormat 创建 SourceID 格式错误
func SourceIDInvalidFormat() error {
	return ErrSourceIDInvalidFormat
}

// ModuleEmpty 创建模块为空错误
func ModuleEmpty() error {
	return ErrSourceIDModuleEmpty
}

// SubModuleEmpty 创建子模块为空错误
func SubModuleEmpty() error {
	return ErrSourceIDSubModuleEmpty
}

// ActionEmpty 创建动作字段为空错误
func ActionEmpty() error {
	return ErrSourceIDActionEmpty
}

// UnknownTaskMode 创建未知任务模式错误
func UnknownTaskMode(mode string) error {
	return Wrapf(ErrTaskModeUnknown, "mode: %s", mode)
}
