// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

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
