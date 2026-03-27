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

// TestRootSplit_DebugDataIntegrity 调试根分裂数据完整性问题
func TestRootSplit_DebugDataIntegrity(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	// 第一批：写入 1000 个 key
	t.Log("Writing batch 1: key-0 to key-999")
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := fmt.Sprintf("value-%d", i)
		err := btree.Set(ctx, []byte(key), []byte(value))
		require.NoError(t, err)
	}

	// 验证第一批
	t.Log("Verifying batch 1")
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		expectedValue := fmt.Sprintf("value-%d", i)
		gotValue, err := btree.Get(ctx, []byte(key))
		require.NoError(t, err, "Failed to get key: %s after batch 1", key)
		if string(gotValue) != expectedValue {
			t.Errorf("Value mismatch for key %s: got %s, want %s", key, string(gotValue), expectedValue)
		}
	}
	t.Log("Batch 1 verified: 1000 keys OK")

	// 第二批：写入 100 个 key（101 个 key 可能会触发分裂）
	t.Log("Writing batch 2: key-1000 to key-1099")
	for i := 1000; i < 1100; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := fmt.Sprintf("value-%d", i)
		err := btree.Set(ctx, []byte(key), []byte(value))
		require.NoError(t, err)
	}

	// 验证第一批仍然存在
	t.Log("Verifying batch 1 after batch 2")
	missingKeys := []string{}
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		expectedValue := fmt.Sprintf("value-%d", i)
		gotValue, err := btree.Get(ctx, []byte(key))
		if err != nil {
			missingKeys = append(missingKeys, key)
		} else if string(gotValue) != expectedValue {
			t.Errorf("Value mismatch for key %s: got %s, want %s", key, string(gotValue), expectedValue)
		}
	}

	if len(missingKeys) > 0 {
		t.Logf("MISSING KEYS after batch 2 (%d keys): %v", len(missingKeys), missingKeys[:min(10, len(missingKeys))])
		for _, key := range missingKeys {
			t.Logf("  Missing: %s", key)
		}
	}

	// 验证第二批
	t.Log("Verifying batch 2")
	for i := 1000; i < 1100; i++ {
		key := fmt.Sprintf("key-%d", i)
		expectedValue := fmt.Sprintf("value-%d", i)
		gotValue, err := btree.Get(ctx, []byte(key))
		require.NoError(t, err, "Failed to get key: %s after batch 2", key)
		if string(gotValue) != expectedValue {
			t.Errorf("Value mismatch for key %s: got %s, want %s", key, string(gotValue), expectedValue)
		}
	}
	t.Log("Batch 2 verified: 100 keys OK")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
