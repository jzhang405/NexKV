// Package rpc 基于 libp2p Stream 的 RPC 实现
// Fanout 集成测试
package rpc

import (
	"context"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
)

// ========================================
// Integration Mock Tests
// ========================================

// MockFanoutClient 模拟 Fanout Client（用于集成测试）
type MockFanoutClient struct {
	callFunc func(ctx context.Context, peerID peer.ID, method string, body []byte) ([]byte, error)
}

func (m *MockFanoutClient) Call(ctx context.Context, peerID peer.ID, method string, body []byte) ([]byte, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, peerID, method, body)
	}
	return []byte("mock response"), nil
}

// TestFanout_IntegrationWithQuorum 测试与 Quorum 集成
func TestFanout_IntegrationWithQuorum(t *testing.T) {
	manager := NewQuorumManager(&QuorumConfig{
		Enabled:       true,
		DefaultQuorum: 0, // 动态计算
		MinQuorum:     1,
	})

	peers := []peer.ID{"peer1", "peer2", "peer3", "peer4", "peer5"}

	// 测试 Quorum 阈值计算
	quorum := manager.GetQuorumThreshold(len(peers))
	assert.Equal(t, 3, quorum, "5个peers的Quorum应该是3")

	// 测试 Quorum 结果计算
	result := manager.CalculateQuorumResult(
		len(peers),
		3, // 成功3个
		nil,
		nil,
	)

	assert.True(t, result.Success, "3/5应该达到Quorum")
	assert.Equal(t, 3, result.Quorum)
}
