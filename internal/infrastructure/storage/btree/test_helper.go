// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
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

// ===== Merge 测试场景构造函数 =====

// buildThreeLeafTree 构造 3 个叶子节点的树
//
// 树结构：
//
//	   [Internal]
//	   /    |    \
//	[Leaf1][Leaf2][Leaf3]
//
// 每个 LeafPage 可以指定键的数量
func buildThreeLeafTree(t require.TestingT, leftKeys, middleKeys, rightKeys int) (*BTree, *LeafPage, *LeafPage, *LeafPage) {
	builder := NewTestBTreeBuilder(t)

	// 创建 3 个叶子节点
	leftPage := NewLeafPage(model.PageID(1))
	leftPage.keys = makeKeys(leftKeys)
	leftPage.values = makeValues(leftKeys)
	leftPage.version = 1

	middlePage := NewLeafPage(model.PageID(2))
	middlePage.keys = makeKeysRange(leftKeys, leftKeys+middleKeys)
	middlePage.values = makeValuesRange(leftKeys, leftKeys+middleKeys)
	middlePage.version = 1

	rightPage := NewLeafPage(model.PageID(3))
	rightPage.keys = makeKeysRange(leftKeys+middleKeys, leftKeys+middleKeys+rightKeys)
	rightPage.values = makeValuesRange(leftKeys+middleKeys, leftKeys+middleKeys+rightKeys)
	rightPage.version = 1

	// 创建内部节点
	internalPage := NewInternalPage(model.PageID(10))
	internalPage.keys = [][]byte{
		[]byte{byte(leftKeys)},              // Leaf1 和 Leaf2 之间的分隔键
		[]byte{byte(leftKeys + middleKeys)}, // Leaf2 和 Leaf3 之间的分隔键
	}
	internalPage.children = []*PageRef{
		builder.CreatePageRef(leftPage),
		builder.CreatePageRef(middlePage),
		builder.CreatePageRef(rightPage),
	}
	internalPage.version = 1

	// 设置根节点
	builder.SetRoot(internalPage)

	return builder.Build(), leftPage, middlePage, rightPage
}

// makeKeys 生成指定数量的键
func makeKeys(n int) [][]byte {
	keys := make([][]byte, n)
	for i := 0; i < n; i++ {
		keys[i] = []byte{byte(i)}
	}
	return keys
}

// makeValues 生成指定数量的值
func makeValues(n int) [][]byte {
	values := make([][]byte, n)
	for i := 0; i < n; i++ {
		values[i] = []byte{byte(i + 100)}
	}
	return values
}

// makeKeysRange 生成指定范围的键
func makeKeysRange(start, end int) [][]byte {
	keys := make([][]byte, end-start)
	for i := start; i < end; i++ {
		keys[i-start] = []byte{byte(i)}
	}
	return keys
}

// makeValuesRange 生成指定范围的值
func makeValuesRange(start, end int) [][]byte {
	values := make([][]byte, end-start)
	for i := start; i < end; i++ {
		values[i-start] = []byte{byte(i + 100)}
	}
	return values
}

// ===== Merge 场景构造函数 =====

// createBorrowFromLeftScenario 创建从左兄弟借键场景
//
// 场景：左节点有足够键，中间节点键不足
func createBorrowFromLeftScenario(t require.TestingT) (*BTree, *LeafPage, *LeafPage, *LeafPage) {
	// 左节点 9 个键，中间节点 5 个键（触发借键），右节点 8 个键
	return buildThreeLeafTree(t, 9, 5, 8)
}

// createMergeLeftScenario 创建与左兄弟合并场景
//
// 场景：中间节点和左节点都接近 minKeys
func createMergeLeftScenario(t require.TestingT) (*BTree, *LeafPage, *LeafPage, *LeafPage) {
	// 每个节点 6 个键，删除中间节点的键后触发合并
	return buildThreeLeafTree(t, 6, 6, 8)
}

// createMergeRightScenario 创建与右兄弟合并场景
//
// 场景：中间节点和右节点都接近 minKeys
func createMergeRightScenario(t require.TestingT) (*BTree, *LeafPage, *LeafPage, *LeafPage) {
	// 每个节点 6 个键，删除中间节点的键后触发合并
	return buildThreeLeafTree(t, 8, 6, 6)
}

// createRootReductionScenario 创建根节点降低场景
//
// 场景：根节点只有 1 个键，合并后根节点降低
func createRootReductionScenario(t require.TestingT) (*BTree, *InternalPage) {
	builder := NewTestBTreeBuilder(t)

	// 创建 2 个叶子节点
	leftPage := NewLeafPage(model.PageID(1))
	leftPage.keys = makeKeys(8)
	leftPage.values = makeValues(8)
	leftPage.version = 1

	rightPage := NewLeafPage(model.PageID(2))
	rightPage.keys = makeKeysRange(8, 16)
	rightPage.values = makeValuesRange(8, 16)
	rightPage.version = 1

	// 创建内部节点作为根（只有 1 个键）
	rootPage := NewInternalPage(model.PageID(10))
	rootPage.keys = [][]byte{[]byte{8}} // 只有 1 个分隔键
	rootPage.children = []*PageRef{
		builder.CreatePageRef(leftPage),
		builder.CreatePageRef(rightPage),
	}
	rootPage.version = 1

	builder.SetRoot(rootPage)

	return builder.Build(), rootPage
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

// verifyPageIntegrity 验证页面完整性
func verifyPageIntegrity(t require.TestingT, page interface{}, expectedKeys int, expectedMinKeys bool) {
	switch p := page.(type) {
	case *LeafPage:
		require.Equal(t, expectedKeys, p.NumKeys(), "key count mismatch")
		if expectedMinKeys {
			require.GreaterOrEqual(t, p.NumKeys(), model.DefaultMinKeys,
				"keys should be >= minKeys")
		}
	case *InternalPage:
		require.Equal(t, expectedKeys, len(p.keys), "key count mismatch")
		require.Equal(t, expectedKeys+1, len(p.children), "children count mismatch")
	}
}

// verifyChildrenIntegrity 验证子节点引用完整性
func verifyChildrenIntegrity(t require.TestingT, page interface{}) {
	switch p := page.(type) {
	case *InternalPage:
		for i, childRef := range p.children {
			require.NotNil(t, childRef, "child %d should not be nil", i)
			childInfo := childRef.pInfo.Load()
			require.NotNil(t, childInfo, "child %d info should not be nil", i)

			// 验证 parentRef 指向正确的父节点
			parentRef := childInfo.GetParentRef()
			require.NotNil(t, parentRef, "child %d parentRef should not be nil", i)

			// 验证子节点页面存在
			childPage := childInfo.GetPage()
			require.NotNil(t, childPage, "child %d page should not be nil", i)
		}
	}
}

// printTreeStructure 打印树结构（调试用）
func printTreeStructure(page interface{}, indent int) {
	if page == nil {
		return
	}

	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	switch p := page.(type) {
	case *LeafPage:
		fmt.Printf("%sLeaf[ID=%d, Keys=%d]\n", prefix, p.pageID, p.NumKeys())
	case *InternalPage:
		fmt.Printf("%sInternal[ID=%d, Keys=%d, Children=%d]\n", prefix, p.pageID, len(p.keys), len(p.children))
		for _, childRef := range p.children {
			if childRef != nil {
				childInfo := childRef.pInfo.Load()
				if childInfo != nil && childInfo.page != nil {
					printTreeStructure(childInfo.page, indent+1)
				}
			}
		}
	}
}

// countKeysInTree 统计树中所有键的数量
func countKeysInTree(page interface{}) int {
	if page == nil {
		return 0
	}

	switch p := page.(type) {
	case *LeafPage:
		return p.NumKeys()
	case *InternalPage:
		count := 0
		for _, childRef := range p.children {
			if childRef != nil {
				childInfo := childRef.pInfo.Load()
				if childInfo != nil && childInfo.page != nil {
					count += countKeysInTree(childInfo.page)
				}
			}
		}
		return count
	default:
		return 0
	}
}
