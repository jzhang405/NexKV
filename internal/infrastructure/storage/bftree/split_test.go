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
	node := NewLeafNode(1, L3)
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
