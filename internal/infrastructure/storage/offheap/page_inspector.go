// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package offheap

import (
	"fmt"
	"runtime"
	"slices"
	"sort"
	"sync"
	"time"
)

// PageInspector 追踪页面生命周期（用于调试循环引用问题）
// 这是一个调试工具，仅在调查阶段使用，不应在生产环境启用
type PageInspector struct {
	enabled bool                   // 是否启用追踪
	history map[uint32]*PageRecord // 页面生命周期历史（仅保留 active pages）
	mu      sync.RWMutex           // 保护 history 的并发访问
}

// PageRecord 记录单个页面的生命周期
type PageRecord struct {
	PageID         uint32    // 页面 ID
	AllocTime      time.Time // 分配时间
	AllocCaller    string    // 分配调用栈
	ReusedCount    int       // 重用次数
	ParentPageIDs  []uint32  // 父节点页面 ID 列表（用于 B-Tree）
	ChildPageIDs   []uint32  // 子节点页面 ID 列表（用于 B-Tree）
	PageType       string    // 页面类型（Leaf/Internal）
	LastAccessType time.Time // 最后访问时间
}

// LeakReport 泄漏报告
type LeakReport struct {
	PageID      uint32
	AllocTime   time.Time
	AllocCaller string
	Age         time.Duration
}

// NewPageInspector 创建页面生命周期追踪器
func NewPageInspector(enabled bool) *PageInspector {
	return &PageInspector{
		enabled: enabled,
		history: make(map[uint32]*PageRecord),
	}
}

// Enable 启用追踪
func (t *PageInspector) Enable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = true
}

// Disable 禁用追踪
func (t *PageInspector) Disable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = false
}

// RecordAlloc 记录页面分配
func (t *PageInspector) RecordAlloc(pageID uint32) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.history[pageID]; ok {
		existing.ReusedCount++
		existing.AllocTime = time.Now()
		existing.AllocCaller = getCaller(3)
		existing.LastAccessType = time.Now()
	} else {
		t.history[pageID] = &PageRecord{
			PageID:         pageID,
			AllocTime:      time.Now(),
			AllocCaller:    getCaller(3),
			ParentPageIDs:  make([]uint32, 0),
			ChildPageIDs:   make([]uint32, 0),
			LastAccessType: time.Now(),
		}
	}
}

// RecordFree 记录页面释放
// 释放后从 history 中删除记录，防止长期运行时内存无限增长
func (t *PageInspector) RecordFree(pageID uint32) {
	if !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.history, pageID)
}

// SetParentPageID 设置父节点
func (t *PageInspector) SetParentPageID(pageID, parentPageID uint32) {
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
func (t *PageInspector) SetChildPageID(pageID, childPageID uint32) {
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
func (t *PageInspector) SetPageType(pageID uint32, pageType string) {
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
func (t *PageInspector) RecordAccess(pageID uint32) {
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

// GetLifecycle 获取页面生命周期信息（深拷贝）
func (t *PageInspector) GetLifecycle(pageID uint32) (*PageRecord, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	lifecycle, ok := t.history[pageID]
	if !ok {
		return nil, false
	}

	return deepCopyLifecycle(lifecycle), true
}

// GetAllLifecycles 获取所有页面生命周期信息（深拷贝）
func (t *PageInspector) GetAllLifecycles() map[uint32]*PageRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[uint32]*PageRecord, len(t.history))
	for pageID, lifecycle := range t.history {
		result[pageID] = deepCopyLifecycle(lifecycle)
	}
	return result
}

// DetectLeaks 检测疑似泄漏的页面
// threshold: 分配后超过此时间仍未释放视为疑似泄漏
func (t *PageInspector) DetectLeaks(threshold time.Duration) []LeakReport {
	if !t.enabled {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var leaks []LeakReport
	now := time.Now()

	for pageID, lc := range t.history {
		age := now.Sub(lc.AllocTime)
		if age > threshold {
			leaks = append(leaks, LeakReport{
				PageID:      pageID,
				AllocTime:   lc.AllocTime,
				AllocCaller: lc.AllocCaller,
				Age:         age,
			})
		}
	}

	sort.Slice(leaks, func(i, j int) bool {
		return leaks[i].Age > leaks[j].Age
	})

	return leaks
}

// Stats 返回追踪器统计信息
func (t *PageInspector) Stats() map[string]any {
	t.mu.RLock()
	defer t.mu.RUnlock()

	activePages := len(t.history)
	reusedCount := 0

	for _, lifecycle := range t.history {
		reusedCount += lifecycle.ReusedCount
	}

	return map[string]any{
		"active_pages": activePages,
		"reused_count": reusedCount,
	}
}

// deepCopyLifecycle 深拷贝 PageRecord，避免切片的浅拷贝问题
func deepCopyLifecycle(lc *PageRecord) *PageRecord {
	cp := *lc
	cp.ParentPageIDs = slices.Clone(lc.ParentPageIDs)
	cp.ChildPageIDs = slices.Clone(lc.ChildPageIDs)
	return &cp
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
