// Package consistency 提供 2PC 强一致性协调器实现
//
// 强制优化 7.3: Gossip 消息限流器
// 使用令牌桶算法限制 Gossip 消息发送速率，防止网络拥塞
package consistency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
)

// ==================== 令牌桶限流器 ====================

// GossipRateLimiter Gossip 消息限流器
//
// 设计思路：
//   - 使用令牌桶算法（Token Bucket）
//   - 支持配置化的速率和突发容量
//   - 支持按节点粒度限流
//   - 提供监控指标
type GossipRateLimiter struct {
	mu sync.Mutex

	// 全局限流器配置
	rate       float64   // 每秒产生的令牌数
	burst      int       // 桶的最大容量（突发容量）
	tokens     float64   // 当前令牌数
	lastUpdate time.Time // 上次更新时间

	// 节点级限流器配置
	perNodeRate  float64 // 每个节点每秒产生的令牌数
	perNodeBurst int     // 每个节点的桶最大容量

	// 按节点粒度的限流器
	nodeLimiters map[string]*nodeRateLimiter

	// 监控指标
	metrics *RateLimiterMetrics
}

// nodeRateLimiter 单节点限流器
type nodeRateLimiter struct {
	tokens     float64
	lastUpdate time.Time
}

// RateLimiterConfig 限流器配置
type RateLimiterConfig struct {
	// Rate 每秒产生的令牌数（默认 100）
	Rate float64

	// Burst 桶的最大容量（默认 200）
	Burst int

	// PerNodeRate 每个节点的速率限制（默认 20）
	PerNodeRate float64

	// PerNodeBurst 每个节点的突发容量（默认 50）
	PerNodeBurst int

	// Enabled 是否启用限流（默认 true）
	Enabled bool
}

// DefaultRateLimiterConfig 返回默认限流器配置
func DefaultRateLimiterConfig() *RateLimiterConfig {
	return &RateLimiterConfig{
		Rate:         100, // 全局每秒 100 条消息
		Burst:        200, // 突发 200 条
		PerNodeRate:  20,  // 每节点每秒 20 条
		PerNodeBurst: 50,  // 每节点突发 50 条
		Enabled:      true,
	}
}

// RateLimiterMetrics 限流器监控指标
type RateLimiterMetrics struct {
	mu sync.RWMutex

	// 全局指标
	TotalMessages   int64 // 总消息数
	AllowedMessages int64 // 允许的消息数
	DroppedMessages int64 // 丢弃的消息数

	// 节点级指标
	NodeMessages   map[string]int64 // 每个节点的消息数
	NodeDropped    map[string]int64 // 每个节点丢弃的消息数
	LastUpdateTime time.Time        // 最后更新时间
}

// NewGossipRateLimiter 创建 Gossip 消息限流器
func NewGossipRateLimiter(config *RateLimiterConfig) *GossipRateLimiter {
	if config == nil {
		config = DefaultRateLimiterConfig()
	}

	// 确保配置有效
	if config.Rate <= 0 {
		config.Rate = 100
	}
	if config.Burst <= 0 {
		config.Burst = 200
	}
	if config.PerNodeRate <= 0 {
		config.PerNodeRate = 20
	}
	if config.PerNodeBurst <= 0 {
		config.PerNodeBurst = 50
	}

	now := time.Now()
	return &GossipRateLimiter{
		rate:         config.Rate,
		burst:        config.Burst,
		tokens:       float64(config.Burst), // 初始填满令牌
		lastUpdate:   now,
		perNodeRate:  config.PerNodeRate,
		perNodeBurst: config.PerNodeBurst,
		nodeLimiters: make(map[string]*nodeRateLimiter),
		metrics: &RateLimiterMetrics{
			NodeMessages: make(map[string]int64),
			NodeDropped:  make(map[string]int64),
		},
	}
}

// Allow 检查是否允许发送消息（全局限流）
//
// 返回 true 表示允许，false 表示需要等待
func (r *GossipRateLimiter) Allow() bool {
	return r.AllowN(1)
}

// AllowN 检查是否允许发送 n 条消息
func (r *GossipRateLimiter) AllowN(n int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(r.lastUpdate).Seconds()

	// 补充令牌
	r.tokens += elapsed * r.rate
	if r.tokens > float64(r.burst) {
		r.tokens = float64(r.burst)
	}
	r.lastUpdate = now

	// 检查是否有足够的令牌
	if r.tokens >= float64(n) {
		r.tokens -= float64(n)
		r.updateMetrics(true, "", n)
		return true
	}

	r.updateMetrics(false, "", n)
	return false
}

// AllowForNode 检查是否允许向特定节点发送消息
//
// 除了全局限流，还会检查按节点粒度的限流
func (r *GossipRateLimiter) AllowForNode(nodeID string) bool {
	return r.AllowNForNode(nodeID, 1)
}

// AllowNForNode 检查是否允许向特定节点发送 n 条消息
func (r *GossipRateLimiter) AllowNForNode(nodeID string, n int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	// 1. 检查全局限流
	elapsed := now.Sub(r.lastUpdate).Seconds()
	r.tokens += elapsed * r.rate
	if r.tokens > float64(r.burst) {
		r.tokens = float64(r.burst)
	}
	r.lastUpdate = now

	if r.tokens < float64(n) {
		r.updateMetrics(false, nodeID, n)
		return false
	}

	// 2. 检查节点级限流
	nodeLimiter, exists := r.nodeLimiters[nodeID]
	if !exists {
		nodeLimiter = &nodeRateLimiter{
			tokens:     float64(r.perNodeBurst), // 初始填满节点令牌
			lastUpdate: now,
		}
		r.nodeLimiters[nodeID] = nodeLimiter
	}

	nodeElapsed := now.Sub(nodeLimiter.lastUpdate).Seconds()
	nodeLimiter.tokens += nodeElapsed * r.perNodeRate
	if nodeLimiter.tokens > float64(r.perNodeBurst) {
		nodeLimiter.tokens = float64(r.perNodeBurst)
	}
	nodeLimiter.lastUpdate = now

	if nodeLimiter.tokens < float64(n) {
		r.updateMetrics(false, nodeID, n)
		return false
	}

	// 3. 消耗令牌
	r.tokens -= float64(n)
	nodeLimiter.tokens -= float64(n)

	r.updateMetrics(true, nodeID, n)
	return true
}

// WaitForAllow 等待直到允许发送（带超时）
//
// 使用 context 支持取消等待
func (r *GossipRateLimiter) WaitForAllow(ctx context.Context) bool {
	return r.WaitForAllowN(ctx, 1)
}

// WaitForAllowN 等待直到允许发送 n 条消息
func (r *GossipRateLimiter) WaitForAllowN(ctx context.Context, n int) bool {
	// 快速路径
	if r.AllowN(n) {
		return true
	}

	// 计算需要等待的时间
	r.mu.Lock()
	needed := float64(n) - r.tokens
	r.mu.Unlock()

	if needed <= 0 {
		return true
	}

	waitTime := time.Duration(needed / r.rate * float64(time.Second))
	if waitTime > 5*time.Second {
		waitTime = 5 * time.Second // 最大等待 5 秒
	}

	select {
	case <-ctx.Done():
		return false
	case <-time.After(waitTime):
		return r.AllowN(n)
	}
}

// WaitForNodeAllow 等待直到允许向特定节点发送
func (r *GossipRateLimiter) WaitForNodeAllow(ctx context.Context, nodeID string) bool {
	// 快速路径
	if r.AllowForNode(nodeID) {
		return true
	}

	// 计算等待时间（使用节点级限流参数）
	waitTime := 100 * time.Millisecond // 默认等待 100ms

	select {
	case <-ctx.Done():
		return false
	case <-time.After(waitTime):
		return r.AllowForNode(nodeID)
	}
}

// updateMetrics 更新监控指标（内部方法，调用前必须持有锁）
func (r *GossipRateLimiter) updateMetrics(allowed bool, nodeID string, n int) {
	r.metrics.mu.Lock()
	defer r.metrics.mu.Unlock()

	r.metrics.TotalMessages += int64(n)
	r.metrics.LastUpdateTime = time.Now()

	if allowed {
		r.metrics.AllowedMessages += int64(n)
		if nodeID != "" {
			r.metrics.NodeMessages[nodeID] += int64(n)
		}
	} else {
		r.metrics.DroppedMessages += int64(n)
		if nodeID != "" {
			r.metrics.NodeDropped[nodeID] += int64(n)
		}
	}
}

// GetMetrics 获取监控指标
func (r *GossipRateLimiter) GetMetrics() *RateLimiterMetrics {
	r.metrics.mu.RLock()
	defer r.metrics.mu.RUnlock()

	// 深拷贝
	metrics := RateLimiterMetrics{
		TotalMessages:   r.metrics.TotalMessages,
		AllowedMessages: r.metrics.AllowedMessages,
		DroppedMessages: r.metrics.DroppedMessages,
		LastUpdateTime:  r.metrics.LastUpdateTime,
		NodeMessages:    make(map[string]int64),
		NodeDropped:     make(map[string]int64),
	}

	for k, v := range r.metrics.NodeMessages {
		metrics.NodeMessages[k] = v
	}
	for k, v := range r.metrics.NodeDropped {
		metrics.NodeDropped[k] = v
	}

	// 返回指针避免锁复制
	return &metrics
}

// ResetMetrics 重置监控指标
func (r *GossipRateLimiter) ResetMetrics() {
	r.metrics.mu.Lock()
	defer r.metrics.mu.Unlock()

	r.metrics.TotalMessages = 0
	r.metrics.AllowedMessages = 0
	r.metrics.DroppedMessages = 0
	r.metrics.NodeMessages = make(map[string]int64)
	r.metrics.NodeDropped = make(map[string]int64)
	r.metrics.LastUpdateTime = time.Now()
}

// GetDropRate 获取消息丢弃率
func (r *GossipRateLimiter) GetDropRate() float64 {
	r.metrics.mu.RLock()
	defer r.metrics.mu.RUnlock()

	if r.metrics.TotalMessages == 0 {
		return 0
	}
	return float64(r.metrics.DroppedMessages) / float64(r.metrics.TotalMessages)
}

// ==================== 限流配置 ====================

// RateLimiterOptions 限流器配置选项
type RateLimiterOptions struct {
	// Config 限流器配置
	Config *RateLimiterConfig

	// OnDropped 消息丢弃时的回调函数
	OnDropped func(nodeID string, reason string)
}

// DefaultRateLimiterOptions 返回默认选项
func DefaultRateLimiterOptions() *RateLimiterOptions {
	return &RateLimiterOptions{
		Config: DefaultRateLimiterConfig(),
		OnDropped: func(nodeID string, reason string) {
			logging.WithFields(map[string]any{
				"node_id": nodeID,
				"reason":  reason,
			}).Debug("Gossip 消息被限流")
		},
	}
}

// ==================== 批量限流辅助函数 ====================

// BatchRateLimit 批量限流发送
//
// 对多个节点进行批量发送，自动跳过被限流的节点
//
// 参数：
//   - nodeIDs: 目标节点列表
//   - sendFunc: 实际发送函数，返回是否成功
//
// 返回：
//   - successCount: 成功发送的节点数
//   - droppedCount: 被限流的节点数
func (r *GossipRateLimiter) BatchRateLimit(
	ctx context.Context,
	nodeIDs []string,
	sendFunc func(nodeID string) error,
) (successCount int, droppedCount int, lastError error) {
	for _, nodeID := range nodeIDs {
		// 检查上下文是否已取消
		if ctx.Err() != nil {
			break
		}

		// 检查是否允许发送
		if !r.AllowForNode(nodeID) {
			droppedCount++
			logging.WithFields(map[string]any{
				"node_id": nodeID,
			}).Debug("Gossip 消息被限流，跳过")
			continue
		}

		// 执行发送
		if err := sendFunc(nodeID); err != nil {
			lastError = err
			logging.WithFields(map[string]any{
				"node_id": nodeID,
				"error":   err,
			}).Debug("Gossip 发送失败")
			continue
		}

		successCount++
	}

	return successCount, droppedCount, lastError
}

// ==================== 动态配置 ====================

// SetRate 动态设置速率
func (r *GossipRateLimiter) SetRate(rate float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if rate <= 0 {
		return
	}
	r.rate = rate

	logging.WithField("rate", rate).Info("Gossip 限流器速率已更新")
}

// SetBurst 动态设置突发容量
func (r *GossipRateLimiter) SetBurst(burst int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if burst <= 0 {
		return
	}
	r.burst = burst

	// 如果当前令牌数超过新的容量，裁剪
	if r.tokens > float64(burst) {
		r.tokens = float64(burst)
	}

	logging.WithField("burst", burst).Info("Gossip 限流器突发容量已更新")
}

// ==================== 状态检查 ====================

// IsEnabled 检查限流器是否启用
func (r *GossipRateLimiter) IsEnabled() bool {
	return r != nil
}

// GetTokenCount 获取当前令牌数（用于调试）
func (r *GossipRateLimiter) GetTokenCount() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokens
}

// GetNodeTokenCount 获取特定节点的令牌数（用于调试）
func (r *GossipRateLimiter) GetNodeTokenCount(nodeID string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if limiter, exists := r.nodeLimiters[nodeID]; exists {
		return limiter.tokens
	}
	return 0
}

// ==================== 清理 ====================

// CleanupStaleNodeLimiters 清理过期的节点限流器
//
// 删除超过指定时间未使用的节点限流器，释放内存
func (r *GossipRateLimiter) CleanupStaleNodeLimiters(maxIdleTime time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cleaned := 0

	for nodeID, limiter := range r.nodeLimiters {
		if now.Sub(limiter.lastUpdate) > maxIdleTime {
			delete(r.nodeLimiters, nodeID)
			cleaned++
		}
	}

	if cleaned > 0 {
		logging.WithField("cleaned_count", cleaned).Debug("已清理过期的节点限流器")
	}

	return cleaned
}

// String 返回限流器状态字符串
func (r *GossipRateLimiter) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return fmt.Sprintf("GossipRateLimiter{rate=%.1f, burst=%d, tokens=%.1f, nodes=%d}",
		r.rate, r.burst, r.tokens, len(r.nodeLimiters))
}
