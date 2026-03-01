// Package discovery 提供节点发现的基础设施实现
package discovery

import (
	"context"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/sirupsen/logrus"
)

var discoveryLog = logrus.New().WithField("component", "discovery")

// ==========================================
// MDNSDiscovery mDNS 发现实现
// ==========================================

// MDNSDiscovery mDNS 节点发现服务实现
type MDNSDiscovery struct {
	host     host.Host
	tag      string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	notifee  service.DiscoveryNotifee
	mdnsSvc  mdns.Service
	provider service.ExecutorManager
	started  bool
	mu       sync.RWMutex
}

// mdnsNotifee 适配 libp2p mdns.Notifee 接口
type mdnsNotifee struct {
	host   host.Host
	parent *MDNSDiscovery
}

// HandlePeerFound 处理发现的节点（实现 mdns.Notifee）
func (n *mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	// 转换为领域模型
	peerID := model.PeerID(pi.ID.String())

	// ✅ 使用适配器转换地址，避免领域层依赖 libp2p
	addrs := ConvertToDomainAddresses(pi.Addrs)

	// 尝试连接
	if err := n.host.Connect(context.Background(), pi); err != nil {
		discoveryLog.WithFields(logrus.Fields{
			"peer":  pi.ID,
			"error": err,
		}).Warn("failed to connect to discovered peer")
	}

	// 通知领域层（使用领域抽象）
	if n.parent.notifee != nil {
		n.parent.notifee.HandlePeerFound(peerID, addrs)
	}
}

// NewMDNSDiscovery 创建 mDNS 发现服务
// provider 是必要参数，用于管理 goroutine 生命周期
func NewMDNSDiscovery(h host.Host, tag string, provider service.ExecutorManager) *MDNSDiscovery {
	return &MDNSDiscovery{
		host:     h,
		tag:      tag,
		provider: provider,
	}
}

// Start 启动发现服务（实现 DiscoveryService 接口）
func (d *MDNSDiscovery) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.started {
		return nil
	}

	// 创建独立的上下文，便于关闭控制
	d.ctx, d.cancel = context.WithCancel(ctx)

	// 创建 mdns 通知适配器
	notifee := &mdnsNotifee{
		host:   d.host,
		parent: d,
	}

	// 创建 mDNS 服务
	d.mdnsSvc = mdns.NewMdnsService(d.host, d.tag, notifee)

	// 启动 mDNS 服务（使用 TaskPoolProvider）
	d.wg.Add(1)
	startFunc := func(ctx context.Context) {
		defer d.wg.Done()
		if err := d.mdnsSvc.Start(); err != nil {
			discoveryLog.WithField("error", err).Warn("failed to start mDNS service")
			return
		}
		<-d.ctx.Done()
	}

	_ = d.provider.Submit(d.ctx, startFunc)

	d.started = true
	discoveryLog.WithField("tag", d.tag).Info("mDNS discovery service started")
	return nil
}

// Stop 停止发现服务（实现 DiscoveryService 接口）
func (d *MDNSDiscovery) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return nil
	}

	// 取消上下文，让 goroutine 退出
	if d.cancel != nil {
		d.cancel()
	}

	// 等待 goroutine 退出
	d.wg.Wait()

	// 关闭 mDNS 服务
	if d.mdnsSvc != nil {
		if err := d.mdnsSvc.Close(); err != nil {
			discoveryLog.WithField("error", err).Warn("failed to close mDNS service")
		}
	}

	d.started = false
	discoveryLog.Info("mDNS discovery service stopped")
	return nil
}

// SetNotifee 设置节点发现通知处理器（实现 DiscoveryService 接口）
func (d *MDNSDiscovery) SetNotifee(notifee service.DiscoveryNotifee) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.notifee = notifee
}
