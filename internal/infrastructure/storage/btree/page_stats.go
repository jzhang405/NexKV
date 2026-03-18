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
//
// ✅ 优化：使用 sync.Map 替代 map+sync.RWMutex，减少锁竞争
// 适用场景：高并发读写，key-value 存储
type PageStats struct {
	readCounts sync.Map // PageID -> int64
	lastAccess sync.Map // PageID -> time.Time
}

// NewPageStats 创建新的页面统计
func NewPageStats() *PageStats {
	return &PageStats{}
}

// IncrementReadCount 增加页面的读取计数
func (s *PageStats) IncrementReadCount(pageID model.PageID) {
	// 使用 LoadOrStore 获取或初始化计数
	actual, _ := s.readCounts.LoadOrStore(pageID, int64(0))
	count := actual.(int64)

	// 原子递增
	s.readCounts.Store(pageID, count+1)
	s.lastAccess.Store(pageID, time.Now())
}

// GetReadCount 获取页面的读取计数
func (s *PageStats) GetReadCount(pageID model.PageID) int64 {
	actual, ok := s.readCounts.Load(pageID)
	if !ok {
		return 0
	}
	return actual.(int64)
}

// GetTopReadPages 获取读取次数最多的页面
// 返回：按读取次数降序排列的页面 ID 列表
func (s *PageStats) GetTopReadPages(n int) []model.PageID {
	type pageCount struct {
		id    model.PageID
		count int64
	}

	pages := make([]pageCount, 0)
	s.readCounts.Range(func(key, value any) bool {
		pages = append(pages, pageCount{
			id:    key.(model.PageID),
			count: value.(int64),
		})
		return true
	})

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
	now := time.Now()

	s.lastAccess.Range(func(key, value any) bool {
		pageID := key.(model.PageID)
		lastAccess := value.(time.Time)
		elapsed := now.Sub(lastAccess)

		if elapsed > time.Hour {
			actualCount, ok := s.readCounts.Load(pageID)
			if !ok {
				return true
			}

			count := actualCount.(int64)
			newCount := int64(float64(count) * factor)

			if newCount > 0 {
				s.readCounts.Store(pageID, newCount)
			} else {
				// 计数衰减到 0 或以下，删除记录
				s.readCounts.Delete(pageID)
				s.lastAccess.Delete(pageID)
			}
		}
		return true
	})
}

// Reset 重置统计信息
func (s *PageStats) Reset() {
	s.readCounts.Range(func(key, _ any) bool {
		s.readCounts.Delete(key)
		return true
	})
	s.lastAccess.Range(func(key, _ any) bool {
		s.lastAccess.Delete(key)
		return true
	})
}

// GetStats 获取统计信息（用于调试和监控）
func (s *PageStats) GetStats() map[model.PageID]int64 {
	result := make(map[model.PageID]int64)
	s.readCounts.Range(func(key, value any) bool {
		result[key.(model.PageID)] = value.(int64)
		return true
	})
	return result
}
