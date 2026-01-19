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
	"encoding/binary"

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
// 将消息编码为 Protobuf 格式的字节流
// 格式: [Type:2字节][DataLen:4字节][Data:N字节]
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

	// 解析 WrapperMessageProto
	wrapper := &proto.WrapperMessageProto{}
	if err := googleproto.Unmarshal(data[6:6+dataLen], wrapper); err != nil {
		return nil, types.NewCodecDecodeFailedError("Protobuf", err)
	}

	// 从 Wrapper 提取消息
	msg, err := c.wrapperToMessage(wrapper)
	if err != nil {
		return nil, types.NewCodecDecodeFailedError("Protobuf", err)
	}

	// 验证消息类型匹配
	if msg.Type() != msgType {
		return nil, types.NewCodecInvalidDataError("Decode", "消息类型不匹配")
	}

	return msg, nil
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
