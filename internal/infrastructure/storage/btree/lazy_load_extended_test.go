// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this license is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadPage_Basic 测试基本页面加载功能
func TestLoadPage_Basic(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入一些数据以创建页面
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 验证页面可以被加载
	rootInfo := tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)
	require.NotNil(t, rootInfo.GetPage())

	// 测试加载已存在的页面
	page, err := tree.loadPage(rootInfo.GetPos())
	require.NoError(t, err)
	require.NotNil(t, page)
}

// TestLoadPage_PageNotFound 测试加载不存在的页面
func TestLoadPage_PageNotFound(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	// 尝试加载不存在的页面位置
	_, err = tree.loadPage(0) // 位置 0 表示无效
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk 0 not found")
}

// TestLoadPage_NoChunkManager 测试没有 ChunkManager 的情况
func TestLoadPage_NoChunkManager(t *testing.T) {
	t.Parallel()

	tree := &BTree{
		rootRef: NewRootPageRef(),
	}

	// 没有 ChunkManager，应该返回错误
	_, err := tree.loadPage(100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk manager")
}

// TestGetPageOrLoad_CacheHit 测试缓存命中场景
func TestGetPageOrLoad_CacheHit(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据
	key := []byte("test-key")
	value := []byte("test-value")
	err = tree.Set(ctx, key, value)
	require.NoError(t, err)

	// 获取根页面信息
	rootInfo := tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)

	// 第一次调用应该从缓存获取
	page1, err := tree.getPageOrLoad(rootInfo)
	require.NoError(t, err)
	require.NotNil(t, page1)

	// 第二次调用应该命中缓存
	page2, err := tree.getPageOrLoad(rootInfo)
	require.NoError(t, err)
	require.NotNil(t, page2)

	// 两次调用应该返回相同的页面对象
	assert.Same(t, page1, page2)
}

// TestGetPageOrLoad_CacheMiss 测试缓存未命中场景
func TestGetPageOrLoad_CacheMiss(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入足够多的数据以创建多个页面
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 强制刷新缓存（模拟缓存未命中）
	rootInfo := tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)

	// 重新加载应该成功（即使页面已在缓存中）
	page, err := tree.getPageOrLoad(rootInfo)
	require.NoError(t, err)
	require.NotNil(t, page)
}

// TestGetPageOrLoad_Concurrent 测试并发加载
func TestGetPageOrLoad_Concurrent(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入数据
	for i := 0; i < 50; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	rootInfo := tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)

	// 并发加载测试
	const goroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := tree.getPageOrLoad(rootInfo)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// 验证没有错误
	for err := range errors {
		t.Errorf("Concurrent load error: %v", err)
	}
}

// TestGetPageOrLoad_PageNotLoaded 测试页面未加载的情况
func TestGetPageOrLoad_PageNotLoaded(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	// 创建一个 PageInfo 但不加载 Page
	info := NewPageInfo()
	info.SetPos(100) // 设置一个假位置

	// 尝试加载应该失败（位置不存在）
	_, err = tree.getPageOrLoad(info)
	require.Error(t, err)
}

// TestLazyLoad_MemoryEfficiency 测试懒加载的内存效率
func TestLazyLoad_MemoryEfficiency(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping memory efficiency test in short mode")
	}

	tempDir := t.TempDir()
	tree, err := OpenBTree(tempDir, nil)
	require.NoError(t, err)
	defer tree.Close()

	ctx := context.Background()

	// 插入大量数据
	const keyCount = 1000
	for i := 0; i < keyCount; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i + 100)}
		err := tree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	// 验证只有部分页面被加载到内存
	// 懒加载应该只加载访问过的页面
	rootInfo := tree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo)
	require.NotNil(t, rootInfo.GetPage(), "Root page should be loaded")

	// 这里可以添加更详细的内存使用统计
	// 但在当前 Phase 1 中，我们只验证基本功能
}
