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

// ============================================================================
// 额外测试（提高覆盖率）
// ============================================================================

// TestLibp2pRPC_Call_Timeout 测试调用超时
func TestLibp2pRPC_Call_Timeout(t *testing.T) {
	transport := newMockTransport("node-1")
	// 模拟节点已连接
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, &service.RPCConfig{
		CallTimeout: 100 * time.Millisecond,
	})
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	ctx := context.Background()
	_, err := rpc.Call(ctx, "node-2", msg)

	// 由于 mock 无法真正发送，应该返回错误
	if err == nil {
		t.Error("Call() should return error when transport fails")
	}
}

// TestLibp2pRPC_Call_ContextCanceled 测试上下文取消
func TestLibp2pRPC_Call_ContextCanceled(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, err := rpc.Call(ctx, "node-2", msg)
	if err == nil {
		t.Error("Call() should return error when context is canceled")
	}
}

// TestLibp2pRPC_CallAsync_NilCallback 测试异步调用空回调
func TestLibp2pRPC_CallAsync_NilCallback(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	err := rpc.CallAsync(context.Background(), "node-2", msg, nil)
	if err != nil {
		t.Errorf("CallAsync() with nil callback error = %v", err)
	}
}

// TestLibp2pRPC_BroadcastCall_EmptyPeers 测试广播到空节点列表
func TestLibp2pRPC_BroadcastCall_EmptyPeers(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	ctx := context.Background()
	result, err := rpc.BroadcastCall(ctx, []model.PeerID{}, msg, service.ResponseAll, nil)

	if err != nil {
		t.Errorf("BroadcastCall() with empty peers error = %v", err)
	}

	if len(result.FailedPeers) != 0 {
		t.Errorf("FailedPeers = %d, want 0", len(result.FailedPeers))
	}
}

// TestLibp2pRPC_BroadcastAsync_NilCallback 测试异步广播空回调
func TestLibp2pRPC_BroadcastAsync_NilCallback(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	err := rpc.BroadcastAsync(context.Background(), peers, msg, service.ResponseNone, nil, nil)
	if err != nil {
		t.Errorf("BroadcastAsync() with nil callback error = %v", err)
	}

	// 等待 goroutine 完成
	time.Sleep(50 * time.Millisecond)
}

// TestLibp2pRPC_WriteV_EmptyTargets 测试批量写入空目标
func TestLibp2pRPC_WriteV_EmptyTargets(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msgs := []model.Message{
		model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("msg1")),
	}

	err := rpc.WriteV(context.Background(), []model.PeerID{}, msgs, nil)
	// 空目标应该返回错误
	if err == nil {
		t.Error("WriteV() with empty targets should return error")
	}
}

// TestLibp2pRPC_WriteVCall_EmptyTargets 测试批量调用空目标
func TestLibp2pRPC_WriteVCall_EmptyTargets(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msgs := []model.Message{
		model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("msg1")),
	}

	_, err := rpc.WriteVCall(context.Background(), []model.PeerID{}, msgs, service.ResponseAll, nil)
	// 空目标应该返回错误
	if err == nil {
		t.Error("WriteVCall() with empty targets should return error")
	}
}

// TestLibp2pRPC_OnRequest_NilHandler 测试注册空处理器
func TestLibp2pRPC_OnRequest_NilHandler(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	err := rpc.OnRequest(nil)
	if err != nil {
		t.Errorf("OnRequest() with nil handler error = %v", err)
	}
}

// TestBroadcastTracker_WaitFull_AllFailed 测试全部失败场景
func TestBroadcastTracker_WaitFull_AllFailed(t *testing.T) {
	targets := []model.PeerID{"node-1", "node-2"}
	tracker := service.NewBroadcastTracker("test-001", targets)

	// 全部标记为失败
	tracker.RecordFailure("node-1", service.ErrTimeout)
	tracker.RecordFailure("node-2", service.ErrTimeout)

	// WaitFull 应该立即返回
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := tracker.WaitFull(ctx)
	if err != nil {
		t.Errorf("WaitFull() after all failed error = %v", err)
	}

	if !tracker.IsFullDone() {
		t.Error("IsFullDone() = false after all failed")
	}
}

// TestBroadcastTracker_Stats_AfterOperations 测试操作后统计
func TestBroadcastTracker_Stats_AfterOperations(t *testing.T) {
	targets := []model.PeerID{"node-1", "node-2", "node-3"}
	tracker := service.NewBroadcastTracker("test-001", targets)

	// 混合操作
	tracker.RecordSuccess("node-1", nil)
	tracker.RecordFailure("node-2", service.ErrTimeout)

	success, failed, pending := tracker.Stats()
	if success != 1 {
		t.Errorf("Stats() success = %d, want 1", success)
	}
	if failed != 1 {
		t.Errorf("Stats() failed = %d, want 1", failed)
	}
	if pending != 1 {
		t.Errorf("Stats() pending = %d, want 1", pending)
	}
}

// TestRequestIDGenerator_Concurrent 测试并发生成 ID
func TestRequestIDGenerator_Concurrent(t *testing.T) {
	gen := service.NewRequestIDGenerator("node-001")

	var wg sync.WaitGroup
	ids := make(chan service.RequestID, 1000)
	idMap := make(map[string]bool)
	var mu sync.Mutex

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				id := gen.Next()
				ids <- id
			}
		}()
	}

	go func() {
		wg.Wait()
		close(ids)
	}()

	for id := range ids {
		mu.Lock()
		idStr := string(id)
		if idMap[idStr] {
			t.Errorf("Duplicate ID generated: %s", idStr)
		}
		idMap[idStr] = true
		mu.Unlock()
	}

	if len(idMap) != 1000 {
		t.Errorf("Generated %d IDs, want 1000", len(idMap))
	}
}

// ============================================================================
// validateStrategy 和 cleanNilResponses 测试
// ============================================================================

// TestValidateStrategy_ResponseAll 测试 ResponseAll 策略
func TestValidateStrategy_ResponseAll(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		success  int
		failed   int
		wantErr  bool
	}{
		{"all success", 3, 3, 0, false},
		{"one failed", 3, 2, 1, true},
		{"all failed", 3, 0, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStrategy(service.ResponseAll, tt.total, tt.success, tt.failed)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStrategy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateStrategy_ResponseMajority 测试 ResponseMajority 策略
func TestValidateStrategy_ResponseMajority(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		success  int
		failed   int
		wantErr  bool
	}{
		{"majority reached (2/3)", 3, 2, 1, false},
		{"majority not reached (1/3)", 3, 1, 2, true},
		{"all success", 3, 3, 0, false},
		{"all failed", 3, 0, 3, true},
		{"5 nodes, 3 success", 5, 3, 2, false},
		{"5 nodes, 2 success", 5, 2, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStrategy(service.ResponseMajority, tt.total, tt.success, tt.failed)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStrategy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateStrategy_ResponseNone 测试 ResponseNone 策略
func TestValidateStrategy_ResponseNone(t *testing.T) {
	// ResponseNone 应该永远返回 nil
	err := validateStrategy(service.ResponseNone, 3, 0, 3)
	if err != nil {
		t.Errorf("validateStrategy(ResponseNone) error = %v, want nil", err)
	}
}

// TestCleanNilResponses 测试清理 nil 响应
func TestCleanNilResponses(t *testing.T) {
	tests := []struct {
		name     string
		input    []model.Message
		wantLen  int
	}{
		{"all nil", []model.Message{nil, nil, nil}, 0},
		{"no nil", []model.Message{
			model.NewMessage("1", model.MessageTypeResponse, "a", "b", []byte("1")),
			model.NewMessage("2", model.MessageTypeResponse, "a", "b", []byte("2")),
		}, 2},
		{"mixed", []model.Message{
			model.NewMessage("1", model.MessageTypeResponse, "a", "b", []byte("1")),
			nil,
			model.NewMessage("2", model.MessageTypeResponse, "a", "b", []byte("2")),
			nil,
		}, 2},
		{"empty", []model.Message{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanNilResponses(tt.input)
			if len(result) != tt.wantLen {
				t.Errorf("cleanNilResponses() len = %d, want %d", len(result), tt.wantLen)
			}
		})
	}
}

// TestLibp2pRPC_CallAsync_ConnectedPeer 测试异步调用已连接节点
func TestLibp2pRPC_CallAsync_ConnectedPeer(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	var callbackErr error
	var callbackMu sync.Mutex
	cb := func(resp model.Message, err error) {
		callbackMu.Lock()
		callbackErr = err
		callbackMu.Unlock()
	}

	err := rpc.CallAsync(context.Background(), "node-2", msg, cb)
	if err != nil {
		t.Fatalf("CallAsync() error = %v", err)
	}

	// 等待回调执行
	time.Sleep(200 * time.Millisecond)

	callbackMu.Lock()
	err = callbackErr
	callbackMu.Unlock()

	// 由于 mock 无法真正通信，应该返回错误
	if err == nil {
		t.Error("CallAsync() callback error = nil, want error")
	}
}

// TestLibp2pRPC_BroadcastCall_AllStrategies 测试所有广播策略
func TestLibp2pRPC_BroadcastCall_AllStrategies(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	// ResponseAll 和 ResponseMajority 会等待响应
	strategies := []service.ResponseStrategy{
		service.ResponseAll,
		service.ResponseMajority,
	}

	strategyNames := []string{"ResponseAll", "ResponseMajority"}

	for i, strategy := range strategies {
		t.Run(strategyNames[i], func(t *testing.T) {
			ctx := context.Background()
			_, err := rpc.BroadcastCall(ctx, peers, msg, strategy, nil)
			// 应该返回错误（mock 无法通信，节点未连接）
			if err == nil {
				t.Error("BroadcastCall() should return error with mock transport")
			}
		})
	}
}

// TestLibp2pRPC_BroadcastCall_ContextTimeout 测试广播超时
func TestLibp2pRPC_BroadcastCall_ContextTimeout(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, &service.RPCConfig{
		CallTimeout: 100 * time.Millisecond,
	})
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := rpc.BroadcastCall(ctx, peers, msg, service.ResponseAll, nil)
	if err == nil {
		t.Error("BroadcastCall() should return error on timeout")
	}
}

// TestLibp2pRPC_CloseCh 测试关闭通道
func TestLibp2pRPC_CloseCh(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)

	// 关闭前应该返回一个可用的通道
	ch := rpc.CloseCh()
	if ch == nil {
		t.Error("CloseCh() returned nil before close")
	}

	// 通道应该未关闭
	select {
	case <-ch:
		t.Error("CloseCh() should not be closed yet")
	default:
		// 正确
	}

	// 关闭 RPC
	rpc.Close()

	// 通道应该已关闭
	select {
	case <-ch:
		// 正确
	default:
		t.Error("CloseCh() should be closed after RPC.Close()")
	}
}

// TestLibp2pRPC_HandleResponse 测试响应处理
func TestLibp2pRPC_HandleResponse(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	// 注册一个待处理的调用
	requestID := "test-req-001"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	call := rpc.registerPendingCall(requestID, ctx)

	// 发送响应
	go func() {
		time.Sleep(10 * time.Millisecond)
		rpc.handleResponse(requestID, service.ResponseMsg{
			Msg: model.NewMessage("resp-001", model.MessageTypeResponse, "node-2", "node-1", []byte("response")),
			Err: nil,
		})
	}()

	// 等待响应
	select {
	case resp := <-call.responseCh:
		if resp.Err != nil {
			t.Errorf("handleResponse() error = %v", resp.Err)
		}
		if resp.Msg == nil {
			t.Error("handleResponse() returned nil message")
		}
	case <-ctx.Done():
		t.Error("timeout waiting for response")
	}

	rpc.unregisterPendingCall(requestID)
}

// TestLibp2pRPC_HandleResponse_Timeout 测试响应超时
func TestLibp2pRPC_HandleResponse_Timeout(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	// 注册一个待处理的调用，超时很短
	requestID := "test-req-timeout"
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	call := rpc.registerPendingCall(requestID, ctx)

	// 不发送响应，等待超时
	select {
	case <-call.responseCh:
		t.Error("should not receive response")
	case <-ctx.Done():
		// 正确 - 超时
	}

	rpc.unregisterPendingCall(requestID)
}

// TestLibp2pRPC_HandleResponse_UnknownID 测试未知请求 ID 的响应
func TestLibp2pRPC_HandleResponse_UnknownID(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	// 发送一个不存在请求 ID 的响应（不应该 panic）
	rpc.handleResponse("unknown-id", service.ResponseMsg{
		Msg: nil,
		Err: service.ErrTimeout,
	})
	// 如果没有 panic，测试通过
}

// TestAsyncCommon_Context 测试 AsyncLifecycle 的 Context
func TestAsyncCommon_Context(t *testing.T) {
	lifecycle := NewAsyncLifecycle()

	// 测试 Context
	ctx := lifecycle.Context()
	if ctx == nil {
		t.Error("Context() returned nil")
	}
}

// TestValidateBufferSize 测试缓冲区大小验证
func TestValidateBufferSize(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"valid size", 100, 100},
		{"too small", 10, DefaultAsyncBufferSize},
		{"too large", 20 * 1024 * 1024, MaxAsyncBufferSize},
		{"negative", -1, DefaultAsyncBufferSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateBufferSize(tt.input)
			if result != tt.expected {
				t.Errorf("ValidateBufferSize(%d) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

// TestValidateTimeout 测试超时验证
func TestValidateTimeout(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{"valid timeout", 30 * time.Second, 30 * time.Second},
		{"zero timeout", 0, DefaultTimeout},
		{"negative timeout", -1 * time.Second, DefaultTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateTimeout(tt.input)
			if result != tt.expected {
				t.Errorf("ValidateTimeout(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestValidateMaxMessageSize 测试最大消息大小验证
func TestValidateMaxMessageSize(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"valid size", 1024, 1024},
		{"zero size", 0, DefaultMaxMessageSize},
		{"negative size", -1, DefaultMaxMessageSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateMaxMessageSize(tt.input)
			if result != tt.expected {
				t.Errorf("ValidateMaxMessageSize(%d) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}
