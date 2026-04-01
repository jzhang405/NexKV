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
	allocator       OffHeapAllocator      // 跨平台内存分配器
	base            unsafe.Pointer        // mmap 起始地址
	total           uint32                // 总页数
	used            atomic.Uint32         // 已使用页数
	nextPageID      atomic.Uint32         // 下一个要分配的页面ID（单调递增）
	freeList        *LockFreeQueue        // 空闲 PageID 队列（lock-free）
	delayedFreeList *LockFreeQueue        // 延迟释放 PageID 队列（已释放但需等待1个epoch）
	currentEpoch    atomic.Uint64         // 当前 epoch（用于延迟回收）
	tracker         *PageLifecycleTracker // 页面生命周期追踪器（调试用）
	initOnce        sync.Once             //nolint:unused // 确保初始化一次
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
		// ⭐ Reference Counting: 初始 refCount 为 0
		header.refCount = 0
		header.deleted = 0
		header.inQueue = 0

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
	// ⭐ Reference Counting: 初始 refCount 为 0
	header.refCount = 0
	header.deleted = 0
	header.inQueue = 0

	pm.used.Add(1)
	pm.tracker.RecordAlloc(pageID)
	return pageID, nil
}

// Free 释放一个页面（Reference Counting 模式）
// 修改记录 (2026-04-01): 使用 Reference Counting 替代纯 Epoch 延迟回收
//
// Reference Counting 流程：
// 1. Free(pageID) 调用时：
//   - refCount > 0：标记 deleted=1，inQueue=1，进入 delayedFreeList（等待 refCount 归零）
//   - refCount == 0 且 deleted == 1：已在 delayedFreeList 中，不重复添加
//   - refCount == 0 且 deleted == 0：直接进入 freeList（无活跃引用，可立即回收）
//
// 2. Release(pageID) 调用时：
//   - refCount 减 1，如果变为 0 且 deleted==1 且 inQueue==0，进入 delayedFreeList
func (pm *PageManager) Free(pageID uint32) error {
	if pageID >= pm.total {
		return errpkg.OffHeapInvalidPageID(int(pageID), int(pm.total))
	}

	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)

	// Reference Counting: 检查 refCount
	currentRefCount := atomic.LoadInt32(&header.refCount)
	if currentRefCount > 0 {
		// 仍有活跃引用，标记为已删除但不立即回收
		// 页面会留在 delayedFreeList，直到 refCount 归零
		header.deleted = 1
		header.deleteEpoch = pm.currentEpoch.Load()
		// 使用原子操作设置 inQueue 标志，防止重复添加
		oldVal := atomic.SwapUint32(&header.inQueue, 1)
		if oldVal == 0 {
			// 页面不在队列中，添加进去
			pm.delayedFreeList.Enqueue(pageID)
		}
		pm.used.Add(^uint32(0))
		return nil
	}

	// refCount == 0，检查 deleted 标志
	// 如果 deleted == 1，说明页面之前已被标记为待删除（refCount 之前 > 0）
	// 需要进入 delayedFreeList 而不是 freeList
	if header.deleted == 1 {
		// 使用原子操作设置 inQueue 标志，防止重复添加
		oldVal := atomic.SwapUint32(&header.inQueue, 1)
		if oldVal == 0 {
			// 页面不在队列中，添加进去
			pm.delayedFreeList.Enqueue(pageID)
		}
		pm.used.Add(^uint32(0))
		return nil
	}

	// refCount == 0 且 deleted == 0，无活跃引用，直接加入 freeList
	// 重要：仍然设置 deleted=1，防止 SearchChild 在页面进入 freeList 后
	// 但尚未被 Alloc 回收前，误读到已释放页面的脏数据导致自环
	header.deleted = 1
	pm.freeList.Enqueue(pageID)
	pm.used.Add(^uint32(0))
	pm.tracker.RecordFree(pageID)
	return nil
}

// AdvanceDelayedFreeList 将延迟释放列表中的页面移到可用列表
// 这应该在 epoch 推进后调用，确保没有 goroutine 仍在访问这些页面
//
// 重要：此函数只负责根据 epoch 年龄移除"够老"的页面。
// 但页面必须同时满足 refCount == 0 才能移到 freeList。
// 如果 refCount > 0，页面保留在 delayedFreeList 中。
//
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
		epochDiff := currentEpoch - header.deleteEpoch

		// 检查 refCount：只有 refCount == 0 才能移到 freeList
		// 如果 refCount > 0，页面仍在被使用，保留在 delayedFreeList
		currentRefCount := atomic.LoadInt32(&header.refCount)
		if currentRefCount > 0 {
			// 页面仍在使用，重新加入 delayedFreeList，等待 refCount 归零
			pm.delayedFreeList.Enqueue(pageID)
			continue
		}

		// 如果页面太新，保留在 delayedFreeList 中，等待下次 Advance
		if epochDiff < minEpochDiff {
			// 页面留在 delayedFreeList，等待下次 Advance
			pm.delayedFreeList.Enqueue(pageID)
			break // 队列已排序，越老越前面，后面的不用检查了
		}

		// 页面够老且 refCount == 0，移到 freeList
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

// AddRef 增加引用计数
// 线程安全：使用原子操作
// 返回更新后的引用计数
func (pm *PageManager) AddRef(pageID uint32) int32 {
	if pageID == 0 || pageID >= pm.total {
		return 0
	}
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	return atomic.AddInt32(&header.refCount, 1)
}

// Release 减少引用计数
// 当 refCount 降至 0 时：
// - 如果 deleted == 0：页面仍在使用中，只需重置 refCount
// - 如果 deleted == 1 且 inQueue == 0：页面已被 Free 标记为待回收，需要进入 delayedFreeList
// - 如果 deleted == 1 且 inQueue == 1：页面已在 delayedFreeList 中，不需要重复添加
// 线程安全：使用原子操作
func (pm *PageManager) Release(pageID uint32) {
	if pageID == 0 || pageID >= pm.total {
		return
	}
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)

	newCount := atomic.AddInt32(&header.refCount, -1)
	if newCount == 0 {
		// refCount == 0，检查是否需要添加到 delayedFreeList
		if header.deleted == 1 {
			// 页面已被 Free 标记为待回收
			// 使用原子操作检查和设置 inQueue 标志
			oldVal := atomic.SwapUint32(&header.inQueue, 1)
			if oldVal == 0 {
				// 页面不在队列中，添加进去
				pm.delayedFreeList.Enqueue(pageID)
				pm.used.Add(^uint32(0))
			}
			// 如果 oldVal == 1，页面已在队列中，不需要重复添加
		}
		// 如果 deleted == 0，页面仍在正常使用中，refCount 归零不影响
	}
}

// TryAcquire 尝试获取页面引用
// 如果页面已在回收队列中（inQueue==1）或 refCount<0，返回 false
// 成功时 refCount++
// 线程安全：使用 CAS
//
// 注意：在 Reference Counting 模式下：
// - deleted == 1 表示页面被标记为待删除，但可能仍在使用中
// - inQueue == 1 表示页面已在回收队列中，不可获取
// - 只有 inQueue == 1 时才应该拒绝获取
func (pm *PageManager) TryAcquire(pageID uint32) bool {
	if pageID == 0 || pageID >= pm.total {
		return false
	}
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)

	for {
		// 检查是否已在回收队列中
		// inQueue == 1 表示页面已在 delayedFreeList 中，不应该被获取
		inQueueBits := atomic.LoadUint32(&header.inQueue)
		if inQueueBits == 1 {
			return false
		}

		oldCount := atomic.LoadInt32(&header.refCount)
		if oldCount < 0 {
			return false
		}

		if atomic.CompareAndSwapInt32(&header.refCount, oldCount, oldCount+1) {
			// CAS 成功后，再次检查 inQueue 标志
			// 防止在 CAS 成功后、返回前，页面被添加到回收队列
			inQueueBits = atomic.LoadUint32(&header.inQueue)
			if inQueueBits == 1 {
				// 页面在获取引用后被添加到回收队列，回退
				atomic.AddInt32(&header.refCount, -1)
				return false
			}
			return true
		}
		// CAS 失败，重试
	}
}

// GetRefCount 获取页面的当前引用计数（调试用）
// 线程安全：使用原子操作
func (pm *PageManager) GetRefCount(pageID uint32) int32 {
	if pageID == 0 || pageID >= pm.total {
		return 0
	}
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	return atomic.LoadInt32(&header.refCount)
}

// GetFreeListSize 返回 freeList 的大小（调试用）
func (pm *PageManager) GetFreeListSize() int {
	return pm.freeList.Size()
}

// GetDelayedFreeListSize 返回 delayedFreeList 的大小（调试用）
func (pm *PageManager) GetDelayedFreeListSize() int {
	return pm.delayedFreeList.Size()
}

// AdvanceEpoch 推进 epoch 并尝试释放延迟页面
// 这应该在 BTree 操作成功后调用
func (pm *PageManager) AdvanceEpoch() {
	pm.currentEpoch.Add(1)
	pm.AdvanceDelayedFreeList()
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

// IsInQueue 检查页面是否在回收队列中（调试用）
func (pm *PageManager) IsInQueue(pageID uint32) bool {
	if pageID == 0 || pageID >= pm.total {
		return false
	}
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	return header.inQueue == 1
}

// GetDeleteEpoch 获取页面的删除 epoch（调试用）
func (pm *PageManager) GetDeleteEpoch(pageID uint32) uint64 {
	if pageID == 0 || pageID >= pm.total {
		return 0
	}
	ptr := pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	return header.deleteEpoch
}
