# Page 级别锁设计方案

**作者**: Claude AI
**日期**: 2026-03-09
**状态**: 设计方案
**目标**: 实现 Lealone 风格的 Page 级别并发控制，提升写入 QPS 到 100 万+

---

## 📋 目录

1. [问题分析](#1-问题分析)
2. [Lealone 锁机制解析](#2-lealone-锁机制解析)
3. [技术方案设计](#3-技术方案设计)
4. [实现步骤](#4-实现步骤)
5. [性能预期](#5-性能预期)
6. [风险评估](#6-风险评估)

---

## 1. 问题分析

### 1.1 当前性能瓶颈

**测试结果** (2026-03-09):

| 场景 | QPS | 延迟 | 问题 |
|------|-----|------|------|
| 单线程写入 | 88.8 万 | 4,042 ns | 基线 |
| 并发写入 (4 线程) | ~86.4 万 | 2,080 ns | **无提升** ❌ |
| 单线程读取 | 1,213 万 | 240.6 ns | 优秀 ✅ |
| 并发读取 (4 线程) | 1,547 万 | 64.6 ns | **+27%** ✅ |

**关键发现**：
- ✅ 读取性能优秀，并发扩展性良好
- ❌ **写入并发完全失效**：4 线程并发 = 1 线程性能
- ❌ 根本原因：全局 CAS 竞争，只有一个写入者能成功

### 1.2 根本原因分析

**当前实现**：
```go
// version.go
func (v *VersionedRoot) Update(...) error {
    for range maxRetries {
        oldRootInfo := v.current.Load().(*RootInfo)
        newRootInfo := &RootInfo{...}

        // ❌ 所有线程竞争同一个 CAS
        if v.current.CompareAndSwap(oldRootInfo, newRootInfo) {
            return nil  // 只有一个线程成功
        }
        // 其他线程全部失败重试
    }
    return ErrRetry
}
```

**问题**：
- 只有一个 `current` 指针（全局根）
- 多个写入者竞争同一个 CAS
- 即使操作的是不同的键，也要串行化
- **锁粒度：整个 BTree** ❌

### 1.3 内存分配瓶颈

**CPU Profile 分析**：

```
97.83% 的内存分配来自 Node.Clone()：
- Node.Clone() → 15KB/op
- 每次写入都克隆整个节点
- 即使只修改一个键值对
```

**对比 Lealone**：
- Page 级别的锁（4KB）
- 只有被修改的 Page 需要复制
- 不同 Page 可以并发修改

---

## 2. Lealone 锁机制解析

### 2.1 分层锁结构

```
Level 0: 全局锁（ReentrantReadWriteLock）
    ├── sharedLock: 用于 save/GC/RedoLog
    └── exclusiveLock: 用于 clear/remove/close/repair
    使用频率：极低（只在管理操作时）

Level 1: 页面锁（PageLock）✅ 关键
    ├── 读锁：用于读取页面
    └── 写锁：用于修改页面
    使用频率：极高（每次 put/get）

Level 2: 调度器锁（SchedulerLock）
    └── 用于单写线程协调
```

### 2.2 并发写入模型

**Lealone put() 操作**：

```java
public V put(K key, V value) {
    // 1. 获取当前根
    Page root = rootRef.getOrReadPage();

    // 2. 遍历到叶子 Page（无锁）
    Page leaf = gotoLeafPage(root, key);

    // 3. 获取 Page 级别的写锁
    leaf.lock();  // ← 只锁这个 Page

    // 4. 修改 Page
    leaf.put(key, value);

    // 5. 释放 Page 锁
    leaf.unlock();

    return null;
}
```

**并发场景**：

```
时间线：

t0: 线程1-3 并发调用 put()

t1: 线程1: put(k1=v1) → 锁 Page[0] → 修改 Page[0]
    线程2: put(k2=v2) → 锁 Page[1] → 修改 Page[1]
    线程3: put(k3=v3) → 锁 Page[2] → 修改 Page[2]
    → 三个线程完全并发执行！✅

t2: 线程1 完成，释放 Page[0] 锁
     线程2 完成，释放 Page[1] 锁
     线程3 完成，释放 Page[2] 锁
```

### 2.3 为什么 Page 级别锁有效？

**关键洞察**：
1. **BTree 的特性**：不同键在不同的叶子节点
2. **空间局部性**：相邻的键可能在同一节点，但远距离的键在不同节点
3. **锁粒度**：Page (4KB) 比 Node (15KB) 更细粒度

**示例**：

```
BTree 结构（3层）：

                    Root [Page 0]
                   /              \
           Internal [Page 1]    Internal [Page 2]
          /    |    \            |    \
    Leaf[3] Leaf[4] Leaf[5]   Leaf[6] Leaf[7] Leaf[8]

并发写入：
- 线程1: 修改 Leaf[3] → 只锁 Page[3] ✅
- 线程2: 修改 Leaf[7] → 只锁 Page[7] ✅
- 线程3: 修改 Leaf[3] → 等待线程1，然后锁 Page[3] ⏳

→ 空间上分散的写入可以并发！
```

---

## 3. 技术方案设计

### 3.1 架构对比

#### 当前架构（全局 CAS）

```
┌─────────────────────────────────────────┐
│         BTree (全局)                    │
│  ┌─────────────────────────────────┐   │
│  │ Root Node (15KB)                │   │
│  │  - Keys: [k1, k2, k3, ...]     │   │
│  │  - Values: [v1, v2, v3, ...]   │   │
│  └─────────────────────────────────┘   │
│           ↓                            │
│  atomic.Value (CAS)                   │
│    ↓                                   │
│  所有写入竞争一个 CAS ❌               │
└─────────────────────────────────────────┘

问题：
- 单一全局状态
- 所有写入串行化
- 无法并发
```

#### 目标架构（Page 级别锁）

```
┌─────────────────────────────────────────┐
│         BTree (多层)                    │
│                                          │
│  ┌────────┐  ┌────────┐  ┌────────┐   │
│  │Page[0] │  │Page[1] │  │Page[2] │   │
│  │ Root   │  │Internal│  │Internal│   │
│  │        │  │        │  │        │   │
│  └───┬────┘  └───┬────┘  └───┬────┘   │
│      │           │           │          │
│  ┌───┴────┐  ┌───┴────┐  ┌───┴───┐   │
│  │Page[3] │  │Page[4] │  │Page[5]│   │
│  │Leaf    │  │Leaf    │  │Leaf   │   │
│  │Lock A  │  │Lock B  │  │Lock C │   │
│  └────────┘  └────────┘  └───────┘   │
│                  ↑                   │
│         不同 Page 可以并发写 ✅       │
└─────────────────────────────────────────┘

优势：
- 多个 Page 可以并发修改
- 锁粒度：Page (4KB)
- 空间分散的写入可并发
```

### 3.2 核心设计

#### 3.2.1 Page 结构

```go
// Page 代表 BTree 的一个节点页（磁盘页或内存页）
type Page struct {
    // === 固定部分 (4KB) ===
    id       model.PageID
    typ      model.PageType
    version  uint64

    // === 可变部分 ===
    mu       sync.RWMutex  // Page 级别的读写锁 ✅
    refCount atomic.Int32  // 引用计数

    // === 数据区域 ===
    // 使用 Node 的序列化数据
    nodeData *Node

    // === 元数据 ===
    dirty     bool      // 是否被修改（需要刷盘）
    pinCount  int32     // 钉计数（防止被换出）
}

// 关键方法
func (p *Page) Lock() {
    p.mu.Lock()
}

func (p *Page) Unlock() {
    p.mu.Unlock()
}

func (p *Page) RLock() {
    p.mu.RLock()
}

func (p *Page) RUnlock() {
    p.mu.RUnlock()
}
```

#### 3.2.2 BTree 节点与 Page 的映射

```go
// BTree 从纯内存改为 Page 层架构
type BTree struct {
    config      *model.BTreeConfig
    closed      bool

    // === Page 管理器 ===
    pageManager *PageManager

    // === 版本控制 ===
    root        *VersionedRoot  // 指向 Root Page

    // === 对象池 ===
    nodeCache   *nodeCache
}
```

#### 3.2.3 Page 管理器

```go
type PageManager struct {
    nextPageID atomic.Uint64

    // Page 缓存（已加载的 Page）
    pages sync.Map  // map[PageID]*Page

    // Page 锁管理器
    lockManager *PageLockManager
}

type PageLockManager struct {
    // 支持多 Page 并发锁
    locks sync.Map
}

func (pm *PageManager) GetPage(id model.PageID) (*Page, error) {
    // 从缓存获取或加载 Page
    if page, ok := pm.pages.Load(id); ok {
        return page.(*Page), nil
    }

    // 加载 Page（实现略）
    return pm.loadPage(id)
}

func (p *Page) Write(key, value []byte) error {
    p.Lock()
    defer p.Unlock()

    // 修改 Node 数据
    return p.nodeData.Insert(key, value)
}
```

### 3.3 写操作流程

**完整流程**：

```
1. 客户端调用: Set(key, value)
         ↓
2. BTree.Set()
         ↓
3. 查找路径: Root → Internal → Leaf
         ↓
4. 定位到目标 Page (Leaf Page)
         ↓
5. 获取 Page 写锁: page.Lock()  ✅ 只锁这个 Page
         ↓
6. 修改 Page 数据: page.Write(key, value)
         ↓
7. 释放 Page 锁: page.Unlock()
         ↓
8. 返回成功
```

**关键点**：
- 步骤 3-4：路径查找（无锁，读取）
- 步骤 5-7：Page 修改（加锁，写入）
- **不同 Page 的写入可以并发执行**

---

## 4. 实现步骤

### 阶段 1：Page 层基础设施（1-2 天）

**目标**：建立 Page 级别的数据结构和管理机制

#### 1.1 Page 结构定义

```go
// internal/infrastructure/storage/page/page.go

package page

import (
    "sync"
    "sync/atomic"

    "github.com/jzhang405/NexKV/internal/domain/model"
)

const (
    PageDataSize = 4096  // 4KB per page
)

type Page struct {
    // === 固定元数据 ===
    id       model.PageID
    typ      model.PageType
    version  uint64

    // === 并发控制 ===
    mu       sync.RWMutex  // Page 级别锁 ✅
    refCount atomic.Int32

    // === 数据内容 ===
    // Option 1: 直接存储 Node (简单)
    nodeData *Node

    // Option 2: 原始字节 (更灵活)
    data [PageDataSize]byte

    // === 状态管理 ===
    dirty    bool     // 是否需要刷盘
    pinCount int32    // 钉计数
}

func NewPage(id model.PageID, typ model.PageType) *Page {
    return &Page{
        id:      id,
        typ:     typ,
        nodeData: NewNode(typ == model.LeafPage),
    }
}

// === 锁操作 ===

func (p *Page) Lock() {
    p.mu.Lock()
}

func (p *Page) Unlock() {
    p.mu.Unlock()
}

func (p *Page) RLock() {
    p.mu.RLock()
}

func (p *Page) RUnlock() {
    p.mu.RUnlock()
}

// === 引用计数 ===

func (p *Page) Acquire() {
    p.refCount.Add(1)
}

func (p *Page) Release() int32 {
    return p.refCount.Add(-1)
}

// === 数据操作 ===

func (p *Page) Write(key, value []byte) error {
    p.Lock()
    defer p.Unlock()

    p.dirty = true
    return p.nodeData.Insert(key, value)
}

func (p *Page) Read(key []byte) ([]byte, error) {
    p.RLock()
    defer p.RUnlock()

    return p.nodeData.Get(key)
}
```

#### 1.2 Page 管理器

```go
// internal/infrastructure/storage/page/manager.go

package page

import (
    "sync"
    "sync/atomic"

    "github.com/jzhang405/NexKV/internal/domain/model"
)

type PageManager struct {
    nextPageID atomic.Uint64

    // Page 缓存
    pages sync.Map  // map[PageID]*Page

    // 对象池
    pagePool sync.Pool
}

func NewPageManager() *PageManager {
    return &PageManager{
        pagePool: sync.Pool{
            New: func() any {
                data := [PageDataSize]byte{}
                return &Page{data: data}
            },
        },
    }
}

// Allocate 分配新 Page
func (pm *PageManager) Allocate(typ model.PageType) (*Page, error) {
    id := model.PageID(pm.nextPageID.Add(1))

    page := NewPage(id, typ)
    pm.pages.Store(id, page)

    return page, nil
}

// Get 获取 Page（从缓存或加载）
func (pm *PageManager) Get(id model.PageID) (*Page, error) {
    if page, ok := pm.pages.Load(id); ok {
        return page.(*Page), nil
    }

    // TODO: 从磁盘加载
    return nil, fmt.Errorf("page not found: %d", id)
}

// Release 释放 Page 回缓存
func (pm *PageManager) Release(page *Page) {
    if refCount := page.Release(); refCount == 0 {
        // 可以考虑放回对象池
    }
}
```

### 阶段 2：BTree 集成 Page 层（2-3 天）

**目标**：将 BTree 从纯内存改造为 Page 层架构

#### 2.1 BTree 结构调整

```go
// internal/infrastructure/storage/btree/btree.go

type BTree struct {
    config      *model.BTreeConfig
    closed      bool

    // === Page 管理 ===
    pageManager *page.PageManager

    // === 根 Page（持久化）===
    rootPageID  model.PageID
    root        *VersionedRoot

    // === 内存缓存 ===
    nodeCache   *nodeCache
}
```

#### 2.2 路径查找（支持 Page）

```go
// FindPathWithPages 查找键路径，返回 Page 而非 Node
func (b *BTree) FindPathWithPages(key []byte) (PagePath, error) {
    path := make(PagePath, 0, 10)

    // 从根 Page 开始
    currentPage, err := b.pageManager.Get(b.rootPageID)
    if err != nil {
        return nil, err
    }
    defer currentPage.Release()

    for {
        path = append(path, &PagePathNode{
            Page:  currentPage,
            Level: len(path),
        })

        // 如果是叶子 Page，结束
        if currentPage.IsLeaf() {
            break
        }

        // 查找子 Page
        childPageID := currentPage.FindChild(key)
        childPage, err := b.pageManager.Get(childPageID)
        if err != nil {
            return nil, err
        }

        currentPage.Release()  // 释放父 Page
        currentPage = childPage
    }

    return path, nil
}
```

#### 2.3 Page 级别的写入

```go
// Set 使用 Page 级别锁写入
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    if b.closed {
        return ErrClosed
    }

    // 1. 查找路径
    path, err := b.FindPathWithPages(key)
    if err != nil {
        return err
    }
    defer path.Release()

    // 2. 获取目标 Page（叶子 Page）
    leafPage := path.Last().Page

    // 3. 加 Page 写锁（只锁这个 Page）✅
    leafPage.Lock()
    defer leafPage.Unlock()

    // 4. 写入数据
    if err := leafPage.Write(key, value); err != nil {
        return fmt.Errorf("page write failed: %w", err)
    }

    // 5. 如果 Page 满，需要分裂（TODO: 阶段3）
    if leafPage.IsFull() {
        return b.handleSplit(path, key, value)
    }

    return nil
}
```

### 阶段 3：Page 分裂与合并（3-4 天）

**目标**：实现 Page 的分裂和合并，支持多层 BTree

#### 3.1 Page 分裂

```go
// SplitPage 分裂满 Page
func (b *BTree) SplitPage(fullPage *Page, parentPath PagePath) error {
    fullPage.Lock()
    defer fullPage.Unlock()

    // 1. 创建新 Page
    newPage, err := b.pageManager.Allocate(model.LeafPage)
    if err != nil {
        return err
    }

    // 2. 分裂数据
    mid := len(fullPage.nodeData.Keys) / 2
    newPage.nodeData.Keys = fullPage.nodeData.Keys[mid:]
    newPage.nodeData.Values = fullPage.nodeData.Values[mid:]
    fullPage.nodeData.Keys = fullPage.nodeData.Keys[:mid]
    fullPage.nodeData.Values = fullPage.nodeData.Values[:mid]

    // 3. 更新父 Page
    if len(parentPath) == 0 {
        // 根 Page 分裂，创建新根
        return b.splitRoot(fullPage, newPage)
    }

    parentPage := parentPath[len(parentPath)-2].Page
    parentPage.Lock()
    defer parentPage.Unlock()

    // 将新 Page 插入父节点
    return parentPage.InsertChild(newPage.id, newPage)
}
```

### 阶段 4：性能优化（2-3 天）

**目标**：优化 Page 锁的获取和释放

#### 4.1 锁获取优化

```go
// LockManager 管理 Page 锁的获取
type LockManager struct {
    // 锁排序（避免死锁）
    lockOrder sync.Map

    // 等待队列
    waitQueues sync.Map
}

// AcquireLocks 按顺序获取多个 Page 锁（避免死锁）
func (lm *LockManager) AcquireLocks(pageIDs []model.PageID) []*Page {
    // 按 PageID 排序，确保锁获取顺序一致
    sort.Slice(pageIDs, func(i, j int) bool {
        return pageIDs[i] < pageIDs[j]
    })

    pages := make([]*Page, len(pageIDs))
    for i, id := range pageIDs {
        page, _ := b.pageManager.Get(id)
        page.Lock()
        pages[i] = page
    }

    return pages
}
```

#### 4.2 锁降级（减少锁竞争）

```go
// TryLock 尝试获取锁（非阻塞）
func (p *Page) TryLock() bool {
    return p.mu.TryLock()
}

// SetWithTimeout 带超时的写入
func (p *Page) SetWithTimeout(key, value []byte, timeout time.Duration) error {
    timer := time.NewTimer(timeout)
    defer timer.Stop()

    for {
        if p.TryLock() {
            defer p.Unlock()
            return p.nodeData.Insert(key, value)
        }

        select {
        case <-timer.C:
            return ErrTimeout
        case <-time.After(time.Microsecond * 100):
            // 退避 100μs 后重试
        }
    }
}
```

---

## 5. 性能预期

### 5.1 理论分析

**当前性能**：
- 单线程写入：88.8 万 QPS
- 并发写入：86.4 万 QPS（4 线程）

**Page 级别锁后**：

| 场景 | 理论 QPS | 提升倍数 |
|------|---------|---------|
| 单线程写入 | 90 万 QPS | 1.0x（持平） |
| 并发写入（2 线程，不同 Page） | 150 万 QPS | **1.7x** |
| 并发写入（4 线程，不同 Page） | 300 万 QPS | **3.4x** |
| 并发写入（8 线程，不同 Page） | 500 万 QPS | **5.7x** |

**假设条件**：
- 锁获取/释放开销：100 ns
- Page 写入开销：3 μs
- 锁竞争概率：< 10%（不同 Page）

### 5.2 实测预期

**保守估计**：

| 并发度 | 当前 QPS | 目标 QPS | 提升 |
|-------|---------|---------|------|
| 1 线程 | 88 万 | 90 万 | +2% |
| 2 线程 | 86 万 | **150 万** | **+74%** |
| 4 线程 | 86 万 | **250 万** | **+191%** |
| 8 线程 | - | **400 万** | **+365%** |

**乐观估计**（理想场景，键均匀分布）：

| 并发度 | 当前 QPS | 目标 QPS | 提升 |
|-------|---------|---------|------|
| 2 线程 | 86 万 | 200 万 | +133% |
| 4 线程 | 86 万 | 400 万 | +365% |
| 8 线程 | - | 700 万 | +714% |

---

## 6. 风险评估

### 6.1 技术风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **死锁** | 高 | 中 | 锁排序、超时机制 |
| **锁竞争** | 中 | 低 | 退避重试、锁分离 |
| **内存增加** | 低 | 中 | Page 缓存限制 |
| **复杂性增加** | 中 | 高 | 分阶段实施 |

### 6.2 实施风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **开发周期长** | 中 | 中 | 分阶段，每个阶段可独立验证 |
| **回归测试** | 高 | 低 | 完整的测试套件 |
| **性能不达标** | 中 | 中 | 基准测试驱动 |

### 6.3 对比 Lealone

**NexKV 的劣势**：
- Go 锁机制不如 Java 细粒度
- GC 开销可能更高
- 没有成熟的 Delta Chain

**NexKV 的优势**：
- 语言简洁，易于维护
- 编译型语言，性能可预测
- 无 JVM 开销

---

## 7. 实施计划

### Phase 1: Page 基础设施（1-2 周）
- [ ] Page 结构定义
- [ ] PageManager 实现
- [ ] 对象池管理
- [ ] 单元测试

### Phase 2: BTree 集成（2-3 周）
- [ ] FindPathWithPages 实现
- [ ] Page 级别的 Set/Get
- [ ] 版本控制集成
- [ ] 集成测试

### Phase 3: 分裂与合并（3-4 周）
- [ ] Page 分裂逻辑
- [ ] Page 合并逻辑
- [ ] 多层 BTree 支持
- [ ] 端到端测试

### Phase 4: 性能优化（1-2 周）
- [ ] 锁获取优化
- [ ] 退避重试
- [ ] 对象池优化
- [ ] 基准测试验证

### Phase 5: 压力测试（1 周）
- [ ] 并发压力测试
- [ ] 稳定性测试
- [ ] 性能调优
- [ ] 文档完善

**总计：8-12 周**

---

## 8. 成功标准

### 8.1 功能指标

- ✅ 支持多层 BTree（自动分裂）
- ✅ Page 级别的读写锁
- ✅ 并发写入无死锁
- ✅ 向后兼容现有 API

### 8.2 性能指标

| 指标 | 当前 | 目标 | 验收标准 |
|------|------|------|---------|
| 并发写入 QPS (4 线程) | 86 万 | **200 万+** | 基准测试 |
| 并发写入 QPS (8 线程) | - | **350 万+** | 基准测试 |
| 读取 QPS (4 线程) | 1,547 万 | **1,500 万+** | 保持性能 |
| 锁竞争率 | - | < 20% | pprof 分析 |

### 8.3 稳定性指标

- ✅ 并发测试 1 小时无死锁
- ✅ 内存泄漏检测通过
- ✅ 数据竞态检测通过 (`-race`)

---

## 9. 附录

### 9.1 参考资料

- [Lealone BTree 源码](https://github.com/lealone/Lealone)
- [CCOW 论文](https://www.cidrdb.org/cidr2005/papers-P29.pdf)
- [B+Tree 并发控制研究](https://www.vldb.org/pvldb/vldb10/p123-gong.pdf)

### 9.2 术语表

| 术语 | 解释 |
|------|------|
| **Page** | 固定大小的数据块（通常 4KB） |
| **Page 级别锁** | 锁的粒度为单个 Page |
| **CCOW** | Copy-On-Write，写时复制 |
| **锁粒度** | 锁保护的数据范围 |
| **死锁** | 多个线程互相等待对方释放锁 |
| **锁竞争** | 多个线程竞争同一个锁 |

### 9.3 代码示例

**Page 级别锁完整示例**：

```go
// 并发写入示例
func main() {
    btree, _ := OpenBTree("data", nil)
    defer btree.Close()

    ctx := context.Background()

    // 模拟 100 个并发写入
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()

            key := []byte(fmt.Sprintf("key-%d", id))
            value := []byte(fmt.Sprintf("value-%d", id))

            // Page 级别的锁：不同 Page 可以并发
            err := btree.Set(ctx, key, value)
            if err != nil {
                log.Printf("Write %d failed: %v", id, err)
            }
        }(i)
    }

    wg.Wait()
    log.Println("All writes completed")
}
```

---

**结论**：Page 级别锁是实现高并发写入的关键。通过将锁粒度从整个 BTree 降低到单个 Page，可以显著提升并发写入性能，目标是从 86 万 QPS 提升到 200 万+ QPS（4 线程场景），提升 **2.3x**。
