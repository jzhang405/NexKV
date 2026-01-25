// Package transport 编解码实现
//
// 支持多种编解码器：MessagePack（默认）和 JSON（兼容性）
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
// 默认使用 MessagePack，原因：
//  1. 性能优异（二进制格式，紧凑高效）
//  2. Schema 明确，跨语言支持好
//  3. Wrapper 模式无语义丢失
//  4. 无需额外的 .proto 文件和代码生成步骤
const defaultCodec = types.CodecTypeMessagePack

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
	default:
		return nil, types.NewStoreInvalidParameterError("不支持的编解码器类型")
	}
}

// ========================================
// 元数据操作消息（使用 BaseMessage 消除重复代码）
// ========================================

// GetMessage 获取元数据消息
type GetMessage struct {
	BaseMessage
	Key string `json:"key" msgpack:"key"`
}

// PutMessage 更新元数据消息
type PutMessage struct {
	BaseMessage
	Key   string `json:"key" msgpack:"key"`
	Value []byte `json:"value" msgpack:"value"`
}

// DeleteMessage 删除元数据消息
type DeleteMessage struct {
	BaseMessage
	Key string `json:"key" msgpack:"key"`
}

// GetReplyMessage Get 响应消息
type GetReplyMessage struct {
	BaseMessage
	Key     string `json:"key" msgpack:"key"`
	Value   []byte `json:"value" msgpack:"value"`
	Found   bool   `json:"found" msgpack:"found"`
	Version uint64 `json:"version" msgpack:"version"`
}

// PutReplyMessage Put 响应消息
type PutReplyMessage struct {
	BaseMessage
	Key     string `json:"key" msgpack:"key"`
	Success bool   `json:"success" msgpack:"success"`
	Version uint64 `json:"version" msgpack:"version"`
}

// DeleteReplyMessage Delete 响应消息
type DeleteReplyMessage struct {
	BaseMessage
	Key     string `json:"key" msgpack:"key"`
	Success bool   `json:"success" msgpack:"success"`
}

// ========================================
// Gossip 协议消息（使用 BaseMessage 消除重复代码）
// ========================================

// GossipSyncMessage Gossip 同步消息
type GossipSyncMessage struct {
	BaseMessage
	Version   uint64            `json:"version" msgpack:"version"`
	Metadata  map[string][]byte `json:"metadata" msgpack:"metadata"`
	Timestamp int64             `json:"timestamp" msgpack:"timestamp"`
}

// GossipSyncReplyMessage Gossip 同步响应
type GossipSyncReplyMessage struct {
	BaseMessage
	Accepted bool   `json:"accepted" msgpack:"accepted"`
	Version  uint64 `json:"version" msgpack:"version"`
}

// GossipDigestMessage Gossip 摘要消息
type GossipDigestMessage struct {
	BaseMessage
	Version uint64            `json:"version" msgpack:"version"`
	Digest  map[string]uint64 `json:"digest" msgpack:"digest"`
}

// GossipDigestReplyMessage Gossip 摘要响应
type GossipDigestReplyMessage struct {
	BaseMessage
	Version uint64            `json:"version" msgpack:"version"`
	Digest  map[string]uint64 `json:"digest" msgpack:"digest"`
}

// ========================================
// Quorum 协议消息（使用 BaseMessage 消除重复代码）
// ========================================

// QuorumProposeMessage Quorum 提案消息
type QuorumProposeMessage struct {
	BaseMessage
	ProposalID string `json:"proposal_id" msgpack:"proposal_id"`
	Key        string `json:"key" msgpack:"key"`
	Value      []byte `json:"value" msgpack:"value"`
	Operation  string `json:"operation" msgpack:"operation"` // "put", "delete"
	Proposer   string `json:"proposer" msgpack:"proposer"`
	Timestamp  int64  `json:"timestamp" msgpack:"timestamp"`
}

// QuorumVoteMessage Quorum 投票消息
type QuorumVoteMessage struct {
	BaseMessage
	ProposalID string `json:"proposal_id" msgpack:"proposal_id"`
	Voter      string `json:"voter" msgpack:"voter"`
	Vote       bool   `json:"vote" msgpack:"vote"`
	Reason     string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

// QuorumDecideMessage Quorum 决策消息
type QuorumDecideMessage struct {
	BaseMessage
	ProposalID string `json:"proposal_id" msgpack:"proposal_id"`
	Approved   bool   `json:"approved" msgpack:"approved"`
	Version    uint64 `json:"version" msgpack:"version"`
}

// ========================================
// 2PC 协议消息（使用 BaseMessage 消除重复代码）
// ========================================

// Operation 操作定义（用于 TwoPCPrepareMessage）
type Operation struct {
	Type  string `json:"type" msgpack:"type"` // "put", "delete"
	Key   string `json:"key" msgpack:"key"`
	Value []byte `json:"value,omitempty" msgpack:"value,omitempty"`
}

// TwoPCPrepareMessage 2PC 准备阶段消息
type TwoPCPrepareMessage struct {
	BaseMessage
	TransactionID string      `json:"transaction_id" msgpack:"transaction_id"`
	Participants  []string    `json:"participants" msgpack:"participants"`
	Operations    []Operation `json:"operations" msgpack:"operations"`
	Timeout       int64       `json:"timeout" msgpack:"timeout"`
}

// TwoPCPrepareReplyMessage 2PC 准备响应
type TwoPCPrepareReplyMessage struct {
	BaseMessage
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Vote          string `json:"vote" msgpack:"vote"` // "commit", "abort"
	Reason        string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

// TwoPCCommitMessage 2PC 提交消息
type TwoPCCommitMessage struct {
	BaseMessage
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
}

// TwoPCRollbackMessage 2PC 回滚消息
type TwoPCRollbackMessage struct {
	BaseMessage
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Reason        string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

// TwoPCCommitReplyMessage 2PC 提交响应
type TwoPCCommitReplyMessage struct {
	BaseMessage
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Success       bool   `json:"success" msgpack:"success"`
}

// TwoPCRollbackReplyMessage 2PC 回滚响应
type TwoPCRollbackReplyMessage struct {
	BaseMessage
	TransactionID string `json:"transaction_id" msgpack:"transaction_id"`
	Participant   string `json:"participant" msgpack:"participant"`
	Success       bool   `json:"success" msgpack:"success"`
}

// ========================================
// 节点管理消息（使用 BaseMessage 消除重复代码）
// ========================================

// NodePingMessage 节点心跳消息
type NodePingMessage struct {
	BaseMessage
	NodeID    string `json:"node_id" msgpack:"node_id"`
	Sequence  int64  `json:"sequence" msgpack:"sequence"`
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"`
}

// NodePongMessage 心跳响应
type NodePongMessage struct {
	BaseMessage
	NodeID    string `json:"node_id" msgpack:"node_id"`
	Sequence  int64  `json:"sequence" msgpack:"sequence"`
	Status    string `json:"status" msgpack:"status"`       // "ready", "busy", "leaving"
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // Pong 发送时间戳（用于计算 RTT）
}

// NodeJoinMessage 节点加入消息
type NodeJoinMessage struct {
	BaseMessage
	NodeID   string `json:"node_id" msgpack:"node_id"`
	Addr     string `json:"addr" msgpack:"addr"`
	Role     string `json:"role" msgpack:"role"` // "parent", "child"
	ParentID string `json:"parent_id,omitempty" msgpack:"parent_id,omitempty"`
}

// NodeLeaveMessage 节点离开消息
type NodeLeaveMessage struct {
	BaseMessage
	NodeID string `json:"node_id" msgpack:"node_id"`
	Reason string `json:"reason,omitempty" msgpack:"reason,omitempty"`
}

// NodeSyncMessage 节点同步消息
type NodeSyncMessage struct {
	BaseMessage
	Version  uint64            `json:"version" msgpack:"version"`
	Metadata map[string][]byte `json:"metadata" msgpack:"metadata"`
}

// ClockSyncMessage 时钟同步请求消息
type ClockSyncMessage struct {
	BaseMessage
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // 发送节点的 HLC 时间戳
	NodeID    string `json:"node_id" msgpack:"node_id"`     // 发送节点 ID
}

// ClockSyncReplyMessage 时钟同步响应消息
type ClockSyncReplyMessage struct {
	BaseMessage
	Timestamp int64  `json:"timestamp" msgpack:"timestamp"` // 响应节点的 HLC 时间戳
	NodeID    string `json:"node_id" msgpack:"node_id"`     // 响应节点 ID
	Drift     int64  `json:"drift" msgpack:"drift"`         // 时间漂移（毫秒）
}

// ========================================
// 集群管理消息（使用 BaseMessage 消除重复代码）
// ========================================

// ClusterStatusMessage 集群状态查询
type ClusterStatusMessage struct {
	BaseMessage
	NodeID string `json:"node_id" msgpack:"node_id"`
}

// ClusterStatusReplyMessage 集群状态响应
type ClusterStatusReplyMessage struct {
	BaseMessage
	Nodes []NodeInfo `json:"nodes" msgpack:"nodes"`
}

// NodeInfo 节点信息（用于 ClusterStatusReplyMessage）
type NodeInfo struct {
	NodeID   string `json:"node_id" msgpack:"node_id"`
	Addr     string `json:"addr" msgpack:"addr"`
	Role     string `json:"role" msgpack:"role"`
	ParentID string `json:"parent_id,omitempty" msgpack:"parent_id,omitempty"`
	Status   string `json:"status" msgpack:"status"` // "ready", "busy", "leaving"
	Level    int    `json:"level" msgpack:"level"`
}

// LeaderElectionMessage Leader 选举消息
type LeaderElectionMessage struct {
	BaseMessage
	ElectionID       string `json:"election_id" msgpack:"election_id"`
	NodeID           string `json:"node_id" msgpack:"node_id"`
	ElectionPriority int    `json:"priority" msgpack:"priority"` // 选举优先级
}

// ========================================
// 消息工厂函数
// ========================================

// createMessageByType 根据消息类型创建消息实例（使用注册表）
func createMessageByType(msgType MessageType) (Message, error) {
	return createMessage(msgType)
}

// ========================================
// 消息注册表初始化
// ========================================

// init 初始化消息注册表
//
// 注册所有消息类型到注册表，替代 switch-case 工厂函数
func init() {
	// 元数据操作消息
	registerMessage(types.MessageTypeGet, func() Message { return &GetMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGet}} })
	registerMessage(types.MessageTypePut, func() Message { return &PutMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypePut}} })
	registerMessage(types.MessageTypeDelete, func() Message { return &DeleteMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeDelete}} })
	registerMessage(types.MessageTypeGetReply, func() Message {
		return &GetReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGetReply}}
	})
	registerMessage(types.MessageTypePutReply, func() Message {
		return &PutReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypePutReply}}
	})
	registerMessage(types.MessageTypeDeleteReply, func() Message {
		return &DeleteReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeDeleteReply}}
	})

	// Gossip 协议消息
	registerMessage(types.MessageTypeGossipSync, func() Message {
		return &GossipSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipSync}}
	})
	registerMessage(types.MessageTypeGossipSyncReply, func() Message {
		return &GossipSyncReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipSyncReply}}
	})
	registerMessage(types.MessageTypeGossipDigest, func() Message {
		return &GossipDigestMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipDigest}}
	})
	registerMessage(types.MessageTypeGossipDigestReply, func() Message {
		return &GossipDigestReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipDigestReply}}
	})

	// Quorum 协议消息
	registerMessage(types.MessageTypeQuorumPropose, func() Message {
		return &QuorumProposeMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumPropose}}
	})
	registerMessage(types.MessageTypeQuorumVote, func() Message {
		return &QuorumVoteMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumVote}}
	})
	registerMessage(types.MessageTypeQuorumDecide, func() Message {
		return &QuorumDecideMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumDecide}}
	})

	// 2PC 协议消息
	registerMessage(types.MessageType2PCPrepare, func() Message {
		return &TwoPCPrepareMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCPrepare}}
	})
	registerMessage(types.MessageType2PCPrepareReply, func() Message {
		return &TwoPCPrepareReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCPrepareReply}}
	})
	registerMessage(types.MessageType2PCCommit, func() Message {
		return &TwoPCCommitMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCCommit}}
	})
	registerMessage(types.MessageType2PCRollback, func() Message {
		return &TwoPCRollbackMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCRollback}}
	})
	registerMessage(types.MessageType2PCCommitReply, func() Message {
		return &TwoPCCommitReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCCommitReply}}
	})
	registerMessage(types.MessageType2PCRollbackReply, func() Message {
		return &TwoPCRollbackReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCRollbackReply}}
	})

	// 节点管理消息
	registerMessage(types.MessageTypeNodePing, func() Message {
		return &NodePingMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePing}}
	})
	registerMessage(types.MessageTypeNodePong, func() Message {
		return &NodePongMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePong}}
	})
	registerMessage(types.MessageTypeNodeJoin, func() Message {
		return &NodeJoinMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeJoin}}
	})
	registerMessage(types.MessageTypeNodeLeave, func() Message {
		return &NodeLeaveMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeLeave}}
	})
	registerMessage(types.MessageTypeNodeSync, func() Message {
		return &NodeSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeSync}}
	})
	registerMessage(types.MessageTypeClockSync, func() Message {
		return &ClockSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClockSync}}
	})
	registerMessage(types.MessageTypeClockSyncReply, func() Message {
		return &ClockSyncReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClockSyncReply}}
	})

	// 集群管理消息
	registerMessage(types.MessageTypeClusterStatus, func() Message {
		return &ClusterStatusMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClusterStatus}}
	})
	registerMessage(types.MessageTypeClusterStatusReply, func() Message {
		return &ClusterStatusReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClusterStatusReply}}
	})
	registerMessage(types.MessageTypeLeaderElection, func() Message {
		return &LeaderElectionMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeLeaderElection}}
	})
}

// ========================================
// 编解码器工具函数
// ========================================

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

	// 根据 Message.ExpectResponse() 计算 Flags（使用统一函数）
	flags := FlagsFromMessage(msg)

	// 创建帧（MsgType 现在在 FixedHeader 中）
	frame := NewFrame(nodeID, msgSeq, msg.Type(), uint16(defaultCodec), flags, data)

	// 计算 CRC32
	frame = frame.Finalize()

	return frame, nil
}

// DecodeFrame 从帧解码消息
//
// 从完整帧中解码出 MsgFrame（FixedHeader + TLVs + Message）
// MsgType 从 FixedHeader 中读取，TLV 字段懒加载解码
func DecodeFrame(frame *Frame) (*MsgFrame, error) {
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

	// 构建 MsgFrame（TLV 字段将在首次访问时懒加载解码）
	msgFrame := &MsgFrame{
		FixedHeader: *frame.FixedHeader,
		Message:     msg,
		TLVs:        make([]TLV, 0, len(frame.VarExtHeader.Fields)),
	}

	// 遍历所有 TLV 字段并添加到 MsgFrame
	// 注意：字段不会立即解析，而是在首次访问 GetExt() 时才解码
	for _, field := range frame.VarExtHeader.Fields {
		msgFrame.TLVs = append(msgFrame.TLVs, *field)
	}

	return msgFrame, nil
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
	msgFrame, err := DecodeFrame(frame)
	if err != nil {
		return nil, err
	}

	return msgFrame.Message, nil
}

// ReadMsgFrame 读取一条消息帧（FixedHeader + TLVs + Message）
func (mr *MessageReader) ReadMsgFrame() (*MsgFrame, error) {
	// 读取帧
	frame, err := mr.frameReader.ReadFrame()
	if err != nil {
		return nil, err
	}

	// 使用 DecodeFrame 解码（直接返回 *MsgFrame）
	return DecodeFrame(frame)
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
//
// 参数:
//   - msg: 要写入的消息
//   - nodeID: 发送节点 ID
//   - msgSeq: 消息序列号
func (mw *MessageWriter) WriteMessage(msg Message, nodeID uint64, msgSeq uint64) error {
	return mw.WriteMessageWithOptions(msg, nodeID, msgSeq, nil)
}

// WriteMessageWithOptions 写入一条消息（带 TLV 扩展选项）
//
// 参数:
//   - msg: 要写入的消息
//   - nodeID: 发送节点 ID
//   - msgSeq: 消息序列号
//   - opts: 发送选项（hopCount, compressID, encryptID, priority）
func (mw *MessageWriter) WriteMessageWithOptions(msg Message, nodeID uint64, msgSeq uint64, opts *sendOptions) error {
	// 使用 EncodeFrame 编码基础帧
	frame, err := EncodeFrame(msg, nodeID, msgSeq)
	if err != nil {
		return err
	}

	// 应用 TLV 扩展字段
	if opts != nil {
		if opts.hopCount != nil {
			// 设置 Hops 字段（FixedHeader）
			frame.FixedHeader.Hops = uint8(*opts.hopCount)
		}
		if opts.compressID != nil {
			frame.WithCompress(*opts.compressID)
		}
		if opts.encryptID != nil {
			// 安全检查：不允许使用空 nonce
			// 加密扩展必须显式指定 nonce，调用方应使用 Frame.WithEncrypt() 方法
			return types.NewOpErr(types.ErrCodeInternal, "WriteMessageWithOptions",
				"加密扩展需要显式指定 nonce，请使用 Frame.WithEncrypt() 方法或移除加密选项", nil)
		}
	}

	// 完成构建并计算 CRC32
	frame.Finalize()

	// 写入帧
	if err := mw.frameWriter.WriteFrame(frame); err != nil {
		return types.NewOpErr(types.ErrCodeTransport, "WriteFrame", "写入帧失败", err)
	}

	return nil
}
