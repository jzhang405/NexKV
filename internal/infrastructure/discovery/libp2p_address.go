package discovery

import (
	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/multiformats/go-multiaddr"
)

// Libp2pAddress libp2p 地址适配器
// 实现领域层的 NetworkAddress 接口，封装 libp2p 的 multiaddr
type Libp2pAddress struct {
	inner multiaddr.Multiaddr
}

// NewLibp2pAddress 创建 libp2p 地址适配器
func NewLibp2pAddress(addr multiaddr.Multiaddr) *Libp2pAddress {
	return &Libp2pAddress{inner: addr}
}

// String 实现 NetworkAddress 接口
func (a *Libp2pAddress) String() string {
	if a.inner == nil {
		return ""
	}
	return a.inner.String()
}

// Protocol 实现 NetworkAddress 接口
// 提取第一个协议类型（如 tcp、quic、ws 等）
func (a *Libp2pAddress) Protocol() string {
	if a.inner == nil {
		return "unknown"
	}

	protocols := a.inner.Protocols()
	if len(protocols) > 0 {
		return protocols[0].Name
	}
	return "unknown"
}

// Inner 返回原始的 libp2p multiaddr（仅供基础设施层内部使用）
// 领域层不应该调用此方法
func (a *Libp2pAddress) Inner() multiaddr.Multiaddr {
	return a.inner
}

// ConvertToLibp2pAddresses 批量转换领域地址为 libp2p 地址
// 用于需要调用 libp2p API 的场景
func ConvertToLibp2pAddresses(addrs []model.NetworkAddress) ([]multiaddr.Multiaddr, error) {
	result := make([]multiaddr.Multiaddr, 0, len(addrs))
	for _, addr := range addrs {
		if libp2pAddr, ok := addr.(*Libp2pAddress); ok {
			result = append(result, libp2pAddr.Inner())
		} else {
			// 如果不是 Libp2pAddress，尝试从字符串解析
			ma, err := multiaddr.NewMultiaddr(addr.String())
			if err != nil {
				return nil, err
			}
			result = append(result, ma)
		}
	}
	return result, nil
}

// ConvertToDomainAddresses 批量转换 libp2p 地址为领域地址
// 用于发现节点后通知领域层
func ConvertToDomainAddresses(addrs []multiaddr.Multiaddr) []model.NetworkAddress {
	result := make([]model.NetworkAddress, 0, len(addrs))
	for _, addr := range addrs {
		result = append(result, NewLibp2pAddress(addr))
	}
	return result
}
