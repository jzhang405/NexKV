// Package service 定义领域服务接口
package service

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TaskPriority 任务优先级（业务化命名）
// Deprecated: 使用 model.TaskPriority 代替
type TaskPriority = model.TaskPriority

// GoroutinePriority 是 TaskPriority 的别名，保持向后兼容
// Deprecated: 使用 TaskPriority 代替
type GoroutinePriority = model.GoroutinePriority

// TaskPoolStats 任务池统计信息（业务化命名）
// Deprecated: 使用 model.TaskPoolStats 代替
type TaskPoolStats = model.TaskPoolStats

// GoroutinePoolStats 是 TaskPoolStats 的别名，保持向后兼容
// Deprecated: 使用 TaskPoolStats 代替
type GoroutinePoolStats = model.GoroutinePoolStats

// TaskHealthStatus 健康状态（业务化命名）
// Deprecated: 使用 model.TaskHealthStatus 代替
type TaskHealthStatus = model.TaskHealthStatus

// GoroutineHealthStatus 是 TaskHealthStatus 的别名，保持向后兼容
// Deprecated: 使用 TaskHealthStatus 代替
type GoroutineHealthStatus = model.GoroutineHealthStatus

// 常量别名（向后兼容）
const (
	TaskPriorityCritical      = model.TaskPriorityCritical
	TaskPriorityHigh          = model.TaskPriorityHigh
	TaskPriorityNormal        = model.TaskPriorityNormal
	TaskPriorityLow           = model.TaskPriorityLow
	GoroutinePriorityCritical = model.GoroutinePriorityCritical
	GoroutinePriorityHigh     = model.GoroutinePriorityHigh
	GoroutinePriorityNormal   = model.GoroutinePriorityNormal
	GoroutinePriorityLow      = model.GoroutinePriorityLow
)

// ==========================================
// 任务执行器接口（拆分后的小接口）
// ==========================================

// TaskExecutor 基础任务执行器接口（最小接口）
// 适用于简单场景，只需要 Submit 方法
type TaskExecutor interface {
	Submit(ctx context.Context, task func(context.Context)) error
}

// TaskExecutorWithArg 带参数的任务执行器
type TaskExecutorWithArg interface {
	SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error
}

// TaskExecutorWithResult 带返回值的任务执行器
type TaskExecutorWithResult interface {
	SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) GoroutineResult[any]
}

// TaskScheduler 任务调度器（延迟任务）
type TaskScheduler interface {
	SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error
}

// TaskPriorityExecutor 优先级任务执行器
type TaskPriorityExecutor interface {
	SubmitWithPriority(ctx context.Context, priority TaskPriority, task func(context.Context)) error
}

// ==========================================
// GoroutineProvider 协程池提供者接口（完整接口）
// ==========================================

// GoroutineProvider 协程池提供者接口
// 注意：由于 Go 接口限制，接口方法不能有类型参数
// 泛型方法通过独立辅助函数提供（由基础设施层实现）
type GoroutineProvider interface {
	// ======================================
	// 基础方法（简单场景，快速上手）
	// ======================================

	// Submit 简单任务：无参数，无返回值
	Submit(ctx context.Context, task func(context.Context)) error

	// SubmitWithArg 带参数任务：避免闭包陷阱（使用 any 类型）
	SubmitWithArg(ctx context.Context, task func(context.Context, any), arg any) error

	// SubmitWithResult 带返回值任务（使用 any 类型）
	SubmitWithResult(ctx context.Context, task func(context.Context) (any, error)) GoroutineResult[any]

	// SubmitWithArgAndResult 带参数和返回值任务（使用 any 类型）
	SubmitWithArgAndResult(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
	) GoroutineResult[any]

	// SubmitWithPriority 优先级任务
	SubmitWithPriority(ctx context.Context, priority GoroutinePriority, task func(context.Context)) error

	// SubmitDelayed 延迟任务
	SubmitDelayed(ctx context.Context, delay time.Duration, task func(context.Context)) error

	// ======================================
	// 高级方法（选项模式）
	// ======================================

	// SubmitAdvanced 高级任务提交（使用 any 类型）
	SubmitAdvanced(
		ctx context.Context,
		task func(context.Context, any) (any, error),
		arg any,
		opts ...GoroutineSubmitOption,
	) GoroutineResult[any]

	// ======================================
	// 批量方法（语义清晰，单独列出）
	// ======================================

	// SubmitBatch 批量提交：快速执行多个任务（无参数，无返回值）
	SubmitBatch(ctx context.Context, tasks []func(context.Context)) error

	// SubmitBatchWithArg 批量提交：带参数（使用 any 类型）
	SubmitBatchWithArg(ctx context.Context, tasks []func(context.Context, any), args []any) error

	// SubmitBatchAllErrors 批量提交：收集所有错误（无参数）
	SubmitBatchAllErrors(ctx context.Context, tasks []func(context.Context)) []error

	// SubmitBatchWithResult 批量提交：带返回值（使用 any 类型）
	SubmitBatchWithResult(ctx context.Context, tasks []func(context.Context) (any, error)) []GoroutineResult[any]

	// ======================================
	// 管理方法
	// ======================================

	Stats() GoroutinePoolStats
	Health() GoroutineHealthStatus
	SetCapacity(capacity int) error
	Close() error
	CloseWithTimeout(timeout time.Duration) error
}

// ==========================================
// 选项模式定义（用于 SubmitAdvanced）
// ==========================================

// GoroutineSubmitOptions 提交选项配置
type GoroutineSubmitOptions struct {
	Priority GoroutinePriority
	Delay    time.Duration
}

// GoroutineSubmitOption 提交选项
type GoroutineSubmitOption func(*GoroutineSubmitOptions)

// WithGoroutinePriority 设置优先级
func WithGoroutinePriority(priority GoroutinePriority) GoroutineSubmitOption {
	return func(opts *GoroutineSubmitOptions) {
		opts.Priority = priority
	}
}

// WithGoroutineDelay 设置延迟
func WithGoroutineDelay(delay time.Duration) GoroutineSubmitOption {
	return func(opts *GoroutineSubmitOptions) {
		opts.Delay = delay
	}
}

// ==========================================
// Result 类型
// ==========================================

// GoroutineResult 异步任务结果接口
type GoroutineResult[T any] interface {
	// Get 获取结果（阻塞等待）
	Get(ctx context.Context) (T, error)
	// IsDone 检查是否已完成
	IsDone() bool
}

// ==========================================
// PR-087: 统一执行器架构 - 新增接口
// ==========================================

// Step 步骤定义
type Step struct {
	ID         string
	Name       string
	Handler    func(ctx context.Context) error
	Rollback   func(ctx context.Context) error
	IsPausable bool
	Timeout    time.Duration
}

// StepContext 步骤执行上下文
type StepContext struct {
	OperationID string
	CurrentStep int
	Steps       []Step
	State       map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// StepExecutor 步骤执行器（可暂停调度器）
type StepExecutor interface {
	// ExecuteSteps 执行多个步骤
	ExecuteSteps(ctx context.Context, steps []Step, stepCtx *StepContext) error

	// PauseStep 暂停步骤
	PauseStep(opID string) error

	// ResumeStep 恢复步骤
	ResumeStep(opID string) error

	// GetStepContext 获取步骤上下文
	GetStepContext(opID string) (*StepContext, error)
}

// StepHandler 步骤处理器
type StepHandler interface {
	// Execute 执行单个步骤
	Execute(ctx context.Context, step *Step, stepCtx *StepContext) error

	// Rollback 回滚单个步骤
	Rollback(ctx context.Context, step *Step, stepCtx *StepContext) error

	// IsPausable 检查步骤是否可暂停
	IsPausable(step *Step) bool
}

// Checkpoint 检查点
type Checkpoint struct {
	ID        string
	LSN       uint64
	ShardID   int
	Step      Step
	Data      []byte
	Timestamp time.Time
	Term      uint64
}

// CheckpointHandler 检查点处理器
type CheckpointHandler interface {
	// ExecuteToCheckpoint 执行到检查点
	ExecuteToCheckpoint(ctx context.Context, stepCtx *StepContext, checkpoint *Checkpoint) error

	// ExecuteFromCheckpoint 从检查点恢复
	ExecuteFromCheckpoint(ctx context.Context, stepCtx *StepContext, checkpoint *Checkpoint) error

	// CreateCheckpoint 创建检查点
	CreateCheckpoint(ctx context.Context, stepCtx *StepContext) (*Checkpoint, error)

	// LoadCheckpoint 加载检查点
	LoadCheckpoint(ctx context.Context, checkpointID string) (*Checkpoint, error)
}

// MigrationRequest 迁移请求
type MigrationRequest struct {
	MigrationID  string
	SourceNodeID string
	TargetNodeID string
	ShardID      int
	CheckpointLSN uint64
	Term         uint64
}

// MigrationStatus 迁移状态
type MigrationStatus struct {
	MigrationID string
	Phase       MigrationPhase
	Progress    float32
	Error       error
	LastUpdated time.Time
}

// MigrationPhase 迁移阶段
type MigrationPhase int

const (
	MigrationPhaseIdle MigrationPhase = iota
	MigrationPhasePrepare
	MigrationPhaseExport
	MigrationPhaseTransfer
	MigrationPhaseCommit
	MigrationPhaseCleanup
	MigrationPhaseDone
	MigrationPhaseFailed
)

// MigrationHandler 迁移处理器
type MigrationHandler interface {
	// PrepareMigrate 准备迁移
	PrepareMigrate(ctx context.Context, req *MigrationRequest) error

	// CommitMigrate 提交迁移
	CommitMigrate(ctx context.Context, req *MigrationRequest) error

	// RollbackMigrate 回滚迁移
	RollbackMigrate(ctx context.Context, req *MigrationRequest) error

	// GetMigrationStatus 获取迁移状态
	GetMigrationStatus(migrationID string) (*MigrationStatus, error)
}
