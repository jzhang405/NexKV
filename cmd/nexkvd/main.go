// Daemon NexKV 守护进程
//
// 长期运行的守护进程，负责：
//   - 启动 RPC Server 接收 CLI 命令
//   - 初始化并管理所有核心模块
//   - 处理信号优雅关闭
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/cluster"
	"github.com/jzhang405/NexKV/internal/metadata/config"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/identity"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/rpc/server"
)

var (
	// 配置文件路径
	configPath = flag.String("config", "./config.yaml", "配置文件路径")
	// 集群名称
	clusterName = flag.String("cluster", "", "集群名称（覆盖配置文件）")
	// 节点 ID
	nodeID = flag.String("node-id", "", "节点 ID（覆盖配置文件）")
	// 节点地址
	nodeAddr = flag.String("addr", "", "节点监听地址（覆盖配置文件）")
	// 环境（dev/cluster）
	env = flag.String("env", "", "运行环境：dev 或 cluster（覆盖配置文件）")
	// 日志级别
	logLevel = flag.String("log-level", "", "日志级别：debug/info/warn/error（覆盖配置文件）")
	// 显示版本
	showVersion = flag.Bool("version", false, "显示版本信息")
)

const (
	// Version 版本号
	Version = "0.1.0"
	// GitCommit Git 提交哈希（构建时注入）
	GitCommit = "unknown"
	// BuildTime 构建时间（构建时注入）
	BuildTime = "unknown"
)

// Daemon 守护进程
type Daemon struct {
	// 配置
	cfg *config.Config

	// 组件
	transport   transport.Transport
	rpcServer   *server.RPCServer
	coordinator interface{} // TreeCoordinator（使用 interface{} 避免循环依赖）

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
}

func main() {
	flag.Parse()

	// 显示版本信息
	if *showVersion {
		fmt.Printf("NexKV Daemon %s\n", Version)
		fmt.Printf("Git Commit: %s\n", GitCommit)
		fmt.Printf("Build Time: %s\n", BuildTime)
		os.Exit(0)
	}

	// 初始化日志
	if err := initLogging(); err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}

	logging.Info("NexKV Daemon 启动中...")

	// 加载配置
	cfg, err := loadConfig()
	if err != nil {
		logging.WithField("error", err).Fatal("加载配置失败")
		os.Exit(1)
	}

	// 创建守护进程
	daemon, err := NewDaemon(cfg)
	if err != nil {
		logging.WithField("error", err).Fatal("创建守护进程失败")
		os.Exit(1)
	}

	// 启动守护进程
	if err := daemon.Start(); err != nil {
		logging.WithField("error", err).Fatal("启动守护进程失败")
		os.Exit(1)
	}

	logging.WithFields(map[string]any{
		"version":     Version,
		"cluster":     cfg.Cluster.Name,
		"node_id":     cfg.Cluster.NodeID,
		"listen_addr": cfg.Network.ListenAddr,
	}).Info("NexKV Daemon 启动成功")

	// 等待信号
	waitForSignal(daemon)

	// 停止守护进程
	if err := daemon.Stop(); err != nil {
		logging.WithField("error", err).Error("停止守护进程失败")
		os.Exit(1)
	}

	logging.Info("NexKV Daemon 已停止")
}

// NewDaemon 创建守护进程
func NewDaemon(cfg *config.Config) (*Daemon, error) {
	if cfg == nil {
		return nil, fmt.Errorf("配置不能为空")
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Daemon{
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Start 启动守护进程
func (d *Daemon) Start() error {
	logging.Info("初始化传输层...")

	// 解析监听地址
	host, port, err := parseListenAddr(d.cfg.Network.ListenAddr)
	if err != nil {
		return fmt.Errorf("解析监听地址失败: %w", err)
	}

	// 使用 identity 包生成节点 ID（FNV-1a 64-bit 哈希）
	nodeID := identity.GenerateNodeIDFromPorts(host, 0, port)

	// 创建消息序列号生成器（原子递增）
	msgSeqGen := identity.NewMsgSeqGenerator()

	logging.WithFields(map[string]any{
		"host":    host,
		"port":    port,
		"node_id": nodeID,
	}).Info("节点 ID 生成成功")

	// 创建传输层
	trans, err := transport.NewUDPTransport(d.cfg.Network.ListenAddr)
	if err != nil {
		return fmt.Errorf("创建传输层失败: %w", err)
	}

	// 启动传输层
	// 参数：nodeID, msgSeqGenerator, listenAddr
	if err := trans.Start(&nodeID, msgSeqGen.Next, d.cfg.Network.ListenAddr); err != nil {
		return fmt.Errorf("启动传输层失败: %w", err)
	}

	d.transport = trans

	logging.Info("启动 RPC Server...")

	// 创建 RPC Server
	rpcServer, err := server.NewRPCServer(&server.Config{
		Addr:      d.cfg.Network.ListenAddr,
		Transport: trans,
	})
	if err != nil {
		return fmt.Errorf("创建 RPC Server 失败: %w", err)
	}

	// 启动 RPC Server
	if err := rpcServer.Start(); err != nil {
		return fmt.Errorf("启动 RPC Server 失败: %w", err)
	}

	d.rpcServer = rpcServer

	// 初始化 TreeCoordinator
	logging.Info("初始化 TreeCoordinator...")

	// 使用配置的节点 ID，如果为空则生成一个
	daemonNodeID := d.cfg.Cluster.NodeID
	if daemonNodeID == "" {
		daemonNodeID = fmt.Sprintf("node-%d", nodeID)
	}

	coordinatorConfig := cluster.DefaultTreeCoordinatorConfig()
	coordinator, err := cluster.NewTreeCoordinator(
		daemonNodeID,
		d.cfg.Network.ListenAddr,
		trans,
		coordinatorConfig,
	)
	if err != nil {
		return fmt.Errorf("创建 TreeCoordinator 失败: %w", err)
	}

	// 启动 TreeCoordinator
	if err := coordinator.Start(); err != nil {
		return fmt.Errorf("启动 TreeCoordinator 失败: %w", err)
	}

	d.coordinator = coordinator

	// 注册 TreeCoordinator RPC Handlers
	// TODO: 实现 cluster.RegisterTreeCoordinatorHandlers
	// logging.Info("注册 TreeCoordinator RPC Handlers...")
	// if err := cluster.RegisterTreeCoordinatorHandlers(rpcServer, coordinator); err != nil {
	// 	return fmt.Errorf("注册 TreeCoordinator Handlers 失败: %w", err)
	// }

	logging.WithFields(map[string]any{
		"node_id": daemonNodeID,
	}).Info("TreeCoordinator 启动成功")

	return nil
}

// Stop 停止守护进程
func (d *Daemon) Stop() error {
	logging.Info("正在停止守护进程...")

	var errs []error

	// 停止 TreeCoordinator
	if d.coordinator != nil {
		// 使用类型断言调用 Stop() 方法
		if coord, ok := d.coordinator.(interface{ Stop() error }); ok {
			if err := coord.Stop(); err != nil {
				errs = append(errs, fmt.Errorf("停止 TreeCoordinator 失败: %w", err))
			}
		}
	}

	// 停止 RPC Server
	if d.rpcServer != nil {
		if err := d.rpcServer.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("停止 RPC Server 失败: %w", err))
		}
	}

	// 停止传输层
	if d.transport != nil {
		if err := d.transport.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("停止传输层失败: %w", err))
		}
	}

	// 取消上下文
	d.cancel()

	if len(errs) > 0 {
		return fmt.Errorf("停止守护进程时发生错误: %v", errs)
	}

	return nil
}

// loadConfig 加载配置
func loadConfig() (*config.Config, error) {
	// 从文件加载配置
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return nil, err
	}

	// 命令行参数覆盖配置文件
	if *clusterName != "" {
		cfg.Cluster.Name = *clusterName
	}
	if *nodeID != "" {
		cfg.Cluster.NodeID = *nodeID
	}
	if *nodeAddr != "" {
		cfg.Cluster.NodeAddr = *nodeAddr
		cfg.Network.ListenAddr = *nodeAddr
	}
	if *logLevel != "" {
		cfg.Logging.Level = *logLevel
	}

	// 环境变量覆盖（可选）
	if envName := os.Getenv("NEXKV_CLUSTER"); envName != "" {
		cfg.Cluster.Name = envName
	}
	if envNodeID := os.Getenv("NEXKV_NODE_ID"); envNodeID != "" {
		cfg.Cluster.NodeID = envNodeID
	}
	if envNodeAddr := os.Getenv("NEXKV_NODE_ADDR"); envNodeAddr != "" {
		cfg.Cluster.NodeAddr = envNodeAddr
		cfg.Network.ListenAddr = envNodeAddr
	}

	return cfg, nil
}

// initLogging 初始化日志
func initLogging() error {
	// TODO: 实现完整的日志初始化
	// 目前使用简单的配置，后续根据 cfg.Logging 配置初始化

	// 设置默认日志级别
	level := "info"
	if *logLevel != "" {
		level = *logLevel
	}

	_ = level // 占位，后续实现

	return nil
}

// waitForSignal 等待系统信号
func waitForSignal(daemon *Daemon) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	sig := <-sigCh

	logging.WithField("signal", sig.String()).Info("收到信号，准备关闭...")

	// 创建超时上下文
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 优雅关闭
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- daemon.Stop()
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			logging.WithField("error", err).Error("守护进程停止失败")
		}
	case <-ctx.Done():
		logging.Error("守护进程停止超时，强制退出")
	case sig := <-sigCh:
		logging.WithField("signal", sig.String()).Warning("收到第二次信号，强制退出")
	}
}

// parseListenAddr 解析监听地址
// 支持 "host:port" 和 ":port" 格式
func parseListenAddr(listenAddr string) (host string, port int, err error) {
	if listenAddr == "" {
		return "", 0, fmt.Errorf("监听地址不能为空")
	}

	h, p, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", 0, fmt.Errorf("解析地址失败: %w", err)
	}

	if h == "" {
		h = "0.0.0.0"
	}

	port, err = parsePort(p)
	if err != nil {
		return "", 0, err
	}

	return h, port, nil
}

// parsePort 解析端口号
func parsePort(portStr string) (int, error) {
	port, err := net.LookupPort("udp", "0.0.0.0:"+portStr)
	if err == nil {
		return port, nil
	}

	var portInt int
	_, err = fmt.Sscanf(portStr, "%d", &portInt)
	if err != nil || portInt <= 0 || portInt > 65535 {
		return 0, fmt.Errorf("无效的端口号: %s", portStr)
	}

	return portInt, nil
}
