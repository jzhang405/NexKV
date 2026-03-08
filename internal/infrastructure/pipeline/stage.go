// Package pipeline 提供通用 Pipeline 框架
//
// 本包实现了基于泛型的 Pipeline 框架，支持：
//   - 类型安全的 Stage 接口（Stage[T]）
//   - 灵活的 Stage 组装（Builder 模式）
//   - 零拷贝数据传递（指针引用）
//   - 背压控制（Channel 容量限制）
//   - 内置监控接口（Metrics、Tracing）
//
// 适用场景：
//   - 异步处理流水线
//   - 数据转换管道
//   - 事件处理链
//   - 任何需要多阶段处理的场景
package pipeline

import (
	"context"
	"errors"
)

var (
	// ErrQueueFull 队列满
	ErrQueueFull = errors.New("pipeline queue full")
	// ErrBackpressure 背压触发
	ErrBackpressure = errors.New("backpressure triggered")
	// ErrNoStages 没有添加任何 Stage
	ErrNoStages = errors.New("no stages added")
)

// Stage[T] 处理阶段接口
//
// T 是输入输出类型，可以是：
//   - *WriteRequest（写请求）
//   - *ReadRequest（读请求）
//   - []byte（原始数据）
//   - any（任意类型）
type Stage[T any] interface {
	// Name 阶段名称（用于监控）
	Name() string

	// Process 处理数据
	//
	// 返回值：
	//   T: 处理后的数据（可以是输入类型，也可以转换类型）
	//   error: 处理错误（会触发 OnError）
	Process(ctx context.Context, item T) (T, error)

	// OnError 错误处理回调
	//
	// 当 Process 返回 error 时调用
	// 可以选择：
	//   1. 返回修改后的 item（继续处理）
	//   2. 返回 error（终止 Pipeline）
	//   3. 记录日志并继续
	OnError(ctx context.Context, item T, err error) (T, error)
}
