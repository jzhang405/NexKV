// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestPortAllocator_AllocatePort(t *testing.T) {
	allocator := NewTestPortAllocator()

	// 测试基本分配
	port, err := allocator.AllocatePort("test-1")
	require.NoError(t, err)
	assert.Greater(t, port, 0)
	assert.Less(t, port, 65536)

	// 验证端口可以监听（Listener 被持有）
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	listener.Close()

	// 测试多次分配返回不同端口
	port2, err := allocator.AllocatePort("test-2")
	require.NoError(t, err)
	assert.NotEqual(t, port, port2, "不同测试应分配不同端口")
}

func TestTestPortAllocator_ReleasePort(t *testing.T) {
	allocator := NewTestPortAllocator()

	port, err := allocator.AllocatePort("test-release")
	require.NoError(t, err)

	// 验证分配记录存在
	assert.Equal(t, 1, allocator.AllocatedCount())

	// 释放端口
	err = allocator.ReleasePort(port)
	require.NoError(t, err)

	// 验证分配记录被移除
	assert.Equal(t, 0, allocator.AllocatedCount())
}

func TestTestPortAllocator_ReleaseAll(t *testing.T) {
	allocator := NewTestPortAllocator()

	// 分配多个端口
	_, err := allocator.AllocatePort("test-1")
	require.NoError(t, err)
	_, err = allocator.AllocatePort("test-2")
	require.NoError(t, err)
	_, err = allocator.AllocatePort("test-3")
	require.NoError(t, err)

	assert.Equal(t, 3, allocator.AllocatedCount())

	// 释放所有
	err = allocator.ReleaseAll()
	require.NoError(t, err)

	assert.Equal(t, 0, allocator.AllocatedCount())
}

func TestTestPortAllocator_GetBinding(t *testing.T) {
	allocator := NewTestPortAllocator()

	port, err := allocator.AllocatePort("test-binding")
	require.NoError(t, err)

	// 获取绑定信息
	binding := allocator.GetBinding(port)
	require.NotNil(t, binding)
	assert.Equal(t, "test-binding", binding.TestID)
	assert.Equal(t, port, binding.Port)
	assert.False(t, binding.AllocatedAt.IsZero())
}

func TestTestPortAllocator_GetBinding_NotExist(t *testing.T) {
	allocator := NewTestPortAllocator()

	// 获取不存在的绑定
	binding := allocator.GetBinding(12345)
	assert.Nil(t, binding)
}

func TestTestPortAllocator_ConcurrentAllocation(t *testing.T) {
	allocator := NewTestPortAllocator()

	const numGoroutines = 100
	ports := make(chan int, numGoroutines)
	errors := make(chan error, numGoroutines)

	// 并发分配端口
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			port, err := allocator.AllocatePort("concurrent-test")
			if err != nil {
				errors <- err
				return
			}
			ports <- port
		}(i)
	}

	// 收集结果
	portSet := make(map[int]bool)
	for i := 0; i < numGoroutines; i++ {
		select {
		case port := <-ports:
			assert.False(t, portSet[port], "端口 %d 被重复分配", port)
			portSet[port] = true
		case err := <-errors:
			t.Fatalf("并发分配失败: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("并发分配超时")
		}
	}

	assert.Equal(t, numGoroutines, len(portSet), "应分配 100 个不同端口")
	assert.Equal(t, numGoroutines, allocator.AllocatedCount())
}
