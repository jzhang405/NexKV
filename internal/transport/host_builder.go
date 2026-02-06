package transport

import (
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/multiformats/go-multiaddr"
)

// HostBuilder libp2p Host 构建器
type HostBuilder struct {
	listenPort int
	keyPath    string
	lowWater   int
	highWater  int
	listenAddr string
}

// NewHostBuilder 创建 Host 构建器
func NewHostBuilder(listenPort int, keyPath string) *HostBuilder {
	return &HostBuilder{
		listenPort: listenPort,
		keyPath:    keyPath,
		lowWater:   100,
		highWater:  400,
		listenAddr: "0.0.0.0",
	}
}

// Build 构建 libp2p Host
func (hb *HostBuilder) Build() (host.Host, error) {
	// 密钥管理
	km := NewKeyManager(hb.keyPath)
	privKey, err := km.LoadOrGenerate()
	if err != nil {
		return nil, err
	}

	// 连接管理器
	cm, err := connmgr.NewConnManager(
		hb.lowWater,
		hb.highWater,
		connmgr.WithGracePeriod(time.Minute),
	)
	if err != nil {
		return nil, err
	}

	// 监听地址
	listenAddr, err := multiaddr.NewMultiaddr(
		fmt.Sprintf("/ip4/%s/tcp/%d", hb.listenAddr, hb.listenPort),
	)
	if err != nil {
		return nil, err
	}

	// 创建 libp2p 选项
	opts := []libp2p.Option{
		libp2p.Identity(privKey),
		libp2p.ListenAddrs(listenAddr),
		libp2p.ConnectionManager(cm),
		libp2p.Ping(true),
	}

	// 创建 Host
	return libp2p.New(opts...)
}

// WithConnectionManager 配置连接管理器
func (hb *HostBuilder) WithConnectionManager(low, high int) *HostBuilder {
	hb.lowWater = low
	hb.highWater = high
	return hb
}

// WithListenAddr 配置监听地址
func (hb *HostBuilder) WithListenAddr(addr string) *HostBuilder {
	hb.listenAddr = addr
	return hb
}
