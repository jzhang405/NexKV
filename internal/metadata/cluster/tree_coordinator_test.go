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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)

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
			_, err := NewTreeCoordinator(tc.nodeID, tc.addr, config, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
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

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)

	_ = coordinator.Start()
	defer func() { _ = coordinator.Stop() }()

	// 初始深度为 0（只有根节点）
	depth := coordinator.GetTreeDepth()
	assert.Equal(t, 0, depth)

	// 添加子节点
	_ = coordinator.AddChild("child1")

	// 深度变为 1（AddChild 在 allNodes 中创建了子节点，Level=1）
	depth = coordinator.GetTreeDepth()
	assert.Equal(t, 1, depth)
}

// TestTreeCoordinator_GetStats 测试获取统计信息
func TestTreeCoordinator_GetStats(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
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
		HostID: "server-1",
		Role:   LeafParent,
		NodeAddr: NodeAddress{
			Host:    "127.0.0.1",
			TCPPort: 5001,
			UDPPort: 5002,
		},
		HostStatus: HostStatusOnline,
	}

	assert.Equal(t, "server-1", host.HostID)
	assert.Equal(t, LeafParent, host.Role)
	assert.Equal(t, "127.0.0.1", host.NodeAddr.Host)
	assert.Equal(t, 5001, host.NodeAddr.TCPPort)
	assert.Equal(t, HostStatusOnline, host.HostStatus)
}

// TestHost_ValidateNodeIDs 测试 Host ValidateNodeIDs 方法
func TestHost_ValidateNodeIDs(t *testing.T) {
	testCases := []struct {
		name        string
		host        *Host
		expectError bool
	}{
		{
			name: "LeafOnly - 有效配置",
			host: &Host{
				HostID:     "server-1",
				Role:       LeafOnly,
				LeafNodeID: "leaf-1",
			},
			expectError: false,
		},
		{
			name: "LeafOnly - 缺少 LeafNodeID",
			host: &Host{
				HostID: "server-1",
				Role:   LeafOnly,
			},
			expectError: true,
		},
		{
			name: "LeafParent - 有效配置",
			host: &Host{
				HostID:       "server-1",
				Role:         LeafParent,
				LeafNodeID:   "leaf-1",
				ParentNodeID: "parent-1",
			},
			expectError: false,
		},
		{
			name: "LeafParent - 缺少 ParentNodeID",
			host: &Host{
				HostID:     "server-1",
				Role:       LeafParent,
				LeafNodeID: "leaf-1",
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.host.ValidateNodeIDs()
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestTreeCoordinator_SingleParentConstraint 测试单父节点约束
// 验证一个真实节点只能有一个 ParentID
func TestTreeCoordinator_SingleParentConstraint(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()

	// 创建第一个协调器 node1
	coordinator1, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)
	_ = coordinator1.Start()
	t.Cleanup(func() { require.NoError(t, coordinator1.Stop()) })

	// 创建第二个协调器 node2
	coordinator2, err := NewTreeCoordinator("node2", "127.0.0.2:9211", config, nil)
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

// Package cluster TreeCoordinator Gossip 协议测试
//
// 测试覆盖：
// - buildTopologyMetadata: 构造拓扑元数据
// - gossipTopologyChange: 拓扑变更扩散（无 RPCClient 场景）
// - gossipSync: 周期性同步（无 RPCClient 场景）

// ============================================================================
// buildTopologyMetadata 测试
// ============================================================================

// Test_TreeCoordinator_buildTopologyMetadata 测试构造拓扑元数据
func Test_TreeCoordinator_buildTopologyMetadata(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)

	// 添加子节点
	err = coordinator.AddChild("child1")
	require.NoError(t, err)
	err = coordinator.AddChild("child2")
	require.NoError(t, err)

	// 调用 buildTopologyMetadata
	metadata := coordinator.buildTopologyMetadata()

	// 验证包含本地节点
	assert.Contains(t, metadata, "node1")
	localNodeData := string(metadata["node1"])
	assert.Contains(t, localNodeData, "node1")
	assert.Contains(t, localNodeData, "/ip4/127.0.0.1/tcp/9211")
	assert.Contains(t, localNodeData, "Init") // 新创建的 coordinator 状态为 Init

	// 验证包含子节点
	assert.Contains(t, metadata, "child1")
	assert.Contains(t, metadata, "child2")

	// 验证子节点数据格式
	child1Data := string(metadata["child1"])
	assert.Contains(t, child1Data, "child1")
	assert.Contains(t, child1Data, "node1") // ParentID
	assert.Contains(t, child1Data, "1")     // Level
}

// Test_TreeCoordinator_buildTopologyMetadata_EmptyChildren 测试没有子节点的情况
func Test_TreeCoordinator_buildTopologyMetadata_EmptyChildren(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)

	// 没有子节点
	metadata := coordinator.buildTopologyMetadata()

	// 只包含本地节点
	assert.Len(t, metadata, 1)
	assert.Contains(t, metadata, "node1")
}

// Test_TreeCoordinator_buildTopologyMetadata_ChildNotInAllNodes 测试子节点不在 allNodes 的情况
func Test_TreeCoordinator_buildTopologyMetadata_ChildNotInAllNodes(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)

	// 手动添加子节点 ID（不通过 AddChild）
	coordinator.nodesMu.Lock()
	coordinator.localNode.ChildrenIDs = append(coordinator.localNode.ChildrenIDs, "child1")
	coordinator.nodesMu.Unlock()

	// 调用 buildTopologyMetadata
	metadata := coordinator.buildTopologyMetadata()

	// 只包含本地节点（child1 不在 allNodes 中）
	assert.Len(t, metadata, 1)
	assert.Contains(t, metadata, "node1")
	assert.NotContains(t, metadata, "child1")
}

// ============================================================================
// gossipTopologyChange 测试（无 RPCClient 场景）
// ============================================================================

// Test_TreeCoordinator_gossipTopologyChange_NoRPCClient 测试没有 RPCClient 的情况
func Test_TreeCoordinator_gossipTopologyChange_NoRPCClient(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)
	// RPCClient 已删除（PR-Libp2p-TransportCleanup）

	// 不应该 panic
	assert.NotPanics(t, func() {
		coordinator.gossipTopologyChange("add", "node2", "node1", 1)
	})
}

// Test_TreeCoordinator_gossipTopologyChange_NoOtherNodes 测试没有其他节点的情况
func Test_TreeCoordinator_gossipTopologyChange_NoOtherNodes(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)

	// 不应该 panic（没有其他节点）
	assert.NotPanics(t, func() {
		coordinator.gossipTopologyChange("add", "node2", "node1", 1)
	})
}

// Test_TreeCoordinator_gossipTopologyChange_WithOtherNodes 测试有其他节点但无 RPCClient
func Test_TreeCoordinator_gossipTopologyChange_WithOtherNodes(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)

	// 添加一些节点到 allNodes（模拟其他节点）
	coordinator.nodesMu.Lock()
	coordinator.allNodes["node2"] = &Node{
		NodeID: "node2",
		Addr:   NodeAddress{Host: "127.0.0.1", TCPPort: 9212},
		Status: NodeStatusReady,
		Level:  1,
	}
	coordinator.allNodes["node3"] = &Node{
		NodeID: "node3",
		Addr:   NodeAddress{Host: "127.0.0.1", TCPPort: 9213},
		Status: NodeStatusReady,
		Level:  1,
	}
	coordinator.nodesMu.Unlock()

	// RPCClient 为 nil，不应该 panic
	assert.NotPanics(t, func() {
		coordinator.gossipTopologyChange("add", "node4", "node1", 1)
	})
}

// ============================================================================
// gossipSync 测试（无 RPCClient 场景）
// ============================================================================

// Test_TreeCoordinator_gossipSync_NoRPCClient 测试没有 RPCClient 的情况
func Test_TreeCoordinator_gossipSync_NoRPCClient(t *testing.T) {
	t.Skip("TODO: PR-034 完成后启用此测试（gossipSync 函数已注释）")
	// 以下代码在 PR-034 完成后启用
	/*
		config := DefaultTreeCoordinatorConfig()
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
		require.NoError(t, err)
		// RPCClient 已删除（PR-Libp2p-TransportCleanup）

		// 不应该 panic
		assert.NotPanics(t, func() {
			coordinator.gossipSync()
		})
	*/
}

// Test_TreeCoordinator_gossipSync_NoOtherNodes 测试没有其他节点的情况
func Test_TreeCoordinator_gossipSync_NoOtherNodes(t *testing.T) {
	t.Skip("TODO: PR-034 完成后启用此测试（gossipSync 函数已注释）")
	// 以下代码在 PR-034 完成后启用
	/*
		config := DefaultTreeCoordinatorConfig()
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
		require.NoError(t, err)

		// 不应该 panic（没有其他节点）
		assert.NotPanics(t, func() {
			coordinator.gossipSync()
		})
	*/
}

// Test_TreeCoordinator_gossipSync_WithOtherNodes 测试有其他节点但无 RPCClient
func Test_TreeCoordinator_gossipSync_WithOtherNodes(t *testing.T) {
	t.Skip("TODO: PR-034 完成后启用此测试（gossipSync 函数已注释）")
	// 以下代码在 PR-034 完成后启用
	/*
		config := DefaultTreeCoordinatorConfig()
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
		require.NoError(t, err)

		// 添加一些节点到 allNodes
		coordinator.nodesMu.Lock()
		coordinator.allNodes["node2"] = &Node{
			NodeID: "node2",
			Addr:   NodeAddress{Host: "127.0.0.1", TCPPort: 9212},
			Status: NodeStatusReady,
			Level:  1,
		}
		coordinator.nodesMu.Unlock()

		// RPCClient 为 nil，不应该 panic
		assert.NotPanics(t, func() {
			coordinator.gossipSync()
		})
	*/
}

// ============================================================================
// Gossip 消息构造测试
// ============================================================================

// Test_TreeCoordinator_buildTopologyMetadata_Format 测试元数据格式
func Test_TreeCoordinator_buildTopologyMetadata_Format(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)

	// 添加子节点
	err = coordinator.AddChild("child1")
	require.NoError(t, err)

	// 调用 buildTopologyMetadata
	metadata := coordinator.buildTopologyMetadata()

	// 验证本地节点格式：node1|/ip4/127.0.0.1/tcp/9211||0|Init
	// 注意：新创建的 coordinator 状态是 Init
	localNodeData := string(metadata["node1"])
	parts := splitString(localNodeData, "|")
	assert.Len(t, parts, 5)
	assert.Equal(t, "node1", parts[0])                   // NodeID
	assert.Equal(t, "/ip4/127.0.0.1/tcp/9211", parts[1]) // TCPAddr
	assert.Equal(t, "", parts[2])                        // ParentID (根节点)
	assert.Equal(t, "0", parts[3])                       // Level
	assert.Equal(t, "Init", parts[4])                    // Status (初始状态)

	// 验证子节点格式：child1|/ip4//tcp/0|node1|1|Ready
	// 注意：AddChild 创建的子节点 Addr 为空，String() 返回 "/ip4//tcp/0"
	// 子节点状态被设置为 Ready
	child1Data := string(metadata["child1"])
	parts = splitString(child1Data, "|")
	assert.Len(t, parts, 5)
	assert.Equal(t, "child1", parts[0])      // NodeID
	assert.Equal(t, "/ip4//tcp/0", parts[1]) // TCPAddr (空 Addr 的 String() 表示)
	assert.Equal(t, "node1", parts[2])       // ParentID
	assert.Equal(t, "1", parts[3])           // Level
	assert.Equal(t, "Ready", parts[4])       // Status (AddChild 设置为 Ready)
}

// splitString 辅助函数：按分隔符分割字符串
func splitString(s, sep string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if i+len(sep) <= len(s) && s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	result = append(result, s[start:])
	return result
}
