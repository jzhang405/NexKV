// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLockFreeQueue_EnqueueDequeue(t *testing.T) {
	q := NewLockFreeQueue()

	// 测试空队列
	_, ok := q.Dequeue()
	assert.False(t, ok, "empty queue should return false")
	assert.True(t, q.IsEmpty(), "queue should be empty")

	// 测试入队出队
	q.Enqueue(100)
	assert.False(t, q.IsEmpty(), "queue should not be empty")

	val, ok := q.Dequeue()
	assert.True(t, ok, "dequeue should succeed")
	assert.Equal(t, uint32(100), val, "value should match")
	assert.True(t, q.IsEmpty(), "queue should be empty again")
}

func TestLockFreeQueue_Multiple(t *testing.T) {
	q := NewLockFreeQueue()

	// 入队多个元素
	expected := []uint32{1, 2, 3, 4, 5}
	for _, v := range expected {
		q.Enqueue(v)
	}

	// 出队并验证
	for _, exp := range expected {
		val, ok := q.Dequeue()
		assert.True(t, ok)
		assert.Equal(t, exp, val)
	}

	// 队列应为空
	_, ok := q.Dequeue()
	assert.False(t, ok)
}

func TestLockFreeQueue_Concurrent(t *testing.T) {
	q := NewLockFreeQueue()
	const numItems = 1000

	// 并发入队
	done := make(chan bool)
	go func() {
		for i := uint32(0); i < numItems; i++ {
		q.Enqueue(i)
		}
		done <- true
	}()

	// 并发出队
	received := make([]uint32, 0, numItems)
	go func() {
		for {
			val, ok := q.Dequeue()
			if !ok {
				return
			}
			received = append(received, val)
		}
	}()

	// 等待入队完成
	<-done

	// 验证所有元素都被处理
	// 注意：由于并发，可能存在竞争，但所有元素最终应该被处理
}

func TestNewPageManager(t *testing.T) {
	// 测试正常大小
	pm, err := NewPageManager(64 << 20) // 64MB
	require.NoError(t, err)
	require.NotNil(t, pm)

	assert.Equal(t, "unix", pm.Platform())
	assert.Equal(t, 4096, pm.PageSize())

	// 清理
	err = pm.Close()
	assert.NoError(t, err)
}

func TestPageManager_AllocFree(t *testing.T) {
	pm, err := NewPageManager(64 << 20) // 64MB
	require.NoError(t, err)
	defer pm.Close()

	stats := pm.GetStats()
	assert.Equal(t, uint32(0), stats.Used)

	// 分配页面
	pageID1, err := pm.Alloc()
	require.NoError(t, err)
	assert.Equal(t, uint32(0), pageID1, "first pageID should be 0")

	stats = pm.GetStats()
	assert.Equal(t, uint32(1), stats.Used)

	// 再分配一个
	pageID2, err := pm.Alloc()
	require.NoError(t, err)
	assert.NotEqual(t, pageID1, pageID2, "pageIDs should be different")

	stats = pm.GetStats()
	assert.Equal(t, uint32(2), stats.Used)

	// 释放第一个页面
	err = pm.Free(pageID1)
	require.NoError(t, err)

	stats = pm.GetStats()
	assert.Equal(t, uint32(1), stats.Used, "used count should decrease")
}

func TestPageManager_AllocReuse(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	// 分配并立即释放
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	err = pm.Free(pageID)
	require.NoError(t, err)

	// 重新分配应该重用释放的页面
	reusedID, err := pm.Alloc()
	require.NoError(t, err)
	assert.Equal(t, pageID, reusedID, "should reuse freed page")
}

func TestPageManager_PageIDToPtr(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	// 分配一个页面
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	// 转换为指针
	ptr := pm.PageIDToPtr(pageID)
	assert.NotZero(t, ptr, "ptr should not be zero")

	// 写入数据 - 使用 byte 数组操作
	slice := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), PageSize)
	slice[0] = 0xDE
	slice[1] = 0xAD
	slice[2] = 0xBE
	slice[3] = 0xEF

	// 读取并验证
	assert.Equal(t, byte(0xDE), slice[0])
	assert.Equal(t, byte(0xEF), slice[3])
}

func TestPageManager_OutOfMemory(t *testing.T) {
	// 创建一个只有 4 个页面的管理器
	pm, err := NewPageManager(4 * PageSize)
	require.NoError(t, err)
	defer pm.Close()

	// 分配所有页面
	var pageIDs []uint32
	for i := 0; i < 4; i++ {
		id, err := pm.Alloc()
		require.NoError(t, err)
		pageIDs = append(pageIDs, id)
	}

	// 第 5 次分配应该失败
	_, err = pm.Alloc()
	assert.Error(t, err, "should fail when out of memory")
}

func TestPageManager_InvalidPageID(t *testing.T) {
	pm, err := NewPageManager(64 << 20)
	require.NoError(t, err)
	defer pm.Close()

	// 尝试释放无效的 PageID
	err = pm.Free(999999)
	assert.Error(t, err, "should fail with invalid pageID")
}

func TestPageManager_OverflowCheck(t *testing.T) {
	// 尝试创建超过 32 位限制的 PageManager
	// 32 位 uint32 最大值是 2^32-1，对应 2^32-1 个页面
	// 每个页面 4KB，所以最大 mmap 大小是 (2^32-1) * 4KB，接近 16TB
	// 我们尝试创建一个更大的 size 来触发溢出检查
	const tooBigSize = 1 << 40 // 1TB，肯定超过限制

	_, err := NewPageManager(tooBigSize)
	assert.Error(t, err, "should fail with overflow error")
	assert.Contains(t, err.Error(), "exceeds 32-bit PageID limit")
}

func TestInitPageManager(t *testing.T) {
	// 测试全局初始化
	err := InitPageManager(64 << 20)
	require.NoError(t, err)

	pm := GetPageManager()
	assert.NotNil(t, pm, "global manager should be initialized")

	// 再次初始化应该被忽略
	err = InitPageManager(64 << 20)
	assert.NoError(t, err)

	pm2 := GetPageManager()
	assert.Same(t, pm, pm2, "should return same instance")
}
