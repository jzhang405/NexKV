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
	majorityCallbackTriggered bool              // OnMajority 是否已触发
	fullDoneCallbackTriggered bool              // OnComplete 是否已触发

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

		// 触发 OnMajority 回调（仅一次）
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

		// 触发 OnComplete 回调（仅一次）
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

	// 触发 OnMajority 回调（仅一次）
	if shouldTriggerMajority {
		safeCallback(func() {
			callback.OnMajority(stats)
		})
	}

	// 触发 OnComplete 回调（仅一次）
	if shouldTriggerFullDone {
		safeCallback(func() {
			callback.OnComplete(stats)
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

		// 触发 OnComplete 回调（仅一次）
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

	// 触发 OnComplete 回调（仅一次）
	if shouldTriggerFullDone {
		safeCallback(func() {
			callback.OnComplete(stats)
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

// ============================================================================
// ProgressBuilder - Builder 模式（链式配置）
// ============================================================================

// ProgressBuilder 进度追踪器构建器
//
// 使用示例：
//
//	tracker := service.NewProgress("task-001", peers).
//	    WithTimeout(10 * time.Second).
//	    OnSuccess(func(peer model.PeerID, resp model.Message, stats BroadcastStats) {
//	        log.Printf("✓ %s 成功", peer)
//	    }).
//	    OnFailure(func(peer model.PeerID, err error, stats BroadcastStats) {
//	        log.Printf("✗ %s 失败: %v", peer, err)
//	    }).
//	    OnMajority(func(stats BroadcastStats) {
//	        log.Printf("🎉 多数派达成!")
//	    }).
//	    OnComplete(func(stats BroadcastStats) {
//	        log.Printf("✅ 全部完成")
//	    }).
//	    Build()
type ProgressBuilder struct {
	progress *BroadcastProgress
}

// NewProgress 创建新的进度追踪器构建器
func NewProgress(taskID string, targets []model.PeerID) *ProgressBuilder {
	targetsCopy := make([]model.PeerID, len(targets))
	copy(targetsCopy, targets)

	return &ProgressBuilder{
		progress: &BroadcastProgress{
			taskID:       taskID,
			targets:      targetsCopy,
			responses:    make(map[model.PeerID]model.Message),
			failures:     make(map[model.PeerID]error),
			fullDone:     make(chan struct{}),
			majorityDone: make(chan struct{}),

			// Callback 机制初始化
			callbacksEnabled: true,
			startTime:        time.Now(),
		},
	}
}

// WithTimeout 设置超时时间（仅用于统计，不自动触发取消）
func (b *ProgressBuilder) WithTimeout(timeout time.Duration) *ProgressBuilder {
	// 超时功能可通过 context 取消实现，此处仅记录
	_ = timeout
	return b
}

// OnSuccess 成功回调
func (b *ProgressBuilder) OnSuccess(fn func(peer model.PeerID, resp model.Message, stats BroadcastStats)) *ProgressBuilder {
	if b.progress.callback == nil {
		b.progress.callback = &builderListener{progress: b.progress}
	}
	b.progress.callback.(*builderListener).onSuccess = fn
	return b
}

// OnFailure 失败回调
func (b *ProgressBuilder) OnFailure(fn func(peer model.PeerID, err error, stats BroadcastStats)) *ProgressBuilder {
	if b.progress.callback == nil {
		b.progress.callback = &builderListener{progress: b.progress}
	}
	b.progress.callback.(*builderListener).onFailure = fn
	return b
}

// OnMajority 多数派达成回调（原 OnMajority）
func (b *ProgressBuilder) OnMajority(fn func(stats BroadcastStats)) *ProgressBuilder {
	if b.progress.callback == nil {
		b.progress.callback = &builderListener{progress: b.progress}
	}
	b.progress.callback.(*builderListener).onMajority = fn
	return b
}

// OnComplete 全部完成回调（原 OnComplete）
func (b *ProgressBuilder) OnComplete(fn func(stats BroadcastStats)) *ProgressBuilder {
	if b.progress.callback == nil {
		b.progress.callback = &builderListener{progress: b.progress}
	}
	b.progress.callback.(*builderListener).onComplete = fn
	return b
}

// Build 构建进度追踪器
func (b *ProgressBuilder) Build() *BroadcastProgress {
	return b.progress
}

// builderListener Builder 模式监听器适配器
type builderListener struct {
	progress   *BroadcastProgress
	onSuccess  func(peer model.PeerID, resp model.Message, stats BroadcastStats)
	onFailure  func(peer model.PeerID, err error, stats BroadcastStats)
	onMajority func(stats BroadcastStats)
	onComplete func(stats BroadcastStats)
}

func (l *builderListener) OnSuccess(peer model.PeerID, resp model.Message, stats BroadcastStats) {
	if l.onSuccess != nil {
		l.onSuccess(peer, resp, stats)
	}
}

func (l *builderListener) OnFailure(peer model.PeerID, err error, stats BroadcastStats) {
	if l.onFailure != nil {
		l.onFailure(peer, err, stats)
	}
}

func (l *builderListener) OnMajority(stats BroadcastStats) {
	if l.onMajority != nil {
		l.onMajority(stats)
	}
}

func (l *builderListener) OnComplete(stats BroadcastStats) {
	if l.onComplete != nil {
		l.onComplete(stats)
	}
}
