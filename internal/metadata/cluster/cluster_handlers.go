// Package cluster TreeCoordinator RPC Handlers
//
// 实现 TreeCoordinator 的 RPC 请求处理器
// 处理来自其他节点的 RPC 请求，实现节点间的协作
package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// TreeCoordinatorRPCHandler RPC 请求处理器
// ========================================

// TreeCoordinatorRPCHandler TreeCoordinator RPC 请求处理器
//
// 实现 transport.RPCHandler 接口，处理来自其他节点的 RPC 请求
type TreeCoordinatorRPCHandler struct {
	coordinator *TreeCoordinator
}

// NewTreeCoordinatorRPCHandler 创建新的 RPC 处理器
func NewTreeCoordinatorRPCHandler(coordinator *TreeCoordinator) *TreeCoordinatorRPCHandler {
	return &TreeCoordinatorRPCHandler{
		coordinator: coordinator,
	}
}

// SetCoordinator 设置 coordinator 引用
// 用于在创建 coordinator 后更新 handler 的引用
func (h *TreeCoordinatorRPCHandler) SetCoordinator(coordinator *TreeCoordinator) {
	h.coordinator = coordinator
}

// HandleRequest 实现 RPCHandler 接口
//
// 处理来自其他节点的 RPC 请求，支持的操作：
//   - AddChild: 子节点请求加入
//   - RemoveChild: 子节点请求离开
//   - GetNodeInfo: 查询节点信息
//   - ReparentChild: 重新建立父子关系
func (h *TreeCoordinatorRPCHandler) HandleRequest(ctx context.Context, req types.Message) (types.Message, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator not initialized")
	}

	switch msg := req.(type) {
	case *transport.NodeJoinMessage:
		// 处理节点加入请求（子节点请求加入本地节点）
		return h.handleNodeJoin(ctx, msg)

	case *transport.NodeLeaveMessage:
		// 处理节点离开请求
		return h.handleNodeLeave(ctx, msg)

	case *transport.NodeReparentMessage:
		// 处理重新建立父子关系请求
		return h.handleNodeReparent(ctx, msg)

	case *transport.ClusterStatusMessage:
		// 处理集群状态查询请求
		return h.handleClusterStatus(ctx, msg)

	case *transport.NodePingMessage:
		// 处理心跳请求
		return h.handleNodePing(ctx, msg)

	case *transport.NodeSyncMessage:
		// 处理节点同步请求
		return h.handleNodeSync(ctx, msg)

	default:
		return nil, fmt.Errorf("unsupported message type: %T", req)
	}
}

// ========================================
// 具体请求处理方法
// ========================================

// handleNodeJoin 处理节点加入请求
//
// 当一个新节点请求加入时，调用 AddChild 方法将其添加为子节点
func (h *TreeCoordinatorRPCHandler) handleNodeJoin(ctx context.Context, msg *transport.NodeJoinMessage) (types.Message, error) {
	logging.WithFields(map[string]any{
		"node_id": msg.NodeID,
		"addr":    msg.Addr,
		"role":    msg.Role,
	}).Info("收到节点加入请求")

	// 将新节点添加为子节点
	if err := h.coordinator.AddChild(msg.NodeID); err != nil {
		return nil, fmt.Errorf("failed to add child: %w", err)
	}

	// 返回同步消息作为确认
	return &transport.NodeSyncMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeSync},
		Version:     uint64(time.Now().Unix()),
		Metadata: map[string][]byte{
			"parent_node_id": []byte(h.coordinator.localNode.NodeID),
			"timestamp":      []byte(time.Now().Format(time.RFC3339)),
		},
	}, nil
}

// handleNodeLeave 处理节点离开请求
//
// 当一个子节点请求离开时，调用 RemoveChild 方法将其移除
func (h *TreeCoordinatorRPCHandler) handleNodeLeave(ctx context.Context, msg *transport.NodeLeaveMessage) (types.Message, error) {
	logging.WithFields(map[string]any{
		"node_id": msg.NodeID,
		"reason":  msg.Reason,
	}).Info("收到节点离开请求")

	// 从子节点列表中移除
	if err := h.coordinator.RemoveChild(msg.NodeID); err != nil {
		return nil, fmt.Errorf("failed to remove child: %w", err)
	}

	// 返回同步消息作为确认
	return &transport.NodeSyncMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeSync},
		Version:     uint64(time.Now().Unix()),
		Metadata: map[string][]byte{
			"status":    []byte("removed"),
			"timestamp": []byte(time.Now().Format(time.RFC3339)),
		},
	}, nil
}

// handleNodeReparent 处理重新建立父子关系请求
//
// PR-034 实现：当一个节点需要重新分配父节点时调用
func (h *TreeCoordinatorRPCHandler) handleNodeReparent(ctx context.Context, msg *transport.NodeReparentMessage) (types.Message, error) {
	logging.WithFields(map[string]any{
		"child_id":      msg.ChildID,
		"new_parent_id": msg.NewParentID,
		"old_parent_id": msg.OldParentID,
		"reason":        msg.Reason,
	}).Info("收到重新建立父子关系请求")

	// 调用 TreeCoordinator 的 ReparentChild 方法
	err := h.coordinator.ReparentChild(msg.ChildID, msg.NewParentID, msg.OldParentID)
	if err != nil {
		logging.WithFields(map[string]any{
			"child_id":   msg.ChildID,
			"new_parent": msg.NewParentID,
			"error":      err,
		}).Error("重新分配父节点失败")

		return &transport.NodeReparentReplyMessage{
			BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeReparentReply},
			Success:     false,
			Reason:      err.Error(),
		}, nil
	}

	logging.WithFields(map[string]any{
		"child_id":   msg.ChildID,
		"old_parent": msg.OldParentID,
		"new_parent": msg.NewParentID,
	}).Info("重新分配父节点成功")

	return &transport.NodeReparentReplyMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeReparentReply},
		Success:     true,
	}, nil
}

// handleClusterStatus 处理集群状态查询请求
//
// 返回本地节点维护的集群拓扑状态
func (h *TreeCoordinatorRPCHandler) handleClusterStatus(ctx context.Context, msg *transport.ClusterStatusMessage) (types.Message, error) {
	nodes := h.coordinator.ListNodes()
	nodeInfos := make([]transport.NodeInfo, 0, len(nodes))

	for _, node := range nodes {
		nodeInfos = append(nodeInfos, transport.NodeInfo{
			NodeID:   node.NodeID,
			Addr:     node.Addr.TCPAddr(),
			Role:     getNodeRole(h.coordinator, node),
			Status:   node.Status.String(),
			Level:    node.Level,
			ParentID: node.ParentID,
		})
	}

	return &transport.ClusterStatusReplyMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeClusterStatusReply},
		Nodes:       nodeInfos,
	}, nil
}

// handleNodePing 处理心跳请求
//
// 返回心跳响应，用于故障检测
func (h *TreeCoordinatorRPCHandler) handleNodePing(ctx context.Context, msg *transport.NodePingMessage) (types.Message, error) {
	return &transport.NodePongMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePong},
		NodeID:      h.coordinator.localNode.NodeID,
		Sequence:    msg.Sequence,
		Timestamp:   time.Now().Unix(),
		Status:      "ready",
	}, nil
}

// handleNodeSync 处理节点同步请求
//
// 返回本地节点的拓扑信息
func (h *TreeCoordinatorRPCHandler) handleNodeSync(ctx context.Context, msg *transport.NodeSyncMessage) (types.Message, error) {
	nodes := h.coordinator.ListNodes()
	metadata := make(map[string][]byte)

	// 将节点信息序列化为 metadata
	for _, node := range nodes {
		nodeData := fmt.Sprintf("%s|%s|%s|%d|%s",
			node.NodeID, node.Addr.TCPAddr(), node.ParentID, node.Level, node.Status.String())
		metadata[node.NodeID] = []byte(nodeData)
	}

	return &transport.NodeSyncMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeSync},
		Version:     msg.Version + 1,
		Metadata:    metadata,
	}, nil
}

// ========================================
// 辅助函数
// ========================================

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
