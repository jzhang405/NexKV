// Daemon NexKV 守护进程
//
// 长期运行的守护进程，负责：
//   - 启动 RPC Server 接收 CLI 命令
//   - 初始化并管理所有核心模块
//   - 处理信号优雅关闭
//
// DDD 重构说明：此文件待根据新的 DDD 架构重新实现
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/internal/infrastructure/clock"
	"github.com/jzhang405/NexKV/internal/infrastructure/transport"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

var (
	Version   = "0.0.1"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

type AppContext struct {
	ConfigPath string
	Env        string
	HostID     string
	NodeID     string

	clockProvider  service.ClockProvider
	transportLayer service.Transport

	ctx    context.Context
	cancel context.CancelFunc
}

func main() {
	app := &cli.App{
		Name:     "nexkvd",
		Usage:    "NexKV 守护进程 - 长期运行的集群节点服务",
		Version:  Version,
		Compiled: time.Now(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "配置文件路径",
				Value:   "./config.yaml",
				EnvVars: []string{"NEXKV_CONFIG"},
			},
			&cli.StringFlag{
				Name:    "host-id",
				Usage:   "主机 ID",
				EnvVars: []string{"NEXKV_HOST_ID"},
			},
			&cli.StringFlag{
				Name:    "node-id",
				Aliases: []string{"i"},
				Usage:   "节点 ID",
				EnvVars: []string{"NEXKV_NODE_ID"},
			},
			&cli.StringFlag{
				Name:    "log-level",
				Aliases: []string{"l"},
				Usage:   "日志级别",
				EnvVars: []string{"NEXKV_LOG_LEVEL"},
			},
		},
		Action: runDaemon,
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemon(c *cli.Context) error {
	logLevel := c.String("log-level")
	hostID := c.String("host-id")
	nodeID := c.String("node-id")

	if err := initLogging(logLevel); err != nil {
		return err
	}

	fmt.Println("NexKV Daemon 启动中...")

	appCtx, err := NewAppContext(hostID, nodeID)
	if err != nil {
		return err
	}

	if err := appCtx.Initialize(); err != nil {
		return err
	}

	fmt.Println("NexKV Daemon 启动成功")

	waitForSignal(appCtx)

	if err := appCtx.Shutdown(); err != nil {
		return err
	}

	fmt.Println("NexKV Daemon 已停止")
	return nil
}

func NewAppContext(hostID, nodeID string) (*AppContext, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &AppContext{
		HostID: hostID,
		NodeID: nodeID,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (app *AppContext) Initialize() error {
	cfg := transport.DefaultConfig()
	cfg.EnableDiscovery = true

	transportLayer, err := transport.NewLibp2pTransport(app.ctx, cfg)
	if err != nil {
		return fmt.Errorf("创建 transport 失败: %w", err)
	}

	app.transportLayer = transportLayer

	app.clockProvider = clock.NewHLCProvider()
	fmt.Println("Clock Provider 初始化成功")

	return nil
}

func (app *AppContext) Shutdown() error {
	fmt.Println("正在停止守护进程...")

	if app.transportLayer != nil {
		if err := app.transportLayer.Close(); err != nil {
			return fmt.Errorf("关闭 transport 失败: %w", err)
		}
	}

	app.cancel()

	fmt.Println("守护进程已停止")
	return nil
}

func initLogging(logLevel string) error {
	level := "info"
	if logLevel != "" {
		level = strings.ToLower(logLevel)
	}

	logger := logrus.New()

	switch level {
	case "debug":
		logger.SetLevel(logrus.DebugLevel)
	case "info":
		logger.SetLevel(logrus.InfoLevel)
	case "warn", "warning":
		logger.SetLevel(logrus.WarnLevel)
	case "error":
		logger.SetLevel(logrus.ErrorLevel)
	default:
		logger.SetLevel(logrus.InfoLevel)
	}

	return nil
}

func waitForSignal(appCtx *AppContext) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("等待信号... (Ctrl+C 停止)")
	<-sigChan
	fmt.Println("\n收到停止信号")
}
