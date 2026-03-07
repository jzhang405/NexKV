// Package bftree 提供 Bf-Tree 的 Delta Chain 优化实现
package bftree

import (
	"bytes"
	"sync"
)

// DeltaChain Delta 链（独立结构）
//
// 设计目的：
// - 写入操作先记录到 Delta Chain
// - 定期合并到主 Mini-Page（Compact）
// - 减少写入放大，提升性能
// - 细粒度锁，减少并发竞争
type DeltaChain struct {
	entries   []*DeltaEntry // Delta 条目列表
	size      uint16        // Delta 大小（字节）
	maxLength int           // 最大 Delta Chain 长度
	maxSize   uint16        // 最大 Delta Chain 大小
	mu        sync.RWMutex  // 读写锁（细粒度锁）
}

// NewDeltaChain 创建新的 Delta Chain
func NewDeltaChain(maxLength int, maxSize uint16) *DeltaChain {
	return &DeltaChain{
		entries:   make([]*DeltaEntry, 0, 8), // 预分配 8 个槽位
		size:      0,
		maxLength: maxLength,
		maxSize:   maxSize,
	}
}

// Append 追加 Delta 条目
func (dc *DeltaChain) Append(opType DeltaOpType, key, value []byte) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	// 计算新大小
	newSize := dc.size + uint16(len(key)) + uint16(len(value))
	if newSize < dc.size { // 溢出检测
		return ErrDeltaFull
	}

	// 检查容量限制
	if len(dc.entries) >= dc.maxLength {
		return ErrDeltaFull
	}
	if dc.size >= dc.maxSize {
		return ErrDeltaFull
	}

	// 创建 Delta 条目
	entry := &DeltaEntry{
		opType:    opType,
		key:       key,
		value:     value,
		timestamp: currentTimestamp(),
	}

	// 追加到链
	dc.entries = append(dc.entries, entry)
	dc.size = newSize

	return nil
}

// ShouldCompact 判断是否需要合并
func (dc *DeltaChain) ShouldCompact() bool {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	return len(dc.entries) >= dc.maxLength || dc.size >= dc.maxSize
}

// CompactTo 合并 Delta Chain 到 Mini-Page（批量优化）
//
// 优化策略：
// 1. 使用 map 去重（O(1) 查找）
// 2. 倒序应用 Delta（最新优先）
// 3. 只保留最终有效的键值对
func (dc *DeltaChain) CompactTo(miniPage *MiniPage) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	// 1. 创建新 Mini-Page
	newMiniPage := NewMiniPage(miniPage.level)

	// 2. 创建去重 map（key → applied）
	applied := make(map[string]bool)

	// 3. 先复制旧 Mini-Page 的有效槽位（不标记 applied，允许 Delta 更新）
	for _, slot := range miniPage.slots {
		keyStr := string(slot.key)
		newMiniPage.slots = append(newMiniPage.slots, slot)
		newMiniPage.slotMap[keyStr] = len(newMiniPage.slots) - 1
		newMiniPage.dataSize += uint16(len(slot.key) + len(slot.value))
		// 注意：这里不设置 applied，允许后续 Delta 更新已存在的 key
	}

	// 4. 应用 Delta Chain（倒序，最新优先）
	for i := len(dc.entries) - 1; i >= 0; i-- {
		delta := dc.entries[i]
		keyStr := string(delta.key)

		if applied[keyStr] {
			continue // 已处理过，跳过
		}

		switch delta.opType {
		case DeltaOpInsert, DeltaOpUpdate:
			// 检查 key 是否已存在于新 Mini-Page
			if idx, exists := newMiniPage.slotMap[keyStr]; exists {
				// 更新现有槽位
				newMiniPage.slots[idx].value = delta.value
			} else {
				// 追加新槽位
				newMiniPage.slots = append(newMiniPage.slots, Slot{
					key:   delta.key,
					value: delta.value,
				})
				newMiniPage.slotMap[keyStr] = len(newMiniPage.slots) - 1
				newMiniPage.dataSize += uint16(len(delta.key) + len(delta.value))
			}
			applied[keyStr] = true

		case DeltaOpDelete:
			// 从 Mini-Page 中删除
			if idx, exists := newMiniPage.slotMap[keyStr]; exists {
				// 删除槽位
				newMiniPage.slots = append(newMiniPage.slots[:idx], newMiniPage.slots[idx+1:]...)
				delete(newMiniPage.slotMap, keyStr)
				// dataSize 在更新时已计算，这里不需要调整
				// 重建 slotMap 索引（因为删除改变了索引）
				for i := idx; i < len(newMiniPage.slots); i++ {
					newMiniPage.slotMap[string(newMiniPage.slots[i].key)] = i
				}
			}
			applied[keyStr] = true
		}
	}

	// 5. 替换 Mini-Page 内容（原地更新）
	miniPage.slots = newMiniPage.slots
	miniPage.slotMap = newMiniPage.slotMap
	miniPage.dataSize = newMiniPage.dataSize

	// 6. 清空 Delta Chain
	dc.entries = make([]*DeltaEntry, 0, 8) // 保留预分配容量
	dc.size = 0

	return nil
}

// Get 查找键（读取优化：先查 Delta Chain）
func (dc *DeltaChain) Get(key []byte) ([]byte, bool) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	// 倒序查找（最新优先）
	for i := len(dc.entries) - 1; i >= 0; i-- {
		entry := dc.entries[i]
		if bytes.Equal(entry.key, key) {
			switch entry.opType {
			case DeltaOpInsert, DeltaOpUpdate:
				// 返回值副本
				value := make([]byte, len(entry.value))
				copy(value, entry.value)
				return value, true
			case DeltaOpDelete:
				return nil, false // 已删除
			}
		}
	}

	return nil, false
}

// CheckExists 检查键是否存在（用于 Delete 验证）
func (dc *DeltaChain) CheckExists(key []byte) (exists bool, deleted bool) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	// 倒序查找
	for i := len(dc.entries) - 1; i >= 0; i-- {
		entry := dc.entries[i]
		if bytes.Equal(entry.key, key) {
			if entry.opType == DeltaOpDelete {
				return true, true // 存在但已删除
			}
			return true, false // 存在且有效
		}
	}

	return false, false
}

// Clear 清空 Delta Chain
func (dc *DeltaChain) Clear() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	dc.entries = make([]*DeltaEntry, 0, 8)
	dc.size = 0
}

// Len 获取 Delta Chain 长度
func (dc *DeltaChain) Len() int {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	return len(dc.entries)
}

// Size 获取 Delta Chain 大小（字节）
func (dc *DeltaChain) Size() uint16 {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	return dc.size
}

// IsEmpty 判断是否为空
func (dc *DeltaChain) IsEmpty() bool {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	return len(dc.entries) == 0
}

// Entries 获取 Delta 条目列表（只读，用于调试）
func (dc *DeltaChain) Entries() []*DeltaEntry {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	// 深拷贝，防止外部修改
	entries := make([]*DeltaEntry, 0, len(dc.entries))
	for _, entry := range dc.entries {
		keyCopy := make([]byte, len(entry.key))
		copy(keyCopy, entry.key)

		var valueCopy []byte
		if entry.value != nil {
			valueCopy = make([]byte, len(entry.value))
			copy(valueCopy, entry.value)
		}

		entries = append(entries, &DeltaEntry{
			opType:    entry.opType,
			key:       keyCopy,
			value:     valueCopy,
			timestamp: entry.timestamp,
		})
	}
	return entries
}

// Clone 克隆 Delta Chain（用于快照）
func (dc *DeltaChain) Clone() *DeltaChain {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	clone := &DeltaChain{
		entries:   make([]*DeltaEntry, 0, len(dc.entries)),
		size:      dc.size,
		maxLength: dc.maxLength,
		maxSize:   dc.maxSize,
	}

	for _, entry := range dc.entries {
		// 深拷贝 key 和 value
		keyCopy := make([]byte, len(entry.key))
		copy(keyCopy, entry.key)

		var valueCopy []byte
		if entry.value != nil {
			valueCopy = make([]byte, len(entry.value))
			copy(valueCopy, entry.value)
		}

		clone.entries = append(clone.entries, &DeltaEntry{
			opType:    entry.opType,
			key:       keyCopy,
			value:     valueCopy,
			timestamp: entry.timestamp,
		})
	}

	return clone
}

// Merge 合并另一个 Delta Chain（用于批量操作）
func (dc *DeltaChain) Merge(other *DeltaChain) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	other.mu.RLock()
	defer other.mu.RUnlock()

	// 计算合并后大小
	newSize := dc.size + other.size
	if newSize < dc.size { // 溢出检测
		return ErrDeltaFull
	}

	// 检查容量限制
	newLen := len(dc.entries) + len(other.entries)
	if newLen > dc.maxLength {
		return ErrDeltaFull
	}
	if newSize > dc.maxSize {
		return ErrDeltaFull
	}

	// 追加所有条目
	for _, entry := range other.entries {
		// 深拷贝
		keyCopy := make([]byte, len(entry.key))
		copy(keyCopy, entry.key)

		var valueCopy []byte
		if entry.value != nil {
			valueCopy = make([]byte, len(entry.value))
			copy(valueCopy, entry.value)
		}

		dc.entries = append(dc.entries, &DeltaEntry{
			opType:    entry.opType,
			key:       keyCopy,
			value:     valueCopy,
			timestamp: entry.timestamp,
		})
	}

	dc.size = newSize
	return nil
}
