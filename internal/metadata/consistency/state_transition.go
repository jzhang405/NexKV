// Package consistency 提供 2PC 强一致性协调器实现
//
// P1-2: 状态转换验证
// 定义状态转换规则表，实现 isValidStateTransition 函数
package consistency

import (
	"fmt"
	"sync"

	"github.com/jzhang405/NexKV/internal/clock"
)

// ==================== 状态转换规则表 ====================

// StateTransition 状态转换定义
type StateTransition struct {
	From TransactionState
	To   TransactionState
}

// validTransitions 定义有效的状态转换规则表
//
// 状态机模型：
//
//	Init ─────────────────────────────────────────────────────┐
//	  │                                                        │
//	  ├──→ PreCommit ──→ Committed (最终状态)                   │
//	  │        │                                              │
//	  │        ├──→ RolledBack (最终状态)                      │
//	  │        │                                              │
//	  │        └──→ Timeout ──→ RolledBack (最终状态)          │
//	  │                                                        │
//	  ├──→ RolledBack (直接回滚，最终状态)                      │
//	  │                                                        │
//	  └──→ Timeout ──→ RolledBack (最终状态)                   │
//	                                                           │
//	最终状态: Committed, RolledBack (不可再转换)               │
//
// ─────────────────────────────────────────────────────────────
var validTransitions = map[StateTransition]bool{
	// Init 状态的转换
	{TxStateInit, TxStatePreCommit}:  true, // Init -> PreCommit (正常流程)
	{TxStateInit, TxStateRolledBack}: true, // Init -> RolledBack (直接回滚)
	{TxStateInit, TxStateTimeout}:    true, // Init -> Timeout (超时)

	// PreCommit 状态的转换
	{TxStatePreCommit, TxStateCommitted}:  true, // PreCommit -> Committed (提交成功)
	{TxStatePreCommit, TxStateRolledBack}: true, // PreCommit -> RolledBack (回滚)
	{TxStatePreCommit, TxStateTimeout}:    true, // PreCommit -> Timeout (超时)

	// Timeout 状态的转换（超时后应该回滚）
	{TxStateTimeout, TxStateRolledBack}: true, // Timeout -> RolledBack (超时回滚)

	// 注意: Committed 和 RolledBack 是最终状态，不能转换到其他状态
}

// finalStates 定义最终状态（不可再转换的状态）
var finalStates = map[TransactionState]bool{
	TxStateCommitted:  true,
	TxStateRolledBack: true,
}

// ==================== 状态转换验证器 ====================

// StateTransitionValidator 状态转换验证器
type StateTransitionValidator struct {
	mu sync.RWMutex

	// HLC 时钟用于时间戳比较
	hlc *clock.HLC

	// 自定义转换规则（可选，用于扩展）
	customTransitions map[StateTransition]bool

	// 状态转换钩子
	onTransitionHooks []func(txID string, from, to TransactionState, hlcTS *clock.HLC)
}

// NewStateTransitionValidator 创建状态转换验证器
func NewStateTransitionValidator(hlc *clock.HLC) *StateTransitionValidator {
	if hlc == nil {
		hlc = clock.NewHLC()
	}

	return &StateTransitionValidator{
		hlc:               hlc,
		customTransitions: make(map[StateTransition]bool),
		onTransitionHooks: make([]func(string, TransactionState, TransactionState, *clock.HLC), 0),
	}
}

// IsValidTransition 检查状态转换是否有效
//
// 参数：
//   - from: 当前状态
//   - to: 目标状态
//
// 返回：
//   - true: 转换有效
//   - false: 转换无效
func (v *StateTransitionValidator) IsValidTransition(from, to TransactionState) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()

	transition := StateTransition{From: from, To: to}

	// 首先检查自定义转换规则（允许覆盖默认规则）
	if v.customTransitions[transition] {
		return true
	}

	// 检查是否是最终状态（如果没有自定义规则，最终状态不能转换）
	if finalStates[from] {
		return false
	}

	// 检查标准转换规则
	if validTransitions[transition] {
		return true
	}

	return false
}

// ValidateTransition 验证状态转换并返回详细错误信息
//
// 参数：
//   - txID: 事务 ID（用于错误信息）
//   - from: 当前状态
//   - to: 目标状态
//
// 返回：
//   - nil: 转换有效
//   - error: 转换无效，包含详细错误信息
func (v *StateTransitionValidator) ValidateTransition(txID string, from, to TransactionState) error {
	if !v.IsValidTransition(from, to) {
		if finalStates[from] {
			return fmt.Errorf("事务 %s: 状态 %s 是最终状态，不能转换到 %s",
				txID, from.String(), to.String())
		}
		return fmt.Errorf("事务 %s: 无效的状态转换 %s -> %s",
			txID, from.String(), to.String())
	}
	return nil
}

// ExecuteTransition 执行状态转换（带验证和钩子）
//
// 参数：
//   - txID: 事务 ID
//   - from: 当前状态
//   - to: 目标状态
//
// 返回：
//   - *clock.HLC: 转换发生的 HLC 时间戳
//   - error: 转换失败时的错误
func (v *StateTransitionValidator) ExecuteTransition(txID string, from, to TransactionState) (*clock.HLC, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// 验证转换
	transition := StateTransition{From: from, To: to}

	// 首先检查自定义转换规则（允许覆盖默认规则）
	isValid := v.customTransitions[transition]

	// 如果没有自定义规则，检查标准规则
	if !isValid {
		// 检查是否是最终状态
		if finalStates[from] {
			return nil, fmt.Errorf("事务 %s: 状态 %s 是最终状态，不能转换到 %s",
				txID, from.String(), to.String())
		}

		// 检查标准转换规则
		isValid = validTransitions[transition]
	}

	if !isValid {
		return nil, fmt.Errorf("事务 %s: 无效的状态转换 %s -> %s",
			txID, from.String(), to.String())
	}

	// 生成 HLC 时间戳
	hlcTS := v.hlc.Now()

	// 执行钩子
	for _, hook := range v.onTransitionHooks {
		hook(txID, from, to, hlcTS)
	}

	return hlcTS, nil
}

// ==================== 扩展功能 ====================

// AddCustomTransition 添加自定义转换规则
func (v *StateTransitionValidator) AddCustomTransition(from, to TransactionState) {
	v.mu.Lock()
	defer v.mu.Unlock()

	transition := StateTransition{From: from, To: to}
	v.customTransitions[transition] = true
}

// RemoveCustomTransition 移除自定义转换规则
func (v *StateTransitionValidator) RemoveCustomTransition(from, to TransactionState) {
	v.mu.Lock()
	defer v.mu.Unlock()

	transition := StateTransition{From: from, To: to}
	delete(v.customTransitions, transition)
}

// RegisterTransitionHook 注册状态转换钩子
func (v *StateTransitionValidator) RegisterTransitionHook(hook func(txID string, from, to TransactionState, hlcTS *clock.HLC)) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.onTransitionHooks = append(v.onTransitionHooks, hook)
}

// IsFinalState 检查是否是最终状态
func (v *StateTransitionValidator) IsFinalState(state TransactionState) bool {
	return finalStates[state]
}

// GetValidTransitions 获取从指定状态可以转换到的所有有效状态
func (v *StateTransitionValidator) GetValidTransitions(from TransactionState) []TransactionState {
	v.mu.RLock()
	defer v.mu.RUnlock()

	var validTargets []TransactionState

	// 如果是最终状态，没有有效转换
	if finalStates[from] {
		return validTargets
	}

	// 检查所有可能的转换
	for _, to := range []TransactionState{
		TxStateInit,
		TxStatePreCommit,
		TxStateCommitted,
		TxStateRolledBack,
		TxStateTimeout,
	} {
		transition := StateTransition{From: from, To: to}
		if validTransitions[transition] || v.customTransitions[transition] {
			validTargets = append(validTargets, to)
		}
	}

	return validTargets
}

// ==================== 全局函数（简化调用） ====================

// globalValidator 全局验证器实例
var globalValidator = NewStateTransitionValidator(nil)

// IsValidStateTransition 检查状态转换是否有效（全局函数）
//
// 这是对全局验证器的便捷访问，适用于简单场景。
// 对于需要自定义规则或钩子的场景，请创建专用的验证器实例。
func IsValidStateTransition(from, to TransactionState) bool {
	return globalValidator.IsValidTransition(from, to)
}

// ValidateStateTransition 验证状态转换并返回错误（全局函数）
func ValidateStateTransition(txID string, from, to TransactionState) error {
	return globalValidator.ValidateTransition(txID, from, to)
}

// ==================== HLC 时间戳比较 ====================

// CompareHLCTimestamps 比较两个 HLC 时间戳
//
// 返回值：
//   - -1: ts1 < ts2
//   - 0: ts1 == ts2
//   - 1: ts1 > ts2
func CompareHLCTimestamps(ts1, ts2 *clock.HLC) int {
	return ts1.Compare(ts2)
}

// IsHLCAfter 检查 ts1 是否在 ts2 之后
func IsHLCAfter(ts1, ts2 *clock.HLC) bool {
	return ts1.Compare(ts2) > 0
}

// IsHLCBefore 检查 ts1 是否在 ts2 之前
func IsHLCBefore(ts1, ts2 *clock.HLC) bool {
	return ts1.Compare(ts2) < 0
}

// ==================== 状态转换统计 ====================

// TransitionStats 状态转换统计
type TransitionStats struct {
	mu sync.RWMutex

	// 转换计数
	Counts map[StateTransition]int64

	// 失败计数
	FailedCounts map[StateTransition]int64

	// 总计数
	TotalTransitions int64
	TotalFailures    int64
}

// NewTransitionStats 创建状态转换统计
func NewTransitionStats() *TransitionStats {
	return &TransitionStats{
		Counts:       make(map[StateTransition]int64),
		FailedCounts: make(map[StateTransition]int64),
	}
}

// RecordTransition 记录成功的状态转换
func (s *TransitionStats) RecordTransition(from, to TransactionState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	transition := StateTransition{From: from, To: to}
	s.Counts[transition]++
	s.TotalTransitions++
}

// RecordFailure 记录失败的状态转换
func (s *TransitionStats) RecordFailure(from, to TransactionState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	transition := StateTransition{From: from, To: to}
	s.FailedCounts[transition]++
	s.TotalFailures++
}

// GetCount 获取特定转换的计数
func (s *TransitionStats) GetCount(from, to TransactionState) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	transition := StateTransition{From: from, To: to}
	return s.Counts[transition]
}

// Reset 重置统计
func (s *TransitionStats) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Counts = make(map[StateTransition]int64)
	s.FailedCounts = make(map[StateTransition]int64)
	s.TotalTransitions = 0
	s.TotalFailures = 0
}

// ==================== 带统计的验证器 ====================

// StatsStateTransitionValidator 带统计的状态转换验证器
type StatsStateTransitionValidator struct {
	*StateTransitionValidator
	stats *TransitionStats
}

// NewStatsStateTransitionValidator 创建带统计的验证器
func NewStatsStateTransitionValidator(hlc *clock.HLC) *StatsStateTransitionValidator {
	return &StatsStateTransitionValidator{
		StateTransitionValidator: NewStateTransitionValidator(hlc),
		stats:                    NewTransitionStats(),
	}
}

// ExecuteTransitionWithStats 执行状态转换并记录统计
func (v *StatsStateTransitionValidator) ExecuteTransitionWithStats(txID string, from, to TransactionState) (*clock.HLC, error) {
	ts, err := v.ExecuteTransition(txID, from, to)
	if err != nil {
		v.stats.RecordFailure(from, to)
		return ts, err
	}

	v.stats.RecordTransition(from, to)
	return ts, nil
}

// GetStats 获取统计信息
func (v *StatsStateTransitionValidator) GetStats() *TransitionStats {
	return v.stats
}
