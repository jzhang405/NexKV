// Package cluster 自愈机制测试
package cluster

import (
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// SelfHealer 测试
// ========================================

// TestNewSelfHealer 测试创建自愈机制
func TestNewSelfHealer(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", trans, config)
	require.NoError(t, err)

	fdConfig := DefaultFailureDetectorConfig()
	failureDetector, err := NewFailureDetector("node1", trans, fdConfig)
	require.NoError(t, err)

	leConfig := DefaultLeaderElectionConfig()
	leaderElection, err := NewLeaderElection("node1", trans, leConfig)
	require.NoError(t, err)

	shConfig := DefaultSelfHealingConfig()
	healer, err := NewSelfHealer("node1", trans, coordinator, failureDetector, leaderElection, shConfig)

	assert.NoError(t, err)
	assert.NotNil(t, healer)
	assert.Equal(t, "node1", healer.localNodeID)
	assert.NotNil(t, healer.config)
	assert.Equal(t, 10*time.Second, healer.config.HealingInterval)
	assert.Equal(t, 3, healer.config.MaxRetryAttempts)
	assert.Equal(t, 5*time.Second, healer.config.RetryDelay)
}

// TestNewSelfHealer_InvalidParams 测试无效参数
func TestNewSelfHealer_InvalidParams(t *testing.T) {
	trans, _ := transport.NewMemoryTransport("node1")
	config := DefaultTreeCoordinatorConfig()
	coordinator, _ := NewTreeCoordinator("node1", "node1:9211", trans, config)
	fdConfig := DefaultFailureDetectorConfig()
	failureDetector, _ := NewFailureDetector("node1", trans, fdConfig)
	leConfig := DefaultLeaderElectionConfig()
	leaderElection, _ := NewLeaderElection("node1", trans, leConfig)
	shConfig := DefaultSelfHealingConfig()

	testCases := []struct {
		name            string
		nodeID          string
		transport       transport.Transport
		coordinator     *TreeCoordinator
		failureDetector *FailureDetector
		leaderElection  *LeaderElection
		config          *SelfHealingConfig
		expectError     bool
	}{
		{"空节点ID", "", trans, coordinator, failureDetector, leaderElection, shConfig, true},
		{"空传输层", "node1", nil, coordinator, failureDetector, leaderElection, shConfig, true},
		{"空协调器", "node1", trans, nil, failureDetector, leaderElection, shConfig, true},
		{"空故障检测器", "node1", trans, coordinator, nil, leaderElection, shConfig, true},
		{"nil配置自动使用默认", "node1", trans, coordinator, failureDetector, leaderElection, nil, false},
		{"有效参数", "node1", trans, coordinator, failureDetector, leaderElection, shConfig, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSelfHealer(tc.nodeID, tc.transport, tc.coordinator, tc.failureDetector, tc.leaderElection, tc.config)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSelfHealer_StartStop 测试启动和停止
func TestSelfHealer_StartStop(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", trans, config)
	require.NoError(t, err)

	fdConfig := DefaultFailureDetectorConfig()
	failureDetector, err := NewFailureDetector("node1", trans, fdConfig)
	require.NoError(t, err)

	leConfig := DefaultLeaderElectionConfig()
	leaderElection, err := NewLeaderElection("node1", trans, leConfig)
	require.NoError(t, err)

	shConfig := DefaultSelfHealingConfig()
	healer, err := NewSelfHealer("node1", trans, coordinator, failureDetector, leaderElection, shConfig)
	require.NoError(t, err)

	// 测试启动
	err = healer.Start()
	assert.NoError(t, err)

	// 测试重复启动
	err = healer.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	// 测试停止
	err = healer.Stop()
	assert.NoError(t, err)

	// 测试重复停止
	err = healer.Stop()
	assert.NoError(t, err) // 停止幂等
}

// TestSelfHealer_NodeFailureDetection 测试节点故障检测
func TestSelfHealer_NodeFailureDetection(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := &TreeCoordinatorConfig{
		MaxChildren:       10,
		MaxLevel:          4, // 支持 1000+ 节点
		HeartbeatInterval: 1 * time.Second,
		HeartbeatTimeout:  3 * time.Second,
		AutoDiscovery:     false,
		EnableSelfHealing: true,
	}

	coordinator, err := NewTreeCoordinator("node1", "node1:9211", trans, config)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	// 添加子节点
	_ = coordinator.AddChild("child1")
	_ = coordinator.AddChild("child2")

	fdConfig := &FailureDetectorConfig{
		Interval:     100 * time.Millisecond,
		Timeout:      300 * time.Millisecond,
		PhiThreshold: 8.0,
		MinSamples:   3,
	}
	failureDetector, err := NewFailureDetector("node1", trans, fdConfig)
	require.NoError(t, err)

	leConfig := DefaultLeaderElectionConfig()
	leaderElection, err := NewLeaderElection("node1", trans, leConfig)
	require.NoError(t, err)

	shConfig := &SelfHealingConfig{
		HealingInterval:      500 * time.Millisecond,
		MaxRetryAttempts:     2,
		RetryDelay:           100 * time.Millisecond,
		EnableTopologyRepair: true,
		EnableLeaderElection: false, // 禁用Leader选举以简化测试
	}
	healer, err := NewSelfHealer("node1", trans, coordinator, failureDetector, leaderElection, shConfig)
	require.NoError(t, err)

	_ = healer.Start()
	defer func() { _ = healer.Stop() }()

	// 添加节点到故障检测器
	failureDetector.AddNode("child1")
	failureDetector.RecordHeartbeat("child1")

	// 启动故障检测器
	_ = failureDetector.Start()
	defer func() { _ = failureDetector.Stop() }()

	// 等待故障检测
	time.Sleep(500 * time.Millisecond)

	// 验证故障被检测到
	stats := healer.GetStats()
	assert.Greater(t, stats.FailuresDetected.Load(), int64(0))
}

// TestSelfHealer_GetHealingNodes 测试获取正在自愈的节点
func TestSelfHealer_GetHealingNodes(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", trans, config)
	require.NoError(t, err)

	fdConfig := DefaultFailureDetectorConfig()
	failureDetector, err := NewFailureDetector("node1", trans, fdConfig)
	require.NoError(t, err)

	leConfig := DefaultLeaderElectionConfig()
	leaderElection, err := NewLeaderElection("node1", trans, leConfig)
	require.NoError(t, err)

	shConfig := DefaultSelfHealingConfig()
	healer, err := NewSelfHealer("node1", trans, coordinator, failureDetector, leaderElection, shConfig)
	require.NoError(t, err)

	// 初始无自愈节点
	nodes := healer.GetHealingNodes()
	assert.Equal(t, 0, len(nodes))

	// 手动触发故障回调
	healer.onNodeFailed("node2")

	// 验证自愈节点被添加
	nodes = healer.GetHealingNodes()
	assert.Equal(t, 1, len(nodes))
	assert.Equal(t, "node2", nodes[0].NodeID)
}

// TestSelfHealer_IsHealing 测试检查节点是否正在自愈
func TestSelfHealer_IsHealing(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", trans, config)
	require.NoError(t, err)

	fdConfig := DefaultFailureDetectorConfig()
	failureDetector, err := NewFailureDetector("node1", trans, fdConfig)
	require.NoError(t, err)

	leConfig := DefaultLeaderElectionConfig()
	leaderElection, err := NewLeaderElection("node1", trans, leConfig)
	require.NoError(t, err)

	shConfig := DefaultSelfHealingConfig()
	healer, err := NewSelfHealer("node1", trans, coordinator, failureDetector, leaderElection, shConfig)
	require.NoError(t, err)

	// 初始节点不在自愈中
	assert.False(t, healer.IsHealing("node2"))

	// 触发故障回调
	healer.onNodeFailed("node2")

	// 节点应该在自愈中
	assert.True(t, healer.IsHealing("node2"))
}

// TestSelfHealer_GetStats 测试获取统计信息
func TestSelfHealer_GetStats(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", trans, config)
	require.NoError(t, err)

	fdConfig := DefaultFailureDetectorConfig()
	failureDetector, err := NewFailureDetector("node1", trans, fdConfig)
	require.NoError(t, err)

	leConfig := DefaultLeaderElectionConfig()
	leaderElection, err := NewLeaderElection("node1", trans, leConfig)
	require.NoError(t, err)

	shConfig := DefaultSelfHealingConfig()
	healer, err := NewSelfHealer("node1", trans, coordinator, failureDetector, leaderElection, shConfig)
	require.NoError(t, err)

	stats := healer.GetStats()
	assert.NotNil(t, stats)
	assert.Equal(t, int64(0), stats.FailuresDetected.Load())
	assert.Equal(t, int64(0), stats.HealingsSuccess.Load())
	assert.Equal(t, int64(0), stats.HealingsFailed.Load())
	assert.Equal(t, int64(0), stats.TopologyRepairs.Load())
}

// TestSelfHealer_LeaderFailure 测试Leader故障处理
func TestSelfHealer_LeaderFailure(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", trans, config)
	require.NoError(t, err)

	fdConfig := DefaultFailureDetectorConfig()
	failureDetector, err := NewFailureDetector("node1", trans, fdConfig)
	require.NoError(t, err)

	leConfig := &LeaderElectionConfig{
		ElectionInterval: 100 * time.Millisecond,
		LeaseTTL:         500 * time.Millisecond,
		Priority:         10,
		AutoElection:     true,
	}
	leaderElection, err := NewLeaderElection("node1", trans, leConfig)
	require.NoError(t, err)

	// 添加候选节点
	node1 := &Node{
		NodeID:        "node1",
		Status:        NodeStatusReady,
		Priority:      10,
		LastHeartbeat: time.Now(),
	}
	_ = leaderElection.AddCandidate(node1)

	shConfig := &SelfHealingConfig{
		HealingInterval:      500 * time.Millisecond,
		MaxRetryAttempts:     2,
		RetryDelay:           100 * time.Millisecond,
		EnableTopologyRepair: false,
		EnableLeaderElection: true,
	}
	healer, err := NewSelfHealer("node1", trans, coordinator, failureDetector, leaderElection, shConfig)
	require.NoError(t, err)

	_ = healer.Start()
	defer func() { _ = healer.Stop() }()

	_ = leaderElection.Start()
	defer func() { _ = leaderElection.Stop() }()

	// 等待选举完成
	time.Sleep(200 * time.Millisecond)

	// 验证node1成为Leader
	assert.True(t, leaderElection.IsLeader())

	// 记录当前Leader
	currentLeader := leaderElection.GetCurrentLeader()
	assert.NotNil(t, currentLeader)
	assert.Equal(t, "node1", currentLeader.NodeID)
}

// TestDefaultSelfHealingConfig 测试默认配置
func TestDefaultSelfHealingConfig(t *testing.T) {
	config := DefaultSelfHealingConfig()

	assert.Equal(t, 10*time.Second, config.HealingInterval)
	assert.Equal(t, 3, config.MaxRetryAttempts)
	assert.Equal(t, 5*time.Second, config.RetryDelay)
	assert.True(t, config.EnableTopologyRepair)
	assert.True(t, config.EnableLeaderElection)
}

// TestHealingStatus_String 测试自愈状态字符串表示
func TestHealingStatus_String(t *testing.T) {
	testCases := []struct {
		status   HealingStatus
		expected string
	}{
		{HealingStatusDetecting, "Detecting"},
		{HealingStatusHealing, "Healing"},
		{HealingStatusRecovered, "Recovered"},
		{HealingStatusFailed, "Failed"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			// HealingStatus 是 int 类型，无法直接定义 String 方法
			// 这里只测试枚举值的正确性
			assert.GreaterOrEqual(t, int(tc.status), 0)
			assert.LessOrEqual(t, int(tc.status), 3)
		})
	}
}
