// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// Per-Core Step Executor 可暂停调度器实现
// ==========================================

// DefaultStepExecutorConfig 默认配置
var DefaultStepExecutorConfig = StepExecutorConfig{
	PausedOpTTL:    1 * time.Hour,
	MaxPausedOps:   10000,
	EnableMetrics: true,
}

// StepExecutorConfig 步骤执行器配置
type StepExecutorConfig struct {
	// PausedOpTTL 暂停操作的超时时间
	PausedOpTTL time.Duration

	// MaxPausedOps 最大暂停操作数
	MaxPausedOps int

	// EnableMetrics 是否启用指标
	EnableMetrics bool
}

// PerCoreStepExecutor Per-Core 可暂停步骤执行器
type PerCoreStepExecutor struct {
	config      StepExecutorConfig
	executor    *PerCoreExecutor
	pausedOps   sync.Map // map[string]*pausedOperation
	pausedCount atomic.Int64
	wg          sync.WaitGroup
	closeCh    chan struct{}
	closed      atomic.Bool
}

// pausedOperation 暂停的操作
type pausedOperation struct {
	opID        string
	stepCtx     *service.StepContext
	pauseCh     chan struct{}
	resumeCh    chan struct{}
	resultCh    chan error
	pausedAt    time.Time
	pauseOnce   sync.Once
	resumeOnce  sync.Once
	isPaused    atomic.Bool
}

// NewPerCoreStepExecutor 创建 Per-Core 步骤执行器
func NewPerCoreStepExecutor(config *StepExecutorConfig, executor *PerCoreExecutor) *PerCoreStepExecutor {
	if config == nil {
		config = &DefaultStepExecutorConfig
	}

	e := &PerCoreStepExecutor{
		config:   *config,
		executor: executor,
		closeCh:  make(chan struct{}),
	}

	// 启动 TTL 清理协程
	e.wg.Add(1)
	go e.cleanupLoop()

	return e
}

// ExecuteSteps 执行多个步骤
func (e *PerCoreStepExecutor) ExecuteSteps(ctx context.Context, steps []service.Step, stepCtx *service.StepContext) error {
	if len(steps) == 0 {
		return nil
	}

	stepCtx.Steps = steps
	stepCtx.CurrentStep = 0
	stepCtx.State = make(map[string]any)
	stepCtx.CreatedAt = time.Now()
	stepCtx.UpdatedAt = time.Now()

	// P0: 保存 stepCtx 到 pausedOperation（用于 GetStepContext）
	opID := stepCtx.OperationID
	if pausedVal, ok := e.pausedOps.Load(opID); ok {
		pausedVal.(*pausedOperation).stepCtx = stepCtx
	}

	for i := range steps {
		stepCtx.CurrentStep = i
		step := &steps[i]

		// P0: 使用 isPaused 标志检查暂停状态，避免 channel 关闭后的问题
		if pausedVal, ok := e.pausedOps.Load(opID); ok {
			paused := pausedVal.(*pausedOperation)
			if paused.isPaused.Load() {
				// 等待恢复
				select {
				case <-paused.resumeCh:
					// 恢复执行
				case <-ctx.Done():
					return ctx.Err()
				case <-e.closeCh:
					return errors.ErrPerCoreExecutorClosed
				}
			}
		}

		// P2: nil 检查
		if step == nil || step.Handler == nil {
			return errors.Wrapf(errors.ErrInvalidParam, "invalid step at index %d", i)
		}

		// 执行步骤
		err := step.Handler(ctx)
		if err != nil {
			// 执行回滚
			if step.Rollback != nil {
				_ = step.Rollback(ctx)
			}
			return errors.Wrapf(err, "step %d failed", i)
		}

		stepCtx.UpdatedAt = time.Now()
	}

	return nil
}

// PauseStep 暂停步骤
func (e *PerCoreStepExecutor) PauseStep(opID string) error {
	// P1: MaxPausedOps 限制检查，防止 DoS
	if e.config.MaxPausedOps > 0 {
		count := e.pausedCount.Load()
		if count >= int64(e.config.MaxPausedOps) {
			return errors.Wrapf(errors.ErrStepMaxPausedReached, "max paused operations limit reached: %d", e.config.MaxPausedOps)
		}
	}

	// P1: 使用 LoadOrStore 保证原子性，避免 race condition
	newPaused := &pausedOperation{
		opID:     opID,
		pauseCh:  make(chan struct{}, 1),
		resumeCh: make(chan struct{}, 1),
		resultCh: make(chan error, 1),
		pausedAt: time.Now(),
	}

	actual, loaded := e.pausedOps.LoadOrStore(opID, newPaused)
	p := actual.(*pausedOperation)

	if !loaded {
		// 新创建时才增加计数
		e.pausedCount.Add(1)
	}

	// P0: 使用 sync.Once 确保 channel 只关闭一次
	p.pauseOnce.Do(func() {
		p.isPaused.Store(true)
		close(p.pauseCh)
	})

	return nil
}

// ResumeStep 恢复步骤
func (e *PerCoreStepExecutor) ResumeStep(opID string) error {
	pausedOp, ok := e.pausedOps.Load(opID)
	if !ok {
		return errors.Wrapf(errors.ErrStepNotFound, "operation %s not found", opID)
	}

	p := pausedOp.(*pausedOperation)

	// P0: 使用 sync.Once 确保 channel 只关闭一次
	p.resumeOnce.Do(func() {
		p.isPaused.Store(false)
		close(p.resumeCh)
	})

	return nil
}

// GetStepContext 获取步骤上下文
func (e *PerCoreStepExecutor) GetStepContext(opID string) (*service.StepContext, error) {
	pausedOp, ok := e.pausedOps.Load(opID)
	if !ok {
		return nil, errors.Wrapf(errors.ErrStepNotFound, "operation %s not found", opID)
	}

	return pausedOp.(*pausedOperation).stepCtx, nil
}

// cleanupLoop 清理过期暂停操作
func (e *PerCoreStepExecutor) cleanupLoop() {
	defer e.wg.Done()

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.cleanup()
		case <-e.closeCh:
			return
		}
	}
}

// cleanup 清理过期的暂停操作
func (e *PerCoreStepExecutor) cleanup() {
	ttl := e.config.PausedOpTTL
	now := time.Now()

	e.pausedOps.Range(func(key, value any) bool {
		op := value.(*pausedOperation)
		if now.Sub(op.pausedAt) > ttl {
			// P1: 关闭 channel 避免资源泄漏
			op.pauseOnce.Do(func() { close(op.pauseCh) })
			op.resumeOnce.Do(func() { close(op.resumeCh) })
			e.pausedOps.Delete(key)
			e.pausedCount.Add(-1)
		}
		return true
	})
}

// Close 关闭
func (e *PerCoreStepExecutor) Close() error {
	// 使用 CAS 确保只关闭一次
	if e.closed.CompareAndSwap(false, true) {
		close(e.closeCh)
		e.wg.Wait()
	}
	return nil
}

// 确保接口实现
var _ service.StepExecutor = (*PerCoreStepExecutor)(nil)

// DefaultCheckpointHandlerConfig 默认检查点处理器配置
var DefaultCheckpointHandlerConfig = CheckpointHandlerConfig{
	CheckpointDir:    "./checkpoints",
	MaxCheckpoints:  10,
	EnableMetrics:   true,
}

// CheckpointHandlerConfig 检查点处理器配置
type CheckpointHandlerConfig struct {
	// CheckpointDir 检查点目录
	CheckpointDir string

	// MaxCheckpoints 最大检查点数量
	MaxCheckpoints int

	// EnableMetrics 是否启用指标
	EnableMetrics bool
}

// FileCheckpointHandler 基于文件的检查点处理器
type FileCheckpointHandler struct {
	config CheckpointHandlerConfig
}

// NewFileCheckpointHandler 创建文件检查点处理器
func NewFileCheckpointHandler(config *CheckpointHandlerConfig) *FileCheckpointHandler {
	if config == nil {
		config = &DefaultCheckpointHandlerConfig
	}

	return &FileCheckpointHandler{
		config: *config,
	}
}

// ExecuteToCheckpoint 执行到检查点
func (h *FileCheckpointHandler) ExecuteToCheckpoint(ctx context.Context, stepCtx *service.StepContext, checkpoint *service.Checkpoint) error {
	// 创建检查点数据
	checkpointData, err := h.serialize(stepCtx)
	if err != nil {
		return errors.Wrap(err, "serialize checkpoint failed")
	}

	checkpoint.Data = checkpointData
	checkpoint.Timestamp = time.Now()

	return nil
}

// ExecuteFromCheckpoint 从检查点恢复
func (h *FileCheckpointHandler) ExecuteFromCheckpoint(ctx context.Context, stepCtx *service.StepContext, checkpoint *service.Checkpoint) error {
	if checkpoint == nil || checkpoint.Data == nil {
		return errors.ErrCheckpointNotFound
	}

	return h.deserialize(checkpoint.Data, stepCtx)
}

// CreateCheckpoint 创建检查点
func (h *FileCheckpointHandler) CreateCheckpoint(ctx context.Context, stepCtx *service.StepContext) (*service.Checkpoint, error) {
	checkpoint := &service.Checkpoint{
		ID:        fmt.Sprintf("cp_%d", time.Now().UnixNano()),
		ShardID:   0,
		Step:      service.Step{},
		Timestamp: time.Now(),
	}

	err := h.ExecuteToCheckpoint(ctx, stepCtx, checkpoint)
	if err != nil {
		return nil, err
	}

	return checkpoint, nil
}

// LoadCheckpoint 加载检查点
func (h *FileCheckpointHandler) LoadCheckpoint(ctx context.Context, checkpointID string) (*service.Checkpoint, error) {
	// TODO: 从文件加载
	return nil, errors.ErrNotImplemented
}

// serialize 序列化步骤上下文
func (h *FileCheckpointHandler) serialize(stepCtx *service.StepContext) ([]byte, error) {
	// 简化实现：不做实际序列化
	return nil, nil
}

// deserialize 反序列化步骤上下文
func (h *FileCheckpointHandler) deserialize(data []byte, stepCtx *service.StepContext) error {
	// 简化实现：不做实际反序列化
	return nil
}

// 确保接口实现
var _ service.CheckpointHandler = (*FileCheckpointHandler)(nil)

// MemoryCheckpointHandler 内存检查点处理器
type MemoryCheckpointHandler struct {
	checkpoints sync.Map // map[string]*service.Checkpoint
	config     CheckpointHandlerConfig
	seq        int64
}

// NewMemoryCheckpointHandler 创建内存检查点处理器
func NewMemoryCheckpointHandler(config *CheckpointHandlerConfig) *MemoryCheckpointHandler {
	if config == nil {
		config = &DefaultCheckpointHandlerConfig
	}

	return &MemoryCheckpointHandler{
		config: *config,
	}
}

// ExecuteToCheckpoint 执行到检查点
func (h *MemoryCheckpointHandler) ExecuteToCheckpoint(ctx context.Context, stepCtx *service.StepContext, checkpoint *service.Checkpoint) error {
	checkpoint.Timestamp = time.Now()
	h.checkpoints.Store(checkpoint.ID, checkpoint)
	return nil
}

// ExecuteFromCheckpoint 从检查点恢复
func (h *MemoryCheckpointHandler) ExecuteFromCheckpoint(ctx context.Context, stepCtx *service.StepContext, checkpoint *service.Checkpoint) error {
	if checkpoint == nil {
		return errors.ErrCheckpointNotFound
	}

	// 从 Step.ID 解析步骤索引（格式：step_<数字>）
	// 注意：这是简化实现，生产环境应使用专门的 StepIndex 字段
	if checkpoint.Step.ID != "" {
		if idx, err := strconv.Atoi(checkpoint.Step.ID); err == nil {
			stepCtx.CurrentStep = idx
		}
	}
	return nil
}

// CreateCheckpoint 创建检查点
func (h *MemoryCheckpointHandler) CreateCheckpoint(ctx context.Context, stepCtx *service.StepContext) (*service.Checkpoint, error) {
	checkpoint := &service.Checkpoint{
		ID:        fmt.Sprintf("cp_%d", atomic.AddInt64(&h.seq, 1)),
		ShardID:   0,
		Step:      service.Step{},
		Timestamp: time.Now(),
	}

	h.ExecuteToCheckpoint(ctx, stepCtx, checkpoint)
	return checkpoint, nil
}

// LoadCheckpoint 加载检查点
func (h *MemoryCheckpointHandler) LoadCheckpoint(ctx context.Context, checkpointID string) (*service.Checkpoint, error) {
	val, ok := h.checkpoints.Load(checkpointID)
	if !ok {
		return nil, errors.Wrapf(errors.ErrCheckpointNotFound, "checkpoint %s not found", checkpointID)
	}

	return val.(*service.Checkpoint), nil
}

// 确保接口实现
var _ service.CheckpointHandler = (*MemoryCheckpointHandler)(nil)

// PerCoreMigrationHandler Per-Core 迁移处理器
type PerCoreMigrationHandler struct {
	executor   *PerCoreExecutor
	migrations sync.Map // map[string]*service.MigrationStatus
}

// NewPerCoreMigrationHandler 创建 Per-Core 迁移处理器
func NewPerCoreMigrationHandler(executor *PerCoreExecutor) *PerCoreMigrationHandler {
	return &PerCoreMigrationHandler{
		executor: executor,
	}
}

// PrepareMigrate 准备迁移
func (h *PerCoreMigrationHandler) PrepareMigrate(ctx context.Context, req *service.MigrationRequest) error {
	status := &service.MigrationStatus{
		MigrationID: req.MigrationID,
		Phase:       service.MigrationPhasePrepare,
		Progress:    0,
		LastUpdated: time.Now(),
	}
	h.migrations.Store(req.MigrationID, status)
	return nil
}

// CommitMigrate 提交迁移
func (h *PerCoreMigrationHandler) CommitMigrate(ctx context.Context, req *service.MigrationRequest) error {
	status := &service.MigrationStatus{
		MigrationID: req.MigrationID,
		Phase:       service.MigrationPhaseCommit,
		Progress:    100,
		LastUpdated: time.Now(),
	}
	h.migrations.Store(req.MigrationID, status)
	return nil
}

// RollbackMigrate 回滚迁移
func (h *PerCoreMigrationHandler) RollbackMigrate(ctx context.Context, req *service.MigrationRequest) error {
	status := &service.MigrationStatus{
		MigrationID: req.MigrationID,
		Phase:       service.MigrationPhaseFailed,
		Progress:    0,
		Error:       errors.Wrap(errors.ErrMigrationNotFound, "migration rolled back"),
		LastUpdated: time.Now(),
	}
	h.migrations.Store(req.MigrationID, status)
	return nil
}

// GetMigrationStatus 获取迁移状态
func (h *PerCoreMigrationHandler) GetMigrationStatus(migrationID string) (*service.MigrationStatus, error) {
	val, ok := h.migrations.Load(migrationID)
	if !ok {
		return nil, errors.Wrapf(errors.ErrMigrationNotFound, "migration %s not found", migrationID)
	}
	return val.(*service.MigrationStatus), nil
}

// 确保接口实现
var _ service.MigrationHandler = (*PerCoreMigrationHandler)(nil)
