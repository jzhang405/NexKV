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
	"fmt"

	"github.com/jzhang405/NexKV/internal/config"
	"github.com/libp2p/go-libp2p/core/peer"
)

// NewTransportFromConfig 根据配置创建 P2P Service
//
// 仅支持 libp2p Transport
func NewTransportFromConfig(cfg *config.TransportConfig) (*P2PService, error) {
	if cfg == nil {
		return nil, fmt.Errorf("配置为空")
	}

	// 仅支持 libp2p
	if cfg.Type != "" && cfg.Type != "libp2p" {
		return nil, fmt.Errorf("不支持的 Transport 类型: %s（仅支持 libp2p）", cfg.Type)
	}

	if cfg.Libp2p == nil {
		return nil, fmt.Errorf("libp2p 配置为空")
	}

	return NewLibp2pTransportFromConfig(cfg.Libp2p)
}

// NewLibp2pTransportFromConfig 根据配置创建 libp2p Transport
func NewLibp2pTransportFromConfig(cfg *config.Libp2pConfig) (*P2PService, error) {
	if cfg == nil {
		return nil, fmt.Errorf("libp2p 配置为空")
	}

	// 构建监听地址（host:port 格式）
	listenHost := cfg.ListenAddr
	if listenHost == "" {
		listenHost = "0.0.0.0"
	}

	// 使用配置端口或默认端口
	listenPort := cfg.ListenPort
	if listenPort == 0 {
		listenPort = 4001 // 默认端口
	}

	// 组合 host:port
	listenAddr := fmt.Sprintf("%s:%d", listenHost, listenPort)

	// 创建 P2P Service 配置
	p2pCfg := &P2PServiceConfig{
		ListenAddr: listenAddr,
		KeyPath:    cfg.PrivateKeyPath,
		LowWater:   DefaultLowWater,
		HighWater:  DefaultHighWater,
	}

	// 设置连接管理器参数
	if cfg.ConnectionManager != nil {
		if cfg.ConnectionManager.LowWater > 0 {
			p2pCfg.LowWater = cfg.ConnectionManager.LowWater
		}
		if cfg.ConnectionManager.HighWater > 0 {
			p2pCfg.HighWater = cfg.ConnectionManager.HighWater
		}
	}

	// 设置发现参数
	if cfg.Discovery != nil {
		if cfg.Discovery.MDNSEnabled {
			p2pCfg.DiscoveryTag = DefaultDiscoveryTag
		}
	}

	// 转换 Bootstrap 节点（从 string multiaddr 到 peer.AddrInfo）
	if len(cfg.Bootstrap) > 0 {
		bootstrapPeers, err := parseBootstrapPeers(cfg.Bootstrap)
		if err != nil {
			return nil, fmt.Errorf("解析 bootstrap 节点失败: %w", err)
		}
		p2pCfg.BootstrapPeers = bootstrapPeers
	}

	return NewP2PService(p2pCfg)
}

// parseBootstrapPeers 解析 bootstrap 节点列表
func parseBootstrapPeers(addrs []string) ([]peer.AddrInfo, error) {
	peers := make([]peer.AddrInfo, 0, len(addrs))

	for _, addr := range addrs {
		pi, err := peer.AddrInfoFromString(addr)
		if err != nil {
			return nil, fmt.Errorf("解析节点地址失败 [%s]: %w", addr, err)
		}
		if pi != nil {
			peers = append(peers, *pi)
		}
	}

	return peers, nil
}
