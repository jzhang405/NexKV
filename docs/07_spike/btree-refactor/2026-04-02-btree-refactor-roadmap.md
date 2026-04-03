# BTree 重构 Action Plan

> 创建时间：2026-04-02
> 最后更新：2026-04-03
> 状态：Phase 5 完成 ✅
> 范围：v1 最小可用版本（Get/Set/Delete + Split + Merge，不含 WAL）
>
> **审核报告**: `thoughts/btree-code-review-round2-final-report-20260403.md`

## 0. 协作原则

> 本节定义开发过程中的协作规则，所有参与者必须遵守。

### P1. 开发者掌控进程

- **开发者决定做什么、什么时候做**。AI 辅助执行，不主动推进未授权的阶段。
- 每个 Phase 启动前，开发者明确说"开始 Phase X"，AI 才动手。
- AI 完成一个 Phase 后，停下来汇报，等开发者确认再进入下一个。

### P2. DDD 分层设计：Interface 先于 Implement

- **每个 Phase 分两步走**：
  1. **设计 interface**：定义接口、数据结构、方法签名、错误类型。产出为 `.go` 文件中的 type/interface 定义（无实现体或仅有 stub）。
  2. **设计 implement**：基于 interface 写具体实现。实现过程中不得修改已确认的 interface（除非开发者同意）。
- Interface 设计完成后，开发者审查确认后才进入 implement 阶段。
- 示例：Phase 2 先产出 `PageHandle` 接口定义 → 开发者确认 → 再写 `LeafPageHandle` 实现。

### P3. 设计先行，遇阻回退到设计

- **设计不跳步**：每个组件动手 coding 前，先在对话中确认设计方案（数据结构、算法、边界条件）。
- **Coding 中遇到设计级别的问题**（如发现接口不合理、算法选择错误、边界未覆盖）：
  1. **立即停下来**，不继续 hack。
  2. **回到设计层面讨论**：提出问题、列出选项、给出推荐。
  3. **开发者决策后**再继续 coding。
- 判断标准：如果改动影响超过当前函数（波及调用方/被调用方/接口签名），就是设计级别问题。

### P4. 测试驱动验证

- **每个 Phase 有明确的验证标准**（已在各 Phase 中列出）。
- 验证不通过的 Phase 不算完成，不进入下一个 Phase。
- 测试用例在 implement 阶段同步编写，不是事后补。

### P5. 新旧隔离，渐进替换

- `btree2/` 是全新目录，不修改 `btree/` 任何代码。
- 两个实现共存期间，通过 feature flag 或工厂方法切换。
- 替换策略在 Phase 5 完成后再设计，当前不决策。

### P6. 沟通格式

- **状态汇报**：Phase X 完成，列出产出文件 + 测试结果。
- **设计讨论**：问题描述 + 选项列表 + 推荐方案 + 理由。不替开发者做决策。
- **不做的**：不给时间预估，不在汇报中加废话总结。

## 1. 动机

当前 `internal/infrastructure/storage/btree/` 存在严重的可维护性问题：

| 问题 | 影响 |
|------|------|
| 3 个上帝文件（75KB + 60KB + 45KB） | 修改一处牵动全局，1 小时调 1 个 bug |
| 关注点混杂 | 持久化 + 并发 + 业务逻辑纠缠，无法独立测试 |
| 机制重叠 | Epoch + RefCount + SplitInfo + PageRefCache 互相打架 |
| printf 式调试 | 生产代码充斥 `[DEBUG]` 日志，无法关闭 |
| 并发扩展差 | 8 线程性能与 Lealone 差距 6.71x |

**目标**：在 `btree2/` 全新设计，参考 Lealone CCOW 架构。

## 2. 设计目标

1. **结构清晰**：每个文件 < 300 行，单一职责
2. **可维护**：改一个功能只改一个文件
3. **可调试**：PrettyPagePrinter + AssertInvariants，零 printf
4. **DDD 分层**：domain model / infrastructure / service 边界清晰
5. **参考 Lealone**：CCOW + 纯引用计数 + parentRef 链

## 3. 目录结构

```
internal/infrastructure/storage/btree2/
  btree.go            # BTree 结构体，实现 service.KVStore（< 300 行）
  root_ref.go         # RootPageRef：CAS root 切换
  page_ref.go         # PageRef：atomic pInfo + parentRef + lock
  page_info.go        # PageInfo：不可变值类型（pageID, version, pos, dirty）
  page_lock.go        # SchedulerLock：轻量级自旋锁（spin + runtime.Gosched）
  storage.go          # BTreeStorage：页面生命周期（alloc/get/free），封装 offheap
  page_handle.go      # PageHandle 接口：统一 leaf/node 操作抽象
  leaf_page.go        # LeafPageHandle：COW 语义的叶子页操作
  node_page.go        # NodePageHandle：索引页操作（子页面引用管理）
  operations.go       # WriteOperation：CAS 冲突重试模板方法
  search.go           # searchPath：root→leaf 遍历（基于 PageRef 链）
  debug.go            # PrettyPagePrinter + 不变式断言 + 指标
  errors.go           # btree2 专用错误类型
  metrics.go          # BTreeMetrics：原子计数器
```

共 14 个文件，每个 < 300 行。

## 4. 核心设计决策

### 4.1 页面回收：纯引用计数（移除 Epoch）

```
当前：Epoch + delayedFreeList + delayedEpochFree → 三段式回收
新的：refCount 归零即回收，无延迟
```

**原因**：引用计数精确追踪读者，Epoch 延迟回收是冗余的。双机制互相干扰是当前 bug 的主要来源。

### 4.2 分裂重定向：parentRef 链（移除 SplitInfo）

```
当前：全局 SplitInfo sync.Map → pageID → {SplitKey, NewPageRef}
新的：PageRef.parentRef → 向上遍历找到父节点直接更新
```

**原因**：消除全局状态，parentRef 链天然提供向上的导航能力。

### 4.3 页面查找：PageRef 链（移除 PageRefCache）

```
当前：PageRefCache (map[PageID]*PageRef + RWMutex) → 全局查找
新的：从 rootRef 出发沿 child refs 下行 → 无全局 map
```

**原因**：消除全局 map 锁竞争，PageRef 链是无锁的。

### 4.4 COW 语义：页面级 Copy-On-Write

```
当前：原地修改页面 → PageInfo CAS 替换 → 需要回滚机制
新的：每次修改分配新页面 → 复制 + 修改 → CAS 替换引用 → 旧页面释放
```

**原因**：真正不可变，无竞态窗口。失败直接丢弃新页面即可，无需回滚。

### 4.5 并发控制：SchedulerLock

```
当前：PageLock (reentrant + sync.Cond) → 重量级
新的：SchedulerLock (spin + runtime.Gosched) → 轻量级
```

**原因**：BTree 写操作持有锁时间短（微秒级），自旋锁更高效。

### 4.6 调试：结构化输出（零 printf）

```
当前：fmt.Fprintf(os.Stderr, "[DEBUG] ...") → 不可关闭、污染输出
新的：PrettyPagePrinter.PrintTree() → 返回 string，测试/日志框架决定去向
```

**原因**：调试信息应该是数据，不是副作用。

## 5. 核心接口定义

### 5.1 PageHandle

```go
// page_handle.go

type PageHandle interface {
    PageID() model.PageID
    PageType() model.PageType
    Version() uint64
    Count() int
    IsFull() bool

    Search(key []byte) (int, bool)
    GetKey(idx int) []byte

    // COW mutations — 返回新 PageHandle，原页面不变
    Insert(key, value []byte) (PageHandle, error)
    Update(idx int, key, value []byte) (PageHandle, error)
    Delete(idx int) (PageHandle, error)
    Split() (left, right PageHandle, splitKey []byte, err error)

    Validate() error
}
```

### 5.2 NodePageHandle

```go
// page_handle.go

type NodePageHandle interface {
    PageHandle
    GetChild(idx int) (model.PageID, error)
    ChildCount() int
    ReplaceChild(idx int, newChildID model.PageID) (NodePageHandle, error)
    InsertEntry(idx int, key []byte, leftChild, rightChild model.PageID) (NodePageHandle, error)
}
```

### 5.3 BTreeStorage

```go
// storage.go

type BTreeStorage struct {
    pm *offheap.PageManager
}

func (s *BTreeStorage) AllocLeafPage() (model.PageID, error)
func (s *BTreeStorage) AllocNodePage() (model.PageID, error)
func (s *BTreeStorage) FreePage(pageID model.PageID) error
func (s *BTreeStorage) GetLeafPage(pageID model.PageID) (*LeafPageHandle, error)
func (s *BTreeStorage) GetNodePage(pageID model.PageID) (*NodePageHandle, error)
func (s *BTreeStorage) Close() error
```

### 5.4 BTree

```go
// btree.go

type BTree struct {
    rootRef  *RootPageRef
    storage  *BTreeStorage
    config   *model.BTreeConfig
    metrics  *BTreeMetrics
    size     atomic.Int64
    closed   atomic.Bool
}

// 实现 service.KVStore
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error)
func (b *BTree) Set(ctx context.Context, key, value []byte) error
func (b *BTree) Delete(ctx context.Context, key []byte) error
```

## 6. WriteOperation 模板方法

参考 Lealone 的 `PageOperations.WriteOperation`：

```
1. searchPath(key) → leafRef + path
2. leafRef.Lock()
3. oldInfo = leafRef.GetPageInfo()
4. oldPage = storage.GetLeafPage(oldInfo.pageID)
5. newPage = oldPage.Insert(key, value)  // COW：分配新页面
6. newInfo = &PageInfo{pageID: newPage.PageID(), version: oldInfo.version+1}
7. if leafRef.CAS(oldInfo, newInfo):
8.     propagateUpward(path)  // 沿 parentRef 链 CAS 更新祖先
9.     storage.FreePage(oldInfo.pageID)
10. else:
11.    storage.FreePage(newPage.PageID())
12.    goto 1  // 重试
```

**关键简化**：
- 无 Epoch 推进
- 无 SplitInfo 注册
- 无 PageRefCache 查找/更新
- 父节点更新是自然的向上 CAS 遍历

## 7. 分阶段实施计划

### Phase 0: 脚手架 ✅ 已完成（2026-04-02，提交 d9a6553）

**产出**：`btree2/` 目录骨架 + 错误类型 + 常量

- [x] 创建 `btree2/errors.go`：9 个哨兵错误（含 `ErrNotImplemented`）
- [x] 创建 `btree2/constants.go`：HeaderSize=56, MaxInternalKeys=126, PageSize=4096 等
- [x] 验证：`go build ./internal/infrastructure/storage/btree2/...` 通过，3 个测试通过

### Phase 1: BTreeStorage ✅ 已完成（2026-04-02，提交 d5d4c63）

**产出**：BTreeStorage 接口 + OffheapBTreeStorage COW 实现 + PageHandle 接口层级

- [x] 创建 `btree2/storage.go` — BTreeStorage 接口（含 Phase 6.5 Merge/Borrow 签名）
- [x] 创建 `btree2/page_handle.go` — PageHandle/LeafPage/NodePage 接口 + read-only stub 实现
- [x] 创建 `btree2/offheap_storage.go` — OffheapBTreeStorage（copyPage = alloc + memcpy 4096B + version++）
- [x] 创建 `btree2/storage_test.go` — 16 个测试
- [x] **不修改 offheap 包**（P0-3 决策：Go 侧 refCount 管理，不触碰 mmap 侧）
- [x] 添加 `checkOpen()` 守卫防止 Close 后操作
- [x] 验证（19 个测试全部通过，含 `-race`）：
  - `TestAllocLeafPage` / `TestAllocNodePage` — 页面类型正确
  - `TestAllocFreeBasic` — 分配/释放/重用循环
  - `TestCopyLeafPage` / `TestCopyNodePage` — COW 数据一致，pageID 不同
  - `TestCopyPageVersionIncrement` — dst.Version == src.Version + 1
  - `TestCopyLeafPageOriginalImmutable` — COW 后修改 dst 不影响 src
  - `TestGetLeafPage` / `TestGetNodePage` / `TestGetLeafPage_WrongType` / `TestGetNodePage_WrongType`
  - `TestClose` / `TestDoubleClose`
  - `TestPageIDValidation` — uint32 边界检查
  - `TestConcurrentAllocFree` — 多 goroutine 并发安全
  - `TestMergeLeavesStub` / `TestBorrowStubs` — Phase 6.5 stub

### Phase 2: LeafPageHandle ✅ 已完成（2026-04-02）

**产出**：COW 语义的叶子页操作

- [x] 修改 `btree2/page_handle.go` — leafPageHandle 持有 `*OffheapBTreeStorage`，替换 panic stub 为 COW 实现
- [x] 修改 `btree2/offheap_storage.go` — 所有 handle 创建点补充 `storage: s` 字段
- [x] 复用 `offheap.PageAccessor` API：SearchKey、InsertLeafEntry、OverwriteLeafValue、CollectKVExcept、BulkInitLeafFromSource
- [x] **Insert**: COW alloc → memcpy 4096B → Search → InsertLeafEntry，重复 key 返回 ErrDuplicateKey
- [x] **Update**: COW → OverwriteLeafValue（值更小/等大），否则降级为 CollectKVExcept + 重建 + 重插
- [x] **Delete**: CollectKVExcept → Alloc → InitLeafPage → 逐条 InsertLeafEntry
- [x] **Split**: Alloc×2 → BulkInitLeafFromSource×2（copy-up 语义），splitKey 复制提升
- [x] **Validate**: key 排序检查
- [x] **GetKey/GetValue**: 返回 `make([]byte) + copy` 的副本（P1-2 决策）
- [x] 创建 `btree2/leaf_page_test.go` — 21 个测试
- [x] 验证（48 个测试全部通过，含 `-race`）：
  - `TestLeafInsertSearch` / `TestLeafSearchMiss` — 插入 + 搜索命中/未命中
  - `TestLeafCOW` / `TestLeafCOWOriginalImmutable` — COW 后原页面不变
  - `TestLeafInsertKeyOrdering` — 逆序插入后 key 有序
  - `TestLeafUpdateValue` — Update 不改变 count
  - `TestLeafDeleteMiddle` / `TestLeafDeleteFirst` / `TestLeafDeleteLast` / `TestLeafDeleteNotFound`
  - `TestLeafSplit` / `TestLeafSplitKeyBoundary` / `TestLeafSplitEvenOdd` / `TestLeafSplitTooFew`
  - `TestLeafGetKeyReturnsCopy` / `TestLeafGetValueReturnsCopy`
  - `TestLeafIsFull` / `TestLeafCapacity`
  - `TestLeafDuplicateInsert` / `TestLeafInsertReverseOrder` / `TestLeafInsertEmptyKey`
  - `TestLeafValidate`

### Phase 3: NodePageHandle + Search ✅ 已完成（2026-04-02）

**产出**：索引页 COW 操作 + 简化版 resolvePath

- [x] 修改 `btree2/page_handle.go` — nodePageHandle 4 个 COW 方法实现（替换 panic stub）
- [x] 创建 `btree2/search.go` — ResolvedPath 类型 + resolvePath 导航（简化版，不含 PageRef）
- [x] 复用 `offheap.PageAccessor` API：SearchChildIndex、SetChild、InsertIndexEntry、BulkInitIndexFromSource
- [x] **ReplaceChild**: COW alloc → memcpy 4096B → SetChild，支持 idx==count 更新 extraChild
- [x] **InsertChild**: 两路分支算法 — idx<count: SetChild(idx, right) + InsertIndexEntry(idx, splitKey, left)；idx==count: InsertIndexEntry(count, splitKey, left) + SetChild(count+1, right)
- [x] **Split**: move-up 语义 — splitKey 从 left 和 right 中移除，提升到父节点。使用 BulkInitIndexFromSource 进行批量复制
- [x] **Validate**: key 排序检查 + ChildCount == Count+1 一致性检查
- [x] **Search 修复**: 精确匹配时返回 idx+1（右子树），而非 key index（idx）
- [x] 创建 `btree2/node_page_test.go` — 19 个测试
- [x] 验证（67 个测试全部通过，含 `-race`）：
  - `TestNodeSearchChildIndex` — key 落在各区间返回正确 child index（7 个子用例）
  - `TestNodeSearchEqualKey` — 精确匹配走右子树
  - `TestNodeReplaceChild` / `TestNodeReplaceChildExtraChild` / `TestNodeReplaceChildCOW` — COW 替换子页面
  - `TestNodeInsertChildMiddle` / `TestNodeInsertChildAtEnd` / `TestNodeInsertChildCOW` — 插入子页面
  - `TestNodeSplit` / `TestNodeSplitChildren` / `TestNodeSplitTooFew` — 内部节点分裂
  - `TestNodeValidate` / `TestNodeValidateEmpty` — 校验
  - `TestResolvePathSingleLeaf` / `TestResolvePathToLeaf` / `TestResolvePathNavigation` — 多层路径解析
  - `TestNodeInsertChildPreservesOtherKeys` — 插入不破坏已有数据
  - `TestNodeGetKeyFormat` / `TestNodeReplaceChildOutOfBounds` / `TestNodeInsertChildOutOfBounds` — 边界

### Phase 4: PageRef + RootPageRef ✅ 已完成

**产出**：CAS 可替换的引用链

- [x] `page_info.go` — 不可变 PageInfo
- [x] `page_ref.go` — atomic CAS + 引用计数管理
- [x] `root_ref.go` — root 特化：CAS + 子节点传播
- [x] `page_lock.go` — SchedulerLock（spin + TryLock）
- [x] 验证通过：
  - `TestPageRefCASSuccess` / `TestPageRefCASConflict`
  - `TestSchedulerLock` / `TestTryLock_*`（6 个测试）
  - `TestConcurrentCAS`

### Phase 5: BTree 核心 + WriteOperation ✅ 已完成

**产出**：Get/Set/Delete 实现（单叶子操作，不含 Split 传播）

- [x] `operations.go` — WriteOperation 模板方法
- [x] `btree.go` — BTree 结构体实现 service.KVStore
- [x] 验证通过（79% 测试覆盖率）：
  - `TestBTreeSetGet` / `TestBTreeUpdate` / `TestBTreeDelete`
  - `TestBTreeConcurrentSet` / `TestBTreeNoDataLoss`（并发数据一致性）
  - `TestBTreeClose` / `TestBTreeSize` / `TestBTreeStubMethods`
  - 所有测试通过（含 `-race`）

**当前代码质量评估**（2026-04-03）：
| 维度 | 评分 | 说明 |
|------|------|------|
| 并发安全性 | 9.5/10 ⭐⭐⭐⭐⭐ | 引用计数+CAS 设计优秀 |
| 资源管理 | 9.0/10 ⭐⭐⭐⭐⭐ | 无泄漏风险，defer 覆盖完整 |
| 错误处理 | 8.5/10 ⭐⭐⭐⭐ | 一致性好，1 个静默忽略问题 |
| 测试覆盖 | 8.0/10 ⭐⭐⭐⭐ | 功能测试完整，缺性能测试 |
| 性能 | 7.0/10 ⭐⭐⭐⭐ | 读卓越（13.8M ops/s），写有优化空间 |

**总体评分**: 8.4/10 ⭐⭐⭐⭐
**生产就绪度**: ✅ Phase 5 已就绪（单叶子操作范围内）

---

## 下一步：按需优化路线图

> ⚠️ **重要原则：不要过早优化！**
> 
> 参考：`thoughts/btree-code-review-round2-final-report-20260403.md`

### Phase 5.5: 性能基准测试基础设施（本周）

**目标**：建立性能测量基础设施，**不实施优化**

**任务**：
- [ ] 添加性能基准测试
  ```go
  // btree_test.go
  func BenchmarkBTreeSetSequential(b *testing.B)
  func BenchmarkBTreeSetParallel(b *testing.B)
  func BenchmarkBTreeGetSequential(b *testing.B)
  func BenchmarkBTreeGetParallel(b *testing.B)
  ```

- [ ] 添加性能监控指标
  ```go
  // metrics.go (新文件)
  type BTreeMetrics struct {
      ReadCount       atomic.Int64
      WriteCount      atomic.Int64
      CASRetryCount   atomic.Int64
      SplitCount      atomic.Int64
      MergeCount      atomic.Int64
  }
  ```

**验证**：
```bash
go test -bench=. -benchmem ./internal/infrastructure/storage/btree/...
```

### Phase 5.6: 观察与分析（下周）

**目标**：使用基准测试收集真实性能数据

**任务**：
- [ ] 运行性能测试（收集 1 周数据）
- [ ] 使用 pprof 分析瓶颈
  ```bash
  go test -bench=. -cpuprofile=cpu.prof -memprofile=mem.prof
  go tool pprof cpu.prof
  go tool pprof mem.prof
  ```
- [ ] 识别真正的热点（而非假设）

**决策点**：
- 如果 SearchPath 分配是热点 → 实施 P2 优化
- 如果 COW 复制是热点 → 考虑路径压缩
- 如果锁竞争是热点 → 考虑分片 BTree

### Phase 6.0: 功能完整性（1-2 个月后）

**目标**：实现多级树功能

**前置条件**：
- ✅ Phase 5.5 完成（有性能基准）
- ✅ Phase 5.6 完成（有瓶颈数据）

**任务**：
- [ ] Split 集成
  - 在 writeOperation 中添加 split 检测
  - 实现 propagateSplit 逻辑
  
- [ ] Merge 实现
  - 实现 RemoveChild 逻辑（已修复 panic → error）
  - 实现页面下溢检测

**验证**：
```bash
go test -run TestMultiLevelRandomOperations ./...
go test -run TestConcurrentSplitMerge ./...
```

### Phase 6.5: 性能优化（按需，2-3 个月后）

**目标**：基于真实瓶颈数据优化

**前置条件**：
- ✅ Phase 6.0 完成（功能完整）
- ✅ 有真实的性能问题数据

**可选优化**（仅在被证明需要时）：

1. **P2**: SearchPath 对象池（如果 pprof 显示是热点）
2. **P2**: SchedulerLock 超时（如果检测到死锁）
3. **P2**: CAS 退避策略（如果 CAS 冲突严重）
4. **P2**: 分片 BTree（如果并发扩展性不足）

---

## 原始计划（参考）

### Phase 6: Split 传播（2-3 天）

**产出**：叶子分裂 → 父节点更新 → 根分裂 → 新根创建

- [ ] 修改 `operations.go` — 分裂处理
- [ ] 修改 `btree.go` — `splitRoot` 方法
- [ ] 验证：
  - `TestSplitPropagation` — 触发叶子分裂，父节点更新
  - `TestRootSplit` — 根分裂，树高度增长
  - `TestMultiLevelSplit` — 3+ 层级联分裂
  - `TestConcurrentSplit` — 并发触发分裂
  - 移植 `root_split_stress_test.go` 场景

### Phase 6.5: Lazy Merge（2-3 天）

**产出**：删除触发的延迟页面合并，复用 Phase 6 的 parentRef 向上传播机制

**策略**：Lazy Merge — 删除后不立即合并，当页面利用率低于阈值时触发。避免高频 delete+insert 场景下反复分裂/合并的抖动。

- [ ] 在 `PageHandle` 接口补充 Merge 相关方法：
  ```go
  // 判断是否需要 merge（利用率 < 阈值）
  NeedMerge(threshold float64) bool
  // 与兄弟页面合并，返回新 PageHandle
  MergeWith(sibling PageHandle) (PageHandle, error)
  // 从兄弟页面借一个 key，返回两个新 PageHandle
  BorrowFromLeft(sibling PageHandle) (self, sib PageHandle, err error)
  BorrowFromRight(sibling PageHandle) (self, sib PageHandle, err error)
  ```
- [ ] 修改 `btree2/operations.go` — Delete 操作后检查 `NeedMerge`，触发向上传播
- [ ] 修改 `btree2/node_page.go` — 索引页 Merge：子节点合并后移除 entry，可能级联
- [ ] 修改 `btree2/btree.go` — `mergeRoot` 方法：根节点只剩一个子节点时降低树高度
- [ ] 验证：
  - `TestLeafMerge` — 两个半空叶子合并为一个满叶子
  - `TestLeafBorrowLeft` — 从左兄弟借 key
  - `TestLeafBorrowRight` — 从右兄弟借 key
  - `TestMergePropagation` — 叶子合并 → 父节点 entry 删除
  - `TestRootMerge` — 根节点降低树高度
  - `TestMergeThreshold` — 利用率高于阈值不触发合并
  - `TestConcurrentMerge` — 并发删除触发合并
  - `TestDeleteHalfKeysNoSpaceLeak` — 删除 50% keys 后验证页面数回落

### Phase 7: 调试基础设施（1-2 天）

**产出**：结构化调试工具

- [ ] 创建 `btree2/debug.go`
- [ ] 创建 `btree2/metrics.go`
- [ ] PrettyPagePrinter：`PrintTree()`, `PrintPage()` 返回 string
- [ ] AssertInvariants：key 排序、parentRef 一致、refCount ≥ 0
- [ ] BTreeMetrics：CASAttempts, CASConflicts, Splits, Retries 原子计数
- [ ] 验证：
  - `TestPrettyPrintFormat` — 输出格式正确
  - `TestAssertDetectsCorruption` — 注入错误，检测到

## 8. 复用 vs 重写

| 组件 | 决策 | 来源文件 |
|------|------|----------|
| offheap.PageAccessor | 原样复用 | `offheap/page_layout.go` |
| offheap.OffHeapMaterializer | 原样复用 | `offheap/materialize.go` |
| offheap.Allocator + LockFreeQueue | 原样复用 | `offheap/` |
| offheap.PageManager | **修改**（移除 epoch） | `offheap/page_manager.go` |
| model.BTreeConfig | 扩展 | `domain/model/btree_types.go` |
| service.KVStore | 实现 | `domain/service/storage.go` |
| 二分查找、KV 读写 | **移植改造** | `offheap_adapter.go` |
| 搜索遍历 | **重写** | `search_path.go` |
| 分裂处理 | **重写** | `leaf_lock_set.go` |
| COW 机制 | **重写** | `cow_delta_ref.go` |
| PageRef | **重写** | `page_ref.go` |
| Epoch | **移除** | 纯引用计数替代 |
| Delta Chain | **v2 延后** | 不在 v1 实现 |
| PageRefCache | **移除** | parentRef 链替代 |
| SplitInfo | **移除** | parentRef 链替代 |

## 9. 可调试性保证

| 机制 | 用途 | 替代 printf |
|------|------|-------------|
| `PrettyPagePrinter.PrintTree()` | 测试失败时打印树状态 | `t.Fatalf("tree:\n%s", printer.PrintTree(rootRef))` |
| `BTree.AssertInvariants()` | 每个测试后自动调用 | 不变式违反 = 测试失败，不需要肉眼检查 |
| 结构化错误 | 每个错误携带 key + pageID + version | 不需要翻日志定位 |
| `BTreeMetrics` | 原子计数器实时观察 | 不需要插入 print 语句 |
| 零 printf 规则 | 所有调试输出返回 string | 测试/日志框架决定去向 |

## 10. 验证方式

每个 Phase 完成后：

```bash
# 1. 编译
go build ./internal/infrastructure/storage/btree2/...

# 2. 测试
go test ./internal/infrastructure/storage/btree2/... -count=1 -race

# 3. Lint
golangci-lint run ./internal/infrastructure/storage/btree2/...

# 4. Phase 5+: 并发压力
go test -run TestBTreeConcurrentSet -count=20  # 20 轮无数据丢失
go test -run TestBTreeLargeDataset              # 10k+ keys
```

## 11. 总工期

| Phase | 工期 | 累计 | 状态 |
|-------|------|------|------|
| Phase 0: 脚手架 | 0.5 天 | 0.5 天 | ✅ 完成（d9a6553）|
| Phase 1: BTreeStorage | 1-2 天 | 1.5-2.5 天 | ✅ 完成（d5d4c63）|
| Phase 2: LeafPageHandle | 2-3 天 | 3.5-5.5 天 | ✅ 完成（2026-04-02）|
| Phase 3: NodePageHandle + Search | 2-3 天 | 5.5-8.5 天 | ✅ 完成（2026-04-02）|
| Phase 4: PageRef + RootPageRef | 1-2 天 | 6.5-10.5 天 | ✅ 完成（2026-04-03）|
| Phase 5: BTree 核心 | 3-4 天 | 9.5-14.5 天 | ✅ 完成（2026-04-03）|
| Phase 5.5: 性能基准基础设施 | 0.5 天 | 10-15 天 | ⏳ 待开始 |
| Phase 5.6: 观察与分析 | 1 周 | - | ⏸ 数据收集 |
| Phase 6: Split 传播 | 2-3 天 | 11.5-17.5 天 | 📋 计划中 |
| Phase 6.5: Lazy Merge | 2-3 天 | 13.5-20.5 天 | 📋 计划中 |
| Phase 6.5+: 性能优化 | 按需 | - | ⏸ 仅在需要时 |

**Phase 5 完成时间**：2026-04-03
**当前状态**：Phase 5 已就绪，等待 Phase 5.5（性能基准基础设施）

## 12. Out of Scope（btree2 不处理）

以下事项与 btree2 相关但不在 v1 scope 内，由其他组件负责：

| 事项 | 负责组件 | 与 btree2 的关系 |
|------|---------|-----------------|
| MVCC value 编码/解码 | `model.ValueEncoder`（domain 层） | BTree 只存 `[]byte`，不解析 value 内容 |
| 溢出页面管理（>2KB value） | `PageManager` 扩展（infrastructure 层） | 溢出页面由 ValueEncoder 直接调用 PageManager，不经过 BTreeStorage |
| 外部对象存储引用 | infrastructure 层 | BTree 只存引用指针（`[]byte`），不负责对象生命周期 |
| value 元数据对页面容量的影响 | 上层调用者 | 叶子 IsFull() 用空间计算，天然适应任意 value 大小 |
| RangeScan / Iterator | 后续版本 | v1 不实现（D8） |
| WAL / 持久化 | 独立模块 | btree2 纯内存引擎，WAL 在上层 |

**关键约束（D10）**：当 value 包含外部资源引用（溢出页面、对象存储指针）时，上层必须在调用 BTree.Delete 前先释放这些资源。BTree.Delete 只回收叶子条目的页面空间。

## 13. 参考资料

- 设计文档：`docs/07_spike/btree-refactor/2026-04-01-btree-refactor-design.md`
- Lealone 分析：`docs/07_spike/btree-refactor/2026-04-01-lealone-btree-deep-dive.md`
- Lealone 源码：`thoughts/Lealone/`
- 当前实现：`internal/infrastructure/storage/btree/`
