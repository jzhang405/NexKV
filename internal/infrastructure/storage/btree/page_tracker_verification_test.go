// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPageTrackerVerification 验证页面追踪器是否工作
func TestPageTrackerVerification(t *testing.T) {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)
	defer btree.Close()

	// 启用页面追踪
	t.Log("[DEBUG] Enabling page tracking...")
	btree.offheapPM.EnablePageTracking()
	t.Log("[DEBUG] Page tracking enabled")

	// 验证追踪器是否真的启用了
	tracker := btree.offheapPM.GetPageTracker()
	if tracker == nil {
		t.Fatal("[DEBUG] Tracker is nil!")
	}

	// 检查追踪器的初始状态
	stats := btree.offheapPM.GetPageTrackingStats()
	t.Logf("[DEBUG] [BEFORE INSERT] Page tracking stats:")
	t.Logf("[DEBUG]   Total allocs: %v", stats["total_allocs"])

	// 直接测试 PageManager.Alloc() 是否调用追踪器
	t.Log("[DEBUG] Testing direct PageManager.Alloc()...")
	testPageID, err := btree.offheapPM.Alloc()
	require.NoError(t, err)
	t.Logf("[DEBUG] Directly allocated pageID: %d", testPageID)

	// 再次检查统计
	stats = btree.offheapPM.GetPageTrackingStats()
	t.Logf("[DEBUG] [AFTER DIRECT ALLOC] Page tracking stats:")
	t.Logf("[DEBUG]   Total allocs: %v", stats["total_allocs"])

	// 检查追踪器历史
	allLifecycles := tracker.GetAllLifecycles()
	t.Logf("[DEBUG] Total lifecycles in history: %d", len(allLifecycles))
	for pageID, lifecycle := range allLifecycles {
		t.Logf("[DEBUG]   pageID=%d: allocated=%v", pageID, !lifecycle.AllocTime.IsZero())
	}

	ctx := context.Background()

	// 分配一些页面 - 使用更多 key 来触发页面分裂
	const keysToInsert = 100
	for i := range keysToInsert {
		// 使用更长的 key 和 value 来减少每个页面能存储的 entry 数
		key := []byte{byte(i >> 8), byte(i), byte(i >> 16), byte(i >> 24)}
		value := make([]byte, 50) // 50 字节 value
		for j := range value {
			value[j] = byte(i + j)
		}

		err := btree.Set(ctx, key, value)
		require.NoError(t, err)
	}

	t.Logf("Inserted %d keys", keysToInsert)

	// 再次检查追踪器统计
	stats = btree.offheapPM.GetPageTrackingStats()
	t.Logf("[DEBUG] [AFTER INSERT] Page tracking stats:")
	t.Logf("[DEBUG]   Total allocs: %v", stats["total_allocs"])
	t.Logf("[DEBUG]   Total frees: %v", stats["total_frees"])
	t.Logf("[DEBUG]   Active pages: %v", stats["active_pages"])
	t.Logf("[DEBUG]   High pageID count: %v", stats["high_page_id_count"])

	// 验证追踪器是否捕获到分配
	totalAllocs, ok := stats["total_allocs"].(int)
	if !ok {
		t.Fatal("total_allocs not found in stats")
	}

	if totalAllocs == 0 {
		t.Logf("[DEBUG] ❌ Tracker captured 0 allocations")
		t.Logf("[DEBUG] This suggests either:")
		t.Logf("[DEBUG]   1. RecordAlloc() is not being called")
		t.Logf("[DEBUG]   2. tracker.enabled is false")
		t.Logf("[DEBUG]   3. Off-Heap mode uses different allocation path")
	} else {
		t.Logf("[DEBUG] ✓ Tracker captured %d allocations", totalAllocs)
	}
}
