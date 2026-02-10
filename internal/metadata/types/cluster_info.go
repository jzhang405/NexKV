// Package types 元数据类型定义
//
// 定义了 NexKV 系统中使用的所有强类型元数据结构
package types

import "time"

// ClusterState 集群状态
type ClusterState string

const (
	// ClusterStateInitializing 集群初始化中
	ClusterStateInitializing ClusterState = "initializing"
	// ClusterStateRunning 集群运行中
	ClusterStateRunning ClusterState = "running"
	// ClusterStateDegraded 集群降级运行
	ClusterStateDegraded ClusterState = "degraded"
	// ClusterStateMaintenance 集群维护中
	ClusterStateMaintenance ClusterState = "maintenance"
)

// ClusterInfo 集群元数据
//
// 存储集群级别的配置和状态信息
type ClusterInfo struct {
	// ClusterID 集群唯一标识
	ClusterID string `msgpack:"cluster_id"`

	// ClusterName 集群名称
	ClusterName string `msgpack:"cluster_name"`

	// ClusterVersion 集群版本（语义化版本）
	ClusterVersion string `msgpack:"cluster_version"`

	// State 集群状态
	State ClusterState `msgpack:"state"`

	// RootNodeIDs 根节点 ID 列表
	RootNodeIDs []string `msgpack:"root_node_ids"`

	// TreeDepth 树深度（最大层级）
	TreeDepth int `msgpack:"tree_depth"`

	// TotalNodes 总节点数
	TotalNodes int `msgpack:"total_nodes"`

	// TotalShards 总分片数
	TotalShards int `msgpack:"total_shards"`

	// QuorumThreshold Quorum 阈值（多数派）
	QuorumThreshold int `msgpack:"quorum_threshold"`

	// GossipInterval Gossip 间隔（纳秒）
	GossipInterval int64 `msgpack:"gossip_interval"`

	// CreatedAt 集群创建时间
	CreatedAt time.Time `msgpack:"created_at"`

	// UpdatedAt 最后更新时间
	UpdatedAt time.Time `msgpack:"updated_at"`

	// Version MVCC 版本号
	Version uint64 `msgpack:"version"`
}
