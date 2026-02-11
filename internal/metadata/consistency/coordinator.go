// Package consistency 提供元数据一致性协调器接口
//
// 核心功能：
//   - 三级一致性模型：2PC（强一致）→ Quorum（增强最终一致）→ Gossip（最终一致）
//   - ACK 要求：2PC（ACK 全部）、Quorum（ACK 大部分）、Gossip（无 ACK）
//   - 统一接口：根据元数据类型自动选择一致性机制
package consistency

import (
	"context"

	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// ==================== ConsistencyLevel ====================

// ConsistencyLevel 一致性级别枚举
type ConsistencyLevel int

const (
	// ConsistencyStrong2PC 强一致（2PC）
	// ACK 要求：ACK 全部（need = n）
	// 特征：所有参与者必须确认，任一失败则全部回滚
	// 适用场景：关键变更（分片创建、主副本切换、节点加入）
	ConsistencyStrong2PC ConsistencyLevel = iota

	// ConsistencyEnhancedEventual 增强最终一致（Quorum）
	// ACK 要求：ACK 大部分（need = ⌊n/2⌋ + 1）
	// 特征：多数派确认即可，少数失败不影响提交
	// 适用场景：重要变更（角色变更、拓扑调整）
	ConsistencyEnhancedEventual

	// ConsistencyEventual 最终一致（Gossip）
	// ACK 要求：无 ACK（need = 0）
	// 特征：异步扩散，10秒内最终一致
	// 适用场景：普通变更（状态更新、负载信息刷新）
	ConsistencyEventual
)

// String 返回一致性级别的字符串表示
func (c ConsistencyLevel) String() string {
	switch c {
	case ConsistencyStrong2PC:
		return "2PC-Strong"
	case ConsistencyEnhancedEventual:
		return "Quorum-EnhancedEventual"
	case ConsistencyEventual:
		return "Gossip-Eventual"
	default:
		return "Unknown"
	}
}

// ACKRequirement 返回 ACK 要求描述
func (c ConsistencyLevel) ACKRequirement() string {
	switch c {
	case ConsistencyStrong2PC:
		return "ACK 全部 (need = n)"
	case ConsistencyEnhancedEventual:
		return "ACK 大部分 (need = ⌊n/2⌋ + 1)"
	case ConsistencyEventual:
		return "无 ACK (need = 0)"
	default:
		return "Unknown"
	}
}

// ==================== ConsistencyCoordinator ====================

// ConsistencyCoordinator 一致性协调器接口
//
// 职责：
//   - 根据元数据类型自动选择一致性机制
//   - 提供 2PC、Quorum、Gossip 三种写入方式
//   - 与 Merkle Tree 协同，实现增量同步
type ConsistencyCoordinator interface {
	// PutWith2PC 使用 2PC 强一致机制写入
	//
	// ACK 要求：ACK 全部（need = n）
	// 特征：所有参与者必须确认，任一失败则全部回滚
	// 适用场景：关键变更（分片创建、主副本切换、节点加入）
	//
	// 流程：
	//  1. Pre-Commit 阶段：暂存到 Merkle Tree Pending 状态
	//  2. 等待所有节点 ACK
	//  3. Commit 阶段：批量应用并更新 Hash
	//  4. Rollback 阶段：清除暂存（任一失败）
	//
	// 返回：error 提交失败或回滚原因
	PutWith2PC(ctx context.Context, ns, key string, value any, opts *PutOptions) error

	// PutWithQuorum 使用 Quorum 增强最终一致机制写入
	//
	// ACK 要求：ACK 大部分（need = ⌊n/2⌋ + 1）
	// 特征：多数派确认即可，少数失败不影响提交
	// 适用场景：重要变更（角色变更、拓扑调整）
	//
	// 流程：
	//  1. 发送 Propose 请求
	//  2. 等待多数派 ACK
	//  3. 达到 Quorum 后提交
	//  4. 更新 Merkle Tree
	//
	// 返回：error 提交失败原因
	PutWithQuorum(ctx context.Context, ns, key string, value any, opts *PutOptions) error

	// PutWithGossip 使用 Gossip 最终一致机制写入
	//
	// ACK 要求：无 ACK（need = 0）
	// 特征：异步扩散，10秒内最终一致
	// 适用场景：普通变更（状态更新、负载信息刷新）
	//
	// 流程：
	//  1. 写入本地元数据
	//  2. 立即更新 Merkle Tree
	//  3. 异步 Gossip 扩散
	//
	// 返回：error 写入失败原因（不等待同步）
	PutWithGossip(ctx context.Context, ns, key string, value any, opts *PutOptions) error

	// GetConsistencyLevel 获取指定 Namespace 的一致性级别
	//
	// 映射关系（基于 Pre 文档 v2.1）：
	//   - NamespaceCluster: ConsistencyStrong2PC
	//   - NamespaceShard:   ConsistencyStrong2PC
	//   - NamespaceNode:    ConsistencyEventual
	//   - NamespaceRole:    ConsistencyEnhancedEventual (从 Gossip 升级)
	//   - NamespaceStatic:  ConsistencyStrong2PC
	//   - NamespaceTopo:    ConsistencyEventual
	//   - NamespaceDynamic: ConsistencyEventual
	//   - NamespaceOp:      ConsistencyEventual
	//   - NamespaceVersion: ConsistencyStrong2PC
	GetConsistencyLevel(ns string) ConsistencyLevel

	// Close 关闭协调器
	Close() error
}

// ==================== PutOptions ====================

// PutOptions 写入选项
type PutOptions struct {
	// Timeout 超时时间（毫秒）
	// 2PC 超时：5 秒（5000ms）
	// Quorum 超时：3 秒（3000ms）
	// Gossip 超时：不适用（异步）
	Timeout int64

	// Participants 参与者节点 ID 列表
	// 用于 2PC 和 Quorum，指定需要确认的节点
	// 如果为空，自动选择（2PC：全员，Quorum：多数派）
	Participants []string

	// SkipMerkleUpdate 是否跳过 Merkle Tree 更新
	// 用于批量操作场景，手动控制 Merkle Tree 更新时机
	SkipMerkleUpdate bool

	// Async 是否异步执行
	// 异步模式下，方法立即返回，实际操作在后台执行
	Async bool
}

// ==================== Namespace 一致性级别映射 ====================

// DefaultConsistencyMapping 默认的 Namespace 一致性级别映射
//
// 注意：这是 Pre 文档 v2.1 中定义的**目标映射**
// 现有代码（metadata_kv.go）中的映射是**现有映射**
// 差异：NamespaceRole 从 Gossip 升级为 Quorum
var DefaultConsistencyMapping = map[string]ConsistencyLevel{
	kvstore.NamespaceCluster: ConsistencyStrong2PC,        // 集群配置：强一致（2PC）
	kvstore.NamespaceShard:   ConsistencyStrong2PC,        // 分片信息：强一致（2PC）
	kvstore.NamespaceNode:    ConsistencyEventual,         // 节点信息：最终一致（Gossip）
	kvstore.NamespaceRole:    ConsistencyEnhancedEventual, // 角色信息：增强最终一致（Quorum）⚠️ 从 Gossip 升级
	kvstore.NamespaceStatic:  ConsistencyStrong2PC,        // 静态配置：强一致（2PC）
	kvstore.NamespaceTopo:    ConsistencyEventual,         // 拓扑关系：最终一致（Gossip）
	kvstore.NamespaceDynamic: ConsistencyEventual,         // 动态状态：最终一致（Gossip）
	kvstore.NamespaceOp:      ConsistencyEventual,         // 操作记录：最终一致（Gossip）
	kvstore.NamespaceVersion: ConsistencyStrong2PC,        // 版本控制：强一致（2PC）
}

// GetDefaultConsistencyLevel 获取指定 Namespace 的默认一致性级别
func GetDefaultConsistencyLevel(ns string) ConsistencyLevel {
	if level, ok := DefaultConsistencyMapping[ns]; ok {
		return level
	}
	return ConsistencyEventual // 默认最终一致
}

// ==================== 辅助函数 ====================

// CalculateQuorum 计算 Quorum 数量（多数派）
// 公式：need = ⌊n/2⌋ + 1
func CalculateQuorum(n int) int {
	if n <= 0 {
		return 0
	}
	return (n / 2) + 1
}

// CalculateQuorumParticipants 从参与者列表中选择 Quorum 数量的节点
func CalculateQuorumParticipants(allParticipants []string) []string {
	n := len(allParticipants)
	need := CalculateQuorum(n)

	if need >= n {
		return allParticipants
	}

	return allParticipants[:need]
}
