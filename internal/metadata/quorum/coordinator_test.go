package quorum

import (
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// TestCalculateQuorum 测试 Quorum 计算
func TestCalculateQuorum(t *testing.T) {
	tests := []struct {
		n      int
		expect int
	}{
		{0, 0},
		{1, 1},
		{2, 2},
		{3, 2},
		{4, 3},
		{5, 3},
		{6, 4},
		{7, 4},
		{10, 6},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := calculateQuorum(tt.n)
			if got != tt.expect {
				t.Errorf("calculateQuorum(%d) = %d, want %d", tt.n, got, tt.expect)
			}
		})
	}
}

// TestNewQuorumCoordinator 测试创建 Quorum 协调器
func TestNewQuorumCoordinator(t *testing.T) {
	participants := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	coordinator := NewQuorumCoordinator(participants, nil)

	if coordinator == nil {
		t.Fatal("NewQuorumCoordinator returned nil")
	}

	// 验证 Quorum 阈值（5 个节点 → 3 个确认）
	expectedQuorum := 3
	if coordinator.quorum != expectedQuorum {
		t.Errorf("Expected quorum %d, got %d", expectedQuorum, coordinator.quorum)
	}

	// 验证默认超时
	expectedTimeout := 3 * time.Second
	if coordinator.timeout != expectedTimeout {
		t.Errorf("Expected timeout %v, got %v", expectedTimeout, coordinator.timeout)
	}
}

// TestGetQuorum 测试获取 Quorum 阈值
func TestGetQuorum(t *testing.T) {
	participants := []string{"node-1", "node-2", "node-3"}
	coordinator := NewQuorumCoordinator(participants, nil)

	quorum := coordinator.GetQuorum()
	expectedQuorum := 2 // (3 / 2) + 1 = 2

	if quorum != expectedQuorum {
		t.Errorf("Expected quorum %d, got %d", expectedQuorum, quorum)
	}
}

// TestGetParticipants 测试获取参与者列表
func TestGetParticipants(t *testing.T) {
	participants := []string{"node-1", "node-2", "node-3"}
	coordinator := NewQuorumCoordinator(participants, nil)

	gotParticipants := coordinator.GetParticipants()

	if len(gotParticipants) != len(participants) {
		t.Errorf("Expected %d participants, got %d", len(participants), len(gotParticipants))
	}

	for i, p := range participants {
		if gotParticipants[i] != p {
			t.Errorf("Participant %d = %s, want %s", i, gotParticipants[i], p)
		}
	}
}

// TestSetParticipants 测试设置参与者列表
func TestSetParticipants(t *testing.T) {
	coordinator := NewQuorumCoordinator([]string{"node-1"}, nil)

	// 设置新的参与者列表
	newParticipants := []string{"node-1", "node-2", "node-3", "node-4"}
	coordinator.SetParticipants(newParticipants)

	// 验证参与者列表已更新
	gotParticipants := coordinator.GetParticipants()
	if len(gotParticipants) != len(newParticipants) {
		t.Errorf("Expected %d participants, got %d", len(newParticipants), len(gotParticipants))
	}

	// 验证 Quorum 阈值已重新计算（4 个节点 → 3 个确认）
	newQuorum := coordinator.GetQuorum()
	expectedQuorum := 3
	if newQuorum != expectedQuorum {
		t.Errorf("Expected quorum %d, got %d", expectedQuorum, newQuorum)
	}
}

// TestQuorumResult_IsQuorumReached 测试 Quorum 结果判断
func TestQuorumResult_IsQuorumReached(t *testing.T) {
	tests := []struct {
		name     string
		result   QuorumResult
		expected bool
	}{
		{
			name:     "达到 Quorum",
			result:   QuorumResult{Success: true, AckCount: 3, TotalPeers: 5, Quorum: 3},
			expected: true,
		},
		{
			name:     "未达到 Quorum",
			result:   QuorumResult{Success: false, AckCount: 2, TotalPeers: 5, Quorum: 3},
			expected: false,
		},
		{
			name:     "刚好达到 Quorum",
			result:   QuorumResult{Success: true, AckCount: 2, TotalPeers: 3, Quorum: 2},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.IsQuorumReached(); got != tt.expected {
				t.Errorf("IsQuorumReached() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestQuorumResult_GetSuccessRate 测试成功率计算
func TestQuorumResult_GetSuccessRate(t *testing.T) {
	tests := []struct {
		name     string
		result   QuorumResult
		expected float64
	}{
		{
			name:     "100% 成功",
			result:   QuorumResult{AckCount: 5, TotalPeers: 5},
			expected: 1.0,
		},
		{
			name:     "60% 成功",
			result:   QuorumResult{AckCount: 3, TotalPeers: 5},
			expected: 0.6,
		},
		{
			name:     "0% 成功",
			result:   QuorumResult{AckCount: 0, TotalPeers: 5},
			expected: 0.0,
		},
		{
			name:     "空 peers",
			result:   QuorumResult{AckCount: 0, TotalPeers: 0},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate := tt.result.GetSuccessRate()
			if rate != tt.expected {
				t.Errorf("GetSuccessRate() = %f, want %f", rate, tt.expected)
			}
		})
	}
}

// TestBuildQuorumProposePayload 测试构建 Quorum Propose Payload
func TestBuildQuorumProposePayload(t *testing.T) {
	payload := BuildQuorumProposePayload("prop-001", kvstore.NamespaceRole, "role-001", []byte(`{"status": "active"}`))

	if payload["phase"] != "propose" {
		t.Errorf("Expected phase 'propose', got '%s'", payload["phase"])
	}
	if payload["proposal_id"] != "prop-001" {
		t.Errorf("Expected proposal_id 'prop-001', got '%s'", payload["proposal_id"])
	}
	if payload["namespace"] != kvstore.NamespaceRole {
		t.Errorf("Expected namespace '%s', got '%s'", kvstore.NamespaceRole, payload["namespace"])
	}
	if payload["key"] != "role-001" {
		t.Errorf("Expected key 'role-001', got '%s'", payload["key"])
	}
}

// TestBuildQuorumVotePayload 测试构建 Quorum Vote Payload
func TestBuildQuorumVotePayload(t *testing.T) {
	payload := BuildQuorumVotePayload("prop-001", "node-2", true)

	if payload["phase"] != "vote" {
		t.Errorf("Expected phase 'vote', got '%s'", payload["phase"])
	}
	if payload["proposal_id"] != "prop-001" {
		t.Errorf("Expected proposal_id 'prop-001', got '%s'", payload["proposal_id"])
	}
	if payload["voter"] != "node-2" {
		t.Errorf("Expected voter 'node-2', got '%s'", payload["voter"])
	}
	if payload["decision"] != true {
		t.Errorf("Expected decision true, got %v", payload["decision"])
	}
}

// TestBuildQuorumDecidePayload 测试构建 Quorum Decide Payload
func TestBuildQuorumDecidePayload(t *testing.T) {
	payload := BuildQuorumDecidePayload("prop-001", true, 3)

	if payload["phase"] != "decide" {
		t.Errorf("Expected phase 'decide', got '%s'", payload["phase"])
	}
	if payload["proposal_id"] != "prop-001" {
		t.Errorf("Expected proposal_id 'prop-001', got '%s'", payload["proposal_id"])
	}
	if payload["decision"] != true {
		t.Errorf("Expected decision true, got %v", payload["decision"])
	}
	if payload["quorum"] != 3 {
		t.Errorf("Expected quorum 3, got %v", payload["quorum"])
	}
}

// BenchmarkCalculateQuorum 性能测试：Quorum 计算
func BenchmarkCalculateQuorum(b *testing.B) {
	for i := 0; i < b.N; i++ {
		calculateQuorum(100)
	}
}

// BenchmarkBuildQuorumProposePayload 性能测试：构建 Propose Payload
func BenchmarkBuildQuorumProposePayload(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildQuorumProposePayload("prop-001", kvstore.NamespaceRole, "role-001", []byte(`{"status": "active"}`))
	}
}
