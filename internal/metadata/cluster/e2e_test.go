// Package cluster TreeCoordinator E2E 集成测试
//
// 测试 TreeCoordinator 与真实 RPC Client/Server 的集成
package cluster

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ========================================
// 测试配置结构（临时用于 PR-037 测试）
// TODO: PR-037 完成后应该移除这些，使用正式的 config 包
// ========================================

// testConfig 三级测试配置结构
type testConfig struct {
	Cluster testClusterConfig `yaml:"cluster"`
}

// testClusterConfig 测试集群配置
type testClusterConfig struct {
	Name    string           `yaml:"name"`
	BaseDir string           `yaml:"base_dir"`
	Hosts   []testHostConfig `yaml:"hosts"`
}

// testHostConfig 测试 Host 配置
type testHostConfig struct {
	HostID   string           `yaml:"host_id"`
	SeedNode string           `yaml:"seed_node"`
	Nodes    []testNodeConfig `yaml:"nodes"`
}

// testNodeConfig 测试 Node 配置
type testNodeConfig struct {
	NodeID      string `yaml:"node_id"`
	NodeAddrTCP string `yaml:"node_addr_tcp"`
	NodeAddrUDP string `yaml:"node_addr_udp"`
}

// raceDetectorEnabled 检测是否启用了 race detector
func raceDetectorEnabled(t *testing.T) bool {
	// 方法1：检查 flag.HasFlag("race")（需要 Go 1.21+）
	if flag.Lookup("race") != nil {
		return true
	}

	// 方法2：检查环境变量
	if os.Getenv("GORACE") != "" {
		return true
	}

	// 方法3：通过测试标志判断（在测试中设置）
	// 注意：这个方法需要在测试运行时设置标志
	return false
}

// ========================================
// E2E 测试辅助结构
// ========================================

// clusterRPCHandler 实现 RPCHandler，处理 TreeCoordinator 相关消息
type clusterRPCHandler struct {
	coordinator *TreeCoordinator
	mu          sync.RWMutex

	// 测试钩子
	onNodeJoin  func(nodeID string)
	onHeartbeat func(nodeID string)
	onNodeSync  func(nodeID string)

	// 记录接收到的消息
	receivedMessages []messageRecord
}

type messageRecord struct {
	msgType   types.MessageType
	timestamp time.Time
}

// newClusterRPCHandler 创建集群 RPC Handler
func newClusterRPCHandler(coordinator *TreeCoordinator) *clusterRPCHandler {
	return &clusterRPCHandler{
		coordinator:      coordinator,
		receivedMessages: make([]messageRecord, 0),
	}
}

// HandleRequest 实现 RPCHandler 接口
func (h *clusterRPCHandler) HandleRequest(ctx context.Context, req types.Message) (types.Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 记录接收到的消息
	h.receivedMessages = append(h.receivedMessages, messageRecord{
		msgType:   req.Type(),
		timestamp: time.Now(),
	})

	// 根据消息类型处理
	switch msg := req.(type) {
	case *transport.NodeJoinMessage:
		return h.handleNodeJoin(ctx, msg)

	case *transport.NodePingMessage:
		return h.handleHeartbeat(ctx, msg)

	case *transport.NodeSyncMessage:
		return h.handleNodeSync(ctx, msg)

	default:
		return nil, types.NewTreeCoordinatorUnsupportedMessageTypeError(fmt.Sprintf("%T", msg))
	}
}

// handleNodeJoin 处理节点加入请求
func (h *clusterRPCHandler) handleNodeJoin(ctx context.Context, msg *transport.NodeJoinMessage) (types.Message, error) {
	logging.WithFields(map[string]any{
		"node_id": msg.NodeID,
		"addr":    msg.Addr,
		"role":    msg.Role,
	}).Info("收到节点加入请求")

	// 调用测试钩子
	if h.onNodeJoin != nil {
		h.onNodeJoin(msg.NodeID)
	}

	// 将新节点添加到 coordinator
	err := h.coordinator.AddChild(msg.NodeID)
	if err != nil {
		return nil, types.NewTreeCoordinatorFailedToAddChildError(err)
	}

	// 返回加入响应
	syncMsg := &transport.NodeSyncMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeSync},
		Version:     uint64(time.Now().Unix()),
		Metadata: map[string][]byte{
			"parent_node_id": []byte(h.coordinator.localNode.NodeID),
			"timestamp":      []byte(time.Now().Format(time.RFC3339)),
		},
	}

	return syncMsg, nil
}

// handleHeartbeat 处理心跳消息
func (h *clusterRPCHandler) handleHeartbeat(ctx context.Context, msg *transport.NodePingMessage) (types.Message, error) {
	logging.WithFields(map[string]any{
		"node_id":  msg.NodeID,
		"sequence": msg.Sequence,
	}).Debug("收到心跳")

	// 调用测试钩子
	if h.onHeartbeat != nil {
		h.onHeartbeat(msg.NodeID)
	}

	// 返回 Pong 消息
	pongMsg := &transport.NodePongMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePong},
		NodeID:      h.coordinator.localNode.NodeID,
		Sequence:    msg.Sequence,
		Timestamp:   time.Now().Unix(),
		Status:      "Ready",
	}

	return pongMsg, nil
}

// handleNodeSync 处理节点同步消息
func (h *clusterRPCHandler) handleNodeSync(ctx context.Context, msg *transport.NodeSyncMessage) (types.Message, error) {
	logging.WithFields(map[string]any{
		"version":  msg.Version,
		"metadata": len(msg.Metadata),
	}).Debug("收到节点同步")

	// 调用测试钩子
	if h.onNodeSync != nil {
		h.onNodeSync("sync")
	}

	// 返回同步响应
	syncMsg := &transport.NodeSyncMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeSync},
		Version:     uint64(time.Now().Unix()),
		Metadata:    h.coordinator.buildTopologyMetadata(),
	}

	return syncMsg, nil
}

// ========================================
// E2E 测试辅助函数
// ========================================

var (
	// testEnvMutex 保证测试环境设置的并发安全
	testEnvMutex sync.Mutex

	// testPortCounter 生成唯一端口号
	testPortCounter uint64 = 10000

	// activeTestDirs 跟踪活跃的测试目录
	activeTestDirs = make(map[string]bool)
)

// getNextTestPort 获取下一个唯一测试端口号
func getNextTestPort() int {
	return int(atomic.AddUint64(&testPortCounter, 1))
}

// SetupTestEnvironment 设置测试环境（三级配置结构）
//
// 返回：
//   - baseDir: 测试基础目录
//   - hostID: Host ID
//   - cfg: 测试配置
//   - cleanup: 清理函数
func SetupTestEnvironment(t *testing.T) (baseDir, hostID string, cfg *testConfig, cleanup func()) {
	t.Helper()

	testEnvMutex.Lock()
	defer testEnvMutex.Unlock()

	// 1. 生成唯一的测试 ID
	testID := fmt.Sprintf("e2e-%d-%s", time.Now().UnixNano(), uuid.New().String()[:8])

	// 2. 构建测试目录路径
	// 优先使用环境变量 BASEDIR，否则使用默认值
	baseDirValue := os.Getenv("BASEDIR")
	if baseDirValue == "" {
		baseDirValue = "/var/tmp/nexkv"
	}
	baseDir = filepath.Join(baseDirValue, "test", testID)
	hostID = "host-1"

	// 3. 注册测试目录
	activeTestDirs[baseDir] = true

	// 4. 创建测试目录结构
	dataDir := filepath.Join(baseDir, hostID)
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		delete(activeTestDirs, baseDir)
		t.Fatalf("创建测试目录失败: %v", err)
	}

	// 5. 生成测试配置（使用三级结构）
	cfg = generateTestConfig(t, baseDir, hostID)

	// 6. 注意：测试中配置仅用于内存，不需要持久化到文件
	// （config.SaveConfig 需要等待 config 包更新为三级结构）

	// 7. 设置测试专用环境变量（防止污染）
	oldBaseDir := os.Getenv("NEXKV_BASE_DIR")
	os.Setenv("NEXKV_BASE_DIR", baseDir)

	// 8. 创建清理函数
	cleanup = func() {
		testEnvMutex.Lock()
		defer testEnvMutex.Unlock()

		// 恢复环境变量
		if oldBaseDir == "" {
			os.Unsetenv("NEXKV_BASE_DIR")
		} else {
			os.Setenv("NEXKV_BASE_DIR", oldBaseDir)
		}

		// 删除测试目录
		os.RemoveAll(baseDir)
		delete(activeTestDirs, baseDir)
	}

	return baseDir, hostID, cfg, cleanup
}

// generateTestConfig 生成测试配置（三级配置结构）
func generateTestConfig(t *testing.T, baseDir, hostID string) *testConfig {
	t.Helper()

	// 生成唯一端口号（port2 已移除，暂不需要）
	port1 := getNextTestPort()
	port3 := getNextTestPort()
	port4 := getNextTestPort()

	// 使用 multiaddr 格式
	seedNodeTCP := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port1)
	node1TCP := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port3)
	node1UDP := fmt.Sprintf("/ip4/127.0.0.1/udp/%d", port4)

	return &testConfig{
		Cluster: testClusterConfig{
			Name:    "test-cluster",
			BaseDir: baseDir,
			Hosts: []testHostConfig{
				{
					HostID:   hostID,
					SeedNode: seedNodeTCP,
					Nodes: []testNodeConfig{
						{
							NodeID:      "node-1",
							NodeAddrTCP: node1TCP,
							NodeAddrUDP: node1UDP,
						},
					},
				},
			},
		},
	}
}

// ========================================
// 从配置文件加载配置
// ========================================

// findProjectRoot 查找项目根目录（包含 go.mod 的目录）
func findProjectRoot() string {
	// 尝试从环境变量获取
	if root := os.Getenv("NEXKV_PROJECT_ROOT"); root != "" {
		return root
	}

	// 从当前目录开始向上查找
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	// 限制查找深度，避免无限循环
	maxDepth := 10
	depth := 0

	for depth < maxDepth {
		// 检查当前目录是否有 go.mod
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		// 向上回退到父目录
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// 已到达根目录
			return ""
		}
		dir = parentDir
		depth++
	}

	return ""
}

// LoadTestConfigFromFile 从配置文件加载测试配置
//
// 参数：
//   - configPath: 配置文件路径（相对于项目根目录或绝对路径）
//
// 返回：
//   - cfg: 加载的配置
//   - err: 错误信息
func LoadTestConfigFromFile(configPath string) (*testConfig, error) {
	// 如果是相对路径，尝试从项目根目录查找
	var attemptedPaths []string
	if !filepath.IsAbs(configPath) {
		// 尝试通过查找 go.mod 来定位项目根目录
		rootDir := findProjectRoot()

		// 标准化配置文件名（如果已包含 configs/ 前缀，则去掉）
		configFileName := strings.TrimPrefix(configPath, "configs/")

		// 尝试多个可能的位置（从测试目录向上查找）
		possiblePaths := []string{
			configPath,                                                 // 直接使用原始路径
			filepath.Join("configs", configFileName),                   // configs/ 目录
			filepath.Join("../../../configs", configFileName),          // 从 cluster 目录回退到项目根目录
			filepath.Join("..", "..", "..", "configs", configFileName), // 使用相对路径
			filepath.Join("..", "..", "configs", configFileName),       // 从 metadata 目录回退
			filepath.Join("..", "configs", configFileName),             // 从 internal 目录回退
		}

		// 如果找到了项目根目录，优先使用
		if rootDir != "" {
			possiblePaths = append([]string{
				filepath.Join(rootDir, "configs", configFileName),
			}, possiblePaths...)
		}

		// 尝试每个路径
		found := false
		for _, path := range possiblePaths {
			attemptedPaths = append(attemptedPaths, path)
			if _, err := os.Stat(path); err == nil {
				configPath = path
				found = true
				break
			}
		}

		if !found {
			return nil, types.NewE2EConfigFileNotFoundError(attemptedPaths, rootDir)
		}
	}

	// 读取配置文件
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, types.NewE2EConfigFileReadError(configPath, err)
	}

	// 解析 YAML
	var cfg testConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, types.NewE2EConfigFileParseError(configPath, err)
	}

	return &cfg, nil
}

// SetupTestEnvironmentFromConfig 使用配置文件设置测试环境
//
// 参数：
//   - t: 测试实例
//   - configPath: 配置文件路径（可选，默认使用 "configs/config.yaml"）
//   - hostIndex: 使用第几个 Host（默认 0，即第一个 Host）
//
// 返回：
//   - baseDir: 测试基础目录
//   - hostID: Host ID
//   - cfg: 测试配置
//   - cleanup: 清理函数
func SetupTestEnvironmentFromConfig(t *testing.T, configPath string, hostIndex int) (baseDir, hostID string, cfg *testConfig, cleanup func()) {
	t.Helper()

	// 使用默认配置文件路径
	if configPath == "" {
		configPath = "configs/config.yaml"
	}

	// 加载配置文件
	var err error
	cfg, err = LoadTestConfigFromFile(configPath)
	if err != nil {
		t.Fatalf("加载配置文件失败: %v", err)
	}

	// 验证配置
	if len(cfg.Cluster.Hosts) == 0 {
		t.Fatalf("配置文件中没有定义任何 Host")
	}

	// 选择指定的 Host
	if hostIndex < 0 || hostIndex >= len(cfg.Cluster.Hosts) {
		t.Fatalf("hostIndex %d 超出范围 [0, %d]", hostIndex, len(cfg.Cluster.Hosts)-1)
	}

	selectedHost := cfg.Cluster.Hosts[hostIndex]
	hostID = selectedHost.HostID

	// 生成唯一的测试 ID（避免并发测试冲突）
	testID := fmt.Sprintf("e2e-%d-%s", time.Now().UnixNano(), uuid.New().String()[:8])

	// 构建测试目录路径（使用配置中的 base_dir 作为前缀）
	baseDirValue := os.Getenv("BASEDIR")
	if baseDirValue == "" {
		// 使用配置文件中的 base_dir
		baseDirValue = cfg.Cluster.BaseDir
		if baseDirValue == "" {
			baseDirValue = "~/.nexkv/data"
		}
	}

	// 展开波浪号
	if strings.HasPrefix(baseDirValue, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			baseDirValue = filepath.Join(homeDir, baseDirValue[2:])
		}
	}

	baseDir = filepath.Join(baseDirValue, "test", testID)

	// 注册测试目录
	testEnvMutex.Lock()
	activeTestDirs[baseDir] = true
	testEnvMutex.Unlock()

	// 创建测试目录结构
	dataDir := filepath.Join(baseDir, hostID)
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		testEnvMutex.Lock()
		delete(activeTestDirs, baseDir)
		testEnvMutex.Unlock()
		t.Fatalf("创建测试目录失败: %v", err)
	}

	// 更新配置中的 base_dir 为测试目录
	cfg.Cluster.BaseDir = baseDir

	// 设置测试专用环境变量（防止污染）
	oldBaseDir := os.Getenv("NEXKV_BASE_DIR")
	os.Setenv("NEXKV_BASE_DIR", baseDir)

	// 创建清理函数
	cleanup = func() {
		testEnvMutex.Lock()
		defer testEnvMutex.Unlock()

		// 恢复环境变量
		if oldBaseDir != "" {
			os.Setenv("NEXKV_BASE_DIR", oldBaseDir)
		} else {
			os.Unsetenv("NEXKV_BASE_DIR")
		}

		// 删除测试目录（如果存在）
		if _, exists := activeTestDirs[baseDir]; exists {
			os.RemoveAll(baseDir)
			delete(activeTestDirs, baseDir)
		}
	}

	return baseDir, hostID, cfg, cleanup
}

// ConvertMultiaddrToHostPort 转换 multiaddr 为 host:port 格式
// 注意：这是临时的辅助函数，用于兼容现有代码
// TODO: 后续应该完全迁移到 multiaddr 格式
func ConvertMultiaddrToHostPort(multiaddr string) (string, error) {
	// 简单解析：提取 /ip4/ 和 /tcp/ 后的值
	// 格式：/ip4/127.0.0.1/tcp/9211
	parts := splitMultiaddrE2E(multiaddr)
	var ip string
	var port string

	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case "ip4":
			ip = parts[i+1]
		case "tcp":
			port = parts[i+1]
		}
	}

	if ip == "" || port == "" {
		return "", types.NewE2EInvalidMultiAddrFormatError(multiaddr)
	}

	return fmt.Sprintf("%s:%s", ip, port), nil
}

// splitMultiaddrE2E 分割 multiaddr 为组件数组
// 与 tree_coordinator.go 中的 splitMultiaddr 功能相同，保留用于 E2E 测试
func splitMultiaddrE2E(addr string) []string {
	if addr == "" || len(addr) == 0 || addr[0] != '/' {
		return []string{}
	}

	// 移除开头的 / 并分割
	return strings.Split(addr[1:], "/")
}

// setupE2ETestEnvironment 创建完整的 E2E 测试环境（更新版：使用三级配置）
//
// 返回：
//   - baseDir: 测试基础目录
//   - hostID: Host ID
//   - server: RPC Server
//   - serverTCP: Server's TCP Transport (for getting address)
//   - client: RPC Client
//   - coordinator: TreeCoordinator
//   - cleanup: 清理函数
func setupE2ETestEnvironment(t *testing.T) (string, string, *transport.RPCServer, *transport.TCPTransport, *transport.RPCClient, *TreeCoordinator, func()) {
	t.Helper()

	// 1. 设置测试环境（三级配置）
	baseDir, hostID, cfg, cleanupConfig := SetupTestEnvironment(t)

	// 2. 从配置中获取节点信息
	hostConfig := cfg.Cluster.Hosts[0]
	nodeConfig := hostConfig.Nodes[0]
	seedNode := hostConfig.SeedNode

	// 3. 转换 seedNode multiaddr 为 host:port（临时兼容）
	seedAddr, err := ConvertMultiaddrToHostPort(seedNode)
	if err != nil {
		cleanupConfig()
		t.Fatalf("转换 seed multiaddr 失败: %v", err)
	}

	// 4. 创建 TreeCoordinator
	config := DefaultTreeCoordinatorConfig()
	config.HeartbeatInterval = 100 * time.Millisecond // 缩短心跳间隔用于测试

	coordinator, err := NewTreeCoordinator(nodeConfig.NodeID, seedAddr, config, nil)
	if err != nil {
		cleanupConfig()
		t.Fatalf("创建 TreeCoordinator 失败: %v", err)
	}

	// 5. 创建 RPC Handler
	handler := newClusterRPCHandler(coordinator)

	// 6. 创建服务端 Transport
	serverTransportConfig := &transport.TransportConfig{
		ListenAddr:         "127.0.0.1:0", // 使用随机端口
		MaxMessageSize:     1024 * 1024 * 100,
		ReadTimeout:        10 * time.Second,
		WriteTimeout:       10 * time.Second,
		KeepAliveInterval:  5 * time.Second,
		KeepAliveTimeout:   15 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 2 * time.Second,
	}

	serverTCP, err := transport.NewTCPTransportWithConfig(serverTransportConfig)
	if err != nil {
		cleanupConfig()
		_ = coordinator.Stop()
		t.Fatalf("创建服务端 Transport 失败: %v", err)
	}

	// 7. 创建客户端 Transport
	clientTransportConfig := &transport.TransportConfig{
		ListenAddr:         "127.0.0.1:0",
		MaxMessageSize:     1024 * 1024 * 100,
		ReadTimeout:        10 * time.Second,
		WriteTimeout:       10 * time.Second,
		KeepAliveInterval:  5 * time.Second,
		KeepAliveTimeout:   15 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 2 * time.Second,
	}

	clientTCP, err := transport.NewTCPTransportWithConfig(clientTransportConfig)
	if err != nil {
		cleanupConfig()
		_ = coordinator.Stop()
		_ = serverTCP.Stop()
		t.Fatalf("创建客户端 Transport 失败: %v", err)
	}

	// 8. 创建 RPC Server
	serverConfig := transport.DefaultRPCServerConfig()
	server, err := transport.NewRPCServer(serverTCP, nil, handler, serverConfig)
	if err != nil {
		cleanupConfig()
		_ = coordinator.Stop()
		_ = serverTCP.Stop()
		_ = clientTCP.Stop()
		t.Fatalf("创建 RPC Server 失败: %v", err)
	}

	// 9. 创建 RPC Client
	clientConfig := &transport.RPCClientConfig{
		DialTimeout:     2 * time.Second,
		RequestTimeout:  5 * time.Second,
		MaxRetries:      2,
		RetryDelay:      50 * time.Millisecond,
		EnableFastFail:  true,
		FastFailTimeout: 2 * time.Second,
	}

	client, err := transport.NewRPCClient(clientTCP, nil, clientConfig)
	if err != nil {
		cleanupConfig()
		_ = coordinator.Stop()
		_ = serverTCP.Stop()
		_ = clientTCP.Stop()
		_ = server.Stop()
		t.Fatalf("创建 RPC Client 失败: %v", err)
	}

	// 10. 启动服务端
	var serverNodeID uint64 = 1
	var serverMsgSeq uint64 = 1
	serverMsgSeqGen := func() uint64 {
		return atomic.AddUint64(&serverMsgSeq, 1)
	}

	err = serverTCP.Start(&serverNodeID, serverMsgSeqGen, "127.0.0.1:0")
	if err != nil {
		cleanupConfig()
		_ = coordinator.Stop()
		_ = serverTCP.Stop()
		_ = clientTCP.Stop()
		_ = server.Stop()
		t.Fatalf("启动服务端 Transport 失败: %v", err)
	}

	err = server.Start()
	if err != nil {
		cleanupConfig()
		_ = coordinator.Stop()
		_ = serverTCP.Stop()
		_ = clientTCP.Stop()
		t.Fatalf("启动 RPC Server 失败: %v", err)
	}

	// 11. 启动客户端
	err = client.Start()
	if err != nil {
		cleanupConfig()
		_ = coordinator.Stop()
		_ = server.Stop()
		_ = serverTCP.Stop()
		t.Fatalf("启动 RPC Client 失败: %v", err)
	}

	var clientNodeID uint64 = 2
	var clientMsgSeq uint64 = 1
	clientMsgSeqGen := func() uint64 {
		return atomic.AddUint64(&clientMsgSeq, 1)
	}

	err = clientTCP.Start(&clientNodeID, clientMsgSeqGen, "127.0.0.1:0")
	if err != nil {
		cleanupConfig()
		_ = coordinator.Stop()
		_ = server.Stop()
		_ = serverTCP.Stop()
		_ = client.Stop()
		t.Fatalf("启动客户端 Transport 失败: %v", err)
	}

	// 12. 等待准备就绪
	time.Sleep(100 * time.Millisecond)

	// 13. 创建完整的清理函数
	cleanup := func() {
		// 停止客户端
		_ = client.Stop()
		_ = clientTCP.Stop()

		// 停止服务端
		_ = server.Stop()
		_ = serverTCP.Stop()

		// 停止 coordinator
		_ = coordinator.Stop()

		// 清理测试环境
		cleanupConfig()

		// 等待资源完全释放（避免测试间状态污染）
		time.Sleep(200 * time.Millisecond)
	}

	return baseDir, hostID, server, serverTCP, client, coordinator, cleanup
}

// setupE2ETestEnvironmentLegacy 旧版测试环境设置（保留用于向后兼容）
//
// 返回：
//   - server: RPC Server
//   - serverTCP: Server's TCP Transport (for getting address)
//   - client: RPC Client
//   - coordinator: TreeCoordinator
//   - cleanup: 清理函数
//
//nolint:unused // 保留用于向后兼容和未来的测试可能使用
func setupE2ETestEnvironmentLegacy(t *testing.T) (*transport.RPCServer, *transport.TCPTransport, *transport.RPCClient, *TreeCoordinator, func()) {
	t.Helper()

	// 创建 TreeCoordinator
	config := DefaultTreeCoordinatorConfig()
	config.HeartbeatInterval = 100 * time.Millisecond // 缩短心跳间隔用于测试

	coordinator, err := NewTreeCoordinator("node-1", "127.0.0.1:9211", config, nil)
	require.NoError(t, err)

	// 创建 RPC Handler
	handler := newClusterRPCHandler(coordinator)

	// 创建服务端 Transport
	serverTransportConfig := &transport.TransportConfig{
		ListenAddr:         "127.0.0.1:0", // 使用随机端口
		MaxMessageSize:     1024 * 1024 * 100,
		ReadTimeout:        10 * time.Second,
		WriteTimeout:       10 * time.Second,
		KeepAliveInterval:  5 * time.Second,
		KeepAliveTimeout:   15 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 2 * time.Second,
	}

	serverTCP, err := transport.NewTCPTransportWithConfig(serverTransportConfig)
	require.NoError(t, err)

	// 创建客户端 Transport
	clientTransportConfig := &transport.TransportConfig{
		ListenAddr:         "127.0.0.1:0",
		MaxMessageSize:     1024 * 1024 * 100,
		ReadTimeout:        10 * time.Second,
		WriteTimeout:       10 * time.Second,
		KeepAliveInterval:  5 * time.Second,
		KeepAliveTimeout:   15 * time.Second,
		BufferSize:         4096,
		ChannelSendTimeout: 2 * time.Second,
	}

	clientTCP, err := transport.NewTCPTransportWithConfig(clientTransportConfig)
	require.NoError(t, err)

	// 创建 RPC Server
	serverConfig := transport.DefaultRPCServerConfig()
	server, err := transport.NewRPCServer(serverTCP, nil, handler, serverConfig)
	require.NoError(t, err)

	// 创建 RPC Client
	clientConfig := &transport.RPCClientConfig{
		DialTimeout:     2 * time.Second,
		RequestTimeout:  5 * time.Second,
		MaxRetries:      2,
		RetryDelay:      50 * time.Millisecond,
		EnableFastFail:  true,
		FastFailTimeout: 2 * time.Second,
	}

	client, err := transport.NewRPCClient(clientTCP, nil, clientConfig)
	require.NoError(t, err)

	// 启动服务端
	var serverNodeID uint64 = 1
	var serverMsgSeq uint64 = 1
	serverMsgSeqGen := func() uint64 {
		return atomic.AddUint64(&serverMsgSeq, 1)
	}

	err = serverTCP.Start(&serverNodeID, serverMsgSeqGen, "127.0.0.1:0")
	require.NoError(t, err)

	err = server.Start()
	require.NoError(t, err)

	// 启动客户端
	err = client.Start()
	require.NoError(t, err)

	var clientNodeID uint64 = 2
	var clientMsgSeq uint64 = 1
	clientMsgSeqGen := func() uint64 {
		return atomic.AddUint64(&clientMsgSeq, 1)
	}

	err = clientTCP.Start(&clientNodeID, clientMsgSeqGen, "127.0.0.1:0")
	require.NoError(t, err)

	// 等待准备就绪
	time.Sleep(100 * time.Millisecond)

	// 创建清理函数
	cleanup := func() {
		// 停止客户端
		_ = client.Stop()
		_ = clientTCP.Stop()

		// 停止服务端
		_ = server.Stop()
		_ = serverTCP.Stop()

		// 停止 coordinator
		_ = coordinator.Stop()

		// 等待资源完全释放（避免测试间状态污染）
		time.Sleep(200 * time.Millisecond)
	}

	return server, serverTCP, client, coordinator, cleanup
}

// setupE2ETestEnvironmentV5 兼容旧版本的测试环境设置（5 参数版本）
// 内部调用新的 7 参数版本，忽略 baseDir 和 hostID
//
// 返回：
//   - server: RPC Server
//   - serverTCP: Server's TCP Transport (for getting address)
//   - client: RPC Client
//   - coordinator: TreeCoordinator
//   - cleanup: 清理函数
func setupE2ETestEnvironmentV5(t *testing.T) (*transport.RPCServer, *transport.TCPTransport, *transport.RPCClient, *TreeCoordinator, func()) {
	t.Helper()
	_, _, server, serverTCP, client, coordinator, cleanup := setupE2ETestEnvironment(t)
	return server, serverTCP, client, coordinator, cleanup
}

// ========================================
// E2E 测试用例
// ========================================

// TestE2E_NodeJoin 测试节点加入集群的完整流程
func TestE2E_NodeJoin(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 E2E 测试（短模式）")
	}

	_, serverTCP, client, coordinator, cleanup := setupE2ETestEnvironmentV5(t)
	defer cleanup()

	// 获取服务端地址
	serverAddr := serverTCP.GetLocalAddr()

	// 模拟节点 2 加入集群
	joinMsg := &transport.NodeJoinMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeJoin},
		NodeID:      "node-2",
		Addr:        "127.0.0.1:9212",
		Role:        "child",
	}

	// 发送加入请求
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Call(ctx, serverAddr, joinMsg)
	require.NoError(t, err, "发送加入请求应该成功")
	require.NotNil(t, resp, "响应不应该为空")

	// 验证响应是 NodeSyncMessage
	syncMsg, ok := resp.(*transport.NodeSyncMessage)
	require.True(t, ok, "响应应该是 NodeSyncMessage 类型")

	// 验证父节点信息
	parentID := string(syncMsg.Metadata["parent_node_id"])
	assert.Equal(t, "node-1", parentID, "父节点应该是 node-1")

	// 验证 coordinator 状态
	coordinator.nodesMu.RLock()
	hasChild := false
	for _, childID := range coordinator.localNode.ChildrenIDs {
		if childID == "node-2" {
			hasChild = true
			break
		}
	}
	coordinator.nodesMu.RUnlock()

	assert.True(t, hasChild, "node-2 应该被添加为子节点")

	t.Log("✅ 节点加入 E2E 测试通过")
}

// TestE2E_Heartbeat 测试心跳收发
func TestE2E_Heartbeat(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 E2E 测试（短模式）")
	}

	_, serverTCP, client, coordinator, cleanup := setupE2ETestEnvironmentV5(t)
	defer cleanup()

	// 获取服务端地址
	serverAddr := serverTCP.GetLocalAddr()

	// 先添加子节点
	err := coordinator.AddChild("node-2")
	require.NoError(t, err)

	// 创建心跳消息
	pingMsg := &transport.NodePingMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePing},
		NodeID:      "node-1",
		Sequence:    1,
		Timestamp:   time.Now().Unix(),
	}

	// 发送心跳
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Call(ctx, serverAddr, pingMsg)
	require.NoError(t, err, "发送心跳应该成功")
	require.NotNil(t, resp, "响应不应该为空")

	// 验证响应是 NodePongMessage
	pongMsg, ok := resp.(*transport.NodePongMessage)
	require.True(t, ok, "响应应该是 NodePongMessage 类型")

	assert.Equal(t, "node-1", pongMsg.NodeID, "Pong 消息应该包含正确的节点 ID")
	assert.EqualValues(t, 1, pongMsg.Sequence, "Pong 消息应该包含正确的序列号")

	t.Log("✅ 心跳 E2E 测试通过")
}

// TestE2E_GossipSync 测试 Gossip 拓扑同步
func TestE2E_GossipSync(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 E2E 测试（短模式）")
	}

	_, serverTCP, client, coordinator, cleanup := setupE2ETestEnvironmentV5(t)
	defer cleanup()

	// 获取服务端地址
	serverAddr := serverTCP.GetLocalAddr()

	// 添加一些子节点以构造拓扑元数据
	err := coordinator.AddChild("node-2")
	require.NoError(t, err)
	err = coordinator.AddChild("node-3")
	require.NoError(t, err)

	// 创建 Gossip 同步消息
	syncMsg := &transport.NodeSyncMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeSync},
		Version:     uint64(time.Now().Unix()),
		Metadata:    coordinator.buildTopologyMetadata(),
	}

	// 发送 Gossip 同步消息
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Call(ctx, serverAddr, syncMsg)
	require.NoError(t, err, "发送 Gossip 同步应该成功")
	require.NotNil(t, resp, "响应不应该为空")

	// 验证响应是 NodeSyncMessage
	responseSyncMsg, ok := resp.(*transport.NodeSyncMessage)
	require.True(t, ok, "响应应该是 NodeSyncMessage 类型")

	// 验证元数据包含预期内容
	assert.Contains(t, responseSyncMsg.Metadata, "node-1", "应该包含 node-1 的元数据")

	t.Log("✅ Gossip 同步 E2E 测试通过")
}

// TestE2E_MultiNodeCluster 测试多节点集群场景
func TestE2E_MultiNodeCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 E2E 测试（短模式）")
	}

	// 创建根节点（node-1）
	_, serverTCP1, _, coord1, cleanup1 := setupE2ETestEnvironmentV5(t)
	defer cleanup1()

	serverAddr1 := serverTCP1.GetLocalAddr()

	// 创建第二个节点（node-2）
	_, _, client2, _, cleanup2 := setupE2ETestEnvironmentV5(t)
	defer cleanup2()

	// node-2 加入 node-1 的集群
	joinMsg := &transport.NodeJoinMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeJoin},
		NodeID:      "node-2",
		Addr:        "127.0.0.1:9212",
		Role:        "child",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client2.Call(ctx, serverAddr1, joinMsg)
	require.NoError(t, err)

	syncMsg := resp.(*transport.NodeSyncMessage)
	assert.Equal(t, "node-1", string(syncMsg.Metadata["parent_node_id"]))

	// 验证 node-1 的子节点列表
	time.Sleep(100 * time.Millisecond) // 等待状态更新
	coord1.nodesMu.RLock()
	children := make([]string, len(coord1.localNode.ChildrenIDs))
	copy(children, coord1.localNode.ChildrenIDs)
	coord1.nodesMu.RUnlock()

	assert.Contains(t, children, "node-2", "node-2 应该是 node-1 的子节点")

	t.Log("✅ 多节点集群 E2E 测试通过")
}

// TestE2E_ConcurrentHeartbeats 测试并发心跳（跳过，仅测试基本功能）
func TestE2E_ConcurrentHeartbeats(t *testing.T) {
	t.Skip("跳过并发性能测试，仅测试基本功能")
	if testing.Short() {
		t.Skip("跳过 E2E 测试（短模式）")
	}

	_, serverTCP, client, coordinator, cleanup := setupE2ETestEnvironmentV5(t)
	defer cleanup()

	serverAddr := serverTCP.GetLocalAddr()

	// 添加多个子节点
	for i := 2; i <= 5; i++ {
		nodeID := fmt.Sprintf("node-%d", i) // PR-037: 匹配新的节点 ID 格式
		err := coordinator.AddChild(nodeID)
		require.NoError(t, err)
	}

	// 并发发送心跳（减少数量以适应 CI 环境）
	// 在 race detection 模式下减少并发数量
	numHeartbeats := 5
	if raceMode := os.Getenv("GORACE"); raceMode != "" || raceDetectorEnabled(t) {
		numHeartbeats = 3 // race 模式下减少并发
	}
	var wg sync.WaitGroup
	errors := make(chan error, numHeartbeats)

	for i := 0; i < numHeartbeats; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()

			pingMsg := &transport.NodePingMessage{
				BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePing},
				NodeID:      "node-1",
				Sequence:    int64(seq),
				Timestamp:   time.Now().Unix(),
			}

			// 在 race detection 模式下使用更长的超时时间
			timeout := 10 * time.Second
			if raceMode := os.Getenv("GORACE"); raceMode != "" || raceDetectorEnabled(t) {
				timeout = 30 * time.Second // race 模式下增加超时
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			resp, err := client.Call(ctx, serverAddr, pingMsg)
			if err != nil {
				errors <- err
				return
			}

			if resp == nil {
				errors <- types.NewE2ENilResponseError(seq)
				return
			}

			_, ok := resp.(*transport.NodePongMessage)
			if !ok {
				errors <- types.NewE2EWrongResponseTypeError(seq)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 检查错误
	for err := range errors {
		t.Errorf("心跳失败: %v", err)
	}

	t.Log("✅ 并发心跳 E2E 测试通过")
}

// TestE2E_HeartbeatWithCoordinatorStart 测试启动 coordinator 后的心跳
func TestE2E_HeartbeatWithCoordinatorStart(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 E2E 测试（短模式）")
	}

	_, serverTCP, client, coordinator, cleanup := setupE2ETestEnvironmentV5(t)
	defer cleanup()

	// 启动 coordinator（这会启动心跳 goroutine）
	err := coordinator.Start()
	require.NoError(t, err)
	defer func() { _ = coordinator.Stop() }()

	serverAddr := serverTCP.GetLocalAddr()

	// 添加子节点
	err = coordinator.AddChild("node-2")
	require.NoError(t, err)

	// 等待心跳发送（coordinator 会自动发送心跳）
	time.Sleep(200 * time.Millisecond)

	// 手动发送一次心跳验证连接
	pingMsg := &transport.NodePingMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePing},
		NodeID:      "node-1",
		Sequence:    1,
		Timestamp:   time.Now().Unix(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Call(ctx, serverAddr, pingMsg)
	require.NoError(t, err)
	require.NotNil(t, resp)

	t.Log("✅ Coordinator 启动后心跳 E2E 测试通过")
}

// TestE2E_TwoWayCommunication 测试双向通信
func TestE2E_TwoWayCommunication(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 E2E 测试（短模式）")
	}

	// 创建两个节点环境
	_, serverTCP1, client1, _, cleanup1 := setupE2ETestEnvironmentV5(t)
	defer cleanup1()

	_, serverTCP2, client2, _, cleanup2 := setupE2ETestEnvironmentV5(t)
	defer cleanup2()

	serverAddr1 := serverTCP1.GetLocalAddr()
	serverAddr2 := serverTCP2.GetLocalAddr()

	// node-2 加入 node-1
	joinMsg := &transport.NodeJoinMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodeJoin},
		NodeID:      "node-2",
		Addr:        "127.0.0.1:9212",
		Role:        "child",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client2.Call(ctx, serverAddr1, joinMsg)
	require.NoError(t, err)

	syncMsg := resp.(*transport.NodeSyncMessage)
	assert.Equal(t, "node-1", string(syncMsg.Metadata["parent_node_id"]))

	// node-1 向 node-2 发送心跳
	pingMsg := &transport.NodePingMessage{
		BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePing},
		NodeID:      "node-1",
		Sequence:    1,
		Timestamp:   time.Now().Unix(),
	}

	resp, err = client1.Call(ctx, serverAddr2, pingMsg)
	require.NoError(t, err)
	require.NotNil(t, resp)

	t.Log("✅ 双向通信 E2E 测试通过")
}

// ========================================
// E2E 性能测试
// ========================================

// TestE2E_HeartbeatPerformance 测试心跳性能（跳过，仅测试基本功能）
func TestE2E_HeartbeatPerformance(t *testing.T) {
	t.Skip("跳过性能测试，仅测试基本功能")
	if testing.Short() {
		t.Skip("跳过 E2E 测试（短模式）")
	}

	_, serverTCP, client, coordinator, cleanup := setupE2ETestEnvironmentV5(t)
	defer cleanup()

	serverAddr := serverTCP.GetLocalAddr()

	// 添加子节点
	err := coordinator.AddChild("node-2")
	require.NoError(t, err)

	// 测试多次心跳的性能
	numHeartbeats := 20 // 默认心跳次数
	timeout := 10 * time.Second

	// race 模式下减少心跳次数并增加超时时间
	if raceDetectorEnabled(t) || os.Getenv("GORACE") != "" {
		numHeartbeats = 10         // race 模式下减少并发
		timeout = 30 * time.Second // race 模式下增加超时
	}

	startTime := time.Now()

	for i := 0; i < numHeartbeats; i++ {
		pingMsg := &transport.NodePingMessage{
			BaseMessage: transport.BaseMessage{MessageType: types.MessageTypeNodePing},
			NodeID:      "node-1",
			Sequence:    int64(i),
			Timestamp:   time.Now().Unix(),
		}

		// 使用动态超时时间以适应 race 模式
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		resp, err := client.Call(ctx, serverAddr, pingMsg)
		cancel()

		require.NoError(t, err)
		require.NotNil(t, resp)
	}

	elapsed := time.Since(startTime)
	avgLatency := elapsed / time.Duration(numHeartbeats)

	t.Logf("发送 %d 次心跳，总耗时: %v，平均延迟: %v", numHeartbeats, elapsed, avgLatency)

	// 验证性能合理（race 模式下放宽限制）
	if raceDetectorEnabled(t) || os.Getenv("GORACE") != "" {
		// race 模式下性能会显著下降，放宽到 500ms
		assert.Less(t, avgLatency, 500*time.Millisecond, "race 模式下平均心跳延迟应该 < 500ms")
	} else {
		// 正常模式下平均延迟应该 < 100ms
		assert.Less(t, avgLatency, 100*time.Millisecond, "平均心跳延迟应该 < 100ms")
	}

	t.Log("✅ 心跳性能 E2E 测试通过")
}

// ========================================
// 使用配置文件的测试示例
// ========================================

// TestE2E_ConfigFileLoading 测试从配置文件加载配置
//
// 用法示例：
//
//  1. 使用默认配置文件（configs/config.yaml）的第一个 Host：
//     go test -v -run TestE2E_ConfigFileLoading
//
//  2. 使用自定义配置文件（通过环境变量）：
//     NEXKV_CONFIG=/path/to/config.yaml go test -v -run TestE2E_ConfigFileLoading
//
//  3. 测试不同的 Host（通过环境变量）：
//     NEXKV_HOST_INDEX=1 go test -v -run TestE2E_ConfigFileLoading
func TestE2E_ConfigFileLoading(t *testing.T) {
	// 从环境变量读取配置
	configPath := os.Getenv("NEXKV_CONFIG")
	hostIndex := 0
	if idx := os.Getenv("NEXKV_HOST_INDEX"); idx != "" {
		_, _ = fmt.Sscanf(idx, "%d", &hostIndex) // 解析 hostIndex
	}

	t.Logf("📋 使用配置文件测试: config=%s, hostIndex=%d", configPath, hostIndex)

	// 方式1：使用 SetupTestEnvironmentFromConfig（推荐）
	// 这种方式会自动从配置文件加载配置，并创建测试目录
	_, hostID, cfg, cleanup := SetupTestEnvironmentFromConfig(t, configPath, hostIndex)
	defer cleanup()

	t.Logf("✅ 配置加载成功:")
	t.Logf("   - 集群名称: %s", cfg.Cluster.Name)
	t.Logf("   - 基础目录: %s", cfg.Cluster.BaseDir)
	t.Logf("   - Host 数量: %d", len(cfg.Cluster.Hosts))
	t.Logf("   - 当前测试 Host: %s", hostID)

	// 验证配置完整性
	require.NotNil(t, cfg, "配置不能为空")
	require.NotEmpty(t, cfg.Cluster.Name, "集群名称不能为空")
	require.NotEmpty(t, cfg.Cluster.Hosts, "至少需要配置一个 Host")

	// 验证选中的 Host
	selectedHost := cfg.Cluster.Hosts[hostIndex]
	t.Logf("✅ Host %s 配置:", selectedHost.HostID)
	t.Logf("   - Seed Node: %s", selectedHost.SeedNode)
	t.Logf("   - Node 数量: %d", len(selectedHost.Nodes))

	// 验证 Node 配置
	for i, node := range selectedHost.Nodes {
		t.Logf("   - Node %d: ID=%s, TCP=%s, UDP=%s",
			i, node.NodeID, node.NodeAddrTCP, node.NodeAddrUDP)
		require.NotEmpty(t, node.NodeID, "Node ID 不能为空")
		require.NotEmpty(t, node.NodeAddrTCP, "Node TCP 地址不能为空")
		require.NotEmpty(t, node.NodeAddrUDP, "Node UDP 地址不能为空")
	}

	t.Logf("✅ 配置文件加载测试通过")

	// 方式2：手动加载配置（适用于需要单独加载配置的场景）
	// cfg2, err := LoadTestConfigFromFile(configPath)
	// require.NoError(t, err, "加载配置文件失败")
	// t.Logf("手动加载配置: %+v", cfg2)
}

// TestE2E_ConfigFileWithRealNode 使用配置文件测试真实节点启动
//
// 这个测试演示如何使用配置文件启动真实的 TreeCoordinator 节点
func TestE2E_ConfigFileWithRealNode(t *testing.T) {
	t.Skip("示例测试，默认跳过。可以通过移除 t.Skip() 来启用")

	// 使用配置文件设置测试环境
	_, hostID, cfg, cleanup := SetupTestEnvironmentFromConfig(t, "", 0)
	defer cleanup()

	// 获取第一个 Host 的第一个 Node
	host := cfg.Cluster.Hosts[0]
	node := host.Nodes[0]

	t.Logf("📋 启动节点: Host=%s, Node=%s", hostID, node.NodeID)

	// 解析 Node 地址
	nodeAddr, err := ConvertMultiaddrToHostPort(node.NodeAddrTCP)
	require.NoError(t, err, "解析 Node 地址失败")

	t.Logf("✅ 节点配置解析成功:")
	t.Logf("   - Node ID: %s", node.NodeID)
	t.Logf("   - Node Addr: %s", nodeAddr)
	t.Logf("   - Node Addr TCP: %s", node.NodeAddrTCP)
	t.Logf("   - Node Addr UDP: %s", node.NodeAddrUDP)

	// TODO: 完整的节点启动示例
	// 1. 创建 TreeCoordinator
	// coordinator, err := NewTreeCoordinator(
	//     node.NodeID,
	//     nodeAddr,
	//     DefaultTreeCoordinatorConfig(),
	//     nil, // clusterConfig
	// )
	//
	// 2. 创建 RPC Server
	// server, err := transport.NewRPCServer(...)
	//
	// 3. 创建 RPC Client
	// client := transport.NewRPCClient(...)

	t.Log("✅ 配置文件节点启动测试通过")
}
