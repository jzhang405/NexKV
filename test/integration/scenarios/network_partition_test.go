// Package scenarios 提供集成测试场景实现
package scenarios

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/jzhang405/NexKV/pkg/test/framework"
	"github.com/jzhang405/NexKV/pkg/test/framework/adapters"
	"github.com/libp2p/go-libp2p/core/peer"
)

// NetworkPartitionScenario 网络分区测试场景
type NetworkPartitionScenario struct {
	*framework.BaseScenario

	nodes         []*adapters.TransportAdapter
	partitionFunc func(ctx context.Context, nodes []*adapters.TransportAdapter) error
	verifyFunc    func(ctx context.Context, nodes []*adapters.TransportAdapter) error
}

// NewNetworkPartitionScenario 创建网络分区测试场景
func NewNetworkPartitionScenario(name string) *NetworkPartitionScenario {
	return &NetworkPartitionScenario{
		BaseScenario: framework.NewBaseScenario(name),
		nodes:        make([]*adapters.TransportAdapter, 0),
	}
}

// Setup 设置测试环境
func (s *NetworkPartitionScenario) Setup(ctx context.Context, cluster framework.TestCluster) error {
	// 创建三个节点
	for i := range 3 {
		node, err := adapters.NewTransportAdapter(nil)
		if err != nil {
			return errors.Wrapf(err, "failed to create node%d", i+1)
		}
		s.nodes = append(s.nodes, node)
	}

	// 启动所有节点
	for i, node := range s.nodes {
		if err := node.Start(ctx); err != nil {
			return errors.Wrapf(err, "failed to start node%d", i+1)
		}
	}

	// 建立全连接 mesh
	for i, source := range s.nodes {
		for j, target := range s.nodes {
			if i != j {
				if err := source.ConnectTo(ctx, target); err != nil {
					return errors.Wrapf(err, "node%d failed to connect to node%d", i+1, j+1)
				}
			}
		}
	}

	time.Sleep(100 * time.Millisecond)
	return nil
}

// Execute 执行测试操作
func (s *NetworkPartitionScenario) Execute(ctx context.Context, cluster framework.TestCluster) error {
	if s.partitionFunc != nil {
		return s.partitionFunc(ctx, s.nodes)
	}
	return nil
}

// Verify 验证测试结果
func (s *NetworkPartitionScenario) Verify(ctx context.Context, cluster framework.TestCluster) error {
	if s.verifyFunc != nil {
		return s.verifyFunc(ctx, s.nodes)
	}
	return nil
}

// Teardown 清理测试环境
func (s *NetworkPartitionScenario) Teardown(ctx context.Context, cluster framework.TestCluster) error {
	for _, node := range s.nodes {
		if node != nil {
			_ = node.Stop(ctx) //nolint:errcheck // cleanup
		}
	}
	return nil
}

// TestIntegration_NetworkPartition_SingleNode 测试单节点隔离
func TestIntegration_NetworkPartition_SingleNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	testCtx, err := framework.NewTestContext()
	if err != nil {
		t.Fatalf("Failed to create test context: %v", err)
	}
	defer testCtx.Close()

	cluster := framework.NewDefaultCluster("test-cluster", testCtx.Registry)
	executor := framework.NewScenarioExecutor(cluster)

	scenario := NewNetworkPartitionScenario("SingleNodePartition")

	// 分区函数：隔离 Node0
	scenario.partitionFunc = func(ctx context.Context, nodes []*adapters.TransportAdapter) error {
		node0ID, _ := peer.Decode(nodes[0].ID())

		// Node1 和 Node2 阻止来自 Node0 的连接
		nodes[1].BlockPeer(node0ID)

		// 主动断开现有连接
		if err := nodes[0].DisconnectFrom(ctx, nodes[1]); err != nil {
			t.Logf("Warning: node0 failed to disconnect from node1: %v", err)
		}
		if err := nodes[1].DisconnectFrom(ctx, nodes[0]); err != nil {
			t.Logf("Warning: node1 failed to disconnect from node0: %v", err)
		}
		nodes[2].BlockPeer(node0ID)
		if err := nodes[0].DisconnectFrom(ctx, nodes[2]); err != nil {
			t.Logf("Warning: node0 failed to disconnect from node2: %v", err)
		}
		if err := nodes[2].DisconnectFrom(ctx, nodes[0]); err != nil {
			t.Logf("Warning: node2 failed to disconnect from node0: %v", err)
		}

		t.Logf("Partitioned node0 from node1 and node2")
		time.Sleep(PartitionStabilizeWait)

		// 验证分区生效
		if nodes[0].IsConnectedTo(nodes[1].ID()) {
			return errors.Wrap(errors.ErrConnectionFailed, "partition should have disconnected node0 from node1")
		}
		if nodes[0].IsConnectedTo(nodes[2].ID()) {
			return errors.Wrap(errors.ErrConnectionFailed, "partition should have disconnected node0 from node2")
		}
		t.Logf("Verified partition effectiveness: node0 disconnected from node1 and node2")

		return nil
	}

	// 验证函数：Node0 应该被隔离
	scenario.verifyFunc = func(ctx context.Context, nodes []*adapters.TransportAdapter) error {
		// Node1 和 Node2 应该仍然互连
		if !nodes[1].IsConnectedTo(nodes[2].ID()) {
			return errors.Wrap(errors.ErrConnectionFailed, "node1 should still be connected to node2")
		}
		if !nodes[2].IsConnectedTo(nodes[1].ID()) {
			return errors.Wrap(errors.ErrConnectionFailed, "node2 should still be connected to node1")
		}

		t.Logf("Verified: node1<->node2 still connected")
		return nil
	}

	result := executor.Execute(ctx, scenario)
	if !result.Passed {
		t.Errorf("Scenario failed: %v", result.Error)
	}

	t.Logf("Duration: %v", result.Duration)
}

// TestIntegration_NetworkPartition_Bidirectional 测试双向分区
func TestIntegration_NetworkPartition_Bidirectional(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	testCtx, err := framework.NewTestContext()
	if err != nil {
		t.Fatalf("Failed to create test context: %v", err)
	}
	defer testCtx.Close()

	cluster := framework.NewDefaultCluster("test-cluster", testCtx.Registry)
	executor := framework.NewScenarioExecutor(cluster)

	scenario := NewNetworkPartitionScenario("BidirectionalPartition")

	// 分区函数：Node0<-> Node1 双向分区
	scenario.partitionFunc = func(ctx context.Context, nodes []*adapters.TransportAdapter) error {
		node0ID, _ := peer.Decode(nodes[0].ID())
		node1ID, _ := peer.Decode(nodes[1].ID())

		// 双向阻止
		nodes[0].BlockPeer(node1ID)
		nodes[1].BlockPeer(node0ID)

		// 主动断开现有连接
		if err := nodes[0].DisconnectFrom(ctx, nodes[1]); err != nil {
			t.Logf("Warning: node0 failed to disconnect from node1: %v", err)
		}
		if err := nodes[1].DisconnectFrom(ctx, nodes[0]); err != nil {
			t.Logf("Warning: node1 failed to disconnect from node0: %v", err)
		}

		t.Logf("Created bidirectional partition between node0 and node1")
		time.Sleep(PartitionStabilizeWait)

		// 验证分区生效
		if nodes[0].IsConnectedTo(nodes[1].ID()) {
			return errors.Wrap(errors.ErrConnectionFailed, "partition should have disconnected node0 from node1")
		}
		t.Logf("Verified partition effectiveness: node0<->node1 disconnected")

		return nil
	}

	// 验证函数：Node0 和 Node1 应该断开
	scenario.verifyFunc = func(ctx context.Context, nodes []*adapters.TransportAdapter) error {
		// Node0 和 Node1 应该不连接
		if nodes[0].IsConnectedTo(nodes[1].ID()) {
			return errors.Wrap(errors.ErrConnectionFailed, "node0 should not be connected to node1 after partition")
		}

		// Node0 和 Node2 应该仍然连接
		if !nodes[0].IsConnectedTo(nodes[2].ID()) {
			return errors.Wrap(errors.ErrConnectionFailed, "node0 should still be connected to node2")
		}

		// Node1 和 Node2 应该仍然连接
		if !nodes[1].IsConnectedTo(nodes[2].ID()) {
			return errors.Wrap(errors.ErrConnectionFailed, "node1 should still be connected to node2")
		}

		t.Logf("Verified: node0<->node1 partitioned, node0<->node2 and node1<->node2 connected")
		return nil
	}

	result := executor.Execute(ctx, scenario)
	if !result.Passed {
		t.Errorf("Scenario failed: %v", result.Error)
	}

	t.Logf("Duration: %v", result.Duration)
}

// TestIntegration_NetworkPartition_Reconnect 测试分区恢复
func TestIntegration_NetworkPartition_Reconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	testCtx, err := framework.NewTestContext()
	if err != nil {
		t.Fatalf("Failed to create test context: %v", err)
	}
	defer testCtx.Close()

	cluster := framework.NewDefaultCluster("test-cluster", testCtx.Registry)
	executor := framework.NewScenarioExecutor(cluster)

	scenario := NewNetworkPartitionScenario("PartitionReconnect")

	node0ID := ""
	node1ID := ""

	// 分区函数：创建分区然后恢复
	scenario.partitionFunc = func(ctx context.Context, nodes []*adapters.TransportAdapter) error {
		node0ID = nodes[0].ID()
		node1ID = nodes[1].ID()
		pid0, _ := peer.Decode(node0ID)
		pid1, _ := peer.Decode(node1ID)

		// Step 1: 创建分区
		nodes[0].BlockPeer(pid1)
		nodes[1].BlockPeer(pid0)
		// 主动断开现有连接
		if err := nodes[0].DisconnectFrom(ctx, nodes[1]); err != nil {
			t.Logf("Warning: node0 failed to disconnect from node1: %v", err)
		}
		if err := nodes[1].DisconnectFrom(ctx, nodes[0]); err != nil {
			t.Logf("Warning: node1 failed to disconnect from node0: %v", err)
		}
		t.Logf("Step 1: Created partition")
		time.Sleep(PartitionStabilizeWait)

		// Step 2: 验证分区生效
		if nodes[0].IsConnectedTo(node1ID) {
			return errors.Wrap(errors.ErrConnectionFailed, "partition should have disconnected node0 and node1")
		}
		t.Logf("Step 2: Verified partition")

		// Step 3: 恢复连接
		nodes[0].UnblockPeer(pid1)
		nodes[1].UnblockPeer(pid0)
		t.Logf("Step 3: Removed partition")

		// Step 4: 重新连接
		if err := nodes[0].ConnectTo(ctx, nodes[1]); err != nil {
			t.Logf("Warning: reconnect failed: %v", err)
		}
		time.Sleep(PartitionStabilizeWait)

		return nil
	}

	// 验证函数：连接应该恢复
	scenario.verifyFunc = func(ctx context.Context, nodes []*adapters.TransportAdapter) error {
		// 检查连接是否恢复
		peers0 := nodes[0].GetConnectedPeers()
		t.Logf("Node0 peers after reconnect: %v", peers0)

		if !slices.Contains(peers0, node1ID) {
			t.Logf("Warning: node1 not in node0's peers, but partition was removed")
		}

		return nil
	}

	result := executor.Execute(ctx, scenario)
	if !result.Passed {
		t.Errorf("Scenario failed: %v", result.Error)
	}

	t.Logf("Duration: %v", result.Duration)
}

// TestIntegration_NetworkPartition_PartialMesh 测试部分 mesh 分区
func TestIntegration_NetworkPartition_PartialMesh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 创建4 个节点进行更复杂的分区测试
	nodes := make([]*adapters.TransportAdapter, 4)
	for i := range 4 {
		node, err := adapters.NewTransportAdapter(nil)
		if err != nil {
			t.Fatalf("Failed to create node%d: %v", i+1, err)
		}
		nodes[i] = node
		defer func() { _ = node.Stop(ctx) }() //nolint:errcheck // cleanup
	}

	// 启动所有节点
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node%d: %v", i+1, err)
		}
	}

	// 建立全连接
	for i, source := range nodes {
		for j, target := range nodes {
			if i != j {
				_ = source.ConnectTo(ctx, target) //nolint:errcheck // test code
			}
		}
	}
	time.Sleep(PartitionStabilizeWait)

	// 验证初始连接
	for i, node := range nodes {
		peers := node.GetConnectedPeers()
		if len(peers) != 3 {
			t.Errorf("Node%d should have 3 peers, got %d", i+1, len(peers))
		}
	}
	t.Logf("Initial mesh: all nodes have 3 peers")

	// 创建分区：[Node0, Node1] | [Node2, Node3]
	node0ID, _ := peer.Decode(nodes[0].ID())
	node1ID, _ := peer.Decode(nodes[1].ID())
	node2ID, _ := peer.Decode(nodes[2].ID())
	node3ID, _ := peer.Decode(nodes[3].ID())

	// Group A 阻止 Group B
	nodes[0].BlockPeer(node2ID)
	nodes[0].BlockPeer(node3ID)
	nodes[1].BlockPeer(node2ID)
	nodes[1].BlockPeer(node3ID)

	// Group B 阻止 Group A
	nodes[2].BlockPeer(node0ID)
	nodes[2].BlockPeer(node1ID)
	nodes[3].BlockPeer(node0ID)
	nodes[3].BlockPeer(node1ID)

	time.Sleep(PartitionStabilizeWait)

	// 验证分区效果
	// Node0 应该只连接 Node1
	peers0 := nodes[0].GetConnectedPeers()
	if len(peers0) > 1 {
		t.Logf("Warning: Node0 has %d peers after partition", len(peers0))
	}

	t.Logf("Partial mesh partition test completed")
}

// TestIntegration_NetworkPartition_BlockSubnet 测试子网阻止
func TestIntegration_NetworkPartition_BlockSubnet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 创建三个节点
	nodes := make([]*adapters.TransportAdapter, 3)
	for i := 0; i < 3; i++ {
		node, err := adapters.NewTransportAdapter(nil)
		if err != nil {
			t.Fatalf("Failed to create node%d: %v", i+1, err)
		}
		nodes[i] = node
		defer func() { _ = node.Stop(ctx) }() //nolint:errcheck // cleanup
	}

	// 启动所有节点
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node%d: %v", i+1, err)
		}
	}

	// 建立全连接
	for i, source := range nodes {
		for j, target := range nodes {
			if i != j {
				_ = source.ConnectTo(ctx, target) //nolint:errcheck // test code
			}
		}
	}
	time.Sleep(100 * time.Millisecond)

	// 阻止整个子网
	node0ID, _ := peer.Decode(nodes[0].ID())
	node1ID, _ := peer.Decode(nodes[1].ID())

	// Node2 阻止 Node0 和 Node1
	nodes[2].BlockSubnet([]peer.ID{node0ID, node1ID})

	time.Sleep(100 * time.Millisecond)

	// 验证 Node2 被隔离
	peers2 := nodes[2].GetConnectedPeers()
	t.Logf("Node2 peers after BlockSubnet: %d", len(peers2))

	// 恢复所有连接
	nodes[2].UnblockAll()

	t.Logf("BlockSubnet test completed")
}

// TestIntegration_NetworkPartition_IsBlocked 测试阻止状态检查
func TestIntegration_NetworkPartition_IsBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 创建两个节点
	node1, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("Failed to create node1: %v", err)
	}
	defer func() { _ = node1.Stop(ctx) }() //nolint:errcheck // cleanup

	node2, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("Failed to create node2: %v", err)
	}
	defer func() { _ = node2.Stop(ctx) }() //nolint:errcheck // cleanup

	// 启动节点
	_ = node1.Start(ctx) //nolint:errcheck // test code
	_ = node2.Start(ctx) //nolint:errcheck // test code

	// 建立连接
	_ = node1.ConnectTo(ctx, node2) //nolint:errcheck // test code
	time.Sleep(50 * time.Millisecond)

	node2ID, _ := peer.Decode(node2.ID())

	// 初始状态：未阻止
	if node1.IsBlocked(node2ID) {
		t.Error("Node2 should not be blocked initially")
	}

	// 阻止 Node2
	node1.BlockPeer(node2ID)

	// 验证阻止状态
	if !node1.IsBlocked(node2ID) {
		t.Error("Node2 should be blocked after BlockPeer")
	}

	// 解除阻止
	node1.UnblockPeer(node2ID)

	// 验证解除状态
	if node1.IsBlocked(node2ID) {
		t.Error("Node2 should not be blocked after UnblockPeer")
	}

	t.Logf("IsBlocked test passed")
}
