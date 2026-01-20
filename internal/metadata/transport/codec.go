// Package transport 编解码实现
//
// 支持多种编解码器：Protobuf（默认）、MessagePack 和 JSON（兼容性）
package transport

import (
	"encoding/json"
	"io"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// 默认编解码器配置
// ========================================

// defaultCodec 系统默认编解码器
//
// 默认使用 Protobuf，原因：
//  1. 性能优异（benchmark 显示数据最小）
//  2. Schema 明确，跨语言支持好
//  3. Wrapper 模式无语义丢失
const defaultCodec = types.CodecTypeProtobuf

// ========================================
// MessagePack 编解码器实现
// ========================================

// MessagePackCodec MessagePack 编解码器
//
// 特点：
//   - 高效二进制序列化
//   - 自动处理 Go 类型到 MessagePack 类型的映射
//   - 支持嵌套结构和切片
//   - Encode/DecodeInto: 纯 MessagePack 数据（类型在 FixedHeader 中）
type MessagePackCodec struct{}

// NewMessagePackCodec 创建 MessagePack 编解码器
func NewMessagePackCodec() *MessagePackCodec {
	return &MessagePackCodec{}
}

// Encode 编码消息
//
// 将消息编码为纯 MessagePack 格式（不包含类型，类型在 FixedHeader 中）
func (c *MessagePackCodec) Encode(msg Message) ([]byte, error) {
	if msg == nil {
		return nil, types.NewCodecInvalidMessageError("消息为空")
	}

	// 序列化消息（纯 MessagePack 数据）
	data, err := msgpack.Marshal(msg)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("msgpack", err)
	}

	return data, nil
}

// Decode 解码消息
//
// 从纯 MessagePack 格式数据解码消息
// msgType 参数指定消息类型
func (c *MessagePackCodec) Decode(msgType MessageType, data []byte) (Message, error) {
	// 创建消息实例
	msg, err := createMessageByType(msgType)
	if err != nil {
		return nil, err
	}

	// 解码数据到消息实例
	if err := msgpack.Unmarshal(data, msg); err != nil {
		return nil, types.NewCodecDecodeFailedError("msgpack", err)
	}

	return msg, nil
}

// DecodeInto 解码消息到指定实例
//
// 从纯 MessagePack 格式数据解码到预先创建的消息实例
func (c *MessagePackCodec) DecodeInto(data []byte, msg Message) error {
	if msg == nil {
		return types.NewCodecInvalidMessageError("消息实例为空")
	}
	if len(data) == 0 {
		return types.NewCodecInvalidDataError("DecodeInto", "数据为空")
	}

	// 解码数据到消息实例
	if err := msgpack.Unmarshal(data, msg); err != nil {
		return types.NewCodecDecodeFailedError("msgpack", err)
	}

	return nil
}

// Name 返回编解码器名称
func (c *MessagePackCodec) Name() string {
	return "msgpack"
}

// Type 返回编解码器类型
func (c *MessagePackCodec) Type() types.CodecType {
	return types.CodecTypeMessagePack
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
//   - Encode/Decode/DecodeInto: 纯 JSON 序列化数据（类型在 FixedHeader 中）
type JSONCodec struct{}

// NewJSONCodec 创建 JSON 编解码器
func NewJSONCodec() *JSONCodec {
	return &JSONCodec{}
}

// Encode 编码消息（JSON 格式）
//
// 将消息编码为纯 JSON 格式（不包含类型，类型在 FixedHeader 中）
func (c *JSONCodec) Encode(msg Message) ([]byte, error) {
	if msg == nil {
		return nil, types.NewCodecInvalidMessageError("消息为空")
	}

	// 序列化消息（纯 JSON 数据）
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("json", err)
	}

	return data, nil
}

// Decode 解码消息
//
// 从纯 JSON 格式数据解码消息
// msgType 参数指定消息类型（从 FixedHeader 获取）
func (c *JSONCodec) Decode(msgType MessageType, data []byte) (Message, error) {
	// 创建消息实例
	msg, err := createMessageByType(msgType)
	if err != nil {
		return nil, err
	}

	// 解码数据
	if err := json.Unmarshal(data, msg); err != nil {
		return nil, types.NewCodecDecodeFailedError("json", err)
	}

	return msg, nil
}

// DecodeInto 解码消息到指定实例
//
// 从纯 JSON 格式数据解码到预先创建的消息实例
func (c *JSONCodec) DecodeInto(data []byte, msg Message) error {
	if msg == nil {
		return types.NewCodecInvalidMessageError("消息实例为空")
	}
	if len(data) == 0 {
		return types.NewCodecInvalidDataError("DecodeInto", "数据为空")
	}

	// 解码数据到消息实例
	if err := json.Unmarshal(data, msg); err != nil {
		return types.NewCodecDecodeFailedError("json", err)
	}

	return nil
}

// Name 返回编解码器名称
func (c *JSONCodec) Name() string {
	return "json"
}

// Type 返回编解码器类型
func (c *JSONCodec) Type() types.CodecType {
	return types.CodecTypeJSON
}

// ========================================
// 编解码器工厂函数
// ========================================

// NewCodec 根据类型创建编解码器
func NewCodec(codecType types.CodecType) (Codec, error) {
	switch codecType {
	case types.CodecTypeMessagePack:
		return NewMessagePackCodec(), nil
	case types.CodecTypeJSON:
		return NewJSONCodec(), nil
	case types.CodecTypeProtobuf:
		return NewProtobufCodec(), nil
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

// PutMessage 更新元数据消息
type PutMessage struct {
	Key   string `json:"key" msgpack:"key"`
	Value []byte `json:"value" msgpack:"value"`
}

func (m *PutMessage) Type() MessageType { return MessageTypePut }

// DeleteMessage 删除元数据消息
type DeleteMessage struct {
	Key string `json:"key" msgpack:"key"`
}

func (m *DeleteMessage) Type() MessageType { return MessageTypeDelete }

// GetReplyMessage Get 响应消息
type GetReplyMessage struct {
	Key     string `json:"key" msgpack:"key"`
	Value   []byte `json:"value" msgpack:"value"`
	Found   bool   `json:"found" msgpack:"found"`
	Version uint64 `json:"version" msgpack:"version"`
}

func (m *GetReplyMessage) Type() MessageType { return MessageTypeGetReply }

// PutReplyMessage Put 响应消息
type PutReplyMessage struct {
	Key     string `json:"key" msgpack:"key"`
	Success bool   `json:"success" msgpack:"success"`
	Version uint64 `json:"version" msgpack:"version"`
}

func (m *PutReplyMessage) Type() MessageType { return MessageTypePutReply }

// DeleteReplyMessage Delete 响应消息
type DeleteReplyMessage struct {
	Key     string `json:"key" msgpack:"key"`
	Success bool   `json:"success" msgpack:"success"`
}

func (m *DeleteReplyMessage) Type() MessageType { return MessageTypeDeleteReply }

// ========================================
// Gossip 协议消息（双标签实现）
// ========================================

// GossipSyncMessage Gossip 同步消息
type GossipSyncMessage struct {
	Version   uint64            `json:"version" msgpack:"version"`
	Metadata  map[string][]byte `json:"metadata" msgpack:"metadata"`
	Timestamp int64             `json:"timestamp" msgpack:"timestamp"`
}

func (m *GossipSyncMessage) Type() MessageType { return MessageTypeGossipSync }

// GossipSyncReplyMessage Gossip 同步响应
type GossipSyncReplyMessage struct {
	Accepted bool   `json:"accepted" msgpack:"accepted"`
	Version  uint64 `json:"version" msgpack:"version"`
}

func (m *GossipSyncReplyMessage) Type() MessageType { return MessageTypeGossipSyncReply }

// GossipDigestMessage Gossip 摘要消息
type GossipDigestMessage struct {
	Version uint64            `json:"version" msgpack:"version"`
	Digest  map[string]uint64 `json:"digest" msgpack:"digest"`
}

func (m *GossipDigestMessage) Type() MessageType { return MessageTypeGossipDigest }

// GossipDigestReplyMessage Gossip 摘要响应
type GossipDigestReplyMessage struct {
	Version uint64            `json:"version" msgpack:"version"`
	Digest  map[string]uint64 `json:"digest" msgpack:"digest"`
}

func (m *GossipDigestReplyMessage) Type() MessageType { return MessageTypeGossipDigestReply }

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

func (m *QuorumProposeMessage) Type() MessageType { return MessageTypeQuorumPropose }

// QuorumVoteMessage Quorum 投票消息
type QuorumVoteMessage struct {
	ProposalID string `json:"proposal_id" msgpack:"proposal_id"`
	Voter      string `json:"voter" msgpack:"voter"`
	Vote       bool   `json:"vote" msgpack:"vote"`
	Reason     string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

func (m *QuorumVoteMessage) Type() MessageType { return MessageTypeQuorumVote }

// QuorumDecideMessage Quorum 决策消息
type QuorumDecideMessage struct {
	ProposalID string `json:"proposal_id" msgpack:"proposal_id"`
	Approved   bool   `json:"approved" msgpack:"approved"`
	Version    uint64 `json:"version" msgpack:"version"`
}

func (m *QuorumDecideMessage) Type() MessageType { return MessageTypeQuorumDecide }

// ========================================
// 2PC 协议消息（双标签实现）
// ========================================

// TwoPCPrepareMessage 2PC 准备阶段消息
type TwoPCPrepareMessage struct {
	TransactionID string      `json:"transaction_id" msgpack:"transaction_id"`
	Participants  []string    `json:"participants" msgpack:"participants"`
	Operations    []Operation `json:"operations" msgpack:"operations"`
	Timeout       int64       `json:"timeout" msgpack:"timeout"`
}

// Operation 操作定义
type Operation struct {
	Type  string `json:"type" msgpack:"type"` // "put", "delete"
	Key   string `json:"key" msgpack:"key"`
	Value []byte `json:"value,omitempty" msgpack:"value,omitempty"`
}

func (m *TwoPCPrepareMessage) Type() MessageType { return MessageType2PCPrepare }

// TwoPCPrepareReplyMessage 2PC 准备响应
type TwoPCPrepareReplyMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Vote          string `json:"vote" msgpack:"vote"` // "commit", "abort"
	Reason        string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

func (m *TwoPCPrepareReplyMessage) Type() MessageType { return MessageType2PCPrepareReply }

// TwoPCCommitMessage 2PC 提交消息
type TwoPCCommitMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
}

func (m *TwoPCCommitMessage) Type() MessageType { return MessageType2PCCommit }

// TwoPCRollbackMessage 2PC 回滚消息
type TwoPCRollbackMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Reason        string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

func (m *TwoPCRollbackMessage) Type() MessageType { return MessageType2PCRollback }

// TwoPCCommitReplyMessage 2PC 提交响应
type TwoPCCommitReplyMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Success       bool   `json:"success" msgpack:"success"`
}

func (m *TwoPCCommitReplyMessage) Type() MessageType { return MessageType2PCCommitReply }

// TwoPCRollbackReplyMessage 2PC 回滚响应
type TwoPCRollbackReplyMessage struct {
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Success       bool   `json:"success" msgpack:"success"`
}

func (m *TwoPCRollbackReplyMessage) Type() MessageType { return MessageType2PCRollbackReply }

// ========================================
// 节点管理消息（双标签实现）
// ========================================

// NodePingMessage 节点心跳消息
type NodePingMessage struct {
	NodeID    string `json:"node_id" msgpack:"node_id"`
	Sequence  int64  `json:"sequence" msgpack:"sequence"`
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"`
}

func (m *NodePingMessage) Type() MessageType { return MessageTypeNodePing }

// NodePongMessage 心跳响应
type NodePongMessage struct {
	NodeID    string `json:"node_id" msgpack:"node_id"`
	Sequence  int64  `json:"sequence" msgpack:"sequence"`
	Status    string `json:"status" msgpack:"status"`       // "ready", "busy", "leaving"
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // Pong 发送时间戳（用于计算 RTT）
}

func (m *NodePongMessage) Type() MessageType { return MessageTypeNodePong }

// NodeJoinMessage 节点加入消息
type NodeJoinMessage struct {
	NodeID   string `json:"node_id" msgpack:"node_id"`
	Addr     string `json:"addr" msgpack:"addr"`
	Role     string `json:"role" msgpack:"role"` // "parent", "child"
	ParentID string `json:"parent_id,omitempty" msgpack:"parent_id,omitempty"`
}

func (m *NodeJoinMessage) Type() MessageType { return MessageTypeNodeJoin }

// NodeLeaveMessage 节点离开消息
type NodeLeaveMessage struct {
	NodeID string `json:"node_id" msgpack:"node_id"`
	Reason string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

func (m *NodeLeaveMessage) Type() MessageType { return MessageTypeNodeLeave }

// NodeSyncMessage 节点同步消息
type NodeSyncMessage struct {
	Version  uint64            `json:"version" msgpack:"version"`
	Metadata map[string][]byte `json:"metadata" msgpack:"metadata"`
}

func (m *NodeSyncMessage) Type() MessageType { return MessageTypeNodeSync }

// ClockSyncMessage 时钟同步请求消息
type ClockSyncMessage struct {
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // 发送节点的 HLC 时间戳
	NodeID    string `json:"node_id" msgpack:"node_id"`     // 发送节点 ID
}

func (m *ClockSyncMessage) Type() MessageType { return MessageTypeClockSync }

// ClockSyncReplyMessage 时钟同步响应消息
type ClockSyncReplyMessage struct {
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // 响应节点的 HLC 时间戳
	NodeID    string `json:"node_id" msgpack:"node_id"`     // 响应节点 ID
	Drift     int64  `json:"drift" msgpack:"drift"`         // 时间漂移（毫秒）
}

func (m *ClockSyncReplyMessage) Type() MessageType { return MessageTypeClockSyncReply }

// ========================================
// 集群管理消息（双标签实现）
// ========================================

// ClusterStatusMessage 集群状态查询
type ClusterStatusMessage struct {
	NodeID string `json:"node_id" msgpack:"node_id"`
}

func (m *ClusterStatusMessage) Type() MessageType { return MessageTypeClusterStatus }

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

func (m *ClusterStatusReplyMessage) Type() MessageType { return MessageTypeClusterStatusReply }

// LeaderElectionMessage Leader 选举消息
type LeaderElectionMessage struct {
	ElectionID string `json:"election_id" msgpack:"election_id"`
	NodeID     string `json:"node_id" msgpack:"node_id"`
	Priority   int    `json:"priority" msgpack:"priority"`
}

func (m *LeaderElectionMessage) Type() MessageType { return MessageTypeLeaderElection }

// ========================================
// 编解码器工具函数
// ========================================

// DecodedMessage 解码后的消息
type DecodedMessage struct {
	Msg    Message // 解码后的消息
	NodeID uint64  // 发送节点 ID
	MsgSeq uint64  // 消息序列号
}

// EncodeFrame 编码消息为帧
//
// 将消息编码并封装为完整帧
// MsgType 会被写入 FixedHeader，数据部分只包含序列化后的消息体
func EncodeFrame(msg Message, nodeID uint64, msgSeq uint64) (*Frame, error) {
	codec, err := NewCodec(defaultCodec)
	if err != nil {
		return nil, err
	}

	// 编码消息（不包含类型，类型在 FixedHeader 中）
	data, err := codec.Encode(msg)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("EncodeFrame", err)
	}

	// 创建帧（MsgType 现在在 FixedHeader 中）
	frame := NewFrame(nodeID, msgSeq, msg.Type(), uint16(defaultCodec), data)

	// 计算 CRC32
	frame = frame.Finalize()

	return frame, nil
}

// DecodeFrame 从帧解码消息
//
// 从完整帧中解码出消息，同时返回 nodeID 和 msgSeq
// MsgType 从 FixedHeader 中读取
func DecodeFrame(frame *Frame) (*DecodedMessage, error) {
	if frame == nil {
		return nil, types.NewCodecInvalidMessageError("帧为空")
	}

	// 根据帧中的编解码器类型选择编解码器
	codec, err := NewCodec(types.CodecType(frame.FixedHeader.CodecID))
	if err != nil {
		return nil, err
	}

	// 从 FixedHeader 获取 MsgType
	msgType := frame.FixedHeader.MsgType

	// 创建对应类型的消息实例
	msg, err := createMessageByType(msgType)
	if err != nil {
		return nil, err
	}

	// 使用编解码器解码数据（数据不包含类型）
	if err := codec.DecodeInto(frame.Data, msg); err != nil {
		return nil, types.NewCodecDecodeFailedError("DecodeFrame", err)
	}

	// 封装解码结果
	return &DecodedMessage{
		Msg:    msg,
		NodeID: frame.FixedHeader.NodeID,
		MsgSeq: frame.FixedHeader.MsgSeq,
	}, nil
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
		var err error
		codec, err = NewCodec(defaultCodec)
		if err != nil {
			return nil
		}
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

	// 使用 DecodeFrame 解码
	decoded, err := DecodeFrame(frame)
	if err != nil {
		return nil, err
	}

	return decoded.Msg, nil
}

// MessageWriter 消息写入器
type MessageWriter struct {
	frameWriter *FrameWriter
	codec       Codec
}

// NewMessageWriter 创建消息写入器
func NewMessageWriter(w io.Writer, codec Codec) *MessageWriter {
	if codec == nil {
		var err error
		codec, err = NewCodec(defaultCodec)
		if err != nil {
			return nil
		}
	}

	return &MessageWriter{
		frameWriter: NewFrameWriter(w),
		codec:       codec,
	}
}

// WriteMessage 写入一条消息
func (mw *MessageWriter) WriteMessage(msg Message) error {
	// 使用 EncodeFrame 编码
	frame, err := EncodeFrame(msg, 0, 0)
	if err != nil {
		return err
	}

	// 写入帧
	if err := mw.frameWriter.WriteFrame(frame); err != nil {
		return types.NewOpErr(types.ErrCodeTransport, "WriteFrame", "写入帧失败", err)
	}

	return nil
}
