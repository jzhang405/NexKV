// Package bftree 提供 Bf-Tree 的异步操作实现
package bftree

import (
	"context"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// GetAsync 异步获取键值
//
// 返回 Task[[]byte]，调用者可以：
// - result, err := task.Wait(ctx)
// - 或：<-task.Done() 等待完成
func (t *BfTree) GetAsync(ctx context.Context, key []byte) model.Task[[]byte] {
	return model.NewBaseTask(
		model.OpStorage,
		model.TaskPriorityNormal,
		model.NewSourceShard("bftree"), // 使用 shard 类型的 SourceID
		func(ctx context.Context, pipeline model.PipelineContext) ([]byte, error) {
			// 直接调用同步方法（MVP 简化实现）
			return t.Get(ctx, key)
		},
	)
}

// SetAsync 异步设置键值
//
// 返回 Task[struct{}]，调用者可以：
// - err := task.Wait(ctx)
// - 或：<-task.Done() 等待完成
func (t *BfTree) SetAsync(ctx context.Context, key, value []byte) model.Task[struct{}] {
	return model.NewBaseTask(
		model.OpStorage,
		model.TaskPriorityNormal,
		model.NewSourceShard("bftree"),
		func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
			// 直接调用同步方法（MVP 简化实现）
			return struct{}{}, t.Set(ctx, key, value)
		},
	)
}

// UpdateAsync 异步更新键值
//
// 返回 Task[struct{}]
func (t *BfTree) UpdateAsync(ctx context.Context, key, value []byte) model.Task[struct{}] {
	return model.NewBaseTask(
		model.OpStorage,
		model.TaskPriorityNormal,
		model.NewSourceShard("bftree"),
		func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
			return struct{}{}, t.Update(ctx, key, value)
		},
	)
}

// DeleteAsync 异步删除键值
//
// 返回 Task[struct{}]
func (t *BfTree) DeleteAsync(ctx context.Context, key []byte) model.Task[struct{}] {
	return model.NewBaseTask(
		model.OpStorage,
		model.TaskPriorityNormal,
		model.NewSourceShard("bftree"),
		func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
			return struct{}{}, t.Delete(ctx, key)
		},
	)
}

// GetStatsAsync 异步获取统计信息
//
// 返回 Task[BfTreeStats]
func (t *BfTree) GetStatsAsync(ctx context.Context) model.Task[BfTreeStats] {
	return model.NewBaseTask(
		model.OpStorage,
		model.TaskPriorityNormal,
		model.NewSourceShard("bftree"),
		func(ctx context.Context, pipeline model.PipelineContext) (BfTreeStats, error) {
			return t.GetStats(), nil
		},
	)
}
