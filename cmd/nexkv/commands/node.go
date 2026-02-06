// Package commands 节点管理命令
package commands

import (
	"errors"

	// ⚠️ PR-Libp2p-TransportCleanup: transport包已删除，这些命令暂时禁用
	// TODO: 使用 libp2p Stream 重写 RPC 功能后恢复这些命令
	// "github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/urfave/cli/v2"
)

// newNodeCommand 创建节点管理命令
func newNodeCommand() *cli.Command {
	return &cli.Command{
		Name:        "node",
		Usage:       "节点管理（暂时禁用）",
		Description: "⚠️ PR-Libp2p-TransportCleanup: transport包已删除，节点命令暂时禁用。TODO: 使用 libp2p Stream 重写 RPC 功能后恢复",
		Subcommands: []*cli.Command{
			newNodeAddCommand(),
			newNodeRemoveCommand(),
			newNodeListCommand(),
			newNodeStatusCommand(),
			newNodePingCommand(),
		},
		Action: func(c *cli.Context) error {
			return errors.New("节点命令暂时禁用。PR-Libp2p-TransportCleanup: transport包已删除，待使用 libp2p Stream 重写 RPC 功能")
		},
	}
}

// newNodeAddCommand 添加节点命令
func newNodeAddCommand() *cli.Command {
	return &cli.Command{
		Name:        "add",
		Usage:       "添加节点到集群",
		Description: `⚠️ PR-Libp2p-TransportCleanup: 暂时禁用，待使用 libp2p Stream 重写 RPC 功能后恢复`,
		Action: func(c *cli.Context) error {
			return errors.New("添加节点命令暂时禁用。PR-Libp2p-TransportCleanup: transport包已删除，待使用 libp2p Stream 重写 RPC 功能")
		},
	}
}

// newNodeRemoveCommand 删除节点命令
func newNodeRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:        "remove",
		Usage:       "从集群中删除节点",
		Description: `⚠️ PR-Libp2p-TransportCleanup: 暂时禁用，待使用 libp2p Stream 重写 RPC 功能后恢复`,
		Action: func(c *cli.Context) error {
			return errors.New("删除节点命令暂时禁用。PR-Libp2p-TransportCleanup: transport包已删除，待使用 libp2p Stream 重写 RPC 功能")
		},
	}
}

// newNodeStatusCommand 节点状态命令
func newNodeStatusCommand() *cli.Command {
	return &cli.Command{
		Name:        "status",
		Usage:       "查看节点状态",
		Description: `⚠️ PR-Libp2p-TransportCleanup: 暂时禁用，待使用 libp2p Stream 重写 RPC 功能后恢复`,
		Action: func(c *cli.Context) error {
			return errors.New("节点状态命令暂时禁用。PR-Libp2p-TransportCleanup: transport包已删除，待使用 libp2p Stream 重写 RPC 功能")
		},
	}
}

// newNodeListCommand 列出节点命令
func newNodeListCommand() *cli.Command {
	return &cli.Command{
		Name:        "list",
		Usage:       "列出集群中的所有节点",
		Description: `⚠️ PR-Libp2p-TransportCleanup: 暂时禁用，待使用 libp2p Stream 重写 RPC 功能后恢复`,
		Action: func(c *cli.Context) error {
			return errors.New("列出节点命令暂时禁用。PR-Libp2p-TransportCleanup: transport包已删除，待使用 libp2p Stream 重写 RPC 功能")
		},
	}
}

// newNodePingCommand Ping 节点命令
func newNodePingCommand() *cli.Command {
	return &cli.Command{
		Name:        "ping",
		Usage:       "Ping 节点检查连通性",
		Description: `⚠️ PR-Libp2p-TransportCleanup: 暂时禁用，待使用 libp2p Stream 重写 RPC 功能后恢复`,
		Action: func(c *cli.Context) error {
			return errors.New("ping 节点命令暂时禁用。PR-Libp2p-TransportCleanup: transport包已删除，待使用 libp2p Stream 重写 RPC 功能")
		},
	}
}
