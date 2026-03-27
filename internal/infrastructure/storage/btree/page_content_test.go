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

// TestPage92Content 详细检查 page 92 的内容
func TestPage92Content(t *testing.T) {
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

	// 检查 page 92 的所有内容
	pageID := uint32(92)
	count := btree.offheapAdapter.pa.GetCount(pageID)
	t.Logf("Page 92 has %d keys", count)

	for i := 0; i < int(count); i++ {
		keyOff, keyLen, _, _ := btree.offheapAdapter.pa.GetLeafEntryOffset(pageID, i)
		key := btree.offheapAdapter.pa.GetKey(pageID, keyOff, keyLen)
		t.Logf("  [%d] key=%s", i, string(key))
	}
}
