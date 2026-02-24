// Package clock 时钟基础设施实现
//
// 此包提供对 internal/clock 的 DDD 风格封装
// 包含：
// - HLC: 有状态混合逻辑时钟实现（来自 internal/clock）
// - HLCProvider: ClockProvider 接口实现
package clock

import (
	"github.com/jzhang405/NexKV/internal/clock"
	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// HLC 混合逻辑时钟（有状态实现）
// 这是 internal/clock.HLC 的别名，用于向后兼容
type HLC = clock.HLC

// NewHLC 创建新的 HLC 实例
// 这是 internal/clock.NewHLC 的包装
var NewHLC = clock.NewHLC

// HLCProvider HLC 时钟提供者基础设施实现
// 包装 internal/clock.HLC 实现 ClockProvider 接口
type HLCProvider struct {
	hlc *clock.HLC
}

// NewHLCProvider 创建新的 HLC 时钟提供者
func NewHLCProvider() service.ClockProvider {
	return &HLCProvider{
		hlc: clock.NewHLC(),
	}
}

// Now 返回当前 HLC 时间戳
// 生成一个新的时间戳，保证单调递增
func (h *HLCProvider) Now() *model.HLC {
	return h.hlc.Now().ToModelHLC()
}

// Update 更新 HLC 时间戳（核心算法）
//
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
func (h *HLCProvider) Update(eventTime int64, remoteHLC *model.HLC) *model.HLC {
	var remote *clock.HLC
	if remoteHLC != nil {
		remote = clock.FromModelHLC(remoteHLC)
	}
	return h.hlc.Update(eventTime, remote).ToModelHLC()
}

// Current 返回当前时钟状态（不推进）
func (h *HLCProvider) Current() *model.HLC {
	return h.hlc.Clone().ToModelHLC()
}
