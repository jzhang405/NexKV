// Package cluster TreeCoordinator RPC Handlers
//
// 实现 TreeCoordinator 的 RPC 请求处理器
// 处理来自其他节点的 RPC 请求，实现节点间的协作
//
// PR-Libp2p-RPC: 使用新的 RPC 框架实现
package cluster

import (
	"context"
	"fmt"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/rpc"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// TreeCoordinatorRPCHandler RPC 请求处理器
// ========================================

// TreeCoordinatorRPCHandler TreeCoordinator RPC 请求处理器
//
// PR-Libp2p-RPC: 实现各个 RPC 方法的处理器
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
func (h *TreeCoordinatorRPCHandler) SetCoordinator(coordinator *TreeCoordinator) {
	h.coordinator = coordinator
}

// RegisterHandlers 注册所有 RPC 方法到 RPC Server
//
// PR-Libp2p-RPC: 将所有处理器注册到 rpc.Server
func (h *TreeCoordinatorRPCHandler) RegisterHandlers(server *rpc.Server) error {
	methods := []struct {
		name    string
		handler rpc.RPCHandler
	}{
		// 节点管理
		{"NodeJoin", h.handleNodeJoin},
		{"NodeLeave", h.handleNodeLeave},
		{"NodeReparent", h.handleNodeReparent},

		// 心跳和状态
		{"NodePing", h.handleNodePing},
		{"ClusterStatus", h.handleClusterStatus},

		// 拓扑同步
		{"NodeSync", h.handleNodeSync},
		{"GossipTopologyChange", h.handleGossipTopologyChange},

		// 集群健康
		{"ClusterHealthFix", h.handleClusterHealthFix},
	}

	for _, method := range methods {
		if err := server.RegisterHandler(method.name, method.handler); err != nil {
			return fmt.Errorf("注册 %s 处理器失败: %w", method.name, err)
		}
		logging.WithField("method", method.name).Info("RPC 方法已注册")
	}

	return nil
}

// ========================================
// 节点管理方法
// ========================================

// handleNodeJoin 处理节点加入请求
func (h *TreeCoordinatorRPCHandler) handleNodeJoin(ctx context.Context, req []byte) ([]byte, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator 未初始化")
	}

	// 解析请求
	var joinReq rpc.NodeJoinRequest
	if err := msgpack.Unmarshal(req, &joinReq); err != nil {
		return nil, fmt.Errorf("解析 NodeJoinRequest 失败: %w", err)
	}

	logging.WithFields(map[string]any{
		"node_id": joinReq.NodeID,
		"addr":    joinReq.Addr,
	}).Info("收到节点加入请求")

	// 检查子节点数量
	if len(h.coordinator.localNode.ChildrenIDs) >= h.coordinator.config.MaxChildren {
		resp := rpc.NodeJoinResponse{
			Accepted:  false,
			ParentID:  "",
			Level:     0,
			Reason:    fmt.Sprintf("子节点数量已达上限 %d", h.coordinator.config.MaxChildren),
			Timestamp: joinReq.Timestamp,
		}
		return msgpack.Marshal(resp)
	}

	// 检查层级限制
	newChildLevel := h.coordinator.localNode.Level + 1
	if newChildLevel > h.coordinator.config.MaxLevel {
		resp := rpc.NodeJoinResponse{
			Accepted:  false,
			ParentID:  "",
			Level:     0,
			Reason:    fmt.Sprintf("超出树的最大深度限制 %d", h.coordinator.config.MaxLevel),
			Timestamp: joinReq.Timestamp,
		}
		return msgpack.Marshal(resp)
	}

	// 添加子节点
	if err := h.coordinator.AddChild(joinReq.NodeID); err != nil {
		resp := rpc.NodeJoinResponse{
			Accepted:  false,
			ParentID:  "",
			Level:     0,
			Reason:    err.Error(),
			Timestamp: joinReq.Timestamp,
		}
		return msgpack.Marshal(resp)
	}

	// 构造成功响应
	resp := rpc.NodeJoinResponse{
		Accepted:  true,
		ParentID:  h.coordinator.localNode.NodeID,
		Level:     newChildLevel,
		Reason:    "",
		Timestamp: joinReq.Timestamp,
	}

	logging.WithFields(map[string]any{
		"node_id": joinReq.NodeID,
		"level":   newChildLevel,
	}).Info("接受节点加入请求")

	return msgpack.Marshal(resp)
}

// handleNodeLeave 处理节点离开请求
func (h *TreeCoordinatorRPCHandler) handleNodeLeave(ctx context.Context, req []byte) ([]byte, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator 未初始化")
	}

	// 解析请求
	var leaveReq rpc.NodeLeaveRequest
	if err := msgpack.Unmarshal(req, &leaveReq); err != nil {
		return nil, fmt.Errorf("解析 NodeLeaveRequest 失败: %w", err)
	}

	logging.WithField("node_id", leaveReq.NodeID).Info("收到节点离开请求")

	// 移除子节点
	_ = h.coordinator.RemoveChild(leaveReq.NodeID)

	// 构造响应
	resp := rpc.NodeLeaveResponse{
		Acknowledged: true,
		Timestamp:    leaveReq.Timestamp,
	}

	return msgpack.Marshal(resp)
}

// handleNodeReparent 处理重新分配父节点请求
func (h *TreeCoordinatorRPCHandler) handleNodeReparent(ctx context.Context, req []byte) ([]byte, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator 未初始化")
	}

	// 解析请求
	var reparentReq rpc.NodeReparentRequest
	if err := msgpack.Unmarshal(req, &reparentReq); err != nil {
		return nil, fmt.Errorf("解析 NodeReparentRequest 失败: %w", err)
	}

	logging.WithFields(map[string]any{
		"child_id":      reparentReq.ChildID,
		"new_parent_id": reparentReq.NewParentID,
		"old_parent_id": reparentReq.OldParentID,
	}).Info("收到重新分配父节点请求")

	// 执行重新分配
	err := h.coordinator.ReparentChild(reparentReq.ChildID, reparentReq.NewParentID, reparentReq.OldParentID)

	// 获取新层级
	var newLevel int
	if node, exists := h.coordinator.allNodes[reparentReq.ChildID]; exists {
		newLevel = node.Level
	}

	// 构造响应
	resp := rpc.NodeReparentResponse{
		Success:   err == nil,
		NewLevel:  newLevel,
		Reason:    "",
		Timestamp: reparentReq.Timestamp,
	}

	if err != nil {
		resp.Reason = err.Error()
	}

	return msgpack.Marshal(resp)
}

// ========================================
// 心跳和状态方法
// ========================================

// handleNodePing 处理心跳请求
func (h *TreeCoordinatorRPCHandler) handleNodePing(ctx context.Context, req []byte) ([]byte, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator 未初始化")
	}

	// 解析请求
	var pingReq rpc.NodePingRequest
	if err := msgpack.Unmarshal(req, &pingReq); err != nil {
		return nil, fmt.Errorf("解析 NodePingRequest 失败: %w", err)
	}

	// 构造响应
	resp := rpc.NodePingResponse{
		Sequence:  pingReq.Sequence,
		Status:    int(h.coordinator.localNode.Status),
		Timestamp: pingReq.Timestamp,
	}

	return msgpack.Marshal(resp)
}

// handleClusterStatus 处理集群状态查询请求
func (h *TreeCoordinatorRPCHandler) handleClusterStatus(ctx context.Context, req []byte) ([]byte, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator 未初始化")
	}

	// 解析请求
	var statusReq rpc.ClusterStatusRequest
	if err := msgpack.Unmarshal(req, &statusReq); err != nil {
		return nil, fmt.Errorf("解析 ClusterStatusRequest 失败: %w", err)
	}

	logging.WithField("requester_id", statusReq.RequesterID).Info("收到集群状态查询请求")

	// 获取拓扑信息
	topology := h.coordinator.GetTopology()

	// 构造节点信息列表
	nodes := make([]rpc.NodeInfo, 0, len(topology))
	for _, node := range topology {
		nodeInfo := rpc.NodeInfo{
			NodeID:   node.NodeID,
			ParentID: node.ParentID,
			Level:    node.Level,
			Status:   int(node.Status),
			Children: node.ChildrenIDs,
		}
		nodes = append(nodes, nodeInfo)
	}

	// 构造响应
	stats := h.coordinator.GetStats()
	resp := rpc.ClusterStatusResponse{
		TotalNodes:  int(stats.TotalNodes.Load()),
		OnlineNodes: int(stats.OnlineNodes.Load()),
		TreeDepth:   int(stats.TreeDepth.Load()),
		Nodes:       nodes,
		Timestamp:   statusReq.Timestamp,
	}

	return msgpack.Marshal(resp)
}

// ========================================
// 拓扑同步方法
// ========================================

// handleNodeSync 处理节点同步请求
func (h *TreeCoordinatorRPCHandler) handleNodeSync(ctx context.Context, req []byte) ([]byte, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator 未初始化")
	}

	// 解析请求
	var syncReq rpc.NodeSyncRequest
	if err := msgpack.Unmarshal(req, &syncReq); err != nil {
		return nil, fmt.Errorf("解析 NodeSyncRequest 失败: %w", err)
	}

	logging.WithFields(map[string]any{
		"requester_id": syncReq.Metadata,
		"version":      syncReq.Version,
	}).Debug("收到节点同步请求")

	// 构造拓扑元数据
	metadata := h.coordinator.buildTopologyMetadata()

	// 构造响应
	resp := rpc.NodeSyncResponse{
		Version:   uint64(h.coordinator.localNode.LastHeartbeat.UnixNano()),
		Metadata:  metadata,
		Timestamp: syncReq.Timestamp,
	}

	return msgpack.Marshal(resp)
}

// handleGossipTopologyChange 处理拓扑变更扩散请求
func (h *TreeCoordinatorRPCHandler) handleGossipTopologyChange(ctx context.Context, req []byte) ([]byte, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator 未初始化")
	}

	// 解析请求
	var gossipReq rpc.GossipTopologyChangeRequest
	if err := msgpack.Unmarshal(req, &gossipReq); err != nil {
		return nil, fmt.Errorf("解析 GossipTopologyChangeRequest 失败: %w", err)
	}

	logging.WithFields(map[string]any{
		"operation": gossipReq.Operation,
		"node_id":   gossipReq.NodeID,
		"version":   gossipReq.Version,
	}).Debug("收到拓扑变更扩散请求")

	// TODO: 根据操作类型更新本地拓扑
	// 这里简化处理，直接确认收到
	// 实际实现中需要根据 operation 类型执行相应的拓扑更新

	// 构造响应
	resp := rpc.GossipTopologyChangeResponse{
		Acknowledged:   true,
		AppliedVersion: gossipReq.Version,
		Timestamp:      gossipReq.Timestamp,
	}

	return msgpack.Marshal(resp)
}

// ========================================
// 集群健康方法
// ========================================

// handleClusterHealthFix 处理集群健康修复请求
func (h *TreeCoordinatorRPCHandler) handleClusterHealthFix(ctx context.Context, req []byte) ([]byte, error) {
	if h.coordinator == nil {
		return nil, fmt.Errorf("coordinator 未初始化")
	}

	// 解析请求
	var fixReq rpc.ClusterHealthFixRequest
	if err := msgpack.Unmarshal(req, &fixReq); err != nil {
		return nil, fmt.Errorf("解析 ClusterHealthFixRequest 失败: %w", err)
	}

	logging.WithFields(map[string]any{
		"requester_id": fixReq.RequesterID,
		"fix_type":     fixReq.FixType,
	}).Info("收到集群健康修复请求")

	// TODO: 根据修复类型执行相应的修复操作
	// 这里简化处理，直接返回成功
	fixedNodes := []string{}

	// 构造响应
	resp := rpc.ClusterHealthFixResponse{
		Success:    true,
		FixedNodes: fixedNodes,
		Reason:     "",
		Timestamp:  fixReq.Timestamp,
	}

	return msgpack.Marshal(resp)
}
