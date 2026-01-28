// Package transport RPC 单元测试
//
// 合并说明：
//   - 原 rpc_client_test.go（6 个测试）
//   - 原 rpc_server_test.go（5 个测试）
//
// 本文件包含 RPC 客户端和服务端的单元测试，使用 Mock Transport
package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// Mock 实现
// ========================================

// mockMessageForRPC 模拟消息
type mockMessageForRPC struct {
	msgType       types.MessageType
	priority      int
	role          types.MsgRole
	protocolType  types.ProtocolType
	correlationID string // 支持动态 CorrelationID（用于 RPC 测试）
}

func (m *mockMessageForRPC) Type() types.MessageType {
	return m.msgType
}

func (m *mockMessageForRPC) Priority() int {
	return m.priority
}

func (m *mockMessageForRPC) MsgRole() types.MsgRole {
	return m.role
}

func (m *mockMessageForRPC) ExpectResponse() types.ResponseExpectation {
	return types.ExpectResponse
}

func (m *mockMessageForRPC) ProtocolType() types.ProtocolType {
	// 如果没有显式设置协议类型，根据消息类型推断
	if m.protocolType != "" {
		return m.protocolType
	}
	// Gossip 相关消息使用 UDP
	switch m.msgType {
	case types.MessageTypeGossipSync, types.MessageTypeGossipSyncReply,
		types.MessageTypeGossipDigest, types.MessageTypeGossipDigestReply,
		types.MessageTypeNodePing:
		return types.ProtocolUDP
	default:
		return types.ProtocolTCP
	}
}

func (m *mockMessageForRPC) CorrelationID() string {
	if m.correlationID != "" {
		return m.correlationID
	}
	return "mock-correlation-id"
}

// mockRPCHandler 模拟 RPC 处理器
type mockRPCHandler struct {
	mu             sync.Mutex
	handledReqs    []types.Message
	handleDelay    time.Duration
	handleError    error
	responseMsg    types.Message
	returnResponse bool
}

func (m *mockRPCHandler) HandleRequest(ctx context.Context, req types.Message) (types.Message, error) {
	if m.handleDelay > 0 {
		time.Sleep(m.handleDelay)
	}

	m.mu.Lock()
	m.handledReqs = append(m.handledReqs, req)
	m.mu.Unlock()

	if m.handleError != nil {
		return nil, m.handleError
	}

	if m.returnResponse && m.responseMsg != nil {
		// 创建响应副本并复制请求的 CorrelationID（避免 race condition）
		// 多个 goroutine 可能同时调用 HandleRequest，直接修改共享的 m.responseMsg 会导致数据竞争
		if mockResp, ok := m.responseMsg.(*mockMessageForRPC); ok {
			respCopy := &mockMessageForRPC{
				msgType:       mockResp.msgType,
				correlationID: req.CorrelationID(),
			}
			return respCopy, nil
		}
		// 其他类型消息直接返回（不需要修改 CorrelationID）
		return m.responseMsg, nil
	}

	return nil, nil
}

// mockTransportForRPC 模拟传输层（用于客户端测试）
type mockTransportForRPC struct {
	mu        sync.Mutex
	sendCh    chan *mockSendRPC
	receiveCh chan MsgFrame
	started   bool
	stopped   bool // 标记是否已停止
	sendDelay time.Duration
	sendError error
}

type mockSendRPC struct {
	ctx  context.Context
	addr string
	msg  types.Message
}

func newMockTransportForRPC() *mockTransportForRPC {
	return &mockTransportForRPC{
		sendCh:    make(chan *mockSendRPC, 100),
		receiveCh: make(chan MsgFrame, 100),
	}
}

func (m *mockTransportForRPC) Start(nodeID *uint64, msgSeqGenerator func() uint64, listenAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

func (m *mockTransportForRPC) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started || m.stopped {
		return nil // 已经停止，直接返回
	}

	m.started = false
	m.stopped = true

	// 关闭 channel（不管是否有数据）
	close(m.sendCh)
	close(m.receiveCh)

	return nil
}

func (m *mockTransportForRPC) Send(ctx context.Context, addr string, msg Message, opt ...SendOpt) error {
	m.mu.Lock()
	sendDelay := m.sendDelay
	sendError := m.sendError
	m.mu.Unlock()

	if sendDelay > 0 {
		time.Sleep(sendDelay)
	}

	if sendError != nil {
		return sendError
	}

	select {
	case m.sendCh <- &mockSendRPC{ctx, addr, msg}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *mockTransportForRPC) Reply(ctx context.Context, addr string, msg Message, nodeID uint64, msgSeq uint64, connID string, opt ...SendOpt) error {
	// Mock 实现：直接调用 Send（忽略 nodeID、msgSeq 和 connID）
	return m.Send(ctx, addr, msg, opt...)
}

func (m *mockTransportForRPC) Receive() <-chan MsgFrame {
	return m.receiveCh
}

func (m *mockTransportForRPC) ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
	return 0, fmt.Errorf("not implemented")
}

func (m *mockTransportForRPC) GetNodeID() uint64 {
	return 1
}

func (m *mockTransportForRPC) GenerateMsgSeq() uint64 {
	return 1
}

// mockTransportForServer 模拟传输层（用于服务端测试）
type mockTransportForServer struct {
	mu        sync.Mutex
	started   bool
	receiveCh chan MsgFrame
}

func newMockTransportForServer() *mockTransportForServer {
	return &mockTransportForServer{
		receiveCh: make(chan MsgFrame, 100),
	}
}

func (m *mockTransportForServer) Start(nodeID *uint64, msgSeqGenerator func() uint64, listenAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

func (m *mockTransportForServer) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = false
	close(m.receiveCh)
	return nil
}

func (m *mockTransportForServer) Send(ctx context.Context, addr string, msg Message, opt ...SendOpt) error {
	return nil
}

func (m *mockTransportForServer) Reply(ctx context.Context, addr string, msg Message, nodeID uint64, msgSeq uint64, connID string, opt ...SendOpt) error {
	// Mock 实现：直接返回成功（忽略参数）
	return nil
}

func (m *mockTransportForServer) Receive() <-chan MsgFrame {
	return m.receiveCh
}

func (m *mockTransportForServer) ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
	return 0, fmt.Errorf("not implemented")
}

func (m *mockTransportForServer) GetNodeID() uint64 {
	return 1
}

func (m *mockTransportForServer) GenerateMsgSeq() uint64 {
	return 1
}

// ========================================
// 测试辅助函数
// ========================================
// RPC 客户端测试
// ========================================

// TestNewRPCClient 测试创建客户端
func TestNewRPCClient(t *testing.T) {
	tests := []struct {
		name         string
		tcpTransport Transport
		udpTransport Transport
		config       *RPCClientConfig
		wantErr      bool
	}{
		{
			name:         "默认配置",
			tcpTransport: newMockTransportForRPC(),
			udpTransport: nil,
			config:       nil,
			wantErr:      false,
		},
		{
			name:         "自定义配置",
			tcpTransport: newMockTransportForRPC(),
			udpTransport: newMockTransportForRPC(),
			config: &RPCClientConfig{
				RequestTimeout: 10 * time.Second,
				EnableFastFail: false,
			},
			wantErr: false,
		},
		{
			name:         "nil TCP transport",
			tcpTransport: nil,
			udpTransport: nil,
			config:       nil,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewRPCClient(tt.tcpTransport, tt.udpTransport, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRPCClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Error("NewRPCClient() returned nil client")
			}
		})
	}
}

// TestRPCClientStartStop 测试启动和停止
func TestRPCClientStartStop(t *testing.T) {
	tcpTransport := newMockTransportForRPC()
	client, err := NewRPCClient(tcpTransport, nil, nil)
	if err != nil {
		t.Fatalf("NewRPCClient() failed: %v", err)
	}

	// 启动
	if err := client.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// 重复启动应该失败
	if err := client.Start(); err == nil {
		t.Error("Expected error when starting already running client")
	}

	// 停止 Transport（关闭 channel，让 responseLoop 退出）
	if err := tcpTransport.Stop(); err != nil {
		t.Fatalf("Transport.Stop() failed: %v", err)
	}

	// 停止客户端
	if err := client.Stop(); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// 重复停止应该失败
	if err := client.Stop(); err == nil {
		t.Error("Expected error when stopping already stopped client")
	}
}

// TestCallBatchFastFail 测试快速失败机制（P1-2 优化）
func TestCallBatchFastFail(t *testing.T) {
	tcpTransport := newMockTransportForRPC()
	config := &RPCClientConfig{
		RequestTimeout:  5 * time.Second,
		EnableFastFail:  true,
		FastFailTimeout: 1 * time.Second,
	}

	client, err := NewRPCClient(tcpTransport, nil, config)
	if err != nil {
		t.Fatalf("NewRPCClient() failed: %v", err)
	}

	if err := client.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := tcpTransport.Stop(); err != nil {
			t.Errorf("tcpTransport.Stop() failed: %v", err)
		}
		if err := client.Stop(); err != nil {
			t.Errorf("client.Stop() failed: %v", err)
		}
	}()

	// 创建批量请求（其中一个会失败）
	requests := []*RPCBatchRequest{
		{
			Addr:    "127.0.0.1:9211",
			Message: &mockMessageForRPC{msgType: types.MessageTypeGet},
		},
		{
			Addr:    "127.0.0.1:9212",
			Message: &mockMessageForRPC{msgType: types.MessageTypeGet},
		},
		{
			Addr:    "127.0.0.1:9213", // 这个会失败
			Message: &mockMessageForRPC{msgType: types.MessageTypeGet},
		},
	}

	// 模拟第三个请求失败
	go func() {
		time.Sleep(100 * time.Millisecond)
		tcpTransport.mu.Lock()
		tcpTransport.sendError = errors.New("connection refused")
		tcpTransport.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 调用 CallBatch（应该快速失败）
	startTime := time.Now()
	_, err = client.CallBatch(ctx, requests)
	elapsed := time.Since(startTime)

	// 应该快速失败（< 1 秒）
	if err == nil {
		t.Error("Expected error from CallBatch with fast-fail")
	}

	if elapsed > 2*time.Second {
		t.Errorf("CallBatch took too long: %v, expected fast-fail < 2s", elapsed)
	}

	t.Logf("CallBatch fast-failed in %v", elapsed)
}

// TestCallBatchWaitAll 测试等待所有请求完成
func TestCallBatchWaitAll(t *testing.T) {
	tcpTransport := newMockTransportForRPC()
	config := &RPCClientConfig{
		RequestTimeout: 5 * time.Second,
		EnableFastFail: false, // 禁用快速失败
	}

	client, err := NewRPCClient(tcpTransport, nil, config)
	if err != nil {
		t.Fatalf("NewRPCClient() failed: %v", err)
	}

	if err := client.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := tcpTransport.Stop(); err != nil {
			t.Errorf("tcpTransport.Stop() failed: %v", err)
		}
		if err := client.Stop(); err != nil {
			t.Errorf("client.Stop() failed: %v", err)
		}
	}()

	// 创建批量请求
	requests := []*RPCBatchRequest{
		{
			Addr:    "127.0.0.1:9211",
			Message: &mockMessageForRPC{msgType: types.MessageTypeGet},
		},
		{
			Addr:    "127.0.0.1:9212",
			Message: &mockMessageForRPC{msgType: types.MessageTypeGet},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 调用 CallBatch（等待所有请求）
	_, err = client.CallBatch(ctx, requests)

	// 由于没有实际响应，最终会超时
	if err == nil {
		t.Error("Expected timeout error from CallBatch")
	}

	t.Logf("CallBatch waited for all requests: %v", err)
}

// TestRequestTable 测试请求表
func TestRequestTable(t *testing.T) {
	rt := newRequestTable()

	// 添加请求（相信 add() 永远不会返回 nil，否则会 panic）
	entry1 := rt.add("correlation-1")
	entry2 := rt.add("correlation-2")

	// 查找请求
	got := rt.get("correlation-1")
	if got != entry1 {
		t.Error("get() returned wrong entry")
	}

	// 删除请求（P1-1: 仅标记完成时间，不立即删除）
	rt.remove("correlation-1")
	got = rt.get("correlation-1")
	// 条目应该仍然存在，但已完成
	if got == nil {
		t.Error("entry should still exist (marked as completed)")
	}
	if got != nil && got.completedAt.IsZero() {
		t.Error("entry should be marked as completed")
	}

	// 取消所有请求
	cancelCh1 := entry1.cancelCh
	cancelCh2 := entry2.cancelCh
	rt.cancelAll()

	select {
	case <-cancelCh1:
		// 已取消
	case <-time.After(100 * time.Millisecond):
		t.Error("entry1 should be canceled")
	}

	select {
	case <-cancelCh2:
		// 已取消
	case <-time.After(100 * time.Millisecond):
		t.Error("entry2 should be canceled")
	}
}

// TestSelectTransport 测试传输协议选择
func TestSelectTransport(t *testing.T) {
	tcpTransport := newMockTransportForRPC()
	udpTransport := newMockTransportForRPC()

	client, err := NewRPCClient(tcpTransport, udpTransport, nil)
	if err != nil {
		t.Fatalf("NewRPCClient() failed: %v", err)
	}

	tests := []struct {
		name        string
		msg         types.Message
		expectedTCP bool
		expectedUDP bool
	}{
		{
			name: "TCP 消息",
			msg: &mockMessageForRPC{
				msgType: types.MessageTypeGet,
			},
			expectedTCP: true,
			expectedUDP: false,
		},
		{
			name: "UDP 消息",
			msg: &mockMessageForRPC{
				msgType: types.MessageTypeGossipDigest,
			},
			expectedTCP: false,
			expectedUDP: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := client.selectTransport(tt.msg)

			isTCP := transport == tcpTransport
			isUDP := transport == udpTransport

			if tt.expectedTCP && !isTCP {
				t.Error("Expected TCP transport")
			}
			if tt.expectedUDP && !isUDP {
				t.Error("Expected UDP transport")
			}
		})
	}
}

// ========================================
// RPC 服务端测试
// ========================================

// TestNewRPCServer 测试创建服务端
func TestNewRPCServer(t *testing.T) {
	tcpTransport := newMockTransportForServer()
	handler := &mockRPCHandler{}

	tests := []struct {
		name         string
		tcpTransport Transport
		udpTransport Transport
		handler      RPCHandler
		config       *RPCServerConfig
		wantErr      bool
	}{
		{
			name:         "默认配置",
			tcpTransport: tcpTransport,
			udpTransport: nil,
			handler:      handler,
			config:       nil,
			wantErr:      false,
		},
		{
			name:         "自定义配置",
			tcpTransport: tcpTransport,
			udpTransport: newMockTransportForServer(),
			handler:      handler,
			config: &RPCServerConfig{
				WorkerCount:    16,
				QueueSize:      5000,
				RequestTimeout: 10 * time.Second,
			},
			wantErr: false,
		},
		{
			name:         "nil TCP transport",
			tcpTransport: nil,
			udpTransport: nil,
			handler:      handler,
			config:       nil,
			wantErr:      true,
		},
		{
			name:         "nil handler",
			tcpTransport: tcpTransport,
			udpTransport: nil,
			handler:      nil,
			config:       nil,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewRPCServer(tt.tcpTransport, tt.udpTransport, tt.handler, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRPCServer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && server == nil {
				t.Error("NewRPCServer() returned nil server")
			}
		})
	}
}

// TestRPCServerStartStop 测试启动和停止
func TestRPCServerStartStop(t *testing.T) {
	tcpTransport := newMockTransportForServer()
	handler := &mockRPCHandler{}

	server, err := NewRPCServer(tcpTransport, nil, handler, nil)
	if err != nil {
		t.Fatalf("NewRPCServer() failed: %v", err)
	}

	// 启动
	if err := server.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// 重复启动应该失败
	if err := server.Start(); err == nil {
		t.Error("Expected error when starting already running server")
	}

	// 等待一下确保启动完成
	time.Sleep(100 * time.Millisecond)

	// 停止
	if err := server.Stop(); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// 重复停止应该失败
	if err := server.Stop(); err == nil {
		t.Error("Expected error when stopping already stopped server")
	}
}

// TestRPCServerDualTransport 测试双 Transport 支持
func TestRPCServerDualTransport(t *testing.T) {
	tcpTransport := newMockTransportForServer()
	udpTransport := newMockTransportForServer()
	handler := &mockRPCHandler{}

	server, err := NewRPCServer(tcpTransport, udpTransport, handler, nil)
	if err != nil {
		t.Fatalf("NewRPCServer() failed: %v", err)
	}

	if err := server.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Errorf("server.Stop() failed: %v", err)
		}
	}()

	// 验证两个 Transport 都已注册
	stats := server.GetStats()
	if !stats.Running {
		t.Error("Expected server to be running")
	}

	t.Logf("Dual transport server started successfully")
}

// TestGetServerStats 测试统计信息
func TestGetServerStats(t *testing.T) {
	tcpTransport := newMockTransportForServer()
	handler := &mockRPCHandler{}

	server, err := NewRPCServer(tcpTransport, nil, handler, nil)
	if err != nil {
		t.Fatalf("NewRPCServer() failed: %v", err)
	}

	// 启动前
	stats := server.GetStats()
	if stats.Running {
		t.Error("Expected Running=false before Start()")
	}

	// 启动后
	if err := server.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Errorf("server.Stop() failed: %v", err)
		}
	}()

	stats = server.GetStats()
	if !stats.Running {
		t.Error("Expected Running=true after Start()")
	}
	if stats.WorkerCount != 8 { // 默认配置是 8 个 worker
		t.Errorf("WorkerCount = %d, want 8", stats.WorkerCount)
	}
}

// TestConcurrentStartStop 并发启动停止
func TestConcurrentStartStop(t *testing.T) {
	tcpTransport := newMockTransportForServer()
	handler := &mockRPCHandler{}

	server, err := NewRPCServer(tcpTransport, nil, handler, nil)
	if err != nil {
		t.Fatalf("NewRPCServer() failed: %v", err)
	}

	// 并发启动
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = server.Start()
		}()
	}
	wg.Wait()

	// 并发停止
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = server.Stop()
		}()
	}
	wg.Wait()

	// 最终状态应该是已停止
	stats := server.GetStats()
	if stats.Running {
		t.Error("Expected server to be stopped after concurrent Stop() calls")
	}
}
