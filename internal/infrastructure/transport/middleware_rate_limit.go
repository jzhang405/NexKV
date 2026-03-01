// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"sync"

	"golang.org/x/time/rate"

	"github.com/jzhang405/NexKV/internal/domain/constants"
	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// RateLimitMiddleware 限流中间件
// 使用令牌桶算法实现按节点限流
type RateLimitMiddleware struct {
	limiters sync.Map // peer.ID -> *rate.Limiter
	config   RateLimitConfig
}

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	// RequestsPerSecond 每秒请求数限制
	RequestsPerSecond int
	// Burst 突发流量容量（令牌桶容量）
	Burst int
}

// DefaultRateLimitConfig 默认限流配置
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		RequestsPerSecond: 1000,
		Burst:             100,
	}
}

// NewRateLimitMiddleware 创建限流中间件
func NewRateLimitMiddleware(config RateLimitConfig) *RateLimitMiddleware {
	defaults := DefaultRateLimitConfig()

	// 验证 RequestsPerSecond（下限 + 上限）
	if config.RequestsPerSecond <= 0 {
		config.RequestsPerSecond = defaults.RequestsPerSecond
	} else if config.RequestsPerSecond > constants.MaxRequestsPerSecond {
		config.RequestsPerSecond = constants.MaxRequestsPerSecond
	}

	// 验证 Burst（下限 + 上限）
	if config.Burst <= 0 {
		config.Burst = defaults.Burst
	} else if config.Burst > constants.MaxBurst {
		config.Burst = constants.MaxBurst
	}

	return &RateLimitMiddleware{config: config}
}

// Name 返回中间件名称
func (m *RateLimitMiddleware) Name() string {
	return "rate-limit"
}

// Priority 返回中间件优先级
func (m *RateLimitMiddleware) Priority() int {
	return service.MiddlewarePriorityRateLimit
}

// InterceptSend 拦截发送请求
func (m *RateLimitMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	limiter := m.getLimiter(peer)
	if !limiter.Allow() {
		return errors.Wrap(errors.ErrRateLimitExceeded, "rate limit exceeded for peer "+string(peer))
	}
	return next(ctx, peer, msg)
}

// InterceptReceive 拦截接收请求（不限流）
func (m *RateLimitMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	return next(ctx, peer, msg)
}

// getLimiter 获取或创建节点的限流器（P1 修复：移除冗余锁，使用 LoadOrStore）
func (m *RateLimitMiddleware) getLimiter(peer model.PeerID) *rate.Limiter {
	// 先尝试加载已存在的 limiter
	if limiter, ok := m.limiters.Load(peer); ok {
		return limiter.(*rate.Limiter)
	}

	// 创建新的 limiter
	newLimiter := rate.NewLimiter(rate.Limit(m.config.RequestsPerSecond), m.config.Burst)

	// 原子操作：如果已存在则使用已有的，否则存储新的
	actual, _ := m.limiters.LoadOrStore(peer, newLimiter)
	return actual.(*rate.Limiter)
}

// RemoveLimiter 移除指定节点的限流器
// 用于节点断开连接后释放资源
func (m *RateLimitMiddleware) RemoveLimiter(peer model.PeerID) {
	m.limiters.Delete(peer)
}

// CleanupLimiters 清理所有不在有效节点列表中的限流器
// 用于定期清理已断开节点的资源
func (m *RateLimitMiddleware) CleanupLimiters(validPeers []model.PeerID) {
	cleanupSyncMap(&m.limiters, validPeers)
}

// LimiterCount 返回当前限流器数量（用于监控）
func (m *RateLimitMiddleware) LimiterCount() int {
	return countSyncMap(&m.limiters)
}

// 确保实现 Middleware 接口
var _ service.Middleware = (*RateLimitMiddleware)(nil)
