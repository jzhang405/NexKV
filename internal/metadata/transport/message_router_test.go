// Package transport 消息路由器模块测试
package transport

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// 配置测试
// ========================================

// TestDefaultRouterConfig 测试默认配置
func TestDefaultRouterConfig(t *testing.T) {
	config := DefaultRouterConfig()

	assert.Equal(t, 1024, config.SmallMessageThreshold)
	assert.Equal(t, 50*1024, config.MediumMessageThreshold)
	assert.Equal(t, 50*1024, config.LargeMessageThreshold)
	assert.NotNil(t, config.BroadcastAddresses)
	assert.NotEmpty(t, config.BroadcastAddresses)
	assert.NotNil(t, config.NonFallbackMessageTypes)
	assert.NotEmpty(t, config.NonFallbackMessageTypes)
	assert.True(t, config.EnableAutoRouting)
}

// TestRouterConfig_Custom 测试自定义配置
func TestRouterConfig_Custom(t *testing.T) {
	config := &RouterConfig{
		SmallMessageThreshold:   2048,
		MediumMessageThreshold:  100 * 1024,
		LargeMessageThreshold:   100 * 1024,
		BroadcastAddresses:      []string{"255.255.255.255"},
		NonFallbackMessageTypes: []types.MessageType{types.MessageType2PCCommit},
		EnableAutoRouting:       false,
	}

	router := NewMessageRouter(config)
	assert.Equal(t, config, router.GetConfig())
}

// TestNewMessageRouter_NilConfig 测试空配置使用默认值
func TestNewMessageRouter_NilConfig(t *testing.T) {
	router := NewMessageRouter(nil)
	assert.NotNil(t, router.GetConfig())
	assert.True(t, router.GetConfig().EnableAutoRouting)
}

// ========================================
// 消息大小分类测试
// ========================================

// TestClassifyMessageSize_Small 测试小消息分类
func TestClassifyMessageSize_Small(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	testCases := []struct {
		name     string
		msgSize  int
		expected MessageSizeRange
	}{
		{"0 字节", 0, SmallMessage},
		{"512 字节", 512, SmallMessage},
		{"1023 字节（边界值）", 1023, SmallMessage},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := router.classifyMessageSize(tc.msgSize, config)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestClassifyMessageSize_Medium 测试中等消息分类
func TestClassifyMessageSize_Medium(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	testCases := []struct {
		name     string
		msgSize  int
		expected MessageSizeRange
	}{
		{"1KB（边界值）", 1024, MediumMessage},
		{"10KB", 10 * 1024, MediumMessage},
		{"50KB-1 字节（边界值）", 50*1024 - 1, MediumMessage},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := router.classifyMessageSize(tc.msgSize, config)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestClassifyMessageSize_Large 测试大消息分类
func TestClassifyMessageSize_Large(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	testCases := []struct {
		name     string
		msgSize  int
		expected MessageSizeRange
	}{
		{"50KB（边界值）", 50 * 1024, LargeMessage},
		{"100KB", 100 * 1024, LargeMessage},
		{"1MB", 1024 * 1024, LargeMessage},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := router.classifyMessageSize(tc.msgSize, config)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// ========================================
// 强制路由规则测试
// ========================================

// TestCheckForcedRouting_BroadcastAddress 测试广播地址强制 UDP
func TestCheckForcedRouting_BroadcastAddress(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	testCases := []struct {
		name     string
		addr     string
		expected ProtocolType
	}{
		{"有限广播地址", "255.255.255.255:8080", ProtocolUDP},
		{"组播地址", "239.1.1.1:8080", ProtocolUDP},
		{"组播地址变体", "239.1.1.1:9000", ProtocolUDP},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			decision := router.checkForcedRouting(tc.addr, types.MessageTypeGet, config)
			require.NotNil(t, decision)
			assert.Equal(t, tc.expected, decision.ProtocolType)
			assert.False(t, decision.ShouldDegrade)
		})
	}
}

// TestCheckForcedRouting_NonFallbackMessageType 测试不可降级消息类型强制 TCP
func TestCheckForcedRouting_NonFallbackMessageType(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	nonFallbackTypes := []types.MessageType{
		types.MessageType2PCCommit,
		types.MessageType2PCRollback,
		types.MessageTypeQuorumDecide,
		types.MessageTypeLeaderElection,
		types.MessageTypeNodeJoin,
		types.MessageTypeNodeLeave,
	}

	for _, msgType := range nonFallbackTypes {
		t.Run(msgType.String(), func(t *testing.T) {
			decision := router.checkForcedRouting("127.0.0.1:8080", msgType, config)
			require.NotNil(t, decision)
			assert.Equal(t, ProtocolTCP, decision.ProtocolType)
			assert.False(t, decision.ShouldDegrade)
		})
	}
}

// TestCheckForcedRouting_NoForcedRouting 测试无强制路由规则
func TestCheckForcedRouting_NoForcedRouting(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	decision := router.checkForcedRouting("127.0.0.1:8080", types.MessageTypeGet, config)
	assert.Nil(t, decision)
}

// ========================================
// 三维路由决策测试
// ========================================

// TestThreeDimensionalDecision_ExpectResponse 测试回应期望决策（维度 1）
func TestThreeDimensionalDecision_ExpectResponse(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	expectResponseTypes := []types.MessageType{
		types.MessageTypeGet,
		types.MessageTypePut,
		types.MessageTypeDelete,
		types.MessageTypeGossipSync,
		types.MessageTypeGossipDigest,
	}

	for _, msgType := range expectResponseTypes {
		t.Run(msgType.String(), func(t *testing.T) {
			// 小消息，但需要回应 → TCP
			decision := router.threeDimensionalDecision(512, msgType, config)
			assert.Equal(t, ProtocolTCP, decision.ProtocolType)
			assert.Contains(t, decision.Reason, "需要回应")
			assert.False(t, decision.ShouldDegrade)
		})
	}
}

// TestThreeDimensionalDecision_LargeMessage 测试大消息决策（维度 2）
func TestThreeDimensionalDecision_LargeMessage(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	// 不需要回应，但是大消息（>50KB）→ TCP
	decision := router.threeDimensionalDecision(100*1024, types.MessageTypeGossipSyncReply, config)
	assert.Equal(t, ProtocolTCP, decision.ProtocolType)
	assert.Contains(t, decision.Reason, "大消息")
	assert.False(t, decision.ShouldDegrade)
}

// TestThreeDimensionalDecision_ProtocolUDP 测试 UDP 协议决策（维度 3）
func TestThreeDimensionalDecision_ProtocolUDP(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	// 不需要回应，小消息 → UDP
	// 使用 MessageTypeGossipSyncReply (响应消息不需要回应，且使用 UDP)
	decision := router.threeDimensionalDecision(512, types.MessageTypeGossipSyncReply, config)
	assert.Equal(t, ProtocolUDP, decision.ProtocolType)
	assert.Contains(t, decision.Reason, "UDP")
	assert.True(t, decision.ShouldDegrade)
}

// TestThreeDimensionalDecision_DefaultToUDP 测试默认 UDP 决策
func TestThreeDimensionalDecision_DefaultToUDP(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	// 不需要回应，小消息，容忍丢失 → UDP
	decision := router.threeDimensionalDecision(512, types.MessageTypeGossipSyncReply, config)
	assert.Equal(t, ProtocolUDP, decision.ProtocolType)
	assert.Contains(t, decision.Reason, "UDP")
	assert.True(t, decision.ShouldDegrade)
}

// ========================================
// DecideProtocol 完整流程测试
// ========================================

// TestDecideProtocol_BroadcastAddress 测试广播地址决策
func TestDecideProtocol_BroadcastAddress(t *testing.T) {
	router := NewMessageRouter(nil)

	decision := router.DecideProtocol(context.Background(), "255.255.255.255:8080", types.MessageTypeGet, 512)
	assert.Equal(t, ProtocolUDP, decision.ProtocolType)
	assert.Contains(t, decision.Reason, "广播地址")
	assert.False(t, decision.ShouldDegrade)
}

// TestDecideProtocol_NonFallbackMessageType 测试不可降级消息类型决策
func TestDecideProtocol_NonFallbackMessageType(t *testing.T) {
	router := NewMessageRouter(nil)

	decision := router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageType2PCCommit, 512)
	assert.Equal(t, ProtocolTCP, decision.ProtocolType)
	assert.Contains(t, decision.Reason, "不可降级")
	assert.False(t, decision.ShouldDegrade)
}

// TestDecideProtocol_AutoRoutingDisabled 测试自动路由禁用
func TestDecideProtocol_AutoRoutingDisabled(t *testing.T) {
	config := DefaultRouterConfig()
	config.EnableAutoRouting = false
	router := NewMessageRouter(config)

	decision := router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 512)
	assert.Equal(t, ProtocolType(""), decision.ProtocolType)
	assert.Contains(t, decision.Reason, "自动路由未启用")
	assert.False(t, decision.ShouldDegrade)
}

// TestDecideProtocol_ThreeDimensionalDecision 测试三维决策流程
func TestDecideProtocol_ThreeDimensionalDecision(t *testing.T) {
	router := NewMessageRouter(nil)

	testCases := []struct {
		name         string
		addr         string
		msgType      types.MessageType
		msgSize      int
		expectedType ProtocolType
	}{
		{"需要回应（小消息）", "127.0.0.1:8080", types.MessageTypeGet, 512, ProtocolTCP},
		{"大消息", "127.0.0.1:8080", types.MessageTypeGossipSyncReply, 100 * 1024, ProtocolTCP},
		{"高可靠性", "127.0.0.1:8080", types.MessageType2PCPrepare, 512, ProtocolTCP},
		{"默认 UDP", "127.0.0.1:8080", types.MessageTypeGossipSyncReply, 512, ProtocolUDP},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			decision := router.DecideProtocol(context.Background(), tc.addr, tc.msgType, tc.msgSize)
			assert.Equal(t, tc.expectedType, decision.ProtocolType)
		})
	}
}

// ========================================
// 统计功能测试
// ========================================

// TestRouterStats_TotalDecisions 测试总决策次数统计
func TestRouterStats_TotalDecisions(t *testing.T) {
	router := NewMessageRouter(nil)

	// 初始状态
	stats := router.GetStats()
	assert.Equal(t, uint64(0), stats.TotalDecisions.Load())

	// 执行 5 次决策
	for i := 0; i < 5; i++ {
		router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 512)
	}

	stats = router.GetStats()
	assert.Equal(t, uint64(5), stats.TotalDecisions.Load())
}

// TestRouterStats_DecisionsByProtocol 测试按协议类型统计
func TestRouterStats_DecisionsByProtocol(t *testing.T) {
	router := NewMessageRouter(nil)

	// TCP 决策
	router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 512)
	router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 512)

	// UDP 决策
	router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGossipSyncReply, 512)

	stats := router.GetStats()

	// 验证 TCP 统计
	if val, ok := stats.DecisionsByProtocol.Load(ProtocolTCP); ok {
		if counter, ok := val.(*atomic.Uint64); ok {
			assert.Equal(t, uint64(2), counter.Load())
		}
	} else {
		t.Fatal("TCP 协议统计不存在")
	}

	// 验证 UDP 统计
	if val, ok := stats.DecisionsByProtocol.Load(ProtocolUDP); ok {
		if counter, ok := val.(*atomic.Uint64); ok {
			assert.Equal(t, uint64(1), counter.Load())
		}
	} else {
		t.Fatal("UDP 协议统计不存在")
	}
}

// TestRouterStats_DecisionsByMessageType 测试按消息类型统计
func TestRouterStats_DecisionsByMessageType(t *testing.T) {
	router := NewMessageRouter(nil)

	// 3 次 Get 请求
	for i := 0; i < 3; i++ {
		router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 512)
	}

	stats := router.GetStats()

	// 验证 Get 消息类型统计
	if val, ok := stats.DecisionsByMessageType.Load(types.MessageTypeGet); ok {
		if counter, ok := val.(*atomic.Uint64); ok {
			assert.Equal(t, uint64(3), counter.Load())
		}
	} else {
		t.Fatal("Get 消息类型统计不存在")
	}
}

// TestRouterStats_DegradationTriggers 测试降级触发统计
func TestRouterStats_DegradationTriggers(t *testing.T) {
	router := NewMessageRouter(nil)

	// 初始状态
	stats := router.GetStats()
	assert.Equal(t, uint64(0), stats.DegradationTriggers.Load())

	// 执行 3 次 UDP 决策（可降级）
	for i := 0; i < 3; i++ {
		router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGossipSyncReply, 512)
	}

	stats = router.GetStats()
	assert.Equal(t, uint64(3), stats.DegradationTriggers.Load())
}

// TestRouterResetStats 测试重置统计
func TestRouterResetStats(t *testing.T) {
	router := NewMessageRouter(nil)

	// 执行一些决策
	for i := 0; i < 5; i++ {
		router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 512)
	}

	// 重置统计
	router.ResetStats()

	stats := router.GetStats()
	assert.Equal(t, uint64(0), stats.TotalDecisions.Load())
	assert.Equal(t, uint64(0), stats.DegradationTriggers.Load())

	// 验证 sync.Map 被清空（通过遍历检查，避免复制 lock value）
	protocolEmpty := true
	stats.DecisionsByProtocol.Range(func(key, value interface{}) bool {
		protocolEmpty = false
		return false // 找到一个就停止
	})
	assert.True(t, protocolEmpty, "DecisionsByProtocol should be empty")

	messageTypeEmpty := true
	stats.DecisionsByMessageType.Range(func(key, value interface{}) bool {
		messageTypeEmpty = false
		return false // 找到一个就停止
	})
	assert.True(t, messageTypeEmpty, "DecisionsByMessageType should be empty")
}

// ========================================
// 并发安全测试
// ========================================

// TestConcurrentDecideProtocol 测试并发决策
func TestConcurrentDecideProtocol(t *testing.T) {
	router := NewMessageRouter(nil)

	// 并发执行 100 次决策
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 512)
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 100; i++ {
		<-done
	}

	// 验证统计正确
	stats := router.GetStats()
	assert.Equal(t, uint64(100), stats.TotalDecisions.Load())
}

// TestRouterConcurrentConfigUpdate 测试并发配置更新
func TestRouterConcurrentConfigUpdate(t *testing.T) {
	router := NewMessageRouter(nil)

	done := make(chan bool, 20)

	// 并发读取配置
	for i := 0; i < 10; i++ {
		go func() {
			router.GetConfig()
			done <- true
		}()
	}

	// 并发更新配置
	for i := 0; i < 10; i++ {
		go func(n int) {
			config := &RouterConfig{
				SmallMessageThreshold: 1024 + n,
				EnableAutoRouting:     true,
			}
			router.UpdateConfig(config)
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 20; i++ {
		<-done
	}

	// 验证未发生 panic
	config := router.GetConfig()
	assert.NotNil(t, config)
}

// TestRouterConcurrentStatsAccess 测试并发统计访问
func TestRouterConcurrentStatsAccess(t *testing.T) {
	router := NewMessageRouter(nil)

	done := make(chan bool, 20)

	// 并发写入统计
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 512)
			}
			done <- true
		}()
	}

	// 并发读取统计
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				router.GetStats()
			}
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 20; i++ {
		<-done
	}

	// 验证统计一致性
	stats := router.GetStats()
	assert.Equal(t, uint64(100), stats.TotalDecisions.Load())
}

// ========================================
// 边界条件测试
// ========================================

// TestDecideProtocol_ZeroMessageSize 测试零大小消息
func TestDecideProtocol_ZeroMessageSize(t *testing.T) {
	router := NewMessageRouter(nil)

	// 零大小，但需要回应 → TCP
	decision := router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, 0)
	assert.Equal(t, ProtocolTCP, decision.ProtocolType)
	assert.Contains(t, decision.Reason, "需要回应")
}

// TestDecideProtocol_VeryLargeMessage 测试超大消息
func TestDecideProtocol_VeryLargeMessage(t *testing.T) {
	router := NewMessageRouter(nil)

	// 10MB 消息 → TCP
	decision := router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGossipSyncReply, 10*1024*1024)
	assert.Equal(t, ProtocolTCP, decision.ProtocolType)
	assert.Contains(t, decision.Reason, "大消息")
}

// TestDecideProtocol_BoundaryValues 测试边界值
func TestDecideProtocol_BoundaryValues(t *testing.T) {
	config := DefaultRouterConfig()
	router := NewMessageRouter(config)

	testCases := []struct {
		name         string
		msgSize      int
		expectedType ProtocolType
	}{
		{"1KB 边界值（小消息上限）", 1024, ProtocolTCP},          // Get 需要回应
		{"50KB 边界值（中等消息上限）", 50*1024 - 1, ProtocolTCP}, // Get 需要回应
		{"50KB 边界值（大消息下限）", 50 * 1024, ProtocolTCP},    // Get 需要回应
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			decision := router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGet, tc.msgSize)
			assert.Equal(t, tc.expectedType, decision.ProtocolType)
		})
	}
}

// ========================================
// 工具函数测试
// ========================================

// TestParseAddress_ValidAddresses 测试有效地址解析
func TestParseAddress_ValidAddresses(t *testing.T) {
	validAddresses := []string{
		"127.0.0.1:8080",
		"192.168.1.1:9000",
		"10.0.0.1:80",
		"255.255.255.255:8080",
		// 注意: IPv6 地址 ::1:8080 当前实现不支持，因为解析器会混淆端口号
		// 如果需要支持 IPv6，需要使用括号格式如 [::1]:8080
	}

	for _, addr := range validAddresses {
		t.Run(addr, func(t *testing.T) {
			parsed, err := ParseAddress(addr)
			assert.NoError(t, err)
			assert.Equal(t, addr, parsed)
		})
	}
}

// TestParseAddress_InvalidAddresses 测试无效地址解析
func TestParseAddress_InvalidAddresses(t *testing.T) {
	testCases := []struct {
		name        string
		addr        string
		expectedErr string
	}{
		{"缺少端口", "127.0.0.1", "missing port in address"},
		{"无效格式", "invalid-address", "invalid address format"},
		{"无效 IP", "999.999.999.999:8080", "invalid IP address"},
		{"空地址", "", "invalid address format"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAddress(tc.addr)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

// TestIsBroadcastAddress 测试广播地址判断
func TestIsBroadcastAddress(t *testing.T) {
	testCases := []struct {
		addr     string
		expected bool
	}{
		{"255.255.255.255:8080", true},
		{"239.1.1.1:9000", true},
		{"127.0.0.1:8080", false},
		{"192.168.1.1:8080", false},
	}

	for _, tc := range testCases {
		t.Run(tc.addr, func(t *testing.T) {
			result := IsBroadcastAddress(tc.addr)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestIsNonFallbackMessageType 测试不可降级消息类型判断
func TestIsNonFallbackMessageType(t *testing.T) {
	nonFallbackTypes := []types.MessageType{
		types.MessageType2PCCommit,
		types.MessageType2PCRollback,
		types.MessageTypeQuorumDecide,
		types.MessageTypeLeaderElection,
		types.MessageTypeNodeJoin,
		types.MessageTypeNodeLeave,
	}

	for _, msgType := range nonFallbackTypes {
		t.Run(msgType.String(), func(t *testing.T) {
			result := IsNonFallbackMessageType(msgType)
			assert.True(t, result)
		})
	}

	// 可降级消息类型
	fallbackTypes := []types.MessageType{
		types.MessageTypeGet,
		types.MessageTypePut,
		types.MessageTypeDelete,
	}

	for _, msgType := range fallbackTypes {
		t.Run(msgType.String(), func(t *testing.T) {
			result := IsNonFallbackMessageType(msgType)
			assert.False(t, result)
		})
	}
}

// ========================================
// 集成测试
// ========================================

// TestMessageRouter_Integration 测试消息路由器集成场景
func TestMessageRouter_Integration(t *testing.T) {
	t.Run("场景1: 典型的 Gossip 消息路由", func(t *testing.T) {
		router := NewMessageRouter(nil)

		// GossipSync（需要回应）→ TCP
		decision := router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGossipSync, 1024)
		assert.Equal(t, ProtocolTCP, decision.ProtocolType)

		// GossipSyncReply（不需要回应，小消息，容忍丢失）→ UDP
		decision = router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGossipSyncReply, 1024)
		assert.Equal(t, ProtocolUDP, decision.ProtocolType)
		assert.True(t, decision.ShouldDegrade)
	})

	t.Run("场景2: 大消息优先使用 TCP", func(t *testing.T) {
		router := NewMessageRouter(nil)

		// 即使不需要回应，大消息也会使用 TCP
		decision := router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageTypeGossipSyncReply, 100*1024)
		assert.Equal(t, ProtocolTCP, decision.ProtocolType)
		assert.False(t, decision.ShouldDegrade)
	})

	t.Run("场景3: 关键消息强制使用 TCP", func(t *testing.T) {
		router := NewMessageRouter(nil)

		// 2PC Commit 即使是小消息也强制使用 TCP
		decision := router.DecideProtocol(context.Background(), "127.0.0.1:8080", types.MessageType2PCCommit, 512)
		assert.Equal(t, ProtocolTCP, decision.ProtocolType)
		assert.False(t, decision.ShouldDegrade)
	})

	t.Run("场景4: 广播地址强制使用 UDP", func(t *testing.T) {
		router := NewMessageRouter(nil)

		// 即使需要回应，广播地址也使用 UDP
		decision := router.DecideProtocol(context.Background(), "255.255.255.255:8080", types.MessageTypeGet, 512)
		assert.Equal(t, ProtocolUDP, decision.ProtocolType)
		assert.False(t, decision.ShouldDegrade)
	})
}
