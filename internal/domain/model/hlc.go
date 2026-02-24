// Package model 定义领域模型
package model

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// HLC 混合逻辑时钟（Hybrid Logical Clock）值对象
//
// 结构: 48-bit 物理时间 + 16-bit 逻辑计数
//   - pt (physical time): 毫秒级物理时间戳
//     c (logical counter): 逻辑计数器，用于处理时间回拨和同一毫秒内的事件
//
// HLC 是不可变的值对象，任何修改操作都返回新的 HLC 实例
// 这保证了线程安全和可预测的行为
//
// 算法: pt' = max(now, pt, eventTime, remoteHLC.pt)
//
//	c' = (pt' == pt && pt' == remoteHLC.pt) ? max(c, remoteHLC.c) + 1 : 0
type HLC struct {
	pt int64  // 物理时间（毫秒）
	c  uint16 // 逻辑计数（0-65535）
}

// NewHLC 创建新的 HLC 实例，使用当前物理时间
func NewHLC() *HLC {
	now := time.Now()
	return &HLC{
		pt: now.UnixMilli(),
		c:  0,
	}
}

// NewHLCWithTime 使用指定物理时间创建 HLC
func NewHLCWithTime(pt int64, c uint16) *HLC {
	return &HLC{
		pt: pt,
		c:  c,
	}
}

// PhysicalTime 返回物理时间（毫秒）
func (h *HLC) PhysicalTime() int64 {
	if h == nil {
		return 0
	}
	return h.pt
}

// LogicalCounter 返回逻辑计数
func (h *HLC) LogicalCounter() uint16 {
	if h == nil {
		return 0
	}
	return h.c
}

// LessThan 判断当前 HLC 是否小于另一个 HLC
func (h *HLC) LessThan(other *HLC) bool {
	if h == nil || other == nil {
		return false
	}

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

	return fmt.Sprintf("HLC(pt=%d, c=%d)", h.pt, h.c)
}

// ToTime 将 HLC 转换为 time.Time
func (h *HLC) ToTime() time.Time {
	if h == nil {
		return time.Time{}
	}

	return time.Unix(h.pt/1000, (h.pt%1000)*1e6)
}

// MarshalBinary 序列化 HLC 为二进制
// 格式: 8 bytes pt (big-endian) + 2 bytes c (big-endian)
func (h *HLC) MarshalBinary() ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("cannot marshal nil HLC")
	}

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
		return fmt.Errorf("invalid HLC data size: expected 10 bytes, got %d", len(data))
	}

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

	return &HLC{
		pt: h.pt,
		c:  h.c,
	}
}

// IsAtMaxValue 判断 HLC 是否已达到最大值
func (h *HLC) IsAtMaxValue() bool {
	if h == nil {
		return false
	}

	// HLC 的物理时间是 48-bit，最大值是 (1 << 48) - 1
	maxPT := int64((1 << 48) - 1)
	return h.pt == maxPT && h.c == math.MaxUint16
}
