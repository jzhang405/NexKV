package id

import (
	"strings"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRequestIDGenerator(t *testing.T) {
	generator := NewRequestIDGenerator("node-001")

	assert.NotNil(t, generator)
	assert.Equal(t, "node-001", generator.NodeID())
}

func TestRequestIDGenerator_Next(t *testing.T) {
	// 使用包含连字符的 nodeID 以匹配 Validate 的要求（4 部分）
	generator := NewRequestIDGenerator("node-001")

	// Generate multiple IDs
	ids := make([]model.RequestID, 10)
	for i := 0; i < 10; i++ {
		ids[i] = generator.Next()
	}

	// Verify all IDs are valid
	for i, id := range ids {
		t.Run("validating ID", func(t *testing.T) {
			assert.NoError(t, id.Validate())
			// NodeID 是整个 nodeID 部分
			assert.Contains(t, id.String(), "node-001")
			assert.True(t, id.Timestamp() > 0)
		})

		// Verify uniqueness
		for j := i + 1; j < len(ids); j++ {
			assert.NotEqual(t, id, ids[j], "IDs should be unique")
		}
	}
}

func TestRequestIDGenerator_Next_Sequential(t *testing.T) {
	// 使用包含连字符的 nodeID
	generator := NewRequestIDGenerator("node-001")

	// Generate IDs rapidly
	id1 := generator.Next()
	id2 := generator.Next()

	// Both should be valid
	require.NoError(t, id1.Validate())
	require.NoError(t, id2.Validate())

	// They should be different
	assert.NotEqual(t, id1, id2)

	// They should have the same node ID
	assert.Equal(t, id1.NodeID(), id2.NodeID())

	// Later IDs should be greater or equal
	assert.True(t, id2.Timestamp() >= id1.Timestamp())
}

func TestRequestIDGenerator_NodeID(t *testing.T) {
	tests := []struct {
		name     string
		nodeID   string
		expected string
	}{
		{
			name:     "simple node ID",
			nodeID:   "node001",
			expected: "node001",
		},
		{
			name:     "complex node ID",
			nodeID:   "my-node-001",
			expected: "my-node-001",
		},
		{
			name:     "empty node ID",
			nodeID:   "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generator := NewRequestIDGenerator(tt.nodeID)
			assert.Equal(t, tt.expected, generator.NodeID())
		})
	}
}

func TestRequestIDGenerator_Concurrent(t *testing.T) {
	// 使用包含连字符的 nodeID
	generator := NewRequestIDGenerator("node-001")
	const numIDs = 1000

	// Generate IDs concurrently
	idChan := make(chan model.RequestID, numIDs)
	for i := 0; i < numIDs; i++ {
		go func() {
			idChan <- generator.Next()
		}()
	}

	// Collect all IDs
	ids := make(map[model.RequestID]bool)
	for i := 0; i < numIDs; i++ {
		id := <-idChan
		err := id.Validate()
		if err != nil {
			t.Logf("Invalid ID generated: %s, error: %v", id, err)
		}
		assert.NoError(t, err, "Generated ID should be valid")

		// Check for uniqueness
		if ids[id] {
			t.Logf("Duplicate ID: %s", id)
		}
		assert.False(t, ids[id], "ID should be unique: %s", id)
		ids[id] = true
	}

	assert.Equal(t, numIDs, len(ids), "All IDs should be unique")
}

func TestRequestID_Format(t *testing.T) {
	generator := NewRequestIDGenerator("test-node")
	id := generator.Next()

	// Verify format: {NodeID}-{Timestamp:08x}-{Sequence:04x}
	parts := strings.Split(id.String(), "-")
	assert.GreaterOrEqual(t, len(parts), 4, "ID should have at least 4 parts")

	// Last two parts should be timestamp and sequence
	timestamp := parts[len(parts)-2]
	sequence := parts[len(parts)-1]

	// Timestamp should be 8 hex characters
	assert.Len(t, timestamp, 8, "Timestamp should be 8 characters")

	// Sequence should be 4 hex characters
	assert.Len(t, sequence, 4, "Sequence should be 4 characters")

	// Verify they are valid hex
	_, err := parseIntHex(timestamp)
	assert.NoError(t, err, "Timestamp should be valid hex")

	_, err = parseIntHex(sequence)
	assert.NoError(t, err, "Sequence should be valid hex")
}

func TestRequestIDGenerator_SequenceOverflow(t *testing.T) {
	// 使用包含连字符的 nodeID
	generator := NewRequestIDGenerator("test-001")

	// Generate many IDs to potentially trigger overflow handling
	for i := 0; i < 100; i++ {
		id := generator.Next()
		err := id.Validate()
		if err != nil {
			t.Logf("Invalid ID at iteration %d: %s, error: %v", i, id, err)
		}
		assert.NoError(t, err)
		seq := id.Sequence()
		assert.LessOrEqual(t, seq, uint32(0xFFFF), "Sequence should not exceed max")
	}
}

// Helper function to parse hex string
func parseIntHex(s string) (int64, error) {
	var result int64
	for _, c := range s {
		result <<= 4
		switch {
		case c >= '0' && c <= '9':
			result |= int64(c - '0')
		case c >= 'a' && c <= 'f':
			result |= int64(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			result |= int64(c - 'A' + 10)
		default:
			return 0, assert.AnError
		}
	}
	return result, nil
}

// ==========================================
// 监控指标测试 (P1-04)
// ==========================================

// TestGetClockBackoffCount 测试时钟回退监控指标
func TestGetClockBackoffCount(t *testing.T) {
	// 初始值应该为 0
	initialCount := GetClockBackoffCount()
	assert.GreaterOrEqual(t, initialCount, int64(0), "Initial clock backoff count should be >= 0")
}

// TestGetSeqOverflowCount 测试序列号溢出监控指标
func TestGetSeqOverflowCount(t *testing.T) {
	// 初始值应该为 0
	initialCount := GetSeqOverflowCount()
	assert.GreaterOrEqual(t, initialCount, int64(0), "Initial sequence overflow count should be >= 0")
}

// TestRequestIDGenerator_MonitoringMetrics 测试生成器运行时监控指标
func TestRequestIDGenerator_MonitoringMetrics(t *testing.T) {
	generator := NewRequestIDGenerator("monitor-001")

	// 生成一些 ID 以确保生成器正常工作
	for i := 0; i < 10; i++ {
		id := generator.Next()
		assert.NoError(t, id.Validate())
	}

	// 监控指标应该可以正常读取
	backoffCount := GetClockBackoffCount()
	overflowCount := GetSeqOverflowCount()

	t.Logf("Clock backoff count: %d", backoffCount)
	t.Logf("Sequence overflow count: %d", overflowCount)
}

// TestRequestIDGenerator_HighThroughput 高吞吐量测试
func TestRequestIDGenerator_HighThroughput(t *testing.T) {
	generator := NewRequestIDGenerator("high-throughput-001")
	const numIDs = 10000

	var wg sync.WaitGroup
	idChan := make(chan model.RequestID, numIDs)

	// 并发生成 ID
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numIDs/10; j++ {
				idChan <- generator.Next()
			}
		}()
	}

	wg.Wait()
	close(idChan)

	// 验证所有 ID 唯一
	ids := make(map[model.RequestID]struct{})
	for id := range idChan {
		if _, exists := ids[id]; exists {
			t.Errorf("Duplicate ID found: %s", id)
		}
		ids[id] = struct{}{}
	}

	assert.Equal(t, numIDs, len(ids), "All IDs should be unique")

	// 报告监控指标
	t.Logf("Generated %d unique IDs", len(ids))
	t.Logf("Clock backoff count: %d", GetClockBackoffCount())
	t.Logf("Sequence overflow count: %d", GetSeqOverflowCount())
}
