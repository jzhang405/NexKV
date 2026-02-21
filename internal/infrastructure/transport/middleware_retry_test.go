// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	pkgerrors "github.com/jzhang405/NexKV/pkg/errors"
)

func TestRetryMiddleware_Name(t *testing.T) {
	m := NewRetryMiddleware(DefaultRetryConfig())
	assert.Equal(t, "retry", m.Name())
}

func TestRetryMiddleware_SuccessNoRetry(t *testing.T) {
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return nil
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 1, callCount) // 成功不重试
}

func TestRetryMiddleware_RetryOnNetError(t *testing.T) {
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	netErr := &netError{msg: "connection refused"}
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		if callCount < 3 {
			return netErr
		}
		return nil
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 3, callCount) // 失败 2 次后成功
}

func TestRetryMiddleware_MaxAttemptsExceeded(t *testing.T) {
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	netErr := &netError{msg: "connection refused"}
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return netErr
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.Error(t, err)
	assert.Equal(t, 3, callCount) // 最多重试 3 次
}

func TestRetryMiddleware_NoRetryOnCircuitBreakerOpen(t *testing.T) {
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return pkgerrors.ErrCircuitBreakerOpen
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, pkgerrors.ErrCircuitBreakerOpen))
	assert.Equal(t, 1, callCount) // 熔断错误不重试
}

func TestRetryMiddleware_NoRetryOnRateLimitExceeded(t *testing.T) {
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return pkgerrors.ErrRateLimitExceeded
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, pkgerrors.ErrRateLimitExceeded))
	assert.Equal(t, 1, callCount) // 限流错误不重试
}

func TestRetryMiddleware_ContextCancel(t *testing.T) {
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  10,
		InitialDelay: 100 * time.Millisecond,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx, cancel := context.WithCancel(context.Background())

	var callCount int32
	netErr := &netError{msg: "connection refused"}

	// 在第一次调用后取消上下文
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		atomic.AddInt32(&callCount, 1)
		return netErr
	}

	err := m.InterceptSend(ctx, peer, msg, next)
	assert.Error(t, err)
	// 应该在取消后立即停止
	assert.LessOrEqual(t, int(atomic.LoadInt32(&callCount)), 2)
}

func TestRetryMiddleware_DefaultConfig(t *testing.T) {
	config := DefaultRetryConfig()
	assert.Equal(t, uint(3), config.MaxAttempts)
	assert.Equal(t, 100*time.Millisecond, config.InitialDelay)
	assert.Equal(t, 5*time.Second, config.MaxDelay)
	assert.NotNil(t, config.RetryOn)
}

func TestRetryMiddleware_ZeroConfig(t *testing.T) {
	// 零值配置应该使用默认值
	m := NewRetryMiddleware(RetryConfig{})
	assert.Equal(t, uint(3), m.config.MaxAttempts)
	assert.Equal(t, 100*time.Millisecond, m.config.InitialDelay)
	assert.Equal(t, 5*time.Second, m.config.MaxDelay)
}

func TestRetryMiddleware_OnRetryCallback(t *testing.T) {
	var retryCount uint
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		OnRetry: func(n uint, err error) {
			retryCount = n
		},
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	netErr := &netError{msg: "connection refused"}
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return netErr
	}

	_ = m.InterceptSend(ctx, peer, msg, next)
	assert.Equal(t, uint(2), retryCount) // 重试 2 次（n=1, n=2）
}

func TestRetryMiddleware_InterceptReceive(t *testing.T) {
	m := NewRetryMiddleware(DefaultRetryConfig())

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		return nil
	}

	// 接收不重试
	err := m.InterceptReceive(ctx, peer, msg, next)
	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)
}

// 确保实现 Middleware 接口
func TestRetryMiddleware_ImplementsInterface(t *testing.T) {
	var _ service.Middleware = NewRetryMiddleware(DefaultRetryConfig())
}

// netError 模拟网络错误
type netError struct {
	msg string
}

func (e *netError) Error() string   { return e.msg }
func (e *netError) Timeout() bool   { return false }
func (e *netError) Temporary() bool { return true }

// BenchmarkRetryMiddleware 基准测试（无重试）
func BenchmarkRetryMiddleware(b *testing.B) {
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
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

// BenchmarkRetryMiddleware_WithRetry 基准测试（有重试）
func BenchmarkRetryMiddleware_WithRetry(b *testing.B) {
	m := NewRetryMiddleware(RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
	})

	peer := model.PeerID("test-peer")
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "src", "dst", []byte("payload"))
	ctx := context.Background()

	var callCount int
	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		callCount++
		if callCount%3 != 0 {
			return &netError{msg: "connection refused"}
		}
		callCount = 0
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}
}
