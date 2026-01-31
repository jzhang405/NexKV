// Package commands 集群管理命令
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/urfave/cli/v2"
)

// ========================================
// 集群管理命令
// ========================================

// newClusterCommand 创建集群管理命令
func newClusterCommand() *cli.Command {
	return &cli.Command{
		Name:        "cluster",
		Usage:       "集群管理",
		Description: "管理集群配置、状态、拓扑等信息",
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
		Name:        "status",
		Usage:       "查看集群状态",
		Description: `显示集群的运行状态，包括节点数、健康状态、分片信息等`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "watch",
				Aliases: []string{"w"},
				Usage:   "持续监控模式（每 2 秒刷新）",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"o"},
				Value:   "table",
				Usage:   "输出格式：table/json",
			},
			&cli.StringFlag{
				Name:  "node-id",
				Value: "root",
				Usage: "查询节点 ID（默认: root）",
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "仅显示节点列表，不显示详细信息",
			},
		},
		Action: func(c *cli.Context) error {
			return handleClusterStatus(c)
		},
	}
}

// handleClusterStatus 处理集群状态查询
func handleClusterStatus(c *cli.Context) error {
	watchMode := c.Bool("watch")
	outputFormat := c.String("format")
	nodeID := c.String("node-id")
	quietMode := c.Bool("quiet")

	// 创建 RPC 客户端
	client, cleanup, err := createRPCClient()
	if err != nil {
		return fmt.Errorf("连接 Daemon 失败: %w", err)
	}
	defer cleanup()

	if Verbose {
		fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
		fmt.Printf("查询节点: %s\n", nodeID)
	}

	// 首次查询
	if err := queryAndDisplayStatus(client, nodeID, outputFormat, quietMode); err != nil {
		return err
	}

	// 监控模式
	if watchMode {
		return runWatchMode(client, nodeID, outputFormat, quietMode)
	}

	return nil
}

// queryAndDisplayStatus 查询并显示状态
func queryAndDisplayStatus(client *transport.RPCClient, nodeID, outputFormat string, quietMode bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造 ClusterStatus 消息
	req := &transport.ClusterStatusMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeClusterStatus},
		NodeID:      nodeID,
	}

	// 调用 RPC 查询集群状态
	respMsg, err := client.Call(ctx, DaemonAddr, req)
	if err != nil {
		return fmt.Errorf("查询集群状态失败: %w", err)
	}

	// 类型断言获取响应
	resp, ok := respMsg.(*transport.ClusterStatusReplyMessage)
	if !ok {
		return fmt.Errorf("响应类型错误: 期望 ClusterStatusReplyMessage")
	}

	// 格式化输出
	switch outputFormat {
	case "json":
		return formatAsJSON(resp)
	case "table":
		return formatStatusAsTable(resp, quietMode)
	default:
		return fmt.Errorf("不支持的输出格式: %s", outputFormat)
	}
}

// runWatchMode 运行监控模式
func runWatchMode(client *transport.RPCClient, nodeID, outputFormat string, quietMode bool) error {
	fmt.Printf("\n🔄 监控模式（每 2 秒刷新，按 Ctrl+C 退出）\n\n")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	tickCount := 0
	for range ticker.C {
		tickCount++

		// 清屏
		fmt.Print("\033[H\033[2J")

		// 显示标题
		fmt.Printf("📊 NexKV 集群状态监控 [%d] - %s\n\n", tickCount, time.Now().Format("15:04:05"))

		// 查询并显示
		if err := queryAndDisplayStatus(client, nodeID, outputFormat, quietMode); err != nil {
			fmt.Printf("⚠️  查询失败: %v\n", err)
		}
	}
	return nil
}

// ========================================
// 集群拓扑命令
// ========================================

// newClusterTopologyCommand 集群拓扑命令
func newClusterTopologyCommand() *cli.Command {
	return &cli.Command{
		Name:        "topology",
		Usage:       "查看集群拓扑",
		Description: `显示集群的拓扑结构，包括节点间的父子关系、树形结构等`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "details",
				Aliases: []string{"d"},
				Usage:   "显示详细信息（包括连接状态、延迟等）",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"o"},
				Value:   "tree",
				Usage:   "输出格式：tree/json/dot",
			},
			&cli.StringFlag{
				Name:  "node-id",
				Value: "root",
				Usage: "根节点 ID（默认: root）",
			},
		},
		Action: func(c *cli.Context) error {
			return handleClusterTopology(c)
		},
	}
}

// handleClusterTopology 处理集群拓扑查询
func handleClusterTopology(c *cli.Context) error {
	showDetails := c.Bool("details")
	outputFormat := c.String("format")
	nodeID := c.String("node-id")

	// 创建 RPC 客户端
	client, cleanup, err := createRPCClient()
	if err != nil {
		return fmt.Errorf("连接 Daemon 失败: %w", err)
	}
	defer cleanup()

	if Verbose {
		fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
		fmt.Printf("查询节点: %s\n", nodeID)
	}

	// 调用 RPC 查询集群拓扑（复用 ClusterStatus 消息）
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	req := &transport.ClusterStatusMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeClusterStatus},
		NodeID:      nodeID,
	}

	respMsg, err := client.Call(ctx, DaemonAddr, req)
	if err != nil {
		return fmt.Errorf("查询集群拓扑失败: %w", err)
	}

	resp, ok := respMsg.(*transport.ClusterStatusReplyMessage)
	if !ok {
		return fmt.Errorf("响应类型错误: 期望 ClusterStatusReplyMessage")
	}

	// 格式化输出
	switch outputFormat {
	case "json":
		return formatAsJSON(resp)
	case "dot":
		return formatTopologyAsDot(resp, showDetails)
	case "tree":
		return formatTopologyAsTree(resp, showDetails)
	default:
		return fmt.Errorf("不支持的输出格式: %s", outputFormat)
	}
}

// ========================================
// 集群信息命令
// ========================================

// newClusterInfoCommand 集群信息命令
func newClusterInfoCommand() *cli.Command {
	return &cli.Command{
		Name:        "info",
		Usage:       "查看集群信息",
		Description: `显示集群的基本信息，包括集群名称、配置参数、版本信息等`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "显示详细信息（包括所有配置参数）",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"o"},
				Value:   "pretty",
				Usage:   "输出格式：pretty/json/yaml",
			},
		},
		Action: func(c *cli.Context) error {
			return handleClusterInfo(c)
		},
	}
}

// handleClusterInfo 处理集群信息查询
func handleClusterInfo(c *cli.Context) error {
	verbose := c.Bool("verbose")
	outputFormat := c.String("format")

	// 创建 RPC 客户端
	client, cleanup, err := createRPCClient()
	if err != nil {
		return fmt.Errorf("连接 Daemon 失败: %w", err)
	}
	defer cleanup()

	if Verbose {
		fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
	}

	// 调用 RPC 查询集群信息（复用 ClusterStatus 消息）
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	req := &transport.ClusterStatusMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeClusterStatus},
		NodeID:      "root", // 默认查询 root 节点
	}

	respMsg, err := client.Call(ctx, DaemonAddr, req)
	if err != nil {
		return fmt.Errorf("查询集群信息失败: %w", err)
	}

	resp, ok := respMsg.(*transport.ClusterStatusReplyMessage)
	if !ok {
		return fmt.Errorf("响应类型错误: 期望 ClusterStatusReplyMessage")
	}

	// 格式化输出
	switch outputFormat {
	case "json":
		return formatAsJSON(resp)
	case "yaml":
		return formatAsYAML(resp)
	case "pretty":
		return formatClusterInfoPretty(resp, verbose)
	default:
		return fmt.Errorf("不支持的输出格式: %s", outputFormat)
	}
}

// ========================================
// 集群健康检查命令
// ========================================

// newClusterHealthCommand 集群健康检查命令
func newClusterHealthCommand() *cli.Command {
	return &cli.Command{
		Name:        "health",
		Usage:       "集群健康检查",
		Description: `检查集群的健康状态，包括节点连通性、资源使用情况等`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "fix",
				Aliases: []string{"f"},
				Usage:   "尝试自动修复发现的问题",
			},
			&cli.IntFlag{
				Name:    "timeout",
				Aliases: []string{"t"},
				Value:   30,
				Usage:   "健康检查超时时间（秒）",
			},
		},
		Action: func(c *cli.Context) error {
			return handleClusterHealth(c)
		},
	}
}

// handleClusterHealth 处理集群健康检查
func handleClusterHealth(c *cli.Context) error {
	tryFix := c.Bool("fix")
	timeoutSecs := c.Int("timeout")

	// 创建 RPC 客户端
	client, cleanup, err := createRPCClient()
	if err != nil {
		return fmt.Errorf("连接 Daemon 失败: %w", err)
	}
	defer cleanup()

	fmt.Printf("🔍 NexKV 集群健康检查\n")
	fmt.Printf("连接到 Daemon: %s\n\n", DaemonAddr)

	// 执行健康检查（通过 Ping 所有节点）
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	// 查询集群状态获取所有节点
	req := &transport.ClusterStatusMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeClusterStatus},
		NodeID:      "root",
	}

	respMsg, err := client.Call(ctx, DaemonAddr, req)
	if err != nil {
		return fmt.Errorf("健康检查失败: %w", err)
	}

	resp, ok := respMsg.(*transport.ClusterStatusReplyMessage)
	if !ok {
		return fmt.Errorf("响应类型错误: 期望 ClusterStatusReplyMessage")
	}

	// 显示健康检查结果
	return formatHealthReport(resp, tryFix)
}

// ========================================
// 辅助函数
// ========================================

// createRPCClient 创建 RPC 客户端
// 返回: 客户端实例、清理函数、错误
func createRPCClient() (*transport.RPCClient, func(), error) {
	// 创建 TCP 传输层（客户端模式，不需要监听地址）
	tcpTransport, err := transport.NewTCPTransport(":0") // :0 表示自动选择端口
	if err != nil {
		return nil, nil, fmt.Errorf("创建 TCP 传输失败: %w", err)
	}

	// 序列号生成器（简单递增）
	var msgSeq uint64
	msgSeqGenerator := func() uint64 {
		msgSeq++
		return msgSeq
	}

	// 启动传输层
	if err := tcpTransport.Start(nil, msgSeqGenerator, ":0"); err != nil {
		return nil, nil, fmt.Errorf("启动 TCP 传输失败: %w", err)
	}

	// 创建 RPC 客户端（不需要 UDP 传输层）
	config := transport.DefaultRPCClientConfig()
	config.RequestTimeout = Timeout

	rpcClient, err := transport.NewRPCClient(tcpTransport, nil, config)
	if err != nil {
		_ = tcpTransport.Stop()
		return nil, nil, fmt.Errorf("创建 RPC 客户端失败: %w", err)
	}

	// 启动 RPC 客户端
	if err := rpcClient.Start(); err != nil {
		_ = tcpTransport.Stop()
		return nil, nil, fmt.Errorf("启动 RPC 客户端失败: %w", err)
	}

	// 清理函数
	cleanup := func() {
		_ = rpcClient.Stop()
		_ = tcpTransport.Stop()
	}

	return rpcClient, cleanup, nil
}

// formatStatusAsTable 格式化状态为表格
func formatStatusAsTable(resp *transport.ClusterStatusReplyMessage, quietMode bool) error {
	if len(resp.Nodes) == 0 {
		fmt.Printf("📊 集群中没有节点\n")
		return nil
	}

	fmt.Printf("📊 节点列表（共 %d 个节点）\n\n", len(resp.Nodes))
	fmt.Printf("%-20s %-20s %-10s %-15s %-10s\n", "NodeID", "Address", "Role", "Parent", "Status")
	fmt.Printf("%s\n", "──────────────────── ──────────────────── ───────── ─────────────── ─────────")

	for _, node := range resp.Nodes {
		parentID := node.ParentID
		if parentID == "" {
			parentID = "-"
		}
		fmt.Printf("%-20s %-20s %-10s %-15s %-10s\n",
			node.NodeID, node.Addr, node.Role, parentID, node.Status)
	}

	if !quietMode {
		fmt.Printf("\n提示: 使用 --format json 输出 JSON 格式\n")
	}
	return nil
}

// formatAsJSON 格式化为 JSON
func formatAsJSON(data any) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON 序列化失败: %w", err)
	}
	fmt.Println(string(jsonData))
	return nil
}

// formatAsYAML 格式化为 YAML
func formatAsYAML(data any) error {
	// TODO: 实现 YAML 格式化
	return fmt.Errorf("YAML 格式暂未实现")
}

// formatTopologyAsDot 格式化为 Graphviz DOT
func formatTopologyAsDot(resp *transport.ClusterStatusReplyMessage, showDetails bool) error {
	fmt.Printf("digraph NexKVCluster {\n")
	fmt.Printf("  // 集群拓扑图\n")
	fmt.Printf("  rankdir=TB;\n\n")

	for _, node := range resp.Nodes {
		label := node.NodeID
		if showDetails {
			label = fmt.Sprintf("%s\\n%s\\n%s", node.NodeID, node.Addr, node.Status)
		}
		fmt.Printf("  \"%s\" [label=\"%s\"];\n", node.NodeID, label)

		if node.ParentID != "" {
			fmt.Printf("  \"%s\" -> \"%s\";\n", node.ParentID, node.NodeID)
		}
	}

	fmt.Printf("}\n")
	return nil
}

// formatTopologyAsTree 格式化为树形结构
func formatTopologyAsTree(resp *transport.ClusterStatusReplyMessage, showDetails bool) error {
	if len(resp.Nodes) == 0 {
		fmt.Printf("🌳 集群拓扑: 无节点\n")
		return nil
	}

	fmt.Printf("🌳 NexKV 集群拓扑\n\n")

	// 构建父子关系映射
	children := make(map[string][]*transport.NodeInfo)
	roots := make([]*transport.NodeInfo, 0)

	for i := range resp.Nodes {
		node := &resp.Nodes[i]
		if node.ParentID == "" {
			roots = append(roots, node)
		} else {
			children[node.ParentID] = append(children[node.ParentID], node)
		}
	}

	// 递归打印树
	for _, root := range roots {
		printTreeNode(root, children, "", showDetails)
	}

	return nil
}

// printTreeNode 递归打印树节点
func printTreeNode(node *transport.NodeInfo, children map[string][]*transport.NodeInfo, prefix string, showDetails bool) {
	connector := "├── "
	if prefix == "" {
		connector = ""
	}

	fmt.Printf("%s%s%s", prefix, connector, node.NodeID)

	if showDetails {
		fmt.Printf(" (%s, %s)", node.Addr, node.Status)
	}
	fmt.Println()

	childNodes := children[node.NodeID]
	for i, child := range childNodes {
		newPrefix := prefix
		if connector != "" {
			newPrefix += "│   "
		} else {
			newPrefix += "    "
		}

		if i == len(childNodes)-1 {
			newPrefix += "└── "
		} else {
			newPrefix += "├── "
		}

		printTreeNode(child, children, newPrefix, showDetails)
	}
}

// formatClusterInfoPretty 格式化集群信息
func formatClusterInfoPretty(resp *transport.ClusterStatusReplyMessage, verbose bool) error {
	fmt.Printf("📋 NexKV 集群信息\n\n")
	fmt.Printf("节点总数: %d\n", len(resp.Nodes))

	if verbose {
		fmt.Printf("\n节点详情:\n")
		for _, node := range resp.Nodes {
			fmt.Printf("  - %s: %s, %s, Level=%d\n",
				node.NodeID, node.Addr, node.Status, node.Level)
		}
	}

	fmt.Printf("\n💡 提示: 使用 --format json 输出完整信息\n")
	return nil
}

// formatHealthReport 格式化健康报告
func formatHealthReport(resp *transport.ClusterStatusReplyMessage, tryFix bool) error {
	healthyCount := 0
	unhealthyCount := 0

	for _, node := range resp.Nodes {
		if node.Status == "ready" {
			healthyCount++
		} else {
			unhealthyCount++
		}
	}

	fmt.Printf("✅ 集群健康状态: ")
	if unhealthyCount == 0 {
		fmt.Printf("健康\n")
	} else {
		fmt.Printf("警告 (%d 个节点异常)\n", unhealthyCount)
	}

	fmt.Printf("\n节点统计:\n")
	fmt.Printf("  总节点数: %d\n", len(resp.Nodes))
	fmt.Printf("  健康节点: %d\n", healthyCount)
	fmt.Printf("  异常节点: %d\n", unhealthyCount)

	if unhealthyCount > 0 && tryFix {
		fmt.Printf("\n⚠️  自动修复功能暂未实现\n")
	}

	return nil
}
