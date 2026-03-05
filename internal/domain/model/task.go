// Package model 定义领域模型
package model

import (
	"context"
	"sync"
	"sync/atomic"
)

// ==========================================
// OpType 操作类型定义
// ==========================================

// OpType 操作类型
// 用于标识任务的来源和类型
type OpType int

const (
	// OpRPC RPC 调用操作
	OpRPC OpType = iota
	// OpStorage 存储操作
	OpStorage
	// OpRaft Raft 协议操作
	OpRaft
	// OpCompaction 压缩操作
	OpCompaction
	// OpSnapshot 快照操作
	OpSnapshot
)

// String 返回操作类型字符串表示
func (o OpType) String() string {
	switch o {
	case OpRPC:
		return "rpc"
	case OpStorage:
		return "storage"
	case OpRaft:
		return "raft"
	case OpCompaction:
		return "compaction"
	case OpSnapshot:
		return "snapshot"
	default:
		return "unknown"
	}
}

// ==========================================
// OperationStatus 操作状态定义
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

// PipelineContext Pipeline 接口（避免循环依赖）
// 实际实现见 Pipeline 结构体
type PipelineContext interface {
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
	Run(ctx context.Context, pipeline PipelineContext)

	// Priority 返回任务优先级
	Priority() TaskPriority

	// SourceID 返回任务来源标识（用于 CPU 亲和性）
	SourceID() SourceID
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
	Execute(ctx context.Context, pipeline PipelineContext) (Result, error)

	// Wait 等待任务完成并返回结果
	Wait(ctx context.Context) (Result, error)

	// IsDone 检查任务是否完成
	IsDone() bool
}

// ==========================================
// BaseTask[Result] 任务基类
// ==========================================

// ExecuteFunc 执行函数类型
type ExecuteFunc[Result any] func(ctx context.Context, pipeline PipelineContext) (Result, error)

// BaseTask[Result] 任务基类
// 提供通用实现，用户传入 ExecuteFunc
type BaseTask[Result any] struct {
	// 任务元数据
	opType   OpType
	priority TaskPriority
	sourceID SourceID

	// 执行函数
	execute ExecuteFunc[Result]

	// 任务状态
	done   chan struct{}
	status atomic.Int32 // OperationStatus

	// 任务结果
	result Result
	err    error
	mu     sync.RWMutex // 保护 result 和 err
}

// NewBaseTask 创建基础任务
func NewBaseTask[Result any](opType OpType, priority TaskPriority, sourceID SourceID, execute ExecuteFunc[Result]) *BaseTask[Result] {
	return &BaseTask[Result]{
		opType:   opType,
		priority: priority,
		sourceID: sourceID,
		execute:  execute,
		done:     make(chan struct{}),
	}
}

// Run 实现 TaskRunner 接口
// 这是通用的执行逻辑，调用 Execute 方法并处理结果
func (b *BaseTask[Result]) Run(ctx context.Context, pipeline PipelineContext) {
	// 检查是否已完成
	if b.IsDone() {
		return
	}

	// 设置运行状态
	b.status.Store(int32(StatusRunning))

	// 执行任务（调用 Execute 方法）
	result, err := b.Execute(ctx, pipeline)

	// 保存结果
	b.mu.Lock()
	b.result = result
	b.err = err
	if err != nil {
		b.status.Store(int32(StatusFailed))
	} else {
		b.status.Store(int32(StatusCompleted))
	}
	b.mu.Unlock()

	// 关闭 done channel
	close(b.done)
}

// Execute 实现 Task[Result] 接口
// 调用内部的 execute 函数
func (b *BaseTask[Result]) Execute(ctx context.Context, pipeline PipelineContext) (Result, error) {
	if b.execute != nil {
		return b.execute(ctx, pipeline)
	}
	var zero Result
	return zero, nil
}

// Priority 实现 TaskRunner 接口
func (b *BaseTask[Result]) Priority() TaskPriority {
	return b.priority
}

// SourceID 实现 TaskRunner 接口
func (b *BaseTask[Result]) SourceID() SourceID {
	return b.sourceID
}

// Execute 方法不在此实现，必须由具体的 Task 类型实现
// BaseTask 只提供通用的状态管理和等待机制

// Wait 等待任务完成并返回结果
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

// Status 获取任务状态
func (b *BaseTask[Result]) Status() OperationStatus {
	return OperationStatus(b.status.Load())
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
