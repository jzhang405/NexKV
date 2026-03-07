# BfTree 性能优化计划（基于 Benchmark 结果）

> **文档日期**: 2026-03-07
> **分支**: feature/bftree-performance-optimization
> **关联 PR**: PR-091
> **基准测试**: [BoltDB vs BfTree 性能对比](../10_benchmark/2026-03-07_boltdb-vs-bftree/performance-report.md)

---

## 1. 问题分析

### 1.1 当前性能瓶颈

根据基准测试结果，BfTree 在以下场景下性能不足：

| 场景 | BoltDB | BfTree P0 | BfTree P1 | 差距 |
|------|--------|-----------|-----------|------|
| **顺序写入** | 23,946 ns | 182,622 ns | 166,282 ns | **慢 6.9x - 7.6x** |
| **随机写入** | 21,727 ns | 275,696 ns | 268,268 ns | **慢 12.3x - 12.7x** |
| **并发写** | 20,722 ns | 70,025 ns | 87,001 ns | **慢 3.4x - 4.2x** |

### 1.2 根本原因分析

**写入路径过长**:
```
当前写入路径：
Set() → WAL.Append() → WAL.Sync() → Page.Modify() → DeltaChain.Append() → PageTable.Update()
         ↓                 ↓                    ↓                     ↓
      同步I/O          fsync系统调用         内存分配              原子操作
```

**BoltDB 写入路径**:
```
BoltDB.Put() → mmap.Write() → (后台定期fsync)
                ↓
            内存复制（极快）
```

**关键差异**:
1. **WAL 同步写入**: 每次写入都调用 fsync（强制刷盘）
2. **页面分配开销**: Delta Chain 需要频繁分配新页面
3. **锁竞争**: RWMutex 在高并发写场景下竞争激烈
4. **内存分配**: 每次写入分配 ~225KB（BoltDB 仅 ~15KB）

---

## 2. 优化目标

### 2.1 性能目标

| 操作 | 当前性能 | 目标性能 | 提升幅度 |
|------|---------|---------|---------|
| **顺序写入** | 182,622 ns | < 50,000 ns | **+3.6x** |
| **随机写入** | 275,696 ns | < 80,000 ns | **+3.4x** |
| **并发写** | 70,025 ns | < 30,000 ns | **+2.3x** |

**最终目标**: 写入性能接近 BoltDB（差距 < 2x）

### 2.2 资源目标

| 指标 | 当前 | 目标 |
|------|------|------|
| **内存分配** | 225 KB/op | < 50 KB/op |
| **分配次数** | 639 allocs/op | < 100 allocs/op |

---

## 3. P0 优化项（核心瓶颈）

### P0-1: WAL 批量写入优化

**优先级**: 🔴 最高
**预期提升**: +50%~100%
**预估工作量**: 2-3 天

#### 问题描述
当前每次 `Set()` 都同步调用 `WAL.Append()` + `WAL.Sync()`，导致大量 fsync 系统调用。

```go
// 当前实现（慢）
func (t *BfTree) Set(key, value []byte) error {
    // 1. 写 WAL（同步）
    lsn, err := t.wal.Append(entry)
    if err != nil {
        return err
    }

    // 2. 强制刷盘（慢！）
    if err := t.wal.Sync(); err != nil {
        return err
    }

    // 3. 修改页面
    return t.modifyPage(key, value)
}
```

#### 优化方案

**方案 1: WAL 批量缓冲**
```go
type BfTree struct {
    wal       *wal.WAL
    walBuffer *WALBatchBuffer  // 新增：WAL 批量缓冲
    walMutex  sync.Mutex
}

type WALBatchBuffer struct {
    entries []WALEntry
    size    int
    maxSize int  // 例如：1MB
}

func (t *BfTree) Set(key, value []byte) error {
    // 1. 写入缓冲区（内存操作，极快）
    t.walMutex.Lock()
    lsn, err := t.walBuffer.Append(entry)
    t.walMutex.Unlock()

    // 2. 修改页面
    if err := t.modifyPage(key, value); err != nil {
        return err
    }

    // 3. 异步刷新 WAL（后台 goroutine）
    if t.walBuffer.ShouldFlush() {
        go t.flushWAL()
    }

    return nil
}
```

**方案 2: WAL 异步写入**
```go
type AsyncWAL struct {
    wal        *wal.WAL
    entryChan  chan WALEntry  // 缓冲通道
    flushChan  chan struct{}   // 刷盘信号
    stopChan   chan struct{}   // 停止信号

    // 配置
    bufferSize    int           // 缓冲区大小
    flushInterval time.Duration // 刷盘间隔
}

func (w *AsyncWAL) Start() {
    go func() {
        ticker := time.NewTicker(w.flushInterval)
        defer ticker.Stop()

        for {
            select {
            case entry := <-w.entryChan:
                w.buffer = append(w.buffer, entry)
                if len(w.buffer) >= w.bufferSize {
                    w.flush()
                }

            case <-ticker.C:
                w.flush()  // 定期刷盘

            case <-w.flushChan:
                w.flush()  // 强制刷盘

            case <-w.stopChan:
                w.flush()  // 停止前刷盘
                return
            }
        }
    }()
}
```

#### 实施步骤

1. **Day 1**: 设计 WALBatchBuffer 接口
2. **Day 2**: 实现批量写入逻辑
3. **Day 3**: 集成测试 + 性能验证

#### 验收标准
- ✅ 写入性能提升 > 50%
- ✅ 数据一致性保证（崩溃恢复正确）
- ✅ 单元测试覆盖 > 80%

---

### P0-2: 页面缓存优化

**优先级**: 🔴 高
**预期提升**: +30%~50%
**预估工作量**: 2-3 天

#### 问题描述
每次写入都分配新页面（~225 KB/op），导致内存分配开销巨大。

```go
// 当前实现
func (t *BfTree) modifyPage(pageID uint64) (*Page, error) {
    // 每次都分配新页面
    page := &Page{
        data: make([]byte, PageSize),  // 4KB 分配
        delta: make([]DeltaEntry, 0),  // 额外分配
    }
    return page, nil
}
```

#### 优化方案

**方案 1: 页面对象池**
```go
type BfTree struct {
    pagePool *sync.Pool  // 页面对象池
}

func NewBfTree(config *Config) (*BfTree, error) {
    return &BfTree{
        pagePool: &sync.Pool{
            New: func() interface{} {
                return &Page{
                    data:   make([]byte, PageSize),
                    delta:  make([]DeltaEntry, 0, 16),
                }
            },
        },
    }, nil
}

func (t *BfTree) allocPage() *Page {
    return t.pagePool.Get().(*Page)
}

func (t *BfTree) freePage(page *Page) {
    page.Reset()  // 清空数据
    t.pagePool.Put(page)  // 放回池中
}
```

**方案 2: DeltaChain 预分配**
```go
type Page struct {
    deltaChain []DeltaEntry
    capacity   int  // 预分配容量
}

func NewPage(capacity int) *Page {
    return &Page{
        deltaChain: make([]DeltaEntry, 0, capacity),  // 预分配
        capacity:   capacity,
    }
}

func (p *Page) AddDelta(key, value []byte) {
    if len(p.deltaChain) >= p.capacity {
        // 扩容策略：2 倍增长
        newCap := p.capacity * 2
        newChain := make([]DeltaEntry, len(p.deltaChain), newCap)
        copy(newChain, p.deltaChain)
        p.deltaChain = newChain
        p.capacity = newCap
    }
    p.deltaChain = append(p.deltaChain, DeltaEntry{key, value})
}
```

#### 实施步骤

1. **Day 1**: 实现 sync.Pool 页面对象池
2. **Day 2**: 优化 DeltaChain 预分配策略
3. **Day 3**: 性能测试 + 内存分析

#### 验收标准
- ✅ 内存分配减少 > 70%
- ✅ 写入性能提升 > 30%
- ✅ 内存占用稳定（无泄漏）

---

### P0-3: 写入锁优化

**优先级**: 🔴 高
**预期提升**: +20%~30%
**预估工作量**: 1-2 天

#### 问题描述
当前使用全局 RWMutex，在高并发写场景下锁竞争严重。

```go
// 当前实现
func (t *BfTree) Set(key, value []byte) error {
    t.rwLock.Lock()         // 全局锁
    defer t.rwLock.Unlock()

    // 所有写操作串行化
    return t.setInternal(key, value)
}
```

#### 优化方案

**方案 1: 页面级细粒度锁**
```go
type BfTree struct {
    pageLocks []*sync.RWMutex  // 页面锁数组
    lockMask  uint32           // 锁掩码
}

func (t *BfTree) getPageLock(pageID uint64) *sync.RWMutex {
    idx := pageID % uint64(len(t.pageLocks))
    return t.pageLocks[idx]
}

func (t *BfTree) Set(key, value []byte) error {
    // 1. 查找页面（读锁）
    t.treeLock.RLock()
    pageID, _ := t.findLeafPage(key)
    t.treeLock.RUnlock()

    // 2. 修改页面（页面级锁）
    lock := t.getPageLock(pageID)
    lock.Lock()
    defer lock.Unlock()

    return t.setInternal(pageID, key, value)
}
```

**方案 2: 写操作批量化**
```go
type WriteBatch struct {
    ops []WriteOp
    tree *BfTree
}

func (b *WriteBatch) Set(key, value []byte) error {
    b.ops = append(b.ops, WriteOp{key, value})
    return nil
}

func (b *WriteBatch) Commit() error {
    // 批量提交，减少锁次数
    b.tree.rwLock.Lock()
    defer b.tree.rwLock.Unlock()

    for _, op := range b.ops {
        if err := b.tree.setInternal(op.key, op.value); err != nil {
            return err
        }
    }
    return nil
}
```

#### 实施步骤

1. **Day 1**: 实现页面级锁方案
2. **Day 2**: 实现 WriteBatch 接口
3. **Day 2**: 集成测试

#### 验收标准
- ✅ 并发写性能提升 > 20%
- ✅ 无数据竞争（race detector 通过）

---

## 4. P1 优化项（性能提升）

### P1-1: BitmapLock 并发读优化

**优先级**: 🟡 中
**预期提升**: +100%~200%
**预估工作量**: 2-3 天

#### 问题描述
BitmapLock 在并发读场景下性能反而不如 RWMutex（338 ns vs 121 ns）。

```go
// 基准测试结果
BenchmarkBfTree_RWMutex_ConcurrentReads-12     122.9 ns/op  ⭐
BenchmarkBfTree_BitmapLock_ConcurrentReads-12   339.5 ns/op  慢 2.8x
```

#### 优化方案

**方案 1: 读写分离锁**
```go
type BitmapLock struct {
    readLocks  []atomic.Bool  // 读锁位图
    writeLock  sync.Mutex     // 写锁
    readCount  atomic.Int32   // 读计数
}

func (bl *BitmapLock) RLock(pageID uint64) {
    idx := bl.hash(pageID)
    for {
        // 快速路径：无写锁
        if !bl.writeLock.TryLock() {
            bl.writeLock.Unlock()
            bl.readCount.Add(1)
            bl.readLocks[idx].Store(true)
            return
        }
        bl.writeLock.Unlock()
        runtime.Gosched()
    }
}

func (bl *BitmapLock) RUnlock(pageID uint64) {
    idx := bl.hash(pageID)
    bl.readLocks[idx].Store(false)
    bl.readCount.Add(-1)
}
```

**方案 2: 无锁读路径**
```go
type BfTree struct {
    version atomic.Uint64  // 全局版本号
}

func (t *BfTree) Get(key []byte) ([]byte, error) {
    const MaxRetries = 3

    for retry := 0; retry < MaxRetries; retry++ {
        // 1. 获取版本号（无锁）
        v1 := t.version.Load()

        // 2. 读取数据（无锁）
        pageID, _ := t.findLeafPage(key)
        data, _ := t.pageTable.Get(pageID)

        // 3. 验证版本号
        v2 := t.version.Load()
        if v1 == v2 {
            return data, nil  // 版本一致，数据有效
        }
        // 版本变化，重试
    }

    return nil, ErrMaxRetries
}
```

#### 实施步骤

1. **Day 1**: 实现读写分离锁
2. **Day 2**: 实现无锁读路径
3. **Day 3**: 性能对比测试

#### 验收标准
- ✅ 并发读性能提升 > 100%
- ✅ 超越 RWMutex 性能

---

### P1-2: Delta Chain 合并策略优化

**优先级**: 🟡 中
**预期提升**: +20%~30%
**预估工作量**: 2-3 天

#### 问题描述
当前 Delta Chain 合并策略不够智能，导致频繁的小合并。

```go
// 当前策略：固定阈值
if len(deltaChain) > 8 {
    compact()  // 合并
}
```

#### 优化方案

**方案 1: 自适应合并阈值**
```go
type DeltaChain struct {
    entries     []DeltaEntry
    size        uint16
    compactSize int   // 动态阈值
}

func (dc *DeltaChain) ShouldCompact() bool {
    // 根据历史数据动态调整
    if len(dc.entries) >= dc.compactSize {
        // 计算合并收益
        save := dc.estimateSavings()
        if save > 0.3 {  // 节省 > 30%
            return true
        }
        // 调整阈值
        dc.compactSize = int(float64(len(dc.entries)) * 1.2)
    }
    return false
}
```

**方案 2: 增量合并**
```go
func (dc *DeltaChain) IncrementalCompact() {
    // 每次只合并部分 Delta
    const batchSize = 4
    if len(dc.entries) > batchSize*2 {
        batch := dc.entries[:batchSize]
        dc.mergeBatch(batch)
        dc.entries = dc.entries[batchSize:]
    }
}
```

#### 实施步骤

1. **Day 1**: 实现自适应阈值算法
2. **Day 2**: 实现增量合并
3. **Day 3**: 性能测试

#### 验收标准
- ✅ 合并次数减少 > 30%
- ✅ 写入性能提升 > 20%

---

## 5. P2 优化项（长期优化）

### P2-1: Mini-Page 智能提升策略

**优先级**: 🟢 低
**预期提升**: +10%~20%
**预估工作量**: 2-3 天

#### 优化方案

**自适应 Mini-Page 提升**
```go
type MiniPage struct {
    level      PageLevel
    promoteThreshold int  // 动态阈值
    accessCount atomic.Int32
}

func (mp *MiniPage) ShouldPromote() bool {
    // 根据访问频率动态调整
    count := mp.accessCount.Load()
    if count > int32(mp.promoteThreshold) {
        // 提升到下一级
        mp.level++
        mp.promoteThreshold = int(float32(count) * 1.5)
        return true
    }
    return false
}
```

---

### P2-2: 压缩算法集成

**优先级**: 🟢 低
**预期提升**: 节省 30%~90% 存储
**预估工作量**: 2-3 天

#### 优化方案

**页面自动压缩**
```go
func (p *Page) Marshal() ([]byte, error) {
    data := p.MarshalInternal()

    // 自动压缩
    compressed, err := p.tree.compressor.Compress(data)
    if err != nil {
        return data, nil  // 降级到未压缩
    }

    // 检查压缩收益
    if len(compressed) > len(data)*3/4 {
        return data, nil  // 压缩收益 < 25%，不压缩
    }

    return compressed, nil
}
```

---

## 6. 实施计划

### 6.1 优先级排序

| 优先级 | 优化项 | 预期提升 | 工作量 | 依赖 |
|--------|--------|---------|--------|------|
| **P0-1** | WAL 批量写入 | +50%~100% | 2-3 天 | - |
| **P0-2** | 页面缓存优化 | +30%~50% | 2-3 天 | - |
| **P0-3** | 写入锁优化 | +20%~30% | 1-2 天 | - |
| **P1-1** | BitmapLock 并发读 | +100%~200% | 2-3 天 | P0-3 |
| **P1-2** | DeltaChain 合并 | +20%~30% | 2-3 天 | P0-2 |
| **P2-1** | Mini-Page 提升 | +10%~20% | 2-3 天 | P1-2 |
| **P2-2** | 压缩算法集成 | 节省 30%~90% | 2-3 天 | - |

### 6.2 时间规划

| 阶段 | 时间 | 任务 | 目标 |
|------|------|------|------|
| **Week 1** | Day 1-5 | P0-1: WAL 批量写入 | 写入性能 +50% |
| **Week 2** | Day 6-10 | P0-2: 页面缓存优化 | 内存分配 -70% |
| **Week 3** | Day 11-13 | P0-3: 写入锁优化 | 并发写 +20% |
| **Week 3-4** | Day 14-20 | P1-1: BitmapLock 并发读 | 并发读 +100% |
| **Week 4-5** | Day 21-27 | P1-2: DeltaChain 合并 | 合并效率 +30% |
| **Week 6+** | Day 28+ | P2 优化项（可选） | 长期优化 |

**总计**: 4-5 周完成 P0+P1 优化

---

## 7. 验收标准

### 7.1 性能指标

| 指标 | 当前 | 目标 | 验收标准 |
|------|------|------|---------|
| **顺序写入** | 182,622 ns | < 50,000 ns | ✅ 快 3.6x |
| **随机写入** | 275,696 ns | < 80,000 ns | ✅ 快 3.4x |
| **并发写** | 70,025 ns | < 30,000 ns | ✅ 快 2.3x |
| **并发读** | 121 ns (P0) | < 100 ns | ✅ 快 20% |
| **内存分配** | 225 KB/op | < 50 KB/op | ✅ 少 78% |

### 7.2 质量指标

- ✅ 所有单元测试通过
- ✅ Race detector 检查通过
- ✅ 测试覆盖率 > 80%
- ✅ 性能回归测试通过

---

## 8. 风险评估

### 8.1 技术风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **WAL 批量写入导致数据丢失** | 高 | 低 | 崩溃恢复测试 |
| **页面池内存泄漏** | 中 | 中 | 内存泄漏检测 |
| **锁优化引入死锁** | 高 | 低 | 死锁检测工具 |
| **性能优化破坏正确性** | 高 | 中 | 完整的单元测试 |

### 8.2 进度风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **P0-1 实现复杂度超预期** | 高 | 中 | 预留缓冲时间 |
| **P1-1 效果不达预期** | 中 | 低 | 准备备选方案 |
| **整体进度延期** | 中 | 低 | 分阶段交付 |

---

## 9. 后续工作

### 9.1 立即行动

1. ✅ **创建优化分支**: `feature/bftree-performance-optimization`
2. ✅ **实现 P0-1**: WAL 批量写入
3. ✅ **性能测试**: 对比优化前后性能

### 9.2 后续迭代

1. **Week 2**: P0-2 页面缓存优化
2. **Week 3**: P0-3 写入锁优化
3. **Week 4-5**: P1 优化项
4. **Week 6+**: P2 优化项（可选）

---

## 10. 附录

### 10.1 相关文档

- [BoltDB vs BfTree 性能对比报告](../10_benchmark/2026-03-07_boltdb-vs-bftree/performance-report.md)
- [PR-089 完成报告](../06_PM/feature/2026-03-01_PR-089_m2-bftree-core_Pre.md)
- [BfTree 设计文档](../../07_spike/2026-02-21_spike_m2-storage-engine-roadmap.md)

### 10.2 性能分析工具

```bash
# CPU 性能分析
go test -bench=. -cpuprofile=cpu.prof
go tool pprof cpu.prof

# 内存性能分析
go test -bench=. -memprofile=mem.prof
go tool pprof mem.prof

# 竞争检测
go test -race ./...

# 内存泄漏检测
go test -memprofile=mem.prof -blockprofile=block.prof
```

---

**文档版本**: V1.0
**创建日期**: 2026-03-07
**作者**: AI + 人工评审
**状态**: 待评审
