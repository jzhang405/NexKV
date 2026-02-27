// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TaskCoordinator 任务协调器（领域服务）
// 职责：协调路由和执行器选择，不维护业务状态
type TaskCoordinator struct {
	// 执行器映射
	executors map[model.TaskMode]TaskExecutor
	mu        sync.RWMutex

	// 路由规则（SourceID pattern -> TaskMode）
	routingRules map[string]model.TaskMode
	routingMu    sync.RWMutex

	// 默认模式
	defaultMode model.TaskMode

	// 统计信息
	stats CoordinatorStats
}

// TaskExecutor 基础任务执行器接口
type TaskExecutor interface {
	Submit(ctx context.Context, task func(context.Context)) error
}

// CoordinatorStats 协调器统计信息
type CoordinatorStats struct {
	TotalSubmitted int64 // 总提交任务数
	ByMode         map[model.TaskMode]int64
	mu             sync.RWMutex
}

// NewTaskCoordinator 创建新的任务协调器
func NewTaskCoordinator() *TaskCoordinator {
	return &TaskCoordinator{
		executors:    make(map[model.TaskMode]TaskExecutor),
		routingRules: make(map[string]model.TaskMode),
		defaultMode:  model.ModeDefaultPool,
		stats: CoordinatorStats{
			ByMode: make(map[model.TaskMode]int64),
		},
	}
}

// RegisterExecutor 注册执行器
func (c *TaskCoordinator) RegisterExecutor(mode model.TaskMode, executor TaskExecutor) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.executors[mode]; exists {
		return ErrExecutorAlreadyRegistered
	}

	c.executors[mode] = executor
	return nil
}

// UnregisterExecutor 注销执行器
func (c *TaskCoordinator) UnregisterExecutor(mode model.TaskMode) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.executors, mode)
}

// GetExecutor 获取执行器
func (c *TaskCoordinator) GetExecutor(mode model.TaskMode) TaskExecutor {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.executors[mode]
}

// SetDefaultMode 设置默认模式
func (c *TaskCoordinator) SetDefaultMode(mode model.TaskMode) {
	c.defaultMode = mode
}

// AddRoutingRule 添加路由规则
func (c *TaskCoordinator) AddRoutingRule(pattern string, mode model.TaskMode) error {
	c.routingMu.Lock()
	defer c.routingMu.Unlock()

	c.routingRules[pattern] = mode
	return nil
}

// RemoveRoutingRule 移除路由规则
func (c *TaskCoordinator) RemoveRoutingRule(pattern string) {
	c.routingMu.Lock()
	defer c.routingMu.Unlock()

	delete(c.routingRules, pattern)
}

// Route 根据 SourceID 路由到合适的执行模式
func (c *TaskCoordinator) Route(sourceID model.SourceID) model.TaskMode {
	// 1. 检查自定义路由规则
	c.routingMu.RLock()
	for pattern, mode := range c.routingRules {
		if sourceID.Match(pattern) {
			c.routingMu.RUnlock()
			return mode
		}
	}
	c.routingMu.RUnlock()

	// 2. 使用 SourceID 推荐模式
	recommendedMode := sourceID.RecommendedMode()

	// 3. 检查推荐模式的执行器是否存在
	c.mu.RLock()
	_, exists := c.executors[recommendedMode]
	c.mu.RUnlock()

	if exists {
		return recommendedMode
	}

	// 4. 降级到默认模式
	return c.defaultMode
}

// Submit 提交任务
func (c *TaskCoordinator) Submit(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	// 1. 路由到合适的模式
	mode := c.Route(sourceID)

	// 2. 获取执行器
	c.mu.RLock()
	executor, exists := c.executors[mode]
	c.mu.RUnlock()

	if !exists {
		// 降级到默认执行器
		c.mu.RLock()
		executor, exists = c.executors[c.defaultMode]
		c.mu.RUnlock()

		if !exists {
			return ErrNoExecutorAvailable
		}
	}

	// 3. 更新统计（统一使用 mutex，避免混用 atomic 和 mutex 导致竞态）
	c.stats.mu.Lock()
	c.stats.TotalSubmitted++
	c.stats.ByMode[mode]++
	c.stats.mu.Unlock()

	// 4. 提交任务
	return executor.Submit(ctx, task)
}

// SubmitWithMode 使用指定模式提交任务
func (c *TaskCoordinator) SubmitWithMode(ctx context.Context, mode model.TaskMode, task func(context.Context)) error {
	c.mu.RLock()
	executor, exists := c.executors[mode]
	c.mu.RUnlock()

	if !exists {
		return ErrNoExecutorAvailable
	}

	// 更新统计
	atomic.AddInt64(&c.stats.TotalSubmitted, 1)
	c.stats.mu.Lock()
	c.stats.ByMode[mode]++
	c.stats.mu.Unlock()

	return executor.Submit(ctx, task)
}

// Close 关闭协调器
func (c *TaskCoordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var lastErr error
	for mode, executor := range c.executors {
		if closer, ok := executor.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				lastErr = err
			}
		}
		delete(c.executors, mode)
	}

	return lastErr
}

// Stats 获取统计信息
func (c *TaskCoordinator) Stats() *CoordinatorStats {
	c.stats.mu.RLock()
	defer c.stats.mu.RUnlock()

	// 复制统计信息
	result := &CoordinatorStats{
		TotalSubmitted: atomic.LoadInt64(&c.stats.TotalSubmitted),
		ByMode:         make(map[model.TaskMode]int64),
	}

	for mode, count := range c.stats.ByMode {
		result.ByMode[mode] = count
	}

	return result
}

// AvailableModes 返回可用的执行模式列表
func (c *TaskCoordinator) AvailableModes() []model.TaskMode {
	c.mu.RLock()
	defer c.mu.RUnlock()

	modes := make([]model.TaskMode, 0, len(c.executors))
	for mode := range c.executors {
		modes = append(modes, mode)
	}
	return modes
}

// HasMode 检查指定模式是否可用
func (c *TaskCoordinator) HasMode(mode model.TaskMode) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, exists := c.executors[mode]
	return exists
}

// ==========================================
// 错误定义
// ==========================================

var (
	// ErrExecutorAlreadyRegistered 执行器已注册
	ErrExecutorAlreadyRegistered = &CoordinatorError{
		Code:    "EXECUTOR_ALREADY_REGISTERED",
		Message: "executor already registered for this mode",
	}

	// ErrNoExecutorAvailable 没有可用的执行器
	ErrNoExecutorAvailable = &CoordinatorError{
		Code:    "NO_EXECUTOR_AVAILABLE",
		Message: "no executor available for the requested mode",
	}
)

// CoordinatorError 协调器错误
type CoordinatorError struct {
	Code    string
	Message string
}

func (e *CoordinatorError) Error() string {
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
func (c *TaskCoordinator) ParseAndSubmit(ctx context.Context, sourceIDStr string, task func(context.Context)) error {
	sourceID, err := model.ParseSourceID(sourceIDStr)
	if err != nil {
		return err
	}
	return c.Submit(ctx, sourceID, task)
}

// SubmitHighPriority 提交高优先级任务
func (c *TaskCoordinator) SubmitHighPriority(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	// 高优先级任务使用 PerCore 模式（如果可用）
	if c.HasMode(model.ModePerCore) {
		return c.SubmitWithMode(ctx, model.ModePerCore, task)
	}
	return c.Submit(ctx, sourceID, task)
}

// SubmitBackground 提交后台任务
func (c *TaskCoordinator) SubmitBackground(ctx context.Context, sourceID model.SourceID, task func(context.Context)) error {
	// 后台任务使用 CustomPool 模式（如果可用）
	if c.HasMode(model.ModeCustomPool) {
		return c.SubmitWithMode(ctx, model.ModeCustomPool, task)
	}
	return c.Submit(ctx, sourceID, task)
}

// GetMetrics 获取指标（用于 Prometheus）
func (c *TaskCoordinator) GetMetrics() map[string]int64 {
	stats := c.Stats()

	result := make(map[string]int64)
	result["total_submitted"] = stats.TotalSubmitted

	for mode, count := range stats.ByMode {
		key := "mode_" + strings.ReplaceAll(mode.String(), "-", "_")
		result[key] = count
	}

	return result
}
