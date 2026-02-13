// Package consistency 提供 2PC 强一致性协调器实现
//
// 强制优化 7.3: Gossip 消息限流器测试
package consistency

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewGossipRateLimiter 测试创建限流器
func TestNewGossipRateLimiter(t *testing.T) {
	// 默认配置
	limiter := NewGossipRateLimiter(nil)
	require.NotNil(t, limiter)
	require.Equal(t, 100.0, limiter.rate)
	require.Equal(t, 200, limiter.burst)
	require.Equal(t, float64(200), limiter.tokens) // 初始填满

	// 自定义配置
	config := &RateLimiterConfig{
		Rate:         50,
		Burst:        100,
		PerNodeRate:  10,
		PerNodeBurst: 25,
		Enabled:      true,
	}
	limiter2 := NewGossipRateLimiter(config)
	require.Equal(t, 50.0, limiter2.rate)
	require.Equal(t, 100, limiter2.burst)
}

// TestGossipRateLimiter_Allow 测试全局限流
func TestGossipRateLimiter_Allow(t *testing.T) {
	config := &RateLimiterConfig{
		Rate:         10, // 每秒 10 个令牌
		Burst:        5,  // 突发容量 5
		PerNodeRate:  100,
		PerNodeBurst: 100,
		Enabled:      true,
	}
	limiter := NewGossipRateLimiter(config)

	// 初始有 5 个令牌
	for i := 0; i < 5; i++ {
		require.True(t, limiter.Allow(), "第 %d 次应该允许", i+1)
	}

	// 令牌耗尽，第 6 次应该被拒绝
	require.False(t, limiter.Allow(), "第 6 次应该被拒绝")

	// 等待令牌补充
	time.Sleep(200 * time.Millisecond) // 0.2s * 10/s = 2 个令牌

	// 现在应该有 2 个令牌
	require.True(t, limiter.Allow(), "等待后应该允许")
	require.True(t, limiter.Allow(), "等待后应该允许")
	require.False(t, limiter.Allow(), "再次应该被拒绝")
}

// TestGossipRateLimiter_AllowForNode 测试节点级限流
func TestGossipRateLimiter_AllowForNode(t *testing.T) {
	config := &RateLimiterConfig{
		Rate:         1000, // 全局速率足够高
		Burst:        1000,
		PerNodeRate:  5,  // 每节点每秒 5 个令牌
		PerNodeBurst: 10, // 每节点突发容量 10
		Enabled:      true,
	}
	limiter := NewGossipRateLimiter(config)

	// 对单个节点发送 - 初始有 10 个令牌，消耗后应该拒绝
	nodeID := "node-1"
	for i := 0; i < 10; i++ {
		require.True(t, limiter.AllowForNode(nodeID), "第 %d 次应该允许", i+1)
	}

	// 节点令牌耗尽（PerNodeBurst=10，已用完）
	require.False(t, limiter.AllowForNode(nodeID), "节点令牌耗尽应该被拒绝")

	// 对另一个节点应该可以发送（每个节点独立的令牌桶）
	require.True(t, limiter.AllowForNode("node-2"), "另一个节点应该允许")
}

// TestGossipRateLimiter_AllowN 测试批量限流
func TestGossipRateLimiter_AllowN(t *testing.T) {
	config := &RateLimiterConfig{
		Rate:         10,
		Burst:        20,
		PerNodeRate:  100,
		PerNodeBurst: 100,
		Enabled:      true,
	}
	limiter := NewGossipRateLimiter(config)

	// 请求 10 个令牌
	require.True(t, limiter.AllowN(10), "请求 10 个应该允许")

	// 再请求 10 个
	require.True(t, limiter.AllowN(10), "请求 10 个应该允许")

	// 再请求 1 个应该失败
	require.False(t, limiter.AllowN(1), "令牌耗尽应该被拒绝")
}

// TestGossipRateLimiter_WaitForAllow 测试等待限流
func TestGossipRateLimiter_WaitForAllow(t *testing.T) {
	config := &RateLimiterConfig{
		Rate:         100, // 每秒 100 个令牌
		Burst:        2,   // 突发容量只有 2
		PerNodeRate:  100,
		PerNodeBurst: 100,
		Enabled:      true,
	}
	limiter := NewGossipRateLimiter(config)

	// 消耗掉初始令牌
	require.True(t, limiter.Allow())
	require.True(t, limiter.Allow())

	// 等待应该成功（因为会补充令牌）
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := limiter.WaitForAllow(ctx)
	require.True(t, result, "等待后应该允许")
}

// TestGossipRateLimiter_WaitForNodeAllow 测试节点级等待限流
func TestGossipRateLimiter_WaitForNodeAllow(t *testing.T) {
	config := &RateLimiterConfig{
		Rate:         1000,
		Burst:        1000,
		PerNodeRate:  100,
		PerNodeBurst: 2, // 节点突发容量只有 2
		Enabled:      true,
	}
	limiter := NewGossipRateLimiter(config)

	nodeID := "node-1"

	// 消耗掉初始令牌
	require.True(t, limiter.AllowForNode(nodeID))
	require.True(t, limiter.AllowForNode(nodeID))

	// 等待应该成功
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result := limiter.WaitForNodeAllow(ctx, nodeID)
	require.True(t, result, "等待后应该允许")
}

// TestGossipRateLimiter_Metrics 测试监控指标
func TestGossipRateLimiter_Metrics(t *testing.T) {
	config := &RateLimiterConfig{
		Rate:         10,
		Burst:        5,
		PerNodeRate:  10,
		PerNodeBurst: 5,
		Enabled:      true,
	}
	limiter := NewGossipRateLimiter(config)

	// 初始有 5 个令牌，消耗顺序：
	// 1. Allow() → 成功 (tokens: 5→4)
	// 2. Allow() → 成功 (tokens: 4→3)
	// 3. AllowForNode("node-1") → 成功 (tokens: 3→2)
	// 4. AllowForNode("node-2") → 成功 (tokens: 2→1)
	// 5. Allow() → 成功 (tokens: 1→0)
	// 6. AllowForNode("node-1") → 失败 (tokens: 0, 全局限流)

	limiter.Allow()
	limiter.Allow()
	limiter.AllowForNode("node-1")
	limiter.AllowForNode("node-2")
	limiter.Allow()                // 第 5 次，令牌耗尽但成功
	limiter.AllowForNode("node-1") // 第 6 次，被全局限流拒绝

	// 检查指标
	metrics := limiter.GetMetrics()
	require.Equal(t, int64(6), metrics.TotalMessages)
	require.Equal(t, int64(5), metrics.AllowedMessages) // 前 5 个成功
	require.Equal(t, int64(1), metrics.DroppedMessages) // 最后 1 个被拒绝

	// 检查丢弃率
	dropRate := limiter.GetDropRate()
	require.InDelta(t, 1.0/6.0, dropRate, 0.001)
}

// TestGossipRateLimiter_ResetMetrics 测试重置指标
func TestGossipRateLimiter_ResetMetrics(t *testing.T) {
	config := DefaultRateLimiterConfig()
	limiter := NewGossipRateLimiter(config)

	// 发送一些消息
	for i := 0; i < 10; i++ {
		limiter.Allow()
	}

	// 重置指标
	limiter.ResetMetrics()

	metrics := limiter.GetMetrics()
	require.Equal(t, int64(0), metrics.TotalMessages)
	require.Equal(t, int64(0), metrics.AllowedMessages)
	require.Equal(t, int64(0), metrics.DroppedMessages)
}

// TestGossipRateLimiter_BatchRateLimit 测试批量限流
func TestGossipRateLimiter_BatchRateLimit(t *testing.T) {
	config := &RateLimiterConfig{
		Rate:         100,
		Burst:        5, // 突发容量只有 5
		PerNodeRate:  100,
		PerNodeBurst: 5,
		Enabled:      true,
	}
	limiter := NewGossipRateLimiter(config)

	nodeIDs := []string{"node-1", "node-2", "node-3", "node-4", "node-5", "node-6", "node-7", "node-8"}

	sendFunc := func(nodeID string) error {
		return nil
	}

	ctx := context.Background()
	successCount, droppedCount, _ := limiter.BatchRateLimit(ctx, nodeIDs, sendFunc)

	// 只有 5 个令牌，所以最多 5 个成功
	require.LessOrEqual(t, successCount, 5)
	require.GreaterOrEqual(t, droppedCount, 3) // 至少 3 个被丢弃
	require.Equal(t, len(nodeIDs), successCount+droppedCount)
}

// TestGossipRateLimiter_DynamicConfig 测试动态配置
func TestGossipRateLimiter_DynamicConfig(t *testing.T) {
	limiter := NewGossipRateLimiter(&RateLimiterConfig{
		Rate:         10,
		Burst:        10,
		PerNodeRate:  10,
		PerNodeBurst: 10,
		Enabled:      true,
	})

	// 消耗掉所有令牌
	for i := 0; i < 10; i++ {
		limiter.Allow()
	}
	require.False(t, limiter.Allow())

	// 动态提高速率
	limiter.SetRate(1000)
	limiter.SetBurst(100)

	// 等待令牌补充
	time.Sleep(50 * time.Millisecond)

	// 现在应该可以发送了
	require.True(t, limiter.Allow())
}

// TestGossipRateLimiter_CleanupStaleNodeLimiters 测试清理过期节点
func TestGossipRateLimiter_CleanupStaleNodeLimiters(t *testing.T) {
	limiter := NewGossipRateLimiter(DefaultRateLimiterConfig())

	// 创建一些节点限流器
	limiter.AllowForNode("node-1")
	limiter.AllowForNode("node-2")
	limiter.AllowForNode("node-3")

	require.Len(t, limiter.nodeLimiters, 3)

	// 等待 100ms
	time.Sleep(100 * time.Millisecond)

	// 更新 node-1
	limiter.AllowForNode("node-1")

	// 清理超过 50ms 未使用的节点
	cleaned := limiter.CleanupStaleNodeLimiters(50 * time.Millisecond)
	require.Equal(t, 2, cleaned) // node-2 和 node-3 被清理
	require.Len(t, limiter.nodeLimiters, 1)
	_, exists := limiter.nodeLimiters["node-1"]
	require.True(t, exists)
}

// TestGossipRateLimiter_Concurrent 测试并发安全
func TestGossipRateLimiter_Concurrent(t *testing.T) {
	limiter := NewGossipRateLimiter(&RateLimiterConfig{
		Rate:         10000, // 高速率避免限流
		Burst:        10000,
		PerNodeRate:  10000,
		PerNodeBurst: 10000,
		Enabled:      true,
	})

	const goroutines = 100
	const messagesPerGoroutine = 10

	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			nodeID := string(rune('a' + id%26))
			for j := 0; j < messagesPerGoroutine; j++ {
				limiter.AllowForNode(nodeID)
			}
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// 验证指标
	metrics := limiter.GetMetrics()
	require.Equal(t, int64(goroutines*messagesPerGoroutine), metrics.TotalMessages)
}

// TestGossipRateLimiter_String 测试字符串表示
func TestGossipRateLimiter_String(t *testing.T) {
	limiter := NewGossipRateLimiter(&RateLimiterConfig{
		Rate:         100,
		Burst:        200,
		PerNodeRate:  20,
		PerNodeBurst: 50,
		Enabled:      true,
	})

	str := limiter.String()
	require.Contains(t, str, "rate=100.0")
	require.Contains(t, str, "burst=200")
	require.Contains(t, str, "tokens=200.0")
}

// TestGossipRateLimiter_GetTokenCount 测试获取令牌数
func TestGossipRateLimiter_GetTokenCount(t *testing.T) {
	limiter := NewGossipRateLimiter(&RateLimiterConfig{
		Rate:         10,
		Burst:        10,
		PerNodeRate:  10,
		PerNodeBurst: 10,
		Enabled:      true,
	})

	require.Equal(t, float64(10), limiter.GetTokenCount())

	limiter.Allow()
	limiter.Allow()
	limiter.Allow()

	// 使用近似比较，因为时间流逝可能导致令牌补充
	require.InDelta(t, float64(7), limiter.GetTokenCount(), 0.5)
}

// TestGossipRateLimiter_GetNodeTokenCount 测试获取节点令牌数
func TestGossipRateLimiter_GetNodeTokenCount(t *testing.T) {
	limiter := NewGossipRateLimiter(&RateLimiterConfig{
		Rate:         1000,
		Burst:        1000,
		PerNodeRate:  10,
		PerNodeBurst: 10,
		Enabled:      true,
	})

	nodeID := "node-1"
	limiter.AllowForNode(nodeID)
	limiter.AllowForNode(nodeID)
	limiter.AllowForNode(nodeID)

	// PerNodeBurst=10，消耗 3 个，剩余 7 个
	// 使用近似比较，因为时间流逝可能导致令牌补充
	require.InDelta(t, float64(7), limiter.GetNodeTokenCount(nodeID), 0.5)

	// 不存在的节点
	require.Equal(t, float64(0), limiter.GetNodeTokenCount("nonexistent"))
}
