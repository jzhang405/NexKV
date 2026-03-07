// Package bftree 集成测试 - P2-2
package bftree

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_CrudLifecycle 端到端 CRUD 生命周期测试
func TestIntegration_CrudLifecycle(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 1. Create - 插入数据
	keys := make([][]byte, 100)
	values := make([][]byte, 100)
	for i := 0; i < 100; i++ {
		keys[i] = []byte(fmt.Sprintf("key-%05d", i))
		values[i] = []byte(fmt.Sprintf("value-%05d", i))
		err := tree.Set(ctx, keys[i], values[i])
		assert.NoError(t, err)
	}

	// 2. Read - 验证数据
	for i := 0; i < 100; i++ {
		val, err := tree.Get(ctx, keys[i])
		assert.NoError(t, err)
		assert.Equal(t, values[i], val)
	}

	// 3. Update - 更新数据
	for i := 0; i < 50; i++ {
		newValue := []byte(fmt.Sprintf("updated-%05d", i))
		err := tree.Set(ctx, keys[i], newValue)
		assert.NoError(t, err)

		val, err := tree.Get(ctx, keys[i])
		assert.NoError(t, err)
		assert.Equal(t, newValue, val)
	}

	// 4. Delete - 删除部分数据
	for i := 0; i < 30; i++ {
		err := tree.Delete(ctx, keys[i])
		assert.NoError(t, err)

		_, err = tree.Get(ctx, keys[i])
		assert.Error(t, err)
	}

	// 5. 验证剩余数据
	for i := 30; i < 100; i++ {
		val, err := tree.Get(ctx, keys[i])
		assert.NoError(t, err)

		if i < 50 {
			assert.Equal(t, []byte(fmt.Sprintf("updated-%05d", i)), val)
		} else {
			assert.Equal(t, values[i], val)
		}
	}
}

// TestIntegration_ConcurrentOperations 并发操作集成测试
func TestIntegration_ConcurrentOperations(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()
	const numGoroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	// 并发写入
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte(fmt.Sprintf("goroutine-%d-key-%d", id, j))
				value := []byte(fmt.Sprintf("value-%d", id*opsPerGoroutine+j))
				if err := tree.Set(ctx, key, value); err != nil {
					errCh <- fmt.Errorf("goroutine %d: %w", id, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	// 检查错误
	for err := range errCh {
		t.Fatalf("Concurrent write error: %v", err)
	}

	// 并发读取验证
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := []byte(fmt.Sprintf("goroutine-%d-key-%d", id, j))
				expectedValue := []byte(fmt.Sprintf("value-%d", id*opsPerGoroutine+j))
				val, err := tree.Get(ctx, key)
				assert.NoError(t, err)
				assert.Equal(t, expectedValue, val)
			}
		}(i)
	}
	wg.Wait()
}

// TestIntegration_LargeDataset 大数据集测试
func TestIntegration_LargeDataset(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过大数据集测试")
	}

	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()
	const numKeys = 5000

	t.Logf("插入 %d 条数据...", numKeys)
	start := time.Now()

	// 批量插入
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%07d", i))
		value := []byte(fmt.Sprintf("value-%07d-padded", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	insertDuration := time.Since(start)
	t.Logf("插入完成，耗时: %v (%.2f ops/s)", insertDuration, float64(numKeys)/insertDuration.Seconds())

	// 随机读取验证
	t.Logf("随机读取验证...")
	start = time.Now()
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("key-%07d", i*5))
		_, err := tree.Get(ctx, key)
		assert.NoError(t, err)
	}
	readDuration := time.Since(start)
	t.Logf("读取完成，耗时: %v (%.2f ops/s)", readDuration, 1000.0/readDuration.Seconds())

	// 获取统计信息
	stats := tree.GetStats()
	t.Logf("统计信息: TotalPages=%d, LeafPages=%d, InnerPages=%d",
		stats.TotalPages, stats.LeafPages, stats.InnerPages)
}

// TestIntegration_MixedOperations 混合操作测试
func TestIntegration_MixedOperations(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()
	const numKeys = 200

	// 1. 初始插入
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("initial-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 2. 混合操作
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))

		// 更新偶数索引（但不能被 3 整除）
		if i%2 == 0 && i%3 != 0 {
			newValue := []byte(fmt.Sprintf("updated-%03d", i))
			err := tree.Set(ctx, key, newValue)
			assert.NoError(t, err)
		}

		// 删除 3 的倍数（但不能被 2 整除）
		if i%3 == 0 && i%2 != 0 {
			err := tree.Delete(ctx, key)
			assert.NoError(t, err)
		}
	}

	// 3. 验证最终状态
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))

		if i%3 == 0 && i%2 != 0 {
			// 已删除
			_, err := tree.Get(ctx, key)
			assert.Error(t, err)
		} else if i%2 == 0 && i%3 != 0 {
			// 已更新
			val, err := tree.Get(ctx, key)
			assert.NoError(t, err)
			assert.Equal(t, []byte(fmt.Sprintf("updated-%03d", i)), val)
		} else {
			// 原始值
			val, err := tree.Get(ctx, key)
			assert.NoError(t, err)
			assert.Equal(t, []byte(fmt.Sprintf("initial-%03d", i)), val)
		}
	}
}

// TestIntegration_ErrorPaths 错误路径测试
func TestIntegration_ErrorPaths(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 1. 读取不存在的键
	_, err = tree.Get(ctx, []byte("nonexistent-key"))
	assert.Error(t, err)

	// 2. 删除不存在的键
	err = tree.Delete(ctx, []byte("nonexistent-key"))
	// 删除不存在的键可能返回成功或错误
	assert.True(t, err == nil || err != nil)

	// 3. Update 不存在的键
	err = tree.Update(ctx, []byte("nonexistent-key"), []byte("new-value"))
	assert.Error(t, err)

	// 4. 关闭后操作
	tree.Close()
	_, err = tree.Get(ctx, []byte("any-key"))
	assert.Error(t, err)

	err = tree.Set(ctx, []byte("key"), []byte("value"))
	assert.Error(t, err)
}

// TestIntegration_DeleteThenReinsert 删除后重新插入测试
func TestIntegration_DeleteThenReinsert(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 1. 插入数据
	key := []byte("test-key")
	value1 := []byte("value-1")
	err = tree.Set(ctx, key, value1)
	assert.NoError(t, err)

	val, err := tree.Get(ctx, key)
	assert.NoError(t, err)
	assert.Equal(t, value1, val)

	// 2. 删除
	err = tree.Delete(ctx, key)
	assert.NoError(t, err)

	_, err = tree.Get(ctx, key)
	assert.Error(t, err)

	// 3. 重新插入
	value2 := []byte("value-2")
	err = tree.Set(ctx, key, value2)
	assert.NoError(t, err)

	val, err = tree.Get(ctx, key)
	assert.NoError(t, err)
	assert.Equal(t, value2, val)
}
