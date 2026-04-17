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
| 事务原子性 | best-effort undo | WAL Redo 保证 all-or-nothing |
| 版本回收 | 无（链无限增长） | Watermark + Eager Pruning |
| commitTS 分配 | Commit 前直接分配 | WAL sync 后分配（预留点已存在） |
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

**MVP 限制**：
- `AppendAsync` / `TruncateAsync` 是同步执行的 stub（`completed_task.go`）
- CRC 字段 "reserved, calculated last"（未实际计算）
- Recovery 无事务语义（不按 TxID 分组过滤）
- Checkpoint 类型已定义但未消费

### 2.2 MVCC Commit 路径（WAL 接入点）

`internal/infrastructure/storage/mvcc/transaction.go` Commit 方法：

```go
func (tx *SnapshotTx) Commit(ctx context.Context) error {
    // Phase 1: PreCheck（冲突检测）
    // Phase 2: Allocate commitTS
    commitTS := tx.engine.tsGen.NextTS()
    // >>> Phase 3: WAL sync 应插入此处 <<<
    // Phase 3: Apply WriteBuffer
    if err := tx.applyWriteBuffer(ctx, commitTS); err != nil { ... }
}
```

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

```
Tx.Commit:
  1. PreCheck（冲突检测）
  2. WAL.Append(WriteBuffer entries)      ← 新增
  3. WAL.Sync()                            ← 新增（或 group commit）
  4. commitTS = tsGen.NextTS()             ← 移到 Sync 之后
  5. Apply WriteBuffer（写入 B+Tree + VersionChain）
  6. WAL.Append(Commit marker)
```

**关键约束**：commitTS 必须在 WAL sync 后分配。原因：崩溃恢复时，commitTS 是版本可见性判断依据。如果在 sync 前分配，崩溃后 commitTS 已消耗但 WAL 丢失，导致时间戳空洞。

### 3.2 WAL 恢复流程

```
Recovery:
  1. 扫描所有 .wal segment 文件（按 LSN 排序）
  2. 按TxID 分组，只保留有 Commit marker 的事务
  3. 丢弃未提交事务的 entries（Rollback + 无 Commit marker）
  4. 按原始顺序重放已提交事务：
     a. 读取 Key/Value/Type
     b. 调用 BTree.Set / BTree.Delete
     c. 重建 VersionChain（如果 MVCC 开启）
  5. 恢复完成，设置下一个 LSN
```

**恢复策略**：**保守策略**（遇损坏即停），而非当前的"跳过继续"。

理由：事务型 KV 的 WAL 损坏意味着数据可能不一致。跳过损坏记录可能导致部分提交，违反原子性。

### 3.3 WAL 格式增强（基于 UnisonDB 研究）

**当前缺失**：

| 问题 | 风险 | 建议 |
|------|------|------|
| 无 Trailer/结束标记 | 断电后无法区分截断 vs 正常结束 | 增加 `0xDEADBEEF` trailer |
| CRC 未实际计算 | 损坏检测失效 | 实现 CRC32 校验 |
| 无 8-byte 对齐 | 跨扇区撕裂写入风险 | `alignUp(n + 7) & ^7` |

**建议格式**：

```
[CRC32:4][Length:4][LSN:8][Type:1][TxID:8][Timestamp:8][PrevLSN:8]
[KeyLen:4][ValueLen:4][Key:N][Value:M][Padding][Trailer:8]
```

### 3.4 Sync 策略：Group Commit

**推荐演进路径**：`SyncPolicyEveryWrite` → `Group Commit`

Group Commit 工作原理：
1. 多个并发事务的 WAL Append 只写入 OS buffer（不 fsync）
2. 后台 `syncWorker` goroutine 周期性（如 1ms）执行一次 fsync
3. 所有在本次 fsync 前完成 Append 的事务一起被持久化
4. 等待 Sync 的事务通过 channel/block 等待通知

**收益**：fsync 是 WAL 的主要瓶颈（~1ms/次）。Group commit 将 N 次合并为 1 次，吞吐提升 N 倍。

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
| **Watermark GC** | 回收 `commitTS < watermark` 的中间版本 | 实现简单 | 长事务阻塞所有 GC |
| **Eager Pruning** (Steam 论文) | 每次 Prepend 时顺便裁剪 | 链长度有界 | 需要活跃事务快照 |
| **Epoch-based Reclamation** | 全局 epoch 推进后回收 | 安全释放内存 | Go 适配复杂 |
| **混合方案** (推荐) | Watermark + Eager Pruning + EBR 释放 | 各取所长 | 实现复杂度中等 |

### 4.3 推荐方案：Watermark + Eager Pruning

**核心思路**：

```
watermark = min(所有活跃事务的 snapshotTS)
```

任何 `commitTS < watermark` 且**不是链中该 watermark 之前的最新可见版本**的节点可以回收。

**ActiveTxRegistry**（新增）：

```go
type ActiveTxRegistry struct {
    txs sync.Map // txID → snapshotTS
}

func (r *ActiveTxRegistry) Register(txID uint64, snapshotTS uint64)    // BeginTx 时调用
func (r *ActiveTxRegistry) Unregister(txID uint64)                     // Commit/Rollback 时调用
func (r *ActiveTxRegistry) Watermark() uint64                          // GC 时调用
```

**Eager Pruning**（在 Prepend 时顺便裁剪）：

```
Prepend(newNode):
  1. CAS(newNode, oldHead) — 正常追加
  2. watermark = registry.Watermark()
  3. 从 newHead 开始遍历链：
     - 保留：commitTS >= watermark 的节点
     - 保留：commitTS < watermark 但是链中第一个 < watermark 的节点（最新历史版本）
     - 回收：其余节点
  4. 被裁剪的节点放入 limbo bag
```

**安全保证**：
- snapshotGet 使用 `generation` 检测并发修改（已实现）
- 裁剪操作通过 CAS 修改 `next` 指针
- Go GC 保证持有旧节点引用的 goroutine 不会访问已释放内存

### 4.4 后台 GC Goroutine

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

### 4.5 GC 与 WAL 的交互

GC 回收 VersionNode 后需要记录 WAL entry 吗？

**不需要**。原因：
- VersionChain 是纯内存结构，不持久化到 WAL
- WAL 只记录逻辑操作（Key/Value 的 Set/Delete）
- 崩溃恢复时 VersionChain 从 BTree 的 beginTS 重建
- GC 回收的是内存中的链节点，不影响持久化数据

---

## 五、实施路线建议

### 5.1 推荐顺序

```
Step 1: VersionChain GC（独立，不依赖 WAL）
  ↓
Step 2: WAL 集成（依赖 GC 的 Watermark 基础设施）
  ↓
Step 3: Checkpoint（依赖 WAL）
```

**理由**：
1. GC 是**纯内存操作**，不涉及磁盘 I/O，独立性强
2. GC 的 `ActiveTxRegistry` 被 Checkpoint 复用（知道哪些事务活跃）
3. WAL 集成是最大改动，先让 GC 稳定再接入
4. Checkpoint 依赖 WAL 的完整实现

### 5.2 Step 1: VersionChain GC（预估 3-5 天）

**接口变更**：

```go
// 新增：mvcc/active_tx_registry.go
type ActiveTxRegistry struct { ... }

// 修改：mvcc/version_chain.go
func (vc *VersionChain) Prune(watermark uint64) int  // 返回回收节点数

// 修改：mvcc/transaction.go
// BeginTx: registry.Register
// Commit/Rollback: registry.Unregister

// 新增：mvcc/gc.go
type GCConfig struct {
    Interval    time.Duration // GC 周期，默认 5s
    MaxVersions int           // 最大保留版本数，默认 10
    BatchSize   int           // 每次GC处理的 key 数
}
```

**验证标准**：
- [ ] GC 后 VersionChain 长度 ≤ MaxVersions
- [ ] 活跃 SI 事务不受 GC 影响（仍能看到正确快照）
- [ ] 并发 GC + 读写无竞态（`-race` 通过）
- [ ] Watermark 在无活跃事务时正确回收所有旧版本

### 5.3 Step 2: WAL 集成（预估 5-7 天）

**接口变更**：

```go
// 修改：mvcc/transaction.go Commit 方法
//   - 在 applyWriteBuffer 前插入 WAL.Append + WAL.Sync
//   - commitTS 移到 Sync 后分配

// 增强：wal/types.go
//   - CRC32 计算
//   - Trailer 魔数
//   - 8-byte 对齐

// 增强：wal/diskwal.go Recover
//   - 按 TxID 分组
//   - 只重放有 Commit marker 的事务
//   - 保守策略（遇损坏即停）

// 新增：mvcc/wal_integration.go
//   - WriteBuffer → WALEntry 序列化
//   - WALEntry → BTree 操作重放
```

**验证标准**：
- [ ] 写入数据 → kill 模拟 → 恢复后数据完整
- [ ] 未提交事务在恢复后不可见
- [ ] CRC 校验损坏的 WAL 被正确检测
- [ ] Group Commit 吞吐优于 PerWrite Sync

### 5.4 Step 3: Checkpoint（预估 3-5 天）

**接口变更**：

```go
// 新增：storage/checkpoint.go
type CheckpointManager struct { ... }
func (cm *CheckpointManager) FuzzyCheckpoint() error
func (cm *CheckpointManager) SharpCheckpoint() error
func (cm *CheckpointManager) RecoverFromCheckpoint() error
```

**验证标准**：
- [ ] Fuzzy Checkpoint 不阻塞读写
- [ ] Sharp Checkpoint 后 WAL 可截断
- [ ] Checkpoint + WAL 恢复 = 完整数据
- [ ] 定时 Checkpoint 正常触发

---

## 六、风险与开放问题

### 6.1 高风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| GC 裁剪与并发 snapshotGet 竞争 | 数据不一致 | generation 检查 + Go GC 内存安全 |
| WAL sync 性能瓶颈 | 写入吞吐下降 | Group Commit 批量刷盘 |
| Checkpoint 期间脏页追踪 | 实现 BTree 脏页标记 | 考虑 page-level LSN |

### 6.2 开放问题研究成果

#### 问题 1：BTree 脏页追踪

**背景**：Checkpoint 需要知道哪些页面被修改过，才能高效刷脏页。当前 NexKV BTree 无脏页标记。

**三种主流方案对比**：

| 方案 | 原理 | 优点 | 缺点 | 代表实现 |
|------|------|------|------|----------|
| **A. 全量遍历** | Checkpoint 时遍历整棵 BTree，逐页判断是否需要写出 | 无额外内存开销，实现简单 | O(N) 遍历，N=所有页面，Checkpoint 慢 | SQLite 早期版本 |
| **B. 脏页位图** | 修改页面时标记 `dirty=1`，Checkpoint 只刷标记页 | 只写真正脏页，Checkpoint 快 | 需要 MMU/操作系统支持或每页一个 bit | RocksDB、Badger |
| **C. Copy-on-Write** | 页面修改时写入新位置，旧位置保留为快照 | 天然支持并发读取，无写放大 | 内存翻倍风险，需要定期合并 | OrioleDB |
| **D. Page-level LSN** | 每页记录最新修改的 LSN，Checkpoint 时比较 LSN 判断是否需要写出 | 精度高，可增量Checkpoint | 每页额外 8 字节 LSN 字段 | PostgreSQL |

**NexKV 推荐方案：B（脏页位图）+ 混合 A（全量遍历兜底）**

理由：
- NexKV 是纯内存 BTree（`sync.Map` 或自定义 B+Tree），无 MMU 保护层
- 脏页位图实现简单：`map[pageID]bool` 或 `bitset`
- 触发 Checkpoint 时全量遍历 BTree 可作为兜底（简单保守）
- 未来可演进到 Page-level LSN 支持增量 Checkpoint

**建议实现**：

```go
// btree/dirty_tracker.go
type DirtyTracker struct {
    mu    sync.Mutex
    dirty map[pageID]struct{} // pageID → dirty flag
}

func (dt *DirtyTracker) MarkDirty(pid pageID) {
    dt.mu.Lock()
    defer dt.mu.Unlock()
    dt.dirty[pid] = struct{}{}
}

func (dt *DirtyTracker) GetDirtyPages() []pageID {
    dt.mu.Lock()
    defer dt.mu.Unlock()
    pages := make([]pageID, 0, len(dt.dirty))
    for pid := range dt.dirty {
        pages = append(pages, pid)
    }
    return pages
}

func (dt *DirtyTracker) Clear(pids []pageID) {
    dt.mu.Lock()
    defer dt.mu.Unlock()
    for _, pid := range pids {
        delete(dt.dirty, pid)
    }
}
```

**触发时机**：每次 `BTree.Set` / `BTree.Delete` 时调用 `MarkDirty`。

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

**生产系统重建方案对比**：

| 系统 | 重建方式 | 持久化内容 | 重建过程 |
|------|---------|-----------|---------|
| **PostgreSQL** | Undo Log 回滚 | 行版本存于 Heap 页面内 | 从 Heap 读取最新行 → 遍历 Undo chain → 重构可见版本 |
| **MySQL InnoDB** | Rollback Segment | Undo Log + MVCC 链表 | 读取 clustered index → 跟随 MVCC 链表 → 过滤不可见版本 |
| **OrioleDB** | Undo + Copy-on-Write | 页面内 Tuple 版本 | 读取页面 → 跟随 Undo chain → 重构 B-Tree 版本 |
| **LeanXcale** | KV-log + MVCC | Undo Log | 读取 KV → 应用 Undo → 恢复历史版本 |

**NexKV 当前数据布局**：

```
BTree Key → { beginTS, value, flags }
VersionChain（纯内存）→ { commitTS, prevPtr, generation }
```

**VersionChain 重建的三种路径**：

| 路径 | 原理 | 优点 | 缺点 |
|------|------|------|------|
| **A. WAL 全量重放** | 扫描所有 WAL entry，按 TxID 分组，找到所有 Commit 的版本，重建 VersionChain | 完整，不丢数据 | 慢（要扫描全量 WAL），只适合小数据量 |
| **B. BTree-only 重建** | 从 BTree 读取所有 Key 的当前版本（包含 beginTS），构建只含最新版本的简化 VersionChain | 快，无需 WAL | 丢失历史版本（违反 MVCC 快照语义） |
| **C. Checkpoint + WAL Delta（推荐）** | Checkpoint 时持久化 VersionChain 的"快照"（每个 Key 的链头 LSN），恢复时从 Checkpoint + Delta WAL 重放 | 平衡速度和完整性 | 需要 Checkpoint 支持 |
| **D. Undo Log 化** | 每个 VersionNode 写入时同时写 Undo Log，恢复时从 Undo 重建 | PostgreSQL 验证可行 | 需要额外的 Undo 存储 |

**NexKV 推荐：路径 C（Checkpoint + WAL Delta）**

**理由**：
- WAL 全量重放太慢（每次 crash 都要扫描所有 WAL）
- BTree-only 丢失快照隔离能力（Phase 2 的核心价值）
- Checkpoint + Delta 是工业标准做法（PostgreSQL、InnoDB 都用类似机制）

**具体设计**：

```
Checkpoint 时：
  - 对每个活跃 VersionChain，记录链头指针（commitTS + LSN）
  - 将链头指针写入 Checkpoint WAL entry

恢复时：
  1. 加载最近的 Checkpoint，获得每个 Key 的链头 LSN
  2. 从 Checkpoint LSN 之后开始重放 WAL delta
  3. 按 TxID 分组过滤，只重放已 Commit 的事务
  4. 重放过程中重建 VersionChain
```

**简化方案（Phase 3 MVP）**：

由于 Phase 3 Checkpoint 还未实现，**先用路径 B 过渡**：

```go
// 简化恢复：从 BTree 重建 VersionChain（只含最新可见版本）
func RecoverVersionChains(bt *BTree) error {
    iter := bt.NewIterator()
    for iter.SeekFirst(); iter.Valid(); iter.Next() {
        node := iter.Current()
        if node.IsDeleted() { continue }
        // 构建只含最新版本的 VersionChain
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

**此简化方案的限制**：只能恢复最新版本，**丢失历史快照**。这是 MVP 可接受的权衡，因为 Phase 3 主要目标是崩溃恢复可用，不是完整 MVCC 历史。

**完整路径 C 的前置依赖**：Checkpoint 实现（Phase 3 Step 3）

---

### 6.3 开放问题决策汇总

| 问题 | 结论 | 阻塞 Phase 3？ | 备注 |
|------|------|--------------|------|
| BTree 脏页追踪 | 脏页位图 + 全量遍历兜底 | 否（Checkpoint 独立 Step） | 建议在 Step 3 实现 |
| WAL logical vs physical | 保持 Logical-with-redo-info | 否 | 与 MVCC 语义一致 |
| 分布式 WAL | 远期问题（Phase 4+） | 否 | 当前 MVP 只保证单机原子性 |
| VersionChain 恢复 | Checkpoint + WAL Delta（远期）；MVP 用 BTree-only | 否（Checkpoint 独立 Step） | 简化方案可接受 |

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

**文档版本**: v1.1
**创建日期**: 2026-04-17
**最后更新**: 2026-04-17
**更新内容**: 补充 4 个开放问题研究成果（脏页追踪、WAL 类型、分布式 WAL、VersionChain 恢复）
**状态**: Draft — 等待评审后决定实施顺序
