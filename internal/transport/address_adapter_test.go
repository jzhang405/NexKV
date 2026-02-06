package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/cluster"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddressAdapter_New 测试创建 AddressAdapter
func TestAddressAdapter_New(t *testing.T) {
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 4001,
		UDPPort: 4002,
	}

	aa := NewAddressAdapter(na)

	assert.NotNil(t, aa)
	assert.Equal(t, na, aa.nodeAddr)
}

// TestAddressAdapter_ToMultiaddrs 测试转换为 multiaddr 列表
func TestAddressAdapter_ToMultiaddrs(t *testing.T) {
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 4001,
		UDPPort: 4002,
	}

	aa := NewAddressAdapter(na)
	addrs := aa.ToMultiaddrs()

	// 应包含 TCP 和 UDP 地址
	assert.Len(t, addrs, 2)

	// 验证 TCP 地址
	foundTCP := false
	foundUDP := false
	for _, ma := range addrs {
		if ma.String() == "/ip4/192.168.1.1/tcp/4001" {
			foundTCP = true
		}
		if ma.String() == "/ip4/192.168.1.1/udp/4002" {
			foundUDP = true
		}
	}
	assert.True(t, foundTCP, "应包含 TCP 地址")
	assert.True(t, foundUDP, "应包含 UDP 地址")
}

// TestAddressAdapter_ToMultiaddrs_OnlyTCP 测试仅 TCP 地址
func TestAddressAdapter_ToMultiaddrs_OnlyTCP(t *testing.T) {
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 4001,
		UDPPort: 0,
	}

	aa := NewAddressAdapter(na)
	addrs := aa.ToMultiaddrs()

	// 应仅包含 TCP 地址
	assert.Len(t, addrs, 1)
	assert.Equal(t, "/ip4/192.168.1.1/tcp/4001", addrs[0].String())
}

// TestAddressAdapter_ToMultiaddrs_OnlyUDP 测试仅 UDP 地址
func TestAddressAdapter_ToMultiaddrs_OnlyUDP(t *testing.T) {
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 0,
		UDPPort: 4002,
	}

	aa := NewAddressAdapter(na)
	addrs := aa.ToMultiaddrs()

	// 应仅包含 UDP 地址
	assert.Len(t, addrs, 1)
	assert.Equal(t, "/ip4/192.168.1.1/udp/4002", addrs[0].String())
}

// TestAddressAdapter_ToMultiaddrs_Hostname 测试 hostname 地址
func TestAddressAdapter_ToMultiaddrs_Hostname(t *testing.T) {
	na := &cluster.NodeAddress{
		Host:    "node1.example.com",
		TCPPort: 4001,
		UDPPort: 0,
	}

	aa := NewAddressAdapter(na)
	addrs := aa.ToMultiaddrs()

	// 应包含 DNS 地址
	assert.Len(t, addrs, 1)
	assert.Equal(t, "/dns4/node1.example.com/tcp/4001", addrs[0].String())
}

// TestAddressAdapter_ToPeerInfo 测试转换为 PeerInfo
func TestAddressAdapter_ToPeerInfo(t *testing.T) {
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 4001,
		UDPPort: 0,
	}

	// 生成测试 PeerID
	privKey, _, err := crypto.GenerateEd25519Key(nil)
	require.NoError(t, err)
	pid, err := peer.IDFromPrivateKey(privKey)
	require.NoError(t, err)

	aa := NewAddressAdapter(na)
	pi := aa.ToPeerInfo(pid)

	// 验证 PeerInfo
	assert.Equal(t, pid, pi.ID)
	assert.Len(t, pi.Addrs, 1)
	assert.Equal(t, "/ip4/192.168.1.1/tcp/4001", pi.Addrs[0].String())
}

// TestMultiaddrsToNodeAddress 测试 multiaddr 列表转换为 NodeAddress
func TestMultiaddrsToNodeAddress(t *testing.T) {
	ma1, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")
	ma2, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/udp/4002")

	na, err := MultiaddrsToNodeAddress([]multiaddr.Multiaddr{ma1, ma2})

	// 应返回第一个成功的转换
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.1", na.Host)
	assert.Equal(t, 4001, na.TCPPort)
}

// TestMultiaddrsToNodeAddress_Empty 测试空列表
func TestMultiaddrsToNodeAddress_Empty(t *testing.T) {
	na, err := MultiaddrsToNodeAddress([]multiaddr.Multiaddr{})

	// 应返回错误
	assert.Error(t, err)
	assert.Nil(t, na)
}
