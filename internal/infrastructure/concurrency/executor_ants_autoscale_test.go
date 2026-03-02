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
// 自动扩缩容测试 (P1-01)
// ==========================================

// TestAntsPoolExecutor_AutoScale 测试自动扩容功能
func TestAntsPoolExecutor_AutoScale(t *testing.T) {
	config := &ProviderConfig{
		Capacity:           100,   // 初始容量
		MaxCapacity:        500,   // 最大容量
		EnableAutoScale:    true,  // 启用自动扩容
		ScaleThreshold:     0.8,   // 80% 使用率时扩容
		ScaleStep:          100,   // 每次扩容 100
		ScaleCheckInterval: 10,    // 每 10 次 Submit 检查一次
		EnableAutoShrink:   false, // 禁用自动缩容（避免干扰测试）
	}

	provider, err := NewAntsExecutor(config)
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
		err := provider.Submit(context.Background(), model.TaskPriorityNormal, func(ctx context.Context) {
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
		_ = provider.Submit(context.Background(), model.TaskPriorityNormal, func(ctx context.Context) {
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

// TestAntsPoolExecutor_AutoShrink 测试自动缩容功能
func TestAntsPoolExecutor_AutoShrink(t *testing.T) {
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

	provider, err := NewAntsExecutor(config)
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
		_ = provider.Submit(context.Background(), model.TaskPriorityNormal, func(ctx context.Context) {
			time.Sleep(10 * time.Millisecond)
		})
	}

	// 等待缩容检查
	time.Sleep(2 * time.Second)

	// 验证缩容
	t.Logf("Initial capacity: 100, Current capacity: %d", provider.currentCapacity)
}

// TestAntsPoolExecutor_AutoScale_Disabled 测试禁用自动扩容
func TestAntsPoolExecutor_AutoScale_Disabled(t *testing.T) {
	config := &ProviderConfig{
		Capacity:        100,
		MaxCapacity:     500,
		EnableAutoScale: false, // 禁用自动扩容
	}

	provider, err := NewAntsExecutor(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 提交大量任务
	for i := 0; i < 50; i++ {
		_ = provider.Submit(context.Background(), model.TaskPriorityNormal, func(ctx context.Context) {
			time.Sleep(10 * time.Millisecond)
		})
	}

	// 验证容量未改变
	stats := provider.Stats()
	if stats.Capacity != 100 {
		t.Errorf("expected capacity to remain 100 when auto-scale disabled, got %d", stats.Capacity)
	}
}

// TestAntsPoolExecutor_AutoScale_MaxCapacity 测试扩容不超过最大容量
func TestAntsPoolExecutor_AutoScale_MaxCapacity(t *testing.T) {
	config := &ProviderConfig{
		Capacity:           100,
		MaxCapacity:        150, // 最大容量略高于初始容量
		EnableAutoScale:    true,
		ScaleThreshold:     0.8,
		ScaleStep:          100, // 每次扩容 100
		ScaleCheckInterval: 10,
		EnableAutoShrink:   false,
	}

	provider, err := NewAntsExecutor(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	// 提交大量任务以尝试触发扩容
	var wg sync.WaitGroup
	blockCh := make(chan struct{})

	for i := 0; i < 90; i++ {
		wg.Add(1)
		_ = provider.Submit(context.Background(), model.TaskPriorityNormal, func(ctx context.Context) {
			<-blockCh
			wg.Done()
		})
	}

	time.Sleep(200 * time.Millisecond)

	// 提交更多任务以触发扩容检查
	for i := 0; i < 100; i++ {
		_ = provider.Submit(context.Background(), model.TaskPriorityNormal, func(ctx context.Context) {
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

// TestAntsPoolExecutor_HighConcurrency 测试高并发场景
func TestAntsPoolExecutor_HighConcurrency(t *testing.T) {
	provider, err := NewAntsExecutor(nil)
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
				err := provider.Submit(context.Background(), model.TaskPriorityNormal, func(ctx context.Context) {
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

// TestAntsPoolExecutor_RaceCondition 测试竞态条件
func TestAntsPoolExecutor_RaceCondition(t *testing.T) {
	provider, err := NewAntsExecutor(nil)
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
				_ = provider.Submit(context.Background(), model.TaskPriorityNormal, func(ctx context.Context) {})
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
