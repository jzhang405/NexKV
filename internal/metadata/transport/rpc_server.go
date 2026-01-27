// Package transport RPC 服务端实现
//
// 支持双 Transport（TCP + UDP）：
//   - TCP：可靠传输，用于关键消息（Quorum、2PC、元数据操作）
//   - UDP：尽力而为，用于尽力型消息（Gossip、心跳）
//
// Dispatcher 集成：
//   - 使用 dispatcher 实现 fan-in 模式
//   - 多连接共享固定数量 worker
//   - 减少协程数量，降低上下文切换开销
package transport

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// RPC Server 结构体
// ========================================

// RPCServer RPC 服务端
//
// 核心功能：
//   - 双 Transport 支持（TCP + UDP）
//   - Dispatcher 消息分发（fan-in 模式）
//   - 请求-响应处理（通过 CorrelationID）
//   - 优雅关闭（等待处理完成）
type RPCServer struct {
	// 传输层
	tcpTransport Transport // TCP 传输（可靠）
	udpTransport Transport // UDP 传输（尽力而为）

	// 分发器
	dispatcher *Dispatcher

	// 请求处理器
	handler RPCHandler

	// 配置
	config *RPCServerConfig

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 状态
	running atomic.Bool
}

// RPCServerConfig 服务端配置
type RPCServerConfig struct {
	// WorkerCount worker 协程数量（默认：8）
	WorkerCount int

	// QueueSize 消息队列大小（默认：10000）
	QueueSize int

	// RequestTimeout 请求超时（默认：30s）
	RequestTimeout time.Duration

	// EnableMetrics 启用统计信息（默认：true）
	EnableMetrics bool
}

// DefaultRPCServerConfig 返回默认配置
func DefaultRPCServerConfig() *RPCServerConfig {
	return &RPCServerConfig{
		WorkerCount:    8,
		QueueSize:      10000,
		RequestTimeout: 30 * time.Second,
		EnableMetrics:  true,
	}
}

// ========================================
// RPC Handler 接口
// ========================================

// RPCHandler RPC 请求处理器接口
//
// 实现此接口来处理 RPC 请求
type RPCHandler interface {
	// HandleRequest 处理 RPC 请求
	//
	// 参数：
	//   - ctx: 请求上下文（支持取消和超时）
	//   - req: 请求消息
	//
	// 返回：
	//   - types.Message: 响应消息（nil 表示不返回响应）
	//   - error: 处理失败时返回错误
	HandleRequest(ctx context.Context, req types.Message) (types.Message, error)
}

// RPCHandlerFunc 函数式 RPC 处理器（便捷实现）
type RPCHandlerFunc func(ctx context.Context, req types.Message) (types.Message, error)

// HandleRequest 实现 RPCHandler 接口
func (f RPCHandlerFunc) HandleRequest(ctx context.Context, req types.Message) (types.Message, error) {
	return f(ctx, req)
}

// ========================================
// RPC Server 创建
// ========================================

// NewRPCServer 创建新的 RPC 服务端
//
// 参数：
//   - tcpTransport: TCP 传输层（必需，用于可靠消息）
//   - udpTransport: UDP 传输层（可选，用于尽力型消息）
//   - handler: RPC 请求处理器（必需）
//   - config: 服务端配置（nil 时使用默认配置）
//
// 返回：
//   - *RPCServer: RPC 服务端实例
//   - error: 配置无效时返回错误
func NewRPCServer(
	tcpTransport Transport,
	udpTransport Transport,
	handler RPCHandler,
	config *RPCServerConfig,
) (*RPCServer, error) {
	if tcpTransport == nil {
		return nil, types.NewRPCInvalidMessage("tcpTransport is required")
	}
	if handler == nil {
		return nil, types.NewRPCInvalidMessage("handler is required")
	}

	// 使用默认配置
	if config == nil {
		config = DefaultRPCServerConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	// 创建 dispatcher 配置
	dispatcherConfig := &DispatcherConfig{
		WorkerCount:   config.WorkerCount,
		QueueSize:     config.QueueSize,
		BatchSize:     32,
		FlushInterval: 10,
	}

	// 创建 dispatcher
	dispatcher, err := NewDispatcher(dispatcherConfig, &rpcServerHandlerAdapter{
		server: nil, // 稍后设置
	})
	if err != nil {
		cancel()
		return nil, types.NewRPCServerError("failed to create dispatcher", err)
	}

	s := &RPCServer{
		tcpTransport: tcpTransport,
		udpTransport: udpTransport,
		dispatcher:   dispatcher,
		handler:      handler,
		config:       config,
		ctx:          ctx,
		cancel:       cancel,
	}

	// 设置 dispatcher 的 server 引用
	dispatcher.handler.(*rpcServerHandlerAdapter).server = s

	return s, nil
}

// ========================================
// RPC Server 生命周期
// ========================================

// Start 启动服务端
//
// 启动 dispatcher 并注册 Transport 连接
func (s *RPCServer) Start() error {
	if !s.running.CompareAndSwap(false, true) {
		return types.NewRPCInvalidMessage("server already running")
	}

	logging.Infof("[RPC-Server] Starting RPC server (TCP+UDP)")

	// 启动 dispatcher
	if err := s.dispatcher.Start(); err != nil {
		s.running.Store(false)
		return types.NewRPCServerError("failed to start dispatcher", err)
	}

	// 注册 TCP Transport 连接
	tcpCh := s.tcpTransport.Receive()
	s.dispatcher.RegisterConnection("tcp", tcpCh)

	// 注册 UDP Transport 连接（如果有）
	if s.udpTransport != nil {
		udpCh := s.udpTransport.Receive()
		s.dispatcher.RegisterConnection("udp", udpCh)
	}

	logging.Infof("[RPC-Server] RPC Server started")
	return nil
}

// Stop 停止服务端
//
// 优雅关闭：
//  1. 停止接收新消息
//  2. 等待处理完成
//  3. 关闭 dispatcher
func (s *RPCServer) Stop() error {
	if !s.running.CompareAndSwap(true, false) {
		return types.NewRPCInvalidMessage("server not running")
	}

	logging.Infof("[RPC-Server] Stopping RPC server")

	// 停止 dispatcher
	if err := s.dispatcher.Stop(); err != nil {
		logging.Warnf("[RPC-Server] Failed to stop dispatcher: %v", err)
	}

	// 等待所有协程完成
	s.wg.Wait()

	s.cancel()

	logging.Infof("[RPC-Server] RPC Server stopped")
	return nil
}

// ========================================
// 统计信息
// ========================================

// ServerStats 服务端统计信息
type ServerStats struct {
	Running     bool   // 是否运行中
	Processed   uint64 // 已处理请求数
	Dropped     uint64 // 已丢弃请求数
	WorkerCount int    // worker 数量
	QueuedMsgs  int    // 队列中待处理消息数
}

// GetStats 获取统计信息
func (s *RPCServer) GetStats() ServerStats {
	stats := s.dispatcher.GetStats()
	return ServerStats{
		Running:     stats.Running,
		Processed:   stats.MsgCount,
		Dropped:     stats.DropCount,
		WorkerCount: stats.WorkerCount,
		QueuedMsgs:  stats.QueuedMsgs,
	}
}

// ========================================
// Dispatcher 适配器
// ========================================

// rpcServerHandlerAdapter 将 RPCHandler 适配为 Dispatcher.Handler
type rpcServerHandlerAdapter struct {
	server *RPCServer
}

// HandleMessage 实现 Dispatcher.Handler 接口
func (a *rpcServerHandlerAdapter) HandleMessage(ctx context.Context, msg MsgFrame) error {
	req, err := a.unmarshalRequest(msg)
	if err != nil {
		logging.Errorf("[RPC-Server] Failed to unmarshal request: %v", err)
		return err
	}

	if req.MsgRole() != types.MsgRoleRequest {
		return nil
	}

	resp, err := a.server.handler.HandleRequest(ctx, req)
	if err != nil {
		logging.Errorf("[RPC-Server] Failed to handle request: %v", err)
		return err
	}

	if resp != nil && req.ExpectResponse() == types.ExpectResponse {
		if err := a.sendResponse(msg, resp); err != nil {
			logging.Errorf("[RPC-Server] Failed to send response: %v", err)
			return err
		}
	}

	return nil
}

// unmarshalRequest 反序列化请求
func (a *rpcServerHandlerAdapter) unmarshalRequest(msgFrame MsgFrame) (types.Message, error) {
	return msgFrame.Message, nil
}

// sendResponse 发送响应到客户端
//
// P0-实现：使用 Transport.Reply() 方法自动处理 TCP/UDP 协议差异
func (a *rpcServerHandlerAdapter) sendResponse(reqFrame MsgFrame, resp types.Message) error {
	correlationID := reqFrame.CorrelationID()
	sourceAddr := reqFrame.SourceAddr

	nodeID, msgSeq := a.parseCorrelationID(correlationID)
	transport := a.selectTransportByProtocol(reqFrame.ProtocolType())
	connID := a.getConnID(reqFrame)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := transport.Reply(ctx, sourceAddr, resp, nodeID, msgSeq, connID); err != nil {
		return types.NewRPCNetworkError(sourceAddr, fmt.Errorf("failed to send response (CorrelationID: %s): %w", correlationID, err))
	}

	logging.Infof("[RPC-Server] Response sent via Reply() (CorrelationID: %s)", correlationID)
	return nil
}

// parseCorrelationID 解析 CorrelationID
func (a *rpcServerHandlerAdapter) parseCorrelationID(correlationID string) (nodeID, msgSeq uint64) {
	_, _ = fmt.Sscanf(correlationID, "%d:%d", &nodeID, &msgSeq)
	return nodeID, msgSeq
}

// selectTransportByProtocol 根据协议类型选择 transport
func (a *rpcServerHandlerAdapter) selectTransportByProtocol(protocolType types.ProtocolType) Transport {
	switch protocolType {
	case types.ProtocolTCP:
		return a.server.tcpTransport
	case types.ProtocolUDP:
		if a.server.udpTransport != nil {
			return a.server.udpTransport
		}
		return a.server.tcpTransport
	default:
		if a.server.tcpTransport != nil {
			return a.server.tcpTransport
		}
		return a.server.udpTransport
	}
}

// getConnID 获取连接 ID（仅 TCP 协议）
func (a *rpcServerHandlerAdapter) getConnID(reqFrame MsgFrame) string {
	if reqFrame.ProtocolType() == types.ProtocolTCP {
		return reqFrame.ConnID
	}
	return ""
}
