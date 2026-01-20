// Package transport UDP 传输实现
//
// 核心特性:
//   - UDP 分片/重组（自动处理大消息）
//   - MessagePack 序列化
//   - CRC32 校验和
//   - 并发安全
package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// UDP 分片协议常量
const (
	// MaxUDPPacketSize 单个 UDP 包最大数据量
	// 1500 (MTU) - 20 (IP头) - 8 (UDP头) - 16 (TLV FixedHeader) ≈ 1456，保守取 1400
	MaxUDPPacketSize = 1400

	// DefaultFragmentTimeout 分片重组超时时间
	DefaultFragmentTimeout = 5 * time.Second

	// MaxFragmentCount 最大分片数量限制（防止 DoS 攻击）
	// 65535 分片 * 1400 字节 ≈ 91 MB，合理的消息大小上限
	MaxFragmentCount = 65535
)

// UDPTransport UDP 传输实现
//
// 实现了基于 UDP 的网络传输层，支持：
//   - 大消息自动分片/重组
//   - CRC32 校验和
//   - 单播（点对点发送）
//   - 优雅关闭
type UDPTransport struct {
	// 配置
	config      *TransportConfig
	codec       Codec
	localNodeID uint64

	// UDP 连接
	conn *net.UDPConn

	// 分片相关
	fragmentBuf  *fragmentBuffer // 分片缓冲区（用于大消息重组）
	msgIDCounter uint64          // 消息 ID 计数器（单调递增）

	// Codec 缓存（优化分片重组性能，避免重复创建）
	codecCache   map[uint16]Codec // codecID -> Codec 实例缓存
	codecCacheMu sync.RWMutex     // 保护 codecCache 的并发访问

	// 错误统计（用于监控和调试）
	parseErrorCount    atomic.Uint64 // 解析错误计数
	crcErrorCount      atomic.Uint64 // CRC32 校验失败计数
	fragmentErrorCount atomic.Uint64 // 分片错误计数
	channelBlockCount  atomic.Uint64 // 接收通道阻塞计数

	// 接收通道
	recvCh   chan Message
	recvOnce sync.Once

	// 生命周期
	started  atomic.Bool
	stopped  atomic.Bool
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// fragmentKey 分片标识符（使用 NodeID + MsgID 组合）
type fragmentKey struct {
	nodeID uint64
	msgID  uint64
}

// fragmentBuffer 分片缓冲区
type fragmentBuffer struct {
	mu        sync.RWMutex
	buffers   map[fragmentKey]*partialMessage // (NodeID, MsgSeq) -> partialMessage
	timeout   time.Duration
	stopCh    chan struct{}
	cleanupWg sync.WaitGroup
}

// partialMessage 部分消息
type partialMessage struct {
	total      uint16
	received   uint16
	fragments  [][]byte
	lastUpdate time.Time
	msgType    MessageType // 保存消息类型
	codecID    uint16      // 保存编解码器ID
}

// NewUDPTransport 创建 UDP 传输
//
// 使用默认配置
func NewUDPTransport(listenAddr string) (*UDPTransport, error) {
	return NewUDPTransportWithConfig(&TransportConfig{
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

// NewUDPTransportWithConfig 创建 UDP 传输（自定义配置）
func NewUDPTransportWithConfig(config *TransportConfig) (*UDPTransport, error) {
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

	t := &UDPTransport{
		config:      config,
		codec:       codec,
		localNodeID: 0, // 需要从配置或外部设置
		recvCh:      make(chan Message, config.BufferSize),
		stopCh:      make(chan struct{}),
		codecCache:  make(map[uint16]Codec), // 初始化 Codec 缓存
	}

	return t, nil
}

// SetLocalNodeID 设置本地节点 ID
func (t *UDPTransport) SetLocalNodeID(nodeID uint64) {
	t.localNodeID = nodeID
}

// Start 启动传输层
//
// 启动 UDP 监听器和接收协程
func (t *UDPTransport) Start() error {
	if !t.started.CompareAndSwap(false, true) {
		return types.NewTransportStateError("已经启动")
	}

	logging.Infof("启动 UDP 传输层，监听地址: %s", t.config.ListenAddr)

	// 监听 UDP 端口
	addr, err := net.ResolveUDPAddr("udp", t.config.ListenAddr)
	if err != nil {
		t.started.Store(false)
		return types.NewTransportConnectionError("解析地址", t.config.ListenAddr, err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.started.Store(false)
		return types.NewTransportConnectionError("监听", t.config.ListenAddr, err)
	}

	t.conn = conn

	// 启动接收协程
	t.wg.Add(1)
	go t.receiveLoop()

	// 启动分片缓冲区清理
	t.initFragmentBuffer()

	logging.Infof("UDP 传输层启动成功，监听地址: %s", t.config.ListenAddr)
	return nil
}

// initFragmentBuffer 初始化分片缓冲区
func (t *UDPTransport) initFragmentBuffer() {
	t.fragmentBuf = &fragmentBuffer{
		buffers: make(map[fragmentKey]*partialMessage),
		timeout: DefaultFragmentTimeout,
		stopCh:  make(chan struct{}),
	}
	t.fragmentBuf.startCleanup()
}

// receiveLoop 接收循环
func (t *UDPTransport) receiveLoop() {
	defer t.wg.Done()

	buf := make([]byte, t.config.MaxMessageSize)

	logging.Info("开始接收 UDP 数据...")

	for {
		select {
		case <-t.stopCh:
			logging.Info("UDP 传输层已停止（收到停止信号）")
			return
		default:
			// 设置读取超时
			if err := t.conn.SetReadDeadline(time.Now().Add(t.config.ReadTimeout)); err != nil {
				logging.Errorf("设置读超时失败: %v", err)
				return
			}

			n, addr, err := t.conn.ReadFromUDP(buf)
			if err != nil {
				if errors.Is(err, io.EOF) {
					logging.Info("UDP 连接已关闭")
					return
				}
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// 读超时，继续循环
					continue
				}
				logging.Errorf("读取 UDP 数据失败: %v", err)
				continue
			}

			// 处理接收到的数据
			data := make([]byte, n)
			copy(data, buf[:n])

			msg := t.processReceivedData(data)
			if msg != nil {
				t.sendToReceiveChannel(msg, addr.String())
			}
		}
	}
}

// sendToReceiveChannel 发送消息到接收通道（带超时和阻塞统计）
func (t *UDPTransport) sendToReceiveChannel(msg Message, fromAddr string) {
	channelTimeout := 5 * time.Second
	if t.config != nil && t.config.ChannelSendTimeout > 0 {
		channelTimeout = t.config.ChannelSendTimeout
	}

	select {
	case t.recvCh <- msg:
		logging.Debugf("接收消息: %s from %s", msg.Type(), fromAddr)
	case <-t.stopCh:
		return
	case <-time.After(channelTimeout):
		t.channelBlockCount.Add(1)
		logging.Errorf("接收通道阻塞超过 %v，消息丢弃", channelTimeout)
	}
}

// processReceivedData 处理接收到的数据（分片重组）
func (t *UDPTransport) processReceivedData(data []byte) Message {
	// 解析 TLV Frame
	frame, err := t.parseFrame(data)
	if err != nil {
		t.parseErrorCount.Add(1)
		logging.Warnf("解析帧失败: %v", err)
		return nil
	}

	// 检查是否有分片扩展字段
	fragmentField := frame.VarExtHeader.GetField(ExtFragment)
	if fragmentField == nil {
		// 无分片扩展，直接解码消息
		return t.decodeMessage(frame)
	}

	// 有分片扩展，进行分片重组
	return t.processFragmentFrame(frame)
}

// processFragmentFrame 处理带分片扩展的帧
func (t *UDPTransport) processFragmentFrame(frame *Frame) Message {
	// 解析分片扩展字段
	fragmentField := frame.VarExtHeader.GetField(ExtFragment)
	if fragmentField == nil {
		return t.decodeMessage(frame)
	}

	index, total, err := DecodeFragmentExt(fragmentField)
	if err != nil {
		t.fragmentErrorCount.Add(1)
		logging.Warnf("解析分片扩展失败: %v", err)
		return nil
	}

	// 验证分片数量（防止 DoS 攻击）
	if total == 0 || int(total) > MaxFragmentCount {
		t.fragmentErrorCount.Add(1)
		logging.Warnf("分片数量异常: total=%d, max=%d", total, MaxFragmentCount)
		return nil
	}

	// 使用 Frame 的 NodeID 和 MsgSeq 作为分片标识
	nodeID := frame.FixedHeader.NodeID
	msgID := uint64(frame.FixedHeader.MsgSeq)
	msgType := frame.FixedHeader.MsgType
	codecID := frame.FixedHeader.CodecID

	// 存储分片并检查是否完整
	key := fragmentKey{nodeID: nodeID, msgID: msgID}
	msg := t.fragmentBuf.addFragment(key, total, index, frame.Data, msgType, codecID, t.getCodec)
	if msg == nil {
		t.fragmentErrorCount.Add(1)
	}
	return msg
}

// decodeMessage 从帧中解码消息
func (t *UDPTransport) decodeMessage(frame *Frame) Message {
	msg, err := t.codec.Decode(frame.FixedHeader.MsgType, frame.Data)
	if err != nil {
		t.parseErrorCount.Add(1)
		logging.Warnf("解码消息失败: %v", err)
		return nil
	}
	return msg
}

// parseFrame 解析帧
func (t *UDPTransport) parseFrame(data []byte) (*Frame, error) {
	frame := &Frame{}
	if err := frame.Unmarshal(data); err != nil {
		return nil, err
	}
	// Frame.Unmarshal 已包含 CRC32 验证
	return frame, nil
}

// getCodec 获取 Codec（使用缓存优化性能）
func (t *UDPTransport) getCodec(codecID uint16) (Codec, error) {
	t.codecCacheMu.RLock()
	codec, exists := t.codecCache[codecID]
	t.codecCacheMu.RUnlock()

	if exists {
		return codec, nil
	}

	// 缓存未命中，创建新的 Codec
	t.codecCacheMu.Lock()
	defer t.codecCacheMu.Unlock()

	// 双重检查，避免重复创建
	if codec, exists := t.codecCache[codecID]; exists {
		return codec, nil
	}

	codec, err := NewCodec(types.CodecType(codecID))
	if err != nil {
		return nil, err
	}

	t.codecCache[codecID] = codec
	return codec, nil
}

// addFragment 添加分片并检查是否完整
// codecGetter: 用于获取 Codec 的函数（支持缓存优化）
func (b *fragmentBuffer) addFragment(key fragmentKey, total, index uint16, data []byte, msgType MessageType, codecID uint16, codecGetter func(uint16) (Codec, error)) Message {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 获取或创建 partialMessage
	partial, exists := b.buffers[key]
	if !exists {
		partial = &partialMessage{
			total:      total,
			received:   0,
			fragments:  make([][]byte, total),
			lastUpdate: time.Now(),
			msgType:    msgType,
			codecID:    codecID,
		}
		b.buffers[key] = partial
	}

	// 验证分片索引是否有效
	if int(index) >= int(total) {
		logging.Warnf("分片索引越界: index=%d, total=%d", index, total)
		return nil
	}

	// 检查并存储分片（防止重复分片）
	if partial.fragments[index] != nil {
		logging.Debugf("重复分片: nodeID=%d, msgID=%d, index=%d", key.nodeID, key.msgID, index)
		return nil
	}
	partial.fragments[index] = data
	partial.received++
	partial.lastUpdate = time.Now()

	// 检查是否收齐所有分片
	if partial.received == partial.total {
		// 重组消息（得到完整的编码后的消息数据）
		reassembled := b.reassembleMessage(partial)

		// 删除缓冲区
		delete(b.buffers, key)

		// 使用 codecGetter 获取 Codec（支持缓存优化）
		codec, err := codecGetter(partial.codecID)
		if err != nil {
			logging.Warnf("创建编解码器失败: %v", err)
			return nil
		}

		msg, err := codec.Decode(partial.msgType, reassembled)
		if err != nil {
			logging.Warnf("解码重组消息失败: %v", err)
			return nil
		}

		logging.Debugf("分片重组成功: nodeID=%d, msgID=%d, total=%d", key.nodeID, key.msgID, total)
		return msg
	}

	return nil
}

// reassembleMessage 重组完整消息
func (b *fragmentBuffer) reassembleMessage(partial *partialMessage) []byte {
	// 计算总长度
	totalLen := 0
	for _, frag := range partial.fragments {
		totalLen += len(frag)
	}

	// 合并所有分片
	reassembled := make([]byte, 0, totalLen)
	for _, frag := range partial.fragments {
		reassembled = append(reassembled, frag...)
	}

	return reassembled
}

// startCleanup 启动超时清理协程
func (b *fragmentBuffer) startCleanup() {
	b.cleanupWg.Add(1)
	go func() {
		defer b.cleanupWg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				b.cleanupExpiredFragments()
			case <-b.stopCh:
				return
			}
		}
	}()
}

// cleanupExpiredFragments 清理超时的分片
func (b *fragmentBuffer) cleanupExpiredFragments() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	expiredCount := 0

	for key, partial := range b.buffers {
		if now.Sub(partial.lastUpdate) > b.timeout {
			// 超时，丢弃未收齐的分片
			delete(b.buffers, key)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		logging.Debugf("清理超时分片: %d 个", expiredCount)
	}
}

// Stop 停止传输层
//
// 优雅关闭 UDP 连接和分片缓冲区
func (t *UDPTransport) Stop() error {
	if !t.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	t.stopOnce.Do(func() {
		logging.Info("停止 UDP 传输层...")

		// 关闭停止信号，通知所有协程退出
		close(t.stopCh)

		// 关闭分片缓冲区
		if t.fragmentBuf != nil {
			close(t.fragmentBuf.stopCh)
			t.fragmentBuf.cleanupWg.Wait()
		}

		// 关闭 UDP 连接
		if t.conn != nil {
			_ = t.conn.Close()
		}

		// 等待接收协程退出
		t.wg.Wait()

		// 关闭接收通道
		t.recvOnce.Do(func() {
			close(t.recvCh)
		})

		logging.Info("UDP 传输层已停止")
	})

	return nil
}

// Send 发送消息到指定节点（支持分片）
func (t *UDPTransport) Send(ctx context.Context, addr string, msg Message) error {
	if !t.started.Load() {
		return types.NewTransportStateError("未启动")
	}

	// 编码消息
	msgData, err := t.codec.Encode(msg)
	if err != nil {
		return types.NewTransportSendError(err)
	}

	// 解析目标地址
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return types.NewTransportConnectionError("解析地址", addr, err)
	}

	// 小消息直接发送（无需分片）
	if len(msgData) <= MaxUDPPacketSize {
		return t.sendDirect(udpAddr, msgData, msg.Type(), addr)
	}

	// 大消息分片发送（直接对编码后的消息数据进行分片）
	return t.sendFragmented(udpAddr, msgData, msg.Type())
}

// sendDirect 直接发送消息（无需分片）
func (t *UDPTransport) sendDirect(addr *net.UDPAddr, msgData []byte, msgType MessageType, originalAddr string) error {
	// 创建完整帧
	frame := NewFrame(0, 0, msgType, uint16(t.codec.Type()), msgData)
	frameData, err := frame.Marshal()
	if err != nil {
		return types.NewTransportSendError(err)
	}

	if err := t.conn.SetWriteDeadline(time.Now().Add(t.config.WriteTimeout)); err != nil {
		return types.NewTransportConnectionError("设置写超时", "", err)
	}

	_, err = t.conn.WriteToUDP(frameData, addr)
	if err != nil {
		return types.NewTransportSendError(err)
	}

	logging.Debugf("发送消息: %s to %s", msgType, originalAddr)
	return nil
}

// sendFragmented 分片发送大消息
//
// 接收编码后的纯消息数据，对每个分片创建独立的 Frame
func (t *UDPTransport) sendFragmented(addr *net.UDPAddr, msgData []byte, msgType MessageType) error {
	// 验证 localNodeID 已设置
	if t.localNodeID == 0 {
		return types.NewTransportStateError("localNodeID 未设置，必须调用 SetLocalNodeID")
	}

	nodeID := t.localNodeID
	msgID := t.nextMessageID()
	totalFragments := (len(msgData) + MaxUDPPacketSize - 1) / MaxUDPPacketSize

	logging.Debugf("分片发送: total=%d, msgID=%d, nodeID=%d", totalFragments, msgID, nodeID)

	// 设置写超时
	if err := t.conn.SetWriteDeadline(time.Now().Add(t.config.WriteTimeout)); err != nil {
		return types.NewTransportConnectionError("设置写超时", "", err)
	}

	// 发送所有分片
	for i := 0; i < totalFragments; i++ {
		start := i * MaxUDPPacketSize
		end := start + MaxUDPPacketSize
		if end > len(msgData) {
			end = len(msgData)
		}

		fragmentData := msgData[start:end]

		// 每个分片创建独立的 Frame（FixedHeader + Fragment 扩展 + 分片数据 + CRC32）
		frameBytes, err := NewFrame(nodeID, msgID, msgType, uint16(t.codec.Type()), fragmentData).
			WithFragment(uint16(i), uint16(totalFragments)).
			Finalize().
			Marshal()
		if err != nil {
			return types.NewTransportSendError(err)
		}

		if _, err := t.conn.WriteToUDP(frameBytes, addr); err != nil {
			return types.NewTransportSendError(err)
		}
	}

	logging.Debugf("发送消息: 分片完成 to %s", addr.String())
	return nil
}

// nextMessageID 生成递增的消息 ID
func (t *UDPTransport) nextMessageID() uint64 {
	return atomic.AddUint64(&t.msgIDCounter, 1)
}

// Receive 返回接收消息的通道
//
// 调用者需要持续从通道读取消息
func (t *UDPTransport) Receive() <-chan Message {
	return t.recvCh
}

// GetLocalAddr 获取本地地址
func (t *UDPTransport) GetLocalAddr() string {
	if t.conn != nil {
		return t.conn.LocalAddr().String()
	}
	return t.config.ListenAddr
}

// GetConfig 获取配置
func (t *UDPTransport) GetConfig() *TransportConfig {
	return t.config
}

// Stats 获取统计信息
func (t *UDPTransport) Stats() map[string]any {
	stats := make(map[string]any)
	stats["started"] = t.started.Load()
	stats["stopped"] = t.stopped.Load()
	stats["listen_addr"] = t.GetLocalAddr()
	stats["local_node_id"] = t.localNodeID
	stats["msg_id_counter"] = atomic.LoadUint64(&t.msgIDCounter)

	// 分片缓冲区统计
	if t.fragmentBuf != nil {
		t.fragmentBuf.mu.RLock()
		stats["pending_fragments"] = len(t.fragmentBuf.buffers)
		t.fragmentBuf.mu.RUnlock()
	}

	// Codec 缓存统计（性能优化指标）
	t.codecCacheMu.RLock()
	stats["codec_cache_size"] = len(t.codecCache)
	t.codecCacheMu.RUnlock()

	// 错误统计（用于监控和调试）
	stats["parse_errors"] = t.parseErrorCount.Load()
	stats["crc_errors"] = t.crcErrorCount.Load()
	stats["fragment_errors"] = t.fragmentErrorCount.Load()
	stats["channel_blocks"] = t.channelBlockCount.Load()

	return stats
}
