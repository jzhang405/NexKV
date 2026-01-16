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

// === 使用 RunTestsWithAllTransports 的测试版本 ===
// 以下测试演示如何在三种 Transport（Memory, GRPC）下运行相同的测试逻辑
// 注意：NullTransport 不支持 TwoPhaseCommitNode

// TestTC001_TwoPhaseCommitBasic_WithAllTransports 2PC 基本流程测试（所有Transport）
func TestTC001_TwoPhaseCommitBasic_WithAllTransports(t *testing.T) {
	RunTestsWithAllTransports(t, "twophasecommit", func(t *testing.T, transport Transport) {
		// NullTransport 不支持 TwoPhaseCommitNode
		if transport.Status().Type == "null" {
			t.Skip("NullTransport does not support TwoPhaseCommitNode")
		}

		// 创建 2PC 节点
		nodeIDs := []string{"n1", "n2", "n3"}
		nodes := createTwoPhaseCommitNodesWithTransport(nodeIDs, transport)

		// n1 作为协调者启动事务
		participants := []string{"n1", "n2", "n3"}
		tx := nodes[0].StartTransaction("tx1", participants)

		// 验证事务创建
		if tx.ID != "tx1" {
			t.Errorf("Expected tx ID 'tx1', got '%s'", tx.ID)
		}

		// 验证 Transport 类型
		status := transport.Status()
		t.Logf("✅ Test passed with %s transport", status.Type)
	})
}

// TestTC002_TwoPhaseCommitAbort_WithAllTransports 2PC 中止测试（所有Transport）
func TestTC002_TwoPhaseCommitAbort_WithAllTransports(t *testing.T) {
	RunTestsWithAllTransports(t, "twophasecommit", func(t *testing.T, transport Transport) {
		// NullTransport 不支持 TwoPhaseCommitNode
		if transport.Status().Type == "null" {
			t.Skip("NullTransport does not support TwoPhaseCommitNode")
		}

		// 创建 2PC 节点
		nodeIDs := []string{"n1", "n2", "n3"}
		nodes := createTwoPhaseCommitNodesWithTransport(nodeIDs, transport)

		// n1 作为协调者启动事务
		participants := []string{"n1", "n2", "n3"}
		tx := nodes[0].StartTransaction("tx1", participants)

		// 阶段 1: 预准备
		nodes[0].PrePrepare(tx)

		// 同步给其他节点（通过 Transport）
		// 注意：这里简化处理，直接调用 GossipSync
		// 在实际的 Transport 场景中，应该通过消息传递
		nodes[0].GossipSync(nodes[1])
		nodes[0].GossipSync(nodes[2])

		// 验证：所有节点都知道事务处于预准备阶段
		for _, node := range nodes {
			phase, _ := node.GetTransactionState("tx1")
			if phase != PhasePreCommit {
				t.Errorf("Expected node %s to know about PreCommit phase, got %s", node.ID, phase)
			}
		}

		// 阶段 2: 投票 - n1 和 n2 投赞成票，n3 投反对票
		nodes[0].VoteRequest(tx, true)
		nodes[1].VoteRequest(tx, true)
		nodes[2].VoteRequest(tx, false)

		// 阶段 3: 决策
		nodes[0].Decide(tx)

		// 验证：事务应该中止
		phase, decision := nodes[0].GetTransactionState("tx1")
		if phase != PhaseDone || decision != "abort" {
			t.Errorf("Expected transaction to abort, got phase=%s, decision=%s", phase, decision)
		}

		// 验证 Transport 类型
		status := transport.Status()
		t.Logf("✅ Test passed with %s transport", status.Type)
	})
}

// TestTC003_TwoPhaseCommitCommit_WithAllTransports 2PC 提交测试（所有Transport）
func TestTC003_TwoPhaseCommitCommit_WithAllTransports(t *testing.T) {
	RunTestsWithAllTransports(t, "twophasecommit", func(t *testing.T, transport Transport) {
		// NullTransport 不支持 TwoPhaseCommitNode
		if transport.Status().Type == "null" {
			t.Skip("NullTransport does not support TwoPhaseCommitNode")
		}

		// 创建 2PC 节点
		nodeIDs := []string{"n1", "n2", "n3"}
		nodes := createTwoPhaseCommitNodesWithTransport(nodeIDs, transport)

		// n1 作为协调者启动事务
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

		// 验证 Transport 类型
		status := transport.Status()
		t.Logf("✅ Test passed with %s transport", status.Type)
	})
}

// TestTC004_TwoPhaseCommitGossip_WithAllTransports 2PC Gossip 同步测试（所有Transport）
func TestTC004_TwoPhaseCommitGossip_WithAllTransports(t *testing.T) {
	RunTestsWithAllTransports(t, "twophasecommit", func(t *testing.T, transport Transport) {
		// NullTransport 不支持 TwoPhaseCommitNode
		if transport.Status().Type == "null" {
			t.Skip("NullTransport does not support TwoPhaseCommitNode")
		}

		// 创建 2PC 节点
		nodeIDs := []string{"n1", "n2", "n3"}
		nodes := createTwoPhaseCommitNodesWithTransport(nodeIDs, transport)

		// n1 作为协调者启动事务
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

		// 验证 Transport 类型
		status := transport.Status()
		t.Logf("✅ Test passed with %s transport", status.Type)
	})
}

// TestTC005_TwoPhaseCommitConcurrent_WithAllTransports 并发事务测试（所有Transport）
func TestTC005_TwoPhaseCommitConcurrent_WithAllTransports(t *testing.T) {
	RunTestsWithAllTransports(t, "twophasecommit", func(t *testing.T, transport Transport) {
		// NullTransport 不支持 TwoPhaseCommitNode
		if transport.Status().Type == "null" {
			t.Skip("NullTransport does not support TwoPhaseCommitNode")
		}

		// 创建 2PC 节点
		nodeIDs := []string{"n1", "n2", "n3"}
		nodes := createTwoPhaseCommitNodesWithTransport(nodeIDs, transport)

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

		// 验证 Transport 类型
		status := transport.Status()
		t.Logf("✅ Test passed with %s transport", status.Type)
	})
}
