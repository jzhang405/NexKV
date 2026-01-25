// Package transport 维度化监控模块测试
package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// 创建和初始化测试
// ========================================

// TestNewDimensionalMonitor 测试创建监控器
func TestNewDimensionalMonitor(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	assert.NotNil(t, monitor)
	assert.NotNil(t, monitor.byMessageType)
	assert.NotNil(t, monitor.byNode)
	assert.NotNil(t, monitor.byErrorType)
	assert.NotNil(t, monitor.byProtocol)
	assert.NotNil(t, monitor.globalStats)
	assert.NotZero(t, monitor.startTime)
	assert.NotNil(t, monitor.stopCh)
}

// TestNewDimensionalMonitorWithContext 测试使用 context 创建监控器
func TestNewDimensionalMonitorWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor := NewDimensionalMonitorWithContext(ctx)
	defer monitor.Stop()

	assert.NotNil(t, monitor)
	assert.NotNil(t, monitor.ctx)
	assert.NotNil(t, monitor.cancel)
}

// TestDimensionalMonitor_Stop 测试停止监控器
func TestDimensionalMonitor_Stop(t *testing.T) {
	monitor := NewDimensionalMonitor()

	// 验证可以多次调用 Stop（幂等）
	monitor.Stop()
	monitor.Stop()
	monitor.Stop()

	// 不应该 panic
	assert.True(t, true)
}

// ========================================
// RecordMessage 测试
// ========================================

// TestRecordMessage_Success 测试记录成功消息
func TestRecordMessage_Success(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolTCP,
		"127.0.0.1:8080",
		1024,
		1000000, // 1ms latency
		true,
		nil,
	)

	// 验证全局统计
	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(1), globalStats.TotalMessages.Load())
	assert.Equal(t, uint64(1), globalStats.TotalSuccess.Load())
	assert.Equal(t, uint64(0), globalStats.TotalFailure.Load())
	assert.Equal(t, uint64(1024), globalStats.TotalBytes.Load())
	assert.Equal(t, uint64(1000000), globalStats.TotalLatency.Load())

	// 验证消息类型统计
	msgStats, exists := monitor.GetMessageTypeStats(types.MessageTypeGet)
	require.True(t, exists)
	assert.Equal(t, uint64(1), msgStats.Count.Load())
	assert.Equal(t, uint64(1), msgStats.SuccessCount.Load())
	assert.Equal(t, uint64(1024), msgStats.TotalSize.Load())

	// 验证节点统计
	nodeStats, exists := monitor.GetNodeStats("127.0.0.1:8080")
	require.True(t, exists)
	assert.Equal(t, uint64(1), nodeStats.MessageCount.Load())
	assert.Equal(t, uint64(1), nodeStats.SuccessCount.Load())

	// 验证协议统计
	protocolStats, exists := monitor.GetProtocolStats(types.ProtocolTCP)
	require.True(t, exists)
	assert.Equal(t, uint64(1), protocolStats.MessageCount.Load())
	assert.Equal(t, uint64(1), protocolStats.SuccessCount.Load())
}

// TestRecordMessage_Failure 测试记录失败消息
func TestRecordMessage_Failure(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 使用预定义的协议错误，包装以提供上下文
	testErr := fmt.Errorf("connect 127.0.0.1:8081 failed: %w", types.ErrTCPConnFailed)

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolUDP,
		"127.0.0.1:8081",
		512,
		500000, // 0.5ms latency
		false,
		testErr,
	)

	// 验证全局统计
	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(1), globalStats.TotalMessages.Load())
	assert.Equal(t, uint64(0), globalStats.TotalSuccess.Load())
	assert.Equal(t, uint64(1), globalStats.TotalFailure.Load())
	assert.Equal(t, uint64(512), globalStats.TotalBytes.Load())

	// 验证消息类型统计
	msgStats, exists := monitor.GetMessageTypeStats(types.MessageTypeGet)
	require.True(t, exists)
	assert.Equal(t, uint64(1), msgStats.Count.Load())
	assert.Equal(t, uint64(0), msgStats.SuccessCount.Load())
	assert.Equal(t, uint64(1), msgStats.FailureCount.Load())

	// 验证节点统计
	nodeStats, exists := monitor.GetNodeStats("127.0.0.1:8081")
	require.True(t, exists)
	assert.Equal(t, uint64(1), nodeStats.MessageCount.Load())
	assert.Equal(t, uint64(0), nodeStats.SuccessCount.Load())
	assert.Equal(t, uint64(1), nodeStats.FailureCount.Load())

	// 验证错误类型统计
	errorStats, exists := monitor.GetErrorTypeStats("protocol_error")
	require.True(t, exists)
	assert.Equal(t, uint64(1), errorStats.Count.Load())
}

// TestRecordMessage_MultipleMessages 测试记录多条消息
func TestRecordMessage_MultipleMessages(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录 10 条成功消息
	for i := 0; i < 10; i++ {
		monitor.RecordMessage(
			types.MessageTypeGet,
			types.ProtocolTCP,
			"127.0.0.1:8080",
			1024,
			1000000,
			true,
			nil,
		)
	}

	// 记录 3 条失败消息
	for i := 0; i < 3; i++ {
		monitor.RecordMessage(
			types.MessageTypePut,
			types.ProtocolUDP,
			"127.0.0.1:8081",
			512,
			500000,
			false,
			errors.New("test error"),
		)
	}

	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(13), globalStats.TotalMessages.Load())
	assert.Equal(t, uint64(10), globalStats.TotalSuccess.Load())
	assert.Equal(t, uint64(3), globalStats.TotalFailure.Load())
}

// TestRecordMessage_ZeroLatency 测试零延迟消息
func TestRecordMessage_ZeroLatency(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolTCP,
		"127.0.0.1:8080",
		1024,
		0, // 零延迟
		true,
		nil,
	)

	// 验证全局统计
	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(0), globalStats.TotalLatency.Load())

	// 验证消息类型统计
	msgStats, exists := monitor.GetMessageTypeStats(types.MessageTypeGet)
	require.True(t, exists)
	assert.Equal(t, uint64(0), msgStats.TotalLatency.Load())

	// 验证节点统计
	nodeStats, exists := monitor.GetNodeStats("127.0.0.1:8080")
	require.True(t, exists)
	assert.Equal(t, uint64(0), nodeStats.TotalLatency.Load())
}

// TestRecordMessage_NoError 测试无错误消息
func TestRecordMessage_NoError(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolTCP,
		"127.0.0.1:8080",
		1024,
		1000000,
		true,
		nil, // 无错误
	)

	// 验证没有错误类型统计
	allErrorStats := monitor.GetAllErrorTypeStats()
	assert.Empty(t, allErrorStats)

	// 验证节点没有错误计数
	nodeStats, exists := monitor.GetNodeStats("127.0.0.1:8080")
	require.True(t, exists)
	assert.Empty(t, nodeStats.ErrorCounts)
}

// ========================================
// 消息类型统计测试
// ========================================

// TestGetMessageTypeStats_Exists 测试获取存在的消息类型统计
func TestGetMessageTypeStats_Exists(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolTCP,
		"127.0.0.1:8080",
		1024,
		1000000,
		true,
		nil,
	)

	stats, exists := monitor.GetMessageTypeStats(types.MessageTypeGet)
	assert.True(t, exists)
	assert.NotNil(t, stats)
	assert.Equal(t, uint64(1), stats.Count.Load())
}

// TestGetMessageTypeStats_NotExists 测试获取不存在的消息类型统计
func TestGetMessageTypeStats_NotExists(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	stats, exists := monitor.GetMessageTypeStats(types.MessageTypeGet)
	assert.False(t, exists)
	assert.Nil(t, stats)
}

// TestGetAllMessageTypeStats 测试获取所有消息类型统计
func TestGetAllMessageTypeStats(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录不同类型的消息
	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, true, nil)
	monitor.RecordMessage(types.MessageTypePut, types.ProtocolUDP, "127.0.0.1:8081", 512, 500000, true, nil)
	monitor.RecordMessage(types.MessageTypeDelete, types.ProtocolTCP, "127.0.0.1:8082", 256, 200000, false, errors.New("error"))

	allStats := monitor.GetAllMessageTypeStats()
	assert.Len(t, allStats, 3)

	// 验证每种类型的统计
	assert.Contains(t, allStats, types.MessageTypeGet)
	assert.Contains(t, allStats, types.MessageTypePut)
	assert.Contains(t, allStats, types.MessageTypeDelete)

	// 验证统计值正确
	getStats := allStats[types.MessageTypeGet]
	assert.Equal(t, uint64(1), getStats.Count.Load())
	assert.Equal(t, uint64(1), getStats.SuccessCount.Load())
	assert.Equal(t, uint64(0), getStats.FailureCount.Load())

	deleteStats := allStats[types.MessageTypeDelete]
	assert.Equal(t, uint64(1), deleteStats.FailureCount.Load())
}

// TestGetAllMessageTypeStats_Empty 测试获取空的消息类型统计
func TestGetAllMessageTypeStats_Empty(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	allStats := monitor.GetAllMessageTypeStats()
	assert.Empty(t, allStats)
}

// ========================================
// 节点统计测试
// ========================================

// TestGetNodeStats_Exists 测试获取存在的节点统计
func TestGetNodeStats_Exists(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolTCP,
		"127.0.0.1:8080",
		1024,
		1000000,
		true,
		nil,
	)

	stats, exists := monitor.GetNodeStats("127.0.0.1:8080")
	assert.True(t, exists)
	assert.NotNil(t, stats)
	assert.Equal(t, "127.0.0.1:8080", stats.NodeID)
	assert.Equal(t, "127.0.0.1:8080", stats.Address)
	assert.Equal(t, uint64(1), stats.MessageCount.Load())
}

// TestGetNodeStats_NotExists 测试获取不存在的节点统计
func TestGetNodeStats_NotExists(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	stats, exists := monitor.GetNodeStats("127.0.0.1:9999")
	assert.False(t, exists)
	assert.Nil(t, stats)
}

// TestGetAllNodeStats 测试获取所有节点统计
func TestGetAllNodeStats(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录到不同节点
	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, true, nil)
	monitor.RecordMessage(types.MessageTypePut, types.ProtocolUDP, "127.0.0.1:8081", 512, 500000, true, nil)

	allStats := monitor.GetAllNodeStats()
	assert.Len(t, allStats, 2)

	// 验证节点地址正确
	assert.Contains(t, allStats, "127.0.0.1:8080")
	assert.Contains(t, allStats, "127.0.0.1:8081")
}

// TestGetAllNodeStats_Empty 测试获取空的节点统计
func TestGetAllNodeStats_Empty(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	allStats := monitor.GetAllNodeStats()
	assert.Empty(t, allStats)
}

// ========================================
// 错误类型统计测试
// ========================================

// TestGetErrorTypeStats_Exists 测试获取存在的错误类型统计
func TestGetErrorTypeStats_Exists(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 使用预定义的协议错误
	testErr := fmt.Errorf("connection failed: %w", types.ErrTCPConnFailed)

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolTCP,
		"127.0.0.1:8080",
		1024,
		1000000,
		false,
		testErr,
	)

	stats, exists := monitor.GetErrorTypeStats("protocol_error")
	assert.True(t, exists)
	assert.NotNil(t, stats)
	assert.Equal(t, "protocol_error", stats.ErrorType)
	assert.Equal(t, uint64(1), stats.Count.Load())
}

// TestGetErrorTypeStats_NotExists 测试获取不存在的错误类型统计
func TestGetErrorTypeStats_NotExists(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	stats, exists := monitor.GetErrorTypeStats("unknown_error")
	assert.False(t, exists)
	assert.Nil(t, stats)
}

// TestGetAllErrorTypeStats 测试获取所有错误类型统计
func TestGetAllErrorTypeStats(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 使用预定义的协议错误和业务错误
	protocolErr := fmt.Errorf("connection failed: %w", types.ErrTCPConnFailed)
	businessErr := types.ErrMsgTooLarge

	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, false, protocolErr)
	monitor.RecordMessage(types.MessageTypePut, types.ProtocolUDP, "127.0.0.1:8081", 512, 500000, false, businessErr)

	allStats := monitor.GetAllErrorTypeStats()
	assert.Len(t, allStats, 2)

	// 验证错误类型正确
	assert.Contains(t, allStats, "protocol_error")
	assert.Contains(t, allStats, "business_error")

	// 验证协议错误统计
	protocolStats := allStats["protocol_error"]
	assert.Equal(t, uint64(1), protocolStats.Count.Load())
	assert.Contains(t, protocolStats.AffectedNodes, "127.0.0.1:8080")

	// 验证业务错误统计
	businessStats := allStats["business_error"]
	assert.Equal(t, uint64(1), businessStats.Count.Load())
	assert.Contains(t, businessStats.AffectedNodes, "127.0.0.1:8081")
}

// TestGetAllErrorTypeStats_Empty 测试获取空的错误类型统计
func TestGetAllErrorTypeStats_Empty(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	allStats := monitor.GetAllErrorTypeStats()
	assert.Empty(t, allStats)
}

// ========================================
// 协议统计测试
// ========================================

// TestGetProtocolStats_Exists 测试获取存在的协议统计
func TestGetProtocolStats_Exists(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolTCP,
		"127.0.0.1:8080",
		1024,
		1000000,
		true,
		nil,
	)

	stats, exists := monitor.GetProtocolStats(types.ProtocolTCP)
	assert.True(t, exists)
	assert.NotNil(t, stats)
	assert.Equal(t, types.ProtocolTCP, stats.ProtocolType)
	assert.Equal(t, uint64(1), stats.MessageCount.Load())
	assert.Equal(t, uint64(1), stats.SuccessCount.Load())
	assert.True(t, stats.IsActive.Load())
}

// TestGetProtocolStats_NotExists 测试获取不存在的协议统计
func TestGetProtocolStats_NotExists(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	stats, exists := monitor.GetProtocolStats("unknown_protocol")
	assert.False(t, exists)
	assert.Nil(t, stats)
}

// TestGetAllProtocolStats 测试获取所有协议统计
func TestGetAllProtocolStats(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录到不同协议
	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, true, nil)
	monitor.RecordMessage(types.MessageTypePut, types.ProtocolUDP, "127.0.0.1:8081", 512, 500000, false, errors.New("error"))
	monitor.RecordMessage(types.MessageTypeDelete, types.ProtocolGRPC, "127.0.0.1:8082", 256, 200000, true, nil)

	allStats := monitor.GetAllProtocolStats()
	assert.Len(t, allStats, 3)

	// 验证协议类型正确
	assert.Contains(t, allStats, types.ProtocolTCP)
	assert.Contains(t, allStats, types.ProtocolUDP)
	assert.Contains(t, allStats, types.ProtocolGRPC)

	// 验证 TCP 统计
	tcpStats := allStats[types.ProtocolTCP]
	assert.Equal(t, uint64(1), tcpStats.MessageCount.Load())
	assert.Equal(t, uint64(1), tcpStats.SuccessCount.Load())
	assert.Equal(t, uint64(0), tcpStats.FailureCount.Load())

	// 验证 UDP 统计
	udpStats := allStats[types.ProtocolUDP]
	assert.Equal(t, uint64(0), udpStats.SuccessCount.Load())
	assert.Equal(t, uint64(1), udpStats.FailureCount.Load())

	// 验证所有协议都是活跃的
	assert.True(t, allStats[types.ProtocolTCP].IsActive.Load())
	assert.True(t, allStats[types.ProtocolUDP].IsActive.Load())
	assert.True(t, allStats[types.ProtocolGRPC].IsActive.Load())
}

// TestGetAllProtocolStats_Empty 测试获取空的协议统计
func TestGetAllProtocolStats_Empty(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	allStats := monitor.GetAllProtocolStats()
	assert.Empty(t, allStats)
}

// ========================================
// 全局统计测试
// ========================================

// TestGetGlobalStats 测试获取全局统计
func TestGetGlobalStats(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录一些消息
	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, true, nil)
	monitor.RecordMessage(types.MessageTypePut, types.ProtocolUDP, "127.0.0.1:8081", 512, 500000, false, errors.New("error"))

	globalStats := monitor.GetGlobalStats()
	assert.NotNil(t, globalStats)
	assert.Equal(t, uint64(2), globalStats.TotalMessages.Load())
	assert.Equal(t, uint64(1), globalStats.TotalSuccess.Load())
	assert.Equal(t, uint64(1), globalStats.TotalFailure.Load())
	assert.Equal(t, uint64(1536), globalStats.TotalBytes.Load())      // 1024 + 512
	assert.Equal(t, uint64(1500000), globalStats.TotalLatency.Load()) // 1000000 + 500000
	assert.NotZero(t, globalStats.StartTime)
}

// TestGlobalStats_Uptime 测试运行时长更新
func TestGlobalStats_Uptime(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 等待运行时长更新（updateUptime 使用 time.Second ticker）
	// 需要等待至少 2 秒确保 ticker 被触发两次
	time.Sleep(2500 * time.Millisecond)

	globalStats := monitor.GetGlobalStats()
	uptime := time.Duration(globalStats.Uptime.Load())

	// 验证运行时长至少为 2 秒（允许 500ms 误差）
	assert.GreaterOrEqual(t, uptime, 2*time.Second)
	assert.Less(t, uptime, 5*time.Second) // 不应该超过 5 秒
}

// ========================================
// 并发安全测试
// ========================================

// TestConcurrentRecordMessage 测试并发记录消息
func TestConcurrentRecordMessage(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	done := make(chan bool, 100)

	// 并发记录 100 条消息
	for i := 0; i < 100; i++ {
		go func(index int) {
			msgType := types.MessageTypeGet
			if index%2 == 0 {
				msgType = types.MessageTypePut
			}

			monitor.RecordMessage(
				msgType,
				types.ProtocolTCP,
				"127.0.0.1:8080",
				1024,
				1000000,
				index%3 != 0, // 部分成功，部分失败
				nil,
			)
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 100; i++ {
		<-done
	}

	// 验证统计正确
	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(100), globalStats.TotalMessages.Load())
}

// TestConcurrentGetStats 测试并发获取统计
func TestConcurrentGetStats(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 先记录一些数据
	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, true, nil)

	done := make(chan bool, 100)

	// 并发读取统计
	for i := 0; i < 100; i++ {
		go func(idx int) {
			switch idx % 5 {
			case 0:
				monitor.GetGlobalStats()
			case 1:
				monitor.GetMessageTypeStats(types.MessageTypeGet)
			case 2:
				monitor.GetAllMessageTypeStats()
			case 3:
				monitor.GetNodeStats("127.0.0.1:8080")
			case 4:
				monitor.GetProtocolStats(types.ProtocolTCP)
			}
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 100; i++ {
		<-done
	}

	// 验证未发生 panic
	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(1), globalStats.TotalMessages.Load())
}

// TestConcurrentRecordAndGet 测试并发记录和读取
func TestConcurrentRecordAndGet(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	done := make(chan bool, 50)

	// 并发记录和读取
	for i := 0; i < 50; i++ {
		go func(idx int) {
			if idx%2 == 0 {
				// 记录消息
				monitor.RecordMessage(
					types.MessageTypeGet,
					types.ProtocolTCP,
					"127.0.0.1:8080",
					1024,
					1000000,
					true,
					nil,
				)
			} else {
				// 读取统计
				monitor.GetGlobalStats()
			}
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 50; i++ {
		<-done
	}

	// 验证统计正确
	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(25), globalStats.TotalMessages.Load())
}

// ========================================
// Reset 测试
// ========================================

// TestReset 测试重置统计
func TestReset(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录一些数据
	for i := 0; i < 10; i++ {
		monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, true, nil)
		monitor.RecordMessage(types.MessageTypePut, types.ProtocolUDP, "127.0.0.1:8081", 512, 500000, false, errors.New("error"))
	}

	// 验证数据已记录
	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(20), globalStats.TotalMessages.Load())

	// 重置统计
	monitor.Reset()

	// 验证所有统计已清空
	globalStats = monitor.GetGlobalStats()
	assert.Equal(t, uint64(0), globalStats.TotalMessages.Load())
	assert.Equal(t, uint64(0), globalStats.TotalSuccess.Load())
	assert.Equal(t, uint64(0), globalStats.TotalFailure.Load())
	assert.Equal(t, uint64(0), globalStats.TotalBytes.Load())
	assert.Equal(t, uint64(0), globalStats.TotalLatency.Load())

	// 验证消息类型统计已清空
	allMsgStats := monitor.GetAllMessageTypeStats()
	assert.Empty(t, allMsgStats)

	// 验证节点统计已清空
	allNodeStats := monitor.GetAllNodeStats()
	assert.Empty(t, allNodeStats)

	// 验证错误类型统计已清空
	allErrorStats := monitor.GetAllErrorTypeStats()
	assert.Empty(t, allErrorStats)

	// 验证协议统计已清空
	allProtocolStats := monitor.GetAllProtocolStats()
	assert.Empty(t, allProtocolStats)
}

// TestReset_AfterContinue 测试重置后继续记录
func TestReset_AfterContinue(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录初始数据
	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, true, nil)

	// 重置
	monitor.Reset()

	// 记录新数据
	monitor.RecordMessage(types.MessageTypePut, types.ProtocolUDP, "127.0.0.1:8081", 512, 500000, true, nil)

	// 验证只包含新数据
	globalStats := monitor.GetGlobalStats()
	assert.Equal(t, uint64(1), globalStats.TotalMessages.Load())
	assert.Equal(t, uint64(512), globalStats.TotalBytes.Load())

	// 验证只包含新消息类型统计
	_, exists := monitor.GetMessageTypeStats(types.MessageTypeGet)
	assert.False(t, exists)

	putStats, exists := monitor.GetMessageTypeStats(types.MessageTypePut)
	assert.True(t, exists)
	assert.Equal(t, uint64(1), putStats.Count.Load())
}

// ========================================
// Context 取消测试
// ========================================

// TestContextCancel_StopsUpdateGoroutine 测试 context 取消停止更新运行时
func TestContextCancel_StopsUpdateGoroutine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	monitor := NewDimensionalMonitorWithContext(ctx)
	defer monitor.Stop()

	// 获取初始运行时长
	globalStats := monitor.GetGlobalStats()
	initialUptime := globalStats.Uptime.Load()
	assert.Equal(t, int64(0), initialUptime)

	// 等待足够时间让更新 goroutine 运行并更新运行时长
	// updateUptime 使用 time.Second ticker，需要等待至少 1 秒
	time.Sleep(1500 * time.Millisecond)

	// 获取中间运行时长，确保 goroutine 正在运行
	globalStats = monitor.GetGlobalStats()
	midUptime := globalStats.Uptime.Load()
	uptime := time.Duration(midUptime)
	// 验证运行时长至少为 1 秒（允许 500ms 误差）
	assert.GreaterOrEqual(t, uptime, 1*time.Second)

	// 取消 context
	cancel()

	// 等待 goroutine 停止
	time.Sleep(200 * time.Millisecond)

	// 获取最终运行时长
	globalStats = monitor.GetGlobalStats()
	finalUptime := globalStats.Uptime.Load()

	// 验证运行时长在合理范围内（1.5-1.8s）
	finalUptimeDuration := time.Duration(finalUptime)
	assert.GreaterOrEqual(t, finalUptimeDuration, 1*time.Second)
	assert.Less(t, finalUptimeDuration, 3*time.Second)
}

// ========================================
// 统计值正确性测试
// ========================================

// TestStatsAccuracy_LastMessageTime 测试最后消息时间准确性
func TestStatsAccuracy_LastMessageTime(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	beforeRecord := time.Now().UnixNano()

	monitor.RecordMessage(
		types.MessageTypeGet,
		types.ProtocolTCP,
		"127.0.0.1:8080",
		1024,
		1000000,
		true,
		nil,
	)

	afterRecord := time.Now().UnixNano()

	// 验证消息类型统计的 LastMessageTime
	msgStats, exists := monitor.GetMessageTypeStats(types.MessageTypeGet)
	require.True(t, exists)
	lastMsgTime := msgStats.LastMessageTime.Load()
	assert.GreaterOrEqual(t, lastMsgTime, beforeRecord)
	assert.LessOrEqual(t, lastMsgTime, afterRecord)

	// 验证节点统计的 LastContactTime
	nodeStats, exists := monitor.GetNodeStats("127.0.0.1:8080")
	require.True(t, exists)
	lastContactTime := nodeStats.LastContactTime.Load()
	assert.GreaterOrEqual(t, lastContactTime, beforeRecord)
	assert.LessOrEqual(t, lastContactTime, afterRecord)
}

// TestStatsAccuracy_ErrorCounts 测试错误计数准确性
func TestStatsAccuracy_ErrorCounts(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录多种错误
	protocolErr1 := fmt.Errorf("connection failed: %w", types.ErrTCPConnFailed)
	protocolErr2 := fmt.Errorf("timeout: %w", types.ErrTCPSendTimeout)
	businessErr := types.ErrInvalidAddr

	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, false, protocolErr1)
	monitor.RecordMessage(types.MessageTypePut, types.ProtocolTCP, "127.0.0.1:8080", 512, 500000, false, protocolErr2)
	monitor.RecordMessage(types.MessageTypeDelete, types.ProtocolUDP, "127.0.0.1:8081", 256, 200000, false, businessErr)

	// 验证节点错误计数
	nodeStats, exists := monitor.GetNodeStats("127.0.0.1:8080")
	require.True(t, exists)
	assert.Equal(t, uint64(2), nodeStats.ErrorCounts["protocol_error"])

	nodeStats, exists = monitor.GetNodeStats("127.0.0.1:8081")
	require.True(t, exists)
	assert.Equal(t, uint64(1), nodeStats.ErrorCounts["business_error"])

	// 验证错误类型统计
	errorStats, exists := monitor.GetErrorTypeStats("protocol_error")
	require.True(t, exists)
	assert.Equal(t, uint64(2), errorStats.Count.Load())
	assert.Len(t, errorStats.AffectedNodes, 1)
	assert.Contains(t, errorStats.AffectedNodes, "127.0.0.1:8080")
}

// TestStatsAccuracy_CopyIndependence 测试统计拷贝独立性
func TestStatsAccuracy_CopyIndependence(t *testing.T) {
	monitor := NewDimensionalMonitor()
	defer monitor.Stop()

	// 记录一条消息
	monitor.RecordMessage(types.MessageTypeGet, types.ProtocolTCP, "127.0.0.1:8080", 1024, 1000000, true, nil)

	// 获取统计拷贝
	stats1, _ := monitor.GetMessageTypeStats(types.MessageTypeGet)
	stats2, _ := monitor.GetMessageTypeStats(types.MessageTypeGet)

	// 验证两次获取的统计是独立的拷贝
	stats1.Count.Add(100)
	assert.Equal(t, uint64(101), stats1.Count.Load())
	assert.Equal(t, uint64(1), stats2.Count.Load())

	// 原始统计应该仍然是 1
	originalStats, _ := monitor.GetMessageTypeStats(types.MessageTypeGet)
	assert.Equal(t, uint64(1), originalStats.Count.Load())
}

// ========================================
// 集成测试
// ========================================

// TestDimensionalMonitor_Integration 测试监控器集成场景
func TestDimensionalMonitor_Integration(t *testing.T) {
	t.Run("场景1: 多维统计关联", func(t *testing.T) {
		monitor := NewDimensionalMonitor()
		defer monitor.Stop()

		// 记录一条消息
		monitor.RecordMessage(
			types.MessageTypeGet,
			types.ProtocolTCP,
			"127.0.0.1:8080",
			1024,
			1000000,
			true,
			nil,
		)

		// 验证所有维度都记录了这条消息
		globalStats := monitor.GetGlobalStats()
		assert.Equal(t, uint64(1), globalStats.TotalMessages.Load())

		msgStats, _ := monitor.GetMessageTypeStats(types.MessageTypeGet)
		assert.Equal(t, uint64(1), msgStats.Count.Load())

		nodeStats, _ := monitor.GetNodeStats("127.0.0.1:8080")
		assert.Equal(t, uint64(1), nodeStats.MessageCount.Load())

		protocolStats, _ := monitor.GetProtocolStats(types.ProtocolTCP)
		assert.Equal(t, uint64(1), protocolStats.MessageCount.Load())
	})

	t.Run("场景2: 错误传播", func(t *testing.T) {
		monitor := NewDimensionalMonitor()
		defer monitor.Stop()

		// 使用预定义的协议错误
		testErr := fmt.Errorf("connection failed: %w", types.ErrTCPConnFailed)

		monitor.RecordMessage(
			types.MessageTypeGet,
			types.ProtocolTCP,
			"127.0.0.1:8080",
			1024,
			1000000,
			false,
			testErr,
		)

		// 验证错误在多个维度都有记录
		globalStats := monitor.GetGlobalStats()
		assert.Equal(t, uint64(1), globalStats.TotalFailure.Load())

		msgStats, _ := monitor.GetMessageTypeStats(types.MessageTypeGet)
		assert.Equal(t, uint64(1), msgStats.FailureCount.Load())

		nodeStats, _ := monitor.GetNodeStats("127.0.0.1:8080")
		assert.Equal(t, uint64(1), nodeStats.FailureCount.Load())
		assert.Equal(t, uint64(1), nodeStats.ErrorCounts["protocol_error"])

		protocolStats, _ := monitor.GetProtocolStats(types.ProtocolTCP)
		assert.Equal(t, uint64(1), protocolStats.FailureCount.Load())

		errorStats, _ := monitor.GetErrorTypeStats("protocol_error")
		assert.Equal(t, uint64(1), errorStats.Count.Load())
		assert.Contains(t, errorStats.AffectedNodes, "127.0.0.1:8080")
	})

	t.Run("场景3: 高并发场景", func(t *testing.T) {
		monitor := NewDimensionalMonitor()
		defer monitor.Stop()

		var wg sync.WaitGroup
		for i := 0; i < 1000; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				monitor.RecordMessage(
					types.MessageTypeGet,
					types.ProtocolTCP,
					"127.0.0.1:8080",
					1024,
					1000000,
					true,
					nil,
				)
			}(i)
		}

		wg.Wait()

		// 验证统计正确
		globalStats := monitor.GetGlobalStats()
		assert.Equal(t, uint64(1000), globalStats.TotalMessages.Load())
		assert.Equal(t, uint64(1000), globalStats.TotalSuccess.Load())
	})

	t.Run("场景4: 资源清理", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		monitor := NewDimensionalMonitorWithContext(ctx)

		// 记录一些数据
		for i := 0; i < 10; i++ {
			monitor.RecordMessage(
				types.MessageTypeGet,
				types.ProtocolTCP,
				"127.0.0.1:8080",
				1024,
				1000000,
				true,
				nil,
			)
		}

		// 取消 context
		cancel()

		// 停止监控器
		monitor.Stop()

		// 验证可以安全地重置
		monitor.Reset()

		// 验证所有统计已清空
		globalStats := monitor.GetGlobalStats()
		assert.Equal(t, uint64(0), globalStats.TotalMessages.Load())
	})
}
