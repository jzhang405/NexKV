// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"sync"
	"time"

	"github.com/sony/gobreaker"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// CircuitBreakerMiddleware 熔断中间件（基于 gobreaker）
// 每个节点独立熔断器，单节点故障不影响其他节点
type CircuitBreakerMiddleware struct {
	breakers sync.Map // peer.ID -> *gobreaker.CircuitBreaker
	config   CircuitBreakerConfig
}

// CircuitBreakerConfig 熔断配置
type CircuitBreakerConfig struct {
	// Name 熔断器名称（用于日志和指标）
	Name string
	// MaxRequests HalfOpen 状态最大请求数（默认 3）
	MaxRequests uint32
	// Interval 统计窗口（默认 0，持续统计）
	Interval time.Duration
	// Timeout Open → HalfOpen 超时（默认 30s）
	Timeout time.Duration
	// ReadyToTrip 触发熔断条件
	ReadyToTrip func(counts gobreaker.Counts) bool
	// OnStateChange 状态变更回调
	OnStateChange func(name string, from, to gobreaker.State)
}

// DefaultCircuitBreakerConfig 默认熔断配置
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Name:        "rpc-circuit-breaker",
		MaxRequests: 3,
		Timeout:     30 * time.Second,
		ReadyToTrip: defaultReadyToTrip,
	}
}

// defaultReadyToTrip 默认熔断触发条件
// 连续 5 次失败或失败率 > 50%（至少 10 个请求）
func defaultReadyToTrip(counts gobreaker.Counts) bool {
	failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
	return counts.Requests >= 10 && failureRatio >= 0.5 || counts.ConsecutiveFailures >= 5
}

// NewCircuitBreakerMiddleware 创建熔断中间件
func NewCircuitBreakerMiddleware(config CircuitBreakerConfig) *CircuitBreakerMiddleware {
	// 应用默认配置
	defaults := DefaultCircuitBreakerConfig()
	if config.Name == "" {
		config.Name = defaults.Name
	}
	if config.MaxRequests == 0 {
		config.MaxRequests = defaults.MaxRequests
	}
	if config.Timeout == 0 {
		config.Timeout = defaults.Timeout
	}
	if config.ReadyToTrip == nil {
		config.ReadyToTrip = defaults.ReadyToTrip
	}

	return &CircuitBreakerMiddleware{
		config: config,
	}
}

// Name 返回中间件名称
func (m *CircuitBreakerMiddleware) Name() string {
	return "circuit-breaker"
}

// Priority 返回中间件优先级
func (m *CircuitBreakerMiddleware) Priority() int {
	return service.MiddlewarePriorityCircuitBreaker
}

// InterceptSend 拦截发送请求
func (m *CircuitBreakerMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	breaker := m.getBreaker(peer)

	_, err := breaker.Execute(func() (interface{}, error) {
		return nil, next(ctx, peer, msg)
	})

	if err == gobreaker.ErrOpenState {
		return errors.Wrap(errors.ErrCircuitBreakerOpen, "circuit breaker is open for peer "+string(peer))
	}
	if err == gobreaker.ErrTooManyRequests {
		return errors.Wrap(errors.ErrCircuitBreakerOpen, "too many requests in half-open state for peer "+string(peer))
	}

	return err
}

// InterceptReceive 拦截接收请求（不熔断）
func (m *CircuitBreakerMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	return next(ctx, peer, msg)
}

// getBreaker 获取或创建节点的熔断器
func (m *CircuitBreakerMiddleware) getBreaker(peer model.PeerID) *gobreaker.CircuitBreaker {
	if breaker, ok := m.breakers.Load(peer); ok {
		return breaker.(*gobreaker.CircuitBreaker)
	}

	// 为每个节点创建独立的熔断器
	st := gobreaker.Settings{
		Name:        m.config.Name + "-" + string(peer),
		MaxRequests: m.config.MaxRequests,
		Interval:    m.config.Interval,
		Timeout:     m.config.Timeout,
		ReadyToTrip: m.config.ReadyToTrip,
		OnStateChange: func(name string, from, to gobreaker.State) {
			if m.config.OnStateChange != nil {
				m.config.OnStateChange(name, from, to)
			}
		},
	}

	breaker := gobreaker.NewCircuitBreaker(st)
	actual, _ := m.breakers.LoadOrStore(peer, breaker)
	return actual.(*gobreaker.CircuitBreaker)
}

// GetState 获取指定节点的熔断器状态
func (m *CircuitBreakerMiddleware) GetState(peer model.PeerID) gobreaker.State {
	breaker := m.getBreaker(peer)
	return breaker.State()
}

// GetCounts 获取指定节点的熔断器统计信息
func (m *CircuitBreakerMiddleware) GetCounts(peer model.PeerID) gobreaker.Counts {
	breaker := m.getBreaker(peer)
	return breaker.Counts()
}

// RemoveBreaker 移除指定节点的熔断器
// 用于节点断开连接后释放资源
func (m *CircuitBreakerMiddleware) RemoveBreaker(peer model.PeerID) {
	m.breakers.Delete(peer)
}

// CleanupBreakers 清理所有不在有效节点列表中的熔断器
// 用于定期清理已断开节点的资源
func (m *CircuitBreakerMiddleware) CleanupBreakers(validPeers []model.PeerID) {
	cleanupSyncMap(&m.breakers, validPeers)
}

// BreakerCount 返回当前熔断器数量（用于监控）
func (m *CircuitBreakerMiddleware) BreakerCount() int {
	return countSyncMap(&m.breakers)
}

// 确保实现 Middleware 接口
var _ service.Middleware = (*CircuitBreakerMiddleware)(nil)
