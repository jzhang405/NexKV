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
// PerCoreExecutor vs AntsDefaultExecutor 性能对比
// 场景：Transport 和 RPC 工作负载
// ==========================================

// 模拟 Transport 场景：网络 I/O 密集型任务
func simulateTransportTask(ctx context.Context) {
	// 模拟网络延迟（微秒级）
	start := time.Now()
	for time.Since(start) < 10*time.Microsecond {
		// 模拟网络等待
		runtime.Gosched()
	}

	// 模拟数据序列化/反序列化
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// 模拟校验和计算
	checksum := uint32(0)
	for _, b := range data {
		checksum += uint32(b)
	}
	_ = checksum
}

// 模拟 RPC 场景：请求-响应处理
func simulateRPCHandler(ctx context.Context) {
	// 模拟请求解析
	reqData := make([]byte, 512)
	for i := range reqData {
		reqData[i] = byte(i)
	}

	// 模拟业务逻辑处理（轻量计算）
	result := 0
	for _, v := range reqData[:100] {
		result += int(v)
	}

	// 模拟响应构建
	respData := make([]byte, 256)
	for i := range respData {
		respData[i] = byte(result % 256)
	}
	_ = respData
}

// 模拟 RPC 客户端场景
func simulateRPCClient(ctx context.Context) {
	// 模拟请求构建
	req := make(map[string]string)
	req["method"] = "GET"
	req["key"] = "test-key"
	req["timestamp"] = time.Now().String()

	// 模拟序列化
	data := fmt.Sprintf("%v", req)

	// 模拟网络等待
	time.Sleep(5 * time.Microsecond)

	// 模拟响应解析
	_ = len(data)
}

// BenchmarkPerCoreVsAnts_Transport_Throughput Transport 吞吐量对比
func BenchmarkPerCoreVsAnts_Transport_Throughput(b *testing.B) {
	benchmarks := []struct {
		name       string
		createExec func() TaskExecutor
	}{
		{
			name: "PerCore",
			createExec: func() TaskExecutor {
				exec, _ := NewPerCoreExecutor(
					WithNumCores(runtime.NumCPU()),
					WithQueueSize(10000),
				)
				return exec
			},
		},
		{
			name: "AntsDefault",
			createExec: func() TaskExecutor {
				return NewAntsDefaultExecutor()
			},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			exec := bm.createExec()
			if closer, ok := exec.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			ctx := context.Background()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_ = exec.Submit(ctx, simulateTransportTask)
				}
			})
		})
	}
}

// BenchmarkPerCoreVsAnts_RPC_Server RPC 服务器吞吐量对比
func BenchmarkPerCoreVsAnts_RPC_Server(b *testing.B) {
	benchmarks := []struct {
		name       string
		createExec func() TaskExecutor
	}{
		{
			name: "PerCore",
			createExec: func() TaskExecutor {
				exec, _ := NewPerCoreExecutor(
					WithNumCores(runtime.NumCPU()),
					WithQueueSize(10000),
				)
				return exec
			},
		},
		{
			name: "AntsDefault",
			createExec: func() TaskExecutor {
				return NewAntsDefaultExecutor()
			},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			exec := bm.createExec()
			if closer, ok := exec.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			ctx := context.Background()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_ = exec.Submit(ctx, simulateRPCHandler)
				}
			})
		})
	}
}

// BenchmarkPerCoreVsAnts_RPC_Client RPC 客户端吞吐量对比
func BenchmarkPerCoreVsAnts_RPC_Client(b *testing.B) {
	benchmarks := []struct {
		name       string
		createExec func() TaskExecutor
	}{
		{
			name: "PerCore",
			createExec: func() TaskExecutor {
				exec, _ := NewPerCoreExecutor(
					WithNumCores(runtime.NumCPU()),
					WithQueueSize(10000),
				)
				return exec
			},
		},
		{
			name: "AntsDefault",
			createExec: func() TaskExecutor {
				return NewAntsDefaultExecutor()
			},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			exec := bm.createExec()
			if closer, ok := exec.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			ctx := context.Background()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_ = exec.Submit(ctx, simulateRPCClient)
				}
			})
		})
	}
}

// BenchmarkPerCoreVsAnts_Latency 任务提交延迟对比
func BenchmarkPerCoreVsAnts_Latency(b *testing.B) {
	benchmarks := []struct {
		name       string
		createExec func() TaskExecutor
	}{
		{
			name: "PerCore",
			createExec: func() TaskExecutor {
				exec, _ := NewPerCoreExecutor(
					WithNumCores(runtime.NumCPU()),
					WithQueueSize(10000),
				)
				return exec
			},
		},
		{
			name: "AntsDefault",
			createExec: func() TaskExecutor {
				return NewAntsDefaultExecutor()
			},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			exec := bm.createExec()
			if closer, ok := exec.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			ctx := context.Background()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = exec.Submit(ctx, func(ctx context.Context) {
					// 极简任务，只测试提交延迟
				})
			}
		})
	}
}

// BenchmarkPerCoreVsAnts_MixedWorkload 混合工作负载对比
func BenchmarkPerCoreVsAnts_MixedWorkload(b *testing.B) {
	benchmarks := []struct {
		name       string
		createExec func() TaskExecutor
	}{
		{
			name: "PerCore",
			createExec: func() TaskExecutor {
				exec, _ := NewPerCoreExecutor(
					WithNumCores(runtime.NumCPU()),
					WithQueueSize(10000),
				)
				return exec
			},
		},
		{
			name: "AntsDefault",
			createExec: func() TaskExecutor {
				return NewAntsDefaultExecutor()
			},
		},
	}

	tasks := []func(context.Context){
		simulateTransportTask,
		simulateRPCHandler,
		simulateRPCClient,
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			exec := bm.createExec()
			if closer, ok := exec.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			ctx := context.Background()
			b.ResetTimer()

			taskIdx := uint32(0)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					idx := atomic.AddUint32(&taskIdx, 1) - 1
					task := tasks[idx%uint32(len(tasks))]
					_ = exec.Submit(ctx, task)
				}
			})
		})
	}
}

// BenchmarkPerCoreVsAnts_ConcurrentTransport 并发 Transport 任务对比
func BenchmarkPerCoreVsAnts_ConcurrentTransport(b *testing.B) {
	benchmarks := []struct {
		name       string
		createExec func() TaskExecutor
	}{
		{
			name: "PerCore",
			createExec: func() TaskExecutor {
				exec, _ := NewPerCoreExecutor(
					WithNumCores(runtime.NumCPU()),
					WithQueueSize(10000),
				)
				return exec
			},
		},
		{
			name: "AntsDefault",
			createExec: func() TaskExecutor {
				return NewAntsDefaultExecutor()
			},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			exec := bm.createExec()
			if closer, ok := exec.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			ctx := context.Background()

			// 预先提交大量任务，测试高并发场景
			var wg sync.WaitGroup
			taskCount := 10000
			completed := atomic.Int64{}

			b.ResetTimer()

			for i := 0; i < taskCount; i++ {
				wg.Add(1)
				_ = exec.Submit(ctx, func(ctx context.Context) {
					defer wg.Done()
					simulateTransportTask(ctx)
					completed.Add(1)
				})
			}

			wg.Wait()

			// 验证所有任务完成
			if completed.Load() != int64(taskCount) {
				b.Fatalf("Expected %d completed, got %d", taskCount, completed.Load())
			}
		})
	}
}

// BenchmarkPerCoreVsAnts_SchedulerOverhead 调度器开销对比
func BenchmarkPerCoreVsAnts_SchedulerOverhead(b *testing.B) {
	b.Run("WithScheduler", func(b *testing.B) {
		scheduler := NewTaskScheduler()

		// 注册 PerCore 执行器
		perCoreExec, _ := NewPerCoreExecutor(
			WithNumCores(runtime.NumCPU()),
			WithQueueSize(10000),
		)
		if err := scheduler.RegisterExecutor(model.ModePerCore, perCoreExec); err != nil {
			b.Fatalf("Failed to register PerCore executor: %v", err)
		}

		// 注册 DefaultPool 执行器
		if err := scheduler.RegisterExecutor(model.ModeDefaultPool, NewAntsDefaultExecutor()); err != nil {
			b.Fatalf("Failed to register DefaultPool executor: %v", err)
		}

		defer scheduler.Close()

		ctx := context.Background()
		sourceID, _ := model.ParseSourceID("hlc:clock:tick")

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = scheduler.Submit(ctx, sourceID, simulateTransportTask)
		}
	})

	b.Run("DirectPerCore", func(b *testing.B) {
		exec, _ := NewPerCoreExecutor(
			WithNumCores(runtime.NumCPU()),
			WithQueueSize(10000),
		)
		defer exec.Close()

		ctx := context.Background()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = exec.Submit(ctx, simulateTransportTask)
		}
	})
}

// BenchmarkPerCoreVsAnts_PriorityScheduling 优先级调度对比
func BenchmarkPerCoreVsAnts_PriorityScheduling(b *testing.B) {
	benchmarks := []struct {
		name       string
		createExec func() TaskExecutor
	}{
		{
			name: "PerCore",
			createExec: func() TaskExecutor {
				exec, _ := NewPerCoreExecutor(
					WithNumCores(runtime.NumCPU()),
					WithQueueSize(10000),
				)
				return exec
			},
		},
		{
			name: "AntsDefault",
			createExec: func() TaskExecutor {
				return NewAntsDefaultExecutor()
			},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			exec := bm.createExec()
			if closer, ok := exec.(interface{ Close() error }); ok {
				defer closer.Close()
			}

			ctx := context.Background()

			// 测试不同优先级任务的提交延迟
			highPriorityCount := atomic.Int64{}
			normalPriorityCount := atomic.Int64{}

			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				taskNum := 0
				for pb.Next() {
					taskNum++
					isHighPriority := taskNum%10 == 0 // 10% 高优先级

					_ = exec.Submit(ctx, func(ctx context.Context) {
						if isHighPriority {
							highPriorityCount.Add(1)
						} else {
							normalPriorityCount.Add(1)
						}
						simulateRPCHandler(ctx)
					})
				}
			})
		})
	}
}
