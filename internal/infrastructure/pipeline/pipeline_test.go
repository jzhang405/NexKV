package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// 测试 Stage 实现
// ==========================================

// mockStage 模拟 Stage
type mockStage[T any] struct {
	name      string
	delay     time.Duration
	fail      bool
	processed atomic.Int64
}

func newMockStage[T any](name string, delay time.Duration) *mockStage[T] {
	return &mockStage[T]{
		name:  name,
		delay: delay,
	}
}

func (m *mockStage[T]) Name() string {
	return m.name
}

func (m *mockStage[T]) Process(ctx context.Context, item T) (T, error) {
	m.processed.Add(1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.fail {
		var zero T
		return zero, errors.New("mock stage error")
	}
	return item, nil
}

func (m *mockStage[T]) OnError(ctx context.Context, item T, err error) (T, error) {
	var zero T
	return zero, err
}

// ==========================================
// 基础功能测试
// ==========================================

func TestPipeline_Build_NoStages(t *testing.T) {
	p := NewPipeline[any]()
	_, err := p.Build()

	assert.Error(t, err)
	assert.Equal(t, ErrNoStages, err)
}

func TestPipeline_Build_Success(t *testing.T) {
	p := NewPipeline[any]().
		AddStage(newMockStage[any]("stage1", 0)).
		AddStage(newMockStage[any]("stage2", 0))

	running, err := p.Build()
	require.NoError(t, err)
	require.NotNil(t, running)

	defer running.Close()
}

func TestPipeline_Submit_Success(t *testing.T) {
	p := NewPipeline[int]().
		AddStage(newMockStage[int]("stage1", 0))

	running, err := p.Build()
	require.NoError(t, err)
	defer running.Close()

	ctx := context.Background()
	err = running.Submit(ctx, 42)
	assert.NoError(t, err)
}

func TestPipeline_SubmitWithResult_Success(t *testing.T) {
	stage1 := newMockStage[int]("stage1", 0)
	stage2 := newMockStage[int]("stage2", 0)

	p := NewPipeline[int]().
		AddStage(stage1).
		AddStage(stage2)

	running, err := p.Build()
	require.NoError(t, err)
	defer running.Close()

	ctx := context.Background()
	result, err := running.SubmitWithResult(ctx, 42)

	assert.NoError(t, err)
	assert.Equal(t, 42, result)
	assert.Equal(t, int64(1), stage1.processed.Load())
	assert.Equal(t, int64(1), stage2.processed.Load())
}

func TestPipeline_Close(t *testing.T) {
	p := NewPipeline[int]().
		AddStage(newMockStage[int]("stage1", 0))

	running, err := p.Build()
	require.NoError(t, err)

	err = running.Close()
	assert.NoError(t, err)
}

func TestPipeline_Stats(t *testing.T) {
	p := NewPipeline[int]().
		AddStage(newMockStage[int]("stage1", 0))

	running, err := p.Build()
	require.NoError(t, err)
	defer running.Close()

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_, _ = running.SubmitWithResult(ctx, i)
	}

	stats := running.Stats()
	assert.NotNil(t, stats)
	assert.Contains(t, stats.Stages, "stage1")
	assert.Equal(t, int64(10), stats.Stages["stage1"].Processed)
}

// ==========================================
// 错误处理测试
// ==========================================

func TestPipeline_StageError_Terminates(t *testing.T) {
	stage1 := newMockStage[int]("stage1", 0)
	stage2 := &mockStage[int]{name: "stage2", fail: true} // 会失败
	stage3 := newMockStage[int]("stage3", 0)

	p := NewPipeline[int]().
		AddStage(stage1).
		AddStage(stage2).
		AddStage(stage3)

	running, err := p.Build()
	require.NoError(t, err)
	defer running.Close()

	ctx := context.Background()
	_, err = running.SubmitWithResult(ctx, 42)

	assert.Error(t, err)
	assert.Equal(t, int64(1), stage1.processed.Load())
	assert.Equal(t, int64(1), stage2.processed.Load())
	assert.Equal(t, int64(0), stage3.processed.Load()) // stage3 不会执行
}

// ==========================================
// 基准测试
// ==========================================

func BenchmarkPipeline_SingleStage(b *testing.B) {
	p := NewPipeline[int]().
		AddStage(newMockStage[int]("stage1", 0))

	running, err := p.Build()
	require.NoError(b, err)
	defer running.Close()

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = running.Submit(ctx, i)
	}
}

func BenchmarkPipeline_TwoStages(b *testing.B) {
	p := NewPipeline[int]().
		AddStage(newMockStage[int]("stage1", 0)).
		AddStage(newMockStage[int]("stage2", 0))

	running, err := p.Build()
	require.NoError(b, err)
	defer running.Close()

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = running.Submit(ctx, i)
	}
}

func BenchmarkPipeline_SubmitWithResult(b *testing.B) {
	p := NewPipeline[int]().
		AddStage(newMockStage[int]("stage1", 0))

	running, err := p.Build()
	require.NoError(b, err)
	defer running.Close()

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = running.SubmitWithResult(ctx, i)
	}
}

// ==========================================
// 背压测试
// ==========================================

func TestPipeline_Submit_Backpressure(t *testing.T) {
	p := NewPipeline[int]().
		WithConfig(&Config{
			QueueSize:          100,
			EnableBackpressure: true,
			MaxQueueLength:     90,
		}).
		AddStage(newMockStage[int]("normal", 0))

	running, err := p.Build()
	require.NoError(t, err)
	defer running.Close()

	ctx := context.Background()

	// 快速提交多个任务，填满队列
	for i := 0; i < 100; i++ {
		_ = running.Submit(ctx, i)
	}

	// 等待让任务被消费
	time.Sleep(10 * time.Millisecond)

	// 现在提交新任务，应该不会触发背压（因为队列长度下降了）
	err = running.Submit(ctx, 999)
	assert.NoError(t, err)
}

// ==========================================
// 覆盖率测试
// ==========================================

func TestPipeline_Submit_WorkerSubmitMethod(t *testing.T) {
	// 测试 worker.Submit 方法（之前没有被使用）
	p := NewPipeline[int]().
		AddStage(newMockStage[int]("stage1", 0))

	running, err := p.Build()
	require.NoError(t, err)
	defer running.Close()

	ctx := context.Background()

	// 直接使用 worker.Submit 方法
	err = running.workers[0].Submit(ctx, 42)
	assert.NoError(t, err)
}

func TestPipeline_Options(t *testing.T) {
	// 测试配置 Option 函数
	p := NewPipeline[int]().
		WithConfig(&Config{
			QueueSize: 100,
		}).
		AddStage(newMockStage[int]("stage1", 0))

	running, err := p.Build()
	require.NoError(t, err)
	defer running.Close()
	assert.NotNil(t, running)
}

func TestPipeline_ProcessErrorPath(t *testing.T) {
	// 测试 Process 返回错误的情况
	errorStage := &mockStage[int]{name: "errorStage", fail: true}

	p := NewPipeline[int]().
		AddStage(newMockStage[int]("stage1", 0)).
		AddStage(errorStage)

	running, err := p.Build()
	require.NoError(t, err)
	defer running.Close()

	ctx := context.Background()
	_, err = running.SubmitWithResult(ctx, 42)

	assert.Error(t, err)
}
