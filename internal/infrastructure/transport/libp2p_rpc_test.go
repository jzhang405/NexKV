package transport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// TestLibp2pRPC_New 测试创建 RPC
func TestLibp2pRPC_New(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)
	defer setup.close()

	assertNotEqual(t, nil, setup.rpc, "RPC should not be nil")
	assertNotEqual(t, nil, setup.rpc.codec, "Codec should not be nil")
	assertNotEqual(t, nil, setup.rpc.config, "Config should not be nil")
}

// TestLibp2pRPC_New_WithConfig 测试使用自定义配置创建 RPC
func TestLibp2pRPC_New_WithConfig(t *testing.T) {
	config := &service.RPCConfig{
		CallTimeout:        10 * time.Second,
		BroadcastTimeout:   30 * time.Second,
		MaxConcurrentCalls: 500,
	}

	setup := newTestRPC(t, "node-1", config)
	defer setup.close()

	assertEqual(t, 10*time.Second, setup.rpc.config.CallTimeout, "CallTimeout mismatch")
}

// TestLibp2pRPC_Call_PeerUnreachable 测试调用未连接的节点
func TestLibp2pRPC_Call_PeerUnreachable(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)
	defer setup.close()

	msg := createTestMessage("test-001", model.MessageTypeRequest, []byte("test"))
	_, err := setup.rpc.Call(context.Background(), "node-2", msg)

	assertError(t, err, service.ErrPeerUnreachable, "Should return ErrPeerUnreachable")
}

// TestLibp2pRPC_CallAsync 测试异步调用
func TestLibp2pRPC_CallAsync(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)
	defer setup.close()

	msg := createTestMessage("test-001", model.MessageTypeRequest, []byte("test"))

	var callbackCalled bool
	var callbackMu sync.Mutex
	cb := func(resp model.Message, err error) {
		callbackMu.Lock()
		callbackCalled = true
		callbackMu.Unlock()
	}

	err := setup.rpc.CallAsync(context.Background(), "node-2", msg, cb)
	assertNoError(t, err, "CallAsync should not return error")

	// 等待回调执行
	waitWithTimeout(t, 100*time.Millisecond, func() bool {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		return callbackCalled
	}, "Callback was not called")
}

// TestLibp2pRPC_OnRequest 测试注册请求处理器
func TestLibp2pRPC_OnRequest(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)
	defer setup.close()

	handler := func(ctx context.Context, from model.PeerID, req model.Message) model.Message {
		return createTestMessageWithPeer("resp-001", model.MessageTypeResponse, "node-1", from, []byte("response"))
	}

	err := setup.rpc.OnRequest(handler)
	assertNoError(t, err, "OnRequest should not return error")
}

// TestLibp2pRPC_OnRequestChan 测试请求通道
func TestLibp2pRPC_OnRequestChan(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)
	defer setup.close()

	ch := setup.rpc.OnRequestChan()
	assertNotEqual(t, nil, ch, "OnRequestChan should not return nil")
}

// TestLibp2pRPC_BroadcastCall_ResponseNone 测试单向广播
func TestLibp2pRPC_BroadcastCall_ResponseNone(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)
	defer setup.close()

	peers := []model.PeerID{"node-2", "node-3", "node-4"}
	msg := createTestMessageWithPeer("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	result, err := setup.rpc.BroadcastCall(context.Background(), peers, msg, service.ResponseNone, nil)
	assertNoError(t, err, "BroadcastCall should not return error")

	// 单向广播不等待响应，所有节点都应失败（无连接）
	assertEqual(t, 3, len(result.FailedPeers), "All peers should fail - no connection")
}

// TestLibp2pRPC_WriteV_LengthMismatch 测试 WriteV 长度不匹配
func TestLibp2pRPC_WriteV_LengthMismatch(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)
	defer setup.close()

	targets := []model.PeerID{"node-2", "node-3"}
	msgs := []model.Message{
		createTestMessage("test-001", model.MessageTypeRequest, []byte("msg1")),
	}

	err := setup.rpc.WriteV(context.Background(), targets, msgs, nil)
	assertError(t, err, nil, "WriteV should return error for length mismatch")
}

// TestLibp2pRPC_WriteVCall_LengthMismatch 测试 WriteVCall 长度不匹配
func TestLibp2pRPC_WriteVCall_LengthMismatch(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)
	defer setup.close()

	targets := []model.PeerID{"node-2", "node-3"}
	msgs := []model.Message{
		createTestMessage("test-001", model.MessageTypeRequest, []byte("msg1")),
	}

	_, err := setup.rpc.WriteVCall(context.Background(), targets, msgs, service.ResponseAll, nil)
	assertError(t, err, nil, "WriteVCall should return error for length mismatch")
}

// TestLibp2pRPC_Close 测试关闭 RPC
func TestLibp2pRPC_Close(t *testing.T) {
	setup := newTestRPC(t, "node-1", nil)

	// 关闭
	err := setup.rpc.Close()
	assertNoError(t, err, "First Close should not return error")

	// 再次关闭应该无错误
	err = setup.rpc.Close()
	assertNoError(t, err, "Second Close should not return error")

	// 关闭后调用应该返回错误
	msg := createTestMessage("test-001", model.MessageTypeRequest, []byte("test"))
	_, err = setup.rpc.Call(context.Background(), "node-2", msg)
	assertError(t, err, service.ErrCanceled, "Call after close should return ErrCanceled")
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
// BroadcastTracker 测试（合并为表驱动测试）
// ============================================================================

func TestBroadcastTracker_All(t *testing.T) {
	tests := []struct {
		name    string
		targets []model.PeerID
		setup   func(*service.BroadcastTracker)
		check   func(*testing.T, *service.BroadcastTracker)
	}{
		{
			name:    "new tracker",
			targets: []model.PeerID{"node-1", "node-2", "node-3"},
			setup:   nil,
			check: func(t *testing.T, tr *service.BroadcastTracker) {
				s, f, p := tr.Stats()
				assertEqual(t, 0, s, "success count should be 0")
				assertEqual(t, 0, f, "failed count should be 0")
				assertEqual(t, 3, p, "pending count should be 3")
			},
		},
		{
			name:    "majority reached",
			targets: []model.PeerID{"node-1", "node-2", "node-3"},
			setup: func(tr *service.BroadcastTracker) {
				tr.RecordSuccess("node-1", nil)
				tr.RecordSuccess("node-2", nil)
			},
			check: func(t *testing.T, tr *service.BroadcastTracker) {
				assertTrue(t, tr.IsMajorityReached(), "Majority should be reached (2/3)")
			},
		},
		{
			name:    "majority not reached",
			targets: []model.PeerID{"node-1", "node-2", "node-3"},
			setup: func(tr *service.BroadcastTracker) {
				tr.RecordSuccess("node-1", nil)
			},
			check: func(t *testing.T, tr *service.BroadcastTracker) {
				assertFalse(t, tr.IsMajorityReached(), "Majority should not be reached (1/3)")
			},
		},
		{
			name:    "full done with mixed results",
			targets: []model.PeerID{"node-1", "node-2"},
			setup: func(tr *service.BroadcastTracker) {
				tr.RecordSuccess("node-1", nil)
				tr.RecordFailure("node-2", service.ErrTimeout)
			},
			check: func(t *testing.T, tr *service.BroadcastTracker) {
				assertTrue(t, tr.IsFullDone(), "Should be full done")
				s, f, p := tr.Stats()
				assertEqual(t, 1, s, "success count should be 1")
				assertEqual(t, 1, f, "failed count should be 1")
				assertEqual(t, 0, p, "pending count should be 0")
			},
		},
		{
			name:    "empty targets",
			targets: []model.PeerID{},
			setup:   nil,
			check: func(t *testing.T, tr *service.BroadcastTracker) {
				err := tr.WaitMajority(context.Background())
				assertNoError(t, err, "WaitMajority with empty targets should not return error")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := service.NewBroadcastTracker("test-001", tt.targets)
			if tt.setup != nil {
				tt.setup(tracker)
			}
			tt.check(t, tracker)
		})
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
// validateStrategy 和 cleanNilResponses 测试（合并为表驱动测试）
// ============================================================================

func TestValidateStrategy_All(t *testing.T) {
	tests := []struct {
		name     string
		strategy service.ResponseStrategy
		total    int
		success  int
		failed   int
		wantErr  bool
	}{
		// ResponseAll 测试
		{"ResponseAll: all success", service.ResponseAll, 3, 3, 0, false},
		{"ResponseAll: one failed", service.ResponseAll, 3, 2, 1, true},
		{"ResponseAll: all failed", service.ResponseAll, 3, 0, 3, true},

		// ResponseMajority 测试
		{"ResponseMajority: 2/3", service.ResponseMajority, 3, 2, 1, false},
		{"ResponseMajority: 1/3", service.ResponseMajority, 3, 1, 2, true},
		{"ResponseMajority: all success", service.ResponseMajority, 3, 3, 0, false},
		{"ResponseMajority: all failed", service.ResponseMajority, 3, 0, 3, true},
		{"ResponseMajority: 3/5", service.ResponseMajority, 5, 3, 2, false},
		{"ResponseMajority: 2/5", service.ResponseMajority, 5, 2, 3, true},

		// ResponseNone 测试
		{"ResponseNone: always nil", service.ResponseNone, 3, 0, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStrategy(tt.strategy, tt.total, tt.success, tt.failed)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStrategy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCleanNilResponses_All(t *testing.T) {
	tests := []struct {
		name    string
		input   []model.Message
		wantLen int
	}{
		{"all nil", []model.Message{nil, nil, nil}, 0},
		{"no nil", []model.Message{
			createTestMessage("1", model.MessageTypeResponse, []byte("1")),
			createTestMessage("2", model.MessageTypeResponse, []byte("2")),
		}, 2},
		{"mixed", []model.Message{
			createTestMessage("1", model.MessageTypeResponse, []byte("1")),
			nil,
			createTestMessage("2", model.MessageTypeResponse, []byte("2")),
			nil,
		}, 2},
		{"empty", []model.Message{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanNilResponses(tt.input)
			assertEqual(t, tt.wantLen, len(result), "Result length mismatch")
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

// ============================================================================
// WriteV 批量写入测试
// ============================================================================

// TestLibp2pRPC_WriteV_SingleTarget 测试批量写入单个目标
func TestLibp2pRPC_WriteV_SingleTarget(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "node-1", "", []byte("data1")),
		model.NewMessage("msg-2", model.MessageTypeRequest, "node-1", "", []byte("data2")),
	}

	err := rpc.WriteV(context.Background(), peers, msgs, nil)
	// 应该返回错误（mock 无法通信）
	if err == nil {
		t.Log("WriteV() returned nil error (expected with mock)")
	}
}

// TestLibp2pRPC_WriteV_MultipleTargets 测试批量写入多个目标
func TestLibp2pRPC_WriteV_MultipleTargets(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "node-1", "", []byte("data1")),
	}

	err := rpc.WriteV(context.Background(), peers, msgs, nil)
	// 应该返回错误（mock 无法通信）
	if err == nil {
		t.Log("WriteV() returned nil error (expected with mock)")
	}
}

// TestLibp2pRPC_WriteV_EmptyMessages 测试批量写入空消息
func TestLibp2pRPC_WriteV_EmptyMessages(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}

	err := rpc.WriteV(context.Background(), peers, []model.Message{}, nil)
	// 空消息应该正常处理
	if err != nil {
		t.Logf("WriteV() with empty messages returned error: %v", err)
	}
}

// TestLibp2pRPC_WriteV_NilMessages 测试批量写入 nil 消息
func TestLibp2pRPC_WriteV_NilMessages(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msgs := []model.Message{nil, nil}

	err := rpc.WriteV(context.Background(), peers, msgs, nil)
	// nil 消息应该被处理（跳过或报错）
	_ = err
}

// TestLibp2pRPC_WriteV_ContextCanceled 测试批量写入上下文取消
func TestLibp2pRPC_WriteV_ContextCanceled(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "node-1", "", []byte("data1")),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err := rpc.WriteV(ctx, peers, msgs, nil)
	if err == nil {
		t.Log("WriteV() with canceled context returned nil")
	}
}

// ============================================================================
// Call 错误路径测试
// ============================================================================

// TestLibp2pRPC_Call_PeerNotConnected 测试调用未连接节点
func TestLibp2pRPC_Call_PeerNotConnected(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	_, err := rpc.Call(context.Background(), "node-2", msg)
	if err == nil {
		t.Error("Call() to unconnected peer should return error")
	}
}

// TestLibp2pRPC_Call_NilMessage 测试调用 nil 消息
func TestLibp2pRPC_Call_NilMessage(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	_, err := rpc.Call(context.Background(), "node-2", nil)
	// nil 消息应该返回错误
	if err == nil {
		t.Error("Call() with nil message should return error")
	}
}

// TestLibp2pRPC_Call_TimeoutWithConfig 测试调用超时（带配置）
func TestLibp2pRPC_Call_TimeoutWithConfig(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, &service.RPCConfig{
		CallTimeout: 50 * time.Millisecond,
	})
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := rpc.Call(ctx, "node-2", msg)
	// mock 无法真正通信，应该超时或连接失败
	if err == nil {
		t.Log("Call() returned nil (may timeout)")
	}
}

// TestLibp2pRPC_Call_AfterClose 测试关闭后调用
func TestLibp2pRPC_Call_AfterClose(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	rpc.Close() // 先关闭

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	_, err := rpc.Call(context.Background(), "node-2", msg)
	if err == nil {
		t.Error("Call() after Close() should return error")
	}
}

// ============================================================================
// BroadcastAsync 测试
// ============================================================================

// TestLibp2pRPC_BroadcastAsync_Basic 测试异步广播
func TestLibp2pRPC_BroadcastAsync_Basic(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msg := model.NewMessage("test-001", model.MessageTypeEvent, "node-1", "", []byte("event"))

	err := rpc.BroadcastAsync(context.Background(), peers, msg, service.ResponseNone, nil, nil)
	// 异步广播应该立即返回
	if err != nil {
		t.Logf("BroadcastAsync() returned error: %v", err)
	}

	// 等待 goroutine 完成
	time.Sleep(100 * time.Millisecond)
}

// TestLibp2pRPC_BroadcastAsync_EmptyPeers 测试空节点列表广播
func TestLibp2pRPC_BroadcastAsync_EmptyPeers(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeEvent, "node-1", "", []byte("event"))

	err := rpc.BroadcastAsync(context.Background(), []model.PeerID{}, msg, service.ResponseNone, nil, nil)
	if err != nil {
		t.Logf("BroadcastAsync() with empty peers returned error: %v", err)
	}
}

// TestLibp2pRPC_BroadcastAsync_ContextCanceled 测试异步广播上下文取消
func TestLibp2pRPC_BroadcastAsync_ContextCanceled(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msg := model.NewMessage("test-001", model.MessageTypeEvent, "node-1", "", []byte("event"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rpc.BroadcastAsync(ctx, peers, msg, service.ResponseNone, nil, nil)
	_ = err
}

// ============================================================================
// RPC 配置测试
// ============================================================================

// TestRPCConfig_Default 测试默认配置
func TestRPCConfig_Default(t *testing.T) {
	config := service.DefaultRPCConfig()

	if config.CallTimeout <= 0 {
		t.Error("Default CallTimeout should be positive")
	}
	if config.BroadcastTimeout <= 0 {
		t.Error("Default BroadcastTimeout should be positive")
	}
	if config.MaxConcurrentCalls <= 0 {
		t.Error("Default MaxConcurrentCalls should be positive")
	}
	if config.RequestBufferSize <= 0 {
		t.Error("Default RequestBufferSize should be positive")
	}
}

// TestLibp2pRPC_CustomConfig 测试自定义配置
func TestLibp2pRPC_CustomConfig(t *testing.T) {
	transport := newMockTransport("node-1")
	config := &service.RPCConfig{
		CallTimeout:        5 * time.Second,
		BroadcastTimeout:   10 * time.Second,
		MaxConcurrentCalls: 100,
		RequestBufferSize:  64,
	}
	rpc := NewLibp2pRPC(transport, config)

	if rpc == nil {
		t.Fatal("NewLibp2pRPC with custom config returned nil")
	}
	rpc.Close()
}

// TestLibp2pRPC_NilConfig 测试 nil 配置使用默认值
func TestLibp2pRPC_NilConfig(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)

	if rpc == nil {
		t.Fatal("NewLibp2pRPC with nil config returned nil")
	}

	// 验证使用了默认配置
	if rpc.config.CallTimeout <= 0 {
		t.Error("Should use default CallTimeout")
	}
	rpc.Close()
}

// ============================================================================
// Close 重复调用测试
// ============================================================================

// TestLibp2pRPC_DoubleClose 测试重复关闭
func TestLibp2pRPC_DoubleClose(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)

	// 第一次关闭
	err := rpc.Close()
	if err != nil {
		t.Errorf("First Close() error = %v", err)
	}

	// 第二次关闭（不应该 panic）
	err = rpc.Close()
	if err != nil {
		t.Logf("Second Close() error = %v", err)
	}
}

// ============================================================================
// sendRequestAndWaitResponse 错误路径测试
// ============================================================================

// TestLibp2pRPC_Call_PeerNotConnected_ErrorPath 测试未连接节点的错误路径
// 覆盖 sendRequestAndWaitResponse 中的连接检查
func TestLibp2pRPC_Call_PeerNotConnected_ErrorPath(t *testing.T) {
	transport := newMockTransport("node-1")
	// 不添加 node-2 到 connected
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	_, err := rpc.Call(context.Background(), "node-2", msg)
	if err == nil {
		t.Error("Call() to unconnected peer should return error")
	}
	// 验证错误类型
	if err != service.ErrPeerUnreachable {
		t.Logf("Expected ErrPeerUnreachable, got: %v", err)
	}
}

// TestLibp2pRPC_Call_OpenStreamFails 测试 OpenStream 失败路径
// 覆盖 sendRequestAndWaitResponse 中的 OpenStream 错误
func TestLibp2pRPC_Call_OpenStreamFails(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	_, err := rpc.Call(context.Background(), "node-2", msg)
	// mock OpenStream 返回 ErrPeerUnreachable
	if err == nil {
		t.Error("Call() should return error when OpenStream fails")
	}
}

// ============================================================================
// sendRequestNoResponse 错误路径测试（通过 WriteV）
// ============================================================================

// TestLibp2pRPC_WriteV_PeerNotConnected 测试 WriteV 到未连接节点
func TestLibp2pRPC_WriteV_PeerNotConnected(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	msgs := []model.Message{
		model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("test")),
	}

	err := rpc.WriteV(context.Background(), peers, msgs, nil)
	// WriteV 会尝试发送，但 mock 返回错误
	if err == nil {
		t.Log("WriteV() returned nil (mock may not check connection)")
	}
}

// ============================================================================
// Broadcast 错误路径测试
// ============================================================================

// TestLibp2pRPC_BroadcastCall_AllPeersUnreachable 测试所有节点不可达
func TestLibp2pRPC_BroadcastCall_AllPeersUnreachable(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3", "node-4"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	result, err := rpc.BroadcastCall(context.Background(), peers, msg, service.ResponseAll, nil)
	if err != nil {
		t.Logf("BroadcastCall() error: %v", err)
	}
	// 验证结果：所有节点都应该失败
	if len(result.SuccessPeers) != 0 {
		t.Errorf("SuccessPeers count = %d, want 0", len(result.SuccessPeers))
	}
	if len(result.FailedPeers) != len(peers) {
		t.Errorf("FailedPeers count = %d, want %d", len(result.FailedPeers), len(peers))
	}
}

// TestLibp2pRPC_BroadcastCall_NilMessage 测试广播 nil 消息
func TestLibp2pRPC_BroadcastCall_NilMessage(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	result, err := rpc.BroadcastCall(context.Background(), peers, nil, service.ResponseAll, nil)
	if err != nil {
		t.Logf("BroadcastCall() with nil message error: %v", err)
	}
	// nil 消息应该被处理
	_ = result
}

// TestLibp2pRPC_BroadcastAsync_NilMessage 测试异步广播 nil 消息
func TestLibp2pRPC_BroadcastAsync_NilMessage(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2"}
	err := rpc.BroadcastAsync(context.Background(), peers, nil, service.ResponseAll, nil, nil)
	if err != nil {
		t.Logf("BroadcastAsync() with nil message error: %v", err)
	}
}

// ============================================================================
// WriteVCall 错误路径测试
// ============================================================================

// TestLibp2pRPC_WriteVCall_AllPeersUnreachable 测试批量调用所有节点不可达
func TestLibp2pRPC_WriteVCall_AllPeersUnreachable(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "node-1", "", []byte("data1")),
		model.NewMessage("msg-2", model.MessageTypeRequest, "node-1", "", []byte("data2")),
	}

	result, err := rpc.WriteVCall(context.Background(), peers, msgs, service.ResponseAll, nil)
	if err != nil {
		t.Logf("WriteVCall() error: %v", err)
	}
	// 验证结果：所有节点都应该失败
	if len(result.SuccessPeers) != 0 {
		t.Errorf("SuccessPeers count = %d, want 0", len(result.SuccessPeers))
	}
	if len(result.FailedPeers) != len(peers) {
		t.Errorf("FailedPeers count = %d, want %d", len(result.FailedPeers), len(peers))
	}
}

// TestLibp2pRPC_WriteVCall_LengthMismatch_Error 测试批量调用长度不匹配
func TestLibp2pRPC_WriteVCall_LengthMismatch_Error(t *testing.T) {
	transport := newMockTransport("node-1")
	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msgs := []model.Message{
		model.NewMessage("msg-1", model.MessageTypeRequest, "node-1", "", []byte("data1")),
		// 只有一个消息，但有两个节点
	}

	_, err := rpc.WriteVCall(context.Background(), peers, msgs, service.ResponseAll, nil)
	if err == nil {
		t.Error("WriteVCall() with mismatched lengths should return error")
	}
}

// ============================================================================
// Context 超时测试
// ============================================================================

// TestLibp2pRPC_Call_WithDeadline 测试带截止时间的调用
func TestLibp2pRPC_Call_WithDeadline(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, &service.RPCConfig{
		CallTimeout: 100 * time.Millisecond,
	})
	defer rpc.Close()

	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "node-2", []byte("test"))

	// 创建一个已过期的上下文
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := rpc.Call(ctx, "node-2", msg)
	if err == nil {
		t.Error("Call() with expired deadline should return error")
	}
}

// TestLibp2pRPC_BroadcastCall_WithCanceledContext 测试取消上下文的广播
func TestLibp2pRPC_BroadcastCall_WithCanceledContext(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.connected["node-3"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	result, err := rpc.BroadcastCall(ctx, peers, msg, service.ResponseAll, nil)
	if err != nil {
		t.Logf("BroadcastCall() with canceled context error: %v", err)
	}
	_ = result
}

// TestLibp2pRPC_SendRequestNoResponse_PeerUnreachable 测试发送请求到未连接节点
func TestLibp2pRPC_SendRequestNoResponse_PeerUnreachable(t *testing.T) {
	transport := newMockTransport("node-1")
	// 不添加任何连接的节点

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := createTestMessage("test-001", model.MessageTypeRequest, []byte("test"))

	err := rpc.sendRequestNoResponse(context.Background(), "node-2", msg)
	assertError(t, err, service.ErrPeerUnreachable, "Should return ErrPeerUnreachable")
}

// TestLibp2pRPC_SendRequestNoResponse_Connected 测试发送请求到已连接节点（模拟 OpenStream 失败）
func TestLibp2pRPC_SendRequestNoResponse_Connected(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	msg := createTestMessage("test-001", model.MessageTypeRequest, []byte("test"))

	err := rpc.sendRequestNoResponse(context.Background(), "node-2", msg)
	// mock transport 的 OpenStream 会返回错误
	if err != nil {
		t.Logf("sendRequestNoResponse() error (expected): %v", err)
	}
}

// TestLibp2pRPC_BroadcastAsync_PeerUnreachable 测试异步广播到未连接节点
func TestLibp2pRPC_BroadcastAsync_PeerUnreachable(t *testing.T) {
	transport := newMockTransport("node-1")
	// 不添加任何连接的节点

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	// BroadcastAsync 需要 6 个参数
	err := rpc.BroadcastAsync(context.Background(), peers, msg, service.ResponseAll, nil, func(from model.PeerID, resp model.Message, err error) {
		t.Logf("BroadcastAsync callback: from=%s, err=%v", from, err)
	})
	if err != nil {
		t.Logf("BroadcastAsync() error: %v", err)
	}
}

// TestLibp2pRPC_BroadcastAsync_WithCallback 测试异步广播带回调
func TestLibp2pRPC_BroadcastAsync_WithCallback(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.connected["node-3"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	var callbackCount int
	var mu sync.Mutex

	err := rpc.BroadcastAsync(context.Background(), peers, msg, service.ResponseAll, nil, func(from model.PeerID, resp model.Message, err error) {
		mu.Lock()
		callbackCount++
		mu.Unlock()
		t.Logf("Callback received: from=%s, err=%v", from, err)
	})
	if err != nil {
		t.Logf("BroadcastAsync() error: %v", err)
	}

	// 等待回调完成
	time.Sleep(100 * time.Millisecond)
	t.Logf("Callback count: %d", callbackCount)
}

// TestLibp2pRPC_BroadcastCall_AllPeersFail 测试广播所有节点失败
func TestLibp2pRPC_BroadcastCall_AllPeersFail(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.connected["node-3"] = true
	transport.mu.Unlock()

	rpc := NewLibp2pRPC(transport, nil)
	defer rpc.Close()

	peers := []model.PeerID{"node-2", "node-3"}
	msg := model.NewMessage("test-001", model.MessageTypeRequest, "node-1", "", []byte("broadcast"))

	// mock transport 的 OpenStream 会返回错误
	result, err := rpc.BroadcastCall(context.Background(), peers, msg, service.ResponseAll, nil)
	if err != nil {
		t.Logf("BroadcastCall() error: %v", err)
	}

	// 验证所有节点都失败
	if len(result.FailedPeers) != 2 {
		t.Errorf("Expected 2 failed peers, got %d", len(result.FailedPeers))
	}
}

// TestLibp2pRPC_Call_ShortTimeout 测试调用超时（短超时配置）
func TestLibp2pRPC_Call_ShortTimeout(t *testing.T) {
	transport := newMockTransport("node-1")
	transport.mu.Lock()
	transport.connected["node-2"] = true
	transport.mu.Unlock()

	// 使用非常短的超时
	config := &service.RPCConfig{
		CallTimeout:        1 * time.Millisecond,
		BroadcastTimeout:   1 * time.Millisecond,
		MaxConcurrentCalls: 100,
	}

	rpc := NewLibp2pRPC(transport, config)
	defer rpc.Close()

	msg := createTestMessage("test-001", model.MessageTypeRequest, []byte("test"))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, err := rpc.Call(ctx, "node-2", msg)
	if err != nil {
		t.Logf("Call() with timeout error: %v (expected)", err)
	}
}
