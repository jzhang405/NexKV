// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// 基准测试
// ========================================

// BenchmarkRPC_Call 单次 RPC 调用基准测试
func BenchmarkRPC_Call(b *testing.B) {
	serverHost, server, client, cleanup := setupBenchmarkEnvironment(b)
	defer cleanup()

	// 注册简单的处理器
	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}
	require.NoError(b, server.RegisterHandlerFunc("Benchmark", handler))

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := client.Call(ctx, serverHost.ID(), "Benchmark", []byte("request"))
		if err != nil {
			b.Fatalf("RPC call failed: %v", err)
		}
	}
}

// BenchmarkRPC_Call_Payload 不同负载大小的 RPC 调用基准测试
func BenchmarkRPC_Call_Payload(b *testing.B) {
	payloadSizes := []struct {
		name string
		size int
	}{
		{"Small", 100},
		{"Medium", 1024},
		{"Large", 10240},
		{"XLarge", 102400},
	}

	for _, ps := range payloadSizes {
		b.Run(ps.name, func(b *testing.B) {
			serverHost, server, client, cleanup := setupBenchmarkEnvironment(b)
			defer cleanup()

			payload := make([]byte, ps.size)

			handler := func(ctx context.Context, req []byte) ([]byte, error) {
				return payload, nil
			}
			require.NoError(b, server.RegisterHandlerFunc("Payload", handler))

			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, err := client.Call(ctx, serverHost.ID(), "Payload", payload)
				if err != nil {
					b.Fatalf("RPC call failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkRPC_ConcurrentCalls 并发 RPC 调用基准测试
func BenchmarkRPC_ConcurrentCalls(b *testing.B) {
	concurrencies := []struct {
		name  string
		level int
	}{
		{"1", 1},
		{"10", 10},
		{"50", 50},
		{"100", 100},
	}

	for _, c := range concurrencies {
		b.Run(c.name, func(b *testing.B) {
			serverHost, server, client, cleanup := setupBenchmarkEnvironment(b)
			defer cleanup()

			handler := func(ctx context.Context, req []byte) ([]byte, error) {
				return []byte("response"), nil
			}
			require.NoError(b, server.RegisterHandlerFunc("Concurrent", handler))

			ctx := context.Background()

			b.ResetTimer()
			b.ReportAllocs()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, err := client.Call(ctx, serverHost.ID(), "Concurrent", []byte("request"))
					if err != nil {
						b.Fatalf("RPC call failed: %v", err)
					}
				}
			})
		})
	}
}

// BenchmarkRPC_MessagePackSerialization MessagePack 序列化基准测试
func BenchmarkRPC_MessagePackSerialization(b *testing.B) {
	// 准备测试数据
	requests := []struct {
		name string
		req  interface{}
	}{
		{
			name: "SmallRequest",
			req: &RPCRequest{
				Method:    "TestMethod",
				RequestID: 12345,
				Body:      []byte("small body"),
			},
		},
		{
			name: "LargeRequest",
			req: &RPCRequest{
				Method:    "TestMethod",
				RequestID: 12345,
				Body:      make([]byte, 10240),
			},
		},
		{
			name: "ClusterStatusResponse",
			req: &ClusterStatusResponse{
				TotalNodes:  100,
				OnlineNodes: 95,
				TreeDepth:   5,
				Nodes:       generateTestNodeInfo(100),
			},
		},
	}

	for _, tc := range requests {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, err := msgpack.Marshal(tc.req)
				if err != nil {
					b.Fatalf("Marshal failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkRPC_MessagePackDeserialization MessagePack 反序列化基准测试
func BenchmarkRPC_MessagePackDeserialization(b *testing.B) {
	// 准备序列化后的数据
	smallReq := &RPCRequest{
		Method:    "TestMethod",
		RequestID: 12345,
		Body:      []byte("small body"),
	}
	smallData, _ := msgpack.Marshal(smallReq)

	largeResp := &ClusterStatusResponse{
		TotalNodes:  100,
		OnlineNodes: 95,
		TreeDepth:   5,
		Nodes:       generateTestNodeInfo(100),
	}
	largeData, _ := msgpack.Marshal(largeResp)

	cases := []struct {
		name string
		data []byte
		typ  interface{}
	}{
		{"SmallRequest", smallData, &RPCRequest{}},
		{"ClusterStatusResponse", largeData, &ClusterStatusResponse{}},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				err := msgpack.Unmarshal(tc.data, tc.typ)
				if err != nil {
					b.Fatalf("Unmarshal failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkRouter_MethodLookup 方法查找基准测试
func BenchmarkRouter_MethodLookup(b *testing.B) {
	router := NewRouter()

	// 注册多个方法
	methods := []string{
		"Method1", "Method2", "Method3", "Method4", "Method5",
		"Method6", "Method7", "Method8", "Method9", "Method10",
	}

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	for _, method := range methods {
		require.NoError(b, router.RegisterHandler(method, handler))
	}

	ctx := context.Background()
	req := []byte("request")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 循环调用所有方法
		for _, method := range methods {
			_, _ = router.Route(method, ctx, req)
		}
	}
}

// BenchmarkRPC_E2eLatency 端到端延迟基准测试
func BenchmarkRPC_E2eLatency(b *testing.B) {
	serverHost, server, client, cleanup := setupBenchmarkEnvironment(b)
	defer cleanup()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return req, nil // Echo back
	}
	require.NoError(b, server.RegisterHandlerFunc("Echo", handler))

	ctx := context.Background()
	payload := []byte("test payload for latency measurement")

	b.ResetTimer()

	var totalLatency int64
	for i := 0; i < b.N; i++ {
		start := time.Now()
		_, err := client.Call(ctx, serverHost.ID(), "Echo", payload)
		if err != nil {
			b.Fatalf("RPC call failed: %v", err)
		}
		totalLatency += time.Since(start).Nanoseconds()
	}

	// 输出平均延迟
	b.ReportMetric(float64(totalLatency)/float64(b.N), "ns/op")
}

// BenchmarkRPC_Throughput 吞吐量基准测试
func BenchmarkRPC_Throughput(b *testing.B) {
	serverHost, server, client, cleanup := setupBenchmarkEnvironment(b)
	defer cleanup()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("ok"), nil
	}
	require.NoError(b, server.RegisterHandlerFunc("Throughput", handler))

	ctx := context.Background()
	payload := []byte("throughput test")

	b.ResetTimer()

	var totalBytes int64
	for i := 0; i < b.N; i++ {
		resp, err := client.Call(ctx, serverHost.ID(), "Throughput", payload)
		if err != nil {
			b.Fatalf("RPC call failed: %v", err)
		}
		totalBytes += int64(len(payload) + len(resp))
	}

	// 报告吞吐量（字节/操作）
	b.ReportMetric(float64(totalBytes)/float64(b.N), "B/op")
}

// BenchmarkRPC_BatchCall 批量 RPC 调用基准测试
func BenchmarkRPC_BatchCall(b *testing.B) {
	serverHost, server, client, cleanup := setupBenchmarkEnvironment(b)
	defer cleanup()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return req, nil
	}
	require.NoError(b, server.RegisterHandlerFunc("Benchmark", handler))

	ctx := context.Background()

	// 准备批量请求
	batchSize := 10
	reqs := make([]BatchRequest, batchSize)
	for i := 0; i < batchSize; i++ {
		reqs[i] = BatchRequest{
			Method: "Benchmark",
			Body:   []byte("benchmark request"),
			ID:     "test-batch",
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		result := client.CallParallel(ctx, serverHost.ID(), reqs, nil)
		if result.Success != batchSize {
			b.Fatalf("Expected %d successful calls, got %d", batchSize, result.Success)
		}
	}
}

// BenchmarkRPC_BatchCall_Parallel 并发批量 RPC 调用基准测试
func BenchmarkRPC_BatchCall_Parallel(b *testing.B) {
	serverHost, server, client, cleanup := setupBenchmarkEnvironment(b)
	defer cleanup()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return req, nil
	}
	require.NoError(b, server.RegisterHandlerFunc("Benchmark", handler))

	ctx := context.Background()

	// 准备批量请求
	batchSize := 10
	reqs := make([]BatchRequest, batchSize)
	for i := 0; i < batchSize; i++ {
		reqs[i] = BatchRequest{
			Method: "Benchmark",
			Body:   []byte("benchmark request"),
			ID:     "test-batch",
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			result := client.CallParallel(ctx, serverHost.ID(), reqs, nil)
			if result.Success != batchSize {
				b.Fatalf("Expected %d successful calls, got %d", batchSize, result.Success)
			}
		}
	})
}

// ========================================
// 压力测试
// ========================================

// TestRPC_StressHighFrequency 高频调用压力测试
func TestRPC_StressHighFrequency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	serverHost, server, client, cleanup := setupBenchmarkEnvironment(t)
	defer cleanup()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("ok"), nil
	}
	require.NoError(t, server.RegisterHandlerFunc("Stress", handler))

	ctx := context.Background()

	const callCount = 1000
	var wg sync.WaitGroup
	var successCount, failCount int32

	start := time.Now()

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callCount/10; j++ {
				_, err := client.Call(ctx, serverHost.ID(), "Stress", []byte("test"))
				if err != nil {
					atomic.AddInt32(&failCount, 1)
				} else {
					atomic.AddInt32(&successCount, 1)
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	successRate := float64(successCount) / float64(successCount+failCount) * 100
	throughput := float64(successCount+failCount) / elapsed.Seconds()

	t.Logf("压力测试结果:")
	t.Logf("  总调用数: %d", successCount+failCount)
	t.Logf("  成功: %d, 失败: %d", successCount, failCount)
	t.Logf("  成功率: %.2f%%", successRate)
	t.Logf("  吞吐量: %.2f calls/sec", throughput)
	t.Logf("  总耗时: %v", elapsed)

	// 验证成功率应该 > 95%
	assert.Greater(t, successRate, 95.0)
}

// TestRPC_StressLargePayload 大负载压力测试
func TestRPC_StressLargePayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	serverHost, server, client, cleanup := setupBenchmarkEnvironment(t)
	defer cleanup()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return req, nil // Echo back
	}
	require.NoError(t, server.RegisterHandlerFunc("LargePayload", handler))

	ctx := context.Background()

	// 测试不同大小的负载（注意：当前 MaxMessageSize = 10KB）
	payloads := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"8KB", 8 * 1024}, // 接近但小于 MaxMessageSize (10KB)
		// 10KB, 100KB, 1MB 的测试被跳过，因为超过了 MaxMessageSize 限制
	}

	for _, p := range payloads {
		t.Run(p.name, func(t *testing.T) {
			payload := make([]byte, p.size)

			start := time.Now()
			resp, err := client.Call(ctx, serverHost.ID(), "LargePayload", payload)
			elapsed := time.Since(start)

			require.NoError(t, err)
			assert.Len(t, resp, p.size)

			t.Logf("%s: 耗时 %v, 吞吐量 %.2f MB/s",
				p.name, elapsed,
				float64(p.size)/(1024*1024)/elapsed.Seconds())
		})
	}
}

// ========================================
// 辅助函数
// ========================================

// setupBenchmarkEnvironment 设置基准测试环境
func setupBenchmarkEnvironment(b testing.TB) (host.Host, *Server, *Client, func()) {
	// 创建测试用的 libp2p host
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		b.Fatalf("创建服务器 host 失败: %v", err)
	}

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		h1.Close()
		b.Fatalf("创建客户端 host 失败: %v", err)
	}

	// 连接客户端到服务器
	h2.Peerstore().AddAddr(h1.ID(), h1.Addrs()[0], peerstore.PermanentAddrTTL)

	// 创建并启动服务器
	server := NewServer(h1)

	ctx, cancel := context.WithCancel(context.Background())

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)

	// 创建客户端
	client := NewClient(h2)

	// 返回清理函数
	cleanup := func() {
		cancel()
		<-serverErr
		h1.Close()
		h2.Close()
	}

	return h1, server, client, cleanup
}

// generateTestNodeInfo 生成测试用的节点信息
func generateTestNodeInfo(count int) []NodeInfo {
	nodes := make([]NodeInfo, count)
	for i := 0; i < count; i++ {
		nodes[i] = NodeInfo{
			NodeID:   fmt.Sprintf("node-%d", i),
			ParentID: fmt.Sprintf("node-%d", (i-1)/10),
			Level:    i / 10,
			Status:   1, // Ready
			Children: []string{},
		}
	}
	return nodes
}
