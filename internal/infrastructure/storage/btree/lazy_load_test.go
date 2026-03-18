package btree

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPageRef_GetOrLoad_LazyLoading 测试懒加载机制
func TestPageRef_GetOrLoad_LazyLoading(t *testing.T) {
	t.Skip("Skipping test - ChunkManager integration pending")

	// TODO: 启用此测试
	// 1. 创建 ChunkManager
	// 2. 创建 PageInfo（page = nil，pos != 0）
	// 3. 第一次 GetOrLoad：应该触发加载
	// 4. 第二次 GetOrLoad：应该直接返回缓存
}

// TestPageRef_GetOrLoad_Concurrent 测试并发懒加载
func TestPageRef_GetOrLoad_Concurrent(t *testing.T) {
	t.Skip("Skipping test - ChunkManager integration pending")

	// TODO: 启用此测试
	// 1. 创建 ChunkManager
	// 2. 创建 PageInfo（page = nil）
	// 3. 多个 goroutine 并发调用 GetOrLoad
	// 4. 验证只有一个 goroutine 触发加载
	// 5. 验证所有 goroutine 都得到正确的页面
}

// TestChunkManager_LoadPage 测试 ChunkManager 加载页面
func TestChunkManager_LoadPage(t *testing.T) {
	t.Skip("Skipping test - LoadPage implementation pending")

	// TODO: 启用此测试
	// 1. 创建 ChunkManager
	// 2. 序列化一个 LeafPage
	// 3. 写入 ChunkManager
	// 4. 使用 LoadPage() 加载
	// 5. 验证反序列化后的页面正确
}

// TestBTree_loadPage 测试 BTree.loadPage() 封装方法
func TestBTree_loadPage(t *testing.T) {
	t.Skip("Skipping test - loadPage implementation pending")

	// TODO: 启用此测试
	// 1. 创建 BTree
	// 2. 调用 loadPage(pos)
	// 3. 验证返回的页面类型正确
}

// TestPageInfo_GetPageVersion 测试获取页面版本号
func TestPageInfo_GetPageVersion(t *testing.T) {
	// 创建 LeafPage
	leafPage := NewLeafPage(1)
	leafPage.SetVersion(42)

	// 创建 PageInfo
	info := NewPageInfo()
	info.SetPage(leafPage)

	// 验证 GetPageVersion
	assert.Equal(t, uint64(42), info.GetPageVersion())
}

// TestPageInfo_GetPageID 测试获取页面 ID
func TestPageInfo_GetPageID(t *testing.T) {
	// 创建 LeafPage
	leafPage := NewLeafPage(123)

	// 创建 PageInfo
	info := NewPageInfo()
	info.SetPage(leafPage)

	// 验证 GetPageID
	assert.Equal(t, uint64(123), info.GetPageID())
}

// TestPageInfo_GetPageType 测试获取页面类型
func TestPageInfo_GetPageType(t *testing.T) {
	// 测试 LeafPage
	leafPage := NewLeafPage(1)
	info1 := NewPageInfo()
	info1.SetPage(leafPage)
	assert.Equal(t, "leaf", info1.GetPageType())

	// 测试 InternalPage
	internalPage := NewInternalPage(2)
	info2 := NewPageInfo()
	info2.SetPage(internalPage)
	assert.Equal(t, "internal", info2.GetPageType())

	// 测试 nil
	info3 := NewPageInfo()
	assert.Equal(t, "nil", info3.GetPageType())
}

// TestPageInfo_IsPageLoaded 测试页面是否已加载
func TestPageInfo_IsPageLoaded(t *testing.T) {
	info := NewPageInfo()

	// 初始状态：未加载
	assert.False(t, info.IsPageLoaded())

	// 加载后
	leafPage := NewLeafPage(1)
	info.SetPage(leafPage)
	assert.True(t, info.IsPageLoaded())
}

// TestPageInfo_GetLeafPage 测试获取叶子节点
func TestPageInfo_GetLeafPage(t *testing.T) {
	leafPage := NewLeafPage(1)
	info := NewPageInfo()
	info.SetPage(leafPage)

	// 类型断言成功
	result := info.GetLeafPage()
	assert.NotNil(t, result)
	assert.Same(t, leafPage, result)

	// 错误类型
	internalPage := NewInternalPage(2)
	info2 := NewPageInfo()
	info2.SetPage(internalPage)

	result2 := info2.GetLeafPage()
	assert.Nil(t, result2)
}

// TestPageInfo_GetInternalPage 测试获取内部节点
func TestPageInfo_GetInternalPage(t *testing.T) {
	internalPage := NewInternalPage(1)
	info := NewPageInfo()
	info.SetPage(internalPage)

	// 类型断言成功
	result := info.GetInternalPage()
	assert.NotNil(t, result)
	assert.Same(t, internalPage, result)

	// 错误类型
	leafPage := NewLeafPage(2)
	info2 := NewPageInfo()
	info2.SetPage(leafPage)

	result2 := info2.GetInternalPage()
	assert.Nil(t, result2)
}
