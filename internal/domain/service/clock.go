// Package service 定义领域服务接口
package service

import (
	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ClockProvider 时钟提供者接口
// 定义了获取和管理逻辑时钟的核心能力
type ClockProvider interface {
	// Now 返回当前时间戳
	// 生成一个新的时间戳，保证单调递增
	Now() *model.HLC

	// Update 更新时钟（核心算法）
	// 这是 HLC 的核心算法，用于处理时钟同步和时间回拨：
	// 1. pt' = max(now, pt, eventTime, remoteHLC.pt)
	// 2. 如果 pt' == pt && pt' == remoteHLC.pt，则 c' = max(c, remoteHLC.c) + 1
	// 3. 否则 c' = 0
	//
	// 参数:
	//   - eventTime: 事件发生时间（毫秒）
	//   - remoteHLC: 远程节点的 HLC 时间戳（可以为 nil）
	//
	// 返回更新后的 HLC 时间戳
	Update(eventTime int64, remoteHLC *model.HLC) *model.HLC

	// Current 返回当前时钟状态（不推进）
	Current() *model.HLC
}

// ClockService 时钟领域服务
// 提供与时钟相关的高级领域操作
type ClockService interface {
	// GetProvider 获取时钟提供者
	GetProvider() ClockProvider

	// CompareTimestamps 比较两个时间戳
	// 返回: -1 if t1 < t2, 0 if t1 == t2, 1 if t1 > t2
	CompareTimestamps(t1, t2 *model.HLC) int

	// IsConcurrent 判断两个时间戳是否并发（不可比较）
	// 在分布式系统中，当两个事件没有因果关系时，它们是并发的
	IsConcurrent(t1, t2 *model.HLC) bool

	// MaxTimestamp 返回两个时间戳中的较大者
	MaxTimestamp(t1, t2 *model.HLC) *model.HLC
}
