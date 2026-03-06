// Package rpc 提供 RPC 层实现
package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// RPCCallTask 测试
// ==========================================

func TestRPCCallTask_Execute_Success(t *testing.T) {
	// 准备
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))
	respMsg := model.NewMessage("resp-1", model.MessageTypeResponse, "node-2", "node-1", []byte("ok"))

	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			assert.Equal(t, model.PeerID("node-2"), to)
			assert.Equal(t, model.MessageTypeRequest, req.Type())
			return respMsg, nil
		},
	}

	task := NewRPCCallTask(
		mockRPC,
		model.PeerID("node-2"),
		reqMsg,
		model.SourceRPCCallback,
		30*time.Second,
	)

	// 执行（通过 Run 方法）
	go task.Run(context.Background(), nil)

	// 等待完成
	resp, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)
	assert.Equal(t, model.MessageTypeResponse, resp.Msg.Type())
	assert.Nil(t, resp.Err)
}

func TestRPCCallTask_Execute_Timeout(t *testing.T) {
	// 准备
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			// 模拟超时
			time.Sleep(100 * time.Millisecond)
			return nil, ctx.Err()
		},
	}

	task := NewRPCCallTask(
		mockRPC,
		model.PeerID("node-2"),
		reqMsg,
		model.SourceRPCCallback,
		10*time.Millisecond, // 很短的超时
	)

	// 执行（通过 Run 方法）
	go task.Run(context.Background(), nil)

	// 等待完成
	resp, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)    // Wait 方法本身不返回错误
	assert.NotNil(t, resp.Err) // 但是响应中包含错误
}

func TestRPCCallTask_Execute_Error(t *testing.T) {
	// 准备
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))
	expectedErr := errors.New("rpc error")

	mockRPC := &mockRPCSync{
		callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
			return nil, expectedErr
		},
	}

	task := NewRPCCallTask(
		mockRPC,
		model.PeerID("node-2"),
		reqMsg,
		model.SourceRPCCallback,
		30*time.Second,
	)

	// 执行（通过 Run 方法）
	go task.Run(context.Background(), nil)

	// 等待完成
	resp, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)                // Wait 方法本身不返回错误
	assert.Equal(t, expectedErr, resp.Err) // 但是响应中包含错误
}

func TestRPCCallTask_Metadata(t *testing.T) {
	// 准备
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))
	sourceID := model.MustParseSourceID("shard:1:write")

	task := NewRPCCallTask(
		&mockRPCSync{},
		model.PeerID("node-2"),
		reqMsg,
		sourceID,
		30*time.Second,
	)

	// 验证元数据
	assert.Equal(t, model.PeerID("node-2"), task.GetPeerID())
	assert.Equal(t, model.MessageTypeRequest, task.GetRequest().Type())
	assert.Equal(t, []byte("test"), task.GetRequest().Payload())
	assert.Equal(t, 30*time.Second, task.GetTimeout())
	assert.Equal(t, model.TaskPriorityNormal, task.Priority())
	assert.Equal(t, sourceID, task.SourceID())
}

// ==========================================
// FailedRPCTask 测试
// ==========================================

func TestFailedRPCTask_Execute(t *testing.T) {
	// 准备
	expectedErr := errors.New("immediate failure")
	task := NewFailedRPCTask(expectedErr)

	// 执行（通过 Run 方法）
	go task.Run(context.Background(), nil)

	// 等待完成
	resp, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Equal(t, expectedErr, resp.Err)
}

// ==========================================
// BaseTask Wait 测试
// ==========================================

func TestBaseTask_Wait(t *testing.T) {
	// 准备
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))
	respMsg := model.NewMessage("resp-1", model.MessageTypeResponse, "node-2", "node-1", []byte("ok"))

	task := NewRPCCallTask(
		&mockRPCSync{
			callFunc: func(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
				time.Sleep(50 * time.Millisecond)
				return respMsg, nil
			},
		},
		model.PeerID("node-2"),
		reqMsg,
		model.SourceRPCCallback,
		30*time.Second,
	)

	// 异步执行
	go task.Run(context.Background(), nil)

	// 等待完成
	resp, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)
	assert.Equal(t, model.MessageTypeResponse, resp.Msg.Type())
}

// ==========================================
// RPCBroadcastTask 测试
// ==========================================

func TestRPCBroadcastTask_Execute_Success(t *testing.T) {
	// 准备
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "broadcast", []byte("test"))

	mockRPC := &mockRPCSync{
		broadcastCallFunc: func(ctx context.Context, to []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error) {
			assert.Equal(t, peers, to)
			assert.Equal(t, service.ResponseAll, strategy)

			// 模拟成功响应
			responses := []model.Message{
				model.NewMessage("resp-1", model.MessageTypeResponse, "node-1", "sender", []byte("ok1")),
				model.NewMessage("resp-2", model.MessageTypeResponse, "node-2", "sender", []byte("ok2")),
				model.NewMessage("resp-3", model.MessageTypeResponse, "node-3", "sender", []byte("ok3")),
			}

			return service.BroadcastResult{
				Responses:    responses,
				SuccessPeers: peers,
				FailedPeers:  nil,
			}, nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCBroadcastTask(mockRPC, peers, reqMsg, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	result, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 3, result.SuccessCount)
	assert.Len(t, result.Responses, 3)
	assert.Len(t, result.Errors, 0)
}

func TestRPCBroadcastTask_Execute_PartialFailure(t *testing.T) {
	// 准备
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "broadcast", []byte("test"))

	mockRPC := &mockRPCSync{
		broadcastCallFunc: func(ctx context.Context, to []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error) {
			// 模拟部分失败
			responses := []model.Message{
				model.NewMessage("resp-1", model.MessageTypeResponse, "node-1", "sender", []byte("ok1")),
				model.NewMessage("resp-2", model.MessageTypeResponse, "node-2", "sender", []byte("ok2")),
			}

			return service.BroadcastResult{
				Responses:    responses,
				SuccessPeers: []model.PeerID{"node-1", "node-2"},
				FailedPeers:  []model.PeerID{"node-3"},
			}, nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCBroadcastTask(mockRPC, peers, reqMsg, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	result, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 2, result.SuccessCount)
	assert.Len(t, result.Responses, 2)
	assert.Len(t, result.Errors, 1)
	assert.Equal(t, model.PeerID("node-3"), result.Errors[0].Peer)
}

func TestRPCBroadcastTask_Execute_NilRPC(t *testing.T) {
	// 准备
	peers := []model.PeerID{"node-1", "node-2"}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "broadcast", []byte("test"))
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCBroadcastTask(nil, peers, reqMsg, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrNilRPC, err)
}

func TestRPCBroadcastTask_Execute_NilConfig(t *testing.T) {
	// 准备
	peers := []model.PeerID{"node-1", "node-2"}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "broadcast", []byte("test"))
	mockRPC := &mockRPCSync{}

	task := NewRPCBroadcastTask(mockRPC, peers, reqMsg, nil, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrNilConfig, err)
}

func TestRPCBroadcastTask_Execute_EmptyPeers(t *testing.T) {
	// 准备
	peers := []model.PeerID{}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "broadcast", []byte("test"))
	mockRPC := &mockRPCSync{}
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCBroadcastTask(mockRPC, peers, reqMsg, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrEmptyPeers, err)
}

func TestRPCBroadcastTask_Execute_WithCallback(t *testing.T) {
	// 准备
	peers := []model.PeerID{"node-1", "node-2"}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "broadcast", []byte("test"))

	callback := &testBroadcastListener{
		onSuccess: func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
			// 回调被触发
		},
	}

	mockRPC := &mockRPCSync{
		broadcastCallFunc: func(ctx context.Context, to []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error) {
			// 模拟回调触发
			if tracker != nil {
				tracker.RecordSuccess(to[0], model.NewMessage("resp-1", model.MessageTypeResponse, to[0], "sender", []byte("ok")))
			}

			return service.BroadcastResult{
				Responses:    []model.Message{model.NewMessage("resp-1", model.MessageTypeResponse, to[0], "sender", []byte("ok"))},
				SuccessPeers: to[:1],
			}, nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCBroadcastTask(mockRPC, peers, reqMsg, config, callback)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	// 注意：回调可能没有被触发，因为 BroadcastProgress 的回调是在 RPC 实现中手动调用的
	// 这里主要验证任务可以正常执行
}

// ==========================================
// RPCQuorumTask 测试
// ==========================================

func TestRPCQuorumTask_Execute_Success(t *testing.T) {
	// 准备
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "quorum", []byte("test"))
	quorum := 2

	mockRPC := &mockRPCSync{
		broadcastCallFunc: func(ctx context.Context, to []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error) {
			assert.Equal(t, service.ResponseMajority, strategy)

			// 模拟达到 quorum
			responses := []model.Message{
				model.NewMessage("resp-1", model.MessageTypeResponse, "node-1", "sender", []byte("ok1")),
				model.NewMessage("resp-2", model.MessageTypeResponse, "node-2", "sender", []byte("ok2")),
			}

			return service.BroadcastResult{
				Responses:    responses,
				SuccessPeers: []model.PeerID{"node-1", "node-2"},
				FailedPeers:  []model.PeerID{"node-3"},
			}, nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCQuorumTask(mockRPC, peers, reqMsg, quorum, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	result, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.Equal(t, quorum, result.Quorum)
	assert.True(t, result.Reached)
	assert.Len(t, result.Responses, 2)
}

func TestRPCQuorumTask_Execute_QuorumNotReached(t *testing.T) {
	// 准备
	peers := []model.PeerID{"node-1", "node-2", "node-3"}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "quorum", []byte("test"))
	quorum := 3

	mockRPC := &mockRPCSync{
		broadcastCallFunc: func(ctx context.Context, to []model.PeerID, req model.Message, strategy service.ResponseStrategy, tracker service.BroadcastProgress) (service.BroadcastResult, error) {
			// 模拟未达到 quorum
			responses := []model.Message{
				model.NewMessage("resp-1", model.MessageTypeResponse, "node-1", "sender", []byte("ok1")),
			}

			return service.BroadcastResult{
				Responses:    responses,
				SuccessPeers: []model.PeerID{"node-1"},
				FailedPeers:  []model.PeerID{"node-2", "node-3"},
			}, nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCQuorumTask(mockRPC, peers, reqMsg, quorum, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	result, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.Equal(t, quorum, result.Quorum)
	assert.False(t, result.Reached)
	assert.Len(t, result.Responses, 1)
}

func TestRPCQuorumTask_Execute_InvalidQuorum(t *testing.T) {
	tests := []struct {
		name   string
		quorum int
		peers  []model.PeerID
	}{
		{
			name:   "quorum is zero",
			quorum: 0,
			peers:  []model.PeerID{"node-1", "node-2"},
		},
		{
			name:   "quorum is negative",
			quorum: -1,
			peers:  []model.PeerID{"node-1", "node-2"},
		},
		{
			name:   "quorum exceeds peers",
			quorum: 5,
			peers:  []model.PeerID{"node-1", "node-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "quorum", []byte("test"))
			mockRPC := &mockRPCSync{}
			config := service.DefaultRPCAsyncConfig()

			task := NewRPCQuorumTask(mockRPC, tt.peers, reqMsg, tt.quorum, config, nil)

			// 执行
			go task.Run(context.Background(), nil)

			// 等待完成
			_, err := task.Wait(context.Background())

			// 验证
			require.Error(t, err)
			assert.Equal(t, service.ErrInvalidQuorum, err)
		})
	}
}

func TestRPCQuorumTask_Execute_NilRPC(t *testing.T) {
	// 准备
	peers := []model.PeerID{"node-1", "node-2"}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "quorum", []byte("test"))
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCQuorumTask(nil, peers, reqMsg, 2, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrNilRPC, err)
}

func TestRPCQuorumTask_Execute_EmptyPeers(t *testing.T) {
	// 准备
	peers := []model.PeerID{}
	reqMsg := model.NewMessage("req-1", model.MessageTypeRequest, "sender", "quorum", []byte("test"))
	mockRPC := &mockRPCSync{}
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCQuorumTask(mockRPC, peers, reqMsg, 2, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrEmptyPeers, err)
}

// ==========================================
// RPCWriteVTask 测试
// ==========================================

func TestRPCWriteVTask_Execute_Success(t *testing.T) {
	// 准备
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "sender", "node-1", []byte("data1")),
		model.NewMessage("msg-2", model.MessageTypeRequest, "sender", "node-2", []byte("data2")),
		model.NewMessage("msg-3", model.MessageTypeRequest, "sender", "node-3", []byte("data3")),
	}

	mockRPC := &mockRPCSync{
		writeVFunc: func(ctx context.Context, targetPeers []model.PeerID, messages []model.Message, progress service.BroadcastProgress) error {
			assert.Equal(t, targets, targetPeers)
			assert.Equal(t, msgs, messages)
			return nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCWriteVTask(mockRPC, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	result, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.Equal(t, targets, result.SuccessPeers)
	assert.Len(t, result.FailedPeers, 0)
}

func TestRPCWriteVTask_Execute_NilRPC(t *testing.T) {
	// 准备
	targets := []model.PeerID{"node-1", "node-2"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "sender", "node-1", []byte("data1")),
		model.NewMessage("msg-2", model.MessageTypeRequest, "sender", "node-2", []byte("data2")),
	}
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCWriteVTask(nil, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrNilRPC, err)
}

func TestRPCWriteVTask_Execute_EmptyTargets(t *testing.T) {
	// 准备
	targets := []model.PeerID{}
	msgs := []model.Message{}
	mockRPC := &mockRPCSync{}
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCWriteVTask(mockRPC, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrEmptyPeers, err)
}

func TestRPCWriteVTask_Execute_TargetsMsgsMismatch(t *testing.T) {
	// 准备
	targets := []model.PeerID{"node-1", "node-2"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "sender", "node-1", []byte("data1")),
	}
	mockRPC := &mockRPCSync{}
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCWriteVTask(mockRPC, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrTargetsMsgsMismatch, err)
}

func TestRPCWriteVTask_Execute_WithCallback(t *testing.T) {
	// 准备
	targets := []model.PeerID{"node-1", "node-2"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "sender", "node-1", []byte("data1")),
		model.NewMessage("msg-2", model.MessageTypeRequest, "sender", "node-2", []byte("data2")),
	}

	callback := &testBroadcastListener{
		onSuccess: func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
			// 回调被触发
		},
	}

	mockRPC := &mockRPCSync{
		writeVFunc: func(ctx context.Context, targetPeers []model.PeerID, messages []model.Message, progress service.BroadcastProgress) error {
			return nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCWriteVTask(mockRPC, targets, msgs, config, callback)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	// 回调可能没有触发，因为 WriteV 是单向的
}

// ==========================================
// RPCWriteVCallTask 测试
// ==========================================

func TestRPCWriteVCallTask_Execute_Success(t *testing.T) {
	// 准备
	targets := []model.PeerID{"node-1", "node-2"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "sender", "node-1", []byte("data1")),
		model.NewMessage("msg-2", model.MessageTypeRequest, "sender", "node-2", []byte("data2")),
	}

	mockRPC := &mockRPCSync{
		writeVCallFunc: func(ctx context.Context, targetPeers []model.PeerID, messages []model.Message, strategy service.ResponseStrategy, progress service.BroadcastProgress) (service.WriteVResult, error) {
			assert.Equal(t, targets, targetPeers)
			assert.Equal(t, msgs, messages)
			assert.Equal(t, service.ResponseAll, strategy)

			// 模拟成功响应
			responses := map[model.PeerID]model.Message{
				"node-1": model.NewMessage("resp-1", model.MessageTypeResponse, "node-1", "sender", []byte("ok1")),
				"node-2": model.NewMessage("resp-2", model.MessageTypeResponse, "node-2", "sender", []byte("ok2")),
			}

			return service.WriteVResult{
				Responses:    responses,
				SuccessPeers: targets,
				FailedPeers:  nil,
			}, nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCWriteVCallTask(mockRPC, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	result, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.Equal(t, targets, result.SuccessPeers)
	assert.Len(t, result.FailedPeers, 0)
	assert.Len(t, result.Responses, 2)
}

func TestRPCWriteVCallTask_Execute_PartialFailure(t *testing.T) {
	// 准备
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "sender", "node-1", []byte("data1")),
		model.NewMessage("msg-2", model.MessageTypeRequest, "sender", "node-2", []byte("data2")),
		model.NewMessage("msg-3", model.MessageTypeRequest, "sender", "node-3", []byte("data3")),
	}

	mockRPC := &mockRPCSync{
		writeVCallFunc: func(ctx context.Context, targetPeers []model.PeerID, messages []model.Message, strategy service.ResponseStrategy, progress service.BroadcastProgress) (service.WriteVResult, error) {
			// 模拟部分失败
			responses := map[model.PeerID]model.Message{
				"node-1": model.NewMessage("resp-1", model.MessageTypeResponse, "node-1", "sender", []byte("ok1")),
				"node-2": model.NewMessage("resp-2", model.MessageTypeResponse, "node-2", "sender", []byte("ok2")),
			}

			return service.WriteVResult{
				Responses:    responses,
				SuccessPeers: []model.PeerID{"node-1", "node-2"},
				FailedPeers:  []model.PeerID{"node-3"},
			}, nil
		},
	}

	config := service.DefaultRPCAsyncConfig()
	task := NewRPCWriteVCallTask(mockRPC, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	result, err := task.Wait(context.Background())

	// 验证
	require.NoError(t, err)
	assert.Len(t, result.SuccessPeers, 2)
	assert.Len(t, result.FailedPeers, 1)
	assert.Equal(t, model.PeerID("node-3"), result.FailedPeers[0])
}

func TestRPCWriteVCallTask_Execute_NilRPC(t *testing.T) {
	// 准备
	targets := []model.PeerID{"node-1", "node-2"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "sender", "node-1", []byte("data1")),
		model.NewMessage("msg-2", model.MessageTypeRequest, "sender", "node-2", []byte("data2")),
	}
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCWriteVCallTask(nil, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrNilRPC, err)
}

func TestRPCWriteVCallTask_Execute_EmptyTargets(t *testing.T) {
	// 准备
	targets := []model.PeerID{}
	msgs := []model.Message{}
	mockRPC := &mockRPCSync{}
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCWriteVCallTask(mockRPC, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrEmptyPeers, err)
}

func TestRPCWriteVCallTask_Execute_TargetsMsgsMismatch(t *testing.T) {
	// 准备
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "sender", "node-1", []byte("data1")),
	}
	mockRPC := &mockRPCSync{}
	config := service.DefaultRPCAsyncConfig()

	task := NewRPCWriteVCallTask(mockRPC, targets, msgs, config, nil)

	// 执行
	go task.Run(context.Background(), nil)

	// 等待完成
	_, err := task.Wait(context.Background())

	// 验证
	require.Error(t, err)
	assert.Equal(t, service.ErrTargetsMsgsMismatch, err)
}

// ==========================================
// 测试辅助类型
// ==========================================

// testBroadcastListener 测试用广播监听器
type testBroadcastListener struct {
	onSuccess  func(peer model.PeerID, resp model.Message, stats service.BroadcastStats)
	onFailure  func(peer model.PeerID, err error, stats service.BroadcastStats)
	onMajority func(stats service.BroadcastStats)
	onComplete func(stats service.BroadcastStats)
}

func (l *testBroadcastListener) OnSuccess(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
	if l.onSuccess != nil {
		l.onSuccess(peer, resp, stats)
	}
}

func (l *testBroadcastListener) OnFailure(peer model.PeerID, err error, stats service.BroadcastStats) {
	if l.onFailure != nil {
		l.onFailure(peer, err, stats)
	}
}

func (l *testBroadcastListener) OnMajority(stats service.BroadcastStats) {
	if l.onMajority != nil {
		l.onMajority(stats)
	}
}

func (l *testBroadcastListener) OnComplete(stats service.BroadcastStats) {
	if l.onComplete != nil {
		l.onComplete(stats)
	}
}
