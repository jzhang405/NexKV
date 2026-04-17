# Phase 3 Spike: WAL 持久化 + VersionChain GC 设计预研

> **预研类型**: Spike
> **创建日期**: 2026-04-17
> **分支**: `spike/phase3-wal-gc-proposal`
> **前置**: Phase 2 MVCC 快照隔离事务引擎（已完成）
> **状态**: Draft

---

## 一、概述

### 1.1 当前状态

| 模块 | 状态 | 说明 |
|------|------|------|
| BTree (btree/) | Phase 0-6.0 完成 | CAS 乐观锁 + Split 传播 + Tombstone |
| MVCC (mvcc/) | Phase 2 完成 | 快照隔离事务引擎，18 文件 2333 行 |
| WAL (wal/) | 接口 + DiskWAL 已实现 | Append/Sync/Recover/Truncate 完整 |
| GC | 未实现 | VersionChain 只增不减 |

### 1.2 Phase 3 目标

在 MVCC Phase 2 基础上引入**持久化 + 版本回收**：

| 能力 | Phase 2 | Phase 3 |
|------|---------|---------|
| 崩溃恢复 | 无（纯内存） | WAL Redo + Checkpoint 恢复 |
| 事务原子性 | best-effort undo | WAL Redo 保证 all-or-nothing（**依赖 Commit marker 在 Apply 之前 sync**） |
| 版本回收 | 无（链无限增长） | Watermark + 后台 Pruning |
| commitTS 分配 | Commit 前直接分配 | WAL sync **前**分配并嵌入 WriteBuffer entries（防止时间戳空洞） |
| 跨 key 原子性 | 单 key KeyLock 保证 | WAL + 2PC prepare（远期） |

---

## 二、已有基础设施盘点

### 2.1 WAL 模块（已实现）

**接口定义** — `internal/infrastructure/storage/wal/wal.go`：

```go
type WAL interface {
    Append(entry *WALEntry) (LSN, error)
    Sync() error
    Recover() ([]*WALEntry, error)
    Truncate(lsn LSN) error
    AppendAsync(ctx context.Context, entry *WALEntry) model.Task[LSN]
    TruncateAsync(ctx context.Context, lsn LSN) model.Task[struct{}]
    Close() error
}
```

**DiskWAL 实现** — `internal/infrastructure/storage/wal/diskwal.go`：
- Segment-based 文件（按 LSN 命名，默认 64MB/segment）
- CRC32 校验
- 三种 Sync 策略：PerWrite / EverySecond / Batch
- Recovery：扫描 `.wal` 文件目录

**WALEntry 结构** — `internal/infrastructure/storage/wal/types.go`：

```
Wire: [CRC:4][LSN:8][Type:1][TxID:8][Timestamp:8][PrevLSN:8][KeyLen:4][ValueLen:4][Key:N][Value:M]
```

Type 枚举：Insert / Update / Delete / Commit / Rollback / Checkpoint / Split

**MVP 限制**（需在 Phase 3 修复）：
- `AppendAsync` / `TruncateAsync` 是同步执行的 stub（`completed_task.go`）
- ~~CRC 字段 "reserved, calculated last"（未实际计算）~~ **已实现**（`types.go:147`：`CRC = crc32.ChecksumIEEE(buf[4:])`）— 文档信息已过时
- Recovery 无事务语义（不按 TxID 分组过滤）— **拟改进**：Step 2 实现
- Checkpoint 类型已定义但未消费 — **拟改进**：Step 3 实现

### 2.2 MVCC Commit 路径（WAL 接入点）

`internal/infrastructure/storage/mvcc/transaction.go` Commit 方法当前实现：

```go
func (tx *SnapshotTx) Commit(ctx context.Context) error {
    // Phase 1: PreCheck（冲突检测）
    // Phase 2: Allocate commitTS
    commitTS := tx.engine.tsGen.NextTS()   // ← 当前：在 apply 之前分配
    // Phase 3: Apply WriteBuffer
    if err := tx.applyWriteBuffer(ctx, commitTS); err != nil { ... }
}
```

> ⚠️ **Phase 3 计划改动**：commitTS 分配顺序需要调整，见 Section 3.1 WAL 写入时机

### 2.3 GC 预留点

| 预留点 | 位置 | 说明 |
|--------|------|------|
| `VersionChain.generation` | `mvcc/version_chain.go:28` | ABA 防护，Phase 2 仅递增 |
| `snapshotGet generation check` | `mvcc/transaction.go:211-233` | 乐观一致性校验 |
| `MaxVersions` | `model/btree_types.go:66` | 默认 10，GC 版本数阈值 |
| `SourceGC` | `model/source_id_defaults.go:16` | GC 任务调度器源 ID |
| `SourceCompaction` | `model/source_id_defaults.go:22` | Compaction 源 ID |
| `ExecutionOrderCompaction` | `concurrency/types.go:25` | 最低优先级执行顺序 |

### 2.4 BTree 持久化相关

| 预留点 | 位置 | 说明 |
|--------|------|------|
| `ExecutionOrderWALAppend = 1` | `concurrency/types.go:19` | 最高优先级（WAL 先于 BTree） |
| `ExecutionOrderBTreeSet = 2` | `concurrency/types.go:21` | BTree 在 WAL 之后 |
| `WALStats` | `service/storage.go:122` | WAL 统计信息结构 |
| `deleteEpoch`（规划中） | `offheap/page_layout.go` | PageHeader 有 `deleted uint8`，epoch 字段待加 |

---

## 三、WAL 集成设计

### 3.1 WAL 写入时机

> ⚠️ **评审修订**：原流程 Apply 在 Commit marker 之前，违反 WAL all-or-nothing。崩溃发生在 Apply 之后、Commit marker 之前时，BTree 有未提交数据的持久化，但 WAL 无 Commit marker 不重放，导致脏数据残留。

**修正后的 WAL 集成流程**：

```
Tx.Commit:
  1. PreCheck（冲突检测）
  2. commitTS = tsGen.NextTS()             ← 先分配（防止时间戳空洞）
  3. WAL.Append(WriteBuffer entries)        ← 带上 commitTS
  4. WAL.Append(Commit marker + commitTS)  ← 一次性 Append（原子单位）
  5. WAL.Sync()                            ← Group Commit 或 PerWrite
  6. Apply WriteBuffer（BTree + VersionChain）
```

**关键约束**：
1. **commitTS 必须在 WAL Append 前分配**，否则 WAL 丢失时时间戳空洞
2. **Commit marker 必须在 Apply 前 sync**，否则崩溃后 BTree 有脏数据残留

**WriteBuffer entry 需要携带 commitTS**（当前 WAL entry 格式不含 commitTS，需修改）。Commit marker entry 携带 commitTS，Recovery 时用于重放分配。

**⚠️ 与当前代码的矛盾**：`transaction.go:365` 的 `commitTS := tx.engine.tsGen.NextTS()` 在 `applyWriteBuffer` 之前。Phase 3 需要：
1. 将 `commitTS` 分配提前到 `WAL.Append` 之前
2. WriteBuffer entries 需要 embed commitTS（修改序列化格式）
3. Commit marker entry 携带 commitTS（修改 WAL entry 类型定义）

### 3.2 WAL 恢复流程

```
Recovery:
  1. 扫描所有 .wal segment 文件（按 LSN 排序）
  2. 按TxID 分组，只保留有 Commit marker 的事务
  3. 丢弃未提交事务的 entries（Rollback + 无 Commit marker）
  4. 按原始顺序重放已提交事务：
     a. 读取 Key/Value/Type + commitTS（来自 Commit marker）
     b. 调用 BTree.Set / BTree.Delete（携带 commitTS）
     c. 重建 VersionChain（如果 MVCC 开启）
  5. 恢复完成，设置下一个 LSN
```

**恢复策略**：**保守策略**（遇损坏即停），而非当前的"跳过继续"。

> ⚠️ **"保守策略"是拟改进**：当前 `Recover()` 实现是"跳过继续"（`wal/diskwal.go` MVP 限制）。Step 2 计划修改为遇损坏即停。

**⚠️ 关键缺失 — commitTS 重建机制**：

WriteBuffer entries 本身不含 commitTS，commitTS 来自 **Commit marker**。因此 Commit marker entry 格式必须携带 commitTS：

```go
// WAL entry Type=Commit 时携带 commitTS
type CommitMarker struct {
    LSN      LSN
    TxID     uint64
    CommitTS uint64   // ← 核心：嵌入 commitTS
}
```

Recovery 流程：扫描到 Commit marker 时提取 commitTS → 向前回溯同一 TxID 的 WriteBuffer entries → 按 LSN 顺序重放，统一使用该 commitTS。

**⚠️ 评审发现**：如果 Commit marker 格式不含 commitTS，Recovery 无法正确重建 BTree 的 MVCC 字段（beginTS/commitTS）。这是 WAL entry 格式的待修复项。

### 3.3 WAL 格式增强（基于 UnisonDB 研究）

**当前缺失**：

| 问题 | 风险 | 建议 |
|------|------|------|
| 无 Trailer/结束标记 | 断电后无法区分截断 vs 正常结束 | 增加 `0xDEADBEEF` trailer |
| ~~CRC 未实际计算~~ | ~~损坏检测失效~~ | ~~实现 CRC32 校验~~ — **已实现**（`types.go:147`） |
| 无 8-byte 对齐 | 跨扇区撕裂写入风险 | `alignUp(n + 7) & ^7` |
| **Commit marker 不含 commitTS** | **Recovery 无法重建 commitTS** | **新增 commitTS 字段到 Commit marker** |

**⚠️ Length 字段用途说明**：`Length:4` 放在 CRC 之后、LSN 之前，用于**变长 entry 的自我描述**。读取时先读 Length 跳到下一个 entry（跳跃扫描），用于损坏后快速定位下一条记录。CRC 计算范围为 `[Length之后]` 到 `[Trailer之前]`，Length 本身不纳入 CRC。

**建议格式**：

```
[CRC32:4][Length:4][LSN:8][Type:1][TxID:8][Timestamp:8][PrevLSN:8]
[KeyLen:4][ValueLen:4][Key:N][Value:M][Padding:0~7][Trailer:8]
```

- CRC 覆盖范围：`Length` 到 `Padding`（不含 CRC 本身）
- `Padding`：对齐到 8 字节（公式 `alignUp(n + 7) & ^7 - n`）
- `Trailer`：`0xDEADBEEF`，用于检测截断。若写入中断电，可能只写了部分 trailer，此时 CRC 校验失败
- `Type=Commit` 时 `KeyLen=ValueLen=0`，`Key/Value` 区域存放 `CommitTS:8`

> ⚠️ **Trailer 检测逻辑**：读取 entry 后，检查最后 8 字节是否为 `0xDEADBEEF`。若不是，说明 entry 不完整（写入中断）。此检查在 CRC 校验之后执行。

### 3.4 Sync 策略：Group Commit

**推荐演进路径**：`SyncPolicyEveryWrite` → `Group Commit`

Group Commit 工作原理：
1. 多个并发事务的 WAL Append 只写入 OS buffer（不 fsync）
2. 后台 `syncWorker` goroutine 周期性（如 1ms）执行一次 fsync
3. 所有在本次 fsync 前完成 Append 的事务一起被持久化
4. 等待 Sync 的事务通过 channel/block 等待通知

**收益**：fsync 是 WAL 的主要瓶颈（~1ms/次）。Group commit 将 N 次合并为 1 次，吞吐提升 N 倍。

**⚠️ 评审发现 — Group Commit 破坏 LSN→commitTS 单调性**：

```
事务A: WAL LSN=1, Append → 等待 Sync (channel)
事务B: WAL LSN=2, Append → 等待 Sync (channel)
fsync batch 完成顺序：B 先于 A

若 commitTS 按 Sync 完成顺序分配：
  commitTS(B) < commitTS(A)

但 LSN(B) > LSN(A)，意味着 later WAL entry 有更小的 commitTS
→ 违反 MVCC "LSN 顺序 = commitTS 顺序" 的单调性保证
```

**解决方案**：commitTS **在 Append 时按 LSN 顺序预留**，不等 Sync 完成才分配。

```go
// Append 时：LSN 顺序即 commitTS 顺序
entry.LSN = atomic.AddUint64(&wal.lsnCounter, 1)
entry.commitTS = wal.nextCommitTS()  // 单调递增，与 LSN 一致
// 不再等 Sync 才分配 commitTS
```

Group Commit 只控制 **fsync 时机**，不改变 commitTS 分配顺序。

### 3.5 Checkpoint 设计

**分层策略**：

| 场景 | 策略 | 说明 |
|------|------|------|
| 在线运行 | Fuzzy Checkpoint | 后台 goroutine 周期性刷脏页 |
| 正常关闭 | Sharp Checkpoint | 暂停写入，刷全部脏页 + TRUNCATE WAL |
| 分布式快照 | Sharp Checkpoint | 作为快照基准 |

**Fuzzy Checkpoint 流程**：

```
1. 记录 checkpointStartLSN
2. 后台遍历 BTree 脏页，逐页写入主存储
3. 记录 checkpointEndLSN
4. 写入 Checkpoint WAL entry
5. Truncate LSN < checkpointEndLSN 的 WAL segments
```

**触发条件**：
- WAL segment 数量超过阈值
- 时间间隔（如 30s）
- 脏页比例超过阈值
- 手动触发

**⚠️ 评审发现 — Truncate 非原子，crash 后可能不一致**：

Sharp Checkpoint 第 5 步"Truncate WAL segments"是非原子操作。如果在删除部分 segment 后 crash（只删了 segment-1 和 segment-2，但还没删 segment-3），文件系统状态和 Checkpoint entry 中的 `checkpointEndLSN` 不一致。

**标准工业做法**：

```
1. 将待删除的 segment 重命名为 .wal.deleting（如 segment-5.wal → segment-5.wal.deleting）
2. fsync(父目录) — 确保重命名持久化
3. 删除 .deleting 文件
4. fsync(父目录) — 确保删除持久化
5. 写入 Checkpoint entry（含 checkpointEndLSN）
```

若 crash 发生在任意步骤，重启后：
- 扫描 WAL 目录，清理所有 `.wal.deleting` 文件
- 从最新 Checkpoint entry 的 `checkpointEndLSN` 开始重放

**Fuzzy Checkpoint 的边界情况**：`步骤3(记录checkpointEndLSN)` 和 `步骤4(写入Checkpoint entry)` 之间 crash，此时脏页已写出但 checkpoint 位置丢失。正确策略：**从上一个有效 Checkpoint 开始重放**，而非截断到 checkpointEndLSN。

---

## 四、VersionChain GC 设计

### 4.1 问题分析

Phase 2 的 VersionChain 是 append-only 不可变链表。每次 commit 在链头 Prepend 一个 VersionNode。不做 GC 意味着：

- **内存无限增长**：每次 Update/Insert 产生一个节点
- **读取性能退化**：snapshotGet 需要遍历整个链找 bestNode
- **Go GC 压力**：大量小对象增加 GC 暂停时间

### 4.2 GC 方案评估

| 方案 | 原理 | 优点 | 缺点 |
|------|------|------|------|
| **Watermark GC** (推荐) | 后台 GC 遍历所有 VersionChain，回收 `commitTS < watermark` 的中间版本 | 实现简单，无热路径开销 | 长事务阻塞所有 GC |
| **Eager Pruning** (Steam 论文) | ~~每次 Prepend 时顺便裁剪~~ | 链长度有界 | ~~热路径 O(n) 开销，不应在 Prepend 内执行~~ |
| **Epoch-based Reclamation** | 全局 epoch 推进后回收 | 安全释放内存 | Go 适配复杂 |
| **Epoch-based + 后台 Eager** | Epoch 推进 + 后台并发裁剪 | 各取所长 | 实现复杂度中等 |

> ⚠️ **评审修订**：原"混合方案（Watermark + Eager Pruning）"中 Eager Pruning 在 `Prepend` 热路径执行 O(n) 遍历，引入不确定延迟到 commit 关键路径，且遍历过程中链可能被并发修改。**Eager Pruning 应作为独立后台任务**，不在 Prepend 内同步执行。

**修正推荐方案**：Watermark GC + 后台 Eager Pruning（独立 goroutine）

### 4.3 ActiveTxRegistry + Watermark GC

**核心思路**：

```
watermark = min(所有活跃事务的 snapshotTS)
```

任何 `commitTS < watermark` 且**不是链中该 watermark 之前的最新可见版本**的节点可以回收。

**⚠️ txID 来源**：当前 `SnapshotTx` 结构体（`transaction.go:114-123`）**无 txID 字段**。ActiveTxRegistry 需要新增 txID 生成方案：

```go
// 方案：从 TSGenerator 分配 txID（与 commitTS 同源）
type ActiveTxRegistry struct {
    txs sync.Map // txID → snapshotTS
    lastTxID atomic.Uint64
}

func (r *ActiveTxRegistry) Register(tx *SnapshotTx) uint64 {
    txID := atomic.AddUint64(&r.lastTxID, 1)
    r.txs.Store(txID, tx.snapshotTS)
    return txID
}
```

**ActiveTxRegistry**（新增）：

```go
type ActiveTxRegistry struct {
    mu      sync.Mutex
    txs     sync.Map // txID → snapshotTS
}

func (r *ActiveTxRegistry) Register(txID uint64, snapshotTS uint64)    // BeginTx 时调用
func (r *ActiveTxRegistry) Unregister(txID uint64)                      // Commit/Rollback 时调用
func (r *ActiveTxRegistry) Watermark() uint64                           // GC 时调用（需加锁保护）
```

**⚠️ Watermark 计算和 GC 执行之间存在 TOCTOU 窗口**：

```
T1: watermark = 100（所有活跃 tx snapshotTS >= 100）
T2: 新事务 T2 开始，snapshotTS = 50（在 watermark 计算之后注册）
T3: GC 开始回收 commitTS < 100 的节点
T4: T2 的 snapshotTS = 50，这些节点对它可见！但已被 GC 回收
```

**缓解**：BeginTx 注册和 GC 之间需要内存屏障（`atomic` 操作保证可见性），且 GC 遍历期间新注册的事务不受当前 watermark 约束（它们的 snapshotTS 更大，不会读到被回收的节点）。

### 4.4 后台 Eager Pruning（独立任务，不在 Prepend 内）

```
PruneBackground(watermark):
  1. 获取 VersionStore.chains 的 snapshot（sync.Map Range）
  2. 对每个 key 的链：
     a. 遍历链，找到可回收节点（commitTS < watermark 且非链中第一个 < watermark）
     b. CAS 修改 next 指针摘除节点
     c. 将摘除节点放入 limbo channel（不保留引用，由 Go GC 自然回收）
```

**⚠️ pruneVersionChains 无 per-key 同步**：GC 遍历 VersionChain 时无 per-key 锁，与并发 `commitKey` 的 KeyLock 无协调。需引入 epoch 或 per-chain mutex 保护。

### 4.5 后台 GC Goroutine

```go
func (tm *TransactionEngine) runGC(ctx context.Context) {
    ticker := time.NewTicker(tm.config.GCInterval) // 默认 5s
    for {
        select {
        case <-ticker.C:
            watermark := tm.activeTxRegistry.Watermark()
            if watermark == 0 { continue } // 无活跃事务
            tm.pruneVersionChains(watermark)
        case <-ctx.Done():
            return
        }
    }
}
```

### 4.6 Limbo Bag 语义

**推荐使用 channel 方式**：

```go
limbo chan *VersionNode  // 节点不可达，Go GC 自然回收
```

> ⚠️ 若使用 `[]*VersionNode` slice 持有引用，会造成内存泄漏。若使用 `runtime.SetFinalizer`，可确保节点最终释放。

### 4.7 GC 与 WAL 的交互

GC 回收 VersionNode 后需要记录 WAL entry 吗？

**不需要**。原因：
- VersionChain 是纯内存结构，不持久化到 WAL
- WAL 只记录逻辑操作（Key/Value 的 Set/Delete）
- 崩溃恢复时 VersionChain 从 BTree 的 beginTS 重建
- GC 回收的是内存中的链节点，不影响持久化数据

---

## 五、实施路线建议

### 5.1 推荐顺序

> ⚠️ **评审修订**：原 GC → WAL 依赖关系**不成立**。WAL 恢复只依赖 Commit marker，不依赖 ActiveTxRegistry。GC 的 ActiveTxRegistry 被 Checkpoint 复用，而非 WAL 依赖。
>
> **修正建议**：GC 和 WAL 可**并行开发**，Checkpoint 最后。

```
Step 1: VersionChain GC（纯内存操作，独立）
Step 2: WAL 集成（与 GC 并行，无依赖）
  ↓（Step 1 + 2 稳定后）
Step 3: Checkpoint（依赖 WAL Truncate）
```

| Step | 估算（评审修订） | 评审修订原因 |
|------|----------------|-------------|
| GC | 5-7 天（原 3-5 天） | ActiveTxRegistry txID 设计 + 并发 CAS 安全性验证 + 后台 Eager Pruning 独立任务 |
| WAL | 5-7 天 | 基本合理，但需额外处理 commitTS 嵌入 WriteBuffer 格式 |
| Checkpoint | 7-10 天（原 3-5 天） | BTree 脏页追踪（COW 语义评估）+ Checkpoint 流程 + 与 WAL 协调 + Truncate 原子性 |
| **总计** | **17-24 天**（原 11-17 天） | 实际工作量约为估算的 1.5-2x |

### 5.2 Step 1: VersionChain GC（预估 5-7 天）

**接口变更**：

```go
// 新增：mvcc/active_tx_registry.go
type ActiveTxRegistry struct {
    mu      sync.Mutex
    txs     sync.Map // txID → snapshotTS
    lastTxID atomic.Uint64  // txID 生成器
}
// BeginTx: txID := registry.Register(snapshotTS)
// Commit/Rollback: registry.Unregister(txID)

func (r *ActiveTxRegistry) Watermark() uint64  // 需加锁遍历

// 修改：mvcc/version_chain.go
func (vc *VersionChain) Prune(watermark uint64) int  // 后台任务调用，非 Prepend 内

// 新增：mvcc/gc.go
type GCConfig struct {
    Interval    time.Duration // GC 周期，默认 5s
    MaxVersions int           // 最大保留版本数，默认 10
    BatchSize   int           // 每次GC处理的 key 数
}

func (tm *txManager) runGC(ctx context.Context)   // 后台 goroutine
func (tm *txManager) pruneVersionChains(watermark uint64)  // 遍历所有 chains
```

> ⚠️ **关键新增**：ActiveTxRegistry 需要在 `BeginTx` 时分配 txID（从 `lastTxID` 原子递增），并与 `SnapshotTx` 生命周期绑定。

**验证标准**：
- [ ] GC 后 VersionChain 长度 ≤ MaxVersions（充分条件非必要）
- [ ] 活跃 SI 事务不受 GC 影响（仍能看到正确快照）
- [ ] 并发 GC + 读写无竞态（`-race` 通过）
- [ ] Watermark 在无活跃事务时正确回收所有旧版本
- [ ] **新增**：长事务存在时，GC 正确保留旧版本（不被阻塞回收）

### 5.3 Step 2: WAL 集成（预估 5-7 天）

**⚠️ 关键接口变更**（相较于原计划）：

```go
// 修改：wal/types.go WALEntry 格式
// Type=Commit 时新增 CommitTS 字段（其他类型不含）
type WALEntry struct {
    LSN      LSN
    Type     EntryType
    TxID     uint64
    Timestamp uint64
    PrevLSN  LSN
    Key      []byte
    Value    []byte
    CommitTS uint64  // 仅 Type=Commit 时有效
}

// 修改：mvcc/transaction.go Commit 方法
func (tx *SnapshotTx) Commit(ctx context.Context) error {
    txID := tx.engine.txIDCounter.Add(1)
    commitTS := tx.engine.tsGen.NextTS()           // 分配 commitTS（在 WAL Append 之前）
    registry.Register(txID, tx.snapshotTS)         // 注册活跃事务

    // WAL.Append 前：WriteBuffer entries 携带 commitTS
    entries := tx.writeBuffer.ToWALEntries(commitTS)
    for _, entry := range entries {
        tx.engine.wal.Append(entry)
    }

    // Commit marker 携带 commitTS
    commitEntry := &WALEntry{Type: Commit, TxID: txID, CommitTS: commitTS}
    tx.engine.wal.Append(commitEntry)
    tx.engine.wal.Sync()

    // Apply 在 Sync 之后（确保 Commit marker 已持久化）
    if err := tx.applyWriteBuffer(ctx, commitTS); err != nil {
        registry.Unregister(txID)
        return err
    }
    registry.Unregister(txID)
    return nil
}
```

> ⚠️ **原计划错误**：原计划"commitTS 移到 WAL.Sync() 之后"会导致 Apply 在 Commit marker 之前，违反 all-or-nothing。正确的分配顺序是：**commitTS 在 WAL.Append 之前分配并嵌入 WriteBuffer entries**。

**验证标准**：
- [ ] 写入数据 → kill 模拟 → 恢复后数据完整
- [ ] 未提交事务在恢复后不可见
- [ ] CRC 校验损坏的 WAL 被正确检测
- [ ] Group Commit 吞吐优于 PerWrite Sync
- [ ] **新增**：commitTS 按 LSN 顺序单调递增（Group Commit 场景下验证）

### 5.4 Step 3: Checkpoint（预估 7-10 天）

> ⚠️ **评审发现**：Step 3 最大未识别依赖是 BTree 脏页追踪集成点。DirtyTracker 接口在文档中设计，但当前 BTree 代码中没有调用点（COW 语义下脏页追踪方案需重新评估，见 Section 6.2 问题 1）。

**接口变更**：

```go
// 新增：storage/checkpoint.go
type CheckpointManager struct {
    wal      WAL
    bt       *BTree
    dirtyTracker *DirtyTracker  // COW 语义下需重新设计
}

func (cm *CheckpointManager) FuzzyCheckpoint() error
func (cm *CheckpointManager) SharpCheckpoint() error
    // 1. 暂停写入
    // 2. 刷所有脏页（见 6.2 问题 1 脏页追踪方案）
    // 3. WAL.Append(Checkpoint entry)
    // 4. WAL.Sync()
    // 5. Rename-Then-Delete WAL segments（原子性保护）
func (cm *CheckpointManager) RecoverFromCheckpoint() error
```

**验证标准**：
- [ ] Fuzzy Checkpoint 不阻塞读写（P99 延迟 < Xms）
- [ ] Sharp Checkpoint 后 WAL 可截断
- [ ] Checkpoint + WAL 恢复 = 完整数据
- [ ] 定时 Checkpoint 正常触发
- [ ] **新增**：DirtyTracker 标记的页面与实际修改页面一致（COW 语义下验证）

---

## 六、风险与开放问题

### 6.1 高风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| ~~GC 裁剪与并发 snapshotGet 竞争~~ | ~~generation 检查 + Go GC 内存安全~~ | **已重新设计**：Eager Pruning 移出 Prepend 热路径，改为后台任务 |
| WAL sync 性能瓶颈 | 写入吞吐下降 | Group Commit 批量刷盘 |
| Checkpoint 期间脏页追踪 | **COW BTree 语义下方案需重新评估** | 见 6.2 问题 1 |
| **Apply 在 Commit marker 之前（原始设计）** | **BTree 残留未提交脏数据** | **已修复**：Commit marker → Sync → Apply |
| **Recovery 无 commitTS 重建机制** | **MVCC 可见性系统无法工作** | **已计划**：Commit marker 携带 commitTS |
| **Group Commit 破坏 LSN→commitTS 单调性** | **MVCC 时间戳单调性被违反** | **已计划**：commitTS 在 Append 时按 LSN 顺序分配 |
| **Eager Pruning 在 Prepend 热路径执行 O(n)** | **commit 关键路径引入不确定延迟** | **已修复**：独立后台 goroutine |
| **GC 执行和 Watermark 计算非原子** | **新事务在 GC 期间注册可能读到正在回收的版本** | 需 epoch/barrier 机制 |
| **pruneVersionChains 无 per-key 同步** | **GC 与并发 commitKey 并发修改同一 key 时无安全保证** | 需 per-chain mutex 或 epoch 保护 |

### 6.2 开放问题研究成果

#### 问题 1：BTree 脏页追踪

> ⚠️ **评审修订**：原方案对 NexKV BTree 架构理解有误。NexKV BTree 是 **COW（Copy-on-Write）B+Tree**（`btree/btree.go`），每次写入分配新页面，旧页面作为快照保留在树中。**这改变了脏页追踪的语义**。

**COW BTree 的脏页语义**：

在 COW 架构下：
- 旧页面**永远不更新**，写入总分配新页面
- "脏页位图"追踪的是"哪些页面被修改过"——但这些旧页面已经是快照，不会再被覆盖写入
- **真正需要追踪的是"哪些页面当前在活跃路径上"**，而非"哪些页面被修改过"

| 方案 | 原理 | 适用 COW？ | 评价 |
|------|------|-----------|------|
| **A. 全量遍历** | Checkpoint 时遍历整棵 BTree，逐页写出 | **适用** | COW 下无需标记，所有活跃路径页面都需要写出 |
| **B. 脏页位图** | 修改页面时标记 dirty | **不适用** | 旧页面永不更新，标记无意义 |
| **C. COW 特性利用** | 利用 COW 特性：活跃路径即需要写出的页面 | **推荐** | 无需额外追踪 |
| **D. Page-level LSN** | 每页记录最新修改的 LSN | **适用** | 需要每页增加 8 字节 LSN 字段 |

**修正推荐方案**：利用 COW 特性 — **Checkpoint 遍历活跃路径即可**

```
Checkpoint 时：
  1. 从 BTree 根节点开始 DFS/BFS 遍历
  2. 追踪所有可达页面（活跃路径）
  3. 将这些页面写入主存储
  4. 不需要脏页位图（所有活跃路径页面都是"脏的"）
```

**理由**：COW BTree 中，只有活跃路径上的页面会被未来的写入覆盖（因为它们会被分裂/合并）。非活跃的旧页面不会改变，Checkpoint 只需要写出活跃路径。

> ⚠️ **待验证**：如果 BTree 有"分裂/合并导致旧页面变活跃"的场景，则需要更复杂的追踪。需确认 BTree 实现中是否有此场景。

---

#### 问题 2：WAL 是 Logical 还是 Physical

**定义区分**：

| 类型 | 记录内容 | 例子 | 适用场景 |
|------|---------|------|----------|
| **Physical WAL** | 页面物理变更（修改了哪个文件的哪个偏移写入什么字节） | PostgreSQL：`PageFooter + full page image` | 崩溃恢复、基于块的复制（流复制） |
| **Logical WAL** | 逻辑操作（INSERT INTO t VALUES(...)）| PostgreSQL logical decoding：`REDO function calls` | CDC、跨版本迁移、逻辑复制 |
| **Logical-with-redo-info** | 逻辑操作 + 重做所需的最小物理信息 | LeanXcale：`operation + key + value` | KV 存储的 MVCC 恢复 |

**关键澄清**：NexKV 文档中的 "logical" 指的是 **KV 层面的逻辑操作**（Key/Value Set/Delete），不同于 PostgreSQL 的 logical decoding（SQL 层面的逻辑变更）。

**当前 NexKV 设计**：

```
[CRC:4][LSN:8][Type:1][TxID:8][Timestamp:8][PrevLSN:8][KeyLen:4][ValueLen:4][Key:N][Value:M]
```

这是 **Logical-with-redo-info** 模式——记录了 Key/Value 逻辑变更，加上足够重建的数据（value 本身）。

**生产系统对比**：

| 系统 | WAL 类型 | 原因 |
|------|---------|------|
| PostgreSQL | Physical（主要）+ Logical（可选） | 崩溃恢复需要物理一致性，logical decoding 用于 CDC |
| RocksDB | Logical（kvPair） | MemTable 已是 kv 结构，WAL = MemTable 的镜像 |
| Badger | Logical（kvPair） | 同 RocksDB，value log 分离 |
| OrioleDB | Physical + Undo | MVCC 行版本存于页面内，WAL 记录物理页面变更 + Undo log |
| **NexKV（推荐）** | **Logical（kvPair）** | 与 MVCC VersionChain 语义一致，恢复时重放 kv 操作 |

**推荐决定：保持 Logical-with-redo-info**

理由：
1. **与 MVCC 语义一致**：MVCC 事务记录的是 Key/Value 版本变更，WAL 直接对应这些版本
2. **实现简单**：不需要维护页面布局的物理一致性
3. **跨版本兼容**：页面格式变化时，Logical WAL 更易兼容
4. **足够用于恢复**：崩溃恢复只需重放 kv 操作，不需要知道 BTree 页面布局

**演进路径**：当前 → Logical-with-redo-info → 可选的 physical redo 增强（如果未来发现 recovery 慢）

---

#### 问题 3：分布式场景的 WAL

**背景**：NexKV 是去中心化 KV（3-100 节点，无单点故障）。单机 WAL 设计无法直接扩展到分布式。

**分布式 WAL 的核心挑战**（来源：VLDB 2024 PALF Paper）：

> "Design a replicated logging system as the foundation of a distributed database with ACID transactions is still a non-trivial problem."

**三种分布式 WAL 模式对比**：

| 模式 | 原理 | 代表系统 | WAL 复制方式 | 适用场景 |
|------|------|---------|-------------|----------|
| **Leader-based WAL** | 所有写入经 Leader WAL 串行化，Follower 异步复制 | PostgreSQL 流复制、etcd | WAL 块同步复制到 Follower | 强一致事务 |
| **Quorum-based WAL** | 写入时写 W 个节点，WAL 条目带全局序号 | DynamoDB、Cassandra | 无中心 WAL，每个节点有本地 WAL | 最终一致 KV |
| **Replicated WAL Service** | 独立的 WAL Service（如 Kafka），状态机复制 | CockroachDB、TiKV | Raft 复制 WAL entries | 分布式事务 |
| **Gossip-based WAL** | 无全局 WAL，每个节点本地 WAL，定期 gossip 同步 | DyWAL、自研实验系统 | 最终一致，冲突合并 | 弱一致场景 |

**NexKV 当前能力边界**：

- NexKV 使用 **Quorum（读写）** 做强一致写入
- 本地 WAL 只保证本地节点 crash recovery
- **跨节点数据一致性**由分片副本的 Quorum 写保证

**分布式 WAL 的核心问题**：本地 WAL 无法保证跨节点事务原子性

```
场景：节点 A 执行跨分片事务（分片1 和分片2）
- A 的 WAL 记录了 "写入分片1" 和 "写入分片2"
- 如果节点 A 在完成分片2 写入后 crash
- 节点 B（持有分片2 的另一个副本）没有这个事务的 WAL 记录
- 分片1 和分片2 数据不一致
```

**解决方案评估**：

| 方案 | 描述 | 实现成本 | 适用范围 |
|------|------|---------|---------|
| **1. 单机 WAL + 无跨节点原子性** | 维持现状，跨分片事务仅靠 KeyLock 保证 | 无 | 当前 MVP |
| **2. WAL 即 Service（Kafka-style）** | 独立 WAL Service，所有节点写入 WAL Service | 高，需要 Raft 复制 WAL Service | 强一致跨分片事务 |
| **3. 2PC + WAL** | prepare 阶段写 WAL，commit 阶段同步 | 中，需要改造事务层 | 跨分片原子性 |
| **4. Chain Replication** | 分片副本链式复制，WAL 随链传播 | 中低，适合线性拓扑 | 强一致读副本 |

**远期建议：采用 2PC + WAL 方案**

Step 1（Phase 3-4）：单机 WAL + Checkpoint 稳定
Step 2（远期）：引入 2PC prepare 写 WAL，commit 同步

**结论**：分布式 WAL 是远期问题，**不阻塞 Phase 3 单机 WAL 实现**。建议在文档中标注：`"分布式 WAL 需要 Phase 4+ 考虑，当前 MVP 只保证单机事务原子性"`

---

#### 问题 4：VersionChain 崩溃恢复重建

**背景**：崩溃后内存中的 VersionChain 丢失。BTree 数据（含 beginTS）和 WAL 仍然存在。如何从这两者重建 VersionChain？

> ⚠️ **评审修订 — 原"链头指针持久化"方案有误**：`head` 是 `*VersionNode`（内存地址），重启后完全无效。WAL 只记录 Key/Value 操作，**不记录 VersionChain 的链表拓扑**（next 指针）。Checkpoint 无法通过链头 commitTS+LSN 重建链表的 next 关系。

**生产系统重建方案对比**：

| 系统 | 重建方式 | 持久化内容 | 重建过程 |
|------|---------|-----------|---------|
| **PostgreSQL** | Undo Log 回滚 | 行版本存于 Heap 页面内 | 从 Heap 读取最新行 → 遍历 Undo chain → 重构可见版本 |
| **MySQL InnoDB** | Rollback Segment | Undo Log + MVCC 链表 | 读取 clustered index → 跟随 MVCC 链表 → 过滤不可见版本 |
| **OrioleDB** | Undo + Copy-on-Write | 页面内 Tuple 版本 | 读取页面 → 跟随 Undo chain → 重构 B-Tree 版本 |
| **LeanXcale** | KV-log + MVCC | Undo Log | 读取 KV → 应用 Undo → 恢复历史版本 |

> ⚠️ **关键发现**：所有工业系统都使用 **Undo Log** 而非"链头指针"来重建 VersionChain。NexKV 目前**没有 Undo Log**，这是 VersionChain 持久化缺失的关键问题。

**VersionChain 重建的三种路径**：

| 路径 | 原理 | 优点 | 缺点 |
|------|------|------|------|
| **A. WAL 全量重放** | 扫描所有 WAL entry，按 TxID 分组，找到所有 Commit 的版本，重建 VersionChain | 完整，不丢数据 | 慢（要扫描全量 WAL），只适合小数据量 |
| **B. BTree-only 重建** | 从 BTree 读取所有 Key 的当前版本，构建只含最新版本的简化 VersionChain | 快，无需额外存储 | **丢失所有历史快照，违反 SI 语义** |
| **C. WAL 全量重放 + BTree 辅助** | WAL 重放时，对于每个 Key 按 commitTS 顺序重建链 | 正确，不依赖额外存储 | 复杂度高，需要 WAL 有完整 commitTS |
| **D. Undo Log** | 每个 VersionNode 写入时同时写 Undo Log | PostgreSQL 验证可行 | 需要额外存储，属于大改动 |

**NexKV 推荐：路径 C（WAL 全量重放 + BTree 辅助）**

> ⚠️ **路径 C 的关键前提**：WAL entry 需要携带 commitTS（见 Section 3.1/3.2 修复）。如果 Commit marker 携带 commitTS，Recovery 时：
> 1. 按 TxID 分组扫描 Commit marker → 获得 commitTS
> 2. 同一 TxID 的 WriteBuffer entries 拥有相同 commitTS
> 3. WAL entries 按 LSN 顺序 → 即 commitTS 顺序 → 可重建正确的 VersionChain 拓扑

**简化方案（Phase 3 MVP）—— 路径 B**：

由于 Commit marker 格式改造（携带 commitTS）工作量较大，**Phase 3 MVP 先用路径 B 过渡**：

```go
// 简化恢复：从 BTree 重建 VersionChain（只含最新可见版本）
func RecoverVersionChains(bt *BTree) error {
    iter := bt.NewIterator()
    for iter.SeekFirst(); iter.Valid(); iter.Next() {
        node := iter.Current()
        if node.IsDeleted() { continue }
        vc := &VersionChain{
            head: &VersionNode{
                commitTS: node.commitTS,
                value:    node.value,
                generation: 0,
                next: nil,
            },
        }
        bt.SetVersionChain(node.key, vc)
    }
    return nil
}
```

**⚠️ 此简化方案的风险（被低估）**：
1. 崩溃后**丢失所有历史快照**——长时间运行的 SI 读事务在重启后可能看到不一致的历史
2. Read-Your-Own-Writes 保障在重启后可能失效
3. MVCC 快照隔离的核心价值被削弱

> ⚠️ **Phase 3 后期应升级到路径 C**：修改 Commit marker 携带 commitTS，WAL 重放时重建完整 VersionChain。

**完整路径 D（Undo Log）的远期考虑**：如果 Phase 3 之后需要完整 MVCC 历史恢复能力，可引入 Undo Log（参考 PostgreSQL）。这是 Phase 4+ 的选项。

---

### 6.3 开放问题决策汇总（评审修订版）

| 问题 | 结论 | 阻塞 Phase 3？ | 备注 |
|------|------|--------------|------|
| BTree 脏页追踪 | **修正**：利用 COW 特性，Checkpoint 遍历活跃路径即可，无需脏页位图 | 否（Checkpoint 独立 Step） | 需 Step 3 验证 COW 语义假设 |
| WAL logical vs physical | 保持 Logical-with-redo-info | 否 | 与 MVCC 语义一致 |
| 分布式 WAL | 远期问题（Phase 4+） | 否 | ⚠️ **新增风险警告**：跨分片事务在节点崩溃后可能部分提交，当前 MVP 只保证单分片原子性 |
| VersionChain 恢复 | **修正**：MVP 路径 B（BTree-only）；远期路径 C（WAL 重放 + CommitTS） | 否（Checkpoint 独立 Step） | ⚠️ **原路径 C 方案有误**（链头指针无法重建链表） |
| **WAL commitTS 嵌入** | **新增**：Commit marker 必须携带 commitTS | **是（Step 2 必须修复）** | 当前 WAL entry 格式不含 commitTS，Recovery 无法重建 MVCC |
| **DirtyTracker 接口** | 新增接口，当前代码无调用点 | 是（Step 3 必须实现） | COW 语义下DirtyTracker 方案已修正为活跃路径遍历 |

---

## 七、参考资料

- [Building a Corruption-Proof WAL in Go — UnisonDB](https://unisondb.io/blog/building-corruption-proof-write-ahead-log-in-go/)
- [SQLite WAL Documentation](https://www.sqlite.org/wal.html)
- [Scalable Garbage Collection for In-Memory MVCC Systems (TUM Steam 论文)](https://db.in.tum.de/~boettcher/p128-boettcher.pdf)
- [CMU 15-721: MVCC Garbage Collection](https://15721.courses.cs.cmu.edu/spring2020/notes/05-mvcc3.pdf)
- [LSM in a Week: Watermark and GC](https://skyzh.github.io/mini-lsm/week3-04-watermark.html)
- [TiDB MVCC Garbage Collection Guide](https://pingcap.github.io/tidb-devguide/understand-tidb/mvcc-garbage-collection.html)
- Lealone 源码：`thoughts/Lealone/`
- NexKV MVCC Phase 2 设计：`docs/07_spike/btree-refactor/2026-04-10-mvcc-phase2-plan.md`

---

**文档版本**: v1.2
**创建日期**: 2026-04-17
**最后更新**: 2026-04-17
**更新内容**: 全部 4 个 Review Agent 评审后系统性修复：
- Section 3.1: WAL 流程修正（Commit marker→Sync→Apply）
- Section 3.2: Recovery commitTS 重建机制
- Section 3.3: WAL 格式增强（Length 字段用途说明，CommitTS 字段新增，CRC32 已实现状态修正）
- Section 3.4: Group Commit LSN→commitTS 单调性修复
- Section 3.5: Checkpoint Truncate 原子性（rename-before-delete + fsync 目录）
- Section 4.2-4.6: GC 方案全面修订（Eager Pruning 移出 Prepend 热路径，改为后台任务；Limbo Bag 语义明确；Watermark TOCTOU 窗口说明）
- Section 5: 实施路线修正（GC↔WAL 并行非依赖，时间估算修订，CRC32 已实现修正，ActiveTxRegistry txID 来源补充）
- Section 6.1: 高风险列表更新（新增 5 项，修订 1 项）
- Section 6.2 问题 1: COW 脏页追踪语义修正
- Section 6.2 问题 3: 分布式 WAL 风险警告补充
- Section 6.2 问题 4: 链头指针无法重建链表错误，远期路径修正
- Section 6.3: 决策汇总修订（新增 WAL commitTS 嵌入和 DirtyTracker 为阻塞项）
**状态**: Draft — 等待架构师评审
