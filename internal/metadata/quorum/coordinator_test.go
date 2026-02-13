package quorum

import (
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// TestCalculateQuorum 测试 Quorum 计算（包含边界情况）
func TestCalculateQuorum(t *testing.T) {
	tests := []struct {
		name   string
		n      int
		expect int
	}{
		// 边界情况
		{"负数", -1, 0},
		{"零", 0, 0},
		{"单个节点", 1, 1},
		{"两个节点", 2, 2},
		// 正常情况
		{"3个节点", 3, 2},
		{"4个节点", 4, 3},
		{"5个节点", 5, 3},
		{"6个节点", 6, 4},
		{"7个节点", 7, 4},
		{"10个节点", 10, 6},
		{"100个节点", 100, 51},
		{"1000个节点", 1000, 501},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calculateQuorum(tt.n); got != tt.expect {
				t.Errorf("calculateQuorum(%d) = %d, want %d", tt.n, got, tt.expect)
			}
		})
	}
}

// TestNewQuorumCoordinator 测试创建 Quorum 协调器
func TestNewQuorumCoordinator(t *testing.T) {
	t.Run("基本创建", func(t *testing.T) {
		participants := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
		coordinator := NewQuorumCoordinator(participants, nil)

		if coordinator == nil {
			t.Fatal("NewQuorumCoordinator returned nil")
		}
		if coordinator.quorum != 3 {
			t.Errorf("Expected quorum 3, got %d", coordinator.quorum)
		}
		if coordinator.timeout != 5*time.Second {
			t.Errorf("Expected timeout 5s, got %v", coordinator.timeout)
		}
	})

	t.Run("带选项创建", func(t *testing.T) {
		tests := []struct {
			name         string
			participants []string
			opts         *PutOptions
			expectQuorum int
			expectTime   time.Duration
		}{
			{
				name:         "nil选项使用默认值",
				participants: []string{"node-1", "node-2", "node-3"},
				opts:         nil,
				expectQuorum: 2,
				expectTime:   5 * time.Second,
			},
			{
				name:         "自定义超时",
				participants: []string{"node-1", "node-2", "node-3"},
				opts:         &PutOptions{Timeout: 10000},
				expectQuorum: 2,
				expectTime:   10 * time.Second,
			},
			{
				name:         "零超时使用默认值",
				participants: []string{"node-1", "node-2"},
				opts:         &PutOptions{Timeout: 0},
				expectQuorum: 2,
				expectTime:   5 * time.Second,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				coordinator := NewQuorumCoordinatorWithOptions(tt.participants, nil, tt.opts)

				if coordinator == nil {
					t.Fatal("NewQuorumCoordinatorWithOptions returned nil")
				}
				if coordinator.quorum != tt.expectQuorum {
					t.Errorf("Expected quorum %d, got %d", tt.expectQuorum, coordinator.quorum)
				}
				if coordinator.timeout != tt.expectTime {
					t.Errorf("Expected timeout %v, got %v", tt.expectTime, coordinator.timeout)
				}
			})
		}
	})
}

// TestQuorumCoordinator_Participants 测试参与者管理
func TestQuorumCoordinator_Participants(t *testing.T) {
	t.Run("获取参与者", func(t *testing.T) {
		participants := []string{"node-1", "node-2", "node-3"}
		coordinator := NewQuorumCoordinator(participants, nil)

		got := coordinator.GetParticipants()
		if len(got) != len(participants) {
			t.Errorf("Expected %d participants, got %d", len(participants), len(got))
			return
		}
		for i, p := range participants {
			if got[i] != p {
				t.Errorf("Participant %d = %s, want %s", i, got[i], p)
			}
		}
	})

	t.Run("设置参与者", func(t *testing.T) {
		coordinator := NewQuorumCoordinator([]string{"node-1"}, nil)
		newParticipants := []string{"node-1", "node-2", "node-3", "node-4"}

		coordinator.SetParticipants(newParticipants)

		got := coordinator.GetParticipants()
		if len(got) != len(newParticipants) {
			t.Errorf("Expected %d participants, got %d", len(newParticipants), len(got))
		}
		if coordinator.GetQuorum() != 3 {
			t.Errorf("Expected quorum 3, got %d", coordinator.GetQuorum())
		}
	})

	// P2-8: 负面测试 - 空参与者和 nil 参与者
	t.Run("空参与者列表", func(t *testing.T) {
		coordinator := NewQuorumCoordinator([]string{}, nil)
		if coordinator.GetQuorum() != 0 {
			t.Errorf("Expected quorum 0 for empty participants, got %d", coordinator.GetQuorum())
		}
		if len(coordinator.GetParticipants()) != 0 {
			t.Errorf("Expected 0 participants, got %d", len(coordinator.GetParticipants()))
		}
	})

	t.Run("nil参与者列表", func(t *testing.T) {
		coordinator := NewQuorumCoordinator(nil, nil)
		if coordinator.GetQuorum() != 0 {
			t.Errorf("Expected quorum 0 for nil participants, got %d", coordinator.GetQuorum())
		}
		if coordinator.GetParticipants() != nil && len(coordinator.GetParticipants()) != 0 {
			t.Errorf("Expected nil or empty participants, got %v", coordinator.GetParticipants())
		}
	})

	t.Run("设置空参与者", func(t *testing.T) {
		coordinator := NewQuorumCoordinator([]string{"node-1"}, nil)
		coordinator.SetParticipants([]string{})
		if coordinator.GetQuorum() != 0 {
			t.Errorf("Expected quorum 0 after setting empty participants, got %d", coordinator.GetQuorum())
		}
	})
}

// TestQuorumCoordinator_Timeout 测试超时配置
func TestQuorumCoordinator_Timeout(t *testing.T) {
	coordinator := NewQuorumCoordinator([]string{"node-1"}, nil)

	t.Run("获取默认超时", func(t *testing.T) {
		if coordinator.GetTimeout() != 5*time.Second {
			t.Errorf("Expected timeout 5s, got %v", coordinator.GetTimeout())
		}
	})

	t.Run("设置超时", func(t *testing.T) {
		newTimeout := 10 * time.Second
		coordinator.SetTimeout(newTimeout)
		if coordinator.GetTimeout() != newTimeout {
			t.Errorf("Expected timeout %v, got %v", newTimeout, coordinator.GetTimeout())
		}
	})
}

// TestQuorumResult_Methods 测试 QuorumResult 的方法
func TestQuorumResult_Methods(t *testing.T) {
	t.Run("IsQuorumReached", func(t *testing.T) {
		tests := []struct {
			name     string
			result   QuorumResult
			expected bool
		}{
			{"达到Quorum", QuorumResult{Success: true, AckCount: 3, TotalPeers: 5, Quorum: 3}, true},
			{"未达到Quorum", QuorumResult{Success: false, AckCount: 2, TotalPeers: 5, Quorum: 3}, false},
			{"刚好达到Quorum", QuorumResult{Success: true, AckCount: 2, TotalPeers: 3, Quorum: 2}, true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := tt.result.IsQuorumReached(); got != tt.expected {
					t.Errorf("IsQuorumReached() = %v, want %v", got, tt.expected)
				}
			})
		}
	})

	t.Run("GetSuccessRate", func(t *testing.T) {
		tests := []struct {
			name     string
			result   QuorumResult
			expected float64
		}{
			{"100%成功", QuorumResult{AckCount: 5, TotalPeers: 5}, 1.0},
			{"60%成功", QuorumResult{AckCount: 3, TotalPeers: 5}, 0.6},
			{"0%成功", QuorumResult{AckCount: 0, TotalPeers: 5}, 0.0},
			{"空peers", QuorumResult{AckCount: 0, TotalPeers: 0}, 0.0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if rate := tt.result.GetSuccessRate(); rate != tt.expected {
					t.Errorf("GetSuccessRate() = %f, want %f", rate, tt.expected)
				}
			})
		}
	})
}

// TestBuildQuorumPayloads 测试构建 Quorum Payload
func TestBuildQuorumPayloads(t *testing.T) {
	t.Run("ProposePayload", func(t *testing.T) {
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
	})

	t.Run("VotePayload", func(t *testing.T) {
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
	})

	t.Run("DecidePayload", func(t *testing.T) {
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
	})
}

// TestDefaultPutOptions 测试默认写入选项
func TestDefaultPutOptions(t *testing.T) {
	opts := DefaultPutOptions()

	if opts == nil {
		t.Fatal("DefaultPutOptions returned nil")
	}
	if opts.Timeout != 5000 {
		t.Errorf("Expected timeout 5000, got %d", opts.Timeout)
	}
	if opts.SkipMerkleUpdate {
		t.Errorf("Expected SkipMerkleUpdate false, got %v", opts.SkipMerkleUpdate)
	}
	if opts.Async {
		t.Errorf("Expected Async false, got %v", opts.Async)
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
