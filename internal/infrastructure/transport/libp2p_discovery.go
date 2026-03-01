package transport

import (
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/discovery"
	"github.com/libp2p/go-libp2p/core/host"
)

// DiscoveryService 是 service.DiscoveryService 的别名
// 用于向后兼容，新代码应直接使用 service.DiscoveryService
type DiscoveryService = service.DiscoveryService

// NewDiscoveryService 创建发现服务（工厂函数）
// 使用基础设施层的 discovery 包创建具体的 mDNS 实现
// provider 是必要参数，用于管理 goroutine 生命周期
func NewDiscoveryService(h host.Host, tag string, provider service.TaskExecutor) service.DiscoveryService {
	return discovery.NewMDNSDiscovery(h, tag, provider)
}
