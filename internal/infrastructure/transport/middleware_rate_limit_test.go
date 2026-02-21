// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	pkgerrors "github.com/jzhang405/NexKV/pkg/errors"
)

func TestRateLimitMiddleware_Name(t *testing.T) {
	m := NewRateLimitMiddleware(DefaultRateLimitConfig())
	assert.Equal(t, "rate-limit", m.Name())
}

func TestRateLimitMiddleware_AllowRequest(t *testing.T) {
	m := NewRateLimitMiddleware(RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             2,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	// 前两个请求应该成功（Burst = 2）
	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return nil
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)

	err = m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 2, callCount)

	// 第三个请求应该被限流
	err = m.InterceptSend(ctx, peer, msg, next)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, pkgerrors.ErrRateLimitExceeded))
	assert.Equal(t, 2, callCount) // callCount 不应该增加
}

func TestRateLimitMiddleware_DifferentPeers(t *testing.T) {
	m := NewRateLimitMiddleware(RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             1,
	})

	peer1 := model.PeerID("peer-1")
	peer2 := model.PeerID("peer-2")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return nil
	}

	// peer1 的第一个请求成功
	err := m.InterceptSend(ctx, peer1, msg, next)
	assert.NoError(t, err)

	// peer1 的第二个请求被限流
	err = m.InterceptSend(ctx, peer1, msg, next)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, pkgerrors.ErrRateLimitExceeded))

	// peer2 的第一个请求成功（独立限流器）
	err = m.InterceptSend(ctx, peer2, msg, next)
	assert.NoError(t, err)

	assert.Equal(t, 2, callCount)
}

func TestRateLimitMiddleware_InterceptReceive(t *testing.T) {
	m := NewRateLimitMiddleware(DefaultRateLimitConfig())

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return nil
	}

	// 接收不限流
	err := m.InterceptReceive(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)
}

func TestRateLimitMiddleware_DefaultConfig(t *testing.T) {
	config := DefaultRateLimitConfig()
	assert.Equal(t, 1000, config.RequestsPerSecond)
	assert.Equal(t, 100, config.Burst)
}

func TestRateLimitMiddleware_ZeroConfig(t *testing.T) {
	// 零值配置应该使用默认值
	m := NewRateLimitMiddleware(RateLimitConfig{})
	assert.Equal(t, 1000, m.config.RequestsPerSecond)
	assert.Equal(t, 100, m.config.Burst)
}

func TestRateLimitMiddleware_TokenBucketRefill(t *testing.T) {
	// 跳过短测试模式
	if testing.Short() {
		t.Skip("skipping token bucket refill test in short mode")
	}

	m := NewRateLimitMiddleware(RateLimitConfig{
		RequestsPerSecond: 100, // 100 req/s = 1 token per 10ms
		Burst:             1,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return nil
	}

	// 第一个请求成功
	err := m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)

	// 立即第二个请求被限流
	err = m.InterceptSend(ctx, peer, msg, next)
	assert.Error(t, err)

	// 等待令牌补充（100 req/s = 每 10ms 补充 1 个令牌）
	time.Sleep(15 * time.Millisecond)

	// 现在应该可以发送
	callCount = 0
	err = m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)
}

// 确保实现 Middleware 接口
func TestRateLimitMiddleware_ImplementsInterface(t *testing.T) {
	var _ service.Middleware = NewRateLimitMiddleware(DefaultRateLimitConfig())
}

// BenchmarkRateLimitMiddleware 基准测试
func BenchmarkRateLimitMiddleware(b *testing.B) {
	m := NewRateLimitMiddleware(RateLimitConfig{
		RequestsPerSecond: 100000,
		Burst:             10000,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}
}

// TestRateLimitMiddleware_Concurrent 测试并发安全性
func TestRateLimitMiddleware_Concurrent(t *testing.T) {
	m := NewRateLimitMiddleware(RateLimitConfig{
		RequestsPerSecond: 10000,
		Burst:             1000,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var successCount int64
	var limitCount int64

	// 并发发送 100 个请求
	const goroutines = 10
	const requestsPerGoroutine = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				err := m.InterceptSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
					return nil
				})
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else if errors.Is(err, pkgerrors.ErrRateLimitExceeded) {
					atomic.AddInt64(&limitCount, 1)
				}
			}
		}()
	}

	wg.Wait()

	// 验证总请求数正确
	total := successCount + limitCount
	require.Equal(t, int64(goroutines*requestsPerGoroutine), total)

	// 成功数应该等于 Burst
	assert.Equal(t, int64(1000), successCount)
}
