// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// TaskSelector 测试
// ==========================================

func TestTaskSelector_NewSelector(t *testing.T) {
	selector := NewTaskSelector()
	if selector == nil {
		t.Fatal("NewTaskSelector() returned nil")
	}
}

func TestTaskSelector_RegisterExecutor(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	err := selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())
	if err != nil {
		t.Errorf("RegisterExecutor() error = %v", err)
	}

	// 重复注册应该失败
	err = selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())
	if err == nil {
		t.Error("RegisterExecutor() should fail for duplicate mode")
	}
}

func TestTaskSelector_Select(t *testing.T) {
	selector := NewTaskSelector()

	// 注册所有执行器
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeCustomPool, mustCreateAntsPoolExecutor(10))
	selector.RegisterExecutor(model.ModeFuncPool, mustCreateAntsFuncExecutor())
	selector.RegisterExecutor(model.ModeMultiPool, mustCreateAntsMultiExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	tests := []struct {
		sourceID     string
		expectedMode model.TaskMode
	}{
		// Per-Core 模式
		{"hlc:clock:tick", model.ModePerCore},
		{"wal:writer:flush", model.ModePerCore},

		// FuncPool 模式
		{"rpc:client:send", model.ModeFuncPool},

		// MultiPool 模式
		{"query:range:scan", model.ModeMultiPool},

		// CustomPool 模式
		{"background:log:flush", model.ModeCustomPool},

		// DefaultPool 模式
		{"unknown:module:action", model.ModeDefaultPool},
	}

	for _, tt := range tests {
		t.Run(tt.sourceID, func(t *testing.T) {
			sourceID, _ := model.ParseSourceID(tt.sourceID)
			executor, mode, err := selector.Select(sourceID)
			if err != nil {
				t.Errorf("Select() error = %v", err)
				return
			}
			if executor == nil {
				t.Error("Select() returned nil executor")
				return
			}
			if mode != tt.expectedMode {
				t.Errorf("Select() mode = %v, want %v", mode, tt.expectedMode)
			}
		})
	}
}

func TestTaskSelector_SelectByMode(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 选择存在的模式
	executor, err := selector.SelectByMode(model.ModePerCore)
	if err != nil {
		t.Errorf("SelectByMode() error = %v", err)
	}
	if executor == nil {
		t.Error("SelectByMode() returned nil executor")
	}

	// 选择不存在的模式
	_, err = selector.SelectByMode(model.ModeFuncPool)
	if err == nil {
		t.Error("SelectByMode() should return error for unregistered mode")
	}
}

func TestTaskSelector_SelectWithFallback(t *testing.T) {
	selector := NewTaskSelector()

	// 只注册默认池
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 请求 PerCore 模式，应该降级到默认池
	sourceID, _ := model.ParseSourceID("hlc:clock:tick")
	executor, mode, err := selector.Select(sourceID)
	if err != nil {
		t.Errorf("Select() error = %v", err)
	}
	if mode != model.ModeDefaultPool {
		t.Errorf("Select() mode = %v, want ModeDefaultPool (fallback)", mode)
	}
	if executor == nil {
		t.Error("Select() returned nil executor")
	}
}

func TestTaskSelector_Submit(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	var executed int32
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("test:module:action")
		err := selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			atomic.AddInt32(&executed, 1)
			wg.Done()
		})
		if err != nil {
			t.Errorf("Submit() error = %v", err)
			wg.Done()
		}
	}

	wg.Wait()

	if atomic.LoadInt32(&executed) != 10 {
		t.Errorf("executed = %d, want 10", executed)
	}
}

func TestTaskSelector_SubmitWithMode(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	selector.RegisterExecutor(model.ModeCustomPool, mustCreateAntsPoolExecutor(10))

	var executed int32
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		err := selector.SubmitWithMode(context.Background(), model.ModeCustomPool, func(ctx context.Context) {
			atomic.AddInt32(&executed, 1)
			wg.Done()
		})
		if err != nil {
			t.Errorf("SubmitWithMode() error = %v", err)
			wg.Done()
		}
	}

	wg.Wait()

	if atomic.LoadInt32(&executed) != 10 {
		t.Errorf("executed = %d, want 10", executed)
	}
}

func TestTaskSelector_AddRoutingRule(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 添加自定义路由规则
	err := selector.AddRoutingRule("custom:*:*", model.ModePerCore)
	if err != nil {
		t.Errorf("AddRoutingRule() error = %v", err)
	}

	// 验证路由规则生效
	sourceID, _ := model.ParseSourceID("custom:module:action")
	_, mode, err := selector.Select(sourceID)
	if err != nil {
		t.Errorf("Select() error = %v", err)
		return
	}
	if mode != model.ModePerCore {
		t.Errorf("Select() mode = %v, want ModePerCore", mode)
	}
}

func TestTaskSelector_RemoveRoutingRule(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 添加并移除路由规则
	selector.AddRoutingRule("temp:*:*", model.ModePerCore)
	selector.RemoveRoutingRule("temp:*:*")

	// 验证规则已移除
	sourceID, _ := model.ParseSourceID("temp:module:action")
	_, mode, err := selector.Select(sourceID)
	if err != nil {
		t.Errorf("Select() error = %v", err)
		return
	}
	// 应该降级到默认池（因为 PerCore 不是 temp 模块的推荐模式）
	if mode != model.ModeDefaultPool {
		t.Errorf("Select() mode = %v, want ModeDefaultPool", mode)
	}
}

func TestTaskSelector_Stats(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 提交任务
	for i := 0; i < 100; i++ {
		sourceID, _ := model.ParseSourceID("test:stats:task")
		selector.Submit(context.Background(), sourceID, func(ctx context.Context) {})
	}

	time.Sleep(50 * time.Millisecond)

	stats := selector.Stats()
	if stats.TotalSubmitted != 100 {
		t.Errorf("Stats().TotalSubmitted = %d, want 100", stats.TotalSubmitted)
	}
}

func TestTaskSelector_AvailableModes(t *testing.T) {
	selector := NewTaskSelector()

	// 注册部分执行器
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	modes := selector.AvailableModes()
	if len(modes) != 2 {
		t.Errorf("AvailableModes() returned %d modes, want 2", len(modes))
	}
}

func TestTaskSelector_HasMode(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	if !selector.HasMode(model.ModeDefaultPool) {
		t.Error("HasMode(ModeDefaultPool) should return true")
	}
	if selector.HasMode(model.ModePerCore) {
		t.Error("HasMode(ModePerCore) should return false")
	}
}

func TestTaskSelector_Close(t *testing.T) {
	selector := NewTaskSelector()

	// 注册多个执行器
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 关闭选择器
	err := selector.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// 关闭后提交应该失败
	sourceID, _ := model.ParseSourceID("test:module:action")
	err = selector.Submit(context.Background(), sourceID, func(ctx context.Context) {})
	if err == nil {
		t.Error("Submit() after Close() should fail")
	}
}

func TestTaskSelector_ConcurrentSelect(t *testing.T) {
	selector := NewTaskSelector()

	// 注册所有执行器
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	var wg sync.WaitGroup
	errors := make(chan error, 1000)

	// 并发选择
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sourceID, _ := model.ParseSourceID("test:concurrent:task")
			_, _, err := selector.Select(sourceID)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent Select() error: %v", err)
	}
}

// ==========================================
// 集成测试
// ==========================================

func TestTaskSelector_Integration_FullWorkflow(t *testing.T) {
	selector := NewTaskSelector()

	// 1. 注册所有执行器
	selector.RegisterExecutor(model.ModePerCore, mustCreatePerCoreExecutor())
	selector.RegisterExecutor(model.ModeCustomPool, mustCreateAntsPoolExecutor(10))
	selector.RegisterExecutor(model.ModeFuncPool, mustCreateAntsFuncExecutor())
	selector.RegisterExecutor(model.ModeMultiPool, mustCreateAntsMultiExecutor())
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 2. 提交不同类型的任务
	var counter int64
	var wg sync.WaitGroup

	// HLC 任务（PerCore）
	for i := 0; i < 10; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("hlc:clock:tick")
		selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			atomic.AddInt64(&counter, 1)
			wg.Done()
		})
	}

	// RPC 任务（FuncPool）
	for i := 0; i < 10; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("rpc:client:send")
		selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			atomic.AddInt64(&counter, 1)
			wg.Done()
		})
	}

	// 后台任务（CustomPool）
	for i := 0; i < 10; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("background:log:flush")
		selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			atomic.AddInt64(&counter, 1)
			wg.Done()
		})
	}

	wg.Wait()

	if atomic.LoadInt64(&counter) != 30 {
		t.Errorf("counter = %d, want 30", counter)
	}

	// 3. 验证统计
	stats := selector.Stats()
	if stats.TotalSubmitted != 30 {
		t.Errorf("Stats().TotalSubmitted = %d, want 30", stats.TotalSubmitted)
	}

	// 4. 关闭
	selector.Close()
}

func TestTaskSelector_Integration_GracefulShutdown(t *testing.T) {
	selector := NewTaskSelector()

	// 注册执行器
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	// 提交一些任务
	var completed int32
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		sourceID, _ := model.ParseSourceID("test:shutdown:task")
		selector.Submit(context.Background(), sourceID, func(ctx context.Context) {
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&completed, 1)
			wg.Done()
		})
	}

	// 等待任务完成
	wg.Wait()

	if atomic.LoadInt32(&completed) != 100 {
		t.Errorf("completed = %d, want 100", completed)
	}

	// 关闭
	selector.Close()
}

// ==========================================
// 辅助函数
// ==========================================

func mustCreatePerCoreExecutor() *PerCoreExecutor {
	e, err := NewPerCoreExecutor(WithNumCores(2), WithQueueSize(100))
	if err != nil {
		panic(err)
	}
	return e
}

func mustCreateAntsPoolExecutor(capacity int) *AntsPoolExecutor {
	e, err := NewAntsPoolExecutor(capacity)
	if err != nil {
		panic(err)
	}
	return e
}

func mustCreateAntsFuncExecutor() *AntsFuncExecutor {
	e, err := NewAntsFuncExecutor(10, func(i interface{}) {})
	if err != nil {
		panic(err)
	}
	return e
}

func mustCreateAntsMultiExecutor() *AntsMultiExecutor {
	e, err := NewAntsMultiExecutor(2, 10)
	if err != nil {
		panic(err)
	}
	return e
}

// ==========================================
// 基准测试
// ==========================================

func BenchmarkTaskSelector_Select(b *testing.B) {
	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	sourceID, _ := model.ParseSourceID("bench:test:task")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		selector.Select(sourceID)
	}
}

func BenchmarkTaskSelector_Submit(b *testing.B) {
	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	sourceID, _ := model.ParseSourceID("bench:test:task")
	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		selector.Submit(context.Background(), sourceID, task)
	}
}

func BenchmarkTaskSelector_ConcurrentSubmit(b *testing.B) {
	selector := NewTaskSelector()
	selector.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor())

	sourceID, _ := model.ParseSourceID("bench:concurrent:task")
	task := func(ctx context.Context) {}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			selector.Submit(context.Background(), sourceID, task)
		}
	})
}
