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
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiscoveryService_New 测试创建 mDNS 发现服务
func TestDiscoveryService_New(t *testing.T) {
	// Given: Host
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	// When: 创建 DiscoveryService
	ds := NewDiscoveryService(h, "nexkv-discovery", nil)

	// Then: 应成功创建
	assert.NotNil(t, ds)
	assert.Equal(t, h, ds.host)
	assert.Equal(t, "nexkv-discovery", ds.serviceTag)
}

// TestDiscoveryService_Start 测试 mDNS 服务启动
func TestDiscoveryService_Start(t *testing.T) {
	// Given: 创建 Host 和 DiscoveryService
	ctx := context.Background()
	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h1.Close()

	discovered := make(chan peer.AddrInfo, 10)
	ds := NewDiscoveryService(h1, "nexkv-discovery", func(pi peer.AddrInfo) {
		discovered <- pi
	})

	// When: 启动服务
	err = ds.Start(ctx)

	// Then: 应成功启动
	assert.NoError(t, err)
	assert.NotNil(t, ds.service)

	// Cleanup
	ds.service.Close()
}

// TestDiscoveryService_HandlePeerFound 测试发现节点处理
func TestDiscoveryService_HandlePeerFound(t *testing.T) {
	// Given: DiscoveryService
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	discovered := make(chan peer.AddrInfo, 10)
	ds := NewDiscoveryService(h, "nexkv-discovery", func(pi peer.AddrInfo) {
		discovered <- pi
	})

	// When: 处理发现的节点
	pid := peer.ID("QmExample")
	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	pi := peer.AddrInfo{ID: pid, Addrs: []multiaddr.Multiaddr{ma}}

	ds.HandlePeerFound(pi)

	// Then: 应触发回调
	select {
	case result := <-discovered:
		assert.Equal(t, pid, result.ID)
	case <-time.After(time.Second):
		t.Fatal("未接收到发现的节点")
	}
}

// TestDiscoveryService_SkipSelf 测试过滤自己
func TestDiscoveryService_SkipSelf(t *testing.T) {
	// Given: DiscoveryService
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	callCount := 0
	ds := NewDiscoveryService(h, "nexkv-discovery", func(pi peer.AddrInfo) {
		callCount++
	})

	// When: 处理自己的 PeerInfo
	pi := h.Peerstore().PeerInfo(h.ID())
	ds.HandlePeerFound(pi)

	// Then: 不应触发回调
	assert.Equal(t, 0, callCount, "应过滤自己")
}
