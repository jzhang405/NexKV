# BTree Set 性能基线 (2026-03-29)

## 测试环境

- **CPU**: AMD Ryzen 9 9950X (16C/32T)
- **OS**: Linux 6.17.0-14-generic
- **Go**: 1.26.0
- **GOGC**: 500
- **测试工具**: `cmd/btree_perf_scheduler`, `cmd/btree_perf_pprof`
- **分支**: `perf/btree-set-benchmark2` (基于 `38de31b`)

## 测试配置

- **操作类型**: Set / Get / Mixed (50% Set + 50% Get)
- **每线程操作数**: 50,000
- **初始数据量**: 5,000 keys
- **Key 格式**: `init-%05d` (10 bytes), update key 与 init key 重叠
- **Value 格式**: `v%05d` (6 bytes)

## Set 性能 (builtin mode)

| 线程数 | 总操作数 | 吞吐量 (ops/s) | 平均延迟 (μs) | 扩展比 |
|--------|---------|---------------|--------------|--------|
| 1      | 50,000  | 16,347        | 61.17        | 1.00x  |
| 2      | 100,000 | 36,302        | 27.55        | 2.22x  |
| 4      | 200,000 | 15,034        | 66.52        | 0.92x  |
| 8      | 400,000 | 13,079        | 76.46        | 0.80x  |

**关键发现**: 2 线程达到峰值，4+ 线程吞吐量反而下降，并发扩展性差。

## Get 性能 (builtin mode)

| 线程数 | 总操作数 | 吞吐量 (ops/s) | 平均延迟 (μs) | 扩展比 |
|--------|---------|---------------|--------------|--------|
| 1      | 50,000  | 1,548,622     | 0.65         | 1.00x  |
| 2      | 100,000 | 1,849,976     | 0.54         | 1.19x  |
| 4      | 200,000 | 2,335,572     | 0.43         | 1.51x  |
| 8      | 400,000 | 2,222,912     | 0.45         | 1.43x  |

**关键发现**: Get 无锁路径，4 线程达到 2.3M ops/s 峰值，Set/Get 性能差距 ~100x。

## Mixed 性能 (50% Set + 50% Get)

| 线程数 | 总操作数 | 吞吐量 (ops/s) | 平均延迟 (μs) | 扩展比 |
|--------|---------|---------------|--------------|--------|
| 1      | 50,000  | 19,327        | 25.95        | 1.00x  |
| 2      | 100,000 | 31,607        | 15.82        | 1.64x  |
| 4      | 200,000 | 31,607        | 31.64        | 1.64x  |
| 8      | 400,000 | 17,871        | 55.96        | 0.92x  |

## CPU Profile 热点分析 (4 线程, 50K ops/thread)

**CPU profile 文件**: `cpu-4t-50k.prof`

### Top 15 CPU 热点 (cumulative)

| 函数 | flat | flat% | cum | cum% |
|------|------|-------|-----|------|
| `runtime.futex` | 550ms | 23.11% | 550ms | 23.11% |
| `(*BTree).setWithLeafLock` | 20ms | 0.84% | 910ms | 38.24% |
| `(*BTree).searchPathWithRefs` | 40ms | 1.68% | 360ms | 15.13% |
| `(*BTree).findLeafPageRef` | — | — | 360ms | 15.13% |
| `(*BTree).SetWithTask` | — | — | 360ms | 15.13% |
| `(*OffHeapAdapter).InsertToOffHeap` | 10ms | — | 330ms | 13.87% |
| `pkg/errors.Wrapf` | 10ms | — | 280ms | 11.76% |
| `runtime.mallocgc` | 20ms | 0.84% | 250ms | 10.50% |
| `(*OffHeapAdapter).linearSearchLeaf` | 40ms | 1.68% | 240ms | 10.08% |
| `(*SchedulerCore).runLoop` | — | — | 220ms | 9.24% |

### 瓶颈分析

1. **`runtime.futex` (23.1%)**: 线程调度/锁竞争开销。高并发时线程频繁 park/wakeup，表明锁争用严重。

2. **`setWithLeafLock` (38.2% cum)**: Set 主路径，包含 searchPath + leaf lock + insert + split。
   - `searchPathWithRefs` (15.1%): 路径搜索占用大量 CPU
   - `findLeafPageRef` (15.1%): 叶子页查找

3. **`InsertToOffHeap` (13.9% cum)**: Off-Heap 插入操作，含 `linearSearchLeaf` (10.1%)。

4. **`pkg/errors.Wrapf` (11.8% cum)**: 错误构造仍然有显著开销，虽然之前做过优化。

5. **`runtime.mallocgc` (10.5% cum)**: 内存分配和 GC 开销。

## 核心问题

### 1. Set 并发扩展性差 (0.8x @ 8T)

- 2 线程时吞吐量最高 (36K ops/s)
- 4+ 线程吞吐量下降到 13-15K
- `runtime.futex` 占 23% CPU，锁竞争是主要瓶颈
- 对比 Get 路径 (2.3M ops/s, 1.51x 扩展比)，Set 性能差距 ~100x

### 2. 错误构造开销偏高

- `pkg/errors.Wrapf` 仍然占 11.8% cumulative CPU
- 高并发下 ErrRetry 返回频繁，每次都构造完整错误链

### 3. 内存分配

- `mallocgc` 占 10.5%
- 主要来自 `searchPathWithRefs` 和 `InsertToOffHeap` 中的临时对象

### 4. 线性搜索开销

- `linearSearchLeaf` 占 10.1%
- 叶子页内线性扫描比较 keys

## 优化方向

| 优先级 | 优化项 | 预期收益 | 难度 |
|--------|--------|---------|------|
| P0 | 减少锁竞争粒度/乐观锁 | 2-3x 吞吐量 | 高 |
| P0 | 热路径错误构造零开销 | 10-15% | 低 |
| P1 | searchPath 对象池 | 5-10% | 中 |
| P1 | 线性搜索 → 二分搜索 | 5-10% | 低 |
| P2 | 减少 futex 调度开销 | 10-20% | 高 |

## 已知 Bug

### SearchKey 并发 Panic

`GetIndexEntry` 在高并发下触发 `index out of range`:

```
panic: index 0 out of range (count: 2)
goroutine X [running]:
offheap.(*PageAccessor).GetIndexEntry (page_layout.go:203)
offheap.(*PageAccessor).SearchKey (page_layout.go:363)
btree.(*OffHeapAdapter).SearchChild
btree.(*BTree).searchPathWithRefs
```

**原因**: 并发写入导致叶子分裂时，`SearchKey` 读取的 `header.count` 与实际 entries 不一致。
**影响**: 8 线程场景下偶发 crash（成功率 ~50%），4 线程下较稳定。

## Profile 文件

- `cpu-4t-50k.prof`: 4 线程 50K ops Set CPU profile
- 查看: `go tool pprof -http=:8080 cpu-4t-50k.prof`
