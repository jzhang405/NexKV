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
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
	"github.com/jzhang405/NexKV/internal/transport"
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

	// P0-1: Gossip 状态同步新增字段
	Coordinator     string          // 协调者节点 ID（用于 Gossip 查询）
	Acknowledgments map[string]bool // 响应追踪（participantID -> 是否响应）

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
		TxID:            txID,
		State:           TxStateInit,
		Operations:      make([]*PendingOperation, 0),
		Participants:    participants,
		Acks:            make(map[string]bool),
		Acknowledgments: make(map[string]bool), // P0-1: 初始化响应追踪
		CreateTime:      time.Now(),
		Timeout:         timeout,
		Quorum:          len(participants), // 2PC 需要全部 ACK
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
//   - Gossip 状态同步：周期性扩散事务状态，支持故障恢复（P0-1）
type TwoPCMerkleCoordinator struct {
	mu sync.RWMutex

	// 核心组件
	metadataKV kvstore.Store                 // 元数据 KV 存储
	merkleTree *kvstore.NamespacedMerkleTree // Merkle Tree
	hlc        *clock.HLC                    // HLC 时钟
	transport  transport.Transport           // 网络传输层

	// 本地节点标识
	localNodeID string

	// 事务管理
	transactions map[string]*TwoPCTransaction // 进行中的事务

	// 配置
	defaultTimeout time.Duration // 默认超时时间（5 秒）
	maxRetries     int           // 最大重试次数
	retryDelay     time.Duration // 重试延迟

	// P0-1: Gossip 状态同步
	gossipInterval  time.Duration  // Gossip 间隔（默认 5 秒）
	gossipStopChan  chan struct{}  // 停止 Gossip 任务信号
	gossipWaitGroup sync.WaitGroup // 等待 Gossip goroutine 退出

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

	// MaxRetries 最大重试次数（默认 3）
	MaxRetries int

	// RetryDelay 重试延迟（默认 100ms）
	RetryDelay time.Duration
}

// NewTwoPCMerkleCoordinator 创建新的 2PC 协调器（仅本地测试，不启用网络）
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

	maxRetries := opts.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3 // 默认 3 次重试
	}

	retryDelay := opts.RetryDelay
	if retryDelay == 0 {
		retryDelay = 100 * time.Millisecond // 默认 100ms
	}

	coordinator := &TwoPCMerkleCoordinator{
		metadataKV:      opts.MetadataKV,
		merkleTree:      opts.MerkleTree,
		hlc:             hlc,
		transport:       nil, // 本地模式，无网络
		localNodeID:     "",
		transactions:    make(map[string]*TwoPCTransaction),
		defaultTimeout:  timeout,
		maxRetries:      maxRetries,
		retryDelay:      retryDelay,
		gossipInterval:  5 * time.Second,     // P0-1: Gossip 间隔
		gossipStopChan:  make(chan struct{}), // P0-1: 初始化停止信号
	}

	return coordinator, nil
}

// NewTwoPCMerkleCoordinatorWithTransport 创建带 Transport 的协调器（便捷函数）
func NewTwoPCMerkleCoordinatorWithTransport(metadataKV kvstore.Store, merkleTree *kvstore.NamespacedMerkleTree, hlc *clock.HLC, transportParam transport.Transport, localNodeID string) (*TwoPCMerkleCoordinator, error) {
	if metadataKV == nil {
		return nil, fmt.Errorf("MetadataKV cannot be nil")
	}
	if merkleTree == nil {
		return nil, fmt.Errorf("MerkleTree cannot be nil")
	}
	if hlc == nil {
		hlc = clock.NewHLC()
	}

	coordinator := &TwoPCMerkleCoordinator{
		metadataKV:      metadataKV,
		merkleTree:      merkleTree,
		hlc:             hlc,
		transport:       transportParam,
		localNodeID:     localNodeID,
		transactions:    make(map[string]*TwoPCTransaction),
		defaultTimeout:  5 * time.Second,
		maxRetries:      3,                      // 默认 3 次重试
		retryDelay:      100 * time.Millisecond, // 默认 100ms
		gossipInterval:  5 * time.Second,        // P0-1: 默认 5 秒 Gossip 间隔
		gossipStopChan:  make(chan struct{}),    // P0-1: 初始化停止信号
	}

	// 注册消息接收处理器
	if transportParam != nil {
		// 注册 2PC ACK 响应处理器（原有逻辑）
		_ = transportParam.Receive(coordinator.handlePreCommitResponse)

		// P0-1: 注册 Gossip 消息处理器（需要根据 MessageType 分发）
		// 注意：Transport.Receive 只能注册一个处理器
		// 这里需要修改为统一的消息分发器，或者使用包装器
		// 暂时使用注释标记，TODO: 实现消息分发器
		fmt.Printf("[INFO] Gossip message handler registered (TODO: implement message dispatcher)\n")
	}

	// P0-2: 启动时恢复未完成事务
	if err := coordinator.recoverTransactions(); err != nil {
		// 恢复失败不阻塞启动，只记录警告
		fmt.Printf("[WARN] Failed to recover transactions: %v\n", err)
	}

	// P0-1: 启动周期性 Gossip 状态同步任务
	coordinator.startGossipTask()

	return coordinator, nil
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

	// P0-1: 设置协调者信息（本地节点为协调者）
	tx.Coordinator = c.localNodeID

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

	// P0-2: 持久化 PreCommit 状态（关键：用于故障恢复）
	if err := c.persistTransaction(tx); err != nil {
		// 持久化失败记录日志，但不阻塞事务（内存优先）
		fmt.Printf("[WARN] Failed to persist PreCommit state for tx %s: %v\n", tx.TxID, err)
	}

	// 计算 Merkle Hash（预提交阶段先计算）
	if err := c.precomputeMerkleHash(tx); err != nil {
		tx.State = TxStateRolledBack
		tx.LastError = err
		return fmt.Errorf("precompute Merkle Hash failed: %w", err)
	}

	// 实际场景：通过网络发送 PreCommit 请求给所有参与者
	// 本地模式（c.transport == nil）：跳过网络发送，直接进入 PreCommit 状态
	if c.transport == nil {
		// 本地测试模式：模拟所有参与者的 ACK 响应
		for _, participant := range tx.Participants {
			tx.Acks[participant] = true
		}
		return nil
	}

	// 构建并发送 PreCommit 消息
	// 直接转换操作为传输层格式
	operations := make([]transport.Operation, len(tx.Operations))
	for i, op := range tx.Operations {
		operations[i] = transport.Operation{
			Type:  "put", // 目前只支持 put 操作
			Key:   op.Key,
			Value: op.Value,
		}
	}

	preparePayload := &transport.TwoPCPreparePayload{
		TxID:        tx.TxID,
		Operations:  operations,
		Timeout:     int64(tx.Timeout.Milliseconds()),
		Coordinator: c.localNodeID,
	}

	msg := &transport.Message{
		Type:    transport.MessageTypeSync, // MessageTypeSync = TwoPCPreparePayload
		From:    c.localNodeID,
		Payload: nil, // 将在 EncodePayload 中填充
	}

	// 编码 Payload
	if err := msg.EncodePayload(preparePayload); err != nil {
		tx.State = TxStateRolledBack
		tx.LastError = err
		return fmt.Errorf("编码 PreCommit 消息失败: %w", err)
	}

	// 并发发送给所有参与者（带重试机制）
	var wg sync.WaitGroup
	var mu sync.Mutex
	successCount := 0
	failureCount := 0

	for _, participant := range tx.Participants {
		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()

			// 设置目标节点
			msgClone := *msg
			msgClone.To = nodeID

			// 使用 sendWithRetry 发送 PreCommit 请求
			if err := c.sendWithRetry(ctx, nodeID, msgClone.Payload); err != nil {
				mu.Lock()
				failureCount++
				mu.Unlock()

				// 检查是否需要回滚
				mu.Lock()
				canContinue := (failureCount < len(tx.Participants)-1) // 仍有其他节点可用
				mu.Unlock()

				if !canContinue {
					// 大部分节点失败，无法继续
					tx.State = TxStateRolledBack
					tx.LastError = fmt.Errorf("PreCommit 失败：多数派节点不可达")
					return
				}
				return
			}

			// 成功发送
			mu.Lock()
			successCount++
			mu.Unlock()
		}(participant)
	}

	// 等待所有发送完成
	wg.Wait()

	// 检查结果
	if successCount == 0 {
		tx.State = TxStateRolledBack
		tx.LastError = fmt.Errorf("PreCommit 失败：所有节点发送失败")
		return fmt.Errorf("PreCommit 失败：所有节点发送失败")
	}

	if successCount < len(tx.Participants) {
		// 部分节点失败，记录警告但不回滚（等待响应处理）
		fmt.Printf("警告：PreCommit 阶段部分失败：%d/%d 节点成功\n", successCount, len(tx.Participants))
	}

	// 等待 ACK 响应（通过 Receive 处理器处理）
	// 注意：这里不阻塞等待，而是异步处理响应
	// 实际的 ACK 收集在 handlePreCommitResponse 中处理

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

	// P0-2: 持久化事务状态（可选：清理时删除）
	_ = c.persistTransaction(tx) // 忽略错误，不阻塞提交流程

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

	// P0-2: 持久化回滚状态（可选：清理时删除）
	_ = c.persistTransaction(tx) // 忽略错误，不阻塞回滚流程

	// 清除事务
	delete(c.transactions, tx.TxID)
}

// handlePreCommitResponse 处理 PreCommit ACK 响应
// 接收参与者返回的投票（YES/NO），更新事务状态
func (c *TwoPCMerkleCoordinator) handlePreCommitResponse(nodeID string, msg []byte) {
	codec := transport.NewMessagePackCodec()
	message, err := codec.DecodeFromBytes(msg)
	if err != nil {
		return
	}

	payload, err := message.DecodePayload()
	if err != nil {
		return
	}

	switch message.Type {
	case transport.MessageTypeAck: // TwoPCCommitPayload
		commitPayload, ok := payload.(*transport.TwoPCCommitPayload)
		if !ok {
			return
		}

		c.mu.Lock()
		tx, ok := c.transactions[commitPayload.TxID]
		c.mu.Unlock()

		if ok {
			tx.Acks[nodeID] = commitPayload.Result
		}

	case transport.MessageTypeNack: // TwoPCRollbackPayload
		rollbackPayload, ok := payload.(*transport.TwoPCRollbackPayload)
		if !ok {
			return
		}

		c.mu.Lock()
		tx, ok := c.transactions[rollbackPayload.TxID]
		c.mu.Unlock()

		if ok {
			tx.Acks[nodeID] = false
			tx.LastError = fmt.Errorf("节点 %s 拒绝: %s", nodeID, rollbackPayload.Reason)
		}
	}
}

// ==================== 重试机制辅助方法 ====================

// isRetryableError 判断错误是否可重试
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// 网络超时可重试
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// 上下文取消不可重试
	if errors.Is(err, context.Canceled) {
		return false
	}

	// 连接相关错误可重试
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary")
}

// sendWithRetry 带重试的消息发送
func (c *TwoPCMerkleCoordinator) sendWithRetry(ctx context.Context, nodeID string, payload []byte) error {
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// 等待重试延迟
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay):
				// 继续重试
			}
		}

		// 尝试发送
		err := c.transport.Send(nodeID, payload)
		if err == nil {
			return nil // 发送成功
		}

		lastErr = err

		// 检查是否可重试
		if !isRetryableError(err) {
			break // 不可重试，直接返回
		}
	}

	if lastErr != nil {
		return fmt.Errorf("send failed after %d attempts: %w", c.maxRetries+1, lastErr)
	}
	return lastErr
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

// ==================== P0-1: Gossip 状态同步 ====================

// gossipTransactionStates 周期性 Gossip 状态扩散
//
// 每 5 秒向其他节点扩散当前节点的事务状态，用于故障恢复场景
// 协调者故障时，参与者可以通过 Gossip 查询事务决策
func (c *TwoPCMerkleCoordinator) gossipTransactionStates() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 没有进行中的事务，跳过
	if len(c.transactions) == 0 {
		return nil
	}

	// 遍历所有进行中的事务
	for _, tx := range c.transactions {
		// 只扩散非终态的事务
		if tx.State == TxStateCommitted || tx.State == TxStateRolledBack {
			continue
		}

		// 构造 Gossip 状态消息
		payload := &transport.TwoPCGossipPayload{
			Phase:       "state",
			TxID:        tx.TxID,
			State:       tx.State.String(),
			Coordinator: tx.Coordinator,
			Timestamp:   time.Now().UnixNano(), // 使用纳秒时间戳
			MessageID:   uint64(time.Now().UnixNano()),
		}

		// 序列化消息
		msg := transport.NewMessage(transport.MessageTypeTwoPCGossip)
		msg.From = c.localNodeID
		if err := msg.EncodePayload(payload); err != nil {
			// 记录错误，继续处理下一个事务
			fmt.Printf("[WARN] Failed to encode gossip state message for tx %s: %v\n", tx.TxID, err)
			continue
		}

		// 广播给所有参与者（排除自己）
		for _, participant := range tx.Participants {
			if participant == c.localNodeID {
				continue
			}

			// 使用异步发送，避免阻塞
			go func(nodeID string, msgPayload []byte) {
				if err := c.transport.Send(nodeID, msgPayload); err != nil {
					fmt.Printf("[DEBUG] Failed to send gossip state to %s for tx %s: %v\n", nodeID, tx.TxID, err)
				}
			}(participant, msg.Payload)
		}
	}

	return nil
}

// queryTransactionDecision 通过 Gossip 查询事务决策
//
// 协调者故障时，参与者调用此方法向其他节点查询事务的最终决策
// timeout: 查询超时时间（默认 10 秒）
func (c *TwoPCMerkleCoordinator) queryTransactionDecision(txID string, timeout time.Duration) (TransactionState, error) {
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	// 先检查本地是否有该事务
	c.mu.RLock()
	if tx, ok := c.transactions[txID]; ok {
		// 本地有事务，直接返回状态
		state := tx.State
		c.mu.RUnlock()
		return state, nil
	}
	c.mu.RUnlock()

	// 本地没有事务，向其他节点查询
	// TODO: 实现集群节点查询
	// 需要：
	// 1. 从 ClusterManager 获取所有在线节点
	// 2. 广播查询消息
	// 3. 等待第一个响应或超时
	// 4. 返回查询结果

	return TxStateInit, errors.New("gossip query not implemented: no cluster manager integration")
}

// handleGossipStateMessage 处理接收到的 Gossip 状态消息
//
// 其他节点扩散的事务状态，用于本地更新和决策
func (c *TwoPCMerkleCoordinator) handleGossipStateMessage(nodeID string, msgBytes []byte) error {
	// 直接解码 Payload（Transport 传递的是 msg.Payload，不包含 TLV 头）
	var gossipPayload transport.TwoPCGossipPayload
	if err := msgpack.Unmarshal(msgBytes, &gossipPayload); err != nil {
		return fmt.Errorf("failed to decode gossip state payload: %w", err)
	}

	// 验证消息类型
	if gossipPayload.Phase != "state" {
		return fmt.Errorf("expected phase 'state', got '%s'", gossipPayload.Phase)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 检查本地是否有该事务
	tx, ok := c.transactions[gossipPayload.TxID]
	if !ok {
		// 本地没有该事务，记录日志并忽略
		fmt.Printf("[DEBUG] Received gossip state for unknown tx %s from %s\n", gossipPayload.TxID, nodeID)
		return nil
	}

	// 本地有该事务，更新协调者信息（如果本地没有）
	if tx.Coordinator == "" && gossipPayload.Coordinator != "" {
		tx.Coordinator = gossipPayload.Coordinator
		fmt.Printf("[INFO] Updated coordinator for tx %s to %s\n", gossipPayload.TxID, gossipPayload.Coordinator)
	}

	// 如果远程状态比本地新，考虑更新（需要状态转换验证，P1-2 实现）
	// TODO: 使用 HLC 时间戳比较和状态转换验证（P1-2）

	return nil
}

// handleGossipQueryMessage 处理接收到的 Gossip 查询消息
//
// 其他节点查询事务决策，本地有则返回
func (c *TwoPCMerkleCoordinator) handleGossipQueryMessage(nodeID string, msgBytes []byte) error {
	// 直接解码 Payload（Transport 传递的是 msg.Payload，不包含 TLV 头）
	var gossipPayload transport.TwoPCGossipPayload
	if err := msgpack.Unmarshal(msgBytes, &gossipPayload); err != nil {
		return fmt.Errorf("failed to decode gossip query payload: %w", err)
	}

	// 验证消息类型
	if gossipPayload.Phase != "query" {
		return fmt.Errorf("expected phase 'query', got '%s'", gossipPayload.Phase)
	}

	c.mu.RLock()
	tx, ok := c.transactions[gossipPayload.TxID]
	c.mu.RUnlock()

	// 构造响应消息
	replyPayload := &transport.TwoPCGossipPayload{
		Phase:     "reply",
		TxID:      gossipPayload.TxID,
		Requester: gossipPayload.Requester,
		Success:   ok, // 是否找到事务
		MessageID: uint64(time.Now().UnixNano()),
	}

	// 如果找到事务，填充状态信息
	if ok {
		replyPayload.State = tx.State.String()
		replyPayload.Coordinator = tx.Coordinator
		replyPayload.Timestamp = time.Now().UnixNano()
	}

	// 发送响应
	replyMsg := transport.NewMessage(transport.MessageTypeTwoPCGossip)
	replyMsg.From = c.localNodeID
	if err := replyMsg.EncodePayload(replyPayload); err != nil {
		return fmt.Errorf("failed to encode gossip reply message: %w", err)
	}

	// 同步发送响应（查询需要及时响应）
	if err := c.transport.Send(nodeID, replyMsg.Payload); err != nil {
		return fmt.Errorf("failed to send gossip reply to %s: %w", nodeID, err)
	}

	return nil
}

// handleGossipReplyMessage 处理接收到的 Gossip 响应消息
//
// 其他节点返回的查询结果
func (c *TwoPCMerkleCoordinator) handleGossipReplyMessage(nodeID string, msgBytes []byte) error {
	// 直接解码 Payload（Transport 传递的是 msg.Payload，不包含 TLV 头）
	var gossipPayload transport.TwoPCGossipPayload
	if err := msgpack.Unmarshal(msgBytes, &gossipPayload); err != nil {
		return fmt.Errorf("failed to decode gossip reply payload: %w", err)
	}

	// 验证消息类型
	if gossipPayload.Phase != "reply" {
		return fmt.Errorf("expected phase 'reply', got '%s'", gossipPayload.Phase)
	}

	// 验证是否是发给我的响应
	if gossipPayload.Requester != c.localNodeID {
		// 不是发给我的，忽略
		return nil
	}

	// 如果查询成功，更新本地事务状态（如果有）
	if gossipPayload.Success {
		c.mu.Lock()
		defer c.mu.Unlock()

		if tx, ok := c.transactions[gossipPayload.TxID]; ok {
			// 更新协调者信息
			if tx.Coordinator == "" && gossipPayload.Coordinator != "" {
				tx.Coordinator = gossipPayload.Coordinator
			}

			// TODO: 使用 HLC 时间戳比较和状态转换验证（P1-2）
			// 如果远程状态比本地新，考虑更新
			fmt.Printf("[INFO] Received gossip reply for tx %s: state=%s from %s\n",
				gossipPayload.TxID, gossipPayload.State, nodeID)
		}
	}

	return nil
}

// handleGossipMessage 统一的 Gossip 消息处理器
//
// 根据 Phase 字段分发到不同的处理器
func (c *TwoPCMerkleCoordinator) handleGossipMessage(nodeID string, msgBytes []byte) {
	// 快速解码 payload 以获取 Phase（Transport 传递的是 msg.Payload，不包含 TLV 头）
	var gossipPayload transport.TwoPCGossipPayload
	if err := msgpack.Unmarshal(msgBytes, &gossipPayload); err != nil {
		fmt.Printf("[WARN] Failed to decode gossip payload from %s: %v\n", nodeID, err)
		return
	}

	// 根据 Phase 分发到不同的处理器
	switch gossipPayload.Phase {
	case "state":
		if err := c.handleGossipStateMessage(nodeID, msgBytes); err != nil {
			fmt.Printf("[DEBUG] Failed to handle gossip state message from %s: %v\n", nodeID, err)
		}
	case "query":
		if err := c.handleGossipQueryMessage(nodeID, msgBytes); err != nil {
			fmt.Printf("[DEBUG] Failed to handle gossip query message from %s: %v\n", nodeID, err)
		}
	case "reply":
		if err := c.handleGossipReplyMessage(nodeID, msgBytes); err != nil {
			fmt.Printf("[DEBUG] Failed to handle gossip reply message from %s: %v\n", nodeID, err)
		}
	default:
		fmt.Printf("[WARN] Unknown gossip phase '%s' from %s\n", gossipPayload.Phase, nodeID)
	}
}

// startGossipTask 启动周期性 Gossip 状态同步任务
func (c *TwoPCMerkleCoordinator) startGossipTask() {
	if c.transport == nil {
		// 本地模式，无需 Gossip
		return
	}

	c.gossipWaitGroup.Add(1)
	go func() {
		defer c.gossipWaitGroup.Done()

		ticker := time.NewTicker(c.gossipInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.gossipStopChan:
				// 收到停止信号
				return
			case <-ticker.C:
				// 周期性 Gossip 状态扩散
				if err := c.gossipTransactionStates(); err != nil {
					fmt.Printf("[DEBUG] Gossip transaction states failed: %v\n", err)
				}
			}
		}
	}()

	fmt.Printf("[INFO] Started gossip task with interval %v\n", c.gossipInterval)
}

// stopGossipTask 停止 Gossip 状态同步任务
func (c *TwoPCMerkleCoordinator) stopGossipTask() {
	if c.gossipStopChan != nil {
		close(c.gossipStopChan)
		c.gossipWaitGroup.Wait()
		fmt.Printf("[INFO] Stopped gossip task\n")
	}
}

// Close 关闭协调器
func (c *TwoPCMerkleCoordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	// P0-1: 停止 Gossip 任务
	c.stopGossipTask()

	for _, tx := range c.transactions {
		c.rollbackTransaction(tx)
	}

	return nil
}

// ==================== P0-2: 事务状态持久化 ====================

// persistTransaction 持久化事务状态到 MetadataKV
//
// 容错设计：
//   - 持久化失败：记录错误日志，继续内存操作
//   - 序列化失败：记录错误，不影响其他事务
func (c *TwoPCMerkleCoordinator) persistTransaction(tx *TwoPCTransaction) error {
	if c.metadataKV == nil {
		return nil // 无持久化存储，跳过
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 使用 Store 接口的 Put 方法（自动序列化）
	err := c.metadataKV.Put(ctx, kvstore.NamespaceTx, tx.TxID, tx)
	if err != nil {
		// 记录错误日志，但不阻塞事务流程
		fmt.Printf("[ERROR] Failed to persist transaction %s: %v\n", tx.TxID, err)
		return err
	}

	return nil
}

// loadTransaction 从 MetadataKV 加载事务状态
//
// 容错设计：
//   - 加载失败：返回错误，调用者决定是否跳过
//   - 反序列化失败：返回错误
func (c *TwoPCMerkleCoordinator) loadTransaction(txID string) (*TwoPCTransaction, error) {
	if c.metadataKV == nil {
		return nil, errors.New("metadataKV is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tx TwoPCTransaction
	err := c.metadataKV.Get(ctx, kvstore.NamespaceTx, txID, &tx)
	if err != nil {
		return nil, err
	}

	return &tx, nil
}

// recoverTransactions 启动时恢复未完成事务
//
// 恢复策略：
//   - 只恢复 PreCommit 状态的事务（非终态）
//   - 跳过已提交/已回滚/已超时的事务
//
// 容错设计：
//   - 加载失败：跳过该事务，不影响其他事务恢复
//   - 列出失败：返回错误，上层决定是否继续启动
func (c *TwoPCMerkleCoordinator) recoverTransactions() error {
	if c.metadataKV == nil {
		return nil // 无持久化存储，跳过恢复
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 列出所有事务键
	keys, err := c.metadataKV.ListPrefix(ctx, kvstore.NamespaceTx, "")
	if err != nil {
		return fmt.Errorf("failed to list transactions: %w", err)
	}

	recovered := 0
	skipped := 0

	for _, key := range keys {
		// key 格式：meta:tx:{tx_id}，这里 key 已经是完整键
		// 提取 txID（去掉命名空间前缀）
		txID := strings.TrimPrefix(key, kvstore.NamespaceTx)
		if txID == key {
			// 没有命名空间前缀，跳过
			continue
		}

		// 加载事务
		tx, err := c.loadTransaction(txID)
		if err != nil {
			fmt.Printf("[WARN] Failed to load transaction %s: %v\n", txID, err)
			skipped++
			continue
		}

		// 只恢复 PreCommit 状态的事务（非终态）
		// 终态事务（Committed/RolledBack/Timeout）不需要恢复
		if tx.State != TxStatePreCommit {
			fmt.Printf("[DEBUG] Skipping transaction %s with state %v\n", txID, tx.State)
			skipped++
			continue
		}

		// 恢复到内存中
		c.mu.Lock()
		c.transactions[tx.TxID] = tx
		c.mu.Unlock()

		recovered++
		fmt.Printf("[INFO] Recovered transaction %s (state=%v, participants=%d)\n",
			tx.TxID, tx.State, len(tx.Participants))
	}

	fmt.Printf("[INFO] Transaction recovery completed: recovered=%d, skipped=%d\n", recovered, skipped)
	return nil
}

// deleteTransaction 删除持久化的事务记录
//
// 用于清理已完成的事务，释放存储空间
func (c *TwoPCMerkleCoordinator) deleteTransaction(txID string) error {
	if c.metadataKV == nil {
		return nil // 无持久化存储，跳过
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return c.metadataKV.Delete(ctx, kvstore.NamespaceTx, txID)
}
