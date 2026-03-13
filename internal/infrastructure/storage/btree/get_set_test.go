package btree

//nolint:errcheck // 测试代码中忽略部分返回值检查

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBTree_Get_Success 测试 Get 成功场景
func TestBTree_Get_Success(t *testing.T) {
	t.Skip("Skipping test - Get implementation needs ChunkManager integration (Week 13-14)")

	// TODO: Week 13-14 - 启用此测试
	// 1. 创建 BTree（使用新的 PageRef 架构）
	// 2. 插入一些键值对
	// 3. 获取已存在的 key
	// 4. 验证返回的 value 正确
}

// TestBTree_Get_KeyNotFound 测试 Get key 不存在场景
func TestBTree_Get_KeyNotFound(t *testing.T) {
	t.Skip("Skipping test - Get implementation needs ChunkManager integration (Week 13-14)")

	// TODO: Week 13-14 - 启用此测试
	// 1. 创建 BTree
	// 2. 获取不存在的 key
	// 3. 验证返回 ErrKeyNotFound
}

// TestBTree_Get_ContextCancel 测试 Get 上下文取消
func TestBTree_Get_ContextCancel(t *testing.T) {
	t.Skip("Skipping test - Get implementation needs ChunkManager integration (Week 13-14)")

	// TODO: Week 13-14 - 启用此测试
	// 1. 创建已取消的 context
	// 2. 调用 Get
	// 3. 验证返回 ctx.Err()
}

// TestBTree_Get_ClosedTree 测试 Get 已关闭的树
func TestBTree_Get_ClosedTree(t *testing.T) {
	// TODO: Week 13-14 - 创建 BTree 后关闭
	// btree, _ := setupTestBTree(t)
	// btree.Close()

	// _, err := btree.Get(context.Background(), []byte("key"))
	// assert.ErrorIs(t, err, ErrClosed)

	t.Skip("Skipping test - Get implementation needs ChunkManager integration (Week 13-14)")
}

// TestBTree_Set_Success 测试 Set 成功场景
func TestBTree_Set_Success(t *testing.T) {
	t.Skip("Skipping test - Set implementation needs ChunkManager integration (Week 13-14)")

	// TODO: Week 13-14 - 启用此测试
	// 1. 创建 BTree（使用新的 PageRef 架构）
	// 2. 插入键值对
	// 3. 验证插入成功
	// 4. 获取 key 验证 value 正确
}

// TestBTree_Set_UpdateExisting 测试 Set 更新已存在的 key
func TestBTree_Set_UpdateExisting(t *testing.T) {
	t.Skip("Skipping test - Set implementation needs ChunkManager integration (Week 13-14)")

	// TODO: Week 13-14 - 启用此测试
	// 1. 创建 BTree
	// 2. 插入键值对 key1 -> value1
	// 3. 更新 key1 -> value2
	// 4. 验证 Get(key1) 返回 value2
}

// TestBTree_Set_ContextCancel 测试 Set 上下文取消
func TestBTree_Set_ContextCancel(t *testing.T) {
	t.Skip("Skipping test - Set implementation needs ChunkManager integration (Week 13-14)")

	// TODO: Week 13-14 - 启用此测试
	// 1. 创建已取消的 context
	// 2. 调用 Set
	// 3. 验证返回 ctx.Err()
}

// TestBTree_Set_ClosedTree 测试 Set 已关闭的树
func TestBTree_Set_ClosedTree(t *testing.T) {
	// TODO: Week 13-14 - 创建 BTree 后关闭
	// btree, _ := setupTestBTree(t)
	// btree.Close()

	// err := btree.Set(context.Background(), []byte("key"), []byte("value"))
	// assert.ErrorIs(t, err, ErrClosed)

	t.Skip("Skipping test - Set implementation needs ChunkManager integration (Week 13-14)")
}

// TestBTree_copyPath 测试 copyPath 辅助方法
func TestBTree_copyPath(t *testing.T) {
	// 创建测试路径
	leafPage := NewLeafPage(1)
	leafPage.Insert([]byte("key1"), []byte("value1"))

	leafInfo := NewPageInfo()
	leafInfo.SetPage(leafPage)
	leafInfo.SetPos(100)

	path := []*PageInfo{leafInfo}

	// 创建 BTree 实例（仅用于测试方法）
	btree := &BTree{
		maxLevels: 10,
	}

	// 测试 copyPath
	copiedPath, err := btree.copyPath(path)
	require.NoError(t, err)
	require.Len(t, copiedPath, 1)

	// 验证复制成功
	copiedInfo := copiedPath[0]
	assert.NotSame(t, leafInfo, copiedInfo, "PageInfo 应该被克隆")

	copiedPage, ok := copiedInfo.GetPage().(*LeafPage)
	require.True(t, ok, "复制的页面应该是 LeafPage 类型")

	// 验证页面内容相同
	value, found := copiedPage.Get([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("value1"), value)

	// 验证修改副本不影响原页面
	copiedPage.Insert([]byte("key2"), []byte("value2"))

	_, found = leafPage.Get([]byte("key2"))
	assert.False(t, found, "原页面不应该包含新插入的 key")
}

// TestBTree_copyPath_EmptyPath 测试 copyPath 空路径
func TestBTree_copyPath_EmptyPath(t *testing.T) {
	btree := &BTree{
		maxLevels: 10,
	}

	_, err := btree.copyPath([]*PageInfo{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty path")
}

// TestBTree_copyPath_UnloadedPage 测试 copyPath 未加载的页面
func TestBTree_copyPath_UnloadedPage(t *testing.T) {
	// 创建未加载的 PageInfo
	info := NewPageInfo()
	info.SetPos(100)
	// 注意：不调用 SetPage，页面未加载

	path := []*PageInfo{info}

	btree := &BTree{
		maxLevels: 10,
	}

	// 测试 copyPath
	copiedPath, err := btree.copyPath(path)
	require.NoError(t, err)
	require.Len(t, copiedPath, 1)

	// 验证 PageInfo 被克隆
	copiedInfo := copiedPath[0]
	assert.NotSame(t, info, copiedInfo)
	assert.False(t, copiedInfo.IsPageLoaded(), "复制的 PageInfo 应该也是未加载状态")
}

// TestBTree_copyPath_MultiplePages 测试 copyPath 多页面路径
func TestBTree_copyPath_MultiplePages(t *testing.T) {
	// 创建测试路径：InternalPage -> LeafPage
	internalPage := NewInternalPage(1)
	leafPage := NewLeafPage(2)
	leafPage.Insert([]byte("key1"), []byte("value1"))

	leafInfo := NewPageInfo()
	leafInfo.SetPage(leafPage)
	leafInfo.SetPos(200)

	leafRef := NewPageRef()
	leafRef.SetPage(leafInfo)

	// 设置内部页面的子节点
	internalPage.children = []*PageRef{leafRef}
	internalPage.keys = [][]byte{[]byte("key1")}

	internalInfo := NewPageInfo()
	internalInfo.SetPage(internalPage)
	internalInfo.SetPos(100)

	path := []*PageInfo{internalInfo, leafInfo}

	// 创建测试用的 BTree，需要初始化 rootRef
	initialRootPage := NewLeafPage(0)
	initialRootInfo := NewPageInfo()
	initialRootInfo.SetPage(initialRootPage)
	rootPageRef := NewRootPageRefWithInfo(initialRootInfo)

	btree := &BTree{
		maxLevels: 10,
		rootRef:   rootPageRef,
	}

	// 测试 copyPath
	copiedPath, err := btree.copyPath(path)
	require.NoError(t, err)
	require.Len(t, copiedPath, 2)

	// 验证所有页面都被克隆
	for i, info := range copiedPath {
		assert.NotSame(t, path[i], info, "PageInfo[%d] 应该被克隆", i)
		assert.True(t, info.IsPageLoaded(), "PageInfo[%d] 应该已加载", i)
	}

	// 验证修改副本不影响原页面
	copiedLeaf := copiedPath[1].GetPage().(*LeafPage)
	copiedLeaf.Insert([]byte("key2"), []byte("value2"))

	_, found := leafPage.Get([]byte("key2"))
	assert.False(t, found, "原叶子页面不应该包含新插入的 key")
}
