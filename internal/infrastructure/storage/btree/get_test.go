// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGet_KeyNotFound 测试获取不存在的键
func TestGet_KeyNotFound(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 尝试获取不存在的键
	key := []byte("non-existent-key")
	value, err := tree.Get(ctx, key)

	// 应该返回错误
	assert.Error(t, err)
	assert.Nil(t, value)
}

// TestGet_EmptyTree 测试从空树获取
func TestGet_EmptyTree(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 从空树获取
	key := []byte("key")
	value, err := tree.Get(ctx, key)

	// 应该返回错误
	assert.Error(t, err)
	assert.Nil(t, value)
}

// TestGet_SingleKey 测试获取单个键
func TestGet_SingleKey(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入一个键
	key := []byte("key")
	expectedValue := []byte("value")
	err = tree.Set(ctx, key, expectedValue)
	require.NoError(t, err)

	// 获取键
	value, err := tree.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, expectedValue, value)
}

// TestGet_MultipleKeys 测试获取多个键
func TestGet_MultipleKeys(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入多个键
	const keyCount = 50
	keys := make([][]byte, keyCount)
	values := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		keys[i] = key
		values[i] = value
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 获取所有键
	for i := 0; i < keyCount; i++ {
		value, err := tree.Get(ctx, keys[i])
		require.NoError(t, err)
		assert.Equal(t, values[i], value)
	}
}

// TestGet_Overwrite 测试覆盖写入后获取
func TestGet_Overwrite(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	key := []byte("key")

	// 第一次写入
	value1 := []byte("value1")
	err = tree.Set(ctx, key, value1)
	require.NoError(t, err)

	retrieved, err := tree.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value1, retrieved)

	// 第二次写入（覆盖）
	value2 := []byte("value2")
	err = tree.Set(ctx, key, value2)
	require.NoError(t, err)

	retrieved, err = tree.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value2, retrieved)
}

// TestGet_ConcurrentSameKey 测试并发获取相同键
func TestGet_ConcurrentSameKey(t *testing.T) {
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

	// 并发获取相同的键
	const goroutines = 100
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			retrieved, err := tree.Get(ctx, key)
			require.NoError(t, err)
			assert.Equal(t, value, retrieved)
		}()
	}

	wg.Wait()
}

// TestGet_ConcurrentDifferentKeys 测试并发获取不同键
func TestGet_ConcurrentDifferentKeys(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入多个键
	const keyCount = 20
	for i := 0; i < keyCount; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 并发获取不同的键
	const goroutines = 20
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := []byte{byte(id)}
			_, err := tree.Get(ctx, key)
			require.NoError(t, err)
		}(i)
	}

	wg.Wait()
}

// TestGet_LargeKey 测试获取大键
func TestGet_LargeKey(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 创建一个较大的键
	largeKey := make([]byte, 1024)
	for i := 0; i < len(largeKey); i++ {
		largeKey[i] = byte(i % 256)
	}

	largeValue := []byte("large-value")

	// 插入大键
	err = tree.Set(ctx, largeKey, largeValue)
	require.NoError(t, err)

	// 获取大键
	retrieved, err := tree.Get(ctx, largeKey)
	require.NoError(t, err)
	assert.Equal(t, largeValue, retrieved)
}

// TestGet_AfterDelete 测试删除后获取
func TestGet_AfterDelete(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	key := []byte("key")
	value := []byte("value")

	// 插入键
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

// TestGet_EmptyKey 测试空键
func TestGet_EmptyKey(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 尝试获取空键
	emptyKey := []byte{}
	value, err := tree.Get(ctx, emptyKey)

	// 应该返回错误或处理空键
	// 根据实现，我们期望它返回错误
	assert.Error(t, err)
	assert.Nil(t, value)
}

// TestGet_NilKey 测试 nil 键
func TestGet_NilKey(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 尝试获取 nil 键
	value, err := tree.Get(ctx, nil)

	// 应该返回错误
	assert.Error(t, err)
	assert.Nil(t, value)
}
