// Package transport TCP 传输实现
//
// 核心特性:
//   - 自定义帧格式 (13 字节头 + Data)
//   - MessagePack 序列化
//   - 连接池管理
//   - 并发安全
package transport

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// TCP 常量配置
const (
	// DefaultIdleTimeout 默认空闲连接超时时间（2分钟）
	DefaultIdleTimeout = 2 * time.Minute

	// MinAcceptChannelSize accept channel 最小缓冲区大小
	// acceptCh 用于接受连接，不需要太大缓冲区（连接按需处理）
	MinAcceptChannelSize int = 32
)

// TCP 统计键名常量
const (
	// 状态统计
	tcpStatKeyStarted           = "started"
	tcpStatKeyStopped           = "stopped"
	tcpStatKeyListenAddr        = "listen_addr"
	tcpStatKeyActiveConnections = "active_connections"
)

// TCPTransport TCP 传输实现
//
// 实现了基于 TCP 的网络传输层，支持：
//   - 双向通信（服务端 + 客户端）
//   - 连接池复用
//   - 心跳保活
//   - 优雅关闭
type TCPTransport struct {
	// 配置
	config *TransportConfig
	codec  Codec

	// 服务端
	listener   net.Listener
	acceptCh   chan *tcpConn
	acceptDone chan struct{}
	acceptWg   sync.WaitGroup

	// 客户端连接池
	connPool *connPool
	poolWg   sync.WaitGroup
	poolDone chan struct{}

	// 接收通道（返回增强消息 MsgFrame，包含原始消息和 TLV 扩展字段）
	recvCh   chan MsgFrame
	recvOnce sync.Once

	// 生命周期
	started  atomic.Bool
	stopped  atomic.Bool
	stopCh   chan struct{}
	stopOnce sync.Once
	stopWg   sync.WaitGroup

	// 本地节点地址
	localAddr string

	// 节点标识
	NodeID          atomic.Uint64
	msgSeqGenerator atomic.Value // 存储 func() uint64
}

// connPool 连接池
//
// 管理到远端节点的连接复用
type connPool struct {
	mu    sync.RWMutex
	conns map[string]*tcpConn // addr -> conn
}

// tcpConn TCP 连接包装
//
// 封装 net.Conn 并添加读写超时
type tcpConn struct {
	conn       net.Conn
	remoteAddr string
	lastUsed   atomic.Int64 // 最后使用时间（Unix timestamp）
	reader     *MessageReader
	writer     *MessageWriter
	closeOnce  sync.Once
	closeCh    chan struct{}
}

// NewTCPTransport 创建 TCP 传输
//
// 使用默认配置
func NewTCPTransport(listenAddr string) (*TCPTransport, error) {
	return NewTCPTransportWithConfig(&TransportConfig{
		ListenAddr:         listenAddr,
		MaxMessageSize:     1024 * 1024 * 100, // 100MB
		ReadTimeout:        30 * time.Second,
		WriteTimeout:       30 * time.Second,
		KeepAliveInterval:  10 * time.Second,
		KeepAliveTimeout:   30 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 5 * time.Second, // P2-2: 通道发送超时
	})
}

// NewTCPTransportWithConfig 创建 TCP 传输（自定义配置）
//
// P2-5: 添加配置验证，确保配置值的有效性
func NewTCPTransportWithConfig(config *TransportConfig) (*TCPTransport, error) {
	if config == nil {
		config = DefaultTransportConfig()
	}

	// P2-5: 验证配置有效性
	if err := validateTransportConfig(config); err != nil {
		return nil, err
	}

	// acceptCh 用于接受连接，缓冲区可以小一些（连接按需处理）
	// recvCh 用于接收消息，使用配置的 BufferSize
	acceptChSize := config.BufferSize / 8
	if acceptChSize < MinAcceptChannelSize {
		acceptChSize = MinAcceptChannelSize
	}

	// 使用系统默认编解码器（Protobuf）
	codec, err := NewCodec(defaultCodec)
	if err != nil {
		return nil, err
	}

	t := &TCPTransport{
		config:     config,
		codec:      codec,
		acceptCh:   make(chan *tcpConn, acceptChSize),
		acceptDone: make(chan struct{}),
		connPool: &connPool{
			conns: make(map[string]*tcpConn),
		},
		poolDone:  make(chan struct{}),
		recvCh:    make(chan MsgFrame, config.BufferSize),
		stopCh:    make(chan struct{}),
		localAddr: config.ListenAddr,
		// NodeID 通过 Start() 参数传入（可选）
		// msgSeqGenerator 通过 Start() 参数传入（必需）
	}

	return t, nil
}

// Start 启动传输层
//
// 参数：
//   - nodeID: 节点 ID（全局唯一，用于消息去重和幂等性）
//   - msgSeqGenerator: 消息序列号生成器（必需，单调递增）
//   - listenAddr: 监听地址（必需，如 "0.0.0.0:9211" 或 "0.0.0.0:0" 自动分配端口）
//
// 启动监听器和连接池管理器
func (t *TCPTransport) Start(nodeID *uint64, msgSeqGenerator func() uint64, listenAddr string) error {
	if !t.started.CompareAndSwap(false, true) {
		return types.NewTransportStateError("已经启动")
	}

	// 验证必需参数
	if msgSeqGenerator == nil {
		return types.NewStoreInvalidParameterError("msgSeqGenerator 不能为空")
	}

	// 验证监听地址（格式、host 解析、port 有效性）
	validatedAddr, err := validateListenAddr(listenAddr, "tcp")
	if err != nil {
		return types.NewStoreInvalidParameterError(err.Error())
	}

	// 更新配置中的监听地址（使用验证后的地址）
	t.config.ListenAddr = validatedAddr

	// 设置节点 ID
	if nodeID != nil {
		t.NodeID.Store(*nodeID)
	}

	// 设置消息序列号生成器
	t.msgSeqGenerator.Store(msgSeqGenerator)

	logging.Infof("启动 TCP 传输层，监听地址: %s, NodeID: %d", validatedAddr, t.NodeID.Load())

	// 启动监听器
	if err := t.startListener(); err != nil {
		t.started.Store(false)
		return types.NewTransportConnectionError("启动监听器", "", err)
	}

	// 启动连接池管理器
	t.startConnPoolManager()

	logging.Infof("TCP 传输层启动成功，监听地址: %s", t.config.ListenAddr)
	return nil
}

// startListener 启动监听器
func (t *TCPTransport) startListener() error {
	listener, err := net.Listen("tcp", t.config.ListenAddr)
	if err != nil {
		return types.NewTransportConnectionError("监听", "", err)
	}

	t.listener = listener

	// 预先添加计数（对应 acceptLoop 中的 handleConn 调用）
	// 注意：这里只添加 acceptLoop 本身的计数
	t.acceptWg.Add(1)
	go t.acceptLoop()

	return nil
}

// acceptLoop 接受连接循环
func (t *TCPTransport) acceptLoop() {
	defer t.acceptWg.Done()
	defer close(t.acceptDone)

	logging.Info("开始接受连接...")

	for {
		// 检查停止信号（在 Accept 前先检查，避免阻塞）
		select {
		case <-t.stopCh:
			logging.Info("监听器已关闭（收到停止信号）")
			return
		default:
		}

		conn, err := t.listener.Accept()
		if err != nil {
			// 接受失败，再次检查是否是正常关闭
			select {
			case <-t.stopCh:
				// 正常关闭
				logging.Info("监听器已关闭")
				return
			default:
				// 非正常关闭导致的错误，记录但不继续循环
				logging.Errorf("接受连接失败: %v", err)
				return
			}
		}

		// 包装连接
		wrappedConn := t.wrapConn(conn)
		if wrappedConn == nil {
			_ = conn.Close()
			continue
		}

		// 添加到连接池
		t.addConnToPool(wrappedConn)

		// 启动接收协程（自己管理 WaitGroup 计数）
		go t.handleConn(wrappedConn)

		logging.Infof("接受新连接: %s", wrappedConn.remoteAddr)
	}
}

// wrapConn 包装连接
func (t *TCPTransport) wrapConn(conn net.Conn) *tcpConn {
	if conn == nil {
		logging.Warn("wrapConn: conn 参数为 nil，返回 nil")
		return nil
	}
	remoteAddr := conn.RemoteAddr().String()

	// 设置 TCP 选项
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// 设置保活
		if err := tcpConn.SetKeepAlive(true); err != nil {
			logging.Warnf("设置 TCP KeepAlive 失败: %v", err)
		}
		if err := tcpConn.SetKeepAlivePeriod(t.config.KeepAliveInterval); err != nil {
			logging.Warnf("设置 TCP KeepAlivePeriod 失败: %v", err)
		}
		// 设置初始读超时
		if err := conn.SetDeadline(time.Now().Add(t.config.ReadTimeout)); err != nil {
			logging.Warnf("设置 TCP 读超时失败: %v", err)
		}
	}

	tc := &tcpConn{
		conn:       conn,
		remoteAddr: remoteAddr,
		reader:     NewMessageReader(conn, t.codec),
		writer:     NewMessageWriter(conn, t.codec),
		closeCh:    make(chan struct{}),
	}
	// 初始化最后使用时间为当前时间
	tc.lastUsed.Store(time.Now().Unix())

	return tc
}

// handleConn 处理连接
func (t *TCPTransport) handleConn(conn *tcpConn) {
	// 自己管理 WaitGroup 计数
	t.stopWg.Add(1)
	defer t.stopWg.Done()

	defer func() {
		// 连接关闭时从池中移除
		t.removeConnFromPool(conn.remoteAddr)
		if err := conn.Close(); err != nil {
			logging.Warnf("关闭连接失败: %s, error: %v", conn.remoteAddr, err)
		}
	}()

	logging.Debugf("开始处理连接: %s", conn.remoteAddr)

	for {
		select {
		case <-t.stopCh:
			logging.Debugf("传输层停止，关闭连接: %s", conn.remoteAddr)
			return
		case <-conn.closeCh:
			logging.Debugf("连接已关闭: %s", conn.remoteAddr)
			return
		default:
		}

		// 设置读取超时
		if err := setReadTimeout(conn.conn, t.config.ReadTimeout); err != nil {
			logging.Errorf("设置读超时失败: %v", err)
			// 设置失败，关闭连接并退出处理循环
			t.removeConnFromPool(conn.remoteAddr)
			return
		}

		// 读取消息帧
		msgFrame, err := conn.reader.ReadMsgFrame()
		if err != nil {
			if err != io.EOF && !isTimeoutError(err) {
				logging.Errorf("读取消息失败: %v", err)
			}
			return
		}

		// 更新最后使用时间
		conn.lastUsed.Store(time.Now().Unix())

		// P2-2: 发送到接收通道（使用配置的超时时间）
		channelTimeout := t.config.ChannelSendTimeout
		if channelTimeout <= 0 {
			channelTimeout = 5 * time.Second // 默认值
		}
		select {
		case t.recvCh <- *msgFrame:
		case <-t.stopCh:
			return
		case <-time.After(channelTimeout):
			logging.Errorf("接收通道阻塞超过 %v，消息丢弃", channelTimeout)
			return
		}

		logging.Debugf("接收消息: %s from %s", msgFrame.Type(), conn.remoteAddr)
	}
}

// Stop 停止传输层
//
// 优雅关闭所有连接和监听器
func (t *TCPTransport) Stop() error {
	if !t.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	t.stopOnce.Do(func() {
		logging.Info("停止 TCP 传输层...")

		// 先关闭停止信号，通知所有协程退出
		close(t.stopCh)

		// 关闭连接池管理器
		close(t.poolDone)

		// 关闭监听器（会触发 Accept() 返回错误）
		if t.listener != nil {
			_ = t.listener.Close()
		}

		// 等待 acceptLoop 退出
		t.acceptWg.Wait()
		logging.Info("监听器已关闭")

		// 关闭所有连接
		t.closeAllConns()

		// 等待所有协程退出
		t.stopWg.Wait()
		t.poolWg.Wait()

		// 关闭接收通道
		t.recvOnce.Do(func() {
			close(t.recvCh)
		})

		logging.Info("TCP 传输层已停止")
	})

	return nil
}

// Send 发送消息到指定节点
//
// 支持函数选项模式，可动态配置 TLV 扩展字段：
//   - WithHopCount(totalHop uint16) - 设置跳数 TTL
//   - WithCompression(compressID uint16) - 设置压缩算法
//   - WithEncryption(encryptID uint16) - 设置加密算法
//
// 阻塞直到消息发送成功或失败
func (t *TCPTransport) Send(ctx context.Context, addr string, msg Message, opts ...SendOpt) error {
	if !t.started.Load() {
		return types.NewTransportStateError("未启动")
	}

	// 处理发送选项
	options := processSendOptions(opts...)
	defer releaseSendOptions(options)

	// 获取或创建连接
	conn, err := t.getOrCreateConn(addr)
	if err != nil {
		return types.NewTransportConnectionError("获取连接", "", err)
	}

	// 设置写入超时
	if err := setWriteTimeout(conn.conn, t.config.WriteTimeout); err != nil {
		// 设置失败，关闭连接并从池中移除
		_ = conn.Close()
		t.removeConnFromPool(addr)
		return types.NewTransportConnectionError("设置写超时", "", err)
	}

	// 发送消息（带 TLV 选项）
	msgSeq := t.GenerateMsgSeq()
	if err := conn.writer.WriteMessageWithOptions(msg, t.NodeID.Load(), msgSeq, options); err != nil {
		// 发送失败，从池中移除连接（removeConnFromPool 会关闭连接）
		t.removeConnFromPool(addr)
		return types.NewTransportSendError(err)
	}

	// 更新最后使用时间
	conn.lastUsed.Store(time.Now().Unix())

	logging.Debugf("发送消息: %s to %s", msg.Type(), addr)
	return nil
}

// ForwardMessage 转发消息到指定节点
// 自动递减 Hops（从 FixedHeader），Hops 减至 0 时返回错误
func (t *TCPTransport) ForwardMessage(ctx context.Context, addr string, msgExt MsgFrame) (uint64, error) {
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

	conn, err := t.getOrCreateConn(addr)
	if err != nil {
		return 0, types.NewTransportConnectionError("获取连接", "", err)
	}

	if err := setWriteTimeout(conn.conn, t.config.WriteTimeout); err != nil {
		_ = conn.Close()
		t.removeConnFromPool(addr)
		return 0, types.NewTransportConnectionError("设置写超时", "", err)
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

	msgSeq := t.GenerateMsgSeq()
	// 转发消息使用单向请求 Flags（Gossip 类型的消息通常是单向的）
	frame := NewFrame(t.NodeID.Load(), msgSeq, forwardMsg.Type(), uint16(t.codec.Type()), FlagsOneWayRequest, msgData)
	frame.AddTLVFields(tlvFields)
	frame.Finalize()

	frameData, err := frame.Marshal()
	if err != nil {
		return 0, types.NewTransportSendError(err)
	}

	if _, err := conn.conn.Write(frameData); err != nil {
		t.removeConnFromPool(addr)
		return 0, types.NewTransportSendError(err)
	}

	conn.lastUsed.Store(time.Now().Unix())

	hops, _ := forwardMsg.GetHopCount()
	logging.Debugf("转发消息: %s to %s, Hops=%d", forwardMsg.Type(), addr, hops)

	return msgSeq, nil
}

// ========================================
// 批量转发实现
// ========================================

const (
	// maxBatchConcurrency 最大批量转发并发数
	maxBatchConcurrency = 10
	// maxBatchSize 最大批量大小
	maxBatchSize = 100
)

// BatchForwardMessage 批量转发消息
//
// 并发转发消息到多个目标地址，部分失败不影响其他转发
func (t *TCPTransport) BatchForwardMessage(
	ctx context.Context,
	addrs []string,
	msgExt MsgFrame,
) BatchForwardMessageResult {
	if !t.started.Load() {
		return createBatchForwardResult(addrs, types.NewTransportStateError("未启动"))
	}

	return executeBatchForward(ctx, addrs, msgExt, t.ForwardMessage)
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
func (t *TCPTransport) Receive() <-chan MsgFrame {
	return t.recvCh
}

// ========================================
// 连接池管理
// ========================================

// getOrCreateConn 获取或创建连接
// 使用双重检查锁定模式避免 TOCTOU 竞态
func (t *TCPTransport) getOrCreateConn(addr string) (*tcpConn, error) {
	// 第一次检查：快速路径（无锁）
	conn := t.getConnFromPool(addr)
	if conn != nil && !conn.isClosed() {
		return conn, nil
	}

	// 需要创建新连接，加锁避免重复拨号
	t.connPool.mu.Lock()
	defer t.connPool.mu.Unlock()

	// 第二次检查：其他协程可能已创建连接
	conn = t.connPool.conns[addr]
	if conn != nil && !conn.isClosed() {
		return conn, nil
	}

	// 持有锁的情况下拨号并添加到池
	return t.dialConnLocked(addr)
}

// dialConn 拨号创建连接（外部已加锁版本）
// 注意：调用前必须持有 t.connPool.mu.Lock()
func (t *TCPTransport) dialConnLocked(addr string) (*tcpConn, error) {
	logging.Debugf("拨号连接: %s", addr)

	// 建立连接
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, types.NewTransportConnectionError("拨号", "", err)
	}

	// 包装连接
	wrappedConn := t.wrapConn(conn)
	if wrappedConn == nil {
		_ = conn.Close()
		return nil, types.NewTransportConnectionError("包装连接", "", nil)
	}

	// 添加到池（调用方已持有锁，直接设置）
	t.connPool.conns[wrappedConn.remoteAddr] = wrappedConn

	return wrappedConn, nil
}

// addConnToPool 添加连接到池
func (t *TCPTransport) addConnToPool(conn *tcpConn) {
	t.connPool.mu.Lock()
	defer t.connPool.mu.Unlock()

	// 关闭旧连接（先从池中移除，避免继续使用）
	if oldConn, exists := t.connPool.conns[conn.remoteAddr]; exists {
		delete(t.connPool.conns, conn.remoteAddr) // 先移除
		_ = oldConn.Close()                       // 再关闭（触发 handleConn 退出）
	}

	t.connPool.conns[conn.remoteAddr] = conn
}

// getConnFromPool 从池中获取连接
func (t *TCPTransport) getConnFromPool(addr string) *tcpConn {
	t.connPool.mu.RLock()
	defer t.connPool.mu.RUnlock()

	return t.connPool.conns[addr]
}

// removeConnFromPool 从池中移除连接
func (t *TCPTransport) removeConnFromPool(addr string) {
	t.connPool.mu.Lock()
	defer t.connPool.mu.Unlock()

	if conn, exists := t.connPool.conns[addr]; exists {
		delete(t.connPool.conns, addr)
		_ = conn.Close()
	}
}

// closeAllConns 关闭所有连接
func (t *TCPTransport) closeAllConns() {
	t.connPool.mu.Lock()
	defer t.connPool.mu.Unlock()

	for addr, conn := range t.connPool.conns {
		_ = conn.Close()
		delete(t.connPool.conns, addr)
	}
}

// startConnPoolManager 启动连接池管理器
//
// 定期清理空闲连接
func (t *TCPTransport) startConnPoolManager() {
	t.poolWg.Add(1)
	go func() {
		defer t.poolWg.Done()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				t.cleanupIdleConns()
			case <-t.poolDone:
				return
			}
		}
	}()
}

// cleanupIdleConns 清理空闲连接
//
// 关闭超过 2 分钟未使用的连接
func (t *TCPTransport) cleanupIdleConns() {
	t.connPool.mu.Lock()
	defer t.connPool.mu.Unlock()

	now := time.Now().Unix()
	// 直接使用秒级精度，避免整数除法精度丢失
	idleTimeout := int64(DefaultIdleTimeout.Seconds())

	for addr, conn := range t.connPool.conns {
		lastUsed := conn.lastUsed.Load()
		if now-lastUsed > idleTimeout {
			logging.Debugf("清理空闲连接: %s", addr)
			_ = conn.Close()
			delete(t.connPool.conns, addr)
		}
	}
}

// ========================================
// tcpConn 方法
// ========================================

// Read 读取数据
func (c *tcpConn) Read(p []byte) (n int, err error) {
	return c.conn.Read(p)
}

// Write 写入数据
func (c *tcpConn) Write(p []byte) (n int, err error) {
	return c.conn.Write(p)
}

// Close 关闭连接
func (c *tcpConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closeCh)
	})
	return c.conn.Close()
}

// RemoteAddr 返回远程地址
func (c *tcpConn) RemoteAddr() string {
	return c.remoteAddr
}

// LocalAddr 返回本地地址
func (c *tcpConn) LocalAddr() string {
	if c.conn == nil {
		return ""
	}
	return c.conn.LocalAddr().String()
}

// SetDeadline 设置读写超时
func (c *tcpConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// SetReadDeadline 设置读超时
func (c *tcpConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline 设置写超时
func (c *tcpConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// isClosed 检查连接是否已关闭
func (c *tcpConn) isClosed() bool {
	select {
	case <-c.closeCh:
		return true
	default:
		return false
	}
}

// ========================================
// 工具函数
// ========================================

// isTimeoutError 判断是否为超时错误
func isTimeoutError(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return false
}

// GetLocalAddr 获取本地地址
func (t *TCPTransport) GetLocalAddr() string {
	return t.localAddr
}

// GetNodeID 获取当前节点 ID
//
// 返回:
//   - uint64: 节点 ID（全局唯一，用于消息去重和幂等性）
func (t *TCPTransport) GetNodeID() uint64 {
	return t.NodeID.Load()
}

// GenerateMsgSeq 生成下一条消息序列号
//
// 返回:
//   - uint64: 消息序列号（单调递增，全局唯一）
func (t *TCPTransport) GenerateMsgSeq() uint64 {
	return generateMsgSeq(t.msgSeqGenerator.Load())
}

// GetConfig 获取配置
func (t *TCPTransport) GetConfig() *TransportConfig {
	return t.config
}

// Stats 获取统计信息
func (t *TCPTransport) Stats() map[string]any {
	t.connPool.mu.RLock()
	defer t.connPool.mu.RUnlock()

	stats := make(map[string]any)
	stats[tcpStatKeyStarted] = t.started.Load()
	stats[tcpStatKeyStopped] = t.stopped.Load()
	stats[tcpStatKeyListenAddr] = t.localAddr
	stats[tcpStatKeyActiveConnections] = len(t.connPool.conns)

	return stats
}
