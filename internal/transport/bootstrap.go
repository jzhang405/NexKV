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
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
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
		return nil
	}

	successCount := connectToAllPeers(ctx, h, cfg.Peers)
	if successCount == 0 {
		return fmt.Errorf("无法连接任何 Bootstrap 节点")
	}

	return nil
}

// connectToAllPeers 并行连接所有节点，返回成功连接数
func connectToAllPeers(ctx context.Context, h host.Host, peers []peer.AddrInfo) int32 {
	var successCount int32
	var wg sync.WaitGroup

	for _, p := range peers {
		wg.Add(1)
		go func(pi peer.AddrInfo) {
			defer wg.Done()
			if connectToPeerWithTimeout(ctx, h, pi) {
				atomic.AddInt32(&successCount, 1)
			}
		}(p)
	}

	wg.Wait()
	return successCount
}

// connectToPeerWithTimeout 连接到单个节点，返回是否成功
func connectToPeerWithTimeout(ctx context.Context, h host.Host, pi peer.AddrInfo) bool {
	connectCtx, cancel := context.WithTimeout(ctx, BootstrapConnectTimeout)
	defer cancel()

	err := h.Connect(connectCtx, pi)
	return err == nil
}

// WaitForBootstrap 等待 Bootstrap 连接完成
//
// 阻塞直到连接到指定数量的节点或超时
func WaitForBootstrap(ctx context.Context, h host.Host, minPeers int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(BootstrapCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if hasEnoughPeers(h, minPeers) {
				return nil
			}
		case <-ctx.Done():
			currentPeers := len(h.Network().Peers())
			return fmt.Errorf("等待 Bootstrap 超时: 已连接 %d/%d 个节点", currentPeers, minPeers)
		}
	}
}

// hasEnoughPeers 检查是否已连接足够的节点
func hasEnoughPeers(h host.Host, minPeers int) bool {
	return len(h.Network().Peers()) >= minPeers
}

// BootstrapPeersFromStrings 从字符串列表创建 Bootstrap 节点
//
// 支持格式:
//   - /ip4/1.2.3.4/tcp/4001/p2p/QmPeerID
//   - /dns4/node.example.com/tcp/4001/p2p/QmPeerID
func BootstrapPeersFromStrings(addrs []string) ([]peer.AddrInfo, error) {
	return parsePeersFromStrings(addrs)
}

// IsBootstrapConnected 检查是否已连接到 Bootstrap 节点
func IsBootstrapConnected(h host.Host, cfg *BootstrapConfig) bool {
	if cfg == nil {
		return true
	}

	return isAnyBootstrapPeerConnected(h, cfg.Peers)
}

// isAnyBootstrapPeerConnected 检查是否有任何 Bootstrap 节点已连接
func isAnyBootstrapPeerConnected(h host.Host, bootstrapPeers []peer.AddrInfo) bool {
	connectedPeers := h.Network().Peers()

	for _, bp := range bootstrapPeers {
		if isPeerConnected(h, connectedPeers, bp.ID) {
			return true
		}
	}

	return false
}

// isPeerConnected 检查特定节点是否已连接
func isPeerConnected(h host.Host, connectedPeers []peer.ID, target peer.ID) bool {
	for _, p := range connectedPeers {
		if p == target && len(h.Network().ConnsToPeer(p)) > 0 {
			return true
		}
	}
	return false
}

// GetConnectionStats 获取连接统计信息
func GetConnectionStats(h host.Host, target peer.ID) int {
	return len(h.Network().ConnsToPeer(target))
}
