package btree

import (
	"sync"
	"sync/atomic"
)

// DeltaOp 增量操作类型
type DeltaOp int

const (
	DeltaInsert DeltaOp = iota
	DeltaUpdate
	DeltaDelete
)

// Delta 表示单个增量变化
type Delta struct {
	op    DeltaOp
	key   []byte
	value []byte // 仅用于 Insert/Update
}

// COWDeltaRef Copy-On-Write 引用 + 增量链
//
// 内存布局:
// - sharedKeys/sharedValues: 共享的原始数据（只读）
// - refCount: 引用计数（atomic.Int32）
// - deltas: 增量操作链（读写需要加锁）
// - maxDeltas: 物化阈值
//
// 注意：不使用 sync.Pool，因为 Delta 切片很小，GC 可以处理
// 如果未来需要优化，可以考虑使用 sync.Pool
type COWDeltaRef struct {
	sharedKeys   [][]byte     // 共享的键数组
	sharedValues [][]byte     // 共享的值数组
	refCount     atomic.Int32 // 引用计数
	deltas       []Delta      // 增量操作链
	maxDeltas    int          // 增量阈值
	mu           sync.RWMutex // 保护增量链的读写
	version      atomic.Uint64// 版本号
}

// NewCOWDeltaRef 创建新的 COW+Delta 引用
func NewCOWDeltaRef(keys, values [][]byte) *COWDeltaRef {
	return &COWDeltaRef{
		sharedKeys:   keys,
		sharedValues: values,
		deltas:       make([]Delta, 0, 8), // 预分配容量
		maxDeltas:    10,                   // 默认阈值
		version:      atomic.Uint64{},
	}
}

// Retain 增加引用计数
func (r *COWDeltaRef) Retain() {
	r.refCount.Add(1)
}

// Release 减少引用计数，返回是否为最后一个引用
func (r *COWDeltaRef) Release() bool {
	newCount := r.refCount.Add(-1)
	return newCount == 0
}

// GetRefCount 获取引用计数
func (r *COWDeltaRef) GetRefCount() int32 {
	return r.refCount.Load()
}

// GetDeltaCount 获取增量数量（需要读锁）
func (r *COWDeltaRef) GetDeltaCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.deltas)
}

// AppendDelta 添加增量操作（需要写锁）
func (r *COWDeltaRef) AppendDelta(delta Delta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deltas = append(r.deltas, delta)
	r.version.Add(1)
}

// GetDeltas 获取增量快照（用于读取）
//
// ⚠️ 重要：调用方在遍历返回值期间，读锁会被持有
// - RWMutex 的 RUnlock 通过 defer 保证，访问 deltas 引用期间锁一直被持有
// - 这确保了在遍历期间增量链不会被并发修改
// - 调用方应快速完成遍历，避免长时间持有读锁
func (r *COWDeltaRef) GetDeltas() []Delta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deltas
}

// GetVersion 获取版本号
func (r *COWDeltaRef) GetVersion() uint64 {
	return r.version.Load()
}

// CompactDeltas 压缩增量链（合并重复 key 的操作）
func (r *COWDeltaRef) CompactDeltas() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.deltas) < 2 {
		return
	}

	// 使用 map 保留最新的操作
	keyMap := make(map[string]Delta)
	for _, delta := range r.deltas {
		keyMap[string(delta.key)] = delta
	}

	// 重建增量链
	r.deltas = make([]Delta, 0, len(keyMap))
	for _, delta := range keyMap {
		r.deltas = append(r.deltas, delta)
	}
}

// ShouldMaterialize 判断是否需要物化
func (r *COWDeltaRef) ShouldMaterialize(baseSize int, refCount int32) bool {
	deltaCount := r.GetDeltaCount()

	// 多因素决策
	if deltaCount > r.maxDeltas {
		return true // 数量超限
	}

	if float64(deltaCount)/float64(baseSize) > 0.2 {
		return true // 比例超限（20%）
	}

	if refCount > 10 {
		return true // 引用计数高，减少锁竞争
	}

	return false
}
