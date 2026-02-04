// Package commands 节点管理命令
package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/urfave/cli/v2"
)

// newNodeCommand 创建节点管理命令
func newNodeCommand() *cli.Command {
	return &cli.Command{
		Name:        "node",
		Usage:       "节点管理",
		Description: `管理集群中的节点，包括添加、删除、列表、状态、ping 等操作`,
		Subcommands: []*cli.Command{
			newNodeAddCommand(),
			newNodeRemoveCommand(),
			newNodeListCommand(),
			newNodeStatusCommand(),
			newNodePingCommand(),
		},
	}
}

// newNodeAddCommand 添加节点命令
func newNodeAddCommand() *cli.Command {
	return &cli.Command{
		Name:        "add",
		Usage:       "添加节点到集群",
		Description: `将新节点添加到集群中，建立节点间的连接关系`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "id",
				Required: true,
				Usage:    "节点 ID",
			},
			&cli.StringFlag{
				Name:     "addr",
				Required: true,
				Usage:    "节点地址（格式：host:port）",
			},
		},
		Action: func(c *cli.Context) error {
			nodeID := c.String("id")
			nodeAddr := c.String("addr")

			if nodeID == "" {
				return types.NewCLINodeIDRequiredError()
			}
			if nodeAddr == "" {
				return types.NewCLINodeAddrRequiredError()
			}

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient()
			if err != nil {
				return err
			}
			defer cleanup()

			// 调用 RPC 添加节点
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
				fmt.Printf("添加节点: ID=%s, Addr=%s\n", nodeID, nodeAddr)
			}

			// 构造 NodeJoin 消息
			req := &transport.NodeJoinMessage{
				BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeJoin},
				NodeID:      nodeID,
				Addr:        nodeAddr,
				Role:        "child",
			}

			_, err = client.Call(ctx, DaemonAddr, req)
			if err != nil {
				return types.NewCLIAddNodeFailedError(err)
			}

			fmt.Printf("✓ 节点 %s 添加成功\n", nodeID)
			return nil
		},
	}
}

// newNodeRemoveCommand 删除节点命令
func newNodeRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:        "remove",
		Usage:       "从集群中删除节点",
		Description: `从集群中删除指定节点，可选择强制删除`,
		ArgsUsage:   "[node-id]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "强制删除，不提示确认",
			},
		},
		Action: func(c *cli.Context) error {
			force := c.Bool("force")

			if c.NArg() < 1 {
				return types.NewCLINodeIDRequiredError()
			}

			nodeID := c.Args().First()

			if !force {
				// 确认提示
				fmt.Printf("确认要删除节点 %s 吗？(y/N): ", nodeID)
				var confirm string
				_, _ = fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					fmt.Println("操作已取消")
					return nil
				}
			}

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient()
			if err != nil {
				return err
			}
			defer cleanup()

			// 调用 RPC 删除节点
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
				fmt.Printf("删除节点: ID=%s\n", nodeID)
			}

			// 构造 NodeLeave 消息
			req := &transport.NodeLeaveMessage{
				BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeLeave},
				NodeID:      nodeID,
				Reason:      "admin_remove",
			}

			_, err = client.Call(ctx, DaemonAddr, req)
			if err != nil {
				return types.NewCLIRemoveNodeFailedError(err)
			}

			fmt.Printf("✓ 节点 %s 删除成功\n", nodeID)
			return nil
		},
	}
}

// newNodeStatusCommand 节点状态命令
func newNodeStatusCommand() *cli.Command {
	return &cli.Command{
		Name:        "status",
		Usage:       "查看节点状态",
		Description: `查看指定节点的详细状态信息，包括连接数、负载、健康状态等`,
		ArgsUsage:   "[node-id]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"o"},
				Value:   "pretty",
				Usage:   "输出格式：pretty/json/yaml",
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return types.NewCLINodeIDRequiredError()
			}

			nodeID := c.Args().First()
			outputFormat := c.String("format")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient()
			if err != nil {
				return err
			}
			defer cleanup()

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
				fmt.Printf("查询节点状态: ID=%s\n", nodeID)
			}

			// 调用 RPC 查询节点状态
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()

			// 构造 ClusterStatus 消息获取节点状态
			req := &transport.ClusterStatusMessage{
				BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeClusterStatus},
				NodeID:      nodeID,
			}

			respMsg, err := client.Call(ctx, DaemonAddr, req)
			if err != nil {
				return types.NewCLIQueryNodeStatusFailedError(err)
			}

			resp, ok := respMsg.(*transport.ClusterStatusReplyMessage)
			if !ok {
				return types.NewCLIResponseTypeError("ClusterStatusReplyMessage")
			}

			// 查找目标节点
			var targetNode *transport.NodeInfo
			for i := range resp.Nodes {
				if resp.Nodes[i].NodeID == nodeID {
					targetNode = &resp.Nodes[i]
					break
				}
			}

			if targetNode == nil {
				return types.NewCLINodeNotFoundError(nodeID)
			}

			// 格式化输出
			return formatNodeStatus(targetNode, outputFormat)
		},
	}
}

// newNodeListCommand 列出节点命令
func newNodeListCommand() *cli.Command {
	return &cli.Command{
		Name:        "list",
		Usage:       "列出集群中的所有节点",
		Description: `显示集群中所有节点的信息，包括状态、连接数、负载等`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "显示详细信息",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"o"},
				Value:   "table",
				Usage:   "输出格式：table/json",
			},
		},
		Action: func(c *cli.Context) error {
			verbose := c.Bool("verbose")
			outputFormat := c.String("format")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient()
			if err != nil {
				return err
			}
			defer cleanup()

			// 调用 RPC 查询节点列表
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
			}

			// 构造 ClusterStatus 消息获取节点列表
			req := &transport.ClusterStatusMessage{
				BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeClusterStatus},
				NodeID:      "root",
			}

			respMsg, err := client.Call(ctx, DaemonAddr, req)
			if err != nil {
				return types.NewCLIQueryNodeListFailedError(err)
			}

			resp, ok := respMsg.(*transport.ClusterStatusReplyMessage)
			if !ok {
				return types.NewCLIResponseTypeError("ClusterStatusReplyMessage")
			}

			// 格式化输出
			if err := formatNodeList(resp, verbose, outputFormat); err != nil {
				return types.NewCLIFormatOutputError(err)
			}

			return nil
		},
	}
}

// newNodePingCommand Ping 节点命令
func newNodePingCommand() *cli.Command {
	return &cli.Command{
		Name:        "ping",
		Usage:       "Ping 节点检查连通性",
		Description: `向指定节点发送 Ping 请求，检查连通性和延迟`,
		ArgsUsage:   "[node-id]",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "count",
				Aliases: []string{"c"},
				Value:   1,
				Usage:   "Ping 次数",
			},
		},
		Action: func(c *cli.Context) error {
			count := c.Int("count")

			if c.NArg() < 1 {
				return types.NewCLINodeIDRequiredError()
			}

			nodeID := c.Args().First()

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient()
			if err != nil {
				return err
			}
			defer cleanup()

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
			}

			// 执行 Ping 测试
			successCount := 0
			for i := 0; i < count; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)

				start := time.Now()

				// 构造 NodePing 消息
				req := &transport.NodePingMessage{
					BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePing},
					NodeID:      nodeID,
					Sequence:    int64(i + 1),
					Timestamp:   time.Now().Unix(),
				}

				respMsg, err := client.Call(ctx, DaemonAddr, req)
				elapsed := time.Since(start)

				cancel()

				if err != nil {
					fmt.Printf("Ping %s: 超时或失败 (%v)\n", nodeID, err)
				} else {
					resp, ok := respMsg.(*transport.NodePongMessage)
					if ok {
						successCount++
						fmt.Printf("Ping %s: 成功 (耗时: %v, 来自: %s, 状态: %s)\n", nodeID, elapsed, resp.NodeID, resp.Status)
					} else {
						fmt.Printf("Ping %s: 响应格式错误 (类型: %T)\n", nodeID, respMsg)
					}
				}

				// 多次 ping 之间间隔 1 秒
				if count > 1 && i < count-1 {
					time.Sleep(1 * time.Second)
				}
			}

			// 显示统计
			if count > 1 {
				fmt.Printf("\n--- %s ping 统计 ---\n", nodeID)
				fmt.Printf("发送: %d, 成功: %d, 失败: %d\n", count, successCount, count-successCount)
			}

			return nil
		},
	}
}

// ========================================
// 辅助函数
// ========================================

// formatNodeList 格式化节点列表
func formatNodeList(resp *transport.ClusterStatusReplyMessage, verbose bool, outputFormat string) error {
	switch outputFormat {
	case "json":
		return formatAsJSON(resp)
	case "table":
		return formatNodeListAsTable(resp, verbose)
	default:
		return types.NewCLIUnsupportedOutputFormatError(outputFormat)
	}
}

// formatNodeListAsTable 格式化节点列表为表格
func formatNodeListAsTable(resp *transport.ClusterStatusReplyMessage, verbose bool) error {
	if len(resp.Nodes) == 0 {
		fmt.Printf("集群中没有节点\n")
		return nil
	}

	fmt.Printf("节点列表（共 %d 个节点）\n\n", len(resp.Nodes))
	fmt.Printf("%-20s %-20s %-10s %-15s %-10s\n", "NodeID", "Address", "Role", "Parent", "Status")
	fmt.Printf("%s\n", "──────────────────── ──────────────────── ───────── ─────────────── ─────────")

	for _, node := range resp.Nodes {
		parentID := node.ParentID
		if parentID == "" {
			parentID = "-"
		}
		fmt.Printf("%-20s %-20s %-10s %-15s %-10s\n",
			node.NodeID, node.Addr, node.Role, parentID, node.Status)

		if verbose {
			fmt.Printf("  Level: %d\n", node.Level)
		}
	}

	return nil
}

// formatNodeStatus 格式化节点状态
func formatNodeStatus(node *transport.NodeInfo, outputFormat string) error {
	switch outputFormat {
	case "json":
		return formatAsJSON(node)
	case "yaml":
		return formatAsYAML(node)
	case "pretty":
		return formatNodeStatusPretty(node)
	default:
		return types.NewCLIUnsupportedOutputFormatError(outputFormat)
	}
}

// formatNodeStatusPretty 格式化节点状态为易读格式
func formatNodeStatusPretty(node *transport.NodeInfo) error {
	fmt.Printf("📊 节点状态详情\n\n")
	fmt.Printf("NodeID:    %s\n", node.NodeID)
	fmt.Printf("Address:    %s\n", node.Addr)
	fmt.Printf("Role:       %s\n", node.Role)
	fmt.Printf("Status:     %s\n", node.Status)
	fmt.Printf("Level:      %d\n", node.Level)

	if node.ParentID != "" {
		fmt.Printf("Parent:     %s\n", node.ParentID)
	}

	fmt.Printf("\n💡 提示:\n")
	fmt.Printf("  使用 --format json 输出 JSON 格式\n")
	fmt.Printf("  使用 --format yaml 输出 YAML 格式\n")

	return nil
}
