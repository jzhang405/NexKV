//go:build !darwin

// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPerCoreExecutor_CPUAffinity 测试 CPU 绑核功能
// PerCore 总是启用绑核（在支持的平台上）
func TestPerCoreExecutor_CPUAffinity(t *testing.T) {
	if !isAffinitySupported() {
		t.Skip("CPU affinity not supported on this platform")
	}

	// 创建执行器（PerCore 总是启用绑核）
	numCores := runtime.NumCPU()
	executor, err := NewPerCoreExecutor(
		WithNumCores(numCores),
		WithQueueSize(100),
	)
	require.NoError(t, err)
	require.NotNil(t, executor)
	defer executor.Close()

	// 验证配置
	config := executor.Config()
	assert.Equal(t, numCores, config.NumCores, "should use all available cores")

	// 提交任务验证执行
	var executed int32
	for i := 0; i < 100; i++ {
		err := executor.Submit(context.Background(), func(ctx context.Context) {
			atomic.AddInt32(&executed, 1)
		})
		assert.NoError(t, err)
	}

	// 等待任务完成
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(100), atomic.LoadInt32(&executed), "all tasks should be executed")
}

// TestPerCoreExecutor_DefaultAffinity 测试默认绑核行为
func TestPerCoreExecutor_DefaultAffinity(t *testing.T) {
	// 创建使用默认配置的执行器
	executor, err := NewPerCoreExecutor(WithNumCores(2))
	require.NoError(t, err)
	require.NotNil(t, executor)
	defer executor.Close()

	// 验证默认行为
	config := executor.Config()
	// PerCore 总是启用绑核（在支持的平台上会实际绑核）
	assert.Equal(t, 2, config.NumCores, "should use configured cores")
}

// BenchmarkPerCoreExecutor_WithAffinity 绑核性能基准测试
func BenchmarkPerCoreExecutor_WithAffinity(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(100000),
	)
	defer executor.Close()

	task := func(ctx context.Context) {}

	// Warm-up: 确保所有 worker 都已完成初始化和绑核
	// 提交足够多的任务让所有 worker 都启动并完成绑核
	for i := 0; i < runtime.NumCPU()*10; i++ {
		_ = executor.Submit(context.Background(), task)
	}

	// 等待 warm-up 任务完成
	time.Sleep(100 * time.Millisecond)

	// 重置计时器，现在开始测量真实的绑核后性能
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = executor.Submit(context.Background(), task)
	}
}

// BenchmarkPerCoreExecutor_SimulatedWorkload 绑核 + 模拟真实工作负载
func BenchmarkPerCoreExecutor_SimulatedWorkload(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(100000),
	)
	defer executor.Close()

	// 模拟 HLC 时钟更新（轻量级计算）
	task := func(ctx context.Context) {
		// 模拟一些内存操作和简单计算
		counter := 0
		for i := 0; i < 10; i++ {
			counter += i
		}
		_ = counter
	}

	// Warm-up
	for i := 0; i < runtime.NumCPU()*100; i++ {
		_ = executor.Submit(context.Background(), task)
	}
	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), task)
		}
	})
}

// BenchmarkPerCoreExecutor_MemoryIntensive 绑核 + 内存密集型任务
func BenchmarkPerCoreExecutor_MemoryIntensive(b *testing.B) {
	if !isAffinitySupported() {
		b.Skip("CPU affinity not supported on this platform")
	}

	executor, _ := NewPerCoreExecutor(
		WithNumCores(runtime.NumCPU()),
		WithQueueSize(100000),
	)
	defer executor.Close()

	// 模拟内存密集型任务（如 WAL 缓冲区操作）
	var data [1024]int
	task := func(ctx context.Context) {
		// 模拟内存访问
		for i := 0; i < len(data); i += 64 {
			data[i] = i * 2
		}
	}

	// Warm-up
	for i := 0; i < runtime.NumCPU()*100; i++ {
		_ = executor.Submit(context.Background(), task)
	}
	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = executor.Submit(context.Background(), task)
		}
	})
}
