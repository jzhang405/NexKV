// Package clock 时钟应用服务
package clock

import (
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ClockServiceImpl 时钟应用服务实现
type ClockServiceImpl struct {
	provider service.ClockProvider
}

// NewClockService 创建时钟应用服务
func NewClockService(provider service.ClockProvider) service.ClockService {
	return &ClockServiceImpl{
		provider: provider,
	}
}

// GetProvider 获取时钟提供者
func (s *ClockServiceImpl) GetProvider() service.ClockProvider {
	return s.provider
}

// CompareTimestamps 比较两个时间戳
func (s *ClockServiceImpl) CompareTimestamps(t1, t2 *model.HLC) int {
	return t1.Compare(t2)
}

// IsConcurrent 判断两个时间戳是否并发
// 在 HLC 中，两个时间戳要么有因果关系（可比较），要么是并发的
// 实际上 HLC 总是可比较的，所以并发意味着它们在同一物理时间发生
func (s *ClockServiceImpl) IsConcurrent(t1, t2 *model.HLC) bool {
	if t1 == nil || t2 == nil {
		return false
	}
	// 在 HLC 中，如果物理时间相同且逻辑计数也相同，则它们是"并发"的
	// 这表示它们可能是独立发生的事件
	return t1.PhysicalTime() == t2.PhysicalTime() && t1.LogicalCounter() == t2.LogicalCounter()
}

// MaxTimestamp 返回两个时间戳中的较大者
func (s *ClockServiceImpl) MaxTimestamp(t1, t2 *model.HLC) *model.HLC {
	if t1 == nil {
		return t2
	}
	if t2 == nil {
		return t1
	}
	if t1.GreaterThan(t2) {
		return t1
	}
	return t2
}

// ==========================================
// HLCProvider 实现
// ==========================================

// HLCProvider HLC 时钟提供者实现
type HLCProvider struct {
	pt int64  // 物理时间（毫秒）
	c  uint16 // 逻辑计数（0-65535）
	mu sync.RWMutex
}

// NewHLCProvider 创建新的 HLC 时钟提供者
func NewHLCProvider() service.ClockProvider {
	now := time.Now()
	return &HLCProvider{
		pt: now.UnixMilli(),
		c:  0,
	}
}

// Now 返回当前 HLC 时间戳
func (h *HLCProvider) Now() *model.HLC {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().UnixMilli()

	// 物理时间前进，重置逻辑计数
	if now > h.pt {
		h.pt = now
		h.c = 0
		return model.NewHLCWithTime(h.pt, h.c)
	}

	// 物理时间相同或回拨，增加逻辑计数
	h.c++
	if h.c == 0 { // 溢出检测：65535 + 1 = 0
		// 溢出时推进物理时间，重置逻辑计数
		h.pt++
		h.c = 0
	}
	return model.NewHLCWithTime(h.pt, h.c)
}

// Update 更新 HLC 时间戳（核心算法）
func (h *HLCProvider) Update(eventTime int64, remoteHLC *model.HLC) *model.HLC {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().UnixMilli()

	// 处理 remoteHLC 为 nil 的情况
	remotePT := int64(0)
	remoteC := uint16(0)
	if remoteHLC != nil {
		remotePT = remoteHLC.PhysicalTime()
		remoteC = remoteHLC.LogicalCounter()
	}

	// 核心算法: pt' = max(now, pt, eventTime, remotePT)
	newPT := maxInt64(now, h.pt, eventTime, remotePT)

	// 如果物理时间没有前进，需要增加逻辑计数
	if newPT == h.pt && newPT == remotePT {
		// 相同物理时间，逻辑计数取最大值 + 1
		newC := maxUint16(h.c, remoteC) + 1
		if newC == 0 { // 溢出检测
			// 溢出时推进物理时间，重置逻辑计数
			newPT++
			newC = 0
		}
		h.c = newC
	} else {
		// 物理时间前进，重置逻辑计数
		h.c = 0
	}

	h.pt = newPT

	// 返回新的 HLC 时间戳
	return model.NewHLCWithTime(h.pt, h.c)
}

// Current 返回当前时钟状态（不推进）
func (h *HLCProvider) Current() *model.HLC {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return model.NewHLCWithTime(h.pt, h.c)
}

// maxInt64 返回多个 int64 中的最大值
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

// maxUint16 返回两个 uint16 中的最大值
func maxUint16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}
