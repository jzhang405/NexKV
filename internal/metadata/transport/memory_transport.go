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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
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
	addr       string
	sendCh     chan Message
	recvCh     chan Message
	closeOnce  sync.Once
	closeCh    chan struct{}
	closed     atomic.Bool
}

// NewMemoryTransport 创建内存传输
func NewMemoryTransport(localAddr string) (*MemoryTransport, error) {
	t := &MemoryTransport{
		config:    DefaultTransportConfig(),
		codec:     NewMessagePackCodec(),
		nodes:     make(map[string]*memoryNode),
		channels:  make(map[string]chan Message),
		stopCh:    make(chan struct{}),
		localAddr: localAddr,
	}

	// 注册本地节点
	t.registerNode(localAddr)

	return t, nil
}

// Start 启动传输层
func (t *MemoryTransport) Start() error {
	if !t.started.CompareAndSwap(false, true) {
		return fmt.Errorf("传输层已经启动")
	}

	logging.Infof("启动内存传输层，地址: %s", t.localAddr)

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
		return fmt.Errorf("传输层未启动")
	}

	t.nodesMu.RLock()
	node, exists := t.nodes[addr]
	t.nodesMu.RUnlock()

	if !exists {
		return fmt.Errorf("节点不存在: %s", addr)
	}

	if node.isClosed() {
		return fmt.Errorf("节点已关闭: %s", addr)
	}

	// 通过通道发送
	select {
	case node.sendCh <- msg:
		logging.Debugf("发送消息: %s to %s", msg.Type(), addr)
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("发送超时")
	case <-ctx.Done():
		return ctx.Err()
	case <-t.stopCh:
		return fmt.Errorf("传输层已停止")
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
		addr:   addr,
		sendCh: make(chan Message, 1024),
		recvCh: make(chan Message, 1024),
		closeCh: make(chan struct{}),
	}

	t.nodes[addr] = node

	// 创建接收通道
	t.channelsMu.Lock()
	if _, exists := t.channels[addr]; !exists {
		t.channels[addr] = make(chan Message, 1024)
	}
	t.channelsMu.Unlock()

	logging.Debugf("注册内存节点: %s", addr)
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

	logging.Debugf("开始接收消息: %s", t.localAddr)

	for {
		select {
		case <-t.stopCh:
			logging.Debugf("接收循环停止: %s", t.localAddr)
			return

		case msg, ok := <-node.sendCh:
			if !ok {
				logging.Debugf("发送通道已关闭: %s", t.localAddr)
				return
			}

			// 转发到接收通道
			t.channelsMu.RLock()
			recvCh, exists := t.channels[t.localAddr]
			t.channelsMu.RUnlock()

			if exists {
				select {
				case recvCh <- msg:
					logging.Debugf("接收消息: %s from %s", msg.Type(), t.localAddr)
				case <-time.After(1 * time.Second):
					logging.Errorf("接收通道阻塞，消息丢弃")
				}
			}

		case <-node.closeCh:
			logging.Debugf("节点已关闭: %s", t.localAddr)
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
	}
}

// forwardMessages 转发消息
//
// 在两个节点之间转发消息
func (t *MemoryTransport) forwardMessages(from, to *memoryNode) {
	defer t.stopWg.Done()

	for {
		select {
		case <-t.stopCh:
			return
		case <-from.closeCh:
			return
		case msg, ok := <-from.sendCh:
			if !ok {
				return
			}

			select {
			case to.sendCh <- msg:
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

// isClosed 检查节点是否已关闭
func (n *memoryNode) isClosed() bool {
	return n.closed.Load()
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
func (t *MemoryTransport) Stats() map[string]interface{} {
	t.nodesMu.RLock()
	defer t.nodesMu.RUnlock()

	stats := make(map[string]interface{})
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
		return fmt.Errorf("本地节点不存在: %s", t.localAddr)
	}

	if !remoteExists {
		t.RegisterRemoteNode(remoteAddr)
	}

	logging.Infof("建立连接: %s -> %s", t.localAddr, remoteAddr)
	return nil
}

// DisconnectFrom 断开与远程节点的连接
func (t *MemoryTransport) DisconnectFrom(remoteAddr string) error {
	t.nodesMu.Lock()
	defer t.nodesMu.Unlock()

	node, exists := t.nodes[remoteAddr]
	if !exists {
		return fmt.Errorf("节点不存在: %s", remoteAddr)
	}

	_ = node.Close()
	delete(t.nodes, remoteAddr)

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
