// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPageLifecycleTracker_BasicTracking 测试基本的页面生命周期追踪
func TestPageLifecycleTracker_BasicTracking(t *testing.T) {
	tracker := NewPageLifecycleTracker(true)
	require.NotNil(t, tracker)

	// 记录分配
	tracker.RecordAlloc(100)
	lifecycle, ok := tracker.GetLifecycle(100)
	require.True(t, ok)
	assert.Equal(t, uint32(100), lifecycle.PageID)
	assert.False(t, lifecycle.AllocTime.IsZero())
	assert.True(t, lifecycle.FreeTime.IsZero())

	// 记录释放
	tracker.RecordFree(100)
	lifecycle, _ = tracker.GetLifecycle(100)
	assert.False(t, lifecycle.FreeTime.IsZero())
}

// TestPageLifecycleTracker_Page4095Tracking 测试 Page 4095 的专门追踪
func TestPageLifecycleTracker_Page4095Tracking(t *testing.T) {
	tracker := NewPageLifecycleTracker(true)

	// 分配 Page 4095
	tracker.RecordAlloc(4095)

	// 设置父节点
	tracker.SetParentPageID(4095, 100)
	tracker.SetParentPageID(4095, 101)

	// 设置子节点
	tracker.SetChildPageID(4095, 200)
	tracker.SetChildPageID(4095, 201)

	// 设置页面类型
	tracker.SetPageType(4095, "Leaf")

	// 记录释放
	tracker.RecordFree(4095)

	// 获取报告
	report := tracker.GetPage4095Report()
	assert.Contains(t, report, "Page 4095 Lifecycle Report")
	assert.Contains(t, report, "Leaf")
}

// TestPageLifecycleTracker_HighPageIDTracking 测试高 PageID 的追踪
func TestPageLifecycleTracker_HighPageIDTracking(t *testing.T) {
	tracker := NewPageLifecycleTracker(true)

	// 分配多个高 PageID
	for i := 4001; i <= 4010; i++ {
		tracker.RecordAlloc(uint32(i))
		tracker.SetPageType(uint32(i), "Internal")
		tracker.RecordFree(uint32(i))
	}

	// 获取报告
	report := tracker.GetHighPageIDReport()
	assert.Contains(t, report, "High Page ID Report")
	assert.Contains(t, report, "Total high page IDs: 10")
}

// TestPageLifecycleTracker_PageReuse 测试页面重用检测
func TestPageLifecycleTracker_PageReuse(t *testing.T) {
	tracker := NewPageLifecycleTracker(true)

	// 第一次分配
	tracker.RecordAlloc(4095)
	tracker.RecordFree(4095)

	// 第二次分配（模拟重用）
	tracker.RecordAlloc(4095)
	tracker.RecordFree(4095)

	// 第三次分配（模拟重用）
	tracker.RecordAlloc(4095)

	lifecycle, ok := tracker.GetLifecycle(4095)
	require.True(t, ok)
	assert.Equal(t, 2, lifecycle.ReusedCount) // 释放后重用了2次
}

// TestPageLifecycleTracker_ParentChildTracking 测试父子节点关系追踪
func TestPageLifecycleTracker_ParentChildTracking(t *testing.T) {
	tracker := NewPageLifecycleTracker(true)

	// 创建 B-Tree 结构
	// Root (100) → Internal (200) → Leaf (300, 301)
	tracker.RecordAlloc(100)
	tracker.SetPageType(100, "Root")

	tracker.RecordAlloc(200)
	tracker.SetPageType(200, "Internal")
	tracker.SetParentPageID(200, 100) // 200 的父节点是 100
	tracker.SetChildPageID(100, 200)  // 100 的子节点是 200

	tracker.RecordAlloc(300)
	tracker.SetPageType(300, "Leaf")
	tracker.SetParentPageID(300, 200) // 300 的父节点是 200
	tracker.SetChildPageID(200, 300)  // 200 的子节点是 300

	tracker.RecordAlloc(301)
	tracker.SetPageType(301, "Leaf")
	tracker.SetParentPageID(301, 200) // 301 的父节点是 200
	tracker.SetChildPageID(200, 301)  // 200 的子节点是 301

	// 验证关系
	lifecycle200, _ := tracker.GetLifecycle(200)
	assert.Equal(t, 1, len(lifecycle200.ParentPageIDs))
	assert.Equal(t, uint32(100), lifecycle200.ParentPageIDs[0])
	assert.Equal(t, 2, len(lifecycle200.ChildPageIDs))

	lifecycle300, _ := tracker.GetLifecycle(300)
	assert.Equal(t, 1, len(lifecycle300.ParentPageIDs))
	assert.Equal(t, uint32(200), lifecycle300.ParentPageIDs[0])
}

// TestPageLifecycleTracker_Stats 测试统计信息
func TestPageLifecycleTracker_Stats(t *testing.T) {
	tracker := NewPageLifecycleTracker(true)

	// 分配 100 个页面
	for i := 1; i <= 100; i++ {
		tracker.RecordAlloc(uint32(i))
	}

	// 释放 50 个页面
	for i := 1; i <= 50; i++ {
		tracker.RecordFree(uint32(i))
	}

	// 分配一些高 PageID
	tracker.RecordAlloc(4095)
	tracker.RecordAlloc(4096)
	tracker.RecordAlloc(4097)

	// 获取统计
	stats := tracker.Stats()
	assert.Equal(t, 103, stats["total_allocs"])
	assert.Equal(t, 50, stats["total_frees"])
	assert.Equal(t, 53, stats["active_pages"])
	assert.Equal(t, 3, stats["high_page_id_count"])
}

// TestPageLifecycleTracker_EnableDisable 测试启用/禁用功能
func TestPageLifecycleTracker_EnableDisable(t *testing.T) {
	tracker := NewPageLifecycleTracker(false) // 初始禁用

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

// TestPageLifecycleTracker_Clear 测试清空功能
func TestPageLifecycleTracker_Clear(t *testing.T) {
	tracker := NewPageLifecycleTracker(true)

	// 分配一些页面
	tracker.RecordAlloc(100)
	tracker.RecordAlloc(200)
	tracker.RecordAlloc(300)

	// 清空
	tracker.Clear()

	// 验证已清空
	_, ok := tracker.GetLifecycle(100)
	assert.False(t, ok)
	_, ok = tracker.GetLifecycle(200)
	assert.False(t, ok)
	_, ok = tracker.GetLifecycle(300)
	assert.False(t, ok)

	stats := tracker.Stats()
	assert.Equal(t, 0, stats["total_allocs"])
}

// TestPageLifecycleTracker_Hooks 测试钩子函数
func TestPageLifecycleTracker_Hooks(t *testing.T) {
	tracker := NewPageLifecycleTracker(true)

	allocCalled := false
	freeCalled := false

	tracker.SetAllocHook(func(pageID uint32) {
		if pageID == 4095 {
			allocCalled = true
		}
	})

	tracker.SetFreeHook(func(pageID uint32) {
		if pageID == 4095 {
			freeCalled = true
		}
	})

	// 分配和释放 Page 4095
	tracker.RecordAlloc(4095)
	tracker.RecordFree(4095)

	assert.True(t, allocCalled, "Alloc hook should be called")
	assert.True(t, freeCalled, "Free hook should be called")
}
