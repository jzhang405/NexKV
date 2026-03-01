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
	for range 10 {
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
	for range 3 {
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
	for range 2 {
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
	for range 2 {
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
	for range 2 {
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
	for range 3 {
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
	for b.Loop() {
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
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range requestsPerGoroutine {
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

// TestDefaultReadyToTrip 测试默认熔断触发条件
func TestDefaultReadyToTrip(t *testing.T) {
	tests := []struct {
		name       string
		counts     gobreaker.Counts
		shouldTrip bool
	}{
		{
			name: "no requests",
			counts: gobreaker.Counts{
				Requests:             0,
				TotalSuccesses:       0,
				TotalFailures:        0,
				ConsecutiveSuccesses: 0,
				ConsecutiveFailures:  0,
			},
			shouldTrip: false,
		},
		{
			name: "less than 10 requests",
			counts: gobreaker.Counts{
				Requests:             5,
				TotalSuccesses:       2,
				TotalFailures:        3,
				ConsecutiveSuccesses: 0,
				ConsecutiveFailures:  3,
			},
			shouldTrip: false,
		},
		{
			name: "10 requests, 50% failure rate",
			counts: gobreaker.Counts{
				Requests:             10,
				TotalSuccesses:       5,
				TotalFailures:        5,
				ConsecutiveSuccesses: 0,
				ConsecutiveFailures:  0,
			},
			shouldTrip: true, // >= 10 requests and 50% failure rate
		},
		{
			name: "10 requests, 60% failure rate",
			counts: gobreaker.Counts{
				Requests:             10,
				TotalSuccesses:       4,
				TotalFailures:        6,
				ConsecutiveSuccesses: 0,
				ConsecutiveFailures:  0,
			},
			shouldTrip: true, // >= 10 requests and > 50% failure rate
		},
		{
			name: "10 requests, 40% failure rate",
			counts: gobreaker.Counts{
				Requests:             10,
				TotalSuccesses:       6,
				TotalFailures:        4,
				ConsecutiveSuccesses: 0,
				ConsecutiveFailures:  0,
			},
			shouldTrip: false, // failure rate < 50%
		},
		{
			name: "5 consecutive failures",
			counts: gobreaker.Counts{
				Requests:             5,
				TotalSuccesses:       0,
				TotalFailures:        5,
				ConsecutiveSuccesses: 0,
				ConsecutiveFailures:  5,
			},
			shouldTrip: true, // >= 5 consecutive failures
		},
		{
			name: "3 consecutive failures",
			counts: gobreaker.Counts{
				Requests:             10,
				TotalSuccesses:       7,
				TotalFailures:        3,
				ConsecutiveSuccesses: 0,
				ConsecutiveFailures:  3,
			},
			shouldTrip: false, // < 5 consecutive failures
		},
		{
			name: "both conditions met",
			counts: gobreaker.Counts{
				Requests:             20,
				TotalSuccesses:       5,
				TotalFailures:        15,
				ConsecutiveSuccesses: 0,
				ConsecutiveFailures:  5,
			},
			shouldTrip: true, // both conditions met
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultReadyToTrip(tt.counts)
			if result != tt.shouldTrip {
				t.Errorf("defaultReadyToTrip() = %v, want %v", result, tt.shouldTrip)
			}
		})
	}
}

// TestCircuitBreakerMiddleware_RemoveBreaker 测试移除熔断器
func TestCircuitBreakerMiddleware_RemoveBreaker(t *testing.T) {
	m := NewCircuitBreakerMiddleware(DefaultCircuitBreakerConfig())

	peer1 := model.PeerID("peer-1")
	peer2 := model.PeerID("peer-2")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	// 创建一些请求以初始化熔断器
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	// 为 peer1 创建请求，触发熔断器创建
	_ = m.InterceptSend(ctx, peer1, msg, next)

	// 验证熔断器存在
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer1))

	// 移除 peer1 的熔断器
	m.RemoveBreaker(peer1)

	// 再次获取状态应该创建新的熔断器（初始状态为 Closed）
	state := m.GetState(peer1)
	assert.Equal(t, gobreaker.StateClosed, state)

	// peer2 的熔断器应该仍然存在
	_ = m.InterceptSend(ctx, peer2, msg, next)
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer2))
}

// TestCircuitBreakerMiddleware_CleanupBreakers 测试清理熔断器
func TestCircuitBreakerMiddleware_CleanupBreakers(t *testing.T) {
	m := NewCircuitBreakerMiddleware(DefaultCircuitBreakerConfig())

	peer1 := model.PeerID("peer-1")
	peer2 := model.PeerID("peer-2")
	peer3 := model.PeerID("peer-3")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	// 为三个节点创建请求
	_ = m.InterceptSend(ctx, peer1, msg, next)
	_ = m.InterceptSend(ctx, peer2, msg, next)
	_ = m.InterceptSend(ctx, peer3, msg, next)

	// 验证熔断器存在
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer1))
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer2))
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer3))

	// 清理不在有效列表中的熔断器（peer3 不在有效列表中）
	validPeers := []model.PeerID{peer1, peer2}
	m.CleanupBreakers(validPeers)

	// peer1 和 peer2 的熔断器应该仍然存在
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer1))
	assert.Equal(t, gobreaker.StateClosed, m.GetState(peer2))

	// peer3 的熔断器应该被移除（重新获取会创建新的）
	state := m.GetState(peer3)
	assert.Equal(t, gobreaker.StateClosed, state)
}

// TestCircuitBreakerMiddleware_BreakerCount 测试熔断器计数
func TestCircuitBreakerMiddleware_BreakerCount(t *testing.T) {
	m := NewCircuitBreakerMiddleware(DefaultCircuitBreakerConfig())

	// 初始计数应该为 0
	assert.Equal(t, 0, m.BreakerCount())

	peer1 := model.PeerID("peer-1")
	peer2 := model.PeerID("peer-2")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	// 为两个节点创建请求，应该创建两个熔断器
	_ = m.InterceptSend(ctx, peer1, msg, next)
	_ = m.InterceptSend(ctx, peer2, msg, next)

	// 计数应该为 2
	count := m.BreakerCount()
	assert.Equal(t, 2, count)
}
