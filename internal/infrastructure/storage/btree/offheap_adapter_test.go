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
	newPageID, splitRequired, err := adapter.InsertToOffHeap(pageID, key, value)
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
		newPageID, splitRequired, err := adapter.InsertToOffHeap(pageID, key, value)
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
	pageID, _, err = adapter.InsertToOffHeap(pageID, key, value1)
	require.NoError(t, err)

	// 更新 KV
	value2 := []byte("value2")
	newPageID, splitRequired, err := adapter.InsertToOffHeap(pageID, key, value2)
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
		newPageID, splitRequired, err := adapter.InsertToOffHeap(pageID, key, value)
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
		pageID, _, err = adapter.InsertToOffHeap(pageID, key, value)
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
	pageID, _, err = adapter.InsertToOffHeap(pageID, key, value)
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
