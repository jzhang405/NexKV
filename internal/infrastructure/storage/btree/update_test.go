package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOffHeap_UpdatesOnly 测试只更新现有键
func TestOffHeap_UpdatesOnly(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	const numKeys = 10
	keys := make([][]byte, numKeys)
	values := make([][]byte, numKeys)

	// 第一次插入
	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("update-key-%03d", i))
		values[i] = []byte(fmt.Sprintf("update-value-v1-%03d", i))

		err := tree.Set(ctx, keys[i], values[i])
		require.NoError(t, err, "Failed to set key %d (v1)", i)
	}

	// 验证第一次插入
	for i := 0; i < numKeys; i++ {
		got, err := tree.Get(ctx, keys[i])
		require.NoError(t, err, "Failed to get key %d (v1)", i)
		require.Equal(t, values[i], got, "Value mismatch for key %d (v1)", i)
	}

	// 第二次更新
	for i := 0; i < numKeys; i++ {
		values[i] = []byte(fmt.Sprintf("update-value-v2-%03d", i))

		err := tree.Set(ctx, keys[i], values[i])
		require.NoError(t, err, "Failed to update key %d (v2)", i)
	}

	// 验证第二次更新
	for i := 0; i < numKeys; i++ {
		got, err := tree.Get(ctx, keys[i])
		require.NoError(t, err, "Failed to get key %d (v2)", i)
		require.Equal(t, values[i], got, "Value mismatch for key %d (v2)", i)
	}
}
