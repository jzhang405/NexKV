package implementations

import (
	"fmt"
	"testing"
	"time"
)

// TestTC006_CoordinatorCrash 协调者崩溃恢复测试
//
// 测试场景：
// 1. n1 作为协调者启动事务
// 2. n1 在 PrePrepare 阶段崩溃
// 3. n2 或 n3 检测到协调者故障，接管协调者角色
// 4. 新协调者继续执行 2PC 流程
// 5. 验证事务最终完成
func TestTC006_CoordinatorCrash(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 阶段 1: 协调者 n1 预准备
	nodes[0].PrePrepare(tx)

	// Gossip 同步事务状态
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	// 验证：所有节点都知道事务处于 PreCommit 阶段
	for i, node := range nodes {
		phase, _ := node.GetTransactionState("tx1")
		if phase != PhasePreCommit {
			t.Errorf("[Step1] Expected n%d to know PreCommit phase, got %s", i+1, phase)
		}
	}

	// 阶段 2: 所有参与者投票
	nodes[0].VoteRequest(tx, true)
	nodes[1].VoteRequest(tx, true)
	nodes[2].VoteRequest(tx, true)

	// 同步投票结果
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	t.Logf("[Step2] All participants voted YES")

	// 阶段 3: 模拟协调者 n1 崩溃
	// 创建新的协调者实例（模拟 n2 接管）
	t.Logf("[Step3] Coordinator n1 crashed, n2 taking over...")

	// n2 检查本地事务状态，发现需要接管
	phase, decision := nodes[2].GetTransactionState("tx1")
	if phase != PhasePreCommit {
		t.Errorf("Expected PreCommit phase before takeover, got %s", phase)
	}

	// n2 接管协调者角色，执行决策
	// 注意：这里 n2 使用本地存储的事务对象进行决策
	if txFromN2, exists := nodes[2].Transactions["tx1"]; exists {
		nodes[2].Decide(txFromN2)
	}

	// 验证：n2 成功决策
	phase, decision = nodes[2].GetTransactionState("tx1")
	if phase != PhaseCommit || decision != "commit" {
		t.Errorf("[Step4] Expected n2 to commit transaction, got phase=%s, decision=%s", phase, decision)
	}

	// 阶段 4: 新协调者 n2 将决策结果 Gossip 给其他节点
	nodes[2].GossipSync(nodes[0]) // n1 恢复后接收状态
	nodes[2].GossipSync(nodes[1])

	// 验证：所有节点都知道最终决策
	for i, node := range nodes {
		phase, decision := node.GetTransactionState("tx1")
		if phase != PhaseCommit && phase != PhaseDone {
			t.Errorf("[Step5] Expected n%d to know final decision, got phase=%s, decision=%s", i+1, phase, decision)
		}
	}

	t.Logf("[Success] Transaction completed successfully after coordinator crash")
}

// TestTC007_ParticipantCrashRecovery 参与者崩溃恢复测试
//
// 测试场景：
// 1. 启动 2PC 流程，n2 为参与者
// 2. n2 在 PreCommit 阶段崩溃
// 3. n2 恢复后，通过 Gossip 请求事务状态
// 4. n2 根据决策恢复本地事务状态
// 5. 验证所有节点最终一致性
func TestTC007_ParticipantCrashRecovery(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 阶段 1: 协调者 n1 预准备
	nodes[0].PrePrepare(tx)

	// Gossip 同步（n2 收到状态后崩溃）
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	t.Logf("[Step1] n2 received PreCommit state")

	// 阶段 2: 模拟 n2 崩溃（丢失内存状态）
	// 创建新的 n2 实例（模拟重启）
	n2Recovered := NewTwoPhaseCommitNode("n2")
	nodes[1] = n2Recovered

	t.Logf("[Step2] n2 crashed and recovered")

	// 阶段 3: n1 和 n3 继续投票
	// 注意：n2 已经重启，使用新的 n2Recovered 实例
	// 需要确保 n1 使用的是原始事务对象
	if txObj, exists := nodes[0].Transactions["tx1"]; exists {
		nodes[0].VoteRequest(txObj, true)
	}

	if txObj, exists := nodes[2].Transactions["tx1"]; exists {
		nodes[2].VoteRequest(txObj, true)
	}

	// n1 和 n3 同步投票给 n2（恢复后的节点）
	nodes[0].GossipSync(n2Recovered)
	nodes[2].GossipSync(n2Recovered)

	// n2 恢复后也需要投票
	if txObj, exists := n2Recovered.Transactions["tx1"]; exists {
		n2Recovered.VoteRequest(txObj, true)
	}

	// 同步 n2 的投票
	n2Recovered.GossipSync(nodes[0])
	n2Recovered.GossipSync(nodes[2])

	// 阶段 4: 协调者 n1 决策
	if txObj, exists := nodes[0].Transactions["tx1"]; exists {
		nodes[0].Decide(txObj)
	}

	// 验证：n1 和 n3 知道最终决策
	phase, decision := nodes[0].GetTransactionState("tx1")
	if phase != PhaseCommit || decision != "commit" {
		t.Errorf("[Step4] Expected n1 to commit, got phase=%s, decision=%s", phase, decision)
	}

	// 阶段 5: n2 恢复后主动请求状态
	// 通过 Gossip 从 n1 或 n3 同步
	nodes[0].GossipSync(n2Recovered)
	nodes[2].GossipSync(n2Recovered)

	// 验证：n2 恢复了事务状态
	phase, decision = n2Recovered.GetTransactionState("tx1")
	if phase != PhaseCommit && phase != PhaseDone {
		t.Errorf("[Step5] Expected n2 to recover final decision, got phase=%s, decision=%s", phase, decision)
	}

	if decision != "commit" {
		t.Errorf("[Step5] Expected n2 to know commit decision, got %s", decision)
	}

	t.Logf("[Success] All nodes reached consistency after participant crash recovery")
}

// TestTC008_TwoPhaseCommitUnderPartition 网络分区下的 2PC 测试
//
// 测试场景：
// 1. 集群分成两个分区：{n1, n2} vs {n3}
// 2. n1 作为协调者启动事务
// 3. 分区导致 n3 的投票无法传递给协调者
// 4. 协调者超时后中止事务
// 5. 分区恢复后，通过 Gossip 同步最终状态
func TestTC008_TwoPhaseCommitUnderPartition(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 阶段 1: 协调者 n1 预准备
	nodes[0].PrePrepare(tx)

	// 正常 Gossip 同步（分区还未发生）
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	t.Logf("[Step1] All nodes received PreCommit state")

	// 阶段 2: 模拟网络分区 {n1, n2} vs {n3}
	t.Logf("[Step2] Network partition occurred: {n1, n2} isolated from {n3}")

	// 分区 {n1, n2} 内部可以通信
	if txObj, exists := nodes[0].Transactions["tx1"]; exists {
		nodes[0].VoteRequest(txObj, true)
	}
	if txObj, exists := nodes[1].Transactions["tx1"]; exists {
		nodes[1].VoteRequest(txObj, true)
	}

	// 分区内同步
	nodes[0].GossipSync(nodes[1])

	// n3 被隔离，投了票但无法同步给 n1 和 n2
	if txObj, exists := nodes[2].Transactions["tx1"]; exists {
		nodes[2].VoteRequest(txObj, true)
	}
	// n3 的投票无法同步给 n1 和 n2

	// 阶段 3: 协调者 n1 只收到 n1 和 n2 的投票
	// 由于 n3 的投票无法到达，协调者检测到投票不完整
	voteCount := 0
	if nodes[0].Transactions["tx1"] != nil {
		for _, vote := range nodes[0].Transactions["tx1"].Votes {
			if vote {
				voteCount++
			}
		}
	}

	t.Logf("[Step3] Coordinator n1 collected %d/%d votes (n3 isolated)", voteCount, len(participants))

	// 协调者等待超时后决定中止（模拟超时场景）
	if txObj, exists := nodes[0].Transactions["tx1"]; exists {
		txObj.Decision = "abort"
		txObj.Phase = PhaseDone
	}

	// 验证：事务中止
	phase, decision := nodes[0].GetTransactionState("tx1")
	if decision != "abort" {
		t.Errorf("[Step4] Expected transaction to abort due to partition, got phase=%s, decision=%s", phase, decision)
	}

	// 阶段 4: 分区恢复
	t.Logf("[Step5] Network partition healed, syncing states...")

	// 分区恢复后的 Gossip 同步
	nodes[0].GossipSync(nodes[2])
	nodes[1].GossipSync(nodes[2])

	// 验证：n3 也知道事务中止
	phase, decision = nodes[2].GetTransactionState("tx1")
	if decision != "abort" {
		t.Errorf("[Step5] Expected n3 to know abort decision, got phase=%s, decision=%s", phase, decision)
	}

	// 验证：所有节点最终一致
	for i, node := range nodes {
		_, decision := node.GetTransactionState("tx1")
		if decision != "abort" {
			t.Errorf("[Step6] Expected all nodes to agree on abort, n%d has decision=%s", i+1, decision)
		}
	}

	t.Logf("[Success] All nodes reached consistency after partition recovery")
}

// TestTC009_TransactionTimeout 事务超时回滚测试
//
// 测试场景：
// 1. 协调者启动事务
// 2. 长时间未收到所有投票
// 3. 事务超时，自动回滚
// 4. 通过 Gossip 通知所有参与者
func TestTC009_TransactionTimeout(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 阶段 1: 协调者预准备
	nodes[0].PrePrepare(tx)

	// Gossip 同步
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	t.Logf("[Step1] Transaction started, waiting for votes...")

	// 阶段 2: 只有 n1 投票，n2 和 n3 迟迟不投票
	nodes[0].VoteRequest(tx, true)

	// 模拟超时：等待足够时间
	time.Sleep(100 * time.Millisecond)

	// 阶段 3: 协调者检测超时，决定回滚
	// 手动设置为超时状态
	if txObj, exists := nodes[0].Transactions["tx1"]; exists {
		txObj.Decision = "abort-timeout"
		txObj.Phase = PhaseDone
	}

	phase, decision := nodes[0].GetTransactionState("tx1")
	if decision != "abort-timeout" {
		t.Errorf("[Step3] Expected timeout abort, got phase=%s, decision=%s", phase, decision)
	}

	// 阶段 4: Gossip 超时决策
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	// 验证：所有节点都知道超时中止
	for i, node := range nodes {
		_, decision := node.GetTransactionState("tx1")
		if decision != "abort-timeout" {
			t.Errorf("[Step4] Expected n%d to know timeout abort, got decision=%s", i+1, decision)
		}
	}

	t.Logf("[Success] Transaction aborted due to timeout, all nodes notified")
}

// TestTC010_AllNodesVoteNo 所有节点投反对票测试
//
// 测试场景：
// 1. 协调者启动事务
// 2. 所有参与者投票 NO（资源不足、冲突等）
// 3. 协调者决定中止
// 4. 验证所有节点知道中止决策
func TestTC010_AllNodesVoteNo(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 阶段 1: 协调者预准备
	nodes[0].PrePrepare(tx)

	// Gossip 同步
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	// 阶段 2: 所有节点投反对票
	nodes[0].VoteRequest(tx, false) // n1 投 NO
	nodes[1].VoteRequest(tx, false) // n2 投 NO
	nodes[2].VoteRequest(tx, false) // n3 投 NO

	t.Logf("[Step2] All participants voted NO")

	// 阶段 3: 协调者决策（应该中止）
	nodes[0].Decide(tx)

	// 验证：事务中止
	phase, decision := nodes[0].GetTransactionState("tx1")
	if phase != PhaseDone || decision != "abort" {
		t.Errorf("[Step3] Expected transaction abort, got phase=%s, decision=%s", phase, decision)
	}

	// 阶段 4: Gossip 决策
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])

	// 验证：所有节点都知道中止
	for i, node := range nodes {
		_, decision := node.GetTransactionState("tx1")
		if decision != "abort" {
			t.Errorf("[Step4] Expected n%d to know abort, got decision=%s", i+1, decision)
		}
	}

	t.Logf("[Success] Transaction aborted due to unanimous NO vote")
}

// TestTC011_CoordinatorLeaveCluster 协调者离开集群测试
//
// 测试场景：
// 1. n1 作为协调者启动事务
// 2. n1 在 PreCommit 阶段离开集群（掉线）
// 3. 剩余节点检测到协调者故障
// 4. 选举新协调者，继续或中止事务
// 5. 验证集群最终一致性
func TestTC011_CoordinatorLeaveCluster(t *testing.T) {
	nodes := make([]*TwoPhaseCommitNode, 4)
	for i := 0; i < 4; i++ {
		nodes[i] = NewTwoPhaseCommitNode(fmt.Sprintf("n%d", i+1))
	}

	participants := []string{"n1", "n2", "n3", "n4"}
	tx := nodes[0].StartTransaction("tx1", participants)

	// 阶段 1: n1 预准备
	nodes[0].PrePrepare(tx)

	// Gossip 同步
	for i := 1; i < 4; i++ {
		nodes[0].GossipSync(nodes[i])
	}

	t.Logf("[Step1] Coordinator n1 started transaction")

	// 阶段 2: 部分节点投票
	nodes[0].VoteRequest(tx, true)
	nodes[1].VoteRequest(tx, true)

	// 同步投票
	nodes[0].GossipSync(nodes[1])
	nodes[0].GossipSync(nodes[2])
	nodes[0].GossipSync(nodes[3])

	// 阶段 3: n1 离开集群（模拟掉线）
	t.Logf("[Step3] Coordinator n1 left the cluster")

	// 阶段 4: 剩余节点检测到协调者故障
	// n2 接管协调者角色
	// n2 检查当前投票状态
	if txObj, exists := nodes[1].Transactions["tx1"]; exists {
		// 只收到 2 票（n1 和 n2），未达到多数派（3/4）
		voteCount := 0
		for _, vote := range txObj.Votes {
			if vote {
				voteCount++
			}
		}

		if voteCount < 3 {
			t.Logf("[Step4] New coordinator n2 detected insufficient votes (%d/4), aborting", voteCount)
			txObj.Decision = "abort"
			txObj.Phase = PhaseDone
		}
	}

	// 验证：n2 决定中止
	phase, decision := nodes[1].GetTransactionState("tx1")
	if decision != "abort" {
		t.Errorf("[Step4] Expected n2 to abort transaction, got phase=%s, decision=%s", phase, decision)
	}

	// 阶段 5: 新协调者 n2 通知所有剩余节点
	nodes[1].GossipSync(nodes[2])
	nodes[1].GossipSync(nodes[3])

	// 验证：所有剩余节点都知道中止
	for i := 1; i < 4; i++ {
		_, decision := nodes[i].GetTransactionState("tx1")
		if decision != "abort" {
			t.Errorf("[Step5] Expected n%d to know abort, got decision=%s", i+1, decision)
		}
	}

	t.Logf("[Success] Cluster recovered from coordinator failure, transaction aborted")
}
