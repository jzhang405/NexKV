// Package transport 消息路由器
//
// 实现三维路由决策矩阵：
//   - 维度 1：有回应 vs 无回应
//   - 维度 2：消息大小（<1KB / 1-50KB / >50KB）
//   - 维度 3：可靠性要求（容忍丢失 / 强依赖）
package transport

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/metadata/types"
)

// MessageSizeRange 消息大小范围
type MessageSizeRange int

const (
	SmallMessage  MessageSizeRange = iota // 小消息（< 1KB）
	MediumMessage                         // 中等消息（1KB - 50KB）
	LargeMessage                          // 大消息（> 50KB）
)

// RouteDecision 路由决策
type RouteDecision struct {
	ProtocolType  ProtocolType // 选择的协议类型
	Reason        string       // 选择原因
	ShouldDegrade bool         // 是否应该降级（false 表示使用默认协议）
}

// RouterConfig 路由器配置
type RouterConfig struct {
	SmallMessageThreshold   int                 // 小消息阈值（字节）
	MediumMessageThreshold  int                 // 中等消息阈值（字节）
	LargeMessageThreshold   int                 // 大消息阈值（字节）
	BroadcastAddresses      []string            // 广播地址列表（强制使用 UDP）
	NonFallbackMessageTypes []types.MessageType // 不可降级消息类型（强制使用 TCP）
	EnableAutoRouting       bool                // 启用自动路由决策
}

// DefaultRouterConfig 返回默认路由配置
func DefaultRouterConfig() *RouterConfig {
	return &RouterConfig{
		SmallMessageThreshold:  1 * 1024,  // 1KB
		MediumMessageThreshold: 50 * 1024, // 50KB
		LargeMessageThreshold:  50 * 1024, // 50KB
		// 广播地址配置说明：
		// - 255.255.255.255: 有限广播地址，适用于小局域网节点发现
		// - 239.x.x.x: 管理型组播地址范围，适用于中大型分布式集群
		//   注：实际使用时需绑定端口（如 239.1.1.1:8080）
		BroadcastAddresses: []string{
			"255.255.255.255", // 有限广播地址（小局域网场景）
			"239.1.1.1",       // 组播地址（分布式集群场景）
		},
		NonFallbackMessageTypes: []types.MessageType{
			types.MessageType2PCCommit,
			types.MessageType2PCRollback,
			types.MessageTypeQuorumDecide,
			types.MessageTypeLeaderElection,
			types.MessageTypeNodeJoin,
			types.MessageTypeNodeLeave,
		},
		EnableAutoRouting: true,
	}
}

// MessageRouter 消息路由器
//
// 根据三维决策矩阵选择最佳传输协议
type MessageRouter struct {
	config   *RouterConfig // 配置
	configMu sync.RWMutex  // 配置锁
	stats    *RouterStats  // 统计信息
}

// RouterStats 路由统计
//
// 使用 atomic 操作优化并发性能
type RouterStats struct {
	TotalDecisions         atomic.Uint64 // 总路由决策次数
	DegradationTriggers    atomic.Uint64 // 降级触发次数
	DecisionsByProtocol    sync.Map      // 按协议类型统计 (ProtocolType -> atomic.Uint64)
	DecisionsByMessageType sync.Map      // 按消息类型统计 (MessageType -> atomic.Uint64)
}

// NewMessageRouter 创建消息路由器
func NewMessageRouter(config *RouterConfig) *MessageRouter {
	if config == nil {
		config = DefaultRouterConfig()
	}

	return &MessageRouter{
		config: config,
		stats:  &RouterStats{},
	}
}

// DecideProtocol 决策协议类型
//
// 根据三维决策矩阵（回应期望、消息大小、可靠性要求）选择最佳传输协议。
// 回应期望和可靠性要求从消息类型自动推断。
//
// 决策流程：
//  1. 强制路由规则检查（广播地址 → UDP，不可降级消息 → TCP）
//  2. 自动路由决策（如果启用）：
//     - 需要回应 → TCP
//     - 大消息（>50KB）→ TCP
//     - 高可靠性要求 → TCP
//     - 默认 → UDP（可降级）
//
// 参数:
//   - ctx: 上下文（预留，当前未使用）
//   - addr: 目标地址
//   - msgType: 消息类型
//   - msgSize: 消息大小（字节）
//
// 返回:
//   - RouteDecision: 路由决策结果
func (r *MessageRouter) DecideProtocol(
	ctx context.Context,
	addr string,
	msgType types.MessageType,
	msgSize int,
) RouteDecision {
	r.configMu.RLock()
	config := r.config
	r.configMu.RUnlock()

	// 强制路由规则检查
	if decision := r.checkForcedRouting(addr, msgType, config); decision != nil {
		return r.recordDecision(*decision, msgType)
	}

	// 自动路由决策
	if !config.EnableAutoRouting {
		return r.recordDecision(RouteDecision{
			ProtocolType:  "",
			Reason:        "自动路由未启用，使用默认协议",
			ShouldDegrade: false,
		}, msgType)
	}

	return r.recordDecision(r.threeDimensionalDecision(msgSize, msgType, config), msgType)
}

// threeDimensionalDecision 三维路由决策
//
// 根据三个维度的优先级进行决策：
//  1. 回应期望（最高优先级）- 需要回应的消息必须使用 TCP
//  2. 消息大小（次优先级）- 大消息（>50KB）使用 TCP
//  3. 可靠性要求（最低优先级）- 关键消息使用 TCP
//
// 决策优先级：回应期望 > 消息大小 > 可靠性要求
//
// 参数:
//   - msgSize: 消息大小（字节）
//   - msgType: 消息类型
//   - config: 路由器配置
//
// 返回:
//   - RouteDecision: 路由决策结果
func (r *MessageRouter) threeDimensionalDecision(
	msgSize int,
	msgType types.MessageType,
	config *RouterConfig,
) RouteDecision {
	// 决策优先级：回应期望 > 消息大小 > 可靠性要求
	if msgType.ExpectResponse() == types.ExpectResponse {
		return r.buildDecision(ProtocolTCP, "需要回应，使用 TCP", false)
	}

	if r.classifyMessageSize(msgSize, config) == LargeMessage {
		return r.buildDecision(ProtocolTCP, "大消息，使用 TCP", false)
	}

	if msgType.Reliability() == types.Reliable {
		return r.buildDecision(ProtocolTCP, "高可靠性要求，使用 TCP", false)
	}

	return r.buildDecision(ProtocolUDP, "默认使用 UDP（低延迟）", true)
}

// checkForcedRouting 检查强制路由规则
func (r *MessageRouter) checkForcedRouting(
	addr string,
	msgType types.MessageType,
	config *RouterConfig,
) *RouteDecision {
	// 广播地址强制 UDP
	if r.isBroadcastAddress(addr, config) {
		return &RouteDecision{
			ProtocolType:  ProtocolUDP,
			Reason:        "广播地址，使用 UDP",
			ShouldDegrade: false,
		}
	}

	// 不可降级消息类型强制 TCP
	if r.isNonFallbackMessageType(msgType, config) {
		return &RouteDecision{
			ProtocolType:  ProtocolTCP,
			Reason:        fmt.Sprintf("不可降级消息类型 %v，使用 TCP", msgType),
			ShouldDegrade: false,
		}
	}

	return nil
}

// buildDecision 构建路由决策（辅助函数）
func (r *MessageRouter) buildDecision(
	protocolType ProtocolType,
	reason string,
	shouldDegrade bool,
) RouteDecision {
	return RouteDecision{
		ProtocolType:  protocolType,
		Reason:        reason,
		ShouldDegrade: shouldDegrade,
	}
}

// isBroadcastAddress 检查是否为广播地址
func (r *MessageRouter) isBroadcastAddress(addr string, config *RouterConfig) bool {
	for _, broadcastAddr := range config.BroadcastAddresses {
		if strings.HasPrefix(addr, broadcastAddr) {
			return true
		}
	}
	return false
}

// isNonFallbackMessageType 检查是否为不可降级消息类型
func (r *MessageRouter) isNonFallbackMessageType(msgType types.MessageType, config *RouterConfig) bool {
	return slices.Contains(config.NonFallbackMessageTypes, msgType)
}

// classifyMessageSize 分类消息大小
func (r *MessageRouter) classifyMessageSize(msgSize int, config *RouterConfig) MessageSizeRange {
	if msgSize < config.SmallMessageThreshold {
		return SmallMessage
	} else if msgSize < config.MediumMessageThreshold {
		return MediumMessage
	}
	return LargeMessage
}

// recordDecision 记录路由决策
//
// 记录路由决策到统计信息，包括：
//   - 总决策次数
//   - 按协议类型统计
//   - 按消息类型统计
//   - 降级触发次数
//
// 使用 atomic 操作和 sync.Map 优化并发性能
//
// 参数:
//   - decision: 路由决策
//   - msgType: 消息类型
//
// 返回:
//   - RouteDecision: 原样返回的路由决策
func (r *MessageRouter) recordDecision(decision RouteDecision, msgType types.MessageType) RouteDecision {
	// 原子操作更新计数
	r.stats.TotalDecisions.Add(1)

	if decision.ShouldDegrade {
		r.stats.DegradationTriggers.Add(1)
	}

	// 使用 sync.Map 更新协议统计
	if val, ok := r.stats.DecisionsByProtocol.Load(decision.ProtocolType); ok {
		if counter, ok := val.(*atomic.Uint64); ok {
			counter.Add(1)
		}
	} else {
		counter := &atomic.Uint64{}
		counter.Add(1)
		r.stats.DecisionsByProtocol.Store(decision.ProtocolType, counter)
	}

	// 使用 sync.Map 更新消息类型统计
	if val, ok := r.stats.DecisionsByMessageType.Load(msgType); ok {
		if counter, ok := val.(*atomic.Uint64); ok {
			counter.Add(1)
		}
	} else {
		counter := &atomic.Uint64{}
		counter.Add(1)
		r.stats.DecisionsByMessageType.Store(msgType, counter)
	}

	return decision
}

// UpdateConfig 更新路由配置
func (r *MessageRouter) UpdateConfig(config *RouterConfig) {
	r.configMu.Lock()
	defer r.configMu.Unlock()

	r.config = config
}

// GetConfig 获取当前配置
func (r *MessageRouter) GetConfig() *RouterConfig {
	r.configMu.RLock()
	defer r.configMu.RUnlock()

	return r.config
}

// GetStats 获取路由统计
//
// 返回统计信息的深拷贝，避免外部修改内部状态
func (r *MessageRouter) GetStats() *RouterStats {
	stats := &RouterStats{}
	stats.TotalDecisions.Store(r.stats.TotalDecisions.Load())
	stats.DegradationTriggers.Store(r.stats.DegradationTriggers.Load())

	// 拷贝 sync.Map 中的数据
	r.stats.DecisionsByProtocol.Range(func(key, value any) bool {
		if counter, ok := value.(*atomic.Uint64); ok {
			newCounter := &atomic.Uint64{}
			newCounter.Store(counter.Load())
			stats.DecisionsByProtocol.Store(key, newCounter)
		}
		return true
	})

	r.stats.DecisionsByMessageType.Range(func(key, value any) bool {
		if counter, ok := value.(*atomic.Uint64); ok {
			newCounter := &atomic.Uint64{}
			newCounter.Store(counter.Load())
			stats.DecisionsByMessageType.Store(key, newCounter)
		}
		return true
	})

	return stats
}

// ResetStats 重置统计
func (r *MessageRouter) ResetStats() {
	r.stats.TotalDecisions.Store(0)
	r.stats.DegradationTriggers.Store(0)

	// 清空 sync.Map
	r.stats.DecisionsByProtocol.Range(func(key, value any) bool {
		r.stats.DecisionsByProtocol.Delete(key)
		return true
	})

	r.stats.DecisionsByMessageType.Range(func(key, value any) bool {
		r.stats.DecisionsByMessageType.Delete(key)
		return true
	})
}

var (
	// defaultRouterConfig 包级别默认路由配置（使用 sync.Once 惰性初始化）
	defaultRouterConfig     *RouterConfig
	defaultRouterConfigOnce sync.Once
)

// getDefaultRouterConfig 获取默认路由配置（包级别单例）
func getDefaultRouterConfig() *RouterConfig {
	defaultRouterConfigOnce.Do(func() {
		defaultRouterConfig = DefaultRouterConfig()
	})
	return defaultRouterConfig
}

// ParseAddress 解析并验证地址格式
//
// 验证地址格式是否正确，并返回解析后的地址。
// 支持的格式：host:port（如 "127.0.0.1:8080"）
//
// 验证项：
//   - 地址格式是否符合 host:port 结构
//   - host 部分是否为有效的 IP 地址
//   - port 部分是否存在
//
// 参数:
//   - addr: 待解析的地址字符串
//
// 返回:
//   - string: 解析后的地址（验证通过时）
//   - error: 验证失败时的错误信息
func ParseAddress(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid address format: %w", err)
	}

	if net.ParseIP(host) == nil {
		return "", fmt.Errorf("invalid IP address: %s", host)
	}

	if port == "" {
		return "", fmt.Errorf("missing port number")
	}

	return addr, nil
}

// IsBroadcastAddress 判断是否为广播地址（工具函数）
// 使用包级别默认实例，避免重复创建
func IsBroadcastAddress(addr string) bool {
	config := getDefaultRouterConfig()
	for _, broadcastAddr := range config.BroadcastAddresses {
		if strings.HasPrefix(addr, broadcastAddr) {
			return true
		}
	}
	return false
}

// IsNonFallbackMessageType 判断是否为不可降级消息类型（工具函数）
// 使用包级别默认实例，避免重复创建
func IsNonFallbackMessageType(msgType types.MessageType) bool {
	config := getDefaultRouterConfig()
	return slices.Contains(config.NonFallbackMessageTypes, msgType)
}
