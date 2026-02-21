// Package transport 实现传输层基础设施
package transport

import (
	"context"
	stderrors "errors"
	"net"
	"time"

	"github.com/avast/retry-go/v4"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// RetryMiddleware 重试中间件
// 使用指数退避策略，支持熔断联动
type RetryMiddleware struct {
	config RetryConfig
}

// RetryConfig 重试配置
type RetryConfig struct {
	// MaxAttempts 最大重试次数（默认 3）
	MaxAttempts uint
	// InitialDelay 初始延迟（默认 100ms）
	InitialDelay time.Duration
	// MaxDelay 最大延迟（默认 5s）
	MaxDelay time.Duration
	// MaxTotalTime 最大总重试时长（默认 10s）
	// 用于防止长时间阻塞
	MaxTotalTime time.Duration
	// RetryOn 判断是否可重试的错误（默认网络错误、超时错误）
	RetryOn func(error) bool
	// OnRetry 重试回调（用于日志）
	OnRetry func(n uint, err error)
	// DelayType 延迟类型（默认指数退避）
	DelayType retry.DelayTypeFunc
}

// DefaultRetryConfig 默认重试配置
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		MaxTotalTime: 10 * time.Second,
		RetryOn:      defaultRetryOn,
		DelayType:    retry.BackOffDelay,
	}
}

// defaultRetryOn 默认可重试错误判断
// 网络错误、超时错误可重试；业务错误、熔断错误、限流错误不重试
func defaultRetryOn(err error) bool {
	if err == nil {
		return false
	}

	// 网络错误可重试
	var netErr net.Error
	if stderrors.As(err, &netErr) {
		return true
	}

	// 超时错误可重试
	if stderrors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// 用户取消不重试
	if stderrors.Is(err, context.Canceled) {
		return false
	}

	// 熔断器打开时不重试
	if stderrors.Is(err, errors.ErrCircuitBreakerOpen) {
		return false
	}

	// 限流错误不重试
	if stderrors.Is(err, errors.ErrRateLimitExceeded) {
		return false
	}

	return false
}

// NewRetryMiddleware 创建重试中间件
func NewRetryMiddleware(config RetryConfig) *RetryMiddleware {
	// 应用默认配置
	defaults := DefaultRetryConfig()
	if config.MaxAttempts == 0 {
		config.MaxAttempts = defaults.MaxAttempts
	}
	if config.InitialDelay == 0 {
		config.InitialDelay = defaults.InitialDelay
	}
	if config.MaxDelay == 0 {
		config.MaxDelay = defaults.MaxDelay
	}
	if config.MaxTotalTime == 0 {
		config.MaxTotalTime = defaults.MaxTotalTime
	}
	if config.RetryOn == nil {
		config.RetryOn = defaults.RetryOn
	}
	if config.DelayType == nil {
		config.DelayType = defaults.DelayType
	}

	return &RetryMiddleware{config: config}
}

// Name 返回中间件名称
func (m *RetryMiddleware) Name() string {
	return "retry"
}

// Priority 返回中间件优先级
func (m *RetryMiddleware) Priority() int {
	return service.MiddlewarePriorityRetry
}

// InterceptSend 拦截发送请求
func (m *RetryMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	// 创建带超时的 context，防止长时间阻塞
	retryCtx, cancel := context.WithTimeout(ctx, m.config.MaxTotalTime)
	defer cancel()

	opts := []retry.Option{
		retry.Attempts(m.config.MaxAttempts),
		retry.Delay(m.config.InitialDelay),
		retry.MaxDelay(m.config.MaxDelay),
		retry.DelayType(m.config.DelayType),
		retry.Context(retryCtx), // 使用带超时的 context
		retry.RetryIf(retryIfFunc(m.config.RetryOn)),
	}

	if m.config.OnRetry != nil {
		opts = append(opts, retry.OnRetry(m.config.OnRetry))
	}

	return retry.Do(func() error {
		return next(retryCtx, peer, msg)
	}, opts...)
}

// InterceptReceive 拦截接收请求（不重试）
func (m *RetryMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	return next(ctx, peer, msg)
}

// retryIfFunc 将自定义错误判断函数转换为 retry.RetryIfFunc
func retryIfFunc(fn func(error) bool) retry.RetryIfFunc {
	return func(err error) bool {
		return fn(err)
	}
}

// 确保实现 Middleware 接口
var _ service.Middleware = (*RetryMiddleware)(nil)
