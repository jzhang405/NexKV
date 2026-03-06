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

func TestBfTree_Set_Get(t *testing.T) {
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

	// 插入键值
	err = tree.Set(context.Background(), []byte("key1"), []byte("value1"))
	require.NoError(t, err)

	// 验证可以获取
	value, err := tree.Get(context.Background(), []byte("key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)

	// 验证不存在的键
	_, err = tree.Get(context.Background(), []byte("key2"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestBfTree_Set_Multiple(t *testing.T) {
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

	// 插入多个键值
	pairs := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
		"key4": "value4",
		"key5": "value5",
	}

	for k, v := range pairs {
		err := tree.Set(context.Background(), []byte(k), []byte(v))
		require.NoError(t, err)
	}

	// 验证所有键值
	for k, v := range pairs {
		value, err := tree.Get(context.Background(), []byte(k))
		require.NoError(t, err)
		assert.Equal(t, []byte(v), value)
	}
}

func TestBfTree_Set_Overwrite(t *testing.T) {
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

	// 插入键值
	_ = tree.Set(context.Background(), []byte("key1"), []byte("value1"))
	value, _ := tree.Get(context.Background(), []byte("key1"))
	assert.Equal(t, []byte("value1"), value)

	// 覆盖
	_ = tree.Set(context.Background(), []byte("key1"), []byte("value2"))
	value, _ = tree.Get(context.Background(), []byte("key1"))
	assert.Equal(t, []byte("value2"), value)
}

func TestBfTree_Update(t *testing.T) {
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

	// 先插入
	_ = tree.Set(context.Background(), []byte("key1"), []byte("value1"))

	// 更新
	err = tree.Update(context.Background(), []byte("key1"), []byte("value2"))
	require.NoError(t, err)

	value, _ := tree.Get(context.Background(), []byte("key1"))
	assert.Equal(t, []byte("value2"), value)
}

func TestBfTree_Update_NotFound(t *testing.T) {
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

	// 更新不存在的键
	err = tree.Update(context.Background(), []byte("key1"), []byte("value1"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestBfTree_Delete(t *testing.T) {
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

	// 先插入
	_ = tree.Set(context.Background(), []byte("key1"), []byte("value1"))
	_, err = tree.Get(context.Background(), []byte("key1"))
	require.NoError(t, err)

	// 删除
	err = tree.Delete(context.Background(), []byte("key1"))
	require.NoError(t, err)

	// 验证已删除
	_, err = tree.Get(context.Background(), []byte("key1"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestBfTree_Delete_NotFound(t *testing.T) {
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

	// 删除不存在的键
	err = tree.Delete(context.Background(), []byte("key1"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
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

	// 测试 Set 会写 WAL
	_ = tree.Set(context.Background(), []byte("key1"), []byte("value1"))
	stats := tree.GetStats()
	assert.Equal(t, int64(1), stats.WALAppends)
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

	// 先插入数据
	_ = tree.Set(context.Background(), []byte("key1"), []byte("value1"))

	const goroutines = 10
	done := make(chan bool, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- true }()
			_, _ = tree.Get(context.Background(), []byte("key1"))
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

	_ = tree.Set(context.Background(), []byte("key1"), []byte("value1"))
	_, _ = tree.Get(context.Background(), []byte("key1"))
	_ = tree.Delete(context.Background(), []byte("key1"))

	stats := tree.GetStats()
	assert.Equal(t, int64(1), stats.WriteCount)
	assert.Equal(t, int64(1), stats.ReadCount)
	assert.Equal(t, int64(1), stats.DeleteCount)
}

func TestBfTree_CrudWorkflow(t *testing.T) {
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

	// 1. Create
	_ = tree.Set(context.Background(), []byte("key1"), []byte("value1"))
	value, err := tree.Get(context.Background(), []byte("key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)

	// 2. Update
	_ = tree.Update(context.Background(), []byte("key1"), []byte("value2"))
	value, _ = tree.Get(context.Background(), []byte("key1"))
	assert.Equal(t, []byte("value2"), value)

	// 3. Delete
	_ = tree.Delete(context.Background(), []byte("key1"))
	_, err = tree.Get(context.Background(), []byte("key1"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}
