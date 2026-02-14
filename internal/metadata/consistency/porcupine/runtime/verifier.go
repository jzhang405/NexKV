// Package runtime 提供 Porcupine 运行时验证器
// 本文件实现运行时验证器，统一管理所有 Hook 的创建和生命周期
package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine"
	"github.com/jzhang405/NexKV/internal/metadata/consistency/porcupine/hooks"
)

// ==================== RuntimeVerifier ====================

// RuntimeVerifier 运行时验证器
// 统一管理所有模块的验证 Hook，提供周期验证和生命周期管理
type RuntimeVerifier struct {
	mu     sync.RWMutex
	config VerifierConfig

	// 共享的 recorder（P1-02 修复）
	recorder *porcupine.EnhancedHistoryRecorder

	// 各模块 Hook（共享 recorder）
	gossipHook      *hooks.GossipHook
	quorumHook      *hooks.QuorumHook
	failureHook     *hooks.FailureHook
	degradationHook *hooks.DegradationHook
	leaderHook      *hooks.LeaderHook

	// 验证结果缓存
	lastResult    *VerificationResult
	resultHistory []VerificationResult

	// 生命周期管理（P1-04 修复）
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRuntimeVerifier 创建运行时验证器
func NewRuntimeVerifier(config VerifierConfig, nodeID string) *RuntimeVerifier {
	ctx, cancel := context.WithCancel(context.Background())

	// 创建共享的 recorder（P1-02 修复）
	recorder := porcupine.NewEnhancedHistoryRecorder("verifier", porcupine.NewMonotonicTimestamp())

	v := &RuntimeVerifier{
		config:        config,
		recorder:      recorder,
		ctx:           ctx,
		cancel:        cancel,
		resultHistory: make([]VerificationResult, 0, config.HistorySize),
	}

	// 初始化各 Hook（共享 recorder）
	v.gossipHook = hooks.NewGossipHook(recorder, nodeID, config.AsyncConfig)
	v.quorumHook = hooks.NewQuorumHook(recorder, nodeID, config.AsyncConfig)
	v.failureHook = hooks.NewFailureHook(recorder, config.AsyncConfig)
	v.degradationHook = hooks.NewDegradationHook(recorder, nodeID, config.AsyncConfig)
	v.leaderHook = hooks.NewLeaderHook(recorder, nodeID, config.AsyncConfig)

	// 设置初始启用状态
	v.initHookStates()

	return v
}

// initHookStates 初始化 Hook 启用状态
func (v *RuntimeVerifier) initHookStates() {
	v.gossipHook.SetEnabled(v.config.GossipEnabled && v.config.Enabled)
	v.quorumHook.SetEnabled(v.config.QuorumEnabled && v.config.Enabled)
	v.failureHook.SetEnabled(v.config.FailureEnabled && v.config.Enabled)
	v.degradationHook.SetEnabled(v.config.DegradationEnabled && v.config.Enabled)
	v.leaderHook.SetEnabled(v.config.LeaderEnabled && v.config.Enabled)
}

// ==================== Hook 访问器 ====================

// GossipHook 返回 Gossip Hook
func (v *RuntimeVerifier) GossipHook() *hooks.GossipHook {
	return v.gossipHook
}

// QuorumHook 返回 Quorum Hook
func (v *RuntimeVerifier) QuorumHook() *hooks.QuorumHook {
	return v.quorumHook
}

// FailureHook 返回 Failure Hook
func (v *RuntimeVerifier) FailureHook() *hooks.FailureHook {
	return v.failureHook
}

// DegradationHook 返回 Degradation Hook
func (v *RuntimeVerifier) DegradationHook() *hooks.DegradationHook {
	return v.degradationHook
}

// LeaderHook 返回 Leader Hook
func (v *RuntimeVerifier) LeaderHook() *hooks.LeaderHook {
	return v.leaderHook
}

// Recorder 返回共享的 recorder
func (v *RuntimeVerifier) Recorder() *porcupine.EnhancedHistoryRecorder {
	return v.recorder
}

// ==================== 验证方法 ====================

// Verify 执行验证（P1-02 修复：使用共享 recorder）
func (v *RuntimeVerifier) Verify() *VerificationResult {
	v.mu.Lock()
	defer v.mu.Unlock()

	start := time.Now()
	result := &VerificationResult{
		Timestamp: start,
	}

	// 从共享 recorder 获取操作并分类
	topologyOps := v.recorder.GetTopologyOperations()
	failureOps := v.recorder.GetFailureRecoveryOperations()
	leaderOps := v.recorder.GetLeaderHAOperations()

	// 验证拓扑感知
	result.TopologyPass, result.TopologyMsg = v.verifyModel(
		len(topologyOps) > 0,
		"no topology operations",
		func() (bool, string) { return porcupine.VerifyTopology(v.recorder) },
	)

	// 验证失败恢复
	result.FailurePass, result.FailureMsg = v.verifyModel(
		len(failureOps) > 0,
		"no failure operations",
		func() (bool, string) { return porcupine.VerifyFailureRecovery(v.recorder) },
	)

	// 验证 Leader HA
	result.LeaderHAPass, result.LeaderHAMsg = v.verifyModel(
		len(leaderOps) > 0,
		"no leader HA operations",
		func() (bool, string) { return porcupine.VerifyLeaderHA(v.recorder) },
	)

	// 计算总操作数
	result.TotalOps = len(topologyOps) + len(failureOps) + len(leaderOps)
	result.Duration = time.Since(start)

	// 更新历史
	v.updateHistory(result)

	// 内存控制（P1-03 修复）
	v.trimRecorderHistory()

	return result
}

// verifyModel 通用的模型验证逻辑（DRY 优化）
type verifyFunc func() (bool, string)

func (v *RuntimeVerifier) verifyModel(hasOps bool, skipMsg string, verify verifyFunc) (bool, string) {
	if !hasOps {
		return true, skipMsg
	}
	return verify()
}

// updateHistory 更新验证历史
func (v *RuntimeVerifier) updateHistory(result *VerificationResult) {
	v.lastResult = result
	v.resultHistory = append(v.resultHistory, *result)
	if len(v.resultHistory) > v.config.HistorySize {
		v.resultHistory = v.resultHistory[1:]
	}
}

// VerifyOnCriticalEvent 关键事件后立即验证（P2-05 建议）
func (v *RuntimeVerifier) VerifyOnCriticalEvent(event string) *VerificationResult {
	return v.Verify()
}

// trimRecorderHistory 清理 recorder 历史（P1-03 修复）
func (v *RuntimeVerifier) trimRecorderHistory() {
	if v.config.MaxOpsPerRecorder > 0 && v.recorder.Len() > v.config.MaxOpsPerRecorder {
		v.recorder.Trim(v.config.MaxOpsPerRecorder)
	}
}

// ==================== 结果访问器 ====================

// GetLastResult 获取最近验证结果
func (v *RuntimeVerifier) GetLastResult() *VerificationResult {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastResult
}

// GetResultHistory 获取验证结果历史
func (v *RuntimeVerifier) GetResultHistory() []VerificationResult {
	v.mu.RLock()
	defer v.mu.RUnlock()
	result := make([]VerificationResult, len(v.resultHistory))
	copy(result, v.resultHistory)
	return result
}

// ==================== 生命周期管理 ====================

// allHooks 返回所有 Hook 切片（用于批量操作）
func (v *RuntimeVerifier) allHooks() []hooks.VerificationHook {
	return []hooks.VerificationHook{
		v.gossipHook,
		v.quorumHook,
		v.failureHook,
		v.degradationHook,
		v.leaderHook,
	}
}

// Start 启动验证器（包含生命周期管理，P1-04 修复）
func (v *RuntimeVerifier) Start() {
	// 启动所有 Hook 的后台处理
	hookStates := []bool{
		v.config.GossipEnabled,
		v.config.QuorumEnabled,
		v.config.FailureEnabled,
		v.config.DegradationEnabled,
		v.config.LeaderEnabled,
	}

	for i, h := range v.allHooks() {
		if hookStates[i] {
			h.Start()
		}
	}

	// 启动周期验证
	if v.config.VerifyInterval <= 0 {
		return
	}

	v.wg.Add(1)
	go v.periodicVerifyLoop()
}

// periodicVerifyLoop 周期验证循环
func (v *RuntimeVerifier) periodicVerifyLoop() {
	defer v.wg.Done()

	ticker := time.NewTicker(v.config.VerifyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-v.ctx.Done():
			return
		case <-ticker.C:
			v.Verify()
		}
	}
}

// Stop 停止验证器（P1-04 修复：先 Flush 再 Stop）
func (v *RuntimeVerifier) Stop() {
	// 1. 停止周期验证
	if v.cancel != nil {
		v.cancel()
	}

	// 2. 等待所有 goroutine 退出
	v.wg.Wait()

	// 3. 先刷新所有 Hook 的待处理操作，再停止
	for _, h := range v.allHooks() {
		if h != nil {
			h.Flush()
			h.Stop()
		}
	}
}

// ==================== 配置访问器 ====================

// SetEnabled 设置是否启用验证
func (v *RuntimeVerifier) SetEnabled(enabled bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.config.Enabled = enabled
	v.updateAllHookStates()
}

// updateAllHookStates 更新所有 Hook 的启用状态
func (v *RuntimeVerifier) updateAllHookStates() {
	hookConfigs := []struct {
		enabled *bool
		hook    hooks.VerificationHook
	}{
		{&v.config.GossipEnabled, v.gossipHook},
		{&v.config.QuorumEnabled, v.quorumHook},
		{&v.config.FailureEnabled, v.failureHook},
		{&v.config.DegradationEnabled, v.degradationHook},
		{&v.config.LeaderEnabled, v.leaderHook},
	}

	for _, hc := range hookConfigs {
		hc.hook.SetEnabled(*hc.enabled && v.config.Enabled)
	}
}

// SetGossipEnabled 设置 Gossip Hook 是否启用
func (v *RuntimeVerifier) SetGossipEnabled(enabled bool) {
	v.setHookEnabled(&v.config.GossipEnabled, v.gossipHook, enabled)
}

// SetQuorumEnabled 设置 Quorum Hook 是否启用
func (v *RuntimeVerifier) SetQuorumEnabled(enabled bool) {
	v.setHookEnabled(&v.config.QuorumEnabled, v.quorumHook, enabled)
}

// SetFailureEnabled 设置 Failure Hook 是否启用
func (v *RuntimeVerifier) SetFailureEnabled(enabled bool) {
	v.setHookEnabled(&v.config.FailureEnabled, v.failureHook, enabled)
}

// SetDegradationEnabled 设置 Degradation Hook 是否启用
func (v *RuntimeVerifier) SetDegradationEnabled(enabled bool) {
	v.setHookEnabled(&v.config.DegradationEnabled, v.degradationHook, enabled)
}

// SetLeaderEnabled 设置 Leader Hook 是否启用
func (v *RuntimeVerifier) SetLeaderEnabled(enabled bool) {
	v.setHookEnabled(&v.config.LeaderEnabled, v.leaderHook, enabled)
}

// setHookEnabled 通用的 Hook 启用状态设置（DRY 优化）
func (v *RuntimeVerifier) setHookEnabled(configField *bool, hook hooks.VerificationHook, enabled bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	*configField = enabled
	hook.SetEnabled(enabled && v.config.Enabled)
}

// ==================== 统计信息 ====================

// Stats 返回验证器统计信息
func (v *RuntimeVerifier) Stats() VerifierStats {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return VerifierStats{
		TotalOps:          v.recorder.Len(),
		PendingOps:        v.recorder.PendingLen(),
		VerificationCount: len(v.resultHistory),
		GossipStats:       v.gossipHook.Stats(),
		QuorumStats:       v.quorumHook.Stats(),
		FailureStats:      v.failureHook.Stats(),
		DegradationStats:  v.degradationHook.Stats(),
		LeaderStats:       v.leaderHook.Stats(),
	}
}

// VerifierStats 验证器统计信息
type VerifierStats struct {
	TotalOps          int
	PendingOps        int
	VerificationCount int
	GossipStats       hooks.HookStats
	QuorumStats       hooks.HookStats
	FailureStats      hooks.HookStats
	DegradationStats  hooks.HookStats
	LeaderStats       hooks.HookStats
}

// ==================== 工厂函数 ====================

// NewRuntimeVerifierWithDefaults 创建默认配置的验证器
func NewRuntimeVerifierWithDefaults(nodeID string) *RuntimeVerifier {
	return NewRuntimeVerifier(DefaultVerifierConfig(), nodeID)
}
