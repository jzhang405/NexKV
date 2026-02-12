// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ========================================
// Quorum 集成接口（与集群层集成）
// ========================================

// QuorumConfig Quorum 配置（从集群层获取）
type QuorumConfig struct {
	Enabled       bool // 是否启用 Quorum
	DefaultQuorum int  // 默认多数派阈值 (N/2 + 1)
	Timeout       int  // Quorum 超时时间（毫秒）
	MinQuorum     int  // 最小 Quorum 值
}

// QuorumResult Quorum 结果
type QuorumResult struct {
	Success      bool      // 是否达到 Quorum
	SuccessCount int       // 成功响应数
	TotalPeers   int       // 总 peer 数
	Quorum       int       // Quorum 阈值
	Responses    []peer.ID // 成功响应的 peer 列表
	Errors       []error   // 错误列表
}

// IsQuorumReached 判断是否达到 Quorum
func (r *QuorumResult) IsQuorumReached() bool {
	return r.Success
}

// GetSuccessRate 获取成功率
func (r *QuorumResult) GetSuccessRate() float64 {
	if r.TotalPeers == 0 {
		return 0
	}
	return float64(r.SuccessCount) / float64(r.TotalPeers)
}

// QuorumManager Quorum 管理器（与集群层集成）
type QuorumManager struct {
	config    *QuorumConfig
	metrics   *QuorumMetrics
	mu        sync.RWMutex
	peerCache []peer.ID // 缓存的有效 Quorum peer 列表
}

// QuorumMetrics Quorum 指标
type QuorumMetrics struct {
	QuorumTotal      int // 总 Quorum 调用次数
	QuorumSuccess    int // Quorum 成功次数
	QuorumFailed     int // Quorum 失败次数
	QuorumTimeout    int // Quorum 超时次数
	PeerSuccessTotal int // Peer 级别成功次数
	PeerFailedTotal  int // Peer 级别失败次数
}

// NewQuorumManager 创建 Quorum 管理器
func NewQuorumManager(config *QuorumConfig) *QuorumManager {
	if config == nil {
		config = DefaultQuorumConfig()
	}

	return &QuorumManager{
		config:    config,
		metrics:   &QuorumMetrics{},
		peerCache: make([]peer.ID, 0),
	}
}

// DefaultQuorumConfig 返回默认 Quorum 配置
func DefaultQuorumConfig() *QuorumConfig {
	return &QuorumConfig{
		Enabled:       true,
		DefaultQuorum: 0, // 动态计算多数派
		Timeout:       5000,
		MinQuorum:     1, // 最小 Quorum 为 1
	}
}

// GetConfig 获取 Quorum 配置
func (m *QuorumManager) GetConfig() *QuorumConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// SetConfig 设置 Quorum 配置
func (m *QuorumManager) SetConfig(config *QuorumConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config
	logging.WithFields(map[string]any{
		"enabled":        config.Enabled,
		"default_quorum": config.DefaultQuorum,
		"timeout":        config.Timeout,
	}).Info("Quorum 配置已更新")
}

// GetQuorumThreshold 计算 Quorum 阈值
func (m *QuorumManager) GetQuorumThreshold(peerCount int) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 如果配置了固定 Quorum，使用配置值
	if m.config.DefaultQuorum > 0 {
		return m.config.DefaultQuorum
	}

	// 否则动态计算多数派阈值 (N/2 + 1)
	quorum := peerCount/2 + 1
	if quorum < m.config.MinQuorum {
		quorum = m.config.MinQuorum
	}

	return quorum
}

// UpdatePeerCache 更新 peer 缓存（从集群层获取）
func (m *QuorumManager) UpdatePeerCache(peers []peer.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.peerCache = make([]peer.ID, len(peers))
	copy(m.peerCache, peers)

	logging.WithField("peer_count", len(peers)).Debug("Quorum peer 缓存已更新")
}

// GetQuorumPeers 获取有效 Quorum peer 列表
func (m *QuorumManager) GetQuorumPeers() []peer.ID {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.peerCache) == 0 {
		return nil
	}

	// 返回副本
	peers := make([]peer.ID, len(m.peerCache))
	copy(peers, m.peerCache)
	return peers
}

// CalculateQuorumResult 计算 Quorum 结果
func (m *QuorumManager) CalculateQuorumResult(
	peerCount int,
	successCount int,
	responses []peer.ID,
	errors []error,
) *QuorumResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 直接计算 Quorum，避免重复获取锁
	quorum := m.calculateQuorumThresholdLocked(peerCount)
	success := successCount >= quorum

	// 更新指标
	m.metrics.QuorumTotal++
	if success {
		m.metrics.QuorumSuccess++
	} else {
		m.metrics.QuorumFailed++
	}

	return &QuorumResult{
		Success:      success,
		SuccessCount: successCount,
		TotalPeers:   peerCount,
		Quorum:       quorum,
		Responses:    responses,
		Errors:       errors,
	}
}

// calculateQuorumThresholdLocked 计算阈值（内部方法，已持有锁）
func (m *QuorumManager) calculateQuorumThresholdLocked(peerCount int) int {
	// 如果配置了固定 Quorum，使用配置值
	if m.config.DefaultQuorum > 0 {
		return m.config.DefaultQuorum
	}

	// 否则动态计算多数派阈值 (N/2 + 1)
	quorum := peerCount/2 + 1
	if quorum < m.config.MinQuorum {
		quorum = m.config.MinQuorum
	}

	return quorum
}

// ValidateQuorum 验证 Quorum 参数
func (m *QuorumManager) ValidateQuorum(opts *FanoutOptions, peerCount int) error {
	if opts.Mode != Quorum {
		return nil
	}

	if !m.config.Enabled {
		return fmt.Errorf("quorum 未启用")
	}

	if peerCount == 0 {
		return fmt.Errorf("peer 列表为空")
	}

	// 计算并验证 Quorum 阈值
	quorum := m.GetQuorumThreshold(peerCount)
	if quorum > peerCount {
		return fmt.Errorf("quorum 阈值 (%d) 大于 peer 数量 (%d)", quorum, peerCount)
	}

	return nil
}

// GetMetrics 获取 Quorum 指标
func (m *QuorumManager) GetMetrics() QuorumMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return QuorumMetrics{
		QuorumTotal:      m.metrics.QuorumTotal,
		QuorumSuccess:    m.metrics.QuorumSuccess,
		QuorumFailed:     m.metrics.QuorumFailed,
		QuorumTimeout:    m.metrics.QuorumTimeout,
		PeerSuccessTotal: m.metrics.PeerSuccessTotal,
		PeerFailedTotal:  m.metrics.PeerFailedTotal,
	}
}

// ResetMetrics 重置 Quorum 指标
func (m *QuorumManager) ResetMetrics() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.metrics = &QuorumMetrics{}
}

// ========================================
// 与集群层集成接口（待实现）
// ========================================

// ClusterService 集群服务接口（预留，用于后续集成）
type ClusterService interface {
	// GetQuorumConfig 获取集群层 Quorum 配置
	GetQuorumConfig() *QuorumConfig

	// GetActivePeers 获取活跃的 peer 列表（用于 Quorum）
	GetActivePeers() []peer.ID

	// IsPeerAvailable 判断 peer 是否可用
	IsPeerAvailable(peerID peer.ID) bool
}

// globalClusterService 全局集群服务（支持运行时更新）
// 使用 atomic.Value 保证并发安全，支持多次更新（如热重启、故障转移）
var globalClusterService atomic.Value

// SetClusterService 设置全局集群服务
func SetClusterService(service ClusterService) {
	globalClusterService.Store(service)
	logging.Info("集群服务已设置")
}

// GetClusterService 获取全局集群服务
// 注意：返回 nil 表示未初始化，调用处需要处理
func GetClusterService() ClusterService {
	v := globalClusterService.Load()
	if v == nil {
		return nil
	}
	return v.(ClusterService)
}

// SyncQuorumConfigFromCluster 从集群层同步 Quorum 配置
func (m *QuorumManager) SyncQuorumConfigFromCluster() error {
	svc := GetClusterService()
	if svc == nil {
		// 集群服务未设置，使用默认配置
		return nil
	}

	config := svc.GetQuorumConfig()
	if config != nil {
		m.SetConfig(config)
		logging.Info("已从集群层同步 Quorum 配置")
	}

	return nil
}

// SyncPeersFromCluster 从集群层同步 peer 列表
func (m *QuorumManager) SyncPeersFromCluster() error {
	svc := GetClusterService()
	if svc == nil {
		// 集群服务未设置，清空缓存
		m.UpdatePeerCache(nil)
		return nil
	}

	peers := svc.GetActivePeers()
	m.UpdatePeerCache(peers)

	logging.WithField("peer_count", len(peers)).Info("已从集群层同步 peer 列表")
	return nil
}

// ========================================
// FanoutOptions 集成（扩展 Quorum 支持）
// ========================================

// ValidateAndNormalizeWithQuorum 使用 Quorum 管理器验证 FanoutOptions
func (m *QuorumManager) ValidateAndNormalizeWithQuorum(opts *FanoutOptions, peers []peer.ID) (*FanoutOptions, error) {
	// 先调用基础验证
	normalized, err := ValidateAndNormalize(opts, len(peers))
	if err != nil {
		return nil, err
	}

	// Quorum 模式验证
	if normalized.Mode == Quorum {
		if err := m.ValidateQuorum(normalized, len(peers)); err != nil {
			return nil, err
		}

		// 从配置获取 Quorum 阈值（如果未指定）
		if normalized.Quorum == 0 {
			normalized.Quorum = m.GetQuorumThreshold(len(peers))
		}
	}

	return normalized, nil
}

// ========================================
// 使用示例
// ========================================

// ExampleQuorumUsage Quorum 使用示例
func ExampleQuorumUsage() {
	// 创建 Quorum 管理器
	manager := NewQuorumManager(DefaultQuorumConfig())

	// 设置配置
	manager.SetConfig(&QuorumConfig{
		Enabled:       true,
		DefaultQuorum: 0, // 动态计算
		Timeout:       5000,
		MinQuorum:     2,
	})

	// 更新 peer 缓存
	peers := []peer.ID{"peer1", "peer2", "peer3", "peer4", "peer5"}
	manager.UpdatePeerCache(peers)

	// 计算 Quorum 阈值
	quorum := manager.GetQuorumThreshold(len(peers))
	fmt.Printf("5 个 peers 的 Quorum 阈值: %d\n", quorum) // 输出: 3

	// 计算 Quorum 结果（3/5 成功）
	result := manager.CalculateQuorumResult(
		5,                                    // 总 peer 数
		3,                                    // 成功数
		[]peer.ID{"peer1", "peer2", "peer3"}, // 成功的 peers
		nil,                                  // 错误列表
	)

	fmt.Printf("Quorum 达成: %v (3/5 >= %d)\n", result.Success, result.Quorum)

	// 获取指标
	metrics := manager.GetMetrics()
	fmt.Printf("Quorum 成功率: %d/%d\n", metrics.QuorumSuccess, metrics.QuorumTotal)
}
