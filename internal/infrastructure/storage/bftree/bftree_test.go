package bftree

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBfTree(t *testing.T) {
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
	assert.NotNil(t, tree)
	assert.False(t, tree.closed.Load())
	assert.Equal(t, uint64(0), tree.rootPageID)

	_ = tree.Close()
}

func TestNewBfTree_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{"空数据目录", &Config{DataDir: ""}},
		{"无效页面大小", &Config{DataDir: "/tmp", PageSize: 100}},
		{"无效深度", &Config{DataDir: "/tmp", MaxDepth: 20}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBfTree(tt.config)
			assert.Error(t, err)
		})
	}
}

func TestBfTree_Get_EmptyTree(t *testing.T) {
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

	value, err := tree.Get(context.Background(), []byte("key"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
	assert.Nil(t, value)
}

func TestBfTree_Close(t *testing.T) {
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

	err = tree.Close()
	assert.NoError(t, err)
	assert.True(t, tree.closed.Load())

	err = tree.Close()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTreeClosed)
}

func TestBfTree_Get_Closed(t *testing.T) {
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
	_ = tree.Close()

	_, err = tree.Get(context.Background(), []byte("key"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTreeClosed)

	err = tree.Set(context.Background(), []byte("key"), []byte("value"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTreeClosed)

	err = tree.Delete(context.Background(), []byte("key"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTreeClosed)
}

func TestBfTree_GetStats(t *testing.T) {
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

	stats := tree.GetStats()
	assert.Equal(t, int64(0), stats.TotalPages)
	assert.Equal(t, int64(0), stats.ReadCount)
}

func TestBfTree_WithWAL(t *testing.T) {
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

	assert.True(t, tree.walEnabled)
	assert.NotNil(t, tree.wal)
}

func TestBfTree_WAL_CreateDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	config := &Config{
		DataDir:          tmpDir,
		WALDir:           filepath.Join(tmpDir, "wal"),
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

	walDir := filepath.Join(tmpDir, "wal")
	info, err := os.Stat(walDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestBfTree_ConcurrentRead(t *testing.T) {
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

	const goroutines = 10
	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- true }()
			_, _ = tree.Get(context.Background(), []byte("key"))
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	stats := tree.GetStats()
	assert.Equal(t, int64(goroutines), stats.ReadCount)
}

func TestBfTree_Stats_Accumulation(t *testing.T) {
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

	_, _ = tree.Get(context.Background(), []byte("key1"))
	_, _ = tree.Get(context.Background(), []byte("key2"))
	_, _ = tree.Get(context.Background(), []byte("key3"))

	stats := tree.GetStats()
	assert.Equal(t, int64(3), stats.ReadCount)
}
