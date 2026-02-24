// Package commands 节点管理命令
// DDD 重构说明：此文件待根据新的 DDD 架构重新实现
package commands

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

// newNodeCommand 创建节点管理命令（占位实现）
// nolint:unused // 将在 DDD 重构后使用
func newNodeCommand() *cli.Command {
	return &cli.Command{
		Name:        "node",
		Usage:       "节点管理",
		Description: "管理集群中的节点（待实现）",
		Action: func(c *cli.Context) error {
			fmt.Println("节点管理命令待实现")
			return nil
		},
	}
}
