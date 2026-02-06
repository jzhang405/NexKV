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
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

// DiscoveryService mDNS 发现服务
// 用于局域网内节点自动发现
type DiscoveryService struct {
	host        host.Host
	serviceTag  string
	service     mdns.Service
	onPeerFound func(peer.AddrInfo)
}

// NewDiscoveryService 创建 mDNS 发现服务
//
// 参数:
//   - h: libp2p Host 实例
//   - tag: 服务标签，用于标识发现的服务（如 "nexkv-discovery"）
//   - onPeerFound: 发现节点时的回调函数
//
// 返回:
//   - *DiscoveryService: 发现服务实例
func NewDiscoveryService(h host.Host, tag string, onPeerFound func(peer.AddrInfo)) *DiscoveryService {
	return &DiscoveryService{
		host:        h,
		serviceTag:  tag,
		onPeerFound: onPeerFound,
	}
}

// Start 启动 mDNS 发现服务
//
// 启动后，服务会：
//   - 广播自己的 PeerInfo 到局域网
//   - 监听其他节点的广播
//   - 自动连接发现的节点
func (ds *DiscoveryService) Start(ctx context.Context) error {
	ds.service = mdns.NewMdnsService(ds.host, ds.serviceTag, ds)
	if ds.service == nil {
		return fmt.Errorf("创建 mDNS 服务失败: 服务为空")
	}

	// 启动服务
	if err := ds.service.Start(); err != nil {
		return fmt.Errorf("启动 mDNS 服务失败: %w", err)
	}

	return nil
}

// HandlePeerFound 处理发现的节点（实现 mdns.Notifee 接口）
//
// 流程:
//  1. 过滤自己
//  2. 触发回调（如果设置）
//  3. 自动连接节点
func (ds *DiscoveryService) HandlePeerFound(pi peer.AddrInfo) {
	// 过滤自己
	if pi.ID == ds.host.ID() {
		return
	}

	// 回调处理
	if ds.onPeerFound != nil {
		ds.onPeerFound(pi)
	}

	// 自动连接
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := ds.host.Connect(ctx, pi); err != nil {
			// 连接失败，记录但不阻塞
			return
		}
	}()
}

// Close 关闭 mDNS 服务
func (ds *DiscoveryService) Close() error {
	if ds.service != nil {
		return ds.service.Close()
	}
	return nil
}
