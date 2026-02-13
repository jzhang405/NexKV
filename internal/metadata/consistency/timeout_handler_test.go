// Package consistency 提供 2PC 强一致性协调器实现
//
// P1-1: 2PC 超时处理优化测试
package consistency

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTimeoutPolicy 测试超时策略
func TestTimeoutPolicy(t *testing.T) {
	// 默认策略
	policy := DefaultTimeoutPolicy()
	require.Equal(t, 5*time.Second, policy.PreCommitTimeout)
	require.Equal(t, 3*time.Second, policy.CommitTimeout)
	require.Equal(t, 5*time.Second, policy.AckWaitTimeout)
	require.Equal(t, 3, policy.RetryCount)
	require.Equal(t, 500*time.Millisecond, policy.RetryDelay)

	// 验证和修正
	policy2 := &TimeoutPolicy{
		PreCommitTimeout: -1, // 无效值
	}
	_ = policy2.Validate()
	require.Equal(t, 5*time.Second, policy2.PreCommitTimeout)
}

// TestConditionWaiter 测试条件等待器
func TestConditionWaiter(t *testing.T) {
	waiter := NewConditionWaiter()

	// 测试条件满足
	start := time.Now()
	var condition int32 // 使用 int32 以支持原子操作

	go func() {
		time.Sleep(100 * time.Millisecond)
		atomic.StoreInt32(&condition, 1)
		waiter.Signal(nil, nil)
	}()

	result := waiter.Wait(1*time.Second, func() bool {
		return atomic.LoadInt32(&condition) == 1
	})

	elapsed := time.Since(start)
	require.True(t, result)
	require.Less(t, elapsed, 200*time.Millisecond)
}

// TestConditionWaiter_Timeout 测试条件等待超时
func TestConditionWaiter_Timeout(t *testing.T) {
	waiter := NewConditionWaiter()

	start := time.Now()
	result := waiter.Wait(100*time.Millisecond, func() bool {
		return false // 永远不满足
	})

	elapsed := time.Since(start)
	require.False(t, result)
	require.GreaterOrEqual(t, elapsed, 90*time.Millisecond)
}

// TestConditionWaiter_WithContext 测试带上下文的等待
func TestConditionWaiter_WithContext(t *testing.T) {
	waiter := NewConditionWaiter()
	ctx := context.Background()

	var condition int32 // 使用 int32 以支持原子操作
	go func() {
		time.Sleep(50 * time.Millisecond)
		atomic.StoreInt32(&condition, 1)
		waiter.Signal(nil, nil)
	}()

	result, err := waiter.WaitWithContext(ctx, func() bool {
		return atomic.LoadInt32(&condition) == 1
	})

	require.NoError(t, err)
	require.True(t, result)
}

// TestConditionWaiter_ContextCancel 测试上下文取消
func TestConditionWaiter_ContextCancel(t *testing.T) {
	waiter := NewConditionWaiter()
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := waiter.WaitWithContext(ctx, func() bool {
		return false // 永远不满足
	})

	require.Error(t, err)
	require.False(t, result)
}

// TestAckCollector 测试 ACK 收集器
func TestAckCollector(t *testing.T) {
	collector := NewAckCollector(3, 1*time.Second)

	// 模拟接收 ACK
	go func() {
		time.Sleep(30 * time.Millisecond)
		collector.ReceiveACK("node-1", true)
		time.Sleep(30 * time.Millisecond)
		collector.ReceiveACK("node-2", true)
		time.Sleep(30 * time.Millisecond)
		collector.ReceiveACK("node-3", true)
	}()

	successCount, failedCount, success := collector.WaitAll()

	require.Equal(t, 3, successCount)
	require.Equal(t, 0, failedCount)
	require.True(t, success)
}

// TestAckCollector_Timeout 测试 ACK 收集超时
func TestAckCollector_Timeout(t *testing.T) {
	collector := NewAckCollector(3, 100*time.Millisecond)

	// 只收到部分 ACK
	go func() {
		time.Sleep(20 * time.Millisecond)
		collector.ReceiveACK("node-1", true)
		time.Sleep(20 * time.Millisecond)
		collector.ReceiveACK("node-2", true)
		// node-3 不响应
	}()

	successCount, failedCount, success := collector.WaitAll()

	require.Equal(t, 2, successCount)
	require.Equal(t, 0, failedCount)
	require.False(t, success) // 未达到期望数量
}

// TestAckCollector_WithFailure 测试 ACK 收集带失败
func TestAckCollector_WithFailure(t *testing.T) {
	collector := NewAckCollector(3, 1*time.Second)

	// 模拟一个失败
	go func() {
		time.Sleep(20 * time.Millisecond)
		collector.ReceiveACK("node-1", true)
		time.Sleep(20 * time.Millisecond)
		collector.ReceiveACK("node-2", false) // 失败
	}()

	successCount, _, success := collector.WaitAll()

	require.Equal(t, 1, successCount)
	require.False(t, success)
}

// TestAckCollector_WithContext 测试带上下文的 ACK 收集
func TestAckCollector_WithContext(t *testing.T) {
	collector := NewAckCollector(2, 1*time.Second)
	ctx := context.Background()

	go func() {
		time.Sleep(30 * time.Millisecond)
		collector.ReceiveACK("node-1", true)
		time.Sleep(30 * time.Millisecond)
		collector.ReceiveACK("node-2", true)
	}()

	successCount, _, success, err := collector.WaitWithContext(ctx)

	require.NoError(t, err)
	require.Equal(t, 2, successCount)
	require.True(t, success)
}

// TestAckCollector_Progress 测试进度查询
func TestAckCollector_Progress(t *testing.T) {
	collector := NewAckCollector(3, 1*time.Second)

	received, expected := collector.GetProgress()
	require.Equal(t, 0, received)
	require.Equal(t, 3, expected)

	collector.ReceiveACK("node-1", true)
	received, expected = collector.GetProgress()
	require.Equal(t, 1, received)
	require.Equal(t, 3, expected)
}

// TestTimeoutManager 测试超时管理器
func TestTimeoutManager(t *testing.T) {
	manager := NewTimeoutManager(nil)

	// 默认策略
	policy := manager.GetPolicy()
	require.NotNil(t, policy)
	require.Equal(t, 5*time.Second, policy.PreCommitTimeout)

	// 获取各类型超时
	require.Equal(t, 5*time.Second, manager.GetPreCommitTimeout())
	require.Equal(t, 3*time.Second, manager.GetCommitTimeout())
	require.Equal(t, 5*time.Second, manager.GetAckWaitTimeout())
}

// TestTimeoutManager_CustomPolicy 测试自定义策略
func TestTimeoutManager_CustomPolicy(t *testing.T) {
	customPolicy := &TimeoutPolicy{
		PreCommitTimeout: 10 * time.Second,
		CommitTimeout:    5 * time.Second,
		AckWaitTimeout:   8 * time.Second,
		RetryCount:       5,
		RetryDelay:       1 * time.Second,
	}

	manager := NewTimeoutManager(customPolicy)

	require.Equal(t, 10*time.Second, manager.GetPreCommitTimeout())
	require.Equal(t, 5*time.Second, manager.GetCommitTimeout())
	require.Equal(t, 8*time.Second, manager.GetAckWaitTimeout())
}

// TestTimeoutManager_AdaptiveTimeout 测试自适应超时
func TestTimeoutManager_AdaptiveTimeout(t *testing.T) {
	policy := DefaultTimeoutPolicy()
	policy.EnableAdaptiveTimeout = true
	manager := NewTimeoutManager(policy)

	// 记录延迟样本
	manager.RecordLatency(100 * time.Millisecond)
	manager.RecordLatency(200 * time.Millisecond)
	manager.RecordLatency(300 * time.Millisecond)

	// 平均延迟应该是 200ms
	avgLatency := manager.GetAverageLatency()
	require.Equal(t, 200*time.Millisecond, avgLatency)

	// 自适应超时应该是平均延迟的 3 倍（600ms），小于默认的 5 秒
	timeout := manager.GetPreCommitTimeout()
	require.LessOrEqual(t, timeout, 600*time.Millisecond)
}

// TestTimeoutManager_SetPolicy 测试设置策略
func TestTimeoutManager_SetPolicy(t *testing.T) {
	manager := NewTimeoutManager(nil)

	newPolicy := &TimeoutPolicy{
		PreCommitTimeout: 15 * time.Second,
	}
	manager.SetPolicy(newPolicy)

	require.Equal(t, 15*time.Second, manager.GetPreCommitTimeout())
}

// TestTimeoutContext 测试超时上下文
func TestTimeoutContext(t *testing.T) {
	manager := NewTimeoutManager(nil)

	ctx := manager.NewTimeoutContext(context.Background(), "precommit")
	defer ctx.Cancel()

	require.NotNil(t, ctx.Context)

	// 检查超时
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.GreaterOrEqual(t, time.Until(deadline), 4*time.Second)
}

// TestTimeoutContext_Cancel 测试取消上下文
func TestTimeoutContext_Cancel(t *testing.T) {
	manager := NewTimeoutManager(nil)

	ctx := manager.NewTimeoutContext(context.Background(), "precommit")
	ctx.Cancel()

	<-ctx.Done()
	require.Error(t, ctx.Err())
}

// TestRetryableOperation 测试重试操作
func TestRetryableOperation(t *testing.T) {
	manager := NewTimeoutManager(&TimeoutPolicy{
		RetryCount: 3,
		RetryDelay: 10 * time.Millisecond,
	})

	op := manager.NewRetryableOperation()

	// 第一次成功
	callCount := 0
	err := op.Execute(func() error {
		callCount++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, callCount)

	// 第三次成功
	callCount = 0
	err = op.Execute(func() error {
		callCount++
		if callCount < 3 {
			return errors.New("timeout")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, callCount)

	// 全部失败
	callCount = 0
	err = op.Execute(func() error {
		callCount++
		return errors.New("timeout")
	})
	require.Error(t, err)
	require.Equal(t, 3, callCount)
}

// TestRetryableOperation_WithContext 测试带上下文的重试
func TestRetryableOperation_WithContext(t *testing.T) {
	manager := NewTimeoutManager(&TimeoutPolicy{
		RetryCount: 5,
		RetryDelay: 100 * time.Millisecond,
	})

	op := manager.NewRetryableOperation()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	callCount := 0
	err := op.ExecuteWithContext(ctx, func() error {
		callCount++
		return errors.New("timeout")
	})

	require.Error(t, err)
	// 上下文应该在第 2 次重试前超时
	require.LessOrEqual(t, callCount, 2)
}

// TestIsRetryableError 测试可重试错误判断
func TestIsRetryableError(t *testing.T) {
	// 可重试（包含 connection, timeout, 或 temporary）
	require.True(t, isRetryableError(errors.New("connection timeout")))
	require.True(t, isRetryableError(errors.New("connection refused")))
	require.True(t, isRetryableError(errors.New("temporary failure")))
	require.True(t, isRetryableError(context.DeadlineExceeded))

	// 不可重试
	require.False(t, isRetryableError(errors.New("invalid argument")))
	require.False(t, isRetryableError(errors.New("not found")))
	require.False(t, isRetryableError(errors.New("network is unreachable")))
	require.False(t, isRetryableError(context.Canceled))
	require.False(t, isRetryableError(nil))
}

// TestConditionWaiter_Reuse 测试等待器重用
func TestConditionWaiter_Reuse(t *testing.T) {
	waiter := NewConditionWaiter()

	// 第一次使用
	waiter.Signal("result1", nil)
	result, _ := waiter.GetResult()
	require.Equal(t, "result1", result)

	// 重置
	waiter.Reset()
	require.False(t, waiter.done)

	// 第二次使用
	waiter.Signal("result2", nil)
	result, _ = waiter.GetResult()
	require.Equal(t, "result2", result)
}

// TestTimeoutManager_CreateCollectors 测试创建收集器
func TestTimeoutManager_CreateCollectors(t *testing.T) {
	manager := NewTimeoutManager(nil)

	// 创建 ACK 收集器
	collector := manager.NewAckCollector(5)
	require.NotNil(t, collector)

	// 创建条件等待器
	waiter := manager.NewConditionWaiter()
	require.NotNil(t, waiter)
}

// TestTimeoutContext_Types 测试不同类型的超时上下文
func TestTimeoutContext_Types(t *testing.T) {
	policy := &TimeoutPolicy{
		PreCommitTimeout:   1 * time.Second,
		CommitTimeout:      2 * time.Second,
		AckWaitTimeout:     3 * time.Second,
		RollbackTimeout:    4 * time.Second,
		GossipQueryTimeout: 5 * time.Second,
	}
	manager := NewTimeoutManager(policy)

	testCases := []struct {
		timeoutType string
		expected    time.Duration
	}{
		{"precommit", 1 * time.Second},
		{"commit", 2 * time.Second},
		{"ack", 3 * time.Second},
		{"rollback", 4 * time.Second},
		{"gossip", 5 * time.Second},
		{"unknown", 1 * time.Second}, // 默认使用 PreCommitTimeout
	}

	for _, tc := range testCases {
		t.Run(tc.timeoutType, func(t *testing.T) {
			ctx := manager.NewTimeoutContext(context.Background(), tc.timeoutType)
			defer ctx.Cancel()

			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.GreaterOrEqual(t, time.Until(deadline), tc.expected-100*time.Millisecond)
		})
	}
}
