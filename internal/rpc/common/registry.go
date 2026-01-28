// Package common RPC 通信公共模块
package common

import (
	"fmt"
	"sync"
)

// Handler RPC 处理器接口
type Handler interface {
	// Handle 处理 RPC 请求
	Handle(payload []byte) ([]byte, error)
}

// HandlerRegistry RPC 处理器注册表
type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]Handler // messageType -> Handler
}

// NewHandlerRegistry 创建处理器注册表
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: make(map[string]Handler),
	}
}

// RegisterHandler 注册处理器
func (r *HandlerRegistry) RegisterHandler(messageType string, handler Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[messageType]; exists {
		return fmt.Errorf("处理器已存在: %s", messageType)
	}

	r.handlers[messageType] = handler
	return nil
}

// GetHandler 获取处理器
func (r *HandlerRegistry) GetHandler(messageType string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, exists := r.handlers[messageType]
	return handler, exists
}

// ListHandlers 列出所有已注册的处理器
func (r *HandlerRegistry) ListHandlers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.handlers))
	for messageType := range r.handlers {
		types = append(types, messageType)
	}
	return types
}
