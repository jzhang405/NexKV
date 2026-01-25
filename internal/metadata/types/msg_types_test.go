// Package types 消息类型测试
//
// 测试 MessageType 的 ExpectResponse()、ProtocolType() 和 MsgRole() 方法
package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMessageType_ExpectResponseAndProtocolType 测试所有消息类型的 ExpectResponse()、ProtocolType() 和 MsgRole() 方法
func TestMessageType_ExpectResponseAndProtocolType(t *testing.T) {
	tests := []struct {
		name           string
		msgType        MessageType
		expectResponse ResponseExpectation
		protocolType   ProtocolType
		msgRole        MsgRole
	}{
		{
			name:           "GetMessage",
			msgType:        MessageTypeGet,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "PutMessage",
			msgType:        MessageTypePut,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "DeleteMessage",
			msgType:        MessageTypeDelete,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "GetReplyMessage",
			msgType:        MessageTypeGetReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "PutReplyMessage",
			msgType:        MessageTypePutReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "DeleteReplyMessage",
			msgType:        MessageTypeDeleteReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "GossipSyncMessage",
			msgType:        MessageTypeGossipSync,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "GossipSyncReplyMessage",
			msgType:        MessageTypeGossipSyncReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "GossipDigestMessage",
			msgType:        MessageTypeGossipDigest,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "GossipDigestReplyMessage",
			msgType:        MessageTypeGossipDigestReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "QuorumProposeMessage",
			msgType:        MessageTypeQuorumPropose,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "QuorumVoteMessage",
			msgType:        MessageTypeQuorumVote,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "QuorumDecideMessage",
			msgType:        MessageTypeQuorumDecide,
			expectResponse: NoResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "TwoPCPrepareMessage",
			msgType:        MessageType2PCPrepare,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "TwoPCPrepareReplyMessage",
			msgType:        MessageType2PCPrepareReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "TwoPCCommitMessage",
			msgType:        MessageType2PCCommit,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "TwoPCRollbackMessage",
			msgType:        MessageType2PCRollback,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "TwoPCCommitReplyMessage",
			msgType:        MessageType2PCCommitReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "TwoPCRollbackReplyMessage",
			msgType:        MessageType2PCRollbackReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolTCP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "NodePingMessage",
			msgType:        MessageTypeNodePing,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "NodePongMessage",
			msgType:        MessageTypeNodePong,
			expectResponse: NoResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "NodeJoinMessage",
			msgType:        MessageTypeNodeJoin,
			expectResponse: NoResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "NodeLeaveMessage",
			msgType:        MessageTypeNodeLeave,
			expectResponse: NoResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "NodeSyncMessage",
			msgType:        MessageTypeNodeSync,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "ClockSyncMessage",
			msgType:        MessageTypeClockSync,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "ClockSyncReplyMessage",
			msgType:        MessageTypeClockSyncReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "ClusterStatusMessage",
			msgType:        MessageTypeClusterStatus,
			expectResponse: ExpectResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
		{
			name:           "ClusterStatusReplyMessage",
			msgType:        MessageTypeClusterStatusReply,
			expectResponse: NoResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleResponse,
		},
		{
			name:           "LeaderElectionMessage",
			msgType:        MessageTypeLeaderElection,
			expectResponse: NoResponse,
			protocolType:   ProtocolUDP,
			msgRole:        MsgRoleRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 ExpectResponse()
			require.Equal(t, tt.expectResponse, tt.msgType.ExpectResponse())

			// 测试 ProtocolType()
			require.Equal(t, tt.protocolType, tt.msgType.ProtocolType())

			// 测试 MsgRole()
			require.Equal(t, tt.msgRole, tt.msgType.MsgRole())
		})
	}
}
