# DebugPrintf 审计报告
统计时间: 2026-03-28T09:41:27+08:00

## 概要

- **调用总数**: 154
- **涉及文件**: 7
- **调试标签**: 27
- **当前状态**: 函数体已清空为 no-op，调用仍在代码中

## 按文件统计

| 文件 | 调用数 | 占比 |
|------|--------|------|
| `leaf_lock_set.go` | 98 | 63.6% |
| `offheap_adapter.go` | 41 | 26.6% |
| `search_path.go` | 7 | 4.5% |
| `btree.go` | 5 | 3.2% |
| `parent_split_item.go` | 3 | 1.9% |
| **总计** | **154** | **100%** |

## 按调试标签统计

| 标签 | 调用数 | 说明 |
|------|--------|------|
| `SPLIT_DEBUG` | 24 | offheap 页面分裂调试 |
| `HANDLE_SPLIT` | 21 | 叶子分裂主流程 |
| `FALLBACK` | 14 | 活锁检测降级路径 |
| `ROOT_SPLIT` | 13 | 根节点分裂 |
| `SPLIT_INTERNAL` | 12 | 内部节点分裂 |
| `ROOT_SPLIT_ONLY` | 8 | 仅根节点分裂(2层→3层) |
| `UPDATE_GRANDPARENT` | 7 | 祖父节点更新 |
| `ROOT_SPLIT_VALIDATION` | 7 | 根分裂验证 |
| `INSERT_DEBUG` | 7 | offheap 插入调试 |
| `GET_OFFHEAP` | 4 | offheap 读取调试 |
| `UPDATE_DEBUG` | 4 | offheap 更新调试 |
| `ROOT_SPLIT_POST_INSERT` | 3 | 根分裂后插入 |
| `PARENT_UPDATE` | 3 | 父节点更新 |
| `POST_SPLIT_INSERT` | 3 | 分裂后插入 |
| `ASYNC_SPLIT_RECURSIVE` | 3 | 异步递归分裂 |
| `ASYNC_PARENT_SPLIT` | 3 | 异步父节点分裂任务 |
| `EPOCH_ADVANCE` | 2 | epoch 推进 |
| `PARENT_UPDATE_FAILED` | 2 | 父节点更新失败 |
| `PARENT_ID_CHANGED` | 2 | 父节点ID变更 |
| `CIRCULAR_REF` | 2 | 循环引用检测 |
| `HAS_CYCLE` | 2 | 环检测 |
| `EPOCH_ADD` | 1 | epoch 页面释放注册 |
| `EPOCH_DELAYED` | 1 | epoch 延迟释放 |
| `EPOCH_DELAYED_ADVANCE` | 1 | epoch 延迟释放推进 |
| `SPLIT_NEXT` | 1 | 链表指针更新 |
| `STALE_REF` | 1 | 过期引用检测 |
| `SEARCH_PATH` | 1 | 搜索路径 |

## 热点分析

### 分裂相关 (最密集)

- 分裂路径标签: 125 处 (81%)
- `leaf_lock_set.go` 一个文件占 98 处

### offheap 适配层

- `offheap_adapter.go` 占 41 处

### 其他

- 搜索路径/epoch/其他: -12 处

## 详细列表

```
btree.go:274:	DebugPrintf("[EPOCH_ADD] epoch=%d pageID=%d caller=%s pending_count=%d\n",
btree.go:300:			DebugPrintf("[EPOCH_DELAYED] epoch=%d pageID=%d\n", epochToDelayed, pid)
btree.go:312:		DebugPrintf("[EPOCH_ADVANCE] old=%d new=%d freeing_epoch=%d pages_to_free=%d\n",
btree.go:318:			DebugPrintf("[EPOCH_DELAYED_ADVANCE] moved=%d pages from delayed to available\n", moved)
btree.go:322:		DebugPrintf("[EPOCH_ADVANCE] old=%d new=%d pages_to_free=0 (waiting for epoch 3)\n",
debug.go:8:func DebugPrintf(format string, args ...any) {}
leaf_lock_set.go:345:		DebugPrintf("[HANDLE_SPLIT] WARNING: leafPageID=%d is INDEX node, calling splitInternalOffHeapSync instead\n", leafPageID)
leaf_lock_set.go:374:		DebugPrintf("[HANDLE_SPLIT] ============================================\n")
leaf_lock_set.go:375:		DebugPrintf("[HANDLE_SPLIT] Livelock detected: pageID=%d pendingCount=%d, using fallback strategy\n", leafPageID, pendingCount)
leaf_lock_set.go:376:		DebugPrintf("[HANDLE_SPLIT] ============================================\n")
leaf_lock_set.go:392:			DebugPrintf("[HANDLE_SPLIT] Fallback page also full, returning ErrRetry\n")
leaf_lock_set.go:397:		DebugPrintf("[HANDLE_SPLIT] Fallback SUCCESS: key=%s inserted into new pageID=%d\n", string(key), newPageID)
leaf_lock_set.go:417:			DebugPrintf("[FALLBACK] len(path)=%d, attempting to update parent\n", len(path))
leaf_lock_set.go:421:				DebugPrintf("[FALLBACK] parent info is nil\n")
leaf_lock_set.go:431:				DebugPrintf("[FALLBACK] parent lock is nil\n")
leaf_lock_set.go:437:				DebugPrintf("[FALLBACK] parent lock trylock failed\n")
leaf_lock_set.go:445:				DebugPrintf("[FALLBACK] parent page info is nil\n")
leaf_lock_set.go:456:				DebugPrintf("[FALLBACK] parent page FULL (count=%d), calling splitInternalOffHeapSync\n", parentCount)
leaf_lock_set.go:461:					DebugPrintf("[FALLBACK] splitInternalOffHeapSync FAILED: %v\n", splitErr)
leaf_lock_set.go:470:				DebugPrintf("[FALLBACK] parent split SUCCESS, freeing new page and returning ErrRetry\n")
leaf_lock_set.go:490:				DebugPrintf("[FALLBACK] child pageID %d not found in parent %d (race condition), returning ErrRetry\n",
leaf_lock_set.go:495:			DebugPrintf("[FALLBACK] parentPageID=%d count=%d insertIndex=%d leafPageID=%d newPageID=%d\n",
leaf_lock_set.go:502:				DebugPrintf("[FALLBACK] UpdateIndexEntry failed: %v\n", err)
leaf_lock_set.go:517:				DebugPrintf("[FALLBACK] parent CAS failed, adding to epoch delay list\n")
leaf_lock_set.go:533:			DebugPrintf("[FALLBACK] parent update SUCCESS: oldParent=%d newParent=%d\n",
leaf_lock_set.go:536:			DebugPrintf("[FALLBACK] len(path)=%d, no parent to update\n", len(path))
leaf_lock_set.go:544:	DebugPrintf("[HANDLE_SPLIT] pageID=%d isLeaf=%v count=%d pendingCount=%d key=%s\n",
leaf_lock_set.go:553:		DebugPrintf("[HANDLE_SPLIT] pageID=%d is INDEX node, calling splitInternalOffHeapSync\n", leafPageID)
leaf_lock_set.go:566:	DebugPrintf("[HANDLE_SPLIT] Step 1: Splitting leaf pageID=%d\n", leafPageID)
leaf_lock_set.go:573:		DebugPrintf("[HANDLE_SPLIT] SplitOffHeapLeafPage FAILED: %v\n", err)
leaf_lock_set.go:576:	DebugPrintf("[HANDLE_SPLIT] SplitOffHeapLeafPage SUCCESS: leftPageID=%d rightPageID=%d splitKey=%s\n",
leaf_lock_set.go:585:		DebugPrintf("[HANDLE_SPLIT] len(path)=%d, ROOT SPLIT scenario\n", len(path))
leaf_lock_set.go:615:			DebugPrintf("[ROOT_SPLIT_POST_INSERT] FAILED to insert key=%s into pageID=%d: %v\n", string(key), targetPageID, err)
leaf_lock_set.go:620:			DebugPrintf("[ROOT_SPLIT_POST_INSERT] key=%s still needs split after inserting to pageID=%d\n", string(key), targetPageID)
leaf_lock_set.go:623:		DebugPrintf("[ROOT_SPLIT_POST_INSERT] SUCCESS: key=%s inserted into pageID=%d\n", string(key), targetPageID)
leaf_lock_set.go:714:		DebugPrintf("[HANDLE_SPLIT] Child %d not found in parent %d (count=%d)\n",
leaf_lock_set.go:740:				DebugPrintf("[HANDLE_SPLIT] 2-layer tree root split: rootPageID=%d\n", currentParentPageID)
leaf_lock_set.go:757:					DebugPrintf("[HANDLE_SPLIT] handleRootSplitOnly FAILED: %v\n", rootSplitErr)
leaf_lock_set.go:762:				DebugPrintf("[HANDLE_SPLIT] handleRootSplitOnly SUCCESS, returning ErrRetry\n")
leaf_lock_set.go:778:	DebugPrintf("[PARENT_UPDATE] Starting: oldParent=%d newParent=%d\n",
leaf_lock_set.go:815:		DebugPrintf("[PARENT_UPDATE_FAILED] oldParent=%d newParent=%d err=%v\n",
leaf_lock_set.go:824:		DebugPrintf("[PARENT_UPDATE] CIRCULAR REFERENCE DETECTED after split: newParentPageID=%d\n", newParentPageID)
leaf_lock_set.go:835:		DebugPrintf("[PARENT_UPDATE] Integrity check failed: %v\n", err)
leaf_lock_set.go:869:		DebugPrintf("[PARENT_ID_CHANGED] oldParent=%d newParent=%d pathLen=%d\n", oldParentPageID, currentParentPageID, len(path))
leaf_lock_set.go:896:				DebugPrintf("[UPDATE_GRANDPARENT] oldParent=%d not found in grandparent %d, grandParentCount=%d\n",
leaf_lock_set.go:902:					DebugPrintf("[UPDATE_GRANDPARENT]   child[%d]=%d\n", i, child)
leaf_lock_set.go:907:			DebugPrintf("[UPDATE_GRANDPARENT] grandParent=%d index=%d oldChild=%d newChild=%d grandParentCount=%d\n",
leaf_lock_set.go:935:						DebugPrintf("[UPDATE_GRANDPARENT] grandparent page full, triggering split: grandParent=%d err=%v\n",
leaf_lock_set.go:983:				DebugPrintf("[UPDATE_GRANDPARENT] updating extraChild: grandParent=%d oldExtraChild=%d newExtraChild=%d\n",
leaf_lock_set.go:1019:						DebugPrintf("[UPDATE_GRANDPARENT] grandparent page full (extraChild), triggering split: grandParent=%d err=%v\n",
leaf_lock_set.go:1064:				DebugPrintf("[UPDATE_GRANDPARENT] successfully updated extraChild: grandParent=%d -> newGrandParent=%d\n",
leaf_lock_set.go:1070:			DebugPrintf("[PARENT_ID_CHANGED] parent is root, no grandparent update needed\n")
leaf_lock_set.go:1092:		DebugPrintf("[POST_SPLIT_INSERT] FAILED to insert key=%s into pageID=%d: %v\n", string(key), targetPageID, err)
leaf_lock_set.go:1097:		DebugPrintf("[POST_SPLIT_INSERT] key=%s still needs split after inserting to pageID=%d\n", string(key), targetPageID)
leaf_lock_set.go:1100:	DebugPrintf("[POST_SPLIT_INSERT] SUCCESS: key=%s inserted into pageID=%d\n", string(key), targetPageID)
leaf_lock_set.go:1104:		DebugPrintf("[HANDLE_SPLIT] parent full, triggering sync split: parentPageID=%d count=%d\n",
leaf_lock_set.go:1111:		DebugPrintf("[HANDLE_SPLIT] using sync split to ensure index updated\n")
leaf_lock_set.go:1112:		DebugPrintf("[HANDLE_SPLIT] calling splitInternalOffHeapSync: parentPageID=%d, pathLen=%d\n",
leaf_lock_set.go:1117:			DebugPrintf("[HANDLE_SPLIT] splitInternalOffHeapSync FAILED: %v\n", err)
leaf_lock_set.go:1121:		DebugPrintf("[HANDLE_SPLIT] splitInternalOffHeapSync SUCCESS, returning ErrRetry\n")
leaf_lock_set.go:1233:	DebugPrintf("[SPLIT_INTERNAL] Starting: internalPageID=%d pathLen=%d\n", internalPageID, len(path))
leaf_lock_set.go:1279:	DebugPrintf("[SPLIT_INTERNAL] pageID=%d mid=%d\n", internalPageID, mid)
leaf_lock_set.go:1280:	DebugPrintf("[SPLIT_INTERNAL] Total children: %d (keys: %d)\n", len(children), len(keys))
leaf_lock_set.go:1281:	DebugPrintf("[SPLIT_INTERNAL] leftKeys=%d, leftChildren=%d (indices [:,%d])\n",
leaf_lock_set.go:1283:	DebugPrintf("[SPLIT_INTERNAL] rightKeys=%d, rightChildren=%d (indices [%d:])\n",
leaf_lock_set.go:1285:	DebugPrintf("[SPLIT_INTERNAL] children array:\n")
leaf_lock_set.go:1287:		DebugPrintf("[SPLIT_INTERNAL]   [%d] child=%d\n", i, child)
leaf_lock_set.go:1302:	DebugPrintf("[SPLIT_INTERNAL] Materializing leftPageID=%d with %d keys, %d children...\n",
leaf_lock_set.go:1314:	DebugPrintf("[SPLIT_INTERNAL] After materialization: leftPageID=%d has %d keys, %d children (expected %d children)\n",
leaf_lock_set.go:1320:	DebugPrintf("[SPLIT_INTERNAL] Materializing rightPageID=%d with %d keys, %d children...\n",
leaf_lock_set.go:1332:	DebugPrintf("[SPLIT_INTERNAL] After materialization: rightPageID=%d has %d keys, %d children (expected %d children)\n",
leaf_lock_set.go:1352:		DebugPrintf("[ROOT_SPLIT] len(path)=0, Splitting root pageID=%d\n", internalPageID)
leaf_lock_set.go:1353:		DebugPrintf("[ROOT_SPLIT] Old root has %d children, %d keys\n", len(children), len(keys))
leaf_lock_set.go:1354:		DebugPrintf("[ROOT_SPLIT] Left page %d should have %d children (keys[:%d])\n",
leaf_lock_set.go:1356:		DebugPrintf("[ROOT_SPLIT] Right page %d should have %d children (keys[%d:])\n",
leaf_lock_set.go:1358:		DebugPrintf("[ROOT_SPLIT] Total children in new pages: %d + %d = %d (should be %d)\n",
leaf_lock_set.go:1365:	DebugPrintf("[SPLIT_INTERNAL] len(path)=%d, NOT a root split, internalPageID=%d\n", len(path), internalPageID)
leaf_lock_set.go:1473:			DebugPrintf("[PARENT_UPDATE_FAILED] oldParent=%d newParent=%d err=%v\n",
leaf_lock_set.go:1526:	DebugPrintf("[ASYNC_SPLIT_RECURSIVE] Starting: parentPageID=%d leftChild=%d rightChild=%d\n",
leaf_lock_set.go:1550:			DebugPrintf("[ASYNC_SPLIT_RECURSIVE] Grandparent full, recursive split: pageID=%d\n", grandParentPageID)
leaf_lock_set.go:1581:	DebugPrintf("[ASYNC_SPLIT_RECURSIVE] SUCCESS: parentPageID=%d\n", parentPageID)
leaf_lock_set.go:1613:	DebugPrintf("[ROOT_SPLIT_VALIDATION] Splitting root pageID=%d\n", oldRootPageID)
leaf_lock_set.go:1614:	DebugPrintf("[ROOT_SPLIT_VALIDATION] Old root: %d children, %d keys\n", len(oldChildren), len(oldKeys))
leaf_lock_set.go:1615:	DebugPrintf("[ROOT_SPLIT_VALIDATION] Expected splitIdx=%d: left=%d children, right=%d children\n",
leaf_lock_set.go:1629:	DebugPrintf("[ROOT_SPLIT_VALIDATION] New pages:\n")
leaf_lock_set.go:1630:	DebugPrintf("[ROOT_SPLIT_VALIDATION]   leftPageID=%d: expected=%d children, actual=%d children (keys=%d)\n",
leaf_lock_set.go:1632:	DebugPrintf("[ROOT_SPLIT_VALIDATION]   rightPageID=%d: expected=%d children, actual=%d children (keys=%d)\n",
leaf_lock_set.go:1634:	DebugPrintf("[ROOT_SPLIT_VALIDATION]   Total: expected=%d, actual=%d\n",
leaf_lock_set.go:1658:	DebugPrintf("[ROOT_SPLIT] Materializing new root pageID=%d with %d key, %d children\n",
leaf_lock_set.go:1660:	DebugPrintf("[ROOT_SPLIT]   key=%s left=%d right=%d\n",
leaf_lock_set.go:1671:	DebugPrintf("[ROOT_SPLIT] New root pageID=%d has %d keys (expected 1), %d children\n",
leaf_lock_set.go:1675:	DebugPrintf("[ROOT_SPLIT]   children[0]=%d (expected %d)\n", leftChild, leftPageID)
leaf_lock_set.go:1676:	DebugPrintf("[ROOT_SPLIT]   children[1]=%d (expected %d)\n", rightChild, rightPageID)
leaf_lock_set.go:1698:			DebugPrintf("[ROOT_SPLIT] CAS #1 SUCCESS: oldRootID=%d -> newRootPageID=%d\n", oldRootID, newRootPageID)
leaf_lock_set.go:1720:	DebugPrintf("[ROOT_SPLIT] After CAS, root pageID=%d (expected %d)\n", currentRootID, newRootPageID)
leaf_lock_set.go:1723:	DebugPrintf("[ROOT_SPLIT] Freeing oldRootPageID=%d\n", oldRootPageID)
leaf_lock_set.go:1743:	DebugPrintf("[ROOT_SPLIT_ONLY] Starting: rootPageID=%d\n", rootPageID)
leaf_lock_set.go:1748:	DebugPrintf("[ROOT_SPLIT_ONLY] Old root has %d keys\n", count)
leaf_lock_set.go:1772:	DebugPrintf("[ROOT_SPLIT_ONLY] Collected %d keys and %d children from old root\n", len(oldKeys), len(oldChildren))
leaf_lock_set.go:1780:	DebugPrintf("[ROOT_SPLIT_ONLY] Allocated new internal page: %d\n", newInternalPageID)
leaf_lock_set.go:1795:	DebugPrintf("[ROOT_SPLIT_ONLY] Allocated new root page: %d\n", newRootPageID)
leaf_lock_set.go:1813:	DebugPrintf("[ROOT_SPLIT_ONLY] New root structure: root=%d -> [internal=%d, right=%d]\n",
leaf_lock_set.go:1841:			DebugPrintf("[ROOT_SPLIT_ONLY] CAS SUCCESS: oldRoot=%d -> newRoot=%d\n", oldRootID, newRootPageID)
leaf_lock_set.go:1875:	DebugPrintf("[ROOT_SPLIT_ONLY] SUCCESS: 2-layer -> 3-layer complete\n")
offheap_adapter.go:71:		DebugPrintf("[GET_OFFHEAP] key=%s pageID=%d idx=%d found=%v count=%d nextPage=%d\n",
offheap_adapter.go:79:			DebugPrintf("[GET_OFFHEAP] pageID=%d firstKey=%s lastKey=%s\n", pageID, string(firstKey), string(lastKey))
offheap_adapter.go:87:			DebugPrintf("[GET_OFFHEAP] key=%s NOT FOUND in page %d, trying nextPage=%d\n", string(key), pageID, nextPage)
offheap_adapter.go:91:				DebugPrintf("[GET_OFFHEAP] key=%s in nextPage=%d idx=%d found=%v\n", string(key), nextPage, nextIdx, nextFound)
offheap_adapter.go:127:		DebugPrintf("[INSERT_DEBUG] key=%s pageID=%d idx=%d found=%v count=%d (LINEAR SEARCH)\n", string(key), pageID, idx, found, count)
offheap_adapter.go:133:			DebugPrintf("[INSERT_DEBUG] pageID=%d firstKey=%s lastKey=%s\n", pageID, string(firstKey), string(lastKey))
offheap_adapter.go:140:			DebugPrintf("[INSERT_DEBUG] key=%s UPDATE -> newPageID=%d err=%v\n", string(key), newPageID, err)
offheap_adapter.go:150:			DebugPrintf("[INSERT_DEBUG] key=%s page FULL -> splitRequired=true\n", string(key))
offheap_adapter.go:159:		DebugPrintf("[INSERT_DEBUG] key=%s BEFORE InsertLeafEntry dataEnd=%d\n", string(key), dataEnd)
offheap_adapter.go:168:			DebugPrintf("[INSERT_DEBUG] key=%s INSERT SUCCESS count=%d->%d splitRequired=%v\n", string(key), idx, newCount, splitRequired)
offheap_adapter.go:175:		DebugPrintf("[INSERT_DEBUG] key=%s INSERT FAILED: %v\n", string(key), insertErr)
offheap_adapter.go:236:		DebugPrintf("[UPDATE_DEBUG] ========== UPDATE START pageID=%d idx=%d ==========\n", pageID, idx)
offheap_adapter.go:267:		DebugPrintf("[UPDATE_DEBUG] pageID=%d count=%d keys=%d\n", pageID, count, len(keys))
offheap_adapter.go:280:		DebugPrintf("[UPDATE_DEBUG] FREED pageID=%d, allocated newPageID=%d\n", pageID, newPageID)
offheap_adapter.go:291:		DebugPrintf("[UPDATE_DEBUG] ========== UPDATE END pageID=%d -> newPageID=%d ==========\n", pageID, newPageID)
offheap_adapter.go:582:		DebugPrintf("[SPLIT_DEBUG] ========== SPLIT START pageID=%d count=%d ==========\n", pageID, count)
offheap_adapter.go:585:		DebugPrintf("[SPLIT_DEBUG] pageID=%d ptr=%x\n", pageID, ptr)
offheap_adapter.go:606:				DebugPrintf("[SPLIT_DEBUG] pageID=530 FOUND key-06150 at index %d\n", i)
offheap_adapter.go:610:				DebugPrintf("[SPLIT_DEBUG] pageID=530 FOUND key-06151 at index %d\n", i)
offheap_adapter.go:618:				DebugPrintf("[SPLIT_DEBUG] pageID=533 FOUND key-06150 at index %d\n", i)
offheap_adapter.go:622:				DebugPrintf("[SPLIT_DEBUG] pageID=533 FOUND key-06151 at index %d\n", i)
offheap_adapter.go:636:			DebugPrintf("[SPLIT_DEBUG]   [%d] key=%s\n", i, string(key))
offheap_adapter.go:642:			DebugPrintf("[SPLIT_DEBUG] pageID=530 search result: found6150=%v found6151=%v\n", found6150, found6151)
offheap_adapter.go:645:			DebugPrintf("[SPLIT_DEBUG] pageID=533 search result: found6150=%v found6151=%v\n", found6150, found6151)
offheap_adapter.go:648:			DebugPrintf("[SPLIT_DEBUG]   ... total %d keys\n", count)
offheap_adapter.go:650:			DebugPrintf("[SPLIT_DEBUG]   ... total %d keys\n", count)
offheap_adapter.go:695:			DebugPrintf("[SPLIT_DEBUG] Trying 30%% split: mid=%d leftKeys=%d rightKeys=%d\n", mid, len(leftKeys), len(rightKeys))
offheap_adapter.go:697:				DebugPrintf("[SPLIT_DEBUG]   left[0]=%s left[-1]=%s\n", string(leftKeys[0]), string(leftKeys[len(leftKeys)-1]))
offheap_adapter.go:700:				DebugPrintf("[SPLIT_DEBUG]   right[0]=%s right[-1]=%s\n", string(rightKeys[0]), string(rightKeys[len(rightKeys)-1]))
offheap_adapter.go:711:				DebugPrintf("[SPLIT_DEBUG] 30%% split SUCCESS\n")
offheap_adapter.go:715:				DebugPrintf("[SPLIT_DEBUG] 30%% split FAILED: leftErr=%v rightErr=%v\n", leftErr, rightErr)
offheap_adapter.go:791:		DebugPrintf("[SPLIT_DEBUG] Final splitIdx=%d splitKey=%s\n", splitIdx, string(splitKey))
offheap_adapter.go:792:		DebugPrintf("[SPLIT_DEBUG]   leftKeys: %d [%s...%s]\n", splitIdx, string(keys[0]), string(keys[splitIdx-1]))
offheap_adapter.go:793:		DebugPrintf("[SPLIT_DEBUG]   rightKeys: %d [%s...%s]\n", len(keys[splitIdx:]), string(keys[splitIdx]), string(keys[len(keys)-1]))
offheap_adapter.go:805:		DebugPrintf("[SPLIT_DEBUG] Left page %d materialized OK\n", leftPageID)
offheap_adapter.go:817:		DebugPrintf("[SPLIT_DEBUG] Right page %d materialized OK\n", rightPageID)
offheap_adapter.go:818:		DebugPrintf("[SPLIT_DEBUG] ========== SPLIT END pageID=%d -> left=%d right=%d ==========\n", pageID, leftPageID, rightPageID)
offheap_adapter.go:821:			DebugPrintf("[SPLIT_DEBUG] *** WARNING: Circular reference detected! ***\n")
offheap_adapter.go:822:			DebugPrintf("[SPLIT_DEBUG] *** input=%d left=%d right=%d ***\n", pageID, leftPageID, rightPageID)
offheap_adapter.go:832:		DebugPrintf("[SPLIT_NEXT] before: pageID=%d left=%d right=%d oldPrev=%d oldNext=%d\n",
offheap_adapter.go:938:		DebugPrintf("[STALE_REF] parent=%d childIdx=%d childID=%d expectedVer=%d actualVer=%d\n",
parent_split_item.go:61:				DebugPrintf("[ASYNC_PARENT_SPLIT] Starting async split: parentPageID=%d\n", parentPageID)
parent_split_item.go:74:					DebugPrintf("[ASYNC_PARENT_SPLIT] FAILED: parentPageID=%d err=%v\n", parentPageID, err)
parent_split_item.go:78:				DebugPrintf("[ASYNC_PARENT_SPLIT] SUCCESS: parentPageID=%d\n", parentPageID)
search_path.go:228:			DebugPrintf("[SEARCH_PATH] Warning: childPageID %d near max limit (4095), parent=%d depth=%d\n",
search_path.go:252:			DebugPrintf("[CIRCULAR_REF] pageID=%d depth=%d\n", currentPageID, len(path))
search_path.go:253:			DebugPrintf("[CIRCULAR_REF] Path: ")
search_path.go:255:				DebugPrintf("%d ", p.GetPageID())
search_path.go:257:			DebugPrintf("\n")
search_path.go:331:			DebugPrintf("[HAS_CYCLE] Max depth exceeded at page %d\n", pid)
search_path.go:337:			DebugPrintf("[HAS_CYCLE] Cycle detected at page %d (depth %d)\n", pid, depth)
offheap/debug.go:8:func DebugPrintf(format string, args ...any) {}
```
