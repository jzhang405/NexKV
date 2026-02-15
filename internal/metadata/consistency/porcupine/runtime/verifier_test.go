// Package runtime 提供 Porcupine 运行时验证器
// 本文件测试运行时验证器
package runtime

import (
	"sync"
	"testing"
	"time"
)

// ==================== 创建测试 ====================

func TestNewRuntimeVerifier(t *testing.T) {
	config := DefaultVerifierConfig()
	verifier := NewRuntimeVerifier(config, "test-node-1")

	assertVerifierCreated(t, verifier)
	assertHookNotNil(t, verifier.GossipHook(), "GossipHook")
	assertHookNotNil(t, verifier.QuorumHook(), "QuorumHook")
	assertHookNotNil(t, verifier.FailureHook(), "FailureHook")
	assertHookNotNil(t, verifier.DegradationHook(), "DegradationHook")
	assertHookNotNil(t, verifier.LeaderHook(), "LeaderHook")
}

func TestNewRuntimeVerifierWithDefaults(t *testing.T) {
	verifier := NewRuntimeVerifierWithDefaults("test-node-1")
	assertVerifierCreated(t, verifier)
}

// ==================== Hook 访问器测试 ====================

func TestRuntimeVerifier_Hooks(t *testing.T) {
	verifier := NewRuntimeVerifierWithDefaults("test-node-1")

	assertHookNotNil(t, verifier.GossipHook(), "GossipHook")
	assertHookNotNil(t, verifier.QuorumHook(), "QuorumHook")
	assertHookNotNil(t, verifier.FailureHook(), "FailureHook")
	assertHookNotNil(t, verifier.DegradationHook(), "DegradationHook")
	assertHookNotNil(t, verifier.LeaderHook(), "LeaderHook")
	assertHookNotNil(t, verifier.Recorder(), "Recorder")
}

// ==================== 验证方法测试 ====================

func TestRuntimeVerifier_Verify_NoOps(t *testing.T) {
	verifier := NewRuntimeVerifier(syncTestConfig(), "test-node-1")

	result := verifier.Verify()
	assertResultNotNil(t, result)

	if !result.AllPassed() {
		t.Errorf("Expected all passed when no ops, got: %s", result.Summary())
	}
}

func TestRuntimeVerifier_Verify_WithOps(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	verifier.gossipHook.SetEnabled(true)
	opID := verifier.gossipHook.OnGossipWrite("key1", []byte("value1"))
	verifier.gossipHook.OnGossipReturn(opID, true, "")

	result := verifier.Verify()
	assertResultNotNil(t, result)

	if result.TotalOps == 0 {
		t.Error("Expected some operations to be recorded")
	}
}

func TestRuntimeVerifier_VerifyOnCriticalEvent(t *testing.T) {
	verifier := NewRuntimeVerifier(syncTestConfig(), "test-node-1")

	result := verifier.VerifyOnCriticalEvent("test-event")
	assertResultNotNil(t, result)
}

// ==================== 结果访问器测试 ====================

func TestRuntimeVerifier_GetLastResult(t *testing.T) {
	verifier := NewRuntimeVerifier(syncTestConfig(), "test-node-1")

	// 初始没有结果
	if result := verifier.GetLastResult(); result != nil {
		t.Error("Expected nil result before first verify")
	}

	verifier.Verify()
	assertResultNotNil(t, verifier.GetLastResult())
}

func TestRuntimeVerifier_GetResultHistory(t *testing.T) {
	verifier := NewRuntimeVerifier(configWithHistorySize(10), "test-node-1")

	assertHistoryLength(t, verifier.GetResultHistory(), 0)

	for i := 0; i < 5; i++ {
		verifier.Verify()
	}

	assertHistoryLength(t, verifier.GetResultHistory(), 5)
}

func TestRuntimeVerifier_GetResultHistory_Limit(t *testing.T) {
	verifier := NewRuntimeVerifier(configWithHistorySize(3), "test-node-1")

	for i := 0; i < 5; i++ {
		verifier.Verify()
	}

	assertHistoryLength(t, verifier.GetResultHistory(), 3)
}

// ==================== 生命周期测试 ====================

func TestRuntimeVerifier_StartStop(t *testing.T) {
	verifier := NewRuntimeVerifier(configWithInterval(100*time.Millisecond), "test-node-1")

	verifier.Start()
	waitForIntervalVerification()
	verifier.Stop()
	verifier.Stop() // 再次 Stop 应该安全
}

func TestRuntimeVerifier_Start_NoInterval(t *testing.T) {
	config := DefaultVerifierConfig()
	config.Enabled = true
	config.VerifyInterval = 0
	verifier := NewRuntimeVerifier(config, "test-node-1")

	verifier.Start()
	verifier.Stop()
}

func TestRuntimeVerifier_StartStop_Disabled(t *testing.T) {
	verifier := NewRuntimeVerifier(disabledConfig(), "test-node-1")

	verifier.Start()
	verifier.Stop()
}

// ==================== 配置访问器测试 ====================

func TestRuntimeVerifier_SetEnabled(t *testing.T) {
	config := disabledConfig()
	config.GossipEnabled = true
	verifier := NewRuntimeVerifier(config, "test-node-1")

	if verifier.gossipHook.Enabled() {
		t.Error("Expected GossipHook to be disabled initially")
	}

	verifier.SetEnabled(true)
	if !verifier.gossipHook.Enabled() {
		t.Error("Expected GossipHook to be enabled after SetEnabled(true)")
	}

	verifier.SetEnabled(false)
	if verifier.gossipHook.Enabled() {
		t.Error("Expected GossipHook to be disabled after SetEnabled(false)")
	}
}

// ==================== 单个 Hook 启用测试 ====================

func TestRuntimeVerifier_SetGossipEnabled(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	verifier.SetGossipEnabled(false)
	if verifier.gossipHook.Enabled() {
		t.Error("Expected GossipHook to be disabled")
	}

	verifier.SetGossipEnabled(true)
	if !verifier.gossipHook.Enabled() {
		t.Error("Expected GossipHook to be enabled")
	}
}

func TestRuntimeVerifier_SetQuorumEnabled(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	verifier.SetQuorumEnabled(false)
	if verifier.quorumHook.Enabled() {
		t.Error("Expected QuorumHook to be disabled")
	}

	verifier.SetQuorumEnabled(true)
	if !verifier.quorumHook.Enabled() {
		t.Error("Expected QuorumHook to be enabled")
	}
}

func TestRuntimeVerifier_SetFailureEnabled(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	verifier.SetFailureEnabled(false)
	if verifier.failureHook.Enabled() {
		t.Error("Expected FailureHook to be disabled")
	}

	verifier.SetFailureEnabled(true)
	if !verifier.failureHook.Enabled() {
		t.Error("Expected FailureHook to be enabled")
	}
}

func TestRuntimeVerifier_SetDegradationEnabled(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	verifier.SetDegradationEnabled(false)
	if verifier.degradationHook.Enabled() {
		t.Error("Expected DegradationHook to be disabled")
	}

	verifier.SetDegradationEnabled(true)
	if !verifier.degradationHook.Enabled() {
		t.Error("Expected DegradationHook to be enabled")
	}
}

func TestRuntimeVerifier_SetLeaderEnabled(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	verifier.SetLeaderEnabled(false)
	if verifier.leaderHook.Enabled() {
		t.Error("Expected LeaderHook to be disabled")
	}

	verifier.SetLeaderEnabled(true)
	if !verifier.leaderHook.Enabled() {
		t.Error("Expected LeaderHook to be enabled")
	}
}

// ==================== 统计信息测试 ====================

func TestRuntimeVerifier_Stats(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	stats := verifier.Stats()

	if stats.GossipStats.TotalRecorded != 0 {
		t.Error("Expected GossipStats.TotalRecorded=0")
	}
	if stats.QuorumStats.TotalRecorded != 0 {
		t.Error("Expected QuorumStats.TotalRecorded=0")
	}
	if stats.FailureStats.TotalRecorded != 0 {
		t.Error("Expected FailureStats.TotalRecorded=0")
	}
	if stats.DegradationStats.TotalRecorded != 0 {
		t.Error("Expected DegradationStats.TotalRecorded=0")
	}
	if stats.LeaderStats.TotalRecorded != 0 {
		t.Error("Expected LeaderStats.TotalRecorded=0")
	}
}

func TestRuntimeVerifier_Stats_AfterOps(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	verifier.gossipHook.SetEnabled(true)
	_ = verifier.gossipHook.OnGossipWrite("key1", []byte("value1"))

	stats := verifier.Stats()
	if stats.GossipStats.TotalRecorded != 1 {
		t.Errorf("Expected GossipStats.TotalRecorded=1, got %d", stats.GossipStats.TotalRecorded)
	}
}

// ==================== 并发安全测试 ====================

func TestRuntimeVerifier_ConcurrentVerify(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			verifier.Verify()
		}()
	}
	wg.Wait()
}

func TestRuntimeVerifier_ConcurrentSetEnabled(t *testing.T) {
	verifier := testVerifierWithSync("test-node-1")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)

		go func(val bool) {
			defer wg.Done()
			verifier.SetEnabled(val)
		}(i%2 == 0)

		go func() {
			defer wg.Done()
			_ = verifier.Stats()
		}()
	}
	wg.Wait()
}

// ==================== 内存控制测试 ====================

func TestRuntimeVerifier_TrimRecorderHistory(t *testing.T) {
	verifier := NewRuntimeVerifier(configWithMaxOps(5), "test-node-1")

	verifier.gossipHook.SetEnabled(true)
	for i := 0; i < 10; i++ {
		opID := verifier.gossipHook.OnGossipWrite("key", []byte("value"))
		verifier.gossipHook.OnGossipReturn(opID, true, "")
	}

	verifier.Verify()

	if verifier.recorder.Len() > 5 {
		t.Errorf("Expected recorder length <= 5, got %d", verifier.recorder.Len())
	}
}

func TestRuntimeVerifier_TrimRecorderHistory_Disabled(t *testing.T) {
	config := configWithMaxOps(0) // 禁用 trim
	verifier := NewRuntimeVerifier(config, "test-node-1")

	verifier.gossipHook.SetEnabled(true)
	for i := 0; i < 10; i++ {
		opID := verifier.gossipHook.OnGossipWrite("key", []byte("value"))
		verifier.gossipHook.OnGossipReturn(opID, true, "")
	}

	verifier.Verify()

	if verifier.recorder.Len() != 10 {
		t.Errorf("Expected recorder length=10, got %d", verifier.recorder.Len())
	}
}
