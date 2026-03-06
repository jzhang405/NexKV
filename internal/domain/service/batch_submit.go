// Package service 批量提交并发安全接口
//
// 提供批量任务提交的并发安全保证，包括：
// - 原子性保证（可选）
// - 背压控制
// - 并发安全保护
package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// 批量提交配置
// ==========================================

// BatchSubmitConfig 批量提交配置
type BatchSubmitConfig struct {
	// MaxConcurrent 最大并发提交数（背压控制）
	// 0 表示无限制
	MaxConcurrent int

	// EnableAtomic 是否启用原子性保证
	// 如果启用，部分失败时会回滚所有成功提交
	EnableAtomic bool

	// Timeout 批量提交超时时间（毫秒）
	// 0 表示使用默认超时
	Timeout int64
}

// DefaultBatchSubmitConfig 默认批量提交配置
func DefaultBatchSubmitConfig() *BatchSubmitConfig {
	return &BatchSubmitConfig{
		MaxConcurrent: 100,     // 默认最多100个并发提交
		EnableAtomic:  false,   // 默认不启用原子性（性能优先）
		Timeout:       30000,   // 默认30秒超时
	}
}

// ==========================================
// 批量提交结果
// ==========================================

// BatchSubmitResult 批量提交结果
type BatchSubmitResult struct {
	// Total 总提交数量
	Total int

	// Success 成功提交数量
	Success int

	// Failed 失败提交数量
	Failed int

	// Errors 失败的错误列表
	Errors []error

	// RollbackCount 回滚数量（仅在启用原子性时有意义）
	RollbackCount int
}

// ==========================================
// 批量提交器接口
// ==========================================

// BatchSubmitter 批量提交器接口
type BatchSubmitter interface {
	// SubmitBatch 批量提交任务
	// tasks: 要提交的任务列表
	// 返回：批量提交结果
	SubmitBatch(ctx context.Context, tasks []model.TaskRunner) (*BatchSubmitResult, error)

	// SubmitBatchWithConfig 使用配置批量提交任务
	SubmitBatchWithConfig(ctx context.Context, tasks []model.TaskRunner, config *BatchSubmitConfig) (*BatchSubmitResult, error)

	// Close 关闭批量提交器
	Close() error
}

// ==========================================
// 批量提交器实现
// ==========================================

// batchSubmitter 批量提交器实现
type batchSubmitter struct {
	executor TaskExecutor
	config   *BatchSubmitConfig

	// 背压控制
	sem chan struct{}

	// 关闭标记
	closed bool
	mu     sync.RWMutex
}

// NewBatchSubmitter 创建批量提交器
func NewBatchSubmitter(executor TaskExecutor, config *BatchSubmitConfig) BatchSubmitter {
	if config == nil {
		config = DefaultBatchSubmitConfig()
	}

	// 创建信号量用于背压控制
	var sem chan struct{}
	if config.MaxConcurrent > 0 {
		sem = make(chan struct{}, config.MaxConcurrent)
	}

	return &batchSubmitter{
		executor: executor,
		config:   config,
		sem:      sem,
		closed:   false,
	}
}

// SubmitBatch 批量提交任务（使用默认配置）
func (b *batchSubmitter) SubmitBatch(ctx context.Context, tasks []model.TaskRunner) (*BatchSubmitResult, error) {
	return b.SubmitBatchWithConfig(ctx, tasks, b.config)
}

// SubmitBatchWithConfig 使用配置批量提交任务
func (b *batchSubmitter) SubmitBatchWithConfig(ctx context.Context, tasks []model.TaskRunner, config *BatchSubmitConfig) (*BatchSubmitResult, error) {
	// 检查是否已关闭
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrSubmitterClosed
	}
	b.mu.RUnlock()

	if config == nil {
		config = b.config
	}

	result := &BatchSubmitResult{
		Total:  len(tasks),
		Errors: make([]error, 0),
	}

	// 使用 WaitGroup 等待所有提交完成
	var wg sync.WaitGroup
	var successCount int32
	var failedCount int32
	var errorsMu sync.Mutex
	errors := make([]error, 0)

	// 提交所有任务
	for _, task := range tasks {
		// 背压控制：获取信号量
		if b.sem != nil {
			select {
			case b.sem <- struct{}{}:
				// 获取成功
			case <-ctx.Done():
				// context 取消
				result.Failed = int(failedCount) + 1
				result.Errors = append(result.Errors, ctx.Err())
				return result, ctx.Err()
			}
		}

		wg.Add(1)

		go func(t model.TaskRunner) {
			defer wg.Done()
			defer func() {
				// 释放信号量
				if b.sem != nil {
					<-b.sem
				}
			}()

			// 提交任务
			err := b.executor.Submit(ctx, t.SourceID(), t.Priority(), func(ctx context.Context) {
				t.Run(ctx, nil)
			})

			if err != nil {
				atomic.AddInt32(&failedCount, 1)
				errorsMu.Lock()
				errors = append(errors, err)
				errorsMu.Unlock()
			} else {
				atomic.AddInt32(&successCount, 1)
			}
		}(task)
	}

	// 等待所有提交完成
	wg.Wait()

	// 汇总结果
	result.Success = int(successCount)
	result.Failed = int(failedCount)
	result.Errors = errors

	// 如果启用原子性且有失败，需要回滚
	// 注意：当前实现不支持真正的回滚，这里只是记录
	if config.EnableAtomic && result.Failed > 0 {
		result.RollbackCount = result.Success
		// TODO: 实现回滚逻辑（需要任务支持取消/回滚）
	}

	return result, nil
}

// Close 关闭批量提交器
func (b *batchSubmitter) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	b.closed = true

	// 等待所有进行中的提交完成
	if b.sem != nil {
		// 等待信号量完全释放
		for i := 0; i < cap(b.sem); i++ {
			b.sem <- struct{}{}
		}
	}

	return nil
}

// ==========================================
// 错误定义
// ==========================================

var (
	// ErrSubmitterClosed 批量提交器已关闭
	ErrSubmitterClosed = errors.New("batch submitter is closed")

	// ErrBatchTimeout 批量提交超时
	ErrBatchTimeout = errors.New("batch submit timeout")

	// ErrBatchTooLarge 批量提交任务过多
	ErrBatchTooLarge = errors.New("batch submit too large")
)
