// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 错误定义
// ==========================================

var (
	// ErrExecutorNotFound 执行器未找到
	ErrExecutorNotFound = errors.New("executor not found for requested mode")
	// ErrSelectorClosed 选择器已关闭
	ErrSelectorClosed = errors.New("selector is closed")
	// ErrDuplicateExecutor 执行器重复注册
	ErrDuplicateExecutor = errors.New("executor already registered for this mode")
)

// ==========================================
// TaskSelector 实现
// ==========================================

// TaskSelector 任务选择器
// 根据 SourceID 选择合适的执行器
type TaskSelector struct {
	// 执行器映射
	executors map[model.TaskMode]TaskExecutor
	mu        sync.RWMutex

	// 路由规则（SourceID pattern -> TaskMode）
	routingRules map[string]model.TaskMode
	routingMu    sync.RWMutex

	// 默认模式
	defaultMode model.TaskMode

	// 统计
	stats selectorStatsInternal
}

// selectorStatsInternal 内部统计结构（包含锁）
type selectorStatsInternal struct {
	TotalSubmitted int64
	ByMode         map[model.TaskMode]int64
	mu             sync.RWMutex
}

// SelectorStats 选择器统计（返回结构，不含锁）
type SelectorStats struct {
	TotalSubmitted int64                   // 总提交任务数
	ByMode         map[model.TaskMode]int64 // 按模式统计
}

// NewTaskSelector 创建任务选择器
func NewTaskSelector() *TaskSelector {
	return &TaskSelector{
		executors:    make(map[model.TaskMode]TaskExecutor),
		routingRules: make(map[string]model.TaskMode),
		defaultMode:  model.ModeDefaultPool,
		stats: selectorStatsInternal{
			ByMode: make(map[model.TaskMode]int64),
		},
	}
}

// RegisterExecutor 注册执行器
func (s *TaskSelector) RegisterExecutor(mode model.TaskMode, executor TaskExecutor) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.executors[mode]; exists {
		return ErrDuplicateExecutor
	}

	s.executors[mode] = executor
	return nil
}

// UnregisterExecutor 注销执行器
func (s *TaskSelector) UnregisterExecutor(mode model.TaskMode) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.executors, mode)
}

// Select 根据 SourceID 选择执行器
func (s *TaskSelector) Select(sourceID model.SourceID) (TaskExecutor, model.TaskMode, error) {
	// 1. 检查自定义路由规则
	s.routingMu.RLock()
	for pattern, mode := range s.routingRules {
		if sourceID.Match(pattern) {
			s.routingMu.RUnlock()
			executor := s.getExecutor(mode)
			if executor != nil {
				return executor, mode, nil
			}
			// 规则匹配但执行器不存在，继续尝试其他规则
			break
		}
	}
	s.routingMu.RUnlock()

	// 2. 使用 SourceID 推荐模式
	recommendedMode := sourceID.RecommendedMode()

	// 3. 检查推荐模式的执行器是否存在
	executor := s.getExecutor(recommendedMode)
	if executor != nil {
		return executor, recommendedMode, nil
	}

	// 4. 降级到默认模式
	executor = s.getExecutor(s.defaultMode)
	if executor != nil {
		return executor, s.defaultMode, nil
	}

	return nil, model.ModeDefaultPool, ErrExecutorNotFound
}

// SelectByMode 根据模式选择执行器
func (s *TaskSelector) SelectByMode(mode model.TaskMode) (TaskExecutor, error) {
	executor := s.getExecutor(mode)
	if executor == nil {
		return nil, ErrExecutorNotFound
	}
	return executor, nil
}

// Submit 根据 SourceID 提交任务
func (s *TaskSelector) Submit(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	executor, _, err := s.Select(sourceID)
	if err != nil {
		return err
	}

	// 更新统计
	atomic.AddInt64(&s.stats.TotalSubmitted, 1)

	return executor.Submit(ctx, task)
}

// SubmitWithMode 使用指定模式提交任务
func (s *TaskSelector) SubmitWithMode(ctx context.Context, mode model.TaskMode, task func(context.Context)) error {
	executor, err := s.SelectByMode(mode)
	if err != nil {
		return err
	}

	// 更新统计
	atomic.AddInt64(&s.stats.TotalSubmitted, 1)
	s.stats.mu.Lock()
	s.stats.ByMode[mode]++
	s.stats.mu.Unlock()

	return executor.Submit(ctx, task)
}

// AddRoutingRule 添加路由规则
func (s *TaskSelector) AddRoutingRule(pattern string, mode model.TaskMode) error {
	s.routingMu.Lock()
	defer s.routingMu.Unlock()

	s.routingRules[pattern] = mode
	return nil
}

// RemoveRoutingRule 移除路由规则
func (s *TaskSelector) RemoveRoutingRule(pattern string) {
	s.routingMu.Lock()
	defer s.routingMu.Unlock()

	delete(s.routingRules, pattern)
}

// SetDefaultMode 设置默认模式
func (s *TaskSelector) SetDefaultMode(mode model.TaskMode) {
	s.defaultMode = mode
}

// Stats 获取统计信息
func (s *TaskSelector) Stats() SelectorStats {
	s.stats.mu.RLock()
	defer s.stats.mu.RUnlock()

	result := SelectorStats{
		TotalSubmitted: atomic.LoadInt64(&s.stats.TotalSubmitted),
		ByMode:         make(map[model.TaskMode]int64),
	}

	for mode, count := range s.stats.ByMode {
		result.ByMode[mode] = count
	}

	return result
}

// AvailableModes 返回可用的执行模式列表
func (s *TaskSelector) AvailableModes() []model.TaskMode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	modes := make([]model.TaskMode, 0, len(s.executors))
	for mode := range s.executors {
		modes = append(modes, mode)
	}
	return modes
}

// HasMode 检查指定模式是否可用
func (s *TaskSelector) HasMode(mode model.TaskMode) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.executors[mode]
	return exists
}

// Close 关闭选择器
func (s *TaskSelector) Close() error {
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

// getExecutor 获取执行器（内部方法）
func (s *TaskSelector) getExecutor(mode model.TaskMode) TaskExecutor {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.executors[mode]
}

// ==========================================
// 便捷方法
// ==========================================

// ParseAndSubmit 解析 SourceID 字符串并提交任务
func (s *TaskSelector) ParseAndSubmit(ctx context.Context, sourceIDStr string, task func(context.Context)) error {
	sourceID, err := model.ParseSourceID(sourceIDStr)
	if err != nil {
		return err
	}
	return s.Submit(ctx, sourceID, task)
}

// SubmitHighPriority 提交高优先级任务
func (s *TaskSelector) SubmitHighPriority(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	// 高优先级任务使用 PerCore 模式（如果可用）
	if s.HasMode(model.ModePerCore) {
		return s.SubmitWithMode(ctx, model.ModePerCore, task)
	}
	return s.Submit(ctx, sourceID, task)
}

// SubmitBackground 提交后台任务
func (s *TaskSelector) SubmitBackground(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	// 后台任务使用 CustomPool 模式（如果可用）
	if s.HasMode(model.ModeCustomPool) {
		return s.SubmitWithMode(ctx, model.ModeCustomPool, task)
	}
	return s.Submit(ctx, sourceID, task)
}
