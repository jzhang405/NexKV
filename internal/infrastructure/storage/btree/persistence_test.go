package btree

//nolint:errcheck // 测试代码中忽略部分返回值检查

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPersistence_BasicWrite 测试基本的持久化写入功能
func TestPersistence_BasicWrite(t *testing.T) {
	dir := t.TempDir()

	// 1. 打开 BTree（会初始化 ChunkManager）
	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	defer btree.Close()

	// 2. 插入键值对
	ctx := context.Background()
	err = btree.Set(ctx, []byte("key1"), []byte("value1"))
	require.NoError(t, err, "First insert should succeed")

	err = btree.Set(ctx, []byte("key2"), []byte("value2"))
	require.NoError(t, err, "Second insert should succeed")

	err = btree.Set(ctx, []byte("key3"), []byte("value3"))
	require.NoError(t, err, "Third insert should succeed")

	// 3. 验证 Chunk 文件已创建
	chunkFiles, err := filepath.Glob(filepath.Join(dir, "btree_*.ao"))
	require.NoError(t, err)
	assert.Greater(t, len(chunkFiles), 0, "At least one chunk file should be created")

	// 4. 验证 Chunk 文件大小
	info, err := os.Stat(chunkFiles[0])
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0), "Chunk file should have content")
}

// TestPersistence_Reload 测试持久化后的重新加载
func TestPersistence_Reload(t *testing.T) {
	dir := t.TempDir()

	// 1. 第一次打开 BTree 并插入数据
	btree1, err := OpenBTree(dir, nil)
	require.NoError(t, err)

	ctx := context.Background()
	err = btree1.Set(ctx, []byte("key1"), []byte("value1"))
	require.NoError(t, err)

	err = btree1.Set(ctx, []byte("key2"), []byte("value2"))
	require.NoError(t, err)

	// 2. 关闭 BTree
	btree1.Close()

	// 3. 重新打开 BTree
	btree2, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	defer btree2.Close()

	// 4. 验证数据（暂时跳过，因为 Get 操作还需要集成 ChunkManager）
	// TODO: Week 13-14 - 启用此验证
	// value, err := btree2.Get(ctx, []byte("key1"))
	// require.NoError(t, err)
	// assert.Equal(t, []byte("value1"), value)
}

// TestPersistence_PersistPage 测试单个页面持久化
func TestPersistence_PersistPage(t *testing.T) {
	dir := t.TempDir()

	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	defer btree.Close()

	// 创建 LeafPage
	leafPage := NewLeafPage(1)
	leafPage.Insert([]byte("key1"), []byte("value1"))
	leafPage.Insert([]byte("key2"), []byte("value2"))

	// 创建 PageInfo
	leafInfo := NewPageInfo()
	leafInfo.SetPage(leafPage)

	// 持久化页面
	pos, err := btree.persistPage(leafInfo, PageTypeLeaf)
	require.NoError(t, err)

	// 验证位置编码
	assert.Greater(t, pos, int64(0), "Position should be positive")

	// 验证 PageInfo.pos 已更新
	assert.Equal(t, pos, leafInfo.GetPos())
}

// TestPersistence_PersistPageRecursive 测试递归持久化
func TestPersistence_PersistPageRecursive(t *testing.T) {
	dir := t.TempDir()

	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	defer btree.Close()

	// 创建内部节点和子节点
	child1 := NewLeafPage(10)
	child1.Insert([]byte("a"), []byte("value_a"))

	child2 := NewLeafPage(11)
	child2.Insert([]byte("z"), []byte("value_z"))

	// 创建内部节点
	internalPage := NewInternalPage(1)
	child1Ref := NewPageRefWithInfo(NewPageInfo())
	child1Ref.GetPageInfo().SetPage(child1)

	child2Ref := NewPageRefWithInfo(NewPageInfo())
	child2Ref.GetPageInfo().SetPage(child2)

	internalPage.children = []*PageRef{child1Ref, child2Ref}
	internalPage.keys = [][]byte{[]byte("m")} // 分裂键

	// 创建 PageInfo
	internalInfo := NewPageInfo()
	internalInfo.SetPage(internalPage)

	// 递归持久化
	err = btree.persistPageRecursive(internalInfo)
	require.NoError(t, err)

	// 验证所有页面都有位置编码
	assert.NotEqual(t, int64(0), internalInfo.GetPos())
	assert.NotEqual(t, int64(0), child1Ref.GetPageInfo().GetPos())
	assert.NotEqual(t, int64(0), child2Ref.GetPageInfo().GetPos())
}

// TestPersistence_SplitAndPersist 测试分裂后的持久化
func TestPersistence_SplitAndPersist(t *testing.T) {
	dir := t.TempDir()

	btree, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// 插入 17 个键，触发分裂
	for i := 0; i < 17; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := btree.Set(ctx, key, value)
		require.NoError(t, err, "Insert %d should succeed", i)
	}

	// 验证 Chunk 文件已创建
	chunkFiles, err := filepath.Glob(filepath.Join(dir, "btree_*.ao"))
	require.NoError(t, err)
	assert.Greater(t, len(chunkFiles), 0, "Chunk files should be created after split")

	// TODO: Week 13-14 - 验证数据完整性（需要 Get 操作集成）
}
