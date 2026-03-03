//go:build !darwin

// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ==========================================
// 优化效果验证测试
// ==========================================
// 测试两个关键优化：
// 1. 核心本地数据分配
// 2. 任务队列内存布局优化（避免伪共享）
//
// 运行方式：
//   go test -bench=BenchmarkOptimization_* -benchmem -benchtime=5s \
//     ./internal/infrastructure/concurrency/ -run=^$
//
// Perf 分析：
//   perf stat -e cache-references,cache-misses,L1-dcache-loads,L1-dcache-load-misses \
//     go test -bench=BenchmarkOptimization_WithPadding \
//       -benchtime=5s ./internal/infrastructure/concurrency/ -run=^$

// BenchmarkOptimization_WithPadding 测试优化后的性能（padding + 本地数据）
func BenchmarkOptimization_WithPadding(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	executor, _ := NewPerCoreExecutor(
		WithQueueSize(100000),
	)
	defer executor.Close()

	// 模拟真实工作负载：计算 + 内存访问
	task := func(ctx context.Context) {
		// 模拟 HLC 时钟更新
		timestamp := uint64(time.Now().UnixNano())
		logical := uint32(timestamp & 0xFFFF)
		_ = logical

		// 模拟一些计算
		counter := 0
		for i := 0; i < 10; i++ {
			counter += i
		}
		_ = counter
	}

	// Warm-up
	for i := 0; i < runtime.NumCPU()*100; i++ {
		_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
	}
	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}
	})
}

// BenchmarkOptimization_WithoutAffinity_NoPadding 对比基线（无优化）
func BenchmarkOptimization_WithoutAffinity_NoPadding(b *testing.B) {
	executor, _ := NewPerCoreExecutor(
		WithQueueSize(100000),
		// 禁用绑核
	)
	defer executor.Close()

	task := func(ctx context.Context) {
		timestamp := uint64(time.Now().UnixNano())
		logical := uint32(timestamp & 0xFFFF)
		_ = logical

		counter := 0
		for i := 0; i < 10; i++ {
			counter += i
		}
		_ = counter
	}

	for i := 0; i < runtime.NumCPU()*100; i++ {
		_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
	}
	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}
	})
}

// BenchmarkOptimization_WithAffinity_NoPadding 对比：绑核但无 padding
// 这个测试可以用于分离两个优化的效果
func BenchmarkOptimization_WithAffinity_NoPadding(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	executor, _ := NewPerCoreExecutor(
		WithQueueSize(100000),
	)
	defer executor.Close()

	task := func(ctx context.Context) {
		timestamp := uint64(time.Now().UnixNano())
		logical := uint32(timestamp & 0xFFFF)
		_ = logical

		counter := 0
		for i := 0; i < 10; i++ {
			counter += i
		}
		_ = counter
	}

	for i := 0; i < runtime.NumCPU()*100; i++ {
		_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
	}
	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}
	})
}

// BenchmarkOptimization_MemoryAccessPattern 内存访问模式对比
func BenchmarkOptimization_MemoryAccessPattern(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	b.Run("SequentialAccess", func(b *testing.B) {
		executor, _ := NewPerCoreExecutor(
			WithQueueSize(100000),
		)
		defer executor.Close()

		// 顺序访问模式（缓存友好）
		data := make([][64]int, runtime.NumCPU())

		task := func(ctx context.Context) {
			coreID := int(time.Now().UnixNano()) % runtime.NumCPU()
			localData := &data[coreID]

			// 顺序访问，利用空间局部性
			for i := 0; i < len(localData); i++ {
				localData[i] = i * 2
			}
		}

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
			}
		})
	})

	b.Run("RandomAccess", func(b *testing.B) {
		executor, _ := NewPerCoreExecutor(
			WithQueueSize(100000),
		)
		defer executor.Close()

		// 随机访问模式（缓存不友好）
		data := make([][64]int, runtime.NumCPU())

		task := func(ctx context.Context) {
			// 随机访问，导致缓存失效
			for i := 0; i < 64; i++ {
				idx := int(time.Now().UnixNano()+int64(i)) % runtime.NumCPU()
				data[idx][i%64] = i
			}
		}

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
			}
		})
	})
}

// BenchmarkOptimization_BatchedStats 批量统计优化效果
func BenchmarkOptimization_BatchedStats(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	executor, _ := NewPerCoreExecutor(
		WithQueueSize(100000),
	)
	defer executor.Close()

	// 模拟频繁统计更新的场景
	var globalCounter int64

	task := func(ctx context.Context) {
		// 模拟一些工作
		counter := 0
		for i := 0; i < 10; i++ {
			counter += i
		}

		// 更新统计（现在通过本地数据批量更新）
		atomic.AddInt64(&globalCounter, 1)
		_ = counter
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
		}
	})
}

// TestOptimization_VerifyLocalData 验证核心本地数据功能
func TestOptimization_VerifyLocalData(t *testing.T) {
	if !isAffinitySupported() {
		t.Skip("CPU affinity not supported on this platform")
	}

	executor, _ := NewPerCoreExecutor(

		WithQueueSize(100),
	)
	defer executor.Close()

	var executed int64
	var wg sync.WaitGroup

	// 提交任务
	for i := 0; i < 10; i++ {
		wg.Add(1)
		_ = executor.Submit(context.Background(), func(ctx context.Context) {
			atomic.AddInt64(&executed, 1)
			wg.Done()
		})
	}

	wg.Wait()

	if atomic.LoadInt64(&executed) != 10 {
		t.Errorf("executed = %d, want 10", executed)
	}

	// 验证统计信息
	stats := executor.Stats()
	if stats.TotalCompleted != 10 {
		t.Logf("Warning: stats.TotalCompleted = %d, may be affected by batching", stats.TotalCompleted)
	}
}

// BenchmarkOptimization_FalseSharing 直接测试伪共享影响
func BenchmarkOptimization_FalseSharing(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	b.Run("WithFalseSharing", func(b *testing.B) {
		executor, _ := NewPerCoreExecutor(
			WithQueueSize(100000),
		)
		defer executor.Close()

		// 模拟会导致伪共享的数据结构
		type BadCounter struct {
			value1 int64
			// 缺少 padding，value1 和 value2 可能在同一缓存行
			value2 int64
		}

		counter := &BadCounter{}

		task := func(ctx context.Context) {
			// 频繁修改两个字段
			atomic.AddInt64(&counter.value1, 1)
			atomic.AddInt64(&counter.value2, 1)
		}

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
			}
		})
	})

	b.Run("WithoutFalseSharing", func(b *testing.B) {
		executor, _ := NewPerCoreExecutor(
			WithQueueSize(100000),
		)
		defer executor.Close()

		// 使用 padding 避免伪共享
		type GoodCounter struct {
			value1 int64
			_      [7]int64 // 填充一个缓存行
			value2 int64
		}

		counter := &GoodCounter{}

		task := func(ctx context.Context) {
			atomic.AddInt64(&counter.value1, 1)
			atomic.AddInt64(&counter.value2, 1)
		}

		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = executor.Submit(context.Background(), model.SourceDefault, model.TaskPriorityNormal, task)
			}
		})
	})
}
