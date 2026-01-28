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

var (
	// Version 版本号（构建时注入）
	Version = "0.1.0"
	// GitCommit Git 提交哈希（构建时注入）
	GitCommit = "unknown"
	// BuildTime 构建时间（构建时注入）
	BuildTime = "unknown"
)

// GetVersionInfo 获取版本信息
func GetVersionInfo() (version, gitCommit, buildTime string) {
	return Version, GitCommit, BuildTime
}

func main() {
	// 设置版本信息到 commands 包
	commands.SetVersionInfo(GetVersionInfo())

	if err := commands.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}
