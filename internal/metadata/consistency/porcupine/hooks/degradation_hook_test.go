// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件测试 Degradation Hook
package hooks

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

func TestNewDegradationHook(t *testing.T) {
	recorder := newTestRecorder()
	config := porcupine.DefaultAsyncRecordConfig()

	hook := NewDegradationHook(recorder, "node-1", config)
	assertHookCreated(t, hook, "DegradationHook")

	if hook.Recorder() != recorder {
		t.Error("Recorder mismatch")
	}
}

func TestDegradationHook_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", syncTestConfig())
	assertEnabledState(t, hook)
}

func TestDegradationHook_OnDegradedWrite_Disabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", syncTestConfig())
	// 不启用 hook

	opID := hook.OnDegradedWrite("key1", []byte("value1"))
	assertDisabledHookReturnsMinus1(t, opID)
	assertTotalRecorded(t, hook.Stats(), 0)
}

func TestDegradationHook_OnDegradedWrite_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnDegradedWrite("key1", []byte("value1"))
	assertValidOpID(t, opID)
	assertTotalRecorded(t, hook.Stats(), 1)
}

func TestDegradationHook_OnDegradedReturn(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnDegradedWrite("key1", []byte("value1"))
	assertValidOpID(t, opID)

	// 非降级模式 return
	hook.OnDegradedReturn(opID, true, false)

	failureOps := recorder.GetFailureRecoveryOperations()
	if len(failureOps) == 0 {
		t.Error("Expected failure recovery operations to be recorded")
	}
}

func TestDegradationHook_OnDegradedReturn_Degraded(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnDegradedWrite("key1", []byte("value1"))

	// 降级模式 return
	hook.OnDegradedReturn(opID, true, true)

	failureOps := recorder.GetFailureRecoveryOperations()
	if len(failureOps) == 0 {
		t.Error("Expected failure recovery operations to be recorded")
	}
}

func TestDegradationHook_OnDegradedReturn_InvalidOpID(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	// 不存在的 opID 和负数 opID 应该不会 panic
	hook.OnDegradedReturn(9999, true, false)
	hook.OnDegradedReturn(-1, true, false)
}

func TestDegradationHook_Flush(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", asyncTestConfig())
	hook.SetEnabled(true)
	hook.Start()
	defer hook.Stop()

	_ = hook.OnDegradedWrite("key1", []byte("value1"))
	waitForAsync()

	hook.Flush()
	hook.Flush()
}

func TestDegradationHook_StartStop(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", asyncTestConfig())

	hook.Start()
	hook.SetEnabled(true)
	_ = hook.OnDegradedWrite("key1", []byte("value1"))
	hook.Stop()
	hook.Stop()
}

func TestDegradationHook_Stats(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewDegradationHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	_ = hook.OnDegradedWrite("key1", []byte("value1"))
	_ = hook.OnDegradedWrite("key2", []byte("value2"))

	assertTotalRecorded(t, hook.Stats(), 2)
}
