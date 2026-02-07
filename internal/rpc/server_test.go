// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// TestNewServer 测试创建 RPC 服务器
func TestNewServer(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	server := NewServer(host)

	assert.NotNil(t, server)
	assert.Equal(t, host, server.host)
	assert.NotNil(t, server.router)
	assert.NotNil(t, server.codec)
	assert.False(t, server.IsRunning())
}

// TestServer_StartStop 测试服务器启动和停止
func TestServer_StartStop(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	server := NewServer(host)

	ctx, cancel := context.WithCancel(context.Background())

	// 启动服务器
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)
	assert.True(t, server.IsRunning())

	// 停止服务器
	cancel()
	err := <-errChan
	assert.NoError(t, err)
	assert.False(t, server.IsRunning())
}

// TestServer_StartTwice 测试重复启动
func TestServer_StartTwice(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	server := NewServer(host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 第一次启动
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	assert.True(t, server.IsRunning())

	// 第二次启动应该失败
	err := server.Start(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已在运行")

	// 清理
	cancel()
	<-errChan
}

// TestServer_StopBeforeStart 测试未启动就停止
func TestServer_StopBeforeStart(t *testing.T) {
	host := createTestHost(t)
	server := NewServer(host)

	err := server.Stop()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未运行")
}

// TestServer_RegisterHandler 测试注册处理器
func TestServer_RegisterHandler(t *testing.T) {
	host := createTestHost(t)
	server := NewServer(host)

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	err := server.RegisterHandler("Test", handler)
	assert.NoError(t, err)

	// 验证处理器已注册
	assert.True(t, server.router.HasMethod("test")) // 方法名会被转为小写
}

// TestServer_RegisterHandlerFunc 测试注册函数作为处理器
func TestServer_RegisterHandlerFunc(t *testing.T) {
	host := createTestHost(t)
	server := NewServer(host)

	err := server.RegisterHandlerFunc("Test", func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	})
	assert.NoError(t, err)

	assert.True(t, server.router.HasMethod("test"))
}

// TestServer_RegisterDefaultHandlers 测试注册默认处理器
func TestServer_RegisterDefaultHandlers(t *testing.T) {
	host := createTestHost(t)
	server := NewServer(host)

	err := server.RegisterDefaultHandlers()
	assert.NoError(t, err)

	// 验证 Ping 方法已注册
	assert.True(t, server.router.HasMethod("ping"))
}

// TestServer_handlePing 测试 Ping 处理器
func TestServer_handlePing(t *testing.T) {
	host := createTestHost(t)
	server := NewServer(host)

	// 创建 Ping 请求
	req := &PingRequest{
		Timestamp: time.Now().UnixNano(),
	}
	reqBytes, err := msgpack.Marshal(req)
	require.NoError(t, err)

	// 调用 handlePing
	respBytes, err := server.handlePing(context.Background(), reqBytes)
	require.NoError(t, err)

	// 解析响应
	var resp PingResponse
	err = msgpack.Unmarshal(respBytes, &resp)
	require.NoError(t, err)

	// 验证时间戳匹配
	assert.Equal(t, req.Timestamp, resp.Timestamp)
}

// TestRPCClientServer 测试完整的 RPC 调用流程
// 注意：此测试暂时跳过，需要 libp2p 网络连接
func TestRPCClientServer(t *testing.T) {
	t.Skip("requires libp2p network connection - integration test")
}

// TestRPCClientServer_Ping 测试 Ping 功能
// 注意：此测试暂时跳过，需要 libp2p 网络连接
func TestRPCClientServer_Ping(t *testing.T) {
	t.Skip("requires libp2p network connection - integration test")
}

// TestRouter 测试 Router 功能
func TestRouter(t *testing.T) {
	router := NewRouter()

	// 测试注册处理器
	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	err := router.RegisterHandler("Test", handler)
	assert.NoError(t, err)
	assert.True(t, router.HasMethod("test"))

	// 测试路由
	resp, err := router.Route("test", context.Background(), []byte("request"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("response"), resp)

	// 测试列出方法
	methods := router.ListMethods()
	assert.Contains(t, methods, "test")

	// 测试重复注册
	err = router.RegisterHandler("test", handler)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")

	// 测试未注册的方法
	_, err = router.Route("unknown", context.Background(), []byte("request"))
	assert.Error(t, err)
	assert.True(t, IsRPCError(err))
	assert.Equal(t, ErrCodeNotFound, GetRPCErrorCode(err))
}

// TestRPCHandlerWrapper 测试 RPCHandlerWrapper
func TestRPCHandlerWrapper(t *testing.T) {
	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	wrapper := NewRPCHandlerWrapper("test", handler)

	// 测试 Handle
	ctx := context.Background()
	resp, err := wrapper.Handle(ctx, []byte("request"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("response"), resp)

	// 测试带超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	resp, err = wrapper.Handle(ctx, []byte("request"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("response"), resp)
}

// TestDecodeRPCRequest 测试 RPC 请求解码
func TestDecodeRPCRequest(t *testing.T) {
	req := &RPCRequest{
		Method:    "TestMethod",
		RequestID: 12345,
		Body:      []byte("test body"),
		Timeout:   30 * time.Second,
	}

	data, err := msgpack.Marshal(req)
	require.NoError(t, err)

	decoded, err := UnmarshalRPCRequest(data)
	require.NoError(t, err)

	assert.Equal(t, req.Method, decoded.Method)
	assert.Equal(t, req.RequestID, decoded.RequestID)
	assert.Equal(t, req.Body, decoded.Body)
}

// TestEncodeRPCResponse 测试 RPC 响应编码
func TestEncodeRPCResponse(t *testing.T) {
	resp := &RPCResponse{
		RequestID: 12345,
		Status:    ErrCodeSuccess,
		Body:      []byte("test body"),
	}

	data, err := MarshalRPCResponse(resp)
	require.NoError(t, err)

	var decoded RPCResponse
	err = msgpack.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.RequestID, decoded.RequestID)
	assert.Equal(t, resp.Status, decoded.Status)
	assert.Equal(t, resp.Body, decoded.Body)
}

// TestNormalizeMethodName 测试方法名规范化
func TestNormalizeMethodName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Test", "test"},
		{"  Test  ", "test"},
		{"TEST", "test"},
		{"test", "test"},
		{"  TEST  ", "test"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeMethodName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetContextTimeout 测试获取上下文超时
func TestGetContextTimeout(t *testing.T) {
	// 无超时的上下文
	ctx := context.Background()
	timeout := getContextTimeout(ctx)
	assert.Equal(t, time.Duration(0), timeout)

	// 带超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	timeout = getContextTimeout(ctx)
	assert.Greater(t, timeout, time.Duration(0))
	assert.LessOrEqual(t, timeout, 30*time.Second)
}
