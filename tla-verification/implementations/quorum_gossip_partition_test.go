package implementations

import (
	"os"
	"testing"
	"time"
)

// TestTC041_Partition3vs2 测试 3 vs 2 网络分区
// 对应 TLA+ 验证场景：PartitionSafety, MinorityCannotDecide
// 验证目标：
// 1. 多数派分区（3节点）可以决策
// 2. 少数派分区（2节点）无法决策
// 3. 不会出现脑裂（两个分区决策不同的值）
func TestTC041_Partition3vs2(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	// 1. 创建 5 节点集群
	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	// 2. 立即创建分区（在 Gossip 之前）
	err := cluster.CreatePartition(
		[]string{"n1", "n2", "n3"}, // 多数派分区
		[]string{"n4", "n5"},       // 少数派分区
	)
	if err != nil {
		t.Fatalf("Failed to create partition: %v", err)
	}

	// 验证：集群处于分区状态
	if cluster.NetworkStatus != "partitioned" {
		t.Errorf("Expected network status to be 'partitioned', got '%s'", cluster.NetworkStatus)
	}

	// 3. 多数派分区内的节点发起投票
	for _, nodeID := range []string{"n1", "n2", "n3"} {
		cluster.GetNode(nodeID).ProposeVote(0)
	}

	// 4. 多数派分区内的 Gossip（只在分区内）
	n1 := cluster.GetNode("n1")
	n2 := cluster.GetNode("n2")
	n3 := cluster.GetNode("n3")

	_ = n1.GossipExchange(n2, cluster)
	_ = n1.GossipExchange(n3, cluster)
	_ = n2.GossipExchange(n3, cluster)

	// 5. 多数派分区内的节点可以决策
	for _, nodeID := range []string{"n1", "n2", "n3"} {
		node := cluster.GetNode(nodeID)
		success, err := node.DecideCommit(majority)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !success {
			t.Errorf("Expected majority node %s to be able to commit", nodeID)
		}
	}

	// 6. 少数派分区内的节点发起投票
	n4 := cluster.GetNode("n4")
	n5 := cluster.GetNode("n5")
	n4.ProposeVote(0)
	n5.ProposeVote(0)

	// 7. 少数派分区内的 Gossip（只在分区内）
	_ = n4.GossipExchange(n5, cluster)

	// 8. 少数派分区内的节点无法决策（只有2个投票，需要3个）
	for _, nodeID := range []string{"n4", "n5"} {
		node := cluster.GetNode(nodeID)
		success, err := node.DecideCommit(majority)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if success {
			t.Errorf("Expected minority node %s to fail commit", nodeID)
		}
	}

	// 9. 验证多数派节点已决策
	n1Decision, _, _, _ := n1.GetState()
	if n1Decision != Committed {
		t.Errorf("Expected majority node n1 to be committed, got %s", n1Decision)
	}

	// 10. 验证少数派节点未决策
	n4Decision, _, _, _ := n4.GetState()
	if n4Decision != Undecided {
		t.Errorf("Expected minority node n4 to be undecided, got %s", n4Decision)
	}
}

// TestTC042_PartitionHealing 测试分区恢复
// 对应 TLA+ 验证场景：PartitionRecovery
// 验证目标：
// 1. 分区恢复后，网络状态恢复正常
// 2. 触发全局 gossip 后，所有节点状态一致
// 3. 未决策节点可以跟随已决策节点
func TestTC042_PartitionHealing(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	// 1. 创建 5 节点集群
	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	// 2. 多数派节点发起投票
	for _, nodeID := range []string{"n1", "n2", "n3"} {
		cluster.GetNode(nodeID).ProposeVote(0)
	}

	// 3. 多轮 Gossip
	for round := 0; round < 5; round++ {
		cluster.GossipRound()
	}

	// 4. 创建分区
	err := cluster.CreatePartition(
		[]string{"n1", "n2", "n3"},
		[]string{"n4", "n5"},
	)
	if err != nil {
		t.Fatalf("Failed to create partition: %v", err)
	}

	// 5. 多数派节点决策
	n1 := cluster.GetNode("n1")
	success, err := n1.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Fatal("Expected majority node to commit")
	}

	// 6. 恢复分区（不触发自动 gossip）
	err = cluster.HealPartitionNoAutoGossip()
	if err != nil {
		t.Fatalf("Failed to heal partition: %v", err)
	}

	// 验证：网络状态恢复正常
	if cluster.NetworkStatus != "normal" {
		t.Errorf("Expected network status to be 'normal', got '%s'", cluster.NetworkStatus)
	}

	// 7. 手动触发 Gossip，让所有节点同步
	for round := 0; round < 3; round++ {
		cluster.GossipRound()
	}

	// 8. 验证：所有节点都知道 n1 已决策
	for _, node := range cluster.Nodes {
		_, _, _, decided := node.GetState()
		if !decided["n1"] {
			t.Errorf("Expected node %s to know n1 is decided", node.ID)
		}
	}

	// 9. n4, n5 跟随决策
	n4 := cluster.GetNode("n4")
	n4.ProposeVote(0)
	success = n4.FollowDecision(majority)
	if !success {
		t.Error("Expected n4 to follow decision after healing")
	}

	n4Decision, _, _, _ := n4.GetState()
	if n4Decision != Committed {
		t.Errorf("Expected n4 to be committed, got %s", n4Decision)
	}
}

// TestTC043_CrossPartitionGossipBlocked 测试跨分区 Gossip 被阻塞
// 验证目标：不同分区的节点不能通过 Gossip 交换信息
func TestTC043_CrossPartitionGossipBlocked(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	// 1. 创建分区：n1,n2 vs n3,n4,n5
	err := cluster.CreatePartition(
		[]string{"n1", "n2"},
		[]string{"n3", "n4", "n5"},
	)
	if err != nil {
		t.Fatalf("Failed to create partition: %v", err)
	}

	// 验证：分区映射正确
	t.Logf("PartitionMap: n1=%s, n2=%s, n3=%s",
		cluster.PartitionMap["n1"],
		cluster.PartitionMap["n2"],
		cluster.PartitionMap["n3"])

	// 2. n1 发起投票
	n1 := cluster.GetNode("n1")
	n1.ProposeVote(0)

	// 3. n2 发起投票（同分区）
	n2 := cluster.GetNode("n2")
	n2.ProposeVote(0)

	// 4. n3 发起投票（不同分区）
	n3 := cluster.GetNode("n3")
	n3.ProposeVote(0)

	// 5. 尝试跨分区 Gossip：n1 -> n3（应该失败）
	err = n1.GossipExchange(n3, cluster)
	if err == nil {
		t.Error("Expected cross-partition gossip to fail")
	}
	t.Logf("Cross-partition gossip error: %v", err)

	// 6. 验证：n1 不知道 n3 的投票
	_, _, seen, _ := n1.GetState()
	if seen["n3"] {
		t.Error("Expected n1 to not know about n3's vote due to partition")
	}

	// 7. 同分区内的 Gossip 应该成功：n1 -> n2
	err = n1.GossipExchange(n2, cluster)
	if err != nil {
		t.Errorf("Expected same-partition gossip to succeed: %v", err)
	}

	// 8. 验证：n1 知道 n2 的投票
	_, _, seen, _ = n1.GetState()
	if !seen["n1"] {
		t.Error("Expected n1 to know about its own vote")
	}
	if !seen["n2"] {
		t.Error("Expected n1 to know about n2's vote after same-partition gossip")
	}
	if seen["n3"] {
		t.Error("Expected n1 to not know about n3's vote (different partition)")
	}
}

// TestTC044_AutoPartitionDetection 测试自动分区检测
// 验证目标：通过心跳超时自动检测网络分区
func TestTC044_AutoPartitionDetection(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3"}, tempDir)
	defer cluster.Close()

	// 1. 模拟 n3 心跳超时
	cluster.mu.Lock()
	cluster.HeartbeatMap["n3"] = time.Now().Add(-10 * time.Second) // 10 秒前
	cluster.mu.Unlock()

	// 2. 运行分区检测
	err := cluster.DetectPartition()
	if err != nil {
		t.Fatalf("Failed to detect partition: %v", err)
	}

	// 3. 验证：网络状态变为分区
	if cluster.NetworkStatus != "partitioned" {
		t.Errorf("Expected network status to be 'partitioned', got '%s'", cluster.NetworkStatus)
	}

	// 4. 验证：n3 被标记为少数派
	if cluster.PartitionMap["n3"] != "minority" {
		t.Errorf("Expected n3 to be in minority partition, got '%s'", cluster.PartitionMap["n3"])
	}

	// 5. 验证：n1, n2 被标记为多数派
	if cluster.PartitionMap["n1"] != "majority" {
		t.Errorf("Expected n1 to be in majority partition, got '%s'", cluster.PartitionMap["n1"])
	}

	if cluster.PartitionMap["n2"] != "majority" {
		t.Errorf("Expected n2 to be in majority partition, got '%s'", cluster.PartitionMap["n2"])
	}

	// 6. 恢复 n3 心跳
	cluster.mu.Lock()
	cluster.HeartbeatMap["n3"] = time.Now()
	cluster.mu.Unlock()

	// 7. 再次运行分区检测
	err = cluster.DetectPartition()
	if err != nil {
		t.Fatalf("Failed to detect partition healing: %v", err)
	}

	// 8. 验证：网络状态恢复
	if cluster.NetworkStatus != "normal" {
		t.Errorf("Expected network status to be 'normal' after healing, got '%s'", cluster.NetworkStatus)
	}

	// 9. 验证：分区映射被清空
	if len(cluster.Partitions) != 0 {
		t.Errorf("Expected partitions to be cleared, got %d partitions", len(cluster.Partitions))
	}
}

// TestTC045_MultiplePartitions 测试多分区循环
// 验证目标：可以多次创建和恢复分区
func TestTC045_MultiplePartitions(t *testing.T) {
	tempDir := setupTempDir(t)
	defer os.RemoveAll(tempDir)

	cluster := NewCluster([]string{"n1", "n2", "n3", "n4", "n5"}, tempDir)
	defer cluster.Close()

	majority := cluster.GetMajority()

	// ===== 第一轮分区 =====
	// 1. 创建分区：n1,n2,n3 vs n4,n5
	err := cluster.CreatePartition(
		[]string{"n1", "n2", "n3"},
		[]string{"n4", "n5"},
	)
	if err != nil {
		t.Fatalf("Failed to create partition 1: %v", err)
	}

	// 2. 多数派发起投票并决策
	for _, nodeID := range []string{"n1", "n2", "n3"} {
		cluster.GetNode(nodeID).ProposeVote(0)
	}

	// 3. 多数派分区内 gossip（不跨分区）
	n1 := cluster.GetNode("n1")
	n2 := cluster.GetNode("n2")
	n3 := cluster.GetNode("n3")
	_ = n1.GossipExchange(n2, cluster)
	_ = n1.GossipExchange(n3, cluster)
	_ = n2.GossipExchange(n3, cluster)

	// 4. 多数派的所有节点都提交决策
	success, err := n1.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n1 to commit in partition 1")
	}

	success, err = n2.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n2 to commit in partition 1")
	}

	success, err = n3.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n3 to commit in partition 1")
	}

	// 3. 恢复分区（不触发自动 gossip，避免竞态条件）
	err = cluster.HealPartitionNoAutoGossip()
	if err != nil {
		t.Fatalf("Failed to heal partition 1: %v", err)
	}

	// 手动触发 gossip 确保完全同步
	cluster.GossipRound()

	// 验证：网络状态恢复正常
	if cluster.NetworkStatus != "normal" {
		t.Errorf("Expected network status to be 'normal' after healing 1, got '%s'", cluster.NetworkStatus)
	}

	// ===== 第二轮分区 =====
	// 重置所有节点的决策状态（模拟新一轮投票）
	for _, node := range cluster.Nodes {
		node.mu.Lock()
		node.Decision = Undecided
		node.Knowledge.Seen = make(map[string]bool)
		node.Knowledge.Decided = make(map[string]bool)
		node.mu.Unlock()
	}

	// 4. 创建分区：n1,n2 vs n3,n4,n5（交换多数/少数派角色）
	err = cluster.CreatePartition(
		[]string{"n1", "n2"},
		[]string{"n3", "n4", "n5"},
	)
	if err != nil {
		t.Fatalf("Failed to create partition 2: %v", err)
	}

	// 5. 新的多数派发起投票并决策
	for _, nodeID := range []string{"n3", "n4", "n5"} {
		cluster.GetNode(nodeID).ProposeVote(0)
	}

	// 6. 新的多数派分区内 gossip（不跨分区）
	n3 = cluster.GetNode("n3")
	n4 := cluster.GetNode("n4")
	n5 := cluster.GetNode("n5")
	_ = n3.GossipExchange(n4, cluster)
	_ = n3.GossipExchange(n5, cluster)
	_ = n4.GossipExchange(n5, cluster)

	// 7. 新的多数派的所有节点都提交决策
	success, err = n3.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n3 to commit in partition 2")
	}

	success, err = n4.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n4 to commit in partition 2")
	}

	success, err = n5.DecideCommit(majority)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !success {
		t.Error("Expected n5 to commit in partition 2")
	}

	// 7. 再次恢复分区（不触发自动 gossip，避免竞态条件）
	err = cluster.HealPartitionNoAutoGossip()
	if err != nil {
		t.Fatalf("Failed to heal partition 2: %v", err)
	}

	// 验证：网络状态再次恢复正常
	if cluster.NetworkStatus != "normal" {
		t.Errorf("Expected network status to be 'normal' after healing 2, got '%s'", cluster.NetworkStatus)
	}

	// 少数派节点发起投票，然后跟随多数派决策
	n1.ProposeVote(0)
	n2.ProposeVote(0)

	// 再次 gossip，让 n1, n2 知道 n3, n4, n5 已提交
	cluster.GossipRound()

	if !n1.FollowDecision(majority) {
		t.Error("Expected n1 to follow decision in round 2")
	}
	if !n2.FollowDecision(majority) {
		t.Error("Expected n2 to follow decision in round 2")
	}

	// 验证：所有节点最终都决策了
	for _, node := range cluster.Nodes {
		decision, _, _, _ := node.GetState()
		if decision != Committed {
			t.Errorf("Expected node %s to be committed after multiple partitions", node.ID)
		}
	}
}
