// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPageInfo_CloneShallow 测试浅拷贝功能
func TestPageInfo_CloneShallow(t *testing.T) {
	// 创建原始 PageInfo 并加载 LeafPage
	original := NewPageInfo()
	leaf := NewLeafPage(model.PageID(1))
	leaf.keys = append(leaf.keys, []byte("key1"), []byte("key2"))
	leaf.values = append(leaf.values, []byte("value1"), []byte("value2"))
	original.SetPage(leaf)

	// 执行浅拷贝
	cloned := original.CloneShallow()

	// 验证 PageInfo 元数据已复制
	assert.Equal(t, original.GetPos(), cloned.GetPos())
	assert.Equal(t, original.lastTime.Load(), cloned.lastTime.Load())
	assert.Equal(t, original.hits.Load(), cloned.hits.Load())
	assert.Equal(t, original.flags.Load(), cloned.flags.Load())

	// 验证克隆状态
	assert.Equal(t, uint32(CloneStatusShallow), cloned.GetCloneStatus())
	assert.True(t, cloned.IsShallowClone())
	assert.False(t, cloned.IsDeepClone())

	// ✅ 关键验证：Page 对象是共享的（浅拷贝）
	clonedLeaf := cloned.GetLeafPage()
	require.NotNil(t, clonedLeaf)
	assert.Same(t, leaf, clonedLeaf, "浅拷贝应该共享 Page 对象")
}

// TestPageInfo_CloneDeep 测试深拷贝功能
func TestPageInfo_CloneDeep(t *testing.T) {
	// 创建原始 PageInfo 并加载 LeafPage
	original := NewPageInfo()
	leaf := NewLeafPage(model.PageID(1))
	leaf.keys = append(leaf.keys, []byte("key1"), []byte("key2"))
	leaf.values = append(leaf.values, []byte("value1"), []byte("value2"))
	original.SetPage(leaf)

	// 执行深拷贝
	cloned := original.CloneDeep()

	// 验证 PageInfo 元数据已复制
	assert.Equal(t, original.GetPos(), cloned.GetPos())
	assert.Equal(t, original.lastTime.Load(), cloned.lastTime.Load())

	// 验证克隆状态
	assert.Equal(t, uint32(CloneStatusDeep), cloned.GetCloneStatus())
	assert.False(t, cloned.IsShallowClone())
	assert.True(t, cloned.IsDeepClone())

	// ✅ 关键验证：Page 对象是独立的（深拷贝）
	clonedLeaf := cloned.GetLeafPage()
	require.NotNil(t, clonedLeaf)
	assert.NotSame(t, leaf, clonedLeaf, "深拷贝应该创建独立的 Page 对象")

	// 验证数据独立性
	clonedLeaf.keys[0] = []byte("modified")
	assert.Equal(t, []byte("key1"), leaf.keys[0], "修改克隆不应影响原始")
	assert.Equal(t, []byte("modified"), clonedLeaf.keys[0])
}

// TestPageInfo_CloneDeepFromShallow 测试从浅克隆转换为深克隆
func TestPageInfo_CloneDeepFromShallow(t *testing.T) {
	// 创建原始 PageInfo
	original := NewPageInfo()
	leaf := NewLeafPage(model.PageID(1))
	leaf.keys = append(leaf.keys, []byte("key1"))
	leaf.values = append(leaf.values, []byte("value1"))
	original.SetPage(leaf)

	// 先执行浅拷贝
	shallow := original.CloneShallow()
	assert.Equal(t, uint32(CloneStatusShallow), shallow.GetCloneStatus())

	// 从浅拷贝转换为深拷贝
	deep := shallow.CloneDeep()
	assert.Equal(t, uint32(CloneStatusDeep), deep.GetCloneStatus())

	// 验证 Page 对象已独立
	shallowLeaf := shallow.GetLeafPage()
	deepLeaf := deep.GetLeafPage()
	assert.Same(t, shallowLeaf, leaf, "浅拷贝仍共享原始 Page")
	assert.NotSame(t, deepLeaf, leaf, "深拷贝有独立 Page")
}

// TestPageInfo_CloneDeepTwice 测试多次调用 CloneDeep
func TestPageInfo_CloneDeepTwice(t *testing.T) {
	original := NewPageInfo()
	leaf := NewLeafPage(model.PageID(1))
	leaf.keys = append(leaf.keys, []byte("key1"))
	original.SetPage(leaf)

	// 第一次深拷贝
	deep1 := original.CloneDeep()
	assert.Equal(t, uint32(CloneStatusDeep), deep1.GetCloneStatus())

	// 第二次深拷贝应该返回自身
	deep2 := deep1.CloneDeep()
	assert.Equal(t, uint32(CloneStatusDeep), deep2.GetCloneStatus())
	assert.Same(t, deep1, deep2, "已经是深克隆，应返回自身")
}

// TestPageInfo_CloneShallowWithInternalPage 测试 InternalPage 浅拷贝
func TestPageInfo_CloneShallowWithInternalPage(t *testing.T) {
	// 创建原始 PageInfo 并加载 InternalPage
	original := NewPageInfo()
	internal := NewInternalPage(model.PageID(1))
	internal.keys = append(internal.keys, []byte("key1"), []byte("key2"))
	childRef := NewPageRef()
	internal.children = append(internal.children, childRef, NewPageRef())
	original.SetPage(internal)

	// 执行浅拷贝
	cloned := original.CloneShallow()

	// 验证克隆状态
	assert.Equal(t, uint32(CloneStatusShallow), cloned.GetCloneStatus())

	// 验证 Page 对象是共享的
	clonedInternal := cloned.GetInternalPage()
	require.NotNil(t, clonedInternal)
	assert.Same(t, internal, clonedInternal, "浅拷贝应该共享 InternalPage")
}

// TestPageInfo_CloneDeepWithInternalPage 测试 InternalPage 深拷贝
func TestPageInfo_CloneDeepWithInternalPage(t *testing.T) {
	// 创建原始 PageInfo 并加载 InternalPage
	original := NewPageInfo()
	internal := NewInternalPage(model.PageID(1))
	internal.keys = append(internal.keys, []byte("key1"))
	childRef := NewPageRef()
	internal.children = append(internal.children, childRef, NewPageRef())
	original.SetPage(internal)

	// 执行深拷贝
	cloned := original.CloneDeep()

	// 验证克隆状态
	assert.Equal(t, uint32(CloneStatusDeep), cloned.GetCloneStatus())

	// 验证 Page 对象是独立的
	clonedInternal := cloned.GetInternalPage()
	require.NotNil(t, clonedInternal)
	assert.NotSame(t, internal, clonedInternal, "深拷贝应该创建独立的 InternalPage")

	// 验证数据独立性
	clonedInternal.keys[0] = []byte("modified")
	assert.Equal(t, []byte("key1"), internal.keys[0])
	assert.Equal(t, []byte("modified"), clonedInternal.keys[0])
}

// TestPageInfo_CloneShallowNilPage 测试 Page 为 nil 时的浅拷贝
func TestPageInfo_CloneShallowNilPage(t *testing.T) {
	original := NewPageInfo()
	// Page 为 nil

	cloned := original.CloneShallow()

	assert.Nil(t, cloned.GetPage())
	assert.Equal(t, uint32(CloneStatusShallow), cloned.GetCloneStatus())
}

// TestPageInfo_CloneDeepNilPage 测试 Page 为 nil 时的深拷贝
func TestPageInfo_CloneDeepNilPage(t *testing.T) {
	original := NewPageInfo()
	// Page 为 nil

	cloned := original.CloneDeep()

	assert.Nil(t, cloned.GetPage())
	assert.Equal(t, uint32(CloneStatusDeep), cloned.GetCloneStatus())
}

// TestPageInfo_CloneShallowIndependence 测试浅拷贝的独立性
func TestPageInfo_CloneShallowIndependence(t *testing.T) {
	original := NewPageInfo()
	leaf := NewLeafPage(model.PageID(1))
	leaf.keys = append(leaf.keys, []byte("key1"))
	original.SetPage(leaf)

	// 浅拷贝
	cloned := original.CloneShallow()

	// 修改克隆的元数据（不影响原始）
	cloned.SetPos(999)
	assert.NotEqual(t, original.GetPos(), cloned.GetPos())

	// ✅ 关键：修改共享 Page 的内容会影响原始
	clonedLeaf := cloned.GetLeafPage()
	clonedLeaf.keys[0] = []byte("modified")

	originalLeaf := original.GetLeafPage()
	assert.Equal(t, []byte("modified"), originalLeaf.keys[0], "浅拷贝共享 Page，修改会影响原始")
}

// TestPageInfo_CloneDeepIndependence 测试深拷贝的独立性
func TestPageInfo_CloneDeepIndependence(t *testing.T) {
	original := NewPageInfo()
	leaf := NewLeafPage(model.PageID(1))
	leaf.keys = append(leaf.keys, []byte("key1"))
	original.SetPage(leaf)

	// 深拷贝
	cloned := original.CloneDeep()

	// 修改克隆的元数据（不影响原始）
	cloned.SetPos(999)
	assert.NotEqual(t, original.GetPos(), cloned.GetPos())

	// 修改克隆的 Page（不影响原始）
	clonedLeaf := cloned.GetLeafPage()
	clonedLeaf.keys[0] = []byte("modified")

	originalLeaf := original.GetLeafPage()
	assert.Equal(t, []byte("key1"), originalLeaf.keys[0], "深拷贝独立 Page，修改不影响原始")
}

// TestPageInfo_CloneStatusDefaults 测试默认克隆状态
func TestPageInfo_CloneStatusDefaults(t *testing.T) {
	info := NewPageInfo()

	// 新创建的 PageInfo 应该是共享状态
	assert.Equal(t, uint32(CloneStatusShared), info.GetCloneStatus())
	assert.False(t, info.IsShallowClone())
	assert.False(t, info.IsDeepClone())
}

// TestPageInfo_CloneVsCloneDeep 测试 Clone() 和 CloneDeep() 的等价性
func TestPageInfo_CloneVsCloneDeep(t *testing.T) {
	original := NewPageInfo()
	leaf := NewLeafPage(model.PageID(1))
	leaf.keys = append(leaf.keys, []byte("key1"))
	original.SetPage(leaf)

	// 使用旧方法 Clone()
	cloned1 := original.Clone()

	// 使用新方法 CloneDeep()
	cloned2 := original.CloneDeep()

	// 两者应该创建完全独立的副本
	cloned1Leaf := cloned1.GetLeafPage()
	cloned2Leaf := cloned2.GetLeafPage()

	assert.NotSame(t, leaf, cloned1Leaf)
	assert.NotSame(t, leaf, cloned2Leaf)
	assert.NotSame(t, cloned1Leaf, cloned2Leaf)

	// CloneDeep() 应该标记为深克隆状态
	assert.Equal(t, uint32(CloneStatusDeep), cloned2.GetCloneStatus())
}
