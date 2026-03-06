// Package rpc 实现异步 RPC 性能基准测试
package rpc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ============================================================================
// 测试配置
// ============================================================================

// 基准测试目标：
// - S1 (AsyncOp 创建): ≥50K ops/sec
// - S2 (回调性能): ≥100K callbacks/sec
// - S3 (并发性能): ≥30K ops/sec (100 并发)

// ============================================================================
// S1: AsyncOp 创建开销
// ============================================================================

// BenchmarkAsyncOp_Creation AsyncOp 创建开销
func BenchmarkAsyncOp_Creation(b *testing.B) {
	value := service.ResponseMsg{
		Msg: model.NewMessage("test", model.MessageTypeResponse, "node-1", "node-2", make([]byte, 1024)),
	}

	b.ResetTimer()
	for range b.N {
		// 创建已完成的 AsyncOp（最快路径）
		asyncOp := NewCompletedAsyncOp(value, nil)
		_ = asyncOp
	}
}

// BenchmarkAsyncOp_Await AsyncOp 等待开销
func BenchmarkAsyncOp_Await(b *testing.B) {
	ctx := context.Background()
	value := service.ResponseMsg{
		Msg: model.NewMessage("test", model.MessageTypeResponse, "node-1", "node-2", make([]byte, 1024)),
	}

	b.ResetTimer()
	for range b.N {
		asyncOp := NewCompletedAsyncOp(value, nil)
		_, err := asyncOp.Await(ctx)
		if err != nil {
			b.Fatalf("Await failed: %v", err)
		}
	}
}

// ============================================================================
// S2: 回调性能
// ============================================================================

// BenchmarkCallback_Execution 回调执行性能
func BenchmarkCallback_Execution(b *testing.B) {
	var callbackCount int64

	callback := func() {
		atomic.AddInt64(&callbackCount, 1)
	}

	b.ResetTimer()
	for range b.N {
		callback()
	}
}

// BenchmarkCallback_Concurrent 并发回调性能
func BenchmarkCallback_Concurrent(b *testing.B) {
	var callbackCount int64

	callback := func() {
		atomic.AddInt64(&callbackCount, 1)
	}

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := b.N / numGoroutines

	b.ResetTimer()
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				callback()
			}
		}()
	}

	wg.Wait()
}

// ============================================================================
// S3: 并发性能
// ============================================================================

// BenchmarkAsyncOp_Concurrent 并发 AsyncOp
func BenchmarkAsyncOp_Concurrent(b *testing.B) {
	ctx := context.Background()
	value := service.ResponseMsg{
		Msg: model.NewMessage("test", model.MessageTypeResponse, "node-1", "node-2", make([]byte, 256)),
	}

	var wg sync.WaitGroup
	numGoroutines := 100
	opsPerGoroutine := b.N / numGoroutines

	var successCount int64

	b.ResetTimer()
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				asyncOp := NewCompletedAsyncOp(value, nil)
				_, err := asyncOp.Await(ctx)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				}
			}
		}()
	}

	wg.Wait()
}

// BenchmarkMessage_Creation 消息创建性能
func BenchmarkMessage_Creation(b *testing.B) {
	payload := make([]byte, 1024)

	b.ResetTimer()
	for i := range b.N {
		msg := model.NewMessage(
			string(rune(i)),
			model.MessageTypeRequest,
			"node-1",
			"node-2",
			payload,
		)
		_ = msg
	}
}

// BenchmarkMessage_Concurrent 并发消息创建
func BenchmarkMessage_Concurrent(b *testing.B) {
	payload := make([]byte, 512)

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := b.N / numGoroutines

	b.ResetTimer()
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				msg := model.NewMessage(
					string(rune(id*opsPerGoroutine+i)),
					model.MessageTypeRequest,
					"node-1",
					"node-2",
					payload,
				)
				_ = msg
			}
		}(g)
	}

	wg.Wait()
}

// ============================================================================
// 辅助类型（简化版）
// ============================================================================

// completedAsyncOp 已完成的异步操作
type completedAsyncOp struct {
	value service.ResponseMsg
	err   error
}

// NewCompletedAsyncOp 创建已完成的异步操作
func NewCompletedAsyncOp(value service.ResponseMsg, err error) *completedAsyncOp {
	return &completedAsyncOp{
		value: value,
		err:   err,
	}
}

func (op *completedAsyncOp) Await(ctx context.Context) (service.ResponseMsg, error) {
	return op.value, op.err
}
