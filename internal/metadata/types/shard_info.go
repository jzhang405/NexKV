// Package types 元数据类型定义
package types

// ShardState 分片状态
type ShardState string

const (
	// ShardStateInitializing 分片初始化中
	ShardStateInitializing ShardState = "initializing"
	// ShardStateActive 分片活跃
	ShardStateActive ShardState = "active"
	// ShardStateMigrating 分片迁移中
	ShardStateMigrating ShardState = "migrating"
	// ShardStateSplitting 分片分裂中
	ShardStateSplitting ShardState = "splitting"
	// ShardStateMerging 分片合并中
	ShardStateMerging ShardState = "merging"
	// ShardStateDraining 分片排空中
	ShardStateDraining ShardState = "draining"
)

// ShardInfo 分片元数据
//
// 存储分片的配置、副本分布和状态信息
type ShardInfo struct {
	// ShardID 分片 ID
	ShardID string `msgpack:"shard_id"`

	// RangeStart 起始键（包含）
	RangeStart string `msgpack:"range_start"`

	// RangeEnd 结束键（不包含）
	RangeEnd string `msgpack:"range_end"`

	// ReplicaNodes 副本节点列表
	ReplicaNodes []string `msgpack:"replica_nodes"`

	// State 分片状态
	State ShardState `msgpack:"state"`

	// PrimaryNode 主副本节点 ID
	PrimaryNode string `msgpack:"primary_node"`

	// MigrationStatus 迁移状态（可选）
	MigrationStatus *ShardMigrationStatus `msgpack:"migration_status,omitempty"`

	// Version MVCC 版本号
	Version uint64 `msgpack:"version"`
}

// ShardMigrationStatus 分片迁移状态
type ShardMigrationStatus struct {
	// SourceNode 源节点 ID
	SourceNode string `msgpack:"source_node"`

	// TargetNode 目标节点 ID
	TargetNode string `msgpack:"target_node"`

	// Progress 迁移进度（0-100）
	Progress int `msgpack:"progress"`

	// StartTime 开始时间
	StartTime int64 `msgpack:"start_time"`

	// EstimatedEndTime 预计结束时间
	EstimatedEndTime int64 `msgpack:"estimated_end_time"`
}

// IsInRange 检查键是否在分片范围内
func (s *ShardInfo) IsInRange(key string) bool {
	return key >= s.RangeStart && (s.RangeEnd == "" || key < s.RangeEnd)
}

// HasReplica 检查节点是否为副本
func (s *ShardInfo) HasReplica(nodeID string) bool {
	for _, replica := range s.ReplicaNodes {
		if replica == nodeID {
			return true
		}
	}
	return false
}
