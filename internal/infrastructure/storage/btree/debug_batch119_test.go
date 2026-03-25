// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOffHeap_DebugBatch119 调试 batch 119 的关键操作
func TestOffHeap_DebugBatch119(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 先插入 11900 个 keys（包括 key-00000）
	t.Log("Inserting 11900 keys...")
	for i := 0; i < 11900; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}
	t.Log("11900 keys inserted")

	// 验证 key-00000 存在
	key0 := []byte("key-00000")
	got, err := tree.Get(ctx, key0)
	require.NoError(t, err)
	t.Logf("key-00000 found before batch 119: %s", string(got))

	// 获取树统计信息
	stats := tree.GetStats()
	t.Logf("Tree stats before batch 119: %s", stats.String())

	// 逐个插入 batch 119 的 keys（11900-12000）
	t.Log("Inserting batch 119 (11900-12000) one by one...")
	for i := 11900; i < 12000; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))

		// 插入前验证 key-00000
		before, err := tree.Get(ctx, []byte("key-00000"))
		if err != nil {
			t.Logf("WARNING: key-00000 NOT FOUND before inserting key-%05d", i)
		}

		// 插入新 key
		err = tree.Set(ctx, key, value)
		if err != nil {
			t.Logf("INSERT ERROR at key %d: %v", i, err)
			t.FailNow()
		}

		// 插入后验证 key-00000
		_, err = tree.Get(ctx, []byte("key-00000"))
		if err != nil {
			t.Logf("DATA LOSS DETECTED!")
			t.Logf("  key-00000 was lost after inserting key-%05d", i)
			t.Logf("  Before insertion: key-00000 = %s", before)
			t.Logf("  After insertion: NOT FOUND")
			t.Logf("  Tree stats: %v", tree.GetStats())
			t.FailNow()
		}

		// 每 10 个 key 打印进度
		if (i-11900+1)%10 == 0 {
			t.Logf("Inserted %d keys in batch 119", i-11900+1)
		}
	}

	// 最终验证
	got, err = tree.Get(ctx, []byte("key-00000"))
	require.NoError(t, err)
	t.Logf("key-00000 still found after batch 119: %s", string(got))
}

// TestOffHeap_VerifySpecificKeys 验证特定范围的 keys
func TestOffHeap_VerifySpecificKeys(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入 1000 个 keys
	numKeys := 1000
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	t.Logf("Inserted %d keys", numKeys)

	// 验证所有 keys
	var lostKeys []int
	var foundKeys int
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		_, err := tree.Get(ctx, key)
		if err != nil {
			lostKeys = append(lostKeys, i)
		} else {
			foundKeys++
		}
	}

	t.Logf("Found: %d, Lost: %d", foundKeys, len(lostKeys))

	if len(lostKeys) > 0 {
		t.Logf("Lost keys: %v", lostKeys[:min(10, len(lostKeys))])
		if len(lostKeys) > 10 {
			t.Logf("  ... and %d more", len(lostKeys)-10)
		}
	}

	require.Equal(t, 0, len(lostKeys), "No keys should be lost")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
