package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOffHeap_BasicNoSplit 测试不触发分裂的基本操作
func TestOffHeap_BasicNoSplit(t *testing.T) {
	ctx := context.Background()
	tree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer tree.Close()

	// 只插入少量键，避免触发分裂
	const numKeys = 10
	keys := make([][]byte, numKeys)
	values := make([][]byte, numKeys)

	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("nosplit-key-%d", i))
		values[i] = []byte(fmt.Sprintf("nosplit-value-%d", i))
	}

	// 批量插入
	for i := 0; i < numKeys; i++ {
		err := tree.Set(ctx, keys[i], values[i])
		require.NoError(t, err, "Failed to set key %d", i)
	}

	// 验证所有键
	for i := 0; i < numKeys; i++ {
		got, err := tree.Get(ctx, keys[i])
		require.NoError(t, err, "Failed to get key %d", i)
		require.Equal(t, values[i], got, "Value mismatch for key %d", i)
	}
	t.Logf("Successfully tested %d keys without split", numKeys)
}
