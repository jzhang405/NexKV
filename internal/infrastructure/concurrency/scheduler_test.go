// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// MockExecutor 模拟执行器
type MockExecutor struct {
	submitCount int64
	closed      bool
	mu          sync.Mutex
}

func (m *MockExecutor) Submit(ctx context.Context, task func(context.Context)) error {
	if m.closed {
		return errors.New("executor closed")
	}
	atomic.AddInt64(&m.submitCount, 1)
	go task(ctx)
	return nil
}

func (m *MockExecutor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *MockExecutor) SubmitCount() int64 {
	return atomic.LoadInt64(&m.submitCount)
}

func TestNewTaskScheduler(t *testing.T) {
	coordinator := NewTaskScheduler()
	if coordinator == nil {
		t.Fatal("NewTaskScheduler() returned nil")
	}
}

func TestTaskScheduler_RegisterExecutor(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 注册执行器
	executor := &MockExecutor{}
	err := coordinator.RegisterExecutor(model.ModeDefaultPool, executor)
	if err != nil {
		t.Errorf("RegisterExecutor() error = %v", err)
	}

	// 重复注册应该失败
	err = coordinator.RegisterExecutor(model.ModeDefaultPool, executor)
	if err == nil {
		t.Error("RegisterExecutor() should fail for duplicate mode")
	}
}

func TestTaskScheduler_GetExecutor(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 未注册时应该返回 nil
	executor := coordinator.GetExecutor(model.ModeDefaultPool)
	if executor != nil {
		t.Error("GetExecutor() should return nil for unregistered mode")
	}

	// 注册后应该能获取
	mockExecutor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, mockExecutor); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	executor = coordinator.GetExecutor(model.ModeDefaultPool)
	if executor == nil {
		t.Error("GetExecutor() returned nil after registration")
	}
}

func TestTaskScheduler_Submit(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 注册默认执行器
	defaultExecutor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, defaultExecutor); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	// 注册 PerCore 执行器
	perCoreExecutor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModePerCore, perCoreExecutor); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	tests := []struct {
		name         string
		sourceID     string
		expectedMode model.TaskMode
	}{
		{"hlc task", "hlc:clock:tick", model.ModePerCore},
		{"wal task", "wal:writer:flush", model.ModePerCore},
		{"background task", "background:log:flush", model.ModeCustomPool},
		{"unknown task", "unknown:module:action", model.ModeDefaultPool},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceID, _ := model.ParseSourceID(tt.sourceID)

			// 重置计数器
			atomic.StoreInt64(&defaultExecutor.submitCount, 0)
			atomic.StoreInt64(&perCoreExecutor.submitCount, 0)

			executed := make(chan struct{})
			err := coordinator.Submit(context.Background(), sourceID, func(ctx context.Context) {
				close(executed)
			})

			if err != nil {
				// 如果是未注册模式，应该降级到默认池
				if tt.expectedMode != model.ModeDefaultPool {
					t.Errorf("Submit() error = %v", err)
				}
				return
			}

			// 等待任务执行
			select {
			case <-executed:
			case <-time.After(time.Second):
				t.Error("task did not execute within timeout")
			}
		})
	}
}

func TestTaskScheduler_SubmitWithMode(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 注册执行器
	defaultExecutor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, defaultExecutor); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	customExecutor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeCustomPool, customExecutor); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	// 显式指定模式
	err := coordinator.SubmitWithMode(context.Background(), model.ModeCustomPool, func(ctx context.Context) {})
	if err != nil {
		t.Errorf("SubmitWithMode() error = %v", err)
	}

	// 验证使用了正确的执行器
	if customExecutor.SubmitCount() != 1 {
		t.Errorf("CustomExecutor.SubmitCount() = %d, want 1", customExecutor.SubmitCount())
	}

	if defaultExecutor.SubmitCount() != 0 {
		t.Errorf("DefaultExecutor.SubmitCount() = %d, want 0", defaultExecutor.SubmitCount())
	}
}

func TestTaskScheduler_SubmitUnregisteredMode(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 只注册默认执行器
	defaultExecutor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, defaultExecutor); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	sourceID, _ := model.ParseSourceID("hlc:clock:tick")

	// 尝试使用未注册的 PerCore 模式
	// 应该自动降级到默认池
	err := coordinator.Submit(context.Background(), sourceID, func(ctx context.Context) {})
	if err != nil {
		t.Errorf("Submit() should fallback to default pool, error = %v", err)
	}

	// 验证使用了默认执行器
	if defaultExecutor.SubmitCount() != 1 {
		t.Errorf("DefaultExecutor.SubmitCount() = %d, want 1", defaultExecutor.SubmitCount())
	}
}

func TestTaskScheduler_Close(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 注册多个执行器
	executor1 := &MockExecutor{}
	executor2 := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, executor1); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}
	if err := coordinator.RegisterExecutor(model.ModeCustomPool, executor2); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	// 关闭调度器
	err := coordinator.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// 验证所有执行器都已关闭
	if !executor1.closed {
		t.Error("executor1 should be closed")
	}
	if !executor2.closed {
		t.Error("executor2 should be closed")
	}

	// 关闭后提交应该失败
	sourceID, _ := model.ParseSourceID("test:module:action")
	err = coordinator.Submit(context.Background(), sourceID, func(ctx context.Context) {})
	if err == nil {
		t.Error("Submit() after Close() should fail")
	}
}

func TestTaskScheduler_Route(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 注册所有模式的执行器，以便测试路由逻辑
	if err := coordinator.RegisterExecutor(model.ModePerCore, &MockExecutor{}); err != nil {
		t.Fatalf("RegisterExecutor(ModePerCore) error = %v", err)
	}
	if err := coordinator.RegisterExecutor(model.ModeFuncPool, &MockExecutor{}); err != nil {
		t.Fatalf("RegisterExecutor(ModeFuncPool) error = %v", err)
	}
	if err := coordinator.RegisterExecutor(model.ModeMultiPool, &MockExecutor{}); err != nil {
		t.Fatalf("RegisterExecutor(ModeMultiPool) error = %v", err)
	}
	if err := coordinator.RegisterExecutor(model.ModeCustomPool, &MockExecutor{}); err != nil {
		t.Fatalf("RegisterExecutor(ModeCustomPool) error = %v", err)
	}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, &MockExecutor{}); err != nil {
		t.Fatalf("RegisterExecutor(ModeDefaultPool) error = %v", err)
	}

	tests := []struct {
		sourceID     string
		expectedMode model.TaskMode
	}{
		// Per-Core 模式
		{"hlc:clock:tick", model.ModePerCore},
		{"wal:writer:flush", model.ModePerCore},
		{"transpose:matrix:compute", model.ModePerCore},
		{"replication:sync:send", model.ModePerCore},

		// FuncPool 模式
		{"rpc:client:send", model.ModeFuncPool},
		{"rpc:server:handle", model.ModeFuncPool},

		// MultiPool 模式
		{"query:range:scan", model.ModeMultiPool},
		{"query:point:get", model.ModeMultiPool},

		// CustomPool 模式
		{"background:log:flush", model.ModeCustomPool},
		{"background:metric:collect", model.ModeCustomPool},

		// DefaultPool 模式
		{"test:temp:task", model.ModeDefaultPool},
		{"unknown:module:action", model.ModeDefaultPool},
	}

	for _, tt := range tests {
		t.Run(tt.sourceID, func(t *testing.T) {
			sourceID, _ := model.ParseSourceID(tt.sourceID)
			mode := coordinator.Route(sourceID)
			if mode != tt.expectedMode {
				t.Errorf("Route(%s) = %v, want %v", tt.sourceID, mode, tt.expectedMode)
			}
		})
	}
}

func TestTaskScheduler_ConcurrentSubmit(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 注册执行器
	executor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, executor); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	sourceID, _ := model.ParseSourceID("test:concurrent:task")

	// 并发提交 1000 个任务
	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = coordinator.Submit(context.Background(), sourceID, func(ctx context.Context) {})
		}()
	}
	wg.Wait()

	// 等待所有任务执行完成
	time.Sleep(100 * time.Millisecond)

	// 验证所有任务都被提交
	if executor.SubmitCount() != 1000 {
		t.Errorf("SubmitCount() = %d, want 1000", executor.SubmitCount())
	}
}

func TestTaskScheduler_SetDefaultMode(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 设置默认模式
	coordinator.SetDefaultMode(model.ModeCustomPool)

	// 验证路由时使用设置的默认模式
	sourceID, _ := model.ParseSourceID("unknown:module:action")
	mode := coordinator.Route(sourceID)
	if mode != model.ModeCustomPool {
		t.Errorf("Route() = %v, want %v", mode, model.ModeCustomPool)
	}
}

func TestTaskScheduler_AddRoutingRule(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 添加自定义路由规则
	err := coordinator.AddRoutingRule("custom:*:*", model.ModePerCore)
	if err != nil {
		t.Errorf("AddRoutingRule() error = %v", err)
	}

	// 验证路由规则生效
	sourceID, _ := model.ParseSourceID("custom:module:action")
	mode := coordinator.Route(sourceID)
	if mode != model.ModePerCore {
		t.Errorf("Route() = %v, want %v", mode, model.ModePerCore)
	}
}

func TestTaskScheduler_Stats(t *testing.T) {
	coordinator := NewTaskScheduler()

	// 注册执行器
	executor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, executor); err != nil {
		t.Fatalf("RegisterExecutor() error = %v", err)
	}

	// 提交任务
	sourceID, _ := model.ParseSourceID("test:stats:task")
	for i := 0; i < 100; i++ {
		_ = coordinator.Submit(context.Background(), sourceID, func(ctx context.Context) {})
	}

	// 获取统计信息
	stats := coordinator.Stats()
	if stats.TotalSubmitted != 100 {
		t.Errorf("Stats().TotalSubmitted = %d, want 100", stats.TotalSubmitted)
	}
}

// 基准测试
func BenchmarkTaskScheduler_Submit(b *testing.B) {
	coordinator := NewTaskScheduler()
	executor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, executor); err != nil {
		b.Fatalf("RegisterExecutor() error = %v", err)
	}

	sourceID, _ := model.ParseSourceID("bench:test:task")
	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = coordinator.Submit(context.Background(), sourceID, task)
	}
}

func BenchmarkTaskScheduler_Route(b *testing.B) {
	coordinator := NewTaskScheduler()
	sourceID, _ := model.ParseSourceID("bench:test:task")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		coordinator.Route(sourceID)
	}
}

func BenchmarkTaskScheduler_ConcurrentSubmit(b *testing.B) {
	coordinator := NewTaskScheduler()
	executor := &MockExecutor{}
	if err := coordinator.RegisterExecutor(model.ModeDefaultPool, executor); err != nil {
		b.Fatalf("RegisterExecutor() error = %v", err)
	}

	sourceID, _ := model.ParseSourceID("bench:concurrent:task")
	task := func(ctx context.Context) {}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = coordinator.Submit(context.Background(), sourceID, task)
		}
	})
}
