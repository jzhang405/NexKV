// Package transport 多协议传输实现
//
// 核心特性:
//   - 多协议动态注册（TCP、UDP、gRPC 等）
//   - 协议降级与切换
//   - 统一消息接收通道
//   - 协议级别统计与监控
package transport

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// ProtocolType 协议类型
type ProtocolType string

const (
	ProtocolTCP  ProtocolType = "tcp"
	ProtocolUDP  ProtocolType = "udp"
	ProtocolGRPC ProtocolType = "grpc"
)

// ProtocolConfig 协议配置
type ProtocolConfig struct {
	// ProtocolType 协议类型
	ProtocolType ProtocolType

	// Priority 协议优先级（数字越大优先级越高）
	// 用于默认协议选择和降级顺序
	Priority int

	// CanDegrade 是否允许降级到其他协议
	// false 表示该协议不可降级（如关键业务协议）
	CanDegrade bool

	// Transport 该协议对应的 Transport 实例
	Transport Transport
}

// ProtocolTransport 协议实例包装
type ProtocolTransport struct {
	// Config 协议配置
	Config ProtocolConfig

	// Active 是否活跃
	Active atomic.Bool

	// FailureCount 失败计数（用于降级判断）
	FailureCount atomic.Uint64

	// LastFailureTime 最后一次失败时间
	LastFailureTime atomic.Int64
}

// MultiTransport 多协议传输实现
//
// 实现了基于多协议的网络传输层，支持：
//   - 多协议动态注册
//   - 协议自动降级
//   - 统一消息接收
//   - 协议级别监控
type MultiTransport struct {
	// 配置
	config *TransportConfig
	codec  Codec

	// 节点标识
	NodeID            atomic.Uint64
	msgSeqGenerator   atomic.Value  // 存储 func() uint64
	defaultSeqCounter atomic.Uint64 // 默认序列号计数器

	// 协议管理（key: ProtocolType）
	protocols   map[ProtocolType]*ProtocolTransport
	protocolsMu sync.RWMutex

	// 默认协议
	defaultProtocol atomic.Value // 存储 ProtocolType

	// 接收通道（合并所有协议的消息）
	recvCh   chan MsgFrame
	recvOnce sync.Once

	// 生命周期
	started  atomic.Bool
	stopped  atomic.Bool
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewMultiTransport 创建多协议传输
//
// 使用默认配置
func NewMultiTransport(listenAddr string) (*MultiTransport, error) {
	return NewMultiTransportWithConfig(&TransportConfig{
		ListenAddr:         listenAddr,
		MaxMessageSize:     1024 * 1024 * 100, // 100MB
		ReadTimeout:        30 * time.Second,
		WriteTimeout:       30 * time.Second,
		KeepAliveInterval:  10 * time.Second,
		KeepAliveTimeout:   30 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 5 * time.Second,
	})
}

// NewMultiTransportWithConfig 创建多协议传输（自定义配置）
func NewMultiTransportWithConfig(config *TransportConfig) (*MultiTransport, error) {
	if config == nil {
		config = DefaultTransportConfig()
	}

	// 验证配置
	if err := validateTransportConfig(config); err != nil {
		return nil, err
	}

	// 使用系统默认编解码器（Protobuf）
	codec, err := NewCodec(defaultCodec)
	if err != nil {
		return nil, err
	}

	mt := &MultiTransport{
		config:    config,
		codec:     codec,
		protocols: make(map[ProtocolType]*ProtocolTransport),
		recvCh:    make(chan MsgFrame, config.BufferSize),
		stopCh:    make(chan struct{}),
	}

	return mt, nil
}

// RegisterProtocol 注册协议
//
// 参数:
//   - config: 协议配置（包含协议类型、优先级、是否可降级、Transport 实例）
//
// 返回:
//   - error: 注册失败时返回错误
//
// 使用示例:
//
//	tcp, _ := NewTCPTransport("127.0.0.1:9211")
//	udp, _ := NewUDPTransport("127.0.0.1:9212")
//
//	multiTransport.RegisterProtocol(ProtocolConfig{
//	    ProtocolType: ProtocolTCP,
//	    Priority:     10,
//	    CanDegrade:   true,
//	    Transport:    tcp,
//	})
//
//	multiTransport.RegisterProtocol(ProtocolConfig{
//	    ProtocolType: ProtocolUDP,
//	    Priority:     5,
//	    CanDegrade:   true,
//	    Transport:    udp,
//	})
func (mt *MultiTransport) RegisterProtocol(config ProtocolConfig) error {
	if config.Transport == nil {
		return types.NewOpErr(types.ErrCodeInternal, "RegisterProtocol",
			"协议 Transport 实例不能为 nil", nil)
	}

	mt.protocolsMu.Lock()
	defer mt.protocolsMu.Unlock()

	// 检查是否已注册
	if _, exists := mt.protocols[config.ProtocolType]; exists {
		return fmt.Errorf("协议 %s 已注册", config.ProtocolType)
	}

	// 创建协议实例包装
	pt := &ProtocolTransport{
		Config: config,
	}

	// 存储协议
	mt.protocols[config.ProtocolType] = pt

	// 如果是第一个注册的协议，设置为默认协议
	if mt.defaultProtocol.Load() == nil {
		mt.defaultProtocol.Store(config.ProtocolType)
	}

	logging.Infof("注册协议: %s (优先级: %d, 可降级: %t)",
		config.ProtocolType, config.Priority, config.CanDegrade)

	return nil
}

// SetDefaultProtocol 设置默认协议
//
// 参数:
//   - protocolType: 协议类型
//
// 返回:
//   - error: 协议不存在时返回错误
func (mt *MultiTransport) SetDefaultProtocol(protocolType ProtocolType) error {
	mt.protocolsMu.RLock()
	_, exists := mt.protocols[protocolType]
	mt.protocolsMu.RUnlock()

	if !exists {
		return types.NewOpErr(types.ErrCodeInternal, "SetDefaultProtocol",
			fmt.Sprintf("协议 %s 未注册", protocolType), nil)
	}

	mt.defaultProtocol.Store(protocolType)
	logging.Infof("设置默认协议: %s", protocolType)
	return nil
}

// Start 启动传输层
//
// 扩展参数（可选，传入 nil 表示使用默认值）：
//   - nodeID: 节点 ID（全局唯一，用于消息去重和幂等性）
//   - msgSeqGenerator: 消息序列号生成器（nil 表示使用默认原子计数器）
func (mt *MultiTransport) Start(nodeID *uint64, msgSeqGenerator func() uint64) error {
	if !mt.started.CompareAndSwap(false, true) {
		return types.NewTransportStateError("已经启动")
	}

	// 设置节点 ID
	if nodeID != nil {
		mt.NodeID.Store(*nodeID)
	}

	// 设置消息序列号生成器
	if msgSeqGenerator != nil {
		mt.msgSeqGenerator.Store(msgSeqGenerator)
	} else {
		// 使用默认原子计数器
		mt.msgSeqGenerator.Store(func() uint64 {
			return mt.defaultSeqCounter.Add(1)
		})
	}

	logging.Infof("启动 MultiTransport，NodeID: %d", mt.NodeID.Load())

	// 启动所有已注册的协议
	mt.protocolsMu.RLock()
	defer mt.protocolsMu.RUnlock()

	if len(mt.protocols) == 0 {
		mt.started.Store(false)
		return types.NewOpErr(types.ErrCodeInternal, "Start",
			"未注册任何协议", nil)
	}

	for protocolType, pt := range mt.protocols {
		// 启动底层 Transport
		if err := pt.Config.Transport.Start(nodeID, msgSeqGenerator); err != nil {
			// 回滚已启动的协议
			for _, pt2 := range mt.protocols {
				if pt2 != pt && pt2.Active.Load() {
					_ = pt2.Config.Transport.Stop()
				}
			}
			mt.started.Store(false)
			return types.NewTransportConnectionError("启动协议", string(protocolType), err)
		}

		// 标记为活跃
		pt.Active.Store(true)

		// 启动消息接收协程
		mt.wg.Add(1)
		go mt.receiveLoop(protocolType, pt.Config.Transport.Receive())

		logging.Infof("协议 %s 启动成功", protocolType)
	}

	logging.Infof("MultiTransport 启动成功，协议数量: %d", len(mt.protocols))
	return nil
}

// Stop 停止传输层
func (mt *MultiTransport) Stop() error {
	if !mt.stopped.CompareAndSwap(false, true) {
		return types.NewTransportStateError("已经停止")
	}

	logging.Infof("停止 MultiTransport")

	// 关闭停止通道
	mt.stopOnce.Do(func() {
		close(mt.stopCh)
	})

	// 停止所有协议
	mt.protocolsMu.Lock()
	defer mt.protocolsMu.Unlock()

	for protocolType, pt := range mt.protocols {
		if pt.Active.Load() {
			if err := pt.Config.Transport.Stop(); err != nil {
				logging.Errorf("停止协议 %s 失败: %v", protocolType, err)
			}
			pt.Active.Store(false)
		}
	}

	// 等待所有协程退出
	mt.wg.Wait()

	// 关闭接收通道
	mt.recvOnce.Do(func() {
		close(mt.recvCh)
	})

	logging.Infof("MultiTransport 停止成功")
	return nil
}

// receiveLoop 接收循环（从指定协议接收消息）
func (mt *MultiTransport) receiveLoop(protocolType ProtocolType, recvCh <-chan MsgFrame) {
	defer mt.wg.Done()

	for {
		select {
		case <-mt.stopCh:
			return
		case msgFrame, ok := <-recvCh:
			if !ok {
				logging.Debugf("协议 %s 接收通道已关闭", protocolType)
				return
			}

			// 设置来源协议标识
			// TODO: 在 MsgFrame 中添加来源协议字段

			// 发送到统一接收通道（带超时）
			select {
			case mt.recvCh <- msgFrame:
				// 发送成功
			case <-time.After(mt.config.ChannelSendTimeout):
				// 通道阻塞超时
				logging.Warnf("接收通道阻塞超时，丢弃消息: %s from %s",
					msgFrame.Type(), protocolType)
			case <-mt.stopCh:
				return
			}
		}
	}
}

// Send 发送消息到指定节点（使用默认协议）
func (mt *MultiTransport) Send(ctx context.Context, addr string, msg Message, opts ...SendOpt) error {
	if !mt.started.Load() || mt.stopped.Load() {
		return types.NewTransportStateError("未启动或已停止")
	}

	// 获取默认协议
	defaultProtocol := mt.defaultProtocol.Load()
	if defaultProtocol == nil {
		return types.NewOpErr(types.ErrCodeInternal, "Send",
			"未设置默认协议", nil)
	}

	protocolType, ok := defaultProtocol.(ProtocolType)
	if !ok {
		return types.NewOpErr(types.ErrCodeInternal, "Send",
			"默认协议类型无效", nil)
	}

	return mt.SendWithProtocol(ctx, addr, msg, protocolType, opts...)
}

// SendWithProtocol 使用指定协议发送消息
func (mt *MultiTransport) SendWithProtocol(ctx context.Context, addr string, msg Message, protocolType ProtocolType, opts ...SendOpt) error {
	if !mt.started.Load() || mt.stopped.Load() {
		return types.NewTransportStateError("未启动或已停止")
	}

	mt.protocolsMu.RLock()
	pt, exists := mt.protocols[protocolType]
	mt.protocolsMu.RUnlock()

	if !exists {
		return fmt.Errorf("协议 %s 未注册", protocolType)
	}

	if !pt.Active.Load() {
		return fmt.Errorf("协议 %s 未启动", protocolType)
	}

	// 发送消息
	err := pt.Config.Transport.Send(ctx, addr, msg, opts...)

	// 更新失败计数
	if err != nil {
		pt.FailureCount.Add(1)
		pt.LastFailureTime.Store(time.Now().UnixNano())
	}

	return err
}

// Receive 返回接收消息的通道
func (mt *MultiTransport) Receive() <-chan MsgFrame {
	return mt.recvCh
}

// ForwardMessage 转发消息到指定节点（使用默认协议）
func (mt *MultiTransport) ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
	if !mt.started.Load() || mt.stopped.Load() {
		return 0, types.NewTransportStateError("未启动或已停止")
	}

	// 获取默认协议
	defaultProtocol := mt.defaultProtocol.Load()
	if defaultProtocol == nil {
		return 0, types.NewOpErr(types.ErrCodeInternal, "ForwardMessage",
			"未设置默认协议", nil)
	}

	protocolType, ok := defaultProtocol.(ProtocolType)
	if !ok {
		return 0, types.NewOpErr(types.ErrCodeInternal, "ForwardMessage",
			"默认协议类型无效", nil)
	}

	return mt.ForwardMessageWithProtocol(ctx, addr, msgExt, protocolType)
}

// ForwardMessageWithProtocol 使用指定协议转发消息
func (mt *MultiTransport) ForwardMessageWithProtocol(ctx context.Context, addr string, msgExt MsgFrame, protocolType ProtocolType) (uint64, error) {
	if !mt.started.Load() || mt.stopped.Load() {
		return 0, types.NewTransportStateError("未启动或已停止")
	}

	mt.protocolsMu.RLock()
	pt, exists := mt.protocols[protocolType]
	mt.protocolsMu.RUnlock()

	if !exists {
		return 0, fmt.Errorf("协议 %s 未注册", protocolType)
	}

	if !pt.Active.Load() {
		return 0, fmt.Errorf("协议 %s 未启动", protocolType)
	}

	// 转发消息
	msgSeq, err := pt.Config.Transport.ForwardMessage(ctx, addr, msgExt)

	// 更新失败计数
	if err != nil {
		pt.FailureCount.Add(1)
		pt.LastFailureTime.Store(time.Now().UnixNano())
	}

	return msgSeq, err
}

// BatchForwardMessage 批量转发消息（使用默认协议）
func (mt *MultiTransport) BatchForwardMessage(ctx context.Context, addrs []string, msgExt MsgFrame) BatchForwardMessageResult {
	if !mt.started.Load() || mt.stopped.Load() {
		return BatchForwardMessageResult{
			SuccessCount: 0,
			FailureCount: len(addrs),
			Results:      make([]BatchForwardResult, 0),
		}
	}

	// 获取默认协议
	defaultProtocol := mt.defaultProtocol.Load()
	if defaultProtocol == nil {
		err := types.NewOpErr(types.ErrCodeInternal, "BatchForwardMessage",
			"未设置默认协议", nil)
		results := make([]BatchForwardResult, len(addrs))
		for i, addr := range addrs {
			results[i] = BatchForwardResult{Addr: addr, Error: err}
		}
		return BatchForwardMessageResult{
			SuccessCount: 0,
			FailureCount: len(addrs),
			Results:      results,
		}
	}

	protocolType, ok := defaultProtocol.(ProtocolType)
	if !ok {
		err := types.NewOpErr(types.ErrCodeInternal, "BatchForwardMessage",
			"默认协议类型无效", nil)
		results := make([]BatchForwardResult, len(addrs))
		for i, addr := range addrs {
			results[i] = BatchForwardResult{Addr: addr, Error: err}
		}
		return BatchForwardMessageResult{
			SuccessCount: 0,
			FailureCount: len(addrs),
			Results:      results,
		}
	}

	return mt.BatchForwardMessageWithProtocol(ctx, addrs, msgExt, protocolType)
}

// BatchForwardMessageWithProtocol 使用指定协议批量转发消息
func (mt *MultiTransport) BatchForwardMessageWithProtocol(ctx context.Context, addrs []string, msgExt MsgFrame, protocolType ProtocolType) BatchForwardMessageResult {
	mt.protocolsMu.RLock()
	pt, exists := mt.protocols[protocolType]
	mt.protocolsMu.RUnlock()

	if !exists {
		err := fmt.Errorf("协议 %s 未注册", protocolType)
		results := make([]BatchForwardResult, len(addrs))
		for i, addr := range addrs {
			results[i] = BatchForwardResult{Addr: addr, Error: err}
		}
		return BatchForwardMessageResult{
			SuccessCount: 0,
			FailureCount: len(addrs),
			Results:      results,
		}
	}

	// 检查是否实现 BatchForwardTransport 接口
	batchTransport, ok := pt.Config.Transport.(BatchForwardTransport)
	if !ok {
		// 未实现批量转发接口，逐个转发
		return mt.batchForwardSequential(ctx, addrs, msgExt, pt)
	}

	// 使用批量转发接口
	result := batchTransport.BatchForwardMessage(ctx, addrs, msgExt)

	// 更新失败计数
	if result.FailureCount > 0 {
		pt.FailureCount.Add(uint64(result.FailureCount))
		pt.LastFailureTime.Store(time.Now().UnixNano())
	}

	return result
}

// batchForwardSequential 顺序批量转发（当 Transport 未实现 BatchForwardTransport 接口时）
func (mt *MultiTransport) batchForwardSequential(ctx context.Context, addrs []string, msgExt MsgFrame, pt *ProtocolTransport) BatchForwardMessageResult {
	results := make([]BatchForwardResult, len(addrs))
	successCount := 0
	failureCount := 0

	for i, addr := range addrs {
		msgSeq, err := pt.Config.Transport.ForwardMessage(ctx, addr, msgExt)
		results[i] = BatchForwardResult{
			Addr:  addr,
			SeqID: msgSeq,
			Error: err,
		}

		if err != nil {
			failureCount++
			pt.FailureCount.Add(1)
		} else {
			successCount++
		}
	}

	// 更新最后失败时间
	if failureCount > 0 {
		pt.LastFailureTime.Store(time.Now().UnixNano())
	}

	return BatchForwardMessageResult{
		SuccessCount: successCount,
		FailureCount: failureCount,
		Results:      results,
	}
}

// GetNodeID 获取当前节点 ID
func (mt *MultiTransport) GetNodeID() uint64 {
	return mt.NodeID.Load()
}

// GenerateMsgSeq 生成下一条消息序列号
func (mt *MultiTransport) GenerateMsgSeq() uint64 {
	return generateMsgSeq(mt.msgSeqGenerator.Load(), &mt.defaultSeqCounter)
}

// GetActiveProtocol 获取当前活跃的默认协议
func (mt *MultiTransport) GetActiveProtocol() (ProtocolType, error) {
	defaultProtocol := mt.defaultProtocol.Load()
	if defaultProtocol == nil {
		return "", types.NewOpErr(types.ErrCodeInternal, "GetActiveProtocol",
			"未设置默认协议", nil)
	}

	protocolType, ok := defaultProtocol.(ProtocolType)
	if !ok {
		return "", types.NewOpErr(types.ErrCodeInternal, "GetActiveProtocol",
			"默认协议类型无效", nil)
	}

	return protocolType, nil
}

// GetProtocolStats 获取协议统计信息
func (mt *MultiTransport) GetProtocolStats() map[ProtocolType]map[string]any {
	mt.protocolsMu.RLock()
	defer mt.protocolsMu.RUnlock()

	stats := make(map[ProtocolType]map[string]any)

	for protocolType, pt := range mt.protocols {
		stats[protocolType] = map[string]any{
			"active":            pt.Active.Load(),
			"failure_count":     pt.FailureCount.Load(),
			"last_failure_time": time.Unix(0, pt.LastFailureTime.Load()),
			"priority":          pt.Config.Priority,
			"can_degrade":       pt.Config.CanDegrade,
		}
	}

	return stats
}

// Stats 获取统计信息
func (mt *MultiTransport) Stats() map[string]any {
	stats := make(map[string]any)
	stats["started"] = mt.started.Load()
	stats["stopped"] = mt.stopped.Load()
	stats["node_id"] = mt.NodeID.Load()

	// 获取默认协议
	if defaultProtocol := mt.defaultProtocol.Load(); defaultProtocol != nil {
		stats["default_protocol"] = defaultProtocol
	}

	// 获取协议统计
	stats["protocols"] = mt.GetProtocolStats()

	return stats
}
