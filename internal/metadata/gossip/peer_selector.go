// Package gossip 提供 Peer 选择策略
//
// 核心功能：
//   - 纯随机选择（Pure Random）
//   - 加权随机选择（Weighted Random）
//   - 轮询选择（Round Robin）
//   - Peer 健康度评分
package gossip

import (
	"math/rand"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
)

// ==================== PeerSelector 接口 ====================

// PeerSelector Peer 选择器接口
//
// 提供从候选 peer 列表中选择一个 peer 的策略
type PeerSelector interface {
	// Select 从候选 peer 列表中选择一个 peer
	// 返回选中的 peer ID，如果没有候选则返回空字符串
	Select(peers []string) string

	// Update 更新 peer 健康度指标
	// 根据 sync 结果更新 peer 的评分
	Update(peerID string, result *SyncResult)

	// String 返回策略名称
	String() string
}

// ==================== PeerHealthMetrics 健康度指标 ====================

// PeerHealthMetrics Peer 健康度指标
//
// 记录每个 peer 的性能指标，用于加权随机选择
type PeerHealthMetrics struct {
	mu sync.RWMutex

	// 网络延迟（越低越好）
	latency map[string]time.Duration

	// 历史成功率（越高越好）
	// 范围: 0.0 - 1.0
	successRate map[string]float64

	// 负载情况（越低越好）
	// 估算: 0 - 10000+
	load map[string]uint64

	// 最后同步时间（用于判断新鲜度）
	lastSyncTime map[string]time.Time

	// 评分缓存（减少重复计算）
	scores map[string]float64
}

// NewPeerHealthMetrics 创建 Peer 健康度指标
func NewPeerHealthMetrics() *PeerHealthMetrics {
	return &PeerHealthMetrics{
		latency:       make(map[string]time.Duration),
		successRate:   make(map[string]float64),
		load:          make(map[string]uint64),
		lastSyncTime:  make(map[string]time.Time),
		scores:        make(map[string]float64),
	}
}

// Update 更新 peer 指标
func (p *PeerHealthMetrics) Update(peerID string, result *SyncResult) {
	if result == nil || !result.IsSynced() {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 更新最后同步时间
	p.lastSyncTime[peerID] = time.Now()

	// TODO: 实际测量延迟和负载
	// 当前使用默认值
	if _, exists := p.latency[peerID]; !exists {
		p.latency[peerID] = 50 * time.Millisecond // 默认 50ms
	}
	if _, exists := p.successRate[peerID]; !exists {
		p.successRate[peerID] = 0.8 // 默认 80% 成功率
	}
	if _, exists := p.load[peerID]; !exists {
		p.load[peerID] = 1000 // 默认负载 1000
	}

	// 重新计算评分
	p.scores[peerID] = p.calculateScore(peerID)
}

// calculateScore 计算综合评分
func (p *PeerHealthMetrics) calculateScore(peerID string) float64 {
	if score, ok := p.scores[peerID]; ok {
		return score
	}

	latency := p.getLatencyScore(peerID)
	success := p.getSuccessRateScore(peerID)
	load := p.getLoadScore(peerID)
	freshness := p.getFreshnessScore(peerID)

	// 加权平均
	// 延迟 30% + 成功率 30% + 负载 20% + 新鲜度 20%
	score := latency*0.3 + success*0.3 + load*0.2 + freshness*0.2

	logging.WithFields(map[string]interface{}{
		"peer_id":  peerID,
		"score":     score,
		"latency":  latency,
		"success":   success,
		"load":      load,
		"freshness": freshness,
	}).Debug("Peer 健康度评分计算")

	return score
}

// getLatencyScore 延迟评分（0-100）
func (p *PeerHealthMetrics) getLatencyScore(peerID string) float64 {
	latency, ok := p.latency[peerID]
	if !ok {
		return 50.0 // 默认中等分数
	}

	ms := latency.Milliseconds()
	switch {
	case ms < 10:
		return 100.0
	case ms < 50:
		return 80.0
	case ms < 100:
		return 60.0
	case ms < 500:
		return 40.0
	default:
		return 20.0
	}
}

// getSuccessRateScore 成功率评分（0-100）
func (p *PeerHealthMetrics) getSuccessRateScore(peerID string) float64 {
	success, ok := p.successRate[peerID]
	if !ok {
		return 80.0 // 默认 80%
	}

	// 0% = 0 分, 100% = 100 分
	return success * 100
}

// getLoadScore 负载评分（0-100）
func (p *PeerHealthMetrics) getLoadScore(peerID string) float64 {
	load, ok := p.load[peerID]
	if !ok {
		return 80.0 // 默认中等负载
	}

	// 0 = 100 分 (空闲), 10000+ = 0 分 (过载)
	// 线性反转
	if load >= 10000 {
		return 0.0
	}
	return 100.0 - (float64(load) / 100.0 * 100)
}

// getFreshnessScore 新鲜度评分（0-100）
func (p *PeerHealthMetrics) getFreshnessScore(peerID string) float64 {
	lastSync, ok := p.lastSyncTime[peerID]
	if !ok {
		return 50.0 // 默认中等新鲜度
	}

	// 根据最后同步时间计算新鲜度
	// < 1 分钟 = 100 分
	// < 5 分钟 = 80 分
	// < 30 分钟 = 60 分
	// >= 30 分钟 = 40 分
	elapsed := time.Since(lastSync)
	switch {
	case elapsed < 1*time.Minute:
		return 100.0
	case elapsed < 5*time.Minute:
		return 80.0
	case elapsed < 30*time.Minute:
		return 60.0
	default:
		return 40.0
	}
}

// GetScore 获取 peer 的当前评分
func (p *PeerHealthMetrics) GetScore(peerID string) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if score, ok := p.scores[peerID]; ok {
		return score
	}
	return 1.0 // 默认评分
}

// ==================== 纯随机选择 ====================

// RandomPeerSelector 纯随机选择
type RandomPeerSelector struct{}

// NewRandomPeerSelector 创建纯随机选择器
func NewRandomPeerSelector() *RandomPeerSelector {
	return &RandomPeerSelector{}
}

// Select 从候选 peer 中随机选择一个
func (r *RandomPeerSelector) Select(peers []string) string {
	if len(peers) == 0 {
		return ""
	}
	return peers[rand.Intn(len(peers))]
}

// Update 更新 peer 健康度（纯随机不需要健康度）
func (r *RandomPeerSelector) Update(peerID string, result *SyncResult) {
	// 纯随机策略不使用健康度指标
}

// String 返回策略名称
func (r *RandomPeerSelector) String() string {
	return "RandomPeerSelector"
}

// ==================== 加权随机选择 ====================

// WeightedRandomPeerSelector 加权随机选择
type WeightedRandomPeerSelector struct {
	metrics *PeerHealthMetrics
}

// NewWeightedRandomPeerSelector 创建加权随机选择器
func NewWeightedRandomPeerSelector(metrics *PeerHealthMetrics) *WeightedRandomPeerSelector {
	return &WeightedRandomPeerSelector{
		metrics: metrics,
	}
}

// Select 根据权重随机选择一个 peer（轮盘赌算法）
func (w *WeightedRandomPeerSelector) Select(peers []string) string {
	if len(peers) == 0 {
		return ""
	}

	// 计算总权重
	totalWeight := 0.0
	for _, peerID := range peers {
		totalWeight += w.metrics.GetScore(peerID)
	}

	// 轮盘赌选择
	r := rand.Float64() * totalWeight
	for _, peerID := range peers {
		r -= w.metrics.GetScore(peerID)
		if r <= 0 {
			logging.WithFields(map[string]interface{}{
				"peer_id":     peerID,
				"score":       w.metrics.GetScore(peerID),
				"total_weight": totalWeight,
			}).Debug("加权随机选择 peer")
			return peerID
		}
	}

	return ""
}

// Update 更新 peer 健康度指标
func (w *WeightedRandomPeerSelector) Update(peerID string, result *SyncResult) {
	w.metrics.Update(peerID, result)
}

// String 返回策略名称
func (w *WeightedRandomPeerSelector) String() string {
	return "WeightedRandomPeerSelector"
}

// ==================== 轮询选择 ====================

// RoundRobinPeerSelector 轮询选择
type RoundRobinPeerSelector struct {
	peers []string
	index int
	mu    sync.Mutex
}

// NewRoundRobinPeerSelector 创建轮询选择器
func NewRoundRobinPeerSelector() *RoundRobinPeerSelector {
	return &RoundRobinPeerSelector{
		peers: make([]string, 0),
		index: 0,
	}
}

// Select 按轮询顺序选择下一个 peer
func (r *RoundRobinPeerSelector) Select(peers []string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(peers) == 0 {
		return ""
	}

	// 如果 peer 列表发生变化，重置轮询
	if len(r.peers) != len(peers) || !equalPeers(r.peers, peers) {
		r.peers = make([]string, len(peers))
		copy(r.peers, peers)
		r.index = 0
	}

	peer := r.peers[r.index]
	r.index = (r.index + 1) % len(r.peers)

	logging.WithFields(map[string]interface{}{
		"peer_id":      peer,
		"index":        r.index,
		"total_peers":  len(r.peers),
	}).Debug("轮询选择 peer")

	return peer
}

// equalPeers 检查两个 peer 列表是否相等（忽略顺序）
func equalPeers(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aMap := make(map[string]bool)
	for _, peer := range a {
		aMap[peer] = true
	}
	for _, peer := range b {
		if !aMap[peer] {
			return false
		}
	}
	return true
}

// Update 更新 peer 列表（如果 peer 被移除）
func (r *RoundRobinPeerSelector) Update(peerID string, result *SyncResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 轮询策略不使用健康度指标
	// 如果需要移除失败的 peer，可以在这里实现
}

// String 返回策略名称
func (r *RoundRobinPeerSelector) String() string {
	return "RoundRobinPeerSelector"
}
