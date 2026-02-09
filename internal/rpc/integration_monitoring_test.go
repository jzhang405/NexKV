// Package rpc 基于 libp2p Stream 的 RPC 实现
// 监控指标收集和导出测试
package rpc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
