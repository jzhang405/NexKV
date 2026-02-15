// Package hooks 提供 Porcupine 运行时验证的 Hook 集成
// 本文件测试 Hook 接口和通用类型
package hooks

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
)

// ==================== HookStats 测试 ====================

func TestHookStats_AddRecorded(t *testing.T) {
	var stats HookStats

	stats.AddRecorded(10)
	if stats.TotalRecorded != 10 {
		t.Errorf("Expected TotalRecorded=10, got %d", stats.TotalRecorded)
	}

	stats.AddRecorded(5)
	if stats.TotalRecorded != 15 {
		t.Errorf("Expected TotalRecorded=15, got %d", stats.TotalRecorded)
	}
}

func TestHookStats_AddDropped(t *testing.T) {
	var stats HookStats

	stats.AddDropped(3)
	if stats.DroppedOps != 3 {
		t.Errorf("Expected DroppedOps=3, got %d", stats.DroppedOps)
	}
}

func TestHookStats_AddError(t *testing.T) {
	var stats HookStats

	stats.AddError(2)
	if stats.TotalErrors != 2 {
		t.Errorf("Expected TotalErrors=2, got %d", stats.TotalErrors)
	}
}

func TestHookStats_SetLastVerifyTime(t *testing.T) {
	var stats HookStats

	now := time.Now().UnixNano()
	stats.SetLastVerifyTime(now)
	if stats.LastVerifyTime != now {
		t.Errorf("Expected LastVerifyTime=%d, got %d", now, stats.LastVerifyTime)
	}
}

func TestHookStats_Concurrent(t *testing.T) {
	var stats HookStats
	var wg sync.WaitGroup

	// 并发增加记录数
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats.AddRecorded(1)
		}()
	}

	// 并发增加丢弃数
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats.AddDropped(1)
		}()
	}

	wg.Wait()

	if stats.TotalRecorded != 100 {
		t.Errorf("Expected TotalRecorded=100, got %d", stats.TotalRecorded)
	}
	if stats.DroppedOps != 50 {
		t.Errorf("Expected DroppedOps=50, got %d", stats.DroppedOps)
	}
}

// ==================== BaseHook 测试 ====================

func TestNewBaseHook(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewBaseHook(recorder, porcupine.OpTypeTopology)

	if hook == nil {
		t.Fatal("Expected non-nil BaseHook")
	}
	if hook.Enabled() {
		t.Error("Expected hook to be disabled by default")
	}
	if hook.Recorder() != recorder {
		t.Error("Recorder mismatch")
	}
	if hook.ModelType() != porcupine.OpTypeTopology {
		t.Errorf("Expected ModelType=OpTypeTopology, got %v", hook.ModelType())
	}
}

func TestBaseHook_Enabled(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewBaseHook(recorder, porcupine.OpTypeTopology)
	assertEnabledState(t, hook)
}

func TestBaseHook_Enabled_Concurrent(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewBaseHook(recorder, porcupine.OpTypeTopology)

	var wg sync.WaitGroup
	var enabledCount atomic.Int32

	// 100 个 goroutine 并发读写
	for i := 0; i < 100; i++ {
		wg.Add(2)

		// 写者
		go func(val bool) {
			defer wg.Done()
			hook.SetEnabled(val)
		}(i%2 == 0)

		// 读者
		go func() {
			defer wg.Done()
			if hook.Enabled() {
				enabledCount.Add(1)
			}
		}()
	}

	wg.Wait()
	// 如果没有 race condition，测试通过
}

func TestBaseHook_Stats(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewBaseHook(recorder, porcupine.OpTypeTopology)

	// 初始统计应该为零值
	stats := hook.Stats()
	if stats.TotalRecorded != 0 || stats.DroppedOps != 0 || stats.TotalErrors != 0 {
		t.Errorf("Expected zero stats, got %+v", stats)
	}

	// 增加统计
	hook.AddRecorded(10)
	hook.AddDropped(2)
	hook.AddError(1)

	stats = hook.Stats()
	if stats.TotalRecorded != 10 {
		t.Errorf("Expected TotalRecorded=10, got %d", stats.TotalRecorded)
	}
	if stats.DroppedOps != 2 {
		t.Errorf("Expected DroppedOps=2, got %d", stats.DroppedOps)
	}
	if stats.TotalErrors != 1 {
		t.Errorf("Expected TotalErrors=1, got %d", stats.TotalErrors)
	}
}

func TestBaseHook_ModelType(t *testing.T) {
	recorder := newTestRecorder()

	topologyHook := NewBaseHook(recorder, porcupine.OpTypeTopology)
	if topologyHook.ModelType() != porcupine.OpTypeTopology {
		t.Errorf("Expected OpTypeTopology, got %v", topologyHook.ModelType())
	}

	failureHook := NewBaseHook(recorder, porcupine.OpTypeFailureRecovery)
	if failureHook.ModelType() != porcupine.OpTypeFailureRecovery {
		t.Errorf("Expected OpTypeFailureRecovery, got %v", failureHook.ModelType())
	}

	leaderHook := NewBaseHook(recorder, porcupine.OpTypeLeaderHA)
	if leaderHook.ModelType() != porcupine.OpTypeLeaderHA {
		t.Errorf("Expected OpTypeLeaderHA, got %v", leaderHook.ModelType())
	}
}

func TestBaseHook_Recorder(t *testing.T) {
	recorder := newTestRecorder()
	hook := NewBaseHook(recorder, porcupine.OpTypeTopology)

	if hook.Recorder() != recorder {
		t.Error("Recorder mismatch")
	}
}

// ==================== AsyncProcessor 测试 ====================

func TestNewAsyncProcessor(t *testing.T) {
	var processedOps []AsyncOp
	var mu sync.Mutex

	processFunc := func(op AsyncOp) {
		mu.Lock()
		processedOps = append(processedOps, op)
		mu.Unlock()
	}

	processor := NewAsyncProcessor(asyncTestConfig(), processFunc)
	if processor == nil {
		t.Fatal("Expected non-nil AsyncProcessor")
	}
}

func TestAsyncProcessor_Enqueue_Sync(t *testing.T) {
	var processedOps []AsyncOp
	var mu sync.Mutex

	processFunc := func(op AsyncOp) {
		mu.Lock()
		processedOps = append(processedOps, op)
		mu.Unlock()
	}

	processor := NewAsyncProcessor(syncTestConfig(), processFunc)

	op := AsyncOp{OpType: AsyncOpTypeCall, CallTime: time.Now().UnixNano()}
	success := processor.Enqueue(op)

	if !success {
		t.Error("Expected Enqueue to succeed in sync mode")
	}

	mu.Lock()
	if len(processedOps) != 1 {
		t.Errorf("Expected 1 processed op in sync mode, got %d", len(processedOps))
	}
	mu.Unlock()
}

func TestAsyncProcessor_Enqueue_Async_DropOnFull(t *testing.T) {
	var processedCount atomic.Int32

	processFunc := func(op AsyncOp) {
		processedCount.Add(1)
		time.Sleep(10 * time.Millisecond)
	}

	config := porcupine.AsyncRecordConfig{
		Enabled:    true,
		BufferSize: 2,
		DropOnFull: true,
	}

	processor := NewAsyncProcessor(config, processFunc)
	processor.Start()
	defer processor.Stop()

	successCount := 0
	for i := 0; i < 10; i++ {
		op := AsyncOp{OpType: AsyncOpTypeCall, CallTime: time.Now().UnixNano()}
		if processor.Enqueue(op) {
			successCount++
		}
	}

	if successCount == 0 {
		t.Error("Expected at least some operations to succeed")
	}
}

func TestAsyncProcessor_Enqueue_Blocking(t *testing.T) {
	var processedCount atomic.Int32

	processFunc := func(op AsyncOp) {
		processedCount.Add(1)
	}

	config := porcupine.AsyncRecordConfig{
		Enabled:    true,
		BufferSize: 2,
		DropOnFull: false,
	}

	processor := NewAsyncProcessor(config, processFunc)
	processor.Start()
	defer processor.Stop()

	successCount := 0
	for i := 0; i < 5; i++ {
		op := AsyncOp{OpType: AsyncOpTypeCall, CallTime: time.Now().UnixNano()}
		if processor.Enqueue(op) {
			successCount++
		}
	}

	if successCount != 5 {
		t.Errorf("Expected all 5 operations to succeed in blocking mode, got %d", successCount)
	}

	waitForProcessing()
	if processedCount.Load() != 5 {
		t.Errorf("Expected 5 processed ops, got %d", processedCount.Load())
	}
}

func TestAsyncProcessor_StartStop(t *testing.T) {
	var processedCount atomic.Int32

	processFunc := func(op AsyncOp) {
		processedCount.Add(1)
	}

	processor := NewAsyncProcessor(asyncTestConfig(), processFunc)

	processor.Start()

	for i := 0; i < 5; i++ {
		op := AsyncOp{OpType: AsyncOpTypeCall, CallTime: time.Now().UnixNano()}
		processor.Enqueue(op)
	}

	waitForProcessing()
	processor.Stop()
	processor.Stop() // 再次 Stop 应该安全
}

func TestAsyncProcessor_Start_Disabled(t *testing.T) {
	processFunc := func(op AsyncOp) {}

	processor := NewAsyncProcessor(syncTestConfig(), processFunc)

	processor.Start()
	processor.Stop()
}

// ==================== PendingOpsManager 测试 ====================

func TestNewPendingOpsManager(t *testing.T) {
	m := NewPendingOpsManager()
	if m == nil {
		t.Fatal("Expected non-nil PendingOpsManager")
	}
}

func TestPendingOpsManager_AddGetRemove(t *testing.T) {
	m := NewPendingOpsManager()

	callTime := time.Now().UnixNano()
	callData := "test-data"
	opID := m.Add(callTime, callData)

	if opID < 0 {
		t.Errorf("Expected non-negative opID, got %d", opID)
	}

	op, exists := m.Get(opID)
	if !exists {
		t.Error("Expected op to exist")
	}
	if op.CallTime != callTime {
		t.Errorf("Expected CallTime=%d, got %d", callTime, op.CallTime)
	}
	if op.CallData != callData {
		t.Errorf("Expected CallData=%v, got %v", callData, op.CallData)
	}

	_, exists = m.Get(9999)
	if exists {
		t.Error("Expected non-existent op to not exist")
	}

	m.Remove(opID)
	_, exists = m.Get(opID)
	if exists {
		t.Error("Expected op to be removed")
	}
}

func TestPendingOpsManager_AddMultiple(t *testing.T) {
	m := NewPendingOpsManager()

	id1 := m.Add(1, "data1")
	id2 := m.Add(2, "data2")
	id3 := m.Add(3, "data3")

	if id1 >= id2 || id2 >= id3 {
		t.Errorf("Expected opIDs to be increasing: %d, %d, %d", id1, id2, id3)
	}
}

func TestPendingOpsManager_Clear(t *testing.T) {
	m := NewPendingOpsManager()

	m.Add(1, "data1")
	m.Add(2, "data2")
	m.Add(3, "data3")

	m.Clear()

	count := 0
	m.Range(func(opID int, op *PendingOp) bool {
		count++
		return true
	})

	if count != 0 {
		t.Errorf("Expected 0 pending ops after Clear, got %d", count)
	}
}

func TestPendingOpsManager_Range(t *testing.T) {
	m := NewPendingOpsManager()

	m.Add(1, "data1")
	m.Add(2, "data2")
	m.Add(3, "data3")

	visited := make(map[int]bool)
	m.Range(func(opID int, op *PendingOp) bool {
		visited[opID] = true
		return true
	})

	if len(visited) != 3 {
		t.Errorf("Expected to visit 3 ops, got %d", len(visited))
	}
}

func TestPendingOpsManager_Range_Stop(t *testing.T) {
	m := NewPendingOpsManager()

	m.Add(1, "data1")
	m.Add(2, "data2")
	m.Add(3, "data3")

	count := 0
	m.Range(func(opID int, op *PendingOp) bool {
		count++
		return count < 2
	})

	if count != 2 {
		t.Errorf("Expected to visit 2 ops, got %d", count)
	}
}

func TestPendingOpsManager_RangeAndDelete(t *testing.T) {
	m := NewPendingOpsManager()

	m.Add(1, "data1")
	m.Add(2, "data2")
	m.Add(3, "data3")

	deleted := 0
	m.RangeAndDelete(func(opID int, op *PendingOp) (bool, bool) {
		shouldDelete := opID%2 == 0
		if shouldDelete {
			deleted++
		}
		return true, shouldDelete
	})

	if deleted != 2 {
		t.Errorf("Expected to delete 2 ops, got %d", deleted)
	}

	count := 0
	m.Range(func(opID int, op *PendingOp) bool {
		count++
		return true
	})

	if count != 1 {
		t.Errorf("Expected 1 remaining op, got %d", count)
	}
}

func TestPendingOpsManager_RangeAndDelete_All(t *testing.T) {
	m := NewPendingOpsManager()

	m.Add(1, "data1")
	m.Add(2, "data2")
	m.Add(3, "data3")

	m.RangeAndDelete(func(opID int, op *PendingOp) (bool, bool) {
		return true, true
	})

	count := 0
	m.Range(func(opID int, op *PendingOp) bool {
		count++
		return true
	})

	if count != 0 {
		t.Errorf("Expected 0 remaining ops, got %d", count)
	}
}

func TestPendingOpsManager_Concurrent(t *testing.T) {
	m := NewPendingOpsManager()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			m.Add(int64(val), val)
		}(i)
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Get(0)
		}()
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			m.Remove(val)
		}(i)
	}

	wg.Wait()
}

// ==================== GenerateVersion 测试 ====================

func TestGenerateVersion_Uniqueness(t *testing.T) {
	versions := make(map[uint64]bool)
	const count = 100

	for i := 0; i < count; i++ {
		_, version := GenerateVersion()
		versions[version] = true
		time.Sleep(time.Microsecond)
	}

	uniqueCount := len(versions)
	if uniqueCount < count/2 {
		t.Errorf("Too many duplicate versions: %d unique out of %d", uniqueCount, count)
	}
}

func TestGenerateVersion_Monotonic(t *testing.T) {
	var lastVersion uint64
	const count = 50

	for i := 0; i < count; i++ {
		_, version := GenerateVersion()
		if version < lastVersion {
			t.Errorf("Version went backwards: %d < %d", version, lastVersion)
		}
		lastVersion = version
		time.Sleep(time.Microsecond)
	}
}

func TestGenerateVersion_CallTime(t *testing.T) {
	callTime, version := GenerateVersion()

	now := time.Now().UnixNano()
	diff := now - callTime

	if diff < 0 || diff > int64(time.Millisecond) {
		t.Errorf("CallTime too far from now: diff=%d", diff)
	}

	if version != uint64(callTime) {
		t.Errorf("Expected version=%d, got %d", callTime, version)
	}
}

// ==================== AsyncOp 测试 ====================

func TestAsyncOp_Types(t *testing.T) {
	if AsyncOpTypeCall != 0 {
		t.Errorf("Expected AsyncOpTypeCall=0, got %d", AsyncOpTypeCall)
	}
	if AsyncOpTypeReturn != 1 {
		t.Errorf("Expected AsyncOpTypeReturn=1, got %d", AsyncOpTypeReturn)
	}
}
