// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件测试增强模型集成
package porcupine

import (
	"testing"

	"github.com/anishathalye/porcupine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 模型注册表测试 ====================

func TestEnhancedModelRegistry(t *testing.T) {
	registry := NewEnhancedModelRegistry()

	t.Run("get topology model", func(t *testing.T) {
		model, err := registry.GetModel(ModelTypeTopology)
		assert.NoError(t, err)
		assert.NotNil(t, model)
	})

	t.Run("get failure recovery model", func(t *testing.T) {
		model, err := registry.GetModel(ModelTypeFailureRecovery)
		assert.NoError(t, err)
		assert.NotNil(t, model)
	})

	t.Run("get leader ha model", func(t *testing.T) {
		model, err := registry.GetModel(ModelTypeLeaderHA)
		assert.NoError(t, err)
		assert.NotNil(t, model)
	})

	t.Run("get unknown model", func(t *testing.T) {
		_, err := registry.GetModel(EnhancedModelType("unknown"))
		assert.Error(t, err)
	})

	t.Run("list models", func(t *testing.T) {
		types := registry.ListModels()
		assert.Len(t, types, 3)
	})
}

// ==================== 便捷验证函数测试 ====================

func TestVerifyOperationsWithModel(t *testing.T) {
	t.Run("topology model verification", func(t *testing.T) {
		ops := []porcupine.Operation{
			{
				ClientId: 1,
				Input: TopologyOperation{
					Type: TopologyOpInitTopology,
					Nodes: []*NodeInfo{
						{NodeID: "root", Children: []string{"child"}},
					},
				},
				Output: TopologyOutput{Ok: true},
				Call:   0,
				Return: 1,
			},
		}

		result := VerifyOperationsWithModel(ModelTypeTopology, ops)
		assert.True(t, result.Passed)
		assert.Contains(t, result.Message, "passed")
	})

	t.Run("failure recovery model verification", func(t *testing.T) {
		ops := []porcupine.Operation{
			{
				ClientId: 1,
				Input: FailureRecoveryOperation{
					Type:     FailureRecoveryOpInit,
					AllNodes: []string{"node1", "node2"},
				},
				Output: FailureRecoveryOutput{Ok: true},
				Call:   0,
				Return: 1,
			},
		}

		result := VerifyOperationsWithModel(ModelTypeFailureRecovery, ops)
		assert.True(t, result.Passed)
	})

	t.Run("leader ha model verification", func(t *testing.T) {
		ops := []porcupine.Operation{
			{
				ClientId: 1,
				Input: LeaderHAOperation{
					Type: LeaderHAOpInit,
					Nodes: []*NodeInfo{
						{NodeID: "leader", Children: []string{"child"}},
					},
				},
				Output: LeaderHAOutput{Ok: true, Term: 1},
				Call:   0,
				Return: 1,
			},
		}

		result := VerifyOperationsWithModel(ModelTypeLeaderHA, ops)
		assert.True(t, result.Passed)
	})

	t.Run("unknown model", func(t *testing.T) {
		result := VerifyOperationsWithModel(EnhancedModelType("unknown"), nil)
		assert.False(t, result.Passed)
		assert.Contains(t, result.Message, "unknown model type")
	})
}

func TestVerifyAllModels(t *testing.T) {
	rec := NewEnhancedHistoryRecorder("test", &testTimestampGenerator{})

	t.Run("empty recorder", func(t *testing.T) {
		results := VerifyAllModels(rec)
		assert.Empty(t, results)
	})

	t.Run("all model types", func(t *testing.T) {
		// 记录拓扑操作
		topologyOpID := rec.RecordTopologyCall(TopologyOperation{
			Type:  TopologyOpInitTopology,
			Nodes: []*NodeInfo{{NodeID: "root"}},
		})
		rec.RecordTopologyReturn(topologyOpID, TopologyOutput{Ok: true})

		// 记录失败恢复操作
		frOpID := rec.RecordFailureRecoveryCall(FailureRecoveryOperation{
			Type:     FailureRecoveryOpInit,
			AllNodes: []string{"node1"},
		})
		rec.RecordFailureRecoveryReturn(frOpID, FailureRecoveryOutput{Ok: true})

		// 记录 Leader HA 操作
		leaderOpID := rec.RecordLeaderHACall(LeaderHAOperation{
			Type:  LeaderHAOpInit,
			Nodes: []*NodeInfo{{NodeID: "leader"}},
		})
		rec.RecordLeaderHAReturn(leaderOpID, LeaderHAOutput{Ok: true, Term: 1})

		results := VerifyAllModels(rec)
		require.Len(t, results, 3)

		for _, r := range results {
			assert.True(t, r.Passed, "Model %s should pass", r.ModelType)
		}
	})
}

func TestVerifyAllPassed(t *testing.T) {
	t.Run("all passed", func(t *testing.T) {
		results := []VerifyResult{
			{ModelType: ModelTypeTopology, Passed: true},
			{ModelType: ModelTypeFailureRecovery, Passed: true},
		}
		assert.True(t, VerifyAllPassed(results))
	})

	t.Run("some failed", func(t *testing.T) {
		results := []VerifyResult{
			{ModelType: ModelTypeTopology, Passed: true},
			{ModelType: ModelTypeFailureRecovery, Passed: false},
		}
		assert.False(t, VerifyAllPassed(results))
	})

	t.Run("empty results", func(t *testing.T) {
		assert.True(t, VerifyAllPassed(nil))
	})
}

// ==================== 模型工厂测试 ====================

func TestModelFactory(t *testing.T) {
	factory := NewModelFactory()

	t.Run("create topology model", func(t *testing.T) {
		model, err := factory.CreateModel(ModelTypeTopology)
		assert.NoError(t, err)
		assert.NotNil(t, model)
	})

	t.Run("create all models", func(t *testing.T) {
		models := factory.CreateAllModels()
		assert.Len(t, models, 3)
	})
}

// ==================== 场景验证器测试 ====================

func TestScenarioValidator(t *testing.T) {
	validator := NewScenarioValidator("test", &testTimestampGenerator{})

	t.Run("recorder access", func(t *testing.T) {
		assert.NotNil(t, validator.Recorder())
	})

	t.Run("validate empty topology", func(t *testing.T) {
		result := validator.ValidateTopologyScenario()
		assert.True(t, result.Passed)
		assert.Contains(t, result.Message, "no operations")
	})

	t.Run("validate empty failure recovery", func(t *testing.T) {
		result := validator.ValidateFailureRecoveryScenario()
		assert.True(t, result.Passed)
		assert.Contains(t, result.Message, "no operations")
	})

	t.Run("validate empty leader ha", func(t *testing.T) {
		result := validator.ValidateLeaderHAScenario()
		assert.True(t, result.Passed)
		assert.Contains(t, result.Message, "no operations")
	})

	t.Run("validate with operations", func(t *testing.T) {
		// 记录操作
		opID := validator.Recorder().RecordTopologyCall(TopologyOperation{
			Type:  TopologyOpInitTopology,
			Nodes: []*NodeInfo{{NodeID: "root"}},
		})
		validator.Recorder().RecordTopologyReturn(opID, TopologyOutput{Ok: true})

		result := validator.ValidateTopologyScenario()
		assert.True(t, result.Passed)
	})

	t.Run("validate all", func(t *testing.T) {
		validator.Clear()

		// 记录多种类型的操作
		topologyID := validator.Recorder().RecordTopologyCall(TopologyOperation{
			Type: TopologyOpInitTopology,
		})
		validator.Recorder().RecordTopologyReturn(topologyID, TopologyOutput{Ok: true})

		frID := validator.Recorder().RecordFailureRecoveryCall(FailureRecoveryOperation{
			Type:     FailureRecoveryOpInit,
			AllNodes: []string{"node1"},
		})
		validator.Recorder().RecordFailureRecoveryReturn(frID, FailureRecoveryOutput{Ok: true})

		results := validator.ValidateAll()
		assert.Len(t, results, 2)
	})

	t.Run("clear", func(t *testing.T) {
		validator.Recorder().RecordTopologyCall(TopologyOperation{})
		assert.Greater(t, validator.Recorder().PendingLen(), 0)

		validator.Clear()
		assert.Equal(t, 0, validator.Recorder().Len())
		assert.Equal(t, 0, validator.Recorder().PendingLen())
	})
}

// ==================== 预定义场景模板测试 ====================

func TestRunTopologyInitScenario(t *testing.T) {
	nodes := []*NodeInfo{
		{NodeID: "root", Children: []string{"child"}},
		{NodeID: "child", ParentID: "root"},
	}

	passed, msg := RunTopologyInitScenario(nodes)
	assert.True(t, passed)
	assert.Contains(t, msg, "passed")
}

func TestRunFailureRecoveryScenario(t *testing.T) {
	t.Run("simple failure", func(t *testing.T) {
		allNodes := []string{"node1", "node2", "node3"}
		failedNodes := []string{"node1"}

		passed, msg := RunFailureRecoveryScenario(allNodes, failedNodes)
		assert.True(t, passed)
		assert.Contains(t, msg, "passed")
	})

	t.Run("multiple failures", func(t *testing.T) {
		allNodes := []string{"node1", "node2", "node3", "node4", "node5"}
		failedNodes := []string{"node1", "node2"}

		passed, _ := RunFailureRecoveryScenario(allNodes, failedNodes)
		assert.True(t, passed)
	})
}

func TestRunLeaderHAScenario(t *testing.T) {
	nodes := []*NodeInfo{
		{NodeID: "leader1", Children: []string{"child"}},
		{NodeID: "leader2", Children: []string{"child2"}},
		{NodeID: "child", ParentID: "leader1"},
	}

	t.Run("without switch", func(t *testing.T) {
		passed, msg := RunLeaderHAScenario(nodes, false)
		assert.True(t, passed)
		assert.Contains(t, msg, "passed")
	})

	t.Run("with switch", func(t *testing.T) {
		passed, _ := RunLeaderHAScenario(nodes, true)
		assert.True(t, passed)
	})
}

// ==================== 集成测试 ====================

func TestIntegration_FullWorkflow(t *testing.T) {
	// 创建场景验证器
	validator := NewScenarioValidator("integration-test", &testTimestampGenerator{})

	// 1. 拓扑初始化
	topologyInitOp := TopologyOperation{
		Type: TopologyOpInitTopology,
		Nodes: []*NodeInfo{
			{NodeID: "root", Children: []string{"middle"}},
			{NodeID: "middle", ParentID: "root", Children: []string{"leaf"}},
			{NodeID: "leaf", ParentID: "middle"},
		},
	}
	topologyInitID := validator.Recorder().RecordTopologyCall(topologyInitOp)
	validator.Recorder().RecordTopologyReturn(topologyInitID, TopologyOutput{Ok: true})

	// 2. 拓扑 Gossip 写入
	gossipWriteOp := TopologyOperation{
		Type:    TopologyOpWriteGossip,
		NodeID:  "root",
		Key:     "test-key",
		Value:   []byte("test-value"),
		Version: 1,
	}
	gossipWriteID := validator.Recorder().RecordTopologyCall(gossipWriteOp)
	validator.Recorder().RecordTopologyReturn(gossipWriteID, TopologyOutput{Ok: true})

	// 3. 拓扑读取
	getOp := TopologyOperation{
		Type:   TopologyOpGet,
		NodeID: "root",
		Key:    "test-key",
	}
	getID := validator.Recorder().RecordTopologyCall(getOp)
	validator.Recorder().RecordTopologyReturn(getID, TopologyOutput{Ok: true, Value: []byte("test-value"), Version: 1})

	// 4. 失败恢复场景
	frInitOp := FailureRecoveryOperation{
		Type:     FailureRecoveryOpInit,
		AllNodes: []string{"node1", "node2", "node3"},
	}
	frInitID := validator.Recorder().RecordFailureRecoveryCall(frInitOp)
	validator.Recorder().RecordFailureRecoveryReturn(frInitID, FailureRecoveryOutput{Ok: true})

	nodeFailOp := FailureRecoveryOperation{
		Type:   FailureRecoveryOpNodeFail,
		NodeID: "node1",
	}
	nodeFailID := validator.Recorder().RecordFailureRecoveryCall(nodeFailOp)
	validator.Recorder().RecordFailureRecoveryReturn(nodeFailID, FailureRecoveryOutput{Ok: true})

	// 5. Leader HA 场景
	leaderInitOp := LeaderHAOperation{
		Type: LeaderHAOpInit,
		Nodes: []*NodeInfo{
			{NodeID: "leader1", Children: []string{"child"}},
			{NodeID: "leader2", Children: []string{"child2"}},
		},
	}
	leaderInitID := validator.Recorder().RecordLeaderHACall(leaderInitOp)
	validator.Recorder().RecordLeaderHAReturn(leaderInitID, LeaderHAOutput{Ok: true, Term: 1})

	leaderWriteOp := LeaderHAOperation{
		Type:    LeaderHAOpWrite,
		NodeID:  "leader1",
		Key:     "test-key",
		Value:   []byte("test-value"),
		Version: 1,
		Term:    1,
	}
	leaderWriteID := validator.Recorder().RecordLeaderHACall(leaderWriteOp)
	validator.Recorder().RecordLeaderHAReturn(leaderWriteID, LeaderHAOutput{Ok: true})

	// 验证所有场景
	results := validator.ValidateAll()
	require.Len(t, results, 3)

	t.Run("topology model passed", func(t *testing.T) {
		assert.True(t, results[0].Passed)
		assert.Equal(t, ModelTypeTopology, results[0].ModelType)
	})

	t.Run("failure recovery model passed", func(t *testing.T) {
		assert.True(t, results[1].Passed)
		assert.Equal(t, ModelTypeFailureRecovery, results[1].ModelType)
	})

	t.Run("leader ha model passed", func(t *testing.T) {
		assert.True(t, results[2].Passed)
		assert.Equal(t, ModelTypeLeaderHA, results[2].ModelType)
	})

	t.Run("all models passed", func(t *testing.T) {
		assert.True(t, VerifyAllPassed(results))
	})
}

// ==================== 增强模型类型测试 ====================

func TestEnhancedModelType_String(t *testing.T) {
	assert.Equal(t, "topology", ModelTypeTopology.String())
	assert.Equal(t, "failure_recovery", ModelTypeFailureRecovery.String())
	assert.Equal(t, "leader_ha", ModelTypeLeaderHA.String())
}
