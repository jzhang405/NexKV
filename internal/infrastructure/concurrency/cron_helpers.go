// Package concurrency 提供协程池和定时任务管理
package concurrency

import "context"

// ==========================================
// CronJobProvider 泛型辅助函数
// ==========================================
//
// 设计原理：
// 1. 接口层使用 any 类型（Go 接口不支持泛型方法）
// 2. 实现层保持泛型，提供类型安全的具体方法
// 3. 辅助层提供泛型函数，通过类型断言优先调用实现层的类型安全方法
//
// 使用方式：
//
//	// 类型安全的带参数定时任务
//	jobID, err := concurrency.RegisterWithArg(ctx, cronProvider, "0 */5 * * * *", "cleanup", func(ctx context.Context, dir string) {
//	    cleanupDir(ctx, dir)
//	}, "/var/data")

// RegisterWithArg 泛型辅助函数：注册带参数的定时任务（类型安全）
func RegisterWithArg[T any](
	provider CronJobProvider,
	spec CronSpec,
	name string,
	task func(context.Context, T),
	arg T,
) (string, error) {
	// 类型断言：如果是 RobfigCronProvider，直接调用类型安全函数
	if p, ok := provider.(*RobfigCronProvider); ok {
		return RegisterWithArgTyped(p, spec, name, task, arg)
	}

	// 回退：用接口的 any 方法
	return provider.RegisterWithArg(spec, name, func(ctx context.Context, a any) {
		task(ctx, a.(T))
	}, arg)
}

// RegisterWithPriorityAndArg 泛型辅助函数：注册带参数和优先级的定时任务（类型安全）
func RegisterWithPriorityAndArg[T any](
	provider CronJobProvider,
	spec CronSpec,
	name string,
	priority Priority,
	task func(context.Context, T),
	arg T,
) (string, error) {
	// 类型断言：如果是 RobfigCronProvider，直接调用类型安全函数
	if p, ok := provider.(*RobfigCronProvider); ok {
		return RegisterWithPriorityAndArgTyped(p, spec, name, priority, task, arg)
	}

	// 回退：用接口的 any 方法
	return provider.RegisterWithPriorityAndArg(spec, name, priority, func(ctx context.Context, a any) {
		task(ctx, a.(T))
	}, arg)
}
