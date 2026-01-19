// Package transport Transport 层 Protobuf 编解码器实现（简化版）
//
// 提供 Transport 消息的 Protobuf 序列化/反序列化能力
// 特点：
//   - protoc 预编译，性能最优
//   - 数据大小最小，节省网络带宽
//   - Schema 明确，跨语言支持好
//
// 注意：当前实现为简化版，直接序列化各个消息类型，不使用 TransportMessage 包装器
// Protobuf schema 与 Go 消息结构存在差异，需要做字段映射
package transport

import (
	"encoding/binary"
	"encoding/json"

	"github.com/jzhang405/NexKV/internal/metadata/proto"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	googleproto "google.golang.org/protobuf/proto"
)

// ========================================
// ProtobufCodec 实现
// ========================================

// ProtobufCodec Protobuf 编解码器
//
// 特点：
//   - protoc 预编译，性能最优（编码/解码约为 JSON 的 3-5 倍）
//   - 数据大小最小，约为 JSON 的 40-60%
//   - Schema 明确，跨语言支持好
//   - 推荐用于高性能场景
type ProtobufCodec struct{}

// NewProtobufCodec 创建 Protobuf 编解码器
func NewProtobufCodec() *ProtobufCodec {
	return &ProtobufCodec{}
}

// Encode 编码消息（Protobuf 格式）
//
// 将消息编码为 Protobuf 格式的字节流
// 格式: [Type:2字节][DataLen:4字节][Data:N字节]
func (c *ProtobufCodec) Encode(msg Message) ([]byte, error) {
	if msg == nil {
		return nil, types.NewCodecInvalidMessageError("消息为空")
	}

	// 将 Transport Message 转换为 Protobuf 消息并编码
	dataBytes, err := c.encodeMessage(msg)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("Protobuf", err)
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

// Decode 解码消息（Protobuf 格式）
//
// 从 Protobuf 格式的字节流解码消息
// 格式: [Type:2字节][DataLen:4字节][Data:N字节]
func (c *ProtobufCodec) Decode(data []byte) (Message, error) {
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

	// 解码消息
	msg, err := c.decodeMessage(msgType, data[6:6+dataLen])
	if err != nil {
		return nil, types.NewCodecDecodeFailedError("Protobuf", err)
	}

	return msg, nil
}

// Name 返回编解码器名称
func (c *ProtobufCodec) Name() string {
	return "protobuf"
}

// ========================================
// 消息编解码函数
// ========================================

// encodeMessage 将消息编码为 Protobuf 字节流
func (c *ProtobufCodec) encodeMessage(msg Message) ([]byte, error) {
	switch m := msg.(type) {
	case *GetMessage:
		pbMsg := &proto.MetadataOpMessage{
			Op:  proto.MetadataOpMessage_OP_TYPE_GET,
			Key: m.Key,
		}
		return googleproto.Marshal(pbMsg)

	case *PutMessage:
		pbMsg := &proto.MetadataOpMessage{
			Op:    proto.MetadataOpMessage_OP_TYPE_PUT,
			Key:   m.Key,
			Value: m.Value,
		}
		return googleproto.Marshal(pbMsg)

	case *DeleteMessage:
		pbMsg := &proto.MetadataOpMessage{
			Op:  proto.MetadataOpMessage_OP_TYPE_DELETE,
			Key: m.Key,
		}
		return googleproto.Marshal(pbMsg)

	case *GetReplyMessage:
		pbMsg := &proto.ResponseMessage{
			Success: m.Found,
			Data:    m.Value,
		}
		return googleproto.Marshal(pbMsg)

	case *PutReplyMessage:
		pbMsg := &proto.ResponseMessage{
			Success: m.Success,
			Data:    nil,
		}
		return googleproto.Marshal(pbMsg)

	case *DeleteReplyMessage:
		pbMsg := &proto.ResponseMessage{
			Success: m.Success,
			Data:    nil,
		}
		return googleproto.Marshal(pbMsg)

	case *GossipSyncMessage:
		pbMsg := &proto.GossipMessage{
			Version:  m.Version,
			Metadata: m.Metadata,
		}
		return googleproto.Marshal(pbMsg)

	case *GossipSyncReplyMessage:
		pbMsg := &proto.GossipMessage{
			Version:  m.Version,
			Metadata: make(map[string][]byte),
		}
		// 将 Accepted 转换为 metadata
		if m.Accepted {
			pbMsg.Metadata["accepted"] = []byte{1}
		}
		return googleproto.Marshal(pbMsg)

	case *GossipDigestMessage:
		pbMsg := &proto.GossipMessage{
			Version:  m.Version,
			Metadata: make(map[string][]byte),
		}
		// 将 Digest 映射需要转换
		for k, v := range m.Digest {
			// 将 uint64 转换为 8 字节
			buf := make([]byte, 8)
			binary.BigEndian.PutUint64(buf, v)
			pbMsg.Metadata[k] = buf
		}
		return googleproto.Marshal(pbMsg)

	case *GossipDigestReplyMessage:
		pbMsg := &proto.GossipMessage{
			Version:  m.Version,
			Metadata: make(map[string][]byte),
		}
		// 将 Digest 映射需要转换
		for k, v := range m.Digest {
			buf := make([]byte, 8)
			binary.BigEndian.PutUint64(buf, v)
			pbMsg.Metadata[k] = buf
		}
		return googleproto.Marshal(pbMsg)

	case *QuorumProposeMessage:
		pbMsg := &proto.QuorumMessage{
			ProposalId: m.ProposalID,
			Key:        m.Key,
			Value:      m.Value,
			Version:    uint64(m.Timestamp),
		}
		return googleproto.Marshal(pbMsg)

	case *QuorumVoteMessage:
		voteType := proto.QuorumMessage_VOTE_TYPE_UNSPECIFIED
		if m.Vote {
			voteType = proto.QuorumMessage_VOTE_TYPE_APPROVE
		}
		pbMsg := &proto.QuorumMessage{
			ProposalId: m.ProposalID,
			Vote:       voteType,
			Reason:     m.Reason,
		}
		return googleproto.Marshal(pbMsg)

	case *QuorumDecideMessage:
		pbMsg := &proto.QuorumMessage{
			ProposalId: m.ProposalID,
			Version:    m.Version,
		}
		return googleproto.Marshal(pbMsg)

	case *TwoPCPrepareMessage:
		// 转换 Operations 为 map[string][]byte
		operations := make(map[string][]byte)
		for _, op := range m.Operations {
			// 简化：将操作类型编码为值
			operations[op.Key] = []byte(op.Type)
		}
		pbMsg := &proto.TwoPCMessage{
			TransactionId: m.TransactionID,
			Shards:        m.Participants,
			Operations:    operations,
			Phase:         proto.TwoPCMessage_PHASE_TYPE_PREPARE,
		}
		return googleproto.Marshal(pbMsg)

	case *TwoPCPrepareReplyMessage:
		// 将 Vote ("commit"/"abort") 转换为 Decision 枚举
		decision := convertTwoPCDecisionTypeToProto(m.Vote)
		pbMsg := &proto.TwoPCMessage{
			TransactionId: m.TransactionID,
			Phase:         proto.TwoPCMessage_PHASE_TYPE_PREPARE,
			Decision:      decision,
			Reason:        m.Reason,
		}
		return googleproto.Marshal(pbMsg)

	case *TwoPCCommitMessage:
		pbMsg := &proto.TwoPCMessage{
			TransactionId: m.TransactionID,
			Phase:         proto.TwoPCMessage_PHASE_TYPE_COMMIT,
		}
		return googleproto.Marshal(pbMsg)

	case *TwoPCRollbackMessage:
		pbMsg := &proto.TwoPCMessage{
			TransactionId: m.TransactionID,
			Phase:         proto.TwoPCMessage_PHASE_TYPE_ROLLBACK,
			Reason:        m.Reason,
		}
		return googleproto.Marshal(pbMsg)

	case *TwoPCCommitReplyMessage:
		pbMsg := &proto.TwoPCMessage{
			TransactionId: m.TransactionID,
			Phase:         proto.TwoPCMessage_PHASE_TYPE_COMMIT,
		}
		return googleproto.Marshal(pbMsg)

	case *TwoPCRollbackReplyMessage:
		pbMsg := &proto.TwoPCMessage{
			TransactionId: m.TransactionID,
			Phase:         proto.TwoPCMessage_PHASE_TYPE_ROLLBACK,
		}
		return googleproto.Marshal(pbMsg)

	case *NodePingMessage:
		pbMsg := &proto.HeartbeatMessage{
			NodeId:    m.NodeID,
			Timestamp: uint64(m.Timestamp),
			Status:    proto.HeartbeatMessage_STATUS_TYPE_HEALTHY,
		}
		return googleproto.Marshal(pbMsg)

	case *NodePongMessage:
		pbMsg := &proto.HeartbeatMessage{
			NodeId:    m.NodeID,
			Timestamp: uint64(m.Timestamp),
			Status:    proto.HeartbeatMessage_STATUS_TYPE_HEALTHY,
		}
		return googleproto.Marshal(pbMsg)

	case *NodeJoinMessage:
		pbMsg := &proto.HeartbeatMessage{
			NodeId: m.NodeID,
			Status: proto.HeartbeatMessage_STATUS_TYPE_HEALTHY,
		}
		return googleproto.Marshal(pbMsg)

	case *NodeLeaveMessage:
		pbMsg := &proto.HeartbeatMessage{
			NodeId: m.NodeID,
			Status: proto.HeartbeatMessage_STATUS_TYPE_HEALTHY,
		}
		return googleproto.Marshal(pbMsg)

	case *NodeSyncMessage:
		pbMsg := &proto.GossipMessage{
			Version:  m.Version,
			Metadata: m.Metadata,
		}
		return googleproto.Marshal(pbMsg)

	case *ClockSyncMessage:
		pbMsg := &proto.HeartbeatMessage{
			NodeId: m.NodeID,
			Status: proto.HeartbeatMessage_STATUS_TYPE_HEALTHY,
		}
		return googleproto.Marshal(pbMsg)

	case *ClockSyncReplyMessage:
		pbMsg := &proto.HeartbeatMessage{
			NodeId: m.NodeID,
			Status: proto.HeartbeatMessage_STATUS_TYPE_HEALTHY,
		}
		return googleproto.Marshal(pbMsg)

	case *ClusterStatusMessage:
		pbMsg := &proto.HeartbeatMessage{
			NodeId: m.NodeID,
		}
		return googleproto.Marshal(pbMsg)

	case *ClusterStatusReplyMessage:
		// 将 Nodes 序列化为 JSON 存储到 Data 字段
		data, err := json.Marshal(m.Nodes)
		if err != nil {
			return nil, err
		}
		pbMsg := &proto.ResponseMessage{
			Success: true,
			Data:    data,
		}
		return googleproto.Marshal(pbMsg)

	case *LeaderElectionMessage:
		pbMsg := &proto.HeartbeatMessage{
			NodeId: m.NodeID,
		}
		return googleproto.Marshal(pbMsg)

	default:
		return nil, types.NewCodecUnknownMessageTypeError(int(msg.Type()))
	}
}

// decodeMessage 从 Protobuf 字节流解码消息
func (c *ProtobufCodec) decodeMessage(msgType MessageType, data []byte) (Message, error) {
	// 创建对应类型的消息实例
	msg, err := createMessageByType(msgType)
	if err != nil {
		return nil, err
	}

	// 根据消息类型解码
	switch m := msg.(type) {
	case *GetMessage:
		pbMsg := &proto.MetadataOpMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Key = pbMsg.Key
		return m, nil

	case *PutMessage:
		pbMsg := &proto.MetadataOpMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Key = pbMsg.Key
		m.Value = pbMsg.Value
		return m, nil

	case *DeleteMessage:
		pbMsg := &proto.MetadataOpMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Key = pbMsg.Key
		return m, nil

	case *GetReplyMessage:
		pbMsg := &proto.ResponseMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Found = pbMsg.Success
		m.Value = pbMsg.Data
		// Version 保持默认值
		return m, nil

	case *PutReplyMessage:
		pbMsg := &proto.ResponseMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Success = pbMsg.Success
		// Key 和 Version 保持默认值
		return m, nil

	case *DeleteReplyMessage:
		pbMsg := &proto.ResponseMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Success = pbMsg.Success
		return m, nil

	case *GossipSyncMessage:
		pbMsg := &proto.GossipMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Version = pbMsg.Version
		m.Metadata = pbMsg.Metadata
		m.Timestamp = 0 // 设置默认值
		return m, nil

	case *GossipSyncReplyMessage:
		pbMsg := &proto.GossipMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Version = pbMsg.Version
		// 从 metadata 中提取 Accepted
		if accepted, ok := pbMsg.Metadata["accepted"]; ok && len(accepted) > 0 && accepted[0] == 1 {
			m.Accepted = true
		}
		return m, nil

	case *GossipDigestMessage:
		pbMsg := &proto.GossipMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Version = pbMsg.Version
		m.Digest = make(map[string]uint64)
		// 将 map[string][]byte 转换回 map[string]uint64
		for k, v := range pbMsg.Metadata {
			if len(v) == 8 {
				m.Digest[k] = binary.BigEndian.Uint64(v)
			}
		}
		return m, nil

	case *GossipDigestReplyMessage:
		pbMsg := &proto.GossipMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Version = pbMsg.Version
		m.Digest = make(map[string]uint64)
		for k, v := range pbMsg.Metadata {
			if len(v) == 8 {
				m.Digest[k] = binary.BigEndian.Uint64(v)
			}
		}
		return m, nil

	case *QuorumProposeMessage:
		pbMsg := &proto.QuorumMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.ProposalID = pbMsg.ProposalId
		m.Key = pbMsg.Key
		m.Value = pbMsg.Value
		m.Timestamp = int64(pbMsg.Version)
		return m, nil

	case *QuorumVoteMessage:
		pbMsg := &proto.QuorumMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.ProposalID = pbMsg.ProposalId
		m.Vote = pbMsg.Vote == proto.QuorumMessage_VOTE_TYPE_APPROVE
		m.Reason = pbMsg.Reason
		return m, nil

	case *QuorumDecideMessage:
		pbMsg := &proto.QuorumMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.ProposalID = pbMsg.ProposalId
		m.Version = pbMsg.Version
		return m, nil

	case *TwoPCPrepareMessage:
		pbMsg := &proto.TwoPCMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.TransactionID = pbMsg.TransactionId
		m.Participants = pbMsg.Shards
		// 转换 operations map 回 Operations 切片
		m.Operations = make([]Operation, 0, len(pbMsg.Operations))
		for key, opType := range pbMsg.Operations {
			m.Operations = append(m.Operations, Operation{
				Type: string(opType),
				Key:  key,
			})
		}
		return m, nil

	case *TwoPCPrepareReplyMessage:
		pbMsg := &proto.TwoPCMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.TransactionID = pbMsg.TransactionId
		m.Vote = convertTwoPCDecisionTypeFromProto(pbMsg.Decision)
		m.Reason = pbMsg.Reason
		return m, nil

	case *TwoPCCommitMessage:
		pbMsg := &proto.TwoPCMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.TransactionID = pbMsg.TransactionId
		return m, nil

	case *TwoPCRollbackMessage:
		pbMsg := &proto.TwoPCMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.TransactionID = pbMsg.TransactionId
		m.Reason = pbMsg.Reason
		return m, nil

	case *TwoPCCommitReplyMessage:
		pbMsg := &proto.TwoPCMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.TransactionID = pbMsg.TransactionId
		return m, nil

	case *TwoPCRollbackReplyMessage:
		pbMsg := &proto.TwoPCMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.TransactionID = pbMsg.TransactionId
		return m, nil

	case *NodePingMessage:
		pbMsg := &proto.HeartbeatMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.NodeID = pbMsg.NodeId
		m.Timestamp = int64(pbMsg.Timestamp)
		return m, nil

	case *NodePongMessage:
		pbMsg := &proto.HeartbeatMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.NodeID = pbMsg.NodeId
		m.Timestamp = int64(pbMsg.Timestamp)
		return m, nil

	case *NodeJoinMessage:
		pbMsg := &proto.HeartbeatMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.NodeID = pbMsg.NodeId
		return m, nil

	case *NodeLeaveMessage:
		pbMsg := &proto.HeartbeatMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.NodeID = pbMsg.NodeId
		return m, nil

	case *NodeSyncMessage:
		pbMsg := &proto.GossipMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.Version = pbMsg.Version
		m.Metadata = pbMsg.Metadata
		return m, nil

	case *ClockSyncMessage:
		pbMsg := &proto.HeartbeatMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.NodeID = pbMsg.NodeId
		m.Timestamp = int64(pbMsg.Timestamp)
		return m, nil

	case *ClockSyncReplyMessage:
		pbMsg := &proto.HeartbeatMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.NodeID = pbMsg.NodeId
		m.Timestamp = int64(pbMsg.Timestamp)
		return m, nil

	case *ClusterStatusMessage:
		pbMsg := &proto.HeartbeatMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.NodeID = pbMsg.NodeId
		return m, nil

	case *ClusterStatusReplyMessage:
		pbMsg := &proto.ResponseMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		// 从 Data 字段反序列化 JSON 为 Nodes
		if len(pbMsg.Data) > 0 {
			if err := json.Unmarshal(pbMsg.Data, &m.Nodes); err != nil {
				return nil, err
			}
		}
		return m, nil

	case *LeaderElectionMessage:
		pbMsg := &proto.HeartbeatMessage{}
		if err := googleproto.Unmarshal(data, pbMsg); err != nil {
			return nil, err
		}
		m.NodeID = pbMsg.NodeId
		return m, nil

	default:
		return nil, types.NewCodecUnknownMessageTypeError(int(msgType))
	}
}

// ========================================
// TwoPC DecisionType 转换
// ========================================

func convertTwoPCDecisionTypeToProto(decision string) proto.TwoPCMessage_DecisionType {
	switch decision {
	case "commit":
		return proto.TwoPCMessage_DECISION_TYPE_COMMIT
	case "abort":
		return proto.TwoPCMessage_DECISION_TYPE_ABORT
	default:
		return proto.TwoPCMessage_DECISION_TYPE_UNSPECIFIED
	}
}

func convertTwoPCDecisionTypeFromProto(decision proto.TwoPCMessage_DecisionType) string {
	switch decision {
	case proto.TwoPCMessage_DECISION_TYPE_COMMIT:
		return "commit"
	case proto.TwoPCMessage_DECISION_TYPE_ABORT:
		return "abort"
	default:
		return "unknown"
	}
}
