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

// TestOffHeap_FindLossPoint 缩小数据丢失的范围
func TestOffHeap_FindLossPoint(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 先插入 11700 个 keys
	t.Log("Phase 1: Inserting 11700 keys...")
	for i := 0; i < 11700; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 验证 key-00000
	t.Log("Phase 2: Verifying key-00000 after 11700 keys...")
	got, err := tree.Get(ctx, []byte("key-00000"))
	require.NoError(t, err)
	t.Logf("key-00000 found: %s", string(got))

	// 逐个插入 11700-11900，每 10 个验证一次
	t.Log("Phase 3: Inserting 11700-11900 and verifying...")
	for i := 11700; i < 11900; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)

		// 每 10 个 key 验证一次 key-00000
		if (i-11700+1)%10 == 0 {
			_, err := tree.Get(ctx, []byte("key-00000"))
			if err != nil {
				t.Logf("DATA LOSS DETECTED at key %d!", i)
				t.Logf("key-00000 was lost after inserting %d keys", i)
				t.FailNow()
			}
			t.Logf("Verified after %d keys", i)
		}
	}

	t.Log("Phase 4: Verifying key-00000 after 11900 keys...")
	got, err = tree.Get(ctx, []byte("key-00000"))
	if err != nil {
		t.Logf("DATA LOSS: key-00000 NOT FOUND after 11900 keys")
		t.FailNow()
	}
	t.Logf("key-00000 found: %s", string(got))
}

// TestOffHeap_VerifyFirstHundred 验证前 100 个 keys
func TestOffHeap_VerifyFirstHundred(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 插入 12000 个 keys
	numKeys := 12000
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	t.Logf("Inserted %d keys", numKeys)

	// 验证前 100 个 keys
	var lostKeys []int
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		_, err := tree.Get(ctx, key)
		if err != nil {
			lostKeys = append(lostKeys, i)
		}
	}

	if len(lostKeys) > 0 {
		t.Logf("Lost %d keys out of first 100: %v", len(lostKeys), lostKeys)
	} else {
		t.Logf("All first 100 keys found")
	}

	require.Equal(t, 0, len(lostKeys), "No keys should be lost")
}

// TestOffHeap_CheckRootSplit 检查根分裂是否导致数据丢失
func TestOffHeap_CheckRootSplit(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 记录初始根节点
	initialRoot := tree.rootRef.pInfo.Load()
	t.Logf("Initial root pageID: %d", initialRoot.GetPageID())

	// 插入 keys，监控根节点变化
	lastRootID := initialRoot.GetPageID()
	rootSplitCount := 0

	for i := 0; i < 12000; i++ {
		key := []byte(fmt.Sprintf("key-%05d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)

		// 检查根节点是否变化
		currentRoot := tree.rootRef.pInfo.Load()
		currentRootID := currentRoot.GetPageID()
		if currentRootID != lastRootID {
			rootSplitCount++
			t.Logf("Root split #%d at key %d: pageID %d -> %d",
				rootSplitCount, i, lastRootID, currentRootID)

			// 验证 key-00000
			_, err := tree.Get(ctx, []byte("key-00000"))
			if err != nil {
				t.Logf("DATA LOSS after root split #%d!", rootSplitCount)
				t.Logf("key-00000 was lost after root changed from %d to %d",
					lastRootID, currentRootID)
				t.FailNow()
			}

			lastRootID = currentRootID
		}

		// 每 1000 个 key 打印进度
		if (i+1)%1000 == 0 {
			t.Logf("Inserted %d keys, root splits: %d", i+1, rootSplitCount)
		}
	}

	t.Logf("SUCCESS: All 12000 keys inserted with %d root splits", rootSplitCount)
}
