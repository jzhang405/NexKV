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

func TestOffHeap_SimpleMultipleKeys(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	const numKeys = 500
	format := "key-%04d"

	for i := range numKeys {
		key := []byte(fmt.Sprintf(format, i))
		value := []byte(fmt.Sprintf("value-%d", i))

		err := tree.Set(ctx, key, value)
		if err != nil {
			t.Logf("ERROR at key %d: %v", i, err)
			t.FailNow()
		}
	}

	for i := range numKeys {
		key := []byte(fmt.Sprintf(format, i))
		expectedValue := []byte(fmt.Sprintf("value-%d", i))

		got, err := tree.Get(ctx, key)
		if err != nil {
			t.Logf("GET ERROR at key %d: %v", i, err)
			t.Logf("Key bytes: %v", key)
			t.FailNow()
		}
		if string(got) != string(expectedValue) {
			t.Logf("VALUE MISMATCH at key %d: expected %s, got %s", i, string(expectedValue), string(got))
			t.FailNow()
		}

		// 每 50 个 key 打印一次进度
		if (i+1)%50 == 0 {
			t.Logf("Verified %d keys", i+1)
		}
	}

	t.Logf("SUCCESS: Inserted and retrieved %d keys", numKeys)
}
