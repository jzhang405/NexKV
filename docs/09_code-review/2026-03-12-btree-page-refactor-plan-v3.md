# BTree Page 重构方案 - Lealone AOSE 架构迁移

> **版本**: v3.0 (根据第二轮审核意见修订)
> **审核日期**: 2026-03-12
> **预计工期**: 16 周

## 上下文

### 为什么需要重构

当前 NexKV BTree 实现基于混合架构（Node + Page），存在以下问题：

1. **内存冗余**：Node 同时维护内存指针（`Children []*Node`）和持久化引用（`ChildIDs []PageID`），占用双倍内存
2. **锁竞争**：使用 `sync.RWMutex`，读写互斥影响并发性能
3. **写入放大**：每次修改都需要完整序列化整个 4KB 页面
4. **缓存复杂**：三级缓存（L1/L2/NodeL1）管理复杂，淘汰策略不够精细
5. **可扩展性限制**：直接指针架构限制了数据规模上限（<100GB）

### Lealone AOSE 设计优势

根据 `thoughts/` 目录的设计文档分析：

| 特性 | NexKV 当前 | Lealone AOSE |
|------|-----------|--------------|
| 存储架构 | 覆盖写入 | Append-Only |
| 页面管理 | 混合 Node/Page | 纯 PageReference |
| 并发控制 | RWMutex | PageLock (CAS) |
| 持久化 | 同步写入 | 异步批量写入 |
| 内存优化 | 全页复制 | 仅复制 Leaf Page |
| **数据规模** | <100GB | **>1TB** |

**性能对比**（来自设计文档）：
- 写延迟：1.78µs (AO) vs 5.04µs (WAL)
- fsync 频率：降低 1000x
- 内存复制：2KB (Leaf) vs 32KB (Full)

## 重构目标

1. **纯 Page-based 架构**：移除 Node 类型，使用 PageReference → PageInfo → Page 三层
2. **Append-Only 存储**：Chunk-based 文件管理，替代覆盖写入
3. **异步持久化**：BTreeGC 后台收集脏页并批量写入
4. **轻量级锁**：PageLock (CAS) 替代部分 RWMutex
5. **TB/PB 级支持**：通过间接寻址突破直接指针的内存限制

## 实现方案

### 1. 核心数据结构重构

#### 1.1 PageReference（间接寻址）

```go
// PageReference 实现间接寻址，类似 Lealone 的设计
// 要求：Go 1.19+ (atomic.Pointer 泛型支持)
type PageReference struct {
    pInfo     atomic.Pointer[PageInfo]  // 原子指针，支持 CAS 更新
    parentRef *PageReference             // 父引用，形成引用链（弱引用避免循环）
}

// 关键方法
func (r *PageReference) GetOrReadPage() (*Page, error)
func (r *PageReference) ReplacePage(oldInfo, newInfo *PageInfo) bool  // CAS
func (r *PageReference) MarkDirty() error
```

**注意**：需要 Go 1.19+ 支持 `atomic.Pointer[T]` 泛型。

#### 1.2 PageInfo（内存管理）

```go
// PageInfo 管理页面状态和缓存
// Cache Line 对齐优化（64 bytes）以减少 false sharing
const cacheLineSize = 64

type PageInfo struct {
    // 热数据区域（第 1 个 cache line）- 频繁访问
    pos         int64       // 8 bytes - 在 Chunk 中的位置（0=未写入）
    page        *Page       // 8 bytes - Page 对象（可能为 nil）
    pageLock    *PageLock   // 8 bytes - 轻量级锁
    lastTime    int64       // 8 bytes - LRU 时间戳（纳秒）
    hits        int64       // 8 bytes - 访问计数
    // 总计 40 bytes

    // buff 单独放（24 bytes slice header）
    buff        []byte      // 24 bytes - 序列化缓冲区

    // 元数据区域（第 2 个 cache line）
    isDirty     bool        // 1 byte
    isSplitted  bool        // 1 byte
    metaVersion int         // 4 bytes
    pageSize    int32       // 4 bytes
    _           [cacheLineSize - 10]byte  // padding 到 64 bytes
}

// Copy-on-Write
func (info *PageInfo) Clone() *PageInfo
```

**v3.0 变更**：
- 删除了 `refCount` 字段（atomic.Pointer 已保证不共享引用）
- **新增 cache line 对齐**：将热数据放在第 1 个 cache line，减少 false sharing

**性能优化说明**：
- `pos`、`page`、`pageLock`、`lastTime`、`hits` 是高频访问字段，放在同一个 cache line
- `buff` 单独放置（slice header 24 bytes）
- 元数据（isDirty、isSplitted 等）放在第 2 个 cache line，避免干扰热数据访问

#### 1.3 RootPageReference（特殊处理）✨ 新增

```go
// RootPageReference Root 页面的特殊引用
// 处理 Root Page 的 CAS 更新和引用链维护
type RootPageReference struct {
    PageReference
}

// ReplacePage 替换 Root Page，原子更新并维护引用链
func (r *RootPageReference) ReplacePage(oldInfo, newInfo *PageInfo) bool {
    // 1. 更新所有子节点的 parentRef（指向新 Root）
    if newInfo.page != nil {
        r.updateChildrenParentRef(newInfo.page)
    }

    // 2. CAS 更新 pInfo
    swapped := r.pInfo.CompareAndSwap(oldInfo, newInfo)
    if !swapped {
        return false
    }

    // 3. 旧页面延迟释放（等待活跃读操作完成）
    if oldInfo != nil {
        r.scheduleDelayedRelease(oldInfo)
    }

    return true
}

// updateChildrenParentRef 递归更新子节点的父引用
func (r *RootPageReference) updateChildrenParentRef(page *Page) {
    // 遍历页面树，更新所有子节点的 parentRef
    // 确保引用链完整性
}

// scheduleDelayedRelease 延迟释放旧页面
func (r *RootPageReference) scheduleDelayedRelease(info *PageInfo) {
    // 等待读操作完成后释放，避免 use-after-free
    go func() {
        time.Sleep(100 * time.Millisecond)  // 等待活跃读操作完成
        // 释放 PageInfo
    }()
}
```

**关键设计**：确保并发读写场景下的引用链完整性和内存安全。

#### 1.4 Page 类型分离

```go
// LeafPage 叶子节点
type LeafPage struct {
    pageID   model.PageID
    version  uint64
    keys     [][]byte
    values   [][]byte
}

// InternalPage 内部节点
type InternalPage struct {
    pageID   model.PageID
    version  uint64
    keys     [][]byte
    children []*PageReference  // 子节点引用
}
```

### 2. Chunk Manager（Append-Only 存储）

#### 2.1 位置编码（64 位）✨ 修改

```go
// 64 位位置编码（Lealone 方案）
// ┌────────────────────────────────────────────────────────────────┐
// │  63-38 (26 bits) │ 37-6 (32 bits) │ 5-1 (5 bits) │ 0 (1 bit)  │
// │    Chunk ID      │     Offset     │   Page Type  │  保留位    │
// └────────────────────────────────────────────────────────────────┘

const (
    PageTypeLeaf     = 1  // 叶子节点
    PageTypeInternal = 2  // 内部节点
    PageTypeRoot     = 3  // 根节点
)

// EncodePagePos 编码页面位置
func EncodePagePos(chunkID, offset, pageType int) int64 {
    return (int64(chunkID) << 38) | (int64(offset) << 6) | (int64(pageType) << 1)
}

// DecodePagePos 解码页面位置
func DecodePagePos(pos int64) (chunkID, offset, pageType int) {
    chunkID = int(pos >> 38)
    offset = int((pos >> 6) & 0xFFFFFFFF)
    pageType = int((pos >> 1) & 0x1F)
    return
}
```

**v3.0 变更**：采用 Lealone 的 64 位编码方案，替代 struct 方案。

#### 2.2 ChunkManager 结构

```go
// ChunkManager 管理 Append-Only 文件
type ChunkManager struct {
    // 基础配置
    chunkSize       int64        // 每个 Chunk 大小 (256MB)
    maxChunks       int          // 最大文件数量 (8个)
    pageSize        int          // 页面大小 (4KB)
    dataDir         string       // 数据目录

    // 文件管理
    activeChunks    []*Chunk     // 活跃的 Chunk
    archivedChunks  []*Chunk     // 已归档的 Chunk
    currentChunk    *Chunk       // 当前写入的 Chunk
    currentChunkIdx int

    // 空间管理
    freePages       []int64      // 空闲页面位置（重用）
    totalSize       atomic.Int64 // 总大小

    // 压缩器
    compactor       *ChunkCompactor

    // 内存池
    pagePool        sync.Pool    // *[]byte
    bufferPool      sync.Pool    // *PageInfo
}

// Chunk 文件命名：btree_0000.ao, btree_0001.ao, ...
type Chunk struct {
    id          int
    file        *os.File
    writePos    int64
    pageCount   int
    pageLengths map[int64]int  // pos → length
    isReadOnly  bool
}

// 分配新页面（追加写入）
func (cm *ChunkManager) AllocatePage(size int, pageType int) (int64, error)

// 批量写入脏页
func (cm *ChunkManager) WritePages(pages map[int64][]byte) error

// Chunk 压缩和空间回收
func (cm *ChunkManager) CompactChunk(chunkID int) error
```

### 3. Split 引用更新机制 ✨ 新增

```go
// SplitWithConcurrencyControl 支持并发安全的页面分裂
func (p *InternalPage) SplitWithConcurrencyControl() (*InternalPage, []byte, error) {
    // 1. 创建新的 InternalPage
    newPage := &InternalPage{
        pageID:   allocatePageID(),
        keys:     p.keys[mid+1:],
        children: make([]*PageReference, len(p.children[mid+1:])),
    }

    // 2. 复制 PageReference（共享引用）
    copy(newPage.children, p.children[mid+1:])

    // 3. 更新子节点的 parentRef（指向新父节点）
    for _, childRef := range newPage.children {
        if childRef != nil {
            childRef.SetParentRef(newPageRef)
        }
    }

    // 4. 修改当前页面
    p.keys = p.keys[:mid]
    p.children = p.children[:mid+1]

    return newPage, medianKey, nil
}

// SetParentRef 更新父引用（线程安全）
func (r *PageReference) SetParentRef(parent *PageReference) {
    // 使用 atomic 操作更新
    for {
        oldInfo := r.pInfo.Load()
        newInfo := oldInfo.Clone()
        newInfo.parentRef = parent

        if r.pInfo.CompareAndSwap(oldInfo, newInfo) {
            break
        }
    }
}
```

**关键设计**：确保 Split 过程中的引用链完整性，避免并发访问错误。

### 4. 脏页写入顺序 ✨ 新增

```go
// WriteDirtyPagesBottomUp 自底向上写入脏页
func (gc *BTreeGC) WriteDirtyPagesBottomUp(dirtyPages map[*PageInfo]bool) error {
    // 1. 按深度排序（叶子节点优先）
    sortedPages := gc.sortPagesByDepth(dirtyPages)

    // 2. 自底向上写入
    for _, pageInfo := range sortedPages {
        if !pageInfo.isDirty {
            continue
        }

        // 2.1 序列化页面
        data, err := serializePage(pageInfo.page)
        if err != nil {
            return err
        }

        // 2.2 写入 Chunk，获取位置
        pos, err := gc.chunkManager.WritePage(data)
        if err != nil {
            return err
        }

        // 2.3 更新页面的 pos
        pageInfo.pos = pos

        // 2.4 如果是内部节点，更新父节点的 children 引用
        if !pageInfo.page.IsLeaf() {
            gc.updateParentReference(pageInfo)
        }

        // 2.5 清除脏页标记
        pageInfo.isDirty = false
    }

    // 3. 最后写入 Root Page
    if rootInfo := dirtyPages[gc.rootInfo]; rootInfo != nil {
        return gc.writeRootPage(rootInfo)
    }

    return nil
}

// updateParentReference 更新父节点的引用
func (gc *BTreeGC) updateParentReference(childInfo *PageInfo) {
    // 查找父节点并更新其 children 引用
    // 使用 CAS 操作确保并发安全
}
```

**关键设计**：必须自底向上写入，确保子节点的位置信息已写入后，再更新父节点。

### 5. BTreeGC（渐进式垃圾回收）

```go
// BTreeGC 渐进式垃圾回收
type BTreeGC struct {
    btree    *BTree

    // 内存管理
    maxMemory     int64  // 内存上限 (64MB)
    lowWaterMark  int64  // 低水位 (70% = 44.8MB)
    highWaterMark int64  // 高水位 (90% = 57.6MB)
    usedMemory    atomic.Int64

    // 分层 GC 策略
    pageEvictionRate   float64  // 页面淘汰率 (0.1)
    bufferEvictionRate float64  // 缓冲区淘汰率 (0.3)

    // 智能触发
    memoryPressure     chan bool         // 内存压力信号
    adaptiveInterval   atomic.Duration  // 自适应间隔 (1s-5min)
    stopCh             chan struct{}

    // 统计
    stats              GCStats
}

// GC 类型
const (
    GCTypeFull   = 0  // 完全释放（page + buff）
    GCTypePage   = 1  // 仅释放 page 对象
    GCTypeBuff   = 2  // 仅释放 buff 缓存
)

func (gc *BTreeGC) Start()
func (gc *BTreeGC) collectDirtyPages() error
func (gc *BTreeGC) releasePages(gcType int)
func (gc *BTreeGC) shouldGC() bool  // 智能判断
```

### 6. PageLock（轻量级锁）

```go
// PageLock 支持重入和超时的轻量级锁
type PageLock struct {
    state   atomic.Int64      // 状态编码：(owner_id << 32) | lock_count
    waiters chan struct{}     // 等待队列
    mu      sync.Mutex        // 保护 waiters
}

const (
    unlockedState     = 0
    maxRecurseCount   = 1000  // 最大重入次数
    defaultLockTimeout = 5 * time.Second
)

// 非阻塞加锁
func (l *PageLock) TryLock() bool

// 带超时的加锁
func (l *PageLock) LockWithTimeout(timeout time.Duration) bool

// 解锁（支持重入）
func (l *PageLock) Unlock() error

// 检查是否已锁定
func (l *PageLock) IsLocked() bool
```

### 7. 序列化优化

```go
// FixedLayoutSerializer 固定布局序列化
type FixedLayoutSerializer struct {
    pageSize     int           // 固定页面大小 (4KB)
    version      int           // 版本号 (兼容性)

    // 内存池
    bufferPool   sync.Pool     // *[]byte
    offsetPool   sync.Pool     // []int32

    // 变长数据支持
    maxKeySize   uint16        // 最大键长度 (64KB)
    maxValueSize uint16        // 最大值长度 (16KB)
}

// 页面布局
// +------------------+
// | PageType (1B)    |
// | Version (2B)     |
// | NumKeys (4B)     |
// | Flags (1B)       |
// | Reserved (8B)    |
// | Keys Section     |
// | Values/Children  |
// +------------------+

func (s *FixedLayoutSerializer) Serialize(page Page) ([]byte, error)
func (s *FixedLayoutSerializer) Deserialize(data []byte) (Page, error)
func (s *FixedLayoutSerializer) GetEstimatedSize(page Page) int
```

### 8. 数据迁移方案

```go
// DataMigrator 数据迁移工具
type DataMigrator struct {
    oldDBPath string
    newMgr     *ChunkManager
}

// 从旧格式迁移到新格式
func (m *DataMigrator) Migrate(progressCb func(int, int)) error

// 验证迁移完整性
func (m *DataMigrator) Verify() error

// 回滚迁移
func (m *DataMigrator) Rollback() error
```

### 9. Cache Line 对齐优化 ✨ 新增

```go
// Cache Line 常量（大多数 CPU 架构为 64 bytes）
const cacheLineSize = 64
```

#### 9.1 为什么需要 Cache Line 对齐

**False Sharing 问题**：
- 当多个 CPU 核心同时访问同一 cache line 的不同变量时
- 会导致 cache line 在核心间频繁传输
- 即使访问的是不同的变量，也会相互影响性能

**PageInfo 的访问模式**：
- 高并发读取：`pos`、`page`、`lastTime`、`hits`
- 低频写入：`isDirty`、`metaVersion`
- 如果不分离，读写会相互干扰

#### 9.2 PageInfo 对齐策略

```go
// PageInfo 分为两个 cache line

// 第 1 个 cache line - 热数据（读多写少）
type PageInfoHotData struct {
    pos         int64       // 8 bytes
    page        *Page       // 8 bytes
    pageLock    *PageLock   // 8 bytes
    lastTime    int64       // 8 bytes
    hits        int64       // 8 bytes
    // 总计 40 bytes
}

// 第 2 个 cache line - 元数据（写多读少）
type PageInfoMetadata struct {
    isDirty     bool        // 1 byte
    isSplitted  bool        // 1 byte
    metaVersion int         // 4 bytes
    pageSize    int32       // 4 bytes
    _           [cacheLineSize - 10]byte  // padding
}

// PageInfo 完整结构
type PageInfo struct {
    hotData  PageInfoHotData     // 40 bytes + 24 bytes (buff) ≈ 64 bytes
    buff     []byte              // 24 bytes (slice header)
    metadata PageInfoMetadata    // 64 bytes (对齐)
}
```

#### 9.3 PageReference 对齐策略（可选）

```go
// PageReference 可选的读写分离（延后决定）
type PageReference struct {
    // 读热数据
    pInfo atomic.Pointer[PageInfo]  // 8 bytes
    _     [cacheLineSize - 8]byte   // padding to 64 bytes

    // 写热数据（独立 cache line，避免干扰读）
    parentRef *PageReference         // 8 bytes
    _         [cacheLineSize - 8]byte // padding
}
```

**说明**：PageReference 的分离需要通过性能测试验证是否必要。

#### 9.4 对齐优先级

| 结构 | 是否对齐 | 优先级 | 理由 |
|-----|---------|--------|------|
| PageInfo | ✅ 是 | 高 | 每个页面都有，高并发访问热点 |
| PageReference | ⏳ 测试后决定 | 中 | 通过性能测试确认是否需要分离 |
| PageLock | ❌ 否 | 低 | 访问频率相对较低 |
| Node | ❌ 否 | 低 | 非热点路径 |
| BTree | ❌ 否 | 低 | 全局单例 |

#### 9.5 验证和测试

```go
// 验证对齐是否生效
func verifyPageInfoAlignment() {
    var info PageInfo
    offset1 := unsafe.Offsetof(info.hotData.pos)
    offset2 := unsafe.Offsetof(info.metadata)

    // 检查 hotData 是否在 cache line 边界
    if offset1%cacheLineSize != 0 {
        log.Printf("Warning: PageInfo.hotData not aligned to cache line")
    }

    // 检查 metadata 是否在独立 cache line
    if (offset2 - offset1)%cacheLineSize != 0 {
        log.Printf("Warning: PageInfo.metadata not in separate cache line")
    }
}

// 性能测试对比
func BenchmarkPageInfo_WithAlignment(b *testing.B) {
    // 测试对齐版本的性能
}

func BenchmarkPageInfo_WithoutAlignment(b *testing.B) {
    // 测试未对齐版本的性能
}
```

## 迁移步骤（16 周）

### Phase 0.5: 原型验证（Week 1）✨ 新增

**目标**：验证 atomic.Pointer 性能是否满足要求

- [ ] 实现 PageReference 原型（简化的 CAS 更新）
- [ ] 性能基准测试：对比 atomic.Pointer vs 直接指针
- [ ] 并发测试：1000 goroutines 并发访问
- [ ] 决策：如果性能 <1μs 可行，继续；否则考虑备选方案

**成功标准**：
- 读延迟 <1μs（L1 缓存命中）
- 无 race condition
- 无明显性能回退

### Phase 1: 基础设施（Week 2-3）

**Week 2**：PageReference 和 PageInfo
- [ ] 实现 `PageReference` 结构（CAS 更新，Go 1.19+）
- [ ] 实现 `PageInfo` 结构（Copy-on-Write，无 refCount）
- [ ] 实现 `RootPageReference`（特殊处理）✨
- [ ] 单元测试 + 并发测试

**Week 3**：Chunk Manager
- [ ] 实现 `Chunk` 结构（Write/Read/Sync）
- [ ] 实现 `ChunkManager`（AllocatePage/WritePage）
- [ ] 实现 64 位位置编码 ✨
- [ ] 实现 `ChunkCompactor` 基础框架
- [ ] 单元测试 + 集成测试

### Phase 2: Page 类型重构（Week 4-7）

**Week 4**：LeafPage 和 InternalPage
- [ ] 实现 `LeafPage`（Insert/Update/Delete/Split）
- [ ] 实现 `InternalPage`（InsertChild/Split）
- [ ] 实现 Split 并发控制 ✨
- [ ] 实现 Page 接口
- [ ] 单元测试 + 功能对比

**Week 5-6**：序列化优化
- [ ] 实现 `FixedLayoutSerializer`
- [ ] 实现变长键值对处理
- [ ] 实现版本兼容性
- [ ] 集成测试（序列化/反序列化往返）

**Week 7**：内存池优化
- [ ] 实现内存池（sync.Pool）
- [ ] 性能测试和调优
- [ ] 内存泄漏检测

### Phase 3: 并发控制（Week 8-10）

**Week 8-9**：PageLock 和 BTreeGC
- [ ] 实现 `PageLock`（支持重入和超时）
- [ ] 实现 `BTreeGC`（水位线机制）
- [ ] 实现自适应触发策略
- [ ] 实现脏页自底向上写入 ✨
- [ ] 单元测试 + 集成测试

**Week 10**：CCOW 机制
- [ ] 实现路径复制算法
- [ ] 实现脏页标记传播
- [ ] 集成测试（并发读写 + 快照隔离）

### Phase 4: 集成和优化（Week 11-15）

**Week 11-12**：数据迁移
- [ ] 实现 `DataMigrator`
- [ ] 实现旧格式读取
- [ ] 实现迁移验证和回滚

**Week 13-14**：BTree 集成
- [ ] 替换 BTree 内部实现
- [ ] 更新 PageCache（简化为两层）
- [ ] 配置切换机制
- [ ] 集成测试（现有测试全部通过）

**Week 15**：长期运行测试 ✨ 新增
- [ ] 24 小时稳定性测试
- [ ] 内存泄漏监控
- [ ] 性能回归测试

### Phase 5: 性能优化和文档（Week 16）

**Week 16**：性能优化和文档
- [ ] 性能优化（目标 <1μs 读延迟）
- [ ] 文档（架构设计、API、迁移指南）
- [ ] 代码审查

## 关键文件

需要修改的核心文件：

1. **`internal/infrastructure/storage/btree/node.go`** (502 行)
   - 当前的 Node 混合实现
   - 需要迁移到 PageReference + LeafPage/InternalPage

2. **`internal/infrastructure/storage/btree/page_cache.go`** (440 行)
   - 三级缓存 → 两层缓存（PageReference + PageInfo）
   - 简化淘汰逻辑

3. **`internal/infrastructure/storage/btree/serialize.go`** (303 行)
   - 优化为固定布局
   - 减少 memory allocation

4. **`internal/infrastructure/storage/btree/page_persist.go`**
   - PageManager → ChunkManager
   - 覆盖写入 → Append-Only

5. **`internal/infrastructure/storage/btree/btree.go`** (435 行)
   - 保持公共 API 兼容
   - 内部逐步切换到新架构

**新增文件**：
- `page_reference.go` - PageReference 和 PageInfo（无 refCount）
- `root_page_reference.go` - RootPageReference 特殊处理 ✨
- `page_lock.go` - 增强型 PageLock
- `chunk_manager.go` - Chunk 管理（64 位编码）
- `chunk_compactor.go` - Chunk 压缩
- `data_migrator.go` - 数据迁移工具

## 测试策略

### 单元测试

```go
// PageReference 并发测试
func TestPageReference_ConcurrentUpdate(t *testing.T) {
    ref := NewPageReference()
    var wg sync.WaitGroup

    // 100 goroutines 并发更新
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            info := &PageInfo{page: &Page{ID: model.PageID(id)}}
            ref.ReplacePage(nil, info)
        }(i)
    }
    wg.Wait()
}
```

### 集成测试

- [ ] 基本功能：Put/Get/Delete/Range Scan/Batch
- [ ] 并发场景：100 goroutines 并发读写
- [ ] 持久化场景：写入 10000 条记录，重启后验证

### 性能测试

```go
func BenchmarkBTree_Write(b *testing.B) {
    tree := setupBTree()
    b.ResetTimer()

    for i := 0; i < b.N; i++ {
        key := []byte(fmt.Sprintf("key-%d", i))
        value := []byte(fmt.Sprintf("value-%d", i))
        tree.Put(key, value)
    }
}
```

**性能目标**（保持激进）：
- 随机读：> 1M ops/sec，延迟 **<1μs** ✨
- 随机写：> 300k ops/sec，延迟 <2μs
- 并发读（100 线程）：> 10M ops/sec

### 新增测试用例

```go
// 故障注入测试
func TestChunkManager_CorruptedPage(t *testing.T) {
    // 模拟部分写入失败
    // 验证错误处理和数据一致性
}

// 内存泄漏测试
func TestBTreeGC_MemoryLeak(t *testing.T) {
    // 长时间运行大量操作
    // 监控内存使用情况
    // 验证 GC 有效性
}

// 压力测试
func TestBTree_HighConcurrency(t *testing.T) {
    // 1000 goroutines 并发访问
    // 验证系统稳定性
}

// 长期运行测试 ✨ 新增
func TestBTree_LongRunning(t *testing.T) {
    // 24 小时持续运行
    // 监控内存、性能、稳定性
}

// 迁移测试
func TestDataMigrator_MigrateAndVerify(t *testing.T) {
    // 验证旧数据完整迁移
    // 验证新旧格式兼容性
}
```

## 风险点和应对

### 高风险 🔴

#### 1. atomic.Pointer 性能可能不满足 <1μs 目标
- **风险**：间接寻址增加开销，可能无法达到激进目标
- **应对**：
  - Phase 0.5 原型验证
  - 如果不满足，考虑混合方案（热数据直接指针，冷数据 PageReference）
  - 备选：使用 `sync.Mutex` + `unsafe.Pointer`
- **测试**：性能基准测试对比

#### 2. 内存占用增加 200-300%
- **风险**：PageReference + PageInfo 约 80 bytes vs 直接指针 8 bytes
- **应对**：
  - 强调核心目标是 TB/PB 支持，而非内存优化
  - 激进的 GC 策略（低水位 70%）
  - 内存池复用减少分配
- **收益**：突破数据规模上限，支持 >1TB 数据

#### 3. 数据一致性
- **风险**：异步持久化可能导致数据丢失
- **应对**：
  - WAL 机制（可配置）
  - 崩溃恢复测试
  - 定期检查点（checkpoint）
- **测试**：增加崩溃恢复测试用例

#### 4. Split/Merge 并发控制复杂度高
- **风险**：引用更新顺序错误可能导致 use-after-free
- **应对**：
  - 详细设计引用更新流程
  - 并发测试覆盖所有场景
  - 延迟释放旧页面
- **测试**：并发安全测试 + 模型检查

### 中风险 🟡

#### 5. Chunk 文件管理复杂度
- **应对**：
  - 固定 Chunk 大小（256MB）
  - 最大文件数量限制（8 个）
  - 自动压缩归档
- **工具**：Chunk 监控和管理工具

#### 6. 序列化兼容性
- **应对**：
  - 版本字段支持
  - 数据迁移工具
  - 新旧格式互转测试
- **文档**：详细的迁移指南

#### 7. 工期风险（16 周）
- **应对**：
  - Phase 0.5 早期验证风险
  - 每个里程碑都有清晰的交付物
  - 定期进度评估和调整
- **缓冲**：预留 2-4 周应急时间

## 预期收益

| 指标 | 当前 | 目标 | 说明 |
|------|------|------|------|
| 读延迟 | ~3μs | **<1μs** | 激进目标，通过优化实现 ✨ |
| 写延迟 | ~5μs | <2μs | 含序列化开销 |
| 内存占用 | 100% | **200-300%** | 增加，但换取 TB/PB 支持 ✨ |
| **支持数据规模** | <100GB | **>1TB** | 核心收益 ✨ |
| 写放大因子 | 10-15x | 1.1-1.5x | Append-Only 优势 |

**v3.0 核心变更**：
- 强调 TB/PB 级别支持是核心目标，而非内存优化
- 保持激进的性能目标（<1μs）
- 内存占用会增加，但这是可扩展性的必要代价

## Go 版本要求

- **最低版本**: Go 1.19（`atomic.Pointer[T]` 泛型支持）
- **推荐版本**: Go 1.21+

## 参考文档

本方案基于以下设计原理和最佳实践：

- **Append-Only Storage**：追加写入存储引擎设计原理
- **BTree 数据结构**：传统 BTree 和 Page-based BTree 的区别
- **Copy-on-Write**：写时复制机制实现并发控制
- **CAS 操作**：Compare-And-Swap 原子操作实现无锁并发
- **LRU 缓存**：最近最少使用缓存淘汰策略
- **Chunk 编码**：64 位位置编码方案（支持 TB/PB 级数据）

## 变更记录

| 版本 | 日期 | 变更内容 |
|------|------|----------|
| v1.0 | 2026-03-12 | 初始版本（8 周） |
| v2.0 | 2026-03-12 | 根据第一轮审核修订（10 周） |
| v3.0 | 2026-03-12 | 根据第二轮审核修订（16 周）：✨ 补充 Root Page CAS、Split 引用更新、脏页写入顺序；✨ 删除 PageInfo.refCount；✨ 采用 64 位位置编码；✨ 调整内存目标为 200-300%，强调 TB/PB 支持；✨ 增加 Phase 0.5 原型验证；✨ 保持 <1μs 激进性能目标 |
| v3.1 | 2026-03-12 | ✨ 新增 Cache Line 对齐优化：PageInfo 热数据对齐到第 1 个 cache line，元数据对齐到第 2 个 cache line；PageReference 分离策略延后通过性能测试决定；使用硬编码 64 bytes |
