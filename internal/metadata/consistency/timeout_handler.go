// Package consistency 提供 2PC 强一致性协调器实现
//
// P1-1: 2PC 超时处理优化
// 使用 sync.Cond 条件变量替代硬编码的 time.Sleep
// 实现超时策略配置化
package consistency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
)

// ==================== 超时策略配置 ====================

// TimeoutPolicy 超时策略配置
type TimeoutPolicy struct {
	// PreCommitTimeout PreCommit 阶段超时时间（默认 5 秒）
	PreCommitTimeout time.Duration

	// CommitTimeout Commit 阶段超时时间（默认 3 秒）
	CommitTimeout time.Duration

	// AckWaitTimeout 等待 ACK 响应的超时时间（默认 5 秒）
	AckWaitTimeout time.Duration

	// RollbackTimeout 回滚操作超时时间（默认 3 秒）
	RollbackTimeout time.Duration

	// GossipQueryTimeout Gossip 查询超时时间（默认 10 秒）
	GossipQueryTimeout time.Duration

	// CleanupInterval 清理超时事务的间隔（默认 30 秒）
	CleanupInterval time.Duration

	// RetryCount 重试次数（默认 3 次）
	RetryCount int

	// RetryDelay 重试间隔（默认 500 毫秒）
	RetryDelay time.Duration

	// EnableAdaptiveTimeout 是否启用自适应超时（根据网络延迟动态调整）
	EnableAdaptiveTimeout bool
}

// DefaultTimeoutPolicy 返回默认超时策略
func DefaultTimeoutPolicy() *TimeoutPolicy {
	return &TimeoutPolicy{
		PreCommitTimeout:      5 * time.Second,
		CommitTimeout:         3 * time.Second,
		AckWaitTimeout:        5 * time.Second,
		RollbackTimeout:       3 * time.Second,
		GossipQueryTimeout:    10 * time.Second,
		CleanupInterval:       30 * time.Second,
		RetryCount:            3,
		RetryDelay:            500 * time.Millisecond,
		EnableAdaptiveTimeout: false,
	}
}

// Validate 验证超时策略配置
func (p *TimeoutPolicy) Validate() error {
	if p.PreCommitTimeout <= 0 {
		p.PreCommitTimeout = 5 * time.Second
	}
	if p.CommitTimeout <= 0 {
		p.CommitTimeout = 3 * time.Second
	}
	if p.AckWaitTimeout <= 0 {
		p.AckWaitTimeout = 5 * time.Second
	}
	if p.RollbackTimeout <= 0 {
		p.RollbackTimeout = 3 * time.Second
	}
	if p.GossipQueryTimeout <= 0 {
		p.GossipQueryTimeout = 10 * time.Second
	}
	if p.CleanupInterval <= 0 {
		p.CleanupInterval = 30 * time.Second
	}
	if p.RetryCount <= 0 {
		p.RetryCount = 3
	}
	if p.RetryDelay <= 0 {
		p.RetryDelay = 500 * time.Millisecond
	}
	return nil
}

// ==================== 条件等待器 ====================

// ConditionWaiter 条件等待器
//
// 使用 sync.Cond 实现可中断的条件等待
type ConditionWaiter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	done   bool
	result any
	err    error
}

// NewConditionWaiter 创建条件等待器
func NewConditionWaiter() *ConditionWaiter {
	w := &ConditionWaiter{}
	w.cond = sync.NewCond(&w.mu)
	return w
}

// Wait 等待条件满足或超时
//
// 参数：
//   - timeout: 超时时间
//   - checkFn: 条件检查函数，返回 true 表示条件满足
//
// 返回：
//   - bool: 条件是否满足（true=满足，false=超时）
func (w *ConditionWaiter) Wait(timeout time.Duration, checkFn func() bool) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 检查是否已完成
	if w.done {
		return true
	}

	// 启动超时 goroutine
	timeoutCh := make(chan struct{})
	go func() {
		time.Sleep(timeout)
		close(timeoutCh)
		w.cond.Broadcast() // 超时时唤醒等待者
	}()

	// 等待条件满足或超时
	for !w.done && !checkFn() {
		// 非阻塞检查超时
		select {
		case <-timeoutCh:
			return false // 超时
		default:
		}

		w.cond.Wait()

		// 再次检查超时
		select {
		case <-timeoutCh:
			return false // 超时
		default:
		}
	}

	return w.done || checkFn()
}

// WaitWithContext 带上下文的条件等待
//
// 参数：
//   - ctx: 上下文（支持取消）
//   - checkFn: 条件检查函数
//
// 返回：
//   - bool: 条件是否满足
//   - error: 上下文取消时的错误
func (w *ConditionWaiter) WaitWithContext(ctx context.Context, checkFn func() bool) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 检查是否已完成
	if w.done {
		return true, nil
	}

	// 启动上下文监听 goroutine
	go func() {
		<-ctx.Done()
		w.cond.Broadcast() // 上下文取消时唤醒等待者
	}()

	// 等待条件满足或上下文取消
	for !w.done && !checkFn() {
		// 检查上下文是否取消
		if ctx.Err() != nil {
			return false, ctx.Err()
		}

		w.cond.Wait()

		// 再次检查上下文
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
	}

	return w.done || checkFn(), nil
}

// Signal 发送信号表示条件已满足
func (w *ConditionWaiter) Signal(result any, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.done = true
	w.result = result
	w.err = err
	w.cond.Broadcast()
}

// GetResult 获取结果
func (w *ConditionWaiter) GetResult() (any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.result, w.err
}

// Reset 重置等待器（可重用）
func (w *ConditionWaiter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.done = false
	w.result = nil
	w.err = nil
}

// ==================== ACK 收集器 ====================

// AckCollector ACK 收集器
//
// 使用 sync.Cond 实现 ACK 收集的等待
type AckCollector struct {
	mu        sync.Mutex
	cond      *sync.Cond
	expected  int             // 期望的 ACK 数量
	received  int             // 已收到的 ACK 数量
	acks      map[string]bool // 节点 ID -> ACK 状态
	failed    int             // 失败的节点数量
	timeout   time.Duration   // 超时时间
	startTime time.Time       // 开始时间
}

// NewAckCollector 创建 ACK 收集器
func NewAckCollector(expected int, timeout time.Duration) *AckCollector {
	c := &AckCollector{
		expected:  expected,
		acks:      make(map[string]bool),
		timeout:   timeout,
		startTime: time.Now(),
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// ReceiveACK 接收 ACK
func (c *AckCollector) ReceiveACK(nodeID string, success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 检查是否已经收到过
	if _, exists := c.acks[nodeID]; exists {
		return
	}

	c.acks[nodeID] = success
	if success {
		c.received++
	} else {
		c.failed++
	}

	// 检查是否达到期望数量
	if c.received >= c.expected || c.failed > 0 {
		c.cond.Broadcast()
	}
}

// WaitAll 等待所有 ACK
//
// 返回：
//   - int: 成功收到的 ACK 数量
//   - int: 失败的节点数量
//   - bool: 是否成功（达到期望数量）
func (c *AckCollector) WaitAll() (successCount int, failedCount int, success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 已经满足条件
	if c.received >= c.expected {
		return c.received, c.failed, true
	}

	// 启动超时 goroutine
	go func() {
		time.Sleep(c.timeout)
		c.cond.Broadcast()
	}()

	// 等待条件满足或超时
	for c.received < c.expected && c.failed == 0 {
		// 检查是否超时
		if time.Since(c.startTime) > c.timeout {
			break
		}
		c.cond.Wait()
	}

	return c.received, c.failed, c.received >= c.expected
}

// WaitWithContext 带上下文的等待
func (c *AckCollector) WaitWithContext(ctx context.Context) (int, int, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 已经满足条件
	if c.received >= c.expected {
		return c.received, c.failed, true, nil
	}

	// 启动上下文监听
	go func() {
		<-ctx.Done()
		c.cond.Broadcast()
	}()

	// 启动超时监听
	go func() {
		time.Sleep(c.timeout)
		c.cond.Broadcast()
	}()

	// 等待
	for c.received < c.expected && c.failed == 0 {
		// 检查上下文
		if ctx.Err() != nil {
			return c.received, c.failed, false, ctx.Err()
		}
		// 检查超时
		if time.Since(c.startTime) > c.timeout {
			break
		}
		c.cond.Wait()
	}

	return c.received, c.failed, c.received >= c.expected, nil
}

// GetProgress 获取进度
func (c *AckCollector) GetProgress() (received, expected int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.received, c.expected
}

// ==================== 超时管理器 ====================

// TimeoutManager 超时管理器
type TimeoutManager struct {
	mu     sync.RWMutex
	policy *TimeoutPolicy

	// 自适应超时统计
	avgLatency   time.Duration
	sampleCount  int64
	totalLatency time.Duration
}

// NewTimeoutManager 创建超时管理器
func NewTimeoutManager(policy *TimeoutPolicy) *TimeoutManager {
	if policy == nil {
		policy = DefaultTimeoutPolicy()
	}
	if err := policy.Validate(); err != nil {
		logging.Warn("TimeoutPolicy validation failed, using default: ", err)
		policy = DefaultTimeoutPolicy()
	}

	return &TimeoutManager{
		policy: policy,
	}
}

// GetPolicy 获取当前超时策略
func (m *TimeoutManager) GetPolicy() *TimeoutPolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policy
}

// SetPolicy 设置超时策略
func (m *TimeoutManager) SetPolicy(policy *TimeoutPolicy) {
	if policy == nil {
		policy = DefaultTimeoutPolicy()
	}
	if err := policy.Validate(); err != nil {
		logging.Warn("TimeoutPolicy validation failed: ", err)
		policy = DefaultTimeoutPolicy()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = policy

	logging.WithField("policy", policy).Info("超时策略已更新")
}

// GetPreCommitTimeout 获取 PreCommit 超时时间
func (m *TimeoutManager) GetPreCommitTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.policy.EnableAdaptiveTimeout && m.avgLatency > 0 {
		// 自适应超时：平均延迟的 3 倍
		adaptiveTimeout := m.avgLatency * 3
		if adaptiveTimeout < m.policy.PreCommitTimeout {
			return adaptiveTimeout
		}
	}
	return m.policy.PreCommitTimeout
}

// GetCommitTimeout 获取 Commit 超时时间
func (m *TimeoutManager) GetCommitTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policy.CommitTimeout
}

// GetAckWaitTimeout 获取 ACK 等待超时时间
func (m *TimeoutManager) GetAckWaitTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.policy.EnableAdaptiveTimeout && m.avgLatency > 0 {
		adaptiveTimeout := m.avgLatency * 3
		if adaptiveTimeout < m.policy.AckWaitTimeout {
			return adaptiveTimeout
		}
	}
	return m.policy.AckWaitTimeout
}

// RecordLatency 记录延迟样本（用于自适应超时）
func (m *TimeoutManager) RecordLatency(latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sampleCount++
	m.totalLatency += latency
	m.avgLatency = time.Duration(int64(m.totalLatency) / m.sampleCount)
}

// GetAverageLatency 获取平均延迟
func (m *TimeoutManager) GetAverageLatency() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.avgLatency
}

// NewAckCollector 创建 ACK 收集器（使用当前策略）
func (m *TimeoutManager) NewAckCollector(expected int) *AckCollector {
	return NewAckCollector(expected, m.GetAckWaitTimeout())
}

// NewConditionWaiter 创建条件等待器
func (m *TimeoutManager) NewConditionWaiter() *ConditionWaiter {
	return NewConditionWaiter()
}

// ==================== 超时上下文 ====================

// TimeoutContext 超时上下文
type TimeoutContext struct {
	context.Context
	manager *TimeoutManager
	cancel  context.CancelFunc
}

// NewTimeoutContext 创建超时上下文
func (m *TimeoutManager) NewTimeoutContext(parent context.Context, timeoutType string) *TimeoutContext {
	var timeout time.Duration

	switch timeoutType {
	case "precommit":
		timeout = m.GetPreCommitTimeout()
	case "commit":
		timeout = m.GetCommitTimeout()
	case "ack":
		timeout = m.GetAckWaitTimeout()
	case "rollback":
		timeout = m.policy.RollbackTimeout
	case "gossip":
		timeout = m.policy.GossipQueryTimeout
	default:
		timeout = m.policy.PreCommitTimeout
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	return &TimeoutContext{
		Context: ctx,
		manager: m,
		cancel:  cancel,
	}
}

// Cancel 取消上下文
func (c *TimeoutContext) Cancel() {
	if c.cancel != nil {
		c.cancel()
	}
}

// ==================== 重试策略 ====================

// RetryableOperation 可重试的操作
type RetryableOperation struct {
	manager *TimeoutManager
}

// NewRetryableOperation 创建可重试操作
func (m *TimeoutManager) NewRetryableOperation() *RetryableOperation {
	return &RetryableOperation{manager: m}
}

// Execute 执行可重试操作
func (r *RetryableOperation) Execute(fn func() error) error {
	var lastErr error
	policy := r.manager.GetPolicy()

	for i := 0; i < policy.RetryCount; i++ {
		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// 检查是否可重试
		if !isRetryableError(err) {
			return err
		}

		// 等待重试
		if i < policy.RetryCount-1 {
			time.Sleep(policy.RetryDelay)
		}
	}

	return fmt.Errorf("操作失败，重试 %d 次后仍不成功: %w", policy.RetryCount, lastErr)
}

// ExecuteWithContext 执行可重试操作（带上下文）
func (r *RetryableOperation) ExecuteWithContext(ctx context.Context, fn func() error) error {
	var lastErr error
	policy := r.manager.GetPolicy()

	for i := 0; i < policy.RetryCount; i++ {
		// 检查上下文
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// 检查是否可重试
		if !isRetryableError(err) {
			return err
		}

		// 等待重试
		if i < policy.RetryCount-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(policy.RetryDelay):
			}
		}
	}

	return fmt.Errorf("操作失败，重试 %d 次后仍不成功: %w", policy.RetryCount, lastErr)
}

// 注意：isRetryableError 函数在 twopc_coordinator.go 中已定义，此处不再重复
