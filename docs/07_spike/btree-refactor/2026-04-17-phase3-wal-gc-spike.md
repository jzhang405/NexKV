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

> ⚠️ **Phase 3 范围声明**（第二轮审核 C6）：
>
> Phase 3 WAL + GC **仅用于单节点场景**，以下为明确的分布式限制：
>
> | 限制 | 说明 | 远期方案 |
> |------|------|---------|
> | commitTS 单调性 | `TSGenerator` 使用本地 `atomic.Uint64` 计数器，不保证跨节点全局单调 | ⚠️ **第五轮审核 C4**：高 16 位 nodeID 编码**不能独立建立全局排序**——nodeID 主导下 localCounter 在跨节点比较时几乎无意义。**必须依赖 HLC**（Hybrid Logical Clock）建立跨节点 causal order：低 48 位 = HLC 的 physical+logical 部分，高 16 位 = nodeID（冲突优先级，非排序依据）。详见 Issue-5：commitTS HLC 演进 |
> | 事务范围 | 当前 MVP 事务只能操作同一分片内的 key，跨分片 key 返回错误（第五轮 M4：**单节点部署**下为事务路由层面的逻辑检查；**分布式部署**下为物理限制——跨节点 RTT + 网络故障） | Phase 4+: 2PC + WAL |
> | Checkpoint | Phase 3 Checkpoint 仅保证单节点恢复一致性 | Phase 4: 分布式 Checkpoint 协调协议 |
> | GC Watermark | `ActiveTxRegistry.Watermark()` 仅反映本节点活跃事务 | Phase 4: Global Watermark 协议（Gossip 交换各节点 Watermark） |

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
- ⚠️ **当前为单文件 WAL**（`SegmentSize` 配置存在但未使用），**Phase 3 Step 2 必须实现 Segment 轮转**——当前 `Truncate()` 按文件粒度删除，单文件模式下会误删后续 entries，Truncate 不可用
- CRC32 校验（⚠️ 当前使用 IEEE 多项式，建议替换为 Castagnoli/CRC32C）
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

> ⚠️ **术语说明**：BTree MVCC 编码中的 `beginTS` 字段（`codec.go: [Flag][beginTS][RealVal]`），在 `commitKey` 执行 BTree.Set 时被赋值为事务的 `commitTS`。Recovery 文档中提到的"BTree 的 beginTS"和"commitTS"是同一值的不同视角。

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
  6. Apply WriteBuffer（⚠️ 执行顺序：VersionChain.Prepend → BTree.Set，见第四轮 NEW-2）
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
     b. ⚠️ 三阶段幂等检查：beginTS > commitTS → 跳过；beginTS == commitTS → VersionChain 存在性检查；beginTS < commitTS → 完整重放（见 Section 6.2 问题 4）
     c. 调用 BTree.Set / BTree.Delete（携带 commitTS）
     d. 重建 VersionChain（如果 MVCC 开启）
  5. 恢复完成，设置下一个 LSN
```

**恢复策略**：**保守策略**（遇损坏即停），而非当前的"跳过继续"。

> ⚠️ **恢复性能基线**（第五轮审核 H2）：Step 3（Checkpoint）完成前 WAL 无法 Truncate，Recovery 必须全量扫描。估算基线：30s Checkpoint 间隔、1 万 TPS、128B/entry → 约 **38MB/周期** WAL 体积。按 TxID 分组需全量加载内存，大并发场景（10 万并发 Tx）下分组 map 的内存和 GC 压力不可忽略。建议 Step 2 实现流式分组——边扫描边处理已确定提交的事务 entries，减少内存峰值。

> ⚠️ **"保守策略"是拟改进**：当前 `Recover()` 实现是"跳过继续"（`wal/diskwal.go` MVP 限制）。Step 2 计划修改为遇损坏即停。

**⚠️ commitTS 重建机制**（三专家审核：必须修复）：

WriteBuffer entries 本身不含 commitTS，commitTS 来自 **Commit marker**。Commit marker 的 Key 区域存放 CommitTS（`KeyLen=8, ValueLen=0`，Key = 8 字节大端编码）。

**Recovery 重建 VersionChain 流程**（路径 C — 三专家审核采纳）：

```
1. 扫描所有 .wal segment → 按 LSN 排序
   ⚠️ H5（第五轮审核）：恢复期间需验证 key 所属分片是否仍由本节点负责。
   Phase 4 扩展：在 WALEntry 中预留 ShardID 字段，或确认 key 已包含分片路由信息（如前缀编码）。
   当前单节点场景下跳过此检查。
   ⚠️ C4 修复：当前 scanWALDirectory() 使用 os.ReadDir() 遍历，
   跨文件 entries 不保证全局 LSN 顺序。
   必须在收集所有 entries 后按 LSN 排序（sort.Slice），
   或先按文件名 LSN 排序再逐文件读取。
   这是 Step 2 的阻塞项。
2. 按 TxID 分组：
   - 有 Commit marker → 已提交事务
   - 无 Commit marker 或有 Rollback → 丢弃
3. 对已提交事务，从 Commit marker 提取 commitTS
4. 按 LSN 顺序重放已提交事务的 WriteBuffer entries：
   a. 同一 TxID 的 entries 共享 commitTS
   b. BTree.Set/Delete（携带 commitTS）
   c. 按 commitTS 重建 VersionChain（保证 SI 语义）
   d. ⚠️ 幂等性保护：重放前检查目标 key 的 beginTS 是否等于 commitTS（见 C1）
5. 清理 .wal.deleting 残留文件
6. 设置下一个 LSN
```

**⚠️ 直接采用路径 C**（三专家审核一致）：放弃路径 B（BTree-only 重建），因为路径 B 只保留最新版本，崩溃恢复后快照隔离语义被违反。详见 Section 6.2 问题 4。

> ⚠️ **commitKey 执行顺序变更**（第四轮 NEW-2 — 三专家审核）：
>
> 将 `commitKey` 的执行顺序从 **Set→Prepend** 改为 **Prepend→Set**。
>
> **原因**：Set-before-Prepend 在 half-Apply 场景下（BTree.Set 完成、Prepend 未完成），BTree 中的旧值已被新值覆盖，Recovery 无法推导 Prepend 所需的 OldValue。改为 Prepend-before-Set 后，half-Apply 变为"Prepend 完成、BTree.Set 未完成"——Recovery 只需重放 BTree.Set（幂等），VersionChain 节点已存在且正确。
>
> **Prepend 失败回滚**：如果 Prepend 成功但 BTree.Set 失败，VersionChain 中会多一个"孤儿节点"（commitTS 对应的版本）。但这个孤儿节点不影响正确性——snapshotGet 遍历时会看到这个节点，但 BTree 中的值仍是旧值（beginTS < commitTS），Recovery 重放 BTree.Set 后状态恢复一致。Prepend 的 CAS 去重（commitTS 检查）确保 Recovery 不会重复 Prepend。
>
> ⚠️ **孤儿节点累积风险**（第五轮审核 M3）：孤儿节点的 `commitTS > watermark`，GC 不会回收。高频写入场景下孤儿节点持续累积，增加 snapshotGet 遍历开销。建议在 Prepend 清理逻辑中增加孤儿节点检测——清理时检查 BTree 中对应 key 的 beginTS 是否匹配 commitTS，若不匹配说明是孤儿节点，可直接清理。
>
> **源码参考**：当前 `commitKey`（transaction.go:499-535）执行顺序为 Set→Prepend，Phase 3 需调换。KeyLock 保证同一 key 不会并发 commitKey，Prepend-before-Set 不引入新的竞争条件。

### 3.3 WAL 格式增强（基于 UnisonDB 研究）

**当前缺失**：

| 问题 | 风险 | 建议 |
|------|------|------|
| 无 Trailer/结束标记 | 断电后无法区分截断 vs 正常结束 | 增加 `0xDEADBEEF` trailer |
| ~~CRC 未实际计算~~ | ~~损坏检测失效~~ | ~~实现 CRC32 校验~~ — **已实现**（`types.go:147`） |
| 无 8-byte 对齐 | 跨扇区撕裂写入风险 | `alignUp(n + 7) & ^7` |
| **Commit marker 不含 commitTS** | **Recovery 无法重建 commitTS** | **Type=Commit 时 KeyLen=8，Key 区域存 CommitTS 大端编码** |

**⚠️ Length 字段用途说明**：`Length:4` 放在 CRC 之后、LSN 之前，用于**变长 entry 的自我描述**。读取时先读 Length 跳到下一个 entry（跳跃扫描），用于损坏后快速定位下一条记录。

> ⚠️ **跳跃扫描模式**（第五轮审核 H3）：损坏 entry 的 CRC 校验必然失败，但跳跃扫描需要**先于 CRC 验证**读取 Length 来猜测下一条边界。正确的"乐观猜测 + CRC 验证"模式：
> 1. 当前 entry CRC 失败 → 直接读取 Length（不做 CRC）
> 2. 根据 Length 跳到候选的下一条 entry 位置（对齐到 8 字节）
> 3. 尝试验证候选 entry 的 CRC — 若通过则继续扫描
> 4. 若失败，尝试下一个 8 字节对齐位置作为候选

**建议格式**（三专家审核修正）：

> ⚠️ **Phase 3 格式升级声明**（N4 — 第三轮审核）：
>
> Phase 3 WAL 格式是**破坏性升级**，与 Phase 2 WAL 文件**不兼容**。差异包括：
> 1. 新增 `Length:4` 字段（CRC 之后）
> 2. 新增 `Padding`（8 字节对齐）和 `Trailer`（4 字节 `0xDEADBEEF`）
> 3. CRC 覆盖范围从 `buf[4:]`（LSN 开始）改为从 `Length` 字段开始
>
> **兼容性决策**：Phase 2 是纯内存引擎，**无 WAL 持久化需求**（`DiskWAL` 的 `.wal` 文件在进程退出后即可删除）。Phase 3 升级时无需实现旧格式检测/迁移。
> 如需防御性编程，可在 segment 文件头增加 **Magic Number**（如 `0x4E584B33` = "NXK3"），Recovery 时检测并拒绝无法识别的格式。

```
[CRC32:4][Length:4][LSN:8][Type:1][TxID:8][Timestamp:8][PrevLSN:8]
[KeyLen:4][ValueLen:4][Key:N][Value:M][Padding:0~7][Trailer:4]
```

- **CRC 覆盖范围**：从 `Length` 字段开始到 `Padding` 结束（**含 Length**，不含 CRC 本身和 Trailer）。这确保 Length 损坏时可被 CRC 检测，避免跳跃扫描定位错误。
- `Padding`：对齐到 8 字节，公式 `paddedLen = (totalLen + 7) &^ 7`，其中 `totalLen` 不含 CRC 和 Trailer。Padding 长度 = `paddedLen - totalLen`。
- `Trailer`：**4 字节** `0xDEADBEEF`（修正：原文档错误标记为 8 字节，但 `0xDEADBEEF` 是 32 位值）。用于检测截断。若写入中断电，Trailer 不完整，此时 CRC 校验失败。
- **`Type=Commit` 时**：`KeyLen=8, ValueLen=0`，Key 区域存放 `CommitTS` 的 8 字节大端编码。**修正**：原文档错误标记为 `KeyLen=0`，8 字节 CommitTS 无法放入长度为 0 的区域。
- **`PrevLSN`**：用于完整性校验——验证 entry N 的 `PrevLSN == entry N-1` 的 LSN，确保链式连续性。同时预留与未来分布式 WAL 模式的兼容性（分布式场景下 LSN 排序需要链式校验）。

> ⚠️ **Trailer 检测逻辑**：读取 entry 后，检查最后 4 字节是否为 `0xDEADBEEF`。若不是，说明 entry 不完整（写入中断）。此检查在 CRC 校验之后执行。

**⚠️ WALEntry 是否携带 OldValue/OldFlag（N3 — 第三轮审核决策）**：

Recovery 重建 VersionChain 时，需要为每个 key 创建正确的历史版本链。核心问题是 WALEntry 是否需要存储旧值：

| 方案 | 优点 | 缺点 |
|------|------|------|
| **A. WALEntry 携带 OldValue/OldFlag** | Recovery 可完全重建 VersionChain，无需依赖 BTree 状态 | WAL entry 体积翻倍（每个 Update 写两份值），增加 I/O |
| **B. Recovery 时从 BTree 当前状态推导** | WAL entry 保持轻量，无额外 I/O | half-Apply 场景下 BTree 已被更新，旧值丢失；但两阶段幂等检查（N1）可跳过已 Apply 的 key |

**采纳方案 B（Recovery 时从 BTree 推导）**，理由：
1. **两阶段幂等检查保证安全性**：N1 的两阶段检查确保 half-Apply 的 key 会被正确跳过（BTree+VersionChain 都已完成）或仅补充 Prepend（BTree 已完成但 VersionChain 未完成——此时 BTree 的旧值就是正确的 Prepend 输入）
2. **WAL 体积敏感**：Phase 3 目标是轻量级单节点部署，WAL entry 翻倍的 I/O 开销不可接受
3. **Insert 场景天然安全**：Insert 没有"旧值"，Prepend 的旧值就是 `nil`（key 不存在本身就是 ErrKeyNotFound 语义）

具体实现：`ToWALEntries(commitTS)` 只写入新值（NewValue/NewFlag/Type），Recovery 重放时：
- 对于未 Apply 的 key：先 `BTree.Get(key)` 获取当前值作为 OldValue，然后 `BTree.Set` 写入新值，最后 `Prepend(OldValue, OldFlag)` 重建 VersionChain
- 对于已 Apply 的 key：两阶段幂等检查跳过

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

> ⚠️ **第二轮审核（H2/H3/H5）**：以上代码片段存在以下问题，**Group Commit 需要独立设计文档**：
>
> 1. **LSN 乱序风险**（H5）：`atomic.AddUint64` 分配 LSN 与 Mutex 保护的文件写入之间存在窗口——两个 goroutine 可能按不同 LSN 顺序获得锁，导致 WAL 文件中 LSN 乱序。推荐方案：channel-based 单一写入 goroutine（LSN 在写入时分配，保证顺序）
> 2. **syncWorker 生命周期缺失**（H3）：syncWorker 由谁启动、如何退出、panic 后等待者如何唤醒——全部未描述。syncWorker 异常退出会导致所有等待 Sync 的事务 goroutine 永久阻塞
> 3. **commitTS 归属矛盾**（M6）：Section 3.4 由 WAL 层分配 `wal.nextCommitTS()`，但 Section 5.3 由事务层分配 `tx.engine.tsGen.NextTS()`——应统一为事务层分配，WAL 层只负责存储

```go
// ⚠️ 以下为原理示意，具体实现需在 Group Commit 设计文档中定义
// commitTS 统一由事务层 tsGen.NextTS() 分配（不在 WAL 层）
// LSN 在单一写入 goroutine 中分配（保证文件内 LSN 顺序）
```

Group Commit 只控制 **fsync 时机**，不改变 commitTS 分配顺序。

**⚠️ Group Commit Sync 容错**（H4）：syncWorker 需 `recover()` 保护防止 panic 导致等待者永久阻塞。等待 Sync 的事务使用 `select + ctx.Done()` 处理取消。

**⚠️ syncWorker goroutine 生命周期**（第五轮审核 C5 — Group Commit 独立设计文档必须覆盖）：

```go
type syncWorker struct {
    ch     chan *syncRequest    // 事务提交 sync 请求
    done   chan struct{}        // 优雅退出通知
}

func (w *syncWorker) run(ctx context.Context) {
    // 启动者：TransactionEngine.Init() 中 go w.run(ctx)
    defer func() {
        if r := recover(); r != nil {
            w.broadcastError(fmt.Errorf("syncWorker panic: %v", r))
            // 必须广播错误给所有等待者，防止永久阻塞
        }
    }()
    for {
        select {
        case req := <-w.ch:
            w.processBatch(req)  // fsync 批量刷盘
        case <-ctx.Done():
            w.drainWithError(ctx.Err())  // 优雅退出，唤醒所有等待者
            return
        }
    }
}
```

Key 设计约束：
- **启动者**：`TransactionEngine.Init()` 中 `go sw.run(ctx)` — 保证在引擎接受请求前完成
- **退出路径**：监听 `ctx.Done()`，退出前 drain 所有待处理请求并广播错误
- **panic 恢复**：`recover()` 后 `broadcastError()` 唤醒所有等待者，防止 goroutine 泄漏
- **等待者超时**：`select { case <-sw.syncCh: ...; case <-ctx.Done(): ... }`

### 3.5 Checkpoint 设计

**分层策略**：

| 场景 | 策略 | 说明 |
|------|------|------|
| 在线运行 | Fuzzy Checkpoint | 后台 goroutine 周期性刷脏页 |
| 正常关闭 | Sharp Checkpoint | 暂停写入，刷全部脏页 + TRUNCATE WAL |
| 分布式快照 | Sharp Checkpoint | 作为快照基准 |

**Fuzzy Checkpoint 流程**：

```
1. rootRef = atomic.LoadPointer(&btree.root)     ← 固定 root 快照（C1：防止遍历期间 Split 更新 root）
2. 记录 checkpointStartLSN
3. 基于固定 rootRef DFS 遍历 BTree 活跃路径，逐页写入主存储
4. 记录 checkpointEndLSN
5. 写入 Checkpoint WAL entry
6. Truncate LSN < checkpointEndLSN 的 WAL segments
```

> ⚠️ **Atomic Root Snapshot 必要性**（第五轮审核 C1）：DFS 遍历过程中，CAS-based COW B+Tree 的 root pointer 可能因 Split 传播被 CAS 更新。`atomic.LoadPointer` 获取固定 rootRef 确保遍历基于一致的 BTree 快照。COW 保证旧 root 子树不被就地修改（LMDB/BoltDB 经典做法），checkpointStartLSN 之后的增量变更由 WAL 重放补偿。

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

**分布式 Checkpoint 协调**（第五轮审核 M5）：Phase 4 分布式场景下，Checkpoint 截断需要全局一致的时间点——所有节点的 LSN 截断点必须一致。思路：基于 Global Watermark 协议选择全局最小 LSN 作为截断基准。Phase 3 不涉及。

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
| **Mark-and-Sweep** (采纳) | GC 只标记节点为 `reclaimed`，不修改链拓扑；`snapshotGet` 跳过已标记节点；`Prepend` 时顺便清理 | **无 data race**，热路径开销极低，侵入性最低 | 被标记节点仍占内存直到 Prepend 清理 |
| **Copy-on-Prune** | CAS 替换整个链头，新链是缩短后的版本 | 旧链自然不可达 | 每次需重建缩短后的链 |
| **per-chain RWMutex** | GC 持写锁，Prepend/snapshotGet 持读锁 | 实现简单 | 热路径增加 RWMutex 开销 |
| **Epoch-based Reclamation** | 全局 epoch 推进后回收 | 安全释放内存 | Go 适配复杂 |

**采纳方案：Mark-and-Sweep（三专家审核一致推荐）**

理由：
1. **唯一不破坏并发遍历安全**的方案——当前 `VersionNode.next` 是非 atomic 的 `*VersionNode`，直接修改会与 `snapshotGet` 的无锁遍历形成数据竞争（Go 未定义行为）
2. **热路径几乎无损耗**——`snapshotGet` 只需增加 `node.reclaimed.Load()` 检查（atomic.Bool Load 在 x86 上编译为普通 MOV）
3. **符合 VersionChain 不可变设计哲学**——链的拓扑结构在 GC 期间不变，只有 Prepend（已有 CAS 保护）时才清理
4. **Tombstone 安全**——GC 保留规则确保删除标记不会被误回收

### 4.3 ActiveTxRegistry + Watermark GC

**核心思路**：

```
watermark = min(所有活跃事务的 snapshotTS)
```

任何 `commitTS < watermark` 且**不是链中该 watermark 之前的最新可见版本**的节点可以回收。

**⚠️ txID 来源**：`txID` 由 `txManager` 统一分配（`txIDCounter atomic.Uint64`），在 `BeginTx` 时生成。`ActiveTxRegistry` 不维护独立计数器，接受外部传入的 txID。`SnapshotTx` 需新增 `txID uint64` 字段。

**ActiveTxRegistry**（采纳方案：`sync.Mutex` 保护普通 `map`，三专家审核一致）：

```go
type ActiveTxRegistry struct {
    mu  sync.Mutex
    txs map[uint64]uint64 // txID → snapshotTS（Mutex 保护）
}

func (r *ActiveTxRegistry) Register(txID uint64, snapshotTS uint64) {  // BeginTx 时调用
    r.mu.Lock()
    r.txs[txID] = snapshotTS
    r.mu.Unlock()
}

func (r *ActiveTxRegistry) Unregister(txID uint64) {                   // Commit/Rollback 时调用
    r.mu.Lock()
    delete(r.txs, txID)
    r.mu.Unlock()
}

func (r *ActiveTxRegistry) Watermark() uint64 {                        // GC 时调用
    r.mu.Lock()
    defer r.mu.Unlock()
    if len(r.txs) == 0 { return 0 }
    min := ^uint64(0)
    for _, ts := range r.txs {
        if ts < min { min = ts }
    }
    return min
}
```

**弃用 `sync.Map` 的理由**（三专家审核）：
1. `sync.Map` 优化"读多写少"场景，但 Register/Unregister 是高频写操作（每个事务 Begin/Commit 各一次）
2. `sync.Map.Range` 文档明确声明不保证看到并发的 `Store`，导致 Register/Watermark 之间无 happens-before 保证
3. `sync.Mutex` 的 Lock/Unlock 提供 happens-before 保证：Watermark 在 Lock 时，所有在 Lock 之前完成的 Register 操作对 Watermark 可见

**⚠️ Global Watermark 分布式约束**（第五轮审核 H4）：当前 `Watermark()` 仅反映本节点活跃事务，扩展到分布式时需考虑：
- **安全条件**：一个节点只能在确认**所有节点**的 watermark 都大于该版本 commitTS 后才能回收该版本。即：`safeGC_ts = min(all_nodes_watermarks)`，而非 `min(local_watermark)`
- **Gossip 传播延迟**：Gossip 保证最终一致的 watermark 信息，但 GC 是周期性的。传播延迟可能超过 GC 周期，导致节点回收被其他节点仍需的版本
- **接口预留**：`ActiveTxRegistry` 预留 `SetRemoteWatermark(nodeID, watermark)` 方法（Phase 4 实现），当前留空

**⚠️ Register 必须在 BeginTx 时调用**（三专家审核一致）：

如果 Register 在 Commit 时调用，长事务从 BeginTx 到 Commit 的整个生命周期内不被 GC 感知，其需要的版本可能被回收。

```go
// BeginTx 中：
txID := tm.txIDCounter.Add(1)
tm.activeTxRegistry.Register(txID, snapshotTS)

// Commit/Rollback 中（defer 保护确保 panic 也执行）：
// Commit 中：
defer tm.activeTxRegistry.Unregister(tx.txID)

// Rollback 中（C3 修复：同样 defer 保护）：
defer tm.activeTxRegistry.Unregister(tx.txID)
```

> ⚠️ **双重 Unregister 安全性**（C3 修复说明）：Commit 和 Rollback 不会同时执行（`completed` CAS 保护），但 Rollback 的 defer 确保 panic 时也能 Unregister。`delete(map, key)` 对不存在的 key 是 no-op，即使 Commit 和 Rollback 路径都被触发也不会 panic。

### 4.4 后台 Mark-and-Sweep Pruning（采纳方案）

```
PruneBackground(watermark):
  1. 获取 VersionStore.chains 的 snapshot（sync.Map Range）
     ⚠️ M2: Range 不保证看到并发 Store 的 key，新 key 可能被跳过一次 GC 周期——不影响正确性（新链只有 head），但可能使一次周期内版本数临时超 MaxVersions
  2. 对每个 key 的链：
     a. 遍历链，跳过链头（始终保留）
     b. 找到 watermark 之前的最新可见版本（含其后被 Tombstone 遮盖的所有非 Tombstone 版本）→ 保留（详见下方 GC 保留规则）
     c. 更老的中间节点：node.reclaimed.Store(true)（仅标记，不修改 next）
     d. ⚠️ Prune 完成后必须 chain.generation.Add(1)（确保 snapshotGet 的乐观一致性校验能检测到链的逻辑修改）
  3. Prepend 时顺便清理：从链头开始剔除连续的 reclaimed 节点（⚠️ 只修改 head CAS，不修改任何 VersionNode.next 指针——这是并发安全的保证）
     ⚠️ **清理范围约束**（第五轮审核 M7）：NEW-6 的"扩展清理"**仅限于从链头开始的连续 reclaimed 段**。对于链中间的 reclaimed 节点（如 V3 存活、V2 已回收、V1 存活），物理断开需要修改 V3.next 指针，这与"不修改 next"的根本约束冲突。深度 reclaimed 节点通过 generation bump 触发的 snapshotGet 重试来间接容忍。
```

**GC 保留规则（三专家审核：H1 Tombstone 不复活 + 第二轮审核 H1 修正）**：

> ⚠️ **第二轮审核发现（H1）**：原规则"只保留 watermark 前最新可见版本"不充分。当最新可见版本是 Tombstone 时，被它"遮盖"的更老非 Tombstone 版本不能被回收——否则 snapshotTS < Tombstone.commitTS 的活跃事务会错误地看到 ErrKeyNotFound。

**修正后的保留规则**：
1. 链头始终保留
2. watermark 前的**最新可见版本**保留（含 Tombstone）
3. 如果最新可见版本是 Tombstone，**从该 Tombstone 向前回溯到第一个非 Tombstone 可见版本也必须保留**
4. 更老的版本可标记 reclaimed

```
VersionChain: head → V5(500) → V4(400,Tombstone) → V3(300) → V2(200) → V1(100)

场景 A — watermark=450：
  - V5(500): 链头，始终保留
  - V4(400,Tombstone): watermark 前最新可见版本，保留（防止 key 复活）
  - V3(300): 可标记 reclaimed（无活跃事务 snapshotTS < 400 需要 V3 之前的值）
  - V2(200): 可标记 reclaimed
  - V1(100): 可标记 reclaimed

场景 B — watermark=250（有 snapshotTS=300 的活跃事务）：
  - V5(500): 链头，始终保留
  - V4(400,Tombstone): 保留（watermark 前最新可见版本）
  - V3(300): 保留 — 虽然 V3 的 commitTS >= watermark，但 Tombstone V4 遮盖了 V3。snapshotTS 在 250-300 之间的事务需要 V3 作为可见版本（V4 的 commitTS=400 > snapshotTS，不可见）
  - V2(200): 保留 — 如果 snapshotTS 在 200-250 之间，V2 是它们的可见版本（V3 commitTS=300 > snapshotTS，不可见）
  - V1(100): 可标记 reclaimed（V2 已是 watermark 前的可见版本，V1 更老无需保留）
```

> ⚠️ **跨节点可见性**（第五轮审核 M6）：当前 GC 保留规则仅基于本节点 ActiveTxRegistry 的 watermark。扩展到分布式后，节点 A 无法看到节点 B 上的活跃事务——如果节点 B 有 snapshotTS=300 的事务读取 key k，节点 A 的 GC 可能错误回收 V3(300)。修正为：**跨节点场景下必须基于 Global Watermark 协议**的 min(all_nodes_watermarks)。当前单节点场景此规则成立。

**snapshotGet 配合修改**：

```go
// 遍历时增加 reclaimed 检查
for node != nil {
    if node.reclaimed.Load() || node.rolledBack.Load() {
        node = node.next
        continue  // 跳过已回收或已回滚节点
    }
    // ... 原有逻辑
}
```

**⚠️ Prune 必须递增 generation（C2 — Go 并发专家审核）**：

`snapshotGet`（`transaction.go:211-233`）使用乐观一致性校验：遍历前后比较 `chain.Generation()`，如果 generation 变化则重试。Prune 标记 reclaimed 改变了链的"逻辑可见性"，但如果不递增 generation，snapshotGet 无法检测到这一变化，可能漏掉本应可见的版本——**直接违反快照隔离语义**。

> ⚠️ **Prune 与 snapshotGet 并发的安全性论证**（第四轮 NEW-7）：
> Prune 标记 reclaimed 的节点都是 `commitTS < watermark` 的节点。所有活跃事务的 `snapshotTS >= watermark`（Watermark 是最小活跃 snapshotTS）。因此被 Prune 回收的节点的 `commitTS < watermark <= snapshotTS`，这些节点不满足 snapshotGet 的 `commitTS > snapshotTS` 可见性条件——**snapshotGet 根本不会选择这些节点作为 bestNode**。即使 generation 检查窗口内 snapshotGet 读到了 Prune 的"中间状态"（部分节点已标记 reclaimed），结果仍然正确。
>
> **内存序推理链**（第五轮审核 H7）：`snapshotGet` 依赖于以下 happens-before 链来保证看到 Prune 的 `reclaimed.Store(true)`：
> ```
> Prune goroutine: reclaimed.Store(true) → generation.Add(1)
>                                             ↑ program order
> snapshotGet:     generation.Load()     → reclaimed.Load()
>                                             ↑ program order
> ```
> `generation.Add(1)` 是一个 `sync/atomic` 递增操作。当 `snapshotGet` 的 `generation.Load()` 观察到 `Add(1)` 写入的值时，通过 happens-before 传递性，`reclaimed.Store(true)` 对后续的 `reclaimed.Load()` 可见。这个推理链正确，但**修删其中任一 atomic 操作都会破坏可见性**。

```go
// Prune 完成后递增 generation
func (vc *VersionChain) Prune(watermark uint64) int {
    // ... 标记 reclaimed 节点 ...
    vc.generation.Add(1)  // 确保 snapshotGet 检测到链的逻辑修改
    return marked
}
```

### 4.5 后台 GC Goroutine

```go
func (tm *TransactionEngine) runGC(ctx context.Context) {
    ticker := time.NewTicker(tm.config.GCInterval) // 默认 5s
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            watermark := tm.activeTxRegistry.Watermark()
            if watermark == 0 {
                // ⚠️ N2 修正：无活跃事务时恰恰是最需要 GC 的时刻。
                // 使用当前 TS 作为 watermark，相当于"所有版本都可被回收（保留链头+规则要求版本）"。
                // 需要在 TSGenerator 接口新增 CurrentTS() uint64 方法（只读，不递增）。
                watermark = tm.tsGen.CurrentTS()
            }
            tm.pruneVersionChains(ctx, watermark) // 传入 ctx，支持优雅退出
        case <-ctx.Done():
            return
        }
    }
}

// pruneVersionChains 传入 ctx，在 Range 回调中检查退出信号
func (tm *TransactionEngine) pruneVersionChains(ctx context.Context, watermark uint64) {
    tm.versionStore.chains.Range(func(key, value any) bool {
        select {
        case <-ctx.Done():
            return false // 提前退出
        default:
        }
        chain := value.(*VersionChain)
        chain.Prune(watermark)
        return true
    })
}
```

### 4.6 节点回收机制（简化设计）

**采纳方案：直接让节点不可达，Go GC 自然回收**

Mark-and-Sweep 方案下，被标记为 `reclaimed` 的节点仍在链中（`next` 未修改），但 `snapshotGet` 会跳过它们。当 `Prepend` 清理链头连续的 reclaimed 节点时，这些节点从链中物理断开，变为不可达，由 Go GC 自动回收。

**无需 Limbo Bag / channel**（三专家审核一致）：
- Go GC 已足够安全高效，不需要手动管理节点生命周期
- channel 持有引用反而阻止 Go GC 回收，造成内存泄漏
- Prepend 的 CAS 保护确保清理操作的并发安全

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
    mu  sync.Mutex
    txs map[uint64]uint64 // txID → snapshotTS（Mutex 保护）
}

func (r *ActiveTxRegistry) Register(txID uint64, snapshotTS uint64)    // BeginTx 时调用
func (r *ActiveTxRegistry) Unregister(txID uint64)                      // Commit/Rollback 时调用
func (r *ActiveTxRegistry) Watermark() uint64                           // GC 时调用

// 修改：mvcc/version_chain.go
// VersionNode 新增 reclaimed atomic.Bool 字段
type VersionNode struct {
    commitTS   uint64
    value      []byte
    flag       byte
    rolledBack atomic.Bool
    reclaimed  atomic.Bool  // Phase 3 新增：GC 标记
    next       *VersionNode
}

func (vc *VersionChain) Prune(watermark uint64) int  // 后台任务调用：标记 reclaimed，不修改 next

// 新增：mvcc/gc.go
type GCConfig struct {
    Interval    time.Duration // GC 周期，默认 5s
    MaxVersions int           // 最大保留版本数，默认 10
    BatchSize   int           // 每次GC处理的 key 数
}

func (tm *txManager) runGC(ctx context.Context)                     // 后台 goroutine
func (tm *txManager) pruneVersionChains(ctx context.Context, watermark uint64)  // 遍历所有 chains，传入 ctx

// 修改：mvcc/ts_generator.go
// 新增 CurrentTS() 方法（只读当前值，不递增），用于无活跃事务时 GC 的 watermark fallback
func (g *LocalTS) CurrentTS() uint64  // return g.counter.Load()

// 新增：mvcc/codec.go（第五轮审核 H1）
// 三阶段幂等检查的 BTree 部分需要读取当前值的 beginTS。
// Recovery 流程调用 BTree.GetWithMeta(key) 获取 value + beginTS（即 commitTS）。
func (bt *BTree) GetWithMeta(key []byte) (value []byte, beginTS uint64, err error)
// Wire format: [Flag:1][beginTS:8][RealValue:N]，extractBeginTS() 从 value bytes 中解码 beginTS
```

> ⚠️ **关键新增**：ActiveTxRegistry 在 `BeginTx` 时 `Register(txID, snapshotTS)`，在 `Commit/Rollback` 时 `Unregister(txID)`（defer 保护）。`txID` 由 `txManager.txIDCounter` 统一分配，不维护独立计数器。

**验证标准**：
- [ ] GC 后 VersionChain 长度 ≤ MaxVersions（充分条件非必要）
- [ ] 活跃 SI 事务不受 GC 影响（仍能看到正确快照）
- [ ] 并发 GC + 读写无竞态（`-race` 通过）
- [ ] Watermark 在无活跃事务时正确回收所有旧版本
- [ ] **新增**：长事务存在时，GC 正确保留旧版本（不被阻塞回收）

### 5.3 Step 2: WAL 集成（预估 5-7 天）

**⚠️ 关键接口变更**（相较于原计划）：

```go
// 修改：wal/types.go WALEntry — Type=Commit 时 CommitTS 编码到 Key 字段
// Wire format: KeyLen=8, ValueLen=0, Key = CommitTS 8字节大端编码
// 其他 Type 的 WALEntry 格式不变（Key/Value 正常使用）

// 修改：mvcc/transaction.go BeginTx 方法
// ⚠️ NEW-3 修复：NextTS + Register 在同一 Mutex 临界区内完成，
// 消除 GC goroutine 在两者之间的窗口内使用过期 Watermark 的风险。
// 代价：将 NextTS 放入 Mutex，但 Mutex 只保护 Register/Unregister/Watermark（非热路径），性能影响可忽略。
func (tm *txManager) BeginTx(ctx context.Context, level IsolationLevel) (Tx, error) {
    tm.activeTxRegistry.mu.Lock()
    snapshotTS := tm.tsGen.NextTS()
    txID := tm.txIDCounter.Add(1)                  // txID 统一来源
    tm.activeTxRegistry.txs[txID] = snapshotTS    // 直接写入，避免二次 Lock
    tm.activeTxRegistry.mu.Unlock()

    tx := &SnapshotTx{
        engine: tm, snapshotTS: snapshotTS, txID: txID,
    }
    return tx, nil
}

// 修改：mvcc/transaction.go Commit 方法
func (tx *SnapshotTx) Commit(ctx context.Context) error {
    // 注意：Register 已在 BeginTx 时完成
    defer tx.engine.activeTxRegistry.Unregister(tx.txID)

    commitTS := tx.engine.tsGen.NextTS()           // 分配 commitTS（在 WAL Append 之前）

    // WAL.Append 前：WriteBuffer entries 携带 commitTS（待实现 ToWALEntries）
    entries := tx.writeBuffer.ToWALEntries(commitTS)
    for _, entry := range entries {
        tx.engine.wal.Append(entry)
    }

    // Commit marker：KeyLen=8, ValueLen=0, Key 区域存 CommitTS 大端编码
    commitEntry := &WALEntry{
        Type:  EntryTypeCommit,
        TxID:  tx.txID,
        Key:   encodeUint64BE(commitTS),
        Value: nil,
    }
    tx.engine.wal.Append(commitEntry)
    tx.engine.wal.Sync()

    // Apply 在 Sync 之后（确保 Commit marker 已持久化）
    if err := tx.applyWriteBuffer(ctx, commitTS); err != nil {
        return err  // Apply 失败视为系统级错误，Recovery 中处理
    }
    return nil
}

// Rollback 中也执行 Unregister（defer 在 Commit 中设置，Rollback 需显式调用）
// ⚠️ C3 修复：Rollback 也使用 defer 保护，防止 panic 导致 txID 泄漏
// 双重 Unregister 安全：delete(map, key) 对不存在的 key 是 no-op
func (tx *SnapshotTx) Rollback() error {
    defer tx.engine.activeTxRegistry.Unregister(tx.txID)
    // ... 原有清理逻辑
}
```

> ⚠️ **Apply 失败处理**（H5 + 第四轮 NEW-5 + 第五轮审核 C2）：WAL 中 Commit marker 已持久化，不可回滚。Apply 失败视为系统级错误。Recovery 流程中的处理策略：
>
> 1. **运行时 Apply 失败**（`Commit` 中）：进程应终止，依赖下次 Recovery 重放
> 2. **Recovery 重放 Apply 失败**：最多重试 3 次。重试仍失败：**暂停 Recovery + 上报运维**（第五轮审核 C2 — 改为保守策略）。原因：VersionChain 重建存在隐含 key 冲突依赖——被跳过的事务可能写入后续事务引用的 key，导致后续事务 `Prepend(OldValue)` 的 OldValue 错误。启动完成后由运维介入修复。
>
> ⚠️ **AppendBatch**（H7）：Group Commit 场景下，建议引入 `WAL.AppendBatch(entries []*WALEntry)` 保证单事务 entries 连续写入，减少交错碎片。

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
    // 关键：开始时 atomic load rootRef 快照，基于固定 root 遍历
    // COW 保证旧 root 子树不被就地修改（LMDB/BoltDB 经典做法）
    // checkpointStartLSN 之后的修改由 WAL 重放补偿
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
| ~~GC 裁剪与并发 snapshotGet 竞争~~ | ~~CAS 修改 next 指针与无锁遍历竞争~~ | **已修复**：采纳 Mark-and-Sweep 方案，只标记不剪链，不修改 next 指针 |
| WAL sync 性能瓶颈 | 写入吞吐下降 | Group Commit 批量刷盘 |
| Checkpoint 期间脏页追踪 | **COW BTree 语义下方案需重新评估** | 见 6.2 问题 1 |
| **Apply 在 Commit marker 之前（原始设计）** | **BTree 残留未提交脏数据** | **已修复**：Commit marker → Sync → Apply |
| **Recovery 无 commitTS 重建机制** | **MVCC 可见性系统无法工作** | **已计划**：Commit marker 携带 commitTS |
| **Group Commit 破坏 LSN→commitTS 单调性** | **MVCC 时间戳单调性被违反** | **已计划**：commitTS 在 Append 时按 LSN 顺序分配 |
| **Eager Pruning 在 Prepend 热路径执行 O(n)** | **commit 关键路径引入不确定延迟** | **已修复**：采纳 Mark-and-Sweep，不修改链拓扑，延迟到 Prepend 时清理 |
| **GC 执行和 Watermark 计算非原子** | **新事务在 GC 期间注册可能读到正在回收的版本** | **已修复**：统一 Mutex 保护，Register/Watermark 共享同一把锁 |
| **pruneVersionChains 无 per-key 同步** | **GC 与并发 commitKey 并发修改同一 key 时无安全保证** | **已修复**：Mark-and-Sweep 只标记 reclaimed，不修改链结构，与 commitKey 无冲突 |

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

> ⚠️ **2PC Coordinator 崩溃风险**（第五轮审核 H6）：2PC 在 Coordinator 故障时可能阻塞锁资源释放（Prepare 后 Coordinator 崩溃，participants 持有锁等待决策）。此为 2PC 经典问题，Phase 3 不涉及跨节点事务，此风险在当前 scope 之外。如需缓解阻塞问题，建议远期评估 3PC 或 Paxos/EPaxos 替代方案。

**CAP/PACELC 分析**（第五轮审核 C3）：

不同分布式 WAL 模式对应不同的一致性-延迟-可用性权衡，当前文档的工程对比缺少理论框架约束：

| 模式 | CAP 分类 | PACELC | 分区行为 |
|------|---------|--------|---------|
| **Leader-based WAL** | CP | PC+EC | 分区时 Leader 失联后停止服务 |
| **Quorum-based WAL** | AP（最终一致） | PA+EL | 分区时继续提供服务 |
| **Replicated WAL Service** | CP | PC+EC | 多数派存活即可服务 |
| **2PC + WAL（远期推荐）** | **CP** | **PC+EC** | **分区时阻塞（持有锁等待 Coordinator）** |
| **Gossip-based WAL** | AP | PA+EL | 分区时完全可用，但冲突需合并 |

**对 Phase 3 的影响**：
- 当前单节点 WAL 属于 **CP**（无网络分区问题）
- 远期 2PC+WAL 明确属于 CP 系统，与 NexKV 去中心化目标存在根本张力
- 如果未来引入 Gossip 最终一致分片，2PC 不适用（需 PA+EL 方案），需独立的分布式 WAL 机制（如 Gossip-based WAL + CRDT 冲突合并）
- **建议**：在 Phase 4 分布式 WAL 设计时，明确选择 CP vs AP 路线，这影响整个架构演进方向

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

**NexKV 采纳：路径 C（WAL 全量重放 + BTree 辅助）**（三专家审核一致）

**直接采用路径 C，不做路径 B 过渡**。理由：

1. **路径 B 违反 SI 语义**：只保留最新版本，崩溃恢复后 `snapshotTS=130` 的事务应看到 `commitTS=120` 的旧值，但路径 B 只保留 `commitTS=150` 的值
2. **Commit marker 携带 commitTS 是 Step 2 阻塞项**：路径 C 不需要额外工作
3. **路径 B 的"快速过渡"价值被高估**：核心工作量在 Commit marker 格式改造，路径 B 的 BTree-only 重建代码反而增加维护负担

> ⚠️ **路径 C 的关键前提**：WAL entry 需要携带 commitTS（见 Section 3.1/3.2 修复）。如果 Commit marker 携带 commitTS，Recovery 时：
> 1. 按 TxID 分组扫描 Commit marker → 获得 commitTS
> 2. 同一 TxID 的 WriteBuffer entries 拥有相同 commitTS
> 3. WAL entries 按 LSN 顺序 → 即 commitTS 顺序 → 可重建正确的 VersionChain 拓扑

**⚠️ Recovery 重放幂等性（C1 — 三专家审核）**：

`commitKey` 的 Prepend 不是幂等的——CAS 基于 head 指针而非 commitTS 去重。如果 Recovery 重放时某个 key 的部分 Apply 已在崩溃前完成，重放会产生重复 VersionNode。

**幂等性保护（三阶段检查 — 第四轮审核修订）**：
1. **前向跳过检查**（第四轮 NEW-1）：如果 `beginTS > commitTS`，说明崩溃前已有更新的事务完成了 Apply，直接跳过——重放会导致用旧值覆盖新值
2. **第一阶段 — BTree 检查**：如果 `beginTS == commitTS`，进入第二阶段验证
3. **第二阶段 — VersionChain 节点存在性检查**（第三轮 N1）：即使 `beginTS == commitTS`，仍需检查 VersionChain 中是否已存在该 commitTS 的节点。如果不存在，说明崩溃发生在 Prepend 之前
4. **Prepend 侧**：新增 commitTS 去重检查——遍历链头前几个节点，如果已存在相同 commitTS 的节点，直接返回（不重复 Prepend）

```
Recovery 重放单 key 流程（三阶段幂等）：
  1. 读取 entry 的 commitTS（从 Commit marker）和 Type（Insert/Update/Delete）
  2. BTree.Get(key) → 获取当前 beginTS
  3. 如果 beginTS > commitTS → 跳过（已有更新版本，无需重放）
  4. 如果 beginTS == commitTS：
     a. 检查 VersionChain 是否已有 commitTS 对应的节点
     b. 若已有 → 跳过（BTree 和 VersionChain 都已 Apply）
     c. 若没有 → 根据 entry.Type 补充 Prepend（见下方说明）
  5. 如果 beginTS < commitTS（或 key 不存在）→ 完整重放（Prepend + BTree.Set）
```

> ⚠️ **Recovery 重放 Op 类型来源**（第四轮 NEW-4）：重放时的操作类型直接从 `WALEntry.Type` 字段（Insert/Update/Delete）获取，不从 BTree 状态推导。
> - `Type=Insert`：Prepend Tombstone marker（`commitTS, nil, FlagTombstone`），无需从 BTree 获取旧值
> - `Type=Update`：先 `BTree.Get(key)` 获取当前值作为 OldValue，然后 `BTree.Set` 写入新值，最后 `Prepend(OldValue, OldFlag)` 重建 VersionChain
> - `Type=Delete`：类似 Update，Prepend 删除前的值

**完整路径 D（Undo Log）的远期考虑**：如果 Phase 3 之后需要更高效的 MVCC 历史恢复能力（避免全量 WAL 扫描），可引入 Undo Log（参考 PostgreSQL）。这是 Phase 4+ 的选项。

---

### 6.3 开放问题决策汇总（评审修订版）

| 问题 | 结论 | 阻塞 Phase 3？ | 备注 |
|------|------|--------------|------|
| BTree 脏页追踪 | **修正**：利用 COW 特性，Checkpoint 遍历活跃路径即可，无需脏页位图 | 否（Checkpoint 独立 Step） | 需 Step 3 验证 COW 语义假设 |
| WAL logical vs physical | 保持 Logical-with-redo-info | 否 | 与 MVCC 语义一致 |
| 分布式 WAL | 远期问题（Phase 4+） | 否 | ⚠️ **新增风险警告**：跨分片事务在节点崩溃后可能部分提交，当前 MVP 只保证单分片原子性 |
| VersionChain 恢复 | **修正**：直接采用路径 C（WAL 全量重放 + CommitTS），放弃路径 B | 否（与 Step 2 合并） | ⚠️ **原路径 B 方案已删除**（违反 SI 语义） |
| **WAL commitTS 嵌入** | **修正**：Type=Commit 时 `KeyLen=8, ValueLen=0`，Key 区域存 CommitTS 大端编码 | **是（Step 2 必须修复）** | 当前 WAL entry 格式不含 commitTS，Recovery 无法重建 MVCC |
| **DiskWAL Segment 轮转** | **新增**：当前代码为单文件 WAL，Segment 轮转是 Step 2/3 前置依赖，单文件下 Truncate 不可用 | **是（Step 2 阻塞项，不可跳过）** | SegmentSize 配置存在但未使用；轮转逻辑：文件大小超 SegmentSize 时创建新 segment |
| **DirtyTracker 接口** | 新增接口，当前代码无调用点 | 是（Step 3 必须实现） | COW 语义下 DirtyTracker 方案已修正为活跃路径遍历 |
| **分布式 WAL 2PC 约束** | **新增**：2PC + WAL 仅适用 Quorum 强一致分片 | 否（远期） | 涉及 Gossip 最终一致分片的跨分片事务使用 best-effort + 冲突合并 |
| **Recovery LSN 排序保证** | **新增**（C4）：`Recover()` 必须在收集所有 entries 后按 LSN 排序 | **是（Step 2 阻塞项）** | 当前 `scanWALDirectory()` 不保证跨文件全局 LSN 顺序 |
| **Phase 3 单节点限制** | **新增**（C6）：commitTS/GC/Checkpoint 仅保证单节点场景 | 否（文档标注） | 见 Section 1.2 Phase 3 范围声明表 |
| **Recovery 重放幂等性** | **新增**（C1）：重放前检查 beginTS==commitTS 跳过已 Apply 的 key；**第三轮 N1**：VersionChain 节点存在性检查；**第四轮 NEW-1**：增加 `beginTS > commitTS → 跳过` 前向检查；**第四轮 NEW-2**：commitKey 改为 Prepend-before-Set 执行顺序 | **是（Step 2 必须实现）** | 三阶段幂等 + Prepend-before-Set 消除 half-Apply 旧值丢失 |
| **GC Prune 递增 generation** | **新增**（C2）：Prune 标记 reclaimed 后必须 `chain.generation.Add(1)` | **是（Step 1 必须实现）** | 否则 snapshotGet 无法检测链逻辑修改，违反 SI 语义 |
| **GC Tombstone 保留规则** | **修正**（H1）：Tombstone 遮盖的非 Tombstone 可见版本也必须保留 | **是（Step 1 必须实现）** | 见 Section 4.4 场景 B 示例 |
| **Group Commit 详细设计** | **新增**（H2/H3）：LSN 分配与文件写入原子性、syncWorker 生命周期 | **是（需独立设计文档，Step 2 前完成）** | 当前 Group Commit 设计停留在原理层面，goroutine 级设计缺失 |
| **commitKey 执行顺序** | **新增**（第四轮 NEW-2）：Prepend-before-Set，消除 half-Apply 时 OldValue 丢失 | **是（Step 2 必须实现）** | 当前 Set→Prepend 顺序在 half-Apply 时 BTree 旧值被覆盖 |
| **Fuzzy Checkpoint Root Snapshot** | **新增**（第五轮 C1）：DFS 遍历前必须 `atomic.LoadPointer(&btree.root)` | **是（Step 3 阻塞项）** | 防止遍历期间 Split 更新 root |
| **Apply 失败策略** | **修正**（第五轮 C2）：从"跳过继续"改为"暂停 Recovery + 上报运维" | **是（Step 2 必须实现）** | VersionChain 重建存在隐含 key 冲突依赖 |
| **CAP/PACELC 理论框架** | **新增**（第五轮 C3）：明确 2PC+WAL 属于 CP 系统（分区阻塞），标注 NexKV 去中心化张力 | 否（远期 Phase 4 决策） | 见 §6.2-3 |
| **commitTS nodeID 编码约束** | **修正**（第五轮 C4）：高 16 位 nodeID 不能独立建立全局排序，必须依赖 HLC | 否（文档标注） | 低 48 位 = HLC 的 physical+logical，高 16 位 = nodeID（冲突优先级） |
| **syncWorker goroutine 生命周期** | **新增**（第五轮 C5）：启动者/ctx 优雅退出/panic broadcastError | **是（Step 2 阻塞项，Group Commit 设计）** | 见 §3.4 设计代码和 Key 约束 |
| **BTree.GetWithMeta 接口** | **新增**（第五轮 H1）：Recovery 三阶段幂等需要读取 beginTS | **是（Step 2 阻塞项）** | Wire: [Flag:1][beginTS:8][RealValue:N] |
| **WAL 全量扫描性能基线** | **新增**（第五轮 H2）：30s/1万TPS/128B → ~38MB/周期 | 否（文档标注） | 建议流式分组减少内存峰值 |
| **跳跃扫描模式** | **新增**（第五轮 H3）：乐观读取 Length + CRC 验证 | 否（文档标注） | 见 §3.3 |
| **Global Watermark 安全条件** | **新增**（第五轮 H4）：安全条件为 min(all_nodes_watermarks) | 否（远期 Phase 4） | 见 §4.3 分布式约束小节 |
| **路径 C 分片归属检查** | **新增**（第五轮 H5）：恢复重放前检查 key 所属分片 | 否（远期 Phase 4 扩展） | 当前单节点跳过 |
| **2PC+WAL Coordinator 崩溃** | **新增**（第五轮 H6）：2PC Prepare 后 Coordinator 崩溃阻塞锁释放 | 否（远期 Phase 4 决策） | 建议同时评估 3PC/Paxos |
| **reclaimed 可见性内存序** | **新增**（第五轮 H7）：reclaimed.Store + generation.Add 的 happens-before 推理链注释 | **是（Step 1 必须实现）** | 防止未来修改破坏可见性 |
| **孤儿节点累积** | **新增**（第五轮 M3）：Prepend 清理时检测 BTree beginTS 不匹配的孤儿节点 | **是（Step 1 必须实现）** | 高频写入场景长期累积 |
| **NEW-6 清理范围约束** | **修正**（第五轮 M7）：仅限于从链头开始的连续 reclaimed 段 | **是（Step 1 文档标注）** | 修改 next 与并发安全约束冲突 |

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

**文档版本**: v2.4
**创建日期**: 2026-04-17
**最后更新**: 2026-04-24
**更新内容**: 第五轮三专家审核修复（存储引擎 / 分布式系统 / Go 并发安全），基于 `2026-04-24-phase3-review-action-plan.md`：
- C1 (CRITICAL): Fuzzy Checkpoint 增加 Atomic Root Snapshot 机制——`atomic.LoadPointer(&btree.root)` 获取固定 root 快照，Checkpoint 全程基于该快照遍历
- C2 (CRITICAL): Apply 失败策略从"跳过继续"改为"暂停 Recovery + 上报运维"——VersionChain 重建存在隐含 key 冲突依赖
- C3 (CRITICAL): Section 6.2 增加 CAP/PACELC 分析小节，明确 2PC+WAL 属于 CP 系统，标注分区阻塞风险
- C4 (CRITICAL): commitTS 高 16 位 nodeID 方案标注为"本地单调，跨节点需 HLC"，增加 HLC 演进路径说明
- C5 (CRITICAL): Section 3.4 增加 syncWorker goroutine 生命周期设计（启动者、ctx 退出、panic broadcastError 唤醒等待者）
- H1 (HIGH): 新增 `BTree.GetWithMeta(key) → (value, beginTS, error)` 接口定义，作为 Step 2 阻塞项
- H2 (HIGH): 标注 WAL 全量扫描恢复的性能基线——30s Checkpoint、1万 TPS 下 WAL 体积约 38MB/周期
- H3 (HIGH): 增加 Length 跳跃扫描的"乐观猜测 + CRC 验证"模式说明
- H4 (HIGH): Section 4.3 增加 Global Watermark 核心约束——安全条件为 min(all_nodes_watermarks)
- H5 (HIGH): 路径 C 恢复增加分片归属检查预留注释（Phase 4 扩展）
- H6 (HIGH): 2PC+WAL 方案标注 Coordinator 崩溃阻塞风险，建议远期评估 3PC/Paxos
- H7 (HIGH): Prune 和 snapshotGet 代码增加 reclaimed 可见性内存序推理注释
- M1 (MEDIUM): PrevLSN 标注为完整性校验用途，非移除（保持与未来分布式模式兼容）
- M2 (MEDIUM): GC sync.Map.Range 跳过新 key 的边界情况已文档化
- M3 (MEDIUM): 新增孤儿节点检测机制——Prepend 清理时检查 BTree 中 beginTS 是否匹配
- M4 (MEDIUM): 范围声明表增加"部署形态"列，区分单节点 vs 分布式部署语义
- M5 (MEDIUM): Checkpoint Truncate 分布式协调标注为 Phase 4 工作项
- M6 (MEDIUM): GC 保留规则增加跨节点可见性标注——当前仅对本地事务安全
- M7 (MEDIUM): NEW-6 清理范围约束修正——仅限链头连续段，深度 reclaimed 通过 generation bump 容忍
- L1-L6: 多个 LOW 级别文档注释和边界声明补充

**v2.3 更新**（2026-04-22）— 第四轮三专家审核修复：
- C1: GC 方案全面修订为 Mark-and-Sweep（标记清除），弃用 CAS 修改 next 指针
- C2: ActiveTxRegistry 改为 Mutex + 普通 map，弃用 sync.Map
- C3: Register 从 Commit 移至 BeginTx，txID 由 txManager 统一分配
- C4: Recovery 直接采用路径 C（WAL 全量重放），删除路径 B 过渡方案
- C5: Commit marker 格式修正为 KeyLen=8, ValueLen=0（修正原文 KeyLen=0 错误）
- C6: DiskWAL 标注 Segment 轮转未实现，作为 Step 2 前置依赖
- H1: GC 保留规则明确包含 Tombstone 防复活
- H4: Group Commit Sync 容错机制（ctx.Done + recover）
- H6: CRC 覆盖范围修正为含 Length
- M5: GC goroutine 传入 ctx 支持优雅退出
- M6: 删除 Limbo Bag 设计，直接让节点不可达
- M8: 分布式 WAL 2PC 约束明确仅适用 Quorum 分片
- L1: Trailer 修正为 4 字节
- L5: 删除 memory barrier 误导描述

**v2.1 更新**（2026-04-20）— 第二轮三专家审核修复：
- C1: Recovery 重放幂等性保护（beginTS==commitTS 跳过 + Prepend commitTS 去重）
- C2: GC Prune 标记 reclaimed 后必须递增 chain.generation（否则 snapshotGet 无法检测链逻辑修改，违反 SI 语义）
- C3: Rollback 路径 Unregister 改为 defer 保护（防止 panic 导致 watermark 永久卡死）
- C4: Recover() 收集 entries 后必须按 LSN 排序（当前 scanWALDirectory 不保证全局 LSN 顺序）
- C5: DiskWAL Segment 轮转标注为 Step 2 阻塞项（单文件下 Truncate 不可用）
- C6: 新增 Phase 3 范围声明（commitTS/GC/Checkpoint 仅保证单节点场景）
- H1: GC 保留规则修正——Tombstone 遮盖的非 Tombstone 可见版本也必须保留
- H2/H3: Group Commit 标注需独立设计文档（LSN 乱序风险 + syncWorker 生命周期缺失 + commitTS 归属矛盾）

**v2.2 更新**（2026-04-21）— 第三轮三专家审核修复：
- N1 (CRITICAL): Recovery 幂等性升级为两阶段检查——BTree beginTS 检查 + VersionChain 节点存在性检查（解决 BTree 已写入但 Prepend 未完成的 half-Apply 场景）
- N2 (HIGH): Watermark=0 时 GC 改为使用 `tsGen.CurrentTS()` 而非直接跳过（无活跃事务时恰恰最需要 GC）
- N3 (HIGH): WALEntry 不携带 OldValue/OldFlag，Recovery 时从 BTree 当前状态推导（两阶段幂等检查保证安全性）
- N4 (HIGH): WAL 格式升级标注为破坏性变更，与 Phase 2 不兼容（Phase 2 无持久化需求，无需迁移）

**v2.3 更新**（2026-04-22）— 第四轮三专家审核修复：
- NEW-1 (CRITICAL): Recovery 增加三阶段幂等检查——`beginTS > commitTS → 跳过`（前向检查，防止旧值覆盖新值）
- NEW-2 (CRITICAL): commitKey 执行顺序从 Set→Prepend 改为 Prepend→Set（消除 half-Apply 时 OldValue 丢失）
- NEW-3 (HIGH): BeginTx 中 NextTS + Register 放入同一 Mutex 临界区（消除 GC Watermark 窗口）
- NEW-4 (HIGH): 明确 Recovery 重放 Op 类型来源为 WALEntry.Type 字段
- NEW-5 (HIGH): Recovery Apply 失败增加重试上限（3 次）+ 继续重放后续事务策略
- NEW-6 (MEDIUM): Prepend 清理范围标注可扩展到可达路径连续 reclaimed 段
- NEW-7 (MEDIUM): Prune 与 snapshotGet 并发安全性论证补充
- NEW-9 (MEDIUM): GC 保留规则场景 B 解释修正
- NEW-10 (LOW): 增加 beginTS == commitTS 术语映射说明
**状态**: Draft — 等待架构师评审
