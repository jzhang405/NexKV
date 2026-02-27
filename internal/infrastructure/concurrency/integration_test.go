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
)

// ==========================================
// 集成测试
// ==========================================

// TestIntegration_FullWorkflow 完整工作流集成测试
func TestIntegration_FullWorkflow(t *testing.T) {
	// 1. 创建选择器
	selector := NewTaskSelector()

	// 2. 注册所有执行器
	perCoreExec, _ := NewPerCoreExecutor(WithNumCores(2), WithQueueSize(100))
	selector.RegisterExecutor(model.ModePerCore, perCoreExec)

	customPoolExec, _ := NewAntsPoolExecutor(10)
	selector.RegisterExecutor(model.ModeCustomPool, customPoolExec)

	funcPoolExec, _ := NewAntsFuncExecutor(10, func(i interface{}) {})
	selector.RegisterExecutor(model.ModeFuncPool, funcPoolExec)

	multiPoolExec, _ := NewAntsMultiExecutor(2, 10)
	selector.RegisterExecutor(model.ModeMultiPool, multiPoolExec)

	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 3. 提交不同类型的任务
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

	// 4. 验证统计
	stats := selector.Stats()
	if stats.TotalSubmitted != int64(totalTasks) {
		t.Errorf("Stats().TotalSubmitted = %d, want %d", stats.TotalSubmitted, totalTasks)
	}

	// 5. 关闭
	selector.Close()
}

// TestIntegration_GracefulShutdown 优雅关闭测试
func TestIntegration_GracefulShutdown(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	executor, _ := NewPerCoreExecutor(WithNumCores(2), WithQueueSize(1000))
	selector.RegisterExecutor(model.ModePerCore, executor)

	// 提交大量任务
	var started, completed int64
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("test:shutdown:task")
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
	selector.Close()
}

// TestIntegration_PriorityOrdering 优先级顺序测试
func TestIntegration_PriorityOrdering(t *testing.T) {
	executor, _ := NewPerCoreExecutor(WithNumCores(1), WithQueueSize(100))
	defer executor.Close()

	var executionOrder []int
	var mu sync.Mutex

	// 先提交低优先级任务
	for i := 0; i < 5; i++ {
		executor.SubmitWithPriority(context.Background(), 1, func(ctx context.Context) {
			mu.Lock()
			executionOrder = append(executionOrder, 1)
			mu.Unlock()
		})
	}

	// 提交高优先级任务
	executor.SubmitWithPriority(context.Background(), 10, func(ctx context.Context) {
		mu.Lock()
		executionOrder = append(executionOrder, 10)
		mu.Unlock()
	})

	time.Sleep(100 * time.Millisecond)

	// 验证高优先级任务较早执行
	mu.Lock()
	defer mu.Unlock()

	highPriorityIndex := -1
	for i, priority := range executionOrder {
		if priority == 10 {
			highPriorityIndex = i
			break
		}
	}

	if highPriorityIndex == -1 {
		t.Error("High priority task was not executed")
		return
	}

	// 高优先级任务应该在前面
	if highPriorityIndex > 3 {
		t.Errorf("High priority task at index %d, expected earlier", highPriorityIndex)
	}
}

// TestIntegration_RateLimiting 限流测试
func TestIntegration_RateLimiting(t *testing.T) {
	// 创建带限流的执行器
	executor, _ := NewPerCoreExecutor(
		WithNumCores(1),
		WithQueueSize(1000),
		WithRateLimit(1000, 100), // 1000 QPS, burst 100
	)
	defer executor.Close()

	var accepted, rejected int64
	var wg sync.WaitGroup

	// 快速提交任务
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := executor.Submit(context.Background(), func(ctx context.Context) {})
			if err == ErrRateLimitExceeded {
				atomic.AddInt64(&rejected, 1)
			} else if err == nil {
				atomic.AddInt64(&accepted, 1)
			}
		}()
	}

	wg.Wait()

	// 应该有部分任务被限流
	t.Logf("Accepted: %d, Rejected: %d", accepted, rejected)
}

// TestIntegration_PanicRecovery Panic 恢复测试
func TestIntegration_PanicRecovery(t *testing.T) {
	var panicCount int64
	panicHandler := func(r interface{}) {
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
	selector := NewTaskSelector()

	// 注册执行器
	executor, _ := NewPerCoreExecutor(WithNumCores(runtime.NumCPU()), WithQueueSize(10000))
	selector.RegisterExecutor(model.ModePerCore, executor)
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	var counter int64
	var wg sync.WaitGroup

	// 并发提交 10000 个任务
	numTasks := 10000
	numGoroutines := 100
	tasksPerGoroutine := numTasks / numGoroutines

	for g := 0; g < numGoroutines; g++ {
		go func() {
			for i := 0; i < tasksPerGoroutine; i++ {
				wg.Add(1)
				sourceID, _ := model.ParseSourceID("test:concurrent:task")
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

	selector.Close()
}

// TestIntegration_MultiExecutorFallback 多执行器降级测试
func TestIntegration_MultiExecutorFallback(t *testing.T) {
	selector := NewTaskSelector()

	// 只注册默认池
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 请求各种模式，应该都降级到默认池
	testCases := []string{
		"hlc:clock:tick",     // 推荐 PerCore
		"rpc:client:send",    // 推荐 FuncPool
		"query:range:scan",   // 推荐 MultiPool
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

	selector.Close()
}

// TestIntegration_CustomRoutingRules 自定义路由规则测试
func TestIntegration_CustomRoutingRules(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	perCoreExec, _ := NewPerCoreExecutor(WithNumCores(2), WithQueueSize(100))
	selector.RegisterExecutor(model.ModePerCore, perCoreExec)
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 添加自定义路由规则
	selector.AddRoutingRule("urgent:*:*", model.ModePerCore)

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

	selector.Close()
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
		executor.Submit(context.Background(), task)
	}
}

// BenchmarkIntegration_SelectorSubmit 选择器提交基准
func BenchmarkIntegration_SelectorSubmit(b *testing.B) {
	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())
	defer selector.Close()

	sourceID, _ := model.ParseSourceID("bench:test:task")
	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		selector.Submit(context.Background(), sourceID, task)
	}
}

// BenchmarkIntegration_SelectWithRoute 路由选择基准
func BenchmarkIntegration_SelectWithRoute(b *testing.B) {
	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	sourceID, _ := model.ParseSourceID("hlc:clock:tick")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		selector.Select(sourceID)
	}

	selector.Close()
}

// BenchmarkIntegration_ConcurrentSubmit 并发提交基准
func BenchmarkIntegration_ConcurrentSubmit(b *testing.B) {
	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())
	defer selector.Close()

	sourceID, _ := model.ParseSourceID("bench:concurrent:task")
	task := func(ctx context.Context) {}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			selector.Submit(context.Background(), sourceID, task)
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
		executor.SubmitWithPriority(context.Background(), i%10, task)
	}
}

// BenchmarkIntegration_MixedWorkload 混合工作负载基准
func BenchmarkIntegration_MixedWorkload(b *testing.B) {
	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeCustomPool, mustCreateAntsPoolExecutor(100))
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())
	defer selector.Close()

	task := func(ctx context.Context) {}

	sources := []string{
		"hlc:clock:tick",
		"rpc:client:send",
		"background:log:flush",
		"test:module:action",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sourceID, _ := model.ParseSourceID(sources[i%len(sources)])
		selector.Submit(context.Background(), sourceID, task)
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

	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())
	defer selector.Close()

	numTasks := 100000
	var counter int64
	var wg sync.WaitGroup

	start := time.Now()

	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("stress:test:task")
		selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
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

	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 提交大量任务
	var wg sync.WaitGroup
	for i := 0; i < 10000; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("memory:test:task")
		selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			wg.Done()
		})
	}
	wg.Wait()

	runtime.GC()
	runtime.ReadMemStats(&m2)

	selector.Close()

	heapDiff := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	t.Logf("Heap difference: %d bytes (%.2f MB)", heapDiff, float64(heapDiff)/1024/1024)
}

// ==========================================
// 辅助函数
// ==========================================

func TestMain(m *testing.M) {
	// 设置测试环境
	fmt.Println("Starting integration tests...")
	m.Run()
}
