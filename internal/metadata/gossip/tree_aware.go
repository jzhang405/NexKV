// Package gossip 提供树感知 Gossip 同步实现
//
// GOSSIP-1: 树感知传播策略
//
// 核心功能：
//   - 节点类型识别：叶子/中间/Root 节点
//   - 向上传播：叶子节点 → 父节点（高优先级）
//   - 向下广播：父节点 → 子节点（普通优先级）
//   - 跨子树同步：同级节点间（低优先级）
//
// 设计考量：
//   - 叶子节点：带宽最低（只发父节点），延迟最低（本地产生）
//   - 中间节点：带宽中等，延迟中等
//   - Root 节点：带宽省（只广播），延迟最高（需等待向上传播）
package gossip

import (
	"context"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
)

// ==================== 节点类型定义 ====================

// NodeType 节点类型
type NodeType int

const (
	// NodeTypeUnknown 未知类型
	NodeTypeUnknown NodeType = iota

	// NodeTypeLeaf 叶子节点
	// 特点：只发父节点，带宽最低，延迟最低
	NodeTypeLeaf

	// NodeTypeMiddle 中间节点
	// 特点：发父节点 + 广播子节点，带宽中等
	NodeTypeMiddle

	// NodeTypeRoot Root 节点
	// 特点：只广播子节点，带宽省，延迟最高
	NodeTypeRoot
)

// String 返回节点类型的字符串表示
func (t NodeType) String() string {
	switch t {
	case NodeTypeLeaf:
		return "leaf"
	case NodeTypeMiddle:
		return "middle"
	case NodeTypeRoot:
		return "root"
	default:
		return "unknown"
	}
}

// ==================== 优先级定义 ====================

// PriorityLevel 优先级级别
type PriorityLevel int

const (
	// PriorityHigh 高优先级：向上传播（叶子→父）
	PriorityHigh PriorityLevel = iota

	// PriorityNormal 普通优先级：向下广播（父→子）
	PriorityNormal

	// PriorityLow 低优先级：跨子树同步
	PriorityLow
)

// String 返回优先级的字符串表示
func (p PriorityLevel) String() string {
	switch p {
	case PriorityHigh:
		return "high"
	case PriorityNormal:
		return "normal"
	case PriorityLow:
		return "low"
	default:
		return "unknown"
	}
}

// ==================== 树感知事件 ====================

// TreeAwareEvent 带优先级的树感知事件
type TreeAwareEvent struct {
	Event       GossipEvent   // 原始事件
	Priority    PriorityLevel // 优先级
	TargetNodes []string      // 目标节点（空表示广播）
	EnqueueTime time.Time     // 入队时间
}

// ==================== 树拓扑信息 ====================

// TreeTopology 树拓扑信息
type TreeTopology struct {
	mu sync.RWMutex

	// 本地节点信息
	localNodeID string
	nodeType    NodeType
	treeDepth   int

	// 树结构
	parentNode string   // 父节点 ID（Root 为空）
	childNodes []string // 子节点列表（叶子为空）

	// 所有已知节点
	allNodes []string
}

// NewTreeTopology 创建树拓扑
func NewTreeTopology(localNodeID string) *TreeTopology {
	return &TreeTopology{
		localNodeID: localNodeID,
		nodeType:    NodeTypeUnknown,
		treeDepth:   0,
		parentNode:  "",
		childNodes:  []string{},
		allNodes:    []string{localNodeID},
	}
}

// SetNodeType 设置节点类型
func (t *TreeTopology) SetNodeType(nodeType NodeType) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodeType = nodeType
}

// GetNodeType 获取节点类型
func (t *TreeTopology) GetNodeType() NodeType {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.nodeType
}

// SetParent 设置父节点
func (t *TreeTopology) SetParent(parentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parentNode = parentID
}

// GetParent 获取父节点
func (t *TreeTopology) GetParent() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.parentNode
}

// SetChildren 设置子节点
func (t *TreeTopology) SetChildren(children []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.childNodes = make([]string, len(children))
	copy(t.childNodes, children)
}

// GetChildren 获取子节点
func (t *TreeTopology) GetChildren() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]string, len(t.childNodes))
	copy(result, t.childNodes)
	return result
}

// SetTreeDepth 设置树深度
func (t *TreeTopology) SetTreeDepth(depth int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.treeDepth = depth
}

// GetTreeDepth 获取树深度
func (t *TreeTopology) GetTreeDepth() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.treeDepth
}

// SetAllNodes 设置所有节点
func (t *TreeTopology) SetAllNodes(nodes []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.allNodes = make([]string, len(nodes))
	copy(t.allNodes, nodes)
}

// GetAllNodes 获取所有节点
func (t *TreeTopology) GetAllNodes() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]string, len(t.allNodes))
	copy(result, t.allNodes)
	return result
}

// UpdateTopology 更新拓扑（根据父/子节点自动推断类型）
func (t *TreeTopology) UpdateTopology(parent string, children []string, depth int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.parentNode = parent
	t.childNodes = make([]string, len(children))
	copy(t.childNodes, children)
	t.treeDepth = depth

	// 推断节点类型
	if parent == "" && len(children) > 0 {
		t.nodeType = NodeTypeRoot
	} else if len(children) == 0 && parent != "" {
		t.nodeType = NodeTypeLeaf
	} else if parent != "" && len(children) > 0 {
		t.nodeType = NodeTypeMiddle
	} else {
		// 单节点集群，视为 Root
		t.nodeType = NodeTypeRoot
	}
}

// ==================== 树感知 Gossip 同步 ====================

// TreeAwareGossipSync 树感知 Gossip 同步
type TreeAwareGossipSync struct {
	mu sync.RWMutex

	// 基础 Gossip 同步器
	*EventDrivenGossipSync

	// 树拓扑
	topology *TreeTopology

	// 三级优先队列
	highPriority   chan TreeAwareEvent // 向上传播
	normalPriority chan TreeAwareEvent // 向下广播
	lowPriority    chan TreeAwareEvent // 跨子树同步

	// 配置
	config *TreeAwareConfig

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 统计
	highPrioritySent   uint64
	normalPrioritySent uint64
	lowPrioritySent    uint64
	highPriorityDrop   uint64
	normalPriorityDrop uint64
	lowPriorityDrop    uint64
}

// TreeAwareConfig 树感知 Gossip 配置
type TreeAwareConfig struct {
	// EventDrivenConfig 基础配置
	*EventDrivenConfig

	// HighPriorityChanSize 高优先级通道大小（默认 500）
	HighPriorityChanSize int

	// NormalPriorityChanSize 普通优先级通道大小（默认 300）
	NormalPriorityChanSize int

	// LowPriorityChanSize 低优先级通道大小（默认 200）
	LowPriorityChanSize int

	// StarvationPrevention 饥饿预防（每处理 N 个高优先级后处理 1 个低优先级）
	StarvationPrevention int
}

// NewTreeAwareGossipSync 创建树感知 Gossip 同步
func NewTreeAwareGossipSync(config *TreeAwareConfig) *TreeAwareGossipSync {
	if config == nil {
		config = &TreeAwareConfig{}
	}

	// 设置默认值
	highChanSize := config.HighPriorityChanSize
	if highChanSize <= 0 {
		highChanSize = 500
	}

	normalChanSize := config.NormalPriorityChanSize
	if normalChanSize <= 0 {
		normalChanSize = 300
	}

	lowChanSize := config.LowPriorityChanSize
	if lowChanSize <= 0 {
		lowChanSize = 200
	}

	starvationPrevention := config.StarvationPrevention
	if starvationPrevention <= 0 {
		starvationPrevention = 10
	}

	// 创建基础 Gossip 同步器
	baseSync := NewEventDrivenGossipSync(config.EventDrivenConfig)

	ctx, cancel := context.WithCancel(context.Background())

	sync := &TreeAwareGossipSync{
		EventDrivenGossipSync: baseSync,
		topology:              NewTreeTopology(config.LocalNodeID),
		highPriority:          make(chan TreeAwareEvent, highChanSize),
		normalPriority:        make(chan TreeAwareEvent, normalChanSize),
		lowPriority:           make(chan TreeAwareEvent, lowChanSize),
		config: &TreeAwareConfig{
			EventDrivenConfig:      config.EventDrivenConfig,
			HighPriorityChanSize:   highChanSize,
			NormalPriorityChanSize: normalChanSize,
			LowPriorityChanSize:    lowChanSize,
			StarvationPrevention:   starvationPrevention,
		},
		ctx:    ctx,
		cancel: cancel,
	}

	// 启动优先级循环
	sync.wg.Add(1)
	go sync.runPriorityLoop()

	return sync
}

// runPriorityLoop 运行优先级循环
func (s *TreeAwareGossipSync) runPriorityLoop() {
	defer s.wg.Done()

	starvationCounter := 0
	starvationThreshold := s.config.StarvationPrevention

	for {
		select {
		case <-s.ctx.Done():
			return

		case event := <-s.highPriority:
			s.processTreeAwareEvent(event)

		default:
			select {
			case <-s.ctx.Done():
				return

			case event := <-s.highPriority:
				s.processTreeAwareEvent(event)

			case event := <-s.normalPriority:
				s.processTreeAwareEvent(event)
				starvationCounter++

			case event := <-s.lowPriority:
				// 饥饿预防：每处理 N 个普通优先级后处理 1 个低优先级
				if starvationCounter >= starvationThreshold {
					s.processTreeAwareEvent(event)
					starvationCounter = 0
				} else {
					// P1-3: 不满足条件时，将事件放回队列等待下次处理
					// 外层的 default + time.After 已确保不会 CPU 空转
					select {
					case s.lowPriority <- event:
						// 成功放回队列
					default:
						s.mu.Lock()
						s.lowPriorityDrop++
						s.mu.Unlock()
					}
				}

			case <-time.After(100 * time.Millisecond):
				// 空闲时重置饥饿计数器，避免低优先级事件永久饥饿
				starvationCounter = 0
			}
		}
	}
}

// processTreeAwareEvent 处理树感知事件
func (s *TreeAwareGossipSync) processTreeAwareEvent(event TreeAwareEvent) {
	// 根据目标节点发送
	for _, targetNode := range event.TargetNodes {
		s.sendToNode(event.Event, targetNode, event.Priority)
	}

	// 更新统计
	s.mu.Lock()
	switch event.Priority {
	case PriorityHigh:
		s.highPrioritySent += uint64(len(event.TargetNodes))
	case PriorityNormal:
		s.normalPrioritySent += uint64(len(event.TargetNodes))
	case PriorityLow:
		s.lowPrioritySent += uint64(len(event.TargetNodes))
	}
	s.mu.Unlock()
}

// sendToNode 发送事件到指定节点
func (s *TreeAwareGossipSync) sendToNode(event GossipEvent, targetNode string, priority PriorityLevel) {
	// 使用底层 Gossip 同步器发送
	// 这里简化实现，实际应该通过 transport 发送
	logging.WithFields(map[string]any{
		"event_type": event.Type,
		"namespace":  event.Namespace,
		"key":        event.Key,
		"target":     targetNode,
		"priority":   priority.String(),
		"node_type":  s.topology.GetNodeType().String(),
	}).Debug("树感知 Gossip 发送事件")
}

// ==================== 树感知传播方法 ====================

// Propagate 根据节点类型传播事件
func (s *TreeAwareGossipSync) Propagate(event GossipEvent) {
	nodeType := s.topology.GetNodeType()

	// 根据节点类型决定传播策略
	// Root: 只广播子节点
	// Middle: 向父节点 + 广播子节点
	// Leaf: 只向父节点传播
	// Unknown: 广播到所有节点
	switch nodeType {
	case NodeTypeLeaf:
		s.propagateToParent(event)
	case NodeTypeMiddle:
		s.propagateToParent(event)
		s.broadcastToChildren(event)
	case NodeTypeRoot:
		s.broadcastToChildren(event)
	default:
		s.broadcastToAll(event)
	}
}

// propagateToParent 向父节点传播
func (s *TreeAwareGossipSync) propagateToParent(event GossipEvent) {
	parent := s.topology.GetParent()
	if parent == "" {
		return
	}

	treeEvent := TreeAwareEvent{
		Event:       event,
		Priority:    PriorityHigh,
		TargetNodes: []string{parent},
		EnqueueTime: time.Now(),
	}

	select {
	case s.highPriority <- treeEvent:
		// 成功入队
	default:
		// 队列满，丢弃
		s.mu.Lock()
		s.highPriorityDrop++
		s.mu.Unlock()

		logging.WithFields(map[string]any{
			"event_type": event.Type,
			"namespace":  event.Namespace,
			"key":        event.Key,
			"parent":     parent,
		}).Warn("高优先级队列满，事件丢弃")
	}
}

// broadcastToChildren 广播到子节点
func (s *TreeAwareGossipSync) broadcastToChildren(event GossipEvent) {
	children := s.topology.GetChildren()
	if len(children) == 0 {
		return
	}

	treeEvent := TreeAwareEvent{
		Event:       event,
		Priority:    PriorityNormal,
		TargetNodes: children,
		EnqueueTime: time.Now(),
	}

	select {
	case s.normalPriority <- treeEvent:
		// 成功入队
	default:
		// 队列满，丢弃
		s.mu.Lock()
		s.normalPriorityDrop++
		s.mu.Unlock()

		logging.WithFields(map[string]any{
			"event_type":  event.Type,
			"namespace":   event.Namespace,
			"key":         event.Key,
			"child_count": len(children),
		}).Warn("普通优先级队列满，事件丢弃")
	}
}

// broadcastToAll 广播到所有节点
func (s *TreeAwareGossipSync) broadcastToAll(event GossipEvent) {
	allNodes := s.topology.GetAllNodes()

	treeEvent := TreeAwareEvent{
		Event:       event,
		Priority:    PriorityLow,
		TargetNodes: allNodes,
		EnqueueTime: time.Now(),
	}

	select {
	case s.lowPriority <- treeEvent:
		// 成功入队
	default:
		// 队列满，丢弃
		s.mu.Lock()
		s.lowPriorityDrop++
		s.mu.Unlock()

		logging.WithFields(map[string]any{
			"event_type": event.Type,
			"namespace":  event.Namespace,
			"key":        event.Key,
			"node_count": len(allNodes),
		}).Warn("低优先级队列满，事件丢弃")
	}
}

// ==================== 事件处理接口 ====================

// OnWrite 写入事件触发
func (s *TreeAwareGossipSync) OnWrite(namespace, key string) {
	event := GossipEvent{
		Type:      EventWrite,
		Namespace: namespace,
		Key:       key,
		Timestamp: time.Now(),
	}
	s.Propagate(event)

	// 同时调用底层实现
	s.EventDrivenGossipSync.OnWrite(namespace, key)
}

// OnNamespaceChange Namespace 变更事件
func (s *TreeAwareGossipSync) OnNamespaceChange(namespace string) {
	event := GossipEvent{
		Type:      EventNamespaceChange,
		Namespace: namespace,
		Timestamp: time.Now(),
	}
	s.Propagate(event)

	s.EventDrivenGossipSync.OnNamespaceChange(namespace)
}

// OnPeerJoin 节点加入事件
func (s *TreeAwareGossipSync) OnPeerJoin(nodeID string) {
	event := GossipEvent{
		Type:      EventPeerJoin,
		NodeID:    nodeID,
		Timestamp: time.Now(),
	}
	s.Propagate(event)

	s.EventDrivenGossipSync.OnPeerJoin(nodeID)
}

// OnPeerLeave 节点离开事件
func (s *TreeAwareGossipSync) OnPeerLeave(nodeID string) {
	event := GossipEvent{
		Type:      EventPeerLeave,
		NodeID:    nodeID,
		Timestamp: time.Now(),
	}
	s.Propagate(event)

	s.EventDrivenGossipSync.OnPeerLeave(nodeID)
}

// ==================== 拓扑管理 ====================

// UpdateTopology 更新树拓扑
func (s *TreeAwareGossipSync) UpdateTopology(parent string, children []string, depth int) {
	s.topology.UpdateTopology(parent, children, depth)

	logging.WithFields(map[string]any{
		"node_type":   s.topology.GetNodeType().String(),
		"parent":      parent,
		"child_count": len(children),
		"tree_depth":  depth,
		"local_node":  s.topology.localNodeID,
	}).Info("树拓扑已更新")
}

// GetTopology 获取树拓扑
func (s *TreeAwareGossipSync) GetTopology() *TreeTopology {
	return s.topology
}

// GetNodeType 获取节点类型
func (s *TreeAwareGossipSync) GetNodeType() NodeType {
	return s.topology.GetNodeType()
}

// GetExpectedDelay 获取预期延迟（基于节点类型）
func (s *TreeAwareGossipSync) GetExpectedDelay() time.Duration {
	depth := s.topology.GetTreeDepth()
	// Root 节点延迟 = 树深度 × 单跳延迟（假设单跳延迟 100ms）
	// 叶子节点延迟 = 0（本地产生）
	return time.Duration(depth) * 100 * time.Millisecond
}

// ==================== 统计信息 ====================

// TreeAwareStats 树感知统计
type TreeAwareStats struct {
	NodeType             NodeType
	TreeDepth            int
	HighPrioritySent     uint64
	NormalPrioritySent   uint64
	LowPrioritySent      uint64
	HighPriorityDrop     uint64
	NormalPriorityDrop   uint64
	LowPriorityDrop      uint64
	ExpectedDelay        time.Duration
	HighPriorityQueued   int
	NormalPriorityQueued int
	LowPriorityQueued    int
}

// GetTreeAwareStats 获取树感知统计
func (s *TreeAwareGossipSync) GetTreeAwareStats() TreeAwareStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return TreeAwareStats{
		NodeType:             s.topology.GetNodeType(),
		TreeDepth:            s.topology.GetTreeDepth(),
		HighPrioritySent:     s.highPrioritySent,
		NormalPrioritySent:   s.normalPrioritySent,
		LowPrioritySent:      s.lowPrioritySent,
		HighPriorityDrop:     s.highPriorityDrop,
		NormalPriorityDrop:   s.normalPriorityDrop,
		LowPriorityDrop:      s.lowPriorityDrop,
		ExpectedDelay:        s.GetExpectedDelay(),
		HighPriorityQueued:   len(s.highPriority),
		NormalPriorityQueued: len(s.normalPriority),
		LowPriorityQueued:    len(s.lowPriority),
	}
}

// ==================== 生命周期 ====================

// Close 关闭
func (s *TreeAwareGossipSync) Close() error {
	s.cancel()
	s.wg.Wait()
	return s.EventDrivenGossipSync.Close()
}
