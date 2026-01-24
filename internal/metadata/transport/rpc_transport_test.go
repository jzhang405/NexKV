// Package transport RPCTransport 单元测试
package transport

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
)

// ========================================
// 构造函数测试
// ========================================

// TestNewRPCTransport 测试构造函数
func TestNewRPCTransport(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)

	assert.NotNil(t, rpc)
	assert.Equal(t, transport, rpc.transport)
	assert.Equal(t, 5*time.Second, rpc.defaultTimeout)
	assert.Equal(t, int64(10000), rpc.maxReqTableSize)
	assert.Equal(t, int64(0), rpc.reqTableSize.Load())
	assert.False(t, rpc.closed.Load())
}

// TestNewRPCTransport_NilTransport 测试 transport 为 nil 时 panic
func TestNewRPCTransport_NilTransport(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("应该 panic")
		}
	}()

	_ = NewRPCTransport(nil, 5*time.Second)
}

// TestNewRPCTransport_ZeroTimeout 测试超时时间为 0 时使用默认值
func TestNewRPCTransport_ZeroTimeout(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 0)

	assert.Equal(t, 5*time.Second, rpc.defaultTimeout)
}

// ========================================
// SendRequest 单向消息测试
// ========================================

// TestSendRequest_OneWay 测试单向消息（NoResponse）
func TestSendRequest_OneWay(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	// 模拟单向消息（使用正确格式的消息）
	// encodeMockMessage 格式：[MsgSeq(8)][IsRequest(1)][IsError(1)][ExpectResponse(1)][Body]
	reqBody := encodeMockMessage(123, true, false, types.NoResponse, []byte("test-message"))

	resp, err := rpc.SendRequest("127.0.0.1:9211", reqBody, 5*time.Second)

	// 单向消息应该立即返回
	assert.NoError(t, err)
	assert.Nil(t, resp, "单向消息返回 nil")
	assert.Equal(t, int64(0), rpc.reqTableSize.Load(), "不应该创建 reqTable 条目")
}

// TestSendRequest_OneWay_EmptyBody 测试单向消息空请求体
func TestSendRequest_OneWay_EmptyBody(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	_, err := rpc.SendRequest("127.0.0.1:9211", []byte{}, 5*time.Second)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "请求体不能为空")
}

// TestSendRequest_OneWay_EmptyTargetNode 测试单向消息空目标节点
func TestSendRequest_OneWay_EmptyTargetNode(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	_, err := rpc.SendRequest("", []byte("test"), 5*time.Second)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "目标节点不能为空")
}

// ========================================
// SendRequest 双向消息测试
// ========================================

// TestSendRequest_TwoWay_ImmediateResponse 测试双向消息立即响应
func TestSendRequest_TwoWay_ImmediateResponse(t *testing.T) {
	transport := &mockTransport{
		responseDelay: 100 * time.Millisecond,
	}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	// 模拟双向消息（使用正确格式的消息）
	msgSeq := uint64(123) // 固定 MsgID
	reqBody := encodeMockMessage(msgSeq, true, false, types.ExpectResponse, []byte("test-message"))

	// 并发发送响应
	go func() {
		time.Sleep(100 * time.Millisecond)
		// 模拟接收到的响应
		key := RequestKey{
			NodeID: rpc.transport.GetNodeID(),
			MsgID:  msgSeq, // 使用相同的 MsgID
		}
		if val, ok := rpc.reqTable.Load(key); ok {
			ctx := val.(*RequestCtx)
			ctx.RespCh <- []byte("response-body")
		}
	}()

	resp, err := rpc.SendRequest("127.0.0.1:9211", reqBody, 5*time.Second)

	assert.NoError(t, err)
	assert.Equal(t, []byte("response-body"), resp)
	assert.Equal(t, int64(0), rpc.reqTableSize.Load(), "请求完成后应该清理 reqTable")
}

// TestSendRequest_TwoWay_Timeout 测试双向消息超时
func TestSendRequest_TwoWay_Timeout(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 100*time.Millisecond) // 短超时
	defer func() { _ = rpc.Close() }()

	// ✅ P0-3 修复: 使用正确的 encodeMockMessage 格式
	msgSeq := uint64(456)
	reqBody := encodeMockMessage(msgSeq, true, false, types.ExpectResponse, []byte("test-message"))

	startTime := time.Now()
	resp, err := rpc.SendRequest("127.0.0.1:9211", reqBody, 100*time.Millisecond)
	elapsed := time.Since(startTime)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "请求超时")
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.Less(t, elapsed, 200*time.Millisecond, "超时应该接近设定时间")
	assert.Equal(t, uint64(1), rpc.timeoutCount.Load(), "应该记录超时次数")
}

// TestSendRequest_TwoWay_DoSProtection 测试 reqTable 容量限制
func TestSendRequest_TwoWay_DoSProtection(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	rpc.maxReqTableSize = 10 // 设置小容量
	defer func() { _ = rpc.Close() }()

	// 填充 reqTable 到接近上限
	for i := 0; i < 10; i++ {
		_, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		key := RequestKey{NodeID: 1, MsgID: uint64(i)}
		requestCtx := &RequestCtx{
			MsgID:     uint64(i),
			RespCh:    make(chan []byte, 1),
			ErrorCh:   make(chan error, 1),
			CreatedAt: time.Now(),
			Cancel:    cancel,
		}
		rpc.reqTable.Store(key, requestCtx)
		rpc.reqTableSize.Add(1)
	}

	// 尝试发送新请求，应该被拒绝
	// ✅ P0-3 修复: 使用正确的 encodeMockMessage 格式
	msgSeq := uint64(789)
	reqBody := encodeMockMessage(msgSeq, true, false, types.ExpectResponse, []byte("test-message"))
	_, err := rpc.SendRequest("127.0.0.1:9211", reqBody, 5*time.Second)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "请求等待表已满")
}

// ========================================
// SendResponse 测试
// ========================================

// TestSendResponse_Success 测试发送响应成功
func TestSendResponse_Success(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	err := rpc.SendResponse("127.0.0.1:9211", 123, []byte("response-body"), false)

	assert.NoError(t, err)
}

// TestSendResponse_EmptyTargetNode 测试空目标节点
func TestSendResponse_EmptyTargetNode(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	err := rpc.SendResponse("", 123, []byte("response-body"), false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "目标节点不能为空")
}

// ========================================
// OnRecv 测试
// ========================================

// TestOnRecv_OneWayRequest 测试处理单向请求
func TestOnRecv_OneWayRequest(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	// 模拟单向请求消息
	data := encodeMockMessage(123, true, false, types.NoResponse, []byte("one-way-request"))

	// OnRecv 不应该 panic
	rpc.OnRecv("127.0.0.1:9211", data)

	// 验证没有创建 reqTable 条目
	assert.Equal(t, int64(0), rpc.reqTableSize.Load())
}

// TestOnRecv_TwoWayRequest 测试处理双向请求
func TestOnRecv_TwoWayRequest(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	// 模拟双向请求消息
	data := encodeMockMessage(123, true, false, types.ExpectResponse, []byte("two-way-request"))

	// OnRecv 不应该 panic
	rpc.OnRecv("127.0.0.1:9211", data)

	// 验证没有创建 reqTable 条目（因为接收方不处理请求）
	assert.Equal(t, int64(0), rpc.reqTableSize.Load())
}

// TestOnRecv_Response 测试处理响应
func TestOnRecv_Response(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	// 先创建一个等待的请求
	_, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	key := RequestKey{NodeID: parseNodeID("127.0.0.1:9211"), MsgID: 123}
	requestCtx := &RequestCtx{
		MsgID:     123,
		RespCh:    make(chan []byte, 1),
		ErrorCh:   make(chan error, 1),
		CreatedAt: time.Now(),
		Cancel:    cancel,
	}
	rpc.reqTable.Store(key, requestCtx)
	rpc.reqTableSize.Add(1)

	// 模拟响应消息
	data := encodeMockMessage(123, false, false, types.ExpectResponse, []byte("response-body"))

	// 在 goroutine 中接收响应
	go func() {
		rpc.OnRecv("127.0.0.1:9211", data)
	}()

	// 等待响应
	select {
	case resp := <-requestCtx.RespCh:
		assert.Equal(t, []byte("response-body"), resp)
	case err := <-requestCtx.ErrorCh:
		t.Fatalf("不应该返回错误: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("应该在 1 秒内收到响应")
	}
}

// TestOnRecv_InvalidData 测试处理无效数据
func TestOnRecv_InvalidData(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	// 无效数据（太短）
	data := []byte{1, 2, 3}

	// OnRecv 不应该 panic
	rpc.OnRecv("127.0.0.1:9211", data)
}

// ========================================
// GetStats 测试
// ========================================

// TestGetStats 测试获取统计信息
func TestGetStats(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	stats := rpc.GetStats()

	assert.Contains(t, stats, "activeRequestCount")
	assert.Contains(t, stats, "timeoutCount")
	assert.Contains(t, stats, "totalRequestCount")
	assert.Contains(t, stats, "avgLatency")
	assert.Contains(t, stats, "maxReqTableSize")

	assert.Equal(t, int64(0), stats["activeRequestCount"])
	assert.Equal(t, uint64(0), stats["timeoutCount"])
	assert.Equal(t, uint64(0), stats["totalRequestCount"])
	assert.Equal(t, int64(10000), stats["maxReqTableSize"])
}

// TestGetStats_WithRequests 测试有请求时的统计信息
func TestGetStats_WithRequests(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	// 模拟一些请求统计
	rpc.totalRequest.Add(10)
	rpc.totalLatencyNs.Add(1000000) // 1ms 总延迟
	rpc.timeoutCount.Add(2)

	stats := rpc.GetStats()

	assert.Equal(t, uint64(10), stats["totalRequestCount"])
	assert.Equal(t, uint64(2), stats["timeoutCount"])
	// 平均延迟 = 1ms / 10 = 100µs (100 微秒 = 0.1 毫秒)
	// Go 的 time.Duration.String() 格式化为 "100µs"
	avgLatency := stats["avgLatency"].(string)
	assert.Contains(t, avgLatency, "100µs")
}

// ========================================
// Close 测试
// ========================================

// TestClose_Idempotent 测试 Close 幂等性
func TestClose_Idempotent(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)

	// 第一次关闭
	err := rpc.Close()
	assert.NoError(t, err)

	// 第二次关闭（应该安全）
	err = rpc.Close()
	assert.NoError(t, err)

	assert.True(t, rpc.closed.Load())
}

// TestClose_CleanupResources 测试 Close 清理资源
func TestClose_CleanupResources(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)

	// 创建一些请求
	for i := 0; i < 5; i++ {
		_, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		key := RequestKey{NodeID: 1, MsgID: uint64(i)}
		requestCtx := &RequestCtx{
			MsgID:     uint64(i),
			RespCh:    make(chan []byte, 1),
			ErrorCh:   make(chan error, 1),
			CreatedAt: time.Now(),
			Cancel:    cancel,
		}
		rpc.reqTable.Store(key, requestCtx)
		rpc.reqTableSize.Add(1)
	}

	assert.Equal(t, int64(5), rpc.reqTableSize.Load())

	// 关闭应该清理所有请求
	err := rpc.Close()
	assert.NoError(t, err)

	// 验证所有请求被清理
	assert.Equal(t, int64(0), rpc.reqTableSize.Load())
}

// ========================================
// 并发安全测试
// ========================================

// TestConcurrentSendRequest 测试并发 SendRequest
func TestConcurrentSendRequest(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	const numGoroutines = 100
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			reqBody := []byte("test-message")
			_, _ = rpc.SendRequest("127.0.0.1:9211", reqBody, 5*time.Second)
		}(i)
	}

	wg.Wait()

	// 验证 reqTable 被清理（单向消息不创建条目）
	assert.Equal(t, int64(0), rpc.reqTableSize.Load())
}

// ========================================
// Mock 实现
// ========================================

// mockTransport Mock Transport 实现
type mockTransport struct {
	startCalled     bool
	nodeID          uint64
	msgSeqGenerator uint64
	responseDelay   time.Duration
	sentMessages    [][]byte
	mu              sync.Mutex
}

func (m *mockTransport) Start(nodeID *uint64, msgSeqGenerator func() uint64) error {
	m.startCalled = true
	if nodeID != nil {
		m.nodeID = *nodeID
	}
	if msgSeqGenerator != nil {
		m.msgSeqGenerator = msgSeqGenerator()
	}
	return nil
}

func (m *mockTransport) Stop() error {
	return nil
}

func (m *mockTransport) Send(ctx context.Context, addr string, msg Message, opt ...SendOpt) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 记录发送的消息
	if rpcMsg, ok := msg.(*RPCMessage); ok {
		data := encodeMockMessage(rpcMsg.MsgSeq, rpcMsg.IsRequest, rpcMsg.IsError, rpcMsg.ExpectResponse(), rpcMsg.Body)
		m.sentMessages = append(m.sentMessages, data)
	}

	// 模拟网络延迟
	if m.responseDelay > 0 {
		time.Sleep(m.responseDelay)
	}

	return nil
}

func (m *mockTransport) Receive() <-chan MsgFrame {
	// 简化实现：返回一个永不发送的通道
	ch := make(chan MsgFrame, 1)
	close(ch)
	return ch
}

func (m *mockTransport) ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
	// 简化实现：直接返回成功
	return m.GenerateMsgSeq(), nil
}

func (m *mockTransport) GetNodeID() uint64 {
	return m.nodeID
}

func (m *mockTransport) GenerateMsgSeq() uint64 {
	// 为了测试可预测性，返回固定值 123
	return 123
}

// ========================================
// 辅助函数
// ========================================

// encodeMockMessage 编码 Mock 消息（简化版本）
func encodeMockMessage(msgSeq uint64, isRequest, isError bool, expectResponse types.ResponseExpectation, body []byte) []byte {
	// 简化编码：固定格式
	// [MsgSeq(8)][IsRequest(1)][IsError(1)][ExpectResponse(1)][Body]
	data := make([]byte, 11+len(body))

	// MsgSeq (BigEndian)
	data[0] = byte(msgSeq >> 56)
	data[1] = byte(msgSeq >> 48)
	data[2] = byte(msgSeq >> 40)
	data[3] = byte(msgSeq >> 32)
	data[4] = byte(msgSeq >> 24)
	data[5] = byte(msgSeq >> 16)
	data[6] = byte(msgSeq >> 8)
	data[7] = byte(msgSeq)

	// IsRequest
	if isRequest {
		data[8] = 1
	} else {
		data[8] = 0
	}

	// IsError
	if isError {
		data[9] = 1
	} else {
		data[9] = 0
	}

	// ExpectResponse（暂时不编码，使用默认值）
	data[10] = byte(expectResponse)

	// Body
	copy(data[11:], body)

	return data[0 : 11+len(body)]
}

// ========================================
// RPCMessage 接口方法测试
// ========================================

// TestRPCMessage_Type 测试 Type 方法
func TestRPCMessage_Type(t *testing.T) {
	// 测试请求消息
	reqMsg := &RPCMessage{
		MsgSeq:    123,
		IsRequest: true,
		Body:      []byte("request body"),
	}
	assert.Equal(t, types.MessageTypeGet, reqMsg.Type())

	// 测试响应消息
	respMsg := &RPCMessage{
		MsgSeq:    456,
		IsRequest: false,
		Body:      []byte("response body"),
	}
	assert.Equal(t, types.MessageTypeGetReply, respMsg.Type())
}

// TestRPCMessage_Priority 测试 Priority 方法
func TestRPCMessage_Priority(t *testing.T) {
	msg := &RPCMessage{
		MsgSeq: 123,
		Body:   []byte("test"),
	}
	assert.Equal(t, int(types.PriorityNormal), msg.Priority())
}

// TestRPCMessage_Reliability 测试 Reliability 方法
func TestRPCMessage_Reliability(t *testing.T) {
	msg := &RPCMessage{
		MsgSeq: 123,
		Body:   []byte("test"),
	}
	assert.Equal(t, types.Reliable, msg.Reliability())
}

// TestRPCMessage_ExpectResponse 测试 ExpectResponse 方法
func TestRPCMessage_ExpectResponse(t *testing.T) {
	testCases := []struct {
		name           string
		expectResponse types.ResponseExpectation
	}{
		{"期望响应", types.ExpectResponse},
		{"不期望响应", types.NoResponse},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &RPCMessage{
				MsgSeq:         123,
				expectResponse: tc.expectResponse,
			}
			assert.Equal(t, tc.expectResponse, msg.ExpectResponse())
		})
	}
}

// ========================================
// cleanupExpiredRequests 方法测试
// ========================================

// TestRPCTransport_CleanupExpiredRequests 测试清理过期请求
func TestRPCTransport_CleanupExpiredRequests(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 1*time.Second) // 1秒超时

	// 手动创建一些过期的请求
	now := time.Now()
	oldTime := now.Add(-3 * time.Second) // 3秒前（超过 2*timeout）

	key1 := RequestKey{NodeID: 1, MsgID: 1001}
	ctx1 := &RequestCtx{
		MsgID:     1001,
		CreatedAt: oldTime,
		RespCh:    make(chan []byte, 1),
		ErrorCh:   make(chan error, 1),
		Cancel:    func() {},
	}
	rpc.reqTable.Store(key1, ctx1)
	rpc.reqTableSize.Add(1)

	key2 := RequestKey{NodeID: 2, MsgID: 1002}
	ctx2 := &RequestCtx{
		MsgID:     1002,
		CreatedAt: oldTime,
		RespCh:    make(chan []byte, 1),
		ErrorCh:   make(chan error, 1),
		Cancel:    func() {},
	}
	rpc.reqTable.Store(key2, ctx2)
	rpc.reqTableSize.Add(1)

	// 创建一个未过期的请求
	key3 := RequestKey{NodeID: 3, MsgID: 1003}
	ctx3 := &RequestCtx{
		MsgID:     1003,
		CreatedAt: now, // 刚创建
		RespCh:    make(chan []byte, 1),
		ErrorCh:   make(chan error, 1),
		Cancel:    func() {},
	}
	rpc.reqTable.Store(key3, ctx3)
	rpc.reqTableSize.Add(1)

	initialSize := rpc.reqTableSize.Load()
	assert.Equal(t, int64(3), initialSize)

	// 调用清理方法
	rpc.cleanupExpiredRequests()

	// 验证：过期请求被清理，未过期请求保留
	finalSize := rpc.reqTableSize.Load()
	assert.Equal(t, int64(1), finalSize, "应该只剩1个未过期请求")

	_, exists1 := rpc.reqTable.Load(key1)
	assert.False(t, exists1, "过期请求1应该被删除")

	_, exists2 := rpc.reqTable.Load(key2)
	assert.False(t, exists2, "过期请求2应该被删除")

	_, exists3 := rpc.reqTable.Load(key3)
	assert.True(t, exists3, "未过期请求3应该保留")

	// 手动清理后关闭
	_ = rpc.Close()
}

// TestRPCTransport_CleanupExpiredRequests_MaxLimit 测试清理上限
func TestRPCTransport_CleanupExpiredRequests_MaxLimit(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 1*time.Second)

	// 创建超过 maxCleanupPerRound (100) 的过期请求
	oldTime := time.Now().Add(-3 * time.Second)
	for i := 0; i < 150; i++ {
		key := RequestKey{NodeID: uint64(i), MsgID: uint64(1000 + i)}
		ctx := &RequestCtx{
			MsgID:     uint64(1000 + i),
			CreatedAt: oldTime,
			RespCh:    make(chan []byte, 1),
			ErrorCh:   make(chan error, 1),
			Cancel:    func() {}, // 添加 Cancel 避免并发访问 panic
		}
		rpc.reqTable.Store(key, ctx)
		rpc.reqTableSize.Add(1)
	}

	initialSize := rpc.reqTableSize.Load()
	assert.Equal(t, int64(150), initialSize)

	// 调用清理方法
	rpc.cleanupExpiredRequests()

	// 验证：最多清理 100 个，剩余 50 个
	finalSize := rpc.reqTableSize.Load()
	assert.Equal(t, int64(50), finalSize, "应该剩余50个请求（清理上限100个）")

	// 手动清理后关闭
	_ = rpc.Close()
}

// TestRPCTransport_CleanupExpiredRequests_EmptyTable 测试空请求表
func TestRPCTransport_CleanupExpiredRequests_EmptyTable(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 1*time.Second)

	// 确保请求表为空
	count := int64(0)
	rpc.reqTable.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, int64(0), count)

	// 调用清理方法（不应该 panic）
	rpc.cleanupExpiredRequests()

	// 验证大小仍为 0
	assert.Equal(t, int64(0), rpc.reqTableSize.Load())

	// 手动关闭
	_ = rpc.Close()
}

// ========================================
// RequestKey 测试
// ========================================

// TestRequestKey_AsKey 测试 RequestKey 作为 map key
func TestRequestKey_AsKey(t *testing.T) {
	// 验证 RequestKey 可以作为 map 的 key
	m := make(map[RequestKey]string)

	key1 := RequestKey{NodeID: 1, MsgID: 1001}
	key2 := RequestKey{NodeID: 1, MsgID: 1002}
	key3 := RequestKey{NodeID: 2, MsgID: 1001}

	m[key1] = "value1"
	m[key2] = "value2"
	m[key3] = "value3"

	assert.Equal(t, 3, len(m))
	assert.Equal(t, "value1", m[key1])
	assert.Equal(t, "value2", m[key2])
	assert.Equal(t, "value3", m[key3])

	// 验证相同 NodeID 和 MsgID 会被覆盖
	m[key1] = "value1-updated"
	assert.Equal(t, 3, len(m))
	assert.Equal(t, "value1-updated", m[key1])
}

// ========================================
// RequestCtx 测试
// ========================================

// TestRequestCtx_Channels 测试 RequestCtx 通道缓冲
func TestRequestCtx_Channels(t *testing.T) {
	ctx := &RequestCtx{
		MsgID:     123,
		CreatedAt: time.Now(),
		RespCh:    make(chan []byte, 1),
		ErrorCh:   make(chan error, 1),
		Cancel:    func() {},
	}

	// 验证通道可以正常写入和读取
	respData := []byte("response data")
	go func() {
		ctx.RespCh <- respData
	}()

	received := <-ctx.RespCh
	assert.Equal(t, respData, received)

	// 验证错误通道
	go func() {
		ctx.ErrorCh <- nil
	}()

	err := <-ctx.ErrorCh
	assert.NoError(t, err)
}

// TestRequestCtx_Cancel 测试 Cancel 函数
func TestRequestCtx_Cancel(t *testing.T) {
	cancelCalled := false
	ctx := &RequestCtx{
		MsgID:     123,
		CreatedAt: time.Now(),
		RespCh:    make(chan []byte, 1),
		ErrorCh:   make(chan error, 1),
		Cancel: func() {
			cancelCalled = true
		},
	}

	ctx.Cancel()
	assert.True(t, cancelCalled, "Cancel 函数应该被调用")
}

// ========================================
// 边界条件和错误处理测试
// ========================================

// TestRPCTransport_ReqTableSizeLimit 测试请求表大小限制
func TestRPCTransport_ReqTableSizeLimit(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	// 设置一个较小的最大容量用于测试
	rpc.maxReqTableSize = 10
	defer func() { _ = rpc.Close() }()

	// 创建达到上限的请求
	reqBody := encodeMockMessage(1, true, false, types.ExpectResponse, []byte("test"))
	for i := 0; i < 10; i++ {
		go func() {
			_, _ = rpc.SendRequest("127.0.0.1:9211", reqBody, 5*time.Second)
		}()
	}

	// 等待所有请求添加到请求表
	time.Sleep(50 * time.Millisecond)

	// 验证请求表大小正确
	size := rpc.reqTableSize.Load()
	assert.LessOrEqual(t, size, int64(10), "请求表大小不应超过上限")
}

// TestRPCTransport_ConcurrentAccess 测试并发访问
func TestRPCTransport_ConcurrentAccess(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	// 并发发送多个请求
	reqBody := encodeMockMessage(1, true, false, types.ExpectResponse, []byte("test"))
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = rpc.SendRequest("127.0.0.1:9211", reqBody, 5*time.Second)
		}(i)
	}

	wg.Wait()

	// 验证没有死锁或 panic
	assert.True(t, true)
}

// TestRPCTransport_Close_MultipleClose 测试多次 Close
func TestRPCTransport_Close_MultipleClose(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)

	// 多次调用 Close（不应该 panic）
	err1 := rpc.Close()
	err2 := rpc.Close()
	err3 := rpc.Close()

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NoError(t, err3)
	assert.True(t, rpc.closed.Load())
}

// ========================================
// P0 Bug 修复验证测试
// ========================================

// TestRPCParseInvalidExpectResponse 测试 P0-3: 无效的 ExpectResponse 枚举值
// 验证 decodeMessage 会拒绝 ExpectResponse > 1 的消息
func TestRPCParseInvalidExpectResponse(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	testCases := []struct {
		name              string
		expectResponseVal byte
		shouldFail        bool
	}{
		{"有效值: NoResponse (0)", 0, false},
		{"有效值: ExpectResponse (1)", 1, false},
		{"无效值: 2", 2, true},
		{"无效值: 255", 255, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 构造测试数据（最小长度 11 字节）
			data := make([]byte, 11)
			// MsgSeq = 1
			data[0] = 0
			data[1] = 0
			data[2] = 0
			data[3] = 0
			data[4] = 0
			data[5] = 0
			data[6] = 0
			data[7] = 1
			// IsRequest = true
			data[8] = 1
			// IsError = false
			data[9] = 0
			// ExpectResponse = tc.expectResponseVal
			data[10] = tc.expectResponseVal

			// 调用 decodeMessage
			msg, err := rpc.decodeMessage(data)

			if tc.shouldFail {
				// 应该返回错误
				assert.Error(t, err, "ExpectResponse=%d 应该返回错误", tc.expectResponseVal)
				assert.Nil(t, msg)
				assert.Contains(t, err.Error(), "invalid ExpectResponse value")
				assert.Contains(t, err.Error(), fmt.Sprintf("%d", tc.expectResponseVal))
				assert.Contains(t, err.Error(), "field breakdown")
			} else {
				// 应该成功
				assert.NoError(t, err, "ExpectResponse=%d 应该成功", tc.expectResponseVal)
				assert.NotNil(t, msg)
				assert.Equal(t, types.ResponseExpectation(tc.expectResponseVal), msg.ExpectResponse())
			}
		})
	}

	t.Log("✅ P0-3: ExpectResponse 枚举验证测试通过")
}

// TestRPCParseInvalidLength 测试 P0-3: 无效的数据长度
// 验证 decodeMessage 会拒绝长度 < 11 的消息
func TestRPCParseInvalidLength(t *testing.T) {
	transport := &mockTransport{}
	rpc := NewRPCTransport(transport, 5*time.Second)
	defer func() { _ = rpc.Close() }()

	testCases := []struct {
		name        string
		dataLength  int
		shouldFail  bool
	}{
		{"长度 0", 0, true},
		{"长度 10", 10, true},
		{"长度 11 (最小有效)", 11, false},
		{"长度 12", 12, false},
		{"长度 100", 100, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.dataLength)

			// 调用 decodeMessage
			msg, err := rpc.decodeMessage(data)

			if tc.shouldFail {
				// 应该返回错误
				assert.Error(t, err, "长度=%d 应该返回错误", tc.dataLength)
				assert.Nil(t, msg)
				assert.Contains(t, err.Error(), "invalid data length")
				assert.Contains(t, err.Error(), fmt.Sprintf("%d", tc.dataLength))
			} else {
				// 应该成功（至少有 11 字节）
				assert.NoError(t, err, "长度=%d 应该成功", tc.dataLength)
				assert.NotNil(t, msg)
			}
		})
	}

	t.Log("✅ P0-3: 数据长度验证测试通过")
}

