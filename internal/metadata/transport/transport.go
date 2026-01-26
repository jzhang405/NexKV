// Package transport 提供网络传输层抽象接口
// 支持 TCP、gRPC、Memory 等多种传输协议
package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// Transport 网络传输接口
//
// 核心特性:
//   - 协议抽象：支持 TCP、UDP 等多种实现
//   - 消息传递：异步发送/接收消息
//   - 连接管理：自动重连、连接池
//   - 生命周期：Start/Stop 控制
//   - 消息唯一标识：支持 (NodeID, MsgSeq) 全局唯一标识
//
// 使用场景:
//   - Gossip 协议通信
//   - Quorum 投票通信
//   - 2PC 协议通信
//   - 节点间元数据同步
type Transport interface {
	// Start 启动传输层
	// 初始化监听器、连接池等资源
	//
	// 参数：
	//   - nodeID: 节点 ID（全局唯一，用于消息去重和幂等性）
	//   - msgSeqGenerator: 消息序列号生成器（必需，单调递增）
	//   - listenAddr: 监听地址（必需，如 "0.0.0.0:9211" 或 "0.0.0.0:0" 自动分配端口）
	//
	// 使用示例：
	//   var seq uint64
	//   transport.Start(nil, func() uint64 {
	//       return atomic.AddUint64(&seq, 1)
	//   }, "0.0.0.0:9211")
	Start(nodeID *uint64, msgSeqGenerator func() uint64, listenAddr string) error

	// Stop 停止传输层
	// 优雅关闭所有连接、释放资源
	Stop() error

	// Send 发送消息到指定节点
	// 阻塞直到消息发送成功或失败
	//
	// 支持函数选项模式，可动态配置 TLV 扩展字段：
	//   transport.Send(ctx, addr, msg, WithHopCount(10))
	//   transport.Send(ctx, addr, msg, WithCompression(2), WithHopCount(5))
	//
	// 注意：此方法使用自动生成的 nodeID 和 msgSeq
	// 如果需要指定特定的 nodeID 和 msgSeq（如 RPC 场景），请使用 SendWithID()
	Send(ctx context.Context, addr string, msg Message, opt ...SendOpt) error

	// Reply 发送消息到指定节点（使用指定的 NodeID 和 MsgSeq）
	// 阻塞直到消息发送成功或失败
	//
	// 参数：
	//   - nodeID: 节点 ID（用于 CorrelationID 匹配）
	//   - msgSeq: 消息序列号（用于 CorrelationID 匹配）
	//   - connID: TCP 连接 ID（用于连接复用，UDP 消息传空字符串）
	//
	// 使用场景：
	//   - RPC 客户端：预先生成 CorrelationID，确保请求-响应匹配
	//   - RPC 服务端：使用请求中的 NodeID 和 MsgSeq 发送响应
	//   - TCP 连接复用：通过 connID 复用现有连接，避免创建新连接
	//
	// CorrelationID 格式："{NodeID}:{MsgSeq}"
	//
	// 示例：
	//   // RPC 客户端发送请求
	//   msgSeq := transport.GenerateMsgSeq()
	//   nodeID := transport.GetNodeID()
	//   correlationID := fmt.Sprintf("%d:%d", nodeID, msgSeq)
	//   transport.Reply(ctx, addr, msg, nodeID, msgSeq, "")
	//
	//   // RPC 服务端发送响应（TCP 连接复用）
	//   var nodeID, msgSeq uint64
	//   fmt.Sscanf(correlationID, "%d:%d", &nodeID, &msgSeq)
	//   transport.Reply(ctx, sourceAddr, resp, nodeID, msgSeq, reqFrame.ConnID)
	Reply(ctx context.Context, addr string, msg Message, nodeID uint64, msgSeq uint64, connID string, opts ...SendOpt) error

	// Receive 返回接收消息的通道
	// 调用者需要持续从通道读取消息
	//
	// 返回 MsgFrame（网络帧），包含原始消息和 TLV 扩展字段：
	//   for msgFrame := range transport.Receive() {
	//       if msgFrame.HasHopCount() {
	//           if hop, ok := msgFrame.GetHopCount(); ok {
	//               fmt.Printf("Hop: %d/%d\n", hop.Hop, hop.TotalHop)
	//           }
	//       }
	//   }
	Receive() <-chan MsgFrame

	// ForwardMessage 转发消息到指定节点
	// 自动递减 Hop Count（如果存在），Hop Count 减至 0 时返回错误
	//
	// 使用场景：
	//   - Gossip 协议消息转发
	//   - 消息广播
	//   - 节点间消息中继
	//
	// 行为：
	//   1. 检查 msgFrame 中 Hop Count 是否存在（通过 GetHopCount()）
	//   2. 如果存在且 Hop > 0，则递减 Hop（NewHop = Hop - 1）
	//   3. 如果 Hop == 0，返回 ErrHopCountExpired（消息已过期，不再转发）
	//   4. 如果不存在，直接转发（不添加 Hop Count）
	//   5. 保留所有 TLV 扩展字段（除 Hop Count 递减外）
	//
	// 返回：
	//   - uint64: 转发的消息序列号
	//   - error: 转发失败时返回错误
	ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error)

	// GetNodeID 获取当前节点 ID
	//
	// 返回:
	//   - uint64: 节点 ID（全局唯一，用于消息去重和幂等性）
	GetNodeID() uint64

	// GenerateMsgSeq 生成下一条消息序列号
	//
	// 返回:
	//   - uint64: 消息序列号（单调递增，全局唯一）
	GenerateMsgSeq() uint64
}

// 导出 types 包中的类型别名，方便 transport 包使用

// Message 传输消息接口
type Message = types.Message

// MessageType 消息类型
type MessageType = types.MessageType

// Priority 优先级类型
type Priority = types.Priority

// GetPriority 获取消息优先级
var GetPriority = types.GetPriority

// Address 节点地址
type Address = types.Address

// TransportConfig 传输层配置
type TransportConfig struct {
	// ListenAddr 监听地址
	ListenAddr string

	// MaxMessageSize 最大消息大小（字节）
	MaxMessageSize int64

	// ReadTimeout 读超时
	ReadTimeout time.Duration

	// WriteTimeout 写超时
	WriteTimeout time.Duration

	// KeepAliveInterval 保活间隔
	KeepAliveInterval time.Duration

	// KeepAliveTimeout 保活超时
	KeepAliveTimeout time.Duration

	// BufferSize 缓冲区大小
	BufferSize int

	// ChannelSendTimeout 通道发送超时（P2-2：接收通道阻塞检测）
	// 说明：当接收通道阻塞超过此时间时，认为消费者处理缓慢，丢弃消息并记录日志
	ChannelSendTimeout time.Duration
}

// DefaultTransportConfig 返回默认配置
//
// 注意：ListenAddr 需要由调用者配置，默认值仅为示例
// 生产环境应该根据实际网络环境配置合适的监听地址
func DefaultTransportConfig() *TransportConfig {
	return &TransportConfig{
		ListenAddr:         "",                // 需要调用者配置（如 "0.0.0.0:0" 自动分配端口）
		MaxMessageSize:     1024 * 1024 * 100, // 100MB
		ReadTimeout:        30 * time.Second,
		WriteTimeout:       30 * time.Second,
		KeepAliveInterval:  10 * time.Second,
		KeepAliveTimeout:   30 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 5 * time.Second, // P2-2: 默认 5 秒通道发送超时
	}
}

// Codec 编解码器接口
//
// 用于消息的序列化和反序列化
type Codec interface {
	// Encode 编码消息
	Encode(msg Message) ([]byte, error)

	// Decode 解码消息（创建新实例）
	Decode(msgType types.MessageType, data []byte) (Message, error)

	// DecodeInto 解码消息到指定实例（避免创建新消息）
	// 当消息类型已知时（如从 FixedHeader 读取）使用此方法更高效
	DecodeInto(data []byte, msg Message) error

	// Name 返回编解码器名称
	Name() string

	// Type 返回编解码器类型
	Type() types.CodecType
}

// ========================================
// 批量转发相关数据结构和接口
// ========================================

// BatchForwardMessageResult 批量转发结果
//
// 统计批量转发的成功/失败数量
type BatchForwardMessageResult struct {
	SuccessCount int                  // 成功数量
	FailureCount int                  // 失败数量
	Results      []BatchForwardResult // 详细结果列表
}

// BatchForwardResult 单次转发结果
//
// 记录单个地址的转发结果
type BatchForwardResult struct {
	Addr  string // 目标地址
	SeqID uint64 // 消息序列号（成功时）
	Error error  // 错误信息（失败时）
}

// BatchForwardTransport 批量转发传输接口
//
// 扩展 Transport 接口，支持批量转发
type BatchForwardTransport interface {
	Transport

	// BatchForwardMessage 批量转发消息
	//
	// 并发转发消息到多个目标地址，部分失败不影响其他转发
	//
	// 参数：
	//   - ctx: 上下文（支持取消和超时）
	//   - addrs: 目标地址列表
	//   - msgExt: 要转发的网络帧
	//
	// 返回：
	//   - BatchForwardMessageResult: 批量转发结果（成功/失败统计）
	//
	// 行为：
	//   1. 并发调用 ForwardMessage 转发到每个地址
	//   2. 单个地址失败不影响其他转发
	//   3. 收集所有转发的结果
	//   4. 返回成功/失败统计
	//
	// 使用场景：
	//   - Gossip 协议批量转发到随机节点
	//   - 消息广播到集群节点
	BatchForwardMessage(ctx context.Context, addrs []string, msgExt MsgFrame) BatchForwardMessageResult
}

// ========================================
// 传输层公共实现
// ========================================

// validateTransportConfig 验证传输层配置的有效性
//
// P2-5: 配置验证函数，确保配置值在合理范围内
//
// 注意：ListenAddr 的验证延迟到 Start() 时进行，允许配置对象先创建
func validateTransportConfig(config *TransportConfig) error {
	// ListenAddr 的验证在 Start() 方法中进行，因为：
	// 1. 支持延迟配置（如通过配置中心动态获取）
	// 2. 允许创建配置对象时不立即指定监听地址
	// 3. 更好的错误提示时机（启动时而非创建时）

	if config.MaxMessageSize <= 0 || config.MaxMessageSize > 1024*1024*1024 {
		return types.NewConfigValidationError("MaxMessageSize", fmt.Sprintf("必须在 (0, 1GB] 范围内，当前值: %d", config.MaxMessageSize))
	}

	// 验证超时配置（不能为负数）
	timeouts := []struct {
		name  string
		value time.Duration
	}{
		{"ReadTimeout", config.ReadTimeout},
		{"WriteTimeout", config.WriteTimeout},
		{"KeepAliveInterval", config.KeepAliveInterval},
		{"KeepAliveTimeout", config.KeepAliveTimeout},
		{"ChannelSendTimeout", config.ChannelSendTimeout},
	}

	for _, t := range timeouts {
		if t.value < 0 {
			return types.NewConfigValidationError(t.name, fmt.Sprintf("不能为负数，当前值: %v", t.value))
		}
	}

	if config.BufferSize <= 0 || config.BufferSize > 65536 {
		return types.NewConfigValidationError("BufferSize", fmt.Sprintf("必须在 (0, 64KB] 范围内，当前值: %d", config.BufferSize))
	}

	return nil
}

// createBatchForwardResult 创建批量转发失败结果（用于未启动的情况）
func createBatchForwardResult(addrs []string, err error) BatchForwardMessageResult {
	results := make([]BatchForwardResult, len(addrs))
	for i, addr := range addrs {
		results[i] = BatchForwardResult{
			Addr:  addr,
			SeqID: 0,
			Error: err,
		}
	}
	return BatchForwardMessageResult{
		SuccessCount: 0,
		FailureCount: len(addrs),
		Results:      results,
	}
}

// generateMsgSeq 生成消息序列号的公共实现
//
// 参数:
//   - generator: 存储在 atomic.Value 中的序列号生成器函数
//
// 返回:
//   - uint64: 消息序列号
//
// 注意：msgSeqGenerator 保证不为 nil（在 Start() 时已验证）
func generateMsgSeq(generator any) uint64 {
	fn, ok := generator.(func() uint64)
	if !ok || fn == nil {
		// 理论上不应该到达这里（Start() 已验证）
		// 但作为防御性编程，返回 0 表示错误
		return 0
	}

	return fn()
}

// batchForwarder 批量转发函数类型
type batchForwarder func(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error)

// executeBatchForward 执行批量转发的公共实现
//
// 参数:
//   - ctx: 上下文
//   - addrs: 目标地址列表
//   - msgExt: 要转发的消息
//   - forwarder: 单个转发函数
//
// 返回:
//   - BatchForwardMessageResult: 批量转发结果
func executeBatchForward(
	ctx context.Context,
	addrs []string,
	msgExt MsgFrame,
	forwarder batchForwarder,
) BatchForwardMessageResult {
	// 限制批量大小
	if len(addrs) > maxBatchSize {
		addrs = addrs[:maxBatchSize]
	}

	results := make([]BatchForwardResult, len(addrs))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxBatchConcurrency)

	for i, addr := range addrs {
		wg.Add(1)
		go func(idx int, targetAddr string) {
			defer wg.Done()
			semaphore <- struct{}{}        // 获取信号量
			defer func() { <-semaphore }() // 释放信号量

			seqID, err := forwarder(ctx, targetAddr, msgExt)
			results[idx] = BatchForwardResult{
				Addr:  targetAddr,
				SeqID: seqID,
				Error: err,
			}
		}(i, addr)
	}

	wg.Wait()

	// 统计结果
	var success, failure int
	for _, r := range results {
		if r.Error != nil {
			failure++
		} else {
			success++
		}
	}

	return BatchForwardMessageResult{
		SuccessCount: success,
		FailureCount: failure,
		Results:      results,
	}
}

// ========================================
// 网络连接辅助函数
// ========================================

// setWriteTimeout 设置写入超时（带零值检查）
//
// 参数：
//   - conn: 网络连接
//   - timeout: 超时时间（零值或负值表示不设置超时）
//
// 返回：
//   - error: 设置失败时返回错误
func setWriteTimeout(conn net.Conn, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	return conn.SetWriteDeadline(time.Now().Add(timeout))
}

// setReadTimeout 设置读取超时（带零值检查）
//
// 参数：
//   - conn: 网络连接
//   - timeout: 超时时间（零值或负值表示不设置超时）
//
// 返回：
//   - error: 设置失败时返回错误
func setReadTimeout(conn net.Conn, timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	return conn.SetReadDeadline(time.Now().Add(timeout))
}

// validateListenAddr 验证监听地址的有效性
//
// 参数：
//   - listenAddr: 监听地址（格式：host:port）
//   - protocol: 协议类型（"tcp" 或 "udp"）
//
// 返回：
//   - string: 验证通过的地址（规范化后的地址）
//   - error: 验证失败时返回错误
//
// 验证规则：
//   - 地址不能为空
//   - 地址格式必须为 host:port
//   - host 必须可以解析（为有效 IP 或可解析的 hostname）
//   - port 必须为有效端口号（1-65535，0 表示自动分配）
//
// 特殊值：
//   - "*" 或 "0.0.0.0" 表示监听所有网络接口
//   - port 为 0 表示由系统自动分配端口
func validateListenAddr(listenAddr, protocol string) (string, error) {
	// 检查地址是否为空
	if listenAddr == "" {
		return "", types.NewStoreInvalidParameterError("listenAddr 不能为空")
	}

	// 根据协议类型解析地址
	var resolvedAddr string

	switch protocol {
	case "tcp":
		tcpAddr, err := net.ResolveTCPAddr("tcp", listenAddr)
		if err != nil {
			return "", types.NewTransportInvalidListenAddrError(listenAddr, "TCP 解析失败", err)
		}
		resolvedAddr = tcpAddr.String()
	case "udp":
		udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
		if err != nil {
			return "", types.NewTransportInvalidListenAddrError(listenAddr, "UDP 解析失败", err)
		}
		resolvedAddr = udpAddr.String()
	default:
		return "", types.NewTransportUnsupportedProtocolError(protocol)
	}

	return resolvedAddr, nil
}
