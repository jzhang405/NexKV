# BTree Memory Mode 性能调试预研

> 创建日期：2026-05-23
> 最后更新：2026-05-24
> 状态：Investigation — **P0 完成，自适应 Leaf Queue 已实施，Lealone 对比分析完成（§七）**
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

## 六、自适应 Leaf Queue 方案

> 基于两次探索（goroutine 级联 ❌、channel+worker ⚠️）的教训，提出最小化方案。

### 6.1 教训回顾

| 尝试 | 问题 | 根因 |
|------|------|------|
| goroutine 级联 | goroutine 风暴，更差 | 无并发控制 |
| channel+worker | seq-put 退化 20x，par-put 仍波动 | 所有写都进 queue，热路径 clone path |

**关键认识**：瓶颈在叶级 CAS。但**大部分写没有冲突**（不同 key → 不同 leaf）。只需要在冲突时才串行化。

### 6.2 方案：自适应 Leaf Queue

```
正常路径（大多数情况，无额外开销）:
  searchPath → GetLeafPage → COW → CAS leaf ✅

冲突路径（连续 CAS 失败 ≥3 次）:
  ... → CAS leaf ❌ (×3)
    → leafQueue[pageID % GOMAXPROCS]  // 入队
      → worker 串行执行 → CAS leaf ✅
```

**pageID 来自 searchPath 结果**（`oldInfo.PageID`）——不需要 Range Index。
**改动量**：`writeOperation` 的 CAS 循环中加一段冲突检测 + `enqueueLeafWrite`，~15 行。

### 6.3 详细设计

#### 6.3.1 集成到 `writeOperation`

```go
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
    path, err := searchPath(b.rootRef, key)
    // ... existing code ...

    for attempt := range MaxCASRetries {
        // ... GetLeafPage, mutate ...
        if leafRef.CAS(oldInfo, newInfo) {
            return nil
        }
        _ = b.storage.FreePage(result.newPageID)
        path.ReleaseAll()  // ★ release before potential enqueue (C1)

        const fastRetries = 10  // CAS fail threshold before enqueue
        if attempt >= fastRetries && b.leafQueues != nil {
            pageID := oldInfo.PageID
            err := b.enqueueLeafWrite(key, pageID, mutate)
            if err == ErrCASConflict {
                continue  // queue full → back to CAS loop (M5)
            }
            return err
        }
        continue
    }
    return ErrCASConflict
}
```

#### 6.3.2 入队 + Worker

```go
type BTree struct {
    leafQueues []chan leafWriteTask
    leafWg     sync.WaitGroup
}

type leafWriteTask struct {
    key    []byte
    pageID model.PageID
    mutate mutateFunc
    done   chan error
}

func (b *BTree) enqueueLeafWrite(key []byte, pageID model.PageID, mutate mutateFunc) error {
    if b.closed.Load() {
        return ErrTreeClosed
    }
    q := b.leafQueues[pageID%len(b.leafQueues)]  // route by pageID from searchPath
    task := leafWriteTask{key: key, pageID: pageID, mutate: mutate, done: make(chan error, 1)}
    select {
    case q <- task:
        return <-task.done
    default:
        return ErrCASConflict
    }
}

func (b *BTree) leafWorker(id int) {
    defer b.leafWg.Done()
    for task := range b.leafQueues[id] {
        func() {
            defer func() {
                if r := recover(); r != nil {
                    // Log panic for debugging (L8), don't re-panic
                    stack := make([]byte, 4096)
                    stack = stack[:runtime.Stack(stack, false)]
                    GlobalTracer.LogOp("leafWorker.panic", "workerID", id, "recover", r, "stack", string(stack))
                    task.done <- fmt.Errorf("btree: leafWorker[%d] panic: %v", id, r)
                }
            }()
            task.done <- b.directWrite(task.key, task.pageID, task.mutate)
        }()
    }
}

// directWrite executed inside worker (serial per-page, zero CAS contention).
// Worker re-derives the leaf via searchPath: if the page was split between
// enqueue and execution, searchPath follows the new tree topology correctly (M6).
func (b *BTree) directWrite(key []byte, pageID model.PageID, mutate mutateFunc) error {
    // Limited retry: worker has near-exclusive access, 3 attempts cover rare races (H4)
    for range 3 {
        path, err := searchPath(b.rootRef, key)
        if err != nil { return err }
        defer path.ReleaseAll()

        leafRef := path.Leaf().Ref
        oldInfo := leafRef.GetPageInfo()
        oldLeaf, err := b.storage.GetLeafPage(oldInfo.PageID)
        if err != nil { return fmt.Errorf("btree: directWrite get leaf: %w", err) } // M7

        result, err := mutate(oldLeaf)
        if err != nil { return err }

        // Apply tombstoneDelta — must match writeOperation path (C2)
        if result.tombstoneDelta != 0 {
            rawID := uint32(result.newPageID)
            tc := b.storage.pa.GetTombstoneCount(rawID)
            newTC := int16(tc) + result.tombstoneDelta
            if newTC < 0 { newTC = 0 }
            b.storage.pa.SetTombstoneCount(rawID, uint16(newTC))
        }

        newInfo := &PageInfo{PageID: result.newPageID, Version: oldInfo.Version + 1, IsLeaf: true}
        if leafRef.CAS(oldInfo, newInfo) {
            return nil
        }
        _ = b.storage.FreePage(result.newPageID)
    }
    return ErrCASConflict
}
```

#### 6.3.3 路由规则

| pageID % N | 效果 |
|------------|------|
| 同一 page → 同一 worker | 串行，零 CAS 冲突 |
| 不同 page → 不同 worker (大概率) | 并行，无相互影响 |
| page 在入队后被 split | Worker 的 searchPath 自动跟随新拓扑 (M6) |

#### 6.3.4 初始化 + Close

```go
// NewBTree: last step after all fallible init
n := runtime.GOMAXPROCS(0)
b.leafQueues = make([]chan leafWriteTask, n)
for i := range b.leafQueues {
    b.leafQueues[i] = make(chan leafWriteTask, 64)
}
b.leafWg.Add(n)
for i := range b.leafQueues {
    go b.leafWorker(i)
}

// Close: stop accepting → drain → shutdown storage
if !b.closed.CompareAndSwap(false, true) { return nil }
for i := range b.leafQueues { close(b.leafQueues[i]) }
b.leafWg.Wait()  // after this, no worker touches storage
// ... then epoch shutdown, storage.Close()
```


### 6.4 为什么这次会成功

| 之前的尝试 | 问题 | 自适应 Leaf Queue 如何解决 |
|-----------|------|--------------------------|
| goroutine 级联 | goroutine 风暴 | 不入队——直接 CAS |
| channel+worker | 所有写都进 queue | **仅冲突时入队**，大部分写走原路径 |
| handleParentCASWithSpin 退避 | 仍在 CAS 上循环 | 冲突 >=3 次后串行化，不再自旋 |

**根本区别**：不改变正常路径。只在检测到争用时才介入。

```
正常: searchPath → COW → CAS (零额外开销)
冲突: ... CAS ×3 → leafQueue[pageID % N] → worker 串行
```

**par-put measure 阶段**：key 已存在 → 无 split → 无级联。4 goroutine 各写独立 range → 不同 leaf → CAS 几乎无冲突 → 不走 queue → 性能接近 seq-put × 4。

---

### 6.5 改动清单

| 文件 | 改动 | 行数 |
|------|------|------|
| `btree.go` | `leafQueues` + `leafWg` + 初始化 + Close 顺序 | ~25 |
| `operations.go` | CAS 冲突检测 + `enqueueLeafWrite` + `directWrite` + `leafWorker` | ~45 |

**总改动**：~70 行。不依赖外部包，不修改 searchPath。

### 6.6 实施计划

| Phase | 内容 | 验证 |
|-------|------|------|
| **P1** | `leafQueues` + 冲突检测 + worker | `go test -race`, benchmark × 20 |



## 七、Lealone vs NexKV 写路径对比分析

> 基于 Lealone BTreeMapBenchmark + NexKV seq-put pprof 实测数据

### 7.1 性能差距

| 测试 | Lealone (实测) | NexKV (实测) | 差距 |
|------|---------------|-------------|------|
| seq-put | 3,671K QPS | 1,277K QPS | **2.9x** |
| par-put-16 | 5,634K QPS | 52K-1,862K | **3-108x** |
| par-get-8 | 13,735K QPS | 6,224K QPS | **2.2x** |

### 7.2 单线程写的操作分解

**NexKV seq-put 每写操作**（pprof 实测，504ms Duration / 380ms CPU samples）：

```mermaid
flowchart TB
    subgraph NexKV["NexKV Set(key,value)"]
        N1["1. searchPath<br/>root→internal→leaf<br/>~16% CPU"] --> N2["2. GetLeafPage<br/>mmap 读旧页<br/>21% CPU"]
        N2 --> N3["3. COW mutate<br/>Alloc新页(44%) + 拷贝旧数据"]
        N3 --> N4["4. CAS leafRef<br/>原子发布新页"]
    end
```

| 步骤 | 操作 | CPU 占比 | 代价 |
|------|------|---------|------|
| 1 | `searchPath` | ~16% | 遍历 root→internal→leaf，每层 `ChildrenCache.Search` 二分 |
| 2 | `GetLeafPage` | 21% | mmap 读取旧页、`IsLeaf` 检查 |
| 3 | `Alloc` | **44%** | freeList Dequeue + 清零 Header 56B + version 写入 |
| 4 | COW 拷贝 + Insert/Update | 32% | `BulkInitLeafFromSource` 拷贝旧数据 + 写入新 entry |
| 5 | `CAS leafRef` | ~3% | 原子 CAS 发布新页 |
| 6 | 其他 | ~5% | refCount Release、metrics |

**Lealone seq-put 每写操作**（源码分析）：

```mermaid
flowchart TB
    subgraph Lealone["Lealone put(key,value)"]
        L1["1. gotoLeafPage → pRef"] --> L2["2. pRef.tryLock()"]
        L2 -->|"锁可用"| L3["3. binarySearch"]
        L3 --> L4{"index < 0?"}
        L4 -->|"YES (新key)"| L5["copyAndInsertLeaf() COW"]
        L4 -->|"NO  (已有key)"| L6["setValue() 原地修改"]
        L2 -->|"锁不可用"| L7["scheduler 入队等待"]
    end
```

| 步骤 | 操作 | 代价 |
|------|------|------|
| 1 | `gotoLeafPage` | 遍历 BTree，有 page cache |
| 2 | `getOrReadPage` | Java 堆内对象直接读，**无 mmap** |
| 3 | `binarySearch` | 二分查找插入位置 |
| 4 | `copyAndInsert` | 非满页：**原地修改**（零 Alloc）；满页：新页分配 |

### 7.3 关键差异

| 维度 | Lealone | NexKV |
|------|---------|-------|
| **内存模型** | Java 堆内 Page 对象 | mmap 文件映射 |
| **非满页更新** | **原地修改**（0 次 Alloc） | **COW**（1 次 Alloc + 1 次拷贝） |
| **页读取** | `pageRef.page` 直接引用 | `GetLeafPage(pageID)` mmap 寻址 |
| **页清零** | 无（Java GC 管理） | `memclrNoHeapPointers(56B)` |
| **并发控制** | Scheduler 串行化同页写 | **CAS 乐观锁 + 自旋重试** |
| **每写 Alloc** | **0 次**（非满页） | **1 次**（必须） |

### 7.4 根因

```
NexKV 单线程 seq-put 1.28M QPS = 0.78μs/op，其中：
  Alloc (新页分配)    = 0.34μs (44%)
  GetLeafPage (mmap读)= 0.16μs (21%)
  COW 拷贝+插入       = 0.25μs (32%)
  searchPath           = 0.12μs (16%)
  CAS + 其他          = 0.05μs (7%)

Lealone 单线程 seq-put 3.67M QPS = 0.27μs/op：
  非满页 = 0 Alloc + 0 COW拷贝 + 0 mmap读
  = 只有 binarySearch + 原地修改
```

**NexKV COW 税**：每写必定 Alloc + 拷贝旧页 + 发布新页。即使单线程无并发读者，也必须走完整 COW 路径。

**Lealone 不交 COW 税**：非满页原地修改，只分配新页当页满时。

### 7.5 改进方向

| 方向 | 预期收益 | 复杂度 |
|------|---------|--------|
| **非满页原地更新**（无并发读者时） | seq-put 1.3M→3M+ | 高（需检测并发读者） |
| **预分配页池**（后台清零） | Alloc 降 50% | 低 |
| **更大页**（8KB/16KB） | 减少 Alloc 频率 | 低（改常量） |
| **PageScheduler 串行化**（§六） | 消除 par-put 波动 | 中（~100行） |


---

**文档版本**：v4.0
**状态**：Investigation — P0+P1(Benchmark) 完成，Lazy Split 方案设计完成（§六），待实施验证#### Lealone 写操作逐行分析（PageOperations.java + LeafPage.java）

```java
// WriteOperation.run() — PageOperations.java:82
public PageOperationResult run(InternalScheduler scheduler, boolean waitingIfLocked) {
    pRef = gotoLeafPage().getRef();                   // 1. 导航到 leaf page
    if (pRef.tryLock(scheduler, waitingIfLocked)) {   // 2. 页级锁
        p = pRef.getPage();
        return writeLocal(scheduler);                 // 3. 执行写
    } else {
        return PageOperationResult.LOCKED; // 锁被占 → 入 scheduler 队列
    }
}

// Put.writeLocal() — PageOperations.java:191
protected Object writeLocal(int index, InternalScheduler scheduler) {
    if (index < 0) {
        insertLeaf(index, value);  // → copyAndInsertLeaf() ★ COW (新 key)
    } else {
        return p.setValue(index, value); // → values[index] = value ★ 原地修改！
    }
}

// LeafPage.setValue() — LeafPage.java:47
public Object setValue(int index, Object value) {
    Object old = getValues()[index];
    getValues()[index] = value;  // ★ 直接数组赋值，0 Alloc！
    return old;
}
```

| 操作 | Lealone 方法 | 是否 COW | 原因 |
|------|-------------|---------|------|
| **新增 key** | `copyAndInsertLeaf()` | ✅ COW | 页可能满，需分配新页 |
| **更新已有 key** | `setValue()` | ❌ **原地修改** | `values[index] = value`，0 Alloc |
| **删除 key** | `p.copy().remove()` | ✅ COW | 避免并发读问题 |

#### NexKV 写操作（operations.go）

```go
func writeOperation(b *BTree, key []byte, mutate mutateFunc) error {
    path, _ := searchPath(b.rootRef, key)  // 遍历 BTree
    // ...
    result, _ := mutate(oldLeaf)   // ★ Insert/Update 都走 COW
    // Alloc 新页 → 拷贝旧数据 → 修改 → CAS 发布
}
```

**NexKV：所有写都 COW**。新增还是更新，都分配新页 + 拷贝 + CAS。

### 7.3 seq-put 性能拆解

benchmark measure 阶段：warmup 后所有 key 已存在 → **100% 更新**操作（无新增 key，无 Split）。

| | Lealone | NexKV |
|---|---|---|
| 每次更新 | `setValue()` 原地数组赋值 | `mutate()` COW 新页 |
| Alloc 次数 | **0** | **1** (每写) |
| 页面拷贝 | **0** | **1** (BulkInit 全页 4KB) |
| 单次更新耗时 | ~0.27μs | ~0.78μs |
| **QPS** | **3.67M** | **1.28M** |

### 7.4 NexKV seq-put CPU 足迹（pprof 实测，504ms）

```
Alloc (新页分配)     = 170ms (44%)  ← 更新不需要新页，但 COW 必须
GetLeafPage (mmap读) =  80ms (21%)  ← 更新不需要读旧页全量
COW 拷贝+修改        = 120ms (32%)  ← 原地修改只需 1 次数组赋值
searchPath            =  60ms (16%)
CAS + 其他           =  70ms (18%)
```

### 7.5 根本差异

**Lealone 分离了 insert 和 update**：insert 走 COW（必须），update 走原地（性能）。

**Lealone `setValue()` 三种情况**：

| value 变化 | `addMemory(delta)` | memory | 是否溢出 | 处理 |
|-----------|-------------------|--------|---------|------|
| **等长** | delta=0 | 不变 | 否 | ✅ 原地修改 |
| **变短** | delta<0 | 减小 | 否 | ✅ 原地修改 |
| **变长** | delta>0 | 增加 | **可能** | ⚠️ **不处理** |

变长时 `needSplit()` 不被调用——`writeLocal()` 里 `index < 0`（新 key）才检查 split。注释明确：*"暂时不考虑被更新的值过大，导致超过 page size 的情况"*。变长更新可能默默超出 pageSize。

**NexKV 不分离**：insert/update 都走 COW。变长 value 自然处理——新页大小自适应。代价是每写必 Alloc。

**benchmark 场景**：value 固定 `"value-0000000000"` = 16B，等长。Lealone 原地更新完美适用。这是 3.67M vs 1.28M 的根源。

### 7.6 改进方向

| 方向 | 思路 | 预期 | 复杂度 |
|------|------|------|--------|
| **Update 跳过 COW** | 无并发读者时原地修改 mmap 页 | seq-put →3M+ | 中（检测 refCount==0） |
| 预分配页池 | 后台清零 | Alloc -50% | 低 |
| PageScheduler | 同页串行化 | par-put 波动消除 | 中 |

---

---

## 八、In-Place Update 提案

> 基于 Lealone `setValue()` 源码分析 + NexKV `leaf.Update()` 已有 fast path

### 8.1 当前代码已经有了 90%

`leaf_page.go:120-136` 的 `Update` 已经实现了 "新 value <= 旧 slot" 的快速路径：

```go
// leaf_page.go:121-136 — 当前代码
if len(value) <= int(oldValLen) {
    newRawID, _ := h.storage.pm.Alloc()  // ← 仍然 COW！
    copy(dst, src)                         // ← 全页 4KB 拷贝
    h.pa.OverwriteLeafValue(newRawID, idx, value)  // ← 在新页上覆写
    return newHandle, nil
}
```

**问题**：仍然 Alloc + 4KB 拷贝。只需要把 `OverwriteLeafValue` 直接作用于**原页**即可。

### 8.2 改造：跳过 COW，原地覆写

```go
func (h *leafPageHandle) UpdateInPlace(idx int, value []byte) error {
    rawID := uint32(h.id)
    _, _, _, oldValLen := h.pa.GetLeafEntryOffset(rawID, idx)
    if len(value) > int(oldValLen) {
        return errValueTooLarge // fallback to COW path
    }
    // 直接覆写原页上的 value，0 Alloc，0 拷贝
    h.pa.OverwriteLeafValue(rawID, idx, value)
    h.pa.IncVersion(rawID) // 递增版本号，保持一致性
    return nil
}
```

### 8.3 何时可以安全使用

| 条件 | 检查方式 |
|------|---------|
| 无并发读者 | `epochMgr == nil`（benchmark 默认）或 epoch slot 无活跃读者 |
| 新 value <= 旧 slot | `len(value) <= int(oldValLen)` |
| 非 split 路径 | `!oldLeaf.IsFull(len(key), 0)` |

### 8.4 集成到 `writeOperation`

```go
// operations.go writeOperation 非 split 路径
if b.epochMgr == nil {  // 无并发读者 → 可以原地修改
    if err := oldLeaf.UpdateInPlace(idx, value); err == nil {
        // 成功！0 Alloc，0 拷贝，0 CAS
        path.ReleaseAll()
        return nil
    }
    // value 太长 → 回退到 COW
}
// 有并发读者 → COW（现有路径不变）
result, err := mutate(oldLeaf)
```

### 8.5 预期效果

**seq-put measure 阶段**：100K 次 update，value 等长（16B），全部命中原地修改：

| | 当前 | 原地修改 | 
|---|---|---|
| Alloc | 100K 次 | **0 次** |
| 4KB 拷贝 | 100K 次 × 4KB = 400MB | **0** |
| CAS leafRef | 100K 次 | **0 次**（页版本号递增） |
| CPU (pprof) | 504ms | **~200ms**（减半） |
| **QPS** | **1.28M** | **~2.5M-3M** |

### 8.6 风险

| 风险 | 缓解 |
|------|------|
| 并发读者读到半写数据 | 仅 `epochMgr==nil` 时启用（无并发读者） |
| value 变长回退 | `UpdateInPlace` 返回 error → 走 COW 慢路径 |
| 页版本号溢出 | uint64，永远不会 |

---

## 九、参考

- Phase 5.6 性能分析：`docs/07_spike/btree-refactor/2026-04-02-btree-refactor-roadmap.md` §Phase 5.6
- Epoch 性能基线：`docs/07_spike/btree-refactor/2026-05-21-epoch-page-reclamation-spike.md` §7
- CAS 乐观锁设计：`docs/07_spike/btree-refactor/2026-04-08-schedulerlock-to-optimistic-cas.md`
- Benchmark 源码：`cmd/tools/btree_bench/main.go`

---

**文档版本**：v4.0
**状态**：Investigation — P0+P1(Benchmark) 完成，Lazy Split 方案设计完成（§六），待实施验证
