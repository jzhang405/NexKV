// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"fmt"
	"unsafe"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
)

// OffHeapAdapter Off-Heap 页面适配器
// 负责在现有 BTree 架构和 Off-Heap 存储之间转换
type OffHeapAdapter struct {
	pm         *offheap.PageManager
	pa         *offheap.PageAccessor
	materializer *offheap.OffHeapMaterializer
	dataEndMap map[uint32]*uint16 // 页面 dataEnd 状态（用于跟踪数据区使用）
}

// NewOffHeapAdapter 创建 Off-Heap 适配器
func NewOffHeapAdapter(pm *offheap.PageManager) *OffHeapAdapter {
	return &OffHeapAdapter{
		pm:         pm,
		pa:         offheap.NewPageAccessor(pm),
		materializer: offheap.NewOffHeapMaterializer(pm),
		dataEndMap: make(map[uint32]*uint16),
	}
}

// AllocLeafPage 分配新的叶子页面
// 返回 pageID
func (a *OffHeapAdapter) AllocLeafPage() (model.PageID, error) {
	pageID, err := a.pm.Alloc()
	if err != nil {
		return 0, fmt.Errorf("alloc page: %w", err)
	}
	a.pa.InitLeafPage(pageID, 0)
	return model.PageID(pageID), nil
}

// AllocIndexPage 分配新的索引页面
// 返回 pageID
func (a *OffHeapAdapter) AllocIndexPage() (model.PageID, error) {
	pageID, err := a.pm.Alloc()
	if err != nil {
		return 0, fmt.Errorf("alloc page: %w", err)
	}
	a.pa.InitIndexPage(pageID, 0)
	return model.PageID(pageID), nil
}

// FreePage 释放页面
func (a *OffHeapAdapter) FreePage(pageID model.PageID) error {
	return a.pm.Free(uint32(pageID))
}

// GetFromOffHeap 从 Off-Heap 叶子页面获取 key 对应的 value
// 返回 (value, found, error)
func (a *OffHeapAdapter) GetFromOffHeap(pageID model.PageID, key []byte) ([]byte, bool, error) {
	idx, found := a.pa.SearchKey(uint32(pageID), key, true)
	if !found {
		return nil, false, nil
	}

	// 获取 LeafEntry offsets
	_, _, valOff, valLen := a.pa.GetLeafEntryOffset(uint32(pageID), idx)
	val := a.pa.GetValue(uint32(pageID), valOff, valLen)
	// 复制 value（因为指向 mmap 内存）
	result := make([]byte, len(val))
	copy(result, val)
	return result, true, nil
}

// InsertToOffHeap 向 Off-Heap 叶子页面插入 KV 对
// 返回 (pageID, splitRequired, error)
func (a *OffHeapAdapter) InsertToOffHeap(pageID model.PageID, key, value []byte) (model.PageID, bool, error) {
	// 获取或初始化 dataEnd
	dataEndPtr := a.dataEndMap[uint32(pageID)]
	if dataEndPtr == nil {
		var initialDataEnd uint16 = 0
		dataEndPtr = &initialDataEnd
		a.dataEndMap[uint32(pageID)] = dataEndPtr
	}

	// 检查是否需要分页
	isFull := a.checkPageFull(uint32(pageID), len(key), len(value), *dataEndPtr)
	if isFull {
		return pageID, true, nil
	}

	// 查找插入位置
	idx, found := a.pa.SearchKey(uint32(pageID), key, true)
	if found {
		// 更新现有 key（需要重新分配页面，因为 Off-Heap 不可变）
		// 清除旧页面的 dataEnd 状态
		delete(a.dataEndMap, uint32(pageID))
		newPageID, err := a.updateLeafEntry(pageID, idx, key, value)
		return newPageID, false, err
	}

	// 插入新 KV
	err := a.pa.InsertLeafEntry(uint32(pageID), idx, key, value, dataEndPtr)
	return pageID, false, err
}

// checkPageFull 检查页面是否已满
func (a *OffHeapAdapter) checkPageFull(pageID uint32, keyLen int, valLen int, dataEnd uint16) bool {
	count := a.pa.GetCount(pageID)
	headerSize := uint32(offheap.SizeofPageHeader)

	var entrySize uint32
	if a.pa.IsLeaf(pageID) {
		entrySize = uint32(offheap.SizeofLeafEntry)
	} else {
		entrySize = uint32(offheap.SizeofIndexEntry)
	}

	// 计算已使用空间：header + entries + 数据区
	usedSpace := headerSize + uint32(count)*entrySize + uint32(dataEnd)
	requiredSpace := entrySize + uint32(keyLen) + uint32(valLen)
	return usedSpace+requiredSpace > offheap.PageSize
}

// updateLeafEntry 更新叶子条目（需要重新分配页面）
func (a *OffHeapAdapter) updateLeafEntry(pageID model.PageID, idx int, key, value []byte) (model.PageID, error) {
	// 收集所有 KV 对
	count := a.pa.GetCount(uint32(pageID))
	keys := make([][]byte, 0, count)
	values := make([][]byte, 0, count)

	for i := 0; i < int(count); i++ {
		keyOff, keyLen, valOff, valLen := a.pa.GetLeafEntryOffset(uint32(pageID), i)
		k := a.pa.GetKey(uint32(pageID), keyOff, keyLen)
		v := a.pa.GetValue(uint32(pageID), valOff, valLen)
		if i == idx {
			// 更新这个 key
			k = key
			v = value
		}
		// 复制 KV
		kCopy := make([]byte, len(k))
		copy(kCopy, k)
		vCopy := make([]byte, len(v))
		copy(vCopy, v)
		keys = append(keys, kCopy)
		values = append(values, vCopy)
	}

	// 释放旧页面
	a.pm.Free(uint32(pageID))

	// 分配新页面
	newPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, fmt.Errorf("alloc new page: %w", err)
	}

	// 物化到新页面
	err = a.materializer.MaterializePageFromBytes(newPageID, keys, values)
	if err != nil {
		a.pm.Free(newPageID)
		return 0, fmt.Errorf("materialize page: %w", err)
	}

	return model.PageID(newPageID), nil
}

// MaterializeLeafPage 从 Go 堆 LeafPage 物化到 Off-Heap
// 返回 (newPageID, error)
func (a *OffHeapAdapter) MaterializeLeafPage(leaf *LeafPage) (model.PageID, error) {
	pageID, err := a.pm.Alloc()
	if err != nil {
		return 0, fmt.Errorf("alloc page: %w", err)
	}

	// 收集 keys 和 values（跳过 Delta，只物化基线数据）
	keys := make([][]byte, 0, len(leaf.keys))
	values := make([][]byte, 0, len(leaf.values))

	for i := range leaf.keys {
		// 获取实际值（考虑 Delta）
		val, ok := leaf.Get(leaf.keys[i])
		if ok {
			// 复制 key 和 value
			keyCopy := make([]byte, len(leaf.keys[i]))
			copy(keyCopy, leaf.keys[i])
			valCopy := make([]byte, len(val))
			copy(valCopy, val)
			keys = append(keys, keyCopy)
			values = append(values, valCopy)
		}
	}

	err = a.materializer.MaterializePageFromBytes(uint32(pageID), keys, values)
	if err != nil {
		a.pm.Free(uint32(pageID))
		return 0, fmt.Errorf("materialize page: %w", err)
	}

	return model.PageID(pageID), nil
}

// CloneOffHeapPage 克隆 Off-Heap 页面（用于 COW）
// 返回 (newPageID, error)
func (a *OffHeapAdapter) CloneOffHeapPage(pageID model.PageID, isLeaf bool) (model.PageID, error) {
	newPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, fmt.Errorf("alloc page: %w", err)
	}

	// 获取源页面和目标页面的指针
	srcPtr := a.pm.PageIDToPtr(uint32(pageID))
	dstPtr := a.pm.PageIDToPtr(newPageID)

	// 4KB 页面复制
	// 注意：这里 srcPtr/dstPtr 是 uintptr，指向 mmap 内存（不在 Go 堆上）
	// 转换后立即使用，没有中间 GC 点，所以是安全的
	// 参考：https://pkg.go.dev/unsafe#Pointer
	srcSlice := unsafe.Slice((*byte)(unsafe.Pointer(srcPtr)), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(unsafe.Pointer(dstPtr)), offheap.PageSize)
	copy(dstSlice, srcSlice)

	// 更新新页面的版本号
	newVersion := a.pa.GetVersion(newPageID) + 1
	a.pa.SetVersion(newPageID, newVersion)

	return model.PageID(newPageID), nil
}

// SplitOffHeapLeafPage 分割 Off-Heap 叶子页面
// 返回 (leftPageID, rightPageID, splitKey, error)
func (a *OffHeapAdapter) SplitOffHeapLeafPage(pageID model.PageID) (model.PageID, model.PageID, []byte, error) {
	// 获取当前页面的所有 keys
	count := a.pa.GetCount(uint32(pageID))

	// 收集所有 KV
	keys := make([][]byte, 0, count)
	values := make([][]byte, 0, count)
	for i := 0; i < int(count); i++ {
		keyOff, keyLen, valOff, valLen := a.pa.GetLeafEntryOffset(uint32(pageID), i)
		key := a.pa.GetKey(uint32(pageID), keyOff, keyLen)
		val := a.pa.GetValue(uint32(pageID), valOff, valLen)
		// 复制 KV
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		valCopy := make([]byte, len(val))
		copy(valCopy, val)
		keys = append(keys, keyCopy)
		values = append(values, valCopy)
	}

	// 分割点：中间位置
	mid := count / 2
	splitKey := make([]byte, len(keys[mid]))
	copy(splitKey, keys[mid])

	// 分配左右两个新页面
	leftPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, 0, nil, fmt.Errorf("alloc left page: %w", err)
	}
	rightPageID, err := a.pm.Alloc()
	if err != nil {
		a.pm.Free(leftPageID)
		return 0, 0, nil, fmt.Errorf("alloc right page: %w", err)
	}

	// 物化左半部分
	err = a.materializer.MaterializePageFromBytes(leftPageID, keys[:mid], values[:mid])
	if err != nil {
		a.pm.Free(leftPageID)
		a.pm.Free(rightPageID)
		return 0, 0, nil, fmt.Errorf("materialize left page: %w", err)
	}

	// 物化右半部分（包含 splitKey）
	err = a.materializer.MaterializePageFromBytes(rightPageID, keys[mid:], values[mid:])
	if err != nil {
		a.pm.Free(leftPageID)
		a.pm.Free(rightPageID)
		return 0, 0, nil, fmt.Errorf("materialize right page: %w", err)
	}

	// 设置链表指针
	a.pa.SetNextPage(leftPageID, rightPageID)
	a.pa.SetPrevPage(rightPageID, leftPageID)

	// 释放旧页面
	a.pm.Free(uint32(pageID))

	return model.PageID(leftPageID), model.PageID(rightPageID), splitKey, nil
}

// NumKeys 获取 Off-Heap 页面的 key 数量
func (a *OffHeapAdapter) NumKeys(pageID model.PageID) int {
	return int(a.pa.GetCount(uint32(pageID)))
}

// GetPageVersion 获取 Off-Heap 页面版本号
func (a *OffHeapAdapter) GetPageVersion(pageID model.PageID) uint64 {
	return a.pa.GetVersion(uint32(pageID))
}

// SetPageVersion 设置 Off-Heap 页面版本号
func (a *OffHeapAdapter) SetPageVersion(pageID model.PageID, version uint64) {
	a.pa.SetVersion(uint32(pageID), version)
}

// GetPrevNextPage 获取链表前驱后继页面
func (a *OffHeapAdapter) GetPrevNextPage(pageID model.PageID) (prev, next model.PageID) {
	prevPageID := a.pa.GetPrevPage(uint32(pageID))
	nextPageID := a.pa.GetNextPage(uint32(pageID))
	return model.PageID(prevPageID), model.PageID(nextPageID)
}

// SetPrevNextPage 设置链表前驱后继页面
func (a *OffHeapAdapter) SetPrevNextPage(pageID model.PageID, prev, next model.PageID) {
	a.pa.SetPrevPage(uint32(pageID), uint32(prev))
	a.pa.SetNextPage(uint32(pageID), uint32(next))
}

// GetChild 获取 Off-Heap 索引页面的子节点
func (a *OffHeapAdapter) GetChild(pageID model.PageID, index int) (model.PageID, error) {
	childID := a.pa.GetChild(uint32(pageID), index)
	return model.PageID(childID), nil
}

// SearchChild 在 Off-Heap 索引页面中搜索子节点
// 返回 (childPageID, found)
func (a *OffHeapAdapter) SearchChild(pageID model.PageID, key []byte) (model.PageID, bool) {
	idx, found := a.pa.SearchKey(uint32(pageID), key, false)
	childID := a.pa.GetChild(uint32(pageID), idx)
	return model.PageID(childID), found
}

// InsertIndexEntry 向 Off-Heap 索引页面插入条目
func (a *OffHeapAdapter) InsertIndexEntry(pageID model.PageID, index int, key []byte, child model.PageID) error {
	var dataEnd uint16 = 0
	return a.pa.InsertIndexEntry(uint32(pageID), index, key, uint32(child), &dataEnd)
}

// VerifyOffHeapPage 验证 Off-Heap 页面内容
// 返回 (isValid, error)
func (a *OffHeapAdapter) VerifyOffHeapPage(pageID model.PageID) (bool, error) {
	count := a.pa.GetCount(uint32(pageID))
	isLeaf := a.pa.IsLeaf(uint32(pageID))

	// 收集所有 keys 用于验证
	keys := make([][]byte, 0, int(count))
	if isLeaf {
		for i := 0; i < int(count); i++ {
			keyOff, keyLen, _, _ := a.pa.GetLeafEntryOffset(uint32(pageID), i)
			key := a.pa.GetKey(uint32(pageID), keyOff, keyLen)
			keyCopy := make([]byte, len(key))
			copy(keyCopy, key)
			keys = append(keys, keyCopy)
		}
	} else {
		for i := 0; i < int(count); i++ {
			keyOff, keyLen, _ := a.pa.GetIndexEntryOffset(uint32(pageID), i)
			key := a.pa.GetKey(uint32(pageID), keyOff, keyLen)
			keyCopy := make([]byte, len(key))
			copy(keyCopy, key)
			keys = append(keys, keyCopy)
		}
	}

	// 验证 keys 是否有序
	for i := 1; i < len(keys); i++ {
		if bytes.Compare(keys[i-1], keys[i]) >= 0 {
			return false, fmt.Errorf("keys not sorted: [%d] %v >= [%d] %v", i-1, keys[i-1], i, keys[i])
		}
	}

	return true, nil
}

// GetStats 获取 Off-Heap 统计信息
func (a *OffHeapAdapter) GetStats() offheap.Stats {
	return a.pm.GetStats()
}

// IsLeaf 检查页面是否为叶子节点
func (a *OffHeapAdapter) IsLeaf(pageID model.PageID) bool {
	return a.pa.IsLeaf(uint32(pageID))
}
