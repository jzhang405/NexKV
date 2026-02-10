// Package types 元数据类型定义
package types

import "time"

// RoleType 角色类型
type RoleType string

const (
	// RoleTypeParent 父节点角色
	RoleTypeParent RoleType = "Parent"
	// RoleTypeLeaf 叶子节点角色
	RoleTypeLeaf RoleType = "Leaf"
)

// FailoverRecord 故障转移记录
type FailoverRecord struct {
	// From 原主节点 ID
	From string `msgpack:"from"`

	// To 新主节点 ID
	To string `msgpack:"to"`

	// Time 故障转移时间
	Time time.Time `msgpack:"time"`

	// Reason 故障转移原因
	Reason string `msgpack:"reason"`

	// Duration 持续时间（纳秒）
	Duration int64 `msgpack:"duration"`
}

// RoleInfo 角色元数据
//
// 存储角色的定义和成员信息，包含 Standby 管理
type RoleInfo struct {
	// RoleID 角色 ID（格式：role-{type}-{id}）
	RoleID string `msgpack:"role_id"`

	// RoleType 角色类型
	RoleType RoleType `msgpack:"role_type"`

	// ActiveNodes 活跃节点列表
	ActiveNodes []string `msgpack:"active_nodes"`

	// StandbyNodes 备用节点列表
	StandbyNodes []string `msgpack:"standby_nodes"`

	// CurrentPrimary 当前主节点 ID
	CurrentPrimary string `msgpack:"current_primary"`

	// LastFailover 最后故障转移时间
	LastFailover time.Time `msgpack:"last_failover"`

	// FailoverHistory 故障转移历史（最近 100 条）
	FailoverHistory []*FailoverRecord `msgpack:"failover_history"`

	// Version MVCC 版本号
	Version uint64 `msgpack:"version"`
}

// AddFailoverRecord 添加故障转移记录
func (r *RoleInfo) AddFailoverRecord(record *FailoverRecord) {
	// 保持最多 100 条记录
	if len(r.FailoverHistory) >= 100 {
		// 移除最旧的记录
		r.FailoverHistory = r.FailoverHistory[1:]
	}
	r.FailoverHistory = append(r.FailoverHistory, record)
	r.LastFailover = record.Time
}
