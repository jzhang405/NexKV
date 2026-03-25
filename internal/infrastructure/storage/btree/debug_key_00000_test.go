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

// TestOffHeap_DebugKey00000 调试 key-00000 丢失问题
func TestOffHeap_DebugKey00000(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 只插入 key-00000
	key := []byte("key-00000")
	value := []byte("value-0")

	fmt.Printf("=== Step 1: Inserting key-00000 ===\n")
	err = tree.Set(ctx, key, value)
	require.NoError(t, err)

	fmt.Printf("=== Step 2: Reading key-00000 immediately ===\n")
	got, err := tree.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, string(value), string(got))
	fmt.Printf("SUCCESS: key-00000 found, value=%s\n", string(got))

	// 插入一些其他 keys，可能触发页面分裂
	fmt.Printf("\n=== Step 3: Inserting 100 more keys ===\n")
	for i := 1; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		if err != nil {
			t.Logf("ERROR at key %d: %v", i, err)
		}
	}

	// 再次读取 key-00000
	fmt.Printf("\n=== Step 4: Reading key-00000 after 99 more inserts ===\n")
	got, err = tree.Get(ctx, []byte("key-00000"))
	if err != nil {
		t.Logf("GET ERROR: %v", err)
		t.Log("This indicates key-00000 was lost after page splits!")
		t.FailNow()
	}
	require.Equal(t, string(value), string(got))
	fmt.Printf("SUCCESS: key-00000 still found, value=%s\n", string(got))

	// 插入更多 keys，触发根分裂
	fmt.Printf("\n=== Step 5: Inserting more keys to trigger root split ===\n")
	for i := 100; i < 200; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		if err != nil {
			t.Logf("ERROR at key %d: %v", i, err)
		}
	}

	// 再次读取 key-00000
	fmt.Printf("\n=== Step 6: Reading key-00000 after root split ===\n")
	got, err = tree.Get(ctx, []byte("key-00000"))
	if err != nil {
		t.Logf("GET ERROR: %v", err)
		t.Log("This indicates key-00000 was lost after root split!")
		t.FailNow()
	}
	require.Equal(t, string(value), string(got))
	fmt.Printf("SUCCESS: key-00000 still found after root split, value=%s\n", string(got))
}

// TestOffHeap_InsertThenVerify 插入一批 keys 后立即验证
func TestOffHeap_InsertThenVerify(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	const batchSize = 100

	for batch := 0; batch < 150; batch++ {
		startIdx := batch * batchSize
		endIdx := startIdx + batchSize

		// 插入一批 keys
		for i := startIdx; i < endIdx; i++ {
			key := []byte(fmt.Sprintf("key-%05d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			err := tree.Set(ctx, key, value)
			require.NoError(t, err)
		}

		// 验证这一批 keys
		for i := startIdx; i < endIdx; i++ {
			key := []byte(fmt.Sprintf("key-%05d", i))
			expectedValue := []byte(fmt.Sprintf("value-%d", i))
			got, err := tree.Get(ctx, key)
			if err != nil {
				t.Logf("BATCH %d: GET ERROR at key %d: %v", batch, i, err)
				t.Logf("This indicates data loss after inserting %d keys", endIdx)
				t.FailNow()
			}
			if string(got) != string(expectedValue) {
				t.Logf("BATCH %d: VALUE MISMATCH at key %d: expected %s, got %s",
					batch, i, string(expectedValue), string(got))
				t.FailNow()
			}
		}

		if (batch+1)%10 == 0 {
			t.Logf("Verified %d keys (%d batches)", endIdx, batch+1)
		}
	}

	t.Logf("SUCCESS: All 15000 keys verified")
}
