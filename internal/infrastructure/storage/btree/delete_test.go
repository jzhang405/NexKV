// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this license code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDelete_KeyNotFound 测试删除不存在的键
func TestDelete_KeyNotFound(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 尝试删除不存在的键
	key := []byte("non-existent-key")
	err = tree.Delete(ctx, key)

	// 删除不存在的键应该返回错误
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

// TestDelete_EmptyTree 测试从空树删除
func TestDelete_EmptyTree(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 从空树删除
	key := []byte("key")
	err = tree.Delete(ctx, key)

	// 删除不存在的键应该返回错误
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)

	// 验证树仍然是空的
	value, err := tree.Get(ctx, key)
	assert.Error(t, err)
	assert.Nil(t, value)
}

// TestDelete_SingleKey 测试删除单个键
func TestDelete_SingleKey(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入一个键
	key := []byte("key")
	value := []byte("value")
	err = tree.Set(ctx, key, value)
	require.NoError(t, err)

	// 验证键存在
	retrieved, err := tree.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value, retrieved)

	// 删除键
	err = tree.Delete(ctx, key)
	require.NoError(t, err)

	// 验证键不存在
	retrieved, err = tree.Get(ctx, key)
	assert.Error(t, err)
	assert.Nil(t, retrieved)
}

// TestDelete_MultipleKeys 测试删除多个键
func TestDelete_MultipleKeys(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入多个键
	const keyCount = 20
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		key := []byte{byte(i)}
		keys[i] = key
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 删除一半的键
	for i := 0; i < keyCount/2; i++ {
		err := tree.Delete(ctx, keys[i])
		require.NoError(t, err)
	}

	// 验证删除的键不存在
	for i := 0; i < keyCount/2; i++ {
		_, err := tree.Get(ctx, keys[i])
		assert.Error(t, err)
	}

	// 验证剩余的键存在
	for i := keyCount / 2; i < keyCount; i++ {
		value, err := tree.Get(ctx, keys[i])
		require.NoError(t, err)
		assert.NotNil(t, value)
	}
}

// TestDelete_TriggerMerge 测试删除触发合并
func TestDelete_TriggerMerge(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入刚好够触发合并的键
	// 假设 minKeys = 4，我们需要创建接近 minKeys 的节点
	// 然后删除键触发合并
	for i := 0; i < 15; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 删除中间的键，可能触发合并
	middleKey := []byte{7}
	err = tree.Delete(ctx, middleKey)
	require.NoError(t, err)

	// 验证删除成功
	_, err = tree.Get(ctx, middleKey)
	assert.Error(t, err)
}

// TestDelete_ConcurrentSameKey 测试并发删除相同键
func TestDelete_ConcurrentSameKey(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入一个键
	key := []byte("concurrent-key")
	value := []byte("value")
	err = tree.Set(ctx, key, value)
	require.NoError(t, err)

	// 并发删除相同的键
	const goroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := tree.Delete(ctx, key)
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// 应该只有一个删除成功，其他的可能返回错误
	// 或者都成功（幂等操作）
	errorCount := 0
	for err := range errors {
		if err != nil {
			errorCount++
		}
	}

	// 验证键最终被删除
	value, err = tree.Get(ctx, key)
	assert.Error(t, err)
	assert.Nil(t, value)

	t.Logf("Concurrent delete completed with %d errors", errorCount)
}

// TestDelete_AllKeys 测试删除所有键
func TestDelete_AllKeys(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入多个键
	const keyCount = 50
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		key := []byte{byte(i)}
		keys[i] = key
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 删除所有键（忽略不存在的键）
	for _, key := range keys {
		_ = tree.Delete(ctx, key)
	}

	// 验证所有键都被删除
	for _, key := range keys {
		_, err := tree.Get(ctx, key)
		assert.Error(t, err)
	}
}

// TestDelete_RandomOrder 测试随机顺序删除
func TestDelete_RandomOrder(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入多个键
	const keyCount = 30
	insertedKeys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		key := []byte{byte(i)}
		insertedKeys[i] = key
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 以随机顺序删除（使用反向顺序模拟）
	for i := keyCount - 1; i >= 0; i-- {
		err := tree.Delete(ctx, insertedKeys[i])
		require.NoError(t, err)
	}

	// 验证所有键都被删除
	for _, key := range insertedKeys {
		_, err := tree.Get(ctx, key)
		assert.Error(t, err)
	}
}

// TestDelete_BoundaryKeys 测试边界键删除
func TestDelete_BoundaryKeys(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入一些键
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 删除第一个键
	firstKey := []byte{0}
	err = tree.Delete(ctx, firstKey)
	require.NoError(t, err)

	// 删除最后一个键
	lastKey := []byte{9}
	err = tree.Delete(ctx, lastKey)
	require.NoError(t, err)

	// 验证删除成功
	_, err = tree.Get(ctx, firstKey)
	assert.Error(t, err)

	_, err = tree.Get(ctx, lastKey)
	assert.Error(t, err)
}
