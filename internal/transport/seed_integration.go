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

	"github.com/jzhang405/NexKV/internal/metadata/config"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// SeedNodeIntegration 种子节点集成
// 将现有的种子节点配置与 libp2p Bootstrap 集成
type SeedNodeIntegration struct {
	host          host.Host
	seedWatcher   *config.SeedNodesWatcher
	onSeedsChange func([]peer.AddrInfo)
}

// NewSeedNodeIntegration 创建种子节点集成
//
// 参数:
//   - h: libp2p Host 实例
//   - configPath: 配置文件路径
//   - onSeedsChange: 种子节点变化回调
//
// 返回:
//   - *SeedNodeIntegration: 集成实例
//   - error: 创建失败时返回错误
func NewSeedNodeIntegration(
	h host.Host,
	configPath string,
	onSeedsChange func([]peer.AddrInfo),
) (*SeedNodeIntegration, error) {
	if h == nil {
		return nil, fmt.Errorf("host 不能为空")
	}

	// 创建种子节点监控器
	watcher, err := config.NewSeedNodesWatcher(configPath, func(seeds []string) {
		// 将种子节点字符串转换为 peer.AddrInfo
		peers := seedNodesToPeers(seeds)
		if onSeedsChange != nil {
			onSeedsChange(peers)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("创建种子节点监控器失败: %w", err)
	}

	return &SeedNodeIntegration{
		host:          h,
		seedWatcher:   watcher,
		onSeedsChange: onSeedsChange,
	}, nil
}

// Start 启动种子节点集成
func (si *SeedNodeIntegration) Start() error {
	return si.seedWatcher.Start()
}

// Stop 停止种子节点集成
func (si *SeedNodeIntegration) Stop() {
	si.seedWatcher.Stop()
}

// GetBootstrapConfig 获取当前 Bootstrap 配置
func (si *SeedNodeIntegration) GetBootstrapConfig() (*BootstrapConfig, error) {
	seeds := si.seedWatcher.GetSeedNodes()
	peers := seedNodesToPeers(seeds)

	return &BootstrapConfig{
		Peers: peers,
	}, nil
}

// ConnectToSeeds 连接到种子节点
func (si *SeedNodeIntegration) ConnectToSeeds(ctx context.Context) error {
	cfg, err := si.GetBootstrapConfig()
	if err != nil {
		return err
	}

	return ConnectToBootstrap(ctx, si.host, cfg)
}

// seedNodesToPeers 将种子节点字符串列表转换为 peer.AddrInfo
func seedNodesToPeers(seeds []string) []peer.AddrInfo {
	peers, _ := parsePeersFromStrings(seeds)
	return peers
}

// ExtractSeedNodesFromConfig 从配置文件提取种子节点
//
// 这是一个便捷函数，用于一次性读取配置文件并提取种子节点
func ExtractSeedNodesFromConfig(configPath string) ([]string, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("加载配置文件失败: %w", err)
	}

	// 从三级配置结构中提取所有 Host 的 seed_node
	seedNodeList := make([]string, 0, len(cfg.Cluster.Hosts))
	for _, host := range cfg.Cluster.Hosts {
		if host.SeedNode != "" {
			seedNodeList = append(seedNodeList, host.SeedNode)
		}
	}

	// 解析种子节点（包含去重和规范化）
	nodes, err := config.ParseSeedNodes(seedNodeList)
	if err != nil {
		return nil, fmt.Errorf("解析种子节点失败: %w", err)
	}

	return nodes, nil
}

// CreateBootstrapConfigFromSeeds 从种子节点列表创建 Bootstrap 配置
//
// 这是一个便捷函数，用于快速创建 Bootstrap 配置
func CreateBootstrapConfigFromSeeds(seeds []string) (*BootstrapConfig, error) {
	peers := seedNodesToPeers(seeds)

	return &BootstrapConfig{
		Peers: peers,
	}, nil
}

// UpdateBootstrapConfig 动态更新 Bootstrap 配置
//
// 当检测到种子节点配置变化时调用此方法
func UpdateBootstrapConfig(
	ctx context.Context,
	h host.Host,
	newSeeds []string,
) error {
	cfg, err := CreateBootstrapConfigFromSeeds(newSeeds)
	if err != nil {
		return err
	}

	return ConnectToBootstrap(ctx, h, cfg)
}
