// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现 Gossip 收敛性检查器，基于 Merkle Tree 检测节点收敛状态
package porcupine

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MerkleRootProvider Merkle Root 提供者接口
// TreeTopologyCoordinator 已实现此接口（GetMerkleRoot() string）
type MerkleRootProvider interface {
	// GetMerkleRoot 获取本地 Merkle Tree 的 Global Root Hash
	GetMerkleRoot() string
}

// NodeIdentifier 节点标识接口
type NodeIdentifier interface {
	// GetNodeID 获取节点 ID
	GetNodeID() string
}

// ConvergenceNode 收敛检测节点接口
// 组合 MerkleRootProvider 和 NodeIdentifier
type ConvergenceNode interface {
	MerkleRootProvider
	NodeIdentifier
}

// GossipConvergenceChecker Gossip 收敛性检查器
// 基于 Merkle Tree 检测所有节点是否达到一致状态
type GossipConvergenceChecker struct {
	nodes    []ConvergenceNode
	timeout  time.Duration
	interval time.Duration
}

// NewGossipConvergenceChecker 创建收敛检查器
// nodes: 参与收敛检测的节点列表（需实现 ConvergenceNode 接口）
// timeout: 收敛超时时间
// interval: 检测轮询间隔
func NewGossipConvergenceChecker(nodes []ConvergenceNode, timeout, interval time.Duration) *GossipConvergenceChecker {
	return &GossipConvergenceChecker{
		nodes:    nodes,
		timeout:  timeout,
		interval: interval,
	}
}

// WaitForConvergence 等待所有节点收敛
// 返回 nil 表示收敛成功，否则返回 ConvergenceError
func (c *GossipConvergenceChecker) WaitForConvergence(ctx context.Context) error {
	// 边界情况：没有节点
	if len(c.nodes) == 0 {
		return nil
	}

	// 边界情况：只有一个节点，总是收敛
	if len(c.nodes) == 1 {
		return nil
	}

	deadline := time.Now().Add(c.timeout)

	for time.Now().Before(deadline) {
		// 检查是否已收敛
		if c.isConverged() {
			return nil
		}

		// 等待下一次检测
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.interval):
		}
	}

	// 超时，返回详细诊断信息
	return c.buildConvergenceError()
}

// isConverged 检查所有节点 Merkle Root 是否一致
func (c *GossipConvergenceChecker) isConverged() bool {
	if len(c.nodes) == 0 {
		return true
	}

	// 获取第一个节点的 Merkle Root 作为基准
	baseRoot := c.nodes[0].GetMerkleRoot()

	// 检查所有节点 Merkle Root 是否一致
	for _, node := range c.nodes[1:] {
		root := node.GetMerkleRoot()
		if baseRoot != root {
			return false
		}
	}
	return true
}

// buildConvergenceError 构建收敛失败诊断信息
func (c *GossipConvergenceChecker) buildConvergenceError() *ConvergenceError {
	err := &ConvergenceError{
		Timeout:        c.timeout,
		NodeRoots:      make(map[string]string),
		DivergentNodes: []string{},
	}

	if len(c.nodes) == 0 {
		return err
	}

	// 收集所有节点的 Merkle Root
	baseRoot := c.nodes[0].GetMerkleRoot()
	err.NodeRoots[c.nodes[0].GetNodeID()] = baseRoot

	// 找出未收敛的节点
	for _, node := range c.nodes[1:] {
		root := node.GetMerkleRoot()
		nodeID := node.GetNodeID()
		err.NodeRoots[nodeID] = root

		if baseRoot != root {
			err.DivergentNodes = append(err.DivergentNodes, nodeID)
		}
	}

	return err
}

// ConvergenceError 收敛失败诊断信息
type ConvergenceError struct {
	Timeout        time.Duration     // 收敛超时时间
	NodeRoots      map[string]string // 各节点的 Merkle Root 快照
	DivergentNodes []string          // 未收敛的节点 ID 列表
}

// Error 实现 error 接口
func (e *ConvergenceError) Error() string {
	return fmt.Sprintf("convergence timeout after %v, divergent nodes: %v",
		e.Timeout, e.DivergentNodes)
}

// GetDiagnostics 获取详细诊断信息
func (e *ConvergenceError) GetDiagnostics() string {
	result := fmt.Sprintf("Convergence Timeout: %v\n", e.Timeout)
	result += fmt.Sprintf("Divergent Nodes: %v\n", e.DivergentNodes)
	result += "Node Merkle Roots:\n"
	for nodeID, root := range e.NodeRoots {
		result += fmt.Sprintf("  %s: %s\n", nodeID, root)
	}
	return result
}

// SimpleConvergenceNode 简单的 ConvergenceNode 实现（用于测试）
type SimpleConvergenceNode struct {
	mu         sync.RWMutex
	nodeID     string
	merkleRoot string
}

// NewSimpleConvergenceNode 创建简单收敛节点
func NewSimpleConvergenceNode(nodeID, merkleRoot string) *SimpleConvergenceNode {
	return &SimpleConvergenceNode{
		nodeID:     nodeID,
		merkleRoot: merkleRoot,
	}
}

// GetNodeID 实现 NodeIdentifier 接口
func (n *SimpleConvergenceNode) GetNodeID() string {
	return n.nodeID
}

// GetMerkleRoot 实现 MerkleRootProvider 接口
func (n *SimpleConvergenceNode) GetMerkleRoot() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.merkleRoot
}

// SetMerkleRoot 设置 Merkle Root（用于测试）
func (n *SimpleConvergenceNode) SetMerkleRoot(root string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.merkleRoot = root
}
