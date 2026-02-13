// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件包含时间戳生成器的单元测试
package porcupine

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMonotonicTimestamp_NeverDecreases 测试单调时间戳永不会回退
func TestMonotonicTimestamp_NeverDecreases(t *testing.T) {
	ts := NewMonotonicTimestamp()
	last := ts.Now()

	for i := 0; i < 100000; i++ {
		now := ts.Now()
		require.True(t, now > last, "Timestamp must be monotonically increasing, got now=%d, last=%d", now, last)
		last = now
	}
}

// TestMonotonicTimestamp_Concurrent 测试并发场景下单调时间戳的正确性
func TestMonotonicTimestamp_Concurrent(t *testing.T) {
	ts := NewMonotonicTimestamp()
	timestamps := make([]int64, 10000)
	var wg sync.WaitGroup

	// 10 个并发 goroutine，每个生成 1000 个时间戳
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				idx := goroutineID*1000 + i
				timestamps[idx] = ts.Now()
			}
		}(g)
	}

	wg.Wait()

	// 验证所有时间戳都是唯一的
	seen := make(map[int64]bool)
	for _, ts := range timestamps {
		require.False(t, seen[ts], "Duplicate timestamp detected: %d", ts)
		seen[ts] = true
	}
}

// TestLogicalTimestamp_ClientIsolation 测试不同客户端的逻辑时间戳不会冲突
func TestLogicalTimestamp_ClientIsolation(t *testing.T) {
	ts1 := NewLogicalTimestamp(1)
	ts2 := NewLogicalTimestamp(2)

	// 不同客户端的时间戳不会冲突
	ts1Val := ts1.Now()
	ts2Val := ts2.Now()
	require.NotEqual(t, ts1Val, ts2Val, "Different clients should generate different timestamps")

	// 同一客户端的时间戳单调递增
	ts1Prev := ts1Val
	for i := 0; i < 100; i++ {
		ts1Curr := ts1.Now()
		require.True(t, ts1Curr > ts1Prev, "Same client timestamps should be monotonically increasing")
		ts1Prev = ts1Curr
	}
}

// TestLogicalTimestamp_ClientIDInTimestamp 测试客户端 ID 正确编码到时间戳中
func TestLogicalTimestamp_ClientIDInTimestamp(t *testing.T) {
	clientID := 5
	ts := NewLogicalTimestamp(clientID)

	// 生成多个时间戳，验证 clientID 正确编码
	for i := 0; i < 10; i++ {
		val := ts.Now()
		// 提取 clientID（高 16 位）
		extractedClientID := int(val >> 48)
		require.Equal(t, clientID, extractedClientID, "Client ID should be encoded in high bits")
	}
}

// TestLogicalTimestamp_SequenceIncreasing 测试同一客户端的序列号递增
func TestLogicalTimestamp_SequenceIncreasing(t *testing.T) {
	ts := NewLogicalTimestamp(1)

	// 连续生成时间戳，序列号应该递增
	lastSeq := int64(0)
	for i := 0; i < 100; i++ {
		val := ts.Now()
		// 提取序列号（低 48 位）
		seq := val & 0xFFFFFFFFFFFF
		require.True(t, seq > lastSeq, "Sequence should be monotonically increasing")
		lastSeq = seq
	}
}

// TestNewTimestampGenerator_SingleNode 测试单节点场景使用单调时间戳
func TestNewTimestampGenerator_SingleNode(t *testing.T) {
	gen := NewTimestampGenerator("node-1", 1)
	require.NotNil(t, gen, "Generator should not be nil")

	// 验证时间戳单调递增
	last := gen.Now()
	for i := 0; i < 100; i++ {
		now := gen.Now()
		require.True(t, now > last, "Timestamp should be monotonically increasing")
		last = now
	}
}

// TestNewTimestampGenerator_MultiNode 测试多节点场景使用逻辑时间戳
func TestNewTimestampGenerator_MultiNode(t *testing.T) {
	gen1 := NewTimestampGenerator("node-1", 3)
	gen2 := NewTimestampGenerator("node-2", 3)
	gen3 := NewTimestampGenerator("node-3", 3)

	// 不同节点生成的时间戳应该不同
	ts1 := gen1.Now()
	ts2 := gen2.Now()
	ts3 := gen3.Now()

	// 所有时间戳应该唯一
	require.NotEqual(t, ts1, ts2, "Different nodes should generate different timestamps")
	require.NotEqual(t, ts2, ts3, "Different nodes should generate different timestamps")
	require.NotEqual(t, ts1, ts3, "Different nodes should generate different timestamps")
}

// TestTimestampGenerator_Interface 测试 TimestampGenerator 接口实现
func TestTimestampGenerator_Interface(t *testing.T) {
	// 确保两种实现都满足接口
	var _ TimestampGenerator = NewMonotonicTimestamp()
	var _ TimestampGenerator = NewLogicalTimestamp(1)
	var _ TimestampGenerator = NewTimestampGenerator("node-1", 1)
	var _ TimestampGenerator = NewTimestampGenerator("node-1", 3)
}
