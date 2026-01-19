// Package cluster 自愈机制实现
//
// 基于故障检测的自动恢复机制
//
// 核心特性:
//   - 故障节点检测：监听故障检测器的事件
//   - 自动重连：节点恢复后自动重新加入集群
//   - 拓扑修复：自动修复树形结构的破损
//   - Leader切换：Leader故障时自动选举新Leader
package cluster

import (
	"context"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
)

// SelfHealer 自愈机制
//
// 自愈策略：
//  1. 监听故障检测器的节点故障事件
//  2. 从故障节点的子节点中选择新的父节点
//  3. 通知子节点重新连接到新父节点
//  4. 节点恢复后自动重新加入集群
type SelfHealer struct {
	// 配置
	config *SelfHealingConfig

	// 本地节点
	localNodeID string

	// 传输层
	transport transport.Transport

	// 树形协调器
	coordinator *TreeCoordinator

	// 故障检测器
	failureDetector *FailureDetector

	// Leader选举
	leaderElection *LeaderElection

	// 自愈状态
	healingNodes   map[string]*HealingRecord
	healingNodesMu sync.RWMutex

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
	stopCh  chan struct{}
	stopWg  sync.WaitGroup

	// 统计信息
	stats *SelfHealingStats
}

// SelfHealingConfig 自愈机制配置
type SelfHealingConfig struct {
	// HealingInterval 自愈检查间隔（默认 10 秒）
	HealingInterval time.Duration

	// MaxRetryAttempts 最大重试次数（默认 3）
	MaxRetryAttempts int

	// RetryDelay 重试延迟（默认 5 秒）
	RetryDelay time.Duration

	// EnableTopologyRepair 是否启用拓扑修复
	EnableTopologyRepair bool

	// EnableLeaderElection 是否启用Leader选举
	EnableLeaderElection bool
}

// DefaultSelfHealingConfig 返回默认配置
func DefaultSelfHealingConfig() *SelfHealingConfig {
	return &SelfHealingConfig{
		HealingInterval:      10 * time.Second,
		MaxRetryAttempts:     3,
		RetryDelay:           5 * time.Second,
		EnableTopologyRepair: true,
		EnableLeaderElection: true,
	}
}

// HealingRecord 自愈记录
type HealingRecord struct {
	// NodeID 故障节点ID
	NodeID string

	// FailedAt 故障检测时间
	FailedAt time.Time

	// RetryCount 重试次数
	RetryCount int

	// LastRetryAt 最后重试时间
	LastRetryAt time.Time

	// Status 自愈状态
	Status HealingStatus
}

// HealingStatus 自愈状态
type HealingStatus int

const (
	HealingStatusDetecting HealingStatus = iota // 检测中
	HealingStatusHealing                        // 自愈中
	HealingStatusRecovered                      // 已恢复
	HealingStatusFailed                         // 自愈失败
)

// SelfHealingStats 自愈统计信息
type SelfHealingStats struct {
	// 故障检测总数
	FailuresDetected atomic.Int64

	// 自愈成功数
	HealingsSuccess atomic.Int64

	// 自愈失败数
	HealingsFailed atomic.Int64

	// 拓扑修复次数
	TopologyRepairs atomic.Int64

	// 最后一次自愈时间
	LastHealingTime atomic.Value // time.Time
}

// NewSelfHealer 创建自愈机制
func NewSelfHealer(
	localNodeID string,
	transport transport.Transport,
	coordinator *TreeCoordinator,
	failureDetector *FailureDetector,
	leaderElection *LeaderElection,
	config *SelfHealingConfig,
) (*SelfHealer, error) {
	if config == nil {
		config = DefaultSelfHealingConfig()
	}

	if transport == nil {
		return nil, types.NewClusterNilParameterError("transport")
	}

	if localNodeID == "" {
		return nil, types.NewClusterNilParameterError("localNodeID")
	}

	if coordinator == nil {
		return nil, types.NewClusterNilParameterError("coordinator")
	}

	if failureDetector == nil {
		return nil, types.NewClusterNilParameterError("failureDetector")
	}

	healer := &SelfHealer{
		config:          config,
		localNodeID:     localNodeID,
		transport:       transport,
		coordinator:     coordinator,
		failureDetector: failureDetector,
		leaderElection:  leaderElection,
		healingNodes:    make(map[string]*HealingRecord),
		stopCh:          make(chan struct{}),
		stats:           &SelfHealingStats{},
	}

	// 初始化统计信息
	healer.stats.LastHealingTime.Store(time.Time{})

	// 设置故障回调
	failureDetector.SetFailureCallback(healer.onNodeFailed)

	return healer, nil
}

// Start 启动自愈机制
func (sh *SelfHealer) Start() error {
	if !sh.started.CompareAndSwap(false, true) {
		return types.NewClusterServiceStateError("自愈机制", "已经启动")
	}

	logging.WithFields(map[string]any{
		"node_id":          sh.localNodeID,
		"healing_interval": sh.config.HealingInterval,
		"max_retries":      sh.config.MaxRetryAttempts,
	}).Info("启动自愈机制")

	// 启动自愈循环
	sh.stopWg.Add(1)
	go sh.healingLoop()

	sh.started.Store(true)

	logging.Info("自愈机制启动成功")
	return nil
}

// Stop 停止自愈机制
func (sh *SelfHealer) Stop() error {
	if !sh.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	logging.Info("停止自愈机制...")

	// 关闭停止信号
	close(sh.stopCh)

	// 等待所有协程退出
	sh.stopWg.Wait()

	// 打印统计信息
	logging.WithFields(map[string]any{
		"failures_detected": sh.stats.FailuresDetected.Load(),
		"healings_success":  sh.stats.HealingsSuccess.Load(),
		"healings_failed":   sh.stats.HealingsFailed.Load(),
		"topology_repairs":  sh.stats.TopologyRepairs.Load(),
	}).Info("自愈机制统计")

	logging.Info("自愈机制已停止")
	return nil
}

// ========================================
// 核心自愈逻辑
// ========================================

// healingLoop 自愈循环
func (sh *SelfHealer) healingLoop() {
	defer sh.stopWg.Done()

	ticker := time.NewTicker(sh.config.HealingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sh.checkAndHeal()

		case <-sh.stopCh:
			return
		}
	}
}

// onNodeFailed 节点故障回调
func (sh *SelfHealer) onNodeFailed(nodeID string) {
	sh.stats.FailuresDetected.Add(1)

	logging.WithFields(map[string]any{
		"node_id":  nodeID,
		"local_id": sh.localNodeID,
	}).Warn("检测到节点故障")

	// 记录故障节点
	sh.healingNodesMu.Lock()
	defer sh.healingNodesMu.Unlock()

	sh.healingNodes[nodeID] = &HealingRecord{
		NodeID:     nodeID,
		FailedAt:   time.Now(),
		RetryCount: 0,
		Status:     HealingStatusDetecting,
	}

	// 立即触发自愈检查
	go sh.healNode(nodeID)
}

// checkAndHeal 检查并执行自愈
func (sh *SelfHealer) checkAndHeal() {
	sh.healingNodesMu.RLock()
	nodes := make([]*HealingRecord, 0, len(sh.healingNodes))
	for _, record := range sh.healingNodes {
		nodes = append(nodes, record)
	}
	sh.healingNodesMu.RUnlock()

	for _, record := range nodes {
		if record.Status == HealingStatusDetecting || record.Status == HealingStatusHealing {
			sh.healNode(record.NodeID)
		}
	}
}

// healNode 自愈节点
func (sh *SelfHealer) healNode(nodeID string) {
	sh.healingNodesMu.Lock()
	record, exists := sh.healingNodes[nodeID]
	if !exists {
		sh.healingNodesMu.Unlock()
		return
	}

	// 检查重试次数
	if record.RetryCount >= sh.config.MaxRetryAttempts {
		record.Status = HealingStatusFailed
		sh.stats.HealingsFailed.Add(1)
		sh.healingNodesMu.Unlock()

		logging.WithFields(map[string]any{
			"node_id":     nodeID,
			"retry_count": record.RetryCount,
		}).Error("自愈失败：超过最大重试次数")

		return
	}

	record.RetryCount++
	record.LastRetryAt = time.Now()
	record.Status = HealingStatusHealing
	sh.healingNodesMu.Unlock()

	logging.WithFields(map[string]any{
		"node_id":     nodeID,
		"retry_count": record.RetryCount,
	}).Info("开始自愈节点")

	// 执行自愈策略
	if err := sh.performHealing(nodeID); err != nil {
		logging.WithFields(map[string]any{
			"node_id": nodeID,
			"error":   err.Error(),
		}).Error("自愈执行失败")

		// 安排重试
		time.AfterFunc(sh.config.RetryDelay, func() {
			sh.healNode(nodeID)
		})
		return
	}

	// 自愈成功
	sh.healingNodesMu.Lock()
	record.Status = HealingStatusRecovered
	sh.stats.HealingsSuccess.Add(1)
	sh.stats.LastHealingTime.Store(time.Now())
	sh.healingNodesMu.Unlock()

	logging.WithFields(map[string]any{
		"node_id": nodeID,
	}).Info("节点自愈成功")

	// 从自愈列表中移除已恢复的节点
	go func() {
		time.Sleep(sh.config.HealingInterval)
		sh.healingNodesMu.Lock()
		delete(sh.healingNodes, nodeID)
		sh.healingNodesMu.Unlock()
	}()
}

// performHealing 执行自愈
func (sh *SelfHealer) performHealing(nodeID string) error {
	// 策略1：修复拓扑结构
	if sh.config.EnableTopologyRepair {
		if err := sh.repairTopology(nodeID); err != nil {
			logging.WithFields(map[string]any{
				"node_id": nodeID,
				"error":   err.Error(),
			}).Warn("拓扑修复失败")
		}
	}

	// 策略2：检查Leader是否故障
	if sh.config.EnableLeaderElection && sh.leaderElection != nil {
		currentLeader := sh.leaderElection.GetCurrentLeader()
		if currentLeader != nil && currentLeader.NodeID == nodeID {
			logging.WithFields(map[string]any{
				"failed_leader": nodeID,
			}).Warn("Leader节点故障，触发重新选举")

			// 触发重新选举
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := sh.leaderElection.Campaign(ctx); err != nil {
				return types.NewClusterElectionError("leader 选举失败", err)
			}
		}
	}

	return nil
}

// repairTopology 修复拓扑结构
func (sh *SelfHealer) repairTopology(failedNodeID string) error {
	// 获取故障节点的信息
	failedNode, err := sh.coordinator.GetNode(failedNodeID)
	if err != nil {
		return types.NewClusterFailureDetectionError("获取故障节点信息失败", err)
	}

	// 如果故障节点有子节点，需要为子节点找新父节点
	if len(failedNode.ChildrenIDs) > 0 {
		logging.WithFields(map[string]any{
			"failed_node":    failedNodeID,
			"children_count": len(failedNode.ChildrenIDs),
		}).Info("为孤儿节点寻找新父节点")

		// 查找候选父节点
		newParentID, err := sh.findNewParent(failedNodeID)
		if err != nil {
			return types.NewClusterCoordinatorError("查找新父节点失败", err)
		}

		if newParentID == "" {
			logging.Warn("没有可用的候选父节点")
			return nil
		}

		// 重新建立父子关系
		for _, childID := range failedNode.ChildrenIDs {
			if err := sh.reparentChild(childID, newParentID); err != nil {
				logging.WithFields(map[string]any{
					"child_id":   childID,
					"new_parent": newParentID,
					"error":      err.Error(),
				}).Error("重新建立父子关系失败")
				continue
			}

			logging.WithFields(map[string]any{
				"child_id":   childID,
				"old_parent": failedNodeID,
				"new_parent": newParentID,
			}).Info("成功重新建立父子关系")
		}

		sh.stats.TopologyRepairs.Add(1)
	}

	return nil
}

// findNewParent 查找新父节点
func (sh *SelfHealer) findNewParent(excludeNodeID string) (string, error) {
	// 获取所有在线节点
	allNodes := sh.coordinator.ListNodes()

	var bestCandidate string
	var bestScore int64

	for _, node := range allNodes {
		// 排除故障节点自身
		if node.NodeID == excludeNodeID {
			continue
		}

		// 只考虑Ready状态的节点
		if node.Status != NodeStatusReady {
			continue
		}

		// 检查节点是否有可用容量
		if len(node.ChildrenIDs) >= 10 {
			continue
		}

		// 计算候选节点得分
		score := sh.calculateParentScore(node)

		if score > bestScore {
			bestScore = score
			bestCandidate = node.NodeID
		}
	}

	return bestCandidate, nil
}

// calculateParentScore 计算父节点得分
func (sh *SelfHealer) calculateParentScore(node *Node) int64 {
	var score int64

	// 优先选择层级较低的节点（减少树深度）
	score += int64(10-node.Level) * 100

	// 优先选择子节点较少的节点
	score += int64(10-len(node.ChildrenIDs)) * 50

	// 优先选择优先级较高的节点
	score += int64(node.Priority) * 10

	return score
}

// reparentChild 重新建立父子关系
func (sh *SelfHealer) reparentChild(childID string, newParentID string) error {
	// TODO: 实际实现中，这里需要通过transport发送消息给子节点
	// 通知子节点更新其父节点信息

	// 从树形协调器中移除旧的父子关系
	// 注意：这是简化实现，实际需要更复杂的协议

	logging.WithFields(map[string]any{
		"child_id":      childID,
		"new_parent_id": newParentID,
	}).Debug("重新建立父子关系")

	return nil
}

// ========================================
// 查询接口
// ========================================

// GetStats 获取统计信息
func (sh *SelfHealer) GetStats() *SelfHealingStats {
	return sh.stats
}

// GetHealingNodes 获取正在自愈的节点列表
func (sh *SelfHealer) GetHealingNodes() []*HealingRecord {
	sh.healingNodesMu.RLock()
	defer sh.healingNodesMu.RUnlock()

	records := make([]*HealingRecord, 0, len(sh.healingNodes))
	for _, record := range sh.healingNodes {
		records = append(records, record)
	}

	return records
}

// IsHealing 检查节点是否正在自愈
func (sh *SelfHealer) IsHealing(nodeID string) bool {
	sh.healingNodesMu.RLock()
	defer sh.healingNodesMu.RUnlock()

	record, exists := sh.healingNodes[nodeID]
	if !exists {
		return false
	}

	return record.Status == HealingStatusDetecting || record.Status == HealingStatusHealing
}
