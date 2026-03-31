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
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// OffHeapAdapter Off-Heap 页面适配器
// 负责在现有 BTree 架构和 Off-Heap 存储之间转换
type OffHeapAdapter struct {
	pm           *offheap.PageManager
	pa           *offheap.PageAccessor
	materializer *offheap.OffHeapMaterializer
	epochFree    func(model.PageID) // epoch 释放回调（COW 旧页面走 epoch 机制，由 BTree 层注入）
}

// NewOffHeapAdapter 创建 Off-Heap 适配器
func NewOffHeapAdapter(pm *offheap.PageManager) *OffHeapAdapter {
	return &OffHeapAdapter{
		pm:           pm,
		pa:           offheap.NewPageAccessor(pm),
		materializer: offheap.NewOffHeapMaterializer(pm),
	}
}

// SetEpochFree 设置 epoch 释放回调
// COW 路径的旧页面通过此回调走 epoch 机制，避免 use-after-free
func (a *OffHeapAdapter) SetEpochFree(fn func(model.PageID)) {
	a.epochFree = fn
}

// freeOldPage 释放 COW 旧页面（走 epoch 机制或直接释放）
func (a *OffHeapAdapter) freeOldPage(pageID uint32) {
	if a.epochFree != nil {
		a.epochFree(model.PageID(pageID))
	} else {
		a.pm.Free(pageID)
	}
}

// AllocLeafPage 分配新的叶子页面
// 返回 pageID
func (a *OffHeapAdapter) AllocLeafPage() (model.PageID, error) {
	pageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocPageAdapter(err)
	}
	a.pa.InitLeafPage(pageID, 0)
	return model.PageID(pageID), nil
}

// AllocIndexPage 分配新的索引页面
// 返回 pageID
func (a *OffHeapAdapter) AllocIndexPage() (model.PageID, error) {
	pageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocPageAdapter(err)
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
	idx, found, err := a.pa.SearchKey(uint32(pageID), key, true)
	if err != nil {
		return nil, false, err
	}

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
	// 使用线性搜索替代二分查找
	// SearchKey 依赖 keys 有序的假设，但页面可能已无序
	// 使用线性搜索确保即使 keys 无序也能找到正确位置
	idx, found := a.linearSearchLeaf(uint32(pageID), key)

	if found {
		// 更新现有 key（需要重新分配页面，因为 Off-Heap 不可变）
		newPageID, err := a.UpdateLeafEntry(pageID, idx, key, value)
		return newPageID, false, err
	}

	// 插入新 KV 之前，预估是否需要分裂
	// checkPageFull 现在直接从页面读取 dataEnd，无需缓存
	if a.checkPageFull(uint32(pageID), len(key), len(value)) {
		// 页面可能已满，返回 splitRequired=true
		return pageID, true, nil
	}

	// 插入新 KV
	// 重要：需要从页面读取当前的 dataEnd，因为 InsertLeafEntry 使用它来分配空间
	dataEnd := a.pa.GetDataEnd(uint32(pageID))
	insertErr := a.pa.InsertLeafEntry(uint32(pageID), idx, key, value, &dataEnd)

	if insertErr == nil {
		// 插入成功，检查是否需要分裂
		splitRequired := a.checkPageFull(uint32(pageID), len(key), len(value))
		return pageID, splitRequired, nil
	}

	return pageID, false, insertErr
}

// linearSearchLeaf 线性搜索叶子页面，找到正确的插入位置
// 方案 C：不依赖 keys 有序的假设，确保即使页面已损坏也能正确插入
// 返回：(插入索引, 是否找到)
func (a *OffHeapAdapter) linearSearchLeaf(pageID uint32, key []byte) (int, bool) {
	count := a.pa.GetCount(pageID)

	// 遍历所有现有 keys
	for i := 0; i < int(count); i++ {
		keyOff, keyLen, _, _ := a.pa.GetLeafEntryOffset(pageID, i)
		existingKey := a.pa.GetKey(pageID, keyOff, keyLen)
		cmp := bytes.Compare(key, existingKey)
		if cmp == 0 {
			// 找到相同的 key
			return i, true
		} else if cmp < 0 {
			// 新 key 应该插入到当前位置之前
			return i, false
		}
	}

	// 所有现有 keys 都小于新 key，插入到末尾
	return int(count), false
}

// checkPageFull 检查页面是否已满（直接从页面读取 dataEnd）
func (a *OffHeapAdapter) checkPageFull(pageID uint32, keyLen int, valLen int) bool {
	count := a.pa.GetCount(pageID)
	headerSize := uint32(offheap.SizeofPageHeader)

	var entrySize uint32
	isLeaf := a.pa.IsLeaf(pageID)
	if isLeaf {
		entrySize = uint32(offheap.SizeofLeafEntry)
	} else {
		entrySize = uint32(offheap.SizeofIndexEntry)
	}

	// 直接从页面读取实际的 dataEnd（不再使用缓存）
	dataEnd := a.pa.GetDataEnd(pageID)

	// 计算已使用空间：header + entries + 数据区
	usedSpace := headerSize + uint32(count)*entrySize + uint32(dataEnd)
	requiredSpace := entrySize + uint32(keyLen) + uint32(valLen)

	// 核心分裂判断：基于实际空间使用
	// 页面大小 = header + entries + data
	// 分裂条件：当前使用 + 新增所需 > 页面大小
	return usedSpace+requiredSpace > offheap.PageSize
}

// updateLeafEntryFullMaterialization 完整物化路径（fallback）
// 用于新 value 长度 > 旧 value 长度的场景，或 BulkInit COW 失败时的降级路径
func (a *OffHeapAdapter) updateLeafEntryFullMaterialization(pageID model.PageID, idx int, key, value []byte) (model.PageID, error) {

	// 收集所有 KV 对
	count := a.pa.GetCount(uint32(pageID))

	// 注意：移除了条目数限制，让实际的物化操作决定是否可以更新
	// 如果页面太大，MaterializePageFromBytes 会返回错误

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

	// 释放旧页面（走 epoch 机制，避免并发读访问已回收页面）
	a.freeOldPage(uint32(pageID))

	// 分配新页面
	newPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocNewPageForSplit(err)
	}

	// 物化到新页面
	_, err = a.materializer.MaterializePageFromBytes(newPageID, keys, values)
	if err != nil {
		a.pm.Free(newPageID)
		return 0, errpkg.BTreeMaterializePageAdapter(err)
	}

	return model.PageID(newPageID), nil
}

// updateLeafEntryBulkCOW BulkInit COW 路径（零 Go 堆分配）
//
// 核心优化：使用 BulkInitLeafFromSource 一次性拷贝所有条目，然后用 OverwriteLeafValue 覆盖目标 entry。
// 前置条件: len(value) <= entry.valLen（由调用者 UpdateLeafEntry 检查）
//
// 流程:
//  1. Alloc 新页面
//  2. BulkInitLeafFromSource 一次性拷贝所有条目（零堆拷贝）
//  3. OverwriteLeafValue 覆盖目标 entry（O(1)）
//  4. Free 旧页面
func (a *OffHeapAdapter) updateLeafEntryBulkCOW(
	pageID model.PageID, idx int, value []byte,
) (model.PageID, error) {
	srcPageID := uint32(pageID)
	count := int(a.pa.GetCount(srcPageID))

	// 1. 分配新页面
	newRawPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocNewPageForSplit(err)
	}

	// 关键安全检查：如果 Alloc 返回的页面恰好是源页面（页面被回收重用），
	// BulkInitLeafFromSource 会先 InitLeafPage 清空目标页面（即源页面自身），
	// 导致后续读取 count=0 而 panic。
	// 降级到 fullMaterialization 路径（先将数据读到堆上再释放源页面）。
	if newRawPageID == srcPageID {
		a.pm.Free(newRawPageID)
		return a.updateLeafEntryFullMaterialization(pageID, idx, nil, value)
	}

	// 2. BulkInitLeafFromSource 一次性拷贝所有条目（零堆拷贝）
	_, err = a.pa.BulkInitLeafFromSource(srcPageID, newRawPageID, 0, count)
	if err != nil {
		a.pm.Free(newRawPageID)
		return 0, err
	}

	// 3. OverwriteLeafValue 覆盖目标 entry（O(1)）
	if !a.pa.OverwriteLeafValue(newRawPageID, idx, value) {
		a.pm.Free(newRawPageID)
		return 0, fmt.Errorf("overwrite leaf value failed: valLen insufficient")
	}

	// 4. 释放旧页面（走 epoch 机制，避免并发读访问已回收页面）
	a.freeOldPage(srcPageID)

	return model.PageID(newRawPageID), nil
}

// UpdateLeafEntry 更新叶子条目（COW 路径选择）
// 优先使用 BulkInit COW（零堆分配），valLen 不足时降级到完整物化
func (a *OffHeapAdapter) UpdateLeafEntry(
	pageID model.PageID, idx int, key, value []byte,
) (model.PageID, error) {
	// 检查 valLen
	_, _, _, valLen := a.pa.GetLeafEntryOffset(uint32(pageID), idx)

	if uint32(len(value)) <= valLen {
		return a.updateLeafEntryBulkCOW(pageID, idx, value)
	}
	return a.updateLeafEntryFullMaterialization(pageID, idx, key, value)
}

// UpdateIndexEntry 更新索引条目（需要重新分配页面）
// 当子页面分裂时，替换旧子页面为新的左右子页面
//
// 参数：
//
//	pageID - 父页面 ID
//	index - 插入位置
//	key - split key（要插入的新键）
//	leftPageID - 左子页面（替换旧的子页面）
//	rightPageID - 右子页面（新的子页面）
//
// 返回：新的父页面 ID
func (a *OffHeapAdapter) UpdateIndexEntry(pageID model.PageID, index int, key []byte, leftPageID, rightPageID uint32) (model.PageID, error) {
	// 防御性检查：rightPageID 必须非零（分裂操作需要两个子节点）
	if rightPageID == 0 {
		return 0, errpkg.BTreeUpdateIndexEntryRightZero()
	}
	if leftPageID == 0 {
		return 0, errpkg.BTreeUpdateIndexEntryLeftZero()
	}

	count := a.pa.GetCount(uint32(pageID))

	// 检查父节点是否已满
	if int(count) >= maxInternalKeys {
		return 0, errpkg.BTreeParentFull(int(count), maxInternalKeys)
	}

	keys := make([][]byte, 0, count+1)
	children := make([]uint32, 0, count+2)

	inserted := false
	for i := 0; i < int(count); i++ {
		keyOff, keyLen, _ := a.pa.GetIndexEntryOffset(uint32(pageID), i)
		k := a.pa.GetKey(uint32(pageID), keyOff, keyLen)
		// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
		encodedChild := a.pa.GetChild(uint32(pageID), i)
		child, _ := a.DecodeChildWithVersion(encodedChild)

		if i == index {
			// 分裂位置：插入 splitKey 和 left/right child
			// 修复：必须复制原来的 key[index]，否则会导致 key 丢失
			keys = append(keys, key) // splitKey
			kCopy := make([]byte, len(k))
			copy(kCopy, k)
			keys = append(keys, kCopy) // 复制 key[index]
			children = append(children, leftPageID)
			children = append(children, rightPageID)
			inserted = true
		} else {
			// 非分裂位置：保留原 key 和 child
			kCopy := make([]byte, len(k))
			copy(kCopy, k)
			keys = append(keys, kCopy)
			children = append(children, child)
		}
	}

	// 如果 index == count（在最后插入），循环中没有插入
	if !inserted {
		keys = append(keys, key)
		children = append(children, leftPageID)
		children = append(children, rightPageID)
	}

	// 添加 extraChild（N+1 child）
	// B+Tree 语义：splitKey 和 rightChild 插入后，extraChild 位置发生变化
	//
	// 如果 index < count（中间插入）：
	//   原结构: [k0, ..., k(index-1), k(index), ..., k(count-1)] | [c0, ..., c(index), ..., c(count)]  (c(count) 是 extraChild)
	//   新结构: [k0, ..., k(index-1), splitKey, k(index), ..., k(count-1)] | [c0, ..., c(index-1), left, right, ..., c(count)]
	//   extraChild 保持不变 (c(count))
	//
	// 如果 index == count（末尾插入）：
	//   原结构: [k0, ..., k(count-1)] | [c0, ..., c(count)]  (c(count) 是 extraChild)
	//   新结构: [k0, ..., k(count-1), splitKey] | [c0, ..., c(count-1), left]
	//   rightPageID 成为新的 extraChild
	//
	// 注意：当 index == count 时，上面的循环已经添加了 leftPageID，
	//       但没有添加 extraChild，所以这里需要添加 rightPageID
	if index < int(count) {
		// 中间插入：保留原 extraChild
		// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
		encodedExtraChild := a.pa.GetChild(uint32(pageID), int(count))
		extraChild, _ := a.DecodeChildWithVersion(encodedExtraChild)
		children = append(children, extraChild)
	}
	// index == count 的情况：rightPageID 已经作为 extraChild 在上面被添加了
	// (line 328: children = append(children, leftPageID))
	// (line 329: children = append(children, rightPageID))
	// 所以这里不需要额外处理

	// 注意：不在此时释放旧页面 pageID
	// 调用者 (handleSplitOffHeapSync) 会延迟释放
	// 这样可以避免在 CAS 之前释放页面，导致页面被重新分配

	newPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocNewPageForSplit(err)
	}

	_, err = a.materializer.MaterializeIndexPageFromBytes(uint32(newPageID), keys, children)
	if err != nil {
		a.pm.Free(newPageID)
		return 0, errpkg.BTreeMaterializePageForSplit(err)
	}

	return model.PageID(newPageID), nil
}

// FindChildIndex 查找父页面中指定 child 的索引位置
//
// 参数：
//
//	parentPageID - 父页面 ID
//	childPageID - 要查找的子页面 ID
//
// 返回：child 的索引位置（0 到 count），如果未找到返回 -1
// 注意：会遍历所有 child（包括 extraChild），因为 key 索引和 child 索引不同
func (a *OffHeapAdapter) FindChildIndex(parentPageID uint32, childPageID uint32) int {
	count := a.pa.GetCount(parentPageID)

	// 遍历所有 child（包括 extraChild）
	for i := 0; i <= int(count); i++ {
		encodedChild := a.pa.GetChild(parentPageID, i)
		child, _ := a.DecodeChildWithVersion(encodedChild)
		if child == childPageID {
			return i
		}
	}

	return -1 // 未找到
}

// ReplaceChild 替换索引节点中的单个子节点（不增加子节点数量）
// 用于 fallback 场景：将旧子节点替换为新子节点，但不分裂
//
// 参数：
//
//	pageID - 父页面 ID
//	index - 要替换的子节点位置（可以是 0 到 count，其中 count 表示 extraChild）
//	newChildID - 新的子节点 ID
//
// 返回：新的父页面 ID
func (a *OffHeapAdapter) ReplaceChild(pageID model.PageID, index int, newChildID uint32) (model.PageID, error) {
	pid := uint32(pageID)

	// TOCTOU 防御 Layer 2a: 页面类型检查
	// 父页面被 epoch 回收重用为叶子页时，pageType 变为 PageTypeLeaf
	if a.pa.IsLeaf(pid) {
		return 0, errpkg.ErrBTreeParentPageRecycled
	}

	count := a.pa.GetCount(pid)

	// TOCTOU 防御 Layer 2b: count 合理性检查
	// 被回收重用的页面 count=0（InitPage 重置为 0）
	// maxInternalKeys=180 (constants.go)
	if count == 0 || count > maxInternalKeys {
		return 0, errpkg.ErrBTreeInvalidParentState
	}

	// 验证索引有效（index 可以是 0 到 count，其中 count 表示 extraChild）
	if index < 0 || index > int(count) {
		return 0, errpkg.BTreeInvalidChildIndexAt(index, int(count))
	}

	// 复制所有 keys 和 children，只替换指定位置的 child
	keys := make([][]byte, 0, count)
	children := make([]uint32, 0, count+1)

	for i := 0; i < int(count); i++ {
		keyOff, keyLen, _ := a.pa.GetIndexEntryOffset(pid, i)
		k := a.pa.GetKey(pid, keyOff, keyLen)

		// 复制 key
		kCopy := make([]byte, len(k))
		copy(kCopy, k)
		keys = append(keys, kCopy)

		// 替换或复制 child（不包括 extraChild）
		// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
		if i == index {
			children = append(children, newChildID)
		} else {
			encodedChild := a.pa.GetChild(pid, i)
			child, _ := a.DecodeChildWithVersion(encodedChild)
			children = append(children, child)
		}
	}

	// 添加 extraChild（N+1 child）
	// 如果 index == count，替换 extraChild；否则复制原 extraChild
	// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
	encodedExtraChild := a.pa.GetChild(pid, int(count))
	if index == int(count) {
		children = append(children, newChildID)
	} else {
		extraChild, _ := a.DecodeChildWithVersion(encodedExtraChild)
		children = append(children, extraChild)
	}

	// 分配新页面
	newPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocNewPageForSplit(err)
	}

	// 物化新页面
	_, err = a.materializer.MaterializeIndexPageFromBytes(uint32(newPageID), keys, children)
	if err != nil {
		a.pm.Free(newPageID)
		return 0, errpkg.BTreeMaterializePageForSplit(err)
	}

	return model.PageID(newPageID), nil
}

// MaterializeLeafPage 从 Go 堆 LeafPage 物化到 Off-Heap
// 返回 (newPageID, error)
func (a *OffHeapAdapter) MaterializeLeafPage(leaf *LeafPage) (model.PageID, error) {
	pageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocPageAdapter(err)
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

	_, err = a.materializer.MaterializePageFromBytes(uint32(pageID), keys, values)
	if err != nil {
		a.pm.Free(uint32(pageID))
		return 0, errpkg.BTreeMaterializePageAdapter(err)
	}

	return model.PageID(pageID), nil
}

// CloneOffHeapPage 克隆 Off-Heap 页面（用于 COW）
// 返回 (newPageID, error)
func (a *OffHeapAdapter) CloneOffHeapPage(pageID model.PageID, isLeaf bool) (model.PageID, error) {
	newPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocPageAdapter(err)
	}

	// 获取源页面和目标页面的指针
	srcPtr := a.pm.PageIDToPtr(uint32(pageID))
	dstPtr := a.pm.PageIDToPtr(newPageID)

	// 4KB 页面复制
	// srcPtr/dstPtr 是 unsafe.Pointer，指向 mmap 内存（不在 Go 堆上）
	srcSlice := unsafe.Slice((*byte)(srcPtr), offheap.PageSize)
	dstSlice := unsafe.Slice((*byte)(dstPtr), offheap.PageSize)
	copy(dstSlice, srcSlice)

	// 更新新页面的版本号
	newVersion := a.pa.GetVersion(newPageID) + 1
	a.pa.SetVersion(newPageID, newVersion)

	return model.PageID(newPageID), nil
}

// SplitOffHeapLeafPage 分割 Off-Heap 叶子页面
// 返回 (leftPageID, rightPageID, splitKey, error)
//
// 优化策略：优先使用零拷贝路径（BulkInitLeafFromSource），
// 消除 Go 堆中转分配。失败时回退到原有 Go 堆路径。
func (a *OffHeapAdapter) SplitOffHeapLeafPage(pageID model.PageID) (model.PageID, model.PageID, []byte, error) {
	count := a.pa.GetCount(uint32(pageID))
	countInt := int(count)

	if countInt < 2 {
		return 0, 0, nil, errpkg.BTreeSplitMinKeys(countInt)
	}

	// 分配左右两个新页面
	leftPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, 0, nil, errpkg.BTreeAllocLeftPage(err)
	}
	rightPageID, err := a.pm.Alloc()
	if err != nil {
		a.pm.Free(leftPageID)
		return 0, 0, nil, errpkg.BTreeAllocRightPage(err)
	}

	srcRawID := uint32(pageID)
	if leftPageID == rightPageID || leftPageID == srcRawID || rightPageID == srcRawID {
		a.pm.Free(leftPageID)
		a.pm.Free(rightPageID)
		// 源页面被回收重用，降级到 Go 堆路径（先将数据拷贝到堆上再释放源页面）
		return a.splitOffHeapLeafPageFallback(pageID)
	}
	if leftPageID == 0 || rightPageID == 0 {
		a.pm.Free(leftPageID)
		a.pm.Free(rightPageID)
		return 0, 0, nil, errpkg.BTreeInvalidPageIDAlloc(leftPageID, rightPageID)
	}

	var success bool

	// 搜索策略：恢复原始 11 个比例（三次审核 P1）
	// 1. 30/70
	mid := int(float64(countInt) * 0.3)
	if mid > 0 {
		_, leftErr := a.pa.BulkInitLeafFromSource(uint32(pageID), leftPageID, 0, mid)
		_, rightErr := a.pa.BulkInitLeafFromSource(uint32(pageID), rightPageID, mid, countInt)
		if leftErr == nil && rightErr == nil {
			success = true
		}
	}

	// 2. 渐进式比例：2/3, 3/4, ..., 9/10
	if !success && countInt > 10 {
		for divisor := 3; divisor <= 10; divisor++ {
			mid = countInt * (divisor - 1) / divisor
			if mid <= 1 || mid >= countInt-1 {
				continue
			}
			_, leftErr := a.pa.BulkInitLeafFromSource(uint32(pageID), leftPageID, 0, mid)
			_, rightErr := a.pa.BulkInitLeafFromSource(uint32(pageID), rightPageID, mid, countInt)
			if leftErr == nil && rightErr == nil {
				success = true
				break
			}
		}
	}

	// 3. 极端比例：splitIdx=1, splitIdx=0
	if !success {
		for _, tryMid := range []int{1, 0} {
			mid = tryMid
			_, leftErr := a.pa.BulkInitLeafFromSource(uint32(pageID), leftPageID, 0, mid)
			_, rightErr := a.pa.BulkInitLeafFromSource(uint32(pageID), rightPageID, mid, countInt)
			if leftErr == nil && rightErr == nil {
				success = true
				break
			}
		}
	}

	if !success {
		// 零拷贝路径全部失败，回退到 Go 堆路径
		a.pm.Free(leftPageID)
		a.pm.Free(rightPageID)
		return a.splitOffHeapLeafPageFallback(pageID)
	}

	// 获取 splitKey（从右页面第一个条目）
	keyOff, keyLen, _, _ := a.pa.GetLeafEntryOffset(rightPageID, 0)
	splitKey := a.pa.GetKey(rightPageID, keyOff, keyLen)
	splitKeyCopy := make([]byte, len(splitKey))
	copy(splitKeyCopy, splitKey)

	// 设置链表指针
	// 注意：调用者负责在 CAS 成功后更新相邻页面的反向指针
	oldPrevPage := a.pa.GetPrevPage(uint32(pageID))
	oldNextPage := a.pa.GetNextPage(uint32(pageID))

	a.pa.SetNextPage(leftPageID, rightPageID)
	a.pa.SetPrevPage(rightPageID, leftPageID)

	if oldPrevPage != 0xFFFFFFFF {
		a.pa.SetPrevPage(leftPageID, oldPrevPage)
	}
	if oldNextPage != 0xFFFFFFFF {
		a.pa.SetNextPage(rightPageID, oldNextPage)
	}

	return model.PageID(leftPageID), model.PageID(rightPageID), splitKeyCopy, nil
}

// splitOffHeapLeafPageFallback Go 堆路径分裂（极端 KV 大小不均时使用）
func (a *OffHeapAdapter) splitOffHeapLeafPageFallback(pageID model.PageID) (model.PageID, model.PageID, []byte, error) {
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

	// 分配左右两个新页面（提前分配，避免重复分配）
	leftPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, 0, nil, errpkg.BTreeAllocLeftPage(err)
	}
	rightPageID, err := a.pm.Alloc()
	if err != nil {
		a.pm.Free(leftPageID)
		return 0, 0, nil, errpkg.BTreeAllocRightPage(err)
	}

	// 调试：验证分配的页面ID不同
	if leftPageID == rightPageID {
		return 0, 0, nil, errpkg.BTreeDuplicatePageIDAlloc(leftPageID)
	}
	if leftPageID == 0 || rightPageID == 0 {
		return 0, 0, nil, errpkg.BTreeInvalidPageIDAlloc(leftPageID, rightPageID)
	}

	// 智能分裂搜索：找到能使两侧都成功物化的分裂点
	// 策略：渐进式尝试不同比例
	var splitIdx int
	var success bool
	countInt := int(count)

	// 边界检查：至少需要 2 个 key 才能分裂
	if countInt < 2 {
		return 0, 0, nil, errpkg.BTreeSplitMinKeys(countInt)
	}

	// 首先尝试 30/70 分裂（非常激进，确保右页面不会过大）
	mid := int(float64(countInt) * 0.3) // 30%
	if mid > 0 {
		leftKeys := keys[:mid]
		leftValues := values[:mid]
		rightKeys := keys[mid:]
		rightValues := values[mid:]

		_, leftErr := a.materializer.MaterializePageFromBytes(leftPageID, leftKeys, leftValues)
		_, rightErr := a.materializer.MaterializePageFromBytes(rightPageID, rightKeys, rightValues)

		if leftErr == nil && rightErr == nil {
			splitIdx = mid
			success = true
		}
	}

	// 如果 50/50 失败，尝试更极端的比例（从 2/3 开始向 9/10 推进）
	if !success && countInt > 10 {
		for divisor := 3; divisor <= 10; divisor++ {
			mid := countInt * (divisor - 1) / divisor
			if mid <= 1 || mid >= countInt-1 {
				continue
			}

			leftKeys := keys[:mid]
			leftValues := values[:mid]
			rightKeys := keys[mid:]
			rightValues := values[mid:]

			_, leftErr := a.materializer.MaterializePageFromBytes(leftPageID, leftKeys, leftValues)
			_, rightErr := a.materializer.MaterializePageFromBytes(rightPageID, rightKeys, rightValues)

			if leftErr == nil && rightErr == nil {
				splitIdx = mid
				success = true
				break
			}
		}
	}

	if !success {
		for _, trySplitIdx := range []int{1, 0} {
			newLeftPageID, err := a.pm.Alloc()
			if err != nil {
				continue
			}
			newRightPageID, err := a.pm.Alloc()
			if err != nil {
				a.pm.Free(newLeftPageID)
				continue
			}

			leftKeys := keys[:trySplitIdx]
			leftValues := values[:trySplitIdx]
			rightKeys := keys[trySplitIdx:]
			rightValues := values[trySplitIdx:]

			_, leftErr := a.materializer.MaterializePageFromBytes(newLeftPageID, leftKeys, leftValues)
			_, rightErr := a.materializer.MaterializePageFromBytes(newRightPageID, rightKeys, rightValues)

			if leftErr == nil && rightErr == nil {
				splitIdx = trySplitIdx
				success = true
				leftPageID = newLeftPageID
				rightPageID = newRightPageID
				break
			}

			a.pm.Free(newLeftPageID)
			a.pm.Free(newRightPageID)
		}

		if !success {
			return 0, 0, nil, errpkg.BTreePageTooLargeToSplit(countInt)
		}
	}

	// 使用找到的分裂点
	// splitKey 是右半部分的第一个 key，需要从 keys[splitIdx] 复制
	// 边界检查：splitIdx 必须在 [0, len(keys)-1] 范围内
	if splitIdx < 0 || splitIdx >= len(keys) {
		return 0, 0, nil, errpkg.BTreeInvalidSplitIdx(splitIdx, len(keys))
	}
	splitKey := make([]byte, len(keys[splitIdx]))
	copy(splitKey, keys[splitIdx])

	// 物化左半部分
	_, err = a.materializer.MaterializePageFromBytes(leftPageID, keys[:splitIdx], values[:splitIdx])
	if err != nil {
		a.pm.Free(leftPageID)
		a.pm.Free(rightPageID)
		return 0, 0, nil, errpkg.BTreeMaterializeLeftPage(err)
	}

	// 物化右半部分（包含 splitKey）
	_, err = a.materializer.MaterializePageFromBytes(rightPageID, keys[splitIdx:], values[splitIdx:])
	if err != nil {
		a.pm.Free(leftPageID)
		a.pm.Free(rightPageID)
		return 0, 0, nil, errpkg.BTreeMaterializeRightPage(err)
	}

	// 获取原始页面的 prevPage 和 nextPage
	oldPrevPage := a.pa.GetPrevPage(uint32(pageID))
	oldNextPage := a.pa.GetNextPage(uint32(pageID))

	// 设置链表指针
	// 1. 设置 left 和 right 之间的链接
	a.pa.SetNextPage(leftPageID, rightPageID)
	a.pa.SetPrevPage(rightPageID, leftPageID)

	// 2. 链接到前驱节点
	if oldPrevPage != 0xFFFFFFFF {
		a.pa.SetPrevPage(leftPageID, oldPrevPage)
		// 注意：不修改前驱节点的 nextPage，因为旧页面可能还在使用
		// 调用者负责在 CAS 成功后更新链接
	}

	// 3. 链接到后继节点
	if oldNextPage != 0xFFFFFFFF {
		a.pa.SetNextPage(rightPageID, oldNextPage)
		// 注意：不修改后继节点的 prevPage，因为旧页面可能还在使用
		// 调用者负责在 CAS 成功后更新链接
	}

	// 注意：不立即释放旧页面，由调用者在 CAS 成功后释放

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
// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
func (a *OffHeapAdapter) GetChild(pageID model.PageID, index int) (model.PageID, error) {
	encodedChildID := a.pa.GetChild(uint32(pageID), index)
	childID, _ := a.DecodeChildWithVersion(encodedChildID)
	return model.PageID(childID), nil
}

// SearchChild 在 Off-Heap 索引页面中搜索子节点
// 返回 (childPageID, found, error)
//
// B+ 树索引节点语义：
// - keys = [k0, k1, ..., k(n-1)]
// - children = [c0, k0, c1, k1, ..., k(n-1), cn] (N+1 child)
// - 如果 key < k0，返回 c0
// - 如果 key >= k(i) 且 key < k(i+1)，返回 c(i+1)
// - 如果 key >= k(n-1)，返回 cn
// - 如果精确匹配 k(i)，返回 c(i+1)（右子节点）
//
// 版本号检测：
// - 从父节点读取子节点的 pageID 和期望版本号
// - 验证子页面的实际版本号是否匹配
// - 不匹配说明是僵尸引用，返回 ErrRetry
func (a *OffHeapAdapter) SearchChild(pageID model.PageID, key []byte) (model.PageID, bool, error) {
	idx, found, err := a.pa.SearchKey(uint32(pageID), key, false)
	if err != nil {
		return 0, false, err
	}

	// B+ 树：精确匹配时返回右子节点（idx+1）
	// 例如：keys=['key-0040'], children=[1,2]
	// 查找 'key-0040' 时，idx=0, found=true，应该返回 children[1]=2
	childIdx := idx
	if found {
		childIdx = idx + 1
	}

	// 读取子节点的 pageID 和期望版本号（编码在 child 字段中）
	childID, expectedVersion := a.pa.GetChildWithVersion(uint32(pageID), childIdx)

	// 如果 childID == 0，说明没有子节点（可能到达叶子）
	if childID == 0 {
		return 0, found, nil
	}

	// 版本号检测：读取子页面的实际版本号
	actualVersion := a.pa.GetVersionSafe(childID)

	if actualVersion != expectedVersion {
		// 版本号不匹配，说明父节点存储的是陈旧的子节点引用（僵尸引用）
		// 这可能发生在：
		// 1. 子节点被释放并重新分配
		// 2. 父节点未更新子节点引用
		return 0, false, errpkg.BTreeStaleChildRef(uint64(pageID), uint64(childID), expectedVersion, actualVersion)
	}

	return model.PageID(childID), found, nil
}

// InsertIndexEntry 向 Off-Heap 索引页面插入条目
func (a *OffHeapAdapter) InsertIndexEntry(pageID model.PageID, index int, key []byte, child model.PageID) error {
	// 检查页面是否已满（现在直接从页面读取 dataEnd）
	if a.checkPageFull(uint32(pageID), len(key), 0) {
		return errpkg.BTreeIndexPageFull(uint64(pageID))
	}

	// 插入索引条目
	// 重要：需要从页面读取当前的 dataEnd，因为 InsertIndexEntry 使用它来分配空间
	dataEnd := a.pa.GetDataEnd(uint32(pageID))
	err := a.pa.InsertIndexEntry(uint32(pageID), index, key, uint32(child), &dataEnd)
	if err != nil {
		return err
	}
	return nil
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
			return false, errpkg.BTreeKeyOrderViolationAt(i, keys[i-1], keys[i])
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

// DeleteFromLeafPage 从 Off-Heap 叶子页面删除指定 key
// 返回新的页面 ID（原页面保持不变，遵循 COW 语义）
func (a *OffHeapAdapter) DeleteFromLeafPage(
	pageID model.PageID,
	key []byte,
) (model.PageID, error) {
	// 1. 搜索 key 在页面中的位置
	idx, found, err := a.pa.SearchKey(uint32(pageID), key, true)
	if err != nil {
		return pageID, err
	}
	if !found {
		return pageID, ErrKeyNotFound
	}

	// 2. 收集剩余的 KV 对（跳过被删除的 key）
	keys, values := a.pa.CollectKVExcept(uint32(pageID), idx)

	// 3. 分配新页面
	newPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocNewPageForDelete(err)
	}

	// 4. 物化新页面（只包含剩余的 KV 对）
	_, err = a.materializer.MaterializePageFromBytes(newPageID, keys, values)
	if err != nil {
		a.pm.Free(newPageID)
		return 0, errpkg.BTreeMaterializePageAfterDelete(err)
	}

	return model.PageID(newPageID), nil
}

// UpdateChildIndex 更新索引节点中指定位置的 child 指针
// 用于 Delete 操作后更新父节点指向新子页面
// 返回：新的父页面 ID
func (a *OffHeapAdapter) UpdateChildIndex(
	parentPageID model.PageID,
	childIndex int,
	newChildPageID model.PageID,
) (model.PageID, error) {
	count := a.pa.GetCount(uint32(parentPageID))

	// 收集所有 keys 和 children
	keys := make([][]byte, 0, count)
	children := make([]uint32, 0, count+1)

	for i := 0; i < int(count); i++ {
		keyOff, keyLen, encodedChild := a.pa.GetIndexEntryOffset(uint32(parentPageID), i)
		k := a.pa.GetKey(uint32(parentPageID), keyOff, keyLen)

		// 复制 key
		kCopy := make([]byte, len(k))
		copy(kCopy, k)
		keys = append(keys, kCopy)

		// 更新 child 指针（如果是指定位置）
		// 修复：GetIndexEntryOffset 返回编码后的值，需要解码才能获取真实的 pageID
		if i == childIndex {
			children = append(children, uint32(newChildPageID))
		} else {
			child, _ := a.DecodeChildWithVersion(encodedChild)
			children = append(children, child)
		}
	}

	// 添加 extraChild（N+1 child）
	// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
	encodedExtraChild := a.pa.GetChild(uint32(parentPageID), int(count))
	if childIndex == int(count) {
		// 更新 extraChild
		children = append(children, uint32(newChildPageID))
	} else {
		extraChild, _ := a.DecodeChildWithVersion(encodedExtraChild)
		children = append(children, extraChild)
	}

	// 分配新页面
	newParentPageID, err := a.pm.Alloc()
	if err != nil {
		return 0, errpkg.BTreeAllocNewParentPage(err)
	}

	// 物化新父页面
	_, err = a.materializer.MaterializeIndexPageFromBytes(uint32(newParentPageID), keys, children)
	if err != nil {
		a.pm.Free(newParentPageID)
		return 0, errpkg.BTreeMaterializeParentPage(err)
	}

	return model.PageID(newParentPageID), nil
}

// DecodeChildWithVersion 解码子节点引用
// 从编码后的 uint64 中提取真实的 pageID 和版本号
//
// 返回：(pageID, version)
func (a *OffHeapAdapter) DecodeChildWithVersion(encoded uint64) (pageID uint32, version uint32) {
	return offheap.DecodeChildWithVersion(encoded)
}

// EncodeChildWithVersion 编码子节点引用
// 将 pageID 和版本号编码到 uint64 中
//
// 参数：
//
//	pageID - 子节点页面 ID
//	version - 子节点版本号
//
// 返回：编码后的 uint64 值
func (a *OffHeapAdapter) EncodeChildWithVersion(pageID uint32, version uint64) uint64 {
	return offheap.EncodeChildWithVersion(pageID, version)
}
