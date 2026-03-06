// Package bftree 提供 Mini-Page 提升机制
package bftree

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// readCountMap 存储每个 MiniPage 的读取计数
// 使用指针作为 key 来识别不同的 MiniPage 实例
var (
	readCountMap = make(map[uintptr]*uint32)
	readCountMu  sync.RWMutex
)

// incrementReadCount 增加读取计数（用于 Mini-Page 提升判断）
func (mp *MiniPage) incrementReadCount() {
	key := getMiniPageKey(mp)
	
	readCountMu.RLock()
	counter, exists := readCountMap[key]
	readCountMu.RUnlock()
	
	if !exists {
		readCountMu.Lock()
		// 双重检查
		if counter, exists = readCountMap[key]; !exists {
			counter = new(uint32)
			readCountMap[key] = counter
		}
		readCountMu.Unlock()
	}
	
	atomic.AddUint32(counter, 1)
}

// getMiniPageKey 获取 MiniPage 的唯一键（使用指针地址）
func getMiniPageKey(mp *MiniPage) uintptr {
	return uintptr(unsafe.Pointer(mp))
}

// GetReadCount 获取 MiniPage 的读取计数
func (mp *MiniPage) GetReadCount() uint32 {
	key := getMiniPageKey(mp)
	
	readCountMu.RLock()
	defer readCountMu.RUnlock()
	
	if counter, exists := readCountMap[key]; exists {
		return atomic.LoadUint32(counter)
	}
	return 0
}

// shouldPromote 判断是否需要提升 Mini-Page
//
// 提升条件：
// 1. Read Promotion: 读取次数 >= 配置的阈值
// 2. Size Promotion: 数据大小 >= 容量的阈值百分比
func (mp *MiniPage) shouldPromote() bool {
	// 使用默认 PromotionConfig
	config := DefaultPromotionConfig()

	// Read Promotion（读取提升）
	if threshold, ok := config.ReadThresholds[mp.level]; ok {
		readCount := mp.GetReadCount()
		if readCount >= threshold {
			return true
		}
	}

	// Size Promotion（大小提升）
	if mp.dataSize >= uint16(float64(mp.capacity)*float64(config.SizeThresholdPct)/100) {
		return true
	}

	return false
}

// Promote 提升 LeafNode 的 Mini-Page 到下一级别
//
// 返回：
//   - error: 错误（nil 表示成功）
func (n *LeafNode) Promote() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 检查是否已经是最高级别
	if n.miniPage.level >= Full {
		return nil // 无需提升
	}

	// 检查是否需要提升
	if !n.miniPage.shouldPromote() {
		return nil // 不满足提升条件
	}

	return n.promoteLocked()
}

// promoteLocked 提升 Mini-Page（已持有锁）
func (n *LeafNode) promoteLocked() error {
	// 检查是否已经是最高级别
	if n.miniPage.level >= Full {
		return nil
	}

	// 提升到下一级别
	nextLevel := n.miniPage.level.NextLevel()

	// 创建新的 Mini-Page
	newMiniPage := NewMiniPage(nextLevel)

	// 复制所有槽位
	for _, slot := range n.miniPage.slots {
		newMiniPage.slots = append(newMiniPage.slots, slot)
		newMiniPage.slotMap[string(slot.key)] = len(newMiniPage.slots) - 1
		newMiniPage.dataSize += uint16(len(slot.key) + len(slot.value))
	}

	// 替换 Mini-Page
	n.miniPage = newMiniPage
	n.level = nextLevel

	return nil
}

// GetLevel 获取当前 Mini-Page 级别
func (n *LeafNode) GetLevel() PageLevel {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.level
}

// PromoteIfNeeded 检查并自动提升 Mini-Page
//
// 返回：
//   - promoted: 是否发生了提升
//   - error: 错误
func (n *LeafNode) PromoteIfNeeded() (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.miniPage.level >= Full {
		return false, nil
	}

	if !n.miniPage.shouldPromote() {
		return false, nil
	}

	if err := n.promoteLocked(); err != nil {
		return false, err
	}

	return true, nil
}
