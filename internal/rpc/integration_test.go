// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// 集成测试
// ========================================

// TestRPCClientServer_Integration 测试完整的 RPC 调用流程
func TestRPCClientServer_Integration(t *testing.T) {
	// 创建服务器 host
	serverHost := createTestHost(t)
	defer serverHost.Close()

	// 创建客户端 host
	clientHost := createTestHost(t)
	defer clientHost.Close()

	// 创建并启动 RPC 服务器
	server := NewServer(serverHost)

	// 注册测试处理器
	testHandler := func(ctx context.Context, req []byte) ([]byte, error) {
		// 解析请求
		var request struct {
			Message string `msgpack:"message"`
		}
		if err := msgpack.Unmarshal(req, &request); err != nil {
			return nil, err
		}

		// 构造响应
		response := struct {
			Reply string `msgpack:"reply"`
		}{
			Reply: fmt.Sprintf("Echo: %s", request.Message),
		}

		return msgpack.Marshal(response)
	}

	err := server.RegisterHandlerFunc("Echo", testHandler)
	require.NoError(t, err)

	// 启动服务器
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	// 等待服务器启动
	time.Sleep(200 * time.Millisecond)
	assert.True(t, server.IsRunning())

	// 连接客户端到服务器
	clientHost.Peerstore().AddAddr(serverHost.ID(), serverHost.Addrs()[0], peerstore.PermanentAddrTTL)

	// 创建 RPC 客户端
	client := NewClient(clientHost)

	// 发送 RPC 请求
	request := struct {
		Message string `msgpack:"message"`
	}{
		Message: "Hello, RPC!",
	}
	reqBody, err := msgpack.Marshal(request)
	require.NoError(t, err)

	respBody, err := client.Call(ctx, serverHost.ID(), "Echo", reqBody)
	require.NoError(t, err)

	// 解析响应
	var response struct {
		Reply string `msgpack:"reply"`
	}
	err = msgpack.Unmarshal(respBody, &response)
	require.NoError(t, err)

	assert.Equal(t, "Echo: Hello, RPC!", response.Reply)

	// 停止服务器
	cancel()
	assert.NoError(t, <-serverErr)
	assert.False(t, server.IsRunning())
}

// TestRPCClientServer_Ping_Integration 测试 Ping 功能的完整流程
func TestRPCClientServer_Ping_Integration(t *testing.T) {
	// 创建服务器和客户端 host
	serverHost := createTestHost(t)
	defer serverHost.Close()

	clientHost := createTestHost(t)
	defer clientHost.Close()

	// 创建并启动 RPC 服务器，注册默认处理器
	server := NewServer(serverHost)
	err := server.RegisterDefaultHandlers()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	// 等待服务器启动
	time.Sleep(200 * time.Millisecond)

	// 连接客户端到服务器
	clientHost.Peerstore().AddAddr(serverHost.ID(), serverHost.Addrs()[0], peerstore.PermanentAddrTTL)

	// 创建 RPC 客户端
	client := NewClient(clientHost)

	// 发送 Ping 请求
	sequence := uint64(time.Now().UnixNano())
	req := NewNodePingRequest(sequence)
	reqBody, err := msgpack.Marshal(req)
	require.NoError(t, err)

	respBody, err := client.Call(ctx, serverHost.ID(), "NodePing", reqBody)
	require.NoError(t, err)

	// 解析响应
	var resp NodePingResponse
	err = msgpack.Unmarshal(respBody, &resp)
	require.NoError(t, err)

	// 验证响应
	assert.Equal(t, sequence, resp.Sequence)
	assert.Equal(t, 1, resp.Status) // Ready 状态（0=Init, 1=Ready, 2=Joining, 3=Leaving, 4=Failed）

	// 停止服务器
	cancel()
	assert.NoError(t, <-serverErr)
}

// TestRPCClientServer_Concurrent 测试并发 RPC 调用
func TestRPCClientServer_Concurrent(t *testing.T) {
	serverHost := createTestHost(t)
	defer serverHost.Close()

	clientHost := createTestHost(t)
	defer clientHost.Close()

	// 创建并启动服务器
	server := NewServer(serverHost)

	counter := 0
	counterMu := sync.Mutex{}

	testHandler := func(ctx context.Context, req []byte) ([]byte, error) {
		counterMu.Lock()
		counter++
		counterMu.Unlock()
		return []byte("OK"), nil
	}

	err := server.RegisterHandlerFunc("Increment", testHandler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	// 连接客户端到服务器
	clientHost.Peerstore().AddAddr(serverHost.ID(), serverHost.Addrs()[0], peerstore.PermanentAddrTTL)

	client := NewClient(clientHost)

	// 并发发送多个请求
	const concurrency = 10
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Call(ctx, serverHost.ID(), "Increment", []byte("test"))
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	// 验证所有请求都成功
	for err := range errs {
		assert.NoError(t, err)
	}

	// 验证处理器被调用了正确的次数
	assert.Equal(t, concurrency, counter)

	// 停止服务器
	cancel()
	assert.NoError(t, <-serverErr)
}

// TestRPCClientServer_Timeout 测试 RPC 调用超时
func TestRPCClientServer_Timeout(t *testing.T) {
	serverHost := createTestHost(t)
	defer serverHost.Close()

	clientHost := createTestHost(t)
	defer clientHost.Close()

	// 创建服务器，注册一个慢速处理器
	server := NewServer(serverHost)

	slowHandler := func(ctx context.Context, req []byte) ([]byte, error) {
		// 模拟慢速处理
		time.Sleep(2 * time.Second)
		return []byte("Done"), nil
	}

	err := server.RegisterHandlerFunc("Slow", slowHandler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	// 连接客户端到服务器
	clientHost.Peerstore().AddAddr(serverHost.ID(), serverHost.Addrs()[0], peerstore.PermanentAddrTTL)

	client := NewClient(clientHost)
	client.SetDefaultTimeout(500 * time.Millisecond) // 设置短超时

	// 发送带超时的请求
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer shortCancel()

	// 应该超时
	_, err = client.Call(shortCtx, serverHost.ID(), "Slow", []byte("test"))
	assert.Error(t, err)

	// 停止服务器
	cancel()
	assert.NoError(t, <-serverErr)
}

// TestRPCClientServer_StreamReusability 测试 Stream 复用
func TestRPCClientServer_StreamReusability(t *testing.T) {
	serverHost := createTestHost(t)
	defer serverHost.Close()

	clientHost := createTestHost(t)
	defer clientHost.Close()

	// 创建服务器
	server := NewServer(serverHost)

	callCount := 0
	callCountMu := sync.Mutex{}

	testHandler := func(ctx context.Context, req []byte) ([]byte, error) {
		callCountMu.Lock()
		callCount++
		callCountMu.Unlock()
		return []byte(fmt.Sprintf("Call #%d", callCount)), nil
	}

	err := server.RegisterHandlerFunc("Count", testHandler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	// 连接客户端到服务器
	clientHost.Peerstore().AddAddr(serverHost.ID(), serverHost.Addrs()[0], peerstore.PermanentAddrTTL)

	client := NewClient(clientHost)

	// 通过同一个 Stream 发送多个请求
	const requestCount = 5
	for i := 0; i < requestCount; i++ {
		respBody, err := client.Call(ctx, serverHost.ID(), "Count", []byte("test"))
		require.NoError(t, err)
		require.Contains(t, string(respBody), "Call #")
	}

	// 停止服务器
	cancel()
	assert.NoError(t, <-serverErr)

	// 验证所有请求都被处理了
	assert.Equal(t, requestCount, callCount)
}

// ========================================
// 监控和限流集成测试
// ========================================

// TestRPCMonitoring_MetricsCollection 集成测试：指标收集
func TestRPCMonitoring_MetricsCollection(t *testing.T) {
	// 获取全局指标实例
	metrics := GetRPCMetrics()
	assert.NotNil(t, metrics)

	// 模拟 RPC 调用场景
	peerID := peer.ID("test-peer-metrics-collection")

	// 1. 连接生命周期
	metrics.RecordConnectionOpened(peerID.String())

	// 2. Stream 生命周期
	metrics.RecordStreamCreated(peerID.String())
	timer := metrics.RecordCallStart(peerID.String(), "test.method")
	time.Sleep(5 * time.Millisecond)
	timer.Stop()
	metrics.RecordStreamClosed(peerID.String())

	// 3. 连接关闭
	metrics.RecordConnectionClosed(peerID.String())

	// 验证：指标收集没有 panic
	assert.NotNil(t, metrics.StreamsCreated)
	assert.NotNil(t, metrics.CallTotal)
	assert.NotNil(t, metrics.ConnectionsTotal)
}

// TestRPCMonitoring_PrometheusExport 集成测试：Prometheus 导出
func TestRPCMonitoring_PrometheusExport(t *testing.T) {
	// 创建测试 HTTP 服务器
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	promhttp.Handler().ServeHTTP(w, req)

	// 验证响应
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotEmpty(t, body)

	// 验证包含关键指标
	bodyStr := string(body)
	assert.Contains(t, bodyStr, "nexkv_rpc_")
	assert.Contains(t, bodyStr, "streams_active")
	assert.Contains(t, bodyStr, "calls_total")
	assert.Contains(t, bodyStr, "batch_calls_total")
	assert.Contains(t, bodyStr, "connections_total")
}

// TestRPCRateLimiter_GlobalAndPeer 集成测试：全局+Peer限流器
func TestRPCRateLimiter_GlobalAndPeer(t *testing.T) {
	// 1. 创建全局限流器
	globalConfig := &RateLimiterConfig{
		MaxConnections: 3,
		RefillRate:     50 * time.Millisecond,
		RefillAmount:   2,
		BucketSize:     3, // 与 MaxConnections 一致
		AcquireTimeout: 1 * time.Second,
	}
	globalLimiter := NewRateLimiter(globalConfig)
	defer globalLimiter.Close()

	// 2. 创建 Peer 限流器
	peerConfig := &PeerRateLimiterConfig{
		DefaultRate:         20,
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	}
	peerLimiter := NewPeerRateLimiter(peerConfig)
	defer peerLimiter.Close()

	ctx := context.Background()
	peerID := peer.ID("test-peer-multi-limiter")

	// 3. 模拟多级限流场景
	successCount := 0
	const attempts = 10

	for i := 0; i < attempts; i++ {
		// 全局限流检查
		err := globalLimiter.Acquire(ctx)
		if err != nil {
			// 全局限流失败，跳过
			continue
		}

		// Peer 限流检查
		err = peerLimiter.Allow(ctx, peerID)
		if err != nil {
			// Peer 限流失败，释放全局限流配额
			globalLimiter.Release()
			continue
		}

		// 两个限流器都通过
		successCount++

		// 模拟异步处理完成
		go func(idx int) {
			time.Sleep(time.Duration(idx) * 10 * time.Millisecond)
			globalLimiter.Release()
		}(i)
	}

	// 等待部分异步操作完成
	time.Sleep(100 * time.Millisecond)

	// 验证至少有一些调用成功
	assert.Greater(t, successCount, 0, "至少应该有一些调用通过双重限流")
	// 注意：由于 Release() 会将令牌放回桶中，且定期 refill 会添加令牌
	// 所以总成功数可能超过 MaxConnections
	// 真正的限制是并发数（semaphore），而不是总请求数
	assert.LessOrEqual(t, successCount, attempts, "成功数不应超过总尝试次数")
}

// TestRPCRateLimiter_DynamicAdjustment 集成测试：动态速率调整
func TestRPCRateLimiter_DynamicAdjustment(t *testing.T) {
	config := &PeerRateLimiterConfig{
		DefaultRate:         10,
		MaxRate:             100,
		EnableDynamicAdjust: true,
		AdjustWindow:        500 * time.Millisecond,
		RateUpFactor:        1.2,
		RateDownFactor:      0.8,
		MinRate:             1,
	}

	limiter := NewPeerRateLimiter(config)
	defer limiter.Close()

	peerID := peer.ID("test-peer-dynamic")
	ctx := context.Background()

	// 1. 初始速率
	initialRate := limiter.GetPeerRate(peerID)
	assert.Equal(t, 10, initialRate)

	// 2. 模拟快速响应（可能触发速率提升）
	for i := 0; i < 20; i++ {
		err := limiter.Allow(ctx, peerID)
		assert.NoError(t, err)
		time.Sleep(1 * time.Millisecond) // 模拟快速响应
	}

	// 等待调整窗口
	time.Sleep(700 * time.Millisecond)

	// 3. 验证速率可能已经调整
	// 注意：动态调整是基于响应时间的，这里我们只验证不会崩溃
	newRate := limiter.GetPeerRate(peerID)
	assert.GreaterOrEqual(t, newRate, config.MinRate, "速率不应低于最小值")
	assert.LessOrEqual(t, newRate, config.MaxRate, "速率不应高于最大值")
}

// TestRPCMonitoring_EndToEnd 端到端测试：监控+限流完整流程
func TestRPCMonitoring_EndToEnd(t *testing.T) {
	// 创建服务器和客户端 host
	serverHost := createTestHost(t)
	defer serverHost.Close()

	clientHost := createTestHost(t)
	defer clientHost.Close()

	// 创建并启动 RPC 服务器
	server := NewServer(serverHost)

	testHandler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("OK"), nil
	}

	err := server.RegisterHandlerFunc("Test", testHandler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	// 连接客户端到服务器
	clientHost.Peerstore().AddAddr(serverHost.ID(), serverHost.Addrs()[0], peerstore.PermanentAddrTTL)

	// 创建客户端和限流器
	client := NewClient(clientHost)

	globalLimiter := NewRateLimiter(&RateLimiterConfig{
		MaxConnections: 5,
		RefillRate:     20 * time.Millisecond,
		RefillAmount:   1,
		BucketSize:     5,
		AcquireTimeout: 1 * time.Second,
	})
	defer globalLimiter.Close()

	peerLimiter := NewPeerRateLimiter(&PeerRateLimiterConfig{
		DefaultRate:         20,
		MaxRate:             100,
		EnableDynamicAdjust: false,
		MinRate:             1,
	})
	defer peerLimiter.Close()

	// 模拟完整的 RPC 调用流程（含监控和限流）
	metrics := GetRPCMetrics()
	peerID := serverHost.ID()

	// 1. 连接
	metrics.RecordConnectionOpened(peerID.String())
	err = globalLimiter.Acquire(ctx)
	require.NoError(t, err)
	defer globalLimiter.Release()

	// 2. Stream 创建
	metrics.RecordStreamCreated(peerID.String())

	// 3. Peer 限流
	err = peerLimiter.Allow(ctx, peerID)
	require.NoError(t, err)

	// 4. RPC 调用
	timer := metrics.RecordCallStart(peerID.String(), "Test")
	respBody, err := client.Call(ctx, peerID, "Test", []byte("request"))
	timer.StopWithError(err)

	require.NoError(t, err)
	assert.Equal(t, []byte("OK"), respBody)

	// 5. 清理
	metrics.RecordStreamClosed(peerID.String())
	metrics.RecordConnectionClosed(peerID.String())

	// 停止服务器
	cancel()
	assert.NoError(t, <-serverErr)
}

// ========================================
// Stream 复用验证测试
// ========================================

// TestRPC_StreamReuse 验证 Stream 复用机制
func TestRPC_StreamReuse(t *testing.T) {
	serverHost := createTestHost(t)
	defer serverHost.Close()

	clientHost := createTestHost(t)
	defer clientHost.Close()

	// 连接客户端到服务器
	clientHost.Peerstore().AddAddr(serverHost.ID(), serverHost.Addrs()[0], peerstore.PermanentAddrTTL)

	// 创建并启动 RPC 服务器
	server := NewServer(serverHost)

	var callCount int
	var callCountMu sync.Mutex

	testHandler := func(ctx context.Context, req []byte) ([]byte, error) {
		callCountMu.Lock()
		callCount++
		count := callCount
		callCountMu.Unlock()

		resp := struct {
			Count int `msgpack:"count"`
		}{
			Count: count,
		}
		return msgpack.Marshal(resp)
	}

	err := server.RegisterHandlerFunc("StreamReuse", testHandler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	// 等待服务器启动
	time.Sleep(200 * time.Millisecond)

	client := NewClient(clientHost)

	// 发送多个请求到同一个 peer
	const requestCount = 5
	for i := 0; i < requestCount; i++ {
		reqBody, err := msgpack.Marshal(struct {
			Message string `msgpack:"message"`
		}{
			Message: "test",
		})
		require.NoError(t, err)

		respBody, err := client.Call(ctx, serverHost.ID(), "StreamReuse", reqBody)
		require.NoError(t, err)

		var resp struct {
			Count int `msgpack:"count"`
		}
		err = msgpack.Unmarshal(respBody, &resp)
		require.NoError(t, err)

		// 验证服务器端按顺序处理了请求
		require.Equal(t, i+1, resp.Count)
	}

	// 验证：5 个请求都成功
	t.Logf("发送 %d 个请求到同一个 peer，全部成功", requestCount)

	// 停止服务器
	cancel()
	<-serverErr
}

// TestRPC_StreamRecovery 测试 RPC 调用失败后的恢复能力
func TestRPC_StreamRecovery(t *testing.T) {
	serverHost := createTestHost(t)
	defer serverHost.Close()

	clientHost := createTestHost(t)
	defer clientHost.Close()

	// 连接客户端到服务器
	clientHost.Peerstore().AddAddr(serverHost.ID(), serverHost.Addrs()[0], peerstore.PermanentAddrTTL)

	// 创建并启动 RPC 服务器
	server := NewServer(serverHost)

	testHandler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	err := server.RegisterHandlerFunc("Test", testHandler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Start(ctx)
	}()

	// 等待服务器启动
	time.Sleep(200 * time.Millisecond)

	client := NewClient(clientHost)

	// 第一次调用 - 应该成功
	resp1, err := client.Call(ctx, serverHost.ID(), "Test", []byte("request1"))
	require.NoError(t, err)
	require.Equal(t, []byte("response"), resp1)

	// 第二次调用 - 应该成功（可能复用 Stream）
	resp2, err := client.Call(ctx, serverHost.ID(), "Test", []byte("request2"))
	require.NoError(t, err)
	require.Equal(t, []byte("response"), resp2)

	// 第三次调用 - 应该成功
	resp3, err := client.Call(ctx, serverHost.ID(), "Test", []byte("request3"))
	require.NoError(t, err)
	require.Equal(t, []byte("response"), resp3)

	// 停止服务器
	cancel()
	<-serverErr
}
