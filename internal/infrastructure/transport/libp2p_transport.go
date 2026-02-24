// Package transport 提供 Transport 接口的 libp2p 实现
package transport

import (
	"context"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"github.com/sirupsen/logrus"
)

// transportLog 使用 logrus 结构化日志
var transportLog = logrus.WithField("component", "transport")

// 常量定义
const (
	MaxPeerIDLength       = 128              // libp2p PeerID 最大长度
	MaxAddrLength         = 1024             // 地址最大长度
	DefaultMaxMessageSize = 10 * 1024 * 1024 // 默认最大消息大小 (10MB)
)

// 确保实现 service.Transport 接口
var _ service.Transport = (*Libp2pTransport)(nil)

// Libp2pTransport 实现 domain.Transport 新接口
type Libp2pTransport struct {
	mu        sync.RWMutex
	host      host.Host
	discovery service.DiscoveryService
	codec     *LengthPrefixedCodec
	acceptor  *streamAcceptor // 流接受器

	// 生命周期管理
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool

	// Discovery goroutine 管理
	wg sync.WaitGroup
}

// Config 传输层配置
type Config struct {
	ListenAddr      string
	DiscoveryTag    string
	EnableDiscovery bool
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:      "/ip4/0.0.0.0/tcp/0",
		DiscoveryTag:    "nexkv-discovery",
		EnableDiscovery: true,
	}
}

// NewLibp2pTransport 创建新的 libp2p 传输实现
func NewLibp2pTransport(ctx context.Context, cfg *Config) (*Libp2pTransport, error) {
	// 合并默认配置
	if cfg == nil {
		cfg = DefaultConfig()
	} else {
		// 为空字段填充默认值
		defaults := DefaultConfig()
		if cfg.ListenAddr == "" {
			cfg.ListenAddr = defaults.ListenAddr
		}
		if cfg.DiscoveryTag == "" {
			cfg.DiscoveryTag = defaults.DiscoveryTag
		}
	}

	h, err := libp2p.New(libp2p.ListenAddrStrings(cfg.ListenAddr))
	if err != nil {
		return nil, service.Wrap(err, "create libp2p host")
	}

	childCtx, cancel := context.WithCancel(ctx)
	t := &Libp2pTransport{
		host:     h,
		codec:    &LengthPrefixedCodec{},
		acceptor: newStreamAcceptor(h),
		ctx:      childCtx,
		cancel:   cancel,
	}

	if cfg.EnableDiscovery {
		t.discovery = NewDiscoveryService(h, cfg.DiscoveryTag, childCtx, &t.wg)
	}

	return t, nil
}

// ===========================
// 输入验证函数
// ===========================

func validatePeerID(peerID model.PeerID) error {
	if len(peerID) == 0 {
		return service.Wrap(service.ErrPeerIDInvalid, "empty")
	}
	if len(peerID) > MaxPeerIDLength {
		return service.Wrapf(service.ErrPeerIDInvalid, "too long: %d > %d", len(peerID), MaxPeerIDLength)
	}
	return nil
}

func validateAddr(addr string) error {
	if len(addr) == 0 {
		return service.Wrap(service.ErrAddrInvalid, "empty")
	}
	if len(addr) > MaxAddrLength {
		return service.Wrapf(service.ErrAddrTooLong, "%d > %d", len(addr), MaxAddrLength)
	}
	return nil
}

// ===========================
// 基础方法
// ===========================

// Self 返回本地节点 ID
func (t *Libp2pTransport) Self() model.PeerID {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed.Load() {
		return ""
	}
	if t.host == nil {
		return ""
	}
	return model.PeerID(t.host.ID().String())
}

// Connect 连接到指定地址的节点
func (t *Libp2pTransport) Connect(ctx context.Context, addr string) (model.PeerID, error) {
	if err := validateAddr(addr); err != nil {
		return "", err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed.Load() {
		return "", service.ErrTransportClosed
	}

	if t.host == nil || t.host.Network() == nil {
		return "", service.ErrTransportClosed
	}

	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return "", service.Wrap(service.ErrAddrInvalid, err.Error())
	}

	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return "", service.Wrap(service.ErrAddrInvalid, err.Error())
	}

	if t.host.Network().Connectedness(info.ID) == network.Connected {
		return model.PeerID(info.ID.String()), service.ErrAlreadyConnected
	}

	if err := t.host.Connect(ctx, *info); err != nil {
		return "", service.Wrapf(service.ErrConnectionFailed, "peer=%s, reason=%v", info.ID.String(), err)
	}

	return model.PeerID(info.ID.String()), nil
}

// Disconnect 断开与指定节点的连接
func (t *Libp2pTransport) Disconnect(peerID model.PeerID) error {
	if err := validatePeerID(peerID); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed.Load() {
		return service.ErrTransportClosed
	}

	pid, err := peer.Decode(peerID.String())
	if err != nil {
		return service.Wrap(service.ErrPeerIDInvalid, err.Error())
	}

	if t.host == nil || t.host.Network() == nil {
		return service.ErrTransportClosed
	}

	if t.host.Network().Connectedness(pid) != network.Connected {
		return service.ErrNotConnected
	}

	conns := t.host.Network().ConnsToPeer(pid)
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			return service.Wrap(err, "close connection")
		}
	}
	return nil
}

// ConnectedPeers 返回当前已连接的节点列表
func (t *Libp2pTransport) ConnectedPeers() []model.PeerID {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed.Load() {
		return nil
	}

	if t.host == nil || t.host.Network() == nil {
		return nil
	}

	peers := t.host.Network().Peers()
	result := make([]model.PeerID, 0, len(peers))
	for _, p := range peers {
		result = append(result, model.PeerID(p.String()))
	}
	return result
}

// IsConnected 检查是否与指定节点已连接
func (t *Libp2pTransport) IsConnected(peerID model.PeerID) bool {
	if err := validatePeerID(peerID); err != nil {
		return false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed.Load() {
		return false
	}

	if t.host == nil || t.host.Network() == nil {
		return false
	}

	pid, err := peer.Decode(peerID.String())
	if err != nil {
		return false
	}
	return t.host.Network().Connectedness(pid) == network.Connected
}

// ===========================
// OpenStream/OpenChannel 方法
// ===========================

// OpenStream 打开到指定节点的流式连接
func (t *Libp2pTransport) OpenStream(ctx context.Context, peerID model.PeerID, proto string) (service.Stream, error) {
	if err := validatePeerID(peerID); err != nil {
		return nil, err
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed.Load() {
		return nil, service.ErrTransportClosed
	}

	if t.host == nil {
		return nil, service.ErrTransportClosed
	}

	pid, err := peer.Decode(peerID.String())
	if err != nil {
		return nil, service.Wrap(service.ErrPeerIDInvalid, err.Error())
	}

	if !t.IsConnected(peerID) {
		return nil, service.ErrNotConnected
	}

	stream, err := t.host.NewStream(ctx, pid, protocol.ID(proto))
	if err != nil {
		return nil, service.Wrapf(service.ErrConnectionFailed, "open stream: %v", err)
	}

	return NewLibp2pStream(stream, proto), nil
}

// streamAcceptor 管理协议流的接受
type streamAcceptor struct {
	host    host.Host
	mu      sync.Mutex
	streams map[string]chan network.Stream
}

// newStreamAcceptor 创建新的流接受器
func newStreamAcceptor(h host.Host) *streamAcceptor {
	return &streamAcceptor{
		host:    h,
		streams: make(map[string]chan network.Stream),
	}
}

// AcceptStream 接受指定协议的入站流（支持并发和多次接受）
func (a *streamAcceptor) AcceptStream(ctx context.Context, proto string) (network.Stream, error) {
	a.mu.Lock()
	ch, exists := a.streams[proto]
	if !exists {
		ch = make(chan network.Stream, 10) // 缓冲 10 个流
		a.streams[proto] = ch
		// 只设置一次 handler
		a.host.SetStreamHandler(protocol.ID(proto), func(s network.Stream) {
			select {
			case ch <- s:
			default:
				// 缓冲区满，拒绝连接
				if err := s.Reset(); err != nil {
					transportLog.WithField("error", err).Warn("failed to reset stream")
				}
			}
		})
	}
	a.mu.Unlock()

	select {
	case stream := <-ch:
		return stream, nil
	case <-ctx.Done():
		return nil, service.ErrCanceled
	}
}

// Close 关闭接受器
func (a *streamAcceptor) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for proto, ch := range a.streams {
		a.host.RemoveStreamHandler(protocol.ID(proto))
		close(ch)
		delete(a.streams, proto)
	}
}

// AcceptStream 接受指定协议的入站流
func (t *Libp2pTransport) AcceptStream(proto string) (service.Stream, error) {
	ctx, cancel := context.WithTimeout(t.ctx, 30*time.Second)
	defer cancel()

	stream, err := t.acceptor.AcceptStream(ctx, proto)
	if err != nil {
		return nil, err
	}

	return NewLibp2pStream(stream, proto), nil
}

// OpenChannel 打开到指定节点的双向通道
func (t *Libp2pTransport) OpenChannel(ctx context.Context, peerID model.PeerID, proto string) (service.Channel, error) {
	stream, err := t.OpenStream(ctx, peerID, proto)
	if err != nil {
		return nil, err
	}

	libp2pStream, ok := stream.(*Libp2pStream)
	if !ok {
		return nil, service.Wrap(service.ErrInvalidParam, "unexpected stream type")
	}
	return NewLibp2pChannel(libp2pStream, DefaultChannelConfig()), nil
}

// ===========================
// SetStreamHandler 带 panic 恢复
// ===========================

// SetStreamHandler 设置流处理器（带 panic 恢复）
func (t *Libp2pTransport) SetStreamHandler(proto string, handler func(service.Stream)) {
	t.host.SetStreamHandler(protocol.ID(proto), func(s network.Stream) {
		// panic 恢复，防止节点崩溃
		defer func() {
			if r := recover(); r != nil {
				// 记录 panic 信息到日志（便于问题追踪）
				panicErr := service.Wrapf(service.ErrCallbackPanic, "stream=%s, panic=%v", s.ID(), r)
				log.Printf("[Transport] %v\n%s", panicErr, debug.Stack())
				if err := s.Reset(); err != nil {
					transportLog.WithField("error", err).Warn("failed to reset stream after panic")
				}
			}
		}()

		handler(NewLibp2pStream(s, proto))
	})
}

// ===========================
// Close 方法
// ===========================

// Close 关闭传输层（避免死锁）
func (t *Libp2pTransport) Close() error {
	// 0. 防御性 nil 检查
	if t == nil {
		return nil
	}

	// 1. 原子标记关闭状态
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}

	// 2. 取消上下文
	if t.cancel != nil {
		t.cancel()
	}

	// 3. 在锁外执行可能阻塞的操作，避免死锁
	var errs []error

	// 3.1 关闭 acceptor
	if t.acceptor != nil {
		t.acceptor.Close()
	}

	// 3.2 关闭 discovery
	if t.discovery != nil {
		if err := t.discovery.Stop(); err != nil {
			errs = append(errs, service.Wrap(err, "close discovery"))
		}
	}

	// 3.2 等待所有 goroutine 退出
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		errs = append(errs, service.Wrap(service.ErrTimeout, "wait goroutines"))
	}

	// 3.3 关闭 host
	if t.host != nil {
		if err := t.host.Close(); err != nil {
			errs = append(errs, service.Wrap(err, "close host"))
		}
	}

	// 4. 错误信息不暴露内部细节
	if len(errs) > 0 {
		log.Printf("[Transport] close failed: %v", errs)
		return service.ErrTransportClosed
	}
	return nil
}
