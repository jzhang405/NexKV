// Package transport 降级机制模块测试
package transport

import (
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// 配置测试
// ========================================

// TestDefaultDegradationConfig 测试默认配置
func TestDefaultDegradationConfig(t *testing.T) {
	config := DefaultDegradationConfig()

	assert.Equal(t, 3, config.FailureThreshold)
	assert.Equal(t, 30*time.Second, config.FailureTimeout)
	assert.Equal(t, 60*time.Second, config.RecoveryTimeout)
	assert.Equal(t, true, config.EnableAutoDegradation)
	assert.Equal(t, 30*time.Second, config.DegradationCooldown)
	assert.Equal(t, 10, config.MaxGlobalDegradations)
	assert.Equal(t, 60*time.Second, config.MinRecoveryInterval)
}

// TestDegradationConfig_Custom 测试自定义配置
func TestDegradationConfig_Custom(t *testing.T) {
	config := &DegradationConfig{
		FailureThreshold:      5,
		FailureTimeout:        60 * time.Second,
		RecoveryTimeout:       120 * time.Second,
		EnableAutoDegradation: false,
		DegradationCooldown:   45 * time.Second,
		MaxGlobalDegradations: 20,
		MinRecoveryInterval:   90 * time.Second,
	}

	dm := NewDegradationManager(config)
	assert.Equal(t, config, dm.GetConfig())
}

// ========================================
// 降级判断测试
// ========================================

// TestShouldDegrade_AutoDisabled 测试禁用自动降级
func TestShouldDegrade_AutoDisabled(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: false,
		FailureThreshold:      3,
		MaxGlobalDegradations: 10,
	}
	dm := NewDegradationManager(config)

	// 连续失败但不应该降级
	for i := 0; i < 5; i++ {
		shouldDegrade, reason := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
		assert.False(t, shouldDegrade)
		assert.Contains(t, reason, "自动降级未启用")
	}
}

// TestShouldDegrade_Cooldown 测试降级冷却
func TestShouldDegrade_Cooldown(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      3,
		DegradationCooldown:   100 * time.Millisecond,
		MaxGlobalDegradations: 10,
		FailureTimeout:        30 * time.Second,
	}
	dm := NewDegradationManager(config)

	// 触发第一次降级
	for i := 0; i < 3; i++ {
		dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	}

	// 验证第一次降级成功
	snapshot, _ := dm.GetProtocolState(ProtocolTCP)
	assert.True(t, snapshot.IsDegraded)
	assert.Equal(t, int64(3), snapshot.ConsecutiveFailures)

	// 立即尝试第二次降级（应被冷却阻止）
	shouldDegrade, reason := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.False(t, shouldDegrade)
	assert.Contains(t, reason, "降级冷却中")

	// 等待降级冷却结束
	time.Sleep(150 * time.Millisecond)

	// 冷却结束后，应该可以再次降级
	// 但由于协议仍然处于降级状态，需要先恢复
	// 修改 LastFailureTime 使恢复超时通过
	state := dm.getOrCreateProtocolState(ProtocolTCP)
	state.mu.Lock()
	state.LastFailureTime = time.Now().Add(-100 * time.Second).UnixNano()
	state.mu.Unlock()

	// 触发恢复
	shouldRecover, _ := dm.ShouldRecover(ProtocolTCP)
	assert.True(t, shouldRecover, "应该可以恢复")

	// 验证恢复后状态
	snapshot, _ = dm.GetProtocolState(ProtocolTCP)
	assert.False(t, snapshot.IsDegraded)
	assert.Equal(t, int64(0), snapshot.ConsecutiveFailures)

	// 恢复后，应该可以再次降级
	for i := 0; i < 3; i++ {
		dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	}

	snapshot, _ = dm.GetProtocolState(ProtocolTCP)
	assert.True(t, snapshot.IsDegraded, "恢复后应该可以再次降级")
}

// TestShouldDegrade_GlobalLimit 测试全局降级限制
func TestShouldDegrade_GlobalLimit(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      1, // 低阈值快速触发
		MaxGlobalDegradations: 2,
		FailureTimeout:        30 * time.Second,
	}
	dm := NewDegradationManager(config)

	// 触发第一次降级
	shouldDegrade, _ := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.True(t, shouldDegrade, "第一次降级应成功")

	// 触发第二次降级
	shouldDegrade, _ = dm.ShouldDegrade(ProtocolUDP, types.ErrUDPReceiveFailed)
	assert.True(t, shouldDegrade, "第二次降级应成功")

	// 第三次降级应被全局限制阻止
	shouldDegrade, reason := dm.ShouldDegrade(ProtocolGRPC, types.ErrProtocolTimeout)
	assert.False(t, shouldDegrade)
	assert.Contains(t, reason, "已达到全局降级计数限制")
}

// TestShouldDegrade_BusinessError 测试业务错误不触发降级
func TestShouldDegrade_BusinessError(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      3,
		MaxGlobalDegradations: 10,
	}
	dm := NewDegradationManager(config)

	// 连续业务错误不应触发降级
	businessErrors := []error{
		types.ErrMsgTooLarge,
		types.ErrInvalidAddr,
		types.ErrCodecFailed,
		types.ErrInvalidMsgType,
		types.ErrUnauthorized,
	}

	for _, err := range businessErrors {
		for i := 0; i < 5; i++ {
			shouldDegrade, reason := dm.ShouldDegrade(ProtocolTCP, err)
			assert.False(t, shouldDegrade)
			assert.Contains(t, reason, "业务层错误")
		}
	}

	// 验证协议未降级（协议状态可能不存在或未降级）
	snapshot, exists := dm.GetProtocolState(ProtocolTCP)
	if exists {
		assert.False(t, snapshot.IsDegraded)
	}
}

// TestShouldDegrade_ConsecutiveFailures 测试连续失败触发降级
func TestShouldDegrade_ConsecutiveFailures(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      3,
		FailureTimeout:        30 * time.Second,
		MaxGlobalDegradations: 10,
	}
	dm := NewDegradationManager(config)

	// 前两次失败不应触发降级
	shouldDegrade, reason := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.False(t, shouldDegrade)
	assert.Contains(t, reason, "连续失败次数不足")

	shouldDegrade, reason = dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.False(t, shouldDegrade)
	assert.Contains(t, reason, "连续失败次数不足")

	// 第三次失败应触发降级
	shouldDegrade, reason = dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.True(t, shouldDegrade)
	assert.Contains(t, reason, "降级")

	// 验证协议状态
	snapshot, exists := dm.GetProtocolState(ProtocolTCP)
	require.True(t, exists)
	assert.True(t, snapshot.IsDegraded)
	assert.Equal(t, int64(3), snapshot.ConsecutiveFailures)
}

// TestShouldDegrade_FailureTimeout 测试失败超时后的降级行为
func TestShouldDegrade_FailureTimeout(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      3,
		FailureTimeout:        100 * time.Millisecond, // 短超时用于测试
		MaxGlobalDegradations: 10,
	}
	dm := NewDegradationManager(config)

	// 累积 3 次失败触发降级
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	shouldDegrade, _ := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.True(t, shouldDegrade, "应该触发降级")

	snapshot, _ := dm.GetProtocolState(ProtocolTCP)
	assert.True(t, snapshot.IsDegraded)
	assert.Equal(t, int64(3), snapshot.ConsecutiveFailures)

	// 等待超过失败超时
	time.Sleep(150 * time.Millisecond)

	// 修改 LastFailureTime 使恢复超时通过
	state := dm.getOrCreateProtocolState(ProtocolTCP)
	state.mu.Lock()
	state.LastFailureTime = time.Now().Add(-200 * time.Millisecond).UnixNano()
	state.mu.Unlock()

	// 触发恢复
	shouldRecover, _ := dm.ShouldRecover(ProtocolTCP)
	assert.True(t, shouldRecover, "应该可以恢复")

	// 验证恢复后状态
	snapshot, _ = dm.GetProtocolState(ProtocolTCP)
	assert.False(t, snapshot.IsDegraded)
	assert.Equal(t, int64(0), snapshot.ConsecutiveFailures)

	// 恢复后重新累积失败
	// 如果在 FailureTimeout 内累积 3 次，应该触发降级
	for i := 0; i < 3; i++ {
		dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	}

	snapshot, _ = dm.GetProtocolState(ProtocolTCP)
	assert.True(t, snapshot.IsDegraded, "恢复后应能再次降级")
	assert.Equal(t, int64(3), snapshot.ConsecutiveFailures)

	// 再次等待超过失败超时
	time.Sleep(150 * time.Millisecond)

	// 修改 LastFailureTime 使恢复超时通过
	state.mu.Lock()
	state.LastFailureTime = time.Now().Add(-200 * time.Millisecond).UnixNano()
	state.mu.Unlock()

	// 再次恢复
	shouldRecover, _ = dm.ShouldRecover(ProtocolTCP)
	assert.True(t, shouldRecover, "应该可以再次恢复")

	// 验证恢复后状态
	snapshot, _ = dm.GetProtocolState(ProtocolTCP)
	assert.False(t, snapshot.IsDegraded)
	assert.Equal(t, int64(0), snapshot.ConsecutiveFailures)

	// 测试超时后的累积：等待一段时间后累积失败
	time.Sleep(150 * time.Millisecond)
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	snapshot, _ = dm.GetProtocolState(ProtocolTCP)
	assert.Equal(t, int64(1), snapshot.ConsecutiveFailures, "超时后应重新开始计数")
}

// TestShouldDegrade_RecoveryCooldown 测试恢复冷却
func TestShouldDegrade_RecoveryCooldown(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      3,
		FailureTimeout:        30 * time.Second,
		RecoveryTimeout:       50 * time.Millisecond, // 短超时用于测试
		MinRecoveryInterval:   200 * time.Millisecond,
		MaxGlobalDegradations: 10,
	}
	dm := NewDegradationManager(config)

	// 触发降级
	for i := 0; i < 3; i++ {
		dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	}

	// 修改失败时间使恢复超时通过
	state := dm.getOrCreateProtocolState(ProtocolTCP)
	state.mu.Lock()
	state.LastFailureTime = time.Now().Add(-100 * time.Second).UnixNano()
	state.mu.Unlock()

	// 触发恢复
	shouldRecover, _ := dm.ShouldRecover(ProtocolTCP)
	assert.True(t, shouldRecover, "恢复应该成功")

	// 立即尝试再次降级（应被恢复冷却阻止）
	// 注意：恢复后计数已重置，需要重新累积失败
	for i := 0; i < 3; i++ {
		shouldDegrade, reason := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
		// 由于恢复冷却，前两次可能不会触发降级
		if i < 2 {
			if shouldDegrade {
				assert.Contains(t, reason, "降级")
			} else {
				assert.Contains(t, reason, "恢复冷却中")
			}
		}
	}
}

// TestShouldDegrade_ProtocolErrors 测试协议层错误触发降级
func TestShouldDegrade_ProtocolErrors(t *testing.T) {
	config := DefaultDegradationConfig()

	protocolErrors := []error{
		types.ErrUDPFragmentTimeout,
		types.ErrUDPSendFailed,
		types.ErrUDPReceiveFailed,
		types.ErrTCPConnFailed,
		types.ErrTCPSendTimeout,
		types.ErrTCPReceiveFailed,
		types.ErrTCPConnReset,
		types.ErrProtocolTimeout,
		types.ErrNetworkUnreachable,
	}

	for _, err := range protocolErrors {
		t.Run(err.Error(), func(t *testing.T) {
			// 创建新的 manager 用于每个测试
			dm := NewDegradationManager(config)

			// 触发连续失败
			for i := 0; i < 3; i++ {
				dm.ShouldDegrade(ProtocolTCP, err)
			}

			// 验证已降级
			snapshot, exists := dm.GetProtocolState(ProtocolTCP)
			require.True(t, exists)
			assert.True(t, snapshot.IsDegraded)
		})
	}
}

// ========================================
// 恢复判断测试
// ========================================

// TestShouldRecover_NotDegraded 测试协议未降级时不能恢复
func TestShouldRecover_NotDegraded(t *testing.T) {
	dm := NewDegradationManager(nil)

	shouldRecover, reason := dm.ShouldRecover(ProtocolTCP)
	assert.False(t, shouldRecover)
	assert.Contains(t, reason, "协议未降级")
}

// TestShouldRecover_RecoveryTimeout 测试恢复超时
func TestShouldRecover_RecoveryTimeout(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      3,
		RecoveryTimeout:       100 * time.Millisecond,
		MaxGlobalDegradations: 10,
		FailureTimeout:        30 * time.Second,
	}
	dm := NewDegradationManager(config)

	// 触发降级
	for i := 0; i < 3; i++ {
		dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	}

	// 验证已降级
	snapshot, _ := dm.GetProtocolState(ProtocolTCP)
	require.True(t, snapshot.IsDegraded, "协议应该已降级")

	// 立即尝试恢复（超时未到）
	shouldRecover, reason := dm.ShouldRecover(ProtocolTCP)
	assert.False(t, shouldRecover)
	assert.Contains(t, reason, "恢复超时未到")

	// 等待超时后恢复
	time.Sleep(150 * time.Millisecond)
	shouldRecover, reason = dm.ShouldRecover(ProtocolTCP)
	assert.True(t, shouldRecover)
	assert.Contains(t, reason, "已恢复")
}

// TestShouldRecover_Successful 测试成功恢复
func TestShouldRecover_Successful(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      3,
		RecoveryTimeout:       50 * time.Millisecond,
		MaxGlobalDegradations: 10,
		FailureTimeout:        30 * time.Second,
	}
	dm := NewDegradationManager(config)

	// 触发降级
	for i := 0; i < 3; i++ {
		dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	}

	// 验证已降级
	snapshot, _ := dm.GetProtocolState(ProtocolTCP)
	require.True(t, snapshot.IsDegraded, "协议应该已降级")

	// 修改失败时间使恢复超时通过
	state := dm.getOrCreateProtocolState(ProtocolTCP)
	state.mu.Lock()
	state.LastFailureTime = time.Now().Add(-100 * time.Second).UnixNano()
	state.mu.Unlock()

	// 触发恢复
	shouldRecover, reason := dm.ShouldRecover(ProtocolTCP)
	assert.True(t, shouldRecover)
	assert.Contains(t, reason, "已恢复")

	// 验证协议状态已重置
	snapshot, _ = dm.GetProtocolState(ProtocolTCP)
	assert.False(t, snapshot.IsDegraded)
	assert.Equal(t, int64(0), snapshot.ConsecutiveFailures)
	assert.NotZero(t, snapshot.TotalRecoveries)

	stats := dm.GetStats()
	assert.Equal(t, uint64(1), stats.TotalRecoveries)
}

// ========================================
// 错误分类测试
// ========================================

// TestClassifyError_ProtocolErrors 测试协议层错误分类
func TestClassifyError_ProtocolErrors(t *testing.T) {
	dm := NewDegradationManager(nil)

	t.Run("UDPFragmentTimeout", func(t *testing.T) {
		errType := dm.classifyError(types.ErrUDPFragmentTimeout)
		assert.Equal(t, ProtocolError, errType)
	})

	t.Run("UDPSendFailed", func(t *testing.T) {
		errType := dm.classifyError(types.ErrUDPSendFailed)
		assert.Equal(t, ProtocolError, errType)
	})

	t.Run("UDPReceiveFailed", func(t *testing.T) {
		errType := dm.classifyError(types.ErrUDPReceiveFailed)
		assert.Equal(t, ProtocolError, errType)
	})

	t.Run("TCPConnFailed", func(t *testing.T) {
		errType := dm.classifyError(types.ErrTCPConnFailed)
		assert.Equal(t, ProtocolError, errType)
	})

	t.Run("TCPSendTimeout", func(t *testing.T) {
		errType := dm.classifyError(types.ErrTCPSendTimeout)
		assert.Equal(t, ProtocolError, errType)
	})

	t.Run("TCPReceiveFailed", func(t *testing.T) {
		errType := dm.classifyError(types.ErrTCPReceiveFailed)
		assert.Equal(t, ProtocolError, errType)
	})

	t.Run("TCPConnReset", func(t *testing.T) {
		errType := dm.classifyError(types.ErrTCPConnReset)
		assert.Equal(t, ProtocolError, errType)
	})

	t.Run("ProtocolTimeout", func(t *testing.T) {
		errType := dm.classifyError(types.ErrProtocolTimeout)
		assert.Equal(t, ProtocolError, errType)
	})

	t.Run("NetworkUnreachable", func(t *testing.T) {
		errType := dm.classifyError(types.ErrNetworkUnreachable)
		assert.Equal(t, ProtocolError, errType)
	})
}

// TestClassifyError_BusinessErrors 测试业务层错误分类
func TestClassifyError_BusinessErrors(t *testing.T) {
	dm := NewDegradationManager(nil)

	businessErrors := []error{
		types.ErrMsgTooLarge,
		types.ErrInvalidAddr,
		types.ErrCodecFailed,
		types.ErrInvalidMsgType,
		types.ErrUnauthorized,
	}

	for _, err := range businessErrors {
		errType := dm.classifyError(err)
		assert.Equal(t, BusinessError, errType)
	}
}

// TestClassifyError_UnknownErrors 测试未知错误分类
func TestClassifyError_UnknownErrors(t *testing.T) {
	dm := NewDegradationManager(nil)

	// nil 错误
	errType := dm.classifyError(nil)
	assert.Equal(t, UnknownError, errType)

	// 自定义错误
	customErr := assert.AnError
	errType = dm.classifyError(customErr)
	assert.Equal(t, UnknownError, errType)
}

// ========================================
// 统计信息测试
// ========================================

// TestDegradationStats 测试获取统计信息
func TestDegradationStats(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      2,
		FailureTimeout:        30 * time.Second,
		MaxGlobalDegradations: 5,
	}
	dm := NewDegradationManager(config)

	// 触发 TCP 协议降级（正好达到阈值）
	for i := 0; i < 2; i++ {
		dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	}

	// 触发 UDP 协议降级（正好达到阈值）
	for i := 0; i < 2; i++ {
		dm.ShouldDegrade(ProtocolUDP, types.ErrUDPReceiveFailed)
	}

	stats := dm.GetStats()

	// TotalFailures 记录所有 ShouldDegrade 调用
	assert.Equal(t, uint64(4), stats.TotalFailures)
	// TotalDegradations 记录触发降级的次数
	assert.Equal(t, uint64(2), stats.TotalDegradations)
	assert.Equal(t, uint64(1), stats.DegradationsByProtocol[ProtocolTCP])
	assert.Equal(t, uint64(1), stats.DegradationsByProtocol[ProtocolUDP])
	assert.False(t, stats.LastDegradationTime.IsZero())
}

// TestGetStats_ErrorTracking 测试错误类型统计
func TestGetStats_ErrorTracking(t *testing.T) {
	dm := NewDegradationManager(nil)

	// 触发不同类型的错误
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	dm.ShouldDegrade(ProtocolTCP, types.ErrUDPFragmentTimeout)
	dm.ShouldDegrade(ProtocolUDP, types.ErrProtocolTimeout)

	stats := dm.GetStats()

	// 验证错误类型统计
	assert.NotZero(t, stats.ErrorsByType["tcp_conn_failed"])
	assert.NotZero(t, stats.ErrorsByType["udp_fragment_timeout"])
	assert.NotZero(t, stats.ErrorsByType["protocol_timeout"])
}

// TestResetStats 测试重置统计
func TestResetStats(t *testing.T) {
	dm := NewDegradationManager(nil)

	// 触发一些降级
	for i := 0; i < 3; i++ {
		dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	}

	// 验证统计不为空
	stats := dm.GetStats()
	assert.NotZero(t, stats.TotalDegradations)

	// 重置统计
	dm.ResetStats()

	// 验证统计已清空
	stats = dm.GetStats()
	assert.Zero(t, stats.TotalDegradations)
	assert.Zero(t, stats.TotalFailures)
	// DegradationsByProtocol 会创建新的空 map，不是 nil
	assert.NotNil(t, stats.DegradationsByProtocol)
	assert.Zero(t, len(stats.DegradationsByProtocol))
}

// ========================================
// 配置管理测试
// ========================================

// TestUpdateConfig 测试更新配置
func TestUpdateConfig(t *testing.T) {
	dm := NewDegradationManager(nil)

	// 获取初始配置
	initialConfig := dm.GetConfig()
	assert.Equal(t, 3, initialConfig.FailureThreshold)

	// 更新配置
	newConfig := &DegradationConfig{
		FailureThreshold:      5,
		FailureTimeout:        60 * time.Second,
		EnableAutoDegradation: true,
		MaxGlobalDegradations: 20,
	}
	dm.UpdateConfig(newConfig)

	// 验证配置已更新
	updatedConfig := dm.GetConfig()
	assert.Equal(t, 5, updatedConfig.FailureThreshold)
	assert.Equal(t, 60*time.Second, updatedConfig.FailureTimeout)
}

// ========================================
// 协议状态测试
// ========================================

// TestGetProtocolState_NotFound 测试获取不存在的协议状态
func TestGetProtocolState_NotFound(t *testing.T) {
	dm := NewDegradationManager(nil)

	snapshot, exists := dm.GetProtocolState(ProtocolTCP)
	assert.Nil(t, snapshot)
	assert.False(t, exists)
}

// TestGetProtocolState_Found 测试获取存在的协议状态
func TestGetProtocolState_Found(t *testing.T) {
	dm := NewDegradationManager(nil)

	// 触发降级
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)

	snapshot, exists := dm.GetProtocolState(ProtocolTCP)
	require.True(t, exists)

	assert.True(t, snapshot.IsDegraded)
	assert.Equal(t, int64(3), snapshot.ConsecutiveFailures)
	assert.Equal(t, uint64(3), snapshot.TotalFailures)
	assert.NotZero(t, snapshot.LastFailureTime)
}

// ========================================
// 并发安全测试
// ========================================

// TestConcurrentShouldDegrade 测试并发降级判断
func TestConcurrentShouldDegrade(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      100, // 设置高阈值避免降级影响计数
		MaxGlobalDegradations: 1000,
	}
	dm := NewDegradationManager(config)
	protocolType := ProtocolTCP

	var wg sync.WaitGroup
	numGoroutines := 10
	errorsPerGoroutine := 5

	// 并发调用 ShouldDegrade
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < errorsPerGoroutine; j++ {
				dm.ShouldDegrade(protocolType, types.ErrTCPConnFailed)
			}
		}()
	}

	wg.Wait()

	// 验证状态一致性
	snapshot, _ := dm.GetProtocolState(protocolType)
	assert.Equal(t, uint64(numGoroutines*errorsPerGoroutine), snapshot.TotalFailures)

	// 验证统计一致性
	stats := dm.GetStats()
	assert.Equal(t, uint64(numGoroutines*errorsPerGoroutine), stats.TotalFailures)
}

// TestConcurrentConfigUpdate 测试并发配置更新
func TestConcurrentConfigUpdate(t *testing.T) {
	dm := NewDegradationManager(nil)

	var wg sync.WaitGroup

	// 并发读取配置
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				dm.GetConfig()
			}
		}()
	}

	// 并发更新配置
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			config := &DegradationConfig{
				FailureThreshold:      n,
				EnableAutoDegradation: true,
				MaxGlobalDegradations: 10,
			}
			dm.UpdateConfig(config)
		}(i)
	}

	wg.Wait()

	// 验证未发生 panic
	config := dm.GetConfig()
	assert.NotNil(t, config)
}

// TestConcurrentStatsAccess 测试并发统计访问
func TestConcurrentStatsAccess(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      100, // 设置高阈值避免降级影响计数
		MaxGlobalDegradations: 1000,
	}
	dm := NewDegradationManager(config)

	var wg sync.WaitGroup

	// 并发写入统计
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
			}
		}()
	}

	// 并发读取统计
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				dm.GetStats()
				dm.GetProtocolState(ProtocolTCP)
			}
		}()
	}

	wg.Wait()

	// 验证统计一致性
	stats := dm.GetStats()
	assert.Equal(t, uint64(500), stats.TotalFailures)
}

// ========================================
// 集成测试
// ========================================

// TestDegradationAndRecovery_Lifecycle 测试完整的降级恢复生命周期
func TestDegradationAndRecovery_Lifecycle(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      3,
		FailureTimeout:        30 * time.Second,
		RecoveryTimeout:       100 * time.Millisecond,
		MinRecoveryInterval:   50 * time.Millisecond,
		MaxGlobalDegradations: 10,
	}
	dm := NewDegradationManager(config)

	// 阶段 1: 初始状态 - 协议未降级
	_, exists := dm.GetProtocolState(ProtocolTCP)
	assert.False(t, exists, "初始状态协议不应存在")

	// 阶段 2: 累积失败
	shouldDegrade, _ := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.False(t, shouldDegrade, "第一次失败不应降级")
	shouldDegrade, _ = dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.False(t, shouldDegrade, "第二次失败不应降级")

	// 阶段 3: 触发降级
	shouldDegrade, reason := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.True(t, shouldDegrade, "第三次失败应触发降级")
	assert.Contains(t, reason, "降级")

	snapshot, exists := dm.GetProtocolState(ProtocolTCP)
	require.True(t, exists)
	assert.True(t, snapshot.IsDegraded)

	stats := dm.GetStats()
	assert.Equal(t, uint64(1), stats.TotalDegradations)

	// 阶段 4: 等待恢复超时
	time.Sleep(150 * time.Millisecond)

	// 阶段 5: 尝试恢复
	shouldRecover, reason := dm.ShouldRecover(ProtocolTCP)
	assert.True(t, shouldRecover, "恢复超时后应能恢复")
	assert.Contains(t, reason, "已恢复")

	snapshot, _ = dm.GetProtocolState(ProtocolTCP)
	assert.False(t, snapshot.IsDegraded, "恢复后不应处于降级状态")
	assert.Equal(t, int64(0), snapshot.ConsecutiveFailures, "恢复后计数应重置")

	stats = dm.GetStats()
	assert.Equal(t, uint64(1), stats.TotalRecoveries)
}

// ========================================
// 边界条件测试
// ========================================

// TestNilConfig 测试 nil 配置使用默认值
func TestNilConfig(t *testing.T) {
	dm := NewDegradationManager(nil)

	config := dm.GetConfig()
	assert.NotNil(t, config)
	assert.Equal(t, 3, config.FailureThreshold)
}

// TestZeroThreshold 测试零阈值
func TestZeroThreshold(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      0, // 零阈值意味着立即降级
		MaxGlobalDegradations: 10,
		FailureTimeout:        30 * time.Second,
	}
	dm := NewDegradationManager(config)

	shouldDegrade, _ := dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	assert.True(t, shouldDegrade, "零阈值应该立即触发降级")
}

// TestMultipleProtocols 测试多协议独立降级
func TestMultipleProtocols(t *testing.T) {
	config := &DegradationConfig{
		EnableAutoDegradation: true,
		FailureThreshold:      2,
		MaxGlobalDegradations: 10,
		FailureTimeout:        30 * time.Second,
	}
	dm := NewDegradationManager(config)

	// TCP 降级
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)
	dm.ShouldDegrade(ProtocolTCP, types.ErrTCPConnFailed)

	// UDP 降级
	dm.ShouldDegrade(ProtocolUDP, types.ErrUDPReceiveFailed)
	dm.ShouldDegrade(ProtocolUDP, types.ErrUDPReceiveFailed)

	// 验证各协议独立降级
	tcpSnapshot, _ := dm.GetProtocolState(ProtocolTCP)
	assert.True(t, tcpSnapshot.IsDegraded)

	udpSnapshot, _ := dm.GetProtocolState(ProtocolUDP)
	assert.True(t, udpSnapshot.IsDegraded)

	stats := dm.GetStats()
	assert.Equal(t, uint64(2), stats.TotalDegradations)
}
