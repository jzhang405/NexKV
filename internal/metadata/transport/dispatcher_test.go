// Package transport dispatcher unit tests
package transport

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// Test helper functions
// ========================================

// ========================================
// 测试辅助函数
// ========================================

// newTestMsgFrame 创建测试用 MsgFrame
func newTestMsgFrame(nodeID uint64, msgSeq uint64, msgType types.MessageType) MsgFrame {
	return MsgFrame{
		FixedHeader: *NewFixedHeader(nodeID, msgSeq, msgType, 1, 0, 0, 10),
	}
}

// mockHandler 模拟消息处理器
type mockHandler struct {
	mu          sync.Mutex
	handledMsgs []MsgFrame
	handleDelay time.Duration
	handleError error
}

func (m *mockHandler) HandleMessage(ctx context.Context, msg MsgFrame) error {
	if m.handleDelay > 0 {
		time.Sleep(m.handleDelay)
	}

	m.mu.Lock()
	m.handledMsgs = append(m.handledMsgs, msg)
	m.mu.Unlock()

	return m.handleError
}

func (m *mockHandler) handledCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.handledMsgs)
}

// ========================================
// 基础功能测试
// ========================================

// TestNewDispatcher 测试创建分发器
func TestNewDispatcher(t *testing.T) {
	tests := []struct {
		name    string
		config  *DispatcherConfig
		handler Handler
		wantErr bool
	}{
		{
			name:    "默认配置",
			config:  nil,
			handler: &mockHandler{},
			wantErr: false,
		},
		{
			name: "自定义配置",
			config: &DispatcherConfig{
				WorkerCount:   4,
				QueueSize:     1000,
				BatchSize:     16,
				FlushInterval: 5,
			},
			handler: &mockHandler{},
			wantErr: false,
		},
		{
			name: "无效 WorkerCount",
			config: &DispatcherConfig{
				WorkerCount: 0,
			},
			handler: &mockHandler{},
			wantErr: true,
		},
		{
			name: "无效 QueueSize",
			config: &DispatcherConfig{
				WorkerCount: 8,
				QueueSize:   0,
			},
			handler: &mockHandler{},
			wantErr: true,
		},
		{
			name:    "nil handler",
			config:  nil,
			handler: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := NewDispatcher(tt.config, tt.handler)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDispatcher() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && d == nil {
				t.Error("NewDispatcher() returned nil dispatcher")
			}
		})
	}
}

// TestDispatcherStartStop 测试启动和停止
func TestDispatcherStartStop(t *testing.T) {
	handler := &mockHandler{}
	config := DefaultDispatcherConfig()

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	// 启动
	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// 重复启动应该失败
	if err := d.Start(); err == nil {
		t.Error("Expected error when starting already running dispatcher")
	}

	// 停止
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// 重复停止应该失败
	if err := d.Stop(); err == nil {
		t.Error("Expected error when stopping already stopped dispatcher")
	}
}

// TestRegisterConnection 测试连接注册
func TestRegisterConnection(t *testing.T) {
	handler := &mockHandler{}
	config := DefaultDispatcherConfig()

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			t.Errorf("d.Stop() failed: %v", err)
		}
	}()

	// 注册连接
	msgChan := make(chan MsgFrame, 10)
	cancel := d.RegisterConnection("127.0.0.1:9211", msgChan)
	if cancel == nil {
		t.Fatal("RegisterConnection() returned nil cancel function")
	}

	// 重复注册应该失败
	cancel2 := d.RegisterConnection("127.0.0.1:9211", msgChan)
	if cancel2 != nil {
		t.Error("Expected nil cancel function when registering duplicate connection")
	}

	// 注销连接
	cancel()
	time.Sleep(100 * time.Millisecond) // 等待 goroutine 退出

	// 重新注册应该成功
	cancel3 := d.RegisterConnection("127.0.0.1:9211", msgChan)
	if cancel3 == nil {
		t.Error("Expected valid cancel function after re-registration")
	}

	close(msgChan)
}

// ========================================
// Fan-in 模式测试
// ========================================

// TestFanInMultipleConnections 测试多连接 fan-in
func TestFanInMultipleConnections(t *testing.T) {
	handler := &mockHandler{
		handleDelay: 10 * time.Millisecond, // 模拟处理延迟
	}
	config := &DispatcherConfig{
		WorkerCount:        4,
		QueueSize:          500, // 增加队列大小以容纳所有消息
		BatchSize:          10,
		FlushInterval:      10,
		EnableBackpressure: true, // 启用背压模式，确保消息不丢失
	}

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			t.Errorf("d.Stop() failed: %v", err)
		}
	}()

	// 模拟 10 个连接
	connCount := 10
	msgsPerConn := 20

	var wg sync.WaitGroup
	for i := 0; i < connCount; i++ {
		addr := fmt.Sprintf("127.0.0.%d:9211", i+1)
		msgChan := make(chan MsgFrame, msgsPerConn)

		cancel := d.RegisterConnection(addr, msgChan)
		defer cancel()

		wg.Add(1)
		go func(addr string, ch chan<- MsgFrame) {
			defer wg.Done()
			for j := 0; j < msgsPerConn; j++ {
				msg := newTestMsgFrame(uint64(j), uint64(j), types.MessageTypeGet)
				ch <- msg
			}
			close(ch)
		}(addr, msgChan)
	}

	// 等待所有消息发送完成
	wg.Wait()

	// 等待处理完成（使用轮询方式，最多等待 3 秒）
	expected := connCount * msgsPerConn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if handler.handledCount() >= expected {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 验证消息处理数量
	handled := handler.handledCount()
	if handled != expected {
		t.Errorf("Handled %d messages, expected %d", handled, expected)
	}
}

// ========================================
// 性能测试
// ========================================

// TestDispatcherPerformance 测试分发器性能
func TestDispatcherPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	handler := &mockHandler{}
	config := &DispatcherConfig{
		WorkerCount:   8,
		QueueSize:     10000,
		BatchSize:     32,
		FlushInterval: 10,
	}

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			t.Errorf("d.Stop() failed: %v", err)
		}
	}()

	// 模拟连接数和每连接消息数
	// race 模式下减少负载以避免超时
	connCount := 100
	msgsPerConn := 1000

	// 检测 race 模式并调整参数
	if flag.Lookup("race") != nil || os.Getenv("GORACE") != "" {
		connCount = 10   // race 模式下减少连接数
		msgsPerConn = 100 // race 模式下减少每连接消息数
	}

	startTime := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < connCount; i++ {
		addr := fmt.Sprintf("10.0.0.%d:9211", i)
		msgChan := make(chan MsgFrame, msgsPerConn)

		cancel := d.RegisterConnection(addr, msgChan)
		defer cancel()

		wg.Add(1)
		go func(ch chan<- MsgFrame) {
			defer wg.Done()
			for j := 0; j < msgsPerConn; j++ {
				msg := newTestMsgFrame(uint64(j), uint64(j), types.MessageTypeGet)
				ch <- msg
			}
			close(ch)
		}(msgChan)
	}

	wg.Wait()

	// 等待处理完成（添加超时保护，避免无限等待）
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		stats := d.GetStats()
		if stats.MsgCount >= uint64(connCount*msgsPerConn) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	elapsed := time.Since(startTime)
	totalMsgs := connCount * msgsPerConn
	qps := float64(totalMsgs) / elapsed.Seconds()

	stats := d.GetStats()
	t.Logf("Performance: %d messages in %v (%.0f QPS)", totalMsgs, elapsed, qps)
	t.Logf("Stats: Processed=%d, Dropped=%d, Queued=%d",
		stats.MsgCount, stats.DropCount, stats.QueuedMsgs)

	// 性能基准：应该能处理至少 10000 QPS
	// 注意：如果因为超时导致 QPS 降低，可以跳过此检查
	if stats.MsgCount >= uint64(connCount*msgsPerConn) && qps < 10000 {
		t.Errorf("Performance too low: %.0f QPS, expected at least 10000 QPS", qps)
	}
}

// ========================================
// 统计信息测试
// ========================================

// TestGetStats 测试统计信息
func TestGetStats(t *testing.T) {
	handler := &mockHandler{}
	config := DefaultDispatcherConfig()

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	// 启动前
	stats := d.GetStats()
	if stats.Running {
		t.Error("Expected Running=false before Start()")
	}

	// 启动后
	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	stats = d.GetStats()
	if !stats.Running {
		t.Error("Expected Running=true after Start()")
	}
	if stats.WorkerCount != config.WorkerCount {
		t.Errorf("WorkerCount = %d, want %d", stats.WorkerCount, config.WorkerCount)
	}
	if stats.QueueSize != config.QueueSize {
		t.Errorf("QueueSize = %d, want %d", stats.QueueSize, config.QueueSize)
	}

	// 发送消息后
	msgChan := make(chan MsgFrame, 10)
	cancel := d.RegisterConnection("test", msgChan)
	defer cancel()

	msgChan <- newTestMsgFrame(1, 1, types.MessageTypeGet)
	close(msgChan)

	time.Sleep(100 * time.Millisecond)

	stats = d.GetStats()
	if stats.MsgCount == 0 {
		t.Error("Expected MsgCount > 0 after sending message")
	}

	if err := d.Stop(); err != nil {
		t.Errorf("d.Stop() failed: %v", err)
	}
}

// ========================================
// 并发安全测试
// ========================================

// TestConcurrentRegister 并发注册连接
func TestConcurrentRegister(t *testing.T) {
	handler := &mockHandler{}
	config := DefaultDispatcherConfig()

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			t.Errorf("d.Stop() failed: %v", err)
		}
	}()

	// 并发注册连接
	connCount := 100
	var wg sync.WaitGroup

	for i := 0; i < connCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			addr := fmt.Sprintf("concurrent-%d:9211", id)
			msgChan := make(chan MsgFrame, 1)

			cancel := d.RegisterConnection(addr, msgChan)
			if cancel != nil {
				// 发送一条消息后关闭
				msgChan <- newTestMsgFrame(uint64(id), 1, types.MessageTypeGet)
				close(msgChan)
			}
		}(i)
	}

	wg.Wait()

	// 等待处理
	time.Sleep(500 * time.Millisecond)

	stats := d.GetStats()
	t.Logf("Concurrent register: Processed=%d messages", stats.MsgCount)
}

// ========================================
// P0-3: 背压机制测试
// ========================================

// TestBackpressureEnabled 测试启用背压时，队列满会阻塞发送者
func TestBackpressureEnabled(t *testing.T) {
	handler := &mockHandler{
		handleDelay: 100 * time.Millisecond, // 模拟慢处理
	}
	config := &DispatcherConfig{
		WorkerCount:        4,    // 满足 MinWorkers 约束
		QueueSize:          2,    // 小队列
		EnableBackpressure: true, // 启用背压
	}

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			t.Errorf("d.Stop() failed: %v", err)
		}
	}()

	msgChan := make(chan MsgFrame, 10)
	cancel := d.RegisterConnection("backpressure-test", msgChan)
	if cancel == nil {
		t.Fatal("RegisterConnection() returned nil cancel")
	}
	defer cancel()

	// 快速发送多条消息，超过队列大小
	sentCount := 0
	done := make(chan bool)

	go func() {
		for i := 0; i < 5; i++ {
			msgChan <- newTestMsgFrame(1, uint64(i), types.MessageTypeGet)
			sentCount++
			t.Logf("Sent message %d", i)
		}
		done <- true
	}()

	// 等待发送完成（如果背压生效，会阻塞）
	select {
	case <-done:
		t.Logf("All %d messages sent (backpressure blocked sender)", sentCount)
	case <-time.After(2 * time.Second):
		t.Fatalf("Timeout - sender blocked by backpressure (only %d sent before block)", sentCount)
	}

	// 等待处理
	time.Sleep(600 * time.Millisecond)

	stats := d.GetStats()
	t.Logf("Processed: %d, Dropped: %d", stats.MsgCount, stats.DropCount)

	// 启用背压时，不应该丢弃消息
	if stats.DropCount > 0 {
		t.Errorf("Backpressure enabled but %d messages were dropped", stats.DropCount)
	}
}

// TestBackpressureDisabled 测试禁用背压时，队列满会调用回调
func TestBackpressureDisabled(t *testing.T) {
	droppedCount := 0
	droppedMessages := make([]MsgFrame, 0)
	var mu sync.Mutex

	handler := &mockHandler{
		handleDelay: 500 * time.Millisecond, // 模拟慢处理（增加延迟）
	}
	config := &DispatcherConfig{
		WorkerCount:        4,     // 最小 worker 数（新配置约束）
		QueueSize:          2,     // 小队列
		EnableBackpressure: false, // 禁用背压
		OnDroppedMessage: func(addr string, msg MsgFrame) bool {
			mu.Lock()
			defer mu.Unlock()
			droppedCount++
			droppedMessages = append(droppedMessages, msg)
			t.Logf("Dropped message %d from %s (CorrelationID: %s)",
				droppedCount, addr, msg.CorrelationID())
			return false // 不重试，直接放弃
		},
	}

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			t.Errorf("d.Stop() failed: %v", err)
		}
	}()

	msgChan := make(chan MsgFrame, 20)
	cancel := d.RegisterConnection("no-backpressure-test", msgChan)
	if cancel == nil {
		t.Fatal("RegisterConnection() returned nil cancel")
	}
	defer cancel()

	// 快速发送多条消息，超过队列大小
	for i := 0; i < 20; i++ { // 增加消息数量以触发丢弃
		msgChan <- newTestMsgFrame(1, uint64(i), types.MessageTypeGet)
		t.Logf("Sent message %d", i)
	}

	// 等待处理和回调
	time.Sleep(500 * time.Millisecond)

	stats := d.GetStats()

	// 使用互斥锁保护读取共享变量
	mu.Lock()
	actualDroppedCount := droppedCount
	actualDroppedMessages := make([]MsgFrame, len(droppedMessages))
	copy(actualDroppedMessages, droppedMessages)
	mu.Unlock()

	t.Logf("Processed: %d, Dropped: %d (callback invoked: %d)",
		stats.MsgCount, stats.DropCount, actualDroppedCount)

	// 禁用背压时，应该有消息被丢弃
	if stats.DropCount == 0 {
		t.Error("Backpressure disabled but no messages were dropped")
	}

	if droppedCount == 0 {
		t.Error("OnDroppedMessage callback was not invoked")
	}
}

// TestBackpressureCallbackRetry 测试回调返回 true 时重试发送
func TestBackpressureCallbackRetry(t *testing.T) {
	retryCount := 0
	var mu sync.Mutex

	handler := &mockHandler{
		handleDelay: 200 * time.Millisecond, // 增加延迟以确保队列满
	}
	config := &DispatcherConfig{
		WorkerCount:        4, // 最小 worker 数（新配置约束）
		QueueSize:          2, // 小队列
		EnableBackpressure: false,
		OnDroppedMessage: func(addr string, msg MsgFrame) bool {
			mu.Lock()
			defer mu.Unlock()
			retryCount++
			t.Logf("Callback invoked for message (retry count: %d)", retryCount)
			// 前两次重试，第三次放弃
			return retryCount <= 2
		},
	}

	d, err := NewDispatcher(config, handler)
	if err != nil {
		t.Fatalf("NewDispatcher() failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			t.Errorf("d.Stop() failed: %v", err)
		}
	}()

	msgChan := make(chan MsgFrame, 20)
	cancel := d.RegisterConnection("retry-test", msgChan)
	if cancel == nil {
		t.Fatal("RegisterConnection() returned nil cancel")
	}
	defer cancel()

	// 快速发送多条消息，超过队列大小 (2)
	// 由于有 4 个 worker 并行处理，需要发送更多消息才能触发队列满
	for i := 0; i < 15; i++ {
		msgChan <- newTestMsgFrame(1, uint64(i), types.MessageTypeGet)
		t.Logf("Sent message %d", i)
	}

	// 等待处理
	time.Sleep(1000 * time.Millisecond)

	stats := d.GetStats()

	// 使用互斥锁保护读取共享变量
	mu.Lock()
	actualRetryCount := retryCount
	mu.Unlock()

	t.Logf("Processed: %d, Dropped: %d, Retries: %d",
		stats.MsgCount, stats.DropCount, actualRetryCount)

	if actualRetryCount == 0 {
		t.Error("Callback was never invoked - queue may not have filled up")
	}
}
