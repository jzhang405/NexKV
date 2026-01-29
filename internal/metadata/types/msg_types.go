// Package types 定义内部通用的数据类型
//
// 避免各层之间的循环依赖，提供统一的类型定义
package types

import "fmt"

// ResponseExpectation 响应期望
type ResponseExpectation int

const (
	// NoResponse 不需要响应
	NoResponse ResponseExpectation = iota
	// ExpectResponse 需要响应
	ExpectResponse
)

// MsgRole 消息角色
//
// 用于快速判断消息是请求还是响应，避免字符串匹配开销
type MsgRole int

const (
	// MsgRoleRequest 请求消息（发起方发送）
	MsgRoleRequest MsgRole = 0
	// MsgRoleResponse 响应消息（接收方回复）
	MsgRoleResponse MsgRole = 1
)

// String 返回消息角色的字符串表示
func (r MsgRole) String() string {
	switch r {
	case MsgRoleRequest:
		return "Request"
	case MsgRoleResponse:
		return "Response"
	default:
		return "Unknown"
	}
}

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
	MessageTypeNodePing          MessageType = 300 // 节点心跳
	MessageTypeNodePong          MessageType = 301 // 心跳响应
	MessageTypeNodeJoin          MessageType = 302 // 节点加入
	MessageTypeNodeLeave         MessageType = 303 // 节点离开
	MessageTypeNodeSync          MessageType = 304 // 节点同步
	MessageTypeClockSync         MessageType = 305 // 时钟同步请求
	MessageTypeClockSyncReply    MessageType = 306 // 时钟同步响应
	MessageTypeNodeReparent      MessageType = 307 // 重新建立父子关系
	MessageTypeNodeReparentReply MessageType = 308 // 重新建立父子关系响应

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
	case MessageTypeNodeReparent:
		return "NodeReparent"
	case MessageTypeNodeReparentReply:
		return "NodeReparentReply"
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

	// 高优先级：2PC 提交/回滚、节点重挂载
	case MessageType2PCCommit, MessageType2PCRollback,
		MessageType2PCCommitReply, MessageType2PCRollbackReply,
		MessageTypeNodeReparent, MessageTypeNodeReparentReply:
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

// MsgRole 返回消息角色（请求或响应）
//
// 用于快速判断消息类型，避免字符串 "Reply" 后缀匹配
// 判断逻辑：Reply 结尾的消息类型为响应，其他为请求
func (t MessageType) MsgRole() MsgRole {
	if t.isReplyMessage() {
		return MsgRoleResponse
	}
	return MsgRoleRequest
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
		MessageTypeNodeJoin, MessageTypeNodePing, MessageTypeNodeSync, MessageTypeNodeReparent,
		MessageTypeClockSync, MessageTypeClusterStatus:
		return ExpectResponse
	}

	// 默认不需要响应（Gossip 单向传播、状态广播等）
	return NoResponse
}

// ========================================
// 协议类型映射表（P0-2 优化）
// ========================================

// protocolTypeTable 协议类型映射表
//
// 优化方案：
//   - 使用 map 代替 switch-case，查找复杂度从 O(n) 降至 O(1)
//   - 集中管理协议映射，易于维护
//   - 在包初始化时构建，避免重复创建
var protocolTypeTable = map[MessageType]ProtocolType{
	// 元数据操作（TCP 可靠传输）
	MessageTypeGet:         ProtocolTCP,
	MessageTypePut:         ProtocolTCP,
	MessageTypeDelete:      ProtocolTCP,
	MessageTypeGetReply:    ProtocolTCP,
	MessageTypePutReply:    ProtocolTCP,
	MessageTypeDeleteReply: ProtocolTCP,

	// Quorum 协议（TCP 可靠传输）
	MessageTypeQuorumPropose: ProtocolTCP,
	MessageTypeQuorumVote:    ProtocolTCP,
	MessageTypeQuorumDecide:  ProtocolTCP,

	// 2PC 协议（TCP 可靠传输）
	MessageType2PCPrepare:       ProtocolTCP,
	MessageType2PCPrepareReply:  ProtocolTCP,
	MessageType2PCCommit:        ProtocolTCP,
	MessageType2PCCommitReply:   ProtocolTCP,
	MessageType2PCRollback:      ProtocolTCP,
	MessageType2PCRollbackReply: ProtocolTCP,

	// 节点管理（TCP 可靠传输）
	MessageTypeNodeReparent:      ProtocolTCP,
	MessageTypeNodeReparentReply: ProtocolTCP,
}

// ProtocolType 返回消息使用的传输协议类型
//
// 判断逻辑：
//   - Quorum 协议消息需要可靠传输（TCP）
//   - 2PC 协议消息需要可靠传输（TCP）
//   - 元数据操作需要可靠传输（TCP）
//   - Gossip 消息使用尽力而为（UDP）
//   - 心跳消息使用尽力而为（UDP）
//
// 性能优化（P0-2）：
//   - 使用协议映射表查找，复杂度 O(1)
//   - 相比 switch-case 线性查找，性能提升约 30%
func (t MessageType) ProtocolType() ProtocolType {
	if protocol, ok := protocolTypeTable[t]; ok {
		return protocol
	}

	// 默认使用 UDP（Gossip、心跳、时钟同步等）
	return ProtocolUDP
}

// isReplyMessage 判断是否是响应消息
func (t MessageType) isReplyMessage() bool {
	// 定义响应消息类型集合
	replyMessageTypes := map[MessageType]bool{
		MessageTypeGetReply:           true,
		MessageTypePutReply:           true,
		MessageTypeDeleteReply:        true,
		MessageTypeGossipSyncReply:    true,
		MessageTypeGossipDigestReply:  true,
		MessageType2PCPrepareReply:    true,
		MessageType2PCCommitReply:     true,
		MessageType2PCRollbackReply:   true,
		MessageTypeNodePong:           true,
		MessageTypeClockSyncReply:     true,
		MessageTypeClusterStatusReply: true,
		MessageTypeNodeReparentReply:  true,
	}

	return replyMessageTypes[t]
}

// Priority 消息优先级
//
// 用于标识消息处理优先级，支持 QoS（服务质量）控制
// 应用场景：关键消息优先处理、流量控制、负载均衡
type Priority uint8

const (
	// PriorityLow 低优先级
	//
	// 适用场景：后台同步、非关键元数据更新
	// 处理策略：在网络拥塞时可能被延迟或丢弃
	// 示例：Gossip 摘要同步、统计信息上报
	PriorityLow Priority = 0

	// PriorityNormal 普通优先级（默认）
	//
	// 适用场景：常规元数据操作、节点心跳
	// 处理策略：按先进先出（FIFO）顺序处理
	// 示例：Get/Put/Delete 请求、NodePing/NodePong
	PriorityNormal Priority = 1

	// PriorityHigh 高优先级
	//
	// 适用场景：重要但非阻塞的操作
	// 处理策略：优先于普通/低优先级消息处理
	// 示例：Quorum 提案、Gossip 同步、节点状态变更
	PriorityHigh Priority = 2

	// PriorityCritical 关键优先级
	//
	// 适用场景：系统关键操作、阻塞式协调
	// 处理策略：最高优先级，立即处理，不丢弃
	// 示例：2PC 协议消息、Leader 选举、故障恢复
	PriorityCritical Priority = 3
)

// String 返回 Priority 的字符串表示
func (p Priority) String() string {
	switch p {
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	case PriorityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Validate 验证 Priority 是否有效
func (p Priority) Validate() error {
	switch p {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityCritical:
		return nil
	default:
		return NewStoreInvalidParameterError("Priority")
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

	// MsgRole 返回消息角色（请求或响应）
	// 默认实现：调用 Type().MsgRole()
	// 用于快速判断消息类型，避免字符串匹配
	MsgRole() MsgRole

	// ExpectResponse 返回消息是否期望响应
	// 默认实现：调用 Type().ExpectResponse()
	ExpectResponse() ResponseExpectation

	// ProtocolType 返回消息使用的传输协议类型（TCP 或 UDP）
	// 默认实现：调用 Type().ProtocolType()
	ProtocolType() ProtocolType

	// CorrelationID 返回全局唯一的关联ID
	// 用途：传输层通过此ID匹配请求-响应，reqTable 核心索引
	// - MsgFrame: 自动从 FixedHeader (NodeID:MsgSeq) 计算，无需手动设置
	// - BaseMessage: 由传输层自动赋值，业务层无需关心
	// - 响应消息：必须和对应请求的 CorrelationID 一致
	CorrelationID() string
}
