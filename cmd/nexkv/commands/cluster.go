// Package commands 集群管理命令
//
// PR-Libp2p-RPC: 使用新的 RPC 框架恢复集群管理命令
package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/rpc"
	"github.com/urfave/cli/v2"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// 集群管理命令
// ========================================

// newClusterCommand 创建集群管理命令
func newClusterCommand() *cli.Command {
	return &cli.Command{
		Name:        "cluster",
		Usage:       "集群管理",
		Description: `管理集群状态、拓扑和信息`,
		Subcommands: []*cli.Command{
			newClusterStatusCommand(),
			newClusterTopologyCommand(),
			newClusterInfoCommand(),
			newClusterHealthCommand(),
		},
	}
}

// ========================================
// 集群状态命令
// ========================================

// newClusterStatusCommand 集群状态命令
func newClusterStatusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "查看集群状态",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "输出格式（table, json）",
				Value:   "table",
			},
			&cli.BoolFlag{
				Name:    "watch",
				Aliases: []string{"w"},
				Usage:   "持续监控模式",
				Value:   false,
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "静默模式，仅显示摘要",
				Value:   false,
			},
		},
		Action: func(c *cli.Context) error {
			format := c.String("format")
			watch := c.Bool("watch")
			quiet := c.Bool("quiet")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			if watch {
				return handleClusterStatusWatch(client, format, quiet)
			}
			return handleClusterStatus(client, format, quiet)
		},
	}
}

// handleClusterStatus 处理集群状态查询
func handleClusterStatus(client *rpc.Client, format string, quietMode bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造请求
	req := rpc.NewClusterStatusRequest("cli")
	reqBody, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	targetPeerID := extractPeerIDFromAddr(nil)

	// 发送 RPC 请求
	respBody, err := client.Call(ctx, targetPeerID, "ClusterStatus", reqBody)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var resp rpc.ClusterStatusResponse
	if err := msgpack.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 格式化输出
	return formatClusterStatus(resp, format, quietMode)
}

// handleClusterStatusWatch 处理集群状态监控模式
func handleClusterStatusWatch(client *rpc.Client, format string, quietMode bool) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	fmt.Printf("监控集群状态（按 Ctrl+C 退出）...\n\n")

	// 使用 context 来处理中断信号
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 捕获中断信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	for {
		select {
		case <-ticker.C:
			if err := handleClusterStatus(client, format, quietMode); err != nil {
				logging.WithField("error", err).Warn("获取集群状态失败")
			}
			fmt.Println() // 分隔符
		case <-ctx.Done():
			fmt.Println("\n监控已停止")
			return nil
		}
	}
}

// formatClusterStatus 格式化集群状态
func formatClusterStatus(resp rpc.ClusterStatusResponse, format string, quietMode bool) error {
	switch format {
	case "json":
		return formatClusterStatusAsJSON(resp, quietMode)
	case "table":
		return formatClusterStatusAsTable(resp, quietMode)
	default:
		return fmt.Errorf("不支持的格式: %s", format)
	}
}

// formatClusterStatusAsTable 格式化集群状态为表格
func formatClusterStatusAsTable(resp rpc.ClusterStatusResponse, quietMode bool) error {
	if quietMode {
		// 静默模式，只显示摘要
		fmt.Printf("集群状态: %d/%d 在线, 深度 %d\n",
			resp.OnlineNodes, resp.TotalNodes, resp.TreeDepth)
		return nil
	}

	fmt.Printf("集群状态:\n")
	fmt.Printf("  总节点数: %d\n", resp.TotalNodes)
	fmt.Printf("  在线节点: %d\n", resp.OnlineNodes)
	fmt.Printf("  离线节点: %d\n", resp.TotalNodes-resp.OnlineNodes)
	fmt.Printf("  树深度: %d\n", resp.TreeDepth)
	fmt.Printf("\n")

	// 节点列表
	if len(resp.Nodes) > 0 {
		fmt.Printf("节点列表:\n")
		for _, node := range resp.Nodes {
			status := formatNodeStatus(node.Status)
			fmt.Printf("  %s: Level=%d, Parent=%s, Status=%s\n",
				node.NodeID, node.Level, node.ParentID, status)
		}
	}

	return nil
}

// formatClusterStatusAsJSON 格式化集群状态为 JSON
func formatClusterStatusAsJSON(resp rpc.ClusterStatusResponse, quietMode bool) error {
	// TODO: 实现 JSON 格式输出
	fmt.Printf("TODO: JSON 格式输出\n")
	return formatClusterStatusAsTable(resp, quietMode)
}

// ========================================
// 集群拓扑命令
// ========================================

// newClusterTopologyCommand 集群拓扑命令
func newClusterTopologyCommand() *cli.Command {
	return &cli.Command{
		Name:  "topology",
		Usage: "查看集群拓扑",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "输出格式（tree, dot, json）",
				Value:   "tree",
			},
			&cli.BoolFlag{
				Name:    "details",
				Aliases: []string{"d"},
				Usage:   "显示详细信息",
				Value:   false,
			},
		},
		Action: func(c *cli.Context) error {
			format := c.String("format")
			details := c.Bool("details")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			return handleClusterTopology(client, format, details)
		},
	}
}

// handleClusterTopology 处理集群拓扑查询
func handleClusterTopology(client *rpc.Client, format string, showDetails bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造请求
	req := rpc.NewClusterStatusRequest("cli")
	reqBody, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	targetPeerID := extractPeerIDFromAddr(nil)

	// 发送 RPC 请求
	respBody, err := client.Call(ctx, targetPeerID, "ClusterStatus", reqBody)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var resp rpc.ClusterStatusResponse
	if err := msgpack.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 格式化输出
	switch format {
	case "tree":
		return formatTopologyAsTree(resp, showDetails)
	case "dot":
		return formatTopologyAsDot(resp, showDetails)
	case "json":
		return formatTopologyAsJSON(resp, showDetails)
	default:
		return fmt.Errorf("不支持的格式: %s", format)
	}
}

// formatTopologyAsTree 格式化拓扑为树形结构
func formatTopologyAsTree(resp rpc.ClusterStatusResponse, showDetails bool) error {
	fmt.Printf("集群拓扑:\n")

	// 构建节点映射
	nodeMap := make(map[string]*rpc.NodeInfo)
	for i := range resp.Nodes {
		nodeMap[resp.Nodes[i].NodeID] = &resp.Nodes[i]
	}

	// 找到根节点（没有父节点的节点）
	var roots []string
	for _, node := range resp.Nodes {
		if node.ParentID == "" {
			roots = append(roots, node.NodeID)
		}
	}

	// 递归打印树
	for _, rootID := range roots {
		printTreeNode(nodeMap, rootID, "", showDetails)
	}

	return nil
}

// printTreeNode 递归打印树节点
func printTreeNode(nodeMap map[string]*rpc.NodeInfo, nodeID string, prefix string, showDetails bool) {
	node, exists := nodeMap[nodeID]
	if !exists {
		return
	}

	// 打印节点信息
	if showDetails {
		fmt.Printf("%s%s [Level=%d, Status=%d]\n", prefix, nodeID, node.Level, node.Status)
	} else {
		fmt.Printf("%s%s\n", prefix, nodeID)
	}

	// 打印子节点
	for i, childID := range node.Children {
		isLast := i == len(node.Children)-1
		childPrefix := prefix
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
		printTreeNode(nodeMap, childID, childPrefix+"└── ", showDetails)
	}
}

// formatTopologyAsDot 格式化为 Graphviz DOT
func formatTopologyAsDot(resp rpc.ClusterStatusResponse, showDetails bool) error {
	fmt.Printf("digraph cluster {\n")
	fmt.Printf("  rankdir=TB;\n")
	fmt.Printf("  node [shape=box, style=rounded];\n\n")

	for _, node := range resp.Nodes {
		color := "lightblue"
		if node.Status == 4 {
			color = "lightcoral"
		} else if node.Status != 1 {
			color = "lightgray"
		}

		fmt.Printf("  \"%s\" [label=\"%s\", fillcolor=%s];\n", node.NodeID, node.NodeID, color)

		if node.ParentID != "" {
			fmt.Printf("  \"%s\" -> \"%s\";\n", node.ParentID, node.NodeID)
		}
	}

	fmt.Printf("}\n")
	return nil
}

// formatTopologyAsJSON 格式化拓扑为 JSON
func formatTopologyAsJSON(resp rpc.ClusterStatusResponse, showDetails bool) error {
	// TODO: 实现 JSON 格式输出
	fmt.Printf("TODO: JSON 格式输出\n")
	return formatTopologyAsTree(resp, showDetails)
}

// ========================================
// 集群信息命令
// ========================================

// newClusterInfoCommand 集群信息命令
func newClusterInfoCommand() *cli.Command {
	return &cli.Command{
		Name:  "info",
		Usage: "查看集群信息",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "显示详细信息",
				Value:   false,
			},
		},
		Action: func(c *cli.Context) error {
			verbose := c.Bool("verbose")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			return handleClusterInfo(client, verbose)
		},
	}
}

// handleClusterInfo 处理集群信息查询
func handleClusterInfo(client *rpc.Client, verbose bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造请求
	req := rpc.NewClusterStatusRequest("cli")
	reqBody, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	targetPeerID := extractPeerIDFromAddr(nil)

	// 发送 RPC 请求
	respBody, err := client.Call(ctx, targetPeerID, "ClusterStatus", reqBody)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var resp rpc.ClusterStatusResponse
	if err := msgpack.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 格式化输出
	return formatClusterInfoPretty(resp, verbose)
}

// formatClusterInfoPretty 格式化集群信息
func formatClusterInfoPretty(resp rpc.ClusterStatusResponse, verbose bool) error {
	fmt.Printf("集群信息:\n")
	fmt.Printf("  总节点数: %d\n", resp.TotalNodes)
	fmt.Printf("  在线节点: %d\n", resp.OnlineNodes)
	fmt.Printf("  离线节点: %d\n", resp.TotalNodes-resp.OnlineNodes)
	fmt.Printf("  树深度: %d\n", resp.TreeDepth)

	if verbose {
		fmt.Printf("\n详细节点信息:\n")
		for _, node := range resp.Nodes {
			status := formatNodeStatus(node.Status)
			fmt.Printf("  节点: %s\n", node.NodeID)
			fmt.Printf("    父节点: %s\n", node.ParentID)
			fmt.Printf("    层级: %d\n", node.Level)
			fmt.Printf("    状态: %s\n", status)
			fmt.Printf("    子节点数: %d\n", len(node.Children))
		}
	}

	return nil
}

// ========================================
// 集群健康检查命令
// ========================================

// newClusterHealthCommand 集群健康检查命令
func newClusterHealthCommand() *cli.Command {
	return &cli.Command{
		Name:  "health",
		Usage: "集群健康检查",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "fix",
				Aliases: []string{"f"},
				Usage:   "自动修复发现的问题",
				Value:   false,
			},
			&cli.StringFlag{
				Name:    "fix-type",
				Aliases: []string{"t"},
				Usage:   "修复类型（unreachable, mismatch, gossip）",
				Value:   "auto",
			},
		},
		Action: func(c *cli.Context) error {
			fix := c.Bool("fix")
			fixType := c.String("fix-type")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			return handleClusterHealth(client, fix, fixType)
		},
	}
}

// handleClusterHealth 处理集群健康检查
func handleClusterHealth(client *rpc.Client, autoFix bool, fixType string) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造集群状态请求（用于检查健康状态）
	req := rpc.NewClusterStatusRequest("cli")
	reqBody, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	targetPeerID := extractPeerIDFromAddr(nil)

	// 发送 RPC 请求
	respBody, err := client.Call(ctx, targetPeerID, "ClusterStatus", reqBody)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var resp rpc.ClusterStatusResponse
	if err := msgpack.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 分析健康状态
	healthy, unhealthy := countNodeStatus(resp)

	fmt.Printf("集群健康检查:\n")
	fmt.Printf("  健康节点: %d\n", healthy)
	fmt.Printf("  异常节点: %d\n", unhealthy)

	// 如果有异常节点，打印列表
	if unhealthy > 0 {
		printUnhealthyNodes(resp.Nodes)
	}

	// 自动修复
	if autoFix && unhealthy > 0 {
		fmt.Printf("\n正在执行自动修复...\n")
		return executeClusterFix(client, fixType, resp)
	}

	return nil
}

// executeClusterFix 执行集群修复
func executeClusterFix(client *rpc.Client, fixType string, statusResp rpc.ClusterStatusResponse) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造修复请求
	req := rpc.NewClusterHealthFixRequest("cli", fixType)
	reqBody, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	targetPeerID := extractPeerIDFromAddr(nil)

	// 发送 RPC 请求
	respBody, err := client.Call(ctx, targetPeerID, "ClusterHealthFix", reqBody)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var resp rpc.ClusterHealthFixResponse
	if err := msgpack.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	// 显示修复结果
	printFixResults(resp)

	return nil
}

// ========================================
// 辅助函数
// ========================================

// countNodeStatus 统计节点状态
func countNodeStatus(resp rpc.ClusterStatusResponse) (healthy, unhealthy int) {
	for _, node := range resp.Nodes {
		if node.Status == 1 { // Ready
			healthy++
		} else {
			unhealthy++
		}
	}
	return
}

// printUnhealthyNodes 打印异常节点列表
func printUnhealthyNodes(nodes []rpc.NodeInfo) {
	fmt.Printf("\n异常节点:\n")
	for _, node := range nodes {
		if node.Status != 1 {
			status := formatNodeStatus(node.Status)
			fmt.Printf("  - %s: %s\n", node.NodeID, status)
		}
	}
}

// printFixResults 打印修复结果
func printFixResults(resp rpc.ClusterHealthFixResponse) {
	if resp.Success {
		fmt.Printf("✓ 修复成功\n")
		if len(resp.FixedNodes) > 0 {
			fmt.Printf("  已修复节点: %v\n", resp.FixedNodes)
		}
	} else {
		fmt.Printf("✗ 修复失败: %s\n", resp.Reason)
	}
}
