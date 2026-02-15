// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件测试 Leader Hook
package hooks

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

func TestNewLeaderHook(t *testing.T) {
	recorder := newTestRecorder()
	config := porcupine.DefaultAsyncRecordConfig()

	hook := NewLeaderHook(recorder, "node-1", config)
	assertHookCreated(t, hook, "LeaderHook")

	if hook.Recorder() != recorder {
		t.Error("Recorder mismatch")
	}
}

func TestLeaderHook_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	assertEnabledState(t, hook)
}

// ==================== Leader Change 测试 ====================

func TestLeaderHook_OnLeaderChange_Disabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	// 不启用 hook

	opID := hook.OnLeaderChange("node-1", "node-2", 2)
	assertDisabledHookReturnsMinus1(t, opID)
	assertTotalRecorded(t, hook.Stats(), 0)
}

func TestLeaderHook_OnLeaderChange_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnLeaderChange("node-1", "node-2", 2)
	assertValidOpID(t, opID)
	assertTotalRecorded(t, hook.Stats(), 1)
}

func TestLeaderHook_OnLeaderChangeReturn(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnLeaderChange("node-1", "node-2", 2)
	assertValidOpID(t, opID)

	hook.OnLeaderChangeReturn(opID, true, "", "node-2", 2)

	leaderOps := recorder.GetLeaderHAOperations()
	if len(leaderOps) == 0 {
		t.Error("Expected leader HA operations to be recorded")
	}
}

func TestLeaderHook_OnLeaderChangeReturn_InvalidOpID(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	// 不存在的 opID 和负数 opID 应该不会 panic
	hook.OnLeaderChangeReturn(9999, true, "", "node-2", 2)
	hook.OnLeaderChangeReturn(-1, true, "", "node-2", 2)
}

// ==================== Fencing Write 测试 ====================

func TestLeaderHook_OnFencingWrite_Disabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	// 不启用 hook

	opID := hook.OnFencingWrite("key1", []byte("value1"), 1)
	assertDisabledHookReturnsMinus1(t, opID)
	assertTotalRecorded(t, hook.Stats(), 0)
}

func TestLeaderHook_OnFencingWrite_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnFencingWrite("key1", []byte("value1"), 1)
	assertValidOpID(t, opID)
	assertTotalRecorded(t, hook.Stats(), 1)
}

func TestLeaderHook_OnFencingWriteReturn(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnFencingWrite("key1", []byte("value1"), 1)
	assertValidOpID(t, opID)

	hook.OnFencingWriteReturn(opID, true, "", 1)

	leaderOps := recorder.GetLeaderHAOperations()
	if len(leaderOps) == 0 {
		t.Error("Expected leader HA operations to be recorded")
	}
}

func TestLeaderHook_OnFencingWriteReturn_Error(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnFencingWrite("key1", []byte("value1"), 1)
	// fencing token 过期错误
	hook.OnFencingWriteReturn(opID, false, "stale fencing token", 1)

	leaderOps := recorder.GetLeaderHAOperations()
	if len(leaderOps) == 0 {
		t.Error("Expected leader HA operations to be recorded")
	}
}

func TestLeaderHook_OnFencingWriteReturn_InvalidOpID(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	// 不存在的 opID 和负数 opID 应该不会 panic
	hook.OnFencingWriteReturn(9999, true, "", 1)
	hook.OnFencingWriteReturn(-1, true, "", 1)
}

// ==================== 生命周期测试 ====================

func TestLeaderHook_Flush(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", asyncTestConfig())
	hook.SetEnabled(true)
	hook.Start()
	defer hook.Stop()

	_ = hook.OnLeaderChange("node-1", "node-2", 2)
	_ = hook.OnFencingWrite("key1", []byte("value1"), 1)
	waitForAsync()

	hook.Flush()
	hook.Flush()
}

func TestLeaderHook_StartStop(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", asyncTestConfig())

	hook.Start()
	hook.SetEnabled(true)
	_ = hook.OnLeaderChange("node-1", "node-2", 2)
	hook.Stop()
	hook.Stop()
}

func TestLeaderHook_Stats(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	_ = hook.OnLeaderChange("node-1", "node-2", 2)
	_ = hook.OnFencingWrite("key1", []byte("value1"), 1)
	_ = hook.OnFencingWrite("key2", []byte("value2"), 1)

	assertTotalRecorded(t, hook.Stats(), 3)
}

// ==================== 场景测试 ====================

func TestLeaderHook_LeaderChangeSequence(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	// 模拟 Leader 变更序列
	// 1. node-1 成为 Leader
	opID1 := hook.OnLeaderChange("", "node-1", 1)
	hook.OnLeaderChangeReturn(opID1, true, "", "node-1", 1)

	// 2. node-2 成为新 Leader（term 2）
	opID2 := hook.OnLeaderChange("node-1", "node-2", 2)
	hook.OnLeaderChangeReturn(opID2, true, "", "node-2", 2)

	leaderOps := recorder.GetLeaderHAOperations()
	if len(leaderOps) != 2 {
		t.Errorf("Expected 2 leader HA operations, got %d", len(leaderOps))
	}
}

func TestLeaderHook_FencingWriteSequence(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewLeaderHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	// 模拟 Fencing Token 写入序列
	// 1. term=1 的写入
	opID1 := hook.OnFencingWrite("key1", []byte("value1"), 1)
	hook.OnFencingWriteReturn(opID1, true, "", 1)

	// 2. term=2 的写入（新 Leader）
	opID2 := hook.OnFencingWrite("key1", []byte("value2"), 2)
	hook.OnFencingWriteReturn(opID2, true, "", 2)

	leaderOps := recorder.GetLeaderHAOperations()
	if len(leaderOps) != 2 {
		t.Errorf("Expected 2 leader HA operations, got %d", len(leaderOps))
	}
}
