// Package partition 提供网络分区检测器实现
//
// DEGR-2: 分区检测器
//
// 核心功能：
//   - 基于 Phi Accrual 故障检测器判断节点状态
//   - 计算可达节点数，判定是否可达 Quorum
//   - 提供分区状态信息（可达/不可达节点列表）
//
// 分区判定逻辑：
//  1. 使用 Phi Accrual 检测每个节点的故障状态
//  2. 统计可达节点数量
//  3. 如果可达节点数 >= Quorum 大小，则判定为正常
//  4. 否则判定为分区状态
package partition

import (
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/fault"
)

// PartitionStatus 分区状态
type PartitionStatus struct {
	// IsPartitioned 是否处于分区状态
	// 当无法达成 Quorum 时为 true
	IsPartitioned bool

	// CanReachQuorum 是否可达 Quorum
	CanReachQuorum bool

	// ReachableNodes 可达节点列表
	ReachableNodes []string

	// UnreachableNodes 不可达节点列表
	UnreachableNodes []string

	// LastChecked 最后检查时间
	LastChecked time.Time

	// LocalNodeID 本地节点 ID
	LocalNodeID string

	// QuorumSize Quorum 大小
	QuorumSize int
}

// Detector 分区检测器
type Detector struct {
	mu sync.RWMutex

	// 配置
	localNodeID string
	allNodes    []string
	quorumSize  int

	// Phi Accrual 故障检测器
	phiDetector *fault.PhiAccrualDetector

	// 连续检测失败次数（避免网络抖动误判）
	consecutiveFailures map[string]int
	requiredFailures    int // 连续 N 次失败才判定为故障

	// 模拟故障（仅用于测试）
	simulatedFailures map[string]bool

	// 缓存的分区状态
	cachedStatus *PartitionStatus
	lastUpdate   time.Time
	cacheTTL     time.Duration
}

// DetectorConfig 分区检测器配置
type DetectorConfig struct {
	// LocalNodeID 本地节点 ID
	LocalNodeID string

	// AllNodes 所有节点 ID 列表（包括本地节点）
	AllNodes []string

	// QuorumSize Quorum 大小
	QuorumSize int

	// PhiDetector Phi Accrual 故障检测器（可选，如果不提供则自动创建）
	PhiDetector *fault.PhiAccrualDetector

	// PhiConfig Phi Accrual 配置（如果未提供 PhiDetector）
	PhiConfig *fault.PhiAccrualConfig

	// RequiredFailures 连续检测失败次数阈值（默认 3）
	RequiredFailures int

	// CacheTTL 状态缓存 TTL（默认 1 秒）
	CacheTTL time.Duration
}

// NewDetector 创建分区检测器
func NewDetector(config *DetectorConfig) *Detector {
	if config == nil {
		panic("config cannot be nil")
	}

	// 验证必要参数
	if config.LocalNodeID == "" {
		panic("LocalNodeID cannot be empty")
	}
	if len(config.AllNodes) == 0 {
		panic("AllNodes cannot be empty")
	}
	if config.QuorumSize <= 0 {
		panic("QuorumSize must be positive")
	}

	// 设置默认值
	requiredFailures := config.RequiredFailures
	if requiredFailures <= 0 {
		requiredFailures = 3
	}

	cacheTTL := config.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = time.Second
	}

	// 创建或使用提供的 Phi 检测器
	phiDetector := config.PhiDetector
	if phiDetector == nil {
		phiConfig := config.PhiConfig
		if phiConfig == nil {
			phiConfig = &fault.PhiAccrualConfig{
				LocalNodeID: config.LocalNodeID,
			}
		}
		phiDetector = fault.NewPhiAccrualDetector(phiConfig)
	}

	return &Detector{
		localNodeID:         config.LocalNodeID,
		allNodes:            config.AllNodes,
		quorumSize:          config.QuorumSize,
		phiDetector:         phiDetector,
		consecutiveFailures: make(map[string]int),
		simulatedFailures:   make(map[string]bool),
		requiredFailures:    requiredFailures,
		cacheTTL:            cacheTTL,
	}
}

// RecordHeartbeat 记录来自节点的心跳
//
// 当收到来自其他节点的心跳时调用此方法。
func (d *Detector) RecordHeartbeat(nodeID string) {
	d.phiDetector.RecordHeartbeat(nodeID)

	// 重置连续失败计数
	d.mu.Lock()
	delete(d.consecutiveFailures, nodeID)
	d.mu.Unlock()
}

// CheckPartition 检查分区状态
//
// 返回当前的分区状态信息，包括可达/不可达节点列表。
// 结果会被缓存 CacheTTL 时间以提高性能。
func (d *Detector) CheckPartition() PartitionStatus {
	d.mu.RLock()
	// 检查缓存
	if d.cachedStatus != nil && time.Since(d.lastUpdate) < d.cacheTTL {
		status := *d.cachedStatus
		d.mu.RUnlock()
		return status
	}
	d.mu.RUnlock()

	// 计算新的分区状态
	status := d.computePartitionStatus()

	// 更新缓存
	d.mu.Lock()
	d.cachedStatus = &status
	d.lastUpdate = time.Now()
	d.mu.Unlock()

	return status
}

// computePartitionStatus 计算分区状态
func (d *Detector) computePartitionStatus() PartitionStatus {
	var reachable, unreachable []string

	for _, nodeID := range d.allNodes {
		// 跳过本地节点（自己总是可达的）
		if nodeID == d.localNodeID {
			reachable = append(reachable, nodeID)
			continue
		}

		// 检查是否是模拟故障（仅用于测试）
		d.mu.RLock()
		isSimulatedFailure := d.simulatedFailures[nodeID]
		d.mu.RUnlock()

		if isSimulatedFailure {
			unreachable = append(unreachable, nodeID)
			continue
		}

		// 使用 Phi 检测器判断节点状态
		phi := d.phiDetector.Phi(nodeID)
		isFailed := phi > d.phiDetector.GetThreshold()

		d.mu.Lock()
		if isFailed {
			// 增加连续失败计数
			d.consecutiveFailures[nodeID]++
			failures := d.consecutiveFailures[nodeID]

			// 只有连续失败超过阈值才判定为不可达
			if failures >= d.requiredFailures {
				unreachable = append(unreachable, nodeID)
			} else {
				// 尚未达到阈值，暂时认为可达
				reachable = append(reachable, nodeID)
			}
		} else {
			// 节点正常，重置失败计数
			delete(d.consecutiveFailures, nodeID)
			reachable = append(reachable, nodeID)
		}
		d.mu.Unlock()
	}

	// 判断是否可达 Quorum
	canReachQuorum := len(reachable) >= d.quorumSize

	return PartitionStatus{
		IsPartitioned:    !canReachQuorum,
		CanReachQuorum:   canReachQuorum,
		ReachableNodes:   reachable,
		UnreachableNodes: unreachable,
		LastChecked:      time.Now(),
		LocalNodeID:      d.localNodeID,
		QuorumSize:       d.quorumSize,
	}
}

// IsPartitioned 简化的分区检查
//
// 返回当前是否处于分区状态。
func (d *Detector) IsPartitioned() bool {
	return d.CheckPartition().IsPartitioned
}

// CanReachQuorum 检查是否可达 Quorum
func (d *Detector) CanReachQuorum() bool {
	return d.CheckPartition().CanReachQuorum
}

// GetReachableNodes 获取可达节点列表
func (d *Detector) GetReachableNodes() []string {
	status := d.CheckPartition()
	return status.ReachableNodes
}

// GetUnreachableNodes 获取不可达节点列表
func (d *Detector) GetUnreachableNodes() []string {
	status := d.CheckPartition()
	return status.UnreachableNodes
}

// UpdateNodes 更新节点列表
//
// 当集群成员发生变化时调用此方法。
func (d *Detector) UpdateNodes(nodes []string, quorumSize int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.allNodes = nodes
	d.quorumSize = quorumSize
	d.cachedStatus = nil

	// 清除不在新列表中的节点的失败计数
	newNodeSet := make(map[string]struct{})
	for _, nodeID := range nodes {
		newNodeSet[nodeID] = struct{}{}
	}
	for nodeID := range d.consecutiveFailures {
		if _, exists := newNodeSet[nodeID]; !exists {
			delete(d.consecutiveFailures, nodeID)
		}
	}
}

// ResetNode 重置节点状态
//
// 当节点重新加入时调用此方法。
func (d *Detector) ResetNode(nodeID string) {
	d.phiDetector.Reset(nodeID)

	d.mu.Lock()
	delete(d.consecutiveFailures, nodeID)
	delete(d.simulatedFailures, nodeID)
	d.cachedStatus = nil
	d.mu.Unlock()
}

// ResetAll 重置所有节点状态
func (d *Detector) ResetAll() {
	d.phiDetector.ResetAll()

	d.mu.Lock()
	d.consecutiveFailures = make(map[string]int)
	d.simulatedFailures = make(map[string]bool)
	d.cachedStatus = nil
	d.mu.Unlock()
}

// GetPhiDetector 获取 Phi 检测器（用于高级配置）
func (d *Detector) GetPhiDetector() *fault.PhiAccrualDetector {
	return d.phiDetector
}

// GetQuorumSize 获取 Quorum 大小
func (d *Detector) GetQuorumSize() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.quorumSize
}

// GetAllNodes 获取所有节点列表
func (d *Detector) GetAllNodes() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]string(nil), d.allNodes...)
}

// GetLocalNodeID 获取本地节点 ID
func (d *Detector) GetLocalNodeID() string {
	return d.localNodeID
}

// GetRequiredFailures 获取连续失败阈值
func (d *Detector) GetRequiredFailures() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.requiredFailures
}

// SetRequiredFailures 设置连续失败阈值
func (d *Detector) SetRequiredFailures(requiredFailures int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if requiredFailures > 0 {
		d.requiredFailures = requiredFailures
	}
}

// SimulateFailure 模拟节点故障（仅用于测试）
//
// 设置节点为故障状态，用于测试分区检测逻辑。
func (d *Detector) SimulateFailure(nodeID string) {
	d.mu.Lock()
	d.simulatedFailures[nodeID] = true
	d.cachedStatus = nil
	d.mu.Unlock()
}

// SimulateRecovery 模拟节点恢复（仅用于测试）
func (d *Detector) SimulateRecovery(nodeID string) {
	d.mu.Lock()
	delete(d.simulatedFailures, nodeID)
	d.cachedStatus = nil
	d.mu.Unlock()
	d.ResetNode(nodeID)
}

// InvalidateCache 使缓存失效
//
// 强制下一次 CheckPartition 重新计算状态。
func (d *Detector) InvalidateCache() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cachedStatus = nil
}
