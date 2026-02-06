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
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jzhang405/NexKV/internal/config"
	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/cluster"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/multiformats/go-multiaddr"
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
//
// ⚠️ PR-Libp2p-TransportCleanup 变更：
// - 移除 TCP/UDP Transport（已迁移到 libp2p）
// - 移除 RPC Client/Server（待使用 libp2p Stream 重写）
// - 移除 identity 依赖（使用 libp2p peer.ID）
type AppContext struct {
	// 配置
	ConfigPath string
	Env        string
	HostID     string // PR-037: 主机 ID（用于从三级配置中选择 Host）
	NodeID     string // PR-037: 节点 ID（用于从三级配置中选择 Node）

	// 核心组件
	Coordinator *cluster.TreeCoordinator

	// TODO: 待使用 libp2p Stream 重写 RPC 功能
	// - RPCClient: 使用 libp2p Stream 实现
	// - RPCServer: 使用 libp2p Stream Handler 实现

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
				Name: "host-id",
				// 移除短选项 "h"，避免与 --help 冲突
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
// 初始化流程（PR-Libp2p-TransportCleanup 简化版）
// ========================================

// ⚠️ PR-Libp2p-TransportCleanup 重大变更：
// - 移除 identity 包依赖（已删除）
// - 移除 transport 包依赖（已删除）
// - 移除 RPC Client/Server（待使用 libp2p Stream 重写）
// - 移除 TCP/UDP Transport（已迁移到 libp2p）
// - 简化初始化流程，只保留核心 TreeCoordinator

// msgSeqGenerator 消息序列号生成器（内联实现，替代 identity 包）
type msgSeqGenerator struct {
	counter uint64
}

// newMsgSeqGenerator 创建消息序列号生成器
func newMsgSeqGenerator() *msgSeqGenerator {
	return &msgSeqGenerator{
		counter: uint64(time.Now().UnixMicro()),
	}
}

// next 生成下一个序列号（原子递增）
//
//nolint:unused // TODO: 待 libp2p Stream 重写后使用
func (g *msgSeqGenerator) next() uint64 {
	return atomic.AddUint64(&g.counter, 1)
}

// Initialize 初始化所有组件（简化版）
func (app *AppContext) Initialize(cfg *config.Config) error {
	// 获取节点配置
	configNodeID, nodeListenAddr, err := selectedNodeInfo(cfg, app.HostID, "")
	var daemonNodeID string
	var listenAddr string

	if err != nil {
		// 降级处理，使用全局配置
		logging.Warnf("从配置获取节点信息失败: %v，使用全局网络配置", err)
		listenAddr = cfg.Network.ListenAddr
	} else {
		listenAddr = nodeListenAddr
	}

	// 解析监听地址
	host, port, err := parseListenAddr(listenAddr)
	if err != nil {
		return types.NewDaemonParseListenAddrError(err)
	}

	// 设置节点ID（简化版，使用配置或端口）
	if configNodeID != "" {
		daemonNodeID = configNodeID
	} else {
		// TODO: 使用 libp2p peer.ID 替代
		daemonNodeID = fmt.Sprintf("node-%s", listenAddr)
	}

	// 创建消息序列号生成器（内联实现）
	_ = newMsgSeqGenerator()

	logging.WithFields(map[string]any{
		"host":    host,
		"port":    port,
		"node_id": daemonNodeID,
	}).Info("节点信息初始化成功")

	// =====================================
	// 步骤 1: 创建 TreeCoordinator
	// =====================================
	logging.Info("步骤 1: 创建 TreeCoordinator...")

	coordinatorConfig := cluster.DefaultTreeCoordinatorConfig()
	coordinator, err := cluster.NewTreeCoordinator(
		daemonNodeID,
		listenAddr,
		coordinatorConfig,
		&cfg.Cluster,
	)
	if err != nil {
		return types.NewDaemonCreateTreeCoordinatorError(err)
	}

	app.Coordinator = coordinator

	// =====================================
	// 步骤 2: 启动 TreeCoordinator
	// =====================================
	logging.Info("步骤 2: 启动 TreeCoordinator...")

	if err := coordinator.Start(); err != nil {
		return types.NewDaemonStartTreeCoordinatorError(err)
	}

	logging.WithFields(map[string]any{
		"node_id": daemonNodeID,
	}).Info("TreeCoordinator 启动成功")

	// TODO: 待使用 libp2p Stream 重写 RPC 功能
	// - RPC Client: 使用 libp2p Stream 实现
	// - RPC Server: 使用 libp2p Stream Handler 实现
	// - CLI 命令: 需要适配新的 RPC 接口

	return nil
}

// ========================================
// Shutdown 优雅关闭（简化版）
// ========================================

// Shutdown 停止守护进程（PR-Libp2p-TransportCleanup 简化版）
func (app *AppContext) Shutdown() error {
	logging.Info("正在停止守护进程...")

	var errs []error

	// 停止 TreeCoordinator
	if app.Coordinator != nil {
		if err := app.Coordinator.Stop(); err != nil {
			errs = append(errs, types.NewDaemonStopTreeCoordinatorError(err))
		}
	}

	// 取消上下文
	app.cancel()

	if len(errs) > 0 {
		return types.NewDaemonShutdownMultipleErrorsError(len(errs))
	}

	logging.Info("守护进程已停止")
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

	// P1-1 修复：配置并发访问保护
	// 配置在启动后只读，不支持运行时热更新
	// 深度复制配置以避免并发读写导致的竞态条件
	// （如果后续需要支持热更新，应使用 sync.RWMutex 保护）
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

	// PR-038 Fix: 转换 multiaddr 格式为 Transport 层需要的格式
	// 例如：/ip4/127.0.0.1/tcp/9213 → 127.0.0.1:9213
	listenAddr, err := convertMultiaddrToListenAddr(selectedNode.NodeAddrTCP)
	if err != nil {
		return "", "", fmt.Errorf("转换节点地址失败: %w", err)
	}

	return selectedNode.NodeID, listenAddr, nil
}

// convertMultiaddrToListenAddr 将 multiaddr 格式转换为 Transport 层需要的格式
// 例如：/ip4/127.0.0.1/tcp/9213 → 127.0.0.1:9213
//
//	/ip4/0.0.0.0/tcp/9213   → 0.0.0.0:9213
func convertMultiaddrToListenAddr(multiaddrStr string) (string, error) {
	// 如果不是 multiaddr 格式（不以 / 开头），直接返回
	if !strings.HasPrefix(multiaddrStr, "/") {
		return multiaddrStr, nil
	}

	// 解析 multiaddr
	maddr, err := multiaddr.NewMultiaddr(multiaddrStr)
	if err != nil {
		return "", fmt.Errorf("无效的 multiaddr 格式: %w", err)
	}

	// 提取 IP 和端口
	var host string
	var portStr string

	// 遍历 multiaddr 的所有组件
	for _, protocol := range maddr.Protocols() {
		// ValueForProtocol 需要传入 protocol.Code
		value, err := maddr.ValueForProtocol(protocol.Code)
		if err != nil {
			continue
		}

		switch protocol.Name {
		case "ip4", "ip6", "dns4", "dns6":
			host = value
		case "tcp":
			portStr = value
		}
	}

	if host == "" || portStr == "" {
		return "", fmt.Errorf("multiaddr 中缺少 IP 或端口组件: %s", multiaddrStr)
	}

	return fmt.Sprintf("%s:%s", host, portStr), nil
}
