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

// TestBinarySearchIntegrity 使用二分查找定位数据丢失的精确阈值
func TestBinarySearchIntegrity(t *testing.T) {
	// 已知：1050 通过，1100 失败（丢失 954 个）
	// 使用二分查找找到精确的失败点

	low, high := 1050, 1100
	firstFail := -1

	for low <= high {
		mid := (low + high) / 2
		t.Logf("测试 %d 个 key...", mid)

		btree, err := OpenBTree("", nil)
		require.NoError(t, err)

		ctx := context.Background()

		// 写入 mid 个 key
		for i := 0; i < mid; i++ {
			key := fmt.Sprintf("key-%d", i)
			value := fmt.Sprintf("value-%d", i)
			err := btree.Set(ctx, []byte(key), []byte(value))
			require.NoError(t, err, "写入 key-%d 失败", i)
		}

		// 验证所有 key
		missing := 0
		for i := 0; i < mid; i++ {
			key := fmt.Sprintf("key-%d", i)
			_, err := btree.Get(ctx, []byte(key))
			if err != nil {
				missing++
			}
		}

		btree.Close()

		if missing == 0 {
			t.Logf("✅ %d 个 key: 全部存在", mid)
			low = mid + 1
		} else {
			t.Logf("❌ %d 个 key: 丢失 %d 个", mid, missing)
			high = mid - 1
			if firstFail == -1 || mid < firstFail {
				firstFail = mid
			}
		}
	}

	t.Logf("精确失败阈值: %d 个 key", firstFail)
}
