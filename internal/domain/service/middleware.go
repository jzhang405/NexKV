// Package service 定义领域服务接口
package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ============================================================================
// Middleware 接口定义
// ============================================================================

// SendFunc 发送函数签名
type SendFunc func(ctx context.Context, peer model.PeerID, msg model.Message) error

// ReceiveFunc 接收函数签名
type ReceiveFunc func(ctx context.Context, peer model.PeerID, msg model.Message) error

// Middleware 中间件接口（拦截器模式）
type Middleware interface {
	// Name 中间件名称
	Name() string

	// Priority 中间件优先级（数字越小越先执行）
	// 固定优先级：
	// - RateLimit: 10
	// - CircuitBreaker: 20
	// - Compression: 30
	// - Retry: 40
	// - Logging/Metrics: 5（最外层）
	Priority() int

	// InterceptSend 拦截发送消息
	InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next SendFunc) error

	// InterceptReceive 拦截接收消息
	InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next ReceiveFunc) error
}

// MiddlewarePriority 中间件优先级常量
// 数字越小越先执行（越外层）
const (
	MiddlewarePriorityLogging        = 5  // 日志（最外层）
	MiddlewarePriorityMetrics        = 6  // 指标
	MiddlewarePriorityRateLimit      = 10 // 限流
	MiddlewarePriorityCircuitBreaker = 20 // 熔断
	MiddlewarePriorityCompression    = 30 // 压缩
	MiddlewarePriorityRetry          = 40 // 重试（最内层）
)

// MiddlewareChain 中间件链管理器
//
// 并发安全策略：
// 1. 使用读写锁（sync.RWMutex）保护中间件列表
// 2. Execute 时获取快照执行，避免持锁时间过长
// 3. 提供 Freeze 方法，冻结后禁止修改（高性能场景）
type MiddlewareChain interface {
	// Use 添加中间件（自动按 Priority() 排序）
	Use(middleware Middleware) error

	// Remove 移除指定名称的中间件
	Remove(name string) error

	// List 获取所有中间件列表（返回快照）
	List() []Middleware

	// Freeze 冻结中间件链，禁止后续修改
	// 冻结后 Use/Remove/Clear 返回 ErrChainFrozen
	// 适用场景：启动完成后调用，避免运行时修改开销
	Freeze()

	// IsFrozen 检查是否已冻结
	IsFrozen() bool

	// ExecuteSend 执行发送中间件链
	ExecuteSend(ctx context.Context, peer model.PeerID, msg model.Message, final SendFunc) error

	// ExecuteReceive 执行接收中间件链
	ExecuteReceive(ctx context.Context, peer model.PeerID, msg model.Message, final ReceiveFunc) error

	// Clear 清空所有中间件（冻结后返回错误）
	Clear() error
}
