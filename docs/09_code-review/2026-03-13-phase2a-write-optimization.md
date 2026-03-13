# BTree Phase 2A: 写性能优化（现有架构）

**文档类型**: 技术规划
**创建日期**: 2026-03-13
**负责人**: NexKV Team
**状态**: 📋 讨论稿
**预估工期**: 1 周
**分支**: phase2-write-optimization

**相关文档**:
- [Phase 1 性能报告](../10_benchmark/2026-03-13_btree_page_refactor/README.md)
- [纯内存性能分析](../10_benchmark/2026-03-13_btree_page_refactor/MEMORY_ONLY_ANALYSIS.md) ⭐ **必读**
- [Phase 2A 基准测试](../10_benchmark/2026-03-13-phase2-write-optimization/)

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
Phase 2A (1 周):
├── Buffer Pool: 2.5x QPS ↑
├── 异步刷盘: 2.0x QPS ↑
├── 序列化优化: 1.3x QPS ↑
└── 预期: 3,500+ QPS (2.0x+ 提升)

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
├── 序列化优化: 10,900 QPS (1.3x)
└── 预期: 3,500+ QPS (保守 2.0x+ 提升)

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
Set QPS          1,696     3,500+         2.0x+
Set P99 延迟     15ms      <8ms            2x
吞吐量 (MB/s)    0.8       1.6             2x
内存分配/op      5.5KB     1KB             ↓82%
GC CPU 时间      15.23%    3%              ↓80%
```

**功能目标**:
- ✅ Buffer Pool（减少内存分配）
- ✅ 异步刷盘优化（启用现有框架）
- ✅ 序列化优化（unsafe 直接内存操作）

**不做什么**（本次优化范围外）:
- ❌ 批量 Set API（不实现 SetBatch）
- ❌ WAL（不实现崩溃恢复）
- ❌ 改变存储格式（向后兼容）
- ❌ 引入新的依赖

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

### 纯内存性能上限分析

**对比测试数据** (关闭磁盘持久化):
```
┌──────────────────────────────────────────────────────┐
│ 操作           纯内存         磁盘 I/O       差距     │
├──────────────────────────────────────────────────────┤
│ Set QPS        1430 万       1,696         8430x    │
│ Set 延迟       69.95 ns      478.6 μs      6848x    │
│ Get QPS        1.53 亿       2593 万        5.9x     │
│ Get 延迟       6.53 ns       38.56 ns       5.9x     │
└──────────────────────────────────────────────────────┘

纯内存 CPU 时间分布:
├── mallocgc:       7.89%   ← 内存分配（主要瓶颈）
├── mallocgcTiny:   6.04%
├── nextFreeFast:   6.04%
├── LeafPage.Insert: 5.20%   ← 业务逻辑
├── LeafPage.search: 4.03%
└── 其他:           70.80%
```

**关键发现**:
- 🔥 **磁盘 I/O 是绝对瓶颈**: Set 操作性能差距 8430x
- ⚡ **纯内存性能已接近硬件极限**: 搜索仅 6.53 ns（~20 CPU 周期）
- 💡 **Phase 2A 优化目标**: 从 1,696 QPS → 3,500+ QPS（仍有 4086x 提升空间到纯内存）

**性能天花板分析**:
```
优化路径:
当前 (磁盘):      1,696 QPS   ← Phase 1 基线
  ↓ 异步刷盘
  ↓ Buffer Pool
  ↓ 序列化优化
Phase 2A 目标:    3,500+ QPS  ← 2.0x+ 提升
  ↓ WAL + Group Commit
Phase 2B 目标:   10,000+ QPS  ← 6x 提升
  ↓ 纯内存 (无持久化)
理论极限:     14,300,000 QPS  ← 8430x 提升
```

**结论**: Phase 2A 优化后仍远低于纯内存性能上限，后续优化空间巨大

### 快速优化机会

**聚焦 Set 操作优化**（本次范围）:
```
优化项              预期提升    实施难度    工作量
──────────────────────────────────────────────
异步刷盘（已有框架） 2.0x       低         2 天
Buffer Pool         2.5x       低         3 天
序列化优化           1.3x       中         2 天
──────────────────────────────────────────────
总计                6.5x       -          7 天
实际 (保守估计)     2.0x       -          7 天
```

**不在本次范围内**:
- ❌ 批量 Set API（留给后续考虑）
- ❌ WAL 实现（留在 Phase 2B）
- ❌ Get 操作优化（聚焦 Set）

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
    "fmt"
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

    fmt.Printf("Buffer Pool Stats:\n")
    fmt.Printf("  Total:      %d\n", total)
    fmt.Printf("  Hits:       %d (%.2f%%)\n", hits, hitRate)
    fmt.Printf("  Misses:     %d (%.2f%%)\n", misses, 100-hitRate)
    fmt.Printf("  Allocations:%d\n", pool.stats.Allocations.Load())
    fmt.Printf("  In Pool:    %d\n", pool.stats.InPool.Load())
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

### 3. 序列化优化（可选）

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

## 📅 实施计划（1 周 = 5 个工作日）

### Day 1-2: 异步刷盘 + Buffer Pool

**启用异步刷盘**:
- [ ] 检查现有异步刷盘框架
- [ ] 启用异步刷盘配置
- [ ] 测试数据安全性

**实现 Buffer Pool**:
- [ ] 创建 `BufferPool` 结构体
- [ ] 实现 4 级 pool (4KB, 8KB, 16KB, 32KB)
- [ ] 实现 `Get()` 和 `Put()` 方法
- [ ] 集成到 `PageSerializer`

**Day 1 结束**:
- [ ] 异步刷盘启用
- [ ] Buffer Pool 基础实现完成

### Day 3: 序列化优化（可选）

**unsafe 优化**:
- [ ] 替换 `binary.PutUvarint` 为 `binary.LittleEndian`
- [ ] 使用 `unsafe.Pointer` 直接操作内存
- [ ] 精确预分配 buffer 大小

**Day 3 结束**:
- [ ] 序列化性能测试通过

### Day 4-5: 性能测试和调优

**基准测试**:
- [ ] 运行完整 benchmark suite
- [ ] 生成 CPU/Memory profiling
- [ ] 对比基线数据

**调优**:
- [ ] 调整 Buffer Pool 大小
- [ ] 调整异步刷盘批量大小
- [ ] 调整刷盘间隔

**Day 5 结束**:
- [ ] 性能目标达成
- [ ] 测试报告完成

### 可选：额外时间（测试和文档）

如果有额外时间，可以进行：
- [ ] 并发压力测试
- [ ] 长时间运行测试
- [ ] 性能测试报告
- [ ] API 使用文档
- [ ] Code Review

---

## ✅ 验收标准

### 功能验收

- [ ] **异步刷盘**: 正确启用，无数据丢失
- [ ] **Buffer Pool**: 命中率 >80%，无内存泄露
- [ ] **序列化优化**: 性能提升达标
- [ ] **向后兼容**: 现有 API 无需修改

### 性能验收

```
基准测试目标:
BenchmarkBTree_Set:        1,696 → 3,500+  (2.0x)
BenchmarkBTree_Set_P99:    15ms → <8ms      (2x)

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
Set QPS:    3,500+ (2.0x+ 提升)
Set P99:    <8ms (2x 提升)
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
┌────────────────────────────────────────────────────────────┐
│ 阶段               QPS           P99       内存分配   GC CPU  │
├────────────────────────────────────────────────────────────┤
│ Phase 1 (当前)     1,696         15ms      5.5KB      15.23% │
│ Phase 2A (目标)    3,500+        <8ms      1KB        3%     │
│ Phase 2B (WAL)    10,000+        <2ms      1KB        3%     │
│ 纯内存 (理论)   14,300,000    69.95 ns    <1KB       7.89%  │ ⭐
├────────────────────────────────────────────────────────────┤
│ 提升 (1→2A)        2.0x+         2x       ↓82%      ↓80%    │
│ 提升 (2A→2B)       2.9x          4x       持平       持平     │
│ 提升 (1→2B)        6x           7.5x      ↓82%      ↓80%    │
│ 提升 (1→内存)   8430x         214680x    ↓82%      ↓48%    │
└────────────────────────────────────────────────────────────┘

⭐ 纯内存数据来源: MEMORY_ONLY_ANALYSIS.md
   说明: 纯内存无持久化，展示理论性能上限
```

---

## 💡 讨论要点

1. **优化顺序**: 是否同意先优化现有架构再实施 WAL？
2. **性能目标**: Phase 2A 的 3,500+ QPS (2.0x+) 目标是否合理？
3. **实施时间**: 1 周是否足够完成 Phase 2A？
4. **风险评估**: 异步刷盘是否会影响数据一致性？
5. **Phase 2B**: 是否需要在 Phase 2A 完成后再评估？
6. **性能天花板**: 纯内存性能 1430 万 QPS，Phase 2A 目标是否太保守？

---

**文档版本**: v1.1
**分支**: phase2-write-optimization
**最后更新**: 2026-03-13

## 📝 更新日志

**v1.1** (2026-03-13):
- ✅ 添加纯内存性能上限分析
- ✅ 引用 MEMORY_ONLY_ANALYSIS.md 详细数据
- ✅ 更新优化对比表，标注理论极限 (1430 万 QPS)
- ✅ 添加性能天花板讨论点

**v1.0** (2026-03-13):
- 初始版本

🎯 **渐进式优化：先优化现有架构，再考虑架构升级！**
