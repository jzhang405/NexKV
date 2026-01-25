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
	"math/big"
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

	// MaxReassembledMessageSize 重组后的消息最大大小限制（防止 DoS 攻击）
	// 100 MB 是合理的消息大小上限，防止内存耗尽
	MaxReassembledMessageSize = 100 * 1024 * 1024

	// MaxCodecCacheSize Codec 缓存最大容量限制（防止 DoS 攻击）
	// 虽然实际只有 3 种有效的 codecID，但设置上限防止恶意数据包导致缓存无限增长
	MaxCodecCacheSize = 16
)

// UDP 统计键名常量
const (
	// 状态统计
	udpStatKeyStarted       = "started"
	udpStatKeyStopped       = "stopped"
	udpStatKeyListenAddr    = "listen_addr"
	udpStatKeyLocalNodeID   = "local_node_id"
	udpStatKeyMsgSeqCounter = "msg_seq_counter"

	// 运行时统计
	udpStatKeyPendingFragments = "pending_fragments"
	udpStatKeyCodecCacheSize   = "codec_cache_size"

	// 错误统计
	udpStatKeyParseErrors      = "parse_errors"
	udpStatKeyCRCErrors        = "crc_errors"
	udpStatKeyFragmentErrors   = "fragment_errors"
	udpStatKeyChannelBlocks    = "channel_blocks"
	udpStatKeyFragmentTimeouts = "fragment_timeouts" // PR-020：超时统计
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
	config *TransportConfig
	codec  Codec
	NodeID atomic.Uint64

	// 节点标识
	msgSeqGenerator   atomic.Value  // 存储 func() uint64
	defaultSeqCounter atomic.Uint64 // 默认序列号计数器

	// UDP 连接
	conn *net.UDPConn

	// 分片相关
	fragmentBuf *fragmentBuffer // 分片缓冲区（用于大消息重组）

	// Codec 缓存（优化分片重组性能，避免重复创建）
	codecCache   map[uint16]Codec // codecID -> Codec 实例缓存
	codecCacheMu sync.RWMutex     // 保护 codecCache 的并发访问

	// 错误统计（用于监控和调试）
	parseErrorCount    atomic.Uint64 // 解析错误计数
	crcErrorCount      atomic.Uint64 // CRC32 校验失败计数
	fragmentErrorCount atomic.Uint64 // 分片错误计数
	channelBlockCount  atomic.Uint64 // 接收通道阻塞计数

	// 接收通道（返回增强消息 MsgFrame，包含原始消息和 TLV 扩展字段）
	recvCh   chan MsgFrame
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
	mu           sync.RWMutex
	buffers      map[fragmentKey]*partialMessage // (NodeID, MsgSeq) -> partialMessage
	timeout      time.Duration
	timeoutCount atomic.Uint64 // PR-020：超时清理统计
	stopCh       chan struct{}
	cleanupWg    sync.WaitGroup
}

// partialMessage 部分消息（使用位图跟踪分片接收状态）
type partialMessage struct {
	total      uint16
	received   uint16
	fragments  [][]byte
	lastUpdate time.Time
	msgType    MessageType // 保存消息类型
	codecID    uint16      // 保存编解码器ID

	// 位图跟踪（PR-020 优化）
	// 快速路径：total <= 64 时使用 uint64 位图
	bitmapFast uint64
	// 慢速路径：total > 64 时使用 big.Int 位图（需并发保护）
	bitmap *big.Int

	// 并发保护：bitmap 访问需要 mutex 保护（big.Int 非并发安全）
	bitmapMu sync.RWMutex
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
		config: config,
		codec:  codec,
		// NodeID 需要从外部设置（atomic.Uint64 默认零值）
		recvCh:     make(chan MsgFrame, config.BufferSize),
		stopCh:     make(chan struct{}),
		codecCache: make(map[uint16]Codec), // 初始化 Codec 缓存
	}

	return t, nil
}

// Start 启动传输层
//
// 参数：
//   - nodeID: 节点 ID（全局唯一，可选，用于消息去重和幂等性）
//   - msgSeqGenerator: 消息序列号生成器（必需，单调递增）
func (t *UDPTransport) Start(nodeID *uint64, msgSeqGenerator func() uint64) error {
	if !t.started.CompareAndSwap(false, true) {
		return types.NewTransportStateError("已经启动")
	}

	// 验证必需参数
	if msgSeqGenerator == nil {
		return types.NewStoreInvalidParameterError("msgSeqGenerator 不能为空")
	}

	// 设置节点 ID
	if nodeID != nil {
		t.NodeID.Store(*nodeID)
	}

	// 设置消息序列号生成器
	t.msgSeqGenerator.Store(msgSeqGenerator)

	logging.Infof("启动 UDP 传输层，监听地址: %s, NodeID: %d", t.config.ListenAddr, t.NodeID.Load())

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
		// 每次循环都设置读超时，确保每次读取都有超时保护
		if err := setReadTimeout(t.conn, t.config.ReadTimeout); err != nil {
			logging.Errorf("设置读超时失败: %v", err)
			return
		}

		select {
		case <-t.stopCh:
			logging.Info("UDP 传输层已停止（收到停止信号）")
			return
		default:
			n, addr, err := t.conn.ReadFromUDP(buf)
			if err != nil {
				if errors.Is(err, io.EOF) {
					logging.Info("UDP 连接已关闭")
					return
				}
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// 读超时，继续循环（下次循环会重新设置超时）
					continue
				}
				logging.Errorf("读取 UDP 数据失败: %v", err)
				continue
			}

			// 处理接收到的数据
			data := make([]byte, n)
			copy(data, buf[:n])

			msg := t.processReceivedData(data)
			if msg.Message != nil {
				t.sendToReceiveChannel(msg, addr.String())
			}
		}
	}
}

// sendToReceiveChannel 发送消息到接收通道（带超时和阻塞统计）
func (t *UDPTransport) sendToReceiveChannel(msg MsgFrame, fromAddr string) {
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
func (t *UDPTransport) processReceivedData(data []byte) MsgFrame {
	// 解析 TLV Frame
	frame, err := t.parseFrame(data)
	if err != nil {
		t.parseErrorCount.Add(1)
		logging.Warnf("解析帧失败: %v", err)
		return MsgFrame{}
	}

	// 空指针检查（P2-2）
	if frame.FixedHeader == nil {
		t.parseErrorCount.Add(1)
		logging.Warnf("帧固定头为空")
		return MsgFrame{}
	}

	// 检查是否有分片扩展字段
	if frame.VarExtHeader.GetField(ExtFragment) == nil {
		// 无分片扩展，直接解码消息
		return t.decodeMessage(frame)
	}

	// 有分片扩展，进行分片重组
	return t.processFragmentFrame(frame)
}

// processFragmentFrame 处理带分片扩展的帧
func (t *UDPTransport) processFragmentFrame(frame *Frame) MsgFrame {
	// 解析分片扩展字段
	fragmentField := frame.VarExtHeader.GetField(ExtFragment)
	if fragmentField == nil {
		return t.decodeMessage(frame)
	}

	index, total, err := DecodeFragmentExt(fragmentField)
	if err != nil {
		t.fragmentErrorCount.Add(1)
		logging.Warnf("解析分片扩展失败: %v", err)
		return MsgFrame{}
	}

	// 验证分片数量（防止 DoS 攻击）
	if total == 0 || int(total) > MaxFragmentCount {
		t.fragmentErrorCount.Add(1)
		logging.Warnf("分片数量异常: total=%d, max=%d", total, MaxFragmentCount)
		return MsgFrame{}
	}

	// 使用 Frame 的 NodeID 和 MsgSeq 作为分片标识
	key := fragmentKey{
		nodeID: frame.FixedHeader.NodeID,
		msgID:  uint64(frame.FixedHeader.MsgSeq),
	}

	// 存储分片并检查是否完整
	msgExt := t.fragmentBuf.addFragment(
		key,
		total,
		index,
		frame.Data,
		frame.FixedHeader.MsgType,
		frame.FixedHeader.CodecID,
		t.getCodec,
	)
	// 检查返回的 MsgFrame 是否有效
	if msgExt.Message == nil {
		t.fragmentErrorCount.Add(1)
	}
	return msgExt
}

// buildMsgFrame 从原始消息和 TLV 扩展头构建增强消息 MsgFrame
//
// 参数：
//   - msg: 原始消息
//   - extHeader: TLV 扩展头
//
// 返回：
//   - MsgFrame: 增强消息（包含原始消息和解析后的 TLV 字段）
func (t *UDPTransport) buildMsgFrame(msg Message, extHeader *VarExtHeader) MsgFrame {
	msgFrame := MsgFrame{
		Message: msg,
		TLVs:    make([]ExtField, 0, len(extHeader.Fields)),
	}

	// 遍历所有 TLV 字段并添加到 MsgFrame
	// 注意：字段不会立即解析，而是在首次访问 GetExt() 时才解码
	for _, field := range extHeader.Fields {
		msgFrame.TLVs = append(msgFrame.TLVs, *field)
	}

	return msgFrame
}

// decodeMessage 从帧中解码消息，返回增强消息 MsgFrame
func (t *UDPTransport) decodeMessage(frame *Frame) MsgFrame {
	msg, err := t.codec.Decode(frame.FixedHeader.MsgType, frame.Data)
	if err != nil {
		t.parseErrorCount.Add(1)
		logging.Warnf("解码消息失败: %v", err)
		return MsgFrame{}
	}
	// 构建 MsgFrame（解析 TLV 扩展字段）
	return t.buildMsgFrame(msg, frame.VarExtHeader)
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

	// 检查缓存大小（防止 DoS 攻击）
	if len(t.codecCache) >= MaxCodecCacheSize {
		return nil, types.NewOpErr(types.ErrCodeInternal, "getCodec",
			"Codec 缓存已满，可能受到 DoS 攻击", nil)
	}

	codec, err := NewCodec(types.CodecType(codecID))
	if err != nil {
		return nil, err
	}

	t.codecCache[codecID] = codec
	return codec, nil
}

// newPartialMessage 创建新的部分消息（PR-020：自动选择快速/慢速路径）
//
// 路径选择策略：
//   - total <= 64：快速路径（uint64 位图）
//   - total > 64：慢速路径（big.Int 位图）
func newPartialMessage(total uint16, msgType MessageType, codecID uint16) *partialMessage {
	pm := &partialMessage{
		total:      total,
		received:   0,
		fragments:  make([][]byte, total),
		lastUpdate: time.Now(),
		msgType:    msgType,
		codecID:    codecID,
		bitmapFast: 0, // 快速路径默认零值
	}

	// 路径选择：total > 64 触发慢速路径
	if total > 64 {
		pm.bitmap = new(big.Int)
	}
	// total <= 64 使用快速路径（bitmapFast，默认 0）

	return pm
}

// isComplete 检查是否收齐所有分片（PR-020：快速/慢速路径优化）
//
// 快速路径（total <= 64）：
//   - 使用 uint64 位图
//   - 目标：< 50ns
//
// 慢速路径（total > 64）：
//   - 使用 big.Int 位图（需 mutex 保护）
//   - 目标：< 1μs（total=100）
func (p *partialMessage) isComplete() bool {
	// 快速路径：total <= 64（无锁）
	if p.total <= 64 {
		// 检查 bitmapFast 的低 total 位是否全为 1
		// 使用位掩码：((1 << total) - 1)
		// 特殊处理：total=64 时，uint64(1)<<64 会溢出为 0
		if p.total == 64 {
			return p.bitmapFast == 0xFFFFFFFFFFFFFFFF
		}
		mask := uint64(1)<<p.total - 1
		return p.bitmapFast == mask
	}

	// 慢速路径：total > 64（需锁保护）
	p.bitmapMu.RLock()
	defer p.bitmapMu.RUnlock()

	if p.bitmap == nil {
		return false
	}

	// 检查位图的低 total 位是否全为 1
	// big.Int.Cmp 比较位图与预期值
	expected := new(big.Int)
	expected.Lsh(big.NewInt(1), uint(p.total)) // 1 << total
	expected.Sub(expected, big.NewInt(1))      // (1 << total) - 1

	return p.bitmap.Cmp(expected) == 0
}

// getMissingIndexes 获取缺失分片索引列表（PR-020：用于超时清理日志）
//
// 注意：此方法仅供超时清理时调用，非热路径，性能要求不高
func (p *partialMessage) getMissingIndexes() []uint16 {
	missing := make([]uint16, 0)

	// 快速路径：total <= 64
	if p.total <= 64 {
		for i := uint16(0); i < p.total; i++ {
			if p.bitmapFast&(1<<i) == 0 {
				missing = append(missing, i)
			}
		}
		return missing
	}

	// 慢速路径：total > 64（需锁保护）
	p.bitmapMu.RLock()
	defer p.bitmapMu.RUnlock()

	if p.bitmap == nil {
		// 未初始化，返回全部索引
		for i := uint16(0); i < p.total; i++ {
			missing = append(missing, i)
		}
		return missing
	}

	for i := uint16(0); i < p.total; i++ {
		if p.bitmap.Bit(int(i)) == 0 {
			missing = append(missing, i)
		}
	}

	return missing
}

// addFragment 添加分片并检查是否完整（PR-020：使用位图跟踪）
// codecGetter: 用于获取 Codec 的函数（支持缓存优化）
// 返回 MsgFrame（增强消息），分片重组后的消息不包含原始 TLV 扩展字段（因分片传输中 TLV 信息丢失）
func (b *fragmentBuffer) addFragment(key fragmentKey, total, index uint16, data []byte, msgType MessageType, codecID uint16, codecGetter func(uint16) (Codec, error)) MsgFrame {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 获取或创建 partialMessage
	partial, exists := b.buffers[key]
	if !exists {
		// PR-020：使用 newPartialMessage 初始化（自动选择快速/慢速路径）
		partial = newPartialMessage(total, msgType, codecID)
		b.buffers[key] = partial
	}

	// 验证分片索引是否有效
	if int(index) >= int(total) {
		logging.Warnf("分片索引越界: index=%d, total=%d", index, total)
		return MsgFrame{}
	}

	// 验证重组后的消息大小（防止 DoS 攻击）
	if int(total) > 0 {
		estimatedSize := int(total) * MaxUDPPacketSize
		if estimatedSize > MaxReassembledMessageSize {
			logging.Warnf("拒绝过大的分片消息: nodeID=%d, msgID=%d, total=%d, estimatedSize=%d, max=%d",
				key.nodeID, key.msgID, total, estimatedSize, MaxReassembledMessageSize)
			return MsgFrame{}
		}
	}

	// 检查并存储分片（防止重复分片）
	if partial.fragments[index] != nil {
		logging.Debugf("重复分片: nodeID=%d, msgID=%d, index=%d", key.nodeID, key.msgID, index)
		return MsgFrame{}
	}
	partial.fragments[index] = data
	partial.received++
	partial.lastUpdate = time.Now()

	// PR-020：使用位图跟踪分片接收状态
	// 快速路径：total <= 64（无锁）
	if total <= 64 {
		partial.bitmapFast |= (1 << index)
	} else {
		// 慢速路径：total > 64（需锁保护）
		partial.bitmapMu.Lock()
		partial.bitmap.SetBit(partial.bitmap, int(index), 1)
		partial.bitmapMu.Unlock()
	}

	// PR-020：使用位图检查是否收齐所有分片
	if partial.isComplete() {
		// 重组消息（得到完整的编码后的消息数据）
		reassembled := b.reassembleMessage(partial)

		// 删除缓冲区
		delete(b.buffers, key)

		// 使用 codecGetter 获取 Codec（支持缓存优化）
		codec, err := codecGetter(partial.codecID)
		if err != nil {
			logging.Warnf("创建编解码器失败: %v", err)
			return MsgFrame{}
		}

		msg, err := codec.Decode(partial.msgType, reassembled)
		if err != nil {
			logging.Warnf("解码重组消息失败: %v", err)
			return MsgFrame{}
		}

		logging.Debugf("分片重组成功: nodeID=%d, msgID=%d, total=%d", key.nodeID, key.msgID, total)
		// 返回 MsgFrame（分片重组后的消息不包含原始 TLV 扩展字段）
		return MsgFrame{
			Message: msg,
			TLVs:    make([]ExtField, 0),
		}
	}

	return MsgFrame{}
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

// cleanupExpiredFragments 清理超时的分片（PR-020：增强日志和监控）
func (b *fragmentBuffer) cleanupExpiredFragments() {
	// PR-020：快照遍历优化（减少全局锁持有时间）

	// 1. 读锁复制 keys
	b.mu.RLock()
	keys := make([]fragmentKey, 0, len(b.buffers))
	for k := range b.buffers {
		keys = append(keys, k)
	}
	b.mu.RUnlock()

	// 2. 写锁删除超时项
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	expiredCount := 0

	for _, k := range keys {
		partial, exists := b.buffers[k]
		if !exists {
			continue // 可能已被其他 goroutine 删除
		}

		if now.Sub(partial.lastUpdate) > b.timeout {
			// PR-020：结构化日志（记录关键信息）
			missing := partial.getMissingIndexes()
			logging.Warnf("分片重组超时 msgID=%d received=%d total=%d duration=%v missing=%v",
				k.msgID,
				partial.received,
				partial.total,
				now.Sub(partial.lastUpdate),
				missing,
			)

			delete(b.buffers, k)
			expiredCount++
			b.timeoutCount.Add(1) // PR-020：更新超时统计
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

// Send 发送消息到指定节点（支持分片和函数选项）
//
// 支持函数选项模式，可动态配置 TLV 扩展字段：
//
//	transport.Send(ctx, addr, msg, WithHopCount(10))
//	transport.Send(ctx, addr, msg, WithCompression(2), WithHopCount(5))
func (t *UDPTransport) Send(ctx context.Context, addr string, msg Message, opts ...SendOpt) error {
	if !t.started.Load() {
		return types.NewTransportStateError("未启动")
	}

	// 处理发送选项
	options := processSendOptions(opts...)
	defer releaseSendOptions(options)

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

	// 计算消息 Flags（根据 ExpectResponse）
	flags := FlagsFromMessage(msg)

	// 小消息直接发送（无需分片）
	if len(msgData) <= MaxUDPPacketSize {
		return t.sendDirectWithOptions(udpAddr, msgData, msg.Type(), addr, t.GenerateMsgSeq(), flags, options)
	}

	// 大消息分片发送（直接对编码后的消息数据进行分片）
	return t.sendFragmentedWithOptions(udpAddr, msgData, msg.Type(), t.GenerateMsgSeq(), flags, options)
}

// sendDirectWithOptions 直接发送消息（带 TLV 选项，无需分片）
func (t *UDPTransport) sendDirectWithOptions(addr *net.UDPAddr, msgData []byte, msgType MessageType, originalAddr string, msgSeq uint64, flags uint8, opts *sendOptions) error {
	nodeID := t.NodeID.Load()
	msgID := msgSeq

	// 创建完整帧（使用 Flags 字段）
	frame := NewFrame(nodeID, msgID, msgType, uint16(t.codec.Type()), flags, msgData)

	// 应用 TLV 扩展字段
	if opts.hopCount != nil {
		// 设置 Hops 字段（FixedHeader）
		frame.FixedHeader.Hops = uint8(*opts.hopCount)
	}
	if opts.compressID != nil {
		frame.WithCompress(*opts.compressID)
	}

	// 完成构建并计算 CRC32
	frame.Finalize()

	// 序列化发送
	frameData, err := frame.Marshal()
	if err != nil {
		return types.NewTransportSendError(err)
	}

	if err := setWriteTimeout(t.conn, t.config.WriteTimeout); err != nil {
		return types.NewTransportConnectionError("设置写超时", "", err)
	}

	_, err = t.conn.WriteToUDP(frameData, addr)
	if err != nil {
		return types.NewTransportSendError(err)
	}

	logging.Debugf("发送消息: %s to %s", msgType, originalAddr)
	return nil
}

// sendFragmentedWithOptions 分片发送大消息（带 TLV 选项）
//
// 接收编码后的纯消息数据，对每个分片创建独立的 Frame
func (t *UDPTransport) sendFragmentedWithOptions(addr *net.UDPAddr, msgData []byte, msgType MessageType, msgSeq uint64, flags uint8, opts *sendOptions) error {
	// 验证 localNodeID 已设置
	if t.NodeID.Load() == 0 {
		return types.NewTransportStateError("localNodeID 未设置")
	}

	nodeID := t.NodeID.Load()
	msgID := msgSeq
	totalFragments := (len(msgData) + MaxUDPPacketSize - 1) / MaxUDPPacketSize

	logging.Debugf("分片发送: total=%d, msgID=%d, nodeID=%d", totalFragments, msgID, nodeID)

	// 设置写超时
	if err := setWriteTimeout(t.conn, t.config.WriteTimeout); err != nil {
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

		// 创建 Frame（FixedHeader + Fragment 扩展 + TLV 扩展 + 分片数据 + CRC32）
		frame := NewFrame(nodeID, msgID, msgType, uint16(t.codec.Type()), flags, fragmentData)
		frame.WithFragment(uint16(i), uint16(totalFragments))

		// 应用 TLV 扩展字段（注意：分片消息通常不需要 Hops）
		if opts.hopCount != nil {
			// 设置 Hops 字段（FixedHeader）
			frame.FixedHeader.Hops = uint8(*opts.hopCount)
		}
		if opts.compressID != nil {
			frame.WithCompress(*opts.compressID)
		}

		frame.Finalize()

		frameBytes, err := frame.Marshal()
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

// ForwardMessage 转发消息到指定节点
// 自动递减 Hop Count（如果存在），Hop Count 减至 0 时返回错误
func (t *UDPTransport) ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
	if !t.started.Load() {
		return 0, types.NewTransportStateError("未启动")
	}

	select {
	case <-ctx.Done():
		return 0, types.NewTransportSendError(ctx.Err())
	default:
	}

	forwardMsg, err := prepareForwardMessage(&msgExt)
	if err != nil {
		return 0, err
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return 0, types.NewTransportConnectionError("解析地址", addr, err)
	}

	msgData, err := t.codec.Encode(forwardMsg.Message)
	if err != nil {
		return 0, types.NewTransportSendError(err)
	}

	tlvFields, err := forwardMsg.EncodeTLVs()
	if err != nil {
		return 0, types.NewOpErr(types.ErrCodecEncodeFailed, "ForwardMessage",
			"编码 TLV 失败", err)
	}

	frameOverhead := 74
	maxPayloadSize := MaxUDPPacketSize - frameOverhead
	msgSeq := t.GenerateMsgSeq()
	nodeID := t.NodeID.Load()

	if len(msgData) <= maxPayloadSize {
		return t.forwardDirect(nodeID, msgSeq, *forwardMsg, msgData, tlvFields, udpAddr, addr)
	}

	return t.forwardFragmented(nodeID, msgSeq, *forwardMsg, msgData, tlvFields, maxPayloadSize, udpAddr, addr)
}

// forwardDirect 直接转发消息（无需分片）
func (t *UDPTransport) forwardDirect(nodeID uint64, msgSeq uint64, forwardMsg MsgFrame, msgData []byte, tlvFields []ExtField, udpAddr *net.UDPAddr, originalAddr string) (uint64, error) {
	// 获取当前跳数，检查是否可以转发
	currentHops, hasHops := forwardMsg.GetHopCount()
	if hasHops && currentHops == 0 {
		return 0, types.NewTransportHopCountExpiredError()
	}

	// 计算新的跳数（递减）
	newHops := currentHops
	if newHops > 0 {
		newHops--
	}

	// 转发消息使用单向请求 Flags（Gossip 类型的消息通常是单向的）
	// 同时设置 ForwardNodeID 和 IsForward 标志
	forwardNodeID := t.NodeID.Load() // 转发节点的 ID
	frame := NewForwardFrame(nodeID, msgSeq, forwardMsg.Type(), uint16(t.codec.Type()), FlagsOneWayRequest, forwardNodeID, newHops, msgData)
	frame.AddTLVFields(tlvFields)
	frame.Finalize()

	frameData, err := frame.Marshal()
	if err != nil {
		return 0, types.NewTransportSendError(err)
	}

	if err := t.conn.SetWriteDeadline(time.Now().Add(t.config.WriteTimeout)); err != nil {
		return 0, types.NewTransportConnectionError("设置写超时", "", err)
	}

	if _, err := t.conn.WriteToUDP(frameData, udpAddr); err != nil {
		return 0, types.NewTransportConnectionError("发送", originalAddr, err)
	}

	logging.Debugf("转发消息: %s to %s, Hops=%d→%d, ForwardNodeID=%d", forwardMsg.Type(), originalAddr, currentHops, newHops, forwardNodeID)

	return msgSeq, nil
}

// forwardFragmented 分片转发大消息
func (t *UDPTransport) forwardFragmented(nodeID uint64, msgSeq uint64, forwardMsg MsgFrame, msgData []byte, tlvFields []ExtField, maxPayloadSize int, udpAddr *net.UDPAddr, originalAddr string) (uint64, error) {
	// 获取当前跳数，检查是否可以转发
	currentHops, hasHops := forwardMsg.GetHopCount()
	if hasHops && currentHops == 0 {
		return 0, types.NewTransportHopCountExpiredError()
	}

	// 计算新的跳数（递减）
	newHops := currentHops
	if newHops > 0 {
		newHops--
	}

	totalFragments := (len(msgData) + maxPayloadSize - 1) / maxPayloadSize
	forwardNodeID := t.NodeID.Load() // 转发节点的 ID

	for i := 0; i < totalFragments; i++ {
		start := i * maxPayloadSize
		end := start + maxPayloadSize
		if end > len(msgData) {
			end = len(msgData)
		}

		fragmentData := msgData[start:end]

		// 转发消息使用单向请求 Flags（Gossip 类型的消息通常是单向的）
		// 同时设置 ForwardNodeID 和 IsForward 标志
		frame := NewForwardFrame(nodeID, msgSeq, forwardMsg.Type(), uint16(t.codec.Type()), FlagsOneWayRequest, forwardNodeID, newHops, fragmentData)
		frame.WithFragment(uint16(i), uint16(totalFragments))

		if i == 0 {
			frame.AddTLVFields(tlvFields)
		}

		frame.Finalize()

		frameData, err := frame.Marshal()
		if err != nil {
			return 0, types.NewTransportSendError(err)
		}

		if err := t.conn.SetWriteDeadline(time.Now().Add(t.config.WriteTimeout)); err != nil {
			return 0, types.NewTransportConnectionError("设置写超时", "", err)
		}

		if _, err := t.conn.WriteToUDP(frameData, udpAddr); err != nil {
			return 0, types.NewTransportConnectionError("发送分片", originalAddr, err)
		}
	}

	logging.Debugf("转发消息: 分片完成 to %s, Hops=%d→%d, ForwardNodeID=%d", originalAddr, currentHops, newHops, forwardNodeID)

	return msgSeq, nil
}

// ========================================
// 批量转发实现
// ========================================

// BatchForwardMessage 批量转发消息
//
// UDP 无连接协议，直接并发发送到多个目标地址
func (t *UDPTransport) BatchForwardMessage(
	ctx context.Context,
	addrs []string,
	msgExt MsgFrame,
) BatchForwardMessageResult {
	if !t.started.Load() {
		return createBatchForwardResult(addrs, types.NewTransportStateError("未启动"))
	}

	return executeBatchForward(ctx, addrs, msgExt, t.ForwardMessage)
}

// GetNodeID 获取当前节点 ID
func (t *UDPTransport) GetNodeID() uint64 {
	return t.NodeID.Load()
}

// GenerateMsgSeq 生成下一条消息序列号
func (t *UDPTransport) GenerateMsgSeq() uint64 {
	return generateMsgSeq(t.msgSeqGenerator.Load(), &t.defaultSeqCounter)
}

// Receive 返回接收消息的通道
//
// # Receive 返回接收消息的通道
//
// 返回 MsgFrame（增强消息），包含原始消息和 TLV 扩展字段：
//
//	for msgExt := range transport.Receive() {
//	    if msgExt.HasHopCount() {
//	        fmt.Printf("Hop: %d/%d\n", msgExt.HopCount.Hop, msgExt.HopCount.TotalHop)
//	    }
//	}
func (t *UDPTransport) Receive() <-chan MsgFrame {
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
	stats[udpStatKeyStarted] = t.started.Load()
	stats[udpStatKeyStopped] = t.stopped.Load()
	stats[udpStatKeyListenAddr] = t.GetLocalAddr()
	stats[udpStatKeyLocalNodeID] = t.NodeID.Load()
	stats[udpStatKeyMsgSeqCounter] = t.defaultSeqCounter.Load()

	// 分片缓冲区统计
	if t.fragmentBuf != nil {
		t.fragmentBuf.mu.RLock()
		stats[udpStatKeyPendingFragments] = len(t.fragmentBuf.buffers)
		stats[udpStatKeyFragmentTimeouts] = t.fragmentBuf.timeoutCount.Load() // PR-020：超时统计
		t.fragmentBuf.mu.RUnlock()
	}

	// Codec 缓存统计（性能优化指标）
	t.codecCacheMu.RLock()
	stats[udpStatKeyCodecCacheSize] = len(t.codecCache)
	t.codecCacheMu.RUnlock()

	// 错误统计（用于监控和调试）
	stats[udpStatKeyParseErrors] = t.parseErrorCount.Load()
	stats[udpStatKeyCRCErrors] = t.crcErrorCount.Load()
	stats[udpStatKeyFragmentErrors] = t.fragmentErrorCount.Load()
	stats[udpStatKeyChannelBlocks] = t.channelBlockCount.Load()

	return stats
}
