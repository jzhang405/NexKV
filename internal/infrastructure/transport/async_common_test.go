package transport

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
)

// TestNonBlockingSendResult_Success 测试成功发送
func TestNonBlockingSendResult_Success(t *testing.T) {
	ch := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := NonBlockingSendResult(ch, 42, "test", ctx.Done())
	if !result {
		t.Error("NonBlockingSendResult() = false, want true")
	}

	select {
	case v := <-ch:
		if v != 42 {
			t.Errorf("received value = %d, want 42", v)
		}
	default:
		t.Error("channel should have value")
	}
}

// TestNonBlockingSendResult_ChannelFull 测试 channel 满的情况
func TestNonBlockingSendResult_ChannelFull(t *testing.T) {
	ch := make(chan int, 1)
	ch <- 100 // 填满 channel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := NonBlockingSendResult(ch, 42, "test", ctx.Done())
	if result {
		t.Error("NonBlockingSendResult() = true when channel full, want false")
	}
}

// TestNonBlockingSendResult_ContextCanceled 测试上下文取消
func TestNonBlockingSendResult_ContextCanceled(t *testing.T) {
	ch := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	// 上下文取消时，应该返回 false（因为 select 优先检查 ctx.Done）
	// 但如果 channel 有空间且上下文刚好取消，行为取决于 select 的随机选择
	result := NonBlockingSendResult(ch, 42, "test", ctx.Done())
	// 由于 select 行为不确定，我们只验证不会 panic
	_ = result
}

// TestValidateMessageSize_Success 测试消息大小验证通过
func TestValidateMessageSize_Success(t *testing.T) {
	data := []byte("hello world")
	err := ValidateMessageSize(data, 100, "test")
	if err != nil {
		t.Errorf("ValidateMessageSize() error = %v, want nil", err)
	}
}

// TestValidateMessageSize_ExceedsLimit 测试消息超过限制
func TestValidateMessageSize_ExceedsLimit(t *testing.T) {
	data := []byte("hello world")
	err := ValidateMessageSize(data, 5, "test")
	if err != errors.ErrMessageTooLarge {
		t.Errorf("ValidateMessageSize() error = %v, want ErrMessageTooLarge", err)
	}
}

// TestValidateMessageSize_NoLimit 测试无限制
func TestValidateMessageSize_NoLimit(t *testing.T) {
	data := make([]byte, 10000)
	err := ValidateMessageSize(data, 0, "test")
	if err != nil {
		t.Errorf("ValidateMessageSize() with no limit error = %v, want nil", err)
	}
}

// TestDrainWriteQueue_Empty 测试空队列
func TestDrainWriteQueue_Empty(t *testing.T) {
	ch := make(chan service.WriteRequest, 1)
	close(ch)

	count := DrainWriteQueue(ch, func(req service.WriteRequest) {})
	if count != 0 {
		t.Errorf("DrainWriteQueue() count = %d, want 0", count)
	}
}

// TestDrainWriteQueue_WithItems 测试有元素的队列
func TestDrainWriteQueue_WithItems(t *testing.T) {
	ch := make(chan service.WriteRequest, 3)
	ch <- service.WriteRequest{Data: []byte("msg1")}
	ch <- service.WriteRequest{Data: []byte("msg2")}
	ch <- service.WriteRequest{Data: []byte("msg3")}
	close(ch)

	var items []string
	count := DrainWriteQueue(ch, func(req service.WriteRequest) {
		items = append(items, string(req.Data))
	})

	if count != 3 {
		t.Errorf("DrainWriteQueue() count = %d, want 3", count)
	}

	if len(items) != 3 {
		t.Errorf("items count = %d, want 3", len(items))
	}
}

// TestNonBlockingSendError_Success 测试成功发送错误
func TestNonBlockingSendError_Success(t *testing.T) {
	ch := make(chan error, 1)
	NonBlockingSendError(ch, errors.ErrTimeout, "test")

	select {
	case err := <-ch:
		if err != errors.ErrTimeout {
			t.Errorf("received error = %v, want ErrTimeout", err)
		}
	default:
		t.Error("error channel should have value")
	}
}

// TestNonBlockingSendError_NilChannel 测试空 channel
func TestNonBlockingSendError_NilChannel(t *testing.T) {
	// 不应该 panic
	NonBlockingSendError(nil, errors.ErrTimeout, "test")
}

// TestNonBlockingSendError_ChannelFull 测试满 channel
func TestNonBlockingSendError_ChannelFull(t *testing.T) {
	ch := make(chan error, 1)
	ch <- errors.ErrCanceled // 填满

	// 不应该阻塞
	NonBlockingSendError(ch, errors.ErrTimeout, "test")
}

// TestAtomicError_Store 测试 AtomicError 存储
func TestAtomicError_Store(t *testing.T) {
	var ae AtomicError

	// 存储错误
	testErr := errors.ErrTimeout
	ae.Store(&testErr)

	// 加载并验证
	loaded := ae.Load()
	if loaded != testErr {
		t.Errorf("Load() = %v, want %v", loaded, testErr)
	}

	// 存储另一个错误
	testErr2 := errors.ErrCanceled
	ae.Store(&testErr2)

	loaded = ae.Load()
	if loaded != testErr2 {
		t.Errorf("Load() after second Store = %v, want %v", loaded, testErr2)
	}
}

// TestAtomicError_StoreNil 测试存储 nil 错误
func TestAtomicError_StoreNil(t *testing.T) {
	var ae AtomicError

	// 存储 nil
	ae.Store(nil)

	// 加载应该是 nil
	loaded := ae.Load()
	if loaded != nil {
		t.Errorf("Load() after Store(nil) = %v, want nil", loaded)
	}
}
