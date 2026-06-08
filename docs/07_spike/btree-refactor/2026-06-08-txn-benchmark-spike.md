# Spike：btree-txn-bench — 事务（Transaction）基准测试独立工具

> **文档类型**：预研究 / 技术探索  
> **日期**：2026-06-08  
> **作者**：jzhang405  
> **关联分支**：`spike/btree-bench-txn-benchmark`  
> **关键词**：Transaction, MVCC, TxManager, benchmark, Lealone, isolation

---

## 一、背景

当前 `cmd/tools/btree_bench` 已覆盖纯内存、WAL、Checkpoint 三种模式的 Set/Get 吞吐量（600+ 行）。如果再加入事务场景（纯写/只读/读写混合 + WAL/Checkpoint 组合），会膨胀至 1000+ 行。**独立为 `cmd/tools/btree-txn-bench`**——职责单一、对标 Lealone `LealoneTxnBenchmarkRunner`。

NexKV 的生产路径中，所有写操作都在事务内完成（MVCC Tx），benchmark 必须独立度量事务路径的吞吐量。

## 二、NexKV 已有事务支持

```
BTree.BeginTx(ctx, opts...)
  → b.txMgr.BeginTx(ctx, mvcc.SnapshotIsolation)
  → mvcc.Tx {
        Get(ctx, key)    // MVCC 快照读
        Set(key, value)  // 写入 WriteBuffer (未提交)
        Delete(key)      // 写入 Tombstone
        Commit(ctx)      // WAL + 分配 commitTS
        Rollback()       // 丢弃 WriteBuffer
    }
```

事务路径比裸 `BTree.Set()` 多了：
1. **MVCC 版本链查找**：Get 时跳过 beginTS 之后和已回滚的版本
2. **WriteBuffer 缓冲**：Set 写入缓冲区，Commit 时才批量写 BTree
3. **Commit 流程**：WAL TxPrepare（fwrite+fsync）→ 分配 commitTS → applyWriteBuffer（MVCC 编码 + SetBatch 批量 Set）→ WAL TxCommit（fwrite+fsync）

> ⚠️ **架构关键差异**：NexKV Commit 有 **2 次 WAL fsync**（TxPrepare + TxCommit，源码 `transaction.go:426-439,451-469`）。Lealone Commit 只有 **1 次 RedoLog fsync**（`writeRedoLog()`）。即使 benchmark 不接 WAL（`txManager.SetWAL(nil)`），PreCheck + keyLock + Prepend + Set 的固定开销 ~几微秒/op 仍不可忽略。

> 关键差异：事务模式下 WriteBuffer 是纯内存缓冲，Commit 才批量写入 BTree。大 batch 下可能有更好的吞吐（写合并），小 batch 下有事务管理开销。
>
> ⚠️ **事务 Commit 与 Checkpoint 的关系**：Commit 不调用 Checkpoint save()——它们异步分离。Commit 唯一的 Checkpoint 交互是 BTree.Set() 中的 `dirtyBytes.Add(4KB)`（原子加法 ~1ns）。因此 **事务 QPS 与是否启用 Checkpoint 无关**——`txn-put-1` ≈ `txn-put-1-ckpt`。Checkpoint 只是延迟落盘（异步），WAL 才在 Commit 内同步 fsync。


#### 隔离级别对标 Lealone

> Lealone 支持 **4 种隔离级别**（`Transaction.java:20-26`），NexKV 当前仅 2 种。本次实现扩展到 4 种，对标 Lealone。

| 隔离级别 | Lealone 常量 | NexKV（当前） | NexKV（计划） | Phase | 实现方式 |
|---------|-------------|:-----------:|:-----------:|:-----:|------|
| Read Uncommitted | `IL_READ_UNCOMMITTED (1)` | ❌ | 🚧 | Phase 1 | 跳过 VersionChain → 读 BTree 最新值（脏读） |
| Read Committed | `IL_READ_COMMITTED (2)` | ✅ 已有 | ✅ | 已完成 | 每次读 BTree 最新 Committed 值 |
| Repeatable Read | `IL_REPEATABLE_READ (4)` | ❌ | 🚧 | Phase 2 | Snapshot 快照读 + PreCheck 验证所有读 |
| Serializable | `IL_SERIALIZABLE (8)` | ❌ | 🚧 | Phase 2 | Snapshot + PreCheck all reads + per-key KeyLock 串行化写 |

> **实现策略**：4 种隔离级别复用一个 `SnapshotTx` 结构，通过 `isolationLevel` 字段控制读行为。代码量约 +30 行（2 常量 + Put 分支 + Commit 验证扩展）。

## 三、Lealone 事务基准对照

### 实现

```java
// TransactionEngine + Storage → beginTransaction → openMap → put → commit
Transaction tx = te.beginTransaction();
TransactionMap<Integer, String> map = tx.openMap(name, storage);
for (int i = 0; i < batchSize; i++) { map.put(i, "v-" + i); }
tx.commit();
```

### 实测数据（MacBook Pro M4 Pro，2026-06-08）

**纯写事务 — 纯内存 vs 持久化**：

| 场景 | 纯内存 (NO_SYNC) | 持久化 (INSTANT fsync) | 比值 |
|------|------:|------:|:----:|
| `txn-put-1` | **643,110** | **158** | 4070x |
| `txn-put-10` | **896,274** | **1,226** | 731x |
| `txn-put-100` | **1,078,265** | **17,037** | 63x |
| `txn-put-1000` | **1,100,562** | **155,062** | 7.1x |
| `txn-put-10000` | **1,149,016** | — | — |

**只读事务**（纯内存与持久化相同——读不触发 fsync）：

| 场景 | 纯内存 | 持久化 |
|------|------:|------:|
| `txn-get-1` | **4,653,869** | — |
| `txn-get-10` | **20,536,162** | — |
| `txn-get-100` | **29,957,314** | **34,403,666** |

**读写混合**：

| 场景 | 纯内存 | 持久化 | 比值 |
|------|------:|------:|:----:|
| `txn-rw-80-10` | **2,936,211** | **1,764** | 1664x |
| `txn-rw-50-100` | **2,743,663** | — | — |
| `seq-put` (非事务基线) | — | **931,999** | — |

### Lealone 分析

- **纯内存事务**：`txn-put-1` 643K QPS（比之前的 459K 快 40%——去掉了 RedoLog 开销）。batch 增大 → 接近 1.15M，几乎与非事务相同
- **持久化事务**：`txn-put-1` 仅 158 QPS（每条 Commit 一次 RedoLog fsync ≈ 6.3ms）。batch=1000 恢复至 155K QPS（摊销 fsync）
- **只读不受影响**：`txn-get-100` 30-34M QPS，两种模式相同——读没有 RedoLog
- **读写混合**：纯内存 2.9M，持久化 1,764（1664x 差距）——Commit 中 fsync 碾压 read 优势

## 四、NexKV 事务 benchmark 设计

### 新增 flag

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-txn` | bool | false | 启用事务模式 |
| `-txn-batch` | int | 1 | 每事务操作数（1=每条单独事务, N=每N条commit一次） |
| `-txn-isolation` | string | `snapshot` | 隔离级别：`snapshot` / `read-committed` / `repeatable-read` / `serializable` |
| `-read-ratio` | float | 0.5 | 读比例（仅读写混合模式）：0.0=全写, 1.0=全读 |

### 组合维度

| 维度 | 可选值 |
|------|--------|
| 事务大小 | -txn-batch={1,10,100,1000,10000} |
| 持久化 | -persist={wal,checkpoint,空} |
| 并发 | -t={1,4,8,16} |

### Benchmark 场景（单线程基线）

**纯写事务**（默认纯内存：`txManager.SetWAL(nil)`，Commit 仅 PreCheck→commitTS→BTree.Set，无 fsync）：

| 场景 | txn | batch | persist | 说明 |
|------|:--:|:-----:|----------|------|
| `seq-put` | ✘ | — | — | 纯内存基线（非事务对照） |
| `txn-put-1` | ✔ | 1 | — | 纯内存事务，对标 Lealone txn-mem-put-1 (643K) |
| `txn-put-10` | ✔ | 10 | — | 对标 Lealone txn-mem-put-10 (896K) |
| `txn-put-100` | ✔ | 100 | — | 对标 Lealone txn-mem-put-100 (1.08M) |
| `txn-put-1000` | ✔ | 1000 | — | 对标 Lealone txn-mem-put-1000 (1.10M) |
| `txn-put-10000` | ✔ | 10000 | — | 对标 Lealone txn-mem-put-10000 (1.15M) |
| `txn-put-1-wal` | ✔ | 1 | wal | 事务+WAL持久化，对标 Lealone txn-persist-put-1 (158) |
| `txn-put-100-wal` | ✔ | 100 | wal | 对标 Lealone txn-persist-put-100 (17K) |

**事务 + Checkpoint 组合**：

> 事务 Commit → BTree 纯内存 COW（无 WAL fsync）。Checkpoint 周期 flush 脏页 → AO chunk。数据丢失窗口 = 两次 checkpoint 之间。对比纯 WAL 模式：checkpoint 无每条事务的 fsync 开销，但空闲期数据悬浮在内存。

| 场景 | txn | batch | persist | 说明 |
|------|:--:|:-----:|----------|------|
| `txn-put-1-ckpt` | ✔ | 1 | checkpoint | 每条单独事务+BTree写入，周期 checkpoint 落盘 |
| `txn-put-100-ckpt` | ✔ | 100 | checkpoint | 批量事务+checkpoint |
| `txn-rw-80-10-ckpt` | ✔ | 10 | checkpoint | 读写混合+checkpoint，对标 OLTP 真实路径 |
| `txn-get-100-ckpt` | — | 100 | checkpoint | 只读事务+checkpoint（读不触发脏页） |

> **事务 Commit 与 Checkpoint save 是异步分离的**：
> ```
> 事务 Commit（两种模式相同）:
>   PreCheck → commitTS → applyWriteBuffer → BTree.Set(COW)
>                                           └─ dirtyBytes.Add(4KB) ← 原子加法，~1ns
>   返回（结束。不调用 save）
>
> Checkpoint save（异步，独立 goroutine）:
>
>   EnumeratePages → ChunkManager.WritePage → Sync
> ```
>
> **触发条件**（参考 PersistCheckpoint）:
> 1. count: `setCount % ckptInterval == 0` (默认每 10K 条)
> 2. idle: `maxIdleDuration` 内无写入 (默认 3s)
> 3. Close: 退出前最后一次同步 save
> **核心结论**：`txn-put-1` 与 `txn-put-1-ckpt` QPS **相同**——Checkpoint 不阻塞 Commit。唯一的区别是持久化窗口：Checkpoint ≤ `maxIdleDuration`（默认3s），WAL 每条 Commit 同步 fsync。

**读写混合事务**（OLTP 真实路径）：

> 事务内 Get + Put 混合，测试 VersionChain 遍历 + ReadSet PreCheck + WriteBuffer 的交织开销。

| 场景 | 读比例 | batch | 说明 |
|------|:-----:|:-----:|------|
| `txn-rw-20-10` | 20% | 10 | 每事务 2 读 + 8 写 → commit |
| `txn-rw-50-10` | 50% | 10 | 每事务 5 读 + 5 写 → commit |
| `txn-rw-80-10` | 80% | 10 | 每事务 8 读 + 2 写 → commit（读为主） |
| `txn-rw-50-100` | 50% | 100 | 每事务 50 读 + 50 写 → commit（中等批量） |
| `txn-rw-80-100` | 80% | 100 | 每事务 80 读 + 20 写 → commit（大数据量读重写轻） |

> 实现方式：preload N 条 key 到 BTree 提供读基础，事务中按 readRatio 随机决定 Get 还是 Put。Get 走 WriteBuffer（先查）→ BTree → VersionChain（如果有）。Put 走 WriteBuffer 缓冲 → Commit 时 applyWriteBuffer。

**只读事务**（VersionChain 遍历性能）：

| 场景 | batch | 说明 |
|------|:-----:|------|
| `txn-get-1` | 1 | 每条事务单 Get（最小事务） |
| `txn-get-10` | 10 | 每事务 10 读 → commit |
| `txn-get-100` | 100 | 每事务 100 读 → commit |
| `txn-get-1000` | 1000 | 大只读事务 |

> 只读事务不需要 WAL 写入、不需要分配 commitTS。主要开销是 BTree.GetRaw → VersionChain traversal（如果 chain 深度 > 0）。

**Rollback 事务**（discard WriteBuffer 开销）：

| 场景 | batch | persist | 说明 |
|------|:-----:|----------|------|
| `txn-rollback-1` | 1 | — | 逐条 put→rollback |
| `txn-rollback-100` | 100 | — | 批量 rollback |

### 输出指标

```
=== Transaction Stats ===
txn.count         : 1000       # 事务总数
txn.commits       : 1000       # 成功提交数
txn.rollbacks     : 0          # 回滚数
txn.avg_batch     : 100        # 平均每事务操作数
txn.total_ops     : 100000     # 总操作数
```

### 代码结构

```
cmd/tools/
├── btree_bench/          # 现有: Set/Get 吞吐量 + WAL/Checkpoint (600+行, 不改动)
└── btree-txn-bench/      # 新增: 事务 benchmark 独立工具 (~300行)
    └── main.go           # 全部逻辑: flag + scene + runTxn/runTxnRW/runTxnRead + print
```

> 独立为 `btree-txn-bench` 而非追加到 `btree_bench`: 职责单一, 对标 Lealone `LealoneTxnBenchmarkRunner`。`btree_bench` 600+行, 再加事务场景 (3模式×5batch×8混合) 会膨胀至 1000+。

```go
func runTxn(label string, n int, batchSize int, mmapSize int) {
    storage, _ := btree.NewOffheapBTreeStorage(mmapSize)
    // ⚠️ 必须 WithTSGenerator, 否则 BeginTx → panic (txMgr is nil)
    tree,_:=btree.NewBTree(storage,btree.WithTSGenerator(mvcc.NewLocalTS()))
    ctx:=context.Background()
    iso:=mvcc.SnapshotIsolation
    switch *txnIsolation{
    case"read-uncommitted":iso=mvcc.ReadUncommitted
    case"read-committed":iso=mvcc.ReadCommitted
    case"repeatable-read":iso=mvcc.RepeatableRead
    case"serializable":iso=mvcc.Serializable
    }
    // ⚠️ mvcc.BeginTx 直接传 level; BeginTx opts 参数当前未传隔离级别
    //    需实现 btree.WithIsolation(level) 或使用 txMgr.BeginTx(ctx,iso) 绕线

    // Warmup
    warmupTxnOps(n/10,batchSize,tree,ctx,iso)

    // Measure: 精确计量 Begin → Put×N → Commit
    var totalOps atomic.Int64
    t0 := time.Now()
    for i := 0; i < n; i += batchSize {
        tx,err:=tree.BeginTx(ctx) // ⚠️ 当前硬编码 SnapshotIsolation
        if err!=nil{log.Fatal(err)}
        actual := 0
        for j := 0; j < batchSize && i+j < n; j++ {
            // ⚠️ 不同事务使用不同 key（keyOf 使用全局递增计数器），
            //    避免 KeyLock 竞争导致串行化。见 commitKey() per-key KeyLock。
            _ = tx.Set(ctx, keyOf(i+j), valOf(i+j)) // err suppressed for benchmark
            actual++
        }
        if err:=tx.Commit(ctx);err!=nil{log.Fatal(err)}                         // commit — 计量内！
        totalOps.Add(int64(actual))
    }
    elapsed := time.Since(t0)
    qps := float64(totalOps.Load()) / elapsed.Seconds()

    _ = tree.Close()
}
```

> ⚠️ **关键**：`tx.Set()`（WriteBuffer.Put）是纯内存操作。BTree 写入 + VersionChain Prepend + WAL fsync **都在 Commit() 中**。**Commit() 必须在计时的 for 循环内**，否则度量失真。

> BTree 已内置 `BeginTx()` → `mvcc.Tx`，无需新依赖。本次只增加 benchmark 层代码。

#### warmup + 多线程 key 隔离

```go
func warmupTxnOps(n,batch int,tree *btree.BTree,ctx context.Context,iso mvcc.IsolationLevel){
    for i:=0;i<n;i+=batch{
        tx,_:=tree.BeginTx(ctx)
        for j:=0;j<batch&&i+j<n;j++{_=tx.Set(ctx,warmupKeyOf(i+j),valOf(i+j))}
        _=tx.Commit(ctx)
    }
}

// 多线程: 每 goroutine 独立 key 偏移量 → 无竞态, 无 per-key KeyLock 竞争
func runTxnConcurrent(t,n,batch int,mmapSize int){
    tree,_:=btree.NewBTree(storage,btree.WithTSGenerator(mvcc.NewLocalTS()))
    perThread:=n/t
    var wg sync.WaitGroup
    for g:=0;g<t;g++{
        wg.Add(1)
        go func(offset int){
            defer wg.Done()
            for i:=0;i<perThread;i+=batch{
                tx,_:=tree.BeginTx(ctx)
                for j:=0;j<batch&&i+j<perThread;j++{
                    _=tx.Set(ctx,keyOf(offset+i+j),valOf(offset+i+j))
                }
                _=tx.Commit(ctx)
            }
        }(g*perThread)
    }
    wg.Wait()
}
```

#### 读写混合伪代码

```go
func runTxnRW(label string, n int, batchSize int, readRatio float64, mmapSize int) {
    storage, _ := btree.NewOffheapBTreeStorage(mmapSize)
    tree,_:=btree.NewBTree(storage,btree.WithTSGenerator(mvcc.NewLocalTS()))
    ctx:=context.Background()
    iso:=mvcc.SnapshotIsolation
    switch *txnIsolation{
    case"read-uncommitted":iso=mvcc.ReadUncommitted
    case"read-committed":iso=mvcc.ReadCommitted
    case"repeatable-read":iso=mvcc.RepeatableRead
    case"serializable":iso=mvcc.Serializable
    }

    // ① Preload: 写 N 条 key 到 BTree 提供读基础
    for i := 0; i < n; i++ { _ = tree.Set(ctx, keyOf(i), valOf(i)) }

    // ② Warmup
    warmupTxnRWMixed(n/10, batchSize, readRatio, tree, ctx)

    // ③ Measure
    var totalOps atomic.Int64
    t0 := time.Now()
    for i := 0; i < n; i += batchSize {
        tx,_:=tree.BeginTx(ctx) // ⚠️ BeginTx 当前硬编码 SnapshotIsolation
        for j:=0;j<batchSize&&i+j<n;j++{
            if rand.Float64()<readRatio{
                // Get: WriteBuffer→BTree→VersionChain if exists
                _, _ = tx.Get(ctx, keyOf(rand.IntN(n)))
            } else {
                // Put: WriteBuffer only (no BTree yet in tx)
                _ = tx.Set(ctx, keyOf(n+i+j), valOf(i+j))
            }
        }
        if err:=tx.Commit(ctx);err!=nil{log.Fatal(err)} // PreCheck(readSet)→TxPrepare→commitKey×N→TxCommit
        totalOps.Add(int64(batchSize))
    }
    elapsed := time.Since(t0)
}
```

> **Get 开销差异**：首次 Get（无 VersionChain）走 BTree.GetRaw 解析 + beginTS 比较 → O(1)。后续 Get 在已 Commit key 的 VersionChain 中遍历找 snapshot 可见版本 → O(chainDepth)。读为主事务 write set 小 → PreCheck 轻量。

## 五、实测性能

### NexKV 纯内存事务实测（2026-06-08, MacBook Pro M4 Pro, 100K ops, 全部优化生效）

> **包含全部已实现优化**：preTouch 默认预热 + WriteBuffer sync.Pool + 悲观锁(统一路径) + COW 批量化(SetBatch) + 版本内嵌 BTree(消除 VersionChain)。

| 场景 | NexKV QPS | Lealone mem | NexKV/Lealone | 分析 |
|------|------:|------:|:-----:|------|
| `seq-put`（非事务基线） | **3.01M** | 932K | 3.23x | 纯内存 COW |
| `txn-put-1` | **718K** | 643K | **1.12x** 🔥 | 逐条 Commit, 首次超过 Lealone |
| `txn-put-10` | **924K** | 896K | **1.03x** 🔥 | 10 ops/txn, 超过 Lealone |
| `txn-put-100` | **1.08M** | 1.08M | **1.00x** 🎯 | 与 Lealone 持平 |
| `txn-put-1000` | **1.04M** | 1.10M | 0.95x | 1K ops/txn, 接近 |
| `txn-put-10000` | **1.09M** | 1.15M | 0.95x | 10K ops/txn |
| `txn-get-1` | **2.44M** | 4.65M | 0.52x | 单 Get/txn |
| `txn-get-10` | **3.77M** | 20.5M | 0.18x | 10 读/txn |
| `txn-get-100` | **4.07M** | 30.0M | 0.14x | ← 差距最大 |
| `txn-rw-20-1` | **313K** | — | — | 2读+8写/txn |
| `txn-rw-50-1` | **443K** | — | — | 5读+5写/txn |
| `txn-rw-80-1` | **844K** | — | — | 8读+2写/txn |
| `txn-rw-20-10` | **622K** | — | — | 2读+8写/txn |
| `txn-rw-50-10` | **773K** | — | — | 5读+5写/txn |
| `txn-rw-80-10` | **1.05M** | 2.94M | 0.36x | 8读+2写/txn |
| `txn-rw-20-100` | **847K** | — | — | 20读+80写/txn |
| `txn-rw-50-100` | **897K** | — | — | 50读+50写/txn |
| `txn-rw-80-100` | **1.10M** | — | — | 80读+20写/txn |
| `txn-rw-50-1000` | **942K** | — | — | 500读+500写/txn |
| `txn-rw-80-1000` | **1.16M** | — | — | 800读+200写/txn |
| `txn-rw-50-10000` | **875K** | — | — | 5000读+5000写/txn |
| `txn-rw-80-10000` | **1.06M** | — | — | 8000读+2000写/txn |

### 最终优化演进：txn-put-* 全程

| 场景 | 原始基线 | +preTouch+pool | +悲观锁统一 | +COW批量化 | +版本内嵌 | +SetBatch修复 | 累计提升 |
|------|------:|------:|------:|------:|------:|------:|:--:|
| `txn-put-1` | 462K | 480K | 670K | 717K | 698K | **718K** | **+55%** |
| `txn-put-10` | 535K | 531K | 892K | 927K | 928K | **924K** | **+73%** |
| `txn-put-100` | 570K | 560K | 917K | 936K | 1.10M | **1.08M** | **+89%** |
| `txn-put-1000` | 571K | 567K | 961K | 951K | 1.05M | **1.04M** | **+83%** |
| `txn-put-10000` | 552K | 563K | 928K | — | 1.06M | **1.09M** | **+98%** |

> **结论**：6 项优化 txn-put-100 稳定在 1.08M（与 Lealone 持平），txn-put-1/10 超 Lealone。batch-set-64 从 977K→2.84M (+190%)。写路径瓶颈已基本消除。

### btree_bench（非事务路径，100K ops，512MB，SetBatch 改用 SetWithRetry）

> preTouch 默认开启 + 版本内嵌 (value ~50B/entry) + SetBatch 修复 (SetWithRetry 替代 PD)。
> SetBatch 性能大幅提升：batch-set-64 977K→2.84M (+190%)，batch-set-1024 2.17M→2.89M (+33%)。

| 场景 | QPS | 场景 | QPS |
|------|------:|------|------:|
| `seq-put` | **3.01M** | `par-put-8` | **5.94M** |
| `seq-get` | **4.41M** | `par-get-8` | **12.1M** |
| `seq-put-get` | **3.24M** | `mixed-8-r80` | **9.30M** |
| `batch-set-64` | **2.84M** | `batch-set-256` | **2.86M** |
| `batch-set-1024` | **2.89M** | `par-batch-set-1024-8` | **3.80M** |

### 差距分析（最终状态）

| 开销源 | 状态 | 分析 |
|--------|:--:|------|
| **PreCheck（readSet 验证）** | ✅ 已消除 | 悲观锁统一路径, Commit 不再验证 |
| **VersionChain 遍历** | ✅ 已消除 | 版本内嵌 BTree, 无链遍历 |
| **单层 Leaf Page (Phase 5)** | ⚠️ 残留 | 所有 key 共享 4KB page, 多线程 CAS 串行化 |
| **Get 路径** | ⚠️ 残留 | 仍落后 Lealone 7x, 非 VersionChain 而是 BTree 查找本身 |


## 六、性能优化：三项改动对标 Lealone

> **目标**：将 NexKV 事务 QPS 从 Lealone 的 0.5x 提升至接近 1x。三项改动逐一降低。

### 改动 ①：PreCheck → 悲观锁（🟡 中 ~100行）

```
现状: Commit.PreCheck 逐 key GetRaw 验证 readSet 指纹
目标: Put 时提前获取 KeyLock → Commit 释放, 去掉 PreCheck
对标: Lealone AOTransaction — put 时加锁, Commit 不重读
```

| | 改动前 | 改动后 |
|---|--------|--------|
| Put 路径 | WriteBuffer.Put（无锁） | WriteBuffer.Put + KeyLock.Lock |
| Commit 路径 | PreCheck（逐 key GetRaw）+ commitKey | commitKey 直接 Prepend+Set |
| 并发 | Commit 时才竞争 KeyLock | Put 时竞争（相同, 提前而已） |

**早期数据**（仅悲观锁，无 COW 批量化 + 版本内嵌）: `txn-put-100` 601K → **662K (+10%)**。最终收益见 §5 演进表。

### 改动 ②：BTree 多层分裂（✅ 已实现）

```
现状: BTree 多层 split propagation 已完整实现 (handleLeafSplit/handleInternalSplit/handleRootSplit)。
      5000 key 插入后 root 有 2 个子 InternalPage → 深度 ≥ 3 层。
目标: 已实现 — 多线程并发写入时 CAS 竞争分散到不同叶子页。
```

| | 改动前 | 改动后 |
|---|--------|--------|
| 写入热点 | 全部 key 竞争 1 page 的 CAS | 分散到 N leaf page |
| 并发度 | 1（全局争用） | ~N page（散列争用） |

### 改动 ③：版本内嵌 BTree — 消除 VersionChain（🔴 高 ~400行）

> **核心思路**：将 VersionChain 链表替换为 BTree value 内嵌**单级**前一版本。
> 稳态下每个 key 最多 2 个并发版本（当前 + 前一），不需要完整链表历史。
> 极少数 >2 版本冲突的 key 用浅回退容忍。

#### 存储格式变更

```
当前格式 (VersionChain 分离):
  BTree:   [Flag:1][beginTS:8][realVal:N]
  Memory:  VersionChain 链表 {commitTS:8, val:N, next:*Node} ← sync.Map per-key
  Get:     BTree.GetRaw + versionStore.Load + chain traversal + Generation 校验
  分配:    每次 Commit 分配 1 个 VersionNode (堆)

目标格式 (版本内嵌, 直接替换旧格式):
  BTree:   [Flag:1][prevFlag:1][prevBeginTS:8][prevValLen:2][prevVal:N][beginTS:8][realVal:N]
  Memory:  无 (版本信息已在 BTree 页内)
  Get:     BTree.GetRaw → ParseMVCC → 如果 beginTS>snapshotTS 则读 prev 字段
  分配:    0 额外 heap 分配

Insert 无旧版本: prevFlag=0, prevBeginTS=0, prevValLen=0
Update 有旧版本: prevFlag=归一化(OldFlag), prevBeginTS=OldBeginTS, prevVal=OldValue
```

**固定开销**: 前一版本头 11 字节 (prevFlag:1 + prevBeginTS:8 + prevValLen:2)。

#### 写入路径变更

```
Commit → commitKey(key, entry, commitTS):
  当前:
    1. GetRaw(key) → oldRawVal, oldMVCC          ← 已在 Put 时做了
    2. Prepend(key, commitTS, oldVal, oldFlag)     ← 🔴 删除: CAS 插入 chain head
    3. BuildMVCC(flag, commitTS, newVal)           ← 扩展为带 prev 参数的版本
    4. SetBatch 批量化 Set

  目标:
    1. (Put 时已读 oldVal, oldFlag, oldBeginTS → 存在 WriteEntry)
    2. BuildMVCC(flag, commitTS, newVal,            // 当前版本
                entry.OldFlag, entry.OldBeginTS, entry.OldValue)  // 嵌入的前一版本
    3. SetBatch 批量化 Set (不变)

  删除的代码:
    - versionStore.LoadOrStore(key)                  ← ~5 行
    - versionStore.Prepend(key, commitTS, ...)       ← ~10 行  
    - prePrependHead 记录与生成回退                  ← ~15 行
    - UndoEntry.PrePrependHead, .PrependSucceeded    ← 2 字段
```

#### 读取路径变更

```go
// 改后 snapshotGet — 从 ~80 行缩减为 ~25 行
func (tx *SnapshotTx) snapshotGet(ctx context.Context, key []byte) ([]byte, error) {
    raw, err := tx.engine.storage.GetRaw(ctx, key)
    if err != nil { return nil, err }

    mv, err := ParseMVCC(raw)
    if err != nil { return nil, err }

    // 路径 1: 当前版本可见 → 直接返回
    if mv.BeginTS <= tx.snapshotTS {
        if mv.IsTombstone() { return nil, ErrKeyNotFound }
        return deepCopy(mv.RealVal), nil
    }

    // 路径 2: 当前版本不可见 → 检查嵌入的前一版本
    // PrevBeginTS != 0 表示存在有效的 prev 字段
    // prevFlag 已由 BuildMVCC 归一化为 0x00/0x01
    if mv.PrevBeginTS != 0 && mv.PrevBeginTS <= tx.snapshotTS {
        if mv.PrevFlag == FlagTombstone { return nil, ErrKeyNotFound }
        return deepCopy(mv.PrevVal), nil
    }

    // 路径 3: 两个版本都不可见 (PrevBeginTS==0 即 Insert 写的新 key)
    return nil, ErrKeyNotFound
}

// ❌ 完全删除:
//   - versionStore.Load(keyStr)
//   - chainVal.Generation() 乐观校验
//   - chain traversal loop (for node := ... node.next)
//   - snapshotGetMaxRetries 重试循环 (无链竞争 → 无需重试)
```

> **⚠️ prevFlag 归一化**（Review C1）：`BuildMVCC` 对 prevFlag 做 `prevFlag & 0x01` 归一化。Flag 只有 0x00(Normal)/0x01(Tombstone) 两种，确保 prev 读写一致。

#### 回滚路径变更

```
当前 Rollback → releaseHeldLocks → rollbackApplied → rollbackOneKey:
  1. GetRaw → 验证 beginTS == commitTS
  2. Set(oldRawVal)  ← 恢复 OldRawVal
  3. CAS revert VersionChain head (bumpGeneration)

目标 Rollback:
  1. (无需 GetRaw 验证 — 锁已持有，其他 txn 无法修改)
  2. Set(oldEncodedVal)  ← 用 SetBatch 统一处理
  3. ❌ 删除 VersionChain head CAS revert

UndoEntry 简化:
  type UndoEntry struct {
      Key        string
      OldRawVal  []byte  // MVCCV2 编码的恢复值 (含嵌入的旧前一版本)
      CommitTS   uint64  // 保留: rollbackOneKey 需要验证 BTree 当前值是否为己方写入
      EncodedKey []byte
      EncodedVal []byte
      // ❌ 删除: PrePrependHead, PrependSucceeded
  }
```

> **⚠️ 回滚路径 Review 确认**：UndoEntry.CommitTS 必须保留——rollbackOneKey 用它验证 BTree 当前 beginTS == 己方 commitTS，区分"我写的"和"别人写的"。PrePrependHead/PrependSucceeded 可以删除（V2 无 VersionChain head CAS revert）。

#### GC/Prune 影响

```
当前: VersionChain.Prune(watermark) — 遍历链标记 reclaimed
      gc.go: gcCycle() → tm.versionStore.Range → chain.Prune(watermark) → bumpGeneration
      需要 GC 协程定期扫描 versionStore.sync.Map

目标: 版本信息在 BTree 值内，自然随 COW 回收
      - BTree 页面被 free → old version 自动消失
      - ❌ 不需要 VersionChain.Prune()
      - ❌ 不需要 gcCycle() 中的 versionStore.Range 扫描
      - ✅ gc.go 缩减: gcCycle() 改为 no-op 或删除 (保留 GCStats 结构用于监控)
```

#### 连续写入的版本数分析 (Review C2 修复)

> **悲观锁下最多 2 个并发版本**：KeyLock 保证同 key 的 Put 串行化。tx1 Commit 释放锁之前 tx2 无法 Put。因此同时间点最多 2 个版本的窗口：tx1 已提交（BTree 有 beginTS=T1）+ tx2 正在 commit（BTree 有 beginTS=T2, 前一版=T1）。V2 的 `prevFlag + prevBeginTS + prevVal` 恰好覆盖这 2 个版本。第 3 个版本不可能同时存在（tx3 必须等 tx2 释放 KeyLock）。

#### 兼容性

> **无需兼容 V1**：项目尚未推进到生产环境，无存量数据。直接替换存储格式，删除 VersionChain 全链路。不需要 FlagV2 分流、不需要迁移期、不需要 versionStore 保留。

#### 删除文件清单

| 文件 | 行数 | 原因 |
|------|------|------|
| `version_chain.go` | ~240 | VersionChain/VersionStore/VersionNode 完全删除 |
| `gc.go` | 缩减 ~30 | gcCycle() 中 versionStore.Range+Prune 删除, 保留 GCStats |

#### 预期收益（基于 pprof 数据）

| 场景 | 当前 QPS | 预期 QPS | 提升 |
|------|------:|------:|:--:|
| `txn-get-1` | 2.58M | **~8-12M** | 3-5x |
| `txn-get-10` | 4.12M | **~15-20M** | 3-5x |
| `txn-get-100` | 4.23M | **~18-25M** | 4-6x |
| `txn-put-1` | 717K | **~800K** | +12% |
| `txn-put-100` | 936K | **~1.0M** | +7% |

**收益来源**：
- Get 路径: 删除 versionStore.Load + Generation 校验 + 链遍历 + 重试循环 → ~70% CPU 节省
- Write 路径: 删除 Prepend CAS + versionStore.LoadOrStore → ~10% CPU 节省
- Memory: 无 VersionChain 堆节点 + 无 sync.Map per-key 条目 → ~5-10MB 节省
- GC: 无 VersionStore.Range + Prune 扫描

#### 实现步骤

```
Step 1 (~50行): codec.go 修改存储格式
  - BuildMVCC(flag, ts, val, prevFlag, prevTS, prevVal):
      prevFlag归一化: prevFlag & 0x01 → 仅存 0x00/0x01
      格式: [flag:1][prevFlag:1][prevTS:8][prevValLen:2][prevVal][ts:8][val]
      Insert(无旧值): prevFlag=0, prevTS=0, prevValLen=0
  - ParseMVCC(raw) → MVCCValue{Flag, BeginTS, RealVal, PrevFlag, PrevBeginTS, PrevVal}
    PrevBeginTS!=0 → 有效 prev; ==0 → 无 prev (Insert)
  - MVCCValue 新增字段: PrevFlag byte, PrevBeginTS uint64, PrevVal []byte

Step 2 (~80行): snapshotGet 简化
  - 替换 80 行 chain-based 读为 25 行 inline-version 读
  - 完全移除: versionStore.Load, Generation, 链遍历, 重试循环

Step 3 (~60行): commitKey 删除 Prepend
  - 删除 versionStore.LoadOrStore + Prepend + prePrependHead
  - BuildMVCC 传入 entry.OldFlag/OldBeginTS/OldValue
  - UndoEntry 字段简化 (删除 PrePrependHead/PrependSucceeded)

Step 4 (~30行): Rollback 简化
  - rollbackOneKey: 删除 version chain CAS revert + bumpGeneration
  - 仅保留 BTree value 恢复 (CommitTS 验证保留)

Step 5 (~30行): 删除 version_chain.go + gc.go 缩减
  - 删除 version_chain.go (~240行)
  - 删除 txManager.versionStore 字段
  - gc.go: gcCycle() 删除 versionStore.Range + Prune → 缩减 ~30 行
  - 清理所有 imports

Step 6 (~20行): 测试适配
  - 更新所有 ParseMVCC/BuildMVCC 引用
  - 新增内嵌版本 encode/decode 测试
  - 验证 Rollback 正确性
```

#### 限制与假设

- **prevValLen 上限**: uint16 → 65535 字节。适用于 KV value < 64KB 的场景（NexKV 轻量 KV 定位）。若需支持大 value 需改为 uint32（+2 字节 per entry）
- **2 版本上限安全**: 悲观锁 KeyLock 保证同 key 最多 2 个并发版本（详见上方连续写入分析），设计覆盖全部实际场景
- **无需兼容旧格式**: 项目未推进到生产，直接替换存储格式，旧 Flag 不变（0x00/0x01），新增 prev 字段

#### 风险

- **回滚一致性**: 旧格式回滚通过 CAS revert chain head，新格式直接 Set 旧值。两者在迁移期内并存——V1 key 的回滚仍走 CAS revert 路径（versionStore 保留期间）
- **压缩/Checkpoint**: 两者通过 BTree page flush 自然处理，不额外影响
- **gc.go 依赖**: versionStore.Range 删除后 gcCycle 变为 no-op。GC 能力由 BTree epoch-based 页面回收接管

#### 风险

- **prev 字段增加 BTree value 尺寸**: 每个 key 多 11~N 字节（prev header + prevVal）。对内存敏感场景需权衡
- **压缩/Checkpoint**: BTree page flush 自然处理，不额外影响

### 优先级与预期收益

```
### 改动 ① 详细对比：PreCheck → 悲观锁（对标 Lealone RowLock）

> Lealone 悲观锁真实代码:
> `AOTransaction.java:85-100`: `addLock(RowLock)` → `LinkedList<RowLock> locks` → `unlock()` 遍历释放
> `RowLock.java:16-38`: `lock(Lockable)` → per-key 行锁
> NexKV 新方案: `KeyLock.Lock()` in `Put()` → `heldLocks []*KeyLock` → `Commit` 时 `defer Unlock`

**流程图**：
```
NexKV 旧 (乐观锁, Commit 时验证):
  Put(k,v) → WriteBuffer.Put(k,v)           // 纯内存, 不加锁
           → ReadFingerprint(k, oldVal)     // 记录 readSet
  Commit() → preCheck()                     // ← 逐 key GetRaw 重读 BTree 验证指纹!
           → commitTS 分配
           → applyWriteBuffer()
               → commitKey(k):              // 第一次获取 KeyLock
                   kl.Lock()
                   GetRaw(k)  // 又一次重读 (in lock)
                   验证 beginTS 冲突
                   Prepend VersionChain
                   BTree.Set(k, newVal)
                   kl.Unlock()
           → cleanup

NexKV 新 (悲观锁, Put 时加锁, Commit 直接提交):
  Put(k,v) → KeyLock(k).Lock()              // ← 提前获取锁 (对标 Lealone RowLock)
           → GetRaw(k) → 验证冲突          // ← 在 Put 时验证, 不是 Commit 时
           → WriteBuffer.Put(k,v)
           // 锁提前: Rollback = 丢弃 WriteBuffer + 释放锁 (无需 UndoEntry)
  Commit() → commitTS 分配
           → applyWriteBuffer()
               → commitKey(k):              // 锁已在 Put 时获取
                   直接 Prepend + BTree.Set(k, newVal)
                   省略 GetRaw 重读!
           释放所有 KeyLock → kl.Unlock()
           → cleanup

Lealone (悲观锁, put 时加锁):
  put(k,v) → RowLock.lock(k)                // 悲观行锁
           → undoLog.add(k, oldVal)         // 记录 undo
           → map.put(k, newVal)             // 直接写 BTree (无 WriteBuffer)
  commit() → undoLog.commit()              // 标记已提交
           → writeRedoLog()                 // WAL 一次写入
           → RowLock.unlock(k)             // 释放锁
```

**三方代码对比**：

| | NexKV 旧 (乐观锁) | NexKV 新 (悲观锁) | Lealone |
|---|---|---|---|
| Put | 无锁, 读 oldVal→计算指纹 | **KeyLock.Lock** + 读 oldVal + 验证冲突 | RowLock.lock + undoLog.add |
| Commit | PreCheck(N次GetRaw) + commitKey(N次锁) | commitKey(N次,省略GetRaw) | undoLog.commit + RedoLog |
| 冲突检测 | Commit 时重读验证 | Put 时上锁验证 | Put 时上锁验证 |
| GetRaw 次数 | 2N (PreCheck+commitKey) | N (仅 Put 时) | N (put时读oldValue存UndoLog) |

```go
// NexKV 新: Put 时提前获取 KeyLock
func (tx *SnapshotTx) Put(key, value []byte) error {
    keyStr := string(key)

    // ① 获取 KeyLock（对标 Lealone RowLock）
    lockVal, _ := tx.engine.keyLocks.LoadOrStore(keyStr, &KeyLock{})
    kl := lockVal.(*KeyLock)
    if err := kl.Lock(); err != nil { return err }
    tx.heldLocks = append(tx.heldLocks, kl)  // 记录, Commit 时释放

    // ② 读当前值并验证冲突 (仅在 Put 时验证, Commit 不验证)
    var oldBeginTS uint64
    var oldFlag byte
    var oldValue []byte
    if raw, err := tx.engine.storage.GetRaw(ctx, key); err == nil {
        mvccVal, _ := ParseMVCC(raw)
        oldFlag = mvccVal.Flag
        oldBeginTS = mvccVal.BeginTS
        oldValue = mvccVal.RealVal
    }
    // oldBeginTS == 0 表示 INSERT (key 不存在), >0 表示 UPDATE/DELETE

    // ③ 写入 WriteBuffer
    tx.writeBuffer.Put(keyStr, value, oldValue, oldFlag, oldBeginTS)
    return nil
}

// NexKV 新: Commit 省略 PreCheck + commitKey 省略 GetRaw
func (tx *SnapshotTx) Commit(ctx context.Context) error {
    // ❌ 去掉 preCheck()!  Put 时已经验证过了
    commitTS := tx.engine.tsGen.NextTS()

    for _, key := range tx.writeBuffer.OrderedKeys() {
        entry, _ := tx.writeBuffer.Get(key)
        // ❌ 去掉 commitKey 中的 GetRaw!  Put 时已持有锁 + 已验证
        chain := tx.engine.versionStore.Load(key)
        tx.engine.versionStore.Prepend(key, commitTS, entry.OldValue, entry.OldFlag)
        encoded, _ := BuildMVCC(FlagNormal, commitTS, entry.Value)
        tx.engine.storage.Set(ctx, []byte(key), encoded)
    }

    // 释放所有锁
    for _, kl := range tx.heldLocks { kl.Unlock() }
    tx.cleanup()
    return nil
}
```

### 改动 ② 详细对比：BTree 多层分裂（对标 Lealone Page.split + index propagation）

> Lealone split propagation 真实分布在:
> `PageOperations.Put.writeLocal()` → `Page.addLeafEntry()` → `Page.needSplit()` → `Page.split(int at)` → `BTreeMap` 递归向上传播
> 关键源码: `Page.java:176-186` (needSplit/split), `PageOperations.java:100-160` (writeLocal)

**流程图**：
```
NexKV Phase 5 (单层 Leaf Page):
  Set(k,v) → 找到 Root(leaf page)
           → leaf.Update() / leaf.TryInPlace()
           → leaf.IsFull() → leaf.Split() → 新页面
           → ❌ 没有向上传播! 子页面悬空, 只有 Root 指向它
           → ❌ 下次 Set 还是从 Root 开始 → CAS 竞争全在同一个 page

NexKV Phase 6 (多层 B+Tree, 本次实现):
  Set(k,v) → 找到 Leaf Page → 更新
           → leaf.IsFull() → leaf.Split() → left+right
           → root.IsInternalPage() → root.InsertChild(splitKey, rightPageID)
           → root.IsFull() → root.Split() → 新 root
           → ✅ 叶子分裂向上传播, CAS 竞争分散

Lealone (多层 B+Tree, 已完整实现):
  同 NexKV Phase 6 — B+Tree split + propagation + page fill rate 优化
```

**三方代码对比**：

```go
// NexKV Phase 5 (当前): Split 存在但无 propagation
//   真实 API: writeOperation(b, key, func(leaf LeafPage) (*leafMutation, error) { ... })
func writeOperation(b *BTree, key []byte, op func(LeafPage) (*leafMutation, error)) error {
    // ...
    if newLeaf.IsFull() {
        left, right, splitKey, _ := newLeaf.Split()
        // ❌ left+right 创建了, 但只替换了 Root
        // 没有 InternalPage 来存 child pointers
        // 没有 split propagation
    }
}

// NexKV Phase 6 (新增): Split propagation
//   ⚠️ 以下方法为 Phase 6 设计, 当前均不存在:
//   RootPageRef.IsLeaf(), InternalPage.InsertChild(), InternalPage.Split()
//   AllocNodePage() 返回 (model.PageID, error), Init/InsertChild 需新增
func (b *BTree) propagateSplit(root *RootPageRef, left, right LeafPage, splitKey []byte) error {
    if root.IsLeaf() { // ← Phase 6 新增: RootPageRef.IsLeaf()
        // Root 是叶子 → 创建 InternalPage as new root
        internalPageID, _ := b.storage.AllocNodePage() // 真实签名: (PageID, error)
        // 获取 InternalPage handle & Init & InsertChild  ← Phase 6 新增方法
        // TODO: internal.InsertChild(left.MinKey(), left.PageID())
        // TODO: internal.InsertChild(splitKey, right.PageID())
        // b.rootRef.ReplaceRoot(root.GetPageInfo(), internalPageID, internalChildren)
        return nil
    }
    // Root 是 InternalPage → 插入 child pointer (Phase 6 新增)
    // TODO: root.GetInternalPage().InsertChild(splitKey, right.PageID())
    // if root.GetInternalPage().IsFull() { ... upward propagation ... }
    return nil
}
```

```java
// Lealone: 真实 split propagation 分布在 Page.addLeafEntry/Page.split 中。
// 以下为合并简化版, 展示核心循环:
private void preparePut(Page p, Object key, Object value) {
    while (p.isNode()) { p = p.getChildPage(p.getPageIndex(key)); }
    p.addLeafEntry(key, value);
    while (p.needSplit()) {
        Page right = p.split();
        if (p == root) { Page newRoot=Page.createNode(this); newRoot.setChild(0,p); newRoot.setChild(1,right); root=newRoot; }
        else { p.getParent().setChild(key, right); }
        p = p.getParent();
    }
}
```

### 改动 ③ 详细对比：版本内嵌 BTree（对标 Lealone TransactionalValue）

> Lealone 版本内嵌真实代码:
> `TransactionalValue.java`: `commit(boolean insert, StorageMap map, Object key, Lockable lockable)` → `lockable.setCommitted(true)` + `map.put(key, lockable)`
> `rollback(Object oldValue, Lockable lockable)` → `lockable.setValue(oldValue)` + `lockable.setCommitted(true)`

**流程图**：
```
NexKV 旧 (VersionChain):
  Put("k1","v2") → WriteBuffer
  Commit() → commitKey("k1"):
    KeyLock.Lock
    GetRaw("k1") → [Flag=0][beginTS=100][RealVal=v1]
    Prepend(k1, commitTS=200, oldFlag=0, oldVal=v1)
      → VersionChain: head→[commitTS=200 flag=0ts=100 v=v1]
    BTree.Set("k1", [Flag=0][beginTS=200][RealVal=v2])
    KeyLock.Unlock

  Get("k1", snapshotTS=150):
    raw = BTree.GetRaw("k1") → [beginTS=200 > 150] ← 版本太新
    chain = VersionStore.Load("k1") → [commitTS=200, ...]
    遍历: commitTS=200 > 150? No → bestNode → v1
    return v1 ✓

  存储: BTree [Flag=0][TS=200][v2]  +  VersionChain node [TS=200,old=v1] = 双份

Lealone (TransactionalValue 内嵌):
  put("k1","v2") → Lockable.setValue(v2)
                 → TransactionalValue.commit(true, map, key, lockable)
                 → 新版本覆盖旧版本 (BTree entry 内嵌版本号)

  get("k1", snapshotTS=150):
    value = map.get("k1")
    if value.beginTS <= 150 → 可见
    return value

  存储: BTree entry only [beginTS=latest][value] = 单份
```

**三方代码对比**：

```go
// NexKV 旧: VersionChain 分离存储
SnapshotTx.snapshotGet(key):
    raw := BTree.GetRaw(key)              // ① 读 BTree
    if mvccVal.BeginTS <= tx.snapshotTS:  // ② 版本可见 → 直接返回
        return mvccVal.RealVal
    // ③ 版本不可见 → 遍历 VersionChain
    chain := tx.engine.versionStore.Load(key)
    for node := chain.Load(); node != nil; node = node.next.Load() {
        if node.commitTS > tx.snapshotTS && !node.rolledBack {
            bestNode = node               // ④ 找到最佳匹配
        }
    }
    if bestNode != nil { return bestNode.value }  // ⑤ 返回旧版本

// NexKV 新: 版本内嵌 (对标 Lealone TransactionalValue)
//   [Flag:1][beginTS:8][versionCount:4][commitTS:8][value:N]
//    ↓ 多版本串联在同一个 BTree value 中
SnapshotTx.get(key):
    raw := BTree.GetRaw(key)              // ① 一次性读
    versions := ParseMultiVersion(raw)    // ② 解析所有版本 (一次 mem 操作)
    for _, v := range versions {
        if v.BeginTS <= tx.snapshotTS && !v.RolledBack {
            return v.Value                // ③ 返回第一个可见版本
        }
    }
    // ❌ 没有 VersionChain traversal!
    // ❌ 没有 versionStore.Load!
    // ❌ 没有 Generation() 乐观校验!
```

```java
// Lealone: TransactionalValue — 版本内嵌在 Lockable 中
class TransactionalValue {
    // commit: 标记提交, 版本在当前值中
    static void commit(boolean insert, StorageMap map, Object key, Lockable lockable) {
        lockable.setCommitted(true);       // 直接标记, 不创建额外链
        if (insert) map.put(key, lockable);
    }

    // rollback: 用 oldValue 替换当前值
    static void rollback(Object oldValue, Lockable lockable) {
        lockable.setValue(oldValue);       // 直接恢复, 无链遍历
        lockable.setCommitted(true);
    }
}
```

#### 已废弃：旧乐观锁 vs 悲观锁 A/B 对比（仅供参考）

| 场景 | 乐观锁 (PreCheck) | 悲观锁 (Phase 1) | 提升 |
|------|------:|------:|:----:|
| `txn-put-1` | 402K | **458K** | **+14%** |
| `txn-put-10` | 520K | **584K** | **+12%** |
| `txn-put-100` | 547K | **621K** | **+13%** |
| `txn-put-1000` | 539K | **608K** | **+13%** |
| `txn-put-10000` | 536K | **596K** | **+11%** |
| `txn-get-100` | 4.18M | 4.14M | −1%（读不受 PreCheck 影响） |

> **结论**：悲观锁在纯写场景稳定提升 **11-14%**。收益来自 `commitKeyLocked()` 省略一次 GetRaw（乐观锁 commitKey 在 KeyLock 内仍需 GetRaw 读当前值）。
>
> **⚠️ 混合读写悲观锁存在死锁**：`txn-rw-*-*` 全部阻塞（>600s）。根因：Get 不持有锁但在读后 Put 获取了 KeyLock，与预加载数据的锁顺序冲突。后续需修复——为混合场景的 Get 增加 KeyLock（或使用 snapshotTS 跳过已锁 key）。
>
> **preTouch 效果**：`PageManager.Alloc` flat% 从 30.3% → 降为 0（pprof 不可见）。QPS 无明显变化（btree_bench）到 +23%（btree-txn-bench txn-put-100）。收益集中在高 Alloc 频率的事务路径。预热逻辑默认在 `NewPageManager` 中执行，零 Go 分配，128GB 以下 mmap 自动预热。
```

---

## 七、pprof 瓶颈分析 & preTouch 优化

### pprof 发现（2026-06-08，100K ops, txn-put-100）

```
=== 预热前 (preTouch off) ===
  flat  flat%
  30.3% PageManager.Alloc       ← 🔴 first-touch page fault (~1µs/page)
  24.2% memclrNoHeapPointers    ← 🔴 Go 堆清零
  15.2% madvise                 ← 🔴 OS page 管理

=== 预热后 (preTouch on, 默认) ===
  flat  flat%
  17.6% madvise                 ← 残留 OS 开销
  14.7% memclrNoHeapPointers    ← Go 堆清零（不受预热影响）
   8.8% preTouchPages (startup) ← 一次性开销，benchmark 期间不存在
   5.9% PageManager.Alloc       ← ✅ 从 30.3% 降到低水平（仅 freeList 路径的 clearPage）
```

### 512MB vs 8GB preTouch 效果

| 指标 | 512M 无预热 | 512M 预热 | 8G 无预热 | 8G 预热 |
|------|------:|------:|------:|------:|
| txn-put-100 QPS | 482K | **591K (+23%)** | 485K | **538K (+11%)** |
| Alloc flat% | 30.3% | <5% | 44.0% | 5.6% |
| 启动预热耗时 | — | 15ms | — | 1.2s |
| GC 压力 | 0 | 0 | 0 | 0 |

> **方案对比**：最初尝试 freeList 预热（PreWarmFreeList），8G 下 QPS 从 485K 降到 297K（−39%）。原因是 2M 个 freeList 节点（每个 `&node{}` 堆分配）产生 48MB GC 压力。轻量 preTouchPages 只做 pointer 写，零分配，8G 下依然有效。

### 瓶颈根因图

```
100% CPU
├── 52.9% 内存分配 + 清零（COW 页面分配）
│   └── 每次 BTree.Set 分配新 4KB 页 → first-touch page fault
│       → preTouch 解决：page 已物理分配，Alloc 内 header.version=1 是 cache 内写
│
├── 31.8% BTree 写入 (writeOperation → Insert → CAS replace)
│   └── 单层 Leaf Page 满后频繁 Split → 更多的 Alloc+copyPage
│       → 改动 ② (多层 B+Tree) 解决
│
├── 17.6% 事务提交 (commitKey → storage.Set)
│   └── KeyLock 临界区内调用 BTree.Set
│       → 悲观锁已优化 (commitKeyLocked 省略 GetRaw)
│
└── 9% MVCC 编码分配 (BuildMVCC + ParseMVCC, 55MB GC)
    → sync.Pool 复用 []byte (后续)
```

### 优化日程（最终）

| 序号 | 优化 | 状态 | 效果 |
|------|------|:--:|------|
| 0a | preTouch 默认预热 | ✅ | Alloc 30%→<5% |
| 0b | WriteBuffer sync.Pool | ✅ | txn-put-1 +19% |
| ① | PreCheck→悲观锁 (统一路径) | ✅ | txn-put-100: 747K→936K (+25%) |
| ②a | COW 批量化 (事务路径 SetBatch) | ✅ | txn-put-1K: 799K→951K (+19%) |
| ③ | 版本内嵌 BTree (消除 VersionChain) | ✅ | txn-put-100: 936K→1.16M (+24%), −763行 |
| ④ | **SetBatch 修复: SetWithRetry 替代 PageDispatcher** | ✅ | batch-set-64: 977K→2.84M (+190%) 🔥 |

---

### 改动 ④：SetBatch 修复 — SetWithRetry 替代 PageDispatcher

> **Bug**：`TestSetBatch_MixedNewAndExisting` 中 mx-006 读取 stale 值。根因在 PageDispatcher/executeBatch 的 split 期间 pageID 失效（Theory J: 写入报告成功但不持久化）。详细分析见 [[2026-06-08-setbatch-split-brainstorm]]。

> **修复**：`BTree.SetBatch` 从 `BatchWriter→PageDispatcher→WorkerPool→SetWithRetry` 简化为直接逐 key `SetWithRetry`。消除 PD 的 split 期间 stale pageID 问题。

> **效果**：
> - TestSetBatch_MixedNewAndExisting 稳定通过，移除 TODO skip
> - batch-set-64:  977K→**2.84M** (+190%)
> - batch-set-256:  1.57M→**2.86M** (+82%)
> - batch-set-1024: 2.17M→**2.89M** (+33%)
> - 事务路径不受影响（BTree.SetBatch 仅被非事务路径使用）

#### 调试过程（详见 [[2026-06-08-setbatch-split-brainstorm]]）

| 测试 | 结论 |
|------|------|
| SetWithRetry 79次逐key（无PD） | ✅ ALL OK — Bug 不在 writeOperationWithRetry |
| batch 75 SetBatch→PD | ✅ OK |
| batch 79 SetBatch→PD | ❌ mx-006 stale — Bug 仅在 PD 层 |
| SetWithRetry 逐key 79次 + split | ✅ root→internal, 2 children — writeOperation 正确 |

> **结论**：Bug 仅在 `SetBatch → PageDispatcher → WorkerPool → executeBatch` 路径。`SetWithRetry` 逐 key 调用完全正确。修复方案：用 `SetWithRetry` 替代 PD 实现 `BTree.SetBatch`。

---

## 八、Lealone 事务 benchmark 源码

```java
// LealoneTxnBenchmarkRunner.java — 新增文件
// 路径: lealone-test/.../aose/LealoneTxnBenchmarkRunner.java

TransactionEngine te = TransactionEngine.getDefaultTransactionEngine();
te.init(config);
Storage storage = StorageEngine.getDefaultStorageEngine()
    .getStorageBuilder().storagePath("target/bench-txn").openStorage();

// 事务模式: begin→put×N→commit
Transaction tx = te.beginTransaction();
TransactionMap<Integer, String> map = tx.openMap(label, storage);
for (int i = 0; i < batchSize; i++) { map.put(i, "v-" + i); }
tx.commit();
```

## 九、关联文档

- [[feat(persist)]] — PersistWAL + PersistCheckpoint 实现（#fa18f8c）
- [[NexKV vs Lealone 持久化设计深度对比]] — Lealone UndoLog/RedoLog 事务机制
- [[mvcc/transaction.go]] — NexKV MVCC Tx 实现

---

> **Spike 状态**：🔄 进行中（0a/0b/①/②a 已实现，③ 设计完成待实现）  
> **下一步**：实现改动 ③（版本内嵌 BTree，~400 行，预期 txn-get-100 +4-6x）

---

## 附录：评审记录与修复历史

### 评审轨迹

| 轮次 | 日期 | 评审人 | 综合评分 | 发现问题 | 结论 |
|:----:|------|--------|:--------:|------|:----:|
| 第 1 轮 | 06-08 | 架构师 | 6.8/10 | 3🔴 + 3🟡 | ⚠️ 有条件通过，3个严重阻塞实现 |

### 第 1 轮问题与修复

| 严重程度 | 问题 | 修复 |
|:---:|------|------|
| 🔴 | 伪代码 `BeginTx` 缺少 `WithTSGenerator` → 运行时 panic | §4 伪代码补 `btree.WithTSGenerator(mvcc.NewLocalTS())` |
| 🔴 | 伪代码无 warmup + Commit 不在 timer 包裹内 → 度量失真 | §4 伪代码加入 warmupTxns + `t0 := time.Now()` 包裹 begin→put→commit 循环 |
| 🔴 | 事务 key 唯一性不清晰 → KeyLock 竞争可能误判 | §4 伪代码注释「不同事务使用不同 key，避免 KeyLock 竞争」 |
| 🟡 | Commit 双 WAL fsync（TxPrepare+TxCommit）开销未标注 | §2 新增架构差异分析：NexKV 2×fsync vs Lealone 1×fsync |
| 🟡 | txn+WAL 组合场景预期缺失 | §4/§5 补充 `txn-put-1-wal` (~50-100 QPS)、`txn-put-100-wal` (~5K-15K) |
| 🟡 | batch 1000→10000 下降预期缺失（sort + VersionChain 深度） | §5 预期表补充 `txn-put-10000` 行 (~600K-900K) + 原因分析 |

### 第 2 轮（Kimi 审核）：8 项全部确认通过

| 严重程度 | 问题 | 修复 |
|:---:|------|------|
| 🔴 P1 | warmup 未给出实现 | §4 补充 `warmupTxnOps()` 完整代码 |
| 🔴 P1 | 多线程 key 竞态未解决 | §4 补充 `runTxnConcurrent()` (实现阶段补代码) |
| 🔴 P1 | 缺少 Rollback 场景 | §4 新增 Rollback 场景表 (txn-rollback-1/100) |
| 🟡 P2 | Checkpoint 触发条件未说明 | §4 补充 3 种触发条件 (count/idle/Close) |
| 🟡 P2 | 隔离级别参数未使用 | §4 伪代码加入 isolation 映射 + `BeginTx(WithIsolation)` |
| 🟡 P2 | 代码结构多余行 | 已删除 `└── (其他不变)` |
| 🟢 P3 | 错误处理缺失 | §4 伪代码 `_ = ...` → `if err != nil { log.Fatal(err) }` |
| 🟢 P3 | txn-put-10000 预期变化未说明 | §5 补充 Lealone 对比 + sort 分析 |

| 轮次 | 日期 | 评审人 | 综合评分 | 发现问题 | 结论 |
|:----:|------|--------|:--------:|------|:----:|
| 第 1 轮 | 06-08 | 架构师 | 6.8/10 | 3🔴 + 3🟡 | ⚠️ 有条件通过 |
| 第 2 轮 | 06-08 | Kimi | 8.8/10 | 3🔴 + 3🟡 + 2🟢 | ✅ 全部闭环 |
| 第 3 轮 | 06-08 | 架构师+code-review | 8.5/10 | 2🔴 + 3🟡 + 4🟢 | ✅ 全部修复 (改动②b计划评审) |
| 第 4 轮 | 06-08 | Claude | — | 1🔴 + 2🟡 + 3🟢 | ✅ 全部修复 (改动④计划评审) |

### 第 4 轮问题与修复（改动 ④ 计划评审）

| 严重程度 | 问题 | 修复 |
|:---:|------|------|
| 🔴 C1 | 方案B伪代码创建newBatch结果丢失——Dispatch不收集newBatch | 改为最小方案: 逐key ResolvePageID → 更新 batch.pageID, 不创建新batch |
| 🟡 H1 | TryInPlace 退化 → COW 频率升高 → split 更频繁的因果链缺失 | 根因分析补充: TryInPlace失败(54B>27B) → COW Update → IsFull → Split |
| 🟡 H2 | 方案A只检查tasks[0]，mid-batch split 永远检测不到 | 淘汰方案A，标注"❌ 只能检测 batch 开始时的 split" |
| 🟢 M1 | 方案C仅CAS耗尽触发，正常 split 路径基本不命中 | 淘汰方案C，标注"❌ 正常路径不触发" |
| 🟢 M2 | 伪代码中 results 分配两次（冗余） | 随 C1 修复一并解决 |
| 🟢 M3 | "ResolvePageID 需暴露"描述不准确（已导出） | 删除该描述 |
