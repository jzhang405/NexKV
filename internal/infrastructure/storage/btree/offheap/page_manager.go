// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
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
type PageManager struct {
	allocator        OffHeapAllocator      // 跨平台内存分配器
	base             unsafe.Pointer        // mmap 起始地址
	total            uint32                // 总页数
	used             atomic.Uint32         // 已使用页数
	nextPageID       atomic.Uint32         // 下一个要分配的页面ID（单调递增）
	freeList         *LockFreeQueue        // 空闲 PageID 队列（lock-free）
	delayedFreeList  *LockFreeQueue        // 延迟释放 PageID 队列（已释放但需等待1个epoch）
	currentEpoch     atomic.Uint64         // 当前 epoch（用于延迟回收）
	tracker          *PageLifecycleTracker // 页面生命周期追踪器（调试用）
	initOnce         sync.Once             //nolint:unused // 确保初始化一次
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

// GetRecommendedMmapSize 根据系统物理内存计算推荐 mmap 大小
// ratio: 使用物理内存的比例（推荐 0.6）
// 最小 64MB，最大 6GB（受 MaxMmapSize 约束）
func GetRecommendedMmapSize(ratio float64) (int, error) {
	sysInfo := &syscall.Sysinfo_t{}
	if err := syscall.Sysinfo(sysInfo); err != nil {
		fmt.Fprintf(os.Stderr, "offheap: failed to get system memory info: %v, fallback to 64MB\n", err)
		return 64 << 20, nil
	}

	totalMem := uint64(sysInfo.Totalram) * uint64(sysInfo.Unit)
	size := uint64(float64(totalMem) * ratio)

	const minSize = 64 << 20 // 64MB

	if size < minSize {
		size = minSize
	}
	if size > uint64(MaxMmapSize) {
		size = uint64(MaxMmapSize)
	}
	return int(size), nil
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
		allocator:       allocator,
		base:            base,
		total:           uint32(maxPages),
		nextPageID:      atomic.Uint32{},
		freeList:        NewLockFreeQueue(),
		delayedFreeList: NewLockFreeQueue(),
		tracker:         NewPageLifecycleTracker(false),
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

// Alloc 分配一个页面
// 优先从 freeList 回收已释放页面，nextPageID 作为 fallback
// 注意：delayedFreeList → freeList 由 epoch 机制（AdvanceDelayedFreeList）管理
// 不在 Alloc 中主动 flush，避免 COW 操作期间回收正在使用的源页面
func (pm *PageManager) Alloc() (uint32, error) {
	// 路径 1：从 freeList 取已释放页面（lock-free）
	if pageID, ok := pm.freeList.Dequeue(); ok {
		// 清零页面内容，防止旧数据污染
		pm.clearPage(pageID)

		// 修复：重置版本号为 1
		// 问题：clearPage 将整个页面清零，导致 header.version 变成 0
		// 旧引用期望的版本可能是非零值，而 GetVersionSafe 返回 0
		// 这导致版本不匹配错误（stale child reference），实际上是页面已被正确回收
		// 解决：在清零后设置版本号为 1，确保旧引用能正确检测到版本不匹配
		ptr := pm.PageIDToPtr(pageID)
		header := (*PageHeader)(ptr)
		header.version = 1

		pm.used.Add(1)
		pm.tracker.RecordAlloc(pageID)
		return pageID, nil
	}

	// // 路径 2：fallback，nextPageID 递增
	pageID := pm.nextPageID.Load()
	if pageID >= pm.total {
		return 0, errpkg.OffHeapOutOfMemory(int(pm.total), int(pm.used.Load()))
	}
	pm.nextPageID.Add(1)
	// 清零页面内容，防止旧数据污染
	pm.clearPage(pageID)
	// 路径 2 是全新页面，但也要设置 version=1
	// 确保与路径 1 一致，避免同一页面不同 version
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	header.version = 1

	pm.used.Add(1)
	pm.tracker.RecordAlloc(pageID)
	return pageID, nil
}

// Free 释放一个页面（加入延迟释放列表）
// 修改记录 (2026-04-01): 添加 deleted 和 deleteEpoch 支持 Epoch 延迟回收
// 注意：不再增加 version++，因为 epoch 延迟机制 + Alloc 时 version=1 设置已经足够
func (pm *PageManager) Free(pageID uint32) error {
	if pageID >= pm.total {
		return errpkg.OffHeapInvalidPageID(int(pageID), int(pm.total))
	}

	// 设置删除标记和 epoch
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	header.deleted = 1
	header.deleteEpoch = pm.currentEpoch.Load()
	// 不再 version++，因为 epoch 延迟机制已经可以防止页面过早被重用
	// 且 version++ 在 Free 中会与正在读取该页面的 goroutine 产生竞态

	pm.tracker.RecordFree(pageID)
	pm.delayedFreeList.Enqueue(pageID)
	pm.used.Add(^uint32(0))
	return nil
}

// AdvanceDelayedFreeList 将延迟释放列表中的页面移到可用列表
// 这应该在 epoch 推进后调用，确保没有 goroutine 仍在访问这些页面
// 修改记录 (2026-04-01): 实现 epoch 延迟逻辑，防止页面过早被重用
func (pm *PageManager) AdvanceDelayedFreeList() int {
	moved := 0
	minEpochDiff := uint64(5) // 至少 5 个 epoch 的延迟

	currentEpoch := pm.currentEpoch.Load()

	for {
		pageID, ok := pm.delayedFreeList.Dequeue()
		if !ok {
			break
		}

		// 检查页面是否过了足够的 epoch
		ptr := pm.PageIDToPtr(pageID)
		header := (*PageHeader)(ptr)

		// 如果页面太新，放回队列尾部等待
		if currentEpoch-header.deleteEpoch < minEpochDiff {
			pm.delayedFreeList.Enqueue(pageID)
			break // 队列已排序，越老越前面
		}

		// 页面够老，移到 freeList
		header.deleted = 0 // 清除删除标记（页面现在可用）
		pm.freeList.Enqueue(pageID)
		moved++
	}
	return moved
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
func (pm *PageManager) GetPageTracker() *PageLifecycleTracker {
	return pm.tracker
}

// GetPage4095Report 获取 Page 4095 的专门报告（调试用）
func (pm *PageManager) GetPage4095Report() string {
	return pm.tracker.GetPage4095Report()
}

// GetHighPageIDReport 获取所有 > 4000 的页面报告（调试用）
func (pm *PageManager) GetHighPageIDReport() string {
	return pm.tracker.GetHighPageIDReport()
}

// GetPageTrackingStats 获取追踪统计信息（调试用）
func (pm *PageManager) GetPageTrackingStats() map[string]any {
	return pm.tracker.Stats()
}
