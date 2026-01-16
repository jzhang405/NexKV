package implementations

import (
	"context"
	"testing"
	"time"
)

// TestMemoryTransportBasicOperations 测试内存传输层基本操作
func TestMemoryTransportBasicOperations(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 创建内存传输层
	mt, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		t.Fatalf("Failed to create memory transport: %v", err)
	}

	// 使用通用测试函数
	RunTransportBasicOperations(t, mt)
}

// TestMemoryTransportConcurrentOperations 测试内存传输层并发操作
func TestMemoryTransportConcurrentOperations(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 创建内存传输层
	mt, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		t.Fatalf("Failed to create memory transport: %v", err)
	}

	// 使用通用测试函数
	RunTransportConcurrentOperations(t, mt)
}

// TestMemoryTransportErrorHandling 测试内存传输层错误处理
func TestMemoryTransportErrorHandling(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 创建内存传输层
	mt, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		t.Fatalf("Failed to create memory transport: %v", err)
	}

	// 使用通用测试函数
	RunTransportErrorHandling(t, mt)
}

// TestMemoryTransportGossipExchange 测试 Gossip 交换
func TestMemoryTransportGossipExchange(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 创建传输层
	mt1, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		t.Fatalf("Failed to create memory transport: %v", err)
	}

	mt2, err := CreateMemoryTransport("n2", cluster)
	if err != nil {
		t.Fatalf("Failed to create memory transport: %v", err)
	}

	// 启动传输层
	if err := mt1.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer mt1.Stop()

	if err := mt2.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer mt2.Stop()

	// 节点发起投票
	n1 := cluster.GetNode("n1")
	n2 := cluster.GetNode("n2")

	if !n1.ProposeVote(cluster.Version) {
		t.Error("Failed to propose vote on n1")
	}

	if !n2.ProposeVote(cluster.Version) {
		t.Error("Failed to propose vote on n2")
	}

	// 发送 Gossip 消息
	msg := Message{
		Type:      GossipExchange,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Context:   context.Background(),
	}

	if err := mt1.Send("n2", msg); err != nil {
		t.Errorf("Failed to send gossip message: %v", err)
	}

	// 验证状态同步
	decision1, version1, seen1, _ := n1.GetState()
	decision2, version2, seen2, _ := n2.GetState()

	if version1 != version2 {
		t.Errorf("Version mismatch: n1=%d, n2=%d", version1, version2)
	}

	if len(seen1) != len(seen2) {
		t.Errorf("Seen count mismatch: n1=%d, n2=%d", len(seen1), len(seen2))
	}

	if decision1 != decision2 {
		t.Errorf("Decision mismatch: n1=%s, n2=%s", decision1, decision2)
	}
}

// TestMemoryTransportNetworkPartition 测试网络分区场景
func TestMemoryTransportNetworkPartition(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 创建传输层
	mt, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		t.Fatalf("Failed to create memory transport: %v", err)
	}

	if err := mt.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer mt.Stop()

	// 创建网络分区：n1 与 n2,n3 隔离
	partition1 := []string{"n1"}
	partition2 := []string{"n2", "n3"}

	if err := cluster.CreatePartition(partition1, partition2); err != nil {
		t.Fatalf("Failed to create partition: %v", err)
	}

	// 尝试向 n2 发送消息（应该失败）
	msg := Message{
		Type:      GossipExchange,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Context:   context.Background(),
	}

	err = mt.Send("n2", msg)
	if err == nil {
		t.Error("Expected error when sending across partition")
	}

	// 恢复分区
	if err := cluster.HealPartition(); err != nil {
		t.Fatalf("Failed to heal partition: %v", err)
	}

	// 再次尝试发送（应该成功）
	if err := mt.Send("n2", msg); err != nil {
		t.Errorf("Failed to send after healing partition: %v", err)
	}
}

// TestMemoryTransportBroadcast 测试广播功能
func TestMemoryTransportBroadcast(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 创建传输层
	mt, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		t.Fatalf("Failed to create memory transport: %v", err)
	}

	if err := mt.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}
	defer mt.Stop()

	// 广播心跳消息
	msg := Message{
		Type:      Heartbeat,
		From:      "n1",
		To:        "", // 广播
		Timestamp: time.Now().UnixNano(),
		Payload:   []byte("heartbeat"),
		Context:   context.Background(),
	}

	if err := mt.Broadcast(msg); err != nil {
		t.Errorf("Failed to broadcast: %v", err)
	}

	// 验证状态
	status := mt.Status()
	if status.MessagesSent < 2 { // 应该至少发送 2 条消息（n2, n3）
		t.Errorf("Expected at least 2 messages sent, got %d", status.MessagesSent)
	}
}

// TestMemoryTransportStatus 测试状态统计
func TestMemoryTransportStatus(t *testing.T) {
	// 设置测试环境
	tempDir, nodeIDs := setupTransportTest(t)
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	// 创建传输层
	mt, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		t.Fatalf("Failed to create memory transport: %v", err)
	}

	// 初始状态
	status := mt.Status()
	if status.IsRunning {
		t.Error("Transport should not be running initially")
	}

	// 启动后状态
	if err := mt.Start(); err != nil {
		t.Fatalf("Failed to start transport: %v", err)
	}

	status = mt.Status()
	if !status.IsRunning {
		t.Error("Transport should be running after Start()")
	}

	// 发送消息后状态
	msg := Message{
		Type:      Heartbeat,
		From:      "n1",
		To:        "n2",
		Timestamp: time.Now().UnixNano(),
		Payload:   []byte("test"),
		Context:   context.Background(),
	}

	if err := mt.Send("n2", msg); err != nil {
		t.Errorf("Failed to send message: %v", err)
	}

	status = mt.Status()
	if status.MessagesSent == 0 {
		t.Error("Expected MessagesSent > 0 after sending")
	}

	if status.BytesSent == 0 {
		t.Error("Expected BytesSent > 0 after sending")
	}

	// 停止后状态
	if err := mt.Stop(); err != nil {
		t.Errorf("Failed to stop transport: %v", err)
	}

	status = mt.Status()
	if status.IsRunning {
		t.Error("Transport should not be running after Stop()")
	}
}

// BenchmarkMemoryTransportSend 内存传输层发送基准测试
func BenchmarkMemoryTransportSend(b *testing.B) {
	tempDir, nodeIDs := setupTransportTest(&testing.T{})
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	mt, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		b.Fatalf("Failed to create memory transport: %v", err)
	}

	RunBenchmarkTransportSend(b, mt)
}

// BenchmarkMemoryTransportReceive 内存传输层接收基准测试
func BenchmarkMemoryTransportReceive(b *testing.B) {
	tempDir, nodeIDs := setupTransportTest(&testing.T{})
	cluster := NewCluster(nodeIDs, tempDir)
	defer cluster.Close()

	mt, err := CreateMemoryTransport("n1", cluster)
	if err != nil {
		b.Fatalf("Failed to create memory transport: %v", err)
	}

	RunBenchmarkTransportReceive(b, mt)
}
