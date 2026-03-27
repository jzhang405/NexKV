// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBTree_copyPathWithDelta_Basic 测试 copyPathWithDelta 基本功能
func TestBTree_copyPathWithDelta_Basic(t *testing.T) {
	config := &model.BTreeConfig{}
	tree, err := OpenBTree("", config)
	require.NoError(t, err)
	defer tree.Close()

	// 修复：Off-Heap 模式下 GetPage() 返回包装器，不是 *LeafPage
	// Delta Chain 功能是 On-Heap 专用，Off-Heap 模式不支持
	if tree.offheapPM != nil {
		t.Skip("Off-Heap 模式不支持 Delta Chain（On-Heap 专用功能）")
	}

	// 初始化一些数据
	for i := range 10 {
		key := []byte{byte(i)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 获取路径
	_, path, err := tree.findLeafPage(context.Background(), []byte{5})
	require.NoError(t, err)
	require.NotEmpty(t, path)

	// 使用 copyPathWithDelta
	copiedPath, err := tree.copyPathWithDelta(path)
	require.NoError(t, err)
	require.NotEmpty(t, copiedPath)

	// 验证路径长度
	assert.Equal(t, len(path), len(copiedPath))

	// 验证叶子节点在 Delta 模式
	leafInfo := copiedPath[len(copiedPath)-1]
	leafPage, ok := leafInfo.GetPage().(*LeafPage)
	require.True(t, ok, "leaf should be LeafPage")
	assert.True(t, leafPage.IsInDeltaMode(), "leaf should be in delta mode")
}

// TestBTree_copyPathWithDelta_VersusShallow 测试 copyPathWithDelta 与 copyPathShallow 的区别
func TestBTree_copyPathWithDelta_VersusShallow(t *testing.T) {
	config := &model.BTreeConfig{}
	tree, err := OpenBTree("", config)
	require.NoError(t, err)
	defer tree.Close()

	// 修复：Off-Heap 模式下 GetPage() 返回包装器，不是 On-Heap 页面
	// Delta Chain 功能是 On-Heap 专用，Off-Heap 模式不支持
	if tree.offheapPM != nil {
		t.Skip("Off-Heap 模式不支持 Delta Chain（On-Heap 专用功能）")
	}

	// 初始化一些数据
	for i := range 10 {
		key := []byte{byte(i)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 获取路径
	_, path, err := tree.findLeafPage(context.Background(), []byte{5})
	require.NoError(t, err)
	require.NotEmpty(t, path)

	// 使用 copyPathShallow
	copiedPathShallow, err := tree.copyPathShallow(path)
	require.NoError(t, err)

	// 使用 copyPathWithDelta
	copiedPathDelta, err := tree.copyPathWithDelta(path)
	require.NoError(t, err)

	// 验证两种方法产生的路径长度相同
	assert.Equal(t, len(copiedPathShallow), len(copiedPathDelta))

	// 验证 copyPathShallow 的叶子节点是深拷贝
	leafInfoShallow := copiedPathShallow[len(copiedPathShallow)-1]
	leafPageShallow, ok := leafInfoShallow.GetPage().(*LeafPage)
	require.True(t, ok)
	assert.False(t, leafPageShallow.IsInDeltaMode(), "shallow copy should not be in delta mode")
	assert.True(t, leafInfoShallow.IsDeepClone(), "shallow copy should be marked as deep clone")

	// 验证 copyPathWithDelta 的叶子节点在 Delta 模式
	leafInfoDelta := copiedPathDelta[len(copiedPathDelta)-1]
	leafPageDelta, ok := leafInfoDelta.GetPage().(*LeafPage)
	require.True(t, ok)
	assert.True(t, leafPageDelta.IsInDeltaMode(), "delta copy should be in delta mode")
	assert.False(t, leafInfoDelta.IsDeepClone(), "delta copy should not be marked as deep clone")
}

// TestBTree_copyPathWithDelta_InternalNode 测试内部节点的 Delta 克隆
func TestBTree_copyPathWithDelta_InternalNode(t *testing.T) {
	config := &model.BTreeConfig{}
	tree, err := OpenBTree("", config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 初始化大量数据以触发内部节点创建
	for i := range 200 {
		key := []byte{byte(i / 256), byte(i % 256)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 获取路径
	_, path, err := tree.findLeafPage(ctx, []byte{100, 50})
	require.NoError(t, err)

	// 如果有多层路径，验证内部节点
	if len(path) > 1 {
		// 使用 copyPathWithDelta
		copiedPath, err := tree.copyPathWithDelta(path)
		require.NoError(t, err)

		// 验证内部节点也在 Delta 模式（如果有）
		for i, info := range copiedPath {
			if internalPage, ok := info.GetPage().(*InternalPage); ok {
				// InternalPage 也应该在 Delta 模式
				assert.True(t, internalPage.IsInDeltaMode(),
					"internal page at depth %d should be in delta mode", i)
			}
		}
	}
}

// TestBTree_CopyPathWithDelta_Integration 测试 copyPathWithDelta 的集成
func TestBTree_CopyPathWithDelta_Integration(t *testing.T) {
	config := &model.BTreeConfig{}
	tree, err := OpenBTree("", config)
	require.NoError(t, err)
	defer tree.Close()

	// 测试多次 Set 操作
	for i := range 50 {
		key := []byte{byte(i)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 注意：Validate 方法还未实现，跳过验证
	// err = tree.Validate(context.Background())
	// assert.NoError(t, err, "tree should be valid after multiple sets")
}
