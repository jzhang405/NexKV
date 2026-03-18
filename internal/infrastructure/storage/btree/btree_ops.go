// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// ===== 已实现的辅助函数 =====

// GetDepth 返回当前树的深度
// 深度定义为从根节点到叶子节点的最长路径长度
func (b *BTree) GetDepth() int {
	rootInfo := b.rootRef.GetRootPageInfo()
	if rootInfo == nil {
		return 0
	}

	// 从根节点开始，向下遍历到叶子节点
	depth := 0
	currentPage := rootInfo.GetPage()

	for currentPage != nil {
		if leaf, ok := currentPage.(*LeafPage); ok && leaf != nil {
			// 到达叶子节点
			break
		}

		if internal, ok := currentPage.(*InternalPage); ok && internal != nil {
			// 内部节点，继续向下
			if len(internal.children) > 0 {
				// 获取第一个子节点
				childRef := internal.children[0]
				if childRef != nil {
					childInfo := childRef.pInfo.Load()
					if childInfo != nil {
						currentPage = childInfo.GetPage()
						depth++
						continue
					}
				}
			}
		}

		break
	}

	return depth + 1 // +1 因为 depth 从 0 开始计数
}

// GetStats 返回 BTree 的统计信息
func (b *BTree) GetStats() *BTreeStats {
	rootInfo := b.rootRef.GetRootPageInfo()
	if rootInfo == nil {
		return &BTreeStats{
			Depth:     0,
			MaxLevels: b.maxLevels,
			RootSize:  0,
			MaxKeys:   model.DefaultMaxKeys,
			MinKeys:   model.DefaultMinKeys,
		}
	}

	rootPage := rootInfo.GetPage()
	rootSize := 0
	if rootPage != nil {
		if leaf, ok := rootPage.(*LeafPage); ok {
			rootSize = leaf.NumKeys()
		} else if internal, ok := rootPage.(*InternalPage); ok {
			rootSize = internal.NumKeys()
		}
	}

	return &BTreeStats{
		Depth:     b.GetDepth(),
		MaxLevels: b.maxLevels,
		RootSize:  rootSize,
		MaxKeys:   model.DefaultMaxKeys,
		MinKeys:   model.DefaultMinKeys,
		// 统计功能
		TotalNodes: 0,
		TotalKeys:  0,
	}
}

// GetMaxLevels returns the maximum number of levels in the tree.
func (b *BTree) GetMaxLevels() int {
	return b.maxLevels
}

// SetMaxLevels sets the maximum number of levels in the tree.
func (b *BTree) SetMaxLevels(levels int) {
	b.maxLevels = levels
}

// BTreeStats holds BTree statistics.
type BTreeStats struct {
	Depth      int
	MaxLevels  int
	RootSize   int
	MaxKeys    int
	MinKeys    int
	TotalNodes int
	TotalKeys  int
}

// String returns a string representation of the stats.
func (s *BTreeStats) String() string {
	return fmt.Sprintf("Depth: %d/%d, RootSize: %d/%d",
		s.Depth, s.MaxLevels, s.RootSize, s.MaxKeys)
}

// ===== 已删除的废弃函数 =====
//
// 以下函数已被新 API 替代，已删除：
// - InsertWithSplit() → 使用 b.Set() 替代
// - DeleteWithMerge() → 使用 b.Delete() 替代
//
// 这些函数在 Week 14 重构时被移除，以简化代码库。
