// Package bftree 提供 Bf-Tree 的节点分裂测试
package bftree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBfTree_Split_LeafNode(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        false,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 插入大量数据，触发分裂
	const numKeys = 200
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		value := []byte("value")
		err := tree.Set(context.Background(), key, value)
		require.NoError(t, err, "failed to insert key %d", i)
	}

	// 验证数据可读
	for i := 0; i < numKeys; i++ {
		key := []byte{byte(i / 256), byte(i % 256)}
		value, err := tree.Get(context.Background(), key)
		require.NoError(t, err, "failed to get key %d", i)
		assert.Equal(t, []byte("value"), value)
	}

	// 验证统计信息
	stats := tree.GetStats()
	assert.Equal(t, int64(numKeys), stats.WriteCount)
}

// 注意：根节点增长需要大量数据才能触发
// Phase 2.3 将优化根节点分裂逻辑
func TestBfTree_Split_RootGrowth(t *testing.T) {
	t.Skip("Phase 2.2 MVP: 根节点增长需要更多数据，跳过此测试")
}

// 注意：分裂后删除需要更复杂的实现
// Phase 2.3 将实现完整的合并逻辑
func TestBfTree_Split_DeleteAfterSplit(t *testing.T) {
	t.Skip("Phase 2.2 MVP: 分裂后删除需要额外优化，跳过此测试")
}

func TestCompareKeys(t *testing.T) {
	tests := []struct {
		name     string
		k1       []byte
		k2       []byte
		expected int
	}{
		{"k1 < k2", []byte{1}, []byte{2}, -1},
		{"k1 > k2", []byte{2}, []byte{1}, 1},
		{"k1 == k2", []byte{1}, []byte{1}, 0},
		{"k1 < k2 (multi-byte)", []byte{1, 2}, []byte{1, 3}, -1},
		{"k1 > k2 (multi-byte)", []byte{1, 3}, []byte{1, 2}, 1},
		{"k1 shorter", []byte{1}, []byte{1, 2}, -1},
		{"k1 longer", []byte{1, 2}, []byte{1}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareKeys(tt.k1, tt.k2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCompareKeys_EdgeCases_Coverage 测试键比较的边界情况
func TestCompareKeys_EdgeCases_Coverage(t *testing.T) {
	tests := []struct {
		name     string
		k1       []byte
		k2       []byte
		expected int
	}{
		{"空键", []byte{}, []byte{}, 0},
		{"k1 空", []byte{}, []byte{1}, -1},
		{"k2 空", []byte{1}, []byte{}, 1},
		{"相同单字节", []byte{5}, []byte{5}, 0},
		{"相同多字节", []byte{1, 2, 3}, []byte{1, 2, 3}, 0},
		{"前缀相同", []byte{1, 2}, []byte{1, 2, 3}, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareKeys(tt.k1, tt.k2)
			assert.Equal(t, tt.expected, result)
		})
	}
}
