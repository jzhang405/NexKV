package implementations

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// TransportNodeAdapter Transport 到 Node 的适配器
//
// 这个适配器连接了 Transport 层和业务层（Node/TwoPhaseCommitNode）
// 负责：
// 1. 从 Transport 接收消息
// 2. 根据消息类型分发到对应的处理方法
// 3. 支持多种业务节点类型（Node, TwoPhaseCommitNode）
type TransportNodeAdapter struct {
	node       interface{}           // 业务节点（Node 或 TwoPhaseCommitNode）
	transport  Transport             // 传输层
	stopCh     chan struct{}         // 停止信号
	wg         sync.WaitGroup        // 等待组
	mu         sync.RWMutex          // 保护状态
	running    bool                  // 是否正在运行
}

// NewTransportNodeAdapter 创建适配器
func NewTransportNodeAdapter(node interface{}, transport Transport) *TransportNodeAdapter {
	return &TransportNodeAdapter{
		node:      node,
		transport: transport,
		stopCh:    make(chan struct{}),
	}
}

// Start 启动适配器
func (a *TransportNodeAdapter) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.running {
		return fmt.Errorf("adapter already running")
	}

	// 启动 Transport
	if err := a.transport.Start(); err != nil {
		return fmt.Errorf("failed to start transport: %w", err)
	}

	// 启动消息接收循环
	a.wg.Add(1)
	go a.receiveLoop()

	a.running = true
	return nil
}

// Stop 停止适配器
func (a *TransportNodeAdapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}

	// 发送停止信号
	close(a.stopCh)

	// 等待接收循环结束
	a.wg.Wait()

	// 停止 Transport
	if err := a.transport.Stop(); err != nil {
		return fmt.Errorf("failed to stop transport: %w", err)
	}

	a.running = false
	return nil
}

// receiveLoop 消息接收循环
func (a *TransportNodeAdapter) receiveLoop() {
	defer a.wg.Done()

	// 从 Transport 的 channel 接收消息
	msgCh := a.transport.Receive()

	if msgCh == nil {
		// Null 模式：没有 channel
		// 这种情况下，消息会直接通过 handleDirectMessage 处理
		log.Printf("[TransportAdapter] No message channel (Null mode)")
		return
	}

	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				// channel 关闭
				return
			}

			// 处理接收到的消息
			if err := a.handleMessage(msg); err != nil {
				log.Printf("[TransportAdapter] Error handling message: %v", err)
			}

		case <-a.stopCh:
			// 收到停止信号
			return
		}
	}
}

// handleMessage 处理接收到的消息
func (a *TransportNodeAdapter) handleMessage(msg Message) error {
	// 根据业务节点类型分发
	switch node := a.node.(type) {
	case *Node:
		return a.handleForQuorumNode(node, msg)

	case *TwoPhaseCommitNode:
		return a.handleForTwoPhaseCommitNode(node, msg)

	default:
		return fmt.Errorf("unsupported node type: %T", a.node)
	}
}

// handleForQuorumNode 处理 QuorumNode 的消息
func (a *TransportNodeAdapter) handleForQuorumNode(node *Node, msg Message) error {
	switch msg.Type {
	case GossipExchange:
		// Gossip 消息 - 暂不处理，因为 Node 不需要通过消息接口处理 Gossip
		// Node 直接通过 GossipExchange 方法进行交互
		return nil

	case Heartbeat:
		// 心跳消息（暂不处理）
		return nil

	default:
		log.Printf("[TransportAdapter] Unsupported message type for QuorumNode: %v", msg.Type)
		return nil
	}
}

// handleForTwoPhaseCommitNode 处理 TwoPhaseCommitNode 的消息
func (a *TransportNodeAdapter) handleForTwoPhaseCommitNode(node *TwoPhaseCommitNode, msg Message) error {
	switch msg.Type {
	case PrepareRequest, PrepareResponse, VoteRequest, VoteResponse, DecisionRequest, DecisionAck:
		// 所有 2PC 相关消息 - 暂不处理，因为 TwoPhaseCommitNode 不需要通过消息接口处理事务
		// TwoPhaseCommitNode 直接通过 GossipSync 方法进行交互
		return nil

	default:
		log.Printf("[TransportAdapter] Unsupported message type for TwoPhaseCommitNode: %v", msg.Type)
		return nil
	}
}

// SendThroughTransport 通过 Transport 发送消息（辅助方法）
func (a *TransportNodeAdapter) SendThroughTransport(targetID string, msgType MessageType, payload []byte) error {
	if !a.transport.Status().IsRunning {
		return ErrTransportNotStarted
	}

	msg := Message{
		Type:    msgType,
		From:    a.transport.Status().NodeID,
		To:      targetID,
		Payload: payload,
		Context: context.Background(),
	}

	return a.transport.Send(targetID, msg)
}

// BroadcastThroughTransport 通过 Transport 广播消息（辅助方法）
func (a *TransportNodeAdapter) BroadcastThroughTransport(msgType MessageType, payload []byte) error {
	if !a.transport.Status().IsRunning {
		return ErrTransportNotStarted
	}

	msg := Message{
		Type:    msgType,
		From:    a.transport.Status().NodeID,
		Payload: payload,
		Context: context.Background(),
	}

	return a.transport.Broadcast(msg)
}
