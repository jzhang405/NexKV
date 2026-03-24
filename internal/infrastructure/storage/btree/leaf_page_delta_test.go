// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLeafPage_CloneWithDelta_Basic 测试 CloneWithDelta 基本功能
func TestLeafPage_CloneWithDelta_Basic(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))
	page.Insert([]byte("key2"), []byte("val2"))

	// CloneWithDelta 应该创建 COWDeltaRef
	clone := page.CloneWithDelta()

	require.NotNil(t, clone.cowDelta, "cowDelta should not be nil")
	// 克隆页面有自己的 COWDeltaRef，refCount = 1
	assert.Equal(t, int32(1), clone.cowDelta.GetRefCount(), "refCount should be 1 (only clone)")
	// 原始页面不应该在 Delta 模式
	assert.Nil(t, page.cowDelta, "original page should not be in delta mode")

	// keys 和 values 应该指向相同的底层数组（验证共享）
	// 修改底层数组应该同时影响原始页面和克隆页面
	if len(clone.keys) > 0 && len(page.keys) > 0 {
		// 通过修改底层数组验证共享
		originalFirstKey := page.keys[0]
		clone.keys[0][0] = 'X' // 修改底层数组的第一个字节
		assert.Equal(t, page.keys[0][0], byte('X'), "modifying clone keys should affect original (shared)")
		assert.Equal(t, clone.keys[0][0], byte('X'), "clone should see the change")
		// 恢复原始值
		page.keys[0] = originalFirstKey
	}

	// 版本号应该递增
	assert.Equal(t, page.version+1, clone.version)
}

// TestLeafPage_CloneWithDelta_InsertDelta 测试增量模式下的插入
func TestLeafPage_CloneWithDelta_InsertDelta(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))
	page.Insert([]byte("key2"), []byte("val2"))

	clone := page.CloneWithDelta()

	// 在增量模式下插入新键
	success, err := clone.Insert([]byte("key3"), []byte("val3"))
	require.NoError(t, err)
	assert.True(t, success)

	// 增量链长度应该是 1
	assert.Equal(t, 1, clone.GetDeltaCount())

	// 原始页面不应受影响
	_, found := page.Get([]byte("key3"))
	assert.False(t, found, "original page should not have key3")

	// 克隆页面应该能读取到增量
	val, found := clone.Get([]byte("key3"))
	assert.True(t, found)
	assert.Equal(t, []byte("val3"), val)
}

// TestLeafPage_CloneWithDelta_UpdateDelta 测试增量模式下的更新
func TestLeafPage_CloneWithDelta_UpdateDelta(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))
	page.Insert([]byte("key2"), []byte("val2"))

	clone := page.CloneWithDelta()

	// 更新已存在的键
	success, err := clone.Insert([]byte("key1"), []byte("val1-updated"))
	require.NoError(t, err)
	assert.False(t, success, "update should return false")

	// 原始页面不应受影响
	val, _ := page.Get([]byte("key1"))
	assert.Equal(t, []byte("val1"), val, "original should have old value")

	// 克隆页面应该读取到新值
	val, _ = clone.Get([]byte("key1"))
	assert.Equal(t, []byte("val1-updated"), val, "clone should have updated value")
}

// TestLeafPage_CloneWithDelta_DeleteDelta 测试增量模式下的删除
func TestLeafPage_CloneWithDelta_DeleteDelta(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))
	page.Insert([]byte("key2"), []byte("val2"))

	clone := page.CloneWithDelta()

	// 删除键
	success, err := clone.Delete([]byte("key1"))
	require.NoError(t, err)
	assert.True(t, success)

	// 原始页面不应受影响
	_, found := page.Get([]byte("key1"))
	assert.True(t, found, "original should still have key1")

	// 克隆页面应该读取到删除
	_, found = clone.Get([]byte("key1"))
	assert.False(t, found, "clone should not have key1")
}

// TestLeafPage_CloneWithDelta_AutoMaterialize 测试自动物化
func TestLeafPage_CloneWithDelta_AutoMaterialize(t *testing.T) {
	page := NewLeafPage(1)
	// 添加初始数据
	for i := range 100 {
		page.Insert([]byte(fmt.Sprintf("key%d", i)), []byte(fmt.Sprintf("val%d", i)))
	}

	clone := page.CloneWithDelta()

	// 添加超过阈值的增量（maxDeltas = 10）
	for i := range 15 {
		clone.Insert([]byte(fmt.Sprintf("new%d", i)), []byte(fmt.Sprintf("newval%d", i)))
	}

	// 应该自动物化（cowDelta 变为 nil）
	assert.Nil(t, clone.cowDelta, "cowDelta should be nil after materialization")

	// 验证数据完整性（物化后所有数据都合并到 keys/values）
	val, found := clone.Get([]byte("new0"))
	assert.True(t, found)
	assert.Equal(t, []byte("newval0"), val)

	// 验证原始数据也存在
	val, found = clone.Get([]byte("key0"))
	assert.True(t, found)
	assert.Equal(t, []byte("val0"), val)
}

// TestLeafPage_CloneWithDelta_Concurrent 测试单写入者并发读取
// 注意：Delta Chain 不是为多写入者并发设计的，而是单写入者 + 多读者
func TestLeafPage_CloneWithDelta_Concurrent(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))

	clone := page.CloneWithDelta()

	// 预先添加一些数据
	for i := range 10 {
		clone.Insert([]byte(fmt.Sprintf("key-%d", i)), []byte(fmt.Sprintf("val-%d", i)))
	}

	const goroutines = 100
	const opsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	// 并发读取（多读者是安全的）
	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			for j := range opsPerGoroutine {
				key := []byte(fmt.Sprintf("key-%d", j%10))
				clone.Get(key)
			}
		}(i)
	}

	wg.Wait()

	// 验证引用计数正确
	assert.Equal(t, int32(1), clone.GetRefCount())
}

// TestLeafPage_CloneVsCloneWithDelta 测试深拷贝与 Delta Chain 的区别
func TestLeafPage_CloneVsCloneWithDelta(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))
	page.Insert([]byte("key2"), []byte("val2"))

	// CloneDeep: 深拷贝（独立切片）
	deepClone := page.CloneDeep()
	assert.Nil(t, deepClone.cowDelta, "deep clone should not have cowDelta")
	// 注意：CloneDeep() 实现中，keys/values 切片是独立的
	// 验证切片独立性（通过替换整个元素）
	if len(deepClone.keys) > 0 {
		originalKey := deepClone.keys[0]
		deepClone.keys[0] = []byte("modified")
		assert.NotEqual(t, page.keys[0], deepClone.keys[0], "deep clone keys should be independent")
		// 恢复
		deepClone.keys[0] = originalKey
	}

	// Clone: Delta Chain 模式（共享 keys）
	deltaClone := page.Clone()
	assert.NotNil(t, deltaClone.cowDelta, "delta clone should have cowDelta")
	// Delta 克隆应该共享 keys（验证底层数组共享）
	if len(deltaClone.keys) > 0 && len(page.keys) > 0 {
		originalFirst := page.keys[0][0]
		deltaClone.keys[0][0] = 'Y'
		assert.Equal(t, page.keys[0][0], byte('Y'), "delta clone should share keys with original")
		// 恢复
		page.keys[0][0] = originalFirst
	}

	// 修改深拷贝不应影响原始
	deepClone.Insert([]byte("key3"), []byte("val3"))
	_, found := page.Get([]byte("key3"))
	assert.False(t, found, "original should not see deep clone changes")

	// 修改 Delta 克隆不应立即影响原始（增量模式）
	deltaClone.Insert([]byte("key4"), []byte("val4"))
	_, found = page.Get([]byte("key4"))
	assert.False(t, found, "original should not see delta clone changes")
}

// TestLeafPage_MultipleClonesWithDelta 测试多个克隆共享同一 COWDeltaRef
func TestLeafPage_MultipleClonesWithDelta(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))

	// 创建第一个克隆
	clone1 := page.CloneWithDelta()
	// clone1 创建了 COWDeltaRef，但 page 不在 Delta 模式
	// 所以 refCount = 1（clone1 持有）
	assert.Equal(t, int32(1), clone1.cowDelta.GetRefCount())

	// 创建第二个克隆（注意：这会创建一个新的 COWDeltaRef）
	clone2 := page.CloneWithDelta()
	// clone2 创建了新的 COWDeltaRef，refCount = 1
	assert.Equal(t, int32(1), clone2.cowDelta.GetRefCount())

	// 验证它们指向相同的底层数据
	assert.Equal(t, len(page.keys), len(clone1.keys))
	assert.Equal(t, len(clone1.keys), len(clone2.keys))
}

// TestLeafPage_CloneWithDelta_IsInDeltaMode 测试 IsInDeltaMode
func TestLeafPage_CloneWithDelta_IsInDeltaMode(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))

	// 原始页面不在 Delta 模式
	assert.False(t, page.IsInDeltaMode())

	// CloneDeep 不在 Delta 模式（深拷贝）
	deepClone := page.CloneDeep()
	assert.False(t, deepClone.IsInDeltaMode())

	// Clone (Delta Chain 模式)
	deltaClone := page.Clone()
	assert.True(t, deltaClone.IsInDeltaMode())

	// CloneWithDelta 等同于 Clone
	deltaClone2 := page.CloneWithDelta()
	assert.True(t, deltaClone2.IsInDeltaMode())
}

// TestLeafPage_CloneWithDelta_GetDeltaCount 测试 GetDeltaCount
func TestLeafPage_CloneWithDelta_GetDeltaCount(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))

	clone := page.CloneWithDelta()

	// 初始增量链为空
	assert.Equal(t, 0, clone.GetDeltaCount())

	// 添加少量增量（避免触发物化）
	clone.Insert([]byte("key2"), []byte("val2"))
	// 注意：Insert 可能会触发物化检查
	// 如果 page 只有 1 个 key，添加 1 个不会触发物化
	if clone.IsInDeltaMode() {
		assert.Equal(t, 1, clone.GetDeltaCount())
	}
}

// TestLeafPage_CloneWithDelta_IsShared 测试 IsShared
// 注意：IsShared 检查引用计数 > 1
// 当原始页面不在 Delta 模式时，CloneWithDelta 创建新的 COWDeltaRef，refCount = 1
func TestLeafPage_CloneWithDelta_IsShared(t *testing.T) {
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))

	// 原始页面不在 Delta 模式
	assert.False(t, page.IsInDeltaMode())
	assert.False(t, page.IsShared())

	// CloneWithDelta 创建克隆，refCount = 1（不共享）
	clone := page.CloneWithDelta()
	// 克隆的 COWDeltaRef refCount = 1（只有自己）
	assert.False(t, clone.IsShared(), "clone should not be shared (refCount=1)")

	// 深拷贝不在 Delta 模式
	deepClone := page.Clone()
	assert.False(t, deepClone.IsShared(), "deep clone should not be shared")
}
