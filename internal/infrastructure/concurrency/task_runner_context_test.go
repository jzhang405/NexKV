// Package concurrency 并发原语测试
package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// TaskRunnerContext 测试
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

func (m *mockTask) Run(ctx context.Context, pipeline model.TaskRunnerContext) {
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

func (m *mockDelayedTask) Run(ctx context.Context, pipeline model.TaskRunnerContext) {
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

func (m *mockCancellableTask) Run(ctx context.Context, pipeline model.TaskRunnerContext) {
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
	pipeline := NewTaskRunnerContext(context.Background(), executor)

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
	pipeline := NewTaskRunnerContext(context.Background(), executor)

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
	assert.Equal(t, service.ErrTaskRunnerClosed, err)
}

func TestPipeline_GracefulShutdown_Success(t *testing.T) {
	executor := &mockTaskExecutor{}
	pipeline := NewTaskRunnerContext(context.Background(), executor)

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
	// 验证任务完成（主要断言：确保 GracefulShutdown 等待了任务执行完毕）
	assert.Equal(t, int32(1), atomic.LoadInt32(&taskCompleted), "Task should complete")
	// 时间断言仅作为辅助检查，放宽下限以容忍 CI 环境中的调度延迟
	assert.True(t, elapsed >= 30*time.Millisecond, "Should wait at least 30ms for task to complete")
}

func TestPipeline_GracefulShutdown_Timeout(t *testing.T) {
	executor := &mockTaskExecutor{}
	pipeline := NewTaskRunnerContext(context.Background(), executor)

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

	assert.Equal(t, service.ErrTaskRunnerShutdownTimeout, err)
	assert.True(t, elapsed >= 100*time.Millisecond, "Should wait for timeout")
	assert.Less(t, elapsed, 500*time.Millisecond, "Should not wait too long")
}

func TestPipeline_Cancel(t *testing.T) {
	executor := &mockTaskExecutor{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pipeline := NewTaskRunnerContext(ctx, executor)

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
	pipeline := NewTaskRunnerContext(context.Background(), executor)

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
	task := model.NewBaseTask(
		model.TaskPriorityNormal,
		0, // maxRetries
		func(ctx context.Context, pipeline model.TaskRunnerContext) (struct{}, error) {
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

// ==========================================
// TaskRunnerContext 新增测试
// ==========================================

// TestTaskRunnerContext_Executor 测试 Executor 方法
func TestTaskRunnerContext_Executor(t *testing.T) {
	executor := &mockTaskExecutor{}
	taskCtx := NewTaskRunnerContext(context.Background(), executor)

	// Executor 应该返回传入的 executor
	result := taskCtx.Executor()
	assert.Equal(t, executor, result)
}

// TestTaskRunnerContext_Context 测试 Context 方法
func TestTaskRunnerContext_Context(t *testing.T) {
	baseCtx := context.Background()
	taskCtx := NewTaskRunnerContext(baseCtx, &mockTaskExecutor{})

	// Context 应该返回可取消的上下文
	result := taskCtx.Context()
	assert.NotNil(t, result)

	// 验证 context 可以被取消
	select {
	case <-result.Done():
		t.Error("Context should not be cancelled initially")
	default:
	}

	taskCtx.Cancel()

	select {
	case <-result.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Context should be cancelled after Cancel()")
	}
}

// TestTaskRunnerContext_InterfaceCompliance 验证接口实现
func TestTaskRunnerContext_InterfaceCompliance(t *testing.T) {
	executor := &mockTaskExecutor{}
	taskCtx := NewTaskRunnerContext(context.Background(), executor)

	// 验证实现 model.TaskRunnerContext 接口
	var _ model.TaskRunnerContext = taskCtx

	// 验证实现 service.TaskRunnerContext 接口
	var _ service.TaskRunnerContext = taskCtx

	assert.NotNil(t, taskCtx)
}

// TestTaskRunnerContext_MultipleGracefulShutdown 测试多次调用 GracefulShutdown
func TestTaskRunnerContext_MultipleGracefulShutdown(t *testing.T) {
	executor := &mockTaskExecutor{}
	taskCtx := NewTaskRunnerContext(context.Background(), executor)

	// 第一次调用应该成功
	err := taskCtx.GracefulShutdown(1 * time.Second)
	assert.NoError(t, err)

	// 第二次调用也应该成功（幂等性）
	err = taskCtx.GracefulShutdown(1 * time.Second)
	assert.NoError(t, err)
}

// TestTaskRunnerContext_ConcurrentSubmitAndShutdown 测试并发提交和关闭
func TestTaskRunnerContext_ConcurrentSubmitAndShutdown(t *testing.T) {
	executor := &mockTaskExecutor{}
	taskCtx := NewTaskRunnerContext(context.Background(), executor)

	const taskCount = 50
	var wg sync.WaitGroup

	// 并发提交任务
	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			task := &mockTask{
				id:       string(rune(id)),
				priority: model.TaskPriorityNormal,
				sourceID: model.SourceNetwork,
				delay:    50 * time.Millisecond,
			}
			_ = taskCtx.Submit(task)
		}(i)
	}

	// 等待所有提交完成
	wg.Wait()

	// 立即开始优雅关闭
	done := make(chan error, 1)
	go func() {
		done <- taskCtx.GracefulShutdown(2 * time.Second)
	}()

	// 验证关闭成功
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("GracefulShutdown timed out")
	}

	executor.wait()
	assert.Equal(t, int32(taskCount), atomic.LoadInt32(&executor.submitted))
}

// TestTaskRunnerContext_SubmitAfterCancel 测试 Cancel 后提交任务
func TestTaskRunnerContext_SubmitAfterCancel(t *testing.T) {
	executor := &mockTaskExecutor{}
	taskCtx := NewTaskRunnerContext(context.Background(), executor)

	// 取消上下文
	taskCtx.Cancel()

	// 尝试提交任务
	task := &mockTask{
		id:       "test-cancel",
		priority: model.TaskPriorityNormal,
		sourceID: model.SourceNetwork,
	}

	err := taskCtx.Submit(task)
	// Submit 不应该直接失败，因为 closed 标志还没设置
	// 只有 GracefulShutdown 才会设置 closed 标志
	assert.NoError(t, err)
}

// TestTaskRunnerContext_NilExecutor 测试空 executor
func TestTaskRunnerContext_NilExecutor(t *testing.T) {
	// NewTaskRunnerContext 允许 nil executor（调用方负责保证不为 nil）
	taskCtx := NewTaskRunnerContext(context.Background(), nil)
	assert.NotNil(t, taskCtx)

	// 但 Submit 会 panic（nil pointer dereference）
	task := &mockTask{
		id:       "test-nil",
		priority: model.TaskPriorityNormal,
		sourceID: model.SourceNetwork,
	}

	// Submit 应该 panic（nil executor）
	assert.Panics(t, func() {
		_ = taskCtx.Submit(task)
	})
}
