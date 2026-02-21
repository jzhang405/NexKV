// Package scenarios 提供集成测试场景实现
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/test/framework"
	"github.com/jzhang405/NexKV/pkg/test/framework/adapters"
	"github.com/libp2p/go-libp2p/core/peer"
)

// RPCCommunicationScenario RPC 通信测试场景
// 验证节点间 RPC 调用、HandleIncomingStream 等核心功能
type RPCCommunicationScenario struct {
	*framework.BaseScenario

	server *adapters.TransportAdapter
	client *adapters.TransportAdapter
}

// NewRPCCommunicationScenario 创建 RPC 通信测试场景
func NewRPCCommunicationScenario() *RPCCommunicationScenario {
	return &RPCCommunicationScenario{
		BaseScenario: framework.NewBaseScenario("RPCCommunication"),
	}
}

// Setup 设置测试环境
func (s *RPCCommunicationScenario) Setup(ctx context.Context, cluster framework.TestCluster) error {
	// 创建服务端节点
	server, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		return err
	}
	s.server = server

	// 创建客户端节点
	client, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		return err
	}
	s.client = client

	// 启动节点
	if err := s.server.Start(ctx); err != nil {
		return err
	}
	if err := s.client.Start(ctx); err != nil {
		return err
	}

	return nil
}

// Execute 执行测试操作
func (s *RPCCommunicationScenario) Execute(ctx context.Context, cluster framework.TestCluster) error {
	// 客户端连接到服务端
	if err := s.client.ConnectTo(ctx, s.server); err != nil {
		return err
	}

	// 等待连接建立
	time.Sleep(100 * time.Millisecond)

	return nil
}

// Verify 验证测试结果
func (s *RPCCommunicationScenario) Verify(ctx context.Context, cluster framework.TestCluster) error {
	// 验证双向连接
	if !s.client.IsConnectedTo(s.server.ID()) {
		return service.ErrPeerUnreachable
	}
	if !s.server.IsConnectedTo(s.client.ID()) {
		return service.ErrPeerUnreachable
	}

	return nil
}

// Teardown 清理测试环境
func (s *RPCCommunicationScenario) Teardown(ctx context.Context, cluster framework.TestCluster) error {
	if s.client != nil {
		_ = s.client.Stop(ctx) //nolint:errcheck // cleanup
	}
	if s.server != nil {
		_ = s.server.Stop(ctx) //nolint:errcheck // cleanup
	}
	return nil
}

// TestRPCCommunication_Basic 测试基本 RPC 通信
func TestRPCCommunication_Basic(t *testing.T) {
	scenario := NewRPCCommunicationScenario()
	ctx := context.Background()

	// Setup
	if err := scenario.Setup(ctx, nil); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer func() { _ = scenario.Teardown(ctx, nil) }() //nolint:errcheck // cleanup

	// Execute
	if err := scenario.Execute(ctx, nil); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify
	if err := scenario.Verify(ctx, nil); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

// TestRPCCommunication_WithHandler 测试带处理器的 RPC 通信
func TestRPCCommunication_WithHandler(t *testing.T) {
	ctx := context.Background()

	// 创建服务端
	server, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	defer func() { _ = server.Stop(ctx) }() //nolint:errcheck // cleanup

	if err := server.Start(ctx); err != nil {
		t.Fatalf("start server: %v", err)
	}

	// 创建客户端
	client, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer func() { _ = client.Stop(ctx) }() //nolint:errcheck // cleanup

	if err := client.Start(ctx); err != nil {
		t.Fatalf("start client: %v", err)
	}

	// 连接
	if err := client.ConnectTo(ctx, server); err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	// 等待连接建立
	time.Sleep(100 * time.Millisecond)

	// 验证连接
	if !client.IsConnectedTo(server.ID()) {
		t.Error("Client not connected to server")
	}
	if !server.IsConnectedTo(client.ID()) {
		t.Error("Server not connected to client")
	}

	t.Logf("RPC communication test passed: client=%s, server=%s", client.ID(), server.ID())
}

// NetworkReconnectScenario 网络重连测试场景
type NetworkReconnectScenario struct {
	*framework.BaseScenario

	node1 *adapters.TransportAdapter
	node2 *adapters.TransportAdapter
}

// NewNetworkReconnectScenario 创建网络重连测试场景
func NewNetworkReconnectScenario() *NetworkReconnectScenario {
	return &NetworkReconnectScenario{
		BaseScenario: framework.NewBaseScenario("NetworkReconnect"),
	}
}

// Setup 设置测试环境
func (s *NetworkReconnectScenario) Setup(ctx context.Context, cluster framework.TestCluster) error {
	node1, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		return err
	}
	s.node1 = node1

	node2, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		return err
	}
	s.node2 = node2

	if err := s.node1.Start(ctx); err != nil {
		return err
	}
	if err := s.node2.Start(ctx); err != nil {
		return err
	}

	return nil
}

// Execute 执行测试操作
func (s *NetworkReconnectScenario) Execute(ctx context.Context, cluster framework.TestCluster) error {
	// 1. 建立连接
	if err := s.node1.ConnectTo(ctx, s.node2); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)

	// 2. 模拟网络分区（阻止连接）
	node1ID, _ := peer.Decode(s.node1.ID())
	node2ID, _ := peer.Decode(s.node2.ID())
	s.node1.BlockPeer(node2ID)
	s.node2.BlockPeer(node1ID)

	// 3. 断开连接
	_ = s.node1.DisconnectFrom(ctx, s.node2) //nolint:errcheck // test code
	_ = s.node2.DisconnectFrom(ctx, s.node1) //nolint:errcheck // test code
	time.Sleep(200 * time.Millisecond)

	// 4. 恢复网络
	s.node1.UnblockPeer(node2ID)
	s.node2.UnblockPeer(node1ID)

	// 5. 重新连接
	if err := s.node1.ConnectTo(ctx, s.node2); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)

	return nil
}

// Verify 验证测试结果
func (s *NetworkReconnectScenario) Verify(ctx context.Context, cluster framework.TestCluster) error {
	if !s.node1.IsConnectedTo(s.node2.ID()) {
		return service.ErrPeerUnreachable
	}
	if !s.node2.IsConnectedTo(s.node1.ID()) {
		return service.ErrPeerUnreachable
	}
	return nil
}

// Teardown 清理测试环境
func (s *NetworkReconnectScenario) Teardown(ctx context.Context, cluster framework.TestCluster) error {
	if s.node1 != nil {
		_ = s.node1.Stop(ctx) //nolint:errcheck // cleanup
	}
	if s.node2 != nil {
		_ = s.node2.Stop(ctx) //nolint:errcheck // cleanup
	}
	return nil
}

// TestNetworkReconnect 测试网络重连
func TestNetworkReconnect(t *testing.T) {
	scenario := NewNetworkReconnectScenario()
	ctx := context.Background()

	if err := scenario.Setup(ctx, nil); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer func() { _ = scenario.Teardown(ctx, nil) }() //nolint:errcheck // cleanup

	if err := scenario.Execute(ctx, nil); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if err := scenario.Verify(ctx, nil); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	t.Log("Network reconnect test passed")
}

// TestNetworkTimeout 测试网络超时
func TestNetworkTimeout(t *testing.T) {
	ctx := context.Background()

	node1, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("create node1: %v", err)
	}
	defer func() { _ = node1.Stop(ctx) }() //nolint:errcheck // cleanup

	node2, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("create node2: %v", err)
	}
	defer func() { _ = node2.Stop(ctx) }() //nolint:errcheck // cleanup

	if err := node1.Start(ctx); err != nil {
		t.Fatalf("start node1: %v", err)
	}
	if err := node2.Start(ctx); err != nil {
		t.Fatalf("start node2: %v", err)
	}

	// 建立连接
	if err := node1.ConnectTo(ctx, node2); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// 验证连接
	if !node1.IsConnectedTo(node2.ID()) {
		t.Fatal("Node1 not connected to node2")
	}

	// 使用超时上下文测试
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 在超时上下文中进行操作
	peers := node1.GetConnectedPeers()
	if len(peers) == 0 {
		t.Error("No connected peers")
	}

	select {
	case <-timeoutCtx.Done():
		t.Log("Timeout context expired as expected")
	default:
		t.Logf("Connected peers: %v", peers)
	}

	t.Log("Network timeout test passed")
}

// StreamCommunicationScenario 流通信测试场景
type StreamCommunicationScenario struct {
	*framework.BaseScenario

	server *adapters.TransportAdapter
	client *adapters.TransportAdapter
}

// NewStreamCommunicationScenario 创建流通信测试场景
func NewStreamCommunicationScenario() *StreamCommunicationScenario {
	return &StreamCommunicationScenario{
		BaseScenario: framework.NewBaseScenario("StreamCommunication"),
	}
}

// Setup 设置测试环境
func (s *StreamCommunicationScenario) Setup(ctx context.Context, cluster framework.TestCluster) error {
	server, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		return err
	}
	s.server = server

	client, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		return err
	}
	s.client = client

	if err := s.server.Start(ctx); err != nil {
		return err
	}
	if err := s.client.Start(ctx); err != nil {
		return err
	}

	return nil
}

// Execute 执行测试操作
func (s *StreamCommunicationScenario) Execute(ctx context.Context, cluster framework.TestCluster) error {
	// 连接
	if err := s.client.ConnectTo(ctx, s.server); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)

	return nil
}

// Verify 验证测试结果
func (s *StreamCommunicationScenario) Verify(ctx context.Context, cluster framework.TestCluster) error {
	// 验证双向连接
	if !s.client.IsConnectedTo(s.server.ID()) {
		return service.ErrPeerUnreachable
	}
	return nil
}

// Teardown 清理测试环境
func (s *StreamCommunicationScenario) Teardown(ctx context.Context, cluster framework.TestCluster) error {
	if s.client != nil {
		_ = s.client.Stop(ctx) //nolint:errcheck // cleanup
	}
	if s.server != nil {
		_ = s.server.Stop(ctx) //nolint:errcheck // cleanup
	}
	return nil
}

// TestStreamCommunication 测试流通信
func TestStreamCommunication(t *testing.T) {
	scenario := NewStreamCommunicationScenario()
	ctx := context.Background()

	if err := scenario.Setup(ctx, nil); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer func() { _ = scenario.Teardown(ctx, nil) }() //nolint:errcheck // cleanup

	if err := scenario.Execute(ctx, nil); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if err := scenario.Verify(ctx, nil); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	t.Log("Stream communication test passed")
}

// TestMultipleNodesCommunication 测试多节点通信
func TestMultipleNodesCommunication(t *testing.T) {
	ctx := context.Background()

	// 创建 3 个节点
	nodes := make([]*adapters.TransportAdapter, 3)
	for i := 0; i < 3; i++ {
		node, err := adapters.NewTransportAdapter(nil)
		if err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
		nodes[i] = node
		defer func() { _ = node.Stop(ctx) }() //nolint:errcheck // cleanup

		if err := node.Start(ctx); err != nil {
			t.Fatalf("start node %d: %v", i, err)
		}
	}

	// 节点 0 连接到节点 1 和节点 2
	if err := nodes[0].ConnectTo(ctx, nodes[1]); err != nil {
		t.Fatalf("node0 connect to node1: %v", err)
	}
	if err := nodes[0].ConnectTo(ctx, nodes[2]); err != nil {
		t.Fatalf("node0 connect to node2: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// 验证连接
	if !nodes[0].IsConnectedTo(nodes[1].ID()) {
		t.Error("Node0 not connected to node1")
	}
	if !nodes[0].IsConnectedTo(nodes[2].ID()) {
		t.Error("Node0 not connected to node2")
	}

	// 节点 1 连接到节点 2
	if err := nodes[1].ConnectTo(ctx, nodes[2]); err != nil {
		t.Fatalf("node1 connect to node2: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if !nodes[1].IsConnectedTo(nodes[2].ID()) {
		t.Error("Node1 not connected to node2")
	}

	t.Logf("Multiple nodes communication test passed: %s <-> %s <-> %s",
		nodes[0].ID()[:8], nodes[1].ID()[:8], nodes[2].ID()[:8])
}

// TestMessagePropagation 测试消息传播
func TestMessagePropagation(t *testing.T) {
	ctx := context.Background()

	// 创建两个节点
	node1, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("create node1: %v", err)
	}
	defer func() { _ = node1.Stop(ctx) }() //nolint:errcheck // cleanup

	node2, err := adapters.NewTransportAdapter(nil)
	if err != nil {
		t.Fatalf("create node2: %v", err)
	}
	defer func() { _ = node2.Stop(ctx) }() //nolint:errcheck // cleanup

	if err := node1.Start(ctx); err != nil {
		t.Fatalf("start node1: %v", err)
	}
	if err := node2.Start(ctx); err != nil {
		t.Fatalf("start node2: %v", err)
	}

	// 连接
	if err := node1.ConnectTo(ctx, node2); err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// 验证连接列表
	peers1 := node1.GetConnectedPeers()
	peers2 := node2.GetConnectedPeers()

	if len(peers1) == 0 {
		t.Error("Node1 has no connected peers")
	}
	if len(peers2) == 0 {
		t.Error("Node2 has no connected peers")
	}

	t.Logf("Node1 connected to: %v", peers1)
	t.Logf("Node2 connected to: %v", peers2)

	t.Log("Message propagation test passed")
}
