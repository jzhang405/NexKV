// Package types 定义内部通用的数据类型
//
// 避免各层之间的循环依赖，提供统一的类型定义
package types

import "fmt"

// ReliabilityRequirement 可靠性要求
type ReliabilityRequirement int

const (
	// BestEffort 尽力而为（容忍丢失）
	BestEffort ReliabilityRequirement = iota
	// Reliable 可靠传输（强依赖）
	Reliable
)

// ResponseExpectation 响应期望
type ResponseExpectation int

const (
	// NoResponse 不需要响应
	NoResponse ResponseExpectation = iota
	// ExpectResponse 需要响应
	ExpectResponse
)

// MessageType 消息类型
//
// 消息类型范围分配:
//   - 100-149: 元数据操作（Put、Delete、Get 等）
//   - 150-199: Gossip 协议消息
//   - 200-249: Quorum 协议消息
//   - 250-299: 2PC 协议消息
//   - 300-349: 节点管理消息
//   - 350-399: 集群管理消息
type MessageType uint16

const (
	// 元数据操作消息 (100-149)
	MessageTypeGet         MessageType = 100 // 获取元数据
	MessageTypePut         MessageType = 101 // 更新元数据
	MessageTypeDelete      MessageType = 102 // 删除元数据
	MessageTypeGetReply    MessageType = 103 // Get 响应
	MessageTypePutReply    MessageType = 104 // Put 响应
	MessageTypeDeleteReply MessageType = 105 // Delete 响应

	// Gossip 协议消息 (150-199)
	MessageTypeGossipSync        MessageType = 150 // Gossip 同步请求
	MessageTypeGossipSyncReply   MessageType = 151 // Gossip 同步响应
	MessageTypeGossipDigest      MessageType = 152 // Gossip 摘要
	MessageTypeGossipDigestReply MessageType = 153 // Gossip 摘要响应

	// Quorum 协议消息 (200-249)
	MessageTypeQuorumPropose MessageType = 200 // Quorum 提案
	MessageTypeQuorumVote    MessageType = 201 // Quorum 投票
	MessageTypeQuorumDecide  MessageType = 202 // Quorum 决策

	// 2PC 协议消息 (250-299)
	MessageType2PCPrepare       MessageType = 250 // 2PC 准备阶段
	MessageType2PCPrepareReply  MessageType = 251 // 2PC 准备响应
	MessageType2PCCommit        MessageType = 252 // 2PC 提交阶段
	MessageType2PCRollback      MessageType = 253 // 2PC 回滚阶段
	MessageType2PCCommitReply   MessageType = 254 // 2PC 提交响应
	MessageType2PCRollbackReply MessageType = 255 // 2PC 回滚响应

	// 节点管理消息 (300-349)
	MessageTypeNodePing       MessageType = 300 // 节点心跳
	MessageTypeNodePong       MessageType = 301 // 心跳响应
	MessageTypeNodeJoin       MessageType = 302 // 节点加入
	MessageTypeNodeLeave      MessageType = 303 // 节点离开
	MessageTypeNodeSync       MessageType = 304 // 节点同步
	MessageTypeClockSync      MessageType = 305 // 时钟同步请求
	MessageTypeClockSyncReply MessageType = 306 // 时钟同步响应

	// 集群管理消息 (350-399)
	MessageTypeClusterStatus      MessageType = 350 // 集群状态查询
	MessageTypeClusterStatusReply MessageType = 351 // 集群状态响应
	MessageTypeLeaderElection     MessageType = 352 // Leader 选举
)

// String 返回消息类型的字符串表示
func (t MessageType) String() string {
	switch t {
	case MessageTypeGet:
		return "Get"
	case MessageTypePut:
		return "Put"
	case MessageTypeDelete:
		return "Delete"
	case MessageTypeGetReply:
		return "GetReply"
	case MessageTypePutReply:
		return "PutReply"
	case MessageTypeDeleteReply:
		return "DeleteReply"
	case MessageTypeGossipSync:
		return "GossipSync"
	case MessageTypeGossipSyncReply:
		return "GossipSyncReply"
	case MessageTypeGossipDigest:
		return "GossipDigest"
	case MessageTypeGossipDigestReply:
		return "GossipDigestReply"
	case MessageTypeQuorumPropose:
		return "QuorumPropose"
	case MessageTypeQuorumVote:
		return "QuorumVote"
	case MessageTypeQuorumDecide:
		return "QuorumDecide"
	case MessageType2PCPrepare:
		return "2PCPrepare"
	case MessageType2PCPrepareReply:
		return "2PCPrepareReply"
	case MessageType2PCCommit:
		return "2PCCommit"
	case MessageType2PCRollback:
		return "2PCRollback"
	case MessageType2PCCommitReply:
		return "2PCCommitReply"
	case MessageType2PCRollbackReply:
		return "2PCRollbackReply"
	case MessageTypeNodePing:
		return "NodePing"
	case MessageTypeNodePong:
		return "NodePong"
	case MessageTypeNodeJoin:
		return "NodeJoin"
	case MessageTypeNodeLeave:
		return "NodeLeave"
	case MessageTypeNodeSync:
		return "NodeSync"
	case MessageTypeClockSync:
		return "ClockSync"
	case MessageTypeClockSyncReply:
		return "ClockSyncReply"
	case MessageTypeClusterStatus:
		return "ClusterStatus"
	case MessageTypeClusterStatusReply:
		return "ClusterStatusReply"
	case MessageTypeLeaderElection:
		return "LeaderElection"
	default:
		return "Unknown"
	}
}

// GetPriority 获取消息优先级
//
// 优先级分级:
//   - PriorityLow: 后台同步、非关键元数据更新
//   - PriorityNormal: 常规元数据操作、节点心跳（默认）
//   - PriorityHigh: 重要操作、Quorum 提案
//   - PriorityCritical: 系统关键操作、2PC 协议
func GetPriority(msgType MessageType) Priority {
	switch msgType {
	// 低优先级：后台同步、时钟同步、心跳
	case MessageTypeGossipDigest, MessageTypeGossipDigestReply,
		MessageTypeGossipSyncReply, MessageTypeNodePing,
		MessageTypeClockSync, MessageTypeClockSyncReply:
		return PriorityLow

	// 高优先级：2PC 提交/回滚
	case MessageType2PCCommit, MessageType2PCRollback,
		MessageType2PCCommitReply, MessageType2PCRollbackReply:
		return PriorityHigh

	// 关键优先级：Quorum 决策
	case MessageTypeQuorumDecide:
		return PriorityCritical

	// 普通优先级（默认）
	default:
		return PriorityNormal
	}
}

// Address 节点地址
type Address struct {
	Host string // 主机名或 IP
	Port int    // 端口号
}

// String 返回地址的字符串表示 (host:port)
func (a *Address) String() string {
	return fmt.Sprintf("%s:%d", a.Host, a.Port)
}

// ExpectResponse 返回消息是否期望响应
//
// 判断逻辑：
//   - Reply 结尾的消息类型不需要响应（本身是响应）
//   - Quorum 提案和投票需要响应
//   - Get/Put/Delete 操作需要响应
//   - Gossip 消息和心跳不需要响应（单向传播）
func (t MessageType) ExpectResponse() ResponseExpectation {
	// Reply 消息本身是响应，不需要再响应
	if t.isReplyMessage() {
		return NoResponse
	}

	// 需要响应的消息类型
	switch t {
	case MessageTypeGet, MessageTypePut, MessageTypeDelete,
		MessageTypeGossipSync, MessageTypeGossipDigest,
		MessageTypeQuorumPropose, MessageTypeQuorumVote,
		MessageType2PCPrepare, MessageType2PCCommit, MessageType2PCRollback,
		MessageTypeNodePing, MessageTypeNodeSync,
		MessageTypeClockSync, MessageTypeClusterStatus:
		return ExpectResponse
	}

	// 默认不需要响应（Gossip 单向传播、状态广播等）
	return NoResponse
}

// Reliability 返回消息的可靠性要求
//
// 判断逻辑：
//   - Quorum 协议消息需要可靠传输（提案、投票、决策）
//   - 2PC 协议消息需要可靠传输（准备、提交、回滚）
//   - 元数据操作需要可靠传输（Get、Put、Delete）
//   - Gossip 消息使用尽力而为（最终一致性）
//   - 心跳消息使用尽力而为（容忍丢失）
func (t MessageType) Reliability() ReliabilityRequirement {
	// 需要可靠传输的消息类型
	switch t {
	case MessageTypeGet, MessageTypePut, MessageTypeDelete,
		MessageTypeGetReply, MessageTypePutReply, MessageTypeDeleteReply,
		MessageTypeQuorumPropose, MessageTypeQuorumVote, MessageTypeQuorumDecide,
		MessageType2PCPrepare, MessageType2PCPrepareReply,
		MessageType2PCCommit, MessageType2PCCommitReply,
		MessageType2PCRollback, MessageType2PCRollbackReply:
		return Reliable
	}

	// 默认使用尽力而为（Gossip、心跳、时钟同步等）
	return BestEffort
}

// isReplyMessage 判断是否是响应消息
func (t MessageType) isReplyMessage() bool {
	switch t {
	case MessageTypeGetReply, MessageTypePutReply, MessageTypeDeleteReply,
		MessageTypeGossipSyncReply, MessageTypeGossipDigestReply,
		MessageType2PCPrepareReply, MessageType2PCCommitReply,
		MessageType2PCRollbackReply, MessageTypeNodePong,
		MessageTypeClockSyncReply, MessageTypeClusterStatusReply:
		return true
	default:
		return false
	}
}

// ========================================
// Message 接口定义
// ========================================

// Message 传输消息接口
//
// 所有传输的消息都需要实现此接口
type Message interface {
	// Type 返回消息类型
	Type() MessageType

	// Priority 返回消息优先级（0-4，0最低，4最高）
	// 用于流量控制：接收端过载时优先丢弃低优先级消息
	Priority() int

	// ExpectResponse 返回消息是否期望响应
	// 默认实现：调用 Type().ExpectResponse()
	ExpectResponse() ResponseExpectation

	// Reliability 返回消息的可靠性要求
	// 默认实现：调用 Type().Reliability()
	Reliability() ReliabilityRequirement
}
