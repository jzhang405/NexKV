// Package rpc RPC 层对象池实现
//
// 使用 sync.Pool 复用 BroadcastProgress 对象，减少内存分配和 GC 压力
// 特别适用于高频广播场景（如 Raft 心跳）
package rpc

import (
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// broadcastProgressPool BroadcastProgress 对象池
//
// 设计原则:
// 1. 对象池复用减少内存分配
// 2. reset() 彻底清空状态，避免数据泄漏
// 3. cleanup() 清空 map，避免持有引用
var broadcastProgressPool = sync.Pool{
	New: func() any {
		// 预分配固定容量，减少后续扩容
		return &BroadcastProgress{
			responses:    make(map[model.PeerID]model.Message, 16),
			failures:     make(map[model.PeerID]error, 16),
			fullDone:     make(chan struct{}),
			majorityDone: make(chan struct{}),
			startTime:    time.Time{}, // 零值时间
		}
	},
}

// acquireBroadcastProgress 从池中获取 BroadcastProgress
//
// 使用后必须调用 releaseBroadcastProgress 归还对象
func acquireBroadcastProgress(taskID string, targets []model.PeerID) *BroadcastProgress {
	bp := broadcastProgressPool.Get().(*BroadcastProgress)

	// 重置状态
	bp.reset(taskID, targets)

	return bp
}

// releaseBroadcastProgress 归还 BroadcastProgress 到池
//
// 必须确保对象不再被使用
func releaseBroadcastProgress(bp *BroadcastProgress) {
	if bp == nil {
		return
	}

	// 清理资源
	bp.cleanup()

	// 归还到池中
	broadcastProgressPool.Put(bp)
}

// reset 重置 BroadcastProgress 状态
func (t *BroadcastProgress) reset(taskID string, targets []model.PeerID) {
	// 保护性拷贝，防止外部修改
	targetsCopy := make([]model.PeerID, len(targets))
	copy(targetsCopy, targets)

	t.mu.Lock()
	defer t.mu.Unlock()

	// 重置基础字段
	t.taskID = taskID
	t.targets = targetsCopy

	// 清空 map（避免持有旧引用）
	for k := range t.responses {
		delete(t.responses, k)
	}
	for k := range t.failures {
		delete(t.failures, k)
	}

	// 重置标志
	t.majorityCallbackTriggered = false
	t.fullDoneCallbackTriggered = false
	t.firstResponseRecorded = false

	// 重置时间
	t.startTime = time.Time{}
	t.firstResponseTime = time.Time{}
	t.majorityReachTime = time.Time{}

	// 确保 channels 可用
	if t.fullDone == nil || isClosed(t.fullDone) {
		t.fullDone = make(chan struct{})
	}
	if t.majorityDone == nil || isClosed(t.majorityDone) {
		t.majorityDone = make(chan struct{})
	}
}

// cleanup 清理 BroadcastProgress 资源
//
// 防止内存泄漏：清空 map，避免持有对象引用
func (t *BroadcastProgress) cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 清空 map（避免持有引用）
	for k := range t.responses {
		delete(t.responses, k)
	}
	for k := range t.failures {
		delete(t.failures, k)
	}

	// 不关闭 channel，因为对象会被复用
	// channels 在 reset() 中会被重新创建（如果被关闭）
}

// isClosed 检查 channel 是否已关闭
func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
