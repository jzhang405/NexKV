// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"sync"
	"sync/atomic"
	"unsafe"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

const (
	// PageSize 页面大小（4KB）
	PageSize = 4096
	// MaxMmapSize 默认最大 mmap 大小（6GB）
	MaxMmapSize = 6 << 30
	// MaxPageID 最大 PageID（32 位限制）
	MaxPageID = uint32(0xFFFFFFFF)
)

var (
	// 全局 PageManager 单例
	globalPM     *PageManager
	globalPMOnce sync.Once
)

// PageManager 管理 Off-Heap 内存中的 4KB 页面
// 引用计数由上层 btree 的 PageRef 管理，PageManager 只负责 alloc/free 的物理页面生命周期。
type PageManager struct {
	allocator  OffHeapAllocator // 跨平台内存分配器
	base       unsafe.Pointer   // mmap 起始地址
	total      uint32           // 总页数
	used       atomic.Uint32    // 已使用页数
	nextPageID atomic.Uint32    // 下一个要分配的页面ID（单调递增）
	freeList   *LockFreeQueue   // 空闲 PageID 队列（lock-free）
	tracker    *PageInspector   // 页面生命周期追踪器（调试用，默认 disabled）
}

// InitPageManager 初始化全局 PageManager
func InitPageManager(mmapSize int) error {
	var initErr error
	globalPMOnce.Do(func() {
		pm, err := NewPageManager(mmapSize)
		if err != nil {
			initErr = err
			return
		}
		globalPM = pm
	})
	return initErr
}

// GetPageManager 获取全局 PageManager
func GetPageManager() *PageManager {
	return globalPM
}

// NewPageManager 创建新的 PageManager
func NewPageManager(mmapSize int) (*PageManager, error) {
	maxPages := mmapSize / PageSize
	if maxPages > int(MaxPageID) {
		return nil, errpkg.OffHeapMMapSizeExceedsLimit(int64(mmapSize), int64(MaxPageID))
	}

	allocator, err := NewAllocator(mmapSize)
	if err != nil {
		return nil, errpkg.OffHeapCreateAllocatorFailed(err)
	}

	base, err := allocator.Alloc(mmapSize)
	if err != nil {
		allocator.Free(base, mmapSize)
		return nil, errpkg.OffHeapAllocMemoryFailed(err)
	}

	pm := &PageManager{
		allocator:  allocator,
		base:       base,
		total:      uint32(maxPages),
		nextPageID: atomic.Uint32{},
		freeList:   NewLockFreeQueue(),
		tracker:    NewPageInspector(false),
	}

	return pm, nil
}

// clearPage 清零页面内容
func (pm *PageManager) clearPage(pageID uint32) {
	ptr := pm.PageIDToPtr(pageID)
	// 清零整个页面
	slice := unsafe.Slice((*byte)(ptr), PageSize)
	for i := range slice {
		slice[i] = 0
	}
}

// Alloc 分配一个页面。
// 优先从 freeList 回收已释放页面，nextPageID 作为 fallback。
func (pm *PageManager) Alloc() (uint32, error) {
	// 路径 1：从 freeList 取已释放页面（lock-free）
	if pageID, ok := pm.freeList.Dequeue(); ok {
		pm.clearPage(pageID)
		ptr := pm.PageIDToPtr(pageID)
		header := (*PageHeader)(ptr)
		header.version = 1
		header.deleted = 0

		pm.used.Add(1)
		pm.tracker.RecordAlloc(pageID)
		return pageID, nil
	}

	// 路径 2：fallback，nextPageID 原子递增
	// Add(1) 返回递增后的值，减 1 得到本次分配的 pageID。
	// 原子操作保证并发安全——多个 goroutine 不会获得相同 pageID。
	newVal := pm.nextPageID.Add(1)
	pageID := newVal - 1
	if pageID >= pm.total {
		return 0, errpkg.OffHeapOutOfMemory(int(pm.total), int(pm.used.Load()))
	}
	pm.clearPage(pageID)
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	header.version = 1
	header.deleted = 0

	pm.used.Add(1)
	pm.tracker.RecordAlloc(pageID)
	return pageID, nil
}

// Free 释放一个页面。
// 调用方（btree 的 PageRef.Release）保证在引用计数归零后才调用此方法，
// 因此页面可以安全地直接回收。
func (pm *PageManager) Free(pageID uint32) error {
	if pageID >= pm.total {
		return errpkg.OffHeapInvalidPageID(int(pageID), int(pm.total))
	}

	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)

	// 设置 deleted=1，防止 SearchChild 在页面进入 freeList 后
	// 但尚未被 Alloc 回收前，误读到已释放页面的脏数据导致自环
	header.deleted = 1
	pm.freeList.Enqueue(pageID)
	pm.used.Add(^uint32(0))
	pm.tracker.RecordFree(pageID)
	return nil
}

// PageIDToPtr 将 PageID 转换为内存地址
func (pm *PageManager) PageIDToPtr(pageID uint32) unsafe.Pointer {
	if pageID >= pm.total {
		panic("offheap: pageID out of range")
	}
	return unsafe.Add(pm.base, uintptr(pageID)*PageSize)
}

//go:inline
func (pm *PageManager) pageIDToPtrUnchecked(pageID uint32) unsafe.Pointer {
	return unsafe.Add(pm.base, uintptr(pageID)*PageSize)
}

// Stats 返回 PageManager 统计信息
type Stats struct {
	Total     uint32 // 总页数
	Used      uint32 // 已使用页数
	Free      uint32 // 空闲页数
	TotalSize int    // 总字节数
	UsedSize  int    // 已使用字节数
}

// GetStats 获取统计信息
func (pm *PageManager) GetStats() Stats {
	used := pm.used.Load()
	return Stats{
		Total:     pm.total,
		Used:      used,
		Free:      pm.total - used,
		TotalSize: int(pm.total) * PageSize,
		UsedSize:  int(used) * PageSize,
	}
}

// Close 释放 PageManager 占用的资源
func (pm *PageManager) Close() error {
	if pm.allocator != nil {
		return pm.allocator.Free(pm.base, int(pm.total)*PageSize)
	}
	return nil
}

// Platform 返回当前平台名称
func (pm *PageManager) Platform() string {
	if pm.allocator != nil {
		return pm.allocator.Platform()
	}
	return "unknown"
}

// PageSize 返回页面大小
func (pm *PageManager) PageSize() int {
	return PageSize
}

// EnablePageTracking 启用页面生命周期追踪（调试用）
func (pm *PageManager) EnablePageTracking() {
	pm.tracker.Enable()
}

// DisablePageTracking 禁用页面生命周期追踪
func (pm *PageManager) DisablePageTracking() {
	pm.tracker.Disable()
}

// GetPageTracker 获取页面生命周期追踪器（调试用）
func (pm *PageManager) GetPageTracker() *PageInspector {
	return pm.tracker
}

// GetPageTrackingStats 获取追踪统计信息（调试用）
func (pm *PageManager) GetPageTrackingStats() map[string]any {
	return pm.tracker.Stats()
}

// GetFreeListSize 返回 freeList 的大小（调试用）
func (pm *PageManager) GetFreeListSize() int {
	return pm.freeList.Size()
}

// NextPageID 返回下一个要分配的 PageID（即已分配页面的上限，不含 freeList 回收页面）。
// 用于 Inspector 遍历所有可能存活的页面：pageID ∈ [1, NextPageID())。
func (pm *PageManager) NextPageID() uint32 {
	return pm.nextPageID.Load()
}

// IsDeleted 检查页面是否被标记为已删除（调试用）
func (pm *PageManager) IsDeleted(pageID uint32) bool {
	if pageID == 0 || pageID >= pm.total {
		return false
	}
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	return header.deleted == 1
}
