// Package rpc 基于 libp2p Stream 的 RPC 实现
//
// 提供节点间远程过程调用（RPC）功能，用于：
// - TreeCoordinator 与其他节点通信
// - CLI 命令与节点交互
// - 集群状态同步
//
// 架构设计：
// - 使用 libp2p Stream 作为传输层
// - 复用 internal/transport 的 MessagePackCodec 编解码
// - 支持方法路由和请求/响应封装
package rpc

import (
	"context"
	"fmt"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// RPCRequest RPC 请求封装
type RPCRequest struct {
	Method    string        // 方法名（如 "NodeJoin", "ClusterStatus"）
	RequestID uint64        // 请求ID（用于匹配响应）
	Body      []byte        // 请求体（MessagePack 序列化的参数）
	Timeout   time.Duration // 请求超时时间
}

// RPCResponse RPC 响应封装
type RPCResponse struct {
	RequestID uint64 // 请求ID（对应请求的 ID）
	Status    int    // 状态码（0=成功，非0=错误）
	Body      []byte // 响应体（MessagePack 序列化的结果）
}

// RPCHandler RPC 处理器函数类型
type RPCHandler func(ctx context.Context, req []byte) ([]byte, error)

// RPCHandlerWrapper 包装 RPCHandler，提供超时处理
type RPCHandlerWrapper struct {
	handler RPCHandler
	method  string
}

// NewRPCHandlerWrapper 创建 RPC 处理器包装器
func NewRPCHandlerWrapper(method string, handler RPCHandler) *RPCHandlerWrapper {
	return &RPCHandlerWrapper{
		handler: handler,
		method:  method,
	}
}

// Handle 处理 RPC 请求
func (w *RPCHandlerWrapper) Handle(ctx context.Context, req []byte) ([]byte, error) {
	// 直接使用传入的 context，不创建新的带超时的 context
	// 超时由调用方在创建 stream 时控制，避免 context 泄漏
	return w.handler(ctx, req)
}

// getContextTimeout 从上下文中提取超时时间
func getContextTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline).Round(time.Millisecond)
	}
	return 0
}

// RPCError RPC 错误
type RPCError struct {
	Code    int
	Message string
}

// Error 实现 error 接口
func (e *RPCError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "RPC error"
}

// NewRPCError 创建 RPC 错误
func NewRPCError(code int, message string) *RPCError {
	return &RPCError{
		Code:    code,
		Message: message,
	}
}

// 错误码定义
const (
	ErrCodeSuccess      = 0
	ErrCodeNotFound     = 404
	ErrCodeTimeout      = 408
	ErrCodeInternal     = 500
	ErrCodeUnavailable  = 503 // 服务暂时不可用
	ErrCodeBadRequest   = 400 // 请求格式错误
	ErrCodeUnauthorized = 401 // 未授权
	ErrCodeForbidden    = 403 // 禁止访问
)

// IsRPCError 判断是否为 RPC 错误
func IsRPCError(err error) bool {
	_, ok := err.(*RPCError)
	return ok
}

// GetRPCErrorCode 获取 RPC 错误码
func GetRPCErrorCode(err error) int {
	if rpcErr, ok := err.(*RPCError); ok {
		return rpcErr.Code
	}
	return ErrCodeInternal
}

// 常用 RPC 错误
var (
	ErrTimeout      = NewRPCError(ErrCodeTimeout, "RPC request timeout")
	ErrNotFound     = NewRPCError(ErrCodeNotFound, "Resource not found")
	ErrInternal     = NewRPCError(ErrCodeInternal, "Internal RPC error")
	ErrUnavailable  = NewRPCError(ErrCodeUnavailable, "Service temporarily unavailable")
	ErrBadRequest   = NewRPCError(ErrCodeBadRequest, "Bad request format")
	ErrUnauthorized = NewRPCError(ErrCodeUnauthorized, "Unauthorized")
	ErrForbidden    = NewRPCError(ErrCodeForbidden, "Forbidden")
)

// ========================================
// 序列化/反序列化辅助函数
// ========================================

// MarshalRPCRequest 序列化 RPC 请求
func MarshalRPCRequest(req *RPCRequest) ([]byte, error) {
	data, err := msgpack.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化 RPC 请求失败: %w", err)
	}
	return data, nil
}

// UnmarshalRPCRequest 反序列化 RPC 请求
func UnmarshalRPCRequest(data []byte) (*RPCRequest, error) {
	var req RPCRequest
	if err := msgpack.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("反序列化 RPC 请求失败: %w", err)
	}
	return &req, nil
}

// MarshalRPCResponse 序列化 RPC 响应
func MarshalRPCResponse(resp *RPCResponse) ([]byte, error) {
	data, err := msgpack.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("序列化 RPC 响应失败: %w", err)
	}
	return data, nil
}

// UnmarshalRPCResponse 反序列化 RPC 响应
func UnmarshalRPCResponse(data []byte) (*RPCResponse, error) {
	var resp RPCResponse
	if err := msgpack.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("反序列化 RPC 响应失败: %w", err)
	}
	return &resp, nil
}

// ========================================
// 通用辅助函数
// ========================================

// nowTimestamp 获取当前时间戳（纳秒）
func nowTimestamp() int64 {
	return time.Now().UnixNano()
}

// setStreamDeadline 设置 Stream 超时
func setStreamDeadline(stream interface{ SetDeadline(time.Time) error }, deadline time.Time) error {
	if err := stream.SetDeadline(deadline); err != nil {
		return fmt.Errorf("设置超时失败: %w", err)
	}
	return nil
}
