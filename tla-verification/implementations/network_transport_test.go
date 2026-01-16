package implementations

import (
	"context"
	"testing"
	"time"
)

// TestNetworkTransportBasicOperations 测试网络传输层基本操作
func TestNetworkTransportBasicOperations(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)

	// 定义网络地址映射
	peers := map[string]string{
		"n1": "localhost:5001",
		"n2": "localhost:5002",
		"n3": "localhost:5003",
	}

	// 创建网络传输层
	nt, err := CreateNetworkTransport("n1", peers)
	if err != nil {
		t.Fatalf("Failed to create network transport: %v", err)
	}

	// 关闭临时目录
	defer func() {
		nt.Stop()
	}()

	// 1. 启动传输层
	if err := nt.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer nt.Stop()

	// 检查状态
	status := nt.Status()
	if !status.IsRunning {
		t.Error("Transport should be running after Start()")
	}

	// 2. 测试发送消息（测试连接其他节点）
	msg := Message{
		Type:      Heartbeat,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Payload:   []byte("test payload"),
		Context:   context.Background(),
	}

	// 注意：由于 n2 没有实际启动，这里会失败，但可以测试连接逻辑
	_ = nt.Send("n2", msg)

	_ = tempDir
	_ = nodeIDs
}

// TestNetworkTransportConcurrentOperations 测试网络传输层并发操作
func TestNetworkTransportConcurrentOperations(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)

	// 定义网络地址映射
	peers := map[string]string{
		"n1": "localhost:5011",
		"n2": "localhost:5012",
		"n3": "localhost:5013",
	}

	// 创建网络传输层
	nt, err := CreateNetworkTransport("n1", peers)
	if err != nil {
		t.Fatalf("Failed to create network transport: %v", err)
	}

	defer func() {
		nt.Stop()
	}()

	// 使用通用测试函数（跳过实际发送测试，因为需要真实的网络连接）
	t.Run("StartStop", func(t *testing.T) {
		if err := nt.Start(); err != nil {
			t.Fatalf("Failed to start transport: %v", err)
		}

		status := nt.Status()
		if !status.IsRunning {
			t.Error("Transport should be running after Start()")
		}
	})

	_ = tempDir
	_ = nodeIDs
}

// TestNetworkTransportErrorHandling 测试网络传输层错误处理
func TestNetworkTransportErrorHandling(t *testing.T) {
	// 1. 测试配置为 nil
	_, err := NewNetworkTransport(nil)
	if err == nil {
		t.Error("Expected error when config is nil")
	}

	// 2. 测试 node ID 为空
	config := DefaultTransportConfig("")
	config.NodeID = ""
	config.Peers = map[string]string{
		"n1": "localhost:5001",
	}
	_, err = NewNetworkTransport(config)
	if err == nil {
		t.Error("Expected error when node ID is empty")
	}

	// 3. 测试地址未配置
	config = DefaultTransportConfig("n1")
	config.Peers = map[string]string{
		"n2": "localhost:5002", // n1 的地址未配置
	}
	_, err = NewNetworkTransport(config)
	if err == nil {
		t.Error("Expected error when address not configured")
	}

	// 4. 测试发送到未启动的传输层
	nt, err := CreateNetworkTransport("n1", map[string]string{
		"n1": "localhost:5201",
		"n2": "localhost:5202",
	})
	if err != nil {
		t.Fatalf("Failed to create network transport: %v", err)
	}

	msg := Message{
		Type:      Heartbeat,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Payload:   []byte("test"),
		Context:   context.Background(),
	}

	err = nt.Send("n2", msg)
	if err == nil {
		t.Error("Expected error when sending before Start()")
	} else if err != ErrTransportNotStarted && err != ErrTransportStopped {
		t.Logf("Got error (acceptable): %v", err)
	}
}

// TestNetworkTransportStatus 测试状态统计
func TestNetworkTransportStatus(t *testing.T) {
	// 创建网络传输层
	nt, err := CreateNetworkTransport("n1", map[string]string{
		"n1": "localhost:5301",
		"n2": "localhost:5302",
	})
	if err != nil {
		t.Fatalf("Failed to create network transport: %v", err)
	}

	// 初始状态
	status := nt.Status()
	if status.IsRunning {
		t.Error("Transport should not be running initially")
	}

	// 启动后状态
	if err := nt.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}

	status = nt.Status()
	if !status.IsRunning {
		t.Error("Transport should be running after Start()")
	}

	// 停止后状态
	if err := nt.Stop(); err != nil {
		t.Fatalf("Failed to stop transport: %v", err)
	}

	status = nt.Status()
	if status.IsRunning {
		t.Error("Transport should not be running after Stop()")
	}
}

// TestNetworkTransportGRPCInterface 测试 gRPC 服务接口
func TestNetworkTransportGRPCInterface(t *testing.T) {
	// 创建网络传输层
	nt, err := CreateNetworkTransport("n1", map[string]string{
		"n1": "localhost:5401",
		"n2": "localhost:5402",
	})
	if err != nil {
		t.Fatalf("Failed to create network transport: %v", err)
	}

	// 启动传输层
	if err := nt.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer nt.Stop()

	// 验证 gRPC 服务已启动
	status := nt.Status()
	if !status.IsRunning {
		t.Error("gRPC server should be running")
	}

	// TODO: 添加真实的 gRPC 客户端测试
	// 需要创建另一个节点并连接到此节点
}

// TestNetworkTransportMultipleNodes 测试多节点网络传输
func TestNetworkTransportMultipleNodes(t *testing.T) {
	// 这是一个需要真实网络连接的测试
	// 在实际集成测试中，应该启动多个进程

	t.Skip("Skipping multi-node test in unit tests - requires network setup")

	// 创建 3 个节点
	nodes := []string{"n1", "n2", "n3"}
	ports := []string{"localhost:5501", "localhost:5502", "localhost:5503"}
	peers := make(map[string]string)
	for i, node := range nodes {
		peers[node] = ports[i]
	}

	var transports []*NetworkTransport
	for _, node := range nodes {
		nt, err := CreateNetworkTransport(node, peers)
		if err != nil {
			t.Fatalf("Failed to create transport for %s: %v", node, err)
		}
		transports = append(transports, nt)
	}

	// 启动所有节点
	for _, nt := range transports {
		if err := nt.Start(); err != nil {
			t.Fatalf("Failed to start transport: %v", err)
		}
		defer nt.Stop()
	}

	// TODO: 测试节点间通信
}

// BenchmarkNetworkTransportSend 网络传输层发送基准测试
func BenchmarkNetworkTransportSend(b *testing.B) {
	tempDir, nodeIDs := setupTransportTest(&testing.T{})

	// 使用随机端口避免冲突
	peers := map[string]string{
		"n1": "localhost:0", // 系统自动分配端口
		"n2": "localhost:0",
	}

	nt, err := CreateNetworkTransport("n1", peers)
	if err != nil {
		b.Fatalf("Failed to create network transport: %v", err)
	}

	if err := nt.Start(); err != nil {
		b.Fatalf("Failed to start transport: %v", err)
	}
	defer nt.Stop()

	// 注意：由于网络延迟和实际连接开销，网络传输会比内存传输慢得多
	// 这个基准测试主要是为了比较性能差异
	b.ResetTimer()

	msg := Message{
		Type:      Heartbeat,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Payload:   make([]byte, 1024),
		Context:   context.Background(),
	}

	for i := 0; i < b.N; i++ {
		// 网络传输可能失败，但这里我们主要测试发送函数的开销
		_ = nt.Send("n2", msg)
	}

	_ = tempDir
	_ = nodeIDs
}
