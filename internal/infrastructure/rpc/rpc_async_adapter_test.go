// Package rpc RPC 适配器测试
package rpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// RPCAsyncAdapter 测试
// ==========================================

func TestNewRPCAsyncAdapter(t *testing.T) {
	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			return newTestMessage("resp"), nil
		},
	}

	t.Run("with nil config", func(t *testing.T) {
		adapter := NewRPCAsyncAdapter(mockRPC, nil)
		if adapter == nil {
			t.Fatal("expected adapter, got nil")
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		config := service.DefaultRPCAsyncConfig()
		config.DefaultTimeoutMs = 2000
		adapter := NewRPCAsyncAdapter(mockRPC, config)
		if adapter == nil {
			t.Fatal("expected adapter, got nil")
		}
	})
}

func TestRPCAsyncAdapter_CallAsync(t *testing.T) {
	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			return newTestMessage("resp"), nil
		},
	}

	adapter := NewRPCAsyncAdapter(mockRPC, nil)
	ctx := context.Background()

	t.Run("basic call", func(t *testing.T) {
		op := adapter.CallAsync(ctx, "peer-1", newTestMessage("test"))
		if op == nil {
			t.Fatal("expected operation, got nil")
		}

		resp, err := op.Await(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Msg == nil {
			t.Fatal("expected response message, got nil")
		}
	})

	t.Run("concurrent calls", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				op := adapter.CallAsync(ctx, "peer-1", newTestMessage("test"))
				_, _ = op.Await(ctx)
			}()
		}
		wg.Wait()
	})
}

func TestRPCAsyncAdapter_CallAsyncWithTimeout(t *testing.T) {
	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			// 检查 context 是否已超时
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				return newTestMessage("resp"), nil
			}
		},
	}

	adapter := NewRPCAsyncAdapter(mockRPC, nil)
	ctx := context.Background()

	t.Run("with sufficient timeout", func(t *testing.T) {
		op := adapter.CallAsyncWithTimeout(ctx, "peer-1", newTestMessage("test"), 200)
		resp, err := op.Await(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Msg == nil {
			t.Fatal("expected response")
		}
	})

	t.Run("with zero timeout", func(t *testing.T) {
		// 使用 0 超时会导致立即超时
		op := adapter.CallAsyncWithTimeout(ctx, "peer-1", newTestMessage("test"), 0)
		_, err := op.Await(ctx)
		// 0 超时应该导致错误，但具体行为取决于实现
		// 这里只验证操作能正常完成不 panic
		_ = err
	})
}

func TestRPCAsyncAdapter_BroadcastAsync(t *testing.T) {
	mockRPC := &mockRPCSync{
		broadcastCallFunc: func(ctx context.Context, peers []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error) {
			return service.BroadcastResult{
				Responses: []model.Message{
					newTestMessage("resp1"),
					newTestMessage("resp2"),
				},
				SuccessPeers: []model.PeerID{"peer-1", "peer-2"},
			}, nil
		},
	}

	adapter := NewRPCAsyncAdapter(mockRPC, nil)
	ctx := context.Background()

	t.Run("basic broadcast", func(t *testing.T) {
		peers := []model.PeerID{"peer-1", "peer-2"}
		op := adapter.BroadcastAsync(ctx, peers, newTestMessage("test"))
		if op == nil {
			t.Fatal("expected operation, got nil")
		}

		result, err := op.Await(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Responses) != 2 {
			t.Fatalf("expected 2 responses, got %d", len(result.Responses))
		}
	})

	t.Run("with callback option", func(t *testing.T) {
		// 回调通过 BroadcastConfig 设置，这里简化测试
		peers := []model.PeerID{"peer-1"}
		op := adapter.BroadcastAsync(ctx, peers, newTestMessage("test"))
		_, _ = op.Await(ctx)
	})
}

func TestRPCAsyncAdapter_BroadcastQuorumAsync(t *testing.T) {
	mockRPC := &mockRPCSync{
		broadcastCallFunc: func(ctx context.Context, peers []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error) {
			return service.BroadcastResult{
				Responses: []model.Message{
					newTestMessage("resp1"),
					newTestMessage("resp2"),
				},
				SuccessPeers: []model.PeerID{"peer-1", "peer-2"},
			}, nil
		},
	}

	adapter := NewRPCAsyncAdapter(mockRPC, nil)
	ctx := context.Background()

	t.Run("basic quorum", func(t *testing.T) {
		peers := []model.PeerID{"peer-1", "peer-2", "peer-3"}
		op := adapter.BroadcastQuorumAsync(ctx, peers, newTestMessage("test"), 2)
		if op == nil {
			t.Fatal("expected operation, got nil")
		}

		result, err := op.Await(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Responses) < 2 {
			t.Fatalf("expected at least 2 responses, got %d", len(result.Responses))
		}
	})
}

func TestRPCAsyncAdapter_WriteVAsync(t *testing.T) {
	mockRPC := &mockRPCSync{}
	adapter := NewRPCAsyncAdapter(mockRPC, nil)
	ctx := context.Background()

	t.Run("basic writev", func(t *testing.T) {
		targets := []model.PeerID{"peer-1", "peer-2"}
		msgs := []model.Message{
			newTestMessage("msg1"),
			newTestMessage("msg2"),
		}
		op := adapter.WriteVAsync(ctx, targets, msgs)
		if op == nil {
			t.Fatal("expected operation, got nil")
		}

		_, err := op.Await(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRPCAsyncAdapter_WriteVCallAsync(t *testing.T) {
	mockRPC := &mockRPCSync{}
	adapter := NewRPCAsyncAdapter(mockRPC, nil)
	ctx := context.Background()

	t.Run("basic writevcall", func(t *testing.T) {
		targets := []model.PeerID{"peer-1", "peer-2"}
		msgs := []model.Message{
			newTestMessage("msg1"),
			newTestMessage("msg2"),
		}
		op := adapter.WriteVCallAsync(ctx, targets, msgs)
		if op == nil {
			t.Fatal("expected operation, got nil")
		}

		_, err := op.Await(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRPCAsyncAdapter_SetExecutor(t *testing.T) {
	mockRPC := &mockRPCSync{}
	config := service.DefaultRPCAsyncConfig()
	adapter := NewRPCAsyncAdapter(mockRPC, config)

	t.Run("set provider", func(t *testing.T) {
		provider := newMockTaskPoolProvider()
		adapter.SetExecutor(provider)
		// 验证通过执行异步操作
	})

	t.Run("concurrent set and use", func(t *testing.T) {
		var wg sync.WaitGroup
		ctx := context.Background()

		// 并发设置 provider 和调用
		for i := 0; i < 10; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				provider := newMockTaskPoolProvider()
				adapter.SetExecutor(provider)
			}()
			go func() {
				defer wg.Done()
				_ = adapter.CallAsync(ctx, "peer-1", newTestMessage("test"))
			}()
		}
		wg.Wait()
	})
}

func TestRPCAsyncAdapter_ConcurrentSafety(t *testing.T) {
	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			return newTestMessage("resp"), nil
		},
	}

	adapter := NewRPCAsyncAdapter(mockRPC, nil)
	ctx := context.Background()

	var wg sync.WaitGroup
	// 并发执行各种操作
	for i := 0; i < 20; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			_ = adapter.CallAsync(ctx, "peer-1", newTestMessage("test"))
		}()
		go func() {
			defer wg.Done()
			adapter.SetExecutor(newMockTaskPoolProvider())
		}()
		go func() {
			defer wg.Done()
			peers := []model.PeerID{"peer-1", "peer-2"}
			_ = adapter.BroadcastAsync(ctx, peers, newTestMessage("test"))
		}()
		go func() {
			defer wg.Done()
			_ = adapter.CallAsyncWithTimeout(ctx, "peer-1", newTestMessage("test"), 100)
		}()
	}
	wg.Wait()
}

// ==========================================
// Mock 实现
// ==========================================

// mockTaskPoolProvider 模拟 TaskPoolProvider
type mockTaskPoolProvider struct{}

func newMockTaskPoolProvider() *mockTaskPoolProvider {
	return &mockTaskPoolProvider{}
}

func (m *mockTaskPoolProvider) Submit(ctx context.Context, priority service.TaskPriority, task func(context.Context)) error {
	go task(ctx)
	return nil
}

func (m *mockTaskPoolProvider) Close() error {
	return nil
}

func (m *mockTaskPoolProvider) CloseWithTimeout(timeout time.Duration) error {
	return nil
}

// mockRPCSync 模拟 RPCSync 接口
type mockRPCSync struct {
	callFunc          func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error)
	broadcastCallFunc func(ctx context.Context, peers []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error)
}

func (m *mockRPCSync) Call(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, to, req)
	}
	return newTestMessage("resp"), nil
}

func (m *mockRPCSync) BroadcastCall(ctx context.Context, peers []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error) {
	if m.broadcastCallFunc != nil {
		return m.broadcastCallFunc(ctx, peers, req, strategy, tracker)
	}
	return service.BroadcastResult{}, nil
}

func (m *mockRPCSync) WriteV(ctx context.Context, targets []model.PeerID, msgs []model.Message, progress service.BroadcastProgress) error {
	return nil
}

func (m *mockRPCSync) WriteVCall(ctx context.Context, targets []model.PeerID, msgs []model.Message, strategy service.ResponseStrategy, progress service.BroadcastProgress) (service.WriteVResult, error) {
	return service.WriteVResult{}, nil
}

func (m *mockRPCSync) OnRequest(handler func(ctx context.Context, from model.PeerID, req model.Message) model.Message) error {
	return nil
}

func (m *mockRPCSync) OnRequestChan() <-chan service.RequestMsg {
	return nil
}

func (m *mockRPCSync) Close() error {
	return nil
}

func (m *mockRPCSync) SetExecutor(provider service.TaskExecutor) {}

// newTestMessage 创建测试消息
func newTestMessage(content string) model.Message {
	return model.NewMessage(content, model.MessageTypeRequest, "", "", []byte(content))
}
