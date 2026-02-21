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

func TestMiddlewareChain_UseFirst(t *testing.T) {
	chain := NewMiddlewareChain()

	mw1 := &testMiddleware{name: "mw1"}
	mw2 := &testMiddleware{name: "mw2"}

	_ = chain.Use(mw1)
	if err := chain.UseFirst(mw2); err != nil {
		t.Fatalf("UseFirst() error = %v", err)
	}

	list := chain.List()
	if len(list) != 2 {
		t.Errorf("List() length = %d, want 2", len(list))
	}
	if list[0].Name() != "mw2" {
		t.Errorf("List()[0] = %s, want mw2 (should be first)", list[0].Name())
	}
}

func TestMiddlewareChain_UseAt(t *testing.T) {
	chain := NewMiddlewareChain()

	mw1 := &testMiddleware{name: "mw1"}
	mw2 := &testMiddleware{name: "mw2"}
	mw3 := &testMiddleware{name: "mw3"}

	_ = chain.Use(mw1)
	_ = chain.Use(mw3)
	if err := chain.UseAt(1, mw2); err != nil {
		t.Fatalf("UseAt() error = %v", err)
	}

	list := chain.List()
	if len(list) != 3 {
		t.Errorf("List() length = %d, want 3", len(list))
	}
	if list[1].Name() != "mw2" {
		t.Errorf("List()[1] = %s, want mw2 (should be at index 1)", list[1].Name())
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
	if err := chain.UseFirst(&testMiddleware{name: "mw3"}); err != service.ErrChainFrozen {
		t.Errorf("UseFirst() after freeze error = %v, want ErrChainFrozen", err)
	}
	if err := chain.UseAt(0, &testMiddleware{name: "mw4"}); err != service.ErrChainFrozen {
		t.Errorf("UseAt() after freeze error = %v, want ErrChainFrozen", err)
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

	// 验证执行顺序
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
	name string
}

func (m *testMiddleware) Name() string { return m.name }
func (m *testMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	return next(ctx, peer, msg)
}
func (m *testMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	return next(ctx, peer, msg)
}

// orderMiddleware 记录执行顺序的中间件
type orderMiddleware struct {
	name  string
	order *[]string
}

func (m *orderMiddleware) Name() string { return m.name }
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

func (m *errorMiddleware) Name() string { return "error" }
func (m *errorMiddleware) InterceptSend(ctx context.Context, peer model.PeerID, msg model.Message, next service.SendFunc) error {
	return m.err
}
func (m *errorMiddleware) InterceptReceive(ctx context.Context, peer model.PeerID, msg model.Message, next service.ReceiveFunc) error {
	return m.err
}
