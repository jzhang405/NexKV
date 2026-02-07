// Package rpc 基于 libp2p Stream 的 RPC 实现
package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/transport"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// TestNewClient 测试创建 RPC 客户端
func TestNewClient(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	client := NewClient(host)

	assert.NotNil(t, client)
	assert.Equal(t, host, client.host)
	assert.Equal(t, 30*time.Second, client.defaultTimeout)
	assert.Equal(t, uint16(transport.MaxMessageSize), client.maxMessageSize)
	assert.NotNil(t, client.codec)
}

// TestClient_SetDefaultTimeout 测试设置默认超时
func TestClient_SetDefaultTimeout(t *testing.T) {
	host := createTestHost(t)
	client := NewClient(host)

	newTimeout := 60 * time.Second
	client.SetDefaultTimeout(newTimeout)

	assert.Equal(t, newTimeout, client.defaultTimeout)
}

// TestClient_Call_Timeout 测试超时上下文处理
func TestClient_Call_Timeout(t *testing.T) {
	host := createTestHost(t)
	client := NewClient(host)

	// 设置已过期的上下文
	ctx, cancel := context.WithTimeout(context.Background(), -1*time.Hour)
	defer cancel()

	peerID := createTestPeerID(t)

	// 应该返回错误（连接失败）
	_, err := client.Call(ctx, peerID, "Test", []byte("test"))
	assert.Error(t, err)
}

// TestClient_CallStream 测试创建 Stream
func TestClient_CallStream(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	client := NewClient(host)
	ctx := context.Background()
	peerID := createTestPeerID(t)

	// CallStream 到不存在的节点会失败，但覆盖了方法路径
	stream, err := client.CallStream(ctx, peerID)
	assert.Error(t, err)
	assert.Nil(t, stream)
}

// TestPingRequestEncodeDecode 测试 Ping 请求编解码
func TestPingRequestEncodeDecode(t *testing.T) {
	req := &PingRequest{
		Timestamp: time.Now().UnixNano(),
	}

	data, err := msgpack.Marshal(req)
	require.NoError(t, err)

	var decoded PingRequest
	err = msgpack.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, req.Timestamp, decoded.Timestamp)
}

// TestPingResponseEncodeDecode 测试 Ping 响应编解码
func TestPingResponseEncodeDecode(t *testing.T) {
	resp := &PingResponse{
		Timestamp: time.Now().UnixNano(),
	}

	data, err := msgpack.Marshal(resp)
	require.NoError(t, err)

	var decoded PingResponse
	err = msgpack.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.Timestamp, decoded.Timestamp)
}

// TestRPCError 测试 RPC 错误
func TestRPCError(t *testing.T) {
	err := NewRPCError(ErrCodeTimeout, "timeout error")

	assert.True(t, IsRPCError(err))
	assert.Equal(t, ErrCodeTimeout, GetRPCErrorCode(err))
	assert.Contains(t, err.Error(), "timeout error")
}

// createTestHost 创建测试用 libp2p host
func createTestHost(t *testing.T) host.Host {
	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
	)
	require.NoError(t, err)
	return h
}

// createTestPeerID 创建测试用 peer ID
func createTestPeerID(t *testing.T) peer.ID {
	// 创建临时 host 以生成有效的 peer ID
	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
	)
	require.NoError(t, err)
	defer h.Close()
	return h.ID()
}

// ========================================
// 覆盖率提升测试
// ========================================

// TestClient_Ping 测试 Client Ping 方法
func TestClient_Ping(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	client := NewClient(host)
	ctx := context.Background()

	// Ping 到不存在的节点（会失败，但覆盖了 Ping 方法）
	_, err := client.Ping(ctx, createTestPeerID(t))
	assert.Error(t, err)
}

// TestRouter_GetHandler 测试获取处理器
func TestRouter_GetHandler(t *testing.T) {
	router := NewRouter()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	// 注册处理器
	err := router.RegisterHandler("test", handler)
	require.NoError(t, err)

	// 获取已注册的处理器
	retrievedHandler, exists := router.GetHandler("test")
	assert.True(t, exists)
	assert.NotNil(t, retrievedHandler)

	// 尝试获取不存在的处理器
	_, exists = router.GetHandler("nonexistent")
	assert.False(t, exists)
}

// TestRouter_UnregisterHandler 测试注销处理器
func TestRouter_UnregisterHandler(t *testing.T) {
	router := NewRouter()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	// 注册处理器
	err := router.RegisterHandler("test", handler)
	require.NoError(t, err)

	// 注销处理器（不返回错误）
	router.UnregisterHandler("test")

	// 验证处理器已被注销
	_, exists := router.GetHandler("test")
	assert.False(t, exists)

	// 尝试注销不存在的处理器（不会 panic）
	router.UnregisterHandler("nonexistent")
}

// TestRouter_Call 测试 Router 的 Call 方法
func TestRouter_Call(t *testing.T) {
	router := NewRouter()

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	err := router.RegisterHandler("test", handler)
	require.NoError(t, err)

	ctx := context.Background()
	resp, err := router.Call("test", ctx, []byte("request"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("response"), resp)
}

// TestServer_Stop 测试服务器停止
func TestServer_Stop(t *testing.T) {
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

	// 使用 Stop 方法停止服务器
	err := server.Stop()
	assert.NoError(t, err)
	assert.False(t, server.IsRunning())

	// 清理
	cancel()
	<-errChan
}

// TestNewNodeConstructors 测试 tree_coordinator_messages 构造函数
func TestNewNodeConstructors(t *testing.T) {
	// NewNodeJoinRequest
	joinReq := NewNodeJoinRequest("node-1", "/ip4/127.0.0.1/tcp/8080", 0)
	assert.Equal(t, "node-1", joinReq.NodeID)
	assert.Equal(t, "/ip4/127.0.0.1/tcp/8080", joinReq.Addr)
	assert.Equal(t, 0, joinReq.Role)
	assert.Greater(t, joinReq.Timestamp, int64(0))

	// NewNodeLeaveRequest
	leaveReq := NewNodeLeaveRequest("node-1")
	assert.Equal(t, "node-1", leaveReq.NodeID)
	assert.Greater(t, leaveReq.Timestamp, int64(0))

	// NewNodeReparentRequest
	reparentReq := NewNodeReparentRequest("child-1", "parent-new", "parent-old")
	assert.Equal(t, "child-1", reparentReq.ChildID)
	assert.Equal(t, "parent-new", reparentReq.NewParentID)
	assert.Equal(t, "parent-old", reparentReq.OldParentID)
	assert.Greater(t, reparentReq.Timestamp, int64(0))

	// NewClusterStatusRequest
	statusReq := NewClusterStatusRequest("node-1")
	assert.Equal(t, "node-1", statusReq.RequesterID)
	assert.Greater(t, statusReq.Timestamp, int64(0))

	// NewGossipTopologyChangeRequest
	gossipReq := NewGossipTopologyChangeRequest("add", "node-1", "parent-1", 1, 100)
	assert.Equal(t, "add", gossipReq.Operation)
	assert.Equal(t, "node-1", gossipReq.NodeID)
	assert.Equal(t, "parent-1", gossipReq.ParentID)
	assert.Equal(t, 1, gossipReq.Level)
	assert.Equal(t, uint64(100), gossipReq.Version)
	assert.Greater(t, gossipReq.Timestamp, int64(0))

	// NewClusterHealthFixRequest
	healthReq := NewClusterHealthFixRequest("node-1", "unreachable")
	assert.Equal(t, "node-1", healthReq.RequesterID)
	assert.Equal(t, "unreachable", healthReq.FixType)
	assert.Greater(t, healthReq.Timestamp, int64(0))
}

// TestServer_handleStream_ErrorPaths 测试 handleStream 的错误路径
func TestServer_handleStream_ErrorPaths(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	server := NewServer(host)

	// 注册一个处理器
	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return nil, NewRPCError(ErrCodeInternal, "test error")
	}
	err := server.RegisterHandlerFunc("test", handler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动服务器
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)

	// 确保服务器已启动
	assert.True(t, server.IsRunning())

	// 创建客户端连接
	clientHost := createTestHost(t)
	defer clientHost.Close()

	clientHost.Peerstore().AddAddr(host.ID(), host.Addrs()[0], peerstore.PermanentAddrTTL)

	client := NewClient(clientHost)

	// 测试调用返回错误的处理器
	_, err = client.Call(ctx, host.ID(), "test", []byte("request"))
	assert.Error(t, err)

	// 清理：停止服务器
	cancel()
	<-errChan
}

// TestServer_RegisterDefaultHandlers_FullPath 测试完整的默认处理器注册
func TestServer_RegisterDefaultHandlers_FullPath(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	server := NewServer(host)

	// 注册所有默认处理器
	err := server.RegisterDefaultHandlers()
	require.NoError(t, err)

	// 验证所有处理器都已注册
	assert.True(t, server.router.HasMethod("ping"))
	assert.True(t, server.router.HasMethod("nodeping"))
}

// TestMarshalUnmarshalRPCRequest 测试 RPC 请求序列化/反序列化
func TestMarshalUnmarshalRPCRequest(t *testing.T) {
	// 创建请求
	req := &RPCRequest{
		Method:    "TestMethod",
		RequestID: 12345,
		Body:      []byte("test body"),
		Timeout:   30 * time.Second,
	}

	// 序列化
	data, err := MarshalRPCRequest(req)
	require.NoError(t, err)
	assert.NotNil(t, data)

	// 反序列化
	decoded, err := UnmarshalRPCRequest(data)
	require.NoError(t, err)
	assert.Equal(t, req.Method, decoded.Method)
	assert.Equal(t, req.RequestID, decoded.RequestID)
	assert.Equal(t, req.Body, decoded.Body)
}

// TestMarshalUnmarshalRPCResponse 测试 RPC 响应序列化/反序列化
func TestMarshalUnmarshalRPCResponse(t *testing.T) {
	// 创建响应
	resp := &RPCResponse{
		RequestID: 12345,
		Status:    ErrCodeSuccess,
		Body:      []byte("response body"),
	}

	// 序列化
	data, err := MarshalRPCResponse(resp)
	require.NoError(t, err)
	assert.NotNil(t, data)

	// 反序列化
	decoded, err := UnmarshalRPCResponse(data)
	require.NoError(t, err)
	assert.Equal(t, resp.RequestID, decoded.RequestID)
	assert.Equal(t, resp.Status, decoded.Status)
	assert.Equal(t, resp.Body, decoded.Body)
}

// TestSetStreamDeadline 测试设置 Stream 超时
func TestSetStreamDeadline(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	// 创建测试 stream（使用 mock）
	stream, err := host.NewStream(context.Background(), host.ID(), transport.ProtocolNexKVRPC)
	if err != nil {
		// 如果无法创建 stream，跳过此测试
		t.Skip("无法创建测试 stream")
		return
	}
	defer stream.Close()

	// 测试设置超时
	deadline := time.Now().Add(30 * time.Second)
	err = setStreamDeadline(stream, deadline)
	assert.NoError(t, err)
}

// TestAbs 测试 abs 辅助函数
func TestAbs(t *testing.T) {
	assert.Equal(t, int64(0), abs(0))
	assert.Equal(t, int64(5), abs(5))
	assert.Equal(t, int64(5), abs(-5))
	assert.Equal(t, int64(100), abs(-100))
}

// TestValidateCallParams 测试参数验证
func TestValidateCallParams(t *testing.T) {
	// 正常情况
	err := validateCallParams(createTestPeerID(t), "TestMethod")
	assert.NoError(t, err)

	// 空 peer ID
	err = validateCallParams("", "TestMethod")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "无效的 peer ID")

	// 空方法名
	err = validateCallParams(createTestPeerID(t), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "方法名不能为空")

	// 方法名过长
	longMethod := string(make([]byte, 300))
	err = validateCallParams(createTestPeerID(t), longMethod)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "方法名过长")
}
