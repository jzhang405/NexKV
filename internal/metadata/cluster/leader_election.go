// Package cluster Leader 选举实现
//
// 基于优先级和节点的 Leader 选举机制
//
// 核心特性:
//   - 基于优先级的选举：优先级高的节点更容易成为 Leader
//   - 租约机制：保证同一时刻只有一个 Leader
//   - 故障转移：Leader 故障时自动选举新 Leader
//   - 平稳切换：新旧 Leader 平滑交接
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

// LeaderElection Leader 选举器
//
// 选举算法：
//  1. 计算每个候选节点的权重（优先级 + 节点状态）
//  2. 选择权重最高的节点作为 Leader
//  3. 定期检查 Leader 存活状态
//  4. Leader 故障时触发重新选举
type LeaderElection struct {
	// 配置
	config *LeaderElectionConfig

	// 本地节点
	localNodeID string

	// 传输层
	transport transport.Transport

	// 候选节点列表
	candidates   map[string]*Node
	candidatesMu sync.RWMutex

	// 当前 Leader
	currentLeader atomic.Value // *Node
	leaderLease   atomic.Int64 // 租约过期时间（Unix 时间戳）

	// 选举状态
	isLeader atomic.Bool

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
	stopCh  chan struct{}
	stopWg  sync.WaitGroup

	// 统计信息
	stats *LeaderElectionStats
}

// LeaderElectionConfig Leader 选举配置
type LeaderElectionConfig struct {
	// ElectionInterval 选举检查间隔（默认 5 秒）
	ElectionInterval time.Duration

	// LeaseTTL Leader 租约过期时间（默认 15 秒）
	LeaseTTL time.Duration

	// Priority 本地节点优先级（默认 0）
	Priority int

	// AutoElection 是否自动参与选举
	AutoElection bool
}

// DefaultLeaderElectionConfig 返回默认配置
func DefaultLeaderElectionConfig() *LeaderElectionConfig {
	return &LeaderElectionConfig{
		ElectionInterval: 5 * time.Second,
		LeaseTTL:         15 * time.Second,
		Priority:         0,
		AutoElection:     true,
	}
}

// LeaderElectionStats Leader 选举统计信息
type LeaderElectionStats struct {
	// 选举次数
	ElectionsTotal atomic.Int64

	// 成为 Leader 的次数
	BecomeLeaderCount atomic.Int64

	// Leader 切换次数
	LeaderTransitions atomic.Int64

	// 最后选举时间
	LastElectionTime atomic.Value // time.Time

	// 当前 Leader 任期开始时间
	TermStartTime atomic.Value // time.Time
}

// NewLeaderElection 创建 Leader 选举器
func NewLeaderElection(
	localNodeID string,
	transport transport.Transport,
	config *LeaderElectionConfig,
) (*LeaderElection, error) {
	if config == nil {
		config = DefaultLeaderElectionConfig()
	}

	if transport == nil {
		return nil, types.NewClusterNilParameterError("transport")
	}

	if localNodeID == "" {
		return nil, types.NewClusterNilParameterError("localNodeID")
	}

	election := &LeaderElection{
		config:      config,
		localNodeID: localNodeID,
		transport:   transport,
		candidates:  make(map[string]*Node),
		stopCh:      make(chan struct{}),
		stats:       &LeaderElectionStats{},
	}

	// 初始化统计信息
	election.stats.LastElectionTime.Store(time.Time{})
	election.stats.TermStartTime.Store(time.Time{})

	return election, nil
}

// Start 启动 Leader 选举
func (le *LeaderElection) Start() error {
	if !le.started.CompareAndSwap(false, true) {
		return types.NewClusterServiceStateError("leader 选举", "已经启动")
	}

	logging.WithFields(map[string]any{
		"node_id":       le.localNodeID,
		"priority":      le.config.Priority,
		"auto_election": le.config.AutoElection,
	}).Info("启动 Leader 选举")

	// 启动选举循环
	le.stopWg.Add(1)
	go le.electionLoop()

	// 启动租约续约循环
	le.stopWg.Add(1)
	go le.leaseRenewalLoop()

	le.started.Store(true)

	logging.Info("Leader 选举启动成功")
	return nil
}

// Stop 停止 Leader 选举
func (le *LeaderElection) Stop() error {
	if !le.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	logging.Info("停止 Leader 选举...")

	// 关闭停止信号
	close(le.stopCh)

	// 等待所有协程退出
	le.stopWg.Wait()

	// 如果是 Leader，主动放弃领导权
	if le.IsLeader() {
		le.resignLeadership()
	}

	// 打印统计信息
	logging.WithFields(map[string]any{
		"elections_total":     le.stats.ElectionsTotal.Load(),
		"become_leader_count": le.stats.BecomeLeaderCount.Load(),
		"leader_transitions":  le.stats.LeaderTransitions.Load(),
	}).Info("Leader 选举统计")

	logging.Info("Leader 选举已停止")
	return nil
}

// ========================================
// 核心选举逻辑
// ========================================

// electionLoop 选举循环
func (le *LeaderElection) electionLoop() {
	defer le.stopWg.Done()

	// 首次启动时立即进行一次选举
	le.conductElection()

	ticker := time.NewTicker(le.config.ElectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			le.checkAndElect()

		case <-le.stopCh:
			return
		}
	}
}

// leaseRenewalLoop 租约续约循环
func (le *LeaderElection) leaseRenewalLoop() {
	defer le.stopWg.Done()

	ticker := time.NewTicker(le.config.LeaseTTL / 2) // 租约一半时间续约
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if le.IsLeader() {
				le.renewLease()
			}

		case <-le.stopCh:
			return
		}
	}
}

// conductElection 执行选举
func (le *LeaderElection) conductElection() {
	le.stats.ElectionsTotal.Add(1)
	le.stats.LastElectionTime.Store(time.Now())

	logging.WithField("node_id", le.localNodeID).Debug("执行 Leader 选举")

	// 获取所有候选节点
	candidates := le.getCandidates()

	if len(candidates) == 0 {
		logging.Warn("没有可用的候选节点")
		return
	}

	// 选择 Leader
	newLeader := le.selectLeader(candidates)

	// 更新 Leader
	le.updateLeader(newLeader)

	logging.WithFields(map[string]any{
		"leader": newLeader.NodeID,
		"term":   time.Now().Unix(),
	}).Info("Leader 选举完成")
}

// checkAndElect 检查并执行选举
func (le *LeaderElection) checkAndElect() {
	// 检查当前 Leader 存活状态
	currentLeader := le.GetCurrentLeader()
	if currentLeader != nil {
		// Leader 存活且租约未过期，无需选举
		if time.Now().Unix() < le.leaderLease.Load() {
			return
		}

		// Leader 租约过期，触发重新选举
		logging.WithFields(map[string]any{
			"current_leader": currentLeader.NodeID,
			"lease_expired":  true,
		}).Warn("Leader 租约过期，触发重新选举")
	}

	// 执行选举
	le.conductElection()
}

// selectLeader 选择 Leader
func (le *LeaderElection) selectLeader(candidates []*Node) *Node {
	var bestCandidate *Node
	var highestScore int64

	for _, candidate := range candidates {
		score := le.calculateScore(candidate)

		if score > highestScore {
			highestScore = score
			bestCandidate = candidate
		}
	}

	return bestCandidate
}

// calculateScore 计算节点得分
func (le *LeaderElection) calculateScore(node *Node) int64 {
	var score int64

	// 优先级权重（1000 为基础）
	score += int64(node.Priority) * 1000

	// 节点状态权重
	switch node.Status {
	case NodeStatusReady:
		score += 500
	case NodeStatusJoining:
		score += 200
	case NodeStatusInit:
		score += 100
	case NodeStatusLeaving:
		score += 50
	case NodeStatusFailed:
		score += 0
	}

	// 存活时间权重（最近的节点得分更高）
	uptime := time.Since(node.LastHeartbeat)
	if uptime < time.Minute {
		score += 100
	} else if uptime < 5*time.Minute {
		score += 50
	}

	return score
}

// updateLeader 更新 Leader
func (le *LeaderElection) updateLeader(newLeader *Node) {
	oldLeader := le.GetCurrentLeader()

	// 如果 Leader 没有变化，只需续约
	if oldLeader != nil && oldLeader.NodeID == newLeader.NodeID {
		le.renewLease()
		return
	}

	// Leader 发生变化
	le.currentLeader.Store(newLeader)
	le.leaderLease.Store(time.Now().Unix() + int64(le.config.LeaseTTL.Seconds()))

	// 更新本地节点状态
	if newLeader.NodeID == le.localNodeID {
		if !le.isLeader.Load() {
			le.isLeader.Store(true)
			le.stats.BecomeLeaderCount.Add(1)
			le.stats.TermStartTime.Store(time.Now())

			logging.WithField("node_id", le.localNodeID).Info("成为 Leader")
		}
	} else {
		if le.isLeader.Load() {
			le.isLeader.Store(false)
			logging.WithFields(map[string]any{
				"node_id":    le.localNodeID,
				"new_leader": newLeader.NodeID,
			}).Info("放弃 Leader 身份")
		}
	}

	// 统计 Leader 切换
	if oldLeader != nil && oldLeader.NodeID != newLeader.NodeID {
		le.stats.LeaderTransitions.Add(1)
	}
}

// renewLease 续约 Leader 租约
func (le *LeaderElection) renewLease() {
	le.leaderLease.Store(time.Now().Unix() + int64(le.config.LeaseTTL.Seconds()))
	logging.WithField("node_id", le.localNodeID).Debug("续约 Leader 租约")
}

// resignLeadership 放弃领导权
func (le *LeaderElection) resignLeadership() {
	le.isLeader.Store(false)
	le.currentLeader.Store((*Node)(nil))
	le.leaderLease.Store(0)

	logging.WithField("node_id", le.localNodeID).Info("放弃 Leader 身份")
}

// ========================================
// 节点管理
// ========================================

// getCandidates 获取候选节点列表
func (le *LeaderElection) getCandidates() []*Node {
	le.candidatesMu.RLock()
	defer le.candidatesMu.RUnlock()

	candidates := make([]*Node, 0, len(le.candidates))
	for _, node := range le.candidates {
		if node.Status == NodeStatusReady || node.Status == NodeStatusJoining {
			candidates = append(candidates, node)
		}
	}

	return candidates
}

// AddCandidate 添加候选节点
func (le *LeaderElection) AddCandidate(node *Node) error {
	le.candidatesMu.Lock()
	defer le.candidatesMu.Unlock()

	if node == nil {
		return types.NewClusterNilParameterError("节点")
	}

	if _, exists := le.candidates[node.NodeID]; exists {
		return types.NewClusterElectionError("候选节点已存在: "+node.NodeID, nil)
	}

	le.candidates[node.NodeID] = node

	logging.WithFields(map[string]any{
		"candidate": node.NodeID,
		"priority":  node.Priority,
	}).Info("添加候选节点")

	return nil
}

// RemoveCandidate 移除候选节点
func (le *LeaderElection) RemoveCandidate(nodeID string) {
	le.candidatesMu.Lock()
	defer le.candidatesMu.Unlock()

	delete(le.candidates, nodeID)

	// 如果移除的是当前 Leader，触发重新选举
	currentLeader := le.GetCurrentLeader()
	if currentLeader != nil && currentLeader.NodeID == nodeID {
		logging.WithFields(map[string]any{
			"removed_leader": nodeID,
		}).Warn("当前 Leader 被移除，触发重新选举")

		go le.conductElection()
	}

	logging.WithField("candidate", nodeID).Info("移除候选节点")
}

// ========================================
// 查询接口
// ========================================

// GetCurrentLeader 获取当前 Leader
func (le *LeaderElection) GetCurrentLeader() *Node {
	value := le.currentLeader.Load()
	if value == nil {
		return nil
	}
	return value.(*Node)
}

// IsLeader 检查是否为 Leader
func (le *LeaderElection) IsLeader() bool {
	return le.isLeader.Load()
}

// GetLeaseExpiry 获取租约过期时间
func (le *LeaderElection) GetLeaseExpiry() time.Time {
	expiry := le.leaderLease.Load()
	return time.Unix(expiry, 0)
}

// GetStats 获取统计信息
func (le *LeaderElection) GetStats() *LeaderElectionStats {
	return le.stats
}

// Campaign 竞选 Leader（手动触发选举）
func (le *LeaderElection) Campaign(ctx context.Context) error {
	if !le.config.AutoElection {
		return types.NewClusterElectionError("自动选举未启用", nil)
	}

	le.conductElection()

	return nil
}
