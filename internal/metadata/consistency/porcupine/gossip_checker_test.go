// Package porcupine 测试 Gossip 收敛性检查器
package porcupine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGossipConvergenceChecker_EmptyNodes 测试空节点列表
func TestGossipConvergenceChecker_EmptyNodes(t *testing.T) {
	checker := NewGossipConvergenceChecker(nil, 1*time.Second, 10*time.Millisecond)

	err := checker.WaitForConvergence(context.Background())
	require.NoError(t, err, "空节点列表应该立即返回成功")
}

// TestGossipConvergenceChecker_SingleNode 测试单节点
func TestGossipConvergenceChecker_SingleNode(t *testing.T) {
	nodes := []ConvergenceNode{
		NewSimpleConvergenceNode("node-1", "root-123"),
	}
	checker := NewGossipConvergenceChecker(nodes, 1*time.Second, 10*time.Millisecond)

	err := checker.WaitForConvergence(context.Background())
	require.NoError(t, err, "单节点应该立即返回成功")
}

// TestGossipConvergenceChecker_AlreadyConverged 测试已收敛状态
func TestGossipConvergenceChecker_AlreadyConverged(t *testing.T) {
	nodes := []ConvergenceNode{
		NewSimpleConvergenceNode("node-1", "root-abc"),
		NewSimpleConvergenceNode("node-2", "root-abc"),
		NewSimpleConvergenceNode("node-3", "root-abc"),
	}
	checker := NewGossipConvergenceChecker(nodes, 1*time.Second, 10*time.Millisecond)

	err := checker.WaitForConvergence(context.Background())
	require.NoError(t, err, "所有节点已收敛")
}

// TestGossipConvergenceChecker_NeverConverged 测试永不收敛
func TestGossipConvergenceChecker_NeverConverged(t *testing.T) {
	nodes := []ConvergenceNode{
		NewSimpleConvergenceNode("node-1", "root-abc"),
		NewSimpleConvergenceNode("node-2", "root-def"), // 不同的 root
		NewSimpleConvergenceNode("node-3", "root-ghi"), // 不同的 root
	}
	checker := NewGossipConvergenceChecker(nodes, 100*time.Millisecond, 10*time.Millisecond)

	err := checker.WaitForConvergence(context.Background())
	require.Error(t, err, "节点未收敛应返回错误")

	// 验证错误类型
	convErr, ok := err.(*ConvergenceError)
	require.True(t, ok, "错误应该是 ConvergenceError")
	require.Equal(t, 100*time.Millisecond, convErr.Timeout)
	require.Len(t, convErr.DivergentNodes, 2, "应该有 2 个未收敛节点")
	require.Contains(t, convErr.DivergentNodes, "node-2")
	require.Contains(t, convErr.DivergentNodes, "node-3")
}

// TestGossipConvergenceChecker_EventualConvergence 测试最终收敛
func TestGossipConvergenceChecker_EventualConvergence(t *testing.T) {
	// 创建初始状态不同的节点
	node1 := NewSimpleConvergenceNode("node-1", "root-abc")
	node2 := NewSimpleConvergenceNode("node-2", "root-def")
	node3 := NewSimpleConvergenceNode("node-3", "root-abc")

	nodes := []ConvergenceNode{node1, node2, node3}
	checker := NewGossipConvergenceChecker(nodes, 500*time.Millisecond, 20*time.Millisecond)

	// 启动 goroutine 模拟 Gossip 同步
	go func() {
		time.Sleep(100 * time.Millisecond)
		// node2 同步到相同状态
		node2.SetMerkleRoot("root-abc")
	}()

	err := checker.WaitForConvergence(context.Background())
	require.NoError(t, err, "最终应该收敛")
}

// TestGossipConvergenceChecker_ContextCancellation 测试上下文取消
func TestGossipConvergenceChecker_ContextCancellation(t *testing.T) {
	nodes := []ConvergenceNode{
		NewSimpleConvergenceNode("node-1", "root-abc"),
		NewSimpleConvergenceNode("node-2", "root-def"),
	}
	checker := NewGossipConvergenceChecker(nodes, 5*time.Second, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := checker.WaitForConvergence(ctx)
	require.Error(t, err, "上下文取消应返回错误")
	require.Equal(t, context.DeadlineExceeded, err)
}

// TestGossipConvergenceChecker_Diagnostics 测试诊断信息
func TestGossipConvergenceChecker_Diagnostics(t *testing.T) {
	nodes := []ConvergenceNode{
		NewSimpleConvergenceNode("node-1", "root-abc"),
		NewSimpleConvergenceNode("node-2", "root-def"),
		NewSimpleConvergenceNode("node-3", "root-ghi"),
	}
	checker := NewGossipConvergenceChecker(nodes, 50*time.Millisecond, 10*time.Millisecond)

	err := checker.WaitForConvergence(context.Background())
	require.Error(t, err)

	convErr := err.(*ConvergenceError)
	diagnostics := convErr.GetDiagnostics()

	require.Contains(t, diagnostics, "Convergence Timeout")
	require.Contains(t, diagnostics, "Divergent Nodes")
	require.Contains(t, diagnostics, "node-1")
	require.Contains(t, diagnostics, "node-2")
	require.Contains(t, diagnostics, "node-3")
	require.Contains(t, diagnostics, "root-abc")
	require.Contains(t, diagnostics, "root-def")
	require.Contains(t, diagnostics, "root-ghi")
}

// TestConvergenceError_Error 测试错误消息
func TestConvergenceError_Error(t *testing.T) {
	err := &ConvergenceError{
		Timeout:        5 * time.Second,
		DivergentNodes: []string{"node-2", "node-3"},
	}

	errMsg := err.Error()
	require.Contains(t, errMsg, "5s")
	require.Contains(t, errMsg, "node-2")
	require.Contains(t, errMsg, "node-3")
}

// TestSimpleConvergenceNode 测试简单收敛节点
func TestSimpleConvergenceNode(t *testing.T) {
	node := NewSimpleConvergenceNode("test-node", "test-root")

	require.Equal(t, "test-node", node.GetNodeID())
	require.Equal(t, "test-root", node.GetMerkleRoot())

	// 修改 root
	node.SetMerkleRoot("new-root")
	require.Equal(t, "new-root", node.GetMerkleRoot())
}

// BenchmarkGossipConvergenceChecker 性能基准测试
func BenchmarkGossipConvergenceChecker(b *testing.B) {
	nodes := []ConvergenceNode{
		NewSimpleConvergenceNode("node-1", "root-abc"),
		NewSimpleConvergenceNode("node-2", "root-abc"),
		NewSimpleConvergenceNode("node-3", "root-abc"),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		checker := NewGossipConvergenceChecker(nodes, 1*time.Second, 10*time.Millisecond)
		_ = checker.WaitForConvergence(context.Background())
	}
}
