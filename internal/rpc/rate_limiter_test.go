// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// RateLimiterConfig Tests
// ========================================

// TestDefaultRateLimiterConfig 测试默认配置
func TestDefaultRateLimiterConfig(t *testing.T) {
	config := DefaultRateLimiterConfig()

	assert.Equal(t, 100, config.MaxConnections, "默认最大连接数应该是 100")
	assert.Equal(t, 100*time.Millisecond, config.RefillRate, "默认补充率应该是 100ms")
	assert.Equal(t, 10, config.RefillAmount, "默认补充量应该是 10")
	assert.Equal(t, 100, config.BucketSize, "默认桶大小应该是 100")
	assert.Equal(t, 5*time.Second, config.AcquireTimeout, "默认超时应该是 5s")
	assert.False(t, config.EnableAutoAdjust, "默认不启用自动调整")
}

// ========================================
// RateLimiter Basic Tests
// ========================================

// TestRateLimiter_AcquireRelease 测试获取和释放
func TestRateLimiter_AcquireRelease(t *testing.T) {
	config := &RateLimiterConfig{
		MaxConnections: 5,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   2,
		BucketSize:     10,
		AcquireTimeout: 1 * time.Second,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	ctx := context.Background()

	// 获取 5 个连接
	for i := 0; i < 5; i++ {
		err := limiter.Acquire(ctx)
		assert.NoError(t, err, "第 %d 个连接应该成功获取", i)
	}

	// 验证统计信息
	stats := limiter.GetCurrentStats()
	assert.Equal(t, 5, stats.CurrentConnections)
	assert.Equal(t, 5, stats.MaxConnections)

	// 释放所有连接
	for i := 0; i < 5; i++ {
		limiter.Release()
	}

	// 验证统计信息
	stats = limiter.GetCurrentStats()
	assert.Equal(t, 0, stats.CurrentConnections)
}

// TestRateLimiter_TryAcquire 测试非阻塞获取
func TestRateLimiter_TryAcquire(t *testing.T) {
	config := &RateLimiterConfig{
		MaxConnections: 3,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   1,
		BucketSize:     5,
		AcquireTimeout: 1 * time.Second,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	// 尝试获取 3 个连接
	for i := 0; i < 3; i++ {
		acquired := limiter.TryAcquire()
		assert.True(t, acquired, "第 %d 个连接应该成功", i)
	}

	// 第 4 个应该失败
	acquired := limiter.TryAcquire()
	assert.False(t, acquired, "第 4 个连接应该失败")

	// 释放 1 个连接
	limiter.Release()

	// 再次尝试应该成功
	acquired = limiter.TryAcquire()
	assert.True(t, acquired, "释放后应该可以再次获取")
}

// TestRateLimiter_ConnectionLimit 测试连接数限制
func TestRateLimiter_ConnectionLimit(t *testing.T) {
	config := &RateLimiterConfig{
		MaxConnections: 2,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   1,
		BucketSize:     5,
		AcquireTimeout: 100 * time.Millisecond,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	ctx := context.Background()

	// 获取 2 个连接（达到上限）
	err := limiter.Acquire(ctx)
	assert.NoError(t, err)
	err = limiter.Acquire(ctx)
	assert.NoError(t, err)

	// 第 3 个应该超时
	start := time.Now()
	err = limiter.Acquire(ctx)
	duration := time.Since(start)

	assert.Error(t, err)
	assert.True(t, IsTimeout(err), "应该超时")
	assert.GreaterOrEqual(t, duration, 100*time.Millisecond, "应该至少等待超时时间")
}

// TestRateLimiter_TokenBucket 测试令牌桶
func TestRateLimiter_TokenBucket(t *testing.T) {
	config := &RateLimiterConfig{
		MaxConnections: 5,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   2,
		BucketSize:     4,
		AcquireTimeout: 1 * time.Second,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	ctx := context.Background()

	// 获取 4 个连接（令牌耗尽）
	for i := 0; i < 4; i++ {
		err := limiter.Acquire(ctx)
		assert.NoError(t, err)
		limiter.Release()
	}

	// 等待令牌补充
	time.Sleep(100 * time.Millisecond)

	// 应该可以继续获取连接
	err := limiter.Acquire(ctx)
	assert.NoError(t, err, "补充后应该可以获取连接")
}

// ========================================
// Dynamic Adjustment Tests
// ========================================

// TestRateLimiter_UpdateConfig 测试配置更新
func TestRateLimiter_UpdateConfig(t *testing.T) {
	config := &RateLimiterConfig{
		MaxConnections: 5,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   1,
		BucketSize:     10,
		AcquireTimeout: 1 * time.Second,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	// 获取 2 个连接
	ctx := context.Background()
	err := limiter.Acquire(ctx)
	assert.NoError(t, err)
	err = limiter.Acquire(ctx)
	assert.NoError(t, err)

	// 尝试更新为更小的最大连接数（应该失败）
	newConfig := &RateLimiterConfig{
		MaxConnections:   1, // 小于当前连接数
		RefillRate:       config.RefillRate,
		RefillAmount:     config.RefillAmount,
		BucketSize:       config.BucketSize,
		AcquireTimeout:   config.AcquireTimeout,
		EnableAutoAdjust: false,
	}

	err = limiter.UpdateConfig(newConfig)
	assert.Error(t, err, "更新为更小的连接数应该失败")

	// 释放连接
	limiter.Release()
	limiter.Release()

	// 更新为更大的最大连接数（应该成功）
	newConfig.MaxConnections = 10
	err = limiter.UpdateConfig(newConfig)
	assert.NoError(t, err)

	stats := limiter.GetCurrentStats()
	assert.Equal(t, 10, stats.MaxConnections)
}

// ========================================
// Stats Tests
// ========================================

// TestRateLimiter_GetCurrentStats 测试获取统计信息
func TestRateLimiter_GetCurrentStats(t *testing.T) {
	config := &RateLimiterConfig{
		MaxConnections: 10,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   1,
		BucketSize:     20,
		AcquireTimeout: 1 * time.Second,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	// 初始状态
	stats := limiter.GetCurrentStats()
	assert.Equal(t, 0, stats.CurrentConnections)
	assert.Equal(t, 10, stats.MaxConnections)
	assert.Equal(t, 20, stats.BucketSize)
	assert.Equal(t, 20, stats.AvailableTokens)

	// 获取连接后
	ctx := context.Background()
	err := limiter.Acquire(ctx)
	require.NoError(t, err)

	stats = limiter.GetCurrentStats()
	assert.Equal(t, 1, stats.CurrentConnections)
}

// ========================================
// Edge Cases
// ========================================

// TestRateLimiter_ContextCancel 测试上下文取消
func TestRateLimiter_ContextCancel(t *testing.T) {
	config := &RateLimiterConfig{
		MaxConnections: 5,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   1,
		BucketSize:     10,
		AcquireTimeout: 5 * time.Second,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 立即取消
	cancel()

	// 尝试获取连接应该立即返回取消错误
	err := limiter.Acquire(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// TestRateLimiter_ConcurrentAccess 测试并发访问
func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	config := &RateLimiterConfig{
		MaxConnections: 10,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   2,
		BucketSize:     20,
		AcquireTimeout: 2 * time.Second,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	// 并发获取连接
	const numGoroutines = 20
	const numAcquires = 5

	var wg sync.WaitGroup
	successCount := make(chan int, numGoroutines*numAcquires)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()

			for j := 0; j < numAcquires; j++ {
				err := limiter.Acquire(ctx)
				if err == nil {
					limiter.Release()
					successCount <- 1
				}
				time.Sleep(1 * time.Millisecond) // 模拟工作
			}
		}()
	}

	wg.Wait()
	close(successCount)

	// 统计成功的获取次数
	totalSuccess := 0
	for range successCount {
		totalSuccess++
	}

	// 验证至少有一些成功
	assert.Greater(t, totalSuccess, 0, "应该有部分成功的连接获取")
}

// ========================================
// Benchmark Tests
// ========================================

// BenchmarkRateLimiter_Acquire 基准测试：获取连接性能
func BenchmarkRateLimiter_Acquire(b *testing.B) {
	config := &RateLimiterConfig{
		MaxConnections: 1000,
		RefillRate:     10 * time.Microsecond,
		RefillAmount:   100,
		BucketSize:     1000,
		AcquireTimeout: 100 * time.Millisecond,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if limiter.TryAcquire() {
			limiter.Release()
		}
		_ = ctx // 避免未使用变量警告
	}
}

// BenchmarkRateLimiter_TryAcquire 基准测试：非阻塞获取性能
func BenchmarkRateLimiter_TryAcquire(b *testing.B) {
	config := &RateLimiterConfig{
		MaxConnections: 1000,
		RefillRate:     10 * time.Microsecond,
		RefillAmount:   100,
		BucketSize:     1000,
		AcquireTimeout: 100 * time.Millisecond,
	}

	limiter := NewRateLimiter(config)
	defer limiter.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.TryAcquire()
	}
}
