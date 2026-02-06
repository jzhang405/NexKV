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
	"time"
)

// HopMax 树形拓扑中节点间通信的最大跳数
const HopMax uint8 = 10

// MessageType 消息类型枚举
type MessageType uint8

const (
	MessageTypeUnknown MessageType = 0
	MessageTypeGet     MessageType = 1
	MessageTypePut     MessageType = 2
	MessageTypeDelete  MessageType = 3
	MessageTypeSync    MessageType = 4
	MessageTypeAck     MessageType = 5
	MessageTypeNack    MessageType = 6
	MessageTypeGossip  MessageType = 7
	MessageTypeCluster MessageType = 8
	MessageTypeQuorum  MessageType = 9
)

// String 返回消息类型的字符串表示
func (mt MessageType) String() string {
	switch mt {
	case MessageTypeGet:
		return "GET"
	case MessageTypePut:
		return "PUT"
	case MessageTypeDelete:
		return "DELETE"
	case MessageTypeSync:
		return "SYNC"
	case MessageTypeAck:
		return "ACK"
	case MessageTypeNack:
		return "NACK"
	case MessageTypeGossip:
		return "GOSSIP"
	case MessageTypeCluster:
		return "CLUSTER"
	case MessageTypeQuorum:
		return "QUORUM"
	default:
		return "UNKNOWN"
	}
}

// Message NexKV 协议消息定义
type Message struct {
	// Type 消息类型
	Type MessageType
	// Seq 消息序号（单调递增）
	Seq uint64
	// Key 键（用于 GET/DELETE）
	Key []byte
	// Value 值（用于 PUT）
	Value []byte
	// Version 版本号（用于 MVCC）
	Version uint64
	// Timestamp 时间戳
	Timestamp time.Time
	// From 发送方节点 ID
	From string
	// To 接收方节点 ID
	To string
	// HopCount 跳数（用于消息路由）
	HopCount uint8
	// Payload 扩展负载（用于自定义消息）
	Payload []byte
}

// NewMessage 创建新消息
func NewMessage(msgType MessageType) *Message {
	return &Message{
		Type:      msgType,
		Timestamp: time.Now(),
		HopCount:  0,
	}
}

// Clone 克隆消息（用于转发）
func (m *Message) Clone() *Message {
	clone := *m
	// 深拷贝切片字段
	clone.copyBytes(m.Key, &clone.Key)
	clone.copyBytes(m.Value, &clone.Value)
	clone.copyBytes(m.Payload, &clone.Payload)
	return &clone
}

// copyBytes 复制字节数据
func (m *Message) copyBytes(src []byte, dest *[]byte) {
	if src != nil {
		*dest = make([]byte, len(src))
		copy(*dest, src)
	}
}

// IncrementHopCount 增加跳数（如果超过最大值返回 false）
func (m *Message) IncrementHopCount() bool {
	if m.HopCount >= HopMax {
		return false
	}
	m.HopCount++
	return true
}

// IsValid 验证消息有效性
func (m *Message) IsValid() bool {
	// 检查消息类型
	if m.Type == MessageTypeUnknown || m.Type > MessageTypeQuorum {
		return false
	}

	// 根据消息类型验证必填字段
	switch m.Type {
	case MessageTypeGet, MessageTypeDelete:
		return len(m.Key) > 0
	case MessageTypePut:
		return len(m.Key) > 0 && m.Value != nil
	case MessageTypeAck, MessageTypeNack:
		return m.Seq > 0
	}

	return true
}

// Size 返回消息的预估大小（字节）
func (m *Message) Size() int {
	// Type(1) + Seq(8) + Version(8) + HopCount(1) + Timestamp(8) + From + To + Payload
	return 1 + 8 + 8 + 1 + 8 + len(m.From) + len(m.To) + len(m.Key) + len(m.Value) + len(m.Payload)
}
