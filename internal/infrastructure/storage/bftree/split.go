// Package bftree 提供 Bf-Tree 的节点分裂实现
package bftree

import (
	"fmt"
	"sync/atomic"
)

// splitLeafNode 分裂叶子节点
//
// 分裂步骤：
// 1. 分配新页面（新节点）
// 2. 将所有键值对 compact 到临时 Mini-Page
// 3. 找到中间键作为分隔键
// 4. 将键值对分配到左右两个节点
// 5. 如果是根节点，创建新根节点并提升高度
// 6. 否则，将分隔键插入父节点（MVP：仅支持根节点分裂）
//
// 注意：旧节点不会自动释放，调用者负责在成功后释放
//
// 返回：
//   - leftPageID: 左节点页面 ID
//   - rightPageID: 右节点页面 ID
//   - splitKey: 分隔键（提升到父节点）
//   - oldPageID: 旧节点页面 ID（需要调用者释放）
//   - error: 错误
func (t *BfTree) splitLeafNode(pageID uint64) (leftPageID, rightPageID uint64, splitKey []byte, oldPageID uint64, err error) {
	// 1. 获取要分裂的节点
	leafNode, err := t.pageStore.getLeaf(pageID)
	if err != nil {
		return 0, 0, nil, 0, fmt.Errorf("failed to get leaf node: %w", err)
	}

	// 2. 先 compact 所有 Delta 到 Mini-Page
	if err := leafNode.compact(); err != nil {
		return 0, 0, nil, 0, fmt.Errorf("failed to compact before split: %w", err)
	}

	// 3. 收集所有键值对
	allPairs := collectAllSlots(leafNode.miniPage)
	if len(allPairs) == 0 {
		return 0, 0, nil, 0, fmt.Errorf("cannot split empty node")
	}

	// 4. 找到中间位置
	midIndex := len(allPairs) / 2
	// 深拷贝分隔键，防止后续修改
	splitKey = make([]byte, len(allPairs[midIndex].key))
	copy(splitKey, allPairs[midIndex].key)

	// 5. 创建左右两个新节点
	leftLevel := leafNode.level
	leftPageID, err = t.pageTable.Alloc(PageTypeLeaf, leftLevel)
	if err != nil {
		return 0, 0, nil, 0, fmt.Errorf("failed to allocate left page: %w", err)
	}

	rightPageID, err = t.pageTable.Alloc(PageTypeLeaf, leftLevel)
	if err != nil {
		// 回滚左节点分配
		_ = t.pageTable.Free(leftPageID)
		return 0, 0, nil, 0, fmt.Errorf("failed to allocate right page: %w", err)
	}

	// 6. 创建左右节点并分配键值对
	leftNode := NewLeafNode(leftPageID, leftLevel, t.config.MaxDeltaChainLen, t.config.MaxDeltaChainSize)
	rightNode := NewLeafNode(rightPageID, leftLevel, t.config.MaxDeltaChainLen, t.config.MaxDeltaChainSize)

	// 左节点：[0, midIndex)
	for i := 0; i < midIndex; i++ {
		pair := allPairs[i]
		if err := leftNode.Set(pair.key, pair.value); err != nil {
			// 回滚
			_ = t.pageTable.Free(leftPageID)
			_ = t.pageTable.Free(rightPageID)
			return 0, 0, nil, 0, fmt.Errorf("failed to insert to left node: %w", err)
		}
	}

	// 右节点：[midIndex, len)
	for i := midIndex; i < len(allPairs); i++ {
		pair := allPairs[i]
		if err := rightNode.Set(pair.key, pair.value); err != nil {
			// 回滚
			_ = t.pageTable.Free(leftPageID)
			_ = t.pageTable.Free(rightPageID)
			return 0, 0, nil, 0, fmt.Errorf("failed to insert to right node: %w", err)
		}
	}

	// 7. 存储新节点
	t.pageStore.putLeaf(leftPageID, leftNode)
	t.pageStore.putLeaf(rightPageID, rightNode)

	// 8. 返回旧节点 ID，调用者负责释放
	// 注意：不在这里释放，确保父节点更新成功后再释放
	oldPageID = pageID

	// 9. 更新统计
	atomic.AddInt64(&t.stats.LeafPages, 1)

	return leftPageID, rightPageID, splitKey, oldPageID, nil
}

// splitInnerNode 分裂内部节点
//
// 分裂步骤：
// 1. 分配新页面
// 2. 找到中间子节点作为分隔点
// 3. 将子节点分配到左右两个节点
// 4. 提升分隔键到父节点
// 5. 如果是根节点，创建新根节点并提升高度
//
// 返回：
//   - leftPageID: 左节点页面 ID
//   - rightPageID: 右节点页面 ID
//   - splitKey: 分隔键（提升到父节点）
//   - error: 错误
//
//nolint:unused // Phase 2.3: 多级分裂时使用
func (t *BfTree) splitInnerNode(pageID uint64) (leftPageID, rightPageID uint64, splitKey []byte, err error) {
	// 1. 获取要分裂的节点
	innerNode, err := t.pageStore.getInner(pageID)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("failed to get inner node: %w", err)
	}

	// 2. 收集所有子节点
	children := innerNode.children
	keys := innerNode.keys

	if len(children) == 0 {
		return 0, 0, nil, fmt.Errorf("cannot split empty inner node")
	}

	// 3. 找到中间位置
	midIndex := len(children) / 2
	splitKey = keys[midIndex-1] // 分隔键是中间位置前的键

	// 4. 创建左右两个新节点
	leftPageID, err = t.pageTable.Alloc(PageTypeInner, L1) // InnerNode 使用 L1
	if err != nil {
		return 0, 0, nil, fmt.Errorf("failed to allocate left page: %w", err)
	}

	rightPageID, err = t.pageTable.Alloc(PageTypeInner, L1)
	if err != nil {
		// 回滚
		_ = t.pageTable.Free(leftPageID)
		return 0, 0, nil, fmt.Errorf("failed to allocate right page: %w", err)
	}

	// 5. 创建左右节点并分配子节点
	leftNode := NewInnerNode(leftPageID, L1)
	rightNode := NewInnerNode(rightPageID, L1)

	// 左节点：[0, midIndex)
	leftNode.children = make([]uint64, midIndex)
	copy(leftNode.children, children[:midIndex])
	if midIndex > 1 {
		leftNode.keys = make([][]byte, midIndex-1)
		for i := 0; i < midIndex-1; i++ {
			leftNode.keys[i] = keys[i]
		}
	}

	// 右节点：[midIndex, len)
	rightNode.children = make([]uint64, len(children)-midIndex)
	copy(rightNode.children, children[midIndex:])
	if len(children)-midIndex > 1 {
		rightNode.keys = make([][]byte, len(children)-midIndex-1)
		for i := midIndex; i < len(children)-1; i++ {
			rightNode.keys[i-midIndex] = keys[i]
		}
	}

	// 6. 存储新节点
	t.pageStore.putInner(leftPageID, leftNode)
	t.pageStore.putInner(rightPageID, rightNode)

	// 7. 释放旧节点（延迟到父节点更新后）
	_ = t.pageTable.Free(pageID)

	// 8. 更新统计
	atomic.AddInt64(&t.stats.InnerPages, 1)

	return leftPageID, rightPageID, splitKey, nil
}

// insertSplitIntoParent 将分裂结果插入父节点
//
// 支持多级分裂：
// - 如果 parentPageID == 0，创建新根节点
// - 否则，将分隔键和右子节点插入父节点
// - 如果父节点也满了，递归分裂父节点
//
//nolint:unused // Phase 2.3: 多级分裂时使用
func (t *BfTree) insertSplitIntoParent(parentPageID, leftPageID, rightPageID uint64, splitKey []byte) error {
	// 1. 如果是根节点分裂（parentPageID == 0）
	if parentPageID == 0 || t.rootPageID == 0 {
		return t.createNewRoot(leftPageID, rightPageID, splitKey)
	}

	// 2. 获取父节点
	parentNode, err := t.pageStore.getInner(parentPageID)
	if err != nil {
		return fmt.Errorf("failed to get parent node: %w", err)
	}

	// 3. 检查父节点是否已满
	if parentNode.IsFull() {
		// 父节点已满，需要分裂
		return t.splitParentAndInsert(parentPageID, leftPageID, rightPageID, splitKey)
	}

	// 4. 父节点未满，直接插入
	// 找到 leftPageID 在父节点中的位置
	insertIndex := -1
	for i, childID := range parentNode.children {
		if childID == leftPageID {
			insertIndex = i
			break
		}
	}

	if insertIndex == -1 {
		return fmt.Errorf("leftPageID %d not found in parent node %d", leftPageID, parentPageID)
	}

	// 5. 插入分隔键和右子节点
	// 在 insertIndex 位置后插入 splitKey 和 rightPageID
	if err := parentNode.InsertChild(insertIndex, splitKey, rightPageID); err != nil {
		return fmt.Errorf("failed to insert child to parent: %w", err)
	}

	// 6. 存储更新后的父节点
	t.pageStore.putInner(parentPageID, parentNode)

	return nil
}

// splitParentAndInsert 分裂父节点并插入新子节点
//
// 当父节点已满时：
// 1. 分裂父节点
// 2. 提升分隔键到祖父节点
// 3. 如果祖父节点也满，递归分裂
//
// 参数：
//   - parentPageID: 父节点页面 ID
//   - leftPageID: 左子节点页面 ID
//   - rightPageID: 右子节点页面 ID
//   - splitKey: 分隔键
//
// 返回：
//   - error: 错误
func (t *BfTree) splitParentAndInsert(parentPageID, leftPageID, rightPageID uint64, splitKey []byte) error {
	// 1. 先从父节点中移除 leftPageID
	parentNode, err := t.pageStore.getInner(parentPageID)
	if err != nil {
		return fmt.Errorf("failed to get parent node: %w", err)
	}

	// 找到 leftPageID 的位置
	leftIndex := -1
	for i, childID := range parentNode.children {
		if childID == leftPageID {
			leftIndex = i
			break
		}
	}

	if leftIndex == -1 {
		return fmt.Errorf("leftPageID %d not found in parent node", leftPageID)
	}

	// 2. 分裂父节点
	newLeftPageID, newRightPageID, parentSplitKey, splitErr := t.splitInnerNode(parentPageID)
	if splitErr != nil {
		return fmt.Errorf("failed to split parent node: %w", splitErr)
	}

	// 3. 找到祖父节点
	grandParentID, err := t.findParent(parentPageID)
	if err != nil {
		return fmt.Errorf("failed to find grandparent: %w", err)
	}

	// 4. 递归插入分裂结果到祖父节点
	if grandParentID == 0 || grandParentID == parentPageID {
		// 到达根节点，创建新根
		return t.createNewRoot(newLeftPageID, newRightPageID, parentSplitKey)
	}

	// 5. 将 splitKey 和 rightPageID 插入到正确的父节点中
	// 需要确定 leftPageID 现在是在 newLeftPageID 还是 newRightPageID 中
	newLeftNode, err := t.pageStore.getInner(newLeftPageID)
	if err != nil {
		return fmt.Errorf("failed to get new left node: %w", err)
	}

	// 检查 leftPageID 是否在 newLeftNode 中
	foundInLeft := false
	for _, childID := range newLeftNode.children {
		if childID == leftPageID {
			foundInLeft = true
			break
		}
	}

	var targetParentID uint64
	var insertIndex int

	if foundInLeft {
		// leftPageID 在 newLeftNode 中，插入到 newLeftNode
		targetParentID = newLeftPageID
		// 找到 leftPageID 的位置
		for i, childID := range newLeftNode.children {
			if childID == leftPageID {
				insertIndex = i
				break
			}
		}
	} else {
		// leftPageID 在 newRightNode 中，插入到 newRightNode
		targetParentID = newRightPageID
		newRightNode, err := t.pageStore.getInner(newRightPageID)
		if err != nil {
			return fmt.Errorf("failed to get new right node: %w", err)
		}
		// 找到 leftPageID 的位置
		for i, childID := range newRightNode.children {
			if childID == leftPageID {
				insertIndex = i
				break
			}
		}
	}

	// 6. 插入 splitKey 和 rightPageID 到目标父节点
	targetNode, err := t.pageStore.getInner(targetParentID)
	if err != nil {
		return fmt.Errorf("failed to get target parent node: %w", err)
	}

	if err := targetNode.InsertChild(insertIndex, splitKey, rightPageID); err != nil {
		return fmt.Errorf("failed to insert child: %w", err)
	}

	t.pageStore.putInner(targetParentID, targetNode)

	// 7. 将父节点的分裂结果插入到祖父节点
	return t.insertSplitIntoParent(grandParentID, newLeftPageID, newRightPageID, parentSplitKey)
}

// createNewRoot 创建新的根节点（树高度 +1）
func (t *BfTree) createNewRoot(leftPageID, rightPageID uint64, splitKey []byte) error {
	// 1. 分配新根节点页面
	newRootID, err := t.pageTable.Alloc(PageTypeInner, L1)
	if err != nil {
		return fmt.Errorf("failed to allocate new root page: %w", err)
	}

	// 2. 创建新的 InnerNode 作为根节点
	newRoot := NewInnerNode(newRootID, L1)
	newRoot.children = []uint64{leftPageID, rightPageID}
	newRoot.keys = [][]byte{splitKey}

	// 3. 存储新根节点
	t.pageStore.putInner(newRootID, newRoot)

	// 4. 更新根节点指针
	t.rootPageID = newRootID

	// 5. 更新统计
	atomic.AddInt64(&t.stats.InnerPages, 1)

	return nil
}

// collectAllSlots 收集 Mini-Page 中的所有键值对
func collectAllSlots(mp *MiniPage) []Slot {
	slots := make([]Slot, 0, len(mp.slots))
	for _, slot := range mp.slots {
		// 深拷贝键值
		keyCopy := make([]byte, len(slot.key))
		copy(keyCopy, slot.key)

		valueCopy := make([]byte, len(slot.value))
		copy(valueCopy, slot.value)

		slots = append(slots, Slot{
			key:   keyCopy,
			value: valueCopy,
		})
	}
	return slots
}

// insertKeyAtIndex 在指定位置插入键
//
//nolint:unused // Phase 2.3: 多级分裂时使用
func insertKeyAtIndex(keys [][]byte, key []byte, index int) [][]byte {
	// 扩展切片
	newKeys := make([][]byte, len(keys)+1)
	copy(newKeys, keys[:index])
	newKeys[index] = key
	copy(newKeys[index+1:], keys[index:])
	return newKeys
}

// insertChildAtIndex 在指定位置插入子节点
//
//nolint:unused // Phase 2.3: 多级分裂时使用
func insertChildAtIndex(children []uint64, childID uint64, index int) []uint64 {
	// 扩展切片
	newChildren := make([]uint64, len(children)+1)
	copy(newChildren, children[:index])
	newChildren[index] = childID
	copy(newChildren[index+1:], children[index:])
	return newChildren
}

// maxChildrenForInnerNode 内部节点的最大子节点数
//
//nolint:unused // Phase 2.3: 多级分裂时使用
func maxChildrenForInnerNode() int {
	// 假设页面大小 4KB，每个子节点指针 8 字节 + 键平均 16 字节
	// 约可支持 128 个子节点
	return 128
}

// compareKeys 比较两个键
// 返回值：-1 (k1 < k2), 0 (k1 == k2), 1 (k1 > k2)
func compareKeys(k1, k2 []byte) int {
	minLen := len(k1)
	if len(k2) < minLen {
		minLen = len(k2)
	}

	for i := 0; i < minLen; i++ {
		if k1[i] < k2[i] {
			return -1
		}
		if k1[i] > k2[i] {
			return 1
		}
	}

	if len(k1) < len(k2) {
		return -1
	}
	if len(k1) > len(k2) {
		return 1
	}
	return 0
}
