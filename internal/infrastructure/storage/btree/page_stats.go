// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"sort"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// PageStats 页面访问统计
// 用于识别热数据，触发自动物化优化
type PageStats struct {
	mu         sync.RWMutex
	readCounts map[model.PageID]int64
	lastAccess map[model.PageID]time.Time
}

// NewPageStats 创建新的页面统计
func NewPageStats() *PageStats {
	return &PageStats{
		readCounts: make(map[model.PageID]int64),
		lastAccess: make(map[model.PageID]time.Time),
	}
}

// IncrementReadCount 增加页面的读取计数
func (s *PageStats) IncrementReadCount(pageID model.PageID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readCounts[pageID]++
	s.lastAccess[pageID] = time.Now()
}

// GetReadCount 获取页面的读取计数
func (s *PageStats) GetReadCount(pageID model.PageID) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readCounts[pageID]
}

// GetTopReadPages 获取读取次数最多的页面
// 返回：按读取次数降序排列的页面 ID 列表
func (s *PageStats) GetTopReadPages(n int) []model.PageID {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type pageCount struct {
		id    model.PageID
		count int64
	}

	pages := make([]pageCount, 0, len(s.readCounts))
	for id, count := range s.readCounts {
		pages = append(pages, pageCount{id: id, count: count})
	}

	// 按读取次数降序排序
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].count > pages[j].count
	})

	// 返回前 n 个页面 ID
	result := make([]model.PageID, 0, n)
	for i := 0; i < n && i < len(pages); i++ {
		result = append(result, pages[i].id)
	}
	return result
}

// DecayReadCounts 衰减读计数（定期调用）
// factor: 衰减因子（0-1），如 0.9 表示衰减 10%
func (s *PageStats) DecayReadCounts(factor float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, lastAccess := range s.lastAccess {
		elapsed := now.Sub(lastAccess)
		if elapsed > time.Hour {
			// 超过 1 小时未访问，衰减计数
			if newCount := int64(float64(s.readCounts[id]) * factor); newCount > 0 {
				s.readCounts[id] = newCount
			} else {
				// 计数衰减到 0 或以下，删除记录
				delete(s.readCounts, id)
				delete(s.lastAccess, id)
			}
		}
	}
}

// Reset 重置统计信息
func (s *PageStats) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readCounts = make(map[model.PageID]int64)
	s.lastAccess = make(map[model.PageID]time.Time)
}

// GetStats 获取统计信息（用于调试和监控）
func (s *PageStats) GetStats() map[model.PageID]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[model.PageID]int64, len(s.readCounts))
	for id, count := range s.readCounts {
		result[id] = count
	}
	return result
}
