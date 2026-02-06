package transport

import (
	"sync"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper function to generate test PeerID
func generateTestPeerID(t *testing.T) peer.ID {
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	require.NoError(t, err)
	pid, err := peer.IDFromPrivateKey(privKey)
	require.NoError(t, err)
	return pid
}

// TestPeerInfoManager_New 测试创建 PeerInfoManager
func TestPeerInfoManager_New(t *testing.T) {
	// When: 创建 PeerInfoManager
	pm := NewPeerInfoManager()

	// Then: 应成功创建
	assert.NotNil(t, pm)
	assert.NotNil(t, pm.peerInfos)
}

// TestPeerInfoManager_Add 测试添加 PeerInfo
func TestPeerInfoManager_Add(t *testing.T) {
	// Given: PeerInfoManager
	pm := NewPeerInfoManager()
	pid := generateTestPeerID(t)

	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	pi := &peer.AddrInfo{
		ID:    pid,
		Addrs: []multiaddr.Multiaddr{ma},
	}

	// When: 添加PeerInfo
	pm.Add(pi)

	// Then: 应成功添加
	result, ok := pm.Get(pid)
	assert.True(t, ok)
	assert.Equal(t, pid, result.ID)
	assert.Len(t, result.Addrs, 1)
}

// TestPeerInfoManager_Add_Overwrite 测试覆盖已有 PeerInfo
func TestPeerInfoManager_Add_Overwrite(t *testing.T) {
	// Given: PeerInfoManager 和已添加的 PeerInfo
	pm := NewPeerInfoManager()
	pid := generateTestPeerID(t)

	ma1, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	pi1 := &peer.AddrInfo{
		ID:    pid,
		Addrs: []multiaddr.Multiaddr{ma1},
	}
	pm.Add(pi1)

	// When: 添加新的 PeerInfo（相同 ID）
	ma2, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.2/tcp/4001")
	pi2 := &peer.AddrInfo{
		ID:    pid,
		Addrs: []multiaddr.Multiaddr{ma2},
	}
	pm.Add(pi2)

	// Then: 应覆盖旧的 PeerInfo
	result, ok := pm.Get(pid)
	assert.True(t, ok)
	assert.Equal(t, 1, len(result.Addrs))
	assert.Equal(t, "/ip4/192.168.1.2/tcp/4001", result.Addrs[0].String())
}

// TestPeerInfoManager_Get 测试获取 PeerInfo
func TestPeerInfoManager_Get(t *testing.T) {
	// Given: PeerInfoManager 和已添加的 PeerInfo
	pm := NewPeerInfoManager()
	pid := generateTestPeerID(t)

	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	pi := &peer.AddrInfo{
		ID:    pid,
		Addrs: []multiaddr.Multiaddr{ma},
	}
	pm.Add(pi)

	// When: 获取 PeerInfo
	result, ok := pm.Get(pid)

	// Then: 应成功获取
	assert.True(t, ok)
	assert.Equal(t, pid, result.ID)
	assert.NotEmpty(t, result.Addrs)
}

// TestPeerInfoManager_Get_NotFound 测试获取不存在的 PeerInfo
func TestPeerInfoManager_Get_NotFound(t *testing.T) {
	// Given: PeerInfoManager
	pm := NewPeerInfoManager()
	pid := generateTestPeerID(t)

	// When: 获取不存在的 PeerInfo
	result, ok := pm.Get(pid)

	// Then: 应返回 false
	assert.False(t, ok)
	assert.Nil(t, result)
}

// TestPeerInfoManager_Remove 测试删除 PeerInfo
func TestPeerInfoManager_Remove(t *testing.T) {
	// Given: PeerInfoManager和已添加的PeerInfo
	pm := NewPeerInfoManager()
	pid := generateTestPeerID(t)

	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	pi := &peer.AddrInfo{
		ID:    pid,
		Addrs: []multiaddr.Multiaddr{ma},
	}
	pm.Add(pi)

	// When: 删除PeerInfo
	pm.Remove(pid)

	// Then: 应成功删除
	_, ok := pm.Get(pid)
	assert.False(t, ok)
}

// TestPeerInfoManager_Remove_NotFound 测试删除不存在的 PeerInfo
func TestPeerInfoManager_Remove_NotFound(t *testing.T) {
	// Given: PeerInfoManager
	pm := NewPeerInfoManager()
	pid := generateTestPeerID(t)

	// When: 删除不存在的 PeerInfo（不应 panic）
	pm.Remove(pid)

	// Then: 应正常执行（无 panic）
	assert.True(t, true)
}

// TestPeerInfoManager_List 测试列出所有 PeerInfo
func TestPeerInfoManager_List(t *testing.T) {
	// Given: PeerInfoManager和多个PeerInfo
	pm := NewPeerInfoManager()
	pid1 := generateTestPeerID(t)
	pid2 := generateTestPeerID(t)

	ma1, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	ma2, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.2/tcp/4001")

	pm.Add(&peer.AddrInfo{ID: pid1, Addrs: []multiaddr.Multiaddr{ma1}})
	pm.Add(&peer.AddrInfo{ID: pid2, Addrs: []multiaddr.Multiaddr{ma2}})

	// When: 列出所有PeerInfo
	list := pm.List()

	// Then: 应返回所有PeerInfo
	assert.Len(t, list, 2)

	// 验证 ID 唯一性
	ids := make(map[peer.ID]bool)
	for _, pi := range list {
		ids[pi.ID] = true
	}
	assert.Len(t, ids, 2)
}

// TestPeerInfoManager_List_Empty 测试列出空的 PeerInfoManager
func TestPeerInfoManager_List_Empty(t *testing.T) {
	// Given: 空的 PeerInfoManager
	pm := NewPeerInfoManager()

	// When: 列出所有PeerInfo
	list := pm.List()

	// Then: 应返回空列表
	assert.Empty(t, list)
}

// TestPeerInfoManager_ConcurrentAccess 测试并发访问
func TestPeerInfoManager_ConcurrentAccess(t *testing.T) {
	// Given: PeerInfoManager
	pm := NewPeerInfoManager()

	// 预生成 100 个 PeerID
	peerIDs := make([]peer.ID, 100)
	for i := 0; i < 100; i++ {
		peerIDs[i] = generateTestPeerID(t)
	}

	// When: 并发添加
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pid := peerIDs[idx]
			ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
			pm.Add(&peer.AddrInfo{ID: pid, Addrs: []multiaddr.Multiaddr{ma}})
		}(i)
	}
	wg.Wait()

	// Then: 应全部添加成功
	list := pm.List()
	assert.Len(t, list, 100)
}

// TestPeerInfoManager_ConcurrentRead 测试并发读取
func TestPeerInfoManager_ConcurrentRead(t *testing.T) {
	// Given: PeerInfoManager 和已添加的 PeerInfo
	pm := NewPeerInfoManager()
	pid := generateTestPeerID(t)

	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	pi := &peer.AddrInfo{
		ID:    pid,
		Addrs: []multiaddr.Multiaddr{ma},
	}
	pm.Add(pi)

	// When: 并发读取
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok := pm.Get(pid)
			assert.True(t, ok)
		}()
	}
	wg.Wait()

	// Then: 应全部读取成功
	result, ok := pm.Get(pid)
	assert.True(t, ok)
	assert.Equal(t, pid, result.ID)
}

// TestPeerInfoManager_ConcurrentReadWrite 测试并发读写
func TestPeerInfoManager_ConcurrentReadWrite(t *testing.T) {
	// Given: PeerInfoManager
	pm := NewPeerInfoManager()

	// 预生成 50 个 PeerID
	peerIDs := make([]peer.ID, 50)
	for i := 0; i < 50; i++ {
		peerIDs[i] = generateTestPeerID(t)
	}

	// When: 并发读写
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)

		// 写入
		go func(idx int) {
			defer wg.Done()
			pid := peerIDs[idx]
			ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
			pm.Add(&peer.AddrInfo{ID: pid, Addrs: []multiaddr.Multiaddr{ma}})
		}(i)

		// 读取
		go func(idx int) {
			defer wg.Done()
			pid := peerIDs[idx]
			pm.Get(pid)
		}(i)
	}
	wg.Wait()

	// Then: 应正常执行（无 race condition）
	list := pm.List()
	assert.Len(t, list, 50)
}
