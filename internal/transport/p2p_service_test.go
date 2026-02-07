// Copyright 2025 The NexKV Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package transport

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestP2PService_New 测试创建 P2P 服务
func TestP2PService_New(t *testing.T) {
	// 创建临时密钥目录
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9211", keyPath)
	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	assert.NotNil(t, service)
	assert.NotNil(t, service.Host())
	assert.NotNil(t, service.Protocol())
	assert.NotNil(t, service.Codec())
	assert.False(t, service.IsStarted())
}

// TestP2PService_NewNilConfig 测试使用 nil 配置创建
func TestP2PService_NewNilConfig(t *testing.T) {
	_, err := NewP2PService(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "配置不能为空")
}

// TestP2PService_StartStop 测试启动和停止服务
func TestP2PService_StartStop(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9212", keyPath)
	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// 启动服务
	err = service.Start(ctx)
	require.NoError(t, err)
	assert.True(t, service.IsStarted())

	// 重复启动应失败
	err = service.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "服务已启动")

	// 停止服务
	err = service.Stop()
	require.NoError(t, err)
	assert.False(t, service.IsStarted())

	// 重复停止应成功（幂等）
	err = service.Stop()
	assert.NoError(t, err)
}

// TestP2PService_PeerID 测试获取节点 ID
func TestP2PService_PeerID(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9213", keyPath)
	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	peerID := service.PeerID()
	assert.NotEmpty(t, peerID)
}

// TestP2PService_Addrs 测试获取监听地址
func TestP2PService_Addrs(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9214", keyPath)
	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	addrs := service.Addrs()
	assert.NotEmpty(t, addrs)

	// 验证地址格式
	for _, addr := range addrs {
		assert.NotEmpty(t, addr.String())
	}

	// 验证格式化的地址列表
	formattedAddrs := service.GetListenAddrs()
	assert.NotEmpty(t, formattedAddrs)
	assert.Equal(t, len(addrs), len(formattedAddrs))
}

// TestP2PService_ConnectToPeer 测试连接到其他节点
func TestP2PService_ConnectToPeer(t *testing.T) {
	ctx := context.Background()

	// 创建两个服务
	tmpDir := t.TempDir()
	keyPath1 := filepath.Join(tmpDir, "peer1.key")
	cfg1 := DefaultP2PServiceConfig("9215", keyPath1)
	service1, err := NewP2PService(cfg1)
	require.NoError(t, err)
	defer service1.Close()

	keyPath2 := filepath.Join(tmpDir, "peer2.key")
	cfg2 := DefaultP2PServiceConfig("9216", keyPath2)
	service2, err := NewP2PService(cfg2)
	require.NoError(t, err)
	defer service2.Close()

	// 启动服务
	err = service1.Start(ctx)
	require.NoError(t, err)
	err = service2.Start(ctx)
	require.NoError(t, err)

	// 连接 service1 到 service2（带重试机制，处理间歇性 TLS 握手问题）
	peerInfo2 := service2.GetPeerInfo()
	maxRetries := 3
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		err = service1.ConnectToPeer(ctx, peerInfo2)
		if err == nil {
			break
		}
		lastErr = err
		// 等待一段时间后重试
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, err, "连接失败（已重试 %d 次）: %v", maxRetries, lastErr)

	// 验证连接（检查 service1 的连接数）
	time.Sleep(100 * time.Millisecond)
	peers := service1.Host().Network().Peers()
	assert.Contains(t, peers, service2.PeerID())
}

// TestP2PService_KeyPersistence 测试密钥持久化
func TestP2PService_KeyPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9217", keyPath)

	// 第一次创建服务
	service1, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service1.Close()
	peerID1 := service1.PeerID()

	// 第二次创建服务（应使用相同密钥）
	service2, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service2.Close()
	peerID2 := service2.PeerID()

	// 验证 PeerID 相同
	assert.Equal(t, peerID1, peerID2)
}

// TestP2PService_GetPeerInfo 测试获取节点信息
func TestP2PService_GetPeerInfo(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9218", keyPath)
	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	peerInfo := service.GetPeerInfo()
	assert.NotEmpty(t, peerInfo.ID)
	assert.NotEmpty(t, peerInfo.Addrs)
}

// TestP2PService_CustomConfig 测试自定义配置
func TestP2PService_CustomConfig(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := &P2PServiceConfig{
		ListenAddr:     "9219",
		KeyPath:        keyPath,
		LowWater:       50,
		HighWater:      200,
		DiscoveryTag:   "custom-nexkv",
		BootstrapPeers: nil,
	}

	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	assert.NotNil(t, service)
	assert.Equal(t, "custom-nexkv", service.discovery.serviceTag)
}

// TestP2PService_InvalidKeyPath 测试无效密钥路径
func TestP2PService_InvalidKeyPath(t *testing.T) {
	// 使用一个无法创建的路径
	keyPath := "/root/nonexistent_dir/peer.key"

	cfg := DefaultP2PServiceConfig("9220", keyPath)
	_, err := NewP2PService(cfg)
	assert.Error(t, err)
}

// TestP2PService_ConfigureBootstrapPeers 测试配置引导节点
func TestP2PService_ConfigureBootstrapPeers(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath1 := filepath.Join(tmpDir, "peer1.key")
	keyPath2 := filepath.Join(tmpDir, "peer2.key")

	// 创建第二个节点作为引导节点
	cfg2 := DefaultP2PServiceConfig("9222", keyPath2)
	bootstrapService, err := NewP2PService(cfg2)
	require.NoError(t, err)
	defer bootstrapService.Close()

	ctx := context.Background()
	err = bootstrapService.Start(ctx)
	require.NoError(t, err)

	// 配置引导节点
	cfg1 := &P2PServiceConfig{
		ListenAddr:     "9221",
		KeyPath:        keyPath1,
		BootstrapPeers: []peer.AddrInfo{bootstrapService.GetPeerInfo()},
	}

	service, err := NewP2PService(cfg1)
	require.NoError(t, err)
	defer service.Close()

	assert.NotNil(t, service)
}

// TestP2PService_Close 测试关闭服务
func TestP2PService_Close(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9223", keyPath)
	service, err := NewP2PService(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	err = service.Start(ctx)
	require.NoError(t, err)

	// 使用 Close 方法停止服务
	err = service.Close()
	require.NoError(t, err)
	assert.False(t, service.IsStarted())
}

// TestP2PService_DefaultConfig 测试默认配置
func TestP2PService_DefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9224", keyPath)
	assert.NotNil(t, cfg)
	assert.Equal(t, "9224", cfg.ListenAddr)
	assert.Equal(t, keyPath, cfg.KeyPath)
	assert.Equal(t, 100, cfg.LowWater)
	assert.Equal(t, 400, cfg.HighWater)
	assert.Equal(t, "nexkv-discovery", cfg.DiscoveryTag)
}

// TestP2PService_ExpandPath 测试路径展开
func TestP2PService_ExpandPath(t *testing.T) {
	tmpDir := t.TempDir()
	homeKeyPath := filepath.Join(tmpDir, "~/.nexkv/peer.key")

	cfg := DefaultP2PServiceConfig("9225", homeKeyPath)
	// NewP2PService 会自动展开路径
	_, err := NewP2PService(cfg)
	assert.NoError(t, err)
}

// TestP2PService_MultipleStartStop 测试多次启动停止
func TestP2PService_MultipleStartStop(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "peer.key")

	cfg := DefaultP2PServiceConfig("9226", keyPath)
	service, err := NewP2PService(cfg)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	// 第一次启动
	err = service.Start(ctx)
	require.NoError(t, err)

	// 停止
	err = service.Stop()
	require.NoError(t, err)

	// 第二次启动（应成功）
	err = service.Start(ctx)
	require.NoError(t, err)

	// 再次停止
	err = service.Stop()
	require.NoError(t, err)
}
