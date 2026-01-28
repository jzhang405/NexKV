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
	"maps"
	"slices"

	"github.com/jzhang405/NexKV/internal/metadata/types"
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
//
// RPC 架构（PR-032）：
//   - RPCClient: 主动调用其他节点的 RPC 方法
//   - RPCServer: 接收并处理其他节点的 RPC 请求
type TreeCoordinator struct {
	// 配置
	config *TreeCoordinatorConfig

	// 本地节点信息
	localNode *Node

	// RPC 组件（PR-032 架构）
	RPCClient *transport.RPCClient // RPC 客户端（主动调用）
	RPCServer *transport.RPCServer // RPC 服务端（接收请求）

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
	config *TreeCoordinatorConfig,
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
		return types.NewClusterServiceStateError("树形协调器", "已经启动")
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

	// 添加子节点
	tc.localNode.ChildrenIDs = append(tc.localNode.ChildrenIDs, childID)

	// 更新子节点信息
	if child, exists := tc.allNodes[childID]; exists {
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
	newNode := &Node{
		NodeID:   nodeID,
		Addr:     addr,
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

	// TODO: 通过 Gossip 协议扩散拓扑变更
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
		return fmt.Errorf("节点不存在: %s", nodeID)
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

	// TODO: 通过 Gossip 协议扩散拓扑变更
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
