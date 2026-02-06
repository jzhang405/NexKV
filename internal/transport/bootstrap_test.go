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

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectToBootstrap_Success 测试成功连接 Bootstrap 节点
func TestConnectToBootstrap_Success(t *testing.T) {
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

// TestConnectToBootstrap_EmptyList 测试空 Bootstrap 列表
func TestConnectToBootstrap_EmptyList(t *testing.T) {
	// Given: 空 Bootstrap 配置
	ctx := context.Background()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4001"))
	require.NoError(t, err)
	defer h.Close()

	cfg := &BootstrapConfig{
		Peers: []peer.AddrInfo{},
	}

	// When: 连接空 Bootstrap 列表
	err = ConnectToBootstrap(ctx, h, cfg)

	// Then: 应成功（无操作）
	assert.NoError(t, err)
}

// TestConnectToBootstrap_PartialFailure 测试部分连接失败
func TestConnectToBootstrap_PartialFailure(t *testing.T) {
	// Given: Bootstrap 配置，包含有效和无效节点
	ctx := context.Background()
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4001"))
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/4002"))
	require.NoError(t, err)
	defer h2.Close()

	// 创建一个无效的 PeerInfo（不存在的节点）
	invalidPeer := peer.AddrInfo{
		ID: peer.ID("QmInvalid"),
	}

	cfg := &BootstrapConfig{
		Peers: []peer.AddrInfo{
			h2.Peerstore().PeerInfo(h2.ID()),
			invalidPeer,
		},
	}

	// When: 连接 Bootstrap 节点
	err = ConnectToBootstrap(ctx, h1, cfg)

	// Then: 应成功（至少连接了一个节点）
	assert.NoError(t, err)

	// And: 节点 1 应看到节点 2
	peers := h1.Network().Peers()
	assert.Contains(t, peers, h2.ID())
}
