# Phase 3: MVCC 剩余 5 项补全预研

> 创建日期：2026-05-23
> 前置：MVCC Phase 2 已完成（`mvcc/` 22 文件）、Epoch-based Page Reclamation 已就位（`28ec388`）、Phase 4 WAL+AO 已就位（`12b338e`/`397d4d7`）
> 状态：Planning
> 来源：`2026-04-10-mvcc-phase2-plan.md` Phase 3 延后项

---

## 一、背景

MVCC Phase 2（`mvcc/` 22 文件）实现了完整的内存多版本并发控制：Transaction、VersionChain、KeyLock、WriteBuffer、快照读（SI）、PreCheck+Apply 冲突检测。但以下 5 项被显式标记为 "Phase 3 延后"：

| # | 来源章节 | 内容 | 延后原因 |
|---|---------|------|---------|
| 1 | §6.6 | VersionChain GC 安全回收（generation-protected CAS） | Phase 2 GC 仅基础 Prune，不做物理删除 |
| 2 | §7.1-7.2 | WAL Recovery 集成事务恢复 | Phase 2 纯内存，无持久化 |
| 3 | §7.3 | 跨 key 原子性（2PC + WAL Redo） | Phase 2 Commit 为 best-effort undo |
| 4 | §4.1 | endTS 标记 + GC 联动 | Phase 2 无 GC 回收，endTS 预留为 0 |
| 5 | §7.2 | Commit timestamp 从 WAL sync 回调分配 | Phase 2 在 Commit 开始时立即分配 |

**关键前提变化**：Phase 3 WAL（`wal/` 13 文件 + `checkpoint/` 5 文件）和 Phase 4 WAL+AO 持久化已在 Phase 2 之后实现，Epoch-based Page Reclamation（`epoch.go`）已就位。当初标记的 "Phase 3 延后" 依赖项**现已全部满足**。

---

## 二、现有代码审计

### 2.1 VersionChain（`mvcc/version_chain.go`）

```go
type VersionNode struct {
    CommitTS uint64
    Value    []byte
    Flag     byte        // FlagNormal / FlagTombstone
    Next     *VersionNode
    // endTS 预留但始终为 0
}

type VersionChain struct {
    head       atomic.Pointer[VersionNode]
    generation atomic.Uint64  // Phase 3 ABA 防护预留
}
```

**当前 GC（Prune）**：遍历链，标记 `CommitTS < watermark` 的节点 `rolledBack=true`，不释放内存。`PrependWithCleanup` 在追加时跳过已标记节点。

**缺口**：
- `generation` 未纳入 CAS 比较 — Phase 3 GC 回收旧节点后，指针复用可能导致 ABA
- `endTS` 字段始终为 0 — Phase 3 GC 需在节点回收前设置 endTS
- 无 epoch-based reclamation — 物理删除 `*VersionNode` 时 reader 可能仍在遍历

### 2.2 WAL Integration（`mvcc/wal_integration.go`）

当前仅提供 `WriteBufferSnapshot` 和 `KVStoreIntegration`（序列化/反序列化 WriteBuffer 条目）。Recovery 路径不重建事务状态：

```go
type WriteBufferSnapshot struct {
    Entries map[string]WriteEntry
    Ordered []string
}
```

**缺口**：
- Recovery 不重建 VersionChain — 重放 WAL 后 VersionChain 为空
- Checkpoint 不持久化 ActiveTxRegistry — 重启后未提交事务丢失
- 事务 Commit 的 WAL entry 无 beginTS/commitTS 关联

### 2.3 Checkpoint（`checkpoint/checkpoint_manager.go`）

`FuzzyCheckpoint` 已集成 AO 页面刷新 + pageLocs 持久化。但不涉及 MVCC 状态：

**缺口**：
- Checkpoint 不保存 `VersionStore`（chain head + generation）
- 不保存 `ActiveTxRegistry`（活跃事务列表）

---

## 三、5 项逐一分析

### 3.1 VersionChain GC 安全回收

**问题**：`Prune()` 标记已回收节点但保留 `*VersionNode` 指针。需要物理删除节点，但 reader 可能正在遍历 `node.Next`。

**Prerequisites 现状**：
- `generation atomic.Uint64`：✅ 已预留
- Epoch-based Reclamation：✅ `btree/epoch.go` 已就位

**方案**：

```
Prune(watermark):
  1. 遍历链，收集 CommitTS < watermark 的节点
  2. 对每个候选节点：
     a. CAS 标记 endTS = currentTS（防止新 reader 通过该节点遍历）
     b. 从链中摘除（CAS head 或 prev.Next = node.Next）
     c. 将 node 指针放入 EpochManager.Retire
  3. EpochManager.tryReclaim 在 safeEpoch 后 Free 节点

generation-protected CAS:
  Prepend 的 CAS 从 CompareAndSwap(head, newHead)
  变为 CompareAndSwap(head, newHead, generation)
  防止 GC 回收后指针复用导致的 ABA
```

**改动**：
- `version_chain.go`：Prepend CAS 纳入 generation 比较
- `version_chain.go`：Prune 改为 epoch-retire + 物理摘除
- 可选：复用 `btree.EpochManager` 或为 VersionNode 创建独立 epoch 域

### 3.2 WAL Recovery 集成事务恢复

**问题**：Recovery 重放 WAL 但无法重建事务的 VersionChain 和未提交 WriteBuffer。

**现有基础设施**：
- `wal/recovery.go`：WAL 扫描 + 重放
- `wal/recovery_manager.go`（Phase 4.3 新增）：三阶段恢复（基础设施 → BTree 结构 → 增量重放）

**方案**：在 Recovery Phase C（增量 WAL 重放）中增加事务状态重建：

```
Recovery Phase C 扩展:
  1. 扫描 WAL 找最新 CheckpointEntryV2 → 恢复 BTree 结构
  2. 重放 Checkpoint 之后的 WAL entries:
     a. WALTypeTxBegin    → ActiveTxRegistry.Register(txID, beginTS)
     b. WALTypeTxWrite    → 重建 WriteBuffer (txID → {key, value, opType})
     c. WALTypeTxCommit   → 重建 VersionChain (commitTS + entries)
     d. WALTypeTxRollback → ActiveTxRegistry.Unregister(txID)
     e. WALTypeSet/Delete (非事务) → 直接 Apply 到 BTree
  3. 未提交事务（ActiveTxRegistry 中的残余）→ Rollback
```

**新的 WAL Entry 类型**：
```
WALTypeTxBegin  = 8  // Key: txID(8) + beginTS(8)
WALTypeTxWrite  = 9  // Key: txID(8) + key
WALTypeTxCommit = 10 // Key: txID(8) + commitTS(8) + entryCount(4)
WALTypeTxRollback = 11 // Key: txID(8)
```

**改动**：
- `mvcc/wal_integration.go`：新增 TxBegin/TxWrite/TxCommit/TxRollback WAL entry 构造
- `mvcc/transaction.go`：Commit 路径增加 WAL Append
- `wal/recovery_manager.go`：Phase C 增加事务状态重建
- `checkpoint/checkpoint_manager.go`：Checkpoint 后清理已提交事务的 WAL entries

### 3.3 跨 key 原子性（2PC + WAL Redo）

**问题**：当前 `Commit()` 逐 key Apply + Prepend，如果中途失败，已完成的 key 不回滚（best-effort undo）。

**方案**：

```
WAL-based 2PC（Phase 3 完整实现）:
  
  Prepare Phase:
    1. WAL.Append(TxPrepare) — 记录所有 WriteBuffer entries
    2. WAL.Sync() — 持久化 Prepare 记录
  
  Commit Phase:
    3. 分配 commitTS（从 WAL sync 回调）
    4. 逐 key Apply + Prepend（与 Phase 2 相同）
    5. WAL.Append(TxCommit) — 记录 commitTS
    6. WAL.Sync()
  
  Recovery:
    - TxPrepare 无 TxCommit → Rollback（撤销已 Apply 的 key）
    - TxPrepare + TxCommit → 恢复 VersionChain（与 Phase 2 相同）

Rollback 安全性:
  WAL 保证 Prepare 记录持久化。崩溃恢复时：
  - 有 Prepare + 无 Commit → 基于 WAL 记录的 OldValue 执行 Rollback
  - 有 Prepare + 有 Commit → 重建 VersionChain
```

**改动**：
- `mvcc/transaction.go`：Commit 路径增加 Prepare/Commit WAL 写入
- `mvcc/wal_integration.go`：TxPrepare/TxCommit WAL entry 序列化
- `wal/recovery_manager.go`：2PC Recovery 协议

### 3.4 endTS 标记 + GC 联动

**问题**：`VersionNode.endTS` 始终为 0，GC 无法精确判断节点是否可回收。

**方案**：

```
endTS 写入时机:
  1. Prune 遍历链时，对候选节点 CAS 设置 endTS
  2. endTS = 当前 globalEpoch（或 watermark）
  
endTS 在可见性判断中的使用:
  snapshotGet 遍历链时检查:
    - node.endTS != 0 && node.endTS <= snapshotTS → 跳过（节点在快照时间点已回收）
    - 当前 Phase 2 不使用 endTS 做可见性判断（append-only 保证旧节点不变）
    - Phase 3 GC 后：reader 可能通过 Next 指针访问到已回收节点
      → Epoch-based reclamation 保证 reader 退出前节点不被物理删除
      → 因此 endTS 仅用于 GC 内部标记，不影响可见性判断
```

**改动**：
- `version_chain.go`：Prune 时 CAS 设置 endTS
- `mvcc/codec.go`：endTS 字段已有预留，无需格式变更

### 3.5 Commit timestamp 从 WAL sync 回调分配

**问题**：当前 `commitTS` 在 `Commit()` 开始时通过 `tsGen.Next()` 分配。Phase 3 应在 WAL sync 后分配，保证 commitTS 严格递增且与 WAL 顺序一致。

**方案**：

```
Before (Phase 2):
  func (tx *Transaction) Commit() error {
      tx.commitTS = tx.tsGen.Next()  // 立即分配
      // ... PreCheck + Apply + Prepend ...
  }

After (Phase 3):
  func (tx *Transaction) Commit() error {
      // 1. PreCheck 所有 key
      // 2. WAL.Append(TxPrepare, entries)
      // 3. WAL.Sync()
      // 4. tx.commitTS = tx.tsGen.Next()  // WAL sync 后分配
      // 5. Apply + Prepend
      // 6. WAL.Append(TxCommit, commitTS)
  }
```

**接口预留**：`TSGenerator` 接口已有 `Next()` 方法，无需变更。Commit 逻辑重组即可。

**改动**：
- `mvcc/transaction.go`：Commit 流程重组，commitTS 分配移到 WAL sync 之后
- 向后兼容：Phase 2 无 WAL 时保持立即分配行为

---

## 四、依赖关系

```
Epoch Reclamation (已就位)
        ↓
  [1] VersionChain GC 安全回收 ──→ 需要 Epoch
        ↓
  [4] endTS 标记 ──→ 依赖 [1]
        ↓
  [5] Commit TS 从 WAL 分配 ──→ 需要 WAL 基础设施（已就位）
        ↓
  [2] WAL Recovery 事务恢复 ──→ 依赖 [5]
        ↓
  [3] 跨 key 原子性 (2PC) ──→ 依赖 [2]
```

建议执行顺序：**1 → 4 → 5 → 2 → 3**

---

## 五、实现阶段划分

### Phase 3.1: VersionChain GC 安全回收（Item 1 + 4）
- generation-protected CAS（Prepend CAS 纳入 generation 比较）
- Prune 改为 physical removal（epoch-retire VersionNode）
- endTS 标记写入

### Phase 3.2: Commit TS + WAL Recovery（Item 5 + 2）
- 新增 4 个 WAL entry 类型（TxBegin/TxWrite/TxCommit/TxRollback）
- Commit 流程重组（commitTS 在 WAL sync 后分配）
- Recovery Phase C 扩展（事务状态重建）
- Checkpoint 持久化 VersionStore + ActiveTxRegistry

### Phase 3.3: 跨 key 原子性（Item 3）
- WAL-based 2PC（Prepare + Commit）
- Recovery 时未完成事务的 Rollback

---

## 六、Out of Scope

| 事项 | 原因 |
|------|------|
| RangeScan SI | 不在 MVCC Phase 2 scope 内 |
| Off-Heap Version Storage | 远期优化 |
| 分布式事务（跨节点 2PC） | 当前单节点聚焦 |
| siCount 零开销优化（跳过单版本 key 的 VersionChain 构建） | 性能优化，延后 |

---

## 七、风险与缓解

| 风险 | 缓解 |
|------|------|
| generation CAS 变更影响所有 Prepend 调用 | generation 字段已预留，变更仅影响 `version_chain.go` 内部 |
| WAL Recovery 重建 VersionChain 可能丢失未 sync 的 entry | Group Commit + WAL sync 保证持久化顺序 |
| 2PC Prepare/Commit 增加延迟（WAL sync × 2） | Group Commit 批量 sync，Phase 2 单 key 事务不受影响 |
| endTS 标记与 epoch retire 之间 reader 仍可访问节点 | epoch 保证 reader 退出前不 Free 节点 |

---

## 八、参考

- MVCC Phase 2 预研：`2026-04-10-mvcc-phase2-plan.md`
- Phase 3 WAL+GC 预研：`2026-04-17-phase3-wal-gc-spike.md`
- Phase 4 WAL+AO 持久化：`2026-05-16-phase4-wal-ao-persistence-spike.md`
- Epoch Page Reclamation：`2026-05-21-epoch-page-reclamation-spike.md`
- VersionChain 实现：`internal/infrastructure/storage/mvcc/version_chain.go`
- WAL Integration：`internal/infrastructure/storage/mvcc/wal_integration.go`
- Transaction 实现：`internal/infrastructure/storage/mvcc/transaction.go`
- Recovery Manager：`internal/infrastructure/storage/wal/recovery_manager.go`

---

**文档版本**：v1.0
**状态**：Planning
