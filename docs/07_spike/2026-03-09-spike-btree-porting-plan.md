# Java Lealone BTree 到 Go NexKV 移植计划（修订版）

**创建时间**: 2026-03-09
**最后更新**: 2026-03-09（根据审核意见修订）
**目标**: 8-10 周完成核心功能，达到 Lealone BTree 性能水平
**策略**: 稳健路线 + 完整 CCOW + 深度 WAL 集成

**修订说明**:
- 原计划（4-6周激进路线）经审核评估为时间估算过于乐观
- 调整为8-10周稳健路线，增加技术验证阶段
- 增强测试策略和内存管理专项
- 调整性能目标为更保守的可达目标

---

## 1. 上下文（Context）

### 1.1 问题陈述

NexKV 项目当前仅有 WAL 存储实现，缺乏高性能的 KV 索引引擎。Lealone BTree 提供了经过验证的高性能实现，包括：
- **CCOW 机制**: 无锁读写并发，支持快照隔离
- **单写线程架构**: 消除锁竞争，简化并发控制
- **版本化根指针**: 支持 MVCC 和崩溃恢复
- **Delta Chain 优化**: 写放大因子降至 1.1-1.5

### 1.2 性能目标

基于 Lealone 实测数据和专家评审建议，我们的目标是（**更保守的可达成目标**）：

| 指标 | Lealone 实测 | 原计划目标 | **修订目标** | 可行性 |
|------|-------------|-----------|-----------|--------|
| 随机读延迟 | 941.61 ns/op | ≤ 950 ns/op | **≤ 1,000 ns/op** | ⭐⭐⭐⭐⭐ |
| 随机写延迟 | 1,596.01 ns/op | ≤ 1,600 ns/op | **≤ 2,000 ns/op** | ⭐⭐⭐⭐ |
| 随机读吞吐 | 1.07M ops/s | ≥ 1.0M ops/s | **≥ 800K ops/s** | ⭐⭐⭐⭐⭐ |
| 随机写吞吐 | 0.67M ops/s | ≥ 650K ops/s | **≥ 500K ops/s** | ⭐⭐⭐⭐ |
| 并发读扩展 | 线性 (4x) | 线性 (4x) | 线性 (4x) | ⭐⭐⭐⭐⭐ |
| 写放大因子 | 1.1-1.5 | ≤ 1.5 | **≤ 1.5** | ⭐⭐⭐⭐ |

**调整说明**:
- 写延迟目标放宽 25%（1,600 → 2,000 ns/op），考虑 Go GC 影响
- 写吞吐目标降低 23%（650K → 500K ops/s），更现实
- 读目标略微放宽，但仍然接近 Lealone 性能

**分阶段达成目标**:
- **MVP 版本**（Phase 3 完成）: 写 ≥ 300K ops/s, 读 ≥ 500K ops/s
- **优化版本**（Phase 5 完成）: 写 ≥ 500K ops/s, 读 ≥ 800K ops/s

### 1.3 技术挑战

1. **Java 到 Go 的语义差异**
   - Java: AtomicReferenceFieldUpdater + CAS
   - Go: atomic.Value + 不可变对象

2. **内存管理差异**
   - Java: 手动内存管理，GC 回收
   - Go: 自动 GC，需使用 sync.Pool 优化

3. **锁机制转换**
   - Java: ReentrantReadWriteLock
   - Go: sync.RWMutex

4. **CCOW 路径复制的无锁实现**
   - 需要确保读操作的完全无锁
   - 写操作的原子性保证

### 1.4 审核反馈（修订依据）

**总体评分**: 7.5/10

**主要问题**:
1. ❌ **时间估算过于乐观**: 4-6周 → 8-10周（专家评审建议）
2. ❌ **缺少技术验证阶段**: 需要先实现 Mini CCOW 原型
3. ⚠️ **内存管理考虑不足**: 需要增加专项任务
4. ⚠️ **并发安全测试不充分**: 需要专门的竞态检测
5. ⚠️ **性能目标过于激进**: 建议调整为更保守的目标

**关键风险识别**:
- 🔴 **最可能失败**: CCOW 路径复制实现（算法复杂度高）
- 🔴 **高风险**: Go GC 性能抖动（影响延迟稳定性）
- 🟡 **中风险**: 内存泄漏（引用计数错误）

**改进措施**（已纳入本修订版）:
1. ✅ 调整时间计划为8-10周
2. ✅ 新增 Phase 0.5 技术验证阶段
3. ✅ 增加内存管理专项任务
4. ✅ 增强并发安全测试
5. ✅ 调整性能目标为可达目标

---

## 2. 架构设计

### 2.1 模块结构

```
internal/infrastructure/storage/btree/
├── btree.go              # BTree 主入口
├── btree_config.go       # 配置定义
├── ccow.go               # CCOW 机制实现
├── page.go               # 页面管理
├── node.go               # 节点操作
├── path.go               # 路径复制逻辑
├── version.go            # 版本管理和快照
├── gc.go                 # 垃圾回收
├── iterator.go           # 范围扫描迭代器
├── serializer.go         # 二进制序列化
├── pool.go               # 对象池优化
└── wal_adapter.go        # WAL 集成适配器

internal/domain/service/
├── storage.go            # KVStore 接口扩展
└── btree.go              # BTree 特定接口

internal/domain/model/
├── storage.go            # 存储领域模型
└── btree_types.go        # BTree 类型定义

internal/infrastructure/storage/wal/
└── types.go              # [修改] 扩展 WAL 类型
```

### 2.2 核心接口设计

```go
// internal/domain/service/storage.go

// KVStore 统一存储接口（扩展）
type KVStore interface {
    // 基础 CRUD
    Get(ctx context.Context, key []byte) ([]byte, error)
    Set(ctx context.Context, key, value []byte) error
    Delete(ctx context.Context, key []byte) error

    // 范围查询
    RangeScan(ctx context.Context, start, end []byte) (Iterator, error)

    // 快照支持（CCOW）
    CreateSnapshot(ctx context.Context) (SnapshotID, error)
    ReleaseSnapshot(ctx context.Context, id SnapshotID) error

    // 异步支持（v4 模式）
    GetAsync(ctx context.Context, key []byte) model.Task[[]byte]
    SetAsync(ctx context.Context, key, value []byte) model.Task[struct{}]
}

// BTree BTree 索引接口（新增）
type BTree interface {
    KVStore

    // 统计信息
    Stats(ctx context.Context) (*BTreeStats, error)
}
```

### 2.3 数据流设计

```
┌─────────────┐
│ Client RPC  │
└──────┬──────┘
       │
       ▼
┌─────────────────────────────────────┐
│  Pipeline.Submit(BTreeInsertTask)   │
└──────┬──────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────┐
│  PerCoreExecutor (SourceBTree)      │
│  - CPU 亲和性调度                   │
│  - 10 级优先级队列                  │
└──────┬──────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────┐
│  BTree.InsertAsync (Task[struct{}]) │
│  1. WAL.AppendAsync                 │
│  2. Wait WAL 完成                   │
│  3. CCOW 路径复制                   │
│  4. 原子切换根指针                   │
└──────┬──────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────┐
│  task.Wait(ctx) 返回结果            │
└─────────────────────────────────────┘
```

---

## 3. 实施计划（8-10 周稳健路线）

### Phase 0: 准备阶段（3-5 天）

**目标**: 环境准备和基础架构搭建

**任务清单**:

1. **代码仓库设置**
   - [ ] 创建功能分支: `feature/btree-ccow-implementation`
   - [ ] 设置目录结构: `internal/infrastructure/storage/btree/`
   - [ ] 配置 Makefile 目标 (test, benchmark, coverage)

2. **依赖库准备**
   ```go
   // go.mod 新增依赖
   require (
       github.com/stretchr/testify v1.11.1    // 已有
   )
   ```

3. **接口定义**
   - [ ] 定义 `KVStore` 接口 (internal/domain/service/storage.go)
   - [ ] 定义 `BTree` 接口 (internal/domain/service/btree.go)
   - [ ] 定义核心数据结构 (internal/domain/model/btree_types.go)
   - [ ] 定义错误常量 (pkg/errors)

4. **测试框架搭建**
   - [ ] 创建 `base_test.go` (测试辅助函数)
   - [ ] 创建 `mock` 对象 (mockgen)
   - [ ] 设置基准测试框架

**交付物**:
- ✅ 完整的接口定义文件
- ✅ 测试框架代码
- ✅ Makefile 更新
- ✅ 接口文档

**关键文件**:
- `internal/domain/service/storage.go`
- `internal/domain/service/btree.go`
- `internal/domain/model/btree_types.go`

---

### Phase 0.5: 技术验证阶段（1 周）⭐ 新增

**目标**: 验证核心技术可行性，降低实施风险

**背景**:
经专家评审，CCOW 路径复制是最可能失败的环节。在全面实施前，必须先通过 Mini CCOW 原型验证核心技术可行性。

**任务清单**:

#### Day 1-2: Lealone 源码深度分析

- [ ] 阅读 Lealone BTree 关键源码文件
  - `BTreeMap.java` - 核心实现
  - `PageReference.java` - 页面引用管理
  - `RootPageReference.java` - 根节点引用
- [ ] 分析 CCOW 路径复制的具体实现
- [ ] 提取关键算法和设计模式
- [ ] 编写技术分析文档

**交付物**:
- ✅ Lealone 源码分析文档
- ✅ 关键算法提取和注释

#### Day 3-5: Mini CCOW 原型实现

```go
// 内部/试验性/mini_ccow.go

// MiniCCOW 最小可验证原型
type MiniCCOW struct {
    root atomic.Value // *RootNode
    mu   sync.RWMutex  // 仅用于写操作保护
}

type RootNode struct {
    Version int
    Data    map[string]string
}

func (m *MiniCCOW) Read(key string) (string, bool)
func (m *MiniCCOW) Write(key, value string) error
func (m *MiniCCOW) CopyOnWritePath(key, value string) error
```

**验证目标**:
- [ ] 无锁读操作正确性
- [ ] 路径复制算法正确性
- [ ] 原子切换根指针正确性
- [ ] 基本性能测试（1000 ops/s）

**测试要求**:
- [ ] 并发读写测试 (100 goroutines)
- [ ] 压力测试 (10K ops)
- [ ] 正确性验证

#### Day 6-7: sync.Pool 性能基准测试

- [ ] 实现 Page/Node 对象池
- [ ] 性能对比测试（pool vs new）
- [ ] 内存分配分析（逃逸分析）
- [ ] GC 压力测试

**测试代码**:
```go
func BenchmarkPageWithPool(b *testing.B)
func BenchmarkPageWithoutPool(b *testing.B)
func TestMemoryLeak(t *testing.T)
```

**性能基准**:
- pool 版本 ≥ new 版本性能的 80%
- 内存分配减少 ≥ 50%

#### Day 8-10: 风险评估和 Go/no-Go 决策

**风险评估检查清单**:
1. [ ] CCOW 路径复制算法实现可行？
2. [ ] sync.Pool 性能满足要求？
3. [ ] 并发安全无死锁/竞态？
4. [ ] 内存管理无泄漏风险？
5. [ ] 性能目标可达（初步判断）？

**Go/no-Go 决策标准**:
- ✅ 继续：所有关键风险可控
- ⚠️ 条件继续：部分风险需要缓解措施
- ❌ 终止：关键风险无法解决

**交付物**:
- ✅ Mini CCOW 原型代码
- ✅ 性能基准测试报告
- ✅ 风险评估报告
- ✅ Go/no-Go 决策文档

---

### Phase 1: 核心数据结构（1 周）

**目标**: 实现页面、节点等基础数据结构

**任务清单**:

#### Day 1-2: 页面管理

```go
// internal/infrastructure/storage/btree/page.go

type PageID uint64

type PageType uint8

const (
    LeafPage PageType = iota
    InternalPage
    RootPage
)

type Page struct {
    ID       PageID
    Version  uint64
    Type     PageType
    Data     []byte
    RefCount atomic.Int32
}

func (p *Page) Acquire()
func (p *Page) Release()
func (p *Page) IsLeaf() bool
```

**测试要求**:
- [ ] 单元测试: 页面创建、引用计数
- [ ] 并发测试: 多 goroutine Acquire/Release
- [ ] 覆盖率 > 85%

#### Day 3-4: 节点操作

```go
// internal/infrastructure/storage/btree/node.go

type Node struct {
    Page     *Page
    Keys     [][]byte
    Values   [][]byte  // 叶子节点
    Children []PageID  // 内部节点
    IsLeaf   bool
}

func (n *Node) Search(key []byte) int
func (n *Node) Insert(key, value []byte) error
func (n *Node) Split() (*Node, error)
func (n *Node) Merge(other *Node) error
func (n *Node) BinarySearch(key []byte) int
```

**测试要求**:
- [ ] 单元测试: 插入、分裂、合并
- [ ] 边界测试: 空节点、单节点、满节点
- [ ] 覆盖率 > 85%

#### Day 5: 序列化机制

```go
// internal/infrastructure/storage/btree/serializer.go

type BinarySerializer struct {
    compression CompressionType
}

func (s *BinarySerializer) MarshalPage(page *Page) ([]byte, error)
func (s *BinarySerializer) UnmarshalPage(data []byte) (*Page, error)
func (s *BinarySerializer) MarshalNode(node *Node) ([]byte, error)
func (s *BinarySerializer) UnmarshalNode(data []byte) (*Node, error)
```

**测试要求**:
- [ ] 序列化/反序列化正确性
- [ ] 压缩算法对比 (LZF vs DEFLATE)
- [ ] 性能基准测试

#### Day 6-7: 对象池优化

```go
// internal/infrastructure/storage/btree/pool.go

var (
    pagePool = sync.Pool{
        New: func() any {
            return &Page{
                Data: make([]byte, PageSize),
            }
        },
    }

    nodePool = sync.Pool{
        New: func() any {
            return &Node{
                Keys:   make([][]byte, 0, MaxKeys),
                Values: make([][]byte, 0, MaxKeys),
            }
        },
    }
)

func AcquirePage() *Page
func ReleasePage(page *Page)
func AcquireNode() *Node
func ReleaseNode(node *Node)
```

**测试要求**:
- [ ] 内存泄漏测试
- [ ] 性能对比测试 (pool vs new)
- [ ] 并发安全性测试

**交付物**:
- ✅ page.go + page_test.go
- ✅ node.go + node_test.go
- ✅ serializer.go + serializer_test.go
- ✅ pool.go + pool_test.go
- ✅ 基准测试结果

---

### Phase 2: CCOW 机制实现（3 周）⭐ 时间调整

**目标**: 实现完整 CCOW 并发控制

**时间调整说明**: 原计划1.5周 → 3周（经专家评审，CCOW路径复制是最可能失败的环节，需要充足时间）

**任务清单**:

#### Week 1, Day 1-3: 版本化根指针

```go
// internal/infrastructure/storage/btree/ccow.go

type RootInfo struct {
    RootID   PageID
    Version  uint64
    Created  time.Time
    WALSeqNum uint64
}

type VersionedRoot struct {
    current  atomic.Value // *RootInfo
    versions sync.Map     // map[uint64]*RootInfo (历史版本)
    gc       *BTreeGC
}

func (v *VersionedRoot) Get() *RootInfo
func (v *VersionedRoot) Update(newRoot PageID, walSeqNum uint64) error
func (v *VersionedRoot) CreateSnapshot() SnapshotID
func (v *VersionedRoot) ReleaseSnapshot(id SnapshotID) error
```

**测试要求**:
- [ ] 并发读写测试 (1000 goroutines)
- [ ] 版本切换原子性测试
- [ ] 快照创建/释放测试
- [ ] 覆盖率 > 90%

#### Week 1, Day 4-7: 路径复制实现

```go
// internal/infrastructure/storage/btree/path.go

type PathNode struct {
    Node    *Node
    PageID  PageID
    Level   int
}

type Path []*PathNode

func (b *BTree) FindPath(key []byte) (Path, error)
func (b *BTree) CopyPathBottomUp(path Path) (PageID, error)
func (b *BTree) CopyPage(old *Page) (*Page, error)
func (b *BTree) ModifyPage(page *Page, key, value []byte) error
```

**关键算法**:
```
CCOW 路径复制流程:
1. FindPath: 从根到叶查找路径 (只读)
2. CopyPathBottomUp: 从底向上复制
   a. 复制叶子页面
   b. 修改新页面 (插入/删除/更新)
   c. 复制父页面，更新子节点引用
   d. 递归向上直到根节点
3. Atomic Switch: 原子切换根指针
```

**测试要求**:
- [ ] 正确性测试: 单线程插入/删除
- [ ] 并发测试: 1000 goroutines 同时写入
- [ ] 压力测试: 10K ops/s 持续 1 分钟
- [ ] 覆盖率 > 90%

#### Week 2, Day 1-5: 路径复制增强和优化

- [ ] 处理边界条件（页面分裂、合并）
- [ ] 错误处理和重试机制
- [ ] 性能优化（减少内存拷贝）
- [ ] 并发安全性验证

**关键风险**: 这是整个计划的最复杂部分，预留充足时间进行调试和验证。

#### Week 2, Day 6-10: 垃圾回收（GC）

```go
// internal/infrastructure/storage/btree/gc.go

type BTreeGC struct {
    tree        *BTree
    maxVersions int
    stopChan    chan struct{}
    wg          sync.WaitGroup
}

func (g *BTreeGC) Start()
func (g *BTreeGC) Stop()
func (g *BTreeGC) Collect() error
func (g *BTreeGC) ReleaseOldVersions(keepVersion uint64)
```

**GC 策略**:
- 保留最近 N 个版本 (默认 N=10)
- 基于 WAL 序列号判断版本是否可释放
- 定期清理 (每 30 秒)
- 触发清理 (版本数超过阈值)

**测试要求**:
- [ ] 内存泄漏测试
- [ ] GC 触发时机测试
- [ ] 并发安全性测试
- [ ] 覆盖率 > 85%

#### Week 3: CCOW 并发安全专项测试 ⭐ 新增

**目标**: 确保并发安全无死锁、无竞态条件

**专项测试任务**:

1. **竞态条件检测** (2天)
   ```bash
   go test -race ./internal/infrastructure/storage/btree/...
   ```
   - [ ] 运行所有测试带上 -race 标志
   - [ ] 修复所有检测到的竞态条件
   - [ ] 验证修复后无竞态

2. **死锁检测** (1天)
   - [ ] 使用 `go-deadlock` 检测潜在死锁
   - [ ] 超时测试验证无死锁
   - [ ] 代码审查检查锁顺序

3. **饥饿测试** (1天)
   - [ ] 低优先级任务饥饿测试
   - [ ] 验证公平性
   - [ ] 调整优先级策略

4. **压力测试** (1天)
   - [ ] 1000 goroutines 同时读写
   - [ ] 持续 30 分钟压力测试
   - [ ] 内存泄漏检测

**交付物**:
- ✅ ccow.go + ccow_test.go
- ✅ path.go + path_test.go
- ✅ gc.go + gc_test.go
- ✅ 并发压力测试报告
- ✅ 竞态条件检测报告 ⭐

---

### Phase 3: BTree 核心逻辑（1.5 周）⭐ 时间调整

**目标**: 实现完整的 BTree CRUD 操作

**任务清单**:

#### Day 1-2: 查询操作

```go
// internal/infrastructure/storage/btree/btree.go

func (b *BTree) Search(ctx context.Context, key []byte) ([]byte, error) {
    // 1. 获取当前根版本 (无锁读)
    rootInfo := b.root.Get()

    // 2. 从根开始查找 (只读，无需加锁)
    current, err := b.pageManager.Get(rootInfo.RootID)
    if err != nil {
        return nil, err
    }

    // 3. 二分查找 (完全无锁)
    for !current.IsLeaf() {
        node := b.deserializeNode(current)
        idx := node.BinarySearch(key)

        childID := node.Children[idx]
        current, err = b.pageManager.Get(childID)
        if err != nil {
            return nil, err
        }
    }

    // 4. 返回结果
    node := b.deserializeNode(current)
    idx := node.BinarySearch(key)
    if idx < len(node.Keys) && bytes.Equal(node.Keys[idx], key) {
        return node.Values[idx], nil
    }

    return nil, ErrKeyNotFound
}
```

**测试要求**:
- [ ] 单线程查询测试
- [ ] 并发查询测试 (1000 goroutines)
- [ ] 延迟测试 (p50, p95, p99)
- [ ] 覆盖率 > 90%

#### Day 3-5: 插入操作

```go
func (b *BTree) Insert(ctx context.Context, key, value []byte) error {
    // 1. 参数校验
    if len(key) == 0 || len(value) == 0 {
        return ErrInvalidArgument
    }

    // 2. 检查容量
    if b.size.Load() >= b.config.MaxSize {
        return ErrTreeFull
    }

    // 3. 执行 CCOW 路径复制
    if err := b.copyOnWritePath(ctx, key, value); err != nil {
        return err
    }

    // 4. 更新计数
    b.size.Add(1)

    return nil
}

// 异步插入
func (b *BTree) InsertAsync(ctx context.Context, key, value []byte) model.Task[struct{}] {
    baseTask := model.NewBaseTask(
        model.OpStorage,
        model.TaskPriorityNormal,
        model.SourceBTree,
        func(ctx context.Context, pipeline model.PipelineContext) (struct{}, error) {
            return struct{}{}, b.Insert(ctx, key, value)
        },
    )

    return &BTreeInsertTask{
        BaseTask: baseTask,
    }
}

type BTreeInsertTask struct {
    *model.BaseTask[struct{}]
}
```

**测试要求**:
- [ ] 单线程插入测试
- [ ] 并发插入测试 (1000 goroutines)
- [ ] 边界测试: 重复键、空键、大键值
- [ ] 覆盖率 > 90%

#### Day 6: 删除操作

```go
func (b *BTree) Delete(ctx context.Context, key []byte) error {
    // 1. 执行 CCOW 路径复制 (标记删除)
    if err := b.copyOnWritePathDelete(ctx, key); err != nil {
        return err
    }

    // 2. 更新计数
    b.size.Add(-1)

    return nil
}
```

**测试要求**:
- [ ] 单线程删除测试
- [ ] 并发删除测试
- [ ] 边界测试: 删除不存在的键
- [ ] 覆盖率 > 90%

#### Day 7: 范围扫描

```go
// internal/infrastructure/storage/btree/iterator.go

type BTreeIterator struct {
    tree     *BTree
    snapshot *RootInfo
    stack    []*Node
    current  int
    start    []byte
    end      []byte
}

func (b *BTree) RangeScan(ctx context.Context, start, end []byte) (Iterator, error) {
    // 1. 创建快照 (无锁)
    rootInfo := b.root.Get()

    // 2. 创建迭代器
    iter := &BTreeIterator{
        tree:     b,
        snapshot: rootInfo,
        start:    start,
        end:      end,
    }

    // 3. 定位到起始位置
    if err := iter.Seek(start); err != nil {
        return nil, err
    }

    return iter, nil
}

func (it *BTreeIterator) Next() bool
func (it *BTreeIterator) Key() []byte
func (it *BTreeIterator) Value() []byte
func (it *BTreeIterator) Close() error
```

**测试要求**:
- [ ] 范围查询正确性
- [ ] 空范围测试
- [ ] 大范围扫描性能测试
- [ ] 覆盖率 > 85%

**交付物**:
- ✅ btree.go + btree_test.go
- ✅ iterator.go + iterator_test.go
- ✅ 性能测试报告

---

### Phase 4: WAL 集成（1 周）

**目标**: 深度集成现有 WAL 系统

**任务清单**:

#### Day 1-2: 扩展 WAL 类型

```go
// internal/infrastructure/storage/wal/types.go [修改]

const (
    // 现有类型
    WALTypeInsert    WALType = iota
    WALTypeUpdate
    WALTypeDelete
    WALTypeCommit
    WALTypeRollback

    // 新增 BTree 类型
    WALTypeBTreeSplit   WALType = 10
    WALTypeBTreeMerge
    WALTypeBTreeRootChange
    WALTypePageAlloc
    WALTypePageFree
)
```

#### Day 3-4: WAL 适配器

```go
// internal/infrastructure/storage/btree/wal_adapter.go

type BTreeWALAdapter struct {
    wal   wal.WAL
    tree  *BTree
}

func (a *BTreeWALAdapter) LogSplit(oldRoot, newRoot PageID) (wal.LSN, error)
func (a *BTreeWALAdapter) LogMerge(oldRoot, newRoot PageID) (wal.LSN, error)
func (a *BTreeWALAdapter) LogRootChange(oldRoot, newRoot PageID) (wal.LSN, error)
func (a *BTreeWALAdapter) LogPageAlloc(pageID PageID) (wal.LSN, error)
func (a *BTreeWALAdapter) LogPageFree(pageID PageID) (wal.LSN, error)
```

#### Day 5-7: 恢复机制

```go
func (b *BTree) Recover(ctx context.Context) error {
    // 1. 读取 WAL 日志
    entries, err := b.wal.Recover()
    if err != nil {
        return err
    }

    // 2. 重放日志
    for _, entry := range entries {
        if err := b.recoverEntry(entry); err != nil {
            return err
        }
    }

    // 3. 重建根指针
    return b.rebuildRoot()
}

func (b *BTree) recoverEntry(entry *wal.WALEntry) error {
    switch entry.Type {
    case WALTypeBTreeSplit:
        return b.recoverSplit(entry)
    case WALTypeBTreeMerge:
        return b.recoverMerge(entry)
    case WALTypeBTreeRootChange:
        return b.recoverRootChange(entry)
    // ...
    }
    return nil
}
```

**测试要求**:
- [ ] 崩溃恢复测试: 随机崩溃 + 验证数据完整性
- [ ] WAL 重放测试: 验证所有日志类型
- [ ] 并发恢复测试: 多个恢复任务
- [ ] 覆盖率 > 90%

**交付物**:
- ✅ wal_adapter.go + wal_adapter_test.go
- ✅ types.go [修改]
- ✅ 恢复机制测试报告

---

### Phase 5: 性能优化（2 周）⭐ 时间调整

**目标**: 达到 Lealone BTree 性能水平

**时间调整说明**: 原计划1周 → 2周（性能优化需要多次迭代）

**任务清单**:

#### Week 1, Day 1-3: 内存优化

- [ ] 页面对齐优化 (4KB 对齐)
- [ ] sync.Pool 优化 (预分配)
- [ ] 减少内存分配 (逃逸分析)
- [ ] 使用 `[PageSize]byte` 替代 `[]byte`
- [ ] **新增**: Go GC 参数调优

```go
// 优化前
type Page struct {
    Data []byte  // 动态分配
}

// 优化后
type Page struct {
    Data [PageSize]byte  // 静态分配，零拷贝
}
```

**Go GC 调优**:
```go
// 环境变量设置
// GOGC=100 // 增加 GC 触发阈值
// GODEBUG=gctrace=1 // GC 跟踪
```

#### Week 1, Day 4-5: 并发优化

- [ ] 减少 critical section
- [ ] 使用 atomic 操作优化统计
- [ ] 优化锁粒度 (页面级别锁)
- [ ] Sharding (多棵 BTree 降低竞争)

```go
// Sharding 优化
type ShardedBTree struct {
    shards    []*BTreeShard
    numShards int
}

func (sb *ShardedBTree) getShard(key []byte) *BTreeShard {
    hash := fnv.New32()
    hash.Write(key)
    idx := int(hash.Sum32()) % sb.numShards
    return sb.shards[idx]
}
```

#### Week 2, Day 1-2: 序列化优化

- [ ] 使用 unsafe 优化 (谨慎)
- [ ] 批量序列化
- [ ] 压缩算法选择 (Snappy vs LZ4 vs ZSTD)

```go
// unsafe 优化
func fastBytesToSlice(b []byte) []byte {
    return *(*[]byte)(unsafe.Pointer(&b))
}
```

#### Week 2, Day 3-4: Benchmark 和调优

- [ ] 运行完整基准测试
- [ ] pprof 分析 (CPU, Memory, Block, Mutex)
- [ ] 热点优化
- [ ] 回归测试
- [ ] **新增**: 性能目标验证

#### Week 2, Day 5: 性能回归测试建立 ⭐ 新增

- [ ] 建立性能基准测试体系
- [ ] 设置性能回归检测
- [ ] 持续性能监控

**性能基准（调整后更保守的目标）**:

```go
// btree_bench_test.go

func BenchmarkBTree_Search(b *testing.B) {
    // 目标: < 1,000 ns/op (原 950 ns/op)
}

func BenchmarkBTree_Insert(b *testing.B) {
    // 目标: < 2,000 ns/op (原 1,600 ns/op)
}

func BenchmarkBTree_ConcurrentRead(b *testing.B) {
    // 目标: ≥ 800K ops/s (原 1.0M ops/s)
}

func BenchmarkBTree_ConcurrentWrite(b *testing.B) {
    // 目标: ≥ 500K ops/s (原 650K ops/s)
}
```

**交付物**:
- ✅ 性能优化报告
- ✅ pprof 分析报告
- ✅ 基准测试结果
- ✅ 性能回归测试体系 ⭐

---

### Phase 6: 集成测试和文档（2 周）⭐ 时间调整

**目标**: 确保质量和可维护性

**时间调整说明**: 原计划0.5周 → 2周（测试是质量保证的关键）

**任务清单**:

#### Week 1: 增强测试

**Day 1-2: 模糊测试（Fuzzing）** ⭐ 新增
- [ ] 集成 `go-fuzz` 模糊测试器
- [ ] 随机输入测试
- [ ] 边界条件测试
- [ ] 崩溃测试

**Day 3-4: 内存泄漏专项测试** ⭐ 增强
- [ ] 使用 pprof 进行内存分析
- [ ] 长时间运行测试（4小时）
- [ ] 内存泄漏检测
- [ ] goroutine 泄漏检测

**Day 5: 集成测试**
- [ ] 端到端测试 (RPC → BTree → WAL)
- [ ] Pipeline 集成测试
- [ ] 压力测试 (10M ops)

**Week 2: 稳定性测试和文档**

**Day 1-2: 长时间稳定性测试**
- [ ] 24 小时稳定性测试
- [ ] 48 小时稳定性测试
- [ ] 内存监控
- [ ] 性能监控

**Day 3-4: 文档完善**
- [ ] API 文档 (godoc)
- [ ] 架构文档 (CCOW 机制详解)
- [ ] 性能测试报告
- [ ] 故障排查指南
- [ ] 迁移指南

**交付物**:
- ✅ 集成测试报告
- ✅ 模糊测试报告 ⭐
- ✅ 内存泄漏检测报告 ⭐
- ✅ 完整文档
- ✅ 性能测试报告

---

## 4. 关键文件清单

### 需要新建的文件

```
internal/infrastructure/storage/btree/
├── btree.go              # BTree 主入口
├── btree_config.go       # 配置定义
├── btree_test.go         # BTree 测试
├── ccow.go               # CCOW 机制
├── ccow_test.go          # CCOW 测试
├── page.go               # 页面管理
├── page_test.go          # 页面测试
├── node.go               # 节点操作
├── node_test.go          # 节点测试
├── path.go               # 路径复制
├── path_test.go          # 路径复制测试
├── version.go            # 版本管理
├── version_test.go       # 版本管理测试
├── gc.go                 # 垃圾回收
├── gc_test.go            # 垃圾回收测试
├── iterator.go           # 范围扫描
├── iterator_test.go      # 范围扫描测试
├── serializer.go         # 序列化
├── serializer_test.go    # 序列化测试
├── pool.go               # 对象池
├── pool_test.go          # 对象池测试
├── wal_adapter.go        # WAL 适配器
├── wal_adapter_test.go   # WAL 适配器测试
└── base_test.go          # 测试辅助函数

internal/domain/service/
├── storage.go            # [扩展] KVStore 接口
└── btree.go              # BTree 特定接口

internal/domain/model/
├── storage.go            # [扩展] 存储模型
└── btree_types.go        # BTree 类型定义

internal/infrastructure/storage/wal/
└── types.go              # [修改] 扩展 WAL 类型
```

### 需要修改的现有文件

```
internal/infrastructure/storage/wal/types.go
  - 新增 WALTypeBTreeSplit, WALTypeBTreeMerge, etc.

Makefile
  - 新增 make test-btree, benchmark-btree, coverage-btree

go.mod
  - 无需新增依赖 (使用现有依赖)
```

---

## 5. 技术方案详解

### 5.1 CCOW 路径复制算法

```
算法: CopyOnWritePath(key, value)
输入: key (要插入的键), value (要插入的值)
输出: error

步骤:
1. FindPath(key) → path
   - 从当前根开始，二分查找到叶子节点
   - 记录路径: [root, node1, node2, ..., leaf]
   - 完全只读操作，无需加锁

2. CopyPathBottomUp(path) → newRoot
   for i = len(path) - 1 to 0:
     a. oldPage = path[i]
     b. newPage = CopyPage(oldPage)
     c. if i == len(path) - 1:
          // 叶子节点: 插入键值对
          ModifyPage(newPage, key, value)
        else:
          // 内部节点: 更新子节点引用
          newPage.Children[idx] = path[i+1].newPageID
     d. path[i].newPage = newPage

   return path[0].newPageID

3. AtomicSwitch(newRoot)
   - 使用 atomic.Value.Store(newRoot)
   - 保证原子性

4. UpdateWAL(newRoot)
   - 异步写入 WAL

时间复杂度: O(log N * M)
- N: 树的高度
- M: 页面大小 (复制成本)

空间复杂度: O(log N * PageSize)
- 复制路径上的所有页面
```

### 5.2 版本化和快照

```
版本管理数据结构:

VersionedRoot {
  current: atomic.Value<*RootInfo>
  versions: sync.Map[uint64, *RootInfo]
}

RootInfo {
  RootID: PageID
  Version: uint64
  Created: time.Time
  WALSeqNum: uint64
  RefCount: atomic.Int32
}

快照创建:
1. current := root.current.Load().(*RootInfo)
2. snapshot := &Snapshot{RootInfo: current}
3. current.RefCount.Add(1)
4. register snapshot ID
5. return snapshot ID

快照释放:
1. snapshot.RootInfo.RefCount.Add(-1)
2. if RefCount == 0:
     - 从 versions map 删除
     - 触发 GC 回收页面

GC 策略:
- 定期扫描 versions map
- 删除 RefCount == 0 且超过保留时间的版本
- 回收不再被任何版本引用的页面
```

### 5.3 并发控制保证

```
读操作 (Search):
- 完全无锁
- 读取 current atomic.Value
- 访问页面 (引用计数保护)
- 不会阻塞任何写操作

写操作 (Insert/Delete):
- 单写线程 (通过 PerCoreExecutor SourceBTree 绑定)
- 路径复制 (不修改旧页面)
- 原子切换根指针
- 异步写 WAL

一致性保证:
- 读操作总是看到一致的快照
- 写操作原子切换 (all-or-nothing)
- WAL 保证持久性
- 版本化支持 MVCC
```

---

## 6. 测试策略

### 6.1 单元测试

**覆盖率要求**: > 85%

**测试结构**:
```
internal/infrastructure/storage/btree/
├── ccow_test.go           # CCOW 机制测试
├── page_test.go           # 页面管理测试
├── node_test.go           # 节点操作测试
├── path_test.go           # 路径复制测试
├── btree_test.go          # BTree 核心测试
├── serializer_test.go     # 序列化测试
├── version_test.go        # 版本管理测试
├── gc_test.go             # GC 测试
└── base_test.go           # 测试辅助函数
```

**关键测试用例**:

```go
// btree_test.go

func TestBTree_SimpleInsert(t *testing.T) {
    btree := setupTestBTree(t)
    defer btree.Close()

    key := []byte("test-key")
    value := []byte("test-value")

    err := btree.Insert(context.Background(), key, value)
    require.NoError(t, err)

    // 验证插入
    got, err := btree.Search(context.Background(), key)
    require.NoError(t, err)
    assert.Equal(t, value, got)
}

func TestBTree_ConcurrentInsert(t *testing.T) {
    btree := setupTestBTree(t)
    defer btree.Close()

    const numGoroutines = 100
    const opsPerGoroutine = 100

    var wg sync.WaitGroup
    for i := 0; i < numGoroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < opsPerGoroutine; j++ {
                key := []byte(fmt.Sprintf("key-%d-%d", id, j))
                value := []byte(fmt.Sprintf("value-%d", j))
                err := btree.Insert(context.Background(), key, value)
                assert.NoError(t, err)
            }
        }(i)
    }

    wg.Wait()

    // 验证数据完整性
    for i := 0; i < numGoroutines; i++ {
        for j := 0; j < opsPerGoroutine; j++ {
            key := []byte(fmt.Sprintf("key-%d-%d", i, j))
            value, err := btree.Search(context.Background(), key)
            assert.NoError(t, err)
            assert.NotNil(t, value)
        }
    }
}

func TestBTree_SnapshotIsolation(t *testing.T) {
    btree := setupTestBTree(t)
    defer btree.Close()

    // 插入初始数据
    key := []byte("test-key")
    value1 := []byte("value-1")
    err := btree.Insert(context.Background(), key, value1)
    require.NoError(t, err)

    // 创建快照
    snapshotID, err := btree.CreateSnapshot(context.Background())
    require.NoError(t, err)
    defer btree.ReleaseSnapshot(context.Background(), snapshotID)

    // 更新数据
    value2 := []byte("value-2")
    err = btree.Insert(context.Background(), key, value2)
    require.NoError(t, err)

    // 快照应该看到旧数据
    snapshotValue, err := btree.SearchSnapshot(context.Background(), snapshotID, key)
    require.NoError(t, err)
    assert.Equal(t, value1, snapshotValue)

    // 当前应该看到新数据
    currentValue, err := btree.Search(context.Background(), key)
    require.NoError(t, err)
    assert.Equal(t, value2, currentValue)
}
```

### 6.2 集成测试

**测试场景**:

1. **WAL 集成测试**
   ```go
   func TestBTree_WALIntegration(t *testing.T) {
       // 1. 创建 BTree + WAL
       btree := setupTestBTreeWithWAL(t)
       defer btree.Close()

       // 2. 执行操作
       key := []byte("test-key")
       value := []byte("test-value")
       err := btree.Insert(context.Background(), key, value)
       require.NoError(t, err)

       // 3. 模拟崩溃
       btree.Close()

       // 4. 恢复
       btree2, err := recoverBTree(t)
       require.NoError(t, err)
       defer btree2.Close()

       // 5. 验证数据
       value2, err := btree2.Search(context.Background(), key)
       require.NoError(t, err)
       assert.Equal(t, value, value2)
   }
   ```

2. **Pipeline 集成测试**
   ```go
   func TestBTree_PipelineIntegration(t *testing.T) {
       // 1. 创建 Pipeline + BTree
       executor := setupTestExecutor(t)
       pipeline := service.NewPipeline(context.Background(), executor)
       btree := setupTestBTreeWithPipeline(t, pipeline)

       // 2. 提交异步任务
       task := btree.InsertAsync(context.Background(), []byte("key"), []byte("value"))

       // 3. 等待完成
       result, err := task.Wait(context.Background())
       assert.NoError(t, err)
       assert.Equal(t, struct{}{}, result)

       // 4. 验证结果
       value, err := btree.Search(context.Background(), []byte("key"))
       assert.NoError(t, err)
       assert.Equal(t, []byte("value"), value)
   }
   ```

3. **崩溃恢复测试**
   ```go
   func TestBTree_CrashRecovery(t *testing.T) {
       // 1. 创建 BTree
       btree := setupTestBTreeWithWAL(t)

       // 2. 插入大量数据
       for i := 0; i < 10000; i++ {
           key := []byte(fmt.Sprintf("key-%d", i))
           value := []byte(fmt.Sprintf("value-%d", i))
           err := btree.Insert(context.Background(), key, value)
           require.NoError(t, err)
       }

       // 3. 随机崩溃
       if rand.Intn(2) == 0 {
           // 模拟崩溃 (不优雅关闭)
           runtime.Goexit()
       }

       // 4. 恢复
       btree2, err := recoverBTree(t)
       require.NoError(t, err)
       defer btree2.Close()

       // 5. 验证所有数据
       for i := 0; i < 10000; i++ {
           key := []byte(fmt.Sprintf("key-%d", i))
           value, err := btree2.Search(context.Background(), key)
           assert.NoError(t, err)
           assert.Equal(t, []byte(fmt.Sprintf("value-%d", i)), value)
       }
   }
   ```

### 6.3 性能测试

**基准测试**:

```go
// btree_bench_test.go

func BenchmarkBTree_Insert(b *testing.B) {
    btree := setupBenchmarkBTree(b)
    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := []byte(fmt.Sprintf("key-%d", i))
        value := []byte(fmt.Sprintf("value-%d", i))
        err := btree.Insert(ctx, key, value)
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkBTree_Search(b *testing.B) {
    btree := setupBenchmarkBTree(b)
    ctx := context.Background()

    // 预填充数据
    for i := 0; i < 1000000; i++ {
        key := []byte(fmt.Sprintf("key-%d", i))
        value := []byte(fmt.Sprintf("value-%d", i))
        btree.Insert(ctx, key, value)
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := []byte(fmt.Sprintf("key-%d", i%1000000))
        _, err := btree.Search(ctx, key)
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkBTree_ConcurrentRead(b *testing.B) {
    btree := setupBenchmarkBTree(b)
    ctx := context.Background()

    // 预填充数据
    for i := 0; i < 1000000; i++ {
        key := []byte(fmt.Sprintf("key-%d", i))
        value := []byte(fmt.Sprintf("value-%d", i))
        btree.Insert(ctx, key, value)
    }

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            key := []byte(fmt.Sprintf("key-%d", i%1000000))
            _, err := btree.Search(ctx, key)
            if err != nil {
                b.Fatal(err)
            }
            i++
        }
    })
}

func BenchmarkBTree_ConcurrentWrite(b *testing.B) {
    btree := setupBenchmarkBTree(b)
    ctx := context.Background()

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            key := []byte(fmt.Sprintf("key-%d", i))
            value := []byte(fmt.Sprintf("value-%d", i))
            err := btree.Insert(ctx, key, value)
            if err != nil {
                b.Fatal(err)
            }
            i++
        }
    })
}
```

**性能目标验证**（修订后的保守目标）:

```bash
# 运行基准测试
make benchmark-btree

# 预期输出（修订后的保守目标）
BenchmarkBTree_Search-8         1000000   1000 ns/op    # 目标: < 1,000 ns/op ⭐ 调整
BenchmarkBTree_Insert-8          500000   2000 ns/op    # 目标: < 2,000 ns/op ⭐ 调整
BenchmarkBTree_ConcurrentRead-8   800000   1250 ns/op    # 目标: ≥ 800K ops/s ⭐ 调整
BenchmarkBTree_ConcurrentWrite-8  500000   2000 ns/op    # 目标: ≥ 500K ops/s ⭐ 调整
```

**说明**: 性能目标已调整为更保守的可达目标（见第 1.2 节），降低失败风险。

---

## 7. 风险和缓解措施（修订版）

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **CCOW 实现复杂度高** | 高 | 高 | ⭐ Phase 0.5 技术验证 - Mini CCOW 原型先行验证 |
| **Go GC 性能影响** | 高 | 中 | ⭐ Phase 5.1 专项优化 - GC 参数调优 + 逃逸分析 |
| **内存泄漏** | 中 | 中 | ⭐ Phase 6.1 专项测试 - 4小时内存泄漏检测 |
| **并发安全 bug** | 高 | 中 | ⭐ Phase 2.W3 专项测试 - 竞态检测 + 死锁检测 |
| **性能不达标** | 高 | 中 | ⭐ 调整目标为保守值 - 更容易达成 |
| **时间估算不准确** | 中 | 中 | ⭐ 8-10周稳健路线 - 预留缓冲时间 |
| **WAL 集成困难** | 中 | 低 | 复用现有 WAL，最小化修改 |
| **技术验证失败** | 高 | 低 | ⭐ Phase 0.5 Go/no-Go 决策点 - 降低后续风险 |

**新增风险缓解措施**（基于审核反馈）：

1. **Phase 0.5 技术验证阶段** ⭐
   - Mini CCOW 原型验证核心算法可行性
   - sync.Pool 性能基准测试
   - Go/no-Go 决策点，及时止损

2. **并发安全专项测试** ⭐
   - Phase 2.W3: 竞态条件检测 (`go test -race`)
   - 死锁检测 (`go-deadlock`)
   - 饥饿测试和公平性验证
   - 压力测试 (1000 goroutines × 30分钟)

3. **内存管理专项** ⭐
   - Phase 6.1: 模糊测试 (go-fuzz)
   - 4小时内存泄漏检测
   - pprof 内存分析和逃逸分析
   - Go GC 参数调优

4. **性能目标调整** ⭐
   - 写延迟: 1,600 → 2,000 ns/op (+25%)
   - 写吞吐: 650K → 500K ops/s (-23%)
   - 更容易达成，降低失败风险

5. **时间缓冲** ⭐
   - 4-6周 → 8-10周 (+67% 时间)
   - Phase 2: 1.5周 → 3周 (+100%)
   - Phase 5: 1周 → 2周 (+100%)
   - Phase 6: 0.5周 → 2周 (+300%)

---

## 8. 验收标准

### 8.1 功能验收

- [ ] 完整的 CRUD 操作 (Create/Read/Update/Delete)
- [ ] 范围扫描 (Range Scan)
- [ ] 快照隔离 (Snapshot Isolation)
- [ ] 崩溃恢复 (Crash Recovery)
- [ ] WAL 持久化
- [ ] 版本管理和 GC
- [ ] 异步操作支持 (Task[Result])

### 8.2 性能验收（修订后的保守目标）

- [ ] 随机读延迟 ≤ 1,000 ns/op (p50) ⭐ 调整 (原 950)
- [ ] 随机写延迟 ≤ 2,000 ns/op (p50) ⭐ 调整 (原 1,600)
- [ ] 随机读吞吐 ≥ 800K ops/s ⭐ 调整 (原 1.0M)
- [ ] 随机写吞吐 ≥ 500K ops/s ⭐ 调整 (原 650K)
- [ ] 并发读线性扩展 (4x)
- [ ] 写放大因子 ≤ 1.5

**分阶段达成目标**:
- **MVP 版本**（Phase 3 完成）: 写 ≥ 300K ops/s, 读 ≥ 500K ops/s
- **优化版本**（Phase 5 完成）: 写 ≥ 500K ops/s, 读 ≥ 800K ops/s

### 8.3 质量验收

- [ ] 单元测试覆盖率 > 85%
- [ ] 集成测试通过率 100%
- [ ] 并发测试通过 (race detector)
- [ ] 内存泄漏检测通过
- [ ] 崩溃恢复测试通过
- [ ] 24 小时稳定性测试通过

---

## 9. 后续工作

### Phase 7: 分布式集成（可选，3 周）

如果时间允许或后续需求，可以添加：

1. **分布式 CCOW**
   - 副本同步协议
   - Raft 集成
   - 分布式事务

2. **性能优化**
   - Sharding (分片)
   - 缓存优化
   - 压缩算法优化

3. **高级特性**
   - 热点数据识别
   - 自适应页面大小
   - 智能预取

---

## 10. 总结（修订版）

本计划采用稳健路线，在 **8-10 周**内实现完整的 Java Lealone BTree 到 Go NexKV 的移植，包括：

- ✅ 完整 CCOW 机制 (路径复制 + 版本化 + GC)
- ✅ 深度 WAL 集成 (扩展 WAL 类型)
- ✅ **保守性能目标** (读 ≥ 800K ops/s, 写 ≥ 500K ops/s)
- ✅ **增强测试覆盖** (单元 + 集成 + 性能 + **模糊测试** + **内存泄漏检测**)

**关键改进**（基于专家审核反馈）：

1. **⭐ 新增 Phase 0.5: 技术验证阶段**
   - Mini CCOW 原型验证
   - sync.Pool 性能基准测试
   - Go/no-Go 决策点，降低实施风险

2. **⭐ 时间调整: 4-6周 → 8-10周**
   - Phase 2 CCOW 实现: 1.5周 → 3周
   - Phase 5 性能优化: 1周 → 2周
   - Phase 6 集成测试: 0.5周 → 2周
   - 充分缓冲，提高成功率

3. **⭐ 性能目标调整: 更保守可达**
   - 写延迟: 1,600 → 2,000 ns/op (+25%)
   - 写吞吐: 650K → 500K ops/s (-23%)
   - 降低失败风险，提高可达成性

4. **⭐ 增强测试策略**
   - 并发安全专项测试 (race detector + deadlock detection)
   - 模糊测试 (go-fuzz) 发现边界条件
   - 4小时内存泄漏检测
   - 24-48小时稳定性测试

**关键成功因素**:
1. ✅ Phase 0.5 技术验证先行，降低风险
2. ✅ 严格按照阶段实施，每个阶段都有明确的交付物
3. ✅ 充分的并发测试和性能测试（专项测试）
4. ✅ 代码 review 和重构
5. ✅ 持续的性能监控和优化

**预期挑战**:
1. CCOW 实现复杂度高 - **缓解**: Phase 0.5 Mini CCOW 原型验证
2. Go GC 性能抖动 - **缓解**: Phase 5.1 GC 参数调优 + 逃逸分析
3. 并发安全 bug - **缓解**: Phase 2.W3 专项并发测试
4. 内存泄漏风险 - **缓解**: Phase 6.1 专项内存检测

**修订依据**:
- 专家评审评分: 7.5/10
- 主要问题: 时间估算过于乐观、缺少技术验证、并发安全测试不足
- 关键风险: CCOW 路径复制实现（最可能失败）
- 改进措施: 新增技术验证阶段、增强测试、调整时间线和性能目标

---

**计划创建者**: Claude Code
**最后更新**: 2026-03-09
**状态**: 待用户审批
