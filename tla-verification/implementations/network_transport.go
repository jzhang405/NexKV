package implementations

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/jzhang405/NexKV/proto"
)

// NetworkTransport 网络传输层实现
//
// NetworkTransport 使用 gRPC 实现节点间的网络通信，
// 支持真实的分布式部署场景。
type NetworkTransport struct {
	pb.UnimplementedMetadataTransportServer // 必须嵌入以实现 gRPC 服务
	mu    sync.RWMutex
	config *TransportConfig

	// gRPC 服务器
	server     *grpc.Server
	listener   net.Listener
	serverAddr string

	// gRPC 客户端连接池
	connPool   map[string]*grpc.ClientConn
	clients    map[string]pb.MetadataTransportClient

	// 消息通道
	sendCh    chan Message
	receiveCh chan Message

	// 状态
	running   bool
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	// 统计
	stats TransportStatus

	// 消息处理器（可选）
	handler MessageHandler
}

// NewNetworkTransport 创建网络传输层
func NewNetworkTransport(config *TransportConfig) (Transport, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// 验证配置
	if config.NodeID == "" {
		return nil, fmt.Errorf("node ID cannot be empty")
	}

	// 获取本节点地址
	addr, ok := config.Peers[config.NodeID]
	if !ok {
		return nil, fmt.Errorf("address not configured for node %s", config.NodeID)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &NetworkTransport{
		config:    config,
		serverAddr: addr,
		connPool:   make(map[string]*grpc.ClientConn),
		clients:    make(map[string]pb.MetadataTransportClient),
		sendCh:     make(chan Message, config.BufferSize),
		receiveCh:  make(chan Message, config.BufferSize),
		ctx:        ctx,
		cancel:     cancel,
		stats: TransportStatus{
			IsRunning: false,
		},
	}, nil
}

// SetMessageHandler 设置消息处理器
func (nt *NetworkTransport) SetMessageHandler(handler MessageHandler) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	nt.handler = handler
}

// Start 启动传输层
func (nt *NetworkTransport) Start() error {
	nt.mu.Lock()
	defer nt.mu.Unlock()

	if nt.running {
		return nil // 已经启动
	}

	// 创建监听器
	listener, err := net.Listen("tcp", nt.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", nt.serverAddr, err)
	}
	nt.listener = listener

	// 创建 gRPC 服务器
	nt.server = grpc.NewServer()

	// 注册服务
	pb.RegisterMetadataTransportServer(nt.server, nt)

	// 启动 gRPC 服务器
	nt.wg.Add(1)
	go func() {
		defer nt.wg.Done()
		log.Printf("[NetworkTransport %s] Starting gRPC server on %s", nt.config.NodeID, nt.serverAddr)
		if err := nt.server.Serve(listener); err != nil {
			log.Printf("[NetworkTransport %s] gRPC server error: %v", nt.config.NodeID, err)
		}
	}()

	// 启动消息处理循环
	nt.wg.Add(1)
	go nt.messageLoop()

	nt.running = true
	nt.stats.IsRunning = true

	log.Printf("[NetworkTransport %s] Started", nt.config.NodeID)

	return nil
}

// Stop 停止传输层
func (nt *NetworkTransport) Stop() error {
	// 先获取锁来检查状态
	nt.mu.Lock()
	if !nt.running {
		nt.mu.Unlock()
		return nil // 已经停止
	}

	// 标记为未运行
	nt.running = false
	nt.stats.IsRunning = false

	// 取消上下文（会触发 messageLoop 退出）
	nt.cancel()

	// 释放锁，避免死锁
	nt.mu.Unlock()

	// 停止 gRPC 服务器
	if nt.server != nil {
		nt.server.GracefulStop()
	}

	// 关闭所有客户端连接
	nt.mu.Lock()
	for nodeID, conn := range nt.connPool {
		if err := conn.Close(); err != nil {
			log.Printf("[NetworkTransport %s] Error closing connection to %s: %v",
				nt.config.NodeID, nodeID, err)
		}
	}
	nt.mu.Unlock()

	// 等待 goroutine 结束
	nt.wg.Wait()

	// 关闭监听器
	if nt.listener != nil {
		nt.listener.Close()
	}

	// 关闭通道
	close(nt.sendCh)
	close(nt.receiveCh)

	log.Printf("[NetworkTransport %s] Stopped (sent=%d, received=%d)",
		nt.config.NodeID, nt.stats.MessagesSent, nt.stats.MessagesReceived)

	return nil
}

// Send 发送消息到指定节点
func (nt *NetworkTransport) Send(targetID string, msg Message) error {
	nt.mu.RLock()
	if !nt.running {
		nt.mu.RUnlock()
		return ErrTransportStopped
	}
	nt.mu.RUnlock()

	// 设置消息来源
	msg.From = nt.config.NodeID
	msg.To = targetID
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixNano()
	}

	// 获取目标客户端
	_, err := nt.getOrCreateClient(targetID)
	if err != nil {
		return fmt.Errorf("failed to get client for %s: %w", targetID, err)
	}

	// 模拟延迟（如果配置了）
	if nt.config.SimulatedLatency > 0 {
		time.Sleep(nt.config.SimulatedLatency)
	}

	// 根据消息类型发送
	_, cancel := context.WithTimeout(nt.ctx, 5*time.Second)
	defer cancel()

	switch msg.Type {
	case GossipExchange:
		// TODO: 反序列化 Knowledge
		// knowledge := deserializeKnowledge(msg.Payload)
		// _, err := client.GossipExchange(ctx, &pb.GossipRequest{
		// 	FromNode:  msg.From,
		// 	Knowledge: knowledge,
		// 	Timestamp: msg.Timestamp,
		// })
		// if err != nil {
		// 	return fmt.Errorf("gossip exchange failed: %w", err)
		// }

	case ProposeVote:
		// TODO: 实现 ProposeVote RPC
		// _, err := client.ProposeVote(ctx, &pb.ProposeVoteRequest{
		// 	FromNode: msg.From,
		// 	Version:  int32(version),
		// 	Timestamp: msg.Timestamp,
		// })
		// if err != nil {
		// 	return fmt.Errorf("propose vote failed: %w", err)
		// }

	case PrepareRequest:
		// 2PC Prepare
		// TODO: 实现 Prepare RPC

	case VoteRequest:
		// 2PC Vote
		// TODO: 实现 Vote RPC

	case DecisionRequest:
		// 2PC Decision
		// TODO: 实现 Decision RPC

	default:
		// 其他消息通过通道发送
		select {
		case nt.sendCh <- msg:
		case <-nt.ctx.Done():
			return ErrTransportStopped
		}
	}

	// 更新统计
	nt.mu.Lock()
	nt.stats.MessagesSent++
	nt.stats.BytesSent += int64(len(msg.Payload))
	nt.mu.Unlock()

	return nil
}

// Broadcast 广播消息到所有节点
func (nt *NetworkTransport) Broadcast(msg Message) error {
	nt.mu.RLock()
	if !nt.running {
		nt.mu.RUnlock()
		return ErrTransportStopped
	}
	nt.mu.RUnlock()

	// 设置消息来源
	msg.From = nt.config.NodeID
	msg.To = "" // 广播
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixNano()
	}

	var errs []error

	// 向所有节点发送
	for nodeID := range nt.config.Peers {
		if nodeID == nt.config.NodeID {
			continue // 跳过自己
		}

		// 复制消息（每个接收者一份）
		msgCopy := Message{
			Type:      msg.Type,
			From:      msg.From,
			To:        nodeID,
			Timestamp: msg.Timestamp,
			Payload:   msg.Payload,
			Context:   msg.Context,
		}

		if err := nt.Send(nodeID, msgCopy); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("broadcast failed with %d errors: %v", len(errs), errs)
	}

	return nil
}

// Receive 返回接收消息通道
func (nt *NetworkTransport) Receive() <-chan Message {
	return nt.receiveCh
}

// Status 获取传输层状态
func (nt *NetworkTransport) Status() TransportStatus {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	return nt.stats
}

// getOrCreateClient 获取或创建目标节点的 gRPC 客户端
func (nt *NetworkTransport) getOrCreateClient(targetID string) (pb.MetadataTransportClient, error) {
	nt.mu.Lock()
	defer nt.mu.Unlock()

	// 检查是否已存在
	if client, ok := nt.clients[targetID]; ok {
		return client, nil
	}

	// 获取目标地址
	addr, ok := nt.config.Peers[targetID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, targetID)
	}

	// 创建连接（带超时）
	ctx, cancel := context.WithTimeout(nt.ctx, 2*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s at %s: %w", targetID, addr, err)
	}

	// 保存连接
	nt.connPool[targetID] = conn
	client := pb.NewMetadataTransportClient(conn)
	nt.clients[targetID] = client

	log.Printf("[NetworkTransport %s] Connected to %s at %s", nt.config.NodeID, targetID, addr)

	return client, nil
}

// messageLoop 消息处理循环
func (nt *NetworkTransport) messageLoop() {
	defer nt.wg.Done()

	for {
		select {
		case msg, ok := <-nt.sendCh:
			if !ok {
				return // 通道已关闭
			}
			nt.handleMessage(msg)

		case <-nt.ctx.Done():
			return
		}
	}
}

// handleMessage 处理接收到的消息
func (nt *NetworkTransport) handleMessage(msg Message) {
	// 更新统计
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.stats.BytesReceived += int64(len(msg.Payload))
	nt.mu.Unlock()

	// 如果有处理器，使用处理器
	if nt.handler != nil {
		switch msg.Type {
		case GossipExchange:
			// 反序列化 Knowledge
			// knowledge := deserializeKnowledge(msg.Payload)
			// nt.handler.HandleGossip(msg.From, knowledge)

		case ProposeVote:
			// 处理投票提议
			// version := deserializeInt(msg.Payload)
			// nt.handler.HandleVote(msg.From, version)

		case DecisionNotify:
			// 处理决策通知
			// decision := deserializeDecision(msg.Payload)
			// nt.handler.HandleDecision(msg.From, decision)
		}
	}

	// 将消息放入接收通道
	select {
	case nt.receiveCh <- msg:
	case <-nt.ctx.Done():
		return
	}
}

// ===== gRPC 服务实现 =====

// GossipExchange 实现 Gossip 交换 RPC
func (nt *NetworkTransport) GossipExchange(ctx context.Context, req *pb.GossipRequest) (*pb.GossipResponse, error) {
	// 更新统计
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.mu.Unlock()

	log.Printf("[NetworkTransport %s] Received GossipExchange from %s", nt.config.NodeID, req.FromNode)

	// 如果有处理器，处理请求
	if nt.handler != nil {
		// TODO: 转换 Knowledge 格式
		// knowledge := convertFromProtoKnowledge(req.Knowledge)
		// if err := nt.handler.HandleGossip(req.FromNode, knowledge); err != nil {
		// 	return nil, status.Errorf(codes.Internal, "handle gossip failed: %v", err)
		// }
	}

	return &pb.GossipResponse{
		Accepted: true,
		// RemoteKnowledge: convertToProtoKnowledge(nt.getKnowledge()),
	}, nil
}

// ProposeVote 实现发起投票 RPC
func (nt *NetworkTransport) ProposeVote(ctx context.Context, req *pb.ProposeVoteRequest) (*pb.ProposeVoteResponse, error) {
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.mu.Unlock()

	log.Printf("[NetworkTransport %s] Received ProposeVote from %s (version=%d)",
		nt.config.NodeID, req.FromNode, req.Version)

	if nt.handler != nil {
		// TODO: 处理投票提议
		// if err := nt.handler.HandleVote(req.FromNode, int(req.Version)); err != nil {
		// 	return nil, status.Errorf(codes.Internal, "handle vote failed: %v", err)
		// }
	}

	return &pb.ProposeVoteResponse{
		Accepted: true,
	}, nil
}

// AckVote 实现确认投票 RPC
func (nt *NetworkTransport) AckVote(ctx context.Context, req *pb.AckVoteRequest) (*pb.AckVoteResponse, error) {
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.mu.Unlock()

	log.Printf("[NetworkTransport %s] Received AckVote from %s", nt.config.NodeID, req.FromNode)

	return &pb.AckVoteResponse{
		Accepted: true,
	}, nil
}

// DecisionNotify 实现决策通知 RPC
func (nt *NetworkTransport) DecisionNotify(ctx context.Context, req *pb.DecisionNotifyRequest) (*pb.DecisionNotifyResponse, error) {
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.mu.Unlock()

	log.Printf("[NetworkTransport %s] Received DecisionNotify from %s (decision=%v)",
		nt.config.NodeID, req.FromNode, req.Decision)

	if nt.handler != nil {
		// TODO: 处理决策通知
		// decision := convertFromProtoDecisionState(req.Decision)
		// if err := nt.handler.HandleDecision(req.FromNode, decision); err != nil {
		// 	return nil, status.Errorf(codes.Internal, "handle decision failed: %v", err)
		// }
	}

	return &pb.DecisionNotifyResponse{
		Accepted: true,
	}, nil
}

// SendPrepare 实现 2PC Prepare RPC
func (nt *NetworkTransport) SendPrepare(ctx context.Context, req *pb.PrepareRequest) (*pb.PrepareResponse, error) {
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.mu.Unlock()

	log.Printf("[NetworkTransport %s] Received SendPrepare for transaction %s from %s",
		nt.config.NodeID, req.TransactionId, req.Coordinator)

	return &pb.PrepareResponse{
		Accepted: true,
		Message:  "Prepare accepted",
	}, nil
}

// SendVote 实现 2PC Vote RPC
func (nt *NetworkTransport) SendVote(ctx context.Context, req *pb.VoteRequest) (*pb.VoteResponse, error) {
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.mu.Unlock()

	log.Printf("[NetworkTransport %s] Received SendVote for transaction %s from %s (vote=%v)",
		nt.config.NodeID, req.TransactionId, req.Participant, req.Vote)

	return &pb.VoteResponse{
		Accepted: true,
		Message:  "Vote accepted",
	}, nil
}

// SendDecision 实现 2PC Decision RPC
func (nt *NetworkTransport) SendDecision(ctx context.Context, req *pb.DecisionRequest) (*pb.DecisionResponse, error) {
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.mu.Unlock()

	log.Printf("[NetworkTransport %s] Received SendDecision for transaction %s from %s (decision=%s)",
		nt.config.NodeID, req.TransactionId, req.Coordinator, req.Decision)

	return &pb.DecisionResponse{
		Accepted: true,
		Message:  "Decision accepted",
	}, nil
}

// AckDecision 实现 2PC Ack Decision RPC
func (nt *NetworkTransport) AckDecision(ctx context.Context, req *pb.AckDecisionRequest) (*pb.AckDecisionResponse, error) {
	nt.mu.Lock()
	nt.stats.MessagesReceived++
	nt.mu.Unlock()

	log.Printf("[NetworkTransport %s] Received AckDecision for transaction %s from %s",
		nt.config.NodeID, req.TransactionId, req.Participant)

	return &pb.AckDecisionResponse{
		Accepted: true,
	}, nil
}

// HealthCheck 实现健康检查 RPC
func (nt *NetworkTransport) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	return &pb.HealthCheckResponse{
		Healthy:  nt.running,
		NodeId:   nt.config.NodeID,
		Version:  0, // TODO: 获取实际版本号
		Message:  "OK",
	}, nil
}

// RegisterNetworkTransport 注册网络传输层工厂
func init() {
	RegisterTransport("network", NewNetworkTransport)
}

// CreateNetworkTransport 创建网络传输层的辅助函数
func CreateNetworkTransport(nodeID string, peers map[string]string) (*NetworkTransport, error) {
	config := DefaultTransportConfig(nodeID)
	config.Peers = peers

	transport, err := CreateTransport("network", config)
	if err != nil {
		return nil, err
	}

	nt := transport.(*NetworkTransport)
	return nt, nil
}
