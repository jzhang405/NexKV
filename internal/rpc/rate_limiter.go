// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/prometheus/client_golang/prometheus"
)

// ========================================
// 连接限流器配置
// ========================================

// init 注册 Prometheus 指标
func init() {
	metrics := NewRateLimiterMetrics()
	prometheus.MustRegister(metrics.ConnectionTotal)
	prometheus.MustRegister(metrics.ConnectionAccepted)
	prometheus.MustRegister(metrics.ConnectionRejected)
	prometheus.MustRegister(metrics.ConnectionActive)
	prometheus.MustRegister(metrics.ConnectionTimeout)
	prometheus.MustRegister(metrics.TokenBucketRefill)
	prometheus.MustRegister(metrics.TokenBucketExhausted)
	prometheus.MustRegister(metrics.AdjustmentTotal)
	prometheus.MustRegister(metrics.AdjustmentUp)
	prometheus.MustRegister(metrics.AdjustmentDown)
}

// RateLimiterConfig 限流器配置
type RateLimiterConfig struct {
	// 令牌桶参数
	MaxConnections int           // 最大并发连接数
	RefillRate     time.Duration // 令牌补充间隔
	RefillAmount   int           // 每次补充的令牌数
	BucketSize     int           // 令牌桶大小

	// 超时配置
	AcquireTimeout time.Duration // 获取令牌超时时间

	// 动态调整
	EnableAutoAdjust   bool          // 是否启用自动调整
	MinConnections     int           // 最小连接数
	AutoMaxConnections int           // 自动调整上限
	AdjustInterval     time.Duration // 调整间隔
	AdjustThreshold    float64       // 调整阈值（CPU/内存使用率）
}

// DefaultRateLimiterConfig 返回默认限流器配置
func DefaultRateLimiterConfig() *RateLimiterConfig {
	return &RateLimiterConfig{
		MaxConnections:     100,
		RefillRate:         100 * time.Millisecond,
		RefillAmount:       10,
		BucketSize:         100,
		AcquireTimeout:     5 * time.Second,
		EnableAutoAdjust:   false,
		MinConnections:     50,
		AutoMaxConnections: 200,
		AdjustInterval:     30 * time.Second,
		AdjustThreshold:    0.8,
	}
}

// ========================================
// 连接限流器实现
// ========================================

// RateLimiter 连接限流器（令牌桶 + 并发控制）
type RateLimiter struct {
	config  *RateLimiterConfig
	metrics *RateLimiterMetrics
	mu      sync.RWMutex

	// 令牌桶
	bucket     chan struct{}
	bucketSize int

	// 并发控制
	semaphore chan struct{}

	// 当前连接数
	currentConnections int

	// 自动调整
	stopAdjust chan struct{}
	wg         sync.WaitGroup
}

// RateLimiterMetrics 限流器指标
type RateLimiterMetrics struct {
	// 连接指标
	ConnectionTotal    prometheus.Counter
	ConnectionAccepted prometheus.Counter
	ConnectionRejected prometheus.Counter
	ConnectionActive   prometheus.Gauge
	ConnectionTimeout  prometheus.Counter

	// 令牌桶指标
	TokenBucketRefill    prometheus.Counter
	TokenBucketExhausted prometheus.Counter

	// 动态调整指标
	AdjustmentTotal prometheus.Counter
	AdjustmentUp    prometheus.Counter
	AdjustmentDown  prometheus.Counter
}

// NewRateLimiter 创建连接限流器
func NewRateLimiter(config *RateLimiterConfig) *RateLimiter {
	if config == nil {
		config = DefaultRateLimiterConfig()
	}

	rl := &RateLimiter{
		config:             config,
		metrics:            NewRateLimiterMetrics(),
		bucket:             make(chan struct{}, config.BucketSize),
		bucketSize:         config.BucketSize,
		semaphore:          make(chan struct{}, config.MaxConnections),
		stopAdjust:         make(chan struct{}),
		currentConnections: 0,
	}

	// 初始化令牌桶
	for i := 0; i < config.BucketSize; i++ {
		rl.bucket <- struct{}{}
	}

	// 启动令牌补充 goroutine
	rl.wg.Add(1)
	go rl.refillLoop()

	// 启动自动调整 goroutine（如果启用）
	if config.EnableAutoAdjust {
		rl.wg.Add(1)
		go rl.autoAdjustLoop()
	}

	return rl
}

// refillLoop 令牌补充循环
func (r *RateLimiter) refillLoop() {
	defer r.wg.Done()

	// 缓存配置值到局部变量，避免竞态条件
	r.mu.RLock()
	refillRate := r.config.RefillRate
	refillAmount := r.config.RefillAmount
	r.mu.RUnlock()

	ticker := time.NewTicker(refillRate)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 补充令牌
		RefillLoop:
			for i := 0; i < refillAmount; i++ {
				select {
				case r.bucket <- struct{}{}:
					// 成功补充令牌
					r.metrics.TokenBucketRefill.Inc()
				default:
					// 桶已满，停止补充
					break RefillLoop
				}
			}
		case <-r.stopAdjust:
			return
		}
	}
}

// autoAdjustLoop 自动调整循环
func (r *RateLimiter) autoAdjustLoop() {
	defer r.wg.Done()

	// 缓存配置值到局部变量，避免竞态条件
	r.mu.RLock()
	adjustInterval := r.config.AdjustInterval
	r.mu.RUnlock()

	ticker := time.NewTicker(adjustInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.adjustConnections()
		case <-r.stopAdjust:
			return
		}
	}
}

// adjustConnections 动态调整连接数
func (r *RateLimiter) adjustConnections() {
	r.mu.Lock()
	current := r.currentConnections
	maxConn := r.config.MaxConnections
	r.mu.Unlock()

	// 获取系统资源使用情况
	usageRate := float64(current) / float64(maxConn)

	// 简单的内存压力检测
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memoryPressure := float64(m.Alloc) / float64(m.Sys)

	// 综合判断：连接使用率或内存压力过高时增加限制
	// 连接使用率或内存压力较低时减少限制
	if (usageRate > r.config.AdjustThreshold || memoryPressure > 0.8) && maxConn < r.config.AutoMaxConnections {
		r.increaseMaxConnections()
	} else if usageRate < 0.3 && memoryPressure < 0.5 && maxConn > r.config.MinConnections {
		r.decreaseMaxConnections()
	}
}

// increaseMaxConnections 增加最大连接数
func (r *RateLimiter) increaseMaxConnections() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.config.MaxConnections >= r.config.AutoMaxConnections {
		return
	}

	oldMax := r.config.MaxConnections
	// 使用 Go 1.21+ 内置的 min 函数
	newMax := min(r.config.MaxConnections*2, r.config.AutoMaxConnections)
	r.config.MaxConnections = newMax

	// 扩展信号量
	for range newMax - oldMax {
		r.semaphore <- struct{}{}
	}

	r.metrics.AdjustmentTotal.Inc()
	r.metrics.AdjustmentUp.Inc()

	logging.WithFields(map[string]any{
		"old_max": oldMax,
		"new_max": newMax,
	}).Info("限流器：增加最大连接数")
}

// decreaseMaxConnections 减少最大连接数
func (r *RateLimiter) decreaseMaxConnections() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.config.MaxConnections <= r.config.MinConnections {
		return
	}

	oldMax := r.config.MaxConnections
	// 使用 Go 1.21+ 内置的 max 函数
	newMax := max(r.config.MaxConnections/2, r.config.MinConnections)
	r.config.MaxConnections = newMax

	// 收缩信号量
	for range oldMax - newMax {
		<-r.semaphore
	}

	r.metrics.AdjustmentTotal.Inc()
	r.metrics.AdjustmentDown.Inc()

	logging.WithFields(map[string]any{
		"old_max": oldMax,
		"new_max": newMax,
	}).Info("限流器：减少最大连接数")
}

// ========================================
// 限流接口
// ========================================

// Acquire 尝试获取连接许可（阻塞）
func (r *RateLimiter) Acquire(ctx context.Context) error {
	// 记录总连接尝试
	r.metrics.ConnectionTotal.Inc()

	// 检查上下文是否已取消
	select {
	case <-ctx.Done():
		r.metrics.ConnectionRejected.Inc()
		return ctx.Err()
	default:
	}

	// 超时上下文
	ctx, cancel := context.WithTimeout(ctx, r.config.AcquireTimeout)
	defer cancel()

	// 第一步：获取令牌
	select {
	case <-r.bucket:
		// 获取令牌成功，继续获取信号量
	case <-ctx.Done():
		// 超时或取消（令牌获取失败）
		if ctx.Err() == context.DeadlineExceeded {
			r.metrics.ConnectionTimeout.Inc()
		}
		r.metrics.ConnectionRejected.Inc()
		return ctx.Err()
	}

	// 第二步：获取信号量（会阻塞直到有可用或超时）
	select {
	case r.semaphore <- struct{}{}:
		// 获取信号量成功
		r.mu.Lock()
		r.currentConnections++
		r.metrics.ConnectionActive.Set(float64(r.currentConnections))
		r.mu.Unlock()

		r.metrics.ConnectionAccepted.Inc()
		return nil
	case <-ctx.Done():
		// 超时或取消（信号量获取失败），归还令牌
		r.bucket <- struct{}{}
		if ctx.Err() == context.DeadlineExceeded {
			r.metrics.ConnectionTimeout.Inc()
		}
		r.metrics.ConnectionRejected.Inc()
		return ctx.Err()
	}
}

// TryAcquire 尝试获取连接许可（非阻塞）
func (r *RateLimiter) TryAcquire() bool {
	r.metrics.ConnectionTotal.Inc()

	select {
	case <-r.bucket:
		select {
		case r.semaphore <- struct{}{}:
			r.mu.Lock()
			r.currentConnections++
			r.metrics.ConnectionActive.Set(float64(r.currentConnections))
			r.mu.Unlock()

			r.metrics.ConnectionAccepted.Inc()
			return true
		default:
			// 信号量已满，归还令牌
			r.bucket <- struct{}{}
			r.metrics.ConnectionRejected.Inc()
			return false
		}
	default:
		r.metrics.ConnectionRejected.Inc()
		return false
	}
}

// Release 释放连接许可
func (r *RateLimiter) Release() {
	r.mu.Lock()
	r.currentConnections--
	r.metrics.ConnectionActive.Set(float64(r.currentConnections))
	r.mu.Unlock()

	// 释放信号量（非阻塞，避免死锁）
	select {
	case <-r.semaphore:
		// 成功释放
	default:
		// 信号量已满，记录异常但不阻塞
		// 这通常意味着 Release 被调用了多次，属于配额不匹配
		logging.Warn("尝试释放信号量时发现已满，可能存在配额不匹配")
	}

	// 补充令牌（保持桶满）
	select {
	case r.bucket <- struct{}{}:
	default:
		// 桶已满
	}
}

// GetCurrentStats 获取当前统计信息
func (r *RateLimiter) GetCurrentStats() RateLimiterStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return RateLimiterStats{
		CurrentConnections: r.currentConnections,
		MaxConnections:     r.config.MaxConnections,
		BucketSize:         len(r.bucket),
		AvailableTokens:    len(r.bucket),
	}
}

// UpdateConfig 更新配置
func (r *RateLimiter) UpdateConfig(config *RateLimiterConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 验证配置
	if config.MaxConnections < r.currentConnections {
		return NewRPCError(ErrCodeInvalidArgument, "新最大连接数不能小于当前连接数")
	}

	oldMax := r.config.MaxConnections
	newMax := config.MaxConnections

	if newMax > oldMax {
		// 扩展信号量
		for i := 0; i < newMax-oldMax; i++ {
			r.semaphore <- struct{}{}
		}
	} else if newMax < oldMax {
		// 收缩信号量：使用异步方式，避免阻塞
		shrinkBy := oldMax - newMax
		timeout := time.After(5 * time.Second)
		for i := 0; i < shrinkBy; i++ {
			select {
			case <-r.semaphore:
				// 成功收缩
			case <-timeout:
				return NewRPCError(ErrCodeTimeout, "收缩信号量超时，请稍后重试")
			}
		}
	}

	r.config = config

	logging.WithFields(map[string]any{
		"old_max": oldMax,
		"new_max": newMax,
	}).Info("限流器配置已更新")

	return nil
}

// Close 关闭限流器
func (r *RateLimiter) Close() error {
	close(r.stopAdjust)
	r.wg.Wait()

	logging.Info("限流器已关闭")
	return nil
}

// ========================================
// 统计信息
// ========================================

// RateLimiterStats 限流器统计信息
type RateLimiterStats struct {
	CurrentConnections int
	MaxConnections     int
	BucketSize         int
	AvailableTokens    int
}

// ========================================
// 监控指标
// ========================================

// NewRateLimiterMetrics 创建限流器指标
func NewRateLimiterMetrics() *RateLimiterMetrics {
	return &RateLimiterMetrics{
		ConnectionTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_connection_total",
			Help: "Total connection attempts",
		}),
		ConnectionAccepted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_connection_accepted_total",
			Help: "Total accepted connections",
		}),
		ConnectionRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_connection_rejected_total",
			Help: "Total rejected connections",
		}),
		ConnectionActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_ratelimiter_connections_active",
			Help: "Currently active connections",
		}),
		ConnectionTimeout: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_connection_timeout_total",
			Help: "Total timed out connection attempts",
		}),
		TokenBucketRefill: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_token_refill_total",
			Help: "Total token bucket refills",
		}),
		TokenBucketExhausted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_token_exhausted_total",
			Help: "Total token bucket exhausted events",
		}),
		AdjustmentTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_adjustment_total",
			Help: "Total configuration adjustments",
		}),
		AdjustmentUp: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_adjustment_up_total",
			Help: "Total increases to max connections",
		}),
		AdjustmentDown: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_ratelimiter_adjustment_down_total",
			Help: "Total decreases to max connections",
		}),
	}
}
