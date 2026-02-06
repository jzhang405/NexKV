// Package cluster TreeCoordinator RPC Handlers
//
// 实现 TreeCoordinator 的 RPC 请求处理器
// 处理来自其他节点的 RPC 请求，实现节点间的协作
//
// ⚠️ PR-Libp2p-TransportCleanup: transport包已删除，RPC Handler暂时禁用
// TODO: 使用 libp2p Stream 重写 RPC 功能后恢复
package cluster

import (
	"context"

	"github.com/jzhang405/NexKV/internal/config/logging"
	// ⚠️ PR-Libp2p-TransportCleanup: transport包已删除
	// "github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// TreeCoordinatorRPCHandler RPC 请求处理器
// ========================================

// TreeCoordinatorRPCHandler TreeCoordinator RPC 请求处理器
//
// ⚠️ PR-Libp2p-TransportCleanup: 暂时禁用，待使用 libp2p Stream 重写
type TreeCoordinatorRPCHandler struct {
	coordinator *TreeCoordinator
}

// NewTreeCoordinatorRPCHandler 创建新的 RPC 处理器
//
// ⚠️ PR-Libp2p-TransportCleanup: 暂时禁用
func NewTreeCoordinatorRPCHandler(coordinator *TreeCoordinator) *TreeCoordinatorRPCHandler {
	logging.Warn("⚠️ TreeCoordinatorRPCHandler 已禁用（PR-Libp2p-TransportCleanup: transport包已删除）")
	return &TreeCoordinatorRPCHandler{
		coordinator: coordinator,
	}
}

// SetCoordinator 设置 coordinator 引用
// ⚠️ PR-Libp2p-TransportCleanup: 暂时禁用
func (h *TreeCoordinatorRPCHandler) SetCoordinator(coordinator *TreeCoordinator) {
	h.coordinator = coordinator
}

// HandleRequest 实现 RPCHandler 接口
//
// ⚠️ PR-Libp2p-TransportCleanup: transport包已删除，Message类型已删除
// TODO: 使用 libp2p Stream 重写 RPC 功能
func (h *TreeCoordinatorRPCHandler) HandleRequest(ctx context.Context, req interface{}) (interface{}, error) {
	if h.coordinator == nil {
		return nil, types.NewTreeCoordinatorNotInitializedError()
	}

	// ⚠️ 暂时返回错误，提示使用 libp2p Stream
	logging.Warn("⚠️ RPC Handler 已禁用（PR-Libp2p-TransportCleanup: 待使用 libp2p Stream 重写）")
	return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError("RPC disabled - transport package removed, waiting for libp2p Stream rewrite")
}

// ========================================
// 以下方法暂时禁用（依赖已删除的 transport 消息类型）
// TODO: 使用 libp2p Stream 重写后恢复
// ========================================

/*
// handleNodeJoin 处理节点加入请求
func (h *TreeCoordinatorRPCHandler) handleNodeJoin(ctx context.Context, msg *transport.NodeJoinMessage) (types.Message, error) {
	// TODO: 使用 libp2p Stream 重写
	return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError("NodeJoin: transport package removed")
}

// handleNodeLeave 处理节点离开请求
func (h *TreeCoordinatorRPCHandler) handleNodeLeave(ctx context.Context, msg *transport.NodeLeaveMessage) (types.Message, error) {
	// TODO: 使用 libp2p Stream 重写
	return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError("NodeLeave: transport package removed")
}

// handleNodeReparent 处理重新建立父子关系请求
func (h *TreeCoordinatorRPCHandler) handleNodeReparent(ctx context.Context, msg *transport.NodeReparentMessage) (types.Message, error) {
	// TODO: 使用 libp2p Stream 重写
	return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError("NodeReparent: transport package removed")
}

// handleClusterStatus 处理集群状态查询请求
func (h *TreeCoordinatorRPCHandler) handleClusterStatus(ctx context.Context, msg *transport.ClusterStatusMessage) (types.Message, error) {
	// TODO: 使用 libp2p Stream 重写
	return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError("ClusterStatus: transport package removed")
}

// handleClusterHealthFix 处理集群健康修复请求
func (h *TreeCoordinatorRPCHandler) handleClusterHealthFix(ctx context.Context, msg *transport.ClusterHealthFixMessage) (types.Message, error) {
	// TODO: 使用 libp2p Stream 重写
	return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError("ClusterHealthFix: transport package removed")
}

// handleNodePing 处理心跳请求
func (h *TreeCoordinatorRPCHandler) handleNodePing(ctx context.Context, msg *transport.NodePingMessage) (types.Message, error) {
	// TODO: 使用 libp2p Stream 重写
	return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError("NodePing: transport package removed")
}

// handleNodeSync 处理节点同步请求
func (h *TreeCoordinatorRPCHandler) handleNodeSync(ctx context.Context, msg *transport.NodeSyncMessage) (types.Message, error) {
	// TODO: 使用 libp2p Stream 重写
	return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError("NodeSync: transport package removed")
}

// fixUnreachableNodes 修复不可达节点
func (h *TreeCoordinatorRPCHandler) fixUnreachableNodes() []string {
	// TODO: 使用 libp2p Stream 重写
	return nil
}

// triggerGossipSync 触发 Gossip 同步
func (h *TreeCoordinatorRPCHandler) triggerGossipSync() []string {
	// TODO: 使用 libp2p Stream 重写
	return nil
}

// fixConfigMismatch 修复配置不一致
func (h *TreeCoordinatorRPCHandler) fixConfigMismatch() []string {
	// TODO: 使用 libp2p Stream 重写
	return nil
}

// getNodeRole 根据节点信息返回角色
func getNodeRole(tc *TreeCoordinator, node *Node) string {
	if node.NodeID == tc.localNode.NodeID {
		return "self"
	}
	if node.ParentID == "" {
		return "root"
	}
	if len(node.ChildrenIDs) > 0 {
		return "parent"
	}
	return "child"
}
*/
