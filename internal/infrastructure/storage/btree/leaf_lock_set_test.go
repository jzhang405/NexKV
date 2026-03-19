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

// TestSetWithLeafLock_Success 验证基本写入流程
func TestSetWithLeafLock_Success(t *testing.T) {
	// 创建纯内存 BTree（使用空字符串）
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// 插入键值对
	key := []byte("test-key")
	value := []byte("test-value")

	err = btree.Set(ctx, key, value)
	require.NoError(t, err, "Set should succeed")

	// 验证值已插入
	retrieved, err := btree.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value, retrieved)
}

// TestSetWithLeafLock_Update 验证更新现有键
func TestSetWithLeafLock_Update(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	key := []byte("test-key")
	value1 := []byte("value1")
	value2 := []byte("value2")

	// 插入初始值
	err = btree.Set(ctx, key, value1)
	require.NoError(t, err)

	// 更新值
	err = btree.Set(ctx, key, value2)
	require.NoError(t, err)

	// 验证已更新
	retrieved, err := btree.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value2, retrieved)
}

// TestSetWithLeafLock_Concurrent 验证并发写入安全性
func TestSetWithLeafLock_Concurrent(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()
	const goroutines = 100
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	errors := make(chan error, goroutines*operationsPerGoroutine)

	// 并发写入不同的键
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				key := []byte{byte(id >> 8), byte(id), byte(j)}
				value := []byte{byte(j)}

				err := btree.Set(ctx, key, value)
				if err != nil {
					errors <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 检查是否有错误
	for err := range errors {
		t.Errorf("Concurrent Set failed: %v", err)
	}
}

// TestSetWithLeafLock_MultipleKeys 验证多键写入
func TestSetWithLeafLock_MultipleKeys(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// 插入多个键值对
	const count = 1000
	for i := 0; i < count; i++ {
		key := []byte{byte(i >> 8), byte(i)}
		value := []byte{byte(i), byte(i >> 8), byte(i >> 16)}

		err := btree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 验证所有值
	for i := 0; i < count; i++ {
		key := []byte{byte(i >> 8), byte(i)}
		value, err := btree.Get(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, value)
	}
}

// TestFindLeafPageRef_Success 验证 findLeafPageRef 正确返回 PageRef
func TestFindLeafPageRef_Success(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// 插入一个键
	key := []byte("test-key")
	value := []byte("test-value")
	_ = btree.Set(ctx, key, value)

	// 查找 PageRef
	leafRef, path, err := btree.findLeafPageRef(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, leafRef, "leafRef should not be nil")
	require.NotEmpty(t, path, "path should not be empty")

	// 验证 PageRef 可以获取锁
	pageLock := leafRef.GetLock()
	assert.NotNil(t, pageLock, "pageLock should not be nil")
}

// TestFindLeafPageRef_Concurrent 验证 findLeafPageRef 并发安全性
func TestFindLeafPageRef_Concurrent(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// 预先插入一些键
	const count = 100
	for i := 0; i < count; i++ {
		key := []byte{byte(i >> 8), byte(i)}
		_ = btree.Set(ctx, key, []byte{byte(i)})
	}

	const goroutines = 50
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				key := []byte{byte((id*10+j) >> 8), byte(id*10 + j)}
				_, _, err := btree.findLeafPageRef(ctx, key)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()
}

// BenchmarkSetWithLeafLock 性能基准测试
func BenchmarkSetWithLeafLock(b *testing.B) {
	b.StopTimer()
	btree, err := OpenBTree("", nil) // 使用空字符串表示纯内存模式
	require.NoError(b, err)
	defer btree.Close()

	ctx := context.Background()
	key := []byte("benchmark-key")
	value := []byte("benchmark-value")

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		_ = btree.Set(ctx, key, value)
	}
}

// BenchmarkSetWithLeafLock_Concurrent 并发写入性能基准测试
func BenchmarkSetWithLeafLock_Concurrent(b *testing.B) {
	b.StopTimer()
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	ctx := context.Background()

	b.StartTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := []byte{0, 0, 0} // 使用相同键测试锁竞争
		value := []byte{1, 2, 3}
		for pb.Next() {
			_ = btree.Set(ctx, key, value)
		}
	})
}

// BenchmarkSetWithLeafLock_DifferentKeys 不同键并发写入性能基准测试
func BenchmarkSetWithLeafLock_DifferentKeys(b *testing.B) {
	b.StopTimer()
	btree, err := OpenBTree("", nil)
	require.NoError(b, err)
	defer btree.Close()

	ctx := context.Background()

	b.StartTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := []byte{0, 0, 0} // 实际运行时会有不同的 goroutine
		value := []byte{1, 2, 3}
		for pb.Next() {
			// 使用递增的键模拟不同键
			key[2]++
			_ = btree.Set(ctx, key, value)
		}
	})
}
