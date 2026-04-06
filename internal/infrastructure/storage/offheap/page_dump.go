// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package offheap

import "fmt"

// PageSummary 是单个物理页面的诊断摘要，用于 Inspector 遍历。
type PageSummary struct {
	PageID   uint32
	IsLeaf   bool
	Count    uint16   // 条目数
	Version  uint64   // 页面版本
	Deleted  bool     // 是否已标记删除
	Keys     [][]byte // 所有 key（leaf: 数据 key；index: separator key）
	Children []uint32 // 子节点 PageID（仅 index page）
}

// DumpAllPages 遍历所有已分配的物理页面（pageID ∈ [1, nextPageID)），
// 返回每个页面的摘要信息，跳过未初始化（version==0）的页面。
func (pa *PageAccessor) DumpAllPages() []PageSummary {
	upper := pa.pm.nextPageID.Load()
	var result []PageSummary
	for pageID := uint32(1); pageID < upper; pageID++ {
		header := pa.GetHeader(pageID)
		if header.version == 0 {
			continue // 未初始化
		}
		summary := PageSummary{
			PageID:  pageID,
			IsLeaf:  header.pageType == PageTypeLeaf,
			Count:   header.count,
			Version: header.version,
			Deleted: header.deleted == 1,
		}

		count := int(header.count)
		if summary.IsLeaf {
			for i := range count {
				entry, err := pa.GetLeafEntrySafe(pageID, i)
				if err != nil {
					break // 页面被并发修改，count 已变
				}
				key := pa.GetKey(pageID, entry.keyOff, entry.keyLen)
				// copy 因为 mmap slice 不稳定
				cp := make([]byte, len(key))
				copy(cp, key)
				summary.Keys = append(summary.Keys, cp)
			}
		} else {
			for i := range count {
				entry, err := pa.GetIndexEntrySafe(pageID, i)
				if err != nil {
					break
				}
				key := pa.GetKey(pageID, entry.keyOff, entry.keyLen)
				cp := make([]byte, len(key))
				copy(cp, key)
				summary.Keys = append(summary.Keys, cp)

				childID, _ := DecodeChildWithVersion(entry.child)
				summary.Children = append(summary.Children, childID)
			}
			// extraChild (N+1 child)
			if count > 0 {
				extraChildID, _ := DecodeChildWithVersion(header.extraChild)
				summary.Children = append(summary.Children, extraChildID)
			}
		}

		result = append(result, summary)
	}
	return result
}

// String 返回人类可读的页面摘要。
func (s PageSummary) String() string {
	pageType := "Index"
	if s.IsLeaf {
		pageType = "Leaf"
	}
	status := "active"
	if s.Deleted {
		status = "deleted"
	}
	return fmt.Sprintf("Page{%d} %s count=%d ver=%d %s keys=%v children=%v",
		s.PageID, pageType, s.Count, s.Version, status, keyStrs(s.Keys), s.Children)
}

func keyStrs(keys [][]byte) []string {
	if len(keys) == 0 {
		return nil
	}
	result := make([]string, len(keys))
	for i, k := range keys {
		result[i] = string(k)
	}
	return result
}
