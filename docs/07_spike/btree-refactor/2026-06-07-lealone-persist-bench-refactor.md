# Spike：btree_bench 落盘模式重构 — 全面迁移到 Lealone 三大持久化机制

> **文档类型**：预研究 / 技术探索  
> **日期**：2026-06-07  
> **作者**：jzhang405  
> **关联 PR**：`docs/btree-bench-persistence-benchmark`  
> **关联分支**：`spike/btree-bench-lealone-persist`  
> **关键词**：Checkpoint, WAL, BTree, 持久化, benchmark, Lealone, UndoLog, RedoLog, Chunk

---

## 目录

1. [背景与动机](#背景与动机)
2. [Lealone 三大持久化机制全景](#lealone-三大持久化机制全景)
3. [三大机制逐一剖析](#三大机制逐一剖析)
4. [当前 NexKV 架构对比](#当前-nexkv-架构对比)
5. [重构方案：全面迁移到 BTree config](#重构方案全面迁移到-btree-config)
6. [两种模式预期性能对比](#两种模式预期性能对比)
7. [实现计划](#实现计划)
8. [风险与 trade-off](#风险与-trade-off)
9. [决策记录](#决策记录)
10. [附录](#附录)

---

## 背景与动机

### 当前状态

在 `docs/btree-bench-persistence-benchmark` 分支上设计了 btree_bench 的落盘模式，采用 **benchmark 层松耦合接线**：

```
persistSetLoop() {
    tree.Set()        // ① BTree 纯内存 (不改动)
    wal.Append()      // ② WAL fwrite
    wal.Sync()        // ③ 策略化 fsync
    chunkMgr.Write()  // ④ AO 异步
}
```

> 持久化逻辑在 benchmark 层，BTree 本身不感知。

### 问题

1. **benchmark 与生产路径不一致**：生产环境 Set() 必须自动触发持久化，不能依赖调用方手动串 WAL
2. **对照 Lealone 发现**：Lealone 用三个独立机制覆盖全场景——持久化不是 benchmark 层的事，是存储引擎内部的事
3. **两个持久化模型**（WAL-per-op vs Checkpoint）有完全不同的适用场景，应该都作为 BTree 的一等公民

### 目标

**将 WAL 和 Checkpoint 两种持久化模式内置到 `internal/infrastructure/storage/btree` 的 config 中**，使 BTree.Set() 根据配置自动选择持久化路径——与 Lealone 三大机制的架构一致。

---

## Lealone 三大持久化机制全景

> 详见 `[[NexKV vs Lealone 持久化设计深度对比]]` §3 完整源码分析。此处仅保留架构层面的关键要点，作为 NexKV 重构的参考蓝图。

### 全景调用图

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                Lealone 三大持久化机制的调用全景                                  │
│                                                                               │
│   【事务路径】                           【非事务路径】                           │
│                                                                               │
│   BeginTxn                               put("key", "value")                  │
│       │                                      │                                │
│       ▼                                      ▼                                │
│   put("k", newValue):                        BTreeMap.put()                   │
│    1. 读 oldValue                            ├─ COW 新页面                    │
│    2. undoLog.add(key, OLD)  ← ① 记录旧值    ├─ CAS 替换                      │
│    3. map.put(key, newValue)                 └─ return ← 纯内存                │
│                                                                               │
│   Commit():                                                                   │
│    1. undoLog.writeForRedo()                                                 │
│       → 记录 NEW value 到 RedoLogBuffer ← ② 准备 Redo                        │
│    2. LogSyncService.syncWrite()                                             │
│       → Chunk.writeRedoLog(fwrite) ← ③ 写 .redo 文件                          │
│       → FileStorage.sync() ← ✅ fsync                                        │
│    3. undoLog.commit() ← ① 标记已提交                                        │
│                                                                               │
│   Rollback():                                                                 │
│    undoLog.rollbackTo(0) ← ① 用 OLD 值恢复                                    │
│                                                                               │
│  ════════════════════════════════════════════                                  │
│                                                                               │
│  显式 save() Checkpoint:                                                      │
│    BTreeStorage.save()                                                        │
│      → 序列化所有脏页                                                           │
│      → Chunk.write() → fwrite + fsync                                         │
│      → 截断 RedoLog                                                           │
│                                                                               │
│  ┌── 三大机制速览 ───────────────────────────────────────────────┐           │
│  │                                                               │           │
│  │  ① UndoLog           ② AOTE RedoLog         ③ AOSE Chunk RedoLog         │
│  │  ─────────            ─────────────        ─────────────────              │
│  │  记录: oldValue       记录: newValue        记录: page 级变更              │
│  │  位置: 纯内存          位置: .redo 文件      位置: .ao chunk 文件           │
│  │  fsync: ❌            fsync: ✅             fsync: ❌                     │
│  │  触发: put()          触发: Commit()        触发: save()/compact           │
│  │  用途: 事务回滚        用途: 崩溃恢复         用途: page 级恢复              │
│  │                                                               │           │
│  └───────────────────────────────────────────────────────────────┘           │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## 三大机制逐一剖析

### 机制一：UndoLog — 事务回滚日志（纯内存 ❌ 无磁盘 I/O）

```
比喻：UndoLog = 草稿纸上的"改前记录"

数据结构：双向链表
  first → [k1, old=v1_old] ↔ [k2, old=v2_old] ← last

生命周期：
  BeginTxn → put() → undoLog.add(oldValue) → ...
  Commit   → undoLog.commit() (标记) → undoLog = null
  Rollback → undoLog.rollbackTo(0) (恢复 oldValue)

为什么不需要 fsync？
  场景 A: Commit 成功 → UndoLog 丢弃（已提交，不需要回滚）
  场景 B: 进程崩溃 → 所有内存丢失 + Checkpoint 不含未提交事务 → UndoLog 不需要
  场景 C: 主动 Rollback → 进程存活，UndoLog 在内存可用

NexKV 对等物：MVCC 版本链 —— 已存历史版本，天然就是 UndoLog
  差异：Lealone 单独链表，NexKV 内嵌在页面中。功能等价。
```

### 机制二：AOTE 事务 RedoLog — 崩溃恢复日志（✅ fwrite + fsync）

```
比喻：AOTE RedoLog = 公证处的"交易登记簿"

调用链 8 步：
  ① Commit() → ② writeRedoLog() → ③ undoLog.writeForRedo() (遍历链表，写 NEW value)
  → ④ LogSyncService.syncWrite() (唤醒后台线程)
  → ⑤ RedoLog.save() → ⑥ RedoLogBuffer.writeRedoLog() → fwrite
  → ⑦ RedoLogBuffer.sync() → FileStorage.sync() ← ✅ fsync!
  → ⑧ t.onSynced() → 分配 commitTimestamp → 返回客户端

三种 Sync 策略：
  Periodic (默认): 后台线程定期 fsync (loop 间隔 3s)，事务不等 fsync 就返回
  Instant:        每次 Commit 阻塞等待 fsync 完成
  NoSync:         完全不 fsync，依赖 save() Checkpoint

RedoLog 文件: base_dir/redo_log/redoLog_N.redo
  Entry 格式: [len:4][type:1][metaVersion][key:N][newValue:M]  (INSERT)
               [len:4][type:0][key:N]                           (DELETE)
  Buffer:    2MB DirectByteBuffer (RedoLogBuffer)

NexKV 对等物：WAL Entry 完整记录 key+value
  差异：Lealone 事务 RedoLog 由 LogSyncService 后台线程管理，有 Periodic/Instant/NoSync
        NexKV WAL 由 benchmark 显式控制 Sync 频率 (EveryWrite/GroupCommit/EverySecond)
```

### 机制三：AOSE Chunk RedoLog — Page 级操作日志（❌ 仅 fwrite，无 fsync）

```
比喻：AOSE Chunk RedoLog = 工地施工日志

记录内容：page 分裂、合并等结构变更
触发时机：save() 过程中
sync:      ❌ 无独立 fsync —— 注释明确写 "这个方法未调用sync，上层调用者需要额外按需调用sync"
依赖:      上层 save() 的 FileStorage.sync() 统一刷盘

与 AOTE RedoLog 的调用方差异：
  同一个底层 Chunk.writeRedoLog():
    AOTE 调用 → fwrite 后立刻 RedoLogBuffer.sync() → fsync ✅
    AOSE 调用 → fwrite 后不 sync，等 save() 的整体 fsync ❌

NexKV 对等物：无 —— NexKV 不区分 page 级和事务级日志
  所有变更统一走 WAL Entry，没有单独的 page 操作日志
```

---

## 当前 NexKV 架构对比

### 现有代码结构

```
BTree struct {                              ← btree.go:30-47
    rootRef        *RootPageRef
    storage        *OffheapBTreeStorage
    // ❌ 没有 WAL 字段
    // ❌ 没有 ChunkManager 字段
    // ✅ 仅有 SetChunkManager 注入点 (Checkpoint 用)
}

BTree.Set() {
    COW + MVCC + CAS
    return   ← 纯内存，无 fwrite/fsync
}

WAL (service.WAL) → 仅在 MVCC Tx.Commit() 中使用
ChunkManager (service.ChunkManager) → 仅在 BTree.EnumeratePages() (Checkpoint) 中使用
```

### 对 Lealone 的映射

| Lealone | NexKV 当前 | 差距 |
|---------|-----------|------|
| ① UndoLog (纯内存) | MVCC 版本链 | ✅ 已有等价物 |
| ② AOTE RedoLog (有 fsync) | WAL 模块 | ⚠️ WAL 存在但未接入 BTree.Set() |
| ③ AOSE Chunk RedoLog (无 fsync) | 无 | NexKV 不区分 page 级和事务级日志 |
| Save Checkpoint | EnumeratePages + ChunkManager | ⚠️ 存在但未接入 Set() 路径 |
| 日志生命周期管理 | 无 | 缺少 LogSyncService 等价物 |

---

## 重构方案：全面迁移到 BTree config

### 核心决策：持久化是 BTree 内部的事

```
┌──────────────────────────────────────────────────────────────┐
│  之前（方案 B 松耦合）：                                       │
│                                                               │
│  benchmark 层: persistSetLoop() {                             │
│      tree.Set() + wal.Append() + wal.Sync() + chunkMgr.Write()│
│  }                                                            │
│  问题: benchmark 和生产路径不一致; 每个调用方都要自己接线       │
│                                                               │
│  ─────────────────────────────────────────────────────────── │
│                                                               │
│  现在（方案 C：内部化）：                                       │
│                                                               │
│  BTree.Set() {                                                │
│      COW + CAS                              // 现有逻辑       │
│      if cfg.persistMode == WAL {                              │
│          wal.Append(entry)                   // ② 等价物      │
│          if syncPolicy.ShouldSync() { wal.Sync() }             │
│      }                                                       │
│  }                                                           │
│                                                               │
│  benchmark 层: tree.Set() ← 一行，自动持久化                   │
│  生产环境:    tree.Set() ← 同一行，同一路径                    │
│                                                               │
│  BTree.Save() { // Checkpoint 模式或 WAL 截断                  │
│      EnumeratePages() → chunkMgr.Write → chunkMgr.Sync()      │
│      wal.Truncate(lastSyncedLSN)          // 截断 WAL         │
│  }                                                           │
└──────────────────────────────────────────────────────────────┘
```

### BTree config 扩展

```go
// options.go — 新增

// PersistMode 定义持久化模式
type PersistMode int

const (
    PersistModeNone       PersistMode = iota // 纯内存 (默认, 向后兼容)
    PersistModeWAL                           // WAL-per-operation
    PersistModeCheckpoint                    // 周期 Checkpoint (仿 Lealone)
)

// WalSyncPolicy 定义 WAL fsync 策略
type WalSyncPolicy int

const (
    WalSyncEveryWrite  WalSyncPolicy = iota // 每条 fsync
    WalSyncGroupCommit                      // 批量 fsync (默认每 16 条)
    WalSyncEverySecond                      // 每秒 fsync
)

// btreeConfig 扩展
type btreeConfig struct {
    // ... 现有字段 ...
    metrics        *BTreeMetrics
    tracer         Tracer
    tsGen          mvcc.TSGenerator
    txMgr          mvcc.TxManager
    latencyMetrics *BTreeMetricsWithLatency
    enableEpoch    bool

    // 新增: 持久化配置
    persistMode     PersistMode     // 持久化模式 (默认 PersistModeNone)
    walSync         WalSyncPolicy   // WAL sync 策略 (仅 PersistModeWAL 时生效)
    ckptInterval    int             // Checkpoint 间隔 (仅 PersistModeCheckpoint 时生效)
    wal             service.WAL     // WAL 实例 (PersistModeWAL 时必填)
    chunkMgr        service.ChunkManager  // ChunkManager 实例 (PersistModeCheckpoint 时必填)
    serializer      *chunk.PageSerializer
}

// 新增 Option 函数
func WithPersistWAL(wal service.WAL, sync WalSyncPolicy) BTreeOption {
    return func(cfg *btreeConfig) {
        cfg.persistMode = PersistModeWAL
        cfg.wal = wal
        cfg.walSync = sync
    }
}

func WithPersistCheckpoint(cm service.ChunkManager, serializer *chunk.PageSerializer, interval int) BTreeOption {
    return func(cfg *btreeConfig) {
        cfg.persistMode = PersistModeCheckpoint
        cfg.chunkMgr = cm
        cfg.serializer = serializer
        cfg.ckptInterval = interval
    }
}
```

### BTree struct 扩展

```go
// btree.go — BTree struct 新增字段

type BTree struct {
    // ... 现有字段 ...

    // 持久化配置 (从 btreeConfig 注入)
    persistMode  PersistMode
    wal          service.WAL
    walSync      WalSyncPolicy
    chunkMgr     service.ChunkManager
    serializer   *chunk.PageSerializer
    ckptInterval int
    setCount     atomic.Int64  // Set() 操作计数 (Checkpoint 模式用)
}
```

### BTree.Set() 改造

```go
// btree.go — Set() 内部化持久化
func (b *BTree) Set(_ context.Context, key, value []byte) error {
    // ... 现有 COW + MVCC + CAS 逻辑 ...

    // 新增: 根据 persistMode 选择持久化路径
    switch b.persistMode {
    case PersistModeWAL:
        entry := &service.WALEntry{
            Type:  service.WALTypeInsert,
            Key:   key,
            Value: value,
        }
        lsn, err := b.wal.Append(entry)     // ② 等价物: fwrite
        if err != nil {
            return err
        }
        if b.walSync == WalSyncEveryWrite {
            if err := b.wal.Sync(); err != nil {  // ✅ fsync
                return err
            }
        }
        // GroupCommit / EverySecond 由后台 goroutine 或 WAL 内部管理

    case PersistModeCheckpoint:
        count := b.setCount.Add(1)
        if count%int64(b.ckptInterval) == 0 {
            b.Save(context.Background())     // 周期 Checkpoint
        }
    }
    return nil
}
```

### BTree.Save() — Checkpoint 全量快照

```go
// btree.go — 新增 Save() 方法 (等效 Lealone BTreeStorage.save())

func (b *BTree) Save(ctx context.Context) error {
    if b.persistMode == PersistModeNone {
        return nil
    }

    // ① 遍历所有脏页
    items, err := b.EnumeratePages(nil)
    if err != nil {
        return err
    }

    // ② 写 pages → ChunkManager
    for _, item := range items {
        data, err := b.serializer.Serialize(item.Page)
        if err != nil {
            return err
        }
        pos, err := b.chunkMgr.Allocate(len(data), item.PageType)
        if err != nil {
            return err
        }
        if err := b.chunkMgr.WritePage(pos, data); err != nil {
            return err
        }
    }

    // ③ fsync (等效 FileStorage.sync())
    if err := b.chunkMgr.Sync(); err != nil {
        return err
    }

    // ④ WAL 截断 (如果 WAL 模式)
    //    等效 Lealone save() 后截断 RedoLog
    if b.wal != nil {
        b.wal.Truncate(b.wal.CurrentLSN())
    }

    return nil
}
```

### Benchmark 层大幅简化

```go
// main.go — benchmark 层不再需要接线逻辑

func run(label string, n, threads int, getOnly bool, mmapSize int, readRatios ...float64) {
    storage, _ := btree.NewOffheapBTreeStorage(mmapSize)

    // WAL 模式
    wal := newDiskWAL(...)
    tree, _ := btree.NewBTree(storage, btree.WithPersistWAL(wal, btree.WalSyncGroupCommit))

    // 或 Checkpoint 模式
    cm := newDiskChunkManager(...)
    ser := chunk.NewPageSerializer()
    tree, _ := btree.NewBTree(storage, btree.WithPersistCheckpoint(cm, ser, 10000))

    // benchmark loop: 一行 Set()，自动持久化
    for i := 0; i < n; i++ {
        tree.Set(ctx, keyOf(i), valOf(i))  // ← 持久化在 Set() 内部完成
        ops.Add(1)
    }

    // 结束时:
    tree.Save(ctx)  // 最后 Checkpoint (两种模式都可用)
    tree.Close()
}
```

### 代码结构

```
internal/infrastructure/storage/btree/
├── options.go          # 修改: +PersistMode + WalSyncPolicy + 4个 Option 函数
├── btree.go            # 修改: +persist 字段 + Set() 持久化分支 + Save() 方法
├── set_with_retry.go   # 修改: Set() 持久化分支 (如已拆分则改此处)
└── ...

cmd/tools/btree_bench/
├── main.go             # 修改: -persist-mode flag → BTree config (大幅简化)
├── main_test.go        # 修改: 测试两种 persist mode
└── (不再需要 persist.go / persist_wal.go / persist_checkpoint.go)
```

### 改动影响范围

| 文件 | 改动量 | 说明 |
|------|:------:|------|
| `options.go` | +40行 | PersistMode/WalSyncPolicy 类型 + 4个 Option |
| `btree.go` | +60行 | struct 扩展 + Set() 分支 + Save() |
| `main.go` | -30行 +15行 | 移除接线逻辑，改用 Option 注入 |

**总计**：btree 包约 +100 行，benchmark 包约 -15 行。

### Benchmark 场景

| 场景 | persistMode | 参数 | 说明 |
|------|:---------:|------|------|
| `seq-put-mem` | None | — | 纯内存基线 |
| `seq-put-wal-every-write` | WAL | every-write | 每条 fsync |
| `seq-put-wal-group-commit` | WAL | group-commit | 16条/批 fsync |
| `seq-put-wal-every-second` | WAL | every-second | 每秒 fsync |
| `seq-put-ckpt-100` | Checkpoint | interval=100 | 每 100 条 save |
| `seq-put-ckpt-1000` | Checkpoint | interval=1K | 每 1K 条 save |
| `seq-put-ckpt-10000` | Checkpoint | interval=10K | 每 10K 条 save |
| `seq-put-ckpt-100000` | Checkpoint | interval=100K | 每 100K 条 save |
| `seq-put-ckpt-end` | Checkpoint | interval=N | 仅末尾 save |
| `par-put-wal-8` | WAL | group-commit | 8线程 WAL |
| `par-put-ckpt-8-10000` | Checkpoint | interval=10K | 8线程 checkpoint |

---

## 两种模式预期性能对比

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
  ✅ 小批量高频 fsync (every-write: 15K-30K) 优于 Checkpoint (batch=1: 205)
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

### Phase 1：BTree config 扩展 + Set() 改造

| 任务 | 预估 | 内容 |
|------|:----:|------|
| 1.1 | 1h | `options.go` — PersistMode/WalSyncPolicy 类型 + 4个 Option 函数 |
| 1.2 | 3h | `btree.go` — struct 扩展 + Set() 持久化分支 + Save() |
| 1.3 | 2h | WAL GroupCommit/EverySecond 策略（后台 goroutine 定时 sync） |
| 1.4 | 2h | 单元测试 — TestSetWithPersistWAL, TestSetWithPersistCheckpoint 等 |

### Phase 2：Benchmark 简化 + 对接

| 任务 | 预估 | 内容 |
|------|:----:|------|
| 2.1 | 2h | `main.go` — 移除接线逻辑，改用 Option 注入 |
| 2.2 | 1h | `main_test.go` — 更新测试 |
| 2.3 | 1h | 兼容性验证 — `-persist=false` 行为不变 |

### Phase 3：Checkpoint 模式完整实现

| 任务 | 预估 | 内容 |
|------|:----:|------|
| 3.1 | 3h | `BTree.Save()` — EnumeratePages + ChunkManager + fsync |
| 3.2 | 2h | `BTree.Save()` 周期触发 — Set() 自动计数 + ckptInterval |
| 3.3 | 2h | 单元测试 — TestSave, TestSavePeriodic 等 |

### Phase 4：性能对比 + 文档

| 任务 | 预估 | 内容 |
|------|:----:|------|
| 4.1 | 1h | 跑 WAL + Checkpoint 全量 benchmark |
| 4.2 | 1h | 对照分析 — 与 Lealone 实测数据对比 |
| 4.3 | 2h | 更新 Pre/Post 文档 |

**总计**：约 20h

---

## 风险与 trade-off

| 风险 | 影响 | 缓解 |
|------|------|------|
| BTree.Set() 热路径增加 persist 分支 | 低 | switch 单次分支，纯内存模式下几乎零开销 |
| Checkpoint save() 期间阻塞 Set() | 高 | save() 持锁——与 Lealone `synchronized save()` 一致。大 batch 时由 ckptInterval 控制频率 |
| WAL 接入 BTree 增加耦合 | 中 | 通过接口注入（`service.WAL`），不依赖具体实现 |
| 旧 benchmark 兼容性 | 低 | 默认 PersistModeNone = 纯内存，行为不变 |

---

## 决策记录

### 决策 1：持久化放入 BTree config，通过 Option 模式注入（方案 C）

**理由**：
- benchmark 和生产 Set() 统一路径
- BTree 是唯一知道自己何时写了什么的人——持久化逻辑放在这里最合理
- Functional Option 模式已有先例（WithEpoch, WithMetrics），风格一致

### 决策 2：两种持久化模式共存于 BTree

**理由**：
- WAL-per-op = 生产 OLTP 路径
- Checkpoint = 批量导入路径 + 对照 Lealone
- 互斥但互补——通过 `PersistMode` enum 区分

### 决策 3：Checkpoint 模式复用 EnumeratePages + ChunkManager，不引入新组件

**理由**：
- 已有实现语义等价于 Lealone `save()` → Chunk.write()
- 不需要 WAL Entry 序列化 → 无 WAL 写入放大

### 决策 4：不实现独立的 UndoLog（Lealone 机制①）

**理由**：
- NexKV 的 MVCC 版本链已经保存了历史版本——功能等价于 UndoLog
- 不需要单独的链表结构
- 对标 Lealone 的映射：MVCC 版本链 = UndoLog，WAL = AOTE RedoLog，Save() = Checkpoint

---

## 附录

### Lealone 三大机制与 NexKV 的完整映射

| Lealone | NexKV | 状态 |
|---------|-------|:----:|
| ① UndoLog (纯内存) | MVCC 版本链 | ✅ 已有 |
| ② AOTE RedoLog (有 fsync) | WAL (接入 BTree.Set()) | 🚧 本次实现 |
| ③ AOSE Chunk RedoLog (无 fsync) | 无 | 暂不实现 |
| Save Checkpoint | BTree.Save() → ChunkManager | 🚧 本次实现 |
| LogSyncService (后台 sync) | WAL GroupCommit/EverySecond goroutine | 🚧 本次实现 |

### 关联文档

- [[NexKV vs Lealone 持久化设计深度对比]] — 三大机制完整源码分析 (1352行)
- [[PR-btree-bench-persistence-Pre]] — 当前 persist benchmark Pre 文档
- [[2026-05-16-phase4-wal-ao-persistence-spike]] — WAL+AO 集成 Spike
- [[2026-05-23-persistence-architecture-comprehensive-guide]] — 持久化架构全景

### 关联源码

| 项目 | 关键文件 |
|------|---------|
| Lealone | `lealone-aose/.../BTreeStorage.java:294-401` — `save()` |
| Lealone | `lealone-aote/.../log/UndoLog.java:93-183` — ① UndoLog |
| Lealone | `lealone-aote/.../log/UndoLogRecord.java:112-179` — ② writeForRedo |
| Lealone | `lealone-aote/.../log/LogSyncService.java:106-142` — ② 后台 sync |
| Lealone | `lealone-aote/.../log/RedoLog.java:287-489` — ② save() + sync |
| Lealone | `lealone-aose/.../chunk/Chunk.java:321-329` — ③ writeRedoLog (无 sync) |
| NexKV | `internal/infrastructure/storage/btree/options.go` | BTree config (扩展点) |
| NexKV | `internal/infrastructure/storage/btree/btree.go` | BTree struct + Set() (改造点) |
| NexKV | `internal/domain/service/wal.go` | WAL 接口 |
| NexKV | `internal/domain/service/chunk_manager.go` | ChunkManager 接口 |

---

> **文档版本**：v2.0  
> **创建日期**：2026-06-07  
> **最后更新**：2026-06-07  
> **Spike 状态**：待评审  
> **下一步**：架构师评审通过后启动 Phase 1（BTree config 扩展）
