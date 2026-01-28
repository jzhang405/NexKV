// CLI NexKV 命令行工具
//
// 通过 RPC 协议与 Daemon 守护进程通信
// 执行节点管理、集群管理、数据操作等命令
package main

import (
	"fmt"
	"os"

	"github.com/jzhang405/NexKV/cmd/nexkv/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}
