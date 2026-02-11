package consistency

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// TestConsistencyLevelString 测试一致性级别字符串表示
func TestConsistencyLevelString(t *testing.T) {
	tests := []struct {
		level    ConsistencyLevel
		expected string
	}{
		{ConsistencyStrong2PC, "2PC-Strong"},
		{ConsistencyEnhancedEventual, "Quorum-EnhancedEventual"},
		{ConsistencyEventual, "Gossip-Eventual"},
		{ConsistencyLevel(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.level.String(); got != tt.expected {
				t.Errorf("String() = %s, want %s", got, tt.expected)
			}
		})
	}
}

// TestConsistencyLevelACKRequirement 测试 ACK 要求描述
func TestConsistencyLevelACKRequirement(t *testing.T) {
	tests := []struct {
		level    ConsistencyLevel
		expected string
	}{
		{ConsistencyStrong2PC, "ACK 全部 (need = n)"},
		{ConsistencyEnhancedEventual, "ACK 大部分 (need = ⌊n/2⌋ + 1)"},
		{ConsistencyEventual, "无 ACK (need = 0)"},
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			if got := tt.level.ACKRequirement(); got != tt.expected {
				t.Errorf("ACKRequirement() = %s, want %s", got, tt.expected)
			}
		})
	}
}

// TestDefaultConsistencyMapping 测试默认一致性级别映射
func TestDefaultConsistencyMapping(t *testing.T) {
	tests := []struct {
		namespace     string
		expectedLevel ConsistencyLevel
		expectedDesc  string
	}{
		{kvstore.NamespaceCluster, ConsistencyStrong2PC, "集群配置应该是强一致（2PC）"},
		{kvstore.NamespaceShard, ConsistencyStrong2PC, "分片信息应该是强一致（2PC）"},
		{kvstore.NamespaceNode, ConsistencyEventual, "节点信息应该是最终一致（Gossip）"},
		{kvstore.NamespaceRole, ConsistencyEnhancedEventual, "角色信息应该是增强最终一致（Quorum）"}, // ⚠️ 从 Gossip 升级
		{kvstore.NamespaceStatic, ConsistencyStrong2PC, "静态配置应该是强一致（2PC）"},
		{kvstore.NamespaceTopo, ConsistencyEventual, "拓扑关系应该是最终一致（Gossip）"},
		{kvstore.NamespaceDynamic, ConsistencyEventual, "动态状态应该是最终一致（Gossip）"},
		{kvstore.NamespaceOp, ConsistencyEventual, "操作记录应该是最终一致（Gossip）"},
		{kvstore.NamespaceVersion, ConsistencyStrong2PC, "版本控制应该是强一致（2PC）"},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			level, ok := DefaultConsistencyMapping[tt.namespace]
			if !ok {
				t.Errorf("Namespace %s not found in DefaultConsistencyMapping", tt.namespace)
				return
			}
			if level != tt.expectedLevel {
				t.Errorf("%s: got level %d, want %d", tt.expectedDesc, level, tt.expectedLevel)
			}
		})
	}
}

// TestGetDefaultConsistencyLevel 测试获取默认一致性级别
func TestGetDefaultConsistencyLevel(t *testing.T) {
	tests := []struct {
		namespace     string
		expectedLevel ConsistencyLevel
	}{
		{kvstore.NamespaceCluster, ConsistencyStrong2PC},
		{kvstore.NamespaceShard, ConsistencyStrong2PC},
		{kvstore.NamespaceNode, ConsistencyEventual},
		{kvstore.NamespaceRole, ConsistencyEnhancedEventual},
		{kvstore.NamespaceStatic, ConsistencyStrong2PC},
		{kvstore.NamespaceTopo, ConsistencyEventual},
		{kvstore.NamespaceDynamic, ConsistencyEventual},
		{kvstore.NamespaceOp, ConsistencyEventual},
		{kvstore.NamespaceVersion, ConsistencyStrong2PC},
		// 不存在的 Namespace 应该返回默认值（最终一致）
		{"unknown:namespace:", ConsistencyEventual},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			level := GetDefaultConsistencyLevel(tt.namespace)
			if level != tt.expectedLevel {
				t.Errorf("GetDefaultConsistencyLevel() = %d, want %d", level, tt.expectedLevel)
			}
		})
	}
}

// TestCalculateQuorum 测试 Quorum 数量计算
func TestCalculateQuorum(t *testing.T) {
	tests := []struct {
		n      int
		expect int
	}{
		{0, 0},     // 0 节点 → 0
		{1, 1},     // 1 节点 → 1
		{2, 2},     // 2 节点 → 2
		{3, 2},     // 3 节点 → 2
		{4, 3},     // 4 节点 → 3
		{5, 3},     // 5 节点 → 3
		{6, 4},     // 6 节点 → 4
		{7, 4},     // 7 节点 → 4
		{10, 6},    // 10 节点 → 6
		{100, 51},  // 100 节点 → 51
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := CalculateQuorum(tt.n)
			if got != tt.expect {
				t.Errorf("CalculateQuorum(%d) = %d, want %d", tt.n, got, tt.expect)
			}
		})
	}
}

// TestCalculateQuorumParticipants 测试选择 Quorum 参与者
func TestCalculateQuorumParticipants(t *testing.T) {
	participants := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}

	// 5 个节点，Quorum 应该是 3 个
	quorum := CalculateQuorumParticipants(participants)

	if len(quorum) != 3 {
		t.Errorf("Expected 3 quorum participants, got %d", len(quorum))
	}

	// 验证返回的是前 3 个参与者
	expected := []string{"node-1", "node-2", "node-3"}
	for i, p := range quorum {
		if p != expected[i] {
			t.Errorf("Participant %d = %s, want %s", i, p, expected[i])
		}
	}
}

// TestCalculateQuorumParticipantsLessThanQuorum 测试参与者数量少于 Quorum
func TestCalculateQuorumParticipantsLessThanQuorum(t *testing.T) {
	participants := []string{"node-1", "node-2"}

	// 2 个节点，Quorum 应该是 2 个（全部）
	quorum := CalculateQuorumParticipants(participants)

	if len(quorum) != 2 {
		t.Errorf("Expected 2 quorum participants (all), got %d", len(quorum))
	}

	// 验证返回的是所有参与者
	for i, p := range quorum {
		if p != participants[i] {
			t.Errorf("Participant %d = %s, want %s", i, p, participants[i])
		}
	}
}

// TestPutOptions 测试 PutOptions 默认值
func TestPutOptions(t *testing.T) {
	opts := &PutOptions{}

	if opts.Timeout != 0 {
		t.Errorf("Expected default Timeout 0, got %d", opts.Timeout)
	}
	if opts.Participants == nil {
		// nil 是有效的，表示自动选择
	} else if len(opts.Participants) != 0 {
		t.Errorf("Expected empty Participants, got %v", opts.Participants)
	}
	if opts.SkipMerkleUpdate {
		t.Error("Expected default SkipMerkleUpdate false")
	}
	if opts.Async {
		t.Error("Expected default Async false")
	}
}

// BenchmarkCalculateQuorum 性能测试：Quorum 计算
func BenchmarkCalculateQuorum(b *testing.B) {
	for i := 0; i < b.N; i++ {
		CalculateQuorum(100)
	}
}

// BenchmarkCalculateQuorumParticipants 性能测试：选择 Quorum 参与者
func BenchmarkCalculateQuorumParticipants(b *testing.B) {
	participants := make([]string, 100)
	for i := 0; i < 100; i++ {
		participants[i] = "node-" + string(rune(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CalculateQuorumParticipants(participants)
	}
}
