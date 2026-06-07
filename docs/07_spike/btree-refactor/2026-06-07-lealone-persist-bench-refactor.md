# Spike：btree_bench 落盘模式重构 — 从 WAL-per-op 到 Lealone Checkpoint 模型

> **文档类型**：预研究 / 技术探索  
> **日期**：2026-06-07  
> **作者**：jzhang405  
> **关联 PR**：`docs/btree-bench-persistence-benchmark`  
> **关联分支**：`spike/btree-bench-lealone-persist`  
> **关键词**：Checkpoint, WAL, BTree, 持久化, benchmark, Lealone, 写放大, 性能模型

---

## 目录

1. [背景与动机](#背景与动机)
2. [两种持久化模型回顾](#两种持久化模型回顾)
3. [当前 NexKV persist benchmark 设计](#当前-nexkv-persist-benchmark-设计)
4. [Lealone Checkpoint 模型精髓](#lealone-checkpoint-模型精髓)
5. [重构方案](#重构方案)
6. [两种方案预期性能对比](#两种方案预期性能对比)
7. [实现计划](#实现计划)
8. [风险与 trade-off](#风险与-trade-off)
9. [决策记录](#决策记录)

---

## 背景与动机

### 当前状态

我们在 `docs/btree-bench-persistence-benchmark` 分支上设计了 btree_bench 的落盘模式，采用 **WAL-per-operation** 模型：

```
Set() → BTree.Set()(COW) → WAL.Append(fwrite) → fsync(策略控制) → AO.WritePage(异步)
```

这个设计是合理的——它是 NexKV 生产路径的精确复现。

### 问题

我们跑了 Lealone 作为外部对照基准，发现：

| | seq-put QPS | 持久化模型 |
|---|---|---|
| Lealone "disk" mode | **4.5M** | put() 纯内存，save() 批量 checkpoint |
| Lealone persist batch=1 | **207** | save() 每条 ~5ms |
| NexKV every-write 预期 | **15K-30K** | WAL fwrite+fsync 每条 ~0.03ms |
| NexKV every-second 预期 | **200K-400K** | WAL fwrite only, 无每条 fsync |

**关键发现**：Lealone 的 `save()` 每次至少 ~5ms（写完整 Chunk Header + fsync），不适合小批量。但大 batch（≥100K）下，有效 QPS 可达到 **1.7M**，接近纯内存性能。

> **这个 Spike 要回答的问题**：NexKV 的 btree_bench 是否应该同时实现两种持久化模式——WAL-per-op（控制 fsync 频率）和 Checkpoint（控制 save 频率）——让 benchmark 能度量两种模型下的真实性能？

---

## 两种持久化模型回顾

```
┌─────────────────────────────────────────────────────────────────┐
│                    两种持久化模型的实现对比                        │
│                                                                  │
│  WAL-per-op 模型 (当前 NexKV 设计)                               │
│  ────────────────────────────────                                │
│  for i := 0..N:                                                  │
│      tree.Set(k, v)              // COW                          │
│      wal.Append(entry)           // fwrite (每条约 53B)          │
│      if i % syncInterval == 0:                                   │
│          wal.Sync()              // fsync                        │
│          ops.Add(syncInterval)                                    │
│      chunkMgr.WritePage(...)     // AO 异步                       │
│                                                                  │
│  Checkpoint 模型 (Lealone, 本次新增)                              │
│  ──────────────────────────────────                              │
│  for i := 0..N:                                                  │
│      tree.Set(k, v)              // COW (纯内存, 无 IO)          │
│      ops.Add(1)                                                  │
│      if ops % checkpointInterval == 0:                           │
│          storage.Save()          // 序列化脏页 → 写 chunk → fsync │
│                                                                  │
│  本质区别:                                                        │
│  - WAL: 每次写入都产生一条日志, sync 频率控制持久化保证级别         │
│  - Checkpoint: 写入是纯内存操作, 持久化是周期性全量快照             │
└─────────────────────────────────────────────────────────────────┘
```

---

## 当前 NexKV persist benchmark 设计

### 架构

```
persistSetLoop() {
    for i := 0..N {
        tree.Set(key, value)             // ① BTree 纯内存 COW
        entry := marshalWALEntry(...)    // ② WAL Entry 序列化
        wal.Append(entry)                // ③ WAL fwrite
        if syncPolicy.ShouldSync(i) {
            wal.Sync()                   // ④ 策略化 fsync
            ops.Add(syncInterval)        //    计数 (WAL 确认后)
        }
        chunkMgr.WritePage(...)          // ⑤ AO 异步写入 (不阻塞计数)
    }
}
```

### 三种 Sync 策略

| 策略 | fsync 频率 | 预期 QPS | 适用场景 |
|------|-----------|----------|---------|
| `every-write` | 每条 | 15K-30K | 最强持久化保证 |
| `group-commit` | 每 16 条 | 80K-150K | 批量优化 |
| `every-second` | 每秒 | 200K-400K | 最大吞吐 |

### flag 设计

```
-persist      bool   启用落盘
-wal-sync     string WAL sync 策略: every-write/group-commit/every-second
-wal-dir      string WAL 文件目录
-ao-dir       string AO chunk 文件目录
-ao-chunk-size int   AO chunk 大小 (MB)
```

---

## Lealone Checkpoint 模型精髓

### 核心思想

> **put() 是纯内存操作，save() 是全量快照。持久化的成本不在每条写入，而在每次 checkpoint。**

### 源码分析（已确认）

```
BTreeMap.put() → Put.writeLocal() → COW + CAS
  全程无 fwrite / fsync / RedoLog
  4.5M QPS = 纯内存 COW 吞吐量

BTreeStorage.save():
  ① chunkCompactor.executeCompact()           // GC
  ② 序列化所有脏页 → DataBuffer (DirectByteBuffer)
  ③ 写 Chunk Header (8KB)
  ④ 写 Chunk Body (脏页数据) → FileChannel.write
  ⑤ FileStorage.sync() → FileChannel.force(true) = fsync
  ⑥ 原子切换 Chunk 引用
  → 最小开销 ~5ms (创建 Chunk 文件 + Header + fsync)
```

### 有效 QPS 公式

```
effQPS = batchSize / (batchSize / putRate + saveTime)

  当 batchSize 很小时: effQPS ≈ batchSize / saveTime
    例: batchSize=1   → effQPS ≈ 1/0.005s = 205 ✓
  当 batchSize 很大时: effQPS ≈ putRate
    例: batchSize=1M  → effQPS ≈ 1M/0.54s = 1.85M ✓
```

### 实测数据（同机）

| Batch | save 耗时 | 有效 QPS |
|:-----:|----------:|---------:|
| 1 | 4.84ms | **207** |
| 16 | 4.86ms | **3,292** |
| 100 | 5.03ms | **19,733** |
| 1K | 5.26ms | **180,036** |
| 10K | 7.79ms | **1,050,316** |
| 100K | 41.0ms | **1,697,443** |
| 1M | 337ms | **1,794,974** |

---

## 重构方案

### 总体思路

**新增独立的 Checkpoint 模式**，与现有 WAL 模式并行。用户通过 flag 选择模式。

```
-persist-mode   string   持久化模式: wal / checkpoint (默认 wal)
-persist        bool     启用落盘 (两种模式共用)
```

### 新增 Flag

| Flag | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-persist` | bool | false | 启用落盘模式 |
| `-persist-mode` | string | `wal` | 持久化模式：`wal` / `checkpoint` |
| `-wal-sync` | string | `group-commit` | [WAL 模式] Sync 策略 |
| `-ckpt-interval` | int | `1000` | [Checkpoint 模式] 多少条 put 后触发一次 save() |
| `-wal-dir` | string | `os.TempDir()...` | [共用] WAL 文件目录 |
| `-ao-dir` | string | `os.TempDir()...` | [共用] AO chunk 文件目录 |
| `-ao-chunk-size` | int | 256 | [共用] AO chunk 文件大小 (MB) |

### 代码结构

```
cmd/tools/btree_bench/
├── main.go              # 修改：+persist-mode flag + checkpoint 场景入口
├── persist_wal.go       # 新增：WAL-per-op 模式 (原 persist.go 的设计)
│   ├── newWALPersistStorage()  # 创建 WAL + BTree
│   ├── walPersistSetLoop()     # WAL per-op 循环
│   └── printWALStats()         # WAL 统计
├── persist_checkpoint.go # 新增：Checkpoint 模式 (仿 Lealone)
│   ├── newCkptPersistStorage() # 创建 BTree + ChunkManager (无 WAL!)
│   ├── ckptPersistSetLoop()    # Checkpoint 循环 (纯内存 put + 周期 save)
│   └── printCkptStats()        # Checkpoint 统计
└── main_test.go         # 修改：新增 checkpoint 模式测试
```

### Checkpoint 模式伪代码

```go
// persist_checkpoint.go

func ckptPersistSetLoop(tree *btree.BTree, cm service.ChunkManager,
    n int, ckptInterval int, ops *atomic.Int64) {
    
    for i := 0; i < n; i++ {
        tree.Set(ctx, keyOf(i), valOf(i))  // ① 纯内存 COW (无磁盘 I/O)
        ops.Add(1)                           // ② 立即计数
        
        if (i+1) % ckptInterval == 0 {
            // ③ 周期 checkpoint: 序列化所有脏页 → 写 chunk → fsync
            tree.EnumeratePages(...)          // 遍历脏页
            // → ChunkManager.Allocate + WritePage + Sync
        }
    }
}
```

### Benchmark 场景

**WAL 模式（原有）**：

| 场景 | 模式 | Sync 策略 | 说明 |
|------|------|----------|------|
| `seq-put-wal-every-write` | WAL | every-write | 每条 fsync |
| `seq-put-wal-group-commit` | WAL | group-commit | 16条/批 fsync |
| `seq-put-wal-every-second` | WAL | every-second | 每秒 fsync |
| `par-put-wal-8` | WAL | group-commit | 8线程 |

**Checkpoint 模式（新增）**：

| 场景 | 模式 | ckptInterval | 说明 |
|------|------|:-----------:|------|
| `seq-put-ckpt-100` | Checkpoint | 100 | 每 100 条 save |
| `seq-put-ckpt-1000` | Checkpoint | 1K | 每 1K 条 save |
| `seq-put-ckpt-10000` | Checkpoint | 10K | 每 10K 条 save |
| `seq-put-ckpt-100000` | Checkpoint | 100K | 每 100K 条 save |
| `seq-put-ckpt-end` | Checkpoint | N (末尾一次) | 仅最后 save (类似 Lealone seq-put+save) |
| `par-put-ckpt-8-10000` | Checkpoint | 10K | 8线程 + 每 10K save |

**纯内存对照组**：

| 场景 | 说明 |
|------|------|
| `seq-put-mem` | 纯内存，无任何持久化 |
| `par-put-mem-4` | 4线程纯内存 |

---

## 两种方案预期性能对比

### 同机实测（MacBook Pro M4 Pro）

| | WAL-per-op (预期) | Checkpoint (Lealone实测) | Checkpoint (NexKV预期) |
|---|---|---|---|
| 每条持久化 | 15K-30K | 207 | ~200-500 |
| 每 16 条持久化 | 80K-150K | 3,292 | ~3K-10K |
| 每 1K 条持久化 | — | 180K | ~150K-300K |
| 每 10K 条持久化 | — | 1.05M | ~0.8M-1.5M |
| 每 100K 条持久化 | — | 1.70M | ~1.5M-2.0M |
| 末尾一次持久化 | — | 1.79M | ~1.8M-2.0M |
| 纯内存 | 1.99M (实测) | 4.5M (Lealone) | 1.99M |

### 关键观察

```
WAL-per-op 的优势:
  ✅ 精细控制每条写入的持久化保证级别
  ✅ 小批量高频 fsync 的场景 (every-write: 15K-30K) 优于 Checkpoint (batch=1: 205)
  ✅ 与 NexKV 生产路径一致

Checkpoint 的优势:
  ✅ 大 batch (≥10K) 下有效 QPS 接近纯内存 (1.0M-1.8M)
  ✅ 无 WAL 写入放大 — 数据只写一遍 (AO chunk 直接写入)
  ✅ 恢复简单 — 加载最近完整 chunk, 不需要回放 WAL
  ✅ 跨语言验证 — Lealone 实测数据可直接参考

两者互补:
  ┌──────────────────────────────────────────────────────────┐
  │  高频小批量写入                  低频大批量写入            │
  │  ────────────                   ────────────            │
  │  WAL-per-op 更适合              Checkpoint 更适合         │
  │  fsync 开销 ~0.03ms             fsync 开销 ~5ms          │
  │  但每条都有 WAL 开销             但摊销后接近内存性能       │
  │                                                          │
  │  在线 OLTP                      批量导入/ETL             │
  └──────────────────────────────────────────────────────────┘
```

---

## 实现计划

### Phase 1：Checkpoint 模式核心（`persist_checkpoint.go`）

| 任务 | 预估 | 内容 |
|------|:----:|------|
| 1.1 | 2h | `newCkptPersistStorage()` — 创建 BTree + ChunkManager (不需要 WAL) |
| 1.2 | 3h | `ckptPersistSetLoop()` — 纯内存 put + 周期 EnumeratePages + WritePage + Sync |
| 1.3 | 1h | `printCkptStats()` — 输出 checkpoint 统计 (save 次数, chunk 数, fsync 耗时) |
| 1.4 | 2h | main.go 集成 — `-persist-mode checkpoint` flag + 场景入口 |
| 1.5 | 2h | 单元测试 — TestCkptPersistSetLoop, TestCkptTempDirCleanup 等 |

### Phase 2：WAL 模式独立（`persist_wal.go`）

| 任务 | 预估 | 内容 |
|------|:----:|------|
| 2.1 | 2h | 将 WAL 模式从 persist.go 拆分到 persist_wal.go |
| 2.2 | 1h | 统一接口 — 两种模式共用 flag 解析和输出格式 |

### Phase 3：性能对比 + 文档

| 任务 | 预估 | 内容 |
|------|:----:|------|
| 3.1 | 1h | 跑 WAL 模式和 Checkpoint 模式全量 benchmark |
| 3.2 | 1h | 对照分析 — 生成对比表格 |
| 3.3 | 2h | 更新 Pre/Post 文档 — 补充 Checkpoint 模式章节 |

**总计**：约 16h

---

## 风险与 trade-off

| 风险 | 影响 | 缓解 |
|------|------|------|
| Checkpoint 模式下脏页积累导致内存压力 | 中 | 限制 `-ckpt-interval` 最大值；大 batch 时增大 `-mmap` |
| save() 期间阻塞所有写入 | 高 | Save() 期间 map 加锁 — 与 Lealone `synchronized save()` 一致。多线程下需在文档中明确 |
| 两种模式共存的复杂度 | 低 | 通过 flag 和独立文件清晰隔离 |
| 与现有 persist.go 设计冲突 | 低 | Pre 文档已预留两种方案对比 (§3.1.2)，Checkpoint 是新增模式而非替换 |

---

## 决策记录

### 决策 1：Checkpoint 模式作为独立模式，不替换 WAL 模式

**理由**：
- WAL-per-op 是 NexKV 的生产路径，benchmark 需要度量生产路径的性能
- Checkpoint 模式为对照和批量场景提供高性能替代方案
- 两者互补而非互斥

### 决策 2：Checkpoint 模式复用现有 ChunkManager，不引入新组件

**理由**：
- `EnumeratePages()` + `ChunkManager.Allocate/WritePage/Sync` 已经实现了 checkpoint 的核心路径
- 与 Lealone 的 `save()` → Chunk.write() → FileStorage.sync() 语义等价
- 不需要 WAL Entry 序列化和 fwrite

### 决策 3：Checkpoint 模式不实现事务语义

**理由**：
- benchmark 是单线程/多线程纯写入，不涉及事务
- 事务 RedoLog（AOTE RedoLog）是 Lealone 的事务层机制，benchmark 不需要
- 简化实现，聚焦 checkpoint 吞吐量度量

---

## 附录

### 关联文档

- [[NexKV vs Lealone 持久化设计深度对比]] — 三大机制源码分析
- [[PR-btree-bench-persistence-Pre]] — 当前 persist benchmark Pre 文档
- [[2026-05-16-phase4-wal-ao-persistence-spike]] — WAL+AO 集成 Spike
- [[2026-05-23-persistence-architecture-comprehensive-guide]] — 持久化架构全景

### 关联源码

| 项目 | 关键文件 |
|------|---------|
| Lealone | `lealone-aose/.../btree/BTreeStorage.java:294-401` — `save()` |
| Lealone | `lealone-aose/.../btree/chunk/Chunk.java:308-317` — `write()` |
| Lealone | `lealone-aote/.../log/LogSyncService.java` — RedoLog sync |
| NexKV | `internal/domain/service/chunk_manager.go` — ChunkManager 接口 |
| NexKV | `internal/infrastructure/storage/btree/btree.go:109-112` — SetChunkManager |
| NexKV | `internal/infrastructure/storage/btree/btree.go:120-125` — EnumeratePages |
| NexKV | `internal/infrastructure/storage/chunk/disk_chunk_manager.go` — DiskChunkManager |

---

> **文档版本**：v1.0  
> **创建日期**：2026-06-07  
> **Spike 状态**：待评审  
> **下一步**：架构师评审通过后启动 Phase 1 实现
