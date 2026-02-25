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
)

// ==========================================
// 可暂停调度器实现（PR-087c）
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
	config   StepExecutorConfig
	executor *PerCoreExecutor
	pausedOps sync.Map // map[string]*pausedOp
	mu        sync.RWMutex
	wg        sync.WaitGroup
	closeCh   chan struct{}
}

// pausedOperation 暂停的操作
type pausedOperation struct {
	opID       string
	stepCtx    *service.StepContext
	pauseCh    chan struct{}
	resumeCh   chan struct{}
	resultCh   chan error
	pausedAt   time.Time
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

	for i := range steps {
		stepCtx.CurrentStep = i
		step := &steps[i]

		// 检查是否已暂停
		opID := stepCtx.OperationID
		if pausedVal, ok := e.pausedOps.Load(opID); ok {
			paused := pausedVal.(*pausedOperation)
			select {
			case <-paused.pauseCh:
				// 等待恢复
				select {
				case <-paused.resumeCh:
					// 恢复执行
				case <-ctx.Done():
					return ctx.Err()
				case <-e.closeCh:
					return fmt.Errorf("executor closed")
				}
			}
		}

		// 执行步骤
		err := step.Handler(ctx)
		if err != nil {
			// 执行回滚
			if step.Rollback != nil {
				_ = step.Rollback(ctx)
			}
			return fmt.Errorf("step %d failed: %w", i, err)
		}

		stepCtx.UpdatedAt = time.Now()
	}

	return nil
}

// PauseStep 暂停步骤
func (e *PerCoreStepExecutor) PauseStep(opID string) error {
	// 查找暂停的操作
	pausedVal, ok := e.pausedOps.Load(opID)
	if !ok {
		// 创建新的暂停操作
		newPaused := &pausedOperation{
			opID:     opID,
			pauseCh:  make(chan struct{}),
			resumeCh: make(chan struct{}),
			resultCh: make(chan error, 1),
			pausedAt: time.Now(),
		}
		e.pausedOps.Store(opID, newPaused)
		pausedVal = newPaused
	}

	p := pausedVal.(*pausedOperation)
	close(p.pauseCh) // 发送暂停信号

	return nil
}

// ResumeStep 恢复步骤
func (e *PerCoreStepExecutor) ResumeStep(opID string) error {
	pausedOp, ok := e.pausedOps.Load(opID)
	if !ok {
		return fmt.Errorf("operation %s not found", opID)
	}

	p := pausedOp.(*pausedOperation)
	close(p.resumeCh) // 发送恢复信号

	return nil
}

// GetStepContext 获取步骤上下文
func (e *PerCoreStepExecutor) GetStepContext(opID string) (*service.StepContext, error) {
	pausedOp, ok := e.pausedOps.Load(opID)
	if !ok {
		return nil, fmt.Errorf("operation %s not found", opID)
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
			e.pausedOps.Delete(key)
		}
		return true
	})
}

// Close 关闭
func (e *PerCoreStepExecutor) Close() error {
	close(e.closeCh)
	e.wg.Wait()
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
	mu     sync.RWMutex
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
		return fmt.Errorf("serialize checkpoint failed: %w", err)
	}

	checkpoint.Data = checkpointData
	checkpoint.Timestamp = time.Now()

	return nil
}

// ExecuteFromCheckpoint 从检查点恢复
func (h *FileCheckpointHandler) ExecuteFromCheckpoint(ctx context.Context, stepCtx *service.StepContext, checkpoint *service.Checkpoint) error {
	if checkpoint == nil || checkpoint.Data == nil {
		return fmt.Errorf("invalid checkpoint")
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
	return nil, fmt.Errorf("not implemented")
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
	mu         sync.RWMutex
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
		return fmt.Errorf("checkpoint is nil")
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
		return nil, fmt.Errorf("checkpoint %s not found", checkpointID)
	}

	return val.(*service.Checkpoint), nil
}

var checkpointSeq int64

// 确保接口实现
var _ service.CheckpointHandler = (*MemoryCheckpointHandler)(nil)

// PerCoreMigrationHandler Per-Core 迁移处理器
type PerCoreMigrationHandler struct {
	executor   *PerCoreExecutor
	migrations sync.Map // map[string]*service.MigrationStatus
	mu         sync.RWMutex
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
		Error:       fmt.Errorf("migration rolled back"),
		LastUpdated: time.Now(),
	}
	h.migrations.Store(req.MigrationID, status)
	return nil
}

// GetMigrationStatus 获取迁移状态
func (h *PerCoreMigrationHandler) GetMigrationStatus(migrationID string) (*service.MigrationStatus, error) {
	val, ok := h.migrations.Load(migrationID)
	if !ok {
		return nil, fmt.Errorf("migration %s not found", migrationID)
	}
	return val.(*service.MigrationStatus), nil
}

// 确保接口实现
var _ service.MigrationHandler = (*PerCoreMigrationHandler)(nil)
