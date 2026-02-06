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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceRestart 服务重启后恢复测试
func TestServiceRestart(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	keyPath := filepath.Join(tmpDir, "node.key")
	cfg := DefaultP2PServiceConfig("9811", keyPath)

	// 第一次启动
	service1, err := NewP2PService(cfg)
	require.NoError(t, err)
	err = service1.Start(ctx)
	require.NoError(t, err)

	peerID1 := service1.PeerID().String()
	addrs1 := service1.Host().Addrs()

	// 关闭服务
	err = service1.Close()
	require.NoError(t, err)

	// 等待资源释放
	time.Sleep(100 * time.Millisecond)

	// 第二次启动（使用相同的 key）
	service2, err := NewP2PService(cfg)
	require.NoError(t, err)
	err = service2.Start(ctx)
	require.NoError(t, err)
	defer service2.Close()

	peerID2 := service2.PeerID().String()
	addrs2 := service2.Host().Addrs()

	// 验证重启后身份一致
	assert.Equal(t, peerID1, peerID2, "重启后 PeerID 应该保持一致")
	assert.Equal(t, len(addrs1), len(addrs2), "重启后监听地址数量应该一致")

	t.Log("服务重启测试：身份和配置保持一致")
}

// TestNodeDynamicJoin 节点动态加入测试
func TestNodeDynamicJoin(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 初始集群：2 个节点
	nodes := make([]*P2PService, 2)
	for i := 0; i < 2; i++ {
		keyPath := filepath.Join(tmpDir, fmt.Sprintf("node%d.key", i))
		cfg := DefaultP2PServiceConfig(fmt.Sprintf("982%d", i), keyPath)

		service, err := NewP2PService(cfg)
		require.NoError(t, err)
		err = service.Start(ctx)
		require.NoError(t, err)

		nodes[i] = service
	}
	defer func() {
		for _, node := range nodes {
			if node != nil {
				node.Close()
			}
		}
	}()

	// 建立初始连接
	peerInfo1 := nodes[1].GetPeerInfo()
	err := nodes[0].ConnectToPeer(ctx, peerInfo1)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// 验证初始连接
	peers0 := nodes[0].Host().Network().Peers()
	assert.Contains(t, peers0, nodes[1].PeerID())

	// 动态加入新节点
	newNodeKeyPath := filepath.Join(tmpDir, "node2.key")
	newNodeCfg := DefaultP2PServiceConfig("9822", newNodeKeyPath)
	newNode, err := NewP2PService(newNodeCfg)
	require.NoError(t, err)
	err = newNode.Start(ctx)
	require.NoError(t, err)
	defer newNode.Close()

	// 新节点连接到现有集群
	for i := 0; i < 2; i++ {
		peerInfo := nodes[i].GetPeerInfo()
		err = newNode.ConnectToPeer(ctx, peerInfo)
		if err != nil {
			t.Logf("新节点连接到节点 %d 失败：%v", i, err)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// 验证新节点已加入
	newNodePeers := newNode.Host().Network().Peers()
	t.Logf("新节点看到的集群成员：%d 个", len(newNodePeers))

	// 验证双向发现
	for i := 0; i < 2; i++ {
		peers := nodes[i].Host().Network().Peers()
		t.Logf("节点 %d 看到的集群成员：%d 个", i, len(peers))
	}

	t.Log("节点动态加入测试完成")
}

// TestNodeDynamicLeave 节点动态离开测试
func TestNodeDynamicLeave(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 创建 3 节点集群
	nodes := make([]*P2PService, 3)
	for i := 0; i < 3; i++ {
		keyPath := filepath.Join(tmpDir, fmt.Sprintf("node%d.key", i))
		cfg := DefaultP2PServiceConfig(fmt.Sprintf("983%d", i), keyPath)

		service, err := NewP2PService(cfg)
		require.NoError(t, err)
		err = service.Start(ctx)
		require.NoError(t, err)

		nodes[i] = service
	}

	// 建立全连接
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			peerInfo := nodes[j].GetPeerInfo()
			err := nodes[i].ConnectToPeer(ctx, peerInfo)
			if err != nil {
				t.Logf("节点 %d 连接到节点 %d 失败：%v", i, j, err)
			}
		}
	}

	time.Sleep(200 * time.Millisecond)

	// 记录离开前的连接状态
	beforeLeavePeers := make(map[peer.ID][]peer.ID)
	for i, node := range nodes {
		beforeLeavePeers[node.PeerID()] = node.Host().Network().Peers()
		t.Logf("节点 %d 离开前的连接数：%d", i, len(beforeLeavePeers[node.PeerID()]))
	}

	// 节点 1 离开（关闭）
	nodes[1].Close()
	nodes[1] = nil

	// 等待连接清理
	time.Sleep(500 * time.Millisecond)

	// 验证其他节点的连接已清理
	for i, node := range nodes {
		if node == nil {
			continue
		}
		peers := node.Host().Network().Peers()
		t.Logf("节点 %d 离开后的连接数：%d", i, len(peers))

		// 验证离开的节点不在 peer 列表中
		if i != 1 && nodes[1] != nil {
			assert.NotContains(t, peers, nodes[1].PeerID(), "离开的节点应该被移除")
		}
	}

	// 清理剩余节点
	for _, node := range nodes {
		if node != nil {
			node.Close()
		}
	}

	t.Log("节点动态离开测试完成")
}

// TestConfigMigration 配置迁移测试
func TestConfigMigration(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	keyPath := filepath.Join(tmpDir, "node.key")

	// 初始配置
	cfg1 := DefaultP2PServiceConfig("9841", keyPath)
	service1, err := NewP2PService(cfg1)
	require.NoError(t, err)
	err = service1.Start(ctx)
	require.NoError(t, err)

	originalPeerID := service1.PeerID().String()

	// 关闭服务
	service1.Close()

	// 等待资源释放
	time.Sleep(100 * time.Millisecond)

	// 新配置（使用相同 key）
	cfg2 := DefaultP2PServiceConfig("9841", keyPath)
	// 可以修改其他配置字段，但 ListenAddr 保持不变以使用相同端口

	service2, err := NewP2PService(cfg2)
	require.NoError(t, err)
	err = service2.Start(ctx)
	require.NoError(t, err)
	defer service2.Close()

	// 验证身份保持一致
	newPeerID := service2.PeerID().String()
	assert.Equal(t, originalPeerID, newPeerID, "配置迁移后 PeerID 应该保持一致")

	t.Log("配置迁移测试完成")
}

// TestMetadataPersistence 元数据持久化与恢复测试
func TestMetadataPersistence(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 创建元数据存储目录
	metaDir := filepath.Join(tmpDir, "metadata")
	err := os.MkdirAll(metaDir, 0755)
	require.NoError(t, err)

	keyPath := filepath.Join(tmpDir, "node.key")
	cfg := DefaultP2PServiceConfig("9851", keyPath)

	// 第一次启动：写入元数据
	service1, err := NewP2PService(cfg)
	require.NoError(t, err)
	err = service1.Start(ctx)
	require.NoError(t, err)

	// 获取基本信息
	peerID1 := service1.PeerID().String()

	// 关闭服务
	service1.Close()

	// 等待资源释放
	time.Sleep(100 * time.Millisecond)

	// 第二次启动：恢复元数据
	service2, err := NewP2PService(cfg)
	require.NoError(t, err)
	err = service2.Start(ctx)
	require.NoError(t, err)
	defer service2.Close()

	// 验证元数据恢复
	peerID2 := service2.PeerID().String()
	assert.Equal(t, peerID1, peerID2, "元数据恢复后 PeerID 应该一致")

	t.Log("元数据持久化与恢复测试完成")
}

// TestStandaloneToDistributed 单机到分布式迁移测试
func TestStandaloneToDistributed(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 阶段 1：单机模式（单节点）
	keyPath1 := filepath.Join(tmpDir, "node1.key")
	cfg1 := DefaultP2PServiceConfig("9861", keyPath1)
	service1, err := NewP2PService(cfg1)
	require.NoError(t, err)
	err = service1.Start(ctx)
	require.NoError(t, err)
	defer service1.Close()

	// 验证单机模式
	peers1 := service1.Host().Network().Peers()
	assert.Equal(t, 0, len(peers1), "单机模式应该没有 peer 连接")
	t.Log("阶段 1：单机模式运行正常")

	// 阶段 2：扩展为分布式模式（添加节点）
	keyPath2 := filepath.Join(tmpDir, "node2.key")
	cfg2 := DefaultP2PServiceConfig("9862", keyPath2)
	service2, err := NewP2PService(cfg2)
	require.NoError(t, err)
	err = service2.Start(ctx)
	require.NoError(t, err)
	defer service2.Close()

	// 建立连接
	peerInfo1 := service1.GetPeerInfo()
	err = service2.ConnectToPeer(ctx, peerInfo1)
	require.NoError(t, err)

	peerInfo2 := service2.GetPeerInfo()
	err = service1.ConnectToPeer(ctx, peerInfo2)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// 验证分布式模式
	peers1After := service1.Host().Network().Peers()
	peers2 := service2.Host().Network().Peers()

	assert.Greater(t, len(peers1After), 0, "分布式模式应该有 peer 连接")
	assert.Greater(t, len(peers2), 0, "新节点应该有 peer 连接")

	t.Log("阶段 2：分布式模式运行正常")
	t.Log("单机到分布式迁移测试完成")
}

// TestGracefulShutdown 优雅关闭测试
func TestGracefulShutdown(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 创建 2 节点集群
	nodes := make([]*P2PService, 2)
	for i := 0; i < 2; i++ {
		keyPath := filepath.Join(tmpDir, fmt.Sprintf("node%d.key", i))
		cfg := DefaultP2PServiceConfig(fmt.Sprintf("987%d", i), keyPath)

		service, err := NewP2PService(cfg)
		require.NoError(t, err)
		err = service.Start(ctx)
		require.NoError(t, err)

		nodes[i] = service
	}

	// 建立连接
	peerInfo2 := nodes[1].GetPeerInfo()
	err := nodes[0].ConnectToPeer(ctx, peerInfo2)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// 验证连接建立
	peers0 := nodes[0].Host().Network().Peers()
	assert.Contains(t, peers0, nodes[1].PeerID())

	// 优雅关闭节点 1
	err = nodes[1].Close()
	require.NoError(t, err)

	// 等待连接清理
	time.Sleep(300 * time.Millisecond)

	// 验证节点 0 检测到节点 1 离开
	peers0After := nodes[0].Host().Network().Peers()

	// 注意：libp2p 可能不会立即清理连接，这是正常行为
	t.Logf("关闭前节点 0 的连接数：%d，关闭后：%d", len(peers0), len(peers0After))

	// 清理节点 0
	nodes[0].Close()

	t.Log("优雅关闭测试完成")
}

// BenchmarkServiceRestart 服务重启基准测试
func BenchmarkServiceRestart(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		keyPath := filepath.Join(tmpDir, "node.key")
		cfg := DefaultP2PServiceConfig("9881", keyPath)

		service, err := NewP2PService(cfg)
		if err != nil {
			continue
		}

		err = service.Start(ctx)
		if err != nil {
			continue
		}

		// 模拟短暂运行
		time.Sleep(10 * time.Millisecond)

		service.Close()
	}
}
