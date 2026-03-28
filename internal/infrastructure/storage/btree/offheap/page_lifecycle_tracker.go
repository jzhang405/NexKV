// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"fmt"
	"runtime"
	"slices"
	"sync"
	"time"
)

// PageLifecycleTracker 追踪页面生命周期（用于调试循环引用问题）
// 这是一个调试工具，仅在调查阶段使用，不应在生产环境启用
type PageLifecycleTracker struct {
	enabled   bool                      // 是否启用追踪
	history   map[uint32]*PageLifecycle // 页面生命周期历史
	mu        sync.RWMutex              // 保护 history 的并发访问
	allocHook func(uint32)              // 分配钩子
	freeHook  func(uint32)              // 释放钩子
}

// PageLifecycle 记录单个页面的生命周期
type PageLifecycle struct {
	PageID         uint32    // 页面 ID
	AllocTime      time.Time // 分配时间
	AllocCaller    string    // 分配调用栈
	FreeTime       time.Time // 释放时间
	FreeCaller     string    // 释放调用栈
	ReusedCount    int       // 重用次数
	ParentPageIDs  []uint32  // 父节点页面 ID 列表（用于 B-Tree）
	ChildPageIDs   []uint32  // 子节点页面 ID 列表（用于 B-Tree）
	PageType       string    // 页面类型（Leaf/Internal）
	LastAccessType time.Time // 最后访问时间
}

// NewPageLifecycleTracker 创建页面生命周期追踪器
func NewPageLifecycleTracker(enabled bool) *PageLifecycleTracker {
	return &PageLifecycleTracker{
		enabled: enabled,
		history: make(map[uint32]*PageLifecycle),
	}
}

// Enable 启用追踪
func (t *PageLifecycleTracker) Enable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = true
}

// Disable 禁用追踪
func (t *PageLifecycleTracker) Disable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = false
}

// RecordAlloc 记录页面分配
func (t *PageLifecycleTracker) RecordAlloc(pageID uint32) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.history[pageID]; ok {
		existing.ReusedCount++
		existing.AllocTime = time.Now()
		existing.AllocCaller = getCaller(3)
		existing.FreeTime = time.Time{}
		existing.FreeCaller = ""
		existing.LastAccessType = time.Now()
	} else {
		t.history[pageID] = &PageLifecycle{
			PageID:         pageID,
			AllocTime:      time.Now(),
			AllocCaller:    getCaller(3),
			ParentPageIDs:  make([]uint32, 0),
			ChildPageIDs:   make([]uint32, 0),
			LastAccessType: time.Now(),
		}
	}

	if t.allocHook != nil {
		t.allocHook(pageID)
	}
}

// RecordFree 记录页面释放
func (t *PageLifecycleTracker) RecordFree(pageID uint32) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	lifecycle, ok := t.history[pageID]
	if !ok {
		return
	}

	lifecycle.FreeTime = time.Now()
	lifecycle.FreeCaller = getCaller(3)

	if t.freeHook != nil {
		t.freeHook(pageID)
	}
}

// SetParentPageID 设置父节点
func (t *PageLifecycleTracker) SetParentPageID(pageID, parentPageID uint32) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	lifecycle, ok := t.history[pageID]
	if !ok {
		return
	}

	if slices.Contains(lifecycle.ParentPageIDs, parentPageID) {
		return
	}

	lifecycle.ParentPageIDs = append(lifecycle.ParentPageIDs, parentPageID)
	lifecycle.LastAccessType = time.Now()
}

// SetChildPageID 设置子节点
func (t *PageLifecycleTracker) SetChildPageID(pageID, childPageID uint32) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	lifecycle, ok := t.history[pageID]
	if !ok {
		return
	}

	if slices.Contains(lifecycle.ChildPageIDs, childPageID) {
		return
	}

	lifecycle.ChildPageIDs = append(lifecycle.ChildPageIDs, childPageID)
	lifecycle.LastAccessType = time.Now()
}

// SetPageType 设置页面类型
func (t *PageLifecycleTracker) SetPageType(pageID uint32, pageType string) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	lifecycle, ok := t.history[pageID]
	if !ok {
		return
	}

	lifecycle.PageType = pageType
	lifecycle.LastAccessType = time.Now()
}

// RecordAccess 记录页面访问
func (t *PageLifecycleTracker) RecordAccess(pageID uint32) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	lifecycle, ok := t.history[pageID]
	if !ok {
		return
	}

	lifecycle.LastAccessType = time.Now()
}

// GetLifecycle 获取页面生命周期信息
func (t *PageLifecycleTracker) GetLifecycle(pageID uint32) (*PageLifecycle, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	lifecycle, ok := t.history[pageID]
	if !ok {
		return nil, false
	}

	// 返回副本，避免并发修改
	copy := *lifecycle
	return &copy, true
}

// GetAllLifecycles 获取所有页面生命周期信息
func (t *PageLifecycleTracker) GetAllLifecycles() map[uint32]*PageLifecycle {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[uint32]*PageLifecycle, len(t.history))
	for pageID, lifecycle := range t.history {
		copy := *lifecycle
		result[pageID] = &copy
	}
	return result
}

// GetPage4095Report 获取 Page 4095 的专门报告
func (t *PageLifecycleTracker) GetPage4095Report() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	lifecycle, ok := t.history[4095]
	if !ok {
		return "Page 4095 not found in history"
	}

	report := fmt.Sprintf(`
=== Page 4095 Lifecycle Report ===
Allocated: %s
Caller: %s
Freed: %s
Caller: %s
Lifespan: %s
Reused Count: %d
Page Type: %s
Parent Page IDs: %v
Child Page IDs: %v
Last Access: %s
`,
		lifecycle.AllocTime.Format("2006-01-02 15:04:05.000"),
		lifecycle.AllocCaller,
		lifecycle.FreeTime.Format("2006-01-02 15:04:05.000"),
		lifecycle.FreeCaller,
		lifecycle.FreeTime.Sub(lifecycle.AllocTime).Round(time.Millisecond),
		lifecycle.ReusedCount,
		lifecycle.PageType,
		lifecycle.ParentPageIDs,
		lifecycle.ChildPageIDs,
		lifecycle.LastAccessType.Format("2006-01-02 15:04:05.000"),
	)

	return report
}

// GetHighPageIDReport 获取所有 > 4000 的页面报告
func (t *PageLifecycleTracker) GetHighPageIDReport() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	report := "=== High Page ID Report (> 4000) ===\n"
	count := 0

	for pageID, lifecycle := range t.history {
		if pageID > 4000 {
			count++
			report += fmt.Sprintf("\n--- Page %d ---\n", pageID)
			report += fmt.Sprintf("Allocated: %s\n", lifecycle.AllocTime.Format("15:04:05.000"))
			report += fmt.Sprintf("Caller: %s\n", lifecycle.AllocCaller)
			report += fmt.Sprintf("Freed: %s\n", lifecycle.FreeTime.Format("15:04:05.000"))
			report += fmt.Sprintf("Reused: %d\n", lifecycle.ReusedCount)
			report += fmt.Sprintf("Type: %s\n", lifecycle.PageType)
		}
	}

	report += fmt.Sprintf("\nTotal high page IDs: %d\n", count)
	return report
}

// SetAllocHook 设置分配钩子
func (t *PageLifecycleTracker) SetAllocHook(hook func(uint32)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.allocHook = hook
}

// SetFreeHook 设置释放钩子
func (t *PageLifecycleTracker) SetFreeHook(hook func(uint32)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.freeHook = hook
}

// Clear 清空追踪历史
func (t *PageLifecycleTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.history = make(map[uint32]*PageLifecycle)
}

// Stats 返回追踪器统计信息
func (t *PageLifecycleTracker) Stats() map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	totalAllocs := len(t.history)
	totalFrees := 0
	reusedCount := 0
	highPageIDCount := 0

	for _, lifecycle := range t.history {
		if !lifecycle.FreeTime.IsZero() {
			totalFrees++
		}
		if lifecycle.ReusedCount > 0 {
			reusedCount += lifecycle.ReusedCount
		}
		if lifecycle.PageID > 4000 {
			highPageIDCount++
		}
	}

	return map[string]interface{}{
		"total_allocs":       totalAllocs,
		"total_frees":        totalFrees,
		"active_pages":       totalAllocs - totalFrees,
		"reused_count":       reusedCount,
		"high_page_id_count": highPageIDCount,
	}
}

// getCaller 获取调用栈信息
func getCaller(skip int) string {
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return fmt.Sprintf("unknown(pc=%x)", pc)
	}
	return fn.Name()
}
