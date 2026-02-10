// Package types 元数据类型定义
package types

import "time"

// NodeRole 节点角色
type NodeRole string

const (
	// NodeRoleLeaf 叶子节点（存储数据）
	NodeRoleLeaf NodeRole = "Leaf"
	// NodeRoleParent 父节点（协调数据分布）
	NodeRoleParent NodeRole = "Parent"
	// NodeRoleParentStandby 备用父节点（故障转移）
	NodeRoleParentStandby NodeRole = "ParentStandby"
)

// NodeStatus 节点状态
type NodeStatus string

const (
	// NodeStatusInit 节点初始化中
	NodeStatusInit NodeStatus = "Init"
	// NodeStatusReady 节点就绪
	NodeStatusReady NodeStatus = "Ready"
	// NodeStatusJoining 节点加入中
	NodeStatusJoining NodeStatus = "Joining"
	// NodeStatusLeaving 节点离开中
	NodeStatusLeaving NodeStatus = "Leaving"
	// NodeStatusFailed 节点故障
	NodeStatusFailed NodeStatus = "Failed"
)

// NodeAddress 节点网络地址
type NodeAddress struct {
	// Host 主机地址（IP 或域名）
	Host string `msgpack:"host"`

	// TCPPort TCP 端口
	TCPPort int `msgpack:"tcp_port"`

	// UDPPort UDP 端口
	UDPPort int `msgpack:"udp_port"`
}

// NodeInfo 节点元数据
//
// 存储节点的基本信息和状态
type NodeInfo struct {
	// NodeID 节点唯一标识
	NodeID string `msgpack:"node_id"`

	// HostID 所属物理机器 ID
	HostID string `msgpack:"host_id"`

	// Role 节点角色
	Role NodeRole `msgpack:"role"`

	// Addr 节点网络地址
	Addr NodeAddress `msgpack:"addr"`

	// ParentID 父节点 ID
	ParentID string `msgpack:"parent_id"`

	// Level 节点层级
	Level int `msgpack:"level"`

	// Status 节点状态
	Status NodeStatus `msgpack:"status"`

	// Priority 节点优先级（用于故障转移）
	Priority int `msgpack:"priority"`

	// LastHeartbeat 最后心跳时间
	LastHeartbeat time.Time `msgpack:"last_heartbeat"`

	// Version MVCC 版本号
	Version uint64 `msgpack:"version"`
}
