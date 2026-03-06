// Package bftree 提供 Bf-Tree 的叶子节点实现
package bftree

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

// 错误定义
var (
	ErrNilKey    = errors.New("key cannot be nil")
	ErrEmptyKey  = errors.New("key cannot be empty")
	ErrNilValue  = errors.New("value cannot be nil")
	ErrDeltaFull = errors.New("delta chain is full")
)

// LeafNode Bf-Tree 叶子节点
//
// 结构设计：
// - Mini-Page 机制：3-level 分层存储，减少空间占用
// - Delta Chain 优化：写入先记录到 Delta Chain，定期合并
// - Bitmap 并发控制：细粒度锁，减少竞争
type LeafNode struct {
	// 基础元数据
	pageID  uint64    // 页面 ID（唯一标识）
	level   PageLevel // Mini-Page 级别
	version uint64    // 版本号（用于并发控制）

	// Mini-Page 存储
	miniPage *MiniPage // Mini-Page（L1/L2/L3/L4/L5/L6/Full）

	// Delta Chain 优化
	deltas    []*DeltaEntry // Delta 链（未合并的写入）
	deltaSize uint16        // Delta 大小（字节）

	// 并发控制
	mu sync.RWMutex // 读写锁（MVP 使用 RWMutex）

	// 配置
	maxDeltaLen  int    // 最大 Delta Chain 长度
	maxDeltaSize uint16 // 最大 Delta Chain 大小
}

// MiniPage Mini-Page 结构（3-level 分层存储）
//
// 分级说明：
// - L1 (64B):  存储约 1-2 个键值对
// - L2 (128B): 存储约 4 个键值对
// - L3 (256B): 存储约 8 个键值对
// - L4 (512B): 存储约 16 个键值对
// - L5 (1KB):  存储约 32 个键值对
// - L6 (2KB):  存储约 64 个键值对
// - Full (4KB): 完整页面，存储约 128 个键值对
type MiniPage struct {
	level    PageLevel      // 页面级别
	bitmap   uint64         // 位图（标记空闲槽位，0=空闲，1=占用）
	slots    []Slot         // 槽位数组
	slotMap  map[string]int // key → slotIndex（O(1) 查找）
	dataSize uint16         // 数据大小（字节）
	capacity uint16         // 容量（字节）
}

// Slot 槽位（存储键值对）
type Slot struct {
	key   []byte // 键（内联存储）
	value []byte // 值（内联存储）
}

// DeltaEntry Delta Chain 条目（未合并的写入）
//
// 设计目的：
// - 写入操作先记录到 Delta Chain
// - 定期合并到主 Mini-Page（Compact）
// - 减少写入放大，提升性能
type DeltaEntry struct {
	opType    DeltaOpType // 操作类型
	key       []byte      // 键
	value     []byte      // 值（Insert/Update）
	timestamp uint64      // 时间戳（用于排序）
}

// DeltaOpType Delta 操作类型
type DeltaOpType uint8

const (
	DeltaOpInsert DeltaOpType = iota + 1 // 插入
	DeltaOpUpdate                        // 更新
	DeltaOpDelete                        // 删除
)

// NewLeafNode 创建新的叶子节点
//
// 参数：
//   - pageID: 页面 ID
//   - level: Mini-Page 级别（默认 L1）
//
// 返回：
//   - 初始化的 LeafNode
func NewLeafNode(pageID uint64, level PageLevel) *LeafNode {
	return &LeafNode{
		pageID:       pageID,
		level:        level,
		version:      1,
		miniPage:     NewMiniPage(level),
		deltas:       make([]*DeltaEntry, 0, 8), // 预分配 8 个 Delta 槽位
		deltaSize:    0,
		maxDeltaLen:  8,                                  // 默认最大 8 个 Delta
		maxDeltaSize: uint16(maxSizeForLevel(level) / 2), // 默认容量 50%
	}
}

// NewMiniPage 创建新的 Mini-Page
func NewMiniPage(level PageLevel) *MiniPage {
	capacity := maxSizeForLevel(level)
	slotCount := maxSlotsForLevel(level)

	return &MiniPage{
		level:    level,
		bitmap:   0,                               // 初始无占用
		slots:    make([]Slot, 0, slotCount),      // 预分配槽位数组
		slotMap:  make(map[string]int, slotCount), // P1-4: O(1) 查找
		dataSize: 0,
		capacity: capacity,
	}
}

// maxSizeForLevel 获取各级别最大大小
func maxSizeForLevel(level PageLevel) uint16 {
	switch level {
	case L1:
		return 64
	case L2:
		return 128
	case L3:
		return 256
	case L4:
		return 512
	case L5:
		return 1024
	case L6:
		return 2048
	default:
		return 4096 // Full-Page
	}
}

// maxSlotsForLevel 获取各级别最大槽数
func maxSlotsForLevel(level PageLevel) int {
	// 假设平均槽位大小为 32 字节（key+value+overhead）
	return int(maxSizeForLevel(level) / 32)
}

// Get 获取键值
//
// 查询顺序：
// 1. 先查 Delta Chain（最新写入）
// 2. 再查 Mini-Page（主数据）
//
// 返回：
//   - value: 值（不存在返回 nil）
//   - found: 是否找到
func (n *LeafNode) Get(key []byte) ([]byte, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	// P1-3: 使用 bytes.Equal 替代 string 比较（性能提升 4x）
	// 1. 先查 Delta Chain（倒序，最新优先）
	for i := len(n.deltas) - 1; i >= 0; i-- {
		delta := n.deltas[i]
		if bytes.Equal(delta.key, key) {
			switch delta.opType {
			case DeltaOpInsert, DeltaOpUpdate:
				// P1-7: 返回副本，防止外部修改
				value := make([]byte, len(delta.value))
				copy(value, delta.value)
				return value, true
			case DeltaOpDelete:
				return nil, false // 已删除
			}
		}
	}

	// 2. 再查 Mini-Page
	slotIndex := n.miniPage.findSlot(key)
	if slotIndex == -1 {
		return nil, false
	}

	slot := &n.miniPage.slots[slotIndex]
	// P1-7: 返回副本
	value := make([]byte, len(slot.value))
	copy(value, slot.value)
	return value, true
}

// findSlot 查找槽位（P1-4: 使用 map O(1) 查找）
// 返回：槽位索引（-1 表示未找到）
func (mp *MiniPage) findSlot(key []byte) int {
	// P1-4: 使用 map 实现 O(1) 查找
	idx, ok := mp.slotMap[string(key)]
	if !ok {
		return -1
	}
	return idx
}

// Set 设置键值（写入 Delta Chain）
//
// 写入策略：
// - 写入先记录到 Delta Chain
// - 定期合并到 Mini-Page（Compact）
//
// 返回：
//   - error: 错误（nil 表示成功）
func (n *LeafNode) Set(key, value []byte) error {
	// P1-6: 添加参数验证
	if key == nil {
		return ErrNilKey
	}
	if len(key) == 0 {
		return ErrEmptyKey
	}
	if value == nil {
		return ErrNilValue
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// P1-6: 检查容量
	newDeltaSize := n.deltaSize + uint16(len(key)) + uint16(len(value))
	if uint16(len(n.deltas)) >= uint16(n.maxDeltaLen) {
		return ErrDeltaFull
	}
	if newDeltaSize < n.deltaSize { // 溢出检测
		return ErrDeltaFull
	}

	// 创建 Delta 条目
	// 深拷贝键和值，防止外部修改影响 Delta Chain
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	delta := &DeltaEntry{
		opType:    DeltaOpInsert,
		key:       keyCopy,
		value:     valueCopy,
		timestamp: currentTimestamp(),
	}

	// 追加到 Delta Chain
	n.deltas = append(n.deltas, delta)
	n.deltaSize = newDeltaSize

	// P1-5: 检查是否需要合并并立即执行
	if n.shouldCompact() {
		if err := n.compact(); err != nil {
			return err
		}
	}

	return nil
}

// shouldCompact 判断是否需要合并 Delta Chain
//
// 合并条件：
// 1. Delta Chain 长度 >= 阈值（默认 8）
// 2. Delta 大小 >= Mini-Page 容量的 50%
func (n *LeafNode) shouldCompact() bool {
	if len(n.deltas) >= n.maxDeltaLen {
		return true
	}

	return n.deltaSize >= n.maxDeltaSize
}

// P1-5: compact 合并 Delta Chain 到 Mini-Page
// compact 合并 Delta Chain 到 Mini-Page
func (n *LeafNode) compact() error {
	// 1. 创建新 Mini-Page
	newMiniPage := NewMiniPage(n.level)

	// 2. 将旧 Mini-Page 的槽位复制到临时切片
	// 使用切片而非 map 以保持插入顺序
	tempSlots := append([]Slot(nil), n.miniPage.slots...)
	// 按键排序确保顺序稳定
	sort.Slice(tempSlots, func(i, j int) bool {
		return compareKeys(tempSlots[i].key, tempSlots[j].key) < 0
	})

	// 3. 应用 Delta Chain（倒序，最新优先）
	// 记录被 Delta 处理过的键
	processed := make(map[string]bool)
	for i := len(n.deltas) - 1; i >= 0; i-- {
		delta := n.deltas[i]
		keyStr := string(delta.key)

		// 标记为已处理
		processed[keyStr] = true

		switch delta.opType {
		case DeltaOpInsert, DeltaOpUpdate:
			// 在切片中查找并更新/添加键
			found := false
			for idx, slot := range tempSlots {
				if string(slot.key) == keyStr {
					// 更新现有槽位
					tempSlots[idx] = Slot{
						key:   delta.key,
						value: delta.value,
					}
					found = true
					break
				}
			}
			// 如果没找到，添加新槽位
			if !found {
				tempSlots = append(tempSlots, Slot{
					key:   delta.key,
					value: delta.value,
				})
			}

		case DeltaOpDelete:
			// 从切片中删除键
			for idx, slot := range tempSlots {
				if string(slot.key) == keyStr {
					// 删除元素
					tempSlots = append(tempSlots[:idx], tempSlots[idx+1:]...)
					break
				}
			}
		}
	}

	// 4. 将所有槽位添加到新 Mini-Page
	for _, slot := range tempSlots {
		keyStr := string(slot.key)
		newMiniPage.slots = append(newMiniPage.slots, slot)
		newMiniPage.slotMap[keyStr] = len(newMiniPage.slots) - 1
		newMiniPage.dataSize += uint16(len(slot.key) + len(slot.value))
	}

	// 5. 替换 Mini-Page
	n.miniPage = newMiniPage

	// 6. 清空 Delta Chain
	n.deltas = make([]*DeltaEntry, 0, 8)
	n.deltaSize = 0

	return nil
}

// currentTimestamp 获取当前时间戳（纳秒）
func currentTimestamp() uint64 {
	return uint64(0) // MVP 简化实现
}

// Size 获取节点大小（字节）
func (n *LeafNode) Size() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()

	size := uint64(n.miniPage.dataSize)
	size += uint64(n.deltaSize)
	return size
}

// DeltaCount 获取 Delta Chain 长度
func (n *LeafNode) DeltaCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.deltas)
}

// Delete 删除键值（写入 Delta Chain）
//
// 删除策略：
// - 写入删除标记到 Delta Chain（DeltaOpDelete）
// - 定期合并到 Mini-Page（Compact）
// - Get 时先查 Delta Chain，返回"已删除"
//
// 返回：
//   - error: 错误（nil 表示成功）
//   - ErrKeyNotFound: 键不存在
func (n *LeafNode) Delete(key []byte) error {
	// P1-6: 添加参数验证
	if key == nil {
		return ErrNilKey
	}
	if len(key) == 0 {
		return ErrEmptyKey
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// 检查键是否存在（先查 Delta Chain，再查 Mini-Page）
	exists := false
	for i := len(n.deltas) - 1; i >= 0; i-- {
		delta := n.deltas[i]
		if bytes.Equal(delta.key, key) {
			if delta.opType == DeltaOpDelete {
				// 已经被删除
				return ErrKeyNotFound
			}
			exists = true
			break
		}
	}

	if !exists {
		// 检查 Mini-Page
		slotIndex := n.miniPage.findSlot(key)
		if slotIndex == -1 {
			return ErrKeyNotFound
		}
	}

	// 检查 Delta Chain 容量
	if uint16(len(n.deltas)) >= uint16(n.maxDeltaLen) {
		// 容量已满，先触发合并
		if err := n.compact(); err != nil {
			return err
		}
	}

	// 创建删除 Delta 条目
	// 深拷贝键，防止外部修改影响 Delta Chain
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	delta := &DeltaEntry{
		opType:    DeltaOpDelete,
		key:       keyCopy,
		value:     nil,
		timestamp: currentTimestamp(),
	}

	// 追加到 Delta Chain
	n.deltas = append(n.deltas, delta)

	// 检查是否需要合并
	if n.shouldCompact() {
		if err := n.compact(); err != nil {
			return err
		}
	}

	return nil
}
