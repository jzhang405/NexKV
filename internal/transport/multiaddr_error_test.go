package transport

import (
	"testing"

	"github.com/libp2p/go-libp2p"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddressManager_DNSResolutionFailure 测试DNS解析失败
func TestAddressManager_DNSResolutionFailure(t *testing.T) {
	// Given: 无效的hostname
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4001"))
	require.NoError(t, err)
	defer h.Close()

	am := NewAddressManager(h, "invalid-hostname-that-does-not-exist.example.invalid", 4001)

	// When: 配置地址
	err = am.SetupAddresses()

	// Then: 应记录警告但不失败（libp2p会自动重试）
	assert.NoError(t, err, "DNS解析失败不应阻止配置")
}
