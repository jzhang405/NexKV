package transport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

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
func (t *mockTransport) OpenAsyncChannel(ctx context.Context, peer model.PeerID, protocol string) (service.AsyncChannel, error) {
	return nil, service.ErrPeerUnreachable
}
func (t *mockTransport) OpenAsyncStream(ctx context.Context, peer model.PeerID, protocol string) (service.AsyncStream, error) {
	return nil, service.ErrPeerUnreachable
}
func (t *mockTransport) Close() error { return nil }

// TestLibp2pRPC_New 测试创建 RPC
func TestLibp2pRPC_New(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)

	if rpc == nil {
		t.Fatal("NewLibp2pRPC returned nil")
	}

	if rpc.codec == nil {
		t.Error("codec should not be nil")
	}

	if rpc.config == nil {
		t.Error("config should not be nil")
	}

	_ = rpc.Close()
}

// TestLibp2pRPC_New_WithConfig 测试使用自定义配置创建 RPC
func TestLibp2pRPC_New_WithConfig(t *testing.T) {
	transport := newMockTransport("node-1")
	config := &service.RPCConfig{
		CallTimeout:        10 * time.Second,
		BroadcastTimeout:   30 * time.Second,
		MaxConcurrentCalls: 500,
	}

	rpc := NewLibp2pRPC(transport, config)

	if rpc.config.CallTimeout != 10*time.Second {
		t.Errorf("CallTimeout = %v, want 10s", rpc.config.CallTimeout)
	}

	_ = rpc.Close()
}

// TestLibp2pRPC_Call_PeerUnreachable 测试调用未连接的节点
func TestLibp2pRPC_Call_PeerUnreachable(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	ctx := context.Background()
	_, err := rpc.Call(ctx, "node-2", msg)

	if err != service.ErrPeerUnreachable {
		t.Errorf("Call() error = %v, want ErrPeerUnreachable", err)
	}
}

// TestLibp2pRPC_CallAsync 测试异步调用
func TestLibp2pRPC_CallAsync(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	var callbackCalled bool
	var callbackMu sync.Mutex
	cb := func(resp model.Message, err error) {
		callbackMu.Lock()
		callbackCalled = true
		callbackMu.Unlock()
	}

	err := rpc.CallAsync(context.Background(), "node-2", msg, cb)
	if err != nil {
		t.Fatalf("CallAsync() error = %v", err)
	}

	// 等待回调执行
	time.Sleep(100 * time.Millisecond)

	callbackMu.Lock()
	called := callbackCalled
	callbackMu.Unlock()

	if !called {
		t.Error("Callback was not called")
	}
}

// TestLibp2pRPC_OnRequest 测试注册请求处理器
func TestLibp2pRPC_OnRequest(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	handler := func(ctx context.Context, from model.PeerID, req model.Message) model.Message {
		return model.NewMessage("resp-001", model.MessageTypeResponse, "node-1", from, []byte("response"))
	}

	err := rpc.OnRequest(handler)
	if err != nil {
		t.Fatalf("OnRequest() error = %v", err)
	}
}

// TestLibp2pRPC_OnRequestChan 测试请求通道
func TestLibp2pRPC_OnRequestChan(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	ch := rpc.OnRequestChan()
	if ch == nil {
		t.Error("OnRequestChan() returned nil")
	}
}

// TestLibp2pRPC_BroadcastCall_ResponseNone 测试单向广播
func TestLibp2pRPC_BroadcastCall_ResponseNone(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3", "node-4"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	ctx := context.Background()
	result, err := rpc.BroadcastCall(ctx, peers, msg, service.ResponseNone, nil)

	if err != nil {
		t.Fatalf("BroadcastCall() error = %v", err)
	}

	// 单向广播不等待响应
	if len(result.FailedPeers) != 3 {
		t.Errorf("FailedPeers = %d, want 3 (all should fail - no connection)", len(result.FailedPeers))
	}
}

// TestLibp2pRPC_WriteV_LengthMismatch 测试 WriteV 长度不匹配
func TestLibp2pRPC_WriteV_LengthMismatch(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	targets := []model.PeerID{"node-2", "node-3"}
	msgs := []model.Message{
		model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("msg1")),
	}

	err := rpc.WriteV(context.Background(), targets, msgs, nil)
	if err == nil {
		t.Error("WriteV() should return error for length mismatch")
	}
}

// TestLibp2pRPC_WriteVCall_LengthMismatch 测试 WriteVCall 长度不匹配
func TestLibp2pRPC_WriteVCall_LengthMismatch(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	targets := []model.PeerID{"node-2", "node-3"}
	msgs := []model.Message{
		model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("msg1")),
	}

	_, err := rpc.WriteVCall(context.Background(), targets, msgs, service.ResponseAll, nil)
	if err == nil {
		t.Error("WriteVCall() should return error for length mismatch")
	}
}

// TestLibp2pRPC_Close 测试关闭 RPC
func TestLibp2pRPC_Close(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)

	// 关闭
	err := rpc.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// 再次关闭应该无错误
	err = rpc.Close()
	if err != nil {
		t.Fatalf("Second Close() error = %v", err)
	}

	// 关闭后调用应该返回错误
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))
	_, err = rpc.Call(context.Background(), "node-2", msg)
	if err != service.ErrCanceled {
		t.Errorf("Call() after close error = %v, want ErrCanceled", err)
	}
}

// TestLibp2pRPC_GetMiddleware 测试获取中间件链
func TestLibp2pRPC_GetMiddleware(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	mw := rpc.GetMiddleware()
	if mw == nil {
		t.Error("GetMiddleware() returned nil")
	}
}

// TestLibp2pRPC_BroadcastCall_InvalidStrategy 测试无效策略
func TestLibp2pRPC_BroadcastCall_InvalidStrategy(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	_, err := rpc.BroadcastCall(context.Background(), peers, msg, service.ResponseStrategy(99), nil)
	if err != service.ErrInvalidStrategy {
		t.Errorf("BroadcastCall() with invalid strategy error = %v, want ErrInvalidStrategy", err)
	}
}

// TestLibp2pRPC_BroadcastAsync 测试异步广播
func TestLibp2pRPC_BroadcastAsync(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	var callbackCount int
	var callbackMu sync.Mutex
	cb := func(from model.PeerID, resp model.Message, err error) {
		callbackMu.Lock()
		callbackCount++
		callbackMu.Unlock()
	}

	err := rpc.BroadcastAsync(context.Background(), peers, msg, service.ResponseNone, nil, cb)
	if err != nil {
		t.Fatalf("BroadcastAsync() error = %v", err)
	}

	// 等待回调执行
	time.Sleep(200 * time.Millisecond)

	callbackMu.Lock()
	count := callbackCount
	callbackMu.Unlock()

	if count == 0 {
		t.Error("No callbacks were called")
	}
}

// TestLibp2pRPC_ImplementsInterface 验证接口实现
func TestLibp2pRPC_ImplementsInterface(t *testing.T) {
	transport := newMockTransport("node-1")
	var _ service.RPC = NewLibp2pRPC(transport, nil)
}

// ============================================================================
// RequestIDGenerator 测试（从 transport.go 转移）
// ============================================================================

func TestRequestIDGenerator_Next(t *testing.T) {
	gen := service.NewRequestIDGenerator("node-001")

	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := gen.Next()
		idStr := string(id)

		if ids[idStr] {
			t.Errorf("Duplicate ID generated: %s", idStr)
		}
		ids[idStr] = true
	}
}

func TestRequestIDGenerator_Parse(t *testing.T) {
	gen := service.NewRequestIDGenerator("node-001")
	id := gen.Next()

	nodeID, timestamp, sequence, err := service.ParseRequestID(id)
	if err != nil {
		t.Fatalf("ParseRequestID() error = %v", err)
	}

	if nodeID != "node-001" {
		t.Errorf("nodeID = %s, want node-001", nodeID)
	}
	if timestamp == 0 {
		t.Error("timestamp should not be 0")
	}
	if sequence == 0 {
		t.Error("sequence should not be 0")
	}
}

func TestRequestIDGenerator_Time(t *testing.T) {
	gen := service.NewRequestIDGenerator("node-001")
	id := gen.Next()

	tm := id.Time()
	if tm.IsZero() {
		t.Error("Time() should not return zero time")
	}
}

func TestRequestIDGenerator_ParseInvalid(t *testing.T) {
	_, _, _, err := service.ParseRequestID("invalid-id")
	if err == nil {
		t.Error("ParseRequestID() with invalid ID should return error")
	}
}

// ============================================================================
// BroadcastTracker 测试
// ============================================================================

func TestBroadcastTracker_New(t *testing.T) {
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := service.NewBroadcastTracker("test-001", targets)

	if tracker == nil {
		t.Fatal("NewBroadcastTracker returned nil")
	}

	success, failed, pending := tracker.Stats()
	if success != 0 || failed != 0 || pending != 3 {
		t.Errorf("Stats() = (%d, %d, %d), want (0, 0, 3)", success, failed, pending)
	}
}

func TestBroadcastTracker_WaitMajority_EmptyTargets(t *testing.T) {
	tracker := service.NewBroadcastTracker("test-001", []model.PeerID{})

	ctx := context.Background()
	err := tracker.WaitMajority(ctx)
	if err != nil {
		t.Errorf("WaitMajority() with empty targets error = %v", err)
	}
}

func TestBroadcastTracker_IsMajorityReached(t *testing.T) {
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := service.NewBroadcastTracker("test-001", targets)

	if tracker.IsMajorityReached() {
		t.Error("IsMajorityReached() = true, want false")
	}

	// 记录 2 个成功（2/3 = majority）
	tracker.RecordSuccess("node-1", nil)
	tracker.RecordSuccess("node-2", nil)

	if !tracker.IsMajorityReached() {
		t.Error("IsMajorityReached() = false after 2/3 success, want true")
	}
}

func TestBroadcastTracker_IsFullDone(t *testing.T) {
	targets := []model.PeerID{"node-1", "node-2"}
	tracker := service.NewBroadcastTracker("test-001", targets)

	if tracker.IsFullDone() {
		t.Error("IsFullDone() = true, want false")
	}

	// 记录所有响应
	tracker.RecordSuccess("node-1", nil)
	tracker.RecordFailure("node-2", service.ErrTimeout)

	if !tracker.IsFullDone() {
		t.Error("IsFullDone() = false after all responses, want true")
	}
}

func TestBroadcastTracker_WaitFull_ContextCancellation(t *testing.T) {
	targets := []model.PeerID{"node-1", "node-2"}
	tracker := service.NewBroadcastTracker("test-001", targets)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err := tracker.WaitFull(ctx)
	if err != context.Canceled {
		t.Errorf("WaitFull() with canceled context error = %v, want context.Canceled", err)
	}
}

func TestBroadcastTracker_RecordSuccess_ClosesMajorityChannel(t *testing.T) {
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := service.NewBroadcastTracker("test-001", targets)

	// 记录 2 个成功（达到 majority）
	tracker.RecordSuccess("node-1", nil)
	tracker.RecordSuccess("node-2", nil)

	// WaitMajority 应该立即返回
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := tracker.WaitMajority(ctx)
	if err != nil {
		t.Errorf("WaitMajority() after majority reached error = %v", err)
	}
}

func TestBroadcastTracker_ConcurrentRecord(t *testing.T) {
	targets := []model.PeerID{"node-1", "node-2", "node-3", "node-4", "node-5"}
	tracker := service.NewBroadcastTracker("test-001", targets)

	var wg sync.WaitGroup

	// 并发记录成功
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			peer := targets[idx]
			tracker.RecordSuccess(peer, nil)
		}(i)
	}

	wg.Wait()

	// 验证所有记录都成功
	success, failed, pending := tracker.Stats()
	if success != 5 {
		t.Errorf("Stats() success = %d, want 5", success)
	}
	if failed != 0 {
		t.Errorf("Stats() failed = %d, want 0", failed)
	}
	if pending != 0 {
		t.Errorf("Stats() pending = %d, want 0", pending)
	}

	// 验证 IsFullDone
	if !tracker.IsFullDone() {
		t.Error("IsFullDone() = false, want true")
	}
}
