// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件测试增强记录器
package porcupine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 辅助函数 ====================

func newTestEnhancedRecorder() *EnhancedHistoryRecorder {
	return NewEnhancedHistoryRecorder("test-client", &testTimestampGenerator{})
}

// ==================== 基础功能测试 ====================

func TestEnhancedHistoryRecorder_Creation(t *testing.T) {
	rec := newTestEnhancedRecorder()

	assert.NotNil(t, rec)
	assert.Equal(t, 0, rec.Len())
	assert.Equal(t, 0, rec.PendingLen())
}

func TestEnhancedOpType_String(t *testing.T) {
	tests := []struct {
		opType   EnhancedOpType
		expected string
	}{
		{OpTypeTopology, "Topology"},
		{OpTypeFailureRecovery, "FailureRecovery"},
		{OpTypeLeaderHA, "LeaderHA"},
		{EnhancedOpType(99), "Unknown"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.opType.String())
	}
}

// ==================== 拓扑感知操作测试 ====================

func TestEnhancedHistoryRecorder_TopologyOps(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 记录初始化操作
	initOp := TopologyOperation{
		Type: TopologyOpInitTopology,
		Nodes: []*NodeInfo{
			{NodeID: "root", Children: []string{"child"}},
		},
	}
	opID := rec.RecordTopologyCall(initOp)
	assert.Equal(t, 0, rec.Len())
	assert.Equal(t, 1, rec.PendingLen())

	// 检查待完成操作
	pending := rec.GetPendingInput(opID)
	require.NotNil(t, pending)
	assert.Equal(t, OpTypeTopology, pending.Type)
	assert.Equal(t, TopologyOpInitTopology, pending.TopologyOp.Type)

	// 记录返回
	initOutput := TopologyOutput{Ok: true}
	rec.RecordTopologyReturn(opID, initOutput)

	assert.Equal(t, 1, rec.Len())
	assert.Equal(t, 0, rec.PendingLen())

	// 验证操作记录
	ops := rec.GetOperations()
	require.Len(t, ops, 1)
	assert.Equal(t, OpTypeTopology, ops[0].Input.(EnhancedInput).Type)
}

func TestEnhancedHistoryRecorder_TopologyOpsFilter(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 记录多种类型的操作
	topologyOp := TopologyOperation{Type: TopologyOpGet, NodeID: "node1", Key: "key1"}
	topologyOpID := rec.RecordTopologyCall(topologyOp)
	rec.RecordTopologyReturn(topologyOpID, TopologyOutput{Ok: true})

	frOp := FailureRecoveryOperation{Type: FailureRecoveryOpGet, NodeID: "node2", Key: "key2"}
	frOpID := rec.RecordFailureRecoveryCall(frOp)
	rec.RecordFailureRecoveryReturn(frOpID, FailureRecoveryOutput{Ok: true})

	// 验证过滤
	topologyOps := rec.GetTopologyOperations()
	require.Len(t, topologyOps, 1)
	assert.Equal(t, OpTypeTopology, topologyOps[0].Input.(EnhancedInput).Type)
}

// ==================== 失败恢复操作测试 ====================

func TestEnhancedHistoryRecorder_FailureRecoveryOps(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 记录节点故障操作
	failOp := FailureRecoveryOperation{
		Type:   FailureRecoveryOpNodeFail,
		NodeID: "node1",
	}
	opID := rec.RecordFailureRecoveryCall(failOp)
	assert.Equal(t, 1, rec.PendingLen())

	// 记录返回
	failOutput := FailureRecoveryOutput{Ok: true}
	rec.RecordFailureRecoveryReturn(opID, failOutput)

	assert.Equal(t, 1, rec.Len())

	// 验证失败恢复操作过滤
	frOps := rec.GetFailureRecoveryOperations()
	require.Len(t, frOps, 1)
	assert.Equal(t, OpTypeFailureRecovery, frOps[0].Input.(EnhancedInput).Type)
}

func TestEnhancedHistoryRecorder_FailureRecoveryOpsFilter(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 记录失败恢复操作
	frOp := FailureRecoveryOperation{Type: FailureRecoveryOpNodeFail, NodeID: "node1"}
	frOpID := rec.RecordFailureRecoveryCall(frOp)
	rec.RecordFailureRecoveryReturn(frOpID, FailureRecoveryOutput{Ok: true})

	// 记录其他类型的操作
	topologyOp := TopologyOperation{Type: TopologyOpGet}
	topologyOpID := rec.RecordTopologyCall(topologyOp)
	rec.RecordTopologyReturn(topologyOpID, TopologyOutput{Ok: true})

	// 验证过滤
	frOps := rec.GetFailureRecoveryOperations()
	require.Len(t, frOps, 1)
	assert.Equal(t, OpTypeFailureRecovery, frOps[0].Input.(EnhancedInput).Type)
}

// ==================== Leader HA 操作测试 ====================

func TestEnhancedHistoryRecorder_LeaderHAOps(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 记录 Leader 切换操作
	changeOp := LeaderHAOperation{
		Type:      LeaderHAOpLeaderChange,
		Term:      1,
		NewLeader: "leader2",
	}
	opID := rec.RecordLeaderHACall(changeOp)
	assert.Equal(t, 1, rec.PendingLen())

	// 记录返回
	changeOutput := LeaderHAOutput{
		Ok:         true,
		NewLeader:  "leader2",
		ActiveTerm: 2,
	}
	rec.RecordLeaderHAReturn(opID, changeOutput)

	assert.Equal(t, 1, rec.Len())

	// 验证 Leader HA 操作过滤
	leaderOps := rec.GetLeaderHAOperations()
	require.Len(t, leaderOps, 1)
	assert.Equal(t, OpTypeLeaderHA, leaderOps[0].Input.(EnhancedInput).Type)
}

func TestEnhancedHistoryRecorder_LeaderHAOpsFilter(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 记录 Leader HA 操作
	leaderOp := LeaderHAOperation{Type: LeaderHAOpLeaderChange}
	leaderOpID := rec.RecordLeaderHACall(leaderOp)
	rec.RecordLeaderHAReturn(leaderOpID, LeaderHAOutput{Ok: true})

	// 记录其他类型的操作
	frOp := FailureRecoveryOperation{Type: FailureRecoveryOpNodeFail}
	frOpID := rec.RecordFailureRecoveryCall(frOp)
	rec.RecordFailureRecoveryReturn(frOpID, FailureRecoveryOutput{Ok: true})

	// 验证过滤
	leaderOps := rec.GetLeaderHAOperations()
	require.Len(t, leaderOps, 1)
	assert.Equal(t, OpTypeLeaderHA, leaderOps[0].Input.(EnhancedInput).Type)
}

// ==================== 混合操作测试 ====================

func TestEnhancedHistoryRecorder_MixedOps(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 记录混合操作
	topologyOp := TopologyOperation{Type: TopologyOpGet, NodeID: "node1", Key: "key1"}
	topologyOpID := rec.RecordTopologyCall(topologyOp)

	frOp := FailureRecoveryOperation{Type: FailureRecoveryOpNodeFail, NodeID: "node2"}
	frOpID := rec.RecordFailureRecoveryCall(frOp)

	leaderOp := LeaderHAOperation{Type: LeaderHAOpWrite, NodeID: "leader1"}
	leaderOpID := rec.RecordLeaderHACall(leaderOp)

	assert.Equal(t, 3, rec.PendingLen())
	assert.Equal(t, 0, rec.Len())

	// 记录返回
	rec.RecordTopologyReturn(topologyOpID, TopologyOutput{Ok: true})
	rec.RecordFailureRecoveryReturn(frOpID, FailureRecoveryOutput{Ok: true})
	rec.RecordLeaderHAReturn(leaderOpID, LeaderHAOutput{Ok: true})

	assert.Equal(t, 0, rec.PendingLen())
	assert.Equal(t, 3, rec.Len())

	// 验证统计
	stats := rec.Stats()
	assert.Equal(t, 3, stats.TotalOps)
	assert.Equal(t, 0, stats.PendingOps)
	assert.Equal(t, 1, stats.ByType[OpTypeTopology])
	assert.Equal(t, 1, stats.ByType[OpTypeFailureRecovery])
	assert.Equal(t, 1, stats.ByType[OpTypeLeaderHA])
}

func TestEnhancedHistoryRecorder_Clear(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 记录一些操作
	op := TopologyOperation{Type: TopologyOpGet}
	opID := rec.RecordTopologyCall(op)
	rec.RecordTopologyReturn(opID, TopologyOutput{Ok: true})

	assert.Equal(t, 1, rec.Len())

	// 清空
	rec.Clear()

	assert.Equal(t, 0, rec.Len())
	assert.Equal(t, 0, rec.PendingLen())
}

func TestEnhancedHistoryRecorder_InvalidOpID(t *testing.T) {
	rec := newTestEnhancedRecorder()

	// 尝试记录不存在的操作 ID 的返回
	rec.RecordTopologyReturn(999, TopologyOutput{Ok: true})

	// 不应该崩溃，也不应该记录任何操作
	assert.Equal(t, 0, rec.Len())

	// 获取不存在的待完成操作
	pending := rec.GetPendingInput(999)
	assert.Nil(t, pending)
}

// ==================== 验证功能测试 ====================

func TestVerifyTopology(t *testing.T) {
	t.Run("empty operations", func(t *testing.T) {
		rec := newTestEnhancedRecorder()
		passed, msg := VerifyTopology(rec)
		assert.True(t, passed)
		assert.Contains(t, msg, "no topology operations")
	})

	t.Run("valid operations", func(t *testing.T) {
		rec := newTestEnhancedRecorder()

		// 记录一个有效的拓扑操作序列
		initOp := TopologyOperation{
			Type: TopologyOpInitTopology,
			Nodes: []*NodeInfo{
				{NodeID: "root", Children: []string{"child"}},
				{NodeID: "child", ParentID: "root"},
			},
		}
		initOpID := rec.RecordTopologyCall(initOp)
		rec.RecordTopologyReturn(initOpID, TopologyOutput{Ok: true})

		passed, msg := VerifyTopology(rec)
		assert.True(t, passed)
		assert.Contains(t, msg, "passed")
	})
}

func TestVerifyFailureRecovery(t *testing.T) {
	t.Run("empty operations", func(t *testing.T) {
		rec := newTestEnhancedRecorder()
		passed, msg := VerifyFailureRecovery(rec)
		assert.True(t, passed)
		assert.Contains(t, msg, "no failure recovery operations")
	})

	t.Run("valid operations", func(t *testing.T) {
		rec := newTestEnhancedRecorder()

		// 记录一个有效的失败恢复操作序列
		initOp := FailureRecoveryOperation{
			Type:     FailureRecoveryOpInit,
			AllNodes: []string{"node1", "node2", "node3"},
		}
		initOpID := rec.RecordFailureRecoveryCall(initOp)
		rec.RecordFailureRecoveryReturn(initOpID, FailureRecoveryOutput{Ok: true})

		passed, msg := VerifyFailureRecovery(rec)
		assert.True(t, passed)
		assert.Contains(t, msg, "passed")
	})
}

func TestVerifyLeaderHA(t *testing.T) {
	t.Run("empty operations", func(t *testing.T) {
		rec := newTestEnhancedRecorder()
		passed, msg := VerifyLeaderHA(rec)
		assert.True(t, passed)
		assert.Contains(t, msg, "no leader HA operations")
	})

	t.Run("valid operations", func(t *testing.T) {
		rec := newTestEnhancedRecorder()

		// 记录一个有效的 Leader HA 操作序列
		initOp := LeaderHAOperation{
			Type: LeaderHAOpInit,
			Nodes: []*NodeInfo{
				{NodeID: "leader1", Children: []string{"child"}},
				{NodeID: "child", ParentID: "leader1"},
			},
		}
		initOpID := rec.RecordLeaderHACall(initOp)
		rec.RecordLeaderHAReturn(initOpID, LeaderHAOutput{Ok: true, Term: 1})

		passed, msg := VerifyLeaderHA(rec)
		assert.True(t, passed)
		assert.Contains(t, msg, "passed")
	})
}

func TestVerifyAll(t *testing.T) {
	t.Run("empty recorder", func(t *testing.T) {
		rec := newTestEnhancedRecorder()
		passed, messages := VerifyAll(rec)
		assert.True(t, passed)
		assert.Empty(t, messages)
	})

	t.Run("all types passed", func(t *testing.T) {
		rec := newTestEnhancedRecorder()

		// 拓扑操作
		topologyInitID := rec.RecordTopologyCall(TopologyOperation{
			Type:  TopologyOpInitTopology,
			Nodes: []*NodeInfo{{NodeID: "root", Children: []string{"child"}}},
		})
		rec.RecordTopologyReturn(topologyInitID, TopologyOutput{Ok: true})

		// 失败恢复操作
		frInitID := rec.RecordFailureRecoveryCall(FailureRecoveryOperation{
			Type:     FailureRecoveryOpInit,
			AllNodes: []string{"node1"},
		})
		rec.RecordFailureRecoveryReturn(frInitID, FailureRecoveryOutput{Ok: true})

		// Leader HA 操作
		leaderInitID := rec.RecordLeaderHACall(LeaderHAOperation{
			Type:  LeaderHAOpInit,
			Nodes: []*NodeInfo{{NodeID: "leader", Children: []string{"child"}}},
		})
		rec.RecordLeaderHAReturn(leaderInitID, LeaderHAOutput{Ok: true, Term: 1})

		passed, messages := VerifyAll(rec)
		assert.True(t, passed)
		assert.Len(t, messages, 3)
		for _, msg := range messages {
			assert.Contains(t, msg, "[PASS]")
		}
	})
}

// ==================== 并发安全测试 ====================

func TestEnhancedHistoryRecorder_ConcurrentAccess(t *testing.T) {
	rec := newTestEnhancedRecorder()
	done := make(chan bool)

	// 并发写入
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				op := TopologyOperation{
					Type:   TopologyOpGet,
					NodeID: "node",
					Key:    "key",
				}
				opID := rec.RecordTopologyCall(op)
				rec.RecordTopologyReturn(opID, TopologyOutput{Ok: true})
			}
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证操作数量
	assert.Equal(t, 100, rec.Len())
}
