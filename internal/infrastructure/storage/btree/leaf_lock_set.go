// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
)

// setWithLeafLock 实现 Leaf-Level Locking 写入路径（Off-Heap 模式）
//
// 核心流程：
// 1. findLeafPageRef：查找路径和 PageRef（只读，不克隆）
// 2. Leaf.Lock：获取叶子节点锁
// 3. OffHeap Insert：使用 OffHeapAdapter.InsertToOffHeap() 插入
// 4. Leaf CAS：原子替换叶子节点（如果 pageID 变化）
// 5. Leaf.Unlock：释放锁
// 6. 检查分裂：如果需要，调用分裂逻辑
//
// Off-Heap 变更：
// - 不再使用 Delta Chain（CloneWithDelta）
// - 直接使用 OffHeapAdapter.InsertToOffHeap()
// - pageID 可能变化（update 场景）
//
// 并发分裂协调：
// - 使用 splitMutexMap 防止多个 goroutine 同时分裂同一页面
// - 正在分裂的页面会标记为 splitting，其他 goroutine 等待分裂完成
func (b *BTree) setWithLeafLock(ctx context.Context, key, value []byte) error {
	// Step 1: 查找 PageRef 和路径（只读，不克隆）
	leafRef, path, err := b.findLeafPageRef(ctx, key)
	if err != nil {
		return fmt.Errorf("find leaf ref: %w", err)
	}

	if len(path) == 0 {
		return fmt.Errorf("empty path")
	}

	// Step 2: 获取锁（懒加载，每个 PageRef 有独立的锁）
	pageLock := leafRef.GetLock()
	if pageLock == nil {
		return fmt.Errorf("page lock is nil")
	}

	// 使用 TryLock 快速失败（避免死锁）
	if !pageLock.TryLock() {
		return ErrRetry // 快速失败，让外层重试
	}
	defer pageLock.Unlock()

	// Step 3: 获取当前 PageInfo（在锁保护下）
	oldInfo := leafRef.GetPageInfo()
	if oldInfo == nil {
		return fmt.Errorf("leaf page info is nil")
	}

	// Step 4: 验证页面已加载（Off-Heap 模式）
	if !oldInfo.IsPageLoaded() {
		return fmt.Errorf("leaf page not loaded")
	}

	// Step 5: Off-Heap 插入（直接修改，不需要克隆）
	oldPageID := model.PageID(oldInfo.GetPageID())
	newPageID, splitRequired, err := b.offheapAdapter.InsertToOffHeap(oldPageID, key, value)
	if err != nil {
		return fmt.Errorf("offheap insert: %w", err)
	}

	// Step 6: 创建新的 PageInfo（Off-Heap 模式）
	newInfo := NewPageInfo()
	newInfo.SetNodeRef(offheap.NewNodeRef(uint32(newPageID), true)) // true = isLeaf
	// 继承其他属性
	newInfo.SetPos(oldInfo.GetPos())
	if oldInfo.IsDirty() {
		newInfo.MarkDirty()
	}

	// Step 7: Leaf-Level CAS（在锁保护下，几乎不会失败）
	// 注意：如果 pageID 变化（update 场景），CAS 会失败，需要更新 PageRefCache
	if !leafRef.ReplacePage(oldInfo, newInfo) {
		// CAS 失败（极少发生），返回重试
		return ErrRetry
	}

	// Step 8: 如果 pageID 变化（update 场景），返回 ErrRetry
	// 因为 UpdateLeafEntry 重新分配了页面，分裂应该基于新页面而不是旧页面
	if newPageID != oldPageID {
		// update 场景：删除旧引用，添加新引用，并返回重试
		b.pageRefCache.Delete(oldPageID)
		b.pageRefCache.Update(newPageID, leafRef)
		// 返回 ErrRetry，让外层重新搜索路径并处理新页面的分裂
		return ErrRetry
	}

	// Step 9: 检查是否需要分裂（同步，在锁保护下）
	// 此时 newPageID == oldPageID，所以传递 newPageID 即可
	if splitRequired {
		// 获取页面级别的分裂锁，防止多个 goroutine 同时分裂同一页面
		splitMuAny, _ := b.splitMuMap.LoadOrStore(uint32(newPageID), &sync.Mutex{})
		splitMu := splitMuAny.(*sync.Mutex)
		splitMu.Lock()
		defer func() {
			splitMu.Unlock()
			b.splitMuMap.Delete(uint32(newPageID))
		}()

		// 需要分裂，调用 Off-Heap 分裂逻辑
		// 注意：分裂会释放当前锁，按深度顺序获取新的锁
		leftRef, err := b.handleSplitOffHeapSync(leafRef, newInfo, newPageID, path)
		if err != nil {
			// 分裂失败，返回 ErrRetry 让外层重试
			// 注意：如果 SplitOffHeapLeafPage 失败，页面可能已处于不一致状态
			return ErrRetry
		}
		// 分裂成功，leafRef 现在指向 leftPageID
		// 更新 newInfo 以便后续持久化使用正确的页面信息
		newInfo = leftRef.GetPageInfo()
		if newInfo == nil {
			return fmt.Errorf("leftRef page info is nil after split")
		}
	}

	// Step 10: 持久化集成（仅持久化模式）
	// Leaf-Level Locking 完成后，需要持久化整个树
	if b.chunkMgr != nil {
		// 获取全局写锁，防止并发修改干扰持久化
		b.writeMu.Lock()
		defer b.writeMu.Unlock()

		// 获取当前 Root（CAS 和分裂后可能已改变）
		currentRoot := b.rootRef.pInfo.Load()
		if currentRoot == nil {
			return fmt.Errorf("root page info is nil after persist")
		}

		// 构建持久化路径：从 Root 到 Leaf 的完整路径
		// Leaf-Level Locking 只克隆了叶子节点，需要收集完整路径进行深拷贝
		persistPath := b.buildPersistPath(currentRoot, newInfo)

		// 深拷贝路径（确保数据独立）
		if err := b.finalizeDeepClone(persistPath); err != nil {
			return fmt.Errorf("finalize deep clone: %w", err)
		}

		// 持久化根节点（会递归持久化整个树）
		if err := b.persistRoot(currentRoot); err != nil {
			return fmt.Errorf("persist root: %w", err)
		}
	}

	return nil
}

// buildPersistPath 从 Root 到目标 PageInfo 构建完整路径
// 用于持久化时确保整个修改路径被深拷贝
func (b *BTree) buildPersistPath(root, target *PageInfo) []*PageInfo {
	// BFS 搜索从 Root 到 target 的路径
	queue := []*PageInfo{root}
	visited := make(map[uint64]bool)
	visited[root.GetPageID()] = true
	parentMap := make(map[uint64]*PageInfo)
	parentMap[root.GetPageID()] = nil

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.GetPageID() == target.GetPageID() {
			// 找到目标，构建路径
			result := []*PageInfo{current}
			for p := parentMap[current.GetPageID()]; p != nil; p = parentMap[p.GetPageID()] {
				result = append([]*PageInfo{p}, result...)
			}
			return result
		}

		// 遍历子节点
		if internalPage, ok := current.GetPage().(*InternalPage); ok {
			for i := 0; i < internalPage.NumChildren(); i++ {
				childRef := internalPage.GetChild(i)
				if childRef == nil {
					continue
				}

				childInfo := childRef.GetPageInfo()
				if childInfo == nil {
					continue
				}

				childID := childInfo.GetPageID()
				if !visited[childID] {
					visited[childID] = true
					parentMap[childID] = current
					queue = append(queue, childInfo)
				}
			}
		}
	}

	// 未找到完整路径，至少返回 Root
	return []*PageInfo{root}
}

// handleSplitSync 处理叶子节点分裂（同步，带锁管理）
//
// 这是 Leaf-Level Locking 的关键部分：
// 1. 叶子节点已被 setWithLeafLock 锁定
// 2. 按深度顺序获取锁（leaf → parent → grandparent）
// 3. 使用 CAS 原子更新父节点
// 4. 避免直接修改父节点导致的并发读冲突
//
// 参数：
//
//	leafRef - 叶子节点的 PageRef（已锁定）
//	leafInfo - 叶子节点的 PageInfo（已 CAS 更新到新版本）
//	path - 搜索路径（用于向上传播分裂）
//
// 返回：error - 错误信息
func (b *BTree) handleSplitSync(leafRef *PageRef, leafInfo *PageInfo, path []*PageInfo) error {
	// 获取叶子页面（已在 setWithLeafLock 中 CAS 更新）
	leafPage := leafInfo.GetLeafPage()
	if leafPage == nil {
		return fmt.Errorf("leaf page not loaded")
	}

	// 检查是否需要分裂
	if leafPage.NumKeys() <= splitThreshold {
		return nil // 无需分裂
	}

	// 保存原始状态，用于 CAS 失败时恢复
	originalKeys := make([][]byte, len(leafPage.keys))
	copy(originalKeys, leafPage.keys)
	originalValues := make([][]byte, len(leafPage.values))
	copy(originalValues, leafPage.values)

	// 执行叶子分裂（注意：这会修改 leafPage）
	rightLeaf, splitKey, err := leafPage.Split()
	if err != nil {
		return fmt.Errorf("leaf split failed: %w", err)
	}

	// 恢复函数：如果 CAS 失败，恢复原始状态
	restoreIfNeeded := func(casErr error) error {
		if casErr == ErrRetry {
			// CAS 失败，恢复 leafPage 的原始状态
			leafPage.keys = originalKeys
			leafPage.values = originalValues
			// leafPage.version++ // 不需要递增版本，因为我们恢复到原始状态
		}
		return casErr
	}

	// 为新的右叶子节点分配 pageID
	rightLeaf.pageID = b.allocatePageID()

	// 创建右叶子节点的 PageInfo 和 PageRef
	rightLeafInfo := NewPageInfo()
	rightLeafInfo.SetPage(rightLeaf)
	rightLeafRef := NewPageRefWithInfo(rightLeafInfo)

	// 检查是否有父节点
	if len(path) < 2 {
		// 没有父节点，需要创建新的根节点
		// 注意：我们已经创建了 rightLeafInfo，但 splitRootSync 会重新处理
		err := restoreIfNeeded(b.splitRootSync(leafRef, rightLeafInfo, splitKey))
		return err
	}

	// 获取父节点的 PageRef（从 leafRef 的 parentRef 获取）
	parentRef := leafRef.GetParentRef()
	if parentRef == nil {
		// 降级方案：恢复 leafPage，然后使用现有的 splitLeaf 逻辑
		// splitLeaf 会再次执行 Split()，所以需要先恢复
		leafPage.keys = originalKeys
		leafPage.values = originalValues
		err := b.splitLeaf(leafInfo, splitKey, path)
		if err == ErrRetry {
			return ErrRetry
		}
		return err
	}

	// 获取父节点锁（自底向上加锁）
	parentLock := parentRef.GetLock()
	if parentLock == nil {
		return fmt.Errorf("parent lock is nil")
	}

	if !parentLock.TryLock() {
		// 锁获取失败，返回重试
		return ErrRetry
	}
	defer parentLock.Unlock()

	// 获取父节点的当前 PageInfo
	oldParentInfo := parentRef.GetPageInfo()
	if oldParentInfo == nil {
		return fmt.Errorf("parent page info is nil")
	}

	oldParentPage := oldParentInfo.GetPage()
	if oldParentPage == nil {
		return fmt.Errorf("parent page not loaded")
	}

	parentPage, ok := oldParentPage.(*InternalPage)
	if !ok || parentPage == nil {
		return fmt.Errorf("invalid parent page type: %T", oldParentPage)
	}

	// 克隆父节点（使用 CloneDeep 获得完全独立的副本）
	// 不能使用 Clone() 因为它共享 keys 数组，InsertKeyChild 会修改共享数组导致数据竞争
	newParentPage := parentPage.CloneDeep()

	// 在克隆的父节点中插入分裂键和新的右子节点
	// 注意：左子节点仍是 leafRef（已在 CAS 中更新）
	if err := newParentPage.InsertKeyChild(splitKey, rightLeafRef); err != nil {
		return fmt.Errorf("insert split key to parent: %w", err)
	}

	// 创建新的父节点 PageInfo
	newParentInfo := NewPageInfo()
	newParentInfo.SetPage(newParentPage)
	newParentInfo.SetPos(oldParentInfo.GetPos())

	// CAS 更新父节点
	if !parentRef.ReplacePage(oldParentInfo, newParentInfo) {
		// CAS 失败，恢复 leafPage 并返回重试
		return restoreIfNeeded(ErrRetry)
	}

	// 更新子节点的 parentRef
	leafRef.SetParentRef(parentRef)
	rightLeafRef.SetParentRef(parentRef)

	// 检查父节点是否需要分裂
	if newParentPage.NumKeys() > maxInternalKeys {
		// 父节点也需要分裂，使用现有的 splitLeaf 逻辑作为降级方案
		// 注意：这会退化为 Root CAS 路径，但只在极少数情况下触发
		return b.splitLeaf(leafInfo, splitKey, path)
	}

	return nil
}

// splitRootSync 处理根节点分裂（同步）
// 当叶子节点没有父节点时，创建新的内部节点作为根
func (b *BTree) splitRootSync(leftLeafRef *PageRef, rightLeafInfo *PageInfo, splitKey []byte) error {
	// 获取左叶子节点的 PageInfo
	leftLeafInfo := leftLeafRef.GetPageInfo()
	if leftLeafInfo == nil {
		return fmt.Errorf("left leaf info is nil")
	}

	// 创建新的内部节点作为根
	newRootPage := NewInternalPage(b.allocatePageID())
	newRootPage.keys = [][]byte{splitKey}

	// 创建右子节点的 PageRef
	rightRef := NewPageRefWithInfo(rightLeafInfo)

	// 创建左子节点的 PageRef（必须创建新的 PageRef，避免循环引用）
	// 如果 leftLeafRef 是 root，直接使用会导致 children[0] 指向 root，形成循环
	leftRef := NewPageRefWithInfo(leftLeafInfo)

	// 设置 children 数组
	newRootPage.children = []*PageRef{leftRef, rightRef}

	// 创建新的 Root PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetPage(newRootPage)
	newRootInfo.SetParentRef(nil) // 根节点没有父引用

	// CAS 更新根节点
	oldRootInfo := b.rootRef.pInfo.Load()
	oldRootID := uint64(0)
	if oldRootInfo != nil {
		oldRootID = oldRootInfo.GetPageID()
	}

	if !b.rootRef.ReplacePage(oldRootID, newRootInfo) {
		// CAS 失败，返回重试
		return ErrRetry
	}

	// 更新子节点的 parentRef
	leftRef.SetParentRef(b.rootRef.PageRef)
	rightRef.SetParentRef(b.rootRef.PageRef)

	return nil
}

// handleSplitOffHeapSync 处理叶子节点分裂（Off-Heap 模式，同步，带锁管理）
//
// 参数：
//
//	leafRef - 叶子节点的 PageRef
//	leafInfo - 叶子节点的 PageInfo
//	leafPageID - 叶子节点的 PageID
//	path - 从 Root 到 Leaf 的路径
//
// 返回：
//
//	*PageRef - 分裂后的左子节点 PageRef（用于后续持久化）
//	error - 错误信息（ErrRetry 表示需要重试）
func (b *BTree) handleSplitOffHeapSync(leafRef *PageRef, leafInfo *PageInfo, leafPageID model.PageID, path []*PageInfo) (*PageRef, error) {
	// 调试：记录页面 ID 和 leafRef 状态
	count := b.offheapAdapter.pa.GetCount(uint32(leafPageID))

	// 注意：这里 leafPageID 是旧页面 ID，leafInfo 指向新页面
	// 但当页面已满时，InsertToOffHeap 返回 splitRequired=true 但不插入数据
	// 所以我们需要从旧页面分裂，然后插入新数据

	// Step 1: 调用 OffHeapAdapter.SplitOffHeapLeafPage（从旧页面分裂）
	leftPageID, rightPageID, splitKey, err := b.offheapAdapter.SplitOffHeapLeafPage(leafPageID)
	if err != nil {
		// 分裂失败，返回 ErrRetry 让外层重试
		// 注意：如果 SplitOffHeapLeafPage 失败，页面可能已处于不一致状态
		return nil, ErrRetry
	}

	// Step 2: 创建左右子节点的 PageRef（更新 leafRef 指向左侧，创建新的右侧）
	leftRef := b.pageRefCache.GetOrCreate(leftPageID, true)
	rightRef := b.pageRefCache.GetOrCreate(rightPageID, true)

	// Step 3: 检查是否有父节点（根分裂场景需要特殊处理）
	if len(path) < 2 {
		// 没有父节点，需要创建新的根节点（Root Split）
		// 根分裂场景下，不要提前更新 leafRef，让 splitRootOffHeapSync 原子性地处理所有更新
		// 注意：此时 leafRef 就是 b.rootRef，不应该被提前 CAS
		err := b.splitRootOffHeapSync(leafRef, leafInfo, leftRef, rightRef, splitKey, leafPageID)
		if err != nil {
			// 如果根分裂失败，需要清理分配的页面
			b.offheapAdapter.pm.Free(uint32(leftPageID))
			b.offheapAdapter.pm.Free(uint32(rightPageID))
			return nil, err
		}
		// 根分裂成功，返回 leftRef
		return leftRef, nil
	}

	// 非根分裂场景：正常更新 leafRef
	// Step 4: 更新 leafRef 的 PageInfo（指向新的左页面）
	newLeftInfo := NewPageInfo()
	newLeftInfo.SetNodeRef(offheap.NewNodeRef(uint32(leftPageID), true))
	newLeftInfo.SetPos(leafInfo.GetPos())
	if leafInfo.IsDirty() {
		newLeftInfo.MarkDirty()
	}

	// 在锁保护下 CAS 更新 leafRef
	if !leafRef.ReplacePage(leafInfo, newLeftInfo) {
		// CAS 失败，返回重试
		return nil, ErrRetry
	}

	// CAS 成功后延迟释放旧页面（使用 epoch 避免立即重用）
	b.epochBasedFreeList.Add(leafPageID)

	// Step 5: 更新 PageRefCache
	// 旧的 leafPageID 现在指向 leftRef
	b.pageRefCache.Delete(leafPageID)
	b.pageRefCache.Update(leftPageID, leftRef)
	b.pageRefCache.Update(rightPageID, rightRef)

	// Step 6: 获取父节点的 PageRef
	parentInfo := path[len(path)-2]
	if parentInfo == nil {
		return nil, fmt.Errorf("parent info is nil")
	}

	oldParentPageID := model.PageID(parentInfo.GetPageID())

	// 重要：如果父节点是根节点，直接使用根的 PageRef
	// 否则 pageRefCache.GetOrCreate 会创建新的 PageRef，导致根节点和父节点不同步
	currentRootInfo := b.rootRef.pInfo.Load()
	parentRef := b.pageRefCache.GetOrCreate(oldParentPageID, false) // 默认值
	if currentRootInfo != nil && currentRootInfo.GetPageID() == uint64(oldParentPageID) {
		// 父节点就是根节点，使用根的 PageRef
		parentRef = b.rootRef.PageRef
	}

	// Step 7: 获取父节点锁（自底向上加锁）
	parentLock := parentRef.GetLock()
	if parentLock == nil {
		return nil, fmt.Errorf("parent lock is nil")
	}

	if !parentLock.TryLock() {
		// 锁获取失败，返回重试
		return nil, ErrRetry
	}
	defer parentLock.Unlock()

	// Step 8: 找到插入位置（二分查找）
	insertIndex := 0
	parentPageIDForSearch := model.PageID(parentInfo.GetPageID())
	count = b.offheapAdapter.pa.GetCount(uint32(parentPageIDForSearch))

	for i := 0; i < int(count); i++ {
		keyOff, keyLen, _ := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(parentPageIDForSearch), i)
		key := b.offheapAdapter.pa.GetKey(uint32(parentPageIDForSearch), keyOff, keyLen)
		if bytes.Compare(key, splitKey) >= 0 {
			insertIndex = i
			break
		}
		insertIndex = i + 1
	}


	// Step 9: 使用 UpdateIndexEntry 更新父节点（不可变方式）
	// 替换旧的子页面为新的左右子页面
	newParentPageID, err := b.offheapAdapter.UpdateIndexEntry(oldParentPageID, insertIndex, splitKey, uint32(leftPageID), uint32(rightPageID))
	if err != nil {
		return nil, fmt.Errorf("update parent index entry: %w", err)
	}

	// Step 10: 创建新的父节点 PageInfo
	newParentInfo := NewPageInfo()
	newParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(newParentPageID), false))
	newParentInfo.SetPos(parentInfo.GetPos())
	if parentInfo.IsDirty() {
		newParentInfo.MarkDirty()
	}

	// 调试：打印新父页面信息

	// Step 11: CAS 更新父节点
	if !parentRef.ReplacePage(parentInfo, newParentInfo) {
		// CAS 失败，释放新分配的父页面，返回重试
		b.offheapAdapter.pm.Free(uint32(newParentPageID))
		return nil, ErrRetry
	}

	// Step 11.5: 强制删除旧父页面的 PageRefCache 条目
	// 这确保任何持有旧页面 PageRef 的代码都会失效
	// 同时删除新页面ID，确保下次访问时重新创建 PageRef
	b.pageRefCache.Delete(oldParentPageID)
	b.pageRefCache.Delete(model.PageID(newParentPageID))

	// Step 12: CAS 成功后释放旧父页面
	b.offheapAdapter.pm.Free(uint32(oldParentPageID))

	// Step 13: 更新 PageRefCache
	b.pageRefCache.Delete(oldParentPageID)
	b.pageRefCache.Update(newParentPageID, parentRef)

	// Step 14: 更新子节点的 parentRef
	leftRef.SetParentRef(parentRef)
	rightRef.SetParentRef(parentRef)


	// Step 15: 检查父节点是否需要分裂（不仅是根节点）
	// 获取更新后的父节点信息
	currentParentInfo := parentRef.GetPageInfo()
	if currentParentInfo == nil {
		return nil, fmt.Errorf("parent info is nil after CAS")
	}
	currentParentPageID := model.PageID(currentParentInfo.GetPageID())
	currentParentCount := b.offheapAdapter.pa.GetCount(uint32(currentParentPageID))

	// 调试：打印父节点状态

	// 检查父节点是否需要分裂
	if int(currentParentCount) > maxInternalKeys {
		// 父节点需要分裂，递归处理
		err := b.splitInternalOffHeapSync(parentRef, currentParentInfo, currentParentPageID, path[:len(path)-1])
		if err != nil {
			return nil, err
		}
		// 父节点分裂成功，返回 ErrRetry 让外层重新搜索路径
		return nil, ErrRetry
	}

	// Step 16: 确保所有更新对其他 goroutine 可见
	// 添加内存屏障，建议调度器调度其他 goroutine
	runtime.Gosched()

	// 分裂成功，返回 leftRef 和 nil
	// 注意：数据已经在分裂前的页面中，分裂只是重新组织数据
	// 不需要返回 ErrRetry，因为外层已经完成插入
	return leftRef, nil
}

// splitRootOffHeapSync 处理根节点分裂（Off-Heap 模式，同步）
// 当叶子节点没有父节点时，创建新的内部节点作为根
func (b *BTree) splitRootOffHeapSync(oldLeafRef *PageRef, oldLeafInfo *PageInfo, leftRef, rightRef *PageRef, splitKey []byte, oldLeafPageID model.PageID) error {

	// Step 1: 分配新的根索引页面
	newRootPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		return fmt.Errorf("alloc index page: %w", err)
	}

	// Step 2: 物化根节点内容（使用 B+ 树的 N+1 child 语义）
	// 对于新根：keys = [splitKey], children = [leftPageID, rightPageID]
	leftPageID := uint32(leftRef.GetPageInfo().GetPageID())
	rightPageID := uint32(rightRef.GetPageInfo().GetPageID())


	keys := [][]byte{splitKey}
	children := []uint32{leftPageID, rightPageID}

	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(newRootPageID), keys, children)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(newRootPageID))
		return fmt.Errorf("materialize root index page: %w", err)
	}

	// Step 3: 创建新的根 PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetNodeRef(offheap.NewNodeRef(uint32(newRootPageID), false))

	// Step 4: CAS 更新根节点（使用 RootPageRef，带重试）
	const maxRetries = 3
	for i := 0; i < maxRetries; i++ {
		oldRootInfo := b.rootRef.pInfo.Load()
		if oldRootInfo == nil {
			// 根未初始化，直接设置
			if b.rootRef.pInfo.CompareAndSwap(nil, newRootInfo) {
				break
			}
			continue
		}

		oldRootID := oldRootInfo.GetPageID()

		if b.rootRef.ReplacePage(oldRootID, newRootInfo) {
			// CAS 成功
			break
		}

		// CAS 失败，重试
		if i == maxRetries-1 {
			// 最后一次重试也失败，返回详细错误
			b.offheapAdapter.pm.Free(uint32(newRootPageID))
			return fmt.Errorf("CAS failed: oldRootID=%d, newRootPageID=%d, retry=%d", oldRootID, newRootPageID, i+1)
		}
	}

	// Step 5: 更新子节点的 parentRef
	leftRef.SetParentRef(b.rootRef.PageRef)
	rightRef.SetParentRef(b.rootRef.PageRef)

	// Step 6: 更新 leafRef（此时 oldLeafRef 就是 b.rootRef）
	// 将旧的叶子节点 PageInfo 更新为左子节点
	newLeftInfo := NewPageInfo()
	newLeftInfo.SetNodeRef(offheap.NewNodeRef(uint32(leftPageID), true))
	newLeftInfo.SetPos(oldLeafInfo.GetPos())
	if oldLeafInfo.IsDirty() {
		newLeftInfo.MarkDirty()
	}

	// 由于 oldLeafRef 就是 root，而且我们已经 CAS 更新了 root
	// 注意：root CAS 成功后，oldLeafRef.pInfo 已经是 newRootInfo，而不是 oldLeafInfo
	// 所以 oldLeafRef.ReplacePage 会失败，这是正常的，不需要再次更新
	// oldLeafRef 就是 root，现在它指向 newRootInfo，不需要额外操作

	// Step 7: 释放旧页面并更新缓存
	b.offheapAdapter.pm.Free(uint32(oldLeafPageID))
	b.pageRefCache.Delete(oldLeafPageID)
	b.pageRefCache.Update(model.PageID(leftPageID), leftRef)
	b.pageRefCache.Update(model.PageID(rightPageID), rightRef)

	// Step 8: 更新 PageRefCache
	b.pageRefCache.Update(model.PageID(newRootPageID), b.rootRef.PageRef)

	return nil
}

// splitInternalOffHeapSync 处理内部节点分裂（Off-Heap 模式，同步）
//
// 参数：
//
//	internalRef - 内部节点的 PageRef
//	internalInfo - 内部节点的 PageInfo
//	internalPageID - 内部节点的 PageID
//	path - 从 Root 到 Internal 的路径（不包括 Internal 本身）
//
// 返回：
//
//	error - 错误信息（ErrRetry 表示需要重试）
func (b *BTree) splitInternalOffHeapSync(internalRef *PageRef, internalInfo *PageInfo, internalPageID model.PageID, path []*PageInfo) error {

	// Step 1: 收集内部节点的所有 keys 和 children
	count := b.offheapAdapter.pa.GetCount(uint32(internalPageID))

	keys := make([][]byte, 0, count)
	children := make([]uint32, 0, count+1)

	// 收集所有 keys 和 children
	for i := 0; i < int(count); i++ {
		keyOff, keyLen, child := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(internalPageID), i)
		key := b.offheapAdapter.pa.GetKey(uint32(internalPageID), keyOff, keyLen)

		// 复制 key
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		keys = append(keys, keyCopy)
		children = append(children, child)
	}

	// 最后一个 child（索引节点的 children 数量 = keys 数量 + 1）
	lastChild := b.offheapAdapter.pa.GetChild(uint32(internalPageID), int(count))
	children = append(children, lastChild)

	// Step 2: 找到中间位置作为分裂点
	// B+ 树分裂规则：中间的 key 提升到父节点
	mid := len(keys) / 2
	splitKey := keys[mid] // 这个 key 会提升到父节点


	// 左子节点：keys[:mid], children[:mid+1]
	leftKeys := make([][]byte, mid)
	copy(leftKeys, keys[:mid])
	leftChildren := make([]uint32, mid+1)
	copy(leftChildren, children[:mid+1])

	// 右子节点：keys[mid+1:], children[mid+1:]
	rightKeys := make([][]byte, len(keys)-mid-1)
	copy(rightKeys, keys[mid+1:])
	rightChildren := make([]uint32, len(children)-mid-1)
	copy(rightChildren, children[mid+1:])

	// Step 3: 分配两个新的内部页面
	leftPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		return fmt.Errorf("alloc left index page: %w", err)
	}
	rightPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(leftPageID))
		return fmt.Errorf("alloc right index page: %w", err)
	}


	// Step 4: 物化左右两半
	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(leftPageID), leftKeys, leftChildren)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(leftPageID))
		b.offheapAdapter.pm.Free(uint32(rightPageID))
		return fmt.Errorf("materialize left index page: %w", err)
	}

	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(rightPageID), rightKeys, rightChildren)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(leftPageID))
		b.offheapAdapter.pm.Free(uint32(rightPageID))
		return fmt.Errorf("materialize right index page: %w", err)
	}

	// Step 5: 创建左右子节点的 PageRef
	leftRef := b.pageRefCache.GetOrCreate(leftPageID, false)
	rightRef := b.pageRefCache.GetOrCreate(rightPageID, false)

	// Step 6: 更新子节点的 parentRef（需要遍历所有子节点的 PageRef）
	// 注意：这是一个简化的实现，完整的实现需要更新所有子节点的 parentRef
	// TODO: 递归更新所有子节点的 parentRef

	// Step 7: 检查是否有父节点
	if len(path) == 0 {
		// 没有父节点，需要创建新的根节点（Root Split）
		// 修复：传递完整的旧根信息，以便正确处理所有 children
		return b.splitRootOffHeapSyncForInternal(internalRef, internalInfo, internalPageID, leftRef, rightRef, splitKey, keys, children)
	}

	// Step 8: 获取父节点的 PageRef
	parentInfo := path[len(path)-1]
	if parentInfo == nil {
		return fmt.Errorf("parent info is nil")
	}

	parentPageID := model.PageID(parentInfo.GetPageID())
	parentRef := b.pageRefCache.GetOrCreate(parentPageID, false)

	// Step 9: 获取父节点锁（自底向上加锁）
	parentLock := parentRef.GetLock()
	if parentLock == nil {
		return fmt.Errorf("parent lock is nil")
	}

	if !parentLock.TryLock() {
		// 锁获取失败，返回重试
		return ErrRetry
	}
	defer parentLock.Unlock()

	// Step 10: 获取父节点的当前 PageInfo
	oldParentInfo := parentRef.GetPageInfo()
	if oldParentInfo == nil {
		return fmt.Errorf("parent page info is nil")
	}

	// Step 11: 在父节点中插入分裂键和新的右子节点
	// 策略：先检查父节点是否已满，如果满了先分裂
	parentPageIDForSearch := model.PageID(oldParentInfo.GetPageID())
	parentCount := b.offheapAdapter.pa.GetCount(uint32(parentPageIDForSearch))

	// 检查父节点是否已满或接近满
	if int(parentCount) >= maxInternalKeys {
		// XXX_DEBUG: 父节点已满，开始分裂
		splitErr := b.splitInternalOffHeapSync(parentRef, oldParentInfo, parentPageIDForSearch, path[:len(path)-1])
		if splitErr != nil {
			return fmt.Errorf("XXX_DEBUG: split parent index page (pathLen=%d, pageID=%d): %w", len(path), internalPageID, splitErr)
		}

		// 父节点分裂后，原来的父节点已经被分裂成两个新节点
		// 分裂键已经被插入到祖父节点中
		// 需要返回 ErrRetry，让外层重新搜索路径并插入
		return ErrRetry
	} else {
		// 父节点未满，直接插入
		insertIndex := 0

		// 二分查找插入位置
		for i := 0; i < int(parentCount); i++ {
			keyOff, keyLen, _ := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(parentPageIDForSearch), i)
			key := b.offheapAdapter.pa.GetKey(uint32(parentPageIDForSearch), keyOff, keyLen)
			if bytes.Compare(key, splitKey) >= 0 {
				insertIndex = i
				break
			}
			insertIndex = i + 1
		}

		// Step 12: 插入索引条目（分裂键 + 右子节点）
		err = b.offheapAdapter.InsertIndexEntry(parentPageIDForSearch, insertIndex, splitKey, model.PageID(rightPageID))
		if err != nil {
			return fmt.Errorf("insert index entry to parent: %w", err)
		}

		// Step 13: 创建新的父节点 PageInfo（Off-Heap 模式）
		newParentInfo := NewPageInfo()
		newParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(parentPageIDForSearch), false))
		newParentInfo.SetPos(oldParentInfo.GetPos())
		if oldParentInfo.IsDirty() {
			newParentInfo.MarkDirty()
		}

		// Step 14: CAS 更新父节点
		if !parentRef.ReplacePage(oldParentInfo, newParentInfo) {
			// CAS 失败，返回重试
			return fmt.Errorf("CAS failed when updating parent after insert (pageID=%d)", internalPageID)
		}

		// CAS 成功后延迟释放旧的内部页面（使用 epoch 避免立即重用）
		b.epochBasedFreeList.Add(internalPageID)
	}

	// Step 15: 更新子节点的 parentRef
	leftRef.SetParentRef(parentRef)
	rightRef.SetParentRef(parentRef)

	// Step 16: 更新 PageRefCache
	b.pageRefCache.Delete(internalPageID)
	b.pageRefCache.Update(leftPageID, leftRef)
	b.pageRefCache.Update(rightPageID, rightRef)

	return nil
}

// splitRootOffHeapSyncForInternal 处理内部节点的根节点分裂（Off-Heap 模式，同步）
// 当内部节点没有父节点时，创建新的根节点
//
// 参数：
//   - oldRootRef: 旧根节点的 PageRef
//   - oldRootInfo: 旧根节点的 PageInfo
//   - oldRootPageID: 旧根节点的 PageID
//   - leftRef: 已分配的左子树 PageRef
//   - rightRef: 已分配的右子树 PageRef
//   - splitKey: 分裂键（提升到新根）
//   - oldKeys: 旧根节点的所有 keys
//   - oldChildren: 旧根节点的所有 children
func (b *BTree) splitRootOffHeapSyncForInternal(
	oldRootRef *PageRef,
	oldRootInfo *PageInfo,
	oldRootPageID model.PageID,
	leftRef, rightRef *PageRef,
	splitKey []byte,
	oldKeys [][]byte,
	oldChildren []uint32,
) error {
	// Step 1: 分配新的根索引页面
	newRootPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		return fmt.Errorf("alloc index page: %w", err)
	}

	// Step 2: 物化根节点内容（splitKey + 左右子节点）
	// 对于新根，我们需要存储 [splitKey][leftChild][rightChild]
	leftPageID := uint32(leftRef.GetPageInfo().GetPageID())
	rightPageID := uint32(rightRef.GetPageInfo().GetPageID())

	keys := [][]byte{splitKey}
	children := []uint32{leftPageID, rightPageID}

	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(newRootPageID), keys, children)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(newRootPageID))
		return fmt.Errorf("materialize root index page: %w", err)
	}

	// Step 3: 创建新的根 PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetNodeRef(offheap.NewNodeRef(uint32(newRootPageID), false))

	// Step 4: CAS 更新根节点（使用 RootPageRef，带重试）
	const maxRetries = 3
	for i := 0; i < maxRetries; i++ {
		oldRootInfo := b.rootRef.pInfo.Load()
		if oldRootInfo == nil {
			// 根未初始化，直接设置
			if b.rootRef.pInfo.CompareAndSwap(nil, newRootInfo) {
				break
			}
			continue
		}

		oldRootID := oldRootInfo.GetPageID()

		if b.rootRef.ReplacePage(oldRootID, newRootInfo) {
			// CAS 成功
			break
		}

		// CAS 失败，重试
		if i == maxRetries-1 {
			// 最后一次重试也失败，返回详细错误
			b.offheapAdapter.pm.Free(uint32(newRootPageID))
			return fmt.Errorf("CAS failed: oldRootID=%d, newRootPageID=%d, retry=%d", oldRootID, newRootPageID, i+1)
		}
	}

	// Step 5: 更新子节点的 parentRef
	leftRef.SetParentRef(b.rootRef.PageRef)
	rightRef.SetParentRef(b.rootRef.PageRef)

	// Step 6: CAS 更新根节点（使用 RootPageRef，带重试）
	for i := 0; i < maxRetries; i++ {
		oldRootInfo := b.rootRef.pInfo.Load()
		if oldRootInfo == nil {
			// 根未初始化，直接设置
			if b.rootRef.pInfo.CompareAndSwap(nil, newRootInfo) {
				break
			}
			continue
		}

		if b.rootRef.ReplacePage(oldRootInfo.GetPageID(), newRootInfo) {
			// CAS 成功，释放旧根页面
			// 注意：使用 epoch 延迟释放，而不是立即释放
			b.epochBasedFreeList.Add(oldRootPageID)
			break
		}

		// CAS 失败，重试
		if i == maxRetries-1 {
			return fmt.Errorf("failed to update root after %d retries", maxRetries)
		}
	}

	// Step 7: 更新 PageRefCache
	b.pageRefCache.Delete(oldRootPageID)
	b.pageRefCache.Update(model.PageID(newRootPageID), b.rootRef.PageRef)
	b.pageRefCache.Update(model.PageID(leftPageID), leftRef)
	b.pageRefCache.Update(model.PageID(rightPageID), rightRef)

	return nil
}
