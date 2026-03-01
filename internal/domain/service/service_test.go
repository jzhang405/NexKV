// Package service 定义领域服务测试
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
)

// ==========================================
// DiscoveryEventType 测试
// ==========================================

func TestDiscoveryEventType_String(t *testing.T) {
	tests := []struct {
		eventType DiscoveryEventType
		expected  string
	}{
		{DiscoveryEventPeerFound, "peer_found"},
		{DiscoveryEventPeerUpdated, "peer_updated"},
		{DiscoveryEventPeerSuspected, "peer_suspected"},
		{DiscoveryEventPeerLost, "peer_lost"},
		{DiscoveryEventType(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.eventType.String())
		})
	}
}

// ==========================================
// DiscoveryEvent 测试
// ==========================================

func TestDiscoveryEvent_Fields(t *testing.T) {
	peerID := model.PeerID("12D3KooWTest")
	addrs := []model.NetworkAddress{
		&mockNetworkAddress{addr: "/ip4/127.0.0.1/tcp/8080"},
	}

	event := DiscoveryEvent{
		PeerID: peerID,
		Addrs:  addrs,
		Type:   DiscoveryEventPeerFound,
	}

	assert.Equal(t, peerID, event.PeerID)
	assert.Len(t, event.Addrs, 1)
	assert.Equal(t, DiscoveryEventPeerFound, event.Type)
}

// ==========================================
// Future[T] 泛型类型测试
// ==========================================

func TestFuture_Resolve(t *testing.T) {
	future := NewFuture[int]()
	expected := 42

	future.Resolve(expected)

	result, err := future.Get(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestFuture_Reject(t *testing.T) {
	future := NewFuture[int]()
	testErr := errors.New("test error")

	future.Reject(testErr)

	result, err := future.Get(context.Background())
	assert.Error(t, err)
	assert.Equal(t, 0, result)
	assert.Equal(t, testErr, err)
}

func TestFuture_IsDone(t *testing.T) {
	future := NewFuture[int]()

	assert.False(t, future.IsDone())

	future.Resolve(42)

	assert.True(t, future.IsDone())
}

func TestFuture_ContextCanceled(t *testing.T) {
	future := NewFuture[int]()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := future.Get(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, 0, result)
}

func TestSubmitWithResult_Success(t *testing.T) {
	executor := &mockExecutor{delay: 10 * time.Millisecond}

	ctx := context.Background()
	future := SubmitWithResult(executor, ctx, PriorityNormal, func(ctx context.Context) (int, error) {
		return 42, nil
	})

	result, err := future.Get(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 42, result)
}

func TestSubmitWithResult_Error(t *testing.T) {
	executor := &mockExecutor{delay: 10 * time.Millisecond}
	testErr := errors.New("test error")

	ctx := context.Background()
	future := SubmitWithResult(executor, ctx, PriorityNormal, func(ctx context.Context) (int, error) {
		return 0, testErr
	})

	result, err := future.Get(ctx)
	assert.Error(t, err)
	assert.Equal(t, testErr, err)
	assert.Equal(t, 0, result)
}

// mockExecutor 是用于测试的模拟执行器
type mockExecutor struct {
	delay time.Duration
}

func (m *mockExecutor) Submit(ctx context.Context, priority TaskPriority, task func(context.Context)) error {
	go func() {
		time.Sleep(m.delay)
		task(ctx)
	}()
	return nil
}

func (m *mockExecutor) Close() error {
	return nil
}

// ==========================================
// 领域事件测试
// ==========================================

func TestTaskSubmittedEvent_Fields(t *testing.T) {
	now := time.Now()
	event := TaskSubmittedEvent{
		TaskID:    "task-001",
		Priority:  PriorityHigh,
		Timestamp: now,
	}

	assert.Equal(t, "task-001", event.TaskID)
	assert.Equal(t, PriorityHigh, event.Priority)
	assert.Equal(t, now, event.Timestamp)
}

func TestTaskCompletedEvent_Fields(t *testing.T) {
	now := time.Now()
	event := TaskCompletedEvent{
		TaskID:    "task-001",
		Duration:  100 * time.Millisecond,
		Timestamp: now,
	}

	assert.Equal(t, "task-001", event.TaskID)
	assert.Equal(t, 100*time.Millisecond, event.Duration)
	assert.Equal(t, now, event.Timestamp)
}

func TestTaskFailedEvent_Fields(t *testing.T) {
	now := time.Now()
	testErr := assert.AnError
	event := TaskFailedEvent{
		TaskID:    "task-001",
		Error:     testErr,
		Timestamp: now,
	}

	assert.Equal(t, "task-001", event.TaskID)
	assert.Equal(t, testErr, event.Error)
	assert.Equal(t, now, event.Timestamp)
}

func TestQueueFullEvent_Fields(t *testing.T) {
	now := time.Now()
	event := QueueFullEvent{
		CoreID:      2,
		QueueLength: 100,
		Strategy:    "drop",
		Timestamp:   now,
	}

	assert.Equal(t, 2, event.CoreID)
	assert.Equal(t, 100, event.QueueLength)
	assert.Equal(t, "drop", event.Strategy)
	assert.Equal(t, now, event.Timestamp)
}

// ==========================================
// 错误重新导出测试
// ==========================================

func TestErrors_Export(t *testing.T) {
	// 验证错误变量已正确导出
	assert.NotNil(t, ErrCanceled)
	assert.NotNil(t, ErrTimeout)
	assert.NotNil(t, ErrCompleted)
	assert.NotNil(t, ErrAlreadyCanceled)
	assert.NotNil(t, ErrInvalidParam)
	assert.NotNil(t, ErrTransportClosed)
	assert.NotNil(t, ErrAlreadyConnected)
	assert.NotNil(t, ErrConnectionFailed)
	assert.NotNil(t, ErrNotConnected)
	assert.NotNil(t, ErrChannelClosed)
	assert.NotNil(t, ErrMessageTooLarge)
	assert.NotNil(t, ErrInvalidMessage)
	assert.NotNil(t, ErrNodeNotFound)
	assert.NotNil(t, ErrPeerIDInvalid)
	assert.NotNil(t, ErrAddrInvalid)
	assert.NotNil(t, ErrAddrTooLong)
	assert.NotNil(t, ErrAsyncExecFailed)
	assert.NotNil(t, ErrCallbackPanic)
	assert.NotNil(t, ErrMajorityFailed)
	assert.NotNil(t, ErrAllFailed)
	assert.NotNil(t, ErrPeerUnreachable)
	assert.NotNil(t, ErrNoHandler)
	assert.NotNil(t, ErrCodecFailure)
	assert.NotNil(t, ErrStrategyNotMajority)
	assert.NotNil(t, ErrInvalidStrategy)
	assert.NotNil(t, ErrChainFrozen)
}

func TestWrap_Functions(t *testing.T) {
	// 验证 Wrap 函数已正确导出
	assert.NotNil(t, Wrap)
	assert.NotNil(t, Wrapf)

	// 测试 Wrap 功能
	err := Wrap(assert.AnError, "wrapped")
	assert.Contains(t, err.Error(), "wrapped")
}

// ==========================================
// 优先级常量测试
// ==========================================

func TestTaskPriority_Constants(t *testing.T) {
	assert.Equal(t, TaskPriority(model.TaskPriorityCritical), PriorityCritical)
	assert.Equal(t, TaskPriority(model.TaskPriorityHigh), PriorityHigh)
	assert.Equal(t, TaskPriority(model.TaskPriorityNormal), PriorityNormal)
	assert.Equal(t, TaskPriority(model.TaskPriorityLow), PriorityLow)
}

// ==========================================
// 中间件优先级常量测试
// ==========================================

func TestMiddlewarePriority_Constants(t *testing.T) {
	// 验证优先级顺序：数字越小越先执行（越外层）
	assert.Less(t, MiddlewarePriorityLogging, MiddlewarePriorityRateLimit)
	assert.Less(t, MiddlewarePriorityMetrics, MiddlewarePriorityRateLimit)
	assert.Less(t, MiddlewarePriorityRateLimit, MiddlewarePriorityCircuitBreaker)
	assert.Less(t, MiddlewarePriorityCircuitBreaker, MiddlewarePriorityCompression)
	assert.Less(t, MiddlewarePriorityCompression, MiddlewarePriorityRetry)
}

// mockNetworkAddress 用于测试的 mock 实现
type mockNetworkAddress struct {
	addr string
}

func (m *mockNetworkAddress) String() string {
	return m.addr
}

func (m *mockNetworkAddress) Protocol() string {
	return "mock"
}
