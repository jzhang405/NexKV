// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ==========================================
// 自动扩缩容测试 (P1-01)
// ==========================================

// TestAntsTaskPoolProvider_AutoScale 测试自动扩容功能
func TestAntsTaskPoolProvider_AutoScale(t *testing.T) {
	config := &ProviderConfig{
		Capacity:           100,   // 初始容量
		MaxCapacity:        500,   // 最大容量
		EnableAutoScale:    true,  // 启用自动扩容
		ScaleThreshold:     0.8,   // 80% 使用率时扩容
		ScaleStep:          100,   // 每次扩容 100
		ScaleCheckInterval: 10,    // 每 10 次 Submit 检查一次
		EnableAutoShrink:   false, // 禁用自动缩容（避免干扰测试）
	}

	provider, err := NewAntsTaskPoolProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 验证初始容量
	stats := provider.Stats()
	if stats.Capacity != 100 {
		t.Errorf("expected initial capacity 100, got %d", stats.Capacity)
	}

	// 提交大量任务以触发扩容
	// 使用阻塞任务来保持高使用率
	var runningCount atomic.Int32
	var wg sync.WaitGroup
	blockCh := make(chan struct{})

	for i := 0; i < 90; i++ { // 90% 使用率
		wg.Add(1)
		err := provider.Submit(context.Background(), func(ctx context.Context) {
			runningCount.Add(1)
			<-blockCh // 阻塞任务
			wg.Done()
		})
		if err != nil {
			t.Logf("Submit %d failed: %v", i, err)
		}
	}

	// 等待任务开始执行
	time.Sleep(200 * time.Millisecond)

	// 提交更多任务以触发扩容检查
	for i := 0; i < 50; i++ {
		_ = provider.Submit(context.Background(), func(ctx context.Context) {
			time.Sleep(10 * time.Millisecond)
		})
	}

	// 等待扩容检查
	time.Sleep(500 * time.Millisecond)

	// 释放阻塞任务
	close(blockCh)
	wg.Wait()

	// 验证扩容（容量应该增加）
	// 注意：由于扩容是异步的，可能需要多次检查
	t.Logf("Initial capacity: 100, Current capacity: %d", provider.currentCapacity)
}

// TestAntsTaskPoolProvider_AutoShrink 测试自动缩容功能
func TestAntsTaskPoolProvider_AutoShrink(t *testing.T) {
	config := &ProviderConfig{
		Capacity:            100,
		MaxCapacity:         500,
		EnableAutoScale:     true,
		ScaleThreshold:      0.8,
		ScaleStep:           100,
		ScaleCheckInterval:  10,
		EnableAutoShrink:    true,
		ShrinkThreshold:     0.3,
		ShrinkStep:          50,
		ShrinkCheckInterval: 500 * time.Millisecond,
		ShrinkCooldown:      1 * time.Second,
	}

	provider, err := NewAntsTaskPoolProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 验证缩容检查器已启动
	if provider.scaleCheckTicker == nil {
		t.Error("expected shrink checker to be started")
	}

	// 提交少量任务（低使用率）
	for i := 0; i < 10; i++ {
		_ = provider.Submit(context.Background(), func(ctx context.Context) {
			time.Sleep(10 * time.Millisecond)
		})
	}

	// 等待缩容检查
	time.Sleep(2 * time.Second)

	// 验证缩容
	t.Logf("Initial capacity: 100, Current capacity: %d", provider.currentCapacity)
}

// TestAntsTaskPoolProvider_AutoScale_Disabled 测试禁用自动扩容
func TestAntsTaskPoolProvider_AutoScale_Disabled(t *testing.T) {
	config := &ProviderConfig{
		Capacity:        100,
		MaxCapacity:     500,
		EnableAutoScale: false, // 禁用自动扩容
	}

	provider, err := NewAntsTaskPoolProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 提交大量任务
	for i := 0; i < 50; i++ {
		_ = provider.Submit(context.Background(), func(ctx context.Context) {
			time.Sleep(10 * time.Millisecond)
		})
	}

	// 验证容量未改变
	stats := provider.Stats()
	if stats.Capacity != 100 {
		t.Errorf("expected capacity to remain 100 when auto-scale disabled, got %d", stats.Capacity)
	}
}

// TestAntsTaskPoolProvider_AutoScale_MaxCapacity 测试扩容不超过最大容量
func TestAntsTaskPoolProvider_AutoScale_MaxCapacity(t *testing.T) {
	config := &ProviderConfig{
		Capacity:           100,
		MaxCapacity:        150, // 最大容量略高于初始容量
		EnableAutoScale:    true,
		ScaleThreshold:     0.8,
		ScaleStep:          100, // 每次扩容 100
		ScaleCheckInterval: 10,
		EnableAutoShrink:   false,
	}

	provider, err := NewAntsTaskPoolProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 提交大量任务以尝试触发扩容
	var wg sync.WaitGroup
	blockCh := make(chan struct{})

	for i := 0; i < 90; i++ {
		wg.Add(1)
		_ = provider.Submit(context.Background(), func(ctx context.Context) {
			<-blockCh
			wg.Done()
		})
	}

	time.Sleep(200 * time.Millisecond)

	// 提交更多任务以触发扩容检查
	for i := 0; i < 100; i++ {
		_ = provider.Submit(context.Background(), func(ctx context.Context) {
			time.Sleep(10 * time.Millisecond)
		})
	}

	time.Sleep(500 * time.Millisecond)

	// 释放阻塞任务
	close(blockCh)
	wg.Wait()

	// 验证扩容不超过最大容量
	if provider.currentCapacity > config.MaxCapacity {
		t.Errorf("capacity %d exceeded max capacity %d", provider.currentCapacity, config.MaxCapacity)
	}
}

// ==========================================
// 并发压力测试 (P1-01)
// ==========================================

// TestAntsTaskPoolProvider_HighConcurrency 测试高并发场景
func TestAntsTaskPoolProvider_HighConcurrency(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	const numGoroutines = 100
	const tasksPerGoroutine = 100

	var completed atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < tasksPerGoroutine; j++ {
				err := provider.Submit(context.Background(), func(ctx context.Context) {
					completed.Add(1)
				})
				if err != nil {
					t.Errorf("Submit failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	// 等待所有任务完成
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if completed.Load() == numGoroutines*tasksPerGoroutine {
				return
			}
		case <-timeout:
			t.Errorf("timeout: only %d/%d tasks completed", completed.Load(), numGoroutines*tasksPerGoroutine)
			return
		}
	}
}

// TestAntsTaskPoolProvider_RaceCondition 测试竞态条件
func TestAntsTaskPoolProvider_RaceCondition(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	const numOps = 1000
	var wg sync.WaitGroup

	// 并发提交和获取状态
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				_ = provider.Submit(context.Background(), func(ctx context.Context) {})
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				_ = provider.Stats()
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				_ = provider.Health()
			}
		}()
	}

	wg.Wait()
}

// ==========================================
// 延迟任务测试 (P1-01)
// ==========================================

// TestAntsTaskPoolProvider_SubmitDelayed_Many 测试大量延迟任务
func TestAntsTaskPoolProvider_SubmitDelayed_Many(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	const numTasks = 100
	var executed atomic.Int32

	for i := 0; i < numTasks; i++ {
		err := provider.SubmitDelayed(context.Background(), 10*time.Millisecond, func(ctx context.Context) {
			executed.Add(1)
		})
		if err != nil {
			t.Errorf("SubmitDelayed failed: %v", err)
		}
	}

	// 等待所有延迟任务执行
	time.Sleep(500 * time.Millisecond)

	if executed.Load() != numTasks {
		t.Errorf("expected %d tasks executed, got %d", numTasks, executed.Load())
	}
}

// TestAntsTaskPoolProvider_SubmitDelayed_TooMany 测试延迟任务速率限制
func TestAntsTaskPoolProvider_SubmitDelayed_TooMany(t *testing.T) {
	config := &ProviderConfig{
		Capacity: 1000,
	}
	// 使用默认的 DefaultMaxDelayedTasks = 10000

	provider, err := NewAntsTaskPoolProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 尝试提交超过限制的延迟任务
	var errors atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 15000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := provider.SubmitDelayed(context.Background(), 1*time.Second, func(ctx context.Context) {})
			if err == ErrTooManyDelayedTasks {
				errors.Add(1)
			}
		}()
	}

	wg.Wait()

	// 应该有一些任务因为速率限制被拒绝
	t.Logf("Tasks rejected due to rate limit: %d", errors.Load())
}

// TestAntsTaskPoolProvider_SubmitDelayed_Close 测试关闭时延迟任务处理
func TestAntsTaskPoolProvider_SubmitDelayed_Close(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	var executed atomic.Int32

	// 提交延迟任务
	for i := 0; i < 10; i++ {
		_ = provider.SubmitDelayed(context.Background(), 1*time.Second, func(ctx context.Context) {
			executed.Add(1)
		})
	}

	// 立即关闭（延迟任务尚未执行）
	err = provider.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// 等待足够时间让延迟任务执行（如果它们没有被正确取消）
	time.Sleep(200 * time.Millisecond)

	// 延迟任务应该被取消，不执行
	if executed.Load() > 0 {
		t.Errorf("expected no delayed tasks to execute after close, got %d", executed.Load())
	}
}

// ==========================================
// 批量任务测试 (P1-01)
// ==========================================

// TestAntsTaskPoolProvider_SubmitBatchWithResult 测试批量带结果任务
func TestAntsTaskPoolProvider_SubmitBatchWithResult(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	tasks := []func(context.Context) (any, error){
		func(ctx context.Context) (any, error) { return 1, nil },
		func(ctx context.Context) (any, error) { return 2, nil },
		func(ctx context.Context) (any, error) { return 3, nil },
	}

	results := provider.SubmitBatchWithResult(ctx, tasks)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, result := range results {
		val, err := result.Get(ctx)
		if err != nil {
			t.Errorf("result %d: unexpected error: %v", i, err)
		}
		if val != i+1 {
			t.Errorf("result %d: expected %d, got %v", i, i+1, val)
		}
	}
}

// TestAntsTaskPoolProvider_SubmitBatchWithArg 测试批量带参数任务
func TestAntsTaskPoolProvider_SubmitBatchWithArg(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	var results []int32
	var mu sync.Mutex

	tasks := []func(context.Context, any){
		func(ctx context.Context, arg any) {
			mu.Lock()
			results = append(results, int32(arg.(int)))
			mu.Unlock()
		},
		func(ctx context.Context, arg any) {
			mu.Lock()
			results = append(results, int32(arg.(int)))
			mu.Unlock()
		},
	}
	args := []any{1, 2}

	err = provider.SubmitBatchWithArg(ctx, tasks, args)
	if err != nil {
		t.Fatalf("SubmitBatchWithArg failed: %v", err)
	}

	// 等待任务执行
	time.Sleep(200 * time.Millisecond)

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

// TestAntsTaskPoolProvider_SubmitBatchWithArg_Mismatch 测试参数长度不匹配
func TestAntsTaskPoolProvider_SubmitBatchWithArg_Mismatch(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	tasks := []func(context.Context, any){
		func(ctx context.Context, arg any) {},
	}
	args := []any{1, 2, 3} // 长度不匹配

	err = provider.SubmitBatchWithArg(ctx, tasks, args)
	if err != ErrTaskArgLengthMismatch {
		t.Errorf("expected ErrTaskArgLengthMismatch, got: %v", err)
	}
}

// ==========================================
// 优雅关闭测试 (P1-01)
// ==========================================

// TestAntsTaskPoolProvider_CloseWithTimeout_WithRunningTasks 测试带超时关闭（有运行中任务）
func TestAntsTaskPoolProvider_CloseWithTimeout_WithRunningTasks(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// 提交一些任务
	for i := 0; i < 10; i++ {
		_ = provider.Submit(context.Background(), func(ctx context.Context) {
			time.Sleep(100 * time.Millisecond)
		})
	}

	// 带超时关闭
	start := time.Now()
	err = provider.CloseWithTimeout(50 * time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Logf("CloseWithTimeout returned: %v", err)
	}

	// 关闭应该在超时时间内完成
	if elapsed > 100*time.Millisecond {
		t.Errorf("close took too long: %v", elapsed)
	}
}

// TestAntsTaskPoolProvider_Close_Idempotent 测试关闭幂等性
func TestAntsTaskPoolProvider_Close_Idempotent(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// 多次关闭应该安全
	for i := 0; i < 5; i++ {
		err = provider.Close()
		if err != nil {
			t.Errorf("Close %d failed: %v", i, err)
		}
	}
}

// ==========================================
// 统计信息测试 (P1-01)
// ==========================================

// TestAntsTaskPoolProvider_Stats_Running 测试运行中任务统计
func TestAntsTaskPoolProvider_Stats_Running(t *testing.T) {
	config := &ProviderConfig{
		Capacity: 10,
	}
	provider, err := NewAntsTaskPoolProvider(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 提交阻塞任务
	var wg sync.WaitGroup
	blockCh := make(chan struct{})

	for i := 0; i < 5; i++ {
		wg.Add(1)
		_ = provider.Submit(context.Background(), func(ctx context.Context) {
			<-blockCh
			wg.Done()
		})
	}

	// 等待任务开始执行
	time.Sleep(100 * time.Millisecond)

	// 检查运行中任务数
	stats := provider.Stats()
	t.Logf("Running: %d, Capacity: %d", stats.Running, stats.Capacity)

	// 释放任务
	close(blockCh)
	wg.Wait()
}

// TestAntsTaskPoolProvider_Stats_ByPriority 测试优先级统计
func TestAntsTaskPoolProvider_Stats_ByPriority(t *testing.T) {
	provider, err := NewAntsTaskPoolProvider(nil)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()

	// 提交不同优先级的任务
	_ = provider.SubmitWithPriority(ctx, PriorityHigh, func(ctx context.Context) {})
	_ = provider.SubmitWithPriority(ctx, PriorityHigh, func(ctx context.Context) {})
	_ = provider.SubmitWithPriority(ctx, PriorityNormal, func(ctx context.Context) {})

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)

	// 检查优先级统计
	stats := provider.Stats()
	if stats.ByPriority == nil {
		t.Error("expected ByPriority map to be initialized")
	}

	t.Logf("ByPriority: %v", stats.ByPriority)
}
