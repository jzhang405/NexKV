// Package consistency 提供 2PC 强一致性协调器实现
//
// 核心功能：
//   - 两阶段提交（2PC）：PreCommit → Commit/Rollback
//   - 与 Merkle Tree 协同：提交后更新 Hash
//   - Pending 操作暂存：支持批量操作
//   - 超时与重试：5秒超时，自动回滚
package consistency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// ==================== 2PC 状态定义 ====================

// TransactionState 事务状态
type TransactionState int

const (
	// TxStateInit 初始状态
	TxStateInit TransactionState = iota

	// TxStatePreCommit PreCommit 阶段完成，等待 ACK
	TxStatePreCommit

	// TxStateCommitted 事务已提交
	TxStateCommitted

	// TxStateRolledBack 事务已回滚
	TxStateRolledBack

	// TxStateTimeout 事务超时
	TxStateTimeout
)

// String 返回状态的字符串表示
func (s TransactionState) String() string {
	switch s {
	case TxStateInit:
		return "Init"
	case TxStatePreCommit:
		return "PreCommit"
	case TxStateCommitted:
		return "Committed"
	case TxStateRolledBack:
		return "RolledBack"
	case TxStateTimeout:
		return "Timeout"
	default:
		return "Unknown"
	}
}

// ==================== Pending 操作 ====================

// PendingOperation 暂存的操作（PreCommit 阶段）
type PendingOperation struct {
	// 基础信息
	TxID       string    // 事务 ID
	NS         string    // 命名空间
	Key        string    // 键
	Value      []byte    // 值（编码后）
	Version    uint64    // 版本号
	CreateTime time.Time // 创建时间

	// Merkle 相关
	MerkleHash   string // Merkle Hash（Commit 时计算）
	ShouldUpdate bool   // 是否更新 Merkle Tree
}

// ==================== 2PC 事务 ====================

// TwoPCTransaction 2PC 事务
type TwoPCTransaction struct {
	TxID       string              // 事务 ID
	State      TransactionState    // 当前状态
	Operations []*PendingOperation // 暂存的操作列表

	// 参与者
	Participants []string        // 参与者节点 ID 列表
	Acks         map[string]bool // participantID -> ACK 状态

	// 时间戳
	CreateTime    time.Time // 创建时间
	PreCommitTime time.Time // PreCommit 时间
	CommitTime    time.Time // Commit 时间

	// 配置
	Timeout time.Duration // 超时时间（默认 5 秒）
	Quorum  int           // 需要的 ACK 数量（默认全部）

	// 错误
	LastError error // 最后一次错误
}

// NewTwoPCTransaction 创建新的 2PC 事务
func NewTwoPCTransaction(txID string, participants []string, timeout time.Duration) *TwoPCTransaction {
	if timeout == 0 {
		timeout = 5 * time.Second // 默认 5 秒超时
	}

	return &TwoPCTransaction{
		TxID:         txID,
		State:        TxStateInit,
		Operations:   make([]*PendingOperation, 0),
		Participants: participants,
		Acks:         make(map[string]bool),
		CreateTime:   time.Now(),
		Timeout:      timeout,
		Quorum:       len(participants), // 2PC 需要全部 ACK
	}
}

// AddOperation 添加操作到事务
func (tx *TwoPCTransaction) AddOperation(ns, key string, value []byte, version uint64) {
	op := &PendingOperation{
		TxID:         tx.TxID,
		NS:           ns,
		Key:          key,
		Value:        value,
		Version:      version,
		CreateTime:   time.Now(),
		ShouldUpdate: true, // 默认更新 Merkle Tree
	}
	tx.Operations = append(tx.Operations, op)
}

// IsTimedOut 检查事务是否超时
func (tx *TwoPCTransaction) IsTimedOut() bool {
	if tx.State == TxStateCommitted || tx.State == TxStateRolledBack {
		return false
	}

	elapsed := time.Since(tx.CreateTime)
	return elapsed > tx.Timeout
}

// HasAllAcks 检查是否收到所有 ACK
func (tx *TwoPCTransaction) HasAllAcks() bool {
	if len(tx.Acks) < tx.Quorum {
		return false
	}

	for _, participant := range tx.Participants {
		if !tx.Acks[participant] {
			return false
		}
	}

	return true
}

// ==================== TwoPCMerkleCoordinator ====================

// TwoPCMerkleCoordinator 2PC 强一致性协调器
//
// 核心功能：
//   - 两阶段提交：PreCommit → Commit/Rollback
//   - 与 Merkle Tree 协同：提交后更新 Hash
//   - Pending 操作暂存：支持批量操作
//   - 超时与重试：5秒超时，自动回滚
type TwoPCMerkleCoordinator struct {
	mu sync.RWMutex

	// 核心组件
	metadataKV kvstore.Store                 // 元数据 KV 存储
	merkleTree *kvstore.NamespacedMerkleTree // Merkle Tree
	hlc        *clock.HLC                    // HLC 时钟

	// 事务管理
	transactions map[string]*TwoPCTransaction // 进行中的事务

	// 配置
	defaultTimeout time.Duration // 默认超时时间（5 秒）

	// 状态
	closed bool
}

// TwoPCOptions 2PC 协调器配置选项
type TwoPCOptions struct {
	// MetadataKV 元数据 KV 存储
	MetadataKV kvstore.Store

	// MerkleTree Merkle Tree
	MerkleTree *kvstore.NamespacedMerkleTree

	// HLC HLC 时钟实例（如果为空，创建新实例）
	HLC *clock.HLC

	// DefaultTimeout 默认超时时间（默认 5 秒）
	DefaultTimeout time.Duration
}

// NewTwoPCMerkleCoordinator 创建新的 2PC 协调器
func NewTwoPCMerkleCoordinator(opts *TwoPCOptions) (*TwoPCMerkleCoordinator, error) {
	if opts == nil {
		return nil, fmt.Errorf("options cannot be nil")
	}
	if opts.MetadataKV == nil {
		return nil, fmt.Errorf("MetadataKV cannot be nil")
	}
	if opts.MerkleTree == nil {
		return nil, fmt.Errorf("MerkleTree cannot be nil")
	}

	hlc := opts.HLC
	if hlc == nil {
		hlc = clock.NewHLC()
	}

	timeout := opts.DefaultTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	return &TwoPCMerkleCoordinator{
		metadataKV:     opts.MetadataKV,
		merkleTree:     opts.MerkleTree,
		hlc:            hlc,
		transactions:   make(map[string]*TwoPCTransaction),
		defaultTimeout: timeout,
	}, nil
}

// ==================== 核心 2PC 流程 ====================

// BeginTransaction 开始新事务
func (c *TwoPCMerkleCoordinator) BeginTransaction(participants []string) (*TwoPCTransaction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, fmt.Errorf("coordinator is closed")
	}

	// 生成事务 ID
	txID := c.generateTxID()

	// 创建新事务
	tx := NewTwoPCTransaction(txID, participants, c.defaultTimeout)

	// 存储事务
	c.transactions[txID] = tx

	return tx, nil
}

// PreCommit 第一阶段：预提交
//
// 流程：
//  1. 暂存操作到 Pending 状态
//  2. 计算 Merkle Hash
//  3. 发送 PreCommit 请求给所有参与者
//  4. 等待 ACK
//
// 返回：error 如果 PreCommit 失败
func (c *TwoPCMerkleCoordinator) PreCommit(ctx context.Context, tx *TwoPCTransaction) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("coordinator is closed")
	}

	// 检查事务状态
	if tx.State != TxStateInit {
		return fmt.Errorf("transaction is not in Init state: %s", tx.State)
	}

	// 更新状态为 PreCommit
	tx.State = TxStatePreCommit
	tx.PreCommitTime = time.Now()

	// 计算 Merkle Hash（预提交阶段先计算）
	if err := c.precomputeMerkleHash(tx); err != nil {
		tx.State = TxStateRolledBack
		tx.LastError = err
		return fmt.Errorf("precompute Merkle Hash failed: %w", err)
	}

	// TODO: 实际场景中，这里需要通过网络发送 PreCommit 请求给所有参与者
	// 模拟：假设所有参与者都会 ACK
	for _, participant := range tx.Participants {
		tx.Acks[participant] = true
	}

	return nil
}

// Commit 第二阶段：提交
//
// 流程：
//  1. 检查是否收到所有 ACK
//  2. 批量应用所有操作到 MetadataKV
//  3. 更新 Merkle Tree
//  4. 清除 Pending 状态
//
// 返回：error 如果提交失败
func (c *TwoPCMerkleCoordinator) Commit(ctx context.Context, tx *TwoPCTransaction) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("coordinator is closed")
	}

	// 检查事务状态
	if tx.State != TxStatePreCommit {
		return fmt.Errorf("transaction is not in PreCommit state: %s", tx.State)
	}

	// 检查是否收到所有 ACK
	if !tx.HasAllAcks() {
		tx.State = TxStateRolledBack
		tx.LastError = fmt.Errorf("not all ACKs received: %d/%d", len(tx.Acks), tx.Quorum)
		return tx.LastError
	}

	// 批量应用所有操作
	for _, op := range tx.Operations {
		// 写入 MetadataKV
		if err := c.metadataKV.PutRaw(ctx, op.NS, op.Key, op.Value); err != nil {
			// 写入失败，回滚
			tx.State = TxStateRolledBack
			tx.LastError = fmt.Errorf("PutRaw failed for key %s: %w", op.Key, err)
			c.rollbackTransaction(tx)
			return tx.LastError
		}

		// 更新 Merkle Tree（如果需要）
		if op.ShouldUpdate {
			if err := c.merkleTree.UpdateKeyFromBytes(op.NS, op.Key, op.Value); err != nil {
				tx.State = TxStateRolledBack
				tx.LastError = fmt.Errorf("merkle UpdateKey failed for key %s: %w", op.Key, err)
				c.rollbackTransaction(tx)
				return tx.LastError
			}
		}
	}

	// 提交成功
	tx.State = TxStateCommitted
	tx.CommitTime = time.Now()

	// 清除事务
	delete(c.transactions, tx.TxID)

	return nil
}

// Rollback 回滚事务
//
// 流程：
//  1. 清除 Pending 状态
//  2. 删除暂存的操作
//  3. 更新事务状态为 RolledBack
func (c *TwoPCMerkleCoordinator) Rollback(ctx context.Context, tx *TwoPCTransaction) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("coordinator is closed")
	}

	// 回滚事务
	c.rollbackTransaction(tx)

	return nil
}

// rollbackTransaction 内部回滚方法（调用前必须持有锁）
func (c *TwoPCMerkleCoordinator) rollbackTransaction(tx *TwoPCTransaction) {
	tx.State = TxStateRolledBack
	tx.Operations = nil
	tx.Acks = nil

	// 清除事务
	delete(c.transactions, tx.TxID)
}

// ==================== 辅助方法 ====================

// generateTxID 生成事务 ID
func (c *TwoPCMerkleCoordinator) generateTxID() string {
	hlcTS := c.hlc.Now()
	return fmt.Sprintf("tx-%d-%d", hlcTS.PhysicalTime(), hlcTS.LogicalCounter())
}

// precomputeMerkleHash 预计算 Merkle Hash
func (c *TwoPCMerkleCoordinator) precomputeMerkleHash(tx *TwoPCTransaction) error {
	for _, op := range tx.Operations {
		if op.ShouldUpdate {
			hash, err := c.merkleTree.GetKeyHash(op.NS, op.Key)
			if err == nil {
				op.MerkleHash = hash
			}
		}
	}
	return nil
}

// GetTransaction 获取事务
func (c *TwoPCMerkleCoordinator) GetTransaction(txID string) (*TwoPCTransaction, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tx, ok := c.transactions[txID]
	if !ok {
		return nil, fmt.Errorf("transaction not found: %s", txID)
	}

	return tx, nil
}

// GetAllTransactions 获取所有进行中的事务
func (c *TwoPCMerkleCoordinator) GetAllTransactions() map[string]*TwoPCTransaction {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*TwoPCTransaction, len(c.transactions))
	for k, v := range c.transactions {
		result[k] = v
	}

	return result
}

// CleanupTimeoutTransactions 清理超时事务
//
// 返回：清理的事务数量
func (c *TwoPCMerkleCoordinator) CleanupTimeoutTransactions() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	cleaned := 0

	for _, tx := range c.transactions {
		if tx.IsTimedOut() {
			tx.State = TxStateTimeout
			c.rollbackTransaction(tx)
			cleaned++
		}
	}

	return cleaned
}

// Close 关闭协调器
func (c *TwoPCMerkleCoordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	for _, tx := range c.transactions {
		c.rollbackTransaction(tx)
	}

	return nil
}
