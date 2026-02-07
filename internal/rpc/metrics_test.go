// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

// ========================================
// RPCMetrics Tests
// ========================================

// TestNewRPCMetrics 测试 RPC 指标创建
func TestNewRPCMetrics(t *testing.T) {
	metrics := NewRPCMetrics()

	assert.NotNil(t, metrics)
	// 验证所有指标都被初始化
	assert.NotNil(t, metrics.StreamsActive)
	assert.NotNil(t, metrics.StreamsCreated)
	assert.NotNil(t, metrics.StreamsReused)
	assert.NotNil(t, metrics.StreamsClosed)
	assert.NotNil(t, metrics.CallTotal)
	assert.NotNil(t, metrics.CallSuccess)
	assert.NotNil(t, metrics.CallErrors)
	assert.NotNil(t, metrics.CallLatency)
	assert.NotNil(t, metrics.BatchCallsTotal)
	assert.NotNil(t, metrics.ConnectionsActive)
}

// ========================================
// Stream Metrics Tests
// ========================================

// TestRPCMetrics_RecordStreamCreated 测试记录 Stream 创建
func TestRPCMetrics_RecordStreamCreated(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	// 记录 Stream 创建
	metrics.RecordStreamCreated(peerID.String())

	// 验证指标递增
	assert.Greater(t, getCounterValue(metrics.StreamsCreated), float64(0))
	assert.Equal(t, getGaugeValue(metrics.StreamsActive), float64(1))
}

// TestRPCMetrics_RecordStreamReused 测试记录 Stream 复用
func TestRPCMetrics_RecordStreamReused(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	// 记录 Stream 复用
	metrics.RecordStreamReused(peerID.String())

	// 验证指标递增
	assert.Greater(t, getCounterValue(metrics.StreamsReused), float64(0))
	assert.Equal(t, getGaugeValue(metrics.StreamsActive), float64(1))
}

// TestRPCMetrics_RecordStreamClosed 测试记录 Stream 关闭
func TestRPCMetrics_RecordStreamClosed(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	// 先创建一个 Stream
	metrics.RecordStreamCreated(peerID.String())

	// 关闭 Stream
	metrics.RecordStreamClosed(peerID.String())

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.StreamsClosed), float64(0))
	assert.Equal(t, getGaugeValue(metrics.StreamsActive), float64(0))
}

// TestRPCMetrics_StreamLifecycle 测试 Stream 完整生命周期
func TestRPCMetrics_StreamLifecycle(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	// 创建 Stream
	metrics.RecordStreamCreated(peerID.String())
	assert.Equal(t, float64(1), getGaugeValue(metrics.StreamsActive))

	// 复用 Stream（另一个 peer）
	peerID2 := peer.ID("test-peer-2")
	metrics.RecordStreamReused(peerID2.String())
	assert.Equal(t, float64(2), getGaugeValue(metrics.StreamsActive))

	// 关闭第一个 Stream
	metrics.RecordStreamClosed(peerID.String())
	assert.Equal(t, float64(1), getGaugeValue(metrics.StreamsActive))

	// 关闭第二个 Stream
	metrics.RecordStreamClosed(peerID2.String())
	assert.Equal(t, float64(0), getGaugeValue(metrics.StreamsActive))
}

// ========================================
// Call Metrics Tests
// ========================================

// TestRPCMetrics_RecordCallStart 测试记录调用开始
func TestRPCMetrics_RecordCallStart(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	timer := metrics.RecordCallStart(peerID.String(), "test.method")

	assert.NotNil(t, timer)

	// 停止计时
	timer.Stop()

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.CallTotal), float64(0))
	assert.Greater(t, getCounterValue(metrics.CallSuccess), float64(0))
}

// TestRPCMetrics_RecordCallSuccess 测试记录调用成功
func TestRPCMetrics_RecordCallSuccess(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")
	duration := 100 * time.Millisecond

	metrics.RecordCallSuccess(peerID.String(), "test.method", duration)

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.CallSuccess), float64(0))
}

// TestRPCMetrics_RecordCallError 测试记录调用错误
func TestRPCMetrics_RecordCallError(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")
	duration := 50 * time.Millisecond
	err := errors.New("test error")

	metrics.RecordCallError(peerID.String(), "test.method", err, duration)

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.CallErrors.WithLabelValues("unknown")), float64(0))
}

// TestRPCMetrics_CallWithTimeout 测试记录调用超时
func TestRPCMetrics_CallWithTimeout(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")
	duration := 5 * time.Second
	err := context.DeadlineExceeded

	metrics.RecordCallError(peerID.String(), "test.method", err, duration)

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.CallTimeout), float64(0))
	assert.Greater(t, getCounterValue(metrics.CallErrors.WithLabelValues("deadline_exceeded")), float64(0))
}

// TestStartTimer_StopWithError 测试计时器停止并记录错误
func TestStartTimer_StopWithError(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	timer := metrics.RecordCallStart(peerID.String(), "test.method")

	// 模拟工作
	time.Sleep(10 * time.Millisecond)

	// 停止并记录错误
	err := errors.New("test error")
	timer.StopWithError(err)

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.CallErrors.WithLabelValues("unknown")), float64(0))
}

// ========================================
// Batch Metrics Tests
// ========================================

// TestRPCMetrics_RecordBatchCall 测试记录 Batch 调用
func TestRPCMetrics_RecordBatchCall(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")
	size := 10
	duration := 200 * time.Millisecond

	metrics.RecordBatchCall(peerID.String(), size, duration, nil)

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.BatchCallsTotal), float64(0))
}

// TestRPCMetrics_RecordBatchCallWithError 测试记录 Batch 调用错误
func TestRPCMetrics_RecordBatchCallWithError(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")
	size := 5
	duration := 100 * time.Millisecond
	err := errors.New("batch error")

	metrics.RecordBatchCall(peerID.String(), size, duration, err)

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.BatchCallErrors), float64(0))
}

// ========================================
// Connection Metrics Tests
// ========================================

// TestRPCMetrics_RecordConnectionOpened 测试记录连接打开
func TestRPCMetrics_RecordConnectionOpened(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	metrics.RecordConnectionOpened(peerID.String())

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.ConnectionsTotal), float64(0))
	assert.Equal(t, float64(1), getGaugeValue(metrics.ConnectionsActive))
}

// TestRPCMetrics_RecordConnectionClosed 测试记录连接关闭
func TestRPCMetrics_RecordConnectionClosed(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	// 打开连接
	metrics.RecordConnectionOpened(peerID.String())

	// 关闭连接
	metrics.RecordConnectionClosed(peerID.String())

	// 验证指标
	assert.Equal(t, float64(0), getGaugeValue(metrics.ConnectionsActive))
}

// TestRPCMetrics_RecordConnectionFailed 测试记录连接失败
func TestRPCMetrics_RecordConnectionFailed(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")
	err := errors.New("connection failed")

	metrics.RecordConnectionFailed(peerID.String(), err)

	// 验证指标
	assert.Greater(t, getCounterValue(metrics.ConnectionsFailed), float64(0))
}

// ========================================
// Error Type Tests
// ========================================

// TestGetErrorType 测试错误类型提取
func TestGetErrorType(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "空错误",
			err:      nil,
			expected: "none",
		},
		{
			name:     "RPC 超时错误",
			err:      NewRPCError(ErrCodeTimeout, "timeout"),
			expected: "timeout",
		},
		{
			name:     "RPC Peer 不可用错误",
			err:      NewRPCError(ErrCodePeerUnavailable, "peer unavailable"),
			expected: "peer_unavailable",
		},
		{
			name:     "Context 取消错误",
			err:      context.Canceled,
			expected: "canceled",
		},
		{
			name:     "Context 超时错误",
			err:      context.DeadlineExceeded,
			expected: "deadline_exceeded",
		},
		{
			name:     "未知错误",
			err:      errors.New("unknown error"),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getErrorType(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ========================================
// Global Metrics Tests
// ========================================

// TestGetRPCMetrics 测试获取全局指标
func TestGetRPCMetrics(t *testing.T) {
	metrics := GetRPCMetrics()

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.StreamsActive)
	assert.NotNil(t, metrics.CallTotal)
	assert.NotNil(t, metrics.BatchCallsTotal)
}

// ========================================
// Helper Functions
// ========================================

// getCounterValue 获取 Counter 的当前值
func getCounterValue(counter prometheus.Counter) float64 {
	var metric dto.Metric
	if err := counter.Write(&metric); err != nil {
		return 0
	}
	return metric.Counter.GetValue()
}

// getGaugeValue 获取 Gauge 的当前值
func getGaugeValue(gauge prometheus.Gauge) float64 {
	var metric dto.Metric
	if err := gauge.Write(&metric); err != nil {
		return 0
	}
	return metric.Gauge.GetValue()
}

// ========================================
// Integration Tests
// ========================================

// TestRPCMetrics_Integration 测试指标集成
func TestRPCMetrics_Integration(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID := peer.ID("test-peer-1")

	// 模拟完整的 RPC 调用流程
	// 1. 打开连接
	metrics.RecordConnectionOpened(peerID.String())

	// 2. 创建 Stream
	metrics.RecordStreamCreated(peerID.String())

	// 3. 发起 RPC 调用
	timer := metrics.RecordCallStart(peerID.String(), "test.method")

	// 4. 模拟工作
	time.Sleep(10 * time.Millisecond)

	// 5. 调用成功
	timer.Stop()

	// 6. 关闭 Stream
	metrics.RecordStreamClosed(peerID.String())

	// 7. 关闭连接
	metrics.RecordConnectionClosed(peerID.String())

	// 验证：这里我们只验证不会 panic，实际值需要通过 Prometheus 端点查询
	assert.NotNil(t, metrics)
}

// TestRPCMetrics_PeerLevelMetrics 测试 Peer 级别指标
func TestRPCMetrics_PeerLevelMetrics(t *testing.T) {
	metrics := NewRPCMetrics()
	peerID1 := peer.ID("test-peer-1")
	peerID2 := peer.ID("test-peer-2")

	// 为不同 peer 记录调用
	metrics.RecordCallStart(peerID1.String(), "method1").Stop()
	metrics.RecordCallStart(peerID2.String(), "method2").Stop()

	// 为不同 peer 记录错误
	metrics.RecordCallError(peerID1.String(), "method1", errors.New("error1"), 0)
	metrics.RecordCallError(peerID2.String(), "method2", errors.New("error2"), 0)

	// 验证：这里我们只验证不会 panic
	assert.NotNil(t, metrics.PeerCallsTotal)
	assert.NotNil(t, metrics.PeerCallErrors)
	assert.NotNil(t, metrics.PeerCallLatency)
}
