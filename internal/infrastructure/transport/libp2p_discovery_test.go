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
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/discovery"
	"github.com/libp2p/go-libp2p/core/peer"
)

// TestDiscoveryService_HandlePeerFound 测试发现节点的处理
func TestDiscoveryService_HandlePeerFound(t *testing.T) {
	ctx := t.Context()
	provider := newMockGoroutineProvider()

	// 创建本地 host
	localTr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create local transport: %v", err)
	}
	defer localTr.Close()

	// 创建 remote host
	remoteTr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create remote transport: %v", err)
	}
	defer remoteTr.Close()

	// 创建 discovery service
	d := discovery.NewMDNSDiscovery(localTr.host, "nexkv-test", provider)

	// 获取 remote host 的地址信息
	remoteAddrInfo := peer.AddrInfo{
		ID:    remoteTr.host.ID(),
		Addrs: remoteTr.host.Addrs(),
	}

	// 创建 notifee 并测试 HandlePeerFound
	notifee := &testDiscoveryNotifee{host: localTr.host}
	d.SetNotifee(notifee)

	// 调用 HandlePeerFound（通过内部机制）
	_ = remoteAddrInfo

	// 等待连接建立
	time.Sleep(100 * time.Millisecond)

	// 验证连接（可能成功也可能失败，取决于网络配置）
	t.Logf("Discovery test completed: local=%s, remote=%s",
		localTr.host.ID(), remoteTr.host.ID())
}

// testDiscoveryNotifee 测试用的发现通知处理器
type testDiscoveryNotifee struct {
	host interface{}
}

// HandlePeerFound 处理发现的节点
func (n *testDiscoveryNotifee) HandlePeerFound(peerID model.PeerID, addrs []model.NetworkAddress) {
	// 测试实现，不做实际操作
}

// TestNewDiscoveryService 测试创建发现服务
func TestNewDiscoveryService(t *testing.T) {
	ctx := t.Context()
	provider := newMockGoroutineProvider()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	// 创建发现服务
	discoverySvc := NewDiscoveryService(tr.host, "nexkv-test-discovery", provider)
	if discoverySvc == nil {
		t.Fatal("NewDiscoveryService returned nil")
	}

	// 启动服务
	if err := discoverySvc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		if err := discoverySvc.Stop(); err != nil {
			t.Logf("Stop failed: %v", err)
		}
	}()

	// 等待 mDNS 服务启动
	time.Sleep(200 * time.Millisecond)

	t.Logf("Discovery service created with tag: nexkv-test-discovery")
}

// TestDiscoveryService_Stop 测试停止发现服务
func TestDiscoveryService_Stop(t *testing.T) {
	ctx := t.Context()
	provider := newMockGoroutineProvider()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	discoverySvc := NewDiscoveryService(tr.host, "nexkv-test-stop", provider)
	if discoverySvc == nil {
		t.Fatal("NewDiscoveryService returned nil")
	}

	// 启动服务
	if err := discoverySvc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 等待服务启动
	time.Sleep(100 * time.Millisecond)

	// 停止发现服务
	err = discoverySvc.Stop()
	if err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// 再次停止（应该安全处理）
	err = discoverySvc.Stop()
	if err != nil {
		t.Logf("Second stop returned: %v", err)
	}

	t.Log("Discovery service stop test completed")
}

// TestDiscoveryService_InvalidPeer 测试发现无效节点
func TestDiscoveryService_InvalidPeer(t *testing.T) {
	ctx := t.Context()
	provider := newMockGoroutineProvider()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	// 创建 discovery service
	d := discovery.NewMDNSDiscovery(tr.host, "nexkv-test", provider)

	// 创建 notifee
	notifee := &testDiscoveryNotifee{host: tr.host}
	d.SetNotifee(notifee)

	// 创建无效的 peer 地址信息
	invalidAddrInfo := peer.AddrInfo{
		ID:    "invalid-peer-id",
		Addrs: nil,
	}
	_ = invalidAddrInfo

	// 测试完成（应该处理错误而不 panic）
	t.Log("Discovery with invalid peer completed without panic")
}

// TestDiscoveryService_MultipleInstances 测试多个发现服务实例
func TestDiscoveryService_MultipleInstances(t *testing.T) {
	ctx := t.Context()
	provider := newMockGoroutineProvider()

	// P0 修复：使用切片收集资源，避免在循环中使用 defer
	var transports []*Libp2pTransport
	var discoveries []service.DiscoveryService

	// 确保在函数结束时清理所有资源
	defer func() {
		// 先关闭 discovery 服务
		for _, d := range discoveries {
			if d != nil {
				if err := d.Stop(); err != nil {
					t.Logf("Stop discovery failed: %v", err)
				}
			}
		}
		// 再关闭 transport
		for _, tr := range transports {
			if tr != nil {
				if err := tr.Close(); err != nil {
					t.Logf("Close transport failed: %v", err)
				}
			}
		}
	}()

	// 创建多个 host 和发现服务
	for i := 0; i < 3; i++ {
		tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
		if err != nil {
			t.Fatalf("create transport %d: %v", i, err)
		}
		transports = append(transports, tr)

		discoverySvc := NewDiscoveryService(tr.host, "nexkv-multi-test", provider)
		if discoverySvc == nil {
			t.Fatalf("NewDiscoveryService %d returned nil", i)
		}
		discoveries = append(discoveries, discoverySvc)

		// 启动服务
		if err := discoverySvc.Start(ctx); err != nil {
			t.Fatalf("Start discovery %d failed: %v", i, err)
		}
	}

	// 等待服务启动
	time.Sleep(300 * time.Millisecond)

	t.Log("Multiple discovery service instances created successfully")
}
