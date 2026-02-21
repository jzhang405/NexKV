package transport

import (
	"context"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/sirupsen/logrus"
)

// DiscoveryService mDNS 节点发现服务
type DiscoveryService struct {
	host    host.Host
	tag     string
	ctx     context.Context
	cancel  context.CancelFunc
	wg      *sync.WaitGroup
	notifee *discoveryNotifee
	mdnsSvc mdns.Service
}

// discoveryNotifee 实现 mdns.Notifee 接口
type discoveryNotifee struct {
	host host.Host
}

// HandlePeerFound 处理发现的节点
func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	if err := n.host.Connect(context.Background(), pi); err != nil {
		transportLog.WithFields(logrus.Fields{
			"peer":  pi.ID,
			"error": err,
		}).Warn("failed to connect to discovered peer")
	}
}

// NewDiscoveryService 创建发现服务
func NewDiscoveryService(h host.Host, tag string, parentCtx context.Context, wg *sync.WaitGroup) *DiscoveryService {
	notifee := &discoveryNotifee{host: h}

	mdnsSvc := mdns.NewMdnsService(h, tag, notifee)

	// 创建独立的上下文，便于关闭控制
	ctx, cancel := context.WithCancel(parentCtx)

	d := &DiscoveryService{
		host:    h,
		tag:     tag,
		ctx:     ctx,
		cancel:  cancel,
		wg:      wg,
		notifee: notifee,
		mdnsSvc: mdnsSvc,
	}

	// 启动 mDNS 服务
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if err := mdnsSvc.Start(); err != nil {
			transportLog.WithField("error", err).Warn("failed to start mDNS service")
			return
		}
		<-ctx.Done()
	}()

	return d
}

// Close 关闭发现服务
func (d *DiscoveryService) Close() error {
	// 先取消上下文，让 goroutine 退出
	if d.cancel != nil {
		d.cancel()
	}

	// 关闭 mDNS 服务
	if d.mdnsSvc != nil {
		return d.mdnsSvc.Close()
	}
	return nil
}
