// Package transport 流量控制模块
//
// 核心功能:
//   - 发送端反压（检测接收通道满载）
//   - 接收端主动丢弃（基于优先级）
//   - 流量统计和监控
package transport

import (
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// ShouldDrop 判断是否应该丢弃消息（基于优先级和当前状态）
//
// 参数:
//   - msg: 消息
//   - channelUsage: 当前接收通道使用率（0.0-1.0）
//
// 返回:
//   - bool: true 表示应该丢弃，false 表示应该接收
//
// 丢弃策略:
//   - channelUsage < 0.8: 不丢弃任何消息
//   - 0.8 <= channelUsage < 0.9: 丢弃最低优先级消息
//   - 0.9 <= channelUsage < 0.95: 丢弃低优先级和最低优先级消息
//   - channelUsage >= 0.95: 丢弃正常、低优先级和最低优先级消息（仅保留关键消息）
func ShouldDrop(msg Message, channelUsage float64) bool {
	// 通道未满载，不丢弃
	if channelUsage < 0.8 {
		return false
	}

	priority := msg.Priority()

	// 根据通道使用率决定丢弃策略
	switch {
	case channelUsage < 0.9:
		// 轻微满载：仅丢弃最低优先级
		return priority <= PriorityLowest

	case channelUsage < 0.95:
		// 中度满载：丢弃低优先级和最低优先级
		return priority <= PriorityLow

	default:
		// 严重满载：丢弃正常、低优先级和最低优先级（仅保留关键消息）
		return priority <= PriorityNormal
	}
}

// GetPriorityName 获取优先级名称（用于日志）
func GetPriorityName(priority int) string {
	switch priority {
	case PriorityLowest:
		return "LOWEST"
	case PriorityLow:
		return "LOW"
	case PriorityNormal:
		return "NORMAL"
	case PriorityHigh:
		return "HIGH"
	case PriorityCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// LogFlowControlEvent 记录流量控制事件
//
// 参数:
//   - event: 事件类型（accept、drop、channel_full）
//   - msg: 消息
//   - channelUsage: 当前通道使用率
func LogFlowControlEvent(event string, msg Message, channelUsage float64) {
	switch event {
	case "accept":
		logging.Debugf("流量控制: 接收消息 type=%d priority=%s usage=%.2f",
			msg.Type(), GetPriorityName(msg.Priority()), channelUsage)

	case "drop":
		priority := msg.Priority()
		logging.Warnf("流量控制: 丢弃消息 type=%d priority=%s usage=%.2f",
			msg.Type(), GetPriorityName(priority), channelUsage)

	case "channel_full":
		logging.Warnf("流量控制: 通道满载等待超时，消息可能丢失")
	}
}
