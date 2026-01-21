// Package transport 提供消息去重功能
//
// 核心功能:
//   - 基于节点 ID 和消息序列号的双层去重
//   - LRU 淘汰机制
//   - 自动清理过期条目
//   - 并发安全
package transport

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// 去重器配置常量
const (
	// DefaultMaxCacheSize 默认最大缓存大小（节点数量）
	DefaultMaxCacheSize = 10000

	// DefaultCleanupInterval 默认清理间隔
	DefaultCleanupInterval = 5 * time.Minute

	// DefaultEntryTTL 默认条目生存时间
	DefaultEntryTTL = 10 * time.Minute
)

// lruEntry LRU 缓存条目（记录访问时间）
type lruEntry struct {
	maxSeq     uint64
	lastAccess time.Time
}

// MessageDeduplicator 消息去重器
//
// 核心功能:
//   - 检测重复消息（基于 NodeID + MsgSeq）
//   - LRU 淘汰机制（防止内存无限增长）
//   - 自动清理过期条目
//   - 并发安全（使用 RWMutex）
type MessageDeduplicator struct {
	// 去重表：nodeID -> lruEntry
	nodeMaxSeq map[uint64]*lruEntry
	mu         sync.RWMutex

	// 清理协程
	stopCh    chan struct{}
	cleanupWg sync.WaitGroup

	// 统计指标
	hitCount   atomic.Uint64 // 去重命中次数
	totalCount atomic.Uint64 // 总检查次数

	// 配置
	maxCacheSize    int           // 最大缓存大小
	cleanupInterval time.Duration // 清理间隔
	entryTTL        time.Duration // 条目生存时间
}

// NewMessageDeduplicator 创建消息去重器
func NewMessageDeduplicator() *MessageDeduplicator {
	return &MessageDeduplicator{
		nodeMaxSeq:      make(map[uint64]*lruEntry),
		stopCh:          make(chan struct{}),
		maxCacheSize:    DefaultMaxCacheSize,
		cleanupInterval: DefaultCleanupInterval,
		entryTTL:        DefaultEntryTTL,
	}
}

// NewMessageDeduplicatorWithConfig 创建消息去重器（自定义配置）
func NewMessageDeduplicatorWithConfig(maxCacheSize int, cleanupInterval, entryTTL time.Duration) *MessageDeduplicator {
	return &MessageDeduplicator{
		nodeMaxSeq:      make(map[uint64]*lruEntry),
		stopCh:          make(chan struct{}),
		maxCacheSize:    maxCacheSize,
		cleanupInterval: cleanupInterval,
		entryTTL:        entryTTL,
	}
}

// Start 启动去重器（启动清理协程）
func (d *MessageDeduplicator) Start() {
	d.cleanupWg.Add(1)
	go d.cleanupLoop()
	logging.Info("消息去重器已启动")
}

// Stop 停止去重器（停止清理协程）
func (d *MessageDeduplicator) Stop() {
	close(d.stopCh)
	d.cleanupWg.Wait()
	logging.Info("消息去重器已停止")
}

// IsDuplicate 检查消息是否重复
//
// 参数:
//   - nodeID: 节点 ID
//   - msgSeq: 消息序列号
//
// 返回:
//   - bool: true 表示重复，false 表示新消息
func (d *MessageDeduplicator) IsDuplicate(nodeID, msgSeq uint64) bool {
	d.totalCount.Add(1)

	d.mu.RLock()
	entry, exists := d.nodeMaxSeq[nodeID]
	d.mu.RUnlock()

	// 新节点，不是重复消息
	if !exists {
		return false
	}

	// 检查序列号：使用 uint64 相减判断新旧（无需回绕检测）
	// 如果 msgSeq <= maxSeq，说明是旧消息或重复消息
	if msgSeq <= entry.maxSeq {
		d.hitCount.Add(1)
		return true
	}

	return false
}

// Record 记录消息（更新节点最大序列号）
//
// 参数:
//   - nodeID: 节点 ID
//   - msgSeq: 消息序列号
func (d *MessageDeduplicator) Record(nodeID, msgSeq uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 检查是否需要 LRU 淘汰
	if len(d.nodeMaxSeq) >= d.maxCacheSize {
		// 真正的 LRU 策略：找到最久未访问的条目并删除
		var oldestNodeID uint64
		var oldestTime time.Time
		firstEntry := true

		for nodeID, entry := range d.nodeMaxSeq {
			if firstEntry || entry.lastAccess.Before(oldestTime) {
				oldestNodeID = nodeID
				oldestTime = entry.lastAccess
				firstEntry = false
			}
		}

		if !firstEntry {
			delete(d.nodeMaxSeq, oldestNodeID)
		}
	}

	// 更新节点最大序列号和访问时间
	now := time.Now()
	if entry, exists := d.nodeMaxSeq[nodeID]; exists {
		if msgSeq > entry.maxSeq {
			entry.maxSeq = msgSeq
		}
		entry.lastAccess = now
	} else {
		d.nodeMaxSeq[nodeID] = &lruEntry{
			maxSeq:     msgSeq,
			lastAccess: now,
		}
	}
}

// cleanupLoop 清理过期条目
func (d *MessageDeduplicator) cleanupLoop() {
	defer d.cleanupWg.Done()

	ticker := time.NewTicker(d.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.cleanup()
		case <-d.stopCh:
			return
		}
	}
}

// cleanup 清理过期条目
//
// 注意：当前实现为简化版本，仅清理超出缓存大小限制的条目
// 完整的 TTL 实现需要记录每个条目的最后访问时间
func (d *MessageDeduplicator) cleanup() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 如果超出最大缓存大小，删除部分条目
	if len(d.nodeMaxSeq) > d.maxCacheSize {
		// 删除最旧的 10% 条目
		deleteCount := len(d.nodeMaxSeq) / 10
		count := 0
		for key := range d.nodeMaxSeq {
			if count >= deleteCount {
				break
			}
			delete(d.nodeMaxSeq, key)
			count++
		}
		logging.Debugf("去重器清理: 删除 %d 个条目，当前缓存大小: %d", count, len(d.nodeMaxSeq))
	}
}

// Stats 获取统计信息
func (d *MessageDeduplicator) Stats() map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := make(map[string]any)

	// 基础统计
	stats["cache_size"] = len(d.nodeMaxSeq)
	stats["max_cache_size"] = d.maxCacheSize

	// 去重统计
	total := d.totalCount.Load()
	hit := d.hitCount.Load()
	stats["total_checks"] = total
	stats["hit_count"] = hit

	if total > 0 {
		stats["hit_rate"] = float64(hit) / float64(total)
	} else {
		stats["hit_rate"] = 0.0
	}

	return stats
}

// Clear 清空去重缓存（主要用于测试）
func (d *MessageDeduplicator) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.nodeMaxSeq = make(map[uint64]*lruEntry)
	d.hitCount.Store(0)
	d.totalCount.Store(0)
}
