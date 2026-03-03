// Package discovery 提供节点发现测试
package discovery

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// Mock TaskPoolProvider
// ==========================================

type mockDiscoveryTaskPoolProvider struct {
	tasks []func(context.Context)
	mu    sync.Mutex
}

func (m *mockDiscoveryTaskPoolProvider) Submit(ctx context.Context, sourceID model.SourceID, priority service.TaskPriority, task func(context.Context)) error {
	m.mu.Lock()
	m.tasks = append(m.tasks, task)
	m.mu.Unlock()
	go task(ctx)
	return nil
}

func (m *mockDiscoveryTaskPoolProvider) Close() error {
	return nil
}

// ==========================================
// Mock DiscoveryNotifee
// ==========================================

type mockDiscoveryNotifee struct {
	peers []struct {
		id    model.PeerID
		addrs []model.NetworkAddress
	}
	updated []struct {
		id    model.PeerID
		addrs []model.NetworkAddress
	}
	suspected []struct {
		id     model.PeerID
		reason string
	}
	lost []model.PeerID
	mu   sync.Mutex
}

func (m *mockDiscoveryNotifee) HandlePeerFound(peerID model.PeerID, addrs []model.NetworkAddress) {
	m.mu.Lock()
	m.peers = append(m.peers, struct {
		id    model.PeerID
		addrs []model.NetworkAddress
	}{id: peerID, addrs: addrs})
	m.mu.Unlock()
	// 不使用 wg.Done()，因为 mDNS 可能随时调用
	// 调用方应该使用 peers 切片的长度或其他同步机制
}

func (m *mockDiscoveryNotifee) HandlePeerUpdated(peerID model.PeerID, addrs []model.NetworkAddress) {
	m.mu.Lock()
	m.updated = append(m.updated, struct {
		id    model.PeerID
		addrs []model.NetworkAddress
	}{id: peerID, addrs: addrs})
	m.mu.Unlock()
}

func (m *mockDiscoveryNotifee) HandlePeerSuspected(peerID model.PeerID, reason string) {
	m.mu.Lock()
	m.suspected = append(m.suspected, struct {
		id     model.PeerID
		reason string
	}{id: peerID, reason: reason})
	m.mu.Unlock()
}

func (m *mockDiscoveryNotifee) HandlePeerLost(peerID model.PeerID) {
	m.mu.Lock()
	m.lost = append(m.lost, peerID)
	m.mu.Unlock()
}

// ==========================================
// MDNSDiscovery 构造函数测试
// ==========================================

func TestNewMDNSDiscovery(t *testing.T) {
	// 创建 libp2p host
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}

	discovery := NewMDNSDiscovery(h, "test-tag", provider)

	assert.NotNil(t, discovery)
	assert.Equal(t, "test-tag", discovery.tag)
	assert.Equal(t, h, discovery.host)
	assert.Equal(t, provider, discovery.provider)
}

func TestNewMDNSDiscovery_NilHost(t *testing.T) {
	provider := &mockDiscoveryTaskPoolProvider{}

	discovery := NewMDNSDiscovery(nil, "test-tag", provider)

	assert.NotNil(t, discovery)
	assert.Nil(t, discovery.host)
}

// ==========================================
// SetNotifee 测试
// ==========================================

func TestMDNSDiscovery_SetNotifee(t *testing.T) {
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "test-tag", provider)

	notifee := &mockDiscoveryNotifee{}
	discovery.SetNotifee(notifee)

	discovery.mu.RLock()
	assert.Equal(t, notifee, discovery.notifee)
	discovery.mu.RUnlock()
}

func TestMDNSDiscovery_SetNotifee_Nil(t *testing.T) {
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "test-tag", provider)

	// 设置 nil 不应该 panic
	discovery.SetNotifee(nil)

	discovery.mu.RLock()
	assert.Nil(t, discovery.notifee)
	discovery.mu.RUnlock()
}

func TestMDNSDiscovery_SetNotifee_Concurrent(t *testing.T) {
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "test-tag", provider)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			notifee := &mockDiscoveryNotifee{}
			discovery.SetNotifee(notifee)
		}()
	}
	wg.Wait()
}

// ==========================================
// Start/Stop 生命周期测试
// ==========================================

func TestMDNSDiscovery_StartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "nexkv-test-startstop", provider)

	ctx := context.Background()

	// 启动
	err = discovery.Start(ctx)
	assert.NoError(t, err)

	discovery.mu.RLock()
	started := discovery.started
	discovery.mu.RUnlock()
	assert.True(t, started)

	// 停止
	err = discovery.Stop()
	assert.NoError(t, err)

	discovery.mu.RLock()
	started = discovery.started
	discovery.mu.RUnlock()
	assert.False(t, started)
}

func TestMDNSDiscovery_StartTwice(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "nexkv-test-starttwice", provider)

	ctx := context.Background()

	// 第一次启动
	err = discovery.Start(ctx)
	require.NoError(t, err)

	// 第二次启动应该返回 nil（幂等）
	err = discovery.Start(ctx)
	assert.NoError(t, err)

	// 清理
	_ = discovery.Stop()
}

func TestMDNSDiscovery_StopWithoutStart(t *testing.T) {
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "nexkv-test-stopwithoutstart", provider)

	// 未启动就停止应该返回 nil
	err = discovery.Stop()
	assert.NoError(t, err)
}

func TestMDNSDiscovery_StopTwice(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "nexkv-test-stoptwice", provider)

	ctx := context.Background()

	// 启动
	err = discovery.Start(ctx)
	require.NoError(t, err)

	// 第一次停止
	err = discovery.Stop()
	assert.NoError(t, err)

	// 第二次停止应该返回 nil（幂等）
	err = discovery.Stop()
	assert.NoError(t, err)
}

func TestMDNSDiscovery_StartWithCanceledContext(t *testing.T) {
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "nexkv-test-canceledctx", provider)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	// 启动仍然应该成功（内部会创建新的 context）
	err = discovery.Start(ctx)
	assert.NoError(t, err)

	// 清理
	_ = discovery.Stop()
}

// ==========================================
// mdnsNotifee.HandlePeerFound 测试
// ==========================================

func TestMdnsNotifee_HandlePeerFound(t *testing.T) {
	// 创建两个 host
	h1, err := libp2p.New()
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New()
	require.NoError(t, err)
	defer h2.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h1, "nexkv-test-handlepeerfound", provider)

	notifee := &mockDiscoveryNotifee{}
	discovery.SetNotifee(notifee)

	// 创建 mdnsNotifee
	mdnsN := &mdnsNotifee{
		host:   h1,
		parent: discovery,
	}

	// 准备 peer.AddrInfo
	peerInfo := peer.AddrInfo{
		ID:    h2.ID(),
		Addrs: h2.Addrs(),
	}

	// 调用 HandlePeerFound
	mdnsN.HandlePeerFound(peerInfo)

	// 轮询等待通知完成
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			notifee.mu.Lock()
			if len(notifee.peers) > 0 {
				// 验证通知内容
				assert.Len(t, notifee.peers, 1)
				if len(notifee.peers) > 0 {
					assert.Equal(t, model.PeerID(h2.ID().String()), notifee.peers[0].id)
					assert.NotEmpty(t, notifee.peers[0].addrs)
				}
				notifee.mu.Unlock()
				return
			}
			notifee.mu.Unlock()
		case <-timeout:
			t.Fatal("timeout waiting for peer notification")
		}
	}
}

func TestMdnsNotifee_HandlePeerFound_NilNotifee(t *testing.T) {
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "nexkv-test-nilnotifee", provider)
	// 不设置 notifee

	mdnsN := &mdnsNotifee{
		host:   h,
		parent: discovery,
	}

	// 准备 peer.AddrInfo
	peerInfo := peer.AddrInfo{
		ID:    h.ID(),
		Addrs: h.Addrs(),
	}

	// 不应该 panic
	mdnsN.HandlePeerFound(peerInfo)
}

func TestMdnsNotifee_HandlePeerFound_EmptyAddrs(t *testing.T) {
	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "nexkv-test-emptyaddrs", provider)

	notifee := &mockDiscoveryNotifee{}
	discovery.SetNotifee(notifee)

	mdnsN := &mdnsNotifee{
		host:   h,
		parent: discovery,
	}

	// 准备空的 peer.AddrInfo
	peerInfo := peer.AddrInfo{
		ID:    h.ID(),
		Addrs: []multiaddr.Multiaddr{},
	}

	// 调用 HandlePeerFound
	mdnsN.HandlePeerFound(peerInfo)

	// 轮询等待通知完成
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			notifee.mu.Lock()
			if len(notifee.peers) > 0 {
				// 验证通知内容（应该有空的 addrs）
				assert.Len(t, notifee.peers, 1)
				if len(notifee.peers) > 0 {
					assert.Equal(t, model.PeerID(h.ID().String()), notifee.peers[0].id)
					assert.Empty(t, notifee.peers[0].addrs)
				}
				notifee.mu.Unlock()
				return
			}
			notifee.mu.Unlock()
		case <-timeout:
			t.Fatal("timeout waiting for peer notification")
		}
	}
}

// ==========================================
// 并发测试
// ==========================================

func TestMDNSDiscovery_ConcurrentStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	h, err := libp2p.New()
	require.NoError(t, err)
	defer h.Close()

	provider := &mockDiscoveryTaskPoolProvider{}
	discovery := NewMDNSDiscovery(h, "nexkv-test-concurrent", provider)

	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = discovery.Start(ctx)
		}()
		go func() {
			defer wg.Done()
			_ = discovery.Stop()
		}()
	}
	wg.Wait()

	// 最终清理
	_ = discovery.Stop()
}

// ==========================================
// 集成测试（需要 mDNS 多播）
// ==========================================

func TestMDNSDiscovery_Integration_TwoNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// 创建两个 host
	h1, err := libp2p.New()
	require.NoError(t, err)
	defer h1.Close()

	h2, err := libp2p.New()
	require.NoError(t, err)
	defer h2.Close()

	provider := &mockDiscoveryTaskPoolProvider{}

	// 创建 discovery 服务
	d1 := NewMDNSDiscovery(h1, "nexkv-integration-test", provider)
	notifee1 := &mockDiscoveryNotifee{}
	d1.SetNotifee(notifee1)

	d2 := NewMDNSDiscovery(h2, "nexkv-integration-test", provider)
	notifee2 := &mockDiscoveryNotifee{}
	d2.SetNotifee(notifee2)

	ctx := context.Background()

	// 启动两个 discovery 服务
	err = d1.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = d1.Stop() }()

	err = d2.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = d2.Stop() }()

	// 等待发现（mDNS 可能需要几秒钟）
	time.Sleep(3 * time.Second)

	t.Logf("Node1 discovered %d peers", len(notifee1.peers))
	t.Logf("Node2 discovered %d peers", len(notifee2.peers))

	t.Log("Integration test completed")
}
