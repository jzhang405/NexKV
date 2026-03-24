// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

//nolint:errcheck // 测试代码中忽略部分返回值检查

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOffHeap_BasicCRUD 测试 Off-Heap 模式下的基本 CRUD 操作
func TestOffHeap_BasicCRUD(t *testing.T) {
	ctx := context.Background()

	// 创建纯内存模式的 BTree（Off-Heap，无持久化）
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Run("Set and Get single key", func(t *testing.T) {
		key := []byte("test-key")
		value := []byte("test-value")

		// Set
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)

		// Get
		got, err := tree.Get(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, value, got)
	})

	t.Run("Get non-existent key returns ErrKeyNotFound", func(t *testing.T) {
		key := []byte("non-existent")

		_, err := tree.Get(ctx, key)
		assert.Error(t, err)
		assert.Equal(t, ErrKeyNotFound, err)
	})

	t.Run("Set updates existing key", func(t *testing.T) {
		key := []byte("update-key")
		value1 := []byte("value1")
		value2 := []byte("value2")

		// Set first value
		err := tree.Set(ctx, key, value1)
		require.NoError(t, err)

		got1, err := tree.Get(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, value1, got1)

		// Update with second value
		err = tree.Set(ctx, key, value2)
		require.NoError(t, err)

		got2, err := tree.Get(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, value2, got2)
	})

	t.Run("Set and Get multiple keys", func(t *testing.T) {
		keys := make([][]byte, 100)
		values := make([][]byte, 100)

		for i := 0; i < 100; i++ {
			keys[i] = []byte(fmt.Sprintf("multi-key-%d", i))
			values[i] = []byte(fmt.Sprintf("multi-value-%d", i))
		}

		// Set all keys
		for i := 0; i < 100; i++ {
			err := tree.Set(ctx, keys[i], values[i])
			require.NoError(t, err, "Failed to set key %d", i)
		}

		// Get all keys
		for i := 0; i < 100; i++ {
			got, err := tree.Get(ctx, keys[i])
			require.NoError(t, err, "Failed to get key %d: %s", i, string(keys[i]))
			assert.Equal(t, values[i], got, "Value mismatch for key %d", i)
		}
	})
}

// TestOffHeap_LeafSplit 测试叶子节点分裂
func TestOffHeap_LeafSplit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Run("Insert 200 keys to trigger leaf split", func(t *testing.T) {
		// 假设每个叶子页面可以存储约 100 个条目
		// 插入 200 个键应该触发至少一次叶子分裂
		const numKeys = 200

		keys := make([][]byte, numKeys)
		values := make([][]byte, numKeys)

		for i := 0; i < numKeys; i++ {
			keys[i] = []byte(fmt.Sprintf("leaf-split-key-%04d", i))
			values[i] = []byte(fmt.Sprintf("value-%d", i))

			err := tree.Set(ctx, keys[i], values[i])
			require.NoError(t, err)
		}

		// 验证所有键都可以读取
		for i := 0; i < numKeys; i++ {
			got, err := tree.Get(ctx, keys[i])
			require.NoError(t, err)
			assert.Equal(t, values[i], got)
		}
	})
}

// TestOffHeap_InternalSplit 测试内部节点分裂
func TestOffHeap_InternalSplit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Run("Insert many keys to trigger internal split", func(t *testing.T) {
		// 假设每个叶子页面可以存储约 100 个条目
		// 每个内部页面可以存储约 200 个子节点引用
		// 插入足够多的键来触发内部节点分裂
		const numKeys = 1000

		keys := make([][]byte, numKeys)
		values := make([][]byte, numKeys)

		for i := 0; i < numKeys; i++ {
			keys[i] = []byte(fmt.Sprintf("internal-split-key-%05d", i))
			values[i] = []byte(fmt.Sprintf("value-%d", i))

			err := tree.Set(ctx, keys[i], values[i])
			require.NoError(t, err)
		}

		// 验证所有键都可以读取
		for i := 0; i < numKeys; i++ {
			got, err := tree.Get(ctx, keys[i])
			require.NoError(t, err)
			assert.Equal(t, values[i], got)
		}
	})
}

// TestOffHeap_RootSplit 测试根节点分裂（树高度增加）
func TestOffHeap_RootSplit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Run("Insert large dataset to trigger root split", func(t *testing.T) {
		// 插入足够多的键来触发根节点分裂（树高度从 1 增加到 2）
		// 假设初始根是叶子节点，可以存储约 100 个条目
		// 当根节点分裂时，会创建一个新的内部节点作为根
		const numKeys = 5000

		keys := make([][]byte, numKeys)
		values := make([][]byte, numKeys)

		for i := 0; i < numKeys; i++ {
			keys[i] = []byte(fmt.Sprintf("root-split-key-%06d", i))
			values[i] = []byte(fmt.Sprintf("value-%d", i))

			err := tree.Set(ctx, keys[i], values[i])
			require.NoError(t, err)
		}

		// 验证所有键都可以读取
		for i := 0; i < numKeys; i++ {
			got, err := tree.Get(ctx, keys[i])
			require.NoError(t, err)
			assert.Equal(t, values[i], got)
		}
	})
}

// TestOffHeap_ConcurrentReadWrite 测试并发读写
func TestOffHeap_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Run("Concurrent Set and Get", func(t *testing.T) {
		const numGoroutines = 10
		const numOpsPerGoroutine = 100

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// 用于验证的计数器
		var successCount atomic.Int64

		for g := 0; g < numGoroutines; g++ {
			go func(goroutineID int) {
				defer wg.Done()

				for i := 0; i < numOpsPerGoroutine; i++ {
					key := []byte(fmt.Sprintf("concurrent-key-%d-%d", goroutineID, i))
					value := []byte(fmt.Sprintf("value-%d", i))

					// Set
					err := tree.Set(ctx, key, value)
					if err != nil {
						t.Errorf("Set failed: %v", err)
						return
					}

					// Get
					got, err := tree.Get(ctx, key)
					if err != nil {
						t.Errorf("Get failed: %v", err)
						return
					}

					if got != nil && string(got) == string(value) {
						successCount.Add(1)
					}
				}
			}(g)
		}

		wg.Wait()

		// 验证至少有一定比例的操作成功
		expectedSuccess := int64(numGoroutines * numOpsPerGoroutine)
		assert.Equal(t, expectedSuccess, successCount.Load())
	})

	t.Run("Concurrent readers on same key", func(t *testing.T) {
		key := []byte("shared-key")
		value := []byte("shared-value")

		// 先设置一个键
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)

		const numReaders = 50
		var wg sync.WaitGroup
		wg.Add(numReaders)

		for i := 0; i < numReaders; i++ {
			go func() {
				defer wg.Done()
				got, err := tree.Get(ctx, key)
				require.NoError(t, err)
				assert.Equal(t, value, got)
			}()
		}

		wg.Wait()
	})
}

// TestOffHeap_TreeIntegrity 测试树结构完整性
func TestOffHeap_TreeIntegrity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Run("Insert sequential keys and verify", func(t *testing.T) {
		const numKeys = 1000

		// 插入顺序键
		for i := 0; i < numKeys; i++ {
			key := []byte(fmt.Sprintf("seq-key-%04d", i))
			value := []byte(fmt.Sprintf("value-%d", i))

			err := tree.Set(ctx, key, value)
			require.NoError(t, err)
		}

		// 验证所有键
		for i := 0; i < numKeys; i++ {
			key := []byte(fmt.Sprintf("seq-key-%04d", i))
			expectedValue := []byte(fmt.Sprintf("value-%d", i))

			got, err := tree.Get(ctx, key)
			require.NoError(t, err)
			assert.Equal(t, expectedValue, got)
		}
	})

	t.Run("Insert reverse keys and verify", func(t *testing.T) {
		const numKeys = 1000

		// 插入逆序键
		for i := numKeys - 1; i >= 0; i-- {
			key := []byte(fmt.Sprintf("rev-key-%04d", i))
			value := []byte(fmt.Sprintf("value-%d", i))

			err := tree.Set(ctx, key, value)
			require.NoError(t, err)
		}

		// 验证所有键
		for i := 0; i < numKeys; i++ {
			key := []byte(fmt.Sprintf("rev-key-%04d", i))
			expectedValue := []byte(fmt.Sprintf("value-%d", i))

			got, err := tree.Get(ctx, key)
			require.NoError(t, err)
			assert.Equal(t, expectedValue, got)
		}
	})
}

// TestOffHeap_MixedOperations 测试混合操作模式
func TestOffHeap_MixedOperations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Run("Read-Modify-Write pattern", func(t *testing.T) {
		key := []byte("rmw-key")
		initialValue := []byte("0")

		// 设置初始值
		err := tree.Set(ctx, key, initialValue)
		require.NoError(t, err)

		// 执行 10 次 Read-Modify-Write 操作
		for i := 1; i <= 10; i++ {
			// Read
			currentValue, err := tree.Get(ctx, key)
			require.NoError(t, err)

			// Modify（简单递增）
			newValue := []byte(fmt.Sprintf("%d", i))

			// Write
			err = tree.Set(ctx, key, newValue)
			require.NoError(t, err)

			// 验证
			got, err := tree.Get(ctx, key)
			require.NoError(t, err)
			assert.Equal(t, newValue, got)
			assert.NotEqual(t, currentValue, got)
		}
	})
}

// TestOffHeap_EdgeCases 测试边界情况
func TestOffHeap_EdgeCases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	t.Run("Empty key", func(t *testing.T) {
		key := []byte("")
		value := []byte("empty-key-value")

		err := tree.Set(ctx, key, value)
		// 可能会返回错误，但不应该 panic
		if err == nil {
			got, err := tree.Get(ctx, key)
			if err == nil {
				assert.Equal(t, value, got)
			}
		}
	})

	t.Run("Empty value", func(t *testing.T) {
		key := []byte("empty-value-key")
		value := []byte("")

		err := tree.Set(ctx, key, value)
		require.NoError(t, err)

		got, err := tree.Get(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, value, got)
	})

	t.Run("Large key", func(t *testing.T) {
		largeKey := make([]byte, 1024) // 1KB key
		for i := range largeKey {
			largeKey[i] = byte(i % 256)
		}
		value := []byte("large-key-value")

		err := tree.Set(ctx, largeKey, value)
		// 可能会返回错误（key 太大），但不应该 panic
		if err == nil {
			got, err := tree.Get(ctx, largeKey)
			if err == nil {
				assert.Equal(t, value, got)
			}
		}
	})
}
