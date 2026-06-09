# Spike：BTree Get/Set 读路径性能调优

> **日期**：2026-06-08
> **分支**：`spike/btree-get-perf`
> **背景**：写路径已完成 6 项优化，txn-put-100 达 Lealone 98%。读路径仍落后 7x（txn-get-100: 3.99M vs Lealone 30M）

---

## 一、当前读路径性能基准

> **2026-06-09, MacBook Pro M4 Pro, 100K ops, 512MB mmap — 全部优化生效**

| 场景 | NexKV QPS | Lealone QPS | NexKV/Lealone | gap |
|------|------:|------:|:--:|:--:|
| `seq-get` (非事务) | **5.62M** | — | — | — |
| `seq-put` (非事务) | **3.56M** | 932K | 3.82x | — |
| `batch-get-64` | **5.04M** | — | — | — |
| `batch-get-256` | **5.12M** | — | — | — |
| `batch-get-1024` | **4.94M** | — | — | — |
| `txn-put-1` | **799K** | 643K | **1.24x** 🔥 | — |
| `txn-put-10` | **1.01M** | 896K | **1.13x** 🔥 | — |
| `txn-put-100` | **1.20M** | 1.08M | **1.11x** 🔥 | — |
| `txn-get-1` | **2.88M** | 4.65M | 0.62x | 1.6x |
| `txn-get-10` | **4.64M** | 20.5M | 0.23x | 4.4x |
| `txn-get-100` | **5.15M** | 30.0M | 0.17x | 5.8x |
| **`txn-get-batch-100`** | **17.9M** | 30.0M | **0.60x** 🔥 | 1.7x |

**核心发现**：
- 写路径全面超越 Lealone（1.11-1.24x）
- **`txn-get-batch-100` 达到 Lealone 的 60%**（逐 key Get 仅 17%）
- `batch-get-*` 从 1.7-1.9M → 4.9-5.1M (3x)
- 读路径瓶颈从 VersionChain 遍历变为 **BTree 查找本身的 O(log N) 开销**

---

## 二、读路径 CPU 剖析

### 2.1 btree_bench 非事务读写 — NexKV vs Lealone（100K/1M ops, 512MB/64MB）

| 场景 | NexKV QPS | Lealone QPS | NexKV/Lealone |
|------|------:|------:|:--:|
| `seq-put` | **3.56M** | **3.18M** | 1.12x |
| `seq-get` | **5.62M** | **13.1M** | 0.43x |
| `par-put-8` | **6.57M** | **4.92M** | 1.34x |
| `par-get-8` | **15.8M** | **13.7M** | 1.15x |
| `par-put-16` | — | **6.29M** | — |
| `mixed-8-r80` | **11.1M** | **4.21M** | 2.64x |

> **Lealone 数据**：`BTreeMapBenchmarkRunner` inMemory mode，100万 ops，64MB cache，JDK 21。排除 `.m2` 旧版 JAR 冲突后运行成功。
>
> **关键发现**：
> - **NexKV seq-get 落后 Lealone 2.3x** — Lealone 用 Java NIO DirectByteBuffer 零拷贝读 mmap，NexKV 每次 Get 需 Go heap copy（epoch 安全边界）。这是下一轮优化的主战场。
> - seq-put NexKV 领先 12% — COW+CAS 比 Java synchronized 块更高效
> - par-put-8 NexKV 领先 34% — 单层 BTree 的 CAS 串行化对 Java 影响更大（synchronized 比 Go atomic CAS 更重）
> - mixed-8-r80 NexKV 领先 2.6x — 批量 GetBatch 后读效率大幅提升

| 场景 | QPS | 场景 | QPS |
|------|------:|------|------:|
| `seq-put` | **3.12M** | `par-put-8` | **6.38M** |
| `seq-get` | **4.77M** | `par-get-8` | **12.3M** |
| `seq-put-get` | **3.60M** | `mixed-8-r80` | **10.1M** |
| `batch-set-64` | **3.04M** | `batch-get-64` | **1.71M** |
| `batch-set-256` | **3.05M** | `batch-get-256` | **1.94M** |
| `batch-set-1024` | **3.13M** | `batch-get-1024` | **1.87M** |
| `par-batch-set-1024-8` | **10.3M** | `par-batch-get-1024-8` | **3.54M** |

> `seq-get` 4.77M vs `seq-put` 3.12M (53% higher) — 读路径无需 COW+CAS，纯 mmap 访问。
> `batch-get` 仅 1.7-1.9M — 远低于 `seq-get`，因为当前 `GetBatch` 走 `PageDispatcher` 间接路径。

### 2.2 pprof 实测（2026-06-08，纯 Get benchmark，10K preload + 1M reads）

```
非事务纯 Get: 298 ns/op ≈ 3.35M QPS (单核, 128MB mmap)

=== alloc_space (纯 Get 路径，preload 不计时) ===
  ParseMVCC               83.5MB  (18.5%)  ← 🔴 最大 Get 路径分配
  GetValue                48.5MB  (10.7%)  ← 🔴 heap copy mmap → Go
  GetLeafPage              3.5MB  (0.8%)   ← 页面 handle 池化获取
  fmt.Sprintf              17.0MB           ← benchmark key 生成（非 Get 路径）

注: NewEpochManager(208MB) 来自其他测试的 setup，统计噪音
```

**关键发现**：
- **Get 路径最大瓶颈是 Go heap 分配**，不是 CPU 计算
- `ParseMVCC` + `GetValue` = 132MB，占总分配的 29%（其余 71% 为 NewEpochManager/fmt.Sprintf 等 setup 噪音）。在纯 Get 路径中这两个是**唯二**的 heap 分配源，占 Get 路径分配约 90%（其余 GetLeafPage/searchPath/Search 为零分配或 pool 复用）
- `searchPath` 和 `leaf.Search` 几乎不分配（零或极少）
- 纯 Get CPU 本身极低（< 5%），主要是 `UnsafePointer.Load`（atomic 读）和 `cmpbody`（bytes.Compare）
- 83.5MB / 1M reads = 83 bytes/op，MVCCValue struct(~66B) + error wrapper(~16B) ≈ 82B，完全吻合——**每 call 确实一次 heap alloc**
- 消除 ParseMVCC + GetValue 的 heap 分配 → 预期 298ns → ~180ns → **~5.5M QPS (+64%)**

### 2.3 非事务 Get 路径

```
BTree.Get(key):
  1. searchPath(rootRef, key)          ← 从 root 走到 leaf（锁无关，零分配）
  2. GetLeafPage(leafPageID)           ← 获取 mmap 页面 handle（pool 复用）
  3. leaf.Search(key)                  ← 二分查找 key（零分配）
  4. leaf.GetValue(idx)                ← 🔴 heap copy 从 mmap → Go (48.5MB/1M reads)
  5. mvcc.ParseMVCC(raw)               ← 🔴 解码 MVCC → heap alloc (83.5MB/1M reads)
  6. tombstone 检查 + 返回值
```

**主要分配点**：`GetValue`(heap copy) + `ParseMVCC`(MVCCValue struct alloc + beginTS 解析)

### 2.4 事务 Get 路径（额外开销 vs 非事务）

```
SnapshotTx.Get(key):
  1. WriteBuffer.Get(key)              ← string key → map lookup（零分配）
  2. snapshotGet(key):
     a. BTree.GetRaw — 同非事务 Get
     b. ParseMVCC(raw)                 ← 同非事务
     c. BeginTS <= snapshotTS?         ← 整数比较
     d. PrevBeginTS <= snapshotTS?     ← 整数比较（版本内嵌后）
     e. deepCopy(val)                  ← 🔴 额外 Go heap 分配 + 拷贝
```

**事务 Get 比非事务 Get 多**：`deepCopy`（每 Get 一次 heap 分配 + 拷贝）

---

## 三、优化机会

> **核心发现**：Get 路径是**分配密集型**（allocation-bound），不是计算密集型。298ns/op 中大部分时间花在 GC/分配上。消除 top 2 分配（ParseMVCC 83.5MB + GetValue 48.5MB）可预期 ~2x 提升。

### 改动 ① 🔥：ParseMVCC 零分配（🟡 中 ~30 行）

**现状**：`ParseMVCC` 每次返回 `&MVCCValue{}`（heap alloc），1M reads → 83.5MB 分配。

**修复**：改为栈分配。`MVCCValue` 直接作为值返回，不取地址。Go 编译器对小 struct 做逃逸分析——如果 `MVCCValue` 不逃逸到堆，自动栈分配。

```go
// 改前: 返回指针 → heap alloc
func ParseMVCC(val []byte) (*MVCCValue, error) { ... }

// 改后: 返回值 → stack alloc（不逃逸时）
func ParseMVCC(val []byte) (MVCCValue, error) { ... }
```

**风险**：调用方如果有 `mv := ParseMVCC(...)` → 不逃逸 → 栈分配。如果存到 `map[string]MVCCValue` → 逃逸 → 仍 heap。需检查所有 call site。

**逃逸分析验证**：实现后运行 `go build -gcflags="-m" ./internal/infrastructure/storage/mvcc/ 2>&1 | grep "ParseMVCC"` 确认 `MVCCValue` 不逃逸到堆。

**预期收益**：`seq-get` 3.35M → 4.5-5M (+30-40%)。GC mark/sweep 减少 83MB 对象扫描，但 Parse 中 `binary.BigEndian.Uint64 × 2` 和 bounds check 等 CPU 操作仍在。`MVCCValue` ~66 字节栈分配，`PrevVal/RealVal` sub-slices 零分配。

### 改动 ② 🔥：leaf.GetValue 零拷贝（🟡 中 ~15 行）

**现状**：`GetValue` 每次 `make([]byte, len) + copy`（heap alloc），1M reads → 48.5MB。

**修复**：返回 mmap sub-slice + epoch 保护（调用方通过 PageRef.Retain 保证页面不被 free）。

```go
// 改前: heap copy
func (h *leafPageHandle) GetValue(idx int) []byte {
    result := make([]byte, valLen)
    copy(result, mmapSlice)
    return result
}

// 改后: mmap sub-slice + epoch guard
func (h *leafPageHandle) GetValue(idx int) []byte {
    return mmapSlice  // caller must not mutate; lifetime guaranteed by epoch
}
```

**风险**：调用方若修改返回值会污染 mmap。需 epoch 保护防止页面在读取期间被 free。

**epoch 保护方案**：
```
读路径时序:
  1. epochSlot = b.epochMgr.Acquire()         ← 标记"我在读"
  2. searchPath + GetLeafPage → pageRef Retain
  3. GetValue → return mmap[offset:offset+len] ← 直接返回 mmap sub-slice
  4. 调用方消费返回值 (ParseMVCC / tombstone 检查)
  5. epochMgr.Release(epochSlot)              ← 标记"我读完了"
  6. pageRef.Release()                         ← 若 refCount=0 → 推入 epoch retiredPages 队列

  Free 延迟: 页面在 epoch 结束后才被物理回收。
  保证: 步骤 3 返回的 sub-slice 在步骤 5 之前有效。
```

当前 `BTree.Get` 已有 epoch 保护（`btree.go:218`，`epochSlot = b.epochMgr.AllocSlot()` + defer `RetireBatch`）。需确认 `GetValue` 返回值的消费在 epoch Release 之前完成。

**实现分步**：
- Step 2a — 确认当前 epoch 保护窗口覆盖 GetValue 返回值生命周期。
- Step 2b — 消除 copy，返回 mmap sub-slice。

**替代方案**：`sync.Pool` 管理 GetValue buffer 复用。调用方用完归还 buffer → 消除 heap 分配的同时不引入 mmap 生命周期耦合。复杂度略高（需 pool + 调用方契约），作为 Step 2b 的可选替代。

**跨层耦合风险**：GetValue 零拷贝引入 `leaf_page.go → epoch_manager → page_manager → Get 调用方` 的跨层生命周期契约。未来修改 epoch/页面回收/GetValue 调用方时需同步更新多处。标注为 `中（epoch保护）` 风险。

**预期收益**：`seq-get` +20-35%（消除 48.5MB 分配）

### 改动 ③：事务 Get 路径 deepCopy 消除（🟢 小 ~5 行）

同改动 ② 的效果在事务路径——`snapshotGet` 的 `deepCopy(mv.RealVal)` 每次分配新 []byte。

**修复**：如果 ParseMVCC + GetValue 已返回栈/heap 安全副本，不再需要 deepCopy。

**预期收益**：`txn-get-*` +10-15%

### 改动 ④：批量 Get API（与 ParseMVCC/GetValue 零分配协同）

**思路**：`GetBatch(ctx, keys [][]byte) ([][]byte, error)` — 一次 searchPath 后批量 Search+GetValue。

与改动 ①+② 协同：单次 GetBatch 内共享 epoch 保护，所有返回值共享同一个 mmap 页引用。

**预期收益**：`txn-get-100` 3.99M → 15-20M（需配合 ①+②）

### 改动 ⑤：searchPath 缓存（🟢 小 ~50 行）

单层 BTree 下所有 key 命中同一页。缓存 leaf handle 避免重复 searchPath。但 pprof 显示 searchPath 几乎零分配——此优化的绝对收益有限（～5-10%）。降为低优先级。

**预期收益**：`seq-get` +5-10%

### 改动 ⑥：非事务 Set InPlace 同长优化（🟢 小 ~10 行）

`len(new) == len(old)` 时跳过 COW → 直接 `OverwriteLeafValue` + CAS。

**预期收益**：`seq-put` 2.95M → 3.5-4.0M (+20-35%)

---

## 四、优先级与预期收益

| 序号 | 优化 | 分配节省 | 预期 QPS 提升 | 工作量 | 风险 |
|------|------|:--:|:--:|:--:|:--:|
| ① | **ParseMVCC 零分配** (值返回) | 83.5MB | seq-get +30-40% | ~40行 | 低 |
| ② | **GetValue 零拷贝** (mmap slice) | 48.5MB | seq-get +20-35% | ~15行 | 中（epoch保护） |
| ③ | 事务 deepCopy 消除 | ~30MB | txn-get +10-15% | ~5行 | 中（epoch保护） |
| ④ | 批量 Get API | — | txn-get-100 +275-400% | ~80行 | 低 |
| ⑤ | searchPath 缓存 | 零分配 | seq-get +5-10% | ~50行 | 低 |
| ⑥ | InPlace 同长优化 | — | seq-put +20-35% | ~10行 | 低 |

**推荐顺序**：
1. 先做 ①（最大收益/风险比，ParseMVCC 值返回）
2. 再做 ②+③（需要 epoch 保护，但收益确定）
3. 然后 ④（批量 Get，需 ①+② 配合达到最大效果）
4. 最后 ⑤+⑥（锦上添花）

> ⚠️ **④ 强依赖 ①+②**：GetBatch 批量 searchPath 节省的是"定位到 leaf page"的开销。但每个 key 仍经过 ParseMVCC + GetValue。若 ①+② 未完成，批量内每个 key 仍做 132MB heap 分配 → 批量 searchPath 的节省被淹没 → 实测收益远低于预期。**④ 的 benchmark 验证必须在 ①+② 全部完成后进行。**建议在 ①+② 完成后用 build tag 或 internal flag 控制 ④ 的启用。

### pprof 验证后的组合预期

> 基准数据来源：btree_bench 100K ops (512MB mmap) + pprof benchmark 1M reads (128MB mmap)。
> 改动 ① 消除 ParseMVCC 分配（83.5MB），改动 ② 消除 GetValue 分配（48.5MB）。组合消除 132MB（100% Get 路径分配）。

```
改动 ① 后: seq-get      4.77M → 5.50M   (ParseMVCC 值返回, +15%)
改动 ② 后: seq-get      5.50M → 5.62M   (GetValue 零拷贝, 拷贝搬家, +2%)
改动 ③ 后: txn-get-100  5.05M → 5.15M   (deepCopy 消除, ~持平)
改动 ④-BTree后: batch-get-64 1.71M→5.04M (单页批量, +195%)
改动 ④-事务后: txn-get-batch-100 → 17.9M (批量搜索+MVCC, +247%)
改动 ⑤: 跳过 (searchPath缓存, 收益<5%, 不值得)
改动 ⑥: 跳过 (InPlace同长, 已内置实现)
```

### 对比 Lealone（最终实测）

> **2026-06-09, MacBook Pro M4 Pro, 100K ops, 512MB mmap**

| 场景 | NexKV 最终 | 贡献改动 | Lealone | NexKV/Lealone |
|------|------:|------|------:|:--:|
| seq-get (非事务) | **5.62M** | ①+② | — | — |
| batch-get-64 | **5.04M** | ①+②+④-BTree | — | — |
| batch-get-256 | **5.12M** | ①+②+④-BTree | — | — |
| txn-get-1 | **2.88M** | ①+②+③ | 4.65M | 62% |
| txn-get-100 | **5.15M** | ①+②+③ | 30.0M | 17% |
| **txn-get-batch-100** | **17.9M** | ①+②+③+④-事务 | 30.0M | **60%** 🔥 |
| seq-put | **3.56M** | — | 932K | 3.82x |

---

## 五、改动 ④ 详细设计：批量 Get API

### 5.1 双层设计：BTree 层 + 事务层

**BTree 层**：一次 searchPath → 批量 Search+GetValue（同一 leaf page 内）：

```go
func (b *BTree) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
    // 排序 keys
    sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 })

    // 一次 searchPath 定位到第一个 key 的 leaf
    path, err := searchPath(b.rootRef, keys[0])
    leaf := GetLeafPage(path.Leaf().Ref.GetPageInfo().PageID)

    results := make([][]byte, len(keys))
    for i, key := range keys {
        idx, found := leaf.Search(key)
        if !found {
            results[i] = nil  // or ErrKeyNotFound
            continue
        }
        results[i] = leaf.GetValue(idx)
    }
    path.ReleaseAll()
    return results, nil
}

// 跨页 key: 如果 root 是 InternalPage（多层 BTree），回退到逐个 Get。
// 单层 BTree 下所有 key 命中同一页 → 100% 命中批量路径。
```

**事务层**：在 BTree.GetBatch 之上叠加 MVCC 语义：

```go
func (tx *SnapshotTx) GetBatch(ctx context.Context, keys [][]byte) ([][]byte, error) {
    // Phase 1: WriteBuffer 过滤（已修改 key 从 buffer 读）
    // Phase 2: 剩余 keys → BTree.GetBatch → 批量 raw bytes
    // Phase 3: 批量 ParseMVCC → 批量 snapshotTS 检查
    //   每个 raw[i] 独立判断可见性（不需额外 searchPath）
}
```

**关键**：事务层的批量 parse 在**同一 BTree 层调用后**做——所有 key 的 raw bytes 已获取，MVCC 可见性检查是纯内存操作（ParseMVCC + 整数比较）。100 次 searchPath → 1 次，100 次 deepCopy → 配合改动 ②+③ 消除。

---

## 六、实现计划

| Step | 内容 | 工作量 | 状态 |
|------|------|:--:|:--:|
| 1 | ParseMVCC 值返回 | ~40行 | ✅ 完成 (253133b) |
| 2 | leaf.GetValue 零拷贝 + epoch 保护 | ~15行 | ✅ 完成 (603e914) |
| 3 | snapshotGet deepCopy 消除 | ~5行 | ✅ 完成 (b92141c) |
| 4 | BTree.GetBatch 单页批量优化 | ~92行 | ✅ 完成 (f4cbc00) |
| 5 | SnapshotTx.GetBatch 事务层批量 | ~82行 | ✅ 完成 (fe07e49) |
| 6 | btree-txn-bench txn-get-batch-* | ~43行 | ✅ 完成 (fe07e49) |
| 7 | writeOperation 合并 (消除179行复制) | −179行 | ✅ 完成 (88d0588) |
| 8 | searchPath 缓存 | — | ⏭️ 跳过 (收益<5%) |
| 9 | InPlace 同长优化 | — | ⏭️ 跳过 (已内置)

### 改动 ① 影响的 call site（ParseMVCC 签名变更）

ParseMVCC 当前返回 `(*MVCCValue, error)`，改为 `(MVCCValue, error)`。影响文件：

| 文件 | call sites | 变更 |
|------|:--:|------|
| `mvcc/transaction.go` | 6 | `mv, err := ParseMVCC(...)` → `mv` 变值类型 |
| `mvcc/codec_test.go` | 8 | 同 |
| `mvcc/transaction_test.go` | 5 | 同 |
| `btree/btree.go` | 4 | 同 |
| `btree/set_with_retry.go` | 1 | 同 |
| `btree/leaf_page.go` | 2 | 同 |
| `btree/storage_adapter.go` | 4 | 同 |
| `btree/compaction.go` | 1 | 同 |
| `wal/recovery.go` | 1 | 同 |
| `btree/get_with_meta.go` | 1 | 同 |

> `mv.IsTombstone()` 和 `mv.Flag/BeginTS/RealVal` 访问方式不变（Go 自动解引用值接收者方法）。
>
> **验证 call site 完整性**：运行 `grep -rn "ParseMVCC\b" internal/infrastructure/storage/ --include="*.go" | grep -v "_test.go"` 确认所有调用方已列出。

### 改动 ① 分配细目确认

83.5MB / 1M reads = 83.5 bytes/op：
- `MVCCValue` struct ≈ 66B (Flag:1 + BeginTS:8 + RealVal:24 + PrevFlag:1 + PrevBeginTS:8 + PrevVal:24)
- `error` interface wrapper ≈ 16B（绝大部分路径 `err==nil`，零值 interface 仍占 16B）
- 合计 ≈ 82B，与实测 83.5B 完全吻合。**每 call 一次 heap alloc 确认。**

### pprof 数据说明

当前 pprof 数据混有同包其他测试的 setup 开销。`NewEpochManager`(208MB) 和 `preTouchPages`(93% CPU) 来自 package-level test setup 而非 Get benchmark 本身。纯 Get 路径的分配数据（ParseMVCC 83.5MB + GetValue 48.5MB）已通过 alloc_space breakdown 确认。后续每个改动后需重新跑纯 Get benchmark 验证分配量下降。

### benchmark 验证方法

```bash
# 纯 Get benchmark（排除 Preload 干扰）
go test -bench=BenchmarkPureGet -benchtime=1000000x \
  -cpuprofile=/tmp/get-cpu.prof -memprofile=/tmp/get-mem.prof \
  ./internal/infrastructure/storage/btree/

# 查看 Get 路径分配
go tool pprof -top -alloc_space /tmp/get-mem.prof | grep -E "ParseMVCC|GetValue"
```

---

## 七、关联文档

- [[2026-06-08-txn-benchmark-spike]] — 写路径 6 项优化全程
- [[2026-06-08-setbatch-split-brainstorm]] — SetBatch split bug 分析与修复
