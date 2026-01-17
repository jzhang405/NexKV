// Package cluster 提供节点管理层实现
//
// 包含：
//   - 树形协调器：层级化管理，每父最多 10 个子节点
//   - Leader 选举：基于优先级和节点的选举机制
//   - 故障检测：心跳机制检测节点存活
//   - 自愈机制：节点重启、重新找父
package cluster

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/transport"
)

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
type TreeCoordinator struct {
	// 配置
	config *TreeCoordinatorConfig

	// 本地节点信息
	localNode *Node

	// 传输层
	transport transport.Transport

	// 节点管理
	allNodes map[string]*Node
	nodesMu  sync.RWMutex

	// 状态管理
	state atomic.Int32 // CoordinatorState

	// 统计信息
	stats *TreeCoordinatorStats

	// 生命周期
	started atomic.Bool
	stopped atomic.Bool
	stopCh  chan struct{}
}

// TreeCoordinatorConfig 树形协调器配置
type TreeCoordinatorConfig struct {
	// MaxChildren 最大子节点数（默认 10）
	MaxChildren int

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

	// Addr 节点地址
	Addr string

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
func NewTreeCoordinator(
	localNodeID string,
	localAddr string,
	transport transport.Transport,
	config *TreeCoordinatorConfig,
) (*TreeCoordinator, error) {
	if config == nil {
		config = DefaultTreeCoordinatorConfig()
	}

	if transport == nil {
		return nil, fmt.Errorf("transport 不能为空")
	}

	if localNodeID == "" {
		return nil, fmt.Errorf("localNodeID 不能为空")
	}

	if localAddr == "" {
		return nil, fmt.Errorf("localAddr 不能为空")
	}

	// 创建本地节点
	localNode := &Node{
		NodeID:      localNodeID,
		Addr:        localAddr,
		ParentID:    "", // 初始无父节点
		ChildrenIDs: make([]string, 0),
		Level:       0,
		Status:      NodeStatusInit,
		Priority:    0,
		Metadata:    make(map[string]string),
	}

	coordinator := &TreeCoordinator{
		config:    config,
		localNode: localNode,
		transport: transport,
		allNodes:  make(map[string]*Node),
		stopCh:    make(chan struct{}),
		stats:     &TreeCoordinatorStats{},
	}

	// 添加本地节点
	coordinator.allNodes[localNodeID] = localNode
	coordinator.stats.TotalNodes.Store(1)
	coordinator.stats.OnlineNodes.Store(1)
	coordinator.stats.LastTopologyUpdate.Store(time.Now())

	return coordinator, nil
}

// Start 启动树形协调器
func (tc *TreeCoordinator) Start() error {
	if !tc.started.CompareAndSwap(false, true) {
		return fmt.Errorf("树形协调器已经启动")
	}

	tc.state.Store(int32(StateStarting))

	logging.WithFields(map[string]any{
		"node_id":        tc.localNode.NodeID,
		"max_children":   tc.config.MaxChildren,
		"auto_discovery": tc.config.AutoDiscovery,
	}).Info("启动树形协调器")

	// 标记本地节点就绪
	tc.localNode.Status = NodeStatusReady
	tc.localNode.LastHeartbeat = time.Now()

	// 如果启用自动发现，开始寻找父节点
	if tc.config.AutoDiscovery {
		go tc.discoverAndJoin()
	}

	// 启动心跳循环
	go tc.heartbeatLoop()

	// 启动故障检测循环
	go tc.failureDetectionLoop()

	tc.state.Store(int32(StateRunning))
	tc.started.Store(true)

	logging.WithField("node_id", tc.localNode.NodeID).Info("树形协调器启动成功")
	return nil
}

// Stop 停止树形协调器
func (tc *TreeCoordinator) Stop() error {
	if !tc.stopped.CompareAndSwap(false, true) {
		return nil // 已经停止
	}

	tc.state.Store(int32(StateStopping))

	logging.WithField("node_id", tc.localNode.NodeID).Info("停止树形协调器...")

	// 关闭停止信号
	close(tc.stopCh)

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
func (tc *TreeCoordinator) discoverAndJoin() {
	// TODO: 实现自动发现和加入逻辑
	// 1. 通过传输层发现可用节点
	// 2. 选择合适的父节点
	// 3. 发送加入请求
	// 4. 更新本地节点信息
	logging.Debug("自动发现并加入树形结构")
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
func (tc *TreeCoordinator) sendHeartbeat() {
	// 更新本地节点心跳时间
	tc.localNode.LastHeartbeat = time.Now()

	// TODO: 向父节点和子节点发送心跳
	logging.WithFields(map[string]any{
		"node_id": tc.localNode.NodeID,
		"status":  tc.localNode.Status,
	}).Debug("发送心跳")
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
func (tc *TreeCoordinator) triggerSelfHealing(failedNode *Node) {
	// TODO: 实现自愈逻辑
	// 1. 移除故障节点的父子关系
	// 2. 子节点重新寻找父节点
	// 3. 更新树形拓扑
	logging.WithFields(map[string]any{
		"failed_node": failedNode.NodeID,
		"level":       failedNode.Level,
	}).Info("触发自愈机制")
}

// leaveTree 离开树形结构
func (tc *TreeCoordinator) leaveTree() {
	tc.localNode.Status = NodeStatusLeaving

	// TODO: 通知父节点和子节点
	logging.WithField("node_id", tc.localNode.NodeID).Info("离开树形结构")
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
		return fmt.Errorf("子节点数量已达上限 %d", tc.config.MaxChildren)
	}

	// 检查是否已存在
	for _, cid := range tc.localNode.ChildrenIDs {
		if cid == childID {
			return fmt.Errorf("子节点已存在: %s", childID)
		}
	}

	// 检查 child 是否已经有父节点（确保一个真实节点只能有一个 ParentID）
	if child, exists := tc.allNodes[childID]; exists {
		// 如果 child 已经有父节点，且不是当前节点
		if child.ParentID != "" && child.ParentID != tc.localNode.NodeID {
			return fmt.Errorf("节点 %s 已经是 %s 的子节点，不能同时作为 %s 的子节点",
				childID, child.ParentID, tc.localNode.NodeID)
		}
	}

	// 添加子节点
	tc.localNode.ChildrenIDs = append(tc.localNode.ChildrenIDs, childID)

	// 更新子节点信息
	if child, exists := tc.allNodes[childID]; exists {
		child.ParentID = tc.localNode.NodeID
		child.Level = tc.localNode.Level + 1
	}

	tc.stats.LastTopologyUpdate.Store(time.Now())

	logging.WithFields(map[string]any{
		"parent": tc.localNode.NodeID,
		"child":  childID,
		"level":  tc.localNode.Level + 1,
	}).Info("添加子节点")

	return nil
}

// RemoveChild 移除子节点
func (tc *TreeCoordinator) RemoveChild(childID string) error {
	tc.nodesMu.Lock()
	defer tc.nodesMu.Unlock()

	// 查找并移除子节点
	newChildren := make([]string, 0, len(tc.localNode.ChildrenIDs))
	found := false

	for _, cid := range tc.localNode.ChildrenIDs {
		if cid == childID {
			found = true
			continue
		}
		newChildren = append(newChildren, cid)
	}

	if !found {
		return fmt.Errorf("子节点不存在: %s", childID)
	}

	tc.localNode.ChildrenIDs = newChildren

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

// GetNode 获取节点信息
func (tc *TreeCoordinator) GetNode(nodeID string) (*Node, error) {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	node, exists := tc.allNodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("节点不存在: %s", nodeID)
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
