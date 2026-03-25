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

// TestOffHeap_FindDataLoss 找到数据丢失的临界点
func TestOffHeap_FindDataLoss(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 从 6200 开始，逐个插入并验证
	startIdx := 6200
	endIdx := 6400

	// 先插入 6200 个 keys
	for i := 0; i < startIdx; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	t.Logf("Inserted %d keys, now inserting one by one and verifying...", startIdx)

	// 逐个插入并立即验证
	for i := startIdx; i < endIdx; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))

		// 插入新 key
		err := tree.Set(ctx, key, value)
		if err != nil {
			t.Logf("INSERT ERROR at key %d: %v", i, err)
			t.FailNow()
		}

		// 验证新 key
		got, err := tree.Get(ctx, key)
		if err != nil {
			t.Logf("GET ERROR for just-inserted key %d: %v", i, err)
			t.FailNow()
		}
		if string(got) != string(value) {
			t.Logf("VALUE MISMATCH for key %d: expected %s, got %s", i, string(value), string(got))
			t.FailNow()
		}

		// 每 10 个 key，验证所有之前的 keys
		if i%10 == 0 {
			t.Logf("Verifying all keys up to %d...", i)
			for j := 0; j <= i; j++ {
				key := []byte(fmt.Sprintf("key-%05d", j))
				expectedValue := []byte(fmt.Sprintf("value-%d", j))
				got, err := tree.Get(ctx, key)
				if err != nil {
					t.Logf("DATA LOSS at key %d when total keys = %d: %v", j, i, err)
					t.Logf("This is the first data loss detected!")
					t.Logf("Tree stats: %v", tree.GetStats())
					t.FailNow()
				}
				if string(got) != string(expectedValue) {
					t.Logf("VALUE MISMATCH at key %d when total keys = %d: expected %s, got %s",
						j, i, string(expectedValue), string(got))
					t.FailNow()
				}
			}
		}
	}

	t.Logf("SUCCESS: All %d keys verified", endIdx)
}

// TestOffHeap_VerifyFirstKey 找到 key-00000 丢失的时间点
func TestOffHeap_VerifyFirstKey(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 先插入 key-00000
	key0 := []byte("key-00000")
	value0 := []byte("value-0")
	err = tree.Set(ctx, key0, value0)
	require.NoError(t, err)

	// 逐批插入其他 keys，每次验证 key-00000
	for batch := 1; batch <= 150; batch++ {
		startIdx := batch * 100
		endIdx := startIdx + 100

		// 插入一批 keys
		for i := startIdx; i < endIdx; i++ {
			key := []byte(fmt.Sprintf("key-%05d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			err := tree.Set(ctx, key, value)
			if err != nil {
				t.Logf("INSERT ERROR at key %d: %v", i, err)
				t.FailNow()
			}
		}

		// 验证 key-00000
		got, err := tree.Get(ctx, key0)
		if err != nil {
			t.Logf("KEY-00000 LOST after inserting %d keys (batch %d): %v", endIdx, batch, err)
			t.Logf("Tree stats: %v", tree.GetStats())
			t.FailNow()
		}
		if string(got) != string(value0) {
			t.Logf("KEY-00000 VALUE MISMATCH after inserting %d keys: expected %s, got %s",
				endIdx, string(value0), string(got))
			t.FailNow()
		}

		if batch%10 == 0 {
			t.Logf("Verified key-00000 after %d keys", endIdx)
		}
	}

	t.Logf("SUCCESS: key-00000 found after all 15000 keys")
}
