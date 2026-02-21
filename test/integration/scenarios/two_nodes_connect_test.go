// Package scenarios 提供集成测试场景实现
package scenarios

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/pkg/test/framework"
	"github.com/jzhang405/NexKV/pkg/test/framework/adapters"
)

// TwoNodesConnectScenario 双节点连接测试场景
// 验证两个节点能够互相发现并建立连接
type TwoNodesConnectScenario struct {
	*framework.BaseScenario

	node1 *adapters.TransportAdapter
	node2 *adapters.TransportAdapter
}

// NewTwoNodesConnectScenario 创建双节点连接测试场景
func NewTwoNodesConnectScenario() *TwoNodesConnectScenario {
	return &TwoNodesConnectScenario{
		BaseScenario: framework.NewBaseScenario("TwoNodesConnect"),
	}
}

// Setup 设置测试环境
func (s *TwoNodesConnectScenario) Setup(ctx context.Context, cluster framework.TestCluster) error {
	// 创建第一个节点
	node1, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		return fmt.Errorf("failed to create node1: %w", err)
	}
	s.node1 = node1

	// 创建第二个节点
	node2, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		return fmt.Errorf("failed to create node2: %w", err)
	}
	s.node2 = node2

	// 启动节点
	if err := s.node1.Start(ctx); err != nil {
		return fmt.Errorf("failed to start node1: %w", err)
	}

	if err := s.node2.Start(ctx); err != nil {
		return fmt.Errorf("failed to start node2: %w", err)
	}

	return nil
}

// Execute 执行测试操作
func (s *TwoNodesConnectScenario) Execute(ctx context.Context, cluster framework.TestCluster) error {
	// Node1 连接到 Node2
	if err := s.node1.ConnectTo(ctx, s.node2); err != nil {
		return fmt.Errorf("node1 failed to connect to node2: %w", err)
	}

	// 等待连接建立
	time.Sleep(100 * time.Millisecond)

	return nil
}

// Verify 验证测试结果
func (s *TwoNodesConnectScenario) Verify(ctx context.Context, cluster framework.TestCluster) error {
	// 验证 Node1 已连接到 Node2
	if !s.node1.IsConnectedTo(s.node2.ID()) {
		return fmt.Errorf("node1 is not connected to node2")
	}

	// 验证 Node2 已连接到 Node1（双向连接）
	if !s.node2.IsConnectedTo(s.node1.ID()) {
		return fmt.Errorf("node2 is not connected to node1")
	}

	// 验证 Node1 的连接列表包含 Node2
	peers := s.node1.GetConnectedPeers()
	if len(peers) == 0 {
		return fmt.Errorf("node1 has no connected peers")
	}

	if !slices.Contains(peers, s.node2.ID()) {
		return fmt.Errorf("node2 not found in node1's connected peers")
	}

	return nil
}

// Teardown 清理测试环境
func (s *TwoNodesConnectScenario) Teardown(ctx context.Context, cluster framework.TestCluster) error {
	// 停止节点
	if s.node1 != nil {
		_ = s.node1.Stop(ctx) //nolint:errcheck // cleanup
	}
	if s.node2 != nil {
		_ = s.node2.Stop(ctx) //nolint:errcheck // cleanup
	}

	return nil
}

// TestTransport_TwoNodesConnect_Success 测试双节点连接成功
func TestTransport_TwoNodesConnect_Success(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 创建测试上下文
	testCtx, err := framework.NewTestContext()
	if err != nil {
		t.Fatalf("Failed to create test context: %v", err)
	}
	defer testCtx.Close()

	// 创建集群
	cluster := framework.NewDefaultCluster("test-cluster", testCtx.Registry)

	// 创建场景执行器
	executor := framework.NewScenarioExecutor(cluster)

	// 执行测试场景
	scenario := NewTwoNodesConnectScenario()
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

// TestTransport_TwoNodesConnect_Timeout 测试双节点连接超时
func TestTransport_TwoNodesConnect_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// 创建测试上下文
	testCtx, err := framework.NewTestContext()
	if err != nil {
		t.Fatalf("Failed to create test context: %v", err)
	}
	defer testCtx.Close()

	// 创建集群
	cluster := framework.NewDefaultCluster("test-cluster", testCtx.Registry)

	// 创建场景执行器
	executor := framework.NewScenarioExecutor(cluster)

	// 执行测试场景（应该因超时而失败）
	scenario := NewTwoNodesConnectScenario()
	result := executor.Execute(ctx, scenario)

	// 验证结果（应该失败）
	if result.Passed {
		t.Error("Expected scenario to fail due to timeout")
	}

	t.Logf("Scenario correctly failed: %v", result.Error)
}

// TestTransport_TwoNodesConnect_HealthCheck 测试节点健康检查
func TestTransport_TwoNodesConnect_HealthCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 创建节点
	node, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}
	defer func() { _ = node.Stop(ctx) }() //nolint:errcheck // cleanup

	// 启动节点
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}

	// 执行健康检查
	if err := node.HealthCheck(ctx); err != nil {
		t.Errorf("Health check failed: %v", err)
	}

	// 验证节点运行状态
	if !node.IsRunning() {
		t.Error("Node should be running")
	}

	// 验证节点健康
	if !node.IsHealthy(ctx) {
		t.Error("Node should be healthy")
	}

	t.Logf("Node ID: %s", node.ID())
	t.Logf("Node Address: %s", node.Address())
}
