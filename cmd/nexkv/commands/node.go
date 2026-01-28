// Package commands 节点管理命令
package commands

import (
	"fmt"
	"time"

	"github.com/urfave/cli/v2"
)

// newNodeCommand 创建节点管理命令
func newNodeCommand() *cli.Command {
	return &cli.Command{
		Name:  "node",
		Usage: "节点管理",
		Description: `管理集群中的节点，包括添加、删除、列表、ping 等操作`,
		Subcommands: []*cli.Command{
			newNodeAddCommand(),
			newNodeRemoveCommand(),
			newNodeListCommand(),
			newNodePingCommand(),
		},
	}
}

// newNodeAddCommand 添加节点命令
func newNodeAddCommand() *cli.Command {
	return &cli.Command{
		Name:  "add",
		Usage: "添加节点到集群",
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
				handleError(fmt.Errorf("--id 参数不能为空"))
			}
			if nodeAddr == "" {
				handleError(fmt.Errorf("--addr 参数不能为空"))
			}

			// 创建 RPC 客户端
			rpcClient, err := NewRPCClient(DaemonAddr)
			if err != nil {
				handleError(fmt.Errorf("连接 Daemon 失败: %w", err))
			}
			defer rpcClient.Close()

			// 调用 RPC 添加节点
			ctx, cancel := createRPCContext()
			defer cancel()

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
				fmt.Printf("添加节点: ID=%s, Addr=%s\n", nodeID, nodeAddr)
			}

			if err := rpcClient.AddNode(ctx, nodeID, nodeAddr); err != nil {
				handleError(fmt.Errorf("添加节点失败: %w", err))
			}

			fmt.Printf("✓ 节点 %s 添加成功\n", nodeID)
			return nil
		},
	}
}

// newNodeRemoveCommand 删除节点命令
func newNodeRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:  "remove",
		Usage: "从集群中删除节点",
		Description: `从集群中删除指定节点，可选择强制删除`,
		ArgsUsage: "[node-id]",
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
				handleError(fmt.Errorf("需要指定节点 ID"))
			}

			nodeID := c.Args().First()

			if !force {
				// 确认提示
				fmt.Printf("确认要删除节点 %s 吗？(y/N): ", nodeID)
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					fmt.Println("操作已取消")
					return nil
				}
			}

			// 创建 RPC 客户端
			rpcClient, err := NewRPCClient(DaemonAddr)
			if err != nil {
				handleError(fmt.Errorf("连接 Daemon 失败: %w", err))
			}
			defer rpcClient.Close()

			// 调用 RPC 删除节点
			ctx, cancel := createRPCContext()
			defer cancel()

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
				fmt.Printf("删除节点: ID=%s\n", nodeID)
			}

			if err := rpcClient.RemoveNode(ctx, nodeID); err != nil {
				handleError(fmt.Errorf("删除节点失败: %w", err))
			}

			fmt.Printf("✓ 节点 %s 删除成功\n", nodeID)
			return nil
		},
	}
}

// newNodeListCommand 列出节点命令
func newNodeListCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "列出集群中的所有节点",
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
			&cli.StringFlag{
				Name:  "node-id",
				Value: "",
				Usage: "查询节点 ID（默认: root）",
			},
		},
		Action: func(c *cli.Context) error {
			verbose := c.Bool("verbose")
			outputFormat := c.String("format")
			nodeID := c.String("node-id")

			// 创建 RPC 客户端
			rpcClient, err := NewRPCClient(DaemonAddr)
			if err != nil {
				handleError(fmt.Errorf("连接 Daemon 失败: %w", err))
			}
			defer rpcClient.Close()

			// 调用 RPC 查询节点列表
			ctx, cancel := createRPCContext()
			defer cancel()

			// 使用当前节点 ID 查询（如果是空，默认为 root）
			if nodeID == "" {
				nodeID = "root"
			}

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
			}

			response, err := rpcClient.ListNode(ctx, nodeID)
			if err != nil {
				handleError(fmt.Errorf("查询节点列表失败: %w", err))
			}

			// 格式化输出
			if err := FormatNodeList(response, verbose, outputFormat); err != nil {
				handleError(fmt.Errorf("格式化输出失败: %w", err))
			}

			return nil
		},
	}
}

// newNodePingCommand Ping 节点命令
func newNodePingCommand() *cli.Command {
	return &cli.Command{
		Name:  "ping",
		Usage: "Ping 节点检查连通性",
		Description: `向指定节点发送 Ping 请求，检查连通性和延迟`,
		ArgsUsage: "[node-id]",
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
				handleError(fmt.Errorf("需要指定节点 ID"))
			}

			nodeID := c.Args().First()

			// 创建 RPC 客户端
			rpcClient, err := NewRPCClient(DaemonAddr)
			if err != nil {
				handleError(fmt.Errorf("连接 Daemon 失败: %w", err))
			}
			defer rpcClient.Close()

			if Verbose {
				fmt.Printf("连接到 Daemon: %s\n", DaemonAddr)
			}

			// 执行 Ping 测试
			for i := 0; i < count; i++ {
				ctx, cancel := createRPCContext()

				start := time.Now()
				err := rpcClient.PingNode(ctx, nodeID, uint64(i+1))
				elapsed := time.Since(start)

				cancel()

				if err != nil {
					fmt.Printf("Ping %s: 超时或失败 (%v)\n", nodeID, err)
				} else {
					fmt.Printf("Ping %s: 成功 (耗时: %v)\n", nodeID, elapsed)
				}

				// 多次 ping 之间间隔 1 秒
				if count > 1 && i < count-1 {
					time.Sleep(1 * time.Second)
				}
			}

			return nil
		},
	}
}
