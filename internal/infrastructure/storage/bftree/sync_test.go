// Package bftree 提供 Bf-Tree 的 Sync 方法测试
package bftree

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBfTree_Sync_WithoutWAL(t *testing.T) {
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

	// 未启用 WAL，Sync 应该直接成功
	err = tree.Sync()
	assert.NoError(t, err)
}

func TestBfTree_Sync_WithWAL(t *testing.T) {
	tmpDir := t.TempDir()
	walDir := filepath.Join(tmpDir, "wal")
	config := &Config{
		DataDir:          tmpDir,
		WALDir:           walDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        true,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		SegmentSize:      DefaultSegmentSize,
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 写入一些数据
	err = tree.Set(context.Background(), []byte("key1"), []byte("value1"))
	require.NoError(t, err)

	// Sync 应该成功
	err = tree.Sync()
	assert.NoError(t, err)

	// 验证统计信息
	stats := tree.GetStats()
	assert.Equal(t, int64(1), stats.WALAppends)
	assert.GreaterOrEqual(t, int64(1), stats.WALSyncCount)
}

func TestBfTree_Sync_Closed(t *testing.T) {
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

	// 关闭树
	err = tree.Close()
	require.NoError(t, err)

	// Sync 应该返回错误
	err = tree.Sync()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTreeClosed)
}

func TestBfTree_Sync_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	walDir := filepath.Join(tmpDir, "wal")
	config := &Config{
		DataDir:          tmpDir,
		WALDir:           walDir,
		PageSize:         DefaultPageSize,
		MaxDepth:         DefaultMaxDepth,
		EnableWAL:        true,
		EnableDeltaChain: true,
		PromotionConfig:  DefaultPromotionConfig(),
		SegmentSize:      DefaultSegmentSize,
		BitmapLockShards: DefaultBitmapLockShards,
	}

	tree, err := NewBfTree(config)
	require.NoError(t, err)
	defer tree.Close()

	// 写入一些数据
	for i := 0; i < 10; i++ {
		key := []byte{byte(i)}
		err := tree.Set(context.Background(), key, []byte("value"))
		require.NoError(t, err)
	}

	// 并发 Sync
	const goroutines = 10
	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- true }()
			err := tree.Sync()
			assert.NoError(t, err)
		}()
	}

	// 等待所有 Sync 完成
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// 验证统计信息
	stats := tree.GetStats()
	assert.Equal(t, int64(10), stats.WALAppends)
	assert.GreaterOrEqual(t, int64(10), stats.WALSyncCount)
}
