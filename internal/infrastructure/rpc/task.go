// Package rpc 提供 RPC 层实现
package rpc

import (
	"context"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// RPCCallTask 单播调用任务
// ==========================================

// RPCCallTask 单播调用任务
// 实现单个节点的 RPC 调用
type RPCCallTask struct {
	*model.BaseTask[service.ResponseMsg]

	// RPC 特定字段
	rpc     service.RPCSync // 同步 RPC 接口
	to      model.PeerID    // 目标节点
	req     model.Message   // 请求消息
	timeout time.Duration   // 超时时间
}

// NewRPCCallTask 创建单播调用任务
func NewRPCCallTask(rpc service.RPCSync, to model.PeerID, req model.Message,
	timeout time.Duration) *RPCCallTask {
	task := &RPCCallTask{
		rpc:     rpc,
		to:      to,
		req:     req,
		timeout: timeout,
	}

	// 创建 BaseTask 并传入执行函数
	task.BaseTask = model.NewBaseTask(
		model.TaskPriorityNormal,

		0, // maxRetries
		func(ctx context.Context, pipeline model.TaskRunnerContext) (service.ResponseMsg, error) {
			// 创建带超时的上下文
			var cancel context.CancelFunc
			if task.timeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, task.timeout)
				defer cancel()
			}

			// 调用同步 RPC
			msg, err := task.rpc.Call(ctx, task.to, task.req)

			// 返回结果
			return service.ResponseMsg{
				Msg: msg,
				Err: err,
			}, nil
		},
	)

	return task
}

// GetPeerID 获取目标节点 ID
func (t *RPCCallTask) GetPeerID() model.PeerID {
	return t.to
}

// GetRequest 获取请求消息
func (t *RPCCallTask) GetRequest() model.Message {
	return t.req
}

// SetTimeout 设置超时时间
func (t *RPCCallTask) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

// GetTimeout 获取超时时间
func (t *RPCCallTask) GetTimeout() time.Duration {
	return t.timeout
}

// ==========================================
// RPCBroadcastTask 广播任务
// ==========================================

// RPCBroadcastTask 广播任务
// 实现多节点的广播调用
type RPCBroadcastTask struct {
	*model.BaseTask[service.AsyncBroadcastResult]

	// 广播特定字段
	rpc      service.RPCSync
	peers    []model.PeerID
	req      model.Message
	config   *service.RPCAsyncConfig
	callback service.BroadcastListener
}

// NewRPCBroadcastTask 创建广播任务
func NewRPCBroadcastTask(
	rpc service.RPCSync,
	peers []model.PeerID,
	req model.Message,
	config *service.RPCAsyncConfig,
	callback service.BroadcastListener,
) *RPCBroadcastTask {
	task := &RPCBroadcastTask{
		rpc:      rpc,
		peers:    peers,
		req:      req,
		config:   config,
		callback: callback,
	}

	task.BaseTask = model.NewBaseTask(
		model.TaskPriorityNormal,
		0, // maxRetries
		func(ctx context.Context, pipeline model.TaskRunnerContext) (service.AsyncBroadcastResult, error) {
			// 参数验证
			if rpc == nil {
				return service.AsyncBroadcastResult{}, service.ErrNilRPC
			}
			if config == nil {
				return service.AsyncBroadcastResult{}, service.ErrNilConfig
			}
			if len(peers) == 0 {
				return service.AsyncBroadcastResult{}, service.ErrEmptyPeers
			}

			// 创建带超时的上下文
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
			defer cancel()

			// 创建跟踪器
			tracker := NewBroadcastProgress("broadcast", peers)
			if callback != nil {
				tracker.SetCallback(callback)
			}

			// 执行广播调用
			result, err := rpc.BroadcastCall(callCtx, peers, req, service.ResponseAll, tracker)
			if err != nil {
				return service.AsyncBroadcastResult{}, err
			}

			// 构建异步结果
			asyncResult := service.AsyncBroadcastResult{
				Total:        len(peers),
				SuccessCount: len(result.SuccessPeers),
			}

			for i, peer := range result.SuccessPeers {
				if i < len(result.Responses) {
					asyncResult.Responses = append(asyncResult.Responses, service.PeerResponse{
						Peer:     peer,
						Response: result.Responses[i],
					})
				}
			}

			for _, peer := range result.FailedPeers {
				asyncResult.Errors = append(asyncResult.Errors, service.PeerError{
					Peer:  peer,
					Error: service.ErrPeerUnreachable,
				})
			}

			return asyncResult, nil
		},
	)

	return task
}

// ==========================================
// RPCQuorumTask Quorum 调用任务
// ==========================================

// RPCQuorumTask Quorum 调用任务
// 实现多数派调用
type RPCQuorumTask struct {
	*model.BaseTask[service.QuorumResult]

	// Quorum 特定字段
	rpc      service.RPCSync
	peers    []model.PeerID
	req      model.Message
	quorum   int
	config   *service.RPCAsyncConfig
	callback service.BroadcastListener
}

// NewRPCQuorumTask 创建 Quorum 任务
func NewRPCQuorumTask(
	rpc service.RPCSync,
	peers []model.PeerID,
	req model.Message,
	quorum int,
	config *service.RPCAsyncConfig,
	callback service.BroadcastListener,
) *RPCQuorumTask {
	task := &RPCQuorumTask{
		rpc:      rpc,
		peers:    peers,
		req:      req,
		quorum:   quorum,
		config:   config,
		callback: callback,
	}

	task.BaseTask = model.NewBaseTask(
		model.TaskPriorityNormal,
		0, // maxRetries
		func(ctx context.Context, pipeline model.TaskRunnerContext) (service.QuorumResult, error) {
			// 参数验证
			if rpc == nil {
				return service.QuorumResult{}, service.ErrNilRPC
			}
			if config == nil {
				return service.QuorumResult{}, service.ErrNilConfig
			}
			if len(peers) == 0 {
				return service.QuorumResult{}, service.ErrEmptyPeers
			}
			if quorum <= 0 || quorum > len(peers) {
				return service.QuorumResult{}, service.ErrInvalidQuorum
			}

			// 创建带超时的上下文
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
			defer cancel()

			// 创建跟踪器
			tracker := NewBroadcastProgress("quorum", peers)
			if callback != nil {
				tracker.SetCallback(callback)
			}

			// 执行 Quorum 调用
			result, err := rpc.BroadcastCall(callCtx, peers, req, service.ResponseMajority, tracker)
			if err != nil {
				return service.QuorumResult{}, err
			}

			// 构建结果
			quorumResult := service.QuorumResult{
				Quorum:  quorum,
				Reached: len(result.SuccessPeers) >= quorum,
			}

			for i, peer := range result.SuccessPeers {
				if i < len(result.Responses) {
					quorumResult.Responses = append(quorumResult.Responses, service.PeerResponse{
						Peer:     peer,
						Response: result.Responses[i],
					})
				}
			}

			return quorumResult, nil
		},
	)

	return task
}

// ==========================================
// RPCWriteVTask 批量写入任务
// ==========================================

// RPCWriteVTask 批量写入任务（单向）
type RPCWriteVTask struct {
	*model.BaseTask[service.WriteVResult]

	// WriteV 特定字段
	rpc      service.RPCSync
	targets  []model.PeerID
	msgs     []model.Message
	config   *service.RPCAsyncConfig
	callback service.BroadcastListener
}

// NewRPCWriteVTask 创建批量写入任务（单向）
func NewRPCWriteVTask(
	rpc service.RPCSync,
	targets []model.PeerID,
	msgs []model.Message,
	config *service.RPCAsyncConfig,
	callback service.BroadcastListener,
) *RPCWriteVTask {
	task := &RPCWriteVTask{
		rpc:      rpc,
		targets:  targets,
		msgs:     msgs,
		config:   config,
		callback: callback,
	}

	task.BaseTask = model.NewBaseTask(
		model.TaskPriorityNormal,
		0, // maxRetries
		func(ctx context.Context, pipeline model.TaskRunnerContext) (service.WriteVResult, error) {
			// 参数验证
			if rpc == nil {
				return service.WriteVResult{}, service.ErrNilRPC
			}
			if config == nil {
				return service.WriteVResult{}, service.ErrNilConfig
			}
			if len(targets) == 0 {
				return service.WriteVResult{}, service.ErrEmptyPeers
			}
			if len(targets) != len(msgs) {
				return service.WriteVResult{}, service.ErrTargetsMsgsMismatch
			}

			// 创建跟踪器
			tracker := NewBroadcastProgress("writev", targets)
			if callback != nil {
				tracker.SetCallback(callback)
			}

			// 执行批量写入（单向，不等待响应）
			err := rpc.WriteV(ctx, targets, msgs, tracker)
			if err != nil {
				return service.WriteVResult{}, err
			}

			// 返回结果（单向调用没有响应）
			return service.WriteVResult{
				SuccessPeers: targets, // 单向调用假设全部成功
			}, nil
		},
	)

	return task
}

// ==========================================
// RPCWriteVCallTask 批量写入任务（带响应）
// ==========================================

// RPCWriteVCallTask 批量写入任务（带响应）
type RPCWriteVCallTask struct {
	*model.BaseTask[service.WriteVResult]

	// WriteVCall 特定字段
	rpc      service.RPCSync
	targets  []model.PeerID
	msgs     []model.Message
	config   *service.RPCAsyncConfig
	callback service.BroadcastListener
}

// NewRPCWriteVCallTask 创建批量写入任务（带响应）
func NewRPCWriteVCallTask(
	rpc service.RPCSync,
	targets []model.PeerID,
	msgs []model.Message,
	config *service.RPCAsyncConfig,
	callback service.BroadcastListener,
) *RPCWriteVCallTask {
	task := &RPCWriteVCallTask{
		rpc:      rpc,
		targets:  targets,
		msgs:     msgs,
		config:   config,
		callback: callback,
	}

	task.BaseTask = model.NewBaseTask(
		model.TaskPriorityNormal,
		0, // maxRetries
		func(ctx context.Context, pipeline model.TaskRunnerContext) (service.WriteVResult, error) {
			// 参数验证
			if rpc == nil {
				return service.WriteVResult{}, service.ErrNilRPC
			}
			if config == nil {
				return service.WriteVResult{}, service.ErrNilConfig
			}
			if len(targets) == 0 {
				return service.WriteVResult{}, service.ErrEmptyPeers
			}
			if len(targets) != len(msgs) {
				return service.WriteVResult{}, service.ErrTargetsMsgsMismatch
			}

			// 创建带超时的上下文
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.GetBroadcastTimeout())*time.Millisecond)
			defer cancel()

			// 创建跟踪器
			tracker := NewBroadcastProgress("writev-call", targets)
			if callback != nil {
				tracker.SetCallback(callback)
			}

			// 执行批量写入调用（带响应）
			result, err := rpc.WriteVCall(callCtx, targets, msgs, service.ResponseAll, tracker)
			if err != nil {
				return service.WriteVResult{}, err
			}

			return result, nil
		},
	)

	return task
}

// ==========================================
// 辅助函数
// ==========================================

// NewFailedRPCTask 创建失败的 RPC 任务
// 用于立即返回错误的场景
func NewFailedRPCTask(err error) *model.BaseTask[service.ResponseMsg] {
	return model.NewBaseTask(
		model.TaskPriorityNormal,
		0, // maxRetries
		func(ctx context.Context, pipeline model.TaskRunnerContext) (service.ResponseMsg, error) {
			return service.ResponseMsg{Err: err}, err
		},
	)
}
