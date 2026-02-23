// Package service 定义领域服务接口
package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ============================================================================
// RPCSync 同步 RPC 接口
// ============================================================================

// RPCSync 同步 RPC 接口
//
// 提供阻塞式的 RPC 调用能力，包括单播、广播和批量写入。
// 所有方法都会阻塞等待直到操作完成或超时。
//
// 与 RPCAsync 的区别：
// - RPCSync: 阻塞式调用，直接返回结果
// - RPCAsync: 返回 AsyncOperation[T]，支持链式回调和超时设置
type RPCSync interface {
	// ====== 单播 ======
	// 同步调用（阻塞等响应）
	Call(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error)

	// ====== 广播 ======
	// 同消息广播：支持响应策略 + 可选进度追踪
	// - strategy: 响应策略（All/Majority/None）
	// - progress: 可选进度追踪器，nil 表示不追踪
	BroadcastCall(
		ctx context.Context,
		to []model.PeerID,
		req model.Message,
		strategy ResponseStrategy,
		progress *BroadcastProgress,
	) (BroadcastResult, error)

	// 不同消息群发：WriteV（单向，不等待响应，等价于 ResponseNone）
	// 注意：WriteV 是 "Write Vector" 的缩写，表示批量写入多个目标节点
	WriteV(ctx context.Context, targets []model.PeerID, msgs []model.Message, progress *BroadcastProgress) error

	// 不同消息群发：支持响应策略 + 可选进度追踪
	WriteVCall(
		ctx context.Context,
		targets []model.PeerID,
		msgs []model.Message,
		strategy ResponseStrategy,
		progress *BroadcastProgress,
	) (WriteVResult, error)

	// ====== 服务端 ======
	// 函数式处理（服务端注册处理器）
	OnRequest(handler func(ctx context.Context, from model.PeerID, req model.Message) model.Message) error

	// Channel 模式接收请求
	OnRequestChan() <-chan RequestMsg

	// ====== 生命周期 ======
	Close() error

	// ====== Goroutine 管理 ======
	// SetGoroutineProvider 设置 goroutine 提供者
	// 用于统一管理 goroutine 的创建和生命周期
	SetGoroutineProvider(provider GoroutineProvider)
}

// ============================================================================
// 向后兼容类型别名
// ============================================================================

// RPC 是 RPCSync 的类型别名，用于向后兼容
//
// Deprecated: 建议直接使用 RPCSync 以明确表达同步调用语义。
// 此别名将在未来版本中保留，以确保现有代码的兼容性。
//
// 迁移示例:
//
//	// 旧代码
//	var rpc service.RPC = transport.NewLibp2pRPC(...)
//
//	// 新代码（推荐）
//	var rpc service.RPCSync = transport.NewLibp2pRPC(...)
type RPC = RPCSync
