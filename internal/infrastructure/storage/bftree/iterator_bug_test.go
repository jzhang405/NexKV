package bftree

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBug_IteratorOrder 复现迭代器顺序 bug
func TestBug_IteratorOrder(t *testing.T) {
	config := DefaultConfig()
	config.DataDir = t.TempDir()
	config.EnableWAL = false

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()
	const numKeys = 100

	// 插入有序数据
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("key-%03d", i))
		value := []byte(fmt.Sprintf("value-%03d", i))
		err := tree.Set(ctx, key, value)
		assert.NoError(t, err)
	}

	// 使用迭代器遍历
	iter := tree.Scan(ctx, nil, nil)
	defer iter.Close()

	collectedKeys := make([]string, 0, numKeys)
	for {
		valid, key, _, err := iter.Next()
		if !valid {
			break
		}
		assert.NoError(t, err)
		collectedKeys = append(collectedKeys, string(key))
	}

	// 验证顺序
	expectedKeys := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		expectedKeys[i] = fmt.Sprintf("key-%03d", i)
	}

	// 打印实际顺序和期望顺序
	t.Logf("Expected: %v", expectedKeys)
	t.Logf("Actual: %v", collectedKeys)

	assert.Equal(t, expectedKeys, collectedKeys)
}
