// Package concurrency 提供任务池和定时任务管理
package concurrency

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ==========================================
// TaskPoolProvider 基础测试
// ==========================================

func TestAntsTaskExecutorProvider_BasicSubmit(t *testing.T) {
	config := &ProviderConfig{
		Capacity: 10,
	}

	provider, err := NewAntsExecutor(config)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer provider.Close()

	t.Run("Submit simple task", func(t *testing.T) {
		done := make(chan bool)
		ctx := context.Background()

		err := provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {
			done <- true
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		select {
		case <-done:
			// 成功
		case <-time.After(time.Second):
			t.Error("task did not complete in time")
		}
	})

	t.Run("Submit with different priorities", func(t *testing.T) {
		done := make(chan bool, 3)
		ctx := context.Background()

		// 提交不同优先级的任务
		_ = provider.Submit(ctx, model.TaskPriorityHigh, func(ctx context.Context) {
			done <- true
		})
		_ = provider.Submit(ctx, model.TaskPriorityNormal, func(ctx context.Context) {
			done <- true
		})
		_ = provider.Submit(ctx, model.TaskPriorityLow, func(ctx context.Context) {
			done <- true
		})

		// 等待所有任务完成
		count := 0
		timeout := time.After(time.Second)
		for {
			select {
			case <-done:
				count++
				if count == 3 {
					return // 所有任务完成
				}
			case <-timeout:
				if count != 3 {
					t.Errorf("expected 3 tasks, got %d", count)
				}
				return
			}
		}
	})
}
