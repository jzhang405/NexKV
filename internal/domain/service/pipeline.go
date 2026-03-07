// Package service 定义领域服务
package service

import (
	"context"
	"github.com/jzhang405/NexKV/pkg/errors"
	"sync"
	"time"

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

	// 优雅关闭支持
	wg      sync.WaitGroup
	closed  bool
	closeMu sync.RWMutex
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
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()

	if p.closed {
		return ErrPipelineClosed
	}

	p.wg.Add(1)
	return p.executor.Submit(p.ctx, task.SourceID(), task.Priority(), func(ctx context.Context) {
		defer p.wg.Done()
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
// 立即取消，不等待正在执行的任务
func (p *Pipeline) Cancel() {
	if p.cancel != nil {
		p.cancel()
	}
}

// GracefulShutdown 优雅关闭 Pipeline
// 1. 停止接收新任务
// 2. 等待正在执行的任务完成
// 3. 超时后强制取消
func (p *Pipeline) GracefulShutdown(timeout time.Duration) error {
	// 1. 标记为已关闭，不再接收新任务
	p.closeMu.Lock()
	p.closed = true
	p.closeMu.Unlock()

	// 2. 等待正在执行的任务完成或超时
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 所有任务已完成
		return nil
	case <-time.After(timeout):
		// 超时，强制取消
		p.Cancel()
		return ErrPipelineShutdownTimeout
	}
}

// Pipeline errors
var (
	ErrPipelineClosed          = errors.ErrPipelineClosed
	ErrPipelineShutdownTimeout = errors.ErrPipelineShutdownTimeout
)
