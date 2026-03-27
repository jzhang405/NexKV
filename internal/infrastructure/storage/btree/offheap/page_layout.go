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
	version    uint64  // 8 bytes - 版本号（用于 CCOW）
	prevPage   uint32  // 4 bytes - 前一个页面 pageID（链表）
	nextPage   uint32  // 4 bytes - 后一个页面 pageID（链表）
	extraChild uint32  // 4 bytes - 索引节点的 N+1 child（B+ 树语义，从 _pad 借用空间）
	count      uint16  // 2 bytes - 条目数（entries 数量）
	pageType   uint8   // 1 byte  - 页面类型（0=索引 1=叶子）
	_pad       [9]byte // 9 bytes - 对齐到 32 字节 (8+4+4+4+2+1+9 = 32)
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

// GetPageID 获取页面 ID
func (ref NodeRef) GetPageID() uint32 {
	return ref.pageID
}

// IsLeaf 检查是否为叶子节点
func (ref NodeRef) IsLeaf() bool {
	return ref.isLeaf
}

// 版本号编码常量（用于 IndexEntry.child 字段）
const (
	ChildVersionBits = 16                  // 版本号使用的位数
	ChildVersionMask = 0xFFFF0000          // 版本号掩码（高 16 位）
	ChildIDMask      = 0x0000FFFF          // pageID 掩码（低 16 位）
	ChildVersionShift = 16                 // 版本号位移量
	MaxChildID       = (1 << 16) - 1       // 最大 pageID (65535)
	MaxChildVersion  = (1 << 16) - 1       // 最大版本号 (65535)
)

// EncodeChildWithVersion 编码 pageID 和版本号到 uint32
// 高 16 位：版本号的低 16 位
// 低 16 位：pageID
func EncodeChildWithVersion(pageID uint32, version uint64) uint32 {
	if pageID > MaxChildID {
		panic(fmt.Sprintf("pageID %d exceeds max %d", pageID, MaxChildID))
	}
	version16 := uint16(version) & MaxChildVersion
	return (uint32(version16) << ChildVersionShift) | (pageID & ChildIDMask)
}

// DecodeChildWithVersion 从 uint32 解码 pageID 和版本号
func DecodeChildWithVersion(encoded uint32) (pageID uint32, version uint16) {
	pageID = encoded & ChildIDMask
	version = uint16((encoded & ChildVersionMask) >> ChildVersionShift)
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

// GetDataEnd 从页面结构计算实际的 dataEnd
// dataEnd 表示从页面末尾到第一个 KV 数据起点的距离
// 通过扫描所有 entries 来计算实际的 KV 数据区大小
func (pa *PageAccessor) GetDataEnd(pageID uint32) uint16 {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)

	if header.count == 0 {
		return 0
	}

	if pa.IsLeaf(pageID) {
		// 叶子节点：扫描所有 entries，找到最小的 keyOff（KV 数据区的起点）
		minKeyOff := uint32(PageSize)
		for i := 0; i < int(header.count); i++ {
			entryPtr := unsafe.Add(ptr, SizeofPageHeader+i*SizeofLeafEntry)
			entry := (*LeafEntry)(entryPtr)
			if entry.keyOff < minKeyOff {
				minKeyOff = entry.keyOff
			}
		}
		// dataEnd = 从页面末尾到 KV 数据区起点的距离
		return uint16(PageSize - minKeyOff)
	} else {
		// 索引节点：扫描所有 entries，找到最小的 keyOff（KV 数据区的起点）
		minKeyOff := uint32(PageSize)
		for i := 0; i < int(header.count); i++ {
			entryPtr := unsafe.Add(ptr, SizeofPageHeader+i*SizeofIndexEntry)
			entry := (*IndexEntry)(entryPtr)
			if entry.keyOff < minKeyOff {
				minKeyOff = entry.keyOff
			}
		}
		return uint16(PageSize - minKeyOff)
	}
}

// GetSpaceUsage 计算页面空间使用率（0.0-1.0）
func (pa *PageAccessor) GetSpaceUsage(pageID uint32) float64 {
	ptr := pa.pm.PageIDToPtr(pageID)
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
	ptr := pa.pm.PageIDToPtr(pageID)
	return (*PageHeader)(ptr)
}

// IsValidPage 检查页面是否有效（未被释放）
// 用于并发场景下 Get 操作的页面状态验证
func (pa *PageAccessor) IsValidPage(pageID uint32) bool {
	if pageID == 0 || pageID == 0xFFFFFFFF {
		return false
	}

	// 通过检查页面的 pageType 来验证
	// 已释放的页面 pageType 应该是 0（未初始化状态）
	header := pa.GetHeader(pageID)
	if header == nil {
		return false
	}

	// pageType 为 0 表示页面未初始化或已释放
	// 有效页面的 pageType 应该是 PageTypeIndex (0) 或 PageTypeLeaf (1)
	// 但由于 PageTypeIndex = 0，我们需要额外检查版本号
	// 已初始化的页面版本号 >= 1
	return header.version >= 1
}

// GetIndexEntry 获取索引节点条目
func (pa *PageAccessor) GetIndexEntry(pageID uint32, index int) *IndexEntry {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	if index >= int(header.count) {
		panic(fmt.Sprintf("index %d out of range (count: %d)", index, header.count))
	}

	entryPtr := unsafe.Add(ptr, SizeofPageHeader+index*SizeofIndexEntry)
	return (*IndexEntry)(entryPtr)
}

// GetLeafEntry 获取叶子节点条目
func (pa *PageAccessor) GetLeafEntry(pageID uint32, index int) *LeafEntry {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)
	if index >= int(header.count) {
		panic(fmt.Sprintf("index %d out of range (count: %d)", index, header.count))
	}

	entryPtr := unsafe.Add(ptr, SizeofPageHeader+index*SizeofLeafEntry)
	return (*LeafEntry)(entryPtr)
}

// GetKey 获取 key（返回 Go 切片，指向 mmap 内存）
func (pa *PageAccessor) GetKey(pageID uint32, keyOff, keyLen uint32) []byte {
	ptr := pa.pm.PageIDToPtr(pageID)
	keyPtr := unsafe.Add(ptr, keyOff)
	return unsafe.Slice((*byte)(keyPtr), keyLen)
}

// GetValue 获取 value（返回 Go 切片，指向 mmap 内存）
func (pa *PageAccessor) GetValue(pageID uint32, valOff, valLen uint32) []byte {
	ptr := pa.pm.PageIDToPtr(pageID)
	valPtr := unsafe.Add(ptr, valOff)
	return unsafe.Slice((*byte)(valPtr), valLen)
}

// InitPage 初始化新页面
func (pa *PageAccessor) InitPage(pageID uint32, pageType uint8, version uint64) {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)

	oldPageType := header.pageType
	header.pageType = pageType
	header.count = 0
	header.extraChild = 0        // 清空 N+1 child（防止页面重用时出现循环引用）
	header.prevPage = 0xFFFFFFFF // 空链表
	header.nextPage = 0xFFFFFFFF
	header.version = version
	// _pad 自动初始化为零

	// Debug logging for specific pages
	if pageID == 539 || pageID == 547 || pageID == 548 || pageID == 1317 {
		pageTypeName := "LEAF"
		if pageType == PageTypeIndex {
			pageTypeName = "INDEX"
		}
		oldTypeName := "LEAF"
		if oldPageType == PageTypeIndex {
			oldTypeName = "INDEX"
		}
		fmt.Printf("[INIT_PAGE] pageID=%d %s -> %s (version=%d) prev=0x%08x next=0x%08x\n",
			pageID, oldTypeName, pageTypeName, version, header.prevPage, header.nextPage)
	}
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
	header := (*PageHeader)(ptr)

	// 检查是否有空间
	keyLen := uint32(len(key))
	requiredSpace := uint32(SizeofIndexEntry) + keyLen
	usedSpace := uint32(SizeofPageHeader) + uint32(header.count)*uint32(SizeofIndexEntry) + uint32(*dataEnd)
	if usedSpace+requiredSpace > PageSize {
		return fmt.Errorf("page full: used=%d, required=%d, total=%d", usedSpace, requiredSpace, PageSize)
	}

	// 移动现有 entries（如果需要）
	if index < int(header.count) {
		src := unsafe.Add(ptr, SizeofPageHeader+index*SizeofIndexEntry)
		dst := unsafe.Add(ptr, SizeofPageHeader+(index+1)*SizeofIndexEntry)
		count := int(header.count - uint16(index))
		moveSlice := unsafe.Slice((*byte)(src), count*SizeofIndexEntry)
		dstSlice := unsafe.Slice((*byte)(dst), len(moveSlice))
		copy(dstSlice, moveSlice)
	}

	// 写入 key（从页面尾部开始分配）
	keyOff := PageSize - uint32(*dataEnd) - keyLen
	*dataEnd += uint16(keyLen)
	keyPtr := unsafe.Add(ptr, keyOff)
	keySlice := unsafe.Slice((*byte)(keyPtr), keyLen)
	copy(keySlice, key)

	// 写入 entry
	entryPtr := unsafe.Add(ptr, SizeofPageHeader+index*SizeofIndexEntry)
	entry := (*IndexEntry)(entryPtr)
	entry.keyOff = uint32(keyOff)
	entry.keyLen = keyLen

	// 版本号检测：编码子节点的版本号到 child 字段
	// 只有当 child != 0 时才读取版本号（0 表示没有子节点）
	if child != 0 {
		childVersion := pa.GetVersion(child)
		entry.child = EncodeChildWithVersion(child, childVersion)
	} else {
		entry.child = 0
	}

	header.count++
	return nil
}

// InsertLeafEntry 插入叶子条目（返回写入的 offset）
func (pa *PageAccessor) InsertLeafEntry(pageID uint32, index int, key, value []byte, dataEnd *uint16) error {
	ptr := pa.pm.PageIDToPtr(pageID)
	header := (*PageHeader)(ptr)

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
		src := unsafe.Add(ptr, SizeofPageHeader+index*SizeofLeafEntry)
		dst := unsafe.Add(ptr, SizeofPageHeader+(index+1)*SizeofLeafEntry)
		count := int(header.count - uint16(index))
		moveSlice := unsafe.Slice((*byte)(src), count*SizeofLeafEntry)
		dstSlice := unsafe.Slice((*byte)(dst), len(moveSlice))
		copy(dstSlice, moveSlice)
	}

	// 写入 value（从页面尾部开始分配）
	valOff := PageSize - uint32(*dataEnd) - valLen
	*dataEnd += uint16(valLen)
	valPtr := unsafe.Add(ptr, valOff)
	valSlice := unsafe.Slice((*byte)(valPtr), valLen)
	copy(valSlice, value)

	// 写入 key（在 value 前面）
	keyOff := valOff - keyLen
	*dataEnd += uint16(keyLen)
	keyPtr := unsafe.Add(ptr, keyOff)
	keySlice := unsafe.Slice((*byte)(keyPtr), keyLen)
	copy(keySlice, key)

	// 写入 entry
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
func (pa *PageAccessor) GetChildWithVersion(pageID uint32, index int) (childPageID uint32, expectedVersion uint16) {
	encoded := pa.GetChild(pageID, index)
	childPageID, expectedVersion = DecodeChildWithVersion(encoded)
	return
}

// SetChildWithVersion 设置子节点 pageID 和版本号（编码到 child 值中）
func (pa *PageAccessor) SetChildWithVersion(pageID uint32, index int, childPageID uint32, childVersion uint64) {
	encoded := EncodeChildWithVersion(childPageID, childVersion)
	pa.SetChild(pageID, index, encoded)
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
	// 调试：追踪页面 1317 的 nextPage 设置
	if pageID == 1317 || next == 1317 || next == 1318 || next == 1316 {
		fmt.Printf("[SET_NEXT] pageID=%d next=%d\n", pageID, next)
	}
}

// GetChild 获取索引节点的子节点
// 支持 B+ 树的 N+1 child 语义：如果 index == count，返回 extraChild
//
// 并发安全：在读取 GetIndexEntry 前重新验证 index 范围，
// 防止 TOCTOU 竞态条件（页面在检查和使用之间被修改）
func (pa *PageAccessor) GetChild(pageID uint32, index int) uint32 {
	header := pa.GetHeader(pageID)
	if index == int(header.count) {
		// 返回 N+1 child（最后一个 child）
		return header.extraChild
	}
	// 重新读取 header.count，防止 TOCTOU
	header = pa.GetHeader(pageID)
	if index >= int(header.count) {
		// 页面被修改，index 越界
		// 返回 0 表示无效子节点
		return 0
	}
	entry := pa.GetIndexEntry(pageID, index)
	return entry.child
}

// SetChild 设置索引节点的子节点
// 支持 B+ 树的 N+1 child 语义：如果 index == count，设置 extraChild
// 自动编码子节点的版本号到 child 字段中（用于僵尸引用检测）
func (pa *PageAccessor) SetChild(pageID uint32, index int, child uint32) {
	header := pa.GetHeader(pageID)
	if index == int(header.count) {
		// 设置 N+1 child（最后一个 child）
		if child != 0 {
			childVersion := pa.GetVersion(child)
			header.extraChild = EncodeChildWithVersion(child, childVersion)
		} else {
			header.extraChild = 0
		}
		return
	}
	entry := pa.GetIndexEntry(pageID, index)
	if child != 0 {
		childVersion := pa.GetVersion(child)
		entry.child = EncodeChildWithVersion(child, childVersion)
	} else {
		entry.child = 0
	}
}

// GetLeafEntryOffset 获取叶子条目的 key/value offset（用于跨包访问）
func (pa *PageAccessor) GetLeafEntryOffset(pageID uint32, index int) (keyOff, keyLen, valOff, valLen uint32) {
	entry := pa.GetLeafEntry(pageID, index)
	return entry.keyOff, entry.keyLen, entry.valOff, entry.valLen
}

// GetIndexEntryOffset 获取索引条目的 key offset 和 child（用于跨包访问）
func (pa *PageAccessor) GetIndexEntryOffset(pageID uint32, index int) (keyOff, keyLen uint32, child uint32) {
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
