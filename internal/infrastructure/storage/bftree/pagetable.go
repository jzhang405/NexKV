// Package bftree 提供 Bf-Tree 的页面表实现
package bftree

import (
	"sync"
	"sync/atomic"
)

// PageEntry 页面表条目
//
// 存储页面的元数据和引用
type PageEntry struct {
	pageID   uint64         // 页面 ID
	pageType PageType       // 页面类型
	level    PageLevel      // 页面级别
	refCount int32          // 引用计数
	pinCount int32          // 固定计数（防止被驱逐）
	version  atomic.Uint64  // 版本号（原子操作，用于并发修改检测）
	loaded   bool           // 是否已加载到内存
}

// PageTable 页面表（管理所有页面实体）
//
// 功能：
// - 页面分配（Alloc）：分配新页面 ID
// - 页面释放（Free）：释放页面
// - 页面查找（Get）：查找页面
// - 引用管理（Ref/Unref）：引用计数
// - 并发安全：使用 RWMutex 保护
type PageTable struct {
	mu     sync.RWMutex
	pages  map[uint64]*PageEntry // 页面表：pageID → PageEntry
	nextID atomic.Uint64         // 下一个可用的页面 ID

	// 统计信息
	totalAllocated atomic.Int64 // 总分配页面数
	totalFreed     atomic.Int64 // 总释放页面数
	currentCount   atomic.Int64 // 当前页面数
}

// NewPageTable 创建新的页面表
//
// 返回：
//   - 初始化的 PageTable
func NewPageTable() *PageTable {
	return &PageTable{
		pages: make(map[uint64]*PageEntry),
	}
}

// Alloc 分配新页面
//
// 参数：
//   - pageType: 页面类型（Inner/Leaf）
//   - level: 页面级别
//
// 返回：
//   - pageID: 分配的页面 ID
//   - error: 错误
func (pt *PageTable) Alloc(pageType PageType, level PageLevel) (uint64, error) {
	// 分配新的页面 ID
	pageID := pt.nextID.Add(1)

	// 创建页面条目
	entry := &PageEntry{
		pageID:   pageID,
		pageType: pageType,
		level:    level,
		refCount: 1, // 初始引用计数为 1
		pinCount: 0,
		loaded:   true,
	}
	entry.version.Store(1) // 初始化版本号为 1

	pt.mu.Lock()
	defer pt.mu.Unlock()

	// 添加到页面表
	pt.pages[pageID] = entry

	// 更新统计
	pt.totalAllocated.Add(1)
	pt.currentCount.Add(1)

	return pageID, nil
}

// Free 释放页面
//
// 参数：
//   - pageID: 页面 ID
//
// 返回：
//   - error: 错误
func (pt *PageTable) Free(pageID uint64) error {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	entry, exists := pt.pages[pageID]
	if !exists {
		return ErrPageNotFound
	}

	// 检查引用计数
	if entry.refCount > 0 {
		return ErrPageStillReferenced
	}

	// 从页面表中删除
	delete(pt.pages, pageID)

	// 更新统计
	pt.totalFreed.Add(1)
	pt.currentCount.Add(-1)

	return nil
}

// Get 获取页面
//
// 参数：
//   - pageID: 页面 ID
//
// 返回：
//   - entry: 页面条目
//   - found: 是否找到
func (pt *PageTable) Get(pageID uint64) (*PageEntry, bool) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	entry, exists := pt.pages[pageID]
	return entry, exists
}

// Ref 增加页面引用
//
// 参数：
//   - pageID: 页面 ID
//
// 返回：
//   - error: 错误
func (pt *PageTable) Ref(pageID uint64) error {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	entry, exists := pt.pages[pageID]
	if !exists {
		return ErrPageNotFound
	}

	atomic.AddInt32(&entry.refCount, 1)
	return nil
}

// Unref 减少页面引用
//
// 参数：
//   - pageID: 页面 ID
//
// 返回：
//   - error: 错误
func (pt *PageTable) Unref(pageID uint64) error {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	entry, exists := pt.pages[pageID]
	if !exists {
		return ErrPageNotFound
	}

	newCount := atomic.AddInt32(&entry.refCount, -1)
	if newCount < 0 {
		// 引用计数不能为负，重置为 0
		atomic.StoreInt32(&entry.refCount, 0)
	}

	return nil
}

// Pin 固定页面（防止被驱逐）
//
// 参数：
//   - pageID: 页面 ID
func (pt *PageTable) Pin(pageID uint64) error {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	entry, exists := pt.pages[pageID]
	if !exists {
		return ErrPageNotFound
	}

	atomic.AddInt32(&entry.pinCount, 1)
	return nil
}

// Unpin 解除固定
//
// 参数：
//   - pageID: 页面 ID
func (pt *PageTable) Unpin(pageID uint64) error {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	entry, exists := pt.pages[pageID]
	if !exists {
		return ErrPageNotFound
	}

	newCount := atomic.AddInt32(&entry.pinCount, -1)
	if newCount < 0 {
		// PinCount 不能为负，重置为 0
		atomic.StoreInt32(&entry.pinCount, 0)
	}

	return nil
}

// GetRefCount 获取页面引用计数
func (pt *PageTable) GetRefCount(pageID uint64) (int32, bool) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	entry, exists := pt.pages[pageID]
	if !exists {
		return 0, false
	}

	return atomic.LoadInt32(&entry.refCount), true
}

// GetPinCount 获取页面固定计数
func (pt *PageTable) GetPinCount(pageID uint64) (int32, bool) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	entry, exists := pt.pages[pageID]
	if !exists {
		return 0, false
	}

	return atomic.LoadInt32(&entry.pinCount), true
}

// ListPages 列出所有页面 ID
//
// 返回：
//   - pageIDs: 页面 ID 列表
func (pt *PageTable) ListPages() []uint64 {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	pageIDs := make([]uint64, 0, len(pt.pages))
	for pageID := range pt.pages {
		pageIDs = append(pageIDs, pageID)
	}

	return pageIDs
}

// GetStats 获取页面表统计信息
func (pt *PageTable) GetStats() PageTableStats {
	return PageTableStats{
		TotalAllocated: pt.totalAllocated.Load(),
		TotalFreed:     pt.totalFreed.Load(),
		CurrentCount:   pt.currentCount.Load(),
	}
}

// PageTableStats 页面表统计信息
type PageTableStats struct {
	TotalAllocated int64 // 总分配页面数
	TotalFreed     int64 // 总释放页面数
	CurrentCount   int64 // 当前页面数
}
