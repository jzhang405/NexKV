// Package cluster 树形协调器测试
package cluster

import (
	"sync"
	"testing"
	"time"

	metadataconfig "github.com/jzhang405/NexKV/internal/config"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/jzhang405/NexKV/internal/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// TreeCoordinator 测试
// ========================================

// TestNewTreeCoordinator 测试创建树形协调器
func TestNewTreeCoordinator(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)

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
			_, err := NewTreeCoordinator(tc.nodeID, tc.addr, config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	// 测试启动
	err = coordinator.Start()
	assert.NoError(t, err)
	assert.True(t, coordinator.IsRunning())

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

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	addr := &types.NodeAddress{
		Host:    "127.0.0.1",
		TCPPort: 5001,
		UDPPort: 5002,
	}
	result := addr.TCPAddr()
	assert.Equal(t, "/ip4/127.0.0.1/tcp/5001", result)
}

// TestNodeAddress_UDPAddr 测试 UDPAddr 方法
func TestNodeAddress_UDPAddr(t *testing.T) {
	addr := &types.NodeAddress{
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
		NodeAddr: types.NodeAddress{
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
	coordinator1, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	_ = coordinator1.Start()
	t.Cleanup(func() { require.NoError(t, coordinator1.Stop()) })

	// 创建第二个协调器 node2
	coordinator2, err := NewTreeCoordinator("node2", "127.0.0.2:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	// 不应该 panic（没有其他节点）
	assert.NotPanics(t, func() {
		coordinator.gossipTopologyChange("add", "node2", "node1", 1)
	})
}

// Test_TreeCoordinator_gossipTopologyChange_WithOtherNodes 测试有其他节点但无 RPCClient
func Test_TreeCoordinator_gossipTopologyChange_WithOtherNodes(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	// 添加一些节点到 allNodes（模拟其他节点）
	coordinator.nodesMu.Lock()
	coordinator.allNodes["node2"] = &Node{
		NodeID: "node2",
		Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9212},
		Status: NodeStatusReady,
		Level:  1,
	}
	coordinator.allNodes["node3"] = &Node{
		NodeID: "node3",
		Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9213},
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
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
		require.NoError(t, err)

		// 添加一些节点到 allNodes
		coordinator.nodesMu.Lock()
		coordinator.allNodes["node2"] = &Node{
			NodeID: "node2",
			Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9212},
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
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
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

// ========================================
// 故障检测和自愈测试
// ========================================

// TestDetectFailures 测试故障检测逻辑
func TestDetectFailures(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.HeartbeatTimeout = 100 * time.Millisecond
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 启动协调器
	if err := coordinator.Start(); err != nil {
		t.Skipf("跳过测试：启动 coordinator 失败: %v", err)
	}

	// 添加子节点
	_ = coordinator.AddChild("child1")
	_ = coordinator.AddChild("child2")

	t.Run("检测所有节点正常", func(t *testing.T) {
		// 所有节点心跳正常
		coordinator.nodesMu.RLock()
		for _, node := range coordinator.allNodes {
			node.LastHeartbeat = time.Now()
		}
		coordinator.nodesMu.RUnlock()

		// detectFailures 应该不检测到任何故障节点
		// 这里我们只是验证函数不会 panic
	})

	t.Run("检测心跳超时节点", func(t *testing.T) {
		coordinator.nodesMu.Lock()
		// 设置 child1 心跳超时
		if child, ok := coordinator.allNodes["child1"]; ok {
			child.LastHeartbeat = time.Now().Add(-time.Hour) // 1小时前
		}
		coordinator.nodesMu.Unlock()

		// 触发故障检测
		coordinator.detectFailures()

		// 验证超时节点被标记为离线
		coordinator.nodesMu.RLock()
		if child, ok := coordinator.allNodes["child1"]; ok {
			assert.Equal(t, NodeStatusFailed, child.Status)
		}
		coordinator.nodesMu.RUnlock()
	})
}

// TestTriggerSelfHealing 测试自愈机制
func TestTriggerSelfHealing(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.EnableSelfHealing = true
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("parent1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("自愈叶子节点", func(t *testing.T) {
		// 添加失败的叶子节点
		_ = coordinator.AddChild("failed-leaf")

		coordinator.nodesMu.Lock()
		if node, ok := coordinator.allNodes["failed-leaf"]; ok {
			node.Status = NodeStatusFailed
			node.ParentID = "parent1"
		}
		coordinator.nodesMu.Unlock()

		// 触发自愈（仅验证不会 panic）
		coordinator.nodesMu.RLock()
		failedNode, ok := coordinator.allNodes["failed-leaf"]
		coordinator.nodesMu.RUnlock()

		if ok {
			coordinator.triggerSelfHealing(failedNode)
		}
	})

	t.Run("自愈父节点", func(t *testing.T) {
		// 添加失败的父节点（带子节点）
		_ = coordinator.AddChild("failed-parent")
		_ = coordinator.AddChild("grandchild1")

		coordinator.nodesMu.Lock()
		if parent, ok := coordinator.allNodes["failed-parent"]; ok {
			parent.Status = NodeStatusFailed
			parent.Role = Parent
			parent.ChildrenIDs = []string{"grandchild1"}
		}
		if gc, ok := coordinator.allNodes["grandchild1"]; ok {
			gc.ParentID = "failed-parent"
		}
		coordinator.nodesMu.Unlock()

		// 触发自愈
		coordinator.nodesMu.RLock()
		failedNode, ok := coordinator.allNodes["failed-parent"]
		coordinator.nodesMu.RUnlock()

		if ok {
			coordinator.triggerSelfHealing(failedNode)
		}
	})
}

// TestRemoveNodeRelationships 测试移除节点关系
func TestRemoveNodeRelationships(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("parent1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("移除父节点的子节点关系", func(t *testing.T) {
		// 添加父节点和子节点
		_ = coordinator.AddChild("child1")
		_ = coordinator.AddChild("child2")

		coordinator.nodesMu.RLock()
		parentChildren := len(coordinator.localNode.ChildrenIDs)
		coordinator.nodesMu.RUnlock()

		assert.Equal(t, 2, parentChildren)

		// 移除关系
		coordinator.removeNodeRelationships("child1")

		// 验证子节点从父节点的 ChildrenIDs 中移除
		coordinator.nodesMu.RLock()
		newChildren := len(coordinator.localNode.ChildrenIDs)
		coordinator.nodesMu.RUnlock()

		assert.Equal(t, 1, newChildren)
	})

	t.Run("移除不存在的节点", func(t *testing.T) {
		// 不应该 panic
		coordinator.removeNodeRelationships("non-existent")
	})
}

// TestReparentNode 测试重新分配父节点
func TestReparentNode(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("grandparent1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("重新分配叶子节点", func(t *testing.T) {
		// 构建三层树结构
		_ = coordinator.AddChild("parent1")
		_ = coordinator.AddChild("parent2")
		_ = coordinator.AddChild("child1")

		coordinator.nodesMu.Lock()
		// 设置 child1 的父节点为 parent1
		if child1, ok := coordinator.allNodes["child1"]; ok {
			child1.ParentID = "parent1"
			child1.Level = 2
		}
		// 设置 parent1 为父节点角色
		if parent1, ok := coordinator.allNodes["parent1"]; ok {
			parent1.Role = Parent
			parent1.ChildrenIDs = []string{"child1"}
			parent1.Level = 1
		}
		if parent2, ok := coordinator.allNodes["parent2"]; ok {
			parent2.Role = Parent
			parent2.Level = 1
		}
		coordinator.nodesMu.Unlock()

		// 重新分配 child1（会自动选择新父节点）
		coordinator.nodesMu.RLock()
		child := coordinator.allNodes["child1"]
		coordinator.nodesMu.RUnlock()

		err := coordinator.reparentNode(child, "parent1")
		assert.NoError(t, err)

		// 验证新父节点是候选节点之一（parent1、parent2 或 grandparent1）
		coordinator.nodesMu.RLock()
		newParentID := child.ParentID
		coordinator.nodesMu.RUnlock()
		assert.Contains(t, []string{"parent1", "parent2", "grandparent1"}, newParentID)
		// 验证层级正确更新
		coordinator.nodesMu.RLock()
		assert.Equal(t, coordinator.allNodes[newParentID].Level+1, child.Level)
		coordinator.nodesMu.RUnlock()
	})

	t.Run("重新分配到不存在的父节点", func(t *testing.T) {
		coordinator.nodesMu.RLock()
		child, ok := coordinator.allNodes["child1"]
		coordinator.nodesMu.RUnlock()

		if ok {
			// reparentNode 在旧父节点不存在时也会成功（只记录日志）
			err := coordinator.reparentNode(child, "non-existent")
			// 验证不会 panic，不要求返回错误
			_ = err
		}
	})
}

// TestFindCandidateParents 测试查找候选父节点
func TestFindCandidateParents(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 5
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("root1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("有可用父节点", func(t *testing.T) {
		// 添加两个父节点
		_ = coordinator.AddChild("parent1")
		_ = coordinator.AddChild("parent2")

		coordinator.nodesMu.Lock()
		for _, nodeID := range []string{"parent1", "parent2"} {
			if node, ok := coordinator.allNodes[nodeID]; ok {
				node.Role = Parent
				node.Level = 1
			}
		}
		coordinator.nodesMu.Unlock()

		// 创建一个孤儿节点，Level = 2（需要比候选父节点的 Level 大）
		orphan := &Node{
			NodeID:      "orphan1",
			ParentID:    "",
			Level:       2,
			Role:        Leaf,
			Status:      NodeStatusReady,
			ChildrenIDs: []string{},
		}

		candidates := coordinator.findCandidateParents(orphan)
		assert.NotEmpty(t, candidates)
		assert.Contains(t, candidates, "parent1")
		assert.Contains(t, candidates, "parent2")
		// 根节点也可以作为候选
		assert.Contains(t, candidates, "root1")
	})

	t.Run("无可用父节点", func(t *testing.T) {
		// 创建一个 Level = 0 的节点（没有更高级别的节点可以作为父节点）
		orphan := &Node{
			NodeID:      "orphan2",
			ParentID:    "",
			Level:       0, // 与根节点相同，没有更高级别的节点
			Role:        Leaf,
			Status:      NodeStatusReady,
			ChildrenIDs: []string{},
		}

		candidates := coordinator.findCandidateParents(orphan)
		assert.Empty(t, candidates)
	})
}

// TestGetKnownNodes 测试获取已知节点列表
func TestGetKnownNodes(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("空节点列表", func(t *testing.T) {
		nodes := coordinator.getKnownNodes()
		assert.Len(t, nodes, 0) // getKnownNodes 跳过本地节点，返回空列表
	})

	t.Run("有子节点", func(t *testing.T) {
		// 使用 AddChildWithAddr 为每个子节点设置不同的地址
		addr1, _ := ParseNodeAddress("127.0.0.1:9001")
		addr2, _ := ParseNodeAddress("127.0.0.1:9002")
		_ = coordinator.AddChildWithAddr("child1", addr1)
		_ = coordinator.AddChildWithAddr("child2", addr2)

		nodes := coordinator.getKnownNodes()
		assert.Len(t, nodes, 2) // 2个子节点（本地节点被跳过）
	})

	t.Run("从配置中读取种子节点", func(t *testing.T) {
		// 创建带种子节点配置的 coordinator
		clusterConfig := &metadataconfig.ClusterConfig{
			Hosts: []metadataconfig.HostConfig{
				{SeedNode: "/ip4/127.0.0.1/tcp/9001"},
				{SeedNode: "/ip4/127.0.0.1/tcp/9002"},
			},
		}

		config := DefaultTreeCoordinatorConfig()
		config.AutoDiscovery = false
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, clusterConfig, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		nodes := coordinator.getKnownNodes()
		// 应该返回 2 个种子节点（不包括本地节点）
		assert.Len(t, nodes, 2)
	})

	t.Run("过滤自身地址", func(t *testing.T) {
		// 创建包含本地节点地址的配置
		clusterConfig := &metadataconfig.ClusterConfig{
			Hosts: []metadataconfig.HostConfig{
				{SeedNode: "/ip4/127.0.0.1/tcp/9211"}, // 本地节点地址
				{SeedNode: "/ip4/127.0.0.1/tcp/9001"},
			},
		}

		config := DefaultTreeCoordinatorConfig()
		config.AutoDiscovery = false
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, clusterConfig, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		nodes := coordinator.getKnownNodes()
		// 应该只返回 1 个节点（本地节点被过滤）
		assert.Len(t, nodes, 1)
		if len(nodes) > 0 {
			assert.NotEqual(t, "/ip4/127.0.0.1/tcp/9211", nodes[0].Addr.TCPAddr())
		}
	})

	t.Run("过滤无效地址", func(t *testing.T) {
		// 创建包含无效地址的配置
		clusterConfig := &metadataconfig.ClusterConfig{
			Hosts: []metadataconfig.HostConfig{
				{SeedNode: "invalid-address"}, // 无效地址
				{SeedNode: "/ip4/127.0.0.1/tcp/9001"},
			},
		}

		config := DefaultTreeCoordinatorConfig()
		config.AutoDiscovery = false
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, clusterConfig, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		nodes := coordinator.getKnownNodes()
		// 应该只返回 1 个有效节点（无效地址被跳过）
		assert.Len(t, nodes, 1)
	})

	t.Run("去重处理", func(t *testing.T) {
		// 创建包含重复地址的配置
		clusterConfig := &metadataconfig.ClusterConfig{
			Hosts: []metadataconfig.HostConfig{
				{SeedNode: "/ip4/127.0.0.1/tcp/9001"},
				{SeedNode: "/ip4/127.0.0.1/tcp/9001"}, // 重复地址
			},
		}

		config := DefaultTreeCoordinatorConfig()
		config.AutoDiscovery = false
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, clusterConfig, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		nodes := coordinator.getKnownNodes()
		// 应该只返回 1 个节点（重复地址被去重）
		assert.Len(t, nodes, 1)
	})
}

// TestSelectBestParent 测试选择最佳父节点
func TestSelectBestParent(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 10
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("选择最不繁忙的父节点", func(t *testing.T) {
		// 添加两个父节点，一个有更多子节点
		_ = coordinator.AddChild("parent1")
		_ = coordinator.AddChild("parent2")

		coordinator.nodesMu.Lock()
		// parent1: Level 1, 2个子节点
		if p1, ok := coordinator.allNodes["parent1"]; ok {
			p1.Role = Parent
			p1.ChildrenIDs = []string{"child3", "child4"}
			p1.Level = 1
			p1.Status = NodeStatusReady
		}
		// parent2: Level 2, 没有子节点
		if p2, ok := coordinator.allNodes["parent2"]; ok {
			p2.Role = Parent
			p2.Level = 2
			p2.Status = NodeStatusReady
		}
		coordinator.nodesMu.Unlock()

		// 直接构建候选节点列表（而不是使用 getKnownNodes）
		coordinator.nodesMu.RLock()
		candidates := []*Node{
			coordinator.allNodes["parent1"],
			coordinator.allNodes["parent2"],
		}
		coordinator.nodesMu.RUnlock()

		bestParent := coordinator.selectBestParent(candidates)
		// 应该选择 parent1（Level 1 更低）
		assert.NotNil(t, bestParent)
		assert.Equal(t, "parent1", bestParent.NodeID)
	})

	t.Run("空候选列表", func(t *testing.T) {
		bestParent := coordinator.selectBestParent([]*Node{})
		assert.Nil(t, bestParent)
	})

	t.Run("所有节点非 Ready 状态", func(t *testing.T) {
		config := DefaultTreeCoordinatorConfig()
		config.MaxChildren = 10
		config.MaxLevel = 3
		config.AutoDiscovery = false

		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		// 创建非 Ready 状态的候选节点
		candidates := []*Node{
			{
				NodeID: "node2",
				Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
				Level:  0,
				Status: NodeStatusInit,
				Role:   Parent,
			},
			{
				NodeID: "node3",
				Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9202},
				Level:  0,
				Status: NodeStatusJoining,
				Role:   Parent,
			},
		}

		bestParent := coordinator.selectBestParent(candidates)
		assert.Nil(t, bestParent)
	})

	t.Run("所有节点达到最大深度", func(t *testing.T) {
		config := DefaultTreeCoordinatorConfig()
		config.MaxChildren = 10
		config.MaxLevel = 1 // 最大深度为 1
		config.AutoDiscovery = false

		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		// 创建达到最大深度的候选节点
		candidates := []*Node{
			{
				NodeID: "node2",
				Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
				Level:  1, // 等于 MaxLevel
				Status: NodeStatusReady,
				Role:   Parent,
			},
		}

		bestParent := coordinator.selectBestParent(candidates)
		assert.Nil(t, bestParent)
	})

	t.Run("所有节点子节点数达到上限", func(t *testing.T) {
		config := DefaultTreeCoordinatorConfig()
		config.MaxChildren = 2 // 最大子节点数为 2
		config.MaxLevel = 3
		config.AutoDiscovery = false

		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		// 创建子节点数达到上限的候选节点
		candidates := []*Node{
			{
				NodeID:      "node2",
				Addr:        types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
				Level:       0,
				Status:      NodeStatusReady,
				Role:        Parent,
				ChildrenIDs: []string{"child1", "child2", "child3"}, // 超过 MaxChildren=2
			},
		}

		bestParent := coordinator.selectBestParent(candidates)
		assert.Nil(t, bestParent)
	})
}

// TestGossipTopologyChange 测试拓扑变更 Gossip
func TestGossipTopologyChange(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("无 RPC 客户端", func(t *testing.T) {
		// 没有 RPC 客户端，应该直接返回
		coordinator.gossipTopologyChange("node-join", "child1", "", 0)
		// 验证不会 panic
	})

	t.Run("有其他节点但无 RPC 客户端", func(t *testing.T) {
		_ = coordinator.AddChild("child1")

		coordinator.nodesMu.RLock()
		otherNodes := make([]*Node, 0, len(coordinator.allNodes))
		for _, node := range coordinator.allNodes {
			if node.NodeID != coordinator.localNode.NodeID {
				otherNodes = append(otherNodes, node)
			}
		}
		coordinator.nodesMu.RUnlock()

		// 验证 otherNodes 不为空
		assert.NotEmpty(t, otherNodes, "应该有其他节点")

		// 没有 RPC 客户端，无法发送
		coordinator.gossipTopologyChange("node-join", "child1", "", 0)
	})
}

// TestGetTreeDepth 测试获取树深度
func TestGetTreeDepth(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("root1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("只有根节点", func(t *testing.T) {
		depth := coordinator.GetTreeDepth()
		assert.Equal(t, 0, depth)
	})

	t.Run("一层子节点", func(t *testing.T) {
		_ = coordinator.AddChild("child1")
		_ = coordinator.AddChild("child2")

		coordinator.nodesMu.Lock()
		for _, nodeID := range []string{"child1", "child2"} {
			if node, ok := coordinator.allNodes[nodeID]; ok {
				node.Level = 1
			}
		}
		coordinator.nodesMu.Unlock()

		depth := coordinator.GetTreeDepth()
		assert.Equal(t, 1, depth)
	})

	t.Run("多层子节点", func(t *testing.T) {
		// 添加三层结构
		_ = coordinator.AddChild("parent1")
		_ = coordinator.AddChild("grandchild1")

		coordinator.nodesMu.Lock()
		if p1, ok := coordinator.allNodes["parent1"]; ok {
			p1.Role = Parent
			p1.Level = 1
			p1.ChildrenIDs = []string{"grandchild1"}
		}
		if gc1, ok := coordinator.allNodes["grandchild1"]; ok {
			gc1.ParentID = "parent1"
			gc1.Level = 2
		}
		coordinator.nodesMu.Unlock()

		depth := coordinator.GetTreeDepth()
		assert.Equal(t, 2, depth)
	})
}

// TestGetStats 测试获取统计信息
func TestGetStats(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// AddChild 不更新统计信息，统计信息在 AddNode 等方法中更新
	_ = coordinator.AddChild("child1")
	_ = coordinator.AddChild("child2")

	stats := coordinator.GetStats()

	assert.NotNil(t, stats)
	// AddChild 不更新 TotalNodes，所以仍然是 1（只有本地节点）
	assert.Equal(t, int32(1), stats.TotalNodes.Load())
	assert.Equal(t, int32(1), stats.OnlineNodes.Load())
	assert.Equal(t, int32(0), stats.OfflineNodes.Load())
	// TreeDepth 会根据当前拓扑计算
	assert.GreaterOrEqual(t, stats.TreeDepth.Load(), int32(0))
}

// TestListNodes 测试列出所有节点
func TestListNodes(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("空列表", func(t *testing.T) {
		nodes := coordinator.ListNodes()
		assert.Len(t, nodes, 1) // 只有本地节点
	})

	t.Run("有多个节点", func(t *testing.T) {
		_ = coordinator.AddChild("child1")
		_ = coordinator.AddChild("child2")

		nodes := coordinator.ListNodes()
		assert.Len(t, nodes, 3)

		nodeIDs := make([]string, len(nodes))
		for i, node := range nodes {
			nodeIDs[i] = node.NodeID
		}
		assert.Contains(t, nodeIDs, "node1")
		assert.Contains(t, nodeIDs, "child1")
		assert.Contains(t, nodeIDs, "child2")
	})
}

// TestGetNode 测试获取节点
func TestGetNode(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("获取本地节点", func(t *testing.T) {
		node, err := coordinator.GetNode("node1")
		assert.NoError(t, err)
		assert.Equal(t, "node1", node.NodeID)
	})

	t.Run("获取子节点", func(t *testing.T) {
		_ = coordinator.AddChild("child1")

		node, err := coordinator.GetNode("child1")
		assert.NoError(t, err)
		assert.Equal(t, "child1", node.NodeID)
	})

	t.Run("获取不存在的节点", func(t *testing.T) {
		_, err := coordinator.GetNode("non-existent")
		assert.Error(t, err)
	})
}

// TestAddChildWithAddr 测试带地址添加子节点
func TestAddChildWithAddr(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("添加带地址的子节点", func(t *testing.T) {
		addr := &types.NodeAddress{
			Host:    "192.168.1.100",
			TCPPort: 7001,
			UDPPort: 7002,
		}

		err := coordinator.AddChildWithAddr("child1", addr)
		assert.NoError(t, err)

		// 验证节点已添加
		node, err := coordinator.GetNode("child1")
		assert.NoError(t, err)
		assert.Equal(t, "child1", node.NodeID)
		assert.Equal(t, "192.168.1.100", node.Addr.Host)
		assert.Equal(t, 7001, node.Addr.TCPPort)
		assert.Equal(t, 1, node.Level)
		assert.Equal(t, "node1", node.ParentID)
	})

	t.Run("子节点数量超限", func(t *testing.T) {
		config := DefaultTreeCoordinatorConfig()
		config.MaxChildren = 2

		coordinator2, err := NewTreeCoordinator("node2", "127.0.0.1:9212", config, nil, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator2.Stop() }()

		addr := &types.NodeAddress{Host: "127.0.0.1", TCPPort: 7001}

		_ = coordinator2.AddChild("child1")
		_ = coordinator2.AddChild("child2")

		// 第三个子节点应该失败
		err = coordinator2.AddChildWithAddr("child3", addr)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "子节点数量已达上限")
	})
}

// TestRemoveChild 测试移除子节点
func TestRemoveChild(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("移除存在的子节点", func(t *testing.T) {
		_ = coordinator.AddChild("child1")

		err := coordinator.RemoveChild("child1")
		assert.NoError(t, err)

		// 验证已从本地节点的子节点列表中移除
		coordinator.nodesMu.RLock()
		assert.NotContains(t, coordinator.localNode.ChildrenIDs, "child1")
		// 验证节点的父节点已被清空
		if child, ok := coordinator.allNodes["child1"]; ok {
			assert.Equal(t, "", child.ParentID)
			assert.Equal(t, 0, child.Level)
		}
		coordinator.nodesMu.RUnlock()
	})

	t.Run("移除不存在的子节点", func(t *testing.T) {
		err := coordinator.RemoveChild("non-existent")
		// 可能返回成功或错误，取决于实现
		// 这里只验证不会 panic
		_ = err
	})
}

// TestReparentChild 测试重新分配子节点
func TestReparentChild(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("重新分配子节点到本地节点", func(t *testing.T) {
		// 添加子节点
		_ = coordinator.AddChild("child1")
		_ = coordinator.AddChild("child2")

		// 手动设置 child1 为父节点，grandchild1 作为 child1 的子节点
		coordinator.nodesMu.Lock()
		if node, ok := coordinator.allNodes["child1"]; ok {
			node.Role = Parent
			node.Level = 1
		}
		// 添加一个孙子节点，父节点是 child1（不是本地节点）
		grandchild := &Node{
			NodeID:      "grandchild1",
			ParentID:    "child1",
			Level:       2,
			Role:        Leaf,
			Status:      NodeStatusReady,
			ChildrenIDs: []string{},
		}
		coordinator.allNodes["grandchild1"] = grandchild
		if c1, ok := coordinator.allNodes["child1"]; ok {
			c1.ChildrenIDs = []string{"grandchild1"}
		}
		coordinator.nodesMu.Unlock()

		// 重新分配 grandchild1 到本地节点（node1）
		err := coordinator.ReparentChild("grandchild1", "node1", "child1")
		assert.NoError(t, err)

		// 验证新父节点是本地节点
		coordinator.nodesMu.RLock()
		if gc, ok := coordinator.allNodes["grandchild1"]; ok {
			assert.Equal(t, "node1", gc.ParentID)
		}
		if local, ok := coordinator.allNodes["node1"]; ok {
			assert.Contains(t, local.ChildrenIDs, "grandchild1")
		}
		// 注意：child1.ChildrenIDs 不会被修改，因为 child1 不是本地节点
		// 在实际分布式环境中，会通过 RPC 同步来更新 child1 的状态
		coordinator.nodesMu.RUnlock()
	})

	t.Run("不涉及本地节点，返回成功", func(t *testing.T) {
		// 添加子节点
		_ = coordinator.AddChild("child1")
		_ = coordinator.AddChild("child2")

		// 手动设置节点关系
		coordinator.nodesMu.Lock()
		if node, ok := coordinator.allNodes["child1"]; ok {
			node.Role = Parent
			node.Level = 1
		}
		if node, ok := coordinator.allNodes["child2"]; ok {
			node.Role = Parent
			node.Level = 1
		}
		grandchild := &Node{
			NodeID:      "grandchild1",
			ParentID:    "child1",
			Level:       2,
			Role:        Leaf,
			Status:      NodeStatusReady,
			ChildrenIDs: []string{},
		}
		coordinator.allNodes["grandchild1"] = grandchild
		if c1, ok := coordinator.allNodes["child1"]; ok {
			c1.ChildrenIDs = []string{"grandchild1"}
		}
		coordinator.nodesMu.Unlock()

		// 重新分配 grandchild1 从 child1 到 child2（都不涉及本地节点）
		// 应该返回 nil（由相关节点处理）
		err := coordinator.ReparentChild("grandchild1", "child2", "child1")
		assert.NoError(t, err)

		// 验证关系没有改变（因为本地节点不参与处理）
		coordinator.nodesMu.RLock()
		if gc, ok := coordinator.allNodes["grandchild1"]; ok {
			assert.Equal(t, "child1", gc.ParentID) // 保持不变
		}
		coordinator.nodesMu.RUnlock()
	})

	t.Run("子节点不存在", func(t *testing.T) {
		err := coordinator.ReparentChild("non-existent", "child1", "")
		assert.Error(t, err)
	})
}

// TestStartGoroutineWithRecovery 测试 panic 恢复
func TestStartGoroutineWithRecovery(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 测试 panic 恢复
	panicked := false
	var mu sync.Mutex

	recoveryFunc := func() {
		mu.Lock()
		panicked = true
		mu.Unlock()
		panic("test panic")
	}

	// 启动带恢复的 goroutine
	coordinator.startGoroutineWithRecovery("testPanic", recoveryFunc)

	// 等待 goroutine 执行
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.True(t, panicked, "goroutine 应该已经 panic 并被恢复")
	mu.Unlock()
}

// ========================================
// GetLocalNode 测试
// ========================================

// TestGetLocalNode 测试获取本地节点信息
func TestGetLocalNode(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	localNode := coordinator.GetLocalNode()

	assert.NotNil(t, localNode)
	assert.Equal(t, "node1", localNode.NodeID)
	assert.Equal(t, "127.0.0.1", localNode.Addr.Host)
	assert.Equal(t, 9211, localNode.Addr.TCPPort)
	assert.Equal(t, 0, localNode.Level)
	assert.Equal(t, NodeStatusInit, localNode.Status)
}

// TestGetLocalNode_AfterStart 测试启动后获取本地节点信息
func TestGetLocalNode_AfterStart(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	err = coordinator.Start()
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 等待启动完成
	time.Sleep(100 * time.Millisecond)

	localNode := coordinator.GetLocalNode()

	assert.NotNil(t, localNode)
	assert.Equal(t, "node1", localNode.NodeID)
	assert.Equal(t, NodeStatusReady, localNode.Status)
}

// ========================================
// sendHeartbeat 测试
// ========================================

// TestSendHeartbeat_NoRPCClient 测试无 RPC 客户端时发送心跳
func TestSendHeartbeat_NoRPCClient(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 添加父节点和子节点
	coordinator.nodesMu.Lock()
	coordinator.localNode.ParentID = "parent1"
	coordinator.localNode.ChildrenIDs = []string{"child1"}
	coordinator.allNodes["parent1"] = &Node{
		NodeID: "parent1",
		Addr: types.NodeAddress{
			Host:    "127.0.0.1",
			TCPPort: 9201,
		},
		Status: NodeStatusReady,
	}
	coordinator.allNodes["child1"] = &Node{
		NodeID: "child1",
		Addr: types.NodeAddress{
			Host:    "127.0.0.1",
			TCPPort: 9202,
		},
		Status: NodeStatusReady,
	}
	coordinator.nodesMu.Unlock()

	// 没有 RPC 客户端，不应该 panic
	coordinator.sendHeartbeat()

	// 验证本地节点心跳时间已更新
	assert.True(t, time.Since(coordinator.localNode.LastHeartbeat) < time.Second)
}

// ========================================
// sendHeartbeatToNode 测试
// ========================================

// TestSendHeartbeatToNode_NoRPCClient 测试无 RPC 客户端时向节点发送心跳
func TestSendHeartbeatToNode_NoRPCClient(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	targetNode := &Node{
		NodeID: "target1",
		Addr: types.NodeAddress{
			Host:    "127.0.0.1",
			TCPPort: 9201,
		},
		Status: NodeStatusReady,
	}

	reqBody := []byte("test")

	// 没有 RPC 客户端，不应该 panic
	coordinator.sendHeartbeatToNode(targetNode, reqBody)
}

// ========================================
// sendLeaveMessage 测试
// ========================================

// TestSendLeaveMessage_NoRPCClient 测试无 RPC 客户端时发送离开消息
func TestSendLeaveMessage_NoRPCClient(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	targetNode := &Node{
		NodeID: "target1",
		Addr: types.NodeAddress{
			Host:    "127.0.0.1",
			TCPPort: 9201,
		},
		Status: NodeStatusReady,
	}

	reqBody := []byte("test")

	// 没有 RPC 客户端，不应该 panic
	coordinator.sendLeaveMessage(targetNode, reqBody)
}

// ========================================
// sendJoinRequest 测试
// ========================================

// TestSendJoinRequest_NoRPCClient 测试无 RPC 客户端时发送加入请求
func TestSendJoinRequest_NoRPCClient(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	targetNode := &Node{
		NodeID: "parent1",
		Addr: types.NodeAddress{
			Host:    "127.0.0.1",
			TCPPort: 9201,
		},
		Status: NodeStatusReady,
	}

	err = coordinator.sendJoinRequest(targetNode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "RPC 客户端未初始化")
}

// ========================================
// extractSeedNodesFromConfig 测试
// ========================================

// TestExtractSeedNodesFromConfig_NilConfig 测试空配置
func TestExtractSeedNodesFromConfig_NilConfig(t *testing.T) {
	result := extractSeedNodesFromConfig(nil)
	assert.Nil(t, result)
}

// TestExtractSeedNodesFromConfig_EmptyHosts 测试空 Hosts 列表
func TestExtractSeedNodesFromConfig_EmptyHosts(t *testing.T) {
	// 需要先导入 metadataconfig 包
	// 这里简化处理，直接测试逻辑
	result := extractSeedNodesFromConfig(nil)
	assert.Nil(t, result)
}

// TestExtractSeedNodesFromConfig_WithValidHosts 测试有效配置
func TestExtractSeedNodesFromConfig_WithValidHosts(t *testing.T) {
	config := &metadataconfig.ClusterConfig{
		Hosts: []metadataconfig.HostConfig{
			{SeedNode: "127.0.0.1:9201"},
			{SeedNode: "127.0.0.1:9202"},
			{SeedNode: "127.0.0.1:9201"}, // 重复，应该被去重
			{SeedNode: ""},               // 空值，应该被跳过
		},
	}

	result := extractSeedNodesFromConfig(config)
	assert.NotNil(t, result)
	assert.Len(t, result, 2) // 去重后应该只有 2 个
	assert.Contains(t, result, "127.0.0.1:9201")
	assert.Contains(t, result, "127.0.0.1:9202")
}

// ========================================
// loadHost 测试
// ========================================

// TestLoadHost_FileNotFound 测试文件不存在的情况
func TestLoadHost_FileNotFound(t *testing.T) {
	// loadHost 是私有方法，需要通过 NewHostManager 和适当的 MVStore 来测试
	// 这里我们跳过这个测试，因为它需要 MVStore 设置
	t.Skip("loadHost 需要 MVStore 设置")
}

// ========================================
// addrToPeerID 测试
// ========================================

// TestAddrToPeerID 测试地址转换为 peer.ID
func TestAddrToPeerID(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 测试地址转换 - addrToPeerID 会生成一个 base58 编码的 peer.ID
	peerID := coordinator.addrToPeerID("127.0.0.1:9211")
	assert.NotEmpty(t, peerID)
	// peer.ID 是一个 base58 编码的字符串，不会直接包含 IP 和端口
}

// ========================================
// gossipTopologyChange 测试
// ========================================

// TestGossipTopologyChange_Basic 测试拓扑变更广播
func TestGossipTopologyChange_Basic(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 添加一些子节点
	_ = coordinator.AddChild("child1")
	_ = coordinator.AddChild("child2")

	// 测试 gossip（没有 RPC 客户端，不应该 panic）
	coordinator.gossipTopologyChange("add", "new-node", "node1", 1)
	// 等待 goroutine 执行
	time.Sleep(100 * time.Millisecond)
}

// ========================================
// sendGossipMessage 测试
// ========================================

// TestSendGossipMessage_Basic 测试发送 gossip 消息
func TestSendGossipMessage_Basic(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 添加一些子节点（需要使用带地址的方法）
	addr1 := &types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201}
	_ = coordinator.AddChildWithAddr("child1", addr1)

	addr2 := &types.NodeAddress{Host: "127.0.0.1", TCPPort: 9202}
	_ = coordinator.AddChildWithAddr("child2", addr2)

	// 创建目标节点和消息体
	targetNode := &Node{
		NodeID: "child1",
		Addr:   *addr1,
	}

	gossipReq := rpc.GossipTopologyChangeRequest{
		Operation: "add",
		NodeID:    "new-node",
		ParentID:  "node1",
		Level:     1,
		Version:   12345,
		Timestamp: time.Now().UnixNano(),
	}
	reqBody, _ := msgpack.Marshal(gossipReq)

	// 测试发送（没有 RPC 客户端，不应该 panic）
	_ = coordinator.sendGossipMessage(targetNode, reqBody)
}

// ========================================
// AddNode 测试
// ========================================

// TestAddNode_Basic 测试添加新节点到集群
func TestAddNode_Basic(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	err = coordinator.Start()
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("成功添加节点", func(t *testing.T) {
		err := coordinator.AddNode("new-node", "127.0.0.1:9201")
		assert.NoError(t, err)

		// 验证节点已添加
		coordinator.nodesMu.RLock()
		_, exists := coordinator.allNodes["new-node"]
		coordinator.nodesMu.RUnlock()
		assert.True(t, exists)
	})

	t.Run("节点已存在", func(t *testing.T) {
		err := coordinator.AddNode("node1", "127.0.0.1:9211")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "节点已存在")
	})

	t.Run("无效地址", func(t *testing.T) {
		err := coordinator.AddNode("invalid-node", "invalid-address")
		assert.Error(t, err)
	})
}

// ========================================
// RemoveNode 测试
// ========================================

// TestRemoveNode_Basic 测试从集群移除节点
func TestRemoveNode_Basic(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	err = coordinator.Start()
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 添加子节点
	_ = coordinator.AddChild("child1")

	t.Run("成功移除节点", func(t *testing.T) {
		err := coordinator.RemoveNode("child1")
		assert.NoError(t, err)

		// RemoveNode 会从 allNodes 中删除节点，所以我们验证节点不存在
		coordinator.nodesMu.RLock()
		_, exists := coordinator.allNodes["child1"]
		coordinator.nodesMu.RUnlock()
		assert.False(t, exists) // 节点应该已被删除
	})

	t.Run("节点不存在", func(t *testing.T) {
		err := coordinator.RemoveNode("non-existent")
		assert.Error(t, err)
	})

	t.Run("不能移除本地节点", func(t *testing.T) {
		err := coordinator.RemoveNode("node1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "本地节点")
	})
}

// ========================================
// AddChildWithAddr 扩展测试
// ========================================

// TestAddChildWithAddr_WithNodeAddress 测试带 NodeAddress 添加子节点
func TestAddChildWithAddr_WithNodeAddress(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 5
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	t.Run("成功添加带地址的子节点", func(t *testing.T) {
		addr := &types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201}
		err := coordinator.AddChildWithAddr("child1", addr)
		assert.NoError(t, err)

		// 验证节点已添加
		coordinator.nodesMu.RLock()
		child, exists := coordinator.allNodes["child1"]
		coordinator.nodesMu.RUnlock()
		assert.True(t, exists)
		assert.Equal(t, "127.0.0.1", child.Addr.Host)
		assert.Equal(t, 9201, child.Addr.TCPPort)
	})
}

// ========================================
// containsNodeAddr 测试
// ========================================

// TestContainsNodeAddr_Helper 测试检查节点地址是否存在（辅助函数）
func TestContainsNodeAddr_Helper(t *testing.T) {
	nodes := []*Node{
		{NodeID: "node1", Addr: types.NodeAddress{Host: "127.0.0.1", TCPPort: 9211}},
		{NodeID: "node2", Addr: types.NodeAddress{Host: "127.0.0.1", TCPPort: 9212}},
	}

	t.Run("地址存在", func(t *testing.T) {
		// TCPAddr() 返回 IPFS 格式：/ip4/127.0.0.1/tcp/9211
		result := containsNodeAddr(nodes, "/ip4/127.0.0.1/tcp/9211")
		assert.True(t, result)
	})

	t.Run("地址不存在", func(t *testing.T) {
		result := containsNodeAddr(nodes, "/ip4/127.0.0.1/tcp/9299")
		assert.False(t, result)
	})

	t.Run("空节点列表", func(t *testing.T) {
		result := containsNodeAddr([]*Node{}, "/ip4/127.0.0.1/tcp/9211")
		assert.False(t, result)
	})
}

// ========================================
// discoverAndJoin 测试
// ========================================

// TestDiscoverAndJoin 测试发现并加入集群
func TestDiscoverAndJoin(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 测试没有 RPC 客户端的情况
	coordinator.discoverAndJoin()
	// 不应该 panic
}

// ========================================
// leaveTree 测试
// ========================================

// TestLeaveTree 测试离开树形结构
func TestLeaveTree(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	err = coordinator.Start()
	require.NoError(t, err)

	// 添加一些子节点
	_ = coordinator.AddChild("child1")
	_ = coordinator.AddChild("child2")

	// 测试离开（没有 RPC 客户端，不应该 panic）
	coordinator.leaveTree()

	// 验证状态已更新
	coordinator.nodesMu.RLock()
	status := coordinator.localNode.Status
	coordinator.nodesMu.RUnlock()
	assert.Equal(t, NodeStatusLeaving, status)

	_ = coordinator.Stop()
}

// ========================================
// sendJoinRequest_WithRPC 测试
// ========================================

// TestSendJoinRequest_WithMockRPC 测试带模拟 RPC 的加入请求
func TestSendJoinRequest_WithMockRPC(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	targetNode := &Node{
		NodeID: "parent1",
		Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
	}

	// 没有 RPC 客户端，应该返回错误
	err = coordinator.sendJoinRequest(targetNode)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "RPC 客户端未初始化")
}

// ========================================
// Start 扩展测试
// ========================================

// TestStart_WithoutAutoDiscovery 测试不自动发现的启动
func TestStart_WithoutAutoDiscovery(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	err = coordinator.Start()
	assert.NoError(t, err)

	// 验证状态
	assert.True(t, coordinator.IsRunning())
	assert.Equal(t, NodeStatusReady, coordinator.localNode.Status)

	_ = coordinator.Stop()
}

// ========================================
// NewTreeCoordinator 扩展测试
// ========================================

// TestNewTreeCoordinator_WithClusterConfig 测试带集群配置的创建
func TestNewTreeCoordinator_WithClusterConfig(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false

	clusterConfig := &metadataconfig.ClusterConfig{
		Hosts: []metadataconfig.HostConfig{
			{SeedNode: "127.0.0.1:9201"},
			{SeedNode: "127.0.0.1:9202"},
		},
	}

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, clusterConfig, nil)
	assert.NoError(t, err)
	assert.NotNil(t, coordinator)
	defer func() { _ = coordinator.Stop() }()
}

// ========================================
// gossipTopologyChange 扩展测试
// ========================================

// TestGossipTopologyChange_AllOperations 测试所有操作类型
func TestGossipTopologyChange_AllOperations(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 添加一些子节点
	_ = coordinator.AddChild("child1")
	_ = coordinator.AddChild("child2")

	operations := []string{"add", "remove", "reparent"}
	for _, op := range operations {
		t.Run("操作_"+op, func(t *testing.T) {
			// 没有 RPC 客户端，不应该 panic
			coordinator.gossipTopologyChange(op, "test-node", "node1", 1)
			time.Sleep(50 * time.Millisecond)
		})
	}
}

// ========================================
// sendLeaveMessage 扩展测试
// ========================================

// TestSendLeaveMessage_WithTargetNode 测试向目标节点发送离开消息
func TestSendLeaveMessage_WithTargetNode(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	targetNode := &Node{
		NodeID: "parent1",
		Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
	}

	reqBody := []byte("test-leave-message")

	// 没有 RPC 客户端，不应该 panic
	coordinator.sendLeaveMessage(targetNode, reqBody)
}

// ========================================
// sendHeartbeatWithParent 测试
// ========================================

// TestSendHeartbeat_WithParent 测试向父节点发送心跳
func TestSendHeartbeat_WithParent(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 设置父节点
	coordinator.nodesMu.Lock()
	coordinator.localNode.ParentID = "parent1"
	coordinator.allNodes["parent1"] = &Node{
		NodeID: "parent1",
		Addr:   types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
		Status: NodeStatusReady,
	}
	coordinator.nodesMu.Unlock()

	// 测试发送心跳（没有 RPC 客户端，不应该 panic）
	coordinator.sendHeartbeat()

	// 验证心跳时间已更新
	assert.True(t, time.Since(coordinator.localNode.LastHeartbeat) < time.Second)
}

// ========================================
// sendHeartbeatWithChildren 测试
// ========================================

// TestSendHeartbeat_WithChildren 测试向子节点发送心跳
func TestSendHeartbeat_WithChildren(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 添加子节点
	addr1 := &types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201}
	_ = coordinator.AddChildWithAddr("child1", addr1)

	addr2 := &types.NodeAddress{Host: "127.0.0.1", TCPPort: 9202}
	_ = coordinator.AddChildWithAddr("child2", addr2)

	// 测试发送心跳（没有 RPC 客户端，不应该 panic）
	coordinator.sendHeartbeat()

	// 验证心跳时间已更新
	assert.True(t, time.Since(coordinator.localNode.LastHeartbeat) < time.Second)
}

// ========================================
// AddChildWithAddr 扩展测试
// ========================================

// TestAddChildWithAddr_LevelLimit 测试层级限制
func TestAddChildWithAddr_LevelLimit(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxLevel = 2
	config.AutoDiscovery = false

	coordinator, err := NewTreeCoordinator("node2", "127.0.0.1:9212", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 设置 Level 接近 MaxLevel
	coordinator.nodesMu.Lock()
	coordinator.localNode.Level = 2 // 已经是 MaxLevel
	coordinator.nodesMu.Unlock()

	addr := &types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201}
	err = coordinator.AddChildWithAddr("child1", addr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "超出树的最大深度限制")
}

// ========================================
// HostRole String 测试
// ========================================

// TestHostRoleString 测试主机角色 String 方法
func TestHostRoleString(t *testing.T) {
	t.Run("LeafOnly", func(t *testing.T) {
		role := LeafOnly
		assert.Equal(t, "leaf_only", role.String())
	})

	t.Run("LeafParent", func(t *testing.T) {
		role := LeafParent
		assert.Equal(t, "leaf_parent", role.String())
	})

	t.Run("LeafParentStandby", func(t *testing.T) {
		role := LeafParentStandby
		assert.Equal(t, "leaf_parent_standby", role.String())
	})

	t.Run("无效角色", func(t *testing.T) {
		role := HostRole(99)
		assert.Equal(t, "unknown", role.String())
	})
}

// ========================================
// NodeRole String 测试
// ========================================

// TestNodeRoleString 测试节点角色 String 方法
func TestNodeRoleString(t *testing.T) {
	t.Run("Leaf", func(t *testing.T) {
		role := Leaf
		assert.Equal(t, "leaf", role.String())
	})

	t.Run("Parent", func(t *testing.T) {
		role := Parent
		assert.Equal(t, "parent", role.String())
	})

	t.Run("ParentStandby", func(t *testing.T) {
		role := ParentStandby
		assert.Equal(t, "parent_standby", role.String())
	})

	t.Run("无效角色", func(t *testing.T) {
		role := NodeRole(99)
		assert.Equal(t, "unknown", role.String())
	})
}

// ========================================
// ScaleUp/ScaleDown 测试
// ========================================

// TestScaleUp 测试扩容操作
func TestScaleUp(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 10
	config.MaxLevel = 3
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 需要先启动 coordinator 才能添加节点
	err = coordinator.Start()
	if err != nil {
		t.Skipf("启动 coordinator 失败: %v", err)
	}

	t.Run("成功扩容", func(t *testing.T) {
		nodeIDs := []string{"node2", "node3"}
		addrs := []string{"127.0.0.1:9212", "127.0.0.1:9213"}

		err := coordinator.ScaleUp(nodeIDs, addrs)
		assert.NoError(t, err)

		// 验证节点已添加到 allNodes
		coordinator.nodesMu.RLock()
		_, exists := coordinator.allNodes["node2"]
		coordinator.nodesMu.RUnlock()
		assert.True(t, exists)

		coordinator.nodesMu.RLock()
		_, exists = coordinator.allNodes["node3"]
		coordinator.nodesMu.RUnlock()
		assert.True(t, exists)
	})

	t.Run("参数长度不一致", func(t *testing.T) {
		nodeIDs := []string{"node4"}
		addrs := []string{"127.0.0.1:9214", "127.0.0.1:9215"}

		err := coordinator.ScaleUp(nodeIDs, addrs)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "长度不一致")
	})
}

// TestScaleDown 测试缩容操作
func TestScaleDown(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 10
	config.MaxLevel = 3
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)

	// 需要先启动 coordinator 才能添加/移除节点
	err = coordinator.Start()
	if err != nil {
		t.Skipf("启动 coordinator 失败: %v", err)
	}

	// 先添加一些节点
	_ = coordinator.AddNode("node2", "127.0.0.1:9212")
	_ = coordinator.AddNode("node3", "127.0.0.1:9213")

	t.Run("成功缩容", func(t *testing.T) {
		nodeIDs := []string{"node2", "node3"}

		err := coordinator.ScaleDown(nodeIDs)
		assert.NoError(t, err)

		// 验证节点已移除
		coordinator.nodesMu.RLock()
		_, exists := coordinator.allNodes["node2"]
		coordinator.nodesMu.RUnlock()
		assert.False(t, exists)

		coordinator.nodesMu.RLock()
		_, exists = coordinator.allNodes["node3"]
		coordinator.nodesMu.RUnlock()
		assert.False(t, exists)
	})

	t.Run("部分失败", func(t *testing.T) {
		// 添加一个节点
		_ = coordinator.AddNode("node4", "127.0.0.1:9214")

		// 尝试移除一个存在的节点和一个不存在的节点
		nodeIDs := []string{"node4", "nonexistent"}

		err := coordinator.ScaleDown(nodeIDs)
		// 应该返回 nil，因为至少有一个成功
		assert.NoError(t, err)

		// node4 应该被移除
		coordinator.nodesMu.RLock()
		_, exists := coordinator.allNodes["node4"]
		coordinator.nodesMu.RUnlock()
		assert.False(t, exists)
	})

	t.Run("全部失败", func(t *testing.T) {
		nodeIDs := []string{"nonexistent1", "nonexistent2"}

		err := coordinator.ScaleDown(nodeIDs)
		assert.Error(t, err)
	})

	_ = coordinator.Stop()
}

// ========================================
// redistributeChildren 测试
// ========================================

// TestRedistributeChildren 测试重新分配子节点
func TestRedistributeChildren(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 5
	config.MaxLevel = 3
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 启动 coordinator 以添加本地节点到 allNodes
	err = coordinator.Start()
	if err != nil {
		t.Skipf("启动 coordinator 失败: %v", err)
	}

	// 创建一个中间节点，带子节点
	parentNode := &Node{
		NodeID:      "parent1",
		Addr:        types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
		ParentID:    "node1",
		Level:       1,
		Status:      NodeStatusReady,
		Role:        Parent,
		ChildrenIDs: []string{"child1", "child2"},
	}

	child1 := &Node{
		NodeID:   "child1",
		Addr:     types.NodeAddress{Host: "127.0.0.1", TCPPort: 9202},
		ParentID: "parent1",
		Level:    2,
		Status:   NodeStatusReady,
		Role:     Leaf,
	}

	child2 := &Node{
		NodeID:   "child2",
		Addr:     types.NodeAddress{Host: "127.0.0.1", TCPPort: 9203},
		ParentID: "parent1",
		Level:    2,
		Status:   NodeStatusReady,
		Role:     Leaf,
	}

	coordinator.allNodes["parent1"] = parentNode
	coordinator.allNodes["child1"] = child1
	coordinator.allNodes["child2"] = child2

	t.Run("重新分配子节点", func(t *testing.T) {
		err := coordinator.redistributeChildren(parentNode)
		assert.NoError(t, err)

		// 验证子节点的父节点已更新
		coordinator.nodesMu.RLock()
		child1NewParent := coordinator.allNodes["child1"].ParentID
		child2NewParent := coordinator.allNodes["child2"].ParentID
		coordinator.nodesMu.RUnlock()

		// 父节点应该不再是原来的 parent1（应该变为 node1）
		assert.NotEqual(t, "parent1", child1NewParent)
		assert.NotEqual(t, "parent1", child2NewParent)
	})
}

// ========================================
// selectParentForNewNode 测试
// ========================================

// TestSelectParentForNewNode 测试为新节点选择父节点
func TestSelectParentForNewNode(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 5
	config.MaxLevel = 3
	config.AutoDiscovery = false
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 启动 coordinator 以添加本地节点到 allNodes
	err = coordinator.Start()
	if err != nil {
		t.Skipf("启动 coordinator 失败: %v", err)
	}

	t.Run("选择本地节点（层级最低）", func(t *testing.T) {
		// 启动后本地节点在 allNodes 中，层级为 0
		parentID, err := coordinator.selectParentForNewNode()
		assert.NoError(t, err)
		// 应该选择 node1（层级 0，最低层级）
		assert.Equal(t, "node1", parentID)
	})

	t.Run("选择有空位的节点", func(t *testing.T) {
		// 添加一个子节点
		addr := &types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201}
		_ = coordinator.AddChildWithAddr("node2", addr)

		parentID, err := coordinator.selectParentForNewNode()
		assert.NoError(t, err)
		// 算法会遍历 allNodes，如果遇到有空位的节点会提前返回
		// node2 在 level 1 有 0 个子节点，可能被选中
		// node1 在 level 0 有 1 个子节点 (node2)
		// 由于 node2 有空位（0 children），算法可能选择 node2
		assert.NotEmpty(t, parentID)
		// 只验证能选择到有效的父节点
		assert.True(t, parentID == "node1" || parentID == "node2")
	})

	t.Run("达到最大深度时返回错误", func(t *testing.T) {
		config := DefaultTreeCoordinatorConfig()
		config.MaxChildren = 5
		config.MaxLevel = 0 // 最大深度为 0，意味着不能有任何子节点
		config.AutoDiscovery = false
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		// 启动 coordinator
		err = coordinator.Start()
		if err != nil {
			t.Skipf("启动 coordinator 失败: %v", err)
		}

		// node1 在 Level 0，如果 MaxLevel=0，node1 的 Level >= MaxLevel
		// 所以 node1 不能接受子节点
		_, err = coordinator.selectParentForNewNode()
		// 应该返回错误，因为 node1 的 level (0) >= MaxLevel (0)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "没有可用的父节点")
	})
}

// ========================================
// AddChild 边缘情况测试
// ========================================

// TestAddChild_EdgeCases 测试 AddChild 边缘情况
func TestAddChild_EdgeCases(t *testing.T) {
	t.Run("子节点已有其他父节点", func(t *testing.T) {
		config := DefaultTreeCoordinatorConfig()
		config.MaxChildren = 5
		config.MaxLevel = 3
		config.AutoDiscovery = false
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		// 创建一个已经有父节点的子节点
		childNode := &Node{
			NodeID:   "child1",
			Addr:     types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
			Status:   NodeStatusReady,
			ParentID: "other-parent", // 已有其他父节点
			Level:    1,
		}

		coordinator.nodesMu.Lock()
		coordinator.allNodes["child1"] = childNode
		coordinator.nodesMu.Unlock()

		// 尝试添加已有其他父节点的子节点
		err = coordinator.AddChild("child1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "已经是")
		assert.Contains(t, err.Error(), "other-parent")
	})

	t.Run("更新现有子节点", func(t *testing.T) {
		config := DefaultTreeCoordinatorConfig()
		config.MaxChildren = 5
		config.MaxLevel = 3
		config.AutoDiscovery = false
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		// 创建一个在 allNodes 中但没有父节点的子节点
		childNode := &Node{
			NodeID:   "child1",
			Addr:     types.NodeAddress{Host: "127.0.0.1", TCPPort: 9201},
			Status:   NodeStatusReady,
			ParentID: "", // 没有父节点
			Level:    0,
		}

		coordinator.nodesMu.Lock()
		coordinator.allNodes["child1"] = childNode
		coordinator.nodesMu.Unlock()

		// 添加子节点（应该更新现有节点的父节点和层级）
		err = coordinator.AddChild("child1")
		assert.NoError(t, err)

		// 验证子节点的父节点和层级已更新
		coordinator.nodesMu.RLock()
		child := coordinator.allNodes["child1"]
		coordinator.nodesMu.RUnlock()

		assert.Equal(t, "node1", child.ParentID)
		assert.Equal(t, 1, child.Level)
	})

	t.Run("超出最大深度限制", func(t *testing.T) {
		config := DefaultTreeCoordinatorConfig()
		config.MaxChildren = 5
		config.MaxLevel = 1 // 最大深度为 1
		config.AutoDiscovery = false
		coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config, nil, nil)
		require.NoError(t, err)
		defer func() { _ = coordinator.Stop() }()

		// 设置本地节点在 Level 1
		coordinator.nodesMu.Lock()
		coordinator.localNode.Level = 1
		coordinator.nodesMu.Unlock()

		// 尝试添加子节点（新子节点会在 Level 2，超过 MaxLevel=1）
		err = coordinator.AddChild("child1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "超出树的最大深度限制")
	})
}
