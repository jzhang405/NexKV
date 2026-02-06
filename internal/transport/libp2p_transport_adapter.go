// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Transport 传输层接口（保持与业务层兼容）
// 业务层使用此接口发送和接收消息
type Transport interface {
	// Send 发送消息到指定节点
	// nodeID: 目标节点的业务层 ID（字符串形式）
	// msg: 消息内容（已编码的字节）
	Send(nodeID string, msg []byte) error

	// Receive 注册消息接收处理器
	// handler: 接收到消息时的回调函数
	//   - nodeID: 发送方节点的业务层 ID
	//   - msg: 接收到的消息内容
	Receive(handler func(nodeID string, msg []byte)) error

	// Close 关闭传输层
	Close() error
}

// Libp2pTransportAdapter 适配器：实现现有 Transport 接口
// 职责：将 NodeID 与 peer.ID 进行双向转换，保持业务层 API 不变
type Libp2pTransportAdapter struct {
	host     host.Host
	protocol *NexKVProtocol
	mapper   *NodeIDMapper // NodeID ↔ PeerID 双向映射
	handler  func(string, []byte)
	handlerMu sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
}

// NewLibp2pTransportAdapter 创建适配器
func NewLibp2pTransportAdapter(h host.Host) *Libp2pTransportAdapter {
	ctx, cancel := context.WithCancel(context.Background())
	protocol := NewNexKVProtocol(h, nil)

	adapter := &Libp2pTransportAdapter{
		host:     h,
		protocol: protocol,
		mapper:   NewNodeIDMapper(),
		ctx:      ctx,
		cancel:   cancel,
		started:  false,
	}

	return adapter
}

// RegisterNodeID 注册 NodeID 与 PeerID 的映射关系
// 业务层在建立连接前调用此方法注册节点映射
func (a *Libp2pTransportAdapter) RegisterNodeID(nodeID string, pid peer.ID) {
	a.mapper.Register(nodeID, pid)
}

// RegisterNodeIDFromString 从 PeerID 字符串注册 NodeID
func (a *Libp2pTransportAdapter) RegisterNodeIDFromString(nodeID, peerIDStr string) error {
	pid, err := peer.Decode(peerIDStr)
	if err != nil {
		return fmt.Errorf("解析 PeerID 失败: %w", err)
	}
	a.mapper.Register(nodeID, pid)
	return nil
}

// Send 实现 Transport.Send 接口
func (a *Libp2pTransportAdapter) Send(nodeID string, msg []byte) error {
	// 查找 NodeID 对应的 PeerID
	pid, ok := a.mapper.GetPeerID(nodeID)
	if !ok {
		return fmt.Errorf("未知节点 ID: %s", nodeID)
	}

	// 包装成 NexKV 消息格式
	nexKVMsg := &Message{
		Type:    MessageTypeCluster,
		Payload: msg,
	}

	// 调用 NexKVProtocol 发送消息
	if err := a.protocol.SendMessage(a.ctx, pid, nexKVMsg); err != nil {
		return fmt.Errorf("发送消息失败: %w", err)
	}

	return nil
}

// Receive 实现 Transport.Receive 接口
func (a *Libp2pTransportAdapter) Receive(handler func(nodeID string, msg []byte)) error {
	a.handlerMu.Lock()
	defer a.handlerMu.Unlock()

	if a.started {
		return fmt.Errorf("接收处理器已注册")
	}

	a.handler = handler

	// 注册消息处理器，将 peer.ID 转回 NodeID
	a.protocol.RegisterHandler(MessageTypeCluster, MessageHandlerFunc(func(ctx context.Context, from peer.ID, msg *Message) error {
		// 查找 PeerID 对应的 NodeID
		nodeID, ok := a.mapper.GetNodeID(from)
		if !ok {
			// 未知 peer，使用 PeerID 字符串作为 NodeID
			nodeID = from.String()
			// 自动注册映射
			a.mapper.Register(nodeID, from)
		}

		// 调用业务层处理器
		a.handlerMu.RLock()
		if a.handler != nil {
			a.handler(nodeID, msg.Payload)
		}
		a.handlerMu.RUnlock()

		return nil
	}))

	a.started = true
	return nil
}

// Close 实现 Transport.Close 接口
func (a *Libp2pTransportAdapter) Close() error {
	a.handlerMu.Lock()
	defer a.handlerMu.Unlock()

	if a.started {
		// 取消上下文
		a.cancel()

		// 移除消息处理器
		a.protocol.UnregisterHandler(MessageTypeCluster)

		a.started = false
	}

	return nil
}

// GetPeerID 获取 NodeID 对应的 PeerID（辅助方法）
func (a *Libp2pTransportAdapter) GetPeerID(nodeID string) (peer.ID, bool) {
	return a.mapper.GetPeerID(nodeID)
}

// GetNodeID 获取 PeerID 对应的 NodeID（辅助方法）
func (a *Libp2pTransportAdapter) GetNodeID(pid peer.ID) (string, bool) {
	return a.mapper.GetNodeID(pid)
}

// Host 返回底层的 libp2p Host（用于高级操作）
func (a *Libp2pTransportAdapter) Host() host.Host {
	return a.host
}

// Protocol 返回底层的 NexKVProtocol（用于高级操作）
func (a *Libp2pTransportAdapter) Protocol() *NexKVProtocol {
	return a.protocol
}

// ConnectToPeer 连接到指定 peer（辅助方法）
func (a *Libp2pTransportAdapter) ConnectToPeer(pid peer.ID) error {
	ctx, cancel := context.WithTimeout(a.ctx, defaultConnectTimeout)
	defer cancel()

	peerInfo := a.host.Peerstore().PeerInfo(pid)
	if err := a.host.Connect(ctx, peerInfo); err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}

	return nil
}

// ConnectToNodeID 连接到指定 NodeID（辅助方法）
func (a *Libp2pTransportAdapter) ConnectToNodeID(nodeID string) error {
	pid, ok := a.mapper.GetPeerID(nodeID)
	if !ok {
		return fmt.Errorf("未知节点 ID: %s", nodeID)
	}

	return a.ConnectToPeer(pid)
}

const defaultConnectTimeout = 30 * time.Second
