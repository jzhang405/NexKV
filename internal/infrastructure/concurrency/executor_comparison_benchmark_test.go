// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// ==========================================
// 全局对比测试：PerCore vs Ants 各模式
// ==========================================

// 模拟实际工作负载的任务
func simulateWork(workTime time.Duration) func(context.Context) {
	return func(ctx context.Context) {
		start := time.Now()
		for time.Since(start) < workTime {
			// CPU 密集型计算
			_ = fmt.Sprintf("work-%d", time.Now().UnixNano())
		}
	}
}

// 统计任务完成数
type statsCounter struct {
	completed atomic.Int64
}

func (s *statsCounter) increment() {
	s.completed.Add(1)
}

func (s *statsCounter) get() int64 {
	return s.completed.Load()
}

// ==========================================
// FuncPool 专用数据结构
// ==========================================

// workItem FuncPool 的任务数据结构
type workItem struct {
	id     int
	result atomic.Int64
}

// workHandler 默认工作负载处理器（100μs）
func workHandler(arg any) {
	if item, ok := arg.(*workItem); ok {
		start := time.Now()
		for time.Since(start) < 100*time.Microsecond {
			_ = fmt.Sprintf("work-%d", item.id)
		}
		item.result.Add(1)
	}
}

// ==========================================
// Benchmark 1: PerCoreExecutor (CPU 绑核)
// ==========================================

func Benchmark_PerCore_Affinity(b *testing.B) {
	executor, err := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(100000),
		WithEnableAffinity(true),
	)
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	// Warm-up: 确保所有 worker 完成绑核
	for i := 0; i < runtime.NumCPU()*10; i++ {
		_ = executor.Submit(context.Background(), simulateWork(100*time.Microsecond))
	}
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()

	stats := &statsCounter{}

	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			simulateWork(100 * time.Microsecond)(ctx)
			stats.increment()
		})
	}

	// 等待所有任务完成
	time.Sleep(1 * time.Second)
	b.ReportMetric(float64(stats.get()), "tasks")
}

// ==========================================
// Benchmark 2: Ants Default Pool (Mode 2)
// ==========================================

func Benchmark_Ants_Default(b *testing.B) {
	executor := NewAntsDefaultExecutor()
	defer executor.Close()

	b.ResetTimer()
	b.ReportAllocs()

	stats := &statsCounter{}

	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			simulateWork(100 * time.Microsecond)(ctx)
			stats.increment()
		})
	}

	// 等待所有任务完成
	time.Sleep(1 * time.Second)
	b.ReportMetric(float64(stats.get()), "tasks")
}

// ==========================================
// Benchmark 3: Ants Custom Pool (Mode 3)
// ==========================================

func Benchmark_Ants_CustomPool(b *testing.B) {
	executor, err := NewAntsPoolExecutor(runtime.NumCPU() * 4)
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	b.ResetTimer()
	b.ReportAllocs()

	stats := &statsCounter{}

	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			simulateWork(100 * time.Microsecond)(ctx)
			stats.increment()
		})
	}

	// 等待所有任务完成
	time.Sleep(1 * time.Second)
	b.ReportMetric(float64(stats.get()), "tasks")
}

// ==========================================
// Benchmark 4: Ants Func Pool (Mode 4)
// ==========================================

// 4.1 错误用法：使用 Submit 接口（不推荐）
func Benchmark_Ants_FuncPool(b *testing.B) {
	handler := func(arg any) {
		if ft, ok := arg.(*funcTask); ok {
			simulateWork(100 * time.Microsecond)(ft.ctx)
		}
	}

	executor, err := NewAntsFuncExecutor(runtime.NumCPU()*4, handler)
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	b.ResetTimer()
	b.ReportAllocs()

	stats := &statsCounter{}

	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			simulateWork(100 * time.Microsecond)(ctx)
			stats.increment()
		})
	}

	// 等待所有任务完成
	time.Sleep(1 * time.Second)
	b.ReportMetric(float64(stats.get()), "tasks")
}

// 4.2 正确用法：使用 Invoke 接口（推荐）
func Benchmark_AntsFuncPool_Invoke(b *testing.B) {
	executor, err := NewAntsFuncExecutor(runtime.NumCPU()*4, workHandler)
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	// Warm-up
	for i := 0; i < runtime.NumCPU()*10; i++ {
		item := &workItem{id: i}
		_ = executor.Invoke(context.Background(), item)
	}
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()

	var completed atomic.Int64

	for i := 0; i < b.N; i++ {
		item := &workItem{id: i}
		_ = executor.Invoke(context.Background(), item)
		completed.Add(1)
	}

	// 等待所有任务完成
	time.Sleep(1 * time.Second)
	b.ReportMetric(float64(completed.Load()), "tasks")
}

// ==========================================
// Benchmark 5: Ants Multi Pool (Mode 5)
// ==========================================

func Benchmark_Ants_MultiPool(b *testing.B) {
	executor, err := NewAntsMultiExecutor(4, runtime.NumCPU())
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	b.ResetTimer()
	b.ReportAllocs()

	stats := &statsCounter{}

	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			simulateWork(100 * time.Microsecond)(ctx)
			stats.increment()
		})
	}

	// 等待所有任务完成
	time.Sleep(1 * time.Second)
	b.ReportMetric(float64(stats.get()), "tasks")
}

// ==========================================
// 并行对比测试
// ==========================================

func Benchmark_Parallel_PerCore_Affinity(b *testing.B) {
	executor, err := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(100000),
		WithEnableAffinity(true),
	)
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	// Warm-up
	for i := 0; i < runtime.NumCPU()*10; i++ {
		_ = executor.Submit(context.Background(), simulateWork(100*time.Microsecond))
	}
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), simulateWork(100*time.Microsecond))
		}
	})
}

func Benchmark_Parallel_Ants_Default(b *testing.B) {
	executor := NewAntsDefaultExecutor()
	defer executor.Close()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), simulateWork(100*time.Microsecond))
		}
	})
}

func Benchmark_Parallel_Ants_CustomPool(b *testing.B) {
	executor, err := NewAntsPoolExecutor(runtime.NumCPU() * 4)
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), simulateWork(100*time.Microsecond))
		}
	})
}

func Benchmark_Parallel_Ants_FuncPool(b *testing.B) {
	handler := func(arg any) {
		if ft, ok := arg.(*funcTask); ok {
			simulateWork(100 * time.Microsecond)(ft.ctx)
		}
	}

	executor, err := NewAntsFuncExecutor(runtime.NumCPU()*4, handler)
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), simulateWork(100*time.Microsecond))
		}
	})
}

func Benchmark_Parallel_AntsFuncPool_Invoke(b *testing.B) {
	executor, err := NewAntsFuncExecutor(runtime.NumCPU()*4, workHandler)
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		id := 0
		for pb.Next() {
			item := &workItem{id: id}
			_ = executor.Invoke(context.Background(), item)
			id++
		}
	})
}

func Benchmark_Parallel_Ants_MultiPool(b *testing.B) {
	executor, err := NewAntsMultiExecutor(4, runtime.NumCPU())
	if err != nil {
		b.Fatalf("Failed to create executor: %v", err)
	}
	defer executor.Close()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), simulateWork(100*time.Microsecond))
		}
	})
}

// ==========================================
// 不同工作负载的对比
// ==========================================

// 短任务 (10μs)
func benchmark_ShortTask(b *testing.B, executor TaskExecutor) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			start := time.Now()
			for time.Since(start) < 10*time.Microsecond {
				_ = i
			}
		})
	}
}

// 中等任务 (100μs)
func benchmark_MediumTask(b *testing.B, executor TaskExecutor) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			start := time.Now()
			for time.Since(start) < 100*time.Microsecond {
				_ = fmt.Sprintf("work-%d", i)
			}
		})
	}
}

// 长任务 (1ms)
func benchmark_LongTask(b *testing.B, executor TaskExecutor) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			start := time.Now()
			for time.Since(start) < 1*time.Millisecond {
				_ = fmt.Sprintf("work-%d-%d", i, time.Now().UnixNano())
			}
		})
	}
}

// 基于工作负载的对比测试 - PerCore
func Benchmark_PerCore_ShortTask(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(100000),
		WithEnableAffinity(true),
	)
	defer executor.Close()
	benchmark_ShortTask(b, executor)
}

func Benchmark_PerCore_MediumTask(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(100000),
		WithEnableAffinity(true),
	)
	defer executor.Close()
	benchmark_MediumTask(b, executor)
}

func Benchmark_PerCore_LongTask(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(100000),
		WithEnableAffinity(true),
	)
	defer executor.Close()
	benchmark_LongTask(b, executor)
}

// 基于工作负载的对比测试 - Ants Default
func Benchmark_AntsDefault_ShortTask(b *testing.B) {
	executor := NewAntsDefaultExecutor()
	defer executor.Close()
	benchmark_ShortTask(b, executor)
}

func Benchmark_AntsDefault_MediumTask(b *testing.B) {
	executor := NewAntsDefaultExecutor()
	defer executor.Close()
	benchmark_MediumTask(b, executor)
}

func Benchmark_AntsDefault_LongTask(b *testing.B) {
	executor := NewAntsDefaultExecutor()
	defer executor.Close()
	benchmark_LongTask(b, executor)
}

// 基于工作负载的对比测试 - Ants CustomPool
func Benchmark_AntsCustomPool_ShortTask(b *testing.B) {
	executor, _ := NewAntsPoolExecutor(runtime.NumCPU() * 4)
	defer executor.Close()
	benchmark_ShortTask(b, executor)
}

func Benchmark_AntsCustomPool_MediumTask(b *testing.B) {
	executor, _ := NewAntsPoolExecutor(runtime.NumCPU() * 4)
	defer executor.Close()
	benchmark_MediumTask(b, executor)
}

func Benchmark_AntsCustomPool_LongTask(b *testing.B) {
	executor, _ := NewAntsPoolExecutor(runtime.NumCPU() * 4)
	defer executor.Close()
	benchmark_LongTask(b, executor)
}

// 基于工作负载的对比测试 - Ants FuncPool (Submit - 不推荐)
func Benchmark_AntsFuncPool_ShortTask(b *testing.B) {
	handler := func(arg any) {
		if _, ok := arg.(*funcTask); ok {
			start := time.Now()
			for time.Since(start) < 10*time.Microsecond {
				_ = time.Now().UnixNano()
			}
		}
	}
	executor, _ := NewAntsFuncExecutor(runtime.NumCPU()*4, handler)
	defer executor.Close()
	benchmark_ShortTask(b, executor)
}

func Benchmark_AntsFuncPool_MediumTask(b *testing.B) {
	handler := func(arg any) {
		if _, ok := arg.(*funcTask); ok {
			start := time.Now()
			for time.Since(start) < 100*time.Microsecond {
				_ = fmt.Sprintf("work-%d", time.Now().UnixNano())
			}
		}
	}
	executor, _ := NewAntsFuncExecutor(runtime.NumCPU()*4, handler)
	defer executor.Close()
	benchmark_MediumTask(b, executor)
}

func Benchmark_AntsFuncPool_LongTask(b *testing.B) {
	handler := func(arg any) {
		if _, ok := arg.(*funcTask); ok {
			start := time.Now()
			for time.Since(start) < 1*time.Millisecond {
				_ = fmt.Sprintf("work-%d", time.Now().UnixNano())
			}
		}
	}
	executor, _ := NewAntsFuncExecutor(runtime.NumCPU()*4, handler)
	defer executor.Close()
	benchmark_LongTask(b, executor)
}

// 基于工作负载的对比测试 - Ants FuncPool (Invoke - 推荐)
func Benchmark_AntsFuncPool_Invoke_ShortTask(b *testing.B) {
	shortHandler := func(arg any) {
		if _, ok := arg.(*workItem); ok {
			start := time.Now()
			for time.Since(start) < 10*time.Microsecond {
				_ = time.Now().UnixNano()
			}
		}
	}
	executor, _ := NewAntsFuncExecutor(runtime.NumCPU()*4, shortHandler)
	defer executor.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item := &workItem{id: i}
		_ = executor.Invoke(context.Background(), item)
	}
}

func Benchmark_AntsFuncPool_Invoke_MediumTask(b *testing.B) {
	executor, _ := NewAntsFuncExecutor(runtime.NumCPU()*4, workHandler)
	defer executor.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item := &workItem{id: i}
		_ = executor.Invoke(context.Background(), item)
	}
}

func Benchmark_AntsFuncPool_Invoke_LongTask(b *testing.B) {
	longHandler := func(arg any) {
		if _, ok := arg.(*workItem); ok {
			start := time.Now()
			for time.Since(start) < 1*time.Millisecond {
				_ = fmt.Sprintf("work-%d", time.Now().UnixNano())
			}
		}
	}
	executor, _ := NewAntsFuncExecutor(runtime.NumCPU()*4, longHandler)
	defer executor.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item := &workItem{id: i}
		_ = executor.Invoke(context.Background(), item)
	}
}

// 基于工作负载的对比测试 - Ants MultiPool
func Benchmark_AntsMultiPool_ShortTask(b *testing.B) {
	executor, _ := NewAntsMultiExecutor(4, runtime.NumCPU())
	defer executor.Close()
	benchmark_ShortTask(b, executor)
}

func Benchmark_AntsMultiPool_MediumTask(b *testing.B) {
	executor, _ := NewAntsMultiExecutor(4, runtime.NumCPU())
	defer executor.Close()
	benchmark_MediumTask(b, executor)
}

func Benchmark_AntsMultiPool_LongTask(b *testing.B) {
	executor, _ := NewAntsMultiExecutor(4, runtime.NumCPU())
	defer executor.Close()
	benchmark_LongTask(b, executor)
}
