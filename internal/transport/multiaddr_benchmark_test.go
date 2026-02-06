package transport

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/cluster"
	"github.com/multiformats/go-multiaddr"
)

// BenchmarkNodeAddrToMultiaddr 基准测试地址转换
func BenchmarkNodeAddrToMultiaddr(b *testing.B) {
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 4001,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NodeAddrToMultiaddr(na, "tcp")
	}
}

// BenchmarkNodeAddrToMultiaddr_Hostname 基准测试 hostname 转换
func BenchmarkNodeAddrToMultiaddr_Hostname(b *testing.B) {
	na := &cluster.NodeAddress{
		Host:    "node1.example.com",
		TCPPort: 4001,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NodeAddrToMultiaddr(na, "tcp")
	}
}

// BenchmarkMultiaddrToNodeAddr 基准测试反向转换
func BenchmarkMultiaddrToNodeAddr(b *testing.B) {
	ma, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.1/tcp/4001")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = MultiaddrToNodeAddr(ma)
	}
}

// BenchmarkMultiaddrToNodeAddr_DNS 基准测试 DNS 地址转换
func BenchmarkMultiaddrToNodeAddr_DNS(b *testing.B) {
	ma, _ := multiaddr.NewMultiaddr("/dns4/node.example.com/tcp/4001")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = MultiaddrToNodeAddr(ma)
	}
}

// BenchmarkExtractHostname 基准测试 hostname 提取
func BenchmarkExtractHostname(b *testing.B) {
	ma, _ := multiaddr.NewMultiaddr("/dns4/node.example.com/tcp/4001")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ExtractHostname(ma)
	}
}

// BenchmarkAddressAdapter_ToMultiaddrs 基准测试适配器转换
func BenchmarkAddressAdapter_ToMultiaddrs(b *testing.B) {
	na := &cluster.NodeAddress{
		Host:    "192.168.1.1",
		TCPPort: 4001,
		UDPPort: 4002,
	}
	aa := NewAddressAdapter(na)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = aa.ToMultiaddrs()
	}
}
