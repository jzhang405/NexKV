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

	const numKeys = 15000
	format := "key-%05d"

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

func TestOffHeap_FiveHundredThousandKeys(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	const numKeys = 500000
	format := "key-%06d"

	for i := range numKeys {
		key := []byte(fmt.Sprintf(format, i))
		value := []byte(fmt.Sprintf("value-%d", i))

		err := tree.Set(ctx, key, value)
		if err != nil {
			t.Logf("ERROR at key %d: %v", i, err)
			t.FailNow()
		}

		// 每 50000 个 key 打印一次进度
		if (i+1)%50000 == 0 {
			t.Logf("Inserted %d keys", i+1)
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

		// 每 50000 个 key 打印一次进度
		if (i+1)%50000 == 0 {
			t.Logf("Verified %d keys", i+1)
		}
	}

	t.Logf("SUCCESS: Inserted and retrieved %d keys", numKeys)
}

// TestOffHeap_SpaceBasedSplitting 验证基于空间而非条数的分裂判断
func TestOffHeap_SpaceBasedSplitting(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 测试1：小 key/value，应该能容纳更多条目（因为基于空间而非条数）
	t.Run("SmallKeyValue", func(t *testing.T) {
		const numKeys = 200
		smallKey := []byte("k")
		smallValue := []byte("v")

		for i := range numKeys {
			key := []byte(fmt.Sprintf("%s%d", smallKey, i))
			value := []byte(fmt.Sprintf("%s%d", smallValue, i))
			err := tree.Set(ctx, key, value)
			require.NoError(t, err)
		}

		// 验证所有数据都可以检索
		for i := range numKeys {
			key := []byte(fmt.Sprintf("%s%d", smallKey, i))
			expectedValue := []byte(fmt.Sprintf("%s%d", smallValue, i))
			got, err := tree.Get(ctx, key)
			require.NoError(t, err)
			require.Equal(t, string(expectedValue), string(got))
		}
		t.Logf("Small key/value test: Successfully inserted and retrieved %d keys", numKeys)
	})

	// 重置树进行下一个测试
	tree.Close()
	tree, err = OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 测试2：大 value，应该触发更频繁的分裂
	t.Run("LargeValue", func(t *testing.T) {
		const numKeys = 100
		// 创建 256 字节的 value
		largeValue := make([]byte, 256)
		for i := range largeValue {
			largeValue[i] = byte('a' + (i % 26))
		}

		for i := range numKeys {
			key := []byte(fmt.Sprintf("k%d", i))
			err := tree.Set(ctx, key, largeValue)
			require.NoError(t, err)
		}

		// 验证所有数据都可以检索
		for i := range numKeys {
			key := []byte(fmt.Sprintf("k%d", i))
			got, err := tree.Get(ctx, key)
			if err != nil {
				t.Logf("GET ERROR at key %d: %v", i, err)
			}
			require.NoError(t, err)
			require.Equal(t, len(largeValue), len(got))
			require.Equal(t, largeValue, got)
		}
		t.Logf("Large value test: Successfully inserted and retrieved %d keys with 256-byte values", numKeys)
	})
}

func TestOffHeap_25000Keys(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	const numKeys = 25000
	format := "key-%05d"

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

		// 每 1000 个 key 打印一次进度
		if (i+1)%1000 == 0 {
			t.Logf("Verified %d keys", i+1)
		}
	}

	t.Logf("SUCCESS: Inserted and retrieved %d keys", numKeys)
}

func TestOffHeap_35000Keys(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	const numKeys = 35000
	format := "key-%05d"

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

		if (i+1)%1000 == 0 {
			t.Logf("Verified %d keys", i+1)
		}
	}

	t.Logf("SUCCESS: Inserted and retrieved %d keys", numKeys)
}
