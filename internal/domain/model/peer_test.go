package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPeerID_String(t *testing.T) {
	tests := []struct {
		name     string
		peerID   PeerID
		expected string
	}{
		{
			name:     "empty peer ID",
			peerID:   "",
			expected: "",
		},
		{
			name:     "simple peer ID",
			peerID:   "node-001",
			expected: "node-001",
		},
		{
			name:     "complex peer ID",
			peerID:   "QmYwAPJzv5CZsnA625s3Xf2nemtYgum4Zk8N5jLx5N8Z",
			expected: "QmYwAPJzv5CZsnA625s3Xf2nemtYgum4Zk8N5jLx5N8Z",
		},
		{
			name:     "peer ID with special characters",
			peerID:   "node-001@example.com",
			expected: "node-001@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.peerID.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPeerID_Empty(t *testing.T) {
	var emptyPeerID PeerID
	nonEmptyPeerID := PeerID("node-001")

	assert.Empty(t, emptyPeerID)
	assert.NotEmpty(t, nonEmptyPeerID)
}

func TestPeerID_Comparison(t *testing.T) {
	peer1 := PeerID("node-001")
	peer2 := PeerID("node-001")
	peer3 := PeerID("node-002")

	assert.Equal(t, peer1, peer2)
	assert.NotEqual(t, peer1, peer3)
}
