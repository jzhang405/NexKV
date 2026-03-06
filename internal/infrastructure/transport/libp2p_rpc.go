// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/id"
)

// Libp2pRPC 基于 libp2p 的 RPC 实现
type Libp2pRPC struct {
	transport   service.Transport
	codec       model.Codec
	streamCodec model.StreamCodec
	config      *service.RPCConfig
	idGenerator *id.RequestIDGenerator
	middleware  service.MiddlewareChain
	provider    atomic.Value // 类型: service.TaskExecutor

	// 请求-响应匹配
	pendingCalls   map[string]*pendingCall
	pendingCallsMu sync.RWMutex

	// 请求处理
	requestChan chan service.RequestMsg
	handler     func(ctx context.Context, from model.PeerID, req model.Message) model.Message
	handlerMu   sync.RWMutex

	// 状态管理
	closed    atomic.Bool
	closeOnce sync.Once
	closeCh   chan struct{}
}

// pendingCall 等待响应的调用
type pendingCall struct {
	responseCh chan service.ResponseMsg
	done       atomic.Bool
}

// NewLibp2pRPC 创建 libp2p RPC 实例
func NewLibp2pRPC(transport service.Transport, provider service.TaskExecutor, config *service.RPCConfig) *Libp2pRPC {
	if provider == nil {
		panic("TaskPoolProvider is required, cannot be nil")
	}
	if config == nil {
		config = service.DefaultRPCConfig()
	}

	self := transport.Self()
	codec := NewMessagePackCodec()
	streamCodec := NewMessagePackStreamCodec()

	rpc := &Libp2pRPC{
		transport:    transport,
		codec:        codec,
		streamCodec:  streamCodec,
		config:       config,
		idGenerator:  id.NewRequestIDGenerator(string(self)),
		middleware:   NewMiddlewareChain(),
		pendingCalls: make(map[string]*pendingCall),
		requestChan:  make(chan service.RequestMsg, config.RequestBufferSize),
		closeCh:      make(chan struct{}),
	}
	rpc.provider.Store(provider)
	return rpc
}

// ============================================================================
// 单播方法
// ============================================================================

// Call 同步调用（阻塞等响应）
func (r *Libp2pRPC) Call(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
	if r.closed.Load() {
		return nil, service.ErrCanceled
	}

	// P1 修复：检查 nil 消息
	if req == nil {
		return nil, service.ErrInvalidMessage
	}

	// 生成请求 ID
	requestID := string(r.idGenerator.Next())
	reqWithID := r.setMessageID(req, requestID)

	// 创建等待上下文
	callCtx, cancel := context.WithTimeout(ctx, r.config.CallTimeout)
	defer cancel()

	// 注册等待响应
	call := r.registerPendingCall(requestID, callCtx)
	defer r.unregisterPendingCall(requestID)

	// P1 修复：发送请求并等待响应（流式双向通信）
	if err := r.sendRequestAndWaitResponse(callCtx, to, reqWithID, call); err != nil {
		return nil, err
	}

	// 等待响应
	select {
	case resp := <-call.responseCh:
		if resp.Err != nil {
			return nil, resp.Err
		}
		return resp.Msg, nil
	case <-callCtx.Done():
		if callCtx.Err() == context.DeadlineExceeded {
			return nil, service.ErrTimeout
		}
		return nil, service.ErrCanceled
	}
}

// CallAsync 异步调用（不阻塞，回调返回）
func (r *Libp2pRPC) CallAsync(ctx context.Context, to model.PeerID, req model.Message, cb func(model.Message, error)) error {
	if r.closed.Load() {
		return service.ErrCanceled
	}

	_ = r.provider.Load().(service.TaskExecutor).Submit(ctx, model.SourceRPCCallback, service.PriorityNormal, func(ctx context.Context) {
		resp, err := r.Call(ctx, to, req)
		if cb != nil {
			cb(resp, err)
		}
	})

	return nil
}

// OnRequest 注册请求处理器
func (r *Libp2pRPC) OnRequest(handler func(ctx context.Context, from model.PeerID, req model.Message) model.Message) error {
	r.handlerMu.Lock()
	defer r.handlerMu.Unlock()
	r.handler = handler
	return nil
}

// OnRequestChan 返回请求通道
// P2-3 修复：添加使用说明
//
// 注意：RPC 关闭时此 channel 不会关闭，需要配合 CloseCh() 使用
//
// 使用示例:
//
//	for {
//	    select {
//	    case req := <-rpc.OnRequestChan():
//	        // 处理请求
//	    case <-rpc.CloseCh():
//	        return
//	    }
//	}
func (r *Libp2pRPC) OnRequestChan() <-chan service.RequestMsg {
	return r.requestChan
}

// CloseCh 返回关闭信号通道
// P2-3 修复：提供关闭检测方法
func (r *Libp2pRPC) CloseCh() <-chan struct{} {
	return r.closeCh
}

// ============================================================================
// 广播方法
// ============================================================================

// BroadcastCall 同消息广播：支持响应策略 + 可选追踪器
func (r *Libp2pRPC) BroadcastCall(
	ctx context.Context,
	to []model.PeerID,
	req model.Message,
	strategy service.ResponseStrategy,
	tracker service.BroadcastProgress,
) (service.BroadcastResult, error) {
	if r.closed.Load() {
		return service.BroadcastResult{}, service.ErrCanceled
	}

	// P1-2 修复：输入验证
	if req == nil {
		return service.BroadcastResult{}, service.ErrInvalidMessage
	}

	if len(to) == 0 {
		return service.BroadcastResult{
			Responses:    make([]model.Message, 0),
			SuccessPeers: make([]model.PeerID, 0),
			FailedPeers:  make([]model.PeerID, 0),
		}, nil
	}

	// P1-2 修复：验证 PeerID 有效性
	for i, peer := range to {
		if peer == "" {
			return service.BroadcastResult{}, service.Wrapf(service.ErrPeerIDInvalid, "empty PeerID at index %d", i)
		}
	}

	// 根据策略处理
	switch strategy {
	case service.ResponseNone:
		// P2 修复：真正异步发送
		return r.broadcastFireAndForget(ctx, to, req, tracker)
	case service.ResponseMajority:
		return r.broadcastAndWait(ctx, to, req, strategy, tracker)
	case service.ResponseAll:
		return r.broadcastAndWait(ctx, to, req, strategy, tracker)
	default:
		return service.BroadcastResult{}, service.ErrInvalidStrategy
	}
}

// BroadcastAsync 同消息广播：异步回调 + 可选追踪器
func (r *Libp2pRPC) BroadcastAsync(
	ctx context.Context,
	to []model.PeerID,
	req model.Message,
	strategy service.ResponseStrategy,
	tracker service.BroadcastProgress,
	cb func(from model.PeerID, resp model.Message, err error),
) error {
	if r.closed.Load() {
		return service.ErrCanceled
	}

	execFunc := func() {
		result, err := r.BroadcastCall(ctx, to, req, strategy, tracker)
		if err != nil && cb != nil {
			cb("", nil, err)
			return
		}

		// 调用回调
		if cb != nil {
			for i, resp := range result.Responses {
				if i < len(result.SuccessPeers) {
					cb(result.SuccessPeers[i], resp, nil)
				}
			}
			for _, peer := range result.FailedPeers {
				cb(peer, nil, service.ErrPeerUnreachable)
			}
		}
	}

	_ = r.provider.Load().(service.TaskExecutor).Submit(ctx, model.SourceRPCClient, service.PriorityNormal, func(ctx context.Context) {
		execFunc()
	})

	return nil
}

// WriteV 不同消息群发：单向发送
func (r *Libp2pRPC) WriteV(ctx context.Context, targets []model.PeerID, msgs []model.Message, tracker service.BroadcastProgress) error {
	// P1-2 修复：输入验证
	if len(targets) != len(msgs) {
		return service.Wrapf(service.ErrInvalidParam, "targets and messages length mismatch: %d vs %d", len(targets), len(msgs))
	}

	if len(targets) == 0 {
		return nil
	}

	// P1-2 修复：验证 PeerID 和消息有效性
	for i, target := range targets {
		if target == "" {
			return service.Wrapf(service.ErrPeerIDInvalid, "empty PeerID at index %d", i)
		}
		if msgs[i] == nil {
			return service.Wrapf(service.ErrInvalidMessage, "nil message at index %d", i)
		}
	}

	// ✅ 修复：添加并发控制，避免瞬时连接数爆炸
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	// 使用信号量限制并发（与 WriteVCall 保持一致）
	sem := make(chan struct{}, r.config.MaxConcurrentCalls)

	for i := range targets {
		// 检查 context 是否已取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		idx := i // 捕获循环变量
		_ = r.provider.Load().(service.TaskExecutor).Submit(ctx, model.SourceRPCClient, service.PriorityNormal, func(ctx context.Context) {
			defer wg.Done()
			peerID := targets[idx]

			// 获取信号量
			sem <- struct{}{}
			defer func() { <-sem }()

			err := r.sendRequestNoResponse(ctx, peerID, msgs[idx])
			if tracker != nil {
				if err != nil {
					tracker.RecordFailure(peerID, err)
					// 记录第一个错误（但不中断其他发送）
					errOnce.Do(func() {
						firstErr = err
					})
				} else {
					// 单向发送没有响应
					tracker.RecordSuccess(peerID, nil)
				}
			}
		})
	}

	// 等待所有发送完成
	wg.Wait()

	return firstErr
}

// WriteVCall 不同消息群发：支持响应策略
func (r *Libp2pRPC) WriteVCall(
	ctx context.Context,
	targets []model.PeerID,
	msgs []model.Message,
	strategy service.ResponseStrategy,
	tracker service.BroadcastProgress,
) (service.WriteVResult, error) {
	if r.closed.Load() {
		return service.WriteVResult{}, service.ErrCanceled
	}

	// P1-2 修复：输入验证
	if len(targets) != len(msgs) {
		return service.WriteVResult{}, service.Wrapf(service.ErrInvalidParam, "targets and messages length mismatch: %d vs %d", len(targets), len(msgs))
	}

	if len(targets) == 0 {
		return service.WriteVResult{
			Responses:    make(map[model.PeerID]model.Message),
			SuccessPeers: make([]model.PeerID, 0),
			FailedPeers:  make([]model.PeerID, 0),
		}, nil
	}

	// P1-2 修复：验证 PeerID 和消息有效性
	for i, target := range targets {
		if target == "" {
			return service.WriteVResult{}, service.Wrapf(service.ErrPeerIDInvalid, "empty PeerID at index %d", i)
		}
		if msgs[i] == nil {
			return service.WriteVResult{}, service.Wrapf(service.ErrInvalidMessage, "nil message at index %d", i)
		}
	}

	// P2 修复：添加并发控制
	var wg sync.WaitGroup
	result := service.WriteVResult{
		Responses:    make(map[model.PeerID]model.Message),
		SuccessPeers: make([]model.PeerID, 0),
		FailedPeers:  make([]model.PeerID, 0),
	}
	var resultMu sync.Mutex

	// 使用信号量限制并发
	sem := make(chan struct{}, r.config.MaxConcurrentCalls)

	for i := range targets {
		// P1-1 修复：检查 context 是否已取消，避免创建不必要的 goroutine
		select {
		case <-ctx.Done():
			// 将剩余节点标记为失败
			for j := i; j < len(targets); j++ {
				result.FailedPeers = append(result.FailedPeers, targets[j])
			}
			wg.Wait()
			return result, ctx.Err()
		default:
		}

		sem <- struct{}{} // 获取信号量
		wg.Add(1)
		idx := i // 捕获循环变量
		_ = r.provider.Load().(service.TaskExecutor).Submit(ctx, model.SourceRPCClient, service.PriorityNormal, func(ctx context.Context) {
			defer func() { <-sem }() // 释放信号量
			defer wg.Done()

			target := targets[idx]
			msg := msgs[idx]

			resp, err := r.Call(ctx, target, msg)

			resultMu.Lock()
			defer resultMu.Unlock()

			if err != nil {
				result.FailedPeers = append(result.FailedPeers, target)
				if tracker != nil {
					tracker.RecordFailure(target, err)
				}
			} else {
				result.Responses[target] = resp
				result.SuccessPeers = append(result.SuccessPeers, target)
				if tracker != nil {
					tracker.RecordSuccess(target, resp)
				}
			}
		})
	}

	wg.Wait()

	// 根据策略判断是否成功
	if err := validateStrategy(strategy, len(targets), len(result.SuccessPeers), len(result.FailedPeers)); err != nil {
		return result, err
	}

	return result, nil
}

// ============================================================================
// 生命周期
// ============================================================================

// Close 关闭 RPC
func (r *Libp2pRPC) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil // 已经关闭
	}

	r.closeOnce.Do(func() {
		close(r.closeCh)
		// P0-3 修复：不关闭 requestChan，避免 HandleIncomingStream 写入时 panic
		// requestChan 会在 RPC 实例被 GC 时自动回收
		// 关闭 closeCh 通知所有 goroutine 退出

		// P0-1 修复：取消所有等待中的调用并清理 map，防止内存泄漏
		r.pendingCallsMu.Lock()
		for id, call := range r.pendingCalls {
			call.done.Store(true)
			select {
			case call.responseCh <- service.ResponseMsg{Err: service.ErrCanceled}:
			default:
			}
			delete(r.pendingCalls, id)
		}
		r.pendingCallsMu.Unlock()
	})

	return nil
}

// SetExecutor 设置任务执行器（实现 service.RPCSync 接口）
func (r *Libp2pRPC) SetExecutor(provider service.TaskExecutor) {
	r.provider.Store(provider)
}

// ============================================================================
// 内部方法
// ============================================================================

// setMessageID 设置消息 ID
func (r *Libp2pRPC) setMessageID(msg model.Message, id string) model.Message {
	// 创建带有新 ID 的消息副本
	return model.NewMessage(id, msg.Type(), msg.Source(), msg.Target(), msg.Payload())
}

// sendRequestAndWaitResponse P1 修复：发送请求并等待响应（流式双向通信）
// P0 中间件集成：在发送前执行中间件链
func (r *Libp2pRPC) sendRequestAndWaitResponse(ctx context.Context, to model.PeerID, req model.Message, call *pendingCall) error {
	// P0 中间件集成：执行发送中间件链
	finalSend := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return r.doSendRequestAndWaitResponse(ctx, peer, msg, call)
	}
	return r.middleware.ExecuteSend(ctx, to, req, finalSend)
}

// doSendRequestAndWaitResponse 实际的发送请求并等待响应逻辑
func (r *Libp2pRPC) doSendRequestAndWaitResponse(ctx context.Context, to model.PeerID, req model.Message, call *pendingCall) error {
	// 检查连接
	if !r.transport.IsConnected(to) {
		return service.ErrPeerUnreachable
	}

	// 打开流
	stream, err := r.transport.OpenStream(ctx, to, "/nexkv/rpc/1.0.0")
	if err != nil {
		return service.Wrapf(service.ErrPeerUnreachable, "%v", err)
	}

	// P1-5 修复：使用 StreamCodec 编码请求（支持大消息和分帧）
	if err := r.streamCodec.EncodeToWriter(stream, req); err != nil {
		stream.Close()
		return service.Wrapf(service.ErrCodecFailure, "%v", err)
	}

	// 异步读取响应（使用 StreamCodec 支持大消息）
	readFunc := func(ctx context.Context) {
		defer stream.Close()

		// P2-2 修复：设置读取超时，避免 goroutine 无限阻塞
		if err := stream.SetReadDeadline(time.Now().Add(r.config.CallTimeout)); err != nil {
			r.handleResponse(req.ID(), service.ResponseMsg{Msg: nil, Err: err})
			return
		}

		// P1-5 修复：使用 StreamCodec 读取响应（支持大消息和分帧）
		resp, err := r.streamCodec.DecodeFromReader(stream)
		if err != nil {
			r.handleResponse(req.ID(), service.ResponseMsg{Msg: nil, Err: err})
			return
		}

		r.handleResponse(req.ID(), service.ResponseMsg{Msg: resp, Err: nil})
	}

	_ = r.provider.Load().(service.TaskExecutor).Submit(ctx, model.SourceRPCClient, service.PriorityNormal, readFunc)

	return nil
}

// sendRequestNoResponse 发送请求但不等待响应（单向）
// P0 中间件集成：在发送前执行中间件链
func (r *Libp2pRPC) sendRequestNoResponse(ctx context.Context, to model.PeerID, req model.Message) error {
	// P0 中间件集成：执行发送中间件链
	finalSend := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return r.doSendRequestNoResponse(ctx, peer, msg)
	}
	return r.middleware.ExecuteSend(ctx, to, req, finalSend)
}

// doSendRequestNoResponse 实际的发送请求逻辑（不等待响应）
func (r *Libp2pRPC) doSendRequestNoResponse(ctx context.Context, to model.PeerID, req model.Message) error {
	// 检查连接
	if !r.transport.IsConnected(to) {
		return service.ErrPeerUnreachable
	}

	// 编码消息
	data, err := r.codec.Encode(req)
	if err != nil {
		return err
	}

	// 打开流
	stream, err := r.transport.OpenStream(ctx, to, "/nexkv/rpc/1.0.0")
	if err != nil {
		return service.Wrapf(service.ErrPeerUnreachable, "%v", err)
	}
	defer stream.Close()

	// 写入数据
	if _, err := stream.Write(data); err != nil {
		return service.Wrapf(service.ErrCodecFailure, "%v", err)
	}

	return nil
}

// handleResponse P0 修复：安全地处理响应
func (r *Libp2pRPC) handleResponse(requestID string, resp service.ResponseMsg) {
	r.pendingCallsMu.Lock()
	call, ok := r.pendingCalls[requestID]
	if !ok {
		r.pendingCallsMu.Unlock()
		return // 没有找到对应的 pending call
	}

	// P0 修复：使用 CAS 确保只发送一次响应
	if !call.done.CompareAndSwap(false, true) {
		r.pendingCallsMu.Unlock()
		return // 已经处理过了
	}

	// 删除 pending call
	delete(r.pendingCalls, requestID)
	r.pendingCallsMu.Unlock()

	// 非阻塞发送响应（channel 有缓冲）
	select {
	case call.responseCh <- resp:
	default:
		// channel 已满，丢弃响应
	}
}

// registerPendingCall 注册等待响应的调用
func (r *Libp2pRPC) registerPendingCall(requestID string, callCtx context.Context) *pendingCall {
	call := &pendingCall{
		responseCh: make(chan service.ResponseMsg, 1),
	}

	r.pendingCallsMu.Lock()
	r.pendingCalls[requestID] = call
	r.pendingCallsMu.Unlock()

	return call
}

// unregisterPendingCall 注销等待响应的调用
func (r *Libp2pRPC) unregisterPendingCall(requestID string) {
	r.pendingCallsMu.Lock()
	if call, ok := r.pendingCalls[requestID]; ok {
		call.done.Store(true)
		delete(r.pendingCalls, requestID)
	}
	r.pendingCallsMu.Unlock()
}

// broadcastFireAndForget P2 修复：真正的单向广播（异步发送）
func (r *Libp2pRPC) broadcastFireAndForget(
	ctx context.Context,
	to []model.PeerID,
	req model.Message,
	tracker service.BroadcastProgress,
) (service.BroadcastResult, error) {
	result := service.BroadcastResult{
		Responses:    make([]model.Message, 0),
		SuccessPeers: make([]model.PeerID, 0),
		FailedPeers:  make([]model.PeerID, 0),
	}
	var resultMu sync.Mutex

	var wg sync.WaitGroup
	for _, peer := range to {
		wg.Add(1)
		p := peer // 捕获循环变量
		_ = r.provider.Load().(service.TaskExecutor).Submit(ctx, model.SourceRPCClient, service.PriorityNormal, func(ctx context.Context) {
			defer wg.Done()

			err := r.sendRequestNoResponse(ctx, p, req)

			resultMu.Lock()
			defer resultMu.Unlock()

			if tracker != nil {
				if err != nil {
					tracker.RecordFailure(p, err)
				} else {
					tracker.RecordSuccess(p, nil)
				}
			}

			if err != nil {
				result.FailedPeers = append(result.FailedPeers, p)
			} else {
				result.SuccessPeers = append(result.SuccessPeers, p)
			}
		})
	}

	// 立即返回（不等待发送完成）
	// 但需要确保 tracker 被正确更新，所以等待 goroutine 完成
	wg.Wait()

	return result, nil
}

// broadcastAndWait 广播并等待响应
func (r *Libp2pRPC) broadcastAndWait(
	ctx context.Context,
	to []model.PeerID,
	req model.Message,
	strategy service.ResponseStrategy,
	tracker service.BroadcastProgress,
) (service.BroadcastResult, error) {
	result := service.BroadcastResult{
		Responses:    make([]model.Message, len(to)),
		SuccessPeers: make([]model.PeerID, 0),
		FailedPeers:  make([]model.PeerID, 0),
	}

	var wg sync.WaitGroup
	var resultMu sync.Mutex

	// P2 修复：使用信号量限制并发（0 表示无限制）
	maxConcurrent := r.config.MaxConcurrentCalls
	if maxConcurrent <= 0 {
		maxConcurrent = 1000 // 默认值
	}
	sem := make(chan struct{}, maxConcurrent)

	// 并发发送请求
	for i, peer := range to {
		// P1-1 修复：检查 context 是否已取消，避免创建不必要的 goroutine
		select {
		case <-ctx.Done():
			// Context 已取消，将剩余节点标记为失败
			for j := i; j < len(to); j++ {
				result.FailedPeers = append(result.FailedPeers, to[j])
			}
			wg.Wait()
			return result, ctx.Err()
		default:
		}

		sem <- struct{}{}
		wg.Add(1)
		idx := i // 捕获循环变量
		p := peer
		_ = r.provider.Load().(service.TaskExecutor).Submit(ctx, model.SourceRPCClient, service.PriorityNormal, func(ctx context.Context) {
			defer func() { <-sem }()
			defer wg.Done()

			// 创建带超时的上下文
			callCtx, cancel := context.WithTimeout(ctx, r.config.BroadcastTimeout)
			defer cancel()

			resp, err := r.Call(callCtx, p, req)

			resultMu.Lock()
			defer resultMu.Unlock()

			if err != nil {
				result.FailedPeers = append(result.FailedPeers, p)
				if tracker != nil {
					tracker.RecordFailure(p, err)
				}
			} else {
				result.Responses[idx] = resp
				result.SuccessPeers = append(result.SuccessPeers, p)
				if tracker != nil {
					tracker.RecordSuccess(p, resp)
				}
			}
		})
	}

	wg.Wait()

	// 根据策略判断是否成功
	if err := validateStrategy(strategy, len(to), len(result.SuccessPeers), len(result.FailedPeers)); err != nil {
		return result, err
	}

	// 清理 responses 中的 nil
	result.Responses = cleanNilResponses(result.Responses)

	return result, nil
}

// HandleIncomingStream 处理入站流（由外部调用）
// P0 修复：添加响应匹配逻辑
func (r *Libp2pRPC) HandleIncomingStream(stream service.Stream) error {
	defer stream.Close()

	// P2-1 修复：创建可取消的上下文，关联 RPC 关闭状态
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = r.provider.Load().(service.TaskExecutor).Submit(ctx, model.SourceRPCCallback, service.PriorityNormal, func(ctx context.Context) {
		select {
		case <-r.closeCh:
			cancel()
		case <-ctx.Done():
		}
	})

	// P1-5 修复：使用 StreamCodec 读取消息（支持大消息和分帧）
	msg, err := r.streamCodec.DecodeFromReader(stream)
	if err != nil {
		return err
	}

	// P0 修复：检查是否是响应消息
	if msg.Type() == model.MessageTypeResponse {
		r.handleResponse(msg.ID(), service.ResponseMsg{Msg: msg, Err: nil})
		return nil
	}

	// 处理请求消息
	r.handlerMu.RLock()
	handler := r.handler
	r.handlerMu.RUnlock()

	if handler != nil {
		// P2-1 修复：使用可取消的上下文
		resp := handler(ctx, stream.RemotePeer(), msg)
		if resp != nil {
			// P0-2 修复：确保响应携带与请求相同的 ID
			respWithID := r.setMessageID(resp, msg.ID())

			// 编码响应（使用 StreamCodec）
			if err := r.streamCodec.EncodeToWriter(stream, respWithID); err != nil {
				return err
			}
		}
		return nil
	}

	// P0-3 修复：发送到请求通道前检查关闭状态（非阻塞 + 关闭检查）
	select {
	case <-r.closeCh:
		return nil // RPC 已关闭，丢弃请求
	case r.requestChan <- service.RequestMsg{
		Ctx:    ctx, // P2-1 修复：使用可取消的上下文
		From:   stream.RemotePeer(),
		Req:    msg,
		RespCh: make(chan service.ResponseMsg, 1),
	}:
		return nil
	default:
		return nil // 通道满，丢弃请求
	}
}

// GetMiddleware 获取中间件链
func (r *Libp2pRPC) GetMiddleware() service.MiddlewareChain {
	return r.middleware
}

// Use 添加中间件到链尾（便捷方法）
// P0 中间件集成：提供便捷的中间件添加方法
func (r *Libp2pRPC) Use(middleware service.Middleware) error {
	return r.middleware.Use(middleware)
}

// validateStrategy 验证广播策略是否满足
func validateStrategy(strategy service.ResponseStrategy, total, success, failed int) error {
	switch strategy {
	case service.ResponseAll:
		if failed > 0 {
			return service.ErrAllFailed
		}
	case service.ResponseMajority:
		majority := total/2 + 1
		if success < majority {
			return service.ErrMajorityFailed
		}
	}
	return nil
}

// cleanNilResponses 清理响应列表中的 nil 值
func cleanNilResponses(responses []model.Message) []model.Message {
	clean := make([]model.Message, 0, len(responses))
	for _, resp := range responses {
		if resp != nil {
			clean = append(clean, resp)
		}
	}
	return clean
}

// 确保实现 RPC 接口
var _ service.RPCSync = (*Libp2pRPC)(nil)
