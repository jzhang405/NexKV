// Package api 元数据高层 API 接口
//
// 提供类型安全的元数据访问接口
package api

import (
	"context"
	"fmt"

	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// MetadataAPI 元数据统一 API
//
// 提供所有类型元数据的访问接口，类型安全且易于使用
type MetadataAPI struct {
	kv *kvstore.MetadataKV
}

// NewMetadataAPI 创建元数据 API
func NewMetadataAPI(kv *kvstore.MetadataKV) *MetadataAPI {
	return &MetadataAPI{
		kv: kv,
	}
}

// ========================================
// 集群操作
// ========================================

// GetClusterInfo 获取集群信息
func (m *MetadataAPI) GetClusterInfo(ctx context.Context, clusterID string) (*types.ClusterInfo, error) {
	var info types.ClusterInfo
	err := m.kv.Get(ctx, kvstore.NamespaceCluster, clusterID, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster info: %w", err)
	}
	return &info, nil
}

// SetClusterInfo 设置集群信息
func (m *MetadataAPI) SetClusterInfo(ctx context.Context, info *types.ClusterInfo) error {
	if info == nil {
		return fmt.Errorf("cluster info cannot be nil")
	}
	return m.kv.Put(ctx, kvstore.NamespaceCluster, info.ClusterID, info)
}

// ========================================
// 节点操作
// ========================================

// GetNodeInfo 获取节点信息
func (m *MetadataAPI) GetNodeInfo(ctx context.Context, nodeID string) (*types.NodeInfo, error) {
	var info types.NodeInfo
	err := m.kv.Get(ctx, kvstore.NamespaceNode, nodeID, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to get node info: %w", err)
	}
	return &info, nil
}

// SetNodeInfo 设置节点信息
func (m *MetadataAPI) SetNodeInfo(ctx context.Context, info *types.NodeInfo) error {
	if info == nil {
		return fmt.Errorf("node info cannot be nil")
	}
	return m.kv.Put(ctx, kvstore.NamespaceNode, info.NodeID, info)
}

// ListNodes 列出所有节点
func (m *MetadataAPI) ListNodes(ctx context.Context) ([]*types.NodeInfo, error) {
	keys, err := m.kv.ListPrefix(ctx, kvstore.NamespaceNode, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	nodes := make([]*types.NodeInfo, 0, len(keys))
	for _, nodeID := range keys {
		info, err := m.GetNodeInfo(ctx, nodeID)
		if err != nil {
			continue // 跳过无法读取的节点
		}
		nodes = append(nodes, info)
	}

	return nodes, nil
}

// DeleteNode 删除节点
func (m *MetadataAPI) DeleteNode(ctx context.Context, nodeID string) error {
	return m.kv.Delete(ctx, kvstore.NamespaceNode, nodeID)
}

// ========================================
// 角色操作
// ========================================

// GetRoleInfo 获取角色信息
func (m *MetadataAPI) GetRoleInfo(ctx context.Context, roleID string) (*types.RoleInfo, error) {
	var info types.RoleInfo
	err := m.kv.Get(ctx, kvstore.NamespaceRole, roleID, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to get role info: %w", err)
	}
	return &info, nil
}

// SetRoleInfo 设置角色信息
func (m *MetadataAPI) SetRoleInfo(ctx context.Context, info *types.RoleInfo) error {
	if info == nil {
		return fmt.Errorf("role info cannot be nil")
	}
	return m.kv.Put(ctx, kvstore.NamespaceRole, info.RoleID, info)
}

// ListRoles 列出所有角色
func (m *MetadataAPI) ListRoles(ctx context.Context) ([]*types.RoleInfo, error) {
	keys, err := m.kv.ListPrefix(ctx, kvstore.NamespaceRole, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list roles: %w", err)
	}

	roles := make([]*types.RoleInfo, 0, len(keys))
	for _, roleID := range keys {
		info, err := m.GetRoleInfo(ctx, roleID)
		if err != nil {
			continue
		}
		roles = append(roles, info)
	}

	return roles, nil
}

// ========================================
// 拓扑操作
// ========================================

// GetTopologyInfo 获取拓扑信息
func (m *MetadataAPI) GetTopologyInfo(ctx context.Context, nodeID string) (*types.TopologyInfo, error) {
	var info types.TopologyInfo
	err := m.kv.Get(ctx, kvstore.NamespaceTopo, nodeID, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to get topology info: %w", err)
	}
	return &info, nil
}

// SetTopologyInfo 设置拓扑信息
func (m *MetadataAPI) SetTopologyInfo(ctx context.Context, info *types.TopologyInfo) error {
	if info == nil {
		return fmt.Errorf("topology info cannot be nil")
	}
	return m.kv.Put(ctx, kvstore.NamespaceTopo, info.NodeID, info)
}

// ========================================
// 分片操作
// ========================================

// GetShardInfo 获取分片信息
func (m *MetadataAPI) GetShardInfo(ctx context.Context, shardID string) (*types.ShardInfo, error) {
	var info types.ShardInfo
	err := m.kv.Get(ctx, kvstore.NamespaceShard, shardID, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to get shard info: %w", err)
	}
	return &info, nil
}

// SetShardInfo 设置分片信息
func (m *MetadataAPI) SetShardInfo(ctx context.Context, info *types.ShardInfo) error {
	if info == nil {
		return fmt.Errorf("shard info cannot be nil")
	}
	return m.kv.Put(ctx, kvstore.NamespaceShard, info.ShardID, info)
}

// ListShards 列出所有分片
func (m *MetadataAPI) ListShards(ctx context.Context) ([]*types.ShardInfo, error) {
	keys, err := m.kv.ListPrefix(ctx, kvstore.NamespaceShard, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list shards: %w", err)
	}

	shards := make([]*types.ShardInfo, 0, len(keys))
	for _, shardID := range keys {
		info, err := m.GetShardInfo(ctx, shardID)
		if err != nil {
			continue
		}
		shards = append(shards, info)
	}

	return shards, nil
}

// ========================================
// 操作记录操作
// ========================================

// GetOperationInfo 获取操作记录
func (m *MetadataAPI) GetOperationInfo(ctx context.Context, opID string) (*types.OperationInfo, error) {
	var info types.OperationInfo
	err := m.kv.Get(ctx, kvstore.NamespaceOp, opID, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to get operation info: %w", err)
	}
	return &info, nil
}

// SetOperationInfo 设置操作记录
func (m *MetadataAPI) SetOperationInfo(ctx context.Context, info *types.OperationInfo) error {
	if info == nil {
		return fmt.Errorf("operation info cannot be nil")
	}
	return m.kv.Put(ctx, kvstore.NamespaceOp, info.OpID, info)
}

// ListOperations 列出所有操作
func (m *MetadataAPI) ListOperations(ctx context.Context) ([]*types.OperationInfo, error) {
	keys, err := m.kv.ListPrefix(ctx, kvstore.NamespaceOp, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list operations: %w", err)
	}

	ops := make([]*types.OperationInfo, 0, len(keys))
	for _, opID := range keys {
		info, err := m.GetOperationInfo(ctx, opID)
		if err != nil {
			continue
		}
		ops = append(ops, info)
	}

	return ops, nil
}
