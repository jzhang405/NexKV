// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPageInspector_BasicTracking(t *testing.T) {
	tracker := NewPageInspector(true)
	require.NotNil(t, tracker)

	// 记录分配
	tracker.RecordAlloc(100)
	lifecycle, ok := tracker.GetLifecycle(100)
	require.True(t, ok)
	assert.Equal(t, uint32(100), lifecycle.PageID)
	assert.False(t, lifecycle.AllocTime.IsZero())

	// 记录释放 → 从 history 中删除
	tracker.RecordFree(100)
	_, ok = tracker.GetLifecycle(100)
	assert.False(t, ok, "freed page should be removed from history")
}

func TestPageInspector_EnableDisable(t *testing.T) {
	tracker := NewPageInspector(false) // 初始禁用

	// 禁用状态下不应该记录
	tracker.RecordAlloc(100)
	_, ok := tracker.GetLifecycle(100)
	assert.False(t, ok)

	// 启用追踪
	tracker.Enable()
	tracker.RecordAlloc(100)
	_, ok = tracker.GetLifecycle(100)
	assert.True(t, ok)

	// 禁用追踪
	tracker.Disable()
	tracker.RecordAlloc(200)
	_, ok = tracker.GetLifecycle(200)
	assert.False(t, ok)
}

func TestPageInspector_PageReuse(t *testing.T) {
	tracker := NewPageInspector(true)

	// 第一次分配-释放（RecordFree 会删除记录）
	tracker.RecordAlloc(42)
	tracker.RecordFree(42)

	// 第二次分配（重用同一 pageID，但 history 已清空，所以是新记录）
	tracker.RecordAlloc(42)
	lifecycle, ok := tracker.GetLifecycle(42)
	require.True(t, ok)
	assert.Equal(t, 0, lifecycle.ReusedCount, "new alloc after free starts fresh")
}

func TestPageInspector_ParentChildTracking(t *testing.T) {
	tracker := NewPageInspector(true)

	tracker.RecordAlloc(100)
	tracker.RecordAlloc(200)
	tracker.SetParentPageID(200, 100)
	tracker.SetChildPageID(100, 200)

	lc200, _ := tracker.GetLifecycle(200)
	assert.Equal(t, []uint32{100}, lc200.ParentPageIDs)

	lc100, _ := tracker.GetLifecycle(100)
	assert.Equal(t, []uint32{200}, lc100.ChildPageIDs)
}

func TestPageInspector_DeepCopy(t *testing.T) {
	tracker := NewPageInspector(true)

	tracker.RecordAlloc(100)
	tracker.SetParentPageID(100, 1)
	tracker.SetChildPageID(100, 2)

	// GetLifecycle 返回深拷贝
	lc1, _ := tracker.GetLifecycle(100)
	lc1.ParentPageIDs[0] = 999 // 修改副本

	lc2, _ := tracker.GetLifecycle(100)
	assert.Equal(t, uint32(1), lc2.ParentPageIDs[0], "original should not be affected")

	// GetAllLifecycles 也返回深拷贝
	all := tracker.GetAllLifecycles()
	all[100].ChildPageIDs[0] = 888

	lc3, _ := tracker.GetLifecycle(100)
	assert.Equal(t, uint32(2), lc3.ChildPageIDs[0], "original should not be affected")
}

func TestPageInspector_DetectLeaks(t *testing.T) {
	tracker := NewPageInspector(true)

	// 分配 3 个页面
	tracker.RecordAlloc(1)
	tracker.RecordAlloc(2)
	tracker.RecordAlloc(3)

	// 释放 page 2
	tracker.RecordFree(2)

	// 等待一小段时间
	time.Sleep(10 * time.Millisecond)

	// 阈值设为 5ms → page 1 和 3 应该被报告为泄漏
	leaks := tracker.DetectLeaks(5 * time.Millisecond)
	assert.Equal(t, 2, len(leaks), "should detect 2 leaked pages")

	// 按泄漏时长排序（最长在前）
	assert.Equal(t, uint32(1), leaks[0].PageID)
	assert.Equal(t, uint32(3), leaks[1].PageID)
	assert.True(t, leaks[0].Age >= 5*time.Millisecond)
}

func TestPageInspector_DetectLeaks_Disabled(t *testing.T) {
	tracker := NewPageInspector(false)
	leaks := tracker.DetectLeaks(time.Second)
	assert.Nil(t, leaks)
}

func TestPageInspector_Stats(t *testing.T) {
	tracker := NewPageInspector(true)

	// 分配 10 个页面
	for i := uint32(1); i <= 10; i++ {
		tracker.RecordAlloc(i)
	}

	// 释放 5 个
	for i := uint32(1); i <= 5; i++ {
		tracker.RecordFree(i)
	}

	stats := tracker.Stats()
	assert.Equal(t, 5, stats["active_pages"])
}

func TestPageInspector_FreePreventsOOM(t *testing.T) {
	tracker := NewPageInspector(true)

	// 分配并释放 1000 个页面
	for i := uint32(0); i < 1000; i++ {
		tracker.RecordAlloc(i)
		tracker.RecordFree(i)
	}

	// history 应该为空（释放即删除）
	assert.Equal(t, 0, len(tracker.GetAllLifecycles()))
}

func TestPageInspector_SetPageType(t *testing.T) {
	tracker := NewPageInspector(true)

	tracker.RecordAlloc(100)
	tracker.SetPageType(100, "Leaf")

	lc, ok := tracker.GetLifecycle(100)
	require.True(t, ok)
	assert.Equal(t, "Leaf", lc.PageType)

	// 不存在的页面
	tracker.SetPageType(999, "Internal")
	_, ok = tracker.GetLifecycle(999)
	assert.False(t, ok)
}

func TestPageInspector_RecordAccess(t *testing.T) {
	tracker := NewPageInspector(true)

	tracker.RecordAlloc(100)
	lc1, _ := tracker.GetLifecycle(100)
	initialAccess := lc1.LastAccessType

	time.Sleep(time.Millisecond)

	tracker.RecordAccess(100)
	lc2, _ := tracker.GetLifecycle(100)
	assert.True(t, lc2.LastAccessType.After(initialAccess))

	// 不存在的页面 — 无 panic
	tracker.RecordAccess(999)
}

func TestPageInspector_SetParentPageIDDedup(t *testing.T) {
	tracker := NewPageInspector(true)

	tracker.RecordAlloc(100)
	tracker.SetParentPageID(100, 1)
	tracker.SetParentPageID(100, 1) // 重复

	lc, _ := tracker.GetLifecycle(100)
	assert.Equal(t, []uint32{1}, lc.ParentPageIDs)
}

func TestPageInspector_SetChildPageIDDedup(t *testing.T) {
	tracker := NewPageInspector(true)

	tracker.RecordAlloc(100)
	tracker.SetChildPageID(100, 1)
	tracker.SetChildPageID(100, 1) // 重复

	lc, _ := tracker.GetLifecycle(100)
	assert.Equal(t, []uint32{1}, lc.ChildPageIDs)
}

func TestPageInspector_RecordAllocExisting(t *testing.T) {
	tracker := NewPageInspector(true)

	// 不释放，直接再次分配同一 pageID
	tracker.RecordAlloc(42)
	lc1, _ := tracker.GetLifecycle(42)
	assert.Equal(t, 0, lc1.ReusedCount)

	// 再次分配（模拟重用，不经过 Free）
	tracker.RecordAlloc(42)
	lc2, _ := tracker.GetLifecycle(42)
	assert.Equal(t, 1, lc2.ReusedCount)
}
