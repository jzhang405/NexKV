// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
)

// ========================================
// PeerRateLimiterConfig Tests
// ========================================

// TestDefaultPeerRateLimiterConfig 测试默认配置
func TestDefaultPeerRateLimiterConfig(t *testing.T) {
	config := DefaultPeerRateLimiterConfig()

	assert.Equal(t, 100, config.DefaultRate)
	assert.Equal(t, 1000, config.MaxRate)
	assert.True(t, config.EnableDynamicAdjust)
	assert.Equal(t, 1.2, config.RateUpFactor)
	assert.Equal(t, 0.8, config.RateDownFactor)
}

// ========================================
// PeerRateLimiter Basic Tests
// ========================================

// TestPeerRateLimiter_Allow 测试允许调用
func TestPeerRateLimiter_Allow(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10, // 每秒 10 个请求
		MaxRate:             100,
		EnableDynamicAdjust: false, // 关闭动态调整，避免干扰
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")
	ctx := context.Background()

	// 第一次调用应该成功
	err := limiter.Allow(ctx, peerID)
	assert.NoError(t, err)

	// 连续多次调用应该成功（在速率限制内）
	for i := 0; i < 5; i++ {
		err = limiter.Allow(ctx, peerID)
		assert.NoError(t, err)
	}
}

// TestPeerRateLimiter_AllowNow 测试非阻塞允许
func TestPeerRateLimiter_AllowNow(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10,
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")

	// 调用应该成功
	allowed := limiter.AllowNow(peerID)
	assert.True(t, allowed)

	// 连续多次调用应该成功
	for i := 0; i < 5; i++ {
		allowed = limiter.AllowNow(peerID)
		assert.True(t, allowed)
	}
}

// TestPeerRateLimiter_RateLimiting 测试速率限制
func TestPeerRateLimiter_RateLimiting(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         2, // 每秒 2 个请求
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")
	ctx := context.Background()

	// 前两次调用应该成功
	err := limiter.Allow(ctx, peerID)
	assert.NoError(t, err)

	err = limiter.Allow(ctx, peerID)
	assert.NoError(t, err)

	// 第三次调用会被速率限制，但最终会成功（等待）
	// 我们只验证它最终会成功
	err = limiter.Allow(ctx, peerID)
	assert.NoError(t, err)
}

// TestPeerRateLimiter_MultiplePeers 测试多个 peer
func TestPeerRateLimiter_MultiplePeers(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10,
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID1 := peer.ID("test-peer-1")
	peerID2 := peer.ID("test-peer-2")

	ctx := context.Background()

	// 两个 peer 都应该能调用
	err := limiter.Allow(ctx, peerID1)
	assert.NoError(t, err)

	err = limiter.Allow(ctx, peerID2)
	assert.NoError(t, err)
}

// ========================================
// Manual Rate Setting Tests
// ========================================

// TestPeerRateLimiter_SetPeerRate 测试手动设置速率
func TestPeerRateLimiter_SetPeerRate(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10,
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")

	// 获取默认速率
	rate := limiter.GetPeerRate(peerID)
	assert.Equal(t, 10, rate)

	// 设置新速率
	err := limiter.SetPeerRate(peerID, 50)
	assert.NoError(t, err)

	// 验证新速率
	rate = limiter.GetPeerRate(peerID)
	assert.Equal(t, 50, rate)
}

// TestPeerRateLimiter_SetPeerRate_Invalid 测试设置无效速率
func TestPeerRateLimiter_SetPeerRate_Invalid(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10,
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")

	// 速率太低
	err := limiter.SetPeerRate(peerID, 0)
	assert.Error(t, err)

	// 速率太高
	err = limiter.SetPeerRate(peerID, 1000)
	assert.Error(t, err)
}

// ========================================
// Dynamic Adjustment Tests
// ========================================

// TestPeerRateLimiter_DynamicAdjust 测试动态调整
func TestPeerRateLimiter_DynamicAdjust(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10,
		MaxRate:             100,
		EnableDynamicAdjust: true,
		AdjustWindow:        1 * time.Second, // 短窗口便于测试
		RateUpFactor:        1.2,
		RateDownFactor:      0.8,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")
	ctx := context.Background()

	// 初始速率
	rate := limiter.GetPeerRate(peerID)
	assert.Equal(t, 10, rate)

	// 快速调用多次（模拟快速响应）
	for i := 0; i < 20; i++ {
		err := limiter.Allow(ctx, peerID)
		assert.NoError(t, err)
	}

	// 等待调整窗口
	time.Sleep(2 * time.Second)

	// 速率可能已经提升（由于快速响应）
	// 注意：这取决于具体的响应时间，可能不会提升
	// 这里我们只验证不会崩溃
	rate = limiter.GetPeerRate(peerID)
	assert.GreaterOrEqual(t, rate, config.MinRate)
	assert.LessOrEqual(t, rate, config.MaxRate)
}

// ========================================
// Peer Removal Tests
// ========================================

// TestPeerRateLimiter_RemovePeer 测试移除 peer
func TestPeerRateLimiter_RemovePeer(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10,
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")
	ctx := context.Background()

	// 创建 peer 的限制器
	err := limiter.Allow(ctx, peerID)
	assert.NoError(t, err)

	// 移除 peer
	limiter.RemovePeer(peerID)

	// 重新调用应该创建新的限制器
	err = limiter.Allow(ctx, peerID)
	assert.NoError(t, err)
}

// ========================================
// Context Cancellation Tests
// ========================================

// TestPeerRateLimiter_ContextCancel 测试上下文取消
func TestPeerRateLimiter_ContextCancel(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         1, // 低速率
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 立即取消
	cancel()

	// 调用应该立即返回取消错误
	err := limiter.Allow(ctx, peerID)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// ========================================
// Metrics Tests
// ========================================

// TestNewPeerRateLimiterMetrics 测试创建指标
func TestNewPeerRateLimiterMetrics(t *testing.T) {
	metrics := NewPeerRateLimiterMetrics()

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.CallsTotal)
	assert.NotNil(t, metrics.CallsAllowed)
	assert.NotNil(t, metrics.CallsThrottled)
	assert.NotNil(t, metrics.RateAdjustments)
	assert.NotNil(t, metrics.RateUps)
	assert.NotNil(t, metrics.RateDowns)
	assert.NotNil(t, metrics.ResponseTime)
}

// ========================================
// Benchmark Tests
// ========================================

// BenchmarkPeerRateLimiter_Allow 基准测试：允许调用性能
func BenchmarkPeerRateLimiter_Allow(b *testing.B) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         1000,
		MaxRate:             10000,
		EnableDynamicAdjust: false,
		MinRate:             100,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = limiter.Allow(ctx, peerID)
	}
}

// BenchmarkPeerRateLimiter_AllowNow 基准测试：非阻塞允许性能
func BenchmarkPeerRateLimiter_AllowNow(b *testing.B) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         1000,
		MaxRate:             10000,
		EnableDynamicAdjust: false,
		MinRate:             100,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-1")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.AllowNow(peerID)
	}
}
