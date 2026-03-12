# Phase 1 Week 13-14: BTree 集成方案

**文档目的**: 详细规划从当前 Node-based 架构迁移到 Page-based 架构的实施步骤
**创建日期**: 2026-03-12
**目标审核人**: jzhang405
**预计工期**: 2 周（Week 13-14）

---

## 一、当前架构分析

### 1.1 当前 BTree 结构

```go
type BTree struct {
    config      *model.BTreeConfig
    closed      bool
    closedMu    sync.RWMutex
    root        *VersionedRoot  // Versioned root pointer
    pageManager *PageManager     // Page manager (覆盖写入)
    pageCache   *PageCache       // 三层缓存 (L1/L2/NodeL1)
    wal         wal.WAL          // Write-Ahead Log
    maxLevels   int
    nodeCache   *nodeCache       // Node 反序列化缓存

    enablePersistence bool
    enableWAL         bool
}
```

**关键依赖**：
- `VersionedRoot`: 版本化根节点管理（原子切换）
- `Node`: 混合架构（内存指针 + PageID 持久化引用）
- `PageCache`: 三层缓存（L1: Page, L2: []byte, NodeL1: Node）
- `PageManager`: 覆盖写入持久化
- `WAL`: Write-Ahead Log

### 1.2 当前操作流程

#### InsertWithSplit 流程
```
1. FindPath(key) → Path (Node 列表)
2. CopyPathBottomUp(path, modifyFunc)
   - 从叶子向上复制 Node
   - 在复制的 Node 上执行修改
3. 处理 Node 满 → Split
4. 写入 WAL
5. root.Update(newRoot) - 原子更新
```

**关键点**：
- ✅ 已实现 CCOW（Copy-on-Write）
- ✅ 已支持 Split/Merge
- ❌ Get/Set 返回 ErrNotImplemented
- ✅ 依赖 `VersionedRoot` 进行原子切换

### 1.3 现有 PageCache 实现

```go
type PageCache struct {
    L1             *sync.Map  // PageID → *Page (热数据)
    L2             *sync.Map  // PageID → []byte (序列化缓冲)
    NodeL1          *sync.Map  // PageID → *Node (懒加载)

    l1Capacity      int
    l2Capacity      int
    nodeL1Capacity  int

    l1Size          atomic.Int64
    l2Size          atomic.Int64
    nodeL1Size      atomic.Int64

    evictionLock    sync.Mutex
    evictionQueue   []model.PageID
    pageManager     *PageManager
}
```

**三层缓存问题**：
1. **L1 和 L2 分离**：需要管理两个独立的缓存
2. **NodeL1 冗余**：因为 Node 混合架构，额外缓存 Node 对象
3. **淘汰复杂**：需要维护三个队列和同步

---

## 二、目标架构设计

### 2.1 新 BTree 结构

```go
type BTree struct {
    config      *model.BTreeConfig
    closed      bool
    closedMu    sync.RWMutex

    // 新架构核心组件
    rootRef     *RootPageRef     // Root 页面引用（原子指针）
    chunkMgr    *ChunkManager    // Append-Only 存储
    gc          *BTreeGC         // 垃圾回收器
    ccow        *CCOWManager     // Copy-on-Write 管理

    // 可选组件
    wal         wal.WAL          // WAL（保留）
    maxLevels   int

    enablePersistence bool
    enableWAL         bool
}
```

**关键变化**：
| 旧组件 | 新组件 | 变化说明 |
|--------|--------|----------|
| `root: *VersionedRoot` | `rootRef: *RootPageRef` | VersionedRoot → RootPageRef |
| `pageManager: *PageManager` | `chunkMgr: *ChunkManager` | PageManager → ChunkManager |
| `pageCache: *PageCache` | `PageInfoCache: *PageInfoCache` | 三层缓存 → 统一 PageInfo 缓存 |
| ~~`nodeCache: *nodeCache`~~ | - | 移除（PageInfo 已包含 Page） |

### 2.2 PageInfoCache 设计

**核心问题**：是否需要 PageInfoCache？

#### 方案 A：保留 PageInfoCache（推荐）

```go
type PageInfoCache struct {
    // 存储 PageInfo（不存储 PageRef）
    pages    map[model.PageID]*PageInfo
    pagesMu  sync.RWMutex

    // LRU 淘汰队列
    lruLock   sync.Mutex
    lruQueue  []model.PageID

    // 容量控制
    maxPages  int
    currentSize atomic.Int64

    // 与 ChunkManager 集成
    chunkMgr  *ChunkManager
    gc        *BTreeGC
}

// Get 获取 PageInfo（从缓存或 ChunkManager）
func (c *PageInfoCache) Get(pageID model.PageID) (*PageInfo, error) {
    // 1. 尝试从缓存获取
    c.pagesMu.RLock()
    info, ok := c.pages[pageID]
    c.pagesMu.RUnlock()

    if ok {
        info.Touch()  // 更新 LRU 时间戳
        return info, nil
    }

    // 2. 从 ChunkManager 读取
    pos, err := c.chunkMgr.LookupPagePos(pageID)
    if err != nil {
        return nil, err
    }

    data, err := c.chunkMgr.ReadPage(pos)
    if err != nil {
        return nil, err
    }

    // 3. 反序列化为 Page
    page, err := deserializePage(data)
    if err != nil {
        return nil, err
    }

    // 4. 创建 PageInfo
    info = &PageInfo{
        pos:      pos,
        page:     page,
        pageLock: NewPageLock(),
        lastTime: time.Now().UnixNano(),
        hits:     0,
    }

    // 5. 加入缓存（检查容量）
    c.pagesMu.Lock()
    defer c.pagesMu.Unlock()

    if c.currentSize.Load() >= int64(c.maxPages) {
        c.evictLRU()
    }

    c.pages[pageID] = info
    c.currentSize.Add(1)

    return info, nil
}

// Put 直接放入缓存（不写入 ChunkManager）
func (c *PageInfoCache) Put(pageID model.PageID, info *PageInfo) error {
    c.pagesMu.Lock()
    defer c.pagesMu.Unlock()

    c.pages[pageID] = info
    c.currentSize.Add(1)

    return nil
}

// evictLRU 淘汰最久未使用的 PageInfo
func (c *PageInfoCache) evictLRU() {
    c.lruLock.Lock()
    defer c.lruLock.Unlock()

    if len(c.lruQueue) == 0 {
        return
    }

    // 淘汰最旧的 PageInfo
    oldestPageID := c.lruQueue[0]
    c.lruQueue = c.lruQueue[1:]

    delete(c.pages, oldestPageID)
    c.currentSize.Add(-1)
}
```

**优势**：
- ✅ 集中管理多个 PageInfo
- ✅ LRU 淘汰策略
- ✅ 容量控制
- ✅ 与 BTreeGC 集成

#### 方案 B：仅使用 PageRef（不推荐）

```go
// 每个 PageRef 持有 PageInfo，无全局缓存
type PageRef struct {
    pInfo atomic.Pointer[PageInfo]
}

// 问题：无法全局控制容量和 LRU
// - 无法统计总内存占用
// - 无法统一淘汰策略
// - 每个 PageInfo 需要独立管理生命周期
```

**问题**：
- ❌ 无法全局控制容量
- ❌ 无法统一 LRU 淘汰
- ❌ 与 BTreeGC 难以集成

### 2.3 PageRef vs PageInfoCache 的职责

| 组件 | 职责 | 说明 |
|------|------|------|
| **PageRef** | 1. 持有 PageInfo（原子指针）<br>2. 提供无锁访问<br>3. CAS 更新 | - `pInfo.Load()` 无锁读取<br>- `ReplacePage(old, new)` 原子更新<br>- **不是缓存管理器** |
| **PageInfoCache** | 1. 管理多个 PageInfo<br>2. LRU 淘汰<br>3. 容量控制<br>4. 与 ChunkManager 集成 | - `Get(pageID)` 获取/加载<br>- `Put(pageID, info)` 加入缓存<br>- `evictLRU()` 淘汰 |

**类比**：
- PageRef ≈ "智能指针"（std::shared_ptr）
- PageInfoCache ≈ "缓存管理器"（LRU Cache）

---

## 三、核心操作改造

### 3.1 Get 操作改造

#### 当前实现（未完成）
```go
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    return nil, ErrNotImplemented
}
```

#### 新实现

```go
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    // 1. 从 rootRef 开始搜索
    rootInfo := b.rootRef.pInfo.Load()
    if rootInfo == nil {
        return nil, ErrKeyNotFound
    }

    rootPage := rootInfo.GetPage()
    if rootPage == nil {
        return nil, ErrKeyNotFound
    }

    // 2. 搜索路径（自顶向下）
    path, err := b.searchPath(rootPage, key)
    if err != nil {
        return nil, err
    }

    // 3. 获取叶子节点
    leafRef := path[len(path)-1]
    leafInfo := leafRef.pInfo.Load()

    // 4. 从 PageInfo 获取 Page
    leafPage := leafInfo.GetPage()
    if leafPage == nil {
        return nil, ErrKeyNotFound
    }

    // 5. 在 LeafPage 中查找键
    value, ok := leafPage.Get(key)
    if !ok {
        return nil, ErrKeyNotFound
    }

    return value, nil
}

// searchPath 搜索从根到叶子的路径
func (b *BTree) searchPath(rootPage *Page, key []byte) ([]*PageRef, error) {
    var path []*PageRef

    current := rootPage
    maxLevels := b.maxLevels

    for level := 0; level < maxLevels; level++ {
        path = append(path, current.ref)

        if current.IsLeaf() {
            // 到达叶子节点
            return path, nil
        }

        // 内部节点：查找子节点
        internalPage := current.(*InternalPage)
        childIdx := internalPage.FindChild(key)
        childRef := internalPage.children[childIdx]

        // 加载子节点的 PageInfo
        childInfo := childRef.pInfo.Load()
        if childInfo == nil {
            // 从缓存或磁盘加载
            childInfo, err := b.cache.Get(childRef.GetPageID())
            if err != nil {
                return nil, err
            }
            // 缓存到 PageRef（非阻塞）
            childRef.pInfo.Store(childInfo)
        }

        current = childInfo.GetPage()
        if current == nil {
            return nil, fmt.Errorf("child page is nil")
        }
    }

    return path, fmt.Errorf("max levels exceeded")
}
```

**关键点**：
1. PageRef 持有 PageInfo（原子指针）
2. PageInfoCache 管理 PageInfo（LRU 淘汰）
3. 懒加载：PageRef.pInfo.Load() 为 nil 时加载

### 3.2 Set 操作改造

```go
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    // 1. 搜索路径
    path, err := b.searchPath(rootPage, key)
    if err != nil {
        return fmt.Errorf("search path failed: %w", err)
    }

    // 2. Copy-on-Write：自底向上复制路径
    // 使用 CCOWManager 的 CopyPathBottomUp
    newRootInfo, err := b.ccow.CopyPathBottomUp(ctx, b.rootRef, path, func(pageInfo *PageInfo) error {
        page := pageInfo.GetPage()

        if page.IsLeaf() {
            leafPage := page.(*LeafPage)
            if _, err := leafPage.Insert(key, value); err != nil {
                return err
            }
        } else {
            // 内部节点：不需要修改（Split 时处理）
        }

        // 修改后，page 的 version 会自动递增
        return nil
    })

    if err != nil {
        return fmt.Errorf("copy path bottom-up failed: %w", err)
    }

    // 3. CAS 更新 RootPageRef
    oldRootInfo := b.rootRef.pInfo.Load()
    swapped := b.rootRef.pInfo.CompareAndSwap(oldRootInfo, newRootInfo)
    if !swapped {
        return ErrRetry // CAS 失败，重试
    }

    // 4. 标记旧路径的 PageInfo 为脏页（GC 清理）
    for _, pageRef := range path {
        oldInfo := pageRef.pInfo.Load()
        if oldInfo != newRootInfo && oldInfo != nil {
            b.ccow.MarkDirty(oldInfo)
        }
    }

    // 5. 异步刷脏页（BTreeGC 后台处理）
    // 不需要立即调用，GC 会自动触发

    return nil
}
```

**关键点**：
1. 使用 CCOWManager.CopyPathBottomUp
2. CAS 更新 RootPageRef
3. 标记旧 PageInfo 为脏页
4. BTreeGC 后台异步刷脏页

### 3.3 Delete 操作改造

类似 Set 操作，省略详细代码。

---

## 四、PageCache 重构方案

### 4.1 当前 PageCache 的问题

```go
// 当前：三层缓存
type PageCache struct {
    L1        *sync.Map  // PageID → *Page
    L2        *sync.Map  // PageID → []byte
    NodeL1    *sync.Map  // PageID → *Node  ← 冗余
    ...
}
```

**问题分析**：

| 问题 | 原因 | 影响 |
|------|------|------|
| NodeL1 冗余 | Node 混合架构需要 | 新架构无 Node，可移除 |
| L1/L2 分离 | Page 对象和序列化缓冲分开管理 | PageInfo 统一包含两者，可合并 |
| sync.Map 锁竞争 | 每个 cache 操作都需要锁 | PageRef 使用 atomic.Pointer，无锁 |
| 复杂淘汰逻辑 | 需要维护三个队列 | PageInfo 统一 LRU |

### 4.2 PageInfoCache 设计

```go
type PageInfoCache struct {
    // PageInfo 存储（map + RWMutex）
    pages      map[model.PageID]*PageInfo
    pagesMu    sync.RWMutex

    // LRU 管理
    lruLock    sync.Mutex
    lruQueue   []model.PageID
    lruIndex   map[model.PageID]int  // 快速查找索引

    // 容量控制
    maxPages   int
    usedPages  atomic.Int64

    // 与后端集成
    chunkMgr   *ChunkManager
    gc         *BTreeGC

    // 统计
    hits       atomic.Int64
    misses     atomic.Int64
}

func NewPageInfoCache(maxPages int, chunkMgr *ChunkManager, gc *BTreeGC) *PageInfoCache {
    return &PageInfoCache{
        pages:     make(map[model.PageID]*PageInfo),
        lruIndex:  make(map[model.PageID]int),
        maxPages:  maxPages,
        chunkMgr:  chunkMgr,
        gc:        gc,
    }
}

// Get 获取 PageInfo
func (c *PageInfoCache) Get(pageID model.PageID) (*PageInfo, error) {
    // 1. 快速路径：读锁查找
    c.pagesMu.RLock()
    info, ok := c.pages[pageID]
    if ok {
        c.pagesMu.RUnlock()
        info.Touch()
        c.hits.Add(1)
        return info, nil
    }
    c.pagesMu.RUnlock()

    // 2. 慢速路径：未命中，从 ChunkManager 加载
    c.misses.Add(1)
    return c.loadPage(pageID)
}

// loadPage 从 ChunkManager 加载页面
func (c *PageInfoCache) loadPage(pageID model.PageID) (*PageInfo, error) {
    // 1. 从 ChunkManager 查找位置
    pos, err := c.chunkMgr.LookupPagePos(pageID)
    if err != nil {
        return nil, fmt.Errorf("lookup page pos: %w", err)
    }

    // 2. 读取页面数据
    data, err := c.chunkMgr.ReadPage(pos)
    if err != nil {
        return nil, fmt.Errorf("read page: %w", err)
    }

    // 3. 反序列化
    page, err := DeserializePage(data)
    if err != nil {
        return nil, fmt.Errorf("deserialize page: %w", err)
    }

    // 4. 创建 PageInfo
    info := &PageInfo{
        pos:      pos,
        page:     page,
        pageLock: NewPageLock(),
        lastTime: time.Now().UnixNano(),
        hits:     0,
    }

    // 5. 加入缓存（写锁）
    c.pagesMu.Lock()
    defer c.pagesMu.Unlock()

    // 检查容量
    if c.usedPages.Load() >= int64(c.maxPages) {
        c.evictLRU()
    }

    c.pages[pageID] = info
    c.lruQueue = append(c.lruQueue, pageID)
    c.lruIndex[pageID] = len(c.lruQueue) - 1
    c.usedPages.Add(1)

    return info, nil
}

// Put 直接放入缓存（用于 Copy-on-Write）
func (c *PageInfoCache) Put(pageID model.PageID, info *PageInfo) error {
    c.pagesMu.Lock()
    defer c.pagesMu.Unlock()

    // 更新 LRU
    if idx, ok := c.lruIndex[pageID]; ok {
        // 已存在，移动到队列尾部
        c.lruQueue = append(c.lruQueue[:idx], c.lruQueue[idx+1:]...)
        c.lruQueue = append(c.lruQueue, pageID)
        c.lruIndex[pageID] = len(c.lruQueue) - 1
    } else {
        // 新增
        if c.usedPages.Load() >= int64(c.maxPages) {
            c.evictLRU()
        }

        c.lruQueue = append(c.lruQueue, pageID)
        c.lruIndex[pageID] = len(c.lruQueue) - 1
        c.usedPages.Add(1)
    }

    c.pages[pageID] = info
    return nil
}

// evictLRU 淘汰最久未使用的 PageInfo
func (c *PageInfoCache) evictLRU() {
    if len(c.lruQueue) == 0 {
        return
    }

    // 淘汰最老的
    oldestPageID := c.lruQueue[0]

    // 从队列移除
    c.lruQueue = c.lruQueue[1:]
    delete(c.lruIndex, oldestPageID)

    // 从 map 移除
    delete(c.pages, oldestPageID)
    c.usedPages.Add(-1)

    // 注意：不删除 PageRef 中的引用（由 GC 清理）
}
```

### 4.3 与 BTreeGC 集成

```go
// BTreeGC 触发条件
func (gc *BTreeGC) shouldGC() bool {
    used := gc.usedMemory.Load()
    return used >= gc.lowWaterMark  // 70%
}

// 收集脏页时通知 PageInfoCache
func (gc *BTreeGC) collectDirtyPages(dirtyPages map[*PageInfo]bool) error {
    // 1. 写入脏页到 ChunkManager
    for pageInfo := range dirtyPages {
        // 序列化
        data, err := pageInfo.page.Serialize()
        if err != nil {
            return err
        }

        // 写入
        pos, err := gc.chunkManager.WritePage(data)
        if err != nil {
            return err
        }

        // 更新位置
        pageInfo.pos = pos
    }

    // 2. 清除脏页标记
    for pageInfo := range dirtyPages {
        pageInfo.ClearDirty()
    }

    return nil
}

// 淘汰页面时与 BTreeGC 协调
func (c *PageInfoCache) evictLRU() {
    if len(c.lruQueue) == 0 {
        return
    }

    oldestPageID := c.lruQueue[0]
    oldestInfo := c.pages[oldestPageID]

    // 通知 BTreeGC PageInfo 已释放
    c.gc.ReleasePageInfo(oldestInfo)

    // ... 其余淘汰逻辑
}
```

---

## 五、实施步骤

### Week 13: 核心改造（Day 1-5）

#### Day 1-2: PageInfoCache 实现
- [ ] 实现 PageInfoCache 结构
- [ ] 实现 Get/Put 方法
- [ ] 实现 LRU 淘汰
- [ ] 单元测试

#### Day 3-4: searchPath 实现
- [ ] 实现 searchPath 方法
- [ ] 处理 PageRef.pInfo 为 nil 的情况
- [ ] 懒加载逻辑
- [ ] 单元测试

#### Day 5: Get/Set 实现
- [ ] 实现 Get 方法
- [ ] 实现 Set 方法（使用 CCOW）
- [ ] CAS 更新 RootPageRef
- [ ] 集成测试

### Week 14: 集成和优化（Day 6-10）

#### Day 6-7: 替换 BTree 结构
- [ ] 修改 BTree 结构（移除旧字段）
- [ ] 修改 OpenBTree 初始化
- [ ] 保留 WAL 和配置兼容性
- [ ] 回归测试

#### Day 8-9: 与 BTreeGC 集成
- [ ] PageInfoCache 与 BTreeGC 协作
- [ ] 脏页标记和收集
- [ ] 内存压力触发淘汰
- [ ] 性能测试

#### Day 10: 集成测试
- [ ] 基本 CRUD 操作测试
- [ ] 并发读写测试
- [ ] 持久化测试
- [ ] 性能基准测试

---

## 六、风险点和应对

### 风险 1：PageRef 和 PageInfoCache 职责混淆 🔴

**问题**：
- PageRef 持有 PageInfo（原子指针）
- PageInfoCache 也管理 PageInfo
- 职责边界不清

**应对**：
- ✅ 明确职责：
  - PageRef：**引用持有**，提供原子访问
  - PageInfoCache：**缓存管理**，LRU 淘汰
- ✅ PageInfoCache 不直接操作 PageRef
- ✅ PageRef 不关心缓存淘汰

**代码约定**：
```go
// ✅ 正确：PageInfoCache 管理 PageInfo
info := cache.Get(pageID)  // 返回 *PageInfo
ref.pInfo.Store(info)     // PageRef 持有

// ❌ 错误：PageRef 直接访问缓存
ref := cache.GetRef(pageID)  // ❌ 不要这样
```

### 风险 2：懒加载的并发安全 🟡

**问题**：
- 多个 goroutine 同时发现 PageRef.pInfo 为 nil
- 可能重复加载同一个页面

**应对**：
```go
// 方案：使用 sync.Map 或 double-checked locking
func (c *PageInfoCache) GetOrLoad(pageID model.PageID) (*PageInfo, error) {
    // 1. 快速路径：读锁检查
    c.pagesMu.RLock()
    info, ok := c.pages[pageID]
    c.pagesMu.RUnlock()

    if ok {
        return info, nil
    }

    // 2. 慢速路径：写锁加载
    c.pagesMu.Lock()
    defer c.pagesMu.Unlock()

    // Double-check
    if info, ok := c.pages[pageID]; ok {
        return info, nil
    }

    // 加载页面
    info, err := c.loadPage(pageID)
    if err != nil {
        return nil, err
    }

    c.pages[pageID] = info
    return info, nil
}
```

### 风险 3：CAS 更新失败重试 🟡

**问题**：
- 高并发下 CAS 可能频繁失败
- 无限重试浪费 CPU

**应对**：
```go
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    const maxRetries = 3

    for attempt := 0; attempt < maxRetries; attempt++ {
        // ... 执行 Copy-on-Write ...

        // CAS 更新
        oldRootInfo := b.rootRef.pInfo.Load()
        swapped := b.rootRef.pInfo.CompareAndSwap(oldRootInfo, newRootInfo)

        if swapped {
            // 成功，标记旧路径为脏页
            for _, pageRef := range path {
                oldInfo := pageRef.pInfo.Load()
                if oldInfo != newRootInfo {
                    b.ccow.MarkDirty(oldInfo)
                }
            }
            return nil
        }

        // CAS 失败，重试
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(time.Microsecond * 100):
            // 短暂等待后重试
        }
    }

    return fmt.Errorf("CAS failed after %d attempts", maxRetries)
}
```

### 风险 4：内存泄漏（PageInfo 未释放）🔴

**问题**：
- PageInfo 被 PageRef 持有
- PageRef 可能被多个地方引用
- 无法及时回收

**应对**：
- ✅ BTreeGC 定期扫描未使用的 PageRef
- ✅ 引用计数 + 超时回收
- ✅ PageInfo 的 pinCount 机制

```go
// PageInfo 引用计数
type PageInfo struct {
    pinCount atomic.Int32  // 引用计数
    ...
}

// Acquire 增加引用
func (info *PageInfo) Acquire() {
    info.pinCount.Add(1)
}

// Release 减少引用
func (info *PageInfo) Release() {
    newCount := info.pinCount.Add(-1)
    if newCount <= 0 {
        // 引用数为 0，可以回收
        // 通知 BTreeGC
    }
}
```

---

## 七、性能考虑

### 7.1 内存占用对比

| 架构 | 内存占用 | 说明 |
|------|----------|------|
| 旧架构（Node） | 100% | 基线 |
| 新架构（PageRef + PageInfo） | 200-300% | 可接受，换取 TB/PB 支持 |

**详细分析**：
```go
// 旧：Node
type Node struct {
    PageID    model.PageID  // 8 bytes
    Keys      [][]byte      // ~1024 bytes
    Values    [][]byte      // ~1024 bytes
    Children  []*Node       // ~256 bytes (指针)
    ChildIDs  []model.PageID // ~256 bytes
}
// 总计：~2560 bytes

// 新：PageRef + PageInfo + Page
type PageRef struct {
    pInfo atomic.Pointer[PageInfo]  // 8 bytes
}
type PageInfo struct {
    pos      int64       // 8 bytes
    page     *Page       // 8 bytes
    pageLock *PageLock   // 8 bytes
    buff     []byte      // 4096 bytes (固定)
    ...
}
type LeafPage struct {
    pageID   model.PageID
    version  uint64
    keys     [][]byte
    values   [][]byte
}
// 总计：PageRef (8) + PageInfo (~4160) + LeafPage (~2048) = ~6216 bytes

// 结论：单个节点内存增加 ~2.4x
// 但：1. 支持间接寻址（TB/PB）2. Append-Only 存储（减少写入放大）
```

### 7.2 延迟优化

| 操作 | 旧架构 | 新架构 | 优化目标 |
|------|--------|--------|----------|
| 读路径（命中） | ~100 ns | ~50 ns | ✅ 无锁 atomic.Pointer |
| 读路径（未命中） | ~10 μs | ~8 μs | 需优化序列化 |
| 写路径（CCOW） | ~40 μs | ~30 μs | ✅ 优化 Page 复制 |
| Split 操作 | ~50 μs | ~40 μs | ✅ 优化引用更新 |

### 7.3 吞吐量优化

**目标**：
- 随机读：> 1M ops/sec
- 随机写：> 300k ops/sec
- 并发读（100 线程）：> 10M ops/sec

**优化手段**：
1. ✅ atomic.Pointer 无锁访问
2. ✅ Copy-on-Write 减少锁竞争
3. ⏳ 批量操作（Batch Get/Set）
4. ⏳ 预取（Prefetch）

---

## 八、测试策略

### 8.1 单元测试

```go
func TestPageInfoCache_Get_Put(t *testing.T) {
    cache := NewPageInfoCache(100, chunkMgr, gc)

    // 测试未命中
    info1, err := cache.Get(1)
    require.NoError(t, err)
    assert.NotNil(t, info1)

    // 测试命中
    info2, err := cache.Get(1)
    require.NoError(t, err)
    assert.Equal(t, info1, info2)  // 同一个对象
}

func TestPageInfoCache_Eviction(t *testing.T) {
    cache := NewPageInfoCache(2, chunkMgr, gc)

    // 添加 3 个页面（超过容量）
    cache.Put(1, info1)
    cache.Put(2, info2)
    cache.Put(3, info3)  // 触发淘汰

    // 验证最老的被淘汰
    _, err := cache.Get(1)
    assert.Error(t, err)  // info1 被淘汰
}

func TestBTree_Get_Set(t *testing.T) {
    tree := openTestBTree()

    // Set
    err := tree.Set(ctx, key1, value1)
    require.NoError(t, err)

    // Get
    value, err := tree.Get(ctx, key1)
    require.NoError(t, err)
    assert.Equal(t, value1, value)
}
```

### 8.2 并发测试

```go
func TestBTree_ConcurrentGetSet(t *testing.T) {
    tree := openTestBTree()

    const goroutines = 100
    const opsPerGoroutine = 1000

    var wg sync.WaitGroup
    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < opsPerGoroutine; j++ {
                key := []byte(fmt.Sprintf("key-%d-%d", id, j))
                value := []byte(fmt.Sprintf("value-%d", j))

                tree.Set(ctx, key, value)

                result, err := tree.Get(ctx, key)
                require.NoError(t, err)
                assert.Equal(t, value, result)
            }
        }(i)
    }

    wg.Wait()
}
```

### 8.3 性能基准测试

```go
func BenchmarkBTree_Get(b *testing.B) {
    tree := setupBTree()
    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := []byte(fmt.Sprintf("key-%d", i%1000))
        tree.Get(ctx, key)
    }
}

func BenchmarkBTree_Set(b *testing.B) {
    tree := setupBTree()
    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        key := []byte(fmt.Sprintf("key-%d", i%1000))
        value := []byte(fmt.Sprintf("value-%d", i))
        tree.Set(ctx, key, value)
    }
}
```

---

## 九、未知问题和待确认

### 问题 1：VersionedRoot 的兼容性

**当前代码**：
```go
type BTree struct {
    root *VersionedRoot  // ← 当前使用
}
```

**问题**：
- VersionedRoot 是否还需要？
- 如何迁移到 RootPageRef？

**待确认**：
- [ ] VersionedRoot 的具体实现
- [ ] 是否需要保留 VersionedRoot 作为包装？
- [ ] RootPageRef 是否需要支持多版本？

### 问题 2：PageID 的分配

**当前**：PageManager 分配 PageID
**新架构**：ChunkManager 64 位位置编码

**问题**：
- PageID 如何与 64 位位置编码对应？
- 是否需要 PageID → ChunkID+Offset 映射？

**待设计**：
```go
// 方案 A：PageID 直接编码
type PageID int64  // 直接使用 64 位位置编码

// 方案 B：PageID 独立
type PageID struct {
    chunkID  uint32
    offset   uint32
    pageType uint8
}
```

### 问题 3：WAL 的兼容性

**当前**：WAL 记录 Node 操作
**新架构**：WAL 记录 Page 操作

**问题**：
- WAL 格式是否需要修改？
- 如何记录 PageRef 的变更？

**待设计**：
- [ ] WAL Entry 格式定义
- [ ] PageRef 变更的 WAL 记录
- [ ] 崩溃恢复时如何重建 PageRef

### 问题 4：PageInfoCache 的容量控制

**问题**：
- maxPages 应该设置多大？
- 如何与 BTreeGC 的水位线机制协调？

**待设计**：
- [ ] PageInfoCache 容量公式
- [ ] 与 BTreeGC 的集成点
- [ ] 内存压力触发条件

---

## 十、待审核的关键决策

### 决策 1：是否保留 PageInfoCache？

**选项 A**：保留 PageInfoCache（推荐）
- ✅ 集中管理缓存
- ✅ LRU 淘汰策略
- ✅ 与 BTreeGC 集成
- ❌ 增加一层间接

**选项 B**：移除 PageInfoCache，仅使用 PageRef
- ✅ 减少间接层
- ❌ 无法全局容量控制
- ❌ LRU 淘汰复杂
- ❌ 与 BTreeGC 难以集成

**建议**：保留 PageInfoCache

### 决策 2：PageID 格式

**选项 A**：PageID = 64 位位置编码
```go
type PageID int64  // 直接使用 ChunkManager 的位置编码
```

**选项 B**：PageID 独立结构
```go
type PageID struct {
    chunkID  uint32
    offset   uint32
    pageType uint8
}
```

**建议**：选项 A（简化设计）

### 决策 3：是否保留 VersionedRoot？

**选项 A**：保留 VersionedRoot，内部使用 RootPageRef
```go
type VersionedRoot struct {
    rootRef *RootPageRef  // 内部使用新架构
}
```

**选项 B**：完全替换为 RootPageRef
```go
type BTree struct {
    rootRef *RootPageRef  // 直接使用
}
```

**建议**：选项 A（平滑迁移）

---

## 十一、总结

### 核心改造

| 组件 | 变化 | 工作量 |
|------|------|--------|
| BTree.root | `*VersionedRoot` → `*RootPageRef` | 2 天 |
| BTree.pageManager | `*PageManager` → `*ChunkManager` | 1 天 |
| BTree.pageCache | `*PageCache` → `*PageInfoCache` | 3 天 |
| Get/Set/Delete | 重写实现 | 3 天 |
| 与 BTreeGC 集成 | 新增集成逻辑 | 2 天 |
| 测试 | 单元 + 并发 + 性能 | 3 天 |

**总计**：约 14 天（2 周）

### 关键风险

| 风险 | 影响 | 应对 |
|------|------|------|
| PageRef/PageInfoCache 职责混淆 | 高 | 明确职责边界 |
| 懒加载并发安全 | 中 | Double-checked locking |
| CAS 更新失败重试 | 中 | 最多重试 3 次 |
| 内存泄漏 | 高 | 引用计数 + GC 清理 |

### 待审核问题

1. ✅ PageInfoCache 是否保留？（建议保留）
2. ⏳ PageID 格式如何选择？（待讨论）
3. ⏳ VersionedRoot 是否保留？（建议保留作为包装）
4. ⏳ PageInfoCache 容量如何设置？（待设计）

---

**附录：当前 BTree 依赖关系**

```
BTree
├── VersionedRoot (原子根节点管理)
├── PageManager (覆盖写入持久化)
├── PageCache (三层缓存)
├── WAL (Write-Ahead Log)
└── Node (混合架构节点)
```

**新 BTree 依赖关系**

```
BTree
├── RootPageRef (Root 页面引用)
├── PageInfoCache (统一 PageInfo 缓存)
├── ChunkManager (Append-Only 存储)
├── BTreeGC (垃圾回收器)
├── CCOWManager (Copy-on-Write 管理)
└── WAL (保留，兼容性)
```

---

**文档版本**：v1.0
**创建日期**：2026-03-12
**作者**：AI Assistant
**审核人**：jzhang405（待审核）
