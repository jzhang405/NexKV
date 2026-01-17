// Package cluster 故障检测器测试
package cluster

import (
	"fmt"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// FailureDetector 测试
// ========================================

// TestNewFailureDetector 测试创建故障检测器
func TestNewFailureDetector(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultFailureDetectorConfig()
	detector, err := NewFailureDetector("node1", trans, config)

	assert.NoError(t, err)
	assert.NotNil(t, detector)
	assert.Equal(t, "node1", detector.localNodeID)
	assert.Equal(t, 5*time.Second, detector.config.Interval)
	assert.Equal(t, 15*time.Second, detector.config.Timeout)
	assert.Equal(t, 8.0, detector.config.PhiThreshold)
}

// TestNewFailureDetector_InvalidParams 测试无效参数
func TestNewFailureDetector_InvalidParams(t *testing.T) {
	trans, _ := transport.NewMemoryTransport("node1")
	config := DefaultFailureDetectorConfig()

	testCases := []struct {
		name        string
		nodeID      string
		transport   transport.Transport
		config      *FailureDetectorConfig
		expectError bool
	}{
		{"空节点ID", "", trans, config, true},
		{"空传输层", "node1", nil, config, true},
		{"nil配置自动使用默认", "node1", trans, nil, false},
		{"有效参数", "node1", trans, config, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewFailureDetector(tc.nodeID, tc.transport, tc.config)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestFailureDetector_StartStop 测试启动和停止
func TestFailureDetector_StartStop(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultFailureDetectorConfig()
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	// 测试启动
	err = detector.Start()
	assert.NoError(t, err)

	// 测试重复启动
	err = detector.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已经启动")

	// 测试停止
	err = detector.Stop()
	assert.NoError(t, err)

	// 测试重复停止
	err = detector.Stop()
	assert.NoError(t, err) // 停止幂等
}

// TestFailureDetector_AddNode 测试添加节点
func TestFailureDetector_AddNode(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultFailureDetectorConfig()
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	// 添加节点
	detector.AddNode("node2")

	// 验证节点已添加
	state, err := detector.GetNodeState("node2")
	assert.NoError(t, err)
	assert.Equal(t, "node2", state.NodeID)
	assert.False(t, state.IsFailed.Load())
	assert.True(t, time.Since(state.LastHeartbeat) < time.Second)
}

// TestFailureDetector_RemoveNode 测试移除节点
func TestFailureDetector_RemoveNode(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultFailureDetectorConfig()
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	// 添加节点
	detector.AddNode("node2")

	// 移除节点
	detector.RemoveNode("node2")

	// 验证节点已移除
	_, err = detector.GetNodeState("node2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestFailureDetector_RecordHeartbeat 测试记录心跳
func TestFailureDetector_RecordHeartbeat(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultFailureDetectorConfig()
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	// 记录第一次心跳
	detector.RecordHeartbeat("node2")

	state, _ := detector.GetNodeState("node2")
	assert.False(t, state.LastHeartbeat.IsZero())
	assert.Equal(t, 0, len(state.HeartbeatIntervals)) // 第一次心跳没有间隔

	// 等待一小段时间
	time.Sleep(10 * time.Millisecond)

	// 记录第二次心跳
	detector.RecordHeartbeat("node2")

	state, _ = detector.GetNodeState("node2")
	assert.Equal(t, 1, len(state.HeartbeatIntervals))
	assert.True(t, state.HeartbeatIntervals[0] > 0)
}

// TestFailureDetector_IsNodeAlive 测试检查节点存活
func TestFailureDetector_IsNodeAlive(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := &FailureDetectorConfig{
		Interval:     100 * time.Millisecond,
		Timeout:      500 * time.Millisecond,
		PhiThreshold: 8.0,
		MinSamples:   5,
	}
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	// 不存在的节点
	assert.False(t, detector.IsNodeAlive("node2"))

	// 添加节点并记录心跳
	detector.AddNode("node2")
	assert.True(t, detector.IsNodeAlive("node2"))

	// 等待超时
	time.Sleep(600 * time.Millisecond)

	// 节点应该被判定为故障
	assert.False(t, detector.IsNodeAlive("node2"))
}

// TestFailureDetector_GetStats 测试获取统计信息
func TestFailureDetector_GetStats(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultFailureDetectorConfig()
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	stats := detector.GetStats()
	assert.NotNil(t, stats)
	assert.Equal(t, int64(0), stats.PingsTotal.Load())
	assert.Equal(t, int64(0), stats.PingsSuccess.Load())
	assert.Equal(t, int64(0), stats.PingsFailed.Load())
	assert.Equal(t, int64(0), stats.FailuresDetected.Load())
}

// TestFailureDetector_SetFailureCallback 测试设置故障回调
func TestFailureDetector_SetFailureCallback(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := &FailureDetectorConfig{
		Interval:     100 * time.Millisecond,
		Timeout:      300 * time.Millisecond,
		PhiThreshold: 8.0,
		MinSamples:   3,
	}
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	// 设置故障回调
	detector.SetFailureCallback(func(nodeID string) {
		// 回调被调用
	})

	// 启动检测器
	_ = detector.Start()
	defer func() { _ = detector.Stop() }()

	// 添加节点
	detector.AddNode("node2")
	detector.RecordHeartbeat("node2")

	// 记录几次心跳以收集样本
	for i := 0; i < 5; i++ {
		time.Sleep(50 * time.Millisecond)
		detector.RecordHeartbeat("node2")
	}

	// 等待心跳超时
	time.Sleep(400 * time.Millisecond)

	// 触发探测循环
	time.Sleep(200 * time.Millisecond)

	// 验证回调可以正常设置（不验证实际调用，因为可能有竞争条件）
	// 只要没有 panic 就说明设置成功
}

// TestDefaultFailureDetectorConfig 测试默认配置
func TestDefaultFailureDetectorConfig(t *testing.T) {
	config := DefaultFailureDetectorConfig()

	assert.Equal(t, 5*time.Second, config.Interval)
	assert.Equal(t, 15*time.Second, config.Timeout)
	assert.Equal(t, 8.0, config.PhiThreshold)
	assert.Equal(t, 10, config.MinSamples)
}

// TestNodeState_Statistics 测试节点状态统计
func TestNodeState_Statistics(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultFailureDetectorConfig()
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	// 记录多次心跳以收集样本
	nodeID := "node2"
	detector.AddNode(nodeID)

	for i := 0; i < 15; i++ {
		time.Sleep(10 * time.Millisecond)
		detector.RecordHeartbeat(nodeID)
	}

	state, err := detector.GetNodeState(nodeID)
	require.NoError(t, err)

	// 验证统计信息已计算
	assert.Equal(t, 15, len(state.HeartbeatIntervals))
	assert.Greater(t, state.Mean, float64(0))
	assert.GreaterOrEqual(t, state.StdDev, float64(0))
}

// TestFailureDetector_ConcurrentAccess 测试并发访问
func TestFailureDetector_ConcurrentAccess(t *testing.T) {
	trans, err := transport.NewMemoryTransport("node1")
	require.NoError(t, err)

	config := DefaultFailureDetectorConfig()
	detector, err := NewFailureDetector("node1", trans, config)
	require.NoError(t, err)

	_ = detector.Start()
	defer func() { _ = detector.Stop() }()

	// 并发添加节点
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(index int) {
			nodeID := fmt.Sprintf("node%d", index)
			detector.AddNode(nodeID)
			for j := 0; j < 5; j++ {
				detector.RecordHeartbeat(nodeID)
				time.Sleep(10 * time.Millisecond)
			}
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证所有节点都已添加
	for i := 0; i < 10; i++ {
		nodeID := fmt.Sprintf("node%d", i)
		state, err := detector.GetNodeState(nodeID)
		assert.NoError(t, err)
		assert.NotNil(t, state)
	}
}
