// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// PortBinding 端口绑定信息
type PortBinding struct {
	TestID      string         // 测试 ID
	Port        int            // 端口号
	Listener    net.Listener   // 持有的 Listener（防止端口泄露）
	AllocatedAt time.Time      // 分配时间
}

// TestPortAllocator 测试专用端口分配器
// 使用 OS 动态分配策略 + Listener 持有策略，避免端口泄露
type TestPortAllocator struct {
	mu        sync.RWMutex
	allocated map[int]*PortBinding  // port -> binding
}

// NewTestPortAllocator 创建测试端口分配器
func NewTestPortAllocator() *TestPortAllocator {
	return &TestPortAllocator{
		allocated: make(map[int]*PortBinding),
	}
}

// AllocatePort 分配一个可用端口
// 使用 net.Listen("127.0.0.1:0") 让 OS 动态分配
// 持有 Listener 防止端口被其他进程占用
func (pa *TestPortAllocator) AllocatePort(testID string) (int, error) {
	// 监听 127.0.0.1:0 获取随机可用端口
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to allocate port: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port

	// 创建绑定信息
	binding := &PortBinding{
		TestID:      testID,
		Port:        port,
		Listener:    listener,  // 持有 Listener
		AllocatedAt: time.Now(),
	}

	// 记录分配
	pa.mu.Lock()
	pa.allocated[port] = binding
	pa.mu.Unlock()

	return port, nil
}

// ReleasePort 释放指定端口
// 关闭 Listener 并移除绑定记录
func (pa *TestPortAllocator) ReleasePort(port int) error {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	binding, exists := pa.allocated[port]
	if !exists {
		return nil  // 不存在则忽略
	}

	// 关闭 Listener
	if binding.Listener != nil {
		binding.Listener.Close()
	}

	// 移除记录
	delete(pa.allocated, port)

	return nil
}

// ReleaseAll 释放所有端口
func (pa *TestPortAllocator) ReleaseAll() error {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	var lastErr error
	for port, binding := range pa.allocated {
		if binding.Listener != nil {
			if err := binding.Listener.Close(); err != nil {
				lastErr = err
			}
		}
		delete(pa.allocated, port)
	}

	return lastErr
}

// GetBinding 获取指定端口的绑定信息
func (pa *TestPortAllocator) GetBinding(port int) *PortBinding {
	pa.mu.RLock()
	defer pa.mu.RUnlock()
	return pa.allocated[port]
}

// AllocatedCount 返回已分配的端口数量
func (pa *TestPortAllocator) AllocatedCount() int {
	pa.mu.RLock()
	defer pa.mu.RUnlock()
	return len(pa.allocated)
}
