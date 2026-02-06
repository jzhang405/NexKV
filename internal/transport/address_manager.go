package transport

import (
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// AddressManager 地址管理器
// 负责配置和管理 libp2p 节点的 multiaddr 地址列表
type AddressManager struct {
	host     host.Host
	hostname string
	port     int
}

// NewAddressManager 创建地址管理器
func NewAddressManager(h host.Host, hostname string, port int) *AddressManager {
	return &AddressManager{
		host:     h,
		hostname: hostname,
		port:     port,
	}
}

// SetupAddresses 配置地址
// 如果设置了 hostname，将添加对外公布的 hostname 地址
func (am *AddressManager) SetupAddresses() error {
	// 如果有 hostname，添加对外公布的地址
	if am.hostname != "" && am.port > 0 {
		announcedAddr, err := multiaddr.NewMultiaddr(
			fmt.Sprintf("/dns4/%s/tcp/%d", am.hostname, am.port),
		)
		if err != nil {
			return fmt.Errorf("创建 hostname 地址失败: %w", err)
		}

		// 注册到 Peerstore，TTL 24小时
		am.host.Peerstore().AddAddr(
			am.host.ID(),
			announcedAddr,
			24*time.Hour,
		)
	}

	return nil
}

// AddAnnouncedAddr 添加对外公布的地址
// addr: multiaddr 格式
// ttl: 地址存活时间
func (am *AddressManager) AddAnnouncedAddr(addr multiaddr.Multiaddr, ttl time.Duration) error {
	if addr == nil {
		return fmt.Errorf("地址不能为空")
	}

	if ttl <= 0 {
		ttl = 24 * time.Hour // 默认 TTL
	}

	am.host.Peerstore().AddAddr(
		am.host.ID(),
		addr,
		ttl,
	)

	return nil
}

// GetPeerInfo 获取自己的 PeerInfo
func (am *AddressManager) GetPeerInfo() *peer.AddrInfo {
	return &peer.AddrInfo{
		ID:    am.host.ID(),
		Addrs: am.host.Addrs(),
	}
}

// UpdateAddresses 更新地址列表
func (am *AddressManager) UpdateAddresses(addrs []multiaddr.Multiaddr) {
	for _, addr := range addrs {
		if addr != nil {
			am.host.Peerstore().AddAddr(
				am.host.ID(),
				addr,
				24*time.Hour,
			)
		}
	}
}

// ListenAddr 获取监听地址
func (am *AddressManager) ListenAddr() multiaddr.Multiaddr {
	addrs := am.host.Addrs()
	if len(addrs) == 0 {
		return nil
	}
	// 返回第一个监听地址
	return addrs[0]
}

// ClearAddresses 清除所有地址
func (am *AddressManager) ClearAddresses() {
	addrs := am.host.Peerstore().Addrs(am.host.ID())
	for _, addr := range addrs {
		am.host.Peerstore().SetAddr(
			am.host.ID(),
			addr,
			0*time.Second, // TTL 为 0 表示删除
		)
	}
}
