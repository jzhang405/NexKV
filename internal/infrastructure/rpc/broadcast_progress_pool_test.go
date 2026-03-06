// Package rpc RPC 层对象池测试
package rpc

import (
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// TestAcquireReleaseBroadcastProgress 测试对象获取和归还
func TestAcquireReleaseBroadcastProgress(t *testing.T) {
	peers := make([]model.PeerID, 3)
	peers[0] = model.PeerID("node-1")
	peers[1] = model.PeerID("node-2")
	peers[2] = model.PeerID("node-3")

	// 第一次获取
	bp1 := acquireBroadcastProgress("task-1", peers)
	assert.NotNil(t, bp1)
	assert.Equal(t, "task-1", bp1.taskID)
	assert.Equal(t, 3, len(bp1.targets))

	// 模拟使用
	bp1.RecordSuccess(peers[0], nil)

	// 归还对象
	releaseBroadcastProgress(bp1)

	// 第二次获取（应该复用同一个对象）
	bp2 := acquireBroadcastProgress("task-2", peers)
	assert.NotNil(t, bp2)

	// 验证状态已重置
	bp2.mu.RLock()
	assert.Equal(t, "task-2", bp2.taskID)
	assert.Equal(t, 0, len(bp2.responses))
	assert.Equal(t, 0, len(bp2.failures))
	bp2.mu.RUnlock()

	// 归还对象
	releaseBroadcastProgress(bp2)
}

// TestBroadcastProgress_ResetState 测试状态重置
func TestBroadcastProgress_ResetState(t *testing.T) {
	peers := make([]model.PeerID, 2)
	peers[0] = model.PeerID("node-1")
	peers[1] = model.PeerID("node-2")

	bp := acquireBroadcastProgress("test-reset", peers)

	// 添加一些数据
	bp.RecordSuccess(peers[0], nil)
	bp.RecordFailure(peers[1], assert.AnError)

	// 验证数据存在
	bp.mu.RLock()
	assert.Equal(t, 1, len(bp.responses))
	assert.Equal(t, 1, len(bp.failures))
	bp.mu.RUnlock()

	// 归还对象
	releaseBroadcastProgress(bp)

	// 再次获取并验证状态已重置
	bp2 := acquireBroadcastProgress("test-reset-2", peers)
	bp2.mu.RLock()
	assert.Equal(t, 0, len(bp2.responses))
	assert.Equal(t, 0, len(bp2.failures))
	bp2.mu.RUnlock()

	// 注意：因为对象的复用，taskID 已经改变
	assert.Equal(t, "test-reset-2", bp2.taskID)

	releaseBroadcastProgress(bp2)
}

// TestBroadcastProgress_ConcurrentAcquire 并发获取测试
func TestBroadcastProgress_ConcurrentAcquire(t *testing.T) {
	peers := make([]model.PeerID, 2)
	peers[0] = model.PeerID("node-1")
	peers[1] = model.PeerID("node-2")

	const goroutines = 100
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bp := acquireBroadcastProgress("concurrent-task", peers)
			_ = bp.taskID
			releaseBroadcastProgress(bp)
		}(i)
	}

	wg.Wait()
	// 如果没有死锁或 panic，测试通过
}

// TestBroadcastProgress_NoDataLeak 测试无内存泄漏
func TestBroadcastProgress_NoDataLeak(t *testing.T) {
	peers := make([]model.PeerID, 2)
	peers[0] = model.PeerID("node-1")
	peers[1] = model.PeerID("node-2")

	// 获取、使用、归还
	bp := acquireBroadcastProgress("leak-test", peers)

	// 添加数据
	bp.RecordSuccess(peers[0], nil)
	bp.RecordFailure(peers[1], assert.AnError)

	bp.mu.RLock()
	responsesCount := len(bp.responses)
	failuresCount := len(bp.failures)
	bp.mu.RUnlock()

	assert.Equal(t, 1, responsesCount)
	assert.Equal(t, 1, failuresCount)

	// 归还对象
	releaseBroadcastProgress(bp)

	// 再次获取，验证数据已清理
	bp2 := acquireBroadcastProgress("leak-test-2", peers)
	bp2.mu.RLock()
	assert.Equal(t, 0, len(bp2.responses))
	assert.Equal(t, 0, len(bp2.failures))
	bp2.mu.RUnlock()

	releaseBroadcastProgress(bp2)
}

// TestReleaseBroadcastProgress_NilSafe 测试 nil 安全处理
func TestReleaseBroadcastProgress_NilSafe(t *testing.T) {
	// 不应该 panic
	releaseBroadcastProgress(nil)

	// 测试通过
	assert.True(t, true)
}
