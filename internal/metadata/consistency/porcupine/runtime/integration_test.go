// Package runtime 提供 Porcupine 运行时验证器
// 本文件测试集成场景
package runtime

import (
	"testing"
	"time"
)

// ==================== 集成测试 ====================

// TestIntegration_Gossip 测试 Gossip 写入 -> 验证流程
func TestIntegration_Gossip(t *testing.T) {
	verifier := testVerifierWithSync("node-1")
	verifier.SetGossipEnabled(true)

	gossipHook := verifier.GossipHook()
	opID1 := gossipHook.OnGossipWrite("key1", []byte("value1"))
	gossipHook.OnGossipReturn(opID1, true, "")

	opID2 := gossipHook.OnGossipWrite("key2", []byte("value2"))
	gossipHook.OnGossipReturn(opID2, true, "")

	result := verifier.Verify()
	assertResultNotNil(t, result)

	t.Logf("Gossip Integration: %s, TotalOps=%d, Duration=%v",
		result.Summary(), result.TotalOps, result.Duration)

	if result.TotalOps == 0 {
		t.Error("Expected some topology operations")
	}
}

// TestIntegration_Quorum 测试 Quorum 写入 -> 验证流程
func TestIntegration_Quorum(t *testing.T) {
	verifier := testVerifierWithSync("node-1")
	verifier.SetQuorumEnabled(true)

	quorumHook := verifier.QuorumHook()
	participants := []string{"node-1", "node-2", "node-3"}

	opID1 := quorumHook.OnQuorumWrite("key1", []byte("value1"), participants)
	quorumHook.OnQuorumReturn(opID1, true, "")

	opID2 := quorumHook.OnQuorumWrite("key2", []byte("value2"), participants)
	quorumHook.OnQuorumReturn(opID2, false, "quorum not reached")

	result := verifier.Verify()
	t.Logf("Quorum Integration: %s, TotalOps=%d", result.Summary(), result.TotalOps)

	if result.TotalOps == 0 {
		t.Error("Expected some topology operations")
	}
}

// TestIntegration_Failure 测试节点故障/恢复 -> 验证流程
func TestIntegration_Failure(t *testing.T) {
	verifier := testVerifierWithSync("node-1")
	verifier.SetFailureEnabled(true)

	failureHook := verifier.FailureHook()

	opID1 := failureHook.OnNodeFailure("node-2")
	failureHook.OnFailureReturn(opID1, true, "")

	opID2 := failureHook.OnNodeRecovery("node-2")
	failureHook.OnFailureReturn(opID2, true, "")

	opID3 := failureHook.OnNodeFailure("node-3")
	failureHook.OnFailureReturn(opID3, true, "")

	result := verifier.Verify()
	t.Logf("Failure Integration: %s, TotalOps=%d", result.Summary(), result.TotalOps)

	if result.TotalOps == 0 {
		t.Error("Expected some failure recovery operations")
	}
}

// TestIntegration_Degradation 测试降级写入 -> 验证流程
func TestIntegration_Degradation(t *testing.T) {
	verifier := testVerifierWithSync("node-1")
	verifier.SetDegradationEnabled(true)

	degradationHook := verifier.DegradationHook()

	opID1 := degradationHook.OnDegradedWrite("key1", []byte("value1"))
	degradationHook.OnDegradedReturn(opID1, true, false)

	opID2 := degradationHook.OnDegradedWrite("key2", []byte("value2"))
	degradationHook.OnDegradedReturn(opID2, true, true)

	result := verifier.Verify()
	t.Logf("Degradation Integration: %s, TotalOps=%d", result.Summary(), result.TotalOps)

	if result.TotalOps == 0 {
		t.Error("Expected some failure recovery operations (degradation)")
	}
}

// TestIntegration_Leader 测试 Leader 变更 -> 验证流程
func TestIntegration_Leader(t *testing.T) {
	verifier := testVerifierWithSync("node-1")
	verifier.SetLeaderEnabled(true)

	leaderHook := verifier.LeaderHook()

	opID1 := leaderHook.OnLeaderChange("node-1", "node-2", 2)
	leaderHook.OnLeaderChangeReturn(opID1, true, "", "node-2", 2)

	opID2 := leaderHook.OnFencingWrite("key1", []byte("value1"), 2)
	leaderHook.OnFencingWriteReturn(opID2, true, "", 2)

	opID3 := leaderHook.OnLeaderChange("node-2", "node-3", 3)
	leaderHook.OnLeaderChangeReturn(opID3, true, "", "node-3", 3)

	opID4 := leaderHook.OnFencingWrite("key1", []byte("value1"), 2)
	leaderHook.OnFencingWriteReturn(opID4, false, "stale fencing token", 3)

	result := verifier.Verify()
	t.Logf("Leader Integration: %s, TotalOps=%d", result.Summary(), result.TotalOps)

	if result.TotalOps == 0 {
		t.Error("Expected some leader HA operations")
	}
}

// TestIntegration_FullWorkflow 测试完整工作流验证
func TestIntegration_FullWorkflow(t *testing.T) {
	verifier := testVerifierWithSync("node-1")

	// 启用所有 Hook
	verifier.SetGossipEnabled(true)
	verifier.SetQuorumEnabled(true)
	verifier.SetFailureEnabled(true)
	verifier.SetDegradationEnabled(true)
	verifier.SetLeaderEnabled(true)

	// 1. Gossip 写入
	gossipHook := verifier.GossipHook()
	opID1 := gossipHook.OnGossipWrite("metadata-key", []byte("metadata-value"))
	gossipHook.OnGossipReturn(opID1, true, "")

	// 2. Quorum 写入
	quorumHook := verifier.QuorumHook()
	participants := []string{"node-1", "node-2", "node-3"}
	opID2 := quorumHook.OnQuorumWrite("critical-key", []byte("critical-value"), participants)
	quorumHook.OnQuorumReturn(opID2, true, "")

	// 3. 节点故障
	failureHook := verifier.FailureHook()
	opID3 := failureHook.OnNodeFailure("node-2")
	failureHook.OnFailureReturn(opID3, true, "")

	// 4. 降级写入
	degradationHook := verifier.DegradationHook()
	opID4 := degradationHook.OnDegradedWrite("degraded-key", []byte("degraded-value"))
	degradationHook.OnDegradedReturn(opID4, true, true)

	// 5. Leader 变更
	leaderHook := verifier.LeaderHook()
	opID5 := leaderHook.OnLeaderChange("node-1", "node-3", 2)
	leaderHook.OnLeaderChangeReturn(opID5, true, "", "node-3", 2)

	result := verifier.Verify()
	t.Logf("Full Workflow Integration: %s, TotalOps=%d, TopologyPass=%v, FailurePass=%v, LeaderHAPass=%v",
		result.Summary(), result.TotalOps, result.TopologyPass, result.FailurePass, result.LeaderHAPass)

	if result.TotalOps == 0 {
		t.Error("Expected some operations in full workflow")
	}

	stats := verifier.Stats()
	t.Logf("Stats: Gossip=%d, Quorum=%d, Failure=%d, Degradation=%d, Leader=%d",
		stats.GossipStats.TotalRecorded,
		stats.QuorumStats.TotalRecorded,
		stats.FailureStats.TotalRecorded,
		stats.DegradationStats.TotalRecorded,
		stats.LeaderStats.TotalRecorded)
}

// TestIntegration_Lifecycle 测试完整生命周期
func TestIntegration_Lifecycle(t *testing.T) {
	config := DefaultVerifierConfig()
	config.Enabled = true
	config.VerifyInterval = 50 * time.Millisecond
	config.AsyncConfig.Enabled = true
	config.AsyncConfig.BufferSize = 1000
	verifier := NewRuntimeVerifier(config, "node-1")

	verifier.Start()
	verifier.SetGossipEnabled(true)
	verifier.SetQuorumEnabled(true)

	gossipHook := verifier.GossipHook()
	for i := 0; i < 10; i++ {
		_ = gossipHook.OnGossipWrite("key", []byte("value"))
	}

	time.Sleep(100 * time.Millisecond)

	if result := verifier.GetLastResult(); result != nil {
		t.Logf("Lifecycle: Last result: %s", result.Summary())
	}

	t.Logf("Lifecycle: %d verification results in history", len(verifier.GetResultHistory()))

	verifier.Stop()
	_ = gossipHook.OnGossipWrite("key", []byte("value"))
}

// TestIntegration_MultipleVerifiers 测试多个验证器共享场景
func TestIntegration_MultipleVerifiers(t *testing.T) {
	verifier1 := testVerifierWithSync("node-1")
	verifier2 := testVerifierWithSync("node-2")

	verifier1.SetGossipEnabled(true)
	verifier2.SetGossipEnabled(true)

	gossip1 := verifier1.GossipHook()
	opID1 := gossip1.OnGossipWrite("key1", []byte("value1"))
	gossip1.OnGossipReturn(opID1, true, "")

	gossip2 := verifier2.GossipHook()
	opID2 := gossip2.OnGossipWrite("key2", []byte("value2"))
	gossip2.OnGossipReturn(opID2, true, "")

	result1 := verifier1.Verify()
	result2 := verifier2.Verify()

	t.Logf("Verifier1: %s, TotalOps=%d", result1.Summary(), result1.TotalOps)
	t.Logf("Verifier2: %s, TotalOps=%d", result2.Summary(), result2.TotalOps)

	if result1.TotalOps == 0 || result2.TotalOps == 0 {
		t.Error("Expected each verifier to have its own operations")
	}
}

// TestIntegration_ErrorHandling 测试错误处理
func TestIntegration_ErrorHandling(t *testing.T) {
	verifier := testVerifierWithSync("node-1")
	verifier.SetGossipEnabled(true)
	verifier.SetQuorumEnabled(true)

	gossipHook := verifier.GossipHook()

	// 无效的 opID return
	gossipHook.OnGossipReturn(-1, true, "")
	gossipHook.OnGossipReturn(9999, true, "")

	// 正常操作
	opID := gossipHook.OnGossipWrite("key", []byte("value"))
	gossipHook.OnGossipReturn(opID, true, "")

	result := verifier.Verify()
	t.Logf("Error Handling: %s, TotalOps=%d", result.Summary(), result.TotalOps)
}

// TestIntegration_HookDisabled 测试 Hook 禁用场景
func TestIntegration_HookDisabled(t *testing.T) {
	config := disabledConfig()
	config.AsyncConfig.Enabled = false
	verifier := NewRuntimeVerifier(config, "node-1")

	gossipHook := verifier.GossipHook()
	opID := gossipHook.OnGossipWrite("key", []byte("value"))
	gossipHook.OnGossipReturn(opID, true, "")

	result := verifier.Verify()

	if result.TotalOps != 0 {
		t.Errorf("Expected 0 ops when hook disabled, got %d", result.TotalOps)
	}

	t.Logf("Hook Disabled: TotalOps=%d", result.TotalOps)
}
