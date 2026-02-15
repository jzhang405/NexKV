// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTestCluster(t *testing.T) {
	portAllocator := NewTestPortAllocator()
	dataDirManager := NewDataDirManager(t.TempDir())

	config := &ClusterConfig{
		Name:      "test-cluster",
		NodeCount: 3,
	}

	cluster, err := NewTestCluster(config, portAllocator, dataDirManager)
	require.NoError(t, err)
	require.NotNil(t, cluster)

	assert.Equal(t, "test-cluster", cluster.Config.Name)
	assert.Len(t, cluster.Nodes, 3, "应有 3 个节点")
}

func TestTestCluster_NodeConfiguration(t *testing.T) {
	portAllocator := NewTestPortAllocator()
	dataDirManager := NewDataDirManager(t.TempDir())

	config := &ClusterConfig{
		Name:      "test-cluster",
		NodeCount: 3,
	}

	cluster, err := NewTestCluster(config, portAllocator, dataDirManager)
	require.NoError(t, err)

	for i, node := range cluster.Nodes {
		assert.NotEmpty(t, node.ID, "节点 %d 应有 ID", i)
		assert.NotEmpty(t, node.HostID, "节点 %d 应有 HostID", i)
		assert.NotEmpty(t, node.Addr, "节点 %d 应有地址", i)
		assert.Greater(t, node.RPCPort, 0, "节点 %d 应有 RPC 端口", i)
		assert.NotEmpty(t, node.DataDir, "节点 %d 应有数据目录", i)
	}
}

func TestTestCluster_GetNode(t *testing.T) {
	portAllocator := NewTestPortAllocator()
	dataDirManager := NewDataDirManager(t.TempDir())

	config := &ClusterConfig{
		Name:      "test-cluster",
		NodeCount: 3,
	}

	cluster, err := NewTestCluster(config, portAllocator, dataDirManager)
	require.NoError(t, err)

	// 获取已存在的节点
	node := cluster.GetNode("node-1")
	require.NotNil(t, node)
	assert.Equal(t, "node-1", node.ID)

	// 获取不存在的节点
	node = cluster.GetNode("node-999")
	assert.Nil(t, node)
}

func TestTestCluster_NodeCount(t *testing.T) {
	portAllocator := NewTestPortAllocator()
	dataDirManager := NewDataDirManager(t.TempDir())

	config := &ClusterConfig{
		Name:      "test-cluster",
		NodeCount: 5,
	}

	cluster, err := NewTestCluster(config, portAllocator, dataDirManager)
	require.NoError(t, err)

	assert.Equal(t, 5, cluster.NodeCount())
}

func TestTestCluster_SingleNode(t *testing.T) {
	portAllocator := NewTestPortAllocator()
	dataDirManager := NewDataDirManager(t.TempDir())

	config := &ClusterConfig{
		Name:      "single-node",
		NodeCount: 1,
	}

	cluster, err := NewTestCluster(config, portAllocator, dataDirManager)
	require.NoError(t, err)

	assert.Len(t, cluster.Nodes, 1)
	assert.Equal(t, "node-1", cluster.Nodes[0].ID)
}
