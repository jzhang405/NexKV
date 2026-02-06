package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/cluster"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultiaddrToNodeAddr_Empty 测试空地址
func TestMultiaddrToNodeAddr_Empty(t *testing.T) {
	// Given: 空 multiaddr
	ma, _ := multiaddr.NewMultiaddr("/")

	// When: 转换为NodeAddress
	na, err := MultiaddrToNodeAddr(ma)

	// Then: 应返回错误
	assert.Error(t, err)
	assert.Nil(t, na)
}

// TestMultiaddrToNodeAddr_NoPort 测试无端口
func TestMultiaddrToNodeAddr_NoPort(t *testing.T) {
	// Given: 仅 IP 地址
	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1")

	// When: 转换为NodeAddress
	na, err := MultiaddrToNodeAddr(ma)

	// Then: 应返回错误（无端口）
	assert.Error(t, err)
	assert.Nil(t, na)
}

// TestNodeAddrToMultiaddr_ZeroPort 测试零端口
func TestNodeAddrToMultiaddr_ZeroPort(t *testing.T) {
	// Given: 零端口的NodeAddress
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 0,
	}

	// When: 转换为multiaddr
	ma, err := NodeAddrToMultiaddr(na, "tcp")

	// Then: 应成功转换（端口0允许）
	require.NoError(t, err)
	assert.Contains(t, ma.String(), "/tcp/0")
}

// TestNodeAddrToMultiaddr_Nil 测试空 NodeAddress
func TestNodeAddrToMultiaddr_Nil(t *testing.T) {
	// When: 转换空 NodeAddress
	ma, err := NodeAddrToMultiaddr(nil, "tcp")

	// Then: 应返回错误
	assert.Error(t, err)
	assert.Nil(t, ma)
}

// TestNodeAddrToMultiaddr_InvalidPort 测试无效端口
func TestNodeAddrToMultiaddr_InvalidPort(t *testing.T) {
	// Given: 无效端口的NodeAddress
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 70000, // 超出范围
	}

	// When: 转换为multiaddr
	ma, err := NodeAddrToMultiaddr(na, "tcp")

	// Then: 应返回错误
	assert.Error(t, err)
	assert.Nil(t, ma)
}
