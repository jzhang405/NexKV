// Package quorum 提供 Quorum 一致性服务集成
//
// P1-3: Quorum 网络集成
// 实现网络消息发送、ACK 收集、超时管理
package quorum

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/transport"
	"github.com/vmihailenco/msgpack/v5"
)

// ==================== 超时策略 ====================

// QuorumTimeoutPolicy Quorum 超时策略
type QuorumTimeoutPolicy struct {
	AckWaitTimeout time.Duration
	RetryCount     int
	RetryDelay     time.Duration
}

// DefaultQuorumTimeoutPolicy 默认超时策略
func DefaultQuorumTimeoutPolicy() *QuorumTimeoutPolicy {
	return &QuorumTimeoutPolicy{
		AckWaitTimeout: 5 * time.Second,
		RetryCount:     3,
		RetryDelay:     100 * time.Millisecond,
	}
}

// ==================== ACK 收集器 ====================

// QuorumAckCollector Quorum ACK 收集器
type QuorumAckCollector struct {
	mu        sync.Mutex
	cond      *sync.Cond
	expected  int
	received  int
	failed    int
	timeout   time.Duration
	startTime time.Time
}

// NewQuorumAckCollector 创建 ACK 收集器
func NewQuorumAckCollector(expected int, timeout time.Duration) *QuorumAckCollector {
	c := &QuorumAckCollector{
		expected:  expected,
		timeout:   timeout,
		startTime: time.Now(),
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// ReceiveACK 接收 ACK
func (c *QuorumAckCollector) ReceiveACK(nodeID string, success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if success {
		c.received++
	} else {
		c.failed++
	}

	if c.received >= c.expected || c.failed > 0 {
		c.cond.Broadcast()
	}
}

// WaitAll 等待所有 ACK
func (c *QuorumAckCollector) WaitAll() (successCount int, failedCount int, success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.received >= c.expected {
		return c.received, c.failed, true
	}

	// 使用 time.AfterFunc 替代 goroutine，避免资源泄漏
	timer := time.AfterFunc(c.timeout, func() {
		c.cond.Broadcast()
	})
	defer timer.Stop()

	for c.received < c.expected && c.failed == 0 {
		if time.Since(c.startTime) > c.timeout {
			break
		}
		c.cond.Wait()
	}

	return c.received, c.failed, c.received >= c.expected
}

// GetProgress 获取进度
func (c *QuorumAckCollector) GetProgress() (received, expected int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.received, c.expected
}

// ==================== 消息类型定义 ====================

// MessageType Quorum 消息类型
type MessageType byte

const (
	MessageTypeQuorumPut         MessageType = 0x40
	MessageTypeQuorumAck         MessageType = 0x41
	MessageTypeQuorumNack        MessageType = 0x42
	MessageTypeQuorumGet         MessageType = 0x43
	MessageTypeQuorumGetResponse MessageType = 0x44
)

// ==================== 消息 Payload 定义 ====================

// QuorumPutPayload Quorum 写入请求
type QuorumPutPayload struct {
	TxID      string
	NS        string
	Key       string
	Value     []byte
	Timestamp int64
}

// QuorumAckPayload Quorum ACK 响应
type QuorumAckPayload struct {
	TxID    string
	Success bool
	NodeID  string
}

// QuorumNackPayload Quorum NACK 响应
type QuorumNackPayload struct {
	TxID   string
	Reason string
	NodeID string
}

// QuorumGetPayload Quorum 读取请求
type QuorumGetPayload struct {
	RequestID string
	NS        string
	Key       string
}

// QuorumGetResponsePayload Quorum 读取响应
type QuorumGetResponsePayload struct {
	RequestID string
	Value     []byte
	Version   uint64
	Found     bool
	NodeID    string
}

// ==================== 消息封装 ====================

// QuorumMessage Quorum 网络消息封装
// 使用 RawMessage 保留 payload 的原始字节，解码时根据 Type 确定具体类型
type QuorumMessage struct {
	Type       MessageType
	PayloadRaw msgpack.RawMessage // 原始 payload 字节
}

// encodeMessage 编码消息（使用 msgpack）
func encodeMessage(msg *QuorumMessage) ([]byte, error) {
	// 直接编码结构体，PayloadRaw 会自动处理
	return msgpack.Marshal(msg)
}

// decodeMessage 解码消息（使用 msgpack）
// 返回的消息中 PayloadRaw 包含原始字节，需要根据 Type 进一步解析
func decodeMessage(data []byte) (*QuorumMessage, error) {
	var msg QuorumMessage
	if err := msgpack.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// decodePayload 根据 message type 解码 payload
func decodePayload(msg *QuorumMessage) (any, error) {
	switch msg.Type {
	case MessageTypeQuorumPut:
		var payload QuorumPutPayload
		if err := msgpack.Unmarshal(msg.PayloadRaw, &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	case MessageTypeQuorumAck:
		var payload QuorumAckPayload
		if err := msgpack.Unmarshal(msg.PayloadRaw, &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	case MessageTypeQuorumNack:
		var payload QuorumNackPayload
		if err := msgpack.Unmarshal(msg.PayloadRaw, &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	case MessageTypeQuorumGet:
		var payload QuorumGetPayload
		if err := msgpack.Unmarshal(msg.PayloadRaw, &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	case MessageTypeQuorumGetResponse:
		var payload QuorumGetResponsePayload
		if err := msgpack.Unmarshal(msg.PayloadRaw, &payload); err != nil {
			return nil, err
		}
		return &payload, nil
	default:
		return nil, fmt.Errorf("unknown message type: %d", msg.Type)
	}
}

// newQuorumMessage 创建带 payload 的消息
func newQuorumMessage(msgType MessageType, payload any) (*QuorumMessage, error) {
	payloadBytes, err := msgpack.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &QuorumMessage{
		Type:       msgType,
		PayloadRaw: payloadBytes,
	}, nil
}

// newQuorumMessageAndEncode 创建消息并编码（便捷函数）
func newQuorumMessageAndEncode(msgType MessageType, payload any) ([]byte, error) {
	msg, err := newQuorumMessage(msgType, payload)
	if err != nil {
		return nil, err
	}
	return encodeMessage(msg)
}

// ==================== 网络集成器 ====================

// NetworkIntegrator Quorum 网络集成器
type NetworkIntegrator struct {
	mu            sync.RWMutex
	transport     transport.Transport
	localNodeID   string
	timeoutPolicy *QuorumTimeoutPolicy
	ackCollectors sync.Map
	ctx           context.Context
	cancel        context.CancelFunc
	closed        bool
}

// NetworkIntegratorOptions 网络集成器选项
type NetworkIntegratorOptions struct {
	Transport     transport.Transport
	LocalNodeID   string
	TimeoutPolicy *QuorumTimeoutPolicy
}

// NewNetworkIntegrator 创建网络集成器
func NewNetworkIntegrator(opts *NetworkIntegratorOptions) *NetworkIntegrator {
	if opts == nil {
		opts = &NetworkIntegratorOptions{}
	}
	if opts.TimeoutPolicy == nil {
		opts.TimeoutPolicy = DefaultQuorumTimeoutPolicy()
	}

	ctx, cancel := context.WithCancel(context.Background())
	ni := &NetworkIntegrator{
		transport:     opts.Transport,
		localNodeID:   opts.LocalNodeID,
		timeoutPolicy: opts.TimeoutPolicy,
		ctx:           ctx,
		cancel:        cancel,
	}

	if ni.transport != nil {
		_ = ni.transport.Receive(ni.handleMessage)
	}
	return ni
}

// ==================== 写入操作 ====================

// PutWithQuorumNetwork 使用网络 Quorum 写入
func (ni *NetworkIntegrator) PutWithQuorumNetwork(
	ctx context.Context,
	coordinator *QuorumCoordinator,
	ns, key string,
	value []byte,
) error {
	if ni.transport == nil {
		return coordinator.PutWithQuorum(ctx, ns, key, value, nil)
	}

	ni.mu.RLock()
	if ni.closed {
		ni.mu.RUnlock()
		return fmt.Errorf("network integrator is closed")
	}
	ni.mu.RUnlock()

	txID := generateTxID()
	participants := coordinator.GetParticipants()
	quorum := coordinator.GetQuorum()
	timeout := ni.timeoutPolicy.AckWaitTimeout

	collector := NewQuorumAckCollector(quorum, timeout)
	ni.ackCollectors.Store(txID, collector)
	defer ni.ackCollectors.Delete(txID)

	collector.ReceiveACK(ni.localNodeID, true)

	payload := &QuorumPutPayload{
		TxID:      txID,
		NS:        ns,
		Key:       key,
		Value:     value,
		Timestamp: time.Now().UnixMilli(),
	}

	msgBytes, err := newQuorumMessageAndEncode(MessageTypeQuorumPut, payload)
	if err != nil {
		return fmt.Errorf("编码消息失败: %w", err)
	}

	var wg sync.WaitGroup
	var sendErr error
	var errMu sync.Mutex

	for _, participant := range participants {
		if participant == ni.localNodeID {
			continue
		}

		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()
			if err := ni.sendWithRetry(nodeID, msgBytes); err != nil {
				errMu.Lock()
				if sendErr == nil {
					sendErr = fmt.Errorf("发送到节点 %s 失败: %w", nodeID, err)
				}
				errMu.Unlock()
			}
		}(participant)
	}

	wg.Wait()

	successCount, failedCount, success := collector.WaitAll()

	logging.WithFields(map[string]any{
		"tx_id":      txID,
		"namespace":  ns,
		"key":        key,
		"success":    successCount,
		"failed":     failedCount,
		"quorum":     quorum,
		"achieved":   success,
		"send_error": sendErr,
	}).Info("Quorum 网络写入完成")

	if !success {
		if sendErr != nil {
			return fmt.Errorf("quorum 确认失败（发送错误）: %w", sendErr)
		}
		return fmt.Errorf("quorum 确认失败: 成功 %d, 失败 %d, 需要 %d", successCount, failedCount, quorum)
	}

	return nil
}

// PutWithQuorumNetworkAsync 异步 Quorum 写入
func (ni *NetworkIntegrator) PutWithQuorumNetworkAsync(
	ctx context.Context,
	coordinator *QuorumCoordinator,
	ns, key string,
	value []byte,
	callback func(success bool, err error),
) {
	go func() {
		err := ni.PutWithQuorumNetwork(ctx, coordinator, ns, key, value)
		if callback != nil {
			callback(err == nil, err)
		}
	}()
}

// ==================== 读取操作 ====================

// GetWithQuorumNetwork 使用网络 Quorum 读取
func (ni *NetworkIntegrator) GetWithQuorumNetwork(
	ctx context.Context,
	coordinator *QuorumCoordinator,
	ns, key string,
) ([]byte, uint64, error) {
	if ni.transport == nil {
		return nil, 0, fmt.Errorf("本地模式不支持 Quorum 读取")
	}

	ni.mu.RLock()
	if ni.closed {
		ni.mu.RUnlock()
		return nil, 0, fmt.Errorf("network integrator is closed")
	}
	ni.mu.RUnlock()

	timeoutCtx, cancel := context.WithTimeout(ctx, ni.timeoutPolicy.AckWaitTimeout)
	defer cancel()

	requestID := generateRequestID()
	responseCollector := newGetResponseCollector(coordinator.GetQuorum())

	ni.ackCollectors.Store(requestID, responseCollector)
	defer ni.ackCollectors.Delete(requestID)

	payload := &QuorumGetPayload{
		RequestID: requestID,
		NS:        ns,
		Key:       key,
	}

	msgBytes, err := newQuorumMessageAndEncode(MessageTypeQuorumGet, payload)
	if err != nil {
		return nil, 0, fmt.Errorf("编码消息失败: %w", err)
	}

	participants := coordinator.GetParticipants()
	for _, participant := range participants {
		if participant == ni.localNodeID {
			continue
		}
		go func() {
			_ = ni.transport.Send(participant, msgBytes)
		}()
	}

	value, version, err := responseCollector.Wait(timeoutCtx)
	if err != nil {
		return nil, 0, fmt.Errorf("quorum 读取失败: %w", err)
	}

	return value, version, nil
}

// ==================== 消息处理 ====================

// handleMessage 处理接收到的消息
func (ni *NetworkIntegrator) handleMessage(nodeID string, msg []byte) {
	quorumMsg, err := decodeMessage(msg)
	if err != nil {
		logging.WithFields(map[string]any{
			"node_id": nodeID,
			"error":   err,
		}).Debug("Quorum 消息解码失败")
		return
	}

	// 解码 payload
	payload, err := decodePayload(quorumMsg)
	if err != nil {
		logging.WithFields(map[string]any{
			"node_id": nodeID,
			"type":    quorumMsg.Type,
			"error":   err,
		}).Debug("Quorum payload 解码失败")
		return
	}

	switch quorumMsg.Type {
	case MessageTypeQuorumPut:
		ni.handleQuorumPut(nodeID, payload)
	case MessageTypeQuorumAck:
		ni.handleQuorumAck(nodeID, payload)
	case MessageTypeQuorumNack:
		ni.handleQuorumNack(nodeID, payload)
	case MessageTypeQuorumGet:
		ni.handleQuorumGet(nodeID, payload)
	case MessageTypeQuorumGetResponse:
		ni.handleQuorumGetResponse(nodeID, payload)
	}
}

// handleQuorumPut 处理 Quorum 写入请求
func (ni *NetworkIntegrator) handleQuorumPut(nodeID string, payload any) {
	putPayload, ok := payload.(*QuorumPutPayload)
	if !ok {
		return
	}

	success := true

	var responsePayload any
	var msgType MessageType

	if success {
		responsePayload = &QuorumAckPayload{
			TxID:    putPayload.TxID,
			Success: true,
			NodeID:  ni.localNodeID,
		}
		msgType = MessageTypeQuorumAck
	} else {
		responsePayload = &QuorumNackPayload{
			TxID:   putPayload.TxID,
			Reason: "写入失败",
			NodeID: ni.localNodeID,
		}
		msgType = MessageTypeQuorumNack
	}

	msgBytes, err := newQuorumMessageAndEncode(msgType, responsePayload)
	if err != nil {
		return
	}

	_ = ni.transport.Send(nodeID, msgBytes)
}

// handleQuorumAck 处理 ACK 响应
func (ni *NetworkIntegrator) handleQuorumAck(nodeID string, payload any) {
	ackPayload, ok := payload.(*QuorumAckPayload)
	if !ok {
		return
	}

	collector, exists := ni.ackCollectors.Load(ackPayload.TxID)
	if !exists {
		return
	}

	if ackCollector, ok := collector.(*QuorumAckCollector); ok {
		ackCollector.ReceiveACK(nodeID, ackPayload.Success)
	}
}

// handleQuorumNack 处理 NACK 响应
func (ni *NetworkIntegrator) handleQuorumNack(nodeID string, payload any) {
	nackPayload, ok := payload.(*QuorumNackPayload)
	if !ok {
		return
	}

	collector, exists := ni.ackCollectors.Load(nackPayload.TxID)
	if !exists {
		return
	}

	if ackCollector, ok := collector.(*QuorumAckCollector); ok {
		ackCollector.ReceiveACK(nodeID, false)
	}

	logging.WithFields(map[string]any{
		"tx_id":   nackPayload.TxID,
		"node_id": nodeID,
		"reason":  nackPayload.Reason,
	}).Debug("收到 Quorum NACK")
}

// handleQuorumGet 处理读取请求
func (ni *NetworkIntegrator) handleQuorumGet(nodeID string, payload any) {
	getPayload, ok := payload.(*QuorumGetPayload)
	if !ok {
		return
	}

	responsePayload := &QuorumGetResponsePayload{
		RequestID: getPayload.RequestID,
		Value:     nil,
		Version:   0,
		Found:     false,
		NodeID:    ni.localNodeID,
	}

	msgBytes, err := newQuorumMessageAndEncode(MessageTypeQuorumGetResponse, responsePayload)
	if err != nil {
		return
	}

	_ = ni.transport.Send(nodeID, msgBytes)
}

// handleQuorumGetResponse 处理读取响应
func (ni *NetworkIntegrator) handleQuorumGetResponse(nodeID string, payload any) {
	respPayload, ok := payload.(*QuorumGetResponsePayload)
	if !ok {
		return
	}

	collector, exists := ni.ackCollectors.Load(respPayload.RequestID)
	if !exists {
		return
	}

	if getCollector, ok := collector.(*getResponseCollector); ok {
		getCollector.AddResponse(respPayload)
	}
}

// ==================== 辅助方法 ====================

// sendWithRetry 带重试的消息发送
func (ni *NetworkIntegrator) sendWithRetry(nodeID string, msg []byte) error {
	var lastErr error

	for i := 0; i < ni.timeoutPolicy.RetryCount; i++ {
		err := ni.transport.Send(nodeID, msg)
		if err == nil {
			return nil
		}

		lastErr = err

		if !isRetryableError(err) {
			return err
		}

		if i < ni.timeoutPolicy.RetryCount-1 {
			time.Sleep(ni.timeoutPolicy.RetryDelay)
		}
	}

	return fmt.Errorf("发送失败，重试 %d 次后仍不成功: %w", ni.timeoutPolicy.RetryCount, lastErr)
}

// isRetryableError 检查错误是否可重试
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return containsAny(errStr, []string{"timeout", "connection", "temporary"})
}

// containsAny 检查字符串是否包含任意子串
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

// GetTimeoutPolicy 获取超时策略
func (ni *NetworkIntegrator) GetTimeoutPolicy() *QuorumTimeoutPolicy {
	return ni.timeoutPolicy
}

// SetTimeoutPolicy 设置超时策略
func (ni *NetworkIntegrator) SetTimeoutPolicy(policy *QuorumTimeoutPolicy) {
	if policy == nil {
		policy = DefaultQuorumTimeoutPolicy()
	}
	ni.mu.Lock()
	defer ni.mu.Unlock()
	ni.timeoutPolicy = policy
}

// Close 关闭网络集成器
func (ni *NetworkIntegrator) Close() error {
	ni.mu.Lock()
	defer ni.mu.Unlock()

	if ni.closed {
		return nil
	}

	ni.closed = true
	ni.cancel()

	return nil
}

// ==================== GET 响应收集器 ====================

// getResponseCollector GET 响应收集器
type getResponseCollector struct {
	mu        sync.Mutex
	cond      *sync.Cond
	responses []*QuorumGetResponsePayload
	expected  int
	received  int
	done      bool
}

// newGetResponseCollector 创建 GET 响应收集器
func newGetResponseCollector(expected int) *getResponseCollector {
	c := &getResponseCollector{
		responses: make([]*QuorumGetResponsePayload, 0),
		expected:  expected,
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// AddResponse 添加响应
func (c *getResponseCollector) AddResponse(resp *QuorumGetResponsePayload) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.responses = append(c.responses, resp)
	c.received++

	if c.received >= c.expected {
		c.done = true
		c.cond.Broadcast()
	}
}

// Wait 等待响应完成
func (c *getResponseCollector) Wait(ctx context.Context) ([]byte, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 使用 done channel 确保 goroutine 能被正确清理
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			c.cond.Broadcast()
		case <-done:
		}
	}()

	for !c.done && ctx.Err() == nil {
		c.cond.Wait()
	}

	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}

	var latestValue []byte
	var latestVersion uint64

	for _, resp := range c.responses {
		if resp.Found && resp.Version > latestVersion {
			latestValue = resp.Value
			latestVersion = resp.Version
		}
	}

	if latestVersion == 0 {
		return nil, 0, fmt.Errorf("未找到数据")
	}

	return latestValue, latestVersion, nil
}

// ==================== ID 生成 ====================

// generateTxID 生成事务 ID
func generateTxID() string {
	// 使用单调递增的计数器确保唯一性
	ts := time.Now().UnixNano()
	return fmt.Sprintf("quorum-tx-%d-%d", ts, atomic.AddUint64(&txIDCounter, 1))
}

// generateRequestID 生成请求 ID
func generateRequestID() string {
	ts := time.Now().UnixNano()
	return fmt.Sprintf("quorum-req-%d-%d", ts, atomic.AddUint64(&reqIDCounter, 1))
}

var (
	txIDCounter  uint64
	reqIDCounter uint64
)
