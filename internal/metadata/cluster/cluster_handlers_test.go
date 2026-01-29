// Package cluster TreeCoordinator RPC Handlers 测试
package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// TreeCoordinatorRPCHandler 测试
// ========================================

// TestNewTreeCoordinatorRPCHandler 测试创建 RPC Handler
func TestNewTreeCoordinatorRPCHandler(t *testing.T) {
	handler := NewTreeCoordinatorRPCHandler(nil)

	assert.NotNil(t, handler)
	assert.Nil(t, handler.coordinator)
}

// TestNewTreeCoordinatorRPCHandler_WithCoordinator 测试带 Coordinator 创建
func TestNewTreeCoordinatorRPCHandler_WithCoordinator(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	assert.NotNil(t, handler)
	assert.Same(t, coordinator, handler.coordinator)
}

// TestTreeCoordinatorRPCHandler_SetCoordinator 测试设置 Coordinator
func TestTreeCoordinatorRPCHandler_SetCoordinator(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)

	handler := NewTreeCoordinatorRPCHandler(nil)
	assert.Nil(t, handler.coordinator)

	handler.SetCoordinator(coordinator)
	assert.Same(t, coordinator, handler.coordinator)
}

// ========================================
// HandleRequest 测试
// ========================================

// TestTreeCoordinatorRPCHandler_HandleRequest_NilCoordinator 测试 nil coordinator
func TestTreeCoordinatorRPCHandler_HandleRequest_NilCoordinator(t *testing.T) {
	handler := NewTreeCoordinatorRPCHandler(nil)
	ctx := context.Background()

	req := &transport.NodeJoinMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeJoin},
		NodeID:      "child1",
		Addr:        "127.0.0.1:9212",
		Role:        "child",
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "coordinator not initialized")
}

// TestTreeCoordinatorRPCHandler_HandleRequest_UnsupportedMessageType 测试不支持的消息类型
func TestTreeCoordinatorRPCHandler_HandleRequest_UnsupportedMessageType(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	// 创建一个不支持的消息类型
	req := &transport.BaseMessage{
		MessageType: 999, // 无效的消息类型
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "unsupported message type")
}

// ========================================
// handleNodeJoin 测试
// ========================================

// TestTreeCoordinatorRPCHandler_HandleRequest_NodeJoin 测试处理节点加入请求
func TestTreeCoordinatorRPCHandler_HandleRequest_NodeJoin(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	req := &transport.NodeJoinMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeJoin},
		NodeID:      "child1",
		Addr:        "127.0.0.1:9212",
		Role:        "child",
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// 验证响应类型
	syncMsg, ok := resp.(*transport.NodeSyncMessage)
	assert.True(t, ok, "响应应该是 NodeSyncMessage 类型")
	assert.NotNil(t, syncMsg)
	assert.Equal(t, types.MessageTypeNodeSync, syncMsg.MessageType)
	assert.NotEmpty(t, syncMsg.Metadata)
	assert.Contains(t, syncMsg.Metadata, "parent_node_id")
	assert.Contains(t, syncMsg.Metadata, "timestamp")

	// 验证节点已添加
	children := coordinator.localNode.ChildrenIDs
	assert.Contains(t, children, "child1")
}

// TestTreeCoordinatorRPCHandler_HandleRequest_NodeJoin_MaxChildren 测试超过最大子节点数
func TestTreeCoordinatorRPCHandler_HandleRequest_NodeJoin_MaxChildren(t *testing.T) {

	config := &TreeCoordinatorConfig{
		MaxChildren:       1, // 限制为 1 个子节点
		MaxLevel:          3, // 允许 3 层深度
		HeartbeatInterval: 5 * time.Second,
		HeartbeatTimeout:  15 * time.Second,
	}

	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	// 添加第一个子节点
	req1 := &transport.NodeJoinMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeJoin},
		NodeID:      "child1",
		Addr:        "127.0.0.1:9212",
		Role:        "child",
	}

	resp1, err1 := handler.HandleRequest(ctx, req1)
	assert.NoError(t, err1)
	assert.NotNil(t, resp1)

	// 添加第二个子节点（应该失败）
	req2 := &transport.NodeJoinMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeJoin},
		NodeID:      "child2",
		Addr:        "child2:9213",
		Role:        "child",
	}

	resp2, err2 := handler.HandleRequest(ctx, req2)
	assert.Error(t, err2)
	assert.Nil(t, resp2)
	assert.Contains(t, err2.Error(), "子节点数量已达上限")
}

// ========================================
// handleNodeLeave 测试
// ========================================

// TestTreeCoordinatorRPCHandler_HandleRequest_NodeLeave 测试处理节点离开请求
func TestTreeCoordinatorRPCHandler_HandleRequest_NodeLeave(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	// 先添加子节点
	err = coordinator.AddChild("child1")
	require.NoError(t, err)

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	req := &transport.NodeLeaveMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeLeave},
		NodeID:      "child1",
		Reason:      "normal_shutdown",
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// 验证响应类型
	syncMsg, ok := resp.(*transport.NodeSyncMessage)
	assert.True(t, ok, "响应应该是 NodeSyncMessage 类型")
	assert.NotNil(t, syncMsg)
	assert.Equal(t, types.MessageTypeNodeSync, syncMsg.MessageType)
	assert.NotEmpty(t, syncMsg.Metadata)
	assert.Contains(t, syncMsg.Metadata, "status")
	assert.Contains(t, syncMsg.Metadata, "timestamp")

	// 验证节点已移除
	children := coordinator.localNode.ChildrenIDs
	assert.NotContains(t, children, "child1")
}

// TestTreeCoordinatorRPCHandler_HandleRequest_NodeLeave_NotFound 测试移除不存在的节点
func TestTreeCoordinatorRPCHandler_HandleRequest_NodeLeave_NotFound(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	req := &transport.NodeLeaveMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeLeave},
		NodeID:      "nonexistent",
		Reason:      "test",
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "子节点不存在")
}

// ========================================
// handleNodeReparent 测试
// ========================================

// TestTreeCoordinatorRPCHandler_HandleRequest_NodeReparent 测试处理重新建立父子关系请求
func TestTreeCoordinatorRPCHandler_HandleRequest_NodeReparent(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)

	// 启动协调器
	err = coordinator.Start()
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	// 步骤 1: 先添加 child1 节点作为 node1 的子节点
	// 注意：AddChild 会将 child1 添加到 allNodes 中（如果不存在）
	err = coordinator.AddChild("child1")
	require.NoError(t, err, "添加子节点应该成功")

	// 步骤 2: 测试将 child1 从 node1 移除（本地节点是旧父节点）
	req := &transport.NodeReparentMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeReparent},
		ChildID:     "child1",
		NewParentID: "node2", // 新父节点（不是本地节点）
		OldParentID: "node1", // 旧父节点（是本地节点）
		Reason:      "测试重新分配",
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// 验证响应类型
	replyMsg, ok := resp.(*transport.NodeReparentReplyMessage)
	assert.True(t, ok, "响应应该是 NodeReparentReplyMessage 类型")
	assert.NotNil(t, replyMsg)
	assert.Equal(t, types.MessageTypeNodeReparentReply, replyMsg.MessageType)

	// PR-034: ReparentChild 逻辑已实现
	// 本地节点 (node1) 是旧父节点，所以会从子节点列表中移除 child1
	// 请求处理成功，所以返回 Success: true
	assert.True(t, replyMsg.Success, "重新分配父节点应该成功")
}

// ========================================
// handleClusterStatus 测试
// ========================================

// TestTreeCoordinatorRPCHandler_HandleRequest_ClusterStatus 测试处理集群状态查询请求
func TestTreeCoordinatorRPCHandler_HandleRequest_ClusterStatus(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	// 添加一些子节点（注意：AddChild 现在会在 allNodes 中创建节点）
	err = coordinator.AddChild("child1")
	require.NoError(t, err)
	err = coordinator.AddChild("child2")
	require.NoError(t, err)

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	req := &transport.ClusterStatusMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeClusterStatus},
		NodeID:      "node1",
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// 验证响应类型
	statusMsg, ok := resp.(*transport.ClusterStatusReplyMessage)
	assert.True(t, ok, "响应应该是 ClusterStatusReplyMessage 类型")
	assert.NotNil(t, statusMsg)
	assert.Equal(t, types.MessageTypeClusterStatusReply, statusMsg.MessageType)

	// 验证节点列表（本地节点 + 两个子节点，因为 AddChild 在 allNodes 中创建了节点）
	assert.Len(t, statusMsg.Nodes, 3) // node1, child1, child2

	// 验证本地节点信息
	localNode := findNodeInfo(statusMsg.Nodes, "node1")
	assert.NotNil(t, localNode)
	assert.Equal(t, "node1", localNode.NodeID)
	assert.Equal(t, "/ip4/127.0.0.1/tcp/9211", localNode.Addr)
	assert.Equal(t, "self", localNode.Role)
	assert.Equal(t, "Ready", localNode.Status) // Status.String() 返回 "Ready"
	assert.Equal(t, 0, localNode.Level)
	assert.Equal(t, "", localNode.ParentID)

	// 验证子节点信息
	child1Node := findNodeInfo(statusMsg.Nodes, "child1")
	assert.NotNil(t, child1Node)
	assert.Equal(t, "child1", child1Node.NodeID)
	assert.Equal(t, "child", child1Node.Role) // 子节点角色为 child
	assert.Equal(t, "node1", child1Node.ParentID)
	assert.Equal(t, 1, child1Node.Level)
}

// ========================================
// handleNodePing 测试
// ========================================

// TestTreeCoordinatorRPCHandler_HandleRequest_NodePing 测试处理心跳请求
func TestTreeCoordinatorRPCHandler_HandleRequest_NodePing(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	req := &transport.NodePingMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePing},
		NodeID:      "node1",
		Sequence:    12345,
		Timestamp:   time.Now().Unix(),
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// 验证响应类型
	pongMsg, ok := resp.(*transport.NodePongMessage)
	assert.True(t, ok, "响应应该是 NodePongMessage 类型")
	assert.NotNil(t, pongMsg)
	assert.Equal(t, types.MessageTypeNodePong, pongMsg.MessageType)
	assert.Equal(t, "node1", pongMsg.NodeID)
	assert.Equal(t, int64(12345), pongMsg.Sequence)
	assert.NotZero(t, pongMsg.Timestamp)
	assert.Equal(t, "ready", pongMsg.Status)
}

// ========================================
// handleNodeSync 测试
// ========================================

// TestTreeCoordinatorRPCHandler_HandleRequest_NodeSync 测试处理节点同步请求
func TestTreeCoordinatorRPCHandler_HandleRequest_NodeSync(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	// 添加子节点（注意：只在 localNode.ChildrenIDs 中添加）
	err = coordinator.AddChild("child1")
	require.NoError(t, err)

	handler := NewTreeCoordinatorRPCHandler(coordinator)
	ctx := context.Background()

	req := &transport.NodeSyncMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeSync},
		Version:     100,
		Metadata:    map[string][]byte{},
	}

	resp, err := handler.HandleRequest(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// 验证响应类型
	syncMsg, ok := resp.(*transport.NodeSyncMessage)
	assert.True(t, ok, "响应应该是 NodeSyncMessage 类型")
	assert.NotNil(t, syncMsg)
	assert.Equal(t, types.MessageTypeNodeSync, syncMsg.MessageType)
	assert.Equal(t, uint64(101), syncMsg.Version) // 版本号应该递增

	// 验证 metadata 包含节点信息（只有本地节点）
	assert.NotEmpty(t, syncMsg.Metadata)
	assert.Contains(t, syncMsg.Metadata, "node1")
	// child1 不在 allNodes 中，所以不会出现在 metadata 中

	// 验证节点数据格式（序列化格式：node1|/ip4/127.0.0.1/tcp/9211||0|Ready）
	node1Data := string(syncMsg.Metadata["node1"])
	assert.Contains(t, node1Data, "node1")
	assert.Contains(t, node1Data, "/ip4/127.0.0.1/tcp/9211") // 使用 TCPAddr() 后的格式
	assert.Contains(t, node1Data, "Ready")                   // Status.String() 返回 "Ready"
}

// ========================================
// getNodeRole 测试
// ========================================

// TestGetNodeRole 测试节点角色判断
func TestGetNodeRole(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	// 注意：AddChild 只添加 ID 到 ChildrenIDs 列表，不在 allNodes 中创建 Node 对象
	// 所以 GetNode("child1") 会返回"节点不存在"错误
	// 因此这里只测试本地节点的情况

	node, err := coordinator.GetNode("node1")
	require.NoError(t, err)
	require.NotNil(t, node)

	role := getNodeRole(coordinator, node)
	assert.Equal(t, "self", role)
}

// TestGetNodeRole_Root 测试根节点角色
func TestGetNodeRole_Root(t *testing.T) {

	config := DefaultTreeCoordinatorConfig()
	coordinator, err := NewTreeCoordinator("node1", "127.0.0.1:9211", config)
	require.NoError(t, err)
	require.NoError(t, coordinator.Start())
	defer func() { _ = coordinator.Stop() }()

	// 本地节点没有父节点，应该是 root
	localNode := coordinator.localNode
	role := getNodeRole(coordinator, localNode)
	assert.Equal(t, "self", role) // 本地节点优先返回 "self"
}

// ========================================
// 辅助函数
// ========================================

// findNodeInfo 在节点列表中查找指定节点
func findNodeInfo(nodes []transport.NodeInfo, nodeID string) *transport.NodeInfo {
	for i := range nodes {
		if nodes[i].NodeID == nodeID {
			return &nodes[i]
		}
	}
	return nil
}
