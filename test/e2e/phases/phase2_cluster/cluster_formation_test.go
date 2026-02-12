// Package phase2 多节点集群测试
// 验证多节点集群的协同工作
package phase2

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/test/e2e/framework"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllNodesRunning 测试所有节点正常运行
func TestAllNodesRunning(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, ctx.Err(), "cluster should be created")
	defer cluster.Stop()

	// 启动集群
	require.NoError(t, cluster.Start(ctx), "cluster should start")

	// 等待集群稳定
	assert := framework.NewE2EAssert(t)
	assert.WaitForClusterStable(ctx, cluster, 30*time.Second)

	// 验证所有节点在线
	assert.ClusterOnlineNodes(cluster, 3, "all 3 nodes should be online")
}

// TestGossipSync 测试 Gossip 同步
func TestGossipSync(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 在 node-1 上添加元数据
	// TODO: 添加实际的元数据操作

	// 等待 Gossip 同步
	time.Sleep(5 * time.Second)

	// 验证其他节点也能看到该元数据
	for _, nodeID := range []string{"node-2", "node-3"} {
		nodeCli := cluster.CLI(nodeID)
		status, err := nodeCli.ClusterStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, status.OnlineNodeCount, "should see all nodes")
	}
}

// TestTopologyFormation 测试拓扑形成
func TestTopologyFormation(t *testing.T) {
	ctx := context.Background()

	// 创建 5 节点集群
	cluster := framework.NewTestCluster(5)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 获取集群拓扑
	cli := cluster.CLI("node-1")
	topology, err := cli.ClusterTopology(ctx)
	require.NoError(t, err, "should get cluster topology")

	// 验证拓扑结构
	assert.NotEmpty(t, topology.RootID, "should have a root node")
	assert.NotEmpty(t, topology.TreeNodes, "should have tree nodes")
}

// TestNodeDiscovery 测试节点发现
func TestNodeDiscovery(t *testing.T) {
	ctx := context.Background()

	// 创建 2 节点集群
	cluster := framework.NewTestCluster(2)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 验证 node-1 能发现 node-2
	result1, err := cluster.CLI("node-1").NodeList(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, result1.Nodes, "node-1 should discover nodes")

	// 验证 node-2 能发现 node-1
	result2, err := cluster.CLI("node-2").NodeList(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, result2.Nodes, "node-2 should discover nodes")
}

// TestClusterWithDifferentRoles 测试不同角色的节点
func TestClusterWithDifferentRoles(t *testing.T) {
	// TODO: 实现不同角色的集群创建
	t.Skip("需要实现角色配置")
}

// TestLeaderElection 测试 leader 选举
func TestLeaderElection(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 获取集群状态，验证有 leader
	cli := cluster.CLI("node-1")
	status, err := cli.ClusterStatus(ctx)
	require.NoError(t, err)

	assert.NotEmpty(t, status.LeaderNodeID, "should have a leader")
}

// TestAddNodeToRunningCluster 测试向运行中的集群添加节点
func TestAddNodeToRunningCluster(t *testing.T) {
	ctx := context.Background()

	// 创建 2 节点集群
	cluster := framework.NewTestCluster(2)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 添加新节点
	cli := cluster.CLI("node-1")
	_, err := cli.NodeAdd(ctx, "node-3", "127.0.0.1:19202", 1)
	require.NoError(t, err, "should add new node")

	// 验证新节点在线
	assert := framework.NewE2EAssert(t)
	assert.Eventually(func() bool {
		return cluster.OnlineNodeCount() == 3
	}, 30*time.Second, 1*time.Second, "new node should come online")
}

// TestRemoveNodeFromCluster 测试从集群移除节点
func TestRemoveNodeFromCluster(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 移除一个节点
	cli := cluster.CLI("node-1")
	_, err := cli.NodeRemove(ctx, "node-3")
	require.NoError(t, err, "should remove node")

	// 验证节点已移除
	result, _ := cli.ClusterStatus(ctx)
	assert.Equal(t, 2, result.OnlineNodeCount, "should have 2 online nodes")
}
