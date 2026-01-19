// Package clock 提供 HLC 混合逻辑时钟实现
package clock

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"math"
	"sync"
	"time"
)

// HLC 混合逻辑时钟
//
// 结构: 48-bit 物理时间 + 16-bit 逻辑计数
//   - pt (physical time): 毫秒级物理时间戳
//   - c (logical counter): 逻辑计数器，用于处理时间回拨和同一毫秒内的事件
//
// 算法: pt' = max(now, pt, eventTime, remoteHLC.pt)
//
//	c' = (pt' == pt && pt' == remoteHLC.pt) ? max(c, remoteHLC.c) + 1 : 0
type HLC struct {
	pt int64  // 物理时间（毫秒）
	c  uint16 // 逻辑计数（0-65535）
	mu sync.RWMutex
}

// NewHLC 创建新的 HLC 实例
func NewHLC() *HLC {
	now := time.Now()
	return &HLC{
		pt: now.UnixMilli(),
		c:  0,
	}
}

// Now 返回当前 HLC 时间戳
func (h *HLC) Now() *HLC {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().UnixMilli()

	// 物理时间前进，重置逻辑计数
	if now > h.pt {
		h.pt = now
		h.c = 0
		return &HLC{pt: h.pt, c: h.c}
	}

	// 物理时间相同或回拨，增加逻辑计数
	h.c++
	return &HLC{pt: h.pt, c: h.c}
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
//   - remoteHLC: 远程节点的 HLC 时间戳
//
// 返回更新后的 HLC 时间戳
func (h *HLC) Update(eventTime int64, remoteHLC *HLC) *HLC {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().UnixMilli()

	// 核心算法: pt' = max(now, pt, eventTime, remoteHLC.pt)
	newPT := maxInt64(now, h.pt, eventTime, remoteHLC.pt)

	// 如果物理时间没有前进，需要增加逻辑计数
	if newPT == h.pt && newPT == remoteHLC.pt {
		// 相同物理时间，逻辑计数取最大值 + 1
		h.c = maxUint16(h.c, remoteHLC.c) + 1
	} else {
		// 物理时间前进，重置逻辑计数
		h.c = 0
	}

	h.pt = newPT

	// 返回新的 HLC 时间戳（副本，避免并发问题）
	return &HLC{pt: h.pt, c: h.c}
}

// PhysicalTime 返回物理时间（毫秒）
func (h *HLC) PhysicalTime() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pt
}

// LogicalCounter 返回逻辑计数
func (h *HLC) LogicalCounter() uint16 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.c
}

// LessThan 判断当前 HLC 是否小于另一个 HLC
func (h *HLC) LessThan(other *HLC) bool {
	if h == nil || other == nil {
		return false
	}

	h.mu.RLock()
	other.mu.RLock()
	defer h.mu.RUnlock()
	defer other.mu.RUnlock()

	// 先比较物理时间
	if h.pt != other.pt {
		return h.pt < other.pt
	}

	// 物理时间相同，比较逻辑计数
	return h.c < other.c
}

// Equal 判断当前 HLC 是否等于另一个 HLC
func (h *HLC) Equal(other *HLC) bool {
	if h == nil || other == nil {
		return h == other
	}

	h.mu.RLock()
	other.mu.RLock()
	defer h.mu.RUnlock()
	defer other.mu.RUnlock()

	return h.pt == other.pt && h.c == other.c
}

// GreaterThan 判断当前 HLC 是否大于另一个 HLC
func (h *HLC) GreaterThan(other *HLC) bool {
	return !h.LessThan(other) && !h.Equal(other)
}

// Compare 比较两个 HLC 时间戳
// 返回: -1 if h < other, 0 if h == other, 1 if h > other
func (h *HLC) Compare(other *HLC) int {
	if h.LessThan(other) {
		return -1
	}
	if h.Equal(other) {
		return 0
	}
	return 1
}

// String 返回 HLC 的字符串表示
func (h *HLC) String() string {
	if h == nil {
		return "HLC(nil)"
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	return fmt.Sprintf("HLC(pt=%d, c=%d)", h.pt, h.c)
}

// ToTime 将 HLC 转换为 time.Time
func (h *HLC) ToTime() time.Time {
	if h == nil {
		return time.Time{}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	return time.Unix(h.pt/1000, (h.pt%1000)*1e6)
}

// MarshalBinary 序列化 HLC 为二进制
// 格式: 8 bytes pt (big-endian) + 2 bytes c (big-endian)
func (h *HLC) MarshalBinary() ([]byte, error) {
	if h == nil {
		return nil, types.NewClockOperationError("cannot marshal nil HLC")
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	buf := new(bytes.Buffer)

	// 写入 pt (8 bytes, big-endian)
	if err := binary.Write(buf, binary.BigEndian, h.pt); err != nil {
		return nil, err
	}

	// 写入 c (2 bytes, big-endian)
	if err := binary.Write(buf, binary.BigEndian, h.c); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// UnmarshalBinary 从二进制反序列化 HLC
func (h *HLC) UnmarshalBinary(data []byte) error {
	if len(data) != 10 {
		return types.NewClockOperationError(fmt.Sprintf("invalid HLC data size: expected 10 bytes, got %d", len(data)))
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	buf := bytes.NewReader(data)

	// 读取 pt (8 bytes, big-endian)
	if err := binary.Read(buf, binary.BigEndian, &h.pt); err != nil {
		return err
	}

	// 读取 c (2 bytes, big-endian)
	if err := binary.Read(buf, binary.BigEndian, &h.c); err != nil {
		return err
	}

	return nil
}

// Clone 克隆 HLC 时间戳
func (h *HLC) Clone() *HLC {
	if h == nil {
		return nil
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	return &HLC{
		pt: h.pt,
		c:  h.c,
	}
}

// maxInt64 返回多个 int64 中的最大值
func maxInt64(values ...int64) int64 {
	if len(values) == 0 {
		return 0
	}

	max := values[0]
	for _, v := range values[1:] {
		if v > max {
			max = v
		}
	}
	return max
}

// maxUint16 返回两个 uint16 中的最大值
func maxUint16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

// MaxLogicalCounter 逻辑计数器的最大值（用于检测溢出）
const MaxLogicalCounter = math.MaxUint16

// IsAtMaxValue 判断 HLC 是否已达到最大值
func (h *HLC) IsAtMaxValue() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// HLC 的物理时间是 48-bit，最大值是 (1 << 48) - 1
	maxPT := int64((1 << 48) - 1)
	return h.pt == maxPT && h.c == MaxLogicalCounter
}
