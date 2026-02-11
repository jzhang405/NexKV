// Package quorum 提供 Quorum 一致性服务集成
//
// 核心功能：
//   - ACK 大部分（need = ⌊n/2⌋ + 1）：多数派确认即可
//   - 增强最终一致：介于 2PC 和 Gossip 之间
//   - 角色变更专用：NamespaceRole 升级为 Quorum
package quorum

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// ==================== QuorumCoordinator ====================

// QuorumCoordinator Quorum 一致性协调器
//
// ACK 要求：ACK 大部分（need = ⌊n/2⌋ + 1）
// 特征：多数派确认即可，少数失败不影响提交
// 适用场景：重要变更（角色变更、拓扑调整）
type QuorumCoordinator struct {
	mu           sync.RWMutex
	participants []string          // 参与者节点 ID 列表
	quorum       int               // Quorum 阈值（多数派）
	timeout      time.Duration     // 超时时间
	metadataKV   *kvstore.MetadataKV // 元数据存储
}

// NewQuorumCoordinator 创建 Quorum 协调器
func NewQuorumCoordinator(
	participants []string,
	metadataKV *kvstore.MetadataKV,
) *QuorumCoordinator {
	quorum := calculateQuorum(len(participants))

	q := &QuorumCoordinator{
		participants: participants,
		quorum:       quorum,
		timeout:      3 * time.Second, // 默认 3 秒（介于 2PC 的 5 秒和 Gossip 的异步之间）
		metadataKV:   metadataKV,
	}
	return q
}

// PutWithQuorum 使用 Quorum 机制写入
//
// 流程：
//  1. 发送 Propose 请求
//  2. 等待多数派 ACK
//  3. 达到 Quorum 后提交
//  4. 更新 Merkle Tree
//
// 返回：error 提交失败原因
func (q *QuorumCoordinator) PutWithQuorum(
	ctx context.Context,
	ns, key string,
	value any,
	opts *PutOptions,
) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	logging.WithFields(map[string]interface{}{
		"namespace":   ns,
		"key":         key,
		"participants": len(q.participants),
		"quorum":      q.quorum,
	}).Info("Quorum 写入开始")

	// 1. 写入本地元数据
	err := q.metadataKV.Put(ctx, ns, key, value)
	if err != nil {
		return fmt.Errorf("本地写入失败: %w", err)
	}

	// 2. 等待多数派 ACK
	acks := 0

	// TODO: 实际的 Quorum 确认逻辑
	// 当前简化实现：模拟 Quorum 确认
	for _, participant := range q.participants {
		if participant != "local" { // 跳过本地节点
			// TODO: 发送 Propose 请求并等待 ACK
			acks++
			if acks >= q.quorum {
				break // 达到 Quorum
			}
		}
	}

	// 3. 检查是否达到 Quorum
	if acks >= q.quorum {
		logging.WithFields(map[string]interface{}{
			"namespace": ns,
			"key":       key,
			"acks":      acks,
			"quorum":    q.quorum,
		}).Info("Quorum 确认成功")

		// 4. 更新 Merkle Tree（如果需要）
		if opts != nil && !opts.SkipMerkleUpdate {
			// TODO: 更新 Merkle Tree
		}

		return nil
	}

	return fmt.Errorf("Quorum 确认失败: %d/%d", acks, q.quorum)
}

// calculateQuorum 计算 Quorum 数量
// 公式：need = ⌊n/2⌋ + 1
func calculateQuorum(n int) int {
	if n <= 0 {
		return 0
	}
	return (n / 2) + 1
}

// GetQuorum 获取 Quorum 阈值
func (q *QuorumCoordinator) GetQuorum() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.quorum
}

// GetParticipants 获取参与者列表
func (q *QuorumCoordinator) GetParticipants() []string {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.participants
}

// SetParticipants 设置参与者列表
func (q *QuorumCoordinator) SetParticipants(participants []string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.participants = participants
	q.quorum = calculateQuorum(len(participants))
}

// ==================== PutOptions ====================

// PutOptions Quorum 写入选项
type PutOptions struct {
	// Timeout 超时时间（毫秒）
	// 默认 3000ms（3 秒）
	Timeout int64

	// Participants 参与者节点 ID 列表
	// 如果为空，使用默认参与者列表
	Participants []string

	// SkipMerkleUpdate 是否跳过 Merkle Tree 更新
	// 用于批量操作场景，手动控制 Merkle Tree 更新时机
	SkipMerkleUpdate bool

	// Async 是否异步执行
	// 异步模式下，方法立即返回，实际操作在后台执行
	Async bool
}

// ==================== QuorumResult ====================

// QuorumResult Quorum 结果
type QuorumResult struct {
	Success      bool      // 是否达到 Quorum
	AckCount     int       // ACK 数量
	TotalPeers   int       // 总 peer 数
	Quorum       int       // Quorum 阈值
	AckedPeers   []string  // 确认的 peer 列表
	FailedPeers  []string  // 失败的 peer 列表
	Latency      time.Duration // 操作延迟
	Error        error     // 错误
}

// IsQuorumReached 判断是否达到 Quorum
func (r *QuorumResult) IsQuorumReached() bool {
	return r.Success
}

// GetSuccessRate 获取成功率
func (r *QuorumResult) GetSuccessRate() float64 {
	if r.TotalPeers == 0 {
		return 0
	}
	return float64(r.AckCount) / float64(r.TotalPeers)
}

// ==================== 辅助函数 ====================

// BuildQuorumProposePayload 构建 Quorum Propose Payload
func BuildQuorumProposePayload(
	proposalID string,
	ns, key string,
	value []byte,
) map[string]interface{} {
	return map[string]interface{}{
		"phase":       "propose",
		"proposal_id":  proposalID,
		"namespace":   ns,
		"key":         key,
		"value":       value,
	}
}

// BuildQuorumVotePayload 构建 Quorum Vote Payload
func BuildQuorumVotePayload(
	proposalID string,
	voter string,
	decision bool,
) map[string]interface{} {
	return map[string]interface{}{
		"phase":       "vote",
		"proposal_id":  proposalID,
		"voter":       voter,
		"decision":    decision,
	}
}

// BuildQuorumDecidePayload 构建 Quorum Decide Payload
func BuildQuorumDecidePayload(
	proposalID string,
	decision bool,
	quorum int,
) map[string]interface{} {
	return map[string]interface{}{
		"phase":       "decide",
		"proposal_id":  proposalID,
		"decision":    decision,
		"quorum":      quorum,
	}
}
