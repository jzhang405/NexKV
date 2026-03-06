// Package rpc RPC Task 并发安全测试
package rpc

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/stretchr/testify/assert"
)

// TestRPCCallTask_NoGoroutineLeak 测试 RPCCallTask 不会有 goroutine 泄漏
func TestRPCCallTask_NoGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	var callCount int32
	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			atomic.AddInt32(&callCount, 1)
			time.Sleep(10 * time.Millisecond)
			return req, nil
		},
	}

	for i := 0; i < 100; i++ {
		req := model.NewMessage("test", model.MessageTypeRequest, "node-1", "node-2", []byte("payload"))
		task := NewRPCCallTask(mockRPC, "node-2", req, model.SourceNetwork, 1*time.Second)

		go task.Run(context.Background(), nil)
		<-task.Done()
	}

	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	leaked := finalGoroutines - initialGoroutines

	assert.LessOrEqual(t, leaked, 5, "Expected no goroutine leak, but found %d", leaked)
	assert.Equal(t, int32(100), atomic.LoadInt32(&callCount))
}

// TestRPCCallTask_HighConcurrency 测试高并发
func TestRPCCallTask_HighConcurrency(t *testing.T) {
	const taskCount = 500
	const concurrency = 50

	var completedTasks int32
	var callCount int32
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			atomic.AddInt32(&callCount, 1)
			time.Sleep(time.Millisecond)
			return req, nil
		},
	}

	for i := 0; i < taskCount; i++ {
		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			req := model.NewMessage("test", model.MessageTypeRequest, "node-1", "node-2", []byte("payload"))
			task := NewRPCCallTask(mockRPC, "node-2", req, model.SourceNetwork, 1*time.Second)

			task.Run(context.Background(), nil)
			<-task.Done()

			atomic.AddInt32(&completedTasks, 1)
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		assert.Equal(t, int32(taskCount), atomic.LoadInt32(&completedTasks))
		assert.Equal(t, int32(taskCount), atomic.LoadInt32(&callCount))
	case <-time.After(10 * time.Second):
		t.Fatalf("Timed out - only %d/%d completed", atomic.LoadInt32(&completedTasks), taskCount)
	}
}

// TestRPCBroadcastTask_ConcurrentBroadcasts 测试并发广播
func TestRPCBroadcastTask_ConcurrentBroadcasts(t *testing.T) {
	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			time.Sleep(time.Millisecond)
			return req, nil
		},
	}

	peers := []model.PeerID{"node-2", "node-3", "node-4"}
	req := model.NewMessage("test", model.MessageTypeRequest, "node-1", "", []byte("payload"))

	const broadcastCount = 100
	var completedBroadcasts int32
	var wg sync.WaitGroup

	for i := 0; i < broadcastCount; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			config := &service.RPCAsyncConfig{
				Executor:        nil,
				DefaultTimeoutMs: 1000,
			}

			task := NewRPCBroadcastTask(mockRPC, peers, req, config, nil)
			task.Run(context.Background(), nil)
			<-task.Done()

			atomic.AddInt32(&completedBroadcasts, 1)
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		assert.Equal(t, int32(broadcastCount), atomic.LoadInt32(&completedBroadcasts))
	case <-time.After(10 * time.Second):
		t.Fatalf("Timed out - only %d/%d completed", atomic.LoadInt32(&completedBroadcasts), broadcastCount)
	}
}
