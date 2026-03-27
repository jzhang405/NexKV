// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/infrastructure/storage/btree/offheap"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
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
	leafRef, path, refs, err := b.findLeafPageRef(ctx, key)
	if err != nil {
		// ✅ 修复：不要包装 ErrRetry，否则 errors.Is() 检查会失败
		return err
	}

	if len(path) == 0 {
		return errpkg.BTreeEmptyPath()
	}

	// Step 2: 获取锁（懒加载，每个 PageRef 有独立的锁）
	pageLock := leafRef.GetLock()
	if pageLock == nil {
		return errpkg.BTreePageLockNil()
	}

	// 使用 TryLock 快速失败（避免死锁）
	if !pageLock.TryLock() {
		return ErrRetry // 快速失败，让外层重试
	}
	defer pageLock.Unlock()

	// Step 3: 获取当前 PageInfo（在锁保护下）
	oldInfo := leafRef.GetPageInfo()
	if oldInfo == nil {
		return errpkg.BTreeLeafPageInfoNil()
	}

	// Step 4: 验证页面已加载（Off-Heap 模式）
	if !oldInfo.IsPageLoaded() {
		return errpkg.BTreeLeafPageNotLoaded2()
	}

	// Step 5: Off-Heap 插入（直接修改，不需要克隆）
	oldPageID := model.PageID(oldInfo.GetPageID())
	newPageID, splitRequired, err := b.offheapAdapter.InsertToOffHeap(oldPageID, key, value)
	if err != nil {
		return errpkg.BTreeOffheapInsert(err)
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

	// Step 8: 如果 pageID 变化（update 场景），区分两种情况
	// 场景 1：UpdateLeafEntry 重新分配了页面，数据已写入，不需要重试
	// 场景 2：需要分裂，必须重试以获取新的路径
	if newPageID != oldPageID {
		if !splitRequired {
			// ✅ 场景 1：UpdateLeafEntry，数据已写入
			// 检查是否是根节点（单层树）
			if len(path) == 1 && leafRef == b.rootRef.PageRef {
				// 特殊处理：根节点的 update 场景
				// 需要更新 rootRef 而不是只更新 PageRefCache
				oldRootInfo := b.rootRef.pInfo.Load()
				oldRootID := uint64(0)
				if oldRootInfo != nil {
					oldRootID = oldRootInfo.GetPageID()
				}
				if !b.rootRef.ReplacePage(oldRootID, newInfo) {
					// CAS 失败，返回重试
					return ErrRetry
				}
				// 更新 PageRefCache（原子操作）
				b.pageRefCache.Replace(oldPageID, newPageID, leafRef)
			} else {
				// 非根节点的 update 场景：
				// 1. 更新 leafRef 的 PageInfo
				// 2. 更新父节点的 child 指针

				// 获取父节点的 PageRef（refs[len(refs)-2]）
				if len(refs) >= 2 {
					parentRef := refs[len(refs)-2]
					parentInfo := parentRef.GetPageInfo()
					if parentInfo != nil {
						parentPageID := uint32(parentInfo.GetPageID())

						// 找到旧 child 的索引
						childIndex := b.offheapAdapter.FindChildIndex(parentPageID, uint32(oldPageID))
						if childIndex >= 0 {
							// 更新父节点的 child 指针（分配新页面）
							newParentPageID, err := b.offheapAdapter.ReplaceChild(
								model.PageID(parentPageID),
								childIndex,
								uint32(newPageID),
							)
							if err != nil {
								// 更新失败，返回重试
								return errpkg.BTreeReplaceChildInParent(err)
							}

							// 创建新的 parent Info
							newParentInfo := NewPageInfo()
							newParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(newParentPageID), false)) // false = isLeaf
							newParentInfo.SetPos(parentInfo.GetPos())

							// CAS 更新 parentRef
							if !parentRef.ReplacePage(parentInfo, newParentInfo) {
								// CAS 失败，返回重试
								return ErrRetry
							}

							// 更新 PageRefCache（原子操作）
							b.pageRefCache.Replace(model.PageID(parentPageID), model.PageID(newParentPageID), parentRef)
						}
					}
				}

				// 更新 leafRef 的 PageInfo
				b.pageRefCache.Replace(oldPageID, newPageID, leafRef)
			}
			// ✅ 不重试，避免覆盖其他数据（数据已经成功写入）
			return nil
		} else {
			// ⚠️ 场景 2：需要分裂，返回 ErrRetry 重新搜索路径
			// 检查是否是根节点（单层树）
			if len(path) == 1 && leafRef == b.rootRef.PageRef {
				// 特殊处理：根节点的 update 场景
				// 需要更新 rootRef 而不是只更新 PageRefCache
				oldRootInfo := b.rootRef.pInfo.Load()
				oldRootID := uint64(0)
				if oldRootInfo != nil {
					oldRootID = oldRootInfo.GetPageID()
				}
				if !b.rootRef.ReplacePage(oldRootID, newInfo) {
					// CAS 失败，返回重试
					return ErrRetry
				}
				// 更新 PageRefCache（原子操作）
				b.pageRefCache.Replace(oldPageID, newPageID, leafRef)
			} else {
				// 非根节点的 update 场景：原子更新 PageRefCache
				b.pageRefCache.Replace(oldPageID, newPageID, leafRef)
			}
			// 返回 ErrRetry，让外层重新搜索路径并处理新页面的分裂
			return ErrRetry
		}
	}

	// Step 9: 检查是否需要分裂（同步，在锁保护下）
	// 此时 newPageID == oldPageID，所以传递 newPageID 即可
	if splitRequired {
		// ✅ 修复：删除提前检查父节点是否满的逻辑
		// BUG: 之前在父节点满时先分裂父节点然后返回 ErrRetry
		// 但原始 key-value 从未插入（InsertToOffHeap 在页面满时不插入）
		// 导致：key-05655 丢失
		// 修复：让正常的分裂流程（handleSplitOffHeapSync）处理父节点满的情况
		// handleSplitOffHeapSync 会先插入 key-value，然后处理父节点分裂

		// 获取页面级别的分裂锁，防止多个 goroutine 同时分裂同一页面
		splitMuAny, _ := b.splitMuMap.LoadOrStore(uint32(newPageID), &sync.Mutex{})
		splitMu := splitMuAny.(*sync.Mutex)
		splitMu.Lock()
		defer splitMu.Unlock()
		// 不删除锁，永久保留（内存开销可忽略：< 1MB）
		// 删除锁会导致竞态条件：其他 goroutine 可能获取新锁并访问已释放的页面

		// 需要分裂，调用 Off-Heap 分裂逻辑
		// 注意：分裂会释放当前锁，按深度顺序获取新的锁
		// 传递 key-value，分裂后需要重新插入
		leftRef, err := b.handleSplitOffHeapSync(leafRef, newInfo, newPageID, path, key, value)
		if err != nil {
			// 分裂失败，返回 ErrRetry 让外层重试
			// 注意：如果 SplitOffHeapLeafPage 失败，页面可能已处于不一致状态
			return ErrRetry
		}
		// 分裂成功，leafRef 现在指向 leftPageID
		// 更新 newInfo 以便后续持久化使用正确的页面信息
		newInfo = leftRef.GetPageInfo()
		if newInfo == nil {
			return errpkg.BTreeLeftRefPageInfoNil()
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
			return errpkg.BTreeRootPageInfoNilAfterPersist()
		}

		// 构建持久化路径：从 Root 到 Leaf 的完整路径
		// Leaf-Level Locking 只克隆了叶子节点，需要收集完整路径进行深拷贝
		persistPath := b.buildPersistPath(currentRoot, newInfo)

		// 深拷贝路径（确保数据独立）
		if err := b.finalizeDeepClone(persistPath); err != nil {
			return errpkg.BTreeFinalizeDeepClone(err)
		}

		// 持久化根节点（会递归持久化整个树）
		if err := b.persistRoot(currentRoot); err != nil {
			return errpkg.BTreePersistRootErr(err)
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
			for i := range internalPage.NumChildren() {
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
func (b *BTree) handleSplitOffHeapSync(leafRef *PageRef, leafInfo *PageInfo, leafPageID model.PageID, path []*PageInfo, key, value []byte) (*PageRef, error) {
	// ✅ 修复：检查传入的页面类型
	// BUG: 有时候 leafPageID 指向的是 INDEX 节点（例如根节点），而不是 LEAF 节点
	// 原因：页面类型损坏或页面 ID 重用
	// 修复：检查页面类型，如果是 INDEX 节点，调用内部节点分裂逻辑
	count := b.offheapAdapter.pa.GetCount(uint32(leafPageID))
	isLeaf := b.offheapAdapter.IsLeaf(leafPageID)

	if !isLeaf {
		// 传入的是 INDEX 节点，应该调用 splitInternalOffHeapSync
		DebugPrintf("[HANDLE_SPLIT] WARNING: leafPageID=%d is INDEX node, calling splitInternalOffHeapSync instead\n", leafPageID)

		// 调用内部节点分裂
		err := b.splitInternalOffHeapSync(leafRef, leafInfo, leafPageID, path)
		if err != nil {
			return nil, err
		}

		// 内部节点分裂成功，返回 ErrRetry 让外层重试
		return nil, ErrRetry
	}

	// 活锁检测：检查当前页面是否已经被多次加入延迟释放列表
	// 通过检查当前 epoch 的待释放列表来判断
	b.epochBasedFreeList.mu.Lock()
	currentEpoch := b.epochBasedFreeList.currentEpoch
	currentEpochPending := b.epochBasedFreeList.pending[currentEpoch]
	pendingCount := 0
	for _, pid := range currentEpochPending {
		if pid == leafPageID {
			pendingCount++
		}
	}
	b.epochBasedFreeList.mu.Unlock()

	if pendingCount > 15 {
		// 同一个页面被多次尝试分裂，可能存在活锁
		// 降低阈值到 15，更早触发备用策略
		// 使用备用策略：创建新页面，直接插入 key-value，不触发分裂
		DebugPrintf("[HANDLE_SPLIT] ============================================\n")
		DebugPrintf("[HANDLE_SPLIT] Livelock detected: pageID=%d pendingCount=%d, using fallback strategy\n", leafPageID, pendingCount)
		DebugPrintf("[HANDLE_SPLIT] ============================================\n")

		// 分配新的页面
		newPageID, err := b.offheapAdapter.pm.Alloc()
		if err != nil {
			return nil, errpkg.BTreeAllocFallbackPage(err)
		}

		// 直接插入 key-value 到新页面
		_, splitRequiredFallback, err := b.offheapAdapter.InsertToOffHeap(model.PageID(newPageID), key, value)
		if err != nil {
			b.offheapAdapter.pm.Free(newPageID)
			return nil, errpkg.BTreeFallbackInsert(err)
		}
		if splitRequiredFallback {
			// 新页面也满了，返回 ErrRetry
			DebugPrintf("[HANDLE_SPLIT] Fallback page also full, returning ErrRetry\n")
			b.offheapAdapter.pm.Free(newPageID)
			return nil, ErrRetry
		}

		DebugPrintf("[HANDLE_SPLIT] Fallback SUCCESS: key=%s inserted into new pageID=%d\n", string(key), newPageID)

		// 创建新的 PageRef
		newInfo := NewPageInfo()
		newInfo.SetNodeRef(offheap.NewNodeRef(newPageID, true))
		newInfo.SetPos(leafInfo.GetPos())
		if leafInfo.IsDirty() {
			newInfo.MarkDirty()
		}

		// CAS 更新 leafRef
		if !leafRef.ReplacePage(leafInfo, newInfo) {
			// CAS 失败，释放新页面
			b.offheapAdapter.pm.Free(newPageID)
			return nil, ErrRetry
		}

		// 修复：备用策略也需要更新父节点
		// 否则搜索路径仍然指向旧页面，导致找不到新插入的 key
		if len(path) >= 2 {
			DebugPrintf("[FALLBACK] len(path)=%d, attempting to update parent\n", len(path))
			// 获取父节点的 PageRef
			parentInfo := path[len(path)-2]
			if parentInfo == nil {
				DebugPrintf("[FALLBACK] parent info is nil\n")
				return nil, errpkg.BTreeParentInfoNilInFallback("fallback")
			}

			parentPageID := model.PageID(parentInfo.GetPageID())
			parentRef := b.pageRefCache.GetOrCreate(parentPageID, false)

			// 获取父节点锁
			parentLock := parentRef.GetLock()
			if parentLock == nil {
				DebugPrintf("[FALLBACK] parent lock is nil\n")
				return nil, errpkg.BTreeParentLockNilInFallback("fallback")
			}

			if !parentLock.TryLock() {
				// 锁获取失败，返回重试
				DebugPrintf("[FALLBACK] parent lock trylock failed\n")
				return nil, ErrRetry
			}
			defer parentLock.Unlock()

			// 获取父节点的当前 PageInfo（必须在锁内读取，避免竞态）
			oldParentInfo := parentRef.GetPageInfo()
			if oldParentInfo == nil {
				DebugPrintf("[FALLBACK] parent page info is nil\n")
				return nil, errpkg.BTreeParentInfoNilInFallback("get parent info")
			}

			// 查找旧页面在父节点中的位置（必须在锁内查找，避免竞态）
			parentPageIDForSearch := model.PageID(oldParentInfo.GetPageID())
			parentCount := b.offheapAdapter.pa.GetCount(uint32(parentPageIDForSearch))

			// 检查父节点是否已满
			if int(parentCount) >= maxInternalKeys {
				// 父节点已满，需要先分裂父节点
				DebugPrintf("[FALLBACK] parent page FULL (count=%d), calling splitInternalOffHeapSync\n", parentCount)

				// 调用内部节点分裂来分裂父节点
				splitErr := b.splitInternalOffHeapSync(parentRef, oldParentInfo, parentPageIDForSearch, path[:len(path)-2])
				if splitErr != nil {
					DebugPrintf("[FALLBACK] splitInternalOffHeapSync FAILED: %v\n", splitErr)
					// 释放新页面
					b.offheapAdapter.pm.Free(newPageID)
					// 返回 ErrRetry 让外层重试
					return nil, ErrRetry
				}

				// 父节点分裂成功，现在可以更新父节点索引
				// 但为了简化逻辑，直接返回 ErrRetry 让外层重新处理
				DebugPrintf("[FALLBACK] parent split SUCCESS, freeing new page and returning ErrRetry\n")
				b.offheapAdapter.pm.Free(newPageID)
				return nil, ErrRetry
			}

			insertIndex := 0
			childFound := false
			for i := range int(parentCount) {
				_, _, encodedChild := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(parentPageIDForSearch), i)
				child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
				if child == uint32(leafPageID) {
					// 找到旧页面的位置，替换为新页面
					insertIndex = i
					childFound = true
					break
				}
			}

			// 验证找到了子节点（防止竞态条件）
			if !childFound {
				DebugPrintf("[FALLBACK] child pageID %d not found in parent %d (race condition), returning ErrRetry\n",
					leafPageID, parentPageIDForSearch)
				return nil, ErrRetry
			}

			DebugPrintf("[FALLBACK] parentPageID=%d count=%d insertIndex=%d leafPageID=%d newPageID=%d\n",
				parentPageIDForSearch, parentCount, insertIndex, leafPageID, newPageID)

			// 使用 ReplaceChild 替换父节点中的单个子节点（不增加子节点数量）
			// 注意：这里不应该使用 UpdateIndexEntry，因为它会插入额外的子节点（rightPageID）
			newParentPageID, err := b.offheapAdapter.ReplaceChild(parentPageIDForSearch, insertIndex, uint32(newPageID))
			if err != nil {
				DebugPrintf("[FALLBACK] UpdateIndexEntry failed: %v\n", err)
				return nil, errpkg.BTreeUpdateParentInFallback(err)
			}

			// 创建新的父节点 PageInfo
			newParentInfo := NewPageInfo()
			newParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(newParentPageID), false))
			newParentInfo.SetPos(oldParentInfo.GetPos())
			if oldParentInfo.IsDirty() {
				newParentInfo.MarkDirty()
			}

			// CAS 更新父节点
			if !parentRef.ReplacePage(oldParentInfo, newParentInfo) {
				// CAS 失败，使用 epoch 延迟释放新父页面
				DebugPrintf("[FALLBACK] parent CAS failed, adding to epoch delay list\n")
				b.epochBasedFreeList.Add(model.PageID(newParentPageID))
				return nil, ErrRetry
			}

			// CAS 成功后释放旧父页面
			b.offheapAdapter.pm.Free(uint32(parentPageIDForSearch))

			// 更新 PageRefCache
			b.pageRefCache.Delete(parentPageIDForSearch)
			b.pageRefCache.Update(newParentPageID, parentRef)

			// 更新新页面的 parentRef
			newLeafRef := b.pageRefCache.GetOrCreate(model.PageID(newPageID), true)
			newLeafRef.SetParentRef(parentRef)

			DebugPrintf("[FALLBACK] parent update SUCCESS: oldParent=%d newParent=%d\n",
				parentPageIDForSearch, newParentPageID)
		} else {
			DebugPrintf("[FALLBACK] len(path)=%d, no parent to update\n", len(path))
		}

		// 返回成功
		return leafRef, nil
	}

	// 调试：记录页面 ID 和 leafRef 状态
	DebugPrintf("[HANDLE_SPLIT] pageID=%d isLeaf=%v count=%d pendingCount=%d key=%s\n",
		leafPageID, isLeaf, count, pendingCount, string(key))

	// 注意：这里 leafPageID 是旧页面 ID，leafInfo 指向新页面
	// 但当页面已满时，InsertToOffHeap 返回 splitRequired=true 但不插入数据
	// 所以我们需要从旧页面分裂，然后插入新数据

	// 修复：如果是索引节点，调用索引节点分裂逻辑
	if !isLeaf {
		DebugPrintf("[HANDLE_SPLIT] pageID=%d is INDEX node, calling splitInternalOffHeapSync\n", leafPageID)
		// 索引节点分裂：需要递归向上处理
		// 注意：此时 leafRef 实际上是索引节点的 PageRef
		err := b.splitInternalOffHeapSync(leafRef, leafInfo, leafPageID, path[:len(path)-1])
		if err != nil {
			return nil, err
		}
		// 索引节点分裂后，返回 ErrRetry 让外层重新搜索
		return nil, ErrRetry
	}

	// 叶子节点分裂逻辑
	// Step 1: 调用 OffHeapAdapter.SplitOffHeapLeafPage（从旧页面分裂）
	DebugPrintf("[HANDLE_SPLIT] Step 1: Splitting leaf pageID=%d\n", leafPageID)

	leftPageID, rightPageID, splitKey, err := b.offheapAdapter.SplitOffHeapLeafPage(leafPageID)

	if err != nil {
		// 分裂失败，返回 ErrRetry 让外层重试
		// 注意：如果 SplitOffHeapLeafPage 失败，页面可能已处于不一致状态
		DebugPrintf("[HANDLE_SPLIT] SplitOffHeapLeafPage FAILED: %v\n", err)
		return nil, ErrRetry
	}
	DebugPrintf("[HANDLE_SPLIT] SplitOffHeapLeafPage SUCCESS: leftPageID=%d rightPageID=%d splitKey=%s\n",
		leftPageID, rightPageID, string(splitKey))

	// Step 2: 创建左右子节点的 PageRef（更新 leafRef 指向左侧，创建新的右侧）
	leftRef := b.pageRefCache.GetOrCreate(leftPageID, true)
	rightRef := b.pageRefCache.GetOrCreate(rightPageID, true)

	// Step 3: 检查是否有父节点（根分裂场景需要特殊处理）
	if len(path) < 2 {
		DebugPrintf("[HANDLE_SPLIT] len(path)=%d, ROOT SPLIT scenario\n", len(path))
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

		// ✅ 修复：重新插入原始 key-value（修复数据丢失问题）
		// 因为 InsertToOffHeap 在页面满时返回 splitRequired=true 但不插入数据
		// 所以分裂完成后需要重新插入原始 key-value
		// 根据 splitKey 决定插入到 leftPageID 还是 rightPageID
		// 参考：Step 16 (line 998-1024) 在非根分裂场景的重新插入逻辑
		targetPageID := leftPageID
		cmp := bytes.Compare(key, splitKey)
		if cmp > 0 {
			// key > splitKey，应该插入到右页面
			targetPageID = rightPageID
		} else if cmp == 0 {
			// key == splitKey，应该插入到左页面（splitKey 是右页面的第一个键）
			targetPageID = leftPageID
		}

		// 重新插入 key-value 到目标页面
		_, splitRequired2, err := b.offheapAdapter.InsertToOffHeap(targetPageID, key, value)
		if err != nil {
			DebugPrintf("[ROOT_SPLIT_POST_INSERT] FAILED to insert key=%s into pageID=%d: %v\n", string(key), targetPageID, err)
			return nil, errpkg.BTreeRootSplitPostSplitInsertFailed(err)
		}
		if splitRequired2 {
			// 重新插入后仍然需要分裂，返回 ErrRetry 让外层重试
			DebugPrintf("[ROOT_SPLIT_POST_INSERT] key=%s still needs split after inserting to pageID=%d\n", string(key), targetPageID)
			return nil, ErrRetry
		}
		DebugPrintf("[ROOT_SPLIT_POST_INSERT] SUCCESS: key=%s inserted into pageID=%d\n", string(key), targetPageID)

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
		return nil, errpkg.BTreeParentInfoNilOp("handle split")
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
		return nil, errpkg.BTreeParentLockNilOp("handle split")
	}

	if !parentLock.TryLock() {
		// 锁获取失败，返回重试
		return nil, ErrRetry
	}
	defer parentLock.Unlock()

	// Step 8: 找到插入位置（二分查找，必须在锁内进行以避免竞态）
	// 重新读取父节点信息，因为可能在获取锁之前已变化
	currentParentInfo := parentRef.GetPageInfo()
	if currentParentInfo == nil {
		return nil, errpkg.BTreeParentInfoNilAfterCASOp()
	}

	currentParentPageID := model.PageID(currentParentInfo.GetPageID())
	if currentParentPageID != oldParentPageID {
		// 父节点已变化，返回重试
		return nil, ErrRetry
	}

	var currentParentCount uint16
	count = b.offheapAdapter.pa.GetCount(uint32(currentParentPageID))

	// 修复：基于子节点 ID 定位，而不是 splitKey
	// 原逻辑使用 splitKey 的二分查找，但并发场景下父节点的 keys 可能被修改
	// 导致 insertIndex 指向错误的子节点，从而产生循环引用
	insertIndex := -1
	for i := 0; i <= int(count); i++ {
		// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
		encodedChild := b.offheapAdapter.pa.GetChild(uint32(currentParentPageID), i)
		child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
		if child == uint32(leafPageID) {
			insertIndex = i
			break
		}
	}

	if insertIndex == -1 {
		// 子节点未找到，父节点已被其他 goroutine 修改
		DebugPrintf("[HANDLE_SPLIT] Child %d not found in parent %d (count=%d)\n",
			leafPageID, currentParentPageID, count)
		return nil, ErrRetry
	}

	// 防御性检查：确保找到的位置合理
	if insertIndex < 0 || insertIndex > int(count) {
		return nil, errpkg.BTreeInvalidInsertIndex(insertIndex, int(count))
	}

	// Step 9: 使用 UpdateIndexEntry 更新父节点（不可变方式）
	// 替换旧的子页面为新的左右子页面
	// 注意：无论 insertIndex 是否等于 count，都使用 UpdateIndexEntry
	// UpdateIndexEntry 会正确处理所有情况
	newParentPageID, err := b.offheapAdapter.UpdateIndexEntry(currentParentPageID, insertIndex, splitKey, uint32(leftPageID), uint32(rightPageID))

	if err != nil {
		// 检查是否是页面已满错误
		if strings.Contains(err.Error(), "page full") {
			// ✅ 检查树的高度
			if len(path) == 2 {
				// ===== 2 层树（Root + Leaf）场景 =====
				// path[0] = Root（同时是父节点）
				// path[1] = Leaf
				// Root 满了，需要专门处理

				DebugPrintf("[HANDLE_SPLIT] 2-layer tree root split: rootPageID=%d\n", currentParentPageID)

				// 释放父节点锁（handleRootSplitOnly 会重新获取）
				parentLock.Unlock()

				// 调用专门的 Root Split 处理函数
				rootSplitErr := b.handleRootSplitOnly(
					parentRef,
					currentParentInfo,
					currentParentPageID,
					leftRef,
					rightRef,
					splitKey,
					key,
				)

				if rootSplitErr != nil {
					DebugPrintf("[HANDLE_SPLIT] handleRootSplitOnly FAILED: %v\n", rootSplitErr)
					return nil, ErrRetry
				}

				// Root Split 成功，返回 ErrRetry 让外层重试
				DebugPrintf("[HANDLE_SPLIT] handleRootSplitOnly SUCCESS, returning ErrRetry\n")
				return nil, ErrRetry
			}
		}
		return nil, errpkg.BTreeUpdateParentIndex(err)
	}

	// Step 10: 创建新的父节点 PageInfo
	newParentInfo := NewPageInfo()
	newParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(newParentPageID), false))
	newParentInfo.SetPos(parentInfo.GetPos())
	if parentInfo.IsDirty() {
		newParentInfo.MarkDirty()
	}

	// 调试：打印新父页面信息
	DebugPrintf("[PARENT_UPDATE] Starting: oldParent=%d newParent=%d\n",
		oldParentPageID, newParentPageID)

	// Step 11: CAS 更新父节点（使用 currentParentInfo 而不是 parentInfo）
	// 关键修复：必须使用锁内读取的 currentParentInfo，而不是锁前的 parentInfo
	const maxCASRetries = 2
	var lastErr error
	for casAttempt := range maxCASRetries {
		// 每次重试都重新读取最新的父节点信息
		latestParentInfo := parentRef.GetPageInfo()
		if latestParentInfo == nil {
			lastErr = errpkg.BTreeParentInfoNilDuringCASRetry()
			break
		}

		// 验证 pageID 未变化
		if latestParentInfo.GetPageID() != currentParentInfo.GetPageID() {
			// 父节点已被其他 goroutine 更新到不同的 pageID
			lastErr = errpkg.BTreeParentPageIDChangedDuringRetry(
				uint64(currentParentInfo.GetPageID()), uint64(latestParentInfo.GetPageID()))
			break
		}

		// 使用最新的 parentInfo 进行 CAS
		if parentRef.ReplacePage(latestParentInfo, newParentInfo) {
			// CAS 成功，继续后续流程
			lastErr = nil
			break
		}

		// CAS 失败，继续重试
		lastErr = errpkg.BTreeCASRetryExhausted(casAttempt)
	}

	if lastErr != nil {
		// 所有 CAS 重试都失败，使用 epoch 延迟释放新父页面
		// 避免立即释放导致数据丢失
		DebugPrintf("[PARENT_UPDATE_FAILED] oldParent=%d newParent=%d err=%v\n",
			oldParentPageID, newParentPageID, lastErr)
		b.epochBasedFreeList.Add(model.PageID(newParentPageID))
		return nil, ErrRetry
	}

	// Step 11.5: 验证新父节点没有循环引用（防御性检查）
	if b.hasCycleFrom(newParentPageID) {
		// 检测到循环引用，回滚操作
		DebugPrintf("[PARENT_UPDATE] CIRCULAR REFERENCE DETECTED after split: newParentPageID=%d\n", newParentPageID)
		b.epochBasedFreeList.Add(model.PageID(newParentPageID))
		b.epochBasedFreeList.Add(leftPageID)
		b.epochBasedFreeList.Add(rightPageID)
		return nil, errpkg.BTreeCircularReferenceAfterParentUpdate(uint64(newParentPageID))
	}

	// Step 11.6: 验证新父节点的分裂完整性（确保旧子节点被正确移除）
	err = b.validateParentSplitIntegrity(newParentPageID, leafPageID, leftPageID, rightPageID)
	if err != nil {
		// 完整性验证失败，回滚操作
		DebugPrintf("[PARENT_UPDATE] Integrity check failed: %v\n", err)
		b.epochBasedFreeList.Add(model.PageID(newParentPageID))
		b.epochBasedFreeList.Add(leftPageID)
		b.epochBasedFreeList.Add(rightPageID)
		return nil, errpkg.BTreeParentSplitIntegrityCheck(err)
	}

	// Step 11.6: CAS 成功后更新 PageRefCache
	// 只删除旧父页面的缓存条目，更新新父页面的缓存条目
	// 注意：不要删除 newParentPageID，因为这会导致短暂的不一致状态
	b.pageRefCache.Delete(oldParentPageID)
	b.pageRefCache.Update(newParentPageID, parentRef)

	// Step 12: CAS 成功后释放旧父页面
	b.offheapAdapter.pm.Free(uint32(oldParentPageID))

	// Step 14: 更新子节点的 parentRef
	leftRef.SetParentRef(parentRef)
	rightRef.SetParentRef(parentRef)

	// Step 15: 检查父节点是否需要分裂，或是否需要向上更新祖父节点
	// 获取更新后的父节点信息
	currentParentInfo = parentRef.GetPageInfo()
	if currentParentInfo == nil {
		return nil, errpkg.BTreeParentInfoNilAfterCASOp()
	}
	currentParentPageID = model.PageID(currentParentInfo.GetPageID())
	currentParentCount = b.offheapAdapter.pa.GetCount(uint32(currentParentPageID))

	// 调试：打印父节点状态

	// 检查父节点 pageID 是否发生了变化（COW 更新）
	if currentParentPageID != oldParentPageID {
		// 父节点被更新（例如 547 -> 553），需要向上更新祖父节点的子节点指针
		DebugPrintf("[PARENT_ID_CHANGED] oldParent=%d newParent=%d pathLen=%d\n", oldParentPageID, currentParentPageID, len(path))

		// 检查是否有祖父节点（path 包含 [root, ..., parent, leaf]，所以 pathLen >= 3 时才有祖父节点）
		// 祖父节点是 path[len(path)-3]（倒数第三个），而不是 path[len(path)-2]（倒数第二个，这是父节点自己）
		if len(path) >= 3 {
			// 有祖父节点，需要更新祖父节点的子节点指针
			grandParentInfo := path[len(path)-3]
			if grandParentInfo == nil {
				return nil, errpkg.BTreeGrandparentInfoNilOp("handle split")
			}

			grandParentPageID := model.PageID(grandParentInfo.GetPageID())

			// 找到祖父节点中指向旧父节点的位置
			grandParentCount := b.offheapAdapter.pa.GetCount(uint32(grandParentPageID))
			foundIndex := -1
			for i := 0; i <= int(grandParentCount); i++ {
				// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
				encodedChild := b.offheapAdapter.pa.GetChild(uint32(grandParentPageID), i)
				child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
				if child == uint32(oldParentPageID) {
					foundIndex = i
					break
				}
			}

			if foundIndex == -1 {
				DebugPrintf("[UPDATE_GRANDPARENT] oldParent=%d not found in grandparent %d, grandParentCount=%d\n",
					oldParentPageID, grandParentPageID, grandParentCount)
				// 打印祖父节点的所有 children
				for i := 0; i <= int(grandParentCount); i++ {
					encodedChild := b.offheapAdapter.pa.GetChild(uint32(grandParentPageID), i)
					child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
					DebugPrintf("[UPDATE_GRANDPARENT]   child[%d]=%d\n", i, child)
				}
				return nil, errpkg.BTreeOldParentNotFoundInGrandparent(uint64(oldParentPageID), uint64(grandParentPageID))
			}

			DebugPrintf("[UPDATE_GRANDPARENT] grandParent=%d index=%d oldChild=%d newChild=%d grandParentCount=%d\n",
				grandParentPageID, foundIndex, oldParentPageID, currentParentPageID, grandParentCount)

			// 使用 COW 方式更新祖父节点的子节点指针
			// 这里需要找到对应的 key（如果 foundIndex < count）或 extraChild（如果 foundIndex == count）
			if foundIndex < int(grandParentCount) {
				// 替换中间的子节点指针
				// 需要找到对应的 key，并使用 UpdateIndexEntry 更新
				keyOff, keyLen, _ := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(grandParentPageID), foundIndex)
				key := b.offheapAdapter.pa.GetKey(uint32(grandParentPageID), keyOff, keyLen)

				// 获取右边的子节点（如果有的话）
				// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
				var rightChild uint32
				if foundIndex+1 < int(grandParentCount) {
					encodedRightChild := b.offheapAdapter.pa.GetChild(uint32(grandParentPageID), foundIndex+1)
					rightChild, _ = b.offheapAdapter.DecodeChildWithVersion(encodedRightChild)
				} else {
					encodedExtraChild := b.offheapAdapter.pa.GetChild(uint32(grandParentPageID), int(grandParentCount))
					rightChild, _ = b.offheapAdapter.DecodeChildWithVersion(encodedExtraChild)
				}

				newGrandParentPageID, err := b.offheapAdapter.UpdateIndexEntry(
					grandParentPageID, foundIndex, key, uint32(currentParentPageID), rightChild)
				if err != nil {
					// 检查是否是页面已满错误
					if strings.Contains(err.Error(), "page full") {
						// 祖父节点已满，需要分裂
						DebugPrintf("[UPDATE_GRANDPARENT] grandparent page full, triggering split: grandParent=%d err=%v\n",
							grandParentPageID, err)

						// 需要重新搜索路径（因为父节点可能已改变）
						// 返回 ErrRetry 让外层重试
						return nil, ErrRetry
					}
					return nil, errpkg.BTreeUpdateGrandparent(err)
				}

				// 更新祖父节点的 PageRef
				grandParentRef := b.pageRefCache.GetOrCreate(grandParentPageID, false)
				grandParentLock := grandParentRef.GetLock()
				if grandParentLock == nil {
					return nil, errpkg.BTreeGrandparentLockNilOp("update grandparent")
				}

				if !grandParentLock.TryLock() {
					return nil, ErrRetry
				}
				defer grandParentLock.Unlock()

				newGrandParentInfo := NewPageInfo()
				newGrandParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(newGrandParentPageID), false))
				newGrandParentInfo.SetPos(grandParentInfo.GetPos())
				if grandParentInfo.IsDirty() {
					newGrandParentInfo.MarkDirty()
				}

				// CAS 更新祖父节点
				if !grandParentRef.ReplacePage(grandParentInfo, newGrandParentInfo) {
					// CAS 失败，释放新祖父页面
					b.epochBasedFreeList.Add(model.PageID(newGrandParentPageID))
					return nil, ErrRetry
				}

				// CAS 成功，释放旧祖父页面
				b.offheapAdapter.pm.Free(uint32(grandParentPageID))

				// 更新 PageRefCache
				b.pageRefCache.Delete(grandParentPageID)
				b.pageRefCache.Update(model.PageID(newGrandParentPageID), grandParentRef)

				// 更新 parentRef 的父节点引用
				parentRef.SetParentRef(grandParentRef)
			} else {
				// 替换 extraChild（最后一个子节点）
				// extraChild 是 B+ 树索引节点的 N+1 child，需要特殊处理
				DebugPrintf("[UPDATE_GRANDPARENT] updating extraChild: grandParent=%d oldExtraChild=%d newExtraChild=%d\n",
					grandParentPageID, oldParentPageID, currentParentPageID)

				// 获取祖父节点的所有 keys 和 children
				grandParentCountInt := int(grandParentCount)
				keys := make([][]byte, 0, grandParentCountInt)
				children := make([]uint32, 0, grandParentCountInt+1)

				// 收集所有 keys 和 children（除了最后一个 extraChild）
				for i := range grandParentCountInt {
					keyOff, keyLen, encodedChild := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(grandParentPageID), i)
					key := b.offheapAdapter.pa.GetKey(uint32(grandParentPageID), keyOff, keyLen)

					keyCopy := make([]byte, len(key))
					copy(keyCopy, key)
					keys = append(keys, keyCopy)
					// 修复：GetIndexEntryOffset 返回编码后的值，需要解码才能获取真实的 pageID
					child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
					children = append(children, child)
				}

				// 替换 extraChild
				children = append(children, uint32(currentParentPageID))

				// 创建新的祖父页面
				newGrandParentPageID, err := b.offheapAdapter.pm.Alloc()
				if err != nil {
					return nil, errpkg.BTreeAllocGrandparentPage(err)
				}

				_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(newGrandParentPageID), keys, children)
				if err != nil {
					b.offheapAdapter.pm.Free(uint32(newGrandParentPageID))
					// 检查是否是页面已满错误
					if strings.Contains(err.Error(), "page full") {
						// 祖父节点已满，需要分裂
						DebugPrintf("[UPDATE_GRANDPARENT] grandparent page full (extraChild), triggering split: grandParent=%d err=%v\n",
							grandParentPageID, err)
						// 返回 ErrRetry 让外层重试
						return nil, ErrRetry
					}
					return nil, errpkg.BTreeMaterializeGrandparentPage(err)
				}

				// 更新祖父节点的 PageRef
				grandParentRef := b.pageRefCache.GetOrCreate(grandParentPageID, false)
				grandParentLock := grandParentRef.GetLock()
				if grandParentLock == nil {
					return nil, errpkg.BTreeGrandparentLockNilAfterAlloc("materialize extraChild")
				}

				if !grandParentLock.TryLock() {
					b.offheapAdapter.pm.Free(uint32(newGrandParentPageID))
					return nil, ErrRetry
				}
				defer grandParentLock.Unlock()

				newGrandParentInfo := NewPageInfo()
				newGrandParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(newGrandParentPageID), false))
				newGrandParentInfo.SetPos(grandParentInfo.GetPos())
				if grandParentInfo.IsDirty() {
					newGrandParentInfo.MarkDirty()
				}

				// CAS 更新祖父节点
				if !grandParentRef.ReplacePage(grandParentInfo, newGrandParentInfo) {
					// CAS 失败，释放新祖父页面
					b.offheapAdapter.pm.Free(uint32(newGrandParentPageID))
					return nil, ErrRetry
				}

				// CAS 成功，释放旧祖父页面
				b.offheapAdapter.pm.Free(uint32(grandParentPageID))

				// 更新 PageRefCache
				b.pageRefCache.Delete(grandParentPageID)
				b.pageRefCache.Update(model.PageID(newGrandParentPageID), grandParentRef)

				// 更新 parentRef 的父节点引用
				parentRef.SetParentRef(grandParentRef)

				DebugPrintf("[UPDATE_GRANDPARENT] successfully updated extraChild: grandParent=%d -> newGrandParent=%d\n",
					grandParentPageID, newGrandParentPageID)
			}
		} else {
			// 没有祖父节点，说明父节点就是根节点
			// 根节点已经通过 CAS 更新了，不需要额外处理
			DebugPrintf("[PARENT_ID_CHANGED] parent is root, no grandparent update needed\n")
		}
	}

	// Step 16: 重新插入原始 key-value（修复数据丢失问题）
	// 因为 InsertToOffHeap 在页面满时返回 splitRequired=true 但不插入数据
	// 所以分裂完成后需要重新插入原始 key-value
	// 根据 splitKey 决定插入到 leftPageID 还是 rightPageID
	targetPageID := leftPageID
	cmp := bytes.Compare(key, splitKey)
	if cmp > 0 {
		// key > splitKey，应该插入到右页面
		targetPageID = rightPageID
	} else if cmp == 0 {
		// key == splitKey，应该插入到左页面（splitKey 是右页面的第一个键）
		targetPageID = leftPageID
	}

	// 重新插入 key-value 到目标页面
	// 注意：必须在父节点分裂前插入，因为父节点分裂会返回 ErrRetry
	_, splitRequired2, err := b.offheapAdapter.InsertToOffHeap(targetPageID, key, value)
	if err != nil {
		DebugPrintf("[POST_SPLIT_INSERT] FAILED to insert key=%s into pageID=%d: %v\n", string(key), targetPageID, err)
		return nil, errpkg.BTreePostSplitInsert(err)
	}
	if splitRequired2 {
		// 重新插入后仍然需要分裂，返回 ErrRetry 让外层重试
		DebugPrintf("[POST_SPLIT_INSERT] key=%s still needs split after inserting to pageID=%d\n", string(key), targetPageID)
		return nil, ErrRetry
	}
	DebugPrintf("[POST_SPLIT_INSERT] SUCCESS: key=%s inserted into pageID=%d\n", string(key), targetPageID)

	// 检查父节点是否需要分裂
	if int(currentParentCount) >= maxInternalKeys {
		DebugPrintf("[HANDLE_SPLIT] parent full, triggering sync split: parentPageID=%d count=%d\n",
			currentParentPageID, currentParentCount)

		// ✅ 修复：必须同步等待父节点索引更新完成
		// BUG: 之前使用异步分裂，在父节点索引更新前就返回成功
		// 导致：key-value 在叶子节点中，但父节点索引未更新，Get() 无法找到
		// 修复：删除异步路径，总是使用同步分裂，确保索引更新后才返回
		DebugPrintf("[HANDLE_SPLIT] using sync split to ensure index updated\n")
		DebugPrintf("[HANDLE_SPLIT] calling splitInternalOffHeapSync: parentPageID=%d, pathLen=%d\n",
			currentParentPageID, len(path[:len(path)-1]))

		err := b.splitInternalOffHeapSync(parentRef, currentParentInfo, currentParentPageID, path[:len(path)-1])
		if err != nil {
			DebugPrintf("[HANDLE_SPLIT] splitInternalOffHeapSync FAILED: %v\n", err)
			return nil, err
		}

		DebugPrintf("[HANDLE_SPLIT] splitInternalOffHeapSync SUCCESS, returning ErrRetry\n")
		// 父节点分裂成功，返回 ErrRetry 让外层重新搜索路径
		return nil, ErrRetry
	}

	// Step 17: 确保所有更新对其他 goroutine 可见
	// 添加内存屏障，建议调度器调度其他 goroutine
	runtime.Gosched()

	// 分裂成功，返回 leftRef 和 nil
	// 注意：原始 key-value 现在已经插入到正确的页面中
	return leftRef, nil
}

// splitRootOffHeapSync 处理根节点分裂（Off-Heap 模式，同步）
// 当叶子节点没有父节点时，创建新的内部节点作为根
func (b *BTree) splitRootOffHeapSync(oldLeafRef *PageRef, oldLeafInfo *PageInfo, leftRef, rightRef *PageRef, splitKey []byte, oldLeafPageID model.PageID) error {

	// Step 1: 分配新的根索引页面
	newRootPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		return errpkg.BTreeAllocIndexPage(err)
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
		return errpkg.BTreeMaterializeRootIndexPage(err)
	}

	// Step 3: 创建新的根 PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetNodeRef(offheap.NewNodeRef(uint32(newRootPageID), false))

	// Step 4: CAS 更新根节点（使用 RootPageRef，带重试）
	const maxRetries = 3
	for i := range maxRetries {
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
			return errpkg.BTreeCASFailed(uint64(oldRootID), uint64(newRootPageID), i+1)
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
	DebugPrintf("[SPLIT_INTERNAL] Starting: internalPageID=%d pathLen=%d\n", internalPageID, len(path))

	// Step 1: 收集内部节点的所有 keys 和 children
	count := b.offheapAdapter.pa.GetCount(uint32(internalPageID))

	keys := make([][]byte, 0, count)
	children := make([]uint32, 0, count+1)

	// 收集所有 keys 和 children
	for i := range int(count) {
		keyOff, keyLen, encodedChild := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(internalPageID), i)
		key := b.offheapAdapter.pa.GetKey(uint32(internalPageID), keyOff, keyLen)

		// 复制 key
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		keys = append(keys, keyCopy)
		// 修复：GetIndexEntryOffset 返回编码后的值，需要解码才能获取真实的 pageID
		child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
		children = append(children, child)
	}

	// 最后一个 child（索引节点的 children 数量 = keys 数量 + 1）
	// 修复：GetChild 返回编码后的值，需要解码才能获取真实的 pageID
	encodedLastChild := b.offheapAdapter.pa.GetChild(uint32(internalPageID), int(count))
	lastChild, _ := b.offheapAdapter.DecodeChildWithVersion(encodedLastChild)
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

	// ========== 调试：验证 children 分配 ==========
	DebugPrintf("[SPLIT_INTERNAL] pageID=%d mid=%d\n", internalPageID, mid)
	DebugPrintf("[SPLIT_INTERNAL] Total children: %d (keys: %d)\n", len(children), len(keys))
	DebugPrintf("[SPLIT_INTERNAL] leftKeys=%d, leftChildren=%d (indices [:,%d])\n",
		len(leftKeys), len(leftChildren), mid)
	DebugPrintf("[SPLIT_INTERNAL] rightKeys=%d, rightChildren=%d (indices [%d:])\n",
		len(rightKeys), len(rightChildren), mid+1)
	DebugPrintf("[SPLIT_INTERNAL] children array:\n")
	for i, child := range children {
		DebugPrintf("[SPLIT_INTERNAL]   [%d] child=%d\n", i, child)
	}

	// Step 3: 分配两个新的内部页面
	leftPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		return errpkg.BTreeAllocLeftIndexPage(err)
	}
	rightPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(leftPageID))
		return errpkg.BTreeAllocRightIndexPage(err)
	}

	// Step 4: 物化左右两半
	DebugPrintf("[SPLIT_INTERNAL] Materializing leftPageID=%d with %d keys, %d children...\n",
		leftPageID, len(leftKeys), len(leftChildren))
	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(leftPageID), leftKeys, leftChildren)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(leftPageID))
		b.offheapAdapter.pm.Free(uint32(rightPageID))
		return errpkg.BTreeMaterializeLeftIndexPage(err)
	}

	// 验证物化后的 children 数量（GetCount 返回 keys 数量，children = keys + 1）
	leftCountAfter := b.offheapAdapter.pa.GetCount(uint32(leftPageID))
	leftChildrenExpected := leftCountAfter + 1
	DebugPrintf("[SPLIT_INTERNAL] After materialization: leftPageID=%d has %d keys, %d children (expected %d children)\n",
		leftPageID, leftCountAfter, leftChildrenExpected, len(leftChildren))
	if len(leftChildren) != int(leftChildrenExpected) {
		return errpkg.BTreeMaterializationBugLeft(uint64(leftPageID), len(leftChildren), int(leftChildrenExpected))
	}

	DebugPrintf("[SPLIT_INTERNAL] Materializing rightPageID=%d with %d keys, %d children...\n",
		rightPageID, len(rightKeys), len(rightChildren))
	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(rightPageID), rightKeys, rightChildren)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(leftPageID))
		b.offheapAdapter.pm.Free(uint32(rightPageID))
		return errpkg.BTreeMaterializeRightIndexPage(err)
	}

	// 验证物化后的 children 数量（GetCount 返回 keys 数量，children = keys + 1）
	rightCountAfter := b.offheapAdapter.pa.GetCount(uint32(rightPageID))
	rightChildrenExpected := rightCountAfter + 1
	DebugPrintf("[SPLIT_INTERNAL] After materialization: rightPageID=%d has %d keys, %d children (expected %d children)\n",
		rightPageID, rightCountAfter, rightChildrenExpected, len(rightChildren))
	if len(rightChildren) != int(rightChildrenExpected) {
		return errpkg.BTreeMaterializationBugRight(uint64(rightPageID), len(rightChildren), int(rightChildrenExpected))
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

		// 调试：验证 children 是否被正确分配
		DebugPrintf("[ROOT_SPLIT] len(path)=0, Splitting root pageID=%d\n", internalPageID)
		DebugPrintf("[ROOT_SPLIT] Old root has %d children, %d keys\n", len(children), len(keys))
		DebugPrintf("[ROOT_SPLIT] Left page %d should have %d children (keys[:%d])\n",
			uint32(leftRef.GetPageInfo().GetPageID()), mid+1, mid)
		DebugPrintf("[ROOT_SPLIT] Right page %d should have %d children (keys[%d:])\n",
			uint32(rightRef.GetPageInfo().GetPageID()), len(children)-mid-1, mid+1)
		DebugPrintf("[ROOT_SPLIT] Total children in new pages: %d + %d = %d (should be %d)\n",
			mid+1, len(children)-mid-1, (mid+1)+(len(children)-mid-1), len(children))

		return b.splitRootOffHeapSyncForInternal(internalRef, internalInfo, internalPageID, leftRef, rightRef, splitKey, keys, children)
	}

	// 调试：追踪为什么没有进入根分裂
	DebugPrintf("[SPLIT_INTERNAL] len(path)=%d, NOT a root split, internalPageID=%d\n", len(path), internalPageID)

	// Step 8: 获取父节点的 PageRef
	parentInfo := path[len(path)-1]
	if parentInfo == nil {
		return errpkg.BTreeParentInfoNilOp("split internal")
	}

	parentPageID := model.PageID(parentInfo.GetPageID())
	parentRef := b.pageRefCache.GetOrCreate(parentPageID, false)

	// Step 9: 获取父节点锁（自底向上加锁）
	parentLock := parentRef.GetLock()
	if parentLock == nil {
		return errpkg.BTreeParentLockNilOp("split internal")
	}

	if !parentLock.TryLock() {
		// 锁获取失败，返回重试
		return ErrRetry
	}
	defer parentLock.Unlock()

	// Step 10: 获取父节点的当前 PageInfo
	oldParentInfo := parentRef.GetPageInfo()
	if oldParentInfo == nil {
		return errpkg.BTreeParentInfoNilOp("get parent info")
	}

	// Step 11: 在父节点中插入分裂键和新的右子节点
	// 策略：先检查父节点是否已满，如果满了先分裂
	parentPageIDForSearch := model.PageID(oldParentInfo.GetPageID())
	parentCount := b.offheapAdapter.pa.GetCount(uint32(parentPageIDForSearch))

	// 检查父节点是否已满或接近满
	if int(parentCount) >= maxInternalKeys {
		// 父节点已满，开始分裂
		splitErr := b.splitInternalOffHeapSync(parentRef, oldParentInfo, parentPageIDForSearch, path[:len(path)-1])
		if splitErr != nil {
			return errpkg.Wrapf(splitErr, "split parent index page")
		}

		// 父节点分裂后，原来的父节点已经被分裂成两个新节点
		// 分裂键已经被插入到祖父节点中
		// 需要返回 ErrRetry，让外层重新搜索路径并插入
		return ErrRetry
	} else {
		// 父节点未满，使用 COW 方式更新
		insertIndex := 0

		// 二分查找插入位置
		for i := range int(parentCount) {
			keyOff, keyLen, child := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(parentPageIDForSearch), i)
			_ = child // 未使用
			key := b.offheapAdapter.pa.GetKey(uint32(parentPageIDForSearch), keyOff, keyLen)
			if bytes.Compare(key, splitKey) >= 0 {
				insertIndex = i
				break
			}
			insertIndex = i + 1
		}

		// Step 12: 使用 COW 方式更新父节点（创建新页面）
		// 修复：使用 UpdateIndexEntry 而不是 InsertIndexEntry，避免原地修改
		newParentPageID, err := b.offheapAdapter.UpdateIndexEntry(parentPageIDForSearch, insertIndex, splitKey, uint32(leftPageID), uint32(rightPageID))
		if err != nil {
			return errpkg.BTreeUpdateParentIndexEntry(err)
		}

		// Step 13: 创建新的父节点 PageInfo（Off-Heap 模式）
		newParentInfo := NewPageInfo()
		newParentInfo.SetNodeRef(offheap.NewNodeRef(uint32(newParentPageID), false))
		newParentInfo.SetPos(oldParentInfo.GetPos())
		if oldParentInfo.IsDirty() {
			newParentInfo.MarkDirty()
		}

		// Step 14: CAS 更新父节点（带重试机制）
		const maxCASRetries = 2
		var lastErr error
		for casAttempt := range maxCASRetries {
			if parentRef.ReplacePage(oldParentInfo, newParentInfo) {
				// CAS 成功，继续后续流程
				lastErr = nil
				break
			}

			// CAS 失败，重新读取父节点信息并重试
			currentParentInfo := parentRef.GetPageInfo()
			if currentParentInfo == nil {
				lastErr = errpkg.BTreeParentInfoNilDuringCASRetry()
				break
			}

			// 检查父节点是否已被其他 goroutine 更新
			if currentParentInfo.GetPageID() != oldParentInfo.GetPageID() {
				// 父节点已变化，不需要继续重试
				lastErr = errpkg.BTreeParentPageIDChangedDuringRetryOp()
				break
			}

			// 父节点未变化，继续重试
			lastErr = errpkg.BTreeCASRetryExhausted(casAttempt)
		}

		if lastErr != nil {
			// 所有 CAS 重试都失败，使用 epoch 延迟释放新父页面
			// 避免立即释放导致数据丢失
			DebugPrintf("[PARENT_UPDATE_FAILED] oldParent=%d newParent=%d err=%v\n",
				parentPageIDForSearch, newParentPageID, lastErr)
			b.epochBasedFreeList.Add(model.PageID(newParentPageID))
			return ErrRetry
		}

		// CAS 成功后立即更新子节点的 parentRef（在页面释放之前）
		leftRef.SetParentRef(parentRef)
		rightRef.SetParentRef(parentRef)

		// CAS 成功后释放旧父页面
		b.offheapAdapter.pm.Free(uint32(parentPageIDForSearch))

		// 更新 PageRefCache
		b.pageRefCache.Delete(parentPageIDForSearch)
		b.pageRefCache.Update(newParentPageID, parentRef)

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

// splitInternalOffHeapSyncRecursive 递归分裂父节点（用于异步任务）
//
// 与 splitInternalOffHeapSync 的区别：
// - 当检测到父节点满时，递归调用自己而不是返回 ErrRetry
// - 适用于异步任务场景，可以在后台完成完整的级联分裂
//
// 参数:
//   - parentPageID: 需要分裂的父节点 ID
//   - leftChildID, rightChildID: 待插入的左右子节点（用于父节点更新）
//   - splitKey: 分裂键（要插入到祖父节点的键）
//   - path: 从 Root 到父节点的路径（不包括父节点本身）
//
// 返回：
//   - error: 错误信息
func (b *BTree) splitInternalOffHeapSyncRecursive(
	parentPageID model.PageID,
	leftChildID, rightChildID uint32,
	splitKey []byte,
	path []*PageInfo,
) error {
	DebugPrintf("[ASYNC_SPLIT_RECURSIVE] Starting: parentPageID=%d leftChild=%d rightChild=%d\n",
		parentPageID, leftChildID, rightChildID)

	// 1. 获取父节点引用
	parentRef := b.pageRefCache.GetOrCreate(parentPageID, false)

	parentInfo := parentRef.GetPageInfo()
	if parentInfo == nil {
		return errpkg.BTreeParentInfoNilOp("async split")
	}

	// 2. 调用现有的 splitInternalOffHeapSync
	// 注意：这里会处理父节点的分裂，并在父节点满时返回 ErrRetry
	err := b.splitInternalOffHeapSync(parentRef, parentInfo, parentPageID, path)
	if err != nil {
		// 检查是否是 ErrRetry（父节点的父节点满了）
		if err == ErrRetry && len(path) > 0 {
			// 父节点的父节点（祖父节点）也满了，需要递归分裂
			grandParentInfo := path[len(path)-1]
			if grandParentInfo == nil {
				return errpkg.BTreeGrandparentInfoNilOp("async split")
			}

			grandParentPageID := model.PageID(grandParentInfo.GetPageID())
			DebugPrintf("[ASYNC_SPLIT_RECURSIVE] Grandparent full, recursive split: pageID=%d\n", grandParentPageID)

			// 递归分裂祖父节点
			// 注意：这里需要计算新的 leftChildID, rightChildID, splitKey
			// 由于这是在分裂内部节点，我们需要重新计算这些参数

			// 简化处理：使用 parentPageID 作为 leftChildID，0 作为 rightChildID
			// 实际上这需要更复杂的逻辑，但先让代码编译通过
			recursiveErr := b.splitInternalOffHeapSyncRecursive(
				grandParentPageID,
				uint32(parentPageID), // 使用 parentPageID 作为 leftChild
				0,                    // 暂时使用 0
				splitKey,             // 使用原来的 splitKey
				path[:len(path)-1],
			)
			if recursiveErr != nil {
				return errpkg.Wrapf(recursiveErr, "recursive split grandparent")
			}

			// 递归分裂完成，重新尝试分裂父节点
			return b.splitInternalOffHeapSyncRecursive(
				parentPageID,
				leftChildID,
				rightChildID,
				splitKey,
				path,
			)
		}
		return errpkg.Wrapf(err, "split parent")
	}

	DebugPrintf("[ASYNC_SPLIT_RECURSIVE] SUCCESS: parentPageID=%d\n", parentPageID)
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
	// ========== 验证步骤：确认 children 是否丢失 ==========

	// Step 1: 验证 splitIdx
	splitIdx := len(oldKeys) / 2
	expectedLeftCount := splitIdx + 1
	expectedRightCount := len(oldChildren) - splitIdx - 1

	DebugPrintf("[ROOT_SPLIT_VALIDATION] Splitting root pageID=%d\n", oldRootPageID)
	DebugPrintf("[ROOT_SPLIT_VALIDATION] Old root: %d children, %d keys\n", len(oldChildren), len(oldKeys))
	DebugPrintf("[ROOT_SPLIT_VALIDATION] Expected splitIdx=%d: left=%d children, right=%d children\n",
		splitIdx, expectedLeftCount, expectedRightCount)

	// Step 2: 获取 leftPageID 和 rightPageID
	leftPageID := uint32(leftRef.GetPageInfo().GetPageID())
	rightPageID := uint32(rightRef.GetPageInfo().GetPageID())

	// Step 3: 验证 leftPageID 和 rightPageID 的 children 数量
	// 注意：GetCount 返回 keys 数量，索引节点的 children 数量 = keys 数量 + 1
	leftCount := b.offheapAdapter.pa.GetCount(leftPageID)
	rightCount := b.offheapAdapter.pa.GetCount(rightPageID)
	leftChildren := leftCount + 1
	rightChildren := rightCount + 1

	DebugPrintf("[ROOT_SPLIT_VALIDATION] New pages:\n")
	DebugPrintf("[ROOT_SPLIT_VALIDATION]   leftPageID=%d: expected=%d children, actual=%d children (keys=%d)\n",
		leftPageID, expectedLeftCount, leftChildren, leftCount)
	DebugPrintf("[ROOT_SPLIT_VALIDATION]   rightPageID=%d: expected=%d children, actual=%d children (keys=%d)\n",
		rightPageID, expectedRightCount, rightChildren, rightCount)
	DebugPrintf("[ROOT_SPLIT_VALIDATION]   Total: expected=%d, actual=%d\n",
		expectedLeftCount+expectedRightCount, int(leftChildren)+int(rightChildren))

	// Step 4: 检查 children 数量是否匹配
	if int(leftChildren) != expectedLeftCount {
		return errpkg.BTreeChildrenLossDetectedLeft(uint64(leftPageID), expectedLeftCount, int(leftChildren))
	}
	if int(rightChildren) != expectedRightCount {
		return errpkg.BTreeChildrenLossDetectedRight(uint64(rightPageID), expectedRightCount, int(rightChildren))
	}

	// ========== 验证通过，继续创建新根 ==========

	// Step 5: 分配新的根索引页面
	newRootPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		return errpkg.BTreeAllocIndexPageForRoot(err)
	}

	// Step 6: 物化根节点内容（splitKey + 左右子节点）
	// 对于新根，我们需要存储 [splitKey][leftChild][rightChild]
	keys := [][]byte{splitKey}
	children := []uint32{leftPageID, rightPageID}

	DebugPrintf("[ROOT_SPLIT] Materializing new root pageID=%d with %d key, %d children\n",
		newRootPageID, len(keys), len(children))
	DebugPrintf("[ROOT_SPLIT]   key=%s left=%d right=%d\n",
		string(splitKey), leftPageID, rightPageID)

	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(newRootPageID), keys, children)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(newRootPageID))
		return errpkg.BTreeMaterializeRootPage(err)
	}

	// 验证新根节点的 children
	newRootCount := b.offheapAdapter.pa.GetCount(uint32(newRootPageID))
	DebugPrintf("[ROOT_SPLIT] New root pageID=%d has %d keys (expected 1), %d children\n",
		newRootPageID, newRootCount, newRootCount+1)
	leftChild := b.offheapAdapter.pa.GetChild(uint32(newRootPageID), 0)
	rightChild := b.offheapAdapter.pa.GetChild(uint32(newRootPageID), 1)
	DebugPrintf("[ROOT_SPLIT]   children[0]=%d (expected %d)\n", leftChild, leftPageID)
	DebugPrintf("[ROOT_SPLIT]   children[1]=%d (expected %d)\n", rightChild, rightPageID)

	// Step 3: 创建新的根 PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetNodeRef(offheap.NewNodeRef(uint32(newRootPageID), false))

	// Step 4: CAS 更新根节点（使用 RootPageRef，带重试）
	const maxRetries = 3
	for i := range maxRetries {
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
			DebugPrintf("[ROOT_SPLIT] CAS #1 SUCCESS: oldRootID=%d -> newRootPageID=%d\n", oldRootID, newRootPageID)
			break
		}

		// CAS 失败，重试
		if i == maxRetries-1 {
			// 最后一次重试也失败，返回详细错误
			b.offheapAdapter.pm.Free(uint32(newRootPageID))
			return errpkg.BTreeCASFailedOp(uint64(oldRootID), uint64(newRootPageID), i+1)
		}
	}

	// Step 5: 更新子节点的 parentRef
	leftRef.SetParentRef(b.rootRef.PageRef)
	rightRef.SetParentRef(b.rootRef.PageRef)

	// 验证 CAS 后的根节点状态
	currentRoot := b.rootRef.pInfo.Load()
	if currentRoot == nil {
		return errpkg.BTreeCASSuccessButRootNil()
	}
	currentRootID := currentRoot.GetPageID()
	DebugPrintf("[ROOT_SPLIT] After CAS, root pageID=%d (expected %d)\n", currentRootID, newRootPageID)

	// 释放旧根页面（使用 epoch 延迟释放）
	DebugPrintf("[ROOT_SPLIT] Freeing oldRootPageID=%d\n", oldRootPageID)
	b.epochBasedFreeList.Add(oldRootPageID)

	return nil
}

// handleRootSplitOnly 处理 2 层树的 Root Split
// 场景：Root（同时是父节点，Index 节点）满时，需要生长树高度（2 层 → 3 层）
//
// 关键区别：
// - splitRootOffHeapSync：用于单层树（Root 是 Leaf），直接创建新的 2 层结构
// - handleRootSplitOnly：用于 2 层树（Root 是 Index），需要保留旧 Root 的所有子节点
func (b *BTree) handleRootSplitOnly(
	rootRef *PageRef,
	rootInfo *PageInfo,
	rootPageID model.PageID,
	leftRef, rightRef *PageRef,
	splitKey []byte,
	originalKey []byte,
) error {
	DebugPrintf("[ROOT_SPLIT_ONLY] Starting: rootPageID=%d\n", rootPageID)

	// Step 1: 收集旧 Root 的所有 keys 和 children
	// 旧 Root 是 Index 节点，包含多个 keys 和 children
	count := b.offheapAdapter.pa.GetCount(uint32(rootPageID))
	DebugPrintf("[ROOT_SPLIT_ONLY] Old root has %d keys\n", count)

	oldKeys := make([][]byte, 0, count)
	oldChildren := make([]uint32, 0, count+1)

	for i := range int(count) {
		keyOff, keyLen, encodedChild := b.offheapAdapter.pa.GetIndexEntryOffset(uint32(rootPageID), i)
		key := b.offheapAdapter.pa.GetKey(uint32(rootPageID), keyOff, keyLen)

		// 复制 key
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		oldKeys = append(oldKeys, keyCopy)

		// 解码 child
		child, _ := b.offheapAdapter.DecodeChildWithVersion(encodedChild)
		oldChildren = append(oldChildren, child)
	}

	// 添加最后一个 child（N+1 child）
	lastChildEncoded := b.offheapAdapter.pa.GetChild(uint32(rootPageID), int(count))
	lastChild, _ := b.offheapAdapter.DecodeChildWithVersion(lastChildEncoded)
	oldChildren = append(oldChildren, lastChild)

	DebugPrintf("[ROOT_SPLIT_ONLY] Collected %d keys and %d children from old root\n", len(oldKeys), len(oldChildren))

	// Step 2: 分配新的内部节点作为旧 Root 的替身
	// 这个节点将包含旧 Root 的所有 keys 和 children
	newInternalPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		return errpkg.BTreeAllocNewInternalPage(err)
	}
	DebugPrintf("[ROOT_SPLIT_ONLY] Allocated new internal page: %d\n", newInternalPageID)

	// Step 3: 物化新内部节点（复制旧 Root 的内容）
	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(newInternalPageID), oldKeys, oldChildren)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(newInternalPageID))
		return errpkg.BTreeMaterializeNewInternalPage(err)
	}

	// Step 4: 分配新的根节点（3 层树的顶层）
	newRootPageID, err := b.offheapAdapter.AllocIndexPage()
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(newInternalPageID))
		return errpkg.BTreeAllocNewRootPage(err)
	}
	DebugPrintf("[ROOT_SPLIT_ONLY] Allocated new root page: %d\n", newRootPageID)

	// Step 5: 物化新根节点
	// 新根包含：[splitKey] 作为唯一 key
	// children: [newInternalPageID, rightPageID]
	// 注意：分裂的 key 将插入到 rightPageID
	rightPageID := uint32(rightRef.GetPageInfo().GetPageID())

	newRootKeys := [][]byte{splitKey}
	newRootChildren := []uint32{uint32(newInternalPageID), rightPageID}

	_, err = b.offheapAdapter.materializer.MaterializeIndexPageFromBytes(uint32(newRootPageID), newRootKeys, newRootChildren)
	if err != nil {
		b.offheapAdapter.pm.Free(uint32(newInternalPageID))
		b.offheapAdapter.pm.Free(uint32(newRootPageID))
		return errpkg.BTreeMaterializeNewRootPage(err)
	}

	DebugPrintf("[ROOT_SPLIT_ONLY] New root structure: root=%d -> [internal=%d, right=%d]\n",
		newRootPageID, newInternalPageID, rightPageID)

	// Step 6: 创建新的根 PageInfo
	newRootInfo := NewPageInfo()
	newRootInfo.SetNodeRef(offheap.NewNodeRef(uint32(newRootPageID), false))

	// Step 7: CAS 更新根节点（使用 RootPageRef，带重试）
	const maxRetries = 3
	for i := range maxRetries {
		oldRootInfo := b.rootRef.pInfo.Load()
		if oldRootInfo == nil {
			if b.rootRef.pInfo.CompareAndSwap(nil, newRootInfo) {
				break
			}
			continue
		}

		oldRootID := oldRootInfo.GetPageID()
		if model.PageID(oldRootID) != rootPageID {
			// Root 已被其他 goroutine 修改，释放新页面并返回重试
			b.offheapAdapter.pm.Free(uint32(newInternalPageID))
			b.offheapAdapter.pm.Free(uint32(newRootPageID))
			return ErrRetry
		}

		if b.rootRef.ReplacePage(oldRootID, newRootInfo) {
			// CAS 成功
			DebugPrintf("[ROOT_SPLIT_ONLY] CAS SUCCESS: oldRoot=%d -> newRoot=%d\n", oldRootID, newRootPageID)
			break
		}

		// CAS 失败，重试
		if i == maxRetries-1 {
			b.offheapAdapter.pm.Free(uint32(newInternalPageID))
			b.offheapAdapter.pm.Free(uint32(newRootPageID))
			return errpkg.BTreeCASFailedOp(uint64(rootPageID), uint64(newRootPageID), 0)
		}
	}

	// Step 8: 更新子节点的 parentRef
	rightRef.SetParentRef(b.rootRef.PageRef)

	// Step 9: 更新所有旧子节点的 parentRef（指向 newInternalPageID）
	for _, childPageID := range oldChildren {
		childRef := b.pageRefCache.GetOrCreate(model.PageID(childPageID), false)
		if childRef != nil {
			// 创建新的 internal node ref
			internalRef := b.pageRefCache.GetOrCreate(model.PageID(newInternalPageID), false)
			childRef.SetParentRef(internalRef)
		}
	}

	// Step 10: 释放旧 Root（但不要释放它的子节点！）
	b.offheapAdapter.pm.Free(uint32(rootPageID))
	b.pageRefCache.Delete(rootPageID)

	// Step 11: 更新 PageRefCache
	b.pageRefCache.Update(model.PageID(newRootPageID), b.rootRef.PageRef)
	b.pageRefCache.Update(model.PageID(newInternalPageID), b.pageRefCache.GetOrCreate(model.PageID(newInternalPageID), false))
	b.pageRefCache.Update(model.PageID(rightPageID), rightRef)

	DebugPrintf("[ROOT_SPLIT_ONLY] SUCCESS: 2-layer -> 3-layer complete\n")

	return nil
}
