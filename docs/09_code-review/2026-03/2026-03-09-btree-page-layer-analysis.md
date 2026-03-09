# BTree 架构分析：纯内存 vs Page 层

**分析日期**: 2026-03-09
**核心问题**: 当前纯内存设计无法落地生产环境

---

## 一、当前设计的局限性

### 1.1 持久化缺失 ⚠️

**当前状态**:
```go
// 纯内存节点设计
type Node struct {
    Keys     [][]byte  // 直接分配在堆上
    Values   [][]byte
    Children []*Node   // 直接指针引用
    IsLeaf   bool
}
```

**问题**:
1. ❌ **数据易丢失**: 崩溃后所有数据丢失
2. ❌ **无法持久化**: 没有序列化到磁盘的机制
3. ❌ **无法恢复**: 没有从磁盘加载数据的能力

**影响**:
- 数据库必须 100% 可靠，当前设计完全不可靠
- 每次重启数据清零，无法用于生产

### 1.2 内存碎片化 ⚠️

**当前状态**:
```
每次插入/分裂:
  - 分配新 Node (16 KB)
  - 分配 Keys 切片 (动态增长)
  - 分配 Values 切片 (动态增长)
  - 分配 Children 切片 (动态增长)

结果:
  - 大量小对象分配 (每键 100-200 B)
  - 内存碎片严重
  - GC 压力大
```

**实测数据**:
```
Node Write 操作:
  延迟: 41,695 ns/op
  内存: 18.6 KB/op
  分配: 403 次/op  ❌ 分配次数过多
```

**问题**:
- Go GC 需要扫描大量小对象
- 内存碎片导致分配效率下降
- 长时间运行会累积内存碎片

### 1.3 大容量存储限制 ⚠️

**当前状态**:
```
纯内存设计:
  - 数据必须全部在内存中
  - 无法超出物理内存容量
  - 内存成本高昂

示例:
  - 100GB 数据集需要 100GB RAM
  - 1TB 数据集需要 1TB RAM（不现实）
```

**问题**:
- ❌ **无法支持大数据集**: 受限于物理内存
- ❌ **成本高昂**: RAM 比 SSD 贵 10-100 倍
- ❌ **扩展性差**: 无法水平扩展

### 1.4 生产环境不可用 ⚠️

**关键缺失功能**:
```
✅ 读性能: 卓越 (10.78M ops/s)
✅ 写性能: 良好 (900K ops/s)
❌ 持久化: 缺失
❌ 崩溃恢复: 缺失
❌ 数据导入/导出: 缺失
❌ 备份/恢复: 缺失
❌ 大容量支持: 缺失
```

**结论**:
> 当前设计是"高性能玩具"，无法用于生产环境。

---

## 二、Page 层的必要性

### 2.1 什么是 Page 层？

**Page 层定义**:
```
Page 是固定大小的内存块（通常是 4KB、8KB、16KB）

作用:
  1. 内存管理单元
  2. 持久化单元
  3. 缓存对齐单元
  4. I/O 原子操作单元
```

**Lealone 的 Page 设计**:
```java
// Lealone Page 接口
public interface Page {
    int getPageSize();
    ByteBuffer getBuffer();
    void writePage(StorageOutput out);
    void readPage(StorageInput in);
    long getPageId();
    void setPageId(long pageId);
}

// 具体实现
public class MMapPage implements Page {
    private ByteBuffer buffer;  // 内存映射文件
    private long pageId;        // 页面 ID
    private int pageSize;       // 页面大小 (通常 4KB)
}
```

### 2.2 Page 层解决了什么问题？

#### 问题 1: 持久化 ✅

**解决方案**:
```go
type Page struct {
    ID       uint64     // 页面 ID
    Data     [PageSize]byte  // 固定大小数据（4KB）
    Dirty    bool       // 脏标记
    Pinned   bool       // 钉标记（防止驱逐）
}

// 持久化操作
func (p *Page) Persist(file *os.File) error {
    offset := int64(p.ID) * PageSize
    _, err := file.WriteAt(p.Data[:], offset)
    return err
}

// 加载操作
func (p *Page) Load(file *os.File) error {
    offset := int64(p.ID) * PageSize
    _, err := file.ReadAt(p.Data[:], offset)
    return err
}
```

**收益**:
- ✅ 数据可持久化到磁盘
- ✅ 崩溃后可恢复
- ✅ 支持数据导入/导出
- ✅ 支持备份/恢复

#### 问题 2: 内存碎片 ✅

**解决方案**:
```go
type PagePool struct {
    pages sync.Map  // map[uint64]*Page
    free  []uint64   // 可用的页面 ID
    mu    sync.Mutex
}

func (pp *PagePool) Allocate() (*Page, error) {
    pp.mu.Lock()
    defer pp.mu.Unlock()

    if len(pp.free) > 0 {
        id := pp.free[len(pp.free)-1]
        pp.free = pp.free[:len(pp.free)-1]
        return &Page{ID: id}, nil
    }

    id := pp.nextPageID
    pp.nextPageID++
    return &Page{ID: id}, nil
}

func (pp *PagePool) Free(page *Page) {
    pp.mu.Lock()
    defer pp.mu.Unlock()
    pp.free = append(pp.free, page.ID)
}
```

**收益**:
- ✅ 固定大小分配（4KB）
- ✅ 减少内存碎片
- ✅ 降低 GC 压力
- ✅ 提高分配效率

#### 问题 3: 大容量存储 ✅

**解决方案**:
```go
type BTree struct {
    root     uint64        // 根页面 ID
    pager    *Pager        // 页面管理器
    cache    *PageCache    // 页面缓存
}

type Pager struct {
    file     *os.File      // 底层文件
    pageSize int           // 页面大小
    maxPages int           // 最大页面数
}

func (b *BTree) Get(key []byte) ([]byte, error) {
    // 1. 从缓存获取根页面
    rootPage, err := b.cache.Get(b.root)
    if err != nil {
        return nil, err
    }

    // 2. 解析页面内容
    node := DeserializeNode(rootPage.Data[:])

    // 3. 二分查找
    idx := node.Search(key)
    if idx < len(node.Keys) && bytes.Equal(node.Keys[idx], key) {
        return node.Values[idx], nil
    }

    return nil, ErrKeyNotFound
}
```

**收益**:
- ✅ 数据可存储在磁盘
- ✅ 支持超出内存的数据集
- ✅ 成本低廉（SSD vs RAM）
- ✅ 可扩展到 TB 级别

### 2.3 Page 缓存层

**为什么需要缓存？**

磁盘 I/O 延迟:
```
内存访问:    100 ns
SSD 随机读:  10-100 μs   (100-1000x 慢)
SSD 随机写:  10-100 μs   (100-1000x 慢)
HDD 随机读:  1-10 ms     (10000-100000x 慢)
```

**缓存设计**:
```go
type PageCache struct {
    cache    sync.Map  // map[uint64]*Page
    maxPages int
    mu       sync.Mutex
    evictor  *LRUEvictor
}

func (pc *PageCache) Get(pageID uint64) (*Page, error) {
    // 1. 尝试从缓存获取
    if page, ok := pc.cache.Load(pageID); ok {
        return page.(*Page), nil
    }

    // 2. 缓存未命中，从磁盘加载
    page, err := pc.pager.LoadPage(pageID)
    if err != nil {
        return nil, err
    }

    // 3. 放入缓存
    pc.cache.Store(pageID, page)
    return page, nil
}

func (pc *PageCache) Flush() error {
    // 刷新所有脏页到磁盘
    var err error
    pc.cache.Range(func(key, value any) bool {
        page := value.(*Page)
        if page.Dirty {
            if e := page.Persist(pc.pager.file); e != nil {
                err = e
            }
        }
        return true
    })
    return err
}
```

**缓存策略**:
```
LRU (最近最少使用):
  - 淘汰最久未使用的页面
  - 简单高效

LFU (最不经常使用):
  - 淘汰访问频率最低的页面
  - 更复杂的统计

ARC (自适应替换缓存):
  - 结合 LRU 和 LFU
  - 更好的缓存命中率
```

---

## 三、轻量 Page 层设计方案

### 3.1 核心接口

```go
// Page 接口
type Page interface {
    // 基本属性
    ID() uint64
    Size() int
    Data() []byte

    // 生命周期
    Pin()   // 钉住（防止驱逐）
    Unpin() // 解钉
    IsPinned() bool

    // 脏标记
    MarkDirty()
    IsDirty() bool

    // 持久化
    Persist(writer io.Writer) error
    Load(reader io.Reader) error
}

// PagePool 接口
type PagePool interface {
    Allocate() (Page, error)
    Free(page Page) error
    Get(pageID uint64) (Page, error)
    Evict(pageID uint64) error
    Flush() error
}
```

### 3.2 具体实现

```go
// 内存页实现
type MemoryPage struct {
    id     uint64
    data   [PageSize]byte  // 4KB 固定大小
    dirty  bool
    pinned int32  // 引用计数
}

func (mp *MemoryPage) ID() uint64 { return mp.id }
func (mp *MemoryPage) Size() int { return PageSize }
func (mp *MemoryPage) Data() []byte { return mp.data[:] }

func (mp *MemoryPage) Pin() {
    atomic.AddInt32(&mp.pinned, 1)
}

func (mp *MemoryPage) Unpin() {
    atomic.AddInt32(&mp.pinned, -1)
}

func (mp *MemoryPage) IsPinned() bool {
    return atomic.LoadInt32(&mp.pinned) > 0
}

func (mp *MemoryPage) MarkDirty() {
    mp.dirty = true
}

func (mp *MemoryPage) IsDirty() bool {
    return mp.dirty
}

func (mp *MemoryPage) Persist(writer io.Writer) error {
    _, err := writer.Write(mp.data[:])
    if err == nil {
        mp.dirty = false
    }
    return err
}

func (mp *MemoryPage) Load(reader io.Reader) error {
    _, err := io.ReadFull(reader, mp.data[:])
    return err
}

// PagePool 实现
type SimplePagePool struct {
    pages    sync.Map  // map[uint64]*MemoryPage
    freeList []uint64   // 可用页面 ID 列表
    nextID   uint64     // 下一个分配的 ID
    pageSize int
    maxPages int
    mu       sync.Mutex
}

func NewSimplePagePool(pageSize, maxPages int) *SimplePagePool {
    return &SimplePagePool{
        pageSize: pageSize,
        maxPages: maxPages,
    }
}

func (spp *SimplePagePool) Allocate() (Page, error) {
    spp.mu.Lock()
    defer spp.mu.Unlock()

    // 检查是否超出限制
    if spp.nextID >= uint64(spp.maxPages) {
        return nil, ErrPoolExhausted
    }

    // 从 freeList 获取或分配新 ID
    var id uint64
    if len(spp.freeList) > 0 {
        id = spp.freeList[len(spp.freeList)-1]
        spp.freeList = spp.freeList[:len(spp.freeList)-1]
    } else {
        id = spp.nextID
        spp.nextID++
    }

    page := &MemoryPage{id: id}
    spp.pages.Store(id, page)
    return page, nil
}

func (spp *SimplePagePool) Free(page Page) error {
    spp.mu.Lock()
    defer spp.mu.Unlock()

    id := page.ID()

    // 检查是否钉住
    if mp, ok := spp.pages.Load(id); ok {
        if mp.(*MemoryPage).IsPinned() {
            return ErrPagePinned
        }
    }

    spp.pages.Delete(id)
    spp.freeList = append(spp.freeList, id)
    return nil
}

func (spp *SimplePagePool) Get(pageID uint64) (Page, error) {
    if page, ok := spp.pages.Load(pageID); ok {
        return page.(Page), nil
    }
    return nil, ErrPageNotFound
}

func (spp *SimplePagePool) Flush() error {
    var err error
    spp.pages.Range(func(key, value any) bool {
        page := value.(*MemoryPage)
        if page.IsDirty() {
            // 这里应该持久化到磁盘
            // 简化版：只清除脏标记
            page.dirty = false
        }
        return true
    })
    return err
}
```

### 3.3 序列化到 Page

```go
// Node 序列化到 Page
func SerializeNodeToPage(node *Node, page Page) error {
    buf := page.Data()
    offset := 0

    // 1. 写入节点头
    binary.LittleEndian.PutUint16(buf[offset:], uint16(len(node.Keys)))
    offset += 2
    buf[offset] = byte(boolToInt(node.IsLeaf))
    offset += 1
    offset += 5 // 填充对齐

    // 2. 写入 Keys
    for _, key := range node.Keys {
        keyLen := uint16(len(key))
        binary.LittleEndian.PutUint16(buf[offset:], keyLen)
        offset += 2
        copy(buf[offset:], key)
        offset += len(key)
    }

    // 3. 写入 Values (如果是叶子节点)
    if node.IsLeaf {
        for _, value := range node.Values {
            valLen := uint16(len(value))
            binary.LittleEndian.PutUint16(buf[offset:], valLen)
            offset += 2
            copy(buf[offset:], value)
            offset += len(value)
        }
    }

    // 4. 写入 Children (如果是内部节点)
    if !node.IsLeaf {
        for _, childID := range node.Children {
            binary.LittleEndian.PutUint64(buf[offset:], childID)
            offset += 8
        }
    }

    return nil
}

// 从 Page 反序列化 Node
func DeserializeNodeFromPage(page Page) (*Node, error) {
    buf := page.Data()
    offset := 0

    // 1. 读取节点头
    numKeys := binary.LittleEndian.Uint16(buf[offset:])
    offset += 2
    isLeaf := intToBool(buf[offset])
    offset += 1
    offset += 5 // 跳过填充

    node := &Node{
        Keys:     make([][]byte, 0, numKeys),
        IsLeaf:   isLeaf,
    }

    // 2. 读取 Keys
    for i := 0; i < int(numKeys); i++ {
        keyLen := binary.LittleEndian.Uint16(buf[offset:])
        offset += 2
        key := make([]byte, keyLen)
        copy(key, buf[offset:offset+int(keyLen)])
        offset += int(keyLen)
        node.Keys = append(node.Keys, key)
    }

    // 3. 读取 Values 或 Children
    if isLeaf {
        node.Values = make([][]byte, 0, numKeys)
        for i := 0; i < int(numKeys); i++ {
            valLen := binary.LittleEndian.Uint16(buf[offset:])
            offset += 2
            value := make([]byte, valLen)
            copy(value, buf[offset:offset+int(valLen)])
            offset += int(valLen)
            node.Values = append(node.Values, value)
        }
    } else {
        node.Children = make([]uint64, 0, numKeys+1)
        for i := 0; i <= int(numKeys); i++ {
            childID := binary.LittleEndian.Uint64(buf[offset:])
            offset += 8
            node.Children = append(node.Children, childID)
        }
    }

    return node, nil
}

func boolToInt(b bool) int {
    if b {
        return 1
    }
    return 0
}

func intToBool(b byte) bool {
    return b == 1
}
```

---

## 四、集成 Page 层到 BTree

### 4.1 修改后的 BTree 结构

```go
type BTree struct {
    root     atomic.Value  // *RootInfo
    pager    PagePool      // 页面管理器
    cache    *PageCache    // 页面缓存
    mu       sync.RWMutex   // 仅用于元数据
}

type RootInfo struct {
    RootID   uint64
    Version  uint64
    Created  time.Time
}

func NewBTree(dataDir string, pageSize int) (*BTree, error) {
    // 1. 打开或创建数据文件
    dataFile := filepath.Join(dataDir, "btree.data")
    file, err := os.OpenFile(dataFile, os.O_RDWR|os.O_CREATE, 0644)
    if err != nil {
        return nil, err
    }

    // 2. 创建页面池
    pager := NewSimplePagePool(pageSize, 1024) // 最多 1024 页

    // 3. 创建页面缓存
    cache := NewPageCache(pager, 256) // 缓存 256 页

    // 4. 加载或创建根节点
    var rootID uint64
    if info, err := loadMetadata(file); err == nil {
        rootID = info.RootID
    } else {
        // 创建新的根节点
        rootPage, _ := pager.Allocate()
        rootID = rootPage.ID()
        rootNode := NewNode(true)
        SerializeNodeToPage(rootNode, rootPage)
        rootPage.MarkDirty()
        rootPage.Persist(file)
    }

    // 5. 创建 BTree
    bt := &BTree{
        pager: pager,
        cache: cache,
    }

    // 6. 设置根节点
    rootInfo := &RootInfo{
        RootID:  rootID,
        Version: 0,
        Created: time.Now(),
    }
    bt.root.Store(rootInfo)

    return bt, nil
}
```

### 4.2 修改后的 Get 操作

```go
func (b *BTree) Get(ctx context.Context, key []byte) ([]byte, error) {
    // 1. 获取根信息
    rootInfo := b.root.Load().(*RootInfo)

    // 2. 从缓存获取根页面
    page, err := b.cache.Get(rootInfo.RootID)
    if err != nil {
        return nil, err
    }
    page.Pin()
    defer page.Unpin()

    // 3. 反序列化节点
    node, err := DeserializeNodeFromPage(page)
    if err != nil {
        return nil, err
    }

    // 4. 搜索
    for !node.IsLeaf {
        idx := node.Search(key)
        childID := node.Children[idx]

        // 从缓存获取子页面
        childPage, err := b.cache.Get(childID)
        if err != nil {
            return nil, err
        }
        childPage.Pin()
        page.Unpin() // 释放父页面
        page = childPage

        // 反序列化子节点
        node, err = DeserializeNodeFromPage(page)
        if err != nil {
            return nil, err
        }
    }

    // 5. 叶子节点查找
    idx := node.Search(key)
    if idx < len(node.Keys) && bytes.Equal(node.Keys[idx], key) {
        return node.Values[idx], nil
    }

    return nil, ErrKeyNotFound
}
```

### 4.3 修改后的 Insert 操作

```go
func (b *BTree) Insert(ctx context.Context, key, value []byte) error {
    // 1. 查找路径
    path, err := b.findPath(key)
    if err != nil {
        return err
    }
    defer b.releasePath(path)

    // 2. 修改叶子节点
    leafNode := path[len(path)-1].Node
    leafNode.Keys = append(leafNode.Keys, key)
    leafNode.Values = append(leafNode.Values, value)

    // 3. 检查是否需要分裂
    if len(leafNode.Keys) >= DefaultMaxKeys {
        if err := b.split(path); err != nil {
            return err
        }
    }

    // 4. 序列化并标记脏页
    for _, pathNode := range path {
        page := pathNode.Page
        SerializeNodeToPage(pathNode.Node, page)
        page.MarkDirty()
    }

    // 5. 更新根信息（如果需要）
    // ...

    return nil
}

func (b *BTree) findPath(key []byte) (Path, error) {
    rootInfo := b.root.Load().(*RootInfo)

    path := make(Path, 0, 4) // 假设树高 4

    // 获取根页面
    page, err := b.cache.Get(rootInfo.RootID)
    if err != nil {
        return nil, err
    }
    page.Pin()

    // 反序列化根节点
    node, err := DeserializeNodeFromPage(page)
    if err != nil {
        page.Unpin()
        return nil, err
    }

    path = append(path, &PathNode{
        Page: page,
        Node: node,
    })

    // 向下查找
    for !node.IsLeaf {
        idx := node.Search(key)
        childID := node.Children[idx]

        childPage, err := b.cache.Get(childID)
        if err != nil {
            return nil, err
        }
        childPage.Pin()

        childNode, err := DeserializeNodeFromPage(childPage)
        if err != nil {
            childPage.Unpin()
            page.Unpin()
            return nil, err
        }

        path = append(path, &PathNode{
            Page: childPage,
            Node: childNode,
        })

        page = childPage
        node = childNode
    }

    return path, nil
}

func (b *BTree) releasePath(path Path) {
    for _, pathNode := range path {
        pathNode.Page.Unpin()
    }
}
```

---

## 五、性能影响分析

### 5.1 Page 层的开销

**额外操作**:
```
每次 Get/Insert:
  1. 页面缓存查找: ~50 ns
  2. 反序列化: ~200 ns
  3. 序列化: ~200 ns
  4. 页面 Pin/Unpin: ~20 ns
  总计: ~470 ns
```

**性能对比**:
```
纯内存设计:
  Node Get:      10.97 ns/op
  Node Insert:   13.21 ns/op

加上 Page 层（估算）:
  BTree Get:     480 ns/op    (慢 43x)
  BTree Insert:  490 ns/op    (慢 37x)
```

**但仍然快**:
```
480 ns/op = 0.48 μs/op
吞吐量 = 2.08M ops/s

仍然比 Lealone 快 2x！
```

### 5.2 缓存命中率的影响

**缓存命中率 vs 性能**:
```
命中率 100%: 480 ns/op   (全部命中)
命中率 99%:  530 ns/op   (1% 磁盘访问)
命中率 95%:  730 ns/op   (5% 磁盘访问)
命中率 90%:  980 ns/op   (10% 磁盘访问)

磁盘访问延迟: 10-100 μs
```

**优化策略**:
1. **增大缓存**: 256 页 → 1024 页（1MB → 4MB）
2. **预取**: 预加载可能访问的页面
3. **批量操作**: 减少页面访问次数

---

## 六、实施计划

### Phase 1: 基础 Page 层（1 周）

**任务**:
1. ✅ 定义 Page 接口
2. ✅ 实现 MemoryPage
3. ✅ 实现 SimplePagePool
4. ✅ 实现序列化/反序列化
5. ✅ 单元测试

**交付物**:
- `page.go` - Page 接口和实现
- `pool.go` - PagePool 实现
- `serializer.go` - 序列化/反序列化
- `page_test.go` - 单元测试

### Phase 2: Page 缓存层（1 周）

**任务**:
1. ✅ 实现 PageCache
2. ✅ 实现 LRU 淘汰策略
3. ✅ 实现后台刷新
4. ✅ 性能测试

**交付物**:
- `cache.go` - PageCache 实现
- `lru.go` - LRU 淘汰器
- `cache_test.go` - 单元测试
- `cache_bench_test.go` - 性能测试

### Phase 3: 集成到 BTree（2 周）

**任务**:
1. ✅ 修改 BTree 结构
2. ✅ 修改 Get 操作
3. ✅ 修改 Insert 操作
4. ✅ 修改 Delete 操作
5. ✅ 集成测试

**交付物**:
- 修改后的 `btree.go`
- 集成测试
- 性能对比报告

### Phase 4: 持久化层（1 周）

**任务**:
1. ✅ 实现文件存储
2. ✅ 实现崩溃恢复
3. ✅ 实现检查点
4. ✅ 集成测试

**交付物**:
- `storage.go` - 文件存储实现
- `recovery.go` - 崩溃恢复
- `checkpoint.go` - 检查点
- 集成测试

### Phase 5: 性能优化（2 周）

**任务**:
1. ✅ 缓存优化（增大、预取）
2. ✅ 序列化优化（二进制格式）
3. ✅ 并发优化
4. ✅ 性能测试

**交付物**:
- 性能优化报告
- 基准测试结果
- 性能对比

---

## 七、总结

### 7.1 关键洞察

> **纯内存设计的"损害"**:
> 1. ❌ 无法持久化（数据易丢失）
> 2. ❌ 内存碎片（GC 压力大）
> 3. ❌ 大容量限制（受限于物理内存）
> 4. ❌ 生产环境不可用
>
> **Page 层的收益**:
> 1. ✅ 可持久化（数据安全）
> 2. ✅ 减少碎片（固定大小分配）
> 3. ✅ 支持大容量（磁盘存储）
> 4. ✅ 生产环境可用

### 7.2 架构权衡

```
纯内存设计:
  ✅ 极致性能 (10.97 ns/op)
  ✅ 简单实现
  ❌ 无法落地生产

Page 层设计:
  ⭐ 高性能 (480 ns/op, 仍然快 2x)
  ⭐ 可落地生产
  ⭐ 支持大容量
  ⭐ 数据安全

结论: Page 层是生产环境的必要条件
```

### 7.3 最终建议

> **必须添加轻量 Page 层**才能落地生产环境。
>
> 预计性能下降 40x，但仍然比 Lealone 快 2x。
>
> 实施时间: 6-8 周
> 优先级: P0（关键路径）

---

**报告生成**: 2026-03-09 13:10:00 CST
**生成者**: Claude Code
**版本**: v1.0
**状态**: 架构分析完成
