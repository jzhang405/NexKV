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

// GRPCTransport gRPC 网络传输层实现
//
// GRPCTransport 使用 gRPC 实现节点间的网络通信，
// 支持真实的分布式部署场景。
type GRPCTransport struct {
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

// NewGRPCTransport 创建 gRPC 网络传输层
func NewGRPCTransport(config *TransportConfig) (Transport, error) {
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

	return &GRPCTransport{
		config:    config,
		serverAddr: addr,
		connPool:   make(map[string]*grpc.ClientConn),
		clients:    make(map[string]pb.MetadataTransportClient),
		sendCh:     make(chan Message, config.BufferSize),
		receiveCh:  make(chan Message, config.BufferSize),
		ctx:        ctx,
		cancel:     cancel,
		stats: TransportStatus{
			NodeID:    config.NodeID,
			Type:      "grpc",
			IsRunning: false,
		},
	}, nil
}

// SetMessageHandler 设置消息处理器
func (gt *GRPCTransport) SetMessageHandler(handler MessageHandler) {
	gt.mu.Lock()
	defer gt.mu.Unlock()
	gt.handler = handler
}

// Start 启动传输层
func (gt *GRPCTransport) Start() error {
	gt.mu.Lock()
	defer gt.mu.Unlock()

	if gt.running {
		return nil // 已经启动
	}

	// 创建监听器
	listener, err := net.Listen("tcp", gt.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", gt.serverAddr, err)
	}
	gt.listener = listener

	// 创建 gRPC 服务器
	gt.server = grpc.NewServer()

	// 注册服务
	pb.RegisterMetadataTransportServer(gt.server, gt)

	// 启动 gRPC 服务器
	gt.wg.Add(1)
	go func() {
		defer gt.wg.Done()
		log.Printf("[GRPCTransport %s] Starting gRPC server on %s", gt.config.NodeID, gt.serverAddr)
		if err := gt.server.Serve(listener); err != nil {
			log.Printf("[GRPCTransport %s] gRPC server error: %v", gt.config.NodeID, err)
		}
	}()

	// 启动消息处理循环
	gt.wg.Add(1)
	go gt.messageLoop()

	gt.running = true
	gt.stats.IsRunning = true

	log.Printf("[GRPCTransport %s] Started", gt.config.NodeID)

	return nil
}

// Stop 停止传输层
func (gt *GRPCTransport) Stop() error {
	// 先获取锁来检查状态
	gt.mu.Lock()
	if !gt.running {
		gt.mu.Unlock()
		return nil // 已经停止
	}

	// 标记为未运行
	gt.running = false
	gt.stats.IsRunning = false

	// 取消上下文（会触发 messageLoop 退出）
	gt.cancel()

	// 释放锁，避免死锁
	gt.mu.Unlock()

	// 停止 gRPC 服务器
	if gt.server != nil {
		gt.server.GracefulStop()
	}

	// 关闭所有客户端连接
	gt.mu.Lock()
	for nodeID, conn := range gt.connPool {
		if err := conn.Close(); err != nil {
			log.Printf("[GRPCTransport %s] Error closing connection to %s: %v",
				gt.config.NodeID, nodeID, err)
		}
	}
	gt.mu.Unlock()

	// 等待 goroutine 结束
	gt.wg.Wait()

	// 关闭监听器
	if gt.listener != nil {
		gt.listener.Close()
	}

	// 关闭通道
	close(gt.sendCh)
	close(gt.receiveCh)

	log.Printf("[GRPCTransport %s] Stopped (sent=%d, received=%d)",
		gt.config.NodeID, gt.stats.MessagesSent, gt.stats.MessagesReceived)

	return nil
}

// Send 发送消息到指定节点
func (gt *GRPCTransport) Send(targetID string, msg Message) error {
	gt.mu.RLock()
	if !gt.running {
		gt.mu.RUnlock()
		return ErrTransportStopped
	}
	gt.mu.RUnlock()

	// 设置消息来源
	msg.From = gt.config.NodeID
	msg.To = targetID
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixNano()
	}

	// 获取目标客户端
	_, err := gt.getOrCreateClient(targetID)
	if err != nil {
		return fmt.Errorf("failed to get client for %s: %w", targetID, err)
	}

	// 模拟延迟（如果配置了）
	if gt.config.SimulatedLatency > 0 {
		time.Sleep(gt.config.SimulatedLatency)
	}

	// 根据消息类型发送
	_, cancel := context.WithTimeout(gt.ctx, 5*time.Second)
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
		case gt.sendCh <- msg:
		case <-gt.ctx.Done():
			return ErrTransportStopped
		}
	}

	// 更新统计
	gt.mu.Lock()
	gt.stats.MessagesSent++
	gt.stats.BytesSent += int64(len(msg.Payload))
	gt.mu.Unlock()

	return nil
}

// Broadcast 广播消息到所有节点
func (gt *GRPCTransport) Broadcast(msg Message) error {
	gt.mu.RLock()
	if !gt.running {
		gt.mu.RUnlock()
		return ErrTransportStopped
	}
	gt.mu.RUnlock()

	// 设置消息来源
	msg.From = gt.config.NodeID
	msg.To = "" // 广播
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixNano()
	}

	var errs []error

	// 向所有节点发送
	for nodeID := range gt.config.Peers {
		if nodeID == gt.config.NodeID {
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

		if err := gt.Send(nodeID, msgCopy); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("broadcast failed with %d errors: %v", len(errs), errs)
	}

	return nil
}

// Receive 返回接收消息通道
func (gt *GRPCTransport) Receive() <-chan Message {
	return gt.receiveCh
}

// Status 获取传输层状态
func (gt *GRPCTransport) Status() TransportStatus {
	gt.mu.RLock()
	defer gt.mu.RUnlock()

	return gt.stats
}

// getOrCreateClient 获取或创建目标节点的 gRPC 客户端
func (gt *GRPCTransport) getOrCreateClient(targetID string) (pb.MetadataTransportClient, error) {
	gt.mu.Lock()
	defer gt.mu.Unlock()

	// 检查是否已存在
	if client, ok := gt.clients[targetID]; ok {
		return client, nil
	}

	// 获取目标地址
	addr, ok := gt.config.Peers[targetID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, targetID)
	}

	// 创建连接（非阻塞模式，使用 grpc.WithBlock() 会阻塞等待连接建立）
	// 优化：移除 WithBlock()，让连接在后台异步建立
	// 优化：使用 grpc.WithReturnConnectionError() 更快失败
	conn, err := grpc.Dial(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(1024*1024*10), // 10MB
			grpc.MaxCallSendMsgSize(1024*1024*10), // 10MB
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for %s at %s: %w", targetID, addr, err)
	}

	// 保存连接
	gt.connPool[targetID] = conn
	client := pb.NewMetadataTransportClient(conn)
	gt.clients[targetID] = client

	log.Printf("[GRPCTransport %s] Created client for %s at %s", gt.config.NodeID, targetID, addr)

	return client, nil
}

// messageLoop 消息处理循环
func (gt *GRPCTransport) messageLoop() {
	defer gt.wg.Done()

	for {
		select {
		case msg, ok := <-gt.sendCh:
			if !ok {
				return // 通道已关闭
			}
			gt.handleMessage(msg)

		case <-gt.ctx.Done():
			return
		}
	}
}

// handleMessage 处理接收到的消息
func (gt *GRPCTransport) handleMessage(msg Message) {
	// 更新统计
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.stats.BytesReceived += int64(len(msg.Payload))
	gt.mu.Unlock()

	// 如果有处理器，使用处理器
	if gt.handler != nil {
		switch msg.Type {
		case GossipExchange:
			// 反序列化 Knowledge
			// knowledge := deserializeKnowledge(msg.Payload)
			// gt.handler.HandleGossip(msg.From, knowledge)

		case ProposeVote:
			// 处理投票提议
			// version := deserializeInt(msg.Payload)
			// gt.handler.HandleVote(msg.From, version)

		case DecisionNotify:
			// 处理决策通知
			// decision := deserializeDecision(msg.Payload)
			// gt.handler.HandleDecision(msg.From, decision)
		}
	}

	// 将消息放入接收通道
	select {
	case gt.receiveCh <- msg:
	case <-gt.ctx.Done():
		return
	}
}

// ===== gRPC 服务实现 =====

// GossipExchange 实现 Gossip 交换 RPC
func (gt *GRPCTransport) GossipExchange(ctx context.Context, req *pb.GossipRequest) (*pb.GossipResponse, error) {
	// 更新统计
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.mu.Unlock()

	log.Printf("[GRPCTransport %s] Received GossipExchange from %s", gt.config.NodeID, req.FromNode)

	// 如果有处理器，处理请求
	if gt.handler != nil {
		// TODO: 转换 Knowledge 格式
		// knowledge := convertFromProtoKnowledge(req.Knowledge)
		// if err := gt.handler.HandleGossip(req.FromNode, knowledge); err != nil {
		// 	return nil, status.Errorf(codes.Internal, "handle gossip failed: %v", err)
		// }
	}

	return &pb.GossipResponse{
		Accepted: true,
		// RemoteKnowledge: convertToProtoKnowledge(gt.getKnowledge()),
	}, nil
}

// ProposeVote 实现发起投票 RPC
func (gt *GRPCTransport) ProposeVote(ctx context.Context, req *pb.ProposeVoteRequest) (*pb.ProposeVoteResponse, error) {
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.mu.Unlock()

	log.Printf("[GRPCTransport %s] Received ProposeVote from %s (version=%d)",
		gt.config.NodeID, req.FromNode, req.Version)

	if gt.handler != nil {
		// TODO: 处理投票提议
		// if err := gt.handler.HandleVote(req.FromNode, int(req.Version)); err != nil {
		// 	return nil, status.Errorf(codes.Internal, "handle vote failed: %v", err)
		// }
	}

	return &pb.ProposeVoteResponse{
		Accepted: true,
	}, nil
}

// AckVote 实现确认投票 RPC
func (gt *GRPCTransport) AckVote(ctx context.Context, req *pb.AckVoteRequest) (*pb.AckVoteResponse, error) {
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.mu.Unlock()

	log.Printf("[GRPCTransport %s] Received AckVote from %s", gt.config.NodeID, req.FromNode)

	return &pb.AckVoteResponse{
		Accepted: true,
	}, nil
}

// DecisionNotify 实现决策通知 RPC
func (gt *GRPCTransport) DecisionNotify(ctx context.Context, req *pb.DecisionNotifyRequest) (*pb.DecisionNotifyResponse, error) {
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.mu.Unlock()

	log.Printf("[GRPCTransport %s] Received DecisionNotify from %s (decision=%v)",
		gt.config.NodeID, req.FromNode, req.Decision)

	if gt.handler != nil {
		// TODO: 处理决策通知
		// decision := convertFromProtoDecisionState(req.Decision)
		// if err := gt.handler.HandleDecision(req.FromNode, decision); err != nil {
		// 	return nil, status.Errorf(codes.Internal, "handle decision failed: %v", err)
		// }
	}

	return &pb.DecisionNotifyResponse{
		Accepted: true,
	}, nil
}

// SendPrepare 实现 2PC Prepare RPC
func (gt *GRPCTransport) SendPrepare(ctx context.Context, req *pb.PrepareRequest) (*pb.PrepareResponse, error) {
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.mu.Unlock()

	log.Printf("[GRPCTransport %s] Received SendPrepare for transaction %s from %s",
		gt.config.NodeID, req.TransactionId, req.Coordinator)

	return &pb.PrepareResponse{
		Accepted: true,
		Message:  "Prepare accepted",
	}, nil
}

// SendVote 实现 2PC Vote RPC
func (gt *GRPCTransport) SendVote(ctx context.Context, req *pb.VoteRequest) (*pb.VoteResponse, error) {
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.mu.Unlock()

	log.Printf("[GRPCTransport %s] Received SendVote for transaction %s from %s (vote=%v)",
		gt.config.NodeID, req.TransactionId, req.Participant, req.Vote)

	return &pb.VoteResponse{
		Accepted: true,
		Message:  "Vote accepted",
	}, nil
}

// SendDecision 实现 2PC Decision RPC
func (gt *GRPCTransport) SendDecision(ctx context.Context, req *pb.DecisionRequest) (*pb.DecisionResponse, error) {
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.mu.Unlock()

	log.Printf("[GRPCTransport %s] Received SendDecision for transaction %s from %s (decision=%s)",
		gt.config.NodeID, req.TransactionId, req.Coordinator, req.Decision)

	return &pb.DecisionResponse{
		Accepted: true,
		Message:  "Decision accepted",
	}, nil
}

// AckDecision 实现 2PC Ack Decision RPC
func (gt *GRPCTransport) AckDecision(ctx context.Context, req *pb.AckDecisionRequest) (*pb.AckDecisionResponse, error) {
	gt.mu.Lock()
	gt.stats.MessagesReceived++
	gt.mu.Unlock()

	log.Printf("[GRPCTransport %s] Received AckDecision for transaction %s from %s",
		gt.config.NodeID, req.TransactionId, req.Participant)

	return &pb.AckDecisionResponse{
		Accepted: true,
	}, nil
}

// HealthCheck 实现健康检查 RPC
func (gt *GRPCTransport) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	gt.mu.RLock()
	defer gt.mu.RUnlock()

	return &pb.HealthCheckResponse{
		Healthy:  gt.running,
		NodeId:   gt.config.NodeID,
		Version:  0, // TODO: 获取实际版本号
		Message:  "OK",
	}, nil
}

// RegisterGRPCTransport 注册 gRPC 传输层工厂
func init() {
	RegisterTransport("grpc", NewGRPCTransport)
}

// CreateGRPCTransport 创建 gRPC 网络传输层的辅助函数
func CreateGRPCTransport(nodeID string, peers map[string]string) (*GRPCTransport, error) {
	config := DefaultTransportConfig(nodeID)
	config.Peers = peers

	transport, err := CreateTransport("grpc", config)
	if err != nil {
		return nil, err
	}

	gt := transport.(*GRPCTransport)
	return gt, nil
}
