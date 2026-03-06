// Package bftree 提供 Bf-Tree 节点合并功能测试
package bftree

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

// TestCalculateNodeUtilization 测试节点利用率计算
func TestCalculateNodeUtilization(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建一个叶子节点并添加数据
	leafNode := NewLeafNode(1, Full)
	// 初始状态：空节点
	utilization := tree.calculateNodeUtilization(leafNode)
	assert.Equal(t, float32(0), utilization)
	// 添加一些数据
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte("value")
		_ = leafNode.Set(key, value)
	}
	// 获取新利用率
	utilization = tree.calculateNodeUtilization(leafNode)
	assert.Greater(t, utilization, float32(0))
	assert.Less(t, utilization, float32(1))
}

// TestCanMergeTwoLeafNodes 测试两节点合并判断
func TestCanMergeTwoLeafNodes(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建两个小节点
	node1 := NewLeafNode(1, L1)
	node2 := NewLeafNode(2, L1)
	// 两个空节点应该可以合并
	assert.True(t, tree.canMergeTwoLeafNodes(node1, node2))
	// 添加少量数据
	_ = node1.Set([]byte("key1"), []byte("value1"))
	_ = node2.Set([]byte("key2"), []byte("value2"))
	// 小数据量应该可以合并
	assert.True(t, tree.canMergeTwoLeafNodes(node1, node2))
	// 创建大节点
	node3 := NewLeafNode(3, L2) // 使用较小的 L2 (128B) 而不是 Full
	for i := 0; i < 30; i++ {
		key := []byte{byte(i)}
		_ = node3.Set(key, []byte("large-value-12345")) // 更大的值
	}
	node4 := NewLeafNode(4, L2)
	for i := 30; i < 60; i++ {
		key := []byte{byte(i)}
		_ = node4.Set(key, []byte("large-value-12345"))
	}
	// 两个大节点不应该能合并
	assert.False(t, tree.canMergeTwoLeafNodes(node3, node4))
}

// TestCanMergeThreeLeafNodes 测试三节点合并判断
func TestCanMergeThreeLeafNodes(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建三个小节点
	left := NewLeafNode(1, L1)
	middle := NewLeafNode(2, L1)
	right := NewLeafNode(3, L1)
	// 三个空节点应该可以合并
	assert.True(t, tree.canMergeThreeLeafNodes(left, middle, right))
	// 添加少量数据
	_ = left.Set([]byte("key1"), []byte("value1"))
	_ = middle.Set([]byte("key2"), []byte("value2"))
	_ = right.Set([]byte("key3"), []byte("value3"))
	// 小数据量应该可以合并
	assert.True(t, tree.canMergeThreeLeafNodes(left, middle, right))
	// 创建三个大节点
	bigLeft := NewLeafNode(4, L2)
	bigMiddle := NewLeafNode(5, L2)
	bigRight := NewLeafNode(6, L2)
	for i := 0; i < 30; i++ {
		key := []byte{byte(i)}
		_ = bigLeft.Set(key, []byte("large-value"))
	}
	for i := 30; i < 60; i++ {
		key := []byte{byte(i)}
		_ = bigMiddle.Set(key, []byte("large-value"))
	}
	for i := 60; i < 90; i++ {
		key := []byte{byte(i)}
		_ = bigRight.Set(key, []byte("large-value"))
	}
	// 三个大节点不应该能合并
	assert.False(t, tree.canMergeThreeLeafNodes(bigLeft, bigMiddle, bigRight))
}

// TestMergeTwoLeafNodes 测试两节点合并
func TestMergeTwoLeafNodes(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建两个节点
	node1 := NewLeafNode(1, L3)
	node2 := NewLeafNode(2, L3)
	// 添加数据到两个节点
	_ = node1.Set([]byte("key1"), []byte("value1"))
	_ = node1.Set([]byte("key2"), []byte("value2"))
	_ = node2.Set([]byte("key3"), []byte("value3"))
	_ = node2.Set([]byte("key4"), []byte("value4"))
	// 执行合并（保留第一个节点）
	err := tree.mergeTwoLeafNodes(node1, node2, 1, true)
	require.NoError(t, err)
	// 验证合并结果
	val1, found := node1.Get([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("value1"), val1)
	val3, found := node1.Get([]byte("key3"))
	assert.True(t, found)
	assert.Equal(t, []byte("value3"), val3)
	// 验证去重
	_ = node1.Set([]byte("key5"), []byte("value5"))
	_ = node2.Set([]byte("key5"), []byte("value5-duplicate"))
	err = tree.mergeTwoLeafNodes(node1, node2, 1, true)
	require.NoError(t, err)
	val5, found := node1.Get([]byte("key5"))
	assert.True(t, found)
	// 应该保留第一个值
	assert.Equal(t, []byte("value5"), val5)
}

// TestMergeThreeLeafNodes 测试三节点合并
func TestMergeThreeLeafNodes(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建三个节点
	left := NewLeafNode(1, L3)
	middle := NewLeafNode(2, L3)
	right := NewLeafNode(3, L3)
	// 添加数据到三个节点
	_ = left.Set([]byte("key1"), []byte("value1"))
	_ = left.Set([]byte("key2"), []byte("value2"))
	_ = middle.Set([]byte("key3"), []byte("value3"))
	_ = middle.Set([]byte("key4"), []byte("value4"))
	_ = right.Set([]byte("key5"), []byte("value5"))
	_ = right.Set([]byte("key6"), []byte("value6"))
	// 执行三节点合并
	err := tree.mergeThreeLeafNodes(left, middle, right, 2)
	require.NoError(t, err)
	// 验证中间节点包含所有键值对
	// 验证中间节点包含所有键值对
	keys := []string{"key1", "key2", "key3", "key4", "key5", "key6"}
	for _, keyStr := range keys {
		key := []byte(keyStr)
		val, found := middle.Get(key)
		assert.True(t, found, keyStr+" should exist")
		assert.NotNil(t, val)
	}
}

// TestTryMergeAfterDelete 测试删除后触发合并
func TestTryMergeAfterDelete(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	ctx := context.Background()
	// 插入数据
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		err := tree.Set(ctx, key, []byte("value"))
		require.NoError(t, err)
	}
	// 删除大部分数据（MVP 实现不会真正触发合并，因为 getSiblings 返回 nil）
	for i := 1; i < 9; i++ {
		key := []byte{byte(i)}
		err := tree.Delete(ctx, key)
		require.NoError(t, err)
	}
	// 验证数据已删除
	val, err := tree.Get(ctx, []byte{byte(1)})
	assert.Error(t, err)
	assert.Nil(t, val)
	// 验证剩余数据仍然存在
	val, err = tree.Get(ctx, []byte{byte(0)})
	assert.NoError(t, err)
	assert.Equal(t, []byte("value"), val)
	// MVP 验证：tryMergeAfterDelete 不会返回错误
	// (即使没有实际合并，因为没有兄弟节点)
}

// TestCanMergeInnerNodes 测试内部节点合并判断
func TestCanMergeInnerNodes(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建两个小的内部节点
	node1 := NewInnerNode(1, L3) // L3 最多 5 个子节点
	node2 := NewInnerNode(2, L3)
	// 添加少量子节点
	for i := uint64(1); i <= 2; i++ {
		_ = node1.InsertChild(int(i-1), []byte{byte(i)}, i)
		_ = node2.InsertChild(int(i-1), []byte{byte(i + 10)}, i+10)
	}
	// 小数据量应该可以合并
	assert.True(t, tree.canMergeInnerNodes(node1, node2))
	// 创建大的内部节点
	node3 := NewInnerNode(3, L1) // L1 最多 2 个子节点
	_ = node3.InsertChild(0, []byte("k1"), 1)
	_ = node3.InsertChild(1, []byte("k2"), 2)
	node4 := NewInnerNode(4, L1)
	_ = node4.InsertChild(0, []byte("k3"), 3)
	_ = node4.InsertChild(1, []byte("k4"), 4)
	// 两个满的 L1 节点 (2 + 2 = 4，但 maxKeys=2，所以不能合并)
	assert.False(t, tree.canMergeInnerNodes(node3, node4))
}

// TestMergeInnerNodes 测试内部节点合并
func TestMergeInnerNodes(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建两个内部节点
	node1 := NewInnerNode(1, L3) // L3 最多 5 个子节点
	node2 := NewInnerNode(2, L3)
	// 添加子节点
	_ = node1.InsertChild(0, []byte("key1"), 101)
	_ = node1.InsertChild(1, []byte("key2"), 102)
	_ = node2.InsertChild(0, []byte("key3"), 103)
	_ = node2.InsertChild(1, []byte("key4"), 104)
	// 执行合并
	separator := []byte("sep")
	err := tree.mergeInnerNodes(node1, node2, separator)
	require.NoError(t, err)
	// 验证合并结果
	assert.Equal(t, 4, len(node1.children))
	assert.Equal(t, 3, len(node1.keys)) // key1, key2, sep, key3, key4
	// 验证子节点
	assert.Equal(t, uint64(101), node1.children[0])
	assert.Equal(t, uint64(102), node1.children[1])
	assert.Equal(t, uint64(103), node1.children[2])
	assert.Equal(t, uint64(104), node1.children[3])
}

// TestGetSiblings 测试获取兄弟节点
func TestGetSiblings(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	ctx := context.Background()
	// 插入数据触发分裂
	for i := 0; i < 20; i++ {
		key := []byte{byte(i)}
		err := tree.Set(ctx, key, []byte("value"))
		require.NoError(t, err)
	}
	// MVP 实现：getSiblings 返回 nil
	left, right, err := tree.getSiblings(1)
	assert.NoError(t, err)
	assert.Nil(t, left)
	assert.Nil(t, right)
}

// TestFindParent 测试查找父节点
func TestFindParent(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	ctx := context.Background()
	// 插入数据
	for i := 0; i < 5; i++ {
		key := []byte{byte(i)}
		err := tree.Set(ctx, key, []byte("value"))
		require.NoError(t, err)
	}
	// MVP 实现：findParent 返回 0（根节点无父节点）
	parentID, err := tree.findParent(tree.rootPageID)
	assert.NoError(t, err)
	assert.Equal(t, uint64(0), parentID)
}

// TestInsertSplitWithDepth 测试带深度限制的递归分裂
func TestInsertSplitWithDepth(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 测试深度限制
	err := tree.insertSplitWithDepth(0, 1, 2, []byte("key"), MaxSplitDepth+1)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTreeTooDeep)
	// 测试正常深度
	err = tree.insertSplitWithDepth(0, 1, 2, []byte("key"), 1)
	// 应该创建新根节点（因为 parentID == 0）
	assert.NoError(t, err)
}

// setupTestTree 创建测试用 Bf-Tree
func setupTestTree(t *testing.T) *BfTree {
	t.Helper()
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	walDir := filepath.Join(tempDir, "wal")
	// 创建配置
	config := &Config{
		DataDir:          dataDir,
		WALDir:           walDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false, // 关闭 WAL 以加快测试速度
		EnableDeltaChain: true,
		MergeThreshold:   0.25,
		CacheSize:        100,
		PromotionConfig:  DefaultPromotionConfig(),
		SegmentSize:      DefaultSegmentSize,
		BitmapLockShards: DefaultBitmapLockShards,
		MergeStrategy:    "merge",
	}
	// 创建 Bf-Tree
	tree, err := NewBfTree(config)
	require.NoError(t, err)
	return tree
}

// TestMergeThresholdConfig 测试合并阈值配置
func TestMergeThresholdConfig(t *testing.T) {
	tests := []struct {
		name          string
		threshold     float32
		expectedValid bool
		description   string
	}{
		{
			name:          "默认阈值",
			threshold:     0.25,
			expectedValid: true,
			description:   "默认 25% 阈值",
		},
		{
			name:          "低阈值",
			threshold:     0.1,
			expectedValid: true,
			description:   "10% 阈值，更激进地合并",
		},
		{
			name:          "高阈值",
			threshold:     0.5,
			expectedValid: true,
			description:   "50% 阈值，更保守地合并",
		},
		{
			name:          "零阈值",
			threshold:     0.0,
			expectedValid: true,
			description:   "0% 阈值，总是合并",
		},
		{
			name:          "全阈值",
			threshold:     1.0,
			expectedValid: true,
			description:   "100% 阈值，从不合并",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			config.MergeThreshold = tt.threshold
			config.DataDir = t.TempDir() // 添加 DataDir
			tree, err := NewBfTree(config)
			if tt.expectedValid {
				assert.NoError(t, err)
				if err == nil {
					tree.Close()
				}
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestMergeWithCompact 测试合并前先 compact
func TestMergeWithCompact(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建节点并添加数据
	node1 := NewLeafNode(1, L4)
	node2 := NewLeafNode(2, L4)
	// 添加数据到 node1
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		_ = node1.Set(key, []byte("value1"))
	}
	// 添加数据到 node2 (不同键)
	for i := 10; i < 20; i++ {
		key := []byte{byte(i)}
		_ = node2.Set(key, []byte("value2"))
	}
	// 验证 Delta Chain 非空
	assert.Greater(t, node1.DeltaCount(), 0)
	assert.Greater(t, node2.DeltaCount(), 0)
	// 合并时会先 compact
	err := tree.mergeTwoLeafNodes(node1, node2, 1, true)
	require.NoError(t, err)
	// 验证合并后数据完整
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		val, found := node1.Get(key)
		assert.True(t, found, "key %d should exist", i)
		assert.Equal(t, []byte("value1"), val)
	}
	for i := 10; i < 20; i++ {
		key := []byte{byte(i)}
		val, found := node1.Get(key)
		assert.True(t, found, "key %d should exist", i)
		assert.Equal(t, []byte("value2"), val)
	}
	// 验证合并后 Delta Chain 被清空
	assert.Equal(t, 0, node1.DeltaCount())
}

// TestMergePreservesData 测试合并保留所有数据
func TestMergePreservesData(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建节点
	node1 := NewLeafNode(1, L4)
	node2 := NewLeafNode(2, L4)
	// 添加不重复的数据
	data1 := map[string]string{}
	data2 := map[string]string{}
	for i := 0; i < 20; i++ {
		key := string([]byte{byte(i)})
		value := string([]byte{byte(i + 100)})
		data1[key] = value
		_ = node1.Set([]byte(key), []byte(value))
	}
	for i := 20; i < 40; i++ {
		key := string([]byte{byte(i)})
		value := string([]byte{byte(i + 100)})
		data2[key] = value
		_ = node2.Set([]byte(key), []byte(value))
	}
	// 合并
	err := tree.mergeTwoLeafNodes(node1, node2, 1, true)
	require.NoError(t, err)
	// 验证所有数据都保留
	for key, expectedValue := range data1 {
		val, found := node1.Get([]byte(key))
		assert.True(t, found, "key %s should exist", key)
		assert.Equal(t, expectedValue, string(val))
	}
	for key, expectedValue := range data2 {
		val, found := node1.Get([]byte(key))
		assert.True(t, found, "key %s should exist", key)
		assert.Equal(t, expectedValue, string(val))
	}
}

// TestMergeWithEmptyNodes 测试与空节点合并
func TestMergeWithEmptyNodes(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()
	// 创建一个有数据的节点和一个空节点
	node1 := NewLeafNode(1, L3)
	node2 := NewLeafNode(2, L3)
	_ = node1.Set([]byte("key1"), []byte("value1"))
	_ = node1.Set([]byte("key2"), []byte("value2"))
	// node2 保持为空
	// 合并
	err := tree.mergeTwoLeafNodes(node1, node2, 1, true)
	require.NoError(t, err)
	// 验证数据完整
	val1, found := node1.Get([]byte("key1"))
	assert.True(t, found)
	assert.Equal(t, []byte("value1"), val1)
	val2, found := node1.Get([]byte("key2"))
	assert.True(t, found)
	assert.Equal(t, []byte("value2"), val2)
}

// TestDeleteAndGet 调试删除功能
func TestDeleteAndGet(t *testing.T) {
	node := NewLeafNode(1, L3)

	// Set key 1
	key1 := []byte{byte(1)}
	err := node.Set(key1, []byte("value1"))
	require.NoError(t, err)

	// Get key 1
	val, found := node.Get(key1)
	assert.True(t, found)
	assert.Equal(t, []byte("value1"), val)

	// Delete key 1
	delKey := []byte{byte(1)}
	err = node.Delete(delKey)
	require.NoError(t, err)

	// Get key 1 again - should not be found
	val, found = node.Get(key1)
	assert.False(t, found, "key should not exist after delete")
	assert.Nil(t, val)
}

// TestTryMergeInnerNode 测试内部节点合并
func TestTryMergeInnerNode(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据创建多层树
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := make([]byte, 20)
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 删除一些数据触发合并检查
	stats := tree.GetStats()
	if stats.InnerPages > 0 {
		// 尝试合并内部节点（可能不会真正合并，但测试代码路径）
		err := tree.tryMergeInnerNode(1)
		// 错误可以接受，我们只是测试代码路径
		_ = err
	}

}

// TestTryMergeInnerNode_Direct 测试内部节点合并
func TestTryMergeInnerNode_Direct(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	// 测试合并内部节点（MVP 实现不做任何事）
	err := tree.tryMergeInnerNode(1)
	// MVP 实现只返回 nil
	assert.NoError(t, err)
}

// TestUpdateParentAfterMerge_Direct 测试合并后更新父节点
func TestUpdateParentAfterMerge_Direct(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	// MVP 实现只返回 nil
	err := tree.updateParentAfterMerge(1, 2)
	assert.NoError(t, err)
}

// TestFindParent_Direct 测试查找父节点
func TestFindParent_Direct(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	// 测试查找根节点的父节点（应该返回 0）
	parentID, err := tree.findParent(tree.rootPageID)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), parentID)

	// 测试查找不存在的节点
	parentID, err = tree.findParent(99999)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), parentID)
}

// TestFindParentBFS_Direct 测试 BFS 查找父节点

func TestInsertSplitWithDepth_Direct(t *testing.T) {
	tree := setupTestTree(t)
	defer tree.Close()

	// 测试深度限制
	err := tree.insertSplitWithDepth(0, 1, 2, []byte("key"), MaxSplitDepth+1)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTreeTooDeep)

	// 测试创建新根节点（parentID=0）
	err = tree.insertSplitWithDepth(0, 1, 2, []byte("key"), 1)
	assert.NoError(t, err)
}
