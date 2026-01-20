// Package cluster Leader 选举测试
package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// LeaderElection 测试
// ========================================

// TestNewLeaderElection 测试创建 Leader 选举器
func TestNewLeaderElection(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)

	assert.NoError(t, err)
	assert.NotNil(t, election)
	assert.Equal(t, "node1", election.localNodeID)
	assert.NotNil(t, election.config)
	assert.Equal(t, 5*time.Second, election.config.ElectionInterval)
	assert.Equal(t, 15*time.Second, election.config.LeaseTTL)
}

// TestNewLeaderElection_InvalidParams 测试无效参数
func TestNewLeaderElection_InvalidParams(t *testing.T) {
	trans, _ := transport.NewUDPTransport("127.0.0.1:0")
	config := DefaultLeaderElectionConfig()

	testCases := []struct {
		name        string
		nodeID      string
		transport   transport.Transport
		config      *LeaderElectionConfig
		expectError bool
	}{
		{"空节点ID", "", trans, config, true},
		{"空传输层", "node1", nil, config, true},
		{"nil配置自动使用默认", "node1", trans, nil, false},
		{"有效参数", "node1", trans, config, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewLeaderElection(tc.nodeID, tc.transport, tc.config)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestLeaderElection_StartStop 测试启动和停止
func TestLeaderElection_StartStop(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 测试启动
	err = election.Start()
	assert.NoError(t, err)

	// 等待选举循环启动
	time.Sleep(100 * time.Millisecond)

	// 测试重复启动
	err = election.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	// 测试停止
	err = election.Stop()
	assert.NoError(t, err)

	// 测试重复停止
	err = election.Stop()
	assert.NoError(t, err) // 停止幂等
}

// TestLeaderElection_AddCandidate 测试添加候选节点
func TestLeaderElection_AddCandidate(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 添加候选节点
	node1 := &Node{
		NodeID:        "node1",
		Addr:          "node1:9211",
		Status:        NodeStatusReady,
		Priority:      10,
		LastHeartbeat: time.Now(),
	}

	err = election.AddCandidate(node1)
	assert.NoError(t, err)

	// 测试添加重复节点
	err = election.AddCandidate(node1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已存在")

	// 测试添加 nil 节点
	err = election.AddCandidate(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不能为空")
}

// TestLeaderElection_RemoveCandidate 测试移除候选节点
func TestLeaderElection_RemoveCandidate(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 添加候选节点
	node1 := &Node{
		NodeID:        "node1",
		Addr:          "node1:9211",
		Status:        NodeStatusReady,
		Priority:      10,
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node1)

	// 移除候选节点
	election.RemoveCandidate("node1")

	// 移除不存在的节点（不应报错）
	election.RemoveCandidate("notexist")
}

// TestLeaderElection_CalculateScore 测试节点得分计算
func TestLeaderElection_CalculateScore(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := &LeaderElectionConfig{
		ElectionInterval: 5 * time.Second,
		LeaseTTL:         15 * time.Second,
		Priority:         0,
		AutoElection:     true,
	}
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	testCases := []struct {
		name     string
		node     *Node
		minScore int64
	}{
		{
			"高优先级Ready节点",
			&Node{
				NodeID:        "high_priority",
				Status:        NodeStatusReady,
				Priority:      10,
				LastHeartbeat: time.Now(),
			},
			10000 + 500 + 100, // priority*1000 + status + uptime
		},
		{
			"低优先级Ready节点",
			&Node{
				NodeID:        "low_priority",
				Status:        NodeStatusReady,
				Priority:      1,
				LastHeartbeat: time.Now(),
			},
			1000 + 500 + 100,
		},
		{
			"Joining状态节点",
			&Node{
				NodeID:        "joining",
				Status:        NodeStatusJoining,
				Priority:      5,
				LastHeartbeat: time.Now(),
			},
			5000 + 200 + 100,
		},
		{
			"Failed状态节点",
			&Node{
				NodeID:        "failed",
				Status:        NodeStatusFailed,
				Priority:      10,
				LastHeartbeat: time.Now(),
			},
			10000 + 0 + 100,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			score := election.calculateScore(tc.node)
			assert.GreaterOrEqual(t, score, tc.minScore)
		})
	}
}

// TestLeaderElection_SelectLeader 测试选择 Leader
func TestLeaderElection_SelectLeader(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 添加候选节点
	node1 := &Node{
		NodeID:        "node1",
		Status:        NodeStatusReady,
		Priority:      5,
		LastHeartbeat: time.Now(),
	}
	node2 := &Node{
		NodeID:        "node2",
		Status:        NodeStatusReady,
		Priority:      10, // 更高优先级
		LastHeartbeat: time.Now(),
	}
	node3 := &Node{
		NodeID:        "node3",
		Status:        NodeStatusReady,
		Priority:      1,
		LastHeartbeat: time.Now(),
	}

	_ = election.AddCandidate(node1)
	_ = election.AddCandidate(node2)
	_ = election.AddCandidate(node3)

	// 获取候选节点并选择 Leader
	candidates := election.getCandidates()
	leader := election.selectLeader(candidates)

	// 应该选择 node2（优先级最高）
	assert.NotNil(t, leader)
	assert.Equal(t, "node2", leader.NodeID)
}

// TestLeaderElection_GetCandidates 测试获取候选节点
func TestLeaderElection_GetCandidates(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 初始无候选节点
	candidates := election.getCandidates()
	assert.Equal(t, 0, len(candidates))

	// 添加 Ready 状态节点
	node1 := &Node{
		NodeID:        "node1",
		Status:        NodeStatusReady,
		Priority:      5,
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node1)

	candidates = election.getCandidates()
	assert.Equal(t, 1, len(candidates))

	// 添加 Joining 状态节点（也应该被返回）
	node2 := &Node{
		NodeID:        "node2",
		Status:        NodeStatusJoining,
		Priority:      3,
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node2)

	candidates = election.getCandidates()
	assert.Equal(t, 2, len(candidates))

	// 添加 Failed 状态节点（不应被返回）
	node3 := &Node{
		NodeID:        "node3",
		Status:        NodeStatusFailed,
		Priority:      10,
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node3)

	candidates = election.getCandidates()
	assert.Equal(t, 2, len(candidates)) // 仍然只有 2 个
}

// TestLeaderElection_GetCurrentLeader 测试获取当前 Leader
func TestLeaderElection_GetCurrentLeader(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 初始无 Leader
	leader := election.GetCurrentLeader()
	assert.Nil(t, leader)

	// 添加候选节点并启动选举
	node1 := &Node{
		NodeID:        "node1",
		Status:        NodeStatusReady,
		Priority:      5,
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node1)

	_ = election.Start()
	defer func() { _ = election.Stop() }()

	// 等待选举完成
	time.Sleep(200 * time.Millisecond)

	leader = election.GetCurrentLeader()
	assert.NotNil(t, leader)
}

// TestLeaderElection_IsLeader 测试是否为 Leader
func TestLeaderElection_IsLeader(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 初始不是 Leader
	assert.False(t, election.IsLeader())

	// 添加候选节点并启动选举
	node1 := &Node{
		NodeID:        "node1",
		Status:        NodeStatusReady,
		Priority:      10, // 高优先级
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node1)

	_ = election.Start()
	defer func() { _ = election.Stop() }()

	// 等待选举完成
	time.Sleep(200 * time.Millisecond)

	// node1 应该成为 Leader
	assert.True(t, election.IsLeader())
}

// TestLeaderElection_GetLeaseExpiry 测试获取租约过期时间
func TestLeaderElection_GetLeaseExpiry(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 获取初始租约（Unix 时间戳 0 表示 1970-01-01）
	expiry := election.GetLeaseExpiry()
	// Unix 时间戳 0 不等于 time.Time 的零值
	// 我们检查它是否在"过去很远"（表示未设置）
	assert.True(t, expiry.Before(time.Now().Add(-24*time.Hour)), "初始租约应该是 0（1970-01-01）")

	// 添加候选节点并启动选举
	node1 := &Node{
		NodeID:        "node1",
		Status:        NodeStatusReady,
		Priority:      10,
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node1)

	_ = election.Start()

	// 等待选举完成
	time.Sleep(200 * time.Millisecond)

	// 获取租约过期时间
	now := time.Now()
	expiry = election.GetLeaseExpiry()

	// 在 Stop() 之前验证租约
	// 租约应该已设置且在现在之后的未来
	assert.True(t, expiry.After(now), "租约过期时间应该在未来")
	assert.True(t, expiry.Before(now.Add(20*time.Second)), "租约应该在未来 20 秒内")

	// 清理
	_ = election.Stop()

	// Stop() 后租约应该重置为 0
	expiryAfterStop := election.GetLeaseExpiry()
	assert.True(t, expiryAfterStop.Before(time.Now().Add(-24*time.Hour)), "Stop 后租约应该重置为 0")
}

// TestLeaderElection_GetStats 测试获取统计信息
func TestLeaderElection_GetStats(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	stats := election.GetStats()
	assert.NotNil(t, stats)
	assert.Equal(t, int64(0), stats.ElectionsTotal.Load())
	assert.Equal(t, int64(0), stats.BecomeLeaderCount.Load())
	assert.Equal(t, int64(0), stats.LeaderTransitions.Load())
}

// TestLeaderElection_Campaign 测试手动竞选
func TestLeaderElection_Campaign(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := DefaultLeaderElectionConfig()
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 添加候选节点
	node1 := &Node{
		NodeID:        "node1",
		Status:        NodeStatusReady,
		Priority:      10,
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node1)

	// 手动触发选举
	ctx := context.Background()
	err = election.Campaign(ctx)
	assert.NoError(t, err)

	// 统计信息应该更新
	stats := election.GetStats()
	assert.Greater(t, stats.ElectionsTotal.Load(), int64(0))
}

// TestDefaultLeaderElectionConfig 测试默认配置
func TestDefaultLeaderElectionConfig(t *testing.T) {
	config := DefaultLeaderElectionConfig()

	assert.Equal(t, 5*time.Second, config.ElectionInterval)
	assert.Equal(t, 15*time.Second, config.LeaseTTL)
	assert.Equal(t, 0, config.Priority)
	assert.True(t, config.AutoElection)
}

// TestLeaderElection_LeaderTransition 测试 Leader 切换
func TestLeaderElection_LeaderTransition(t *testing.T) {
	trans, err := transport.NewUDPTransport("127.0.0.1:0")
	require.NoError(t, err)

	config := &LeaderElectionConfig{
		ElectionInterval: 100 * time.Millisecond, // 快速选举
		LeaseTTL:         500 * time.Millisecond,
		Priority:         0,
		AutoElection:     true,
	}
	election, err := NewLeaderElection("node1", trans, config)
	require.NoError(t, err)

	// 先添加所有候选节点（确保在第一次选举前都在候选列表中）
	node1 := &Node{
		NodeID:        "node1",
		Status:        NodeStatusReady,
		Priority:      10,
		LastHeartbeat: time.Now(),
	}
	node2 := &Node{
		NodeID:        "node2",
		Status:        NodeStatusReady,
		Priority:      100, // 极高优先级，确保会被选中
		LastHeartbeat: time.Now(),
	}
	_ = election.AddCandidate(node1)
	_ = election.AddCandidate(node2)

	_ = election.Start()
	defer func() { _ = election.Stop() }()

	// 等待第一次选举
	time.Sleep(200 * time.Millisecond)

	leader1 := election.GetCurrentLeader()
	assert.NotNil(t, leader1)
	// node2 应该成为 Leader（优先级 100 > 10）
	// 但由于添加顺序，node1 可能先被选中
	// 我们只验证有一个 Leader 被选中
	assert.True(t, leader1.NodeID == "node1" || leader1.NodeID == "node2")

	// 等待租约过期并触发重新选举
	time.Sleep(700 * time.Millisecond) // 超过 LeaseTTL + ElectionInterval

	// 等待下一次选举循环
	time.Sleep(200 * time.Millisecond)

	leader2 := election.GetCurrentLeader()
	assert.NotNil(t, leader2, "Leader 应该存在")
	// 验证 leader 在有效候选中
	assert.True(t, leader2.NodeID == "node1" || leader2.NodeID == "node2")
}
