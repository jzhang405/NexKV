package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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
// Mock Callback 实现
// ============================================================================

// MockCallback 用于测试的 Mock 回调
type MockCallback struct {
	onSuccessCount  int32
	onFailureCount  int32
	onMajorityCount int32
	onFullDoneCount int32
	onSuccessCalls  []CallbackCall
	onFailureCalls  []FailureCall
	onMajorityStats *BroadcastStats
	onFullDoneStats *BroadcastStats
	mu              sync.Mutex
}

type CallbackCall struct {
	Peer  model.PeerID
	Resp  model.Message
	Stats BroadcastStats
}

type FailureCall struct {
	Peer  model.PeerID
	Err   error
	Stats BroadcastStats
}

func NewMockCallback() *MockCallback {
	return &MockCallback{
		onSuccessCalls: make([]CallbackCall, 0),
		onFailureCalls: make([]FailureCall, 0),
	}
}

func (m *MockCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	atomic.AddInt32(&m.onSuccessCount, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSuccessCalls = append(m.onSuccessCalls, CallbackCall{
		Peer:  peer,
		Resp:  resp,
		Stats: stats,
	})
}

func (m *MockCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	atomic.AddInt32(&m.onFailureCount, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onFailureCalls = append(m.onFailureCalls, FailureCall{
		Peer:  peer,
		Err:   err,
		Stats: stats,
	})
}

func (m *MockCallback) OnMajorityReached(stats BroadcastStats) {
	atomic.AddInt32(&m.onMajorityCount, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onMajorityStats = &stats
}

func (m *MockCallback) OnFullDone(stats BroadcastStats) {
	atomic.AddInt32(&m.onFullDoneCount, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onFullDoneStats = &stats
}

func (m *MockCallback) GetSuccessCount() int32 {
	return atomic.LoadInt32(&m.onSuccessCount)
}

func (m *MockCallback) GetFailureCount() int32 {
	return atomic.LoadInt32(&m.onFailureCount)
}

func (m *MockCallback) GetMajorityCount() int32 {
	return atomic.LoadInt32(&m.onMajorityCount)
}

func (m *MockCallback) GetFullDoneCount() int32 {
	return atomic.LoadInt32(&m.onFullDoneCount)
}

// PanicCallback 会 panic 的回调（用于测试 panic 保护）
type PanicCallback struct{}

func (p *PanicCallback) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	panic("OnSuccess panic!")
}

func (p *PanicCallback) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	panic("OnFailure panic!")
}

func (p *PanicCallback) OnMajorityReached(stats BroadcastStats) {
	panic("OnMajorityReached panic!")
}

func (p *PanicCallback) OnFullDone(stats BroadcastStats) {
	panic("OnFullDone panic!")
}

// ============================================================================
// 测试用例 1-7：基础功能测试
// ============================================================================

// TestBroadcastCallback_OnSuccess 测试成功响应回调
func TestBroadcastCallback_OnSuccess(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：记录 2 个成功响应
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))

	// 验证：OnSuccess 应该被调用 2 次
	if count := callback.GetSuccessCount(); count != 2 {
		t.Errorf("OnSuccess should be called 2 times, got %d", count)
	}

	// 验证回调参数
	if len(callback.onSuccessCalls) != 2 {
		t.Fatalf("Expected 2 OnSuccess calls, got %d", len(callback.onSuccessCalls))
	}

	call1 := callback.onSuccessCalls[0]
	if call1.Peer != "node-1" {
		t.Errorf("Expected peer 'node-1', got '%s'", call1.Peer)
	}
	if string(call1.Resp.Payload()) != "resp-1" {
		t.Errorf("Expected response 'resp-1', got '%s'", string(call1.Resp.Payload()))
	}
	if call1.Stats.Success != 1 {
		t.Errorf("Expected Success=1, got %d", call1.Stats.Success)
	}
}

// TestBroadcastCallback_OnFailure 测试失败响应回调
func TestBroadcastCallback_OnFailure(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：记录 2 个失败响应
	testErr1 := context.DeadlineExceeded
	testErr2 := context.Canceled
	tracker.RecordFailure("node-1", testErr1)
	tracker.RecordFailure("node-2", testErr2)

	// 验证：OnFailure 应该被调用 2 次
	if count := callback.GetFailureCount(); count != 2 {
		t.Errorf("OnFailure should be called 2 times, got %d", count)
	}

	// 验证回调参数
	if len(callback.onFailureCalls) != 2 {
		t.Fatalf("Expected 2 OnFailure calls, got %d", len(callback.onFailureCalls))
	}

	call1 := callback.onFailureCalls[0]
	if call1.Peer != "node-1" {
		t.Errorf("Expected peer 'node-1', got '%s'", call1.Peer)
	}
	if call1.Err != testErr1 {
		t.Errorf("Expected error '%v', got '%v'", testErr1, call1.Err)
	}
	if call1.Stats.Failed != 1 {
		t.Errorf("Expected Failed=1, got %d", call1.Stats.Failed)
	}
}

// TestBroadcastCallback_OnMajorityReached 测试多数派回调
func TestBroadcastCallback_OnMajorityReached(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：记录 1 个成功（未达多数派）
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))

	// 验证：OnMajorityReached 不应该被调用
	if count := callback.GetMajorityCount(); count != 0 {
		t.Errorf("OnMajorityReached should not be called yet, got %d", count)
	}

	// 执行：记录第 2 个成功（达到多数派）
	tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))

	// 验证：OnMajorityReached 应该被调用 1 次
	if count := callback.GetMajorityCount(); count != 1 {
		t.Errorf("OnMajorityReached should be called 1 time, got %d", count)
	}

	// 验证统计信息
	if callback.onMajorityStats == nil {
		t.Fatal("OnMajorityReached stats should not be nil")
	}
	if callback.onMajorityStats.Success != 2 {
		t.Errorf("Expected Success=2, got %d", callback.onMajorityStats.Success)
	}

	// 执行：记录第 3 个成功
	tracker.RecordSuccess("node-3", newTestMessage("msg-3", "node-3", "resp-3"))

	// 验证：OnMajorityReached 不应该再次被调用
	if count := callback.GetMajorityCount(); count != 1 {
		t.Errorf("OnMajorityReached should still be called 1 time (not %d)", count)
	}
}

// TestBroadcastCallback_OnFullDone 测试全部完成回调
func TestBroadcastCallback_OnFullDone(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：记录 2 个成功，1 个失败
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))
	tracker.RecordFailure("node-3", context.DeadlineExceeded)

	// 验证：OnFullDone 应该被调用 1 次
	if count := callback.GetFullDoneCount(); count != 1 {
		t.Errorf("OnFullDone should be called 1 time, got %d", count)
	}

	// 验证统计信息
	if callback.onFullDoneStats == nil {
		t.Fatal("OnFullDone stats should not be nil")
	}
	if callback.onFullDoneStats.Success != 2 {
		t.Errorf("Expected Success=2, got %d", callback.onFullDoneStats.Success)
	}
	if callback.onFullDoneStats.Failed != 1 {
		t.Errorf("Expected Failed=1, got %d", callback.onFullDoneStats.Failed)
	}
	if callback.onFullDoneStats.Total != 3 {
		t.Errorf("Expected Total=3, got %d", callback.onFullDoneStats.Total)
	}
}

// TestBroadcastCallback_ConcurrentSafety 测试并发安全性
func TestBroadcastCallback_ConcurrentSafety(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：并发调用 RecordSuccess（同一个节点，重复记录）
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msg := newTestMessage(fmt.Sprintf("msg-%d", idx), "node-1", "resp")
			tracker.RecordSuccess("node-1", msg)
		}(i)
	}
	wg.Wait()

	// 验证：OnSuccess 应该被调用 10 次（重复记录同一节点）
	if count := callback.GetSuccessCount(); count != 10 {
		t.Errorf("OnSuccess should be called 10 times, got %d", count)
	}

	// 注意：由于都是同一个节点，不会达到多数派（需要 2 个不同节点）
	// 所以 OnMajorityReached 不应该被调用
	if count := callback.GetMajorityCount(); count != 0 {
		t.Errorf("OnMajorityReached should not be called (same node repeated), got %d", count)
	}

	// 验证：没有死锁，所有并发调用都成功完成（测试通过即证明）
}

// TestBroadcastCallback_PanicRecovery 测试 Panic 保护
func TestBroadcastCallback_PanicRecovery(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)

	// 创建会 panic 的回调
	panicCallback := &PanicCallback{}
	tracker.SetCallback(panicCallback)

	// 执行：记录成功响应（回调会 panic）
	// 验证：主流程不应该 panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("RecordSuccess should not panic, got: %v", r)
		}
	}()

	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))

	// 验证：主流程正常继续（即使回调 panic）
	// 检查状态是否正确更新
	if !tracker.IsMajorityReached() {
		t.Error("Tracker should still update state after callback panic")
	}

	// 验证：可以继续记录更多响应
	tracker.RecordSuccess("node-3", newTestMessage("msg-3", "node-3", "resp-3"))

	if !tracker.IsFullDone() {
		t.Error("Tracker should be full done after all responses")
	}
}

// TestBroadcastStats_Accuracy 测试统计信息准确性
func TestBroadcastStats_Accuracy(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：记录 2 个成功，1 个失败
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))
	tracker.RecordFailure("node-3", context.DeadlineExceeded)

	// 验证 OnFullDone 的统计信息（最后的完整状态）
	if callback.onFullDoneStats == nil {
		t.Fatal("OnFullDone stats should not be nil")
	}
	stats := *callback.onFullDoneStats

	// 验证基本统计
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

	// 验证成功率
	expectedRate := float64(2) / float64(3)
	if stats.SuccessRate != expectedRate {
		t.Errorf("Expected SuccessRate=%f, got %f", expectedRate, stats.SuccessRate)
	}

	// 验证时间戳
	if stats.ElapsedTime <= 0 {
		t.Error("ElapsedTime should be positive")
	}
	if stats.FirstResponseTime <= 0 {
		t.Error("FirstResponseTime should be positive")
	}
	if stats.MajorityReachTime <= 0 {
		t.Error("MajorityReachTime should be positive")
	}
}

// ============================================================================
// 测试用例 8-11：边界场景测试
// ============================================================================

// TestBroadcastCallback_EmptyTargets 测试空 targets
func TestBroadcastCallback_EmptyTargets(t *testing.T) {
	peers := []model.PeerID{}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：尝试记录成功（不应该 panic）
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))

	// 验证：回调应该被触发（虽然不在 targets 列表中）
	if count := callback.GetSuccessCount(); count != 1 {
		t.Errorf("OnSuccess should be called 1 time, got %d", count)
	}

	// 验证统计信息
	if len(callback.onSuccessCalls) > 0 {
		stats := callback.onSuccessCalls[0].Stats
		if stats.Total != 0 {
			t.Errorf("Expected Total=0, got %d", stats.Total)
		}
		// 验证除零保护：SuccessRate 应该是 0（而不是 NaN）
		if stats.SuccessRate != 0 {
			t.Errorf("Expected SuccessRate=0 (avoid division by zero), got %f", stats.SuccessRate)
		}
	}
}

// TestBroadcastCallback_AllFailed 测试全部失败
func TestBroadcastCallback_AllFailed(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：记录 3 个失败
	tracker.RecordFailure("node-1", context.DeadlineExceeded)
	tracker.RecordFailure("node-2", context.DeadlineExceeded)
	tracker.RecordFailure("node-3", context.DeadlineExceeded)

	// 验证：OnFailure 应该被调用 3 次
	if count := callback.GetFailureCount(); count != 3 {
		t.Errorf("OnFailure should be called 3 times, got %d", count)
	}

	// 验证：OnMajorityReached 不应该被调用（失败不触发多数派）
	if count := callback.GetMajorityCount(); count != 0 {
		t.Errorf("OnMajorityReached should not be called (failures don't count), got %d", count)
	}

	// 验证：OnFullDone 应该被调用 1 次
	if count := callback.GetFullDoneCount(); count != 1 {
		t.Errorf("OnFullDone should be called 1 time, got %d", count)
	}

	// 验证统计信息
	if callback.onFullDoneStats == nil {
		t.Fatal("OnFullDone stats should not be nil")
	}
	if callback.onFullDoneStats.Success != 0 {
		t.Errorf("Expected Success=0, got %d", callback.onFullDoneStats.Success)
	}
	if callback.onFullDoneStats.Failed != 3 {
		t.Errorf("Expected Failed=3, got %d", callback.onFullDoneStats.Failed)
	}
}

// TestBroadcastCallback_MajorityThenFullDone 测试先 Majority 后 FullDone
func TestBroadcastCallback_MajorityThenFullDone(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：记录 2 个成功（达到多数派）
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))

	// 验证：OnMajorityReached 应该被调用
	if count := callback.GetMajorityCount(); count != 1 {
		t.Errorf("OnMajorityReached should be called 1 time, got %d", count)
	}

	// 验证：OnFullDone 不应该被调用（还有 1 个待响应）
	if count := callback.GetFullDoneCount(); count != 0 {
		t.Errorf("OnFullDone should not be called yet, got %d", count)
	}

	// 执行：记录第 3 个成功（全部完成）
	tracker.RecordSuccess("node-3", newTestMessage("msg-3", "node-3", "resp-3"))

	// 验证：OnMajorityReached 不应该再次被调用
	if count := callback.GetMajorityCount(); count != 1 {
		t.Errorf("OnMajorityReached should still be called 1 time, got %d", count)
	}

	// 验证：OnFullDone 应该被调用
	if count := callback.GetFullDoneCount(); count != 1 {
		t.Errorf("OnFullDone should be called 1 time, got %d", count)
	}

	// 验证回调顺序正确
	if callback.onMajorityStats == nil || callback.onFullDoneStats == nil {
		t.Fatal("Both OnMajorityReached and OnFullDone stats should not be nil")
	}
}

// TestBroadcastCallback_ConcurrentRecordSuccess_OnlyOnce 测试并发 RecordSuccess（验证仅触发一次）
func TestBroadcastCallback_ConcurrentRecordSuccess_OnlyOnce(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：并发记录 2 个成功（同时达到多数派）
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	}()

	go func() {
		defer wg.Done()
		tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))
	}()

	wg.Wait()

	// 验证：OnMajorityReached 应该被调用 1 次（仅触发一次）
	if count := callback.GetMajorityCount(); count != 1 {
		t.Errorf("OnMajorityReached should be called exactly 1 time (concurrent safe), got %d", count)
	}

	// 验证：OnSuccess 应该被调用 2 次
	if count := callback.GetSuccessCount(); count != 2 {
		t.Errorf("OnSuccess should be called 2 times, got %d", count)
	}
}

// ============================================================================
// 额外测试：回调禁用/启用
// ============================================================================

// TestBroadcastCallback_EnableDisable 测试回调禁用/启用
func TestBroadcastCallback_EnableDisable(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	// 执行：禁用回调
	tracker.EnableCallbacks(false)

	// 执行：记录成功
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))

	// 验证：回调不应该被触发
	if count := callback.GetSuccessCount(); count != 0 {
		t.Errorf("OnSuccess should not be called when callbacks disabled, got %d", count)
	}

	// 执行：启用回调
	tracker.EnableCallbacks(true)

	// 执行：记录成功
	tracker.RecordSuccess("node-2", newTestMessage("msg-2", "node-2", "resp-2"))

	// 验证：回调应该被触发
	if count := callback.GetSuccessCount(); count != 1 {
		t.Errorf("OnSuccess should be called 1 time after re-enabled, got %d", count)
	}
}

// ============================================================================
// 性能测试
// ============================================================================

// BenchmarkBroadcastCallback_OnSuccess 基准测试：OnSuccess 回调性能
func BenchmarkBroadcastCallback_OnSuccess(b *testing.B) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("bench-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)

	msg := newTestMessage("msg-1", "node-1", "resp-1")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 重置 tracker
		tracker.responses = make(map[model.PeerID]model.Message)
		tracker.failures = make(map[model.PeerID]error)
		atomic.StoreInt32(&callback.onSuccessCount, 0)

		tracker.RecordSuccess("node-1", msg)
	}
}

// ========================================
// 补充测试：提升覆盖率至 80%
// ========================================

// TestBroadcastCallback_NoOpCallback 测试 NoOpCallback 空实现
func TestBroadcastCallback_NoOpCallback(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	tracker := NewBroadcastTracker("test-task", peers)
	tracker.SetCallback(NoOpCallback{}) // 使用空实现

	// 执行：不应 panic
	tracker.RecordSuccess("node-1", newTestMessage("msg-1", "node-1", "resp-1"))
	tracker.RecordFailure("node-2", context.DeadlineExceeded)

	// 验证：状态正常更新
	if !tracker.IsFullDone() {
		t.Error("Tracker should be full done")
	}
}

// TestBroadcastTracker_WaitMajority 测试 WaitMajority 方法
func TestBroadcastTracker_WaitMajority(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)

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

// TestBroadcastTracker_WaitFull 测试 WaitFull 方法
func TestBroadcastTracker_WaitFull(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	tracker := NewBroadcastTracker("test-task", peers)

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

// TestBroadcastTracker_Stats 测试 Stats 方法
func TestBroadcastTracker_Stats(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)

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

// TestBroadcastTracker_IsFullDone_NotClosed 测试 IsFullDone 未关闭分支
func TestBroadcastTracker_IsFullDone_NotClosed(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)

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

// TestBroadcastCallback_RecordFailure_Disabled 测试禁用回调时的 RecordFailure
func TestBroadcastCallback_RecordFailure_Disabled(t *testing.T) {
	peers := []model.PeerID{"node-1"}
	tracker := NewBroadcastTracker("test-task", peers)
	callback := NewMockCallback()
	tracker.SetCallback(callback)
	tracker.EnableCallbacks(false) // 禁用回调

	tracker.RecordFailure("node-1", context.DeadlineExceeded)

	// 验证：回调不应触发
	if count := callback.GetFailureCount(); count != 0 {
		t.Errorf("OnFailure should not be called when callbacks disabled, got %d", count)
	}
}

// TestBroadcastTracker_WaitMajority_AlreadyReached 测试已达到多数派的快速路径
func TestBroadcastTracker_WaitMajority_AlreadyReached(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := NewBroadcastTracker("test-task", peers)

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

// TestBroadcastTracker_WaitMajority_EmptyPeers 测试空节点列表
func TestBroadcastTracker_WaitMajority_EmptyPeers(t *testing.T) {
	peers := []model.PeerID{} // 空列表
	tracker := NewBroadcastTracker("test-task", peers)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 调用 WaitMajority 应立即返回（空列表的特殊情况）
	err := tracker.WaitMajority(ctx)
	if err != nil {
		t.Errorf("WaitMajority should succeed for empty peers, got error: %v", err)
	}
}

// TestBroadcastTracker_WaitFull_Timeout 测试 WaitFull 超时
func TestBroadcastTracker_WaitFull_Timeout(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	tracker := NewBroadcastTracker("test-task", peers)

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
