package service

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ============================================================================
// 测试辅助函数
// ============================================================================

// newTestMessage 创建测试消息
func newTestMessage(id, source, payload string) model.Message {
	return model.NewMessage(id, model.MessageType(1), model.PeerID(source), "local", []byte(payload))
}

// ============================================================================
// BroadcastProgress 基础测试
// ============================================================================

// TestBroadcastProgress_WaitMajority 测试 WaitMajority 方法
func TestBroadcastProgress_WaitMajority(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastProgress("test-task", peers)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 异步记录响应
	go func() {
		time.Sleep(50 * time.Millisecond)
		tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
		time.Sleep(50 * time.Millisecond)
		tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))
	}()

	// 等待多数派
	err := tracker.WaitMajority(ctx)
	if err != nil {
		t.Errorf("WaitMajority failed: %v", err)
	}

	if !tracker.IsMajorityReached() {
		t.Error("Majority should be reached")
	}
}

// TestBroadcastProgress_WaitFull 测试 WaitFull 方法
func TestBroadcastProgress_WaitFull(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	tracker := NewBroadcastProgress("test-task", peers)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 异步记录响应
	go func() {
		time.Sleep(50 * time.Millisecond)
		tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
		time.Sleep(50 * time.Millisecond)
		tracker.RecordFailure("node-2", context.DeadlineExceeded)
	}()

	// 等待全部完成
	err := tracker.WaitFull(ctx)
	if err != nil {
		t.Errorf("WaitFull failed: %v", err)
	}

	if !tracker.IsFullDone() {
		t.Error("Tracker should be full done")
	}
}

// TestBroadcastProgress_Stats 测试 Stats 方法
func TestBroadcastProgress_Stats(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastProgress("test-task", peers)

	// 记录响应
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	tracker.RecordFailure("node-2", context.DeadlineExceeded)

	// 获取统计信息
	success, failed, pending := tracker.Stats()

	// 验证统计信息
	if success != 1 {
		t.Errorf("Expected Success=1, got %d", success)
	}
	if failed != 1 {
		t.Errorf("Expected Failed=1, got %d", failed)
	}
	if pending != 1 {
		t.Errorf("Expected Pending=1, got %d", pending)
	}
}

// TestBroadcastProgress_IsFullDone_NotClosed 测试 IsFullDone 未关闭分支
func TestBroadcastProgress_IsFullDone_NotClosed(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastProgress("test-task", peers)

	// 未记录任何响应时，IsFullDone 应返回 false
	if tracker.IsFullDone() {
		t.Error("IsFullDone should return false when not all responses received")
	}

	// 记录部分响应
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))

	if tracker.IsFullDone() {
		t.Error("IsFullDone should return false when only partial responses received")
	}
}

// TestBroadcastProgress_WaitMajority_AlreadyReached 测试已达到多数派的快速路径
func TestBroadcastProgress_WaitMajority_AlreadyReached(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastProgress("test-task", peers)

	// 先记录足够的成功响应
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 调用 WaitMajority 应立即返回（快速路径）
	start := time.Now()
	err := tracker.WaitMajority(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("WaitMajority should succeed immediately, got error: %v", err)
	}

	// 验证快速路径：应该几乎立即返回（< 10ms）
	if elapsed > 10*time.Millisecond {
		t.Errorf("WaitMajority took too long for fast path: %v", elapsed)
	}
}

// TestBroadcastProgress_WaitMajority_EmptyPeers 测试空节点列表
func TestBroadcastProgress_WaitMajority_EmptyPeers(t *testing.T) {
	peers := []model.PeerID{} // 空列表
	tracker := NewBroadcastProgress("test-task", peers)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 调用 WaitMajority 应立即返回（空列表的特殊情况）
	err := tracker.WaitMajority(ctx)
	if err != nil {
		t.Errorf("WaitMajority should succeed for empty peers, got error: %v", err)
	}
}

// TestBroadcastProgress_WaitFull_Timeout 测试 WaitFull 超时
func TestBroadcastProgress_WaitFull_Timeout(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	tracker := NewBroadcastProgress("test-task", peers)

	// 使用已经过期的 context
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	cancel() // 立即取消

	// 调用 WaitFull 应立即返回 context 错误
	err := tracker.WaitFull(ctx)
	if err == nil {
		t.Error("WaitFull should fail with canceled context")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got: %v", err)
	}
}
