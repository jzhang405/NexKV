// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件测试拓扑感知增强模型
package porcupine

import (
	"testing"
	"time"

	"github.com/anishathalye/porcupine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 辅助函数 ====================

// createTestTopology 创建测试拓扑
//
//	拓扑结构：
//	Root (depth=0)
//	├── Middle-A (depth=1)
//	│   ├── Leaf-A1 (depth=2)
//	│   └── Leaf-A2 (depth=2)
//	└── Middle-B (depth=1)
//	    └── Leaf-B1 (depth=2)
func createTestTopology() *Topology {
	return &Topology{
		Nodes: map[string]*NodeInfo{
			"root": {
				NodeID:    "root",
				ParentID:  "",
				Children:  []string{"middle-a", "middle-b"},
				TreeDepth: 0,
			},
			"middle-a": {
				NodeID:    "middle-a",
				ParentID:  "root",
				Children:  []string{"leaf-a1", "leaf-a2"},
				TreeDepth: 1,
			},
			"middle-b": {
				NodeID:    "middle-b",
				ParentID:  "root",
				Children:  []string{"leaf-b1"},
				TreeDepth: 1,
			},
			"leaf-a1": {
				NodeID:    "leaf-a1",
				ParentID:  "middle-a",
				Children:  []string{},
				TreeDepth: 2,
			},
			"leaf-a2": {
				NodeID:    "leaf-a2",
				ParentID:  "middle-a",
				Children:  []string{},
				TreeDepth: 2,
			},
			"leaf-b1": {
				NodeID:    "leaf-b1",
				ParentID:  "middle-b",
				Children:  []string{},
				TreeDepth: 2,
			},
		},
		ParentOf: map[string][]string{
			"root":     {"middle-a", "middle-b"},
			"middle-a": {"leaf-a1", "leaf-a2"},
			"middle-b": {"leaf-b1"},
		},
		ChildOf: map[string]string{
			"middle-a": "root",
			"middle-b": "root",
			"leaf-a1":  "middle-a",
			"leaf-a2":  "middle-a",
			"leaf-b1":  "middle-b",
		},
	}
}

// createTestTopologyState 创建测试拓扑状态
func createTestTopologyState() *TopologyState {
	topology := createTestTopology()
	nodeStores := make(map[string]map[string]VersionedValue)
	for nodeID := range topology.Nodes {
		nodeStores[nodeID] = make(map[string]VersionedValue)
	}
	return &TopologyState{
		NodeStores:    nodeStores,
		Topology:      topology,
		CurrentLeader: "middle-a", // 父节点 ID 最小者
		CurrentTerm:   1,
	}
}

// ==================== NodeType 测试 ====================

func TestNodeType_String(t *testing.T) {
	tests := []struct {
		name     string
		NodeType NodeType
		expected string
	}{
		{"leaf", NodeTypeLeaf, "leaf"},
		{"middle", NodeTypeMiddle, "middle"},
		{"root", NodeTypeRoot, "root"},
		{"unknown", NodeTypeUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.NodeType.String())
		})
	}
}

// ==================== Topology 测试 ====================

func TestTopology_GetNodeType(t *testing.T) {
	topology := createTestTopology()

	tests := []struct {
		name     string
		nodeID   string
		expected NodeType
	}{
		{"root", "root", NodeTypeRoot},
		{"middle-a", "middle-a", NodeTypeMiddle},
		{"middle-b", "middle-b", NodeTypeMiddle},
		{"leaf-a1", "leaf-a1", NodeTypeLeaf},
		{"leaf-b1", "leaf-b1", NodeTypeLeaf},
		{"unknown", "unknown", NodeTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, topology.GetNodeType(tt.nodeID))
		})
	}
}

func TestTopology_GetNodeType_NilChecks(t *testing.T) {
	t.Run("nil topology", func(t *testing.T) {
		var topology *Topology
		assert.Equal(t, NodeTypeUnknown, topology.GetNodeType("any"))
	})

	t.Run("nil nodes map", func(t *testing.T) {
		topology := &Topology{Nodes: nil}
		assert.Equal(t, NodeTypeUnknown, topology.GetNodeType("any"))
	})
}

func TestTopology_GetMaxTreeDepth(t *testing.T) {
	topology := createTestTopology()
	assert.Equal(t, 2, topology.GetMaxTreeDepth())
}

func TestTopology_GetNodeDepth(t *testing.T) {
	topology := createTestTopology()

	tests := []struct {
		name     string
		nodeID   string
		expected int
	}{
		{"root", "root", 0},
		{"middle-a", "middle-a", 1},
		{"leaf-a1", "leaf-a1", 2},
		{"unknown", "unknown", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, topology.GetNodeDepth(tt.nodeID))
		})
	}
}

// ==================== VersionedValue 测试 ====================

func TestVersionedValue_Clone(t *testing.T) {
	original := VersionedValue{
		Value:   []byte("test-value"),
		Version: 1,
	}

	cloned := original.Clone()

	// 修改原始值不应影响克隆
	original.Value[0] = 'X'
	original.Version = 2

	assert.Equal(t, "test-value", string(cloned.Value))
	assert.Equal(t, uint64(1), cloned.Version)
}

// ==================== TopologyState 测试 ====================

func TestTopologyState_Clone(t *testing.T) {
	st := createTestTopologyState()
	st.NodeStores["leaf-a1"]["key1"] = VersionedValue{
		Value:   []byte("value1"),
		Version: 1,
	}

	cloned := st.Clone()

	// 修改克隆不应影响原始
	cloned.NodeStores["leaf-a1"]["key1"] = VersionedValue{
		Value:   []byte("modified"),
		Version: 2,
	}

	assert.Equal(t, "value1", string(st.NodeStores["leaf-a1"]["key1"].Value))
	assert.Equal(t, uint64(1), st.NodeStores["leaf-a1"]["key1"].Version)

	// 拓扑应该共享引用
	assert.Same(t, st.Topology, cloned.Topology)
}

// ==================== 延迟计算测试 ====================

func TestGetExpectedDelay(t *testing.T) {
	tests := []struct {
		name     string
		depth    int
		expected time.Duration
	}{
		{"depth 0", 0, 0},
		{"depth 1", 1, 100 * time.Millisecond},
		{"depth 2", 2, 200 * time.Millisecond},
		{"depth 3", 3, 300 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetExpectedDelay(tt.depth))
		})
	}
}

func TestGetNodeExpectedDelay(t *testing.T) {
	topology := createTestTopology()

	tests := []struct {
		name     string
		nodeID   string
		expected time.Duration
	}{
		// P1-02: Leaf 延迟 = 0（本地产生）
		{"leaf delay", "leaf-a1", 0},
		// Middle 延迟 = depth * 100ms
		{"middle delay", "middle-a", 100 * time.Millisecond},
		// Root 延迟 = maxDepth * 100ms（最高）
		{"root delay", "root", 200 * time.Millisecond},
		// 未知节点延迟 = 0
		{"unknown delay", "unknown", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetNodeExpectedDelay(topology, tt.nodeID))
		})
	}
}

// ==================== 处理函数测试 ====================

func TestHandleInitTopology(t *testing.T) {
	t.Run("successful initialization", func(t *testing.T) {
		st := &TopologyState{
			NodeStores: make(map[string]map[string]VersionedValue),
		}

		op := TopologyOperation{
			Type: TopologyOpInitTopology,
			Nodes: []*NodeInfo{
				{NodeID: "root", Children: []string{"child1"}, TreeDepth: 0},
				{NodeID: "child1", ParentID: "root", Children: []string{}, TreeDepth: 1},
			},
		}

		output := TopologyOutput{Ok: true}
		result := handleInitTopology(st, op, output)

		require.Len(t, result, 1)
		newSt := result[0].(*TopologyState)
		assert.NotNil(t, newSt.Topology)
		assert.Len(t, newSt.NodeStores, 2)
		assert.Equal(t, "root", newSt.CurrentLeader) // 父节点 ID 最小者
		assert.Equal(t, uint64(1), newSt.CurrentTerm)
	})

	t.Run("empty nodes", func(t *testing.T) {
		st := &TopologyState{}
		op := TopologyOperation{Type: TopologyOpInitTopology}
		output := TopologyOutput{Ok: true}

		result := handleInitTopology(st, op, output)
		assert.Len(t, result, 1)
	})
}

func TestHandleTreeAwareGossip(t *testing.T) {
	st := createTestTopologyState()

	t.Run("leaf node gossip - only to parent", func(t *testing.T) {
		op := TopologyOperation{
			Type:    TopologyOpWriteGossip,
			NodeID:  "leaf-a1",
			Key:     "key1",
			Value:   []byte("value1"),
			Version: 1,
		}
		output := TopologyOutput{Ok: true}

		result := handleTreeAwareGossip(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*TopologyState)
		// 本地写入
		assert.Equal(t, "value1", string(newSt.NodeStores["leaf-a1"]["key1"].Value))
		// 传播到父节点
		assert.Equal(t, "value1", string(newSt.NodeStores["middle-a"]["key1"].Value))
		// 不传播到兄弟节点
		_, exists := newSt.NodeStores["leaf-a2"]["key1"]
		assert.False(t, exists)
	})

	t.Run("middle node gossip - to parent and children", func(t *testing.T) {
		op := TopologyOperation{
			Type:    TopologyOpWriteGossip,
			NodeID:  "middle-a",
			Key:     "key2",
			Value:   []byte("value2"),
			Version: 1,
		}
		output := TopologyOutput{Ok: true}

		result := handleTreeAwareGossip(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*TopologyState)
		// 本地写入
		assert.Equal(t, "value2", string(newSt.NodeStores["middle-a"]["key2"].Value))
		// 传播到父节点
		assert.Equal(t, "value2", string(newSt.NodeStores["root"]["key2"].Value))
		// 广播到子节点
		assert.Equal(t, "value2", string(newSt.NodeStores["leaf-a1"]["key2"].Value))
		assert.Equal(t, "value2", string(newSt.NodeStores["leaf-a2"]["key2"].Value))
	})

	t.Run("root node gossip - only to children", func(t *testing.T) {
		op := TopologyOperation{
			Type:    TopologyOpWriteGossip,
			NodeID:  "root",
			Key:     "key3",
			Value:   []byte("value3"),
			Version: 1,
		}
		output := TopologyOutput{Ok: true}

		result := handleTreeAwareGossip(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*TopologyState)
		// 本地写入
		assert.Equal(t, "value3", string(newSt.NodeStores["root"]["key3"].Value))
		// 广播到所有子节点
		assert.Equal(t, "value3", string(newSt.NodeStores["middle-a"]["key3"].Value))
		assert.Equal(t, "value3", string(newSt.NodeStores["middle-b"]["key3"].Value))
	})

	t.Run("node not exists - returns nil", func(t *testing.T) {
		op := TopologyOperation{
			Type:    TopologyOpWriteGossip,
			NodeID:  "unknown",
			Key:     "key",
			Value:   []byte("value"),
			Version: 1,
		}
		output := TopologyOutput{Ok: true}

		result := handleTreeAwareGossip(st, op, output)
		assert.Nil(t, result)
	})
}

func TestHandle2PCWrite(t *testing.T) {
	st := createTestTopologyState()

	t.Run("successful 2PC write", func(t *testing.T) {
		op := TopologyOperation{
			Type:    TopologyOpWrite2PC,
			NodeID:  "leaf-a1",
			Key:     "key1",
			Value:   []byte("value1"),
			Version: 1,
		}
		output := TopologyOutput{Ok: true}

		result := handle2PCWrite(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*TopologyState)
		// 所有参与者都应该更新
		assert.Equal(t, "value1", string(newSt.NodeStores["leaf-a1"]["key1"].Value))
		assert.Equal(t, "value1", string(newSt.NodeStores["middle-a"]["key1"].Value))
		assert.Equal(t, "value1", string(newSt.NodeStores["leaf-a2"]["key1"].Value))
	})

	t.Run("version conflict - returns original state", func(t *testing.T) {
		// 先写入一个版本
		st.NodeStores["leaf-a1"]["key1"] = VersionedValue{
			Value:   []byte("old-value"),
			Version: 2,
		}

		op := TopologyOperation{
			Type:    TopologyOpWrite2PC,
			NodeID:  "leaf-a1",
			Key:     "key1",
			Value:   []byte("new-value"),
			Version: 1, // 版本号更低
		}
		output := TopologyOutput{Ok: true}

		result := handle2PCWrite(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*TopologyState)
		// 应该保持原值
		assert.Equal(t, "old-value", string(newSt.NodeStores["leaf-a1"]["key1"].Value))
	})
}

func TestHandleQuorumWrite(t *testing.T) {
	st := createTestTopologyState()

	t.Run("successful quorum write", func(t *testing.T) {
		op := TopologyOperation{
			Type:         TopologyOpWriteQuorum,
			NodeID:       "leaf-a1",
			Key:          "key1",
			Value:        []byte("value1"),
			Version:      1,
			Participants: []string{"leaf-a1", "leaf-a2", "leaf-b1"},
		}
		output := TopologyOutput{Ok: true}

		result := handleQuorumWrite(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*TopologyState)
		assert.Equal(t, "value1", string(newSt.NodeStores["leaf-a1"]["key1"].Value))
	})

	t.Run("quorum failed - version conflict on all nodes", func(t *testing.T) {
		// 先在所有节点写入一个高版本
		for _, nodeID := range []string{"leaf-a1", "leaf-a2", "leaf-b1"} {
			st.NodeStores[nodeID]["key1"] = VersionedValue{
				Value:   []byte("old-value"),
				Version: 10, // 高版本
			}
		}

		op := TopologyOperation{
			Type:         TopologyOpWriteQuorum,
			NodeID:       "leaf-a1",
			Key:          "key1",
			Value:        []byte("value1"),
			Version:      1, // 低版本，会导致所有写入失败
			Participants: []string{"leaf-a1", "leaf-a2", "leaf-b1"},
		}
		output := TopologyOutput{Ok: false, Error: "quorum_failed"}

		result := handleQuorumWrite(st, op, output)
		require.Len(t, result, 1)
		// 状态不变（版本冲突导致写入失败）
		assert.Equal(t, "old-value", string(result[0].(*TopologyState).NodeStores["leaf-a1"]["key1"].Value))
	})
}

func TestHandleTopologyGet(t *testing.T) {
	st := createTestTopologyState()
	st.NodeStores["leaf-a1"]["key1"] = VersionedValue{
		Value:   []byte("value1"),
		Version: 1,
	}

	t.Run("successful get", func(t *testing.T) {
		op := TopologyOperation{
			Type:   TopologyOpGet,
			NodeID: "leaf-a1",
			Key:    "key1",
		}
		output := TopologyOutput{
			Ok:      true,
			Value:   []byte("value1"),
			Version: 1,
		}

		result := handleTopologyGet(st, op, output)
		require.Len(t, result, 1)
	})

	t.Run("key not found", func(t *testing.T) {
		op := TopologyOperation{
			Type:   TopologyOpGet,
			NodeID: "leaf-a1",
			Key:    "nonexistent",
		}
		output := TopologyOutput{
			Ok:    false,
			Error: "key not found",
		}

		result := handleTopologyGet(st, op, output)
		require.Len(t, result, 1)
	})
}

// ==================== 模型集成测试 ====================

func TestTopologyAwareModel(t *testing.T) {
	model := TopologyAwareModel()

	t.Run("model creation", func(t *testing.T) {
		assert.NotNil(t, model)
	})

	t.Run("full scenario - init, gossip, get", func(t *testing.T) {
		// 直接使用处理函数进行测试，因为 Model 的 Init/Step 是内部方法
		st := &TopologyState{
			NodeStores:    make(map[string]map[string]VersionedValue),
			Topology:      nil,
			CurrentLeader: "",
			CurrentTerm:   0,
		}

		// 1. 初始化拓扑
		initOp := TopologyOperation{
			Type: TopologyOpInitTopology,
			Nodes: []*NodeInfo{
				{NodeID: "root", Children: []string{"child1"}, TreeDepth: 0},
				{NodeID: "child1", ParentID: "root", Children: []string{"leaf1"}, TreeDepth: 1},
				{NodeID: "leaf1", ParentID: "child1", TreeDepth: 2},
			},
		}
		initOutput := TopologyOutput{Ok: true}

		result := handleInitTopology(st, initOp, initOutput)
		require.Len(t, result, 1)
		st = result[0].(*TopologyState)

		// 2. Gossip 写入
		gossipOp := TopologyOperation{
			Type:    TopologyOpWriteGossip,
			NodeID:  "leaf1",
			Key:     "test-key",
			Value:   []byte("test-value"),
			Version: 1,
		}
		gossipOutput := TopologyOutput{Ok: true}

		result = handleTreeAwareGossip(st, gossipOp, gossipOutput)
		require.Len(t, result, 1)
		st = result[0].(*TopologyState)

		// 3. 验证写入
		assert.Equal(t, "test-value", string(st.NodeStores["leaf1"]["test-key"].Value))
		// 传播到父节点
		assert.Equal(t, "test-value", string(st.NodeStores["child1"]["test-key"].Value))
	})
}

func TestTopologyAwareModel_WithPorcupine(t *testing.T) {
	model := TopologyAwareModel()

	// 构建操作历史
	history := []porcupine.Operation{
		{
			ClientId: 1,
			Input: TopologyOperation{
				Type: TopologyOpInitTopology,
				Nodes: []*NodeInfo{
					{NodeID: "root", Children: []string{"child1"}, TreeDepth: 0},
					{NodeID: "child1", ParentID: "root", TreeDepth: 1},
				},
			},
			Output: TopologyOutput{Ok: true},
			Call:   0,
			Return: 1,
		},
		{
			ClientId: 1,
			Input: TopologyOperation{
				Type:    TopologyOpWriteGossip,
				NodeID:  "child1",
				Key:     "key1",
				Value:   []byte("value1"),
				Version: 1,
			},
			Output: TopologyOutput{Ok: true},
			Call:   2,
			Return: 3,
		},
		{
			ClientId: 1,
			Input: TopologyOperation{
				Type:   TopologyOpGet,
				NodeID: "root",
				Key:    "key1",
			},
			Output: TopologyOutput{Ok: true, Value: []byte("value1"), Version: 1},
			Call:   4,
			Return: 5,
		},
	}

	// 验证线性一致性
	result := porcupine.CheckOperations(model, history)
	assert.True(t, result, "Model should verify successfully")
}

// ==================== 状态比较测试 ====================

func TestTopologyStateEqual(t *testing.T) {
	t.Run("equal states", func(t *testing.T) {
		s1 := createTestTopologyState()
		s2 := createTestTopologyState()
		assert.True(t, topologyStateEqual(s1, s2))
	})

	t.Run("different node stores", func(t *testing.T) {
		s1 := createTestTopologyState()
		s2 := createTestTopologyState()
		s2.NodeStores["leaf-a1"]["key"] = VersionedValue{Value: []byte("value")}
		assert.False(t, topologyStateEqual(s1, s2))
	})

	t.Run("different leader", func(t *testing.T) {
		s1 := createTestTopologyState()
		s2 := createTestTopologyState()
		s2.CurrentLeader = "different"
		assert.False(t, topologyStateEqual(s1, s2))
	})

	t.Run("different term", func(t *testing.T) {
		s1 := createTestTopologyState()
		s2 := createTestTopologyState()
		s2.CurrentTerm = 99
		assert.False(t, topologyStateEqual(s1, s2))
	})

	t.Run("nil states", func(t *testing.T) {
		assert.False(t, topologyStateEqual(nil, &TopologyState{}))
		assert.False(t, topologyStateEqual(&TopologyState{}, nil))
		assert.False(t, topologyStateEqual(nil, nil))
	})
}

// ==================== 边界条件测试 ====================

func TestHandleTreeAwareGossip_EmptyTopology(t *testing.T) {
	st := &TopologyState{
		NodeStores: make(map[string]map[string]VersionedValue),
		Topology:   nil,
	}

	op := TopologyOperation{
		Type:    TopologyOpWriteGossip,
		NodeID:  "node1",
		Key:     "key",
		Value:   []byte("value"),
		Version: 1,
	}
	output := TopologyOutput{Ok: true}

	result := handleTreeAwareGossip(st, op, output)
	assert.Nil(t, result)
}

func TestHandle2PCWrite_EmptyParticipants(t *testing.T) {
	// 单节点场景：root 节点没有父节点
	topology := &Topology{
		Nodes: map[string]*NodeInfo{
			"root": {NodeID: "root", ParentID: "", Children: []string{}, TreeDepth: 0},
		},
		ParentOf: map[string][]string{},
		ChildOf:  map[string]string{},
	}

	st := &TopologyState{
		NodeStores:    map[string]map[string]VersionedValue{"root": {}},
		Topology:      topology,
		CurrentLeader: "root",
		CurrentTerm:   1,
	}

	op := TopologyOperation{
		Type:    TopologyOpWrite2PC,
		NodeID:  "root",
		Key:     "key1",
		Value:   []byte("value1"),
		Version: 1,
	}
	output := TopologyOutput{Ok: true}

	result := handle2PCWrite(st, op, output)
	require.Len(t, result, 1)

	newSt := result[0].(*TopologyState)
	assert.Equal(t, "value1", string(newSt.NodeStores["root"]["key1"].Value))
}
