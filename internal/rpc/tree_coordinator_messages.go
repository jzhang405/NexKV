// Package rpc TreeCoordinator RPC 消息定义
//
// 定义 TreeCoordinator 节点间通信的 RPC 消息类型
// 使用 MessagePack 序列化
package rpc

// ========================================
// 节点加入/离开
// ========================================

// NodeJoinRequest 节点加入请求
type NodeJoinRequest struct {
	NodeID    string `msgpack:"node_id"`   // 请求加入的节点 ID
	Addr      string `msgpack:"addr"`      // 节点地址（IPFS 格式）
	Role      int    `msgpack:"role"`      // 节点角色（0=Leaf, 1=Parent, 2=ParentStandby）
	Timestamp int64  `msgpack:"timestamp"` // 请求时间戳
}

// NodeJoinResponse 节点加入响应
type NodeJoinResponse struct {
	Accepted  bool   `msgpack:"accepted"`  // 是否接受加入
	ParentID  string `msgpack:"parent_id"` // 分配的父节点 ID
	Level     int    `msgpack:"level"`     // 分配的层级
	Reason    string `msgpack:"reason"`    // 拒绝原因（如果拒绝）
	Timestamp int64  `msgpack:"timestamp"` // 响应时间戳
}

// NodeLeaveRequest 节点离开请求
type NodeLeaveRequest struct {
	NodeID    string `msgpack:"node_id"`   // 离开的节点 ID
	Timestamp int64  `msgpack:"timestamp"` // 请求时间戳
}

// NodeLeaveResponse 节点离开响应
type NodeLeaveResponse struct {
	Acknowledged bool  `msgpack:"acknowledged"` // 是否确认
	Timestamp    int64 `msgpack:"timestamp"`    // 响应时间戳
}

// NodeReparentRequest 重新分配父节点请求
type NodeReparentRequest struct {
	ChildID     string `msgpack:"child_id"`      // 子节点 ID
	NewParentID string `msgpack:"new_parent_id"` // 新父节点 ID
	OldParentID string `msgpack:"old_parent_id"` // 旧父节点 ID
	Timestamp   int64  `msgpack:"timestamp"`     // 请求时间戳
}

// NodeReparentResponse 重新分配父节点响应
type NodeReparentResponse struct {
	Success   bool   `msgpack:"success"`   // 是否成功
	NewLevel  int    `msgpack:"new_level"` // 新层级
	Reason    string `msgpack:"reason"`    // 失败原因（如果失败）
	Timestamp int64  `msgpack:"timestamp"` // 响应时间戳
}

// ========================================
// 心跳检测
// ========================================

// NodePingRequest 心跳请求
type NodePingRequest struct {
	Sequence  uint64 `msgpack:"sequence"`  // 心跳序列号
	Timestamp int64  `msgpack:"timestamp"` // 发送时间戳
}

// NodePingResponse 心跳响应
type NodePingResponse struct {
	Sequence  uint64 `msgpack:"sequence"`  // 回显序列号
	Status    int    `msgpack:"status"`    // 节点状态（0=Init, 1=Ready, 2=Joining, 3=Leaving, 4=Failed）
	Timestamp int64  `msgpack:"timestamp"` // 响应时间戳
}

// ========================================
// 集群状态查询
// ========================================

// ClusterStatusRequest 集群状态查询请求
type ClusterStatusRequest struct {
	RequesterID string `msgpack:"requester_id"` // 请求者节点 ID
	Timestamp   int64  `msgpack:"timestamp"`    // 请求时间戳
}

// ClusterStatusResponse 集群状态查询响应
type ClusterStatusResponse struct {
	TotalNodes  int        `msgpack:"total_nodes"`  // 总节点数
	OnlineNodes int        `msgpack:"online_nodes"` // 在线节点数
	TreeDepth   int        `msgpack:"tree_depth"`   // 树深度
	Nodes       []NodeInfo `msgpack:"nodes"`        // 节点信息列表
	Timestamp   int64      `msgpack:"timestamp"`    // 响应时间戳
}

// NodeInfo 节点信息（用于 ClusterStatus）
type NodeInfo struct {
	NodeID   string   `msgpack:"node_id"`   // 节点 ID
	ParentID string   `msgpack:"parent_id"` // 父节点 ID
	Level    int      `msgpack:"level"`     // 层级
	Status   int      `msgpack:"status"`    // 节点状态
	Children []string `msgpack:"children"`  // 子节点 ID 列表
}

// ========================================
// 拓扑同步
// ========================================

// NodeSyncRequest 拓扑同步请求
type NodeSyncRequest struct {
	Version   uint64            `msgpack:"version"`   // 版本号
	Metadata  map[string][]byte `msgpack:"metadata"`  // 拓扑元数据
	Timestamp int64             `msgpack:"timestamp"` // 请求时间戳
}

// NodeSyncResponse 拓扑同步响应
type NodeSyncResponse struct {
	Version   uint64            `msgpack:"version"`   // 响应版本号
	Metadata  map[string][]byte `msgpack:"metadata"`  // 拓扑元数据
	Timestamp int64             `msgpack:"timestamp"` // 响应时间戳
}

// ========================================
// 集群健康修复
// ========================================

// ClusterHealthFixRequest 集群健康修复请求
type ClusterHealthFixRequest struct {
	RequesterID string `msgpack:"requester_id"` // 请求者节点 ID
	FixType     string `msgpack:"fix_type"`     // 修复类型（"unreachable", "mismatch", "gossip"）
	Timestamp   int64  `msgpack:"timestamp"`    // 请求时间戳
}

// ClusterHealthFixResponse 集群健康修复响应
type ClusterHealthFixResponse struct {
	Success    bool     `msgpack:"success"`     // 是否成功
	FixedNodes []string `msgpack:"fixed_nodes"` // 修复的节点列表
	Reason     string   `msgpack:"reason"`      // 失败原因（如果失败）
	Timestamp  int64    `msgpack:"timestamp"`   // 响应时间戳
}

// ========================================
// 拓扑变更扩散
// ========================================

// GossipTopologyChangeRequest 拓扑变更扩散请求
type GossipTopologyChangeRequest struct {
	Operation string `msgpack:"operation"` // 操作类型（"add", "remove", "reparent"）
	NodeID    string `msgpack:"node_id"`   // 变更的节点 ID
	ParentID  string `msgpack:"parent_id"` // 父节点 ID
	Level     int    `msgpack:"level"`     // 节点层级
	Version   uint64 `msgpack:"version"`   // 版本号
	Timestamp int64  `msgpack:"timestamp"` // 变更时间戳
}

// GossipTopologyChangeResponse 拓扑变更扩散响应
type GossipTopologyChangeResponse struct {
	Acknowledged   bool   `msgpack:"acknowledged"`    // 是否确认
	AppliedVersion uint64 `msgpack:"applied_version"` // 应用的版本号
	Timestamp      int64  `msgpack:"timestamp"`       // 响应时间戳
}

// ========================================
// 请求构造辅助函数
// ========================================

// NewNodeJoinRequest 创建节点加入请求
func NewNodeJoinRequest(nodeID, addr string, role int) *NodeJoinRequest {
	return &NodeJoinRequest{
		NodeID:    nodeID,
		Addr:      addr,
		Role:      role,
		Timestamp: nowTimestamp(),
	}
}

// NewNodeLeaveRequest 创建节点离开请求
func NewNodeLeaveRequest(nodeID string) *NodeLeaveRequest {
	return &NodeLeaveRequest{
		NodeID:    nodeID,
		Timestamp: nowTimestamp(),
	}
}

// NewNodeReparentRequest 创建重新分配父节点请求
func NewNodeReparentRequest(childID, newParentID, oldParentID string) *NodeReparentRequest {
	return &NodeReparentRequest{
		ChildID:     childID,
		NewParentID: newParentID,
		OldParentID: oldParentID,
		Timestamp:   nowTimestamp(),
	}
}

// NewNodePingRequest 创建心跳请求
func NewNodePingRequest(sequence uint64) *NodePingRequest {
	return &NodePingRequest{
		Sequence:  sequence,
		Timestamp: nowTimestamp(),
	}
}

// NewClusterStatusRequest 创建集群状态查询请求
func NewClusterStatusRequest(requesterID string) *ClusterStatusRequest {
	return &ClusterStatusRequest{
		RequesterID: requesterID,
		Timestamp:   nowTimestamp(),
	}
}

// NewGossipTopologyChangeRequest 创建拓扑变更扩散请求
func NewGossipTopologyChangeRequest(operation, nodeID, parentID string, level int, version uint64) *GossipTopologyChangeRequest {
	return &GossipTopologyChangeRequest{
		Operation: operation,
		NodeID:    nodeID,
		ParentID:  parentID,
		Level:     level,
		Version:   version,
		Timestamp: nowTimestamp(),
	}
}

// NewClusterHealthFixRequest 创建集群健康修复请求
func NewClusterHealthFixRequest(requesterID, fixType string) *ClusterHealthFixRequest {
	return &ClusterHealthFixRequest{
		RequesterID: requesterID,
		FixType:     fixType,
		Timestamp:   nowTimestamp(),
	}
}
