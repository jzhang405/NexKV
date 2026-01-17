// Package consensus Quorum 机制测试
package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// Quorum 机制测试
// ========================================

// TestNewQuorumService 测试创建 Quorum 服务
func TestNewQuorumService(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	localAddr := "node1"
	nodes := []string{"node1", "node2", "node3"}

	config := DefaultQuorumConfig()
	service, err := NewQuorumService(metaStore, trans, hlc, localAddr, nodes, config)

	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, config, service.config)
	assert.Equal(t, metaStore, service.metaStore)
	assert.Equal(t, trans, service.transport)
	assert.Equal(t, hlc, service.hlc)
	assert.Equal(t, localAddr, service.localAddr)
	assert.Equal(t, nodes, service.nodes)
}

// TestQuorumService_StartStop 测试启动和停止 Quorum 服务
func TestQuorumService_StartStop(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	// 测试启动
	err = service.Start()
	assert.NoError(t, err)
	assert.True(t, service.started.Load())

	// 测试重复启动
	err = service.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	// 测试停止
	err = service.Stop()
	assert.NoError(t, err)
	assert.True(t, service.stopped.Load())

	// 测试重复停止
	err = service.Stop()
	assert.NoError(t, err)
}

// TestQuorumService_Propose_Success 测试提案成功（单节点场景）
func TestQuorumService_Propose_Success(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	hlc := clock.NewHLC()
	nodes := []string{"node1"} // 单节点，避免网络依赖

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 发起提案
	proposal := &transport.QuorumProposeMessage{
		Key:       "shard/test",
		Value:     []byte("shard_data"),
		Operation: "put",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = service.Propose(ctx, proposal)
	// 单节点场景应该成功（本地投票即达到多数派）
	assert.NoError(t, err)

	// 验证数据已写入
	val, err := metaStore.Get("shard/test")
	assert.NoError(t, err)
	assert.Equal(t, []byte("shard_data"), val)
}

// TestQuorumService_Propose_Timeout 测试提案超时
func TestQuorumService_Propose_Timeout(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	// 设置短超时
	config := &QuorumConfig{
		Timeout:    100 * time.Millisecond,
		RetryCount: 1,
		MinQuorum:  3, // 需要所有节点确认
	}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, config)
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 发起提案（没有其他节点响应）
	proposal := &transport.QuorumProposeMessage{
		Key:       "shard/test",
		Value:     []byte("shard_data"),
		Operation: "put",
	}

	ctx := context.Background()
	err = service.Propose(ctx, proposal)

	// 应该超时
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "超时")
}

// TestQuorumService_Vote 测试投票处理
func TestQuorumService_Vote(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 创建一个提案状态
	proposal := &transport.QuorumProposeMessage{
		Key:       "test_key",
		Value:     []byte("test_value"),
		Operation: "put",
	}
	proposal.ProposalID = "test_proposal_1"

	state := &ProposalState{
		ProposalID: proposal.ProposalID,
		Proposal:   proposal,
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}

	service.proposalsMu.Lock()
	service.proposals[proposal.ProposalID] = state
	service.proposalsMu.Unlock()

	// 测试投票
	voteMsg := &transport.QuorumVoteMessage{
		ProposalID: proposal.ProposalID,
		Voter:      "node2",
		Vote:       true,
		Reason:     "",
	}

	err = service.Vote(voteMsg)
	assert.NoError(t, err)

	// 验证投票已记录
	state.votesMu.RLock()
	voted, exists := state.votes["node2"]
	state.votesMu.RUnlock()

	assert.True(t, exists)
	assert.True(t, voted)
}

// TestQuorumService_Vote_NonExistentProposal 测试对不存在的提案投票
func TestQuorumService_Vote_NonExistentProposal(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 对不存在的提案投票
	voteMsg := &transport.QuorumVoteMessage{
		ProposalID: "non_existent_proposal",
		Voter:      "node2",
		Vote:       true,
	}

	err = service.Vote(voteMsg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestQuorumService_checkQuorum 测试法定人数检查
func TestQuorumService_checkQuorum(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	// 测试 3 节点集群，法定人数为 2
	threshold := service.getQuorumThreshold()
	assert.Equal(t, int32(2), threshold) // N/2 + 1 = 3/2 + 1 = 2

	// 创建提案状态
	state := &ProposalState{
		ProposalID: "test_proposal",
		Proposal:   &transport.QuorumProposeMessage{},
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}

	// 测试达到法定人数
	state.voteCount.Store(2)
	service.checkQuorum(state)

	assert.True(t, state.decided.Load())
	assert.True(t, state.approved.Load())

	// 测试无法达到法定人数
	state2 := &ProposalState{
		ProposalID: "test_proposal_2",
		Proposal:   &transport.QuorumProposeMessage{},
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}
	state2.voteCount.Store(0)
	state2.totalVotes.Store(3) // 所有人都已投票，但都不赞成
	service.checkQuorum(state2)

	assert.True(t, state2.decided.Load())
	assert.False(t, state2.approved.Load())
}

// TestQuorumService_getQuorumThreshold 测试法定人数阈值计算
func TestQuorumService_getQuorumThreshold(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()

	// 测试不同节点数量的法定人数
	testCases := []struct {
		name      string
		nodeCount int
		MinQuorum int
		Expected  int32
	}{
		{"3节点，自动计算", 3, 0, 2},  // 3/2 + 1 = 2
		{"5节点，自动计算", 5, 0, 3},  // 5/2 + 1 = 3
		{"7节点，自动计算", 7, 0, 4},  // 7/2 + 1 = 4
		{"5节点，手动设置", 5, 4, 4},  // 使用手动值
		{"5节点，手动设置2", 5, 2, 2}, // 使用手动值
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := make([]string, tc.nodeCount)
			for i := 0; i < tc.nodeCount; i++ {
				nodes[i] = "node" + string(rune('1'+i))
			}

			config := &QuorumConfig{
				MinQuorum: tc.MinQuorum,
			}

			service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, config)
			require.NoError(t, err)

			threshold := service.getQuorumThreshold()
			assert.Equal(t, tc.Expected, threshold)
		})
	}
}

// TestQuorumService_generateProposalID 测试提案 ID 生成
func TestQuorumService_generateProposalID(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	// 生成多个提案 ID，验证唯一性
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := service.generateProposalID()
		assert.Contains(t, id, "proposal-")

		// 验证唯一性
		if exists := ids[id]; exists {
			t.Errorf("生成的提案 ID 重复: %s", id)
		}
		ids[id] = true
	}
}

// TestQuorumService_AddRemoveNode 测试添加和移除节点
func TestQuorumService_AddRemoveNode(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2"}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 添加节点
	service.AddNode("node3")
	assert.Contains(t, service.nodes, "node3")

	// 移除节点
	service.RemoveNode("node2")
	assert.NotContains(t, service.nodes, "node2")
}

// TestQuorumService_GetStats 测试获取统计信息
func TestQuorumService_GetStats(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 获取统计信息
	stats := service.GetStats()

	assert.NotNil(t, stats)
	assert.NotNil(t, stats.ProposalsTotal.Load())
	assert.NotNil(t, stats.ProposalsApproved.Load())
	assert.NotNil(t, stats.ProposalsRejected.Load())
	assert.NotNil(t, stats.ProposalsTimeout.Load())
	assert.NotNil(t, stats.AvgVoteLatency.Load())
}

// TestQuorumService_GetProposalState 测试获取提案状态
func TestQuorumService_GetProposalState(t *testing.T) {
	metaStore := newMockMVStore()
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, err := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	require.NoError(t, err)

	err = service.Start()
	require.NoError(t, err)
	defer func() { _ = service.Stop() }()

	// 创建提案状态
	proposalID := "test_proposal"
	state := &ProposalState{
		ProposalID: proposalID,
		Proposal:   &transport.QuorumProposeMessage{},
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}

	service.proposalsMu.Lock()
	service.proposals[proposalID] = state
	service.proposalsMu.Unlock()

	// 获取提案状态
	retrievedState, exists := service.GetProposalState(proposalID)
	assert.True(t, exists)
	assert.Equal(t, state, retrievedState)

	// 测试获取不存在的提案
	_, exists = service.GetProposalState("non_existent")
	assert.False(t, exists)
}

// TestDefaultQuorumConfig 测试默认 Quorum 配置
func TestDefaultQuorumConfig(t *testing.T) {
	config := DefaultQuorumConfig()

	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, 3, config.RetryCount)
	assert.Equal(t, 0, config.MinQuorum) // 0 表示自动计算
}

// TestProposalState_decide 测试提案决策
func TestProposalState_decide(t *testing.T) {
	state := &ProposalState{
		ProposalID: "test_proposal",
		Proposal:   &transport.QuorumProposeMessage{},
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}

	// 测试批准决策
	state.decide(true)
	assert.True(t, state.decided.Load())
	assert.True(t, state.approved.Load())

	// 验证决策通道已关闭
	select {
	case <-state.decideCh:
		// 正常，通道已关闭
	default:
		t.Error("决策通道应该已关闭")
	}

	// 测试拒绝决策
	state2 := &ProposalState{
		ProposalID: "test_proposal_2",
		Proposal:   &transport.QuorumProposeMessage{},
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}

	state2.decide(false)
	assert.True(t, state2.decided.Load())
	assert.False(t, state2.approved.Load())
}

// ========================================
// 性能基准测试
// ========================================

// BenchmarkQuorumService_Propose 性能基准测试: 提案
func BenchmarkQuorumService_Propose(b *testing.B) {
	metaStore := newMockMVStore()
	trans, _ := transport.NewMemoryTransport("node1")
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	config := &QuorumConfig{
		Timeout:    1 * time.Second,
		RetryCount: 0,
		MinQuorum:  1, // 只需要自己确认
	}

	service, _ := NewQuorumService(metaStore, trans, hlc, "node1", nodes, config)
	_ = service.Start()
	defer func() { _ = service.Stop() }()

	proposal := &transport.QuorumProposeMessage{
		Key:       "test_key",
		Value:     []byte("test_value"),
		Operation: "put",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		_ = service.Propose(ctx, proposal)
	}
}

// BenchmarkQuorumService_Vote 性能基准测试: 投票
func BenchmarkQuorumService_Vote(b *testing.B) {
	metaStore := newMockMVStore()
	trans, _ := transport.NewMemoryTransport("node1")
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, _ := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	_ = service.Start()
	defer func() { _ = service.Stop() }()

	// 创建提案状态
	proposal := &transport.QuorumProposeMessage{
		Key:       "test_key",
		Value:     []byte("test_value"),
		Operation: "put",
	}
	proposal.ProposalID = "test_proposal_1"

	state := &ProposalState{
		ProposalID: proposal.ProposalID,
		Proposal:   proposal,
		votes:      make(map[string]bool),
		decideCh:   make(chan struct{}),
		createTime: time.Now(),
	}

	service.proposalsMu.Lock()
	service.proposals[proposal.ProposalID] = state
	service.proposalsMu.Unlock()

	voteMsg := &transport.QuorumVoteMessage{
		ProposalID: proposal.ProposalID,
		Voter:      "node2",
		Vote:       true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.Vote(voteMsg)
	}
}

// BenchmarkQuorumService_generateProposalID 性能基准测试: 生成提案 ID
func BenchmarkQuorumService_generateProposalID(b *testing.B) {
	metaStore := newMockMVStore()
	trans, _ := transport.NewMemoryTransport("node1")
	hlc := clock.NewHLC()
	nodes := []string{"node1", "node2", "node3"}

	service, _ := NewQuorumService(metaStore, trans, hlc, "node1", nodes, DefaultQuorumConfig())
	_ = service.Start()
	defer func() { _ = service.Stop() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.generateProposalID()
	}
}
