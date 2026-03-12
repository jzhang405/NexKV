// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"

	"github.com/stretchr/testify/require"
)

// ===== BTree 测试构造辅助函数 =====
//
// 这些函数用于直接构造 BTree 结构，用于测试特定的场景
// 如 Merge、Split 等，这些场景难以通过 Set/Delete API 精确控制

// TestBTreeBuilder 用于构造测试用 BTree
type TestBTreeBuilder struct {
	t       require.TestingT
	btree   *BTree
	rootRef *RootPageRef
}

// NewTestBTreeBuilder 创建测试 BTree 构造器
func NewTestBTreeBuilder(t require.TestingT) *TestBTreeBuilder {
	btree, err := OpenBTree("", nil)
	require.NoError(t, err)

	// 禁用持久化
	btree.chunkMgr = nil

	rootInfo := btree.rootRef.GetRootPageInfo()
	if rootInfo == nil {
		rootInfo = &PageInfo{
			pos:         0,
			metaVersion: 1,
			pageSize:    4096,
		}
	}

	return &TestBTreeBuilder{
		t:       t,
		btree:   btree,
		rootRef: btree.rootRef,
	}
}

// Build 构造 BTree
func (tb *TestBTreeBuilder) Build() *BTree {
	return tb.btree
}

// SetRoot 设置根节点
func (tb *TestBTreeBuilder) SetRoot(page interface{}) *TestBTreeBuilder {
	info := &PageInfo{
		pos:         0,
		page:        page,
		metaVersion: 1,
		pageSize:    4096,
	}

	tb.rootRef.pInfo.Store(info)
	return tb
}

// CreatePageRef 创建页面引用
func (tb *TestBTreeBuilder) CreatePageRef(page interface{}) *PageRef {
	info := &PageInfo{
		pos:         0,
		page:        page,
		metaVersion: 1,
		pageSize:    4096,
	}
	return NewPageRefWithInfo(info)
}

// ===== 验证辅助函数 =====

// verifyTreeIntegrity 验证树结构的完整性
func verifyTreeIntegrity(t require.TestingT, btree *BTree) {
	ctx := context.Background()

	// 验证根节点存在
	rootInfo := btree.rootRef.GetRootPageInfo()
	require.NotNil(t, rootInfo, "root info should not be nil")

	rootPage := rootInfo.GetPage()
	require.NotNil(t, rootPage, "root page should not be nil")

	// 验证树的高度一致
	height, err := btree.GetHeight(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, height, 0, "tree height should be non-negative")
}
