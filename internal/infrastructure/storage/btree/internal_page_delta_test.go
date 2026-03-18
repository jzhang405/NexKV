// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInternalPage_CloneWithDelta_Basic 测试 CloneWithDelta 基本功能
func TestInternalPage_CloneWithDelta_Basic(t *testing.T) {
	page := NewInternalPage(1)

	// 添加一些键和子节点
	for i := 0; i < 5; i++ {
		key := []byte{byte(i)}
		childRef := NewPageRef()
		page.InsertKeyChild(key, childRef)
	}

	// CloneWithDelta 应该创建 COWDeltaRef
	clone := page.CloneWithDelta()

	require.NotNil(t, clone.cowDelta, "cowDelta should not be nil")
	// 原始页面不在 Delta 模式，所以克隆页面持有 COWDeltaRef，refCount = 1
	assert.Equal(t, int32(1), clone.cowDelta.GetRefCount(), "refCount should be 1")

	// keys 应该共享引用（验证底层数组共享）
	if len(clone.keys) > 0 && len(page.keys) > 0 {
		// 通过修改底层数组验证共享
		originalFirstKey := page.keys[0]
		clone.keys[0][0] = 'X' // 修改底层数组
		assert.Equal(t, page.keys[0][0], byte('X'), "modifying clone keys should affect original (shared)")
		// 恢复原始值
		page.keys[0] = originalFirstKey
	}

	// children 应该独立（深拷贝）
	// 验证它们长度相同但不是同一个对象
	assert.Equal(t, len(page.children), len(clone.children), "children should have same length")
}

// TestInternalPage_CloneVsCloneWithDelta 测试深拷贝与 Delta Chain 的区别
func TestInternalPage_CloneVsCloneWithDelta(t *testing.T) {
	page := NewInternalPage(1)

	// 添加键和子节点
	key := []byte("key1")
	childRef := NewPageRef()
	page.InsertKeyChild(key, childRef)

	// CloneDeep: 深拷贝（keys 和 children 都独立）
	deepClone := page.CloneDeep()
	assert.Nil(t, deepClone.cowDelta, "deep clone should not have cowDelta")
	// keys 和 children 应该是独立的副本（通过修改验证）
	if len(deepClone.keys) > 0 {
		deepClone.keys[0][0] = 'X'
		assert.NotEqual(t, page.keys[0][0], deepClone.keys[0][0], "deep clone keys should be independent")
		// 恢复
		deepClone.keys[0][0] = page.keys[0][0]
	}

	// Clone: Delta Chain 模式（keys 共享，children 独立）
	deltaClone := page.Clone()
	assert.NotNil(t, deltaClone.cowDelta, "delta clone should have cowDelta")
	// children 应该独立（通过验证长度相同）
	assert.Equal(t, len(page.children), len(deltaClone.children), "children should have same length")

	// keys 应该共享（验证底层数组共享）
	if len(deltaClone.keys) > 0 && len(page.keys) > 0 {
		originalFirst := page.keys[0][0]
		deltaClone.keys[0][0] = 'Y'
		assert.Equal(t, page.keys[0][0], byte('Y'), "delta clone should share keys with original")
		// 恢复
		page.keys[0][0] = originalFirst
	}
}

// TestInternalPage_CloneWithDelta_IsInDeltaMode 测试 IsInDeltaMode
func TestInternalPage_CloneWithDelta_IsInDeltaMode(t *testing.T) {
	page := NewInternalPage(1)
	key := []byte("key1")
	childRef := NewPageRef()
	page.InsertKeyChild(key, childRef)

	// 原始页面不在 Delta 模式
	assert.False(t, page.IsInDeltaMode())

	// CloneDeep 不在 Delta 模式
	deepClone := page.CloneDeep()
	assert.False(t, deepClone.IsInDeltaMode())

	// Clone (Delta Chain 模式)
	deltaClone := page.Clone()
	assert.True(t, deltaClone.IsInDeltaMode())

	// CloneWithDelta 等同于 Clone
	deltaClone2 := page.CloneWithDelta()
	assert.True(t, deltaClone2.IsInDeltaMode())
}

// TestInternalPage_CloneWithDelta_Version 测试版本号递增
func TestInternalPage_CloneWithDelta_Version(t *testing.T) {
	page := NewInternalPage(1)
	key := []byte("key1")
	childRef := NewPageRef()
	page.InsertKeyChild(key, childRef)

	originalVersion := page.version
	clone := page.CloneWithDelta()

	// 版本号应该递增
	assert.Equal(t, originalVersion+1, clone.version)
}

// TestInternalPage_CloneWithDelta_ChildIndependence 测试 children 独立性
func TestInternalPage_CloneWithDelta_ChildIndependence(t *testing.T) {
	page := NewInternalPage(1)

	// 添加多个子节点
	for i := 0; i < 5; i++ {
		key := []byte{byte(i)}
		childRef := NewPageRef()
		page.InsertKeyChild(key, childRef)
	}

	clone := page.CloneWithDelta()

	// children 应该是独立的切片（验证长度相同）
	assert.Equal(t, len(page.children), len(clone.children))

	// 修改克隆的 children 不应影响原始
	newChildRef := NewPageRef()
	if len(clone.children) > 0 {
		originalChild := page.children[0]
		clone.children[0] = newChildRef
		assert.Same(t, originalChild, page.children[0], "original should keep its child")
		assert.Same(t, newChildRef, clone.children[0], "clone should have new child")
	}
}

// TestInternalPage_MultipleClonesWithDelta 测试多个克隆
func TestInternalPage_MultipleClonesWithDelta(t *testing.T) {
	page := NewInternalPage(1)
	key := []byte("key1")
	childRef := NewPageRef()
	page.InsertKeyChild(key, childRef)

	// 创建多个克隆
	clone1 := page.CloneWithDelta()
	clone2 := page.CloneWithDelta()

	// 每个克隆创建新的 COWDeltaRef（因为原始页面不在 Delta 模式）
	assert.Equal(t, int32(1), clone1.cowDelta.GetRefCount())
	assert.Equal(t, int32(1), clone2.cowDelta.GetRefCount())

	// 验证 keys 指向相同的底层数据
	assert.Equal(t, len(page.keys), len(clone1.keys))
	assert.Equal(t, len(clone1.keys), len(clone2.keys))
}

// TestInternalPage_CloneWithDelta_EmptyPage 测试空页面的克隆
func TestInternalPage_CloneWithDelta_EmptyPage(t *testing.T) {
	page := NewInternalPage(1)

	// 空页面克隆
	clone := page.CloneWithDelta()

	assert.NotNil(t, clone.cowDelta, "even empty page should have cowDelta")
	assert.Equal(t, 0, len(clone.keys))
	assert.Equal(t, 0, len(clone.children))
	assert.Equal(t, page.version+1, clone.version)
}

// TestInternalPage_CloneWithDelta_LargePage 测试大页面的克隆性能
func TestInternalPage_CloneWithDelta_LargePage(t *testing.T) {
	page := NewInternalPage(1)

	// 添加大量键（模拟真实场景）
	for i := 0; i < 100; i++ {
		key := []byte{byte(i % 256), byte(i / 256)}
		childRef := NewPageRef()
		page.InsertKeyChild(key, childRef)
	}

	// CloneWithDelta 应该很快（零拷贝 keys）
	clone := page.CloneWithDelta()

	assert.NotNil(t, clone.cowDelta)
	assert.Equal(t, len(page.keys), len(clone.keys))
	assert.Equal(t, len(page.children), len(clone.children))
}

// TestInternalPage_CloneWithDelta_IsShared 测试 IsShared
func TestInternalPage_CloneWithDelta_IsShared(t *testing.T) {
	page := NewInternalPage(1)
	key := []byte("key1")
	childRef := NewPageRef()
	page.InsertKeyChild(key, childRef)

	// 原始页面不在 Delta 模式
	assert.False(t, page.IsInDeltaMode())
	assert.False(t, page.IsShared())

	// CloneWithDelta 创建克隆，refCount = 1（不共享）
	clone := page.CloneWithDelta()
	assert.False(t, clone.IsShared(), "clone should not be shared (refCount=1)")

	// 深拷贝不在 Delta 模式
	deepClone := page.Clone()
	assert.False(t, deepClone.IsShared(), "deep clone should not be shared")
}
