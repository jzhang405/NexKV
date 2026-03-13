# BTree Phase 2A: 内存优化（Buffer Pool + 序列化）

**文档类型**: 技术规划
**创建日期**: 2026-03-13
**负责人**: NexKV Team
**状态**: 📋 讨论稿
**预估工期**: 1 周
**分支**: phase2-write-optimization
**优化范围**: 仅内存优化，不涉及磁盘 I/O

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
Phase 2A (1 周) - 仅内存优化:
├── Buffer Pool: 减少内存分配，降低 GC 压力
├── 序列化优化: unsafe 直接内存操作
└── 预期: 内存分配 ↓82%, GC CPU ↓80%

注意: 磁盘 I/O 优化（异步刷盘、WAL）留在后续阶段
```

#### 3. **性能基线**（更科学）
```
内存优化基线 (纯内存测试):
├── 当前内存分配: 5.5 KB/op
├── GC CPU 时间: 15.23%
└── 序列化开销: 8.45%

Phase 2A 目标:
├── Buffer Pool: 减少分配至 1 KB/op (↓82%)
├── GC CPU: 降低至 3% (↓80%)
└── 序列化: 优化至 5% CPU (↓41%)

注意: QPS 提升主要来自减少 GC 暂停，磁盘 I/O 瓶颈保持不变
```

---

## 🎯 Phase 2A 目标

### 总体目标

**性能目标**（仅内存优化）:
```
指标              当前       Phase 2A 目标   提升
──────────────────────────────────────────────
内存分配/op      5.5KB     1KB             ↓82%
GC CPU 时间      15.23%    3%              ↓80%
序列化 CPU       8.45%     <5%             ↓41%
Buffer Pool 命中率 N/A      >80%           新增
```

**功能目标**:
- ✅ Buffer Pool（减少内存分配）
- ✅ 序列化优化（unsafe 直接内存操作）

**不做什么**（本次优化范围外）:
- ❌ 磁盘 I/O 优化（异步刷盘、WAL、Group Commit）
- ❌ 批量 Set API（不实现 SetBatch）
- ❌ 改变存储格式（向后兼容）
- ❌ 引入新的依赖

### 为什么不优化磁盘 I/O？

**理由**:
1. **磁盘 I/O 瓶颈独立**: fsync 占用 39.87% CPU，与内存优化无关
2. **渐进式优化**: 先优化内存部分，建立基线，再优化磁盘
3. **风险控制**: 磁盘 I/O 优化涉及数据安全，需要更严格的测试
4. **Phase 2B 专门处理**: 异步刷盘、WAL、Group Commit 留在 Phase 2B

**Phase 2B 范围**:
- 异步刷盘（Group Commit）
- WAL 实现
- 崩溃恢复

---

## 📊 Phase 1 性能基线回顾

### 内存瓶颈分析

**内存分配统计** (memprof):
```
每次 Set 操作分配:
├── Page buffer:      4KB     ← 序列化缓冲区
├── 临时 buffer:      1KB     ← 临时变量
├── 搜索路径:         256B    ← 路径栈
├── Key-Value 扩展:   128B    ← Slice 扩展
└── 总计:             5.5KB

1000 次 Set:
├── 总分配: 5.5MB
├── GC 频率: 高
└── GC CPU: 15.23%
```

**序列化开销** (CPU profile):
```
PageSerializer 序列化:
├── binary.PutUvarint: 多次调用  ← 可优化
├── Slice append/扩展: 100+ 次   ← 可优化
├── Offset 计算:      频繁       ← 可优化
└── CPU 占用:         8.45%
```

### 纯内存参考数据

**说明**: 以下数据来自关闭磁盘持久化的纯内存测试，仅作为内存优化参考上限。

**纯内存性能** (关闭磁盘):
```
┌──────────────────────────────────────────────┐
│ 操作           延迟        QPS               │
├──────────────────────────────────────────────┤
│ Set (纯内存)   69.95 ns    1430 万/秒        │
│ Get (纯内存)   6.53 ns     1.53 亿/秒        │
└──────────────────────────────────────────────┘

纯内存 CPU 时间分布:
├── mallocgc:       7.89%   ← 内存分配（主要瓶颈）
├── mallocgcTiny:   6.04%
├── nextFreeFast:   6.04%
├── LeafPage.Insert: 5.20%   ← 业务逻辑
└── 其他:           74.79%
```

**关键发现**:
- ⚡ **纯内存性能已接近硬件极限**: 搜索仅 6.53 ns（~20 CPU 周期）
- 💡 **内存分配是主要瓶颈**: 占用 7.89% CPU
- 🎯 **Phase 2A 优化目标**: 减少内存分配，降低 GC 压力

**注意**: 当前测试包含磁盘持久化，QPS 受磁盘 I/O 限制。纯内存数据仅供参考。

### 快速优化机会

**聚焦内存优化**（本次范围）:
```
优化项              预期提升          实施难度    工作量
──────────────────────────────────────────────────
Buffer Pool         GC CPU ↓80%      低         3 天
序列化优化           CPU ↓41%        中         2 天
──────────────────────────────────────────────────
总计                内存效率大幅提升   -          5 天
实际效果            分配↓82%, GC↓80%   -          5 天
```

**不在本次范围内**:
- ❌ 磁盘 I/O 优化（异步刷盘、WAL、Group Commit）
- ❌ 批量 Set API（不实现 SetBatch）
- ❌ Get 操作优化（聚焦 Set）

---

## 🚀 优化方案

### 1. Buffer Pool 实现

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

### 2. 序列化优化（可选）

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

- [ ] **Buffer Pool**: 命中率 >80%，无内存泄露
- [ ] **序列化优化**: CPU 开销降低至 <5%
- [ ] **向后兼容**: 现有 API 无需修改

### 性能验收

```
内存分配目标:
├── 每次操作分配: 5KB → 1KB (↓82%)
├── GC CPU 时间: 15.23% → 3% (↓80%)
├── 序列化 CPU: 8.45% → <5% (↓41%)
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

## 📊 优化前后对比

### 内存分配对比

```
┌──────────────────────────────────────────────────┐
│ 指标           Phase 1 (当前)  Phase 2A (目标)   │
├──────────────────────────────────────────────────┤
│ 内存分配/op    5.5KB          1KB               │
│ GC CPU         15.23%         3%                │
│ 序列化 CPU     8.45%          <5%               │
│ Buffer Pool    N/A            >80% 命中率        │
├──────────────────────────────────────────────────┤
│ 提升           -              ↓82% 分配, ↓80% GC │
└──────────────────────────────────────────────────┘
```

### 纯内存参考

**说明**: 以下数据来自关闭磁盘持久化的纯内存测试，仅作为参考上限。

```
┌──────────────────────────────────────────────────┐
│ 操作           纯内存性能       说明             │
├──────────────────────────────────────────────────┤
│ Set (纯内存)   1430 万 QPS     无磁盘 I/O        │
│ Get (纯内存)   1.53 亿 QPS     接近硬件极限      │
└──────────────────────────────────────────────────┘
```

---

## 💡 讨论要点

1. **优化范围**: 是否同意 Phase 2A 仅聚焦内存优化？
2. **性能目标**: 内存分配 ↓82%, GC CPU ↓80% 是否合理？
3. **实施时间**: 5 天是否足够完成 Buffer Pool + 序列化优化？
4. **后续规划**: 磁盘 I/O 优化（异步刷盘、WAL）是否需要单独的 Phase？
5. **纯内存参考**: 是否需要参考纯内存性能数据（1430 万 QPS）？

---

**文档版本**: v2.0
**分支**: phase2-write-optimization
**最后更新**: 2026-03-13

## 📝 更新日志

**v2.0** (2026-03-13):
- ✅ **重大调整**: Phase 2A 仅聚焦内存优化
- ✅ 移除所有磁盘 I/O 相关内容（异步刷盘、WAL、Group Commit）
- ✅ 更新性能目标：仅关注内存分配和 GC
- ✅ 简化优化方案：Buffer Pool + 序列化优化
- ✅ 更新工期：5 天（原 1 周）

**v1.1** (2026-03-13):
- 添加纯内存性能上限分析

**v1.0** (2026-03-13):
- 初始版本

🎯 **聚焦内存优化：减少分配，降低 GC，提升效率！**
