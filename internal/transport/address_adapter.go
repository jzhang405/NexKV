package transport

import (
	"fmt"

	"github.com/jzhang405/NexKV/internal/metadata/cluster"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// AddressAdapter NodeAddress 适配层
// 负责在 NodeAddress 和 multiaddr/PeerInfo 之间转换
type AddressAdapter struct {
	nodeAddr *cluster.NodeAddress
}

// NewAddressAdapter 创建 AddressAdapter
func NewAddressAdapter(na *cluster.NodeAddress) *AddressAdapter {
	return &AddressAdapter{
		nodeAddr: na,
	}
}

// ToMultiaddrs 转换为 multiaddr 列表
// 返回 TCP 和 UDP 地址（如果端口 > 0）
func (aa *AddressAdapter) ToMultiaddrs() []multiaddr.Multiaddr {
	var result []multiaddr.Multiaddr

	// TCP 地址
	if aa.nodeAddr.TCPPort > 0 {
		if ma, err := NodeAddrToMultiaddr(aa.nodeAddr, "tcp"); err == nil {
			result = append(result, ma)
		}
	}

	// UDP 地址
	if aa.nodeAddr.UDPPort > 0 {
		if ma, err := NodeAddrToMultiaddr(aa.nodeAddr, "udp"); err == nil {
			result = append(result, ma)
		}
	}

	return result
}

// ToPeerInfo 转换为 PeerInfo
func (aa *AddressAdapter) ToPeerInfo(pid peer.ID) *peer.AddrInfo {
	return &peer.AddrInfo{
		ID:    pid,
		Addrs: aa.ToMultiaddrs(),
	}
}

// ToNodeAddress 从 multiaddr 列表转换为 NodeAddress
// 优先使用第一个地址
func MultiaddrsToNodeAddress(addrs []multiaddr.Multiaddr) (*cluster.NodeAddress, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("地址列表为空")
	}

	// 优先返回第一个成功的转换
	for _, ma := range addrs {
		if na, err := MultiaddrToNodeAddr(ma); err == nil {
			return na, nil
		}
	}

	return nil, fmt.Errorf("无法从地址列表转换")
}
