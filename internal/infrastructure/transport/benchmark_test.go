// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/compressor"
)

// ============================================================================
// 基准测试配置
// ============================================================================

// 基准测试目标：
// - Baseline（无中间件）：≥12K ops/sec
// - WithMiddleware（有中间件链）：≥10K ops/sec

// createMiddlewareChain 创建完整中间件链
func createMiddlewareChain(rpc *Libp2pRPC) {
	// 中间件顺序：RateLimit → CircuitBreaker → Compression → Retry
	// 注：Compression 在 Retry 之前，避免重试时重复压缩
	//nolint:errcheck // 基准测试不需要检查错误
	rpc.Use(NewRateLimitMiddleware(RateLimitConfig{
		RequestsPerSecond: 100000, // 高限流阈值以避免影响基准测试
		Burst:             100000,
	}))
	//nolint:errcheck // 基准测试不需要检查错误
	rpc.Use(NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "benchmark-circuit-breaker",
		MaxRequests: 10000, // 高阈值避免影响基准测试
		Timeout:     30 * time.Second,
	}))
	//nolint:errcheck // 基准测试不需要检查错误
	rpc.Use(NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 1024,
	}))
	//nolint:errcheck // 基准测试不需要检查错误
	rpc.Use(NewRetryMiddleware(RetryConfig{
		MaxAttempts: 1, // 基准测试不重试
	}))
}

// ============================================================================
// Baseline 基准测试（无中间件）
// ============================================================================

// BenchmarkRPC_Baseline 纯 RPC 吞吐量基准（无中间件）
// 目标：≥12K ops/sec
func BenchmarkRPC_Baseline(b *testing.B) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, service.DefaultRPCConfig())
	defer rpc.Close()

	peer := model.PeerID("node-2")
	transport.connected[peer] = true

	ctx := context.Background()
	payload := make([]byte, 64) // 64 字节负载

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			msg := model.NewMessage(
				fmt.Sprintf("msg-%d", i),
				model.MessageTypeRequest,
				"node-1",
				"node-2",
				payload,
			)
			// 直接调用中间件链（无中间件时直接执行 finalSend）
			_ = rpc.middleware.ExecuteSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
				return nil // 模拟发送成功
			})
			i++
		}
	})
}

// ============================================================================
// WithMiddleware 基准测试（完整中间件链）
// ============================================================================

// BenchmarkRPC_WithMiddleware 完整中间件链吞吐量基准
// 目标：≥10K ops/sec
func BenchmarkRPC_WithMiddleware(b *testing.B) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, service.DefaultRPCConfig())
	createMiddlewareChain(rpc)
	defer rpc.Close()

	peer := model.PeerID("node-2")
	transport.connected[peer] = true

	ctx := context.Background()
	payload := make([]byte, 64) // 64 字节负载

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			msg := model.NewMessage(
				fmt.Sprintf("msg-%d", i),
				model.MessageTypeRequest,
				"node-1",
				"node-2",
				payload,
			)
			_ = rpc.middleware.ExecuteSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
				return nil // 模拟发送成功
			})
			i++
		}
	})
}

// ============================================================================
// 吞吐量测试
// ============================================================================

// BenchmarkRPC_Throughput 单机 RPC 吞吐量
// 目标：≥10K ops/sec
func BenchmarkRPC_Throughput(b *testing.B) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, service.DefaultRPCConfig())
	createMiddlewareChain(rpc)
	defer rpc.Close()

	peer := model.PeerID("node-2")
	transport.connected[peer] = true

	ctx := context.Background()
	payload := make([]byte, 64)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := model.NewMessage(
			fmt.Sprintf("msg-%d", i),
			model.MessageTypeRequest,
			"node-1",
			"node-2",
			payload,
		)
		_ = rpc.middleware.ExecuteSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
			return nil
		})
	}
}

// BenchmarkRPC_Concurrent 并发 RPC 吞吐量
// 目标：≥10K ops/sec
func BenchmarkRPC_Concurrent(b *testing.B) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, service.DefaultRPCConfig())
	createMiddlewareChain(rpc)
	defer rpc.Close()

	peer := model.PeerID("node-2")
	transport.connected[peer] = true

	ctx := context.Background()
	payload := make([]byte, 64)

	var counter int64

	b.ResetTimer()
	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := b.N / numGoroutines

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				msg := model.NewMessage(
					fmt.Sprintf("msg-%d-%d", goroutineID, i),
					model.MessageTypeRequest,
					"node-1",
					"node-2",
					payload,
				)
				_ = rpc.middleware.ExecuteSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
					atomic.AddInt64(&counter, 1)
					return nil
				})
			}
		}(g)
	}
	wg.Wait()
}

// ============================================================================
// 负载大小测试
// ============================================================================

// BenchmarkRPC_Payload_Small 64 字节负载
func BenchmarkRPC_Payload_Small(b *testing.B) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, service.DefaultRPCConfig())
	createMiddlewareChain(rpc)
	defer rpc.Close()

	peer := model.PeerID("node-2")
	transport.connected[peer] = true

	ctx := context.Background()
	payload := make([]byte, 64) // 64 字节

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := model.NewMessage(
			fmt.Sprintf("msg-%d", i),
			model.MessageTypeRequest,
			"node-1",
			"node-2",
			payload,
		)
		_ = rpc.middleware.ExecuteSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
			return nil
		})
	}
}

// BenchmarkRPC_Payload_Medium 1 KB 负载
func BenchmarkRPC_Payload_Medium(b *testing.B) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, service.DefaultRPCConfig())
	createMiddlewareChain(rpc)
	defer rpc.Close()

	peer := model.PeerID("node-2")
	transport.connected[peer] = true

	ctx := context.Background()
	payload := make([]byte, 1024) // 1 KB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := model.NewMessage(
			fmt.Sprintf("msg-%d", i),
			model.MessageTypeRequest,
			"node-1",
			"node-2",
			payload,
		)
		_ = rpc.middleware.ExecuteSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
			return nil
		})
	}
}

// BenchmarkRPC_Payload_Large 4 KB 负载
func BenchmarkRPC_Payload_Large(b *testing.B) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, service.DefaultRPCConfig())
	createMiddlewareChain(rpc)
	defer rpc.Close()

	peer := model.PeerID("node-2")
	transport.connected[peer] = true

	ctx := context.Background()
	payload := make([]byte, 4096) // 4 KB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := model.NewMessage(
			fmt.Sprintf("msg-%d", i),
			model.MessageTypeRequest,
			"node-1",
			"node-2",
			payload,
		)
		_ = rpc.middleware.ExecuteSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
			return nil
		})
	}
}

// ============================================================================
// 单独中间件基准测试
// ============================================================================

// BenchmarkMiddleware_Compression_Snappy Snappy 压缩中间件基准
func BenchmarkMiddleware_Compression_Snappy(b *testing.B) {
	m := NewCompressionMiddleware(CompressionConfig{
		Algorithm: compressor.Snappy,
		Threshold: 100,
	})

	peer := model.PeerID("node-2")
	payload := make([]byte, 1024) // 1 KB
	for i := range payload {
		payload[i] = byte(i % 10)
	}
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "node-1", "node-2", payload)
	ctx := context.Background()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}
}

// BenchmarkMiddleware_RateLimit 限流中间件基准
func BenchmarkMiddleware_RateLimit(b *testing.B) {
	m := NewRateLimitMiddleware(RateLimitConfig{
		RequestsPerSecond: 100000,
		Burst:             100000,
	})

	peer := model.PeerID("node-2")
	payload := make([]byte, 64)
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "node-1", "node-2", payload)
	ctx := context.Background()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}
}

// BenchmarkMiddleware_CircuitBreaker 熔断中间件基准
func BenchmarkMiddleware_CircuitBreaker(b *testing.B) {
	m := NewCircuitBreakerMiddleware(CircuitBreakerConfig{
		Name:        "benchmark",
		MaxRequests: 10000,
		Timeout:     30 * time.Second,
	})

	peer := model.PeerID("node-2")
	payload := make([]byte, 64)
	msg := model.NewMessage("test-id", model.MessageTypeRequest, "node-1", "node-2", payload)
	ctx := context.Background()

	next := func(ctx context.Context, p model.PeerID, m model.Message) error {
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.InterceptSend(ctx, peer, msg, next)
	}
}

// ============================================================================
// 性能验证测试（非基准）
// ============================================================================

// TestPerformance_ThroughputVerification 验证吞吐量达到目标
// 此测试验证中间件链吞吐量 ≥10K ops/sec
func TestPerformance_ThroughputVerification(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance verification in short mode")
	}

	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, service.DefaultRPCConfig())
	createMiddlewareChain(rpc)
	defer rpc.Close()

	peer := model.PeerID("node-2")
	transport.connected[peer] = true

	ctx := context.Background()
	payload := make([]byte, 64)

	// 测试 1 秒内的操作数
	duration := 1 * time.Second
	start := time.Now()
	var ops int64

	for time.Since(start) < duration {
		msg := model.NewMessage(
			fmt.Sprintf("msg-%d", ops),
			model.MessageTypeRequest,
			"node-1",
			"node-2",
			payload,
		)
		_ = rpc.middleware.ExecuteSend(ctx, peer, msg, func(ctx context.Context, p model.PeerID, m model.Message) error {
			return nil
		})
		ops++
	}

	elapsed := time.Since(start)
	opsPerSec := float64(ops) / elapsed.Seconds()

	t.Logf("Throughput: %.0f ops/sec (%d ops in %v)", opsPerSec, ops, elapsed)

	// 验证达到 10K ops/sec 目标（考虑 ±10% 方差，实际目标为 9K）
	if opsPerSec < 9000 {
		t.Errorf("Throughput %.0f ops/sec is below target 10K ops/sec (min 9K)", opsPerSec)
	}
}
