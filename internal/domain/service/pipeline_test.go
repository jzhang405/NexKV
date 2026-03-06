// Package service 定义领域服务
package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// Pipeline 测试
// ==========================================

// mockTaskExecutor 模拟任务执行器
type mockTaskExecutor struct {
	submitted int32
	running   int32
	wg        sync.WaitGroup
}

func (m *mockTaskExecutor) Submit(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, task func(context.Context)) error {
	atomic.AddInt32(&m.submitted, 1)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		atomic.AddInt32(&m.running, 1)
		task(ctx)
		atomic.AddInt32(&m.running, -1)
	}()
	return nil
}

func (m *mockTaskExecutor) SubmitSync(ctx context.Context, sourceID model.SourceID, priority model.TaskPriority, task func(context.Context)) error {
	atomic.AddInt32(&m.submitted, 1)
	task(ctx)
	return nil
}

func (m *mockTaskExecutor) Close() error {
	return nil
}

func (m *mockTaskExecutor) CloseWithTimeout(timeout time.Duration) error {
	return nil
}

func (m *mockTaskExecutor) Stats() *model.TaskPoolStats {
	return &model.TaskPoolStats{}
}

func (m *mockTaskExecutor) wait() {
	m.wg.Wait()
}

// mockTask 模拟任务
type mockTask struct {
	id       string
	priority model.TaskPriority
	sourceID model.SourceID
	executed int32
	delay    time.Duration
}

func (m *mockTask) Run(ctx context.Context, pipeline model.PipelineContext) {
	atomic.AddInt32(&m.executed, 1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
}

func (m *mockTask) SourceID() model.SourceID {
	return m.sourceID
}

func (m *mockTask) Priority() model.TaskPriority {
	return m.priority
}

// mockDelayedTask 带延迟和回调的任务
type mockDelayedTask struct {
	id       string
	delay    time.Duration
	callback func()
}

func (m *mockDelayedTask) Run(ctx context.Context, pipeline model.PipelineContext) {
	time.Sleep(m.delay)
	if m.callback != nil {
		m.callback()
	}
}

func (m *mockDelayedTask) SourceID() model.SourceID {
	return model.SourceNetwork
}

func (m *mockDelayedTask) Priority() model.TaskPriority {
	return model.TaskPriorityNormal
}

// mockCancellableTask 可取消的任务
type mockCancellableTask struct {
	id       string
	callback func(ctx context.Context)
}

func (m *mockCancellableTask) Run(ctx context.Context, pipeline model.PipelineContext) {
	if m.callback != nil {
		m.callback(ctx)
	}
}

func (m *mockCancellableTask) SourceID() model.SourceID {
	return model.SourceNetwork
}

func (m *mockCancellableTask) Priority() model.TaskPriority {
	return model.TaskPriorityNormal
}

// mockEmptyPipelineContext 空的 PipelineContext 实现
type mockEmptyPipelineContext struct {
	ctx context.Context
}

func (m *mockEmptyPipelineContext) Submit(task model.TaskRunner) error {
	return nil
}

func (m *mockEmptyPipelineContext) Executor() model.TaskExecutorRef {
	return nil
}

func TestPipeline_Submit(t *testing.T) {
	executor := &mockTaskExecutor{}
	pipeline := NewPipeline(context.Background(), executor)

	task := &mockTask{
		id:       "test-1",
		priority: model.TaskPriorityNormal,
		sourceID: model.SourceNetwork,
	}

	err := pipeline.Submit(task)
	require.NoError(t, err)

	// 等待任务执行
	time.Sleep(100 * time.Millisecond)
	executor.wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&task.executed))
	assert.Equal(t, int32(1), atomic.LoadInt32(&executor.submitted))
}

func TestPipeline_Submit_AfterClose(t *testing.T) {
	executor := &mockTaskExecutor{}
	pipeline := NewPipeline(context.Background(), executor)

	// 关闭 Pipeline
	err := pipeline.GracefulShutdown(1 * time.Second)
	require.NoError(t, err)

	// 尝试提交任务应该失败
	task := &mockTask{
		id:       "test-2",
		priority: model.TaskPriorityNormal,
		sourceID: model.SourceNetwork,
	}

	err = pipeline.Submit(task)
	assert.Equal(t, ErrPipelineClosed, err)
}

func TestPipeline_GracefulShutdown_Success(t *testing.T) {
	executor := &mockTaskExecutor{}
	pipeline := NewPipeline(context.Background(), executor)

	var taskCompleted int32
	delayedTask := &mockDelayedTask{
		id:       "test-3",
		delay:    100 * time.Millisecond,
		callback: func() { atomic.StoreInt32(&taskCompleted, 1) },
	}

	err := pipeline.Submit(delayedTask)
	require.NoError(t, err)

	// 等待任务开始执行
	time.Sleep(50 * time.Millisecond)

	// 优雅关闭
	start := time.Now()
	err = pipeline.GracefulShutdown(1 * time.Second)
	elapsed := time.Since(start)

	require.NoError(t, err)
	// 由于任务已经执行了 50ms，再等待 100ms 应该在 150ms-250ms 之间
	assert.True(t, elapsed >= 50*time.Millisecond, "Should wait for task to complete")
	assert.Equal(t, int32(1), atomic.LoadInt32(&taskCompleted), "Task should complete")
}

func TestPipeline_GracefulShutdown_Timeout(t *testing.T) {
	executor := &mockTaskExecutor{}
	pipeline := NewPipeline(context.Background(), executor)

	delayedTask := &mockDelayedTask{
		id:    "test-4",
		delay: 10 * time.Second, // 很长的延迟
	}

	err := pipeline.Submit(delayedTask)
	require.NoError(t, err)

	// 等待任务开始执行
	time.Sleep(50 * time.Millisecond)

	// 优雅关闭，设置很短的超时
	start := time.Now()
	err = pipeline.GracefulShutdown(100 * time.Millisecond)
	elapsed := time.Since(start)

	assert.Equal(t, ErrPipelineShutdownTimeout, err)
	assert.True(t, elapsed >= 100*time.Millisecond, "Should wait for timeout")
	assert.Less(t, elapsed, 500*time.Millisecond, "Should not wait too long")
}

func TestPipeline_Cancel(t *testing.T) {
	executor := &mockTaskExecutor{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pipeline := NewPipeline(ctx, executor)

	var taskCanceled int32
	cancellableTask := &mockCancellableTask{
		id: "test-5",
		callback: func(ctx context.Context) {
			select {
			case <-ctx.Done():
				atomic.StoreInt32(&taskCanceled, 1)
			case <-time.After(5 * time.Second):
			}
		},
	}

	err := pipeline.Submit(cancellableTask)
	require.NoError(t, err)

	// 等待任务开始执行
	time.Sleep(50 * time.Millisecond)

	// 立即取消
	pipeline.Cancel()

	// 等待任务响应取消
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), atomic.LoadInt32(&taskCanceled), "Task should be canceled")
}

func TestPipeline_ConcurrentSubmit(t *testing.T) {
	executor := &mockTaskExecutor{}
	pipeline := NewPipeline(context.Background(), executor)

	var wg sync.WaitGroup
	taskCount := 100

	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			task := &mockTask{
				id:       string(rune(id)),
				priority: model.TaskPriorityNormal,
				sourceID: model.SourceNetwork,
			}
			_ = pipeline.Submit(task)
		}(i)
	}

	wg.Wait()

	// 等待所有任务执行
	time.Sleep(200 * time.Millisecond)
	executor.wait()

	assert.Equal(t, int32(taskCount), atomic.LoadInt32(&executor.submitted))
}

// ==========================================
// mockEmptyPipelineContext 测试
// ==========================================

func TestMockEmptyPipelineContext_Submit(t *testing.T) {
	emptyCtx := &mockEmptyPipelineContext{ctx: context.Background()}

	task := &mockTask{
		id:       "test-empty",
		priority: model.TaskPriorityNormal,
		sourceID: model.SourceNetwork,
	}

	// Submit 应该返回 nil（不支持子任务提交）
	err := emptyCtx.Submit(task)
	assert.Nil(t, err)
}

func TestMockEmptyPipelineContext_Executor(t *testing.T) {
	emptyCtx := &mockEmptyPipelineContext{ctx: context.Background()}

	// Executor 应该返回 nil
	executor := emptyCtx.Executor()
	assert.Nil(t, executor)
}

// ==========================================
// BaseTask 竞态条件测试
// ==========================================

func TestBaseTask_ConcurrentRun(t *testing.T) {
	var runCount int32
	task := model.NewBaseTask[struct{}](
		model.OpRPC,
		model.TaskPriorityNormal,
		model.SourceNetwork,
		func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
			atomic.AddInt32(&runCount, 1)
			time.Sleep(100 * time.Millisecond) // 模拟耗时操作
			return struct{}{}, nil
		},
	)

	// 并发调用 Run，确保只执行一次
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task.Run(context.Background(), nil)
		}()
	}

	wg.Wait()

	// 确保只执行了一次
	assert.Equal(t, int32(1), atomic.LoadInt32(&runCount))
	assert.True(t, task.IsDone())
}
