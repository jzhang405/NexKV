// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件测试性能基准
package hooks

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== 基准测试 ====================

// BenchmarkHook_Disabled 测试 Hook 禁用时的延迟增加
func BenchmarkHook_Disabled(b *testing.B) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	// 不启用 hook

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hook.OnGossipWrite("key", []byte("value"))
	}
}

// BenchmarkHook_Enabled 测试 Hook 启用时的延迟增加（同步模式）
func BenchmarkHook_Enabled(b *testing.B) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		opID := hook.OnGossipWrite("key", []byte("value"))
		hook.OnGossipReturn(opID, true, "")
	}
}

// BenchmarkHook_Enabled_Async 测试 Hook 启用时的延迟增加（异步模式）
func BenchmarkHook_Enabled_Async(b *testing.B) {
	recorder := newTestRecorder()
	config := porcupine.AsyncRecordConfig{
		Enabled:    true,
		BufferSize: 10000,
		DropOnFull: true,
	}
	hook := NewGossipHook(recorder, "node-1", config)
	hook.SetEnabled(true)
	hook.Start()
	defer hook.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hook.OnGossipWrite("key", []byte("value"))
	}
}

// BenchmarkVerify_1000Ops 测试 1000 ops 验证时间
func BenchmarkVerify_1000Ops(b *testing.B) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	// 预先记录 1000 个操作
	for i := 0; i < 1000; i++ {
		opID := hook.OnGossipWrite(fmt.Sprintf("key-%d", i), []byte("value"))
		hook.OnGossipReturn(opID, true, "")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ops := recorder.GetTopologyOperations()
		_ = ops
	}
}

// BenchmarkAsyncProcessor_Enqueue 测试入队吞吐量
func BenchmarkAsyncProcessor_Enqueue(b *testing.B) {
	var processedCount int

	processFunc := func(op AsyncOp) {
		processedCount++
	}

	config := porcupine.AsyncRecordConfig{
		Enabled:    true,
		BufferSize: 100000,
		DropOnFull: true,
	}

	processor := NewAsyncProcessor(config, processFunc)
	processor.Start()
	defer processor.Stop()

	op := AsyncOp{OpType: AsyncOpTypeCall, CallTime: time.Now().UnixNano()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		processor.Enqueue(op)
	}
}

// BenchmarkAsyncProcessor_Enqueue_Sync 测试同步模式吞吐量
func BenchmarkAsyncProcessor_Enqueue_Sync(b *testing.B) {
	processFunc := func(op AsyncOp) {}
	processor := NewAsyncProcessor(syncTestConfig(), processFunc)

	op := AsyncOp{OpType: AsyncOpTypeCall, CallTime: time.Now().UnixNano()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		processor.Enqueue(op)
	}
}

// BenchmarkPendingOpsManager_Add 测试添加操作吞吐量
func BenchmarkPendingOpsManager_Add(b *testing.B) {
	m := NewPendingOpsManager()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Add(int64(i), "data")
	}
}

// BenchmarkPendingOpsManager_Get 测试获取操作吞吐量
func BenchmarkPendingOpsManager_Get(b *testing.B) {
	m := NewPendingOpsManager()

	for i := 0; i < 1000; i++ {
		m.Add(int64(i), "data")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Get(i % 1000)
	}
}

// BenchmarkPendingOpsManager_Remove 测试删除操作吞吐量
func BenchmarkPendingOpsManager_Remove(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := NewPendingOpsManager()
		for j := 0; j < 100; j++ {
			m.Add(int64(j), "data")
		}
		for j := 0; j < 100; j++ {
			m.Remove(j)
		}
	}
}

// BenchmarkPendingOpsManager_Concurrent 测试并发添加吞吐量
func BenchmarkPendingOpsManager_Concurrent(b *testing.B) {
	m := NewPendingOpsManager()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.Add(int64(i), "data")
			i++
		}
	})
}

// BenchmarkGenerateVersion 测试版本号生成吞吐量
func BenchmarkGenerateVersion(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GenerateVersion()
	}
}

// BenchmarkBaseHook_Enabled 测试启用状态切换吞吐量
func BenchmarkBaseHook_Enabled(b *testing.B) {
	recorder := newTestRecorder()
	hook := NewBaseHook(recorder, porcupine.OpTypeTopology)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hook.SetEnabled(true)
		_ = hook.Enabled()
		hook.SetEnabled(false)
		_ = hook.Enabled()
	}
}

// BenchmarkBaseHook_Stats 测试统计信息获取吞吐量
func BenchmarkBaseHook_Stats(b *testing.B) {
	recorder := newTestRecorder()
	hook := NewBaseHook(recorder, porcupine.OpTypeTopology)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hook.Stats()
	}
}

// BenchmarkHookStats_Concurrent 测试并发统计更新
func BenchmarkHookStats_Concurrent(b *testing.B) {
	var stats HookStats

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			stats.AddRecorded(1)
			stats.AddDropped(1)
		}
	})
}

// ==================== 延迟测试 ====================

// TestLatency_HookDisabled 测试 Hook 禁用时的延迟
func TestLatency_HookDisabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())

	// 测量基线
	start := time.Now()
	for i := 0; i < 10000; i++ {
	}
	baseline := time.Since(start)

	// 测量 Hook 禁用时的延迟
	start = time.Now()
	for i := 0; i < 10000; i++ {
		_ = hook.OnGossipWrite("key", []byte("value"))
	}
	hookLatency := time.Since(start)

	overhead := float64(hookLatency-baseline) / float64(baseline)
	t.Logf("Baseline: %v, Hook Disabled: %v, Overhead: %.2f%%", baseline, hookLatency, overhead*100)

	if overhead > 0.5 {
		t.Logf("Warning: Hook disabled overhead is %.2f%%, expected < 50%%", overhead*100)
	}
}

// TestLatency_HookEnabled 测试 Hook 启用时的延迟
func TestLatency_HookEnabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	start := time.Now()
	for i := 0; i < 1000; i++ {
		opID := hook.OnGossipWrite("key", []byte("value"))
		hook.OnGossipReturn(opID, true, "")
	}
	latency := time.Since(start)

	avgLatency := latency / 1000
	t.Logf("Total: %v, Avg per op: %v", latency, avgLatency)

	if avgLatency > 100*time.Microsecond {
		t.Logf("Warning: Average latency per op is %v, expected < 100us", avgLatency)
	}
}

// TestLatency_Verify1000Ops 测试 1000 ops 验证时间
func TestLatency_Verify1000Ops(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewGossipHook(recorder, "node-1", syncTestConfig())
	hook.SetEnabled(true)

	for i := 0; i < 1000; i++ {
		opID := hook.OnGossipWrite(fmt.Sprintf("key-%d", i), []byte("value"))
		hook.OnGossipReturn(opID, true, "")
	}

	start := time.Now()
	ops := recorder.GetTopologyOperations()
	duration := time.Since(start)

	t.Logf("Verify 1000 ops: %v, ops retrieved: %d", duration, len(ops))

	if duration > 100*time.Millisecond {
		t.Errorf("Verify took %v, expected < 100ms", duration)
	}
}

// ==================== 并发吞吐量测试 ====================

// TestThroughput_AsyncProcessor 测试 AsyncProcessor 吞吐量
func TestThroughput_AsyncProcessor(t *testing.T) {
	var processedCount atomic.Int64

	processFunc := func(op AsyncOp) {
		processedCount.Add(1)
	}

	config := porcupine.AsyncRecordConfig{
		Enabled:    true,
		BufferSize: 100000,
		DropOnFull: true,
	}

	processor := NewAsyncProcessor(config, processFunc)
	processor.Start()
	defer processor.Stop()

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := 10000

	start := time.Now()
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				op := AsyncOp{OpType: AsyncOpTypeCall, CallTime: time.Now().UnixNano()}
				processor.Enqueue(op)
			}
		}()
	}
	wg.Wait()

	time.Sleep(100 * time.Millisecond)

	duration := time.Since(start)
	totalOps := int64(numGoroutines * opsPerGoroutine)
	throughput := float64(processedCount.Load()) / duration.Seconds()

	t.Logf("Processed: %d/%d ops in %v, throughput: %.0f ops/s",
		processedCount.Load(), totalOps, duration, throughput)
}

// TestThroughput_PendingOpsManager 测试 PendingOpsManager 吞吐量
func TestThroughput_PendingOpsManager(t *testing.T) {
	m := NewPendingOpsManager()
	var wg sync.WaitGroup

	numGoroutines := 10
	opsPerGoroutine := 10000

	start := time.Now()
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				m.Add(int64(i), "data")
			}
		}()
	}
	wg.Wait()

	duration := time.Since(start)
	totalOps := numGoroutines * opsPerGoroutine
	throughput := float64(totalOps) / duration.Seconds()

	t.Logf("Added %d ops in %v, throughput: %.0f ops/s", totalOps, duration, throughput)
}
