package implementations

import (
	"fmt"
	"sync"
)

// NullTransport 零开销传输层实现（Null Object 模式）
//
// 特点：
// - 最快性能：零拷贝，无序列化，无 channel，无网络开销
// - 直接调用：持有节点引用，直接调用节点方法（类似函数调用）
// - 适合：性能基线测试、算法验证
// - 限制：只支持 *Node 类型，无法跨进程通信
//
// 命名由来：Null Object 模式 - 表示"无传输层"的基线实现
type NullTransport struct {
	nodeID  string
	nodes   map[string]*Node           // 直接持有节点引用
	peers   map[string]string           // 节点ID -> 地址映射
	mu      sync.RWMutex
	started bool
}

// NewNullTransport 创建 Null Transport
func NewNullTransport(nodeID string, peers map[string]string) *NullTransport {
	return &NullTransport{
		nodeID: nodeID,
		nodes:  make(map[string]*Node),
		peers:  peers,
	}
}

// RegisterNode 注册节点到 Transport
// 注意：Null 需要直接持有节点引用才能进行方法调用
func (t *NullTransport) RegisterNode(node *Node) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.nodes[node.ID]; exists {
		return fmt.Errorf("node %s already registered", node.ID)
	}

	t.nodes[node.ID] = node
	return nil
}

// Start 启动 Transport
func (t *NullTransport) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return fmt.Errorf("transport already started")
	}

	t.started = true
	return nil
}

// Stop 停止 Transport
func (t *NullTransport) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return fmt.Errorf("transport not started")
	}

	t.started = false
	return nil
}

// Send 发送消息到指定节点
// Null 实现：直接调用目标节点的 HandleMessage 方法
func (t *NullTransport) Send(targetID string, msg Message) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.started {
		return ErrTransportNotStarted
	}

	// 查找目标节点
	target, exists := t.nodes[targetID]
	if !exists {
		return fmt.Errorf("target node %s not found", targetID)
	}

	// 直接调用目标节点的接收方法（无 channel，无序列化）
	// 这是最快的通信方式：直接的方法调用
	t.handleDirectMessage(target, msg)

	return nil
}

// Broadcast 广播消息到所有节点
func (t *NullTransport) Broadcast(msg Message) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.started {
		return ErrTransportNotStarted
	}

	var lastErr error
	for targetID := range t.peers {
		if targetID == t.nodeID {
			continue // 跳过自己
		}

		target, exists := t.nodes[targetID]
		if !exists {
			continue // 节点未注册，跳过
		}

		// 直接调用每个节点
		t.handleDirectMessage(target, msg)
		lastErr = nil
	}

	return lastErr
}

// Receive 返回接收消息的 channel
// Null 实现：由于是同步调用，不需要 channel
// 返回一个只读的 nil channel，表示不通过 channel 接收
func (t *NullTransport) Receive() <-chan Message {
	return nil
}

// Status 返回 Transport 状态
func (t *NullTransport) Status() TransportStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return TransportStatus{
		NodeID:    t.nodeID,
		IsRunning: t.started,
		Type:      "null",
		Peers:     t.peers,
	}
}

// handleDirectMessage 直接处理消息（内部方法）
// 根据消息类型调用目标节点的相应方法
func (t *NullTransport) handleDirectMessage(target *Node, msg Message) {
	target.mu.Lock()
	defer target.mu.Unlock()

	// 检查目标节点是否崩溃
	if target.IsCrashed {
		return // 崩溃的节点不处理消息
	}

	// 根据消息类型分发
	// 注意：Null 模式下，我们直接操作内存结构
	// Payload 应该是序列化的数据，这里需要先反序列化
	// 为了简化，暂时只处理 GossipExchange 类型的消息

	switch msg.Type {
	case GossipExchange:
		// Gossip 消息：合并 Knowledge
		// 注意：这里需要实现序列化/反序列化
		// 暂时简化处理：Null 模式主要用于测试，可以假设 Payload 是预处理的
		// 实际使用时，应该通过 RegisterNode 注册的节点进行同步

		// 暂时不做处理，等待重构 Node 结构

	case Heartbeat:
		// 心跳消息：在 Null 模式下，节点直接访问，无需心跳

	default:
		// 其他消息类型暂不处理
	}
}

// GetNode 获取已注册的节点
func (t *NullTransport) GetNode(nodeID string) (*Node, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	node, exists := t.nodes[nodeID]
	return node, exists
}
