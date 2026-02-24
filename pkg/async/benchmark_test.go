// Package async 提供异步操作性能基准测试
package async

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/concurrency"
)

// ==========================================
// AsyncOperation 性能基准测试
// ==========================================

// BenchmarkAsyncOp_Create 测试 AsyncOperation 创建性能
func BenchmarkAsyncOp_Create(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewOp(ctx, provider, func(ctx context.Context) (string, error) {
			return "result", nil
		})
	}
}

// BenchmarkAsyncOp_CreateAndGet 测试创建并获取结果性能
func BenchmarkAsyncOp_CreateAndGet(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := NewOp(ctx, provider, func(ctx context.Context) (string, error) {
			return "result", nil
		})
		_, _ = op.Get(ctx)
	}
}

// BenchmarkAsyncOp_WithCallback 测试带回调的异步操作性能
func BenchmarkAsyncOp_WithCallback(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	var counter atomic.Int64

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := NewOp(ctx, provider, func(ctx context.Context) (string, error) {
			return "result", nil
		})
		op.OnComplete(func(value string, err error) {
			counter.Add(1)
		})
		_, _ = op.Get(ctx)
	}
}

// BenchmarkAsyncOp_WithTimeout 测试带超时的异步操作性能
func BenchmarkAsyncOp_WithTimeout(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := NewOp(ctx, provider, func(ctx context.Context) (string, error) {
			return "result", nil
		}, WithTimeout(1*time.Second))
		_, _ = op.Get(ctx)
	}
}

// BenchmarkAsyncOp_Concurrent 测试并发创建和执行性能
func BenchmarkAsyncOp_Concurrent(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			op := NewOp(ctx, provider, func(ctx context.Context) (string, error) {
				return "result", nil
			})
			_, _ = op.Get(ctx)
		}
	})
}

// ==========================================
// AsyncGroup 性能基准测试
// ==========================================

// BenchmarkAsyncGroup_Create 测试 AsyncGroup 创建性能
func BenchmarkAsyncGroup_Create(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 5)
	for i := 0; i < 5; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewGroup(ctx, provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return "result", nil
		})
	}
}

// BenchmarkAsyncGroup_WaitAll 测试 WaitAll 性能
func BenchmarkAsyncGroup_WaitAll(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 5)
	for i := 0; i < 5; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return "result", nil
		})
		_ = group.WaitAll(ctx)
	}
}

// BenchmarkAsyncGroup_WaitMajority 测试 WaitMajority 性能
func BenchmarkAsyncGroup_WaitMajority(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 5)
	for i := 0; i < 5; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return "result", nil
		})
		_ = group.WaitMajority(ctx)
	}
}

// BenchmarkAsyncGroup_WaitAny 测试 WaitAny 性能
func BenchmarkAsyncGroup_WaitAny(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 5)
	for i := 0; i < 5; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return "result", nil
		})
		_, _, _ = group.WaitAny(ctx)
	}
}

// BenchmarkAsyncGroup_SmallCluster 测试小规模集群性能 (5 节点)
func BenchmarkAsyncGroup_SmallCluster(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 100,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 5)
	for i := 0; i < 5; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			// 模拟 1ms 网络延迟
			time.Sleep(1 * time.Millisecond)
			return "result", nil
		})
		_ = group.WaitAll(ctx)
	}
}

// BenchmarkAsyncGroup_MediumCluster 测试中等规模集群性能 (15 节点)
func BenchmarkAsyncGroup_MediumCluster(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 100,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 15)
	for i := 0; i < 15; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			// 模拟 1ms 网络延迟
			time.Sleep(1 * time.Millisecond)
			return "result", nil
		})
		_ = group.WaitAll(ctx)
	}
}

// BenchmarkAsyncGroup_LargeCluster 测试大规模集群性能 (50 节点)
func BenchmarkAsyncGroup_LargeCluster(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 200,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 50)
	for i := 0; i < 50; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			// 模拟 1ms 网络延迟
			time.Sleep(1 * time.Millisecond)
			return "result", nil
		})
		_ = group.WaitAll(ctx)
	}
}

// ==========================================
// GoroutineProvider 性能基准测试
// ==========================================

// BenchmarkGoroutineProvider_Submit 测试任务提交性能
func BenchmarkGoroutineProvider_Submit(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	var counter atomic.Int64

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = provider.Submit(ctx, func(ctx context.Context) {
			counter.Add(1)
		})
	}
}

// BenchmarkGoroutineProvider_SubmitWithArg 测试带参数提交性能
func BenchmarkGoroutineProvider_SubmitWithArg(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = provider.SubmitWithArg(ctx, func(ctx context.Context, arg any) {
			_ = arg.(int) * 2
		}, i)
	}
}

// BenchmarkGoroutineProvider_ParallelSubmit 测试并行提交性能
func BenchmarkGoroutineProvider_ParallelSubmit(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = provider.Submit(ctx, func(ctx context.Context) {
				// 空操作
			})
		}
	})
}

// ==========================================
// 内存分配基准测试
// ==========================================

// BenchmarkAsyncOp_MemoryAllocation 测试 AsyncOperation 内存分配
func BenchmarkAsyncOp_MemoryAllocation(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		op := NewOp(ctx, provider, func(ctx context.Context) (string, error) {
			return "result", nil
		})
		_, _ = op.Get(ctx)
	}
}

// BenchmarkAsyncGroup_MemoryAllocation 测试 AsyncGroup 内存分配
func BenchmarkAsyncGroup_MemoryAllocation(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 1000,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 5)
	for i := 0; i < 5; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, provider, targets, func(ctx context.Context, target model.PeerID) (string, error) {
			return "result", nil
		})
		_ = group.WaitAll(ctx)
	}
}

// ==========================================
// 吞吐量基准测试
// ==========================================

// BenchmarkAsyncOp_Throughput 测试 AsyncOperation 吞吐量
func BenchmarkAsyncOp_Throughput(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 10000,
	})
	defer provider.Close()

	// 预创建操作以减少测试中的分配
	ops := make([]AsyncOperation[string], b.N)
	for i := 0; i < b.N; i++ {
		ops[i] = NewOp(ctx, provider, func(ctx context.Context) (string, error) {
			return "result", nil
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ops[i].Get(ctx)
	}
}

// BenchmarkAsyncGroup_Throughput 测试 AsyncGroup 吞吐量
func BenchmarkAsyncGroup_Throughput(b *testing.B) {
	ctx := context.Background()
	provider, _ := concurrency.NewAntsGoroutineProvider(&concurrency.ProviderConfig{
		Capacity: 10000,
	})
	defer provider.Close()

	targets := make([]model.PeerID, 5)
	for i := 0; i < 5; i++ {
		targets[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	execFunc := func(ctx context.Context, target model.PeerID) (string, error) {
		return "result", nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		group := NewGroup(ctx, provider, targets, execFunc)
		_ = group.WaitAll(ctx)
	}
}
