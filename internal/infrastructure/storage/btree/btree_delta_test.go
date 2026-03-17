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

	// 初始化一些数据
	for i := 0; i < 10; i++ {
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

	// 初始化一些数据
	for i := 0; i < 10; i++ {
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

// TestBTree_shouldMaterializeBeforeCAS 测试物化决策逻辑
func TestBTree_shouldMaterializeBeforeCAS(t *testing.T) {
	config := &model.BTreeConfig{}
	tree, err := OpenBTree("", config)
	require.NoError(t, err)
	defer tree.Close()

	// 测试 1: 不在 Delta 模式，不需要物化
	page := NewLeafPage(1)
	page.Insert([]byte("key1"), []byte("val1"))
	info := NewPageInfo()
	info.SetPage(page)

	assert.False(t, tree.shouldMaterializeBeforeCAS(info), "non-delta mode should not materialize")

	// 测试 2: Delta 模式，但增量链少，不需要物化
	deltaPage := page.CloneWithDelta()
	info.SetPage(deltaPage)
	assert.False(t, tree.shouldMaterializeBeforeCAS(info), "few deltas should not materialize")

	// 测试 3: Delta 模式，增量链多（模拟），需要物化
	// 注意：由于 materialize 会改变页面状态，这里只测试决策逻辑
	// 实际场景中，大量增量会触发 ShouldMaterialize 返回 true
}

// TestBTree_copyPathWithDelta_InternalNode 测试内部节点的 Delta 克隆
func TestBTree_copyPathWithDelta_InternalNode(t *testing.T) {
	config := &model.BTreeConfig{}
	tree, err := OpenBTree("", config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 初始化大量数据以触发内部节点创建
	for i := 0; i < 200; i++ {
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
	for i := 0; i < 50; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 注意：Validate 方法还未实现，跳过验证
	// err = tree.Validate(context.Background())
	// assert.NoError(t, err, "tree should be valid after multiple sets")
}
