# 事务原子性方案对比：Lealone UndoLog vs NexKV WAL-based 2PC

> 创建日期：2026-05-23
> 前置：Lealone AOTE 源码分析 + NexKV Phase 3.3 2PC 设计
> 核心问题：多 key 事务如何保证"要么全做，要么全不做"？

---

## 一、问题定义

```mermaid
flowchart TB
    subgraph Problem["事务原子性问题"]
        direction TB
        P1["TX: transfer(A→B, 100元)"]
        P2["Put(A, balance-100)"]
        P3["Put(B, balance+100)"]
        P1 --> P2
        P1 --> P3
    end
    
    subgraph Acceptable["可接受的结果"]
        A1["✅ A:-100, B:+100 (全部成功)"]
        A2["✅ A:不变, B:不变 (全部失败)"]
    end
    
    subgraph Disaster["灾难结果 ❌"]
        D1["A:-100 成功, B:+100 失败<br/>→ 100元凭空消失!"]
    end
    
    Problem --> Acceptable
    Problem --> Disaster
```

**核心挑战**：崩溃可能发生在任何时刻——在修改 A 之后、修改 B 之前。如何保证 Recovery 后要么两个 key 都修改了，要么都没修改？

---

## 二、Lealone 方案：UndoLog + RedoLog

### 2.1 架构概览

Lealone 的 AOTE（Async Adaptive Optimization Transaction Engine）使用 **UndoLog + RedoLog 双日志** 实现单节点事务原子性。

```mermaid
flowchart TB
    subgraph LealoneEngine["Lealone AOTE 事务引擎"]
        direction TB
        
        subgraph Transaction["AOTransaction"]
            Undo["UndoLog<br/>记录修改前的旧值<br/>供 Rollback 使用"]
            Redo["RedoLog<br/>记录修改后的新值<br/>供 Recovery 使用"]
        end
        
        SyncService["LogSyncService<br/>3 种模式:<br/>PERIODIC / INSTANT / NO_SYNC"]
    end
    
    Transaction --> SyncService
    SyncService --> Disk["WAL 文件 (磁盘)"]
```

### 2.2 完整流程

```mermaid
sequenceDiagram
    participant TX as AOTransaction
    participant UL as UndoLog (内存+磁盘)
    participant RL as RedoLog (磁盘)
    participant BT as BTree
    participant LS as LogSyncService
    
    Note over TX,LS: 转账: A-100, B+100
    
    rect rgb(255, 255, 200)
        Note over TX,UL: === 操作阶段 (记录 Undo) ===
        TX->>BT: 读取 A 当前值 = 1000
        TX->>UL: 记录 Undo: (A, oldValue=1000)
        TX->>BT: 写入 A=900
        Note over TX: A 在 BTree 中已变为 900<br/>但其他事务看不到 (MVCC 隔离)
        
        TX->>BT: 读取 B 当前值 = 500
        TX->>UL: 记录 Undo: (B, oldValue=500)
        TX->>BT: 写入 B=600
        Note over TX: B 在 BTree 中已变为 600<br/>但其他事务看不到
    end
    
    rect rgb(200, 255, 200)
        Note over TX,RL: === Commit 阶段 ===
        TX->>RL: 写入 RedoLog:<br/>(A: old=1000 → new=900)<br/>(B: old=500 → new=600)
        TX->>RL: Sync()
        Note over RL: ✅ RedoLog 持久化到磁盘
        
        TX->>LS: 请求 commitTimestamp
        Note over LS: 仅在 RedoLog Sync 后分配<br/>保证 commitTS 单调性
        
        TX->>UL: UndoLog.commit()
        Note over UL: 标记所有操作已完成<br/>其他事务现在可以看到 A=900, B=600
    end
    
    rect rgb(200, 200, 255)
        Note over TX,LS: === 崩溃恢复 ===
        Note over RL: Recovery 扫描 RedoLog:
        Note over RL: - 找到完整事务 → 重放
        Note over RL: - 找到不完整事务 → 丢弃 (Undo 自动废弃)
    end
```

### 2.3 关键设计：UndoLog 的磁盘持久化

Lealone 的 UndoLog **存储在磁盘上**（非纯内存）：

```java
// UndoLog.java — 核心数据结构
class UndoLog {
    // 每个事务的 Undo 记录列表
    List<UndoLogRecord> records;
    
    // 记录格式:
    // [key][oldValue][transactionId]
    
    void add(String key, Value oldValue) {
        records.add(new UndoLogRecord(key, oldValue));
        // ★ 写入磁盘 WAL 文件
    }
    
    void commit(TransactionEngine engine) {
        // ★ 原子性地使所有变更可见
        for (UndoLogRecord r : records) {
            engine.commitFinal(r.key, r.oldValue);
        }
    }
}
```

**核心优势**：UndoLog 在磁盘上，崩溃后不丢失。Recovery 不需要特别处理——不完整的事务的 Undo 记录自然被丢弃，BTree 中的未提交修改被 MVCC 隔离。

### 2.4 Rollback 机制

```mermaid
flowchart TB
    Rollback["Rollback() 调用"] --> Undo["遍历 UndoLog 记录"]
    Undo --> Restore["逐 key 恢复 oldValue"]
    Restore --> Discard["丢弃 UndoLog"]
    
    subgraph Key["Lealone Rollback 不需要 WAL"]
        direction TB
        K1["UndoLog 在磁盘上 → 崩溃后仍存在"]
        K2["MVCC 隔离 → 未提交修改对其他事务不可见"]
        K3["Rollback = 恢复 BTree 旧值 + 丢弃 UndoLog"]
    end
```

### 2.5 局限性

| 局限 | 说明 |
|------|------|
| **单节点** | `isLocal()` 始终返回 `true`，无远程参与者 |
| **2PC 已废弃** | `PREPARE_COMMIT`/`COMMIT_TRANSACTION` 语句已移除 |
| **UndoLog 膨胀** | 大事务产生大量 Undo 记录，占用磁盘空间 |
| **MVCC 复杂度** | `TransactionalValue` 的可见性判断依赖 commitTS，与 UndoLog 耦合 |

---

## 三、NexKV 方案：WAL-based 2PC

### 3.1 架构概览

NexKV Phase 3.3 使用 **WAL-based 2PC**（两阶段提交），核心区别在于：**回滚信息（oldValue）在 Prepare 阶段才持久化到 WAL，而非在操作时实时记录**。

```mermaid
flowchart TB
    subgraph NexKVEngine["NexKV 事务引擎"]
        direction TB
        
        subgraph MVCC["MVCC Layer"]
            WB["WriteBuffer (内存)<br/>操作暂存"]
            VC["VersionChain<br/>历史版本链"]
        end
        
        subgraph WAL2["WAL (Phase 3.2)"]
            Prepare["TxPrepare Entry<br/>含所有 key 的 oldValue 快照"]
            Commit2["TxCommit Entry<br/>commitTS + entryCount"]
        end
        
        BT2["BTree (COW)"]
    end
    
    WB --> Prepare
    Prepare --> Commit2
    Commit2 --> BT2
    BT2 --> VC
```

### 3.2 完整流程

```mermaid
sequenceDiagram
    participant TX as SnapshotTx
    participant WB as WriteBuffer (内存)
    participant WAL as WAL (磁盘)
    participant BT as BTree
    participant VC as VersionChain
    
    Note over TX,VC: 转账: A-100, B+100
    
    rect rgb(255, 255, 200)
        Note over TX,WB: === 操作阶段 (纯内存) ===
        TX->>WB: Put("A", "900")
        Note over WB: WriteBuffer["A"] = {Op:Update, NewValue:"900"}
        TX->>WB: Put("B", "600")
        Note over WB: WriteBuffer["B"] = {Op:Update, NewValue:"600"}
        Note over TX,BT: ★ BTree 未修改! (COW 阶段稍后)
    end
    
    rect rgb(255, 200, 200)
        Note over TX,BT: === PreCheck 阶段 ===
        TX->>BT: GetRaw("A") → beginTS 校验 ✓
        TX->>BT: GetRaw("B") → beginTS 校验 ✓
    end
    
    rect rgb(200, 255, 200)
        Note over TX,WAL: === Prepare 阶段 (WAL 持久化 oldValue) ===
        TX->>WAL: Append(TxPrepare)<br/>Key=[txID:8]<br/>Value=[(A, oldVal=1000, newVal=900)<br/>       (B, oldVal=500, newVal=600)]
        TX->>WAL: Sync()
        Note over WAL: ✅ oldValue 快照持久化!
    end
    
    rect rgb(200, 200, 255)
        Note over TX,VC: === Apply + Commit 阶段 ===
        TX->>TX: commitTS = tsGen.Next()
        
        TX->>BT: COW Set(A, BuildMVCC(Normal, commitTS, 900))
        TX->>VC: Prepend(A, oldVal=1000, oldFlag)
        Note over TX: ✅ A 完成
        
        TX->>BT: COW Set(B, BuildMVCC(Normal, commitTS, 600))
        TX->>VC: Prepend(B, oldVal=500, oldFlag)
        Note over TX: ✅ B 完成
        
        TX->>WAL: Append(TxCommit, commitTS)
        TX->>WAL: Sync()
        Note over WAL: ✅ 事务完成
    end
```

### 3.3 崩溃恢复对比

```mermaid
flowchart TB
    Crash["💥 崩溃"] --> Check{"WAL 中有 TxPrepare?"}
    
    Check -->|"无"| NoAction["无操作<br/>WriteBuffer 在内存中已丢失<br/>BTree 未被修改"]
    
    Check -->|"有"| CheckCommit{"有对应的 TxCommit?"}
    
    CheckCommit -->|"有"| Replay["Replay 路径:<br/>1. 从 TxPrepare 提取 newVal<br/>2. BTree.Set(newVal) (幂等)<br/>3. VersionChain.Prepend(oldVal)"]
    
    CheckCommit -->|"无"| Rollback2["Rollback 路径:<br/>1. 从 TxPrepare 提取 oldVal<br/>2. BTree.Set(oldVal) (恢复)<br/>3. VersionChain 回退"]
    
    Replay --> Done2["✅ 数据一致"]
    Rollback2 --> Done2
    NoAction --> Done2
```

---

## 四、核心差异对比

### 4.1 设计哲学

```mermaid
flowchart LR
    subgraph Lealone["Lealone: 先记后做"]
        direction TB
        L1["操作时立即记录 UndoLog"]
        L2["UndoLog 在磁盘上"]
        L3["Commit = 标记完成"]
        L4["Rollback = 恢复旧值"]
    end
    
    subgraph NexKV["NexKV: 先暂存后持久"]
        direction TB
        N1["操作暂存 WriteBuffer"]
        N2["Prepare 时持久化 oldValue"]
        N3["Commit = 写入 BTree + 标记完成"]
        N4["Rollback = WAL 中的 oldValue"]
    end
```

### 4.2 详细对比表

| 维度 | Lealone (UndoLog) | NexKV (WAL-based 2PC) |
|------|------------------|----------------------|
| **操作时行为** | 立即修改 BTree + 记录 Undo | 暂存 WriteBuffer（内存），不修改 BTree |
| **回滚信息存储** | UndoLog（磁盘，实时记录） | WAL TxPrepare（磁盘，Prepare 时一次性写入） |
| **回滚信息来源** | UndoLog 记录 | WAL TxPrepare 中的 oldValue 快照 |
| **BTree 修改时机** | 操作时（MVCC 隔离） | Commit Apply 阶段（COW 批量写入） |
| **崩溃后 Rollback** | UndoLog 自动废弃（磁盘记录不丢失） | Recovery 扫描 WAL → 提取 oldValue → 回滚 |
| **Prepare 阶段** | 无 | 有（WAL 持久化 oldValue） |
| **适用场景** | 单节点 | 单节点（可扩展为分布式 2PC） |
| **磁盘写入量** | 每次操作写入 Undo（多次小写） | Prepare 时一次写入（一次大写） |
| **大事务风险** | UndoLog 持续膨胀 | TxPrepare 单次分配（可能很大） |
| **隔离机制** | MVCC（TransactionalValue + commitTS） | MVCC（VersionChain + beginTS + snapshotTS） |
| **WAL Sync 模式** | PERIODIC/INSTANT/NO_SYNC | EveryWrite/GroupCommit/EverySecond/Batch |

### 4.3 具体场景对比

**场景：转账 10000 个账户，中途崩溃**

```mermaid
flowchart TB
    subgraph LealoneScenario["Lealone: 先改后记"]
        direction TB
        LA["操作 5000 个账户<br/>每操作一次: BTree 修改 + UndoLog 写入"]
        LB["💥 崩溃在第 5001 个"]
        LC["Recovery:<br/>UndoLog 记录完整 → Rollback 5000 个"]
        LD["✅ 全部回滚成功<br/>代价: 5000 次 BTree 写 + 5000 次 UndoLog 写"]
    end
    
    subgraph NexKVScenario["NexKV: 先暂存后持久"]
        direction TB
        NA["操作 10000 个账户<br/>全部暂存 WriteBuffer (纯内存)"]
        NB["PreCheck + Prepare<br/>一条 TxPrepare 写入所有 oldValue"]
        NC["Apply: 修改 BTree"]
        ND["💥 崩溃在第 5001 个 Apply"]
        NE["Recovery:<br/>TxPrepare 完整 → Rollback 5000 个<br/>基于 WAL 中的 oldValue"]
        NF["✅ 全部回滚成功<br/>代价: 0 次 BTree 写 (操作时) + 1 次 WAL 写 (Prepare)"]
    end
```

---

## 五、NexKV 为什么选择 WAL-based 2PC 而非 UndoLog？

### 5.1 COW 架构的天然优势

NexKV 的 BTree 使用 **页面级 Copy-On-Write**——每次修改分配新 Page，旧 Page 不变。这意味着：

- **操作时不需要修改 BTree**：WriteBuffer 暂存即可，COW 延迟到 Apply 阶段
- **不需要"撤销"BTree 修改**：Apply 前的崩溃 = BTree 完全未被修改（因为 COW 还没发生）
- **oldValue 天然可得**：WriteBuffer 记录 first-put 时的 BTree 当前值

### 5.2 与 Phase 3.2 的协同

Phase 3.2 已实现 commitTS 后置（WAL Sync 后分配）和显式事务 WAL 类型。Phase 3.3 的 2PC 直接复用这些基础设施：

```
Phase 3.2 提供:
  ✅ WALTypeTxBegin/TxWrite/TxCommit/TxRollback
  ✅ WriteEntries() + CommitEntry() 两阶段 WAL 写入
  ✅ Recovery 识别新类型

Phase 3.3 在此基础上:
  + TxPrepare Entry (包含 oldValue 快照)
  + Recovery Rollback 路径 (基于 WAL oldValue)
  = 完整的跨 key 原子性
```

### 5.3 不选 UndoLog 的原因

| 如果 NexKV 使用 UndoLog | 代价 |
|------------------------|------|
| 每次 Put 写入磁盘 Undo | 操作延迟增加（磁盘 I/O 在热路径） |
| UndoLog 持续膨胀 | 长时间事务占用大量磁盘 |
| 与 COW 语义冲突 | UndoLog 假设原地修改，COW 是延迟修改 |
| 双日志维护成本 | UndoLog + WAL = 两套日志系统 |

---

## 六、总结

```mermaid
flowchart TB
    subgraph Summary["两种方案总结"]
        direction LR
        
        subgraph L["Lealone UndoLog"]
            L1["适合: 原地修改 BTree"]
            L2["成本: 操作时磁盘写入"]
            L3["优势: 实现简单，回滚快"]
            L4["局限: 单节点，2PC 已废弃"]
        end
        
        subgraph N["NexKV WAL-based 2PC"]
            N1["适合: COW 延迟修改"]
            N2["成本: Prepare 时一次性 WAL 写"]
            N3["优势: 操作时零磁盘 I/O"]
            N4["扩展: 可演进为分布式 2PC"]
        end
    end
```

| | Lealone | NexKV |
|---|---------|-------|
| **原子性保证** | UndoLog (磁盘) | WAL TxPrepare (磁盘) |
| **回滚机制** | UndoLog.commit() 标记 + MVCC 隔离 | WAL oldValue 快照 + rollbackApplied |
| **最大优势** | 简单可靠，10 年验证 | 操作时零 I/O，COW 天然适配 |
| **最大风险** | UndoLog 膨胀 | Prepare 单点写入瓶颈 |
| **2PC 支持** | ❌ 已废弃 | 🔄 Phase 3.3 实现中 |

---

**文档版本**：v1.0
**创建日期**：2026-05-23
**参考**：
- Lealone AOTE: `lealone-aote/src/main/java/com/lealone/transaction/aote/`
- NexKV Phase 3.3: `docs/07_spike/btree-refactor/2026-05-23-phase3-mvcc-remaining-gaps-spike.md` §3.3
- NexKV WAL: `internal/infrastructure/storage/wal/`
- NexKV MVCC: `internal/infrastructure/storage/mvcc/`
