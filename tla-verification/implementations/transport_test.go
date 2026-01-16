package implementations

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestTransportInterface 测试 Transport 接口的基本契约
//
// 这个测试函数用于验证任何 Transport 实现是否符合接口契约。
// 可以用于测试 MemoryTransport、NetworkTransport 等实现。
func TestTransportInterface(t *testing.T) {
	// 这是一个模板测试，具体的传输层实现应该：
	// 1. 创建传输层实例
	// 2. 调用 RunTransportBasicOperations(t, transport)
	// 3. 调用 RunTransportConcurrentOperations(t, transport)
	// 4. 调用 RunTransportErrorHandling(t, transport)
}

// RunTransportBasicOperations 测试传输层基本操作
func RunTransportBasicOperations(t *testing.T, transport Transport) {
	ctx := context.Background()

	// 1. 启动传输层
	if err := transport.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer transport.Stop()

	// 检查状态
	status := transport.Status()
	if !status.IsRunning {
		t.Error("Transport should be running after Start()")
	}

	// 2. 测试发送消息
	msg := Message{
		Type:      GossipExchange,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Payload:   []byte("test payload"),
		Context:   ctx,
	}

	if err := transport.Send("n2", msg); err != nil {
		t.Errorf("Failed to send message: %v", err)
	}

	// 3. 测试广播消息
	broadcastMsg := Message{
		Type:      Heartbeat,
		From:      "n1",
		To:        "", // 空表示广播
		Timestamp: time.Now().UnixNano(),
		Payload:   []byte("heartbeat"),
		Context:   ctx,
	}

	if err := transport.Broadcast(broadcastMsg); err != nil {
		t.Errorf("Failed to broadcast message: %v", err)
	}

	// 4. 测试接收消息
	receiveCh := transport.Receive()
	if receiveCh == nil {
		t.Fatal("Receive() returned nil channel")
	}

	// 5. 停止传输层
	if err := transport.Stop(); err != nil {
		t.Errorf("Failed to stop transport: %v", err)
	}

	// 检查状态
	status = transport.Status()
	if status.IsRunning {
		t.Error("Transport should not be running after Stop()")
	}
}

// RunTransportConcurrentOperations 测试传输层并发操作
func RunTransportConcurrentOperations(t *testing.T, transport Transport) {
	ctx := context.Background()

	// 启动传输层
	if err := transport.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer transport.Stop()

	// 并发参数
	const numGoroutines = 10
	const messagesPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // 发送 + 接收

	// 并发发送
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < messagesPerGoroutine; j++ {
				msg := Message{
					Type:      GossipExchange,
					From:      "n1",
					To:        "n2",
					Timestamp: time.Now().UnixNano(),
					Payload:   []byte("test"),
					Context:   ctx,
				}

				if err := transport.Send("n2", msg); err != nil {
					t.Errorf("Goroutine %d: Failed to send message: %v", id, err)
				}
			}
		}(i)
	}

	// 并发接收
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			receiveCh := transport.Receive()
			timeout := time.After(5 * time.Second)

			for {
				select {
				case msg, ok := <-receiveCh:
					if !ok {
						return // 通道已关闭
					}
					if msg.Type == GossipExchange {
						// 处理消息
					}

				case <-timeout:
					return // 超时
				}
			}
		}(i)
	}

	// 等待所有 goroutine 完成
	wg.Wait()

	// 验证状态
	status := transport.Status()
	if status.IsRunning {
		expectedMessages := int64(numGoroutines * messagesPerGoroutine)
		if status.MessagesSent != expectedMessages {
			t.Logf("Warning: Expected %d messages sent, got %d",
				expectedMessages, status.MessagesSent)
		}
	}
}

// RunTransportErrorHandling 测试传输层错误处理
func RunTransportErrorHandling(t *testing.T, transport Transport) {
	ctx := context.Background()

	// 1. 测试未启动时发送
	msg := Message{
		Type:      GossipExchange,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Payload:   []byte("test"),
		Context:   ctx,
	}

	err := transport.Send("n2", msg)
	if err == nil {
		t.Error("Expected error when sending before Start()")
	} else if err != ErrTransportNotStarted {
		t.Logf("Got error (acceptable): %v", err)
	}

	// 2. 测试发送到不存在的节点
	if err := transport.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer transport.Stop()

	err = transport.Send("nonexistent", msg)
	if err == nil {
		t.Error("Expected error when sending to nonexistent node")
	}

	// 3. 测试停止后发送
	if err := transport.Stop(); err != nil {
		t.Errorf("Failed to stop transport: %v", err)
	}

	err = transport.Send("n2", msg)
	if err == nil {
		t.Error("Expected error when sending after Stop()")
	} else if err != ErrTransportStopped {
		t.Logf("Got error (acceptable): %v", err)
	}
}

// setupTransportTest 设置传输层测试的辅助函数
//
// 这个函数为传输层测试提供通用的设置逻辑：
// - 创建临时目录
// - 初始化测试节点
// - 设置测试配置
func setupTransportTest(t *testing.T) (string, []string) {
	t.Helper()

	// 创建临时目录
	tempDir := t.TempDir()

	// 定义测试节点
	nodeIDs := []string{"n1", "n2", "n3"}

	return tempDir, nodeIDs
}

// assertTransportState 断言传输层状态的辅助函数
func assertTransportState(t *testing.T, transport Transport, isRunning bool) {
	t.Helper()

	status := transport.Status()
	if status.IsRunning != isRunning {
		t.Errorf("Expected IsRunning=%v, got %v", isRunning, status.IsRunning)
	}
}

// waitForMessage 等待接收特定类型消息的辅助函数
func waitForMessage(t *testing.T, receiveCh <-chan Message, timeout time.Duration, expectedType MessageType) *Message {
	t.Helper()

	timeoutCh := time.After(timeout)

	for {
		select {
		case msg, ok := <-receiveCh:
			if !ok {
				t.Fatal("Receive channel closed unexpectedly")
			}
			if msg.Type == expectedType {
				return &msg
			}

		case <-timeoutCh:
			t.Fatalf("Timeout waiting for message type %v", expectedType)
			return nil
		}
	}
}

// BenchmarkTransportSend 基准测试：发送性能
func RunBenchmarkTransportSend(b *testing.B, transport Transport) {
	ctx := context.Background()

	if err := transport.Start(); err != nil {
		b.Fatalf("Failed to start transport: %v", err)
	}
	defer transport.Stop()

	msg := Message{
		Type:      GossipExchange,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Payload:   make([]byte, 1024), // 1KB payload
		Context:   ctx,
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := transport.Send("n2", msg); err != nil {
			b.Fatalf("Failed to send message: %v", err)
		}
	}
}

// RunBenchmarkTransportReceive 基准测试：接收性能
func RunBenchmarkTransportReceive(b *testing.B, transport Transport) {
	if err := transport.Start(); err != nil {
		b.Fatalf("Failed to start transport: %v", err)
	}
	defer transport.Stop()

	receiveCh := transport.Receive()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		select {
		case msg := <-receiveCh:
			_ = msg
		default:
			// 没有消息，继续
		}
	}
}

