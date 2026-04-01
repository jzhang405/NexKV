// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSizeofPageHeader(t *testing.T) {
	// PageHeader 应该是 56 字节（8 字节对齐）
	// 字段布局：
	//   version(8) + prevPage(4) + nextPage(4) + extraChild(8) + count(2) + pageType(1) + deleted(1) + padding(4) + deleteEpoch(8) + refCount(4) + inQueue(4) + _pad(4) = 52
	//   向上对齐到 8 字节：56 字节
	// 修改记录 (2026-04-01): 添加 refCount 和 inQueue 支持 Reference Counting
	assert.Equal(t, 56, SizeofPageHeader, "PageHeader should be 56 bytes for 8-byte alignment")
}

func TestSizeofIndexEntry(t *testing.T) {
	// IndexEntry 应该是 16 字节
	// 字段布局：keyOff(4) + keyLen(4) + child(8) = 16
	assert.Equal(t, 16, SizeofIndexEntry, "IndexEntry should be 16 bytes")
}

func TestSizeofLeafEntry(t *testing.T) {
	// LeafEntry 应该是 16 字节
	assert.Equal(t, 16, SizeofLeafEntry, "LeafEntry should be 16 bytes")
}

func TestNodeRef(t *testing.T) {
	// 测试 NodeRef 创建
	ref := NewNodeRef(100, true)
	assert.Equal(t, uint32(100), ref.pageID)
	assert.True(t, ref.isLeaf)

	// 测试 IsValid
	assert.True(t, ref.IsValid())

	// 测试无效值
	invalidRef := NewNodeRef(0xFFFFFFFF, false)
	assert.False(t, invalidRef.IsValid())
}

func TestPageAccessor_InitPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 测试初始化索引页面
	pa.InitIndexPage(pageID, 1)
	header := pa.GetHeader(pageID)
	assert.Equal(t, uint8(PageTypeIndex), header.pageType)
	assert.Equal(t, uint16(0), header.count)
	assert.Equal(t, uint64(1), header.version)
	assert.True(t, pa.IsLeaf(pageID) == false)

	// 测试初始化叶子页面
	pageID2, err := pm.Alloc()
	require.NoError(t, err)
	pa.InitLeafPage(pageID2, 2)
	header2 := pa.GetHeader(pageID2)
	assert.Equal(t, uint8(PageTypeLeaf), header2.pageType)
	assert.Equal(t, uint16(0), header2.count)
	assert.Equal(t, uint64(2), header2.version)
	assert.True(t, pa.IsLeaf(pageID2))
}

func TestPageAccessor_InsertIndexEntry(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitIndexPage(pageID, 1)
	var dataEnd uint16 = 0

	// 插入第一个条目
	key1 := []byte("key001")
	child1 := uint32(100)
	err = pa.InsertIndexEntry(pageID, 0, key1, child1, &dataEnd)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, uint16(1), pa.GetCount(pageID))
	entry := pa.GetIndexEntry(pageID, 0)
	// entry.child 现在是编码后的 uint64 值，需要解码
	decodedChild, _ := DecodeChildWithVersion(entry.child)
	assert.Equal(t, uint32(100), decodedChild)
	assert.Equal(t, key1, pa.GetKey(pageID, entry.keyOff, entry.keyLen))

	// 插入第二个条目（在前面）
	key2 := []byte("key000")
	child2 := uint32(99)
	err = pa.InsertIndexEntry(pageID, 0, key2, child2, &dataEnd)
	require.NoError(t, err)

	// 验证顺序
	assert.Equal(t, uint16(2), pa.GetCount(pageID))
	entry0 := pa.GetIndexEntry(pageID, 0)
	decodedChild0, _ := DecodeChildWithVersion(entry0.child)
	assert.Equal(t, uint32(99), decodedChild0)
	entry1 := pa.GetIndexEntry(pageID, 1)
	decodedChild1, _ := DecodeChildWithVersion(entry1.child)
	assert.Equal(t, uint32(100), decodedChild1)
}

func TestPageAccessor_InsertLeafEntry(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitLeafPage(pageID, 1)
	var dataEnd uint16 = 0

	// 插入第一个条目
	key1 := []byte("name")
	value1 := []byte("Alice")
	err = pa.InsertLeafEntry(pageID, 0, key1, value1, &dataEnd)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, uint16(1), pa.GetCount(pageID))
	entry := pa.GetLeafEntry(pageID, 0)
	assert.Equal(t, key1, pa.GetKey(pageID, entry.keyOff, entry.keyLen))
	assert.Equal(t, value1, pa.GetValue(pageID, entry.valOff, entry.valLen))

	// 插入第二个条目
	key2 := []byte("age")
	value2 := []byte("30")
	err = pa.InsertLeafEntry(pageID, 1, key2, value2, &dataEnd)
	require.NoError(t, err)

	assert.Equal(t, uint16(2), pa.GetCount(pageID))
}

func TestPageAccessor_SearchKey(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)

	// 测试叶子页面搜索
	leafID, err := pm.Alloc()
	require.NoError(t, err)
	pa.InitLeafPage(leafID, 1)
	var dataEnd uint16 = 0

	keys := [][]byte{
		[]byte("apple"),
		[]byte("banana"),
		[]byte("cherry"),
	}
	values := [][]byte{
		[]byte("red"),
		[]byte("yellow"),
		[]byte("red"),
	}

	for i, key := range keys {
		err = pa.InsertLeafEntry(leafID, i, key, values[i], &dataEnd)
		require.NoError(t, err)
	}

	// 测试找到
	idx, found, _ := pa.SearchKey(leafID, []byte("banana"), true)
	assert.True(t, found)
	assert.Equal(t, 1, idx)
	entry := pa.GetLeafEntry(leafID, idx)
	assert.Equal(t, values[1], pa.GetValue(leafID, entry.valOff, entry.valLen))

	// 测试未找到
	idx, found, _ = pa.SearchKey(leafID, []byte("grape"), true)
	assert.False(t, found)
	assert.Equal(t, 3, idx) // 应该插入到位置 3

	// 测试索引页面搜索
	indexID, err := pm.Alloc()
	require.NoError(t, err)
	pa.InitIndexPage(indexID, 1)
	var dataIndexEnd uint16 = 0

	indexKeys := [][]byte{
		[]byte("key100"),
		[]byte("key200"),
		[]byte("key300"),
	}
	children := []uint32{10, 20, 30}

	for i, key := range indexKeys {
		err = pa.InsertIndexEntry(indexID, i, key, children[i], &dataIndexEnd)
		require.NoError(t, err)
	}

	// 测试找到
	idx, found, _ = pa.SearchKey(indexID, []byte("key200"), false)
	assert.True(t, found)
	assert.Equal(t, 1, idx)
	// GetChild 返回编码后的 uint64 值，需要解码
	encodedChild := pa.GetChild(indexID, idx)
	decodedChild, _ := DecodeChildWithVersion(encodedChild)
	assert.Equal(t, uint32(20), decodedChild)

	// 测试未找到
	idx, found, _ = pa.SearchKey(indexID, []byte("key250"), false)
	assert.False(t, found)
	assert.Equal(t, 2, idx) // 应该插入到位置 2
}

func TestPageAccessor_Version(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitLeafPage(pageID, 42)

	assert.Equal(t, uint64(42), pa.GetVersion(pageID))

	pa.SetVersion(pageID, 100)
	assert.Equal(t, uint64(100), pa.GetVersion(pageID))
}

func TestPageAccessor_LinkedList(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	page1, err := pm.Alloc()
	require.NoError(t, err)
	page2, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitLeafPage(page1, 1)
	pa.InitLeafPage(page2, 2)

	// 设置链表
	pa.SetNextPage(page1, page2)
	pa.SetPrevPage(page2, page1)

	assert.Equal(t, page2, pa.GetNextPage(page1))
	assert.Equal(t, page1, pa.GetPrevPage(page2))
}

func TestPageAccessor_PageFull(t *testing.T) {
	pm, err := NewPageManager(4 * PageSize) // 4 个页面
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitLeafPage(pageID, 1)
	var dataEnd uint16 = 0

	// 尝试填满页面
	// 4KB = 4096 bytes
	// PageHeader = 32 bytes
	// 每个 LeafEntry = 16 bytes
	// 剩余空间 = 4096 - 32 = 4064 bytes
	// 假设平均 key+value = 32 bytes
	// 大约可以插入 4064 / (16 + 32) = 84 个条目

	count := 0
	for {
		key := []byte{byte(count >> 8), byte(count & 0xFF)}
		value := make([]byte, 30) // 30 字节 value
		err = pa.InsertLeafEntry(pageID, count, key, value, &dataEnd)
		if err != nil {
			break
		}
		count++
	}

	// 应该能插入至少 80 个条目
	assert.GreaterOrEqual(t, count, 80, "should insert at least 80 entries")
}

func TestPageAccessor_GetChild(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitIndexPage(pageID, 1)
	var dataEnd uint16 = 0

	child1 := uint32(111)
	child2 := uint32(222)

	err = pa.InsertIndexEntry(pageID, 0, []byte("key1"), child1, &dataEnd)
	require.NoError(t, err)

	err = pa.InsertIndexEntry(pageID, 1, []byte("key2"), child2, &dataEnd)
	require.NoError(t, err)

	// GetChild 返回编码后的 uint64 值，需要解码
	encodedChild1 := pa.GetChild(pageID, 0)
	decodedChild1, _ := DecodeChildWithVersion(encodedChild1)
	assert.Equal(t, child1, decodedChild1)

	encodedChild2 := pa.GetChild(pageID, 1)
	decodedChild2, _ := DecodeChildWithVersion(encodedChild2)
	assert.Equal(t, child2, decodedChild2)

	// 修改子节点（SetChild 现在接受原始 pageID）
	newChild := uint32(999)
	pa.SetChild(pageID, 0, newChild)
	encodedNewChild := pa.GetChild(pageID, 0)
	decodedNewChild, _ := DecodeChildWithVersion(encodedNewChild)
	assert.Equal(t, newChild, decodedNewChild)
}

func TestPageAccessor_GetCount(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitLeafPage(pageID, 1)
	assert.Equal(t, uint16(0), pa.GetCount(pageID))

	var dataEnd uint16 = 0
	err = pa.InsertLeafEntry(pageID, 0, []byte("key"), []byte("value"), &dataEnd)
	require.NoError(t, err)

	assert.Equal(t, uint16(1), pa.GetCount(pageID))
}

func TestPageAccessor_GetDataEnd(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitLeafPage(pageID, 1)

	// 空页面应该返回 0
	dataEnd := pa.GetDataEnd(pageID)
	assert.Equal(t, uint16(0), dataEnd)

	// 插入第一个条目
	key := []byte("test-key")
	value := []byte("test-value")
	var dataEndParam uint16 = 0
	err = pa.InsertLeafEntry(pageID, 0, key, value, &dataEndParam)
	require.NoError(t, err)

	// 验证 GetDataEnd 返回正确的值
	expectedDataEnd := uint16(len(key) + len(value))
	actualDataEnd := pa.GetDataEnd(pageID)
	assert.Equal(t, expectedDataEnd, actualDataEnd)

	// 插入第二个条目
	key2 := []byte("another-key")
	value2 := []byte("another-value")
	err = pa.InsertLeafEntry(pageID, 1, key2, value2, &dataEndParam)
	require.NoError(t, err)

	// 验证 dataEnd 增加了
	expectedDataEnd = uint16(len(key) + len(value) + len(key2) + len(value2))
	actualDataEnd = pa.GetDataEnd(pageID)
	assert.Equal(t, expectedDataEnd, actualDataEnd)
}

func TestPageAccessor_GetDataEnd_IndexPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitIndexPage(pageID, 1)

	// 空页面应该返回 0
	dataEnd := pa.GetDataEnd(pageID)
	assert.Equal(t, uint16(0), dataEnd)

	// 插入第一个条目
	key := []byte("test-key")
	child := uint32(100)
	var dataEndParam uint16 = 0
	err = pa.InsertIndexEntry(pageID, 0, key, child, &dataEndParam)
	require.NoError(t, err)

	// 验证 GetDataEnd 返回正确的值
	expectedDataEnd := uint16(len(key))
	actualDataEnd := pa.GetDataEnd(pageID)
	assert.Equal(t, expectedDataEnd, actualDataEnd)

	// 插入第二个条目
	key2 := []byte("another-key")
	child2 := uint32(200)
	err = pa.InsertIndexEntry(pageID, 1, key2, child2, &dataEndParam)
	require.NoError(t, err)

	// 验证 dataEnd 增加了
	expectedDataEnd = uint16(len(key) + len(key2))
	actualDataEnd = pa.GetDataEnd(pageID)
	assert.Equal(t, expectedDataEnd, actualDataEnd)
}

func TestPageAccessor_GetSpaceUsage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa.InitLeafPage(pageID, 1)

	// 空页面应该只有 header
	usage := pa.GetSpaceUsage(pageID)
	expectedUsage := float64(SizeofPageHeader) / float64(PageSize)
	assert.InDelta(t, expectedUsage, usage, 0.001)

	// 插入一个条目
	key := []byte("key")
	value := []byte("value")
	var dataEndParam uint16 = 0
	err = pa.InsertLeafEntry(pageID, 0, key, value, &dataEndParam)
	require.NoError(t, err)

	// 验证空间使用率增加
	usage = pa.GetSpaceUsage(pageID)
	usedSpace := SizeofPageHeader + SizeofLeafEntry + len(key) + len(value)
	expectedUsage = float64(usedSpace) / float64(PageSize)
	assert.InDelta(t, expectedUsage, usage, 0.001)
}

func TestPageAccessor_CollectKVExcept(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化叶子页面并插入多个 KV 对
	pa.InitLeafPage(pageID, 1)
	keys := [][]byte{
		[]byte("key1"),
		[]byte("key2"),
		[]byte("key3"),
		[]byte("key4"),
		[]byte("key5"),
	}
	values := [][]byte{
		[]byte("value1"),
		[]byte("value2"),
		[]byte("value3"),
		[]byte("value4"),
		[]byte("value5"),
	}

	var dataEnd uint16 = 0
	for i := range keys {
		err := pa.InsertLeafEntry(pageID, i, keys[i], values[i], &dataEnd)
		require.NoError(t, err)
	}

	// 测试跳过中间的 key（跳过索引 2，即 "key3"）
	skipIdx := 2
	collectedKeys, collectedValues := pa.CollectKVExcept(pageID, skipIdx)

	// 验证收集到的 KV 对数量（应该是 4 个）
	assert.Equal(t, 4, len(collectedKeys))
	assert.Equal(t, 4, len(collectedValues))

	// 验证收集到的 KV 对内容（应该不包含 "key3"）
	expectedKeys := [][]byte{
		[]byte("key1"),
		[]byte("key2"),
		[]byte("key4"),
		[]byte("key5"),
	}
	expectedValues := [][]byte{
		[]byte("value1"),
		[]byte("value2"),
		[]byte("value4"),
		[]byte("value5"),
	}

	for i := range expectedKeys {
		assert.Equal(t, string(expectedKeys[i]), string(collectedKeys[i]))
		assert.Equal(t, string(expectedValues[i]), string(collectedValues[i]))
	}
}

func TestPageAccessor_CollectKVExcept_SkipFirst(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化叶子页面并插入多个 KV 对
	pa.InitLeafPage(pageID, 1)
	keys := [][]byte{
		[]byte("key1"),
		[]byte("key2"),
		[]byte("key3"),
	}
	values := [][]byte{
		[]byte("value1"),
		[]byte("value2"),
		[]byte("value3"),
	}

	var dataEnd uint16 = 0
	for i := range keys {
		err := pa.InsertLeafEntry(pageID, i, keys[i], values[i], &dataEnd)
		require.NoError(t, err)
	}

	// 测试跳过第一个 key（索引 0）
	collectedKeys, collectedValues := pa.CollectKVExcept(pageID, 0)

	// 验证收集到的 KV 对
	assert.Equal(t, 2, len(collectedKeys))
	assert.Equal(t, []byte("key2"), collectedKeys[0])
	assert.Equal(t, []byte("key3"), collectedKeys[1])
	assert.Equal(t, []byte("value2"), collectedValues[0])
	assert.Equal(t, []byte("value3"), collectedValues[1])
}

func TestPageAccessor_CollectKVExcept_SkipLast(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化叶子页面并插入多个 KV 对
	pa.InitLeafPage(pageID, 1)
	keys := [][]byte{
		[]byte("key1"),
		[]byte("key2"),
		[]byte("key3"),
	}
	values := [][]byte{
		[]byte("value1"),
		[]byte("value2"),
		[]byte("value3"),
	}

	var dataEnd uint16 = 0
	for i := range keys {
		err := pa.InsertLeafEntry(pageID, i, keys[i], values[i], &dataEnd)
		require.NoError(t, err)
	}

	// 测试跳过最后一个 key（索引 2）
	collectedKeys, collectedValues := pa.CollectKVExcept(pageID, 2)

	// 验证收集到的 KV 对
	assert.Equal(t, 2, len(collectedKeys))
	assert.Equal(t, []byte("key1"), collectedKeys[0])
	assert.Equal(t, []byte("key2"), collectedKeys[1])
	assert.Equal(t, []byte("value1"), collectedValues[0])
	assert.Equal(t, []byte("value2"), collectedValues[1])
}

func TestPageAccessor_CollectKVExcept_EmptyPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化空页面
	pa.InitLeafPage(pageID, 1)

	// 测试空页面（应该返回空切片）
	collectedKeys, collectedValues := pa.CollectKVExcept(pageID, 0)

	assert.Equal(t, 0, len(collectedKeys))
	assert.Equal(t, 0, len(collectedValues))
}

// Phase 3.5: ValidatePage 防御性验证测试

func TestPageAccessor_ValidatePage_ValidIndexPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化有效的索引页面
	pa.InitIndexPage(pageID, 0)

	// 插入有效的 keys 和 children
	keys := [][]byte{[]byte("key1"), []byte("key2"), []byte("key3")}
	children := []uint32{10, 20, 30, 40} // N+1 children

	var dataEnd uint16
	for i := range keys {
		err := pa.InsertIndexEntry(pageID, i, keys[i], children[i], &dataEnd)
		require.NoError(t, err)
	}

	// 设置 extraChild
	header := pa.GetHeader(pageID)
	header.extraChild = EncodeChildWithVersion(children[3], 0)

	// 验证页面应该通过
	err = pa.ValidatePage(pageID)
	assert.NoError(t, err)
}

func TestPageAccessor_ValidatePage_EmptyPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化空索引页面
	pa.InitIndexPage(pageID, 0)

	// 空页面验证应该通过（count=0 时不检查 extraChild）
	err = pa.ValidatePage(pageID)
	assert.NoError(t, err)
}

func TestPageAccessor_ValidatePage_ChildZero(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化索引页面
	pa.InitIndexPage(pageID, 0)

	// 先插入正常的 keys 和 children
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	children := []uint32{10, 20}

	var dataEnd uint16
	for i := range keys {
		err := pa.InsertIndexEntry(pageID, i, keys[i], children[i], &dataEnd)
		require.NoError(t, err)
	}

	// 手动设置 extraChild
	header := pa.GetHeader(pageID)
	header.extraChild = EncodeChildWithVersion(children[1], 0)

	// 验证 ValidatePage 能检测到 child=0（通过直接修改 entry.child）
	// 注意：这里需要手动设置一个 child=0 来测试 ValidatePage
	// 因为 InsertIndexEntry 现在会拒绝 child=0
	entry := pa.GetIndexEntry(pageID, 0)
	originalChild := entry.child // 保存原始值
	entry.child = 0              // 模拟 child=0 的错误状态

	// 验证页面应该失败（child=0）
	err = pa.ValidatePage(pageID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "child=0")

	// 恢复原始值
	entry.child = originalChild
}

func TestPageAccessor_ValidatePage_ExtraChildZero(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化索引页面
	pa.InitIndexPage(pageID, 0)

	// 插入有效的 keys 和 children
	keys := [][]byte{[]byte("key1"), []byte("key2"), []byte("key3")}
	children := []uint32{10, 20, 30}

	var dataEnd uint16
	for i := range keys {
		err := pa.InsertIndexEntry(pageID, i, keys[i], children[i], &dataEnd)
		require.NoError(t, err)
	}

	// 设置 extraChild=0（错误）
	header := pa.GetHeader(pageID)
	header.extraChild = 0 // 错误：count > 0 时 extraChild 不能为 0

	// 验证页面应该失败（extraChild=0）
	err = pa.ValidatePage(pageID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "extraChild=0")
}

// Phase 6: CheckPageInvariants 不变式检查测试

func TestPageAccessor_CheckPageInvariants_ValidPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化有效的索引页面
	pa.InitIndexPage(pageID, 0)

	// 插入有效的 keys 和 children（keys 升序）
	keys := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	children := []uint32{10, 20, 30, 40} // N+1 children

	var dataEnd uint16
	for i := range keys {
		err := pa.InsertIndexEntry(pageID, i, keys[i], children[i], &dataEnd)
		require.NoError(t, err)
	}

	// 设置 extraChild
	header := pa.GetHeader(pageID)
	header.extraChild = EncodeChildWithVersion(children[3], 0)

	// CheckPageInvariants 应该通过
	err = pa.CheckPageInvariants(pageID)
	assert.NoError(t, err)
}

func TestPageAccessor_CheckPageInvariants_KeysNotSorted(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化索引页面
	pa.InitIndexPage(pageID, 0)

	// 先插入正常数据
	keys := [][]byte{[]byte("a"), []byte("b")}
	children := []uint32{10, 20, 30}
	var dataEnd uint16
	for i := range keys {
		err := pa.InsertIndexEntry(pageID, i, keys[i], children[i], &dataEnd)
		require.NoError(t, err)
	}

	header := pa.GetHeader(pageID)
	header.extraChild = EncodeChildWithVersion(children[2], 0)

	// 手动破坏 key 有序性：交换两个 key 的内容
	entry0 := pa.GetIndexEntry(pageID, 0)
	entry1 := pa.GetIndexEntry(pageID, 1)
	// 交换 keyOff 来破坏有序性（让后面的 key 指向前面）
	entry0.keyOff, entry1.keyOff = entry1.keyOff, entry0.keyOff

	// CheckPageInvariants 应该失败（keys not sorted）
	err = pa.CheckPageInvariants(pageID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "keys not sorted")

	// 恢复（不做清理，因为这个测试页面会被释放）
}

func TestPageAccessor_CheckPageInvariants_ChildZero(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化索引页面
	pa.InitIndexPage(pageID, 0)

	// 插入正常数据
	keys := [][]byte{[]byte("a"), []byte("b")}
	children := []uint32{10, 20}
	var dataEnd uint16
	for i := range keys {
		err := pa.InsertIndexEntry(pageID, i, keys[i], children[i], &dataEnd)
		require.NoError(t, err)
	}

	header := pa.GetHeader(pageID)
	header.extraChild = EncodeChildWithVersion(children[1], 0)

	// 手动设置 child=0
	entry := pa.GetIndexEntry(pageID, 0)
	entry.child = 0

	// CheckPageInvariants 应该失败（child=0）
	err = pa.CheckPageInvariants(pageID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "child=0")
}

// Phase 4: 边界测试

func TestBulkInitIndexFromSource_SelfLoop(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	srcPageID, err := pm.Alloc()
	require.NoError(t, err)
	dstPageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化源索引页面
	pa.InitIndexPage(srcPageID, 0)

	// 插入 keys 和 children
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	children := []uint32{10, 20} // 正常 children

	var dataEnd uint16
	for i := range keys {
		err := pa.InsertIndexEntry(srcPageID, i, keys[i], children[i], &dataEnd)
		require.NoError(t, err)
	}

	// 设置 extraChild
	srcHeader := pa.GetHeader(srcPageID)
	srcHeader.extraChild = EncodeChildWithVersion(30, 0)

	// 测试正常 BulkInit（不应该出错）
	_, err = pa.BulkInitIndexFromSource(srcPageID, dstPageID, 0, 2, srcHeader.extraChild)
	assert.NoError(t, err)
}

func TestAdvanceDelayedFreeList_EpochDelay(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)

	t.Run("无引用直接回收", func(t *testing.T) {
		pageID, err := pm.Alloc()
		require.NoError(t, err)
		pa.InitLeafPage(pageID, 1)

		// refCount==0 时 Free 直接进入 freeList，但仍设 deleted=1
		// 防止 SearchChild 在页面被 Alloc 回收前误读脏数据
		err = pm.Free(pageID)
		require.NoError(t, err)

		ptr := pm.PageIDToPtr(pageID)
		header := (*PageHeader)(ptr)
		assert.Equal(t, uint8(1), header.deleted, "Free 后应设 deleted=1（即使 refCount==0）")
		assert.Equal(t, 1, pm.GetFreeListSize(), "应直接进入 freeList")
		assert.Equal(t, 0, pm.GetDelayedFreeListSize(), "不应进入 delayedFreeList")
	})

	t.Run("有引用延迟回收", func(t *testing.T) {
		// 跳过 pageID=0（保留页面），确保 AddRef/Free 生效
		skipID, _ := pm.Alloc()
		_ = skipID

		pageID, err := pm.Alloc()
		require.NoError(t, err)
		require.NotEqual(t, uint32(0), pageID, "pageID 不应为 0")
		pa.InitLeafPage(pageID, 1)

		// 模拟有活跃引用
		pm.AddRef(pageID)
		require.Equal(t, int32(1), pm.GetRefCount(pageID), "AddRef 后 refCount 应为 1")
		currentEpochBeforeFree := pm.currentEpoch.Load()

		// refCount>0 时 Free 标记 deleted=1，进入 delayedFreeList
		err = pm.Free(pageID)
		require.NoError(t, err)

		ptr := pm.PageIDToPtr(pageID)
		header := (*PageHeader)(ptr)
		assert.Equal(t, uint8(1), header.deleted, "有引用页面应标记为已删除")
		assert.Equal(t, currentEpochBeforeFree, header.deleteEpoch, "deleteEpoch 应等于释放时的 epoch")

		// 先释放引用，再推进 epoch
		pm.Release(pageID)
		require.Equal(t, int32(0), pm.GetRefCount(pageID), "Release 后 refCount 应为 0")

		// 推进 6 个 epoch，页面应从 delayedFreeList 移到 freeList
		for i := 0; i < 6; i++ {
			pm.AdvanceEpoch()
		}
		assert.Equal(t, 1, pm.GetFreeListSize(), "释放引用并推进 epoch 后应可回收")
	})
}

func TestBulkInitIndexFromSource_InvalidRange(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	pa := NewPageAccessor(pm)
	srcPageID, err := pm.Alloc()
	require.NoError(t, err)
	dstPageID, err := pm.Alloc()
	require.NoError(t, err)

	// 初始化源索引页面
	pa.InitIndexPage(srcPageID, 0)

	// 插入一些 keys
	keys := [][]byte{[]byte("key1"), []byte("key2")}
	var dataEnd uint16
	for i := range keys {
		err := pa.InsertIndexEntry(srcPageID, i, keys[i], uint32(i+10), &dataEnd)
		require.NoError(t, err)
	}

	// 测试无效范围：startIdx > endIdx
	_, err = pa.BulkInitIndexFromSource(srcPageID, dstPageID, 2, 1, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid range")

	// 测试无效范围：startIdx < 0
	_, err = pa.BulkInitIndexFromSource(srcPageID, dstPageID, -1, 2, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid range")
}
