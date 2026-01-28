// Package server RPC Server 实现
//
// 接收 CLI 和其他节点的 RPC 调用
package server

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	rpccommon "github.com/jzhang405/NexKV/internal/rpc/common"
)

// RPCServer RPC 服务器
type RPCServer struct {
	// 配置
	addr     string
	listener net.Listener

	// RPC 处理器注册表
	registry *rpccommon.HandlerRegistry

	// Transport 层（用于发送响应）
	transport transport.Transport

	// 生命周期
	started sync.Once
	stopped sync.Once
	stopCh  chan struct{}
	stopWg  sync.WaitGroup

	// 统计信息
	stats *ServerStats
}

// ServerStats 服务器统计信息
type ServerStats struct {
	TotalConnections   uint64
	ActiveConnections  uint64
	TotalRequests      uint64
	SuccessfulRequests uint64
	FailedRequests     uint64
}

// Config RPC Server 配置
type Config struct {
	// 监听地址（格式：host:port）
	Addr string
	// Transport 层（可选，用于发送 UDP 消息）
	Transport transport.Transport
}

// NewRPCServer 创建 RPC Server
func NewRPCServer(config *Config) (*RPCServer, error) {
	if config == nil {
		return nil, fmt.Errorf("配置不能为空")
	}

	if config.Addr == "" {
		return nil, fmt.Errorf("监听地址不能为空")
	}

	// Transport 是可选的（RPC Server 通过 TCP 处理请求，不需要 Transport）
	// 只有在需要发送 UDP 消息时才需要 Transport

	return &RPCServer{
		addr:      config.Addr,
		registry:  rpccommon.NewHandlerRegistry(),
		transport: config.Transport, // 可以为 nil
		stopCh:    make(chan struct{}),
		stats:     &ServerStats{},
	}, nil
}

// Start 启动 RPC Server
func (s *RPCServer) Start() error {
	var err error
	s.started.Do(func() {
		logging.WithFields(map[string]any{
			"addr": s.addr,
		}).Info("启动 RPC Server")

		// 创建监听器
		s.listener, err = net.Listen("tcp", s.addr)
		if err != nil {
			logging.WithField("error", err).Error("创建监听器失败")
			return
		}

		// 启动接收协程
		s.stopWg.Add(1)
		go s.acceptLoop()

		logging.WithField("listen_addr", s.listener.Addr()).Info("RPC Server 启动成功")
	})

	return err
}

// Stop 停止 RPC Server
func (s *RPCServer) Stop() error {
	s.stopped.Do(func() {
		logging.Info("停止 RPC Server...")

		// 关闭监听器
		if s.listener != nil {
			s.listener.Close()
		}

		// 关闭停止信号
		close(s.stopCh)

		// 等待所有协程退出
		s.stopWg.Wait()

		logging.WithFields(map[string]any{
			"total_connections":   s.stats.TotalConnections,
			"total_requests":      s.stats.TotalRequests,
			"successful_requests": s.stats.SuccessfulRequests,
			"failed_requests":     s.stats.FailedRequests,
		}).Info("RPC Server 统计")

		logging.Info("RPC Server 已停止")
	})

	return nil
}

// acceptLoop 接收连接循环
func (s *RPCServer) acceptLoop() {
	defer s.stopWg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				// 正常关闭
				return
			default:
				logging.WithField("error", err).Error("接受连接失败")
				continue
			}
		}

		s.stats.TotalConnections++
		s.stats.ActiveConnections++

		// 为每个连接启动一个处理协程
		s.stopWg.Add(1)
		go s.handleConnection(conn)
	}
}

// handleConnection 处理连接
func (s *RPCServer) handleConnection(conn net.Conn) {
	defer s.stopWg.Done()
	defer func() {
		conn.Close()
		s.stats.ActiveConnections--
	}()

	// 设置读写超时
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	logging.WithFields(map[string]any{
		"remote_addr": conn.RemoteAddr(),
	}).Debug("处理 RPC 连接")

	// 创建 Frame 读写器
	reader := transport.NewFrameReader(conn)
	writer := transport.NewFrameWriter(conn)

	// 循环读取并处理消息
	for {
		// 1. 接收 Frame
		frame, err := reader.ReadFrame()
		if err != nil {
			if IsConnectionClosedError(err) {
				logging.WithField("remote_addr", conn.RemoteAddr()).Debug("客户端关闭连接")
				return
			}
			logging.WithFields(map[string]any{
				"remote_addr": conn.RemoteAddr(),
				"error":       err,
			}).Error("读取 Frame 失败")
			return
		}

		s.stats.TotalRequests++

		// 2. 解析 MessageType
		msgType := frame.FixedHeader.MsgType
		logging.WithFields(map[string]any{
			"remote_addr": conn.RemoteAddr(),
			"msg_type":    msgType,
			"msg_seq":     frame.FixedHeader.MsgSeq,
		}).Debug("收到 RPC 请求")

		// 3. 获取 Handler（使用 msgType.String() 而不是 string(msgType)）
		handler, exists := s.registry.GetHandler(msgType.String())
		if !exists {
			logging.WithFields(map[string]any{
				"remote_addr": conn.RemoteAddr(),
				"msg_type":    msgType,
			}).Warning("未找到消息处理器")
			s.stats.FailedRequests++
			s.sendError(writer, frame, "未找到消息处理器")
			return
		}

		// 4. 调用 Handler 处理请求
		responsePayload, err := handler.Handle(frame.Data)
		if err != nil {
			logging.WithFields(map[string]any{
				"remote_addr": conn.RemoteAddr(),
				"msg_type":    msgType,
				"error":       err,
			}).Error("处理请求失败")
			s.stats.FailedRequests++
			s.sendError(writer, frame, err.Error())
			return
		}

		// 5. 发送响应
		if err := s.sendResponse(writer, frame, responsePayload); err != nil {
			logging.WithFields(map[string]any{
				"remote_addr": conn.RemoteAddr(),
				"error":       err,
			}).Error("发送响应失败")
			s.stats.FailedRequests++
			return
		}

		s.stats.SuccessfulRequests++

		// 更新超时时间
		conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
}

// RegisterHandler 注册 RPC 处理器
func (s *RPCServer) RegisterHandler(messageType string, handler rpccommon.Handler) error {
	return s.registry.RegisterHandler(messageType, handler)
}

// GetStats 获取统计信息
func (s *RPCServer) GetStats() *ServerStats {
	return s.stats
}

// ========================================
// 辅助函数
// ========================================

// sendError 发送错误响应
func (s *RPCServer) sendError(writer *transport.FrameWriter, reqFrame *transport.Frame, errMsg string) error {
	responseFrame := buildResponseFrame(reqFrame, types.MessageTypeNodePong, []byte(errMsg))
	return writer.WriteFrame(responseFrame)
}

// sendResponse 发送成功响应
func (s *RPCServer) sendResponse(writer *transport.FrameWriter, reqFrame *transport.Frame, payload []byte) error {
	responseFrame := buildResponseFrame(reqFrame, reqFrame.FixedHeader.MsgType, payload)
	return writer.WriteFrame(responseFrame)
}

// buildResponseFrame 构建响应帧（辅助函数）
func buildResponseFrame(reqFrame *transport.Frame, msgType types.MessageType, payload []byte) *transport.Frame {
	return transport.NewFrame(
		reqFrame.FixedHeader.NodeID,
		reqFrame.FixedHeader.MsgSeq,
		msgType,
		reqFrame.FixedHeader.CodecID,
		0,
		payload,
	).Finalize()
}

// IsConnectionClosedError 判断是否为连接关闭错误
func IsConnectionClosedError(err error) bool {
	if err == nil {
		return false
	}

	// 定义连接关闭的错误消息集合
	closedErrors := map[string]bool{
		"EOF":                              true,
		"use of closed network connection": true,
		"connection reset by peer":         true,
	}

	errStr := err.Error()
	return closedErrors[errStr]
}
