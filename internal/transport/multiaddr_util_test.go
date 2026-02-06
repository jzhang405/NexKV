package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/cluster"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNodeAddrToMultiaddr_IPv4 测试IPv4地址转换
func TestNodeAddrToMultiaddr_IPv4(t *testing.T) {
	// Given: IPv4地址
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 4001,
	}

	// When: 转换为multiaddr
	ma, err := NodeAddrToMultiaddr(na, "tcp")

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "/ip4/192.168.1.1/tcp/4001", ma.String())
}

// TestNodeAddrToMultiaddr_IPv6 测试IPv6地址转换
func TestNodeAddrToMultiaddr_IPv6(t *testing.T) {
	// Given: IPv6地址
	na := &cluster.NodeAddress{
		Host:    "::1",
		TCPPort: 4001,
	}

	// When: 转换为multiaddr
	ma, err := NodeAddrToMultiaddr(na, "tcp")

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "/ip6/::1/tcp/4001", ma.String())
}

// TestNodeAddrToMultiaddr_Hostname 测试hostname地址转换
func TestNodeAddrToMultiaddr_Hostname(t *testing.T) {
	// Given: hostname地址
	na := &cluster.NodeAddress{
		Host:    "node1.example.com",
		TCPPort: 4001,
	}

	// When: 转换为multiaddr
	ma, err := NodeAddrToMultiaddr(na, "tcp")

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "/dns4/node1.example.com/tcp/4001", ma.String())
}

// TestNodeAddrToMultiaddr_UDP 测试UDP协议转换
func TestNodeAddrToMultiaddr_UDP(t *testing.T) {
	// Given: UDP地址
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		UDPPort: 4002,
	}

	// When: 转换为multiaddr
	ma, err := NodeAddrToMultiaddr(na, "udp")

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "/ip4/192.168.1.1/udp/4002", ma.String())
}

// TestNodeAddrToMultiaddr_UnsupportedProto 测试不支持的协议
func TestNodeAddrToMultiaddr_UnsupportedProto(t *testing.T) {
	// Given: NodeAddress
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 4001,
	}

	// When: 使用不支持的协议
	ma, err := NodeAddrToMultiaddr(na, "sctp")

	// Then: 应返回错误
	require.Error(t, err)
	assert.Nil(t, ma)
	assert.Contains(t, err.Error(), "不支持的协议")
}

// TestMultiaddrToNodeAddr_IPv4 测试multiaddr到NodeAddress转换
func TestMultiaddrToNodeAddr_IPv4(t *testing.T) {
	// Given: IPv4 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")

	// When: 转换为NodeAddress
	na, err := MultiaddrToNodeAddr(ma)

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.1", na.Host)
	assert.Equal(t, 4001, na.TCPPort)
}

// TestMultiaddrToNodeAddr_IPv6 测试IPv6 multiaddr转换
func TestMultiaddrToNodeAddr_IPv6(t *testing.T) {
	// Given: IPv6 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/ip6/::1/tcp/4001")

	// When: 转换为NodeAddress
	na, err := MultiaddrToNodeAddr(ma)

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "::1", na.Host)
	assert.Equal(t, 4001, na.TCPPort)
}

// TestMultiaddrToNodeAddr_DNS4 测试DNS4 multiaddr转换
func TestMultiaddrToNodeAddr_DNS4(t *testing.T) {
	// Given: DNS4 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/dns4/node.example.com/tcp/4001")

	// When: 转换为NodeAddress
	na, err := MultiaddrToNodeAddr(ma)

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "node.example.com", na.Host)
	assert.Equal(t, 4001, na.TCPPort)
}

// TestMultiaddrToNodeAddr_DNS6 测试DNS6 multiaddr转换
func TestMultiaddrToNodeAddr_DNS6(t *testing.T) {
	// Given: DNS6 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/dns6/node.example.com/tcp/4001")

	// When: 转换为NodeAddress
	na, err := MultiaddrToNodeAddr(ma)

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "node.example.com", na.Host)
	assert.Equal(t, 4001, na.TCPPort)
}

// TestMultiaddrToNodeAddr_UDP 测试UDP multiaddr转换
func TestMultiaddrToNodeAddr_UDP(t *testing.T) {
	// Given: UDP multiaddr
	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/udp/4002")

	// When: 转换为NodeAddress
	na, err := MultiaddrToNodeAddr(ma)

	// Then: 应成功转换
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.1", na.Host)
	assert.Equal(t, 4002, na.UDPPort)
}

// TestExtractHostname_DNS4 测试从DNS4地址提取hostname
func TestExtractHostname_DNS4(t *testing.T) {
	// Given: DNS4 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/dns4/node.example.com/tcp/4001")

	// When: 提取hostname
	hostname, found := ExtractHostname(ma)

	// Then: 应成功提取
	assert.True(t, found)
	assert.Equal(t, "node.example.com", hostname)
}

// TestExtractHostname_DNS6 测试从DNS6地址提取hostname
func TestExtractHostname_DNS6(t *testing.T) {
	// Given: DNS6 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/dns6/node.example.com/tcp/4001")

	// When: 提取hostname
	hostname, found := ExtractHostname(ma)

	// Then: 应成功提取
	assert.True(t, found)
	assert.Equal(t, "node.example.com", hostname)
}

// TestExtractHostname_IPv4 测试IPv4无hostname
func TestExtractHostname_IPv4(t *testing.T) {
	// Given: IPv4 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")

	// When: 提取hostname
	hostname, found := ExtractHostname(ma)

	// Then: 应无hostname
	assert.False(t, found)
	assert.Empty(t, hostname)
}

// TestExtractHostname_IPv6 测试IPv6无hostname
func TestExtractHostname_IPv6(t *testing.T) {
	// Given: IPv6 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/ip6/::1/tcp/4001")

	// When: 提取hostname
	hostname, found := ExtractHostname(ma)

	// Then: 应无hostname
	assert.False(t, found)
	assert.Empty(t, hostname)
}

// TestParseHostname 测试解析hostname为multiaddr
func TestParseHostname(t *testing.T) {
	// Given: hostname地址
	hostname := "node1.example.com"
	port := 4001

	// When: 解析为multiaddr
	ma, err := ParseHostname(hostname, port)

	// Then: 应成功解析
	require.NoError(t, err)
	assert.Equal(t, "/dns4/node1.example.com/tcp/4001", ma.String())
}

// TestParseHostname_InvalidPort 测试无效端口
func TestParseHostname_InvalidPort(t *testing.T) {
	// Given: hostname和无效端口
	hostname := "node1.example.com"
	port := -1

	// When: 解析为multiaddr
	ma, err := ParseHostname(hostname, port)

	// Then: 应返回错误
	require.Error(t, err)
	assert.Nil(t, ma)
}
