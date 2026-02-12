// Package consistency 提供树形拓扑分层一致性协调器
//
// 核心功能：
//   - 三级一致性模型：2PC（强一致）→ Quorum（增强最终一致）→ Gossip（最终一致）
//   - 树形拓扑分层策略：同父子节点组内 2PC，跨父节点 Gossip
//   - Merkle + 一致性协同：提交后更新 Merkle Tree
//   - 拓扑感知同步：根据树形结构选择最优同步策略
package consistency

import (
	"context"
	"fmt"
	"sync"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/gossip"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/metadata/quorum"
	"github.com/jzhang405/NexKV/internal/transport"
)

// ==================== 树形拓扑定义 ====================

// TreeNode 树节点信息
type TreeNode struct {
	NodeID      string   // 节点 ID
	ParentID    string   // 父节点 ID
	ChildrenIDs []string // 子节点 ID 列表
	Level       int      // 层级（Root=0）
}

// TreeTopology 树形拓扑结构
type TreeTopology struct {
	mu    sync.RWMutex
	nodes map[string]*TreeNode // nodeID -> TreeNode
	root  string               // 根节点 ID
}

// NewTreeTopology 创建新的树形拓扑
func NewTreeTopology(rootID string) *TreeTopology {
	nodes := map[string]*TreeNode{
		rootID: {
			NodeID:      rootID,
			ParentID:    "",
			ChildrenIDs: []string{},
			Level:       0,
		},
	}
	return &TreeTopology{
		nodes: nodes,
		root:  rootID,
	}
}

// AddChild 添加子节点
func (t *TreeTopology) AddChild(parentID, childID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	parent, ok := t.nodes[parentID]
	if !ok {
		return fmt.Errorf("parent node not found: %s", parentID)
	}

	// 检查是否已存在
	if _, ok := t.nodes[childID]; ok {
		return fmt.Errorf("child node already exists: %s", childID)
	}

	// 创建子节点
	child := &TreeNode{
		NodeID:      childID,
		ParentID:    parentID,
		ChildrenIDs: []string{},
		Level:       parent.Level + 1,
	}
	t.nodes[childID] = child

	// 更新父节点的子节点列表
	parent.ChildrenIDs = append(parent.ChildrenIDs, childID)

	return nil
}

// GetNode 获取节点
func (t *TreeTopology) GetNode(nodeID string) (*TreeNode, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	node, ok := t.nodes[nodeID]
	return node, ok
}

// GetSiblingNodes 获取兄弟节点（同父节点的其他子节点）
func (t *TreeTopology) GetSiblingNodes(nodeID string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	node, ok := t.nodes[nodeID]
	if !ok || node.ParentID == "" {
		return []string{}
	}

	parent, ok := t.nodes[node.ParentID]
	if !ok {
		return []string{}
	}

	siblings := make([]string, 0, len(parent.ChildrenIDs)-1)
	for _, childID := range parent.ChildrenIDs {
		if childID != nodeID {
			siblings = append(siblings, childID)
		}
	}

	return siblings
}

// GetDescendantNodes 获取所有子孙节点（递归）
func (t *TreeTopology) GetDescendantNodes(nodeID string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	node, ok := t.nodes[nodeID]
	if !ok {
		return []string{}
	}

	descendants := make([]string, 0)
	for _, childID := range node.ChildrenIDs {
		descendants = append(descendants, childID)
		// 递归获取子节点的子孙节点
		childDescendants := t.getDescendantNodesLocked(childID)
		descendants = append(descendants, childDescendants...)
	}

	return descendants
}

// getDescendantNodesLocked 内部方法（调用前必须持有锁）
func (t *TreeTopology) getDescendantNodesLocked(nodeID string) []string {
	node, ok := t.nodes[nodeID]
	if !ok {
		return []string{}
	}

	descendants := make([]string, 0)
	for _, childID := range node.ChildrenIDs {
		descendants = append(descendants, childID)
		childDescendants := t.getDescendantNodesLocked(childID)
		descendants = append(descendants, childDescendants...)
	}

	return descendants
}

// ==================== 树形拓扑分层策略 ====================

// Layer 三层模型定义
type Layer int

const (
	// Layer1 树内层（父子节点组内）
	// 一致性级别：2PC 强一致
	// 范围：同父子节点的所有节点
	// 场景：关键变更（分片创建、主副本切换、节点加入）
	Layer1 Layer = iota

	// Layer2 组间层（跨父节点）
	// 一致性级别：Quorum 增强最终一致
	// 范围：不同父节点组的代表节点
	// 场景：重要变更（角色变更、拓扑调整）
	Layer2

	// Layer3 全局层（整个集群）
	// 一致性级别：Gossip 最终一致
	// 范围：所有节点
	// 场景：普通变更（状态更新、负载信息刷新）
	Layer3
)

// String 返回层的字符串表示
func (l Layer) String() string {
	switch l {
	case Layer1:
		return "Layer1-Tree2PC"
	case Layer2:
		return "Layer2-Quorum"
	case Layer3:
		return "Layer3-Gossip"
	default:
		return "Unknown"
	}
}

// ConsistencyLevelForLayer 返回层对应的一致性级别
func (l Layer) ConsistencyLevelForLayer() ConsistencyLevel {
	switch l {
	case Layer1:
		return ConsistencyStrong2PC
	case Layer2:
		return ConsistencyEnhancedEventual
	case Layer3:
		return ConsistencyEventual
	default:
		return ConsistencyEventual
	}
}

// ==================== TreeTopologyCoordinator 树形拓扑协调器 ====================

// TreeTopologyCoordinator 树形拓扑分层协调器
//
// 核心功能：
//   - 根据树形拓扑选择最优一致性策略
//   - Layer1：同父子节点组内 2PC
//   - Layer2：跨父节点 Quorum
//   - Layer3：全局 Gossip
//   - Merkle + 一致性协同
type TreeTopologyCoordinator struct {
	mu sync.RWMutex

	// 核心组件
	twoPCCoordinator  *TwoPCMerkleCoordinator   // 2PC 协调器
	quorumCoordinator *quorum.QuorumCoordinator // Quorum 协调器
	gossipSync        *gossip.MerkleGossipSync  // Gossip 同步

	// 拓扑信息
	topology    *TreeTopology // 树形拓扑
	localNodeID string        // 本地节点 ID

	// Merkle Tree
	merkleTree *kvstore.NamespacedMerkleTree

	// 时钟
	hlc *clock.HLC

	// 状态
	closed bool
}

// TreeTopologyOptions 树形拓扑协调器配置选项
type TreeTopologyOptions struct {
	// Topology 树形拓扑
	Topology *TreeTopology

	// LocalNodeID 本地节点 ID
	LocalNodeID string

	// TwoPCOptions 2PC 协调器配置
	TwoPCOptions *TwoPCOptions

	// QuorumParticipants Quorum 参与者列表（用于 Layer2）
	QuorumParticipants []string

	// QuorumMetadataKV Quorum 元数据存储
	QuorumMetadataKV *kvstore.MetadataKV

	// GossipMerkleTree Gossip Merkle Tree（用于 Layer3）
	GossipMerkleTree *kvstore.NamespacedMerkleTree

	// GossipMetadataKV Gossip 元数据存储
	GossipMetadataKV *kvstore.MetadataKV

	// GossipTransport Gossip 传输层（可选）
	GossipTransport transport.Transport

	// HLC HLC 时钟实例
	HLC *clock.HLC
}

// NewTreeTopologyCoordinator 创建树形拓扑协调器
func NewTreeTopologyCoordinator(opts *TreeTopologyOptions) (*TreeTopologyCoordinator, error) {
	if opts == nil {
		return nil, fmt.Errorf("options cannot be nil")
	}

	if opts.Topology == nil {
		return nil, fmt.Errorf("topology cannot be nil")
	}

	if opts.LocalNodeID == "" {
		return nil, fmt.Errorf("local node ID cannot be empty")
	}

	// 创建 2PC 协调器
	twoPCCoordinator, err := NewTwoPCMerkleCoordinator(opts.TwoPCOptions)
	if err != nil {
		return nil, fmt.Errorf("create 2PC coordinator failed: %w", err)
	}

	// 默认 HLC 时钟
	hlc := opts.HLC
	if hlc == nil {
		hlc = clock.NewHLC()
	}

	// 创建 Quorum 协调器（如果提供了参与者列表）
	var quorumCoordinator *quorum.QuorumCoordinator
	if len(opts.QuorumParticipants) > 0 && opts.QuorumMetadataKV != nil {
		quorumCoordinator = quorum.NewQuorumCoordinator(
			opts.QuorumParticipants,
			opts.QuorumMetadataKV,
		)
	}

	// 创建 Gossip 同步（如果提供了必要的配置）
	var gossipSync *gossip.MerkleGossipSync
	if opts.GossipMerkleTree != nil && opts.GossipMetadataKV != nil {
		// 使用默认的随机 Peer 选择器
		peerSelector := gossip.NewRandomPeerSelector()
		gossipSync = gossip.NewMerkleGossipSync(
			opts.GossipMerkleTree,
			opts.GossipMetadataKV,
			opts.GossipTransport,
			opts.LocalNodeID,
			peerSelector,
		)
	}

	return &TreeTopologyCoordinator{
		twoPCCoordinator:  twoPCCoordinator,
		quorumCoordinator: quorumCoordinator,
		gossipSync:        gossipSync,
		topology:          opts.Topology,
		localNodeID:       opts.LocalNodeID,
		merkleTree:        opts.TwoPCOptions.MerkleTree,
		hlc:               hlc,
		closed:            false,
	}, nil
}

// ==================== 核心 API ====================

// PutWithLayer 根据层级写入数据
//
// 根据指定的层级选择对应的一致性机制：
//   - Layer1：2PC 强一致（同父子节点组内）
//   - Layer2：Quorum 增强最终一致（跨父节点）
//   - Layer3：Gossip 最终一致（全局）
func (c *TreeTopologyCoordinator) PutWithLayer(ctx context.Context, ns, key string, value any, layer Layer) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return fmt.Errorf("coordinator is closed")
	}

	switch layer {
	case Layer1:
		return c.putWith2PC(ctx, ns, key, value)
	case Layer2:
		return c.putWithQuorum(ctx, ns, key, value)
	case Layer3:
		return c.putWithGossip(ctx, ns, key, value)
	default:
		return fmt.Errorf("unknown layer: %d", layer)
	}
}

// putWith2PC 使用 2PC 写入（Layer1：树内层）
//
// 策略：同父子节点组内 2PC
// 参与者：本地节点的父节点 + 所有兄弟节点
func (c *TreeTopologyCoordinator) putWith2PC(ctx context.Context, ns, key string, value any) error {
	// 获取本地节点信息
	localNode, ok := c.topology.GetNode(c.localNodeID)
	if !ok {
		return fmt.Errorf("local node not found: %s", c.localNodeID)
	}

	// 构建 Layer1 参与者：父节点 + 兄弟节点 + 本地节点
	participants := []string{c.localNodeID}

	if localNode.ParentID != "" {
		participants = append(participants, localNode.ParentID)
	}

	siblings := c.topology.GetSiblingNodes(c.localNodeID)
	participants = append(participants, siblings...)

	// 开始 2PC 事务
	tx, err := c.twoPCCoordinator.BeginTransaction(participants)
	if err != nil {
		return fmt.Errorf("begin transaction failed: %w", err)
	}

	// 编码值
	// TODO: 这里需要使用 MetadataCodec 编码，暂时简化
	valueBytes := []byte(fmt.Sprintf("%v", value))

	// 添加操作
	tx.AddOperation(ns, key, valueBytes, 0)

	// PreCommit
	if err := c.twoPCCoordinator.PreCommit(ctx, tx); err != nil {
		return fmt.Errorf("precommit failed: %w", err)
	}

	// Commit
	if err := c.twoPCCoordinator.Commit(ctx, tx); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	return nil
}

// putWithQuorum 使用 Quorum 写入（Layer2：组间层）
//
// 策略：跨父节点 Quorum
// 参与者：不同父节点组的代表节点
func (c *TreeTopologyCoordinator) putWithQuorum(ctx context.Context, ns, key string, value any) error {
	if c.quorumCoordinator == nil {
		// 如果 Quorum 协调器未初始化，回退到 Gossip
		return c.putWithGossip(ctx, ns, key, value)
	}

	// 使用 Quorum 机制写入
	opts := &quorum.PutOptions{
		Timeout:          3000, // 默认 3 秒超时
		Participants:     c.quorumCoordinator.GetParticipants(),
		SkipMerkleUpdate: false,
		Async:            false,
	}

	return c.quorumCoordinator.PutWithQuorum(ctx, ns, key, value, opts)
}

// putWithGossip 使用 Gossip 写入（Layer3：全局层）
//
// 策略：全局 Gossip
// 参与者：所有节点（异步扩散）
func (c *TreeTopologyCoordinator) putWithGossip(ctx context.Context, ns, key string, value any) error {
	// 1. 写入本地元数据
	err := c.twoPCCoordinator.metadataKV.Put(ctx, ns, key, value)
	if err != nil {
		return fmt.Errorf("本地写入失败: %w", err)
	}

	// 2. 更新 Merkle Tree
	err = c.UpdateMerkleAfterCommit(ctx, ns, key, nil)
	if err != nil {
		return fmt.Errorf("更新 Merkle Tree 失败: %w", err)
	}

	// 3. 触发 Gossip 同步（异步扩散）
	if c.gossipSync != nil {
		// TODO: 实现 Gossip 异步触发机制（Task 3.5）
		// 这里简化处理：添加到已知 peer 列表，下次 gossip 时会同步
		// 实际实现中，应该启动一个 goroutine 来执行 gossip
		_ = c.gossipSync //nolint:staticcheck // Task 3.5 将实现完整的 Gossip 触发逻辑
	}

	return nil
}

// ==================== Merkle + 一致性协同 ====================

// MerkleSyncRequest Merkle 同步请求
type MerkleSyncRequest struct {
	NodeID          string            // 节点 ID
	GlobalRootHash  string            // Global Root Hash
	NamespaceHashes map[string]string // Namespace Root Hashes
	RequestID       string            // 请求 ID
}

// MerkleSyncResponse Merkle 同步响应
type MerkleSyncResponse struct {
	NodeID          string            // 节点 ID
	GlobalRootHash  string            // Global Root Hash
	NamespaceHashes map[string]string // Namespace Root Hashes
	DiffNamespaces  []string          // 差异的 Namespace 列表
	RequestID       string            // 请求 ID
}

// GetMerkleRoot 获取本地 Merkle Tree 的 Global Root Hash
func (c *TreeTopologyCoordinator) GetMerkleRoot() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.merkleTree == nil {
		return ""
	}
	return c.merkleTree.GetGlobalRootHash()
}

// GetNamespaceRootHashes 获取所有 Namespace 的 Root Hash
func (c *TreeTopologyCoordinator) GetNamespaceRootHashes() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.merkleTree == nil {
		return make(map[string]string)
	}
	return c.merkleTree.GetAllNamespaceRootHashes()
}

// DetectMerkleDiff 检测 Merkle Tree 差异
//
// 比较本地和请求中的 Merkle Root，返回差异的 Namespace 列表
func (c *TreeTopologyCoordinator) DetectMerkleDiff(request *MerkleSyncRequest) *MerkleSyncResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 获取本地 Merkle Root
	localGlobalRoot := c.GetMerkleRoot()
	localNamespaceHashes := c.GetNamespaceRootHashes()

	// 构建响应
	response := &MerkleSyncResponse{
		NodeID:          c.localNodeID,
		GlobalRootHash:  localGlobalRoot,
		NamespaceHashes: localNamespaceHashes,
		DiffNamespaces:  []string{},
		RequestID:       request.RequestID,
	}

	// 比较全局 Root
	if localGlobalRoot == request.GlobalRootHash {
		// Global Root 相同，无差异
		return response
	}

	// 检测差异的 Namespace
	for ns, localHash := range localNamespaceHashes {
		if peerHash, ok := request.NamespaceHashes[ns]; !ok || peerHash != localHash {
			response.DiffNamespaces = append(response.DiffNamespaces, ns)
		}
	}

	// 检查 peer 有但本地没有的 Namespace
	for ns := range request.NamespaceHashes {
		if _, ok := localNamespaceHashes[ns]; !ok {
			response.DiffNamespaces = append(response.DiffNamespaces, ns)
		}
	}

	return response
}

// SyncMerkleBeforeCommit 在提交前同步 Merkle Root 差异
//
// 流程：
//  1. 获取本地 Merkle Root
//  2. 与参与者交换 Merkle Root
//  3. 检测差异
//  4. 同步差异数据
func (c *TreeTopologyCoordinator) SyncMerkleBeforeCommit(ctx context.Context, participants []string) error {
	// 获取本地 Global Root
	localRoot := c.merkleTree.GetGlobalRootHash()

	// TODO: 与参与者交换 Merkle Root（Task 3.4 需要网络通信支持）
	// TODO: 检测差异（已在 DetectMerkleDiff 中实现）
	// TODO: 同步差异数据（Task 3.5 需要完整 Gossip 支持）

	_ = localRoot
	_ = participants

	// 简化实现：当前阶段只记录日志
	// 实际实现中，这里需要通过网络与参与者交换 Merkle Root
	return nil
}

// UpdateMerkleAfterCommit 在提交后更新 Merkle Tree
//
// 流程：
//  1. 收集所有提交的操作
//  2. 批量更新 Merkle Tree
//  3. 通知 Gossip 同步新的 Root Hash
func (c *TreeTopologyCoordinator) UpdateMerkleAfterCommit(ctx context.Context, ns, key string, data []byte) error {
	// 更新 Merkle Tree
	return c.merkleTree.UpdateKeyFromBytes(ns, key, data)
}

// ==================== 辅助方法 ====================

// GetLayerForNamespace 根据 Namespace 返回推荐的层级
func (c *TreeTopologyCoordinator) GetLayerForNamespace(ns string) Layer {
	// 根据元数据类型选择层级
	// 关键元数据：Layer1（2PC）
	// 重要元数据：Layer2（Quorum）
	// 普通元数据：Layer3（Gossip）

	switch ns {
	case kvstore.NamespaceCluster,
		kvstore.NamespaceShard,
		kvstore.NamespaceStatic,
		kvstore.NamespaceVersion:
		return Layer1 // 2PC 强一致

	case kvstore.NamespaceRole,
		kvstore.NamespaceTopo:
		return Layer2 // Quorum 增强最终一致

	default:
		return Layer3 // Gossip 最终一致
	}
}

// GetLocalNodeID 获取本地节点 ID
func (c *TreeTopologyCoordinator) GetLocalNodeID() string {
	return c.localNodeID
}

// GetTopology 获取拓扑结构
func (c *TreeTopologyCoordinator) GetTopology() *TreeTopology {
	return c.topology
}

// Close 关闭协调器
func (c *TreeTopologyCoordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	// 关闭 2PC 协调器
	if c.twoPCCoordinator != nil {
		_ = c.twoPCCoordinator.Close()
	}

	// 关闭 Gossip 同步
	if c.gossipSync != nil {
		_ = c.gossipSync.Close()
	}

	return nil
}
