// Package service 批量提交并发安全测试
package service

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// Mock Executor（用于测试）
// ==========================================

// mockBatchExecutor 模拟 TaskExecutor
type mockBatchExecutor struct {
	submitCount int32
	failCount   int32
	delay       time.Duration
}

func (m *mockBatchExecutor) Submit(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, task func(context.Context)) error {
	atomic.AddInt32(&m.submitCount, 1)

	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			atomic.AddInt32(&m.failCount, 1)
			return ctx.Err()
		}
	}

	// 执行任务
	if task != nil {
		task(ctx)
	}

	return nil
}

func (m *mockBatchExecutor) SubmitSync(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, task func(context.Context)) error {
	return m.Submit(ctx, sourceID, priority, task)
}

func (m *mockBatchExecutor) Go(ctx context.Context, fn func() error) error {
	return nil
}

func (m *mockBatchExecutor) GoNoWait(ctx context.Context, fn func()) {
	// 空实现
}

func (m *mockBatchExecutor) Close() error {
	return nil
}

// ==========================================
// 基础功能测试
// ==========================================

// TestBatchSubmitter_SubmitBatch 测试基础批量提交
func TestBatchSubmitter_SubmitBatch(t *testing.T) {
	executor := &mockBatchExecutor{}
	config := &BatchSubmitConfig{
		MaxConcurrent: 10,
		EnableAtomic:  false,
	}

	submitter := NewBatchSubmitter(executor, config)
	defer submitter.Close()

	// 创建测试任务
	tasks := make([]model.TaskRunner, 20)
	for i := 0; i < 20; i++ {
		tasks[i] = model.NewBaseTask[struct{}](
			model.OpRPC,
			model.TaskPriorityNormal,
			model.SourceNetwork,
			func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
				return struct{}{}, nil
			},
		)
	}

	// 批量提交
	result, err := submitter.SubmitBatch(context.Background(), tasks)

	require.NoError(t, err)
	assert.Equal(t, 20, result.Total)
	assert.Equal(t, 20, result.Success)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, int32(20), atomic.LoadInt32(&executor.submitCount))
}

// TestBatchSubmitter_BackPressure 测试背压控制
func TestBatchSubmitter_BackPressure(t *testing.T) {
	executor := &mockBatchExecutor{
		delay: 100 * time.Millisecond, // 100ms 延迟
	}
	config := &BatchSubmitConfig{
		MaxConcurrent: 5, // 最多 5 个并发
		EnableAtomic:  false,
	}

	submitter := NewBatchSubmitter(executor, config)
	defer submitter.Close()

	// 创建 20 个任务
	tasks := make([]model.TaskRunner, 20)
	for i := 0; i < 20; i++ {
		tasks[i] = model.NewBaseTask[struct{}](
			model.OpRPC,
			model.TaskPriorityNormal,
			model.SourceNetwork,
			func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
				return struct{}{}, nil
			},
		)
	}

	// 批量提交
	start := time.Now()
	result, err := submitter.SubmitBatch(context.Background(), tasks)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, 20, result.Total)
	assert.Equal(t, 20, result.Success)

	// 验证背压控制：20个任务，最多5个并发，每个100ms
	// 理论上至少需要 400ms（4轮，每轮5个）
	assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond,
		"Back pressure control should limit concurrency")
}

// TestBatchSubmitter_ConcurrentSubmit 测试并发批量提交
func TestBatchSubmitter_ConcurrentSubmit(t *testing.T) {
	executor := &mockBatchExecutor{}
	config := &BatchSubmitConfig{
		MaxConcurrent: 50,
		EnableAtomic:  false,
	}

	submitter := NewBatchSubmitter(executor, config)
	defer submitter.Close()

	const batchCount = 10
	const tasksPerBatch = 50

	var wg sync.WaitGroup
	var successCount int32

	// 并发提交 10 个批次
	for i := 0; i < batchCount; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			tasks := make([]model.TaskRunner, tasksPerBatch)
			for j := 0; j < tasksPerBatch; j++ {
				tasks[j] = model.NewBaseTask[struct{}](
					model.OpRPC,
					model.TaskPriorityNormal,
					model.SourceNetwork,
					func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
						atomic.AddInt32(&successCount, 1)
						return struct{}{}, nil
					},
				)
			}

			_, _ = submitter.SubmitBatch(context.Background(), tasks)
		}()
	}

	// 等待所有批次完成
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 所有批次已完成
		assert.Equal(t, int32(batchCount*tasksPerBatch), atomic.LoadInt32(&executor.submitCount))
	case <-time.After(5 * time.Second):
		t.Fatal("Concurrent submit timed out")
	}
}

// TestBatchSubmitter_ContextCancellation 测试 context 取消
func TestBatchSubmitter_ContextCancellation(t *testing.T) {
	executor := &mockBatchExecutor{
		delay: 5 * time.Second, // 长延迟
	}
	config := &BatchSubmitConfig{
		MaxConcurrent: 1, // 串行执行
		EnableAtomic:  false,
	}

	submitter := NewBatchSubmitter(executor, config)
	defer submitter.Close()

	// 创建任务
	tasks := make([]model.TaskRunner, 10)
	for i := 0; i < 10; i++ {
		tasks[i] = model.NewBaseTask[struct{}](
			model.OpRPC,
			model.TaskPriorityNormal,
			model.SourceNetwork,
			func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
				return struct{}{}, nil
			},
		)
	}

	// 创建可取消的 context
	ctx, cancel := context.WithCancel(context.Background())

	// 启动批量提交
	done := make(chan *BatchSubmitResult)
	go func() {
		result, _ := submitter.SubmitBatch(ctx, tasks)
		done <- result
	}()

	// 等待一小段时间后取消
	time.Sleep(50 * time.Millisecond)
	cancel()

	// 等待提交完成
	select {
	case result := <-done:
		// 提交应该因为 context 取消而中断
		assert.Less(t, result.Success, 10, "Should not complete all tasks after cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("Submit should be cancelled quickly")
	}
}

// TestBatchSubmitter_NoGoroutineLeak 测试没有 goroutine 泄漏
func TestBatchSubmitter_NoGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	executor := &mockBatchExecutor{
		delay: time.Millisecond,
	}
	config := &BatchSubmitConfig{
		MaxConcurrent: 100,
		EnableAtomic:  false,
	}

	// 创建并使用多个批量提交器
	for i := 0; i < 10; i++ {
		submitter := NewBatchSubmitter(executor, config)

		tasks := make([]model.TaskRunner, 50)
		for j := 0; j < 50; j++ {
			tasks[j] = model.NewBaseTask[struct{}](
				model.OpRPC,
				model.TaskPriorityNormal,
				model.SourceNetwork,
				func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
					return struct{}{}, nil
				},
			)
		}

		_, err := submitter.SubmitBatch(context.Background(), tasks)
		require.NoError(t, err)

		// 关闭提交器
		err = submitter.Close()
		require.NoError(t, err)
	}

	// 等待 goroutine 退出
	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	leaked := finalGoroutines - initialGoroutines

	assert.LessOrEqual(t, leaked, 5, "Expected no goroutine leak, but found %d", leaked)
}

// TestBatchSubmitter_HighLoad 测试高负载场景
func TestBatchSubmitter_HighLoad(t *testing.T) {
	executor := &mockBatchExecutor{}
	config := &BatchSubmitConfig{
		MaxConcurrent: 200,
		EnableAtomic:  false,
	}

	submitter := NewBatchSubmitter(executor, config)
	defer submitter.Close()

	const totalTasks = 1000
	tasks := make([]model.TaskRunner, totalTasks)

	for i := 0; i < totalTasks; i++ {
		tasks[i] = model.NewBaseTask[struct{}](
			model.OpRPC,
			model.TaskPriorityNormal,
			model.SourceNetwork,
			func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
				return struct{}{}, nil
			},
		)
	}

	// 批量提交
	start := time.Now()
	result, err := submitter.SubmitBatch(context.Background(), tasks)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, totalTasks, result.Total)
	assert.Equal(t, totalTasks, result.Success)
	assert.Equal(t, 0, result.Failed)

	// 验证性能：1000个任务应该在合理时间内完成
	assert.Less(t, elapsed, 2*time.Second,
		"High load test should complete within 2 seconds, but took %v", elapsed)

	t.Logf("Submitted %d tasks in %v (%.0f tasks/sec)",
		totalTasks, elapsed, float64(totalTasks)/elapsed.Seconds())
}

// TestBatchSubmitter_Close 测试关闭功能
func TestBatchSubmitter_Close(t *testing.T) {
	executor := &mockBatchExecutor{}
	config := &BatchSubmitConfig{
		MaxConcurrent: 10,
		EnableAtomic:  false,
	}

	submitter := NewBatchSubmitter(executor, config)

	// 关闭提交器
	err := submitter.Close()
	require.NoError(t, err)

	// 再次关闭应该是幂等的
	err = submitter.Close()
	require.NoError(t, err)

	// 关闭后提交应该失败
	tasks := make([]model.TaskRunner, 1)
	tasks[0] = model.NewBaseTask[struct{}](
		model.OpRPC,
		model.TaskPriorityNormal,
		model.SourceNetwork,
		func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
			return struct{}{}, nil
		},
	)

	_, err = submitter.SubmitBatch(context.Background(), tasks)
	assert.Equal(t, ErrSubmitterClosed, err)
}
