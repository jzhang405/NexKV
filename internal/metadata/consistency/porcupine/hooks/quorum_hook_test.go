// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件测试 Quorum Hook
package hooks

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

func TestNewQuorumHook(t *testing.T) {
	recorder := newTestRecorder()
	config := porcupine.DefaultAsyncRecordConfig()

	hook := NewQuorumHook(recorder, "node-1", config)
	assertHookCreated(t, hook, "QuorumHook")

	if hook.Recorder() != recorder {
		t.Error("Recorder mismatch")
	}
}

func TestQuorumHook_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", syncTestConfig())
	assertEnabledState(t, hook)
}

func TestQuorumHook_OnQuorumWrite_Disabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", syncTestConfig())
	// 不启用 hook

	opID := hook.OnQuorumWrite("key1", []byte("value1"), []string{"node-1", "node-2"})
	assertDisabledHookReturnsMinus1(t, opID)
	assertTotalRecorded(t, hook.Stats(), 0)
}

func TestQuorumHook_OnQuorumWrite_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	participants := []string{"node-1", "node-2", "node-3"}
	opID := hook.OnQuorumWrite("key1", []byte("value1"), participants)
	assertValidOpID(t, opID)
	assertTotalRecorded(t, hook.Stats(), 1)
}

func TestQuorumHook_OnQuorumReturn(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	participants := []string{"node-1", "node-2"}
	opID := hook.OnQuorumWrite("key1", []byte("value1"), participants)
	assertValidOpID(t, opID)

	hook.OnQuorumReturn(opID, true, "")

	topologyOps := recorder.GetTopologyOperations()
	if len(topologyOps) == 0 {
		t.Error("Expected topology operations to be recorded")
	}
}

func TestQuorumHook_OnQuorumReturn_Error(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnQuorumWrite("key1", []byte("value1"), []string{"node-1"})
	hook.OnQuorumReturn(opID, false, "quorum not reached")

	topologyOps := recorder.GetTopologyOperations()
	if len(topologyOps) == 0 {
		t.Error("Expected topology operations to be recorded")
	}
}

func TestQuorumHook_OnQuorumReturn_InvalidOpID(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	// 不存在的 opID 和负数 opID 应该不会 panic
	hook.OnQuorumReturn(9999, true, "")
	hook.OnQuorumReturn(-1, true, "")
}

func TestQuorumHook_Flush(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", asyncTestConfig())
	hook.SetEnabled(true)
	hook.Start()
	defer hook.Stop()

	_ = hook.OnQuorumWrite("key1", []byte("value1"), []string{"node-1"})
	waitForAsync()

	hook.Flush()
	hook.Flush()
}

func TestQuorumHook_StartStop(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", asyncTestConfig())

	hook.Start()
	hook.SetEnabled(true)
	_ = hook.OnQuorumWrite("key1", []byte("value1"), []string{"node-1"})
	hook.Stop()
	hook.Stop()
}

func TestQuorumHook_Stats(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewQuorumHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	_ = hook.OnQuorumWrite("key1", []byte("value1"), []string{"node-1"})
	_ = hook.OnQuorumWrite("key2", []byte("value2"), []string{"node-1", "node-2"})

	assertTotalRecorded(t, hook.Stats(), 2)
}
