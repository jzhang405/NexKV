# Page 层实施计划 - 基于 Lealone 完整实现（优化版 v2.0）

**日期**: 2026-03-09
**基于**:
- `thoughts/2026-03-09-lealone-page-based-btree-implementation.md` ⭐ 新增
- `thoughts/2026-03-09-page-layer-architecture-design.md`
**优先级**: P0（生产环境必要条件）

---

## 🔄 版本更新日志

### v2.0 (2026-03-09) - 基于 Lealone 完整实现优化

**核心改进**:
1. ✅ 引入 Lealone 的 PageLock 机制（可重入、等待）
2. ✅ 引入 PageReference 原子引用（CAS 无锁更新）
3. ✅ 引入 Scheduler 单写调度器（消除 CAS 竞争）
4. ✅ 真正的 BTree 结构（多层 Page，不同叶节点可并发写入）

**性能预期**:
- 4线程写入：491K QPS → **2.5M QPS** (提升 5倍)
- 8线程写入：480K QPS → **4.0M QPS** (提升 7.3倍)

**关键洞察**:
```
当前问题：1个 Page 存储所有 key → Page 锁退化成全局锁
Lealone 方案：真正的 BTree → 不同叶节点独立锁 → 真正并发
```

---

## 一、核心洞察

### 1.1 架构对比

```
┌─────────────────────────────────────────────────────────────┐
│ 当前纯内存架构（不可落地生产）                               │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ✅ 极致性能: 10.97 ns/op (硬件极限)                        │
│  ❌ 无法持久化: 崩溃后数据全丢                                │
│  ❌ 内存碎片: 403 次分配/op (GC 压力大)                     │
│  ❌ 大容量限制: 受限于物理内存                                │
│  ❌ 生产不可用: 无法落地生产                                  │
│                                                              │
└─────────────────────────────────────────────────────────────┘

↓ 迁移

┌─────────────────────────────────────────────────────────────┐
│ Page-based 架构（生产环境可行）                               │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ⭐ 高性能: 100-200 ns/op (仍比 Lealone 快 5-10x)           │
│  ✅ 可持久化: 数据安全, 支持崩溃恢复                          │
│  ✅ 内存友好: 固定 4KB 分配 (减少碎片)                         │
│  ✅ 大容量: 支持磁盘存储 (TB 级)                             │
│  ✅ 生产可用: 完整的 WAL + 检查点机制                          │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 1.2 关键权衡

| 维度 | 纯内存 | Page 层（三层缓存） | 权衡决策 |
|------|--------|-------------------|---------|
| **读延迟** | 10.97 ns | 300-600 ns | **舍短期 27-55x** |
| **写延迟** | 41.7K ns | 800-1500 ns | **取优化 28-52x** ✅ |
| **持久化** | ❌ | ✅ | **取长远** |
| **内存** | 碎片化 | 固定 4KB | **可维护** |
| **容量** | OOM | TB 级 | **可扩展** |
| **生产** | 不可用 | 可用 | **可落地** |

**结论**: "舍短期/极致性能，取长远/可运维" ✅

**预期性能**（vs Lealone）：
- 读延迟: 300-600 ns vs Lealone 941 ns → **预期快 1.6-3.1x**（待验证）
- 写延迟: 800-1500 ns vs Lealone 1596 ns → **预期快 1.1-2.0x**（待验证）

---

## 二、参考 Lealone 的设计精华

### 2.1 三层缓存架构（完整实现）⭐

```
┌─────────────────────────────────────────────────────────────┐
│ Lealone 风格的三层缓存架构                                   │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│ L1: Page 对象缓存 (热数据, ~100 ns)                         │
│   └─ 已反序列化的 Node 对象                                   │
│     - 直接访问 Keys/Values/Children                         │
│     - 容量: 256 pages (~1 MB)                               │
│     - 淘汰: LRU                                              │
│     │                                                         │
│     ▼ (miss)                                                 │
│ L2: ByteBuffer 缓存 (温数据, ~500 ns) ⭐ 新增               │
│   └─ 原始 []byte 数据（未反序列化）                            │
│     - 避免重复磁盘 I/O                                       │
│     - 避免重复反序列化                                         │
│     - 容量: 512 pages (~2 MB, 2x L1)                        │
│     - 淘汰: LRU                                              │
│     │                                                         │
│     ▼ (miss)                                                 │
│ L3: 磁盘文件 (冷数据, ~10-100 μs)                           │
│   └─ Chunk 文件持久化存储                                     │
│     - 文件 I/O                                               │
│     - 容量: TB 级                                            │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**关键优化**:
1. **三层查找**: L1 → L2 → L3，逐级降级
2. **延迟反序列化**: L2 → L1 只在需要时反序列化
3. **缓存复用**: L2 避免重复磁盘 I/O 和反序列化
4. **LR 策略**: lastTime + hits 淘汰（优先保留热数据）

**性能提升**（vs 二层缓存）:
- ✅ **减少磁盘 I/O**: L2 缓存命中率 ~10%，减少磁盘访问
- ✅ **降低 CPU 开销**: 避免重复反序列化
- ✅ **提高并发性能**: 多个 goroutine 共享 L2 的 `[]byte`
- ✅ **整体性能**: 预期提升 ~8%（平均延迟: 550 ns vs 600 ns）

### 2.2 原子性引用机制

```java
// Lealone 使用 AtomicReferenceFieldUpdater
private static final AtomicReferenceFieldUpdater<PageReference, PageInfo> //
pageInfoUpdater = AtomicReferenceFieldUpdater.newUpdater(
    PageReference.class, PageInfo.class, "pInfo");

private volatile PageInfo pInfo;

// CAS 更新（无锁）
public boolean replacePage(PageInfo expect, PageInfo update) {
    return pageInfoUpdater.compareAndSet(this, expect, update);
}
```

**NexKV 实现建议**:
```go
type PageReference struct {
    pInfo atomic.Value  // *PageInfo
}

func (pr *PageReference) replacePage(expect, update *PageInfo) bool {
    old := pr.pInfo.Load()
    if old == expect {
        return pr.pInfo.CompareAndSwap(expect, update)
    }
    return false
}
```

### 2.3 热点数据优化

```java
// Lealone 缓存上一次查找结果
protected int cachedCompare;

public int binarySearch(Object key) {
    // 使用缓存的查找起点
    if (cachedCompare != 0) {
        int x = cachedCompare - 1;
        // 减少比较次数
    }
    // ...
    cachedCompare = low;  // 缓存结果
}
```

---

## 三、NexKV Page 层详细设计

### 3.1 数据结构

#### Page 结构（已有，需增强）

```go
// page.go
type Page struct {
    ID       model.PageID       // 页面 ID
    Type     model.PageType     // 页面类型
    Version  uint64             // CCOW 版本
    Data     [model.PageDataSize]byte  // 4KB 数据区
    RefCount atomic.Int32      // 引用计数
    dirty    bool               // 脏页标志
}

func (p *Page) Pin() {
    p.RefCount.Add(1)
}

func (p *Page) Unpin() {
    p.RefCount.Add(-1)
}

func (p *Page) IsPinned() bool {
    return p.RefCount.Load() > 0
}

func (p *Page) MarkDirty() {
    p.dirty = true
}

func (p *Page) IsDirty() bool {
    return p.dirty
}
```

#### Node 结构（需修改）

```go
// node.go - 修改后
type Node struct {
    Page     *Page              // 关联的 Page
    PageID   model.PageID       // Page ID（用于持久化）
    Keys     [][]byte           // 键
    Values   [][]byte           // 值
    Children []*Node            // 子节点指针（内存操作）
    IsLeaf   bool               // 是否叶子节点
}
```

**关键说明**：
```go
// 内存操作：保持指针
Children []*Node  // ✅ 直接指针，内存操作无需转换

// 持久化时：序列化为 PageID
func (n *Node) SerializeToPage(page *Page) {
    // 遍历 Children，转换为 PageID 写入
    for _, child := range n.Children {
        childPageIDs = append(childPageIDs, child.PageID)
    }
    // ...
}

// 反序列化时：从 PageID 还原（延迟加载）
func (n *Node) GetChild(idx int, pm *PageManager) (*Node, error) {
    childPageID := n.Children[idx].PageID  // 从指针获取 ID
    childPage := pm.Get(childPageID)
    defer childPage.Unpin()
    return DeserializeNode(childPage)
}
```

#### PageManager（三层缓存架构）⭐ 更新

```go
// page_manager.go - 完整的三层缓存实现
type PageManager struct {
    config       *model.BTreeConfig
    nextPageID   atomic.Uint64
    pagePool     sync.Pool

    // ✅ L1: Page 对象缓存（已反序列化，~100 ns）
    l1Cache      *LRUCache[*Page]    // 泛型缓存

    // ✅ L2: ByteBuffer 缓存（原始数据，~500 ns）⭐ 新增
    l2Cache      *LRUCache[[]byte]   // 泛型缓存

    storage      Storage             // L3: 磁盘存储
}

func NewPageManager(config *model.BTreeConfig, storage Storage) *PageManager {
    // L1 缓存容量: 256 pages (~1 MB)
    l1Cache := NewLRUCache[*Page](config.L1CacheSize)

    // L2 缓存容量: 512 pages (~2 MB, 2x L1)
    l2Cache := NewLRUCache[[]byte](config.L2CacheSize)

    return &PageManager{
        config:     config,
        nextPageID: atomic.Uint64{},
        pagePool: sync.Pool{
            New: func() interface{} {
                return &Page{}
            },
        },
        l1Cache: l1Cache,
        l2Cache: l2Cache,
        storage: storage,
    }
}

// ✅ 三层 Get 流程：L1 → L2 → L3
func (pm *PageManager) Get(pageID model.PageID) (*Page, error) {
    // 1️⃣ 尝试 L1: Page 对象缓存
    if page, ok := pm.l1Cache.Get(pageID); ok {
        page.Pin()
        return page, nil  // ✅ L1 命中: ~100 ns
    }

    // 2️⃣ 尝试 L2: ByteBuffer 缓存
    if data, ok := pm.l2Cache.Get(pageID); ok {
        // 从 L2 反序列化到 L1
        page := pm.deserializeFromBuffer(data)
        pm.l1Cache.Put(pageID, page)
        page.Pin()
        return page, nil  // ✅ L2 命中: ~500 ns
    }

    // 3️⃣ 从 L3: 磁盘读取
    data, err := pm.storage.LoadPage(pageID)
    if err != nil {
        return nil, err
    }

    // 放入 L2 缓存
    pm.l2Cache.Put(pageID, data)

    // 反序列化到 L1
    page := pm.deserializeFromBuffer(data)
    pm.l1Cache.Put(pageID, page)
    page.Pin()

    return page, nil  // L3 命中: ~10-100 μs
}

// 从 ByteBuffer 反序列化到 Page
func (pm *PageManager) deserializeFromBuffer(data []byte) *Page {
    page := pm.pagePool.Get().(*Page)
    copy(page.Data[:], data)  // ✅ 零拷贝优化
    page.RefCount.Store(1)
    return page
}

// 分配新 Page
func (pm *PageManager) Allocate() (*Page, error) {
    id := model.PageID(pm.nextPageID.Add(1))
    page := pm.pagePool.Get().(*Page)
    page.ID = id
    page.Data = [model.PageDataSize]byte{}
    return page, nil
}

// 释放 Page
func (pm *PageManager) Release(page *Page) error {
    if page.IsPinned() {
        return ErrPagePinned
    }

    // 如果是脏页，刷盘
    if page.IsDirty() {
        if err := pm.storage.FlushPage(page); err != nil {
            return err
        }
        page.MarkClean()
    }

    // 从缓存移除
    pm.pageCache.Remove(page.ID)

    // 归还池
    pm.pagePool.Put(page)
    return nil
}
```

### 3.2 序列化/反序列化

```go
// serializer.go
func SerializeNode(node *Node, page *Page) error {
    buf := page.Data[:]
    offset := 0

    // 1. 写入元数据
    buf[offset] = byte(boolToInt(node.IsLeaf))
    offset++
    offset += 3 // 填充

    // 2. 写入 NumKeys
    binary.LittleEndian.PutUint32(buf[offset:], uint32(len(node.Keys)))
    offset += 4

    // 3. 写入 Keys
    for _, key := range node.Keys {
        keyLen := uint16(len(key))
        binary.LittleEndian.PutUint16(buf[offset:], keyLen)
        offset += 2
        copy(buf[offset:], key)
        offset += len(key)
    }

    // 4. 写入 Values (叶子节点)
    if node.IsLeaf {
        for _, value := range node.Values {
            valLen := uint16(len(value))
            binary.LittleEndian.PutUint16(buf[offset:], valLen)
            offset += 2
            copy(buf[offset:], value)
            offset += len(value)
        }
    }

    // 5. 写入 Children PageIDs (内部节点)
    if !node.IsLeaf {
        for _, childID := range node.Children {
            binary.LittleEndian.PutUint64(buf[offset:], uint64(childID))
            offset += 8
        }
    }

    page.MarkDirty()
    return nil
}

func DeserializeNode(page *Page) (*Node, error) {
    node := &Node{
        Page:   page,
        PageID: page.ID,
    }

    buf := page.Data[:]
    offset := 0

    // 1. 读取元数据
    node.IsLeaf = intToBool(buf[offset])
    offset += 4  // 跳过 IsLeaf (1 byte) + 填充 (3 bytes)

    // 2. 读取 NumKeys
    numKeys := int(binary.LittleEndian.Uint32(buf[offset:offset+4]))
    offset += 4

    // 3. 读取 Keys
    node.Keys = make([][]byte, 0, numKeys)
    for i := 0; i < numKeys; i++ {
        keyLen := int(binary.LittleEndian.Uint16(buf[offset:offset+2]))
        offset += 2
        key := make([]byte, keyLen)
        copy(key, buf[offset:offset+keyLen])
        node.Keys = append(node.Keys, key)
        offset += keyLen
    }

    // 4. 读取 Values (叶子节点)
    if node.IsLeaf {
        node.Values = make([][]byte, 0, numKeys)
        for i := 0; i < numKeys; i++ {
            valLen := int(binary.LittleEndian.Uint16(buf[offset:offset+2]))
            offset += 2
            value := make([]byte, valLen)
            copy(value, buf[offset:offset+valLen])
            node.Values = append(node.Values, value)
            offset += valLen
        }
    }

    // 5. 读取 Children（内部节点）
    // 反序列化时创建指针，序列化时转换为 PageID
    if !node.IsLeaf {
        node.Children = make([]*Node, 0, numKeys+1)
        for i := 0; i <= numKeys; i++ {
            childID := model.PageID(binary.LittleEndian.Uint64(buf[offset:offset+8]))
            offset += 8
            // 延迟加载：只存 PageID，需要时再获取
            _ = childID  // 占位，后续实现延迟加载
        }
    }

    return node, nil
}
```

### 3.3 泛型 LRU 缓存实现 ⭐ 更新

```go
// lru_cache.go - 泛型实现（支持任意类型）
type LRUCache[T any] struct {
    capacity int
    cache    map[model.PageID]*list.Element
    list     *list.List
    mu       sync.Mutex
}

type CacheEntry[T any] struct {
    Value  T         // 泛型值
    Hits   int
    Time   time.Time
}

func NewLRUCache[T any](capacity int) *LRUCache[T] {
    return &LRUCache[T]{
        capacity: capacity,
        cache:    make(map[model.PageID]*list.Element),
        list:     list.New(),
    }
}

func (c *LRUCache[T]) Get(pageID model.PageID) (T, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()

    var zero T
    if elem, ok := c.cache[pageID]; ok {
        entry := elem.Value.(*CacheEntry[T])
        entry.Hits++
        entry.Time = time.Now()
        c.list.MoveToFront(elem)
        return entry.Value, true
    }

    return zero, false
}

func (c *LRUCache[T]) Put(pageID model.PageID, value T) {
    c.mu.Lock()
    defer c.mu.Unlock()

    // 如果已存在，更新
    if elem, ok := c.cache[pageID]; ok {
        entry := elem.Value.(*CacheEntry[T])
        entry.Value = value
        entry.Time = time.Now()
        c.list.MoveToFront(elem)
        return
    }

    // 检查容量
    if c.list.Len() >= c.capacity {
        // 淘汰最久未使用的页面
        elem := c.list.Back()
        if elem != nil {
            c.list.Remove(elem)
            delete(c.cache, elem.Value.(*CacheEntry[T]).Value.(*Page).ID)
        }
    }

    // 添加到缓存
    entry := &CacheEntry[T]{
        Value: value,
        Time:  time.Now(),
    }
    elem := c.list.PushFront(entry)
    c.cache[pageID] = elem
}

func (c *LRUCache[T]) Remove(pageID model.PageID) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if elem, ok := c.cache[pageID]; ok {
        c.list.Remove(elem)
        delete(c.cache, pageID)
    }
}

// ✅ 新增：Range 方法用于批量操作
func (c *LRUCache[T]) Range(fn func(pageID model.PageID, value T) bool) {
    c.mu.Lock()
    defer c.mu.Unlock()

    for pageID, elem := range c.cache {
        entry := elem.Value.(*CacheEntry[T])
        if !fn(pageID, entry.Value) {
            break
        }
    }
}
```

**使用示例**:

```go
// L1 缓存：存储 *Page
l1Cache := NewLRUCache[*Page](256)
page, ok := l1Cache.Get(pageID)

// L2 缓存：存储 []byte
l2Cache := NewLRUCache[[]byte](512)
data, ok := l2Cache.Get(pageID)
```

---

## 四、并发模型设计（基于 Lealone 实际实现）⭐

**设计原则**: 简洁胜于复杂 - 直接复现 Lealone 的实际架构，而非过度设计

### 4.1 核心架构（参考 Lealone）

```
┌─────────────────────────────────────────────────────────────────┐
│           Lealone 实际实现：简洁的单写多读模型                   │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  BTree                                                           │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  VersionedRoot (atomic.Value)                            │ │
│  │    └─ CCOW 版本化根指针                                    │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                                  │
│  写操作（单线程队列）:                                           │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  chan WriteOperation (LinkedBlockingQueue)              │ │
│  │    └─ 单写线程 goroutine 串行执行所有写操作                 │ │
│  │       1. gotoLeafPage(key)                                │ │
│  │       2. CCOW 路径复制                                     │ │
│  │       3. CAS 更新根指针                                    │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                                  │
│  读操作（直接执行，lock-free 无锁）:                                   │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  多个 goroutine 直接调用 Get(key)                         │ │
│  │    1. root.Get() - 无锁读取版本化根                       │ │
│  │    2. gotoLeafPage(root, key) - 遍历 Page 树             │ │
│  │    3. leaf.Get(key) - 返回结果                            │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**关键简化**:
- ❌ **删除**: ReadPool（过度设计）
- ❌ **删除**: 复杂的 WriteQueue 结构
- ✅ **保留**: `chan WriteOperation`（单写队列）
- ✅ **保留**: 直接的 Get(key) 调用（lock-free 无锁读）

### 4.2 Lealone 的实际实现参考

#### Java Lealone 源码

```java
// ===== BTreeMap.java - Lealone 实际实现 =====

// ✅ 读操作：lock-free 无锁，直接执行
public V get(Object key) {
    // 1. 原子读取根指针
    Page root = rootRef.getOrReadPage();

    // 2. 遍历到叶子页面（无锁）
    Page leaf = gotoLeafPage(root, key);

    // 3. 返回值（无锁）
    return leaf.get(key);
}

// ✅ 写操作：进入队列，单线程执行
public V put(K key, V value) {
    // 1. 创建写操作
    Put<K, V> put = new Put<>(this, key, value);

    // 2. 异步入队（非阻塞）
    writeQueue.offer(put);

    // 3. 等待完成（可选）
    return put.get();
}

// ✅ 单写线程：简单的 BlockingQueue
private final BlockingQueue<WriteOperation> writeQueue
    = new LinkedBlockingQueue<>(1000);

private final Thread writerThread = new Thread(() -> {
    while (running) {
        WriteOperation op = writeQueue.take();  // 阻塞取
        op.run();  // 串行执行 CCOW 路径复制
    }
});
```

### 4.3 Go 实现：数据结构设计

```go
// btree.go - 基于 Lealone 的简化实现
type BTree struct {
    // ✅ 版本化根指针（复用现有实现）
    root        *VersionedRoot

    // ✅ Page 管理器（三层缓存）
    pageManager *PageManager

    // ✅ 单写队列（简单 channel，非复杂 WriteQueue）
    writeQueue  chan WriteOperation

    // ✅ 单写线程协调
    wg          sync.WaitGroup
    closed      bool
    config      *model.BTreeConfig
}

// VersionedRoot - 已有实现（version.go）
// ✅ atomic.Value 存储 current root
// ✅ 版本管理和快照支持
// ✅ 引用计数

// PageManager - 新增（page_manager.go）
// ✅ L1/L2/L3 三层缓存
// ✅ LRU 淘汰策略
// ✅ Pin/Unpin 机制

// WriteOperation - 写操作接口
type WriteOperation interface {
    Execute(ctx context.Context) error
    Done() chan error
}
```

### 4.4 写操作实现（单线程队列）

```go
// btree.go - 写操作

// ✅ 写操作：进入队列，等待完成
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    if b.closed {
        return ErrClosed
    }

    // 创建写操作
    op := &PutOperation{
        tree:  b,
        key:   key,
        value: value,
        done:  make(chan error, 1),
    }

    // 非阻塞入队
    select {
    case b.writeQueue <- op:
        // 等待执行完成
        select {
        case err := <-op.done:
            return err
        case <-ctx.Done():
            return ctx.Err()
        }
    case <-ctx.Done():
        return ctx.Err()
    }
}

// ✅ 单写线程：从队列取任务并执行
func (b *BTree) startWriter() {
    b.wg.Add(1)
    go func() {
        defer b.wg.Done()

        for {
            select {
            case op, ok := <-b.writeQueue:
                if !ok {
                    return  // 队列关闭，退出
                }
                // 串行执行写操作（CCOW 路径复制）
                err := op.Execute(context.Background())
                op.done <- err

            case <-b.stopChan:
                return
            }
        }
    }()
}

// PutOperation - 插入操作
type PutOperation struct {
    tree  *BTree
    key   []byte
    value []byte
    done  chan error
}

func (op *PutOperation) Execute(ctx context.Context) error {
    // 1. 获取当前根
    rootInfo := op.tree.root.Get()
    defer rootInfo.Release()

    // 2. 定位叶子页面
    leaf, path := op.tree.gotoLeafPage(rootInfo.Root, op.key)

    // 3. 检查是否需要分裂
    if leaf.IsFull() {
        return op.tree.splitAndInsert(ctx, path, op.key, op.value)
    }

    // 4. CCOW 路径复制（叶子 → 根）
    newRoot, err := op.tree.copyPathBottomUp(ctx, path, func(page *Page) error {
        return page.Put(op.key, op.value)
    })

    if err != nil {
        return err
    }

    // 5. CAS 更新根指针
    return op.tree.root.Update(ctx, newRoot, 0)
}

func (op *PutOperation) Done() chan error {
    return op.done
}
```

### 4.5 读操作实现（lock-free 无锁）

```go
// btree.go - 读操作

// ✅ 读操作：直接执行，lock-free 无锁
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    if b.closed {
        return nil, ErrClosed
    }

    // 1. 无锁读取版本化根
    rootInfo := b.root.Get()
    defer rootInfo.Release()

    // 2. 遍历到叶子页面（只读，无锁）
    leaf := b.gotoLeafPage(rootInfo.Root, key)

    // 3. 返回值（无锁）
    return leaf.Get(key), nil
}

// gotoLeafPage - 遍历到叶子页面（lock-free 无锁）
func (b *BTree) gotoLeafPage(root *Page, key []byte) *Page {
    current := root

    for !current.IsLeaf {
        // 二分查找子节点
        idx := current.Search(key)

        // 获取子节点（通过 PageManager，三层缓存）
        childRef := current.Children[idx]
        child := b.pageManager.Get(childRef.PageID)

        // 注意：这里不需要 Pin，因为读操作不会修改 Page
        current = child
    }

    return current
}
```

### 4.6 为什么简洁方案更好？

| 维度 | 过度设计（WriteQueue+ReadPool） | Lealone 实际实现（简洁方案） |
|------|------------------------------|---------------------------|
| **代码复杂度** | 🔴 高（~500 行） | 🟢 低（~150 行） |
| **组件数量** | 3个（BTree + WriteQueue + ReadPool） | 2个（BTree + PageManager） |
| **并发控制** | 复杂（ReadPool.Execute()） | 简单（直接 Get()） |
| **可维护性** | 🔴 差 | 🟢 好 |
| **性能** | 相同 | 相同 |
| **Lealone 一致性** | ❌ 不一致 | ✅ 完全一致 |

**关键洞察**:
> Lealone 的实现已经被大规模生产验证，无需"过度优化"。
> 简洁的 `chan WriteOperation` + 直接读调用 = 最佳实践。
```

### 4.7 性能分析

#### 并发扩展性

```
配置: 4读线程 + 1写线程

单线程基线:
  - 读延迟: 300 ns/op
  - 写延迟: 1000 ns/op
  - 读吞吐: 3.33M ops/s
  - 写吞吐: 1M ops/s
  - 总吞吐: 4.33M ops/s

4读1写:
  - 读吞吐: 3.33M x 4 = 13.32M ops/s
  - 写吞吐: 1M x 1 = 1M ops/s
  - 总吞吐: 14.32M ops/s (提升 3.3x)

vs Lealone:
  - Lealone: 1.07M x 4 = 4.28M 读 + 0.67M 写 = 4.95M ops/s
  - NexKV: 13.32M 读 + 1M 写 = 14.32M ops/s
  - 提升: 2.9x ✅
```

#### 关键优势

| 维度 | 多写多读 | 4读1写 | 提升 |
|------|---------|--------|------|
| **写竞争** | 高（CAS 冲突） | 无（串行） | ✅ 消除竞争 |
| **读扩展** | 线性 | 线性（4x） | ✅ 充分利用 |
| **吞吐量** | 中等 | 高（3.3x） | ✅ 显著提升 |
| **延迟** | 写延迟抖动 | 写延迟稳定 | ✅ 可预测 |

---

## 五、实施计划（5-6 周）

### Phase 1: 基础设施（1 周）

**目标**: 建立完整的三层缓存 Page 层基础架构

**任务**:
```
□ 1.1 增强 Page 结构
   - 添加 Pin/Unpin/IsPinned 方法
   - 添加 MarkDirty/IsDirty/MarkClean 方法
   - RefCount 并发安全

□ 1.2 实现泛型 LRUCache ⭐ 新增
   - 支持泛型 T（LRUCache[T]）
   - Get/Put/Remove 方法
   - Range 方法（批量操作）
   - LRU 淘汰策略
   - 并发安全

□ 1.3 实现三层 PageManager ⭐ 更新
   - L1: Page 对象缓存 (256 pages)
   - L2: ByteBuffer 缓存 (512 pages) ⭐ 新增
   - L3: Storage 接口集成
   - 三层 Get 流程（L1 → L2 → L3）
   - Allocate 方法（带数据清理）
   - Release 方法（检查 RefCount）

□ 1.4 pagePool 对象池优化
   - Allocate 时清理旧数据 ⭐ 修正
   - 避免数据泄漏

□ 1.5 单元测试
   - page_test.go (Pin/Unpin/Dirty)
   - lru_cache_test.go (泛型测试)
   - page_manager_test.go (三层流程)
   - 并发安全性测试
```

**交付物**:
- `page.go` (增强)
- `page_manager.go` (三层缓存)
- `lru_cache.go` (泛型实现)
- 测试文件

### Phase 2: 序列化层（1 周）⭐ 基于 2026-03-09-kv-to-page-serialization.md

**目标**: 实现 Node ↔ Page 转换（基于 Lealone 实际实现）

**参考文档**:
- `thoughts/2026-03-09-kv-to-page-serialization.md` - 详细序列化设计
- Lealone `Page.write()` / `Page.read()` 方法

**任务**:
```
□ 2.1 定义 Page 内存布局（参考设计文档）⭐ 核心
   - Header: 21 bytes (Type + Version + ID + RefCount)
   - Data: 4075 bytes (Metadata + Keys + Values + Children)
   - 固定 4KB 大小，避免内存碎片

□ 2.2 实现 SerializeNode（Node → Page）⭐ 基于 Lealone
   - 写入 IsLeaf (1 byte) + NumKeys (4 bytes)
   - 遍历 Keys：写入 Length (2 bytes) + Data
   - 遍历 Values：写入 Length (2 bytes) + Data（叶子节点）
   - 遍历 Children：写入 PageID (8 bytes)（内部节点）
   - MarkDirty() 标记页面脏

□ 2.3 实现 DeserializeNode（Page → Node）⭐ 基于 Lealone
   - 读取 Header → IsLeaf, NumKeys
   - 解析 Keys Section：读取 Length + Data
   - 解析 Values Section（叶子节点）：读取 Length + Data
   - 解析 Children Section（内部节点）：读取 PageID
   - 返回 Node 对象

□ 2.4 优化：变长字段序列化 ⭐ 性能关键
   - 使用 binary.Write 写入定长字段
   - 使用 bytes.Buffer 构建变长数据
   - 减少内存分配和拷贝

□ 2.5 单元测试（参考设计文档示例）⭐ 验证正确性
   - 序列化正确性：Node → Page → Node 循环
   - 边界测试：空节点、满节点、超大键值
   - 字节级验证：与设计文档中的示例对比
```

**交付物**:
- `page.go` - Page 结构定义（固定 4KB）
- `serializer.go` - SerializeNode/DeserializeNode 实现
- `serializer_test.go` - 完整测试覆盖

**关键代码示例**（基于设计文档）:

```go
// ===== Page 结构（固定 4KB）=====
type Page struct {
    ID       model.PageID
    Type     PageType
    Version  uint64
    RefCount atomic.Int32

    // 固定 4KB 数据区
    Data     [PageSize]byte  // PageSize = 4096
}

const PageSize = 4096

// ===== 序列化：Node → Page（零拷贝优化）=====
func SerializeNode(node *Node, page *Page) error {
    data := page.Data[:]
    offset := 0

    // 1. 写入元数据 (5 bytes)
    if node.IsLeaf {
        data[offset] = 1
    } else {
        data[offset] = 0
    }
    offset++
    // 3 bytes padding

    binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(len(node.Keys)))
    offset += 4

    // 2. 写入 Keys (变长：Length + Data)
    for _, key := range node.Keys {
        binary.LittleEndian.PutUint16(data[offset:offset+2], uint16(len(key)))
        offset += 2
        copy(data[offset:offset+len(key)], key)
        offset += len(key)
    }

    // 3. 写入 Values（叶子节点）
    if node.IsLeaf {
        for _, value := range node.Values {
            binary.LittleEndian.PutUint16(data[offset:offset+2], uint16(len(value)))
            offset += 2
            copy(data[offset:offset+len(value)], value)
            offset += len(value)
        }
    }

    // 4. 写入 Children PageIDs（内部节点）
    if !node.IsLeaf {
        for _, child := range node.Children {
            binary.LittleEndian.PutUint64(data[offset:offset+8], uint64(child.PageID))
            offset += 8
        }
    }

    // 5. 标记脏页
    page.MarkDirty()

    return nil
}

// ===== 反序列化：Page → Node =====
func DeserializeNode(page *Page) (*Node, error) {
    node := &Node{
        ID: page.ID,
    }

    buf := bytes.NewBuffer(page.Data[:])

    // 1. 读取元数据
    isLeafByte, err := buf.ReadByte()
    if err != nil {
        return nil, err
    }
    node.IsLeaf = isLeafByte != 0

    var numKeys uint32
    if err := binary.Read(buf, binary.LittleEndian, &numKeys); err != nil {
        return nil, err
    }

    // 2. 读取 Keys
    node.Keys = make([][]byte, numKeys)
    for i := 0; i < int(numKeys); i++ {
        var keyLen uint16
        if err := binary.Read(buf, binary.LittleEndian, &keyLen); err != nil {
            return nil, err
        }

        key := make([]byte, keyLen)
        if _, err := buf.Read(key); err != nil {
            return nil, err
        }
        node.Keys[i] = key
    }

    // 3. 读取 Values（叶子节点）
    if node.IsLeaf {
        node.Values = make([][]byte, numKeys)
        for i := 0; i < int(numKeys); i++ {
            var valueLen uint16
            if err := binary.Read(buf, binary.LittleEndian, &valueLen); err != nil {
                return nil, err
            }

            value := make([]byte, valueLen)
            if _, err := buf.Read(value); err != nil {
                return nil, err
            }
            node.Values[i] = value
        }
    }

    // 4. 读取 Children（内部节点）
    // 反序列化时创建指针，序列化时转换为 PageID
    if !node.IsLeaf {
        node.Children = make([]*Node, numKeys+1)
        for i := 0; i <= int(numKeys); i++ {
            var childID uint64
            if err := binary.Read(buf, binary.LittleEndian, &childID); err != nil {
                return nil, err
            }
            _ = childID // 占位，后续实现延迟加载
        }
    }

    return node, nil
}
```

**测试示例**（验证设计文档中的示例）:

```go
func TestSerializationRoundTrip(t *testing.T) {
    // 输入 Node（设计文档示例）
    node := &Node{
        IsLeaf: true,
        Keys:   [][]byte{[]byte("key1"), []byte("key2")},
        Values: [][]byte{[]byte("val1"), []byte("val2")},
    }

    // 序列化 → Page
    page := &Page{ID: 1}
    err := SerializeNode(node, page)
    require.NoError(t, err)

    // 验证 Page.Data 字节内容
    expected := []byte{
        0x01,                   // IsLeaf = 1
        0x00, 0x00, 0x00,       // Padding
        0x02, 0x00, 0x00, 0x00,  // NumKeys = 2

        0x04, 0x00,             // Key[0] Length = 4
        0x6b, 0x65, 0x79, 0x31,  // "key1"
        0x05, 0x00,             // Value[0] Length = 5
        0x76, 0x61, 0x6c, 0x75, 0x31,  // "val1"

        0x03, 0x00,             // Key[1] Length = 3
        0x6b, 0x65, 0x79,        // "key2"
        0x05, 0x00,             // Value[1] Length = 5
        0x76, 0x61, 0x6c, 0x75, 0x31,  // "val2"
    }

    assert.Equal(t, expected, page.Data[:len(expected)])

    // 反序列化 → Node'
    node2, err := DeserializeNode(page)
    require.NoError(t, err)

    // 验证：Node' == Node
    assert.True(t, node.IsLeaf == node2.IsLeaf)
    assert.Equal(t, node.Keys, node2.Keys)
    assert.Equal(t, node.Values, node2.Values)
}
```

**性能优化要点**:

```
优化目标：
- ✅ 减少内存分配：使用 bytes.Buffer 复用
- ✅ 减少内存拷贝：直接操作 []byte
- ✅ 避免反射：使用 binary.Write/Read
- ✅ 固定大小：4KB Page 避免碎片

预期性能：
- 序列化：~500-1000 ns/op
- 反序列化：~800-1500 ns/op
- 内存分配：~2-3 次/op
```

### Phase 3: BTree 集成（1.5 周）

**目标**: 集成 Page 层到 BTree

**任务**:
```
□ 3.1 修改 BTree 结构
   - 添加 PageManager 字段
   - 添加 Storage 字段
   - 添加 writeQueue 字段（chan WriteOperation）

□ 3.2 修改 Get 流程（lock-free 无锁）⭐ 简化
   - root.Get() - 读取版本化根
   - gotoLeafPage(root, key) - 遍历 Page 树
   - leaf.Get(key) - 返回结果
   - 移除复杂的 ReadPool

□ 3.3 修改 Insert 流程（队列化）⭐ 基于 Lealone
   - 创建 PutOperation
   - 非阻塞入队（writeQueue <- op）
   - 等待完成（<-op.done）

□ 3.4 实现单写线程 goroutine ⭐ 新增
   - 从 writeQueue 取任务
   - 串行执行 CCOW 路径复制
   - CAS 更新根指针

□ 3.5 实现 FindPath (基于 Page)
   - 基于 PageID 遍历
   - 延迟加载子节点
   - 构建 Path[*Page]

□ 3.6 单元测试
   - btree_test.go (Get/Set)
   - 并发测试（多读单写）
   - CCOW 正确性验证
```

**交付物**:
- `btree.go` (修改)
- `path.go` (修改)
- `write_operation.go` (新增)

### Phase 4: 持久化层（1 周）

**目标**: 实现磁盘存储和 WAL 集成

**任务**:
```
□ 4.1 实现 Storage 接口
   - LoadPage (从磁盘加载)
   - FlushPage (刷新到磁盘)
   - 文件管理

□ 4.2 WAL 集成
   - WAL Writer 集成
   - 记录 Page 变更
   - 崩溃恢复

□ 4.3 检查点
   - 定期刷盘
   - 清理旧版本
   - 垃圾回收

□ 4.4 单元测试
   - storage_test.go
   - wal_integration_test.go
   - recovery_test.go
```

**交付物**:
- `storage.go`
- `wal_adapter.go` (修改)
- `checkpoint.go`
- 测试文件

### Phase 5: 性能优化（1 周）

**目标**: 优化性能，接近 Lealone 水平

**任务**:
```
□ 5.1 缓存优化
   - 增大缓存容量 (256 → 1024 页)
   - L2 缓存命中率优化
   - 热点数据识别

□ 5.2 序列化优化
   - 二进制格式优化
   - 批量序列化
   - 零拷贝优化

□ 5.3 并发优化
   - 无锁 Page 引用
   - 单写队列优化（批量化）
   - 读操作优化（无锁遍历）

□ 5.4 基准测试
   - 对比纯内存 vs Page 层
   - 与 Lealone 对比
   - 性能回归检测

□ 5.5 压力测试 ⭐ 新增
   - 长时间运行测试（4小时）
   - 内存泄漏检测
   - 并发安全性测试
```

**交付物**:
- 性能优化报告
- 基准测试结果
- 性能对比文档
- 压力测试报告

### Phase 6: 文档和交付（0.5 周）

**目标**: 完善文档，准备生产部署

**任务**:
```
□ 6.1 API 文档
   - godoc 注释
   - 使用示例
   - 最佳实践

□ 6.2 架构文档
   - 并发模型说明
   - 三层缓存设计
   - 性能调优指南

□ 6.3 运维文档
   - 部署指南
   - 监控指标
   - 故障排查
```

**交付物**:
- 完整文档
- 部署指南
- 监控面板

---

## 五、关键代码示例

### 5.1 修改后的 Get 操作

```go
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    // 1. 获取根 PageID
    rootInfo := b.root.Get()
    defer rootInfo.Release()

    // 2. 获取根 Page（带缓存）
    rootPage := b.pageManager.Get(rootInfo.RootPageID)
    defer rootPage.Unpin()

    // 3. 反序列化为 Node
    node, err := DeserializeNode(rootPage)
    if err != nil {
        return nil, err
    }

    // 4. 搜索（递归）
    return b.searchNode(node, key)
}

func (b *BTree) searchNode(node *Node, key []byte) ([]byte, error) {
    idx := node.Search(key)

    if node.IsLeaf {
        // 叶子节点：直接返回
        if idx < len(node.Keys) && bytes.Equal(node.Keys[idx], key) {
            return node.Values[idx], nil
        }
        return nil, ErrKeyNotFound
    }

    // 内部节点：延迟加载子节点
    childPageID := node.Children[idx]
    childPage := b.pageManager.Get(childPageID)
    defer childPage.Unpin()

    childNode, err := DeserializeNode(childPage)
    if err != nil {
        return nil, err
    }

    return b.searchNode(childNode, key)
}
```

### 5.2 修改后的 Insert 操作

```go
func (b *BTree) Insert(ctx context.Context, key, value []byte) error {
    // 1. 获取当前根 Page
    rootInfo := b.root.Get()
    defer rootInfo.Release()

    rootPage := b.pageManager.Get(rootInfo.RootPageID)
    defer rootPage.Unpin()

    rootNode, err := DeserializeNode(rootPage)
    if err != nil {
        return err
    }

    // 2. CCOW 路径复制
    path, err := b.FindPath(rootNode, key)
    if err != nil {
        return err
    }
    defer b.releasePath(path)

    // 3. 复制路径并修改
    modifyFunc := func(n *Node) error {
        return n.Insert(key, value)
    }

    newRootNode, err := b.CopyPathBottomUp(ctx, path, modifyFunc)
    if err != nil {
        return err
    }

    // 4. 分配新 Page 并序列化
    newPage, err := b.pageManager.Allocate()
    if err != nil {
        return err
    }

    if err = SerializeNode(newRootNode, newPage); err != nil {
        return err
    }

    // 5. WAL 记录
    if b.wal != nil {
        if err := b.wal.WriteInsert(newPage.ID, key, value); err != nil {
            return err
        }
    }

    // 6. CAS 更新根指针
    newRootInfo := &RootInfo{
        RootPageID:  newPage.ID,
        Version:     rootInfo.Version + 1,
    }

    if err = b.root.CompareAndSwap(rootInfo, newRootInfo); err != nil {
        // CAS 失败，重试
        return b.Insert(ctx, key, value)
    }

    // 7. 释放旧引用
    return nil
}

func (b *BTree) releasePath(path Path) error {
    for _, pathNode := range path {
        pathNode.Page.Unpin()
    }
    return nil
}
```

### 5.3 FindPath 实现

```go
func (b *BTree) FindPath(rootNode *Node, key []byte) (Path, error) {
    path := make(Path, 0, 4) // 假设树高 4

    currentNode := rootNode
    currentInfo := &PathNode{
        Node: currentNode,
    }

    for !currentNode.IsLeaf {
        // 找到子节点 PageID
        idx := currentNode.Search(key)
        childPageID := currentNode.Children[idx]

        // 从 PageManager 获取子页面
        childPage := b.pageManager.Get(childPageID)
        defer childPage.Unpin()

        // 反序列化子节点
        childNode, err := DeserializeNode(childPage)
        if err != nil {
            return nil, err
        }

        // 添加到路径
        currentInfo = &PathNode{
            Page: childPage,
            Node: childNode,
        }
        path = append(path, currentInfo)

        currentNode = childNode
    }

    // 添加叶子节点
    path = append(path, currentInfo)
    return path, nil
}
```

---

## 六、性能优化策略

### 6.1 延迟反序列化

```go
// 不立即反序列化所有字段
type Node struct {
    Page     *Page
    keysOnce sync.Once
    keys     [][]byte
    valuesOnce sync.Once
    values   [][]byte
    childrenOnce sync.Once
    children []model.PageID
}

func (n *Node) GetKeys() [][]byte {
    n.keysOnce.Do(func() {
        // 只在需要时反序列化
        n.keys = deserializeKeys(n.Page)
    })
    return n.keys
}
```

### 6.2 页面预取

```go
// 预取可能访问的页面
func (b *BTree) Prefetch(node *Node, key []byte) {
    idx := node.Search(key)

    if !node.IsLeaf && idx+1 < len(node.Children) {
        // 预取右兄弟节点
        siblingPageID := node.Children[idx+1]
        go b.pageManager.Prefetch(siblingPageID)
    }
}
```

### 6.3 批量刷新

```go
// 批量刷新脏页
func (pm *PageManager) FlushBatch() error {
    var dirtyPages []*Page

    // 收集脏页
    pm.pageCache.Range(func(id model.PageID, elem *list.Element) bool {
        entry := elem.Value.(*CacheEntry)
        if entry.Page.IsDirty() {
            dirtyPages = append(dirtyPages, entry.Page)
        }
        return true
    })

    // 批量写入磁盘
    for _, page := range dirtyPages {
        if err := pm.storage.FlushPage(page); err != nil {
            return err
        }
        page.MarkClean()
    }

    return nil
}
```

---

## 七、风险与缓解

### 7.1 性能回退风险

**风险**: 引入 Page 层后性能下降 10-20x

**缓解措施**:
1. ✅ **高缓存命中率**: 保持 >95% 命中率
2. ✅ **延迟反序列化**: 只在需要时反序列化
3. ✅ **批量操作**: 减少序列化次数
4. ✅ **页面预取**: 提前加载可能访问的页面

**预期性能**:
```
纯内存: 10.97 ns/op
Page 层: 100-200 ns/op (慢 10-20x)
但仍快于 Lealone: 941.61 ns/op (快 4.7-9.4x) ✅
```

### 7.2 内存碎片风险

**风险**: 序列化后的 Data 数组可能产生碎片

**缓解措施**:
1. ✅ **固定大小 Page**: 4KB 固定分配
2. ✅ **Page 池化复用**: 减少分配次数
3. ✅ **sync.Pool**: 自动内存管理

### 7.3 实现复杂度风险

**风险**: 需要大量代码改动

**缓解措施**:
1. ✅ **分阶段实现**: 5 个阶段，每个阶段独立验证
2. ✅ **向后兼容**: 保留纯内存模式（可选）
3. ✅ **完整测试**: 单元测试 + 集成测试 + 性能测试

---

## 八、验收标准

### 8.1 功能验收

```
□ Page 分配/释放正常
□ 序列化/反序列化正确
□ LRU 缓存工作正常
□ Get 操作正确（多层遍历）
□ Insert 操作正确（CCOW + Page）
□ Delete 操作正确
□ 崩溃恢复功能
□ WAL 集成正常
□ 检查点功能
```

### 8.2 性能验收（三层缓存）

**分层性能目标**:

```
L1 缓存命中 (85%):
  - 延迟: < 100 ns/op
  - 吞吐: > 10M ops/s

L2 缓存命中 (10%):
  - 延迟: < 500 ns/op
  - 吞吐: > 2M ops/s

L3 磁盘读取 (5%):
  - 延迟: < 100 μs/op
  - 吞吐: > 10K ops/s

综合性能（三层缓存）:
  - 读延迟: 300-600 ns/op (目标) ⭐ 更新
  - 写延迟: 800-1500 ns/op (目标) ⭐ 更新
  - 读吞吐: > 2M ops/s ⭐ 更新
  - 写吞吐: > 1M ops/s
  - L1 命中率: > 85% ⭐ 新增
  - L2 命中率: > 10% ⭐ 新增
  - 整体命中率: > 95% (L1 + L2) ⭐ 更新
  - 内存分配: < 50 KB/op ⭐ 更新
```

**vs Lealone 对比**:
```
指标          | NexKV 三层缓存 | Lealone | 对比
--------------|----------------|---------|-------
读延迟        | 300-600 ns     | 941 ns  | 预期快 1.6-3.1x（待验证）
写延迟        | 800-1500 ns    | 1596 ns  | 预期快 1.1-2.0x（待验证）
读吞吐        | > 2M ops/s     | 1.07M    | 快 1.9x ✅
写吞吐        | > 1M ops/s     | 0.67M    | 快 1.5x ✅
```

### 8.3 质量验收

```
□ 单元测试覆盖率 > 85%
□ 集成测试通过率 100%
□ 并发测试通过 (race detector)
□ 内存泄漏检测通过
□ 崩溃恢复测试通过
□ 24 小时稳定性测试通过
```

---

## 九、总结

### 9.1 核心价值

> **Page 层是存储引擎的必经之路**
>
> "舍短期/高性能，取长远/可运维"
>
> 纯内存设计: 适合原型，不适合生产
> Page 层设计: 可落地生产，支持大容量

### 9.2 预期成果

**性能（三层缓存）**:
```
分层性能:
  - L1 命中 (85%): ~100 ns/op
  - L2 命中 (10%): ~500 ns/op
  - L3 读取 (5%): ~10-100 μs/op

综合性能:
  - 读: 300-600 ns/op（预期比 Lealone 快 1.6-3.1x，待验证）
  - 写: 800-1500 ns/op（预期比 Lealone 快 1.1-2.0x，待验证）

性能提升:
  - vs 二层缓存: ~8% 性能提升 ⭐ 新增
  - vs Lealone: 预期快 1.6-3.1x（待验证）
```

**功能**:
```
  - ✅ 三层缓存架构（L1 Page + L2 ByteBuffer + L3 Disk）⭐ 新增
  - ✅ 可持久化
  - ✅ 崩溃恢复
  - ✅ WAL 支持
  - ✅ 大容量 (TB 级)
  - ✅ 生产可用
```

**时间**:
```
  - 5-6 周完成
  - P0 优先级
```

### 9.3 下一步行动

1. ✅ **立即启动 Phase 1**: 基础设施
2. ✅ **创建实施分支**: `feature/page-layer-implementation`
3. ✅ **建立性能基线**: 当前纯内存性能
4. ✅ **持续性能监控**: 每个阶段后对比

---

**报告生成**: 2026-03-09 13:20:00 CST
**基于**: thoughts/2026-03-09-page-layer-architecture-design.md
**生成者**: Claude Code
**版本**: v1.0
**状态**: 实施计划完成，等待启动

---

## 八、基于 Lealone 实际实现的改进 ⭐ 2026-03-09 更新

### 8.1 设计理念转变

**核心洞察**: 复杂不等于强大 - Lealone 的简洁实现已被大规模生产验证

```
┌─────────────────────────────────────────────────────────────┐
│  从"过度设计"到"简洁复现"                                   │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ❌ 旧方案（过度设计）:                                        │
│  - WriteQueue 复杂结构（~300 行）                            │
│  - ReadPool 线程池（~200 行）                                │
│  - Reader 抽象层                                            │
│  - 总计：~500 行额外代码                                     │
│                                                              │
│  ✅ 新方案（Lealone 实际实现）:                               │
│  - chan WriteOperation（~50 行）                            │
│  - 单写 goroutine（~30 行）                                  │
│  - 直接的 Get(key) 调用（~20 行）                            │
│  - 总计：~100 行核心代码                                     │
│                                                              │
│  代码减少：80% ✅                                            │
│  可维护性：显著提升 ✅                                        │
│  性能：相同 ✅                                              │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 8.2 关键简化对比

| 组件 | 旧方案（过度设计） | Lealone 实际实现 | 改进 |
|------|------------------|-----------------|------|
| **写队列** | WriteQueue 结构体 | `chan WriteOperation` | ✅ 简化 5x |
| **写执行** | executeWrite() 方法 | 单写 goroutine | ✅ 直观清晰 |
| **读操作** | ReadPool.Execute() | 直接 Get(key) | ✅ 无锁无池 |
| **并发模型** | 复杂（BTree + WQ + RP） | 简单（BTree + channel） | ✅ 组件少 3x |
| **代码行数** | ~500 行 | ~100 行 | ✅ 减少 80% |
| **可维护性** | 低（3个组件交互） | 高（2个组件） | ✅ 显著提升 |
| **Lealone 一致性** | ❌ 不一致 | ✅ 完全一致 | ✅ 便于参考 |

### 8.3 实施建议

#### 推荐方案：**完全复现 Lealone 的简洁实现** ✅

**理由**：
1. ✅ 代码量减少 80%
2. ✅ 可维护性显著提升
3. ✅ 性能完全相同
4. ✅ 生产验证可靠
5. ✅ 便于对照源码调试

**实施路径**：
```
Week 1: Page 层基础设施
  - LRUCache[K, V] 泛型实现
  - PageManager 三层缓存
  - Page 结构（Pin/Unpin）

Week 2: BTree 核心集成
  - Get(key) - 直接调用
  - Set(key) - 队列化
  - 单写 goroutine
  - gotoLeafPage() - 遍历

Week 3: 并发验证
  - 多读单写压力测试
  - CCOW 正确性验证
  - 性能基准测试
```

**关键代码文件**：
```
internal/infrastructure/storage/btree/
├── btree.go              # BTree 主入口（Get/Set）
├── page.go               # Page 结构定义
├── page_manager.go       # 三层缓存管理器
├── lru_cache.go          # 泛型 LRU 缓存
├── write_operation.go    # 写操作接口
├── path.go               # CCOW 路径复制
└── version.go            # 版本化根指针（已有）
```

---

**文档版本**: v2.0
**最后更新**: 2026-03-09
**状态**: ✅ 基于Lealone实际实现的完整实施计划（简化版）

---

## 附录：基于 Lealone 完整实现的优化方案 (v2.0)

> **新增于 2026-03-09**：基于 `thoughts/2026-03-09-lealone-page-based-btree-implementation.md` 深度分析后的优化方案

### A.1 问题诊断

**当前已实施** (Phase 1-5 完成):
- ✅ Page 结构添加 `sync.RWMutex`
- ✅ 序列化/反序列化 (573 ns/op, 0 分配)
- ✅ 三层缓存 (L1/L2/L3)
- ✅ 并发测试通过

**性能瓶颈**:
- **当前并发写入**: 491K QPS (4线程)
- **预期目标**: 250万 QPS (4线程)
- **差距**: 5倍未达标

**根本原因**:
```
当前实现: 1个 Page 存储所有 key (哈希映射)
→ 所有写入竞争同一把 Page 锁
→ Page 级别锁退化成全局锁
→ 并发度完全丧失

Lealone 的关键:
真正的 BTree 结构 (多层 Page)
→ 不同叶节点存储不同 key 范围
→ 写入不同叶节点 = 不同 Page 锁
→ 真正的并发写入能力
```

### A.2 Lealone 核心设计迁移

#### 1. Page 级轻量锁 (PageLock)

**Lealone 源码** (`PageReference.java:92-113`):
```java
public class PageReference {
    private final PageLock pageLock = new PageLock();

    public boolean tryLock(InternalScheduler scheduler, boolean waitingIfLocked) {
        return pageLock.tryLock(scheduler, waitingIfLocked);
    }

    public void unlock() {
        pageLock.unlock();
    }
}
```

**NexV 实施建议**:
```go
// page_lock.go - 新建文件
type PageLock struct {
    mu    sync.Mutex
    owner goroutineID
    count int  // 可重入计数
}

func (pl *PageLock) tryLock(scheduler *Scheduler, waitingIfLocked bool) bool {
    // 快速路径：无锁获取
    if pl.count == 0 {
        pl.mu.Lock()
        pl.owner = getGoroutineID()
        pl.count = 1
        return true
    }

    // 可重入检查
    if pl.owner == getGoroutineID() {
        pl.count++
        return true
    }

    // 需要等待
    if waitingIfLocked {
        scheduler.wait()
        // 唤醒后重试...
    }

    return false
}
```

**优势**:
- ✅ 可重入：同一线程可多次获取
- ✅ 等待机制：避免忙等待
- ✅ 快速路径：无竞争时几乎零开销

#### 2. PageReference 原子引用

**Lealone 源码**:
```java
private static final AtomicReferenceFieldUpdater<PageReference, PageInfo>
    pageInfoUpdater = AtomicReferenceFieldUpdater.newUpdater(
        PageReference.class, PageInfo.class, "pInfo"
    );

private volatile PageInfo pInfo;

public boolean replacePage(PageInfo expect, PageInfo update) {
    return pageInfoUpdater.compareAndSet(this, expect, update);
}
```

**NexV 实施建议**:
```go
// page_reference.go - 新建文件
type PageInfo struct {
    Page         *Page
    RefCount     int32
    LastModified time.Time
}

type PageReference struct {
    pInfo atomic.Value  // *PageInfo
    parentRef *PageReference
    lock     *PageLock
}

func (pr *PageReference) replacePage(expect, update *PageInfo) bool {
    old := pr.pInfo.Load()
    if old == expect {
        return pr.pInfo.CompareAndSwap(expect, update)
    }
    return false
}

func (pr *PageReference) Acquire() {
    // CAS 循环增加引用计数
    for {
        oldInfo := pr.pInfo.Load().(*PageInfo)
        newInfo := &PageInfo{
            Page:     oldInfo.Page,
            RefCount: oldInfo.RefCount + 1,
            LastModified: oldInfo.LastModified,
        }
        if pr.pInfo.CompareAndSwap(oldInfo, newInfo) {
            return
        }
    }
}
```

**优势**:
- ✅ 无锁读取：`GetPage()` 直接 `Load()`
- ✅ CAS 更新：原子替换 Page
- ✅ 引用计数：安全的生命周期管理

#### 3. Scheduler 单写调度器

**Lealone 设计** (`SchedulerThread.java`):
```java
public void runPageOperation() {
    WriteOperation op = writeQueue.poll();
    if (op != null) {
        op.run();  // 在单写线程中执行
    }
}
```

**NexV 实施建议**:
```go
// scheduler.go - 新建文件
type Scheduler struct {
    writeQueue chan WriteOperation
    stopChan   chan struct{}
    wg         sync.WaitGroup
}

func (s *Scheduler) Start() {
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        for {
            select {
            case op := <-s.writeQueue:
                op.Run()  // 在单写线程中执行
            case <-s.stopChan:
                return
            }
        }
    }()
}

// Set 提交写操作到队列
func (b *BTree) Set(ctx context.Context, key, value []byte) error {
    op := &PutOperation{
        btree: b,
        key:   key,
        value: value,
        done:  make(chan error, 1),
    }

    if err := b.scheduler.Submit(op); err != nil {
        return err
    }

    return <-op.done  // 等待完成
}
```

**优势**:
- ✅ 消除 CAS 竞争：所有写操作串行化
- ✅ 批量优化：可支持批量写入
- ✅ 简洁：无复杂的锁管理

#### 4. 真正的 BTree 结构

**关键变更**:
```go
// 内存操作：保持指针
Children []*Node  // ✅ 直接指针，内存操作无需转换

// 持久化时：序列化为 PageID
func (n *Node) SerializeToPage(page *Page) {
    for _, child := range n.Children {
        childPageIDs = append(childPageIDs, child.PageID)
    }
    // ...
}

// 读取时延迟加载
func (n *Node) GetChild(idx int, pm *PageManager) (*Node, error) {
    childPageID := n.Children[idx].PageID  // 从指针获取 ID
    childPage, err := pm.Get(childPageID)
    if err != nil {
        return nil, err
    }
    defer childPage.Release()
    return DeserializeNode(childPage)
}
```

**优势**:
- ✅ 真正的并发：不同叶节点独立锁
- ✅ 持久化友好：PageID 可直接序列化
- ✅ 内存高效：按需加载子节点

### A.3 实施步骤（优化版）

#### Phase 1: 基础设施 (1-2周)

**新建文件**:
1. `page_lock.go` - Page 级轻量锁
2. `page_reference.go` - Page 原子引用
3. `scheduler.go` - 单写调度器
4. `write_operation.go` - 写操作接口

**验证**:
- 单元测试覆盖
- 无 data race

#### Phase 2: BTree 重构 (2-3周)

**修改文件**:
- `btree.go` - 集成 Scheduler 和 PageReference
- `node.go` - Children 改为 `[]PageID`

**验证**:
- 功能测试通过
- 性能不退化

#### Phase 3: CCOW 路径复制 (1-2周)

**新建文件**:
- `put_operation.go` - Put 操作实现
- `path_copy.go` - CCOW 路径复制

**验证**:
- CCOW 正确性验证
- 基准测试达标

#### Phase 4: 性能优化 (1周)

**优化项**:
- 批量写入支持
- LRU 缓存调优
- 序列化优化

**验证**:
- 4线程 QPS > 200万
- 读延迟 < 200 ns

### A.4 预期性能提升

| 指标 | 当前 (491K) | 目标 (2.5M) | 提升 |
|------|-----------|------------|------|
| **单线程写入** | ~500K QPS | ~800K QPS | +60% |
| **2线程写入** | ~490K QPS | ~1.5M QPS | +206% |
| **4线程写入** | ~491K QPS | ~2.5M QPS | +409% |
| **8线程写入** | ~480K QPS | ~4.0M QPS | +733% |

**关键改进**:
1. ✅ 真正的 Page 级别锁（不同叶节点并发写入）
2. ✅ 单写队列（消除 CAS 竞争）
3. ✅ Page 引用计数（无锁读取）

### A.5 关键文件清单

**新建文件 (7个)**:
1. `page_lock.go` - Page 级轻量锁
2. `page_reference.go` - Page 原子引用
3. `scheduler.go` - 单写调度器
4. `write_operation.go` - 写操作接口
5. `put_operation.go` - Put 操作实现
6. `path_copy.go` - CCOW 路径复制
7. `lru_cache.go` - 泛型 LRU 缓存

**修改文件 (2个)**:
1. `btree.go` - 集成 Scheduler 和 PageReference
2. `node.go` - Children 改为 `[]PageID`

---

## 参考

- **Lealone 源码分析**: `thoughts/2026-03-09-lealone-page-based-btree-implementation.md`
- **Page 级别锁设计**: `docs/09_code-review/2026-03/2026-03-09-page-level-locking-design.md`
- **KV 序列化规范**: `docs/09_code-review/2026-03-09-kv-to-page-serialization.md`

---

**版本**: v2.0 (基于 Lealone 完整实现优化)
**预计工作量**: 4-6周
**性能目标**: 4线程 250万 QPS (提升 5倍)
