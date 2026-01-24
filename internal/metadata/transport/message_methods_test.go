package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// TestMessage_ExpectResponseAndReliability 测试所有消息类型的 ExpectResponse() 和 Reliability() 方法
func TestMessage_ExpectResponseAndReliability(t *testing.T) {
	tests := []struct {
		name           string
		msg            Message
		expectResponse types.ResponseExpectation
		reliability    types.ReliabilityRequirement
	}{
		{
			name:           "GetMessage",
			msg:            &GetMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGet}, Key: "test"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "PutMessage",
			msg:            &PutMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypePut}, Key: "test", Value: []byte("value")},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "DeleteMessage",
			msg:            &DeleteMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeDelete}, Key: "test"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "GetReplyMessage",
			msg:            &GetReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGetReply}, Key: "test", Found: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "PutReplyMessage",
			msg:            &PutReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypePutReply}, Key: "test", Success: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "DeleteReplyMessage",
			msg:            &DeleteReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeDeleteReply}, Key: "test", Success: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "GossipSyncMessage",
			msg:            &GossipSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipSync}, Version: 1},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "GossipSyncReplyMessage",
			msg:            &GossipSyncReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipSyncReply}, Accepted: true},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "GossipDigestMessage",
			msg:            &GossipDigestMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipDigest}, Version: 1},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "GossipDigestReplyMessage",
			msg:            &GossipDigestReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeGossipDigestReply}, Version: 1},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "QuorumProposeMessage",
			msg:            &QuorumProposeMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumPropose}, ProposalID: "prop-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "QuorumVoteMessage",
			msg:            &QuorumVoteMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumVote}, ProposalID: "prop-1", Vote: true},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "QuorumDecideMessage",
			msg:            &QuorumDecideMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeQuorumDecide}, ProposalID: "prop-1", Approved: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCPrepareMessage",
			msg:            &TwoPCPrepareMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCPrepare}, TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCPrepareReplyMessage",
			msg:            &TwoPCPrepareReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCPrepareReply}, TransactionID: "txn-1", Vote: "commit"},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCCommitMessage",
			msg:            &TwoPCCommitMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCCommit}, TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCRollbackMessage",
			msg:            &TwoPCRollbackMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCRollback}, TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCCommitReplyMessage",
			msg:            &TwoPCCommitReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCCommitReply}, TransactionID: "txn-1", Success: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCRollbackReplyMessage",
			msg:            &TwoPCRollbackReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageType2PCRollbackReply}, TransactionID: "txn-1", Success: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "NodePingMessage",
			msg:            &NodePingMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePing}, NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "NodePongMessage",
			msg:            &NodePongMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodePong}, NodeID: "node-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "NodeJoinMessage",
			msg:            &NodeJoinMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeJoin}, NodeID: "node-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "NodeLeaveMessage",
			msg:            &NodeLeaveMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeLeave}, NodeID: "node-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "NodeSyncMessage",
			msg:            &NodeSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeNodeSync}, Version: 1},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "ClockSyncMessage",
			msg:            &ClockSyncMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClockSync}, NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "ClockSyncReplyMessage",
			msg:            &ClockSyncReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClockSyncReply}, NodeID: "node-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "ClusterStatusMessage",
			msg:            &ClusterStatusMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClusterStatus}, NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "ClusterStatusReplyMessage",
			msg:            &ClusterStatusReplyMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeClusterStatusReply}},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "LeaderElectionMessage",
			msg:            &LeaderElectionMessage{BaseMessage: BaseMessage{MessageType: types.MessageTypeLeaderElection}, ElectionID: "election-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 ExpectResponse()
			if got := tt.msg.ExpectResponse(); got != tt.expectResponse {
				t.Errorf("ExpectResponse() = %v, want %v", got, tt.expectResponse)
			}

			// 测试 Reliability()
			if got := tt.msg.Reliability(); got != tt.reliability {
				t.Errorf("Reliability() = %v, want %v", got, tt.reliability)
			}
		})
	}
}
