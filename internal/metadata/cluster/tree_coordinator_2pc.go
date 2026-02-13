// Package cluster TreeCoordinator 2PC/Quorum 协调者集成
//
// 核心设计：
//   - 父节点（Parent）作为 2PC/Quorum 的协调者
//   - 同父子节点组参与 2PC 强一致
//   - 跨父节点使用 Quorum 增强最终一致
//
// 层级策略：
//   - Layer1（树内层）：父节点协调下的 2PC
//   - Layer2（组间层）：跨父节点的 Quorum
//   - Layer3（全局层）：Gossip 最终一致
package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/consistency"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/metadata/quorum"
	"github.com/jzhang405/NexKV/internal/transport"
)

// ========================================
// 协调者配置
// ========================================

// CoordinatorConfig 2PC/Quorum 协调者配置
type CoordinatorConfig struct {
	// DefaultTimeout 默认超时时间（默认 5 秒）
	DefaultTimeout time.Duration

	// MaxRetries 最大重试次数（默认 3）
	MaxRetries int

	// RetryDelay 重试延迟（默认 100ms）
	RetryDelay time.Duration

	// QuorumTimeout Quorum 超时时间（默认 5 秒）
	QuorumTimeout time.Duration

	// Enable2PC 是否启用 2PC（默认 true）
	Enable2PC bool

	// EnableQuorum 是否启用 Quorum（默认 true）
	EnableQuorum bool
}

// DefaultCoordinatorConfig 返回默认协调者配置
func DefaultCoordinatorConfig() *CoordinatorConfig {
	return &CoordinatorConfig{
		DefaultTimeout: 5 * time.Second,
		MaxRetries:     3,
		RetryDelay:     100 * time.Millisecond,
		QuorumTimeout:  5 * time.Second,
		Enable2PC:      true,
		EnableQuorum:   true,
	}
}

// ========================================
// TreeCoordinator 扩展字段
// ========================================

// coordinatorState 协调者状态（内部使用）
type coordinatorState struct {
	mu sync.RWMutex

	// 2PC 协调器（仅父节点启用）
	twoPCCoordinator *consistency.TwoPCMerkleCoordinator

	// Quorum 协调器
	quorumCoordinator *quorum.QuorumCoordinator

	// 配置
	config *CoordinatorConfig

	// HLC 时钟
	hlc *clock.HLC

	// Merkle Tree
	merkleTree *kvstore.NamespacedMerkleTree

	// 是否已初始化
	initialized bool
}

// ========================================
// 协调者初始化
// ========================================

// InitCoordinator 初始化 2PC/Quorum 协调者
//
// 设计原则：
//   - 父节点（Parent 角色）启用 2PC 协调器
//   - 所有节点启用 Quorum 协调器
//   - 使用父节点作为协调者
func (tc *TreeCoordinator) InitCoordinator(config *CoordinatorConfig) error {
	tc.coordState.mu.Lock()
	defer tc.coordState.mu.Unlock()

	if tc.coordState.initialized {
		return nil // 已初始化
	}

	if config == nil {
		config = DefaultCoordinatorConfig()
	}

	// 初始化 HLC 时钟
	hlc := clock.NewHLC()

	// 检查元数据存储是否已初始化
	tc.metadataMu.RLock()
	metadataKV := tc.metadataKV
	tc.metadataMu.RUnlock()

	if metadataKV == nil {
		logging.WithField("node_id", tc.localNode.NodeID).Warn("元数据 KV 未初始化，协调者将使用简化模式")
	}

	// 创建 Merkle Tree
	var merkleTree *kvstore.NamespacedMerkleTree
	if metadataKV != nil {
		// 使用 MetadataKV 的底层存储创建 Merkle Tree
		merkleTree = kvstore.NewNamespacedMerkleTree(hlc)
	}

	// 根据节点角色决定是否启用 2PC 协调器
	var twoPCCoordinator *consistency.TwoPCMerkleCoordinator
	if config.Enable2PC && tc.isParentNode() && metadataKV != nil && merkleTree != nil {
		// 父节点启用 2PC 协调器
		opts := &consistency.TwoPCOptions{
			MetadataKV:     metadataKV,
			MerkleTree:     merkleTree,
			HLC:            hlc,
			DefaultTimeout: config.DefaultTimeout,
			MaxRetries:     config.MaxRetries,
			RetryDelay:     config.RetryDelay,
		}

		var err error
		twoPCCoordinator, err = consistency.NewTwoPCMerkleCoordinator(opts)
		if err != nil {
			return fmt.Errorf("failed to create 2PC coordinator: %w", err)
		}

		logging.WithFields(map[string]any{
			"node_id":         tc.localNode.NodeID,
			"role":            tc.localNode.Role.String(),
			"default_timeout": config.DefaultTimeout.String(),
		}).Info("2PC 协调器已初始化（父节点角色）")
	}

	// 创建 Quorum 协调器（所有节点都可以使用）
	var quorumCoordinator *quorum.QuorumCoordinator
	if config.EnableQuorum && metadataKV != nil {
		// 获取参与者列表（所有子节点 + 本地节点）
		participants := tc.getQuorumParticipants()

		if len(participants) > 0 {
			// 类型断言：metadataKV 是 kvstore.Store 接口，需要转换为 *kvstore.MetadataKV
			if metadataKVConcrete, ok := metadataKV.(*kvstore.MetadataKV); ok {
				quorumCoordinator = quorum.NewQuorumCoordinator(participants, metadataKVConcrete)
				quorumCoordinator.SetTimeout(config.QuorumTimeout)

				logging.WithFields(map[string]any{
					"node_id":        tc.localNode.NodeID,
					"participants":   len(participants),
					"quorum_timeout": config.QuorumTimeout.String(),
				}).Info("Quorum 协调器已初始化")
			} else {
				logging.WithField("node_id", tc.localNode.NodeID).Warn("元数据 KV 类型不匹配，跳过 Quorum 协调器初始化")
			}
		}
	}

	// 保存状态
	tc.coordState.config = config
	tc.coordState.hlc = hlc
	tc.coordState.merkleTree = merkleTree
	tc.coordState.twoPCCoordinator = twoPCCoordinator
	tc.coordState.quorumCoordinator = quorumCoordinator
	tc.coordState.initialized = true

	logging.WithFields(map[string]any{
		"node_id":        tc.localNode.NodeID,
		"role":           tc.localNode.Role.String(),
		"2pc_enabled":    twoPCCoordinator != nil,
		"quorum_enabled": quorumCoordinator != nil,
	}).Info("协调者初始化完成")

	return nil
}

// InitCoordinatorWithTransport 使用 Transport 初始化协调者（支持网络通信）
func (tc *TreeCoordinator) InitCoordinatorWithTransport(
	config *CoordinatorConfig,
	transportParam transport.Transport,
) error {
	tc.coordState.mu.Lock()
	defer tc.coordState.mu.Unlock()

	if tc.coordState.initialized {
		return nil
	}

	if config == nil {
		config = DefaultCoordinatorConfig()
	}

	// 初始化 HLC 时钟
	hlc := clock.NewHLC()

	// 检查元数据存储是否已初始化
	tc.metadataMu.RLock()
	metadataKV := tc.metadataKV
	tc.metadataMu.RUnlock()

	// 创建 Merkle Tree
	var merkleTree *kvstore.NamespacedMerkleTree
	if metadataKV != nil {
		merkleTree = kvstore.NewNamespacedMerkleTree(hlc)
	}

	// 根据节点角色决定是否启用 2PC 协调器
	var twoPCCoordinator *consistency.TwoPCMerkleCoordinator
	if config.Enable2PC && tc.isParentNode() && metadataKV != nil && merkleTree != nil {
		var err error
		twoPCCoordinator, err = consistency.NewTwoPCMerkleCoordinatorWithTransport(
			metadataKV,
			merkleTree,
			hlc,
			transportParam,
			tc.localNode.NodeID,
		)
		if err != nil {
			return fmt.Errorf("failed to create 2PC coordinator with transport: %w", err)
		}

		logging.WithFields(map[string]any{
			"node_id":         tc.localNode.NodeID,
			"role":            tc.localNode.Role.String(),
			"default_timeout": config.DefaultTimeout.String(),
			"with_transport":  true,
		}).Info("2PC 协调器已初始化（父节点角色，支持网络）")
	}

	// 创建 Quorum 协调器
	var quorumCoordinator *quorum.QuorumCoordinator
	if config.EnableQuorum && metadataKV != nil {
		participants := tc.getQuorumParticipants()

		if len(participants) > 0 {
			// 类型断言：metadataKV 是 kvstore.Store 接口，需要转换为 *kvstore.MetadataKV
			if metadataKVConcrete, ok := metadataKV.(*kvstore.MetadataKV); ok {
				quorumCoordinator = quorum.NewQuorumCoordinator(participants, metadataKVConcrete)
				quorumCoordinator.SetTimeout(config.QuorumTimeout)

				logging.WithFields(map[string]any{
					"node_id":        tc.localNode.NodeID,
					"participants":   len(participants),
					"quorum_timeout": config.QuorumTimeout.String(),
				}).Info("Quorum 协调器已初始化")
			} else {
				logging.WithField("node_id", tc.localNode.NodeID).Warn("元数据 KV 类型不匹配，跳过 Quorum 协调器初始化")
			}
		}
	}

	// 保存状态
	tc.coordState.config = config
	tc.coordState.hlc = hlc
	tc.coordState.merkleTree = merkleTree
	tc.coordState.twoPCCoordinator = twoPCCoordinator
	tc.coordState.quorumCoordinator = quorumCoordinator
	tc.coordState.initialized = true

	return nil
}

// ========================================
// 2PC 操作（由父节点协调）
// ========================================

// Begin2PCTransaction 开始 2PC 事务
//
// 策略：
//   - 如果本地节点是父节点，直接作为协调者
//   - 如果本地节点是子节点，转发给父节点
//
// 参与者：
//   - 本地节点
//   - 父节点（如果存在）
//   - 兄弟节点（同父子节点组）
func (tc *TreeCoordinator) Begin2PCTransaction(ctx context.Context) (*consistency.TwoPCTransaction, error) {
	tc.coordState.mu.RLock()
	defer tc.coordState.mu.RUnlock()

	if !tc.coordState.initialized {
		return nil, fmt.Errorf("coordinator not initialized")
	}

	// 检查是否启用 2PC
	if tc.coordState.twoPCCoordinator == nil {
		return nil, fmt.Errorf("2PC not enabled for this node (role=%s)", tc.localNode.Role.String())
	}

	// 获取参与者列表
	participants := tc.get2PCParticipants()

	// 开始事务
	tx, err := tc.coordState.twoPCCoordinator.BeginTransaction(participants)
	if err != nil {
		return nil, fmt.Errorf("failed to begin 2PC transaction: %w", err)
	}

	logging.WithFields(map[string]any{
		"tx_id":        tx.TxID,
		"node_id":      tc.localNode.NodeID,
		"participants": len(participants),
	}).Debug("2PC 事务已创建")

	return tx, nil
}

// PreCommit2PC 执行 2PC PreCommit 阶段
func (tc *TreeCoordinator) PreCommit2PC(ctx context.Context, tx *consistency.TwoPCTransaction) error {
	tc.coordState.mu.RLock()
	coordinator := tc.coordState.twoPCCoordinator
	tc.coordState.mu.RUnlock()

	if coordinator == nil {
		return fmt.Errorf("2PC coordinator not available")
	}

	return coordinator.PreCommit(ctx, tx)
}

// Commit2PC 执行 2PC Commit 阶段
func (tc *TreeCoordinator) Commit2PC(ctx context.Context, tx *consistency.TwoPCTransaction) error {
	tc.coordState.mu.RLock()
	coordinator := tc.coordState.twoPCCoordinator
	tc.coordState.mu.RUnlock()

	if coordinator == nil {
		return fmt.Errorf("2PC coordinator not available")
	}

	return coordinator.Commit(ctx, tx)
}

// Rollback2PC 执行 2PC 回滚
func (tc *TreeCoordinator) Rollback2PC(ctx context.Context, tx *consistency.TwoPCTransaction) error {
	tc.coordState.mu.RLock()
	coordinator := tc.coordState.twoPCCoordinator
	tc.coordState.mu.RUnlock()

	if coordinator == nil {
		return fmt.Errorf("2PC coordinator not available")
	}

	return coordinator.Rollback(ctx, tx)
}

// Get2PCTransaction 获取 2PC 事务
func (tc *TreeCoordinator) Get2PCTransaction(txID string) (*consistency.TwoPCTransaction, error) {
	tc.coordState.mu.RLock()
	coordinator := tc.coordState.twoPCCoordinator
	tc.coordState.mu.RUnlock()

	if coordinator == nil {
		return nil, fmt.Errorf("2PC coordinator not available")
	}

	return coordinator.GetTransaction(txID)
}

// ========================================
// Quorum 操作（跨父节点）
// ========================================

// PutWithQuorum 使用 Quorum 机制写入
//
// 策略：
//   - 多数派确认即可
//   - 适用于重要变更（角色变更、拓扑调整）
func (tc *TreeCoordinator) PutWithQuorum(ctx context.Context, ns, key string, value any) error {
	tc.coordState.mu.RLock()
	coordinator := tc.coordState.quorumCoordinator
	tc.coordState.mu.RUnlock()

	if coordinator == nil {
		return fmt.Errorf("quorum coordinator not available")
	}

	opts := quorum.DefaultPutOptions()
	opts.Participants = tc.getQuorumParticipants()

	return coordinator.PutWithQuorum(ctx, ns, key, value, opts)
}

// GetQuorumThreshold 获取 Quorum 阈值
func (tc *TreeCoordinator) GetQuorumThreshold() int {
	tc.coordState.mu.RLock()
	coordinator := tc.coordState.quorumCoordinator
	tc.coordState.mu.RUnlock()

	if coordinator == nil {
		// 简单计算
		participants := tc.getQuorumParticipants()
		return len(participants)/2 + 1
	}

	return coordinator.GetQuorum()
}

// ========================================
// 统一一致性接口
// ========================================

// ConsistencyLevel 一致性级别
type ConsistencyLevel int

const (
	// ConsistencyStrong 强一致（2PC）
	ConsistencyStrong ConsistencyLevel = iota
	// ConsistencyEnhanced 增强最终一致（Quorum）
	ConsistencyEnhanced
	// ConsistencyEventual 最终一致（Gossip）
	ConsistencyEventual
)

// PutWithConsistency 根据一致性级别写入数据
//
// 根据一致性级别选择对应的同步机制：
//   - ConsistencyStrong：2PC（父节点协调）
//   - ConsistencyEnhanced：Quorum（多数派确认）
//   - ConsistencyEventual：Gossip（异步扩散）
func (tc *TreeCoordinator) PutWithConsistency(
	ctx context.Context,
	ns, key string,
	value any,
	level ConsistencyLevel,
) error {
	switch level {
	case ConsistencyStrong:
		return tc.putWith2PC(ctx, ns, key, value)
	case ConsistencyEnhanced:
		return tc.PutWithQuorum(ctx, ns, key, value)
	case ConsistencyEventual:
		return tc.putWithGossip(ctx, ns, key, value)
	default:
		return fmt.Errorf("unknown consistency level: %d", level)
	}
}

// putWith2PC 使用 2PC 写入
func (tc *TreeCoordinator) putWith2PC(ctx context.Context, ns, key string, value any) error {
	// 开始事务
	tx, err := tc.Begin2PCTransaction(ctx)
	if err != nil {
		return fmt.Errorf("begin 2PC transaction failed: %w", err)
	}

	// 编码值
	tc.metadataMu.RLock()
	metadataKV := tc.metadataKV
	tc.metadataMu.RUnlock()

	var valueBytes []byte
	if metadataKV != nil {
		// 使用 MetadataCodec 编码
		codec := kvstore.NewMetadataCodec(kvstore.CompressionNone)
		valueBytes, err = codec.Encode(value)
		if err != nil {
			_ = tc.Rollback2PC(ctx, tx)
			return fmt.Errorf("encode value failed: %w", err)
		}
	} else {
		// 简化：直接转换
		valueBytes = []byte(fmt.Sprintf("%v", value))
	}

	// 添加操作
	tx.AddOperation(ns, key, valueBytes, 0)

	// PreCommit
	if err := tc.PreCommit2PC(ctx, tx); err != nil {
		_ = tc.Rollback2PC(ctx, tx)
		return fmt.Errorf("precommit failed: %w", err)
	}

	// Commit
	if err := tc.Commit2PC(ctx, tx); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	return nil
}

// putWithGossip 使用 Gossip 写入（最终一致）
func (tc *TreeCoordinator) putWithGossip(ctx context.Context, ns, key string, value any) error {
	tc.metadataMu.RLock()
	metadataKV := tc.metadataKV
	tc.metadataMu.RUnlock()

	if metadataKV == nil {
		return fmt.Errorf("metadata KV not initialized")
	}

	// 写入本地
	if err := metadataKV.Put(ctx, ns, key, value); err != nil {
		return fmt.Errorf("local write failed: %w", err)
	}

	// 触发 Gossip 扩散（异步）
	// 由 MetadataKV 的 Gossip 回调处理

	return nil
}

// ========================================
// 辅助方法
// ========================================

// isParentNode 判断本地节点是否是父节点
//
// 判断依据：
//   - NodeRole 为 Parent 或 ParentStandby
//   - 或者有子节点（说明在充当父节点角色）
func (tc *TreeCoordinator) isParentNode() bool {
	return tc.localNode.Role == Parent || tc.localNode.Role == ParentStandby || len(tc.localNode.ChildrenIDs) > 0
}

// get2PCParticipants 获取 2PC 参与者列表
//
// 参与者包括：
//   - 本地节点
//   - 父节点（如果存在）
//   - 兄弟节点（同父子节点组）
func (tc *TreeCoordinator) get2PCParticipants() []string {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	participants := make([]string, 0)
	participants = append(participants, tc.localNode.NodeID)

	// 添加父节点
	if tc.localNode.ParentID != "" {
		participants = append(participants, tc.localNode.ParentID)
	}

	// 添加兄弟节点（同父子节点组）
	if tc.localNode.ParentID != "" {
		if parent, exists := tc.allNodes[tc.localNode.ParentID]; exists {
			for _, childID := range parent.ChildrenIDs {
				if childID != tc.localNode.NodeID {
					// 检查子节点是否可用
					if child, ok := tc.allNodes[childID]; ok && child.Status == NodeStatusReady {
						participants = append(participants, childID)
					}
				}
			}
		}
	}

	// 如果本地节点是父节点，添加所有子节点
	if tc.isParentNode() {
		for _, childID := range tc.localNode.ChildrenIDs {
			if child, ok := tc.allNodes[childID]; ok && child.Status == NodeStatusReady {
				participants = append(participants, childID)
			}
		}
	}

	return participants
}

// getQuorumParticipants 获取 Quorum 参与者列表
//
// 参与者包括：
//   - 本地节点
//   - 所有子节点
//   - 父节点（如果存在）
func (tc *TreeCoordinator) getQuorumParticipants() []string {
	tc.nodesMu.RLock()
	defer tc.nodesMu.RUnlock()

	participants := make([]string, 0)

	// 添加本地节点
	participants = append(participants, tc.localNode.NodeID)

	// 添加父节点
	if tc.localNode.ParentID != "" {
		if parent, exists := tc.allNodes[tc.localNode.ParentID]; exists && parent.Status == NodeStatusReady {
			participants = append(participants, tc.localNode.ParentID)
		}
	}

	// 添加所有子节点
	for _, childID := range tc.localNode.ChildrenIDs {
		if child, ok := tc.allNodes[childID]; ok && child.Status == NodeStatusReady {
			participants = append(participants, childID)
		}
	}

	// 添加兄弟节点（可选，用于跨父节点 Quorum）
	if tc.localNode.ParentID != "" {
		if parent, exists := tc.allNodes[tc.localNode.ParentID]; exists {
			for _, childID := range parent.ChildrenIDs {
				if childID != tc.localNode.NodeID {
					if child, ok := tc.allNodes[childID]; ok && child.Status == NodeStatusReady {
						participants = append(participants, childID)
					}
				}
			}
		}
	}

	return participants
}

// GetConsistencyLevelForNamespace 根据 Namespace 返回推荐的一致性级别
func (tc *TreeCoordinator) GetConsistencyLevelForNamespace(ns string) ConsistencyLevel {
	switch ns {
	case kvstore.NamespaceCluster,
		kvstore.NamespaceShard,
		kvstore.NamespaceStatic,
		kvstore.NamespaceVersion:
		return ConsistencyStrong // 2PC 强一致

	case kvstore.NamespaceRole,
		kvstore.NamespaceTopo:
		return ConsistencyEnhanced // Quorum 增强最终一致

	default:
		return ConsistencyEventual // Gossip 最终一致
	}
}

// CloseCoordinator 关闭协调者
func (tc *TreeCoordinator) CloseCoordinator() error {
	tc.coordState.mu.Lock()
	defer tc.coordState.mu.Unlock()

	if !tc.coordState.initialized {
		return nil
	}

	var lastErr error

	// 关闭 2PC 协调器
	if tc.coordState.twoPCCoordinator != nil {
		if err := tc.coordState.twoPCCoordinator.Close(); err != nil {
			lastErr = err
			logging.WithField("error", err).Warn("关闭 2PC 协调器失败")
		}
	}

	tc.coordState.twoPCCoordinator = nil
	tc.coordState.quorumCoordinator = nil
	tc.coordState.merkleTree = nil
	tc.coordState.initialized = false

	logging.WithField("node_id", tc.localNode.NodeID).Info("协调者已关闭")

	return lastErr
}

// Get2PCCoordinator 获取 2PC 协调器（用于高级操作）
func (tc *TreeCoordinator) Get2PCCoordinator() *consistency.TwoPCMerkleCoordinator {
	tc.coordState.mu.RLock()
	defer tc.coordState.mu.RUnlock()
	return tc.coordState.twoPCCoordinator
}

// GetQuorumCoordinator 获取 Quorum 协调器（用于高级操作）
func (tc *TreeCoordinator) GetQuorumCoordinator() *quorum.QuorumCoordinator {
	tc.coordState.mu.RLock()
	defer tc.coordState.mu.RUnlock()
	return tc.coordState.quorumCoordinator
}

// IsCoordinatorInitialized 检查协调者是否已初始化
func (tc *TreeCoordinator) IsCoordinatorInitialized() bool {
	tc.coordState.mu.RLock()
	defer tc.coordState.mu.RUnlock()
	return tc.coordState.initialized
}

// UpdateQuorumParticipants 更新 Quorum 参与者列表
//
// 当拓扑变更时调用，更新 Quorum 参与者
func (tc *TreeCoordinator) UpdateQuorumParticipants() {
	tc.coordState.mu.Lock()
	defer tc.coordState.mu.Unlock()

	if tc.coordState.quorumCoordinator == nil {
		return
	}

	participants := tc.getQuorumParticipants()
	tc.coordState.quorumCoordinator.SetParticipants(participants)

	logging.WithFields(map[string]any{
		"node_id":      tc.localNode.NodeID,
		"participants": len(participants),
	}).Debug("Quorum 参与者已更新")
}
