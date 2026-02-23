package async

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 测试辅助函数
// ==========================================

// mockGroupCallback 模拟批量操作回调
type mockGroupCallback[T any] struct {
	successCount  int64
	failureCount  int64
	majorityCount int64
	fullDoneCount int64
	successValues map[model.PeerID]T
	failureErrors map[model.PeerID]error
	mu            sync.Mutex
}

func newMockGroupCallback[T any]() *mockGroupCallback[T] {
	return &mockGroupCallback[T]{
		successValues: make(map[model.PeerID]T),
		failureErrors: make(map[model.PeerID]error),
	}
}

func (m *mockGroupCallback[T]) OnSuccess(peer model.PeerID, value T, stats GroupStats) {
	atomic.AddInt64(&m.successCount, 1)
	m.mu.Lock()
	m.successValues[peer] = value
	m.mu.Unlock()
}

func (m *mockGroupCallback[T]) OnFailure(peer model.PeerID, err error, stats GroupStats) {
	atomic.AddInt64(&m.failureCount, 1)
	m.mu.Lock()
	m.failureErrors[peer] = err
	m.mu.Unlock()
}

func (m *mockGroupCallback[T]) OnMajorityReached(stats GroupStats) {
	atomic.AddInt64(&m.majorityCount, 1)
}

func (m *mockGroupCallback[T]) OnFullDone(stats GroupStats) {
	atomic.AddInt64(&m.fullDoneCount, 1)
}

// ==========================================
// WaitAny 测试
// ==========================================

// TestNewGroup_WaitAny 测试等待任意一个完成
func TestNewGroup_WaitAny(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		// node-1 最快返回
		if target == "node-1" {
			return "fast", nil
		}
		// 其他节点延迟
		time.Sleep(1 * time.Second)
		return "slow", nil
	})

	peer, value, err := group.WaitAny(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if peer != "node-1" {
		t.Fatalf("expected peer 'node-1', got: %s", peer)
	}
	if value != "fast" {
		t.Fatalf("expected value 'fast', got: %s", value)
	}
}

// TestNewGroup_WaitAny_AllFailed 测试所有任务都失败
func TestNewGroup_WaitAny_AllFailed(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2"}
	expectedErr := errors.New("connection failed")

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return "", expectedErr
	})

	peer, value, err := group.WaitAny(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != expectedErr {
		t.Fatalf("expected error %v, got: %v", expectedErr, err)
	}
	if peer == "" {
		t.Fatal("expected non-empty peer")
	}
	if value != "" {
		t.Fatalf("expected empty value, got: %s", value)
	}
}

// TestNewGroup_WaitAny_Timeout 测试超时
func TestNewGroup_WaitAny_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	targets := []model.PeerID{"node-1", "node-2"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		// 所有任务都延迟
		time.Sleep(1 * time.Second)
		return "slow", nil
	})

	_, _, err := group.WaitAny(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded error, got: %v", err)
	}
}

// ==========================================
// WaitMajority 测试
// ==========================================

// TestNewGroup_WaitMajority 测试等待多数派完成
func TestNewGroup_WaitMajority(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3", "node-4", "node-5"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		// node-1, node-2, node-3 成功
		if target == "node-1" || target == "node-2" || target == "node-3" {
			return fmt.Sprintf("success-%s", target), nil
		}
		// node-4, node-5 失败
		return "", errors.New("failed")
	})

	result := group.WaitMajority(ctx)

	// 应该有 3 个成功（多数派）
	if len(result.SuccessPeers) < 3 {
		t.Fatalf("expected at least 3 success peers, got: %d", len(result.SuccessPeers))
	}

	// 检查成功结果
	if len(result.Values) < 3 {
		t.Fatalf("expected at least 3 values, got: %d", len(result.Values))
	}

	// 检查失败结果
	if len(result.FailedPeers) > 2 {
		t.Fatalf("expected at most 2 failed peers, got: %d", len(result.FailedPeers))
	}
}

// TestNewGroup_WaitMajority_AllSuccess 测试多数派达成
// 注意：WaitMajority 在多数派达成时返回（对于3节点，多数派是2）
// 所以不一定包含所有3个结果
func TestNewGroup_WaitMajority_AllSuccess(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return fmt.Sprintf("success-%s", target), nil
	})

	result := group.WaitMajority(ctx)

	// 多数派达成：至少 2 个成功
	if len(result.SuccessPeers) < 2 {
		t.Fatalf("expected at least 2 success peers, got: %d", len(result.SuccessPeers))
	}

	// 没有失败
	if len(result.FailedPeers) != 0 {
		t.Fatalf("expected 0 failed peers, got: %d", len(result.FailedPeers))
	}
}

// TestNewGroup_WaitMajority_AllFailed 测试全部失败
func TestNewGroup_WaitMajority_AllFailed(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return "", errors.New("failed")
	})

	result := group.WaitMajority(ctx)

	// 全部失败
	if len(result.FailedPeers) != 3 {
		t.Fatalf("expected 3 failed peers, got: %d", len(result.FailedPeers))
	}

	if len(result.SuccessPeers) != 0 {
		t.Fatalf("expected 0 success peers, got: %d", len(result.SuccessPeers))
	}
}

// ==========================================
// WaitAll 测试
// ==========================================

// TestNewGroup_WaitAll 测试等待全部完成
func TestNewGroup_WaitAll(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		// node-1 和 node-2 成功
		if target == "node-1" || target == "node-2" {
			return fmt.Sprintf("success-%s", target), nil
		}
		// node-3 失败
		return "", errors.New("failed")
	})

	result := group.WaitAll(ctx)

	// 检查总数
	if len(result.SuccessPeers)+len(result.FailedPeers) != 3 {
		t.Fatalf("expected 3 total peers, got: %d", len(result.SuccessPeers)+len(result.FailedPeers))
	}

	// 检查成功数
	if len(result.SuccessPeers) != 2 {
		t.Fatalf("expected 2 success peers, got: %d", len(result.SuccessPeers))
	}

	// 检查失败数
	if len(result.FailedPeers) != 1 {
		t.Fatalf("expected 1 failed peer, got: %d", len(result.FailedPeers))
	}
}

// TestNewGroup_WaitAll_AllSuccess 测试全部成功
func TestNewGroup_WaitAll_AllSuccess(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return fmt.Sprintf("success-%s", target), nil
	})

	result := group.WaitAll(ctx)

	if len(result.SuccessPeers) != 3 {
		t.Fatalf("expected 3 success peers, got: %d", len(result.SuccessPeers))
	}

	if len(result.FailedPeers) != 0 {
		t.Fatalf("expected 0 failed peers, got: %d", len(result.FailedPeers))
	}

	// 检查每个结果
	for _, peer := range targets {
		if _, exists := result.Values[peer]; !exists {
			t.Fatalf("expected value for peer %s", peer)
		}
	}
}

// TestNewGroup_WaitAll_Timeout 测试超时
func TestNewGroup_WaitAll_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	targets := []model.PeerID{"node-1", "node-2"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		// 所有任务都延迟
		time.Sleep(1 * time.Second)
		return "slow", nil
	})

	result := group.WaitAll(ctx)

	// 超时后应该没有完成的结果
	if len(result.SuccessPeers)+len(result.FailedPeers) > 0 {
		t.Fatalf("expected no completed peers on timeout, got: %d", len(result.SuccessPeers)+len(result.FailedPeers))
	}
}

// ==========================================
// CancelAll 测试
// ==========================================

// TestNewGroup_CancelAll 测试取消所有操作
func TestNewGroup_CancelAll(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		// 长时间运行的任务
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
			return "should not reach here", nil
		}
	})

	// 等待任务启动
	time.Sleep(50 * time.Millisecond)

	// 取消所有操作
	err := group.CancelAll()
	if err != nil {
		t.Fatalf("expected no error on CancelAll, got: %v", err)
	}

	// 等待取消生效
	time.Sleep(100 * time.Millisecond)

	// 检查状态
	statuses := group.Status()
	canceledCount := 0
	for _, status := range statuses {
		if status == StatusCanceled {
			canceledCount++
		}
	}

	if canceledCount == 0 {
		t.Fatal("expected at least some operations to be canceled")
	}
}

// ==========================================
// 回调机制测试
// ==========================================

// TestNewGroup_Callback 测试回调机制
func TestNewGroup_Callback(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		if target == "node-3" {
			return "", errors.New("failed")
		}
		return fmt.Sprintf("success-%s", target), nil
	})

	// 设置回调
	callback := newMockGroupCallback[string]()
	group.SetCallback(callback)

	// 等待全部完成
	_ = group.WaitAll(ctx)

	// 等待回调执行
	time.Sleep(200 * time.Millisecond)

	// 检查成功回调
	if atomic.LoadInt64(&callback.successCount) != 2 {
		t.Fatalf("expected 2 success callbacks, got: %d", atomic.LoadInt64(&callback.successCount))
	}

	// 检查失败回调
	if atomic.LoadInt64(&callback.failureCount) != 1 {
		t.Fatalf("expected 1 failure callback, got: %d", atomic.LoadInt64(&callback.failureCount))
	}

	// 检查多数派回调（3个目标，需要2个成功）
	if atomic.LoadInt64(&callback.majorityCount) != 1 {
		t.Fatalf("expected 1 majority callback, got: %d", atomic.LoadInt64(&callback.majorityCount))
	}

	// 检查全部完成回调
	if atomic.LoadInt64(&callback.fullDoneCount) != 1 {
		t.Fatalf("expected 1 full done callback, got: %d", atomic.LoadInt64(&callback.fullDoneCount))
	}
}

// TestNewGroup_Callback_OnSuccess 测试成功回调
func TestNewGroup_Callback_OnSuccess(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return fmt.Sprintf("value-%s", target), nil
	})

	callback := newMockGroupCallback[string]()
	group.SetCallback(callback)

	_ = group.WaitAll(ctx)
	time.Sleep(100 * time.Millisecond)

	// 检查每个成功回调都收到了正确的值
	callback.mu.Lock()
	for _, peer := range targets {
		expectedValue := fmt.Sprintf("value-%s", peer)
		if value, exists := callback.successValues[peer]; !exists || value != expectedValue {
			t.Errorf("expected success value %s for peer %s, got: %s", expectedValue, peer, value)
		}
	}
	callback.mu.Unlock()
}

// TestNewGroup_Callback_OnFailure 测试失败回调
func TestNewGroup_Callback_OnFailure(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2"}
	expectedErr := errors.New("test error")

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return "", expectedErr
	})

	callback := newMockGroupCallback[string]()
	group.SetCallback(callback)

	_ = group.WaitAll(ctx)
	time.Sleep(100 * time.Millisecond)

	// 检查每个失败回调都收到了错误
	callback.mu.Lock()
	for _, peer := range targets {
		if err, exists := callback.failureErrors[peer]; !exists || err != expectedErr {
			t.Errorf("expected error %v for peer %s, got: %v", expectedErr, peer, err)
		}
	}
	callback.mu.Unlock()
}

// ==========================================
// 统计信息测试
// ==========================================

// TestNewGroup_Stats 测试统计信息
func TestNewGroup_Stats(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		if target == "node-3" {
			return "", errors.New("failed")
		}
		return "success", nil
	})

	// 初始统计
	stats := group.Stats()
	if stats.TotalPeers != 3 {
		t.Fatalf("expected total peers 3, got: %d", stats.TotalPeers)
	}

	// 等待完成
	_ = group.WaitAll(ctx)

	// 完成后统计
	stats = group.Stats()
	if stats.SuccessCount != 2 {
		t.Fatalf("expected success count 2, got: %d", stats.SuccessCount)
	}
	if stats.FailureCount != 1 {
		t.Fatalf("expected failure count 1, got: %d", stats.FailureCount)
	}

	// 检查时间
	if stats.StartTime.IsZero() {
		t.Fatal("expected start time to be set")
	}
	if stats.FirstResponseTime.IsZero() {
		t.Fatal("expected first response time to be set")
	}
}

// TestNewGroup_Status 测试状态查询
func TestNewGroup_Status(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return "success", nil
	})

	// 初始状态（可能还在 pending）
	statuses := group.Status()
	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got: %d", len(statuses))
	}

	// 等待完成
	_ = group.WaitAll(ctx)

	// 完成后状态
	statuses = group.Status()
	for peer, status := range statuses {
		if status != StatusCompleted {
			t.Errorf("expected status Completed for peer %s, got: %v", peer, status)
		}
	}
}

// ==========================================
// 并发安全测试
// ==========================================

// TestNewGroup_ConcurrentAccess 测试并发访问
func TestNewGroup_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return "success", nil
	})

	var wg sync.WaitGroup
	const goroutines = 10

	// 并发读取状态和统计
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = group.Status()
			_ = group.Stats()
		}()
	}

	// 等待完成
	_ = group.WaitAll(ctx)
	wg.Wait()
}

// TestNewGroup_ConcurrentWait 测试并发等待
func TestNewGroup_ConcurrentWait(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return "success", nil
	})

	var wg sync.WaitGroup
	const goroutines = 5

	// 并发等待
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = group.WaitAll(ctx)
		}()
	}

	wg.Wait()

	// 所有等待应该返回相同的结果
	stats := group.Stats()
	if stats.SuccessCount != 3 {
		t.Fatalf("expected success count 3, got: %d", stats.SuccessCount)
	}
}

// ==========================================
// 边界情况测试
// ==========================================

// TestNewGroup_EmptyTargets 测试空目标列表
func TestNewGroup_EmptyTargets(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return "success", nil
	})

	// 空目标应该立即完成
	result := group.WaitAll(ctx)
	if len(result.SuccessPeers) != 0 {
		t.Fatalf("expected 0 success peers, got: %d", len(result.SuccessPeers))
	}
}

// TestNewGroup_SingleTarget 测试单个目标
func TestNewGroup_SingleTarget(t *testing.T) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return "success", nil
	})

	result := group.WaitAll(ctx)
	if len(result.SuccessPeers) != 1 {
		t.Fatalf("expected 1 success peer, got: %d", len(result.SuccessPeers))
	}

	// 单个目标也构成多数派
	result = group.WaitMajority(ctx)
	if len(result.SuccessPeers) != 1 {
		t.Fatalf("expected 1 success peer in majority, got: %d", len(result.SuccessPeers))
	}
}

// TestNewGroup_LargeGroup 测试大量目标
func TestNewGroup_LargeGroup(t *testing.T) {
	ctx := context.Background()

	// 创建 100 个目标
	targets := make([]model.PeerID, 100)
	for i := 0; i < 100; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return "success", nil
	})

	result := group.WaitAll(ctx)
	if len(result.SuccessPeers) != 100 {
		t.Fatalf("expected 100 success peers, got: %d", len(result.SuccessPeers))
	}

	stats := group.Stats()
	if stats.TotalPeers != 100 {
		t.Fatalf("expected total peers 100, got: %d", stats.TotalPeers)
	}
}

// TestNewGroup_ContextCancellation 测试 context 取消
func TestNewGroup_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	targets := []model.PeerID{"node-1", "node-2"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
			return "should not reach here", nil
		}
	})

	// 等待任务启动
	time.Sleep(50 * time.Millisecond)

	// 取消 context
	cancel()

	// 等待完成
	result := group.WaitAll(ctx)

	// 应该没有成功的（因为被取消了）
	if len(result.SuccessPeers) > 0 {
		t.Fatalf("expected no success peers after cancellation, got: %d", len(result.SuccessPeers))
	}
}

// ==========================================
// 基准测试
// ==========================================

// BenchmarkNewGroup_WaitAll 基准测试 WaitAll
func BenchmarkNewGroup_WaitAll(b *testing.B) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return "success", nil
		})
		_ = group.WaitAll(ctx)
	}
}

// BenchmarkNewGroup_WaitMajority 基准测试 WaitMajority
func BenchmarkNewGroup_WaitMajority(b *testing.B) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3", "node-4", "node-5"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return "success", nil
		})
		_ = group.WaitMajority(ctx)
	}
}

// BenchmarkNewGroup_WithCallback 基准测试带回调
func BenchmarkNewGroup_WithCallback(b *testing.B) {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return "success", nil
		})
		callback := newMockGroupCallback[string]()
		group.SetCallback(callback)
		_ = group.WaitAll(ctx)
	}
}

// ==========================================
// 示例测试
// ==========================================

// ExampleNewGroup 基本使用示例
func ExampleNewGroup() {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3", "node-4", "node-5"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		// 向每个节点发送请求
		return fmt.Sprintf("response-from-%s", target), nil
	})

	// 等待全部完成
	result := group.WaitAll(ctx)
	fmt.Printf("Success: %d, Failed: %d\n", len(result.SuccessPeers), len(result.FailedPeers))

	// 查看统计信息
	stats := group.Stats()
	fmt.Printf("Total: %d, Success: %d, Failure: %d\n",
		stats.TotalPeers, stats.SuccessCount, stats.FailureCount)

	// Output:
	// Success: 5, Failed: 0
	// Total: 5, Success: 5, Failure: 0
}

// ExampleNewGroup_waitAny WaitAny 示例
func ExampleNewGroup_waitAny() {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		// node-1 最快返回
		if target == "node-1" {
			return "fast-response", nil
		}
		time.Sleep(1 * time.Second)
		return "slow-response", nil
	})

	// 等待任意一个完成
	peer, value, err := group.WaitAny(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("First response from %s: %s\n", peer, value)

	// Output:
	// First response from node-1: fast-response
}

// ExampleNewGroup_withCallback 回调示例
func ExampleNewGroup_withCallback() {
	ctx := context.Background()
	targets := []model.PeerID{"node-1", "node-2", "node-3"}

	callback := &silentCallback{}
	group := NewGroup(ctx, nil, targets, func(ctx context.Context, target model.PeerID) (string, error) {
		return "success", nil
	})

	// 设置回调
	group.SetCallback(callback)

	// 等待完成
	_ = group.WaitAll(ctx)
	time.Sleep(100 * time.Millisecond) // 等待回调执行

	// 验证回调被调用
	fmt.Printf("Success count: %d\n", callback.getSuccessCount())

	// Output:
	// Success count: 3
}

// silentCallback 不打印的回调实现，用于测试
type silentCallback struct {
	successCount  int64
	failureCount  int64
	majorityCount int64
	fullDoneCount int64
	mu            sync.Mutex
}

func (s *silentCallback) OnSuccess(peer model.PeerID, value string, stats GroupStats) {
	s.mu.Lock()
	s.successCount++
	s.mu.Unlock()
}

func (s *silentCallback) OnFailure(peer model.PeerID, err error, stats GroupStats) {
	s.mu.Lock()
	s.failureCount++
	s.mu.Unlock()
}

func (s *silentCallback) OnMajorityReached(stats GroupStats) {
	s.mu.Lock()
	s.majorityCount++
	s.mu.Unlock()
}

func (s *silentCallback) OnFullDone(stats GroupStats) {
	s.mu.Lock()
	s.fullDoneCount++
	s.mu.Unlock()
}

func (s *silentCallback) getSuccessCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.successCount
}
