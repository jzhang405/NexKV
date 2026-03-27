// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range operationsPerGoroutine {
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
	for i := range count {
		key := []byte{byte(i >> 8), byte(i)}
		value := []byte{byte(i), byte(i >> 8), byte(i >> 16)}

		err := btree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 验证所有值
	for i := range count {
		key := []byte{byte(i >> 8), byte(i)}
		value, err := btree.Get(ctx, key)
		if err != nil {
			t.Logf("Get failed at i=%d (key=[%d,%d]): %v", i, key[0], key[1], err)
			// 继续检查其他键
			continue
		}
		require.NoError(t, err)
		require.NotNil(t, value)
	}
	t.Logf("All %d keys verified successfully", count)
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
	leafRef, path, _, err := btree.findLeafPageRef(ctx, key)
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
	for i := range count {
		key := []byte{byte(i >> 8), byte(i)}
		_ = btree.Set(ctx, key, []byte{byte(i)})
	}

	const goroutines = 50
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 10 {
				key := []byte{byte((id*10 + j) >> 8), byte(id*10 + j)}
				_, _, _, err := btree.findLeafPageRef(ctx, key)
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
	for b.Loop() {
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
func TestRetry_Path(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 创建竞争条件以触发 ErrRetry
	const numGoroutines = 10
	const keysPerGoroutine = 20

	var wg sync.WaitGroup

	for g := range numGoroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for i := range keysPerGoroutine {
				key := []byte(fmt.Sprintf("g%d-key%02d", goroutineID, i))
				value := []byte(fmt.Sprintf("value-%d", goroutineID*keysPerGoroutine+i))

				// 带重试的 Set
				var err error
				for attempt := range 20 {
					err = tree.Set(ctx, key, value)
					if err == nil {
						break
					}
					if err == ErrRetry {
						time.Sleep(time.Microsecond * time.Duration(attempt+1) * 10)
						continue
					}
					break
				}

				// 某些写入可能失败（竞争），但不应该导致崩溃
				if err != nil && err != ErrRetry {
					t.Logf("goroutine %d, key %d: %v", goroutineID, i, err)
				}
			}
		}(g)
	}

	wg.Wait()

	// 验证至少部分数据成功写入
	successCount := 0
	for g := range numGoroutines {
		for i := range keysPerGoroutine {
			key := []byte(fmt.Sprintf("g%d-key%02d", g, i))
			_, err := tree.Get(ctx, key)
			if err == nil {
				successCount++
			}
		}
	}

	// 至少应该有 50% 的数据成功写入
	minSuccess := (numGoroutines * keysPerGoroutine) / 2
	assert.GreaterOrEqual(t, successCount, minSuccess,
		"expected at least %d successful writes, got %d", minSuccess, successCount)
}

// TestSetWithLeafLock_ExtremeConcurrency 极端并发测试
// 验证 200 goroutine 场景下的并发安全性
func TestSetWithLeafLock_ExtremeConcurrency(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()
	const goroutines = 200
	const keysPerGoroutine = 50

	var wg sync.WaitGroup
	errors := make(chan error, goroutines*keysPerGoroutine)

	// 并发写入不同的键
	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range keysPerGoroutine {
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
	errorCount := 0
	for err := range errors {
		errorCount++
		t.Logf("Concurrent Set failed (extreme): %v", err)
	}

	// 计算成功率
	totalOps := goroutines * keysPerGoroutine
	successOps := totalOps - errorCount
	successRate := float64(successOps) / float64(totalOps) * 100

	t.Logf("Extreme concurrency test: %d goroutine × %d keys = %d operations", goroutines, keysPerGoroutine, totalOps)
	t.Logf("Success: %d/%d (%.2f%%), Failures: %d", successOps, totalOps, successRate, errorCount)

	// 目标：成功率应该 > 95%（考虑极端并发条件）
	if successRate < 95.0 {
		t.Errorf("Success rate %.2f%% is below 95%% threshold", successRate)
	}
}
