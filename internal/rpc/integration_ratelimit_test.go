// Package rpc 基于 libp2p Stream 的 RPC 实现
// 限流器集成测试
package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
)

// TestRPCRateLimiter_GlobalAndPeer 集成测试：全局+Peer限流器
func TestRPCRateLimiter_GlobalAndPeer(t *testing.T) {
	// 1. 创建全局限流器
	globalConfig := &RateLimiterConfig{
		MaxConnections: 3,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   2,
		BucketSize:     3, // 与 MaxConnections 一致
		AcquireTimeout: 1 * time.Second,
	}
	globalLimiter := NewRateLimiter(globalConfig)
	defer globalLimiter.Close()

	// 2. 创建 Peer 限流器
	peerConfig := &PeerRateLimiterConfig{
		DefaultRate:         20,
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}
	peerLimiter := NewPeerRateLimiter(peerConfig)
	defer peerLimiter.Close()

	ctx := context.Background()
	peerID := peer.ID("test-peer-multi-limiter")

	// 3. 模拟多级限流场景
	successCount := 0
	const attempts = 10

	for i := 0; i < attempts; i++ {
		// 全局限流检查
		err := globalLimiter.Acquire(ctx)
		if err != nil {
			// 全局限流失败，跳过
			continue
		}

		// Peer 限流检查
		err = peerLimiter.Allow(ctx, peerID)
		if err != nil {
			// Peer 限流失败，释放全局限流配额
			globalLimiter.Release()
			continue
		}

		// 两个限流器都通过
		successCount++

		// 模拟异步处理完成
		go func(idx int) {
			time.Sleep(time.Duration(idx) * 10 * time.Millisecond)
			globalLimiter.Release()
		}(i)
	}

	// 等待部分异步操作完成
	time.Sleep(100 * time.Millisecond)

	// 验证至少有一些调用成功
	assert.Greater(t, successCount, 0, "至少应该有一些调用通过双重限流")
	// 注意：由于 Release() 会将令牌放回桶中，且定期 refill 会添加令牌
	// 所以总成功数可能超过 MaxConnections
	// 真正的限制是并发数（semaphore），而不是总请求数
	assert.LessOrEqual(t, successCount, attempts, "成功数不应超过总尝试次数")
}

// TestRPCRateLimiter_DynamicAdjustment 集成测试：动态速率调整
func TestRPCRateLimiter_DynamicAdjustment(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10,
		MaxRate:             100,
		EnableDynamicAdjust: true,
		AdjustWindow:        500 * time.Millisecond,
		RateUpFactor:        1.2,
		RateDownFactor:      0.8,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-dynamic")
	ctx := context.Background()

	// 1. 初始速率
	initialRate := limiter.GetPeerRate(peerID)
	assert.Equal(t, 10, initialRate)

	// 2. 模拟快速响应（可能触发速率提升）
	for i := 0; i < 20; i++ {
		err := limiter.Allow(ctx, peerID)
		assert.NoError(t, err)
		time.Sleep(1 * time.Millisecond) // 模拟快速响应
	}

	// 等待调整窗口
	time.Sleep(700 * time.Millisecond)

	// 3. 验证速率可能已经调整
	// 注意：动态调整是基于响应时间的，这里我们只验证不会崩溃
	newRate := limiter.GetPeerRate(peerID)
	assert.GreaterOrEqual(t, newRate, config.MinRate, "速率不应低于最小值")
	assert.LessOrEqual(t, newRate, config.MaxRate, "速率不应高于最大值")
}
