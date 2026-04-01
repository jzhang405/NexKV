// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package btree

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// SplitInfo 追踪页面分裂后的重定向信息
//
// 当一个页面分裂成两个页面时：
// 1. 原页面的 PageInfo 被标记为 "已分裂"
// 2. SplitInfo 持有新页面的引用
// 3. 访问旧页面的线程会自动跟随到新页面
//
// 这避免了同步更新父节点的高成本 CAS 操作
type SplitInfo struct {
	// OriginalPageID 分裂前的原始页面 ID
	OriginalPageID model.PageID

	// NewPageRef 分裂后的新页面引用
	NewPageRef model.PageID

	// SplitKey 分裂点的 key（用于确定搜索方向）
	SplitKey []byte

	// Timestamp 分裂时间戳（纳秒）
	Timestamp int64

	// splitEpoch 分裂时代（用于检测并发冲突）
	// 与 EpochBasedFreeList 的 epoch 类似
	splitEpoch uint64
}

// NewSplitInfo 创建新的 SplitInfo
func NewSplitInfo(originalPageID, newPageRef model.PageID, splitKey []byte, epoch uint64) *SplitInfo {
	return &SplitInfo{
		OriginalPageID: originalPageID,
		NewPageRef:     newPageRef,
		SplitKey:       splitKey,
		Timestamp:      nowNano(),
		splitEpoch:     epoch,
	}
}

// GetSplitEpoch 返回分裂时代
func (si *SplitInfo) GetSplitEpoch() uint64 {
	return atomic.LoadUint64(&si.splitEpoch)
}

// IsRedirecting 判断给定 key 是否需要重定向到新页面
// 如果 key > SplitKey，说明应该在右页面（新页面）
func (si *SplitInfo) IsRedirecting(key []byte) bool {
	if si == nil || si.SplitKey == nil {
		return false
	}
	return compareKeys(key, si.SplitKey) > 0
}

// GetNewPageRef 返回分裂后的新页面引用
func (si *SplitInfo) GetNewPageRef() model.PageID {
	return si.NewPageRef
}

// splitInfoMap 存储页面分裂信息的全局映射
// key: 分裂前的原始页面 ID
// value: *SplitInfo
// 使用 sync.Map 以支持并发安全访问
type splitInfoMap struct {
	m sync.Map
}

// Store 保存分裂信息
func (m *splitInfoMap) Store(originalPageID model.PageID, info *SplitInfo) {
	m.m.Store(originalPageID, info)
}

// Load 加载分裂信息
func (m *splitInfoMap) Load(originalPageID model.PageID) (*SplitInfo, bool) {
	val, ok := m.m.Load(originalPageID)
	if !ok {
		return nil, false
	}
	return val.(*SplitInfo), true
}

// Delete 删除分裂信息（当不再需要时）
func (m *splitInfoMap) Delete(originalPageID model.PageID) {
	m.m.Delete(originalPageID)
}

// 全局分裂信息映射
var globalSplitInfoMap = &splitInfoMap{}

// GetSplitInfo 获取页面的分裂信息（如果存在）
func GetSplitInfo(pageID model.PageID) (*SplitInfo, bool) {
	return globalSplitInfoMap.Load(pageID)
}

// SetSplitInfo 设置页面的分裂信息
func SetSplitInfo(originalPageID model.PageID, info *SplitInfo) {
	globalSplitInfoMap.Store(originalPageID, info)
}

// DeleteSplitInfo 删除页面的分裂信息
func DeleteSplitInfo(pageID model.PageID) {
	globalSplitInfoMap.Delete(pageID)
}

// PageInfo.SplitInfoExtension 为 PageInfo 添加 SplitInfo 相关字段和方法的扩展
// 这是一个辅助结构，用于在 PageInfo 中存储 SplitInfo

// SplitInfoExtension SplitInfo 相关的 PageInfo 扩展
// 提供原子操作的 SplitInfo 访问
type SplitInfoExtension struct {
	// splitInfo 原子指针，指向 *SplitInfo
	splitInfo unsafe.Pointer

	// splitEpoch 分裂时代（用于检测并发冲突）
	splitEpoch atomic.Uint64
}

// GetSplitInfo 获取 SplitInfo（原子操作）
func (ext *SplitInfoExtension) GetSplitInfo() *SplitInfo {
	if ext == nil {
		return nil
	}
	ptr := atomic.LoadPointer(&ext.splitInfo)
	if ptr == nil {
		return nil
	}
	return (*SplitInfo)(ptr)
}

// SetSplitInfo 设置 SplitInfo（原子操作）
func (ext *SplitInfoExtension) SetSplitInfo(info *SplitInfo) {
	if ext == nil {
		return
	}
	var ptr unsafe.Pointer
	if info != nil {
		ptr = unsafe.Pointer(info)
	}
	atomic.StorePointer(&ext.splitInfo, ptr)
}

// GetSplitEpoch 获取分裂时代
func (ext *SplitInfoExtension) GetSplitEpoch() uint64 {
	if ext == nil {
		return 0
	}
	return ext.splitEpoch.Load()
}

// SetSplitEpoch 设置分裂时代
func (ext *SplitInfoExtension) SetSplitEpoch(epoch uint64) {
	if ext == nil {
		return
	}
	ext.splitEpoch.Store(epoch)
}

// IsSplitEpochChanged 检测分裂时代是否变化
func (ext *SplitInfoExtension) IsSplitEpochChanged(initialEpoch uint64) bool {
	if ext == nil {
		return false
	}
	return ext.splitEpoch.Load() != initialEpoch
}

// compareKeys 比较两个 key
// 返回值: -1 (a < b), 0 (a == b), 1 (a > b)
func compareKeys(a, b []byte) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	for i := 0; i < len(a); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// nowNano 返回当前时间戳（纳秒）
func nowNano() int64 {
	return time.Now().UnixNano()
}

// DumpAllSplitInfo 转储所有 SplitInfo（调试用）
func DumpAllSplitInfo() map[model.PageID]*SplitInfo {
	result := make(map[model.PageID]*SplitInfo)
	globalSplitInfoMap.m.Range(func(key, value any) bool {
		pid := key.(model.PageID)
		info := value.(*SplitInfo)
		result[pid] = info
		return true
	})
	return result
}
