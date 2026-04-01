// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOffHeapAdapter_AdapterBasic 测试适配器基本功能
func TestOffHeapAdapter_AdapterBasic(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize) // 16KB
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 测试分配叶子页面（pageID 从 0 开始，所以 0 是有效的）
	leafPageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)
	// PageManager 从 0 开始分配，所以 0 是有效的第一个页面
	assert.Equal(t, model.PageID(0), leafPageID)

	// 测试分配索引页面（第二个页面，ID 应该是 1）
	indexPageID, err := adapter.AllocIndexPage()
	require.NoError(t, err)
	assert.Equal(t, model.PageID(1), indexPageID)

	// 测试释放页面
	err = adapter.FreePage(leafPageID)
	require.NoError(t, err)
}

// TestOffHeapAdapter_InsertAndGet 测试插入和获取
func TestOffHeapAdapter_InsertAndGet(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 分配叶子页面
	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

	// 插入 KV 对
	key := []byte("hello")
	value := []byte("world")
	newPageID, splitRequired, _, err := adapter.InsertToOffHeap(pageID, key, value)
	require.NoError(t, err)
	assert.False(t, splitRequired, "不应该需要分页")
	assert.Equal(t, pageID, newPageID, "页面ID 应该不变")

	// 获取 value
	retrieved, found, err := adapter.GetFromOffHeap(pageID, key)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, value, retrieved)
}

// TestOffHeapAdapter_MultipleInserts 测试多次插入
func TestOffHeapAdapter_MultipleInserts(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 分配叶子页面
	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

	// 插入多个 KV 对
	keys := [][]byte{
		[]byte("apple"),
		[]byte("banana"),
		[]byte("cherry"),
		[]byte("date"),
	}
	values := [][]byte{
		[]byte("1"),
		[]byte("2"),
		[]byte("3"),
		[]byte("4"),
	}

	for i, key := range keys {
		value := values[i]
		newPageID, splitRequired, _, err := adapter.InsertToOffHeap(pageID, key, value)
		require.NoError(t, err)
		assert.False(t, splitRequired, "不应该需要分页")
		pageID = newPageID
	}

	// 验证所有 KV 对
	for i, key := range keys {
		expected := values[i]
		retrieved, found, err := adapter.GetFromOffHeap(pageID, key)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, expected, retrieved)
	}

	// 验证 key 数量
	count := adapter.NumKeys(pageID)
	assert.Equal(t, len(keys), count)
}

// TestOffHeapAdapter_Update 测试更新现有 key
func TestOffHeapAdapter_Update(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 分配叶子页面
	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

	// 插入 KV
	key := []byte("test")
	value1 := []byte("value1")
	pageID, _, _, err = adapter.InsertToOffHeap(pageID, key, value1)
	require.NoError(t, err)

	// 更新 KV
	value2 := []byte("value2")
	newPageID, splitRequired, _, err := adapter.InsertToOffHeap(pageID, key, value2)
	require.NoError(t, err)
	assert.False(t, splitRequired)

	// 验证更新后的值
	retrieved, found, err := adapter.GetFromOffHeap(newPageID, key)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, value2, retrieved)
	assert.NotEqual(t, value1, retrieved)
}

// TestOffHeapAdapter_Split 测试页面分割
func TestOffHeapAdapter_Split(t *testing.T) {
	pm, err := offheap.NewPageManager(8 * offheap.PageSize) // 32KB
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 分配叶子页面
	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

	// 插入大量 KV 直到页面满
	for i := 0; i < 100; i++ {
		key := []byte{byte(i >> 8), byte(i & 0xFF)}
		value := make([]byte, 20)
		newPageID, splitRequired, _, err := adapter.InsertToOffHeap(pageID, key, value)
		require.NoError(t, err)

		if splitRequired {
			// 执行分割
			leftID, rightID, splitKey, err := adapter.SplitOffHeapLeafPage(newPageID)
			require.NoError(t, err)

			// 验证分割结果
			assert.NotEqual(t, model.PageID(0), leftID)
			assert.NotEqual(t, model.PageID(0), rightID)
			assert.NotNil(t, splitKey)

			// 验证链表指针
			prev, next := adapter.GetPrevNextPage(leftID)
			assert.Equal(t, rightID, next, "left 的 next 应该是 right")
			assert.Equal(t, model.PageID(0xFFFFFFFF), prev, "left 的 prev 应该是空")

			prev, next = adapter.GetPrevNextPage(rightID)
			assert.Equal(t, leftID, prev, "right 的 prev 应该是 left")
			assert.Equal(t, model.PageID(0xFFFFFFFF), next, "right 的 next 应该是空")

			// 验证左右页面的 KV 数量
			leftCount := adapter.NumKeys(leftID)
			rightCount := adapter.NumKeys(rightID)
			assert.Equal(t, 100, leftCount+rightCount, "分割后总 KV 数应该不变")

			break
		}

		pageID = newPageID
	}
}

// TestOffHeapAdapter_Verify 测试页面验证
func TestOffHeapAdapter_Verify(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 分配叶子页面并插入数据
	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

	keys := [][]byte{
		[]byte("a"),
		[]byte("b"),
		[]byte("c"),
	}
	values := [][]byte{
		[]byte("1"),
		[]byte("2"),
		[]byte("3"),
	}

	for i, key := range keys {
		value := values[i]
		pageID, _, _, err = adapter.InsertToOffHeap(pageID, key, value)
		require.NoError(t, err)
	}

	// 验证页面
	valid, err := adapter.VerifyOffHeapPage(pageID)
	require.NoError(t, err)
	assert.True(t, valid, "页面应该有效")
}

// TestOffHeapAdapter_Clone 测试页面克隆
func TestOffHeapAdapter_Clone(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 分配叶子页面并插入数据
	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

	key := []byte("test")
	value := []byte("value")
	pageID, _, _, err = adapter.InsertToOffHeap(pageID, key, value)
	require.NoError(t, err)

	// 克隆页面
	clonedPageID, err := adapter.CloneOffHeapPage(pageID, true)
	require.NoError(t, err)

	// 验证克隆后的页面包含相同数据
	retrieved, found, err := adapter.GetFromOffHeap(clonedPageID, key)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, value, retrieved)

	// 验证版本号增加
	originalVersion := adapter.GetPageVersion(pageID)
	clonedVersion := adapter.GetPageVersion(clonedPageID)
	assert.Equal(t, originalVersion+1, clonedVersion, "克隆版本号应该增加")
}

// TestOffHeapAdapter_MaterializeFromLeafPage 测试从 LeafPage 物化
func TestOffHeapAdapter_MaterializeFromLeafPage(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 创建 Go 堆 LeafPage
	leafPage := NewLeafPage(100)
	leafPage.keys = [][]byte{
		[]byte("key1"),
		[]byte("key2"),
	}
	leafPage.values = [][]byte{
		[]byte("value1"),
		[]byte("value2"),
	}

	// 物化到 Off-Heap
	offHeapPageID, err := adapter.MaterializeLeafPage(leafPage)
	require.NoError(t, err)

	// 验证物化结果
	retrieved1, found1, _ := adapter.GetFromOffHeap(offHeapPageID, []byte("key1"))
	assert.True(t, found1)
	assert.Equal(t, []byte("value1"), retrieved1)

	retrieved2, found2, _ := adapter.GetFromOffHeap(offHeapPageID, []byte("key2"))
	assert.True(t, found2)
	assert.Equal(t, []byte("value2"), retrieved2)
}

// TestOffHeapAdapter_DeleteFromLeafPage 测试从叶子页面删除 key
func TestOffHeapAdapter_DeleteFromLeafPage(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 分配叶子页面并插入多个 KV 对
	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

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

	for i, key := range keys {
		value := values[i]
		pageID, _, _, err = adapter.InsertToOffHeap(pageID, key, value)
		require.NoError(t, err)
	}

	// 删除中间的 key（"key2"）
	deleteKey := []byte("key2")
	newPageID, err := adapter.DeleteFromLeafPage(pageID, deleteKey)
	require.NoError(t, err)

	// 验证新页面 ID 不同（COW 语义）
	assert.NotEqual(t, pageID, newPageID)

	// 验证删除的 key 不存在
	_, found, _ := adapter.GetFromOffHeap(newPageID, deleteKey)
	assert.False(t, found)

	// 验证其他 key 仍然存在
	retrieved1, found1, _ := adapter.GetFromOffHeap(newPageID, []byte("key1"))
	assert.True(t, found1)
	assert.Equal(t, []byte("value1"), retrieved1)

	retrieved3, found3, _ := adapter.GetFromOffHeap(newPageID, []byte("key3"))
	assert.True(t, found3)
	assert.Equal(t, []byte("value3"), retrieved3)

	// 验证 key 数量减少
	count := adapter.NumKeys(newPageID)
	assert.Equal(t, 2, count)
}

// TestOffHeapAdapter_DeleteFromLeafPage_DeleteFirst 测试删除第一个 key
func TestOffHeapAdapter_DeleteFromLeafPage_DeleteFirst(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

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

	for i, key := range keys {
		value := values[i]
		pageID, _, _, err = adapter.InsertToOffHeap(pageID, key, value)
		require.NoError(t, err)
	}

	// 删除第一个 key
	newPageID, err := adapter.DeleteFromLeafPage(pageID, []byte("key1"))
	require.NoError(t, err)

	// 验证删除结果
	_, found1, _ := adapter.GetFromOffHeap(newPageID, []byte("key1"))
	assert.False(t, found1)

	retrieved2, found2, _ := adapter.GetFromOffHeap(newPageID, []byte("key2"))
	assert.True(t, found2)
	assert.Equal(t, []byte("value2"), retrieved2)

	retrieved3, found3, _ := adapter.GetFromOffHeap(newPageID, []byte("key3"))
	assert.True(t, found3)
	assert.Equal(t, []byte("value3"), retrieved3)
}

// TestOffHeapAdapter_DeleteFromLeafPage_DeleteLast 测试删除最后一个 key
func TestOffHeapAdapter_DeleteFromLeafPage_DeleteLast(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

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

	for i, key := range keys {
		value := values[i]
		pageID, _, _, err = adapter.InsertToOffHeap(pageID, key, value)
		require.NoError(t, err)
	}

	// 删除最后一个 key
	newPageID, err := adapter.DeleteFromLeafPage(pageID, []byte("key3"))
	require.NoError(t, err)

	// 验证删除结果
	retrieved1, found1, _ := adapter.GetFromOffHeap(newPageID, []byte("key1"))
	assert.True(t, found1)
	assert.Equal(t, []byte("value1"), retrieved1)

	retrieved2, found2, _ := adapter.GetFromOffHeap(newPageID, []byte("key2"))
	assert.True(t, found2)
	assert.Equal(t, []byte("value2"), retrieved2)

	_, found3, _ := adapter.GetFromOffHeap(newPageID, []byte("key3"))
	assert.False(t, found3)
}

// TestOffHeapAdapter_DeleteFromLeafPage_DeleteNonExistent 测试删除不存在的 key
func TestOffHeapAdapter_DeleteFromLeafPage_DeleteNonExistent(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	pageID, err := adapter.AllocLeafPage()
	require.NoError(t, err)

	// 插入一个 key
	key := []byte("key1")
	value := []byte("value1")
	pageID, _, _, err = adapter.InsertToOffHeap(pageID, key, value)
	require.NoError(t, err)

	// 尝试删除不存在的 key
	_, err = adapter.DeleteFromLeafPage(pageID, []byte("nonexistent"))
	assert.Error(t, err)
	assert.ErrorIs(t, ErrKeyNotFound, err)
}

// TestOffHeapAdapter_UpdateChildIndex 测试更新父节点的 child 指针
func TestOffHeapAdapter_UpdateChildIndex(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 创建父页面（索引页面）
	parentPageID, err := adapter.AllocIndexPage()
	require.NoError(t, err)

	// 在父页面中插入 key-child 对（需要 N+1 children）
	keys := [][]byte{
		[]byte("key1"),
		[]byte("key2"),
		[]byte("key3"),
	}
	children := []model.PageID{
		10,
		20,
		30,
		40, // extraChild (N+1 child)
	}

	for i, key := range keys {
		err := adapter.InsertIndexEntry(parentPageID, i, key, children[i])
		require.NoError(t, err)
	}

	// 设置 extraChild（索引为 len(keys)，即 N+1 child）
	// 使用 SetChild 方法，index == count 时会设置 extraChild
	pa := offheap.NewPageAccessor(pm)
	pa.SetChild(uint32(parentPageID), len(keys), uint32(children[3]))

	// 更新索引 1 的 child 指针（从 20 改为 25）
	newChildPageID := model.PageID(25)
	newParentPageID, err := adapter.UpdateChildIndex(parentPageID, 1, newChildPageID)
	require.NoError(t, err)

	// 验证新页面 ID 不同（COW 语义）
	assert.NotEqual(t, parentPageID, newParentPageID)

	// 验证 child 指针已更新
	updatedChild, err := adapter.GetChild(newParentPageID, 1)
	require.NoError(t, err)
	assert.Equal(t, newChildPageID, updatedChild)

	// 验证其他 child 指针未改变
	child0, _ := adapter.GetChild(newParentPageID, 0)
	assert.Equal(t, model.PageID(10), child0)

	child2, _ := adapter.GetChild(newParentPageID, 2)
	assert.Equal(t, model.PageID(30), child2)
}

// TestOffHeapAdapter_UpdateChildIndex_ExtraChild 测试更新 extraChild（N+1 child）
func TestOffHeapAdapter_UpdateChildIndex_ExtraChild(t *testing.T) {
	pm, err := offheap.NewPageManager(4 * offheap.PageSize)
	require.NoError(t, err)
	defer pm.Close()

	adapter := NewOffHeapAdapter(pm)

	// 创建父页面
	parentPageID, err := adapter.AllocIndexPage()
	require.NoError(t, err)

	// 插入 2 个 key-child 对
	keys := [][]byte{
		[]byte("key1"),
		[]byte("key2"),
	}
	children := []model.PageID{
		10,
		20,
	}

	for i, key := range keys {
		err := adapter.InsertIndexEntry(parentPageID, i, key, children[i])
		require.NoError(t, err)
	}

	// 更新 extraChild（索引 2，即 N+1 child）
	newChildPageID := model.PageID(99)
	newParentPageID, err := adapter.UpdateChildIndex(parentPageID, 2, newChildPageID)
	require.NoError(t, err)

	// 验证 extraChild 已更新
	updatedChild, err := adapter.GetChild(newParentPageID, 2)
	require.NoError(t, err)
	assert.Equal(t, newChildPageID, updatedChild)

	// 验证普通 child 未改变
	child0, _ := adapter.GetChild(newParentPageID, 0)
	assert.Equal(t, model.PageID(10), child0)

	child1, _ := adapter.GetChild(newParentPageID, 1)
	assert.Equal(t, model.PageID(20), child1)
}
