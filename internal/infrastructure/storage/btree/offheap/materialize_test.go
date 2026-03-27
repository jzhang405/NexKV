// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOffHeapMaterializer_MaterializePageFromBytes(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 准备测试数据
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

	// 物化到页面
	_, err = m.MaterializePageFromBytes(pageID, keys, values)
	require.NoError(t, err)

	// 验证页面内容
	assert.True(t, m.VerifyPage(pageID, keys))

	// 验证快照
	snapshotKeys := m.GetPageSnapshot(pageID)
	assert.Equal(t, keys, snapshotKeys)

	snapshotValues := m.GetValueSnapshot(pageID)
	assert.Equal(t, values, snapshotValues)
}

func TestOffHeapMaterializer_MaterializeIndexPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 准备测试数据
	keys := [][]byte{
		[]byte("key100"),
		[]byte("key200"),
		[]byte("key300"),
	}
	children := []uint32{10, 20, 30}

	// 物化到页面
	_, err = m.MaterializeIndexPageFromBytes(pageID, keys, children)
	require.NoError(t, err)

	// 验证
	assert.Equal(t, uint16(3), m.pa.GetCount(pageID))

	// 验证 keys
	for i, key := range keys {
		entry := m.pa.GetIndexEntry(pageID, i)
		pageKey := m.pa.GetKey(pageID, entry.keyOff, entry.keyLen)
		assert.Equal(t, key, pageKey)
		// entry.child 现在是编码后的 uint64 值，需要解码
		decodedChild, _ := DecodeChildWithVersion(entry.child)
		assert.Equal(t, children[i], decodedChild)
	}
}

func TestOffHeapMaterializer_BinarySearchInPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 准备测试数据
	keys := [][]byte{
		[]byte("apple"),
		[]byte("banana"),
		[]byte("cherry"),
		[]byte("date"),
		[]byte("elderberry"),
	}
	values := [][]byte{
		[]byte("red"),
		[]byte("yellow"),
		[]byte("red"),
		[]byte("brown"),
		[]byte("purple"),
	}

	_, err = m.MaterializePageFromBytes(pageID, keys, values)
	require.NoError(t, err)

	// 测试查找存在的 key
	idx, found, value := m.BinarySearchInPage(pageID, []byte("cherry"))
	assert.True(t, found)
	assert.Equal(t, 2, idx)
	assert.Equal(t, []byte("red"), value)

	// 测试查找不存在的 key
	// blueberry < cherry，所以应该插入到位置 2
	idx, found, _ = m.BinarySearchInPage(pageID, []byte("blueberry"))
	assert.False(t, found)
	assert.Equal(t, 2, idx) // blueberry 应该插入到 banana (1) 和 cherry (2) 之间

	// 测试查找第一个 key
	idx, found, value = m.BinarySearchInPage(pageID, []byte("apple"))
	assert.True(t, found)
	assert.Equal(t, 0, idx)
	assert.Equal(t, []byte("red"), value)

	// 测试查找最后一个 key
	idx, found, value = m.BinarySearchInPage(pageID, []byte("elderberry"))
	assert.True(t, found)
	assert.Equal(t, 4, idx)
	assert.Equal(t, []byte("purple"), value)
}

func TestOffHeapMaterializer_ClonePageToBytes(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 准备测试数据
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

	_, err = m.MaterializePageFromBytes(pageID, keys, values)
	require.NoError(t, err)

	// 克隆页面
	copiedKeys, copiedValues := m.ClonePageToBytes(pageID)

	// 验证克隆结果
	assert.Equal(t, keys, copiedKeys)
	assert.Equal(t, values, copiedValues)

	// 修改克隆应该不影响原页面
	copiedKeys[0][0] = 'X'
	copiedValues[0][0] = 'Y'

	// 验证原页面未改变
	originalKeys := m.GetPageSnapshot(pageID)
	assert.Equal(t, byte('k'), originalKeys[0][0]) // 应该还是 'k'
}

func TestOffHeapMaterializer_EstimateSpaceUsage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 空页面
	used, free, count := m.EstimateSpaceUsage(pageID)
	assert.Equal(t, 0, count)
	assert.Equal(t, 0, used)
	assert.Equal(t, PageSize-SizeofPageHeader, free)

	// 添加一些数据
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

	_, err = m.MaterializePageFromBytes(pageID, keys, values)
	require.NoError(t, err)

	used, free, count = m.EstimateSpaceUsage(pageID)
	assert.Equal(t, 3, count)
	assert.Greater(t, used, 0)
	assert.Less(t, free, PageSize-SizeofPageHeader)
}

func TestOffHeapMaterializer_VerifyPage(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 准备测试数据
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

	_, err = m.MaterializePageFromBytes(pageID, keys, values)
	require.NoError(t, err)

	// 验证正确
	assert.True(t, m.VerifyPage(pageID, keys))

	// 验证错误的 keys
	wrongKeys := [][]byte{
		[]byte("a"),
		[]byte("x"), // 错误
		[]byte("c"),
	}
	assert.False(t, m.VerifyPage(pageID, wrongKeys))

	// 验证数量不匹配
	shortKeys := [][]byte{
		[]byte("a"),
		[]byte("b"),
	}
	assert.False(t, m.VerifyPage(pageID, shortKeys))
}

func TestOffHeapMaterializer_LargeDataset(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 创建大量数据（减少到 80，确保不会超出页面大小）
	const count = 80
	keys := make([][]byte, count)
	values := make([][]byte, count)

	for i := range count {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		values[i] = make([]byte, 30) // 30 字节 value
	}

	// 物化
	_, err = m.MaterializePageFromBytes(pageID, keys, values)
	require.NoError(t, err)

	// 验证
	assert.True(t, m.VerifyPage(pageID, keys))
	assert.Equal(t, count, int(m.pa.GetCount(pageID)))

	// 测试查找所有 keys
	for i, key := range keys {
		idx, found, value := m.BinarySearchInPage(pageID, key)
		assert.True(t, found)
		assert.Equal(t, i, idx)
		assert.Len(t, value, 30)
	}
}

func TestOffHeapMaterializer_PageFull(t *testing.T) {
	pm, err := NewPageManager(4 * PageSize)
	require.NoError(t, err)
	defer pm.Close()

	m := NewOffHeapMaterializer(pm)
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 尝试填满页面
	count := 0
	keys := [][]byte{}
	values := [][]byte{}

	for {
		key := []byte{byte(count >> 8), byte(count & 0xFF)}
		value := make([]byte, 30)

		// 尝试插入
		err = m.pa.InsertLeafEntry(pageID, count, key, value, new(uint16))
		if err != nil {
			break
		}

		keys = append(keys, key)
		values = append(values, value)
		count++
	}

	// 应该能插入相当数量的条目
	assert.Greater(t, count, 80, "should insert at least 80 entries")

	// 验证所有数据
	if count > 0 {
		_, err = m.MaterializePageFromBytes(pageID, keys, values)
		if err == nil {
			assert.True(t, m.VerifyPage(pageID, keys))
		}
	}
}
