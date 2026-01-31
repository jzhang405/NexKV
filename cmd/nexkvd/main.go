// Daemon NexKV 守护进程
//
// 长期运行的守护进程，负责：
//   - 启动 RPC Server 接收 CLI 命令
//   - 初始化并管理所有核心模块
//   - 处理信号优雅关闭
//
// 架构：遵循 PR-032 第 3.8.2 节规范
package main

import (
	"context"
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
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/urfave/cli/v2"
)

var (
	// Version 版本号（构建时注入）
	Version = "0.0.1"
	// GitCommit Git 提交哈希（构建时注入）
	GitCommit = "unknown"
	// BuildTime 构建时间（构建时注入）
	BuildTime = "unknown"
)

// ========================================
// AppContext 应用上下文（依赖注入容器）
// ========================================

// AppContext 应用上下文（遵循 PR-032 第 3.8.2 节规范）
// PR-037: 添加 hostID 和 nodeID 用于从三级配置结构中选择节点
type AppContext struct {
	// 配置
	ConfigPath string
	Env        string
	HostID     string // PR-037: 主机 ID（用于从三级配置中选择 Host）
	NodeID     string // PR-037: 节点 ID（用于从三级配置中选择 Node）

	// 核心组件
	Coordinator  *cluster.TreeCoordinator
	TCPTransport transport.Transport
	UDPTransport transport.Transport

	// RPC 组件
	RPCClient *transport.RPCClient // RPC 客户端（主动调用）
	RPCServer *transport.RPCServer // RPC 服务端（接收请求）

	// 监控组件（预留，待实现）
	// FailureDetector *cluster.FailureDetector

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
}

// ========================================
// 主函数
// ========================================

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
				Name:    "cluster",
				Aliases: []string{"n"},
				Usage:   "集群名称（覆盖配置文件）",
				EnvVars: []string{"NEXKV_CLUSTER"},
			},
			&cli.StringFlag{
				Name:    "host-id",
				Aliases: []string{"h"},
				Usage:   "主机 ID（PR-037: 从三级配置结构中指定主机）",
				EnvVars: []string{"NEXKV_HOST_ID"},
			},
			&cli.StringFlag{
				Name:    "node-id",
				Aliases: []string{"i"},
				Usage:   "节点 ID（覆盖配置文件）",
				EnvVars: []string{"NEXKV_NODE_ID"},
			},
			&cli.StringFlag{
				Name:    "addr",
				Aliases: []string{"a"},
				Usage:   "节点监听地址（覆盖配置文件）",
				EnvVars: []string{"NEXKV_NODE_ADDR"},
			},
			&cli.StringFlag{
				Name:    "env",
				Aliases: []string{"e"},
				Usage:   "运行环境：dev 或 cluster（覆盖配置文件）",
				EnvVars: []string{"NEXKV_ENV"},
			},
			&cli.StringFlag{
				Name:    "log-level",
				Aliases: []string{"l"},
				Usage:   "日志级别：debug/info/warn/error（覆盖配置文件）",
				EnvVars: []string{"NEXKV_LOG_LEVEL"},
			},
		},
		Action: runDaemon,
	}

	if err := app.Run(os.Args); err != nil {
		logging.WithField("error", err).Fatal("守护进程启动失败")
		os.Exit(1)
	}
}

// runDaemon 运行守护进程
func runDaemon(c *cli.Context) error {
	// 获取配置参数
	configPath := c.String("config")
	env := c.String("env")
	logLevel := c.String("log-level")
	hostID := c.String("host-id") // PR-037: 获取主机 ID

	// 初始化日志
	if err := initLogging(logLevel); err != nil {
		return types.NewDaemonInitializeLoggingError(err)
	}

	logging.Info("NexKV Daemon 启动中...")

	// 加载配置
	cfg, err := loadConfig(c, configPath)
	if err != nil {
		return types.NewDaemonLoadConfigError(err)
	}

	// 创建应用上下文
	appCtx, err := NewAppContext(cfg, env, configPath, hostID)
	if err != nil {
		return types.NewDaemonCreateAppContextError(err)
	}

	// 初始化所有组件（7 步初始化流程）
	if err := appCtx.Initialize(cfg); err != nil {
		return types.NewDaemonInitializeError(err)
	}

	// PR-037: 从三级配置结构中获取节点信息用于日志输出
	// P1-5 修复：确保 displayNodeID 始终有值，避免输出空的 host_id
	displayNodeID := hostID
	if displayNodeID == "" {
		if len(cfg.Cluster.Hosts) > 0 && cfg.Cluster.Hosts[0].HostID != "" {
			displayNodeID = cfg.Cluster.Hosts[0].HostID
		} else {
			// P1-5 修复：降级处理，使用集群名称作为 fallback
			displayNodeID = cfg.Cluster.Name + "-unknown"
			logging.Warnf("无法获取有效的 Host ID，使用 fallback: %s", displayNodeID)
		}
	}

	logging.WithFields(map[string]any{
		"version":     Version,
		"cluster":     cfg.Cluster.Name,
		"host_id":     displayNodeID,
		"listen_addr": cfg.Network.ListenAddr,
	}).Info("NexKV Daemon 启动成功")

	// 等待信号
	waitForSignal(appCtx)

	// 停止守护进程
	if err := appCtx.Shutdown(); err != nil {
		return types.NewDaemonShutdownError(err)
	}

	logging.Info("NexKV Daemon 已停止")
	return nil
}

// ========================================
// AppContext 创建
// ========================================

// NewAppContext 创建应用上下文
// PR-037: 添加 hostID 参数用于从三级配置结构中选择节点
func NewAppContext(cfg *config.Config, env, configPath, hostID string) (*AppContext, error) {
	if cfg == nil {
		return nil, types.NewDaemonConfigNilError()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &AppContext{
		ConfigPath: configPath,
		Env:        env,
		HostID:     hostID, // PR-037: 存储主机 ID
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// ========================================
// 7 步初始化流程（PR-032 第 3.8.2 节）
// ========================================

// Initialize 初始化所有组件（遵循 7 步流程）
func (app *AppContext) Initialize(cfg *config.Config) error {
	// 解析监听地址
	host, port, err := parseListenAddr(cfg.Network.ListenAddr)
	if err != nil {
		return types.NewDaemonParseListenAddrError(err)
	}

	// 使用 identity 包生成节点 ID（FNV-1a 64-bit 哈希）
	// 注意：函数内部使用 os.Hostname() 获取主机名
	nodeID, err := identity.GenerateNodeIDFromPorts(0, port)
	if err != nil {
		return types.NewDaemonGenerateNodeIDError(err)
	}

	// 创建消息序列号生成器（原子递增）
	msgSeqGen := identity.NewMsgSeqGenerator()

	logging.WithFields(map[string]any{
		"host":    host,
		"port":    port,
		"node_id": nodeID,
	}).Info("节点 ID 生成成功")

	// =====================================
	// 步骤 1: 创建 Transport 层（TCP + UDP）
	// =====================================
	logging.Info("步骤 1: 创建 Transport 层...")

	// 创建 TCP 传输层（用于可靠消息）
	tcpTransport, err := transport.NewTCPTransport(cfg.Network.ListenAddr)
	if err != nil {
		return types.NewDaemonCreateTCPTransportError(err)
	}

	// 创建 UDP 传输层（用于尽力型消息）
	udpTransport, err := transport.NewUDPTransport(cfg.Network.ListenAddr)
	if err != nil {
		_ = tcpTransport.Stop()
		return types.NewDaemonCreateUDPTransportError(err)
	}

	// 启动 TCP 传输层
	if err := tcpTransport.Start(&nodeID, msgSeqGen.Next, cfg.Network.ListenAddr); err != nil {
		_ = tcpTransport.Stop()
		_ = udpTransport.Stop()
		return types.NewDaemonStartTCPTransportError(err)
	}

	// 启动 UDP 传输层
	if err := udpTransport.Start(&nodeID, msgSeqGen.Next, cfg.Network.ListenAddr); err != nil {
		_ = tcpTransport.Stop()
		_ = udpTransport.Stop()
		return types.NewDaemonStartUDPTransportError(err)
	}

	app.TCPTransport = tcpTransport
	app.UDPTransport = udpTransport

	// =====================================
	// 步骤 2: 创建 RPC Client
	// =====================================
	logging.Info("步骤 2: 创建 RPC Client...")

	rpcClientConfig := transport.DefaultRPCClientConfig()
	rpcClientConfig.RequestTimeout = 30 * time.Second

	rpcClient, err := transport.NewRPCClient(
		tcpTransport, // TCP 用于可靠消息
		udpTransport, // UDP 用于尽力型消息
		rpcClientConfig,
	)
	if err != nil {
		_ = tcpTransport.Stop()
		_ = udpTransport.Stop()
		return types.NewDaemonCreateRPCClientError(err)
	}

	app.RPCClient = rpcClient

	// =====================================
	// 步骤 3: 创建 RPC Server
	// =====================================
	logging.Info("步骤 3: 创建 RPC Server...")

	// 注意：此时 coordinator 还未创建，先创建一个空的 handler
	// 在步骤 6 之后会重新设置 coordinator
	// 使用 cluster 包中的 RPCHandler 实现
	rpcHandler := cluster.NewTreeCoordinatorRPCHandler(nil)

	rpcServer, err := transport.NewRPCServer(
		tcpTransport, // TCP 用于可靠消息
		udpTransport, // UDP 用于尽力型消息
		rpcHandler,   // TreeCoordinator RPC 处理器
		nil,          // 使用默认配置
	)
	if err != nil {
		_ = tcpTransport.Stop()
		_ = udpTransport.Stop()
		return types.NewDaemonCreateRPCServerError(err)
	}

	app.RPCServer = rpcServer

	// =====================================
	// 步骤 4: 启动 RPC Server（监听其他节点请求）
	// =====================================
	logging.Info("步骤 4: 启动 RPC Server...")

	if err := rpcServer.Start(); err != nil {
		_ = tcpTransport.Stop()
		_ = udpTransport.Stop()
		return types.NewDaemonStartRPCServerError(err)
	}

	// =====================================
	// 步骤 5: 启动 RPC Client（响应处理协程）
	// =====================================
	logging.Info("步骤 5: 启动 RPC Client...")

	if err := rpcClient.Start(); err != nil {
		_ = rpcServer.Stop()
		_ = tcpTransport.Stop()
		_ = udpTransport.Stop()
		return types.NewDaemonStartRPCClientError(err)
	}

	// =====================================
	// 步骤 6: 创建 TreeCoordinator（只注入 RPC 接口）
	// =====================================
	logging.Info("步骤 6: 创建 TreeCoordinator...")

	// P1-3: 始终使用配置中的节点 ID
	// PR-037: 从三级配置结构中获取节点 ID
	configNodeID, _, err := selectedNodeInfo(cfg, app.HostID, "")
	var daemonNodeID string
	if err != nil {
		// P1-1 修复：降级处理，使用自动生成的节点 ID
		// 避免因配置错误导致守护进程崩溃
		logging.Warnf("从配置获取节点 ID 失败: %v，使用自动生成的 ID", err)
		daemonNodeID = fmt.Sprintf("%d", nodeID)
	} else {
		daemonNodeID = configNodeID
	}

	coordinatorConfig := cluster.DefaultTreeCoordinatorConfig()
	coordinator, err := cluster.NewTreeCoordinator(
		daemonNodeID,
		cfg.Network.ListenAddr,
		coordinatorConfig,
		&cfg.Cluster, // PR-036: 传递集群配置（包含种子节点列表）
	)
	if err != nil {
		_ = rpcClient.Stop()
		_ = rpcServer.Stop()
		_ = tcpTransport.Stop()
		_ = udpTransport.Stop()
		return types.NewDaemonCreateTreeCoordinatorError(err)
	}

	app.Coordinator = coordinator

	// 更新 RPC Handler 的 coordinator 引用
	rpcHandler.SetCoordinator(coordinator)

	// =====================================
	// 步骤 7: 启动 TreeCoordinator
	// =====================================
	logging.Info("步骤 7: 启动 TreeCoordinator...")

	if err := coordinator.Start(); err != nil {
		return types.NewDaemonStartTreeCoordinatorError(err)
	}

	logging.WithFields(map[string]any{
		"node_id": daemonNodeID,
	}).Info("TreeCoordinator 启动成功")

	return nil
}

// ========================================
// Shutdown 优雅关闭
// ========================================

// Shutdown 停止守护进程（逆序关闭组件）
func (app *AppContext) Shutdown() error {
	logging.Info("正在停止守护进程...")

	var errs []error

	// 按逆序停止组件
	// 7. 停止 TreeCoordinator
	if app.Coordinator != nil {
		if err := app.Coordinator.Stop(); err != nil {
			errs = append(errs, types.NewDaemonStopTreeCoordinatorError(err))
		}
	}

	// 5. 停止 RPC Client
	if app.RPCClient != nil {
		if err := app.RPCClient.Stop(); err != nil {
			errs = append(errs, types.NewDaemonStopRPCClientError(err))
		}
	}

	// 4. 停止 RPC Server
	if app.RPCServer != nil {
		if err := app.RPCServer.Stop(); err != nil {
			errs = append(errs, types.NewDaemonStopRPCServerError(err))
		}
	}

	// 1. 停止 Transport 层
	if app.TCPTransport != nil {
		if err := app.TCPTransport.Stop(); err != nil {
			errs = append(errs, types.NewDaemonStopTCPTransportError(err))
		}
	}

	if app.UDPTransport != nil {
		if err := app.UDPTransport.Stop(); err != nil {
			errs = append(errs, types.NewDaemonStopUDPTransportError(err))
		}
	}

	// 取消上下文
	app.cancel()

	if len(errs) > 0 {
		return types.NewDaemonShutdownMultipleErrorsError(len(errs))
	}

	return nil
}

// ========================================
// 辅助函数
// ========================================

// loadConfig 加载配置
// PR-037: 移除对 NodeID/NodeAddr 的直接赋值，改为通过 host-id 和 node-id 选择
func loadConfig(c *cli.Context, configPath string) (*config.Config, error) {
	// 从文件加载配置
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	// 命令行参数覆盖配置文件
	if c.IsSet("cluster") {
		cfg.Cluster.Name = c.String("cluster")
	}
	// PR-037: 移除 NodeID/NodeAddr 直接赋值，这些值现在从三级配置结构中获取
	// 保留 addr 标志用于覆盖 Network.ListenAddr
	if c.IsSet("addr") {
		cfg.Network.ListenAddr = c.String("addr")
	}
	if c.IsSet("log-level") {
		cfg.Logging.Level = c.String("log-level")
	}

	// 环境变量覆盖（urfave/cli 已自动处理，这里作为备用）
	if envName := os.Getenv("NEXKV_CLUSTER"); envName != "" && !c.IsSet("cluster") {
		cfg.Cluster.Name = envName
	}
	// PR-037: 移除 NodeID/NodeAddr 环境变量处理
	if envNodeAddr := os.Getenv("NEXKV_NODE_ADDR"); envNodeAddr != "" && !c.IsSet("addr") {
		cfg.Network.ListenAddr = envNodeAddr
	}

	return cfg, nil
}

// initLogging 初始化日志
func initLogging(logLevel string) error {
	// TODO: 实现完整的日志初始化
	// 目前使用简单的配置，后续根据 cfg.Logging 配置初始化

	// 设置默认日志级别
	level := "info"
	if logLevel != "" {
		level = logLevel
	}

	_ = level // 占位，后续实现

	return nil
}

// waitForSignal 等待系统信号
func waitForSignal(app *AppContext) {
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
		doneCh <- app.Shutdown()
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
		return "", 0, types.NewDaemonListenAddrEmptyError()
	}

	h, p, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", 0, types.NewDaemonSplitHostPortError(listenAddr, err)
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
		return 0, types.NewDaemonInvalidPortError(portStr)
	}

	return portInt, nil
}

// ========================================
// PR-037: 三级配置结构辅助函数
// ========================================

// selectedNodeInfo 从三级配置结构中获取选定的节点信息
// 返回: nodeID, nodeAddrTCP, error
func selectedNodeInfo(cfg *config.Config, hostID string, nodeIDOverride string) (string, string, error) {
	if cfg == nil {
		return "", "", types.NewDaemonConfigNilError()
	}

	// P0-1 修复：深度复制配置，避免并发读写导致的竞态条件
	// 如果配置在运行时被并发修改（例如配置热更新），直接访问会导致数据竞态
	hosts := make([]config.HostConfig, len(cfg.Cluster.Hosts))
	for i := range cfg.Cluster.Hosts {
		// 复制 Host
		hosts[i] = cfg.Cluster.Hosts[i]
		// 复制 Host 下的 Nodes 切片
		if len(cfg.Cluster.Hosts[i].Nodes) > 0 {
			hosts[i].Nodes = make([]config.NodeConfig, len(cfg.Cluster.Hosts[i].Nodes))
			copy(hosts[i].Nodes, cfg.Cluster.Hosts[i].Nodes)
		}
	}

	// P1-1: 多 Host 配置下应该要求明确指定 host-id
	if len(hosts) > 1 && hostID == "" {
		return "", "", types.NewDaemonMultipleHostsError(len(hosts))
	}

	// 确定使用哪个 Host
	var selectedHost *config.HostConfig
	if hostID != "" {
		for i := range hosts {
			if hosts[i].HostID == hostID {
				selectedHost = &hosts[i]
				break
			}
		}
		if selectedHost == nil {
			return "", "", types.NewDaemonHostIDNotFoundError(hostID)
		}
	} else {
		if len(hosts) == 0 {
			return "", "", types.NewDaemonNoHostsError()
		}
		selectedHost = &hosts[0]
	}

	// 确定使用哪个 Node
	var selectedNode *config.NodeConfig
	if nodeIDOverride != "" {
		for i := range selectedHost.Nodes {
			if selectedHost.Nodes[i].NodeID == nodeIDOverride {
				selectedNode = &selectedHost.Nodes[i]
				break
			}
		}
		if selectedNode == nil {
			return "", "", types.NewDaemonNodeIDNotFoundError(selectedHost.HostID, nodeIDOverride)
		}
	} else {
		if len(selectedHost.Nodes) == 0 {
			return "", "", types.NewDaemonNoNodesError(selectedHost.HostID)
		}
		selectedNode = &selectedHost.Nodes[0]
	}

	return selectedNode.NodeID, selectedNode.NodeAddrTCP, nil
}
