package service

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ============================================================================
// BroadcastStats 值对象测试
// ============================================================================

// TestBroadcastStats_Fields 测试 BroadcastStats 字段
func TestBroadcastStats_Fields(t *testing.T) {
	stats := BroadcastStats{
		TaskID:            "test-task-001",
		Total:             3,
		Success:           2,
		Failed:            1,
		Pending:           0,
		SuccessRate:       0.666,
		ElapsedTime:       100 * time.Millisecond,
		FirstResponseTime: 20 * time.Millisecond,
		MajorityReachTime: 50 * time.Millisecond,
	}

	// 验证字段值
	if stats.TaskID != "test-task-001" {
		t.Errorf("Expected TaskID=test-task-001, got %s", stats.TaskID)
	}
	if stats.Total != 3 {
		t.Errorf("Expected Total=3, got %d", stats.Total)
	}
	if stats.Success != 2 {
		t.Errorf("Expected Success=2, got %d", stats.Success)
	}
	if stats.Failed != 1 {
		t.Errorf("Expected Failed=1, got %d", stats.Failed)
	}
	if stats.Pending != 0 {
		t.Errorf("Expected Pending=0, got %d", stats.Pending)
	}
	if stats.SuccessRate != 0.666 {
		t.Errorf("Expected SuccessRate=0.666, got %f", stats.SuccessRate)
	}
	if stats.ElapsedTime != 100*time.Millisecond {
		t.Errorf("Expected ElapsedTime=100ms, got %v", stats.ElapsedTime)
	}
	if stats.FirstResponseTime != 20*time.Millisecond {
		t.Errorf("Expected FirstResponseTime=20ms, got %v", stats.FirstResponseTime)
	}
	if stats.MajorityReachTime != 50*time.Millisecond {
		t.Errorf("Expected MajorityReachTime=50ms, got %v", stats.MajorityReachTime)
	}
}

// TestBroadcastStats_ZeroValue 测试 BroadcastStats 零值
func TestBroadcastStats_ZeroValue(t *testing.T) {
	var stats BroadcastStats

	// 验证零值
	if stats.TaskID != "" {
		t.Errorf("Expected empty TaskID, got %s", stats.TaskID)
	}
	if stats.Total != 0 {
		t.Errorf("Expected Total=0, got %d", stats.Total)
	}
	if stats.Success != 0 {
		t.Errorf("Expected Success=0, got %d", stats.Success)
	}
	if stats.Failed != 0 {
		t.Errorf("Expected Failed=0, got %d", stats.Failed)
	}
	if stats.Pending != 0 {
		t.Errorf("Expected Pending=0, got %d", stats.Pending)
	}
	if stats.SuccessRate != 0 {
		t.Errorf("Expected SuccessRate=0, got %f", stats.SuccessRate)
	}
	if stats.ElapsedTime != 0 {
		t.Errorf("Expected ElapsedTime=0, got %v", stats.ElapsedTime)
	}
}

// ============================================================================
// NoOpListener 空实现测试
// ============================================================================

// TestNoOpListener_OnSuccess 测试 OnSuccess 空实现
func TestNoOpListener_OnSuccess(t *testing.T) {
	listener := NoOpListener{}
	msg := model.NewMessage("msg-1", model.MessageType(1), "node-1", "local", []byte("test"))
	stats := BroadcastStats{TaskID: "test"}

	// 调用应不 panic 且正常返回
	listener.OnSuccess("node-1", msg, stats)
}

// TestNoOpListener_OnFailure 测试 OnFailure 空实现
func TestNoOpListener_OnFailure(t *testing.T) {
	listener := NoOpListener{}
	stats := BroadcastStats{TaskID: "test"}

	// 调用应不 panic 且正常返回
	listener.OnFailure("node-1", context.DeadlineExceeded, stats)
}

// TestNoOpListener_OnMajorityReached 测试 OnMajorityReached 空实现
func TestNoOpListener_OnMajorityReached(t *testing.T) {
	listener := NoOpListener{}
	stats := BroadcastStats{TaskID: "test"}

	// 调用应不 panic 且正常返回
	listener.OnMajorityReached(stats)
}

// TestNoOpListener_OnFullDone 测试 OnFullDone 空实现
func TestNoOpListener_OnFullDone(t *testing.T) {
	listener := NoOpListener{}
	stats := BroadcastStats{TaskID: "test"}

	// 调用应不 panic 且正常返回
	listener.OnFullDone(stats)
}

// TestNoOpListener_InterfaceCompliance 测试 NoOpListener 实现 BroadcastListener 接口
func TestNoOpListener_InterfaceCompliance(t *testing.T) {
	// 编译时检查：确保 NoOpListener 实现 BroadcastListener 接口
	var _ BroadcastListener = NoOpListener{}
	var _ BroadcastListener = &NoOpListener{}
}

// ============================================================================
// 自定义 Listener 嵌入 NoOpListener 测试
// ============================================================================

// mockPartialListener 只重写部分方法的监听器（演示 NoOpListener 嵌入用法）
type mockPartialListener struct {
	NoOpListener
	fullDoneCalled bool
}

func (m *mockPartialListener) OnFullDone(stats BroadcastStats) {
	m.fullDoneCalled = true
}

// TestNoOpListener_Embedding 测试嵌入 NoOpListener 只重写部分方法
func TestNoOpListener_Embedding(t *testing.T) {
	listener := &mockPartialListener{}
	msg := model.NewMessage("msg-1", model.MessageType(1), "node-1", "local", []byte("test"))
	stats := BroadcastStats{TaskID: "test"}

	// 调用未重写的方法（使用 NoOpListener 的空实现）
	listener.OnSuccess("node-1", msg, stats)
	listener.OnFailure("node-1", context.DeadlineExceeded, stats)
	listener.OnMajorityReached(stats)

	// 调用重写的方法
	listener.OnFullDone(stats)

	// 验证重写的方法被调用
	if !listener.fullDoneCalled {
		t.Error("OnFullDone should have been called")
	}
}

// TestNoOpListener_Embedding_InterfaceCompliance 测试嵌入结构体仍实现接口
func TestNoOpListener_Embedding_InterfaceCompliance(t *testing.T) {
	// 编译时检查：确保 mockPartialListener 实现 BroadcastListener 接口
	var _ BroadcastListener = &mockPartialListener{}
}
