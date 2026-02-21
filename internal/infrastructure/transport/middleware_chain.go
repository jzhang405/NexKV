// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"sort"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// middlewareChain 中间件链实现
type middlewareChain struct {
	mu          sync.RWMutex
	middlewares []service.Middleware
	frozen      bool
}

// NewMiddlewareChain 创建中间件链
func NewMiddlewareChain() service.MiddlewareChain {
	return &middlewareChain{
		middlewares: make([]service.Middleware, 0),
		frozen:      false,
	}
}

// Use 添加中间件到链尾
// 注意：中间件会按 Priority 自动排序，数字越小越先执行
func (c *middlewareChain) Use(middleware service.Middleware) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.frozen {
		return service.ErrChainFrozen
	}

	// P1-3 修复：nil 中间件保护
	if middleware == nil {
		return nil // 忽略 nil 中间件，不报错
	}

	c.middlewares = append(c.middlewares, middleware)

	// 按优先级排序（数字越小越先执行）
	sort.Slice(c.middlewares, func(i, j int) bool {
		return c.middlewares[i].Priority() < c.middlewares[j].Priority()
	})

	return nil
}

// Remove 移除指定名称的中间件
func (c *middlewareChain) Remove(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.frozen {
		return service.ErrChainFrozen
	}

	for i, mw := range c.middlewares {
		if mw.Name() == name {
			c.middlewares = append(c.middlewares[:i], c.middlewares[i+1:]...)
			return nil
		}
	}

	return nil // 未找到不报错
}

// List 获取所有中间件列表（返回快照）
func (c *middlewareChain) List() []service.Middleware {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 返回快照
	result := make([]service.Middleware, len(c.middlewares))
	copy(result, c.middlewares)
	return result
}

// Freeze 冻结中间件链，禁止后续修改
func (c *middlewareChain) Freeze() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frozen = true
}

// IsFrozen 检查是否已冻结
func (c *middlewareChain) IsFrozen() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.frozen
}

// ExecuteSend 执行发送中间件链
func (c *middlewareChain) ExecuteSend(ctx context.Context, peer model.PeerID, msg model.Message, final service.SendFunc) error {
	// 获取快照并构建责任链（从后向前）
	middlewares := c.snapshot()
	next := final
	for i := len(middlewares) - 1; i >= 0; i-- {
		next = createSendChainLink(middlewares[i], next)
	}
	return next(ctx, peer, msg)
}

// createSendChainLink 创建发送链的链接（避免闭包问题）
func createSendChainLink(mw service.Middleware, next service.SendFunc) service.SendFunc {
	return func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return mw.InterceptSend(ctx, peer, msg, next)
	}
}

// ExecuteReceive 执行接收中间件链
// 注意：Receive 链的执行顺序与 Send 链相反
// - Send: RateLimit → CB → Compression → Retry → Final
// - Receive: Retry → Compression → CB → RateLimit → Final（反向）
func (c *middlewareChain) ExecuteReceive(ctx context.Context, peer model.PeerID, msg model.Message, final service.ReceiveFunc) error {
	// 获取快照并构建责任链（从前向后，与 Send 相反）
	middlewares := c.snapshot()
	next := final
	for i := 0; i < len(middlewares); i++ {
		next = createReceiveChainLink(middlewares[i], next)
	}
	return next(ctx, peer, msg)
}

// snapshot 获取中间件链快照（线程安全）
func (c *middlewareChain) snapshot() []service.Middleware {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]service.Middleware, len(c.middlewares))
	copy(result, c.middlewares)
	return result
}

// createReceiveChainLink 创建接收链的链接（避免闭包问题）
func createReceiveChainLink(mw service.Middleware, next service.ReceiveFunc) service.ReceiveFunc {
	return func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return mw.InterceptReceive(ctx, peer, msg, next)
	}
}

// Clear 清空所有中间件
func (c *middlewareChain) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.frozen {
		return service.ErrChainFrozen
	}

	c.middlewares = make([]service.Middleware, 0)
	return nil
}

// 确保实现 MiddlewareChain 接口
var _ service.MiddlewareChain = (*middlewareChain)(nil)
