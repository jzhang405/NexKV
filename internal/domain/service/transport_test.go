package service

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// TestTransport_PeerID 测试 PeerID 类型
func TestTransport_PeerID(t *testing.T) {
	peer := model.PeerID("test-node")
	if peer.String() != "test-node" {
		t.Errorf("Expected PeerID to be 'test-node', got %s", peer.String())
	}
}
