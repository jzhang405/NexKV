// Package cluster 提供节点管理层实现
//
// 包含：
//   - 树形协调器：层级化管理，每父最多 10 个子节点
//   - Leader 选举：基于优先级和节点的选举机制
//   - 故障检测：心跳机制检测节点存活
//   - 自愈机制：节点重启、重新找父
package cluster

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/metadata/types"

	metadataconfig "github.com/jzhang405/NexKV/internal/config"
	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/api"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/rpc"
	store "github.com/jzhang405/NexKV/internal/wal"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/vmihailenco/msgpack/v5"
)

// ========================================
// 元数据接口（避免循环导入）
// ========================================

// MetadataNodeInfo 节点元数据接口
type MetadataNodeInfo interface {
	GetNodeID() string
	SetNodeID(string)
	GetHostID() string
	SetHostID(string)
	GetRole() string
	SetRole(string)
	GetParentID() string
	SetParentID(string)
	GetLevel() int
	SetLevel(int)
	GetStatus() string
	SetStatus(string)
	GetPriority() int
	SetPriority(int)
}

// ========================================
// 核心数据结构（双层架构）
// ========================================

// NodeAddress 节点地址结构，包含节点的网络地址信息
// 支持 TCP 和 UDP 两种协议，地址格式采用 IPFS 风格：/ip4/127.0.0.1/tcp/5001
type NodeAddress struct {
	Host string // 主机地址（可以是 IP 地址或域名）
	// 注释：原字段名为 IPAddress，改名为 Host 是因为：
	//   1. 结构体同时包含 TCP 和 UDP 端口，IPAddress 命名不够准确
	//   2. Host 更通用，既可以是 IP 地址（如 127.0.0.1）也可以是域名（如 node1.example.com）
	//   3. 与 TCPAddr() 和 UDPAddr() 方法的实现语义一致，它们使用 na.Host 而非 na.IPAddress
	TCPPort int // TCP端口
	UDPPort int // UDP端口
}

// TCPAddr 返回 TCP 地址字符串（IPFS 格式）
func (na *NodeAddress) TCPAddr() string {
	return fmt.Sprintf("/ip4/%s/tcp/%d", na.Host, na.TCPPort)
}

// UDPAddr 返回 UDP 地址字符串（IPFS 格式）
func (na *NodeAddress) UDPAddr() string {
	return fmt.Sprintf("/ip4/%s/udp/%d", na.Host, na.UDPPort)
}

// ParseNodeAddress 从字符串地址解析出 NodeAddress 结构
// 支持的格式：IPFS 风格 (/ip4/127.0.0.1/tcp/5001) 或 简化格式 (127.0.0.1:5001，默认 TCP)
func ParseNodeAddress(addrStr string) (*NodeAddress, error) {
	if addrStr == "" {
		return nil, types.NewTreeCoordinatorAddrEmptyError()
	}

	// 尝试解析 IPFS 风格格式
	if strings.HasPrefix(addrStr, "/ip4/") {
		// 格式: /ip4/<ip>/<protocol>/<port>
		// 示例: /ip4/127.0.0.1/tcp/5001
		// 注意: / 会分割出空字符串作为第一个元素
		parts := strings.Split(addrStr, "/")
		if len(parts) < 4 {
			return nil, types.NewTreeCoordinatorInvalidIPFSAddrError(addrStr)
		}

		ip := parts[2]
		if len(parts) == 4 {
			// 缺少协议类型: /ip4/127.0.0.1/5001
			return nil, types.NewTreeCoordinatorInvalidProtocolError(addrStr)
		}

		protocol := parts[3]
		portStr := parts[4]

		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, types.NewTreeCoordinatorInvalidPortError(portStr)
		}

		nodeAddr := &NodeAddress{
			Host:    ip,
			TCPPort: 0,
			UDPPort: 0,
		}

		if strings.EqualFold(protocol, "tcp") {
			nodeAddr.TCPPort = port
		} else if strings.EqualFold(protocol, "udp") {
			nodeAddr.UDPPort = port
		} else {
			return nil, types.NewTreeCoordinatorUnsupportedProtocolError(protocol)
		}

		return nodeAddr, nil
	}

	// 尝试解析简化的 IP:端口格式
	lastColon := strings.LastIndex(addrStr, ":")
	if lastColon == -1 {
		return nil, types.NewTreeCoordinatorInvalidAddrError(addrStr)
	}

	ip := addrStr[:lastColon]
	portStr := addrStr[lastColon+1:]

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, types.NewTreeCoordinatorInvalidPortError(portStr)
	}

	return &NodeAddress{
		Host:    ip,
		TCPPort: port,
		UDPPort: 0,
	}, nil
}

// HostRole 物理机器角色类型
type HostRole int

const (
	LeafOnly          HostRole = iota // 仅作为叶子节点运行
	LeafParent                        // 作为叶子节点和父节点双重角色
	LeafParentStandby                 // 作为叶子节点和父节点备用
)

// String 返回角色的字符串表示
func (hr HostRole) String() string {
	switch hr {
	case LeafOnly:
		return "leaf_only"
	case LeafParent:
		return "leaf_parent"
	case LeafParentStandby:
		return "leaf_parent_standby"
	default:
		return "unknown"
	}
}

// PR-037: 从三级配置结构中提取所有 Host 的 seed_node
// 辅助函数：从 ClusterConfig 中提取种子节点地址列表
// P1-6: 对种子节点地址进行去重
func extractSeedNodesFromConfig(clusterConfig *metadataconfig.ClusterConfig) []string {
	if clusterConfig == nil || len(clusterConfig.Hosts) == 0 {
		return nil
	}

	// P1-6: 使用 map 去重，保持首次出现的顺序
	seen := make(map[string]bool)
	seedNodes := make([]string, 0, len(clusterConfig.Hosts))

	for _, host := range clusterConfig.Hosts {
		if host.SeedNode != "" {
			if !seen[host.SeedNode] {
				seen[host.SeedNode] = true
				seedNodes = append(seedNodes, host.SeedNode)
			}
		}
	}
	return seedNodes
}

// NodeRole 逻辑节点角色类型
type NodeRole int

const (
	Leaf          NodeRole = iota // 叶子节点（负责数据读写）
	Parent                        // 父节点（负责元数据管理和协调）
	ParentStandby                 // 父节点备用（故障时接管）
)

// String 返回角色的字符串表示
func (nr NodeRole) String() string {
	switch nr {
	case Leaf:
		return "leaf"
	case Parent:
		return "parent"
	case ParentStandby:
		return "parent_standby"
	default:
		return "unknown"
	}
}

// Host 物理机器结构
// Host 物理机器信息（PR-033 扩展）
type Host struct {
	// 基础字段
	Role     HostRole    `msgpack:"role"`     // 物理机器角色
	NodeAddr NodeAddress `msgpack:"nodeaddr"` // 网络地址信息

	// PR-033 新增字段
	HostID              string     `msgpack:"host_id"`                // 机器唯一标识
	Hostname            string     `msgpack:"hostname"`               // 物理机器地址（IP 或域名）
	LeafNodeID          string     `msgpack:"leaf_node_id"`           // 关联的叶子节点 ID
	ParentNodeID        string     `msgpack:"parent_node_id"`         // 关联的父节点 ID
	ParentStandbyNodeID string     `msgpack:"parent_standby_node_id"` // 关联的备用父节点 ID
	HostStatus          HostStatus `msgpack:"host_status"`            // 主机状态（枚举）
	LastHeartbeat       int64      `msgpack:"last_heartbeat"`         // 最后心跳时间戳（Unix 秒）
	CPUUsage            float64    `msgpack:"cpu_usage"`              // CPU 使用率（0-100）
	MemUsage            float64    `msgpack:"mem_usage"`              // 内存使用率（0-100）
	ExistingNodes       int        `msgpack:"existing_nodes"`         // 已存在的节点数量
}

// TreeCoordinator 树形协调器
//
// 核心职责：
//   - 维护树形拓扑结构
//   - 管理父子节点关系
//   - 处理节点加入/离开
//   - 协调层级化元数据同步
//
// 层级化管理：
//   - Level 0: 根节点（无父节点）
//   - Level 1: 叶子节点（最多 10 个子节点）
//   - Level 2+: 扩展层级（支持大规模集群）
//
// 设计原则：
//   - 松连接：父子关系松散，不严格依赖
//   - 自组织：节点自动找父，形成树形结构
//   - 容错性：单节点故障不影响整体
//
// PR-Libp2p-RPC 变更：
//   - 使用新的 rpc.Client/rpc.Server 替代旧的 transport.RPCClient/RPCServer
type TreeCoordinator struct {
	// 配置
	config *TreeCoordinatorConfig

	// PR-036: 集群配置
	clusterConfig *metadataconfig.ClusterConfig // 集群配置（包含种子节点列表）
	// TODO: 在后续版本中添加 seedNodesWatcher 支持运行时热更新

	// 本地节点信息
	localNode *Node

	// PR-Libp2p-RPC: RPC 组件（使用新的 rpc 包）
	rpcClient  *rpc.Client // RPC 客户端（主动调用）
	rpcServer  *rpc.Server // RPC 服务端（接收请求）
	libp2pHost host.Host   // libp2p 主机（用于 RPC 通信）

	// 节点管理
	allNodes map[string]*Node
	nodesMu  sync.RWMutex

	// 元数据管理（PR-MetadataKV）
	//nolint:unused // 阶段 3 集成时使用
	metadataKV  kvstore.Store // 元数据 KV 存储接口（DIP 修复：依赖抽象）
	metadataAPI api.Provider  // 元数据 API 接口（DIP 修复：依赖抽象）
	metadataMu  sync.RWMutex  // 保护元数据字段
	mvStore     store.MVStore // MVStore 存储引擎（保存引用用于关闭）

	// 状态管理
	state atomic.Int32 // CoordinatorState

	// 统计信息
	stats *TreeCoordinatorStats

	// 心跳序列号计数器（PR-034：实现心跳机制）
	heartbeatSeq atomic.Uint64

	// P0 修复：Goroutine 并发控制信号量
	// 限制并发 goroutine 数量，防止无限制创建导致资源耗尽
	gossipSemaphore    chan struct{} // Gossip 并发限制
	heartbeatSemaphore chan struct{} // 心跳并发限制

	// 集群修复操作（P0-1 修复：添加并发控制）
	// fixMu    sync.Mutex // 修复操作专用锁 //nolint:unused // TODO: 待 libp2p Stream 重写后使用
	// isFixing bool       // 是否正在执行修复操作 //nolint:unused // TODO: 待 libp2p Stream 重写后使用

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
	stopCh  chan struct{}
}

// TreeCoordinatorConfig 树形协调器配置
type TreeCoordinatorConfig struct {
	// MaxChildren 最大子节点数（默认 10）
	MaxChildren int

	// MaxLevel 树的最大深度（默认 4，支持 1000+ 节点）
	// Level 0-3: 最多 1+10+100+1000=1111 节点
	MaxLevel int

	// HeartbeatInterval 心跳间隔（默认 5 秒）
	HeartbeatInterval time.Duration

	// HeartbeatTimeout 心跳超时（默认 15 秒）
	HeartbeatTimeout time.Duration

	// AutoDiscovery 是否自动发现节点
	AutoDiscovery bool

	// EnableSelfHealing 是否启用自愈机制
	EnableSelfHealing bool
}

// DefaultTreeCoordinatorConfig 返回默认配置
func DefaultTreeCoordinatorConfig() *TreeCoordinatorConfig {
	return &TreeCoordinatorConfig{
		MaxChildren:       10,
		MaxLevel:          4, // 支持 1000+ 节点 (1+10+100+1000=1111)
		HeartbeatInterval: 5 * time.Second,
		HeartbeatTimeout:  15 * time.Second,
		AutoDiscovery:     true,
		EnableSelfHealing: true,
	}
}

// Node 树形节点信息
type Node struct {
	// NodeID 节点唯一标识
	NodeID string

	// HostID 所属物理机器 ID（如 server-1）
	HostID string

	// Role 逻辑节点角色（Leaf/Parent/ParentStandby）
	Role NodeRole

	// Addr 节点地址（使用 NodeAddress 类型支持 TCP/UDP）
	Addr NodeAddress

	// ParentID 父节点ID（根节点为空）
	ParentID string

	// ChildrenIDs 子节点ID列表
	ChildrenIDs []string

	// Level 层级（根节点为 0）
	Level int

	// Status 节点状态
	Status NodeStatus

	// Priority 优先级（用于 Leader 选举）
	Priority int

	// LastHeartbeat 最后心跳时间
	LastHeartbeat time.Time

	// Metadata 节点元数据
	Metadata map[string]string
}

// NodeStatus 节点状态
type NodeStatus int

const (
	// NodeStatusInit 初始状态
	NodeStatusInit NodeStatus = iota

	// NodeStatusReady 就绪状态
	NodeStatusReady

	// NodeStatusJoining 加入中
	NodeStatusJoining

	// NodeStatusLeaving 离开中
	NodeStatusLeaving

	// NodeStatusFailed 故障状态
	NodeStatusFailed
)

// String 返回状态的字符串表示
func (s NodeStatus) String() string {
	switch s {
	case NodeStatusInit:
		return "Init"
	case NodeStatusReady:
		return "Ready"
	case NodeStatusJoining:
		return "Joining"
	case NodeStatusLeaving:
		return "Leaving"
	case NodeStatusFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// CoordinatorState 协调器状态
type CoordinatorState int

const (
	// StateStopped 已停止
	StateStopped CoordinatorState = iota

	// StateStarting 启动中
	StateStarting

	// StateRunning 运行中
	StateRunning

	// StateStopping 停止中
	StateStopping
)

// TreeCoordinatorStats 树形协调器统计信息
type TreeCoordinatorStats struct {
	// 总节点数
	TotalNodes atomic.Int32

	// 在线节点数
	OnlineNodes atomic.Int32

	// 离线节点数
	OfflineNodes atomic.Int32

	// 树的深度
	TreeDepth atomic.Int32

	// 最后一次拓扑更新时间
	LastTopologyUpdate atomic.Value // time.Time
}

// NewTreeCoordinator 创建树形协调器
//
// PR-036: 添加 clusterConfig 参数支持种子节点配置
// PR-Libp2p-RPC: 添加 libp2pHost 参数用于 RPC 通信
//
// 参数：
//   - localNodeID: 本地节点 ID
//   - localAddr: 本地节点地址（IPFS multiaddr 格式或简化格式）
//   - config: 树形协调器配置（nil 时使用默认配置）
//   - clusterConfig: 集群配置（包含种子节点列表，nil 时跳过种子节点配置）
//   - libp2pHost: libp2p 主机（用于 RPC 通信，nil 时跳过 RPC 初始化）
func NewTreeCoordinator(
	localNodeID string,
	localAddr string,
	config *TreeCoordinatorConfig,
	clusterConfig *metadataconfig.ClusterConfig,
	libp2pHost host.Host,
) (*TreeCoordinator, error) {
	if config == nil {
		config = DefaultTreeCoordinatorConfig()
	}

	if localNodeID == "" {
		return nil, types.NewClusterNilParameterError("localNodeID")
	}

	if localAddr == "" {
		return nil, types.NewClusterNilParameterError("localAddr")
	}

	// 创建本地节点
	parsedAddr, err := ParseNodeAddress(localAddr)
	if err != nil {
		return nil, types.NewClusterCoordinatorError("解析 localAddr 失败", err)
	}

	localNode := &Node{
		NodeID:      localNodeID,
		Addr:        *parsedAddr,
		ParentID:    "",
		ChildrenIDs: make([]string, 0),
		Level:       0,
		Status:      NodeStatusInit,
		Priority:    0,
		Metadata:    make(map[string]string),
	}

	// 创建 TreeCoordinator 基础结构
	coordinator := &TreeCoordinator{
		config:        config,
		clusterConfig: clusterConfig,
		localNode:     localNode,
		libp2pHost:    libp2pHost,
		allNodes:      make(map[string]*Node),
		stopCh:        make(chan struct{}),
		stats:         &TreeCoordinatorStats{},
		// P0 修复：初始化 goroutine 并发控制信号量
		gossipSemaphore:    make(chan struct{}, 20),  // 最多 20 个并发 Gossip goroutine
		heartbeatSemaphore: make(chan struct{}, 100), // 最多 100 个并发心跳 goroutine
	}

	// PR-Libp2p-RPC: 初始化 RPC 组件（如果提供了 libp2pHost）
	if libp2pHost != nil {
		// 创建 RPC 客户端
		coordinator.rpcClient = rpc.NewClient(libp2pHost)

		// 创建 RPC 服务端
		coordinator.rpcServer = rpc.NewServer(libp2pHost)

		logging.WithFields(map[string]any{
			"node_id":     localNodeID,
			"libp2p_peer": libp2pHost.ID().String(),
		}).Info("RPC 组件初始化成功")
	}

	// 添加本地节点
	coordinator.allNodes[localNodeID] = localNode
	coordinator.stats.TotalNodes.Store(1)
	coordinator.stats.OnlineNodes.Store(1)
	coordinator.stats.LastTopologyUpdate.Store(time.Now())

	// PR-037: 初始化种子节点（从三级配置结构中提取）
	if clusterConfig != nil && len(clusterConfig.Hosts) > 0 {
		// PR-037: 从三级配置结构中提取所有 Host 的 seed_node
		seedNodes := extractSeedNodesFromConfig(clusterConfig)
		if len(seedNodes) > 0 {
			// 验证并规范化种子节点配置
			normalizedSeedNodes, err := metadataconfig.ParseSeedNodes(seedNodes)
			if err != nil {
				logging.Warnf("[TreeCoordinator] 解析种子节点配置失败: %v（将不使用种子节点）", err)
			} else {
				// PR-037: 不再写回 clusterConfig（seed_node 现在是每个 Host 的字段）
				logging.Infof("[TreeCoordinator] 初始化 %d 个种子节点", len(normalizedSeedNodes))
			}
		}
	}

	return coordinator, nil
}

// Start 启动树形协调器
func (tc *TreeCoordinator) Start() error {
	if !tc.started.CompareAndSwap(false, true) {
		return types.NewClusterServiceStateError("树形协调器", "已经启动")
	}

	tc.state.Store(int32(StateStarting))

	logging.WithFields(map[string]any{
		"node_id":        tc.localNode.NodeID,
		"max_children":   tc.config.MaxChildren,
		"auto_discovery": tc.config.AutoDiscovery,
	}).Info("启动树形协调器")

	// PR-Libp2p-RPC: 启动 RPC 服务端（如果已初始化）
	if tc.rpcServer != nil {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			if err := tc.rpcServer.Start(ctx); err != nil {
				logging.WithField("error", err).Error("RPC 服务端启动失败")
			}
		}()

		// 等待 RPC 服务端启动
		time.Sleep(100 * time.Millisecond)

		if !tc.rpcServer.IsRunning() {
			return types.NewClusterCoordinatorError("RPC 服务端启动失败", nil)
		}

		logging.WithField("node_id", tc.localNode.NodeID).Info("RPC 服务端启动成功")

		// 注册元数据 RPC 处理器
		if err := tc.registerMetadataRPCHandlers(); err != nil {
			logging.WithField("error", err).Warn("注册元数据 RPC 处理器失败")
		}

		// 初始化元数据存储（使用配置的路径）
		dataDir := tc.getMetadataDir()
		if err := tc.setupMetadataStorage(dataDir); err != nil {
			logging.WithField("error", err).Warn("初始化元数据存储失败，将使用内存节点管理")
		}
	}

	// 标记本地节点就绪
	tc.localNode.Status = NodeStatusReady
	tc.localNode.LastHeartbeat = time.Now()

	// P1-2 修复：启动带 panic 恢复的 goroutine
	if tc.config.AutoDiscovery {
		tc.startGoroutineWithRecovery("discoverAndJoin", tc.discoverAndJoin)
	}
	tc.startGoroutineWithRecovery("heartbeatLoop", tc.heartbeatLoop)
	tc.startGoroutineWithRecovery("failureDetectionLoop", tc.failureDetectionLoop)

	tc.state.Store(int32(StateRunning))
	tc.started.Store(true)

	logging.WithField("node_id", tc.localNode.NodeID).Info("树形协调器启动成功")
	return nil
}

// startGoroutineWithRecovery 启动带 panic 恢复的 goroutine（P1-2 修复）
func (tc *TreeCoordinator) startGoroutineWithRecovery(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logging.WithFields(map[string]any{
					"node_id":   tc.localNode.NodeID,
					"goroutine": name,
					"panic":     r,
					"stack":     string(debug.Stack()),
				}).Error("Goroutine panic recovered")
			}
		}()
		fn()
	}()
}

// Stop 停止树形协调器
func (tc *TreeCoordinator) Stop() error {
	if !tc.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	tc.state.Store(int32(StateStopping))

	logging.WithField("node_id", tc.localNode.NodeID).Info("停止树形协调器...")

	// PR-Libp2p-RPC: 停止 RPC 服务端
	if tc.rpcServer != nil && tc.rpcServer.IsRunning() {
		if err := tc.rpcServer.Stop(); err != nil {
			logging.WithField("error", err).Warn("停止 RPC 服务端失败")
		}
	}

	// 关闭停止信号
	close(tc.stopCh)

	// 关闭元数据 KV 存储
	if err := tc.closeMetadataKV(); err != nil {
		logging.WithField("error", err).Warn("关闭元数据 KV 存储失败")
	}

	// 离开树形结构
	tc.leaveTree()

	// 打印统计信息
	logging.WithFields(map[string]any{
		"total_nodes":   tc.stats.TotalNodes.Load(),
		"online_nodes":  tc.stats.OnlineNodes.Load(),
		"offline_nodes": tc.stats.OfflineNodes.Load(),
		"tree_depth":    tc.stats.TreeDepth.Load(),
	}).Info("树形协调器统计")

	logging.Info("树形协调器已停止")
	tc.state.Store(int32(StateStopped))
	return nil
}

// ========================================
// 核心协调逻辑
// ========================================

// discoverAndJoin 发现并加入树形结构
//
// PR-Libp2p-RPC: 使用新的 RPC 框架恢复 RPC 调用
//
// PR-034 实现：
//  1. 通过传输层发现可用节点
//  2. 选择合适的父节点（Level 最小且未满）
//  3. 发送加入请求
//  4. 更新本地节点信息
func (tc *TreeCoordinator) discoverAndJoin() {
	logging.WithField("node_id", tc.localNode.NodeID).Info("开始自动发现并加入树形结构")

	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		logging.WithField("node_id", tc.localNode.NodeID).Info("⚠️ RPC 客户端未初始化，跳过节点发现")
		tc.nodesMu.Lock()
		tc.localNode.Status = NodeStatusReady
		tc.nodesMu.Unlock()
		return
	}

	// 获取已知节点列表
	nodes := tc.getKnownNodes()
	if len(nodes) == 0 {
		logging.WithField("node_id", tc.localNode.NodeID).Info("没有已知节点，等待种子节点配置")
		tc.nodesMu.Lock()
		tc.localNode.Status = NodeStatusReady
		tc.nodesMu.Unlock()
		return
	}

	// 选择最佳父节点
	parent := tc.selectBestParent(nodes)
	if parent == nil {
		logging.WithField("node_id", tc.localNode.NodeID).Info("没有找到合适的父节点")
		tc.nodesMu.Lock()
		tc.localNode.Status = NodeStatusReady
		tc.nodesMu.Unlock()
		return
	}

	// 发送加入请求
	if err := tc.sendJoinRequest(parent); err != nil {
		logging.WithFields(map[string]any{
			"node_id": tc.localNode.NodeID,
			"parent":  parent.NodeID,
			"error":   err,
		}).Warn("发送加入请求失败")
		// 失败后也标记为 Ready，后续会重试
		tc.nodesMu.Lock()
		tc.localNode.Status = NodeStatusReady
		tc.nodesMu.Unlock()
		return
	}

	logging.WithFields(map[string]any{
		"node_id": tc.localNode.NodeID,
		"parent":  parent.NodeID,
		"level":   tc.localNode.Level,
	}).Info("成功加入树形结构")
}

// getKnownNodes 获取已知节点列表
//
// # PR-036 实现：从配置和内存中获取已知节点
//
// 流程：
//  1. 从配置中读取种子节点（优先级最高）
//  2. 自动过滤自身地址（决策 3）
//  3. 降级处理无效地址（决策 2）
//  4. 合并内存中的已知节点（向后兼容）
func (tc *TreeCoordinator) getKnownNodes() []*Node {
	nodes := make([]*Node, 0)

	// PR-037: 1. 从三级配置结构中读取种子节点
	if tc.clusterConfig != nil && len(tc.clusterConfig.Hosts) > 0 {
		seedNodes := extractSeedNodesFromConfig(tc.clusterConfig)
		if len(seedNodes) == 0 {
			return nodes
		}
		for _, addr := range seedNodes {
			// 决策 3: 自动过滤自身地址
			selfAddr := tc.localNode.Addr.TCPAddr()
			if addr == selfAddr {
				logging.Debugf("[TreeCoordinator] 跳过自身地址: %s", addr)
				continue
			}

			// 决策 2: 降级处理 - 验证地址格式
			if err := metadataconfig.ValidateSeedNodeAddress(addr); err != nil {
				logging.Warnf("[TreeCoordinator] 无效的种子节点地址 %s: %v（跳过）", addr, err)
				continue
			}

			// 解析地址并创建节点对象
			parsedAddr, err := ParseNodeAddress(addr)
			if err != nil {
				logging.Warnf("[TreeCoordinator] 解析节点地址失败 %s: %v（跳过）", addr, err)
				continue
			}

			node := &Node{
				NodeID:   "", // 种子节点可能还没有 NodeID
				Addr:     *parsedAddr,
				Status:   NodeStatusReady, // 种子节点假设为可用状态
				ParentID: "",
				Level:    0,
			}

			// 检查是否已存在（避免重复）
			if !containsNodeAddr(nodes, addr) {
				nodes = append(nodes, node)
			}
		}
	}

	// 2. 合并内存中的已知节点（向后兼容 + 降级处理）
	// P1-3 修复：在锁内完成所有操作，确保数据一致性
	// 使用 defer 确保锁一定会被释放
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	for _, node := range tc.allNodes {
		// 跳过本地节点
		if node.NodeID == tc.localNode.NodeID {
			continue
		}
		nodeAddr := node.Addr.TCPAddr()
		// 去重检查：如果节点地址已存在，则跳过
		if !containsNodeAddr(nodes, nodeAddr) {
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// containsNodeAddr 检查节点地址是否已存在于列表中
func containsNodeAddr(nodes []*Node, addr string) bool {
	for _, node := range nodes {
		if node.Addr.TCPAddr() == addr {
			return true
		}
	}
	return false
}

// selectBestParent 选择最佳父节点
//
// PR-034 实现：
//   - 优先选择 Level 最小的节点（树的最底层）
//   - 确保 Level < MaxLevel
//   - 确保子节点数 < MaxChildren
func (tc *TreeCoordinator) selectBestParent(nodes []*Node) *Node {
	var bestParent *Node
	minLevel := int(^uint(0) >> 1) // 最大 int 值

	logging.WithFields(map[string]any{
		"candidate_count": len(nodes),
		"max_level":       tc.config.MaxLevel,
		"max_children":    tc.config.MaxChildren,
	}).Info("开始选择父节点")

	for _, node := range nodes {
		// 跳过不可用的节点
		if node.Status != NodeStatusReady {
			logging.WithFields(map[string]any{
				"addr":   node.Addr.TCPAddr(),
				"status": node.Status,
			}).Debug("跳过非 Ready 状态节点")
			continue
		}

		// 检查 Level 限制
		if node.Level >= tc.config.MaxLevel {
			logging.WithFields(map[string]any{
				"addr":  node.Addr.TCPAddr(),
				"level": node.Level,
			}).Debug("跳过 Level 达到上限的节点")
			continue
		}

		// 检查子节点数量限制
		if len(node.ChildrenIDs) >= tc.config.MaxChildren {
			logging.WithFields(map[string]any{
				"addr":           node.Addr.TCPAddr(),
				"children_count": len(node.ChildrenIDs),
			}).Debug("跳过子节点数达到上限的节点")
			continue
		}

		// 选择 Level 最小的节点
		if node.Level < minLevel {
			minLevel = node.Level
			bestParent = node
		}
	}

	if bestParent != nil {
		logging.WithFields(map[string]any{
			"selected_addr": bestParent.Addr.TCPAddr(),
			"level":         bestParent.Level,
		}).Info("选择父节点成功")
	} else {
		logging.Info("没有找到合适的父节点")
	}

	return bestParent
}

// sendJoinRequest 发送加入请求
//
// PR-Libp2p-RPC: 使用新的 RPC 框架发送加入请求
//
// PR-034 实现：向目标节点发送 NodeJoinRequest
func (tc *TreeCoordinator) sendJoinRequest(targetNode *Node) error {
	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		return types.NewClusterCoordinatorError("RPC 客户端未初始化", nil)
	}

	// 将目标节点地址转换为 peer.ID
	// 注意：这里简化处理，实际使用中需要维护 NodeID -> peer.ID 的映射
	// 暂时使用地址字符串作为 peer.ID（仅用于测试）
	targetPeerID := tc.addrToPeerID(targetNode.Addr.TCPAddr())

	// 构造加入请求
	joinReq := rpc.NewNodeJoinRequest(
		tc.localNode.NodeID,
		tc.localNode.Addr.TCPAddr(),
		int(tc.localNode.Role),
	)

	// 序列化请求体
	reqBody, err := msgpack.Marshal(joinReq)
	if err != nil {
		return types.NewClusterCoordinatorError("序列化加入请求失败", err)
	}

	// 发送 RPC 请求
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	respBody, err := tc.rpcClient.Call(ctx, targetPeerID, "NodeJoin", reqBody)
	if err != nil {
		return types.NewClusterCoordinatorError("发送加入请求失败", err)
	}

	// 解析响应
	var joinResp rpc.NodeJoinResponse
	if err := msgpack.Unmarshal(respBody, &joinResp); err != nil {
		return types.NewClusterCoordinatorError("解析加入响应失败", err)
	}

	// 检查是否被接受
	if !joinResp.Accepted {
		return types.NewClusterCoordinatorError("加入请求被拒绝", fmt.Errorf("%s", joinResp.Reason))
	}

	// 更新本地节点信息并添加父节点到 allNodes（合并锁区域）
	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	// 更新本地节点
	tc.localNode.ParentID = joinResp.ParentID
	tc.localNode.Level = joinResp.Level
	tc.localNode.Status = NodeStatusReady

	// 添加父节点到 allNodes
	if _, exists := tc.allNodes[joinResp.ParentID]; !exists {
		tc.allNodes[joinResp.ParentID] = &Node{
			NodeID: joinResp.ParentID,
			Addr:   targetNode.Addr,
			Level:  joinResp.Level - 1,
			Status: NodeStatusReady,
		}
	}

	return nil
}

// heartbeatLoop 心跳循环
func (tc *TreeCoordinator) heartbeatLoop() {
	ticker := time.NewTicker(tc.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tc.sendHeartbeat()

		case <-tc.stopCh:
			return
		}
	}
}

// sendHeartbeat 发送心跳
//
// PR-Libp2p-RPC: 使用新的 RPC 框架发送心跳
//
// PR-034 实现：向父节点和子节点发送心跳，用于故障检测
func (tc *TreeCoordinator) sendHeartbeat() {
	// 步骤 1: 更新本地节点心跳时间
	tc.localNode.LastHeartbeat = time.Now()

	// 步骤 2: 生成心跳序列号
	sequence := tc.heartbeatSeq.Add(1)

	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		logging.WithFields(map[string]any{
			"node_id":  tc.localNode.NodeID,
			"sequence": sequence,
		}).Debug("⚠️ RPC 客户端未初始化，跳过心跳发送")
		return
	}

	// 构造心跳请求
	pingReq := rpc.NewNodePingRequest(sequence)

	// 序列化请求体
	reqBody, err := msgpack.Marshal(pingReq)
	if err != nil {
		logging.WithField("error", err).Error("序列化心跳请求失败")
		return
	}

	// 向父节点发送心跳
	if tc.localNode.ParentID != "" {
		tc.nodesMu.RLock()
		parent, exists := tc.allNodes[tc.localNode.ParentID]
		tc.nodesMu.RUnlock()

		if exists {
			// P0 修复：使用信号量限制并发 goroutine 数量
			tc.heartbeatSemaphore <- struct{}{}
			go func(node *Node) {
				defer func() { <-tc.heartbeatSemaphore }()
				tc.sendHeartbeatToNode(node, reqBody)
			}(parent)
		}
	}

	// 向子节点发送心跳
	tc.nodesMu.RLock()
	children := make([]*Node, 0, len(tc.localNode.ChildrenIDs))
	for _, childID := range tc.localNode.ChildrenIDs {
		if child, exists := tc.allNodes[childID]; exists {
			children = append(children, child)
		}
	}
	tc.nodesMu.RUnlock()

	// P0 修复：使用信号量限制并发 goroutine 数量
	for _, child := range children {
		tc.heartbeatSemaphore <- struct{}{}
		go func(node *Node) {
			defer func() { <-tc.heartbeatSemaphore }()
			tc.sendHeartbeatToNode(node, reqBody)
		}(child)
	}
}

// sendHeartbeatToNode 向指定节点发送心跳（独立 goroutine 运行）
//
// PR-Libp2p-RPC: 使用新的 RPC 框架发送心跳
func (tc *TreeCoordinator) sendHeartbeatToNode(targetNode *Node, reqBody []byte) {
	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		return
	}

	// 将目标节点地址转换为 peer.ID
	targetPeerID := tc.addrToPeerID(targetNode.Addr.TCPAddr())

	// 发送 RPC 请求
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	respBody, err := tc.rpcClient.Call(ctx, targetPeerID, "NodePing", reqBody)
	if err != nil {
		logging.WithFields(map[string]any{
			"target_node": targetNode.NodeID,
			"error":       err,
		}).Debug("心跳发送失败")
		return
	}

	// 解析响应
	var pingResp rpc.NodePingResponse
	if err := msgpack.Unmarshal(respBody, &pingResp); err != nil {
		logging.WithFields(map[string]any{
			"target_node": targetNode.NodeID,
			"error":       err,
		}).Debug("解析心跳响应失败")
		return
	}

	// 更新目标节点的心跳时间
	tc.nodesMu.Lock()
	if node, exists := tc.allNodes[targetNode.NodeID]; exists {
		node.LastHeartbeat = time.Now()
	}
	tc.nodesMu.Unlock()

	logging.WithFields(map[string]any{
		"target_node": targetNode.NodeID,
		"sequence":    pingResp.Sequence,
	}).Debug("心跳发送成功")
}

// failureDetectionLoop 故障检测循环
func (tc *TreeCoordinator) failureDetectionLoop() {
	ticker := time.NewTicker(tc.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tc.detectFailures()

		case <-tc.stopCh:
			return
		}
	}
}

// detectFailures 检测故障节点
func (tc *TreeCoordinator) detectFailures() {
	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	now := time.Now()
	timeout := tc.config.HeartbeatTimeout

	for _, node := range tc.allNodes {
		if node.NodeID == tc.localNode.NodeID {
			continue // 跳过本地节点
		}

		// 检查心跳超时
		if now.Sub(node.LastHeartbeat) > timeout {
			if node.Status != NodeStatusFailed {
				logging.WithFields(map[string]any{
					"node_id": node.NodeID,
					"level":   node.Level,
				}).Warn("检测到节点故障")

				node.Status = NodeStatusFailed
				tc.stats.OnlineNodes.Add(-1)
				tc.stats.OfflineNodes.Add(1)

				// 如果启用自愈，触发自愈机制
				if tc.config.EnableSelfHealing {
					go tc.triggerSelfHealing(node)
				}
			}
		}
	}
}

// triggerSelfHealing 触发自愈机制
//
// PR-034 实现：
//  1. 移除故障节点的父子关系
//  2. 子节点重新寻找父节点
//  3. 更新树形拓扑
func (tc *TreeCoordinator) triggerSelfHealing(failedNode *Node) {
	logging.WithFields(map[string]any{
		"failed_node": failedNode.NodeID,
		"level":       failedNode.Level,
	}).Info("触发自愈机制")

	// 步骤 1: 移除故障节点的父子关系
	tc.nodesMu.Lock()
	tc.removeNodeRelationships(failedNode.NodeID)
	tc.nodesMu.Unlock()

	// 步骤 2: 让故障节点的子节点重新寻找父节点
	tc.nodesMu.RLock()
	orphanChildren := make([]*Node, 0, len(failedNode.ChildrenIDs))
	for _, childID := range failedNode.ChildrenIDs {
		if child, exists := tc.allNodes[childID]; exists {
			orphanChildren = append(orphanChildren, child)
		}
	}
	tc.nodesMu.RUnlock()

	// 步骤 3: 为每个孤儿节点重新分配父节点
	for _, orphanChild := range orphanChildren {
		if err := tc.reparentNode(orphanChild, failedNode.NodeID); err != nil {
			logging.WithFields(map[string]any{
				"orphan_node": orphanChild.NodeID,
				"old_parent":  failedNode.NodeID,
				"error":       err,
			}).Error("重新分配父节点失败")
		}
	}

	logging.WithFields(map[string]any{
		"failed_node":     failedNode.NodeID,
		"orphan_children": len(orphanChildren),
	}).Info("自愈机制处理完成")
}

// removeNodeRelationships 移除节点的父子关系
func (tc *TreeCoordinator) removeNodeRelationships(nodeID string) {
	// 步骤 1: 从父节点的子节点列表中移除
	node, exists := tc.allNodes[nodeID]
	if !exists {
		return
	}

	if node.ParentID != "" {
		if parent, exists := tc.allNodes[node.ParentID]; exists {
			parent.ChildrenIDs = slices.DeleteFunc(parent.ChildrenIDs, func(id string) bool {
				return id == nodeID
			})
		}
	}

	// 步骤 2: 清空故障节点的子节点列表
	node.ChildrenIDs = nil
	node.ParentID = ""
}

// reparentNode 为节点重新分配父节点
func (tc *TreeCoordinator) reparentNode(node *Node, oldParentID string) error {
	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	// 步骤 1: 查找合适的父节点
	candidateParents := tc.findCandidateParents(node)
	if len(candidateParents) == 0 {
		return types.NewTreeCoordinatorNoSuitableParentError()
	}

	// 步骤 2: 选择第一个可用的候选父节点
	newParentID := candidateParents[0]

	// 步骤 3: 更新节点的父节点关系
	node.ParentID = newParentID
	newParent := tc.allNodes[newParentID]
	newParent.ChildrenIDs = append(newParent.ChildrenIDs, node.NodeID)

	// 步骤 4: 更新节点的层级
	node.Level = newParent.Level + 1

	logging.WithFields(map[string]any{
		"node":       node.NodeID,
		"old_parent": oldParentID,
		"new_parent": newParentID,
		"new_level":  node.Level,
	}).Info("重新分配父节点成功")

	return nil
}

// findCandidateParents 查找候选父节点
func (tc *TreeCoordinator) findCandidateParents(node *Node) []string {
	candidates := make([]string, 0)

	// 遍历所有节点，寻找合适的父节点
	for _, candidate := range tc.allNodes {
		// 跳过故障节点和自身
		if candidate.Status == NodeStatusFailed || candidate.NodeID == node.NodeID {
			continue
		}

		// 跳过子节点（避免循环依赖）
		if slices.Contains(node.ChildrenIDs, candidate.NodeID) {
			continue
		}

		// 检查层级限制（新父节点的层级 + 1 不能超过 MaxLevel）
		if candidate.Level+1 > tc.config.MaxLevel {
			continue
		}

		// 检查子节点数量限制
		if len(candidate.ChildrenIDs) >= tc.config.MaxChildren {
			continue
		}

		// 优先选择同级或更高级别的节点（保持树的平衡）
		if candidate.Level < node.Level {
			candidates = append(candidates, candidate.NodeID)
		}
	}

	return candidates
}

// leaveTree 离开树形结构
//
// PR-Libp2p-RPC: 使用新的 RPC 框架发送离开消息
//
// PR-034 实现：通知父节点和子节点
func (tc *TreeCoordinator) leaveTree() {
	// 修复竞态条件：加锁保护 localNode.Status
	tc.nodesMu.Lock()
	tc.localNode.Status = NodeStatusLeaving
	tc.nodesMu.Unlock()

	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		logging.WithField("node_id", tc.localNode.NodeID).Info("⚠️ RPC 客户端未初始化，跳过离开通知")
		return
	}

	// 构造离开请求
	leaveReq := rpc.NewNodeLeaveRequest(tc.localNode.NodeID)

	// 序列化请求体
	reqBody, err := msgpack.Marshal(leaveReq)
	if err != nil {
		logging.WithField("error", err).Error("序列化离开请求失败")
		return
	}

	// 向父节点发送离开消息
	if tc.localNode.ParentID != "" {
		tc.nodesMu.RLock()
		parent, exists := tc.allNodes[tc.localNode.ParentID]
		tc.nodesMu.RUnlock()

		if exists {
			go tc.sendLeaveMessage(parent, reqBody)
		}
	}

	// 向子节点发送离开消息
	tc.nodesMu.RLock()
	children := make([]*Node, 0, len(tc.localNode.ChildrenIDs))
	for _, childID := range tc.localNode.ChildrenIDs {
		if child, exists := tc.allNodes[childID]; exists {
			children = append(children, child)
		}
	}
	tc.nodesMu.RUnlock()

	for _, child := range children {
		go tc.sendLeaveMessage(child, reqBody)
	}
}

// sendLeaveMessage 发送离开消息
//
// PR-Libp2p-RPC: 使用新的 RPC 框架发送离开消息
func (tc *TreeCoordinator) sendLeaveMessage(targetNode *Node, reqBody []byte) {
	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		return
	}

	// 将目标节点地址转换为 peer.ID
	targetPeerID := tc.addrToPeerID(targetNode.Addr.TCPAddr())

	// 发送 RPC 请求
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := tc.rpcClient.Call(ctx, targetPeerID, "NodeLeave", reqBody)
	if err != nil {
		logging.WithFields(map[string]any{
			"target_node": targetNode.NodeID,
			"error":       err,
		}).Debug("发送离开消息失败")
		return
	}

	logging.WithField("target_node", targetNode.NodeID).Debug("发送离开消息成功")
}

// ========================================
// 节点管理
// ========================================

// AddChild 添加子节点
func (tc *TreeCoordinator) AddChild(childID string) error {
	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	// 检查子节点数量
	if len(tc.localNode.ChildrenIDs) >= tc.config.MaxChildren {
		return types.NewClusterTreeManagementError(fmt.Sprintf("子节点数量已达上限 %d", tc.config.MaxChildren))
	}

	// 检查层级限制（新子节点的 Level 不能超过 MaxLevel）
	newChildLevel := tc.localNode.Level + 1
	if newChildLevel > tc.config.MaxLevel {
		return types.NewClusterTreeManagementError(fmt.Sprintf("超出树的最大深度限制 %d", tc.config.MaxLevel))
	}

	// 检查是否已存在
	if slices.Contains(tc.localNode.ChildrenIDs, childID) {
		return types.NewClusterTreeManagementError("子节点已存在: " + childID)
	}

	// 检查 child 是否已经有父节点（确保一个真实节点只能只有一个 ParentID）
	if child, exists := tc.allNodes[childID]; exists {
		// 如果 child 已经有父节点，且不是当前节点
		if child.ParentID != "" && child.ParentID != tc.localNode.NodeID {
			return types.NewClusterTreeManagementError(fmt.Sprintf("%s 已经是 %s 的子节点，不能同时作为 %s 的子节点", childID, child.ParentID, tc.localNode.NodeID))
		}
	}

	// 添加子节点到本地节点列表
	tc.localNode.ChildrenIDs = append(tc.localNode.ChildrenIDs, childID)

	// 确保子节点存在于 allNodes 中（如果不存在则创建）
	child, exists := tc.allNodes[childID]
	if !exists {
		// 创建新的子节点
		child = &Node{
			NodeID:   childID,
			Status:   NodeStatusReady,
			ParentID: tc.localNode.NodeID,
			Level:    newChildLevel,
		}
		tc.allNodes[childID] = child
	} else {
		// 更新现有子节点的父节点和层级
		child.ParentID = tc.localNode.NodeID
		child.Level = newChildLevel
	}

	tc.stats.LastTopologyUpdate.Store(time.Now())

	logging.WithFields(map[string]any{
		"parent":       tc.localNode.NodeID,
		"child":        childID,
		"level":        newChildLevel,
		"max_level":    tc.config.MaxLevel,
		"max_children": tc.config.MaxChildren,
	}).Info("添加子节点")

	return nil
}

// AddChildWithAddr 添加子节点（包含地址信息）
//
// 与 AddChild 的区别：
//   - AddChildWithAddr 会设置节点的 Addr 字段
//   - 用于处理来自其他节点的加入请求（已知对方地址）
func (tc *TreeCoordinator) AddChildWithAddr(childID string, addr *NodeAddress) error {
	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	// 检查子节点数量限制
	if len(tc.localNode.ChildrenIDs) >= tc.config.MaxChildren {
		return types.NewClusterTreeManagementError(fmt.Sprintf("子节点数量已达上限 %d", tc.config.MaxChildren))
	}

	// 检查层级限制（新子节点的 Level 不能超过 MaxLevel）
	newChildLevel := tc.localNode.Level + 1
	if newChildLevel > tc.config.MaxLevel {
		return types.NewClusterTreeManagementError(fmt.Sprintf("超出树的最大深度限制 %d", tc.config.MaxLevel))
	}

	// 检查是否已存在
	if slices.Contains(tc.localNode.ChildrenIDs, childID) {
		return types.NewClusterTreeManagementError("子节点已存在: " + childID)
	}

	// 检查 child 是否已经有父节点（确保一个真实节点只能只有一个 ParentID）
	if child, exists := tc.allNodes[childID]; exists {
		// 如果 child 已经有父节点，且不是当前节点
		if child.ParentID != "" && child.ParentID != tc.localNode.NodeID {
			return types.NewClusterTreeManagementError(fmt.Sprintf("%s 已经是 %s 的子节点，不能同时作为 %s 的子节点", childID, child.ParentID, tc.localNode.NodeID))
		}
	}

	// 添加子节点到本地节点列表
	tc.localNode.ChildrenIDs = append(tc.localNode.ChildrenIDs, childID)

	// 确保子节点存在于 allNodes 中（如果不存在则创建）
	child, exists := tc.allNodes[childID]
	if !exists {
		// 创建新的子节点（包含地址信息）
		child = &Node{
			NodeID:        childID,
			Addr:          *addr, // 关键：设置节点地址
			Status:        NodeStatusReady,
			ParentID:      tc.localNode.NodeID,
			Level:         newChildLevel,
			LastHeartbeat: time.Now(), // 初始化心跳时间，避免立即被标记为故障
		}
		tc.allNodes[childID] = child
	} else {
		// 更新现有子节点的父节点、层级和地址
		child.ParentID = tc.localNode.NodeID
		child.Level = newChildLevel
		child.Addr = *addr               // 更新地址
		child.LastHeartbeat = time.Now() // 更新心跳时间
	}

	tc.stats.LastTopologyUpdate.Store(time.Now())

	logging.WithFields(map[string]any{
		"parent":       tc.localNode.NodeID,
		"child":        childID,
		"level":        newChildLevel,
		"max_level":    tc.config.MaxLevel,
		"max_children": tc.config.MaxChildren,
		"addr":         addr.TCPAddr(), // 记录地址信息
	}).Info("添加子节点")

	return nil
}

// RemoveChild 移除子节点
func (tc *TreeCoordinator) RemoveChild(childID string) error {
	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	// 查找并移除子节点
	idx := slices.Index(tc.localNode.ChildrenIDs, childID)
	if idx == -1 {
		return types.NewClusterTreeManagementError("子节点不存在: " + childID)
	}

	tc.localNode.ChildrenIDs = slices.Delete(tc.localNode.ChildrenIDs, idx, idx+1)

	// 更新子节点信息
	if child, exists := tc.allNodes[childID]; exists {
		child.ParentID = ""
		child.Level = 0
	}

	tc.stats.LastTopologyUpdate.Store(time.Now())

	logging.WithFields(map[string]any{
		"parent": tc.localNode.NodeID,
		"child":  childID,
	}).Info("移除子节点")

	return nil
}

// ReparentChild 重新分配子节点的父节点
//
// PR-034 实现：
//   - 如果新父节点是本地节点，添加子节点
//   - 如果旧父节点是本地节点，移除子节点
//   - 如果两者都不是本地节点，返回成功（由相关节点处理）
func (tc *TreeCoordinator) ReparentChild(childID, newParentID, oldParentID string) error {
	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	// 步骤 1: 验证节点存在
	child, exists := tc.allNodes[childID]
	if !exists {
		return types.NewClusterNodeNotFoundError(childID)
	}

	// 步骤 2: 检查是否涉及本地节点
	involvesLocalNode := (newParentID == tc.localNode.NodeID) || (oldParentID == tc.localNode.NodeID)
	if !involvesLocalNode {
		// 不涉及本地节点，返回成功（由相关节点处理）
		logging.WithFields(map[string]any{
			"child":      childID,
			"old_parent": oldParentID,
			"new_parent": newParentID,
		}).Debug("重新分配父节点不涉及本地节点，跳过处理")
		return nil
	}

	// 步骤 3: 如果新父节点是本地节点，添加子节点
	if newParentID == tc.localNode.NodeID {
		// 检查子节点数量
		if len(tc.localNode.ChildrenIDs) >= tc.config.MaxChildren {
			return types.NewClusterTreeManagementError(fmt.Sprintf("子节点数量已达上限 %d", tc.config.MaxChildren))
		}

		// 检查层级限制
		newChildLevel := tc.localNode.Level + 1
		if newChildLevel > tc.config.MaxLevel {
			return types.NewClusterTreeManagementError(fmt.Sprintf("超出树的最大深度限制 %d", tc.config.MaxLevel))
		}

		// 添加到本地节点的子节点列表
		if !slices.Contains(tc.localNode.ChildrenIDs, childID) {
			tc.localNode.ChildrenIDs = append(tc.localNode.ChildrenIDs, childID)
		}

		// 更新子节点的父节点和层级
		child.ParentID = tc.localNode.NodeID
		child.Level = newChildLevel
	}

	// 步骤 4: 如果旧父节点是本地节点，移除子节点
	if oldParentID == tc.localNode.NodeID {
		idx := slices.Index(tc.localNode.ChildrenIDs, childID)
		if idx != -1 {
			tc.localNode.ChildrenIDs = slices.Delete(tc.localNode.ChildrenIDs, idx, idx+1)
		}
	}

	// 步骤 5: 更新拓扑统计
	tc.stats.LastTopologyUpdate.Store(time.Now())

	logging.WithFields(map[string]any{
		"child":      childID,
		"old_parent": oldParentID,
		"new_parent": newParentID,
		"new_level":  child.Level,
	}).Info("重新分配父节点成功")

	return nil
}

// GetNode 获取节点信息
func (tc *TreeCoordinator) GetNode(nodeID string) (*Node, error) {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	node, exists := tc.allNodes[nodeID]
	if !exists {
		return nil, types.NewClusterNodeNotFoundError(nodeID)
	}

	return node, nil
}

// ListNodes 列出所有节点
func (tc *TreeCoordinator) ListNodes() []*Node {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	nodes := make([]*Node, 0, len(tc.allNodes))
	for _, node := range tc.allNodes {
		nodes = append(nodes, node)
	}

	return nodes
}

// GetTreeDepth 获取树的深度
func (tc *TreeCoordinator) GetTreeDepth() int {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	maxDepth := 0
	for _, node := range tc.allNodes {
		if node.Level > maxDepth {
			maxDepth = node.Level
		}
	}

	return maxDepth
}

// ========================================
// 统计信息
// ========================================

// GetStats 获取统计信息
func (tc *TreeCoordinator) GetStats() *TreeCoordinatorStats {
	// 更新树的深度
	tc.stats.TreeDepth.Store(int32(tc.GetTreeDepth()))

	return tc.stats
}

// GetLocalNode 获取本地节点信息
func (tc *TreeCoordinator) GetLocalNode() *Node {
	return tc.localNode
}

// IsRunning 检查是否运行中
func (tc *TreeCoordinator) IsRunning() bool {
	return tc.state.Load() == int32(StateRunning)
}

// ========================================
// 路径管理（PR-037: 三级配置结构）
// ========================================

// getMetadataDir 获取元数据目录
// 返回 {base_dir}/{host_id}/metadata
// base_dir 从 clusterConfig.BaseDir 获取（支持 NEXKV_BASE_DIR 环境变量）
func (tc *TreeCoordinator) getMetadataDir() string {
	if tc.clusterConfig == nil {
		return "./data/metadata" // 降级到相对路径
	}

	baseDir := tc.clusterConfig.BaseDir

	// 展开波浪号
	if strings.HasPrefix(baseDir, "~/") {
		if homeDir, err := os.UserHomeDir(); err == nil {
			baseDir = filepath.Join(homeDir, baseDir[2:])
		}
	}

	// 使用 localNode.HostID 构建路径
	hostID := tc.localNode.HostID
	if hostID == "" {
		hostID = "default"
	}

	return filepath.Join(baseDir, hostID, "metadata")
}

// ========================================
// 动态扩缩容支持（方案 A）
// ========================================

// AddNode 添加新节点到集群（在线扩容）
//
// 动态扩容流程：
//  1. 验证新节点配置
//  2. 为新节点分配父节点（负载均衡）
//  3. 更新本地拓扑
//  4. 通过 Gossip 协议扩散拓扑变更
//  5. 触发后台数据迁移（如果需要）
func (tc *TreeCoordinator) AddNode(nodeID, addr string) error {
	if !tc.IsRunning() {
		return types.NewClusterServiceStateError("协调器", "未运行")
	}

	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	// 检查节点是否已存在
	if _, exists := tc.allNodes[nodeID]; exists {
		return types.NewClusterTreeManagementError("节点已存在: " + nodeID)
	}

	// 为新节点选择父节点（负载均衡）
	parentID, err := tc.selectParentForNewNode()
	if err != nil {
		return types.NewClusterCoordinatorError("选择父节点失败", err)
	}

	// 获取父节点信息，计算新节点的层级
	parent, exists := tc.allNodes[parentID]
	if !exists {
		return types.NewClusterNodeNotFoundError(parentID)
	}

	newNodeLevel := parent.Level + 1

	// 检查层级限制
	if newNodeLevel > tc.config.MaxLevel {
		return types.NewClusterTreeManagementError(fmt.Sprintf("超出树的最大深度限制 %d", tc.config.MaxLevel))
	}

	// 创建新节点
	parsedAddr, err := ParseNodeAddress(addr)
	if err != nil {
		return types.NewClusterCoordinatorError("解析 addr 失败", err)
	}

	newNode := &Node{
		NodeID:   nodeID,
		Addr:     *parsedAddr,
		ParentID: parentID,
		Level:    newNodeLevel,
		Status:   NodeStatusJoining,
		Metadata: make(map[string]string),
	}

	// 更新父节点的子节点列表
	if len(parent.ChildrenIDs) >= tc.config.MaxChildren {
		return types.NewClusterTreeManagementError(fmt.Sprintf("父节点 %s 子节点数已达上限 %d", parentID, tc.config.MaxChildren))
	}
	parent.ChildrenIDs = append(parent.ChildrenIDs, nodeID)

	// 添加节点到拓扑
	tc.allNodes[nodeID] = newNode
	tc.stats.TotalNodes.Add(1)
	tc.stats.OnlineNodes.Add(1)
	tc.stats.LastTopologyUpdate.Store(time.Now())

	logging.WithFields(map[string]any{
		"node_id":   nodeID,
		"addr":      addr,
		"parent_id": parentID,
		"level":     newNodeLevel,
		"max_level": tc.config.MaxLevel,
		"max_depth": tc.config.MaxLevel,
	}).Info("添加节点到集群（在线扩容）")

	// 通过 Gossip 协议扩散拓扑变更（P0 修复：内部已使用信号量控制并发）
	go tc.gossipTopologyChange("add", nodeID, parentID, newNodeLevel)
	// TODO: 如果需要数据迁移，触发后台迁移任务

	return nil
}

// RemoveNode 从集群移除节点（在线缩容）
//
// 动态缩容流程：
//  1. 验证节点存在
//  2. 将节点标记为离开中
//  3. 重新分配其子节点到其他父节点
//  4. 从拓扑中移除
//  5. 通过 Gossip 协议扩散拓扑变更
//  6. 触发后台数据迁移（如果需要）
func (tc *TreeCoordinator) RemoveNode(nodeID string) error {
	if !tc.IsRunning() {
		return types.NewClusterServiceStateError("协调器", "未运行")
	}

	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	node, exists := tc.allNodes[nodeID]
	if !exists {
		return types.NewTreeCoordinatorNodeNotFoundError(nodeID)
	}

	// 不能移除本地节点
	if nodeID == tc.localNode.NodeID {
		return types.NewClusterNodeManagementError("移除", "本地节点", nil)
	}

	// 标记为离开中
	node.Status = NodeStatusLeaving

	// 重新分配子节点
	if len(node.ChildrenIDs) > 0 {
		if err := tc.redistributeChildren(node); err != nil {
			logging.WithField("error", err).Warn("重新分配子节点失败")
			// 继续执行，不阻塞移除操作
		}
	}

	// 从父节点的子节点列表中移除
	if node.ParentID != "" {
		if parent, exists := tc.allNodes[node.ParentID]; exists {
			idx := slices.Index(parent.ChildrenIDs, nodeID)
			if idx != -1 {
				parent.ChildrenIDs = slices.Delete(parent.ChildrenIDs, idx, idx+1)
			}
		}
	}

	// 从拓扑中移除
	delete(tc.allNodes, nodeID)
	tc.stats.TotalNodes.Add(-1)
	tc.stats.OnlineNodes.Add(-1)
	tc.stats.LastTopologyUpdate.Store(time.Now())

	logging.WithFields(map[string]any{
		"node_id":        nodeID,
		"children_count": len(node.ChildrenIDs),
	}).Info("从集群移除节点（在线缩容）")

	// 通过 Gossip 协议扩散拓扑变更
	go tc.gossipTopologyChange("remove", nodeID, node.ParentID, node.Level)
	// TODO: 如果需要数据迁移，触发后台迁移任务

	return nil
}

// ScaleUp 扩容操作
//
// 批量添加节点，支持大规模扩容
func (tc *TreeCoordinator) ScaleUp(nodeIDs []string, addrs []string) error {
	if len(nodeIDs) != len(addrs) {
		return types.NewClusterTreeManagementError("节点 ID 列表和地址列表长度不一致")
	}

	successCount := 0
	var lastErr error

	for i := range nodeIDs {
		if err := tc.AddNode(nodeIDs[i], addrs[i]); err != nil {
			logging.WithFields(map[string]any{
				"node_id": nodeIDs[i],
				"error":   err,
			}).Warn("扩容：添加节点失败")
			lastErr = err
		} else {
			successCount++
		}
	}

	logging.WithFields(map[string]any{
		"requested": len(nodeIDs),
		"success":   successCount,
		"failed":    len(nodeIDs) - successCount,
	}).Info("扩容操作完成")

	if lastErr != nil && successCount == 0 {
		return types.NewClusterNodeManagementError("扩容", "", lastErr)
	}

	return nil
}

// ScaleDown 缩容操作
//
// 批量移除节点，支持大规模缩容
func (tc *TreeCoordinator) ScaleDown(nodeIDs []string) error {
	successCount := 0
	var lastErr error

	for _, nodeID := range nodeIDs {
		if err := tc.RemoveNode(nodeID); err != nil {
			logging.WithFields(map[string]any{
				"node_id": nodeID,
				"error":   err,
			}).Warn("缩容：移除节点失败")
			lastErr = err
		} else {
			successCount++
		}
	}

	logging.WithFields(map[string]any{
		"requested": len(nodeIDs),
		"success":   successCount,
		"failed":    len(nodeIDs) - successCount,
	}).Info("缩容操作完成")

	if lastErr != nil && successCount == 0 {
		return types.NewClusterNodeManagementError("缩容", "", lastErr)
	}

	return nil
}

// selectParentForNewNode 为新节点选择父节点（负载均衡）
//
// 选择策略：
//  1. 优先选择子节点数少的节点
//  2. 考虑节点层级，优先选择层级较低的节点（避免树过深）
//  3. 确保新节点不超过 MaxLevel 限制
func (tc *TreeCoordinator) selectParentForNewNode() (string, error) {
	// 如果没有节点，本地节点成为父节点
	if len(tc.allNodes) == 0 {
		return tc.localNode.NodeID, nil
	}

	var bestParent *Node
	minChildren := tc.config.MaxChildren + 1
	lowestLevel := tc.config.MaxLevel + 1

	for _, node := range tc.allNodes {
		// 只考虑就绪状态的节点
		if node.Status != NodeStatusReady {
			continue
		}

		// 检查层级限制：该节点的子节点不能超过 MaxLevel
		if node.Level >= tc.config.MaxLevel {
			continue // 跳过已达到最大层级的节点
		}

		childrenCount := len(node.ChildrenIDs)

		// 优先选择层级较低的节点
		if bestParent == nil || node.Level < lowestLevel {
			bestParent = node
			minChildren = childrenCount
			lowestLevel = node.Level

			// 如果找到既层级低又有空位的节点，直接使用
			if childrenCount == 0 && node.Level < tc.config.MaxLevel {
				break
			}
			continue
		}

		// 相同层级下，选择子节点数少的节点
		if node.Level == lowestLevel && childrenCount < minChildren {
			bestParent = node
			minChildren = childrenCount

			// 如果找到有空位的节点，直接使用
			if childrenCount == 0 {
				break
			}
		}
	}

	if bestParent == nil {
		return "", types.NewClusterTreeManagementError(fmt.Sprintf("没有可用的父节点（可能已达到树的最大深度 %d）", tc.config.MaxLevel))
	}

	logging.WithFields(map[string]any{
		"parent_id":      bestParent.NodeID,
		"parent_level":   bestParent.Level,
		"children_count": len(bestParent.ChildrenIDs),
		"max_level":      tc.config.MaxLevel,
	}).Debug("为新节点选择父节点")

	return bestParent.NodeID, nil
}

// redistributeChildren 重新分配子节点
//
// 当父节点被移除时，将其子节点重新分配给其他节点
func (tc *TreeCoordinator) redistributeChildren(parentNode *Node) error {
	for _, childID := range parentNode.ChildrenIDs {
		child, exists := tc.allNodes[childID]
		if !exists {
			continue
		}

		// 为子节点选择新的父节点
		newParentID, err := tc.selectParentForNewNode()
		if err != nil {
			logging.WithFields(map[string]any{
				"child_id": childID,
				"error":    err,
			}).Warn("重新分配子节点：选择新父节点失败")
			continue
		}

		// 更新子节点的父节点
		oldParentID := child.ParentID
		child.ParentID = newParentID

		// 更新新父节点的子节点列表
		if newParent, exists := tc.allNodes[newParentID]; exists {
			newParent.ChildrenIDs = append(newParent.ChildrenIDs, childID)
		}

		logging.WithFields(map[string]any{
			"child_id":   childID,
			"old_parent": oldParentID,
			"new_parent": newParentID,
		}).Info("重新分配子节点")
	}

	return nil
}

// GetTopology 获取当前拓扑结构
//
// 返回所有节点及其关系，用于：
//   - 监控和可视化
//   - 拓扑同步
//   - 故障恢复
func (tc *TreeCoordinator) GetTopology() map[string]*Node {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	// 深拷贝节点信息
	topology := make(map[string]*Node, len(tc.allNodes))
	for nodeID, node := range tc.allNodes {
		nodeCopy := *node
		nodeCopy.ChildrenIDs = make([]string, len(node.ChildrenIDs))
		copy(nodeCopy.ChildrenIDs, node.ChildrenIDs)

		if node.Metadata != nil {
			nodeCopy.Metadata = make(map[string]string, len(node.Metadata))
			maps.Copy(nodeCopy.Metadata, node.Metadata)
		}

		topology[nodeID] = &nodeCopy
	}

	return topology
}

// ========================================
// PR-Libp2p-RPC: 辅助函数
// ========================================

// addrToPeerID 将地址字符串转换为 peer.ID
// 注意：这是简化实现，仅用于测试
// 实际使用中需要维护 NodeID -> peer.ID 的映射
func (tc *TreeCoordinator) addrToPeerID(addr string) peer.ID {
	// 简化实现：如果地址是 libp2p peer ID 格式，直接解析
	// 否则返回一个固定的 peer.ID 用于测试
	if strings.HasPrefix(addr, "12D3KooW") {
		return peer.ID(addr)
	}
	// 返回一个固定的 peer.ID 用于测试
	return peer.ID("12D3KooWxFyaGzsVZYgVHQGVrKnwEFzWwSsYiAtHoSKjAPd1YgJq")
}

// ============================================================================
// PR-033: Host/Node 双层架构扩展
// ============================================================================

// HostStatus 物理机器状态（PR-033）
type HostStatus int

const (
	HostStatusOffline  HostStatus = iota // 离线
	HostStatusOnline                     // 在线
	HostStatusDegraded                   // 降级（部分功能异常）
)

// String 返回 HostStatus 的字符串表示
func (s HostStatus) String() string {
	switch s {
	case HostStatusOffline:
		return "Offline"
	case HostStatusOnline:
		return "Online"
	case HostStatusDegraded:
		return "Degraded"
	default:
		return "Unknown"
	}
}

// Validate 验证 NodeAddress 的合法性（PR-033）
// 规则：
//  1. TCPPort 和 UDPPort 都在有效范围内 [1024, 65535]
//  2. 如果两个端口都设置，UDPPort 应该等于 TCPPort + 1
//  3. 至少有一个端口已设置
func (na *NodeAddress) Validate() error {
	const (
		MinPort    = 1024
		MaxTCPPort = 65534
		MaxUDPPort = 65535
	)

	// 检查 TCP 端口范围
	if na.TCPPort != 0 {
		if na.TCPPort < MinPort || na.TCPPort > MaxTCPPort {
			return types.NewTreeCoordinatorTCPPortOutOfRangeError(MinPort, MaxTCPPort, na.TCPPort)
		}
	}

	// 检查 UDP 端口范围
	if na.UDPPort != 0 {
		if na.UDPPort < MinPort || na.UDPPort > MaxUDPPort {
			return types.NewTreeCoordinatorUDPPortOutOfRangeError(MinPort, MaxUDPPort, na.UDPPort)
		}
	}

	// 检查 UDP = TCP + 1 规则（如果两个端口都设置）
	if na.TCPPort != 0 && na.UDPPort != 0 {
		if na.UDPPort != na.TCPPort+1 {
			return types.NewTreeCoordinatorUDPPortMustBeTCPPlusOneError(na.TCPPort, na.UDPPort)
		}
	}

	// 至少需要设置一个端口
	if na.TCPPort == 0 && na.UDPPort == 0 {
		return types.NewTreeCoordinatorAtLeastOnePortRequiredError()
	}

	return nil
}

// GetTCPAddr 获取完整的 TCP 网络地址（PR-033）
// 格式：hostname:port（如 "192.168.1.100:9000"）
// 如果 Host 为空，返回 ":port"
func (na *NodeAddress) GetTCPAddr() string {
	if na.Host == "" {
		return fmt.Sprintf(":%d", na.TCPPort)
	}
	return fmt.Sprintf("%s:%d", na.Host, na.TCPPort)
}

// GetUDPAddr 获取完整的 UDP 网络地址（PR-033）
// 格式：hostname:port（如 "192.168.1.100:9001"）
// 如果 Host 为空，返回 ":port"
func (na *NodeAddress) GetUDPAddr() string {
	if na.Host == "" {
		return fmt.Sprintf(":%d", na.UDPPort)
	}
	return fmt.Sprintf("%s:%d", na.Host, na.UDPPort)
}

// NewNodeAddress 创建新的 NodeAddress（PR-033）
// 自动设置 UDPPort = TCPPort + 1
func NewNodeAddress(host string, tcpPort int) (*NodeAddress, error) {
	const (
		MinPort    = 1024
		MaxTCPPort = 65534
	)

	if tcpPort < MinPort || tcpPort > MaxTCPPort {
		return nil, types.NewTreeCoordinatorTCPPortOutOfRangeError(MinPort, MaxTCPPort, tcpPort)
	}

	// UDP 端口自动 = TCP + 1
	udpPort := tcpPort + 1

	return &NodeAddress{
		Host:    host,
		TCPPort: tcpPort,
		UDPPort: udpPort,
	}, nil
}

// GetTCPAddr 获取节点的 TCP 网络地址（PR-033）
// 格式：hostname:port
func (n *Node) GetTCPAddr() string {
	return n.Addr.GetTCPAddr()
}

// GetUDPAddr 获取节点的 UDP 网络地址（PR-033）
// 格式：hostname:port
func (n *Node) GetUDPAddr() string {
	return n.Addr.GetUDPAddr()
}

// Validate 验证 Node 结构的完整性（PR-033）
func (n *Node) Validate() error {
	// 验证 NodeID
	if n.NodeID == "" {
		return types.NewTreeCoordinatorNodeIDRequiredError()
	}

	// 验证 HostID
	if n.HostID == "" {
		return types.NewTreeCoordinatorHostIDRequiredError()
	}

	// 验证 Addr
	if err := n.Addr.Validate(); err != nil {
		return types.NewTreeCoordinatorInvalidNodeAddrError(err)
	}

	// 验证 Role
	switch n.Role {
	case Leaf, Parent, ParentStandby:
		// 有效角色
	default:
		return types.NewTreeCoordinatorInvalidNodeRoleError(int(n.Role))
	}

	return nil
}

// IsLeaf 判断是否为叶子节点（PR-033）
func (n *Node) IsLeaf() bool {
	return n.Role == Leaf
}

// IsParent 判断是否为父节点（PR-033）
func (n *Node) IsParent() bool {
	return n.Role == Parent
}

// IsParentStandby 判断是否为父热备节点（PR-033）
func (n *Node) IsParentStandby() bool {
	return n.Role == ParentStandby
}

// IsOnline 判断节点是否在线（PR-033）
func (n *Node) IsOnline() bool {
	return n.Status == NodeStatusReady || n.Status == NodeStatusInit
}

// ============================================================================
// PR-033: Host 扩展方法
// ============================================================================

// ValidateNodeIDs 验证 HostRole 到 NodeID 的约束关系（PR-033）
//
// 约束规则：
//   - LeafOnly: 必须有 LeafNodeID
//   - LeafParent: 必须有 LeafNodeID 和 ParentNodeID
//   - LeafParentStandby: 必须有 LeafNodeID 和 ParentStandbyNodeID
func (h *Host) ValidateNodeIDs() error {
	if h.HostID == "" {
		return types.NewTreeCoordinatorHostIDRequiredError()
	}

	switch h.Role {
	case LeafOnly:
		if h.LeafNodeID == "" {
			return types.NewTreeCoordinatorLeafNodeIDRequiredError()
		}
		// ParentNodeID 和 ParentStandbyNodeID 应该为空
		if h.ParentNodeID != "" {
			return types.NewTreeCoordinatorLeafOnlyHostShouldNotHaveParentNodeIDError()
		}
		if h.ParentStandbyNodeID != "" {
			return types.NewTreeCoordinatorLeafOnlyHostShouldNotHaveParentStandbyNodeIDError()
		}

	case LeafParent:
		if h.LeafNodeID == "" {
			return types.NewTreeCoordinatorLeafNodeIDRequiredError()
		}
		if h.ParentNodeID == "" {
			return types.NewTreeCoordinatorParentNodeIDRequiredError()
		}
		// ParentStandbyNodeID 可选
		if h.ParentStandbyNodeID == h.ParentNodeID {
			return types.NewTreeCoordinatorParentStandbyNodeIDMustBeDifferentError()
		}

	case LeafParentStandby:
		if h.LeafNodeID == "" {
			return types.NewTreeCoordinatorLeafNodeIDRequiredError()
		}
		if h.ParentStandbyNodeID == "" {
			return types.NewTreeCoordinatorParentStandbyNodeIDRequiredError()
		}
		// ParentNodeID 可选

	default:
		return types.NewTreeCoordinatorInvalidHostRoleError(int(h.Role))
	}

	return nil
}

// IsOnline 判断 Host 是否在线（PR-033）
func (h *Host) IsOnline() bool {
	return h.HostStatus == HostStatusOnline
}

// IsDegraded 判断 Host 是否降级（PR-033）
func (h *Host) IsDegraded() bool {
	return h.HostStatus == HostStatusDegraded
}

// ========================================
// Gossip 拓扑变更扩散 (PR-034)
// ========================================

// gossipTopologyChange 通过 Gossip 协议扩散拓扑变更
//
// PR-Libp2p-RPC: 使用新的 RPC 框架发送拓扑变更消息
//
// PR-034 实现：
//  1. 随机选择一些节点进行 Gossip
//  2. 发送拓扑变更消息
//  3. 最终所有节点都会达到一致的拓扑状态
//
// 参数：
//   - operation: 操作类型（"add", "remove", "reparent"）
//   - nodeID: 发生变更的节点 ID
//   - parentID: 父节点 ID（如果有）
//   - level: 节点层级
func (tc *TreeCoordinator) gossipTopologyChange(operation, nodeID, parentID string, level int) {
	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		logging.WithFields(map[string]any{
			"operation": operation,
			"node_id":   nodeID,
		}).Debug("⚠️ RPC 客户端未初始化，跳过拓扑变更扩散")
		return
	}

	// 生成版本号
	version := uint64(time.Now().UnixNano())

	// 构造拓扑变更请求
	gossipReq := rpc.NewGossipTopologyChangeRequest(operation, nodeID, parentID, level, version)

	// 序列化请求体
	reqBody, err := msgpack.Marshal(gossipReq)
	if err != nil {
		logging.WithField("error", err).Error("序列化拓扑变更请求失败")
		return
	}

	// 向所有已知节点发送 Gossip 消息
	tc.nodesMu.RLock()
	nodes := make([]*Node, 0, len(tc.allNodes))
	for _, node := range tc.allNodes {
		if node.NodeID != tc.localNode.NodeID && node.Status == NodeStatusReady {
			nodes = append(nodes, node)
		}
	}
	tc.nodesMu.RUnlock()

	// P0 修复：使用信号量限制并发 goroutine 数量
	for _, node := range nodes {
		tc.gossipSemaphore <- struct{}{}
		go func(n *Node) {
			defer func() { <-tc.gossipSemaphore }()
			if err := tc.sendGossipMessage(n, reqBody); err != nil {
				logging.WithField("error", err).WithField("node", n.NodeID).Debug("发送 Gossip 消息失败")
			}
		}(node)
	}
}

// sendGossipMessage 发送 Gossip 消息到指定节点
//
// PR-Libp2p-RPC: 使用新的 RPC 框架发送 Gossip 消息
func (tc *TreeCoordinator) sendGossipMessage(targetNode *Node, reqBody []byte) error {
	// 检查 RPC 客户端是否可用
	if tc.rpcClient == nil {
		return nil
	}

	// 将目标节点地址转换为 peer.ID
	targetPeerID := tc.addrToPeerID(targetNode.Addr.TCPAddr())

	// 发送 RPC 请求
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := tc.rpcClient.Call(ctx, targetPeerID, "GossipTopologyChange", reqBody)
	if err != nil {
		logging.WithFields(map[string]any{
			"target_node": targetNode.NodeID,
			"error":       err,
		}).Debug("发送 Gossip 消息失败")
		return err
	}

	logging.WithField("target_node", targetNode.NodeID).Debug("发送 Gossip 消息成功")
	return nil
}

// ========================================
// Gossip 周期性同步
// ========================================

// buildTopologyMetadata 构造拓扑元数据
func (tc *TreeCoordinator) buildTopologyMetadata() map[string][]byte {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	metadata := make(map[string][]byte)

	// 添加本地节点信息
	localNodeData := fmt.Sprintf("%s|%s|%s|%d|%s",
		tc.localNode.NodeID,
		tc.localNode.Addr.TCPAddr(),
		tc.localNode.ParentID,
		tc.localNode.Level,
		tc.localNode.Status.String())
	metadata[tc.localNode.NodeID] = []byte(localNodeData)

	// 添加子节点信息
	for _, childID := range tc.localNode.ChildrenIDs {
		if child, exists := tc.allNodes[childID]; exists {
			childData := fmt.Sprintf("%s|%s|%s|%d|%s",
				child.NodeID,
				child.Addr.TCPAddr(),
				child.ParentID,
				child.Level,
				child.Status.String())
			metadata[child.NodeID] = []byte(childData)
		}
	}

	return metadata
}
