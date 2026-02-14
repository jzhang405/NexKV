// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件测试失败恢复增强模型
package porcupine

import (
	"testing"

	"github.com/anishathalye/porcupine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 辅助函数 ====================

// createTestFRState 创建测试失败恢复状态
// 5 节点集群
func createTestFRState() *FailureRecoveryState {
	nodeStores := make(map[string]map[string]VersionedValue)
	for _, nodeID := range []string{"node1", "node2", "node3", "node4", "node5"} {
		nodeStores[nodeID] = make(map[string]VersionedValue)
	}
	return &FailureRecoveryState{
		NodeStores:     nodeStores,
		FailedNodes:    make(map[string]bool),
		RecoveredNodes: make(map[string]bool),
	}
}

// ==================== FailureRecoveryState 测试 ====================

func TestFailureRecoveryState_Clone(t *testing.T) {
	st := createTestFRState()
	st.NodeStores["node1"]["key1"] = VersionedValue{Value: []byte("value1"), Version: 1}
	st.FailedNodes["node2"] = true
	st.RecoveredNodes["node3"] = true

	cloned := st.Clone()

	// 修改克隆不应影响原始
	cloned.NodeStores["node1"]["key1"] = VersionedValue{Value: []byte("modified"), Version: 2}
	cloned.FailedNodes["node4"] = true

	assert.Equal(t, "value1", string(st.NodeStores["node1"]["key1"].Value))
	assert.True(t, st.FailedNodes["node2"])
	assert.False(t, st.FailedNodes["node4"])
}

// ==================== 处理函数测试 ====================

func TestHandleFRInit(t *testing.T) {
	t.Run("successful initialization", func(t *testing.T) {
		st := &FailureRecoveryState{
			NodeStores:     make(map[string]map[string]VersionedValue),
			FailedNodes:    make(map[string]bool),
			RecoveredNodes: make(map[string]bool),
		}

		op := FailureRecoveryOperation{
			Type:        FailureRecoveryOpInit,
			AllNodes:    []string{"node1", "node2", "node3"},
			FailedNodes: []string{"node2"},
		}

		output := FailureRecoveryOutput{Ok: true}
		result := handleFRInit(st, op, output)

		require.Len(t, result, 1)
		newSt := result[0].(*FailureRecoveryState)
		assert.Len(t, newSt.NodeStores, 3)
		assert.True(t, newSt.FailedNodes["node2"])
	})

	t.Run("empty nodes", func(t *testing.T) {
		st := &FailureRecoveryState{}
		op := FailureRecoveryOperation{Type: FailureRecoveryOpInit}
		output := FailureRecoveryOutput{Ok: true}

		result := handleFRInit(st, op, output)
		assert.Len(t, result, 1)
	})
}

func TestHandleNodeFail(t *testing.T) {
	st := createTestFRState()

	t.Run("mark node as failed", func(t *testing.T) {
		op := FailureRecoveryOperation{
			Type:   FailureRecoveryOpNodeFail,
			NodeID: "node1",
		}
		output := FailureRecoveryOutput{Ok: true}

		result := handleNodeFail(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*FailureRecoveryState)
		assert.True(t, newSt.FailedNodes["node1"])
		assert.False(t, newSt.RecoveredNodes["node1"])
	})

	t.Run("empty node ID", func(t *testing.T) {
		op := FailureRecoveryOperation{
			Type:   FailureRecoveryOpNodeFail,
			NodeID: "",
		}
		output := FailureRecoveryOutput{Ok: true}

		result := handleNodeFail(st, op, output)
		assert.Nil(t, result)
	})
}

func TestHandleNodeRecover(t *testing.T) {
	st := createTestFRState()
	st.FailedNodes["node1"] = true

	t.Run("recover failed node", func(t *testing.T) {
		op := FailureRecoveryOperation{
			Type:   FailureRecoveryOpNodeRecover,
			NodeID: "node1",
		}
		output := FailureRecoveryOutput{Ok: true}

		result := handleNodeRecover(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*FailureRecoveryState)
		assert.False(t, newSt.FailedNodes["node1"])
		assert.True(t, newSt.RecoveredNodes["node1"])
	})

	t.Run("recover non-failed node", func(t *testing.T) {
		op := FailureRecoveryOperation{
			Type:   FailureRecoveryOpNodeRecover,
			NodeID: "node2", // node2 未故障
		}
		output := FailureRecoveryOutput{Ok: true}

		result := handleNodeRecover(st, op, output)
		require.Len(t, result, 1)
		// 状态不变
		assert.Same(t, st, result[0])
	})
}

func TestHandleQuorumWithFailure(t *testing.T) {
	st := createTestFRState()

	t.Run("quorum with 1 node failed - succeeds", func(t *testing.T) {
		// 5 节点集群，1 节点故障，Quorum = 3，健康节点 = 4
		st.FailedNodes["node1"] = true

		op := FailureRecoveryOperation{
			Type:         FailureRecoveryOpQuorumWrite,
			NodeID:       "node2",
			Key:          "key1",
			Value:        []byte("value1"),
			Version:      1,
			Participants: []string{"node1", "node2", "node3", "node4", "node5"},
		}
		output := FailureRecoveryOutput{Ok: true}

		result := handleQuorumWithFailure(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*FailureRecoveryState)
		// 故障节点不应被写入
		_, exists := newSt.NodeStores["node1"]["key1"]
		assert.False(t, exists)
		// 健康节点应该被写入
		assert.Equal(t, "value1", string(newSt.NodeStores["node2"]["key1"].Value))
	})

	t.Run("quorum with 3 nodes failed - fails", func(t *testing.T) {
		// 5 节点集群，3 节点故障，Quorum = 3，健康节点 = 2
		st.FailedNodes["node1"] = true
		st.FailedNodes["node2"] = true
		st.FailedNodes["node3"] = true

		op := FailureRecoveryOperation{
			Type:         FailureRecoveryOpQuorumWrite,
			NodeID:       "node4",
			Key:          "key1",
			Value:        []byte("value1"),
			Version:      1,
			Participants: []string{"node1", "node2", "node3", "node4", "node5"},
		}
		output := FailureRecoveryOutput{Ok: false, Error: "quorum_failed"}

		result := handleQuorumWithFailure(st, op, output)
		require.Len(t, result, 1)
		// 状态不变
		assert.Same(t, st, result[0])
	})

	t.Run("version conflict - rollback", func(t *testing.T) {
		st := createTestFRState() // 重新创建状态，避免测试污染
		// 先写入一个高版本
		st.NodeStores["node2"]["key1"] = VersionedValue{
			Value:   []byte("old-value"),
			Version: 10,
		}

		op := FailureRecoveryOperation{
			Type:         FailureRecoveryOpQuorumWrite,
			NodeID:       "node2",
			Key:          "key1",
			Value:        []byte("new-value"),
			Version:      1, // 低版本
			Participants: []string{"node2", "node3", "node4"},
		}
		output := FailureRecoveryOutput{Ok: true}

		result := handleQuorumWithFailure(st, op, output)
		require.Len(t, result, 1)

		newSt := result[0].(*FailureRecoveryState)
		// 应该保持原值（版本冲突回滚）
		assert.Equal(t, "old-value", string(newSt.NodeStores["node2"]["key1"].Value))
		assert.Equal(t, uint64(10), newSt.NodeStores["node2"]["key1"].Version)
	})
}

func TestHandleFRGet(t *testing.T) {
	st := createTestFRState()
	st.NodeStores["node1"]["key1"] = VersionedValue{Value: []byte("value1"), Version: 1}

	t.Run("successful get from healthy node", func(t *testing.T) {
		op := FailureRecoveryOperation{
			Type:   FailureRecoveryOpGet,
			NodeID: "node1",
			Key:    "key1",
		}
		output := FailureRecoveryOutput{
			Ok:      true,
			Value:   []byte("value1"),
			Version: 1,
		}

		result := handleFRGet(st, op, output)
		require.Len(t, result, 1)
	})

	t.Run("get from failed node", func(t *testing.T) {
		st.FailedNodes["node1"] = true

		op := FailureRecoveryOperation{
			Type:   FailureRecoveryOpGet,
			NodeID: "node1",
			Key:    "key1",
		}
		output := FailureRecoveryOutput{
			Ok:    false,
			Error: "node_failed",
		}

		result := handleFRGet(st, op, output)
		require.Len(t, result, 1)
	})

	t.Run("key not found", func(t *testing.T) {
		op := FailureRecoveryOperation{
			Type:   FailureRecoveryOpGet,
			NodeID: "node2",
			Key:    "nonexistent",
		}
		output := FailureRecoveryOutput{
			Ok:    false,
			Error: "key not found",
		}

		result := handleFRGet(st, op, output)
		require.Len(t, result, 1)
	})
}

// ==================== 模型集成测试 ====================

func TestFailureRecoveryModel(t *testing.T) {
	model := FailureRecoveryModel()

	t.Run("model creation", func(t *testing.T) {
		assert.NotNil(t, model)
	})

	t.Run("full scenario - init, fail, quorum, recover", func(t *testing.T) {
		st := &FailureRecoveryState{
			NodeStores:     make(map[string]map[string]VersionedValue),
			FailedNodes:    make(map[string]bool),
			RecoveredNodes: make(map[string]bool),
		}

		// 1. 初始化
		initOp := FailureRecoveryOperation{
			Type:     FailureRecoveryOpInit,
			AllNodes: []string{"node1", "node2", "node3"},
		}
		initOutput := FailureRecoveryOutput{Ok: true}

		result := handleFRInit(st, initOp, initOutput)
		require.Len(t, result, 1)
		st = result[0].(*FailureRecoveryState)

		// 2. 节点故障
		failOp := FailureRecoveryOperation{
			Type:   FailureRecoveryOpNodeFail,
			NodeID: "node1",
		}
		failOutput := FailureRecoveryOutput{Ok: true}

		result = handleNodeFail(st, failOp, failOutput)
		require.Len(t, result, 1)
		st = result[0].(*FailureRecoveryState)
		assert.True(t, st.FailedNodes["node1"])

		// 3. Quorum 写入（排除故障节点）
		quorumOp := FailureRecoveryOperation{
			Type:         FailureRecoveryOpQuorumWrite,
			NodeID:       "node2",
			Key:          "key1",
			Value:        []byte("value1"),
			Version:      1,
			Participants: []string{"node1", "node2", "node3"},
		}
		quorumOutput := FailureRecoveryOutput{Ok: true}

		result = handleQuorumWithFailure(st, quorumOp, quorumOutput)
		require.Len(t, result, 1)
		st = result[0].(*FailureRecoveryState)

		// 故障节点未被写入
		_, exists := st.NodeStores["node1"]["key1"]
		assert.False(t, exists)
		// 健康节点被写入
		assert.Equal(t, "value1", string(st.NodeStores["node2"]["key1"].Value))

		// 4. 节点恢复
		recoverOp := FailureRecoveryOperation{
			Type:   FailureRecoveryOpNodeRecover,
			NodeID: "node1",
		}
		recoverOutput := FailureRecoveryOutput{Ok: true}

		result = handleNodeRecover(st, recoverOp, recoverOutput)
		require.Len(t, result, 1)
		st = result[0].(*FailureRecoveryState)
		assert.False(t, st.FailedNodes["node1"])
		assert.True(t, st.RecoveredNodes["node1"])
	})
}

func TestFailureRecoveryModel_WithPorcupine(t *testing.T) {
	model := FailureRecoveryModel()

	// 构建操作历史
	history := []porcupine.Operation{
		{
			ClientId: 1,
			Input: FailureRecoveryOperation{
				Type:     FailureRecoveryOpInit,
				AllNodes: []string{"node1", "node2", "node3"},
			},
			Output: FailureRecoveryOutput{Ok: true},
			Call:   0,
			Return: 1,
		},
		{
			ClientId: 1,
			Input: FailureRecoveryOperation{
				Type:   FailureRecoveryOpNodeFail,
				NodeID: "node1",
			},
			Output: FailureRecoveryOutput{Ok: true},
			Call:   2,
			Return: 3,
		},
		{
			ClientId: 1,
			Input: FailureRecoveryOperation{
				Type:         FailureRecoveryOpQuorumWrite,
				NodeID:       "node2",
				Key:          "key1",
				Value:        []byte("value1"),
				Version:      1,
				Participants: []string{"node1", "node2", "node3"},
			},
			Output: FailureRecoveryOutput{Ok: true},
			Call:   4,
			Return: 5,
		},
	}

	// 验证线性一致性
	result := porcupine.CheckOperations(model, history)
	assert.True(t, result, "Model should verify successfully")
}

// ==================== 状态比较测试 ====================

func TestFailureRecoveryStateEqual(t *testing.T) {
	t.Run("equal states", func(t *testing.T) {
		s1 := createTestFRState()
		s2 := createTestFRState()
		assert.True(t, failureRecoveryStateEqual(s1, s2))
	})

	t.Run("different node stores", func(t *testing.T) {
		s1 := createTestFRState()
		s2 := createTestFRState()
		s2.NodeStores["node1"]["key"] = VersionedValue{Value: []byte("value")}
		assert.False(t, failureRecoveryStateEqual(s1, s2))
	})

	t.Run("different failed nodes", func(t *testing.T) {
		s1 := createTestFRState()
		s2 := createTestFRState()
		s2.FailedNodes["node1"] = true
		assert.False(t, failureRecoveryStateEqual(s1, s2))
	})

	t.Run("different recovered nodes", func(t *testing.T) {
		s1 := createTestFRState()
		s2 := createTestFRState()
		s2.RecoveredNodes["node1"] = true
		assert.False(t, failureRecoveryStateEqual(s1, s2))
	})

	t.Run("nil states", func(t *testing.T) {
		assert.False(t, failureRecoveryStateEqual(nil, &FailureRecoveryState{}))
		assert.False(t, failureRecoveryStateEqual(&FailureRecoveryState{}, nil))
	})
}

// ==================== 边界条件测试 ====================

func TestHandleQuorumWithFailure_AllNodesFailed(t *testing.T) {
	// P2-04: 全节点故障场景
	st := createTestFRState()
	st.FailedNodes["node1"] = true
	st.FailedNodes["node2"] = true
	st.FailedNodes["node3"] = true
	st.FailedNodes["node4"] = true
	st.FailedNodes["node5"] = true

	op := FailureRecoveryOperation{
		Type:         FailureRecoveryOpQuorumWrite,
		NodeID:       "node1",
		Key:          "key1",
		Value:        []byte("value1"),
		Version:      1,
		Participants: []string{"node1", "node2", "node3", "node4", "node5"},
	}
	output := FailureRecoveryOutput{Ok: false, Error: "quorum_failed"}

	result := handleQuorumWithFailure(st, op, output)
	require.Len(t, result, 1)
	// 状态不变
	assert.Same(t, st, result[0])
}
