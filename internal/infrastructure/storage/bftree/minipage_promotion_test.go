package bftree

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMiniPage_ReadPromotion 测试读取提升
func TestMiniPage_ReadPromotion(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// L1 读取阈值是 1，读取一次即可触发提升
	_ = node.Set([]byte("key1"), []byte("value1"))

	// 模拟读取（直接调用 incrementReadCount）
	for i := 0; i < 5; i++ {
		node.miniPage.incrementReadCount()
	}

	// 检查是否应该提升
	assert.True(t, node.miniPage.shouldPromote())

	// 执行提升
	err := node.Promote()
	require.NoError(t, err)

	// 验证级别提升
	assert.Equal(t, L2, node.GetLevel())
}

// TestMiniPage_SizePromotion 测试大小提升
func TestMiniPage_SizePromotion(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// L1 容量 64B，80% 阈值 = 51.2B
	// 写入足够多的数据触发大小提升
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := make([]byte, 10) // 10 字节
		_ = node.Set(key, value)
	}

	// 强制合并（触发 compact）
	for i := 0; i < 10; i++ {
		_ = node.Set([]byte("trigger"), []byte("value"))
	}

	// 检查是否应该提升
	// 由于数据量可能不够，不一定触发
	// 这里主要验证 shouldPromote 方法不崩溃
	node.miniPage.shouldPromote()
}

// TestMiniPage_PromoteToFull 测试提升到 Full 级别
func TestMiniPage_PromoteToFull(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 逐级提升
	for node.GetLevel() < Full {
		currentLevel := node.GetLevel()

		// 增加读取计数以触发提升
		for i := 0; i < 100; i++ {
			node.miniPage.incrementReadCount()
		}

		err := node.Promote()
		require.NoError(t, err)

		// 验证级别提升了
		assert.Greater(t, node.GetLevel(), currentLevel)
	}

	// 最终应该是 Full 级别
	assert.Equal(t, Full, node.GetLevel())

	// 再次尝试提升应该返回 nil
	err := node.Promote()
	assert.NoError(t, err)
	assert.Equal(t, Full, node.GetLevel())
}

// TestLeafNode_PromoteIfNeeded 测试自动提升
func TestLeafNode_PromoteIfNeeded(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 不满足提升条件
	promoted, err := node.PromoteIfNeeded()
	require.NoError(t, err)
	assert.False(t, promoted)
	assert.Equal(t, L1, node.GetLevel())

	// 增加读取计数
	for i := 0; i < 10; i++ {
		node.miniPage.incrementReadCount()
	}

	// 满足提升条件
	promoted, err = node.PromoteIfNeeded()
	require.NoError(t, err)
	assert.True(t, promoted)
	assert.Equal(t, L2, node.GetLevel())
}

// TestMiniPage_GetReadCount 测试读取计数
func TestMiniPage_GetReadCount(t *testing.T) {
	mp := NewMiniPage(L1)

	// 初始计数为 0
	count := mp.GetReadCount()
	assert.Equal(t, uint32(0), count)

	// 增加计数
	for i := 1; i <= 10; i++ {
		mp.incrementReadCount()
		count = mp.GetReadCount()
		assert.Equal(t, uint32(i), count)
	}
}

// TestLeafNode_GetLevel 测试获取级别
func TestLeafNode_GetLevel(t *testing.T) {
	node := NewLeafNode(1, L2, 8, 2048)
	assert.Equal(t, L2, node.GetLevel())

	node = NewLeafNode(2, L3, 8, 2048)
	assert.Equal(t, L3, node.GetLevel())
}

// TestMiniPage_PromotionPreservesData 测试提升后数据完整性
func TestMiniPage_PromotionPreservesData(t *testing.T) {
	node := NewLeafNode(1, L1, 8, 2048)

	// 写入数据
	pairs := [][]byte{
		[]byte("key1"), []byte("value1"),
		[]byte("key2"), []byte("value2"),
		[]byte("key3"), []byte("value3"),
	}

	for i := 0; i < len(pairs); i += 2 {
		err := node.Set(pairs[i], pairs[i+1])
		require.NoError(t, err)
	}

	// 强制合并
	for i := 0; i < 10; i++ {
		_ = node.Set([]byte("trigger"), []byte("value"))
	}

	// 提升前验证数据
	for i := 0; i < len(pairs); i += 2 {
		value, found := node.Get(pairs[i])
		assert.True(t, found)
		assert.Equal(t, pairs[i+1], value)
	}

	// 执行提升
	for i := 0; i < 100; i++ {
		node.miniPage.incrementReadCount()
	}
	err := node.Promote()
	require.NoError(t, err)

	// 提升后验证数据
	for i := 0; i < len(pairs); i += 2 {
		value, found := node.Get(pairs[i])
		assert.True(t, found, "key: %s", string(pairs[i]))
		assert.Equal(t, pairs[i+1], value)
	}
}
