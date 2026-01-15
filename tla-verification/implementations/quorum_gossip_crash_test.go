package implementations

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 辅助函数：创建临时测试目录
func setupTempDir(t *testing.T) string {
	dir := filepath.Join(os.TempDir(), "nexkv-test", t.Name())
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("Failed to cleanup temp dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	return dir
}

// TestTC036_SingleNodeCrash 测试单个节点崩溃恢复
// 对应 TLA+ 验证场景：CrashRecovery
func TestTC036_SingleNodeCrash(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	// 1. 创建 3 节点集群
	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 2. 所有节点发起投票
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}

	// 3. 多轮 Gossip 达到决策
	for round := 0; round < 5; round++ {
		cluster.GossipRound()
	}

	// 4. 所有节点提交
	majority := cluster.GetMajority()
	for _, node := range cluster.Nodes {
		success, err := node.DecideCommit(majority)
		if err != nil {
			t.Fatalf("Failed to commit: %v", err)
		}
		if !success {
			t.Errorf("Expected node %s to commit", node.ID)
		}
	}

	// 5. n1 崩溃
	err := cluster.Nodes[0].Crash()
	if err != nil {
		t.Fatalf("Failed to crash n1: %v", err)
	}

	// 验证：n1 被标记为崩溃
	if !cluster.Nodes[0].IsCrashed {
		t.Error("Expected n1 to be marked as crashed")
	}

	// 验证：n1 不能决策
	_, err = cluster.Nodes[0].DecideCommit(majority)
	if err == nil {
		t.Error("Expected crashed node to fail decision")
	}

	// 6. n1 恢复
	err = cluster.Nodes[0].Recover(cluster)
	if err != nil {
		t.Fatalf("Failed to recover n1: %v", err)
	}

	// 等待增量同步完成
	time.Sleep(3 * time.Second)

	// 验证：n1 恢复正常状态
	if cluster.Nodes[0].IsCrashed {
		t.Error("Expected n1 to be recovered")
	}

	// 验证：n1 的决策状态与其他节点一致
	for _, node := range cluster.Nodes[1:] {
		if cluster.Nodes[0].Decision != node.Decision {
			t.Errorf("Expected n1 decision=%s, got %s",
				node.Decision, cluster.Nodes[0].Decision)
		}
	}
}

// TestTC037_MajorityCrash 测试多数派崩溃
// 对应 TLA+ 性质：ActiveNodesCanDecide
func TestTC037_MajorityCrash(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	// 1. 创建 5 节点集群
	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	// 2. 多数派（3个节点）崩溃
	for i := 0; i < 3; i++ {
		err := cluster.Nodes[i].Crash()
		if err != nil {
			t.Fatalf("Failed to crash node %s: %v", cluster.Nodes[i].ID, err)
		}
	}

	// 3. 剩余少数派尝试决策
	majority := cluster.GetMajority()

	for i := 3; i < 5; i++ {
		// 发起投票
		cluster.Nodes[i].ProposeVote(0)

		// 尝试决策（应该失败）
		success, err := cluster.Nodes[i].DecideCommit(majority)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if success {
			t.Errorf("Expected minority node %s to fail decision", cluster.Nodes[i].ID)
		}
	}
}

// TestTC038_CrashDuringGossip 测试 Gossip 期间崩溃
func TestTC038_CrashDuringGossip(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 1. 所有节点发起投票
	for _, node := range cluster.Nodes {
		node.ProposeVote(0)
	}

	// 2. 第一轮 Gossip
	cluster.GossipRound()

	// 3. n1 崩溃
	err := cluster.Nodes[0].Crash()
	if err != nil {
		t.Fatalf("Failed to crash n1: %v", err)
	}

	// 4. 尝试与崩溃节点 Gossip（应该失败）
	err = cluster.Nodes[1].GossipExchange(cluster.Nodes[0], cluster)
	if err == nil {
		t.Error("Expected gossip with crashed node to fail")
	}

	// 5. n2, n3 继续 Gossip（应该成功）
	err = cluster.Nodes[1].GossipExchange(cluster.Nodes[2], cluster)
	if err != nil {
		t.Errorf("Expected gossip between healthy nodes to succeed: %v", err)
	}
}

// TestTC039_CrashRecoveryIdempotent 测试崩溃恢复幂等性
func TestTC039_CrashRecoveryIdempotent(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1"}, tempDir)
	defer cluster.Close()

	node := cluster.Nodes[0]

	// 1. 重复崩溃（应该不报错）
	err := node.Crash()
	if err != nil {
		t.Fatalf("First crash failed: %v", err)
	}

	err = node.Crash()
	if err != nil {
		t.Errorf("Second crash should be idempotent: %v", err)
	}

	// 2. 恢复
	err = node.Recover(cluster)
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	// 3. 重复恢复（应该不报错）
	err = node.Recover(cluster)
	if err == nil {
		t.Error("Expected error when recovering non-crashed node")
	}
}

// TestTC040_WALPersistence 测试 WAL 持久化
func TestTC040_WALPersistence(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1"}, tempDir)

	// 1. 节点发起投票并提交
	node := cluster.Nodes[0]
	node.ProposeVote(0)
	_, err := node.DecideCommit(1)
	if err != nil {
		t.Fatalf("DecideCommit failed: %v", err)
	}

	// 2. 崩溃（会持久化到 WAL）
	err = node.Crash()
	if err != nil {
		t.Fatalf("Crash failed: %v", err)
	}

	// 关闭 WAL
	if node.WAL != nil {
		node.WAL.Close()
	}

	// 3. 模拟进程重启：重新加载 WAL
	newCluster := NewCluster([]string{"n1"}, tempDir)
	defer newCluster.Close()

	recoveredNode := newCluster.Nodes[0]

	// 4. 从 WAL 恢复状态（进程重启场景）
	err = recoveredNode.RecoverFromWAL(newCluster)
	if err != nil {
		t.Fatalf("RecoverFromWAL failed: %v", err)
	}

	// 5. 验证恢复的状态
	if recoveredNode.Decision != Committed {
		t.Errorf("Expected decision=%s, got %s", Committed, recoveredNode.Decision)
	}

	if !recoveredNode.Knowledge.Seen["n1"] {
		t.Error("Expected n1 to be in seen set")
	}
}

// TestTC041_ProposeVoteVersionMismatch 测试版本不匹配场景
func TestTC041_ProposeVoteVersionMismatch(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1"}, tempDir)
	defer cluster.Close()

	node := cluster.Nodes[0]

	// 发起版本 0 的投票
	if !node.ProposeVote(0) {
		t.Error("Expected ProposeVote version 0 to succeed")
	}

	// 尝试发起版本 1 的投票（应该失败，节点已经在版本 0）
	if node.ProposeVote(1) {
		t.Error("Expected ProposeVote version 1 to fail")
	}
}

// TestTC042_DecideCommitWithoutVote 测试未投票就决策的场景
func TestTC042_DecideCommitWithoutVote(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()
	node := cluster.Nodes[0]

	// 节点未发起投票就直接决策（应该失败）
	success, err := node.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if success {
		t.Error("Expected DecideCommit to fail without voting")
	}
}

// TestTC043_DecideCommitInsufficientVotes 测试投票数不足场景
func TestTC043_DecideCommitInsufficientVotes(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()
	node := cluster.Nodes[0]

	// 节点发起投票
	node.ProposeVote(0)

	// 只有一个投票，未达到多数派（应该失败）
	success, err := node.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if success {
		t.Error("Expected DecideCommit to fail with insufficient votes")
	}
}

// TestTC044_GossipVersionMismatch 测试版本不匹配的 Gossip
func TestTC044_GossipVersionMismatch(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2"}, tempDir)
	defer cluster.Close()

	n1 := cluster.Nodes[0]
	n2 := cluster.Nodes[1]

	// n1 在版本 0 发起投票
	n1.ProposeVote(0)

	// n2 在版本 1（模拟版本升级）
	n2.Knowledge.Version = 1
	n2.ProposeVote(1)

	// Gossip 交换（版本不匹配，应该不交换）
	err := n1.GossipExchange(n2, cluster)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// 验证：n1 不应该知道 n2 的投票
	if n1.Knowledge.Seen["n2"] {
		t.Error("Expected n1 to not know about n2's vote due to version mismatch")
	}
}

// TestTC045_CrashedNodeDecisions 测试崩溃节点不能决策
func TestTC045_CrashedNodeDecisions(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()
	node := cluster.Nodes[0]

	// 节点崩溃
	err := node.Crash()
	if err != nil {
		t.Fatalf("Crash failed: %v", err)
	}

	// 崩溃节点尝试决策（应该失败）
	_, err = node.DecideCommit(majority)
	if err == nil {
		t.Error("Expected crashed node to fail decision")
	}
}

// TestTC046_WALErrors 测试 WAL 错误处理
func TestTC046_WALErrors(t *testing.T) {
	// 1. 测试 WAL 在无效路径下创建失败
	invalidDir := "/root/nonexistent/no-permission"
	_, err := NewWAL(invalidDir)
	if err == nil {
		t.Error("Expected WAL creation to fail with invalid path")
	}

	// 2. 测试 WAL 在空目录下正常创建
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	wal, err := NewWAL(tempDir)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// 3. 测试 Append 正常工作
	entry := WALEntry{
		Timestamp: time.Now(),
		NodeID:    "test-node",
		Knowledge: Knowledge{
			Seen:    map[string]bool{"n1": true},
			Version: 0,
			Decided: map[string]bool{},
		},
		Decision: Committed,
		Version:  0,
	}

	err = wal.Append(entry)
	if err != nil {
		t.Errorf("Failed to append WAL entry: %v", err)
	}

	// 4. 测试 Recover 正常读取
	entries, err := wal.Recover()
	if err != nil {
		t.Errorf("Failed to recover from WAL: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("Expected 1 entry, got %d", len(entries))
	}
}

// TestTC047_ProposeVoteEdgeCases 测试 ProposeVote 边界情况
func TestTC047_ProposeVoteEdgeCases(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1"}, tempDir)
	defer cluster.Close()

	node := cluster.Nodes[0]

	// 1. 已决策节点不能再投票
	node.Decision = Committed
	if node.ProposeVote(0) {
		t.Error("Expected ProposeVote to fail when already committed")
	}

	// 2. 版本不匹配时不能投票
	node.Decision = Undecided
	if !node.ProposeVote(0) {
		t.Error("Expected ProposeVote version 0 to succeed")
	}

	// 再次发起版本0的投票（幂等性）
	if !node.ProposeVote(0) {
		t.Error("Expected ProposeVote version 0 to be idempotent")
	}

	// 尝试发起版本1的投票（应该失败，节点在版本0）
	if node.ProposeVote(1) {
		t.Error("Expected ProposeVote version 1 to fail when node is at version 0")
	}
}

// TestTC048_DecideCommitWALFailure 测试 DecideCommit WAL 持久化失败场景
func TestTC048_DecideCommitWALFailure(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2"}, tempDir)
	defer cluster.Close()

	node := cluster.Nodes[0]
	majority := cluster.GetMajority()

	// 节点发起投票
	node.ProposeVote(0)
	cluster.Nodes[1].ProposeVote(0)
	cluster.GossipRound()

	// 关闭 WAL（模拟 WAL 故障）
	if node.WAL != nil {
		node.WAL.Close()
		node.WAL = nil // 移除 WAL 引用
	}

	// DecideCommit 应该仍然成功（即使 WAL 为 nil）
	success, err := node.DecideCommit(majority)
	if err != nil {
		t.Errorf("Expected DecideCommit to succeed without WAL: %v", err)
	}
	if !success {
		t.Error("Expected DecideCommit to succeed")
	}
}

// TestTC049_ClusterHelperFunctions 测试 Cluster 辅助函数
func TestTC049_ClusterHelperFunctions(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2"}, tempDir)
	defer cluster.Close()

	// 测试 GetNode 找到存在的节点
	node := cluster.GetNode("n1")
	if node == nil {
		t.Error("Expected to find node n1")
	}
	if node.ID != "n1" {
		t.Errorf("Expected node ID to be n1, got %s", node.ID)
	}

	// 测试 GetNode 未找到不存在的节点
	nonExistent := cluster.GetNode("n999")
	if nonExistent != nil {
		t.Error("Expected GetNode to return nil for non-existent node")
	}

	// 测试 GetMajority 计算正确
	majority := cluster.GetMajority()
	expectedMajority := len(cluster.Nodes)/2 + 1
	if majority != expectedMajority {
		t.Errorf("Expected majority %d, got %d", expectedMajority, majority)
	}

	// 测试 Close 正确关闭所有 WAL
	err := cluster.Close()
	if err != nil {
		t.Errorf("Failed to close cluster: %v", err)
	}

	// 重复 Close 应该幂等（但可能返回错误，因为 WAL 已经关闭）
	err = cluster.Close()
	// 这里不强制要求成功，只要不 panic 就行
	_ = err
}

// TestTC050_GossipExchangeAlreadyCrashed 测试 GossipExchange 崩溃节点边界情况
func TestTC050_GossipExchangeAlreadyCrashed(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2"}, tempDir)
	defer cluster.Close()

	n1 := cluster.Nodes[0]
	n2 := cluster.Nodes[1]

	// n1 崩溃
	err := n1.Crash()
	if err != nil {
		t.Fatalf("Failed to crash n1: %v", err)
	}

	// 测试崩溃节点发起 gossip
	err = n1.GossipExchange(n2, cluster)
	if err == nil {
		t.Error("Expected gossip from crashed node to fail")
	}

	// 测试与崩溃节点 gossip
	err = n2.GossipExchange(n1, cluster)
	if err == nil {
		t.Error("Expected gossip to crashed node to fail")
	}
}

