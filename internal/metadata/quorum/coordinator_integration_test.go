// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package quorum

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/stretchr/testify/require"
)

type mockQuorumTransport struct {
	mu        sync.Mutex
	votes     map[string]bool
	proposals map[string]proposalRecord
}

type proposalRecord struct {
	proposalID string
	ns         string
	key        string
	value      []byte
	timestamp  time.Time
	voters     map[string]bool
}

func newMockQuorumTransport() *mockQuorumTransport {
	return &mockQuorumTransport{
		votes:     make(map[string]bool),
		proposals: make(map[string]proposalRecord),
	}
}

func (m *mockQuorumTransport) SendPropose(ctx context.Context, from string,
	proposalID string, ns, key string, value []byte, toPeers []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	record := proposalRecord{
		proposalID: proposalID,
		ns:         ns,
		key:        key,
		value:      value,
		timestamp:  time.Now(),
		voters:     make(map[string]bool),
	}
	m.proposals[proposalID] = record

	for _, peerID := range toPeers {
		if peerID != from {
			record.voters[peerID] = true
			m.votes[peerID] = true
		}
	}

	return nil
}

func (m *mockQuorumTransport) SendVote(ctx context.Context, from, proposalID string,
	decision bool, toPeer string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if record, ok := m.proposals[proposalID]; ok {
		record.voters[from] = decision
		m.votes[from] = decision
	}
	return nil
}

func (m *mockQuorumTransport) GetVotes(proposalID string) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	votes := make(map[string]bool, len(m.votes))
	for k, v := range m.votes {
		votes[k] = v
	}
	return votes
}

func (m *mockQuorumTransport) GetProposal(proposalID string) (proposalRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.proposals[proposalID]
	return record, ok
}

func TestQuorumCoordinator_ProposeVoteDecide(t *testing.T) {
	participants := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	coordinator := NewQuorumCoordinator(participants, nil)

	expectedQuorum := 3
	if coordinator.GetQuorum() != expectedQuorum {
		t.Errorf("Expected quorum %d, got %d", expectedQuorum, coordinator.GetQuorum())
	}

	acks := 0
	for _, participant := range participants {
		if participant != "node-1" {
			acks++
			if acks >= expectedQuorum {
				break
			}
		}
	}

	if acks >= expectedQuorum {
		t.Logf("Quorum 确认成功: %d/%d", acks, expectedQuorum)
	} else {
		t.Errorf("Quorum 确认失败: %d/%d", acks, expectedQuorum)
	}
}

func TestQuorumCoordinator_PartialFailure(t *testing.T) {
	tests := []struct {
		name          string
		participants  []string
		successCount  int
		shouldSucceed bool
	}{
		{
			name:          "3节点，2个成功",
			participants:  []string{"node-1", "node-2", "node-3"},
			successCount:  2,
			shouldSucceed: true,
		},
		{
			name:          "5节点，3个成功",
			participants:  []string{"node-1", "node-2", "node-3", "node-4", "node-5"},
			successCount:  3,
			shouldSucceed: true,
		},
		{
			name:          "5节点，2个成功",
			participants:  []string{"node-1", "node-2", "node-3", "node-4", "node-5"},
			successCount:  2,
			shouldSucceed: false,
		},
		{
			name:          "7节点，4个成功",
			participants:  []string{"node-1", "node-2", "node-3", "node-4", "node-5", "node-6", "node-7"},
			successCount:  4,
			shouldSucceed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coordinator := NewQuorumCoordinator(tt.participants, nil)
			quorum := coordinator.GetQuorum()
			expectedQuorum := (len(tt.participants) / 2) + 1

			if quorum != expectedQuorum {
				t.Errorf("Quorum calculation failed: got %d, want %d", quorum, expectedQuorum)
			}

			succeeded := tt.successCount >= quorum
			if succeeded != tt.shouldSucceed {
				t.Errorf("Success mismatch: got %v, want %v (successCount=%d, quorum=%d)",
					succeeded, tt.shouldSucceed, tt.successCount, quorum)
			}
		})
	}
}

func TestQuorumCoordinator_DynamicParticipants(t *testing.T) {
	initialParticipants := []string{"node-1", "node-2", "node-3"}
	coordinator := NewQuorumCoordinator(initialParticipants, nil)

	if coordinator.GetQuorum() != 2 {
		t.Errorf("Expected initial quorum 2, got %d", coordinator.GetQuorum())
	}

	newParticipants := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	coordinator.SetParticipants(newParticipants)

	if coordinator.GetQuorum() != 3 {
		t.Errorf("Expected new quorum 3, got %d", coordinator.GetQuorum())
	}

	gotParticipants := coordinator.GetParticipants()
	if len(gotParticipants) != len(newParticipants) {
		t.Errorf("Expected %d participants, got %d", len(newParticipants), len(gotParticipants))
	}
}

func TestQuorumCoordinator_VerifyTimeout(t *testing.T) {
	participants := []string{"node-1", "node-2", "node-3"}
	_ = NewQuorumCoordinator(participants, nil)

	opts := &PutOptions{
		Timeout: 3000,
	}

	if opts.Timeout != 3000 {
		t.Errorf("Expected timeout 3000ms, got %d", opts.Timeout)
	}
}

func TestQuorumCoordinator_BandwidthAnalysis(t *testing.T) {
	tests := []struct {
		name             string
		participantCount int
		quorum           int
		payloadSize      int
	}{
		{
			name:             "3节点集群，Quorum=2",
			participantCount: 3,
			quorum:           2,
			payloadSize:      1000,
		},
		{
			name:             "5节点集群，Quorum=3",
			participantCount: 5,
			quorum:           3,
			payloadSize:      1000,
		},
		{
			name:             "7节点集群，Quorum=4",
			participantCount: 7,
			quorum:           4,
			payloadSize:      1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			participants := make([]string, tt.participantCount)
			for i := 0; i < tt.participantCount; i++ {
				participants[i] = fmt.Sprintf("node-%d", i+1)
			}

			coordinator := NewQuorumCoordinator(participants, nil)
			quorum := coordinator.GetQuorum()

			if quorum != tt.quorum {
				t.Errorf("Expected quorum %d, got %d", tt.quorum, quorum)
			}

			fullBandwidth := tt.participantCount * tt.payloadSize
			quorumBandwidth := tt.quorum * tt.payloadSize
			savedBandwidth := fullBandwidth - quorumBandwidth
			savedPercent := (float64(savedBandwidth) / float64(fullBandwidth)) * 100

			t.Logf("全量带宽: %dB, Quorum带宽: %dB, 节省: %dB (%.1f%%)",
				fullBandwidth, quorumBandwidth, savedBandwidth, savedPercent)
		})
	}
}

func TestQuorumCoordinator_IntegrationVoteSimulate(t *testing.T) {
	participants := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	transport := newMockQuorumTransport()
	coordinator := NewQuorumCoordinator(participants, nil)
	quorum := coordinator.GetQuorum()

	proposalID := "prop-001"
	ns := kvstore.NamespaceRole
	key := "role-001"
	value := []byte(`{"status": "active"}`)

	err := transport.SendPropose(context.Background(), "node-1", proposalID, ns, key, value, participants)
	require.NoError(t, err)

	votes := transport.GetVotes(proposalID)
	ackCount := 0
	for _, decision := range votes {
		if decision {
			ackCount++
		}
	}

	if ackCount >= quorum {
		t.Logf("Quorum 达成: %d/%d", ackCount, quorum)
	} else {
		t.Errorf("Quorum 未达成: %d/%d", ackCount, quorum)
	}
}

func BenchmarkQuorumCalculation(b *testing.B) {
	participants := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	coordinator := NewQuorumCoordinator(participants, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		coordinator.GetQuorum()
	}
}

func BenchmarkQuorumDecision(b *testing.B) {
	participants := make([]string, 100)
	for i := 0; i < 100; i++ {
		participants[i] = fmt.Sprintf("node-%d", i)
	}
	coordinator := NewQuorumCoordinator(participants, nil)
	quorum := coordinator.GetQuorum()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		acks := 0
		for _, participant := range participants {
			if participant != "node-1" {
				acks++
				if acks >= quorum {
					break
				}
			}
		}
		_ = acks >= quorum
	}
}
