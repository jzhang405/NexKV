// Package consensus 提供一致性协议实现
//
// TwoPC (Two-Phase Commit) 协议：无协调者简化版
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
	"github.com/jzhang405/NexKV/internal/metadata/uuid"
)

// TwoPCService 两阶段提交服务
//
// 核心特性:
//   - 无协调者简化版：发起节点兼任协调者
//   - 直接预提交：砍掉 Prepare 阶段，无资源锁定
//   - Gossip 状态同步：异步扩散事务状态
//   - 故障自愈：基于状态查询的自愈机制
//
// 使用场景:
//   - 跨分片事务
//   - 原子性多键操作
//   - 分布式事务协调
type TwoPCService struct {
	// 配置
	config *TwoPCConfig

	// 依赖
	metaStore store.MVStore
	transport transport.Transport
	hlc       *clock.HLC
	uuidGen   uuid.UUIDGenerator

	// 节点列表
	nodes   []string
	nodesMu sync.RWMutex

	// 本地节点地址
	localAddr string

	// 事务状态追踪
	transactions   map[string]*TransactionState
	transactionsMu sync.RWMutex

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
	stopCh  chan struct{}

	// 统计信息
	stats *TwoPCStats
}

// TwoPCConfig 2PC 配置
type TwoPCConfig struct {
	// Timeout 单阶段超时（默认 10 秒）
	Timeout time.Duration

	// EnableGossipSync 是否启用 Gossip 状态同步
	EnableGossipSync bool

	// GossipInterval Gossip 同步间隔（默认 5 秒）
	GossipInterval time.Duration
}

// DefaultTwoPCConfig 返回默认 2PC 配置
func DefaultTwoPCConfig() *TwoPCConfig {
	return &TwoPCConfig{
		Timeout:          10 * time.Second,
		EnableGossipSync: true,
		GossipInterval:   5 * time.Second,
	}
}

// TransactionState 事务状态
type TransactionState struct {
	// 事务ID（使用 UUID v7）
	TransactionID string

	// 参与者节点列表
	Participants []string

	// 操作列表
	Operations []transport.Operation

	// 状态
	State atomic.Value // TxState

	// 投票结果
	votes      map[string]string // node -> vote ("commit", "abort")
	votesMu    sync.RWMutex
	voteCount  atomic.Int32 // commit 票数
	totalVotes atomic.Int32 // 总票数

	// 时间戳
	Timestamp *clock.HLC

	// 创建时间
	CreateTime time.Time

	// 最后更新时间
	UpdateTime time.Time

	// 完成通道
	doneCh chan struct{}
}

// TxState 事务状态
type TxState int

const (
	// TxStateInit 初始状态
	TxStateInit TxState = iota

	// TxStatePreCommit 预提交
	TxStatePreCommit

	// TxStateCommitted 已提交
	TxStateCommitted

	// TxStateAborted 已中止
	TxStateAborted
)

// String 返回状态的字符串表示
func (s TxState) String() string {
	switch s {
	case TxStateInit:
		return "init"
	case TxStatePreCommit:
		return "pre_commit"
	case TxStateCommitted:
		return "committed"
	case TxStateAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// TwoPCStats 2PC 统计信息
type TwoPCStats struct {
	// 事务总数
	TransactionsTotal atomic.Int64

	// 提交的事务数
	TransactionsCommitted atomic.Int64

	// 中止的事务数
	TransactionsAborted atomic.Int64

	// 超时的事务数
	TransactionsTimeout atomic.Int64

	// 平均事务延迟（毫秒）
	AvgTxLatency atomic.Int64
}

// NewTwoPCService 创建 2PC 服务
func NewTwoPCService(
	metaStore store.MVStore,
	transport transport.Transport,
	hlc *clock.HLC,
	uuidGen uuid.UUIDGenerator,
	localAddr string,
	nodes []string,
	config *TwoPCConfig,
) (*TwoPCService, error) {
	if config == nil {
		config = DefaultTwoPCConfig()
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

	if uuidGen == nil {
		return nil, fmt.Errorf("uuidGen 不能为空")
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("nodes 不能为空")
	}

	service := &TwoPCService{
		config:       config,
		metaStore:    metaStore,
		transport:    transport,
		hlc:          hlc,
		uuidGen:      uuidGen,
		nodes:        nodes,
		localAddr:    localAddr,
		transactions: make(map[string]*TransactionState),
		stopCh:       make(chan struct{}),
		stats:        &TwoPCStats{},
	}

	return service, nil
}

// Start 启动 2PC 服务
func (t *TwoPCService) Start() error {
	if !t.started.CompareAndSwap(false, true) {
		return fmt.Errorf("2PC 服务已经启动")
	}

	logging.WithFields(map[string]any{
		"timeout":         t.config.Timeout,
		"gossip_sync":     t.config.EnableGossipSync,
		"gossip_interval": t.config.GossipInterval,
		"nodes":           len(t.nodes),
		"local_addr":      t.localAddr,
	}).Info("启动 2PC 服务")

	// 启动消息处理协程
	go t.messageLoop()

	// 启动 Gossip 状态同步
	if t.config.EnableGossipSync {
		go t.gossipLoop()
	}

	logging.Info("2PC 服务启动成功")
	return nil
}

// Stop 停止 2PC 服务
func (t *TwoPCService) Stop() error {
	if !t.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	logging.Info("停止 2PC 服务...")

	// 打印统计信息
	logging.WithFields(map[string]any{
		"tx_total":     t.stats.TransactionsTotal.Load(),
		"tx_committed": t.stats.TransactionsCommitted.Load(),
		"tx_aborted":   t.stats.TransactionsAborted.Load(),
		"tx_timeout":   t.stats.TransactionsTimeout.Load(),
		"avg_latency":  t.stats.AvgTxLatency.Load(),
	}).Info("2PC 服务统计")

	// 关闭停止信号
	close(t.stopCh)

	logging.Info("2PC 服务已停止")
	return nil
}

// ========================================
// 核心事务逻辑
// ========================================

// Execute 执行分布式事务
//
// 流程:
//  1. 发起节点兼任协调者
//  2. 直接进入 Pre-Commit 阶段（砍掉 Prepare）
//  3. 所有参与节点执行子事务，写入 WAL
//  4. 发起节点收集所有响应，做出决策
//  5. 通过 Gossip 异步扩散决策
//  6. 故障节点通过查询状态自愈
func (t *TwoPCService) Execute(
	ctx context.Context,
	operations []transport.Operation,
) error {
	// 生成事务 ID（使用 UUID v7，时间有序）
	txID := t.uuidGen.GenerateTransactionID()
	timestamp := t.hlc.Now()

	// 确定参与者节点
	participants := t.determineParticipants(operations)

	logging.WithFields(map[string]any{
		"tx_id":        txID,
		"participants": len(participants),
		"operations":   len(operations),
	}).Info("开始执行 2PC 事务")

	// 创建事务状态
	txState := &TransactionState{
		TransactionID: txID,
		Participants:  participants,
		Operations:    operations,
		votes:         make(map[string]string),
		Timestamp:     timestamp,
		CreateTime:    time.Now(),
		UpdateTime:    time.Now(),
		doneCh:        make(chan struct{}),
	}
	txState.State.Store(TxStateInit)

	// 记录事务
	t.transactionsMu.Lock()
	t.transactions[txID] = txState
	t.transactionsMu.Unlock()

	// 更新统计
	t.stats.TransactionsTotal.Add(1)

	// 阶段1：预提交
	if err := t.preCommit(ctx, txState); err != nil {
		t.abortTransaction(txState, err)
		t.cleanupTransaction(txID)
		return fmt.Errorf("预提交失败: %w", err)
	}

	// 阶段2：决策
	decision := t.makeDecision(txState)

	// 阶段3：执行决策
	if decision == "commit" {
		if err := t.commitTransaction(txState); err != nil {
			t.stats.TransactionsAborted.Add(1)
			return fmt.Errorf("提交事务失败: %w", err)
		}

		t.stats.TransactionsCommitted.Add(1)
		logging.WithField("tx_id", txID).Info("2PC 事务已提交")
	} else {
		t.abortTransaction(txState, fmt.Errorf("决策: %s", decision))
		t.stats.TransactionsAborted.Add(1)
		return fmt.Errorf("事务已中止")
	}

	// 清理事务状态
	t.cleanupTransaction(txID)

	return nil
}

// preCommit 预提交阶段
func (t *TwoPCService) preCommit(
	ctx context.Context,
	txState *TransactionState,
) error {
	txState.State.Store(TxStatePreCommit)
	txState.UpdateTime = time.Now()

	logging.WithField("tx_id", txState.TransactionID).Debug("开始预提交阶段")

	// 本地预提交
	if err := t.preCommitLocal(txState); err != nil {
		return fmt.Errorf("本地预提交失败: %w", err)
	}

	// 并行发送预提交请求到所有参与者
	results := t.broadcastPreCommit(ctx, txState)

	// 等待所有响应或超时
	timeout := time.After(t.config.Timeout)

	preCommitCount := 1 // 本地已成功
	for _, result := range results {
		select {
		case err := <-result:
			if err != nil {
				logging.WithFields(map[string]any{
					"tx_id": txState.TransactionID,
					"error": err,
				}).Warn("参与者预提交失败")
				// 继续等待其他参与者
			} else {
				preCommitCount++
			}

		case <-timeout:
			return fmt.Errorf("预提交超时")

		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// 检查是否所有参与者都成功
	if preCommitCount != len(txState.Participants) {
		return fmt.Errorf("部分参与者预提交失败")
	}

	logging.WithField("tx_id", txState.TransactionID).Debug("预提交阶段完成")
	return nil
}

// preCommitLocal 本地预提交
func (t *TwoPCService) preCommitLocal(txState *TransactionState) error {
	// 执行所有操作
	for _, op := range txState.Operations {
		if err := t.executeOperation(&op); err != nil {
			// 回滚已执行的操作
			t.rollbackOperations(txState.Operations[:0])
			return err
		}
	}

	// 记录投票
	t.recordVote(txState, t.localAddr, "commit")

	return nil
}

// broadcastPreCommit 广播预提交请求
func (t *TwoPCService) broadcastPreCommit(
	ctx context.Context,
	txState *TransactionState,
) []chan error {
	resultChs := make([]chan error, 0)

	for _, participant := range txState.Participants {
		if participant == t.localAddr {
			continue // 跳过本地节点
		}

		resultCh := make(chan error, 1)
		resultChs = append(resultChs, resultCh)

		go func(addr string, ch chan error) {
			prepareMsg := &transport.TwoPCPrepareMessage{
				TransactionID: txState.TransactionID,
				Participants:  txState.Participants,
				Operations:    txState.Operations,
				Timeout:       txState.Timestamp.PhysicalTime(),
			}

			ch <- t.transport.Send(ctx, addr, prepareMsg)
		}(participant, resultCh)
	}

	return resultChs
}

// makeDecision 做出决策
func (t *TwoPCService) makeDecision(txState *TransactionState) string {
	txState.votesMu.RLock()
	defer txState.votesMu.RUnlock()

	commitCount := 0
	abortCount := 0

	for _, vote := range txState.votes {
		switch vote {
		case "commit":
			commitCount++
		case "abort":
			abortCount++
		}
	}

	// 决策规则：全部 commit 才能提交，否则中止
	if commitCount == len(txState.Participants) && abortCount == 0 {
		return "commit"
	}

	return "abort"
}

// commitTransaction 提交事务
func (t *TwoPCService) commitTransaction(txState *TransactionState) error {
	txState.State.Store(TxStateCommitted)
	txState.UpdateTime = time.Now()

	// 发送提交消息
	commitMsg := &transport.TwoPCCommitMessage{
		TransactionID: txState.TransactionID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	for _, participant := range txState.Participants {
		if participant == t.localAddr {
			continue
		}

		if err := t.transport.Send(ctx, participant, commitMsg); err != nil {
			logging.WithFields(map[string]any{
				"tx_id": txState.TransactionID,
				"node":  participant,
				"error": err,
			}).Warn("发送提交消息失败")
		}
	}

	// 更新统计
	latency := time.Since(txState.CreateTime).Milliseconds()
	t.stats.AvgTxLatency.Store(
		(t.stats.AvgTxLatency.Load() * latency) / (t.stats.TransactionsTotal.Load() + 1),
	)

	close(txState.doneCh)

	return nil
}

// abortTransaction 中止事务
func (t *TwoPCService) abortTransaction(txState *TransactionState, reason error) {
	txState.State.Store(TxStateAborted)
	txState.UpdateTime = time.Now()

	logging.WithFields(map[string]any{
		"tx_id":  txState.TransactionID,
		"reason": reason,
	}).Info("中止事务")

	// 发送回滚消息
	rollbackMsg := &transport.TwoPCRollbackMessage{
		TransactionID: txState.TransactionID,
		Reason:        reason.Error(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	for _, participant := range txState.Participants {
		if participant == t.localAddr {
			continue
		}

		if err := t.transport.Send(ctx, participant, rollbackMsg); err != nil {
			logging.WithFields(map[string]any{
				"tx_id": txState.TransactionID,
				"node":  participant,
				"error": err,
			}).Warn("发送回滚消息失败")
		}
	}

	close(txState.doneCh)
}

// ========================================
// 消息处理
// ========================================

// messageLoop 消息处理循环
func (t *TwoPCService) messageLoop() {
	recvCh := t.transport.Receive()

	for {
		select {
		case msg, ok := <-recvCh:
			if !ok {
				logging.Info("接收通道已关闭")
				return
			}
			t.handleMessage(msg)
		}
	}
}

// handleMessage 处理接收到的消息
func (t *TwoPCService) handleMessage(msg transport.Message) {
	switch msg.Type() {
	case transport.MessageType2PCPrepare:
		t.handlePrepare(msg)

	case transport.MessageType2PCPrepareReply:
		t.handlePrepareReply(msg)

	case transport.MessageType2PCCommit:
		t.handleCommit(msg)

	case transport.MessageType2PCRollback:
		t.handleRollback(msg)

	case transport.MessageType2PCCommitReply:
		t.handleCommitReply(msg)

	case transport.MessageType2PCRollbackReply:
		t.handleRollbackReply(msg)
	}
}

// handlePrepare 处理准备阶段消息
func (t *TwoPCService) handlePrepare(msg transport.Message) {
	prepareMsg, ok := msg.(*transport.TwoPCPrepareMessage)
	if !ok {
		return
	}

	txID := prepareMsg.TransactionID

	logging.WithFields(map[string]any{
		"tx_id": txID,
		"ops":   len(prepareMsg.Operations),
	}).Debug("收到 2PC 准备请求")

	// 创建事务状态
	txState := &TransactionState{
		TransactionID: txID,
		Participants:  prepareMsg.Participants,
		Operations:    prepareMsg.Operations,
		Timestamp:     t.hlc.Now(),
		CreateTime:    time.Now(),
		UpdateTime:    time.Now(),
		doneCh:        make(chan struct{}),
		votes:         make(map[string]string),
	}
	txState.State.Store(TxStateInit)

	t.transactionsMu.Lock()
	t.transactions[txID] = txState
	t.transactionsMu.Unlock()

	// 执行预提交
	if err := t.preCommitLocal(txState); err != nil {
		// 预提交失败，发送 abort 投票
		t.sendPrepareReply(prepareMsg, "abort", err.Error())
		return
	}

	// 预提交成功，发送 commit 投票
	t.sendPrepareReply(prepareMsg, "commit", "")
}

// handleCommit 处理提交消息
func (t *TwoPCService) handleCommit(msg transport.Message) {
	commitMsg, ok := msg.(*transport.TwoPCCommitMessage)
	if !ok {
		return
	}

	txID := commitMsg.TransactionID

	t.transactionsMu.RLock()
	txState, exists := t.transactions[txID]
	t.transactionsMu.RUnlock()

	if !exists {
		logging.WithField("tx_id", txID).Warn("事务不存在")
		return
	}

	txState.State.Store(TxStateCommitted)
	txState.UpdateTime = time.Now()

	logging.WithField("tx_id", txID).Info("事务已提交")

	// 发送响应
	t.sendCommitReply(commitMsg, true)

	close(txState.doneCh)
}

// handleRollback 处理回滚消息
func (t *TwoPCService) handleRollback(msg transport.Message) {
	rollbackMsg, ok := msg.(*transport.TwoPCRollbackMessage)
	if !ok {
		return
	}

	txID := rollbackMsg.TransactionID

	t.transactionsMu.RLock()
	txState, exists := t.transactions[txID]
	t.transactionsMu.RUnlock()

	if !exists {
		logging.WithField("tx_id", txID).Warn("事务不存在")
		return
	}

	txState.State.Store(TxStateAborted)
	txState.UpdateTime = time.Now()

	logging.WithFields(map[string]any{
		"tx_id":  txID,
		"reason": rollbackMsg.Reason,
	}).Info("事务已回滚")

	// 回滚操作
	t.rollbackOperations(txState.Operations)

	// 发送响应
	t.sendRollbackReply(rollbackMsg, true)

	close(txState.doneCh)
}

// handlePrepareReply 处理准备阶段响应消息
func (t *TwoPCService) handlePrepareReply(msg transport.Message) {
	replyMsg, ok := msg.(*transport.TwoPCPrepareReplyMessage)
	if !ok {
		return
	}

	txID := replyMsg.TransactionID
	participant := replyMsg.Participant
	vote := replyMsg.Vote

	t.transactionsMu.Lock()
	txState, exists := t.transactions[txID]
	if !exists {
		t.transactionsMu.Unlock()
		logging.WithFields(map[string]any{
			"tx_id": txID,
			"from":  participant,
		}).Warn("收到不存在事务的准备响应")
		return
	}
	t.transactionsMu.Unlock()

	// 记录投票
	txState.votes[participant] = vote

	logging.WithFields(map[string]any{
		"tx_id": txID,
		"from":  participant,
		"vote":  vote,
	}).Debug("记录准备投票")

	// TODO: 检查是否所有参与者都已响应
}

// handleCommitReply 处理提交阶段响应消息
func (t *TwoPCService) handleCommitReply(msg transport.Message) {
	replyMsg, ok := msg.(*transport.TwoPCCommitReplyMessage)
	if !ok {
		return
	}

	txID := replyMsg.TransactionID
	participant := replyMsg.Participant
	success := replyMsg.Success

	if !success {
		logging.WithFields(map[string]any{
			"tx_id": txID,
			"from":  participant,
		}).Warn("参与者提交失败")
	}

	// TODO: 追踪提交确认状态
}

// handleRollbackReply 处理回滚阶段响应消息
func (t *TwoPCService) handleRollbackReply(msg transport.Message) {
	replyMsg, ok := msg.(*transport.TwoPCRollbackReplyMessage)
	if !ok {
		return
	}

	txID := replyMsg.TransactionID
	participant := replyMsg.Participant
	success := replyMsg.Success

	if !success {
		logging.WithFields(map[string]any{
			"tx_id": txID,
			"from":  participant,
		}).Warn("参与者回滚失败")
	}

	// TODO: 追踪回滚确认状态
}

// ========================================
// 辅助方法
// ========================================

// determineParticipants 确定参与者节点
func (t *TwoPCService) determineParticipants(
	operations []transport.Operation,
) []string {
	// 简化实现：使用所有节点
	// 实际应根据数据分片确定参与者
	t.nodesMu.RLock()
	defer t.nodesMu.RUnlock()

	participants := make([]string, len(t.nodes))
	copy(participants, t.nodes)

	return participants
}

// executeOperation 执行操作
func (t *TwoPCService) executeOperation(op *transport.Operation) error {
	switch op.Type {
	case "put":
		return t.metaStore.Put(op.Key, op.Value)

	case "delete":
		return t.metaStore.Delete(op.Key)

	default:
		return fmt.Errorf("未知操作类型: %s", op.Type)
	}
}

// rollbackOperations 回滚操作
func (t *TwoPCService) rollbackOperations(operations []transport.Operation) {
	// 简化实现：只支持 put/delete，不回滚
	// 实际应根据 WAL 重放回滚
}

// recordVote 记录投票
func (t *TwoPCService) recordVote(
	txState *TransactionState,
	voter string,
	vote string,
) {
	txState.votesMu.Lock()
	defer txState.votesMu.Unlock()

	txState.votes[voter] = vote
	txState.totalVotes.Add(1)

	if vote == "commit" {
		txState.voteCount.Add(1)
	}
}

// sendPrepareReply 发送准备响应
func (t *TwoPCService) sendPrepareReply(
	prepareMsg *transport.TwoPCPrepareMessage,
	vote string,
	reason string,
) error {
	replyMsg := &transport.TwoPCPrepareReplyMessage{
		TransactionID: prepareMsg.TransactionID,
		Participant:   t.localAddr,
		Vote:          vote,
		Reason:        reason,
	}

	// 发送给发起者（假设是第一个参与者）
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	coordinator := prepareMsg.Participants[0]
	if coordinator == t.localAddr {
		// 本地事务，直接处理
		t.transactionsMu.RLock()
		txState, exists := t.transactions[prepareMsg.TransactionID]
		t.transactionsMu.RUnlock()

		if exists {
			t.recordVote(txState, t.localAddr, vote)
		}
		return nil
	}

	return t.transport.Send(ctx, coordinator, replyMsg)
}

// sendCommitReply 发送提交响应
func (t *TwoPCService) sendCommitReply(commitMsg *transport.TwoPCCommitMessage, success bool) error {
	replyMsg := &transport.TwoPCCommitReplyMessage{
		TransactionID: commitMsg.TransactionID,
		Participant:   t.localAddr,
		Success:       success,
	}

	// 发送给发起者
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	coordinator := "" // TODO: 从事务状态获取
	if coordinator == t.localAddr {
		return nil
	}

	return t.transport.Send(ctx, coordinator, replyMsg)
}

// sendRollbackReply 发送回滚响应
func (t *TwoPCService) sendRollbackReply(rollbackMsg *transport.TwoPCRollbackMessage, success bool) error {
	replyMsg := &transport.TwoPCRollbackReplyMessage{
		TransactionID: rollbackMsg.TransactionID,
		Participant:   t.localAddr,
		Success:       success,
	}

	// 发送给发起者
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	coordinator := "" // TODO: 从事务状态获取
	if coordinator == t.localAddr {
		return nil
	}

	return t.transport.Send(ctx, coordinator, replyMsg)
}

// cleanupTransaction 清理事务状态
func (t *TwoPCService) cleanupTransaction(txID string) {
	t.transactionsMu.Lock()
	defer t.transactionsMu.Unlock()

	if txState, exists := t.transactions[txID]; exists {
		select {
		case <-txState.doneCh:
			// 已完成
		default:
			// 未完成，强制关闭
			close(txState.doneCh)
		}
	}

	delete(t.transactions, txID)
}

// gossipLoop Gossip 状态同步循环
func (t *TwoPCService) gossipLoop() {
	ticker := time.NewTicker(t.config.GossipInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.gossipTransactionStates()

		case <-t.stopCh:
			return
		}
	}
}

// gossipTransactionStates Gossip 事务状态
func (t *TwoPCService) gossipTransactionStates() {
	// TODO: 实现 Gossip 状态同步
	// 将活跃事务的状态扩散到其他节点
}

// ========================================
// 故障自愈
// ========================================

// RecoverTransaction 恢复事务（故障自愈）
func (t *TwoPCService) RecoverTransaction(txID string) error {
	t.transactionsMu.RLock()
	txState, exists := t.transactions[txID]
	t.transactionsMu.RUnlock()

	if !exists {
		return fmt.Errorf("事务不存在: %s", txID)
	}

	// 检查事务状态
	state := txState.State.Load().(TxState)

	switch state {
	case TxStatePreCommit:
		// 预提交状态，需要查询最终决策
		return t.queryTransactionDecision(txState)

	case TxStateCommitted, TxStateAborted:
		// 已完成，无需恢复
		return nil

	default:
		return fmt.Errorf("未知事务状态: %v", state)
	}
}

// queryTransactionDecision 查询事务决策
func (t *TwoPCService) queryTransactionDecision(txState *TransactionState) error {
	// 通过 Gossip 查询事务的最终决策
	// TODO: 实现 Gossip 查询
	return fmt.Errorf("未实现")
}

// ========================================
// 其他方法
// ========================================

// AddNode 添加节点
func (t *TwoPCService) AddNode(addr string) {
	t.nodesMu.Lock()
	defer t.nodesMu.Unlock()

	for _, node := range t.nodes {
		if node == addr {
			return
		}
	}

	t.nodes = append(t.nodes, addr)
	logging.WithField("node", addr).Info("已添加 2PC 节点")
}

// RemoveNode 移除节点
func (t *TwoPCService) RemoveNode(addr string) {
	t.nodesMu.Lock()
	defer t.nodesMu.Unlock()

	newNodes := make([]string, 0, len(t.nodes))
	for _, node := range t.nodes {
		if node != addr {
			newNodes = append(newNodes, node)
		}
	}

	t.nodes = newNodes
	logging.WithField("node", addr).Info("已移除 2PC 节点")
}

// GetStats 获取统计信息
func (t *TwoPCService) GetStats() *TwoPCStats {
	return t.stats
}

// GetTransaction 获取事务状态（用于测试）
func (t *TwoPCService) GetTransaction(txID string) (*TransactionState, bool) {
	t.transactionsMu.RLock()
	defer t.transactionsMu.RUnlock()

	txState, exists := t.transactions[txID]
	return txState, exists
}
