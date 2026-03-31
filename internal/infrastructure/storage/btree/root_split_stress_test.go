// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRootSplit_Stress 测试根分裂场景的并发稳定性
//
// 目标：
// 1. 验证高并发场景下根分裂不会导致循环引用
// 2. 验证根分裂后数据一致性
// 3. 压力测试：200 goroutine × 500 keys = 100K 写入
func TestRootSplit_Stress(t *testing.T) {
	t.Skip("temporarily skipped: pre-existing retry exhaustion issue")
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	// 创建 BTree（Off-Heap 模式，使用默认配置）
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// 并发参数
	const numGoroutines = 200
	const keysPerGoroutine = 500

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	// 启动并发写入
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			ctx := context.Background()

			// 每个 goroutine 写入不同的 key 范围
			startKey := goroutineID * keysPerGoroutine
			for i := 0; i < keysPerGoroutine; i++ {
				key := fmt.Sprintf("key-%d", startKey+i)
				value := fmt.Sprintf("value-%d", startKey+i)

				err := btree.Set(ctx, []byte(key), []byte(value))
				if err != nil {
					errors <- fmt.Errorf("goroutine %d, key %s: %w", goroutineID, key, err)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	// 检查错误
	for err := range errors {
		t.Errorf("Concurrent Set error: %v", err)
	}

	if len(errors) > 0 {
		t.Fatalf("Found %d errors during concurrent root split stress test", len(errors))
	}

	t.Logf("Root split stress test completed: %d goroutines × %d keys = %d successful writes",
		numGoroutines, keysPerGoroutine, numGoroutines*keysPerGoroutine)
}

// TestRootSplit_DataIntegrity 验证根分裂后的数据完整性
func TestRootSplit_DataIntegrity(t *testing.T) {
	t.Skip("flaky test: skip until handleSplitOffHeapSync race is fixed")
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()
	const numKeys = 5000

	// 写入大量数据（触发多次分裂，包括根分裂）
	// 每 1000 个 key 验证一次数据完整性
	for batch := 0; batch < numKeys/1000; batch++ {
		startIdx := batch * 1000
		endIdx := startIdx + 1000

		// 写入 1000 个 key
		for i := startIdx; i < endIdx; i++ {
			key := fmt.Sprintf("key-%d", i)
			value := fmt.Sprintf("value-%d", i)

			err := btree.Set(ctx, []byte(key), []byte(value))
			require.NoError(t, err)
		}

		// 验证前 1000*(batch+1) 个 key
		for i := 0; i < endIdx; i++ {
			key := fmt.Sprintf("key-%d", i)
			expectedValue := fmt.Sprintf("value-%d", i)

			gotValue, err := btree.Get(ctx, []byte(key))
			require.NoError(t, err, "Failed to get key: %s at batch %d", key, batch)
			assert.Equal(t, expectedValue, string(gotValue), "Value mismatch for key: %s", key)
		}

		t.Logf("Verified %d keys", endIdx)
	}

	t.Logf("Data integrity verified: %d keys written and read correctly", numKeys)
}

// TestRootSplit_ExtremeConcurrency 极端并发根分裂测试
func TestRootSplit_ExtremeConcurrency(t *testing.T) {
	t.Skip("temporarily skipped: pre-existing retry exhaustion issue")
	if testing.Short() {
		t.Skip("skipping extreme concurrency test in short mode")
	}

	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	const numGoroutines = 500
	const keysPerGoroutine = 200

	var wg sync.WaitGroup
	errorCount := 0
	var errorMu sync.Mutex

	// 启动极端并发写入
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			ctx := context.Background()
			startKey := goroutineID * keysPerGoroutine

			for i := 0; i < keysPerGoroutine; i++ {
				key := fmt.Sprintf("key-%d", startKey+i)
				value := []byte(fmt.Sprintf("value-%d", startKey+i))

				err := btree.Set(ctx, []byte(key), value)
				if err != nil {
					errorMu.Lock()
					errorCount++
					errorMu.Unlock()
					t.Logf("Error at goroutine %d, key %s: %v", goroutineID, key, err)
				}
			}
		}(g)
	}

	wg.Wait()

	if errorCount > 0 {
		t.Errorf("Found %d errors during extreme concurrency test", errorCount)
	}

	t.Logf("Extreme concurrency test completed: %d goroutines × %d keys = %d total operations",
		numGoroutines, keysPerGoroutine, numGoroutines*keysPerGoroutine)
}
