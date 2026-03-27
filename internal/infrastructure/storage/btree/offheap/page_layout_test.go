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
	// PageHeader 应该是 32 字节（Cache Line 对齐）
	// 字段布局：version(8) + prevPage(4) + nextPage(4) + count(2) + pageType(1) + _pad(13) = 32
	assert.Equal(t, 32, SizeofPageHeader, "PageHeader should be 32 bytes for cache line alignment")
}

func TestSizeofIndexEntry(t *testing.T) {
	// IndexEntry 应该是 12 字节
	assert.Equal(t, 12, SizeofIndexEntry, "IndexEntry should be 12 bytes")
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
	assert.Equal(t, uint32(100), entry.child)
	assert.Equal(t, key1, pa.GetKey(pageID, entry.keyOff, entry.keyLen))

	// 插入第二个条目（在前面）
	key2 := []byte("key000")
	child2 := uint32(99)
	err = pa.InsertIndexEntry(pageID, 0, key2, child2, &dataEnd)
	require.NoError(t, err)

	// 验证顺序
	assert.Equal(t, uint16(2), pa.GetCount(pageID))
	entry0 := pa.GetIndexEntry(pageID, 0)
	assert.Equal(t, uint32(99), entry0.child)
	entry1 := pa.GetIndexEntry(pageID, 1)
	assert.Equal(t, uint32(100), entry1.child)
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
	idx, found := pa.SearchKey(leafID, []byte("banana"), true)
	assert.True(t, found)
	assert.Equal(t, 1, idx)
	entry := pa.GetLeafEntry(leafID, idx)
	assert.Equal(t, values[1], pa.GetValue(leafID, entry.valOff, entry.valLen))

	// 测试未找到
	idx, found = pa.SearchKey(leafID, []byte("grape"), true)
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
	idx, found = pa.SearchKey(indexID, []byte("key200"), false)
	assert.True(t, found)
	assert.Equal(t, 1, idx)
	assert.Equal(t, uint32(20), pa.GetChild(indexID, idx))

	// 测试未找到
	idx, found = pa.SearchKey(indexID, []byte("key250"), false)
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

	assert.Equal(t, child1, pa.GetChild(pageID, 0))
	assert.Equal(t, child2, pa.GetChild(pageID, 1))

	// 修改子节点
	pa.SetChild(pageID, 0, 999)
	assert.Equal(t, uint32(999), pa.GetChild(pageID, 0))
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
