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
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
)

// ==========================================
// 测试辅助函数
// ==========================================

// mockGoroutineProvider 模拟协程池提供者（实现完整 GoroutineProvider 接口）
type mockGoroutineProvider struct {
	submitCount int64
}

func (m *mockGoroutineProvider) Submit(ctx context.Context, task func(context.Context)) error {
	atomic.AddInt64(&m.submitCount, 1)
	go task(ctx)
	return nil
}

// 以下为接口兼容性存根方法（测试中未使用）

func (m *mockGoroutineProvider) SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error {
	return m.Submit(ctx, func(ctx context.Context) { task(ctx, arg) })
}

func (m *mockGoroutineProvider) SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) service.GoroutineResult[any] {
	return concurrency.NewAnyResult()
}

func (m *mockGoroutineProvider) SubmitWithArgAndResult(ctx context.Context, task func(context.Context, any) (any, error), arg any) service.GoroutineResult[any] {
	return concurrency.NewAnyResult()
}

func (m *mockGoroutineProvider) SubmitWithPriority(ctx context.Context, priority service.GoroutinePriority, task func(context.Context)) error {
	return m.Submit(ctx, task)
}

func (m *mockGoroutineProvider) SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error {
	go func() {
		time.Sleep(delay)
		_ = m.Submit(ctx, task)
	}()
	return nil
}

func (m *mockGoroutineProvider) SubmitAdvanced(ctx context.Context, task func(context.Context, any) (any, error), arg any, opts ...service.GoroutineSubmitOption) service.GoroutineResult[any] {
	return concurrency.NewAnyResult()
}

func (m *mockGoroutineProvider) SubmitBatch(ctx context.Context, tasks []func(context.Context)) error {
	for _, task := range tasks {
		if err := m.Submit(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockGoroutineProvider) SubmitBatchWithArg(ctx context.Context, tasks []func(context.Context, any), args []any) error {
	for i, task := range tasks {
		if i < len(args) {
			if err := m.SubmitWithArg(ctx, task, args[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *mockGoroutineProvider) SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error {
	errs := make([]error, len(tasks))
	for i, task := range tasks {
		errs[i] = m.Submit(ctx, task)
	}
	return errs
}

func (m *mockGoroutineProvider) SubmitBatchWithResult(ctx context.Context, tasks []func(context.Context) (any, error)) []service.GoroutineResult[any] {
	results := make([]service.GoroutineResult[any], len(tasks))
	for i := range tasks {
		results[i] = concurrency.NewAnyResult()
	}
	return results
}

func (m *mockGoroutineProvider) Stats() service.GoroutinePoolStats {
	return service.GoroutinePoolStats{}
}

func (m *mockGoroutineProvider) Health() service.GoroutineHealthStatus {
	return model.GoroutineHealthStatusHealthy
}

func (m *mockGoroutineProvider) SetCapacity(capacity int) error {
	return nil
}

func (m *mockGoroutineProvider) Close() error {
	return nil
}

func (m *mockGoroutineProvider) CloseWithTimeout(timeout time.Duration) error {
	return nil
}

func (m *mockGoroutineProvider) getSubmitCount() int64 {
	return atomic.LoadInt64(&m.submitCount)
}

// ==========================================
// 基础功能测试
// ==========================================

// TestNewOp_BasicExecution 测试基本执行流程
func TestNewOp_BasicExecution(t *testing.T) {
	ctx := context.Background()
	expectedResult := "success"

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return expectedResult, nil
	})

	result, err := op.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != expectedResult {
		t.Fatalf("expected result %s, got: %s", expectedResult, result)
	}
	if op.Status() != StatusCompleted {
		t.Fatalf("expected status %v, got: %v", StatusCompleted, op.Status())
	}
	if !op.Status().IsTerminal() {
		t.Fatal("expected IsTerminal() to be true")
	}
}

// TestNewOp_ErrorHandling 测试错误处理
func TestNewOp_ErrorHandling(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("test error")

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return "", expectedErr
	})

	result, err := op.Get(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != expectedErr {
		t.Fatalf("expected error %v, got: %v", expectedErr, err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got: %s", result)
	}
	if op.Status() != StatusFailed {
		t.Fatalf("expected status %v, got: %v", StatusFailed, op.Status())
	}
}

// TestNewOp_IsStarted 测试启动状态
func TestNewOp_IsStarted(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return "success", nil
	})

	// 等待一小段时间确保任务启动
	time.Sleep(50 * time.Millisecond)

	if !op.IsStarted() {
		t.Fatal("expected IsStarted() to be true")
	}

	// 等待完成
	_, _ = op.Get(ctx)
}

// ==========================================
// 状态转换测试
// ==========================================

// TestOperationStatus_Transitions 测试状态转换
func TestOperationStatus_Transitions(t *testing.T) {
	tests := []struct {
		name           string
		execFunc       func(ctx context.Context) (string, error)
		expectedStatus OperationStatus
	}{
		{
			name: "success to completed",
			execFunc: func(ctx context.Context) (string, error) {
				return "success", nil
			},
			expectedStatus: StatusCompleted,
		},
		{
			name: "error to failed",
			execFunc: func(ctx context.Context) (string, error) {
				return "", errors.New("error")
			},
			expectedStatus: StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			op := NewOp(ctx, nil, tt.execFunc)
			_, _ = op.Get(ctx)

			if op.Status() != tt.expectedStatus {
				t.Errorf("expected status %v, got: %v", tt.expectedStatus, op.Status())
			}
		})
	}
}

// TestOperationStatus_IsTerminal 测试终态判断
func TestOperationStatus_IsTerminal(t *testing.T) {
	terminalStates := []OperationStatus{
		StatusCompleted,
		StatusFailed,
		StatusCanceled,
		StatusDiscarded,
		StatusTimeout,
	}

	nonTerminalStates := []OperationStatus{
		StatusPending,
		StatusRunning,
	}

	for _, status := range terminalStates {
		if !status.IsTerminal() {
			t.Errorf("expected %v to be terminal", status)
		}
	}

	for _, status := range nonTerminalStates {
		if status.IsTerminal() {
			t.Errorf("expected %v to be non-terminal", status)
		}
	}
}

// TestOperationStatus_String 测试状态字符串
func TestOperationStatus_String(t *testing.T) {
	tests := []struct {
		status   OperationStatus
		expected string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusCanceled, "canceled"},
		{StatusDiscarded, "discarded"},
		{StatusTimeout, "timeout"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.expected {
			t.Errorf("expected %s, got: %s", tt.expected, got)
		}
	}
}

// ==========================================
// 超时和取消测试
// ==========================================

// TestNewOp_Timeout 测试超时处理
func TestNewOp_Timeout(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		// 模拟长时间运行的任务
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
			return "should not reach here", nil
		}
	}, WithTimeout(100*time.Millisecond))

	result, err := op.Get(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded error, got: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got: %s", result)
	}

	// 等待状态更新
	time.Sleep(50 * time.Millisecond)
	if op.Status() != StatusTimeout {
		t.Fatalf("expected status %v, got: %v", StatusTimeout, op.Status())
	}
}

// TestNewOp_Cancel 测试取消操作
func TestNewOp_Cancel(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		// 模拟长时间运行的任务
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
			return "should not reach here", nil
		}
	})

	// 等待任务启动
	time.Sleep(50 * time.Millisecond)

	// 取消操作
	canceled, err := op.Cancel()
	if err != nil {
		t.Fatalf("expected no error on cancel, got: %v", err)
	}
	if !canceled {
		t.Fatal("expected canceled to be true")
	}

	// 等待状态更新
	time.Sleep(50 * time.Millisecond)
	if op.Status() != StatusCanceled {
		t.Fatalf("expected status %v, got: %v", StatusCanceled, op.Status())
	}

	// 尝试再次取消（应该失败）
	canceled, err = op.Cancel()
	if err == nil {
		t.Fatal("expected error on second cancel")
	}
	if canceled {
		t.Fatal("expected canceled to be false on second cancel")
	}
}

// TestNewOp_CancelCompletedOperation 测试取消已完成的操作
func TestNewOp_CancelCompletedOperation(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return "success", nil
	})

	// 等待完成
	_, _ = op.Get(ctx)

	// 尝试取消已完成的操作（应该失败）
	canceled, err := op.Cancel()
	if err == nil {
		t.Fatal("expected error when canceling completed operation")
	}
	if canceled {
		t.Fatal("expected canceled to be false")
	}
}

// TestNewOp_Discard 测试丢弃操作
func TestNewOp_Discard(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		time.Sleep(200 * time.Millisecond)
		return "should be discarded", nil
	})

	// 等待任务启动
	time.Sleep(50 * time.Millisecond)

	// 丢弃操作
	err := op.Discard()
	if err != nil {
		t.Fatalf("expected no error on discard, got: %v", err)
	}

	// 等待状态更新
	time.Sleep(100 * time.Millisecond)
	if op.Status() != StatusDiscarded {
		t.Fatalf("expected status %v, got: %v", StatusDiscarded, op.Status())
	}

	// 尝试再次丢弃（应该失败）
	err = op.Discard()
	if err == nil {
		t.Fatal("expected error on second discard")
	}
}

// TestNewOp_DiscardCompletedOperation 测试丢弃已完成的操作
func TestNewOp_DiscardCompletedOperation(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return "success", nil
	})

	// 等待完成
	_, _ = op.Get(ctx)

	// 尝试丢弃已完成的操作（应该失败）
	err := op.Discard()
	if err == nil {
		t.Fatal("expected error when discarding completed operation")
	}
}

// ==========================================
// 回调机制测试
// ==========================================

// TestNewOp_OnComplete 测试回调机制
func TestNewOp_OnComplete(t *testing.T) {
	ctx := context.Background()
	expectedResult := "success"

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return expectedResult, nil
	})

	var callbackResult string
	var callbackErr error
	var callbackCalled int64

	cbID := op.OnComplete(func(result string, err error) {
		atomic.StoreInt64(&callbackCalled, 1)
		callbackResult = result
		callbackErr = err
	})

	if cbID == "" {
		t.Fatal("expected callback ID to be non-empty")
	}

	// 等待完成
	_, _ = op.Get(ctx)

	// 等待回调执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt64(&callbackCalled) == 0 {
		t.Fatal("expected callback to be called")
	}
	if callbackResult != expectedResult {
		t.Fatalf("expected callback result %s, got: %s", expectedResult, callbackResult)
	}
	if callbackErr != nil {
		t.Fatalf("expected no callback error, got: %v", callbackErr)
	}
}

// TestNewOp_OnCompleteMultipleCallbacks 测试多个回调
func TestNewOp_OnCompleteMultipleCallbacks(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return "success", nil
	})

	var callbackCount int64

	// 注册多个回调
	for i := 0; i < 5; i++ {
		op.OnComplete(func(result string, err error) {
			atomic.AddInt64(&callbackCount, 1)
		})
	}

	// 等待完成
	_, _ = op.Get(ctx)

	// 等待回调执行
	time.Sleep(200 * time.Millisecond)

	if count := atomic.LoadInt64(&callbackCount); count != 5 {
		t.Fatalf("expected 5 callbacks, got: %d", count)
	}
}

// TestNewOp_OffComplete 测试注销回调
func TestNewOp_OffComplete(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		time.Sleep(200 * time.Millisecond)
		return "success", nil
	})

	var callbackCalled int64

	cbID := op.OnComplete(func(result string, err error) {
		atomic.StoreInt64(&callbackCalled, 1)
	})

	// 注销回调
	err := op.OffComplete(cbID)
	if err != nil {
		t.Fatalf("expected no error on OffComplete, got: %v", err)
	}

	// 等待完成
	_, _ = op.Get(ctx)

	// 等待回调执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt64(&callbackCalled) == 1 {
		t.Fatal("expected callback not to be called after OffComplete")
	}
}

// TestNewOp_OffCompleteNotFound 测试注销不存在的回调
func TestNewOp_OffCompleteNotFound(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return "success", nil
	})

	err := op.OffComplete("non-existent-id")
	if err == nil {
		t.Fatal("expected error when OffComplete with non-existent ID")
	}
}

// TestNewOp_OnCompleteAfterCompletion 测试操作完成后注册回调
func TestNewOp_OnCompleteAfterCompletion(t *testing.T) {
	ctx := context.Background()
	expectedResult := "success"

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return expectedResult, nil
	})

	// 等待完成
	_, _ = op.Get(ctx)

	// 操作完成后注册回调（应该立即执行）
	var callbackResult string
	var callbackCalled int64

	op.OnComplete(func(result string, err error) {
		atomic.StoreInt64(&callbackCalled, 1)
		callbackResult = result
	})

	// 等待回调执行
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt64(&callbackCalled) == 0 {
		t.Fatal("expected callback to be called immediately after completion")
	}
	if callbackResult != expectedResult {
		t.Fatalf("expected callback result %s, got: %s", expectedResult, callbackResult)
	}
}

// TestNewOp_CallbackPanicRecovery 测试回调 panic 恢复
func TestNewOp_CallbackPanicRecovery(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return "success", nil
	})

	// 注册会 panic 的回调
	panicCallbackCalled := int64(0)
	op.OnComplete(func(result string, err error) {
		atomic.StoreInt64(&panicCallbackCalled, 1)
		panic("callback panic")
	})

	// 注册正常回调（应该在 panic 回调之后执行）
	normalCallbackCalled := int64(0)
	op.OnComplete(func(result string, err error) {
		atomic.StoreInt64(&normalCallbackCalled, 1)
	})

	// 等待完成
	_, _ = op.Get(ctx)

	// 等待回调执行
	time.Sleep(200 * time.Millisecond)

	// 两个回调都应该被调用（panic 不应该影响其他回调）
	if atomic.LoadInt64(&panicCallbackCalled) == 0 {
		t.Fatal("expected panic callback to be called")
	}
	if atomic.LoadInt64(&normalCallbackCalled) == 0 {
		t.Fatal("expected normal callback to be called even after panic")
	}
}

// ==========================================
// 协程池集成测试
// ==========================================

// TestNewOp_WithGoroutineProvider 测试协程池集成
func TestNewOp_WithGoroutineProvider(t *testing.T) {
	ctx := context.Background()
	mockProvider := &mockGoroutineProvider{}
	expectedResult := "success"

	op := NewOp(ctx, mockProvider, func(ctx context.Context) (string, error) {
		return expectedResult, nil
	})

	result, err := op.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != expectedResult {
		t.Fatalf("expected result %s, got: %s", expectedResult, result)
	}

	// 验证协程池被调用
	if mockProvider.getSubmitCount() != 1 {
		t.Fatalf("expected 1 submit call, got: %d", mockProvider.getSubmitCount())
	}
}

// ==========================================
// 并发安全测试
// ==========================================

// TestNewOp_ConcurrentAccess 测试并发访问
func TestNewOp_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return "success", nil
	})

	var wg sync.WaitGroup
	const goroutines = 10

	// 并发读取状态
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = op.Status()
			_ = op.IsStarted()
		}()
	}

	// 并发注册回调
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cbID := op.OnComplete(func(result string, err error) {})
			if cbID != "" {
				_ = op.OffComplete(cbID)
			}
		}()
	}

	// 等待完成
	_, _ = op.Get(ctx)
	wg.Wait()
}

// TestNewOp_ResultChannel 测试结果通道
func TestNewOp_ResultChannel(t *testing.T) {
	ctx := context.Background()
	expectedResult := "success"

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return expectedResult, nil
	})

	asyncOp := op.(*AsyncOp[string])
	resultCh := asyncOp.ResultChan()

	select {
	case result := <-resultCh:
		if result.Value != expectedResult {
			t.Fatalf("expected result %s, got: %s", expectedResult, result.Value)
		}
		if result.Err != nil {
			t.Fatalf("expected no error, got: %v", result.Err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

// ==========================================
// 边界情况测试
// ==========================================

// TestNewOp_ContextCancellation 测试 context 取消传播
func TestNewOp_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
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
	_, err := op.Get(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestNewOp_ZeroTimeout 测试零超时（无超时）
func TestNewOp_ZeroTimeout(t *testing.T) {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return "success", nil
	}, WithTimeout(0)) // 零超时表示无超时

	result, err := op.Get(ctx)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != "success" {
		t.Fatalf("expected result 'success', got: %s", result)
	}
}

// ==========================================
// 基准测试
// ==========================================

// BenchmarkNewOp_Basic 基准测试基本操作
func BenchmarkNewOp_Basic(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
			return "success", nil
		})
		_, _ = op.Get(ctx)
	}
}

// BenchmarkNewOp_WithCallback 基准测试回调
func BenchmarkNewOp_WithCallback(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
			return "success", nil
		})
		op.OnComplete(func(result string, err error) {})
		_, _ = op.Get(ctx)
	}
}

// BenchmarkNewOp_WithGoroutineProvider 基准测试协程池
func BenchmarkNewOp_WithGoroutineProvider(b *testing.B) {
	ctx := context.Background()
	mockProvider := &mockGoroutineProvider{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := NewOp(ctx, mockProvider, func(ctx context.Context) (string, error) {
			return "success", nil
		})
		_, _ = op.Get(ctx)
	}
}

// ==========================================
// 示例测试
// ==========================================

// ExampleNewOp 基本使用示例
func ExampleNewOp() {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		// 执行异步操作
		return "hello world", nil
	})

	result, err := op.Get(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Result: %s\n", result)
	fmt.Printf("Status: %s\n", op.Status())

	// Output:
	// Result: hello world
	// Status: completed
}

// ExampleNewOp_withTimeout 超时示例
func ExampleNewOp_withTimeout() {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		// 模拟长时间运行的任务
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
			return "done", nil
		}
	}, WithTimeout(100*time.Millisecond))

	result, err := op.Get(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Result: %s\n", result)

	// Output:
	// Error: context deadline exceeded
}

// ExampleNewOp_withCallback 回调示例
func ExampleNewOp_withCallback() {
	ctx := context.Background()

	op := NewOp(ctx, nil, func(ctx context.Context) (string, error) {
		return "success", nil
	})

	op.OnComplete(func(result string, err error) {
		if err != nil {
			fmt.Printf("Failed: %v\n", err)
			return
		}
		fmt.Printf("Completed: %s\n", result)
	})

	_, _ = op.Get(ctx)
	time.Sleep(100 * time.Millisecond) // 等待回调执行

	// Output:
	// Completed: success
}
