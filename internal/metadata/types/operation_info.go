// Package types 元数据类型定义
package types

import "time"

// OperationType 操作类型
type OperationType string

const (
	// OperationTypeShardMigration 分片迁移
	OperationTypeShardMigration OperationType = "shard_migration"
	// OperationTypeShardSplit 分片分裂
	OperationTypeShardSplit OperationType = "shard_split"
	// OperationTypeShardMerge 分片合并
	OperationTypeShardMerge OperationType = "shard_merge"
	// OperationTypeNodeJoin 节点加入
	OperationTypeNodeJoin OperationType = "node_join"
	// OperationTypeNodeLeave 节点离开
	OperationTypeNodeLeave OperationType = "node_leave"
	// OperationTypeRebalance 负载重平衡
	OperationTypeRebalance OperationType = "rebalance"
)

// OperationStatus 操作状态
type OperationStatus string

const (
	// OperationStatusPending 操作等待中
	OperationStatusPending OperationStatus = "pending"
	// OperationStatusRunning 操作运行中
	OperationStatusRunning OperationStatus = "running"
	// OperationStatusCompleted 操作已完成
	OperationStatusCompleted OperationStatus = "completed"
	// OperationStatusFailed 操作失败
	OperationStatusFailed OperationStatus = "failed"
	// OperationStatusCancelled 操作已取消
	OperationStatusCancelled OperationStatus = "cancelled"
)

// OperationInfo 操作记录元数据
//
// 存储集群操作的执行信息和进度
type OperationInfo struct {
	// OpID 操作 ID（格式：op-{timestamp}-{sequence}）
	OpID string `msgpack:"op_id"`

	// OpType 操作类型
	OpType OperationType `msgpack:"op_type"`

	// ShardID 关联的分片 ID（可选）
	ShardID string `msgpack:"shard_id,omitempty"`

	// NodeID 关联的节点 ID（可选）
	NodeID string `msgpack:"node_id,omitempty"`

	// Status 操作状态
	Status OperationStatus `msgpack:"status"`

	// Progress 操作进度（0-100）
	Progress int `msgpack:"progress"`

	// StartTime 开始时间
	StartTime time.Time `msgpack:"start_time"`

	// EndTime 结束时间（可选）
	EndTime *time.Time `msgpack:"end_time,omitempty"`

	// ErrorMessage 错误信息（可选）
	ErrorMessage string `msgpack:"error_message,omitempty"`

	// Version MVCC 版本号
	Version uint64 `msgpack:"version"`
}

// IsCompleted 判断操作是否已完成
func (o *OperationInfo) IsCompleted() bool {
	return o.Status == OperationStatusCompleted ||
		o.Status == OperationStatusFailed ||
		o.Status == OperationStatusCancelled
}

// Duration 返回操作持续时间
func (o *OperationInfo) Duration() time.Duration {
	if o.EndTime != nil && !o.EndTime.IsZero() {
		return o.EndTime.Sub(o.StartTime)
	}
	return time.Since(o.StartTime)
}
