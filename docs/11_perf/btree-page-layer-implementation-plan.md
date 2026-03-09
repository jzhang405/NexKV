# Page 层实施计划 - 基于 thoughts/2026-03-09 设计文档

**日期**: 2026-03-09
**基于**: `thoughts/2026-03-09-page-layer-architecture-design.md`
**优先级**: P0（生产环境必要条件）

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

**性能提升**（vs Lealone）:
- 读延迟: 300-600 ns vs Lealone 941 ns → **快 1.6-3.1x** ✅
- 写延迟: 800-1500 ns vs Lealone 1596 ns → **快 1.1-2.0x** ✅

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
    return pr pInfo.CompareAndSwap(expect, update)
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
    Children []*Node            // 子节点指针（需改为 PageID）
    IsLeaf   bool               // 是否叶子节点
}
```

**关键修改**:
```go
// 之前: 直接指针
Children []*Node

// 之后: PageID 引用
Children []model.PageID  // ← 只存储 PageID

// 读取时延迟加载
func (n *Node) GetChild(idx int, pm *PageManager) (*Node, error) {
    childPageID := n.Children[idx]
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

    // 5. 读取 Children PageIDs (内部节点)
    if !node.IsLeaf {
        node.Children = make([]model.PageID, 0, numKeys+1)
        for i := 0; i <= numKeys; i++ {
            childID := model.PageID(binary.LittleEndian.Uint64(buf[offset:offset+8]))
            offset += 8
            node.Children = append(node.Children, childID)
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

## 四、实施计划（5-6 周）

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

### Phase 2: 序列化层（1 周）

**目标**: 实现 Node ↔ Page 转换

**任务**:
```
□ 2.1 实现 SerializeNode
   - 写入元数据
   - 写入 Keys/Values
   - 写入 Children PageIDs

□ 2.2 实现 DeserializeNode
   - 读取元数据
   - 读取 Keys/Values
   - 读取 Children PageIDs

□ 2.3 修改 Node 结构
   - 添加 Page 字段
   - 添加 PageID 字段
   - Children 改为 PageID 列表

□ 2.4 单元测试
   - serializer_test.go
   - 序列化正确性验证
   - 边界测试
```

**交付物**:
- `serializer.go`
- `node.go` (修改)
- 测试文件

### Phase 3: BTree 集成（2 周）

**目标**: 集成 Page 层到 BTree

**任务**:
```
□ 3.1 修改 BTree 结构
   - 添加 PageManager 字段
   - 添加 Storage 字段

□ 3.2 修改 Get 流程
   - Get(PageID) → 反序列化 → Node
   - 递归查找时延迟加载
   - Pin/Unpin 管理

□ 3.3 修改 Insert 流程
   - FindPath (基于 PageID)
   - CopyPathBottomUp (创建新 Page)
   - 序列化新 Node → Page
   - CAS 更新根 PageID

□ 3.4 实现 FindPath (多层)
   - 基于 PageID 遍历
   - 延迟加载子节点
   - 构建 PathNode[*Page]

□ 3.5 单元测试
   - btree_page_test.go
   - 集成测试
   - 性能测试
```

**交付物**:
- `btree.go` (修改)
- `path.go` (修改)
- 测试文件

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
   - 预取机制
   - 热点数据识别

□ 5.2 序列化优化
   - 二进制格式优化
   - 批量序列化
   - 零拷贝优化

□ 5.3 并发优化
   - 无锁 Page 引用
   - 减少 critical section
   - 批量操作

□ 5.4 基准测试
   - 对比纯内存 vs Page 层
   - 与 Lealone 对比
   - 性能回归检测
```

**交付物**:
- 性能优化报告
- 基准测试结果
- 性能对比文档

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
读延迟        | 300-600 ns     | 941 ns  | 快 1.6-3.1x ✅
写延迟        | 800-1500 ns    | 1596 ns  | 快 1.1-2.0x ✅
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
  - 读: 300-600 ns/op (仍比 Lealone 快 1.6-3.1x) ✅
  - 写: 800-1500 ns/op (仍比 Lealone 快 1.1-2.0x) ✅

性能提升:
  - vs 二层缓存: ~8% 性能提升 ⭐ 新增
  - vs Lealone: 1.6-3.1x 更快 ✅
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
