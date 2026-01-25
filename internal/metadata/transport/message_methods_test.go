package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// TestMessage_ExpectResponseAndProtocolType 测试所有消息类型的 ExpectResponse()、ProtocolType() 和 MsgRole() 方法
func TestMessage_ExpectResponseAndProtocolType(t *testing.T) {
	tests := []struct {
		name           string
		msg            Message
		expectResponse types.ResponseExpectation
		protocolType   types.ProtocolType
		msgRole        types.MsgRole
	}{
		{
			name:           "GetMessage",
			msg:            &GetMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGet}, Key: "test"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "PutMessage",
			msg:            &PutMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypePut}, Key: "test", Value: []byte("value")},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "DeleteMessage",
			msg:            &DeleteMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeDelete}, Key: "test"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "GetReplyMessage",
			msg:            &GetReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGetReply}, Key: "test", Found: true},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "PutReplyMessage",
			msg:            &PutReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypePutReply}, Key: "test", Success: true},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "DeleteReplyMessage",
			msg:            &DeleteReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeDeleteReply}, Key: "test", Success: true},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "GossipSyncMessage",
			msg:            &GossipSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipSync}, Version: 1},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "GossipSyncReplyMessage",
			msg:            &GossipSyncReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipSyncReply}, Accepted: true},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "GossipDigestMessage",
			msg:            &GossipDigestMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipDigest}, Version: 1},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "GossipDigestReplyMessage",
			msg:            &GossipDigestReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipDigestReply}, Version: 1},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "QuorumProposeMessage",
			msg:            &QuorumProposeMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumPropose}, ProposalID: "prop-1"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "QuorumVoteMessage",
			msg:            &QuorumVoteMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumVote}, ProposalID: "prop-1", Vote: true},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "QuorumDecideMessage",
			msg:            &QuorumDecideMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumDecide}, ProposalID: "prop-1", Approved: true},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "TwoPCPrepareMessage",
			msg:            &TwoPCPrepareMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCPrepare}, TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "TwoPCPrepareReplyMessage",
			msg:            &TwoPCPrepareReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCPrepareReply}, TransactionID: "txn-1", Vote: "commit"},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "TwoPCCommitMessage",
			msg:            &TwoPCCommitMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCCommit}, TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "TwoPCRollbackMessage",
			msg:            &TwoPCRollbackMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCRollback}, TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "TwoPCCommitReplyMessage",
			msg:            &TwoPCCommitReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCCommitReply}, TransactionID: "txn-1", Success: true},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "TwoPCRollbackReplyMessage",
			msg:            &TwoPCRollbackReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCRollbackReply}, TransactionID: "txn-1", Success: true},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolTCP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "NodePingMessage",
			msg:            &NodePingMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePing}, NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "NodePongMessage",
			msg:            &NodePongMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePong}, NodeID: "node-1"},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "NodeJoinMessage",
			msg:            &NodeJoinMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeJoin}, NodeID: "node-1"},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "NodeLeaveMessage",
			msg:            &NodeLeaveMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeLeave}, NodeID: "node-1"},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "NodeSyncMessage",
			msg:            &NodeSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeSync}, Version: 1},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "ClockSyncMessage",
			msg:            &ClockSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClockSync}, NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "ClockSyncReplyMessage",
			msg:            &ClockSyncReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClockSyncReply}, NodeID: "node-1"},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "ClusterStatusMessage",
			msg:            &ClusterStatusMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClusterStatus}, NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
		{
			name:           "ClusterStatusReplyMessage",
			msg:            &ClusterStatusReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClusterStatusReply}},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleResponse,
		},
		{
			name:           "LeaderElectionMessage",
			msg:            &LeaderElectionMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeLeaderElection}, ElectionID: "election-1"},
			expectResponse: types.NoResponse,
			protocolType:   types.ProtocolUDP,
			msgRole:        types.MsgRoleRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 ExpectResponse()
			if got := tt.msg.ExpectResponse(); got != tt.expectResponse {
				t.Errorf("ExpectResponse() = %v, want %v", got, tt.expectResponse)
			}

			// 测试 ProtocolType()
			if got := tt.msg.ProtocolType(); got != tt.protocolType {
				t.Errorf("ProtocolType() = %v, want %v", got, tt.protocolType)
			}

			// 测试 MsgRole()
			if got := tt.msg.MsgRole(); got != tt.msgRole {
				t.Errorf("MsgRole() = %v, want %v", got, tt.msgRole)
			}
		})
	}
}
