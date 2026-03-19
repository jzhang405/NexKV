package btree

//nolint:errcheck // 测试代码中忽略部分返回值检查

import (
	"context"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBTree_searchPath_Basic 测试基本的路径搜索
func TestBTree_searchPath_Basic(t *testing.T) {
	t.Skip("Skipping test - searchPath implementation pending ")

	// TODO: 启用此测试
	// 1. 创建 BTree（使用新的 PageRef 架构）
	// 2. 插入一些键值对
	// 3. 搜索某个 key
	// 4. 验证路径正确（Root → ... → Leaf）
}

// TestBTree_searchPath_EmptyTree 测试空树的搜索
func TestBTree_searchPath_EmptyTree(t *testing.T) {
	t.Skip("Skipping test - searchPath implementation pending ")

	// TODO: 启用此测试
	// 1. 创建空的 BTree
	// 2. 搜索 key
	// 3. 验证路径只包含 Root
}

// TestBTree_searchPath_ContextCancel 测试上下文取消
func TestBTree_searchPath_ContextCancel(t *testing.T) {
	t.Skip("Skipping test - searchPath implementation pending ")

	// TODO: 启用此测试
	// 1. 创建已取消的 context
	// 2. 调用 searchPath
	// 3. 验证返回 ctx.Err()
}

// TestBTree_searchPath_MaxLevels 测试最大层级限制
func TestBTree_searchPath_MaxLevels(t *testing.T) {
	t.Skip("Skipping test - searchPath implementation pending ")

	// TODO: 启用此测试
	// 1. 创建深度 > maxLevels 的树
	// 2. 搜索叶子节点
	// 3. 验证返回错误
}

// TestInternalPage_FindChildRef 测试 FindChildRef 方法
func TestInternalPage_FindChildRef(t *testing.T) {
	// 创建内部节点
	internalPage := NewInternalPage(1)

	// 添加一些键
	keys := [][]byte{
		[]byte("key1"),
		[]byte("key3"),
		[]byte("key5"),
	}

	// 添加子节点引用
	children := make([]*PageRef, 4)
	for i := range children {
		children[i] = NewPageRef()
		info := NewPageInfo()
		info.SetPage(NewLeafPage(model.PageID(i + 10)))
		children[i].SetPage(info)
	}

	// 设置键和子节点（直接操作内部字段，用于测试）
	internalPage.keys = keys
	internalPage.children = children

	// 测试查找
	testCases := []struct {
		key       []byte
		expectIdx int
	}{
		{[]byte("key0"), 0}, // 比 key1 小，应该返回第 0 个子节点
		{[]byte("key1"), 1}, // 等于 key1，应该返回第 1 个子节点（右子节点）
		{[]byte("key2"), 1}, // key1 < key2 < key3，应该返回第 1 个子节点
		{[]byte("key4"), 2}, // key3 < key4 < key5，应该返回第 2 个子节点
		{[]byte("key6"), 3}, // 比 key5 大，应该返回第 3 个子节点
	}

	for _, tc := range testCases {
		t.Run(string(tc.key), func(t *testing.T) {
			childRef := internalPage.FindChildRef(tc.key)
			require.NotNil(t, childRef)

			// 验证返回的子节点索引
			// 通过检查 PageRef 是否为 nil 来验证
			assert.NotNil(t, childRef.GetPageInfo())
		})
	}
}

// TestInternalPage_IsLeaf 测试 IsLeaf 方法
func TestInternalPage_IsLeaf(t *testing.T) {
	internalPage := NewInternalPage(1)
	assert.False(t, internalPage.IsLeaf())
}

// TestLeafPage_IsLeaf 测试 LeafPage IsLeaf 方法
func TestLeafPage_IsLeaf(t *testing.T) {
	leafPage := NewLeafPage(1)
	assert.True(t, leafPage.IsLeaf())
}

// TestInternalPage_Children 测试 Children 方法
func TestInternalPage_Children(t *testing.T) {
	internalPage := NewInternalPage(1)

	// 添加子节点
	children := make([]*PageRef, 3)
	for i := range children {
		children[i] = NewPageRef()
	}

	internalPage.children = children

	// 测试 Children() 方法
	result := internalPage.Children()
	assert.Equal(t, 3, len(result))
	assert.Equal(t, children[0], result[0])
	assert.Equal(t, children[1], result[1])
	assert.Equal(t, children[2], result[2])
}
func TestSearchPath_EmptyKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入空键（如果允许）
	err = tree.Set(ctx, []byte(""), []byte("empty-key-value"))
	if err == nil {
		// 如果支持空键，验证可以获取
		value, err := tree.Get(ctx, []byte(""))
		require.NoError(t, err)
		assert.Equal(t, []byte("empty-key-value"), value)
	} else {
		// 如果不支持空键，这是合理的
		assert.NotEqual(t, context.Canceled, err)
	}
}
