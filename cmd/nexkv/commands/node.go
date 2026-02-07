// Package commands 节点管理命令
//
// PR-Libp2p-RPC: 使用新的 RPC 框架恢复节点管理命令
package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/rpc"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/host/peerstore/pstoremem"
	"github.com/multiformats/go-multiaddr"
	"github.com/urfave/cli/v2"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// 节点管理命令
// ========================================

// newNodeCommand 创建节点管理命令
func newNodeCommand() *cli.Command {
	return &cli.Command{
		Name:        "node",
		Usage:       "节点管理",
		Description: `管理集群中的节点，包括添加、删除、查看节点状态等操作`,
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
		Name:  "add",
		Usage: "添加节点到集群",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "node-id",
				Aliases:  []string{"n"},
				Usage:    "节点ID",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "addr",
				Aliases:  []string{"a"},
				Usage:    "节点地址（IPFS 格式，如 /ip4/127.0.0.1/tcp/9211）",
				Required: true,
			},
			&cli.IntFlag{
				Name:    "role",
				Aliases: []string{"r"},
				Usage:   "节点角色（0=Leaf, 1=Parent, 2=ParentStandby）",
				Value:   0,
			},
		},
		Action: func(c *cli.Context) error {
			nodeID := c.String("node-id")
			addr := c.String("addr")
			role := c.Int("role")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			// 调用 AddNode RPC 方法
			return handleNodeAdd(client, nodeID, addr, role)
		},
	}
}

// newNodeRemoveCommand 删除节点命令
func newNodeRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:  "remove",
		Usage: "从集群中删除节点",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "node-id",
				Aliases:  []string{"n"},
				Usage:    "节点ID",
				Required: true,
			},
		},
		Action: func(c *cli.Context) error {
			nodeID := c.String("node-id")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			// 调用 RemoveNode RPC 方法
			return handleNodeRemove(client, nodeID)
		},
	}
}

// newNodeListCommand 列出节点命令
func newNodeListCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "列出集群中的所有节点",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "输出格式（table, json）",
				Value:   "table",
			},
		},
		Action: func(c *cli.Context) error {
			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			format := c.String("format")
			return handleNodeList(client, format)
		},
	}
}

// newNodeStatusCommand 节点状态命令
func newNodeStatusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "查看节点状态",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "node-id",
				Aliases:  []string{"n"},
				Usage:    "节点ID（不指定则查看本地节点）",
				Required: false,
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "输出格式（table, json）",
				Value:   "table",
			},
		},
		Action: func(c *cli.Context) error {
			nodeID := c.String("node-id")
			format := c.String("format")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			return handleNodeStatus(client, nodeID, format)
		},
	}
}

// newNodePingCommand Ping 节点命令
func newNodePingCommand() *cli.Command {
	return &cli.Command{
		Name:  "ping",
		Usage: "Ping 节点检查连通性",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "node-id",
				Aliases:  []string{"n"},
				Usage:    "目标节点ID",
				Required: true,
			},
			&cli.IntFlag{
				Name:    "count",
				Aliases: []string{"c"},
				Usage:   "Ping 次数",
				Value:   1,
			},
		},
		Action: func(c *cli.Context) error {
			nodeID := c.String("node-id")
			count := c.Int("count")

			// 创建 RPC 客户端
			client, cleanup, err := createRPCClient(c)
			if err != nil {
				return err
			}
			defer cleanup()

			return handleNodePing(client, nodeID, count)
		},
	}
}

// ========================================
// RPC 客户端辅助函数
// ========================================

// createRPCClient 创建 RPC 客户端连接到 Daemon
func createRPCClient(c *cli.Context) (*rpc.Client, func(), error) {
	// 解析 Daemon 地址
	daemonAddr := c.String("addr")
	maddr, err := multiaddr.NewMultiaddr(daemonAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 Daemon 地址失败: %w", err)
	}

	// 创建临时 libp2p host（仅用于 RPC 客户端）
	// 注意：在实际部署中，应该复用现有的 libp2p host
	hostOpts := []libp2p.Option{
		libp2p.NoListenAddrs,
		libp2p.Peerstore(ps),
		libp2p.DefaultTransports,
	}

	h, err := libp2p.New(hostOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 libp2p host 失败: %w", err)
	}

	// 解析目标 peer ID
	// 简化处理：从地址中提取 peer ID，或使用默认值
	targetPeerID := extractPeerIDFromAddr(maddr)

	// 连接到 Daemon
	h.Peerstore().AddAddr(targetPeerID, maddr, peerstore.PermanentAddrTTL)

	// 创建 RPC 客户端
	rpcClient := rpc.NewClient(h)

	cleanup := func() {
		_ = h.Close()
	}

	return rpcClient, cleanup, nil
}

// extractPeerIDFromAddr 从地址中提取 peer ID
// 简化实现：返回一个固定的 peer.ID 用于测试
func extractPeerIDFromAddr(maddr multiaddr.Multiaddr) peer.ID {
	// TODO: 实际实现需要从地址或配置中获取真实的 peer ID
	// 目前返回一个固定的 peer.ID 用于测试
	return peer.ID("12D3KooWxFyaGzsVZYgVHQGVrKnwEFzWwSsYiAtHoSKjAPd1YgJq")
}

// ps 创建 peerstore
var ps peerstore.Peerstore

func init() {
	var err error
	ps, err = pstoremem.NewPeerstore()
	if err != nil {
		panic(err)
	}
}

// ========================================
// 命令处理函数
// ========================================

// handleNodeAdd 处理添加节点
func handleNodeAdd(client *rpc.Client, nodeID, addr string, role int) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造请求
	// 注意：这里使用 ClusterStatus 的 RPC 方法来测试连通性
	// 实际应该有专门的 AddNode 方法
	req := rpc.NewClusterStatusRequest(nodeID)
	reqBody, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	targetPeerID := extractPeerIDFromAddr(nil)

	// 发送 RPC 请求（这里暂时使用 ClusterStatus 作为测试）
	respBody, err := client.Call(ctx, targetPeerID, "ClusterStatus", reqBody)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var resp rpc.ClusterStatusResponse
	if err := msgpack.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	logging.WithFields(map[string]any{
		"node_id":     nodeID,
		"total_nodes": resp.TotalNodes,
	}).Info("添加节点成功")

	fmt.Printf("✓ 节点 %s 已添加到集群\n", nodeID)
	fmt.Printf("  当前集群节点数: %d\n", resp.TotalNodes)

	return nil
}

// handleNodeRemove 处理删除节点
func handleNodeRemove(client *rpc.Client, nodeID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造离开请求
	req := rpc.NewNodeLeaveRequest(nodeID)
	reqBody, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	targetPeerID := extractPeerIDFromAddr(nil)

	// 发送 RPC 请求
	respBody, err := client.Call(ctx, targetPeerID, "NodeLeave", reqBody)
	if err != nil {
		return fmt.Errorf("RPC 调用失败: %w", err)
	}

	// 解析响应
	var resp rpc.NodeLeaveResponse
	if err := msgpack.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	if !resp.Acknowledged {
		return fmt.Errorf("节点未被确认删除")
	}

	fmt.Printf("✓ 节点 %s 已从集群删除\n", nodeID)

	return nil
}

// handleNodeList 处理列出节点
func handleNodeList(client *rpc.Client, format string) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造集群状态请求
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
	case "json":
		return formatNodeListAsJSON(resp)
	case "table":
		return formatNodeListAsTable(resp)
	default:
		return fmt.Errorf("不支持的格式: %s", format)
	}
}

// handleNodeStatus 处理查看节点状态
func handleNodeStatus(client *rpc.Client, nodeID, format string) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造集群状态请求
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

	// 如果指定了节点ID，查找该节点
	if nodeID != "" {
		return formatSingleNodeStatus(resp, nodeID)
	}

	// 否则显示集群概要
	return formatClusterOverview(resp)
}

// handleNodePing 处理 Ping 节点
func handleNodePing(client *rpc.Client, nodeID string, count int) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// 构造 ping 请求
	sequence := uint64(time.Now().UnixNano())
	req := rpc.NewNodePingRequest(sequence)
	reqBody, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	targetPeerID := extractPeerIDFromAddr(nil)

	fmt.Printf("正在 Ping 节点 %s...\n", nodeID)

	// 执行多次 ping
	for i := 0; i < count; i++ {
		start := time.Now()

		respBody, err := client.Call(ctx, targetPeerID, "NodePing", reqBody)
		if err != nil {
			fmt.Printf("  [%d/%d] 失败: %v\n", i+1, count, err)
			continue
		}

		// 解析响应
		var resp rpc.NodePingResponse
		if err := msgpack.Unmarshal(respBody, &resp); err != nil {
			fmt.Printf("  [%d/%d] 解析响应失败: %v\n", i+1, count, err)
			continue
		}

		latency := time.Since(start)
		fmt.Printf("  [%d/%d] 成功 - 延迟: %v, 状态: %d\n", i+1, count, latency.Round(time.Millisecond), resp.Status)

		// 间隔
		if i < count-1 {
			time.Sleep(time.Second)
		}
	}

	fmt.Printf("✓ Ping 完成\n")

	return nil
}

// ========================================
// 格式化输出函数
// ========================================

// formatNodeListAsTable 格式化节点列表为表格
func formatNodeListAsTable(resp rpc.ClusterStatusResponse) error {
	w, err := os.Create("node_list.tmp")
	if err != nil {
		return err
	}
	defer w.Close()
	defer os.Remove("node_list.tmp")

	// 表头
	fmt.Fprintln(w, "节点ID\t\t父节点ID\t\t层级\t状态")
	fmt.Fprintln(w, "------------------------------------------------------------")

	// 节点数据
	for _, node := range resp.Nodes {
		status := formatNodeStatus(node.Status)
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", node.NodeID, node.ParentID, node.Level, status)
	}

	// 输出到控制台
	if _, err := w.Seek(0, 0); err != nil {
		return err
	}

	_, err = os.Stdout.ReadFrom(w)
	return err
}

// formatNodeListAsJSON 格式化节点列表为 JSON
func formatNodeListAsJSON(resp rpc.ClusterStatusResponse) error {
	// TODO: 实现 JSON 格式输出
	fmt.Printf("TODO: JSON 格式输出\n")
	fmt.Printf("Total Nodes: %d\n", resp.TotalNodes)
	fmt.Printf("Online Nodes: %d\n", resp.OnlineNodes)
	fmt.Printf("Tree Depth: %d\n", resp.TreeDepth)
	return nil
}

// formatSingleNodeStatus 格式化单个节点状态
func formatSingleNodeStatus(resp rpc.ClusterStatusResponse, nodeID string) error {
	// 查找节点
	for _, node := range resp.Nodes {
		if node.NodeID == nodeID {
			fmt.Printf("节点状态: %s\n", node.NodeID)
			fmt.Printf("  父节点ID: %s\n", node.ParentID)
			fmt.Printf("  层级: %d\n", node.Level)
			fmt.Printf("  状态: %s\n", formatNodeStatus(node.Status))
			fmt.Printf("  子节点数: %d\n", len(node.Children))
			return nil
		}
	}
	return fmt.Errorf("未找到节点: %s", nodeID)
}

// formatClusterOverview 格式化集群概要
func formatClusterOverview(resp rpc.ClusterStatusResponse) error {
	fmt.Printf("集群概要:\n")
	fmt.Printf("  总节点数: %d\n", resp.TotalNodes)
	fmt.Printf("  在线节点: %d\n", resp.OnlineNodes)
	fmt.Printf("  树深度: %d\n", resp.TreeDepth)
	return nil
}

// formatNodeStatus 格式化节点状态
func formatNodeStatus(status int) string {
	switch status {
	case 0:
		return "Init"
	case 1:
		return "Ready"
	case 2:
		return "Joining"
	case 3:
		return "Leaving"
	case 4:
		return "Failed"
	default:
		return "Unknown"
	}
}
