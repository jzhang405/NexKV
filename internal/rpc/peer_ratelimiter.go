// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/ratelimit"
)

// ========================================
// Peer 级别速率限制器（基于 uber.org/ratelimit）
// ========================================

// PeerRateLimiterConfig Peer 级别速率限制器配置
type PeerRateLimiterConfig struct {
	// 默认速率限制（每秒请求数）
	DefaultRate int

	// 最大速率限制（每秒请求数）
	MaxRate int

	// 速率调整窗口
	AdjustWindow time.Duration

	// 是否启用动态速率调整
	EnableDynamicAdjust bool

	// 速率提升因子（当响应快速时）
	RateUpFactor float64

	// 速率降低因子（当响应慢或超时时）
	RateDownFactor float64

	// 最小速率
	MinRate int
}

// DefaultPeerRateLimiterConfig 返回默认配置
func DefaultPeerRateLimiterConfig() *PeerRateLimiterConfig {
	return &PeerRateLimiterConfig{
		DefaultRate:         100,  // 默认每秒 100 个请求
		MaxRate:             1000, // 最大每秒 1000 个请求
		AdjustWindow:        30 * time.Second,
		EnableDynamicAdjust: true,
		RateUpFactor:        1.2, // 响应快时提升 20%
		RateDownFactor:      0.8, // 响应慢时降低 20%
		MinRate:             10,  // 最小每秒 10 个请求
	}
}

// PeerRateLimiter Peer 级别速率限制器
type PeerRateLimiter struct {
	config  *PeerRateLimiterConfig
	metrics *PeerRateLimiterMetrics
	mu      sync.RWMutex

	// peer 级别的速率限制器
	peerLimiters sync.Map // map[peer.ID]ratelimit.Limiter

	// peer 级别的当前速率配置
	peerRates sync.Map // map[peer.ID]int

	// peer 级别的响应时间跟踪（用于动态调整）
	// 使用带互斥锁的列表，避免并发写入时的数据丢失
	peerResponseTimes sync.Map // map[peer.ID]*responseTimeList

	// 动态调整控制
	stopAdjust chan struct{}
	wg         sync.WaitGroup
}

// responseTimeList 响应时间列表（线程安全）
type responseTimeList struct {
	mu    sync.Mutex
	times []time.Duration
}

// PeerRateLimiterMetrics Peer 级别速率限制器指标
type PeerRateLimiterMetrics struct {
	// 调用指标
	CallsTotal     *prometheus.CounterVec
	CallsAllowed   *prometheus.CounterVec
	CallsThrottled *prometheus.CounterVec

	// 速率调整指标
	RateAdjustments *prometheus.CounterVec
	RateUps         *prometheus.CounterVec
	RateDowns       *prometheus.CounterVec

	// 响应时间指标
	ResponseTime *prometheus.HistogramVec
}

// NewPeerRateLimiterMetrics 创建指标
func NewPeerRateLimiterMetrics() *PeerRateLimiterMetrics {
	return &PeerRateLimiterMetrics{
		CallsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_peer_ratelimiter_calls_total",
				Help: "Total calls per peer",
			},
			[]string{"peer_id"},
		),
		CallsAllowed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_peer_ratelimiter_calls_allowed_total",
				Help: "Total allowed calls per peer",
			},
			[]string{"peer_id"},
		),
		CallsThrottled: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_peer_ratelimiter_calls_throttled_total",
				Help: "Total throttled calls per peer",
			},
			[]string{"peer_id"},
		),
		RateAdjustments: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_peer_ratelimiter_rate_adjustments_total",
				Help: "Total rate adjustments per peer",
			},
			[]string{"peer_id"},
		),
		RateUps: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_peer_ratelimiter_rate_ups_total",
				Help: "Total rate increases per peer",
			},
			[]string{"peer_id"},
		),
		RateDowns: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "nexkv_rpc_peer_ratelimiter_rate_downs_total",
				Help: "Total rate decreases per peer",
			},
			[]string{"peer_id"},
		),
		ResponseTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "nexkv_rpc_peer_ratelimiter_response_time_seconds",
				Help:    "Response time per peer",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
			},
			[]string{"peer_id"},
		),
	}
}

// NewPeerRateLimiter 创建 Peer 级别速率限制器
func NewPeerRateLimiter(config *PeerRateLimiterConfig) *PeerRateLimiter {
	if config == nil {
		config = DefaultPeerRateLimiterConfig()
	}

	prl := &PeerRateLimiter{
		config:     config,
		metrics:    NewPeerRateLimiterMetrics(),
		stopAdjust: make(chan struct{}),
	}

	// 启动动态调整 goroutine（如果启用）
	if config.EnableDynamicAdjust {
		prl.wg.Add(1)
		go prl.dynamicAdjustLoop()
	}

	return prl
}

// Allow 检查是否允许调用（阻塞直到允许）
func (p *PeerRateLimiter) Allow(ctx context.Context, peerID peer.ID) error {
	// 记录总调用数
	p.metrics.CallsTotal.WithLabelValues(peerID.String()).Inc()

	// 获取或创建 peer 的速率限制器
	limiter := p.getOrCreateLimiter(peerID)

	// 等待直到允许调用
	// ratelimit.Limiter.Take() 会阻塞直到有可用的令牌
	takeTime := time.Now()
	limiter.Take()
	waitDuration := time.Since(takeTime)

	// 检查是否超时
	select {
	case <-ctx.Done():
		p.metrics.CallsThrottled.WithLabelValues(peerID.String()).Inc()
		return ctx.Err()
	default:
	}

	// 允许调用
	p.metrics.CallsAllowed.WithLabelValues(peerID.String()).Inc()

	// 记录等待时间（用于动态调整）
	p.recordResponseTime(peerID, waitDuration)

	return nil
}

// AllowNow 检查是否允许调用（非阻塞）
func (p *PeerRateLimiter) AllowNow(peerID peer.ID) bool {
	// 记录总调用数
	p.metrics.CallsTotal.WithLabelValues(peerID.String()).Inc()

	// 获取或创建 peer 的速率限制器
	limiter := p.getOrCreateLimiter(peerID)

	// ratelimit.Limiter.Take() 本身会阻塞，所以我们无法实现真正的非阻塞检查
	// 但是我们可以使用 Take() 并假设等待时间很短
	limiter.Take()
	p.metrics.CallsAllowed.WithLabelValues(peerID.String()).Inc()

	return true
}

// getOrCreateLimiter 获取或创建 peer 的速率限制器
func (p *PeerRateLimiter) getOrCreateLimiter(peerID peer.ID) ratelimit.Limiter {
	// 尝试从 sync.Map 获取
	if limiter, ok := p.peerLimiters.Load(peerID); ok {
		return limiter.(ratelimit.Limiter)
	}

	// 不存在，创建新的
	p.mu.Lock()
	defer p.mu.Unlock()

	// 双重检查
	if limiter, ok := p.peerLimiters.Load(peerID); ok {
		return limiter.(ratelimit.Limiter)
	}

	// 创建新的速率限制器（使用 uber.org/ratelimit）
	// ratelimit.New 创建令牌桶限流器，参数为每秒填充的令牌数
	rate := p.config.DefaultRate
	limiter := ratelimit.New(rate, ratelimit.Per(time.Second))

	// 存储到 sync.Map
	p.peerLimiters.Store(peerID, limiter)
	p.peerRates.Store(peerID, rate)

	logging.WithFields(map[string]any{
		"peer_id": peerID,
		"rate":    rate,
	}).Info("创建 Peer 速率限制器")

	return limiter
}

// recordResponseTime 记录响应时间
func (p *PeerRateLimiter) recordResponseTime(peerID peer.ID, duration time.Duration) {
	// 记录到 Prometheus 指标
	p.metrics.ResponseTime.WithLabelValues(peerID.String()).Observe(duration.Seconds())

	// 如果未启用动态调整，直接返回
	if !p.config.EnableDynamicAdjust {
		return
	}

	// 获取或创建响应时间列表
	val, _ := p.peerResponseTimes.LoadOrStore(peerID, &responseTimeList{
		times: make([]time.Duration, 0, 100),
	})
	list := val.(*responseTimeList)

	// 加锁修改
	list.mu.Lock()
	defer list.mu.Unlock()

	list.times = append(list.times, duration)

	// 只保留最近的时间窗口（最多 100 个样本）
	if len(list.times) > 100 {
		list.times = list.times[len(list.times)-100:]
	}
}

// dynamicAdjustLoop 动态调整速率循环
func (p *PeerRateLimiter) dynamicAdjustLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.config.AdjustWindow)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.adjustPeerRates()
		case <-p.stopAdjust:
			return
		}
	}
}

// adjustPeerRates 调整 peer 速率
func (p *PeerRateLimiter) adjustPeerRates() {
	p.peerResponseTimes.Range(func(key, value any) bool {
		peerID := key.(peer.ID)
		list := value.(*responseTimeList)

		// 加锁读取响应时间
		list.mu.Lock()
		if len(list.times) == 0 {
			list.mu.Unlock()
			return true
		}

		// 复制时间列表用于计算，避免长时间持锁
		times := make([]time.Duration, len(list.times))
		copy(times, list.times)
		list.mu.Unlock()

		// 计算平均响应时间
		var total time.Duration
		for _, t := range times {
			total += t
		}
		avgTime := total / time.Duration(len(times))

		// 获取当前速率
		currentRate, _ := p.peerRates.Load(peerID)
		currentRateInt := currentRate.(int)

		// 判断是否需要调整速率
		// 如果平均响应时间很长（> 100ms），降低速率
		// 如果平均响应时间很短（< 10ms），提升速率
		newRate := currentRateInt
		adjusted := false

		if avgTime > 100*time.Millisecond {
			// 响应慢，降低速率
			newRate = int(float64(currentRateInt) * p.config.RateDownFactor)
			if newRate < p.config.MinRate {
				newRate = p.config.MinRate
			}
			adjusted = true

			p.metrics.RateDowns.WithLabelValues(peerID.String()).Inc()
			logging.WithFields(map[string]any{
				"peer_id":  peerID,
				"old_rate": currentRateInt,
				"new_rate": newRate,
				"avg_time": avgTime,
				"reason":   "slow_response",
			}).Info("降低 Peer 速率")
		} else if avgTime < 10*time.Millisecond && currentRateInt < p.config.MaxRate {
			// 响应快，提升速率
			newRate = int(float64(currentRateInt) * p.config.RateUpFactor)
			if newRate > p.config.MaxRate {
				newRate = p.config.MaxRate
			}
			adjusted = true

			p.metrics.RateUps.WithLabelValues(peerID.String()).Inc()
			logging.WithFields(map[string]any{
				"peer_id":  peerID,
				"old_rate": currentRateInt,
				"new_rate": newRate,
				"avg_time": avgTime,
				"reason":   "fast_response",
			}).Info("提升 Peer 速率")
		}

		if adjusted {
			// 更新速率
			p.peerRates.Store(peerID, newRate)

			// 创建新的速率限制器
			newLimiter := ratelimit.New(newRate, ratelimit.Per(time.Second))
			p.peerLimiters.Store(peerID, newLimiter)

			// 记录调整
			p.metrics.RateAdjustments.WithLabelValues(peerID.String()).Inc()

			// 清空响应时间列表（创建新的 responseTimeList）
			p.peerResponseTimes.Store(peerID, &responseTimeList{
				times: make([]time.Duration, 0, 100),
			})
		}

		return true
	})
}

// SetPeerRate 手动设置 peer 的速率
func (p *PeerRateLimiter) SetPeerRate(peerID peer.ID, rate int) error {
	if rate < p.config.MinRate || rate > p.config.MaxRate {
		return NewRPCError(ErrCodeInvalidArgument, "速率超出范围")
	}

	// sync.Map 本身是并发安全的，不需要额外加锁
	p.peerRates.Store(peerID, rate)

	// 创建新的速率限制器
	newLimiter := ratelimit.New(rate, ratelimit.Per(time.Second))
	p.peerLimiters.Store(peerID, newLimiter)

	logging.WithFields(map[string]any{
		"peer_id": peerID,
		"rate":    rate,
	}).Info("手动设置 Peer 速率")

	return nil
}

// GetPeerRate 获取 peer 的当前速率
func (p *PeerRateLimiter) GetPeerRate(peerID peer.ID) int {
	if rate, ok := p.peerRates.Load(peerID); ok {
		return rate.(int)
	}
	return p.config.DefaultRate
}

// RemovePeer 移除 peer 的速率限制器
func (p *PeerRateLimiter) RemovePeer(peerID peer.ID) {
	p.peerLimiters.Delete(peerID)
	p.peerRates.Delete(peerID)
	p.peerResponseTimes.Delete(peerID)

	logging.WithField("peer_id", peerID).Info("移除 Peer 速率限制器")
}

// Close 关闭速率限制器
func (p *PeerRateLimiter) Close() error {
	close(p.stopAdjust)
	p.wg.Wait()

	logging.Info("Peer 速率限制器已关闭")
	return nil
}
