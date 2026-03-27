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

// TestPresentKeys_1076 详细检查 1076 个 key 后哪些 key 存在
func TestPresentKeys_1076(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	ctx := context.Background()

	t.Log("Writing 1076 keys")
	for i := 0; i < 1076; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := fmt.Sprintf("value-%d", i)
		err := btree.Set(ctx, []byte(key), []byte(value))
		require.NoError(t, err)
	}

	presentKeys := []string{}
	missingKeys := []string{}

	for i := 0; i < 1076; i++ {
		key := fmt.Sprintf("key-%d", i)
		_, err := btree.Get(ctx, []byte(key))
		if err != nil {
			missingKeys = append(missingKeys, key)
		} else {
			presentKeys = append(presentKeys, key)
		}
	}

	t.Logf("总计: %d 存在, %d 丢失", len(presentKeys), len(missingKeys))
	t.Logf("存在的 key (前50个): %v", safeSlice(presentKeys, 0, 50))
	t.Logf("存在的 key (后50个): %v", safeSlice(presentKeys, len(presentKeys)-50, len(presentKeys)))
	t.Logf("丢失的 key (前50个): %v", safeSlice(missingKeys, 0, 50))
	t.Logf("丢失的 key (后50个): %v", safeSlice(missingKeys, len(missingKeys)-50, len(missingKeys)))

	// 检查存在的 key 是否有连续的模式
	t.Logf("存在的 key 分析:")
	if len(presentKeys) > 0 {
		firstKey := presentKeys[0]
		lastKey := presentKeys[len(presentKeys)-1]
		t.Logf("  第一个存在的 key: %s", firstKey)
		t.Logf("  最后一个存在的 key: %s", lastKey)
	}
}

func safeSlice(slice []string, start, end int) []string {
	if start >= len(slice) {
		return []string{}
	}
	if end > len(slice) {
		end = len(slice)
	}
	if start < 0 {
		start = 0
	}
	return slice[start:end]
}
