// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ============================================================================
// 并行执行工具（使用 GoroutineProvider）
// ============================================================================

// ParallelResult 并行执行结果
type ParallelResult[T any] struct {
	Index int   // 任务索引
	Value T     // 返回值
	Err   error // 错误
}

// ParallelConfig 并行执行配置
type ParallelConfig struct {
	// MaxConcurrent 最大并发数（0 表示无限制）
	MaxConcurrent int
	// FailFast 是否在首个错误时快速失败
	FailFast bool
}

// ParallelOption 并行执行选项
type ParallelOption func(*ParallelConfig)

// WithMaxConcurrent 设置最大并发数
func WithMaxConcurrent(n int) ParallelOption {
	return func(c *ParallelConfig) {
		c.MaxConcurrent = n
	}
}

// WithFailFast 设置快速失败模式
func WithFailFast(failFast bool) ParallelOption {
	return func(c *ParallelConfig) {
		c.FailFast = failFast
	}
}

// ParallelExecute 并行执行任务（无返回值）
//
// 使用示例:
//
//	err := concurrency.ParallelExecute(ctx, provider, len(tasks), 10, func(ctx context.Context, i int) error {
//	    return processTask(tasks[i])
//	})
func ParallelExecute(
	ctx context.Context,
	provider service.GoroutineProvider,
	taskCount int,
	maxConcurrent int,
	handler func(ctx context.Context, index int) error,
	opts ...ParallelOption,
) error {
	if taskCount == 0 {
		return nil
	}

	// 如果没有提供 provider，使用默认的 ants provider
	if provider == nil {
		provider = defaultProvider
	}

	cfg := &ParallelConfig{MaxConcurrent: maxConcurrent}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = taskCount // 无限制
	}

	var (
		wg       sync.WaitGroup
		firstErr error
		errOnce  sync.Once
		canceled atomic.Bool
		sem      = make(chan struct{}, cfg.MaxConcurrent)
	)

	// 创建错误通道用于快速失败
	errCh := make(chan error, 1)

executeLoop:
	for i := range taskCount {
		// 检查是否已取消或快速失败
		if canceled.Load() {
			break executeLoop
		}

		select {
		case <-ctx.Done():
			if cfg.FailFast {
				return ctx.Err()
			}
			canceled.Store(true)
			break executeLoop
		default:
		}

		// 检查是否需要快速失败（在启动 goroutine 之前）
		if canceled.Load() {
			break executeLoop
		}

		wg.Add(1)
		idx := i // 避免闭包陷阱

		err := provider.Submit(ctx, func(taskCtx context.Context) {
			defer wg.Done()

			// 信号量控制并发
			sem <- struct{}{}
			defer func() { <-sem }()

			// 再次检查取消状态
			if canceled.Load() {
				return
			}

			if err := handler(taskCtx, idx); err != nil {
				if cfg.FailFast {
					errOnce.Do(func() {
						firstErr = err
						canceled.Store(true)
						select {
						case errCh <- err:
						default:
						}
					})
				}
			}
		})

		if err != nil {
			wg.Done()
			if cfg.FailFast {
				return err
			}
		}
	}

	// 等待所有任务完成或快速失败
	if cfg.FailFast {
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case err := <-errCh:
			return err
		}
	} else {
		wg.Wait()
	}

	return firstErr
}

// ParallelExecuteWithResult 并行执行任务（带返回值）
//
// 使用示例:
//
//	results := concurrency.ParallelExecuteWithResult(ctx, provider, len(tasks), 10, func(ctx context.Context, i int) (Result, error) {
//	    return processTask(tasks[i])
//	})
//	for _, r := range results {
//	    if r.Err != nil {
//	        // 处理错误
//	    }
//	    // 使用 r.Value
//	}
func ParallelExecuteWithResult[T any](
	ctx context.Context,
	provider service.GoroutineProvider,
	taskCount int,
	maxConcurrent int,
	handler func(ctx context.Context, index int) (T, error),
	opts ...ParallelOption,
) []ParallelResult[T] {
	if taskCount == 0 {
		return nil
	}

	// 如果没有提供 provider，使用默认的 ants provider
	if provider == nil {
		provider = defaultProvider
	}

	cfg := &ParallelConfig{MaxConcurrent: maxConcurrent}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = taskCount
	}

	var (
		wg       sync.WaitGroup
		canceled atomic.Bool
		sem      = make(chan struct{}, cfg.MaxConcurrent)
		results  = make([]ParallelResult[T], taskCount)
		mu       sync.Mutex
	)

resultLoop:
	for i := range taskCount {
		if canceled.Load() {
			break resultLoop
		}

		select {
		case <-ctx.Done():
			canceled.Store(true)
			break resultLoop
		default:
		}

		wg.Add(1)
		idx := i // 避免闭包陷阱

		err := provider.Submit(ctx, func(taskCtx context.Context) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			if canceled.Load() {
				var zero T
				mu.Lock()
				results[idx] = ParallelResult[T]{Index: idx, Value: zero, Err: taskCtx.Err()}
				mu.Unlock()
				return
			}

			value, err := handler(taskCtx, idx)
			mu.Lock()
			results[idx] = ParallelResult[T]{Index: idx, Value: value, Err: err}
			mu.Unlock()
		})

		if err != nil {
			wg.Done()
			var zero T
			mu.Lock()
			results[idx] = ParallelResult[T]{Index: idx, Value: zero, Err: err}
			mu.Unlock()
		}
	}

	wg.Wait()
	return results
}

// ParallelExecuteWithArg 并行执行任务（带参数）
//
// 使用示例:
//
//	err := concurrency.ParallelExecuteWithArg(ctx, provider, peers, 10, func(ctx context.Context, peer PeerID) error {
//	    return sendToPeer(peer)
//	})
func ParallelExecuteWithArg[T any](
	ctx context.Context,
	provider service.GoroutineProvider,
	args []T,
	maxConcurrent int,
	handler func(ctx context.Context, arg T) error,
	opts ...ParallelOption,
) error {
	if len(args) == 0 {
		return nil
	}

	return ParallelExecute(ctx, provider, len(args), maxConcurrent, func(taskCtx context.Context, i int) error {
		return handler(taskCtx, args[i])
	}, opts...)
}

// ParallelExecuteWithArgAndResult 并行执行任务（带参数和返回值）
//
// 使用示例:
//
//	results := concurrency.ParallelExecuteWithArgAndResult(ctx, provider, peers, 10, func(ctx context.Context, peer PeerID) (Response, error) {
//	    return callPeer(peer)
//	})
func ParallelExecuteWithArgAndResult[T any, R any](
	ctx context.Context,
	provider service.GoroutineProvider,
	args []T,
	maxConcurrent int,
	handler func(ctx context.Context, arg T) (R, error),
	opts ...ParallelOption,
) []ParallelResult[R] {
	if len(args) == 0 {
		return nil
	}

	return ParallelExecuteWithResult(ctx, provider, len(args), maxConcurrent, func(taskCtx context.Context, i int) (R, error) {
		return handler(taskCtx, args[i])
	}, opts...)
}

// ============================================================================
// 默认 Provider（向后兼容）
// ============================================================================

// defaultProvider 是包级别的默认 GoroutineProvider
// 在包初始化时创建，用于向后兼容（当调用者传入 nil provider 时使用）
var defaultProvider service.GoroutineProvider

// SetDefaultProvider 设置默认的 GoroutineProvider
// 应在应用程序初始化时调用
func SetDefaultProvider(provider service.GoroutineProvider) {
	defaultProvider = provider
}

// GetDefaultProvider 获取默认的 GoroutineProvider
func GetDefaultProvider() service.GoroutineProvider {
	return defaultProvider
}
