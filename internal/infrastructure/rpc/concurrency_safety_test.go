// Package rpc RPC 层并发安全测试
//
// 重点关注：
// 1. 死锁检测
// 2. 数据竞争
// 3. 资源泄漏
// 4. 并发状态一致性
package rpc

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==============================================================================
// 死锁检测测试
// ==============================================================================

// testCallback 测试回调实现
type testCallback struct {
	onSuccess  func(peer model.PeerID, resp model.Message, stats service.BroadcastStats)
	onFailure  func(peer model.PeerID, err error, stats service.BroadcastStats)
	onMajority func(stats service.BroadcastStats)
	onComplete func(stats service.BroadcastStats)
}

func (c *testCallback) OnSuccess(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
	if c.onSuccess != nil {
		c.onSuccess(peer, resp, stats)
	}
}

func (c *testCallback) OnFailure(peer model.PeerID, err error, stats service.BroadcastStats) {
	if c.onFailure != nil {
		c.onFailure(peer, err, stats)
	}
}

func (c *testCallback) OnMajority(stats service.BroadcastStats) {
	if c.onMajority != nil {
		c.onMajority(stats)
	}
}

func (c *testCallback) OnComplete(stats service.BroadcastStats) {
	if c.onComplete != nil {
		c.onComplete(stats)
	}
}

// TestDeadlock_ConcurrentCallbackExecution 测试并发回调执行不会死锁
// 这是一个经典的死锁场景：持锁时调用回调，回调尝试获取同一把锁
func TestDeadlock_ConcurrentCallbackExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping deadlock test in short mode")
	}

	const goroutines = 100
	var wg sync.WaitGroup

	// 创建足够的 peers，每个 goroutine 使用不同的 peer
	peers := make([]model.PeerID, goroutines)
	for i := range peers {
		peers[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}
	progress := NewBroadcastProgress("deadlock-test", peers)

	// 添加回调（回调内部会访问 progress 的方法）
	callback := &testCallback{
		onSuccess: func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
			// 回调中访问 progress.Stats()，触发读锁
			success, failed, pending := progress.Stats()
			_ = success + failed + pending
		},
	}
	progress.SetCallback(callback)

	// 并发记录成功（可能触发回调）
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			peer := peers[id]
			req := model.NewMessage(fmt.Sprintf("test-%d", id), model.MessageTypeResponse, "node-1", peer, nil)
			progress.RecordSuccess(peer, req)
		}(i)
	}

	// 设置超时，如果有死锁则超时
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 成功完成，无死锁
		success, failed, pending := progress.Stats()
		assert.Equal(t, goroutines, success+failed+pending)
	case <-time.After(10 * time.Second):
		t.Fatal("DEADLOCK DETECTED: Test timed out after 10 seconds")
	}
}

// TestDeadlock_NilCallbackSafe 测试 nil 回调不会死锁
func TestDeadlock_NilCallbackSafe(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	progress := NewBroadcastProgress("nil-test", peers)

	// 不设置回调（nil）
	progress.SetCallback(nil)

	// 并发记录成功和失败
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			progress.RecordSuccess(peers[0], nil)
		}()

		go func() {
			defer wg.Done()
			progress.RecordFailure(peers[1], fmt.Errorf("test error"))
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 成功
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out with nil callback")
	}
}

// ==============================================================================
// 数据竞争检测测试
// ==============================================================================

// TestDataRace_ConcurrentStatsAccess 测试并发访问 Stats() 无数据竞争
func TestDataRace_ConcurrentStatsAccess(t *testing.T) {
	const goroutines = 100
	// 创建足够的 peers
	peers := make([]model.PeerID, goroutines)
	for i := range peers {
		peers[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	progress := NewBroadcastProgress("race-test", peers)

	var wg sync.WaitGroup

	// 一半 goroutine 记录结果，一半读取状态
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			if id%2 == 0 {
				// 写操作
				peer := peers[id]
				progress.RecordSuccess(peer, nil)
			} else {
				// 读操作
				success, failed, pending := progress.Stats()
				_ = success + failed + pending
				progress.IsMajorityReached()
				progress.IsFullDone()
			}
		}(i)
	}

	wg.Wait()
	success, failed, pending := progress.Stats()
	// 检查没有崩溃，数据一致性由 map 的并发安全保护
	assert.Equal(t, len(peers), success+failed+pending)
}

// TestDataRace_ConcurrentWaitCalls 测试并发 Wait 调用无数据竞争
func TestDataRace_ConcurrentWaitCalls(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	progress := NewBroadcastProgress("wait-test", peers)

	const goroutines = 10
	var wg sync.WaitGroup

	// 多个 goroutine 同时等待
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			// 并发调用 WaitMajority 和 WaitFull
			_ = progress.WaitMajority(ctx)
			_ = progress.WaitFull(ctx)
		}()
	}

	// 完成所有任务
	progress.RecordSuccess(peers[0], nil)
	progress.RecordSuccess(peers[1], nil)

	wg.Wait()
}

// ==============================================================================
// 资源泄漏检测测试
// ==============================================================================

// TestResourceLeak_NoGoroutineLeak 测试无 goroutine 泄漏
func TestResourceLeak_NoGoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping goroutine leak test in short mode")
	}

	initial := runtime.NumGoroutine()

	// 创建大量任务并快速完成
	for i := 0; i < 100; i++ {
		progress := NewBroadcastProgress(fmt.Sprintf("leak-test-%d", i), []model.PeerID{"node-1"})
		progress.RecordSuccess("node-1", nil)
	}

	// 等待所有 goroutine 清理
	time.Sleep(200 * time.Millisecond)

	final := runtime.NumGoroutine()
	leaked := final - initial

	// 允许少量误差（调度器等）
	assert.LessOrEqual(t, leaked, 10, "Possible goroutine leak: %d goroutines leaked", leaked)
}

// TestResourceLeak_ChannelClosure 测试 channel 正确关闭
func TestResourceLeak_ChannelClosure(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}
	progress := NewBroadcastProgress("channel-test", peers)

	// 完成所有任务
	progress.RecordSuccess(peers[0], nil)
	progress.RecordSuccess(peers[1], nil)

	// 验证 channels 可读（已关闭）
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := progress.WaitFull(ctx)
	assert.NoError(t, err, "WaitFull should return without error")

	err = progress.WaitMajority(ctx)
	assert.NoError(t, err, "WaitMajority should return without error")

	// 重复调用应该立即返回（channel 已关闭）
	err = progress.WaitFull(ctx)
	assert.NoError(t, err)
}

// ==============================================================================
// 并发状态一致性测试
// ==============================================================================

// TestStateConsistency_ConcurrentStateTransitions 测试并发状态转换一致性
func TestStateConsistency_ConcurrentStateTransitions(t *testing.T) {
	const operations = 1000

	// 创建足够的 peers
	peers := make([]model.PeerID, operations)
	for i := range peers {
		peers[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	progress := NewBroadcastProgress("consistency-test", peers)

	var wg sync.WaitGroup

	var successCount, failureCount int32

	for i := 0; i < operations; i++ {
		wg.Add(2)

		go func(id int) {
			defer wg.Done()
			peer := peers[id]
			if id%3 == 0 {
				progress.RecordFailure(peer, fmt.Errorf("error-%d", id))
				atomic.AddInt32(&failureCount, 1)
			} else {
				progress.RecordSuccess(peer, nil)
				atomic.AddInt32(&successCount, 1)
			}
		}(i)

		go func(id int) {
			defer wg.Done()
			success, failed, pending := progress.Stats()
			// 验证状态一致性：总数应该等于当前已处理的数量
			total := success + failed + pending
			// 在并发过程中，total <= operations 总是成立
			assert.LessOrEqual(t, total, operations)
		}(i)
	}

	wg.Wait()

	// 最终验证
	success, failed, pending := progress.Stats()
	assert.Equal(t, int(successCount), success)
	assert.Equal(t, int(failureCount), failed)
	assert.Equal(t, 0, pending)
}

// ==============================================================================
// 对象池并发安全测试
// ==============================================================================

// TestObjectPool_ConcurrentAcquireRelease 测试对象池并发获取和归还
func TestObjectPool_ConcurrentAcquireRelease(t *testing.T) {
	const goroutines = 100
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	peers := []model.PeerID{"node-1", "node-2", "node-3"}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				// 获取对象
				bp := acquireBroadcastProgress(fmt.Sprintf("task-%d-%d", id, j), peers)

				// 模拟使用
				bp.RecordSuccess(peers[0], nil)
				success, failed, pending := bp.Stats()
				_ = success + failed + pending

				// 归还对象
				releaseBroadcastProgress(bp)
			}
		}(i)
	}

	wg.Wait()
	// 如果没有 panic 或死锁，测试通过
}

// TestObjectPool_NoCrossContamination 测试对象池不会交叉污染
func TestObjectPool_NoCrossContamination(t *testing.T) {
	peers := []model.PeerID{"node-1", "node-2"}

	// 第一个使用者
	bp1 := acquireBroadcastProgress("task-1", peers)
	bp1.RecordSuccess(peers[0], nil)
	bp1.RecordSuccess(peers[1], nil)
	success1, failed1, pending1 := bp1.Stats()
	require.Equal(t, 2, success1, "First user should have 2 successes")
	require.Equal(t, 0, failed1)
	require.Equal(t, 0, pending1)
	releaseBroadcastProgress(bp1)

	// 第二个使用者 - 对象应该被重置
	bp2 := acquireBroadcastProgress("task-2", peers)
	success2, failed2, pending2 := bp2.Stats()
	// 由于 reset() 设置了新的 targets，responses 和 failures 应该是空的
	assert.Equal(t, 0, success2, "Second user should not see first user's data")
	assert.Equal(t, 0, failed2, "Second user should not see first user's failures")
	// pending = len(targets) - len(responses) - len(failures) = 2 - 0 - 0 = 2
	assert.Equal(t, 2, pending2, "Pending should be 2 (all targets waiting)")
	assert.Equal(t, "task-2", bp2.taskID, "TaskID should be reset")
	releaseBroadcastProgress(bp2)
}

// ==============================================================================
// 压力测试
// ==============================================================================

// TestStress_HighFrequencyBroadcast 模拟 Raft 心跳高频广播
func TestStress_HighFrequencyBroadcast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const duration = 2 * time.Second
	const peersCount = 5

	peers := make([]model.PeerID, peersCount)
	for i := range peers {
		peers[i] = model.PeerID(fmt.Sprintf("node-%d", i))
	}

	var completed int32
	stop := make(chan struct{})

	// 模拟高频广播（类似 Raft 心跳）
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					progress := NewBroadcastProgress(fmt.Sprintf("heartbeat-%d", workerID), peers)
					for _, peer := range peers {
						progress.RecordSuccess(peer, nil)
					}
					atomic.AddInt32(&completed, 1)
					releaseBroadcastProgress(progress) // 使用对象池
				case <-stop:
					return
				}
			}
		}(i)
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	completedCount := atomic.LoadInt32(&completed)
	t.Logf("Completed %d broadcasts in %v", completedCount, duration)
	assert.Greater(t, completedCount, int32(100), "Should complete at least 100 broadcasts")
}
