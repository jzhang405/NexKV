// Package model 定义领域模型
package model

import (
	"context"
	"sync"
	"sync/atomic"
)

// ==========================================
// TaskStatus 任务状态定义
// ==========================================

// TaskStatus 任务状态
// 用于 TaskScheduler 和 BaseTask 的统一状态枚举
type TaskStatus int

const (
	// TaskQueued 任务已入队，等待执行
	TaskQueued TaskStatus = iota
	// TaskExecuting 正在执行（Peek 成功后）
	TaskExecuting
	// TaskPassed 执行成功，需要 Dequeue
	TaskPassed
	// TaskFailed 执行失败，需要 Dequeue
	TaskFailed
	// TaskTimeout 超时，需要 Dequeue
	TaskTimeout
	// TaskBusy 繁忙/资源冲突（可重试）
	// 用于表示任务因临时资源冲突（如锁竞争）而无法完成，需要保留在队列中重试
	TaskBusy
	// TaskRetrying 需要重试，保留在队列
	TaskRetrying
	// TaskCompleted 任务成功完成（与 OperationStatus 兼容）
	TaskCompleted
)

// String 返回状态字符串
func (s TaskStatus) String() string {
	switch s {
	case TaskQueued:
		return "queued"
	case TaskExecuting:
		return "executing"
	case TaskPassed:
		return "passed"
	case TaskFailed:
		return "failed"
	case TaskTimeout:
		return "timeout"
	case TaskBusy:
		return "busy"
	case TaskRetrying:
		return "retrying"
	case TaskCompleted:
		return "completed"
	default:
		return "unknown"
	}
}

// ==========================================
// OperationStatus 操作状态定义（保留兼容性）
// ==========================================

// OperationStatus 操作状态
type OperationStatus int

const (
	// StatusPending 操作待执行
	StatusPending OperationStatus = iota
	// StatusRunning 操作正在执行
	StatusRunning
	// StatusCompleted 操作成功完成
	StatusCompleted
	// StatusFailed 操作失败
	StatusFailed
	// StatusCanceled 操作被取消
	StatusCanceled
	// StatusDiscarded 操作结果被丢弃
	StatusDiscarded
	// StatusTimeout 操作超时
	StatusTimeout
)

// IsTerminal 检查是否为终态
func (s OperationStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCanceled, StatusDiscarded, StatusTimeout:
		return true
	default:
		return false
	}
}

// String 返回状态字符串表示
func (s OperationStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCanceled:
		return "canceled"
	case StatusDiscarded:
		return "discarded"
	case StatusTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// ==========================================
// TaskRunner 非泛型任务接口（Executor 视角）
// ==========================================

// TaskRunnerContext 任务执行上下文接口（避免循环依赖）
// 实际实现见 service.TaskRunnerContext
type TaskRunnerContext interface {
	// Submit 提交子任务
	Submit(task TaskRunner) error

	// Executor 获取执行器
	Executor() TaskExecutorRef
}

// TaskExecutorRef TaskExecutor 引用（避免循环依赖）
// 实际类型是 service.TaskExecutor
type TaskExecutorRef any

// TaskRunner 非泛型任务接口
// Executor 只看到这个接口，用于任务调度和执行
// 这是类型擦除点，允许 Executor 统一处理不同类型的任务
type TaskRunner interface {
	// Run 执行任务
	// Pipeline 提供执行上下文和依赖
	Run(ctx context.Context, trCtx TaskRunnerContext)

	// Priority 返回任务优先级
	Priority() TaskPriority

	// SourceID 返回任务来源标识（用于 CPU 亲和性）
	SourceID() SourceID
}

// ==========================================
// TaskResult 任务结果接口
// ==========================================

// TaskResult 任务结果接口
// 提供任务执行结果的查询能力
// 用于接口组合，允许不同类型统一访问任务状态和结果
type TaskResult interface {
	// Done 返回一个只读 channel，在任务完成时关闭
	Done() <-chan struct{}

	// WaitAny 等待任务完成并返回结果
	// 返回的结果类型为 any，调用方需要进行类型断言
	WaitAny(ctx context.Context) (any, error)

	// Status 返回任务状态（统一为 TaskStatus）
	Status() TaskStatus

	// IsDone 检查任务是否完成
	IsDone() bool

	// GetError 获取任务执行错误
	// 如果任务成功完成，返回 nil
	GetError() error
}

// ==========================================
// Task[Result] 泛型任务接口（用户视角）
// ==========================================

// Task[Result] 泛型任务接口
// 用户实现此接口，提供类型安全的任务执行
// 嵌入 TaskRunner 实现类型擦除
type Task[Result any] interface {
	TaskRunner

	// Execute 执行任务并返回类型化结果
	// 这是用户实现的主要方法
	Execute(ctx context.Context, trCtx TaskRunnerContext) (Result, error)

	// Wait 等待任务完成并返回结果
	Wait(ctx context.Context) (Result, error)

	// IsDone 检查任务是否完成
	IsDone() bool
}

// ==========================================
// BaseTask[Result] 任务基类
// ==========================================

// ExecuteFunc 执行函数类型
type ExecuteFunc[Result any] func(ctx context.Context, trCtx TaskRunnerContext) (Result, error)

// BaseTask[Result] 任务基类
// 提供通用实现，用户传入 ExecuteFunc
type BaseTask[Result any] struct {
	// ===== 任务元数据 =====
	priority TaskPriority // 任务优先级
	// 注意：sourceID 已移除，TaskScheduler 场景不需要此字段
	// 如需 CPU 亲和性，请使用 PerCoreExecutor 并在外部管理 sourceID

	// ===== 执行函数 =====
	execute ExecuteFunc[Result]

	// ===== 重试管理 =====
	retryCount int        // 当前重试次数
	maxRetries int        // 最大重试次数（0 表示不重试）
	muRetry    sync.Mutex // 保护 retryCount

	// ===== 任务状态 =====
	done   chan struct{}
	status atomic.Int32 // TaskStatus

	// ===== 任务结果 =====
	result Result
	err    error
	mu     sync.RWMutex // 保护 result 和 err
}

// NewBaseTask 创建基础任务
// 参数：
//   - priority: 任务优先级
//   - maxRetries: 最大重试次数（0 表示不重试）
//   - execute: 执行函数
func NewBaseTask[Result any](priority TaskPriority, maxRetries int, execute ExecuteFunc[Result]) *BaseTask[Result] {
	return &BaseTask[Result]{
		priority:   priority,
		execute:    execute,
		maxRetries: maxRetries,
		done:       make(chan struct{}),
	}
}

// Run 实现 TaskRunner 接口
// 这是通用的执行逻辑，调用 Execute 方法并处理结果
func (b *BaseTask[Result]) Run(ctx context.Context, trCtx TaskRunnerContext) {
	// 使用 CAS 操作确保只有一个 goroutine 执行任务
	// 只有当状态为 TaskQueued 时才能转换为 TaskExecuting
	if !b.status.CompareAndSwap(int32(TaskQueued), int32(TaskExecuting)) {
		// 其他 goroutine 已经在执行或已完成
		return
	}

	// 执行任务（调用 Execute 方法）
	result, err := b.Execute(ctx, trCtx)

	// 保存结果
	b.mu.Lock()
	b.result = result
	b.err = err
	if err != nil {
		b.status.Store(int32(TaskFailed))
	} else {
		b.status.Store(int32(TaskCompleted))
	}
	b.mu.Unlock()

	// 关闭 done channel（只关闭一次，因为只有一个 goroutine 能到达这里）
	close(b.done)
}

// Execute 实现 Task[Result] 接口
// 调用内部的 execute 函数
func (b *BaseTask[Result]) Execute(ctx context.Context, trCtx TaskRunnerContext) (Result, error) {
	if b.execute != nil {
		return b.execute(ctx, trCtx)
	}
	var zero Result
	return zero, nil
}

// Priority 实现 TaskRunner 接口
func (b *BaseTask[Result]) Priority() TaskPriority {
	return b.priority
}

// SourceID 实现 TaskRunner 接口
// 返回默认值（TaskScheduler 场景不需要此字段）
func (b *BaseTask[Result]) SourceID() SourceID {
	return SourceDefault // 默认通用任务
}

// ==========================================
// 重试管理方法（ShardItem 接口需要）
// ==========================================

// MaxRetries 返回最大重试次数
// 0 表示不重试
func (b *BaseTask[Result]) MaxRetries() int {
	b.muRetry.Lock()
	defer b.muRetry.Unlock()
	return b.maxRetries
}

// IncAttempts 增加尝试次数并返回当前次数
// 返回值 > MaxRetries() 时表示已超过最大重试次数
func (b *BaseTask[Result]) IncAttempts() int {
	b.muRetry.Lock()
	defer b.muRetry.Unlock()
	b.retryCount++
	return b.retryCount
}

// ==========================================
// 错误获取方法（TaskResult 接口需要）
// ==========================================

// GetError 获取任务执行错误
// 如果任务成功完成，返回 nil
// 如果任务尚未完成，阻塞直到完成
func (b *BaseTask[Result]) GetError() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.err
}

// Execute 方法不在此实现，必须由具体的 Task 类型实现
// BaseTask 只提供通用的状态管理和等待机制

// Wait 等待任务完成并返回结果
// 实现 Task[Result] 接口
func (b *BaseTask[Result]) Wait(ctx context.Context) (Result, error) {
	select {
	case <-b.done:
		b.mu.RLock()
		defer b.mu.RUnlock()
		return b.result, b.err
	case <-ctx.Done():
		var zero Result
		return zero, ctx.Err()
	}
}

// WaitAny 等待任务完成并返回结果（any 类型）
// 实现 TaskResult 接口
// 允许通过 TaskResult 接口统一获取任务结果
func (b *BaseTask[Result]) WaitAny(ctx context.Context) (any, error) {
	result, err := b.Wait(ctx)
	return result, err
}

// Done 返回 done channel（用于 select）
func (b *BaseTask[Result]) Done() <-chan struct{} {
	return b.done
}

// IsDone 检查任务是否完成
func (b *BaseTask[Result]) IsDone() bool {
	select {
	case <-b.done:
		return true
	default:
		return false
	}
}

// GetResult 获取结果（如果已完成）
func (b *BaseTask[Result]) GetResult() (Result, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.result, b.err
}

// Status 获取任务状态（统一为 TaskStatus）
func (b *BaseTask[Result]) Status() TaskStatus {
	return TaskStatus(b.status.Load())
}

// TaskPriority 任务优先级
// 遵循 Unix 传统定义 10 级优先级（0 最高，9 最低）
type TaskPriority int

const (
	// Unix 传统：0 最高，9 最低
	TaskPriorityCritical   TaskPriority = iota // 0级：核心优先级（最高）- 如系统关键任务、心跳检测
	TaskPriorityHigh                           // 1级：高优先级 - 如实时数据同步、用户核心操作
	TaskPriorityUrgent                         // 2级：紧急优先级 - 如交易结算、订单处理
	TaskPriorityImportant                      // 3级：重要优先级 - 如业务逻辑计算
	TaskPriorityNormalHigh                     // 4级：次高正常优先级 - 如高频查询
	TaskPriorityNormal                         // 5级：正常优先级 - 常规业务操作（默认）
	TaskPriorityNormalLow                      // 6级：次低正常优先级 - 如非实时统计
	TaskPriorityLow                            // 7级：低优先级 - 如日志批量处理
	TaskPriorityBackground                     // 8级：后台优先级 - 如数据归档
	TaskPriorityIdle                           // 9级：空闲优先级（最低）- 如资源清理、冷数据同步
)

// String 返回优先级字符串
func (p TaskPriority) String() string {
	switch p {
	case TaskPriorityCritical:
		return "critical"
	case TaskPriorityHigh:
		return "high"
	case TaskPriorityUrgent:
		return "urgent"
	case TaskPriorityImportant:
		return "important"
	case TaskPriorityNormalHigh:
		return "normal-high"
	case TaskPriorityNormal:
		return "normal"
	case TaskPriorityNormalLow:
		return "normal-low"
	case TaskPriorityLow:
		return "low"
	case TaskPriorityBackground:
		return "background"
	case TaskPriorityIdle:
		return "idle"
	default:
		return "unknown"
	}
}

// ==========================================
// 统计和健康状态
// ==========================================

// TaskPoolStats 任务池统计信息
type TaskPoolStats struct {
	Total      int
	ByPriority map[TaskPriority]int
	Running    int
	Waiting    int
	Capacity   int
}

// TaskHealthStatus 健康状态
type TaskHealthStatus int

const (
	TaskHealthStatusHealthy TaskHealthStatus = iota
	TaskHealthStatusUnhealthy
)

// String 返回健康状态字符串
func (s TaskHealthStatus) String() string {
	switch s {
	case TaskHealthStatusHealthy:
		return "healthy"
	case TaskHealthStatusUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}

// ==========================================
// BaseTask 对象池（性能优化）
// ==========================================

// baseTaskPool BaseTask 对象池，复用任务对象减少内存分配
// 用于 struct{} 类型（无返回值场景，占 80%+ 使用）
var baseTaskPool = sync.Pool{
	New: func() any {
		return &BaseTask[struct{}]{
			done: make(chan struct{}),
		}
	},
}

// GetPooledTask 从对象池获取任务（优化性能）
// 注意：使用完毕后必须调用 ReleasePooledTask 归还到池
func GetPooledTask(priority TaskPriority, maxRetries int, execute ExecuteFunc[struct{}]) *BaseTask[struct{}] {
	task := baseTaskPool.Get().(*BaseTask[struct{}])

	// 快速路径：只重置必要字段
	task.priority = priority
	task.execute = execute
	task.maxRetries = maxRetries
	task.retryCount = 0

	// 重置状态
	task.status.Store(int32(TaskQueued))

	// 复用 channel（不断开重连）
	select {
	case <-task.done:
		// 已关闭，创建新的
		task.done = make(chan struct{})
	default:
		// 未关闭，复用
	}

	return task
}

// ReleasePooledTask 归还任务到对象池
// 注意：任务执行完成后才能归还，避免并发问题
func ReleasePooledTask(task *BaseTask[struct{}]) {
	// 确保 channel 已关闭，避免资源泄漏
	select {
	case <-task.done:
		// 已关闭，正常归还
	default:
		// 未关闭，先关闭
		close(task.done)
	}

	baseTaskPool.Put(task)
}
