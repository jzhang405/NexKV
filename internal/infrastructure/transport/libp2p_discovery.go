package transport

import (
	"context"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/discovery"
	"github.com/libp2p/go-libp2p/core/host"
)

// DiscoveryService 是 service.DiscoveryService 的别名
// 用于向后兼容，新代码应直接使用 service.DiscoveryService
type DiscoveryService = service.DiscoveryService

// NewDiscoveryService 创建发现服务（工厂函数）
// 使用基础设施层的 discovery 包创建具体的 mDNS 实现
func NewDiscoveryService(h host.Host, tag string, parentCtx context.Context, wg *sync.WaitGroup) service.DiscoveryService {
	// 创建 mDNS 发现服务
	d := discovery.NewMDNSDiscovery(h, tag)

	// 启动服务
	if err := d.Start(parentCtx); err != nil {
		transportLog.WithField("error", err).Warn("failed to start discovery service")
	}

	// 添加到 WaitGroup 用于生命周期管理
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-parentCtx.Done()
		if err := d.Stop(); err != nil {
			transportLog.WithField("error", err).Warn("failed to stop discovery service")
		}
	}()

	return d
}
