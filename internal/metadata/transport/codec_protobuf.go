// Package transport Transport 层 Protobuf 编解码器实现
//
// 使用 WrapperMessageProto oneof 模式，解决语义丢失问题
//
// 核心改进：
//   - 独立的消息类型定义，避免多消息映射到同一 Proto 类型
//   - oneof 模式实现消息类型判别
//   - 完整的字段保留，无信息丢失
package transport

import (
	"github.com/jzhang405/NexKV/internal/metadata/proto"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	googleproto "google.golang.org/protobuf/proto"
)

// ========================================
// ProtobufCodec Protobuf 编解码器
// ========================================

// ProtobufCodec Protobuf 编解码器
//
// 特点：
//   - 使用 WrapperMessageProto oneof 模式
//   - 无语义丢失，完整保留所有字段
//   - protoc 预编译，性能最优
//   - Schema 明确，跨语言支持好
type ProtobufCodec struct{}

// NewProtobufCodec 创建 Protobuf 编解码器
func NewProtobufCodec() *ProtobufCodec {
	return &ProtobufCodec{}
}

// Encode 编码消息（Protobuf 格式）
//
// 将消息编码为纯 Protobuf 格式的字节流
// WrapperMessageProto 本身包含 MessageType，无需额外包装
func (c *ProtobufCodec) Encode(msg Message) ([]byte, error) {
	if msg == nil {
		return nil, types.NewCodecInvalidMessageError("消息为空")
	}

	// 将 Transport Message 转换为 WrapperMessageProto 并编码
	wrapper, err := c.messageToWrapper(msg)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("Protobuf", err)
	}

	dataBytes, err := googleproto.Marshal(wrapper)
	if err != nil {
		return nil, types.NewCodecEncodeFailedError("Protobuf", err)
	}

	return dataBytes, nil
}

// Decode 解码消息（Protobuf 格式）
//
// 从纯 Protobuf 格式的字节流解码消息
// msgType 参数指定消息类型（从 FixedHeader 获取）
// 注意：WrapperMessageProto 内部仍包含类型，可用于一致性验证
func (c *ProtobufCodec) Decode(msgType MessageType, data []byte) (Message, error) {
	if len(data) == 0 {
		return nil, types.NewCodecInvalidDataError("Decode", "数据为空")
	}

	// 解析 WrapperMessageProto
	wrapper := &proto.WrapperMessageProto{}
	if err := googleproto.Unmarshal(data, wrapper); err != nil {
		return nil, types.NewCodecDecodeFailedError("Protobuf", err)
	}

	// 从 Wrapper 提取消息
	msg, err := c.wrapperToMessage(wrapper)
	if err != nil {
		return nil, types.NewCodecDecodeFailedError("Protobuf", err)
	}

	return msg, nil
}

// DecodeInto 解码消息到指定实例
//
// 从纯 Protobuf 格式数据解码到预先创建的消息实例
func (c *ProtobufCodec) DecodeInto(data []byte, msg Message) error {
	if msg == nil {
		return types.NewCodecInvalidMessageError("消息实例为空")
	}
	if len(data) == 0 {
		return types.NewCodecInvalidDataError("DecodeInto", "数据为空")
	}

	// 解析 WrapperMessageProto
	wrapper := &proto.WrapperMessageProto{}
	if err := googleproto.Unmarshal(data, wrapper); err != nil {
		return types.NewCodecDecodeFailedError("Protobuf", err)
	}

	// 从 Wrapper 提取消息并赋值到目标实例
	decodedMsg, err := c.wrapperToMessage(wrapper)
	if err != nil {
		return types.NewCodecDecodeFailedError("Protobuf", err)
	}

	// 使用 wrapperToMessage 返回的消息填充目标实例
	// 注意：这里需要根据具体类型进行字段复制
	// 为了简化，我们直接创建新实例并使用反射或类型断言
	switch m := msg.(type) {
	case *GetMessage:
		if decoded, ok := decodedMsg.(*GetMessage); ok {
			m.Key = decoded.Key
		}
	case *PutMessage:
		if decoded, ok := decodedMsg.(*PutMessage); ok {
			m.Key = decoded.Key
			m.Value = decoded.Value
		}
	case *DeleteMessage:
		if decoded, ok := decodedMsg.(*DeleteMessage); ok {
			m.Key = decoded.Key
		}
	case *GetReplyMessage:
		if decoded, ok := decodedMsg.(*GetReplyMessage); ok {
			m.Key = decoded.Key
			m.Value = decoded.Value
			m.Found = decoded.Found
			m.Version = decoded.Version
		}
	case *PutReplyMessage:
		if decoded, ok := decodedMsg.(*PutReplyMessage); ok {
			m.Key = decoded.Key
			m.Success = decoded.Success
			m.Version = decoded.Version
		}
	case *DeleteReplyMessage:
		if decoded, ok := decodedMsg.(*DeleteReplyMessage); ok {
			m.Key = decoded.Key
			m.Success = decoded.Success
		}
	case *GossipSyncMessage:
		if decoded, ok := decodedMsg.(*GossipSyncMessage); ok {
			m.Version = decoded.Version
			m.Metadata = decoded.Metadata
			m.Timestamp = decoded.Timestamp
		}
	case *GossipSyncReplyMessage:
		if decoded, ok := decodedMsg.(*GossipSyncReplyMessage); ok {
			m.Accepted = decoded.Accepted
			m.Version = decoded.Version
		}
	case *GossipDigestMessage:
		if decoded, ok := decodedMsg.(*GossipDigestMessage); ok {
			m.Version = decoded.Version
			m.Digest = decoded.Digest
		}
	case *GossipDigestReplyMessage:
		if decoded, ok := decodedMsg.(*GossipDigestReplyMessage); ok {
			m.Version = decoded.Version
			m.Digest = decoded.Digest
		}
	case *QuorumProposeMessage:
		if decoded, ok := decodedMsg.(*QuorumProposeMessage); ok {
			m.ProposalID = decoded.ProposalID
			m.Key = decoded.Key
			m.Value = decoded.Value
			m.Operation = decoded.Operation
			m.Proposer = decoded.Proposer
			m.Timestamp = decoded.Timestamp
		}
	case *QuorumVoteMessage:
		if decoded, ok := decodedMsg.(*QuorumVoteMessage); ok {
			m.ProposalID = decoded.ProposalID
			m.Voter = decoded.Voter
			m.Vote = decoded.Vote
			m.Reason = decoded.Reason
		}
	case *QuorumDecideMessage:
		if decoded, ok := decodedMsg.(*QuorumDecideMessage); ok {
			m.ProposalID = decoded.ProposalID
			m.Approved = decoded.Approved
			m.Version = decoded.Version
		}
	case *TwoPCPrepareMessage:
		if decoded, ok := decodedMsg.(*TwoPCPrepareMessage); ok {
			m.TransactionID = decoded.TransactionID
			m.Participants = decoded.Participants
			m.Operations = decoded.Operations
			m.Timeout = decoded.Timeout
		}
	case *TwoPCPrepareReplyMessage:
		if decoded, ok := decodedMsg.(*TwoPCPrepareReplyMessage); ok {
			m.TransactionID = decoded.TransactionID
			m.Participant = decoded.Participant
			m.Vote = decoded.Vote
			m.Reason = decoded.Reason
		}
	case *TwoPCCommitMessage:
		if decoded, ok := decodedMsg.(*TwoPCCommitMessage); ok {
			m.TransactionID = decoded.TransactionID
		}
	case *TwoPCRollbackMessage:
		if decoded, ok := decodedMsg.(*TwoPCRollbackMessage); ok {
			m.TransactionID = decoded.TransactionID
			m.Reason = decoded.Reason
		}
	case *TwoPCCommitReplyMessage:
		if decoded, ok := decodedMsg.(*TwoPCCommitReplyMessage); ok {
			m.TransactionID = decoded.TransactionID
			m.Participant = decoded.Participant
			m.Success = decoded.Success
		}
	case *TwoPCRollbackReplyMessage:
		if decoded, ok := decodedMsg.(*TwoPCRollbackReplyMessage); ok {
			m.TransactionID = decoded.TransactionID
			m.Participant = decoded.Participant
			m.Success = decoded.Success
		}
	case *NodePingMessage:
		if decoded, ok := decodedMsg.(*NodePingMessage); ok {
			m.NodeID = decoded.NodeID
			m.Sequence = decoded.Sequence
			m.Timestamp = decoded.Timestamp
		}
	case *NodePongMessage:
		if decoded, ok := decodedMsg.(*NodePongMessage); ok {
			m.NodeID = decoded.NodeID
			m.Sequence = decoded.Sequence
			m.Status = decoded.Status
			m.Timestamp = decoded.Timestamp
		}
	case *NodeJoinMessage:
		if decoded, ok := decodedMsg.(*NodeJoinMessage); ok {
			m.NodeID = decoded.NodeID
			m.Addr = decoded.Addr
			m.Role = decoded.Role
			m.ParentID = decoded.ParentID
		}
	case *NodeLeaveMessage:
		if decoded, ok := decodedMsg.(*NodeLeaveMessage); ok {
			m.NodeID = decoded.NodeID
			m.Reason = decoded.Reason
		}
	case *NodeSyncMessage:
		if decoded, ok := decodedMsg.(*NodeSyncMessage); ok {
			m.Version = decoded.Version
			m.Metadata = decoded.Metadata
		}
	case *ClockSyncMessage:
		if decoded, ok := decodedMsg.(*ClockSyncMessage); ok {
			m.Timestamp = decoded.Timestamp
			m.NodeID = decoded.NodeID
		}
	case *ClockSyncReplyMessage:
		if decoded, ok := decodedMsg.(*ClockSyncReplyMessage); ok {
			m.Timestamp = decoded.Timestamp
			m.NodeID = decoded.NodeID
			m.Drift = decoded.Drift
		}
	case *ClusterStatusMessage:
		if decoded, ok := decodedMsg.(*ClusterStatusMessage); ok {
			m.NodeID = decoded.NodeID
		}
	case *ClusterStatusReplyMessage:
		if decoded, ok := decodedMsg.(*ClusterStatusReplyMessage); ok {
			m.Nodes = decoded.Nodes
		}
	case *LeaderElectionMessage:
		if decoded, ok := decodedMsg.(*LeaderElectionMessage); ok {
			m.ElectionID = decoded.ElectionID
			m.NodeID = decoded.NodeID
			m.Priority = decoded.Priority
		}
	default:
		return types.NewCodecInvalidDataError("DecodeInto", "不支持的消息类型")
	}

	return nil
}

// Name 返回编解码器名称
func (c *ProtobufCodec) Name() string {
	return "protobuf"
}

// Type 返回编解码器类型
func (c *ProtobufCodec) Type() types.CodecType {
	return types.CodecTypeProtobuf
}

// ========================================
// Message -> WrapperMessageProto 转换
// ========================================

// messageToWrapper 将 Transport Message 转换为 WrapperMessageProto
func (c *ProtobufCodec) messageToWrapper(msg Message) (*proto.WrapperMessageProto, error) {
	wrapper := &proto.WrapperMessageProto{
		MessageType: uint32(msg.Type()),
	}

	switch m := msg.(type) {
	case *GetMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_GetMsg{
			GetMsg: &proto.GetMessageProto{
				Key: m.Key,
			},
		}
	case *PutMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_PutMsg{
			PutMsg: &proto.PutMessageProto{
				Key:   m.Key,
				Value: m.Value,
			},
		}
	case *DeleteMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_DeleteMsg{
			DeleteMsg: &proto.DeleteMessageProto{
				Key: m.Key,
			},
		}
	case *GetReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_GetReplyMsg{
			GetReplyMsg: &proto.GetReplyMessageProto{
				Key:     m.Key,
				Value:   m.Value,
				Found:   m.Found,
				Version: m.Version,
			},
		}
	case *PutReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_PutReplyMsg{
			PutReplyMsg: &proto.PutReplyMessageProto{
				Key:     m.Key,
				Success: m.Success,
				Version: m.Version,
			},
		}
	case *DeleteReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_DeleteReplyMsg{
			DeleteReplyMsg: &proto.DeleteReplyMessageProto{
				Key:     m.Key,
				Success: m.Success,
			},
		}
	case *GossipSyncMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_GossipSyncMsg{
			GossipSyncMsg: &proto.GossipSyncMessageProto{
				Version:   m.Version,
				Metadata:  m.Metadata,
				Timestamp: m.Timestamp,
			},
		}
	case *GossipSyncReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_GossipSyncReplyMsg{
			GossipSyncReplyMsg: &proto.GossipSyncReplyMessageProto{
				Accepted: m.Accepted,
				Version:  m.Version,
			},
		}
	case *GossipDigestMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_GossipDigestMsg{
			GossipDigestMsg: &proto.GossipDigestMessageProto{
				Version: m.Version,
				Digest:  m.Digest,
			},
		}
	case *GossipDigestReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_GossipDigestReplyMsg{
			GossipDigestReplyMsg: &proto.GossipDigestReplyMessageProto{
				Version: m.Version,
				Digest:  m.Digest,
			},
		}
	case *QuorumProposeMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_QuorumProposeMsg{
			QuorumProposeMsg: &proto.QuorumProposeMessageProto{
				ProposalId: m.ProposalID,
				Key:        m.Key,
				Value:      m.Value,
				Operation:  m.Operation,
				Proposer:   m.Proposer,
				Timestamp:  m.Timestamp,
			},
		}
	case *QuorumVoteMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_QuorumVoteMsg{
			QuorumVoteMsg: &proto.QuorumVoteMessageProto{
				ProposalId: m.ProposalID,
				Voter:      m.Voter,
				Vote:       m.Vote,
				Reason:     m.Reason,
			},
		}
	case *QuorumDecideMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_QuorumDecideMsg{
			QuorumDecideMsg: &proto.QuorumDecideMessageProto{
				ProposalId: m.ProposalID,
				Approved:   m.Approved,
				Version:    m.Version,
			},
		}
	case *TwoPCPrepareMessage:
		ops := make([]*proto.OperationProto, len(m.Operations))
		for i, op := range m.Operations {
			ops[i] = &proto.OperationProto{
				Type:  op.Type,
				Key:   op.Key,
				Value: op.Value,
			}
		}
		wrapper.MessageBody = &proto.WrapperMessageProto_TwopcPrepareMsg{
			TwopcPrepareMsg: &proto.TwoPCPrepareMessageProto{
				TransactionId: m.TransactionID,
				Participants:  m.Participants,
				Operations:    ops,
				Timestamp:     m.Timeout,
			},
		}
	case *TwoPCPrepareReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_TwopcPrepareReplyMsg{
			TwopcPrepareReplyMsg: &proto.TwoPCPrepareReplyMessageProto{
				TransactionId: m.TransactionID,
				Participant:   m.Participant,
				Vote:          m.Vote,
				Reason:        m.Reason,
			},
		}
	case *TwoPCCommitMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_TwopcCommitMsg{
			TwopcCommitMsg: &proto.TwoPCCommitMessageProto{
				TransactionId: m.TransactionID,
			},
		}
	case *TwoPCRollbackMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_TwopcRollbackMsg{
			TwopcRollbackMsg: &proto.TwoPCRollbackMessageProto{
				TransactionId: m.TransactionID,
				Reason:        m.Reason,
			},
		}
	case *TwoPCCommitReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_TwopcCommitReplyMsg{
			TwopcCommitReplyMsg: &proto.TwoPCCommitReplyMessageProto{
				TransactionId: m.TransactionID,
				Participant:   m.Participant,
				Success:       m.Success,
			},
		}
	case *TwoPCRollbackReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_TwopcRollbackReplyMsg{
			TwopcRollbackReplyMsg: &proto.TwoPCRollbackReplyMessageProto{
				TransactionId: m.TransactionID,
				Participant:   m.Participant,
				Success:       m.Success,
			},
		}
	case *NodePingMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_NodePingMsg{
			NodePingMsg: &proto.NodePingMessageProto{
				NodeId:    m.NodeID,
				Sequence:  m.Sequence,
				Timestamp: m.Timestamp,
			},
		}
	case *NodePongMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_NodePongMsg{
			NodePongMsg: &proto.NodePongMessageProto{
				NodeId:    m.NodeID,
				Sequence:  m.Sequence,
				Status:    m.Status,
				Timestamp: m.Timestamp,
			},
		}
	case *NodeJoinMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_NodeJoinMsg{
			NodeJoinMsg: &proto.NodeJoinMessageProto{
				NodeId:   m.NodeID,
				Addr:     m.Addr,
				Role:     m.Role,
				ParentId: m.ParentID,
			},
		}
	case *NodeLeaveMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_NodeLeaveMsg{
			NodeLeaveMsg: &proto.NodeLeaveMessageProto{
				NodeId: m.NodeID,
				Reason: m.Reason,
			},
		}
	case *NodeSyncMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_NodeSyncMsg{
			NodeSyncMsg: &proto.NodeSyncMessageProto{
				Version:  m.Version,
				Metadata: m.Metadata,
			},
		}
	case *ClockSyncMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_ClockSyncMsg{
			ClockSyncMsg: &proto.ClockSyncMessageProto{
				Timestamp: m.Timestamp,
				NodeId:    m.NodeID,
			},
		}
	case *ClockSyncReplyMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_ClockSyncReplyMsg{
			ClockSyncReplyMsg: &proto.ClockSyncReplyMessageProto{
				Timestamp: m.Timestamp,
				NodeId:    m.NodeID,
				Drift:     m.Drift,
			},
		}
	case *ClusterStatusMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_ClusterStatusMsg{
			ClusterStatusMsg: &proto.ClusterStatusMessageProto{
				NodeId: m.NodeID,
			},
		}
	case *ClusterStatusReplyMessage:
		nodes := make([]*proto.NodeInfoProto, len(m.Nodes))
		for i, node := range m.Nodes {
			nodes[i] = &proto.NodeInfoProto{
				NodeId:   node.NodeID,
				Addr:     node.Addr,
				Role:     node.Role,
				ParentId: node.ParentID,
				Status:   node.Status,
				Level:    int32(node.Level),
			}
		}
		wrapper.MessageBody = &proto.WrapperMessageProto_ClusterStatusReplyMsg{
			ClusterStatusReplyMsg: &proto.ClusterStatusReplyMessageProto{
				Nodes: nodes,
			},
		}
	case *LeaderElectionMessage:
		wrapper.MessageBody = &proto.WrapperMessageProto_LeaderElectionMsg{
			LeaderElectionMsg: &proto.LeaderElectionMessageProto{
				ElectionId: m.ElectionID, // Proto uses camelCase: election_id
				NodeId:     m.NodeID,
				Priority:   int32(m.Priority),
			},
		}
	default:
		return nil, types.NewCodecUnknownMessageTypeError(int(msg.Type()))
	}

	return wrapper, nil
}

// wrapperToMessage 从 WrapperMessageProto 提取 Transport Message
func (c *ProtobufCodec) wrapperToMessage(wrapper *proto.WrapperMessageProto) (Message, error) {
	if wrapper.MessageBody == nil {
		return nil, types.NewCodecInvalidDataError("wrapperToMessage", "消息体为空")
	}

	switch body := wrapper.MessageBody.(type) {
	case *proto.WrapperMessageProto_GetMsg:
		return &GetMessage{Key: body.GetMsg.Key}, nil
	case *proto.WrapperMessageProto_PutMsg:
		return &PutMessage{Key: body.PutMsg.Key, Value: body.PutMsg.Value}, nil
	case *proto.WrapperMessageProto_DeleteMsg:
		return &DeleteMessage{Key: body.DeleteMsg.Key}, nil
	case *proto.WrapperMessageProto_GetReplyMsg:
		return &GetReplyMessage{
			Key:     body.GetReplyMsg.Key,
			Value:   body.GetReplyMsg.Value,
			Found:   body.GetReplyMsg.Found,
			Version: body.GetReplyMsg.Version,
		}, nil
	case *proto.WrapperMessageProto_PutReplyMsg:
		return &PutReplyMessage{
			Key:     body.PutReplyMsg.Key,
			Success: body.PutReplyMsg.Success,
			Version: body.PutReplyMsg.Version,
		}, nil
	case *proto.WrapperMessageProto_DeleteReplyMsg:
		return &DeleteReplyMessage{
			Key:     body.DeleteReplyMsg.Key,
			Success: body.DeleteReplyMsg.Success,
		}, nil
	case *proto.WrapperMessageProto_GossipSyncMsg:
		return &GossipSyncMessage{
			Version:   body.GossipSyncMsg.Version,
			Metadata:  body.GossipSyncMsg.Metadata,
			Timestamp: body.GossipSyncMsg.Timestamp,
		}, nil
	case *proto.WrapperMessageProto_GossipSyncReplyMsg:
		return &GossipSyncReplyMessage{
			Accepted: body.GossipSyncReplyMsg.Accepted,
			Version:  body.GossipSyncReplyMsg.Version,
		}, nil
	case *proto.WrapperMessageProto_GossipDigestMsg:
		return &GossipDigestMessage{
			Version: body.GossipDigestMsg.Version,
			Digest:  body.GossipDigestMsg.Digest,
		}, nil
	case *proto.WrapperMessageProto_GossipDigestReplyMsg:
		return &GossipDigestReplyMessage{
			Version: body.GossipDigestReplyMsg.Version,
			Digest:  body.GossipDigestReplyMsg.Digest,
		}, nil
	case *proto.WrapperMessageProto_QuorumProposeMsg:
		return &QuorumProposeMessage{
			ProposalID: body.QuorumProposeMsg.ProposalId,
			Key:        body.QuorumProposeMsg.Key,
			Value:      body.QuorumProposeMsg.Value,
			Operation:  body.QuorumProposeMsg.Operation,
			Proposer:   body.QuorumProposeMsg.Proposer,
			Timestamp:  body.QuorumProposeMsg.Timestamp,
		}, nil
	case *proto.WrapperMessageProto_QuorumVoteMsg:
		return &QuorumVoteMessage{
			ProposalID: body.QuorumVoteMsg.ProposalId,
			Voter:      body.QuorumVoteMsg.Voter,
			Vote:       body.QuorumVoteMsg.Vote,
			Reason:     body.QuorumVoteMsg.Reason,
		}, nil
	case *proto.WrapperMessageProto_QuorumDecideMsg:
		return &QuorumDecideMessage{
			ProposalID: body.QuorumDecideMsg.ProposalId,
			Approved:   body.QuorumDecideMsg.Approved,
			Version:    body.QuorumDecideMsg.Version,
		}, nil
	case *proto.WrapperMessageProto_TwopcPrepareMsg:
		ops := make([]Operation, len(body.TwopcPrepareMsg.Operations))
		for i, op := range body.TwopcPrepareMsg.Operations {
			ops[i] = Operation{
				Type:  op.Type,
				Key:   op.Key,
				Value: op.Value,
			}
		}
		return &TwoPCPrepareMessage{
			TransactionID: body.TwopcPrepareMsg.TransactionId,
			Participants:  body.TwopcPrepareMsg.Participants,
			Operations:    ops,
			Timeout:       body.TwopcPrepareMsg.Timestamp,
		}, nil
	case *proto.WrapperMessageProto_TwopcPrepareReplyMsg:
		return &TwoPCPrepareReplyMessage{
			TransactionID: body.TwopcPrepareReplyMsg.TransactionId,
			Participant:   body.TwopcPrepareReplyMsg.Participant,
			Vote:          body.TwopcPrepareReplyMsg.Vote,
			Reason:        body.TwopcPrepareReplyMsg.Reason,
		}, nil
	case *proto.WrapperMessageProto_TwopcCommitMsg:
		return &TwoPCCommitMessage{
			TransactionID: body.TwopcCommitMsg.TransactionId,
		}, nil
	case *proto.WrapperMessageProto_TwopcRollbackMsg:
		return &TwoPCRollbackMessage{
			TransactionID: body.TwopcRollbackMsg.TransactionId,
			Reason:        body.TwopcRollbackMsg.Reason,
		}, nil
	case *proto.WrapperMessageProto_TwopcCommitReplyMsg:
		return &TwoPCCommitReplyMessage{
			TransactionID: body.TwopcCommitReplyMsg.TransactionId,
			Participant:   body.TwopcCommitReplyMsg.Participant,
			Success:       body.TwopcCommitReplyMsg.Success,
		}, nil
	case *proto.WrapperMessageProto_TwopcRollbackReplyMsg:
		return &TwoPCRollbackReplyMessage{
			TransactionID: body.TwopcRollbackReplyMsg.TransactionId,
			Participant:   body.TwopcRollbackReplyMsg.Participant,
			Success:       body.TwopcRollbackReplyMsg.Success,
		}, nil
	case *proto.WrapperMessageProto_NodePingMsg:
		return &NodePingMessage{
			NodeID:    body.NodePingMsg.NodeId,
			Sequence:  body.NodePingMsg.Sequence,
			Timestamp: body.NodePingMsg.Timestamp,
		}, nil
	case *proto.WrapperMessageProto_NodePongMsg:
		return &NodePongMessage{
			NodeID:    body.NodePongMsg.NodeId,
			Sequence:  body.NodePongMsg.Sequence,
			Status:    body.NodePongMsg.Status,
			Timestamp: body.NodePongMsg.Timestamp,
		}, nil
	case *proto.WrapperMessageProto_NodeJoinMsg:
		return &NodeJoinMessage{
			NodeID:   body.NodeJoinMsg.NodeId,
			Addr:     body.NodeJoinMsg.Addr,
			Role:     body.NodeJoinMsg.Role,
			ParentID: body.NodeJoinMsg.ParentId,
		}, nil
	case *proto.WrapperMessageProto_NodeLeaveMsg:
		return &NodeLeaveMessage{
			NodeID: body.NodeLeaveMsg.NodeId,
			Reason: body.NodeLeaveMsg.Reason,
		}, nil
	case *proto.WrapperMessageProto_NodeSyncMsg:
		return &NodeSyncMessage{
			Version:  body.NodeSyncMsg.Version,
			Metadata: body.NodeSyncMsg.Metadata,
		}, nil
	case *proto.WrapperMessageProto_ClockSyncMsg:
		return &ClockSyncMessage{
			Timestamp: body.ClockSyncMsg.Timestamp,
			NodeID:    body.ClockSyncMsg.NodeId,
		}, nil
	case *proto.WrapperMessageProto_ClockSyncReplyMsg:
		return &ClockSyncReplyMessage{
			Timestamp: body.ClockSyncReplyMsg.Timestamp,
			NodeID:    body.ClockSyncReplyMsg.NodeId,
			Drift:     body.ClockSyncReplyMsg.Drift,
		}, nil
	case *proto.WrapperMessageProto_ClusterStatusMsg:
		return &ClusterStatusMessage{
			NodeID: body.ClusterStatusMsg.NodeId,
		}, nil
	case *proto.WrapperMessageProto_ClusterStatusReplyMsg:
		nodes := make([]NodeInfo, len(body.ClusterStatusReplyMsg.Nodes))
		for i, node := range body.ClusterStatusReplyMsg.Nodes {
			nodes[i] = NodeInfo{
				NodeID:   node.NodeId,
				Addr:     node.Addr,
				Role:     node.Role,
				ParentID: node.ParentId,
				Status:   node.Status,
				Level:    int(node.Level),
			}
		}
		return &ClusterStatusReplyMessage{
			Nodes: nodes,
		}, nil
	case *proto.WrapperMessageProto_LeaderElectionMsg:
		return &LeaderElectionMessage{
			ElectionID: body.LeaderElectionMsg.ElectionId, // Proto uses camelCase: election_id
			NodeID:     body.LeaderElectionMsg.NodeId,
			Priority:   int(body.LeaderElectionMsg.Priority),
		}, nil
	default:
		_ = body // 明确忽略：default 匹配未知类型，body 变量无实际用途
		return nil, types.NewCodecInvalidDataError("wrapperToMessage", "未知的消息体类型")
	}
}
