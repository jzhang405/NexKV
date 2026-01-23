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
			msg:            &GetMessage{Key: "test"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "PutMessage",
			msg:            &PutMessage{Key: "test", Value: []byte("value")},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "DeleteMessage",
			msg:            &DeleteMessage{Key: "test"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "GetReplyMessage",
			msg:            &GetReplyMessage{Key: "test", Found: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "PutReplyMessage",
			msg:            &PutReplyMessage{Key: "test", Success: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "DeleteReplyMessage",
			msg:            &DeleteReplyMessage{Key: "test", Success: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "GossipSyncMessage",
			msg:            &GossipSyncMessage{Version: 1},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "GossipSyncReplyMessage",
			msg:            &GossipSyncReplyMessage{Accepted: true},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "GossipDigestMessage",
			msg:            &GossipDigestMessage{Version: 1},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "GossipDigestReplyMessage",
			msg:            &GossipDigestReplyMessage{Version: 1},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "QuorumProposeMessage",
			msg:            &QuorumProposeMessage{ProposalID: "prop-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "QuorumVoteMessage",
			msg:            &QuorumVoteMessage{ProposalID: "prop-1", Vote: true},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "QuorumDecideMessage",
			msg:            &QuorumDecideMessage{ProposalID: "prop-1", Approved: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCPrepareMessage",
			msg:            &TwoPCPrepareMessage{TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCPrepareReplyMessage",
			msg:            &TwoPCPrepareReplyMessage{TransactionID: "txn-1", Vote: "commit"},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCCommitMessage",
			msg:            &TwoPCCommitMessage{TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCRollbackMessage",
			msg:            &TwoPCRollbackMessage{TransactionID: "txn-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCCommitReplyMessage",
			msg:            &TwoPCCommitReplyMessage{TransactionID: "txn-1", Success: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "TwoPCRollbackReplyMessage",
			msg:            &TwoPCRollbackReplyMessage{TransactionID: "txn-1", Success: true},
			expectResponse: types.NoResponse,
			reliability:    types.Reliable,
		},
		{
			name:           "NodePingMessage",
			msg:            &NodePingMessage{NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "NodePongMessage",
			msg:            &NodePongMessage{NodeID: "node-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "NodeJoinMessage",
			msg:            &NodeJoinMessage{NodeID: "node-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "NodeLeaveMessage",
			msg:            &NodeLeaveMessage{NodeID: "node-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "NodeSyncMessage",
			msg:            &NodeSyncMessage{Version: 1},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "ClockSyncMessage",
			msg:            &ClockSyncMessage{NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "ClockSyncReplyMessage",
			msg:            &ClockSyncReplyMessage{NodeID: "node-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "ClusterStatusMessage",
			msg:            &ClusterStatusMessage{NodeID: "node-1"},
			expectResponse: types.ExpectResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "ClusterStatusReplyMessage",
			msg:            &ClusterStatusReplyMessage{},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "LeaderElectionMessage",
			msg:            &LeaderElectionMessage{ElectionID: "election-1"},
			expectResponse: types.NoResponse,
			reliability:    types.BestEffort,
		},
		{
			name:           "BaseMessage",
			msg:            NewBaseMessage(types.MessageTypeGet, []byte("payload")),
			expectResponse: types.ExpectResponse,
			reliability:    types.Reliable,
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
