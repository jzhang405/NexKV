// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件测试 Failure Hook
package hooks

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

func TestNewFailureHook(t *testing.T) {
	recorder := newTestRecorder()
	config := porcupine.DefaultAsyncRecordConfig()

	hook := NewFailureHook(recorder, config)
	assertHookCreated(t, hook, "FailureHook")

	if hook.Recorder() != recorder {
		t.Error("Recorder mismatch")
	}
}

func TestFailureHook_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	assertEnabledState(t, hook)
}

func TestFailureHook_OnNodeFailure_Disabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	// 不启用 hook

	opID := hook.OnNodeFailure("node-2")
	assertDisabledHookReturnsMinus1(t, opID)
	assertTotalRecorded(t, hook.Stats(), 0)
}

func TestFailureHook_OnNodeFailure_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnNodeFailure("node-2")
	assertValidOpID(t, opID)
	assertTotalRecorded(t, hook.Stats(), 1)
}

func TestFailureHook_OnNodeRecovery(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnNodeRecovery("node-2")
	assertValidOpID(t, opID)
	assertTotalRecorded(t, hook.Stats(), 1)
}

func TestFailureHook_OnFailureReturn(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnNodeFailure("node-2")
	assertValidOpID(t, opID)

	hook.OnFailureReturn(opID, true, "")

	failureOps := recorder.GetFailureRecoveryOperations()
	if len(failureOps) == 0 {
		t.Error("Expected failure recovery operations to be recorded")
	}
}

func TestFailureHook_OnFailureReturn_Error(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnNodeRecovery("node-2")
	hook.OnFailureReturn(opID, false, "node still unreachable")

	failureOps := recorder.GetFailureRecoveryOperations()
	if len(failureOps) == 0 {
		t.Error("Expected failure recovery operations to be recorded")
	}
}

func TestFailureHook_OnFailureReturn_InvalidOpID(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	hook.SetEnabled(true)

	// 不存在的 opID 和负数 opID 应该不会 panic
	hook.OnFailureReturn(9999, true, "")
	hook.OnFailureReturn(-1, true, "")
}

func TestFailureHook_Flush(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, asyncTestConfig())
	hook.SetEnabled(true)
	hook.Start()
	defer hook.Stop()

	_ = hook.OnNodeFailure("node-2")
	waitForAsync()

	hook.Flush()
	hook.Flush()
}

func TestFailureHook_StartStop(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, asyncTestConfig())

	hook.Start()
	hook.SetEnabled(true)
	_ = hook.OnNodeFailure("node-2")
	hook.Stop()
	hook.Stop()
}

func TestFailureHook_Stats(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	hook.SetEnabled(true)

	_ = hook.OnNodeFailure("node-2")
	_ = hook.OnNodeRecovery("node-2")
	_ = hook.OnNodeFailure("node-3")

	assertTotalRecorded(t, hook.Stats(), 3)
}

func TestFailureHook_FailureRecoverySequence(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewFailureHook(recorder, syncTestConfig())
	hook.SetEnabled(true)

	// 模拟完整的故障-恢复序列
	failID := hook.OnNodeFailure("node-2")
	hook.OnFailureReturn(failID, true, "")

	recoverID := hook.OnNodeRecovery("node-2")
	hook.OnFailureReturn(recoverID, true, "")

	failureOps := recorder.GetFailureRecoveryOperations()
	if len(failureOps) != 2 {
		t.Errorf("Expected 2 failure recovery operations, got %d", len(failureOps))
	}
}
