// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// Router RPC 方法路由器
//
// 职责：
// - 注册 RPC 方法处理器
// - 路由 RPC 请求到对应处理器
// - 提供方法发现和查询功能
type Router struct {
	mu       sync.RWMutex
	handlers map[string]*RPCHandlerWrapper
}

// NewRouter 创建 RPC 路由器
func NewRouter() *Router {
	return &Router{
		handlers: make(map[string]*RPCHandlerWrapper),
	}
}

// RegisterHandler 注册 RPC 处理器
//
// 参数：
//   - method: 方法名（如 "NodeJoin", "ClusterStatus"）
//   - handler: RPC 处理器函数
//
// 返回：
//   - error: 如果方法名已注册，返回错误
func (r *Router) RegisterHandler(method string, handler RPCHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	method = normalizeMethodName(method)
	if _, exists := r.handlers[method]; exists {
		return errors.Wrapf(errors.ErrNoHandler, "RPC method already registered: %s", method)
	}

	wrapper := NewRPCHandlerWrapper(method, handler)
	r.handlers[method] = wrapper

	logging.WithField("method", method).Info("RPC 方法已注册")
	return nil
}

// UnregisterHandler 注销 RPC 处理器
func (r *Router) UnregisterHandler(method string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	method = normalizeMethodName(method)
	delete(r.handlers, method)

	logging.WithField("method", method).Info("RPC 方法已注销")
}

// Route 路由 RPC 请求到对应处理器
//
// 参数：
//   - method: 方法名
//   - ctx: 上下文
//   - req: 请求体
//
// 返回：
//   - []byte: 响应体
//   - error: 错误信息
func (r *Router) Route(method string, ctx context.Context, req []byte) ([]byte, error) {
	method = normalizeMethodName(method)

	r.mu.RLock()
	wrapper, exists := r.handlers[method]
	r.mu.RUnlock()

	if !exists {
		return nil, NewRPCError(ErrCodeNotFound, fmt.Sprintf("RPC method not found: %s", method))
	}

	// 调用处理器
	return wrapper.Handle(ctx, req)
}

// HasMethod 检查方法是否已注册
func (r *Router) HasMethod(method string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	method = normalizeMethodName(method)
	_, exists := r.handlers[method]
	return exists
}

// ListMethods 列出所有已注册的方法
func (r *Router) ListMethods() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	methods := make([]string, 0, len(r.handlers))
	for method := range r.handlers {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

// GetHandler 获取指定方法的处理器（用于测试）
func (r *Router) GetHandler(method string) (RPCHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	method = normalizeMethodName(method)
	wrapper, exists := r.handlers[method]
	if !exists {
		return nil, false
	}
	return wrapper.handler, true
}

// normalizeMethodName 规范化方法名（转小写，去空格）
func normalizeMethodName(method string) string {
	return strings.ToLower(strings.TrimSpace(method))
}

// RegisterHandlerFunc 便捷方法：注册函数作为处理器
func (r *Router) RegisterHandlerFunc(method string, handler func(ctx context.Context, req []byte) ([]byte, error)) error {
	return r.RegisterHandler(method, RPCHandler(handler))
}

// Call 调用 RPC 方法（便捷方法）
func (r *Router) Call(method string, ctx context.Context, req []byte) ([]byte, error) {
	return r.Route(method, ctx, req)
}
