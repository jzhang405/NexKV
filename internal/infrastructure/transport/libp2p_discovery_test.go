// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// TestDiscoveryNotifee_HandlePeerFound 测试发现节点的处理
func TestDiscoveryNotifee_HandlePeerFound(t *testing.T) {
	ctx := t.Context()

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

	// 创建 notifee
	notifee := &discoveryNotifee{host: localTr.host}

	// 获取 remote host 的地址信息
	remoteAddrInfo := peer.AddrInfo{
		ID:    remoteTr.host.ID(),
		Addrs: remoteTr.host.Addrs(),
	}

	// 调用 HandlePeerFound
	notifee.HandlePeerFound(remoteAddrInfo)

	// 等待连接建立
	time.Sleep(100 * time.Millisecond)

	// 验证连接（可能成功也可能失败，取决于网络配置）
	t.Logf("HandlePeerFound test completed: local=%s, remote=%s",
		localTr.host.ID(), remoteTr.host.ID())
}

// TestNewDiscoveryService 测试创建发现服务
func TestNewDiscoveryService(t *testing.T) {
	ctx := t.Context()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	var wg sync.WaitGroup

	// 创建发现服务
	discovery := NewDiscoveryService(tr.host, "nexkv-test-discovery", ctx, &wg)
	if discovery == nil {
		t.Fatal("NewDiscoveryService returned nil")
	}
	defer discovery.Close()

	// 等待 mDNS 服务启动
	time.Sleep(200 * time.Millisecond)

	t.Logf("Discovery service created with tag: nexkv-test-discovery")
}

// TestDiscoveryService_Close 测试关闭发现服务
func TestDiscoveryService_Close(t *testing.T) {
	ctx := t.Context()
	var wg sync.WaitGroup

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	discovery := NewDiscoveryService(tr.host, "nexkv-test-close", ctx, &wg)
	if discovery == nil {
		t.Fatal("NewDiscoveryService returned nil")
	}

	// 等待服务启动
	time.Sleep(100 * time.Millisecond)

	// 关闭发现服务
	err = discovery.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// 再次关闭（应该安全处理）
	err = discovery.Close()
	if err != nil {
		t.Logf("Second close returned: %v", err)
	}

	// 等待所有 goroutine 完成
	wg.Wait()

	t.Log("Discovery service close test completed")
}

// TestDiscoveryNotifee_HandlePeerFound_InvalidPeer 测试发现无效节点
func TestDiscoveryNotifee_HandlePeerFound_InvalidPeer(t *testing.T) {
	ctx := t.Context()

	tr, err := NewLibp2pTransport(ctx, &Config{EnableDiscovery: false})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer tr.Close()

	notifee := &discoveryNotifee{host: tr.host}

	// 创建无效的 peer 地址信息
	invalidAddrInfo := peer.AddrInfo{
		ID:    "invalid-peer-id",
		Addrs: nil,
	}

	// 调用 HandlePeerFound（应该处理错误而不 panic）
	notifee.HandlePeerFound(invalidAddrInfo)

	t.Log("HandlePeerFound with invalid peer completed without panic")
}

// TestDiscoveryService_MultipleInstances 测试多个发现服务实例
func TestDiscoveryService_MultipleInstances(t *testing.T) {
	ctx := t.Context()

	var wg sync.WaitGroup

	// P0 修复：使用切片收集资源，避免在循环中使用 defer
	var transports []*Libp2pTransport
	var discoveries []*DiscoveryService

	// 确保在函数结束时清理所有资源
	defer func() {
		// 先关闭 discovery 服务
		for _, d := range discoveries {
			if d != nil {
				d.Close()
			}
		}
		// 再关闭 transport
		for _, tr := range transports {
			if tr != nil {
				tr.Close()
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

		discovery := NewDiscoveryService(tr.host, "nexkv-multi-test", ctx, &wg)
		if discovery == nil {
			t.Fatalf("NewDiscoveryService %d returned nil", i)
		}
		discoveries = append(discoveries, discovery)
	}

	// 等待服务启动
	time.Sleep(300 * time.Millisecond)

	t.Log("Multiple discovery service instances created successfully")
}
