// Package transport 实现传输层基础设施
package transport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ============================================================================

// mockTaskPoolProvider 模拟 TaskPoolProvider（用于测试）
// 直接使用 go func() 执行任务，不使用任务池
type mockTaskPoolProvider struct{}

func newMockTaskPoolProvider() service.TaskExecutor {
	return &mockTaskPoolProvider{}
}

func (m *mockTaskPoolProvider) Submit(ctx context.Context, priority service.TaskPriority, task func(context.Context)) error {
	go task(ctx)
	return nil
}

func (m *mockTaskPoolProvider) Close() error {
	return nil
}

// ============================================================================
// Mock Transport
// ============================================================================

// mockTransport 模拟 Transport
type mockTransport struct {
	self      model.PeerID
	connected map[model.PeerID]bool
	mu        sync.RWMutex
}

func newMockTransport(self model.PeerID) *mockTransport {
	return &mockTransport{
		self:      self,
		connected: make(map[model.PeerID]bool),
	}
}

func (t *mockTransport) Self() model.PeerID { return t.self }
func (t *mockTransport) Connect(ctx context.Context, addr string) (model.PeerID, error) {
	return "", nil
}
func (t *mockTransport) Disconnect(peer model.PeerID) error {
	t.mu.Lock()
	delete(t.connected, peer)
	t.mu.Unlock()
	return nil
}
func (t *mockTransport) ConnectedPeers() []model.PeerID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	peers := make([]model.PeerID, 0, len(t.connected))
	for p := range t.connected {
		peers = append(peers, p)
	}
	return peers
}
func (t *mockTransport) IsConnected(peer model.PeerID) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connected[peer]
}
func (t *mockTransport) OpenStream(ctx context.Context, peer model.PeerID, protocol string) (service.Stream, error) {
	return nil, service.ErrPeerUnreachable
}
func (t *mockTransport) AcceptStream(protocol string) (service.Stream, error) {
	return nil, nil
}
func (t *mockTransport) OpenChannel(ctx context.Context, peer model.PeerID, protocol string) (service.Channel, error) {
	return nil, service.ErrPeerUnreachable
}
func (t *mockTransport) Close() error { return nil }

// ============================================================================
// 测试辅助类型
// ============================================================================

// testRPCSetup 测试 RPC 设置辅助结构
type testRPCSetup struct {
	transport *mockTransport
	rpc       *Libp2pRPC
	t         *testing.T
}

// newTestRPC 创建测试 RPC
func newTestRPC(t *testing.T, nodeID model.PeerID, config *service.RPCConfig) *testRPCSetup {
	transport := newMockTransport(nodeID)
	provider := newMockTaskPoolProvider()
	rpc := NewLibp2pRPC(transport, provider, config)
	return &testRPCSetup{
		transport: transport,
		rpc:       rpc,
		t:         t,
	}
}

// close 清理资源
func (s *testRPCSetup) close() {
	if err := s.rpc.Close(); err != nil {
		s.t.Logf("Warning: failed to close RPC: %v", err)
	}
}

// createTestMessage 创建测试消息的辅助函数
func createTestMessage(id string, msgType model.MessageType, payload []byte) *model.BaseMessage {
	return model.NewMessage(id, msgType, "node-1", "node-2", payload)
}

// createTestMessageWithPeer 创建指定节点的测试消息
func createTestMessageWithPeer(id string, msgType model.MessageType, source, target model.PeerID, payload []byte) *model.BaseMessage {
	return model.NewMessage(id, msgType, source, target, payload)
}

// ============================================================================
// 测试断言辅助函数
// ============================================================================

// assertError asserts that an error occurred
func assertError(t *testing.T, err error, expected error, msgAndArgs ...any) {
	t.Helper()
	if err == nil {
		t.Errorf("Expected error %v, got nil. %v", expected, msgAndArgs)
		return
	}
	if expected != nil && err != expected {
		t.Errorf("Expected error %v, got %v. %v", expected, err, msgAndArgs)
	}
}

// assertNoError asserts that no error occurred
func assertNoError(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()
	if err != nil {
		t.Errorf("Unexpected error: %v. %v", err, msgAndArgs)
	}
}

// assertEqual asserts that two values are equal
func assertEqual(t *testing.T, expected, actual any, msgAndArgs ...any) {
	t.Helper()
	if expected != actual {
		t.Errorf("Expected %v, got %v. %v", expected, actual, msgAndArgs)
	}
}

// assertNotEqual asserts that two values are not equal
func assertNotEqual(t *testing.T, expected, actual any, msgAndArgs ...any) {
	t.Helper()
	if expected == actual {
		t.Errorf("Expected %v to not equal %v. %v", expected, actual, msgAndArgs)
	}
}

// assertTrue asserts that a condition is true
func assertTrue(t *testing.T, condition bool, msgAndArgs ...any) {
	t.Helper()
	if !condition {
		t.Errorf("Expected condition to be true. %v", msgAndArgs)
	}
}

// assertFalse asserts that a condition is false
func assertFalse(t *testing.T, condition bool, msgAndArgs ...any) {
	t.Helper()
	if condition {
		t.Errorf("Expected condition to be false. %v", msgAndArgs)
	}
}

// ============================================================================
// 等待辅助函数
// ============================================================================

// waitWithTimeout 等待条件满足或超时
func waitWithTimeout(t *testing.T, timeout time.Duration, condition func() bool, msgAndArgs ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("Timeout waiting for condition. %v", msgAndArgs)
}
