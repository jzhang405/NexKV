// Package cluster 树形协调器测试
package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// TreeCoordinator 测试
// ========================================

// TestNewTreeCoordinator 测试创建树形协调器
func TestNewTreeCoordinator(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)

	assert.NoError(t, err)
	assert.NotNil(t, coordinator)
	assert.Equal(t, "node1", coordinator.localNode.NodeID)
	assert.Equal(t, "node1:9211", coordinator.localNode.Addr)
	assert.Equal(t, 0, coordinator.localNode.Level)
	assert.Equal(t, NodeStatusInit, coordinator.localNode.Status)
	assert.Equal(t, int32(1), coordinator.stats.TotalNodes.Load())
}

// TestNewTreeCoordinator_InvalidParams 测试无效参数
func TestNewTreeCoordinator_InvalidParams(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()

	testCases := []struct {
		name        string
		nodeID      string
		addr        string
		expectError bool
	}{
		{"空节点ID", "", "node1:9211", true},
		{"空地址", "node1", "", true},
		{"有效参数", "node1", "node1:9211", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewTreeCoordinator(tc.nodeID, tc.addr, config)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestTreeCoordinator_StartStop 测试启动和停止
func TestTreeCoordinator_StartStop(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)

	// 测试启动
	err = coordinator.Start()
	assert.NoError(t, err)
	assert.True(t, coordinator.IsRunning())
	assert.Equal(t, NodeStatusReady, coordinator.localNode.Status)

	// 测试重复启动
	err = coordinator.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	// 测试停止
	err = coordinator.Stop()
	assert.NoError(t, err)
	assert.False(t, coordinator.IsRunning())

	// 测试重复停止
	err = coordinator.Stop()
	assert.NoError(t, err) // 停止幂等
}

// TestTreeCoordinator_AddChild 测试添加子节点
func TestTreeCoordinator_AddChild(t *testing.T) {

	config := &TreeCoordinatorConfig{
		MaxChildren:       2, // 限制为 2 个子节点
		MaxLevel:          3, // 允许 3 层深度
		HeartbeatInterval: 1 * time.Second,
		AutoDiscovery:     false, // 禁用自动发现
	}

	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	// 添加第一个子节点
	err = coordinator.AddChild("child1")
	assert.NoError(t, err)
	assert.Contains(t, coordinator.localNode.ChildrenIDs, "child1")
	assert.Equal(t, 1, len(coordinator.localNode.ChildrenIDs))

	// 添加第二个子节点
	err = coordinator.AddChild("child2")
	assert.NoError(t, err)
	assert.Contains(t, coordinator.localNode.ChildrenIDs, "child2")
	assert.Equal(t, 2, len(coordinator.localNode.ChildrenIDs))

	// 添加第三个子节点（超过限制）
	err = coordinator.AddChild("child3")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已达上限")

	// 测试添加重复的子节点（需要先移除一个子节点）
	_ = coordinator.RemoveChild("child2")
	err = coordinator.AddChild("child1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已存在")
}

// TestTreeCoordinator_RemoveChild 测试移除子节点
func TestTreeCoordinator_RemoveChild(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	// 添加子节点
	_ = coordinator.AddChild("child1")
	_ = coordinator.AddChild("child2")

	// 移除子节点
	err = coordinator.RemoveChild("child1")
	assert.NoError(t, err)
	assert.NotContains(t, coordinator.localNode.ChildrenIDs, "child1")
	assert.Equal(t, 1, len(coordinator.localNode.ChildrenIDs))

	// 移除不存在的子节点
	err = coordinator.RemoveChild("notexist")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestTreeCoordinator_GetNode 测试获取节点
func TestTreeCoordinator_GetNode(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	// 获取本地节点
	node, err := coordinator.GetNode("node1")
	assert.NoError(t, err)
	assert.Equal(t, "node1", node.NodeID)
	assert.Equal(t, "node1:9211", node.Addr)

	// 获取不存在的节点
	_, err = coordinator.GetNode("notexist")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestTreeCoordinator_ListNodes 测试列出所有节点
func TestTreeCoordinator_ListNodes(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	// 列出节点
	nodes := coordinator.ListNodes()
	assert.Equal(t, 1, len(nodes))
	assert.Equal(t, "node1", nodes[0].NodeID)
}

// TestTreeCoordinator_GetTreeDepth 测试获取树深度
func TestTreeCoordinator_GetTreeDepth(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	// 初始深度为 0（只有根节点）
	depth := coordinator.GetTreeDepth()
	assert.Equal(t, 0, depth)

	// 添加子节点
	_ = coordinator.AddChild("child1")

	// 深度仍为 0（子节点在本地列表中，但尚未创建完整树）
	depth = coordinator.GetTreeDepth()
	assert.Equal(t, 0, depth)
}

// TestTreeCoordinator_GetStats 测试获取统计信息
func TestTreeCoordinator_GetStats(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	stats := coordinator.GetStats()

	assert.NotNil(t, stats)
	assert.Equal(t, int32(1), stats.TotalNodes.Load())
	assert.Equal(t, int32(1), stats.OnlineNodes.Load())
	assert.Equal(t, int32(0), stats.OfflineNodes.Load())
	assert.Equal(t, int32(0), stats.TreeDepth.Load())
}

// TestTreeCoordinator_IsRunning 测试运行状态
func TestTreeCoordinator_IsRunning(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)

	// 未启动时
	assert.False(t, coordinator.IsRunning())

	// 启动后
	_ = coordinator.Start()
	assert.True(t, coordinator.IsRunning())

	// 停止后
	_ = coordinator.Stop()
	assert.False(t, coordinator.IsRunning())
}

// TestNodeStatus_String 测试节点状态字符串表示
func TestNodeStatus_String(t *testing.T) {
	testCases := []struct {
		status   NodeStatus
		expected string
	}{
		{NodeStatusInit, "Init"},
		{NodeStatusReady, "Ready"},
		{NodeStatusJoining, "Joining"},
		{NodeStatusLeaving, "Leaving"},
		{NodeStatusFailed, "Failed"},
		{NodeStatus(99), "Unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := tc.status.String()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestDefaultTreeCoordinatorConfig 测试默认配置
func TestDefaultTreeCoordinatorConfig(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()

	assert.Equal(t, 10, config.MaxChildren)
	assert.Equal(t, 5*time.Second, config.HeartbeatInterval)
	assert.Equal(t, 15*time.Second, config.HeartbeatTimeout)
	assert.True(t, config.AutoDiscovery)
	assert.True(t, config.EnableSelfHealing)
}

// TestTreeCoordinator_SingleParentConstraint 测试单父节点约束
// 验证一个真实节点只能有一个 ParentID
func TestTreeCoordinator_SingleParentConstraint(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()

	// 创建第一个协调器 node1
	coordinator1, err := NewTreeCoordinator("node1", "node1:9211", config)
	require.NoError(t, err)
	_ = coordinator1.Start()
	t.Cleanup(func() { require.NoError(t, coordinator1.Stop()) })

	// 创建第二个协调器 node2
	coordinator2, err := NewTreeCoordinator("node2", "node2:9211", config)
	require.NoError(t, err)
	_ = coordinator2.Start()
	t.Cleanup(func() { require.NoError(t, coordinator2.Stop()) })

	// 创建一个共享的子节点信息（模拟全局节点视图）
	sharedChild := &Node{
		NodeID:      "child1",
		Addr:        "child1:9211",
		ParentID:    "",
		ChildrenIDs: []string{},
		Level:       0,
		Status:      NodeStatusReady,
	}
	coordinator1.allNodes["child1"] = sharedChild
	coordinator2.allNodes["child1"] = sharedChild

	// node1 将 child1 作为子节点（应该成功）
	err = coordinator1.AddChild("child1")
	assert.NoError(t, err)

	// 验证 child1 的 ParentID 已设置
	assert.Equal(t, "node1", sharedChild.ParentID)

	// node2 尝试将 child1 作为子节点（应该失败）
	err = coordinator2.AddChild("child1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经是")
	assert.Contains(t, err.Error(), "node1")
	assert.Contains(t, err.Error(), "不能同时")
}
