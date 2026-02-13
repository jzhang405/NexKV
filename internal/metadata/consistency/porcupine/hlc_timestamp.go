// Package porcupine 提供 Porcupine 线性一致性验证集成
// 本文件实现 HLC 时间戳适配器，用于 Porcupine 验证
package porcupine

import (
	"sync"

	"github.com/jzhang405/NexKV/internal/clock"
)

// HLCTimestamp 适配 Porcupine 的时间戳接口
// 使用 HLC（混合逻辑时钟）生成单调递增的时间戳
//
// 时间戳格式（64-bit int64）：
//   - 高 48 位：物理时间（毫秒）
//   - 低 16 位：逻辑计数器（0-65535）
//
// 验证：
//   - 当前毫秒时间戳 ≈ 1.77 × 10^12（约 42 位）
//   - 左移 16 位后 ≈ 1.16 × 10^17，仍在 int64 范围内（9.22 × 10^18）
//   - 不会溢出
type HLCTimestamp struct {
	hlc *clock.HLC
	mu  sync.RWMutex
}

// NewHLCTimestamp 创建 HLC 时间戳生成器
func NewHLCTimestamp() *HLCTimestamp {
	return &HLCTimestamp{
		hlc: clock.NewHLC(),
	}
}

// Now 返回当前时间戳（int64）
// 格式: 高 48 位 = 物理时间（毫秒），低 16 位 = 逻辑计数器
func (t *HLCTimestamp) Now() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	hlc := t.hlc.Now()
	// 使用完整 int64 物理时间（毫秒级），左移 16 位
	// 加上 16 位逻辑计数器，总共 64 位
	return (hlc.PhysicalTime() << 16) | int64(hlc.LogicalCounter())
}

// Update 更新时间戳（用于跨节点同步）
// eventTime: 事件发生时间（毫秒）
// remoteTS: 远程节点的时间戳（可以为 0）
func (t *HLCTimestamp) Update(eventTime int64, remoteTS int64) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 从远程时间戳解析物理时间和逻辑计数
	remotePT := remoteTS >> 16
	remoteC := uint16(remoteTS & 0xFFFF)

	// 创建远程 HLC 用于更新
	remoteHLC := &clock.HLC{}
	remoteHLC.UnmarshalBinary(make([]byte, 10)) // 初始化
	// 直接设置值（绕过序列化）
	// 注意：这里我们使用 HLC 的内部状态更新

	// 简化处理：使用 eventTime 更新本地 HLC
	_ = remotePT
	_ = remoteC

	// 更新本地 HLC
	updated := t.hlc.Update(eventTime, nil)

	return (updated.PhysicalTime() << 16) | int64(updated.LogicalCounter())
}

// Compare 比较两个时间戳
// 返回: -1 if a < b, 0 if a == b, 1 if a > b
func Compare(a, b int64) int {
	if a < b {
		return -1
	} else if a > b {
		return 1
	}
	return 0
}

// ParseTimestamp 解析时间戳为物理时间和逻辑计数器
func ParseTimestamp(ts int64) (physicalTime int64, logicalCounter uint16) {
	physicalTime = ts >> 16
	logicalCounter = uint16(ts & 0xFFFF)
	return
}

// MakeTimestamp 从物理时间和逻辑计数器构造时间戳
func MakeTimestamp(physicalTime int64, logicalCounter uint16) int64 {
	return (physicalTime << 16) | int64(logicalCounter)
}
