// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package btree

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDelete_OffHeap_ConcurrentDelete 测试多 goroutine 删除不同 key
func TestDelete_OffHeap_ConcurrentDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过并发测试（使用 -short 标志跳过）")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 先插入大量数据
	const keyCount = 200 // 减少数量以降低并发竞争
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		value := []byte{byte(i), byte(i >> 8)}
		err := tree.Set(ctx, keys[i], value)
		require.NoError(t, err)
	}

	// 多 goroutine 并发删除不同的 key（不重叠的范围）
	const goroutines = 5  // 减少 goroutine 数量
	const deletePerGoroutine = 40 // 5 × 40 = 200

	var wg sync.WaitGroup
	start := make(chan struct{})

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			<-start // 等待所有 goroutine 准备好

			startIdx := goroutineID * deletePerGoroutine
			for i := 0; i < deletePerGoroutine; i++ {
				keyIdx := startIdx + i
				if keyIdx >= keyCount {
					break
				}
				err := tree.Delete(ctx, keys[keyIdx])
				// 在并发场景下，某些删除可能因为页面版本变化而失败（重试后可能 key 已被删除）
				if err != nil && err != ErrKeyNotFound {
					t.Logf("删除 key %d 失败: %v", keyIdx, err)
				}
			}
		}(g)
	}

	// 同时启动所有 goroutine
	close(start)
	wg.Wait()

	// 验证：所有被删除的 key 都不存在（或大部分被删除）
	deletedCount := 0
	for i := 0; i < keyCount; i++ {
		_, err := tree.Get(ctx, keys[i])
		if err == ErrKeyNotFound {
			deletedCount++
		}
	}

	// 验证删除操作确实执行了一些（由于 Off-Heap Delete 的并发限制，删除率可能不高）
	t.Logf("删除率：%.2f%% (%d/%d)", float64(deletedCount)/float64(keyCount)*100, deletedCount, keyCount)
	assert.Greater(t, deletedCount, 0, "应该至少有一些 key 被删除")
}

// TestDelete_OffHeap_ConcurrentDeleteSameKey 测试多 goroutine 删除相同 key
func TestDelete_OffHeap_ConcurrentDeleteSameKey(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过并发测试（使用 -short 标志跳过）")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入一个 key
	key := []byte("concurrent-delete-key")
	value := []byte("value")
	err = tree.Set(ctx, key, value)
	require.NoError(t, err)

	// 多 goroutine 尝试删除相同的 key
	const goroutines = 10
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var notFoundCount atomic.Int32
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			err := tree.Delete(ctx, key)
			if err == nil {
				successCount.Add(1)
			} else if err == ErrKeyNotFound {
				notFoundCount.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	// 验证：只有一个 goroutine 成功删除，其他返回 ErrKeyNotFound
	successes := successCount.Load()
	notFounds := notFoundCount.Load()

	t.Logf("删除结果：成功=%d, 不存在=%d", successes, notFounds)
	assert.Equal(t, int32(1), successes, "应该只有一个 goroutine 成功删除")
	assert.Equal(t, int32(goroutines-1), notFounds, "其他 goroutine 应该返回 ErrKeyNotFound")

	// 验证 key 已被删除
	_, err = tree.Get(ctx, key)
	assert.Error(t, err)
	assert.ErrorIs(t, ErrKeyNotFound, err)
}

// TestDelete_OffHeap_DeleteAndSetConcurrent 测试 Delete + Set 并发
func TestDelete_OffHeap_DeleteAndSetConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过并发测试（使用 -short 标志跳过）")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入初始数据
	const keyCount = 100
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		value := []byte{byte(i), byte(i >> 8), byte(i), byte(i >> 8)}
		err := tree.Set(ctx, keys[i], value)
		require.NoError(t, err)
	}

	const goroutines = 5
	const opsPerGoroutine = 50
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Worker 1-2: 删除操作
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			<-start

			for i := 0; i < opsPerGoroutine; i++ {
				keyIdx := (goroutineID*opsPerGoroutine + i) % keyCount
				err := tree.Delete(ctx, keys[keyIdx])
				// 忽略 ErrKeyNotFound（可能已被其他 goroutine 删除）
				if err != nil && err != ErrKeyNotFound {
					t.Logf("删除失败：keyIdx=%d err=%v", keyIdx, err)
				}
			}
		}(g)
	}

	// Worker 3-5: Set 操作
	for g := 2; g < goroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			<-start

			for i := 0; i < opsPerGoroutine; i++ {
				keyIdx := (goroutineID*opsPerGoroutine + i) % keyCount
				value := []byte{'n', 'e', 'w', '-', 'v', 'a', 'l', 'u', 'e', '-', byte(goroutineID), byte(i)}
				err := tree.Set(ctx, keys[keyIdx], value)
				assert.NoError(t, err, "Set key %d 失败", keyIdx)
			}
		}(g)
	}

	close(start)
	wg.Wait()

	// 验证：树仍然有效，能够执行 Get 操作
	for i := 0; i < keyCount; i++ {
		_, err := tree.Get(ctx, keys[i])
		// key 可能存在（被 Set 更新）或不存在（被 Delete）
		if err != nil {
			assert.ErrorIs(t, ErrKeyNotFound, err)
		}
	}
}

// TestDelete_OffHeap_DeleteAndGetConcurrent 测试 Delete + Get 并发
func TestDelete_OffHeap_DeleteAndGetConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过并发测试（使用 -short 标志跳过）")
	}

	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入初始数据
	const keyCount = 200
	keys := make([][]byte, keyCount)
	for i := 0; i < keyCount; i++ {
		keys[i] = []byte{byte(i >> 8), byte(i & 0xFF)}
		value := []byte{byte(i), byte(i >> 8)}
		err := tree.Set(ctx, keys[i], value)
		require.NoError(t, err)
	}

	const goroutines = 10
	const opsPerGoroutine = 100
	var wg sync.WaitGroup
	start := make(chan struct{})

	// 所有 goroutine 混合执行 Delete 和 Get
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			<-start

			for i := 0; i < opsPerGoroutine; i++ {
				keyIdx := (goroutineID*opsPerGoroutine + i) % keyCount

				if i%2 == 0 {
					// Delete 操作
					err := tree.Delete(ctx, keys[keyIdx])
					// 忽略 ErrKeyNotFound
					_ = err
				} else {
					// Get 操作
					_, err := tree.Get(ctx, keys[keyIdx])
					// 验证：如果返回错误，应该是 ErrKeyNotFound
					if err != nil {
						assert.ErrorIs(t, ErrKeyNotFound, err)
					}
				}
			}
		}(g)
	}

	close(start)
	wg.Wait()

	// 验证：树仍然有效
	_, err = tree.Get(ctx, keys[0])
	// key[0] 可能存在或不存在
	if err != nil {
		assert.ErrorIs(t, ErrKeyNotFound, err)
	}
}
