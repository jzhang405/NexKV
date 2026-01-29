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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)

	assert.NoError(t, err)
	assert.NotNil(t, coordinator)
	assert.Equal(t, "node1", coordinator.localNode.NodeID)
	assert.Equal(t, "127.0.0.1", coordinator.localNode.Addr.Host)
	assert.Equal(t, 9211, coordinator.localNode.Addr.TCPPort)
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
		{"有效参数", "node1", "127.0.0.1:9211", false},
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
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

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	// 获取本地节点
	node, err := coordinator.GetNode("node1")
	assert.NoError(t, err)
	assert.Equal(t, "node1", node.NodeID)
	assert.Equal(t, "127.0.0.1", node.Addr.Host)
	assert.Equal(t, 9211, node.Addr.TCPPort)

	// 获取不存在的节点
	_, err = coordinator.GetNode("notexist")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestTreeCoordinator_ListNodes 测试列出所有节点
func TestTreeCoordinator_ListNodes(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
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

// ========================================
// 新增数据结构测试（Host+Node 双层架构）
// ========================================

// TestNodeAddress_TCPAddr 测试 TCPAddr 方法
func TestNodeAddress_TCPAddr(t *testing.T) {
	addr := &NodeAddress{
		Host:    "127.0.0.1",
		TCPPort: 5001,
		UDPPort: 5002,
	}
	result := addr.TCPAddr()
	assert.Equal(t, "/ip4/127.0.0.1/tcp/5001", result)
}

// TestNodeAddress_UDPAddr 测试 UDPAddr 方法
func TestNodeAddress_UDPAddr(t *testing.T) {
	addr := &NodeAddress{
		Host:    "192.168.1.100",
		TCPPort: 5001,
		UDPPort: 5002,
	}
	result := addr.UDPAddr()
	assert.Equal(t, "/ip4/192.168.1.100/udp/5002", result)
}

// TestParseNodeAddress_IPFS_TCP 测试解析 IPFS 风格 TCP 地址
func TestParseNodeAddress_IPFS_TCP(t *testing.T) {
	result, err := ParseNodeAddress("/ip4/127.0.0.1/tcp/5001")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "127.0.0.1", result.Host)
	assert.Equal(t, 5001, result.TCPPort)
	assert.Equal(t, 0, result.UDPPort)
}

// TestParseNodeAddress_IPFS_UDP 测试解析 IPFS 风格 UDP 地址
func TestParseNodeAddress_IPFS_UDP(t *testing.T) {
	result, err := ParseNodeAddress("/ip4/192.168.1.100/udp/6002")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "192.168.1.100", result.Host)
	assert.Equal(t, 0, result.TCPPort)
	assert.Equal(t, 6002, result.UDPPort)
}

// TestParseNodeAddress_SimpleFormat 测试解析简化格式（默认 TCP）
func TestParseNodeAddress_SimpleFormat(t *testing.T) {
	result, err := ParseNodeAddress("127.0.0.1:5001")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "127.0.0.1", result.Host)
	assert.Equal(t, 5001, result.TCPPort)
	assert.Equal(t, 0, result.UDPPort)
}

// TestParseNodeAddress_EmptyString 测试空地址字符串
func TestParseNodeAddress_EmptyString(t *testing.T) {
	result, err := ParseNodeAddress("")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "不能为空")
}

// TestParseNodeAddress_InvalidIPFS_MissingParts 测试无效的 IPFS 格式（缺少部分）
func TestParseNodeAddress_InvalidIPFS_MissingParts(t *testing.T) {
	result, err := ParseNodeAddress("/ip4/127.0.0.1")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "无效的 IPFS 地址格式")
}

// TestParseNodeAddress_InvalidIPFS_MissingProtocol 测试无效的协议格式
func TestParseNodeAddress_InvalidIPFS_MissingProtocol(t *testing.T) {
	result, err := ParseNodeAddress("/ip4/127.0.0.1/5001")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "无效的协议格式")
}

// TestParseNodeAddress_InvalidPort 测试无效的端口号
func TestParseNodeAddress_InvalidPort(t *testing.T) {
	result, err := ParseNodeAddress("127.0.0.1:abc")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "无效的端口号")
}

// TestParseNodeAddress_InvalidSimpleFormat 测试无效的简化格式（缺少冒号）
func TestParseNodeAddress_InvalidSimpleFormat(t *testing.T) {
	result, err := ParseNodeAddress("127.0.0.1-5001")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "无效的地址格式")
}

// TestHostRole_String 测试 HostRole 字符串表示
func TestHostRole_String(t *testing.T) {
	testCases := []struct {
		role     HostRole
		expected string
	}{
		{LeafOnly, "leaf_only"},
		{LeafParent, "leaf_parent"},
		{LeafParentStandby, "leaf_parent_standby"},
		{HostRole(99), "unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := tc.role.String()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestNodeRole_String 测试 NodeRole 字符串表示
func TestNodeRole_String(t *testing.T) {
	testCases := []struct {
		role     NodeRole
		expected string
	}{
		{Leaf, "leaf"},
		{Parent, "parent"},
		{ParentStandby, "parent_standby"},
		{NodeRole(99), "unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := tc.role.String()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestHost_Structure 测试 Host 结构
func TestHost_Structure(t *testing.T) {
	host := &Host{
		ID:   "server-1",
		Role: LeafParent,
		NodeAddr: NodeAddress{
			Host:    "127.0.0.1",
			TCPPort: 5001,
			UDPPort: 5002,
		},
		Status: "active",
	}

	assert.Equal(t, "server-1", host.ID)
	assert.Equal(t, LeafParent, host.Role)
	assert.Equal(t, "127.0.0.1", host.NodeAddr.Host)
	assert.Equal(t, 5001, host.NodeAddr.TCPPort)
	assert.Equal(t, "active", host.Status)
}

// TestTreeCoordinator_SingleParentConstraint 测试单父节点约束
// 验证一个真实节点只能有一个 ParentID
func TestTreeCoordinator_SingleParentConstraint(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()

	// 创建第一个协调器 node1
	coordinator1, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	_ = coordinator1.Start()
	t.Cleanup(func() { require.NoError(t, coordinator1.Stop()) })

	// 创建第二个协调器 node2
	coordinator2, err := NewTreeCoordinator("node2", "127.0.0.2:9211", config)
	require.NoError(t, err)
	_ = coordinator2.Start()
	t.Cleanup(func() { require.NoError(t, coordinator2.Stop()) })

	// 创建一个共享的子节点信息（模拟全局节点视图）
	addr, err := ParseNodeAddress("child1:9211")
	require.NoError(t, err)

	sharedChild := &Node{
		NodeID:      "child1",
		Addr:        *addr,
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
