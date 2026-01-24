// Package transport 提供 RPC 请求-应答传输层实现
package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ========================================
// RPCMessage RPC 消息格式
// ========================================

// RPCMessage RPC 消息结构（实现 Message 接口）
type RPCMessage struct {
	MsgSeq         uint64                    // 消息序列号
	IsRequest      bool                      // 是否为请求
	IsError        bool                      // 是否为错误响应
	expectResponse types.ResponseExpectation // 响应期望（内部字段）
	Body           []byte                    // 消息体
}

// Type 实现 Message 接口：返回消息类型
func (m *RPCMessage) Type() types.MessageType {
	if m.IsRequest {
		return types.MessageTypeGet // 临时使用 Get 作为请求类型
	}
	return types.MessageTypeGetReply // 临时使用 GetReply 作为响应类型
}

// Priority 实现 Message 接口：返回消息优先级
func (m *RPCMessage) Priority() int {
	return int(types.PriorityNormal)
}

// ExpectResponse 实现 Message 接口：返回响应期望
func (m *RPCMessage) ExpectResponse() types.ResponseExpectation {
	return m.expectResponse
}

// Reliability 实现 Message 接口：返回可靠性要求
func (m *RPCMessage) Reliability() types.ReliabilityRequirement {
	return types.Reliable
}

// ========================================
// RequestKey 和 RequestCtx
// ========================================

// RequestKey 请求等待表的键
type RequestKey struct {
	NodeID uint64 // 目标节点 ID
	MsgID  uint64 // 消息序列号
}

// RequestCtx 请求上下文
type RequestCtx struct {
	MsgID     uint64             // 消息序列号
	RespCh    chan []byte        // 响应通道（缓冲为 1）
	ErrorCh   chan error         // 错误通道（缓冲为 1）
	CreatedAt time.Time          // 创建时间（用于超时清理）
	Cancel    context.CancelFunc // 取消函数
}

// ========================================
// RPCTransport 核心结构体
// ========================================

// RPCTransport 请求-应答传输层
type RPCTransport struct {
	transport       Transport     // 底层传输实现（接口，满足依赖倒置原则）
	reqTable        sync.Map      // 请求等待表（key: RequestKey, value: *RequestCtx）
	defaultTimeout  time.Duration // 默认超时时间
	maxReqTableSize int64         // reqTable 最大容量（防止 DoS）
	reqTableSize    atomic.Int64  // reqTable 当前大小（原子计数）
	cleanupTicker   *time.Ticker  // 定期清理定时器
	cleanupStopCh   chan struct{} // 停止清理信号
	closed          atomic.Bool   // 是否已关闭

	// 监控指标（原子操作）
	timeoutCount   atomic.Uint64 // 超时次数统计
	totalRequest   atomic.Uint64 // 总请求数统计
	totalLatencyNs atomic.Uint64 // 总延迟（纳秒）
}

// NewRPCTransport 创建 RPCTransport 实例
//
// 参数：
//   - transport: 底层传输实现（必须实现 Transport 接口）
//   - defaultTimeout: 默认超时时间（推荐 5 秒）
//
// 返回：
//   - *RPCTransport: RPCTransport 实例
//
// 使用示例：
//
//	multiTransport := NewMultiTransport(":0")
//	multiTransport.Start(nil, nil)
//	rpc := NewRPCTransport(multiTransport, 5*time.Second)
func NewRPCTransport(transport Transport, defaultTimeout time.Duration) *RPCTransport {
	if transport == nil {
		panic("transport 不能为 nil")
	}
	if defaultTimeout <= 0 {
		defaultTimeout = 5 * time.Second
	}

	rpc := &RPCTransport{
		transport:       transport,
		defaultTimeout:  defaultTimeout,
		maxReqTableSize: 10000, // 最大 10000 个并发请求
		cleanupStopCh:   make(chan struct{}),
	}

	// 启动定期清理 goroutine
	rpc.startCleanupLoop()

	return rpc
}

// startCleanupLoop 启动定期清理 goroutine
func (r *RPCTransport) startCleanupLoop() {
	r.cleanupTicker = time.NewTicker(1 * time.Minute)
	go func() {
		for {
			select {
			case <-r.cleanupTicker.C:
				r.cleanupExpiredRequests()
			case <-r.cleanupStopCh:
				return
			}
		}
	}()
}

// cleanupExpiredRequests 清理过期的请求（带提前退出优化）
func (r *RPCTransport) cleanupExpiredRequests() {
	now := time.Now()
	expiredCount := 0
	maxCleanupPerRound := 100 // ✅ 单次最多清理 100 个过期请求

	r.reqTable.Range(func(key, value interface{}) bool {
		// ✅ 提前退出条件：避免单次清理耗时过长
		if expiredCount >= maxCleanupPerRound {
			return false // 停止遍历
		}

		ctx := value.(*RequestCtx)

		// 清理超时超过 2 倍 defaultTimeout 的请求
		if now.Sub(ctx.CreatedAt) > r.defaultTimeout*2 {
			r.reqTable.Delete(key)
			r.reqTableSize.Add(-1)
			expiredCount++
		}

		return true
	})

	if expiredCount > 0 {
		logging.Warnf("RPCTransport 清理过期请求 count=%d", expiredCount)
	}

	// ✅ 如果清理数量达到上限，记录日志
	if expiredCount >= maxCleanupPerRound {
		logging.Warnf("RPCTransport 清理达到上限，剩余过期请求将在下次清理")
	}
}

// ========================================
// SendRequest 方法
// ========================================

// SendRequest 发送请求并根据 Message 类型决定是否等待响应
//
// 参数：
//   - targetNode: 目标节点地址（如 "127.0.0.1:9211"）
//   - reqBody: 请求体（实现 Message 接口的字节数组）
//   - timeout: 超时时间（推荐 5 秒）
//
// 返回：
//   - []byte: 响应体（如果不需要响应，返回 nil）
//   - error: 错误信息（超时、网络错误等）
//
// 行为说明：
//  1. 解析 reqBody 获取 Message 接口
//  2. 调用 Message.ExpectResponse() 检查是否需要响应
//  3. 如果 ExpectResponse() == NoResponse：
//     - 发送消息后立即返回 nil, nil
//     - 不创建 reqTable 条目（节省内存）
//     - 不等待响应（提高性能）
//  4. 如果 ExpectResponse() == RequireResponse：
//     - 创建 reqTable 条目
//     - 阻塞等待响应或超时
//     - 超时后自动清理 reqTable 条目
func (r *RPCTransport) SendRequest(
	targetNode string,
	reqBody []byte,
	timeout time.Duration,
) ([]byte, error) {
	// 参数验证
	if targetNode == "" {
		return nil, fmt.Errorf("目标节点不能为空")
	}
	if len(reqBody) == 0 {
		return nil, fmt.Errorf("请求体不能为空")
	}
	if timeout <= 0 {
		timeout = r.defaultTimeout
	}

	// ✅ 优化 1: 删除未使用的 globalMsgID（复用 Transport.GenerateMsgSeq）
	nodeID := r.transport.GetNodeID()
	msgSeq := r.transport.GenerateMsgSeq()

	// ✅ 新增：解析 Message 接口，获取响应期望
	msg, err := r.decodeMessage(reqBody)
	if err != nil {
		return nil, fmt.Errorf("解析消息失败: %w", err)
	}

	// ✅ 新增：检查是否需要响应
	expectResponse := msg.ExpectResponse()

	// ✅ 新增：单向消息模式（NoResponse）
	if expectResponse == types.NoResponse {
		// 不需要响应，立即发送并返回
		reqMsg := &RPCMessage{
			MsgSeq:         msgSeq,
			IsRequest:      true,
			expectResponse: types.NoResponse, // ✅ 标记为单向消息
			Body:           reqBody,
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		if err := r.transport.Send(ctx, targetNode, reqMsg); err != nil {
			return nil, fmt.Errorf("发送单向消息失败: %w", err)
		}

		// ✅ 立即返回成功（不等待响应）
		logging.Debugf("发送单向消息成功 target=%s msgSeq=%d", targetNode, msgSeq)
		return nil, nil
	}

	// ✅ 双向消息模式（RequireResponse）：需要等待响应
	// ✅ P0: 检查容量限制（防止 DoS）
	if r.reqTableSize.Load() >= r.maxReqTableSize {
		return nil, fmt.Errorf("请求等待表已满（max=%d）", r.maxReqTableSize)
	}

	// ✅ P1: 组合 NodeID + MsgSeq 确保全局唯一
	key := RequestKey{
		NodeID: nodeID,
		MsgID:  msgSeq,
	}

	// 创建请求上下文
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	requestCtx := &RequestCtx{
		MsgID:     msgSeq,
		RespCh:    make(chan []byte, 1),
		ErrorCh:   make(chan error, 1),
		CreatedAt: time.Now(),
		Cancel:    cancel,
	}

	// ✅ P0: 存储到 reqTable
	r.reqTable.Store(key, requestCtx)
	r.reqTableSize.Add(1)

	// ✅ P0: defer 确保清理（修复内存泄漏）
	defer func() {
		r.reqTable.Delete(key)
		r.reqTableSize.Add(-1)
		cancel() // ✅ P0: 释放 context 资源
	}()

	// 构造请求消息（添加 IsRequest 标识）
	startTime := time.Now()
	reqMsg := &RPCMessage{
		MsgSeq:         msgSeq,
		IsRequest:      true,
		expectResponse: types.ExpectResponse, // ✅ 标记为双向消息
		Body:           reqBody,
	}

	// 发送请求
	if err := r.transport.Send(ctx, targetNode, reqMsg); err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err) // ✅ P2: 错误包装
	}

	// ✅ P0: 等待响应或超时（使用 select）
	select {
	case resp := <-requestCtx.RespCh:
		// ✅ 更新监控指标
		latency := time.Since(startTime)
		r.totalRequest.Add(1)
		r.totalLatencyNs.Add(uint64(latency.Nanoseconds()))
		return resp, nil
	case err := <-requestCtx.ErrorCh:
		return nil, err
	case <-ctx.Done():
		// ✅ 更新超时统计
		r.timeoutCount.Add(1)
		return nil, fmt.Errorf("请求超时: %w", ctx.Err()) // ✅ P2: 错误包装
	}
}

// ========================================
// SendResponse 方法
// ========================================

// SendResponse 发送响应
//
// 参数：
//   - targetNode: 目标节点地址
//   - msgID: 请求的 MsgID
//   - respBody: 响应体（字节数组）
//   - isError: 是否为错误响应
//
// 返回：
//   - error: 错误信息
func (r *RPCTransport) SendResponse(
	targetNode string,
	msgID uint64,
	respBody []byte,
	isError bool,
) error {
	// 参数验证
	if targetNode == "" {
		return fmt.Errorf("目标节点不能为空")
	}

	// 构造响应消息
	respMsg := &RPCMessage{
		MsgSeq:         msgID,
		IsRequest:      false,
		IsError:        isError,
		expectResponse: types.NoResponse, // 响应消息不需要响应
		Body:           respBody,
	}

	// 发送响应
	ctx, cancel := context.WithTimeout(context.Background(), r.defaultTimeout)
	defer cancel()

	if err := r.transport.Send(ctx, targetNode, respMsg); err != nil {
		return fmt.Errorf("发送响应失败: %w", err)
	}

	logging.Debugf("发送响应成功 target=%s msgID=%d isError=%v", targetNode, msgID, isError)
	return nil
}

// ========================================
// OnRecv 方法
// ========================================

// OnRecv 处理收到的消息（由 Transport.Receive() 驱动）
//
// 参数：
//   - nodeID: 发送节点 ID
//   - data: 消息数据（已解码）
func (r *RPCTransport) OnRecv(nodeID string, data []byte) {
	// ✅ 优化 3: 解码容错处理
	msg, err := r.decodeMessage(data)
	if err != nil {
		logging.Errorf("解析消息失败 nodeID=%s error=%v dataLen=%d", nodeID, err, len(data))
		return // ✅ 提前返回，避免无效数据污染后续流程
	}

	logging.Debugf("收到消息 nodeID=%s msgSeq=%d isRequest=%v expectResponse=%v",
		nodeID, msg.MsgSeq, msg.IsRequest, msg.ExpectResponse())

	// 如果是响应，匹配 reqTable
	if !msg.IsRequest {
		key := RequestKey{
			NodeID: parseNodeID(nodeID), // 从地址解析 NodeID
			MsgID:  msg.MsgSeq,
		}

		value, ok := r.reqTable.Load(key)
		if !ok {
			logging.Warnf("未匹配到请求 nodeID=%s msgSeq=%d（可能已超时）", nodeID, msg.MsgSeq)
			return
		}

		ctx := value.(*RequestCtx)

		// ✅ P1: 使用 select 超时避免 channel 阻塞
		if msg.IsError {
			select {
			case ctx.ErrorCh <- fmt.Errorf("响应错误: %s", string(msg.Body)):
				logging.Debugf("发送错误响应成功 msgSeq=%d", msg.MsgSeq)
			case <-time.After(5 * time.Second):
				logging.Errorf("发送错误响应超时 msgSeq=%d", msg.MsgSeq)
			}
		} else {
			select {
			case ctx.RespCh <- msg.Body:
				logging.Debugf("发送响应成功 msgSeq=%d", msg.MsgSeq)
			case <-time.After(5 * time.Second):
				logging.Errorf("发送响应超时 msgSeq=%d", msg.MsgSeq)
			}
		}
	} else {
		// ✅ 新增：处理请求（支持 ExpectResponse）
		// 检查是否需要响应
		if msg.ExpectResponse() == types.NoResponse {
			// 单向消息：只处理消息，不发送响应
			logging.Debugf("收到单向请求 nodeID=%s msgSeq=%d（不发送响应）", nodeID, msg.MsgSeq)
			// TODO: 调用业务层处理逻辑（如 Gossip 消息处理）
			return // ✅ 不发送响应
		}

		// 双向消息：需要发送响应
		logging.Debugf("收到双向请求 nodeID=%s msgSeq=%d（需要发送响应）", nodeID, msg.MsgSeq)
		// TODO: 调用业务层处理逻辑，并通过 SendResponse 发送响应
		// 例如：r.SendResponse(nodeID, msg.MsgSeq, respBody, false)
	}
}

// ========================================
// decodeMessage 方法
// ========================================

// decodeMessage 解码消息（带容错处理）
func (r *RPCTransport) decodeMessage(data []byte) (*RPCMessage, error) {
	// ✅ 参数验证
	if len(data) < 11 { // 协议头至少 11 字节（MsgSeq + IsRequest + IsError + ExpectResponse + Body）
		return nil, fmt.Errorf("invalid data length: %d (minimum 11)", len(data))
	}

	// ✅ 协议版本号支持（预留）
	// version := data[0]
	// if version != 1 { // 当前协议版本为 1
	// 	return nil, fmt.Errorf("unsupported protocol version: %d", version)
	// }

	// 解析固定头部（简化版本，实际需要根据协议格式解析）
	msgSeq := binary.BigEndian.Uint64(data[0:8])
	isRequest := data[8] != 0
	isError := data[9] != 0

	// P0-3: 验证 ExpectResponse 枚举值（有效值：0=NoResponse, 1=ExpectResponse）
	expectResponseRaw := data[10]
	if expectResponseRaw > 1 {
		return nil, fmt.Errorf("invalid ExpectResponse value: %d (valid: 0=NoResponse, 1=ExpectResponse), field breakdown: MsgSeq=8bytes, IsRequest=1byte, IsError=1byte, ExpectResponse=1byte",
			expectResponseRaw)
	}
	expectResponse := types.ResponseExpectation(expectResponseRaw)

	body := data[11:]

	return &RPCMessage{
		MsgSeq:         msgSeq,
		IsRequest:      isRequest,
		IsError:        isError,
		expectResponse: expectResponse,
		Body:           body,
	}, nil
}

// ========================================
// GetStats 方法
// ========================================

// GetStats 获取监控指标
func (r *RPCTransport) GetStats() map[string]interface{} {
	totalReq := r.totalRequest.Load()
	totalLatencyNs := r.totalLatencyNs.Load()

	var avgLatency time.Duration
	if totalReq > 0 {
		avgLatency = time.Duration(totalLatencyNs / uint64(totalReq))
	}

	return map[string]interface{}{
		"activeRequestCount": r.reqTableSize.Load(), // ✅ 活跃请求数
		"timeoutCount":       r.timeoutCount.Load(), // ✅ 超时次数统计
		"totalRequestCount":  totalReq,              // ✅ 总请求数
		"avgLatency":         avgLatency.String(),   // ✅ 平均延迟
		"maxReqTableSize":    r.maxReqTableSize,     // 最大容量
	}
}

// ========================================
// Close 方法
// ========================================

// Close 关闭 RPCTransport
func (r *RPCTransport) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil // 已关闭
	}

	// 停止定期清理
	close(r.cleanupStopCh)
	if r.cleanupTicker != nil {
		r.cleanupTicker.Stop()
	}

	// 清理所有等待中的请求
	r.reqTable.Range(func(key, value interface{}) bool {
		ctx := value.(*RequestCtx)
		ctx.Cancel() // ✅ 取消所有等待中的请求
		r.reqTable.Delete(key)
		r.reqTableSize.Add(-1) // ✅ 减少 reqTableSize 计数
		return true
	})

	// 关闭底层 Transport
	if err := r.transport.Stop(); err != nil {
		return fmt.Errorf("关闭底层 Transport 失败: %w", err)
	}

	logging.Info("RPCTransport 已关闭")
	return nil
}

// ========================================
// 辅助方法
// ========================================

// parseNodeID 从地址解析 NodeID（临时实现）
// TODO: 实现完整的 NodeID 解析逻辑
func parseNodeID(addr string) uint64 {
	// 临时实现：从地址哈希生成 NodeID
	// 实际应该从 Transport 或配置获取
	h := 0
	for _, c := range addr {
		h = h*31 + int(c)
	}
	return uint64(h)
}
