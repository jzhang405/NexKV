// Package service 定义领域服务
package service

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// Pipeline 流水线上下文
// ==========================================

// Pipeline 流水线上下文
// 提供任务提交入口，聚合执行器
type Pipeline struct {
	ctx      context.Context
	cancel   context.CancelFunc
	executor TaskExecutor
}

// NewPipeline 创建新的 Pipeline
func NewPipeline(ctx context.Context, executor TaskExecutor) *Pipeline {
	ctx, cancel := context.WithCancel(ctx)
	return &Pipeline{
		ctx:      ctx,
		cancel:   cancel,
		executor: executor,
	}
}

// Submit 提交任务到执行器
// 实现 model.PipelineContext 接口
func (p *Pipeline) Submit(task model.TaskRunner) error {
	return p.executor.Submit(p.ctx, task.SourceID(), task.Priority(), func(ctx context.Context) {
		task.Run(ctx, p)
	})
}

// Executor 获取执行器
// 实现 model.PipelineContext 接口
func (p *Pipeline) Executor() model.TaskExecutorRef {
	return p.executor
}

// Context 获取上下文
func (p *Pipeline) Context() context.Context {
	return p.ctx
}

// Cancel 取消 Pipeline
func (p *Pipeline) Cancel() {
	if p.cancel != nil {
		p.cancel()
	}
}
