// Package clock 时钟基础设施实现
//
// 此包提供 HLC 混合逻辑时钟的完整实现
package clock

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
	"github.com/jzhang405/NexKV/pkg/errors"
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
	pt int64
	c  uint16
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

	if now > h.pt {
		h.pt = now
		h.c = 0
		return &HLC{pt: h.pt, c: h.c}
	}

	h.c++
	if h.c == 0 {
		h.pt++
		h.c = 0
	}
	return &HLC{pt: h.pt, c: h.c}
}

// Update 更新 HLC 时间戳（核心算法）
func (h *HLC) Update(eventTime int64, remoteHLC *HLC) *HLC {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().UnixMilli()

	remotePT := int64(0)
	remoteC := uint16(0)
	if remoteHLC != nil {
		remotePT = remoteHLC.pt
		remoteC = remoteHLC.c
	}

	newPT := maxInt64(now, h.pt, eventTime, remotePT)

	if newPT == h.pt && newPT == remotePT {
		newC := maxUint16(h.c, remoteC) + 1
		if newC == 0 {
			newPT++
			newC = 0
		}
		h.c = newC
	} else {
		h.c = 0
	}

	h.pt = newPT

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

	if h.pt != other.pt {
		return h.pt < other.pt
	}

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
		return nil, errors.Wrap(errors.ErrClockMarshalNil, "HLC is nil")
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	buf := new(bytes.Buffer)

	if err := binary.Write(buf, binary.BigEndian, h.pt); err != nil {
		return nil, err
	}

	if err := binary.Write(buf, binary.BigEndian, h.c); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// UnmarshalBinary 从二进制反序列化 HLC
func (h *HLC) UnmarshalBinary(data []byte) error {
	if len(data) != 10 {
		return errors.Wrapf(errors.ErrClockInvalidSize, "expected 10 bytes, got %d", len(data))
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	buf := bytes.NewReader(data)

	if err := binary.Read(buf, binary.BigEndian, &h.pt); err != nil {
		return err
	}

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

// IsAtMaxValue 判断 HLC 是否已达到最大值
func (h *HLC) IsAtMaxValue() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	maxPT := int64((1 << 48) - 1)
	return h.pt == maxPT && h.c == math.MaxUint16
}

// ToModelHLC 转换为 domain/model.HLC
func (h *HLC) ToModelHLC() *model.HLC {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return model.NewHLCWithTime(h.pt, h.c)
}

// FromModelHLC 从 domain/model.HLC 创建
func FromModelHLC(m *model.HLC) *HLC {
	if m == nil {
		return nil
	}
	return &HLC{
		pt: m.PhysicalTime(),
		c:  m.LogicalCounter(),
	}
}

func maxInt64(values ...int64) int64 {
	if len(values) == 0 {
		return 0
	}

	maxVal := values[0]
	for _, v := range values[1:] {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}

func maxUint16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

// HLCProvider HLC 时钟提供者基础设施实现
type HLCProvider struct {
	hlc *HLC
}

// NewHLCProvider 创建新的 HLC 时钟提供者
func NewHLCProvider() service.ClockProvider {
	return &HLCProvider{
		hlc: NewHLC(),
	}
}

// Now 返回当前 HLC 时间戳
func (h *HLCProvider) Now() *model.HLC {
	return h.hlc.Now().ToModelHLC()
}

// Update 更新 HLC 时间戳
func (h *HLCProvider) Update(eventTime int64, remoteHLC *model.HLC) *model.HLC {
	var remote *HLC
	if remoteHLC != nil {
		remote = FromModelHLC(remoteHLC)
	}
	return h.hlc.Update(eventTime, remote).ToModelHLC()
}

// Current 返回当前时钟状态（不推进）
func (h *HLCProvider) Current() *model.HLC {
	return h.hlc.Clone().ToModelHLC()
}
