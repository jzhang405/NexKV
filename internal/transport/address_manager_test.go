package transport

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddressManager_New 测试创建 AddressManager
func TestAddressManager_New(t *testing.T) {
	// Given: 创建一个临时 host
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	// When: 创建 AddressManager
	am := NewAddressManager(h, "node1.example.com", 4001)

	// Then: 应成功创建
	assert.NotNil(t, am)
	assert.Equal(t, h, am.host)
	assert.Equal(t, "node1.example.com", am.hostname)
	assert.Equal(t, 4001, am.port)
}

// TestAddressManager_SetupAddresses_NoHostname 测试无 hostname 的地址配置
func TestAddressManager_SetupAddresses_NoHostname(t *testing.T) {
	// Given: Host 和 AddressManager（无 hostname）
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "", 0)

	// When: 配置地址
	err = am.SetupAddresses()

	// Then: 应成功配置
	assert.NoError(t, err)
}

// TestAddressManager_SetupAddresses_WithHostname 测试带 hostname 的地址配置
func TestAddressManager_SetupAddresses_WithHostname(t *testing.T) {
	// Given: Host 和 AddressManager（带 hostname）
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4001"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "node1.example.com", 4001)

	// When: 配置地址
	err = am.SetupAddresses()

	// Then: 应成功配置
	assert.NoError(t, err)

	// 验证 Peerstore 中的地址包含 hostname
	addrs := h.Peerstore().Addrs(h.ID())
	found := false
	for _, addr := range addrs {
		if hostname, ok := ExtractHostname(addr); ok && hostname == "node1.example.com" {
			found = true
			break
		}
	}
	assert.True(t, found, "Peerstore 中应包含 hostname 地址")
}

// TestAddressManager_AddAnnouncedAddr 测试添加公布地址
func TestAddressManager_AddAnnouncedAddr(t *testing.T) {
	// Given: Host 和 AddressManager
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4001"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "", 0)
	err = am.SetupAddresses()
	require.NoError(t, err)

	// When: 添加公布地址
	ma, _ := multiaddr.NewMultiaddr("/dns4/node1.example.com/tcp/4001")
	err = am.AddAnnouncedAddr(ma, 24*time.Hour)

	// Then: 应成功添加
	assert.NoError(t, err)

	// 验证 Peerstore 中的地址已添加
	addrs := h.Peerstore().Addrs(h.ID())
	found := false
	for _, addr := range addrs {
		if addr.String() == "/dns4/node1.example.com/tcp/4001" {
			found = true
			break
		}
	}
	assert.True(t, found, "Peerstore 中应包含公布的地址")
}

// TestAddressManager_AddAnnouncedAddr_ZeroTTL 测试添加零 TTL 地址
func TestAddressManager_AddAnnouncedAddr_ZeroTTL(t *testing.T) {
	// Given: Host 和 AddressManager
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4001"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "", 0)
	err = am.SetupAddresses()
	require.NoError(t, err)

	// When: 添加零 TTL 公布地址
	ma, _ := multiaddr.NewMultiaddr("/dns4/node1.example.com/tcp/4001")
	err = am.AddAnnouncedAddr(ma, 0)

	// Then: 应成功添加（libp2p 会使用默认 TTL）
	assert.NoError(t, err)
}

// TestAddressManager_GetPeerInfo 测试获取自己的 PeerInfo
func TestAddressManager_GetPeerInfo(t *testing.T) {
	// Given: Host 和 AddressManager
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "node1.example.com", 4001)

	// When: 获取自己的 PeerInfo
	pi := am.GetPeerInfo()

	// Then: 应返回正确的 PeerInfo
	assert.NotNil(t, pi)
	assert.Equal(t, h.ID(), pi.ID)
	assert.NotEmpty(t, pi.Addrs, "应至少有一个地址")
}

// TestAddressManager_UpdateAddresses 测试更新地址列表
func TestAddressManager_UpdateAddresses(t *testing.T) {
	// Given: Host 和 AddressManager
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "", 0)

	// When: 更新地址列表
	ma1, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	ma2, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.2/tcp/4001")
	am.UpdateAddresses([]multiaddr.Multiaddr{ma1, ma2})

	// Then: 地址应被添加（libp2p 会去重）
	// 注意：libp2p 的 AddrsManager 会管理 TTL，这里只验证没有错误
	assert.NotEmpty(t, h.Addrs())
}

// TestAddressManager_SetupAddresses_DNSResolutionFailure 测试 DNS 解析失败场景
func TestAddressManager_SetupAddresses_DNSResolutionFailure(t *testing.T) {
	// Given: Host 和无效的 hostname
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4001"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "invalid-hostname-that-does-not-exist.example.invalid", 4001)

	// When: 配置地址
	err = am.SetupAddresses()

	// Then: libp2p 允许添加，DNS 解析失败不会立即报错
	// 因为 libp2p 会在实际连接时才解析 DNS
	assert.NoError(t, err, "DNS 解析失败不应阻止配置")
}

// TestAddressManager_AddMultipleAnnouncedAddrs 测试添加多个公布地址
func TestAddressManager_AddMultipleAnnouncedAddrs(t *testing.T) {
	// Given: Host 和 AddressManager
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4001"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "", 0)
	err = am.SetupAddresses()
	require.NoError(t, err)

	// When: 添加多个公布地址
	ma1, _ := multiaddr.NewMultiaddr("/dns4/node1.example.com/tcp/4001")
	ma2, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	ma3, _ := multiaddr.NewMultiaddr("/ip6/::1/tcp/4001")

	err = am.AddAnnouncedAddr(ma1, 24*time.Hour)
	require.NoError(t, err)
	err = am.AddAnnouncedAddr(ma2, 24*time.Hour)
	require.NoError(t, err)
	err = am.AddAnnouncedAddr(ma3, 24*time.Hour)
	require.NoError(t, err)

	// Then: 所有地址应被添加
	addrs := h.Addrs()
	assert.GreaterOrEqual(t, len(addrs), 1, "应至少有一个地址")
}

// TestAddressManager_ListenAddr 测试获取监听地址
func TestAddressManager_ListenAddr(t *testing.T) {
	// Given: Host 和 AddressManager
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "node1.example.com", 4001)

	// When: 获取监听地址
	listenAddr := am.ListenAddr()

	// Then: 应返回正确的监听地址
	assert.NotNil(t, listenAddr)
	assert.Contains(t, listenAddr.String(), "/ip4/127.0.0.1/tcp/")
}
