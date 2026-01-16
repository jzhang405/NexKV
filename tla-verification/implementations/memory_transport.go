package implementations

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// MemoryTransport 内存传输层实现
//
// MemoryTransport 通过直接内存访问实现节点间通信，
// 封装了现有的 GossipExchange 逻辑。
// 适用于快速测试和算法验证。
type MemoryTransport struct {
	mu    sync.RWMutex
	config *TransportConfig

	// 集群引用（用于访问其他节点）
	cluster *Cluster

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

// NewMemoryTransport 创建内存传输层
func NewMemoryTransport(config *TransportConfig) (Transport, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &MemoryTransport{
		config:    config,
		sendCh:    make(chan Message, config.BufferSize),
		receiveCh: make(chan Message, config.BufferSize),
		ctx:       ctx,
		cancel:    cancel,
		stats: TransportStatus{
			NodeID:    config.NodeID,
			Type:      "memory",
			IsRunning: false,
		},
	}, nil
}

// SetCluster 设置集群引用
//
// 必须在 Start() 之前调用。
func (mt *MemoryTransport) SetCluster(cluster *Cluster) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.cluster = cluster
}

// SetMessageHandler 设置消息处理器
func (mt *MemoryTransport) SetMessageHandler(handler MessageHandler) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.handler = handler
}

// Start 启动传输层
func (mt *MemoryTransport) Start() error {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	if mt.running {
		return nil // 已经启动
	}

	if mt.cluster == nil {
		return fmt.Errorf("cluster not set, call SetCluster() first")
	}

	mt.running = true
	mt.stats.IsRunning = true

	// 启动消息处理循环
	mt.wg.Add(1)
	go mt.messageLoop()

	log.Printf("[MemoryTransport %s] Started", mt.config.NodeID)

	return nil
}

// Stop 停止传输层
func (mt *MemoryTransport) Stop() error {
	// 先获取锁来检查状态
	mt.mu.Lock()
	if !mt.running {
		mt.mu.Unlock()
		return nil // 已经停止
	}

	// 标记为未运行
	mt.running = false
	mt.stats.IsRunning = false

	// 取消上下文（会触发 messageLoop 退出）
	mt.cancel()

	// 释放锁，避免死锁（messageLoop 中的 handleMessage 需要获取锁）
	mt.mu.Unlock()

	// 等待 goroutine 结束
	mt.wg.Wait()

	// 关闭通道
	close(mt.sendCh)
	close(mt.receiveCh)

	log.Printf("[MemoryTransport %s] Stopped (sent=%d, received=%d)",
		mt.config.NodeID, mt.stats.MessagesSent, mt.stats.MessagesReceived)

	return nil
}

// Send 发送消息到指定节点
func (mt *MemoryTransport) Send(targetID string, msg Message) error {
	mt.mu.RLock()
	if !mt.running {
		mt.mu.RUnlock()
		return ErrTransportStopped
	}
	mt.mu.RUnlock()

	// 设置消息来源
	msg.From = mt.config.NodeID
	msg.To = targetID
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixNano()
	}

	// 模拟延迟（如果配置了）
	if mt.config.SimulatedLatency > 0 {
		time.Sleep(mt.config.SimulatedLatency)
	}

	// 获取目标节点
	target := mt.cluster.GetNode(targetID)
	if target == nil {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, targetID)
	}

	// 检查网络分区
	mt.cluster.mu.RLock()
	canCommunicate := mt.checkNetworkPartition(targetID)
	mt.cluster.mu.RUnlock()

	if !canCommunicate {
		return fmt.Errorf("nodes %s and %s are in different partitions",
			mt.config.NodeID, targetID)
	}

	// 根据消息类型处理
	switch msg.Type {
	case GossipExchange:
		// 直接调用 GossipExchange
		sourceNode := mt.cluster.GetNode(mt.config.NodeID)
		if err := sourceNode.GossipExchange(target, mt.cluster); err != nil {
			return fmt.Errorf("gossip exchange failed: %w", err)
		}

	default:
		// 其他消息通过通道发送
		select {
		case mt.sendCh <- msg:
		case <-mt.ctx.Done():
			return ErrTransportStopped
		}
	}

	// 更新统计
	mt.mu.Lock()
	mt.stats.MessagesSent++
	mt.stats.BytesSent += int64(len(msg.Payload))
	mt.mu.Unlock()

	return nil
}

// Broadcast 广播消息到所有节点
func (mt *MemoryTransport) Broadcast(msg Message) error {
	mt.mu.RLock()
	if !mt.running {
		mt.mu.RUnlock()
		return ErrTransportStopped
	}
	mt.mu.RUnlock()

	// 设置消息来源
	msg.From = mt.config.NodeID
	msg.To = "" // 广播
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixNano()
	}

	var errs []error

	// 向所有节点发送
	for _, node := range mt.cluster.Nodes {
		if node.ID == mt.config.NodeID {
			continue // 跳过自己
		}

		// 复制消息（每个接收者一份）
		msgCopy := Message{
			Type:      msg.Type,
			From:      msg.From,
			To:        node.ID,
			Timestamp: msg.Timestamp,
			Payload:   msg.Payload,
			Context:   msg.Context,
		}

		if err := mt.Send(node.ID, msgCopy); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("broadcast failed with %d errors: %v", len(errs), errs)
	}

	return nil
}

// Receive 返回接收消息通道
func (mt *MemoryTransport) Receive() <-chan Message {
	return mt.receiveCh
}

// Status 获取传输层状态
func (mt *MemoryTransport) Status() TransportStatus {
	mt.mu.RLock()
	defer mt.mu.RUnlock()

	return mt.stats
}

// checkNetworkPartition 检查是否可以与目标节点通信
func (mt *MemoryTransport) checkNetworkPartition(targetID string) bool {
	// 正常网络
	if mt.cluster.NetworkStatus == "normal" {
		return true
	}

	// 分区网络：检查是否在同一分区
	nPartition, ok1 := mt.cluster.PartitionMap[mt.config.NodeID]
	targetPartition, ok2 := mt.cluster.PartitionMap[targetID]

	if ok1 && ok2 && nPartition == targetPartition {
		return true
	}

	return false
}

// messageLoop 消息处理循环
func (mt *MemoryTransport) messageLoop() {
	defer mt.wg.Done()

	for {
		select {
		case msg, ok := <-mt.sendCh:
			if !ok {
				return // 通道已关闭
			}
			mt.handleMessage(msg)

		case <-mt.ctx.Done():
			return
		}
	}
}

// handleMessage 处理接收到的消息
func (mt *MemoryTransport) handleMessage(msg Message) {
	// 更新统计
	mt.mu.Lock()
	mt.stats.MessagesReceived++
	mt.stats.BytesReceived += int64(len(msg.Payload))
	mt.mu.Unlock()

	// 如果有处理器，使用处理器
	if mt.handler != nil {
		switch msg.Type {
		case GossipExchange:
			// 反序列化 Knowledge
			// （实际实现中需要序列化/反序列化）
			// knowledge := deserializeKnowledge(msg.Payload)
			// mt.handler.HandleGossip(msg.From, knowledge)

		case ProposeVote:
			// 处理投票提议
			// version := deserializeInt(msg.Payload)
			// mt.handler.HandleVote(msg.From, version)

		case DecisionNotify:
			// 处理决策通知
			// decision := deserializeDecision(msg.Payload)
			// mt.handler.HandleDecision(msg.From, decision)
		}
	}

	// 将消息放入接收通道
	select {
	case mt.receiveCh <- msg:
	case <-mt.ctx.Done():
		return
	}
}

// RegisterMemoryTransport 注册内存传输层工厂
func init() {
	RegisterTransport("memory", NewMemoryTransport)
}

// CreateMemoryTransport 创建内存传输层的辅助函数
func CreateMemoryTransport(nodeID string, cluster *Cluster) (*MemoryTransport, error) {
	config := DefaultTransportConfig(nodeID)
	transport, err := CreateTransport("memory", config)
	if err != nil {
		return nil, err
	}

	mt := transport.(*MemoryTransport)
	mt.SetCluster(cluster)

	return mt, nil
}
