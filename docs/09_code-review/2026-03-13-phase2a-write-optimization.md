# BTree Phase 2A: 写性能优化（现有架构）

**文档类型**: 技术规划
**创建日期**: 2026-03-13
**负责人**: NexKV Team
**状态**: 📋 讨论稿
**预估工期**: 1-2 周
**分支**: phase2-write-optimization

---

## 📋 为什么先优化现有架构？

### 🤔 问题思考

**原计划**:
```
Phase 1 → Phase 2 (WAL + 批处理) → Phase 3
```

**调整后的计划**:
```
Phase 1 → Phase 2A (写性能优化) → Phase 2B (WAL) → Phase 3
```

### ✅ 优势分析

#### 1. **渐进式优化**（更安全）
```
原计划风险:
├── WAL 架构复杂度高
├── 引入新的故障点
├── 难以定位性能瓶颈
└── 回滚成本高

渐进式优化:
├── 先优化现有架构
├── 建立性能基线
├── 验证优化效果
└── 再考虑架构升级
```

#### 2. **快速见效**（更务实）
```
Phase 2A (1-2 周):
├── Buffer Pool: 2.5x QPS ↑
├── 异步刷盘: 2.0x QPS ↑
├── 批量 API: 1.5x QPS ↑
└── 预期: 5,000+ QPS (3x 提升)

Phase 2B (2-3 周):
├── WAL 实现
├── Group Commit
└── 预期: 10,000+ QPS (6x 提升)
```

#### 3. **性能基线**（更科学）
```
优化前基线: 1,696 QPS

Phase 2A 后:
├── Buffer Pool: 4,200 QPS (2.5x)
├── 异步刷盘: 8,400 QPS (2.0x)
└── 批量 API: 12,600 QPS (1.5x)

Phase 2B 后:
└── WAL + Group Commit: 15,000+ QPS

每步都有明确的性能提升数据！
```

---

## 🎯 Phase 2A 目标

### 总体目标

**性能目标**:
```
指标              当前       Phase 2A 目标   提升
──────────────────────────────────────────────
Set QPS          1,696     5,000+         3x
Set P99 延迟     15ms      <5ms            3x
吞吐量 (MB/s)    0.8       2.5             3x
内存分配/op      5.5KB     1KB             ↓82%
GC CPU 时间      15.23%    3%              ↓80%
```

**功能目标**:
- ✅ Buffer Pool（减少内存分配）
- ✅ 异步刷盘优化（启用现有框架）
- ✅ 批量写入 API（SetBatch）
- ✅ 序列化优化（unsafe 直接内存操作）

### 不做什么（Phase 2B）

- ❌ 不实现 WAL（留在 Phase 2B）
- ❌ 不实现崩溃恢复（留在 Phase 2B）
- ❌ 不改变存储格式（向后兼容）
- ❌ 不引入新的依赖

---

## 📊 Phase 1 性能基线回顾

### 当前性能瓶颈

```
Set 操作 CPU 时间分布 (pprof):
├── fsync:          39.87%  ← 磁盘 I/O 瓶颈
├── mallocgc:       15.23%  ← 内存分配压力
├── PageSerializer: 8.45%   ← 序列化开销
├── 搜索路径:        7.89%
└── 其他:           28.56%

内存分配 (memprof):
├── PageSerializer: 85%     ← 主要来源
├── WAL Record:      10%     ← 暂不优化
├── 搜索路径:        3%
└── 其他:           2%
```

### 快速优化机会

**高价值低成本优化**:
```
优化项              预期提升    实施难度    工作量
──────────────────────────────────────────────
异步刷盘（已有框架） 2.0x       低         2 天
Buffer Pool         2.5x       低         3 天
批量 Set API         1.5x       中         3 天
序列化优化           1.3x       中         2 天
──────────────────────────────────────────────
总计                9.8x       -          10 天
实际 (保守估计)     3.0x       -          10 天
```

---

## 🚀 优化方案

### 1. 启用异步刷盘（最快见效）

#### 当前状态分析

**现有代码**（已有异步框架）:
```go
// page_persist.go
type PageManager struct {
    dirtyPages     chan *Page         // ✅ 已有
    flushInterval  time.Duration      // ✅ 已有 (100ms)
    flushBatchSize int                // ✅ 已有 (16)
    stopFlush      context.CancelFunc // ✅ 已有
    flushWg        sync.WaitGroup     // ✅ 已有
}

// ✅ 已实现但未启用
func (pm *PageManager) backgroundFlush(ctx context.Context) {
    defer pm.flushWg.Done()
    batch := make([]*Page, 0, pm.flushBatchSize)
    ticker := time.NewTicker(pm.flushInterval)

    for {
        select {
        case page := <-pm.dirtyPages:
            batch = append(batch, page)
            if len(batch) >= pm.flushBatchSize {
                pm.flushBatch(batch)
                batch = batch[:0]
            }
        case <-ticker.C:
            if len(batch) > 0 {
                pm.flushBatch(batch)
                batch = batch[:0]
            }
        case <-ctx.Done():
            return
        }
    }
}
```

**问题**: 当前 `WritePage` 使用同步写入
```go
// 当前实现（同步）
func (pm *PageManager) WritePage(page *Page) error {
    if !page.IsDirty() {
        return nil
    }

    // ❌ 同步写入
    return pm.syncWritePage(page)
}

func (pm *PageManager) syncWritePage(page *Page) error {
    // 直接调用 flushPage，每次都 fsync
    return pm.flushPage(page)
}
```

#### 优化方案

**启用异步写入**:
```go
// 优化后的实现
func (pm *PageManager) WritePage(page *Page) error {
    if !page.IsDirty() {
        return nil
    }

    // ✅ 发送到异步 channel
    select {
    case pm.dirtyPages <- page:
        return nil  // 快速返回
    default:
        // Channel 满，fallback 到同步
        return pm.syncWritePage(page)
    }
}

// 测试模式：使用同步写入
// 生产模式：使用异步写入
var AsyncFlushEnabled = false // 可配置

func (pm *PageManager) WritePage(page *Page) error {
    if !page.IsDirty() {
        return nil
    }

    if AsyncFlushEnabled {
        // 异步模式（生产）
        select {
        case pm.dirtyPages <- page:
            return nil
        default:
            return pm.syncWritePage(page)  // Fallback
        }
    } else {
        // 同步模式（测试）
        return pm.syncWritePage(page)
    }
}
```

**配置化控制**:
```go
// config/btree.yaml
btree:
  async_flush:
    enabled: true           # 生产环境启用
    batch_size: 32           # 批量大小（16 → 32）
    interval: 100ms          # 刷盘间隔
    fallback_on_full: true   # Channel 满时 fallback
```

#### 预期效果

**性能提升**:
```
优化前:
每次 Set: 1 次 fsync (10ms)
100 次 Set: 1,000ms
QPS: 1,696

优化后:
100 次 Set: 1 次 fsync (Group Commit)
实际: 根据批量大小调整

批量大小 16:
├── 每 16 次 Set: 1 次 fsync
├── 理论 QPS: 16 * 1000ms / 10ms = 1,600
└── 考虑并发: ~3,000 QPS

批量大小 32:
├── 每 32 次 Set: 1 次 fsync
├── 理论 QPS: 32 * 1000ms / 10ms = 3,200
└── 考虑并发: ~5,000 QPS

预期提升: 1,696 → 3,500+ (2.0x)
```

#### 实施计划

**Day 1: 启用异步刷盘**
- [ ] 修改 `WritePage` 启用异步写入
- [ ] 添加配置项（AsyncFlushEnabled）
- [ ] 单元测试：异步写入正确性
- [ ] 集成测试：并发写入

**Day 2: 调优和监控**
- [ ] 调整批量大小（16 → 32 → 64）
- [ ] 调整刷盘间隔（100ms → 50ms）
- [ ] 添加监控指标（fsync 次数/间隔）
- [ ] 性能测试：对比优化前后

---

### 2. Buffer Pool 实现

#### 当前问题

**内存分配热点**:
```go
// 每次 Set 都分配新 buffer
func (ps *PageSerializer) Serialize(page *Page) ([]byte, error) {
    buf := make([]byte, PageSize)  // ← 每次分配 4KB
    // ... 序列化
    return buf, nil
}  // ← buffer 丢弃，触发 GC
```

**内存分配统计**:
```
每次 Set 分配:
├── Page buffer:      4KB
├── 临时 buffer:      1KB
└── 总计:             5KB

1000 次 Set:
├── 总分配: 5MB
├── GC 频率: 高
└── CPU 时间: 15.23%
```

#### 优化方案

**Buffer Pool 设计**:
```go
// buffer_pool.go
package btree

import (
    "sync"
    "sync/atomic"
)

var (
    // 全局 Buffer Pool（单例）
    globalBufferPool = NewBufferPool(BufferPoolConfig{
        MaxCached: 1000,  // 每个级别最多 1000 个
    })
)

type BufferPool struct {
    // 四级 pool（4KB, 8KB, 16KB, 32KB）
    pools [4]sync.Pool

    // 统计信息
    stats PoolStats

    // 配置
    config BufferPoolConfig
}

type PoolStats struct {
    Hits        atomic.Int64  // 命中次数
    Misses      atomic.Int64  // 未命中次数
    Allocations atomic.Int64  // 新分配次数
    InPool      atomic.Int64  // 当前在池中的 buffer 数
}

type BufferPoolConfig struct {
    MaxCached int  // 每个级别最多缓存的 buffer 数量
}

func NewBufferPool(config BufferPoolConfig) *BufferPool {
    pool := &BufferPool{
        config: config,
    }

    // 初始化各级 pool
    for i := 0; i < 4; i++ {
        size := int32(4096 << uint(i))  // 4KB, 8KB, 16KB, 32KB
        pool.pools[i] = sync.Pool{
            New: func() interface{} {
                return make([]byte, size)
            },
        }
    }

    return pool
}

// Get 获取 buffer
func (pool *BufferPool) Get(size int32) []byte {
    level := pool.getLevel(size)

    // 尝试从 pool 获取
    if buf, ok := pool.pools[level].Get().([]byte); ok {
        if cap(buf) >= size {
            pool.stats.Hits.Add(1)
            pool.stats.InPool.Add(-1)
            return buf[:size]  // 重置长度
        }
        // 容量不足，丢弃
    }

    // Pool miss，分配新的
    pool.stats.Misses.Add(1)
    pool.stats.Allocations.Add(1)

    actualSize := pool.roundUpSize(size)
    return make([]byte, actualSize)[:size]
}

// Put 归还 buffer
func (pool *BufferPool) Put(buf []byte) {
    if buf == nil || cap(buf) == 0 {
        return
    }

    level := pool.getLevel(int32(cap(buf)))

    // 归还到对应的 pool
    pool.pools[level].Put(buf)
    pool.stats.InPool.Add(1)
}

// getLevel 确定级别
func (pool *BufferPool) getLevel(size int32) int {
    switch {
    case size <= 4*1024:
        return 0  // 4KB
    case size <= 8*1024:
        return 1  // 8KB
    case size <= 16*1024:
        return 2  // 16KB
    default:
        return 3  // 32KB
    }
}

// roundUpSize 向上取整到标准大小
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

// PrintStats 打印统计信息
func (pool *BufferPool) PrintStats() {
    hits := pool.stats.Hits.Load()
    misses := pool.stats.Misses.Load()
    total := hits + misses

    if total == 0 {
        println("Buffer Pool: No data yet")
        return
    }

    hitRate := float64(hits) / float64(total) * 100

    printf("Buffer Pool Stats:\n")
    printf("  Total:      %d\n", total)
    printf("  Hits:       %d (%.2f%%)\n", hits, hitRate)
    printf("  Misses:     %d (%.2f%%)\n", misses, 100-hitRate)
    printf("  Allocations:%d\n", pool.stats.Allocations.Load())
    printf("  In Pool:    %d\n", pool.stats.InPool.Load())
}

// GetGlobalBufferPool 获取全局 pool
func GetGlobalBufferPool() *BufferPool {
    return globalBufferPool
}
```

**集成到 PageSerializer**:
```go
// page_serializer.go
func (ps *PageSerializer) Serialize(page *Page) ([]byte, error) {
    // ✅ 从全局 pool 获取 buffer
    buf := GetGlobalBufferPool().Get(PageSize)

    // 序列化
    offset := 0
    offset += binary.PutUvarint(buf[offset:], uint64(page.ID))
    offset += binary.PutUvarint(buf[offset:], uint64(page.Type))
    // ... 更多字段

    return buf[:offset], nil
}

func (ps *PageSerializer) Deserialize(buf []byte) (*Page, error) {
    // ✅ 使用后归还 buffer
    defer GetGlobalBufferPool().Put(buf)

    // 解序列化
    page := &Page{}
    offset := 0

    id, n := binary.Uvarint(buf[offset:])
    page.ID = model.PageID(id)
    offset += n

    // ... 更多字段

    return page, nil
}
```

#### 预期效果

**性能提升**:
```
优化前:
每次 Set: 分配 5KB buffer
1000 次 Set: 5MB 分配
GC CPU: 15.23%

优化后 (命中率 80%):
每次 Set: 80% 复用，20% 分配
1000 次 Set: 1MB 分配 (↓80%)
GC CPU: 15.23% → 3% (↓80%)

QPS 提升: 额外 2.5x
```

#### 实施计划

**Day 1: Buffer Pool 基础实现**
- [ ] 实现 `BufferPool` 结构
- [ ] 实现 `Get/Put` 方法
- [ ] 单元测试：Put/Get 循环

**Day 2: 集成到 PageSerializer**
- [ ] 修改 `Serialize` 使用 pool
- [ ] 修改 `Deserialize` 归还 buffer
- [ ] 集成测试：序列化/反序列化

**Day 3: 统计和调优**
- [ ] 实现统计信息收集
- [ ] 添加 `PrintStats()` 方法
- [ ] 性能测试：验证分配减少

---

### 3. 批量 Set API

#### 当前问题

**逐条写入效率低**:
```go
// 当前 API: 逐条写入
for i := 0; i < 1000; i++ {
    tree.Set(ctx, key(i), value(i))
    // 每次都触发:
    // 1. 搜索路径
    // 2. Page 加载/分裂
    // 3. 序列化
    // 4. fsync (10ms)
}
```

#### 优化方案

**批量 API 设计**:
```go
// 批量 Set API
func (tree *BTree) SetBatch(ctx context.Context, kvs []KeyValue) error {
    if len(kvs) == 0 {
        return nil
    }
    if len(kvs) > MaxBatchSize {
        return ErrBatchTooLarge
    }

    // Step 1: 按 Page 分组
    pageGroups := tree.groupKeysByPage(kvs)

    // Step 2: 批量处理每个 Page
    for pageID, kvs := range pageGroups {
        if err := tree.setPageBatch(ctx, pageID, kvs); err != nil {
            return err
        }
    }

    // Step 3: 批量刷盘（由异步刷盘处理）
    return nil
}

// 按 Page 分组
func (tree *BTree) groupKeysByPage(kvs []KeyValue) map[model.PageID][]KeyValue {
    groups := make(map[model.PageID][]KeyValue)

    for _, kv := range kvs {
        // 搜索 key 所在的 Page
        path := tree.searchPath(kv.Key)
        if len(path) > 0 {
            leafInfo := path[len(path)-1]
            pageID := leafInfo.GetPos()
            groups[pageID] = append(groups[pageID], kv)
        }
        // TODO: 未找到 Page，需要处理
    }

    return groups
}

// 批量设置单个 Page
func (tree *BTree) setPageBatch(ctx context.Context, pageID model.PageID, kvs []KeyValue) error {
    // 加载 Page
    pageInfo, err := tree.loadPage(pageID)
    if err != nil {
        return err
    }

    page, ok := pageInfo.GetPage().(*LeafPage)
    if !ok {
        return ErrNotLeafPage
    }

    // 批量插入
    for _, kv := range kvs {
        if _, err := page.Insert(kv.Key, kv.Value); err != nil {
            return err
        }
    }

    // 标记脏页（异步刷盘）
    page.MarkDirty()
    pageInfo.MarkDirty()

    return nil
}
```

**使用示例**:
```go
// 示例 1: 批量导入
kvs := make([]KeyValue, 1000)
for i := 0; i < 1000; i++ {
    kvs[i] = KeyValue{
        Key:   []byte(fmt.Sprintf("key-%d", i)),
        Value: []byte(fmt.Sprintf("value-%d", i)),
    }
}

// ✅ 一次性批量写入
if err := tree.SetBatch(ctx, kvs); err != nil {
    log.Fatal(err)
}

// 示例 2: 分批处理大数据集
func importData(tree *BTree, data []KeyValue) error {
    batchSize := 100

    for i := 0; i < len(data); i += batchSize {
        end := i + batchSize
        if end > len(data) {
            end = len(data)
        }

        batch := data[i:end]
        if err := tree.SetBatch(ctx, batch); err != nil {
            return err
        }
    }

    return nil
}
```

#### 预期效果

**性能提升**:
```
优化前 (逐条写入):
1000 次 Set:
├── 搜索路径: 1000 次
├── Page 加载: 1000 次
├── 序列化: 1000 次
├── fsync: 1000 次 (10s)
└── 总时间: ~10 秒

优化后 (批量写入):
1000 次 Set (10 个 Page, 每个 Page 100 keys):
├── 搜索路径: 1000 次 (无法减少)
├── Page 加载: 10 次 (↓99%)
├── 序列化: 10 次 (↓99%)
├── fsync: 10 次 (↓99%, 100ms)
└── 总时间: ~1.2 秒

QPS 提升: 额外 1.5x
```

#### 实施计划

**Day 1: 基础 API 实现**
- [ ] 实现 `SetBatch()` 方法
- [ ] 实现 `groupKeysByPage()` 辅助方法
- [ ] 单元测试：批量写入正确性

**Day 2: 边界情况处理**
- [ ] 处理 Key 跨 Page 的情况
- [ ] 处理 Page 满需要分裂的情况
- [ ] 处理部分失败的情况
- [ ] 集成测试：复杂场景

**Day 3: 性能测试**
- [ ] Benchmark: SetBatch vs Set
- [ ] 不同批量大小的性能
- [ ] 寻找最优批量大小

---

### 4. 序列化优化（可选）

#### 优化方案

**使用 unsafe 直接操作**:
```go
// page_serializer.go
import (
    "encoding/binary"
    "unsafe"
)

func (ps *PageSerializer) Serialize(page *Page) ([]byte, error) {
    buf := GetGlobalBufferPool().Get(PageSize)

    // ✅ 使用 unsafe 直接操作内存
    header := (*[PageSize]byte)(unsafe.Pointer(&buf[0]))

    // 直接写入（避免 offset 计算）
    binary.LittleEndian.PutUint64(buf[0:8], uint64(page.ID))
    binary.LittleEndian.PutUint64(buf[8:16], uint64(page.Version))
    binary.LittleEndian.PutUint64(buf[16:24], uint64(page.Type))

    // Key-Value 对（从 offset 24 开始）
    offset := 24
    if leaf, ok := page.(*LeafPage); ok {
        for i := 0; i < len(leaf.keys); i++ {
            // Key size
            keyLen := uint16(len(leaf.keys[i]))
            binary.LittleEndian.PutUint16(buf[offset:offset+2], keyLen)
            offset += 2

            // Key data
            copy(buf[offset:offset+int(keyLen)], leaf.keys[i])
            offset += int(keyLen)

            // Value size
            valLen := uint16(len(leaf.values[i]))
            binary.LittleEndian.PutUint16(buf[offset:offset+2], valLen)
            offset += 2

            // Value data
            copy(buf[offset:offset+int(valLen)], leaf.values[i])
            offset += int(valLen)
        }
    }

    return buf[:offset], nil
}
```

**预分配 Key-Value 大小**:
```go
// 优化前: 动态扩展 slice
func serializeKeyValue(kv KeyValue) []byte {
    var buf []byte
    buf = binary.PutUvarint(buf, uint64(len(kv.Key)))
    buf = append(buf, kv.Key...)
    buf = binary.PutUvarint(buf, uint64(len(kv.Value)))
    buf = append(buf, kv.Value...)
    return buf  // ← 多次重新分配
}

// 优化后: 精确预分配
func serializeKeyValue(kv KeyValue) []byte {
    size := 2 + len(kv.Key) + 2 + len(kv.Value)  // 精确计算
    buf := GetGlobalBufferPool().Get(int32(size))

    offset := 0
    binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(kv.Key)))
    offset += 2
    copy(buf[offset:], kv.Key)
    offset += len(kv.Key)

    binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(kv.Value)))
    offset += 2
    copy(buf[offset:], kv.Value)

    return buf
}
```

#### 预期效果

```
序列化开销: 8.45% → 5% CPU
函数调用: 100 次 → 10 次
QPS 提升: 额外 1.3x
```

---

## 📅 实施计划（1-2 周）

### Week 1: 核心优化

**Day 1-2: 异步刷盘 + Buffer Pool**
- [ ] 启用异步刷盘框架
- [ ] 实现 Buffer Pool
- [ ] 集成测试

**Day 3-4: 批量 Set API**
- [ ] 实现 `SetBatch()` 方法
- [ ] 边界情况处理
- [ ] 性能测试

**Day 5: 序列化优化（可选）**
- [ ] unsafe 直接内存操作
- [ ] 预分配 buffer 大小
- [ ] 性能测试

**Day 6-7: 性能测试和调优**
- [ ] 运行完整 benchmark
- [ ] 生成 profiling 报告
- [ ] 调优参数

### Week 2: 测试和文档

**Day 1-3: 集成测试**
- [ ] 并发压力测试
- [ ] 长时间运行测试
- [ ] 崩溃恢复测试（无 WAL）

**Day 4-5: 文档编写**
- [ ] 性能测试报告
- [ ] API 使用文档
- [ ] 配置说明

**Day 6-7: Code Review**
- [ ] 团队 Code Review
- [ ] 修复问题
- [ ] 准备合并

---

## ✅ 验收标准

### 功能验收

- [ ] **异步刷盘**: 正确启用，无数据丢失
- [ ] **Buffer Pool**: 命中率 >80%，无内存泄露
- [ ] **SetBatch API**: 功能正确，支持跨 Page
- [ ] **向后兼容**: 现有 API 无需修改

### 性能验收

```
基准测试目标:
BenchmarkBTree_Set:        1,696 → 3,500+  (2.0x)
BenchmarkBTree_Set_P99:    15ms → <5ms      (3x)
BenchmarkBTree_SetBatch_100:  新增   <50ms
BenchmarkBTree_SetBatch_1000: 新增   <500ms

内存分配:
├── 每次操作分配: 5KB → 1KB (↓82%)
├── GC CPU 时间: 15.23% → 3% (↓80%)
└── Buffer Pool 命中率: >80%
```

### 测试验收

```bash
# 单元测试
go test ./internal/infrastructure/storage/btree/... -v
✅ 所有测试通过

# 并发测试
go test ./internal/infrastructure/storage/btree/... -race -v
✅ 无数据竞争

# 基准测试
go test -bench=. -benchmem ./internal/infrastructure/storage/btree/... > report.txt
✅ 性能提升达标

# 覆盖率
go test -coverprofile=coverage.out ./internal/infrastructure/storage/btree/...
go tool cover -func=coverage.out | grep total
✅ 覆盖率 >85%
```

---

## 🎯 Phase 2B 预览（WAL 实现）

### Phase 2A 完成后

**预期性能基线**:
```
Set QPS:    5,000+ (3x 提升)
Set P99:    <5ms (3x 提升)
内存分配:  ↓82%
GC 压力:   ↓80%
```

### Phase 2B 目标

**在 Phase 2A 基础上**:
```
Set QPS:    10,000+ (6x 总提升)
Set P99:    <2ms (7.5x 总提升)
WAL 支持:  ✅
崩溃恢复:   ✅
```

### Phase 2B 关键技术

- **WAL 文件格式**: Header + Records + Checksum
- **顺序写优化**: 追加写，mmap 可选
- **Checkpoint**: 定期快照
- **崩溃恢复**: Replay WAL

---

## 📊 优化对比表

```
┌────────────────────────────────────────────────────┐
│ 阶段           QPS      P99     内存分配   GC CPU    │
├────────────────────────────────────────────────────┤
│ Phase 1       1,696    15ms    5.5KB      15.23%   │
│ Phase 2A      5,000+   <5ms    1KB        3%       │
│ Phase 2B     10,000+   <2ms    1KB        3%       │
├────────────────────────────────────────────────────┤
│ 提升 (1→2A)   3x       3x      ↓82%      ↓80%     │
│ 提升 (2A→2B)  2x       2.5x    持平       持平      │
│ 提升 (1→2B)   6x       7.5x    ↓82%      ↓80%     │
└────────────────────────────────────────────────────┘
```

---

## 💡 讨论要点

1. **优化顺序**: 是否同意先优化现有架构再实施 WAL？
2. **性能目标**: Phase 2A 的 5,000 QPS 目标是否合理？
3. **实施时间**: 1-2 周是否足够完成 Phase 2A？
4. **风险评估**: 异步刷盘是否会影响数据一致性？
5. **Phase 2B**: 是否需要在 Phase 2A 完成后再评估？

---

**文档版本**: v1.0
**分支**: phase2-write-optimization
**最后更新**: 2026-03-13

🎯 **渐进式优化：先优化现有架构，再考虑架构升级！**
