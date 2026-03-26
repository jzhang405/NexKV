// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"os"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestBTree 创建测试用的 BTree
func setupTestBTree(t *testing.T) (*BTree, func()) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "btree_test")
	require.NoError(t, err)

	// 创建 BTree
	b, err := OpenBTree(tmpDir, nil)
	require.NoError(t, err)

	// 返回清理函数
	cleanup := func() {
		b.Close()
		os.RemoveAll(tmpDir)
	}

	return b, cleanup
}

// setupBenchmarkBTree 创建基准测试用的 BTree（无 cleanup）
func setupBenchmarkBTree(b *testing.B) *BTree {
	// 创建 BTree（使用空目录，表示内存模式）
	bt, err := OpenBTree("", nil)
	if err != nil {
		b.Fatalf("Failed to create BTree: %v", err)
	}
	return bt
}

// TestBTree_copyPathShallow 测试浅拷贝路径
func TestBTree_copyPathShallow(t *testing.T) {
	b, cleanup := setupTestBTree(t)
	defer cleanup()

	// Off-Heap 模式下跳过此测试（lazy clone 是 On-Heap 模式优化）
	if b.offheapPM != nil {
		t.Skip("Off-Heap 模式下不使用 lazy clone 优化")
	}

	// 构建路径：root -> internal -> leaf
	rootInfo := NewPageInfo()
	root := NewInternalPage(model.PageID(1))
	rootInfo.SetPage(root)

	internalInfo := NewPageInfo()
	internal := NewInternalPage(model.PageID(2))
	internalInfo.SetPage(internal)

	leafInfo := NewPageInfo()
	leaf := NewLeafPage(model.PageID(3))
	leaf.keys = append(leaf.keys, []byte("key1"), []byte("key2"))
	leaf.values = append(leaf.values, []byte("value1"), []byte("value2"))
	leafInfo.SetPage(leaf)

	path := []*PageInfo{rootInfo, internalInfo, leafInfo}

	// 执行浅拷贝
	copiedPath, err := b.copyPathShallow(path)
	require.NoError(t, err)
	require.Len(t, copiedPath, 3)

	// 验证克隆状态：InternalPage 浅拷贝，LeafPage 深拷贝
	// 这是修复并发问题的关键设计
	copiedRoot := copiedPath[0].GetInternalPage()
	assert.Equal(t, uint32(CloneStatusShallow), copiedPath[0].GetCloneStatus(),
		"InternalPage 应该是浅克隆状态")
	assert.Same(t, root, copiedRoot, "浅拷贝应该共享 root Page")

	copiedInternal := copiedPath[1].GetInternalPage()
	assert.Equal(t, uint32(CloneStatusShallow), copiedPath[1].GetCloneStatus(),
		"InternalPage 应该是浅克隆状态")
	assert.Same(t, internal, copiedInternal, "浅拷贝应该共享 internal Page")

	copiedLeaf := copiedPath[2].GetLeafPage()
	assert.Equal(t, uint32(CloneStatusDeep), copiedPath[2].GetCloneStatus(),
		"LeafPage 应该是深克隆状态（防止并发修改）")
	assert.NotSame(t, leaf, copiedLeaf, "LeafPage 需要深拷贝避免并发问题")
}

// TestBTree_copyPathShallow_EmptyPath 测试空路径
func TestBTree_copyPathShallow_EmptyPath(t *testing.T) {
	b, cleanup := setupTestBTree(t)
	defer cleanup()

	// Off-Heap 模式下跳过此测试（lazy clone 是 On-Heap 模式优化）
	if b.offheapPM != nil {
		t.Skip("Off-Heap 模式下不使用 lazy clone 优化")
	}

	_, err := b.copyPathShallow([]*PageInfo{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty path")
}

// TestBTree_finalizeDeepClone 测试深拷贝转换
func TestBTree_finalizeDeepClone(t *testing.T) {
	b, cleanup := setupTestBTree(t)
	defer cleanup()

	// Off-Heap 模式下跳过此测试（lazy clone 是 On-Heap 模式优化）
	if b.offheapPM != nil {
		t.Skip("Off-Heap 模式下不使用 lazy clone 优化")
	}

	// 构建路径（包含 InternalPage 和 LeafPage）
	rootInfo := NewPageInfo()
	root := NewInternalPage(model.PageID(1))
	rootInfo.SetPage(root)

	leafInfo := NewPageInfo()
	leaf := NewLeafPage(model.PageID(2))
	// 修复：正确初始化 LeafPage，确保 keys 和 values 长度一致
	leaf.keys = append(leaf.keys, []byte("key1"))
	leaf.values = append(leaf.values, []byte("value1"))
	leafInfo.SetPage(leaf)

	path := []*PageInfo{rootInfo, leafInfo}

	// 执行浅拷贝（LeafPage 会立即深拷贝，InternalPage 保持浅拷贝）
	shallowPath, err := b.copyPathShallow(path)
	require.NoError(t, err)

	// 验证初始状态：InternalPage 浅拷贝，LeafPage 深拷贝
	assert.Equal(t, uint32(CloneStatusShallow), shallowPath[0].GetCloneStatus(),
		"InternalPage 初始应该是浅克隆状态")
	assert.Equal(t, uint32(CloneStatusDeep), shallowPath[1].GetCloneStatus(),
		"LeafPage 在 copyPathShallow 中已深拷贝")

	// 执行深拷贝转换（将 InternalPage 转换为深拷贝）
	err = b.finalizeDeepClone(shallowPath)
	require.NoError(t, err)

	// 验证最终状态：所有 PageInfo 都是深克隆状态
	for i, info := range shallowPath {
		assert.Equal(t, uint32(CloneStatusDeep), info.GetCloneStatus(),
			"PageInfo[%d] 应该是深克隆状态", i)
		assert.True(t, info.IsDeepClone())
	}

	// 关键验证：Page 对象已独立
	deepRoot := shallowPath[0].GetInternalPage()
	assert.NotSame(t, root, deepRoot, "深拷贝应该有独立的 root Page")

	deepLeaf := shallowPath[1].GetLeafPage()
	assert.NotSame(t, leaf, deepLeaf, "深拷贝应该有独立的 leaf Page")

	// 验证数据独立性
	deepLeaf.keys[0] = []byte("modified")
	assert.Equal(t, []byte("key1"), leaf.keys[0], "修改深拷贝不应影响原始")
	assert.Equal(t, []byte("modified"), deepLeaf.keys[0])
}

// TestBTree_finalizeDeepClone_EmptyPath 测试空路径
func TestBTree_finalizeDeepClone_EmptyPath(t *testing.T) {
	b, cleanup := setupTestBTree(t)
	defer cleanup()

	// Off-Heap 模式下跳过此测试（lazy clone 是 On-Heap 模式优化）
	if b.offheapPM != nil {
		t.Skip("Off-Heap 模式下不使用 lazy clone 优化")
	}

	err := b.finalizeDeepClone([]*PageInfo{})
	assert.NoError(t, err) // 空路径应该正常返回
}

// TestBTree_finalizeDeepClone_SkipAlreadyDeep 测试跳过已深拷贝的节点
func TestBTree_finalizeDeepClone_SkipAlreadyDeep(t *testing.T) {
	b, cleanup := setupTestBTree(t)
	defer cleanup()

	// Off-Heap 模式下跳过此测试（lazy clone 是 On-Heap 模式优化）
	if b.offheapPM != nil {
		t.Skip("Off-Heap 模式下不使用 lazy clone 优化")
	}

	// 创建一个已经是深克隆的路径
	rootInfo := NewPageInfo()
	root := NewInternalPage(model.PageID(1))
	rootInfo.SetPage(root)

	leafInfo := NewPageInfo()
	leaf := NewLeafPage(model.PageID(2))
	leaf.keys = append(leaf.keys, []byte("key1"))
	leaf.values = append(leaf.values, []byte("value1"))
	leafInfo.SetPage(leaf)

	// 先深拷贝
	deepRootInfo := rootInfo.CloneDeep()
	deepLeafInfo := leafInfo.CloneDeep()

	path := []*PageInfo{deepRootInfo, deepLeafInfo}

	// 验证已经是深克隆状态
	assert.Equal(t, uint32(CloneStatusDeep), deepRootInfo.GetCloneStatus())
	assert.Equal(t, uint32(CloneStatusDeep), deepLeafInfo.GetCloneStatus())

	// 再次调用 finalizeDeepClone（应该跳过）
	err := b.finalizeDeepClone(path)
	require.NoError(t, err)

	// 验证状态保持深克隆
	assert.Equal(t, uint32(CloneStatusDeep), deepRootInfo.GetCloneStatus())
	assert.Equal(t, uint32(CloneStatusDeep), deepLeafInfo.GetCloneStatus())
}

// TestBTree_LazyCloneIntegration 集成测试：模拟 CAS 场景
func TestBTree_LazyCloneIntegration(t *testing.T) {
	b, cleanup := setupTestBTree(t)
	defer cleanup()

	// Off-Heap 模式下跳过此测试（lazy clone 是 On-Heap 模式优化）
	if b.offheapPM != nil {
		t.Skip("Off-Heap 模式下不使用 lazy clone 优化")
	}

	// 构建路径
	rootInfo := NewPageInfo()
	root := NewInternalPage(model.PageID(1))
	rootInfo.SetPage(root)

	leafInfo := NewPageInfo()
	leaf := NewLeafPage(model.PageID(2))
	leaf.keys = append(leaf.keys, []byte("key1"))
	leaf.values = append(leaf.values, []byte("value1"))
	leafInfo.SetPage(leaf)

	path := []*PageInfo{rootInfo, leafInfo}

	// 步骤 1: 浅拷贝路径（CAS 前）
	// 注意：LeafPage 会立即深拷贝（防止并发修改），InternalPage 保持浅拷贝
	copiedPath, err := b.copyPathShallow(path)
	require.NoError(t, err)

	// 验证初始克隆状态
	assert.Equal(t, uint32(CloneStatusShallow), copiedPath[0].GetCloneStatus(),
		"InternalPage 应该是浅拷贝")
	assert.Equal(t, uint32(CloneStatusDeep), copiedPath[1].GetCloneStatus(),
		"LeafPage 应该立即深拷贝")

	// 步骤 2: 模拟 CAS 成功，执行深拷贝
	err = b.finalizeDeepClone(copiedPath)
	require.NoError(t, err)

	// 验证深拷贝状态
	for _, info := range copiedPath {
		assert.Equal(t, uint32(CloneStatusDeep), info.GetCloneStatus())
	}

	// 验证 Page 独立性
	copiedLeaf := copiedPath[1].GetLeafPage()
	require.NotNil(t, copiedLeaf)

	copiedLeaf.keys[0] = []byte("modified")
	assert.Equal(t, []byte("key1"), leaf.keys[0], "修改深拷贝不应影响原始")
	assert.Equal(t, []byte("modified"), copiedLeaf.keys[0])
}

// TestBTree_LazyCloneCASFailure 模拟 CAS 失败场景
func TestBTree_LazyCloneCASFailure(t *testing.T) {
	b, cleanup := setupTestBTree(t)
	defer cleanup()

	// Off-Heap 模式下跳过此测试（lazy clone 是 On-Heap 模式优化）
	if b.offheapPM != nil {
		t.Skip("Off-Heap 模式下不使用 lazy clone 优化")
	}

	// 构建路径
	leafInfo := NewPageInfo()
	leaf := NewLeafPage(model.PageID(1))
	leaf.keys = append(leaf.keys, []byte("key1"))
	leaf.values = append(leaf.values, []byte("value1"))
	leafInfo.SetPage(leaf)

	path := []*PageInfo{leafInfo}

	// 步骤 1: 浅拷贝路径（LeafPage 会立即深拷贝）
	copiedPath, err := b.copyPathShallow(path)
	require.NoError(t, err)

	// 验证克隆状态：LeafPage 立即深拷贝
	assert.Equal(t, uint32(CloneStatusDeep), copiedPath[0].GetCloneStatus(),
		"LeafPage 应该立即深拷贝")

	// 步骤 2: 模拟 CAS 失败（不调用 finalizeDeepClone）
	// 浅拷贝的 copiedPath 会被 GC 回收
	// 原始 leaf 仍然有效

	// 验证原始 leaf 未受影响
	assert.Equal(t, []byte("key1"), leaf.keys[0])

	// 关键：LeafPage 深拷贝，有独立的 Page 副本
	copiedLeaf := copiedPath[0].GetLeafPage()
	assert.NotSame(t, leaf, copiedLeaf, "LeafPage 深拷贝，有独立副本")
}

// TestBTree_copyPathShallow_Integration 测试集成场景
func TestBTree_copyPathShallow_Integration(t *testing.T) {
	b, cleanup := setupTestBTree(t)
	defer cleanup()

	// Off-Heap 模式下跳过此测试（lazy clone 是 On-Heap 模式优化）
	if b.offheapPM != nil {
		t.Skip("Off-Heap 模式下不使用 lazy clone 优化")
	}

	// 构建三层路径：root -> internal -> leaf
	rootInfo := NewPageInfo()
	root := NewInternalPage(model.PageID(1))
	rootInfo.SetPage(root)

	internalInfo := NewPageInfo()
	internal := NewInternalPage(model.PageID(2))
	internal.keys = append(internal.keys, []byte("sep1"))
	internalInfo.SetPage(internal)

	leafInfo := NewPageInfo()
	leaf := NewLeafPage(model.PageID(3))
	leaf.keys = append(leaf.keys, []byte("key1"), []byte("key2"))
	leaf.values = append(leaf.values, []byte("value1"), []byte("value2"))
	leafInfo.SetPage(leaf)

	// 建立父子关系
	internal.children = append(internal.children, NewPageRefWithInfo(leafInfo))
	root.children = append(root.children, NewPageRefWithInfo(internalInfo))

	path := []*PageInfo{rootInfo, internalInfo, leafInfo}

	// 执行浅拷贝
	copiedPath, err := b.copyPathShallow(path)
	require.NoError(t, err)
	require.Len(t, copiedPath, 3)

	// 验证路径完整性
	for i := range len(copiedPath) {
		assert.NotNil(t, copiedPath[i])
	}

	// 验证 Page 共享（InternalPage 共享，LeafPage 深拷贝）
	assert.Same(t, root, copiedPath[0].GetPage(), "Root Page 应该共享")
	assert.Same(t, internal, copiedPath[1].GetPage(), "Internal Page 应该共享")
	assert.NotSame(t, leaf, copiedPath[2].GetPage(), "LeafPage 应该深拷贝（防止并发修改）")
}

// BenchmarkBTree_copyPathShallow 基准测试：浅拷贝路径
func BenchmarkBTree_copyPathShallow(b *testing.B) {
	bt := setupBenchmarkBTree(b)

	// 构建三层路径
	rootInfo := NewPageInfo()
	root := NewInternalPage(model.PageID(1))
	rootInfo.SetPage(root)

	internalInfo := NewPageInfo()
	internal := NewInternalPage(model.PageID(2))
	for i := range 10 {
		internal.keys = append(internal.keys, []byte{byte(i)})
	}
	internalInfo.SetPage(internal)

	leafInfo := NewPageInfo()
	leaf := NewLeafPage(model.PageID(3))
	for i := range 100 {
		leaf.keys = append(leaf.keys, []byte{byte(i)})
	}
	leafInfo.SetPage(leaf)

	internal.children = append(internal.children, NewPageRefWithInfo(leafInfo))
	root.children = append(root.children, NewPageRefWithInfo(internalInfo))

	path := []*PageInfo{rootInfo, internalInfo, leafInfo}

	b.ResetTimer()
	for b.Loop() {
		bt.copyPathShallow(path)
	}
}

// BenchmarkBTree_finalizeDeepClone 基准测试：深拷贝转换
func BenchmarkBTree_finalizeDeepClone(b *testing.B) {
	bt := setupBenchmarkBTree(b)

	// 构建浅拷贝路径
	rootInfo := NewPageInfo()
	root := NewInternalPage(model.PageID(1))
	rootInfo.SetPage(root)

	leafInfo := NewPageInfo()
	leaf := NewLeafPage(model.PageID(3))
	for i := range 100 {
		leaf.keys = append(leaf.keys, []byte{byte(i)})
	}
	leafInfo.SetPage(leaf)

	path := []*PageInfo{rootInfo, leafInfo}
	shallowPath, _ := bt.copyPathShallow(path)

	b.ResetTimer()
	for b.Loop() {
		bt.finalizeDeepClone(shallowPath)
	}
}
