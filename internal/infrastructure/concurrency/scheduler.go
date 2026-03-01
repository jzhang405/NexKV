// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	stderrors "errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// TaskScheduler 任务调度器（领域服务）
// 职责：统一管理路由、执行器选择和三级降级策略
type TaskScheduler struct {
	// 执行器映射
	executors map[model.TaskMode]TaskExecutor
	mu        sync.RWMutex

	// 路由规则（SourceID pattern -> TaskMode）
	routingRules map[string]model.TaskMode
	routingMu    sync.RWMutex

	// 默认模式
	defaultMode model.TaskMode

	// 统计信息
	stats SchedulerStats

	// 降级统计
	degradationStats DegradationStats
}

// TaskExecutor 基础任务执行器接口
type TaskExecutor interface {
	Submit(ctx context.Context, task func(context.Context)) error
}

// SchedulerStats 调度器统计信息
type SchedulerStats struct {
	TotalSubmitted int64 // 总提交任务数
	ByMode         map[model.TaskMode]int64
	mu             sync.RWMutex
}

// DegradationStats 降级统计
type DegradationStats struct {
	PerCoreToAnts     int64 // PerCore -> Ants 降级次数
	AntsToGoroutine   int64 // Ants -> Goroutine 降级次数
	PerCoreSuccess    int64 // PerCore 成功次数
	AntsSuccess       int64 // Ants 成功次数
	GoroutineFallback int64 // Goroutine 兜底次数
}

// NewTaskScheduler 创建新的任务调度器
func NewTaskScheduler() *TaskScheduler {
	return &TaskScheduler{
		executors:    make(map[model.TaskMode]TaskExecutor),
		routingRules: make(map[string]model.TaskMode),
		defaultMode:  model.ModeDefaultPool,
		stats: SchedulerStats{
			ByMode: make(map[model.TaskMode]int64),
		},
	}
}

// RegisterExecutor 注册执行器
func (s *TaskScheduler) RegisterExecutor(mode model.TaskMode, executor TaskExecutor) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.executors[mode]; exists {
		return ErrExecutorAlreadyRegistered
	}

	s.executors[mode] = executor
	return nil
}

// UnregisterExecutor 注销执行器
func (s *TaskScheduler) UnregisterExecutor(mode model.TaskMode) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.executors, mode)
}

// GetExecutor 获取执行器
func (s *TaskScheduler) GetExecutor(mode model.TaskMode) TaskExecutor {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.executors[mode]
}

// SetDefaultMode 设置默认模式
func (s *TaskScheduler) SetDefaultMode(mode model.TaskMode) {
	s.defaultMode = mode
}

// AddRoutingRule 添加路由规则
func (s *TaskScheduler) AddRoutingRule(pattern string, mode model.TaskMode) error {
	s.routingMu.Lock()
	defer s.routingMu.Unlock()

	s.routingRules[pattern] = mode
	return nil
}

// RemoveRoutingRule 移除路由规则
func (s *TaskScheduler) RemoveRoutingRule(pattern string) {
	s.routingMu.Lock()
	defer s.routingMu.Unlock()

	delete(s.routingRules, pattern)
}

// Route 根据 SourceID 路由到合适的执行模式
func (s *TaskScheduler) Route(sourceID model.SourceID) model.TaskMode {
	// 1. 检查自定义路由规则
	s.routingMu.RLock()
	for pattern, mode := range s.routingRules {
		if sourceID.Match(pattern) {
			s.routingMu.RUnlock()
			return mode
		}
	}
	s.routingMu.RUnlock()

	// 2. 使用 SourceID 推荐模式
	recommendedMode := sourceID.RecommendedMode()

	// 3. 检查推荐模式的执行器是否存在
	s.mu.RLock()
	_, exists := s.executors[recommendedMode]
	s.mu.RUnlock()

	if exists {
		return recommendedMode
	}

	// 4. 降级到默认模式
	return s.defaultMode
}

// Select 根据 SourceID 选择执行器（兼容 TaskSelector API）
func (s *TaskScheduler) Select(sourceID model.SourceID) (TaskExecutor, model.TaskMode, error) {
	// 1. 路由到合适的模式
	mode := s.Route(sourceID)

	// 2. 获取执行器
	s.mu.RLock()
	executor, exists := s.executors[mode]
	s.mu.RUnlock()

	if !exists {
		// 降级到默认执行器
		s.mu.RLock()
		executor, exists = s.executors[s.defaultMode]
		s.mu.RUnlock()

		if !exists {
			return nil, mode, ErrNoExecutorAvailable
		}
		return executor, s.defaultMode, nil
	}

	return executor, mode, nil
}

// SelectByMode 根据模式选择执行器
func (s *TaskScheduler) SelectByMode(mode model.TaskMode) (TaskExecutor, error) {
	s.mu.RLock()
	executor, exists := s.executors[mode]
	s.mu.RUnlock()

	if !exists {
		return nil, ErrNoExecutorAvailable
	}
	return executor, nil
}

// Submit 提交任务
func (s *TaskScheduler) Submit(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	// 1. 路由到合适的模式
	mode := s.Route(sourceID)

	// 2. 获取执行器
	s.mu.RLock()
	executor, exists := s.executors[mode]
	s.mu.RUnlock()

	if !exists {
		// 降级到默认执行器
		s.mu.RLock()
		executor, exists = s.executors[s.defaultMode]
		s.mu.RUnlock()

		if !exists {
			return ErrNoExecutorAvailable
		}
	}

	// 3. 更新统计（统一使用 atomic，提升性能）
	atomic.AddInt64(&s.stats.TotalSubmitted, 1)
	s.stats.mu.Lock()
	s.stats.ByMode[mode]++
	s.stats.mu.Unlock()

	// 4. 提交任务
	return executor.Submit(ctx, task)
}

// SubmitWithMode 使用指定模式提交任务
func (s *TaskScheduler) SubmitWithMode(ctx context.Context, mode model.TaskMode, task func(context.Context)) error {
	s.mu.RLock()
	executor, exists := s.executors[mode]
	s.mu.RUnlock()

	if !exists {
		return ErrNoExecutorAvailable
	}

	// 更新统计
	atomic.AddInt64(&s.stats.TotalSubmitted, 1)
	s.stats.mu.Lock()
	s.stats.ByMode[mode]++
	s.stats.mu.Unlock()

	return executor.Submit(ctx, task)
}

// Close 关闭调度器
func (s *TaskScheduler) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var lastErr error
	for mode, executor := range s.executors {
		if closer, ok := executor.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				lastErr = err
			}
		}
		delete(s.executors, mode)
	}

	return lastErr
}

// Stats 获取统计信息
func (s *TaskScheduler) Stats() *SchedulerStats {
	s.stats.mu.RLock()
	defer s.stats.mu.RUnlock()

	// 复制统计信息
	result := &SchedulerStats{
		TotalSubmitted: atomic.LoadInt64(&s.stats.TotalSubmitted),
		ByMode:         make(map[model.TaskMode]int64),
	}

	for mode, count := range s.stats.ByMode {
		result.ByMode[mode] = count
	}

	return result
}

// AvailableModes 返回可用的执行模式列表
func (s *TaskScheduler) AvailableModes() []model.TaskMode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	modes := make([]model.TaskMode, 0, len(s.executors))
	for mode := range s.executors {
		modes = append(modes, mode)
	}
	return modes
}

// HasMode 检查指定模式是否可用
func (s *TaskScheduler) HasMode(mode model.TaskMode) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.executors[mode]
	return exists
}

// ==========================================
// 错误定义
// ==========================================

var (
	// ErrExecutorAlreadyRegistered 执行器已注册
	ErrExecutorAlreadyRegistered = &SchedulerError{
		Code:    "EXECUTOR_ALREADY_REGISTERED",
		Message: "executor already registered for this mode",
	}

	// ErrNoExecutorAvailable 没有可用的执行器
	ErrNoExecutorAvailable = &SchedulerError{
		Code:    "NO_EXECUTOR_AVAILABLE",
		Message: "no executor available for the requested mode",
	}
)

// SchedulerError 调度器错误
type SchedulerError struct {
	Code    string
	Message string
}

func (e *SchedulerError) Error() string {
	return e.Message
}

// ==========================================
// SourceRouter 辅助类型
// ==========================================

// SourceRouter SourceID 路由器
type SourceRouter struct {
	rules       map[string]model.TaskMode
	defaultMode model.TaskMode
	mu          sync.RWMutex
}

// NewSourceRouter 创建新的路由器
func NewSourceRouter() *SourceRouter {
	return &SourceRouter{
		rules:       make(map[string]model.TaskMode),
		defaultMode: model.ModeDefaultPool,
	}
}

// Route 路由 SourceID 到 TaskMode
func (r *SourceRouter) Route(sourceID model.SourceID) model.TaskMode {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 检查精确匹配
	sourceStr := sourceID.String()
	if mode, ok := r.rules[sourceStr]; ok {
		return mode
	}

	// 检查模式匹配
	for pattern, mode := range r.rules {
		if sourceID.Match(pattern) {
			return mode
		}
	}

	return r.defaultMode
}

// AddRule 添加路由规则
func (r *SourceRouter) AddRule(pattern string, mode model.TaskMode) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.rules[pattern] = mode
}

// RemoveRule 移除路由规则
func (r *SourceRouter) RemoveRule(pattern string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.rules, pattern)
}

// SetDefaultMode 设置默认模式
func (r *SourceRouter) SetDefaultMode(mode model.TaskMode) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.defaultMode = mode
}

// ==========================================
// 便捷方法
// ==========================================

// ParseAndSubmit 解析 SourceID 字符串并提交任务
func (s *TaskScheduler) ParseAndSubmit(ctx context.Context, sourceIDStr string, task func(context.Context)) error {
	sourceID, err := model.ParseSourceID(sourceIDStr)
	if err != nil {
		return err
	}
	return s.Submit(ctx, sourceID, task)
}

// SubmitHighPriority 提交高优先级任务
func (s *TaskScheduler) SubmitHighPriority(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	// 高优先级任务使用 PerCore 模式（如果可用）
	if s.HasMode(model.ModePerCore) {
		return s.SubmitWithMode(ctx, model.ModePerCore, task)
	}
	return s.Submit(ctx, sourceID, task)
}

// SubmitBackground 提交后台任务
func (s *TaskScheduler) SubmitBackground(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	// 后台任务使用 DefaultPool 模式
	return s.Submit(ctx, sourceID, task)
}

// GetMetrics 获取指标（用于 Prometheus）
func (s *TaskScheduler) GetMetrics() map[string]int64 {
	stats := s.Stats()

	result := make(map[string]int64)
	result["total_submitted"] = stats.TotalSubmitted

	for mode, count := range stats.ByMode {
		key := "mode_" + strings.ReplaceAll(mode.String(), "-", "_")
		result[key] = count
	}

	return result
}

// ==========================================
// 三级降级策略
// ==========================================

// SubmitWithDegradation 带三级降级的任务提交
// 降级链: PerCore -> Ants Default -> Native Goroutine
func (s *TaskScheduler) SubmitWithDegradation(ctx context.Context, task func(context.Context)) error {
	// Level 1: 尝试 PerCore
	s.mu.RLock()
	executor := s.executors[model.ModePerCore]
	s.mu.RUnlock()

	if executor != nil {
		err := executor.Submit(ctx, task)
		if err == nil {
			atomic.AddInt64(&s.degradationStats.PerCoreSuccess, 1)
			return nil
		}
		// 检查是否为可降级错误
		if s.isDegradableError(err) {
			atomic.AddInt64(&s.degradationStats.PerCoreToAnts, 1)
			// 降级到 Level 2
			return s.submitWithAnts(ctx, task)
		}
		return err
	}

	// PerCore 不可用，直接尝试 Ants
	return s.submitWithAnts(ctx, task)
}

// submitWithAnts 使用 Ants Default 提交（Level 2）
func (s *TaskScheduler) submitWithAnts(ctx context.Context, task func(context.Context)) error {
	s.mu.RLock()
	executor := s.executors[model.ModeDefaultPool]
	s.mu.RUnlock()

	if executor != nil {
		err := executor.Submit(ctx, task)
		if err == nil {
			atomic.AddInt64(&s.degradationStats.AntsSuccess, 1)
			return nil
		}
		// Ants 失败，降级到 Level 3
		atomic.AddInt64(&s.degradationStats.AntsToGoroutine, 1)
		return s.submitWithGoroutine(ctx, task)
	}

	// Ants 不可用，直接降级到 Goroutine
	return s.submitWithGoroutine(ctx, task)
}

// submitWithGoroutine 使用原生 Goroutine（Level 3）
func (s *TaskScheduler) submitWithGoroutine(ctx context.Context, task func(context.Context)) error {
	atomic.AddInt64(&s.degradationStats.GoroutineFallback, 1)

	go func() {
		defer func() {
			_ = recover() // 恢复 panic 但不传播，保证 Goroutine 不崩溃
		}()
		task(ctx)
	}()
	return nil
}

// isDegradableError 判断是否为可降级错误
func (s *TaskScheduler) isDegradableError(err error) bool {
	return stderrors.Is(err, errors.ErrQueueFull) ||
		stderrors.Is(err, errors.ErrExecutorClosed) ||
		stderrors.Is(err, errors.ErrPoolFull)
}

// GetDegradationStats 获取降级统计
func (s *TaskScheduler) GetDegradationStats() DegradationStats {
	return DegradationStats{
		PerCoreToAnts:     atomic.LoadInt64(&s.degradationStats.PerCoreToAnts),
		AntsToGoroutine:   atomic.LoadInt64(&s.degradationStats.AntsToGoroutine),
		PerCoreSuccess:    atomic.LoadInt64(&s.degradationStats.PerCoreSuccess),
		AntsSuccess:       atomic.LoadInt64(&s.degradationStats.AntsSuccess),
		GoroutineFallback: atomic.LoadInt64(&s.degradationStats.GoroutineFallback),
	}
}

// DegradationRate 计算降级率（PerCore -> Ants 的比例）
func (s *TaskScheduler) DegradationRate() float64 {
	stats := s.GetDegradationStats()
	total := stats.PerCoreSuccess + stats.PerCoreToAnts
	if total == 0 {
		return 0
	}
	return float64(stats.PerCoreToAnts) / float64(total)
}
