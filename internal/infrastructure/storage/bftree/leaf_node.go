// Package bftree 提供 Bf-Tree 的叶子节点实现
package bftree

import (
	"sync"
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
	// bitmap    uint64    // Bitmap 锁预留（P1 优化）

	// 统计信息（预留字段，P1 实现）
	// readCount  uint32 // 读取计数（用于提升决策）
	// scanCount  uint32 // 扫描计数（用于提升决策）
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
	level    PageLevel // 页面级别
	bitmap   uint64    // 位图（标记空闲槽位，0=空闲，1=占用）
	slots    []Slot    // 槽位数组
	dataSize uint16    // 数据大小（字节）
	capacity uint16    // 容量（字节）
}

// Slot 槽位（存储键值对）
type Slot struct {
	key   []byte // 键（内联存储）
	value []byte // 值（内联存储）
	// 预留字段（P1 实现）
	// keySize   uint16 // 键长度（字节）
	// valueSize uint32 // 值长度（字节）
	// next      *Slot  // 链表指针（用于冲突解决）
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
	// 预留字段（P1 实现）
	// oldValue []byte // 旧值（Update/Delete，用于回滚）
}

// DeltaOpType Delta 操作类型
type DeltaOpType uint8

const (
	DeltaOpInsert DeltaOpType = iota + 1 // 插入
	DeltaOpUpdate                         // 更新
	DeltaOpDelete                         // 删除
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
		pageID:   pageID,
		level:    level,
		version:  1,
		miniPage: NewMiniPage(level),
		deltas:   make([]*DeltaEntry, 0, 8), // 预分配 8 个 Delta 槽位
		deltaSize: 0,
	}
}

// NewMiniPage 创建新的 Mini-Page
func NewMiniPage(level PageLevel) *MiniPage {
	capacity := maxSizeForLevel(level)
	slotCount := maxSlotsForLevel(level)

	return &MiniPage{
		level:    level,
		bitmap:   0,                          // 初始无占用
		slots:    make([]Slot, 0, slotCount), // 预分配槽位数组
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

	// 1. 先查 Delta Chain（倒序，最新优先）
	for i := len(n.deltas) - 1; i >= 0; i-- {
		delta := n.deltas[i]
		if string(delta.key) == string(key) {
			switch delta.opType {
			case DeltaOpInsert, DeltaOpUpdate:
				return delta.value, true
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
	return slot.value, true
}

// findSlot 查找槽位（线性搜索）
// 返回：槽位索引（-1 表示未找到）
func (mp *MiniPage) findSlot(key []byte) int {
	for i := range mp.slots {
		if string(mp.slots[i].key) == string(key) {
			return i
		}
	}
	return -1
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
	n.mu.Lock()
	defer n.mu.Unlock()

	// 创建 Delta 条目
	delta := &DeltaEntry{
		opType:   DeltaOpInsert,
		key:      key,
		value:    value,
		timestamp: currentTimestamp(),
	}

	// 追加到 Delta Chain
	n.deltas = append(n.deltas, delta)
	n.deltaSize += uint16(len(key) + len(value))

	// 检查是否需要合并（TODO: 后续 Phase 实现 Compact）
	_ = n.shouldCompact()

	return nil
}

// shouldCompact 判断是否需要合并 Delta Chain
//
// 合并条件：
// 1. Delta Chain 长度 >= 阈值（默认 8）
// 2. Delta 大小 >= Mini-Page 容量的 50%
func (n *LeafNode) shouldCompact() bool {
	const maxDeltaLen = 8
	const maxDeltaSizeRatio = 0.5

	if len(n.deltas) >= maxDeltaLen {
		return true
	}

	maxDeltaSize := uint16(float64(n.miniPage.capacity) * maxDeltaSizeRatio)
	return n.deltaSize >= maxDeltaSize
}

// currentTimestamp 获取当前时间戳（纳秒）
func currentTimestamp() uint64 {
	// TODO: 使用更精确的时间戳（HLC）
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
