// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package consistency

import (
	"context"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	walstore "github.com/jzhang405/NexKV/internal/wal"
	"github.com/stretchr/testify/require"
)

// mockMVStoreForQuorum 模拟 MVStore（用于 Quorum 测试）
type mockMVStoreForQuorum struct {
	mu     sync.RWMutex
	data   map[string][]byte
	closed bool
}

func newMockMVStoreForQuorum() *mockMVStoreForQuorum {
	return &mockMVStoreForQuorum{
		data: make(map[string][]byte),
	}
}

func (m *mockMVStoreForQuorum) Put(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return kvstore.ErrStoreClosed
	}
	m.data[key] = value
	return nil
}

func (m *mockMVStoreForQuorum) Get(key string) ([]byte, error) {
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

func (m *mockMVStoreForQuorum) GetVersion(key string, hlcTimestamp *clock.HLC) ([]byte, error) {
	return m.Get(key)
}

func (m *mockMVStoreForQuorum) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return kvstore.ErrStoreClosed
	}
	delete(m.data, key)
	return nil
}

func (m *mockMVStoreForQuorum) Exists(key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return false, kvstore.ErrStoreClosed
	}
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockMVStoreForQuorum) List(offset, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func (m *mockMVStoreForQuorum) ListPrefix(prefix string, offset, limit int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0)
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *mockMVStoreForQuorum) GetVersionCount(key string) (int, error) {
	return 1, nil
}

func (m *mockMVStoreForQuorum) GetAllVersions(key string) ([]*walstore.VersionInfo, error) {
	// 返回空切片，避免未使用导入错误
	return nil, nil
}

func (m *mockMVStoreForQuorum) Flush() error {
	return nil
}

func (m *mockMVStoreForQuorum) CreateSnapshot() ([]byte, error) {
	return []byte{}, nil
}

func (m *mockMVStoreForQuorum) RestoreFromSnapshot(snapshot []byte) error {
	return nil
}

func (m *mockMVStoreForQuorum) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// mockMetadataKVForTree 模拟 MetadataKV（用于树形拓扑测试）
type mockMetadataKVForTree struct {
	*mockMetadataKV
}

func (m *mockMetadataKVForTree) Put(ctx context.Context, ns, key string, value any) error {
	// 简化实现：直接返回成功
	return nil
}

// setupMetadataKVForQuorum 创建用于 Quorum 测试的 MetadataKV
func setupMetadataKVForQuorum(t *testing.T) *kvstore.MetadataKV {
	t.Helper()
	hlc := clock.NewHLC()

	// 创建 mock MVStore
	store := newMockMVStoreForQuorum()

	// 创建 MetadataKV
	metadataKV, err := kvstore.NewMetadataKV(store, &kvstore.MetadataKVOptions{
		HLC: hlc,
	})
	require.NoError(t, err)

	return metadataKV
}

// TestNewTreeTopology 测试创建树形拓扑
func TestNewTreeTopology(t *testing.T) {
	topology := NewTreeTopology("root-001")

	require.NotNil(t, topology)
	require.Equal(t, "root-001", topology.root)

	// 验证根节点存在
	root, ok := topology.GetNode("root-001")
	require.True(t, ok)
	require.Equal(t, "root-001", root.NodeID)
	require.Equal(t, "", root.ParentID)
	require.Equal(t, 0, root.Level)
}

// TestTreeTopology_AddChild 测试添加子节点
func TestTreeTopology_AddChild(t *testing.T) {
	topology := NewTreeTopology("root-001")

	// 添加子节点
	err := topology.AddChild("root-001", "child-001")
	require.NoError(t, err)

	// 验证子节点存在
	child, ok := topology.GetNode("child-001")
	require.True(t, ok)
	require.Equal(t, "child-001", child.NodeID)
	require.Equal(t, "root-001", child.ParentID)
	require.Equal(t, 1, child.Level)

	// 验证根节点的子节点列表
	root, _ := topology.GetNode("root-001")
	require.Len(t, root.ChildrenIDs, 1)
	require.Contains(t, root.ChildrenIDs, "child-001")
}

// TestTreeTopology_AddChild_Error 测试添加子节点的错误情况
func TestTreeTopology_AddChild_Error(t *testing.T) {
	topology := NewTreeTopology("root-001")

	// 添加重复的子节点
	err := topology.AddChild("root-001", "child-001")
	require.NoError(t, err)

	err = topology.AddChild("root-001", "child-001")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")

	// 添加到不存在的父节点
	err = topology.AddChild("non-existent", "child-002")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

// TestTreeTopology_GetSiblingNodes 测试获取兄弟节点
func TestTreeTopology_GetSiblingNodes(t *testing.T) {
	topology := NewTreeTopology("root-001")

	// 添加多个子节点
	_ = topology.AddChild("root-001", "child-001")
	_ = topology.AddChild("root-001", "child-002")
	_ = topology.AddChild("root-001", "child-003")

	// 获取 child-002 的兄弟节点
	siblings := topology.GetSiblingNodes("child-002")

	require.Len(t, siblings, 2)
	require.Contains(t, siblings, "child-001")
	require.Contains(t, siblings, "child-003")
	require.NotContains(t, siblings, "child-002")
}

// TestTreeTopology_GetDescendantNodes 测试获取子孙节点
func TestTreeTopology_GetDescendantNodes(t *testing.T) {
	topology := NewTreeTopology("root-001")

	// 构建三层树结构
	_ = topology.AddChild("root-001", "child-001")
	_ = topology.AddChild("root-001", "child-002")
	_ = topology.AddChild("child-001", "grandchild-001")
	_ = topology.AddChild("child-001", "grandchild-002")
	_ = topology.AddChild("child-002", "grandchild-003")

	// 获取 root-001 的所有子孙节点
	descendants := topology.GetDescendantNodes("root-001")

	require.Len(t, descendants, 5)
	require.Contains(t, descendants, "child-001")
	require.Contains(t, descendants, "child-002")
	require.Contains(t, descendants, "grandchild-001")
	require.Contains(t, descendants, "grandchild-002")
	require.Contains(t, descendants, "grandchild-003")

	// 获取 child-001 的子孙节点
	descendants = topology.GetDescendantNodes("child-001")

	require.Len(t, descendants, 2)
	require.Contains(t, descendants, "grandchild-001")
	require.Contains(t, descendants, "grandchild-002")
}

// TestLayer_String 测试层级字符串表示
func TestLayer_String(t *testing.T) {
	tests := []struct {
		layer    Layer
		expected string
	}{
		{Layer1, "Layer1-Tree2PC"},
		{Layer2, "Layer2-Quorum"},
		{Layer3, "Layer3-Gossip"},
		{Layer(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.layer.String()
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestLayer_ConsistencyLevelForLayer 测试层级对应的一致性级别
func TestLayer_ConsistencyLevelForLayer(t *testing.T) {
	tests := []struct {
		layer    Layer
		expected ConsistencyLevel
	}{
		{Layer1, ConsistencyStrong2PC},
		{Layer2, ConsistencyEnhancedEventual},
		{Layer3, ConsistencyEventual},
	}

	for _, tt := range tests {
		t.Run(tt.layer.String(), func(t *testing.T) {
			result := tt.layer.ConsistencyLevelForLayer()
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestNewTreeTopologyCoordinator 测试创建树形拓扑协调器
func TestNewTreeTopologyCoordinator(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := &mockMetadataKVForTree{mockMetadataKV: newMockMetadataKV()}

	topology := NewTreeTopology("root-001")
	_ = topology.AddChild("root-001", "child-001")

	coordinator, err := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:    topology,
		LocalNodeID: "child-001",
		TwoPCOptions: &TwoPCOptions{
			MetadataKV: metadataKV,
			MerkleTree: merkleTree,
			HLC:        hlc,
		},
		HLC: hlc,
	})

	require.NoError(t, err)
	require.NotNil(t, coordinator)
	require.Equal(t, "child-001", coordinator.GetLocalNodeID())
}

// TestNewTreeTopologyCoordinator_Errors 测试创建协调器的错误情况
func TestNewTreeTopologyCoordinator_Errors(t *testing.T) {
	tests := []struct {
		name    string
		opts    *TreeTopologyOptions
		wantErr string
	}{
		{
			name:    "nil options",
			opts:    nil,
			wantErr: "options cannot be nil",
		},
		{
			name: "nil topology",
			opts: &TreeTopologyOptions{
				LocalNodeID: "node-001",
			},
			wantErr: "topology cannot be nil",
		},
		{
			name: "empty local node ID",
			opts: &TreeTopologyOptions{
				Topology:    NewTreeTopology("root-001"),
				LocalNodeID: "",
			},
			wantErr: "local node ID cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTreeTopologyCoordinator(tt.opts)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestTreeTopologyCoordinator_GetLayerForNamespace 测试根据 Namespace 获取层级
func TestTreeTopologyCoordinator_GetLayerForNamespace(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := &mockMetadataKVForTree{mockMetadataKV: newMockMetadataKV()}

	topology := NewTreeTopology("root-001")
	coordinator, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:    topology,
		LocalNodeID: "root-001",
		TwoPCOptions: &TwoPCOptions{
			MetadataKV: metadataKV,
			MerkleTree: merkleTree,
			HLC:        hlc,
		},
		HLC: hlc,
	})

	tests := []struct {
		namespace string
		expected  Layer
	}{
		{kvstore.NamespaceCluster, Layer1}, // 2PC 强一致
		{kvstore.NamespaceShard, Layer1},   // 2PC 强一致
		{kvstore.NamespaceStatic, Layer1},  // 2PC 强一致
		{kvstore.NamespaceVersion, Layer1}, // 2PC 强一致
		{kvstore.NamespaceRole, Layer2},    // Quorum 增强最终一致
		{kvstore.NamespaceTopo, Layer3},    // Gossip 最终一致 (P0-4 fix)
		{kvstore.NamespaceNode, Layer3},    // Gossip 最终一致
		{kvstore.NamespaceDynamic, Layer3}, // Gossip 最终一致
		{kvstore.NamespaceOp, Layer3},      // Gossip 最终一致
		{"unknown:namespace:", Layer3},     // 默认 Gossip
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			result := coordinator.GetLayerForNamespace(tt.namespace)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestTreeTopologyCoordinator_PutWithLayer_Layer1 测试 Layer1 写入（2PC）
func TestTreeTopologyCoordinator_PutWithLayer_Layer1(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := &mockMetadataKVForTree{mockMetadataKV: newMockMetadataKV()}

	topology := NewTreeTopology("root-001")
	_ = topology.AddChild("root-001", "child-001")

	coordinator, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:    topology,
		LocalNodeID: "child-001",
		TwoPCOptions: &TwoPCOptions{
			MetadataKV: metadataKV,
			MerkleTree: merkleTree,
			HLC:        hlc,
		},
		HLC: hlc,
	})

	ctx := context.Background()
	err := coordinator.PutWithLayer(ctx, kvstore.NamespaceCluster, "test-key", "test-value", Layer1)

	require.NoError(t, err)
}

// TestTreeTopologyCoordinator_PutWithLayer_Closed 测试关闭协调器后写入失败
func TestTreeTopologyCoordinator_PutWithLayer_Closed(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := &mockMetadataKVForTree{mockMetadataKV: newMockMetadataKV()}

	topology := NewTreeTopology("root-001")
	coordinator, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:    topology,
		LocalNodeID: "root-001",
		TwoPCOptions: &TwoPCOptions{
			MetadataKV: metadataKV,
			MerkleTree: merkleTree,
			HLC:        hlc,
		},
		HLC: hlc,
	})

	// 关闭协调器
	_ = coordinator.Close()

	ctx := context.Background()
	err := coordinator.PutWithLayer(ctx, kvstore.NamespaceCluster, "test-key", "test-value", Layer1)

	require.Error(t, err)
	require.Contains(t, err.Error(), "coordinator is closed")
}

// TestTreeTopologyCoordinator_Close 测试关闭协调器
func TestTreeTopologyCoordinator_Close(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := &mockMetadataKVForTree{mockMetadataKV: newMockMetadataKV()}

	topology := NewTreeTopology("root-001")
	coordinator, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:    topology,
		LocalNodeID: "root-001",
		TwoPCOptions: &TwoPCOptions{
			MetadataKV: metadataKV,
			MerkleTree: merkleTree,
			HLC:        hlc,
		},
		HLC: hlc,
	})

	err := coordinator.Close()
	require.NoError(t, err)

	// 再次关闭应该是安全的
	err = coordinator.Close()
	require.NoError(t, err)
}

// BenchmarkTreeTopologyCoordinator_PutWithLayer_Layer1 性能测试：Layer1 写入
func BenchmarkTreeTopologyCoordinator_PutWithLayer_Layer1(b *testing.B) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := &mockMetadataKVForTree{mockMetadataKV: newMockMetadataKV()}

	topology := NewTreeTopology("root-001")
	_ = topology.AddChild("root-001", "child-001")

	coordinator, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:    topology,
		LocalNodeID: "child-001",
		TwoPCOptions: &TwoPCOptions{
			MetadataKV: metadataKV,
			MerkleTree: merkleTree,
			HLC:        hlc,
		},
		HLC: hlc,
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = coordinator.PutWithLayer(ctx, kvstore.NamespaceCluster, "test-key", "test-value", Layer1)
	}
}

// ==================== Task 3.1: Quorum 协调器集成测试 ====================

// TestTreeTopologyCoordinator_PutWithLayer_Layer2_Quorum 测试 Layer2 写入（Quorum）
func TestTreeTopologyCoordinator_PutWithLayer_Layer2_Quorum(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := setupMetadataKVForQuorum(t)

	topology := NewTreeTopology("root-001")
	_ = topology.AddChild("root-001", "child-001")
	_ = topology.AddChild("root-001", "child-002")

	// 构建 Quorum 参与者（不同父节点组的代表节点）
	participants := []string{"child-001", "child-002"}

	coordinator, err := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:           topology,
		LocalNodeID:        "child-001",
		TwoPCOptions:       &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
		QuorumParticipants: participants,
		QuorumMetadataKV:   metadataKV,
		HLC:                hlc,
	})

	require.NoError(t, err)
	require.NotNil(t, coordinator)

	ctx := context.Background()
	err = coordinator.PutWithLayer(ctx, kvstore.NamespaceRole, "test-key", "test-value", Layer2)

	require.NoError(t, err, "Layer2 写入应该成功")
}

// TestTreeTopologyCoordinator_PutWithLayer_Layer2_DefaultNamespace 使用默认 Namespace 层级
func TestTreeTopologyCoordinator_PutWithLayer_Layer2_DefaultNamespace(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := setupMetadataKVForQuorum(t)

	topology := NewTreeTopology("root-001")
	_ = topology.AddChild("root-001", "child-001")

	participants := []string{"child-001"}

	coordinator, err := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:           topology,
		LocalNodeID:        "child-001",
		TwoPCOptions:       &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
		QuorumParticipants: participants,
		QuorumMetadataKV:   metadataKV,
		HLC:                hlc,
	})

	require.NoError(t, err)

	ctx := context.Background()
	// NamespaceRole 默认使用 Layer2（Quorum）
	err = coordinator.PutWithLayer(ctx, kvstore.NamespaceRole, "role-key", "role-value", Layer2)

	require.NoError(t, err)
}

// TestTreeTopologyCoordinator_QuorumIntegration_QuorumThreshold 测试 Quorum 阈值计算
func TestTreeTopologyCoordinator_QuorumIntegration_QuorumThreshold(t *testing.T) {
	tests := []struct {
		name           string
		participants   []string
		expectedQuorum int
	}{
		{"3节点", []string{"node-1", "node-2", "node-3"}, 2},
		{"5节点", []string{"node-1", "node-2", "node-3", "node-4", "node-5"}, 3},
		{"7节点", []string{"node-1", "node-2", "node-3", "node-4", "node-5", "node-6", "node-7"}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hlc := clock.NewHLC()
			merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
			metadataKV := setupMetadataKVForQuorum(t)
			topology := NewTreeTopology("root-001")

			coordinator, err := NewTreeTopologyCoordinator(&TreeTopologyOptions{
				Topology:           topology,
				LocalNodeID:        tt.participants[0],
				TwoPCOptions:       &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
				QuorumParticipants: tt.participants,
				QuorumMetadataKV:   metadataKV,
				HLC:                hlc,
			})

			require.NoError(t, err)
			require.NotNil(t, coordinator.quorumCoordinator)
			require.Equal(t, tt.expectedQuorum, coordinator.quorumCoordinator.GetQuorum())
		})
	}
}

// ==================== Task 3.2: Gossip 同步集成测试 ====================

// TestTreeTopologyCoordinator_PutWithLayer_Layer3_Gossip 测试 Layer3 写入（Gossip）
func TestTreeTopologyCoordinator_PutWithLayer_Layer3_Gossip(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := setupMetadataKVForQuorum(t)

	topology := NewTreeTopology("root-001")
	_ = topology.AddChild("root-001", "child-001")

	coordinator, err := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:         topology,
		LocalNodeID:      "child-001",
		TwoPCOptions:     &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
		GossipMerkleTree: merkleTree,
		GossipMetadataKV: metadataKV,
		GossipTransport:  nil,
		HLC:              hlc,
	})

	require.NoError(t, err)
	require.NotNil(t, coordinator)

	ctx := context.Background()
	err = coordinator.PutWithLayer(ctx, kvstore.NamespaceNode, "test-key", "test-value", Layer3)

	require.NoError(t, err, "Layer3 写入应该成功")
}

// TestTreeTopologyCoordinator_GossipIntegration_MerkleUpdate 测试 Gossip 集成的 Merkle 更新
func TestTreeTopologyCoordinator_GossipIntegration_MerkleUpdate(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := setupMetadataKVForQuorum(t)

	topology := NewTreeTopology("root-001")
	_ = topology.AddChild("root-001", "child-001")

	coordinator, err := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:         topology,
		LocalNodeID:      "child-001",
		TwoPCOptions:     &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
		GossipMerkleTree: merkleTree,
		GossipMetadataKV: metadataKV,
		GossipTransport:  nil,
		HLC:              hlc,
	})

	require.NoError(t, err)

	ctx := context.Background()
	// 写入数据
	err = coordinator.PutWithLayer(ctx, kvstore.NamespaceNode, "node-001", map[string]string{
		"address": "192.168.1.10:8080",
		"status":  "online",
	}, Layer3)

	require.NoError(t, err)

	// 验证 Merkle Tree 已更新
	newRoot := merkleTree.GetGlobalRootHash()
	require.NotEmpty(t, newRoot, "Merkle Root 应该已更新")
}

// TestTreeTopologyCoordinator_GossipIntegration_Close 测试 Gossip 资源清理
func TestTreeTopologyCoordinator_GossipIntegration_Close(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := setupMetadataKVForQuorum(t)

	topology := NewTreeTopology("root-001")
	coordinator, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:         topology,
		LocalNodeID:      "root-001",
		TwoPCOptions:     &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
		GossipMerkleTree: merkleTree,
		GossipMetadataKV: metadataKV,
		GossipTransport:  nil,
		HLC:              hlc,
	})

	require.NotNil(t, coordinator.gossipSync, "GossipSync 应该已初始化")

	// 关闭协调器
	err := coordinator.Close()
	require.NoError(t, err)
}

// ==================== Task 3.3: Merkle Root 交换机制测试 ====================

// TestTreeTopologyCoordinator_MerkleSync_ExchangeRoot 测试 Merkle Root 交换
func TestTreeTopologyCoordinator_MerkleSync_ExchangeRoot(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree1 := kvstore.NewNamespacedMerkleTree(hlc)
	merkleTree2 := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV1 := setupMetadataKVForQuorum(t)
	metadataKV2 := setupMetadataKVForQuorum(t)

	topology1 := NewTreeTopology("root-001")
	topology2 := NewTreeTopology("root-001")
	_ = topology1.AddChild("root-001", "node-001")
	_ = topology2.AddChild("root-001", "node-002")

	coordinator1, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:     topology1,
		LocalNodeID:  "node-001",
		TwoPCOptions: &TwoPCOptions{MetadataKV: metadataKV1, MerkleTree: merkleTree1, HLC: hlc},
		HLC:          hlc,
	})

	coordinator2, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:     topology2,
		LocalNodeID:  "node-002",
		TwoPCOptions: &TwoPCOptions{MetadataKV: metadataKV2, MerkleTree: merkleTree2, HLC: hlc},
		HLC:          hlc,
	})

	ctx := context.Background()

	// 获取初始 Merkle Root（两个空树应该相同）
	initialRoot1 := coordinator1.GetMerkleRoot()
	initialRoot2 := coordinator2.GetMerkleRoot()
	require.NotEmpty(t, initialRoot1, "节点 1 的初始 Merkle Root 不应为空")
	require.NotEmpty(t, initialRoot2, "节点 2 的初始 Merkle Root 不应为空")
	require.Equal(t, initialRoot1, initialRoot2, "两个空树的 Merkle Root 应该相同")

	// 节点 1 写入数据
	err := coordinator1.PutWithLayer(ctx, kvstore.NamespaceNode, "node-001", map[string]string{
		"address": "192.168.1.10:8080",
	}, Layer1)
	require.NoError(t, err, "PutWithLayer 应该成功")

	// 获取写入后的 Merkle Root
	root1 := coordinator1.GetMerkleRoot()
	require.NotEqual(t, initialRoot1, root1, "写入数据后 Merkle Root 应该变化")

	// 节点 2 的 Merkle Root（节点 2 没有数据，应该保持不变）
	root2 := coordinator2.GetMerkleRoot()
	require.Equal(t, initialRoot2, root2, "节点 2 没有数据，Merkle Root 应该保持不变")

	// 两个节点的 Merkle Root 应该不同
	require.NotEqual(t, root1, root2, "不同数据的节点应有不同的 Merkle Root")
}

// TestTreeTopologyCoordinator_MerkleSync_DetectDiff 测试差异检测
func TestTreeTopologyCoordinator_MerkleSync_DetectDiff(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree1 := kvstore.NewNamespacedMerkleTree(hlc)
	merkleTree2 := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV1 := setupMetadataKVForQuorum(t)
	metadataKV2 := setupMetadataKVForQuorum(t)

	topology1 := NewTreeTopology("root-001")
	topology2 := NewTreeTopology("root-001")
	_ = topology1.AddChild("root-001", "node-001")
	_ = topology2.AddChild("root-001", "node-002")

	coordinator1, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:     topology1,
		LocalNodeID:  "node-001",
		TwoPCOptions: &TwoPCOptions{MetadataKV: metadataKV1, MerkleTree: merkleTree1, HLC: hlc},
		HLC:          hlc,
	})

	coordinator2, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:     topology2,
		LocalNodeID:  "node-002",
		TwoPCOptions: &TwoPCOptions{MetadataKV: metadataKV2, MerkleTree: merkleTree2, HLC: hlc},
		HLC:          hlc,
	})

	ctx := context.Background()

	// 节点 1 写入数据到多个 Namespace
	_ = coordinator1.PutWithLayer(ctx, kvstore.NamespaceNode, "node-001", map[string]string{
		"address": "192.168.1.10:8080",
	}, Layer1)
	_ = coordinator1.PutWithLayer(ctx, kvstore.NamespaceShard, "shard-001", map[string]string{
		"status": "active",
	}, Layer1)

	// 构建同步请求
	request := &MerkleSyncRequest{
		NodeID:          "node-001",
		GlobalRootHash:  coordinator1.GetMerkleRoot(),
		NamespaceHashes: coordinator1.GetNamespaceRootHashes(),
		RequestID:       "req-001",
	}

	// 节点 2 检测差异
	response := coordinator2.DetectMerkleDiff(request)

	require.NotNil(t, response, "响应不应为空")
	require.Equal(t, "node-002", response.NodeID, "响应节点 ID 应正确")
	require.NotEmpty(t, response.DiffNamespaces, "应检测到差异的 Namespace")
	require.Contains(t, response.DiffNamespaces, kvstore.NamespaceNode, "应检测到 meta:node Namespace 差异")
}

// TestTreeTopologyCoordinator_MerkleSync_NoDiff 测试无差异场景
func TestTreeTopologyCoordinator_MerkleSync_NoDiff(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := setupMetadataKVForQuorum(t)

	topology1 := NewTreeTopology("root-001")
	topology2 := NewTreeTopology("root-001")

	coordinator1, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:     topology1,
		LocalNodeID:  "node-001",
		TwoPCOptions: &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
		HLC:          hlc,
	})

	coordinator2, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:     topology2,
		LocalNodeID:  "node-002",
		TwoPCOptions: &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
		HLC:          hlc,
	})

	// 两个节点使用相同的 Merkle Tree（共享实例，因此数据相同）
	request := &MerkleSyncRequest{
		NodeID:          "node-001",
		GlobalRootHash:  coordinator1.GetMerkleRoot(),
		NamespaceHashes: coordinator1.GetNamespaceRootHashes(),
		RequestID:       "req-002",
	}

	response := coordinator2.DetectMerkleDiff(request)

	require.NotNil(t, response, "响应不应为空")
	require.Empty(t, response.DiffNamespaces, "相同数据不应有差异")
}

// TestTreeTopologyCoordinator_SyncMerkleBeforeCommit 测试提交前同步
func TestTreeTopologyCoordinator_SyncMerkleBeforeCommit(t *testing.T) {
	hlc := clock.NewHLC()
	merkleTree := kvstore.NewNamespacedMerkleTree(hlc)
	metadataKV := setupMetadataKVForQuorum(t)

	topology := NewTreeTopology("root-001")
	_ = topology.AddChild("root-001", "child-001")

	coordinator, _ := NewTreeTopologyCoordinator(&TreeTopologyOptions{
		Topology:     topology,
		LocalNodeID:  "child-001",
		TwoPCOptions: &TwoPCOptions{MetadataKV: metadataKV, MerkleTree: merkleTree, HLC: hlc},
		HLC:          hlc,
	})

	ctx := context.Background()
	participants := []string{"root-001", "child-001", "child-002"}

	// 测试同步（简化实现，应该不返回错误）
	err := coordinator.SyncMerkleBeforeCommit(ctx, participants)
	require.NoError(t, err, "SyncMerkleBeforeCommit 应该成功")
}
