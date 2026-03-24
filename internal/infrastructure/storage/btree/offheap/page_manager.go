// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"fmt"
	"sync"
	"sync/atomic"
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
	allocator OffHeapAllocator // 跨平台内存分配器
	base      uintptr            // mmap 起始地址
	total     uint32             // 总页数
	used      atomic.Uint32      // 已使用页数
	freeList  *LockFreeQueue     // 空闲 PageID 队列（lock-free）
	initOnce  sync.Once          // 确保初始化一次
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
	// 溢出检查：确保 mmap 大小不超过 32 位 PageID 限制
	maxPages := mmapSize / PageSize
	if maxPages > int(MaxPageID) {
		return nil, fmt.Errorf("mmap size %d exceeds 32-bit PageID limit (%d pages)",
			mmapSize, MaxPageID)
	}

	// 创建内存分配器
	allocator, err := NewAllocator(mmapSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create allocator: %w", err)
	}

	// 获取基地址
	base, err := allocator.Alloc(mmapSize)
	if err != nil {
		allocator.Free(base, mmapSize)
		return nil, fmt.Errorf("failed to allocate memory: %w", err)
	}

	pm := &PageManager{
		allocator: allocator,
		base:      base,
		total:     uint32(maxPages),
		freeList:  NewLockFreeQueue(),
	}

	// 预分配所有 PageID 到 freeList
	for pageID := uint32(0); pageID < pm.total; pageID++ {
		pm.freeList.Enqueue(pageID)
	}

	return pm, nil
}

// Alloc 分配一个页面
// 返回 PageID，如果内存不足则返回错误
func (pm *PageManager) Alloc() (uint32, error) {
	pageID, ok := pm.freeList.Dequeue()
	if !ok {
		return 0, fmt.Errorf("out of memory: no free pages available (total: %d, used: %d)",
			pm.total, pm.used.Load())
	}
	pm.used.Add(1)
	return pageID, nil
}

// Free 释放一个页面
func (pm *PageManager) Free(pageID uint32) error {
	if pageID >= pm.total {
		return fmt.Errorf("invalid pageID %d (total: %d)", pageID, pm.total)
	}
	pm.freeList.Enqueue(pageID)
	pm.used.Add(^uint32(0)) // decrement
	return nil
}

// PageIDToPtr 将 PageID 转换为内存地址
func (pm *PageManager) PageIDToPtr(pageID uint32) uintptr {
	if pageID >= pm.total {
		panic(fmt.Sprintf("pageID %d out of range (total: %d)", pageID, pm.total))
	}
	offset := uintptr(pageID) * PageSize
	return pm.base + offset
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
