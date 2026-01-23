// Package transport 降级机制
//
// 实现协议层/业务层错误区分和精细化降级触发
package transport

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

const (
	statTotalFailure            = "total_failure"
	statCooldownSkipped         = "cooldown_skipped"
	statGlobalLimitReached      = "global_limit_reached"
	statRecoveryCooldownSkipped = "recovery_cooldown_skipped"
	statBusinessErrorsSkipped   = "business_errors_skipped"
	statInsufficientFailures    = "insufficient_failures"
)

// 错误类型和错误常量已移至 types/errors.go
// 为了向后兼容，重新导出类型别名
type ErrorType = types.ErrorType

const (
	ProtocolError = types.ProtocolError // 协议层错误（触发降级）
	BusinessError = types.BusinessError // 业务层错误（不触发降级）
	UnknownError  = types.UnknownError  // 未知错误（保守策略，触发降级）
)

// DegradationConfig 降级配置
type DegradationConfig struct {
	FailureThreshold      int           // 失败阈值（连续失败次数）
	FailureTimeout        time.Duration // 失败超时（从最后一次失败开始计算）
	RecoveryTimeout       time.Duration // 恢复超时（协议恢复后等待时间）
	EnableAutoDegradation bool          // 启用自动降级
	DegradationCooldown   time.Duration // 降级冷却时间（避免频繁降级）
	MaxGlobalDegradations int           // 全局降级计数限制（防止过度降级）
	MinRecoveryInterval   time.Duration // 最小恢复间隔（防止频繁恢复）
}

// DefaultDegradationConfig 返回默认降级配置
func DefaultDegradationConfig() *DegradationConfig {
	return &DegradationConfig{
		FailureThreshold:      3,                // 连续失败 3 次触发降级
		FailureTimeout:        30 * time.Second, // 30 秒内失败算连续
		RecoveryTimeout:       60 * time.Second, // 60 秒后尝试恢复
		EnableAutoDegradation: true,
		DegradationCooldown:   30 * time.Second, // 30 秒冷却时间（从 10s 增加到 30s）
		MaxGlobalDegradations: 10,               // 全局降级计数限制
		MinRecoveryInterval:   60 * time.Second, // 最小恢复间隔
	}
}

// DegradationManager 降级管理器
//
// 管理协议降级逻辑：监控协议失败、触发降级、协议恢复
type DegradationManager struct {
	config              *DegradationConfig              // 配置
	configMu            sync.RWMutex                    // 配置锁
	protocolStates      map[ProtocolType]*ProtocolState // 协议状态
	protocolStatesMu    sync.RWMutex                    // 协议状态锁
	stats               *DegradationStats               // 统计信息
	statsMu             sync.RWMutex                    // 统计锁
	lastDegradationTime atomic.Int64                    // 降级冷却（纳秒时间戳）
	lastRecoveryTime    atomic.Int64                    // 恢复冷却（纳秒时间戳）
}

// ProtocolState 协议状态
//
// 使用互斥锁保护状态更新，确保批量更新的原子性
type ProtocolState struct {
	mu                  sync.RWMutex // 状态锁
	IsDegraded          bool         // 是否已降级
	ConsecutiveFailures int64        // 连续失败次数
	LastFailureTime     int64        // 最后一次失败时间（纳秒时间戳）
	LastRecoveryTime    int64        // 最后一次恢复时间（纳秒时间戳）
	TotalFailures       uint64       // 总失败次数
	TotalRecoveries     uint64       // 总恢复次数
}

// recordFailure 批量更新失败状态（原子操作）
func (s *ProtocolState) recordFailure(now int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ConsecutiveFailures++
	s.TotalFailures++
	s.LastFailureTime = now
}

// recordRecovery 批量更新恢复状态（原子操作）
func (s *ProtocolState) recordRecovery(now int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.IsDegraded = false
	s.ConsecutiveFailures = 0
	s.LastRecoveryTime = now
	s.TotalRecoveries++
}

// setIsDegraded 设置降级状态
func (s *ProtocolState) setIsDegraded(degraded bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IsDegraded = degraded
}

// getSnapshot 获取状态快照（用于外部读取）
func (s *ProtocolState) getSnapshot() ProtocolStateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return ProtocolStateSnapshot{
		IsDegraded:          s.IsDegraded,
		ConsecutiveFailures: s.ConsecutiveFailures,
		LastFailureTime:     s.LastFailureTime,
		LastRecoveryTime:    s.LastRecoveryTime,
		TotalFailures:       s.TotalFailures,
		TotalRecoveries:     s.TotalRecoveries,
	}
}

// ProtocolStateSnapshot 协议状态快照（不可变）
type ProtocolStateSnapshot struct {
	IsDegraded          bool
	ConsecutiveFailures int64
	LastFailureTime     int64
	LastRecoveryTime    int64
	TotalFailures       uint64
	TotalRecoveries     uint64
}

// DegradationStats 降级统计
type DegradationStats struct {
	TotalDegradations       uint64                  // 总降级次数
	TotalRecoveries         uint64                  // 总恢复次数
	DegradationsByProtocol  map[ProtocolType]uint64 // 按协议统计降级次数
	ErrorsByType            map[string]uint64       // 按错误类型统计
	LastDegradationTime     time.Time               // 最后一次降级时间
	LastRecoveryTime        time.Time               // 最后一次恢复时间
	TotalFailures           uint64                  // 总失败次数（所有协议）
	CooldownSkipped         uint64                  // 冷却期跳过次数
	GlobalLimitReached      uint64                  // 全局限制达到次数
	RecoveryCooldownSkipped uint64                  // 恢复冷却跳过次数
	BusinessErrorsSkipped   uint64                  // 业务错误跳过次数
	InsufficientFailures    uint64                  // 失败次数不足跳过次数
}

// NewDegradationManager 创建降级管理器
func NewDegradationManager(config *DegradationConfig) *DegradationManager {
	if config == nil {
		config = DefaultDegradationConfig()
	}

	return &DegradationManager{
		config:         config,
		protocolStates: make(map[ProtocolType]*ProtocolState),
		stats: &DegradationStats{
			DegradationsByProtocol: make(map[ProtocolType]uint64),
			ErrorsByType:           make(map[string]uint64),
		},
	}
}

// ShouldDegrade 判断是否应该降级
//
// 参数:
//   - protocolType: 协议类型
//   - err: 错误信息
//
// 返回:
//   - shouldDegrade: 是否应该降级
//   - reason: 降级原因
func (dm *DegradationManager) ShouldDegrade(
	protocolType ProtocolType,
	err error,
) (shouldDegrade bool, reason string) {
	dm.configMu.RLock()
	config := dm.config
	dm.configMu.RUnlock()

	// 检查是否启用自动降级
	if !config.EnableAutoDegradation {
		return false, "自动降级未启用"
	}

	// 检查降级冷却
	if dm.isInCooldown() {
		dm.recordStat(statCooldownSkipped)
		return false, "降级冷却中"
	}

	// 检查全局降级计数限制
	dm.statsMu.RLock()
	globalDegradations := dm.stats.TotalDegradations
	dm.statsMu.RUnlock()

	if globalDegradations >= uint64(config.MaxGlobalDegradations) {
		dm.recordStat(statGlobalLimitReached)
		return false, fmt.Sprintf("已达到全局降级计数限制 (%d/%d)",
			globalDegradations, config.MaxGlobalDegradations)
	}

	// 检查恢复冷却（防止频繁恢复后立即降级）
	if config.MinRecoveryInterval > 0 {
		lastRecovery := dm.lastRecoveryTime.Load()
		if lastRecovery > 0 {
			lastRecoveryTime := time.Unix(0, lastRecovery)
			if time.Since(lastRecoveryTime) < config.MinRecoveryInterval {
				dm.recordStat(statRecoveryCooldownSkipped)
				return false, fmt.Sprintf("恢复冷却中（距上次恢复 %v/%v）",
					time.Since(lastRecoveryTime), config.MinRecoveryInterval)
			}
		}
	}

	// 分类错误
	errorType := dm.classifyError(err)

	// 业务层错误不触发降级
	if errorType == BusinessError {
		dm.recordStat(statBusinessErrorsSkipped)
		return false, "业务层错误不触发降级"
	}

	// 获取或创建协议状态
	state := dm.getOrCreateProtocolState(protocolType)

	// 批量更新失败状态（原子操作）
	now := time.Now().UnixNano()
	state.recordFailure(now)

	// 记录总失败次数
	dm.recordStat(statTotalFailure)

	// 记录错误统计
	dm.recordError(err)

	// 检查是否达到降级阈值
	snapshot := state.getSnapshot()
	if snapshot.ConsecutiveFailures >= int64(config.FailureThreshold) {
		// 检查失败超时
		lastFailure := time.Unix(0, snapshot.LastFailureTime)
		if time.Since(lastFailure) <= config.FailureTimeout {
			// 触发降级
			return dm.triggerDegradation(protocolType, err)
		}

		// 超时后重置计数
		state.mu.Lock()
		state.ConsecutiveFailures = 0
		state.mu.Unlock()
	}

	dm.recordStat(statInsufficientFailures)

	snapshot = state.getSnapshot()
	return false, fmt.Sprintf("连续失败次数不足 (%d/%d)",
		snapshot.ConsecutiveFailures, config.FailureThreshold)
}

// ShouldRecover 判断是否应该恢复协议
//
// 参数:
//   - protocolType: 协议类型
//
// 返回:
//   - shouldRecover: 是否应该恢复
//   - reason: 恢复原因
func (dm *DegradationManager) ShouldRecover(
	protocolType ProtocolType,
) (shouldRecover bool, reason string) {
	dm.configMu.RLock()
	config := dm.config
	dm.configMu.RUnlock()

	// 获取协议状态
	dm.protocolStatesMu.RLock()
	state, exists := dm.protocolStates[protocolType]
	dm.protocolStatesMu.RUnlock()

	if !exists {
		return false, "协议未降级"
	}

	// 检查状态快照
	snapshot := state.getSnapshot()
	if !snapshot.IsDegraded {
		return false, "协议未降级"
	}

	// 检查恢复超时
	lastFailure := time.Unix(0, snapshot.LastFailureTime)
	if time.Since(lastFailure) < config.RecoveryTimeout {
		return false, "恢复超时未到"
	}

	// 触发恢复
	return dm.triggerRecovery(protocolType)
}

// classifyError 分类错误
func (dm *DegradationManager) classifyError(err error) ErrorType {
	if err == nil {
		return UnknownError
	}

	// 统一错误分类逻辑（使用 types 包中的工具函数）
	if types.IsProtocolError(err) {
		return ProtocolError
	}

	if types.IsBusinessError(err) {
		return BusinessError
	}

	return UnknownError
}

// getOrCreateProtocolState 获取或创建协议状态
func (dm *DegradationManager) getOrCreateProtocolState(protocolType ProtocolType) *ProtocolState {
	dm.protocolStatesMu.Lock()
	defer dm.protocolStatesMu.Unlock()

	state, exists := dm.protocolStates[protocolType]
	if !exists {
		state = &ProtocolState{}
		dm.protocolStates[protocolType] = state
	}

	return state
}

// isInCooldown 检查是否在冷却期
func (dm *DegradationManager) isInCooldown() bool {
	lastTime := dm.lastDegradationTime.Load()
	if lastTime == 0 {
		return false
	}

	dm.configMu.RLock()
	config := dm.config
	dm.configMu.RUnlock()

	lastDegradation := time.Unix(0, lastTime)
	return time.Since(lastDegradation) < config.DegradationCooldown
}

// triggerDegradation 触发降级
func (dm *DegradationManager) triggerDegradation(
	protocolType ProtocolType,
	err error,
) (bool, string) {
	state := dm.getOrCreateProtocolState(protocolType)
	state.setIsDegraded(true)

	// 更新统计
	dm.statsMu.Lock()
	dm.stats.TotalDegradations++
	dm.stats.DegradationsByProtocol[protocolType]++
	dm.stats.LastDegradationTime = time.Now()
	dm.statsMu.Unlock()

	// 更新降级时间
	dm.lastDegradationTime.Store(time.Now().UnixNano())

	// 记录日志
	errMsg := "unknown error"
	if err != nil {
		errMsg = err.Error()
	}
	snapshot := state.getSnapshot()
	logging.Infof("协议降级: protocol=%v, error=%v, consecutive_failures=%d",
		protocolType, errMsg, snapshot.ConsecutiveFailures)

	return true, fmt.Sprintf("协议 %s 降级（连续失败 %d 次）",
		protocolType, snapshot.ConsecutiveFailures)
}

// triggerRecovery 触发恢复
func (dm *DegradationManager) triggerRecovery(
	protocolType ProtocolType,
) (bool, string) {
	state := dm.getOrCreateProtocolState(protocolType)
	now := time.Now().UnixNano()
	state.recordRecovery(now)

	// 更新最后恢复时间（用于恢复冷却检查）
	dm.lastRecoveryTime.Store(now)

	// 更新统计
	dm.statsMu.Lock()
	dm.stats.TotalRecoveries++
	dm.stats.LastRecoveryTime = time.Now()
	dm.statsMu.Unlock()

	// 记录日志
	snapshot := state.getSnapshot()
	logging.Infof("协议恢复: protocol=%v, total_failures=%d",
		protocolType, snapshot.TotalFailures)

	return true, fmt.Sprintf("协议 %s 已恢复", protocolType)
}

// recordError 记录错误
func (dm *DegradationManager) recordError(err error) {
	if err == nil {
		return
	}

	dm.statsMu.Lock()
	defer dm.statsMu.Unlock()

	errType := getErrorTypeName(err)
	dm.stats.ErrorsByType[errType]++
}

// recordStat 记录统计指标
func (dm *DegradationManager) recordStat(statType string) {
	dm.statsMu.Lock()
	defer dm.statsMu.Unlock()

	switch statType {
	case statTotalFailure:
		dm.stats.TotalFailures++
	case statCooldownSkipped:
		dm.stats.CooldownSkipped++
	case statGlobalLimitReached:
		dm.stats.GlobalLimitReached++
	case statRecoveryCooldownSkipped:
		dm.stats.RecoveryCooldownSkipped++
	case statBusinessErrorsSkipped:
		dm.stats.BusinessErrorsSkipped++
	case statInsufficientFailures:
		dm.stats.InsufficientFailures++
	}
}

// getErrorTypeName 获取错误类型名称
func getErrorTypeName(err error) string {
	errorTypes := map[error]string{
		types.ErrUDPFragmentTimeout: "udp_fragment_timeout",
		types.ErrUDPSendFailed:      "udp_send_failed",
		types.ErrUDPReceiveFailed:   "udp_receive_failed",
		types.ErrTCPConnFailed:      "tcp_conn_failed",
		types.ErrTCPSendTimeout:     "tcp_send_timeout",
		types.ErrTCPReceiveFailed:   "tcp_receive_failed",
		types.ErrTCPConnReset:       "tcp_conn_reset",
		types.ErrProtocolTimeout:    "protocol_timeout",
		types.ErrNetworkUnreachable: "network_unreachable",
	}

	for protoErr, name := range errorTypes {
		if err.Error() == protoErr.Error() {
			return name
		}
	}

	return "unknown"
}

// UpdateConfig 更新降级配置
func (dm *DegradationManager) UpdateConfig(config *DegradationConfig) {
	dm.configMu.Lock()
	defer dm.configMu.Unlock()

	dm.config = config
}

// GetConfig 获取当前配置
func (dm *DegradationManager) GetConfig() *DegradationConfig {
	dm.configMu.RLock()
	defer dm.configMu.RUnlock()

	return dm.config
}

// GetStats 获取降级统计
func (dm *DegradationManager) GetStats() *DegradationStats {
	dm.statsMu.RLock()
	defer dm.statsMu.RUnlock()

	return &DegradationStats{
		TotalDegradations:       dm.stats.TotalDegradations,
		TotalRecoveries:         dm.stats.TotalRecoveries,
		DegradationsByProtocol:  copyMap(dm.stats.DegradationsByProtocol),
		ErrorsByType:            copyMap(dm.stats.ErrorsByType),
		LastDegradationTime:     dm.stats.LastDegradationTime,
		LastRecoveryTime:        dm.stats.LastRecoveryTime,
		TotalFailures:           dm.stats.TotalFailures,
		CooldownSkipped:         dm.stats.CooldownSkipped,
		GlobalLimitReached:      dm.stats.GlobalLimitReached,
		RecoveryCooldownSkipped: dm.stats.RecoveryCooldownSkipped,
		BusinessErrorsSkipped:   dm.stats.BusinessErrorsSkipped,
		InsufficientFailures:    dm.stats.InsufficientFailures,
	}
}

// GetProtocolState 获取协议状态
func (dm *DegradationManager) GetProtocolState(
	protocolType ProtocolType,
) (*ProtocolStateSnapshot, bool) {
	dm.protocolStatesMu.RLock()
	defer dm.protocolStatesMu.RUnlock()

	state, exists := dm.protocolStates[protocolType]
	if !exists {
		return nil, false
	}

	// 返回不可变快照
	snapshot := state.getSnapshot()
	return &snapshot, true
}

// ResetStats 重置统计
func (dm *DegradationManager) ResetStats() {
	dm.statsMu.Lock()
	defer dm.statsMu.Unlock()

	dm.stats = &DegradationStats{
		DegradationsByProtocol: make(map[ProtocolType]uint64),
		ErrorsByType:           make(map[string]uint64),
	}

	dm.protocolStatesMu.Lock()
	defer dm.protocolStatesMu.Unlock()

	dm.protocolStates = make(map[ProtocolType]*ProtocolState)
}

// copyMap 泛型 map 拷贝辅助函数
func copyMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	copy := make(map[K]V, len(m))
	for k, v := range m {
		copy[k] = v
	}
	return copy
}
