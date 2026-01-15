package implementations

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestTC001_SingleNodeProposeVote 测试场景：单一节点发起投票
// 对应 TLA+ 验证的 TC_001
func TestTC001_SingleNodeProposeVote(t *testing.T) {
	node := NewNode("n1")

	// 初始状态：未决策
	if node.Decision != Undecided {
		t.Errorf("Expected initial decision to be undecided, got %s", node.Decision)
	}

	// 发起投票
	if !node.ProposeVote(0) {
		t.Error("Expected ProposeVote to succeed")
	}

	// 验证：自己应该在 seen 集合中
	decision, version, seen, _ := node.GetState()
	if decision != Undecided {
		t.Errorf("Expected decision to still be undecided, got %s", decision)
	}
	if version != 0 {
		t.Errorf("Expected version to be 0, got %d", version)
	}
	if !seen["n1"] {
		t.Error("Expected n1 to be in seen set")
	}
}

// TestTC002_QuorumCommitSuccess 测试场景：Quorum 提交成功
// 对应 TLA+ 验证的 TC_002
func TestTC002_QuorumCommitSuccess(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()
	majority := cluster.GetMajority()

	// n1 和 n2 发起投票
	cluster.GetNode("n1").ProposeVote(0)
	cluster.GetNode("n2").ProposeVote(0)

	// Gossip 交换
	cluster.GossipRound()

	// n1 应该能够提交（seen = {n1, n2} >= 2）
	success, err := cluster.GetNode("n1").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n1 to be able to commit")
	}

	// 验证 n1 的状态
	decision, _, seen, decided := cluster.GetNode("n1").GetState()
	if decision != Committed {
		t.Errorf("Expected n1 decision to be committed, got %s", decision)
	}
	if len(seen) < majority {
		t.Errorf("Expected seen size >= %d, got %d", majority, len(seen))
	}
	if !decided["n1"] {
		t.Error("Expected n1 to be in decided set")
	}
}

// TestTC003_QuorumTimeoutRollback 测试场景：Quorum 超时回滚
// 对应 TLA+ 验证的 TC_003
func TestTC003_QuorumTimeoutRollback(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()
	majority := cluster.GetMajority()

	// 只有 n1 发起投票
	cluster.GetNode("n1").ProposeVote(0)

	// Gossip 交换
	cluster.GossipRound()

	// n1 不应该能够提交（seen = {n1} < 2）
	success, err := cluster.GetNode("n1").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if success {
		t.Error("Expected n1 to not be able to commit without quorum")
	}

	// 验证 n1 的状态仍然是 undecided
	decision, _, _, _ := cluster.GetNode("n1").GetState()
	if decision != Undecided {
		t.Errorf("Expected n1 decision to be undecided, got %s", decision)
	}
}

// TestTC010_ConcurrentVote 创建冲突测试
// 对应 TLA+ 验证的 TC_010
func TestTC010_ConcurrentVoteConflict(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()
	majority := cluster.GetMajority()

	var wg sync.WaitGroup

	// 并发发起投票
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()
			cluster.GetNode(nodeID).ProposeVote(0)
		}(fmt.Sprintf("n%d", i))
	}
	wg.Wait()

	// 验证：所有节点都应该在 seen 集合中
	cluster.GossipRound()

	n1Decision, _, n1Seen, _ := cluster.GetNode("n1").GetState()
	if n1Decision != Undecided {
		t.Errorf("Expected n1 decision to be undecided, got %s", n1Decision)
	}
	if len(n1Seen) < 2 {
		t.Errorf("Expected n1 seen size >= %d after gossip, got %d", majority, len(n1Seen))
	}
}

// TestTC025_PartitionSafety 网络分区安全性测试
// 对应 TLA+ 验证的 TC_025
func TestTC025_PartitionSafety(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()
	majority := cluster.GetMajority()

	// 模拟网络分区：n1,n2 在一个分区，n3,n4,n5 在另一个分区
	partition1 := []*Node{cluster.GetNode("n1"), cluster.GetNode("n2")}
	partition2 := []*Node{cluster.GetNode("n3"), cluster.GetNode("n4"), cluster.GetNode("n5")}

	// 分区1内的节点发起投票
	partition1[0].ProposeVote(0)
	partition1[1].ProposeVote(0)
	_ = partition1[0].GossipExchange(partition1[1])

	// 分区2内的节点发起投票
	partition2[0].ProposeVote(0)
	partition2[1].ProposeVote(0)
	partition2[2].ProposeVote(0)
	_ = partition2[0].GossipExchange(partition2[1])
	_ = partition2[0].GossipExchange(partition2[2])
	_ = partition2[1].GossipExchange(partition2[2])

	// 分区1尝试决策：n1 和 n2 看到 2 个节点，但需要 3 个
	// 应该无法决策（因为无法达到多数派）
	success, err := partition1[0].DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if success {
		t.Error("Expected partition1 not to reach quorum")
	}

	// 分区2尝试决策：n3, n4, n5 看到 3 个节点 >= 3
	// 可以决策
	success, err = partition2[0].DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected partition2 to reach quorum")
	}

	// 验证：分区1的节点仍然未决策
	decision, _, _, _ := partition1[0].GetState()
	if decision != Undecided {
		t.Error("Expected partition1 nodes to remain undecided")
	}
}

// TestTC030_GossipConvergence Gossip 收敛性测试
// 对应 TLA+ 验证的 TC_030
func TestTC030_GossipConvergence(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()
	majority := cluster.GetMajority()

	// n1 和 n2 发起投票（5节点需要3个才能提交）
	cluster.GetNode("n1").ProposeVote(0)
	cluster.GetNode("n2").ProposeVote(0)

	// 多轮 gossip 后，所有节点应该知道 n1 和 n2 投票了
	for round := 0; round < 10; round++ {
		cluster.GossipRound()
	}

	// 验证：所有节点都知道 n1 和 n2 投票了
	for _, node := range cluster.Nodes {
		_, _, seen, _ := node.GetState()
		if !seen["n1"] || !seen["n2"] {
			t.Errorf("Expected node %s to know about n1 and n2's votes", node.ID)
		}
	}

	// n1 不应该能够提交（只有2个投票，需要3个）
	success, err := cluster.GetNode("n1").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if success {
		t.Error("Expected n1 to not be able to commit with only 2 votes")
	}

	// n3 也发起投票
	cluster.GetNode("n3").ProposeVote(0)

	// 更多轮 gossip
	for round := 0; round < 10; round++ {
		cluster.GossipRound()
	}

	// n1 现在应该能够提交（看到3个投票）
	success, err = cluster.GetNode("n1").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n1 to be able to commit with 3 votes")
	}
}

// TestTC035_NodeRecovery 节点故障恢复测试
// 对应 TLA+ 验证的 TC_035
func TestTC035_NodeRecovery(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()
	majority := cluster.GetMajority()

	// n1, n2, n3 都发起投票
	cluster.GetNode("n1").ProposeVote(0)
	cluster.GetNode("n2").ProposeVote(0)
	cluster.GetNode("n3").ProposeVote(0)

	// Gossip
	cluster.GossipRound()

	// n1 决策
	success, err := cluster.GetNode("n1").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n1 to commit")
	}

	// 验证：n1 已提交
	decision, _, _, _ := cluster.GetNode("n1").GetState()
	if decision != Committed {
		t.Errorf("Expected n1 decision to be committed, got %s", decision)
	}
}

// TestDecisionSafety 决策安全性测试
// 验证：所有 committed 节点的 seen 集合都 >= majority
func TestDecisionSafety(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()
	majority := cluster.GetMajority()

	// 所有节点发起投票
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}

	// 多轮 gossip
	for round := 0; round < 5; round++ {
		cluster.GossipRound()
	}

	// 所有节点都应该能够提交
	for _, node := range cluster.Nodes {
		success, err := node.DecideCommit(majority)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !success {
			t.Errorf("Expected node %s to be able to commit", node.ID)
		}
	}

	// 验证决策安全性
	for _, node := range cluster.Nodes {
		decision, _, seen, _ := node.GetState()
		if decision == Committed && len(seen) < majority {
			t.Errorf("DecisionSafety violated: node %s committed with seen=%d < majority=%d",
				node.ID, len(seen), majority)
		}
	}
}

// TestVersionConsistency 版本一致性测试
// 验证：所有节点的 knowledge 版本一致
func TestVersionConsistency(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 初始版本都是 0
	versions := make(map[int]int)
	for _, node := range cluster.Nodes {
		_, version, _, _ := node.GetState()
		versions[version]++
	}
	if versions[0] != 3 {
		t.Errorf("Expected all nodes to have version 0, got %v", versions)
	}

	// n1 发起投票（版本 0）
	cluster.GetNode("n1").ProposeVote(0)

	// gossip 后版本仍然一致（因为没有改变版本）
	for round := 0; round < 5; round++ {
		cluster.GossipRound()
	}

	// 验证版本一致性
	for _, node := range cluster.Nodes {
		_, version, _, _ := node.GetState()
		if version != 0 {
			t.Errorf("Expected node %s version to be 0, got %d", node.ID, version)
		}
	}
}

// TestSimulationRun 运行完整模拟
func TestSimulationRun(t *testing.T) {
	config := &SimulationConfig{
		NodeCount:      3,
		MaxRounds:      20,
		GossipInterval: 10 * time.Millisecond,
	}

	result := RunSimulation(config)

	if !result.Success {
		t.Errorf("Expected simulation to succeed, got %+v", result)
	}

	if result.CommittedNodes != config.NodeCount {
		t.Errorf("Expected %d committed nodes, got %d", config.NodeCount, result.CommittedNodes)
	}

	t.Logf("Simulation completed in %d rounds", result.TotalRounds)
}

// TestMajorityCalculation 多数派计算测试
func TestMajorityCalculation(t *testing.T) {
	testCases := []struct {
		nodeCount int
		expected  int
	}{
		{3, 2},
		{5, 3},
		{7, 4},
	}

	for _, tc := range testCases {
		tempDir := setupTempDir(t)
		defer os.RemoveAll(tempDir)

		nodeIDs := make([]string, tc.nodeCount)
		for i := 0; i < tc.nodeCount; i++ {
			nodeIDs[i] = fmt.Sprintf("n%d", i+1)
		}
		cluster := NewCluster(nodeIDs, tempDir)
		cluster.Close()
		majority := cluster.GetMajority()
		if majority != tc.expected {
			t.Errorf("Expected majority %d for %d nodes, got %d",
				tc.expected, tc.nodeCount, majority)
		}
	}
}

// TestFollowDecision 测试跟随决策功能
func TestFollowDecision(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// n1 提交决策
	majority := cluster.GetMajority()
	cluster.GetNode("n1").ProposeVote(0)
	cluster.GetNode("n2").ProposeVote(0)
	cluster.GossipRound()

	success, err := cluster.GetNode("n1").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Fatal("Expected n1 to commit")
	}

	// 再次 gossip，让 n2 知道 n1 已提交
	cluster.GossipRound()

	// n2 知道 n1 已提交，应该跟随决策
	if !cluster.GetNode("n2").FollowDecision(majority) {
		t.Error("Expected n2 to follow n1's decision")
	}

	// 验证 n2 的状态
	decision, _, _, _ := cluster.GetNode("n2").GetState()
	if decision != Committed {
		t.Errorf("Expected n2 decision to be committed, got %s", decision)
	}
}

// TestFollowDecisionCannotFollowWithoutVote 测试未投票节点不能跟随决策
func TestFollowDecisionCannotFollowWithoutVote(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	// n1 和 n2 提交决策
	cluster.GetNode("n1").ProposeVote(0)
	cluster.GetNode("n2").ProposeVote(0)
	cluster.GossipRound()

	success, err := cluster.GetNode("n1").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Fatal("Expected n1 to commit")
	}

	// n3 未发起投票，不能跟随决策
	if cluster.GetNode("n3").FollowDecision(majority) {
		t.Error("Expected n3 to not follow decision without voting")
	}
}

// TestFollowDecisionAlreadyCommitted 测试已提交节点不能再次跟随决策
func TestFollowDecisionAlreadyCommitted(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	// n1 和 n2 都提交决策
	cluster.GetNode("n1").ProposeVote(0)
	cluster.GetNode("n2").ProposeVote(0)
	cluster.GossipRound()

	success, err := cluster.GetNode("n1").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Fatal("Expected n1 to commit")
	}

	success, err = cluster.GetNode("n2").DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Fatal("Expected n2 to commit")
	}

	// n1 已提交，不能再次跟随决策
	if cluster.GetNode("n1").FollowDecision(majority) {
		t.Error("Expected already committed node to not follow again")
	}
}

// TestPrintState 测试 PrintState 功能
func TestPrintState(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2"}, tempDir)
	defer cluster.Close()

	// 调用 PrintState，确保不会 panic
	cluster.PrintState()
}
