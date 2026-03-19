# BTree Page 重构 Phase 2 实施计划

**文档类型**: 技术规划
**创建日期**: 2026-03-13
**负责人**: NexKV Team
**状态**: 📋 讨论稿
**预估工期**: 2-3 周

---

## 📋 目录

1. [Phase 1 回顾](#phase-1-回顾)
2. [Phase 2 目标](#phase-2-目标)
3. [性能优化详解](#性能优化详解)
4. [核心技术架构](#核心技术架构)
5. [实施计划](#实施计划)
6. [验收标准](#验收标准)
7. [风险评估](#风险评估)
8. [资源需求](#资源需求)

---

## Phase 1 回顾

### ✅ 已完成

#### 1. 核心架构重构
- **Page-based 架构**: 完整实现 LeafPage 和 InternalPage
- **并发安全**: PageInfo 使用原子操作（atomic.Uint32, atomic.Int64）
- **引用系统**: PageRef、RootPageRef、Position 完整实现
- **数据管理**: ChunkManager、CCOWManager 基础实现

#### 2. 测试覆盖
- **单元测试**: 20+ 个测试文件，覆盖率 >80%
- **并发测试**: 所有测试通过 race detector
- **集成测试**: E2E、懒加载、Merge API

#### 3. 文档完善
- **性能分析**: CPU/Memory profiling 报告
- **设计文档**: 架构设计、实施记录
- **测试报告**: 覆盖率、性能基准

### ⚠️ 遗留问题

#### 1. 性能瓶颈（P0）
```
Set 操作性能分析:
├── fsync:        39.87% CPU ← 最大瓶颈
├── mallocgc:     15.23% CPU
├── PageSerializer: 8.45% CPU
└── 搜索路径:      7.89% CPU

当前 QPS: 1,696 (磁盘 I/O)
纯内存:  6.53 ns/op (14+ million QPS)
```

**问题根源**:
- 每次写入都触发 fsync（同步刷盘）
- 频繁的内存分配（85% 在 PageSerializer）
- 缺少批处理支持

#### 2. 功能缺失（P1）
- **WAL (Write-Ahead Logging)**: 未实现，无法保证崩溃恢复
- **批处理 API**: 单条键值对写入，无批量优化
- **Buffer Pool**: 每次序列化都分配新 buffer
- **异步刷盘**: 虽有代码框架，但未实际启用

#### 3. 优化空间（P2）
- **并发控制**: 粒度较粗，可细化到 Page 级别
- **缓存策略**: 简单 LRU，可引入 ARC、2Q 等高级策略
- **压缩**: Page 内容可压缩存储

---

## Phase 2 目标

### 🎯 总体目标

**性能目标**:
```
指标              当前       Phase 2 目标    提升
──────────────────────────────────────────────
Set QPS          1,696     10,000+         5.9x
Get QPS          2,845     50,000+         17.6x
P99 延迟         15ms      <2ms            7.5x
吞吐量 (MB/s)    0.8       5+              6.25x
```

**功能目标**:
- ✅ 实现 WAL（崩溃恢复）
- ✅ 批处理 API（100x 批量写入）
- ✅ Buffer Pool（减少 85% 分配）
- ✅ 异步刷盘（可配置）

### 📊 详细目标

#### 1. WAL 实现（P0 - 严重）
**目标**: 实现完整的 Write-Ahead Logging 机制

**核心功能**:
- [ ] WAL 文件格式设计（header + records + checksum）
- [ ] 日志写入接口（批量写入、group commit）
- [ ] Checkpoint 机制（定期合并、压缩）
- [ ] 崩溃恢复（replay、redo、undo）
- [ ] 测试覆盖（单元、集成、性能）

**性能指标**:
- WAL 写入延迟: <100μs (P99)
- Checkpoint 时间: <1s (1GB 数据)
- 恢复时间: <10s (1GB WAL)

**技术选型**:
```
方案对比:
├── 方案 1: 简单 WAL (推荐)
│   ├── 优点: 实现简单、易于调试
│   └── 缺点: 性能一般
│
├── 方案 2: AHEAD (Advanced WAL)
│   ├── 优点: 高性能、零拷贝
│   └── 缺点: 复杂度高
│
└── 方案 3: etcd WAL
    ├── 优点: 成熟稳定
    └── 缺点: 依赖重、学习曲线陡
```

**推荐方案**: 方案 1（简单 WAL）+ 后续优化

#### 2. 批处理 API（P0 - 严重）
**目标**: 实现高效的批量写入接口

**API 设计**:
```go
// 批量 Set API
func (tree *BTree) SetBatch(ctx context.Context, kvs []KeyValue) error

// 批量 Get API
func (tree *BTree) GetBatch(ctx context.Context, keys [][]byte) ([]Value, error)

// 批量 Delete API
func (tree *BTree) DeleteBatch(ctx context.Context, keys [][]byte) error

// 事务 API（可选）
func (tree *BTree) Transaction(ctx context.Context, fn func(tx *Transaction) error) error
```

**性能指标**:
- 100x 批量写入: <10ms
- 1000x 批量写入: <100ms
- 吞吐量提升: 10x+

**实现要点**:
- [ ] 批量 Page 序列化
- [ ] 减少 fsync 次数（group commit）
- [ ] 并行处理（多 goroutine）

#### 3. Buffer Pool（P1 - 重要）
**目标**: 实现高效的 buffer 复用机制

**设计**:
```go
type BufferPool struct {
    pools map[int32]*sync.Pool // key=buffer size, value=pool
    stats PoolStats             // 统计信息
}

func (pool *BufferPool) Get(size int32) []byte
func (pool *BufferPool) Put(buf []byte)
```

**性能指标**:
- 内存分配减少: 85%
- GC 压力降低: 70%
- P99 延迟改善: 30%

**实现要点**:
- [ ] 分级 buffer（4KB, 8KB, 16KB, 32KB）
- [ ] 自动扩容/缩容
- [ ] 统计监控（命中率、分配次数）

#### 4. 异步刷盘优化（P1 - 重要）
**目标**: 启用并优化异步刷盘机制

**当前状态**:
```go
// page_persist.go 已有框架，但未启用
type PageManager struct {
    dirtyPages     chan *Page         // ✅ 已有
    flushInterval  time.Duration      // ✅ 已有
    flushBatchSize int                // ✅ 已有
    stopFlush      context.CancelFunc // ✅ 已有
    flushWg        sync.WaitGroup     // ✅ 已有
}
```

**待优化**:
- [ ] 启用异步写入（测试中需要同步，生产启用异步）
- [ ] 调优批量大小（当前 16 → 建议动态调整）
- [ ] 优化刷盘间隔（当前 100ms → 可配置）
- [ ] 添加紧急刷盘（dirty pages > threshold）

**性能指标**:
- Set 延迟: 15ms → <2ms (P99)
- 吞吐量: 1,696 QPS → 10,000+ QPS

---

## 性能优化详解

本章节基于 Phase 1 的 profiling 结果，详细阐述 Phase 2 的性能优化策略、实施方案和预期效果。

### 📊 Phase 1 性能分析回顾

#### CPU 性能瓶颈分析

**Set 操作 CPU 时间分布** (基于 pprof 数据):
```
总采样时间: 100%

┌─────────────────────────────────────────────────────┐
│ 热点函数              CPU 占比    累积占比           │
├─────────────────────────────────────────────────────┤
│ fsync                39.87%     39.87%  ← 最大瓶颈  │
│ mallocgc             15.23%     55.10%              │
│ PageSerializer       8.45%      63.55%              │
│ searchPath           7.89%      71.44%              │
│ splitLeaf            5.12%      76.56%              │
│ 其他                 23.44%     100.00%             │
└─────────────────────────────────────────────────────┘
```

**关键发现**:
1. **fsync 瓶颈**: 占用近 40% CPU 时间，主要是同步 I/O 等待
2. **内存分配**: mallocgc 占 15.23%，其中 85% 来自 PageSerializer
3. **序列化开销**: PageSerializer 虽然只占 8.45%，但主要是内存分配

#### 内存分配分析

**内存分配热点** (基于 memprof):
```
总分配: 15.23% CPU 时间

分配来源:
├── PageSerializer.Serialize:     85%  ← 主要来源
│   ├── []byte 创建:             60%
│   ├── binary.Write:            25%
│   └── 其他:                    15%
├── searchPath:                  10%
├── splitLeaf:                   3%
└── 其他:                        2%
```

**关键发现**:
- 每次 Set 操作分配 ~4KB buffer（Page 大小）
- 无 buffer 复用机制
- GC 压力大（频繁分配/释放）

#### QPS 分析

**当前性能** (基于 benchmark):
```
操作类型       QPS       延迟(P99)   瓶颈
────────────────────────────────────────────
Set (磁盘)    1,696     15ms        fsync
Get (磁盘)    2,845     8ms         I/O
Set (纯内存)   153K      6.53μs      搜索
Get (纯内存)   14.2M     70ns        序列化
────────────────────────────────────────────
性能差距:      8,400x    2,300x
```

**结论**:
- 纯内存操作非常快（搜索 6.53ns/op）
- 磁盘 I/O 是主要瓶颈（尤其是 fsync）
- 优化空间巨大（理论可达 10,000+ QPS）

---

### 🚀 优化策略总览

根据性能分析结果，我们制定以下优化策略：

```
优化策略优先级:
┌─────────────────────────────────────────────────────┐
│ 优化项             预期提升    实施难度    优先级   │
├─────────────────────────────────────────────────────┤
│ WAL + 批处理        5.9x       中         P0       │
│ Buffer Pool        2.5x       低         P0       │
│ 异步刷盘           2.0x       中         P1       │
│ 序列化优化         1.5x       中         P1       │
│ 搜索路径优化       1.3x       低         P2       │
├─────────────────────────────────────────────────────┤
│ 总计预期提升        28.7x      -          -        │
│ 实际目标提升        5.9x       -          P0       │
└─────────────────────────────────────────────────────┘
```

---

### 1. WAL + 批处理优化（解决 fsync 瓶颈）

#### 问题分析

**当前问题**:
```go
// 每次调用 Set 都会触发 fsync
func (pm *PageManager) WritePage(page *Page) error {
    // 1. 序列化
    buf := pm.serialize(page)

    // 2. 写入磁盘
    pm.file.Write(buf)

    // 3. ⚠️ 立即 fsync（每次都等待磁盘完成）
    pm.file.Sync()  // ← 占用 39.87% CPU 时间

    // 4. 清除脏标记
    page.ClearDirty()
}
```

**瓶颈分析**:
- fsync 是系统调用，需要等待磁盘完成
- 每次 Set 都调用 fsync，无法批量合并
- 磁盘 I/O 延迟: SSD ~0.1ms, HDD ~10ms

#### 优化方案：WAL + Group Commit

**核心思想**:
1. **先写 WAL**: 所有操作先记录到 WAL（顺序写，速度快）
2. **延迟 fsync**: 批量积累多个操作后统一 fsync
3. **异步刷盘**: Page 数据异步刷到磁盘

**实现流程**:
```go
func (tree *BTree) Set(ctx context.Context, key, value []byte) error {
    // 1. 先写 WAL（顺序写，<100μs）
    record := WALRecord{
        Type:   WALTypeSet,
        Key:    key,
        Value:  value,
    }
    if err := tree.wal.Write(ctx, []WALRecord{record}); err != nil {
        return err
    }

    // 2. 更新内存中的 Page（不刷盘）
    if err := tree.updatePage(key, value); err != nil {
        return err
    }

    // 3. 标记脏页（异步刷盘）
    tree.pageManager.MarkDirty(pageID)

    return nil  // ← 快速返回，不等待磁盘
}

// 后台 goroutine：批量刷盘
func (pm *PageManager) backgroundFlusher() {
    ticker := time.NewTicker(100 * time.Millisecond)  // 可配置
    batch := make([]*Page, 0, 32)

    for {
        select {
        case page := <-pm.dirtyPages:
            batch = append(batch, page)
            if len(batch) >= 32 {  // 批量大小
                pm.flushBatch(batch)
                batch = batch[:0]
            }

        case <-ticker.C:
            if len(batch) > 0 {
                pm.flushBatch(batch)
                batch = batch[:0]
            }
        }
    }
}

func (pm *PageManager) flushBatch(pages []*Page) {
    // 1. 批量写入所有页面
    for _, page := range pages {
        buf := pm.serialize(page)
        pm.file.Write(buf)
    }

    // 2. ⚠️ 只调用一次 fsync（Group Commit）
    pm.file.Sync()  // ← 39.87% → 5% CPU

    // 3. 清除脏标记
    for _, page := range pages {
        page.ClearDirty()
    }
}
```

**性能提升分析**:
```
优化前:
每次 Set: 1 次 fsync (10ms)
100 次 Set: 100 次 fsync = 1000ms
QPS: 1,696

优化后:
100 次 Set: 1 次 fsync = 10ms (Group Commit)
理论 QPS: 100 * 1000ms / 10ms = 10,000 QPS

实际 QPS: 考虑 WAL 开销，目标 8,000-10,000 QPS
提升倍数: 5.9x
```

#### WAL 特定优化

**1. WAL 文件设计**（顺序写优化）
```
WAL 文件布局（优化版）:
┌─────────────────────────────────────────────────────┐
│ WAL Header (64 bytes) - 固定大小                   │
├─────────────────────────────────────────────────────┤
│ WAL Records (变长) - 追加写入                       │
│ - Record 1 (Set key1=value1)                        │
│ - Record 2 (Set key2=value2)                        │
│ - ...                                               │
│ - Record N (Delete keyX)                            │
├─────────────────────────────────────────────────────┤
│ WAL Footer (64 bytes) - 包含校验和                  │
└─────────────────────────────────────────────────────┘

关键优化:
✓ 固定 Header/Footer - 快速定位
✓ 追加写入 - 无需 seek
✓ 批量写入 - 减少系统调用
✓ 内存映射 - 可选优化（mmap）
```

**2. WAL 写入优化**
```go
// 使用 buffering 批量写入
type WAL struct {
    file       *os.File
    writer     *bufio.Writer  // ← 缓冲区
    batchChan  chan WALRecord // ← 批量 channel
    batchSize int             // ← 批量大小
}

func (wal *WAL) Write(ctx context.Context, records []WALRecord) error {
    // 1. 快速写入内存 buffer
    for _, record := range records {
        data := wal.serialize(record)
        if _, err := wal.writer.Write(data); err != nil {
            return err
        }
    }

    // 2. 可选: 立即 Flush（关键操作）
    if wal.config.SyncOnWrite {
        return wal.writer.Flush()  // ← 只有 Flush 才触发磁盘 I/O
    }

    // 3. 否则: 后台 goroutine 定期 Flush
    return nil
}
```

**3. Group Commit 策略**
```go
type GroupCommitConfig struct {
    // 策略 1: 时间间隔（默认 100ms）
    Interval time.Duration

    // 策略 2: 批量大小（默认 32 个 Page）
    BatchSize int

    // 策略 3: 紧急阈值（默认 1000 个脏页）
    EmergencyThreshold int
}

// 动态调整策略
func (pm *PageManager) adjustFlushStrategy() {
    dirtyCount := len(pm.dirtyPages)

    switch {
    case dirtyCount > 1000:
        // 紧急模式: 立即刷盘
        go pm.flushBatch(pm.collectDirtyPages())

    case dirtyCount > 100:
        // 正常模式: 缩短间隔
        pm.flushInterval = 50 * time.Millisecond

    default:
        // 节能模式: 正常间隔
        pm.flushInterval = 100 * time.Millisecond
    }
}
```

---

### 2. Buffer Pool 优化（解决内存分配瓶颈）

#### 问题分析

**当前问题**:
```go
// 每次序列化都分配新 buffer
func (ps *PageSerializer) Serialize(page *Page) ([]byte, error) {
    // ⚠️ 每次分配 4KB buffer
    buf := make([]byte, PageSize)  // ← 85% 内存分配来源

    // 序列化到 buffer
    offset := 0
    offset += binary.PutUvarint(buf[offset:], uint64(page.ID))
    // ... 更多序列化代码

    return buf, nil
}  // ← buffer 使用后丢弃，触发 GC
```

**内存分配热点**:
```
每次 Set 操作分配:
├── PageSerializer buffer:  4KB
├── WAL Record buffer:      512B
├── 临时 buffer:            1KB
└── 总计:                   ~5.5KB

1000 次 Set:
├── 总分配: 5.5MB
├── GC 频率: 高（频繁分配/释放）
└── CPU 时间: 15.23% (mallocgc)
```

#### 优化方案：Buffer Pool

**核心思想**:
1. **对象复用**: 使用 sync.Pool 复用 buffer
2. **分级池**: 不同大小 buffer 使用不同 pool
3. **统计监控**: 跟踪命中率、分配次数

**实现设计**:
```go
type BufferPool struct {
    // 分级池（4KB, 8KB, 16KB, 32KB）
    pools [4]*sync.Pool

    // 统计信息
    stats PoolStats

    // 配置
    config PoolConfig
}

type PoolStats struct {
    Hits        atomic.Int64  // 命中次数
    Misses      atomic.Int64  // 未命中次数
    Allocations atomic.Int64  // 新分配次数
    InPool      atomic.Int64  // 当前在池中的 buffer 数
}

// 获取 buffer
func (pool *BufferPool) Get(size int32) []byte {
    // 1. 确定级别
    level := pool.getLevel(size)

    // 2. 尝试从池中获取
    if buf, ok := pool.pools[level].Get().([]byte); ok {
        if cap(buf) >= size {
            pool.stats.Hits.Add(1)
            pool.stats.InPool.Add(-1)
            return buf[:size]  // 重置长度
        }
        // 容量不足，丢弃
    }

    // 3. 池中无可用 buffer，分配新的
    pool.stats.Misses.Add(1)
    pool.stats.Allocations.Add(1)

    actualSize := pool.roundUpSize(size)
    return make([]byte, actualSize)[:size]
}

// 归还 buffer
func (pool *BufferPool) Put(buf []byte) {
    if buf == nil || cap(buf) == 0 {
        return
    }

    // 检查是否在统计范围内
    level := pool.getLevel(int32(cap(buf)))

    // 归还到对应的池
    pool.pools[level].Put(buf)
    pool.stats.InPool.Add(1)
}

// 辅助方法：确定级别
func (pool *BufferPool) getLevel(size int32) int {
    switch {
    case size <= 4*1024:   // 4KB
        return 0
    case size <= 8*1024:   // 8KB
        return 1
    case size <= 16*1024:  // 16KB
        return 2
    default:               // 32KB
        return 3
    }
}

// 辅助方法：向上取整到标准大小
func (pool *BufferPool) roundUpSize(size int32) int32 {
    switch {
    case size <= 4*1024:
        return 4 * 1024
    case size <= 8*1024:
        return 8 * 1024
    case size <= 16*1024:
        return 16 * 1024
    default:
        return 32 * 1024
    }
}
```

**集成到 PageSerializer**:
```go
var globalBufferPool = NewBufferPool(BufferPoolConfig{
    MaxCached: 1000,  // 每个级别最多缓存 1000 个 buffer
})

func (ps *PageSerializer) Serialize(page *Page) ([]byte, error) {
    // ✅ 从 pool 获取 buffer（复用）
    buf := globalBufferPool.Get(PageSize)

    // 序列化到 buffer
    offset := 0
    offset += binary.PutUvarint(buf[offset:], uint64(page.ID))
    // ... 更多序列化代码

    return buf, nil
}

// 解序列化后归还 buffer
func (ps *PageSerializer) Deserialize(buf []byte) (*Page, error) {
    defer globalBufferPool.Put(buf)  // ← 归还到 pool

    // ... 解序列化逻辑
    return page, nil
}
```

**性能提升分析**:
```
优化前:
每次 Set: 分配 5.5KB
1000 次 Set: 分配 5.5MB, GC 压力大
mallocgc CPU: 15.23%

优化后:
每次 Set: Pool 命中率 80%+ (复用 buffer)
1000 次 Set:
  - 命中 800 次: 复用 800 * 4KB = 3.2MB (0 分配)
  - 未命中 200 次: 分配 200 * 4KB = 0.8MB
  - 总分配: 0.8MB (vs 5.5MB)
  - 分配减少: 85%

mallocgc CPU: 15.23% → 2.3% (↓85%)
总 QPS 提升: 2.5x (减少 GC 暂停)
```

**统计监控**:
```go
// 定期打印统计信息
func (pool *BufferPool) PrintStats() {
    hits := pool.stats.Hits.Load()
    misses := pool.stats.Misses.Load()
    total := hits + misses

    hitRate := float64(hits) / float64(total) * 100

    log.Printf("Buffer Pool Stats:\n"+
        "  Hit Rate: %.2f%%\n"+
        "  Hits: %d\n"+
        "  Misses: %d\n"+
        "  Allocations: %d\n"+
        "  In Pool: %d",
        hitRate, hits, misses,
        pool.stats.Allocations.Load(),
        pool.stats.InPool.Load())
}

// 预期输出:
// Buffer Pool Stats:
//   Hit Rate: 85.23%  ← 目标 >80%
//   Hits: 850000
//   Misses: 150000
//   Allocations: 150000
//   In Pool: 420
```

---

### 3. 序列化优化（减少 CPU 开销）

#### 问题分析

**PageSerializer CPU 占用**: 8.45%

**热点函数**:
```go
func binary.PutUvarint(buf []byte, x uint64) int {
    // ⚠️ 大量函数调用
    for i := 0; i < 10; i++ {
        // ...
    }
}
```

#### 优化方案

**1. 批量序列化**
```go
// 优化前: 逐个字段序列化
func (ps *PageSerializer) Serialize(page *Page) []byte {
    buf := make([]byte, PageSize)
    offset := 0
    offset += binary.PutUvarint(buf[offset:], uint64(page.ID))
    offset += binary.PutUvarint(buf[offset:], uint64(page.Type))
    // ... 每个字段都调用 PutUvarint
}

// 优化后: 批量序列化
func (ps *PageSerializer) Serialize(page *Page) []byte {
    buf := globalBufferPool.Get(PageSize)

    // ✅ 使用 unsafe 直接操作内存（零拷贝）
    header := (*[PageSize]byte)(unsafe.Pointer(&buf[0]))

    // 直接写入内存（无需 offset 计算）
    binary.LittleEndian.PutUint64(buf[0:8], uint64(page.ID))
    binary.LittleEndian.PutUint64(buf[8:16], uint64(page.Type))
    // ... 更多字段

    return buf
}
```

**2. 预分配 buffer**
```go
// 优化前: 动态扩展 slice
func serializeKey(key []byte) []byte {
    var buf []byte
    buf = binary.PutUvarint(buf, uint64(len(key)))
    buf = append(buf, key...)
    return buf  // ← 多次重新分配
}

// 优化后: 预分配精确大小
func serializeKey(key []byte) []byte {
    size := 1 + binary.MaxVarintLen64 + len(key)
    buf := globalBufferPool.Get(int32(size))

    offset := 0
    offset += binary.PutUvarint(buf[offset:], uint64(len(key)))
    copy(buf[offset:], key)

    return buf[:offset]
}
```

**性能提升分析**:
```
优化前:
序列化开销: 8.45% CPU
每次 Set: ~100 次函数调用

优化后:
序列化开销: 8.45% → 5% CPU (↓41%)
每次 Set: ~10 次函数调用

QPS 提升: 1.3x (减少 CPU 开销)
```

---

### 4. 综合优化效果分析

#### 优化前后的性能对比

```
┌──────────────────────────────────────────────────────┐
│ 操作            优化前      优化后      提升          │
├──────────────────────────────────────────────────────┤
│ Set QPS         1,696      10,000     5.9x          │
│ Set P99 延迟    15ms       <2ms       7.5x          │
│ Get QPS         2,845      50,000     17.6x         │
│ Get P99 延迟    8ms        <1ms       8x            │
│ CPU 使用率      100%       65%        ↓35%          │
│ 内存分配        5.5KB/op   0.8KB/op   ↓85%          │
│ GC 压力         高         低          ↓70%          │
│ fsync 调用      1/Set      0.1/Set    ↓90%          │
└──────────────────────────────────────────────────────┘
```

#### 优化贡献分析

```
总提升: 5.9x QPS

分解:
┌─────────────────────────────────────────────────────┐
│ 优化项           QPS 贡献    百分比                │
├─────────────────────────────────────────────────────┤
│ WAL + 批处理      +7,200     78%  ← 主要贡献      │
│ Buffer Pool      +1,200     13%                   │
│ 序列化优化       +500       5%                    │
│ 异步刷盘         +400       4%                    │
│ 其他             +100       1%                    │
├─────────────────────────────────────────────────────┤
│ 总计             +9,400     100%                  │
│ 实际提升: 1,696 → 10,000 = +8,304 (88% 达成率)   │
└─────────────────────────────────────────────────────┘
```

#### 实施优先级建议

**P0 - 必须实施** (预期 5.9x 提升):
- [ ] WAL 实现（崩溃恢复 + 顺序写）
- [ ] 批处理 API（SetBatch, GetBatch）
- [ ] Group Commit（减少 fsync）
- [ ] Buffer Pool（减少 85% 分配）

**P1 - 强烈建议** (额外 1.5x 提升):
- [ ] 异步刷盘（可配置策略）
- [ ] 序列化优化（unsafe 直接内存操作）
- [ ] 动态批量大小调整

**P2 - 可选优化** (额外 1.2x 提升):
- [ ] WAL mmap（零拷贝）
- [ ] 搜索路径缓存（减少重复搜索）
- [ ] Page 压缩（减少磁盘 I/O）

---

## 核心技术架构

### 1. WAL 架构设计

#### 文件格式
```
WAL 文件布局:
┌─────────────────────────────────────────────────────┐
│ WAL Header (64 bytes)                               │
│ - Magic: 8 bytes ("NEXKVWAL")                       │
│ - Version: 4 bytes                                   │
│ - FileID: 8 bytes (monotonic increasing)            │
│ - Checksum: 4 bytes (CRC32)                         │
│ - Reserved: 40 bytes                                 │
├─────────────────────────────────────────────────────┤
│ WAL Record 1                                         │
│ - Type: 1 byte (0=data, 1=delete, 2=checkpoint)     │
│ - Length: 4 bytes                                    │
│ - KeySize: 4 bytes                                   │
│ - ValueSize: 4 bytes                                 │
│ - Key: []byte                                        │
│ - Value: []byte                                      │
│ - Checksum: 4 bytes (CRC32)                         │
├─────────────────────────────────────────────────────┤
│ WAL Record 2                                         │
│ ...                                                 │
├─────────────────────────────────────────────────────┤
│ WAL Trailer (64 bytes)                              │
│ - RecordCount: 8 bytes                               │
│ - FileSize: 8 bytes                                  │
│ - Checksum: 4 bytes                                  │
│ - Reserved: 44 bytes                                 │
└─────────────────────────────────────────────────────┘
```

#### 写入流程
```go
func (wal *WAL) Write(ctx context.Context, records []WALRecord) error {
    // 1. 序列化 records
    buf := wal.serializeRecords(records)

    // 2. 计算 checksum
    checksum := crc32.ChecksumIEEE(buf)

    // 3. 追加到 WAL 文件
    if err := wal.file.Write(buf); err != nil {
        return err
    }

    // 4. 可选: 立即 fsync（关键操作）
    if wal.config.SyncOnWrite {
        if err := wal.file.Sync(); err != nil {
            return err
        }
    }

    // 5. 更新内存索引
    wal.index.AddRecords(records)

    return nil
}
```

#### Checkpoint 机制
```go
// Checkpoint 策略
type CheckpointPolicy struct {
    // 时间间隔（默认 5 分钟）
    Interval time.Duration

    // WAL 大小阈值（默认 100MB）
    SizeThreshold int64

    // 记录数阈值（默认 100,000）
    CountThreshold int64
}

func (wal *WAL) Checkpoint(ctx context.Context) error {
    // 1. 创建快照
    snapshot := wal.tree.CreateSnapshot()

    // 2. 持久化快照
    if err := wal.persistSnapshot(snapshot); err != nil {
        return err
    }

    // 3. 清理旧 WAL 文件
    wal.cleanupOldFiles()

    // 4. 更新 checkpoint 元数据
    wal.metadata.LastCheckpoint = time.Now()

    return nil
}
```

#### 崩溃恢复
```go
func (wal *WAL) Recover(ctx context.Context) error {
    // 1. 读取最新 snapshot
    snapshot, err := wal.loadLatestSnapshot()
    if err != nil {
        return err
    }

    // 2. 加载 snapshot 到 BTree
    if err := wal.tree.Restore(snapshot); err != nil {
        return err
    }

    // 3. 扫描 WAL 文件（从 checkpoint 之后）
    walFiles := wal.listWALFilesAfter(snapshot.LastWALFileID)

    // 4. Replay WAL records
    for _, file := range walFiles {
        records, err := wal.readFile(file)
        if err != nil {
            return err
        }

        for _, record := range records {
            if err := wal.applyRecord(record); err != nil {
                return err // 部分恢复失败
            }
        }
    }

    // 5. 创建新的 WAL 文件（避免追加到旧文件）
    wal.createNewWALFile()

    return nil
}
```

### 2. 批处理 API 架构

#### SetBatch 实现
```go
func (tree *BTree) SetBatch(ctx context.Context, kvs []KeyValue) error {
    // 1. 参数校验
    if len(kvs) == 0 {
        return nil
    }
    if len(kvs) > MaxBatchSize {
        return ErrBatchTooLarge
    }

    // 2. 按 Page 分组
    pageGroups := tree.groupByPage(kvs)

    // 3. 并行处理每个 Page
    var wg sync.WaitGroup
    errChan := make(chan error, len(pageGroups))

    for pageID, kvs := range pageGroups {
        wg.Add(1)
        go func(pid model.PageID, batch []KeyValue) {
            defer wg.Done()

            // 加载 Page
            page, err := tree.getPageOrLoad(pid)
            if err != nil {
                errChan <- err
                return
            }

            // 批量插入
            if leaf, ok := page.(*LeafPage); ok {
                for _, kv := range batch {
                    leaf.Insert(kv.Key, kv.Value)
                }
            }

            // 标记脏页
            page.MarkDirty()

        }(pageID, kvs)
    }

    wg.Wait()
    close(errChan)

    // 4. 检查错误
    for err := range errChan {
        if err != nil {
            return err
        }
    }

    // 5. 批量刷盘（group commit）
    if err := tree.flushDirtyPages(ctx); err != nil {
        return err
    }

    return nil
}
```

#### 事务 API（可选）
```go
type Transaction struct {
    tree    *BTree
    writes  []KeyValue
    deletes [][]byte
    mu      sync.Mutex
}

func (tx *Transaction) Set(key, value []byte) error {
    tx.mu.Lock()
    defer tx.mu.Unlock()

    tx.writes = append(tx.writes, KeyValue{Key: key, Value: value})
    return nil
}

func (tx *Transaction) Delete(key []byte) error {
    tx.mu.Lock()
    defer tx.mu.Unlock()

    tx.deletes = append(tx.deletes, key)
    return nil
}

func (tx *Transaction) Commit(ctx context.Context) error {
    // 1. 批量写入
    if len(tx.writes) > 0 {
        if err := tx.tree.SetBatch(ctx, tx.writes); err != nil {
            return err
        }
    }

    // 2. 批量删除
    if len(tx.deletes) > 0 {
        if err := tx.tree.DeleteBatch(ctx, tx.deletes); err != nil {
            return err
        }
    }

    return nil
}
```

### 3. Buffer Pool 设计

#### Pool 结构
```go
type BufferPool struct {
    // 分级 buffer（4KB, 8KB, 16KB, 32KB）
    pools [4]sync.Pool

    // 统计信息
    stats PoolStats

    // 配置
    config PoolConfig
}

type PoolStats struct {
    Hits        atomic.Int64 // 命中次数
    Misses      atomic.Int64 // 未命中次数
    Allocations atomic.Int64 // 新分配次数
    Puts        atomic.Int64 // 归还次数
}

type PoolConfig struct {
    // 最大缓存数量（每个级别）
    MaxCached int

    // 是否启用统计
    EnableStats bool

    // 最小空闲时间（超过则 GC 回收）
    MinIdleTime time.Duration
}
```

#### 实现细节
```go
func (pool *BufferPool) Get(size int32) []byte {
    // 1. 确定级别
    level := pool.getLevel(size)

    // 2. 尝试从 pool 获取
    if buf, ok := pool.pools[level].Get().([]byte); ok {
        if cap(buf) >= size {
            pool.stats.Hits.Add(1)
            return buf[:size] // 重置长度
        }
        // 容量不足，丢弃
    }

    // 3. Pool miss，分配新 buffer
    pool.stats.Misses.Add(1)
    pool.stats.Allocations.Add(1)

    actualSize := pool.roundUpSize(size)
    return make([]byte, actualSize)[:size]
}

func (pool *BufferPool) Put(buf []byte) {
    if buf == nil {
        return
    }

    // 1. 检查是否在统计范围内
    level := pool.getLevel(int32(cap(buf)))

    // 2. 归还到对应的 pool
    pool.pools[level].Put(buf)
    pool.stats.Puts.Add(1)
}

func (pool *BufferPool) getLevel(size int32) int {
    // 0: 4KB, 1: 8KB, 2: 16KB, 3: 32KB
    switch {
    case size <= 4*1024:
        return 0
    case size <= 8*1024:
        return 1
    case size <= 16*1024:
        return 2
    default:
        return 3
    }
}
```

---

## 实施计划

### 📅 时间线

```
Week 1: WAL 实现与测试
├── Day 1-2: WAL 文件格式设计 + 单元测试
├── Day 3-4: WAL 写入/读取实现
├── Day 5:   Checkpoint 机制
└── Day 6-7: 崩溃恢复 + 集成测试

Week 2: 批处理 API + Buffer Pool
├── Day 1-2: Buffer Pool 实现
├── Day 3-4: SetBatch/DeleteBatch API
├── Day 5:   事务 API（可选）
└── Day 6-7: 性能测试 + 调优

Week 3: 异步刷盘优化 + 文档
├── Day 1-2: 启用异步刷盘 + 配置化
├── Day 3-4: 性能调优（批量大小、间隔）
├── Day 5:   文档编写
└── Day 6-7: Code Review + 修复
```

### 📋 任务清单

#### Week 1: WAL 实现（P0）

**Day 1-2: WAL 基础设施**
- [ ] 设计 WAL 文件格式
- [ ] 实现 WAL Header/Trailer
- [ ] 实现 WAL Record 序列化/反序列化
- [ ] 单元测试：文件格式验证
- [ ] 单元测试：Record 编解码

**Day 3-4: WAL 读写**
- [ ] 实现 `WAL.Open()` 创建/打开 WAL
- [ ] 实现 `WAL.Write()` 批量写入
- [ ] 实现 `WAL.Read()` 读取记录
- [ ] 集成测试：写入/读取循环
- [ ] 性能测试：批量写入性能

**Day 5: Checkpoint**
- [ ] 实现 `WAL.Checkpoint()` 快照
- [ ] 实现 `WAL.cleanupOldFiles()` 清理
- [ ] 集成测试：Checkpoint 流程
- [ ] 性能测试：Checkpoint 时间

**Day 6-7: 崩溃恢复**
- [ ] 实现 `WAL.Recover()` 重放
- [ ] 集成测试：模拟崩溃
- [ ] 集成测试：恢复验证
- [ ] 性能测试：恢复时间

#### Week 2: 批处理 API（P0）

**Day 1-2: Buffer Pool**
- [ ] 实现 `BufferPool.Get()`
- [ ] 实现 `BufferPool.Put()`
- [ ] 实现统计信息
- [ ] 单元测试：Put/Get 循环
- [ ] 性能测试：分配减少率

**Day 3-4: 批处理 API**
- [ ] 实现 `BTree.SetBatch()`
- [ ] 实现 `BTree.GetBatch()`
- [ ] 实现 `BTree.DeleteBatch()`
- [ ] 集成测试：批量操作
- [ ] 性能测试：吞吐量提升

**Day 5: 事务 API（可选）**
- [ ] 实现 `Transaction` 类型
- [ ] 实现 `BTree.Transaction()`
- [ ] 集成测试：事务回滚
- [ ] 性能测试：事务开销

**Day 6-7: 性能调优**
- [ ] Benchmark: SetBatch 性能
- [ ] Benchmark: 批处理 vs 单条
- [ ] Profiling: 热点分析
- [ ] 优化: 减少内存分配

#### Week 3: 异步刷盘（P1）

**Day 1-2: 启用异步刷盘**
- [ ] 修改 `PageManager.WritePage()` 启用异步
- [ ] 实现可配置的刷盘策略
- [ ] 集成测试：异步写入
- [ ] 测试：崩溃恢复一致性

**Day 3-4: 性能调优**
- [ ] 调优 `flushBatchSize`（16 → 动态）
- [ ] 调优 `flushInterval`（100ms → 可配置）
- [ ] 实现紧急刷盘（阈值触发）
- [ ] 性能测试：P99 延迟

**Day 5-7: 文档 + Review**
- [ ] 编写 WAL 设计文档
- [ ] 编写批处理 API 文档
- [ ] 编写性能测试报告
- [ ] Code Review: 修复问题
- [ ] 准备 Phase 2 总结

---

## 验收标准

### ✅ 功能验收

#### WAL 验收
- [ ] **基本功能**: 写入、读取、Checkpoint
- [ ] **崩溃恢复**: 模拟崩溃，数据完整恢复
- [ ] **性能**: WAL 写入 <100μs (P99)
- [ ] **测试覆盖**: >90% (核心逻辑)

#### 批处理 API 验收
- [ ] **功能正确**: SetBatch、GetBatch、DeleteBatch
- [ ] **性能提升**: 10x+ 吞吐量
- [ ] **测试覆盖**: >85%
- [ ] **文档完整**: API 文档、示例代码

#### Buffer Pool 验收
- [ ] **内存减少**: 85% 分配减少
- [ ] **命中率**: >80% (典型负载)
- [ ] **线程安全**: 并发测试通过
- [ ] **统计准确**: Hits/Misses 计数正确

#### 异步刷盘验收
- [ ] **延迟改善**: P99 <2ms
- [ ] **吞吐量**: >10,000 QPS
- [ ] **一致性**: 崩溃恢复数据不丢
- [ ] **可配置**: 策略可动态调整

### 📊 性能验收

```bash
# 基准测试目标
BenchmarkBTree_SetBatch_100:    <10ms   # 当前 50ms
BenchmarkBTree_SetBatch_1000:   <100ms  # 当前 500ms
BenchmarkBTree_GetBatch_100:    <5ms    # 当前 20ms
BenchmarkBTree_WAL_Write:       <100μs  # 新增
BenchmarkBTree_Checkpoint:      <1s     # 新增 (1GB)
BenchmarkBTree_Recover:         <10s    # 新增 (1GB WAL)

# QPS 目标
Set QPS:   1,696 → 10,000+    (5.9x)
Get QPS:   2,845 → 50,000+    (17.6x)
Batch QPS: 0     → 100,000+   (新增)
```

### 🧪 测试验收

```bash
# 单元测试
go test ./internal/infrastructure/storage/btree/... -v
✅ 所有测试通过

# 并发测试
go test ./internal/infrastructure/storage/btree/... -race -v
✅ 无数据竞争

# 集成测试
go test ./internal/infrastructure/storage/btree/... -tags=integration -v
✅ WAL + 批处理 + Buffer Pool 集成测试

# 覆盖率
go test ./internal/infrastructure/storage/btree/... -coverprofile=coverage.out
go tool cover -func=coverage.out | grep total
✅ 覆盖率 >85%
```

### 🔍 代码质量验收

```bash
# Lint
make lint
✅ 0 issues

# 格式化
make fmt
✅ 无变更

# Vet
go vet ./...
✅ 无警告
```

---

## 风险评估

### ⚠️ 高风险项

#### 1. WAL 数据一致性（P0）
**风险**: 崩溃恢复时数据损坏/丢失

**缓解措施**:
- [ ] 严格的单元测试（覆盖所有边界情况）
- [ ] 集成测试：模拟各种崩溃场景
- [ ] Chaos Engineering：随机 kill 进程
- [ ] Code Review：至少 2 人审核

#### 2. 异步刷盘数据丢失（P0）
**风险**: 进程崩溃，内存中脏页丢失

**缓解措施**:
- [ ] WAL 保证：所有操作先写 WAL
- [ ] 可配置策略：同步/异步模式
- [ ] 监控告警：脏页数量、刷盘延迟
- [ ] 压力测试：长时间运行稳定性

### ⚡ 中风险项

#### 3. Buffer Pool 内存泄露（P1）
**风险**: Pool 泄露导致 OOM

**缓解措施**:
- [ ] 定期 health check
- [ ] 统计监控：Pool 大小趋势
- [ ] 压力测试：长时间运行
- [ ] pprof 监控：堆内存分析

#### 4. 批处理 API 死锁（P1）
**风险**: 并发访问导致死锁

**缓解措施**:
- [ ] 死锁检测（go test -deadlock）
- [ ] 并发测试：高并发场景
- [ ] 超时机制：避免无限等待
- [ ] 代码审查：锁依赖图

### 📊 低风险项

#### 5. 性能回归（P2）
**风险**: 优化后性能反而下降

**缓解措施**:
- [ ] Benchmark 对比：优化前后对比
- [ ] 持续集成：每次提交运行 benchmark
- [ ] 性能监控：P50/P95/P99 延迟
- [ ] 回滚计划：Git bisect 快速定位

---

## 资源需求

### 👥 人力资源

**核心开发**: 1-2 人
- WAL 实现：5-7 天
- 批处理 API：3-5 天
- Buffer Pool：2-3 天
- 异步刷盘：2-3 天
- 测试 + 文档：3-4 天

**Code Review**: 1-2 人
- 每周 Review：1-2 小时
- 关键设计：1 小时讨论

**测试工程**: 1 人（可选）
- 测试用例编写
- 自动化测试
- 性能测试

### 🖥️ 计算资源

**开发环境**:
- CPU: 4 核心以上
- 内存: 16GB 以上
- 磁盘: SSD 100GB 以上

**测试环境**:
- 压力测试机器：8 核心以上
- 模拟生产环境配置
- 长时间运行稳定性测试

**监控工具**:
- pprof（性能分析）
- Grafana（监控面板）
- Prometheus（指标采集）

### 📚 参考资料

**WAL 设计**:
- [etcd WAL 实现](https://github.com/etcd-io/etcd/tree/main/server/wal)
- [PostgreSQL WAL](https://www.postgresql.org/docs/current/wal.html)
- [MySQL Binlog](https://dev.mysql.com/doc/refman/8.0/en/binary-log.html)

**性能优化**:
- [Go高性能编程](https://github.com/dgryski/go-perfbook)
- [sync.Pool 最佳实践](https://go.dev/doc/effective_go#initialization)
- [异步 I/O 模式](https://blog.cloudflare.com/how-to-optimize-go-code-for-performance/)

**测试方法**:
- [Go 并发测试](https://go.dev/doc/additions/race)
- [Property-Based Testing](https://github.com/testing-in-go/e-book/blob/main/chapter-9-property-based-testing.md)
- [Chaos Engineering](https://principlesofchaos.org/)

---

## 附录

### A. 性能基准测试模板

```go
// bench_test.go
func BenchmarkBTree_SetBatch_100(b *testing.B) {
    tree := setupBTree(b)
    kvs := generateKVs(100)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        if err := tree.SetBatch(context.Background(), kvs); err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkBTree_WAL_Write(b *testing.B) {
    wal := setupWAL(b)
    records := generateRecords(10)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        if err := wal.Write(context.Background(), records); err != nil {
            b.Fatal(err)
        }
    }
}
```

### B. 配置文件示例

```yaml
# config/btree.yaml
btree:
  # WAL 配置
  wal:
    enabled: true
    dir: "./data/wal"
    max_file_size: 100MB
    sync_on_write: false  # 关键操作才 sync

    checkpoint:
      interval: 5m
      size_threshold: 100MB
      count_threshold: 100000

  # 批处理配置
  batch:
    max_size: 1000
    timeout: 100ms

  # Buffer Pool 配置
  buffer_pool:
    max_cached: 1000
    enable_stats: true
    min_idle_time: 10m

  # 异步刷盘配置
  async_flush:
    enabled: true
    batch_size: 32
    interval: 100ms
    emergency_threshold: 1000  # 脏页数
```

### C. 监控指标

```go
// metrics.go
var (
    // QPS 指标
    setQPS    = prometheus.NewCounterVec(...)
    getQPS    = prometheus.NewCounterVec(...)
    batchQPS  = prometheus.NewCounterVec(...)

    // 延迟指标
    setLatency = prometheus.NewHistogramVec(...)
    getLatency = prometheus.NewHistogramVec(...)

    // WAL 指标
    walWriteBytes    = prometheus.NewCounter(...)
    walSyncCount     = prometheus.NewCounter(...)
    walCheckpointTime = prometheus.NewHistogram(...)

    // Buffer Pool 指标
    poolHits      = prometheus.NewCounter(...)
    poolMisses    = prometheus.NewCounter(...)
    poolSize      = prometheus.NewGauge(...)
)
```

---

**文档版本**: v1.0
**最后更新**: 2026-03-13
**讨论链接**: [GitHub Discussion #XXX](https://github.com/jzhang405/NexKV/discussions/XXX)

---

## 📝 讨论要点

请团队成员重点关注以下问题：

1. **WAL 设计**: 简单 WAL vs AHEAD vs etcd WAL，哪种更适合？
2. **事务 API**: 是否需要实现完整的事务支持？
3. **性能目标**: QPS 目标是否现实？需要调整吗？
4. **时间估算**: 2-3 周是否合理？是否需要增加人手？
5. **风险评估**: 是否有遗漏的高风险项？

欢迎提出意见和建议！🙏
