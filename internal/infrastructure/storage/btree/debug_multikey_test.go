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

// TestDebug_MultiKeyIssue 调试 multi-key 问题
func TestDebug_MultiKeyIssue(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 逐个插入并验证
	for i := 0; i < 30; i++ {
		key := []byte(fmt.Sprintf("multi-key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))

		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 打印树结构
	t.Logf("Root pageID: %d", tree.rootRef.pInfo.Load().GetPageID())

	// 验证 key 2
	key2 := []byte("multi-key-2")
	got, err := tree.Get(ctx, key2)
	if err != nil {
		t.Logf("GET key 2 FAILED: %v", err)
	} else {
		t.Logf("GET key 2 SUCCESS: %s", string(got))
	}

	// 验证 key 10
	key10 := []byte("multi-key-10")
	got, err = tree.Get(ctx, key10)
	if err != nil {
		t.Logf("GET key 10 FAILED: %v", err)
	} else {
		t.Logf("GET key 10 SUCCESS: %s", string(got))
	}
}
