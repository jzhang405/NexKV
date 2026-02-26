// Package discovery 提供节点发现测试
package discovery

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==========================================
// Libp2pAddress 测试
// ==========================================

func TestLibp2pAddress_String(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected string
	}{
		{"tcp address", "/ip4/127.0.0.1/tcp/8080", "/ip4/127.0.0.1/tcp/8080"},
		{"quic address", "/ip4/192.168.1.1/udp/4001/quic", "/ip4/192.168.1.1/udp/4001/quic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ma, err := multiaddr.NewMultiaddr(tt.addr)
			require.NoError(t, err)

			addr := NewLibp2pAddress(ma)
			assert.Equal(t, tt.expected, addr.String())
		})
	}
}

func TestLibp2pAddress_String_Nil(t *testing.T) {
	addr := &Libp2pAddress{inner: nil}
	assert.Equal(t, "", addr.String())
}

func TestLibp2pAddress_Protocol(t *testing.T) {
	tests := []struct {
		name          string
		addr          string
		expectedProto string
	}{
		{"tcp", "/ip4/127.0.0.1/tcp/8080", "ip4"},
		{"quic", "/ip4/192.168.1.1/udp/4001/quic", "ip4"},
		{"websocket", "/ip4/127.0.0.1/tcp/8080/ws", "ip4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ma, err := multiaddr.NewMultiaddr(tt.addr)
			require.NoError(t, err)

			addr := NewLibp2pAddress(ma)
			assert.Equal(t, tt.expectedProto, addr.Protocol())
		})
	}
}

func TestLibp2pAddress_Protocol_Nil(t *testing.T) {
	addr := &Libp2pAddress{inner: nil}
	assert.Equal(t, "unknown", addr.Protocol())
}

func TestLibp2pAddress_Inner(t *testing.T) {
	ma, err := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/8080")
	require.NoError(t, err)

	addr := NewLibp2pAddress(ma)
	assert.Equal(t, ma, addr.Inner())
}

// ==========================================
// 地址转换测试
// ==========================================

func TestConvertToDomainAddresses(t *testing.T) {
	ma1, err := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/8080")
	require.NoError(t, err)

	ma2, err := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/9090")
	require.NoError(t, err)

	addrs := ConvertToDomainAddresses([]multiaddr.Multiaddr{ma1, ma2})

	assert.Len(t, addrs, 2)
	assert.Equal(t, ma1.String(), addrs[0].String())
	assert.Equal(t, ma2.String(), addrs[1].String())
}

func TestConvertToDomainAddresses_Empty(t *testing.T) {
	addrs := ConvertToDomainAddresses([]multiaddr.Multiaddr{})
	assert.Empty(t, addrs)
}

func TestConvertToLibp2pAddresses_FromLibp2pAddress(t *testing.T) {
	ma1, err := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/8080")
	require.NoError(t, err)

	ma2, err := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/9090")
	require.NoError(t, err)

	// 从 Libp2pAddress 转换
	domainAddrs := []model.NetworkAddress{
		NewLibp2pAddress(ma1),
		NewLibp2pAddress(ma2),
	}

	result, err := ConvertToLibp2pAddresses(domainAddrs)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, ma1, result[0])
	assert.Equal(t, ma2, result[1])
}

func TestConvertToLibp2pAddresses_FromOtherType(t *testing.T) {
	// 创建一个非 Libp2pAddress 的 NetworkAddress 实现
	otherAddr := &mockNetworkAddress{addr: "/ip4/10.0.0.1/tcp/3000"}

	result, err := ConvertToLibp2pAddresses([]model.NetworkAddress{otherAddr})
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "/ip4/10.0.0.1/tcp/3000", result[0].String())
}

func TestConvertToLibp2pAddresses_InvalidAddress(t *testing.T) {
	// 创建一个返回无效地址的 NetworkAddress
	invalidAddr := &mockNetworkAddress{addr: "invalid-address"}

	_, err := ConvertToLibp2pAddresses([]model.NetworkAddress{invalidAddr})
	assert.Error(t, err)
}

// mockNetworkAddress 用于测试的 mock 实现
type mockNetworkAddress struct {
	addr string
}

func (m *mockNetworkAddress) String() string {
	return m.addr
}

func (m *mockNetworkAddress) Protocol() string {
	return "mock"
}
