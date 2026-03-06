// Package bftree 提供 Bf-Tree 的异步操作实现
package bftree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBfTree_GetAsync(t *testing.T) {
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

	// 异步获取
	task := tree.GetAsync(context.Background(), []byte("key1"))
	assert.NotNil(t, task)
	assert.False(t, task.IsDone())

	// MVP: 直接调用 Run() 执行任务（不需要 Pipeline）
	task.Run(context.Background(), nil)

	// 等待完成
	value, err := task.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)
	assert.True(t, task.IsDone())
}

func TestBfTree_GetAsync_NotFound(t *testing.T) {
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

	// 异步获取不存在的键
	task := tree.GetAsync(context.Background(), []byte("key1"))
	task.Run(context.Background(), nil)

	value, err := task.Wait(context.Background())
	assert.Error(t, err)
	assert.Nil(t, value)
}

func TestBfTree_SetAsync(t *testing.T) {
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

	// 异步设置
	task := tree.SetAsync(context.Background(), []byte("key1"), []byte("value1"))
	assert.NotNil(t, task)
	assert.False(t, task.IsDone())

	// MVP: 直接调用 Run() 执行任务
	task.Run(context.Background(), nil)

	// 等待完成
	_, err = task.Wait(context.Background())
	require.NoError(t, err)
	assert.True(t, task.IsDone())

	// 验证数据已写入
	value, err := tree.Get(context.Background(), []byte("key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)
}

func TestBfTree_UpdateAsync(t *testing.T) {
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

	// 异步更新
	task := tree.UpdateAsync(context.Background(), []byte("key1"), []byte("value2"))
	task.Run(context.Background(), nil)

	_, err = task.Wait(context.Background())
	require.NoError(t, err)

	// 验证已更新
	value, _ := tree.Get(context.Background(), []byte("key1"))
	assert.Equal(t, []byte("value2"), value)
}

func TestBfTree_UpdateAsync_NotFound(t *testing.T) {
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

	// 异步更新不存在的键
	task := tree.UpdateAsync(context.Background(), []byte("key1"), []byte("value1"))
	task.Run(context.Background(), nil)

	_, err = task.Wait(context.Background())
	assert.Error(t, err)
}

func TestBfTree_DeleteAsync(t *testing.T) {
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

	// 异步删除
	task := tree.DeleteAsync(context.Background(), []byte("key1"))
	task.Run(context.Background(), nil)

	_, err = task.Wait(context.Background())
	require.NoError(t, err)

	// 验证已删除
	_, err = tree.Get(context.Background(), []byte("key1"))
	assert.Error(t, err)
}

func TestBfTree_DeleteAsync_NotFound(t *testing.T) {
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

	// 异步删除不存在的键
	task := tree.DeleteAsync(context.Background(), []byte("key1"))
	task.Run(context.Background(), nil)

	_, err = task.Wait(context.Background())
	assert.Error(t, err)
}

func TestBfTree_GetStatsAsync(t *testing.T) {
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

	// 异步获取统计
	task := tree.GetStatsAsync(context.Background())
	task.Run(context.Background(), nil)

	stats, err := task.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.TotalPages)
}

func TestBfTree_Async_Workflow(t *testing.T) {
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

	// 1. 异步设置
	setTask := tree.SetAsync(context.Background(), []byte("key1"), []byte("value1"))
	setTask.Run(context.Background(), nil)
	_, err = setTask.Wait(context.Background())
	require.NoError(t, err)

	// 2. 异步获取
	getTask := tree.GetAsync(context.Background(), []byte("key1"))
	getTask.Run(context.Background(), nil)
	value, err := getTask.Wait(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)

	// 3. 异步更新
	updateTask := tree.UpdateAsync(context.Background(), []byte("key1"), []byte("value2"))
	updateTask.Run(context.Background(), nil)
	_, err = updateTask.Wait(context.Background())
	require.NoError(t, err)

	// 4. 异步删除
	deleteTask := tree.DeleteAsync(context.Background(), []byte("key1"))
	deleteTask.Run(context.Background(), nil)
	_, err = deleteTask.Wait(context.Background())
	require.NoError(t, err)

	// 5. 验证已删除
	_, err = tree.Get(context.Background(), []byte("key1"))
	assert.Error(t, err)
}

func TestBfTree_Async_Concurrent(t *testing.T) {
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

	// 并发异步操作
	const operations = 10
	done := make(chan bool, operations)

	for i := 0; i < operations; i++ {
		go func(idx int) {
			defer func() { done <- true }()
			key := []byte{byte(idx)}
			value := []byte("value")

			// Set
			task := tree.SetAsync(context.Background(), key, value)
			task.Run(context.Background(), nil)
			_, _ = task.Wait(context.Background())

			// Get
			getTask := tree.GetAsync(context.Background(), key)
			getTask.Run(context.Background(), nil)
			_, _ = getTask.Wait(context.Background())
		}(i)
	}

	// 等待所有操作完成
	for i := 0; i < operations; i++ {
		<-done
	}

	// 验证统计
	stats := tree.GetStats()
	assert.Equal(t, int64(operations), stats.WriteCount)
	assert.Equal(t, int64(operations), stats.ReadCount)
}
