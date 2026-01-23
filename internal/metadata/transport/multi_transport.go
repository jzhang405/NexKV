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

// MultiTransport 统计键名常量
const (
	multiStatKeyStarted         = "started"
	multiStatKeyStopped         = "stopped"
	multiStatKeyNodeID          = "node_id"
	multiStatKeyDefaultProtocol = "default_protocol"
	multiStatKeyProtocols       = "protocols"
	multiStatKeyMonitor         = "monitor"
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
	Config          ProtocolConfig // 协议配置
	Active          atomic.Bool    // 是否活跃
	FailureCount    atomic.Uint64  // 失败计数（用于降级判断）
	LastFailureTime atomic.Int64   // 最后一次失败时间
}

// MultiTransport 多协议传输实现
//
// 实现了基于多协议的网络传输层，支持：
//   - 多协议动态注册
//   - 智能路由决策（三维决策矩阵）
//   - 协议自动降级（协议层/业务层错误区分）
//   - 维度化监控统计
//   - 帧编解码统一（TCP粘包处理）
type MultiTransport struct {
	config             *TransportConfig                    // 配置
	codec              Codec                               // 编解码器
	NodeID             atomic.Uint64                       // 节点 ID
	msgSeqGenerator    atomic.Value                        // 消息序列号生成器 func() uint64
	defaultSeqCounter  atomic.Uint64                       // 默认序列号计数器
	protocols          map[ProtocolType]*ProtocolTransport // 协议管理（key: ProtocolType）
	protocolsMu        sync.RWMutex                        // 协议锁
	defaultProtocol    atomic.Value                        // 默认协议（存储 ProtocolType）
	router             *MessageRouter                      // 消息路由器（三维决策矩阵）
	degradationManager *DegradationManager                 // 降级管理器
	monitor            *DimensionalMonitor                 // 维度化监控器
	tcpCodec           *TCPFrameCodec                      // TCP 帧编解码器
	udpCodec           *UDPFrameCodec                      // UDP 帧编解码器
	tcpStreamDecoder   *TCPFrameStreamDecoder              // TCP 流式解码器
	recvCh             chan MsgFrame                       // 接收通道（合并所有协议的消息）
	overflowCh         chan MsgFrame                       // 溢出通道（背压机制）
	recvOnce           sync.Once                           // 接收通道单例
	stateMu            sync.RWMutex                        // 状态锁（协议切换+降级缓存）
	degradedProtocols  map[ProtocolType]ProtocolType       // 已降级协议缓存
	started            atomic.Bool                         // 已启动
	stopped            atomic.Bool                         // 已停止
	stopCh             chan struct{}                       // 停止通道
	stopOnce           sync.Once                           // 停止单例
	wg                 sync.WaitGroup                      // 等待组
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

	// 创建消息路由器（使用默认路由配置）
	router := NewMessageRouter(DefaultRouterConfig())

	// 创建降级管理器（使用默认降级配置）
	degradationManager := NewDegradationManager(DefaultDegradationConfig())

	// 创建维度化监控器
	monitor := NewDimensionalMonitor()

	// 创建帧编解码器
	tcpCodec := NewTCPFrameCodec()
	udpCodec := NewUDPFrameCodec()
	tcpStreamDecoder := NewTCPFrameStreamDecoder()

	mt := &MultiTransport{
		config:             config,
		codec:              codec,
		protocols:          make(map[ProtocolType]*ProtocolTransport),
		recvCh:             make(chan MsgFrame, config.BufferSize),
		overflowCh:         make(chan MsgFrame, config.BufferSize/2), // 溢出通道为缓冲区的一半
		stopCh:             make(chan struct{}),
		router:             router,
		degradationManager: degradationManager,
		monitor:            monitor,
		tcpCodec:           tcpCodec,
		udpCodec:           udpCodec,
		tcpStreamDecoder:   tcpStreamDecoder,
		degradedProtocols:  make(map[ProtocolType]ProtocolType),
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

	// 启动溢出通道处理循环（背压机制）
	mt.wg.Add(1)
	go mt.overflowLoop()

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

	// 关闭接收通道和溢出通道
	mt.recvOnce.Do(func() {
		close(mt.recvCh)
		close(mt.overflowCh)
	})

	logging.Infof("MultiTransport 停止成功")
	return nil
}

// receiveLoop 接收循环（从指定协议接收消息）
//
// 集成维度化监控：记录消息接收统计、接收延迟、协议使用情况
// 使用背压机制：当主通道满时，消息发送到溢出通道，避免阻塞
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

			// 记录接收消息（监控）
			msgType := msgFrame.Type()
			msgSize := 0
			// 估算消息大小（从 FixedHeader.DataLength 获取）
			if msgFrame.Message != nil {
				// 尝试从消息获取 payload 大小
				if baseMsg, ok := msgFrame.Message.(*BaseMessage); ok {
					msgSize = len(baseMsg.GetPayload())
				}
			}

			// 从消息中提取源地址（使用 NodeID）
			nodeAddr := "unknown"
			if msgFrame.NodeID > 0 {
				nodeAddr = fmt.Sprintf("node-%d", msgFrame.NodeID)
			}

			// 记录接收成功（监控）
			mt.monitor.RecordMessage(msgType, protocolType, nodeAddr, msgSize, 0, true, nil)

			// 使用背压机制：先尝试发送到主通道，满则发送到溢出通道
			select {
			case mt.recvCh <- msgFrame:
				// 主通道发送成功，继续处理下一条消息
				continue
			default:
				// 主通道已满，发送到溢出通道（非阻塞）
				select {
				case mt.overflowCh <- msgFrame:
					// 溢出通道发送成功
					logging.Debugf("消息发送到溢出通道: %s from %s",
						msgFrame.Type(), protocolType)
				case mt.overflowCh <- msgFrame:
					// 第二次尝试（确保发送成功）
				default:
					// 溢出通道也已满，丢弃消息
					logging.Warnf("接收通道和溢出通道均满，丢弃消息: %s from %s",
						msgFrame.Type(), protocolType)
					// 记录丢弃消息到监控
					mt.monitor.RecordMessage(msgType, protocolType, nodeAddr, msgSize, 0, false,
						fmt.Errorf("接收通道和溢出通道均满"))
				}
			}
		}
	}
}

// overflowLoop 溢出通道处理循环
//
// 将溢出通道中的消息重新发送到主接收通道
func (mt *MultiTransport) overflowLoop() {
	defer mt.wg.Done()

	for {
		select {
		case <-mt.stopCh:
			return
		case msgFrame, ok := <-mt.overflowCh:
			if !ok {
				return
			}

			// 尝试将溢出消息发送回主通道
			select {
			case mt.recvCh <- msgFrame:
				// 发送成功，继续处理下一个溢出消息
				continue
			case <-mt.stopCh:
				// 停止信号，退出前将消息放回溢出通道
				select {
				case mt.overflowCh <- msgFrame:
					// 成功放回，让其他协程处理
				default:
					// 溢出通道也满了，只能丢弃
					msgType := msgFrame.Type()
					logging.Warnf("停止时溢出消息无法放回，丢弃: %s", msgType)
				}
				return
			case <-time.After(100 * time.Millisecond):
				// 主通道仍然满，将消息放回溢出通道末尾
				select {
				case mt.overflowCh <- msgFrame:
					// 成功放回，稍后重试
				default:
					// 溢出通道也满了，只能丢弃
					msgType := msgFrame.Type()
					logging.Warnf("主通道和溢出通道均满，丢弃溢出消息: %s", msgType)
				}
			}
		}
	}
}

// Send 发送消息到指定节点（使用智能路由选择协议）
//
// 集成三维决策矩阵：回应期望、消息大小、可靠性要求
// 集成降级机制：协议层错误触发降级，业务层错误不触发降级
// 集成维度化监控：记录消息发送统计、协议使用情况、错误类型
func (mt *MultiTransport) Send(ctx context.Context, addr string, msg Message, opts ...SendOpt) error {
	if !mt.started.Load() || mt.stopped.Load() {
		return types.NewTransportStateError("未启动或已停止")
	}

	// 记录开始时间（用于监控延迟）
	startTime := time.Now()

	// 1. 获取消息类型和大小
	msgType := msg.Type()
	msgSize := 0
	// 尝试从消息获取 payload 大小
	if baseMsg, ok := msg.(*BaseMessage); ok {
		msgSize = len(baseMsg.GetPayload())
	}

	// 3. 使用消息路由器进行三维决策（如果路由器启用）
	// 注意：DecideProtocol API 已简化，expectResponse 和 reliability 从 msgType 自动推断
	routeDecision := mt.router.DecideProtocol(ctx, addr, msgType, msgSize)

	// 4. 选择协议
	var selectedProtocol ProtocolType
	if routeDecision.ProtocolType != "" {
		// 使用路由器选择的协议
		selectedProtocol = routeDecision.ProtocolType
	} else {
		// 使用默认协议
		defaultProtocol := mt.defaultProtocol.Load()
		if defaultProtocol == nil {
			return types.NewOpErr(types.ErrCodeInternal, "Send",
				"未设置默认协议", nil)
		}
		var ok bool
		selectedProtocol, ok = defaultProtocol.(ProtocolType)
		if !ok {
			return types.NewOpErr(types.ErrCodeInternal, "Send",
				"默认协议类型无效", nil)
		}
	}

	// 5. 检查协议是否需要降级（带锁保护）
	if routeDecision.ShouldDegrade {
		// 使用单一状态锁，避免死锁
		mt.stateMu.Lock()
		// 检查缓存是否已有降级状态
		cachedProtocol, hasCache := mt.degradedProtocols[selectedProtocol]

		if hasCache {
			// 使用缓存的降级协议
			selectedProtocol = cachedProtocol
			mt.stateMu.Unlock()
			logging.Debugf("协议降级（缓存）: %s -> %s",
				routeDecision.ProtocolType, selectedProtocol)
		} else {
			// 执行降级检查
			if shouldDegrade, reason := mt.degradationManager.ShouldDegrade(selectedProtocol, nil); shouldDegrade {
				// 切换到备用协议（TCP -> UDP 或 UDP -> TCP）
				var fallbackProtocol ProtocolType
				if selectedProtocol == ProtocolTCP {
					fallbackProtocol = ProtocolUDP
				} else {
					fallbackProtocol = ProtocolTCP
				}

				// 记录降级状态到缓存
				mt.degradedProtocols[selectedProtocol] = fallbackProtocol
				selectedProtocol = fallbackProtocol
				mt.stateMu.Unlock()
				logging.Debugf("协议降级: %s -> %s, 原因: %s",
					routeDecision.ProtocolType, selectedProtocol, reason)
			} else {
				mt.stateMu.Unlock()
			}
		}
	}

	// 6. 检查所选协议是否可用
	mt.protocolsMu.RLock()
	pt, exists := mt.protocols[selectedProtocol]
	mt.protocolsMu.RUnlock()

	if !exists {
		err := fmt.Errorf("协议 %s 未注册", selectedProtocol)
		// 记录发送失败（监控）
		latency := time.Since(startTime).Nanoseconds()
		mt.monitor.RecordMessage(msgType, selectedProtocol, addr, msgSize, latency, false, err)
		return err
	}

	if !pt.Active.Load() {
		err := fmt.Errorf("协议 %s 未启动", selectedProtocol)
		// 记录发送失败（监控）
		latency := time.Since(startTime).Nanoseconds()
		mt.monitor.RecordMessage(msgType, selectedProtocol, addr, msgSize, latency, false, err)
		return err
	}

	// 7. 发送消息
	err := pt.Config.Transport.Send(ctx, addr, msg, opts...)

	// 8. 记录发送结果（监控）
	latency := time.Since(startTime).Nanoseconds()
	success := (err == nil)
	mt.monitor.RecordMessage(msgType, selectedProtocol, addr, msgSize, latency, success, err)

	// 9. 更新协议失败计数（用于降级判断）
	if err != nil {
		pt.FailureCount.Add(1)
		pt.LastFailureTime.Store(time.Now().UnixNano())

		// 检查是否需要降级
		if shouldDegrade, reason := mt.degradationManager.ShouldDegrade(selectedProtocol, err); shouldDegrade {
			logging.Warnf("协议降级触发: %s, 原因: %s", selectedProtocol, reason)
		}
	}

	return err
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
	stats[multiStatKeyStarted] = mt.started.Load()
	stats[multiStatKeyStopped] = mt.stopped.Load()
	stats[multiStatKeyNodeID] = mt.NodeID.Load()

	// 获取默认协议
	if defaultProtocol := mt.defaultProtocol.Load(); defaultProtocol != nil {
		stats[multiStatKeyDefaultProtocol] = defaultProtocol
	}

	// 获取协议统计
	stats[multiStatKeyProtocols] = mt.GetProtocolStats()

	// 获取全局监控统计
	stats[multiStatKeyMonitor] = mt.monitor.GetGlobalStats()

	return stats
}

// GetRouterStats 获取路由器统计信息
func (mt *MultiTransport) GetRouterStats() *RouterStats {
	return mt.router.GetStats()
}

// UpdateRouterConfig 更新路由器配置
func (mt *MultiTransport) UpdateRouterConfig(config *RouterConfig) {
	mt.router.UpdateConfig(config)
}

// GetDegradationStats 获取降级统计信息
func (mt *MultiTransport) GetDegradationStats() *DegradationStats {
	return mt.degradationManager.GetStats()
}

// GetProtocolState 获取协议降级状态
func (mt *MultiTransport) GetProtocolState(protocolType ProtocolType) (*ProtocolStateSnapshot, bool) {
	return mt.degradationManager.GetProtocolState(protocolType)
}

// UpdateDegradationConfig 更新降级配置
func (mt *MultiTransport) UpdateDegradationConfig(config *DegradationConfig) {
	mt.degradationManager.UpdateConfig(config)
}

// ShouldRecoverProtocol 判断协议是否应该恢复
func (mt *MultiTransport) ShouldRecoverProtocol(protocolType ProtocolType) (bool, string) {
	return mt.degradationManager.ShouldRecover(protocolType)
}

// GetMessageTypeStats 获取消息类型统计
func (mt *MultiTransport) GetMessageTypeStats(msgType types.MessageType) (*MessageTypeStats, bool) {
	return mt.monitor.GetMessageTypeStats(msgType)
}

// GetAllMessageTypeStats 获取所有消息类型统计
func (mt *MultiTransport) GetAllMessageTypeStats() map[types.MessageType]*MessageTypeStats {
	return mt.monitor.GetAllMessageTypeStats()
}

// GetNodeStats 获取节点统计
func (mt *MultiTransport) GetNodeStats(nodeAddr string) (*NodeStats, bool) {
	return mt.monitor.GetNodeStats(nodeAddr)
}

// GetAllNodeStats 获取所有节点统计
func (mt *MultiTransport) GetAllNodeStats() map[string]*NodeStats {
	return mt.monitor.GetAllNodeStats()
}

// GetErrorTypeStats 获取错误类型统计
func (mt *MultiTransport) GetErrorTypeStats(errType string) (*ErrorTypeStats, bool) {
	return mt.monitor.GetErrorTypeStats(errType)
}

// GetAllErrorTypeStats 获取所有错误类型统计
func (mt *MultiTransport) GetAllErrorTypeStats() map[string]*ErrorTypeStats {
	return mt.monitor.GetAllErrorTypeStats()
}

// GetMonitorStats 获取协议统计（监控器）
func (mt *MultiTransport) GetMonitorStats(protocolType ProtocolType) (*ProtocolStats, bool) {
	return mt.monitor.GetProtocolStats(protocolType)
}

// GetAllMonitorStats 获取所有协议统计（监控器）
func (mt *MultiTransport) GetAllMonitorStats() map[ProtocolType]*ProtocolStats {
	return mt.monitor.GetAllProtocolStats()
}

// GetMonitorGlobalStats 获取全局监控统计
func (mt *MultiTransport) GetMonitorGlobalStats() *GlobalStats {
	return mt.monitor.GetGlobalStats()
}

// ResetMonitorStats 重置监控统计
func (mt *MultiTransport) ResetMonitorStats() {
	mt.monitor.Reset()
}

// GetTCPCodec 获取 TCP 帧编解码器
func (mt *MultiTransport) GetTCPCodec() *TCPFrameCodec {
	return mt.tcpCodec
}

// GetUDPCodec 获取 UDP 帧编解码器
func (mt *MultiTransport) GetUDPCodec() *UDPFrameCodec {
	return mt.udpCodec
}

// GetTCPStreamDecoder 获取 TCP 流式解码器
func (mt *MultiTransport) GetTCPStreamDecoder() *TCPFrameStreamDecoder {
	return mt.tcpStreamDecoder
}
