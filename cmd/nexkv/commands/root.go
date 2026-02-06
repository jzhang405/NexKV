// Package commands CLI 根命令
package commands

import (
	"fmt"
	"os"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/urfave/cli/v2"
)

var (
	// DaemonAddr Daemon 监听地址
	DaemonAddr string
	// Timeout RPC 调用超时
	Timeout time.Duration
	// Verbose 详细输出
	Verbose bool

	// 版本信息（由 main 包设置）
	appVersion   string
	appGitCommit string
	appBuildTime string
)

// NewApp 创建 CLI 应用
func NewApp() *cli.App {
	app := &cli.App{
		Name:    "nexkv",
		Usage:   "NexKV 分布式 KV 存储系统命令行工具",
		Version: "0.1.0",
		Description: `NexKV 是一个轻量化的分布式 KV 存储系统

CLI 通过 RPC 协议与 Daemon 守护进程通信，
执行节点管理、集群管理、数据操作等命令。`,
		Before: func(c *cli.Context) error {
			// 初始化日志
			DaemonAddr = c.String("addr")
			Timeout = c.Duration("timeout")
			Verbose = c.Bool("verbose")
			initLogging()
			return nil
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "addr",
				Aliases: []string{"a"},
				Value:   "127.0.0.1:9211",
				Usage:   "Daemon 监听地址",
				EnvVars: []string{"NEXKV_ADDR"},
			},
			&cli.DurationFlag{
				Name:    "timeout",
				Aliases: []string{"t"},
				Value:   30 * time.Second,
				Usage:   "RPC 调用超时",
				EnvVars: []string{"NEXKV_TIMEOUT"},
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"V"},
				Value:   false,
				Usage:   "详细输出",
				EnvVars: []string{"NEXKV_VERBOSE"},
			},
		},
		Commands: []*cli.Command{
			newNodeCommand(),
			newClusterCommand(),
			newVersionCommand(),
		},
	}

	return app
}

// initLogging 初始化日志
func initLogging() {
	// TODO: 根据配置初始化日志
	// 目前使用简单的配置
	if Verbose {
		// 设置为 debug 级别
		logging.Debug("详细模式已启用")
	}
}

// Execute 执行命令
func Execute() error {
	app := NewApp()
	return app.Run(os.Args)
}

// SetVersionInfo 设置版本信息（由 main 包调用）
func SetVersionInfo(version, gitCommit, buildTime string) {
	appVersion = version
	appGitCommit = gitCommit
	appBuildTime = buildTime
}

// newVersionCommand 创建版本命令
func newVersionCommand() *cli.Command {
	return &cli.Command{
		Name:        "version",
		Usage:       "显示版本信息",
		Description: `显示 NexKV CLI 和 Daemon 的版本信息`,
		Action: func(c *cli.Context) error {
			fmt.Println("NexKV CLI")
			fmt.Printf("Version: %s\n", appVersion)
			fmt.Printf("Git Commit: %s\n", appGitCommit)
			fmt.Printf("Build Time: %s\n", appBuildTime)
			return nil
		},
	}
}
