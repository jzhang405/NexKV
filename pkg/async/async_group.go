package async

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/transport"
)

// ==========================================
// AsyncGroup[T] 批量操作
// ==========================================

// AsyncGroup 批量异步操作组
type AsyncGroup[T any] struct {
	lifecycle             *transport.AsyncLifecycle
	targets               []model.PeerID
	ops                   map[model.PeerID]AsyncOperation[T]
	results               map[model.PeerID]T
	errors                map[model.PeerID]error
	mu                    sync.RWMutex
	anyDone               chan struct{}
	majorityDone          chan struct{}
	allDone               chan struct{}
	callback              GroupCallback[T]
	startTime             time.Time
	firstResponseTime     time.Time
	majorityReachTime     time.Time
	firstResponseRecorded bool
	firstResponseOnce     sync.Once
	majorityOnce          sync.Once
	allOnce               sync.Once
}

// GroupCallback[T] 批量操作回调接口
type GroupCallback[T any] interface {
	// OnSuccess 单个操作成功
	OnSuccess(peer model.PeerID, value T, stats GroupStats)
	// OnFailure 单个操作失败
	OnFailure(peer model.PeerID, err error, stats GroupStats)
	// OnMajorityReached 多数派完成
	OnMajorityReached(stats GroupStats)
	// OnFullDone 全部完成
	OnFullDone(stats GroupStats)
}

// GroupStats 批量操作统计信息
type GroupStats struct {
	TotalPeers        int
	SuccessCount      int
	FailureCount      int
	StartTime         time.Time
	FirstResponseTime time.Time
	MajorityReachTime time.Time
}

// GroupResult 批量操作结果
type GroupResult[T any] struct {
	Values       map[model.PeerID]T
	Errors       map[model.PeerID]error
	SuccessPeers []model.PeerID
	FailedPeers  []model.PeerID
}

// NewGroup 创建批量异步操作组
func NewGroup[T any](
	ctx context.Context,
	targets []model.PeerID,
	execFunc func(ctx context.Context, target model.PeerID) (T, error),
	opts ...GroupOption,
) *AsyncGroup[T] {
	// 应用选项
	config := &groupConfig{
		lifecycle: transport.NewAsyncLifecycle(),
	}
	for _, opt := range opts {
		opt(config)
	}

	lifecycle := config.lifecycle
	if lifecycle == nil {
		lifecycle = transport.NewAsyncLifecycle()
	}

	// 复制 targets 避免外部修改
	targetsCopy := make([]model.PeerID, len(targets))
	copy(targetsCopy, targets)

	g := &AsyncGroup[T]{
		lifecycle:    lifecycle,
		targets:      targetsCopy,
		ops:          make(map[model.PeerID]AsyncOperation[T]),
		results:      make(map[model.PeerID]T),
		errors:       make(map[model.PeerID]error),
		anyDone:      make(chan struct{}),
		majorityDone: make(chan struct{}),
		allDone:      make(chan struct{}),
		startTime:    time.Now(),
	}

	// 为每个目标创建异步操作
	for _, target := range targets {
		target := target // 捕获循环变量
		op := NewOp[T](lifecycle.Context(), func(ctx context.Context) (T, error) {
			return execFunc(ctx, target)
		})
		g.ops[target] = op

		// 注册完成回调
		op.OnComplete(func(value T, err error) {
			g.handleResult(target, value, err)
		})
	}

	return g
}

// groupConfig 批量操作配置
type groupConfig struct {
	lifecycle *transport.AsyncLifecycle
}

// GroupOption 批量操作选项
type GroupOption func(*groupConfig)

// WithGroupLifecycle 设置生命周期管理器
func WithGroupLifecycle(lifecycle *transport.AsyncLifecycle) GroupOption {
	return func(c *groupConfig) {
		c.lifecycle = lifecycle
	}
}

// handleResult 处理单个结果
func (g *AsyncGroup[T]) handleResult(peer model.PeerID, value T, err error) {
	var callback GroupCallback[T]
	var stats GroupStats
	var shouldTriggerMajority bool
	var shouldTriggerAllDone bool

	g.mu.Lock()

	// 记录第一次响应
	g.firstResponseOnce.Do(func() {
		g.firstResponseTime = time.Now()
		g.firstResponseRecorded = true
		close(g.anyDone)
	})

	// 存储结果
	if err != nil {
		g.errors[peer] = err
	} else {
		g.results[peer] = value
	}

	// 计算统计信息
	total := len(g.targets)
	success := len(g.results)
	failed := len(g.errors)
	completed := success + failed

	// 检查是否达到多数派
	majorityCount := (total / 2) + 1
	if success >= majorityCount {
		g.majorityOnce.Do(func() {
			g.majorityReachTime = time.Now()
			shouldTriggerMajority = true
			close(g.majorityDone)
		})
	}

	// 检查是否全部完成
	if completed >= total {
		g.allOnce.Do(func() {
			shouldTriggerAllDone = true
			close(g.allDone)
		})
	}

	// 准备统计信息
	stats = GroupStats{
		TotalPeers:        total,
		SuccessCount:      success,
		FailureCount:      failed,
		StartTime:         g.startTime,
		FirstResponseTime: g.firstResponseTime,
		MajorityReachTime: g.majorityReachTime,
	}

	callback = g.callback
	g.mu.Unlock()

	// 执行回调
	if callback != nil {
		if err != nil {
			callback.OnFailure(peer, err, stats)
		} else {
			callback.OnSuccess(peer, value, stats)
		}
		if shouldTriggerMajority {
			callback.OnMajorityReached(stats)
		}
		if shouldTriggerAllDone {
			callback.OnFullDone(stats)
		}
	}
}

// SetCallback 设置回调
func (g *AsyncGroup[T]) SetCallback(callback GroupCallback[T]) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.callback = callback
}

// WaitAny 等待任意一个完成
func (g *AsyncGroup[T]) WaitAny(ctx context.Context) (model.PeerID, T, error) {
	select {
	case <-g.anyDone:
	case <-ctx.Done():
		var zero T
		return "", zero, ctx.Err()
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// 返回第一个成功的结果
	for peer, value := range g.results {
		return peer, value, nil
	}

	// 如果没有成功的，返回第一个失败的结果
	for peer, err := range g.errors {
		var zero T
		return peer, zero, err
	}

	var zero T
	return "", zero, fmt.Errorf("no result available")
}

// WaitMajority 等待多数派完成
func (g *AsyncGroup[T]) WaitMajority(ctx context.Context) GroupResult[T] {
	select {
	case <-g.majorityDone:
	case <-ctx.Done():
	}

	return g.getResult()
}

// WaitAll 等待全部完成
func (g *AsyncGroup[T]) WaitAll(ctx context.Context) GroupResult[T] {
	select {
	case <-g.allDone:
	case <-ctx.Done():
	}

	return g.getResult()
}

// CancelAll 取消所有操作
func (g *AsyncGroup[T]) CancelAll() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var lastErr error
	for _, op := range g.ops {
		if _, err := op.Cancel(); err != nil {
			lastErr = err
		}
	}

	if g.lifecycle != nil {
		g.lifecycle.Cancel()
	}

	return lastErr
}

// Status 获取所有操作状态
func (g *AsyncGroup[T]) Status() map[model.PeerID]OperationStatus {
	g.mu.RLock()
	defer g.mu.RUnlock()

	statuses := make(map[model.PeerID]OperationStatus)
	for peer, op := range g.ops {
		statuses[peer] = op.Status()
	}
	return statuses
}

// Stats 获取统计信息
func (g *AsyncGroup[T]) Stats() GroupStats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return GroupStats{
		TotalPeers:        len(g.targets),
		SuccessCount:      len(g.results),
		FailureCount:      len(g.errors),
		StartTime:         g.startTime,
		FirstResponseTime: g.firstResponseTime,
		MajorityReachTime: g.majorityReachTime,
	}
}

// getResult 获取结果
func (g *AsyncGroup[T]) getResult() GroupResult[T] {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := GroupResult[T]{
		Values: make(map[model.PeerID]T),
		Errors: make(map[model.PeerID]error),
	}

	for peer, value := range g.results {
		result.Values[peer] = value
		result.SuccessPeers = append(result.SuccessPeers, peer)
	}

	for peer, err := range g.errors {
		result.Errors[peer] = err
		result.FailedPeers = append(result.FailedPeers, peer)
	}

	return result
}
