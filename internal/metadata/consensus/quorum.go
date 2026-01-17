// Package consensus 提供一致性协议实现
//
// Quorum 机制：增强的最终一致性，多数派确认
package consensus

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/store"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
)

// QuorumService Quorum 机制服务
//
// 核心特性:
//   - 增强的最终一致性：需要多数派确认（N/2 + 1）
//   - 超时回滚：超时后自动回滚
//   - 并行投票：并行发送提案到所有节点
//   - 允许脑裂：网络分区时可能出现 n1 commit, n2 rollback
//
// 使用场景:
//   - 分片创建/删除
//   - 主副本切换
//   - 节点加入/离开
//   - 其他重要元数据变更
type QuorumService struct {
	// 配置
	config *QuorumConfig

	// 依赖
	metaStore store.MVStore
	transport transport.Transport
	hlc       *clock.HLC

	// 节点列表
	nodes   []string
	nodesMu sync.RWMutex

	// 本地节点地址
	localAddr string

	// 提案状态追踪
	proposals   map[string]*ProposalState
	proposalsMu sync.RWMutex

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
	stopCh  chan struct{}

	// 统计信息
	stats *QuorumStats
}

// QuorumConfig Quorum 配置
type QuorumConfig struct {
	// Timeout 投票超时（默认 30 秒）
	Timeout time.Duration

	// RetryCount 重试次数（默认 3）
	RetryCount int

	// MinQuorum 最小法定人数（默认自动计算 N/2+1）
	// 设置为 0 表示自动计算
	MinQuorum int
}

// DefaultQuorumConfig 返回默认 Quorum 配置
func DefaultQuorumConfig() *QuorumConfig {
	return &QuorumConfig{
		Timeout:    30 * time.Second,
		RetryCount: 3,
		MinQuorum:  0, // 自动计算
	}
}

// ProposalState 提案状态
type ProposalState struct {
	// 提案ID
	ProposalID string

	// 提案内容
	Proposal *transport.QuorumProposeMessage

	// 投票结果
	votes      map[string]bool // node -> vote
	votesMu    sync.RWMutex
	voteCount  atomic.Int32 // 赞成票数
	totalVotes atomic.Int32 // 总票数

	// 决策状态
	decided    atomic.Bool
	approved   atomic.Bool
	decideCh   chan struct{} // 决策通知
	decideOnce sync.Once

	// 时间戳
	createTime time.Time
	decideTime time.Time
}

// QuorumStats Quorum 统计信息
type QuorumStats struct {
	// 提案总数
	ProposalsTotal atomic.Int64

	// 提案成功数
	ProposalsApproved atomic.Int64

	// 提案拒绝数
	ProposalsRejected atomic.Int64

	// 提案超时数
	ProposalsTimeout atomic.Int64

	// 平均投票延迟（毫秒）
	AvgVoteLatency atomic.Int64
}

// NewQuorumService 创建 Quorum 服务
func NewQuorumService(
	metaStore store.MVStore,
	transport transport.Transport,
	hlc *clock.HLC,
	localAddr string,
	nodes []string,
	config *QuorumConfig,
) (*QuorumService, error) {
	if config == nil {
		config = DefaultQuorumConfig()
	}

	if metaStore == nil {
		return nil, fmt.Errorf("metaStore 不能为空")
	}

	if transport == nil {
		return nil, fmt.Errorf("transport 不能为空")
	}

	if hlc == nil {
		return nil, fmt.Errorf("hlc 不能为空")
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("nodes 不能为空")
	}

	service := &QuorumService{
		config:    config,
		metaStore: metaStore,
		transport: transport,
		hlc:       hlc,
		nodes:     nodes,
		localAddr: localAddr,
		proposals: make(map[string]*ProposalState),
		stopCh:    make(chan struct{}),
		stats:     &QuorumStats{},
	}

	return service, nil
}

// Start 启动 Quorum 服务
func (q *QuorumService) Start() error {
	if !q.started.CompareAndSwap(false, true) {
		return fmt.Errorf("quorum 服务已经启动")
	}

	// 计算最小法定人数
	threshold := q.getQuorumThreshold()

	logging.WithFields(map[string]any{
		"timeout":     q.config.Timeout,
		"retry_count": q.config.RetryCount,
		"nodes":       len(q.nodes),
		"threshold":   threshold,
		"local_addr":  q.localAddr,
	}).Info("启动 Quorum 服务")

	// 启动消息处理协程
	go q.messageLoop()

	logging.Info("Quorum 服务启动成功")
	return nil
}

// Stop 停止 Quorum 服务
func (q *QuorumService) Stop() error {
	if !q.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	logging.Info("停止 Quorum 服务...")

	// 关闭停止信号
	close(q.stopCh)

	// 打印统计信息
	logging.WithFields(map[string]any{
		"proposals_total":    q.stats.ProposalsTotal.Load(),
		"proposals_approved": q.stats.ProposalsApproved.Load(),
		"proposals_rejected": q.stats.ProposalsRejected.Load(),
		"proposals_timeout":  q.stats.ProposalsTimeout.Load(),
		"avg_vote_latency":   q.stats.AvgVoteLatency.Load(),
	}).Info("Quorum 服务统计")

	logging.Info("Quorum 服务已停止")
	return nil
}

// ========================================
// 核心投票逻辑
// ========================================

// Propose 提交提案（发起 Quorum 投票）
//
// 流程:
//  1. 本地持久化提案
//  2. 并行发送提案到所有节点
//  3. 等待多数派确认
//  4. 如果获得多数派批准，提交决策
//  5. 如果超时或被拒绝，回滚
func (q *QuorumService) Propose(
	ctx context.Context,
	proposal *transport.QuorumProposeMessage,
) error {
	// 生成提案ID
	proposal.ProposalID = q.generateProposalID()
	proposal.Proposer = q.localAddr
	proposal.Timestamp = q.hlc.Now().PhysicalTime()

	// 创建提案状态
	state := &ProposalState{
		ProposalID: proposal.ProposalID,
		Proposal:   proposal,
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}

	// 记录提案
	q.proposalsMu.Lock()
	q.proposals[proposal.ProposalID] = state
	q.proposalsMu.Unlock()

	// 更新统计
	q.stats.ProposalsTotal.Add(1)

	logging.WithFields(map[string]any{
		"proposal_id": proposal.ProposalID,
		"key":         proposal.Key,
		"operation":   proposal.Operation,
	}).Info("发起 Quorum 提案")

	// 1. 本地持久化（预提交）
	if err := q.prepareProposal(proposal); err != nil {
		q.cleanupProposal(proposal.ProposalID)
		return fmt.Errorf("本地预提交失败: %w", err)
	}

	// 2. 本地投票（默认赞成）
	q.vote(state, q.localAddr, true, "")

	// 3. 并行发送提案到所有节点
	q.broadcastProposal(ctx, proposal)

	// 4. 等待决策
	_ = q.getQuorumThreshold()
	timeout := time.After(q.config.Timeout)

	select {
	case <-state.decideCh:
		// 决策已做出
		if state.approved.Load() {
			// 提案通过，执行提交
			if err := q.commitProposal(proposal); err != nil {
				q.stats.ProposalsRejected.Add(1)
				return fmt.Errorf("提交提案失败: %w", err)
			}

			q.stats.ProposalsApproved.Add(1)
			logging.WithField("proposal_id", proposal.ProposalID).Info("Quorum 提案已通过")
			return nil
		} else {
			// 提案被拒绝，回滚
			_ = q.rollbackProposal(proposal)
			q.stats.ProposalsRejected.Add(1)
			return fmt.Errorf("提案被拒绝")
		}

	case <-timeout:
		// 超时，回滚
		_ = q.rollbackProposal(proposal)
		q.cleanupProposal(proposal.ProposalID)
		q.stats.ProposalsTimeout.Add(1)
		return fmt.Errorf("提案超时")

	case <-ctx.Done():
		// 上下文取消
		_ = q.rollbackProposal(proposal)
		q.cleanupProposal(proposal.ProposalID)
		return ctx.Err()
	}
}

// prepareProposal 本地预提交提案
func (q *QuorumService) prepareProposal(proposal *transport.QuorumProposeMessage) error {
	switch proposal.Operation {
	case "put":
		return q.metaStore.Put(proposal.Key, proposal.Value)

	case "delete":
		return q.metaStore.Delete(proposal.Key)

	default:
		return fmt.Errorf("未知操作类型: %s", proposal.Operation)
	}
}

// commitProposal 提交提案
func (q *QuorumService) commitProposal(proposal *transport.QuorumProposeMessage) error {
	// 发送决策消息
	decideMsg := &transport.QuorumDecideMessage{
		ProposalID: proposal.ProposalID,
		Approved:   true,
		Version:    uint64(q.hlc.Now().PhysicalTime()),
	}

	return q.broadcastDecision(decideMsg)
}

// rollbackProposal 回滚提案
func (q *QuorumService) rollbackProposal(proposal *transport.QuorumProposeMessage) error {
	// 发送决策消息（拒绝）
	decideMsg := &transport.QuorumDecideMessage{
		ProposalID: proposal.ProposalID,
		Approved:   false,
	}

	_ = q.broadcastDecision(decideMsg)

	// TODO: 本地回滚逻辑
	return nil
}

// broadcastProposal 广播提案到所有节点
func (q *QuorumService) broadcastProposal(
	ctx context.Context,
	proposal *transport.QuorumProposeMessage,
) {
	q.nodesMu.RLock()
	nodes := make([]string, len(q.nodes))
	copy(nodes, q.nodes)
	q.nodesMu.RUnlock()

	for _, node := range nodes {
		if node == q.localAddr {
			continue // 跳过本地节点
		}

		go func(addr string) {
			if err := q.transport.Send(ctx, addr, proposal); err != nil {
				logging.WithFields(map[string]any{
					"addr":        addr,
					"proposal_id": proposal.ProposalID,
					"error":       err,
				}).Warn("发送提案失败")
			}
		}(node)
	}
}

// broadcastDecision 广播决策到所有节点
func (q *QuorumService) broadcastDecision(decideMsg *transport.QuorumDecideMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	q.nodesMu.RLock()
	nodes := make([]string, len(q.nodes))
	copy(nodes, q.nodes)
	q.nodesMu.RUnlock()

	for _, node := range nodes {
		if node == q.localAddr {
			continue
		}

		if err := q.transport.Send(ctx, node, decideMsg); err != nil {
			logging.WithFields(map[string]any{
				"addr":     node,
				"error":    err,
				"approved": decideMsg.Approved,
			}).Warn("发送决策失败")
		}
	}

	return nil
}

// ========================================
// 投票处理
// ========================================

// Vote 处理投票请求
func (q *QuorumService) Vote(voteMsg *transport.QuorumVoteMessage) error {
	proposalID := voteMsg.ProposalID

	// 查找提案状态
	q.proposalsMu.RLock()
	state, exists := q.proposals[proposalID]
	q.proposalsMu.RUnlock()

	if !exists {
		return fmt.Errorf("提案不存在: %s", proposalID)
	}

	// 记录投票
	q.vote(state, voteMsg.Voter, voteMsg.Vote, voteMsg.Reason)

	return nil
}

// vote 记录投票并检查是否达到法定人数
func (q *QuorumService) vote(
	state *ProposalState,
	voter string,
	approve bool,
	reason string,
) {
	state.votesMu.Lock()
	defer state.votesMu.Unlock()

	// 检查是否已投过票
	if _, voted := state.votes[voter]; voted {
		return
	}

	// 记录投票
	state.votes[voter] = approve
	state.totalVotes.Add(1)

	if approve {
		state.voteCount.Add(1)
	}

	// 更新统计
	latency := time.Since(state.createTime).Milliseconds()
	q.stats.AvgVoteLatency.Store(
		(q.stats.AvgVoteLatency.Load() * latency) / (q.stats.ProposalsTotal.Load() + 1),
	)

	logging.WithFields(map[string]any{
		"proposal_id": state.ProposalID,
		"voter":       voter,
		"vote":        approve,
		"reason":      reason,
		"vote_count":  state.voteCount.Load(),
		"total_votes": state.totalVotes.Load(),
	}).Debug("记录投票")

	// 检查是否达到法定人数
	q.checkQuorum(state)
}

// checkQuorum 检查是否达到法定人数
func (q *QuorumService) checkQuorum(state *ProposalState) {
	threshold := q.getQuorumThreshold()
	totalNodes := int32(len(q.nodes))

	// 检查是否获得多数赞成
	if state.voteCount.Load() >= threshold {
		state.decide(true)
		return
	}

	// 检查是否无法获得多数赞成
	remainingVotes := totalNodes - state.totalVotes.Load()
	neededVotes := threshold - state.voteCount.Load()

	if remainingVotes < neededVotes {
		state.decide(false)
	}
}

// decide 做出决策
func (s *ProposalState) decide(approved bool) {
	s.decideOnce.Do(func() {
		s.approved.Store(approved)
		s.decided.Store(true)
		s.decideTime = time.Now()

		// 通知等待者
		close(s.decideCh)

		logging.WithFields(map[string]any{
			"proposal_id": s.ProposalID,
			"approved":    approved,
			"duration":    time.Since(s.createTime).Milliseconds(),
		}).Info("Quorum 决策已做出")
	})
}

// ========================================
// 消息处理
// ========================================

// messageLoop 消息处理循环
func (q *QuorumService) messageLoop() {
	recvCh := q.transport.Receive()

	for {
		select {
		case msg, ok := <-recvCh:
			if !ok {
				logging.Info("接收通道已关闭")
				return
			}

			q.handleMessage(msg)

		case <-q.stopCh:
			return
		}
	}
}

// handleMessage 处理接收到的消息
func (q *QuorumService) handleMessage(msg transport.Message) {
	switch msg.Type() {
	case transport.MessageTypeQuorumPropose:
		q.handleQuorumPropose(msg)

	case transport.MessageTypeQuorumVote:
		q.handleQuorumVote(msg)

	case transport.MessageTypeQuorumDecide:
		q.handleQuorumDecide(msg)
	}
}

// handleQuorumPropose 处理提案消息
func (q *QuorumService) handleQuorumPropose(msg transport.Message) {
	proposal, ok := msg.(*transport.QuorumProposeMessage)
	if !ok {
		return
	}

	logging.WithFields(map[string]any{
		"proposal_id": proposal.ProposalID,
		"proposer":    proposal.Proposer,
		"key":         proposal.Key,
		"operation":   proposal.Operation,
	}).Debug("收到 Quorum 提案")

	// 创建提案状态
	state := &ProposalState{
		ProposalID: proposal.ProposalID,
		Proposal:   proposal,
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}

	q.proposalsMu.Lock()
	q.proposals[proposal.ProposalID] = state
	q.proposalsMu.Unlock()

	// 本地预提交
	if err := q.prepareProposal(proposal); err != nil {
		// 预提交失败，投反对票
		_ = q.sendVote(proposal.ProposalID, false, err.Error())
		return
	}

	// 投赞成票
	_ = q.sendVote(proposal.ProposalID, true, "")
}

// handleQuorumVote 处理投票消息
func (q *QuorumService) handleQuorumVote(msg transport.Message) {
	voteMsg, ok := msg.(*transport.QuorumVoteMessage)
	if !ok {
		return
	}

	if err := q.Vote(voteMsg); err != nil {
		logging.WithFields(map[string]any{
			"error": err,
		}).Warn("处理投票失败")
	}
}

// handleQuorumDecide 处理决策消息
func (q *QuorumService) handleQuorumDecide(msg transport.Message) {
	decideMsg, ok := msg.(*transport.QuorumDecideMessage)
	if !ok {
		return
	}

	q.proposalsMu.RLock()
	state, exists := q.proposals[decideMsg.ProposalID]
	q.proposalsMu.RUnlock()

	if !exists {
		return
	}

	// 应用决策
	state.decide(decideMsg.Approved)

	// 清理提案
	if decideMsg.Approved {
		// 提案通过，无需回滚
	} else {
		// 提案被拒绝，回滚
		_ = q.rollbackProposal(state.Proposal)
	}

	q.cleanupProposal(decideMsg.ProposalID)
}

// sendVote 发送投票
func (q *QuorumService) sendVote(
	proposalID string,
	approve bool,
	reason string,
) error {
	voteMsg := &transport.QuorumVoteMessage{
		ProposalID: proposalID,
		Voter:      q.localAddr,
		Vote:       approve,
		Reason:     reason,
	}

	// 发送给提案发起者
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 从提案状态获取发起者地址
	q.proposalsMu.RLock()
	state, exists := q.proposals[proposalID]
	q.proposalsMu.RUnlock()

	if !exists {
		return fmt.Errorf("提案不存在")
	}

	proposer := state.Proposal.Proposer
	if proposer == q.localAddr {
		// 本地提案，直接处理
		return q.Vote(voteMsg)
	}

	return q.transport.Send(ctx, proposer, voteMsg)
}

// ========================================
// 辅助方法
// ========================================

// getQuorumThreshold 计算法定人数阈值
func (q *QuorumService) getQuorumThreshold() int32 {
	if q.config.MinQuorum > 0 {
		return int32(q.config.MinQuorum)
	}

	// 默认：N/2 + 1
	return int32(len(q.nodes))/2 + 1
}

// generateProposalID 生成提案ID
func (q *QuorumService) generateProposalID() string {
	// 使用 HLC 时间戳生成唯一ID
	hlc := q.hlc.Now()
	return fmt.Sprintf("proposal-%d-%d", hlc.PhysicalTime(), hlc.LogicalCounter())
}

// cleanupProposal 清理提案状态
func (q *QuorumService) cleanupProposal(proposalID string) {
	q.proposalsMu.Lock()
	defer q.proposalsMu.Unlock()

	delete(q.proposals, proposalID)
}

// AddNode 添加节点
func (q *QuorumService) AddNode(addr string) {
	q.nodesMu.Lock()
	defer q.nodesMu.Unlock()

	for _, node := range q.nodes {
		if node == addr {
			return
		}
	}

	q.nodes = append(q.nodes, addr)
	logging.WithField("node", addr).Info("已添加 Quorum 节点")
}

// RemoveNode 移除节点
func (q *QuorumService) RemoveNode(addr string) {
	q.nodesMu.Lock()
	defer q.nodesMu.Unlock()

	newNodes := make([]string, 0, len(q.nodes))
	for _, node := range q.nodes {
		if node != addr {
			newNodes = append(newNodes, node)
		}
	}

	q.nodes = newNodes
	logging.WithField("node", addr).Info("已移除 Quorum 节点")
}

// GetStats 获取统计信息
func (q *QuorumService) GetStats() *QuorumStats {
	return q.stats
}

// GetProposalState 获取提案状态（用于测试）
func (q *QuorumService) GetProposalState(proposalID string) (*ProposalState, bool) {
	q.proposalsMu.RLock()
	defer q.proposalsMu.RUnlock()

	state, exists := q.proposals[proposalID]
	return state, exists
}
