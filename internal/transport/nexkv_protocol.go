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
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// Protocol ID 常量
const (
	// ProtocolNexKV NexKV 主协议
	ProtocolNexKV = protocol.ID("/nexkv/1.0.0")
	// ProtocolNexKVRPC NexKV RPC 协议
	ProtocolNexKVRPC = protocol.ID("/nexkv/rpc/1.0.0")
	// ProtocolNexKVGossip NexKV Gossip 协议
	ProtocolNexKVGossip = protocol.ID("/nexkv/gossip/1.0.0")
	// ProtocolNexKVSync NexKV 同步协议
	ProtocolNexKVSync = protocol.ID("/nexkv/sync/1.0.0")
)

// MessageHandler 消息处理器接口
// 业务层实现此接口来处理接收到的消息
type MessageHandler interface {
	HandleMessage(ctx context.Context, from peer.ID, msg *Message) error
}

// MessageHandlerFunc 函数式消息处理器（便捷实现）
type MessageHandlerFunc func(ctx context.Context, from peer.ID, msg *Message) error

// HandleMessage 实现 MessageHandler 接口
func (f MessageHandlerFunc) HandleMessage(ctx context.Context, from peer.ID, msg *Message) error {
	return f(ctx, from, msg)
}

// NexKVProtocol NexKV 协议处理器（应用层协议，非传输层）
// 提供消息发送、广播和接收处理功能
type NexKVProtocol struct {
	host     host.Host
	codec    MessageCodec
	handlers map[MessageType]MessageHandler
	mutex    sync.RWMutex
	stats    *ProtocolStats
}

// ProtocolStats 协议统计信息
type ProtocolStats struct {
	MessagesSent     uint64
	MessagesReceived uint64
	BytesSent        uint64
	BytesReceived    uint64
	Errors           uint64
	mu               sync.Mutex
}

// NewNexKVProtocol 创建协议处理器
func NewNexKVProtocol(h host.Host, codec MessageCodec) *NexKVProtocol {
	if codec == nil {
		codec = NewMessagePackCodec()
	}

	p := &NexKVProtocol{
		host:     h,
		codec:    codec,
		handlers: make(map[MessageType]MessageHandler),
		stats:    &ProtocolStats{},
	}

	// 注册 Stream 处理器（libp2p 标准模式）
	h.SetStreamHandler(ProtocolNexKV, p.handleStream)

	return p
}

// RegisterHandler 注册消息处理器
func (p *NexKVProtocol) RegisterHandler(msgType MessageType, handler MessageHandler) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.handlers[msgType] = handler
}

// UnregisterHandler 注销消息处理器
func (p *NexKVProtocol) UnregisterHandler(msgType MessageType) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	delete(p.handlers, msgType)
}

// handleStream 处理传入 Stream（libp2p 标准模式）
func (p *NexKVProtocol) handleStream(s network.Stream) {
	defer s.Close()

	// 设置读取超时
	if err := s.SetReadDeadline(time.Now().Add(StreamReadTimeout)); err != nil {
		p.recordError()
		return
	}

	// 解码并验证消息
	msg, err := p.decodeAndValidateMessage(s)
	if err != nil {
		p.recordError()
		return
	}

	// 处理消息
	p.processMessage(s, msg)
}

// decodeAndValidateMessage 解码并验证消息
func (p *NexKVProtocol) decodeAndValidateMessage(s network.Stream) (*Message, error) {
	msg, err := p.codec.Decode(s)
	if err != nil {
		return nil, err
	}
	if !msg.IsValid() {
		return nil, fmt.Errorf("无效消息")
	}
	return msg, nil
}

// processMessage 处理消息
func (p *NexKVProtocol) processMessage(s network.Stream, msg *Message) {
	from := s.Conn().RemotePeer()
	p.updateStats(false, msg.Size())

	handler := p.getHandler(msg.Type)
	if handler == nil {
		// 记录警告，帮助调试
		fmt.Printf("警告：未注册的消息处理器: type=%d, from=%s\n", msg.Type, from)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), StreamReadTimeout)
	defer cancel()

	if err := handler.HandleMessage(ctx, from, msg); err != nil {
		fmt.Printf("消息处理失败: type=%d, from=%s, error=%v\n", msg.Type, from, err)
		p.recordError()
	}
}

// getHandler 获取消息处理器
func (p *NexKVProtocol) getHandler(msgType MessageType) MessageHandler {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.handlers[msgType]
}

// SendMessage 发送消息到指定节点
func (p *NexKVProtocol) SendMessage(ctx context.Context, pid peer.ID, msg *Message) error {
	// 验证消息
	if !msg.IsValid() {
		return fmt.Errorf("无效消息: type=%d", msg.Type)
	}

	// 创建 Stream
	s, err := p.host.NewStream(ctx, pid, ProtocolNexKV)
	if err != nil {
		p.recordError()
		return fmt.Errorf("创建 Stream 失败: %w", err)
	}
	defer s.Close()

	// 设置写入超时
	if err := s.SetWriteDeadline(time.Now().Add(StreamWriteTimeout)); err != nil {
		p.recordError()
		return fmt.Errorf("设置写入超时失败: %w", err)
	}

	// 编码并发送消息
	if err := p.codec.Encode(s, msg); err != nil {
		p.recordError()
		return fmt.Errorf("发送消息失败: %w", err)
	}

	// 更新统计
	p.updateStats(true, msg.Size())
	return nil
}

// BroadcastMessage 广播消息到多个节点（使用信号量限制并发数）
func (p *NexKVProtocol) BroadcastMessage(ctx context.Context, pids []peer.ID, msg *Message) error {
	if len(pids) == 0 {
		return nil
	}

	// 验证消息
	if !msg.IsValid() {
		return fmt.Errorf("无效消息: type=%d", msg.Type)
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(pids))
	sem := make(chan struct{}, MaxConcurrentBroadcasts) // 信号量限制并发

	for _, pid := range pids {
		wg.Add(1)
		go func(target peer.ID) {
			defer wg.Done()

			sem <- struct{}{}        // 获取信号量
			defer func() { <-sem }() // 释放信号量

			// 克隆消息以避免并发编码同一消息对象时的竞态条件
			msgClone := msg.Clone()
			if err := p.SendMessage(ctx, target, msgClone); err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}(pid)
	}

	wg.Wait()
	close(errChan)

	// 收集错误
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("广播消息部分失败: %d/%d", len(errs), len(pids))
	}

	return nil
}

// Stats 获取协议统计信息
func (p *NexKVProtocol) Stats() *ProtocolStats {
	p.stats.mu.Lock()
	defer p.stats.mu.Unlock()

	// 返回副本
	return &ProtocolStats{
		MessagesSent:     p.stats.MessagesSent,
		MessagesReceived: p.stats.MessagesReceived,
		BytesSent:        p.stats.BytesSent,
		BytesReceived:    p.stats.BytesReceived,
		Errors:           p.stats.Errors,
	}
}

// ResetStats 重置统计信息
func (p *NexKVProtocol) ResetStats() {
	p.stats.mu.Lock()
	defer p.stats.mu.Unlock()
	p.stats.MessagesSent = 0
	p.stats.MessagesReceived = 0
	p.stats.BytesSent = 0
	p.stats.BytesReceived = 0
	p.stats.Errors = 0
}

// recordError 记录错误
func (p *NexKVProtocol) recordError() {
	p.stats.mu.Lock()
	defer p.stats.mu.Unlock()
	p.stats.Errors++
}

// updateStats 更新统计信息
func (p *NexKVProtocol) updateStats(sent bool, size int) {
	p.stats.mu.Lock()
	defer p.stats.mu.Unlock()
	if sent {
		p.stats.MessagesSent++
		p.stats.BytesSent += uint64(size)
	} else {
		p.stats.MessagesReceived++
		p.stats.BytesReceived += uint64(size)
	}
}

// Host 返回底层的 libp2p Host（用于高级操作）
func (p *NexKVProtocol) Host() host.Host {
	return p.host
}

// PeerID 返回本地节点 ID
func (p *NexKVProtocol) PeerID() peer.ID {
	return p.host.ID()
}

// Close 关闭协议处理器
func (p *NexKVProtocol) Close() error {
	// 移除 Stream 处理器
	p.host.RemoveStreamHandler(ProtocolNexKV)

	// 清空处理器
	p.mutex.Lock()
	p.handlers = make(map[MessageType]MessageHandler)
	p.mutex.Unlock()

	return nil
}
