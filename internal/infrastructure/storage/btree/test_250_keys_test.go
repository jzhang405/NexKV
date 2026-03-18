// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/stretchr/testify/require"
)

// TestBTree_250_Keys 测试250个key（第一次分裂后）
func TestBTree_250_Keys(t *testing.T) {
	tree, err := OpenBTree("", &model.BTreeConfig{})
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入 250 个 key
	t.Log("插入 250 个 key...")
	for i := 0; i < 250; i++ {
		key := fmt.Sprintf("key-%07d", i)
		value := fmt.Sprintf("value-%07d", i)
		err := tree.Set(ctx, []byte(key), []byte(value))
		require.NoError(t, err, "Set should succeed for key %s", key)
	}

	// 验证
	t.Log("验证所有 250 个 key...")
	missingCount := 0
	for i := 0; i < 250; i++ {
		key := fmt.Sprintf("key-%07d", i)
		_, err := tree.Get(ctx, []byte(key))
		if err != nil {
			if missingCount == 0 {
				t.Logf("第一个丢失的key: key-%07d", i)
			}
			missingCount++
			t.Errorf("Key %s (i=%d) 找不到", key, i)
		}
	}

	if missingCount > 0 {
		t.Errorf("🔴 丢失了 %d 个 key", missingCount)
	} else {
		t.Logf("✅ 所有 250 个 key 都找到了")
	}
}
