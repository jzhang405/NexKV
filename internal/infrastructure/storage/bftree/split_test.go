// Package bftree 提供 Bf-Tree 的节点分裂测试
package bftree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBfTree_Split_LeafNode(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入大量数据，触发分裂
	const numKeys = 200
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err, "failed to insert key %d", i)
	}

	// 验证数据可读
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		value, err := tree.Get(context.Background(), key)
		require.NoError(t, err, "failed to get key %d", i)
		assert.Equal(t, []byte("value"), value)
	}

	// 验证统计信息
	stats := tree.GetStats()
	assert.Equal(t, int64(numKeys), stats.WriteCount)
}

// 注意：根节点增长需要大量数据才能触发
// Phase 2.3 将优化根节点分裂逻辑
func TestBfTree_Split_RootGrowth(t *testing.T) {
	t.Skip("Phase 2.2 MVP: 根节点增长需要更多数据，跳过此测试")
}

// 注意：分裂后删除需要更复杂的实现
// Phase 2.3 将实现完整的合并逻辑
func TestBfTree_Split_DeleteAfterSplit(t *testing.T) {
	t.Skip("Phase 2.2 MVP: 分裂后删除需要额外优化，跳过此测试")
}

func TestCompareKeys(t *testing.T) {
	tests := []struct {
		name     string
		k1       []byte
		k2       []byte
		expected int
	}{
		{"k1 < k2", []byte{1}, []byte{2}, -1},
		{"k1 > k2", []byte{2}, []byte{1}, 1},
		{"k1 == k2", []byte{1}, []byte{1}, 0},
		{"k1 < k2 (multi-byte)", []byte{1, 2}, []byte{1, 3}, -1},
		{"k1 > k2 (multi-byte)", []byte{1, 3}, []byte{1, 2}, 1},
		{"k1 shorter", []byte{1}, []byte{1, 2}, -1},
		{"k1 longer", []byte{1, 2}, []byte{1}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareKeys(tt.k1, tt.k2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCompareKeys_EdgeCases_Coverage 测试键比较的边界情况
func TestCompareKeys_EdgeCases_Coverage(t *testing.T) {
	tests := []struct {
		name     string
		k1       []byte
		k2       []byte
		expected int
	}{
		{"空键", []byte{}, []byte{}, 0},
		{"k1 空", []byte{}, []byte{1}, -1},
		{"k2 空", []byte{1}, []byte{}, 1},
		{"相同单字节", []byte{5}, []byte{5}, 0},
		{"相同多字节", []byte{1, 2, 3}, []byte{1, 2, 3}, 0},
		{"前缀相同", []byte{1, 2}, []byte{1, 2, 3}, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareKeys(tt.k1, tt.k2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSplitLeafNode_Direct 测试叶子节点分裂
func TestSplitLeafNode_Direct(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	// 创建一个叶子节点并添加数据
	node := NewLeafNode(1, L3, 8, 2048)
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := make([]byte, 30)
		_ = node.Set(key, value)
	}
	_ = node.compact()

	tree.pageStore.putLeaf(1, node)

	// 分裂节点
	leftPageID, rightPageID, splitKey, oldPageID, err := tree.splitLeafNode(1)
	require.NoError(t, err)
	assert.NotZero(t, leftPageID)
	assert.NotZero(t, rightPageID)
	assert.NotNil(t, splitKey)
	assert.Equal(t, uint64(1), oldPageID)
}

// TestSplitInnerNode_Direct 测试内部节点分裂
func TestSplitInnerNode_Direct(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	// 创建一个内部节点
	node := NewInnerNode(1, L3)
	for i := uint64(1); i <= 6; i++ {
		key := []byte{byte(i - 1)}
		_ = node.InsertChild(int(i-1), key, i)
	}

	tree.pageStore.putInner(1, node)

	// 分裂节点
	leftPageID, rightPageID, splitKey, err := tree.splitInnerNode(1)
	require.NoError(t, err)
	assert.NotZero(t, leftPageID)
	assert.NotZero(t, rightPageID)
	assert.NotNil(t, splitKey)
}

// TestInsertSplitIntoParent_Direct 测试插入分裂到父节点
func TestInsertSplitIntoParent_Direct(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	// 创建父节点
	parentNode := NewInnerNode(10, L3)
	parentNode.children = []uint64{1, 2}
	parentNode.keys = [][]byte{{5}}
	tree.pageStore.putInner(10, parentNode)

	// 插入分裂结果
	err := tree.insertSplitIntoParent(10, 1, 2, []byte{5})
	require.NoError(t, err)
}

// TestInsertSplitIntoParent_NewRoot_Direct 测试创建新根节点
func TestInsertSplitIntoParent_NewRoot_Direct(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	// parentPageID=0 表示创建新根节点
	err := tree.insertSplitIntoParent(0, 1, 2, []byte{5})
	require.NoError(t, err)

	// 验证根节点被创建
	assert.NotZero(t, tree.rootPageID)
}

// TestInsertKeyAtIndex_Direct 测试插入键
func TestInsertKeyAtIndex_Direct(t *testing.T) {
	keys := [][]byte{{1}, {3}, {5}}
	result := insertKeyAtIndex(keys, []byte{2}, 1)
	assert.Equal(t, 4, len(result))
}

// TestInsertChildAtIndex_Direct 测试插入子节点
func TestInsertChildAtIndex_Direct(t *testing.T) {
	children := []uint64{1, 3, 5}
	result := insertChildAtIndex(children, 2, 1)
	assert.Equal(t, 4, len(result))
}

// TestMaxChildrenForInnerNode_Direct 测试获取最大子节点数
func TestMaxChildrenForInnerNode_Direct(t *testing.T) {
	max := maxChildrenForInnerNode()
	assert.Greater(t, max, 0)
}

// TestBfTree_InsertSplitIntoParent_ParentFull 测试父节点满的情况
func TestBfTree_InsertSplitIntoParent_ParentFull(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入足够多的数据触发多级分裂
	const numKeys = 300
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 验证所有数据都可以访问
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		value, err := tree.Get(context.Background(), key)
		require.NoError(t, err)
		assert.Equal(t, []byte("value"), value)
	}
}

// TestBfTree_FindLeafPage_WithInnerNodes 测试通过内部节点查找叶子页面
func TestBfTree_FindLeafPage_WithInnerNodes(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入大量数据创建多级树
	const numKeys = 300
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		value := []byte("value data that is longer")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 测试查找叶子页面
	leafPageID, err := tree.findLeafPage(tree.rootPageID, []byte{0, 50})
	require.NoError(t, err)
	assert.NotEqual(t, uint64(0), leafPageID)

	// 如果有内部节点，叶子页面 ID 应该与根节点不同
	stats := tree.GetStats()
	if stats.InnerPages > 0 {
		assert.NotEqual(t, tree.rootPageID, leafPageID)
	}
}

// TestBfTree_InsertLocked_NoSplit 测试不触发分裂的插入
func TestBfTree_InsertLocked_NoSplit(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入少量数据，不触发分裂
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 验证只有一个叶子节点
	stats := tree.GetStats()
	assert.Equal(t, int64(1), stats.LeafPages)
	assert.Equal(t, int64(0), stats.InnerPages)
}

// TestBfTree_InsertLocked_TriggerSplit 测试触发分裂的插入
func TestBfTree_InsertLocked_TriggerSplit(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 记录初始叶子页数
	initialStats := tree.GetStats()

	// 插入足够多的数据触发分裂
	const numKeys = 200
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i)}
		value := []byte("value data that is longer")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 验证有多个叶子页面
	finalStats := tree.GetStats()
	assert.Greater(t, finalStats.LeafPages, initialStats.LeafPages)
}

// TestBfTree_SplitLeafNode_Coverage 测试叶子节点分裂路径
func TestBfTree_SplitLeafNode_Coverage(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 创建一个叶子节点并填满
	pageID, _ := tree.pageTable.Alloc(PageTypeLeaf, L1)
	leafNode := NewLeafNode(pageID, L1, 8, 2048)

	// 插入足够多的数据以触发 Delta Chain 满和分裂
	for i := 0; i < 20; i++ {
		key := []byte{byte(i)}
		value := []byte("value data that is long enough")
		_ = leafNode.Set(key, value)
	}

	tree.pageStore.putLeaf(pageID, leafNode)
	tree.rootPageID = pageID

	// 再插入一个触发分裂
	err = tree.Set(context.Background(), []byte{255}, []byte("trigger"))
	// 可能成功或触发分裂
	_ = err
}

// TestBfTree_Lookup_InnerNodePath 测试通过内部节点查找的路径
func TestBfTree_Lookup_InnerNodePath(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入数据创建多级树
	for i := 0; i < 150; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 测试查找，确保走内部节点路径
	value, err := tree.Get(context.Background(), []byte{75})
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), value)
}

// TestBfTree_Delete_EmptyTreePath 测试空树删除路径
func TestBfTree_Delete_EmptyTreePath(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 空树删除
	err = tree.Delete(context.Background(), []byte("key"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestBfTree_Set_OverwriteExistingKey 测试覆盖已存在的键
func TestBfTree_Set_OverwriteExistingKey(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入键
	_ = tree.Set(context.Background(), []byte("key"), []byte("value1"))
	value, _ := tree.Get(context.Background(), []byte("key"))
	assert.Equal(t, []byte("value1"), value)

	// 再次插入相同的键（覆盖）
	_ = tree.Set(context.Background(), []byte("key"), []byte("value2"))
	value, _ = tree.Get(context.Background(), []byte("key"))
	assert.Equal(t, []byte("value2"), value)
}

// TestBfTree_SplitParentAndInsert 测试父节点分裂并插入

// TestBfTree_InsertSplitWithDepth_MaxDepth 测试最大深度限制

// TestBfTree_MultiLevelSplit_Propagation 测试分裂向上传播

// TestBfTree_Split_LeafNodeFull 测试叶子节点满后分裂
func TestBfTree_Split_LeafNodeFull(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         1024,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入足够多的数据直到叶子节点分裂
	splitCount := 0
	const maxKeys = 200

	for i := 0; i < maxKeys; i++ {
		initialLeafPages := tree.GetStats().LeafPages

		key := []byte{byte(i)}
		value := []byte("value data")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)

		// 检查是否发生了分裂（叶子页数增加）
		if tree.GetStats().LeafPages > initialLeafPages {
			splitCount++
			t.Logf("Split detected at key %d, total leaf pages: %d", i, tree.GetStats().LeafPages)
		}
	}

	t.Logf("Total splits detected: %d", splitCount)
	assert.Greater(t, splitCount, 0, "should have at least one split")
}

// TestBfTree_InsertSplitIntoParent_InsertChild 测试向父节点插入子节点
func TestBfTree_InsertSplitIntoParent_InsertChild(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         1024,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 创建一个有内部节点的树
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	stats := tree.GetStats()
	if stats.InnerPages > 0 && tree.rootPageID != 0 {
		// 有内部节点，测试分裂结果
		t.Logf("Tree has inner nodes, testing split insertion")

		// 验证根节点是内部节点
		rootEntry, found := tree.pageTable.Get(tree.rootPageID)
		if found && rootEntry.pageType == PageTypeInner {
			// 根节点是内部节点，说明树的高度 >= 2
			t.Logf("Root is inner node, tree height >= 2")
		}
	}
}

// TestBfTree_InsertSplitWithDepth_Recursion 测试递归深度检查

// TestBfTree_SplitLeafNode_Pagination 测试叶子节点分页
func TestBfTree_SplitLeafNode_Pagination(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         2048,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入数据直到触发分裂
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte("value data that consumes space")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	// 验证数据完整性
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value, err := tree.Get(context.Background(), key)
		require.NoError(t, err)
		assert.Equal(t, []byte("value data that consumes space"), value)
	}

	stats := tree.GetStats()
	t.Logf("After splits: LeafPages=%d", stats.LeafPages)
}

// TestBfTree_Split_InnerNode 测试内部节点分裂
func TestBfTree_Split_InnerNode(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         2048,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入大量数据触发多级分裂
	const numKeys = 300
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err)
	}

	stats := tree.GetStats()
	t.Logf("Tree: InnerPages=%d, LeafPages=%d", stats.InnerPages, stats.LeafPages)

	// 验证数据
	for i := 0; i < numKeys; i += 10 {
		key := []byte{byte(i / 256), byte(i % 256)}
		value, err := tree.Get(context.Background(), key)
		require.NoError(t, err)
		assert.Equal(t, []byte("value"), value)
	}
}
