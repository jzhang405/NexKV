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
func NewDiscoveryService(h host.Host, tag string, ctx context.Context, wg *sync.WaitGroup) *DiscoveryService {
	notifee := &discoveryNotifee{host: h}

	mdnsSvc := mdns.NewMdnsService(h, tag, notifee)

	d := &DiscoveryService{
		host:    h,
		tag:     tag,
		ctx:     ctx,
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
	if d.mdnsSvc != nil {
		return d.mdnsSvc.Close()
	}
	return nil
}
