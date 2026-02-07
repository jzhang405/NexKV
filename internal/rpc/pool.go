// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus"
)

// ConnectionPool 连接池
type ConnectionPool struct {
	host       host.Host
	cache      *StreamCache
	maxStreams int
	metrics    *PoolMetrics
	mu         sync.RWMutex
}

// PoolConfig 连接池配置
type PoolConfig struct {
	MaxStreams  int           // 每个 peer 最大 Stream 数
	StreamTTL   time.Duration // Stream 最大存活时间
	MaxMessages uint64        // 单 Stream 最大消息数
}

// DefaultPoolConfig 返回默认配置
func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		MaxStreams:  10,
		StreamTTL:   DefaultStreamTTL,
		MaxMessages: DefaultMaxMessagesPerStream,
	}
}

// PoolMetrics 连接池指标
type PoolMetrics struct {
	GetTotal      prometheus.Counter
	GetSuccess    prometheus.Counter
	GetError      prometheus.Counter
	ReturnTotal   prometheus.Counter
	ActiveStreams prometheus.Gauge
	CacheHitRate  prometheus.Gauge
}

// NewPoolMetrics 创建连接池指标
func NewPoolMetrics() *PoolMetrics {
	return &PoolMetrics{
		GetTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_pool_get_total",
			Help: "Total pool get operations",
		}),
		GetSuccess: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_pool_get_success_total",
			Help: "Total successful pool get operations",
		}),
		GetError: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_pool_get_error_total",
			Help: "Total failed pool get operations",
		}),
		ReturnTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_pool_return_total",
			Help: "Total pool return operations",
		}),
		ActiveStreams: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_pool_active_streams",
			Help: "Active streams in pool",
		}),
		CacheHitRate: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_pool_cache_hit_rate",
			Help: "Cache hit rate (0-1)",
		}),
	}
}

// NewConnectionPool 创建连接池
func NewConnectionPool(h host.Host, cfg *PoolConfig) *ConnectionPool {
	if cfg == nil {
		cfg = DefaultPoolConfig()
	}

	return &ConnectionPool{
		host:       h,
		cache:      NewStreamCache(cfg.StreamTTL, cfg.MaxMessages),
		maxStreams: cfg.MaxStreams,
		metrics:    NewPoolMetrics(),
	}
}

// GetStream 获取 Stream（优先从缓存）
func (p *ConnectionPool) GetStream(ctx context.Context, pid peer.ID) (network.Stream, error) {
	p.metrics.GetTotal.Inc()

	// 尝试从缓存获取
	stream, err := p.cache.Get(ctx, p.host, pid)
	if err != nil {
		p.metrics.GetError.Inc()
		return nil, fmt.Errorf("从缓存获取 Stream 失败: %w", err)
	}

	p.metrics.GetSuccess.Inc()
	p.updateActiveStreams()

	logging.WithFields(map[string]any{
		"peer_id": pid,
	}).Debug("从连接池获取 Stream")

	return stream, nil
}

// ReturnStream 返回 Stream 到缓存
func (p *ConnectionPool) ReturnStream(stream network.Stream) error {
	if stream == nil {
		return fmt.Errorf("stream 为空")
	}

	p.metrics.ReturnTotal.Inc()

	// 放回缓存
	if err := p.cache.Put(stream); err != nil {
		logging.WithFields(map[string]any{
			"error":     err,
			"stream_id": stream.ID(),
		}).Warn("返回 Stream 到缓存失败")
		return err
	}

	p.updateActiveStreams()

	logging.WithField("stream_id", stream.ID()).Debug("Stream 已返回到连接池")

	return nil
}

// Close 关闭连接池
func (p *ConnectionPool) Close() error {
	return p.cache.Close()
}

// updateActiveStreams 更新活跃 Stream 数量
func (p *ConnectionPool) updateActiveStreams() {
	stats := p.cache.Stats()
	p.metrics.ActiveStreams.Set(float64(stats.ActiveStreams))

	// 注意：缓存命中率由 StreamCache 内部维护
	// 这里只更新活跃 Stream 数量
}

// Stats 获取连接池统计信息
func (p *ConnectionPool) Stats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	cacheStats := p.cache.Stats()

	return PoolStats{
		ActiveStreams: cacheStats.ActiveStreams,
		MaxStreams:    p.maxStreams,
		StreamTTL:     cacheStats.TTL,
		MaxMessages:   cacheStats.MaxMessages,
	}
}

// PoolStats 连接池统计信息
type PoolStats struct {
	ActiveStreams int
	MaxStreams    int
	StreamTTL     time.Duration
	MaxMessages   uint64
}
