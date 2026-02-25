// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// PerCoreStepExecutor 测试
// ==========================================

func TestPerCoreStepExecutor_New(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	require.NotNil(t, executor)
	assert.NotNil(t, executor.closeCh)
	defer executor.Close()
}

func TestPerCoreStepExecutor_NewWithConfig(t *testing.T) {
	config := &StepExecutorConfig{
		PausedOpTTL:    30 * time.Minute,
		MaxPausedOps:   100,
		EnableMetrics:  true,
	}
	executor := NewPerCoreStepExecutor(config, nil)
	require.NotNil(t, executor)
	assert.Equal(t, 30*time.Minute, executor.config.PausedOpTTL)
	assert.Equal(t, 100, executor.config.MaxPausedOps)
	defer executor.Close()
}

func TestPerCoreStepExecutor_ExecuteSteps_Empty(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()
	ctx := context.Background()

	stepCtx := &service.StepContext{}
	err := executor.ExecuteSteps(ctx, nil, stepCtx)
	assert.NoError(t, err)

	err = executor.ExecuteSteps(ctx, []service.Step{}, stepCtx)
	assert.NoError(t, err)
}

func TestPerCoreStepExecutor_ExecuteSteps_Success(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()
	ctx := context.Background()

	var executed bool
	steps := []service.Step{
		{
			ID:   "step-1",
			Name: "test step",
			Handler: func(ctx context.Context) error {
				executed = true
				return nil
			},
		},
	}

	stepCtx := &service.StepContext{
		OperationID: "op-1",
	}
	err := executor.ExecuteSteps(ctx, steps, stepCtx)
	assert.NoError(t, err)
	assert.True(t, executed)
	assert.NotNil(t, stepCtx.State)
}

func TestPerCoreStepExecutor_ExecuteSteps_WithRollback(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()
	ctx := context.Background()

	var rolledBack bool
	steps := []service.Step{
		{
			ID: "step-1",
			Handler: func(ctx context.Context) error {
				return assert.AnError // 返回错误触发回滚
			},
			Rollback: func(ctx context.Context) error {
				rolledBack = true
				return nil
			},
		},
	}

	stepCtx := &service.StepContext{OperationID: "op-1"}
	err := executor.ExecuteSteps(ctx, steps, stepCtx)
	assert.Error(t, err)
	assert.True(t, rolledBack)
}

func TestPerCoreStepExecutor_ExecuteSteps_NilHandler(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()
	ctx := context.Background()

	steps := []service.Step{
		{
			ID:   "step-1",
			Name: "nil handler step",
			Handler: nil, // nil handler
		},
	}

	stepCtx := &service.StepContext{OperationID: "op-1"}
	err := executor.ExecuteSteps(ctx, steps, stepCtx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid step")
}

func TestPerCoreStepExecutor_PauseStep(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()

	// 首次暂停
	err := executor.PauseStep("op-1")
	assert.NoError(t, err)

	// 再次暂停同一操作（幂等）
	err = executor.PauseStep("op-1")
	assert.NoError(t, err)
}

func TestPerCoreStepExecutor_PauseStep_MaxLimit(t *testing.T) {
	config := &StepExecutorConfig{
		MaxPausedOps: 2,
	}
	executor := NewPerCoreStepExecutor(config, nil)
	defer executor.Close()

	// 达到上限
	err := executor.PauseStep("op-1")
	assert.NoError(t, err)
	err = executor.PauseStep("op-2")
	assert.NoError(t, err)

	// 超过上限应该失败
	err = executor.PauseStep("op-3")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max paused operations limit")
}

func TestPerCoreStepExecutor_ResumeStep(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()

	// 先暂停
	err := executor.PauseStep("op-1")
	assert.NoError(t, err)

	// 恢复
	err = executor.ResumeStep("op-1")
	assert.NoError(t, err)

	// 再次恢复（幂等）
	err = executor.ResumeStep("op-1")
	assert.NoError(t, err)
}

func TestPerCoreStepExecutor_ResumeStep_NotFound(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()

	err := executor.ResumeStep("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPerCoreStepExecutor_GetStepContext(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()
	ctx := context.Background()

	// 先暂停操作，这会创建一个暂停操作
	err := executor.PauseStep("op-1")
	assert.NoError(t, err)

	// 立即恢复
	err = executor.ResumeStep("op-1")
	assert.NoError(t, err)

	// 创建 stepCtx
	stepCtx := &service.StepContext{
		OperationID: "op-1",
		State:       map[string]any{"key": "value"},
	}

	// 通过 ExecuteSteps 关联 stepCtx
	steps := []service.Step{{
		ID: "step-1",
		Handler: func(ctx context.Context) error {
			return nil
		},
	}}
	err = executor.ExecuteSteps(ctx, steps, stepCtx)
	assert.NoError(t, err)

	// 获取 stepCtx
	retrievedCtx, err := executor.GetStepContext("op-1")
	assert.NoError(t, err)
	assert.NotNil(t, retrievedCtx)
	assert.Equal(t, "op-1", retrievedCtx.OperationID)
}

func TestPerCoreStepExecutor_GetStepContext_NotFound(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)
	defer executor.Close()

	_, err := executor.GetStepContext("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPerCoreStepExecutor_Close(t *testing.T) {
	executor := NewPerCoreStepExecutor(nil, nil)

	err := executor.Close()
	assert.NoError(t, err)

	// 关闭后再次关闭应该也成功
	err = executor.Close()
	assert.NoError(t, err)
}

// ==========================================
// FileCheckpointHandler 测试
// ==========================================

func TestNewFileCheckpointHandler(t *testing.T) {
	handler := NewFileCheckpointHandler(nil)
	require.NotNil(t, handler)
}

func TestNewFileCheckpointHandler_WithConfig(t *testing.T) {
	config := &CheckpointHandlerConfig{
		CheckpointDir:  "/tmp/test",
		MaxCheckpoints: 5,
		EnableMetrics:   true,
	}
	handler := NewFileCheckpointHandler(config)
	require.NotNil(t, handler)
	assert.Equal(t, "/tmp/test", handler.config.CheckpointDir)
	assert.Equal(t, 5, handler.config.MaxCheckpoints)
}

func TestFileCheckpointHandler_ExecuteToCheckpoint(t *testing.T) {
	handler := NewFileCheckpointHandler(nil)
	ctx := context.Background()

	stepCtx := &service.StepContext{
		OperationID: "op-1",
		State:       map[string]any{"key": "value"},
	}
	checkpoint := &service.Checkpoint{}

	err := handler.ExecuteToCheckpoint(ctx, stepCtx, checkpoint)
	assert.NoError(t, err)
	assert.NotZero(t, checkpoint.Timestamp)
}

func TestFileCheckpointHandler_ExecuteFromCheckpoint_Nil(t *testing.T) {
	handler := NewFileCheckpointHandler(nil)
	ctx := context.Background()

	stepCtx := &service.StepContext{}
	err := handler.ExecuteFromCheckpoint(ctx, stepCtx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFileCheckpointHandler_CreateCheckpoint(t *testing.T) {
	handler := NewFileCheckpointHandler(nil)
	ctx := context.Background()

	stepCtx := &service.StepContext{OperationID: "op-1"}
	checkpoint, err := handler.CreateCheckpoint(ctx, stepCtx)
	assert.NoError(t, err)
	assert.NotNil(t, checkpoint)
	assert.NotZero(t, checkpoint.Timestamp)
}

func TestFileCheckpointHandler_LoadCheckpoint(t *testing.T) {
	handler := NewFileCheckpointHandler(nil)
	ctx := context.Background()

	_, err := handler.LoadCheckpoint(ctx, "non-existent")
	assert.Error(t, err)
}

func TestFileCheckpointHandler_ExecuteFromCheckpoint(t *testing.T) {
	handler := NewFileCheckpointHandler(nil)
	ctx := context.Background()

	stepCtx := &service.StepContext{}
	checkpoint := &service.Checkpoint{Data: []byte("test")}

	err := handler.ExecuteFromCheckpoint(ctx, stepCtx, checkpoint)
	assert.NoError(t, err)
}

func TestFileCheckpointHandler_Serialize(t *testing.T) {
	handler := NewFileCheckpointHandler(nil)

	data, err := handler.serialize(&service.StepContext{})
	assert.NoError(t, err)
	// 空实现返回 nil
	assert.Nil(t, data)
}

func TestFileCheckpointHandler_Deserialize(t *testing.T) {
	handler := NewFileCheckpointHandler(nil)

	err := handler.deserialize([]byte("test"), &service.StepContext{})
	assert.NoError(t, err)
	// 空实现不做任何事
}

// ==========================================
// MemoryCheckpointHandler 测试
// ==========================================

func TestNewMemoryCheckpointHandler(t *testing.T) {
	handler := NewMemoryCheckpointHandler(nil)
	require.NotNil(t, handler)
}

func TestMemoryCheckpointHandler_ExecuteToCheckpoint(t *testing.T) {
	handler := NewMemoryCheckpointHandler(nil)
	ctx := context.Background()

	checkpoint := &service.Checkpoint{ID: "cp-1"}
	err := handler.ExecuteToCheckpoint(ctx, nil, checkpoint)
	assert.NoError(t, err)
	assert.NotZero(t, checkpoint.Timestamp)
}

func TestMemoryCheckpointHandler_ExecuteFromCheckpoint_Nil(t *testing.T) {
	handler := NewMemoryCheckpointHandler(nil)
	ctx := context.Background()

	stepCtx := &service.StepContext{}
	err := handler.ExecuteFromCheckpoint(ctx, stepCtx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint")
}

func TestMemoryCheckpointHandler_CreateCheckpoint(t *testing.T) {
	handler := NewMemoryCheckpointHandler(nil)
	ctx := context.Background()

	checkpoint, err := handler.CreateCheckpoint(ctx, nil)
	assert.NoError(t, err)
	assert.NotNil(t, checkpoint)
	assert.NotZero(t, checkpoint.Timestamp)
}

func TestMemoryCheckpointHandler_CreateCheckpoint_Multiple(t *testing.T) {
	handler := NewMemoryCheckpointHandler(nil)
	ctx := context.Background()

	cp1, err := handler.CreateCheckpoint(ctx, nil)
	assert.NoError(t, err)

	cp2, err := handler.CreateCheckpoint(ctx, nil)
	assert.NoError(t, err)

	// ID 应该不同
	assert.NotEqual(t, cp1.ID, cp2.ID)
}

func TestMemoryCheckpointHandler_LoadCheckpoint(t *testing.T) {
	handler := NewMemoryCheckpointHandler(nil)
	ctx := context.Background()

	// 先创建
	created, err := handler.CreateCheckpoint(ctx, nil)
	assert.NoError(t, err)

	// 再加载
	loaded, err := handler.LoadCheckpoint(ctx, created.ID)
	assert.NoError(t, err)
	assert.Equal(t, created.ID, loaded.ID)
}

func TestMemoryCheckpointHandler_LoadCheckpoint_NotFound(t *testing.T) {
	handler := NewMemoryCheckpointHandler(nil)
	ctx := context.Background()

	_, err := handler.LoadCheckpoint(ctx, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMemoryCheckpointHandler_ExecuteFromCheckpoint_WithStepID(t *testing.T) {
	handler := NewMemoryCheckpointHandler(nil)
	ctx := context.Background()

	stepCtx := &service.StepContext{}
	checkpoint := &service.Checkpoint{
		Step: service.Step{ID: "5"},
	}

	err := handler.ExecuteFromCheckpoint(ctx, stepCtx, checkpoint)
	assert.NoError(t, err)
	assert.Equal(t, 5, stepCtx.CurrentStep)
}

// ==========================================
// PerCoreMigrationHandler 测试
// ==========================================

func TestNewPerCoreMigrationHandler(t *testing.T) {
	executor := NewPerCoreExecutor(nil)
	require.NotNil(t, executor)
	handler := NewPerCoreMigrationHandler(executor)
	require.NotNil(t, handler)
	executor.Close()
}

func TestNewPerCoreMigrationHandler_NilExecutor(t *testing.T) {
	handler := NewPerCoreMigrationHandler(nil)
	require.NotNil(t, handler)
}

func TestPerCoreMigrationHandler_PrepareMigrate(t *testing.T) {
	handler := NewPerCoreMigrationHandler(nil)
	ctx := context.Background()

	req := &service.MigrationRequest{
		MigrationID:  "mig-1",
		SourceNodeID: "node-1",
		TargetNodeID: "node-2",
		ShardID:      1,
	}

	err := handler.PrepareMigrate(ctx, req)
	assert.NoError(t, err)

	// 验证状态已存储
	status, err := handler.GetMigrationStatus("mig-1")
	assert.NoError(t, err)
	assert.Equal(t, "mig-1", status.MigrationID)
	assert.Equal(t, service.MigrationPhasePrepare, status.Phase)
}

func TestPerCoreMigrationHandler_CommitMigrate(t *testing.T) {
	handler := NewPerCoreMigrationHandler(nil)
	ctx := context.Background()

	req := &service.MigrationRequest{
		MigrationID: "mig-2",
		ShardID:     2,
	}

	err := handler.CommitMigrate(ctx, req)
	assert.NoError(t, err)

	status, err := handler.GetMigrationStatus("mig-2")
	assert.NoError(t, err)
	assert.Equal(t, service.MigrationPhaseCommit, status.Phase)
	assert.Equal(t, float32(100), status.Progress)
}

func TestPerCoreMigrationHandler_RollbackMigrate(t *testing.T) {
	handler := NewPerCoreMigrationHandler(nil)
	ctx := context.Background()

	req := &service.MigrationRequest{
		MigrationID: "mig-3",
		ShardID:     3,
	}

	err := handler.RollbackMigrate(ctx, req)
	assert.NoError(t, err)

	status, err := handler.GetMigrationStatus("mig-3")
	assert.NoError(t, err)
	assert.Equal(t, service.MigrationPhaseFailed, status.Phase)
	assert.NotNil(t, status.Error)
}

func TestPerCoreMigrationHandler_GetMigrationStatus_NotFound(t *testing.T) {
	handler := NewPerCoreMigrationHandler(nil)

	_, err := handler.GetMigrationStatus("non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ==========================================
// 接口实现验证测试
// ==========================================

func TestPerCoreStepExecutor_ImplementsStepExecutor(t *testing.T) {
	var _ service.StepExecutor = (*PerCoreStepExecutor)(nil)
}

func TestFileCheckpointHandler_ImplementsCheckpointHandler(t *testing.T) {
	var _ service.CheckpointHandler = (*FileCheckpointHandler)(nil)
}

func TestMemoryCheckpointHandler_ImplementsCheckpointHandler(t *testing.T) {
	var _ service.CheckpointHandler = (*MemoryCheckpointHandler)(nil)
}

func TestPerCoreMigrationHandler_ImplementsMigrationHandler(t *testing.T) {
	var _ service.MigrationHandler = (*PerCoreMigrationHandler)(nil)
}
