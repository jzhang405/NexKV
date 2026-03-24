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
