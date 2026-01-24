// Package transport 流量控制模块测试
package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
)

// ========================================
// 测试消息类型（用于流量控制测试）
// ========================================

// testPriorityMessage 实现用于测试的带优先级的消息
type testPriorityMessage struct {
	priority int
	msgType  types.MessageType
}

func (m *testPriorityMessage) Type() types.MessageType {
	return m.msgType
}

func (m *testPriorityMessage) Priority() int {
	return m.priority
}

func (m *testPriorityMessage) ExpectResponse() types.ResponseExpectation {
	return types.ExpectResponse
}

func (m *testPriorityMessage) Reliability() types.ReliabilityRequirement {
	return types.BestEffort
}

// ========================================
// ShouldDrop 测试
// ========================================

// TestShouldDrop_BelowThreshold 测试通道未满载时不丢弃消息
func TestShouldDrop_BelowThreshold(t *testing.T) {
	testCases := []struct {
		name         string
		channelUsage float64
		priority     int
		expectedDrop bool
	}{
		{"空闲通道(0%)", 0.0, int(types.PriorityLow), false},
		{"空闲通道(50%)", 0.5, int(types.PriorityLow), false},
		{"轻微负载(79%)", 0.79, int(types.PriorityLow), false},
		{"正常优先级(79%)", 0.79, int(types.PriorityNormal), false},
		{"高优先级(79%)", 0.79, int(types.PriorityHigh), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &testPriorityMessage{
				priority: tc.priority,
				msgType:  types.MessageTypeGet,
			}

			result := ShouldDrop(msg, tc.channelUsage)
			assert.Equal(t, tc.expectedDrop, result, "通道未满载时不应丢弃消息")
		})
	}
}

// TestShouldDrop_ModerateLoad 测试中等负载（80%-95%）时的丢弃策略
func TestShouldDrop_ModerateLoad(t *testing.T) {
	testCases := []struct {
		name         string
		channelUsage float64
		priority     int
		expectedDrop bool
	}{
		{"最低优先级(80%)", 0.8, int(types.PriorityLow), true},
		{"最低优先级(90%)", 0.9, int(types.PriorityLow), true},
		{"最低优先级(94%)", 0.94, int(types.PriorityLow), true},
		{"正常优先级(80%)", 0.8, int(types.PriorityNormal), false},
		{"正常优先级(90%)", 0.9, int(types.PriorityNormal), false},
		{"正常优先级(94%)", 0.94, int(types.PriorityNormal), false},
		{"高优先级(80%)", 0.8, int(types.PriorityHigh), false},
		{"高优先级(90%)", 0.9, int(types.PriorityHigh), false},
		{"高优先级(94%)", 0.94, int(types.PriorityHigh), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &testPriorityMessage{
				priority: tc.priority,
				msgType:  types.MessageTypeGet,
			}

			result := ShouldDrop(msg, tc.channelUsage)
			assert.Equal(t, tc.expectedDrop, result, "中等负载时应仅丢弃最低优先级")
		})
	}
}

// TestShouldDrop_HighLoad 测试高负载（>=95%）时的丢弃策略
func TestShouldDrop_HighLoad(t *testing.T) {
	testCases := []struct {
		name         string
		channelUsage float64
		priority     int
		expectedDrop bool
	}{
		{"最低优先级(95%)", 0.95, int(types.PriorityLow), true},
		{"最低优先级(99%)", 0.99, int(types.PriorityLow), true},
		{"正常优先级(95%)", 0.95, int(types.PriorityNormal), true},
		{"正常优先级(99%)", 0.99, int(types.PriorityNormal), true},
		{"高优先级(95%)", 0.95, int(types.PriorityHigh), false},
		{"高优先级(99%)", 0.99, int(types.PriorityHigh), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &testPriorityMessage{
				priority: tc.priority,
				msgType:  types.MessageTypeGet,
			}

			result := ShouldDrop(msg, tc.channelUsage)
			assert.Equal(t, tc.expectedDrop, result, "高负载时应丢弃正常及以下优先级")
		})
	}
}

// TestShouldDrop_BoundaryValues 测试边界值
func TestShouldDrop_BoundaryValues(t *testing.T) {
	testCases := []struct {
		name         string
		channelUsage float64
		priority     int
		expectedDrop bool
	}{
		{"边界值0.0", 0.0, int(types.PriorityLow), false},
		{"边界值0.8", 0.8, int(types.PriorityLow), true},      // 刚好达到阈值
		{"边界值0.95", 0.95, int(types.PriorityNormal), true}, // 刚好达到高负载阈值
		{"边界值1.0", 1.0, int(types.PriorityHigh), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &testPriorityMessage{
				priority: tc.priority,
				msgType:  types.MessageTypeGet,
			}

			result := ShouldDrop(msg, tc.channelUsage)
			assert.Equal(t, tc.expectedDrop, result)
		})
	}
}

// ========================================
// GetPriorityName 测试
// ========================================

// TestGetPriorityName_AllLevels 测试所有优先级名称
func TestGetPriorityName_AllLevels(t *testing.T) {
	testCases := []struct {
		name         string
		priority     int
		expectedName string
	}{
		{"低优先级", int(types.PriorityLow), "LOW"},
		{"正常优先级", int(types.PriorityNormal), "NORMAL"},
		{"高优先级", int(types.PriorityHigh), "HIGH"},
		{"关键优先级", int(types.PriorityCritical), "CRITICAL"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := GetPriorityName(tc.priority)
			assert.Equal(t, tc.expectedName, result)
		})
	}
}

// TestGetPriorityName_UnknownLevel 测试未知优先级
func TestGetPriorityName_UnknownLevel(t *testing.T) {
	testCases := []struct {
		name     string
		priority int
	}{
		{"负数优先级", -1},
		{"超出范围优先级", 100},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := GetPriorityName(tc.priority)
			assert.Equal(t, "UNKNOWN", result, "未知优先级应返回UNKNOWN")
		})
	}
}

// TestGetPriorityName_AllPriorityValues 测试所有可能的优先级值（0-255）
func TestGetPriorityName_AllPriorityValues(t *testing.T) {
	// 测试标准值
	assert.Equal(t, "LOW", GetPriorityName(int(types.PriorityLow)))
	assert.Equal(t, "NORMAL", GetPriorityName(int(types.PriorityNormal)))
	assert.Equal(t, "HIGH", GetPriorityName(int(types.PriorityHigh)))
	assert.Equal(t, "CRITICAL", GetPriorityName(int(types.PriorityCritical)))

	// 测试非标准值
	assert.Equal(t, "UNKNOWN", GetPriorityName(127))
	assert.Equal(t, "UNKNOWN", GetPriorityName(255))
}

// ========================================
// LogFlowControlEvent 测试
// ========================================

// TestLogFlowControlEvent_Accept 测试记录接收事件
func TestLogFlowControlEvent_Accept(t *testing.T) {
	msg := &testPriorityMessage{
		priority: int(types.PriorityNormal),
		msgType:  types.MessageTypeGet,
	}

	// 这个函数只是记录日志，不会返回错误
	// 主要验证函数不会崩溃
	assert.NotPanics(t, func() {
		LogFlowControlEvent(flowControlEventAccept, msg, 0.5)
	})
}

// TestLogFlowControlEvent_Drop 测试记录丢弃事件
func TestLogFlowControlEvent_Drop(t *testing.T) {
	msg := &testPriorityMessage{
		priority: int(types.PriorityLow),
		msgType:  types.MessageTypeGet,
	}

	assert.NotPanics(t, func() {
		LogFlowControlEvent(flowControlEventDrop, msg, 0.95)
	})
}

// TestLogFlowControlEvent_ChannelFull 测试记录通道满载事件
func TestLogFlowControlEvent_ChannelFull(t *testing.T) {
	msg := &testPriorityMessage{
		priority: int(types.PriorityNormal),
		msgType:  types.MessageTypeGet,
	}

	assert.NotPanics(t, func() {
		LogFlowControlEvent(flowControlEventChannelFull, msg, 1.0)
	})
}

// TestLogFlowControlEvent_AllEvents 测试所有事件类型
func TestLogFlowControlEvent_AllEvents(t *testing.T) {
	msg := &testPriorityMessage{
		priority: int(types.PriorityHigh),
		msgType:  types.MessageTypePut,
	}

	events := []string{
		flowControlEventAccept,
		flowControlEventDrop,
		flowControlEventChannelFull,
	}

	for _, event := range events {
		t.Run(event, func(t *testing.T) {
			assert.NotPanics(t, func() {
				LogFlowControlEvent(event, msg, 0.85)
			})
		})
	}
}

// ========================================
// 集成测试
// ========================================

// TestFlowControlIntegration 测试流量控制集成场景
func TestFlowControlIntegration(t *testing.T) {
	t.Run("场景1: 从空闲到高负载", func(t *testing.T) {
		msg := &testPriorityMessage{
			priority: int(types.PriorityNormal),
			msgType:  types.MessageTypeGet,
		}

		// 0% 使用率 - 不丢弃
		assert.False(t, ShouldDrop(msg, 0.0))

		// 85% 使用率 - 不丢弃（正常优先级）
		assert.False(t, ShouldDrop(msg, 0.85))

		// 95% 使用率 - 丢弃（正常优先级）
		assert.True(t, ShouldDrop(msg, 0.95))
	})

	t.Run("场景2: 高优先级消息始终不丢弃", func(t *testing.T) {
		msg := &testPriorityMessage{
			priority: int(types.PriorityHigh),
			msgType:  types.MessageTypePut,
		}

		// 各种负载下都不应丢弃高优先级消息
		assert.False(t, ShouldDrop(msg, 0.5))
		assert.False(t, ShouldDrop(msg, 0.8))
		assert.False(t, ShouldDrop(msg, 0.95))
		assert.False(t, ShouldDrop(msg, 0.99))
		assert.False(t, ShouldDrop(msg, 1.0))
	})

	t.Run("场景3: 低优先级消息容易被丢弃", func(t *testing.T) {
		msg := &testPriorityMessage{
			priority: int(types.PriorityLow),
			msgType:  types.MessageTypeGossipSync,
		}

		// 80% 以上就开始丢弃
		assert.True(t, ShouldDrop(msg, 0.8))
		assert.True(t, ShouldDrop(msg, 0.95))
		assert.True(t, ShouldDrop(msg, 1.0))

		// 只有低负载时保留
		assert.False(t, ShouldDrop(msg, 0.5))
	})
}

// TestFlowControl_PriorityNameMapping 测试优先级名称映射一致性
func TestFlowControl_PriorityNameMapping(t *testing.T) {
	// 验证 Priority 常量值与名称映射一致
	priorities := []struct {
		constant types.Priority
		value    int
		name     string
	}{
		{types.PriorityLow, 0, "LOW"},
		{types.PriorityNormal, 1, "NORMAL"},
		{types.PriorityHigh, 2, "HIGH"},
		{types.PriorityCritical, 3, "CRITICAL"},
	}

	for _, p := range priorities {
		t.Run(p.name, func(t *testing.T) {
			assert.Equal(t, p.name, GetPriorityName(p.value))
		})
	}
}
