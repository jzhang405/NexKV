// Package transport 内存传输实现（测试用）
//
// 核心特性:
//   - 零网络依赖
//   - 使用通道进行节点间通信
//   - 单机多节点模拟
//   - 适合单元测试和集成测试
package transport

import (
	"context"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// 全局接收通道注册表
// 用于在不同 MemoryTransport 实例之间共享接收通道
var (
	globalReceiveRegistry = make(map[string]chan Message)
	globalRegistryMu      sync.RWMutex
)

// MemoryTransport 内存传输实现
//
// 用于测试的传输层实现，不依赖网络
type MemoryTransport struct {
	// 配置
	config *TransportConfig
	codec  Codec

	// 节点注册表
	nodes   map[string]*memoryNode // addr -> node
	nodesMu sync.RWMutex

	// 通道映射
	channels   map[string]chan Message // addr -> receive channel
	channelsMu sync.RWMutex

	// 已断开连接的节点（本地黑名单）
	disconnectedNodes   map[string]bool
	disconnectedNodesMu sync.RWMutex

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
	stopCh  chan struct{}
	stopWg  sync.WaitGroup

	// 本地节点地址
	localAddr string
}

// memoryNode 内存节点
//
// 表示一个虚拟节点
type memoryNode struct {
	addr      string
	sendCh    chan Message
	recvCh    chan Message
	closeOnce sync.Once
	closeCh   chan struct{}
	closed    atomic.Bool
}

// NewMemoryTransport 创建内存传输
func NewMemoryTransport(localAddr string) (*MemoryTransport, error) {
	t := &MemoryTransport{
		config:            DefaultTransportConfig(),
		codec:             NewMessagePackCodec(),
		nodes:             make(map[string]*memoryNode),
		channels:          make(map[string]chan Message),
		disconnectedNodes: make(map[string]bool),
		stopCh:            make(chan struct{}),
		localAddr:         localAddr,
	}

	// 注册本地节点
	t.registerNode(localAddr)

	return t, nil
}

// Start 启动传输层
func (t *MemoryTransport) Start() error {
	if !t.started.CompareAndSwap(false, true) {
		return types.NewTransportStateError("已经启动")
	}

	logging.Infof("启动内存传输层，地址: %s", t.localAddr)

	// 获取接收通道
	t.channelsMu.Lock()
	ch, exists := t.channels[t.localAddr]
	if !exists {
		ch = make(chan Message, 1024)
		t.channels[t.localAddr] = ch
	}
	t.channelsMu.Unlock()

	// 注册接收通道到全局注册表
	globalRegistryMu.Lock()
	globalReceiveRegistry[t.localAddr] = ch
	globalRegistryMu.Unlock()

	logging.Infof("已注册接收通道到全局注册表: %s", t.localAddr)

	// 启动接收协程
	t.stopWg.Add(1)
	go t.receiveLoop()

	logging.Infof("内存传输层启动成功，地址: %s", t.localAddr)
	return nil
}

// Stop 停止传输层
func (t *MemoryTransport) Stop() error {
	if !t.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	logging.Info("停止内存传输层...")

	// 从全局注册表注销接收通道
	globalRegistryMu.Lock()
	delete(globalReceiveRegistry, t.localAddr)
	globalRegistryMu.Unlock()

	logging.Infof("已从全局注册表注销: %s", t.localAddr)

	// 关闭停止信号
	close(t.stopCh)

	// 等待所有协程退出
	t.stopWg.Wait()

	// 关闭所有节点连接
	t.closeAllNodes()

	// 关闭接收通道
	t.channelsMu.Lock()
	for addr, ch := range t.channels {
		close(ch)
		delete(t.channels, addr)
	}
	t.channelsMu.Unlock()

	logging.Info("内存传输层已停止")
	return nil
}

// Close 关闭传输层
func (t *MemoryTransport) Close() error {
	return t.Stop()
}

// Send 发送消息到指定节点
func (t *MemoryTransport) Send(ctx context.Context, addr string, msg Message) error {
	if !t.started.Load() {
		return types.NewTransportStateError("未启动")
	}

	// 检查目标节点是否在断开连接黑名单中
	t.disconnectedNodesMu.RLock()
	_, disconnected := t.disconnectedNodes[addr]
	t.disconnectedNodesMu.RUnlock()

	if disconnected {
		return types.NewTransportConnectionError("目标节点不存在", addr, nil)
	}

	// 从全局注册表查找目标节点的接收通道
	globalRegistryMu.RLock()
	targetRecvCh, exists := globalReceiveRegistry[addr]
	globalRegistryMu.RUnlock()

	if !exists {
		return types.NewTransportConnectionError("目标节点不存在", addr, nil)
	}

	if targetRecvCh == nil {
		return types.NewTransportStateError("目标节点的接收通道未初始化: " + addr)
	}

	// 直接写入目标节点的接收通道
	select {
	case targetRecvCh <- msg:
		logging.Infof("发送消息: %s to %s (直接写入全局接收通道)", msg.Type(), addr)
		return nil
	case <-time.After(5 * time.Second):
		return types.NewTransportTimeoutError("发送")
	case <-ctx.Done():
		return ctx.Err()
	case <-t.stopCh:
		return types.NewTransportStateError("已停止")
	}
}

// Receive 返回接收消息的通道
func (t *MemoryTransport) Receive() <-chan Message {
	t.channelsMu.Lock()
	defer t.channelsMu.Unlock()

	ch, exists := t.channels[t.localAddr]
	if !exists {
		ch = make(chan Message, 1024)
		t.channels[t.localAddr] = ch
	}

	return ch
}

// registerNode 注册节点
func (t *MemoryTransport) registerNode(addr string) *memoryNode {
	t.nodesMu.Lock()
	defer t.nodesMu.Unlock()

	node, exists := t.nodes[addr]
	if exists {
		return node
	}

	node = &memoryNode{
		addr:    addr,
		sendCh:  make(chan Message, 1024),
		recvCh:  make(chan Message, 1024),
		closeCh: make(chan struct{}),
	}

	t.nodes[addr] = node

	// 创建接收通道
	t.channelsMu.Lock()
	if _, exists := t.channels[addr]; !exists {
		t.channels[addr] = make(chan Message, 1024)
	}
	t.channelsMu.Unlock()

	logging.Infof("注册内存节点: %s", addr)
	return node
}

// receiveLoop 接收循环
func (t *MemoryTransport) receiveLoop() {
	defer t.stopWg.Done()

	t.nodesMu.RLock()
	node, exists := t.nodes[t.localAddr]
	t.nodesMu.RUnlock()

	if !exists {
		logging.Errorf("本地节点不存在: %s", t.localAddr)
		return
	}

	logging.Infof("开始接收消息: %s", t.localAddr)

	for {
		select {
		case <-t.stopCh:
			logging.Infof("接收循环停止: %s", t.localAddr)
			return

		case msg, ok := <-node.recvCh:
			if !ok {
				logging.Infof("接收通道已关闭: %s", t.localAddr)
				return
			}

			// 转发到接收通道
			t.channelsMu.RLock()
			recvCh, exists := t.channels[t.localAddr]
			t.channelsMu.RUnlock()

			if exists {
				select {
				case recvCh <- msg:
					logging.Infof("接收消息: %s from %s", msg.Type(), t.localAddr)
				case <-time.After(1 * time.Second):
					logging.Errorf("接收通道阻塞，消息丢弃")
				}
			}

		case <-node.closeCh:
			logging.Infof("节点已关闭: %s", t.localAddr)
			return
		}
	}
}

// closeAllNodes 关闭所有节点
func (t *MemoryTransport) closeAllNodes() {
	t.nodesMu.Lock()
	defer t.nodesMu.Unlock()

	for addr, node := range t.nodes {
		if addr == t.localAddr {
			continue // 跳过本地节点
		}
		_ = node.Close()
	}
}

// RegisterRemoteNode 注册远程节点
//
// 用于在测试中手动注册其他节点
func (t *MemoryTransport) RegisterRemoteNode(addr string) {
	t.registerNode(addr)

	// 从断开连接黑名单中移除
	t.disconnectedNodesMu.Lock()
	delete(t.disconnectedNodes, addr)
	t.disconnectedNodesMu.Unlock()

	logging.Infof("RegisterRemoteNode: %s 注册远程节点 %s", t.localAddr, addr)

	// 建立双向连接
	t.nodesMu.RLock()
	localNode := t.nodes[t.localAddr]
	remoteNode := t.nodes[addr]
	t.nodesMu.RUnlock()

	if localNode != nil && remoteNode != nil {
		// 启动转发协程
		t.stopWg.Add(2)
		go t.forwardMessages(localNode, remoteNode)
		go t.forwardMessages(remoteNode, localNode)
		logging.Infof("RegisterRemoteNode: %s 已启动与 %s 的双向转发", t.localAddr, addr)
	} else {
		logging.Errorf("RegisterRemoteNode: %s 无法启动与 %s 的转发 (localNode=%v, remoteNode=%v)",
			t.localAddr, addr, localNode != nil, remoteNode != nil)
	}
}

// forwardMessages 转发消息
//
// 在两个节点之间转发消息
// 这个协程从 'to' 节点的 sendCh 读取（Send 写入本地节点的 sendCh，forwardMessages 需要从目标节点的 sendCh 读取）
func (t *MemoryTransport) forwardMessages(from, to *memoryNode) {
	defer t.stopWg.Done()

	logging.Infof("启动转发协程: %s.sendCh -> %s.recvCh", from.addr, to.addr)

	for {
		select {
		case <-t.stopCh:
			return
		case <-from.closeCh:
			return
		case msg, ok := <-to.sendCh:
			if !ok {
				return
			}

			logging.Infof("转发消息: %s from %s.sendCh -> %s.recvCh", msg.Type(), to.addr, to.addr)
			select {
			case to.recvCh <- msg:
				logging.Infof("消息已转发到 %s.recvCh", to.addr)
			case <-time.After(1 * time.Second):
				logging.Errorf("转发超时: %s -> %s", from.addr, to.addr)
			}
		}
	}
}

// ========================================
// memoryConn 内存连接
//
// 实现 Conn 接口，用于 FrameReader/Writer
// ========================================

// ========================================
// memoryNode 方法
// ========================================

// Close 关闭节点
func (n *memoryNode) Close() error {
	n.closeOnce.Do(func() {
		n.closed.Store(true)
		close(n.closeCh)
		close(n.sendCh)
		close(n.recvCh)
	})
	return nil
}

// ========================================
// 辅助方法
// ========================================

// GetLocalAddr 获取本地地址
func (t *MemoryTransport) GetLocalAddr() string {
	return t.localAddr
}

// GetConfig 获取配置
func (t *MemoryTransport) GetConfig() *TransportConfig {
	return t.config
}

// Stats 获取统计信息
func (t *MemoryTransport) Stats() map[string]any {
	t.nodesMu.RLock()
	defer t.nodesMu.RUnlock()

	stats := make(map[string]any)
	stats["started"] = t.started.Load()
	stats["stopped"] = t.stopped.Load()
	stats["local_addr"] = t.localAddr
	stats["registered_nodes"] = len(t.nodes)

	return stats
}

// ConnectTo 连接到远程节点
//
// 在测试中用于建立节点间连接
func (t *MemoryTransport) ConnectTo(remoteAddr string) error {
	t.nodesMu.RLock()
	_, localExists := t.nodes[t.localAddr]
	_, remoteExists := t.nodes[remoteAddr]
	t.nodesMu.RUnlock()

	if !localExists {
		return types.NewTransportConnectionError("本地节点不存在", t.localAddr, nil)
	}

	if !remoteExists {
		t.RegisterRemoteNode(remoteAddr)
	}

	// 从断开连接黑名单中移除
	t.disconnectedNodesMu.Lock()
	delete(t.disconnectedNodes, remoteAddr)
	t.disconnectedNodesMu.Unlock()

	logging.Infof("建立连接: %s -> %s", t.localAddr, remoteAddr)
	return nil
}

// DisconnectFrom 断开与远程节点的连接
func (t *MemoryTransport) DisconnectFrom(remoteAddr string) error {
	t.nodesMu.Lock()
	defer t.nodesMu.Unlock()

	node, exists := t.nodes[remoteAddr]
	if !exists {
		return types.NewTransportConnectionError("节点不存在", remoteAddr, nil)
	}

	_ = node.Close()
	delete(t.nodes, remoteAddr)

	// 添加到断开连接黑名单
	t.disconnectedNodesMu.Lock()
	t.disconnectedNodes[remoteAddr] = true
	t.disconnectedNodesMu.Unlock()

	logging.Infof("断开连接: %s -> %s", t.localAddr, remoteAddr)
	return nil
}

// Clear 清理所有节点
//
// 用于测试后的清理
func (t *MemoryTransport) Clear() {
	t.nodesMu.Lock()
	defer t.nodesMu.Unlock()

	for addr, node := range t.nodes {
		if addr != t.localAddr {
			_ = node.Close()
		}
	}

	// 保留本地节点，删除其他节点
	localNode := t.nodes[t.localAddr]
	t.nodes = make(map[string]*memoryNode)
	t.nodes[t.localAddr] = localNode

	logging.Debug("清理所有远程节点连接")
}
