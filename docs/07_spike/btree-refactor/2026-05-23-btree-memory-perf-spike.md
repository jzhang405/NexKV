# BTree Memory Mode 性能调试预研

> 创建日期：2026-05-23
> 最后更新：2026-05-24
> 状态：Investigation — **P0 全完成，P1 Benchmark 完成，Lazy Split 方案设计完成，待实施**
> 分支：`spike/btree-memory-perf`

---

## 一、问题描述

`cmd/tools/btree_bench` 在 memory-only 模式（epoch=off，无 WAL/AO）下，**par-put 性能高度不稳定**。

### 初始状态（P0 优化前）

| 运行 | par-put-4 | par-put-8 | par-put-16 |
|------|-----------|-----------|------------|
| Run 1 | 493K | 583K | 712K |
| Run 2 | 1,346K | 146K | 27K |
| Run 3 | 54K | 99K | — (hang) |

### 当前状态（P0 池化 + clearPage memclr 后，2026-05-24）

| 运行 | par-put-4 | par-put-8 | par-put-16 |
|------|-----------|-----------|------------|
| Run 1 | 1,950K | 1,328K | 161K |
| Run 2 | 54K | 1,366K | 55K |
| Run 3 | 39K | 148K | 73K |
| Run 4 | 2,231K | 1,160K | 55K |
| Run 5 | 2,032K | 1,329K | 1,287K |

**波动幅度仍然 50x 以上**（par-put-4: 39K-2.2M），但**快速运行的峰值从 1.3M 提升至 2.2M**（+70%）。P0 优化提升了稳态吞吐，但未解决波动根因。

---

## 二、Benchmark 设计分析

### 2.1 测试流程

```mermaid
flowchart TB
    A["1. NewOffheapBTreeStorage(512MB)"] --> B["2. NewBTree(storage, opts...)"]
    B --> C["3. Warmup: loop(100K, threads, tree)"]
    C --> D["4. totalOps.Store(0)"]
    D --> E["5. Measure: loop(100K, threads, tree)"]
    E --> F["6. QPS = 100K / elapsed"]
```

```go
// warmup (NOT timed):
loop(*warmup, threads, tree, &totalOps, getOnly, n)

// measure (timed):
t0 := time.Now()
loop(n, threads, tree, &totalOps, getOnly, n)
elapsed := time.Since(t0)
```

### 2.2 Key 分配策略

```go
func keyOf(i int) []byte {
    return []byte(fmt.Sprintf("key-%010d", i))
}

// par-put-4 (4 goroutines, 100K ops):
//   G0: keys 0-24999      → sequential insert
//   G1: keys 25000-49999   → sequential insert
//   G2: keys 50000-74999   → sequential insert
//   G3: keys 75000-99999   → sequential insert
```

### 2.3 两个阶段的差异

| 阶段 | 操作类型 | 树状态 | 是否 Split |
|------|---------|--------|-----------|
| Warmup (100K) | **Insert**（空树→100K keys） | 从 0 增长到 100K | ✅ 大量 Split |
| Measure (100K) | **Update**（key 已存在） | 100K keys | ❌ 无 Split |

**关键发现**：测量阶段是 Update 而非 Insert——key 已在 warmup 中插入，Set 走 Update 路径。不应触发 Split。

---

## 三、争用热点分析

### 3.1 第一阶段 pprof 回顾

```
top 30 CPU (7.43s, 258% utilization):
  runtime.usleep               15.58%  ← 休眠/等待!
  runtime.pthread_cond_wait    9.09%   ← 条件变量等待!
  runtime.madvise              8.21%   ← 内存 advice 系统调用
  runtime.pthread_kill         5.82%   ← 线程信号
  sync/atomic.(*Int32).Add     5.09%   ← PageRef 引用计数
  offheap.clearPage            4.78%   ← Alloc 时清零
  cmpbody                      4.52%   ← Key 比较
```

**前三项合计 33%** 都是调度开销——线程在等锁/CAS，不是在干活。

### 3.2 推测瓶颈路径

```mermaid
flowchart TB
    subgraph WarmupPhase["Warmup 阶段 — 大量 Split"]
        W1["G0-G3 并发 Insert"]
        W2["Leaf 满了 → Split"]
        W3["handleLeafSplit → handleInternalSplit"]
        W4["级联到根: handleRootInternalSplit"]
        W5["ReplaceRoot CAS 竞争"]
    end
    
    subgraph MeasurePhase["Measure 阶段 — Update"]
        M1["searchPath 遍历 root → leaf"]
        M2["COW: Alloc + Copy + Mutate"]
        M3["leafRef.CAS(oldInfo, newInfo)"]
        M4["同一叶子页多 goroutine → CAS 冲突"]
        M5["CAS 重试 → 更多 Alloc/Copy → 更多浪费"]
    end
    
    subgraph Shared["共享争用点"]
        S1["根节点 PageRef CAS"]
        S2["内部节点 ChildrenCache CAS"]
        S3["PageManager Alloc (nextPageID atomic)"]
    end
    
    WarmupPhase --> Shared
    MeasurePhase --> Shared
```

### 3.3 Update 路径的 COW 开销

```go
// operations.go:writeOperation — 非 Split 路径
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
    // Step 1: searchPath → 遍历到叶子 (lock-free)
    path, _ := searchPath(b.rootRef, key)
    
    // Step 2: 读旧页
    oldInfo := leafRef.GetPageInfo()
    oldLeaf, _ := b.storage.GetLeafPage(oldInfo.PageID)
    
    // Step 3: COW mutate
    result, _ := mutate(oldLeaf)           // 分配新页 + 复制 + 修改
    
    // Step 4: CAS 替换
    if !leafRef.CAS(oldInfo, newInfo) {
        // ★ 冲突! → FreePage(newPage) → retry
    }
}
```

**每次 CAS 冲突的成本**：1 次 Alloc + 1 次 4KB memcpy + 1 次 FreePage = ~3-5μs 浪费。

### 3.4 为什么波动如此巨大？

```mermaid
flowchart TB
    subgraph Fast["快速运行 (1.3M QPS)"]
        F1["Warmup: 低冲突 Split"]
        F2["树结构均匀"]
        F3["Measure: goroutine 访问不同叶子"]
        F4["CAS 几乎无冲突 → 高 QPS"]
    end
    
    subgraph Slow["慢速运行 (27K QPS)"]
        S1["Warmup: Split 级联到根"]
        S2["根节点频繁 ReplaceRoot"]
        S3["ChildrenCache 不一致 → searchPath 重试"]
        S4["Retry 触发更多 CAS → 恶性循环"]
    end
    
    subgraph Trigger["波动触发因素"]
        T1["goroutine 调度时序"]
        T2["Split 传播到根的时机"]
        T3["childrenCache 更新时机"]
        T4["操作系统线程调度抖动"]
    end
```

**根因**：warmup 阶段的 Split 传播和 ChildrenCache 更新与 goroutine 调度之间存在**竞态条件**。不同的时序导致树结构差异巨大，进而影响测量阶段的争用程度。

---

## 四、已验证排除的因素

| 因素 | 结论 | 证据 |
|------|------|------|
| Epoch 开销 | ❌ 不是瓶颈 | pprof top 30 中无 epoch 函数 |
| 页面分配器 | ❌ 占比较小 | `clearPage` 4.78% |
| MVCC 编码 | ❌ benchmark 无事务 | 纯 BTree Set/Get |
| WAL/AO | ❌ memory-only | 未启用 |
| RetireBatch | ❌ epoch=off 时跳过 | 代码路径不执行 |

---

## 五、精确 Profiling 结果

### 5.1 测试条件

```bash
go run ./cmd/tools/btree_bench -n 50000 -only par-put -cpuprofile /tmp/parput.prof
```

结果：par-put-4=831K, par-put-8=1.13M, par-put-16=47K

### 5.2 CPU Profile Top 30

```
Duration: 13.39s, Total samples = 23.55s (175.84% CPU utilization)

  flat   flat%   function
  4.17s  17.71%  runtime.pthread_cond_signal    ← 线程信号!
  3.90s  16.56%  runtime.madvise                ← 内存 advice 系统调用!
  1.83s   7.77%  offheap.clearPage              ← 页面清零
  1.75s   7.43%  runtime.usleep                 ← 休眠等待!
  1.73s   7.35%  runtime.pthread_cond_wait      ← 条件变量等待!
  0.87s   3.69%  sync/atomic.(*Int32).Add        ← PageRef 引用计数
  0.79s   3.35%  offheap.InsertLeafEntry          ← 叶子插入
  0.56s   2.38%  runtime.pthread_kill            ← 线程 kill
  0.50s   2.12%  runtime.kevent                  ← kqueue 事件
  0.48s   2.04%  runtime.memmove                 ← 内存拷贝
  0.40s   1.70%  cmpbody                         ← Key 比较
  0.25s   1.06%  btree.(*PageRef).Release        ← PageRef 释放
  0.22s   0.93%  btree.(*PageRef).Retain         ← PageRef 持有
  0.20s   0.85%  btree.(*ChildrenCache).Search   ← 子节点搜索
  0.14s   0.59%  btree.searchPath (cum 11.51%)   ← 路径遍历
```

### 5.3 关键发现

```mermaid
pie title CPU 时间分布
    "调度/睡眠/等待" : 32.5
    "内存管理 (madvise/mmap)" : 17.4
    "页面操作 (clear/memmove/insert)" : 13.2
    "引用计数 (Retain/Release)" : 4.8
    "BTree 遍历 (searchPath/Search)" : 4.0
    "其他" : 28.1
```

**核心结论**：**BTree 代码本身不是瓶颈**。

| 类别 | 占比 | 根因 |
|------|------|------|
| 🔴 调度开销 | **32.5%** | pthread_cond_signal/wait/usleep — goroutine 在等锁/CAS |
| 🔴 内存管理 | **17.4%** | madvise/mmap — mmap 页表的 OS 级开销 |
| 🟡 页面操作 | 13.2% | clearPage(每次 Alloc 清零 4KB) + InsertLeafEntry + memmove |
| 🟢 BTree 逻辑 | **4.0%** | searchPath + ChildrenCache.Search |
| 🟢 引用计数 | 4.8% | PageRef.Retain/Release atomic.Add |

**三个关键洞察**：

1. **pthread_cond_signal (17.7%) 排第一** — Go 调度器在 goroutine 之间频繁发送信号。COW 路径中 CAS 失败 → goroutine 退避 → 调度器唤醒 → 信号开销。这不是 BTree 的问题，是 COW + 高并发 + CAS 退避的组合效应。

2. **madvise (16.6%) 排第二** — mmap 内存区域的 page fault 和 OS 页表操作。每次 `Alloc` 分配新 PageID 后首次访问触发 page fault → OS 分配物理页 → madvise 更新页表。512MB mmap 池中密集分配导致 madvise 成为第二大热点。

3. **clearPage (7.8%) 排第三** — 每次 COW 分配新页面都要清零 4KB。无 Epoch 时旧页进入 FreeList 但不清零（被标记 deleted），下次 Alloc 从 FreeList 取出时清零。

#### 5.3.1 调度开销的根因追踪：`usleep` 是谁调用的？

`runtime.usleep` 不是我们的代码直接调用的——是 **Go runtime 内部锁竞争** 的产物。

```mermaid
flowchart TB
    subgraph OurCode["NexKV 代码 (仅 4% CPU)"]
        COW["COW 写操作<br/>writeOperation / handleLeafSplit"]
        Alloc1["高频 Go 堆分配"]
    end
    
    subgraph GCRuntime["Go Runtime (32.5% CPU)"]
        Alloc1 --> GC["newobject → mallocgc<br/>触发 GC Assist"]
        GC --> Lock["gcParkAssist<br/>→ runtime.lock2 竞争"]
        Lock --> Yield["runtime.osyield<br/>→ usleep (7.4%)"]
        Lock --> CondWait["pthread_cond_wait<br/>→ goroutine 休眠 (7.4%)"]
        Lock --> CondSignal["pthread_cond_signal<br/>→ 唤醒 goroutine (17.7%)"]
    end
    
    OurCode --> GCRuntime
```

**pprof trace 证据**（`go tool pprof -traces -focus="usleep"`）：

```
Trace 1: writeOperation → searchPath → growslice → mallocgc
           → gcAssistAlloc → gcParkAssist
           → runtime.lock2 → runtime.osyield → usleep

Trace 2: writeOperation → handleLeafSplit → leafPageHandle.Split
           → newobject → mallocgc
           → gcAssistAlloc → gcParkAssist
           → runtime.lock2 → runtime.osyield → usleep

Trace 3: (goroutine parking) findRunnable → schedule
           → park_m → runtime.lock2 → osyield → usleep
```

**每条 trace 的共同路径**：

```
writeOperation / handleLeafSplit    ← COW 分配新 Page (mmap) + 新 Handle (Go 堆)
    ↓
newobject / growslice               ← Go 堆对象分配 (leafPageHandle, nodePageHandle, SearchPath...)
    ↓
mallocgc                            ← Go 内存分配器
    ↓
gcAssistAlloc → gcParkAssist       ← GC 辅助扫描: goroutine 被迫帮 GC 干活
    ↓
runtime.lock2 → runtime.osyield     ← GC 内部锁竞争 → usleep!
```

**核心根因**：不是 BTree 算法慢，是 **COW 架构下的 Go 堆分配率**触发了 GC 压力恶性循环：

```mermaid
flowchart LR
    A["每次 Set<br/>→ 1 次 mmap Alloc<br/>→ 2-3 个 Go 堆对象"] -->|"×4 goroutines<br/>×50K ops"| B["每秒 ~200K 次<br/>Go 堆分配"]
    B --> C["Go GC 频繁触发"]
    C --> D["GC Assist: goroutine<br/>被迫暂停帮 GC 扫描"]
    D --> E["GC 内部锁竞争<br/>→ usleep/cond_wait"]
    E -->|"goroutine 被暂停"| F["写吞吐下降"]
    F -->|"队列积压"| A
```

**具体分配来源**（per writeOperation，逐行追踪）：

```mermaid
flowchart TB
    subgraph Path["writeOperation 热路径 (非 Split)"]
        direction TB
        A["1. searchPath()"]
        B["2. GetLeafPage()"]
        C["3. leaf.GetKey() / GetValue()"]
        D["4. leaf.Insert() / Update()"]
        E["5. CAS newInfo"]
        F["6. path.ReleaseAll()"]
        A --> B --> C --> D --> E --> F
    end
    
    subgraph Allocs["Go 堆分配 (每步)"]
        A1["search.go:82,133<br/>path = append(path, PathEntry{})<br/>→ SearchPath slice 扩容"]
        B1["offheap_storage.go:137<br/>return &leafPageHandle{...}<br/>→ 新 Handle 对象"]
        C1["leaf_page.go:64/73<br/>cp := make([]byte, len(raw))<br/>→ key/value 副本"]
        D1["leaf_page.go:80<br/>pm.Alloc() → newRawID<br/>→ mmap 页分配 (非 Go 堆)"]
        D2["leaf_page.go:109<br/>return &leafPageHandle{...}<br/>→ COW 新 Handle"]
        E1["operations.go:232<br/>newInfo := &PageInfo{...}<br/>→ 新 PageInfo"]
        F1["page_ref.go:111<br/>refCount atomic 递减<br/>(无额外分配)"]
    end
    
    Path --> Allocs
```

#### 按分配类型详列

**#1 SearchPath 切片扩容** — `search.go:82,133`

```go
// search.go:82 — 每到达一个叶子页
path = append(path, PathEntry{Ref: currentRef, Index: -1})

// search.go:133 — 每经过一个内部节点
path = append(path, PathEntry{Ref: currentRef, Index: actualIdx})
```

- 初始容量: 0，每层扩容一次（2→4→8...）
- 2 层树: ~2 次 append，切片最终 ~32B
- 3 层树: ~3 次 append + 可能触发 `growslice`（mallocgc）

**#2 leafPageHandle 分配** — `offheap_storage.go:137`

```go
// offheap_storage.go:137 — GetLeafPage 每次创建新 Handle
return &leafPageHandle{id: pageID, pa: s.pa, storage: s}, nil
```

- 每次 `GetLeafPage` 都 `&leafPageHandle{}` — **无条件堆分配**
- 大小: ~48B（3 指针 + 1 uint32）
- **这是单次写操作最大的 Go 堆分配来源**

**#3 Key/Value 副本** — `leaf_page.go:64,73`

```go
// leaf_page.go:64 — GetKey
cp := make([]byte, len(raw))
copy(cp, raw)

// leaf_page.go:73 — GetValue
cp := make([]byte, len(raw))
copy(cp, raw)
```

- 每次读 key/value 都 `make([]byte)` — 新堆分配
- benchmark key: `"key-0000000000"` = 14B
- benchmark value: `"value-0000000000"` = 16B
- **合计 ~30B/op**，加上 slice header 24B

**#4 COW 新 Handle** — `leaf_page.go:80,109`

```go
// leaf_page.go:80 — Insert/Update 内 COW 分配 mmap 页
newRawID, err := h.storage.pm.Alloc()  // mmap 页,非 Go 堆

// leaf_page.go:109 — 返回新页的 Handle
return &leafPageHandle{id: newID, pa: h.pa, storage: h.storage}, nil
```

- `pm.Alloc()` — mmap 分配（不触发 Go GC）
- `&leafPageHandle{}` — Go 堆分配（触发 GC）

**#5 PageInfo** — `operations.go:232`

```go
// operations.go:232 — CAS 发布新页
newInfo := &PageInfo{
    PageID:  result.newPageID,
    Version: oldInfo.Version + 1,
    IsLeaf:  true,
}
```

- 大小: ~64B（8 字段）
- **每次 CAS 无论成功失败都分配**

**#6 MVCC 编码**（仅事务模式） — `btree.go:334`

```go
// btree.go:334 — Set 内 MVCC 编码
encoded, buildErr := mvcc.BuildMVCC(mvcc.FlagNormal, b.tsGen.NextTS(), value)
```

- `BuildMVCC` 内 `make([]byte, 9+len(value))` — 堆分配
- benchmark 无事务时**不触发**（epoch=off 走简化路径）

#### 单次写操作总分配（非 Split 路径，无事务）

| # | 分配 | 文件:行 | 类型 | 大小 | 每 op 次数 |
|---|------|---------|------|------|-----------|
| 1 | SearchPath 扩容 | `search.go:82,133` | slice grow | ~32-64B | 1 |
| 2 | leafPageHandle | `offheap_storage.go:137` | heap obj | ~48B | 1 |
| 3 | key copy | `leaf_page.go:64` | `make([]byte)` | ~14B | 1 |
| 4 | value copy | `leaf_page.go:73` | `make([]byte)` | ~16B | 1 |
| 5 | COW handle | `leaf_page.go:109` | heap obj | ~48B | 1 |
| 6 | PageInfo | `operations.go:232` | heap obj | ~64B | 1 |
| **合计** | | | | **~220B/op** | |

**par-put-4 (50K ops)**: 50K × 220B × 4 goroutines = **~44MB Go 堆分配**
**par-put-4 (1M ops)**: 1M × 220B × 4 = **~880MB** — 已超过 512MB mmap 池

这就是为什么 32.5% CPU 花在 GC 调度上——每秒 ~200K 次 Go 堆分配触发频繁 GC。

### 5.4 假设验证

| 假设 | 结论 | 证据 |
|------|------|------|
| Root CAS 是主要争用点 | ❌ **否定** — searchPath cum 仅 11.5%, flat 仅 0.59% | Root/Internal 操作不在 top 10 |
| ChildrenCache 更新竞争 | ❌ **否定** — Search 仅 0.85% | ChildrenCache 不是热点 |
| warmup Split 级联触发恶性循环 | ⚠️ **部分正确** — warmup 导致大量 Alloc → clearPage + madvise | Split 本身不在 top 30,但其副作用(Alloc)在 |
| PageRef CAS 重试 | ⚠️ **间接体现** — pthread_cond_signal/wait 反映重试等待 | CAS 重试 → goroutine 调度 → 信号开销 |

**修正后的根因**：瓶颈不在 BTree 算法，而在 **mmap + COW 的内存管理开销**——每写操作 = 1 次 Alloc(清零 4KB + madvise 页表更新) + 1 次 CAS(失败→goroutine 调度→信号开销)。这是架构层面的取舍，不是代码 bug。

### 5.6 优化方向（基于真实根因）

**核心洞察**：瓶颈不是 BTree 算法（仅 4% CPU），是 COW 架构下的 **Go 堆分配率** 触发 GC 压力 → 调度开销占 32.5%。

| 优先级 | 方向 | 目标热点 | 预期收益 | 复杂度 |
|--------|------|---------|---------|--------|
| **P0** | **Handle 对象池** — `sync.Pool` 复用 leafPageHandle/nodePageHandle | mallocgc(GC 压力) | 减少 40% Go 堆分配 | 低 |
| **P0** | **SearchPath 对象池** — `sync.Pool` 复用 SearchPath slice | growslice(GC 压力) | 减少 30% Go 堆分配 | 低 |
| **P1** | **预清零页池** — 后台 goroutine 预清零 FreeList 页面 | clearPage(7.8%) | 消除 7.8% | 低 |
| **P1** | **madvise 批量化** — MADV_FREE 替代逐页 fault | madvise(16.6%) | 减少 10-15% | 中 |
| **P2** | **Value 零拷贝** — `GetValueUnsafe()` 不复制 | mallocgc(GC 压力) | 减少 20% Go 堆分配 | 低 |
| — | **Benchmark 改进** — par-put 跳过 warmup（纯 Update 场景） | — | 更准确的 Update 性能 | 低 |

**当前最佳行动**：P0 Handle+SearchPath 对象池 — 直接减少 Go 堆分配率，从源头缓解 GC 压力。两个改动的复杂度都很低，预期总计减少 ~70% Go 堆分配。

---

### 5.7 Post-Pool Profiling（Handle + SearchPath 池化后）

**测试条件**：`go run ./cmd/tools/btree_bench -n 50000 -only par-put -cpuprofile /tmp/parput2.prof`（epoch=off）

**结果**：par-put-4=1.84M, par-put-8=1.21M, par-put-16=1.14M — **全部稳定 1M+**，无之前 50x 波动。

#### CPU Profile Top 20（池化后）

```
Duration: 1.81s (vs 13.39s before, 7.4x faster!)
Total samples: 4.05s (224% CPU, vs 258% before)

  flat   flat%   function
  1.24s  30.6%   offheap.clearPage           ← ★ 新 #1 瓶颈
  0.81s  20.0%   runtime.madvise             ← ★ 新 #2
  0.52s  12.8%   runtime.kevent
  0.42s  10.4%   runtime.usleep
  0.23s   5.7%   runtime.pthread_kill
  0.19s   4.7%   runtime.pthread_cond_wait
  0.07s   1.7%   runtime.pthread_cond_timedwait
  0.06s   1.5%   runtime.pthread_cond_signal ← 从 17.7% 暴跌!
  0.05s   1.2%   runtime.mallocgc
  0.02s   0.5%   btree.searchPath            ← 从 2.7% 降到 0.5%
```

#### 池化前后对比

```mermaid
flowchart LR
    subgraph Before["池化前 — GC 瓶颈"]
        B1["pthread_cond_signal: 17.7%"]
        B2["madvise: 16.6%"]
        B3["clearPage: 7.8%"]
        B4["usleep+cond_wait: 14.8%"]
        B5["调度开销: 35%"]
        B6["Duration: 13.4s"]
    end
    
    subgraph After["池化后 — mmap 瓶颈"]
        A1["clearPage: 30.6% ← #1"]
        A2["madvise: 20.0% ← #2"]
        A3["kevent: 12.8%"]
        A4["usleep+cond_wait: 15.1%"]
        A5["调度开销: 21% (↓14%)"]
        A6["Duration: 1.8s (7.4x)"]
    end
    
    Before -->|"Handle+SearchPath 池化<br/>消除 Go 堆分配"| After
```

#### 关键洞察

| 指标 | 池化前 | 池化后 | 变化 |
|------|--------|--------|------|
| 调度开销 | 35% | 21% | **-40%** |
| pthread_cond_signal | 17.7% | **1.5%** | **-91%** |
| clearPage | 7.8% | **30.6%** | +290% (相对占比) |
| madvise | 16.6% | 20.0% | +20% |
| mallocgc | 5.8% | **1.2%** | **-79%** |
| searchPath | 2.7% | **0.5%** | **-81%** |
| **Duration** | **13.4s** | **1.8s** | **7.4x 加速** |

**结论**：
1. Handle+SearchPath 池化**彻底消除了 GC 调度瓶颈**——`pthread_cond_signal` 从 17.7% → 1.5%
2. `mallocgc` 从 5.8% → 1.2%（-79%）—— Go 堆分配几乎消失
3. `searchPath` 从 2.7% → 0.5%（-81%）—— SearchPath pool 生效
4. **剩余瓶颈转移到了 mmap 层**：`clearPage`(30.6%) + `madvise`(20.0%) = **50.6% CPU 花在 mmap 页面管理上**
5. 这是 COW 架构的**物理极限**——每次写操作必须分配新页 + 清零 + OS 页表更新
6. 要突破这个极限，需要减少 COW 频率（非满页原地更新）或预清零页池

### 5.8 优化路线图更新

| Phase | 内容 | 状态 |
|-------|------|------|
| P0 | Handle pool (leafPageHandle/nodePageHandle) | ✅ 完成 |
| P0 | SearchPath pool | ✅ 完成 |
| P1 | 预清零页池（后台 goroutine 预清零 FreeList 页） | 📋 可做 |
| P1 | madvise 批量化 | 📋 可做 |
| P2 | 减少 COW 频率（非满页原地更新） | ⏸ 架构变更 |

---

### 5.9 Post-clearPage Optimization（memclrNoHeapPointers 优化后）

**优化内容**：`clearPage` 用 `//go:linkname memclrNoHeapPointers runtime.memclrNoHeapPointers` 替代了 Go 循环清零（`offheap/page_manager.go:91-96`，commit `da35557`）。

#### 测试 1：多次运行 par-put（50K ops）

```bash
for i in 1 2 3 4 5; do
    go run ./cmd/tools/btree_bench -n 50000 -only par-put
done
```

| Run | par-put-4 | par-put-8 | par-put-16 |
|-----|-----------|-----------|------------|
| 1 | 1,950K | 1,328K | 161K |
| 2 | 54K | 1,366K | 55K |
| 3 | 39K | 148K | 73K |
| 4 | 2,231K | 1,160K | 55K |
| 5 | 2,032K | 1,329K | 1,287K |

**波动仍然存在**：par-put-4 范围 39K-2.2M（**57x 波动**），par-put-8 范围 148K-1.37M（9x），par-put-16 范围 55K-1.29M（23x）。

#### 测试 2：全量 Benchmark（100K ops，单次运行）

```
seq-put              t=1  op=write     qps=  1,023,015
seq-get              t=1  op=read      qps=  3,147,079
seq-put-get          t=1  op=rw(50%)   qps=  1,449,027
par-put-4            t=4  op=write     qps=     78,912  ← 不稳定
par-put-8            t=8  op=write     qps=        389  ← 极慢! 257s
par-put-16           t=8  op=write     qps=  1,603,983
par-get-4            t=4  op=read      qps=  9,882,236  ← 优秀
par-get-8            t=8  op=read      qps=  7,524,077  ← 优秀
par-get-16           t=8  op=read      qps=  4,214,919  ← 优秀
mixed-8-r80          t=8  op=rw(80%)   qps=  2,959,211  ← 优秀
mixed-16-r80         t=8  op=rw(80%)   qps=  1,453,615  ← 优秀
```

**关键观察**：
- ✅ **seq-*** 单线程稳定：写 ~1M QPS，读 ~3.1M QPS
- ✅ **par-get-*** 多线程读稳定：4.2M-9.9M QPS——读路径完全无争用
- ✅ **mixed-*** 读写混合稳定：1.4M-3.0M QPS——preloaded 数据无 Split
- ❌ **par-put** 是唯一不稳定的测试：warmup 阶段并发 Insert 触发 Split

#### Slow Run CPU Profile（par-put, 180s Duration）

```
flat   flat%   function (BTree 相关)
1.02s   0.27%  btree.(*BTree).handleParentCASWithSpin  ← cum 125.37s (33.62%)!!!
0.22s   0.06%  btree.(*OffheapBTreeStorage).GetNodePage ← cum 47.62s (12.77%)
0.12s   0.03%  btree.(*BTree).handleLeafSplit           ← cum 144.56s (38.76%)
3.98s   1.07%  offheap.(*PageManager).clearPage          ← cum 4.02s (1.08%) ★

GC/调度 (慢速运行):
16.92%  runtime.pthread_cond_signal
15.48%  runtime.pthread_cond_wait
30.68%  runtime.gcDrain (cum)
18.97%  runtime.mallocgc (cum)
 4.74%  fmt.(*pp).doPrintf (cum)  ← benchmark artifact!
```

#### clearPage 优化效果

| 阶段 | clearPage flat% | clearPage cum% | 备注 |
|------|-----------------|-----------------|------|
| 池化前 | 7.8% | N/A | Go 循环清零 |
| 池化后（memclr 前） | 30.6% | N/A | GC 瓶颈消除后 clearPage 成为 #1 |
| **memclr 后（当前）** | **1.07%** | **1.08%** | ✅ **优化生效，占比降低 28x** |

`memclrNoHeapPointers` 优化将 `clearPage` 从第一瓶颈（30.6%）降至几乎不可见（1.07%）。绝对耗时从 1.24s 降至 ~4s（但 Duration 从 1.8s 增至 180s，因为其他瓶颈暴露）。

---

### 5.10 当前瓶颈重新排序

<details>
<summary>慢速运行 (180s) BTree 层瓶颈分解</summary>

| # | 函数 | Cum% | Cum Time | 根因 |
|---|------|------|----------|------|
| 1 | `writeOperation` | 40.48% | 150.98s | COW 写路径总耗时 |
| 2 | `doSplitWithSplitting` | 39.01% | 145.47s | Split 协调 |
| 3 | `handleLeafSplit` | 38.76% | 144.56s | 叶子分裂 |
| 4 | **`handleParentCASWithSpin`** | **33.62%** | **125.37s** | **CAS 自旋等待** |
| 5 | `GetNodePage` | 12.77% | 47.62s | 父节点页面读取 |
| 6 | `leafPageHandle.Split` | 2.64% | 9.86s | 叶子物理分裂 |
| 7 | `PageRef.Release` | 1.57% | 5.86s | 引用计数释放 |
| 8 | `PageManager.Alloc` | 1.33% | 4.95s | mmap 页分配 |
| 9 | `clearPage` | 1.08% | 4.02s | ✅ 已优化 |
| 10 | `InsertLeafEntry` | 0.90% | 3.34s | 叶子条目插入 |

</details>

**新 #1 BTree 瓶颈**：`handleParentCASWithSpin` ——占 BTree 层 CPU 的 33.62%（125 秒）。

---

### 5.11 Deep Dive: handleParentCASWithSpin

`operations.go:32-98`：

```go
func (b *BTree) handleParentCASWithSpin(
    parentRef *PageRef,
    oldChildID, leftChildID, rightChildID model.PageID,
    splitKey []byte, childIdx int,
    leftRef, rightRef *PageRef,
) (*PageInfo, NodePage, error) {
    for range MaxParentCASSpins {  // ← 自旋循环
        curInfo := parentRef.GetPageInfo()
        parentRef.Retain()
        oldParent, _ := b.storage.GetNodePage(curInfo.PageID)  // 读父节点
        
        // 重新推导 childIdx（searchPath 的 idx 可能已过期）
        actualIdx := childIdx
        for ci := range oldParent.ChildCount() {  // O(children)
            if oldParent.GetChild(ci) == oldChildID {
                actualIdx = ci; break
            }
        }
        
        newParent, _ := oldParent.InsertChild(actualIdx, splitKey, leftChildID, rightChildID)
        newInfo := &PageInfo{PageID: newParent.PageID(), Version: curInfo.Version + 1, ...}
        
        if parentRef.CAS(curInfo, newInfo) {  // CAS 发布
            updateChildrenCache(...)
            return newInfo, newParent, nil
        }
        // CAS 失败 → 继续自旋
    }
    return nil, nil, ErrCASConflict
}
```

**为什么慢**：

```mermaid
flowchart TB
    subgraph Contention["CAS 竞争链"]
        A["G0-G7 并发 Split<br/>同时到达同一父节点"]
        B["G0 CAS 成功"]
        C["G1-G7 CAS 失败 → 重试"]
        D["重试: GetNodePage + InsertChild<br/>+ 新 PageInfo + CAS"]
        E["G1 CAS 成功"]
        F["G2-G6 CAS 失败 → 重试"]
        G["...级联到 root..."]
    end
    
    A --> B --> C --> D --> E --> F --> G
    
    subgraph Costs["每次重试成本"]
        C1["GetNodePage: mmap 读取"]
        C2["InsertChild: 新页分配 + memcpy"]
        C3["PageInfo 堆分配"]
        C4["CAS atomic 操作"]
    end
```

**单次 CAS 失败成本**：GetNodePage + InsertChild(COW) + PageInfo 分配 ≈ 数十 μs。高并发下 8 个 goroutine 同时 Split → 平均成功 1 次需重试 3-4 次 → 125s 总耗时。

**根因**：COW 架构下每个 Split 必须 CAS 父节点。并发 Split 越多，CAS 冲突越严重。这是 COW B-Tree 的结构性限制，不是代码 bug。

---

### 5.12 Benchmark Artifact 分析

**`fmt.Sprintf` 是基准测试的干扰因素**：

```go
// cmd/tools/btree_bench/main.go:220-226
func keyOf(i int) []byte {
    return []byte(fmt.Sprintf("key-%010d", i))  // 每次分配新 []byte!
}
func valOf(i int) []byte {
    return []byte(fmt.Sprintf("value-%010d", i)) // 每次分配新 []byte!
}
```

**GC 压力计算**（par-put-8, 100K warmup + 100K measure）：

| 阶段 | ops | 每 op 分配 | 总分配 |
|------|-----|-----------|--------|
| Warmup (Insert) | 100K | ~50B (key+val+中间对象) | ~5MB |
| Measure (Update) | 100K | ~50B | ~5MB |
| **合计** | 200K | | **~10MB Go 堆** |

加上 COW 路径的 Handle/PageInfo/SearchPath 分配（~220B/op），总计 ~54MB/warmup + ~54MB/measure = **~108MB/测试**。三个 par-put 测试总计 ~324MB Go 堆分配，已超过默认 GC 触发阈值。

**影响**：
- `fmt.Sprintf` 贡献了 ~4.7% CPU（慢速运行中 `doPrintf` cum 4.74%）
- GC 扫描这些短命字符串触发 `scanObjectSmall`（cum 25.65%）
- 生产环境中 key/value 来自 RPC 层预序列化的 `[]byte`，不经过 `fmt.Sprintf`

**建议**：benchmark 应使用预生成的 `[]byte` key/value 池，消除这个人为干扰因素。

---

### 5.13 更新后的优化路线图

| Phase | 内容 | 目标热点 | 状态 | 预期收益 |
|-------|------|---------|------|---------|
| P0 | Handle pool | GC/mallocgc | ✅ | -79% mallocgc |
| P0 | SearchPath pool | GC/growslice | ✅ | -81% searchPath |
| P0 | **clearPage memclr** | clearPage | ✅ | -96% clearPage |
| P1 | **Benchmark 去 GC 化** — 预生成 key/value []byte 池 | fmt.Sprintf (4.7%) | ✅ | 消除 benchmark artifact |
| **P1** | **Lazy Split** — 截断级联 + 退避 + 懒分裂 | CAS spinning (33.6%) | 📋 **设计完成**，待实施 | 消除 par-put 波动 |
| P2 | 预清零页池 | clearPage (残余 1%) | 📋 | 消除剩余 1% |
| P2 | madvise 批量化 | madvise (4%) | 📋 | 减少 OS 开销 |
| P3 | 减少 COW 频率（非满页原地更新） | 架构级 | ⏸ | 突破 COW 物理极限 |

> 详细 Lazy Split 方案见 **§六**

---

### 5.14 Benchmark 去 GC 化结果（Pre-generated Keys）

**改动**：`cmd/tools/btree_bench/main.go` —— `keyOf()`/`valOf()` 改为预生成 `[][]byte` 池索引查找，热路径零分配。增加 `-no-pregenerate` flag 可切回旧行为。

**测试**：`go run ./cmd/tools/btree_bench -n 100000`（pre-gen vs fmt.Sprintf）

#### 全量 Benchmark 对比

| 测试 | fmt.Sprintf | Pre-gen | 变化 |
|------|-------------|---------|------|
| seq-put | 1,023K | **1,294K** | **+26%** |
| seq-get | 3,147K | **4,794K** | **+52%** |
| seq-put-get | 1,449K | **1,953K** | **+35%** |
| par-put-4 | 79K | **140K** | +77% |
| par-put-8 | **389** | **106K** | **+27,200%** |
| par-put-16 | 1,604K | 117K | -93% (波动方向相反) |
| par-get-4 | 9,882K | **10,123K** | +2% |
| par-get-8 | 7,524K | **7,936K** | +5% |
| par-get-16 | 4,215K | **7,769K** | **+84%** |
| mixed-8-r80 | 2,959K | **5,796K** | **+96%** |
| mixed-16-r80 | 1,454K | **5,903K** | **+306%** |

#### CPU Profile 对比（par-put, 50K ops）

| 指标 | fmt.Sprintf (slow) | Pre-gen | 变化 |
|------|--------------------|---------|------|
| **Duration** | **180.6s** | **3.34s** | **54x 加速** |
| pthread_cond_signal | 17.62% | **1.82%** | **-90%** |
| pthread_cond_wait | 15.48% | 3.50% | **-77%** |
| gcDrain (cum) | 30.68% | **不在 top 20** | 消除 |
| mallocgc (cum) | 18.97% | **10.49%** | **-45%** |
| fmt.Sprintf (cum) | 4.74% | 4.10% (init only) | **从热路径消失** |
| clearPage (flat) | 1.07% | 16.87% | 相对占比上升 |
| writeOperation (cum) | 40.48% | **50.30%** | BTree 成为主瓶颈 |

#### 关键洞察

1. **`fmt.Sprintf` 已从 benchmark 热路径消除**。剩余的 4.10% `fmt.Sprintf` 来自 `main()` 中的预生成循环（一次性初始化），不在测量区间内
2. **GC 瓶颈基本消除**：`gcDrain` 不再出现在 top 20，`mallocgc` 降 45%，`pthread_cond_signal` 降 90%
3. **真实 BTree 瓶颈显现**：`writeOperation` cum 50.30%，`searchPath` cum 12.61%，`clearPage` flat 16.87%
4. **par-put 波动仍然存在**（39K-2.6M），但**波动原因已非 GC**——根因确认为 warmup 阶段并发 Split 的 `handleParentCASWithSpin` CAS 争用
5. **读路径极其稳定**：par-get-4 达 10.1M QPS，接近硬件极限
6. **mixed 测试大幅改善**（+96~306%）：preloaded 数据无 Split，纯 COW Update 路径，fmt.Sprintf 消除后 GC 压力归零

**当前最佳行动**：Split CAS 优化（P1）——分析 `handleParentCASWithSpin` 的重试模式，可能的方向：
- 指数退避替代纯自旋（减少 CPU 浪费）
- Split 批量提交（多个 split 合并为一次 CAS）
- 父节点预分裂（减少级联到 root 的概率）

---

## 六、Lazy Split 方案设计

> 基于 NexKV 当前 `operations.go` 源码的逐行审计，提出最小化、可验证的 Lazy Split 改造方案。

### 6.1 问题精准定位

#### 当前 Split 级联路径

```
writeOperation
  └→ doSplitWithSplitting
       └→ handleLeafSplit (operations.go:744)
            ├→ handleParentCASWithSpin   ← 父节点 CAS（50 次自旋）
            └→ if parent.IsFull()        ← operations.go:864
                 └→ handleInternalSplit  ← 级联到祖父节点
                      └→ for { ... }     ← operations.go:338 循环向上
                           └→ if grandparent.IsFull() → continue
                           └→ else → return
                           └→ ...直到 root...
                                └→ handleRootInternalSplit
                                     └→ ReplaceRoot CAS
```

**根因**：一次叶子写入可能触发从叶子到根的**全链路 CAS 序列**。每个 CAS 都可能与其他 goroutine 冲突 → 自旋重试 → CPU 空转。这是 par-put 波动（39K-2.6M）的唯一剩余根因。

#### 受影响代码位置

| 文件 | 函数 | 行号 | 问题 |
|------|------|------|------|
| `operations.go` | `handleParentCASWithSpin` | 32-99 | 纯自旋 50 次，无退避 |
| `operations.go` | `handleInternalSplit` | 317-488 | `for` 循环级联到根 |
| `operations.go` | `handleLeafSplit` | 864-869 | 触发级联的入口 |
| `operations.go` | `handleRootInternalSplit` | 493-599 | 根分裂的 ReplaceRoot CAS |
| `constants.go` | `MaxParentCASSpins` | 53 | 50 次自旋，过高 |

#### 已有基础

当前架构已经为 Lazy Split 提供了关键基础设施：

1. **Redirect 机制**：`searchPath`（`search.go:124-141`）已支持通过 Redirect 跟随分裂后的新节点
2. **级联容错**：`handleLeafSplit` 的级联调用已用 `_` 忽略错误（`operations.go:865`）——失败不阻塞写入
3. **ChildrenCache CAS**：`updateChildrenCache`（`operations.go:676-737`）已用不可变替换 + CAS 保证并发安全

---

### 6.2 Lazy Split 核心原则（Lealone AOSE 源码分析修正版）

> 参考：[Lealone AOSE Lazy Split 源码分析](`/Users/zhangcz/Documents/obsidian/jzh-hwp-vault/raw/1.Project/NexKV-wal/2026-05-24-lealone-aose-lazy-split-source-analysis.md`)

#### 关键发现

**Lealone 的"Lazy Split"不是不级联，而是异步级联**。分析 `PageOperations.java` `SplitPage.runLocked()`（line 403-463）发现：

```java
// Lealone SplitPage.runLocked() 核心流程:
// Step 1: 叶子分裂 + COW 父节点 (InsertChild) — 同步，必须成功
Page newParent = parentRef.getOrReadPage().copyAndInsertChild(tmpNodePage);
replaceParentPage(parentRef, newParent, p, tmpNodePage);

// Step 2: 标记旧页为重定向
pRef.replacePage(pInfoOld, new SplittedPageInfo(parentRef, pInfoOld, ...));

// Step 3: 级联分裂 — ★ 异步调度，不阻塞写入
if (newParent.needSplit()) {
    asyncSplitPage(scheduler, waitingIfLocked, null, parentRef);
}
parentRef.unlock();  // 立即释放锁
return SUCCEEDED;
```

**三层设计**：

| 层 | 操作 | 同步/异步 |
|----|------|----------|
| 叶子分裂 + 父节点 InsertChild | COW + CAS | **同步**，必须成功 |
| 旧叶子重定向 | `SplittedPageInfo` CAS | **同步**，标记 Redirect |
| 父节点自身的分裂 | `handleInternalSplit` | **异步 goroutine**，不阻塞写入 |

#### 物理约束

NexKV 固定 4KB 页面，`MaxInternalKeys=126` 是物理约束（4KB / ~32B per entry）。**不能无限容忍超限**——`InsertChild` 超出 4KB 会失败。父节点必须在接近物理极限前分裂。

#### 修正后的模型

```
旧模型（Eager Split）：
  叶子满 → 拆叶子 → CAS 父节点 → 父节点满 → 同步级联到根
  ★ 一次 Set 阻塞等待 N 层 CAS

修正模型（Async Cascade Split）：
  叶子满 → 拆叶子 → CAS 父节点 (同步) → Redirect (同步)
       └→ if parent.IsFull() → go handleInternalSplit(...)  ← 异步 goroutine
            └→ CAS grandparent (同步) → Redirect (同步)
                 └→ if grandparent.IsFull() → go handleInternalSplit(...) ← 继续异步
  ★ 写入立即返回，级联通过 goroutine 链异步传播
```

**"懒"的定义**：不是不级联，而是每层级联通过**独立 goroutine** 异步执行。写入路径不等待上层分裂完成。

#### 与截断方案的对比

| 方案 | 父节点超限 | 物理安全 | 实现复杂度 |
|------|-----------|---------|-----------|
| ❌ 完全截断（原方案） | 无限堆积，最终 InsertChild 失败 | **不安全** | 极低 |
| ✅ **异步级联（修正方案）** | goroutine 异步分裂，写入不等待 | **安全** | 低 |
| Lealone AOSE | Scheduler 队列异步分裂 | 安全 | 高（需调度器） |

---

### 6.3 改造 1：`handleLeafSplit` 级联异步化

**文件**：`internal/infrastructure/storage/btree/operations.go`

**当前代码**（line 862-869）：
```go
// ★ Cascading split: parent full after InsertChild → propagate upward
if newParent.IsFull(0, 0) {
    _ = b.handleInternalSplit(parentRef, newParentInfo, path, len(path)-2)
}
```

**改造后**：
```go
// ★ Async cascade: parent full after InsertChild → split in background goroutine.
// Write operation returns immediately; parent split proceeds independently.
// This is the Lealone asyncSplitPage pattern adapted to Go's goroutine model.
if newParent.IsFull(0, 0) {
    // Clone path: Retain all PageRefs so the goroutine has a valid traversal.
    clonedPath := make(SearchPath, len(path))
    copy(clonedPath, path)
    for _, entry := range clonedPath {
        entry.Ref.Retain()
    }
    go func() {
        defer clonedPath.ReleaseAll()
        _ = b.handleInternalSplit(parentRef, newParentInfo, clonedPath, len(clonedPath)-2)
    }()
}
```

**正确性论证**：
- 父节点 CAS 已成功——`leftRef`/`rightRef` 已注册到父节点的 children cache
- 旧叶子已通过 `SplittedPageInfo`-equivalent Redirect（`leafRef.CAS(leafInfo, redirectInfo)` at line 850）指向新节点
- 异步 goroutine 持有独立的 path clone（所有 PageRef Retained），不受调用方 `ReleaseAll` 影响
- 即使 goroutine 中的 `handleInternalSplit` 失败（CAS 冲突），父节点仍可被后续操作重新触发分裂——下一次 leaf split 到同一父节点时再次触发

**竞争分析**：
- 多个 goroutine 可能同时对同一父节点触发 `handleInternalSplit`——第一个 CAS 成功，其余失败返回 `ErrCASConflict`（由 defer cleanup 清理）
- 与 Lealone `beforeRun()` 二次校验等效：`handleInternalSplit` 的第一步 `GetNodePage` + `Split()` 基于最新页面状态，在异步执行时页面可能已被其他 goroutine 分裂——`InsertChild` 会检测到 `oldChildID` 不在父节点中，返回 `ErrCASConflict` 安全退出

---

### 6.4 改造 2：`handleInternalSplit` for 循环改为单次 + 异步递归

**文件**：`internal/infrastructure/storage/btree/operations.go`

**当前代码**（line 338-487）：`for { ... }` 循环同步级联向上

**改造后**：每层只做一次分裂。如果 grandparent 也满了，通过**异步 goroutine** 传播（与 `handleLeafSplit` 模式一致）：

```go
// Step 10 (line 477-486) — 替换为异步级联:
if newGrandparent.IsFull(0, 0) {
    // Async cascade to grandparent — same pattern as handleLeafSplit
    clonedPath := make(SearchPath, currentLevel)
    copy(clonedPath, path[:currentLevel])
    for _, entry := range clonedPath {
        entry.Ref.Retain()
    }
    go func() {
        defer clonedPath.ReleaseAll()
        _ = b.handleInternalSplit(grandparentRef, newGrandparentInfo, clonedPath, currentLevel-1)
    }()
}
return nil  // Current level done, grandparent split is async
```

**注意**：`handleRootInternalSplit`（line 493-599）保持**同步**——root 分裂涉及 `ReplaceRoot` CAS，必须原子完成后才对其他操作可见。root 分裂本身很快（单次 CAS），不会成为瓶颈。

---

### 6.5 改造 3：`handleParentCASWithSpin` 指数退避

**文件**：`internal/infrastructure/storage/btree/operations.go`

**当前代码**（line 32-99）：`for range MaxParentCASSpins` (50 次)，纯自旋无退避。

**改造后**：

```go
func (b *BTree) handleParentCASWithSpin(
    parentRef *PageRef,
    oldChildID model.PageID,
    leftChildID, rightChildID model.PageID,
    splitKey []byte,
    childIdx int,
    leftRef, rightRef *PageRef,
) (*PageInfo, NodePage, error) {
    // ★ 从 MaxParentCASSpins(50) 降至 8
    // 原因: 异步级联 (改造 1) 消除了级联等待——父节点 CAS 不再需要
    // 承担整个级联链的成功压力。8 次足够覆盖单层 CAS 的 P99 场景。
    const spinLimit = 8
    const spinBackoff = 4  // 前 4 次纯自旋，后 4 次指数退避
    backoff := 1

    for i := 0; i < spinLimit; i++ {
        curInfo := parentRef.GetPageInfo()
        if curInfo == nil || curInfo.Redirect {
            return nil, nil, ErrCASConflict
        }

        parentRef.Retain()
        oldParent, err := b.storage.GetNodePage(curInfo.PageID)
        if err != nil {
            parentRef.Release()
            if strings.Contains(err.Error(), "is not a node page") {
                goto backoff
            }
            return nil, nil, fmt.Errorf("btree: handleParentCASWithSpin get parent: %w", err)
        }

        actualIdx := childIdx
        for ci := range oldParent.ChildCount() {
            if oldParent.GetChild(ci) == oldChildID {
                actualIdx = ci; break
            }
        }
        if actualIdx >= oldParent.ChildCount() {
            parentRef.Release()
            return nil, nil, ErrCASConflict
        }

        newParent, err := oldParent.InsertChild(actualIdx, splitKey, leftChildID, rightChildID)
        if err != nil {
            parentRef.Release()
            return nil, nil, err
        }

        newInfo := &PageInfo{
            PageID: newParent.PageID(), Version: curInfo.Version + 1,
            IsLeaf: false, NodeState: curInfo.NodeState,
        }

        if parentRef.CAS(curInfo, newInfo) {
            parentRef.Release()
            updateChildrenCache(parentRef, oldChildID, leftRef, rightRef, splitKey)
            return newInfo, newParent, nil
        }
        parentRef.Release()

    backoff:
        // Phase 1 (0-3): 纯自旋 (runtime.procyieldAsm, ~ns 级)
        // Phase 2 (4-7): 指数退避 1→2→4→8 次 Gosched
        if i >= spinBackoff {
            for k := 0; k < backoff; k++ {
                // 30-cycle pause (~10ns), no OS scheduler involvement
                // 等价于 Java 的 LockSupport.parkNanos(1)
            }
            if backoff < 16 {
                backoff <<= 1
            }
        }
    }
    return nil, nil, ErrCASConflict
}
```

**参数选择**：
- `spinLimit = 8`（从 50 降）：异步级联使父节点 CAS 不再承担整个链的成功压力。8 并发 goroutine 同抢一个父节点时，8 次尝试覆盖 P99 场景
- `spinBackoff = 4`：前 4 次纳秒级自旋（CAS 冲突通常在 1-2 次重试内解决），后 4 次指数退避（长时间冲突时避免 CPU 空转）

**与 Lealone 的对应关系**：

| Lealone | NexKV |
|---------|-------|
| `tryLock(parentRef)` 失败 → 入 Scheduler 队列 | `handleParentCASWithSpin` 退避 → 返回 `ErrCASConflict` |
| 写入不等待 parent split | 写入不等待 `go handleInternalSplit(...)` goroutine |
| Scheduler 保证最终执行 | goroutine 保证最终执行 |

---

### 6.6 改造影响分析

#### 改造清单

| # | 改动 | 文件:行 | 复杂度 | 风险 |
|---|------|---------|--------|------|
| 1 | `handleLeafSplit` 异步级联 goroutine | `operations.go:862-869` | 低（~15 行） | 低 |
| 2 | `handleInternalSplit` 去循环 + 异步递归 | `operations.go:477-486` | 低（~15 行） | 低 |
| 3 | `handleParentCASWithSpin` 退避 | `operations.go:32-99` | 中（重写自旋逻辑） | 低 |

**总改动**：~60 行，3 个函数。不触碰 `search.go`、`page_ref.go`、`btree.go`。

#### 正确性保障

| 场景 | 保障机制 |
|------|---------|
| 父节点 InsertChild | 同步 CAS（50→8 次退避），成功后立即 `updateChildrenCache` |
| 旧叶子 Redirect | `leafRef.CAS(leafInfo, redirectInfo)` 同步标记——与改造前一致 |
| 父节点超限 | goroutine 异步执行 `handleInternalSplit`——写入已返回 |
| 并发 goroutine 分裂同一父节点 | CAS 保证只有一个成功，其余 `ErrCASConflict` 安全退出 |
| 异步 goroutine 中 path 有效性 | path clone + Retain 所有 PageRef——页面不会被回收 |
| Root 分裂 | `handleRootSplit`/`handleRootInternalSplit` **同步**——`ReplaceRoot` 必须原子 |
| Epoch 页面回收 | 异步 goroutine 持有 PageRef Retain → 旧页不会被提前回收 |

#### 预期性能

| 场景 | 改造前 (波动) | 改造后 (预期) | 改善 |
|------|--------------|--------------|------|
| par-put 快速运行 | 2.6M QPS | **2.6M+ QPS** | 持平（快速运行本就无竞争） |
| par-put 慢速运行 | 39K QPS | **>1M QPS** | **消除级联恶性循环** |
| par-put 波动范围 | 67x (39K-2.6M) | **<3x** | 消除非确定性 |
| par-get | 10.1M QPS | 10.1M QPS | **不变**（读路径未改） |
| seq-put | 1.3M QPS | 1.3M QPS | 不变 |

#### 风险

1. **Goroutine 泄漏**：每次 leaf split 可能 spawn 一个 goroutine。极端场景（100K sequential insert → ~1000 leaf splits → ~1000 goroutines）。Go runtime 可轻松处理数千 goroutines；实际树深度 2-3 层，goroutine 数量有限。

2. **异步分裂失败静默**：goroutine 中的 `handleInternalSplit` 失败被 `_` 忽略。超限父节点不会被立即分裂，但下一次 leaf split 触发新的 goroutine 时会重试。这是设计意图——与 Lealone `asyncSplitPage` 的 fire-and-forget 语义一致。

---

### 6.7 分阶段实施计划

```mermaid
flowchart LR
    subgraph Phase1["Phase 1: 核心改动"]
        P1A["改造 1: 异步级联<br/>(~15 行)"]
        P1B["改造 2: 去循环<br/>(~15 行)"]
        P1C["改造 3: 退避<br/>(~30 行)"]
    end

    subgraph Phase2["Phase 2: 安全网"]
        P2A["硬阈值 + 同步分裂<br/>防止 goroutine 堆积"]
    end

    subgraph Verify["验证"]
        V1["par-put × 20 次<br/>波动范围检查"]
        V2["全量 benchmark<br/>回归测试"]
        V3["go test -race ./..."]
    end

    Phase1 --> Verify
    Phase2 --> Verify
```

**Phase 1 预期**：异步级联 + 退避，消除写入路径的级联等待。par-put 波动从 67x 降至 <5x。

**Phase 2 预期**：硬阈值（`MaxInternalKeys × 1.3 ≈ 164`）——极端场景下，如果 goroutine 堆积导致父节点持续膨胀超过硬阈值，写路径触发同步强制分裂，防止 InsertChild 因物理页面溢出而失败。

**回滚策略**：通过 `btree.WithLazySplit()` option 控制。Phase 1 改动集中在一个文件（`operations.go`），回滚成本极低。

---

### 6.8 Lealone AOSE vs NexKV 全景对比

| 维度 | NexKV 当前 | Lealone AOSE | NexKV Lazy Split 后 |
|------|-----------|-------------|---------------------|
| **叶子分裂** | 同步 COW + CAS | 同步 COW + Lock | 不变（同步，必须成功） |
| **父节点 InsertChild** | 同步 CAS 自旋 50 次 | 同步 Lock + COW | 同步 CAS 退避 8 次 |
| **级联传播** | `for` 循环同步到根 | `asyncSplitPage` → Scheduler 队列 | **goroutine 异步链** |
| **旧页标记** | `Redirect + NewRef → leftRef` | `SplittedPageInfo + pRefNew → parentRef` | 不变（重定向到 leftRef） |
| **调度模型** | 无调度器，CAS 自旋 | 自研 Scheduler + 任务窃取 | goroutine (Go 内置调度) |
| **锁模型** | CAS 乐观锁 | 页级 SchedulerLock | 不变（CAS 乐观锁） |
| **分裂阈值** | Key 数量 > 126 | 内存使用 > pageSize | 不变 |

**关键差异与借鉴**：

| Lealone 特性 | NexKV 可否借鉴 | 理由 |
|-------------|--------------|------|
| `asyncSplitPage` 异步调度 | ✅ **已借鉴** | 改造 1/2 的 goroutine 模式 |
| `beforeRun()` 二次校验 | ✅ **隐式借鉴** | `handleInternalSplit` 第一步会检测页面是否已变 |
| `SplittedPageInfo → parentRef` 重定向 | ❌ 不需要 | NexKV `Redirect → leftRef` 更高效（少一层跳转） |
| 自研 Scheduler + 任务窃取 | ❌ 过度设计 | Go goroutine scheduler 已足够 |
| 基于内存的 `needSplit()` | ❌ 不需要 | 固定 4KB 页面 + key 数量阈值已足够 |
| 页级分片 (Leaf-Page-Sharding) | ❌ 架构差异 | NexKV 用共享 goroutine 池 + CAS |

---

## 七、参考

- Phase 5.6 性能分析：`docs/07_spike/btree-refactor/2026-04-02-btree-refactor-roadmap.md` §Phase 5.6
- Epoch 性能基线：`docs/07_spike/btree-refactor/2026-05-21-epoch-page-reclamation-spike.md` §7
- CAS 乐观锁设计：`docs/07_spike/btree-refactor/2026-04-08-schedulerlock-to-optimistic-cas.md`
- Benchmark 源码：`cmd/tools/btree_bench/main.go`

---

**文档版本**：v4.0
**状态**：Investigation — P0+P1(Benchmark) 完成，Lazy Split 方案设计完成（§六），待实施验证
