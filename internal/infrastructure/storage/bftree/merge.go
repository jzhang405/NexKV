// Package bftree 提供 Bf-Tree 的节点合并实现
package bftree

import (
	"bytes"
	"fmt"
	"sync/atomic"
)

// MaxSplitDepth 最大分裂递归深度
const MaxSplitDepth = 10 // 最大 10 层树高度

// tryMergeAfterDelete Delete 后检查并触发合并
//
// 根据设计决策 1：Delete 后立即检查
// - 获取当前节点和左右兄弟节点
// - 检查是否可以合并（利用率 < MergeThreshold）
// - 执行合并并更新父节点
//
// 参数：
//   - leafPageID: 删除操作所在的叶子页面 ID
//
// 返回：
//   - error: 错误
func (t *BfTree) tryMergeAfterDelete(leafPageID uint64) error {
	// 1. 获取当前节点
	leafNode, err := t.pageStore.getLeaf(leafPageID)
	if err != nil {
		return fmt.Errorf("failed to get leaf node: %w", err)
	}

	// 2. 获取当前节点利用率（不调用 compact，避免干扰 Delete Delta）
	utilization := t.calculateNodeUtilization(leafNode)

	// 4. 如果利用率高于阈值，不需要合并
	if utilization > t.config.MergeThreshold {
		return nil
	}

	// 5. 获取左右兄弟节点
	leftSibling, rightSibling, err := t.getSiblings(leafPageID)
	if err != nil {
		// 无兄弟节点，不合并
		return nil
	}

	// 6. 尝试合并叶子节点
	if leftSibling != nil && rightSibling != nil {
		// 检查三个节点是否可以合并
		if t.canMergeThreeLeafNodes(leftSibling, leafNode, rightSibling) {
			return t.mergeThreeLeafNodes(leftSibling, leafNode, rightSibling, leafPageID)
		}
	}

	// 7. 尝试两两合并
	if leftSibling != nil {
		if t.canMergeTwoLeafNodes(leftSibling, leafNode) {
			return t.mergeTwoLeafNodes(leftSibling, leafNode, leafPageID, true)
		}
	}

	if rightSibling != nil {
		if t.canMergeTwoLeafNodes(leafNode, rightSibling) {
			return t.mergeTwoLeafNodes(leafNode, rightSibling, leafPageID, false)
		}
	}

	return nil
}

// getSiblings 获取左右兄弟节点
//
// 返回：
//   - leftSibling: 左兄弟节点（可能为 nil）
//   - rightSibling: 右兄弟节点（可能为 nil）
//   - error: 错误
func (t *BfTree) getSiblings(pageID uint64) (*LeafNode, *LeafNode, error) {
	// MVP 实现：简化版，仅返回 nil
	// Phase 2.3 完整版：需要遍历父节点找到兄弟节点

	// 如果没有父节点（根节点），无兄弟
	if t.rootPageID == 0 || t.rootPageID == pageID {
		return nil, nil, nil
	}

	// TODO: 实现完整版本
	// 1. 找到父节点
	// 2. 在父节点的 children 数组中找到当前节点的索引
	// 3. 获取左右兄弟节点

	return nil, nil, nil
}

// calculateNodeUtilization 计算节点利用率
//
// 返回：
//   - utilization: 利用率 (0.0 - 1.0)
func (t *BfTree) calculateNodeUtilization(node *LeafNode) float32 {
	nodeSize := node.Size()
	pageSize := uint64(node.level.PageSize())

	if pageSize == 0 {
		return 0
	}

	return float32(nodeSize) / float32(pageSize)
}

// canMergeThreeLeafNodes 检查三个叶子节点是否可以合并
//
// 合并条件：
// - 三个节点的总大小 < 一页大小
//
// 返回：
//   - canMerge: 是否可以合并
func (t *BfTree) canMergeThreeLeafNodes(left, node, right *LeafNode) bool {
	totalSize := left.Size() + node.Size() + right.Size()
	pageSize := uint64(node.level.PageSize())

	return totalSize < pageSize
}

// canMergeTwoLeafNodes 检查两个叶子节点是否可以合并
//
// 合并条件：
// - 两个节点的总大小 < 一页大小 * MergeThreshold
//
// 返回：
//   - canMerge: 是否可以合并
func (t *BfTree) canMergeTwoLeafNodes(node1, node2 *LeafNode) bool {
	totalSize := node1.Size() + node2.Size()
	pageSize := uint64(node1.level.PageSize())

	// 使用 80% 作为安全阈值，留一些空间
	return totalSize < uint64(float32(pageSize)*0.8)
}

// mergeThreeLeafNodes 合并三个叶子节点到中间节点
//
// 根据设计决策 2：优先合并
// - Compact 所有节点的 Delta 到 Mini-Page
// - 合并所有键值对到中间节点
// - 更新父节点指针
// - 释放左右节点
//
// 参数：
//   - left: 左节点
//   - node: 中间节点（合并目标）
//   - right: 右节点
//   - nodePageID: 中间节点的页面 ID
//
// 返回：
//   - error: 错误
func (t *BfTree) mergeThreeLeafNodes(left, node, right *LeafNode, nodePageID uint64) error {
	// 1. Compact 所有节点
	if err := left.compact(); err != nil {
		return fmt.Errorf("failed to compact left node: %w", err)
	}
	if err := node.compact(); err != nil {
		return fmt.Errorf("failed to compact middle node: %w", err)
	}
	if err := right.compact(); err != nil {
		return fmt.Errorf("failed to compact right node: %w", err)
	}

	// 2. 收集所有键值对
	var allPairs []Slot
	allPairs = append(allPairs, collectAllSlots(left.miniPage)...)
	allPairs = append(allPairs, collectAllSlots(node.miniPage)...)
	allPairs = append(allPairs, collectAllSlots(right.miniPage)...)

	// 3. 清空中间节点的 Mini-Page
	newMiniPage := NewMiniPage(node.level)

	// 4. 将所有键值对插入到中间节点
	for _, pair := range allPairs {
		keyStr := string(pair.key)
		if _, exists := newMiniPage.slotMap[keyStr]; !exists {
			newMiniPage.slots = append(newMiniPage.slots, pair)
			newMiniPage.slotMap[keyStr] = len(newMiniPage.slots) - 1
			newMiniPage.dataSize += uint16(len(pair.key) + len(pair.value))
		}
	}

	// 5. 替换中间节点的 Mini-Page
	node.miniPage = newMiniPage
	node.deltas = make([]*DeltaEntry, 0, 8)
	node.deltaSize = 0

	// 6. 存储更新后的中间节点
	t.pageStore.putLeaf(nodePageID, node)

	// 7. 释放左右节点
	_ = t.pageTable.Free(left.pageID)
	_ = t.pageTable.Free(right.pageID)

	// 8. 更新父节点
	// TODO: 需要实现父节点更新逻辑
	// Phase 2.3: 多级树需要更新父节点的子节点指针

	// 9. 更新统计
	atomic.AddInt64(&t.stats.LeafPages, -2)

	return nil
}

// mergeTwoLeafNodes 合并两个叶子节点
//
// 参数：
//   - node1: 第一个节点
//   - node2: 第二个节点
//   - targetPageID: 目标节点页面 ID（保留的节点）
//   - keepFirst: 是否保留第一个节点（true: 保留 node1，false: 保留 node2）
//
// 返回：
//   - error: 错误
func (t *BfTree) mergeTwoLeafNodes(node1, node2 *LeafNode, targetPageID uint64, keepFirst bool) error {
	// 1. Compact 两个节点
	if err := node1.compact(); err != nil {
		return fmt.Errorf("failed to compact first node: %w", err)
	}
	if err := node2.compact(); err != nil {
		return fmt.Errorf("failed to compact second node: %w", err)
	}

	// 2. 确定保留哪个节点
	var targetNode, sourceNode *LeafNode
	var targetPageIDToKeep, sourcePageIDToFree uint64

	if keepFirst {
		targetNode = node1
		sourceNode = node2
		targetPageIDToKeep = node1.pageID
		sourcePageIDToFree = node2.pageID
	} else {
		targetNode = node2
		sourceNode = node1
		targetPageIDToKeep = node2.pageID
		sourcePageIDToFree = node1.pageID
	}

	// 3. 收集所有键值对
	var allPairs []Slot
	allPairs = append(allPairs, collectAllSlots(targetNode.miniPage)...)
	allPairs = append(allPairs, collectAllSlots(sourceNode.miniPage)...)

	// 4. 创建新的 Mini-Page
	newMiniPage := NewMiniPage(targetNode.level)

	// 5. 将所有键值对插入（去重）
	for _, pair := range allPairs {
		keyStr := string(pair.key)
		if _, exists := newMiniPage.slotMap[keyStr]; !exists {
			newMiniPage.slots = append(newMiniPage.slots, pair)
			newMiniPage.slotMap[keyStr] = len(newMiniPage.slots) - 1
			newMiniPage.dataSize += uint16(len(pair.key) + len(pair.value))
		}
	}

	// 6. 替换目标节点的 Mini-Page
	targetNode.miniPage = newMiniPage
	targetNode.deltas = make([]*DeltaEntry, 0, 8)
	targetNode.deltaSize = 0

	// 7. 存储更新后的目标节点
	t.pageStore.putLeaf(targetPageIDToKeep, targetNode)

	// 8. 释放源节点
	_ = t.pageTable.Free(sourcePageIDToFree)

	// 9. 更新父节点
	// TODO: 需要实现父节点更新逻辑

	// 10. 更新统计
	atomic.AddInt64(&t.stats.LeafPages, -1)

	return nil
}

// tryMergeInnerNode 尝试合并内部节点
//
// 参数：
//   - innerPageID: 内部节点页面 ID
//
// 返回：
//   - error: 错误
//
//nolint:unused // Phase 2.3: 完整合并逻辑时使用
func (t *BfTree) tryMergeInnerNode(innerPageID uint64) error {
	// MVP 实现：暂不支持内部节点合并
	// Phase 2.3: 如果需要，可以补充实现

	return nil
}

// canMergeInnerNodes 判断两个内部节点是否可以合并
//
// 参数：
//   - node1: 第一个内部节点
//   - node2: 第二个内部节点
//
// 返回：
//   - canMerge: 是否可以合并
func (t *BfTree) canMergeInnerNodes(node1, node2 *InnerNode) bool {
	totalChildren := len(node1.children) + len(node2.children)
	maxChildren := node1.maxKeys

	return totalChildren < maxChildren
}

// mergeInnerNodes 合并两个内部节点
//
// 参数：
//   - node1: 第一个内部节点
//   - node2: 第二个内部节点
//   - separator: 分隔键（提升到父节点的键）
//
// 返回：
//   - error: 错误
func (t *BfTree) mergeInnerNodes(node1, node2 *InnerNode, separator []byte) error {
	// 1. 合并子节点
	mergedChildren := make([]uint64, 0, len(node1.children)+len(node2.children))
	mergedChildren = append(mergedChildren, node1.children...)
	mergedChildren = append(mergedChildren, node2.children...)

	// 2. 合并键（包括分隔键）
	mergedKeys := make([][]byte, 0, len(node1.keys)+1+len(node2.keys))
	mergedKeys = append(mergedKeys, node1.keys...)
	mergedKeys = append(mergedKeys, separator)
	mergedKeys = append(mergedKeys, node2.keys...)

	// 3. 更新第一个节点
	node1.children = mergedChildren
	node1.keys = mergedKeys
	node1.version++

	// 4. 释放第二个节点
	_ = t.pageTable.Free(node2.pageID)

	// 5. 更新统计
	atomic.AddInt64(&t.stats.InnerPages, -1)

	return nil
}

// findParent 查找父节点
//
// 从根节点开始遍历，找到包含 childPageID 的父节点
//
// 参数：
//   - childPageID: 子节点页面 ID
//
// 返回：
//   - parentPageID: 父节点页面 ID（0 表示无父节点）
//   - error: 错误
func (t *BfTree) findParent(childPageID uint64) (uint64, error) {
	// 如果是根节点，无父节点
	if t.rootPageID == childPageID || t.rootPageID == 0 {
		return 0, nil
	}

	// 从根节点开始 BFS 遍历
	return t.findParentBFS(t.rootPageID, childPageID)
}

// findParentBFS 使用 BFS 查找父节点
//
// 参数：
//   - currentPageID: 当前搜索的页面 ID
//   - childPageID: 要查找的子节点页面 ID
//
// 返回：
//   - parentPageID: 父节点页面 ID（0 表示未找到）
//   - error: 错误
func (t *BfTree) findParentBFS(currentPageID, childPageID uint64) (uint64, error) {
	entry, found := t.pageTable.Get(currentPageID)
	if !found {
		return 0, fmt.Errorf("page not found: %d", currentPageID)
	}

	if entry.pageType == PageTypeInner {
		innerNode, err := t.pageStore.getInner(currentPageID)
		if err != nil {
			return 0, fmt.Errorf("failed to get inner node: %w", err)
		}

		// 检查当前节点的 children 是否包含 childPageID
		for _, childID := range innerNode.children {
			if childID == childPageID {
				// 找到了！当前节点就是父节点
				return currentPageID, nil
			}
		}

		// 递归搜索所有子节点
		for _, childID := range innerNode.children {
			childEntry, found := t.pageTable.Get(childID)
			if !found {
				continue
			}

			// 只有内部节点才需要递归搜索（叶子节点不会有子节点）
			if childEntry.pageType == PageTypeInner {
				parentID, err := t.findParentBFS(childID, childPageID)
				if err != nil {
					return 0, err
				}
				if parentID != 0 {
					return parentID, nil
				}
			}
		}
	}

	// 未找到
	return 0, nil
}

// insertSplitWithDepth 带深度限制的递归分裂
//
// 根据设计决策 3：添加最大深度限制
// - 递归深度不超过 MaxSplitDepth
// - 防止无限递归
//
// 参数：
//   - parentID: 父节点页面 ID
//   - leftPageID: 左子节点页面 ID
//   - rightPageID: 右子节点页面 ID
//   - splitKey: 分隔键
//   - depth: 当前递归深度
//
// 返回：
//   - error: 错误
//
//nolint:unused // Phase 2.3: 多级分裂时使用
func (t *BfTree) insertSplitWithDepth(parentID, leftPageID, rightPageID uint64, splitKey []byte, depth int) error {
	// 1. 递归深度检查
	if depth > MaxSplitDepth {
		return ErrTreeTooDeep
	}

	// 2. 如果是根节点分裂
	if parentID == 0 || t.rootPageID == 0 {
		return t.createNewRoot(leftPageID, rightPageID, splitKey)
	}

	// 3. 获取父节点
	parentNode, err := t.pageStore.getInner(parentID)
	if err != nil {
		return fmt.Errorf("failed to get parent node: %w", err)
	}

	// 4. 尝试插入分隔键和子节点
	if !parentNode.IsFull() {
		// 找到插入位置
		insertIndex := 0
		for i, key := range parentNode.keys {
			if bytes.Compare(splitKey, key) < 0 {
				insertIndex = i
				break
			}
			insertIndex = i + 1
		}

		// 插入子节点和分隔键
		if err := parentNode.InsertChild(insertIndex, splitKey, rightPageID); err != nil {
			return fmt.Errorf("failed to insert child to parent: %w", err)
		}

		// 存储更新后的父节点
		t.pageStore.putInner(parentID, parentNode)
		return nil
	}

	// 5. 父节点也满了，需要分裂
	newLeft, newRight, newSplitKey, splitErr := t.splitInnerNode(parentID)
	if splitErr != nil {
		return fmt.Errorf("failed to split parent node: %w", splitErr)
	}

	// 6. 释放旧父节点
	oldParentID := parentID
	_ = t.pageTable.Free(oldParentID)

	// 7. 递归向上
	grandParentID, err := t.findParent(parentID)
	if err != nil {
		return fmt.Errorf("failed to find grandparent: %w", err)
	}

	if grandParentID == 0 || grandParentID == parentID {
		// 到达根节点，创建新根
		return t.createNewRoot(newLeft, newRight, newSplitKey)
	}

	return t.insertSplitWithDepth(grandParentID, newLeft, newRight, newSplitKey, depth+1)
}

// updateParentAfterMerge 合并后更新父节点
//
// 参数：
//   - childPageID: 子节点页面 ID
//   - mergedPageID: 合并后的节点页面 ID
//
// 返回：
//   - error: 错误
//
//nolint:unused // Phase 2.3: 完整合并逻辑时使用
func (t *BfTree) updateParentAfterMerge(childPageID, mergedPageID uint64) error {
	// MVP 实现：简化版
	// Phase 2.3 完整版：需要更新父节点的子节点指针

	// TODO: 实现完整版本
	// 1. 找到父节点
	// 2. 更新父节点的 children 数组
	// 3. 删除对应的分隔键

	return nil
}
