# BTree Memory Mode 性能调试预研

> 创建日期：2026-05-23
> 状态：Investigation — 已识别热点，待精确定位
> 分支：`spike/btree-memory-perf`

---

## 一、问题描述

`cmd/tools/btree_bench` 在 memory-only 模式（epoch=off，无 WAL/AO）下，**par-put 性能高度不稳定**：

| 运行 | par-put-4 | par-put-8 | par-put-16 |
|------|-----------|-----------|------------|
| Run 1 | 493K | 583K | 712K |
| Run 2 | 1,346K | 146K | 27K |
| Run 3 | 54K | 99K | — (hang) |

**波动幅度 50 倍以上**，同一代码、同一机器、同一参数。

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

## 六、参考

- Phase 5.6 性能分析：`docs/07_spike/btree-refactor/2026-04-02-btree-refactor-roadmap.md` §Phase 5.6
- Epoch 性能基线：`docs/07_spike/btree-refactor/2026-05-21-epoch-page-reclamation-spike.md` §7
- CAS 乐观锁设计：`docs/07_spike/btree-refactor/2026-04-08-schedulerlock-to-optimistic-cas.md`
- Benchmark 源码：`cmd/tools/btree_bench/main.go`

---

**文档版本**：v1.0
**状态**：Investigation
