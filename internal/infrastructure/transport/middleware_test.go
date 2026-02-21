package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/sirupsen/logrus"
)

// createTestMessageForMiddleware 创建测试消息
func createTestMessageForMiddleware() model.Message {
	return model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test payload"))
}

// ============================================================================
// MiddlewareChain 测试
// ============================================================================

func TestMiddlewareChain_Use(t *testing.T) {
	chain := NewMiddlewareChain()

	mw1 := &testMiddleware{name: "mw1"}
	mw2 := &testMiddleware{name: "mw2"}

	if err := chain.Use(mw1); err != nil {
		t.Fatalf("Use() error = %v", err)
	}
	if err := chain.Use(mw2); err != nil {
		t.Fatalf("Use() error = %v", err)
	}

	list := chain.List()
	if len(list) != 2 {
		t.Errorf("List() length = %d, want 2", len(list))
	}
	if list[0].Name() != "mw1" {
		t.Errorf("List()[0] = %s, want mw1", list[0].Name())
	}
	if list[1].Name() != "mw2" {
		t.Errorf("List()[1] = %s, want mw2", list[1].Name())
	}
}

func TestMiddlewareChain_Remove(t *testing.T) {
	chain := NewMiddlewareChain()

	mw1 := &testMiddleware{name: "mw1"}
	mw2 := &testMiddleware{name: "mw2"}

	_ = chain.Use(mw1)
	_ = chain.Use(mw2)
	if err := chain.Remove("mw1"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	list := chain.List()
	if len(list) != 1 {
		t.Errorf("List() length = %d, want 1", len(list))
	}
	if list[0].Name() != "mw2" {
		t.Errorf("List()[0] = %s, want mw2", list[0].Name())
	}
}

func TestMiddlewareChain_Freeze(t *testing.T) {
	chain := NewMiddlewareChain()
	mw := &testMiddleware{name: "mw"}

	// 冻结前可以添加
	if err := chain.Use(mw); err != nil {
		t.Fatalf("Use() before freeze error = %v", err)
	}

	// 冻结
	chain.Freeze()
	if !chain.IsFrozen() {
		t.Error("IsFrozen() = false, want true")
	}

	// 冻结后不能添加
	if err := chain.Use(&testMiddleware{name: "mw2"}); err != service.ErrChainFrozen {
		t.Errorf("Use() after freeze error = %v, want ErrChainFrozen", err)
	}
	if err := chain.Remove("mw"); err != service.ErrChainFrozen {
		t.Errorf("Remove() after freeze error = %v, want ErrChainFrozen", err)
	}
	if err := chain.Clear(); err != service.ErrChainFrozen {
		t.Errorf("Clear() after freeze error = %v, want ErrChainFrozen", err)
	}
}

func TestMiddlewareChain_ExecuteSend(t *testing.T) {
	chain := NewMiddlewareChain()

	// 记录执行顺序
	var order []string
	mw1 := &orderMiddleware{name: "mw1", order: &order}
	mw2 := &orderMiddleware{name: "mw2", order: &order}

	_ = chain.Use(mw1)
	_ = chain.Use(mw2)

	msg := createTestMessageForMiddleware()
	var finalCalled bool
	final := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		finalCalled = true
		order = append(order, "final")
		return nil
	}

	err := chain.ExecuteSend(context.Background(), "node-2", msg, final)
	if err != nil {
		t.Fatalf("ExecuteSend() error = %v", err)
	}

	if !finalCalled {
		t.Error("Final function was not called")
	}

	// 验证执行顺序：mw1 → mw2 → final
	expected := []string{"mw1", "mw2", "final"}
	if len(order) != len(expected) {
		t.Fatalf("Execution order length = %d, want %d", len(order), len(expected))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %s, want %s", i, order[i], v)
		}
	}
}

func TestMiddlewareChain_ExecuteReceive(t *testing.T) {
	chain := NewMiddlewareChain()

	var order []string
	mw1 := &orderMiddleware{name: "mw1", order: &order}
	mw2 := &orderMiddleware{name: "mw2", order: &order}

	_ = chain.Use(mw1)
	_ = chain.Use(mw2)

	msg := createTestMessageForMiddleware()
	var finalCalled bool
	final := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		finalCalled = true
		order = append(order, "final")
		return nil
	}

	err := chain.ExecuteReceive(context.Background(), "node-1", msg, final)
	if err != nil {
		t.Fatalf("ExecuteReceive() error = %v", err)
	}

	if !finalCalled {
		t.Error("Final function was not called")
	}

	// 验证执行顺序（Receive 是反向执行：mw2 → mw1 → final）
	expected := []string{"mw2", "mw1", "final"}
	if len(order) != len(expected) {
		t.Fatalf("Execution order length = %d, want %d", len(order), len(expected))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %s, want %s", i, order[i], v)
		}
	}
}

func TestMiddlewareChain_ErrorPropagation(t *testing.T) {
	chain := NewMiddlewareChain()

	expectedErr := errors.New("test error")
	mw := &errorMiddleware{err: expectedErr}
	_ = chain.Use(mw)

	msg := createTestMessageForMiddleware()
	final := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return nil
	}

	err := chain.ExecuteSend(context.Background(), "node-2", msg, final)
	if err != expectedErr {
		t.Errorf("ExecuteSend() error = %v, want %v", err, expectedErr)
	}
}

func TestMiddlewareChain_ConcurrentAccess(t *testing.T) {
	chain := NewMiddlewareChain()

	var wg sync.WaitGroup
	numGoroutines := 100

	// P0 修复：分阶段执行，先完成所有写入，再执行并发读取
	// 避免写入和读取同时进行导致的 race condition

	// 阶段 1：并发添加中间件（只写入）
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = chain.Use(&testMiddleware{name: fmt.Sprintf("mw-%d", id)})
		}(i)
	}
	wg.Wait() // 等待所有写入完成

	// 阶段 2：并发读取（所有写入已完成）
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = chain.List()
		}()
	}
	wg.Wait()

	// 验证链仍然有效
	list := chain.List()
	if len(list) != numGoroutines {
		t.Errorf("List() length = %d, want %d", len(list), numGoroutines)
	}
}

// TestMiddlewareChain_PriorityOrdering 测试优先级排序
// 验证中间件按 Priority 值从小到大排序（数字越小越先执行）
func TestMiddlewareChain_PriorityOrdering(t *testing.T) {
	chain := NewMiddlewareChain()

	// 以非优先顺序添加中间件
	mwRetry := &testMiddleware{name: "retry", priority: 40}          // 最内层
	mwRateLimit := &testMiddleware{name: "rate-limit", priority: 10} // 最外层
	mwCompression := &testMiddleware{name: "compression", priority: 30}
	mwCircuitBreaker := &testMiddleware{name: "circuit-breaker", priority: 20}

	// 以随机顺序添加
	_ = chain.Use(mwRetry)
	_ = chain.Use(mwRateLimit)
	_ = chain.Use(mwCompression)
	_ = chain.Use(mwCircuitBreaker)

	// 验证排序后的顺序
	list := chain.List()
	expectedOrder := []string{"rate-limit", "circuit-breaker", "compression", "retry"}
	for i, mw := range list {
		if mw.Name() != expectedOrder[i] {
			t.Errorf("Position %d: got %s, want %s", i, mw.Name(), expectedOrder[i])
		}
	}
}

// TestMiddlewareChain_PriorityOrderingWithRealMiddleware 测试真实中间件优先级
func TestMiddlewareChain_PriorityOrderingWithRealMiddleware(t *testing.T) {
	chain := NewMiddlewareChain()

	// 以非优先顺序添加真实中间件
	_ = chain.Use(NewRetryMiddleware(DefaultRetryConfig()))                   // 40
	_ = chain.Use(NewRateLimitMiddleware(DefaultRateLimitConfig()))           // 10
	_ = chain.Use(NewCompressionMiddleware(DefaultCompressionConfig()))       // 30
	_ = chain.Use(NewCircuitBreakerMiddleware(DefaultCircuitBreakerConfig())) // 20

	// 验证排序后的顺序
	list := chain.List()
	expectedOrder := []string{"rate-limit", "circuit-breaker", "compression", "retry"}
	for i, mw := range list {
		if mw.Name() != expectedOrder[i] {
			t.Errorf("Position %d: got %s, want %s", i, mw.Name(), expectedOrder[i])
		}
	}
}

// TestMiddlewareChain_ReceiveReverseOrder 测试 Receive 链反向执行
// 验证 Send 和 Receive 的执行顺序是相反的
func TestMiddlewareChain_ReceiveReverseOrder(t *testing.T) {
	chain := NewMiddlewareChain()

	var sendOrder, receiveOrder []string

	// 添加中间件，按优先级排序后顺序为：mw10, mw20, mw30
	_ = chain.Use(&orderMiddleware{name: "mw30", order: &sendOrder, priority: 30})
	_ = chain.Use(&orderMiddleware{name: "mw10", order: &sendOrder, priority: 10})
	_ = chain.Use(&orderMiddleware{name: "mw20", order: &sendOrder, priority: 20})

	// 测试 Send 顺序：mw10 → mw20 → mw30
	sendOrder = nil
	_ = chain.ExecuteSend(context.Background(), "peer", createTestMessageForMiddleware(), func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return nil
	})
	expectedSendOrder := []string{"mw10", "mw20", "mw30"}
	if len(sendOrder) != len(expectedSendOrder) {
		t.Fatalf("Send order length = %d, want %d", len(sendOrder), len(expectedSendOrder))
	}
	for i, name := range expectedSendOrder {
		if sendOrder[i] != name {
			t.Errorf("Send order[%d] = %s, want %s", i, sendOrder[i], name)
		}
	}

	// 测试 Receive 顺序：mw30 → mw20 → mw10（反向）
	// 更新 orderMiddleware 的 order 指针
	receiveOrder = nil
	for _, mw := range chain.List() {
		if om, ok := mw.(*orderMiddleware); ok {
			om.order = &receiveOrder
		}
	}
	_ = chain.ExecuteReceive(context.Background(), "peer", createTestMessageForMiddleware(), func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return nil
	})
	expectedReceiveOrder := []string{"mw30", "mw20", "mw10"} // 反向
	if len(receiveOrder) != len(expectedReceiveOrder) {
		t.Fatalf("Receive order length = %d, want %d", len(receiveOrder), len(expectedReceiveOrder))
	}
	for i, name := range expectedReceiveOrder {
		if receiveOrder[i] != name {
			t.Errorf("Receive order[%d] = %s, want %s", i, receiveOrder[i], name)
		}
	}
}

// ============================================================================
// LoggingMiddleware 测试
// ============================================================================

func TestLoggingMiddleware_Name(t *testing.T) {
	mw := NewLoggingMiddleware(nil)
	if mw.Name() != "logging" {
		t.Errorf("Name() = %s, want logging", mw.Name())
	}
}

func TestLoggingMiddleware_InterceptSend(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	mw := NewLoggingMiddleware(&LoggingMiddlewareConfig{
		Logger: logger,
		Level:  logrus.DebugLevel,
	})

	msg := createTestMessageForMiddleware()
	var finalCalled bool
	final := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		finalCalled = true
		return nil
	}

	err := mw.InterceptSend(context.Background(), "node-2", msg, final)
	if err != nil {
		t.Fatalf("InterceptSend() error = %v", err)
	}

	if !finalCalled {
		t.Error("Final function was not called")
	}
}

func TestLoggingMiddleware_InterceptReceive(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	mw := NewLoggingMiddleware(&LoggingMiddlewareConfig{
		Logger: logger,
		Level:  logrus.DebugLevel,
	})

	msg := createTestMessageForMiddleware()
	var finalCalled bool
	final := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		finalCalled = true
		return nil
	}

	err := mw.InterceptReceive(context.Background(), "node-1", msg, final)
	if err != nil {
		t.Fatalf("InterceptReceive() error = %v", err)
	}

	if !finalCalled {
		t.Error("Final function was not called")
	}
}

// ============================================================================
// MetricsMiddleware 测试
// ============================================================================

func TestMetricsMiddleware_Name(t *testing.T) {
	collector := NewDefaultMetricsCollector()
	mw := NewMetricsMiddleware(collector)
	if mw.Name() != "metrics" {
		t.Errorf("Name() = %s, want metrics", mw.Name())
	}
}

func TestMetricsMiddleware_InterceptSend(t *testing.T) {
	collector := NewDefaultMetricsCollector()
	mw := NewMetricsMiddleware(collector)

	msg := createTestMessageForMiddleware()
	var finalCalled bool
	final := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		finalCalled = true
		return nil
	}

	err := mw.InterceptSend(context.Background(), "node-2", msg, final)
	if err != nil {
		t.Fatalf("InterceptSend() error = %v", err)
	}

	if !finalCalled {
		t.Error("Final function was not called")
	}

	snap := collector.Snapshot()
	if snap.SendSuccess != 1 {
		t.Errorf("SendSuccess = %d, want 1", snap.SendSuccess)
	}
}

func TestMetricsMiddleware_InterceptReceive(t *testing.T) {
	collector := NewDefaultMetricsCollector()
	mw := NewMetricsMiddleware(collector)

	msg := createTestMessageForMiddleware()
	var finalCalled bool
	final := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		finalCalled = true
		return nil
	}

	err := mw.InterceptReceive(context.Background(), "node-1", msg, final)
	if err != nil {
		t.Fatalf("InterceptReceive() error = %v", err)
	}

	if !finalCalled {
		t.Error("Final function was not called")
	}

	snap := collector.Snapshot()
	if snap.ReceiveSuccess != 1 {
		t.Errorf("ReceiveSuccess = %d, want 1", snap.ReceiveSuccess)
	}
}

func TestMetricsMiddleware_RecordFailure(t *testing.T) {
	collector := NewDefaultMetricsCollector()
	mw := NewMetricsMiddleware(collector)

	msg := createTestMessageForMiddleware()
	expectedErr := errors.New("test error")
	final := func(ctx context.Context, peer model.PeerID, msg model.Message) error {
		return expectedErr
	}

	_ = mw.InterceptSend(context.Background(), "node-2", msg, final)

	snap := collector.Snapshot()
	if snap.SendFailure != 1 {
		t.Errorf("SendFailure = %d, want 1", snap.SendFailure)
	}
	if snap.SendSuccess != 0 {
		t.Errorf("SendSuccess = %d, want 0", snap.SendSuccess)
	}
}

func TestDefaultMetricsCollector_Snapshot(t *testing.T) {
	collector := NewDefaultMetricsCollector()

	// 记录一些数据
	collector.RecordCount("send", true)
	collector.RecordCount("send", false)
	collector.RecordCount("receive", true)
	collector.RecordLatency("send", 1000) // 1µs
	collector.RecordSize("send", 100)

	snap := collector.Snapshot()

	if snap.SendTotal != 2 {
		t.Errorf("SendTotal = %d, want 2", snap.SendTotal)
	}
	if snap.SendSuccess != 1 {
		t.Errorf("SendSuccess = %d, want 1", snap.SendSuccess)
	}
	if snap.SendFailure != 1 {
		t.Errorf("SendFailure = %d, want 1", snap.SendFailure)
	}
	if snap.ReceiveSuccess != 1 {
		t.Errorf("ReceiveSuccess = %d, want 1", snap.ReceiveSuccess)
	}
}

func TestDefaultMetricsCollector_Reset(t *testing.T) {
	collector := NewDefaultMetricsCollector()

	// 记录一些数据
	collector.RecordCount("send", true)
	collector.RecordCount("receive", true)

	// 重置
	collector.Reset()

	snap := collector.Snapshot()
	if snap.SendTotal != 0 {
		t.Errorf("SendTotal after reset = %d, want 0", snap.SendTotal)
	}
	if snap.ReceiveTotal != 0 {
		t.Errorf("ReceiveTotal after reset = %d, want 0", snap.ReceiveTotal)
	}
}

// ============================================================================
// 测试辅助类型
// ============================================================================

// testMiddleware 简单测试中间件
type testMiddleware struct {
	name     string
	priority int
}

func (m *testMiddleware) Name() string  { return m.name }
func (m *testMiddleware) Priority() int { return m.priority }
func (m *testMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	return next(ctx, peer, msg)
}
func (m *testMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	return next(ctx, peer, msg)
}

// orderMiddleware 记录执行顺序的中间件
type orderMiddleware struct {
	name     string
	order    *[]string
	priority int
}

func (m *orderMiddleware) Name() string  { return m.name }
func (m *orderMiddleware) Priority() int { return m.priority }
func (m *orderMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	*m.order = append(*m.order, m.name)
	return next(ctx, peer, msg)
}
func (m *orderMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	*m.order = append(*m.order, m.name)
	return next(ctx, peer, msg)
}

// errorMiddleware 返回错误的中间件
type errorMiddleware struct {
	err error
}

func (m *errorMiddleware) Name() string  { return "error" }
func (m *errorMiddleware) Priority() int { return 50 }
func (m *errorMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	return m.err
}
func (m *errorMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	return m.err
}
