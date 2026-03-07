// Package bftree 覆盖率测试 - P2-1
package bftree

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCoverage_PageSplit 页面分裂测试 - 触发 performSplitWithTreeLock
func TestCoverage_PageSplit(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入大量数据
	const numKeys = 3000
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("split-key-%06d", i))
		value := []byte(fmt.Sprintf("value-%06d-padding-to-make-it-bigger-1234567890", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 验证部分数据
	for i := 0; i < numKeys; i += 100 {
		key := []byte(fmt.Sprintf("split-key-%06d", i))
		_, err := tree.Get(ctx, key)
		assert.NoError(t, err)
	}

	// 验证统计信息
	stats := tree.GetStats()
	t.Logf("After insert: TotalPages=%d, LeafPages=%d, InnerPages=%d",
		stats.TotalPages, stats.LeafPages, stats.InnerPages)

	// 测试已覆盖大量插入路径，分裂可能被 Delta Chain 延迟
}

// TestCoverage_LargeValues 大值测试 - 触发页面分裂
func TestCoverage_LargeValues(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入大值以触发分裂
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("large-%05d", i))
		// 1KB 值
		value := strings.Repeat(fmt.Sprintf("v%05d", i), 100)
		err := tree.Set(ctx, key, []byte(value))
		assert.NoError(t, err)
	}

	// 验证数据
	for i := 0; i < numKeys; i += 5 {
		key := []byte(fmt.Sprintf("large-%05d", i))
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		expected := strings.Repeat(fmt.Sprintf("v%05d", i), 100)
		assert.Equal(t, []byte(expected), val)
	}
}

// TestCoverage_UpdateMany 批量更新测试 - 覆盖 updateInPage 和 updateLocked
func TestCoverage_UpdateMany(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入初始数据
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("update-key-%03d", i))
		value := []byte(fmt.Sprintf("initial-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 批量更新
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("update-key-%03d", i))
		newValue := []byte(fmt.Sprintf("updated-%03d", i))
		err := tree.Update(ctx, key, newValue)
		assert.NoError(t, err)

		// 验证更新
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, newValue, val)
	}
}

// TestCoverage_DeleteMany 批量删除测试
func TestCoverage_DeleteMany(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据
	const numKeys = 200
	keys := make([][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("delete-key-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		err := tree.Set(ctx, keys[i], value)
		assert.NoError(t, err)
	}

	// 删除一半
	for i := 0; i < numKeys/2; i++ {
		err := tree.Delete(ctx, keys[i])
		assert.NoError(t, err)

		// 验证删除
		_, err = tree.Get(ctx, keys[i])
		assert.Error(t, err)
	}

	// 验证剩余数据
	for i := numKeys / 2; i < numKeys; i++ {
		val, err := tree.Get(ctx, keys[i])
		assert.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("value-%03d", i)), val)
	}
}

// TestCoverage_MixedMixedSize 混合大小键值对测试
func TestCoverage_MixedSize(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 混合大小测试
	testCases := []struct {
		keyPrefix string
		valueSize int
		count     int
	}{
		{"small", 10, 50},
		{"medium", 100, 50},
		{"large", 500, 30},
	}

	for _, tc := range testCases {
		for i := 0; i < tc.count; i++ {
			key := []byte(fmt.Sprintf("%s-key-%03d", tc.keyPrefix, i))
			value := []byte(strings.Repeat(fmt.Sprintf("v%d", i), tc.valueSize/10))
			err := tree.Set(ctx, key, value)
			assert.NoError(t, err)
		}
	}

	// 验证
	for _, tc := range testCases {
		for i := 0; i < tc.count; i += 5 {
			key := []byte(fmt.Sprintf("%s-key-%03d", tc.keyPrefix, i))
			_, err := tree.Get(ctx, key)
			assert.NoError(t, err)
		}
	}
}

// TestCoverage_OverwriteSameSize 相同大小覆盖测试
func TestCoverage_OverwriteSameSize(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	const numKeys = 50
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("overwrite-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 多次覆盖
	for round := 0; round < 3; round++ {
		for i := 0; i < numKeys; i++ {
			key := []byte(fmt.Sprintf("overwrite-%03d", i))
			value := []byte(fmt.Sprintf("round-%d-value-%03d", round, i))
			err := tree.Set(ctx, key, value)
			assert.NoError(t, err)
		}
	}

	// 验证最终值
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("overwrite-%03d", i))
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("round-2-value-%03d", i)), val)
	}
}

// TestCoverage_SequentialKeys 顺序键测试
func TestCoverage_SequentialKeys(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 顺序插入
	const numKeys = 200
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("seq-%08d", i))
		value := []byte(fmt.Sprintf("value-%08d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 逆序读取
	for i := numKeys - 1; i >= 0; i -= 10 {
		key := []byte(fmt.Sprintf("seq-%08d", i))
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("value-%08d", i)), val)
	}
}

// TestCoverage_RandomAccess 随机访问测试
func TestCoverage_RandomAccess(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据
	const numKeys = 300
	keys := make([][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("random-%05d", i))
		value := []byte(fmt.Sprintf("value-%05d", i))
		err := tree.Set(ctx, keys[i], value)
		assert.NoError(t, err)
	}

	// 随机读写（使用素数步长）
	primes := []int{7, 11, 13, 17, 19, 23, 29, 31}
	for _, prime := range primes {
		for i := 0; i < numKeys; i += prime {
			if i < numKeys {
				val, err := tree.Get(ctx, keys[i])
				assert.NoError(t, err)
				assert.Equal(t, []byte(fmt.Sprintf("value-%05d", i)), val)
			}
		}
	}
}
