# 【PR全流程文档】Feature - btree_bench KV Set 落盘 Benchmark

> **文档说明**：本文档包含「前置规划」和「后置总结」两部分，记录 btree_bench 增加 KV Set 落盘（WAL + AO 文件写入）基准测试的全流程。

---

## 第一部分：前置部分（开工前必完成）

### 1. 基础信息（与分支/PR绑定）

| 项目 | 内容 |
|------|------|
| 工作类型 | 新功能开发（Feature） |
| PR编号 | PR-XXX（创建GitHub PR后补充完整） |
| 分支名称 | `docs/btree-bench-persistence-benchmark` |
| 工作主题 | btree_bench：KV Set 落盘（WAL + AO 文件写入）吞吐量基准测试 |
| 负责人 | jzhang405 |
| 分支创建日期 | 2026-06-07 |
| 计划完成日期 | 2026-06-10 |
| 关联需求/Issue | 存储引擎持久化性能度量 |
| 更新类型 | ☑ 新增功能（在 btree_bench 中新增落盘 benchmark） |

### 2. 背景与目标（为什么需要）

#### 2.1 背景

- **当前状态**：`cmd/tools/btree_bench` 已实现 BTree KV 纯内存操作（Set/Get/BatchSet/BatchGet）的吞吐量基准测试，覆盖顺序/并发/混合读写等多维度场景。
- **缺失能力**：现有 benchmark 仅度量 mmap 内存页面的 COW 操作性能，**未覆盖落盘路径**——即 WAL（Write-Ahead Log）写入和 AO（Append-Only Chunk）文件写入。
- **业务价值**：在 NexKV 的存储架构中，数据持久化是最关键的 I/O 路径。Set 操作经过 BTree COW → WAL 序列化 → WAL 文件 fwrite/fsync → ChunkManager 页面序列化 → AO 文件写入，这一整条链路是实际生产环境的真实写入路径，必须在 benchmark 中可度量。

#### 2.2 目标

1. **准确性**：benchmark 度量结果反映真实落盘路径的吞吐量，包含 WAL 序列化/写入/Sync 的核心耗时（AO 页面写入异步执行，不阻塞操作计数，简化说明见 §3.1.2）。
2. **可控性**：通过命令行 flag 控制 WAL Sync 策略（EveryWrite / GroupCommit / EverySecond）、AO 文件大小、是否启用落盘，方便对比纯内存 vs 落盘性能。
3. **可观测性**：输出落盘相关的关键指标：WAL 写入字节数、Sync 次数、AO 文件写入次数、ChunkManager 统计信息。

#### 2.3 明确边界

- **本次实现**：
  - 在 `cmd/tools/btree_bench` 中新增 `-persist` flag 控制的落盘模式
  - 落盘模式下，每个 Set 操作经过完整的 BTree → WAL → AO 路径
  - 新增 WAL Sync 策略选择 flag（`-wal-sync`）
  - 输出落盘相关的吞吐量和 I/O 统计
- **暂不实现**：
  - 落盘模式下的 BatchSet/BatchGet（Phase 2）
  - 落盘 + 多节点复制场景
  - 落盘恢复后的正确性验证（由集成测试覆盖）

### 3. 技术设计

#### 3.1 落盘数据流

```
Set(key, value)
    │
    ▼
┌──────────────────────────────────────────────────┐
│ 1. BTree.Set()                                   │
│    ├─ COW 页面分配（OffheapBTreeStorage.AllocXXX）│
│    ├─ MVCC 值编码（mvcc.BuildMVCC）               │
│    └─ 页面 CAS 替换                              │
└──────────────────────────────────────────────────┘
    │
    ▼
┌──────────────────────────────────────────────────┐
│ 2. WAL 写入（落盘第一步）                         │
│    ├─ WALEntry 序列化（MarshalWALEntry）          │
│    │   └─ 二进制格式：[CRC32C:4][Len:4][LSN:8]   │
│    │                 [Type:1][KeyLen:2][Key:N][ValueLen:4][Value:M]│
│    ├─ DiskWAL.Append() → fwrite                   │
│    └─ SyncPolicy 控制 fsync 行为                  │
│        ├─ EveryWrite: 每条 fsync                  │
│        ├─ GroupCommit: 批量 fsync（16条/1ms）      │
│        └─ EverySecond: 每秒 fsync                 │
└──────────────────────────────────────────────────┘
    │
    ▼
┌──────────────────────────────────────────────────┐
│ 3. AO 页面持久化（落盘第二步）                     │
│    ├─ PageSerializer.Serialize(page) → []byte     │
│    ├─ ChunkManager.Allocate(size, pageType)       │
│    │   └─ 在 .ao chunk 文件中预留位置              │
│    ├─ ChunkManager.WritePage(pos, data)            │
│    │   └─ 写入 .ao 文件（256MB/段）               │
│    └─ Storage.UpdatePageLocs(mapping)             │
│        └─ 记录 pageID → ChunkPosition 映射        │
└──────────────────────────────────────────────────┘
```

> **⚠️ 重要：上述数据流是本次 PR 的设计目标，不是当前代码的现状。**  
> 当前 `BTree.Set()` 是纯内存 COW 操作——不调用 WAL，不调用 ChunkManager。  
> WAL 只在 MVCC Transaction.Commit 路径中使用，ChunkManager 只在 Checkpoint (EnumeratePages) 中使用。  
> 本次 PR 在 `persistSetLoop()` 中把三者串联起来（见 §3.1.3）。

#### 3.1.1 当前架构：BTree / WAL / AO 三者分离

> **源码确认**：当前 NexKV 的 BTree、WAL、AO 是三个独立模块，`BTree.Set()` 路径中**无任何 WAL 调用，无任何 ChunkManager 调用**。

```
当前 Set() 路径 (源码分析):

  BTree.Set()                          ← btree.go:321-370
      ├─ COW 页面分配                   ← OffheapBTreeStorage.AllocXXX
      ├─ MVCC 值编码                    ← mvcc.BuildMVCC
      ├─ CAS 页面替换                   ← leafPageHandle.TryInPlace / Update
      └─ return                        ← 结束。纯内存，无磁盘 I/O

  WAL 模块 (service.WAL + DiskWAL)      ← 当前仅在以下路径使用：
      └─ MVCC Transaction.Commit()      ← mvcc/wal_integration.go
          └─ BTree.Set() 不走事务路径时不调用

  ChunkManager (service.ChunkManager + DiskChunkManager)
      └─ BTree.EnumeratePages()         ← 仅在 Checkpoint 时使用
          └─ BTree.Set() 不调用 ChunkManager

  BTree struct {                        ← btree.go:30-47
      rootRef        *RootPageRef
      storage        *OffheapBTreeStorage
      // ❌ 没有 WAL 字段
      // ❌ 没有 ChunkManager 字段 (仅 SetChunkManager 注入点)
  }
```

> **结论**：当前 `btree_bench` 测的 `seq-put` 1,990,694 QPS 是纯内存 COW 吞吐量，与 WAL/AO 模块完全无关。本次 PR 需要在 benchmark 层面把三者串联。

#### 3.1.2 接线方案演进：从 Benchmark-Loop 到 Decorator

> 方案经历了三轮演进，最终选择 **方案 D（装饰器模式）**。详细分析见 `docs/07_spike/btree-refactor/2026-06-07-lealone-persist-bench-refactor.md`。

**方案对比**：

| | 方案 A：BTree 内嵌 | 方案 B：Benchmark Loop | 方案 C：BTree Config | 方案 D：Decorator（✅） |
|---|---|---|---|---|
| 接入点 | `BTree.Set()` 内部 | `persistSetLoop()` | BTree config Option | `service.KVStore` 装饰器 |
| BTree 改动 | 侵入核心路径 | 不改动 | +7 字段 | **0 行** |
| SRP | ❌ | ✅ | ❌ 22 字段 | ✅ |
| benchmark/生产一致 | ✅ | ❌ | ✅ | ✅ |
| 对标 Lealone | ❌ | ❌ | ❌ 反模式 | ✅ 逐层对应 |
| 采纳 | ❌ 否决 | ❌ 被替代 | ❌ 否决 | ✅ |

**三轮演进**：

1. **方案 B**：benchmark 层松耦合——持久化拼接在 `persistSetLoop()` 中。问题：benchmark 和生产路径不一致，每个调用方都要自己接线。

2. **方案 C**：BTree config 内部化——通过 `WithPersistWAL()` / `WithPersistCheckpoint()` 注入。问题：BTree 字段从 15 膨胀到 22，违反 SRP；save() 在热路径同步阻塞导致 P99 长尾；与 Lealone 分层背道而驰（Lealone 的 BTreeMap 也无持久化）。

3. **方案 D（✅ 最终）**：装饰器模式——`PersistWAL` / `PersistCheckpoint` 实现 `service.KVStore` 接口，外挂到 BTree 之上。BTree 保持纯内存 15 字段零改动，对标 Lealone `BTreeMap(纯内存) + BTreeStorage/AOTransaction(持久化层)` 分层。

**方案 D 架构**：

```
               service.KVStore (统一接口)
              ┌──────────┴──────────┐
     PersistWAL              PersistCheckpoint
     (WAL 装饰器)             (Checkpoint 装饰器)
     tree.Set() + wal.Append  tree.Set() + go asyncSave()
              └──────────┬──────────┘
                     BTree (纯内存 15 字段, 零改动)
```

**本次 PR 范围**：`PersistWAL` 装饰器（WAL-per-op 模式）。`PersistCheckpoint` 装饰器在后续 PR 实现。

> **关联 Spike**：`docs/07_spike/btree-refactor/2026-06-07-lealone-persist-bench-refactor.md` — 方案 C vs D 详细对比、Lealone 三大机制映射、AOSE/AOTE 分界分析、6 项决策记录。

#### 3.1.3 并发模型

多 goroutine 并发写入时，落盘路径的线程安全保证：

- **WAL.Append()**：内部使用互斥锁（Mutex）保护分段写入，多 goroutine 可安全并发调用，写入顺序由锁的获取顺序决定。
- **ChunkManager.Allocate()**：使用原子操作（atomic.AddUint64）分配文件偏移量，保证并发分配无竞态。
- **ChunkManager.WritePage()**：基于已分配的偏移量进行 `pwrite`，不同页面写入到不同偏移量，天然线程安全。
- **Storage.UpdatePageLocs()**：使用 sync.Map 存储 pageID → ChunkPosition 映射，支持并发读写。

benchmark 中的并发写入路径与实际生产环境使用的同步机制一致。

#### 3.1.4 WAL/AO 写入原子性（Benchmark 简化假设）

benchmark 中为简化度量，采用以下时序约定：

- **计数时机**：WAL Sync 完成后即对操作计数 +1，不等 AO 写入完成。
- **AO 异步写入**：AO 页面写入在 WAL 确认后异步执行，不阻塞 benchmark 吞吐量计量。
- **与生产差异**：生产环境中 AO 写入完成后 WAL 才会记录 Checkpoint，确保恢复时数据一致。benchmark 中不实现此等待，因此在 EverySecond 策略下度量的吞吐量代表"WAL 确认即返回"的写入上限，而非全路径同步写入。

#### 3.2 新增 Flag 设计

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-persist` | bool | false | 启用落盘模式（WAL + AO 全路径） |
| `-wal-sync` | string | `group-commit` | WAL Sync 策略：`every-write` / `group-commit` / `every-second` |
| `-wal-dir` | string | `os.TempDir()+"/nexkv-bench-wal"` | WAL 文件目录 |
| `-ao-dir` | string | `os.TempDir()+"/nexkv-bench-ao"` | AO chunk 文件目录 |
| `-ao-chunk-size` | int | 256（MB） | 单个 AO chunk 文件大小上限 |

#### 3.3 Benchmark 场景

| 场景名称 | 落盘 | WAL Sync | 线程数 | 说明 |
|----------|------|----------|--------|------|
| `seq-put-persist-every-write` | ✔ | EveryWrite | 1 | 最强持久化保证下的顺序写入 |
| `seq-put-persist-group-commit` | ✔ | GroupCommit | 1 | 批量 fsync 优化下的顺序写入 |
| `seq-put-persist-every-second` | ✔ | EverySecond | 1 | 延迟 fsync 下的顺序写入（最大吞吐） |
| `seq-put-mem` | ✘ | - | 1 | 纯内存顺序写入（对照组） |
| `par-put-persist-4` | ✔ | GroupCommit | 4 | 4 线程并发落盘写入 |
| `par-put-persist-8` | ✔ | GroupCommit | 8 | 8 线程并发落盘写入 |
| `par-put-persist-16` | ✔ | GroupCommit | 16 | 16 线程并发落盘写入 |
| `par-put-mem-4` | ✘ | - | 4 | 纯内存并发写入（对照组） |

#### 3.4 输出指标

除现有 QPS 外，落盘模式下新增：

```
=== Persistence Stats ===
wal.segments        : 1           # WAL 段文件数量
wal.written_bytes   : 256MB       # WAL 总写入字节
wal.sync_count      : 62500       # fsync 调用次数（group-commit: 1M ops / 16条/批）
wal.avg_entry_size  : 53 bytes    # benchmark key=14B + value=16B + WAL header=23B（生产环境更长）
ao.chunks           : 2           # AO chunk 文件数量
ao.written_pages    : 65536       # 持久化页面数量
ao.written_bytes    : 512MB       # AO 总写入字节
ao.avg_page_size    : 4096 bytes  # 平均序列化页面大小
```

#### 3.5 代码结构

**接线方案**（方案 D：Decorator）：

> BTree 零改动。持久化通过 `PersistWAL` 装饰器实现 `service.KVStore` 接口，外挂到 BTree 之上。

```go
// persist_wal.go — PersistWAL 装饰器 (本次 PR)
type PersistWAL struct {
    tree     service.KVStore   // 被装饰的 BTree
    wal      service.WAL
    syncMode WalSyncMode
    batchCh  chan *service.WALEntry   // 批量队列
    // ...
}

func (p *PersistWAL) Set(ctx context.Context, key, value []byte) error {
    p.tree.Set(ctx, key, value)   // ① BTree 纯内存 COW (不改动)
    entry := getWALEntry()         // ② sync.Pool 复用
    // ...
    switch p.syncMode {
    case WalSyncEveryWrite:
        p.wal.Append(entry)       // ③ fwrite + fsync
    case WalSyncGroupCommit:
        p.batchCh <- clone        // ④ 批量队列 → 后台 goroutine sync
    }
}
```

```
internal/infrastructure/storage/
├── btree/                        # 不改动！纯内存 15 字段
│   ├── btree.go
│   └── ...
├── persist/                      # 新增 package
│   ├── persist_wal.go            # PersistWAL 装饰器 (本次 PR)
│   ├── persist_wal_test.go
│   └── (persist_checkpoint.go    # PersistCheckpoint 装饰器, 后续 PR)

cmd/tools/btree_bench/
├── main.go                       # 修改：通过 service.KVStore 接口运行 benchmark
├── main_test.go                  # 修改：测试 PersistWAL 装饰器
└── (不再需要 persist.go)
```

| 文件 | 改动量 | 说明 |
|------|:------:|------|
| `btree/btree.go` | **0 行** | 完全不改 |
| `persist/persist_wal.go` | +150 行 | 新文件 |
| `main.go` | -20 行 +10 行 | 简化接线，改用 `service.KVStore` 接口 |

### 4. 预期结果与风险

#### 4.1 实测基线（MacBook Pro M-series, Apple M4 Pro, 本地 SSD）

> **测试日期**：2026-06-07 | **Go**：1.21+ | **OS**：macOS Darwin 25.5.0

| 场景 | 实际 QPS | 说明 |
|------|----------|------|
| `seq-put` | **1,990,694** | 单线程纯内存写入基线 |
| `seq-get` | **6,238,264** | 单线程纯内存读取基线 |
| `par-put-4` | **4,747,568** | 4 线程并发写入 |
| `par-put-8` | **8,531,713** | 8 线程并发写入（峰值） |
| `par-put-16` | **5,334,792** | 16 线程，CAS 竞争开始显现 |
| `par-get-8` | **13,557,905** | 8 线程并发读取 |
| `mixed-8-r80` | **11,278,672** | 8 线程 80% 读混合负载 |

#### 4.2 外部对照基准：Lealone BTreeMap（Java, OpenJDK 21）

> 同一台机器运行 Lealone `BTreeMapBenchmarkRunner`，作为 BTree KV 存储引擎的跨语言跨实现对照。

**Lealone 持久化模型（源码分析）**：

| 参数 | 值 | 源码位置 |
|------|-----|---------|
| 写入模型 | **Checkpoint，非 WAL-per-op** | `BTreeStorage.save()` |
| Page Cache | **32MB** | `Constants.DEFAULT_CACHE_SIZE=32` × 1MB |
| Max Chunk 大小 | **256MB** | `StorageSetting.MAX_CHUNK_SIZE` |
| sync 触发时机 | **显式 save()/close()**，无后台线程、无 Timer | `BTreeStorage.java:294,302` |
| `put()` 路径 | **纯内存 BTree COW**，无 fwrite/fsync/RedoLog | `PageOperations.Put.writeLocal()` |
| `save()` 路径 | 序列化所有脏页 → 写新 Chunk 文件 → `FileChannel.force(true)` | `BTreeStorage.java:319-401, Chunk.java:308-317` |

> Lealone disk 模式的 `put()` 操作**不走任何磁盘 I/O**，只在显式 `save()` 时才批量写盘并 sync。

**Lealone 周期性 save() 实测（同一台机器）**：

> 每 N 条 put 后执行一次 `save()`，度量不同批量大小下的有效 QPS。每次 save() 创建新 Chunk 文件 + FileChannel.force(true)。

| Batch 大小 | put 耗时 | save 耗时 | 总耗时 | put QPS | **有效 QPS** | 说明 |
|:-:|:-:|:-:|:-:|:-:|:-:|------|
| 1 | 0.001ms | 4.88ms | 4.88ms | 726K | **205** | 每条 fsync（最低开销 ~5ms） |
| 16 | 0.007ms | 4.98ms | 4.98ms | 2.4M | **3,211** | 16条/批，save() 开销碾压 put |
| 100 | 0.036ms | 5.01ms | 5.04ms | 2.8M | **19,835** | |
| 1,000 | 0.34ms | 5.48ms | 5.82ms | 3.0M | **171,956** | 开始达到有意义的吞吐 |
| 10,000 | 1.80ms | 7.84ms | 9.63ms | 5.6M | **1,038,032** | |
| 100,000 | 17.9ms | 42.8ms | 60.7ms | 5.6M | **1,647,678** | |
| 1,000,000 | 201ms | 338ms | 540ms | 5.0M | **1,853,065** | 当前 `seq-put+save` |

> **关键发现**：
> - Lealone `save()` 有 **~5ms 固定开销**（创建 Chunk 文件 + 写 header + FileChannel.force），无论脏页多少
> - 小 batch（≤100）下 save() 开销占主导，有效 QPS 极低（205-19K）
> - 大 batch（≥100K）下 put 开始摊销 save 成本，但最高也只能到 ~1.85M QPS
> - **与 NexKV 预期对比**：NexKV every-write 预期 15K-30K QPS 基于 WAL fwrite+fsync ~0.03ms，而 Lealone save() 每条约 5ms（167x 慢），因为 Lealone 每批都写完整 Chunk header 而非增量 WAL Entry

**对照表**：

| 场景 | Lealone inMemory | Lealone disk (checkpoint) | NexKV mem | NexKV/Lealone mem | 说明 |
|------|:-:|:-:|:-:|:-:|------|
| `seq-put` | 4,209,726 | 4,564,751 | 1,990,694 | 0.47x | disk≈mem，两者均为纯内存 COW |
| `seq-get` | 10,383,928 | 7,547,616 | 6,238,264 | 0.60x | |
| `par-put-4` | 4,682,044 | — | 4,747,568 | 1.01x | |
| `par-put-8` | 6,567,093 | 3,327,498 | 8,531,713 | 1.30x | |
| `par-put-16` | 6,519,533 | — | 5,334,792 | 0.82x | |
| `par-get-8` | 13,675,190 | 12,356,834 | 13,557,905 | 0.99x | |
| `mixed-8-r80` | 3,421,102 | — | 11,278,672 | 3.30x | |

> **对照要点**：
> - Lealone "disk" 模式 ≠ NexKV "persist" 模式。Lealone 采用 **Checkpoint 模型**（批量 save），NexKV 采用 **WAL-per-operation + AO 异步写入** 模型。两者不可直接对比落盘性能
> - Lealone disk mode `seq-put` 4.56M QPS 实际上代表了 **BTree 纯内存 COW 吞吐量**（`put()` 路径无任何磁盘 I/O），可作为 NexKV 纯内存 `seq-put` 2.0M 的跨实现对照 —— Lealone 快 2.1x，可能源于 Java JIT 优化 vs Go mmap COW 路径差异
> - Lealone 的单次 `save()` 0.349s/1M ops ≈ 批量序列化所有脏页 + 写一个 chunk + fsync，类似 NexKV every-second 模式中去掉 WAL fwrite 后的纯 AO 批量刷盘路径
> - NexKV `par-put-8` 领先 Lealone 30%，说明 Go goroutine + CAS 并发模型在多线程竞争写入下优于 Java 线程模型
> - NexKV `mixed-8-r80` 领先 Lealone 3.3x，COW 快照隔离在读多写少场景下有显著优势

#### 4.3 落盘模式预期性能

> 基于 §4.1 实测基线（`seq-put-mem` ≈ 2.0M QPS），推算各落盘场景预期。

| 场景 | 预期 QPS | 推导依据 |
|------|----------|---------|
| `seq-put-mem` | ~2,000,000 | ✅ 实测基线 |
| `seq-put-persist-every-write` | ~15,000–30,000 | 每条 fsync，MacBook NVMe fsync ≈ 0.03–0.07ms，理论上限 33K，加 WAL 序列化+fwrite syscall 开销 |
| `seq-put-persist-group-commit` | ~80,000–150,000 | 16 条/批 fsync → 62,500 次 fsync/1M ops，已大幅削减 fsync 开销，瓶颈转为 WAL 序列化（CRC32C+memcpy）+ fwrite syscall |
| `seq-put-persist-every-second` | ~200,000–400,000 | 无每条 fsync，但 WAL 序列化+fwrite syscall 仍在关键路径，预期为纯内存基线（2.0M）的 10–20% |
| `par-put-persist-8` | ~200,000–400,000 | 8 线程 + GroupCommit，WAL Mutex 竞争是主要瓶颈，实际需实测确定 |
| `par-put-mem-4` | ~4,747,568 | ✅ §4.1 实测基线（4 线程纯内存对照组） |

> `par-put-persist-4` 和 `par-put-persist-16` 预期与 `par-put-persist-8` 同量级（4 线程锁竞争更轻，16 线程可能因 CAS 竞争略有下降），具体以实测为准。

> **预期调整说明**：Pre 文档初版使用历史基线 ~3.4M QPS，本次实测为 ~2.0M（下降 41%），可能源于 Go 版本升级、macOS 内核变化或硬件差异。落盘预期相应下调，`group-commit` 从 100K-200K 调到 80K-150K，`every-second` 从 500K-1M 调到 200K-400K。**所有落盘预期均为推测值，以实际跑分结果为准。**

#### 4.4 落盘性能衰减链（理论模型）

```
纯内存 BTree COW           ~2,000,000 QPS    (基线)
  + WAL 序列化 + fwrite    ~200,000–400,000  (every-second，syscall 瓶颈)
  + 批量 fsync             ~80,000–150,000   (group-commit，IO 瓶颈)
  + 每条 fsync             ~15,000–30,000    (every-write，磁盘延迟瓶颈)
```

各 Sync 策略之间的 QPS 落差来自 fsync 调用频率，反映了「持久化保证」与「吞吐量」之间的 trade-off。

#### 4.5 分布式场景性能关系

> 本 benchmark 度量的是单节点存储引擎的写入上限。分布式部署中，若采用 Quorum 写入（N=3, W=2），实际集群吞吐量约为单节点的 1/W（即 ~1/2），因为需要等待至少 W 个副本确认。此外还需考虑网络 RTT（~0.1-1ms）和 Gossip 同步开销。单机 benchmark 旨在建立写入路径的性能基线，分布式场景的精确度量由后续 PR 覆盖。

#### 4.6 风险

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 落盘目录残留占用磁盘 | 低 | benchmark 结束后自动清理 WAL/AO 临时目录；使用 signal.NotifyContext 捕获 SIGINT/SIGTERM 确保 Ctrl+C 中断时也清理 |
| 落盘模式下 mmap 扩容 | 中 | 增大 mmap 初始大小（默认 -mmap=512MB 增大到 2048MB）或限制操作总数 |
| WAL + AO 双重写入放大 | 中 | 度量并记录写入放大比（写入放大比 = (WAL_Bytes + AO_Bytes) / UserData_Bytes） |

### 5. 评审要点

| 评审项 | 检查内容 | 评审人 | 评审结果 |
|--------|---------|--------|---------|
| 落盘数据流正确性 | Set → WAL → AO 路径是否完整，是否遗漏关键步骤 | 架构师 | ✅ 已修正（新增 §3.1.1 并发模型 + §3.1.2 WAL/AO 原子性简化假设） |
| Flag 设计合理性 | 命令行参数是否清晰，默认值是否合理 | 架构师 | ✅ 已修正（默认值改为 `group-commit`，生产环境常用） |
| 与现有 benchmark 的兼容性 | 不启用 `-persist` 时行为是否与当前完全一致 | 架构师 | ✅ 已修正（main.go 明确标注"修改 +30 行"，兼容性入口清晰） |
| 临时文件清理 | benchmark 结束后是否清理所有临时文件 | 架构师 | ✅ 已修正（§4.6 补充 signal.NotifyContext 机制） |
| 输出格式一致性 | 新增输出是否与现有输出风格一致 | 架构师 | ✅ 已修正（输出格式设计遵循现有 tabular 风格） |
| 并发模型安全性 | 多线程落盘写入的 WAL/AO 同步机制 | 架构师 | ✅ 已修正（§3.1.1：WAL Mutex / ChunkManager atomic / WritePage pwrite / UpdatePageLocs sync.Map） |
| 性能预期合理性 | 各场景 QPS 预期是否基于实际硬件能力 | 架构师 | ✅ 已修正（§4.1 实测基线 + §4.2 Lealone 对照 + §4.3 重新推导 + §4.4 衰减链模型） |
| WAL Entry 格式完整性 | 二进制格式定义是否无歧义 | 架构师 | ✅ 已修正（§3.1：`[KeyLen:2][Key:N][ValueLen:4][Value:M]`） |
| 测试策略 | 单元测试/集成测试覆盖范围 | 架构师 | ✅ 已修正（§3.5：5 个测试函数及职责明确） |
| 分布式性能推导 | 单机→分布式性能关系说明 | 架构师 | ✅ 已修正（§4.5：Quorum N=3,W=2 吞吐量 ~1/2） |

### 6. 评审记录

| 评审轮次 | 评审日期 | 评审人 | 核心意见 | 修改措施 | 完成状态 |
|----------|----------|--------|---------|---------|---------|
| 第1轮 | 2026-06-07 | AI 专家团队（存储引擎 + Go + 分布式 KV） | 综合评分 6.5/10，3 个高风险 + 5 个中风险 + 3 个低风险。详见 §6.1 评审详情 | 见下方修改措施 | ✅ 修改完成 |
| 第2轮 | 2026-06-07 | AI 专家团队 | 综合评分 8.1/10，2 个中风险 + 1 个低风险，均已修正。✅ 通过 | ① §5 评审状态更新 ② WAL Entry 大小修正 ③ §4.3 场景补充 | ✅ 修改完成 |
| 第3轮 | 2026-06-07 | AI 专家团队 | 综合评分 8.3/10，1 个中风险 + 2 个低风险，均已修正。✅ 最终通过 | ① §2.2 目标与 §3.1.2 矛盾修正 ② §4.4 衰减链改为范围 ③ §3.4 sync_count 示例修正 | ✅ 修改完成 |

#### 6.1 第 1 轮评审详情

**评审概况**：

| 维度 | 评分 | 说明 |
|------|------|------|
| 存储引擎设计合理性 | 7/10 | 数据流基本完整，WAL Entry 格式已修正 |
| Go 代码结构合理性 | 6/10 | "main.go 不改动"矛盾已修正 |
| 并发安全性设计 | 6/10 | 并发模型已补充 |
| 性能预期合理性 | 7/10 | EverySecond 预期已调校 |
| 工程落地性 | 7/10 | 测试策略已细化 |
| 分布式场景前瞻 | 6/10 | 已补充单机→分布式推导 |
| **综合评分** | **6.5/10** | **有条件通过** |

**修改措施**（所有 P0/P1 问题已在文档中修正）：

| 严重程度 | 问题 | 修正内容 |
|----------|------|---------|
| 🔴 高风险 | main.go "不改动"矛盾 | §3.5：修正为"修改：+5 flag + 场景入口（约 +30 行）" |
| 🔴 高风险 | 并发模型未定义 | 新增 §3.1.1：WAL Mutex / ChunkManager atomic / UpdatePageLocs sync.Map |
| 🔴 高风险 | WAL/AO 原子性缺失 | 新增 §3.1.2：benchmark 简化假设（WAL 确认即计数，AO 异步写入） |
| 🟡 中风险 | EverySecond 预期偏乐观 | §4.1：调整为 200K-500K，标注「乐观预期，需实测验证」 |
| 🟡 中风险 | $TMPDIR 跨平台兼容性 | §3.2：改为 `os.TempDir()` |
| 🟡 中风险 | main_test.go 测试范围模糊 | §3.5：细化 5 个测试函数及职责 |
| 🟡 中风险 | WAL Entry "..." 模糊 | §3.1：改为 `[KeyLen:2][Key:N][ValueLen:4][Value:M]` |
| 🟡 中风险 | 单机→分布式推导缺失 | §4.1：新增分布式性能关系说明 |
| 🟢 低风险 | Flag 默认值 every-write | §3.2：默认值改为 `group-commit`（生产环境常用） |
| 🟢 低风险 | signal 处理缺失 | §4.2：补充 signal.NotifyContext 清理机制 |
| 🟢 低风险 | GroupCommit 参数说明 | §3.1 保留 16条/1ms 为初始默认值 |
| 📊 数据补充 | seq-put-mem 基线过时（3.4M→2.0M） | §4.1：更新为 2026-06-07 实测基线（1,990,694 QPS） |
| 📊 数据补充 | 缺少外部对照基准 | §4.2：新增 Lealone BTreeMap Java 对照表 |
| 📊 数据补充 | 落盘预期需基于新基线重算 | §4.3：基于 2.0M 基线重新推导 + §4.4 新增衰减链模型 |

---

## 第二部分：流程节点记录

### 1. 开发过程记录

| 节点 | 完成日期 | 具体内容 | 交付物 |
|------|----------|----------|--------|
| Pre文档编写 | 2026-06-07 | 编写前置规划文档 | 本文件（第一部分） |
| 架构师Pre批准 | 2026-06-07 | 架构师评审Pre文档 | ✅ 最终通过（三轮评审：6.5→8.1→8.3） |
| 代码实现 | （待定） | 实现 persist.go + wal_bench.go | 代码 |
| 代码评审 | （待定） | code-reviewer 评审 | 评审意见 |
| Post文档编写 | （待定） | 编写后置总结文档 | 本文件（第三部分） |
| 架构师Post批准 | （待定） | 架构师评审Post文档 | 批准签字 |
| 提交GitHub | （待定） | 创建PR | PR链接 |

### 2. CI流程记录

| CI轮次 | 触发时间 | 结果 | 问题详情 | 修复措施 | 修复结果 |
|--------|----------|------|----------|----------|----------|
| 第1轮 | （待定） | - | - | - | - |

### 3. 合并记录

| 合并时间 | 合并方式 | 审批人 | 备注 |
|----------|----------|--------|------|
| （待定） | Squash Merge | 架构师 | - |

---

## 第三部分：后置部分（PR合并后编写）

### 1. 开发成果总结

#### 1.1 完成情况
- **新增文件**：（待实现后填写）
- **修改文件**：（待实现后填写）
- **变更统计**：（待实现后填写）
- **与Pre文档差异**：（待实现后填写）

#### 1.2 质量验证
- **单元测试**：（待填写）
- **Benchmark 结果**：（待填写）
- **代码覆盖率**：（待填写）

#### 1.3 实际 Bench 结果

（实现后填写实际 QPS 数据）

| 场景 | QPS | WAL Bytes | AO Bytes | 写入放大比 |
|------|-----|-----------|----------|-----------|
| `seq-put-mem` | - | - | - | - |
| `seq-put-persist-every-write` | - | - | - | - |
| `seq-put-persist-group-commit` | - | - | - | - |
| `seq-put-persist-every-second` | - | - | - | - |
| `par-put-persist-8` | - | - | - | - |

#### 1.4 交付物清单

| 类型 | 具体内容 | 链接/路径 |
|------|----------|-----------|
| 新增文件 | persist.go | `cmd/tools/btree_bench/persist.go` |
| 新增文件 | wal_bench.go | `cmd/tools/btree_bench/wal_bench.go` |
| 新增文件 | main_test.go | `cmd/tools/btree_bench/main_test.go` |
| 修改文件 | main.go | `cmd/tools/btree_bench/main.go` |

### 2. 后续优化建议

#### 2.1 未完成项
- **Batch 落盘**：暂不实现 BatchSet/BatchGet 的落盘路径
- **多节点复制**：落盘 + Gossip 复制的联动 benchmark
- **恢复验证**：落盘数据重启后的正确性自动验证

#### 2.2 ToDo清单

| 优先级 | 任务内容 | 预估时间 | 关联PR | 备注 |
|--------|----------|---------|--------|------|
| 中 | BatchSet 落盘 benchmark | 4h | - | Phase 2 |
| 低 | WAL 恢复正确性自动化测试 | 8h | - | 集成测试 |
| 低 | 落盘 + 压缩联动 benchmark | 6h | - | 压缩对落盘的影响 |

### 3. 维护建议

1. **定期运行**：每次存储引擎变更后，运行落盘 benchmark 对比性能回归
2. **多平台数据**：收集 Linux/macOS、SSD/HDD 环境下的基准数据，建立性能基线
3. **CI 集成**：将 `-persist -n 100000` 的快速 smoke test 集成到 CI 中

---

## 文档归档信息

| 项目 | 内容 |
|------|------|
| 文档最终版本 | V1.0 |
| 归档日期 | 2026-06-07 |
| 归档路径 | `docs/06_PM/feature/2026-06-07_PR-btree-bench-persistence_Pre.md` |
| 后续维护人 | jzhang405 |
