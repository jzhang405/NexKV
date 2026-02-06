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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDHTDiscovery_New 测试 DHT 创建
func TestDHTDiscovery_New(t *testing.T) {
	// Given: Host
	ctx := context.Background()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	// When: 创建 DHTDiscovery
	dd, err := NewDHTDiscovery(h, "nexkv-cluster")

	// Then: 应成功创建
	require.NoError(t, err)
	assert.NotNil(t, dd)
	assert.NotNil(t, dd.dht)
	assert.Equal(t, "nexkv-cluster", dd.namespace)
}

// TestDHTDiscovery_EmptyNamespace 测试空命名空间
func TestDHTDiscovery_EmptyNamespace(t *testing.T) {
	// Given: Host 和空命名空间
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	// When: 使用空命名空间
	dd, err := NewDHTDiscovery(h, "")

	// Then: 应返回错误
	assert.Error(t, err)
	assert.Nil(t, dd)
	assert.Contains(t, err.Error(), "命名空间不能为空")
}

// TestDHTDiscovery_Advertise 测试公布地址
func TestDHTDiscovery_Advertise(t *testing.T) {
	// Given: DHTDiscovery
	ctx := context.Background()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	dd, err := NewDHTDiscovery(h, "nexkv-cluster")
	require.NoError(t, err)

	// When: 公布地址
	err = dd.Advertise(ctx)

	// Then: 应成功公布
	assert.NoError(t, err)
}

// TestDHTDiscovery_FindPeers 测试查找节点
func TestDHTDiscovery_FindPeers(t *testing.T) {
	// Given: DHTDiscovery
	ctx := context.Background()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	dd, err := NewDHTDiscovery(h, "nexkv-cluster")
	require.NoError(t, err)

	// When: 查找节点
	peerChan := dd.FindPeers(ctx)

	// Then: 应返回 channel
	assert.NotNil(t, peerChan)
}

// TestDHTDiscovery_FindPeers_Cancelled 测试取消查找
func TestDHTDiscovery_FindPeers_Cancelled(t *testing.T) {
	// Given: DHTDiscovery
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	dd, err := NewDHTDiscovery(h, "nexkv-cluster")
	require.NoError(t, err)

	// When: 立即取消
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	peerChan := dd.FindPeers(ctx)

	// Then: channel 应立即关闭
	_, ok := <-peerChan
	assert.False(t, ok, "channel 应已关闭")
}

// TestDHTDiscovery_RefreshLoop 测试定期刷新
func TestDHTDiscovery_RefreshLoop(t *testing.T) {
	// Given: DHTDiscovery
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	dd, err := NewDHTDiscovery(h, "nexkv-cluster")
	require.NoError(t, err)

	// When: 启动刷新循环
	advertiseCount := 0
	originalAdvertise := dd.Advertise
	dd.Advertise = func(ctx context.Context) error {
		advertiseCount++
		return originalAdvertise(ctx)
	}

	go dd.StartRefreshLoop(ctx, 500*time.Millisecond)

	// Then: 应定期刷新
	<-ctx.Done()
	assert.GreaterOrEqual(t, advertiseCount, 2, "应至少刷新 2 次")
}
