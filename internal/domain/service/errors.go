// Package service 定义领域服务接口
package service

import "github.com/jzhang405/NexKV/pkg/errors"

// ============================================================================
// 错误定义（从 pkg/errors 重新导出）
//
// 设计说明：
// - 所有错误定义集中在 pkg/errors/errors.go
// - 此处重新导出便于 infrastructure 层使用 service.ErrXXX
// - 保持 DDD 分层：infrastructure 依赖 domain/service，不直接依赖 pkg/errors
// ============================================================================

var (
	// 通用错误
	ErrCanceled        = errors.ErrCanceled
	ErrTimeout         = errors.ErrTimeout
	ErrCompleted       = errors.ErrCompleted
	ErrAlreadyCanceled = errors.ErrAlreadyCanceled
	ErrInvalidParam    = errors.ErrInvalidParam

	// Transport 层错误
	ErrTransportClosed  = errors.ErrTransportClosed
	ErrAlreadyConnected = errors.ErrAlreadyConnected
	ErrConnectionFailed = errors.ErrConnectionFailed
	ErrNotConnected     = errors.ErrNotConnected
	ErrChannelClosed    = errors.ErrChannelClosed
	ErrMessageTooLarge  = errors.ErrMessageTooLarge
	ErrInvalidMessage   = errors.ErrInvalidMessage
	ErrNodeNotFound     = errors.ErrNodeNotFound
	ErrPeerIDInvalid    = errors.ErrPeerIDInvalid
	ErrAddrInvalid      = errors.ErrAddrInvalid
	ErrAddrTooLong      = errors.ErrAddrTooLong

	// 异步模块错误
	ErrAsyncExecFailed = errors.ErrAsyncExecFailed
	ErrCallbackPanic   = errors.ErrCallbackPanic

	// RPC 层错误
	ErrMajorityFailed      = errors.ErrMajorityFailed
	ErrAllFailed           = errors.ErrAllFailed
	ErrPeerUnreachable     = errors.ErrPeerUnreachable
	ErrNoHandler           = errors.ErrNoHandler
	ErrCodecFailure        = errors.ErrCodecFailure
	ErrStrategyNotMajority = errors.ErrStrategyNotMajority
	ErrInvalidStrategy     = errors.ErrInvalidStrategy

	// Middleware 层错误
	ErrChainFrozen = errors.ErrChainFrozen

	// TaskRunnerContext 层错误（复用 Pipeline 错误定义）
	ErrTaskRunnerClosed          = errors.ErrPipelineClosed
	ErrTaskRunnerShutdownTimeout = errors.ErrPipelineShutdownTimeout
)

// Wrap 和 Wrapf 从 pkg/errors 重新导出（便于 infrastructure 层使用）
var (
	Wrap = errors.Wrap
)
