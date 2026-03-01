// Package service 测试领域服务
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ==========================================
// OperationStatus 测试
// ==========================================

// TestOperationStatus_IsTerminal 测试终态判断
func TestOperationStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		name     string
		status   OperationStatus
		expected bool
	}{
		{"Pending is not terminal", StatusPending, false},
		{"Running is not terminal", StatusRunning, false},
		{"Completed is terminal", StatusCompleted, true},
		{"Failed is terminal", StatusFailed, true},
		{"Canceled is terminal", StatusCanceled, true},
		{"Discarded is terminal", StatusDiscarded, true},
		{"Timeout is terminal", StatusTimeout, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.status.IsTerminal()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestOperationStatus_String 测试状态字符串表示
func TestOperationStatus_String(t *testing.T) {
	tests := []struct {
		name     string
		status   OperationStatus
		expected string
	}{
		{"Pending", StatusPending, "pending"},
		{"Running", StatusRunning, "running"},
		{"Completed", StatusCompleted, "completed"},
		{"Failed", StatusFailed, "failed"},
		{"Canceled", StatusCanceled, "canceled"},
		{"Discarded", StatusDiscarded, "discarded"},
		{"Timeout", StatusTimeout, "timeout"},
		{"Unknown status", OperationStatus(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.status.String()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// ==========================================
// BroadcastConfig 测试
// ==========================================

// TestBroadcastConfig_GetCallbacks_NilConfig 测试 nil 配置
func TestBroadcastConfig_GetCallbacks_NilConfig(t *testing.T) {
	var config *BroadcastConfig
	callbacks := config.GetCallbacks()
	assert.Nil(t, callbacks)
}

// TestBroadcastConfig_GetCallbacks_EmptyCallbacks 测试空回调列表
func TestBroadcastConfig_GetCallbacks_EmptyCallbacks(t *testing.T) {
	config := &BroadcastConfig{}
	callbacks := config.GetCallbacks()
	assert.Nil(t, callbacks)
}

// TestBroadcastConfig_GetCallbacks_WithCallbacks 测试获取回调列表
func TestBroadcastConfig_GetCallbacks_WithCallbacks(t *testing.T) {
	listener1 := &NoOpListener{}
	listener2 := &NoOpListener{}

	config := &BroadcastConfig{
		callbacks: []BroadcastListener{listener1, listener2},
	}

	callbacks := config.GetCallbacks()

	// 验证返回的副本
	assert.Len(t, callbacks, 2)
	assert.Equal(t, listener1, callbacks[0])
	assert.Equal(t, listener2, callbacks[1])

	// 修改返回的切片不应影响原配置
	callbacks[0] = nil
	originalCallbacks := config.GetCallbacks()
	assert.Equal(t, listener1, originalCallbacks[0])
}

// TestBroadcastConfig_AddCallback_NilConfig 测试 nil 配置添加回调
func TestBroadcastConfig_AddCallback_NilConfig(t *testing.T) {
	var config *BroadcastConfig
	listener := &NoOpListener{}

	// 不应 panic
	config.AddCallback(listener)
	// nil 配置不会添加回调
	assert.Nil(t, config)
}

// TestBroadcastConfig_AddCallback_Normal 测试添加回调
func TestBroadcastConfig_AddCallback_Normal(t *testing.T) {
	config := &BroadcastConfig{}
	listener := &NoOpListener{}

	config.AddCallback(listener)

	callbacks := config.GetCallbacks()
	assert.Len(t, callbacks, 1)
	assert.Equal(t, listener, callbacks[0])
}

// TestBroadcastConfig_AddCallback_Multiple 测试添加多个回调
func TestBroadcastConfig_AddCallback_Multiple(t *testing.T) {
	config := &BroadcastConfig{}
	listener1 := &NoOpListener{}
	listener2 := &NoOpListener{}

	config.AddCallback(listener1)
	config.AddCallback(listener2)

	callbacks := config.GetCallbacks()
	assert.Len(t, callbacks, 2)
	assert.Equal(t, listener1, callbacks[0])
	assert.Equal(t, listener2, callbacks[1])
}

// ==========================================
// RPCAsyncConfig 测试
// ==========================================

// TestRPCAsyncConfig_GetCallTimeout_Explicit 测试获取显式设置的单播超时
func TestRPCAsyncConfig_GetCallTimeout_Explicit(t *testing.T) {
	config := &RPCAsyncConfig{
		CallTimeoutMs:    5000,
		DefaultTimeoutMs: 30000,
	}

	timeout := config.GetCallTimeout()
	assert.Equal(t, int64(5000), timeout)
}

// TestRPCAsyncConfig_GetCallTimeout_Fallback 测试回退到默认超时
func TestRPCAsyncConfig_GetCallTimeout_Fallback(t *testing.T) {
	config := &RPCAsyncConfig{
		CallTimeoutMs:    0,
		DefaultTimeoutMs: 30000,
	}

	timeout := config.GetCallTimeout()
	assert.Equal(t, int64(30000), timeout)
}

// TestRPCAsyncConfig_GetCallTimeout_Zero 测试两者都为零的情况
func TestRPCAsyncConfig_GetCallTimeout_Zero(t *testing.T) {
	config := &RPCAsyncConfig{
		CallTimeoutMs:    0,
		DefaultTimeoutMs: 0,
	}

	timeout := config.GetCallTimeout()
	assert.Equal(t, int64(0), timeout)
}

// TestRPCAsyncConfig_GetBroadcastTimeout_Explicit 测试获取显式设置的广播超时
func TestRPCAsyncConfig_GetBroadcastTimeout_Explicit(t *testing.T) {
	config := &RPCAsyncConfig{
		BroadcastTimeoutMs: 60000,
		DefaultTimeoutMs:   30000,
	}

	timeout := config.GetBroadcastTimeout()
	assert.Equal(t, int64(60000), timeout)
}

// TestRPCAsyncConfig_GetBroadcastTimeout_Fallback 测试回退到默认超时
func TestRPCAsyncConfig_GetBroadcastTimeout_Fallback(t *testing.T) {
	config := &RPCAsyncConfig{
		BroadcastTimeoutMs: 0,
		DefaultTimeoutMs:   30000,
	}

	timeout := config.GetBroadcastTimeout()
	assert.Equal(t, int64(30000), timeout)
}

// TestRPCAsyncConfig_GetBroadcastTimeout_Zero 测试两者都为零的情况
func TestRPCAsyncConfig_GetBroadcastTimeout_Zero(t *testing.T) {
	config := &RPCAsyncConfig{
		BroadcastTimeoutMs: 0,
		DefaultTimeoutMs:   0,
	}

	timeout := config.GetBroadcastTimeout()
	assert.Equal(t, int64(0), timeout)
}

// ==========================================
// DefaultRPCAsyncConfig 测试
// ==========================================

// TestDefaultRPCAsyncConfig 测试默认配置
func TestDefaultRPCAsyncConfig(t *testing.T) {
	config := DefaultRPCAsyncConfig()

	assert.NotNil(t, config)
	assert.Equal(t, int64(30000), config.CallTimeoutMs)
	assert.Equal(t, int64(60000), config.BroadcastTimeoutMs)
	assert.Equal(t, int64(30000), config.DefaultTimeoutMs)
	assert.Equal(t, 1000, config.MaxConcurrentCalls)
}

// TestDefaultRPCAsyncConfig_GetCallTimeout 测试默认配置的单播超时
func TestDefaultRPCAsyncConfig_GetCallTimeout(t *testing.T) {
	config := DefaultRPCAsyncConfig()

	timeout := config.GetCallTimeout()
	assert.Equal(t, int64(30000), timeout)
}

// TestDefaultRPCAsyncConfig_GetBroadcastTimeout 测试默认配置的广播超时
func TestDefaultRPCAsyncConfig_GetBroadcastTimeout(t *testing.T) {
	config := DefaultRPCAsyncConfig()

	timeout := config.GetBroadcastTimeout()
	assert.Equal(t, int64(60000), timeout)
}

// ==========================================
// AsyncBroadcastResult 测试
// ==========================================

// TestAsyncBroadcastResult_Fields 测试异步广播结果结构
func TestAsyncBroadcastResult_Fields(t *testing.T) {
	result := AsyncBroadcastResult{
		Responses: []PeerResponse{
			{Peer: "node1", Response: nil},
			{Peer: "node2", Response: nil},
		},
		Errors: []PeerError{
			{Peer: "node3", Error: assert.AnError},
		},
		Total:        3,
		SuccessCount: 2,
	}

	assert.Len(t, result.Responses, 2)
	assert.Len(t, result.Errors, 1)
	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 2, result.SuccessCount)
}

// ==========================================
// QuorumResult 测试
// ==========================================

// TestQuorumResult_Reached 测试 Quorum 达到
func TestQuorumResult_Reached(t *testing.T) {
	result := QuorumResult{
		Responses: []PeerResponse{
			{Peer: "node1", Response: nil},
			{Peer: "node2", Response: nil},
		},
		Quorum:  2,
		Reached: true,
	}

	assert.Len(t, result.Responses, 2)
	assert.Equal(t, 2, result.Quorum)
	assert.True(t, result.Reached)
}

// TestQuorumResult_NotReached 测试 Quorum 未达到
func TestQuorumResult_NotReached(t *testing.T) {
	result := QuorumResult{
		Responses: []PeerResponse{
			{Peer: "node1", Response: nil},
		},
		Quorum:  2,
		Reached: false,
	}

	assert.Len(t, result.Responses, 1)
	assert.Equal(t, 2, result.Quorum)
	assert.False(t, result.Reached)
}
