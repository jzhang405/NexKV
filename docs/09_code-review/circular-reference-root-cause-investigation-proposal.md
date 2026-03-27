# B-Tree 循环引用 Root Cause 深度调查提案

**文档类型**: Code Review - Root Cause Investigation
**创建日期**: 2026-03-27
**状态**: 待执行
**预估工期**: 2-3 周
**优先级**: 🔴 高

---

## 📋 执行摘要

### 背景

当前 B-Tree 并发写入测试 `TestSetWithLeafLock_Concurrent` 存在 **1% 的间歇性失败率**，错误信息为：
```
circular reference detected at page 4095 (path depth: 3)
```

尽管已实施多项优化（基于子节点 ID 定位、完整性验证、智能重试），成功率从 98.8% 提升至 **99.0%**，但 **剩余 1% 失败的根本原因尚未明确**。

### 问题分布

| 失败类型 | 失败次数 | 占比 | 特征 |
|---------|---------|------|------|
| **Page 4095 循环引用** | 7,897 | **98.7%** | depth 3-4，边界值 |
| **Page 3 根节点循环** | 113 | **1.3%** | depth 1，根分裂 |

### 调查目标

**核心目标**：找出剩余 1% 失败的**根本原因**（Root Cause），而非症状。

**具体目标**：
1. **Page 4095 的来源**：为什么 95% 的失败涉及此特定页面 ID？
2. **Page 3 循环的根因**：根分裂时为何形成循环引用？
3. **并发控制的有效性**：Epoch Based Reclamation 是否正确工作？

---

## 🔬 调查方向

### 方向 1：Page 4095 边界条件分析

#### 1.1 页面 ID 4095 的特殊性

**已知信息**：
- 4095 = 0xFFF（16 进制）
- 32 位最大 PageID = 0xFFFFFFFF
- 对于 6GB mmap：`total = 6GB / 4KB = 1,572,864` 页面
- 理论最大 PageID 应该是 1,572,863（而非 4095）

**疑问**：
1. **4095 是真实的分配页面 ID，还是某种错误/溢出值？**
2. **是否存在某种边界检查，导致 pageID 被截断或溢出？**
3. **4095 是否是某种特殊保留页面（如元数据页）？**

#### 1.2 调查步骤

##### Step 1：验证 PageManager 分配逻辑

**文件**: `internal/infrastructure/storage/btree/offheap/page_manager.go:99-109`

```go
func (pm *PageManager) Alloc() (uint32, error) {
	pageID := pm.nextPageID.Load()
	if pageID >= pm.total {
		return 0, fmt.Errorf("out of memory: no free pages available (total: %d, used: %d)",
			pm.total, pm.used.Load())
	}
	// 原子递增 nextPageID
	pm.nextPageID.Add(1)
	pm.used.Add(1)
	return pageID, nil
}
```

**需要验证**：
1. `pm.total` 的实际值是多少？
2. `pm.nextPageID` 是否可能达到或超过 4095？
3. 是否存在整数溢出或截断？

**实验方案**：
```go
// 在测试中添加调试日志
func (pm *PageManager) Alloc() (uint32, error) {
	pageID := pm.nextPageID.Load()
	if pageID >= pm.total {
		return 0, fmt.Errorf("out of memory: no free pages available (total: %d, used: %d)",
			pm.total, pm.used.Load())
	}

	// 调试：记录接近 4095 的分配
	if pageID > 4000 {
		log.Printf("[PAGE_ALLOC] pageID=%d total=%d used=%d", pageID, pm.total, pm.used.Load())
	}

	pm.nextPageID.Add(1)
	pm.used.Add(1)
	return pageID, nil
}
```

##### Step 2：追踪 Page 4095 的完整生命周期

**需要追踪**：
1. Page 4095 是何时被分配的？
2. Page 4095 被分配给哪个节点（Leaf / Internal）？
3. Page 4095 的父节点是谁？
4. Page 4095 的子节点有哪些？
5. Page 4095 是何时被释放的？
6. Page 4095 被释放后，页面 ID 是否被重新分配？

**实验方案**：
```go
// 在 PageManager 中添加追踪
type PageManager struct {
	// ... 现有字段 ...
	pageHistory map[uint32]*PageLifecycle // 页面生命周期追踪
}

type PageLifecycle struct {
	allocTime   time.Time
	allocCaller string
	freeTime    time.Time
	freeCaller  string
	reusedCount int
}

func (pm *PageManager) Alloc() (uint32, error) {
	// ... 现有分配逻辑 ...
	pageID := pm.nextPageID.Load()

	// 追踪 page 4095
	if pageID == 4095 || pageID > 4000 {
		pm.pageHistory[pageID] = &PageLifecycle{
			allocTime:   time.Now(),
			allocCaller: getCaller(),
		}
		log.Printf("[PAGE_4095] Allocated pageID=%d at %s", pageID, pm.pageHistory[pageID].allocCaller)
	}

	return pageID, nil
}
```

##### Step 3：分析循环引用形成的具体场景

**需要重构**：
1. 收集 100 次失败的完整调用栈
2. 分析每次失败时的树结构状态
3. 识别共同模式

**实验方案**：
```bash
# 修改测试，收集详细日志
go test -v -run TestSetWithLeafLock_Concurrent ./internal/infrastructure/storage/btree/ \
  -count 1000 2>&1 | tee /tmp/circular_ref_detailed.log

# 分析日志
grep "circular reference" /tmp/circular_ref_detailed.log | \
  grep "page 4095" | \
  awk -F'pageID=' '{print $2}' | \
  sort | uniq -c
```

---

### 方向 2：Page 3 根节点循环引用分析

#### 2.1 根分裂的并发控制

**已知信息**：
- Page 3 在 depth 1 就检测到循环
- 这意味着：`Root → Child → Root` 或 `Root → Child1 → Child2 → Child1`
- 根分裂使用 CAS 操作（`b.rootRef.ReplacePage`）

**疑问**：
1. **根分裂 CAS 是否足够强？**
2. **是否存在 CAS 成功但数据不一致的窗口？**
3. **PageRefCache 中根节点的更新是否原子？**

#### 2.2 调查步骤

##### Step 1：分析根分裂 CAS 逻辑

**文件**: `internal/infrastructure/storage/btree/leaf_lock_set.go:1072-1097`

```go
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
		return fmt.Errorf("CAS failed: oldRootID=%d, newRootPageID=%d, retry=%d", oldRootID, newRootPageID, i+1)
	}
}
```

**需要验证**：
1. `ReplacePage` 是否保证原子性？
2. CAS 成功后，旧根节点的子指针是否立即失效？
3. 是否存在 CAS 成功但 PageRefCache 仍指向旧根节点的窗口？

**实验方案**：
```go
// 在 splitRootOffHeapSync 中添加详细日志
func (b *BTree) splitRootOffHeapSync(...) error {
	// ... 现有逻辑 ...

	// CAS 更新前
	oldRootID := oldRootInfo.GetPageID()
	log.Printf("[ROOT_SPLIT] Before CAS: oldRootID=%d, newRootPageID=%d", oldRootID, newRootPageID)

	// CAS 更新
	if b.rootRef.ReplacePage(oldRootID, newRootInfo) {
		log.Printf("[ROOT_SPLIT] CAS SUCCESS: oldRootID=%d → newRootPageID=%d", oldRootID, newRootPageID)

		// 验证：检查 PageRefCache 是否已更新
		cachedRef := b.pageRefCache.GetOrCreate(model.PageID(oldRootID), false)
		cachedID := cachedRef.GetPageInfo().GetPageID()
		log.Printf("[ROOT_SPLIT] After CAS: cached oldRootID=%d, cachedInfo.pageID=%d", oldRootID, cachedID)

		if uint64(oldRootID) == cachedID {
			log.Printf("[ROOT_SPLIT] WARNING: PageRefCache still points to old root!")
		}
		break
	}

	// ... 现有逻辑 ...
}
```

##### Step 2：分析根分裂后的状态一致性

**需要验证**：
1. 根分裂后，旧根节点的子节点是否正确更新？
2. PageRefCache 中的缓存是否及时失效？
3. 其他 goroutine 是否可能读取到不一致的状态？

**实验方案**：
```go
// 在 splitRootOffHeapSync 成功后添加验证
func (b *BTree) splitRootOffHeapSync(...) error {
	// ... CAS 成功后 ...

	// 验证：确保新根不指向自己
	newRootChildren := b.offheapAdapter.GetAllChildren(uint32(newRootPageID))
	for _, child := range newRootChildren {
		if child == uint32(oldLeafPageID) {
			// 旧叶子节点不应该仍然作为新根的子节点
			return fmt.Errorf("old leaf %d still child of new root %d", oldLeafPageID, newRootPageID)
		}
		if child == uint32(newRootPageID) {
			// 新根不应该指向自己
			return fmt.Errorf("new root %d points to itself", newRootPageID)
		}
	}

	// ... 现有逻辑 ...
}
```

##### Step 3：分析 PageRefCache 的同步机制

**文件**: `internal/infrastructure/storage/btree/btree.go:109-156`

**已知信息**：
- PageRefCache 使用 `sync.RWMutex` 保护
- 存在双重检查机制（double-check）
- 但存在时间窗口：页面释放 → 重新分配 → 缓存更新

**需要验证**：
1. PageRefCache 的 `Update` 操作是否与 `GetOrCreate` 原子？
2. 根分裂时，PageRefCache 的更新顺序是什么？
3. 是否存在 PageRefCache 更新顺序错误的情况？

**实验方案**：
```go
// 在 PageRefCache.Update 中添加日志
func (c *PageRefCache) Update(pageID model.PageID, ref *PageRef) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 调试：检查更新前后的 pageID
	oldRef, exists := c.cache[pageID]
	if exists {
		oldID := oldRef.GetPageInfo().GetPageID()
		newID := ref.GetPageInfo().GetPageID()
		log.Printf("[PAGE_REF_CACHE] Update: pageID=%d, oldID=%d, newID=%d", pageID, oldID, newID)
	}

	c.cache[pageID] = ref
}
```

---

### 方向 3：Epoch Based Reclamation 有效性分析

#### 3.1 延迟释放机制

**已知信息**：
- 页面在 CAS 成功后延迟 3 个 epoch 释放
- Epoch 推进仅在 WAL 持久化时触发
- 存在 `delayedFreeList` 和 `freeList` 两个队列

**疑问**：
1. **Epoch 推进时机是否正确？**
2. **是否存在页面过早释放的情况？**
3. **是否存在页面延迟释放导致的内存泄漏？**

#### 3.2 调查步骤

##### Step 1：追踪 Epoch 推进时机

**文件**: `internal/infrastructure/storage/btree/btree.go:261-299`

```go
func (e *EpochBasedFreeList) AdvanceEpoch(pm *offheap.PageManager) {
	e.mu.Lock()
	defer e.mu.Unlock()

	oldEpoch := e.currentEpoch
	e.currentEpoch++

	// 释放 3 个 epoch 之前的页面
	epochToDelayed := e.currentEpoch - 2
	if e.currentEpoch >= 2 {
		pagesToDelayed := e.pending[epochToDelayed]
		delete(e.pending, epochToDelayed)

		for _, pid := range pagesToDelayed {
			DebugPrintf("[EPOCH_DELAYED] epoch=%d pageID=%d\n", epochToDelayed, pid)
			pm.Free(uint32(pid))
		}
	}

	// 第二步：将 N-3 的页面从延迟释放列表移到可用列表
	epochToFree := e.currentEpoch - 3
	if e.currentEpoch >= 3 {
		pagesToFree := e.pending[epochToFree]
		delete(e.pending, epochToFree)

		moved := pm.AdvanceDelayedFreeList()
		if moved > 0 {
			DebugPrintf("[EPOCH_DELAYED_ADVANCE] moved=%d pages from delayed to available\n", moved)
		}
	}
}
```

**需要验证**：
1. `AdvanceEpoch` 在哪些地方被调用？
2. 是否存在 `AdvanceEpoch` 调用频率不足的情况？
3. 高并发场景下，epoch 推进是否及时？

**实验方案**：
```go
// 在 AdvanceEpoch 中添加统计
type EpochBasedFreeList struct {
	currentEpoch uint64
	pending      map[uint64][]model.PageID
	mu           sync.Mutex

	// 新增：统计信息
	advanceCount atomic.Uint64
	lastAdvance  time.Time
}

func (e *EpochBasedFreeList) AdvanceEpoch(pm *offheap.PageManager) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.advanceCount.Add(1)
	e.lastAdvance = time.Now()

	// ... 现有逻辑 ...

	// 调试：检查 pending 队列大小
	for epoch, pages := range e.pending {
		if len(pages) > 100 {
			log.Printf("[EPOCH] Large pending queue: epoch=%d, size=%d", epoch, len(pages))
		}
	}
}

// 添加定期检查
func (e *EpochBasedFreeList) CheckEpochHealth() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 检查是否有长时间未释放的页面
	for epoch, pages := range e.pending {
		age := e.currentEpoch - epoch
		if age > 10 {
			log.Printf("[EPOCH] WARNING: pages pending for %d epochs: count=%d", age, len(pages))
		}
	}
}
```

##### Step 2：验证页面重用机制

**文件**: `internal/infrastructure/storage/btree/offheap/page_manager.go:122-135`

```go
// AdvanceDelayedFreeList 将延迟释放列表中的页面移到可用列表
func (pm *PageManager) AdvanceDelayedFreeList() int {
	moved := 0
	for {
		pageID, ok := pm.delayedFreeList.Dequeue()
		if !ok {
			break
		}
		pm.freeList.Enqueue(pageID)
		moved++
	}
	return moved
}
```

**问题**：
- 代码显示 `Alloc()` 使用单调递增的 `nextPageID`（不重用）
- 但存在 `freeList` 和 `delayedFreeList`
- **是否存在其他代码路径重用页面 ID？**

**需要验证**：
1. `Alloc()` 是否真的从不重用页面？
2. `freeList` 是否被使用？
3. 是否存在直接从 `freeList` 分配的代码路径？

**实验方案**：
```go
// 在 PageManager 中添加统计
type PageManager struct {
	// ... 现有字段 ...

	// 新增：统计信息
	allocCount     atomic.Uint64
	freeCount      atomic.Uint64
	reuseCount     atomic.Uint64
	allocFromFree  atomic.Uint64
}

func (pm *PageManager) Alloc() (uint32, error) {
	pageID := pm.nextPageID.Load()
	if pageID >= pm.total {
		// 尝试从 freeList 分配
		if pageID, ok := pm.freeList.Dequeue(); ok {
			pm.reuseCount.Add(1)
			pm.allocFromFree.Add(1)
			log.Printf("[PAGE_MANAGER] Reused pageID=%d from freeList", pageID)
			return pageID, nil
		}

		return 0, fmt.Errorf("out of memory: no free pages available (total: %d, used: %d)",
			pm.total, pm.used.Load())
	}

	pm.nextPageID.Add(1)
	pm.used.Add(1)
	pm.allocCount.Add(1)
	return pageID, nil
}
```

---

## 📊 实验计划

### Phase 1：数据收集（1 周）

#### 实验 1.1：失败场景完整追踪

**目标**：收集 1000 次失败的详细日志

**步骤**：
1. 修改 `TestSetWithLeafLock_Concurrent`，添加详细调试日志
2. 运行测试 1000 次，收集失败日志
3. 分析日志，识别共同模式

**输出**：
- 失败时的树结构状态
- 失败时的 PageRefCache 状态
- 失败时的 epoch 状态
- 完整的调用栈

#### 实验 1.2：Page 4095 生命周期追踪

**目标**：追踪 Page 4095 的完整生命周期

**步骤**：
1. 在 `PageManager` 中添加页面生命周期追踪
2. 在 `PageRefCache` 中添加缓存命中/未命中统计
3. 运行测试，记录 Page 4095 的所有操作

**输出**：
- Page 4095 分配时间戳
- Page 4095 释放时间戳
- Page 4095 是否被重用
- Page 4095 的父/子节点关系

#### 实验 1.3：根分裂并发压力测试

**目标**：验证根分裂 CAS 的原子性

**步骤**：
1. 创建专门的根分裂测试
2. 使用 `go test -race` 检测数据竞争
3. 模拟高并发根分裂场景

**输出**：
- 根分裂失败率
- CAS 冲突次数
- 数据竞争检测结果

### Phase 2：根因验证（1 周）

#### 实验 2.1：假设验证 - Page 4095 边界条件

**假设**：Page 4095 是某种边界检查的副作用，而非真实分配

**验证方法**：
1. 在 `PageManager.Alloc()` 中添加边界检查
2. 记录所有 pageID > 4000 的分配
3. 分析这些 pageID 的使用情况

**预期结果**：
- 如果 4095 是边界值副作用，应该能找到对应的边界检查代码
- 如果 4095 是真实分配，应该能追踪到完整的生命周期

#### 实验 2.2：假设验证 - PageRefCache 竞态

**假设**：PageRefCache 的更新顺序错误导致根节点循环

**验证方法**：
1. 在 `splitRootOffHeapSync` 中添加 PageRefCache 一致性检查
2. 验证 CAS 前后的缓存状态
3. 检测缓存不一致的时间窗口

**预期结果**：
- 如果 PageRefCache 存在竞态，应该能检测到不一致状态
- 如果竞态存在，需要增强同步机制

#### 实验 2.3：假设验证 - Epoch 延迟不足

**假设**：Epoch 推进不及时，导致页面过早重用

**验证方法**：
1. 在 `AdvanceEpoch` 中添加频率统计
2. 监控 pending 队列的大小
3. 检查是否有页面长时间未释放

**预期结果**：
- 如果 epoch 推进延迟，应该能看到 pending 队列堆积
- 如果延迟严重，需要优化 epoch 推进策略

### Phase 3：修复方案设计（1 周）

#### 备选方案 A：页面池隔离

**方案**：保留 Page 4095 不用于普通分配

**实现**：
```go
func (pm *PageManager) Alloc() (uint32, error) {
	pageID := pm.nextPageID.Load()

	// 边界检查：跳过 page 4095
	if pageID == 4095 {
		pm.nextPageID.Add(1)
		pageID = pm.nextPageID.Load()
	}

	// ... 现有逻辑 ...
}
```

**优点**：快速实施，低风险
**缺点**：治标不治本

#### 备选方案 B：增强 PageRefCache 同步

**方案**：使用版本号或 CAS 替代 RWMutex

**实现**：
```go
type PageRefCache struct {
	cache atomic.Value // map[model.PageID]*PageRef
	version atomic.Uint64
}

func (c *PageRefCache) Update(pageID model.PageID, ref *PageRef) {
	for {
		oldCache := c.cache.Load().(map[model.PageID]*PageRef)
		newCache := make(map[model.PageID]*PageRef)
		for k, v := range oldCache {
			newCache[k] = v
		}
		newCache[pageID] = ref

		if c.cache.CompareAndSwap(oldCache, newCache) {
			break
		}
	}
}
```

**优点**：无锁并发
**缺点**：实现复杂度高

#### 备选方案 C：动态 Epoch 推进

**方案**：不在 WAL 持久化时推进 epoch，而是定时推进

**实现**：
```go
func (b *BTree) StartEpochAdvancer(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.epochBasedFreeList.AdvanceEpoch(b.offheapPM)
		case <-ctx.Done():
			return
		}
	}
}
```

**优点**：更及时的页面释放
**缺点**：可能影响性能

---

## 📂 关键文件清单

| 文件 | 作用 | 优先级 |
|------|------|--------|
| `internal/infrastructure/storage/btree/offheap/page_manager.go` | 页面分配器 | 🔴 高 |
| `internal/infrastructure/storage/btree/btree.go` | PageRefCache, Epoch 管理 | 🔴 高 |
| `internal/infrastructure/storage/btree/leaf_lock_set.go` | 根分裂逻辑 | 🔴 高 |
| `internal/infrastructure/storage/btree/search_path.go` | 循环引用检测 | 🟡 中 |
| `internal/infrastructure/storage/btree/page_ref.go` | PageRef 实现 | 🟡 中 |
| `internal/infrastructure/storage/btree/epoch_based_free_list.go` | Epoch 延迟释放 | 🟡 中 |

---

## 🎯 成功指标

### 功能指标

- ✅ 找出 Page 4095 循环引用的根本原因
- ✅ 找出 Page 3 根节点循环的根本原因
- ✅ 验证 Epoch Based Reclamation 的正确性

### 质量指标

- ✅ 提供详细的 root cause 分析报告
- ✅ 提供可验证的实验数据
- ✅ 提供修复方案的优先级排序

### 时间指标

| 阶段 | 预估时间 | 里程碑 |
|------|----------|--------|
| Phase 1: 数据收集 | 1 周 | 完成 1000 次失败日志收集 |
| Phase 2: 根因验证 | 1 周 | 完成 3 个假设验证 |
| Phase 3: 修复方案 | 1 周 | 完成 3 个备选方案设计 |

---

## 📝 交付物

1. **详细调查报告**（`docs/09_code-review/circular-reference-root-cause-report.md`）
   - 数据收集结果
   - 根因分析
   - 实验数据

2. **修复提案**（`docs/09_code-review/circular-reference-fix-proposal-v2.md`）
   - 备选方案详细设计
   - 风险评估
   - 实施计划

3. **测试报告**（`docs/09_code-review/circular-reference-test-report.md`）
   - 测试用例
   - 测试结果
   - 性能对比

---

## 🚀 后续行动

### 立即行动

1. ✅ 创建详细的调查提案（本文档）
2. ⏳ 修改测试，添加详细日志
3. ⏳ 运行 1000 次测试，收集失败日志
4. ⏳ 分析日志，识别共同模式

### 短期行动（1-2 周）

1. ⏳ 实施 Phase 1 的 3 个实验
2. ⏳ 收集数据，分析结果
3. ⏳ 验证 3 个假设

### 中期行动（2-3 周）

1. ⏳ 设计 3 个备选修复方案
2. ⏳ 评估方案的风险和收益
3. ⏳ 选择最优方案实施

---

## 📚 参考文档

- 失败模式分析：`docs/09_code-review/failure-pattern-analysis.md`
- 实施总结：`docs/09_code-review/circular-reference-fix-implementation-summary.md`
- 关键代码：`internal/infrastructure/storage/btree/`

---

**文档版本**: v1.0
**最后更新**: 2026-03-27
**作者**: jzhang405
**审核者**: [待定]
