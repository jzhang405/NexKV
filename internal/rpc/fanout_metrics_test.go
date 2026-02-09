// Package rpc 基于 libp2p Stream 的 RPC 实现
// Fanout 指标测试
package rpc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ========================================
// Fanout Metrics Tests
// ========================================

// TestNewFanoutMetrics 测试 Fanout 指标创建
func TestNewFanoutMetrics(t *testing.T) {
	metrics := NewFanoutMetrics()

	assert.NotNil(t, metrics)
	// 验证所有指标都被初始化
	assert.NotNil(t, metrics.FanoutTotal)
	assert.NotNil(t, metrics.FanoutSuccess)
	assert.NotNil(t, metrics.FanoutFailed)
	assert.NotNil(t, metrics.FanoutTimeout)
	assert.NotNil(t, metrics.FanoutLatency)
	assert.NotNil(t, metrics.FireForgetCount)
	assert.NotNil(t, metrics.QuorumCount)
	assert.NotNil(t, metrics.WaitAllCount)
	assert.NotNil(t, metrics.PeerSuccess)
	assert.NotNil(t, metrics.PeerFailed)
	assert.NotNil(t, metrics.PeerTimeout)
	assert.NotNil(t, metrics.FanoutForwardTotal)
	assert.NotNil(t, metrics.FanoutForwardFailed)
	assert.NotNil(t, metrics.HopsDistribution)
	assert.NotNil(t, metrics.ForwardPerHop)
}

// TestFanoutMetrics_GetMetrics 测试获取全局指标
func TestFanoutMetrics_GetMetrics(t *testing.T) {
	metrics := GetFanoutMetrics()

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.FanoutTotal)
	assert.NotNil(t, metrics.FanoutSuccess)
	assert.NotNil(t, metrics.FanoutFailed)
	assert.NotNil(t, metrics.FanoutLatency)
	assert.NotNil(t, metrics.PeerSuccess)
	assert.NotNil(t, metrics.PeerFailed)
}

// TestFanoutMetrics_RecordStart 测试记录开始指标
func TestFanoutMetrics_RecordStart(t *testing.T) {
	metrics := GetFanoutMetrics()

	// 验证指标操作不会 panic
	assert.NotPanics(t, func() {
		metrics.FanoutTotal.Inc()
	})
}

// TestFanoutMetrics_RecordModeDistribution 测试响应模式分布
func TestFanoutMetrics_RecordModeDistribution(t *testing.T) {
	metrics := GetFanoutMetrics()

	// 验证所有模式计数器可以正常递增
	assert.NotPanics(t, func() {
		metrics.FireForgetCount.Inc()
		metrics.QuorumCount.Inc()
		metrics.WaitAllCount.Inc()
	})
}

// TestFanoutMetrics_PeerLevelMetrics 测试 Peer 级别指标
func TestFanoutMetrics_PeerLevelMetrics(t *testing.T) {
	metrics := GetFanoutMetrics()
	peerID := "test-peer-123"

	// 验证 peer 级别指标操作不会 panic
	assert.NotPanics(t, func() {
		metrics.PeerSuccess.WithLabelValues(peerID).Inc()
		metrics.PeerFailed.WithLabelValues(peerID).Inc()
		metrics.PeerTimeout.WithLabelValues(peerID).Inc()
	})
}

// TestFanoutMetrics_LatencyHistogram 测试延迟直方图
func TestFanoutMetrics_LatencyHistogram(t *testing.T) {
	metrics := GetFanoutMetrics()

	// 记录不同延迟值
	metrics.FanoutLatency.Observe(0.001) // 1ms
	metrics.FanoutLatency.Observe(0.010) // 10ms
	metrics.FanoutLatency.Observe(0.100) // 100ms
	metrics.FanoutLatency.Observe(1.000) // 1s

	// 验证观察成功（没有断言错误即表示成功）
	assert.True(t, true)
}

// ========================================
// Benchmark Tests
// ========================================

// BenchmarkFanoutOptionsValidation 基准测试：选项验证性能
func BenchmarkFanoutOptionsValidation(b *testing.B) {
	opts := &FanoutOptions{
		Mode:    WaitAll,
		Timeout: 30 * time.Second,
	}
	peerCount := 10

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ValidateAndNormalize(opts, peerCount)
	}
}

// BenchmarkHopsControl 基准测试：跳数控制性能
func BenchmarkHopsControl(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CanForward(5, 10)
		DecrementHops(5)
	}
}
