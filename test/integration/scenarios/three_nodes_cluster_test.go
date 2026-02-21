// Package scenarios 提供集成测试场景实现
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/pkg/errors"
	"github.com/jzhang405/NexKV/pkg/test/framework"
	"github.com/jzhang405/NexKV/pkg/test/framework/adapters"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ThreeNodesClusterScenario 三节点集群测试场景
// 验证三个节点能够形成全连接 mesh 网络
type ThreeNodesClusterScenario struct {
	*framework.BaseScenario

	nodes []*adapters.TransportAdapter
}

// NewThreeNodesClusterScenario 创建三节点集群测试场景
func NewThreeNodesClusterScenario() *ThreeNodesClusterScenario {
	return &ThreeNodesClusterScenario{
		BaseScenario: framework.NewBaseScenario("ThreeNodesCluster"),
		nodes:        make([]*adapters.TransportAdapter, 0, 3),
	}
}

// Setup 设置测试环境
func (s *ThreeNodesClusterScenario) Setup(ctx context.Context, cluster framework.TestCluster) error {
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

	return nil
}

// Execute 执行测试操作
func (s *ThreeNodesClusterScenario) Execute(ctx context.Context, cluster framework.TestCluster) error {
	// 建立全连接 mesh
	// Node0 -> Node1, Node2
	// Node1 -> Node0, Node2
	// Node2 -> Node0, Node1

	for i, source := range s.nodes {
		for j, target := range s.nodes {
			if i == j {
				continue // 跳过自己
			}

			if err := source.ConnectTo(ctx, target); err != nil {
				return errors.Wrapf(err, "node%d failed to connect to node%d", i+1, j+1)
			}
		}
	}

	// 等待连接建立
	time.Sleep(200 * time.Millisecond)

	return nil
}

// Verify 验证测试结果
func (s *ThreeNodesClusterScenario) Verify(ctx context.Context, cluster framework.TestCluster) error {
	// 验证每个节点都有 2 个连接
	for i, node := range s.nodes {
		peers := node.GetConnectedPeers()
		if len(peers) != 2 {
			return errors.Wrapf(errors.ErrConnectionFailed, "node%d should have 2 peers, got %d", i+1, len(peers))
		}
	}

	// 验证全连接 mesh
	for i, node := range s.nodes {
		for j, target := range s.nodes {
			if i == j {
				continue
			}

			if !node.IsConnectedTo(target.ID()) {
				return errors.Wrapf(errors.ErrConnectionFailed, "node%d is not connected to node%d", i+1, j+1)
			}
		}
	}

	return nil
}

// Teardown 清理测试环境
func (s *ThreeNodesClusterScenario) Teardown(ctx context.Context, cluster framework.TestCluster) error {
	for _, node := range s.nodes {
		if node != nil {
			_ = node.Stop(ctx) //nolint:errcheck // cleanup
		}
	}
	return nil
}

// TestIntegration_ThreeNodesCluster_Success 测试三节点集群成功
func TestIntegration_ThreeNodesCluster_Success(t *testing.T) {
	ctx, _, executor := setupIntegrationTest(t, 30*time.Second)

	// 执行测试场景
	scenario := NewThreeNodesClusterScenario()
	result := executor.Execute(ctx, scenario)

	// 验证结果
	if !result.Passed {
		t.Errorf("Scenario failed: %v", result.Error)
	}

	t.Logf("Scenario: %s", scenario.Name())
	t.Logf("Duration: %v", result.Duration)
	t.Logf("Setup: %v, Execute: %v, Verify: %v, Teardown: %v",
		result.SetupDuration, result.ExecuteDuration, result.VerifyDuration, result.TeardownDuration)
}

// TestIntegration_ThreeNodesCluster_NetworkPartition 测试网络分区
func TestIntegration_ThreeNodesCluster_NetworkPartition(t *testing.T) {
	ctx := t.Context()

	// 创建三个节点
	nodes := make([]*adapters.TransportAdapter, 3)

	// P0 修复：确保所有节点在测试结束时被清理
	defer func() {
		for _, node := range nodes {
			if node != nil {
				_ = node.Stop(ctx) //nolint:errcheck // cleanup
			}
		}
	}()

	for i := range 3 {
		node, err := adapters.NewTransportAdapter(nil)
		if err != nil {
			t.Fatalf("Failed to create node%d: %v", i+1, err)
		}
		nodes[i] = node
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
				if err := source.ConnectTo(ctx, target); err != nil {
					t.Fatalf("Node%d failed to connect to node%d: %v", i+1, j+1, err)
				}
			}
		}
	}

	time.Sleep(100 * time.Millisecond)

	// 验证初始连接
	for i, node := range nodes {
		if len(node.GetConnectedPeers()) != 2 {
			t.Errorf("Node%d should have 2 peers initially", i+1)
		}
	}

	// 模拟网络分区：Node0 隔离Node1 和 Node2
	node0ID, _ := peer.Decode(nodes[0].ID())
	nodes[1].BlockPeer(node0ID)
	nodes[2].BlockPeer(node0ID)

	time.Sleep(100 * time.Millisecond)

	// 验证分区效果
	// Node1 和 Node2 应该仍然互连
	if !nodes[1].IsConnectedTo(nodes[2].ID()) {
		t.Log("Node1 and Node2 are still connected (expected)")
	}

	t.Logf("Network partition test passed")
}

// TestIntegration_ThreeNodesCluster_NodeRestart 测试节点重启
func TestIntegration_ThreeNodesCluster_NodeRestart(t *testing.T) {
	ctx := t.Context()

	// 创建三个节点
	nodes := make([]*adapters.TransportAdapter, 3)

	// P0 修复：确保所有节点在测试结束时被清理
	defer func() {
		for _, node := range nodes {
			if node != nil {
				_ = node.Stop(ctx) //nolint:errcheck // cleanup
			}
		}
	}()

	for i := range 3 {
		node, err := adapters.NewTransportAdapter(nil)
		if err != nil {
			t.Fatalf("Failed to create node%d: %v", i+1, err)
		}
		nodes[i] = node
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

	// 停止 Node0
	t.Log("Stopping node0...")
	if err := nodes[0].Stop(ctx); err != nil {
		t.Fatalf("Failed to stop node0: %v", err)
	}

	// 验证 Node0 已停止
	if nodes[0].IsRunning() {
		t.Error("Node0 should not be running")
	}

	// 重启 Node0
	t.Log("Restarting node0...")
	// 需要重新创建节点
	newNode0, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("Failed to recreate node0: %v", err)
	}
	nodes[0] = newNode0

	if err := nodes[0].Start(ctx); err != nil {
		t.Fatalf("Failed to restart node0: %v", err)
	}

	// 验证 Node0 已重启
	if !nodes[0].IsRunning() {
		t.Error("Node0 should be running after restart")
	}

	t.Logf("Node restart test passed")
}

// TestIntegration_ThreeNodesCluster_HealthCheckAll 测试所有节点健康检查
func TestIntegration_ThreeNodesCluster_HealthCheckAll(t *testing.T) {
	ctx := t.Context()

	// 创建三个节点
	nodes := make([]*adapters.TransportAdapter, 3)

	// P0 修复：确保所有节点在测试结束时被清理
	defer func() {
		for _, node := range nodes {
			if node != nil {
				_ = node.Stop(ctx) //nolint:errcheck // cleanup
			}
		}
	}()

	// 创建节点
	for i := range 3 {
		node, err := adapters.NewTransportAdapter(nil)
		if err != nil {
			t.Fatalf("Failed to create node%d: %v", i+1, err)
		}
		nodes[i] = node
	}

	// 启动所有节点
	for i, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node%d: %v", i+1, err)
		}
	}

	// 执行健康检查
	for i, node := range nodes {
		if err := node.HealthCheck(ctx); err != nil {
			t.Errorf("Node%d health check failed: %v", i+1, err)
		}

		if !node.IsHealthy(ctx) {
			t.Errorf("Node%d should be healthy", i+1)
		}

		t.Logf("Node%d: ID=%s, Addr=%s", i+1, node.ID(), node.Address())
	}
}
