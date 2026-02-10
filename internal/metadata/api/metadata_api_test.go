// Package api 元数据 API 单元测试
package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	store "github.com/jzhang405/NexKV/internal/wal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMVStoreForAPI 模拟 MVStore
type mockMVStoreForAPI struct {
	mu     sync.RWMutex
	data   map[string][]byte
	closed bool
}

func newMockMVStoreForAPI() *mockMVStoreForAPI {
	return &mockMVStoreForAPI{
		data: make(map[string][]byte),
	}
}

func (m *mockMVStoreForAPI) Put(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return kvstore.ErrStoreClosed
	}
	m.data[key] = value
	return nil
}

func (m *mockMVStoreForAPI) Get(key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, kvstore.ErrStoreClosed
	}
	val, ok := m.data[key]
	if !ok {
		return nil, kvstore.ErrKeyNotFound
	}
	return val, nil
}

func (m *mockMVStoreForAPI) GetVersion(key string, hlcTimestamp *clock.HLC) ([]byte, error) {
	return m.Get(key)
}

func (m *mockMVStoreForAPI) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return kvstore.ErrStoreClosed
	}
	delete(m.data, key)
	return nil
}

func (m *mockMVStoreForAPI) Exists(key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return false, kvstore.ErrStoreClosed
	}
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockMVStoreForAPI) List(offset, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, kvstore.ErrStoreClosed
	}
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *mockMVStoreForAPI) ListPrefix(prefix string, offset, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, kvstore.ErrStoreClosed
	}
	keys := make([]string, 0)
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *mockMVStoreForAPI) GetVersionCount(key string) (int, error) {
	return 1, nil
}

func (m *mockMVStoreForAPI) GetAllVersions(key string) ([]*store.VersionInfo, error) {
	return nil, nil
}

func (m *mockMVStoreForAPI) Flush() error {
	return nil
}

func (m *mockMVStoreForAPI) CreateSnapshot() ([]byte, error) {
	return nil, nil
}

func (m *mockMVStoreForAPI) RestoreFromSnapshot(snapshot []byte) error {
	return nil
}

func (m *mockMVStoreForAPI) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// TestMetadataAPI_ClusterOperations 测试集群操作
func TestMetadataAPI_ClusterOperations(t *testing.T) {
	store := newMockMVStoreForAPI()
	kv, err := kvstore.NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	api := NewMetadataAPI(kv)
	ctx := context.Background()

	// 设置集群信息
	info := &types.ClusterInfo{
		ClusterID:       "cluster-001",
		ClusterName:     "test-cluster",
		ClusterVersion:  "1.0.0",
		State:           types.ClusterStateRunning,
		RootNodeIDs:     []string{"node-001", "node-002"},
		TreeDepth:       3,
		TotalNodes:      10,
		TotalShards:     100,
		QuorumThreshold: 3,
		GossipInterval:  10000000000,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		Version:         12345,
	}

	err = api.SetClusterInfo(ctx, info)
	require.NoError(t, err)

	// 获取集群信息
	retrieved, err := api.GetClusterInfo(ctx, "cluster-001")
	require.NoError(t, err)

	assert.Equal(t, "cluster-001", retrieved.ClusterID)
	assert.Equal(t, "test-cluster", retrieved.ClusterName)
	assert.Equal(t, "1.0.0", retrieved.ClusterVersion)
	assert.Equal(t, types.ClusterStateRunning, retrieved.State)
	assert.Len(t, retrieved.RootNodeIDs, 2)
	assert.Equal(t, 3, retrieved.TreeDepth)
}

// TestMetadataAPI_NodeOperations 测试节点操作
func TestMetadataAPI_NodeOperations(t *testing.T) {
	store := newMockMVStoreForAPI()
	kv, err := kvstore.NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	api := NewMetadataAPI(kv)
	ctx := context.Background()

	// 添加节点
	node1 := &types.NodeInfo{
		NodeID:   "node-001",
		HostID:   "host-001",
		Role:     types.NodeRoleLeaf,
		Addr:     types.NodeAddress{Host: "192.168.1.10", TCPPort: 9000},
		ParentID: "parent-001",
		Level:    2,
		Status:   types.NodeStatusReady,
		Priority: 0,
		Version:  100,
	}

	node2 := &types.NodeInfo{
		NodeID:   "node-002",
		HostID:   "host-002",
		Role:     types.NodeRoleLeaf,
		Addr:     types.NodeAddress{Host: "192.168.1.11", TCPPort: 9000},
		ParentID: "parent-001",
		Level:    2,
		Status:   types.NodeStatusReady,
		Priority: 0,
		Version:  101,
	}

	err = api.SetNodeInfo(ctx, node1)
	require.NoError(t, err)

	err = api.SetNodeInfo(ctx, node2)
	require.NoError(t, err)

	// 列出所有节点
	nodes, err := api.ListNodes(ctx)
	require.NoError(t, err)
	assert.Len(t, nodes, 2)

	// 验证节点 ID 集合
	nodeIDs := make(map[string]bool)
	for _, node := range nodes {
		nodeIDs[node.NodeID] = true
	}
	assert.True(t, nodeIDs["node-001"])
	assert.True(t, nodeIDs["node-002"])
}

// TestMetadataAPI_RoleOperations 测试角色操作
func TestMetadataAPI_RoleOperations(t *testing.T) {
	store := newMockMVStoreForAPI()
	kv, err := kvstore.NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	api := NewMetadataAPI(kv)
	ctx := context.Background()

	// 设置角色信息
	roleInfo := &types.RoleInfo{
		RoleID:         "role-parent-001",
		RoleType:       types.RoleTypeParent,
		ActiveNodes:    []string{"node-001", "node-002"},
		StandbyNodes:   []string{"node-003", "node-004"},
		CurrentPrimary: "node-001",
		Version:        200,
	}

	err = api.SetRoleInfo(ctx, roleInfo)
	require.NoError(t, err)

	// 获取角色信息
	retrieved, err := api.GetRoleInfo(ctx, "role-parent-001")
	require.NoError(t, err)

	assert.Equal(t, "role-parent-001", retrieved.RoleID)
	assert.Equal(t, types.RoleTypeParent, retrieved.RoleType)
	assert.Len(t, retrieved.ActiveNodes, 2)
	assert.Len(t, retrieved.StandbyNodes, 2)
	assert.Equal(t, "node-001", retrieved.CurrentPrimary)
}

// TestMetadataAPI_ShardOperations 测试分片操作
func TestMetadataAPI_ShardOperations(t *testing.T) {
	store := newMockMVStoreForAPI()
	kv, err := kvstore.NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	api := NewMetadataAPI(kv)
	ctx := context.Background()

	// 添加分片
	shard1 := &types.ShardInfo{
		ShardID:      "shard-001",
		RangeStart:   "key-00000",
		RangeEnd:     "key-10000",
		ReplicaNodes: []string{"node-001", "node-002", "node-003"},
		State:        types.ShardStateActive,
		PrimaryNode:  "node-001",
		Version:      300,
	}

	shard2 := &types.ShardInfo{
		ShardID:      "shard-002",
		RangeStart:   "key-10000",
		RangeEnd:     "key-20000",
		ReplicaNodes: []string{"node-002", "node-003", "node-004"},
		State:        types.ShardStateActive,
		PrimaryNode:  "node-002",
		Version:      301,
	}

	err = api.SetShardInfo(ctx, shard1)
	require.NoError(t, err)

	err = api.SetShardInfo(ctx, shard2)
	require.NoError(t, err)

	// 列出所有分片
	shards, err := api.ListShards(ctx)
	require.NoError(t, err)
	assert.Len(t, shards, 2)

	// 按分片 ID 查找（避免依赖返回顺序）
	var shard001 *types.ShardInfo
	for _, s := range shards {
		if s.ShardID == "shard-001" {
			shard001 = s
			break
		}
	}
	require.NotNil(t, shard001)

	// 验证分片范围
	assert.True(t, shard001.IsInRange("key-05000"))
	assert.False(t, shard001.IsInRange("key-15000"))
}

// TestMetadataAPI_TopologyOperations 测试拓扑操作
func TestMetadataAPI_TopologyOperations(t *testing.T) {
	store := newMockMVStoreForAPI()
	kv, err := kvstore.NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	api := NewMetadataAPI(kv)
	ctx := context.Background()

	// 设置拓扑信息
	topoInfo := &types.TopologyInfo{
		NodeID:      "node-001",
		ParentID:    "parent-001",
		ChildrenIDs: []string{"child-001", "child-002"},
		Level:       1,
		Version:     400,
	}

	err = api.SetTopologyInfo(ctx, topoInfo)
	require.NoError(t, err)

	// 获取拓扑信息
	retrieved, err := api.GetTopologyInfo(ctx, "node-001")
	require.NoError(t, err)

	assert.Equal(t, "node-001", retrieved.NodeID)
	assert.Equal(t, "parent-001", retrieved.ParentID)
	assert.Len(t, retrieved.ChildrenIDs, 2)
	assert.Equal(t, 1, retrieved.Level)

	// 测试辅助方法
	assert.False(t, retrieved.IsRoot())
	assert.False(t, retrieved.IsLeaf())

	// 测试子节点操作
	retrieved.AddChild("child-003")
	assert.Len(t, retrieved.ChildrenIDs, 3)

	retrieved.RemoveChild("child-001")
	assert.Len(t, retrieved.ChildrenIDs, 2)
}

// TestMetadataAPI_OperationOperations 测试操作记录操作
func TestMetadataAPI_OperationOperations(t *testing.T) {
	store := newMockMVStoreForAPI()
	kv, err := kvstore.NewMetadataKV(store, nil)
	require.NoError(t, err)
	defer kv.Close()

	api := NewMetadataAPI(kv)
	ctx := context.Background()

	// 添加操作记录
	opInfo := &types.OperationInfo{
		OpID:      "op-20250210-001",
		OpType:    types.OperationTypeShardMigration,
		ShardID:   "shard-001",
		Status:    types.OperationStatusRunning,
		Progress:  50,
		StartTime: time.Now(),
		Version:   500,
	}

	err = api.SetOperationInfo(ctx, opInfo)
	require.NoError(t, err)

	// 获取操作记录
	retrieved, err := api.GetOperationInfo(ctx, "op-20250210-001")
	require.NoError(t, err)

	assert.Equal(t, "op-20250210-001", retrieved.OpID)
	assert.Equal(t, types.OperationTypeShardMigration, retrieved.OpType)
	assert.Equal(t, types.OperationStatusRunning, retrieved.Status)
	assert.Equal(t, 50, retrieved.Progress)

	// 测试辅助方法
	assert.False(t, retrieved.IsCompleted())
}
