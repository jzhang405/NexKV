// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// 泛型辅助函数：包装接口调用，恢复类型安全
// ==========================================
//
// 设计原理：
// 1. 接口层使用 any 类型（Go 接口不支持泛型方法）
// 2. 实现层保持泛型，提供类型安全的具体方法
// 3. 辅助层提供泛型函数，通过类型断言优先调用实现层的类型安全方法
//
// 使用方式：
//
//	// 类型安全的带参数任务
//	err := concurrency.SubmitWithArg(ctx, provider, func(ctx context.Context, idx int) {
//	    fmt.Println("任务", idx)
//	}, 42)

// defaultWrapTimeout 默认包装超时时间（防止 goroutine 泄漏）
const defaultWrapTimeout = 30 * time.Second

// wrapAnyResult 通用函数：将 Result[any] 包装为 TypedResult[T]
// 处理类型断言和 goroutine 超时控制
// P0-01: 添加 panic 恢复
func wrapAnyResult[T any](anyResult Result[any]) *TypedResult[T] {
	// 类型断言：如果已经是 *AnyResult，直接包装
	if ar, ok := anyResult.(*AnyResult); ok {
		return &TypedResult[T]{inner: ar}
	}

	// 回退：创建新的 AnyResult 包装，带超时控制防止 goroutine 泄漏
	wrapper := NewAnyResult()
	go func() {
		// P0-01: panic 恢复
		defer func() {
			if r := recover(); r != nil {
				wrapper.SetError(errors.Wrapf(errors.ErrCallbackPanic, "panic in wrapAnyResult: %v", r))
			}
		}()

		// 使用超时 context 防止 goroutine 泄漏
		ctx, cancel := context.WithTimeout(context.Background(), defaultWrapTimeout)
		defer cancel()

		val, err := anyResult.Get(ctx)
		if err != nil {
			wrapper.SetError(err)
		} else {
			wrapper.SetValue(val)
		}
	}()
	return &TypedResult[T]{inner: wrapper}
}

// SubmitWithArg 泛型辅助函数：带参数任务（类型安全）
func SubmitWithArg[T any](
	ctx context.Context,
	provider GoroutineProvider,
	task func(context.Context, T),
	arg T,
) error {
	// 类型断言：如果是 AntsGoroutineProvider，直接调用类型安全函数
	if p, ok := provider.(*AntsGoroutineProvider); ok {
		return SubmitWithArgTyped(p, ctx, task, arg)
	}

	// 回退：用接口的 any 方法
	return provider.SubmitWithArg(ctx, func(ctx context.Context, a any) {
		task(ctx, a.(T))
	}, arg)
}

// SubmitWithResult 泛型辅助函数：带返回值任务（类型安全）
func SubmitWithResult[T any](
	ctx context.Context,
	provider GoroutineProvider,
	task func(context.Context) (T, error),
) *TypedResult[T] {
	// 类型断言：如果是 AntsGoroutineProvider，直接调用类型安全函数
	if p, ok := provider.(*AntsGoroutineProvider); ok {
		return SubmitWithResultTyped(p, ctx, task)
	}

	// 回退：用接口的 any 方法，包装结果
	anyResult := provider.SubmitWithResult(ctx, func(ctx context.Context) (any, error) {
		return task(ctx)
	})
	return wrapAnyResult[T](anyResult)
}

// SubmitWithArgAndResult 泛型辅助函数：带参数和返回值任务（类型安全）
func SubmitWithArgAndResult[T any, R any](
	ctx context.Context,
	provider GoroutineProvider,
	task func(context.Context, T) (R, error),
	arg T,
) *TypedResult[R] {
	// 类型断言：如果是 AntsGoroutineProvider，直接调用类型安全函数
	if p, ok := provider.(*AntsGoroutineProvider); ok {
		return SubmitWithArgAndResultTyped(p, ctx, task, arg)
	}

	// 回退：用接口的 any 方法，包装结果
	anyResult := provider.SubmitWithArgAndResult(ctx, func(ctx context.Context, a any) (any, error) {
		return task(ctx, a.(T))
	}, arg)
	return wrapAnyResult[R](anyResult)
}

// SubmitAdvanced 泛型辅助函数：高级任务提交（类型安全）
func SubmitAdvanced[T any, R any](
	ctx context.Context,
	provider GoroutineProvider,
	task func(context.Context, T) (R, error),
	arg T,
	opts ...GoroutineSubmitOption,
) *TypedResult[R] {
	// 类型断言：如果是 AntsGoroutineProvider，直接调用类型安全函数
	if p, ok := provider.(*AntsGoroutineProvider); ok {
		return SubmitAdvancedTyped(p, ctx, task, arg, opts...)
	}

	// 回退：用接口的 any 方法，包装结果
	anyResult := provider.SubmitAdvanced(ctx, func(ctx context.Context, a any) (any, error) {
		return task(ctx, a.(T))
	}, arg, opts...)
	return wrapAnyResult[R](anyResult)
}

// SubmitBatchWithArg 泛型辅助函数：批量带参数任务（类型安全）
func SubmitBatchWithArg[T any](
	ctx context.Context,
	provider GoroutineProvider,
	tasks []func(context.Context, T),
	args []T,
) error {
	if len(tasks) != len(args) {
		return ErrTaskArgLengthMismatch
	}

	// 转换为 any 类型
	anyTasks := make([]func(context.Context, any), len(tasks))
	anyArgs := make([]any, len(args))
	for i, task := range tasks {
		task := task // 捕获循环变量
		arg := args[i]
		anyTasks[i] = func(ctx context.Context, a any) {
			task(ctx, a.(T))
		}
		anyArgs[i] = arg
	}

	return provider.SubmitBatchWithArg(ctx, anyTasks, anyArgs)
}

// SubmitBatchWithResult 泛型辅助函数：批量带返回值任务（类型安全）
func SubmitBatchWithResult[T any](
	ctx context.Context,
	provider GoroutineProvider,
	tasks []func(context.Context) (T, error),
) []*TypedResult[T] {
	// 转换为 any 类型
	anyTasks := make([]func(context.Context) (any, error), len(tasks))
	for i, task := range tasks {
		task := task // 捕获循环变量
		anyTasks[i] = func(ctx context.Context) (any, error) {
			return task(ctx)
		}
	}

	anyResults := provider.SubmitBatchWithResult(ctx, anyTasks)

	// 包装结果
	results := make([]*TypedResult[T], len(anyResults))
	for i, r := range anyResults {
		// 需要将 Result[any] 转换为 *AnyResult
		if ar, ok := r.(*AnyResult); ok {
			results[i] = &TypedResult[T]{inner: ar}
		}
	}
	return results
}
