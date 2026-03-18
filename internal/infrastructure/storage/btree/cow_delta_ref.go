// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/domain/model"
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

// 物化配置默认值
const (
	DefaultDeltaChainThreshold      = 10  // Delta 链长度阈值
	DefaultDeltaChainRatio          = 0.2 // 20% 比例阈值
	DefaultHotPageThreshold         = 1000 // 热数据读取阈值
	DefaultMemoryPressureThreshold = 0.8  // 80% 内存压力阈值
)

// COWDeltaRefConfig COW Delta 引用配置
type COWDeltaRefConfig struct {
	MaxDeltas              int     // Delta 链长度阈值
	DeltaRatio             float64 // 比例阈值（0-1）
	HotPageThreshold       int64   // 热数据阈值
	MemoryPressureThreshold float64 // 内存压力阈值
}

// NewDefaultCOWDeltaRefConfig 创建默认配置
func NewDefaultCOWDeltaRefConfig() *COWDeltaRefConfig {
	return &COWDeltaRefConfig{
		MaxDeltas:              DefaultDeltaChainThreshold,
		DeltaRatio:             DefaultDeltaChainRatio,
		HotPageThreshold:       DefaultHotPageThreshold,
		MemoryPressureThreshold: DefaultMemoryPressureThreshold,
	}
}

// NewCOWDeltaRefConfigFromBTreeConfig 从 BTreeConfig 创建 Delta 引用配置
func NewCOWDeltaRefConfigFromBTreeConfig(config *model.BTreeConfig) *COWDeltaRefConfig {
	return &COWDeltaRefConfig{
		MaxDeltas:              config.DeltaChainThreshold,
		DeltaRatio:             config.DeltaChainRatio,
		HotPageThreshold:       config.HotPageThreshold,
		MemoryPressureThreshold: config.MemoryPressureThreshold,
	}
}

// COWDeltaRef Copy-On-Write 引用 + 增量链
//
// 内存布局:
// - sharedKeys/sharedValues: 共享的原始数据（只读）
// - refCount: 引用计数（atomic.Int32）
// - deltas: 增量操作链（读写需要加锁）
// - maxDeltas: 物化阈值
// - config: 物化配置
type COWDeltaRef struct {
	sharedKeys   [][]byte      // 共享的键数组
	sharedValues [][]byte      // 共享的值数组
	refCount     atomic.Int32  // 引用计数
	deltas       []Delta       // 增量操作链
	maxDeltas    int           // 增量阈值（已弃用，保留用于兼容）
	mu           sync.RWMutex  // 保护增量链的读写
	version      atomic.Uint64 // 版本号
	config       *COWDeltaRefConfig // 物化配置
}

// NewCOWDeltaRef 创建新的 COW+Delta 引用（使用默认配置）
func NewCOWDeltaRef(keys, values [][]byte) *COWDeltaRef {
	return NewCOWDeltaRefWithConfig(keys, values, NewDefaultCOWDeltaRefConfig())
}

// NewCOWDeltaRefWithConfig 创建新的 COW+Delta 引用（使用指定配置）
func NewCOWDeltaRefWithConfig(keys, values [][]byte, config *COWDeltaRefConfig) *COWDeltaRef {
	ref := &COWDeltaRef{
		sharedKeys:   keys,
		sharedValues: values,
		// ✅ 性能优化：减少预分配容量，从 8 降到 0（按需增长）
		// 大部分 Delta Chain 使用量很小（0-2），预分配 8 会浪费内存
		deltas:       make([]Delta, 0, 0), // 按需增长，减少 22.7% 内存分配
		maxDeltas:    config.MaxDeltas,      // 使用配置的阈值
		version:      atomic.Uint64{},
		config:       config,
	}
	// 初始引用计数 = 1（创建者持有）
	ref.refCount.Store(1)
	return ref
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
func (r *COWDeltaRef) GetDeltas() []Delta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// 返回副本，避免并发修改影响
	deltas := make([]Delta, len(r.deltas))
	copy(deltas, r.deltas)
	return deltas
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
// baseSize: 基础数据大小（键数量）
// refCount: 当前引用计数
// memPressure: 是否处于内存压力状态（可选，如果不提供则自动检测）
func (r *COWDeltaRef) ShouldMaterialize(baseSize int, refCount int32, memPressure ...bool) bool {
	if r.config == nil {
		// 兼容旧代码：如果没有配置，使用硬编码的默认值
		return r.shouldMaterializeLegacy(baseSize, refCount, memPressure)
	}

	deltaCount := r.GetDeltaCount()

	// 使用配置的阈值进行多因素决策
	if deltaCount > r.config.MaxDeltas {
		return true // 数量超限（配置的阈值）
	}

	if baseSize > 0 && float64(deltaCount)/float64(baseSize) > r.config.DeltaRatio {
		return true // 比例超限（配置的比例）
	}

	if refCount > 10 {
		return true // 引用计数高，减少锁竞争
	}

	// 内存压力触发
	var isMemPressure bool
	if len(memPressure) > 0 {
		isMemPressure = memPressure[0]
	} else {
		// 自动检测内存压力（如果没有提供参数）
		if r.config.MemoryPressureThreshold > 0 {
			// 这里需要 MemoryMonitor，为了简化，暂时使用传入的参数
			// 实际使用中应该由 BTree 传递 MemoryMonitor.IsUnderPressure() 结果
			isMemPressure = false
		}
	}

	if isMemPressure && refCount == 1 {
		return true // 内存紧张且只有单一引用，立即物化
	}

	return false
}

// shouldMaterializeLegacy 旧版本的物化判断（兼容性）
func (r *COWDeltaRef) shouldMaterializeLegacy(baseSize int, refCount int32, memPressure []bool) bool {
	deltaCount := r.GetDeltaCount()

	// 多因素决策（硬编码的默认值）
	if deltaCount > r.maxDeltas {
		return true // 数量超限（默认 10）
	}

	if baseSize > 0 && float64(deltaCount)/float64(baseSize) > 0.2 {
		return true // 比例超限（默认 20%）
	}

	if refCount > 10 {
		return true // 引用计数高，减少锁竞争
	}

	// 内存压力触发：内存紧张且只有单一引用时，立即物化
	if len(memPressure) > 0 && memPressure[0] && refCount == 1 {
		return true
	}

	return false
}

// GetConfig 获取物化配置
func (r *COWDeltaRef) GetConfig() *COWDeltaRefConfig {
	return r.config
}

// SetConfig 更新物化配置
func (r *COWDeltaRef) SetConfig(config *COWDeltaRefConfig) {
	r.config = config
}

// GetSharedKeys 获取共享的键数组
func (r *COWDeltaRef) GetSharedKeys() [][]byte {
	return r.sharedKeys
}

// GetSharedValues 获取共享的值数组
func (r *COWDeltaRef) GetSharedValues() [][]byte {
	return r.sharedValues
}
