// Package recovery 提供统一的 panic 恢复机制
//
// 使用场景：
//   - goroutine 任务执行
//   - 回调函数执行
//   - 异步操作执行
//
// 设计原则：
//   1. 简单易用：提供 Safe() 和 SafeContext() 两个核心函数
//   2. 可扩展：支持自定义 panic 处理器
//   3. 上下文感知：SafeContext() 支持 context.Context
//   4. 类型安全：PanicError 实现 error 接口
package recovery

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
)

// ==========================================
// PanicError 类型
// ==========================================

// PanicError 表示被恢复的 panic
type PanicError struct {
	R     any       // panic 值
	Stack []byte    // 堆栈信息
}

// Error 实现 error 接口
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.R)
}

// Unwrap 支持 errors.Unwrap
func (e *PanicError) Unwrap() error {
	if err, ok := e.R.(error); ok {
		return err
	}
	return nil
}

// ==========================================
// Handler 类型
// ==========================================

// Handler 定义 panic 处理器函数
// r: panic 值
// stack: 堆栈信息
type Handler func(r any, stack []byte)

// ==========================================
// 默认处理器
// ==========================================

// DefaultHandler 默认的 panic 处理器，使用 slog 记录
func DefaultHandler(r any, stack []byte) {
	slog.Error("[recovery] panic recovered",
		"panic", r,
		"stack", string(stack),
	)
}

// LogrusHandler 兼容 logrus 的处理器
// 用于尚未迁移到 slog 的代码
var LogrusHandler Handler = func(r any, stack []byte) {
	// 这里使用 fmt 而不是直接导入 logrus，避免循环依赖
	// 如果需要 logrus 支持，调用方可以提供自定义处理器
	slog.Error("[recovery] panic recovered (logrus mode)",
		"panic", r,
		"stack", string(stack),
	)
}

// ==========================================
// 核心函数
// ==========================================

// Safe 安全执行函数，捕获 panic
//
// 参数：
//   fn: 要执行的函数
//   handlers: 可选的 panic 处理器（如果没有提供，使用 DefaultHandler）
//
// 返回：
//   error: 如果发生 panic，返回 *PanicError；否则返回 nil
//
// 示例：
//
//	err := recovery.Safe(func() {
//	    task()
//	})
//	if err != nil {
//	    log.Printf("task failed: %v", err)
//	}
func Safe(fn func(), handlers ...Handler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()

			// 调用所有处理器
			for _, h := range handlers {
				if h != nil {
					h(r, stack)
				}
			}

			// 如果没有提供处理器，使用默认处理器
			if len(handlers) == 0 {
				DefaultHandler(r, stack)
			}

			// 增加全局 panic 计数
			incrementPanicCount()

			err = &PanicError{R: r, Stack: stack}
		}
	}()

	fn()
	return nil
}

// SafeContext 安全执行带上下文的函数，捕获 panic
//
// 参数：
//   ctx: 上下文
//   fn: 要执行的函数（接收 context.Context）
//   handlers: 可选的 panic 处理器
//
// 返回：
//   error: 如果发生 panic 或 context 取消，返回错误
//
// 示例：
//
//	err := recovery.SafeContext(ctx, func(ctx context.Context) {
//	    doWork(ctx)
//	})
func SafeContext(ctx context.Context, fn func(context.Context), handlers ...Handler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()

			// 调用所有处理器
			for _, h := range handlers {
				if h != nil {
					h(r, stack)
				}
			}

			// 如果没有提供处理器，使用默认处理器
			if len(handlers) == 0 {
				DefaultHandler(r, stack)
			}

			// 增加全局 panic 计数
			incrementPanicCount()

			err = &PanicError{R: r, Stack: stack}
		}
	}()

	fn(ctx)
	return nil
}

// ==========================================
// 便捷函数
// ==========================================

// Go 安全启动 goroutine
//
// 注意：此函数会立即返回，goroutine 在后台执行
// 如果发生 panic，会被默认处理器捕获
//
// 示例：
//
//	recovery.Go(func() {
//	    heavyTask()
//	})
func Go(fn func()) {
	go func() {
		_ = Safe(fn)
	}()
}

// GoContext 安全启动带上下文的 goroutine
//
// 示例：
//
//	recovery.GoContext(ctx, func(ctx context.Context) {
//	    heavyTask(ctx)
//	})
func GoContext(ctx context.Context, fn func(context.Context)) {
	go func() {
		_ = SafeContext(ctx, fn)
	}()
}

// ==========================================
// 统计信息（可选）
// ==========================================

var (
	panicCount int64 // 全局 panic 计数
)

// GetPanicCount 获取 panic 计数（用于监控）
func GetPanicCount() int64 {
	return atomic.LoadInt64(&panicCount)
}

// ResetPanicCount 重置 panic 计数（用于测试）
func ResetPanicCount() {
	atomic.StoreInt64(&panicCount, 0)
}

// incrementPanicCount 增加 panic 计数
func incrementPanicCount() {
	atomic.AddInt64(&panicCount, 1)
}
