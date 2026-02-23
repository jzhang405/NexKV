// Package service 定义领域服务接口
package service

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ============================================================================
// BroadcastProgress Listener 机制（v1.5）
// ============================================================================

// BroadcastListener 广播进度监听接口
//
// 监听器执行顺序（针对每个响应）：
//  1. OnSuccess / OnFailure（每次响应）
//     ↓
//  2. OnMajorityReached（达到多数派时，仅一次）
//     ↓
//  3. OnFullDone（全部完成时，仅一次）
//
// 特殊场景：OnMajorityReached 和 OnFullDone 可能在同一次 RecordSuccess 中
// 顺序触发（如果 Majority 达成时恰好也是最后一个响应）
//
// 监听器实现注意事项：
// 1. 监听器在锁外执行，但应避免调用 BroadcastProgress 的方法（防止死锁）
// 2. 监听器应快速返回（< 10ms），长时间处理应启动 goroutine
// 3. 监听器可能被并发调用（OnSuccess/OnFailure），实现需线程安全
// 4. 不要依赖监听器的调用顺序（除文档明确保证的之外）
type BroadcastListener interface {
	// OnSuccess 每次收到成功响应时调用
	// 参数说明：
	//   - peer: 响应节点 ID
	//   - resp: 成功响应消息（不会为 nil）
	//   - stats: 当前统计信息
	OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats)

	// OnFailure 每次收到失败响应时调用
	// 参数说明：
	//   - peer: 失败节点 ID
	//   - err: 错误信息（不会为 nil，包含具体错误类型）
	//          - 超时错误：context.DeadlineExceeded
	//          - 网络错误：net.Error
	//          - 业务错误：业务逻辑返回的错误
	//   - stats: 当前统计信息
	OnFailure(peer model.PeerID, err error, stats BroadcastStats)

	// OnMajorityReached 达到多数派时调用（仅调用一次）
	// 触发条件：
	//   - 成功响应数 >= majority（len(targets)/2 + 1）
	//   - 只在 RecordSuccess 时检查，RecordFailure 不会触发
	//   - 例如：3 个节点，2 个成功即触发（即使 1 个失败）
	// 参数说明：
	//   - stats: 达到多数派时的统计信息
	OnMajorityReached(stats BroadcastStats)

	// OnFullDone 全部完成时调用（仅调用一次）
	// 触发条件：
	//   - 成功数 + 失败数 == 总节点数
	// 参数说明：
	//   - stats: 全部完成时的统计信息
	OnFullDone(stats BroadcastStats)
}

// BroadcastStats 广播统计信息
type BroadcastStats struct {
	TaskID            string        // 任务 ID
	Total             int           // 总节点数
	Success           int           // 成功数
	Failed            int           // 失败数
	Pending           int           // 待响应数
	SuccessRate       float64       // 成功率
	ElapsedTime       time.Duration // 已耗时（从任务开始到现在）
	FirstResponseTime time.Duration // 首个响应耗时（从任务开始到首个响应）
	MajorityReachTime time.Duration // 达到多数派耗时（从任务开始到多数派达成）
}

// NoOpListener 空实现的 BroadcastListener
// 可用于嵌入到自定义 Callback 中，只重写需要的方法
//
// 使用示例:
//
//	type MyCallback struct {
//	    NoOpListener // 嵌入所有空实现
//	}
//
//	// 只重写关心的方法
//	func (m *MyCallback) OnFullDone(stats BroadcastStats) {
//	    fmt.Printf("广播完成！成功率: %.2f%%\n", stats.SuccessRate*100)
//	}
type NoOpListener struct{}

// OnSuccess 空实现
func (n NoOpListener) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {}

// OnFailure 空实现
func (n NoOpListener) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {}

// OnMajorityReached 空实现
func (n NoOpListener) OnMajorityReached(stats BroadcastStats) {}

// OnFullDone 空实现
func (n NoOpListener) OnFullDone(stats BroadcastStats) {}

// BroadcastProgress 可选的广播追踪器（一次性使用）
//
// 设计原则：
// 1. Tracker 是一次性的，不复用（避免 channel 泄漏）
// 2. Tracker 是**独立的监控工具**，与 RPC 调用的 ResponseStrategy **解耦**
// 3. 无论 RPC 使用什么策略（All/Majority/None），Tracker 都可以：
//   - WaitFull(): 等待所有节点响应
//   - WaitMajority(): 等待多数派响应
//   - Stats(): 实时查看进度
//
// 典型使用场景：
//
//	// 场景 1：RPC 用 ResponseMajority，tracker 后台监控 full completion
//	tracker := NewBroadcastProgress("task-001", replicas)
//	rpc.BroadcastCall(ctx, replicas, req, ResponseMajority, tracker)
//	// RPC 返回后，异步等待全部完成
//	go func() { tracker.WaitFull(ctx) }()
//
//	// 场景 2：RPC 用 ResponseNone，tracker 监控所有响应
//	tracker := NewBroadcastProgress("task-002", replicas)
//	rpc.BroadcastCall(ctx, replicas, req, ResponseNone, tracker)
//	// 后台等待全部或多数派完成
//	tracker.WaitMajority(ctx)
type BroadcastProgress struct {
	// 基础字段
	taskID    string                         // 任务 ID（用于日志）
	targets   []model.PeerID                 // 目标节点列表
	responses map[model.PeerID]model.Message // 成功响应
	failures  map[model.PeerID]error         // 失败记录

	// 同步原语
	mu           sync.RWMutex  // 保护并发访问
	fullDone     chan struct{} // 全部完成时关闭
	majorityDone chan struct{} // 多数派完成时关闭

	// Callback 机制（v1.4）
	callback                  BroadcastListener // 回调接口（可选）
	callbacksEnabled          bool              // 回调启用/禁用开关
	majorityCallbackTriggered bool              // OnMajorityReached 是否已触发
	fullDoneCallbackTriggered bool              // OnFullDone 是否已触发

	// 时间统计
	startTime             time.Time // 任务开始时间
	firstResponseTime     time.Time // 首个响应时间
	majorityReachTime     time.Time // 达到多数派时间
	firstResponseRecorded bool      // 是否已记录首个响应
}

// NewBroadcastProgress 创建广播追踪器
func NewBroadcastProgress(taskID string, targets []model.PeerID) *BroadcastProgress {
	// 保护性拷贝，防止外部修改
	targetsCopy := make([]model.PeerID, len(targets))
	copy(targetsCopy, targets)

	return &BroadcastProgress{
		taskID:       taskID,
		targets:      targetsCopy,
		responses:    make(map[model.PeerID]model.Message),
		failures:     make(map[model.PeerID]error),
		fullDone:     make(chan struct{}),
		majorityDone: make(chan struct{}),

		// Callback 机制初始化
		callbacksEnabled: true, // 默认启用回调
		startTime:        time.Now(),
	}
}

// WaitFull 等待所有节点响应（包括失败的）
// 适用场景：集群关闭、全局同步
func (t *BroadcastProgress) WaitFull(ctx context.Context) error {
	select {
	case <-t.fullDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitMajority 等待多数派（> N/2）节点响应
// 适用场景：需要确认多数派完成的场景（如 3 副本写入确认 W=2）
// 注意：与 RPC 调用的 ResponseStrategy 无关，可独立使用
func (t *BroadcastProgress) WaitMajority(ctx context.Context) error {
	// 快速路径：先检查当前状态
	t.mu.RLock()
	majority := len(t.targets)/2 + 1
	if len(t.responses) >= majority || len(t.targets) == 0 {
		t.mu.RUnlock()
		return nil
	}
	t.mu.RUnlock()

	// 等待 majorityDone channel（零 CPU 开销）
	select {
	case <-t.majorityDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats 获取实时统计信息
func (t *BroadcastProgress) Stats() (success, failed, pending int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.responses), len(t.failures),
		len(t.targets) - len(t.responses) - len(t.failures)
}

// SetCallback 设置进度回调（必须在开始之前设置）
func (t *BroadcastProgress) SetCallback(cb BroadcastListener) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callback = cb
}

// EnableCallbacks 启用/禁用回调（可选，便于测试）
func (t *BroadcastProgress) EnableCallbacks(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callbacksEnabled = enabled
}

// IsMajorityReached 检查是否已达到多数派
func (t *BroadcastProgress) IsMajorityReached() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	majority := len(t.targets)/2 + 1
	return len(t.responses) >= majority
}

// IsFullDone 检查是否全部完成
func (t *BroadcastProgress) IsFullDone() bool {
	select {
	case <-t.fullDone:
		return true
	default:
		return false
	}
}

// RecordSuccess 记录成功响应（由 RPC 实现调用）
// 线程安全，自动更新状态并关闭 channel
func (t *BroadcastProgress) RecordSuccess(peer model.PeerID, resp model.Message) {
	var callback BroadcastListener
	var stats BroadcastStats
	var shouldTriggerMajority bool
	var shouldTriggerFullDone bool

	// === 锁内：只做状态更新 ===
	t.mu.Lock()

	// 1. 记录响应
	t.responses[peer] = resp

	// 2. 记录首个响应时间（仅一次）
	if !t.firstResponseRecorded {
		t.firstResponseTime = time.Now()
		t.firstResponseRecorded = true
	}

	// 3. 检查是否满足 Majority 策略
	majority := len(t.targets)/2 + 1
	if len(t.responses) >= majority {
		// 关闭 majorityDone channel（仅关闭一次）
		select {
		case <-t.majorityDone:
			// 已经关闭，跳过
		default:
			close(t.majorityDone)
		}

		// 触发 OnMajorityReached 回调（仅一次）
		if !t.majorityCallbackTriggered {
			t.majorityCallbackTriggered = true
			t.majorityReachTime = time.Now()
			shouldTriggerMajority = true
		}
	}

	// 4. 检查是否全部完成
	if len(t.responses)+len(t.failures) == len(t.targets) {
		// 关闭 fullDone channel
		select {
		case <-t.fullDone:
		default:
			close(t.fullDone)
		}

		// 触发 OnFullDone 回调（仅一次）
		if !t.fullDoneCallbackTriggered {
			t.fullDoneCallbackTriggered = true
			shouldTriggerFullDone = true
		}
	}

	// 5. 准备回调数据
	callback = t.callback
	stats = t.buildStatsLocked()
	t.mu.Unlock()
	// === 锁外：执行回调，避免死锁 ===

	// 6. 触发回调（如果启用）
	if callback == nil || !t.callbacksEnabled {
		return
	}

	// 触发 OnSuccess 回调
	safeCallback(func() {
		callback.OnSuccess(peer, resp, stats)
	})

	// 触发 OnMajorityReached 回调（仅一次）
	if shouldTriggerMajority {
		safeCallback(func() {
			callback.OnMajorityReached(stats)
		})
	}

	// 触发 OnFullDone 回调（仅一次）
	if shouldTriggerFullDone {
		safeCallback(func() {
			callback.OnFullDone(stats)
		})
	}
}

// RecordFailure 记录失败响应（由 RPC 实现调用）
// 线程安全，自动更新状态并关闭 channel
func (t *BroadcastProgress) RecordFailure(peer model.PeerID, err error) {
	var callback BroadcastListener
	var stats BroadcastStats
	var shouldTriggerFullDone bool

	// === 锁内：只做状态更新 ===
	t.mu.Lock()

	// 1. 记录失败
	t.failures[peer] = err

	// 2. 检查是否全部完成
	if len(t.responses)+len(t.failures) == len(t.targets) {
		// 关闭 fullDone channel
		select {
		case <-t.fullDone:
		default:
			close(t.fullDone)
		}

		// 触发 OnFullDone 回调（仅一次）
		if !t.fullDoneCallbackTriggered {
			t.fullDoneCallbackTriggered = true
			shouldTriggerFullDone = true
		}
	}

	// 3. 准备回调数据
	callback = t.callback
	stats = t.buildStatsLocked()
	t.mu.Unlock()
	// === 锁外：执行回调，避免死锁 ===

	// 4. 触发回调（如果启用）
	if callback == nil || !t.callbacksEnabled {
		return
	}

	// 触发 OnFailure 回调
	safeCallback(func() {
		callback.OnFailure(peer, err, stats)
	})

	// 触发 OnFullDone 回调（仅一次）
	if shouldTriggerFullDone {
		safeCallback(func() {
			callback.OnFullDone(stats)
		})
	}
}

// buildStatsLocked 构建统计信息（内部方法，需持锁调用）
func (t *BroadcastProgress) buildStatsLocked() BroadcastStats {
	success := len(t.responses)
	failed := len(t.failures)
	total := len(t.targets)

	// P2 修复：避免除零
	var successRate float64
	if total > 0 {
		successRate = float64(success) / float64(total)
	}

	// 计算时间戳
	elapsedTime := time.Since(t.startTime)
	var firstResponseTime time.Duration
	var majorityReachTime time.Duration

	if t.firstResponseRecorded {
		firstResponseTime = t.firstResponseTime.Sub(t.startTime)
	}
	if t.majorityCallbackTriggered {
		majorityReachTime = t.majorityReachTime.Sub(t.startTime)
	}

	return BroadcastStats{
		TaskID:            t.taskID,
		Total:             total,
		Success:           success,
		Failed:            failed,
		Pending:           total - success - failed,
		SuccessRate:       successRate,
		ElapsedTime:       elapsedTime,
		FirstResponseTime: firstResponseTime,
		MajorityReachTime: majorityReachTime,
	}
}

// safeCallback 安全执行回调，防止 panic 影响主流程
func safeCallback(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			// 使用 slog.Error 记录 panic，便于监控和告警
			slog.Error("[BroadcastProgress] callback panic recovered",
				"panic", r,
				"stack", string(debug.Stack()))
		}
	}()
	fn()
}
