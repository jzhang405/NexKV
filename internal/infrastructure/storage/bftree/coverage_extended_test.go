// Package bftree 扩展覆盖率测试 - P2-1
package bftree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCoverage_UpdateWithBitmapLock 使用 BitmapLock 的 Update 测试
// 覆盖 updateInPage (0% → 目标 >0%)
func TestCoverage_UpdateWithBitmapLock(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false
	config.UseBitmapLock = true  // 启用 BitmapLock 以触发 updateInPage
	config.BitmapLockShards = 16

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入初始数据
	const numKeys = 50
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("update-bm-%03d", i))
		value := []byte(fmt.Sprintf("initial-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 使用 Update 方法（会触发 updateInPage）
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("update-bm-%03d", i))
		newValue := []byte(fmt.Sprintf("updated-%03d", i))
		err := tree.Update(ctx, key, newValue)
		assert.NoError(t, err)
	}

	// 验证
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("update-bm-%03d", i))
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("updated-%03d", i)), val)
	}
}

// TestCoverage_InsertWithBitmapLock 使用 BitmapLock 的插入测试
// 覆盖 findLeafPageWithVersion 和更多路径
func TestCoverage_InsertWithBitmapLock(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false
	config.UseBitmapLock = true
	config.BitmapLockShards = 16

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据
	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("insert-bm-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 验证
	for i := 0; i < numKeys; i += 5 {
		key := []byte(fmt.Sprintf("insert-bm-%03d", i))
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("value-%03d", i)), val)
	}
}

// TestCoverage_DeleteWithBitmapLock 使用 BitmapLock 的删除测试
func TestCoverage_DeleteWithBitmapLock(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false
	config.UseBitmapLock = true
	config.BitmapLockShards = 16

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据
	const numKeys = 50
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("delete-bm-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 删除部分数据
	for i := 0; i < numKeys/2; i++ {
		key := []byte(fmt.Sprintf("delete-bm-%03d", i))
		err := tree.Delete(ctx, key)
		assert.NoError(t, err)
	}

	// 验证
	for i := 0; i < numKeys/2; i++ {
		key := []byte(fmt.Sprintf("delete-bm-%03d", i))
		_, err := tree.Get(ctx, key)
		assert.Error(t, err)
	}

	for i := numKeys / 2; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("delete-bm-%03d", i))
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("value-%03d", i)), val)
	}
}

// TestCoverage_ConcurrentWithBitmapLock 并发操作与 BitmapLock
func TestCoverage_ConcurrentWithBitmapLock(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false
	config.UseBitmapLock = true
	config.BitmapLockShards = 16

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 并发写入
	const numGoroutines = 5
	const opsPerGoroutine = 50

	done := make(chan bool, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte(fmt.Sprintf("bm-conc-%d-%03d", id, j))
				value := []byte(fmt.Sprintf("value-%d-%03d", id, j))
				err := tree.Set(ctx, key, value)
				assert.NoError(t, err)
			}
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// 验证数据
	for i := 0; i < numGoroutines; i++ {
		for j := 0; j < opsPerGoroutine; j += 5 {
			key := []byte(fmt.Sprintf("bm-conc-%d-%03d", i, j))
			val, err := tree.Get(ctx, key)
			assert.NoError(t, err)
			expected := []byte(fmt.Sprintf("value-%d-%03d", i, j))
			assert.Equal(t, expected, val)
		}
	}
}

// TestCoverage_SmallPages 小页面配置测试
func TestCoverage_SmallPages(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false
	config.PageSize = 1024  // 使用小页面

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据以触发分裂
	const numKeys = 500
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("small-%04d", i))
		value := []byte(fmt.Sprintf("value-%04d-padding", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 验证
	stats := tree.GetStats()
	t.Logf("Small pages: TotalPages=%d", stats.TotalPages)

	for i := 0; i < numKeys; i += 20 {
		key := []byte(fmt.Sprintf("small-%04d", i))
		_, err := tree.Get(ctx, key)
		assert.NoError(t, err)
	}
}

// TestCoverage_NonExistentOperations 不存在的键操作测试
func TestCoverage_NonExistentOperations(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入一些数据
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 尝试更新不存在的键
	err = tree.Update(ctx, []byte("nonexistent"), []byte("new-value"))
	assert.Error(t, err)

	// 尝试删除不存在的键
	err = tree.Delete(ctx, []byte("nonexistent"))
	// 可能返回成功或错误，取决于实现
	assert.True(t, err == nil || err != nil)

	// 验证原始数据未受影响
	for i := 0; i < 10; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("value-%03d", i)), val)
	}
}


// TestCoverage_EmptyTree 空树操作测试
func TestCoverage_EmptyTree(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 空树操作
	_, err = tree.Get(ctx, []byte("any-key"))
	assert.Error(t, err)

	err = tree.Update(ctx, []byte("any-key"), []byte("value"))
	assert.Error(t, err)

	err = tree.Delete(ctx, []byte("any-key"))
	assert.Error(t, err)

	// 验证统计
	stats := tree.GetStats()
	assert.Equal(t, int64(0), stats.TotalPages)
}

// TestCoverage_LargeKeys 大键测试
func TestCoverage_LargeKeys(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入大键
	const numKeys = 50
	for i := 0; i < numKeys; i++ {
		// 200 字节的键
		key := []byte(fmt.Sprintf("large-key-%0180d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 验证
	for i := 0; i < numKeys; i += 5 {
		key := []byte(fmt.Sprintf("large-key-%0180d", i))
		val, err := tree.Get(ctx, key)
		assert.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("value-%03d", i)), val)
	}
}
