package implementations

import (
	"fmt"
	"testing"
	"time"
)

// TestTC001_TwoPhaseCommitBasic 2PC 基本流程测试
func TestTC001_TwoPhaseCommitBasic(t *testing.T) {
	config := &TwoPhaseCommitConfig{
		NodeCount:      3,
		MaxRounds:      10,
		GossipInterval: 10 * time.Millisecond,
	}

	result := RunTwoPhaseCommitSimulation(config)

	if !result.Success {
		t.Errorf("Expected 2PC simulation to succeed, got %+v", result)
	}

	t.Logf("2PC simulation completed in %d rounds", result.TotalRounds)
}

// TestTC002_TwoPhaseCommitAbort 2PC 中止测试
func TestTC002_TwoPhaseCommitAbort(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 阶段 1: 预准备
	nodes[0].PrePrepare(tx)

	// 同步给其他节点
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	// 验证：所有节点都知道事务处于预准备阶段
	for _, node := range nodes {
		phase, _ := node.GetTransactionState("tx1")
		if phase != PhasePreCommit {
			t.Errorf("Expected node %s to know about PreCommit phase, got %s", node.ID, phase)
		}
	}

	// 阶段 2: 投票 - n1 和 n2 投赞成票
	nodes[0].VoteRequest(tx, true)
	nodes[1].VoteRequest(tx, true)
	nodes[2].VoteRequest(tx, false) // n3 投反对票

	// 阶段 3: 决策
	nodes[0].Decide(tx)

	// 验证：事务应该中止
	phase, decision := nodes[0].GetTransactionState("tx1")
	if phase != PhaseDone || decision != "abort" {
		t.Errorf("Expected transaction to abort, got phase=%s, decision=%s", phase, decision)
	}
}

// TestTC003_TwoPhaseCommitCommit 2PC 提交测试
func TestTC003_TwoPhaseCommitCommit(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 阶段 1: 预准备
	nodes[0].PrePrepare(tx)

	// 同步给其他节点
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	// 阶段 2: 所有参与者投赞成票
	nodes[0].VoteRequest(tx, true)
	nodes[1].VoteRequest(tx, true)
	nodes[2].VoteRequest(tx, true)

	// 阶段 3: 决策
	nodes[0].Decide(tx)

	// 验证：事务应该提交
	phase, decision := nodes[0].GetTransactionState("tx1")
	if phase != PhaseCommit || decision != "commit" {
		t.Errorf("Expected transaction to commit, got phase=%s, decision=%s", phase, decision)
	}
}

// TestTC004_TwoPhaseCommitGossip 2PC Gossip 同步测试
func TestTC004_TwoPhaseCommitGossip(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 协调者预准备
	nodes[0].PrePrepare(tx)

	// Gossip 同步
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	// 验证：所有节点都知道事务状态
	phase1, _ := nodes[1].GetTransactionState("tx1")
	phase2, _ := nodes[2].GetTransactionState("tx1")

	if phase1 != PhasePreCommit {
		t.Errorf("Expected n1 to know about PreCommit phase, got %s", phase1)
	}
	if phase2 != PhasePreCommit {
		t.Errorf("Expected n2 to know about PreCommit phase, got %s", phase2)
	}
}

// TestTC005_TwoPhaseCommitConcurrent 并发事务测试
func TestTC005_TwoPhaseCommitConcurrent(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	// 并发创建两个事务
	tx1 := nodes[0].StartTransaction("tx1", []string{"n1", "n2", "n3"})
	tx2 := nodes[1].StartTransaction("tx2", []string{"n1", "n2", "n3"})

	// 两个事务都进入预准备阶段
	nodes[0].PrePrepare(tx1)
	nodes[1].PrePrepare(tx2)

	// Gossip 同步
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])
	nodes[1].GossipSync(nodes[2])

	// 验证：节点知道两个事务
	phase1, _ := nodes[2].GetTransactionState("tx1")
	phase2, _ := nodes[2].GetTransactionState("tx2")

	if phase1 != PhasePreCommit {
		t.Errorf("Expected node to know about tx1 PreCommit, got %s", phase1)
	}
	if phase2 != PhasePreCommit {
		t.Errorf("Expected node to know about tx2 PreCommit, got %s", phase2)
	}
}
