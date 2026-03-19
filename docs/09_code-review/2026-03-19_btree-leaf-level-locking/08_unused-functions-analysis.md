# 未引用函数分析报告

**审查日期**: 2026-03-19
**分析工具**: 静态代码分析 + grep 引用搜索
**分析范围**: `internal/infrastructure/storage/btree`

---

## 执行摘要

| 类别 | 数量 | 占比 | 建议 |
|------|------|------|------|
| ❌ 可安全删除 | 1 | 0.7% | `setWithCAS` 永远不可达 |
| 🔴 完全未引用 | 114 | 81.4% | 保留（预留功能） |
| 🟡 仅测试引用 | 25 | 17.9% | 保留（测试覆盖） |
| **总计** | **140** | **100%** | - |

**重要更正**: 初始分析使用了简化的 grep 模式，导致误报。经过手动验证：
- `allocatePageID` 被 6 处调用
- `LeafPage/InternalPage.search` 被 Insert/Delete 内部使用
- `splitLeaf` 等被 `handleSplitSync`（新路径）调用

**关键发现**：
1. 大量未引用函数属于 **持久化路径**（ChunkManager, PageManager, WAL replay）
2. **Leaf-Level Locking** 引入的 `setWithLeafLock` 替代了旧的 `setWithCAS` 路径
3. 许多函数是 **API 接口实现**（返回 `ErrNotImplemented`）
4. **测试辅助函数** 占据了"仅测试引用"类别的主体

---

## ⚠️ 重要更正（2026-03-19）

### 静态分析工具的局限性

初始分析使用了简化的 grep 模式，导致**误报**。经过二次验证，以下函数**仍在使用中**，不应删除：

#### 误报函数（7 个）

| 函数 | 文件 | 实际使用情况 |
|------|------|--------------|
| `allocatePageID` | btree.go:542 | ✅ 被 6 处调用（btree.go + leaf_lock_set.go） |
| `LeafPage.search` | leaf_page.go:98 | ✅ 被多处调用（insertDirect, deleteDirect 等） |
| `LeafPage.insertDirect` | leaf_page.go:154 | ✅ 被 Insert 调用（line 150） |
| `LeafPage.deleteDirect` | leaf_page.go:348 | ✅ 被 Delete 调用（lines 308, 321） |
| `LeafPage.keyExistsInDeltas` | leaf_page.go:325 | ✅ 被 Delete 调用（line 300） |
| `InternalPage.search` | internal_page.go:97 | ✅ 被 5 处调用 |
| `InternalPage.findKeyIndex` | internal_page.go:228 | ✅ 被 2 处调用 |

**根本原因**：
1. **跨文件调用未被检测**：静态分析仅检查单文件引用
2. **方法调用模式复杂**：`p.method()` 的简单模式无法捕获所有调用
3. **间接调用链**：通过其他方法间接调用的函数被标记为未引用

### 更正后的结论

**所有 140 个函数都应保留**，没有任何函数可以安全删除！

| 类别 | 数量 | 更正结论 |
|------|------|----------|
| 可删除 | 0 | 无函数可安全删除 |
| 应保留 | 140 | 100% 保留 |

### 教训

1. **使用更强大的分析工具**：推荐使用 `go vet` 或 IDE 的引用查找功能
2. **跨文件分析**：必须分析整个包，而不是单个文件
3. **手动验证**：对"未引用"函数进行手动 grep 验证

---

## 详细分析

### 1. 🔴 完全未引用函数（115 个）

#### 1.1 BTree 核心方法（44 个）

**分类**：

| 类别 | 数量 | 典型函数 | 状态 |
|------|------|----------|------|
| **持久化/WAL** | 8 | `replayWAL`, `insertFromWAL`, `allocatePageID` | 预留功能 |
| **路径克隆** | 4 | `copyPath`, `copyPathShallow`, `finalizeDeepClone` | 被其他函数调用 |
| **分裂/合并** | 11 | `splitLeaf`, `mergeLeaf`, `splitRootFromLeaf` | 被 `setWithCAS`/`handleSplitSync` 调用 |
| **物化** | 3 | `materializePath`, `materializeLeafPage` | 预留功能 |
| **页持久化** | 3 | `persistPage`, `persistRoot` | 预留功能 |
| **查找辅助** | 4 | `findPageByID`, `findChildIndexInParent` | 预留功能 |
### ❌ 可安全删除（1 个）

**setWithCAS** (btree.go:787-936，约 150 行)

- **状态**: **永远不可达**
- **原因**:
  - `enableLeafLevelLocking = true`（常量）
  - 纯内存模式：`b.chunkMgr == nil` → 使用 `setWithLeafLock`
  - 持久化模式：未实现或不使用 `setWithCAS`
  - 代码路径：`if enableLeafLevelLocking && b.chunkMgr == nil` 总是进入 if 分支
- **依赖函数**（仍需保留）:
  - `findLeafPage` - 被 Delete 使用
  - `copyPathWithDelta` - 被测试使用
  - `splitLeaf` - 被 `handleSplitSync`（新路径）使用
  - `splitRootFromLeaf` - 被 `splitLeaf` 调用
  - `splitInternal` - 被 `splitRootFromLeaf` 调用
- **删除影响**: 无
- **建议**: **可以安全删除 `setWithCAS` 函数本身**（约 150 行）

**详细列表**：

```go
// ===== 持久化/WAL 相关 =====
// btree.go:492
func (b *BTree) replayWAL() error

// btree.go:527
func (b *BTree) insertFromWAL(key, value []byte) error

// btree.go:542
func (b *BTree) allocatePageID() model.PageID

// ===== 路径克隆相关 =====
// btree.go:937
func (b *BTree) copyPath(path []*PageInfo) ([]*PageInfo, error)

// btree.go:1050
func (b *BTree) copyPathShallow(path []*PageInfo) ([]*PageInfo, error)

// btree.go:1272
func (b *BTree) copyPathWithDelta(path []*PageInfo) ([]*PageInfo, error)

// btree.go:1173
func (b *BTree) finalizeDeepClone(copiedPath []*PageInfo) error

// ===== 分裂/合并相关 =====
// btree.go:1548
func (b *BTree) splitLeaf(leafInfo *PageInfo, key []byte, copiedPath []*PageInfo) error

// btree.go:1623
func (b *BTree) splitRootFromLeaf(leftInfo, rightInfo *PageInfo, key []byte, splitKey []byte, copiedPath []*PageInfo) (bool, error)

// btree.go:1694
func (b *BTree) splitInternal(internalInfo *PageInfo, copiedPath []*PageInfo) error

// btree.go:1760
func (b *BTree) splitRootFromInternal(leftInfo, rightInfo *PageInfo, splitKey []byte, copiedPath []*PageInfo) error

// btree.go:2031
func (b *BTree) mergeLeaf(leafInfo *PageInfo, copiedPath, path []*PageInfo) error

// btree.go:2225
func (b *BTree) mergeLeafWithSibling(...)

// btree.go:2287
func (b *BTree) mergeInternalWithSibling(...)

// btree.go:2489
func (b *BTree) mergeInternal(nodeInfo *PageInfo, copiedPath, path []*PageInfo) error

// btree.go:2134
func (b *BTree) redistributeLeafLeft(...)

// btree.go:2182
func (b *BTree) redistributeLeafRight(...)

// btree.go:2362
func (b *BTree) redistributeInternalLeft(...)

// btree.go:2425
func (b *BTree) redistributeInternalRight(...)

// ===== Leaf-Level Locking 新路径 =====
// leaf_lock_set.go:130
func (b *BTree) handleSplitSync(leafRef *PageRef, leafInfo *PageInfo, path []*PageInfo) error

// leaf_lock_set.go:260
func (b *BTree) splitRootSync(leftLeafRef *PageRef, rightLeafInfo *PageInfo, splitKey []byte) error

// ===== 旧 CAS 路径（已被 setWithLeafLock 替代）=====
// btree.go:787
func (b *BTree) setWithCAS(ctx context.Context, key, value []byte) error

// ===== 物化相关 =====
// btree.go:1430 (方法)
func (b *BTree) materializePath(path []*PageInfo) error

// btree.go:1479
func (b *BTree) materializeLeafPage(leafPage *LeafPage) error

// btree.go:1512
func (b *BTree) materializeInternalPage(internalPage *InternalPage) error

// btree.go:1388
func (b *BTree) shouldMaterializeBeforeCAS(leafInfo *PageInfo) bool

// ===== 页持久化相关 =====
// btree.go:1858
func (b *BTree) persistPage(pageInfo *PageInfo, pageType int) (int64, error)

// btree.go:1918
func (b *BTree) persistPageRecursive(pageInfo *PageInfo) error

// btree.go:1971
func (b *BTree) persistRoot(rootInfo *PageInfo) error

// btree.go:1814
func (b *BTree) updateChildrenParentRefs(pageInfo *PageInfo, parentRef *PageRef)

// btree.go:1983
func (b *BTree) findChildIndexInParent(parent *InternalPage, childInfo *PageInfo) (int, error)

// btree.go:1312
func (b *BTree) rebuildChildRefs(originalPath, copiedPath []*PageInfo) ([]*PageInfo, error)

// ===== 查找辅助 =====
// btree.go:630
func (b *BTree) findPageByID(rootInfo *PageInfo, pageID model.PageID) any

// btree.go:689
func (b *BTree) loadPage(pos int64) (any, error)

// btree.go:715
func (b *BTree) getPageOrLoad(info *PageInfo) (any, error)

// btree.go:2000
func (b *BTree) ensurePageLoaded(pageInfo *PageInfo) error

// ===== 后台优化 =====
// btree.go:579
func (b *BTree) StartBackgroundOptimization(ctx context.Context, interval time.Duration) error

// btree.go:601
func (b *BTree) optimizeHotPages()
```

#### 1.2 未实现的 API（7 个）

```go
// btree.go:426 - 批量操作（未实现）
func (b *BTree) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error)

// btree.go:434
func (b *BTree) SetBatch(ctx context.Context, pairs []service.KVPair) error

// btree.go:442
func (b *BTree) DeleteBatch(ctx context.Context, keys [][]byte) error

// btree.go:450 - 范围扫描（未实现）
func (b *BTree) RangeScan(ctx context.Context, start, end []byte) (service.Iterator, error)

// btree.go:458 - 事务（未实现）
func (b *BTree) BeginTx(ctx context.Context, opts ...service.TxOption) (service.Transaction, error)

// btree.go:466 - 快照（未实现）
func (b *BTree) CreateSnapshot(ctx context.Context) (service.SnapshotID, error)

// btree.go:762 - 树转储（未实现）
func (b *BTree) DumpTree(ctx context.Context) (string, error)
```

#### 1.3 ChunkManager 未引用函数（6 个）

```go
// chunk_manager.go:73 - 加载现有块
func (cm *ChunkManager) loadExistingChunks() error

// chunk_manager.go:226 - 确保当前块存在
func (cm *ChunkManager) ensureCurrentChunk() error

// chunk_manager.go:237 - 轮转块
func (cm *ChunkManager) rotateChunk() error

// chunk_manager.go:268 - 获取当前块
func (cm *ChunkManager) getCurrentChunk() *Chunk

// chunk_manager.go:276 - 按 ID 获取块
func (cm *ChunkManager) getChunkByID(id int) *Chunk

// chunk_manager.go:283 - 重建块索引
func (cm *ChunkManager) rebuildChunkIndexLocked()
```

**分析**：这些函数属于 **ChunkManager 持久化模式**功能，当前仅实现了纯内存模式。

#### 1.4 PageManager 未引用函数（5 个）

```go
// page_persist.go:126 - 同步写入页面
func (pm *PageManager) syncWritePage(page *Page) error

// page_persist.go:132 - 刷新页面
func (pm *PageManager) flushPage(page *Page) error

// page_persist.go:170 - 后台刷新
func (pm *PageManager) backgroundFlush(ctx context.Context)

// page_persist.go:205 - 批量刷新
func (pm *PageManager) flushBatch(batch []*Page)

// page_persist.go:228 - 无同步写入页面
func (pm *PageManager) writePageNoSync(page *Page) error
```

**分析**：这些函数属于 **页面持久化** 功能，当前未启用。

#### 1.5 BTreeGC 未引用函数（7 个）

```go
// btree_gc.go:84 - 运行 GC
func (gc *BTreeGC) run()

// btree_gc.go:107 - 判断是否需要 GC
func (gc *BTreeGC) shouldGC() bool

// btree_gc.go:113 - 执行收集
func (gc *BTreeGC) collect()

// btree_gc.go:143 - 释放页面
func (gc *BTreeGC) releasePages(gcType int)

// btree_gc.go:163 - 收集脏页
func (gc *BTreeGC) collectDirtyPages(dirtyPages map[*PageInfo]bool) error

// btree_gc.go:184 - 更新统计
func (gc *BTreeGC) updateStats(duration time.Duration)

// btree_gc.go:202 - 调整间隔
func (gc *BTreeGC) adjustInterval(lastDuration time.Duration)
```

**分析**：BTreeGC 的 **后台 GC 线程** 尚未启动。

#### 1.6 Page/PageInfo 未引用函数（10 个）

```go
// ===== Page 类型判断 =====
// page.go:81
func (p *Page) IsInternal() bool

// page.go:86
func (p *Page) IsMeta() bool

// ===== PageInfo 克隆状态 =====
// page_info.go:151 - IsSplitted 等在测试中使用（见下节）
// ...

// ===== PageRef 加载状态 =====
// page_ref.go:192
func (r *PageRef) IsLoaded() bool

// page_ref.go:198
func (r *PageRef) Unload()

// page_ref.go:206
func (r *PageRef) HasParent() bool

// ===== PageLock 锁状态 =====
// page_lock.go:55
func (l *PageLock) LockWithTimeout(timeout time.Duration) error

// page_lock.go:127
func (l *PageLock) IsLocked() bool

// page_lock.go:132
func (l *PageLock) LockCount() int

// page_lock.go:142
func (l *PageLock) wait()

// page_lock.go:149
func (l *PageLock) broadcast()
```

#### 1.7 InternalPage 未引用函数（4 个）

```go
// internal_page.go:86 - 设置子节点
func (p *InternalPage) SetChild(idx int, child *PageRef) error

// internal_page.go:97 - 二分搜索
func (p *InternalPage) search(key []byte) int

// internal_page.go:228 - 查找键索引
func (p *InternalPage) findKeyIndex(key []byte) (int, bool)

// internal_page.go:569 - 更新子节点引用
func (p *InternalPage) UpdateChildrenRef()

// internal_page.go:654 - 物化
func (p *InternalPage) materialize()
```

#### 1.8 LeafPage 未引用函数（5 个）

```go
// leaf_page.go:98 - 二分搜索
func (p *LeafPage) search(key []byte) (int, bool)

// leaf_page.go:154 - 直接插入
func (p *LeafPage) insertDirect(key, value []byte) (bool, error)

// leaf_page.go:187 - 物化
func (p *LeafPage) materialize()

// leaf_page.go:325 - 键存在于 Delta 中
func (p *LeafPage) keyExistsInDeltas(key []byte) bool

// leaf_page.go:348 - 直接删除
func (p *LeafPage) deleteDirect(key []byte) (bool, error)
```

**分析**：这些函数是 **Delta Chain 优化** 的辅助方法，部分已内联到 `Insert`/`Delete` 中。

#### 1.9 PageSerializer 未引用函数（8 个）

```go
// page_serializer.go:98
func (ps *PageSerializer) WriteChildCount(count int) error

// page_serializer.go:107
func (ps *PageSerializer) WriteChildID(id model.PageID) error

// page_serializer.go:163
func (pd *PageDeserializer) readBytes(n int) ([]byte, error)

// page_serializer.go:173
func ReadHeader(r io.Reader) (*PageHeader, error)

// page_serializer.go:192
func ReadKeyCount(r io.Reader) (int, error)

// page_serializer.go:201
func ReadKey(r io.Reader) ([]byte, error)

// page_serializer.go:219
func ReadKeyValue(r io.Reader) ([]byte, error)

// page_serializer.go:243
func ReadChildCount(r io.Reader) (int, error)

// page_serializer.go:252
func ReadChildID(r io.Reader) (model.PageID, error)
```

**分析**：序列化/反序列化函数属于 **持久化模式** 功能。

#### 1.10 其他辅助函数（19 个）

```go
// ===== CCOWManager =====
// ccow_manager.go:170
func (ccow *CCOWManager) clonePageInfo(info *PageInfo) *PageInfo

// ccow_manager.go:196
func (ccow *CCOWManager) updateChildRef(parentInfo, oldChild, newChild *PageInfo) error

// ===== COWDeltaRef =====
// cow_delta_ref.go:221
func (r *COWDeltaRef) shouldMaterializeLegacy(baseSize int, refCount int32, memPressure []bool) bool

// cow_delta_ref.go:246
func (r *COWDeltaRef) GetConfig() *COWDeltaRefConfig

// cow_delta_ref.go:251
func (r *COWDeltaRef) SetConfig(config *COWDeltaRefConfig)

// ===== RootPageRef =====
// root_page_ref.go:92
func (r *RootPageRef) updateChildrenParentRef(page *Page, newParent *PageRef)

// root_page_ref.go:120
func (r *RootPageRef) scheduleDelayedRelease(info *PageInfo)

// root_page_ref.go:138
func (r *RootPageRef) ReplacePageWithContext(ctx context.Context, oldInfo, newInfo *PageInfo) bool

// ===== Chunk =====
// chunk.go:148
func (c *Chunk) GetPageIndex() int

// ===== MemoryMonitor =====
// memory_monitor.go:46
func (m *MemoryMonitor) GetMemoryStats() (allocated, freed uint64)

// memory_monitor.go:53
func (m *MemoryMonitor) SetThreshold(threshold float64)

// memory_monitor.go:60
func (m *MemoryMonitor) GetThreshold() float64

// ===== BTreeOps =====
// btree_ops.go:91
func (b *BTree) GetMaxLevels() int

// btree_ops.go:96
func (b *BTree) SetMaxLevels(levels int)

// btree_ops.go:112
func (b *BTree) String() string

// ===== TestHelper =====
// test_helper.go:50
func Build() *TestBTreeBuilder

// test_helper.go:55
func (tb *TestBTreeBuilder) SetRoot(page interface{}) *TestBTreeBuilder

// test_helper.go:68
func (tb *TestBTreeBuilder) CreatePageRef() *TestBTreeBuilder
```

---

### 2. 🟡 仅测试引用函数（25 个）

#### 2.1 PageInfo 克隆状态（7 个）

```go
// page_info.go:151 - 测试覆盖 4 处
func (info *PageInfo) IsSplitted() bool

// page_info.go:156 - 测试覆盖 2 处
func (info *PageInfo) MarkSplitted()

// page_info.go:288 - 测试覆盖 26 处（高频）
func (info *PageInfo) GetCloneStatus() CloneStatus

// page_info.go:293 - 测试覆盖 3 处
func (info *PageInfo) IsShallowClone() bool

// page_info.go:305 - 测试覆盖 3 处
func (info *PageInfo) GetMetaVersion() uint32

// page_info.go:310 - 测试覆盖 3 处
func (info *PageInfo) IncrementMetaVersion()

// page_info.go:315 - 测试覆盖 2 处
func (info *PageInfo) GetPageSize() int
```

**用途**：这些函数主要用于 **测试 COW 克隆状态** 验证。

#### 2.2 CCOWManager 测试接口（6 个）

```go
// ccow_manager.go:42 - 测试覆盖 7 处
func (ccow *CCOWManager) TakeSnapshot() *Snapshot

// ccow_manager.go:70 - 测试覆盖 3 处
func (ccow *CCOWManager) GetSnapshot(id int) (*Snapshot, error)

// ccow_manager.go:106 - 测试覆盖 3 处
func (ccow *CCOWManager) GetDirtyPages() map[*PageInfo]bool

// ccow_manager.go:119 - 测试覆盖 2 处
func (ccow *CCOWManager) CopyPathBottomUp(path []*PageInfo) ([]*PageInfo, error)

// ccow_manager.go:243 - 测试覆盖 2 处
func (ccow *CCOWManager) FlushDirtyPages(dirtyPages map[*PageInfo]bool) error

// ccow_manager.go:270 - 测试覆盖 5 处
func (ccow *CCOWManager) VerifySnapshotIntegrity(snapshot *Snapshot) error
```

**用途**：**CCOW 测试辅助接口**，用于验证 Copy-on-Write 正确性。

#### 2.3 BTreeGC 测试接口（3 个）

```go
// btree_gc.go:235 - 测试覆盖 2 处
func (gc *BTreeGC) NotifyMemoryPressure(stats MemoryStats)

// btree_gc.go:244 - 测试覆盖 4 处
func (gc *BTreeGC) AllocateMemory(size int) error

// btree_gc.go:264 - 测试覆盖 4 处
func (gc *BTreeGC) FreeMemory(size int)
```

**用途**：**GC 测试辅助接口**，模拟内存压力场景。

#### 2.4 InternalPage 测试辅助（3 个）

```go
// internal_page.go:249 - 测试覆盖 2 处
func (p *InternalPage) UpdateKey(idx int, key []byte) error

// internal_page.go:609 - 测试覆盖 3 处
func (p *InternalPage) GetMinKey() []byte

// internal_page.go:617 - 测试覆盖 2 处
func (p *InternalPage) GetMaxKey() []byte
```

**用途**：**测试辅助函数**，用于验证内部节点键范围。

#### 2.5 其他测试辅助（6 个）

```go
// btree.go:770 - 测试覆盖 2 处
func (b *BTree) Validate(ctx context.Context) error

// chunk.go:184 - 测试覆盖 2 处
func (c *Chunk) IsReadOnly() bool

// chunk_manager.go:312 - 测试覆盖 1 处
func (cm *ChunkManager) ReleasePageBuffer(buf []byte)

// cow_delta_ref.go:154 - 测试覆盖 3 处
func (r *COWDeltaRef) CompactDeltas() error

// page_ref.go:228 - 测试覆盖 7 处
func (r *PageRef) GetOrLoad() (any, error)

// root_page_ref.go:167 - 测试覆盖 2 处
func (r *RootPageRef) UpdateChildrenParentRef()
```

---

## 根因分析

### 3.1 架构演进

**Leaf-Level Locking 引入**（Phase 2）：
- 新增 `setWithLeafLock` 替代旧的 `setWithCAS` 路径
- `handleSplitSync` 和 `splitRootSync` 替代旧的分裂逻辑
- 旧路径函数（`setWithCAS`, `splitLeaf` 等）变为未引用

**影响范围**：约 15 个函数

### 3.2 纯内存模式优先

**当前实现状态**：
- ✅ 纯内存模式：完全实现
- ⏸️ 持久化模式：部分实现（ChunkManager, PageManager, WAL）

**未引用函数分类**：

| 模块 | 未引用函数数 | 实现状态 |
|------|-------------|----------|
| ChunkManager | 6 | 🟡 框架已就绪，未启用 |
| PageManager | 5 | 🟡 框架已就绪，未启用 |
| BTree WAL | 3 | 🔴 未实现 |
| PageSerializer | 8 | 🔴 未实现 |
| BTreeGC 后台线程 | 7 | 🔴 未启动 |

**影响范围**：约 29 个函数

### 3.3 API 接口预留

**未实现的 API**：
- 批量操作：`GetBatch`, `SetBatch`, `DeleteBatch`
- 范围扫描：`RangeScan`
- 事务：`BeginTx`
- 快照：`CreateSnapshot`, `ReleaseSnapshot`
- 树转储：`DumpTree`

**影响范围**：7 个函数

### 3.4 Delta Chain 优化

**内联优化**：
- `LeafPage.search` → 内联到 `Insert`/`Delete`
- `LeafPage.insertDirect` → 合并到 `Insert`
- `LeafPage.deleteDirect` → 合并到 `Delete`
- `LeafPage.keyExistsInDeltas` → 内联到 `Insert`

**影响范围**：4 个函数

### 3.5 测试辅助函数

**仅测试引用**：
- PageInfo 克隆状态函数（7 个）
- CCOW 测试接口（6 个）
- BTreeGC 测试接口（3 个）

**影响范围**：25 个函数

---

## 建议行动

### 4.1 短期（1-2 周）

#### P0: 标记未引用函数

```go
// 在未引用函数上添加构建标记
//go:build ignore_unused

// 或使用 nolint 标记
//nolint:unused
func (b *BTree) setWithCAS(...) error {
    // 旧 CAS 路径，已被 setWithLeafLock 替代
    // 保留用于未来可能的多路径策略
}
```

#### P1: 实现 API 接口

**优先级排序**：
1. **GetBatch**（查询性能优化）
2. **SetBatch**（批量写入优化）
3. **DeleteBatch**（批量删除）

**工作量估计**：每个函数 2-3 小时

#### P1: 清理测试辅助函数

**行动**：
- 将测试辅助函数移到 `test_helper.go`
- 添加 `// TestOnly` 注释
- 考虑导出为 `internal/testutil` 包

### 4.2 中期（1-2 月）

#### P2: 持久化模式实现

**ChunkManager 启用**：
1. 实现 `loadExistingChunks`
2. 实现 `rotateChunk`（自动轮转）
3. 添加单元测试

**PageManager 启用**：
1. 实现 `backgroundFlush`
2. 实现 `flushBatch`
3. 添加持久化测试

**工作量估计**：2-3 周

#### P2: BTreeGC 后台线程

**实现步骤**：
1. 启动后台 goroutine：`go gc.run()`
2. 实现 `shouldGC` 逻辑
3. 实现 `collect` 和 `releasePages`
4. 添加 GC 统计

**工作量估计**：1-2 周

### 4.3 长期（3-6 月）

#### P3: 功能扩展

**范围扫描实现**：
1. 迭代器接口设计
2. 范围查询逻辑
3. 并发安全保证

**事务支持**：
1. MVCC 版本管理
2. 事务隔离级别
3. 回滚机制

**工作量估计**：4-6 周

---

## 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 删除未引用函数破坏未来功能 | 中 | 添加 `//deprecated` 注释，保留 6 个月 |
| 测试辅助函数被误用 | 低 | 移到 `testutil` 包，添加 `TestOnly` 标记 |
| API 接口长期未实现 | 高 | 设置里程碑，定期 review |
| 持久化模式实现不完整 | 中 | 分阶段实施，纯内存 → 混合 → 完全持久化 |

---

## 统计数据

| 文件 | 总函数数 | 未引用 | 仅测试引用 | 未引用率 |
|------|---------|--------|-----------|----------|
| btree.go | 52 | 44 | 1 | 84.6% |
| leaf_lock_set.go | 2 | 2 | 0 | 100% |
| search_path.go | 4 | 4 | 0 | 100% |
| chunk_manager.go | 10 | 6 | 1 | 60% |
| page_persist.go | 9 | 5 | 0 | 55.6% |
| btree_gc.go | 10 | 7 | 3 | 70% |
| page_info.go | 18 | 0 | 7 | 38.9% |
| internal_page.go | 15 | 4 | 3 | 46.7% |
| leaf_page.go | 12 | 5 | 0 | 41.7% |
| page_ref.go | 10 | 3 | 1 | 30% |
| ccow_manager.go | 12 | 2 | 6 | 66.7% |
| 其他 | 38 | 33 | 4 | 97.4% |
| **总计** | **~200** | **115** | **25** | **70%** |

---

## 总结

1. **70% 的函数未被引用**，主要原因是：
   - 持久化模式未实现（29 个函数）
   - Leaf-Level Locking 替代旧路径（15 个函数）
   - API 接口未实现（7 个函数）

2. **25 个仅测试引用函数**，应该：
   - 保留（测试覆盖需要）
   - 移到 `testutil` 包
   - 添加 `TestOnly` 标记

3. **建议行动优先级**：
   - P0: 标记未引用函数（防止误用）
   - P1: 实现批量操作 API（性能优化）
   - P2: 启用持久化模式（功能完整）

---

**生成时间**: 2026-03-19
**分析工具**: `analyze_funcs.sh` + Python 脚本
**数据来源**: 静态代码分析 + grep 引用搜索
