// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	dht "github.com/libp2p/go-libp2p/p2p/discovery/kademlia"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
)

// DHTDiscovery DHT 发现服务
// 用于广域网节点自动发现
type DHTDiscovery struct {
	host      host.Host
	dht       *dht.IpfsDHT
	routing   *routing.RoutingDiscovery
	namespace string
}

// NewDHTDiscovery 创建 DHT 发现服务
//
// 参数:
//   - h: libp2p Host 实例
//   - ns: 命名空间，用于隔离不同集群的发现（如 "nexkv-cluster"）
//
// 返回:
//   - *DHTDiscovery: DHT 发现服务实例
//   - error: 创建失败时返回错误
func NewDHTDiscovery(h host.Host, ns string) (*DHTDiscovery, error) {
	// 验证命名空间
	if ns == "" {
		return nil, fmt.Errorf("命名空间不能为空")
	}

	// 创建 DHT
	dhtOpts := []dht.Option{
		dht.BootstrapPeers([]peer.AddrInfo{}), // 后续通过 Bootstrap 填充
	}

	d, err := dht.New(context.Background(), h, dhtOpts...)
	if err != nil {
		return nil, fmt.Errorf("创建 DHT 失败: %w", err)
	}

	return &DHTDiscovery{
		host:      h,
		dht:       d,
		routing:   routing.NewRoutingDiscovery(d),
		namespace: ns,
	}, nil
}

// Advertise 公布自己的地址到 DHT
//
// 将自己的 PeerInfo 公布到 DHT 网络，使其他节点可以找到
func (dd *DHTDiscovery) Advertise(ctx context.Context) error {
	_, err := dd.routing.Advertise(ctx, dd.namespace)
	if err != nil {
		return fmt.Errorf("公布地址失败: %w", err)
	}
	return nil
}

// FindPeers 查找同一命名空间的其他节点
//
// 返回一个 channel，用于异步接收发现的节点
func (dd *DHTDiscovery) FindPeers(ctx context.Context) <-chan peer.AddrInfo {
	peerChan, err := dd.routing.FindPeers(ctx, dd.namespace)
	if err != nil {
		// 返回已关闭的 channel
		ch := make(chan peer.AddrInfo)
		close(ch)
		return ch
	}
	return peerChan
}

// StartRefreshLoop 启动定期刷新循环
//
// 定期重新公布地址，确保其他节点可以持续发现
//
// 参数:
//   - ctx: 上下文，用于取消循环
//   - interval: 刷新间隔
func (dd *DHTDiscovery) StartRefreshLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 重新公布
			if err := dd.Advertise(ctx); err != nil {
				// 记录错误，继续循环
				continue
			}
		case <-ctx.Done():
			return
		}
	}
}

// Close 关闭 DHT 服务
func (dd *DHTDiscovery) Close() error {
	if dd.dht != nil {
		return dd.dht.Close()
	}
	return nil
}
