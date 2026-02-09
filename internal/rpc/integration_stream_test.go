// Package rpc 基于 libp2p Stream 的 RPC 实现
// Stream 复用和恢复测试
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
