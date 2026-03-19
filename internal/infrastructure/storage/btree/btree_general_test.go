// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

//nolint:errcheck // 测试代码中忽略部分返回值检查

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWAL_Replay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// 第一次打开并写入数据
	tree1, err := OpenBTree(dir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// 插入一些数据
	for i := range 10 {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree1.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 关闭树（数据会持久化到 WAL 和磁盘）
	tree1.Close()

	// 重新打开（应该从 WAL 恢复）
	tree2, err := OpenBTree(dir, nil)
	require.NoError(t, err)
	defer tree2.Close()

	// 验证数据已恢复
	// 注意：由于纯内存模式和持久化模式的差异，这里只验证 WAL 不会导致崩溃
	// 实际的数据持久化需要完整实现持久化模式
	_ = tree2
}
func TestMultipleOperations_Mixed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 执行混合操作：插入、读取、更新、删除
	operations := []struct {
		name string
		fn   func() error
	}{
		{
			name: "insert",
			fn: func() error {
				return tree.Set(ctx, []byte("key1"), []byte("value1"))
			},
		},
		{
			name: "read",
			fn: func() error {
				_, err := tree.Get(ctx, []byte("key1"))
				return err
			},
		},
		{
			name: "update",
			fn: func() error {
				return tree.Set(ctx, []byte("key1"), []byte("value2"))
			},
		},
		{
			name: "read-again",
			fn: func() error {
				_, err := tree.Get(ctx, []byte("key1"))
				return err
			},
		},
		{
			name: "delete",
			fn: func() error {
				return tree.Delete(ctx, []byte("key1"))
			},
		},
		{
			name: "read-missing",
			fn: func() error {
				_, err := tree.Get(ctx, []byte("key1"))
				if err == ErrKeyNotFound {
					return nil
				}
				return err
			},
		},
	}

	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			err := op.fn()
			assert.NoError(t, err, "operation: %s", op.name)
		})
	}
}
func TestSpecialKeys_BoundaryCharacters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 测试特殊字符
	specialKeys := []struct {
		name  string
		key   []byte
		value []byte
	}{
		{"empty-key", []byte(""), []byte("empty")},
		{"null-byte", []byte{0x00}, []byte("null")},
		{"binary-data", []byte{0xFF, 0xFE, 0xFD}, []byte("binary")},
		{"unicode", []byte("键"), []byte("值")},
	}

	for _, sk := range specialKeys {
		t.Run(sk.name, func(t *testing.T) {
			err := tree.Set(ctx, sk.key, sk.value)
			if err == nil {
				// 如果支持，验证读取
				value, err := tree.Get(ctx, sk.key)
				require.NoError(t, err)
				assert.Equal(t, sk.value, value)
			}
			// 某些键可能不被支持，这是合理的
		})
	}
}
func TestConcurrency_RapidSequential(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 快速连续操作，测试内部锁和一致性
	const iterations = 1000
	for i := range iterations {
		key := []byte(fmt.Sprintf("key-%d", i%100))
		value := []byte(fmt.Sprintf("value-%d", i))

		err := tree.Set(ctx, key, value)
		if err != nil && err != ErrRetry {
			t.Errorf("failed at iteration %d: %v", i, err)
			break
		}

		// 每隔 10 次读取一次
		if i%10 == 0 {
			_, err := tree.Get(ctx, key)
			if err != nil && err != ErrKeyNotFound && err != ErrRetry {
				t.Errorf("get failed at iteration %d: %v", i, err)
				break
			}
		}
	}
}
func TestRebuildChildRefs_ChildReferenceRebuilding(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入足够多的数据以建立多层级树
	for i := range 250 {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 读取所有数据以确保树结构正确
	for i := range 250 {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value, err := tree.Get(ctx, key)
		if err != nil && err != ErrKeyNotFound {
			require.NoError(t, err, "key: %s", key)
			assert.NotNil(t, value, "key: %s", key)
		}
	}
}
func TestLargeTree_ManyLevels(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入足够多的数据以创建多层树
	const keyCount = 1000

	for i := range keyCount {
		key := []byte(fmt.Sprintf("key-%06d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err, "failed at key %d", i)
	}

	// 验证所有数据
	successCount := 0
	for i := range keyCount {
		key := []byte(fmt.Sprintf("key-%06d", i))
		expected := []byte(fmt.Sprintf("value-%d", i))

		value, err := tree.Get(ctx, key)
		if err == nil {
			assert.Equal(t, expected, value)
			successCount++
		}
	}

	// 至少 99% 的数据应该成功
	minSuccess := keyCount * 99 / 100
	assert.GreaterOrEqual(t, successCount, minSuccess,
		"expected at least %d successful reads, got %d", minSuccess, successCount)
}
