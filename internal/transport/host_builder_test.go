package transport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHostBuilder_BuildSuccess 测试Host构建
func TestHostBuilder_BuildSuccess(t *testing.T) {
	// Given: 配置
	keyFile := filepath.Join(os.TempDir(), "test-host-key.pem")
	defer os.Remove(keyFile)

	// When: 构建Host
	hb := NewHostBuilder(4001, keyFile)
	builtHost, err := hb.Build()

	// Then: 应成功创建
	require.NoError(t, err)
	assert.NotNil(t, builtHost)
	assert.NotEmpty(t, builtHost.ID())
	assert.NotEmpty(t, builtHost.Addrs())

	// Cleanup
	builtHost.Close()
}

// TestHostBuilder_ConnectionManager 测试连接管理器
func TestHostBuilder_ConnectionManager(t *testing.T) {
	// Given: 配置连接管理器
	keyFile := filepath.Join(os.TempDir(), "test-conn-key.pem")
	defer os.Remove(keyFile)

	hb := NewHostBuilder(4001, keyFile)
	hb.WithConnectionManager(10, 50)

	// When: 构建Host
	builtHost, err := hb.Build()

	// Then: 连接管理器应生效
	require.NoError(t, err)
	assert.NotNil(t, builtHost.Network())

	// Cleanup
	builtHost.Close()
}

// TestHostBuilder_ListenAddr 测试监听地址
func TestHostBuilder_ListenAddr(t *testing.T) {
	// Given: 配置监听地址
	keyFile := filepath.Join(os.TempDir(), "test-addr-key.pem")
	defer os.Remove(keyFile)

	hb := NewHostBuilder(4001, keyFile)
	hb.WithListenAddr("127.0.0.1")

	// When: 构建Host
	builtHost, err := hb.Build()

	// Then: 应监听指定地址
	require.NoError(t, err)
	addrs := builtHost.Addrs()
	assert.NotEmpty(t, addrs)

	// Cleanup
	builtHost.Close()
}

// TestHostBuilder_Performance 测试性能
func TestHostBuilder_Performance(t *testing.T) {
	// Given: 密钥文件存在
	keyFile := filepath.Join(os.TempDir(), "test-perf-key.pem")
	defer os.Remove(keyFile)

	km := NewKeyManager(keyFile)
	_, err := km.LoadOrGenerate()
	require.NoError(t, err)

	// When: 测量初始化时间
	start := time.Now()
	hb := NewHostBuilder(4001, keyFile)
	builtHost, err := hb.Build()
	duration := time.Since(start)

	// Then: 应 < 100ms
	require.NoError(t, err)
	assert.Less(t, duration.Milliseconds(), int64(100), "Host初始化应 < 100ms")

	// Cleanup
	builtHost.Close()
}

// TestHost_Communication 测试节点间通信
func TestHost_Communication(t *testing.T) {
	// Given: 两个节点
	ctx := context.Background()
	keyFile1 := filepath.Join(os.TempDir(), "test-node1-key.pem")
	keyFile2 := filepath.Join(os.TempDir(), "test-node2-key.pem")
	defer os.Remove(keyFile1)
	defer os.Remove(keyFile2)

	hb1 := NewHostBuilder(4001, keyFile1)
	hb2 := NewHostBuilder(4002, keyFile2)

	host1, err := hb1.Build()
	require.NoError(t, err)
	defer host1.Close()

	host2, err := hb2.Build()
	require.NoError(t, err)
	defer host2.Close()

	// When: 节点1连接节点2
	pi := host2.Peerstore().PeerInfo(host2.ID())
	err = host1.Connect(ctx, pi)

	// Then: 连接成功
	assert.NoError(t, err)

	// And: 节点2应看到节点1
	peers := host2.Network().Peers()
	assert.Contains(t, peers, host1.ID())
}

// TestHost_MultiNode 测试多节点
func TestHost_MultiNode(t *testing.T) {
	// Given: 3个节点
	ctx := context.Background()
	hosts := make([]host.Host, 3)
	keyFiles := make([]string, 3)

	for i := 0; i < 3; i++ {
		keyFile := filepath.Join(os.TempDir(), fmt.Sprintf("test-multi-key-%d.pem", i))
		keyFiles[i] = keyFile
		hb := NewHostBuilder(5001+i, keyFile)
		h, err := hb.Build()
		require.NoError(t, err)
		hosts[i] = h
		defer h.Close()
		defer os.Remove(keyFile)
	}

	// When: 连接成链
	pi12 := hosts[1].Peerstore().PeerInfo(hosts[1].ID())
	err := hosts[0].Connect(ctx, pi12)
	require.NoError(t, err)

	pi23 := hosts[2].Peerstore().PeerInfo(hosts[2].ID())
	err = hosts[1].Connect(ctx, pi23)
	require.NoError(t, err)

	// Then: 连接成功
	assert.Contains(t, hosts[1].Network().Peers(), hosts[0].ID())
	assert.Contains(t, hosts[2].Network().Peers(), hosts[1].ID())
}
