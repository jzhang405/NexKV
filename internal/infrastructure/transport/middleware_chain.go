// Package transport 实现传输层基础设施
package transport

import (
	"context"
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
	return nil
}

// UseFirst 添加中间件到链头（优先执行）
func (c *middlewareChain) UseFirst(middleware service.Middleware) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.frozen {
		return service.ErrChainFrozen
	}

	// P1-3 修复：nil 中间件保护
	if middleware == nil {
		return nil // 忽略 nil 中间件，不报错
	}

	c.middlewares = append([]service.Middleware{middleware}, c.middlewares...)
	return nil
}

// UseAt 在指定位置插入中间件
func (c *middlewareChain) UseAt(index int, middleware service.Middleware) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.frozen {
		return service.ErrChainFrozen
	}

	// P1-3 修复：nil 中间件保护
	if middleware == nil {
		return nil // 忽略 nil 中间件，不报错
	}

	// 边界检查
	if index < 0 {
		index = 0
	}
	if index > len(c.middlewares) {
		index = len(c.middlewares)
	}

	// 插入中间件
	c.middlewares = append(
		c.middlewares[:index],
		append([]service.Middleware{middleware}, c.middlewares[index:]...)...,
	)
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
	// 1. 获取快照（读锁）
	c.mu.RLock()
	middlewares := make([]service.Middleware, len(c.middlewares))
	copy(middlewares, c.middlewares)
	c.mu.RUnlock()

	// 2. 构建责任链（从后向前）
	next := final
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw := middlewares[i]
		// 捕获当前中间件和 next
		next = createSendChainLink(mw, next)
	}

	// 3. 执行链
	return next(ctx, peer, msg)
}

// createSendChainLink 创建发送链的链接（避免闭包问题）
func createSendChainLink(mw service.Middleware, next service.SendFunc) service.SendFunc {
	return func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return mw.InterceptSend(ctx, peer, msg, next)
	}
}

// ExecuteReceive 执行接收中间件链
func (c *middlewareChain) ExecuteReceive(ctx context.Context, peer model.PeerID, msg model.Message, final service.ReceiveFunc) error {
	// 1. 获取快照（读锁）
	c.mu.RLock()
	middlewares := make([]service.Middleware, len(c.middlewares))
	copy(middlewares, c.middlewares)
	c.mu.RUnlock()

	// 2. 构建责任链（从后向前）
	next := final
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw := middlewares[i]
		// 捕获当前中间件和 next
		next = createReceiveChainLink(mw, next)
	}

	// 3. 执行链
	return next(ctx, peer, msg)
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
