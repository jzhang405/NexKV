// Package consistency 提供 2PC 强一致性协调器实现
//
// P1-2: 状态转换验证测试
package consistency

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/stretchr/testify/require"
)

// TestValidStateTransitions 测试有效的状态转换
func TestValidStateTransitions(t *testing.T) {
	validator := NewStateTransitionValidator(nil)

	// 定义有效的转换测试用例
	validCases := []struct {
		from TransactionState
		to   TransactionState
		desc string
	}{
		{TxStateInit, TxStatePreCommit, "Init -> PreCommit"},
		{TxStateInit, TxStateRolledBack, "Init -> RolledBack"},
		{TxStateInit, TxStateTimeout, "Init -> Timeout"},
		{TxStatePreCommit, TxStateCommitted, "PreCommit -> Committed"},
		{TxStatePreCommit, TxStateRolledBack, "PreCommit -> RolledBack"},
		{TxStatePreCommit, TxStateTimeout, "PreCommit -> Timeout"},
		{TxStateTimeout, TxStateRolledBack, "Timeout -> RolledBack"},
	}

	for _, tc := range validCases {
		t.Run(tc.desc, func(t *testing.T) {
			require.True(t, validator.IsValidTransition(tc.from, tc.to),
				"转换 %s 应该有效", tc.desc)
		})
	}
}

// TestInvalidStateTransitions 测试无效的状态转换
func TestInvalidStateTransitions(t *testing.T) {
	validator := NewStateTransitionValidator(nil)

	// 定义无效的转换测试用例
	invalidCases := []struct {
		from TransactionState
		to   TransactionState
		desc string
	}{
		// 从最终状态转换
		{TxStateCommitted, TxStateInit, "Committed -> Init (最终状态)"},
		{TxStateCommitted, TxStatePreCommit, "Committed -> PreCommit (最终状态)"},
		{TxStateCommitted, TxStateRolledBack, "Committed -> RolledBack (最终状态)"},
		{TxStateRolledBack, TxStateInit, "RolledBack -> Init (最终状态)"},
		{TxStateRolledBack, TxStatePreCommit, "RolledBack -> PreCommit (最终状态)"},
		{TxStateRolledBack, TxStateCommitted, "RolledBack -> Committed (最终状态)"},

		// 非法转换
		{TxStateInit, TxStateCommitted, "Init -> Committed (跳过 PreCommit)"},
		{TxStatePreCommit, TxStateInit, "PreCommit -> Init (不能回退)"},
		{TxStateTimeout, TxStateCommitted, "Timeout -> Committed (超时后不能提交)"},
		{TxStateTimeout, TxStatePreCommit, "Timeout -> PreCommit (超时后不能继续)"},
		{TxStateTimeout, TxStateInit, "Timeout -> Init (不能回退)"},
	}

	for _, tc := range invalidCases {
		t.Run(tc.desc, func(t *testing.T) {
			require.False(t, validator.IsValidTransition(tc.from, tc.to),
				"转换 %s 应该无效", tc.desc)
		})
	}
}

// TestFinalStates 测试最终状态
func TestFinalStates(t *testing.T) {
	validator := NewStateTransitionValidator(nil)

	// 最终状态
	require.True(t, validator.IsFinalState(TxStateCommitted), "Committed 应该是最终状态")
	require.True(t, validator.IsFinalState(TxStateRolledBack), "RolledBack 应该是最终状态")

	// 非最终状态
	require.False(t, validator.IsFinalState(TxStateInit), "Init 不应该是最终状态")
	require.False(t, validator.IsFinalState(TxStatePreCommit), "PreCommit 不应该是最终状态")
	require.False(t, validator.IsFinalState(TxStateTimeout), "Timeout 不应该是最终状态")
}

// TestValidateTransition 测试验证转换函数
func TestValidateTransition(t *testing.T) {
	validator := NewStateTransitionValidator(nil)

	// 有效转换
	err := validator.ValidateTransition("tx-001", TxStateInit, TxStatePreCommit)
	require.NoError(t, err)

	// 无效转换
	err = validator.ValidateTransition("tx-001", TxStateCommitted, TxStateRolledBack)
	require.Error(t, err)
	require.Contains(t, err.Error(), "最终状态")
}

// TestExecuteTransition 测试执行转换
func TestExecuteTransition(t *testing.T) {
	hlc := clock.NewHLC()
	validator := NewStateTransitionValidator(hlc)

	// 有效转换
	ts, err := validator.ExecuteTransition("tx-001", TxStateInit, TxStatePreCommit)
	require.NoError(t, err)
	require.NotNil(t, ts)

	// 无效转换
	_, err = validator.ExecuteTransition("tx-001", TxStateCommitted, TxStateInit)
	require.Error(t, err)
}

// TestCustomTransition 测试自定义转换规则
func TestCustomTransition(t *testing.T) {
	validator := NewStateTransitionValidator(nil)

	// 初始时，Committed -> Init 应该无效
	require.False(t, validator.IsValidTransition(TxStateCommitted, TxStateInit))

	// 添加自定义规则（仅用于测试，实际业务不应这样做）
	validator.AddCustomTransition(TxStateCommitted, TxStateInit)

	// 现在应该有效
	require.True(t, validator.IsValidTransition(TxStateCommitted, TxStateInit))

	// 移除自定义规则
	validator.RemoveCustomTransition(TxStateCommitted, TxStateInit)

	// 又应该无效
	require.False(t, validator.IsValidTransition(TxStateCommitted, TxStateInit))
}

// TestTransitionHook 测试转换钩子
func TestTransitionHook(t *testing.T) {
	hlc := clock.NewHLC()
	validator := NewStateTransitionValidator(hlc)

	// 记录钩子调用
	var hookCalled bool
	var recordedTxID string
	var recordedFrom, recordedTo TransactionState

	validator.RegisterTransitionHook(func(txID string, from, to TransactionState, hlcTS *clock.HLC) {
		hookCalled = true
		recordedTxID = txID
		recordedFrom = from
		recordedTo = to
	})

	// 执行转换
	_, err := validator.ExecuteTransition("tx-hook-test", TxStateInit, TxStatePreCommit)
	require.NoError(t, err)

	// 验证钩子被调用
	require.True(t, hookCalled)
	require.Equal(t, "tx-hook-test", recordedTxID)
	require.Equal(t, TxStateInit, recordedFrom)
	require.Equal(t, TxStatePreCommit, recordedTo)
}

// TestGetValidTransitions 测试获取有效转换
func TestGetValidTransitions(t *testing.T) {
	validator := NewStateTransitionValidator(nil)

	// Init 状态可以转换到的状态
	initTransitions := validator.GetValidTransitions(TxStateInit)
	require.Len(t, initTransitions, 3)
	require.Contains(t, initTransitions, TxStatePreCommit)
	require.Contains(t, initTransitions, TxStateRolledBack)
	require.Contains(t, initTransitions, TxStateTimeout)

	// PreCommit 状态可以转换到的状态
	preCommitTransitions := validator.GetValidTransitions(TxStatePreCommit)
	require.Len(t, preCommitTransitions, 3)
	require.Contains(t, preCommitTransitions, TxStateCommitted)
	require.Contains(t, preCommitTransitions, TxStateRolledBack)
	require.Contains(t, preCommitTransitions, TxStateTimeout)

	// Committed 状态（最终状态）没有有效转换
	committedTransitions := validator.GetValidTransitions(TxStateCommitted)
	require.Empty(t, committedTransitions)

	// RolledBack 状态（最终状态）没有有效转换
	rolledBackTransitions := validator.GetValidTransitions(TxStateRolledBack)
	require.Empty(t, rolledBackTransitions)
}

// TestGlobalValidator 测试全局验证器函数
func TestGlobalValidator(t *testing.T) {
	// 使用全局函数
	require.True(t, IsValidStateTransition(TxStateInit, TxStatePreCommit))
	require.False(t, IsValidStateTransition(TxStateCommitted, TxStateInit))

	// 验证函数
	err := ValidateStateTransition("tx-global", TxStateInit, TxStatePreCommit)
	require.NoError(t, err)

	err = ValidateStateTransition("tx-global", TxStateCommitted, TxStateInit)
	require.Error(t, err)
}

// TestHLCTimestampComparison 测试 HLC 时间戳比较
func TestHLCTimestampComparison(t *testing.T) {
	hlc := clock.NewHLC()

	ts1 := hlc.Now()
	ts2 := hlc.Now()

	// ts2 应该 >= ts1（时间递增）
	require.True(t, ts2.Compare(ts1) >= 0)

	// 测试比较函数
	require.True(t, CompareHLCTimestamps(ts2, ts1) >= 0)

	// 测试 IsHLCAfter
	ts3 := hlc.Now()
	require.True(t, IsHLCAfter(ts3, ts1) || ts3.Compare(ts1) == 0)

	// 测试 IsHLCBefore
	require.True(t, IsHLCBefore(ts1, ts3) || ts1.Compare(ts3) == 0)

	// 验证时间戳不为 nil
	require.NotNil(t, ts1)
	require.NotNil(t, ts2)
	require.NotNil(t, ts3)
}

// TestTransitionStats 测试状态转换统计
func TestTransitionStats(t *testing.T) {
	stats := NewTransitionStats()

	// 记录转换
	stats.RecordTransition(TxStateInit, TxStatePreCommit)
	stats.RecordTransition(TxStateInit, TxStatePreCommit)
	stats.RecordTransition(TxStatePreCommit, TxStateCommitted)

	// 记录失败
	stats.RecordFailure(TxStateCommitted, TxStateInit)

	// 检查计数
	require.Equal(t, int64(2), stats.GetCount(TxStateInit, TxStatePreCommit))
	require.Equal(t, int64(1), stats.GetCount(TxStatePreCommit, TxStateCommitted))
	require.Equal(t, int64(3), stats.TotalTransitions)
	require.Equal(t, int64(1), stats.TotalFailures)

	// 重置
	stats.Reset()
	require.Equal(t, int64(0), stats.TotalTransitions)
	require.Equal(t, int64(0), stats.TotalFailures)
}

// TestStatsStateTransitionValidator 测试带统计的验证器
func TestStatsStateTransitionValidator(t *testing.T) {
	validator := NewStatsStateTransitionValidator(nil)

	// 执行成功转换
	_, err := validator.ExecuteTransitionWithStats("tx-stats-1", TxStateInit, TxStatePreCommit)
	require.NoError(t, err)

	_, err = validator.ExecuteTransitionWithStats("tx-stats-1", TxStatePreCommit, TxStateCommitted)
	require.NoError(t, err)

	// 执行失败转换
	_, err = validator.ExecuteTransitionWithStats("tx-stats-2", TxStateCommitted, TxStateInit)
	require.Error(t, err)

	// 检查统计
	stats := validator.GetStats()
	require.Equal(t, int64(2), stats.TotalTransitions)
	require.Equal(t, int64(1), stats.TotalFailures)
}

// TestTransactionState_String 测试状态字符串表示
func TestTransactionState_String(t *testing.T) {
	require.Equal(t, "Init", TxStateInit.String())
	require.Equal(t, "PreCommit", TxStatePreCommit.String())
	require.Equal(t, "Committed", TxStateCommitted.String())
	require.Equal(t, "RolledBack", TxStateRolledBack.String())
	require.Equal(t, "Timeout", TxStateTimeout.String())
}

// TestConcurrentValidation 测试并发验证
func TestConcurrentValidation(t *testing.T) {
	validator := NewStateTransitionValidator(nil)

	const goroutines = 100
	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			// 并发验证
			validator.IsValidTransition(TxStateInit, TxStatePreCommit)
			if err := validator.ValidateTransition("tx-concurrent", TxStateInit, TxStatePreCommit); err != nil {
				t.Errorf("ValidateTransition failed: %v", err)
			}

			// 并发执行转换
			if _, err := validator.ExecuteTransition("tx-concurrent", TxStateInit, TxStatePreCommit); err != nil {
				t.Errorf("ExecuteTransition failed: %v", err)
			}

			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < goroutines; i++ {
		<-done
	}
}
