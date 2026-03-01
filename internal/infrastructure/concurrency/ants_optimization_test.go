package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ==========================================
// 方案对比测试
// ==========================================

// 原始实现 (RWMutex)
type AntsDefaultV1 struct {
	mu     sync.RWMutex
	closed bool
}

func (e *AntsDefaultV1) Submit(ctx context.Context, task func(context.Context)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	e.mu.RLock()
	closed := e.closed
	e.mu.RUnlock()
	if closed {
		return ErrExecutorClosed
	}
	// 模拟 ants.Submit
	go task(ctx)
	return nil
}

func (e *AntsDefaultV1) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	return nil
}

// 优化方案 1: atomic.Bool
type AntsDefaultV2 struct {
	closed atomic.Bool
}

func (e *AntsDefaultV2) Submit(ctx context.Context, task func(context.Context)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e.closed.Load() {
		return ErrExecutorClosed
	}
	// 模拟 ants.Submit
	go task(ctx)
	return nil
}

func (e *AntsDefaultV2) Close() error {
	e.closed.Store(true)
	return nil
}

// 优化方案 2: atomic.Bool + 提前返回
type AntsDefaultV3 struct {
	closed atomic.Bool
}

func (e *AntsDefaultV3) Submit(ctx context.Context, task func(context.Context)) error {
	// 快速失败路径
	if e.closed.Load() || ctx.Err() != nil {
		if e.closed.Load() {
			return ErrExecutorClosed
		}
		return ctx.Err()
	}
	// 模拟 ants.Submit
	go task(ctx)
	return nil
}

func (e *AntsDefaultV3) Close() error {
	e.closed.Store(true)
	return nil
}

// ==========================================
// 基准测试
// ==========================================

func BenchmarkAntsV1_RWMutex(b *testing.B) {
	exec := &AntsDefaultV1{}
	ctx := context.Background()
	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exec.Submit(ctx, task)
	}
}

func BenchmarkAntsV2_AtomicBool(b *testing.B) {
	exec := &AntsDefaultV2{}
	ctx := context.Background()
	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exec.Submit(ctx, task)
	}
}

func BenchmarkAntsV3_AtomicBoolOptimized(b *testing.B) {
	exec := &AntsDefaultV3{}
	ctx := context.Background()
	task := func(ctx context.Context) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exec.Submit(ctx, task)
	}
}

// 并发测试
func BenchmarkAntsV1_RWMutex_Parallel(b *testing.B) {
	exec := &AntsDefaultV1{}
	ctx := context.Background()
	task := func(ctx context.Context) {}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = exec.Submit(ctx, task)
		}
	})
}

func BenchmarkAntsV2_AtomicBool_Parallel(b *testing.B) {
	exec := &AntsDefaultV2{}
	ctx := context.Background()
	task := func(ctx context.Context) {}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = exec.Submit(ctx, task)
		}
	})
}

func BenchmarkAntsV3_AtomicBoolOptimized_Parallel(b *testing.B) {
	exec := &AntsDefaultV3{}
	ctx := context.Background()
	task := func(ctx context.Context) {}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = exec.Submit(ctx, task)
		}
	})
}

// ==========================================
// 正确性测试
// ==========================================

func TestAntsOptimization_Correctness(t *testing.T) {
	ctx := context.Background()
	task := func(ctx context.Context) {}

	// 测试 V1
	v1 := &AntsDefaultV1{}
	err := v1.Submit(ctx, task)
	assert.NoError(t, err)

	// 测试 V2
	v2 := &AntsDefaultV2{}
	err = v2.Submit(ctx, task)
	assert.NoError(t, err)

	// 测试 V3
	v3 := &AntsDefaultV3{}
	err = v3.Submit(ctx, task)
	assert.NoError(t, err)

	// 测试 Close 后拒绝任务
	_ = v1.Close()
	_ = v2.Close()
	_ = v3.Close()

	err = v1.Submit(ctx, task)
	assert.Error(t, err)

	err = v2.Submit(ctx, task)
	assert.Error(t, err)

	err = v3.Submit(ctx, task)
	assert.Error(t, err)
}
