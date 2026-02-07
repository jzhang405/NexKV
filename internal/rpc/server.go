// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/transport"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	// DefaultWriteTimeout 默认写超时时间
	DefaultWriteTimeout = 30 * time.Second

	// maxMessagesPerStream 单个 Stream 最大消息数（防止资源耗尽）
	maxMessagesPerStream = 1000
)

// Server 基于 libp2p Stream 的 RPC 服务器
type Server struct {
	host    host.Host
	router  *Router
	codec   *transport.MessagePackCodec
	mu      sync.RWMutex
	running bool
}

// NewServer 创建 RPC 服务器
func NewServer(host host.Host) *Server {
	return &Server{
		host:    host,
		router:  NewRouter(),
		codec:   transport.NewMessagePackCodec(),
		running: false,
	}
}

// RegisterHandler 注册 RPC 处理器
func (s *Server) RegisterHandler(method string, handler RPCHandler) error {
	return s.router.RegisterHandler(method, handler)
}

// RegisterHandlerFunc 便捷方法：注册函数作为处理器
func (s *Server) RegisterHandlerFunc(method string, handler func(ctx context.Context, req []byte) ([]byte, error)) error {
	return s.router.RegisterHandlerFunc(method, handler)
}

// Start 启动 RPC 服务器
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()

	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("RPC 服务器已在运行")
	}

	// 设置 Stream Handler
	s.host.SetStreamHandler(transport.ProtocolNexKVRPC, s.handleStream)

	s.running = true
	s.mu.Unlock()

	logging.WithFields(map[string]any{
		"protocol": transport.ProtocolNexKVRPC,
	}).Info("RPC 服务器已启动")

	// 等待上下文取消（不持有锁）
	<-ctx.Done()

	// 上下文已取消，停止服务器
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	logging.Info("RPC 服务器已停止（上下文已取消）")

	return nil
}

// Stop 停止 RPC 服务器
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return fmt.Errorf("RPC 服务器未运行")
	}

	// 移除 Stream Handler
	s.host.RemoveStreamHandler(transport.ProtocolNexKVRPC)

	s.running = false
	logging.Info("RPC 服务器已停止")

	return nil
}

// IsRunning 检查服务器是否正在运行
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// handleStream 处理传入的 Stream
func (s *Server) handleStream(stream network.Stream) {
	defer stream.Close()

	remotePeer := stream.Conn().RemotePeer()
	logging.WithField("remote_peer", remotePeer).Debug("收到 RPC Stream 连接")

	// 循环处理多个请求（单 Stream 多请求）
	messageCount := 0
	for messageCount < maxMessagesPerStream {
		msg, err := s.codec.Decode(stream)
		if err != nil {
			s.handleDecodeError(err, remotePeer.String())
			return
		}
		messageCount++

		// 验证消息
		if !msg.IsValid() {
			s.logInvalidMessage(remotePeer.String(), string(msg.Type))
			return
		}

		// 解析 RPC 请求
		req, err := UnmarshalRPCRequest(msg.Payload)
		if err != nil {
			s.logUnmarshalError(remotePeer.String(), err)
			_ = s.sendErrorResponse(stream, 0, ErrCodeBadRequest, err.Error())
			continue
		}

		// 路由到对应处理器
		respBody, err := s.router.Route(req.Method, context.Background(), req.Body)
		if err != nil {
			s.logRouteError(remotePeer.String(), req.Method, err)
			_ = s.sendErrorResponse(stream, req.RequestID, ErrCodeInternal, err.Error())
			continue
		}

		// 发送成功响应
		if err := s.sendSuccessResponse(stream, req.RequestID, respBody); err != nil {
			s.logSendError(remotePeer.String(), err)
			return
		}

		s.logRPCCallSuccess(remotePeer.String(), req.Method, req.RequestID)
	}

	// 达到消息数上限
	if messageCount >= maxMessagesPerStream {
		logging.WithFields(map[string]any{
			"remote_peer":   remotePeer,
			"message_count": messageCount,
		}).Info("Stream 消息数达到上限，关闭连接")
	}
}

// sendSuccessResponse 发送成功响应
func (s *Server) sendSuccessResponse(stream network.Stream, requestID uint64, body []byte) error {
	resp := &RPCResponse{
		RequestID: requestID,
		Status:    ErrCodeSuccess,
		Body:      body,
	}
	return s.sendResponse(stream, resp)
}

// sendErrorResponse 发送错误响应
func (s *Server) sendErrorResponse(stream network.Stream, requestID uint64, statusCode int, errMsg string) error {
	resp := &RPCResponse{
		RequestID: requestID,
		Status:    statusCode,
		Body:      []byte(errMsg),
	}
	return s.sendResponse(stream, resp)
}

// sendResponse 发送响应
func (s *Server) sendResponse(stream network.Stream, resp *RPCResponse) error {
	// 创建响应消息
	msg := transport.NewMessage(transport.MessageTypeCluster)
	msg.To = stream.Conn().RemotePeer().String()

	// 序列化响应
	payload, err := MarshalRPCResponse(resp)
	if err != nil {
		return err
	}
	msg.Payload = payload

	// 设置写超时
	if err := setStreamDeadline(stream, time.Now().Add(DefaultWriteTimeout)); err != nil {
		return err
	}

	// 写入消息
	if err := s.codec.Encode(stream, msg); err != nil {
		return fmt.Errorf("编码响应失败: %w", err)
	}

	return nil
}

// ========================================
// 默认处理器
// ========================================

// handlePing 处理 Ping 请求
func (s *Server) handlePing(ctx context.Context, req []byte) ([]byte, error) {
	var pingReq PingRequest
	if err := msgpack.Unmarshal(req, &pingReq); err != nil {
		return nil, err
	}

	pingResp := &PingResponse{
		Timestamp: pingReq.Timestamp,
	}

	return msgpack.Marshal(pingResp)
}

// handleNodePing 处理 TreeCoordinator 节点心跳
func (s *Server) handleNodePing(ctx context.Context, req []byte) ([]byte, error) {
	var pingReq NodePingRequest
	if err := msgpack.Unmarshal(req, &pingReq); err != nil {
		return nil, err
	}

	pingResp := &NodePingResponse{
		Sequence:  pingReq.Sequence,
		Status:    1, // Ready 状态
		Timestamp: nowTimestamp(),
	}

	return msgpack.Marshal(pingResp)
}

// RegisterDefaultHandlers 注册默认 RPC 方法
func (s *Server) RegisterDefaultHandlers() error {
	// 注册 Ping 方法（简单测试用）
	if err := s.RegisterHandlerFunc("Ping", s.handlePing); err != nil {
		return err
	}

	// 注册 NodePing 方法（TreeCoordinator 节点心跳）
	if err := s.RegisterHandlerFunc("NodePing", s.handleNodePing); err != nil {
		return err
	}

	// TODO: 注册更多默认方法（NodeJoin, ClusterStatus 等）

	logging.Info("默认 RPC 处理器已注册")
	return nil
}

// ========================================
// 日志辅助函数
// ========================================

func (s *Server) handleDecodeError(err error, remotePeer string) {
	if err == io.EOF {
		logging.WithField("remote_peer", remotePeer).Debug("Stream 连接已关闭")
		return
	}
	logging.WithFields(map[string]any{
		"remote_peer": remotePeer,
		"error":       err,
	}).Warn("解码 RPC 请求失败")
}

func (s *Server) logInvalidMessage(remotePeer string, msgType string) {
	logging.WithFields(map[string]any{
		"remote_peer": remotePeer,
		"msg_type":    msgType,
	}).Warn("收到无效的 RPC 消息")
}

func (s *Server) logUnmarshalError(remotePeer string, err error) {
	logging.WithFields(map[string]any{
		"remote_peer": remotePeer,
		"error":       err,
	}).Warn("解析 RPC 请求失败")
}

func (s *Server) logRouteError(remotePeer string, method string, err error) {
	logging.WithFields(map[string]any{
		"remote_peer": remotePeer,
		"method":      method,
		"error":       err,
	}).Warn("RPC 方法调用失败")
}

func (s *Server) logSendError(remotePeer string, err error) {
	logging.WithFields(map[string]any{
		"remote_peer": remotePeer,
		"error":       err,
	}).Warn("发送 RPC 响应失败")
}

func (s *Server) logRPCCallSuccess(remotePeer string, method string, requestID uint64) {
	logging.WithFields(map[string]any{
		"remote_peer": remotePeer,
		"method":      method,
		"request_id":  requestID,
	}).Debug("RPC 调用成功")
}
