// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"bytes"
	"fmt"
	"unsafe"

	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// 页面类型常量
const (
	PageTypeIndex = 0 // 索引节点（内部节点）
	PageTypeLeaf  = 1 // 叶子节点
)

// 4KB 页面布局：
// ┌──────────────┬──────────────┬──────────────┬──────────────┐
// │ PageHeader   │ Entry 数组    │ 空闲区        │ KV 数据区     │
// │ 32B          │ N×12/16B    │ (预留增长)    │ key[]+val[]  │
// └──────────────┴──────────────┴──────────────┴──────────────┘
//
// 空闲区从后往前分配，Entry 数组从前往后增长
// KV 数据区紧凑存储，支持变长 key/value

// PageHeader 页面头部（32 字节）
type PageHeader struct {
	version    uint64 // 版本号（CCOW）
	prevPage   uint32 // 前一个页面 pageID
	nextPage   uint32 // 后一个页面 pageID
	extraChild uint64 // 索引节点的 N+1 child（pageID + version）
	count      uint16 // 条目数
	pageType   uint8  // 页面类型（0=索引 1=叶子）
	_pad       [5]byte
}

// SizeofPageHeader PageHeader 大小（32 字节）
const SizeofPageHeader = int(unsafe.Sizeof(PageHeader{}))

// IndexEntry 索引节点条目（16 字节）
type IndexEntry struct {
	keyOff uint32 // 4 bytes - key 在页内的偏移（从页面尾部开始）
	keyLen uint32 // 4 bytes - key 长度
	child  uint64 // 8 bytes - 子节点（32-bit pageID + 32-bit version）
}

// SizeofIndexEntry IndexEntry 大小（16 字节）
const SizeofIndexEntry = int(unsafe.Sizeof(IndexEntry{}))

// LeafEntry 叶子节点条目（16 字节）
type LeafEntry struct {
	keyOff uint32 // 4 bytes - key 在页内的偏移
	keyLen uint32 // 4 bytes - key 长度
	valOff uint32 // 4 bytes - value 在页内的偏移
	valLen uint32 // 4 bytes - value 长度
}

// SizeofLeafEntry LeafEntry 大小（16 字节）
const SizeofLeafEntry = int(unsafe.Sizeof(LeafEntry{}))

// NodeRef 节点引用（替换 *BTreeNode）
// 一次性替换策略：不保留 *BTreeNode 双模式并存
type NodeRef struct {
	pageID uint32 // 页面 ID
	isLeaf bool   // 是否为叶子节点
}

// NewNodeRef 创建节点引用
func NewNodeRef(pageID uint32, isLeaf bool) NodeRef {
	return NodeRef{pageID: pageID, isLeaf: isLeaf}
}

// IsValid 检查节点引用是否有效
func (ref NodeRef) IsValid() bool {
	return ref.pageID != 0xFFFFFFFF
}

// GetPageID 获取页面 ID
func (ref NodeRef) GetPageID() uint32 {
	return ref.pageID
}

// IsLeaf 检查是否为叶子节点
func (ref NodeRef) IsLeaf() bool {
	return ref.isLeaf
}

const (
	ChildVersionBits  = 32
	ChildVersionMask  = 0xFFFFFFFF00000000
	ChildIDMask       = 0x00000000FFFFFFFF
	ChildVersionShift = 32
	MaxChildID        = (1 << 32) - 1
	MaxChildVersion   = (1 << 32) - 1
)

// EncodeChildWithVersion 编码 pageID 和版本号到 uint64
// 高 32 位：版本号
// 低 32 位：pageID
func EncodeChildWithVersion(pageID uint32, version uint64) uint64 {
	if pageID > MaxChildID {
		panic(fmt.Sprintf("pageID %d exceeds max %d", pageID, MaxChildID))
	}
	version32 := uint32(version) & MaxChildVersion
	return (uint64(version32) << ChildVersionShift) | (uint64(pageID) & ChildIDMask)
}

// DecodeChildWithVersion 从 uint64 解码 pageID 和版本号
func DecodeChildWithVersion(encoded uint64) (pageID uint32, version uint32) {
	pageID = uint32(encoded & ChildIDMask)
	version = uint32((encoded & ChildVersionMask) >> ChildVersionShift)
	return
}

// PageAccessor 页面访问器（封装 unsafe 操作）
type PageAccessor struct {
	pm *PageManager
}

// NewPageAccessor 创建页面访问器
func NewPageAccessor(pm *PageManager) *PageAccessor {
	return &PageAccessor{pm: pm}
}

func (pa *PageAccessor) getPtr(pageID uint32) unsafe.Pointer {
	return pa.pm.pageIDToPtrUnchecked(pageID)
}

func (pa *PageAccessor) GetDataEnd(pageID uint32) uint16 {
	ptr := pa.getPtr(pageID)
	header := (*PageHeader)(ptr)

	if header.count == 0 {
		return 0
	}

	minKeyOff := uint32(PageSize)
	if pa.IsLeaf(pageID) {
		for i := 0; i < int(header.count); i++ {
			entryPtr := unsafe.Add(ptr, SizeofPageHeader+i*SizeofLeafEntry)
			entry := (*LeafEntry)(entryPtr)
			if entry.keyOff < minKeyOff {
				minKeyOff = entry.keyOff
			}
		}
	} else {
		for i := 0; i < int(header.count); i++ {
			entryPtr := unsafe.Add(ptr, SizeofPageHeader+i*SizeofIndexEntry)
			entry := (*IndexEntry)(entryPtr)
			if entry.keyOff < minKeyOff {
				minKeyOff = entry.keyOff
			}
		}
	}
	return uint16(PageSize - minKeyOff)
}

// GetSpaceUsage 计算页面空间使用率（0.0-1.0）
func (pa *PageAccessor) GetSpaceUsage(pageID uint32) float64 {
	ptr := pa.getPtr(pageID)
	header := (*PageHeader)(ptr)

	var entrySize uint32
	if pa.IsLeaf(pageID) {
		entrySize = uint32(SizeofLeafEntry)
	} else {
		entrySize = uint32(SizeofIndexEntry)
	}

	// 计算已使用空间：header + entries + dataEnd
	dataEnd := pa.GetDataEnd(pageID)
	usedSpace := uint32(SizeofPageHeader) + uint32(header.count)*entrySize + uint32(dataEnd)

	return float64(usedSpace) / float64(PageSize)
}

// GetHeader 获取页面头
func (pa *PageAccessor) GetHeader(pageID uint32) *PageHeader {
	ptr := pa.getPtr(pageID)
	return (*PageHeader)(ptr)
}

func (pa *PageAccessor) IsValidPage(pageID uint32) bool {
	if pageID == 0 || pageID == 0xFFFFFFFF {
		return false
	}
	header := pa.GetHeader(pageID)
	if header == nil {
		return false
	}
	return header.version >= 1
}

// GetIndexEntry 获取索引节点条目
func (pa *PageAccessor) GetIndexEntry(pageID uint32, index int) *IndexEntry {
	ptr := pa.getPtr(pageID)
	header := (*PageHeader)(ptr)
	if index >= int(header.count) {
		panic(fmt.Sprintf("index %d out of range (count: %d)", index, header.count))
	}

	entryPtr := unsafe.Add(ptr, SizeofPageHeader+index*SizeofIndexEntry)
	return (*IndexEntry)(entryPtr)
}

// GetLeafEntry 获取叶子节点条目
func (pa *PageAccessor) GetLeafEntry(pageID uint32, index int) *LeafEntry {
	ptr := pa.getPtr(pageID)
	header := (*PageHeader)(ptr)
	if index >= int(header.count) {
		panic(fmt.Sprintf("index %d out of range (count: %d)", index, header.count))
	}

	entryPtr := unsafe.Add(ptr, SizeofPageHeader+index*SizeofLeafEntry)
	return (*LeafEntry)(entryPtr)
}

// GetKey 获取 key（返回 Go 切片，指向 mmap 内存）
func (pa *PageAccessor) GetKey(pageID uint32, keyOff, keyLen uint32) []byte {
	ptr := pa.getPtr(pageID)
	keyPtr := unsafe.Add(ptr, keyOff)
	return unsafe.Slice((*byte)(keyPtr), keyLen)
}

// GetValue 获取 value（返回 Go 切片，指向 mmap 内存）
func (pa *PageAccessor) GetValue(pageID uint32, valOff, valLen uint32) []byte {
	ptr := pa.getPtr(pageID)
	valPtr := unsafe.Add(ptr, valOff)
	return unsafe.Slice((*byte)(valPtr), valLen)
}

// InitPage 初始化新页面
func (pa *PageAccessor) InitPage(pageID uint32, pageType uint8, version uint64) {
	ptr := pa.getPtr(pageID)
	header := (*PageHeader)(ptr)

	header.pageType = pageType
	header.count = 0
	header.extraChild = 0
	header.prevPage = 0xFFFFFFFF
	header.nextPage = 0xFFFFFFFF
	header.version = version
}

// InitIndexPage 初始化索引页面
func (pa *PageAccessor) InitIndexPage(pageID uint32, version uint64) {
	pa.InitPage(pageID, PageTypeIndex, version)
}

// InitLeafPage 初始化叶子页面
func (pa *PageAccessor) InitLeafPage(pageID uint32, version uint64) {
	pa.InitPage(pageID, PageTypeLeaf, version)
}

// InsertIndexEntry 插入索引条目
func (pa *PageAccessor) InsertIndexEntry(pageID uint32, index int, key []byte, child uint32, dataEnd *uint16) error {
	ptr := pa.getPtr(pageID)
	header := (*PageHeader)(ptr)

	keyLen := uint32(len(key))
	requiredSpace := uint32(SizeofIndexEntry) + keyLen
	usedSpace := uint32(SizeofPageHeader) + uint32(header.count)*uint32(SizeofIndexEntry) + uint32(*dataEnd)
	if usedSpace+requiredSpace > PageSize {
		return errpkg.OffHeapPageFull(int(usedSpace), int(requiredSpace), PageSize)
	}

	if index < int(header.count) {
		src := unsafe.Add(ptr, SizeofPageHeader+index*SizeofIndexEntry)
		dst := unsafe.Add(ptr, SizeofPageHeader+(index+1)*SizeofIndexEntry)
		count := int(header.count - uint16(index))
		moveSlice := unsafe.Slice((*byte)(src), count*SizeofIndexEntry)
		dstSlice := unsafe.Slice((*byte)(dst), len(moveSlice))
		copy(dstSlice, moveSlice)
	}

	keyOff := PageSize - uint32(*dataEnd) - keyLen
	*dataEnd += uint16(keyLen)
	keyPtr := unsafe.Add(ptr, keyOff)
	keySlice := unsafe.Slice((*byte)(keyPtr), keyLen)
	copy(keySlice, key)

	entryPtr := unsafe.Add(ptr, SizeofPageHeader+index*SizeofIndexEntry)
	entry := (*IndexEntry)(entryPtr)
	entry.keyOff = uint32(keyOff)
	entry.keyLen = keyLen

	if child != 0 {
		var childVersion uint64
		func() {
			defer func() {
				if r := recover(); r != nil {
					childVersion = 0
				}
			}()
			childVersion = pa.GetVersion(child)
		}()
		entry.child = EncodeChildWithVersion(child, childVersion)
	} else {
		entry.child = 0
	}

	header.count++
	return nil
}

// InsertLeafEntry 插入叶子条目
func (pa *PageAccessor) InsertLeafEntry(pageID uint32, index int, key, value []byte, dataEnd *uint16) error {
	ptr := pa.getPtr(pageID)
	header := (*PageHeader)(ptr)

	keyLen := uint32(len(key))
	valLen := uint32(len(value))
	requiredSpace := uint32(SizeofLeafEntry) + keyLen + valLen
	usedSpace := uint32(SizeofPageHeader) + uint32(header.count)*uint32(SizeofLeafEntry) + uint32(*dataEnd)
	if usedSpace+requiredSpace > PageSize {
		return errpkg.OffHeapPageFull(int(usedSpace), int(requiredSpace), PageSize)
	}

	if index < int(header.count) {
		src := unsafe.Add(ptr, SizeofPageHeader+index*SizeofLeafEntry)
		dst := unsafe.Add(ptr, SizeofPageHeader+(index+1)*SizeofLeafEntry)
		count := int(header.count - uint16(index))
		moveSlice := unsafe.Slice((*byte)(src), count*SizeofLeafEntry)
		dstSlice := unsafe.Slice((*byte)(dst), len(moveSlice))
		copy(dstSlice, moveSlice)
	}

	valOff := PageSize - uint32(*dataEnd) - valLen
	*dataEnd += uint16(valLen)
	valPtr := unsafe.Add(ptr, valOff)
	valSlice := unsafe.Slice((*byte)(valPtr), valLen)
	copy(valSlice, value)

	keyOff := valOff - keyLen
	*dataEnd += uint16(keyLen)
	keyPtr := unsafe.Add(ptr, keyOff)
	keySlice := unsafe.Slice((*byte)(keyPtr), keyLen)
	copy(keySlice, key)

	entryPtr := unsafe.Add(ptr, SizeofPageHeader+index*SizeofLeafEntry)
	entry := (*LeafEntry)(entryPtr)
	entry.keyOff = uint32(keyOff)
	entry.keyLen = keyLen
	entry.valOff = uint32(valOff)
	entry.valLen = valLen

	header.count++
	return nil
}

// SearchKey 二分查找 key
// 返回：索引位置，是否找到
func (pa *PageAccessor) SearchKey(pageID uint32, key []byte, isLeaf bool) (int, bool) {
	header := pa.GetHeader(pageID)
	left, right := 0, int(header.count)-1

	result := 0
	found := false

	for left <= right {
		mid := left + (right-left)/2
		var midKey []byte
		if isLeaf {
			entry := pa.GetLeafEntry(pageID, mid)
			midKey = pa.GetKey(pageID, entry.keyOff, entry.keyLen)
		} else {
			entry := pa.GetIndexEntry(pageID, mid)
			midKey = pa.GetKey(pageID, entry.keyOff, entry.keyLen)
		}

		cmp := bytes.Compare(key, midKey)
		if cmp == 0 {
			return mid, true
		} else if cmp < 0 {
			right = mid - 1
		} else {
			left = mid + 1
			result = left
		}
	}

	return result, found
}

// GetVersion 获取页面版本号
func (pa *PageAccessor) GetVersion(pageID uint32) uint64 {
	return pa.GetHeader(pageID).version
}

// GetVersionSafe 安全获取页面版本号，如果页面不存在则返回 0
// 用于测试场景或边界情况，其中子页面可能尚未分配
func (pa *PageAccessor) GetVersionSafe(pageID uint32) uint64 {
	var version uint64
	func() {
		defer func() {
			if r := recover(); r != nil {
				// pageID 不存在或无效，使用版本号 0
				version = 0
			}
		}()
		version = pa.GetVersion(pageID)
	}()
	return version
}

// SetVersion 设置页面版本号
func (pa *PageAccessor) SetVersion(pageID uint32, version uint64) {
	pa.GetHeader(pageID).version = version
}

// IncrementVersion 递增页面版本号（用于版本号检测机制）
// 返回新版本号
func (pa *PageAccessor) IncrementVersion(pageID uint32) uint64 {
	header := pa.GetHeader(pageID)
	header.version++
	return header.version
}

// GetChildWithVersion 获取子节点 pageID 和期望版本号（从编码的 child 值解码）
// 返回的版本号是 uint64，与 GetVersion 保持一致
func (pa *PageAccessor) GetChildWithVersion(pageID uint32, index int) (childPageID uint32, expectedVersion uint64) {
	encoded := pa.GetChild(pageID, index)
	decodedPageID, decodedVersion := DecodeChildWithVersion(encoded)
	childPageID = decodedPageID
	expectedVersion = uint64(decodedVersion)
	return
}

// SetChildWithVersion 设置子节点 pageID 和版本号（编码到 child 值中）
// 注意：这个函数会使用指定的版本号，而不是当前版本
func (pa *PageAccessor) SetChildWithVersion(pageID uint32, index int, childPageID uint32, childVersion uint64) {
	encoded := EncodeChildWithVersion(childPageID, childVersion)
	pa.setChildEncoded(pageID, index, encoded)
}

// GetCount 获取条目数量
func (pa *PageAccessor) GetCount(pageID uint32) uint16 {
	return pa.GetHeader(pageID).count
}

// IsLeaf 判断是否为叶子节点
func (pa *PageAccessor) IsLeaf(pageID uint32) bool {
	return pa.GetHeader(pageID).pageType == PageTypeLeaf
}

// GetPrevPage 获取前一个页面
func (pa *PageAccessor) GetPrevPage(pageID uint32) uint32 {
	return pa.GetHeader(pageID).prevPage
}

// SetPrevPage 设置前一个页面
func (pa *PageAccessor) SetPrevPage(pageID uint32, prev uint32) {
	pa.GetHeader(pageID).prevPage = prev
}

// GetNextPage 获取后一个页面
func (pa *PageAccessor) GetNextPage(pageID uint32) uint32 {
	return pa.GetHeader(pageID).nextPage
}

// SetNextPage 设置后一个页面
func (pa *PageAccessor) SetNextPage(pageID uint32, next uint32) {
	pa.GetHeader(pageID).nextPage = next
}

// GetChild 获取索引节点的子节点
// index == count 时返回 extraChild
func (pa *PageAccessor) GetChild(pageID uint32, index int) uint64 {
	header := pa.GetHeader(pageID)
	if index == int(header.count) {
		return header.extraChild
	}
	header = pa.GetHeader(pageID)
	if index >= int(header.count) {
		return 0
	}
	entry := pa.GetIndexEntry(pageID, index)
	return entry.child
}

// SetChild 设置索引节点的子节点
// index == count 时设置 extraChild
func (pa *PageAccessor) SetChild(pageID uint32, index int, child uint32) {
	var encodedChild uint64
	if child != 0 {
		var childVersion uint64
		func() {
			defer func() {
				if r := recover(); r != nil {
					childVersion = 0
				}
			}()
			childVersion = pa.GetVersion(child)
		}()
		encodedChild = EncodeChildWithVersion(child, childVersion)
	} else {
		encodedChild = 0
	}

	pa.setChildEncoded(pageID, index, encodedChild)
}

// setChildEncoded 内部函数：直接设置编码后的子节点值
// 仅供 SetChildWithVersion 使用，避免双重编码
func (pa *PageAccessor) setChildEncoded(pageID uint32, index int, encodedChild uint64) {
	header := pa.GetHeader(pageID)
	if index == int(header.count) {
		// 设置 N+1 child（最后一个 child）
		header.extraChild = encodedChild
		return
	}
	entry := pa.GetIndexEntry(pageID, index)
	entry.child = encodedChild
}

// GetLeafEntryOffset 获取叶子条目的 key/value offset（用于跨包访问）
func (pa *PageAccessor) GetLeafEntryOffset(pageID uint32, index int) (keyOff, keyLen, valOff, valLen uint32) {
	entry := pa.GetLeafEntry(pageID, index)
	return entry.keyOff, entry.keyLen, entry.valOff, entry.valLen
}

// GetIndexEntryOffset 获取索引条目的 key offset 和 child（用于跨包访问）
func (pa *PageAccessor) GetIndexEntryOffset(pageID uint32, index int) (keyOff, keyLen uint32, child uint64) {
	entry := pa.GetIndexEntry(pageID, index)
	return entry.keyOff, entry.keyLen, entry.child
}

// GetIndexKey 获取索引节点的 key
func (pa *PageAccessor) GetIndexKey(pageID uint32, index int) []byte {
	entry := pa.GetIndexEntry(pageID, index)
	return pa.GetKey(pageID, entry.keyOff, entry.keyLen)
}

// SearchChildIndex 在索引页面中搜索子节点
// 返回 (childIndex, found)
func (pa *PageAccessor) SearchChildIndex(pageID uint32, key []byte) (int, bool) {
	return pa.SearchKey(pageID, key, false)
}

// CollectKVExcept 收集叶子节点的 KV 对（跳过指定索引）
// 用于 Off-Heap Delete 操作
// 返回：keys 切片, values 切片
func (pa *PageAccessor) CollectKVExcept(pageID uint32, skipIdx int) ([][]byte, [][]byte) {
	header := pa.GetHeader(pageID)
	count := int(header.count)

	var keys [][]byte
	var values [][]byte

	for i := 0; i < count; i++ {
		if i == skipIdx {
			continue // 跳过被删除的 key
		}

		keyOff, keyLen, valOff, valLen := pa.GetLeafEntryOffset(pageID, i)
		key := pa.GetKey(pageID, keyOff, keyLen)
		value := pa.GetValue(pageID, valOff, valLen)

		keys = append(keys, key)
		values = append(values, value)
	}

	return keys, values
}

// BulkInitLeafFromSource 从源叶子页面批量拷贝连续条目范围到目标页面
// 跳过 Go 堆分配：key/value 直接从源 mmap 页面读取，逐条插入目标页面
//
// srcPageID: 源页面
// dstPageID: 目标页面（必须已分配）
// startIdx, endIdx: 源页面中的条目范围 [startIdx, endIdx)
//
// 返回 dataEnd（KV 数据区大小）和 error
func (pa *PageAccessor) BulkInitLeafFromSource(
	srcPageID, dstPageID uint32,
	startIdx, endIdx int,
) (uint16, error) {
	srcHeader := pa.GetHeader(srcPageID)
	totalCount := int(srcHeader.count)

	if startIdx < 0 || endIdx > totalCount || startIdx >= endIdx {
		return 0, fmt.Errorf("invalid range [%d, %d) (count: %d)", startIdx, endIdx, totalCount)
	}

	// 初始化目标页面
	pa.InitLeafPage(dstPageID, srcHeader.version)
	dataEnd := uint16(0)

	// 逐条从源页面读取并插入目标页面
	// key/value 是 mmap 切片，不经过 Go 堆分配
	for i := startIdx; i < endIdx; i++ {
		entry := pa.GetLeafEntry(srcPageID, i)
		key := pa.GetKey(srcPageID, entry.keyOff, entry.keyLen)
		value := pa.GetValue(srcPageID, entry.valOff, entry.valLen)
		dstIdx := i - startIdx
		if err := pa.InsertLeafEntry(dstPageID, dstIdx, key, value, &dataEnd); err != nil {
			return 0, err
		}
	}

	return dataEnd, nil
}

// BulkInitIndexFromSource 从源索引页面批量拷贝连续条目范围到目标页面
// 跳过 Go 堆分配：key 直接从源 mmap 页面读取，逐条插入目标页面
//
// srcPageID: 源页面
// dstPageID: 目标页面（必须已分配）
// startIdx, endIdx: 源页面中的条目范围 [startIdx, endIdx)
// extraChild: 额外的子节点（最后一个条目右边的子节点，编码后的 uint64）
//
// 返回 dataEnd（key 数据区大小）和 error
func (pa *PageAccessor) BulkInitIndexFromSource(
	srcPageID, dstPageID uint32,
	startIdx, endIdx int,
	extraChild uint64,
) (uint16, error) {
	srcHeader := pa.GetHeader(srcPageID)
	totalCount := int(srcHeader.count)

	if startIdx < 0 || endIdx > totalCount || startIdx >= endIdx {
		return 0, fmt.Errorf("invalid range [%d, %d) (count: %d)", startIdx, endIdx, totalCount)
	}

	// 初始化目标页面
	pa.InitIndexPage(dstPageID, srcHeader.version)
	dataEnd := uint16(0)

	// 逐条从源页面读取并插入目标页面
	// key 是 mmap 切片，不经过 Go 堆分配
	for i := startIdx; i < endIdx; i++ {
		entry := pa.GetIndexEntry(srcPageID, i)
		key := pa.GetKey(srcPageID, entry.keyOff, entry.keyLen)
		child, _ := DecodeChildWithVersion(entry.child)
		dstIdx := i - startIdx
		if err := pa.InsertIndexEntry(dstPageID, dstIdx, key, child, &dataEnd); err != nil {
			return 0, err
		}
	}

	// 设置 extraChild（N+1 child）
	dstHeader := pa.GetHeader(dstPageID)
	dstHeader.extraChild = extraChild

	return dataEnd, nil
}
