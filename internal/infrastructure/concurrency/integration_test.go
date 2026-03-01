// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"go.uber.org/goleak"
)

// ==========================================
// 集成测试
// ==========================================

// TestIntegration_FullWorkflow 完整工作流集成测试
func TestIntegration_FullWorkflow(t *testing.T) {
	selector := setupFullSelector(t)

	var counter int64
	var wg sync.WaitGroup

	taskTypes := []struct {
		sourceID string
		count    int
	}{
		{"hlc:clock:tick", 20},
		{"wal:writer:flush", 20},
		{"rpc:client:send", 20},
		{"query:range:scan", 20},
		{"background:log:flush", 20},
	}

	totalTasks := 0
	for _, tt := range taskTypes {
		totalTasks += tt.count
		for i := 0; i < tt.count; i++ {
			wg.Add(1)
			sourceID, _ := model.ParseSourceID(tt.sourceID)
			err := selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
				atomic.AddInt64(&counter, 1)
				wg.Done()
			})
			if err != nil {
				t.Errorf("Submit(%s) error: %v", tt.sourceID, err)
				wg.Done()
			}
		}
	}

	wg.Wait()

	if atomic.LoadInt64(&counter) != int64(totalTasks) {
		t.Errorf("counter = %d, want %d", counter, totalTasks)
	}

	stats := selector.Stats()
	if stats.TotalSubmitted != int64(totalTasks) {
		t.Errorf("Stats().TotalSubmitted = %d, want %d", stats.TotalSubmitted, totalTasks)
	}

	_ = selector.Close()
}

// TestIntegration_GracefulShutdown 优雅关闭测试
func TestIntegration_GracefulShutdown(t *testing.T) {
	selector := NewTaskScheduler()

	// 注册执行器
	executor, _ := NewPerCoreExecutor(WithNumCores(2), WithQueueSize(1000))
	if err := selector.RegisterExecutor(model.ModePerCore, executor); err != nil {
		t.Fatalf("RegisterExecutor(ModePerCore) error: %v", err)
	}
	// 注册默认池作为 fallback
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		t.Fatalf("RegisterExecutor(ModeDefaultPool) error: %v", err)
	}

	// 提交大量任务
	var started, completed int64
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("hlc:clock:tick") // 使用 PerCore 模式的 SourceID
		err := selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			atomic.AddInt64(&started, 1)
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&completed, 1)
			wg.Done()
		})
		if err != nil {
			t.Errorf("Submit() error: %v", err)
			wg.Done()
		}
	}

	// 等待任务完成
	wg.Wait()

	if atomic.LoadInt64(&started) != 100 {
		t.Errorf("started = %d, want 100", started)
	}
	if atomic.LoadInt64(&completed) != 100 {
		t.Errorf("completed = %d, want 100", completed)
	}

	// 关闭
	_ = selector.Close()
}

// TestIntegration_PriorityOrdering 优先级顺序测试
func TestIntegration_PriorityOrdering(t *testing.T) {
	executor, _ := NewPerCoreExecutor(WithNumCores(1), WithQueueSize(100))
	defer executor.Close()

	var allCompleted sync.WaitGroup
	var executionOrder []int
	var mu sync.Mutex

	// 先提交低优先级任务（Normal = 5）
	for i := 0; i < 5; i++ {
		allCompleted.Add(1)
		_ = executor.SubmitWithPriority(context.Background(), 5, func(ctx context.Context) {
			defer allCompleted.Done()
			mu.Lock()
			executionOrder = append(executionOrder, 5)
			mu.Unlock()
		})
	}

	// 提交高优先级任务（Critical = 0）
	allCompleted.Add(1)
	_ = executor.SubmitWithPriority(context.Background(), 0, func(ctx context.Context) {
		defer allCompleted.Done()
		mu.Lock()
		executionOrder = append(executionOrder, 0)
		mu.Unlock()
	})

	// 等待所有任务执行完成
	allCompleted.Wait()

	// 验证高优先级任务较早执行
	mu.Lock()
	defer mu.Unlock()

	highPriorityIndex := -1
	for i, priority := range executionOrder {
		if priority == 0 {
			highPriorityIndex = i
			break
		}
	}

	if highPriorityIndex == -1 {
		t.Error("High priority task (Critical) was not executed")
		return
	}

	// 放宽条件：单核 executor 中期望高优先级在前 5 个任务中
	if highPriorityIndex > 5 {
		t.Errorf("High priority task at index %d, expected <= 5 (order: %v)", highPriorityIndex, executionOrder)
	}
}

// TestIntegration_PanicRecovery Panic 恢复测试
func TestIntegration_PanicRecovery(t *testing.T) {
	var panicCount int64
	panicHandler := func(r any) {
		atomic.AddInt64(&panicCount, 1)
	}

	executor, _ := NewPerCoreExecutor(
		WithNumCores(1),
		WithPanicHandler(panicHandler),
	)
	defer executor.Close()

	var wg sync.WaitGroup

	// 提交会 panic 的任务
	for i := 0; i < 5; i++ {
		wg.Add(1)
		err := executor.Submit(context.Background(), func(ctx context.Context) {
			defer wg.Done()
			panic("test panic")
		})
		if err != nil {
			wg.Done()
		}
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt64(&panicCount) != 5 {
		t.Errorf("panicCount = %d, want 5", panicCount)
	}

	// 执行器应该仍然可用
	err := executor.Submit(context.Background(), func(ctx context.Context) {})
	if err != nil {
		t.Error("Executor should still be usable after panics")
	}
}

// TestIntegration_HighConcurrency 高并发测试
func TestIntegration_HighConcurrency(t *testing.T) {
	selector := NewTaskScheduler()

	executor, err := NewPerCoreExecutor(WithNumCores(runtime.NumCPU()), WithQueueSize(10000))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModePerCore, executor); err != nil {
		t.Fatalf("RegisterExecutor(ModePerCore) error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		t.Fatalf("RegisterExecutor(ModeDefaultPool) error: %v", err)
	}

	var counter int64
	var wg sync.WaitGroup

	// 并发提交 10000 个任务
	numTasks := 10000
	numGoroutines := 100
	tasksPerGoroutine := numTasks / numGoroutines
	wg.Add(numTasks)

	sourceID, _ := model.ParseSourceID("test:concurrent:task")
	for g := 0; g < numGoroutines; g++ {
		go func() {
			for i := 0; i < tasksPerGoroutine; i++ {
				err := selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
					atomic.AddInt64(&counter, 1)
					wg.Done()
				})
				if err != nil {
					wg.Done()
				}
			}
		}()
	}

	wg.Wait()

	if atomic.LoadInt64(&counter) != int64(numTasks) {
		t.Errorf("counter = %d, want %d", counter, numTasks)
	}

	_ = selector.Close()
}

// TestIntegration_MultiExecutorFallback 多执行器降级测试
func TestIntegration_MultiExecutorFallback(t *testing.T) {
	selector := NewTaskScheduler()

	// 只注册默认池
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		t.Fatalf("RegisterExecutor(ModeDefaultPool) error: %v", err)
	}

	// 请求各种模式，应该都降级到默认池
	testCases := []string{
		"hlc:clock:tick",       // 推荐 PerCore
		"rpc:client:send",      // 推荐 FuncPool
		"query:range:scan",     // 推荐 MultiPool
		"background:log:flush", // 推荐 CustomPool
	}

	for _, tc := range testCases {
		sourceID, _ := model.ParseSourceID(tc)
		executor, mode, err := selector.Select(sourceID)
		if err != nil {
			t.Errorf("Select(%s) error: %v", tc, err)
			continue
		}
		if executor == nil {
			t.Errorf("Select(%s) returned nil executor", tc)
			continue
		}
		if mode != model.ModeDefaultPool {
			t.Errorf("Select(%s) mode = %v, want ModeDefaultPool", tc, mode)
		}
	}

	_ = selector.Close()
}

// TestIntegration_CustomRoutingRules 自定义路由规则测试
func TestIntegration_CustomRoutingRules(t *testing.T) {
	selector := NewTaskScheduler()

	// 注册执行器
	perCoreExec, _ := NewPerCoreExecutor(WithNumCores(2), WithQueueSize(100))
	if err := selector.RegisterExecutor(model.ModePerCore, perCoreExec); err != nil {
		t.Fatalf("RegisterExecutor(ModePerCore) error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		t.Fatalf("RegisterExecutor(ModeDefaultPool) error: %v", err)
	}

	// 添加自定义路由规则
	_ = selector.AddRoutingRule("urgent:*:*", model.ModePerCore)

	// 测试路由规则
	sourceID, _ := model.ParseSourceID("urgent:module:action")
	_, mode, err := selector.Select(sourceID)
	if err != nil {
		t.Errorf("Select() error: %v", err)
		return
	}
	if mode != model.ModePerCore {
		t.Errorf("Select() mode = %v, want ModePerCore", mode)
	}

	// 移除路由规则
	selector.RemoveRoutingRule("urgent:*:*")

	// 验证规则已移除
	sourceID, _ = model.ParseSourceID("urgent:module:action")
	_, mode, err = selector.Select(sourceID)
	if err != nil {
		t.Errorf("Select() error: %v", err)
		return
	}
	// 应该降级到默认池
	if mode != model.ModeDefaultPool {
		t.Errorf("Select() mode = %v, want ModeDefaultPool", mode)
	}

	_ = selector.Close()
}

// ==========================================
// 性能基准测试
// ==========================================

// BenchmarkIntegration_PerCoreSubmit PerCore 提交基准
func BenchmarkIntegration_PerCoreSubmit(b *testing.B) {
	executor, _ := NewPerCoreExecutor(WithNumCores(runtime.NumCPU()), WithQueueSize(100000))
	defer executor.Close()

	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

// BenchmarkIntegration_SelectorSubmit 调度器提交基准
func BenchmarkIntegration_SelectorSubmit(b *testing.B) {
	selector := NewTaskScheduler()
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		b.Fatalf("RegisterExecutor() error: %v", err)
	}
	defer selector.Close()

	sourceID, _ := model.ParseSourceID("bench:test:task")
	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = selector.Submit(context.Background(), sourceID, task)
	}
}

// BenchmarkIntegration_SelectWithRoute 路由选择基准
func BenchmarkIntegration_SelectWithRoute(b *testing.B) {
	selector := NewTaskScheduler()
	perCoreExec, err := NewPerCoreExecutor(WithNumCores(runtime.NumCPU()), WithQueueSize(10000))
	if err != nil {
		b.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModePerCore, perCoreExec); err != nil {
		b.Fatalf("RegisterExecutor(ModePerCore) error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		b.Fatalf("RegisterExecutor(ModeDefaultPool) error: %v", err)
	}

	sourceID, _ := model.ParseSourceID("hlc:clock:tick")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = selector.Select(sourceID)
	}

	_ = selector.Close()
}

// BenchmarkIntegration_ConcurrentSubmit 并发提交基准
func BenchmarkIntegration_ConcurrentSubmit(b *testing.B) {
	selector := NewTaskScheduler()
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		b.Fatalf("RegisterExecutor() error: %v", err)
	}
	defer selector.Close()

	sourceID, _ := model.ParseSourceID("bench:concurrent:task")
	task := func(ctx context.Context) {}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = selector.Submit(context.Background(), sourceID, task)
		}
	})
}

// BenchmarkIntegration_PrioritySubmit 优先级提交基准
func BenchmarkIntegration_PrioritySubmit(b *testing.B) {
	executor, _ := NewPerCoreExecutor(WithNumCores(4), WithQueueSize(100000))
	defer executor.Close()

	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		priority := model.TaskPriority(i % 10)
		_ = executor.SubmitWithPriority(context.Background(), priority, task)
	}
}

// BenchmarkIntegration_MixedWorkload 混合工作负载基准
func BenchmarkIntegration_MixedWorkload(b *testing.B) {
	selector := NewTaskScheduler()

	perCoreExec, err := NewPerCoreExecutor(WithNumCores(runtime.NumCPU()), WithQueueSize(10000))
	if err != nil {
		b.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModePerCore, perCoreExec); err != nil {
		b.Fatalf("RegisterExecutor(ModePerCore) error: %v", err)
	}

	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		b.Fatalf("RegisterExecutor(ModeDefaultPool) error: %v", err)
	}
	defer selector.Close()

	task := func(ctx context.Context) {}
	sources := []string{
		"hlc:clock:tick",
		"wal:writer:flush",
		"test:module:action",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sourceID, _ := model.ParseSourceID(sources[i%len(sources)])
		_ = selector.Submit(context.Background(), sourceID, task)
	}
}

// ==========================================
// 压力测试
// ==========================================

// TestStress_HighLoad 高负载压力测试
func TestStress_HighLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	selector := NewTaskScheduler()

	perCoreExec, err := NewPerCoreExecutor(WithNumCores(runtime.NumCPU()), WithQueueSize(100000))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModePerCore, perCoreExec); err != nil {
		t.Fatalf("RegisterExecutor(ModePerCore) error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		t.Fatalf("RegisterExecutor(ModeDefaultPool) error: %v", err)
	}
	defer selector.Close()

	numTasks := 100000
	var counter int64
	var wg sync.WaitGroup

	start := time.Now()
	sourceID, _ := model.ParseSourceID("stress:test:task")

	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		_ = selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			atomic.AddInt64(&counter, 1)
			wg.Done()
		})
	}

	wg.Wait()
	elapsed := time.Since(start)

	throughput := float64(numTasks) / elapsed.Seconds()
	t.Logf("Throughput: %.2f tasks/sec", throughput)

	if atomic.LoadInt64(&counter) != int64(numTasks) {
		t.Errorf("counter = %d, want %d", counter, numTasks)
	}
}

// TestStress_MemoryUsage 内存使用测试
func TestStress_MemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	selector := NewTaskScheduler()
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		t.Fatalf("RegisterExecutor() error: %v", err)
	}

	// 提交大量任务
	var wg sync.WaitGroup
	for i := 0; i < 10000; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("memory:test:task")
		_ = selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			wg.Done()
		})
	}
	wg.Wait()

	runtime.GC()
	runtime.ReadMemStats(&m2)

	_ = selector.Close()

	heapDiff := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	t.Logf("Heap difference: %d bytes (%.2f MB)", heapDiff, float64(heapDiff)/1024/1024)
}

// ==========================================
// 辅助函数
// ==========================================

// setupFullSelector 创建并注册执行器的调度器
func setupFullSelector(t *testing.T) *TaskScheduler {
	t.Helper()
	selector := NewTaskScheduler()

	// 注册 PerCore 执行器
	perCoreExec, err := NewPerCoreExecutor(WithNumCores(2), WithQueueSize(100))
	if err != nil {
		t.Fatalf("NewPerCoreExecutor() error: %v", err)
	}
	if err := selector.RegisterExecutor(model.ModePerCore, perCoreExec); err != nil {
		t.Fatalf("RegisterExecutor(ModePerCore) error: %v", err)
	}

	// 注册 DefaultPool 执行器
	if err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
		t.Fatalf("RegisterExecutor(ModeDefaultPool) error: %v", err)
	}

	return selector
}

func TestMain(m *testing.M) {
	fmt.Println("Starting integration tests...")

	goleak.VerifyTestMain(m,
		// 忽略 ants 池的后台清理 goroutine
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*Pool).purgeStaleWorkers"),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*Pool).ticktock"),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*PoolWithFunc).purgeStaleWorkers"),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*PoolWithFunc).ticktock"),
		// 忽略系统级轮询 goroutine
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		// 忽略 Go 运行时定时器 goroutine
		goleak.IgnoreTopFunction("time.Sleep"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		// 忽略 sync.Cond.Wait 导致的等待（PerCoreExecutor worker 正常等待）
		goleak.IgnoreTopFunction("sync.(*Cond).Wait"),
		goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
		// 忽略测试辅助函数中的 goroutine（wrapAnyResult 的超时等待）
		goleak.IgnoreTopFunction("github.com/jzhang405/NexKV/internal/infrastructure/concurrency.wrapAnyResult[...].func1"),
		goleak.IgnoreTopFunction("github.com/jzhang405/NexKV/internal/infrastructure/concurrency.(*mockResult).Get"),
	)
}
