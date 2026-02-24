// Package rpc async_impl.go 补充测试
package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	serviceerrors "github.com/jzhang405/NexKV/pkg/errors"
)

// ==========================================
// asyncOpImpl 状态方法测试
// ==========================================

func TestAsyncOpImpl_StatusMethods(t *testing.T) {
	t.Run("initial status", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			status: atomic.Int32{},
			done:   atomic.Bool{},
		}

		if op.Status() != 0 { // StatusPending = 0
			t.Fatalf("expected pending status, got %d", op.Status())
		}
		if op.IsDone() {
			t.Fatal("should not be done initially")
		}
		if op.IsSuccess() {
			t.Fatal("should not be success initially")
		}
		if op.IsFailed() {
			t.Fatal("should not be failed initially")
		}
		if op.IsCanceled() {
			t.Fatal("should not be canceled initially")
		}
	})

	t.Run("completed status", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			status: atomic.Int32{},
			done:   atomic.Bool{},
			errCh:  make(chan error, 1),
		}
		op.status.Store(2) // StatusCompleted = 2
		op.done.Store(true)

		if !op.IsDone() {
			t.Fatal("should be done")
		}
		if !op.IsSuccess() {
			t.Fatal("should be success")
		}
		if op.IsFailed() {
			t.Fatal("should not be failed")
		}
	})

	t.Run("failed status", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			status: atomic.Int32{},
			done:   atomic.Bool{},
			errCh:  make(chan error, 1),
		}
		op.status.Store(3) // StatusFailed = 3
		op.done.Store(true)

		if !op.IsDone() {
			t.Fatal("should be done")
		}
		if op.IsSuccess() {
			t.Fatal("should not be success")
		}
		if !op.IsFailed() {
			t.Fatal("should be failed")
		}
	})

	t.Run("canceled status", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			status: atomic.Int32{},
			done:   atomic.Bool{},
			errCh:  make(chan error, 1),
		}
		op.status.Store(4) // StatusCanceled = 4
		op.done.Store(true)

		if !op.IsDone() {
			t.Fatal("should be done")
		}
		if !op.IsCanceled() {
			t.Fatal("should be canceled")
		}
	})

	t.Run("all status values", func(t *testing.T) {
		op := &asyncOpImpl[string]{}

		statuses := []struct {
			value    int32
			expected service.OperationStatus
		}{
			{0, service.StatusPending},
			{1, service.StatusRunning},
			{2, service.StatusCompleted},
			{3, service.StatusFailed},
			{4, service.StatusCanceled},
			{5, service.StatusDiscarded},
			{6, service.StatusTimeout},
			{99, service.StatusPending}, // Unknown -> Pending
		}

		for _, s := range statuses {
			op.status.Store(s.value)
			result := op.Status()
			if result != s.expected {
				t.Fatalf("status %d: expected %d, got %d", s.value, s.expected, result)
			}
		}
	})
}

// ==========================================
// asyncOpImpl Get 方法测试
// ==========================================

func TestAsyncOpImpl_Get(t *testing.T) {
	t.Run("get delegates to await", func(t *testing.T) {
		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)
		resultCh <- "test-result"

		op := &asyncOpImpl[string]{
			resultCh: resultCh,
			errCh:    errCh,
			done:     atomic.Bool{},
		}
		op.done.Store(true)

		ctx := context.Background()
		result, err := op.Get(ctx)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "test-result" {
			t.Fatalf("expected 'test-result', got '%s'", result)
		}
	})
}

// ==========================================
// asyncOpImpl Cancel 方法测试
// ==========================================

func TestAsyncOpImpl_Cancel(t *testing.T) {
	t.Run("cancel success", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			done:      atomic.Bool{},
			status:    atomic.Int32{},
			errCh:     make(chan error, 1),
			callbacks: make(map[string]func(string, error)),
		}

		ok, err := op.Cancel()
		if !ok {
			t.Fatal("cancel should return true")
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !op.IsCanceled() {
			t.Fatal("should be canceled")
		}
	})

	t.Run("cancel already done", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			done:   atomic.Bool{},
			status: atomic.Int32{},
			errCh:  make(chan error, 1),
		}
		op.done.Store(true)

		ok, err := op.Cancel()
		if ok {
			t.Fatal("cancel should return false for already done")
		}
		if !errors.Is(err, serviceerrors.ErrOperationAlreadyCompleted) {
			t.Fatalf("expected ErrOperationAlreadyCompleted, got %v", err)
		}
	})

	t.Run("cancel triggers callback", func(t *testing.T) {
		called := atomic.Bool{}
		op := &asyncOpImpl[string]{
			done:      atomic.Bool{},
			status:    atomic.Int32{},
			errCh:     make(chan error, 1),
			callbacks: make(map[string]func(string, error)),
		}

		op.OnComplete(func(result string, err error) {
			if errors.Is(err, serviceerrors.ErrOperationCanceled) {
				called.Store(true)
			}
		})

		_, _ = op.Cancel()

		// 等待回调执行
		time.Sleep(50 * time.Millisecond)

		if !called.Load() {
			t.Fatal("cancel callback should be called")
		}
	})
}

// ==========================================
// asyncOpImpl Discard 方法测试
// ==========================================

func TestAsyncOpImpl_Discard(t *testing.T) {
	t.Run("discard delegates to cancel", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			done:      atomic.Bool{},
			status:    atomic.Int32{},
			errCh:     make(chan error, 1),
			callbacks: make(map[string]func(string, error)),
		}

		err := op.Discard()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !op.IsCanceled() {
			t.Fatal("should be canceled after discard")
		}
	})
}

// ==========================================
// asyncOpImpl IsStarted 方法测试
// ==========================================

func TestAsyncOpImpl_IsStarted(t *testing.T) {
	op := &asyncOpImpl[string]{}
	if !op.IsStarted() {
		t.Fatal("IsStarted should always return true")
	}
}

// ==========================================
// asyncOpImpl OffComplete 方法测试
// ==========================================

func TestAsyncOpImpl_OffComplete(t *testing.T) {
	t.Run("off complete success", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			callbacks: make(map[string]func(string, error)),
			cbMu:      sync.RWMutex{},
		}

		cbID := op.OnComplete(func(string, error) {})
		err := op.OffComplete(cbID)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 验证已删除
		op.cbMu.RLock()
		_, exists := op.callbacks[cbID]
		op.cbMu.RUnlock()

		if exists {
			t.Fatal("callback should be removed")
		}
	})

	t.Run("off complete empty id", func(t *testing.T) {
		op := &asyncOpImpl[string]{}

		err := op.OffComplete("")
		if !errors.Is(err, serviceerrors.ErrCallbackIDEmpty) {
			t.Fatalf("expected ErrCallbackIDEmpty, got %v", err)
		}
	})

	t.Run("off complete not found", func(t *testing.T) {
		op := &asyncOpImpl[string]{
			callbacks: make(map[string]func(string, error)),
		}

		err := op.OffComplete("non-existent")
		if !errors.Is(err, serviceerrors.ErrCallbackNotFound) {
			t.Fatalf("expected ErrCallbackNotFound, got %v", err)
		}
	})
}

// ==========================================
// timeoutAsyncOp 测试
// ==========================================

func TestTimeoutAsyncOp(t *testing.T) {
	t.Run("await with timeout", func(t *testing.T) {
		inner := &asyncOpImpl[string]{
			resultCh: make(chan string, 1),
			errCh:    make(chan error, 1),
			done:     atomic.Bool{},
		}
		inner.done.Store(true)
		inner.resultCh <- "result"

		timeoutOp := &timeoutAsyncOp[string]{
			inner:   inner,
			timeout: 100 * time.Millisecond,
		}

		ctx := context.Background()
		result, err := timeoutOp.Await(ctx)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "result" {
			t.Fatalf("expected 'result', got '%s'", result)
		}
	})

	t.Run("timeoutAsyncOp delegates", func(t *testing.T) {
		inner := &asyncOpImpl[string]{
			resultCh:  make(chan string, 1),
			errCh:     make(chan error, 1),
			done:      atomic.Bool{},
			status:    atomic.Int32{},
			callbacks: make(map[string]func(string, error)),
		}
		inner.done.Store(true)
		inner.status.Store(2) // Completed

		timeoutOp := &timeoutAsyncOp[string]{
			inner:   inner,
			timeout: time.Second,
		}

		// 测试委托方法
		if !timeoutOp.IsDone() {
			t.Fatal("IsDone should delegate to inner")
		}
		if !timeoutOp.IsSuccess() {
			t.Fatal("IsSuccess should delegate to inner")
		}
		if timeoutOp.IsFailed() {
			t.Fatal("IsFailed should delegate to inner")
		}
		if timeoutOp.IsCanceled() {
			t.Fatal("IsCanceled should delegate to inner")
		}

		// 测试 Get
		inner.resultCh <- "test"
		result, _ := timeoutOp.Get(context.Background())
		if result != "test" {
			t.Fatal("Get should delegate to Await")
		}
	})

	t.Run("timeoutAsyncOp Status", func(t *testing.T) {
		inner := &asyncOpImpl[string]{
			done:   atomic.Bool{},
			status: atomic.Int32{},
		}

		timeoutOp := &timeoutAsyncOp[string]{
			inner:   inner,
			timeout: time.Second,
		}

		// Pending
		if timeoutOp.Status() != 0 {
			t.Fatalf("expected pending, got %d", timeoutOp.Status())
		}

		// Completed
		inner.done.Store(true)
		inner.status.Store(2)
		if timeoutOp.Status() != 2 {
			t.Fatalf("expected completed, got %d", timeoutOp.Status())
		}

		// Failed
		inner.status.Store(3)
		if timeoutOp.Status() != 3 {
			t.Fatalf("expected failed, got %d", timeoutOp.Status())
		}

		// Canceled
		inner.status.Store(4)
		if timeoutOp.Status() != 4 {
			t.Fatalf("expected canceled, got %d", timeoutOp.Status())
		}
	})

	t.Run("timeoutAsyncOp Cancel", func(t *testing.T) {
		inner := &asyncOpImpl[string]{
			done:      atomic.Bool{},
			status:    atomic.Int32{},
			errCh:     make(chan error, 1),
			callbacks: make(map[string]func(string, error)),
		}

		timeoutOp := &timeoutAsyncOp[string]{
			inner:   inner,
			timeout: time.Second,
		}

		ok, err := timeoutOp.Cancel()
		if !ok {
			t.Fatal("cancel should succeed")
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("timeoutAsyncOp Discard", func(t *testing.T) {
		inner := &asyncOpImpl[string]{
			done:      atomic.Bool{},
			status:    atomic.Int32{},
			errCh:     make(chan error, 1),
			callbacks: make(map[string]func(string, error)),
		}

		timeoutOp := &timeoutAsyncOp[string]{
			inner:   inner,
			timeout: time.Second,
		}

		err := timeoutOp.Discard()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("timeoutAsyncOp IsStarted", func(t *testing.T) {
		inner := &asyncOpImpl[string]{}
		timeoutOp := &timeoutAsyncOp[string]{inner: inner}

		if !timeoutOp.IsStarted() {
			t.Fatal("IsStarted should return true")
		}
	})

	t.Run("timeoutAsyncOp callbacks", func(t *testing.T) {
		inner := &asyncOpImpl[string]{
			callbacks: make(map[string]func(string, error)),
		}

		timeoutOp := &timeoutAsyncOp[string]{inner: inner}

		// OnComplete
		cbID := timeoutOp.OnComplete(func(string, error) {})
		if cbID == "" {
			t.Fatal("OnComplete should return callback ID")
		}

		// OffComplete
		err := timeoutOp.OffComplete(cbID)
		if err != nil {
			t.Fatalf("OffComplete failed: %v", err)
		}

		// OnError
		errID := timeoutOp.OnError(func(error) {})
		if errID == "" {
			t.Fatal("OnError should return callback ID")
		}
	})

	t.Run("timeoutAsyncOp WithTimeout", func(t *testing.T) {
		inner := &asyncOpImpl[string]{}
		timeoutOp := &timeoutAsyncOp[string]{
			inner:   inner,
			timeout: 100 * time.Millisecond,
		}

		// 更短的超时
		newOp := timeoutOp.WithTimeout(50 * time.Millisecond)
		top, ok := newOp.(*timeoutAsyncOp[string])
		if !ok {
			t.Fatal("WithTimeout should return *timeoutAsyncOp")
		}
		if top.timeout != 50*time.Millisecond {
			t.Fatalf("expected 50ms timeout, got %v", top.timeout)
		}

		// 更长的超时（应该保持原值）
		newOp2 := timeoutOp.WithTimeout(200 * time.Millisecond)
		top2 := newOp2.(*timeoutAsyncOp[string])
		if top2.timeout != 100*time.Millisecond {
			t.Fatalf("expected 100ms timeout, got %v", top2.timeout)
		}
	})
}
