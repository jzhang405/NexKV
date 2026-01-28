// Package cluster 树形协调器集成测试
//
// 集成测试场景：
//   - 多节点协调
//   - 节点加入/离开流程
//   - 集群状态同步
//   - 多层级树形结构
package cluster

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// 多节点集成测试
// ========================================

// TestIntegration_MultiNodeCluster 测试多节点集群管理
//
// 测试场景：
// 1. 启动根节点
// 2. 添加多个子节点
// 3. 查询节点列表
// 4. 移除部分节点
func TestIntegration_MultiNodeCluster(t *testing.T) {
	// 创建根节点

	rootConfig := &TreeCoordinatorConfig{
		MaxChildren:       5,
		MaxLevel:          3,
		HeartbeatInterval: 2 * time.Second,
		AutoDiscovery:     false,
		EnableSelfHealing: false,
	}

	root, err := NewTreeCoordinator("root", "127.0.0.1:9211", rootConfig)
	require.NoError(t, err)
	require.NoError(t, root.Start())
	defer func() { require.NoError(t, root.Stop()) }()

	t.Logf("✅ 根节点 root 已启动")

	// 创建并添加多个子节点
	childCount := 3
	children := make([]*TreeCoordinator, childCount)

	for i := 0; i < childCount; i++ {
		nodeID := fmt.Sprintf("child%d", i+1)
		addr := fmt.Sprintf("127.0.0.1:%d", 9212+i)

		child, err := NewTreeCoordinator(nodeID, addr, rootConfig)
		require.NoError(t, err)
		require.NoError(t, child.Start())
		children[i] = child

		// 将子节点添加到根节点
		err = root.AddChild(nodeID)
		require.NoError(t, err, "添加子节点 %s 应该成功", nodeID)

		// 验证子节点已在根节点的子节点列表中
		assert.Contains(t, root.localNode.ChildrenIDs, nodeID)

		t.Logf("✅ 节点 %s 已加入集群", nodeID)
	}

	// 确保所有子节点都会被清理
	defer func() {
		for i := 0; i < childCount; i++ {
			if children[i] != nil {
				_ = children[i].Stop()
			}
		}
	}()

	// 等待状态稳定
	time.Sleep(100 * time.Millisecond)

	// 测试 ListNodes - 列出所有节点
	nodes := root.ListNodes()
	assert.GreaterOrEqual(t, len(nodes), 1, "至少应该有根节点")

	t.Logf("📊 集群节点列表（共 %d 个）:", len(nodes))
	for _, node := range nodes {
		t.Logf("  - %s: %s (Level: %d, Status: %s)",
			node.NodeID, node.Addr, node.Level, node.Status)
	}

	// 验证根节点存在
	rootNode, err := root.GetNode("root")
	require.NoError(t, err)
	assert.Equal(t, "root", rootNode.NodeID)
	assert.Equal(t, 0, rootNode.Level, "根节点应该在 Level 0")

	// 测试 GetStats - 获取统计信息
	stats := root.GetStats()
	assert.NotNil(t, stats)
	t.Logf("📈 集群统计: TotalNodes=%d, OnlineNodes=%d, TreeDepth=%d",
		stats.TotalNodes.Load(), stats.OnlineNodes.Load(), stats.TreeDepth.Load())

	// 测试 RemoveChild - 移除一个子节点
	err = root.RemoveChild("child1")
	require.NoError(t, err, "移除子节点 child1 应该成功")

	// 验证子节点已从列表中移除
	assert.NotContains(t, root.localNode.ChildrenIDs, "child1")
	t.Logf("✅ 节点 child1 已离开集群")

	// 验证剩余子节点仍然存在
	assert.Contains(t, root.localNode.ChildrenIDs, "child2")
	assert.Contains(t, root.localNode.ChildrenIDs, "child3")

	// 测试移除不存在的节点
	err = root.RemoveChild("notexist")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestIntegration_NodeJoinLeave 测试节点加入和离开
//
// 测试场景：
// 1. 节点加入流程
// 2. 节点离开流程
// 3. 状态验证
func TestIntegration_NodeJoinLeave(t *testing.T) {
	// 创建根节点

	rootConfig := &TreeCoordinatorConfig{
		MaxChildren:       3,
		MaxLevel:          3,
		HeartbeatInterval: 2 * time.Second,
		AutoDiscovery:     false,
		EnableSelfHealing: false,
	}

	root, err := NewTreeCoordinator("root", "127.0.0.1:9211", rootConfig)
	require.NoError(t, err)
	require.NoError(t, root.Start())
	defer func() { require.NoError(t, root.Stop()) }()

	t.Log("=== 阶段 1: 节点加入 ===")

	// 创建子节点并加入

	child, err := NewTreeCoordinator("child1", "127.0.0.1:9212", rootConfig)
	require.NoError(t, err)
	require.NoError(t, child.Start())
	defer func() { require.NoError(t, child.Stop()) }()

	// 子节点加入根节点
	err = root.AddChild("child1")
	require.NoError(t, err)

	// 验证加入成功
	assert.Contains(t, root.localNode.ChildrenIDs, "child1")
	assert.Equal(t, 1, len(root.localNode.ChildrenIDs))
	t.Logf("✅ 节点 child1 已加入 root，ChildrenIDs=%v", root.localNode.ChildrenIDs)

	// 查询根节点状态
	rootNode, _ := root.GetNode("root")
	t.Logf("📊 root 节点状态: Level=%d, ChildrenIDs=%v",
		rootNode.Level, rootNode.ChildrenIDs)

	t.Log("=== 阶段 2: 节点离开 ===")

	// 子节点离开
	err = root.RemoveChild("child1")
	require.NoError(t, err)

	// 验证离开成功
	assert.NotContains(t, root.localNode.ChildrenIDs, "child1")
	assert.Equal(t, 0, len(root.localNode.ChildrenIDs))
	t.Logf("✅ 节点 child1 已离开 root，ChildrenIDs=%v", root.localNode.ChildrenIDs)

	// 验证不能重复移除
	err = root.RemoveChild("child1")
	assert.Error(t, err)
	t.Logf("✅ 重复移除被正确拒绝")
}

// TestIntegration_MaxChildrenConstraint 测试子节点数量限制
//
// 测试场景：
// 1. 设置 MaxChildren = 2
// 2. 尝试添加第 3 个子节点
// 3. 验证添加被拒绝
// 4. 移除一个后可以再添加
func TestIntegration_MaxChildrenConstraint(t *testing.T) {

	rootConfig := &TreeCoordinatorConfig{
		MaxChildren:       2, // 限制为 2 个子节点
		MaxLevel:          3,
		HeartbeatInterval: 2 * time.Second,
		AutoDiscovery:     false,
	}

	root, err := NewTreeCoordinator("root", "127.0.0.1:9211", rootConfig)
	require.NoError(t, err)
	require.NoError(t, root.Start())
	defer func() { require.NoError(t, root.Stop()) }()

	// 添加第 1 个子节点
	err = root.AddChild("child1")
	require.NoError(t, err)
	t.Logf("✅ 添加第 1 个子节点成功")

	// 添加第 2 个子节点
	err = root.AddChild("child2")
	require.NoError(t, err)
	t.Logf("✅ 添加第 2 个子节点成功")

	// 验证子节点数量
	assert.Equal(t, 2, len(root.localNode.ChildrenIDs))

	// 尝试添加第 3 个子节点（应该失败）
	err = root.AddChild("child3")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "已达上限")
	t.Logf("✅ 第 3 个子节点添加被拒绝（已达上限）")

	// 移除一个子节点
	err = root.RemoveChild("child1")
	require.NoError(t, err)

	// 现在可以添加新的子节点
	err = root.AddChild("child3")
	require.NoError(t, err)
	t.Logf("✅ 移除 child1 后，成功添加 child3")

	// 验证子节点列表
	assert.NotContains(t, root.localNode.ChildrenIDs, "child1")
	assert.Contains(t, root.localNode.ChildrenIDs, "child2")
	assert.Contains(t, root.localNode.ChildrenIDs, "child3")
	assert.Equal(t, 2, len(root.localNode.ChildrenIDs))
}

// TestIntegration_ClusterStats 测试集群统计信息
//
// 测试场景：
// 1. 空集群统计
// 2. 添加节点后统计
// 3. 移除节点后统计
func TestIntegration_ClusterStats(t *testing.T) {

	rootConfig := &TreeCoordinatorConfig{
		MaxChildren:       5,
		MaxLevel:          3,
		HeartbeatInterval: 2 * time.Second,
		AutoDiscovery:     false,
		EnableSelfHealing: false,
	}

	root, err := NewTreeCoordinator("root", "127.0.0.1:9211", rootConfig)
	require.NoError(t, err)
	require.NoError(t, root.Start())
	defer func() { require.NoError(t, root.Stop()) }()

	// 初始统计
	stats := root.GetStats()
	t.Logf("📊 初始统计: TotalNodes=%d, OnlineNodes=%d, OfflineNodes=%d, TreeDepth=%d",
		stats.TotalNodes.Load(), stats.OnlineNodes.Load(), stats.OfflineNodes.Load(), stats.TreeDepth.Load())

	assert.Equal(t, int32(1), stats.TotalNodes.Load())
	assert.Equal(t, int32(1), stats.OnlineNodes.Load())
	assert.Equal(t, int32(0), stats.OfflineNodes.Load())
	assert.Equal(t, int32(0), stats.TreeDepth.Load())

	// 添加子节点
	err = root.AddChild("child1")
	require.NoError(t, err)
	err = root.AddChild("child2")
	require.NoError(t, err)

	// 添加后统计
	stats = root.GetStats()
	t.Logf("📊 添加 2 个子节点后: TotalNodes=%d, OnlineNodes=%d, TreeDepth=%d",
		stats.TotalNodes.Load(), stats.OnlineNodes.Load(), stats.TreeDepth.Load())

	// 移除一个子节点
	err = root.RemoveChild("child1")
	require.NoError(t, err)

	// 移除后统计
	stats = root.GetStats()
	t.Logf("📊 移除 1 个子节点后: TotalNodes=%d, OnlineNodes=%d, TreeDepth=%d",
		stats.TotalNodes.Load(), stats.OnlineNodes.Load(), stats.TreeDepth.Load())
}

// TestIntegration_ListNodes 测试列出所有节点
//
// 测试场景：
// 1. 空集群列表
// 2. 添加节点后列表
// 3. 节点信息完整性
func TestIntegration_ListNodes(t *testing.T) {

	rootConfig := &TreeCoordinatorConfig{
		MaxChildren:       3,
		MaxLevel:          3,
		HeartbeatInterval: 2 * time.Second,
		AutoDiscovery:     false,
		EnableSelfHealing: false,
	}

	root, err := NewTreeCoordinator("root", "127.0.0.1:9211", rootConfig)
	require.NoError(t, err)
	require.NoError(t, root.Start())
	defer func() { require.NoError(t, root.Stop()) }()

	// 列出初始节点
	nodes := root.ListNodes()
	t.Logf("📊 初始节点列表（%d 个）:", len(nodes))
	for _, node := range nodes {
		t.Logf("  - %s: %s (Level: %d, Status: %s)",
			node.NodeID, node.Addr, node.Level, node.Status)
	}

	// 添加子节点
	err = root.AddChild("child1")
	require.NoError(t, err)
	err = root.AddChild("child2")
	require.NoError(t, err)

	// 列出添加后的节点
	nodes = root.ListNodes()
	t.Logf("📊 添加节点后列表（%d 个）:", len(nodes))
	for _, node := range nodes {
		t.Logf("  - %s: %s (Level: %d, Status: %s)",
			node.NodeID, node.Addr, node.Level, node.Status)
	}

	// 验证根节点存在
	rootNode, err := root.GetNode("root")
	require.NoError(t, err)
	assert.Equal(t, "root", rootNode.NodeID)
	assert.Equal(t, "127.0.0.1:9211", rootNode.Addr)
	assert.Equal(t, NodeStatusReady, rootNode.Status)
}
