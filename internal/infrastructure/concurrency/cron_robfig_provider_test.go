// Package concurrency 提供协程池和定时任务管理
package concurrency

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRobfigCronProvider_Register(t *testing.T) {
	gp, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create goroutine provider: %v", err)
	}
	defer gp.Close()

	cp := NewRobfigCronProvider(gp)
	cp.Start()
	defer cp.Stop()

	var executed int32

	jobID, err := cp.Register("*/1 * * * * *", "test-job", func(ctx context.Context) {
		atomic.AddInt32(&executed, 1)
	})
	if err != nil {
		t.Fatalf("failed to register job: %v", err)
	}

	if jobID == "" {
		t.Error("expected non-empty job ID")
	}

	// 等待至少执行一次
	time.Sleep(2 * time.Second)

	if atomic.LoadInt32(&executed) < 1 {
		t.Errorf("expected at least 1 execution, got %d", atomic.LoadInt32(&executed))
	}
}

func TestRobfigCronProvider_RegisterWithArg(t *testing.T) {
	gp, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create goroutine provider: %v", err)
	}
	defer gp.Close()

	cp := NewRobfigCronProvider(gp)
	cp.Start()
	defer cp.Stop()

	var result atomic.Value

	jobID, err := cp.RegisterWithArg("*/1 * * * * *", "test-job-arg", func(ctx context.Context, arg any) {
		result.Store(arg.(string))
	}, "hello")
	if err != nil {
		t.Fatalf("failed to register job: %v", err)
	}

	if jobID == "" {
		t.Error("expected non-empty job ID")
	}

	// 等待至少执行一次
	time.Sleep(2 * time.Second)

	val := result.Load()
	if val == nil || val.(string) != "hello" {
		t.Errorf("expected result 'hello', got '%v'", val)
	}
}

func TestRobfigCronProvider_RegisterWithArg_Generic(t *testing.T) {
	gp, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create goroutine provider: %v", err)
	}
	defer gp.Close()

	cp := NewRobfigCronProvider(gp)
	cp.Start()
	defer cp.Stop()

	var result int32

	jobID, err := RegisterWithArg(cp, "*/1 * * * * *", "test-job-generic", func(ctx context.Context, arg int) {
		atomic.StoreInt32(&result, int32(arg))
	}, 42)
	if err != nil {
		t.Fatalf("failed to register job: %v", err)
	}

	if jobID == "" {
		t.Error("expected non-empty job ID")
	}

	// 等待至少执行一次
	time.Sleep(2 * time.Second)

	if atomic.LoadInt32(&result) != 42 {
		t.Errorf("expected result 42, got %d", atomic.LoadInt32(&result))
	}
}

func TestRobfigCronProvider_PauseResume(t *testing.T) {
	gp, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create goroutine provider: %v", err)
	}
	defer gp.Close()

	cp := NewRobfigCronProvider(gp)
	cp.Start()
	defer cp.Stop()

	var executed int32

	jobID, _ := cp.Register("*/1 * * * * *", "pause-test", func(ctx context.Context) {
		atomic.AddInt32(&executed, 1)
	})

	// 等待执行
	time.Sleep(1500 * time.Millisecond)
	countBeforePause := atomic.LoadInt32(&executed)

	// 暂停
	err = cp.Pause(jobID)
	if err != nil {
		t.Fatalf("failed to pause job: %v", err)
	}

	// 验证状态
	info, _ := cp.GetJob(jobID)
	if info.Status != CronJobStatusPaused {
		t.Errorf("expected status paused, got %v", info.Status)
	}

	// 等待，应该不再执行
	time.Sleep(1500 * time.Millisecond)
	countAfterPause := atomic.LoadInt32(&executed)

	// 恢复
	err = cp.Resume(jobID)
	if err != nil {
		t.Fatalf("failed to resume job: %v", err)
	}

	// 等待执行
	time.Sleep(1500 * time.Millisecond)
	countAfterResume := atomic.LoadInt32(&executed)

	// 验证：恢复后应该继续执行
	if countAfterPause >= countAfterResume {
		t.Errorf("expected more executions after resume: before=%d, after_pause=%d, after_resume=%d",
			countBeforePause, countAfterPause, countAfterResume)
	}
}

func TestRobfigCronProvider_Unregister(t *testing.T) {
	gp, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create goroutine provider: %v", err)
	}
	defer gp.Close()

	cp := NewRobfigCronProvider(gp)
	cp.Start()
	defer cp.Stop()

	jobID, _ := cp.Register("*/1 * * * * *", "unregister-test", func(ctx context.Context) {})

	// 取消注册
	err = cp.Unregister(jobID)
	if err != nil {
		t.Fatalf("failed to unregister job: %v", err)
	}

	// 验证任务已删除
	_, err = cp.GetJob(jobID)
	if err == nil {
		t.Error("expected error for unregistered job")
	}
}

func TestRobfigCronProvider_GetJob(t *testing.T) {
	gp, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create goroutine provider: %v", err)
	}
	defer gp.Close()

	cp := NewRobfigCronProvider(gp)
	cp.Start()
	defer cp.Stop()

	jobID, _ := cp.Register("*/1 * * * * *", "getjob-test", func(ctx context.Context) {})

	info, err := cp.GetJob(jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}

	if info.ID != jobID {
		t.Errorf("expected ID %s, got %s", jobID, info.ID)
	}
	if info.Name != "getjob-test" {
		t.Errorf("expected name 'getjob-test', got '%s'", info.Name)
	}
}

func TestRobfigCronProvider_ListJobs(t *testing.T) {
	gp, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create goroutine provider: %v", err)
	}
	defer gp.Close()

	cp := NewRobfigCronProvider(gp)
	cp.Start()
	defer cp.Stop()

	// 注册多个任务
	_, _ = cp.Register("*/1 * * * * *", "job1", func(ctx context.Context) {})
	_, _ = cp.Register("*/2 * * * * *", "job2", func(ctx context.Context) {})
	_, _ = cp.Register("*/3 * * * * *", "job3", func(ctx context.Context) {})

	jobs := cp.ListJobs()
	if len(jobs) != 3 {
		t.Errorf("expected 3 jobs, got %d", len(jobs))
	}
}

func TestRobfigCronProvider_DuplicateName(t *testing.T) {
	gp, err := NewAntsGoroutineProvider(nil)
	if err != nil {
		t.Fatalf("failed to create goroutine provider: %v", err)
	}
	defer gp.Close()

	cp := NewRobfigCronProvider(gp)
	cp.Start()
	defer cp.Stop()

	// 注册第一个
	_, err = cp.Register("*/1 * * * * *", "duplicate", func(ctx context.Context) {})
	if err != nil {
		t.Fatalf("failed to register first job: %v", err)
	}

	// 尝试注册同名任务
	_, err = cp.Register("*/1 * * * * *", "duplicate", func(ctx context.Context) {})
	if err == nil {
		t.Error("expected error for duplicate name")
	}
}
