// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件测试 Leader HA 增强模型
package porcupine

import (
	"testing"

	"github.com/anishathalye/porcupine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 辅助函数 ====================

// createTestLeaderHAState 创建测试 Leader HA 状态
// 3 层树结构: root -> middle1, middle2 -> leaf1, leaf2
func createTestLeaderHAState() *LeaderHAState {
	topology := &Topology{
		Nodes: map[string]*NodeInfo{
			"root":    {NodeID: "root", Children: []string{"middle1", "middle2"}},
			"middle1": {NodeID: "middle1", ParentID: "root", Children: []string{"leaf1"}},
			"middle2": {NodeID: "middle2", ParentID: "root", Children: []string{"leaf2"}},
			"leaf1":   {NodeID: "leaf1", ParentID: "middle1"},
			"leaf2":   {NodeID: "leaf2", ParentID: "middle2"},
		},
		ParentOf: map[string][]string{
			"root":    {"middle1", "middle2"},
			"middle1": {"leaf1"},
			"middle2": {"leaf2"},
		},
		ChildOf: map[string]string{
			"middle1": "root",
			"middle2": "root",
			"leaf1":   "middle1",
			"leaf2":   "middle2",
		},
	}

	nodeStores := make(map[string]map[string]VersionedValue)
	for nodeID := range topology.Nodes {
		nodeStores[nodeID] = make(map[string]VersionedValue)
	}

	return &LeaderHAState{
		NodeStores:     nodeStores,
		Topology:       topology,
		ActiveLeader:   "middle1",                   // ID 最小的父节点
		StandbyLeaders: []string{"middle2", "root"}, // 按 ID 排序
		CurrentTerm:    1,
	}
}

// ==================== LeaderHAState 测试 ====================

func TestLeaderHAState_Clone(t *testing.T) {
	st := createTestLeaderHAState()
	st.NodeStores["middle1"]["key1"] = VersionedValue{Value: []byte("value1"), Version: 1}

	cloned := st.Clone()

	// 修改克隆不应影响原始
	cloned.NodeStores["middle1"]["key1"] = VersionedValue{Value: []byte("modified"), Version: 2}
	cloned.CurrentTerm = 10

	assert.Equal(t, "value1", string(st.NodeStores["middle1"]["key1"].Value))
	assert.Equal(t, uint64(1), st.CurrentTerm)
}

func TestNewLeaderHAState(t *testing.T) {
	t.Run("with topology", func(t *testing.T) {
		topology := &Topology{
			Nodes: map[string]*NodeInfo{
				"root":   {NodeID: "root", Children: []string{"child1"}},
				"child1": {NodeID: "child1", ParentID: "root"},
				"child2": {NodeID: "child2", ParentID: "root"},
			},
		}

		state := NewLeaderHAState(topology)

		// 只有一个父节点(root)，所以 ActiveLeader = root，无 Standby
		assert.Equal(t, "root", state.ActiveLeader)
		assert.Empty(t, state.StandbyLeaders)
		assert.Equal(t, uint64(1), state.CurrentTerm)
	})

	t.Run("nil topology", func(t *testing.T) {
		state := NewLeaderHAState(nil)

		assert.Empty(t, state.NodeStores)
		assert.Nil(t, state.Topology)
		assert.Equal(t, uint64(1), state.CurrentTerm)
	})

	t.Run("multiple parents sorted by ID", func(t *testing.T) {
		topology := &Topology{
			Nodes: map[string]*NodeInfo{
				"parent_z": {NodeID: "parent_z", Children: []string{"child1"}},
				"parent_a": {NodeID: "parent_a", Children: []string{"child2"}},
				"parent_m": {NodeID: "parent_m", Children: []string{"child3"}},
				"child1":   {NodeID: "child1", ParentID: "parent_z"},
				"child2":   {NodeID: "child2", ParentID: "parent_a"},
				"child3":   {NodeID: "child3", ParentID: "parent_m"},
			},
		}

		state := NewLeaderHAState(topology)

		// 按 ID 排序: parent_a < parent_m < parent_z
		assert.Equal(t, "parent_a", state.ActiveLeader)
		assert.Equal(t, []string{"parent_m", "parent_z"}, state.StandbyLeaders)
	})
}

func TestLeaderHAState_GetActiveLeader(t *testing.T) {
	t.Run("existing active leader", func(t *testing.T) {
		st := createTestLeaderHAState()
		assert.Equal(t, "middle1", st.GetActiveLeader())
	})

	t.Run("compute from topology", func(t *testing.T) {
		st := &LeaderHAState{
			NodeStores: make(map[string]map[string]VersionedValue),
			Topology: &Topology{
				Nodes: map[string]*NodeInfo{
					"parent_b": {NodeID: "parent_b", Children: []string{"child1"}},
					"parent_a": {NodeID: "parent_a", Children: []string{"child2"}},
				},
			},
		}

		// 应该自动计算
		leader := st.GetActiveLeader()
		assert.Equal(t, "parent_a", leader)
	})
}

func TestLeaderHAState_HandleLeaderFailover(t *testing.T) {
	t.Run("successful failover", func(t *testing.T) {
		st := createTestLeaderHAState()
		originalTerm := st.CurrentTerm

		newLeader := st.HandleLeaderFailover("middle1")

		// 新 Leader 应该是 Standby 列表中的第一个（排除故障的）
		// StandbyLeaders = ["middle2", "root"]
		assert.Equal(t, "middle2", newLeader)
		assert.Equal(t, "middle2", st.ActiveLeader)
		assert.Equal(t, originalTerm+1, st.CurrentTerm)
	})

	t.Run("no available standby", func(t *testing.T) {
		st := &LeaderHAState{
			ActiveLeader:   "leader1",
			StandbyLeaders: []string{},
			CurrentTerm:    1,
		}

		newLeader := st.HandleLeaderFailover("leader1")
		assert.Empty(t, newLeader)
	})
}

// ==================== 处理函数测试 ====================

func TestHandleLeaderHAInit(t *testing.T) {
	t.Run("successful initialization", func(t *testing.T) {
		st := &LeaderHAState{
			NodeStores: make(map[string]map[string]VersionedValue),
		}

		op := LeaderHAOperation{
			Type: LeaderHAOpInit,
			Nodes: []*NodeInfo{
				{NodeID: "parent", Children: []string{"child"}},
				{NodeID: "child", ParentID: "parent"},
			},
		}
		output := LeaderHAOutput{Ok: true, Term: 1}

		result := handleLeaderHAInit(st, op, output)

		require.Len(t, result, 1)
		newSt := result[0].(*LeaderHAState)
		assert.Equal(t, "parent", newSt.ActiveLeader)
		assert.Equal(t, uint64(1), newSt.CurrentTerm)
	})

	t.Run("term mismatch", func(t *testing.T) {
		st := &LeaderHAState{
			NodeStores: make(map[string]map[string]VersionedValue),
		}

		op := LeaderHAOperation{
			Type: LeaderHAOpInit,
			Nodes: []*NodeInfo{
				{NodeID: "parent", Children: []string{"child"}},
			},
		}
		output := LeaderHAOutput{Ok: true, Term: 99} // 不匹配

		result := handleLeaderHAInit(st, op, output)
		assert.Nil(t, result)
	})

	t.Run("failed output", func(t *testing.T) {
		st := &LeaderHAState{}
		op := LeaderHAOperation{Type: LeaderHAOpInit}
		output := LeaderHAOutput{Ok: false}

		result := handleLeaderHAInit(st, op, output)
		assert.Nil(t, result)
	})
}

func TestHandleLeaderChange(t *testing.T) {
	t.Run("successful leader change", func(t *testing.T) {
		st := createTestLeaderHAState()
		originalTerm := st.CurrentTerm

		op := LeaderHAOperation{
			Type:      LeaderHAOpLeaderChange,
			Term:      st.CurrentTerm,
			NewLeader: "middle2",
		}
		output := LeaderHAOutput{
			Ok:         true,
			NewLeader:  "middle2",
			ActiveTerm: originalTerm + 1,
		}

		result := handleLeaderChange(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*LeaderHAState)
		assert.Equal(t, "middle2", newSt.ActiveLeader)
		assert.Equal(t, originalTerm+1, newSt.CurrentTerm)
	})

	t.Run("stale term rejected", func(t *testing.T) {
		st := createTestLeaderHAState()
		st.CurrentTerm = 10

		op := LeaderHAOperation{
			Type: LeaderHAOpLeaderChange,
			Term: 5, // 旧 Term
		}
		output := LeaderHAOutput{
			Ok:    false,
			Error: "stale_term",
		}

		result := handleLeaderChange(st, op, output)
		require.Len(t, result, 1)
		// 状态不变
		assert.Same(t, st, result[0])
	})

	t.Run("no standby available", func(t *testing.T) {
		st := &LeaderHAState{
			NodeStores:     make(map[string]map[string]VersionedValue),
			ActiveLeader:   "leader1",
			StandbyLeaders: []string{},
			CurrentTerm:    1,
		}

		op := LeaderHAOperation{
			Type: LeaderHAOpLeaderChange,
			Term: 1,
		}
		output := LeaderHAOutput{
			Ok:    false,
			Error: "no_standby",
		}

		result := handleLeaderChange(st, op, output)
		require.Len(t, result, 1)
		assert.Same(t, st, result[0])
	})

	t.Run("output mismatch - new leader", func(t *testing.T) {
		st := createTestLeaderHAState()

		op := LeaderHAOperation{
			Type:      LeaderHAOpLeaderChange,
			Term:      st.CurrentTerm,
			NewLeader: "middle2",
		}
		output := LeaderHAOutput{
			Ok:         true,
			NewLeader:  "wrong_leader", // 不匹配
			ActiveTerm: st.CurrentTerm + 1,
		}

		result := handleLeaderChange(st, op, output)
		assert.Nil(t, result)
	})
}

func TestHandleLeaderHAWrite(t *testing.T) {
	t.Run("successful write by active leader", func(t *testing.T) {
		st := createTestLeaderHAState()

		op := LeaderHAOperation{
			Type:    LeaderHAOpWrite,
			NodeID:  "middle1", // Active Leader
			Key:     "key1",
			Value:   []byte("value1"),
			Version: 1,
			Term:    st.CurrentTerm,
		}
		output := LeaderHAOutput{Ok: true}

		result := handleLeaderHAWrite(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*LeaderHAState)
		assert.Equal(t, "value1", string(newSt.NodeStores["middle1"]["key1"].Value))
	})

	t.Run("stale term rejected", func(t *testing.T) {
		st := createTestLeaderHAState()
		st.CurrentTerm = 10

		op := LeaderHAOperation{
			Type:    LeaderHAOpWrite,
			NodeID:  "middle1",
			Key:     "key1",
			Value:   []byte("value1"),
			Version: 1,
			Term:    5, // 旧 Term
		}
		output := LeaderHAOutput{
			Ok:    false,
			Error: "stale_term",
		}

		result := handleLeaderHAWrite(st, op, output)
		require.Len(t, result, 1)
		assert.Same(t, st, result[0])
	})

	t.Run("non-leader write rejected", func(t *testing.T) {
		st := createTestLeaderHAState()

		op := LeaderHAOperation{
			Type:    LeaderHAOpWrite,
			NodeID:  "middle2", // Standby，不是 Active Leader
			Key:     "key1",
			Value:   []byte("value1"),
			Version: 1,
			Term:    st.CurrentTerm,
		}
		output := LeaderHAOutput{
			Ok:    false,
			Error: "not_leader",
		}

		result := handleLeaderHAWrite(st, op, output)
		require.Len(t, result, 1)
		assert.Same(t, st, result[0])
	})

	t.Run("version conflict", func(t *testing.T) {
		st := createTestLeaderHAState()
		st.NodeStores["middle1"]["key1"] = VersionedValue{
			Value:   []byte("old-value"),
			Version: 10,
		}

		op := LeaderHAOperation{
			Type:    LeaderHAOpWrite,
			NodeID:  "middle1",
			Key:     "key1",
			Value:   []byte("new-value"),
			Version: 5, // 低于现有版本
			Term:    st.CurrentTerm,
		}
		output := LeaderHAOutput{
			Ok:    false,
			Error: "version_conflict",
		}

		result := handleLeaderHAWrite(st, op, output)
		require.Len(t, result, 1)
		// 状态不变
		assert.Same(t, st, result[0])
	})

	t.Run("successful write with higher version", func(t *testing.T) {
		st := createTestLeaderHAState()
		st.NodeStores["middle1"]["key1"] = VersionedValue{
			Value:   []byte("old-value"),
			Version: 5,
		}

		op := LeaderHAOperation{
			Type:    LeaderHAOpWrite,
			NodeID:  "middle1",
			Key:     "key1",
			Value:   []byte("new-value"),
			Version: 10, // 高于现有版本
			Term:    st.CurrentTerm,
		}
		output := LeaderHAOutput{Ok: true}

		result := handleLeaderHAWrite(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*LeaderHAState)
		assert.Equal(t, "new-value", string(newSt.NodeStores["middle1"]["key1"].Value))
		assert.Equal(t, uint64(10), newSt.NodeStores["middle1"]["key1"].Version)
	})
}

func TestHandleLeaderHAGet(t *testing.T) {
	t.Run("successful get", func(t *testing.T) {
		st := createTestLeaderHAState()
		st.NodeStores["middle1"]["key1"] = VersionedValue{Value: []byte("value1"), Version: 1}

		op := LeaderHAOperation{
			Type:   LeaderHAOpGet,
			NodeID: "middle1",
			Key:    "key1",
		}
		output := LeaderHAOutput{
			Ok:      true,
			Value:   []byte("value1"),
			Version: 1,
		}

		result := handleLeaderHAGet(st, op, output)
		require.Len(t, result, 1)
	})

	t.Run("node not found", func(t *testing.T) {
		st := createTestLeaderHAState()

		op := LeaderHAOperation{
			Type:   LeaderHAOpGet,
			NodeID: "nonexistent",
			Key:    "key1",
		}
		output := LeaderHAOutput{
			Ok:    false,
			Error: "node not found",
		}

		result := handleLeaderHAGet(st, op, output)
		require.Len(t, result, 1)
	})

	t.Run("key not found", func(t *testing.T) {
		st := createTestLeaderHAState()

		op := LeaderHAOperation{
			Type:   LeaderHAOpGet,
			NodeID: "middle1",
			Key:    "nonexistent",
		}
		output := LeaderHAOutput{
			Ok:    false,
			Error: "key not found",
		}

		result := handleLeaderHAGet(st, op, output)
		require.Len(t, result, 1)
	})

	t.Run("value mismatch", func(t *testing.T) {
		st := createTestLeaderHAState()
		st.NodeStores["middle1"]["key1"] = VersionedValue{Value: []byte("value1"), Version: 1}

		op := LeaderHAOperation{
			Type:   LeaderHAOpGet,
			NodeID: "middle1",
			Key:    "key1",
		}
		output := LeaderHAOutput{
			Ok:      true,
			Value:   []byte("wrong-value"), // 不匹配
			Version: 1,
		}

		result := handleLeaderHAGet(st, op, output)
		assert.Nil(t, result)
	})
}

// ==================== 模型集成测试 ====================

func TestLeaderHAModel(t *testing.T) {
	model := LeaderHAModel()

	t.Run("model creation", func(t *testing.T) {
		assert.NotNil(t, model)
	})

	t.Run("full scenario - init, write, leader change, fencing", func(t *testing.T) {
		st := &LeaderHAState{
			NodeStores: make(map[string]map[string]VersionedValue),
		}

		// 1. 初始化
		initOp := LeaderHAOperation{
			Type: LeaderHAOpInit,
			Nodes: []*NodeInfo{
				{NodeID: "parent_a", Children: []string{"child1"}},
				{NodeID: "parent_b", Children: []string{"child2"}},
				{NodeID: "child1", ParentID: "parent_a"},
				{NodeID: "child2", ParentID: "parent_b"},
			},
		}
		initOutput := LeaderHAOutput{Ok: true, Term: 1}

		result := handleLeaderHAInit(st, initOp, initOutput)
		require.Len(t, result, 1)
		st = result[0].(*LeaderHAState)
		assert.Equal(t, "parent_a", st.ActiveLeader)

		// 2. Active Leader 写入
		writeOp := LeaderHAOperation{
			Type:    LeaderHAOpWrite,
			NodeID:  "parent_a",
			Key:     "key1",
			Value:   []byte("value1"),
			Version: 1,
			Term:    st.CurrentTerm,
		}
		writeOutput := LeaderHAOutput{Ok: true}

		result = handleLeaderHAWrite(st, writeOp, writeOutput)
		require.Len(t, result, 1)
		st = result[0].(*LeaderHAState)
		assert.Equal(t, "value1", string(st.NodeStores["parent_a"]["key1"].Value))

		// 3. Leader 切换
		changeOp := LeaderHAOperation{
			Type:      LeaderHAOpLeaderChange,
			Term:      st.CurrentTerm,
			NewLeader: "parent_b",
		}
		changeOutput := LeaderHAOutput{
			Ok:         true,
			NewLeader:  "parent_b",
			ActiveTerm: 2,
		}

		result = handleLeaderChange(st, changeOp, changeOutput)
		require.Len(t, result, 1)
		st = result[0].(*LeaderHAState)
		assert.Equal(t, "parent_b", st.ActiveLeader)
		assert.Equal(t, uint64(2), st.CurrentTerm)

		// 4. 旧 Leader 尝试写入（Fencing Token）
		oldWriteOp := LeaderHAOperation{
			Type:    LeaderHAOpWrite,
			NodeID:  "parent_a",
			Key:     "key2",
			Value:   []byte("stale-write"),
			Version: 1,
			Term:    1, // 旧 Term
		}
		oldWriteOutput := LeaderHAOutput{
			Ok:    false,
			Error: "stale_term",
		}

		result = handleLeaderHAWrite(st, oldWriteOp, oldWriteOutput)
		require.Len(t, result, 1)
		// 状态不变，旧写入被拒绝
		assert.Same(t, st, result[0])
	})
}

func TestLeaderHAModel_WithPorcupine(t *testing.T) {
	model := LeaderHAModel()

	// 构建操作历史
	history := []porcupine.Operation{
		{
			ClientId: 1,
			Input: LeaderHAOperation{
				Type: LeaderHAOpInit,
				Nodes: []*NodeInfo{
					{NodeID: "parent", Children: []string{"child"}},
					{NodeID: "child", ParentID: "parent"},
				},
			},
			Output: LeaderHAOutput{Ok: true, Term: 1},
			Call:   0,
			Return: 1,
		},
		{
			ClientId: 1,
			Input: LeaderHAOperation{
				Type:    LeaderHAOpWrite,
				NodeID:  "parent",
				Key:     "key1",
				Value:   []byte("value1"),
				Version: 1,
				Term:    1,
			},
			Output: LeaderHAOutput{Ok: true},
			Call:   2,
			Return: 3,
		},
		{
			ClientId: 1,
			Input: LeaderHAOperation{
				Type:   LeaderHAOpGet,
				NodeID: "parent",
				Key:    "key1",
			},
			Output: LeaderHAOutput{Ok: true, Value: []byte("value1"), Version: 1},
			Call:   4,
			Return: 5,
		},
	}

	// 验证线性一致性
	result := porcupine.CheckOperations(model, history)
	assert.True(t, result, "Model should verify successfully")
}

// ==================== 状态比较测试 ====================

func TestLeaderHAStateEqual(t *testing.T) {
	t.Run("equal states", func(t *testing.T) {
		s1 := createTestLeaderHAState()
		s2 := createTestLeaderHAState()
		assert.True(t, leaderHAStateEqual(s1, s2))
	})

	t.Run("different node stores", func(t *testing.T) {
		s1 := createTestLeaderHAState()
		s2 := createTestLeaderHAState()
		s2.NodeStores["middle1"]["key"] = VersionedValue{Value: []byte("value")}
		assert.False(t, leaderHAStateEqual(s1, s2))
	})

	t.Run("different active leader", func(t *testing.T) {
		s1 := createTestLeaderHAState()
		s2 := createTestLeaderHAState()
		s2.ActiveLeader = "different"
		assert.False(t, leaderHAStateEqual(s1, s2))
	})

	t.Run("different term", func(t *testing.T) {
		s1 := createTestLeaderHAState()
		s2 := createTestLeaderHAState()
		s2.CurrentTerm = 99
		assert.False(t, leaderHAStateEqual(s1, s2))
	})

	t.Run("different standby leaders", func(t *testing.T) {
		s1 := createTestLeaderHAState()
		s2 := createTestLeaderHAState()
		s2.StandbyLeaders = []string{"different"}
		assert.False(t, leaderHAStateEqual(s1, s2))
	})

	t.Run("nil states", func(t *testing.T) {
		assert.False(t, leaderHAStateEqual(nil, &LeaderHAState{}))
		assert.False(t, leaderHAStateEqual(&LeaderHAState{}, nil))
	})
}

// ==================== 边界条件测试 ====================

func TestLeaderHAState_EmptyStandbyList(t *testing.T) {
	// P1-05: 只有 1 个父节点的场景
	topology := &Topology{
		Nodes: map[string]*NodeInfo{
			"only_parent": {NodeID: "only_parent", Children: []string{"child"}},
			"child":       {NodeID: "child", ParentID: "only_parent"},
		},
	}

	state := NewLeaderHAState(topology)
	assert.Equal(t, "only_parent", state.ActiveLeader)
	assert.Empty(t, state.StandbyLeaders)

	// 尝试故障转移应失败
	newLeader := state.HandleLeaderFailover("only_parent")
	assert.Empty(t, newLeader)
}

func TestLeaderHAModel_LeaderFencingScenario(t *testing.T) {
	// P1-05: 完整的 Fencing Token 场景
	model := LeaderHAModel()

	// 场景：Leader 切换后，旧 Leader 的写入被拒绝
	history := []porcupine.Operation{
		// 初始化
		{
			ClientId: 1,
			Input: LeaderHAOperation{
				Type: LeaderHAOpInit,
				Nodes: []*NodeInfo{
					{NodeID: "leader1", Children: []string{"child"}},
					{NodeID: "leader2", Children: []string{"child2"}},
					{NodeID: "child", ParentID: "leader1"},
				},
			},
			Output: LeaderHAOutput{Ok: true, Term: 1},
			Call:   0,
			Return: 1,
		},
		// Leader1 写入成功（Term=1）
		{
			ClientId: 1,
			Input: LeaderHAOperation{
				Type:    LeaderHAOpWrite,
				NodeID:  "leader1",
				Key:     "key1",
				Value:   []byte("value1"),
				Version: 1,
				Term:    1,
			},
			Output: LeaderHAOutput{Ok: true},
			Call:   2,
			Return: 3,
		},
		// Leader 切换（Term=2）
		{
			ClientId: 2,
			Input: LeaderHAOperation{
				Type:      LeaderHAOpLeaderChange,
				Term:      1,
				NewLeader: "leader2",
			},
			Output: LeaderHAOutput{Ok: true, NewLeader: "leader2", ActiveTerm: 2},
			Call:   4,
			Return: 5,
		},
		// 旧 Leader 尝试写入（Term=1）被拒绝
		{
			ClientId: 1,
			Input: LeaderHAOperation{
				Type:    LeaderHAOpWrite,
				NodeID:  "leader1",
				Key:     "key2",
				Value:   []byte("stale"),
				Version: 1,
				Term:    1, // 旧 Term
			},
			Output: LeaderHAOutput{Ok: false, Error: "stale_term"},
			Call:   6,
			Return: 7,
		},
		// 新 Leader 写入成功（Term=2）
		{
			ClientId: 2,
			Input: LeaderHAOperation{
				Type:    LeaderHAOpWrite,
				NodeID:  "leader2",
				Key:     "key3",
				Value:   []byte("value3"),
				Version: 1,
				Term:    2,
			},
			Output: LeaderHAOutput{Ok: true},
			Call:   8,
			Return: 9,
		},
	}

	result := porcupine.CheckOperations(model, history)
	assert.True(t, result, "Fencing scenario should verify successfully")
}
