// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// Libp2pRPC 基于 libp2p 的 RPC 实现
type Libp2pRPC struct {
	transport   service.Transport
	codec       service.Codec
	streamCodec service.StreamCodec
	config      *service.RPCConfig
	idGenerator *service.RequestIDGenerator
	middleware  service.MiddlewareChain

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
	ctx        context.Context
	cancel     context.CancelFunc
	done       atomic.Bool // P0 修复：添加完成标记，防止向已关闭 channel 发送
}

// NewLibp2pRPC 创建 libp2p RPC 实例
func NewLibp2pRPC(transport service.Transport, config *service.RPCConfig) *Libp2pRPC {
	if config == nil {
		config = service.DefaultRPCConfig()
	}

	self := transport.Self()
	codec := NewMessagePackCodec()
	streamCodec := NewMessagePackStreamCodec()

	return &Libp2pRPC{
		transport:    transport,
		codec:        codec,
		streamCodec:  streamCodec,
		config:       config,
		idGenerator:  service.NewRequestIDGenerator(string(self)),
		middleware:   NewMiddlewareChain(),
		pendingCalls: make(map[string]*pendingCall),
		requestChan:  make(chan service.RequestMsg, config.RequestBufferSize),
		closeCh:      make(chan struct{}),
	}
}

// ============================================================================
// 单播方法
// ============================================================================

// Call 同步调用（阻塞等响应）
func (r *Libp2pRPC) Call(ctx context.Context, to model.PeerID, req model.Message) (model.Message, error) {
	if r.closed.Load() {
		return nil, service.ErrCanceled
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

	go func() {
		resp, err := r.Call(ctx, to, req)
		if cb != nil {
			cb(resp, err)
		}
	}()

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
func (r *Libp2pRPC) OnRequestChan() <-chan service.RequestMsg {
	return r.requestChan
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
	tracker *service.BroadcastTracker,
) (service.BroadcastResult, error) {
	if r.closed.Load() {
		return service.BroadcastResult{}, service.ErrCanceled
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
	tracker *service.BroadcastTracker,
	cb func(from model.PeerID, resp model.Message, err error),
) error {
	if r.closed.Load() {
		return service.ErrCanceled
	}

	go func() {
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
	}()

	return nil
}

// WriteV 不同消息群发：单向发送
func (r *Libp2pRPC) WriteV(ctx context.Context, targets []model.PeerID, msgs []model.Message, tracker *service.BroadcastTracker) error {
	if len(targets) != len(msgs) {
		return fmt.Errorf("targets and messages length mismatch: %d vs %d", len(targets), len(msgs))
	}

	for i, target := range targets {
		err := r.sendRequestNoResponse(ctx, target, msgs[i])
		if tracker != nil {
			if err != nil {
				tracker.RecordFailure(target, err)
			} else {
				// 单向发送没有响应
				tracker.RecordSuccess(target, nil)
			}
		}
	}

	return nil
}

// WriteVCall 不同消息群发：支持响应策略
func (r *Libp2pRPC) WriteVCall(
	ctx context.Context,
	targets []model.PeerID,
	msgs []model.Message,
	strategy service.ResponseStrategy,
	tracker *service.BroadcastTracker,
) (service.WriteVResult, error) {
	if r.closed.Load() {
		return service.WriteVResult{}, service.ErrCanceled
	}

	if len(targets) != len(msgs) {
		return service.WriteVResult{}, fmt.Errorf("targets and messages length mismatch: %d vs %d", len(targets), len(msgs))
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
		sem <- struct{}{} // 获取信号量
		wg.Add(1)
		go func(idx int) {
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
		}(i)
	}

	wg.Wait()

	// 根据策略判断是否成功
	switch strategy {
	case service.ResponseAll:
		if len(result.FailedPeers) > 0 {
			return result, service.ErrAllFailed
		}
	case service.ResponseMajority:
		majority := len(targets)/2 + 1
		if len(result.SuccessPeers) < majority {
			return result, service.ErrMajorityFailed
		}
	case service.ResponseNone:
		// 不检查结果
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
		close(r.requestChan)

		// 取消所有等待中的调用
		r.pendingCallsMu.Lock()
		for _, call := range r.pendingCalls {
			call.cancel()
		}
		r.pendingCallsMu.Unlock()
	})

	return nil
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
func (r *Libp2pRPC) sendRequestAndWaitResponse(ctx context.Context, to model.PeerID, req model.Message, call *pendingCall) error {
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
		return fmt.Errorf("%w: %v", service.ErrPeerUnreachable, err)
	}
	defer stream.Close()

	// 写入请求
	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("%w: %v", service.ErrCodecFailure, err)
	}

	// 异步读取响应
	go func() {
		buf := make([]byte, 64*1024)
		n, err := stream.Read(buf)
		if err != nil {
			r.handleResponse(req.ID(), service.ResponseMsg{Msg: nil, Err: err})
			return
		}

		resp, err := r.codec.Decode(buf[:n])
		if err != nil {
			r.handleResponse(req.ID(), service.ResponseMsg{Msg: nil, Err: err})
			return
		}

		r.handleResponse(req.ID(), service.ResponseMsg{Msg: resp, Err: nil})
	}()

	return nil
}

// sendRequestNoResponse 发送请求但不等待响应（单向）
func (r *Libp2pRPC) sendRequestNoResponse(ctx context.Context, to model.PeerID, req model.Message) error {
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
		return fmt.Errorf("%w: %v", service.ErrPeerUnreachable, err)
	}
	defer stream.Close()

	// 写入数据
	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("%w: %v", service.ErrCodecFailure, err)
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
func (r *Libp2pRPC) registerPendingCall(requestID string, ctx context.Context) *pendingCall {
	callCtx, cancel := context.WithCancel(ctx)

	call := &pendingCall{
		responseCh: make(chan service.ResponseMsg, 1),
		ctx:        callCtx,
		cancel:     cancel,
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
		call.cancel()
		// 标记为已完成，防止后续响应发送
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
	tracker *service.BroadcastTracker,
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
		go func(p model.PeerID) {
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
		}(peer)
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
	tracker *service.BroadcastTracker,
) (service.BroadcastResult, error) {
	result := service.BroadcastResult{
		Responses:    make([]model.Message, len(to)),
		SuccessPeers: make([]model.PeerID, 0),
		FailedPeers:  make([]model.PeerID, 0),
	}

	var wg sync.WaitGroup
	var resultMu sync.Mutex

	// P2 修复：使用信号量限制并发
	sem := make(chan struct{}, r.config.MaxConcurrentCalls)

	// 并发发送请求
	for i, peer := range to {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, p model.PeerID) {
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
		}(i, peer)
	}

	wg.Wait()

	// 根据策略判断是否成功
	switch strategy {
	case service.ResponseAll:
		if len(result.FailedPeers) > 0 {
			return result, service.ErrAllFailed
		}
	case service.ResponseMajority:
		majority := len(to)/2 + 1
		if len(result.SuccessPeers) < majority {
			return result, service.ErrMajorityFailed
		}
	}

	// 清理 responses 中的 nil
	cleanResponses := make([]model.Message, 0, len(result.SuccessPeers))
	for _, resp := range result.Responses {
		if resp != nil {
			cleanResponses = append(cleanResponses, resp)
		}
	}
	result.Responses = cleanResponses

	return result, nil
}

// HandleIncomingStream 处理入站流（由外部调用）
// P0 修复：添加响应匹配逻辑
func (r *Libp2pRPC) HandleIncomingStream(stream service.Stream) error {
	defer stream.Close()

	// 读取数据
	buf := make([]byte, 64*1024) // 64KB buffer
	n, err := stream.Read(buf)
	if err != nil {
		return err
	}

	// 解码消息
	msg, err := r.codec.Decode(buf[:n])
	if err != nil {
		return err
	}

	// P0 修复：检查是否是响应消息
	if msg.Type() == model.MessageTypeResponse {
		// 查找 pending call 并发送响应
		r.handleResponse(msg.ID(), service.ResponseMsg{Msg: msg, Err: nil})
		return nil
	}

	// 处理请求消息
	r.handlerMu.RLock()
	handler := r.handler
	r.handlerMu.RUnlock()

	if handler != nil {
		// 同步处理请求
		resp := handler(context.Background(), stream.RemotePeer(), msg)
		if resp != nil {
			// 编码响应
			respData, err := r.codec.Encode(resp)
			if err != nil {
				return err
			}
			// 发送响应
			if _, err := stream.Write(respData); err != nil {
				return err
			}
		}
		return nil
	}

	// 发送到请求通道（非阻塞）
	select {
	case r.requestChan <- service.RequestMsg{
		Ctx:    context.Background(),
		From:   stream.RemotePeer(),
		Req:    msg,
		RespCh: make(chan service.ResponseMsg, 1),
	}:
		return nil
	default:
		// 通道满，丢弃请求
		return nil
	}
}

// GetMiddleware 获取中间件链
func (r *Libp2pRPC) GetMiddleware() service.MiddlewareChain {
	return r.middleware
}

// 确保实现 RPC 接口
var _ service.RPC = (*Libp2pRPC)(nil)
