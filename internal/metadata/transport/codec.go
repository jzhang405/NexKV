// Package transport 编解码实现
//
// 支持多种编解码器：MessagePack（默认）和 JSON（兼容性）
// 使用双标签实现同一结构体同时支持两种编解码器
package transport

import (
	"encoding/binary"
	"encoding/json"
	"io"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// MessagePack 编解码器实现
// ========================================
//
// Codec 接口定义在 transport.go 中（第 233 行）
// 此文件提供具体的编解码器实现

// MessagePackCodec MessagePack 编解码器
//
// 特点：
//   - 高效二进制序列化
//   - 自动处理 Go 类型到 MessagePack 类型的映射
//   - 支持嵌套结构和切片
type MessagePackCodec struct{}

// NewMessagePackCodec 创建 MessagePack 编解码器
func NewMessagePackCodec() *MessagePackCodec {
	return &MessagePackCodec{}
}

// Encode 编码消息
//
// 将消息编码为 MessagePack 格式的字节流
// 格式: [Type:2字节][DataLen:4字节][Data:N字节]
func (c *MessagePackCodec) Encode(msg Message) ([]byte, error) {
	if msg == nil {
		return nil, types.NewCodecInvalidMessageError("消息为空")
	}

	// 编码消息数据
	dataBytes, err := msgpack.Marshal(msg)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("Encode", err)
	}

	// 构建完整的编码数据
	// 格式: [Type(2字节)][DataLen(4字节)][Data(N字节)]
	buf := make([]byte, 2+4+len(dataBytes))

	// 写入 Type (2 字节)
	binary.BigEndian.PutUint16(buf[0:2], uint16(msg.Type()))

	// 写入 DataLen (4 字节)
	binary.BigEndian.PutUint32(buf[2:6], uint32(len(dataBytes)))

	// 写入 Data
	copy(buf[6:], dataBytes)

	return buf, nil
}

// Decode 解码消息
//
// 从 MessagePack 格式的字节流解码消息
// 格式: [Type:2字节][DataLen:4字节][Data:N字节]
func (c *MessagePackCodec) Decode(data []byte) (Message, error) {
	if len(data) == 0 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据为空")
	}
	if len(data) < 6 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据长度不足")
	}

	// 读取 Type (2 字节)
	msgType := MessageType(binary.BigEndian.Uint16(data[0:2]))

	// 读取 DataLen (4 字节)
	dataLen := int(binary.BigEndian.Uint32(data[2:6]))

	// 验证数据长度
	if len(data) < 6+dataLen {
		return nil, types.NewCodecInvalidDataError("Decode", "数据长度不足")
	}

	// 创建消息实例
	msg, err := createMessageByType(msgType)
	if err != nil {
		return nil, types.NewOpErr(types.ErrCodeInternal, "createMessage", "创建消息实例失败", err)
	}

	// 解码消息数据（使用 msgpack 标签）
	if dataLen > 0 {
		if err := msgpack.Unmarshal(data[6:6+dataLen], msg); err != nil {
			return nil, types.NewCodecDecodeFailedError("Unmarshal", err)
		}
	}

	return msg, nil
}

// Name 返回编解码器名称
func (c *MessagePackCodec) Name() string {
	return "msgpack"
}

// ========================================
// JSON 编解码器实现
// ========================================

// JSONCodec JSON 编解码器
//
// 特点：
//   - 文本格式，可读性好，支持跨语言
//   - 性能：编码/解码速度相对较慢
//   - 数据大小：约为 MessagePack 的 2-3 倍
//   - 推荐用于调试和跨语言兼容场景
type JSONCodec struct{}

// NewJSONCodec 创建 JSON 编解码器
func NewJSONCodec() *JSONCodec {
	return &JSONCodec{}
}

// Encode 编码消息（JSON 格式）
//
// 将消息编码为 JSON 格式的字节流
// 格式: [Type:2字节][DataLen:4字节][Data:N字节]
func (c *JSONCodec) Encode(msg Message) ([]byte, error) {
	if msg == nil {
		return nil, types.NewCodecInvalidMessageError("消息为空")
	}

	// 编码消息数据
	dataBytes, err := json.Marshal(msg)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("JSON", err)
	}

	// 构建完整的编码数据
	// 格式: [Type(2字节)][DataLen(4字节)][Data(N字节)]
	buf := make([]byte, 2+4+len(dataBytes))

	// 写入 Type (2 字节)
	binary.BigEndian.PutUint16(buf[0:2], uint16(msg.Type()))

	// 写入 DataLen (4 字节)
	binary.BigEndian.PutUint32(buf[2:6], uint32(len(dataBytes)))

	// 写入 Data
	copy(buf[6:], dataBytes)

	return buf, nil
}

// Decode 解码消息（JSON 格式）
//
// 从 JSON 格式的字节流解码消息
// 格式: [Type:2字节][DataLen:4字节][Data:N字节]
func (c *JSONCodec) Decode(data []byte) (Message, error) {
	if len(data) == 0 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据为空")
	}
	if len(data) < 6 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据长度不足")
	}

	// 读取 Type (2 字节)
	msgType := MessageType(binary.BigEndian.Uint16(data[0:2]))

	// 读取 DataLen (4 字节)
	dataLen := int(binary.BigEndian.Uint32(data[2:6]))

	// 验证数据长度
	if len(data) < 6+dataLen {
		return nil, types.NewCodecInvalidDataError("Decode", "数据长度不足")
	}

	// 创建消息实例
	msg, err := createMessageByType(msgType)
	if err != nil {
		return nil, types.NewOpErr(types.ErrCodeInternal, "createMessage", "创建消息实例失败", err)
	}

	// 解码消息数据（使用 json 标签）
	if dataLen > 0 {
		if err := json.Unmarshal(data[6:6+dataLen], msg); err != nil {
			return nil, types.NewCodecDecodeFailedError("JSON", err)
		}
	}

	return msg, nil
}

// Name 返回编解码器名称
func (c *JSONCodec) Name() string {
	return "json"
}

// ========================================
// 编解码器工厂函数
// ========================================

// NewCodec 根据类型创建编解码器
//
// 支持的类型：
//   - types.CodecTypeMessagePack: MessagePack 编解码器（默认）
//   - types.CodecTypeJSON: JSON 编解码器
func NewCodec(codecType types.CodecType) (Codec, error) {
	switch codecType {
	case types.CodecTypeMessagePack:
		return NewMessagePackCodec(), nil
	case types.CodecTypeJSON:
		return NewJSONCodec(), nil
	default:
		return nil, types.NewStoreInvalidParameterError("不支持的编解码器类型")
	}
}

// ========================================
// 消息工厂函数
// ========================================

// createMessageByType 根据消息类型创建消息实例
func createMessageByType(msgType MessageType) (Message, error) {
	switch msgType {
	// 元数据操作消息
	case MessageTypeGet:
		return &GetMessage{}, nil
	case MessageTypePut:
		return &PutMessage{}, nil
	case MessageTypeDelete:
		return &DeleteMessage{}, nil
	case MessageTypeGetReply:
		return &GetReplyMessage{}, nil
	case MessageTypePutReply:
		return &PutReplyMessage{}, nil
	case MessageTypeDeleteReply:
		return &DeleteReplyMessage{}, nil

	// Gossip 协议消息
	case MessageTypeGossipSync:
		return &GossipSyncMessage{}, nil
	case MessageTypeGossipSyncReply:
		return &GossipSyncReplyMessage{}, nil
	case MessageTypeGossipDigest:
		return &GossipDigestMessage{}, nil
	case MessageTypeGossipDigestReply:
		return &GossipDigestReplyMessage{}, nil

	// Quorum 协议消息
	case MessageTypeQuorumPropose:
		return &QuorumProposeMessage{}, nil
	case MessageTypeQuorumVote:
		return &QuorumVoteMessage{}, nil
	case MessageTypeQuorumDecide:
		return &QuorumDecideMessage{}, nil

	// 2PC 协议消息
	case MessageType2PCPrepare:
		return &TwoPCPrepareMessage{}, nil
	case MessageType2PCPrepareReply:
		return &TwoPCPrepareReplyMessage{}, nil
	case MessageType2PCCommit:
		return &TwoPCCommitMessage{}, nil
	case MessageType2PCRollback:
		return &TwoPCRollbackMessage{}, nil
	case MessageType2PCCommitReply:
		return &TwoPCCommitReplyMessage{}, nil
	case MessageType2PCRollbackReply:
		return &TwoPCRollbackReplyMessage{}, nil

	// 节点管理消息
	case MessageTypeNodePing:
		return &NodePingMessage{}, nil
	case MessageTypeNodePong:
		return &NodePongMessage{}, nil
	case MessageTypeNodeJoin:
		return &NodeJoinMessage{}, nil
	case MessageTypeNodeLeave:
		return &NodeLeaveMessage{}, nil
	case MessageTypeNodeSync:
		return &NodeSyncMessage{}, nil
	case MessageTypeClockSync:
		return &ClockSyncMessage{}, nil
	case MessageTypeClockSyncReply:
		return &ClockSyncReplyMessage{}, nil

	// 集群管理消息
	case MessageTypeClusterStatus:
		return &ClusterStatusMessage{}, nil
	case MessageTypeClusterStatusReply:
		return &ClusterStatusReplyMessage{}, nil
	case MessageTypeLeaderElection:
		return &LeaderElectionMessage{}, nil

	default:
		return nil, types.NewCodecUnknownMessageTypeError(int(msgType))
	}
}

// ========================================
// 元数据操作消息（双标签实现）
// ========================================

// GetMessage 获取元数据消息
type GetMessage struct {
	Key string `json:"key" msgpack:"key"`
}

// Type 实现 Message 接口
func (m *GetMessage) Type() MessageType {
	return MessageTypeGet
}

// Marshal 实现 Message 接口
func (m *GetMessage) Marshal() ([]byte, error) {
	return msgpack.Marshal(m)
}

// Unmarshal 实现 Message 接口
func (m *GetMessage) Unmarshal(data []byte) error {
	return msgpack.Unmarshal(data, m)
}

// Size 实现 Message 接口
func (m *GetMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// PutMessage 更新元数据消息
type PutMessage struct {
	Key   string `json:"key" msgpack:"key"`
	Value []byte `json:"value" msgpack:"value"`
}

func (m *PutMessage) Type() MessageType           { return MessageTypePut }
func (m *PutMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *PutMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *PutMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// DeleteMessage 删除元数据消息
type DeleteMessage struct {
	Key string `json:"key" msgpack:"key"`
}

func (m *DeleteMessage) Type() MessageType           { return MessageTypeDelete }
func (m *DeleteMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *DeleteMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *DeleteMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// GetReplyMessage Get 响应消息
type GetReplyMessage struct {
	Key     string `json:"key" msgpack:"key"`
	Value   []byte `json:"value" msgpack:"value"`
	Found   bool   `json:"found" msgpack:"found"`
	Version uint64 `json:"version" msgpack:"version"`
}

func (m *GetReplyMessage) Type() MessageType           { return MessageTypeGetReply }
func (m *GetReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *GetReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *GetReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// PutReplyMessage Put 响应消息
type PutReplyMessage struct {
	Key     string `json:"key" msgpack:"key"`
	Success bool   `json:"success" msgpack:"success"`
	Version uint64 `json:"version" msgpack:"version"`
}

func (m *PutReplyMessage) Type() MessageType           { return MessageTypePutReply }
func (m *PutReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *PutReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *PutReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// DeleteReplyMessage Delete 响应消息
type DeleteReplyMessage struct {
	Key     string `json:"key" msgpack:"key"`
	Success bool   `json:"success" msgpack:"success"`
}

func (m *DeleteReplyMessage) Type() MessageType           { return MessageTypeDeleteReply }
func (m *DeleteReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *DeleteReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *DeleteReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ========================================
// Gossip 协议消息（双标签实现）
// ========================================

// GossipSyncMessage Gossip 同步消息
type GossipSyncMessage struct {
	Version   uint64            `json:"version" msgpack:"version"`
	Metadata  map[string][]byte `json:"metadata" msgpack:"metadata"`
	Timestamp int64             `json:"timestamp" msgpack:"timestamp"`
}

func (m *GossipSyncMessage) Type() MessageType           { return MessageTypeGossipSync }
func (m *GossipSyncMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *GossipSyncMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *GossipSyncMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// GossipSyncReplyMessage Gossip 同步响应
type GossipSyncReplyMessage struct {
	Accepted bool   `json:"accepted" msgpack:"accepted"`
	Version  uint64 `json:"version" msgpack:"version"`
}

func (m *GossipSyncReplyMessage) Type() MessageType           { return MessageTypeGossipSyncReply }
func (m *GossipSyncReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *GossipSyncReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *GossipSyncReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// GossipDigestMessage Gossip 摘要消息
type GossipDigestMessage struct {
	Version uint64            `json:"version" msgpack:"version"`
	Digest  map[string]uint64 `json:"digest" msgpack:"digest"`
}

func (m *GossipDigestMessage) Type() MessageType           { return MessageTypeGossipDigest }
func (m *GossipDigestMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *GossipDigestMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *GossipDigestMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// GossipDigestReplyMessage Gossip 摘要响应
type GossipDigestReplyMessage struct {
	Version uint64            `json:"version" msgpack:"version"`
	Digest  map[string]uint64 `json:"digest" msgpack:"digest"`
}

func (m *GossipDigestReplyMessage) Type() MessageType           { return MessageTypeGossipDigestReply }
func (m *GossipDigestReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *GossipDigestReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *GossipDigestReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ========================================
// Quorum 协议消息（双标签实现）
// ========================================

// QuorumProposeMessage Quorum 提案消息
type QuorumProposeMessage struct {
	ProposalID string `json:"proposal_id" msgpack:"proposal_id"`
	Key        string `json:"key" msgpack:"key"`
	Value      []byte `json:"value" msgpack:"value"`
	Operation  string `json:"operation" msgpack:"operation"` // "put", "delete"
	Proposer   string `json:"proposer" msgpack:"proposer"`
	Timestamp  int64  `json:"timestamp" msgpack:"timestamp"`
}

func (m *QuorumProposeMessage) Type() MessageType           { return MessageTypeQuorumPropose }
func (m *QuorumProposeMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *QuorumProposeMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *QuorumProposeMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// QuorumVoteMessage Quorum 投票消息
type QuorumVoteMessage struct {
	ProposalID string `json:"proposal_id" msgpack:"proposal_id"`
	Voter      string `json:"voter" msgpack:"voter"`
	Vote       bool   `json:"vote" msgpack:"vote"`
	Reason     string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

func (m *QuorumVoteMessage) Type() MessageType           { return MessageTypeQuorumVote }
func (m *QuorumVoteMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *QuorumVoteMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *QuorumVoteMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// QuorumDecideMessage Quorum 决策消息
type QuorumDecideMessage struct {
	ProposalID string `json:"proposal_id" msgpack:"proposal_id"`
	Approved   bool   `json:"approved" msgpack:"approved"`
	Version    uint64 `json:"version" msgpack:"version"`
}

func (m *QuorumDecideMessage) Type() MessageType           { return MessageTypeQuorumDecide }
func (m *QuorumDecideMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *QuorumDecideMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *QuorumDecideMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ========================================
// 2PC 协议消息（双标签实现）
// ========================================

// TwoPCPrepareMessage 2PC 准备阶段消息
type TwoPCPrepareMessage struct {
	TransactionID string      `json:"transaction_id" msgpack:"transaction_id"`
	Participants  []string    `json:"participants" msgpack:"participants"`
	Operations    []Operation `json:"operations" msgpack:"operations"`
	Timeout       int64       `json:"timestamp" msgpack:"timestamp"`
}

// Operation 操作定义
type Operation struct {
	Type  string `json:"type" msgpack:"type"` // "put", "delete"
	Key   string `json:"key" msgpack:"key"`
	Value []byte `json:"value,omitempty" msgpack:"value,omitempty"`
}

func (m *TwoPCPrepareMessage) Type() MessageType           { return MessageType2PCPrepare }
func (m *TwoPCPrepareMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *TwoPCPrepareMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *TwoPCPrepareMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// TwoPCPrepareReplyMessage 2PC 准备响应
type TwoPCPrepareReplyMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Vote          string `json:"vote" msgpack:"vote"` // "commit", "abort"
	Reason        string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

func (m *TwoPCPrepareReplyMessage) Type() MessageType           { return MessageType2PCPrepareReply }
func (m *TwoPCPrepareReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *TwoPCPrepareReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *TwoPCPrepareReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// TwoPCCommitMessage 2PC 提交消息
type TwoPCCommitMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
}

func (m *TwoPCCommitMessage) Type() MessageType           { return MessageType2PCCommit }
func (m *TwoPCCommitMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *TwoPCCommitMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *TwoPCCommitMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// TwoPCRollbackMessage 2PC 回滚消息
type TwoPCRollbackMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Reason        string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

func (m *TwoPCRollbackMessage) Type() MessageType           { return MessageType2PCRollback }
func (m *TwoPCRollbackMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *TwoPCRollbackMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *TwoPCRollbackMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// TwoPCCommitReplyMessage 2PC 提交响应
type TwoPCCommitReplyMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Success       bool   `json:"success" msgpack:"success"`
}

func (m *TwoPCCommitReplyMessage) Type() MessageType           { return MessageType2PCCommitReply }
func (m *TwoPCCommitReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *TwoPCCommitReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *TwoPCCommitReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// TwoPCRollbackReplyMessage 2PC 回滚响应
type TwoPCRollbackReplyMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Success       bool   `json:"success" msgpack:"success"`
}

func (m *TwoPCRollbackReplyMessage) Type() MessageType           { return MessageType2PCRollbackReply }
func (m *TwoPCRollbackReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *TwoPCRollbackReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *TwoPCRollbackReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ========================================
// 节点管理消息（双标签实现）
// ========================================

// NodePingMessage 节点心跳消息
type NodePingMessage struct {
	NodeID    string `json:"node_id" msgpack:"node_id"`
	Sequence  int64  `json:"sequence" msgpack:"sequence"`
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"`
}

func (m *NodePingMessage) Type() MessageType           { return MessageTypeNodePing }
func (m *NodePingMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *NodePingMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *NodePingMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// NodePongMessage 心跳响应
type NodePongMessage struct {
	NodeID    string `json:"node_id" msgpack:"node_id"`
	Sequence  int64  `json:"sequence" msgpack:"sequence"`
	Status    string `json:"status" msgpack:"status"`       // "ready", "busy", "leaving"
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // Pong 发送时间戳（用于计算 RTT）
}

func (m *NodePongMessage) Type() MessageType           { return MessageTypeNodePong }
func (m *NodePongMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *NodePongMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *NodePongMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// NodeJoinMessage 节点加入消息
type NodeJoinMessage struct {
	NodeID   string `json:"node_id" msgpack:"node_id"`
	Addr     string `json:"addr" msgpack:"addr"`
	Role     string `json:"role" msgpack:"role"` // "parent", "child"
	ParentID string `json:"parent_id,omitempty" msgpack:"parent_id,omitempty"`
}

func (m *NodeJoinMessage) Type() MessageType           { return MessageTypeNodeJoin }
func (m *NodeJoinMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *NodeJoinMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *NodeJoinMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// NodeLeaveMessage 节点离开消息
type NodeLeaveMessage struct {
	NodeID string `json:"node_id" msgpack:"node_id"`
	Reason string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

func (m *NodeLeaveMessage) Type() MessageType           { return MessageTypeNodeLeave }
func (m *NodeLeaveMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *NodeLeaveMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *NodeLeaveMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// NodeSyncMessage 节点同步消息
type NodeSyncMessage struct {
	Version  uint64            `json:"version" msgpack:"version"`
	Metadata map[string][]byte `json:"metadata" msgpack:"metadata"`
}

func (m *NodeSyncMessage) Type() MessageType           { return MessageTypeNodeSync }
func (m *NodeSyncMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *NodeSyncMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *NodeSyncMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ClockSyncMessage 时钟同步请求消息
type ClockSyncMessage struct {
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // 发送节点的 HLC 时间戳
	NodeID    string `json:"node_id" msgpack:"node_id"`     // 发送节点 ID
}

func (m *ClockSyncMessage) Type() MessageType           { return MessageTypeClockSync }
func (m *ClockSyncMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *ClockSyncMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *ClockSyncMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ClockSyncReplyMessage 时钟同步响应消息
type ClockSyncReplyMessage struct {
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // 响应节点的 HLC 时间戳
	NodeID    string `json:"node_id" msgpack:"node_id"`     // 响应节点 ID
	Drift     int64  `json:"drift" msgpack:"drift"`         // 时间漂移（毫秒）
}

func (m *ClockSyncReplyMessage) Type() MessageType           { return MessageTypeClockSyncReply }
func (m *ClockSyncReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *ClockSyncReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *ClockSyncReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ========================================
// 集群管理消息（双标签实现）
// ========================================

// ClusterStatusMessage 集群状态查询
type ClusterStatusMessage struct {
	NodeID string `json:"node_id" msgpack:"node_id"`
}

func (m *ClusterStatusMessage) Type() MessageType           { return MessageTypeClusterStatus }
func (m *ClusterStatusMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *ClusterStatusMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *ClusterStatusMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ClusterStatusReplyMessage 集群状态响应
type ClusterStatusReplyMessage struct {
	Nodes []NodeInfo `json:"nodes" msgpack:"nodes"`
}

// NodeInfo 节点信息（双标签实现）
type NodeInfo struct {
	NodeID   string `json:"node_id" msgpack:"node_id"`
	Addr     string `json:"addr" msgpack:"addr"`
	Role     string `json:"role" msgpack:"role"`
	ParentID string `json:"parent_id,omitempty" msgpack:"parent_id,omitempty"`
	Status   string `json:"status" msgpack:"status"` // "ready", "busy", "leaving"
	Level    int    `json:"level" msgpack:"level"`
}

func (m *ClusterStatusReplyMessage) Type() MessageType           { return MessageTypeClusterStatusReply }
func (m *ClusterStatusReplyMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *ClusterStatusReplyMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *ClusterStatusReplyMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// LeaderElectionMessage Leader 选举消息
type LeaderElectionMessage struct {
	ElectionID string `json:"election_id" msgpack:"election_id"`
	NodeID     string `json:"node_id" msgpack:"node_id"`
	Priority   int    `json:"priority" msgpack:"priority"`
}

func (m *LeaderElectionMessage) Type() MessageType           { return MessageTypeLeaderElection }
func (m *LeaderElectionMessage) Marshal() ([]byte, error)    { return msgpack.Marshal(m) }
func (m *LeaderElectionMessage) Unmarshal(data []byte) error { return msgpack.Unmarshal(data, m) }
func (m *LeaderElectionMessage) Size() int {
	bytes, _ := m.Marshal()
	return len(bytes)
}

// ========================================
// 编解码器工具函数
// ========================================

// EncodeFrame 编码消息为帧
//
// 将消息编码并封装为完整帧
func EncodeFrame(msg Message) (*Frame, error) {
	codec := NewMessagePackCodec()

	// 编码消息
	data, err := codec.Encode(msg)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("EncodeFrame", err)
	}

	// 创建帧 (使用 MessagePack 编解码器)
	frame := NewFrame(msg.Type(), types.CodecTypeMessagePack, data)
	return frame, nil
}

// DecodeFrame 从帧解码消息
//
// 从完整帧中解码出消息
func DecodeFrame(frame *Frame) (Message, error) {
	if frame == nil {
		return nil, types.NewCodecInvalidMessageError("帧为空")
	}

	// 根据帧中的编解码器类型选择编解码器
	codec, err := NewCodec(frame.CodecType)
	if err != nil {
		return nil, err
	}

	// 解码消息
	msg, err := codec.Decode(frame.Data)
	if err != nil {
		return nil, types.NewCodecDecodeFailedError("DecodeFrame", err)
	}

	return msg, nil
}

// ========================================
// Reader/Writer 辅助函数
// ========================================

// MessageReader 消息读取器
//
// 用于从连接中读取消息
type MessageReader struct {
	frameReader *FrameReader
	codec       Codec
}

// NewMessageReader 创建消息读取器
func NewMessageReader(r io.Reader, codec Codec) *MessageReader {
	if codec == nil {
		codec = NewMessagePackCodec()
	}

	return &MessageReader{
		frameReader: NewFrameReader(r),
		codec:       codec,
	}
}

// ReadMessage 读取一条消息
func (mr *MessageReader) ReadMessage() (Message, error) {
	// 读取帧
	frame, err := mr.frameReader.ReadFrame()
	if err != nil {
		return nil, err
	}

	// 根据帧中的编解码器类型选择编解码器
	codec, err := NewCodec(frame.CodecType)
	if err != nil {
		return nil, err
	}

	// 解码消息
	msg, err := codec.Decode(frame.Data)
	if err != nil {
		return nil, types.NewCodecDecodeFailedError("ReadMessage", err)
	}

	return msg, nil
}

// MessageWriter 消息写入器
//
// 用于向连接写入消息
type MessageWriter struct {
	frameWriter *FrameWriter
	codec       Codec
}

// NewMessageWriter 创建消息写入器
func NewMessageWriter(w io.Writer, codec Codec) *MessageWriter {
	if codec == nil {
		codec = NewMessagePackCodec()
	}

	return &MessageWriter{
		frameWriter: NewFrameWriter(w),
		codec:       codec,
	}
}

// WriteMessage 写入一条消息
func (mw *MessageWriter) WriteMessage(msg Message) error {
	// 编码消息
	data, err := mw.codec.Encode(msg)
	if err != nil {
		return types.NewCodecEncodeFailedError("WriteMessage", err)
	}

	// 创建帧（使用对应的编解码器类型）
	frame := NewFrame(msg.Type(), types.CodecTypeMessagePack, data)

	// 写入帧
	if err := mw.frameWriter.WriteFrame(frame); err != nil {
		return types.NewOpErr(types.ErrCodeTransport, "WriteFrame", "写入帧失败", err)
	}

	return nil
}
