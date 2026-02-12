// Package phase3 故障注入测试
// 验证集群对节点故障的处理和自愈能力
package phase3

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/test/e2e/framework"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSingleNodeFailure 测试单个节点故障
func TestSingleNodeFailure(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 验证初始状态
	assert.Equal(t, 3, cluster.OnlineNodeCount(), "should have 3 online nodes")

	// 杀死 node-2
	require.NoError(t, cluster.KillNode("node-2"), "should kill node-2")

	// 等待故障检测
	time.Sleep(2 * time.Second)

	// 验证集群检测到故障
	cli := cluster.CLI("node-1")
	result, err := cli.ClusterStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, result.OnlineNodeCount, "should have 2 online nodes after failure")

	// 验证集群仍可操作
	// TODO: 执行一些操作验证集群可用性
}

// TestNodeRecovery 测试节点恢复
func TestNodeRecovery(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 杀死 node-2
	require.NoError(t, cluster.KillNode("node-2"))

	// 等待故障检测
	time.Sleep(2 * time.Second)

	// 重启 node-2
	require.NoError(t, cluster.RestartNode(ctx, "node-2"), "should restart node-2")

	// 验证节点恢复
	assert := framework.NewE2EAssert(t)
	assert.Eventually(func() bool {
		return cluster.OnlineNodeCount() == 3
	}, 30*time.Second, 1*time.Second, "node-2 should recover")

	// 等待 Gossip 同步完成
	cluster.WaitStable(ctx, 30*time.Second)
}

// TestMultipleNodeFailures 测试多个节点故障
func TestMultipleNodeFailures(t *testing.T) {
	ctx := context.Background()

	// 创建 5 节点集群
	cluster := framework.NewTestCluster(5)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 杀死 2 个节点
	require.NoError(t, cluster.KillNode("node-3"))
	require.NoError(t, cluster.KillNode("node-4"))

	// 等待故障检测
	time.Sleep(2 * time.Second)

	// 验证集群仍可运行（剩余 3 个节点）
	assert.Equal(t, 3, cluster.OnlineNodeCount(), "should have 3 online nodes")

	// 验证集群仍可操作
	cli := cluster.CLI("node-1")
	result, err := cli.ClusterStatus(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, result.ClusterID, "cluster should still be functional")
}

// TestLeaderFailure 测试 leader 节点故障
func TestLeaderFailure(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 获取当前 leader
	cli := cluster.CLI("node-1")
	status, err := cli.ClusterStatus(ctx)
	require.NoError(t, err)
	oldLeader := status.LeaderNodeID

	// 杀死 leader
	require.NoError(t, cluster.KillNode(oldLeader), "should kill leader")

	// 等待重新选举
	time.Sleep(5 * time.Second)

	// 验证新 leader 产生
	newStatus, err := cli.ClusterStatus(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, newStatus.LeaderNodeID, "should have new leader")
	assert.NotEqual(t, oldLeader, newStatus.LeaderNodeID, "leader should change")
}

// TestPartitionRecovery 测试网络分区恢复
func TestPartitionRecovery(t *testing.T) {
	// TODO: 实现网络分区模拟
	// 需要使用网络隔离工具（如 iptables、tc 等）
	t.Skip("需要实现网络分区模拟")
}

// TestNodeFailDuringOperation 测试节点在操作过程中故障
func TestNodeFailDuringOperation(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 在执行操作时杀死节点
	// TODO: 启动一个长时间运行的操作
	// 然后杀死节点

	// 验证操作正确处理
}

// TestCascadingFailures 测试级联故障
func TestCascadingFailures(t *testing.T) {
	ctx := context.Background()

	// 创建 5 节点集群
	cluster := framework.NewTestCluster(5)
	require.NoError(t, cluster.Start(ctx))
	defer cluster.Stop()

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 依次杀死多个节点
	for i := 1; i <= 3; i++ {
		nodeID := "node-" + string(rune('0'+i))
		require.NoError(t, cluster.KillNode(nodeID))
		time.Sleep(1 * time.Second)
	}

	// 验证集群仍可运行（剩余 2 个节点）
	assert.Equal(t, 2, cluster.OnlineNodeCount(), "should have 2 online nodes")
}

// TestFullClusterRestart 测试整个集群重启
func TestFullClusterRestart(t *testing.T) {
	ctx := context.Background()

	// 创建 3 节点集群
	cluster := framework.NewTestCluster(3)
	require.NoError(t, cluster.Start(ctx))

	// 等待集群稳定
	cluster.WaitStable(ctx, 30*time.Second)

	// 停止所有节点
	require.NoError(t, cluster.Stop(), "should stop cluster")

	// 等待停止完成
	time.Sleep(2 * time.Second)

	// 重新启动
	require.NoError(t, cluster.Start(ctx), "should restart cluster")
	defer cluster.Stop()

	// 验证集群恢复
	cluster.WaitStable(ctx, 30*time.Second)
	assert.Equal(t, 3, cluster.OnlineNodeCount(), "all nodes should be online")
}
