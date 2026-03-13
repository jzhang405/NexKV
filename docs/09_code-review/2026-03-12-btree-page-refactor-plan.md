# BTree Page 重构方案 - Lealone AOSE 架构迁移

> **版本**: v2.0 (根据审核意见修订)
> **审核日期**: 2026-03-12
> **预计工期**: 10 周

## 上下文

### 为什么需要重构

当前 NexKV BTree 实现基于混合架构（Node + Page），存在以下问题：

1. **内存冗余**：Node 同时维护内存指针（`Children []*Node`）和持久化引用（`ChildIDs []PageID`），占用双倍内存
2. **锁竞争**：使用 `sync.RWMutex`，读写互斥影响并发性能
3. **写入放大**：每次修改都需要完整序列化整个 4KB 页面
4. **缓存复杂**：三级缓存（L1/L2/NodeL1）管理复杂，淘汰策略不够精细

### Lealone AOSE 设计优势

根据 `thoughts/` 目录的设计文档分析：

| 特性 | NexKV 当前 | Lealone AOSE |
|------|-----------|--------------|
| 存储架构 | 覆盖写入 | Append-Only |
| 页面管理 | 混合 Node/Page | 纯 PageReference |
| 并发控制 | RWMutex | PageLock (CAS) |
| 持久化 | 同步写入 | 异步批量写入 |
| 内存优化 | 全页复制 | 仅复制 Leaf Page |

**性能对比**（来自设计文档）：
- 写延迟：1.78µs (AO) vs 5.04µs (WAL)
- fsync 频率：降低 1000x
- 内存复制：2KB (Leaf) vs 32KB (Full)

## 重构目标

1. **纯 Page-based 架构**：移除 Node 类型，使用 PageReference → PageInfo → Page 三层
2. **Append-Only 存储**：Chunk-based 文件管理，替代覆盖写入
3. **异步持久化**：BTreeGC 后台收集脏页并批量写入
4. **轻量级锁**：PageLock (CAS) 替代部分 RWMutex

## 实现方案

### 1. 核心数据结构重构

#### 1.1 PageReference（间接寻址）

```go
// PageReference 实现间接寻址，类似 Lealone 的 48 字节设计
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
// PageInfo 管理页面状态和缓存，类似 Lealone 的 56 字节设计
type PageInfo struct {
    pos         int64       // 在 Chunk 中的位置（0=未写入）
    page        *Page       // Page 对象（可能为 nil）
    buff        []byte      // 序列化缓冲区
    pageLock    *PageLock   // 轻量级锁（支持重入和超时）
    lastTime    int64       // LRU 时间戳（纳秒）
    hits        int64       // 访问计数
    isDirty     bool        // 是否脏页
    isSplitted  bool        // 是否被分裂
    metaVersion int         // 元数据版本
    refCount    atomic.Int32 // 引用计数
    pageSize    int32       // 页面实际大小（变长支持）
}

// Copy-on-Write
func (info *PageInfo) Clone() *PageInfo
```

#### 1.3 Page 类型分离

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

### 2. Chunk Manager（Append-Only 存储）✨ 已完善

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
    freePages       []PageOffset // 空闲页面位置（重用）
    totalSize       atomic.Int64 // 总大小

    // 压缩器
    compactor       *ChunkCompactor

    // 内存池
    pagePool        sync.Pool    // *[]byte
    bufferPool      sync.Pool    // *PageInfo
}

// PageLocation - 页面位置编码
type PageLocation struct {
    ChunkID  uint32 // Chunk 文件编号 (高 16 位)
    Offset   uint32 // 在 Chunk 中的偏移 (低 16 位，页号)
    Length   uint16 // 页面长度
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
func (cm *ChunkManager) AllocatePage(size int) (PageLocation, error)

// 批量写入脏页
func (cm *ChunkManager) WritePages(pages map[PageLocation][]byte) error

// Chunk 压缩和空间回收
func (cm *ChunkManager) CompactChunk(chunkID int) error
```

**新增功能**：
- ✅ 文件数量控制（maxChunks）
- ✅ 空间回收机制（ChunkCompactor）
- ✅ 空闲页面重用（freePages）
- ✅ 位置编码方案（PageLocation）

### 3. BTreeGC（渐进式垃圾回收）✨ 已改进

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

**改进点**：
- ✅ 水位线机制（低水位 70%，高水位 90%）
- ✅ 自适应触发间隔（根据内存压力动态调整）
- ✅ 分层淘汰策略（page vs buffer 不同淘汰率）

### 4. PageLock（轻量级锁）✨ 已增强

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

**改进点**：
- ✅ 支持重入锁（递归调用）
- ✅ 支持锁超时（避免死锁）
- ✅ 等待队列（公平性）

### 5. 序列化优化（新增）✨

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

**新增特性**：
- ✅ 变长键值对处理（maxKeySize/maxValueSize）
- ✅ 版本兼容性设计
- ✅ 内存池复用

### 6. 数据迁移方案（新增）✨

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

## 迁移步骤（10 周）

### Phase 1: 基础设施（Week 1-2）

**Week 1**：PageReference 和 PageInfo
- [ ] 实现 `PageReference` 结构（CAS 更新，Go 1.19+）
- [ ] 实现 `PageInfo` 结构（Copy-on-Write）
- [ ] 实现 `RootPageReference`（特殊处理）
- [ ] 单元测试 + 并发测试

**Week 2**：Chunk Manager
- [ ] 实现 `Chunk` 结构（Write/Read/Sync）
- [ ] 实现 `ChunkManager`（AllocatePage/WritePage）
- [ ] 实现 `PageLocation` 位置编码
- [ ] 实现 `ChunkCompactor` 基础框架
- [ ] 单元测试 + 集成测试

### Phase 2: Page 类型重构（Week 3-5）

**Week 3**：LeafPage 和 InternalPage
- [ ] 实现 `LeafPage`（Insert/Update/Delete/Split）
- [ ] 实现 `InternalPage`（InsertChild/Split）
- [ ] 实现 Page 接口
- [ ] 单元测试 + 功能对比

**Week 4**：序列化优化
- [ ] 实现 `FixedLayoutSerializer`
- [ ] 实现变长键值对处理
- [ ] 实现版本兼容性
- [ ] 集成测试（序列化/反序列化往返）

**Week 5**：内存池优化
- [ ] 实现内存池（sync.Pool）
- [ ] 性能测试和调优
- [ ] 内存泄漏检测

### Phase 3: 并发控制（Week 6-7）

**Week 6**：PageLock 和 BTreeGC
- [ ] 实现 `PageLock`（支持重入和超时）
- [ ] 实现 `BTreeGC`（水位线机制）
- [ ] 实现自适应触发策略
- [ ] 单元测试 + 集成测试

**Week 7**：CCOW 机制
- [ ] 实现路径复制算法
- [ ] 实现脏页标记传播
- [ ] 集成测试（并发读写 + 快照隔离）

### Phase 4: 集成和优化（Week 8-10）

**Week 8**：数据迁移
- [ ] 实现 `DataMigrator`
- [ ] 实现旧格式读取
- [ ] 实现迁移验证和回滚

**Week 9**：BTree 集成
- [ ] 替换 BTree 内部实现
- [ ] 更新 PageCache（简化为两层）
- [ ] 配置切换机制
- [ ] 集成测试（现有测试全部通过）

**Week 10**：性能优化和文档
- [ ] 性能优化
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
- `page_reference.go` - PageReference 和 PageInfo
- `page_lock.go` - 增强型 PageLock
- `chunk_manager.go` - Chunk 管理
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

**性能目标**：
- 随机读：> 1M ops/sec，延迟 < 1μs
- 随机写：> 300k ops/sec，延迟 < 2μs
- 并发读（100 线程）：> 10M ops/sec（调整后）

### 新增测试用例 ✨

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

// 迁移测试
func TestDataMigrator_MigrateAndVerify(t *testing.T) {
    // 验证旧数据完整迁移
    // 验证新旧格式兼容性
}
```

## 风险点和应对

### 高风险 🔴

#### 1. GC 压力
- **风险**：三层架构可能导致内存使用量翻倍
- **应对**：
  - sync.Pool 复用对象
  - 渐进式 GC（低水位触发）
  - 实时监控内存使用
- **监控**：增加内存使用实时监控

#### 2. 数据一致性
- **风险**：异步持久化可能导致数据丢失
- **应对**：
  - WAL 机制（可配置）
  - 崩溃恢复测试
  - 定期检查点（checkpoint）
- **测试**：增加崩溃恢复测试用例

#### 3. 性能回退
- **风险**：过于复杂的架构可能降低性能
- **应对**：
  - 保留性能基准测试
  - 定期对比重构前后
  - 使用 pprof 识别瓶颈
- **优化**：性能分析工具辅助

### 中风险 🟡

#### 4. Chunk 文件管理复杂度
- **应对**：
  - 固定 Chunk 大小（256MB）
  - 最大文件数量限制（8 个）
  - 自动压缩归档
- **工具**：Chunk 监控和管理工具

#### 5. 序列化兼容性
- **应对**：
  - 版本字段支持
  - 数据迁移工具
  - 新旧格式互转测试
- **文档**：详细的迁移指南

#### 6. API 兼容性
- **应对**：
  - 保持公共接口不变
  - 配置项控制新旧架构切换
  - 充分的集成测试

## 预期收益

| 指标 | 当前 | 目标 | 提升 |
|------|------|------|------|
| 读延迟 | ~3μs | <1μs | 3x |
| 写延迟 | ~5μs | <2μs | 2.5x |
| 内存占用 | 100% | 50-60% | 40-50% ↓ |
| 写放大因子 | 10-15x | 1.1-1.5x | 10x ↓ |

## Go 版本要求

- **最低版本**: Go 1.19（`atomic.Pointer[T]` 泛型支持）
- **推荐版本**: Go 1.21+

## 参考文档

- `thoughts/Lealone-page-based-btree-design.md` - 页面式 BTree 设计
- `thoughts/2026-03-11-lealone-file-design-ao-vs-wal-analysis.md` - AO vs WAL 分析
- `thoughts/2026-03-11-lealone-aose-write-flow-deep-dive.md` - AOSE 写流程
- `thoughts/2026-03-12-btree-page-refactor-review.md` - 方案审核报告
- `thoughts/Lealone/lealone-aose/src/main/java/com/lealone/storage/aose/btree/` - Java 实现参考

## 变更记录

| 版本 | 日期 | 变更内容 |
|------|------|----------|
| v1.0 | 2026-03-12 | 初始版本 |
| v2.0 | 2026-03-12 | 根据审核意见修订：完善 ChunkManager、改进 BTreeGC、增强 PageLock、补充序列化细节、增加数据迁移方案、调整为 10 周计划 |
