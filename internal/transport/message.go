// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// ==================== 常量定义 ====================

// HopMax 树形拓扑中节点间通信的最大跳数
const HopMax uint8 = 10

// ==================== MessageType 枚举 ====================

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

// ==================== Message 结构定义 ====================

// Message NexKV 协议消息定义
//
// 注意：Key、Value、Version 等业务字段已移至 Payload 中
// 使用 EncodePayload/DecodePayload 方法进行序列化/反序列化
type Message struct {
	// Type 消息类型
	Type MessageType
	// Seq 消息序号（单调递增）
	Seq uint64
	// Timestamp 时间戳
	Timestamp time.Time
	// From 发送方节点 ID
	From string
	// To 接收方节点 ID
	To string
	// HopCount 跳数（用于消息路由）
	HopCount uint8
	// Payload 扩展负载（序列化后的业务数据）
	//
	// Payload 类型定义：
	//   - MessageTypePut:     PutPayload
	//   - MessageTypeGet:     GetPayload
	//   - MessageTypeDelete:  DeletePayload
	//   - MessageTypeGossip:  GossipPayload
	//   - MessageTypeQuorum:  QuorumPayload
	//   - MessageTypeSync:    TwoPCPreparePayload
	//   - MessageTypeAck:     TwoPCCommitPayload
	//   - MessageTypeNack:    TwoPCRollbackPayload
	//   - MessageTypeCluster: ClusterPayload
	//
	// 使用 EncodePayload/DecodePayload 方法进行类型安全的序列化/反序列化
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

	// 如果有 Payload，认为是有效的
	if len(m.Payload) > 0 {
		return true
	}

	// 没有 Payload 时，只验证基本字段
	switch m.Type {
	case MessageTypeAck, MessageTypeNack:
		return m.Seq > 0
	}

	// 其他消息类型必须有 Payload
	return false
}

// Size 返回消息的预估大小（字节）
func (m *Message) Size() int {
	// Type(1) + Seq(8) + HopCount(1) + Timestamp(8) + From + To + Payload
	return 1 + 8 + 1 + 8 + len(m.From) + len(m.To) + len(m.Payload)
}

// ==================== Payload 结构定义 ====================

// PutPayload KV PUT 操作专属 Payload
type PutPayload struct {
	Key     []byte `msgpack:"key"`             // 从 Message.Key 移入
	Value   []byte `msgpack:"value,omitempty"` // 从 Message.Value 移入
	Version uint64 `msgpack:"version"`         // 从 Message.Version 移入
	Sync    bool   `msgpack:"sync"`            // 是否同步写入
}

// GetPayload KV GET 操作专属 Payload
type GetPayload struct {
	Key         []byte `msgpack:"key"`          // 从 Message.Key 移入
	WithVersion bool   `msgpack:"with_version"` // 是否返回版本号
}

// DeletePayload KV DELETE 操作专属 Payload
type DeletePayload struct {
	Key    []byte `msgpack:"key"`    // 从 Message.Key 移入
	Verify bool   `msgpack:"verify"` // 是否验证删除成功
}

// GossipPayload Gossip 协议专属 Payload
//
// 扩展字段（Phase 1: Merkle Tree 集成）：
//   - GlobalRootHash: 全局 Merkle Root Hash（用于快速差异检测）
//   - NamespaceHashes: Namespace -> Root Hash 映射
//   - RequestedData: 双向同步请求数据
type GossipPayload struct {
	// 原有字段
	Digest       map[string]uint64 `msgpack:"digest"`        // key -> version
	VersionDelta uint64            `msgpack:"version_delta"` // 版本增量
	FullSync     bool              `msgpack:"full_sync"`     // 是否全量同步

	// Merkle Tree 字段（新增）
	GlobalRootHash  string            `msgpack:"global_root_hash,omitempty"` // 全局 Root Hash
	NamespaceHashes map[string]string `msgpack:"namespace_hashes,omitempty"` // Namespace -> Root Hash
	RequestedData   []SyncRequest     `msgpack:"requested_data,omitempty"`   // 双向请求数据
	// 消息去重字段（Phase 3: P3-1.3 消息去重）
	MessageID     uint64            `msgpack:"message_id,omitempty"` // 消息唯一 ID（发送方生成，接收方用于去重）
}

// SyncRequest 双向同步请求
type SyncRequest struct {
	Namespace string `msgpack:"namespace"` // Namespace
	Key       string `msgpack:"key"`       // 请求的 Key
}

// QuorumPayload Quorum 协议专属 Payload
type QuorumPayload struct {
	Phase      string `msgpack:"phase"`              // "propose", "vote", "decide"
	ProposalID string `msgpack:"proposal_id"`        // 提案ID
	Key        string `msgpack:"key"`                // 操作的键
	Value      []byte `msgpack:"value,omitempty"`    // 操作的值
	Voter      string `msgpack:"voter,omitempty"`    // 投票节点
	Decision   bool   `msgpack:"decision,omitempty"` // 决策结果

	MessageID  uint64            `msgpack:"message_id,omitempty"` // 消息唯一 ID（发送方生成，接收方用于去重）
}

// Operation 2PC 操作定义
type Operation struct {
	Type  string `msgpack:"type"`            // "put", "delete"
	Key   string `msgpack:"key"`             // 操作的键
	Value []byte `msgpack:"value,omitempty"` // 操作的值
}

// TwoPCPreparePayload 2PC 准备阶段专属 Payload
type TwoPCPreparePayload struct {
	TxID        string      `msgpack:"tx_id"`       // 事务ID
	Operations  []Operation `msgpack:"operations"`  // 操作列表
	Timeout     int64       `msgpack:"timeout"`     // 超时时间（毫秒）
	Coordinator string      `msgpack:"coordinator"` // 协调节点
}

// TwoPCCommitPayload 2PC 提交阶段专属 Payload
type TwoPCCommitPayload struct {
	TxID   string `msgpack:"tx_id"`  // 事务ID
	Result bool   `msgpack:"result"` // 提交结果（成功/失败）
}

// TwoPCRollbackPayload 2PC 回滚阶段专属 Payload
type TwoPCRollbackPayload struct {
	TxID   string `msgpack:"tx_id"`  // 事务ID
	Reason string `msgpack:"reason"` // 回滚原因
}

// ClusterPayload 集群管理专属 Payload
type ClusterPayload struct {
	Action   string            `msgpack:"action"`   // "join", "leave", "status"
	NodeID   string            `msgpack:"node_id"`  // 节点ID
	Metadata map[string]string `msgpack:"metadata"` // 元数据
}

// ==================== Payload 类型映射 ====================

// PayloadTypeFactory Payload 类型工厂函数（用于创建新实例）
type PayloadTypeFactory func() any

// payloadTypeFactories 维护 MessageType 与 Payload 工厂函数的映射
var payloadTypeFactories = map[MessageType]PayloadTypeFactory{
	MessageTypePut:     func() any { return &PutPayload{} },
	MessageTypeGet:     func() any { return &GetPayload{} },
	MessageTypeDelete:  func() any { return &DeletePayload{} },
	MessageTypeGossip:  func() any { return &GossipPayload{} },
	MessageTypeQuorum:  func() any { return &QuorumPayload{} },
	MessageTypeSync:    func() any { return &TwoPCPreparePayload{} },
	MessageTypeAck:     func() any { return &TwoPCCommitPayload{} },
	MessageTypeNack:    func() any { return &TwoPCRollbackPayload{} },
	MessageTypeCluster: func() any { return &ClusterPayload{} },
}

// ==================== Payload 编解码方法 ====================

// EncodePayload 将结构化 Payload 序列化为 []byte
// 自动绑定 Message.Type 为对应 MessageType
func (m *Message) EncodePayload(payload any) error {
	// 1. 校验 Payload 类型，绑定对应的 MessageType
	var msgType MessageType
	switch payload.(type) {
	case *PutPayload:
		msgType = MessageTypePut
	case *GetPayload:
		msgType = MessageTypeGet
	case *DeletePayload:
		msgType = MessageTypeDelete
	case *GossipPayload:
		msgType = MessageTypeGossip
	case *QuorumPayload:
		msgType = MessageTypeQuorum
	case *TwoPCPreparePayload:
		msgType = MessageTypeSync
	case *TwoPCCommitPayload:
		msgType = MessageTypeAck
	case *TwoPCRollbackPayload:
		msgType = MessageTypeNack
	case *ClusterPayload:
		msgType = MessageTypeCluster
	default:
		return errors.New("unsupported payload type")
	}
	m.Type = msgType

	// 2. MessagePack 序列化 Payload 为 []byte
	data, err := msgpack.Marshal(payload)
	if err != nil {
		return errors.New("msgpack marshal failed: " + err.Error())
	}
	m.Payload = data
	return nil
}

// DecodePayload 将 Message.Payload 反序列化为对应结构化 Payload
// 根据 Message.Type 自动匹配 Payload 类型，保证类型安全
func (m *Message) DecodePayload() (any, error) {
	// 1. 检查 MessageType 是否合法
	factory, ok := payloadTypeFactories[m.Type]
	if !ok {
		return nil, errors.New("unsupported message type: " + m.Type.String())
	}

	// 2. 使用工厂函数创建新的 Payload 实例（避免状态污染）
	payload := factory()

	// 3. MessagePack 反序列化到结构化 Payload
	if err := msgpack.Unmarshal(m.Payload, payload); err != nil {
		return nil, errors.New("msgpack unmarshal failed: " + err.Error())
	}

	return payload, nil
}

// MustEncodePayload 编码 Payload，失败时 panic（仅用于测试）
func (m *Message) MustEncodePayload(payload any) {
	if err := m.EncodePayload(payload); err != nil {
		panic(err)
	}
}

// MustDecodePayload 解码 Payload，失败时 panic（仅用于测试）
func (m *Message) MustDecodePayload() any {
	payload, err := m.DecodePayload()
	if err != nil {
		panic(err)
	}
	return payload
}

// ==================== MessageCodec 接口定义 ====================

// MessageCodec 消息编解码器接口
type MessageCodec interface {
	Encode(w io.Writer, msg *Message) error
	Decode(r io.Reader) (*Message, error)
}

// ==================== MessagePackCodec 实现 ====================

// MessagePackCodec MessagePack 编解码实现
// 使用 TLV (Type-Length-Value) 格式封装 MessagePack 编码的消息体
type MessagePackCodec struct {
	seqGenerator *atomic.Uint64 // 消息序号生成器
}

// NewMessagePackCodec 创建 MessagePack 编解码器
func NewMessagePackCodec() *MessagePackCodec {
	seq := atomic.Uint64{}
	seq.Store(0)
	return &MessagePackCodec{seqGenerator: &seq}
}

// Encode 编码消息并通过 io.Writer 写入（触发网络发送）
//
// 方法行为：
//  1. 使用 MessagePack 编码消息体
//  2. 写入 TLV 消息头（Type + Length）
//  3. 写入消息体到 io.Writer
//
// 重要说明：如果 w 是网络 Stream（如 libp2p Stream），Write 操作会触发网络发送。
// 消息格式：Type(1) + Length(2) + Value(MessagePack)
func (c *MessagePackCodec) Encode(w io.Writer, msg *Message) error {
	// 自动生成消息序号
	if msg.Seq == 0 {
		msg.Seq = c.seqGenerator.Add(1)
	}

	// 1. 使用 MessagePack 编码消息体
	msgData, err := msgpack.Marshal(msg)
	if err != nil {
		return fmt.Errorf("MessagePack 编码失败: %w", err)
	}

	// 2. 写入消息头（Type + Length）
	if err := c.writeHeader(w, msg.Type, uint16(len(msgData))); err != nil {
		return err
	}

	// 3. 写入消息体
	if _, err := w.Write(msgData); err != nil {
		return fmt.Errorf("写入消息体失败: %w", err)
	}

	return nil
}

// Decode 解码消息
func (c *MessagePackCodec) Decode(r io.Reader) (*Message, error) {
	// 1. 读取消息头（Type + Length）
	msgType, length, err := c.readHeader(r)
	if err != nil {
		return nil, err
	}

	// 2. 读取并解码消息体
	msgData, err := c.readMessageBody(r, length)
	if err != nil {
		return nil, err
	}

	// 3. 解码 MessagePack 数据
	var msg Message
	if err := msgpack.Unmarshal(msgData, &msg); err != nil {
		return nil, fmt.Errorf("MessagePack 解码失败: %w", err)
	}

	// 确保消息类型正确
	msg.Type = msgType
	return &msg, nil
}

// EncodeToBytes 编码消息为字节切片（便捷方法）
func (c *MessagePackCodec) EncodeToBytes(msg *Message) ([]byte, error) {
	// 预分配缓冲区
	buf := make([]byte, 0, msg.Size()+3) // +3 for Type (1) + Length (2)

	// 使用内存缓冲区编码
	bufWriter := newByteSliceWriter(&buf)
	if err := c.Encode(bufWriter, msg); err != nil {
		return nil, err
	}

	return buf, nil
}

// DecodeFromBytes 从字节切片解码消息（便捷方法）
func (c *MessagePackCodec) DecodeFromBytes(data []byte) (*Message, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("数据过短: %d 字节", len(data))
	}

	bufReader := newByteSliceReader(data)
	return c.Decode(bufReader)
}

// byteSliceWriter 用于写入字节切片的 io.Writer 实现
type byteSliceWriter struct {
	buf *[]byte
}

func newByteSliceWriter(buf *[]byte) *byteSliceWriter {
	return &byteSliceWriter{buf: buf}
}

func (w *byteSliceWriter) Write(p []byte) (n int, err error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

// byteSliceReader 用于读取字节切片的 io.Reader 实现
type byteSliceReader struct {
	data []byte
	pos  int
}

func newByteSliceReader(data []byte) *byteSliceReader {
	return &byteSliceReader{data: data, pos: 0}
}

func (r *byteSliceReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// writeHeader 写入消息头（Type + Length）
func (c *MessagePackCodec) writeHeader(w io.Writer, msgType MessageType, length uint16) error {
	// 写入消息类型
	if err := binary.Write(w, binary.BigEndian, msgType); err != nil {
		return fmt.Errorf("写入消息类型失败: %w", err)
	}
	// 写入长度
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("写入长度失败: %w", err)
	}
	return nil
}

// readHeader 读取消息头（Type + Length）
func (c *MessagePackCodec) readHeader(r io.Reader) (MessageType, uint16, error) {
	var msgType MessageType
	var length uint16

	// 读取消息类型
	if err := binary.Read(r, binary.BigEndian, &msgType); err != nil {
		return 0, 0, fmt.Errorf("读取消息类型失败: %w", err)
	}

	// 读取长度
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return 0, 0, fmt.Errorf("读取长度失败: %w", err)
	}

	// 验证长度
	if length > MaxMessageSize {
		return 0, 0, fmt.Errorf("消息过大: %d 字节（最大 %d 字节）", length, MaxMessageSize)
	}

	return msgType, length, nil
}

// readMessageBody 读取消息体
func (c *MessagePackCodec) readMessageBody(r io.Reader, length uint16) ([]byte, error) {
	msgData := make([]byte, length)
	if _, err := io.ReadFull(r, msgData); err != nil {
		return nil, fmt.Errorf("读取消息体失败: %w", err)
	}
	return msgData, nil
}

// ResetSeqGenerator 重置序号生成器（主要用于测试）
func (c *MessagePackCodec) ResetSeqGenerator() {
	c.seqGenerator.Store(0)
}

// GetNextSeq 获取下一个消息序号（不递增）
func (c *MessagePackCodec) GetNextSeq() uint64 {
	return c.seqGenerator.Load() + 1
}
