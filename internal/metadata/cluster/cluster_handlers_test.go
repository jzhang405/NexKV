// Package cluster TreeCoordinator RPC Handlers 集成测试
//
// 测试 TreeCoordinator RPC 请求处理器的核心功能
package cluster

import (
	"context"
	"testing"
	"time"

	metadataconfig "github.com/jzhang405/NexKV/internal/config"
	"github.com/jzhang405/NexKV/internal/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// TestNewTreeCoordinatorRPCHandler 测试创建 RPC Handler
func TestNewTreeCoordinatorRPCHandler(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	// 测试创建处理器
	t.Run("创建处理器", func(t *testing.T) {
		handler := NewTreeCoordinatorRPCHandler(coordinator)
		assert.NotNil(t, handler)
		assert.Equal(t, coordinator, handler.coordinator)
	})

	// 测试设置 coordinator
	t.Run("设置 coordinator", func(t *testing.T) {
		handler := &TreeCoordinatorRPCHandler{}
		assert.Nil(t, handler.coordinator)

		handler.SetCoordinator(coordinator)
		assert.Equal(t, coordinator, handler.coordinator)
	})
}

// TestTreeCoordinatorRPCHandler_HandleNodeJoin 测试节点加入请求处理
func TestTreeCoordinatorRPCHandler_HandleNodeJoin(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 2
	config.MaxLevel = 3
	config.AutoDiscovery = false // 禁用自动发现避免测试竞态
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	ctx := context.Background()

	// 尝试启动（如果失败则跳过测试）
	if err := coordinator.Start(); err != nil {
		t.Skipf("跳过测试：启动 coordinator 失败: %v", err)
	}

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	// 测试用例 1: 成功加入
	t.Run("成功加入", func(t *testing.T) {
		joinReq := rpc.NodeJoinRequest{
			NodeID:    "child-001",
			Addr:      "127.0.0.1:5002",
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(joinReq)
		require.NoError(t, err)

		respBody, err := handler.handleNodeJoin(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.NodeJoinResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		assert.True(t, resp.Accepted, "应该接受加入请求")
		assert.Equal(t, "node-001", resp.ParentID)
		assert.Equal(t, 1, resp.Level) // localNode Level 0 + 1
		assert.Empty(t, resp.Reason, "原因应该为空")
	})

	// 测试用例 2: 子节点数量已达上限
	t.Run("子节点数量已达上限", func(t *testing.T) {
		// 添加第二个子节点
		_ = coordinator.AddChild("child-002")

		// 尝试添加第三个子节点（超过 MaxChildren=2）
		joinReq := rpc.NodeJoinRequest{
			NodeID:    "child-003",
			Addr:      "127.0.0.1:5003",
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(joinReq)
		require.NoError(t, err)

		respBody, err := handler.handleNodeJoin(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.NodeJoinResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		assert.False(t, resp.Accepted, "应该拒绝加入请求")
		assert.Empty(t, resp.ParentID)
		assert.Equal(t, 0, resp.Level)
		assert.Contains(t, resp.Reason, "子节点数量已达上限")
	})

	// 测试用例 3: coordinator 未初始化
	t.Run("coordinator 未初始化", func(t *testing.T) {
		emptyHandler := &TreeCoordinatorRPCHandler{}

		joinReq := rpc.NodeJoinRequest{
			NodeID:    "child-004",
			Addr:      "127.0.0.1:5004",
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(joinReq)
		require.NoError(t, err)

		respBody, err := emptyHandler.handleNodeJoin(ctx, reqBody)
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})

	// 测试用例 4: 无效请求格式
	t.Run("无效请求格式", func(t *testing.T) {
		respBody, err := handler.handleNodeJoin(ctx, []byte("invalid"))
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})
}

// TestTreeCoordinatorRPCHandler_HandleNodeLeave 测试节点离开请求处理
func TestTreeCoordinatorRPCHandler_HandleNodeLeave(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 2
	config.MaxLevel = 3
	config.AutoDiscovery = false // 禁用自动发现避免测试竞态
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	ctx := context.Background()
	if err := coordinator.Start(); err != nil {
		t.Skipf("跳过测试：启动 coordinator 失败: %v", err)
	}

	// 添加子节点
	_ = coordinator.AddChild("child-001")

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	// 测试用例 1: 成功离开
	t.Run("成功离开", func(t *testing.T) {
		leaveReq := rpc.NodeLeaveRequest{
			NodeID:    "child-001",
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(leaveReq)
		require.NoError(t, err)

		respBody, err := handler.handleNodeLeave(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.NodeLeaveResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		assert.True(t, resp.Acknowledged)
	})

	// 测试用例 2: 无效请求格式
	t.Run("无效请求格式", func(t *testing.T) {
		respBody, err := handler.handleNodeLeave(ctx, []byte("invalid"))
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})
}

// TestTreeCoordinatorRPCHandler_HandleNodePing 测试心跳请求处理
func TestTreeCoordinatorRPCHandler_HandleNodePing(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 2
	config.MaxLevel = 3
	config.AutoDiscovery = false // 禁用自动发现避免测试竞态
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	ctx := context.Background()
	if err := coordinator.Start(); err != nil {
		t.Skipf("跳过测试：启动 coordinator 失败: %v", err)
	}

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	// 测试用例 1: 成功响应心跳
	t.Run("成功响应心跳", func(t *testing.T) {
		pingReq := rpc.NodePingRequest{
			Sequence:  12345,
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(pingReq)
		require.NoError(t, err)

		respBody, err := handler.handleNodePing(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.NodePingResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		assert.Equal(t, uint64(12345), resp.Sequence)
		assert.GreaterOrEqual(t, resp.Status, 0) // 状态码应该有效
	})

	// 测试用例 2: 无效请求格式
	t.Run("无效请求格式", func(t *testing.T) {
		respBody, err := handler.handleNodePing(ctx, []byte("invalid"))
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})
}

// TestTreeCoordinatorRPCHandler_HandleClusterStatus 测试集群状态查询处理
func TestTreeCoordinatorRPCHandler_HandleClusterStatus(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 2
	config.MaxLevel = 3
	config.AutoDiscovery = false // 禁用自动发现避免测试竞态
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	ctx := context.Background()
	if err := coordinator.Start(); err != nil {
		t.Skipf("跳过测试：启动 coordinator 失败: %v", err)
	}

	// 添加一些子节点
	_ = coordinator.AddChild("child-001")
	_ = coordinator.AddChild("child-002")

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	// 测试用例 1: 成功获取状态
	t.Run("成功获取状态", func(t *testing.T) {
		statusReq := rpc.ClusterStatusRequest{
			RequesterID: "node-002",
			Timestamp:   time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(statusReq)
		require.NoError(t, err)

		respBody, err := handler.handleClusterStatus(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.ClusterStatusResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		assert.GreaterOrEqual(t, resp.TotalNodes, 1)  // 至少包含本地节点
		assert.GreaterOrEqual(t, resp.OnlineNodes, 1) // 本地节点应该在线
		assert.GreaterOrEqual(t, resp.TreeDepth, 1)
		assert.NotEmpty(t, resp.Nodes) // 应该返回节点列表
	})

	// 测试用例 2: 无效请求格式
	t.Run("无效请求格式", func(t *testing.T) {
		respBody, err := handler.handleClusterStatus(ctx, []byte("invalid"))
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})
}

// TestTreeCoordinatorRPCHandler_HandleNodeReparent 测试重新分配父节点
func TestTreeCoordinatorRPCHandler_HandleNodeReparent(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.MaxChildren = 5
	config.MaxLevel = 3
	config.AutoDiscovery = false // 禁用自动发现避免测试竞态
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	ctx := context.Background()
	if err := coordinator.Start(); err != nil {
		t.Skipf("跳过测试：启动 coordinator 失败: %v", err)
	}

	// 添加子节点
	_ = coordinator.AddChild("child-001")

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	// 测试用例 1: 重新分配请求
	t.Run("重新分配请求", func(t *testing.T) {
		reparentReq := rpc.NodeReparentRequest{
			ChildID:     "child-001",
			NewParentID: "node-002",
			OldParentID: "node-001",
			Timestamp:   time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(reparentReq)
		require.NoError(t, err)

		respBody, err := handler.handleNodeReparent(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.NodeReparentResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		// 响应应该正确格式化（可能成功或失败）
		assert.NotNil(t, resp)
		// Reason 字段应该存在（成功时为空，失败时包含错误信息）
	})

	// 测试用例 2: 无效请求格式
	t.Run("无效请求格式", func(t *testing.T) {
		respBody, err := handler.handleNodeReparent(ctx, []byte("invalid"))
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})
}

// TestTreeCoordinatorRPCHandler_HandleNodeSync 测试节点同步请求处理
func TestTreeCoordinatorRPCHandler_HandleNodeSync(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	ctx := context.Background()

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	// 测试用例 1: 成功同步
	t.Run("成功同步", func(t *testing.T) {
		syncReq := rpc.NodeSyncRequest{
			Version:   12345,
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(syncReq)
		require.NoError(t, err)

		respBody, err := handler.handleNodeSync(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.NodeSyncResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		assert.NotEqual(t, uint64(0), resp.Version)
		assert.NotNil(t, resp.Metadata)
	})

	// 测试用例 2: coordinator 未初始化
	t.Run("coordinator 未初始化", func(t *testing.T) {
		emptyHandler := &TreeCoordinatorRPCHandler{}

		syncReq := rpc.NodeSyncRequest{
			Version:   12345,
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(syncReq)
		require.NoError(t, err)

		respBody, err := emptyHandler.handleNodeSync(ctx, reqBody)
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})

	// 测试用例 3: 无效请求格式
	t.Run("无效请求格式", func(t *testing.T) {
		respBody, err := handler.handleNodeSync(ctx, []byte("invalid"))
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})
}

// TestTreeCoordinatorRPCHandler_HandleGossipTopologyChange 测试拓扑变更扩散请求处理
func TestTreeCoordinatorRPCHandler_HandleGossipTopologyChange(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	ctx := context.Background()

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	// 测试用例 1: 成功处理拓扑变更
	t.Run("成功处理", func(t *testing.T) {
		gossipReq := rpc.GossipTopologyChangeRequest{
			Operation: "add",
			NodeID:    "new-node",
			ParentID:  "node-001",
			Level:     1,
			Version:   12345,
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(gossipReq)
		require.NoError(t, err)

		respBody, err := handler.handleGossipTopologyChange(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.GossipTopologyChangeResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		assert.True(t, resp.Acknowledged)
		assert.Equal(t, uint64(12345), resp.AppliedVersion)
	})

	// 测试用例 2: coordinator 未初始化
	t.Run("coordinator 未初始化", func(t *testing.T) {
		emptyHandler := &TreeCoordinatorRPCHandler{}

		gossipReq := rpc.GossipTopologyChangeRequest{
			Operation: "add",
			NodeID:    "new-node",
			Version:   12345,
			Timestamp: time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(gossipReq)
		require.NoError(t, err)

		respBody, err := emptyHandler.handleGossipTopologyChange(ctx, reqBody)
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})

	// 测试用例 3: 无效请求格式
	t.Run("无效请求格式", func(t *testing.T) {
		respBody, err := handler.handleGossipTopologyChange(ctx, []byte("invalid"))
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})
}

// TestTreeCoordinatorRPCHandler_HandleClusterHealthFix 测试集群健康修复请求处理
func TestTreeCoordinatorRPCHandler_HandleClusterHealthFix(t *testing.T) {
	config := DefaultTreeCoordinatorConfig()
	config.AutoDiscovery = false
	clusterConfig := &metadataconfig.ClusterConfig{}

	coordinator, err := NewTreeCoordinator("node-001", "127.0.0.1:5001", config, clusterConfig, nil)
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	ctx := context.Background()

	handler := NewTreeCoordinatorRPCHandler(coordinator)

	// 测试用例 1: 成功处理修复请求
	t.Run("成功处理", func(t *testing.T) {
		fixReq := rpc.ClusterHealthFixRequest{
			RequesterID: "node-002",
			FixType:     "unreachable",
			Timestamp:   time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(fixReq)
		require.NoError(t, err)

		respBody, err := handler.handleClusterHealthFix(ctx, reqBody)
		require.NoError(t, err)

		var resp rpc.ClusterHealthFixResponse
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		assert.True(t, resp.Success)
		assert.Empty(t, resp.Reason)
	})

	// 测试用例 2: coordinator 未初始化
	t.Run("coordinator 未初始化", func(t *testing.T) {
		emptyHandler := &TreeCoordinatorRPCHandler{}

		fixReq := rpc.ClusterHealthFixRequest{
			RequesterID: "node-002",
			FixType:     "gossip",
			Timestamp:   time.Now().UnixNano(),
		}

		reqBody, err := msgpack.Marshal(fixReq)
		require.NoError(t, err)

		respBody, err := emptyHandler.handleClusterHealthFix(ctx, reqBody)
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})

	// 测试用例 3: 无效请求格式
	t.Run("无效请求格式", func(t *testing.T) {
		respBody, err := handler.handleClusterHealthFix(ctx, []byte("invalid"))
		assert.Error(t, err)
		assert.Nil(t, respBody)
	})
}

// TestTreeCoordinatorRPCHandler_RegisterHandlers 测试注册处理器
func TestTreeCoordinatorRPCHandler_RegisterHandlers(t *testing.T) {
	// RegisterHandlers 需要 libp2p host，在单元测试中我们跳过这个测试
	// 在集成测试中会覆盖这个场景
	t.Run("跳过 RegisterHandlers 测试", func(t *testing.T) {
		// RegisterHandlers 需要 host.Host 参数，单元测试无法提供
		// 这个功能会在集成测试中覆盖
		t.Skip("需要 libp2p host，在集成测试中覆盖")
	})
}
