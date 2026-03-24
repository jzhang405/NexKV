// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"bytes"
	"fmt"
	"unsafe"
)

// 页面类型常量
const (
	PageTypeIndex = 0 // 索引节点（内部节点）
	PageTypeLeaf  = 1 // 叶子节点
)

// 4KB 页面布局：
// ┌──────────────┬──────────────┬──────────────┬──────────────┐
// │ PageHeader   │ Entry 数组   │ 空闲区       │ KV 数据区     │
// │ 32B          │ N×12/16B     │ (预留增长)   │ key[]+val[]   │
// └──────────────┴──────────────┴──────────────┴──────────────┘
//
// 空闲区从后往前分配，Entry 数组从前往后增长
// KV 数据区紧凑存储，支持变长 key/value

// PageHeader 页面头部（32 字节，Cache Line 对齐）
// 字段按大小排序，确保无内部 padding
type PageHeader struct {
	version  uint64 // 8 bytes - 版本号（用于 CCOW）
	prevPage uint32 // 4 bytes - 前一个页面 pageID（链表）
	nextPage uint32 // 4 bytes - 后一个页面 pageID（链表）
	count    uint16 // 2 bytes - 条目数（entries 数量）
	pageType uint8  // 1 byte  - 页面类型（0=索引 1=叶子）
	_pad     [13]byte // 13 bytes - 对齐到 32 字节 (8+4+4+2+1+13 = 32)
}

// SizeofPageHeader PageHeader 大小（32 字节）
const SizeofPageHeader = int(unsafe.Sizeof(PageHeader{}))

// IndexEntry 索引节点条目（12 字节）
type IndexEntry struct {
	keyOff uint32 // 4 bytes - key 在页内的偏移（从页面尾部开始）
	keyLen uint32 // 4 bytes - key 长度
	child  uint32 // 4 bytes - 子节点 pageID
}

// SizeofIndexEntry IndexEntry 大小（12 字节）
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
	return NodeRef{
		pageID: pageID,
		isLeaf: isLeaf,
	}
}

// IsValid 检查节点引用是否有效
func (ref NodeRef) IsValid() bool {
	return ref.pageID != 0xFFFFFFFF // 0xFFFFFFFF 保留为无效值
}

// PageAccessor 页面访问器（封装 unsafe 操作）
type PageAccessor struct {
	pm *PageManager
}

// NewPageAccessor 创建页面访问器
func NewPageAccessor(pm *PageManager) *PageAccessor {
	return &PageAccessor{pm: pm}
}

// GetHeader 获取页面头
func (pa *PageAccessor) GetHeader(pageID uint32) *PageHeader {
	ptr := pa.pm.PageIDToPtr(pageID)
	return (*PageHeader)(unsafe.Pointer(ptr))
}

// GetIndexEntry 获取索引节点条目
func (pa *PageAccessor) GetIndexEntry(pageID uint32, index int) *IndexEntry {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(unsafe.Pointer(ptr))
	if index >= int(header.count) {
		panic(fmt.Sprintf("index %d out of range (count: %d)", index, header.count))
	}

	entryPtr := ptr + uintptr(SizeofPageHeader) + uintptr(index)*uintptr(SizeofIndexEntry)
	return (*IndexEntry)(unsafe.Pointer(entryPtr))
}

// GetLeafEntry 获取叶子节点条目
func (pa *PageAccessor) GetLeafEntry(pageID uint32, index int) *LeafEntry {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(unsafe.Pointer(ptr))
	if index >= int(header.count) {
		panic(fmt.Sprintf("index %d out of range (count: %d)", index, header.count))
	}

	entryPtr := ptr + uintptr(SizeofPageHeader) + uintptr(index)*uintptr(SizeofLeafEntry)
	return (*LeafEntry)(unsafe.Pointer(entryPtr))
}

// GetKey 获取 key（返回 Go 切片，指向 mmap 内存）
func (pa *PageAccessor) GetKey(pageID uint32, keyOff, keyLen uint32) []byte {
	ptr := pa.pm.PageIDToPtr(pageID)
	keyPtr := ptr + uintptr(keyOff)
	return unsafe.Slice((*byte)(unsafe.Pointer(keyPtr)), keyLen)
}

// GetValue 获取 value（返回 Go 切片，指向 mmap 内存）
func (pa *PageAccessor) GetValue(pageID uint32, valOff, valLen uint32) []byte {
	ptr := pa.pm.PageIDToPtr(pageID)
	valPtr := ptr + uintptr(valOff)
	return unsafe.Slice((*byte)(unsafe.Pointer(valPtr)), valLen)
}

// InitPage 初始化新页面
func (pa *PageAccessor) InitPage(pageID uint32, pageType uint8, version uint64) {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(unsafe.Pointer(ptr))

	header.pageType = pageType
	header.count = 0
	header.prevPage = 0xFFFFFFFF // 空链表
	header.nextPage = 0xFFFFFFFF
	header.version = version
	// _pad 自动初始化为零
}

// InitIndexPage 初始化索引页面
func (pa *PageAccessor) InitIndexPage(pageID uint32, version uint64) {
	pa.InitPage(pageID, PageTypeIndex, version)
}

// InitLeafPage 初始化叶子页面
func (pa *PageAccessor) InitLeafPage(pageID uint32, version uint64) {
	pa.InitPage(pageID, PageTypeLeaf, version)
}

// InsertIndexEntry 插入索引条目（返回写入的 offset）
func (pa *PageAccessor) InsertIndexEntry(pageID uint32, index int, key []byte, child uint32, dataEnd *uint16) error {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(unsafe.Pointer(ptr))

	// 检查是否有空间
	keyLen := uint32(len(key))
	requiredSpace := uint32(SizeofIndexEntry) + keyLen
	usedSpace := uint32(SizeofPageHeader) + uint32(header.count)*uint32(SizeofIndexEntry) + uint32(*dataEnd)
	if usedSpace+requiredSpace > PageSize {
		return fmt.Errorf("page full: used=%d, required=%d, total=%d", usedSpace, requiredSpace, PageSize)
	}

	// 移动现有 entries（如果需要）
	if index < int(header.count) {
		src := ptr + uintptr(SizeofPageHeader) + uintptr(index)*uintptr(SizeofIndexEntry)
		dst := ptr + uintptr(SizeofPageHeader) + uintptr(index+1)*uintptr(SizeofIndexEntry)
		count := header.count - uint16(index)
		moveSlice := unsafe.Slice((*byte)(unsafe.Pointer(src)), count*uint16(SizeofIndexEntry))
		dstSlice := unsafe.Slice((*byte)(unsafe.Pointer(dst)), len(moveSlice))
		copy(dstSlice, moveSlice)
	}

	// 写入 key（从页面尾部开始分配）
	keyOff := PageSize - uint32(*dataEnd) - keyLen
	*dataEnd += uint16(keyLen)
	keyPtr := ptr + uintptr(keyOff)
	keySlice := unsafe.Slice((*byte)(unsafe.Pointer(keyPtr)), keyLen)
	copy(keySlice, key)

	// 写入 entry
	entryPtr := ptr + uintptr(SizeofPageHeader) + uintptr(index)*uintptr(SizeofIndexEntry)
	entry := (*IndexEntry)(unsafe.Pointer(entryPtr))
	entry.keyOff = uint32(keyOff)
	entry.keyLen = keyLen
	entry.child = child

	header.count++
	return nil
}

// InsertLeafEntry 插入叶子条目（返回写入的 offset）
func (pa *PageAccessor) InsertLeafEntry(pageID uint32, index int, key, value []byte, dataEnd *uint16) error {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(unsafe.Pointer(ptr))

	// 检查是否有空间
	keyLen := uint32(len(key))
	valLen := uint32(len(value))
	requiredSpace := uint32(SizeofLeafEntry) + keyLen + valLen
	usedSpace := uint32(SizeofPageHeader) + uint32(header.count)*uint32(SizeofLeafEntry) + uint32(*dataEnd)
	if usedSpace+requiredSpace > PageSize {
		return fmt.Errorf("page full: used=%d, required=%d, total=%d", usedSpace, requiredSpace, PageSize)
	}

	// 移动现有 entries（如果需要）
	if index < int(header.count) {
		src := ptr + uintptr(SizeofPageHeader) + uintptr(index)*uintptr(SizeofLeafEntry)
		dst := ptr + uintptr(SizeofPageHeader) + uintptr(index+1)*uintptr(SizeofLeafEntry)
		count := header.count - uint16(index)
		moveSlice := unsafe.Slice((*byte)(unsafe.Pointer(src)), count*uint16(SizeofLeafEntry))
		dstSlice := unsafe.Slice((*byte)(unsafe.Pointer(dst)), len(moveSlice))
		copy(dstSlice, moveSlice)
	}

	// 写入 value（从页面尾部开始分配）
	valOff := PageSize - uint32(*dataEnd) - valLen
	*dataEnd += uint16(valLen)
	valPtr := ptr + uintptr(valOff)
	valSlice := unsafe.Slice((*byte)(unsafe.Pointer(valPtr)), valLen)
	copy(valSlice, value)

	// 写入 key（在 value 前面）
	keyOff := valOff - keyLen
	*dataEnd += uint16(keyLen)
	keyPtr := ptr + uintptr(keyOff)
	keySlice := unsafe.Slice((*byte)(unsafe.Pointer(keyPtr)), keyLen)
	copy(keySlice, key)

	// 写入 entry
	entryPtr := ptr + uintptr(SizeofPageHeader) + uintptr(index)*uintptr(SizeofLeafEntry)
	entry := (*LeafEntry)(unsafe.Pointer(entryPtr))
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

// SetVersion 设置页面版本号
func (pa *PageAccessor) SetVersion(pageID uint32, version uint64) {
	pa.GetHeader(pageID).version = version
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
func (pa *PageAccessor) GetChild(pageID uint32, index int) uint32 {
	entry := pa.GetIndexEntry(pageID, index)
	return entry.child
}

// SetChild 设置索引节点的子节点
func (pa *PageAccessor) SetChild(pageID uint32, index int, child uint32) {
	entry := pa.GetIndexEntry(pageID, index)
	entry.child = child
}
