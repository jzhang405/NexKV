package implementations

import (
	"fmt"
	"sync"
	"time"
)

// Phase 事务阶段
type Phase string

const (
	PhaseInit      Phase = "init"
	PhasePreCommit Phase = "precommit"
	PhaseCommit    Phase = "commit"
	PhaseDone      Phase = "done"
)

// Transaction 事务状态
type Transaction struct {
	ID           string
	Phase        Phase
	Participants []string
	Votes        map[string]bool // 参与者投票
	Decision     string          // 决策结果
}

// NewTransaction 创建新事务
func NewTransaction(id string, participants []string) *Transaction {
	return &Transaction{
		ID:           id,
		Phase:        PhaseInit,
		Participants: participants,
		Votes:        make(map[string]bool),
		Decision:     "",
	}
}

// TwoPhaseCommitNode 2PC 节点
type TwoPhaseCommitNode struct {
	ID           string
	Transactions map[string]*Transaction
	mu           sync.RWMutex
}

// NewTwoPhaseCommitNode 创建 2PC 节点
func NewTwoPhaseCommitNode(id string) *TwoPhaseCommitNode {
	return &TwoPhaseCommitNode{
		ID:           id,
		Transactions: make(map[string]*Transaction),
	}
}

// StartTransaction 协调者启动事务
func (n *TwoPhaseCommitNode) StartTransaction(txID string, participants []string) *Transaction {
	n.mu.Lock()
	defer n.mu.Unlock()

	tx := NewTransaction(txID, participants)
	n.Transactions[txID] = tx
	return tx
}

// PrePrepare 预准备阶段（协调者发送 Pre-Prepare）
func (n *TwoPhaseCommitNode) PrePrepare(tx *Transaction) {
	if tx.Phase != PhaseInit {
		return
	}
	tx.Phase = PhasePreCommit
}

// VoteRequest 参与者处理投票请求
func (n *TwoPhaseCommitNode) VoteRequest(tx *Transaction, vote bool) {
	if tx.Phase != PhasePreCommit {
		return
	}
	tx.Votes[n.ID] = vote
}

// GossipSync gossip 同步事务状态（简化版，避免死锁）
func (n *TwoPhaseCommitNode) GossipSync(other *TwoPhaseCommitNode) {
	n.mu.RLock()
	other.mu.Lock()
	defer n.mu.RUnlock()
	defer other.mu.Unlock()

	// 同步事务状态
	for txID, tx := range n.Transactions {
		if otherTx, ok := other.Transactions[txID]; ok {
			// 更新现有事务
			otherTx.Phase = tx.Phase
			otherTx.Decision = tx.Decision
		} else {
			// 创建新事务引用
			other.Transactions[txID] = tx
		}
	}
}

// Decide 协调者做出决策
func (n *TwoPhaseCommitNode) Decide(tx *Transaction) {
	yes, no := 0, 0
	for _, v := range tx.Votes {
		if v {
			yes++
		} else {
			no++
		}
	}
	allVoted := len(tx.Votes) == len(tx.Participants)

	if allVoted {
		if no == 0 {
			tx.Phase = PhaseCommit
			tx.Decision = "commit"
		} else {
			tx.Phase = PhaseDone
			tx.Decision = "abort"
		}
	}
}

// Ack 参与者确认决策
func (n *TwoPhaseCommitNode) Ack(tx *Transaction) {
	// 确认决策
}

// GetTransactionState 获取事务状态
func (n *TwoPhaseCommitNode) GetTransactionState(txID string) (Phase, string) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if tx, ok := n.Transactions[txID]; ok {
		return tx.Phase, tx.Decision
	}
	return PhaseInit, ""
}

// TwoPhaseCommitConfig 2PC 模拟配置
type TwoPhaseCommitConfig struct {
	NodeCount      int
	MaxRounds      int
	GossipInterval time.Duration
}

// DefaultTwoPhaseCommitConfig 默认配置
func DefaultTwoPhaseCommitConfig() *TwoPhaseCommitConfig {
	return &TwoPhaseCommitConfig{
		NodeCount:      3,
		MaxRounds:      20,
		GossipInterval: 10 * time.Millisecond,
	}
}

// RunTwoPhaseCommitSimulation 运行 2PC 模拟
func RunTwoPhaseCommitSimulation(config *TwoPhaseCommitConfig) *SimulationResult {
	if config == nil {
		config = DefaultTwoPhaseCommitConfig()
	}

	// 创建节点
	nodes := make([]*TwoPhaseCommitNode, config.NodeCount)
	for i := 0; i < config.NodeCount; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := make([]string, config.NodeCount)
	for i := 0; i < config.NodeCount; i++ {
		participants[i] = nodes[i].ID
	}

	// 协调者创建事务
	txID := "tx1"
	tx := nodes[0].StartTransaction(txID, participants)

	result := &SimulationResult{
		TotalRounds:    0,
		CommittedNodes: 0,
		Success:        false,
	}

	// 模拟 2PC
	for round := 0; round < config.MaxRounds; round++ {
		result.TotalRounds = round + 1

		// 阶段 1: 预准备
		if round == 0 {
			nodes[0].PrePrepare(tx)
		}

		// 阶段 2: 投票 - 所有参与者（包括协调者）都投票
		if round >= 1 && tx.Phase == PhasePreCommit {
			for i := 0; i < config.NodeCount; i++ {
				nodes[i].VoteRequest(tx, true) // 假设都投赞成票
			}
		}

		// 阶段 3: 决策
		if round >= 2 && tx.Phase == PhasePreCommit && len(tx.Votes) == len(tx.Participants) {
			nodes[0].Decide(tx)
		}

		// 阶段 4: 确认
		if tx.Phase == PhaseCommit || tx.Phase == PhaseDone {
			for i := 1; i < config.NodeCount; i++ {
				nodes[i].Ack(tx)
			}
		}

		// Gossip 同步
		for i := 0; i < config.NodeCount; i++ {
			for j := i + 1; j < config.NodeCount; j++ {
				nodes[i].GossipSync(nodes[j])
			}
		}

		// 检查是否完成
		phase, _ := nodes[0].GetTransactionState(txID)
		if phase == PhaseCommit || phase == PhaseDone {
			result.Success = true
			break
		}

		time.Sleep(config.GossipInterval)
	}

	return result
}

// TwoPhaseCommitNodeState 节点状态快照
type TwoPhaseCommitNodeState struct {
	ID        string
	Phase     Phase
	Decision  string
	VoteCount int
}
