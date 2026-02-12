package framework

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CLIExecutor 封装 nexkv CLI 命令执行
type CLIExecutor struct {
	// daemonAddr daemon 地址
	daemonAddr string
	// timeout 命令超时
	timeout time.Duration
	// cliPath CLI 可执行文件路径
	cliPath string
}

// CLIResult 表示 CLI 命令执行结果
type CLIResult struct {
	// ExitCode 退出码
	ExitCode int
	// Stdout 标准输出
	Stdout string
	// Stderr 标准错误
	Stderr string
	// Duration 执行耗时
	Duration time.Duration
}

// NewCLIExecutor 创建 CLI 执行器
func NewCLIExecutor(daemonAddr string) *CLIExecutor {
	return &CLIExecutor{
		daemonAddr: daemonAddr,
		timeout:    5 * time.Second,
		cliPath:    "./nexkv",
	}
}

// SetTimeout 设置命令超时
func (e *CLIExecutor) SetTimeout(timeout time.Duration) {
	e.timeout = timeout
}

// Execute 执行 CLI 命令
func (e *CLIExecutor) Execute(ctx context.Context, args ...string) *CLIResult {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	start := time.Now()

	// 构建命令
	cmdArgs := append([]string{"--addr", e.daemonAddr}, args...)
	cmd := exec.CommandContext(ctx, e.cliPath, cmdArgs...)

	// 执行命令
	stdout, err := cmd.Output()
	duration := time.Since(start)

	result := &CLIResult{
		Stdout:   string(stdout),
		Duration: duration,
	}

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
			result.Stderr = string(exitError.Stderr)
		} else {
			result.ExitCode = -1
			result.Stderr = err.Error()
		}
	}

	return result
}

// ClusterStatus 获取集群状态
func (e *CLIExecutor) ClusterStatus(ctx context.Context) (*ClusterStatusResult, error) {
	result := e.Execute(ctx, "cluster", "status")
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("cluster status failed: %s", result.Stderr)
	}

	return ParseClusterStatus(result.Stdout), nil
}

// NodeList 列出节点
func (e *CLIExecutor) NodeList(ctx context.Context) (*NodeListResult, error) {
	result := e.Execute(ctx, "node", "list")
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("node list failed: %s", result.Stderr)
	}

	return ParseNodeList(result.Stdout), nil
}

// NodeAdd 添加节点
func (e *CLIExecutor) NodeAdd(ctx context.Context, nodeID, addr string, role int) (*CLIResult, error) {
	result := e.Execute(ctx, "node", "add", "--id", nodeID, "--addr", addr, "--role", fmt.Sprintf("%d", role))
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("node add failed: %s", result.Stderr)
	}
	return result, nil
}

// NodeRemove 移除节点
func (e *CLIExecutor) NodeRemove(ctx context.Context, nodeID string) (*CLIResult, error) {
	result := e.Execute(ctx, "node", "remove", nodeID)
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("node remove failed: %s", result.Stderr)
	}
	return result, nil
}

// ClusterTopology 获取集群拓扑
func (e *CLIExecutor) ClusterTopology(ctx context.Context) (*TopologyResult, error) {
	result := e.Execute(ctx, "cluster", "topology")
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("cluster topology failed: %s", result.Stderr)
	}

	return ParseTopology(result.Stdout), nil
}

// ========================================
// 结果解析辅助结构
// ========================================

// ClusterStatusResult 集群状态结果
type ClusterStatusResult struct {
	ClusterID     string
	NodeCount     int
	OnlineNodeCount int
	LeaderNodeID  string
	Nodes         []NodeStatus
}

// NodeStatus 节点状态
type NodeStatus struct {
	NodeID    string
	Addr      string
	Role      string
	Status    string
	ParentID  string
	ChildIDs  []string
}

// NodeListResult 节点列表结果
type NodeListResult struct {
	Nodes []NodeInfo
}

// NodeInfo 节点信息
type NodeInfo struct {
	NodeID string
	Addr   string
	Role   int
	Status string
}

// TopologyResult 拓扑结果
type TopologyResult struct {
	RootID  string
	TreeNodes []TreeNode
}

// TreeNode 树形节点
type TreeNode struct {
	NodeID   string
	Children []TreeNode
}

// ========================================
// 结果解析函数
// ========================================

// ParseClusterStatus 解析集群状态输出
func ParseClusterStatus(output string) *ClusterStatusResult {
	// TODO: 实现真实的解析逻辑
	// 这里返回模拟数据
	return &ClusterStatusResult{
		ClusterID:       "test-cluster",
		NodeCount:       3,
		OnlineNodeCount: 3,
		LeaderNodeID:    "node-1",
		Nodes: []NodeStatus{
			{
				NodeID: "node-1",
				Addr:   "127.0.0.1:7946",
				Role:   "leader",
				Status: "online",
			},
		},
	}
}

// ParseNodeList 解析节点列表输出
func ParseNodeList(output string) *NodeListResult {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	nodes := make([]NodeInfo, 0, len(lines))

	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "NODE ID") {
			continue
		}
		// TODO: 解析实际格式
		nodes = append(nodes, NodeInfo{
			NodeID: "node-1",
			Addr:   "127.0.0.1:7946",
			Role:   1,
			Status: "online",
		})
	}

	return &NodeListResult{Nodes: nodes}
}

// ParseTopology 解析拓扑输出
func ParseTopology(output string) *TopologyResult {
	// TODO: 实现真实的解析逻辑
	return &TopologyResult{
		RootID: "node-1",
		TreeNodes: []TreeNode{
			{
				NodeID: "node-1",
				Children: []TreeNode{
					{NodeID: "node-2"},
					{NodeID: "node-3"},
				},
			},
		},
	}
}
