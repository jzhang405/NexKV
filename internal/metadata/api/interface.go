// Package api 定义元数据 API 接口
//
// 遵循依赖倒置原则：接口由提供者（api）定义，
// 而不是由使用者（cluster）定义。
package api

import (
	"context"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// Provider 元数据提供者接口
//
// 定义了强类型元数据的访问操作，包括：
//   - 节点操作
//   - 角色操作
//   - 拓扑操作
//   - 分片操作
type Provider interface {
	// ========================================
	// 节点操作
	// ========================================

	// GetNodeInfo 获取节点信息
	GetNodeInfo(ctx context.Context, nodeID string) (*types.NodeInfo, error)

	// SetNodeInfo 设置节点信息
	SetNodeInfo(ctx context.Context, info *types.NodeInfo) error

	// ListNodes 列出所有节点
	ListNodes(ctx context.Context) ([]*types.NodeInfo, error)

	// DeleteNode 删除节点
	DeleteNode(ctx context.Context, nodeID string) error

	// ========================================
	// 角色操作
	// ========================================

	// GetRoleInfo 获取角色信息
	GetRoleInfo(ctx context.Context, roleID string) (*types.RoleInfo, error)

	// SetRoleInfo 设置角色信息
	SetRoleInfo(ctx context.Context, info *types.RoleInfo) error

	// ListRoles 列出所有角色
	ListRoles(ctx context.Context) ([]*types.RoleInfo, error)

	// ========================================
	// 拓扑操作
	// ========================================

	// GetTopologyInfo 获取拓扑信息
	GetTopologyInfo(ctx context.Context, nodeID string) (*types.TopologyInfo, error)

	// SetTopologyInfo 设置拓扑信息
	SetTopologyInfo(ctx context.Context, info *types.TopologyInfo) error

	// ========================================
	// 分片操作
	// ========================================

	// GetShardInfo 获取分片信息
	GetShardInfo(ctx context.Context, shardID string) (*types.ShardInfo, error)

	// SetShardInfo 设置分片信息
	SetShardInfo(ctx context.Context, info *types.ShardInfo) error
}
