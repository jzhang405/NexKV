# Phase 6: 8 线程 Set 操作 pprof 分析

日期: 2026-03-28
分支: `perf/btree-set-benchmark`
测试工具: `./bin/btree_perf_pprof -threads 8 -count 50000 -init 200`
测试工具: `./bin/btree_perf_scheduler -op set -threads 8 -count 50000 -init 200`

## 1. 8 线程吞吐量基准

scheduler 工具（5 轮），预生成 key/value：

| 轮次 | 吞吐量 (ops/s) | 延迟 (μs/op) |
|------|---------------|--------------|
| 1 | 23,324 | 42.87 |
| 2 | 27,636 | 36.18 |
| 3 | 11,810 | 84.67 |
| 4 | 20,515 | 48.74 |
| 5 | 21,619 | 46.25 |

**中位数: 21,619 ops/s, 46.25 μs/op**

**vs 单线程 (35K ops/s): 扩展比 0.62x（负扩展）**

## 2. CPU Profile 分析（单线程 profiling，1 线程 × 50K ops）

```
Duration: 398.84ms, Total samples = 620ms (155.45%)
```

### 2.1 Flat Top 15

| flat% | 函数 | 说明 |
|-------|------|------|
| 19.35% | `runtime.tryDeferToSpanScan` | GC 扫描调度 |
| 6.45% | `UpdateLeafEntry` | 叶子条目更新（COW 重分配） |
| 6.45% | `runtime.scanObject` | GC 对象扫描 |
| 4.84% | `GetLeafEntry` | 读取叶子条目 |
| 4.84% | `InitPage` | 页面初始化 |
| 4.84% | `runtime.mallocgcTiny` | 小对象分配 |
| 4.84% | `runtime.(*mspan).base` | GC span 操作 |
| 3.23% | `runtime.mallocgc` | 堆内存分配 |
| 3.23% | `runtime.memclrNoHeapPointers` | 内存清零 |
| 3.23% | `runtime.memmove` | 内存拷贝 |

### 2.2 Cumulative Top 15

| cum% | 函数 | 说明 |
|------|------|------|
| 62.90% | `BTree.Set` | Set 入口 |
| 61.29% | `setWithLeafLock` | 叶子锁写入主路径 |
| 54.84% | `InsertToOffHeap` | Off-Heap 插入 |
| 51.61% | `UpdateLeafEntry` | **叶子更新（COW 重分配）** |
| 41.94% | `runtime.systemstack` | GC 系统栈切换 |
| 35.48% | `runtime.gcBgMarkWorker` | GC 后台标记 |
| 35.48% | `runtime.gcDrain` | GC 标记排空 |
| 29.03% | `runtime.mallocgc` | 堆内存分配 |
| 25.81% | `runtime.scanObject` | GC 对象扫描 |
| 22.58% | `runtime.tryDeferToSpanScan` | GC 扫描调度 |

## 3. 瓶颈分析

### 瓶颈 1: UpdateLeafEntry — 51.61% CPU（P0）

**调用链展开**：

```
UpdateLeafEntry (51.61% cum, 6.45% flat)
├── runtime.mallocgc (40.62%)  ← Go 堆分配
├── gcWriteBarrier (9.38%)     ← GC 写屏障
├── GetLeafEntryOffset (9.38%) ← 读取 entry 元数据
├── runtime.makeslice (9.38%)  ← slice 分配
├── PageManager.Alloc (6.25%)  ← 页面分配
├── PageManager.Free (6.25%)   ← 页面释放
├── GetValue (3.12%)           ← 读取 value
└── runtime.memmove (3.12%)    ← 内存拷贝
```

**根因**：`UpdateLeafEntry` 在 `offheap_adapter.go:164-210` 每次更新都要：
1. 收集所有 KV 对到 Go 堆 slices（`make([][]byte, 0, count)` + `make([]byte, len(k))` + `copy`）
2. 释放旧页面
3. 分配新页面
4. 调用 `MaterializePageFromBytes` 重新物化

**问题**：
- 每次 update 产生 2N 个 `make([]byte)` + 4N 次 `copy`（N = 页面条目数）
- `makeslice` 占 9.38%，`mallocgc` 占 40.62%
- 这是 GC 压力的主要来源

**GC 总开销**：`gcBgMarkWorker` + `gcDrain` + `scanObject` + `tryDeferToSpanScan` + `mallocgc` + `mallocgcTiny` + `gcWriteBarrier` = **69.35% CPU**

### 瓶颈 2: 8 线程锁竞争 — 负扩展（P0）

| 并发度 | 吞吐量 | 扩展比 |
|--------|--------|--------|
| 1T | 35K ops/s | 1.00x |
| 2T | 32K ops/s | 0.92x |
| 4T | 34K ops/s | 0.99x |
| **8T** | **21K ops/s** | **0.62x** |

**根因**：
- `TryLock()` 非阻塞锁，失败立即返回 `ErrRetry`
- 8 线程碰撞率极高，大量 CPU 浪费在重试上
- 方差极大（12K ~ 28K），说明竞争条件随机且不稳定

### 瓶颈 3: GC 压力 — 69.35% CPU（P1）

| GC 函数 | CPU 占比 |
|---------|---------|
| `tryDeferToSpanScan` | 19.35% |
| `gcBgMarkWorker` | 35.48% |
| `gcDrain` | 35.48% |
| `scanObject` | 25.81% |
| `mallocgc` | 29.03% |
| `mallocgcTiny` | 17.74% |
| `gcWriteBarrier` | 4.84% |

**分析**：
- GC 开销来自 `UpdateLeafEntry` 的 Go 堆分配
- 每次 update 产生大量临时 `[]byte` 对象
- GC 需要扫描和标记这些对象
- 多线程下 GC 压力进一步放大（每个 goroutine 都在分配）

### 瓶颈 4: fmt.Sprintf 残留 — 约 1.61%（P2）

- `fmt.(*pp).doPrintf` 仍然出现
- 来自 `UpdateLeafEntry` 内部的错误路径
- 占比低，优先级低

## 4. CPU 分布

```
┌──────────────────────────────────────────────────────┐
│            CPU 分布 (单线程 profiling)                 │
├──────────────────────────────────────────────────────┤
│                                                       │
│  ████████████████████████████████████   51%  UpdateLeafEntry (COW 重分配) │
│  █████████████████████████████         35%  GC (gcBgMark+gcDrain)         │
│  ████                                  10%  页面操作 (Alloc/Free/Init)    │
│  ███                                    5%  搜索路径 (findLeaf/linear)    │
│  ██                                     3%  其他                         │
│                                                       │
└──────────────────────────────────────────────────────┘

多线程额外开销：
┌──────────────────────────────────────────────────────┐
│  ████████████████████████████████████   50%+  锁竞争重试 (ErrRetry) │
│  ██████████████████████████             35%    GC 压力 (多线程放大)   │
│  ████████████                           15%    实际数据处理          │
└──────────────────────────────────────────────────────┘
```

## 5. 优化路线图

### P0: UpdateLeafEntry 零堆分配（预期 +50-80% 单线程）

**当前问题**：每次 update 收集所有 KV 到 Go 堆 slices，再重新物化。

**方案 A — In-Place Update（最优）**：

对于 key 已存在的 update，直接在 mmap 页面上修改 value：
- 不需要重新分配页面
- 不需要收集所有 KV 对
- 零 Go 堆分配

```go
func (a *OffHeapAdapter) UpdateLeafEntryInPlace(pageID, idx int, value []byte) error {
    // 检查新 value 长度 <= 旧 value 长度
    entry := pa.GetLeafEntry(pageID, idx)
    if len(value) <= int(entry.valLen) {
        // 直接覆盖 value
        valPtr := unsafe.Add(ptr, entry.valOff)
        copy(unsafe.Slice((*byte)(valPtr), entry.valLen), value)
        return nil
    }
    // value 变大，需要 COW
    return ErrNeedCOW
}
```

**方案 B — COW BulkInit（次优）**：

类似 BulkInitLeafFromSource，直接从源页面拷贝到新页面，跳过 Go 堆：

```go
func (pa *PageAccessor) BulkUpdateLeafEntry(
    srcPageID, dstPageID uint32,
    updateIdx int, newValue []byte,
) (uint16, error) {
    // 类似 BulkInitLeafFromSource，但在 updateIdx 处替换 value
}
```

### P1: 指数退避重试策略（预期 +30-50% 多线程）

当前重试无退避，8 线程碰撞率极高。添加指数退避：

```go
for retries := 0; retries < maxRetries; retries++ {
    err := b.setWithLeafLock(ctx, key, value)
    if err == nil { return nil }
    if errors.Is(err, ErrRetry) {
        time.Sleep(time.Duration(1<<retries) * time.Microsecond)
    }
}
```

### P2: GC 调优（预期 +10-15%）

- 设置 `GOGC=500` 或 `GOMEMLIMIT` 减少 GC 频率
- 使用 `sync.Pool` 复用 `[]byte` 切片
- 减少 `UpdateLeafEntry` 中的临时分配

## 6. 与 Phase 5 对比

```
Phase 5 (单线程, pprof 工具):
1. futex 调度         37%  ████████████████████████████████████
2. 搜索路径           10%  ██████████
3. TaskScheduler       9%  █████████
4. fmt/errors         10%  ██████████
5. 分裂处理            9%  █████████
6. GC                  5%  █████

Phase 6 (单线程, fmt 优化后):
1. UpdateLeafEntry    52%  ████████████████████████████████████████████████████
2. GC (malloc+scan)   35%  ████████████████████████████████████
3. 页面操作           10%  ██████████
4. 搜索路径            5%  █████

结论：
- TaskScheduler futex 开销已消除（直通模式）
- fmt.Sprintf 开销已消除（预生成）
- 新的主要瓶颈：UpdateLeafEntry 的 COW 重分配导致的 GC 压力
- 多线程下锁竞争叠加 GC 压力，导致负扩展
```

## 7. 目标

| 优化 | 单线程预期 | 8 线程预期 |
|------|-----------|-----------|
| 当前 | 35K ops/s | 21K ops/s (0.62x) |
| +P0 (in-place update) | 50-60K ops/s | 30-35K ops/s (0.6x) |
| +P1 (退避重试) | 50-60K ops/s | 60-80K ops/s (1.2x) |
| +P2 (GC 调优) | 55-65K ops/s | 80-100K ops/s (1.5x) |
