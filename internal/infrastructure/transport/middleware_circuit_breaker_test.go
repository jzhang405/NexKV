// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	pkgerrors "github.com/jzhang405/NexKV/pkg/errors"
)

func TestCircuitBreakerMiddleware_Name(t *testing.T) {
	m := NewCircuitBreakerMiddleware(DefaultCircuitBreakerConfig())
	assert.Equal(t, "circuit-breaker", m.Name())
}

func TestCircuitBreakerMiddleware_SuccessRequests(t *testing.T) {
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "test",
		MaxRequests: 3,
		Timeout:     1 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 3 },
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return nil
	}

	// 成功请求不应该触发熔断
	for i := 0; i < 10; i++ {
		err := m.InterceptSend(ctx, peer, msg, next)
		assert.NoError(t, err)
	}
	assert.Equal(t, 10, callCount)
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer))
}

func TestCircuitBreakerMiddleware_OpenOnFailures(t *testing.T) {
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "test",
		MaxRequests: 3,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 3 },
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	testErr := errors.New("test error")
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return testErr
	}

	// 连续失败 3 次后熔断器应该打开
	for i := 0; i < 3; i++ {
		err := m.InterceptSend(ctx, peer, msg, next)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, testErr))
	}

	// 熔断器应该处于 Open 状态
	assert.Equal(t, gobreaker.StateOpen, m.GetState(peer))

	// 后续请求应该直接返回熔断错误
	err := m.InterceptSend(ctx, peer, msg, next)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, pkgerrors.ErrCircuitBreakerOpen))
}

func TestCircuitBreakerMiddleware_HalfOpenToClosed(t *testing.T) {
	// 跳过短测试模式
	if testing.Short() {
		t.Skip("skipping half-open test in short mode")
	}

	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "test",
		MaxRequests: 1,
		Timeout:     50 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 2 },
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	testErr := errors.New("test error")
	failCount := 0
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		failCount++
		if failCount <= 2 {
			return testErr
		}
		return nil
	}

	// 连续失败 2 次触发熔断
	for i := 0; i < 2; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}
	assert.Equal(t, gobreaker.StateOpen, m.GetState(peer))

	// 等待超时进入 HalfOpen
	time.Sleep(60 * time.Millisecond)

	// 下一个成功请求应该让熔断器恢复
	err := m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer))
}

func TestCircuitBreakerMiddleware_DifferentPeers(t *testing.T) {
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "test",
		MaxRequests: 1,
		Timeout:     1 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 2 },
	})

	peer1 := model.PeerID("peer-1")
	peer2 := model.PeerID("peer-2")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	testErr := errors.New("test error")
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return testErr
	}

	// peer1 连续失败 2 次
	for i := 0; i < 2; i++ {
		_ = m.InterceptSend(ctx, peer1, msg, next)
	}

	// peer1 熔断器应该打开
	assert.Equal(t, gobreaker.StateOpen, m.GetState(peer1))

	// peer2 熔断器应该仍然关闭
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer2))
}

func TestCircuitBreakerMiddleware_InterceptReceive(t *testing.T) {
	m := NewCircuitBreakerMiddleware(DefaultCircuitBreakerConfig())

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return nil
	}

	// 接收不熔断
	err := m.InterceptReceive(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)
}

func TestCircuitBreakerMiddleware_DefaultConfig(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	assert.Equal(t, "rpc-circuit-breaker", config.Name)
	assert.Equal(t, uint32(3), config.MaxRequests)
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.NotNil(t, config.ReadyToTrip)
}

func TestCircuitBreakerMiddleware_ZeroConfig(t *testing.T) {
	// 零值配置应该使用默认值
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{})
	assert.Equal(t, "rpc-circuit-breaker", m.config.Name)
	assert.Equal(t, uint32(3), m.config.MaxRequests)
	assert.Equal(t, 30*time.Second, m.config.Timeout)
}

func TestCircuitBreakerMiddleware_StateChangeCallback(t *testing.T) {
	var stateChanges []string
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "test",
		MaxRequests: 1,
		Timeout:     50 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 2 },
		OnStateChange: func(name string, from, to gobreaker.State) {
			stateChanges = append(stateChanges, from.String()+"->"+to.String())
		},
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	testErr := errors.New("test error")
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return testErr
	}

	// 触发熔断
	for i := 0; i < 2; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}

	// 验证状态变更回调被触发
	assert.Contains(t, stateChanges, "closed->open")
}

func TestCircuitBreakerMiddleware_GetCounts(t *testing.T) {
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "test",
		MaxRequests: 1,
		Timeout:     1 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 5 },
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	testErr := errors.New("test error")
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return testErr
	}

	// 连续失败 3 次
	for i := 0; i < 3; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}

	counts := m.GetCounts(peer)
	assert.Equal(t, uint32(3), counts.ConsecutiveFailures)
	assert.Equal(t, uint32(3), counts.TotalFailures)
	assert.Equal(t, uint32(3), counts.Requests)
}

// 确保实现 Middleware 接口
func TestCircuitBreakerMiddleware_ImplementsInterface(t *testing.T) {
	var _ service.Middleware = NewCircuitBreakerMiddleware(DefaultCircuitBreakerConfig())
}

// BenchmarkCircuitBreakerMiddleware 基准测试
func BenchmarkCircuitBreakerMiddleware(b *testing.B) {
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "bench",
		MaxRequests: 100,
		Timeout:     1 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 1000 },
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

// TestCircuitBreakerMiddleware_Concurrent 测试并发安全性
func TestCircuitBreakerMiddleware_Concurrent(t *testing.T) {
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "test",
		MaxRequests: 1000,
		Timeout:     1 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 1000 },
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var successCount int64
	var failCount int64

	// 并发发送请求
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
				} else {
					atomic.AddInt64(&failCount, 1)
				}
			}
		}()
	}

	wg.Wait()

	// 所有请求都应该成功（熔断器未触发）
	require.Equal(t, int64(goroutines*requestsPerGoroutine), successCount)
	assert.Equal(t, int64(0), failCount)
}
