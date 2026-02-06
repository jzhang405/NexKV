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
	"github.com/libp2p/go-libp2p/core/network"
)

// BootstrapConfig Bootstrap 配置
// 用于配置启动时连接的节点列表
type BootstrapConfig struct {
	// Peers Bootstrap 节点列表
	Peers []peer.AddrInfo
}

// ConnectToBootstrap 连接 Bootstrap 节点
//
// 尝试连接配置的所有 Bootstrap 节点，即使部分失败也继续
//
// 参数:
//   - ctx: 上下文
//   - h: libp2p Host 实例
//   - cfg: Bootstrap 配置
//
// 返回:
//   - error: 所有连接都失败时返回错误
func ConnectToBootstrap(ctx context.Context, h host.Host, cfg *BootstrapConfig) error {
	if cfg == nil || len(cfg.Peers) == 0 {
		// 无 Bootstrap 节点，直接返回
		return nil
	}

	// 记录成功连接数
	successCount := 0

	// 并行连接所有 Bootstrap 节点
	for _, p := range cfg.Peers {
		go func(pi peer.AddrInfo) {
			// 为每个连接设置独立的超时上下文
			connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			if err := h.Connect(connectCtx, pi); err != nil {
				// 连接失败，记录但不阻塞
				return
			}
			successCount++
		}(p)
	}

	// 等待所有连接尝试完成
	// 这里简化处理，实际可能需要更复杂的同步机制
	// 给予一定时间让连接建立
	time.Sleep(2 * time.Second)

	// 检查是否至少连接了一个节点
	if successCount == 0 {
		return fmt.Errorf("无法连接任何 Bootstrap 节点")
	}

	return nil
}

// WaitForBootstrap 等待 Bootstrap 连接完成
//
// 阻塞直到连接到指定数量的节点或超时
func WaitForBootstrap(ctx context.Context, h host.Host, minPeers int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 检查连接的节点数
			peerCount := len(h.Network().Peers())
			if peerCount >= minPeers {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("等待 Bootstrap 超时: 已连接 %d/%d 个节点",
				len(h.Network().Peers()), minPeers)
		}
	}
}

// SetupBootstrapDHT 配置 DHT 的 Bootstrap 节点
//
// 将 Bootstrap 配置应用到 DHT 实例
func SetupBootstrapDHT(dhtInstance interface{}, cfg *BootstrapConfig) error {
	// TODO: 实现 DHT Bootstrap 配置
	// 这需要访问 DHT 的 Bootstrap 方法，具体实现取决于使用的 DHT 库
	return nil
}

// BootstrapPeersFromStrings 从字符串列表创建 Bootstrap 节点
//
// 支持格式:
//   - /ip4/1.2.3.4/tcp/4001/p2p/QmPeerID
//   - /dns4/node.example.com/tcp/4001/p2p/QmPeerID
func BootstrapPeersFromStrings(addrs []string) ([]peer.AddrInfo, error) {
	var peers []peer.AddrInfo

	for _, addr := range addrs {
		pi, err := peer.AddrInfoFromString(addr)
		if err != nil {
			return nil, fmt.Errorf("解析 Bootstrap 地址失败 [%s]: %w", addr, err)
		}
		peers = append(peers, pi)
	}

	return peers, nil
}

// IsBootstrapConnected 检查是否已连接到 Bootstrap 节点
func IsBootstrapConnected(h host.Host, cfg *BootstrapConfig) bool {
	if cfg == nil {
		return true
	}

	peers := h.Network().Peers()
	for _, bp := range cfg.Peers {
		for _, p := range peers {
			if p == bp.ID {
				// 检查连接状态
				conns := h.Network().ConnsToPeer(p)
				if len(conns) > 0 {
					return true
				}
			}
		}
	}

	return false
}

// GetConnectionInfo 获取连接信息
func GetConnectionInfo(h host.Host, target peer.ID) []*network.ConnInfo {
	conns := h.Network().ConnsToPeer(target)
	infos := make([]*network.ConnInfo, 0, len(conns))

	for _, conn := range conns {
		infos = append(infos, conn.Stat())
	}

	return infos
}
