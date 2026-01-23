// Package transport Protobuf 编解码器测试
package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/proto"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	googleproto "google.golang.org/protobuf/proto"
)

// ========================================
// Protobuf messageToWrapper/wrapperToMessage 测试
// ========================================

// TestProtobufCodec_MessageToWrapper_Metadata 测试元数据操作消息的 Wrapper 转换
func TestProtobufCodec_MessageToWrapper_Metadata(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name     string
		message  Message
		wantType MessageType
	}{
		{
			name:     "GetMessage",
			message:  &GetMessage{Key: "test-key"},
			wantType: types.MessageTypeGet,
		},
		{
			name:     "PutMessage",
			message:  &PutMessage{Key: "test-key", Value: []byte("test-value")},
			wantType: types.MessageTypePut,
		},
		{
			name:     "DeleteMessage",
			message:  &DeleteMessage{Key: "test-key"},
			wantType: types.MessageTypeDelete,
		},
		{
			name: "GetReplyMessage",
			message: &GetReplyMessage{
				Key:     "test-key",
				Value:   []byte("test-value"),
				Found:   true,
				Version: 123,
			},
			wantType: types.MessageTypeGetReply,
		},
		{
			name: "PutReplyMessage",
			message: &PutReplyMessage{
				Key:     "test-key",
				Success: true,
				Version: 456,
			},
			wantType: types.MessageTypePutReply,
		},
		{
			name: "DeleteReplyMessage",
			message: &DeleteReplyMessage{
				Key:     "test-key",
				Success: true,
			},
			wantType: types.MessageTypeDeleteReply,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper, err := codec.messageToWrapper(tc.message)
			require.NoError(t, err)
			require.NotNil(t, wrapper)
			assert.Equal(t, uint32(tc.wantType), wrapper.MessageType)
		})
	}
}

// TestProtobufCodec_MessageToWrapper_Gossip 测试 Gossip 消息的 Wrapper 转换
func TestProtobufCodec_MessageToWrapper_Gossip(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name     string
		message  Message
		wantType MessageType
	}{
		{
			name: "GossipSyncMessage",
			message: &GossipSyncMessage{
				Version:   100,
				Metadata:  map[string][]byte{"key1": []byte("value1")},
				Timestamp: 1234567890,
			},
			wantType: types.MessageTypeGossipSync,
		},
		{
			name: "GossipSyncReplyMessage",
			message: &GossipSyncReplyMessage{
				Accepted: true,
				Version:  200,
			},
			wantType: types.MessageTypeGossipSyncReply,
		},
		{
			name: "GossipDigestMessage",
			message: &GossipDigestMessage{
				Version: 300,
				Digest:  map[string]uint64{"key1": 100},
			},
			wantType: types.MessageTypeGossipDigest,
		},
		{
			name: "GossipDigestReplyMessage",
			message: &GossipDigestReplyMessage{
				Version: 400,
				Digest:  map[string]uint64{"key2": 200},
			},
			wantType: types.MessageTypeGossipDigestReply,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper, err := codec.messageToWrapper(tc.message)
			require.NoError(t, err)
			require.NotNil(t, wrapper)
			assert.Equal(t, uint32(tc.wantType), wrapper.MessageType)
		})
	}
}

// TestProtobufCodec_MessageToWrapper_Quorum 测试 Quorum 消息的 Wrapper 转换
func TestProtobufCodec_MessageToWrapper_Quorum(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name     string
		message  Message
		wantType MessageType
	}{
		{
			name: "QuorumProposeMessage",
			message: &QuorumProposeMessage{
				ProposalID: "prop-123",
				Key:        "test-key",
				Value:      []byte("test-value"),
				Operation:  "SET",
				Proposer:   "node-1",
				Timestamp:  1234567890,
			},
			wantType: types.MessageTypeQuorumPropose,
		},
		{
			name: "QuorumVoteMessage",
			message: &QuorumVoteMessage{
				ProposalID: "prop-123",
				Voter:      "node-2",
				Vote:       true,
				Reason:     "OK",
			},
			wantType: types.MessageTypeQuorumVote,
		},
		{
			name: "QuorumDecideMessage",
			message: &QuorumDecideMessage{
				ProposalID: "prop-123",
				Approved:   true,
				Version:    500,
			},
			wantType: types.MessageTypeQuorumDecide,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper, err := codec.messageToWrapper(tc.message)
			require.NoError(t, err)
			require.NotNil(t, wrapper)
			assert.Equal(t, uint32(tc.wantType), wrapper.MessageType)
		})
	}
}

// TestProtobufCodec_MessageToWrapper_TwoPC 测试 TwoPC 消息的 Wrapper 转换
func TestProtobufCodec_MessageToWrapper_TwoPC(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name     string
		message  Message
		wantType MessageType
	}{
		{
			name: "TwoPCPrepareMessage",
			message: &TwoPCPrepareMessage{
				TransactionID: "txn-123",
				Participants:  []string{"node-1", "node-2"},
				Operations: []Operation{
					{Type: "SET", Key: "key1", Value: []byte("value1")},
					{Type: "SET", Key: "key2", Value: []byte("value2")},
				},
				Timeout: 60000,
			},
			wantType: types.MessageType2PCPrepare,
		},
		{
			name: "TwoPCPrepareReplyMessage",
			message: &TwoPCPrepareReplyMessage{
				TransactionID: "txn-123",
				Participant:   "node-1",
				Vote:          "commit",
				Reason:        "OK",
			},
			wantType: types.MessageType2PCPrepareReply,
		},
		{
			name: "TwoPCCommitMessage",
			message: &TwoPCCommitMessage{
				TransactionID: "txn-123",
			},
			wantType: types.MessageType2PCCommit,
		},
		{
			name: "TwoPCRollbackMessage",
			message: &TwoPCRollbackMessage{
				TransactionID: "txn-123",
				Reason:        "timeout",
			},
			wantType: types.MessageType2PCRollback,
		},
		{
			name: "TwoPCCommitReplyMessage",
			message: &TwoPCCommitReplyMessage{
				TransactionID: "txn-123",
				Participant:   "node-1",
				Success:       true,
			},
			wantType: types.MessageType2PCCommitReply,
		},
		{
			name: "TwoPCRollbackReplyMessage",
			message: &TwoPCRollbackReplyMessage{
				TransactionID: "txn-123",
				Participant:   "node-1",
				Success:       true,
			},
			wantType: types.MessageType2PCRollbackReply,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper, err := codec.messageToWrapper(tc.message)
			require.NoError(t, err)
			require.NotNil(t, wrapper)
			assert.Equal(t, uint32(tc.wantType), wrapper.MessageType)
		})
	}
}

// TestProtobufCodec_MessageToWrapper_Node 测试节点消息的 Wrapper 转换
func TestProtobufCodec_MessageToWrapper_Node(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name     string
		message  Message
		wantType MessageType
	}{
		{
			name: "NodePingMessage",
			message: &NodePingMessage{
				NodeID:    "node-1",
				Sequence:  123,
				Timestamp: 1234567890,
			},
			wantType: types.MessageTypeNodePing,
		},
		{
			name: "NodePongMessage",
			message: &NodePongMessage{
				NodeID:    "node-1",
				Sequence:  123,
				Status:    "ready",
				Timestamp: 1234567890,
			},
			wantType: types.MessageTypeNodePong,
		},
		{
			name: "NodeJoinMessage",
			message: &NodeJoinMessage{
				NodeID:   "node-2",
				Addr:     "127.0.0.1:9211",
				Role:     "leaf",
				ParentID: "node-1",
			},
			wantType: types.MessageTypeNodeJoin,
		},
		{
			name: "NodeLeaveMessage",
			message: &NodeLeaveMessage{
				NodeID: "node-2",
				Reason: "shutdown",
			},
			wantType: types.MessageTypeNodeLeave,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper, err := codec.messageToWrapper(tc.message)
			require.NoError(t, err)
			require.NotNil(t, wrapper)
			assert.Equal(t, uint32(tc.wantType), wrapper.MessageType)
		})
	}
}

// TestProtobufCodec_WrapperToMessage_Metadata 测试元数据消息的逆向转换
func TestProtobufCodec_WrapperToMessage_Metadata(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name    string
		wrapper *proto.WrapperMessageProto
		wantMsg Message
	}{
		{
			name: "GetMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypeGet),
				MessageBody: &proto.WrapperMessageProto_GetMsg{
					GetMsg: &proto.GetMessageProto{Key: "test-key"},
				},
			},
			wantMsg: &GetMessage{Key: "test-key"},
		},
		{
			name: "PutMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypePut),
				MessageBody: &proto.WrapperMessageProto_PutMsg{
					PutMsg: &proto.PutMessageProto{
						Key:   "test-key",
						Value: []byte("test-value"),
					},
				},
			},
			wantMsg: &PutMessage{Key: "test-key", Value: []byte("test-value")},
		},
		{
			name: "DeleteMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypeDelete),
				MessageBody: &proto.WrapperMessageProto_DeleteMsg{
					DeleteMsg: &proto.DeleteMessageProto{Key: "test-key"},
				},
			},
			wantMsg: &DeleteMessage{Key: "test-key"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := codec.wrapperToMessage(tc.wrapper)
			require.NoError(t, err)

			// 类型断言验证
			switch want := tc.wantMsg.(type) {
			case *GetMessage:
				getMsg, ok := msg.(*GetMessage)
				require.True(t, ok)
				assert.Equal(t, want.Key, getMsg.Key)
			case *PutMessage:
				putMsg, ok := msg.(*PutMessage)
				require.True(t, ok)
				assert.Equal(t, want.Key, putMsg.Key)
				assert.Equal(t, want.Value, putMsg.Value)
			case *DeleteMessage:
				delMsg, ok := msg.(*DeleteMessage)
				require.True(t, ok)
				assert.Equal(t, want.Key, delMsg.Key)
			}
		})
	}
}

// TestProtobufCodec_WrapperToMessage_Gossip 测试 Gossip 消息的逆向转换
func TestProtobufCodec_WrapperToMessage_Gossip(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name    string
		wrapper *proto.WrapperMessageProto
		wantMsg Message
	}{
		{
			name: "GossipSyncMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypeGossipSync),
				MessageBody: &proto.WrapperMessageProto_GossipSyncMsg{
					GossipSyncMsg: &proto.GossipSyncMessageProto{
						Version:   100,
						Metadata:  map[string][]byte{"key1": []byte("value1")},
						Timestamp: 1234567890,
					},
				},
			},
			wantMsg: &GossipSyncMessage{
				Version:   100,
				Metadata:  map[string][]byte{"key1": []byte("value1")},
				Timestamp: 1234567890,
			},
		},
		{
			name: "GossipDigestMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypeGossipDigest),
				MessageBody: &proto.WrapperMessageProto_GossipDigestMsg{
					GossipDigestMsg: &proto.GossipDigestMessageProto{
						Version: 300,
						Digest:  map[string]uint64{"key1": 100},
					},
				},
			},
			wantMsg: &GossipDigestMessage{
				Version: 300,
				Digest:  map[string]uint64{"key1": 100},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := codec.wrapperToMessage(tc.wrapper)
			require.NoError(t, err)

			// 验证消息类型和内容
			switch want := tc.wantMsg.(type) {
			case *GossipSyncMessage:
				gossipMsg, ok := msg.(*GossipSyncMessage)
				require.True(t, ok)
				assert.Equal(t, want.Version, gossipMsg.Version)
				assert.Equal(t, want.Metadata, gossipMsg.Metadata)
				assert.Equal(t, want.Timestamp, gossipMsg.Timestamp)
			case *GossipDigestMessage:
				digestMsg, ok := msg.(*GossipDigestMessage)
				require.True(t, ok)
				assert.Equal(t, want.Version, digestMsg.Version)
				assert.Equal(t, want.Digest, digestMsg.Digest)
			}
		})
	}
}

// TestProtobufCodec_WrapperToMessage_TwoPC 测试 TwoPC 消息的逆向转换
func TestProtobufCodec_WrapperToMessage_TwoPC(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name    string
		wrapper *proto.WrapperMessageProto
		wantMsg Message
	}{
		{
			name: "TwoPCPrepareMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageType2PCPrepare),
				MessageBody: &proto.WrapperMessageProto_TwopcPrepareMsg{
					TwopcPrepareMsg: &proto.TwoPCPrepareMessageProto{
						TransactionId: "txn-123",
						Participants:  []string{"node-1", "node-2"},
						Operations: []*proto.OperationProto{
							{Type: "SET", Key: "key1", Value: []byte("value1")},
						},
						Timestamp: 60000,
					},
				},
			},
			wantMsg: &TwoPCPrepareMessage{
				TransactionID: "txn-123",
				Participants:  []string{"node-1", "node-2"},
				Operations: []Operation{
					{Type: "SET", Key: "key1", Value: []byte("value1")},
				},
				Timeout: 60000,
			},
		},
		{
			name: "TwoPCCommitMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageType2PCCommit),
				MessageBody: &proto.WrapperMessageProto_TwopcCommitMsg{
					TwopcCommitMsg: &proto.TwoPCCommitMessageProto{
						TransactionId: "txn-123",
					},
				},
			},
			wantMsg: &TwoPCCommitMessage{
				TransactionID: "txn-123",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := codec.wrapperToMessage(tc.wrapper)
			require.NoError(t, err)

			// 验证消息类型和内容
			switch want := tc.wantMsg.(type) {
			case *TwoPCPrepareMessage:
				prepareMsg, ok := msg.(*TwoPCPrepareMessage)
				require.True(t, ok)
				assert.Equal(t, want.TransactionID, prepareMsg.TransactionID)
				assert.Equal(t, want.Participants, prepareMsg.Participants)
				assert.Equal(t, want.Operations, prepareMsg.Operations)
			case *TwoPCCommitMessage:
				commitMsg, ok := msg.(*TwoPCCommitMessage)
				require.True(t, ok)
				assert.Equal(t, want.TransactionID, commitMsg.TransactionID)
			}
		})
	}
}

// TestProtobufCodec_WrapperToMessage_Node 测试节点消息的逆向转换
func TestProtobufCodec_WrapperToMessage_Node(t *testing.T) {
	codec := NewProtobufCodec()

	testCases := []struct {
		name    string
		wrapper *proto.WrapperMessageProto
		wantMsg Message
	}{
		{
			name: "NodePingMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypeNodePing),
				MessageBody: &proto.WrapperMessageProto_NodePingMsg{
					NodePingMsg: &proto.NodePingMessageProto{
						NodeId:    "node-1",
						Sequence:  123,
						Timestamp: 1234567890,
					},
				},
			},
			wantMsg: &NodePingMessage{
				NodeID:    "node-1",
				Sequence:  123,
				Timestamp: 1234567890,
			},
		},
		{
			name: "NodePongMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypeNodePong),
				MessageBody: &proto.WrapperMessageProto_NodePongMsg{
					NodePongMsg: &proto.NodePongMessageProto{
						NodeId:    "node-1",
						Sequence:  123,
						Status:    "ready",
						Timestamp: 1234567890,
					},
				},
			},
			wantMsg: &NodePongMessage{
				NodeID:    "node-1",
				Sequence:  123,
				Status:    "ready",
				Timestamp: 1234567890,
			},
		},
		{
			name: "NodeJoinMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypeNodeJoin),
				MessageBody: &proto.WrapperMessageProto_NodeJoinMsg{
					NodeJoinMsg: &proto.NodeJoinMessageProto{
						NodeId:   "node-2",
						Addr:     "127.0.0.1:9211",
						Role:     "leaf",
						ParentId: "node-1",
					},
				},
			},
			wantMsg: &NodeJoinMessage{
				NodeID:   "node-2",
				Addr:     "127.0.0.1:9211",
				Role:     "leaf",
				ParentID: "node-1",
			},
		},
		{
			name: "NodeLeaveMessage",
			wrapper: &proto.WrapperMessageProto{
				MessageType: uint32(types.MessageTypeNodeLeave),
				MessageBody: &proto.WrapperMessageProto_NodeLeaveMsg{
					NodeLeaveMsg: &proto.NodeLeaveMessageProto{
						NodeId: "node-2",
						Reason: "shutdown",
					},
				},
			},
			wantMsg: &NodeLeaveMessage{
				NodeID: "node-2",
				Reason: "shutdown",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := codec.wrapperToMessage(tc.wrapper)
			require.NoError(t, err)

			// 验证消息类型和内容
			switch want := tc.wantMsg.(type) {
			case *NodePingMessage:
				pingMsg, ok := msg.(*NodePingMessage)
				require.True(t, ok)
				assert.Equal(t, want.NodeID, pingMsg.NodeID)
				assert.Equal(t, want.Sequence, pingMsg.Sequence)
				assert.Equal(t, want.Timestamp, pingMsg.Timestamp)
			case *NodePongMessage:
				pongMsg, ok := msg.(*NodePongMessage)
				require.True(t, ok)
				assert.Equal(t, want.NodeID, pongMsg.NodeID)
				assert.Equal(t, want.Sequence, pongMsg.Sequence)
				assert.Equal(t, want.Status, pongMsg.Status)
			case *NodeJoinMessage:
				joinMsg, ok := msg.(*NodeJoinMessage)
				require.True(t, ok)
				assert.Equal(t, want.NodeID, joinMsg.NodeID)
				assert.Equal(t, want.Addr, joinMsg.Addr)
				assert.Equal(t, want.Role, joinMsg.Role)
				assert.Equal(t, want.ParentID, joinMsg.ParentID)
			case *NodeLeaveMessage:
				leaveMsg, ok := msg.(*NodeLeaveMessage)
				require.True(t, ok)
				assert.Equal(t, want.NodeID, leaveMsg.NodeID)
				assert.Equal(t, want.Reason, leaveMsg.Reason)
			}
		})
	}
}

// TestProtobufCodec_RoundTrip_MessageWrapper 测试完整的往返转换
func TestProtobufCodec_RoundTrip_MessageWrapper(t *testing.T) {
	codec := NewProtobufCodec()

	originalMsg := &PutMessage{
		Key:   "test-roundtrip-key",
		Value: []byte("test-roundtrip-value"),
	}

	// Message -> Wrapper
	wrapper, err := codec.messageToWrapper(originalMsg)
	require.NoError(t, err)
	require.NotNil(t, wrapper)

	// Wrapper -> Message
	decodedMsg, err := codec.wrapperToMessage(wrapper)
	require.NoError(t, err)

	// 验证
	putMsg, ok := decodedMsg.(*PutMessage)
	require.True(t, ok)
	assert.Equal(t, originalMsg.Key, putMsg.Key)
	assert.Equal(t, originalMsg.Value, putMsg.Value)
}

// TestProtobufCodec_WrapperToMessage_NilBody 测试空消息体错误
func TestProtobufCodec_WrapperToMessage_NilBody(t *testing.T) {
	codec := NewProtobufCodec()

	// 测试空消息体
	wrapper := &proto.WrapperMessageProto{
		MessageType: uint32(types.MessageTypeGet),
		MessageBody: nil,
	}

	msg, err := codec.wrapperToMessage(wrapper)
	assert.Error(t, err)
	assert.Nil(t, msg)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecInvalidData, cerr.Code)
	}
}

// ========================================
// Encode 错误处理测试
// ========================================

// TestProtobufCodec_Encode_NilMessage 测试 nil 消息编码
func TestProtobufCodec_Encode_NilMessage(t *testing.T) {
	codec := NewProtobufCodec()

	data, err := codec.Encode(nil)
	assert.Error(t, err)
	assert.Nil(t, data)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecInvalidMessage, cerr.Code)
	}
}

// ========================================
// Decode 错误处理测试
// ========================================

// TestProtobufCodec_Decode_EmptyData 测试空数据解码
func TestProtobufCodec_Decode_EmptyData(t *testing.T) {
	codec := NewProtobufCodec()

	msg, err := codec.Decode(types.MessageTypeGet, []byte{})
	assert.Error(t, err)
	assert.Nil(t, msg)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecInvalidData, cerr.Code)
	}
}

// TestProtobufCodec_Decode_InvalidProtobuf 测试无效 Protobuf 数据解码
func TestProtobufCodec_Decode_InvalidProtobuf(t *testing.T) {
	codec := NewProtobufCodec()

	invalidData := []byte{0xFF, 0xFF, 0xFF, 0xFF} // 无效的 Protobuf 数据
	msg, err := codec.Decode(types.MessageTypeGet, invalidData)
	assert.Error(t, err)
	assert.Nil(t, msg)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecDecodeFailed, cerr.Code)
	}
}

// TestProtobufCodec_Decode_NilBodyInWrapper 测试包装消息中消息体为空
func TestProtobufCodec_Decode_NilBodyInWrapper(t *testing.T) {
	codec := NewProtobufCodec()

	// 创建一个有效的 Wrapper，但 MessageBody 为 nil
	wrapper := &proto.WrapperMessageProto{
		MessageType: uint32(types.MessageTypeGet),
		MessageBody: nil,
	}
	data, _ := googleproto.Marshal(wrapper)

	msg, err := codec.Decode(types.MessageTypeGet, data)
	assert.Error(t, err)
	assert.Nil(t, msg)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecDecodeFailed, cerr.Code)
	}
}

// ========================================
// DecodeInto 错误处理测试
// ========================================

// TestProtobufCodec_DecodeInto_NilMessage 测试 nil 消息实例解码
func TestProtobufCodec_DecodeInto_NilMessage(t *testing.T) {
	codec := NewProtobufCodec()

	data := []byte("test data")
	err := codec.DecodeInto(data, nil)
	assert.Error(t, err)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecInvalidMessage, cerr.Code)
	}
}

// TestProtobufCodec_DecodeInto_EmptyData 测试空数据解码到实例
func TestProtobufCodec_DecodeInto_EmptyData(t *testing.T) {
	codec := NewProtobufCodec()

	msg := &GetMessage{}
	err := codec.DecodeInto([]byte{}, msg)
	assert.Error(t, err)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecInvalidData, cerr.Code)
	}
}

// TestProtobufCodec_DecodeInto_InvalidProtobuf 测试无效 Protobuf 数据解码到实例
func TestProtobufCodec_DecodeInto_InvalidProtobuf(t *testing.T) {
	codec := NewProtobufCodec()

	invalidData := []byte{0xFF, 0xFF, 0xFF, 0xFF} // 无效的 Protobuf 数据
	msg := &GetMessage{}
	err := codec.DecodeInto(invalidData, msg)
	assert.Error(t, err)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecDecodeFailed, cerr.Code)
	}
}

// TestProtobufCodec_DecodeInto_NilBodyInWrapper 测试包装消息中消息体为空
func TestProtobufCodec_DecodeInto_NilBodyInWrapper(t *testing.T) {
	codec := NewProtobufCodec()

	// 创建一个有效的 Wrapper，但 MessageBody 为 nil
	wrapper := &proto.WrapperMessageProto{
		MessageType: uint32(types.MessageTypeGet),
		MessageBody: nil,
	}
	data, _ := googleproto.Marshal(wrapper)

	msg := &GetMessage{}
	err := codec.DecodeInto(data, msg)
	assert.Error(t, err)
	if cerr, ok := err.(*types.Error); ok {
		assert.Equal(t, types.ErrCodecDecodeFailed, cerr.Code)
	}
}
