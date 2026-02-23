// Package adapters 提供测试框架的组件适配器实现
package adapters

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/jzhang405/NexKV/pkg/test/framework"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	swarmtesting "github.com/libp2p/go-libp2p/p2p/net/swarm/testing"
	"github.com/multiformats/go-multiaddr"
)

// TransportAdapterConfig Transport 适配器配置
type TransportAdapterConfig struct {
	// ListenAddr 监听地址
	ListenAddr string
	// DiscoveryTag 发现标签
	DiscoveryTag string
	// EnableDiscovery 是否启用节点发现
	EnableDiscovery bool
}

// DefaultTransportAdapterConfig 返回默认配置
func DefaultTransportAdapterConfig() *TransportAdapterConfig {
	return &TransportAdapterConfig{
		ListenAddr:      "/ip4/0.0.0.0/tcp/0",
		DiscoveryTag:    "nexkv-test-discovery",
		EnableDiscovery: true,
	}
}

// TransportAdapter Transport 层测试适配器
// 封装 libp2p Host 和 MockConnectionGater，提供网络分区模拟能力
//
// v2.12 更新：
// - 使用 swarm/testing.MockConnectionGater 替代自实现
// - 添加并发安全保护（P0-1 修复）
// - 实现 TestComponent 和 TestNode 接口
type TransportAdapter struct {
	// 基础组件
	framework.BaseComponent

	// libp2p 相关
	host      host.Host
	connGater *swarmtesting.MockConnectionGater // libp2p 官方连接控制器
	transport service.Transport

	// 测试环境
	env          framework.TestEnvironment
	dependencies []framework.TestComponent

	// 并发安全字段（P0-1 修复）
	blockedPeers map[peer.ID]bool
	mu           sync.RWMutex

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// 确保实现接口
var _ framework.TestComponent = (*TransportAdapter)(nil)
var _ framework.TestNode = (*TransportAdapter)(nil)

// NewTransportAdapter 创建 Transport 适配器
func NewTransportAdapter(config *TransportAdapterConfig) (*TransportAdapter, error) {
	if config == nil {
		config = DefaultTransportAdapterConfig()
	}

	// 确保有默认监听地址
	if config.ListenAddr == "" {
		config.ListenAddr = "/ip4/0.0.0.0/tcp/0"
	}
	// 确保有默认发现标签
	if config.DiscoveryTag == "" {
		config.DiscoveryTag = "nexkv-test-discovery"
	}

	// 创建 libp2p 官方 MockConnectionGater
	connGater := swarmtesting.DefaultMockConnectionGater()

	// 创建 libp2p Host（使用 MockConnectionGater）
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(config.ListenAddr),
		libp2p.ConnectionGater(connGater),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create libp2p host")
	}

	// ⭐ P0-1 修复：在初始化时一次性设置 PeerDial 闭包
	// 避免每次 BlockPeer 调用时重新赋值导致的竞态条件
	adapter := &TransportAdapter{
		BaseComponent: *framework.NewBaseComponent(
			h.ID().String(),
			framework.ComponentTypeTransport,
			[]framework.ComponentType{}, // Transport 无依赖
		),
		host:         h,
		connGater:    connGater,
		blockedPeers: make(map[peer.ID]bool),
	}

	// 设置 PeerDial 闭包（只设置一次）
	connGater.PeerDial = func(p peer.ID) bool {
		adapter.mu.RLock()
		defer adapter.mu.RUnlock()
		return !adapter.blockedPeers[p]
	}

	// 设置 Dial 闭包（地址级别拦截）
	connGater.Dial = func(p peer.ID, addr multiaddr.Multiaddr) bool {
		adapter.mu.RLock()
		defer adapter.mu.RUnlock()
		return !adapter.blockedPeers[p]
	}

	// 创建 Transport 实例
	ctx, cancel := context.WithCancel(context.Background())
	adapter.ctx = ctx
	adapter.cancel = cancel

	// 使用 transportWrapper 包装 host
	adapter.transport = &transportWrapper{host: h}

	return adapter, nil
}

// =====================
// TestNode 接口实现
// =====================

// ID 返回节点唯一标识
func (a *TransportAdapter) ID() string {
	return a.host.ID().String()
}

// Address 返回节点地址
func (a *TransportAdapter) Address() string {
	addrs := a.host.Addrs()
	if len(addrs) == 0 {
		return ""
	}
	// 返回第一个可用的多地址
	addr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/p2p/%s", a.host.ID()))
	if err != nil {
		return ""
	}
	return addrs[0].Encapsulate(addr).String()
}

// Start 启动节点
func (a *TransportAdapter) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// libp2p host 在创建时已启动
	return nil
}

// Stop 停止节点
func (a *TransportAdapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
	}

	// 等待所有 goroutine 完成
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	if a.host != nil {
		return a.host.Close()
	}
	return nil
}

// IsRunning 检查节点是否运行
func (a *TransportAdapter) IsRunning() bool {
	return a.host != nil && len(a.host.Network().ListenAddresses()) > 0
}

// AddComponent 添加组件到节点
func (a *TransportAdapter) AddComponent(comp framework.TestComponent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dependencies = append(a.dependencies, comp)
	return nil
}

// GetComponent 获取节点上的组件实例
func (a *TransportAdapter) GetComponent(name string) (framework.TestComponent, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, dep := range a.dependencies {
		if dep.Name() == name {
			return dep, nil
		}
	}
	return nil, errors.Wrapf(errors.ErrComponentNotFound, "component %s not found", name)
}

// IsHealthy 检查节点健康状态
func (a *TransportAdapter) IsHealthy(ctx context.Context) bool {
	return a.host != nil
}

// ConnectTo 连接到另一个节点
func (a *TransportAdapter) ConnectTo(ctx context.Context, target framework.TestNode) error {
	if target == nil {
		return errors.Wrap(errors.ErrInvalidParam, "target node cannot be nil")
	}
	targetAddr := target.Address()
	if targetAddr == "" {
		return errors.Wrap(errors.ErrInvalidParam, "target node has no address")
	}

	// 解析多地址
	maddr, err := multiaddr.NewMultiaddr(targetAddr)
	if err != nil {
		return errors.Wrapf(err, "invalid address %s", targetAddr)
	}

	// 从多地址中提取 Peer ID
	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return errors.Wrap(err, "failed to extract peer info")
	}

	// 添加到 peerstore
	a.host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

	// 连接
	return a.host.Connect(ctx, *info)
}

// DisconnectFrom 断开与另一个节点的连接
func (a *TransportAdapter) DisconnectFrom(ctx context.Context, target framework.TestNode) error {
	if target == nil {
		return errors.Wrap(errors.ErrInvalidParam, "target node cannot be nil")
	}
	targetID := target.ID()
	pid, err := peer.Decode(targetID)
	if err != nil {
		return errors.Wrapf(err, "invalid peer ID %s", targetID)
	}

	return a.host.Network().ClosePeer(pid)
}

// IsConnectedTo 检查是否连接到指定节点
func (a *TransportAdapter) IsConnectedTo(nodeID string) bool {
	pid, err := peer.Decode(nodeID)
	if err != nil {
		return false
	}
	return a.host.Network().Connectedness(pid) == network.Connected
}

// GetConnectedPeers 获取已连接的节点列表
func (a *TransportAdapter) GetConnectedPeers() []string {
	peers := a.host.Network().Peers()
	result := make([]string, len(peers))
	for i, p := range peers {
		result[i] = p.String()
	}
	return result
}

// =====================
// 网络分区模拟
// =====================

// BlockPeer 阻止与指定节点的连接（网络分区模拟）
func (a *TransportAdapter) BlockPeer(pid peer.ID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blockedPeers[pid] = true
	// MockConnectionGater 通过 PeerDial 闭包检查，无需额外调用
}

// UnblockPeer 解除对指定节点的连接阻止
// P0-1 修复：保持其他 blocked 状态，避免状态丢失
func (a *TransportAdapter) UnblockPeer(pid peer.ID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.blockedPeers, pid)
	// MockConnectionGater 通过 PeerDial 闭包检查，无需额外调用
}

// IsBlocked 检查节点是否被阻止
func (a *TransportAdapter) IsBlocked(pid peer.ID) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.blockedPeers[pid]
}

// BlockSubnet 阻止整个子网（批量分区）
func (a *TransportAdapter) BlockSubnet(peers []peer.ID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, pid := range peers {
		a.blockedPeers[pid] = true
	}
}

// UnblockAll 解除所有阻止（恢复网络）
func (a *TransportAdapter) UnblockAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blockedPeers = make(map[peer.ID]bool)
}

// =====================
// TestComponent 接口实现
// =====================

// Init 初始化组件
func (a *TransportAdapter) Init(ctx context.Context, env framework.TestEnvironment) error {
	a.env = env
	return nil
}

// HealthCheck 执行深度健康检查
// P0-2 修复：使用 time.Timer 替代 time.After
func (a *TransportAdapter) HealthCheck(ctx context.Context) error {
	config := a.GetHealthCheckConfig()

	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	// P0-2 修复：使用 time.Timer 替代 time.After，避免 goroutine 泄漏
	retryTimer := time.NewTimer(config.RetryInterval)
	defer retryTimer.Stop()

	var lastErr error
	for i := 0; i < config.RetryCount; i++ {
		// 检查 context 是否已取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 执行健康检查
		if err := a.doHealthCheck(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}

		// 可中断的重试等待（最后一次不需要等待）
		if i < config.RetryCount-1 {
			// 安全地重置 timer
			if !retryTimer.Stop() {
				select {
				case <-retryTimer.C:
				default:
				}
			}
			retryTimer.Reset(config.RetryInterval)

			select {
			case <-retryTimer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return errors.Wrapf(lastErr, "health check failed after %d retries", config.RetryCount)
}

// doHealthCheck 执行实际的健康检查逻辑
func (a *TransportAdapter) doHealthCheck(ctx context.Context) error {
	// 检查 host 是否有效
	if a.host == nil {
		return errors.Wrap(errors.ErrNotInitialized, "host not initialized")
	}

	// 检查是否有监听地址
	addrs := a.host.Addrs()
	if len(addrs) == 0 {
		return errors.Wrap(errors.ErrNotInitialized, "no listen addresses")
	}

	return nil
}

// Close 关闭适配器
func (a *TransportAdapter) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	if a.host != nil {
		return a.host.Close()
	}
	return nil
}

// =====================
// 辅助类型
// =====================

// transportWrapper 包装 libp2p host 以实现 Transport 接口
type transportWrapper struct {
	host host.Host
}

// Self 返回本地节点 ID
func (t *transportWrapper) Self() model.PeerID {
	return model.PeerID(t.host.ID().String())
}

// Connect 连接到指定地址的节点
func (t *transportWrapper) Connect(ctx context.Context, addr string) (model.PeerID, error) {
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return "", err
	}

	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return "", err
	}

	if err := t.host.Connect(ctx, *info); err != nil {
		return "", err
	}

	return model.PeerID(info.ID.String()), nil
}

// Disconnect 断开与指定节点的连接
func (t *transportWrapper) Disconnect(peerID model.PeerID) error {
	pid, err := peer.Decode(string(peerID))
	if err != nil {
		return err
	}
	return t.host.Network().ClosePeer(pid)
}

// ConnectedPeers 返回当前已连接的节点列表
func (t *transportWrapper) ConnectedPeers() []model.PeerID {
	peers := t.host.Network().Peers()
	result := make([]model.PeerID, len(peers))
	for i, p := range peers {
		result[i] = model.PeerID(p.String())
	}
	return result
}

// IsConnected 检查是否与指定节点已连接
func (t *transportWrapper) IsConnected(peerID model.PeerID) bool {
	pid, err := peer.Decode(string(peerID))
	if err != nil {
		return false
	}
	return t.host.Network().Connectedness(pid) == network.Connected
}

// OpenStream 打开到指定节点的流式连接
// 注意：此方法在测试适配器中不支持，请使用 ConnectTo/IsConnectedTo 进行连接测试
func (t *transportWrapper) OpenStream(ctx context.Context, peerID model.PeerID, protocol string) (service.Stream, error) {
	return nil, errors.Wrap(errors.ErrNotImplemented, "OpenStream not supported in TransportAdapter: use ConnectTo/IsConnectedTo for connection testing")
}

// AcceptStream 接受指定协议的入站流
// 注意：此方法在测试适配器中不支持
func (t *transportWrapper) AcceptStream(protocol string) (service.Stream, error) {
	return nil, errors.Wrap(errors.ErrNotImplemented, "AcceptStream not supported in TransportAdapter: testing focuses on connection management")
}

// OpenChannel 打开到指定节点的双向通道
// 注意：此方法在测试适配器中不支持
func (t *transportWrapper) OpenChannel(ctx context.Context, peerID model.PeerID, protocol string) (service.Channel, error) {
	return nil, errors.Wrap(errors.ErrNotImplemented, "OpenChannel not supported in TransportAdapter: use ConnectTo/GetConnectedPeers for connection testing")
}

// SetStreamHandler 设置流处理器
// 注意：此方法在测试适配器中不支持
func (t *transportWrapper) SetStreamHandler(protocol string, handler func(service.Stream)) {
	// 测试适配器不支持流处理，静默忽略
}

// Close 关闭传输层
func (t *transportWrapper) Close() error {
	return t.host.Close()
}
