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

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/multiformats/go-multiaddr"
)

// P2PService 完整的 P2P 服务（统一入口）
// 整合了 Host、Protocol、Discovery 等组件
type P2PService struct {
	host      host.Host
	protocol  *NexKVProtocol
	discovery *DiscoveryService
	codec     MessageCodec
	keyPath   string
	started   bool
}

// P2PServiceConfig P2P 服务配置
type P2PServiceConfig struct {
	// ListenAddr 监听地址（如 "0.0.0.0:9211"）
	ListenAddr string
	// KeyPath 密钥文件路径
	KeyPath string
	// LowWater 连接管理器低水位
	LowWater int
	// HighWater 连接管理器高水位
	HighWater int
	// DiscoveryTag mDNS 发现服务标签
	DiscoveryTag string
	// BootstrapPeers 启动时连接的引导节点
	BootstrapPeers []peer.AddrInfo
}

// DefaultP2PServiceConfig 返回默认配置
func DefaultP2PServiceConfig(listenAddr string, keyPath string) *P2PServiceConfig {
	return &P2PServiceConfig{
		ListenAddr:    listenAddr,
		KeyPath:       keyPath,
		LowWater:      100,
		HighWater:     400,
		DiscoveryTag:  "nexkv-discovery",
		BootstrapPeers: nil,
	}
}

// NewP2PService 创建 P2P 服务
func NewP2PService(cfg *P2PServiceConfig) (*P2PService, error) {
	if cfg == nil {
		return nil, fmt.Errorf("配置不能为空")
	}

	// 1. 密钥管理（复用 PR-001）
	km := NewKeyManager(cfg.KeyPath)
	// 展开路径
	cfg.KeyPath = km.ExpandPath(cfg.KeyPath)

	privKey, err := km.LoadOrGenerate()
	if err != nil {
		return nil, fmt.Errorf("密钥管理失败: %w", err)
	}

	// 2. 连接管理器（复用 PR-001）
	cm, err := connmgr.NewConnManager(
		cfg.LowWater,
		cfg.HighWater,
		connmgr.WithGracePeriod(time.Minute),
	)
	if err != nil {
		return nil, fmt.Errorf("连接管理器创建失败: %w", err)
	}

	// 3. 构建监听地址
	listenAddr, err := multiaddr.NewMultiaddr(
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%s", cfg.ListenAddr),
	)
	if err != nil {
		return nil, fmt.Errorf("构建监听地址失败: %w", err)
	}

	// 4. 创建 libp2p Host（使用 DefaultTransports）
	opts := []libp2p.Option{
		libp2p.Identity(privKey),
		libp2p.ListenAddrs(listenAddr),
		libp2p.ConnectionManager(cm),
		libp2p.Ping(true),
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("创建 Host 失败: %w", err)
	}

	// 5. 创建编解码器
	codec := NewMessagePackCodec()

	// 6. 创建协议处理器
	protocol := NewNexKVProtocol(h, codec)

	// 7. 创建发现服务（复用 PR-003）
	discovery := NewDiscoveryService(h, cfg.DiscoveryTag, nil)

	return &P2PService{
		host:      h,
		protocol:  protocol,
		discovery: discovery,
		codec:     codec,
		keyPath:   cfg.KeyPath,
		started:   false,
	}, nil
}

// Start 启动 P2P 服务
func (s *P2PService) Start(ctx context.Context) error {
	if s.started {
		return fmt.Errorf("服务已启动")
	}

	// 启动发现服务
	if err := s.discovery.Start(ctx); err != nil {
		return fmt.Errorf("启动发现服务失败: %w", err)
	}

	s.started = true
	return nil
}

// Stop 停止 P2P 服务
func (s *P2PService) Stop() error {
	if !s.started {
		return nil
	}

	// 停止发现服务
	s.discovery.Close()

	// 关闭协议处理器
	s.protocol.Close()

	// 关闭 Host（libp2p 自动清理所有连接和 Stream）
	if err := s.host.Close(); err != nil {
		return fmt.Errorf("关闭 Host 失败: %w", err)
	}

	s.started = false
	return nil
}

// Protocol 返回协议处理器
func (s *P2PService) Protocol() *NexKVProtocol {
	return s.protocol
}

// Host 返回 libp2p Host（用于高级操作）
func (s *P2PService) Host() host.Host {
	return s.host
}

// Codec 返回编解码器
func (s *P2PService) Codec() MessageCodec {
	return s.codec
}

// PeerID 返回节点 ID
func (s *P2PService) PeerID() peer.ID {
	return s.host.ID()
}

// Addrs 返回节点监听地址列表
func (s *P2PService) Addrs() []multiaddr.Multiaddr {
	return s.host.Addrs()
}

// ConnectToPeer 连接到指定节点
func (s *P2PService) ConnectToPeer(ctx context.Context, pi peer.AddrInfo) error {
	return s.host.Connect(ctx, pi)
}

// Close 关闭服务（同 Stop）
func (s *P2PService) Close() error {
	return s.Stop()
}

// IsStarted 返回服务是否已启动
func (s *P2PService) IsStarted() bool {
	return s.started
}

// GetListenAddrs 获取格式化的监听地址列表
func (s *P2PService) GetListenAddrs() []string {
	addrs := s.host.Addrs()
	result := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		result = append(result, addr.String())
	}
	return result
}

// GetPeerInfo 获取节点信息
func (s *P2PService) GetPeerInfo() peer.AddrInfo {
	return peer.AddrInfo{
		ID:    s.host.ID(),
		Addrs: s.host.Addrs(),
	}
}
