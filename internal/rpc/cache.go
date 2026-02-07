// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/transport"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// DefaultStreamTTL Stream 默认存活时间
	DefaultStreamTTL = 5 * time.Minute

	// DefaultMaxMessagesPerStream 单个 Stream 默认最大消息数
	DefaultMaxMessagesPerStream = 1000

	// DefaultCleanupInterval 默认清理间隔
	DefaultCleanupInterval = 1 * time.Minute
)

// StreamCache Stream 缓存（按 peer ID 分组）
type StreamCache struct {
	caches      map[peer.ID]*streamEntry
	mu          sync.RWMutex
	ttl         time.Duration
	maxMessages uint64
	metrics     *CacheMetrics
	stopCh      chan struct{}
}

// streamEntry 单个 Stream 缓存条目
type streamEntry struct {
	stream       network.Stream
	createdAt    time.Time
	lastUsedAt   time.Time
	messageCount uint64
}

// CacheMetrics 缓存指标
type CacheMetrics struct {
	Hit     prometheus.Counter
	Miss    prometheus.Counter
	Created prometheus.Counter
	Expired prometheus.Counter
	Active  prometheus.Gauge
}

// NewCacheMetrics 创建缓存指标
func NewCacheMetrics() *CacheMetrics {
	return &CacheMetrics{
		Hit: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_cache_hit_total",
			Help: "Total cache hits",
		}),
		Miss: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_cache_miss_total",
			Help: "Total cache misses",
		}),
		Created: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_cache_created_total",
			Help: "Total streams created",
		}),
		Expired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nexkv_rpc_cache_expired_total",
			Help: "Total streams expired",
		}),
		Active: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nexkv_rpc_cache_active_streams",
			Help: "Active cached streams",
		}),
	}
}

// NewStreamCache 创建 Stream 缓存
func NewStreamCache(ttl time.Duration, maxMessages uint64) *StreamCache {
	cache := &StreamCache{
		caches:      make(map[peer.ID]*streamEntry),
		ttl:         ttl,
		maxMessages: maxMessages,
		metrics:     NewCacheMetrics(),
		stopCh:      make(chan struct{}),
	}

	// 启动后台清理协程
	go cache.cleanupLoop()

	return cache
}

// Get 获取或创建 Stream
func (c *StreamCache) Get(ctx context.Context, h host.Host, pid peer.ID) (network.Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 检查缓存
	if entry, ok := c.caches[pid]; ok && c.isValid(entry) {
		entry.lastUsedAt = time.Now()
		entry.messageCount++
		c.metrics.Hit.Inc()
		logging.WithField("peer_id", pid).Debug("Stream 缓存命中")
		return entry.stream, nil
	}

	// 创建新 Stream
	stream, err := h.NewStream(ctx, pid, transport.ProtocolNexKVRPC)
	if err != nil {
		c.metrics.Miss.Inc()
		return nil, err
	}

	c.caches[pid] = &streamEntry{
		stream:       stream,
		createdAt:    time.Now(),
		lastUsedAt:   time.Now(),
		messageCount: 1,
	}
	c.metrics.Created.Inc()
	c.metrics.Active.Set(float64(len(c.caches)))

	logging.WithFields(map[string]any{
		"peer_id":    pid,
		"cache_size": len(c.caches),
	}).Info("创建新 Stream")

	return stream, nil
}

// Put 将 Stream 放回缓存
func (c *StreamCache) Put(stream network.Stream) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 获取 remote peer ID
	pid := stream.Conn().RemotePeer()

	// 检查是否已存在
	if _, ok := c.caches[pid]; ok {
		return nil // 已存在，不重复缓存
	}

	// 添加到缓存
	c.caches[pid] = &streamEntry{
		stream:       stream,
		createdAt:    time.Now(),
		lastUsedAt:   time.Now(),
		messageCount: 0,
	}
	c.metrics.Active.Set(float64(len(c.caches)))

	return nil
}

// isValid 检查 Stream 是否有效
func (c *StreamCache) isValid(entry *streamEntry) bool {
	// 检查存活时间
	if time.Since(entry.createdAt) > c.ttl {
		return false
	}

	// 检查消息数
	if entry.messageCount >= c.maxMessages {
		return false
	}

	return true
}

// cleanupLoop 后台清理过期 Stream
func (c *StreamCache) cleanupLoop() {
	ticker := time.NewTicker(DefaultCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.Cleanup()
		case <-c.stopCh:
			return
		}
	}
}

// Cleanup 清理过期 Stream
func (c *StreamCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	expiredCount := 0
	now := time.Now()

	for pid, entry := range c.caches {
		// 检查是否过期
		if now.Sub(entry.createdAt) > c.ttl || entry.messageCount >= c.maxMessages {
			// 关闭 Stream
			if err := entry.stream.Close(); err != nil {
				logging.WithFields(map[string]any{
					"peer_id": pid,
					"error":   err,
				}).Warn("关闭过期 Stream 失败")
			}

			delete(c.caches, pid)
			c.metrics.Expired.Inc()
			expiredCount++
		}
	}

	c.metrics.Active.Set(float64(len(c.caches)))

	if expiredCount > 0 {
		logging.WithFields(map[string]any{
			"expired_count": expiredCount,
			"cache_size":    len(c.caches),
		}).Debug("清理过期 Stream 完成")
	}
}

// Close 关闭缓存
func (c *StreamCache) Close() error {
	close(c.stopCh)

	c.mu.Lock()
	defer c.mu.Unlock()

	// 关闭所有 Stream
	for pid, entry := range c.caches {
		if err := entry.stream.Close(); err != nil {
			logging.WithFields(map[string]any{
				"peer_id": pid,
				"error":   err,
			}).Warn("关闭 Stream 失败")
		}
	}

	c.caches = make(map[peer.ID]*streamEntry)
	c.metrics.Active.Set(0)

	return nil
}

// Stats 获取缓存统计信息
func (c *StreamCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CacheStats{
		ActiveStreams: len(c.caches),
		TTL:           c.ttl,
		MaxMessages:   c.maxMessages,
	}
}

// CacheStats 缓存统计信息
type CacheStats struct {
	ActiveStreams int
	TTL           time.Duration
	MaxMessages   uint64
}
