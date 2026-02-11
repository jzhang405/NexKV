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
	"testing"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/stretchr/testify/require"
)

// mockMetadataKVForTree 模拟 MetadataKV（用于树形拓扑测试）
type mockMetadataKVForTree struct {
	*mockMetadataKV
}

func (m *mockMetadataKVForTree) Put(ctx context.Context, ns, key string, value any) error {
	// 简化实现：直接返回成功
	return nil
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
		{kvstore.NamespaceCluster, Layer1},   // 2PC 强一致
		{kvstore.NamespaceShard, Layer1},     // 2PC 强一致
		{kvstore.NamespaceStatic, Layer1},    // 2PC 强一致
		{kvstore.NamespaceVersion, Layer1},   // 2PC 强一致
		{kvstore.NamespaceRole, Layer2},      // Quorum 增强最终一致
		{kvstore.NamespaceTopo, Layer2},      // Quorum 增强最终一致
		{kvstore.NamespaceNode, Layer3},      // Gossip 最终一致
		{kvstore.NamespaceDynamic, Layer3},   // Gossip 最终一致
		{kvstore.NamespaceOp, Layer3},        // Gossip 最终一致
		{"unknown:namespace:", Layer3},       // 默认 Gossip
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
