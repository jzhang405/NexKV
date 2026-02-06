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
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNodeDiscovery_Bootstrap 测试 Bootstrap 连接
func TestNodeDiscovery_Bootstrap(t *testing.T) {
	// Given: Bootstrap 配置
	ctx := context.Background()
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4001"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4002"))
	require.NoError(t, err)
	defer h2.Close()

	cfg := &BootstrapConfig{
		Peers: []peer.AddrInfo{h2.Peerstore().PeerInfo(h2.ID())},
	}

	// When: 连接 Bootstrap 节点
	err = ConnectToBootstrap(ctx, h1, cfg)

	// Then: 应成功连接
	assert.NoError(t, err)

	// And: 节点 1 应看到节点 2
	peers := h1.Network().Peers()
	assert.Contains(t, peers, h2.ID())
}

// TestNodeDiscovery_WaitForBootstrap 测试等待 Bootstrap
func TestNodeDiscovery_WaitForBootstrap(t *testing.T) {
	// Given: 两个节点
	ctx := context.Background()
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4003"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4004"))
	require.NoError(t, err)
	defer h2.Close()

	cfg := &BootstrapConfig{
		Peers: []peer.AddrInfo{h2.Peerstore().PeerInfo(h2.ID())},
	}

	// When: 连接 Bootstrap 并等待
	go ConnectToBootstrap(ctx, h1, cfg)
	err = WaitForBootstrap(ctx, h1, 1, 5*time.Second)

	// Then: 应成功等待
	assert.NoError(t, err)
}

// TestBootstrapPeersFromStrings 测试从字符串创建 Bootstrap 节点
func TestBootstrapPeersFromStrings(t *testing.T) {
	// Given: Bootstrap 地址列表
	addrs := []string{
		"/ip4/127.0.0.1/tcp/4001/p2p/QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N",
		"/dns4/localhost/tcp/4002/p2p/QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N",
	}

	// When: 解析地址
	peers, err := BootstrapPeersFromStrings(addrs)

	// Then: 应成功解析
	assert.NoError(t, err)
	assert.Len(t, peers, 2)
}

// TestBootstrapPeersFromStrings_Invalid 测试无效地址
func TestBootstrapPeersFromStrings_Invalid(t *testing.T) {
	// Given: 无效地址
	addrs := []string{
		"invalid-address",
	}

	// When: 解析地址
	peers, err := BootstrapPeersFromStrings(addrs)

	// Then: 应返回错误
	assert.Error(t, err)
	assert.Nil(t, peers)
}

// TestIsBootstrapConnected 测试检查 Bootstrap 连接状态
func TestIsBootstrapConnected(t *testing.T) {
	// Given: 两个连接的节点
	ctx := context.Background()
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4005"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4006"))
	require.NoError(t, err)
	defer h2.Close()

	cfg := &BootstrapConfig{
		Peers: []peer.AddrInfo{h2.Peerstore().PeerInfo(h2.ID())},
	}

	err = ConnectToBootstrap(ctx, h1, cfg)
	require.NoError(t, err)

	// When: 检查连接状态
	connected := IsBootstrapConnected(h1, cfg)

	// Then: 应已连接
	assert.True(t, connected)
}

// TestIsBootstrapConnected_NotConnected 测试未连接状态
func TestIsBootstrapConnected_NotConnected(t *testing.T) {
	// Given: 两个未连接的节点
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4007"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4008"))
	require.NoError(t, err)
	defer h2.Close()

	cfg := &BootstrapConfig{
		Peers: []peer.AddrInfo{h2.Peerstore().PeerInfo(h2.ID())},
	}

	// When: 检查连接状态
	connected := IsBootstrapConnected(h1, cfg)

	// Then: 应未连接
	assert.False(t, connected)
}
