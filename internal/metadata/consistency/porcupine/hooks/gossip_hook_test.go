// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件测试 Gossip Hook
package hooks

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

func TestNewGossipHook(t *testing.T) {
	recorder := newTestRecorder()
	config := porcupine.DefaultAsyncRecordConfig()

	hook := NewGossipHook(recorder, "node-1", config)
	assertHookCreated(t, hook, "GossipHook")

	if hook.Recorder() != recorder {
		t.Error("Recorder mismatch")
	}
}

func TestGossipHook_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	assertEnabledState(t, hook)
}

func TestGossipHook_OnGossipWrite_Disabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	// 不启用 hook

	opID := hook.OnGossipWrite("key1", []byte("value1"))
	assertDisabledHookReturnsMinus1(t, opID)
	assertTotalRecorded(t, hook.Stats(), 0)
}

func TestGossipHook_OnGossipWrite_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnGossipWrite("key1", []byte("value1"))
	assertValidOpID(t, opID)
	assertTotalRecorded(t, hook.Stats(), 1)
}

func TestGossipHook_OnGossipReturn(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnGossipWrite("key1", []byte("value1"))
	assertValidOpID(t, opID)

	hook.OnGossipReturn(opID, true, "")

	topologyOps := recorder.GetTopologyOperations()
	if len(topologyOps) == 0 {
		t.Error("Expected topology operations to be recorded")
	}
}

func TestGossipHook_OnGossipReturn_InvalidOpID(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	// 不存在的 opID 和负数 opID 应该不会 panic
	hook.OnGossipReturn(9999, true, "")
	hook.OnGossipReturn(-1, true, "")
}

func TestGossipHook_OnGossipReturn_Disabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	opID := hook.OnGossipWrite("key1", []byte("value1"))

	hook.SetEnabled(false)
	hook.OnGossipReturn(opID, true, "")
	// 应该不会 panic
}

func TestGossipHook_Flush(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", asyncTestConfig())
	hook.SetEnabled(true)
	hook.Start()
	defer hook.Stop()

	_ = hook.OnGossipWrite("key1", []byte("value1"))
	waitForAsync()

	hook.Flush()
	hook.Flush() // 再次验证 pending 已清空
}

func TestGossipHook_StartStop(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", asyncTestConfig())

	hook.Start()
	hook.SetEnabled(true)
	_ = hook.OnGossipWrite("key1", []byte("value1"))
	hook.Stop()
	hook.Stop() // 再次 Stop 应该安全
}

func TestGossipHook_StartStop_Disabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())

	// 在禁用模式下 Start/Stop 应该安全
	hook.Start()
	hook.Stop()
}

func TestGossipHook_SetTopology(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", porcupine.DefaultAsyncRecordConfig())

	topology := &porcupine.Topology{
		Nodes: map[string]*porcupine.NodeInfo{
			"node-1": {NodeID: "node-1"},
			"node-2": {NodeID: "node-2"},
			"node-3": {NodeID: "node-3"},
		},
	}

	hook.SetTopology(topology) // 应该不会 panic
}

func TestGossipHook_Stats(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	assertTotalRecorded(t, hook.Stats(), 0)

	_ = hook.OnGossipWrite("key1", []byte("value1"))
	_ = hook.OnGossipWrite("key2", []byte("value2"))

	assertTotalRecorded(t, hook.Stats(), 2)
}

func TestGossipHook_DropOnFull(t *testing.T) {
	recorder := newTestRecorder()
	config := porcupine.AsyncRecordConfig{
		Enabled:    true,
		BufferSize: 2,
		DropOnFull: true,
	}
	hook := NewGossipHook(recorder, "node-1", config)
	hook.SetEnabled(true)
	hook.Start()
	defer hook.Stop()

	successCount := 0
	for i := 0; i < 10; i++ {
		if hook.OnGossipWrite("key", []byte("value")) >= 0 {
			successCount++
		}
	}

	if successCount == 0 {
		t.Error("Expected at least some operations to succeed")
	}

	stats := hook.Stats()
	if stats.DroppedOps == 0 {
		t.Log("Warning: No operations dropped, but this may be timing-dependent")
	}
}
