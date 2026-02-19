package transport

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestTransportGoroutineLeak 测试 goroutine 泄漏
func TestTransportGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	// 创建并关闭 Transport 100 次
	for range 100 {
		transport, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
		if err != nil {
			t.Fatalf("create transport: %v", err)
		}

		// 模拟一些操作
		transport.Self()
		transport.ConnectedPeers()

		// 关闭
		if err := transport.Close(); err != nil {
			t.Fatalf("close transport: %v", err)
		}
	}

	// 等待 goroutine 回收
	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()

	// 允许少量波动（±5）
	if finalGoroutines > initialGoroutines+5 {
		t.Errorf("goroutine leak detected: initial=%d, final=%d, leaked=%d",
			initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)
	}
}

// TestConcurrentOpenClose 测试并发打开/关闭
func TestConcurrentOpenClose(t *testing.T) {
	transport, err := NewLibp2pTransport(context.Background(), &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("NewLibp2pTransport failed: %v", err)
	}

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			transport.Self()
			transport.ConnectedPeers()
		}()
	}

	// 并发关闭
	go transport.Close()

	wg.Wait()
	// 不应 panic 或死锁
}
