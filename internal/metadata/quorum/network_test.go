// Package quorum 提供 Quorum 一致性服务集成
//
// P1-3: Quorum 网络集成测试
package quorum

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// ==================== 测试辅助工具 ====================

// mockQuorumNetworkTransport 模拟网络传输层
type mockQuorumNetworkTransport struct {
	mu       sync.Mutex
	handlers map[string]func(nodeID string, msg []byte)
	sent     map[string][]byte
	delay    time.Duration
	failSend bool
}

func newMockQuorumNetworkTransport() *mockQuorumNetworkTransport {
	return &mockQuorumNetworkTransport{
		handlers: make(map[string]func(nodeID string, msg []byte)),
		sent:     make(map[string][]byte),
	}
}

func (m *mockQuorumNetworkTransport) Send(nodeID string, msg []byte) error {
	m.mu.Lock()
	if m.failSend {
		m.mu.Unlock()
		return errors.New("simulated failure: connection timeout")
	}
	m.sent[nodeID] = msg
	delay := m.delay
	m.mu.Unlock()

	// 在锁外执行延迟，避免阻塞其他 goroutine
	if delay > 0 {
		time.Sleep(delay)
	}

	return nil
}

func (m *mockQuorumNetworkTransport) Receive(handler func(nodeID string, msg []byte)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers["default"] = handler
	return nil
}

func (m *mockQuorumNetworkTransport) Close() error {
	return nil
}

func (m *mockQuorumNetworkTransport) getLastSent(nodeID string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[nodeID]
}

// createTestQuorumCoordinator 创建测试用的 QuorumCoordinator
func createTestQuorumCoordinator(participants []string) *QuorumCoordinator {
	return NewQuorumCoordinator(participants, nil)
}

// createTestNetworkIntegrator 创建带 mock transport 的网络集成器
func createTestNetworkIntegrator() (*NetworkIntegrator, *mockQuorumNetworkTransport) {
	mockTransport := newMockQuorumNetworkTransport()
	ni := NewNetworkIntegrator(&NetworkIntegratorOptions{
		Transport:   mockTransport,
		LocalNodeID: "node-1",
		TimeoutPolicy: &QuorumTimeoutPolicy{
			RetryCount: 3,
			RetryDelay: 10 * time.Millisecond,
		},
	})
	return ni, mockTransport
}

// ==================== 网络集成器基础测试 ====================

// TestNetworkIntegrator_Creation 测试网络集成器创建
func TestNetworkIntegrator_Creation(t *testing.T) {
	t.Run("无Transport本地模式", func(t *testing.T) {
		ni := NewNetworkIntegrator(nil)
		require.NotNil(t, ni)
		require.Nil(t, ni.transport)
		require.NotNil(t, ni.timeoutPolicy)
	})

	t.Run("带Transport", func(t *testing.T) {
		mockTransport := newMockQuorumNetworkTransport()
		ni := NewNetworkIntegrator(&NetworkIntegratorOptions{
			Transport:   mockTransport,
			LocalNodeID: "node-1",
		})
		require.NotNil(t, ni)
		require.NotNil(t, ni.transport)
		require.Equal(t, "node-1", ni.localNodeID)
	})
}

// TestNetworkIntegrator_MessageTypes 测试消息类型
func TestNetworkIntegrator_MessageTypes(t *testing.T) {
	require.Equal(t, MessageType(0x40), MessageTypeQuorumPut)
	require.Equal(t, MessageType(0x41), MessageTypeQuorumAck)
	require.Equal(t, MessageType(0x42), MessageTypeQuorumNack)
	require.Equal(t, MessageType(0x43), MessageTypeQuorumGet)
	require.Equal(t, MessageType(0x44), MessageTypeQuorumGetResponse)
}

// TestNetworkIntegrator_Close 测试关闭
func TestNetworkIntegrator_Close(t *testing.T) {
	ni := NewNetworkIntegrator(nil)

	require.False(t, ni.closed)

	err := ni.Close()
	require.NoError(t, err)
	require.True(t, ni.closed)

	// 重复关闭应该无错误
	err = ni.Close()
	require.NoError(t, err)
}

// ==================== Payload 编解码测试 ====================

// TestMsgpackPayloadTypes 测试 msgpack 对不同 Payload 类型的编解码
func TestMsgpackPayloadTypes(t *testing.T) {
	tests := []struct {
		name    string
		msgType MessageType
		payload any
	}{
		{
			name:    "QuorumPut",
			msgType: MessageTypeQuorumPut,
			payload: &QuorumPutPayload{TxID: "tx-001", NS: "default", Key: "key", Value: []byte("value"), Timestamp: 1234567890},
		},
		{
			name:    "QuorumAck",
			msgType: MessageTypeQuorumAck,
			payload: &QuorumAckPayload{TxID: "tx-002", Success: true, NodeID: "node-1"},
		},
		{
			name:    "QuorumNack",
			msgType: MessageTypeQuorumNack,
			payload: &QuorumNackPayload{TxID: "tx-003", Reason: "error", NodeID: "node-2"},
		},
		{
			name:    "QuorumGet",
			msgType: MessageTypeQuorumGet,
			payload: &QuorumGetPayload{RequestID: "req-001", NS: "default", Key: "key"},
		},
		{
			name:    "QuorumGetResponse",
			msgType: MessageTypeQuorumGetResponse,
			payload: &QuorumGetResponsePayload{RequestID: "req-002", Value: []byte("value"), Version: 1, Found: true, NodeID: "node-3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := newQuorumMessage(tc.msgType, tc.payload)
			require.NoError(t, err, "创建消息失败")

			encoded, err := encodeMessage(msg)
			require.NoError(t, err, "编码失败")
			require.NotEmpty(t, encoded, "编码结果为空")

			decoded, err := decodeMessage(encoded)
			require.NoError(t, err, "解码失败")
			require.Equal(t, tc.msgType, decoded.Type, "消息类型不匹配")

			decodedPayload, err := decodePayload(decoded)
			require.NoError(t, err, "解码 payload 失败")
			require.NotNil(t, decodedPayload, "解码后的 payload 为空")
		})
	}
}

// TestMsgpackDirectEncoding 直接测试 msgpack 编解码
func TestMsgpackDirectEncoding(t *testing.T) {
	payload := &QuorumPutPayload{
		TxID:      "tx-direct",
		NS:        "test",
		Key:       "key",
		Value:     []byte("direct-value"),
		Timestamp: time.Now().UnixMilli(),
	}

	encoded, err := msgpack.Marshal(payload)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	var decoded QuorumPutPayload
	err = msgpack.Unmarshal(encoded, &decoded)
	require.NoError(t, err)
	require.Equal(t, payload.TxID, decoded.TxID)
	require.Equal(t, payload.NS, decoded.NS)
	require.Equal(t, payload.Key, decoded.Key)
	require.Equal(t, payload.Value, decoded.Value)
}

// TestDecodePayload_Errors 测试解码错误情况
func TestDecodePayload_Errors(t *testing.T) {
	t.Run("未知消息类型", func(t *testing.T) {
		msg := &QuorumMessage{Type: MessageType(0xFF), PayloadRaw: []byte{0x00}}
		_, err := decodePayload(msg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown message type")
	})

	t.Run("无效payload字节", func(t *testing.T) {
		msgTypes := []struct {
			name    string
			msgType MessageType
		}{
			{"QuorumPut", MessageTypeQuorumPut},
			{"QuorumAck", MessageTypeQuorumAck},
			{"QuorumNack", MessageTypeQuorumNack},
			{"QuorumGet", MessageTypeQuorumGet},
			{"QuorumGetResponse", MessageTypeQuorumGetResponse},
		}

		for _, mt := range msgTypes {
			t.Run(mt.name, func(t *testing.T) {
				msg := &QuorumMessage{Type: mt.msgType, PayloadRaw: []byte{0xFF, 0xFE, 0xFD}}
				_, err := decodePayload(msg)
				require.Error(t, err)
			})
		}
	})
}

// TestEncodeMessage 测试消息编解码
func TestEncodeMessage(t *testing.T) {
	t.Run("有效消息", func(t *testing.T) {
		msg := &QuorumMessage{Type: MessageTypeQuorumPut, PayloadRaw: []byte{0x01, 0x02, 0x03}}
		encoded, err := encodeMessage(msg)
		require.NoError(t, err)
		require.NotEmpty(t, encoded)
	})

	t.Run("无效消息解码", func(t *testing.T) {
		_, err := decodeMessage([]byte{0xFF, 0xFE, 0xFD})
		require.Error(t, err)
	})

	t.Run("编码错误", func(t *testing.T) {
		_, err := newQuorumMessage(MessageTypeQuorumPut, make(chan int))
		require.Error(t, err)

		_, err = newQuorumMessageAndEncode(MessageTypeQuorumPut, make(chan int))
		require.Error(t, err)
	})
}

// ==================== ACK 收集器测试 ====================

// TestQuorumAckCollector 测试 ACK 收集器
func TestQuorumAckCollector(t *testing.T) {
	t.Run("全部成功", func(t *testing.T) {
		collector := NewQuorumAckCollector(3, 1*time.Second)

		go func() {
			time.Sleep(30 * time.Millisecond)
			collector.ReceiveACK("node-1", true)
			time.Sleep(30 * time.Millisecond)
			collector.ReceiveACK("node-2", true)
			time.Sleep(30 * time.Millisecond)
			collector.ReceiveACK("node-3", true)
		}()

		successCount, failedCount, success := collector.WaitAll()
		require.Equal(t, 3, successCount)
		require.Equal(t, 0, failedCount)
		require.True(t, success)
	})

	t.Run("超时", func(t *testing.T) {
		collector := NewQuorumAckCollector(3, 100*time.Millisecond)

		go func() {
			time.Sleep(20 * time.Millisecond)
			collector.ReceiveACK("node-1", true)
			time.Sleep(20 * time.Millisecond)
			collector.ReceiveACK("node-2", true)
		}()

		successCount, _, success := collector.WaitAll()
		require.Equal(t, 2, successCount)
		require.False(t, success)
	})

	t.Run("带失败响应", func(t *testing.T) {
		collector := NewQuorumAckCollector(3, 1*time.Second)

		collector.ReceiveACK("node-1", true)
		collector.ReceiveACK("node-2", false)
		collector.ReceiveACK("node-3", true)

		successCount, failedCount, success := collector.WaitAll()
		require.Equal(t, 2, successCount)
		require.Equal(t, 1, failedCount)
		require.False(t, success)
	})

	t.Run("早期失败立即返回", func(t *testing.T) {
		collector := NewQuorumAckCollector(3, 1*time.Second)

		go func() {
			time.Sleep(20 * time.Millisecond)
			collector.ReceiveACK("node-1", false)
		}()

		start := time.Now()
		successCount, failedCount, success := collector.WaitAll()
		elapsed := time.Since(start)

		require.Equal(t, 0, successCount)
		require.Equal(t, 1, failedCount)
		require.False(t, success)
		require.Less(t, elapsed, 500*time.Millisecond)
	})

	t.Run("进度查询", func(t *testing.T) {
		collector := NewQuorumAckCollector(3, 1*time.Second)

		received, expected := collector.GetProgress()
		require.Equal(t, 0, received)
		require.Equal(t, 3, expected)

		collector.ReceiveACK("node-1", true)
		received, expected = collector.GetProgress()
		require.Equal(t, 1, received)
		require.Equal(t, 3, expected)
	})
}

// ==================== GET 响应收集器测试 ====================

// TestGetResponseCollector 测试 GET 响应收集器
func TestGetResponseCollector(t *testing.T) {
	t.Run("正常响应", func(t *testing.T) {
		collector := newGetResponseCollector(3)

		go func() {
			time.Sleep(20 * time.Millisecond)
			collector.AddResponse(&QuorumGetResponsePayload{RequestID: "req-001", Value: []byte("value-1"), Version: 1, Found: true, NodeID: "node-1"})
			time.Sleep(20 * time.Millisecond)
			collector.AddResponse(&QuorumGetResponsePayload{RequestID: "req-001", Value: []byte("value-2"), Version: 2, Found: true, NodeID: "node-2"})
			time.Sleep(20 * time.Millisecond)
			collector.AddResponse(&QuorumGetResponsePayload{RequestID: "req-001", Value: []byte("value-1"), Version: 1, Found: true, NodeID: "node-3"})
		}()

		ctx := context.Background()
		value, version, err := collector.Wait(ctx)

		require.NoError(t, err)
		require.Equal(t, []byte("value-2"), value)
		require.Equal(t, uint64(2), version)
	})

	t.Run("超时", func(t *testing.T) {
		collector := newGetResponseCollector(3)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, _, err := collector.Wait(ctx)
		require.Error(t, err)
	})

	t.Run("未找到数据", func(t *testing.T) {
		collector := newGetResponseCollector(1)

		go func() {
			time.Sleep(20 * time.Millisecond)
			collector.AddResponse(&QuorumGetResponsePayload{RequestID: "req-001", Value: nil, Version: 0, Found: false, NodeID: "node-1"})
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		_, _, err := collector.Wait(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "未找到数据")
	})

	t.Run("选择最高版本", func(t *testing.T) {
		collector := newGetResponseCollector(3)

		go func() {
			collector.AddResponse(&QuorumGetResponsePayload{RequestID: "req-001", Value: []byte("v1"), Version: 1, Found: true, NodeID: "node-1"})
			collector.AddResponse(&QuorumGetResponsePayload{RequestID: "req-001", Value: []byte("v3"), Version: 3, Found: true, NodeID: "node-2"})
			collector.AddResponse(&QuorumGetResponsePayload{RequestID: "req-001", Value: []byte("v2"), Version: 2, Found: true, NodeID: "node-3"})
		}()

		ctx := context.Background()
		value, version, err := collector.Wait(ctx)

		require.NoError(t, err)
		require.Equal(t, []byte("v3"), value)
		require.Equal(t, uint64(3), version)
	})
}

// ==================== 超时策略测试 ====================

// TestNetworkIntegrator_TimeoutPolicy 测试超时策略
func TestNetworkIntegrator_TimeoutPolicy(t *testing.T) {
	t.Run("自定义策略", func(t *testing.T) {
		customPolicy := &QuorumTimeoutPolicy{
			AckWaitTimeout: 10 * time.Second,
			RetryCount:     5,
			RetryDelay:     200 * time.Millisecond,
		}

		ni := NewNetworkIntegrator(&NetworkIntegratorOptions{TimeoutPolicy: customPolicy})

		policy := ni.GetTimeoutPolicy()
		require.NotNil(t, policy)
		require.Equal(t, 10*time.Second, policy.AckWaitTimeout)
		require.Equal(t, 5, policy.RetryCount)
	})

	t.Run("默认策略", func(t *testing.T) {
		policy := DefaultQuorumTimeoutPolicy()
		require.Equal(t, 5*time.Second, policy.AckWaitTimeout)
		require.Equal(t, 3, policy.RetryCount)
		require.Equal(t, 100*time.Millisecond, policy.RetryDelay)
	})

	t.Run("设置策略", func(t *testing.T) {
		ni := NewNetworkIntegrator(nil)

		newPolicy := &QuorumTimeoutPolicy{AckWaitTimeout: 15 * time.Second, RetryCount: 5}
		ni.SetTimeoutPolicy(newPolicy)

		policy := ni.GetTimeoutPolicy()
		require.Equal(t, 15*time.Second, policy.AckWaitTimeout)
		require.Equal(t, 5, policy.RetryCount)

		// 设置 nil 应该使用默认值
		ni.SetTimeoutPolicy(nil)
		policy = ni.GetTimeoutPolicy()
		require.Equal(t, 5*time.Second, policy.AckWaitTimeout)
	})
}

// ==================== ID 生成测试 ====================

// TestIDGeneration 测试 ID 生成
func TestIDGeneration(t *testing.T) {
	t.Run("事务ID", func(t *testing.T) {
		id1 := generateTxID()
		id2 := generateTxID()

		require.NotEmpty(t, id1)
		require.NotEmpty(t, id2)
		require.NotEqual(t, id1, id2)
		require.Contains(t, id1, "quorum-tx-")
	})

	t.Run("请求ID", func(t *testing.T) {
		id1 := generateRequestID()
		id2 := generateRequestID()

		require.NotEmpty(t, id1)
		require.NotEmpty(t, id2)
		require.NotEqual(t, id1, id2)
		require.Contains(t, id1, "quorum-req-")
	})
}

// ==================== 错误判断测试 ====================

// TestErrorHelpers 测试错误辅助函数
func TestErrorHelpers(t *testing.T) {
	t.Run("containsAny", func(t *testing.T) {
		require.True(t, containsAny("connection timeout", []string{"timeout", "connection"}))
		require.True(t, containsAny("temporary failure", []string{"temporary"}))
		require.False(t, containsAny("invalid argument", []string{"timeout", "connection"}))
		require.False(t, containsAny("short", []string{"longer"}))
		// 边界情况
		require.False(t, containsAny("", []string{"test"}))
		require.False(t, containsAny("test", []string{}))
		require.False(t, containsAny("ab", []string{"abcd"}))
		require.True(t, containsAny("test", []string{"test"}))
		require.True(t, containsAny("testing", []string{"test"}))
	})

	t.Run("isRetryableError", func(t *testing.T) {
		tests := []struct {
			err      error
			expected bool
		}{
			{errors.New("connection refused"), true},
			{errors.New("timeout occurred"), true},
			{errors.New("temporary unavailable"), true},
			{errors.New("network error"), false},
			{errors.New("invalid data"), false},
			{nil, false},
		}

		for _, tc := range tests {
			result := isRetryableError(tc.err)
			require.Equal(t, tc.expected, result, "isRetryableError(%v)", tc.err)
		}
	})
}

// ==================== 消息处理测试 ====================

// TestNetworkIntegrator_HandleMessage 测试消息处理
func TestNetworkIntegrator_HandleMessage(t *testing.T) {
	t.Run("ACK消息", func(t *testing.T) {
		ni, _ := createTestNetworkIntegrator()

		collector := NewQuorumAckCollector(1, 1*time.Second)
		txID := generateTxID()
		ni.ackCollectors.Store(txID, collector)

		ackPayload := &QuorumAckPayload{TxID: txID, Success: true, NodeID: "node-2"}
		msgBytes, err := newQuorumMessageAndEncode(MessageTypeQuorumAck, ackPayload)
		require.NoError(t, err)

		ni.handleMessage("node-2", msgBytes)

		received, expected := collector.GetProgress()
		require.Equal(t, 1, received)
		require.Equal(t, 1, expected)
	})

	t.Run("所有消息类型", func(t *testing.T) {
		ni, _ := createTestNetworkIntegrator()

		tests := []struct {
			name    string
			msgType MessageType
			payload any
		}{
			{"QuorumPut", MessageTypeQuorumPut, &QuorumPutPayload{TxID: "tx-1", NS: "ns", Key: "key", Value: []byte("val")}},
			{"QuorumGet", MessageTypeQuorumGet, &QuorumGetPayload{RequestID: "req-1", NS: "ns", Key: "key"}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				msgBytes, err := newQuorumMessageAndEncode(tc.msgType, tc.payload)
				require.NoError(t, err)
				// 不应该 panic
				ni.handleMessage("node-2", msgBytes)
			})
		}
	})

	t.Run("无效消息", func(t *testing.T) {
		ni, _ := createTestNetworkIntegrator()

		// 发送无效的消息字节
		ni.handleMessage("node-2", []byte{0x00, 0x01, 0x02, 0x03})
		// 应该优雅处理，不应该 panic

		ni.handleMessage("node-2", []byte{0xFF, 0xFE, 0xFD})
		// 应该优雅处理，不 panic

		// 创建有效消息结构但 payload 无效
		msg := &QuorumMessage{Type: MessageTypeQuorumPut, PayloadRaw: []byte{0xFF, 0xFE, 0xFD}}
		msgBytes, err := encodeMessage(msg)
		require.NoError(t, err)
		ni.handleMessage("node-2", msgBytes)
	})
}

// TestNetworkIntegrator_HandleInvalidPayloads 测试处理无效 Payload
func TestNetworkIntegrator_HandleInvalidPayloads(t *testing.T) {
	ni, _ := createTestNetworkIntegrator()

	tests := []struct {
		name    string
		handler func()
	}{
		{"无效PutPayload", func() { ni.handleQuorumPut("node-2", "invalid payload type") }},
		{"无效AckPayload", func() { ni.handleQuorumAck("node-2", "invalid payload type") }},
		{"无效NackPayload", func() { ni.handleQuorumNack("node-2", "invalid payload type") }},
		{"无效GetPayload", func() { ni.handleQuorumGet("node-2", "invalid payload type") }},
		{"无效GetResponsePayload", func() { ni.handleQuorumGetResponse("node-2", "invalid payload type") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// 不应该 panic
			tc.handler()
		})
	}
}

// TestNetworkIntegrator_HandleNoCollector 测试没有 Collector 的情况
func TestNetworkIntegrator_HandleNoCollector(t *testing.T) {
	ni, _ := createTestNetworkIntegrator()

	// Ack 无 Collector
	ackPayload := &QuorumAckPayload{TxID: "non-existent-tx", Success: true, NodeID: "node-2"}
	ni.handleQuorumAck("node-2", ackPayload)

	// Nack 无 Collector
	nackPayload := &QuorumNackPayload{TxID: "non-existent-tx", Reason: "test", NodeID: "node-2"}
	ni.handleQuorumNack("node-2", nackPayload)

	// GetResponse 无 Collector
	respPayload := &QuorumGetResponsePayload{RequestID: "non-existent-req", Value: []byte("value"), Version: 1, Found: true, NodeID: "node-2"}
	ni.handleQuorumGetResponse("node-2", respPayload)
}

// TestNetworkIntegrator_HandleQuorumPut 测试处理 Quorum Put 消息
func TestNetworkIntegrator_HandleQuorumPut(t *testing.T) {
	ni, mockTransport := createTestNetworkIntegrator()

	putPayload := &QuorumPutPayload{
		TxID:      "tx-001",
		NS:        "default",
		Key:       "test-key",
		Value:     []byte("test-value"),
		Timestamp: time.Now().UnixMilli(),
	}

	ni.handleQuorumPut("node-2", putPayload)

	sentMsg := mockTransport.getLastSent("node-2")
	require.NotNil(t, sentMsg)

	decoded, err := decodeMessage(sentMsg)
	require.NoError(t, err)
	require.Equal(t, MessageTypeQuorumAck, decoded.Type)

	decodedPayload, err := decodePayload(decoded)
	require.NoError(t, err)
	ackPayload, ok := decodedPayload.(*QuorumAckPayload)
	require.True(t, ok)
	require.Equal(t, "tx-001", ackPayload.TxID)
	require.True(t, ackPayload.Success)
	require.Equal(t, "node-1", ackPayload.NodeID)
}

// TestNetworkIntegrator_HandleQuorumNack 测试处理 NACK 消息
func TestNetworkIntegrator_HandleQuorumNack(t *testing.T) {
	ni, _ := createTestNetworkIntegrator()

	collector := NewQuorumAckCollector(1, 1*time.Second)
	txID := "tx-nack-001"
	ni.ackCollectors.Store(txID, collector)

	nackPayload := &QuorumNackPayload{TxID: txID, Reason: "write failed", NodeID: "node-2"}
	ni.handleQuorumNack("node-2", nackPayload)

	received, expected := collector.GetProgress()
	require.Equal(t, 0, received) // NACK 不增加成功计数
	require.Equal(t, 1, expected)
}

// TestNetworkIntegrator_HandleQuorumGet 测试处理 GET 请求
func TestNetworkIntegrator_HandleQuorumGet(t *testing.T) {
	ni, mockTransport := createTestNetworkIntegrator()

	getPayload := &QuorumGetPayload{RequestID: "req-001", NS: "default", Key: "test-key"}
	ni.handleQuorumGet("node-2", getPayload)

	sentMsg := mockTransport.getLastSent("node-2")
	require.NotNil(t, sentMsg)

	decoded, err := decodeMessage(sentMsg)
	require.NoError(t, err)
	require.Equal(t, MessageTypeQuorumGetResponse, decoded.Type)

	decodedPayload, err := decodePayload(decoded)
	require.NoError(t, err)
	respPayload, ok := decodedPayload.(*QuorumGetResponsePayload)
	require.True(t, ok)
	require.Equal(t, "req-001", respPayload.RequestID)
	require.Equal(t, "node-1", respPayload.NodeID)
}

// TestNetworkIntegrator_HandleQuorumGetResponse 测试处理 GET 响应
func TestNetworkIntegrator_HandleQuorumGetResponse(t *testing.T) {
	ni, _ := createTestNetworkIntegrator()

	collector := newGetResponseCollector(1)
	requestID := "req-resp-001"
	ni.ackCollectors.Store(requestID, collector)

	respPayload := &QuorumGetResponsePayload{RequestID: requestID, Value: []byte("test-value"), Version: 1, Found: true, NodeID: "node-2"}
	ni.handleQuorumGetResponse("node-2", respPayload)

	require.Equal(t, 1, collector.received)
}

// ==================== 发送重试测试 ====================

// TestNetworkIntegrator_SendWithRetry 测试重试发送
func TestNetworkIntegrator_SendWithRetry(t *testing.T) {
	t.Run("发送成功", func(t *testing.T) {
		ni, mockTransport := createTestNetworkIntegrator()
		mockTransport.failSend = false

		err := ni.sendWithRetry("node-2", []byte("test"))
		require.NoError(t, err)
	})

	t.Run("全部失败", func(t *testing.T) {
		ni, mockTransport := createTestNetworkIntegrator()
		mockTransport.failSend = true

		err := ni.sendWithRetry("node-2", []byte("test"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "重试")
	})
}

// ==================== 并发测试 ====================

// TestNetworkIntegrator_Concurrent 测试并发操作
func TestNetworkIntegrator_Concurrent(t *testing.T) {
	ni := NewNetworkIntegrator(nil)

	const goroutines = 100
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = generateTxID()
			_ = generateRequestID()
		}()
	}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			txID := generateTxID()
			collector := NewQuorumAckCollector(1, 1*time.Second)
			ni.ackCollectors.Store(txID, collector)
			ni.ackCollectors.Delete(txID)
		}(i)
	}

	wg.Wait()
}

// ==================== 网络操作测试 ====================

// TestNetworkIntegrator_PutWithQuorumNetwork 测试网络写入
func TestNetworkIntegrator_PutWithQuorumNetwork(t *testing.T) {
	t.Run("本地模式", func(t *testing.T) {
		ni := NewNetworkIntegrator(nil)
		require.Nil(t, ni.transport)
		require.NotNil(t, ni.timeoutPolicy)
	})

	t.Run("关闭后操作", func(t *testing.T) {
		ni, _ := createTestNetworkIntegrator()
		err := ni.Close()
		require.NoError(t, err)

		coordinator := createTestQuorumCoordinator([]string{"node-1", "node-2"})

		err = ni.PutWithQuorumNetwork(context.Background(), coordinator, "ns", "key", []byte("value"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "closed")

		_, _, err = ni.GetWithQuorumNetwork(context.Background(), coordinator, "ns", "key")
		require.Error(t, err)
		require.Contains(t, err.Error(), "closed")
	})

	t.Run("本地模式GET不支持", func(t *testing.T) {
		ni := NewNetworkIntegrator(nil)
		coordinator := createTestQuorumCoordinator([]string{"node-1"})

		_, _, err := ni.GetWithQuorumNetwork(context.Background(), coordinator, "ns", "key")
		require.Error(t, err)
		require.Contains(t, err.Error(), "本地模式不支持")
	})

	t.Run("带Transport", func(t *testing.T) {
		ni, mockTransport := createTestNetworkIntegrator()
		ni.timeoutPolicy.AckWaitTimeout = 1 * time.Second

		coordinator := createTestQuorumCoordinator([]string{"node-1", "node-2", "node-3"})

		go func() {
			time.Sleep(50 * time.Millisecond)
			sentMsg := mockTransport.getLastSent("node-2")
			if sentMsg != nil {
				decoded, err := decodeMessage(sentMsg)
				if err == nil {
					payload, err := decodePayload(decoded)
					if err == nil {
						if putPayload, ok := payload.(*QuorumPutPayload); ok {
							collector, exists := ni.ackCollectors.Load(putPayload.TxID)
							if exists {
								if ackCollector, ok := collector.(*QuorumAckCollector); ok {
									ackCollector.ReceiveACK("node-2", true)
									ackCollector.ReceiveACK("node-3", true)
								}
							}
						}
					}
				}
			}
		}()

		_ = ni.PutWithQuorumNetwork(context.Background(), coordinator, "ns", "key", []byte("value"))
	})
}

// TestPutWithQuorumNetworkAsync 测试异步写入
func TestPutWithQuorumNetworkAsync(t *testing.T) {
	t.Run("回调执行", func(t *testing.T) {
		ni, _ := createTestNetworkIntegrator()
		ni.timeoutPolicy.AckWaitTimeout = 100 * time.Millisecond
		ni.timeoutPolicy.RetryCount = 1

		coordinator := createTestQuorumCoordinator([]string{"node-1", "node-2"})

		done := make(chan bool, 1)
		var callbackSuccess bool
		var callbackErr error

		ni.PutWithQuorumNetworkAsync(
			context.Background(),
			coordinator,
			"ns",
			"key",
			[]byte("value"),
			func(success bool, err error) {
				callbackSuccess = success
				callbackErr = err
				done <- true
			},
		)

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("回调未在预期时间内执行")
		}

		require.False(t, callbackSuccess)
		require.Error(t, callbackErr)
	})

	t.Run("nil回调", func(t *testing.T) {
		ni, _ := createTestNetworkIntegrator()
		ni.timeoutPolicy.AckWaitTimeout = 100 * time.Millisecond

		coordinator := createTestQuorumCoordinator([]string{"node-1", "node-2"})

		ni.PutWithQuorumNetworkAsync(context.Background(), coordinator, "ns", "key", []byte("value"), nil)
		time.Sleep(200 * time.Millisecond)
	})
}

// TestQuorumCoordinator_NetworkIntegration 测试协调器与网络集成
func TestQuorumCoordinator_NetworkIntegration(t *testing.T) {
	t.Run("基本配置", func(t *testing.T) {
		coordinator := createTestQuorumCoordinator([]string{"node-1", "node-2", "node-3"})
		require.Equal(t, 2, coordinator.GetQuorum())
		require.Equal(t, 3, len(coordinator.GetParticipants()))
	})

	t.Run("带选项创建", func(t *testing.T) {
		participants := []string{"node-1", "node-2", "node-3"}
		opts := &PutOptions{Timeout: 5000}

		coordinator := NewQuorumCoordinatorWithOptions(participants, nil, opts)
		require.Equal(t, 2, coordinator.GetQuorum())
		require.Equal(t, participants, coordinator.GetParticipants())
	})

	t.Run("超时配置", func(t *testing.T) {
		coordinator := createTestQuorumCoordinator([]string{"node-1"})

		require.Equal(t, 5*time.Second, coordinator.GetTimeout())

		coordinator.SetTimeout(3 * time.Second)
		require.Equal(t, 3*time.Second, coordinator.GetTimeout())
	})
}
